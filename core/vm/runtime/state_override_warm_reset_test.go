// Copyright 2026 The Erigon Authors
// This file is part of Erigon.
//
// Erigon is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// Erigon is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with Erigon. If not, see <http://www.gnu.org/licenses/>.

package runtime

import (
	"encoding/binary"
	"testing"

	"github.com/holiman/uint256"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon/core/state"
	"github.com/erigontech/erigon/core/vm"
)

// TestEthCallStateDiffOverride_WarmResetSSTORE reproduces the warm-reset
// case from pos-e2e tests/execution-specs/evm/pip88-erigon-sload-sstore-chicago-gas.bats
// in-process, contrasting the broken eth_call stateDiff path (raw SetState)
// against the fixed path (SetStateOverride).
//
// The bytecode warms slot 1 with an SLOAD, then SSTOREs slot 1 := 2 and
// returns gas-before minus gas-after. EIP-2200 inspects both GetState (current)
// and GetCommittedState (original):
//   - Broken: only dirtyStorage carries the override, so original=0, current=1.
//     The "dirty update" branch fires → SSTORE costs WarmStorageReadCostEIP2929 (100).
//   - Fixed: originStorage also carries the override, so original=current=1,
//     value=2. The "reset existing slot" branch fires →
//     SSTORE costs SstoreResetGasEIP2200 - ColdSloadCostEIP2929 (2900).
//
// Inner sandwich (PUSH PUSH SSTORE GAS) adds 3+3+X+2 around SSTORE cost X.
// Bor reports the reset value because override.go calls statedb.Finalise after
// applying the diff, which promotes dirty → pending storage and makes
// GetCommittedState return it. Erigon's IntraBlockState has no equivalent
// public path, so we seed originStorage directly.
func TestEthCallStateDiffOverride_WarmResetSSTORE(t *testing.T) {
	t.Parallel()

	// 60 01     PUSH1 0x01
	// 54        SLOAD            warms slot 1 into the access list
	// 50        POP
	// 5a        GAS              push gas_before
	// 60 02     PUSH1 0x02
	// 60 01     PUSH1 0x01
	// 55        SSTORE           slot 1 := 2
	// 5a        GAS              push gas_after
	// 90        SWAP1
	// 03        SUB              consumed = gas_before - gas_after
	// 60 00     PUSH1 0x00
	// 52        MSTORE           mem[0:32] = consumed
	// 60 20     PUSH1 0x20
	// 60 00     PUSH1 0x00
	// f3       RETURN           return mem[0:32]
	code := []byte{
		byte(vm.PUSH1), 0x01,
		byte(vm.SLOAD),
		byte(vm.POP),
		byte(vm.GAS),
		byte(vm.PUSH1), 0x02,
		byte(vm.PUSH1), 0x01,
		byte(vm.SSTORE),
		byte(vm.GAS),
		byte(vm.SWAP1),
		byte(vm.SUB),
		byte(vm.PUSH1), 0x00,
		byte(vm.MSTORE),
		byte(vm.PUSH1), 0x20,
		byte(vm.PUSH1), 0x00,
		byte(vm.RETURN),
	}

	addr := common.HexToAddress("0xaa")
	slot := common.BigToHash(uint256.NewInt(1).ToBig())
	one := *uint256.NewInt(1)

	// EIP-2200 reset path: SstoreResetGasEIP2200 - ColdSloadCostEIP2929 = 5000 - 2100.
	const sstoreResetWarm = 2900
	// EIP-2200 dirty update branch: WarmStorageReadCostEIP2929.
	const sstoreDirty = 100
	// PUSH1 (3) + PUSH1 (3) + GAS (2) framing around the inner SSTORE.
	const sandwich = 3 + 3 + 2

	measure := func(seed func(s *state.IntraBlockState)) uint64 {
		db := testTemporalDB(t)
		tx, domains := testTemporalTxSD(t, db)
		s := state.New(state.NewReaderV3(domains.AsGetter(tx)))
		s.SetCode(addr, code)
		seed(s)
		ret, _, err := Call(addr, nil, &Config{State: s})
		if err != nil {
			t.Fatalf("Call failed: %v", err)
		}
		if len(ret) != 32 {
			t.Fatalf("expected 32-byte return, got %d bytes", len(ret))
		}
		// Return value is a 32-byte big-endian uint; consumed gas comfortably fits in 64 bits.
		return binary.BigEndian.Uint64(ret[24:])
	}

	t.Run("broken/raw SetState matches Erigon pre-fix", func(t *testing.T) {
		got := measure(func(s *state.IntraBlockState) {
			if err := s.SetState(addr, slot, one); err != nil {
				t.Fatalf("SetState: %v", err)
			}
		})
		want := uint64(sstoreDirty + sandwich)
		if got != want {
			t.Fatalf("dirty-branch gas: got %d, want %d", got, want)
		}
	})

	t.Run("fixed/SetStateOverride matches Bor/geth", func(t *testing.T) {
		got := measure(func(s *state.IntraBlockState) {
			if err := s.SetStateOverride(addr, slot, one); err != nil {
				t.Fatalf("SetStateOverride: %v", err)
			}
		})
		want := uint64(sstoreResetWarm + sandwich)
		if got != want {
			t.Fatalf("reset-branch gas: got %d, want %d", got, want)
		}
	})

	t.Run("delta is the EIP-2200 reset/dirty spread", func(t *testing.T) {
		broken := measure(func(s *state.IntraBlockState) {
			_ = s.SetState(addr, slot, one)
		})
		fixed := measure(func(s *state.IntraBlockState) {
			_ = s.SetStateOverride(addr, slot, one)
		})
		if fixed-broken != sstoreResetWarm-sstoreDirty {
			t.Fatalf("expected reset-vs-dirty spread %d, got %d (broken=%d fixed=%d)",
				sstoreResetWarm-sstoreDirty, fixed-broken, broken, fixed)
		}
	})
}

// TestEthCallStateDiffOverride_BeatsReplayDirty pins the eth_callMany
// behavior: when an override is applied after a replayed transaction has
// already written the same slot, the override must win for both reads.
// FinalizeTx writes dirtyStorage through to the writer but does not clear
// it, so without an explicit dirty-clear in setCommittedStorage the replay
// value would still shadow GetState — only GetCommittedState would change.
func TestEthCallStateDiffOverride_BeatsReplayDirty(t *testing.T) {
	t.Parallel()

	// SLOAD slot 1, return the 32-byte value.
	code := []byte{
		byte(vm.PUSH1), 0x01,
		byte(vm.SLOAD),
		byte(vm.PUSH1), 0x00,
		byte(vm.MSTORE),
		byte(vm.PUSH1), 0x20,
		byte(vm.PUSH1), 0x00,
		byte(vm.RETURN),
	}
	addr := common.HexToAddress("0xaa")
	slot := common.BigToHash(uint256.NewInt(1).ToBig())
	replay := *uint256.NewInt(0xdead)
	override := *uint256.NewInt(0x42)

	db := testTemporalDB(t)
	tx, domains := testTemporalTxSD(t, db)
	s := state.New(state.NewReaderV3(domains.AsGetter(tx)))
	s.SetCode(addr, code)

	// Simulate a replayed tx writing slot 1.
	if err := s.SetState(addr, slot, replay); err != nil {
		t.Fatalf("seed replay: %v", err)
	}
	// stateOverride applied after replay.
	if err := s.SetStateOverride(addr, slot, override); err != nil {
		t.Fatalf("SetStateOverride: %v", err)
	}

	// Direct read: GetCommittedState and GetState must both see the override.
	var got uint256.Int
	if err := s.GetCommittedState(addr, slot, &got); err != nil {
		t.Fatalf("GetCommittedState: %v", err)
	}
	if got.Cmp(&override) != 0 {
		t.Fatalf("GetCommittedState: got %s, want %s", got.Hex(), override.Hex())
	}
	if err := s.GetState(addr, slot, &got); err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if got.Cmp(&override) != 0 {
		t.Fatalf("GetState: got %s, want %s (replay leaked through dirtyStorage)", got.Hex(), override.Hex())
	}

	// EVM-level read via SLOAD must match too.
	ret, _, err := Call(addr, nil, &Config{State: s})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(ret) != 32 {
		t.Fatalf("expected 32-byte return, got %d", len(ret))
	}
	var sload uint256.Int
	sload.SetBytes(ret)
	if sload.Cmp(&override) != 0 {
		t.Fatalf("SLOAD: got %s, want %s (replay leaked through dirtyStorage)", sload.Hex(), override.Hex())
	}
}
