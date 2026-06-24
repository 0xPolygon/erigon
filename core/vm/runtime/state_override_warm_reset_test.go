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

func TestEthCallStateDiffOverride_WarmResetSSTORE(t *testing.T) {
	t.Parallel()

	// Warm slot 1, SSTORE slot 1 := 2, and return the gas delta around SSTORE.
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

	// stateDiff must be visible as the original value, or SSTORE takes the cheap dirty path.
	const sstoreResetWarm = 2900 // EIP-2200 reset (warm): SstoreResetGas - ColdSloadCost.
	const sstoreDirty = 100      // EIP-2200 dirty update: WarmStorageReadCost.
	const sandwich = 3 + 3 + 2   // PUSH1 + PUSH1 + GAS around the inner SSTORE.

	measure := func(seed func(s *state.IntraBlockState)) uint64 {
		db := testTemporalDB(t)
		tx, domains := testTemporalTxSD(t, db)
		s := state.New(state.NewReaderV3(domains.AsGetter(tx)))
		s.SetCode(addr, code)
		seed(s)
		ret, _, err := Call(addr, nil, &Config{State: s})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if len(ret) != 32 {
			t.Fatalf("ret len: %d", len(ret))
		}
		return binary.BigEndian.Uint64(ret[24:])
	}

	t.Run("broken/raw SetState matches Erigon pre-fix", func(t *testing.T) {
		got := measure(func(s *state.IntraBlockState) {
			if err := s.SetState(addr, slot, one); err != nil {
				t.Fatalf("SetState: %v", err)
			}
		})
		if want := uint64(sstoreDirty + sandwich); got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("fixed/SetStateOverride matches Bor/geth", func(t *testing.T) {
		got := measure(func(s *state.IntraBlockState) {
			if err := s.SetStateOverride(addr, slot, one); err != nil {
				t.Fatalf("SetStateOverride: %v", err)
			}
		})
		if want := uint64(sstoreResetWarm + sandwich); got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("delta is the EIP-2200 reset/dirty spread", func(t *testing.T) {
		broken := measure(func(s *state.IntraBlockState) { _ = s.SetState(addr, slot, one) })
		fixed := measure(func(s *state.IntraBlockState) { _ = s.SetStateOverride(addr, slot, one) })
		if fixed-broken != sstoreResetWarm-sstoreDirty {
			t.Fatalf("spread %d, want %d (broken=%d fixed=%d)",
				fixed-broken, sstoreResetWarm-sstoreDirty, broken, fixed)
		}
	})
}

// Regression: stateDiff must win over a replay-dirtied slot.
// eth_callMany applies overrides after replay, and FinalizeTx leaves dirtyStorage in memory.
func TestEthCallStateDiffOverride_BeatsReplayDirty(t *testing.T) {
	t.Parallel()

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

	if err := s.SetState(addr, slot, replay); err != nil {
		t.Fatalf("seed replay: %v", err)
	}
	if err := s.SetStateOverride(addr, slot, override); err != nil {
		t.Fatalf("SetStateOverride: %v", err)
	}

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
		t.Fatalf("GetState: got %s, want %s", got.Hex(), override.Hex())
	}

	ret, _, err := Call(addr, nil, &Config{State: s})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(ret) != 32 {
		t.Fatalf("ret len: %d", len(ret))
	}
	var sload uint256.Int
	sload.SetBytes(ret)
	if sload.Cmp(&override) != 0 {
		t.Fatalf("SLOAD: got %s, want %s", sload.Hex(), override.Hex())
	}
}
