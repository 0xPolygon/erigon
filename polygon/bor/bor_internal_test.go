// Copyright 2024 The Erigon Authors
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

package bor

import (
	"context"
	"math/big"
	"testing"
	"time"

	lru "github.com/hashicorp/golang-lru/arc/v2"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/crypto"
	"github.com/erigontech/erigon/execution/consensus"
	"github.com/erigontech/erigon/execution/types"
	"github.com/erigontech/erigon/polygon/bor/borcfg"
	"github.com/erigontech/erigon/polygon/bor/statefull"
	polychain "github.com/erigontech/erigon/polygon/chain"
	"github.com/erigontech/erigon/polygon/heimdall"
)

var _ bridgeReader = mockBridgeReader{}

type mockBridgeReader struct{}

func (m mockBridgeReader) Events(context.Context, common.Hash, uint64) ([]*types.Message, error) {
	panic("mock")
}

func (m mockBridgeReader) EventsWithinTime(context.Context, time.Time, time.Time) ([]*types.Message, error) {
	panic("mock")
}

var _ spanReader = mockSpanReader{}

type mockSpanReader struct{}

func (m mockSpanReader) Span(context.Context, uint64) (*heimdall.Span, bool, error) {
	panic("mock")
}

func (m mockSpanReader) Producers(context.Context, uint64) (*heimdall.ValidatorSet, error) {
	panic("mock")
}

func TestCommitStatesIndore(t *testing.T) {
	ctrl := gomock.NewController(t)
	cr := consensus.NewMockChainReader(ctrl)
	br := NewMockbridgeReader(ctrl)

	bor := New(polychain.BorDevnet.Config, nil, nil, nil, nil, br, nil)

	header := &types.Header{
		Number: big.NewInt(112),
		Time:   1744000028,
	}

	contractAddr := common.HexToAddress("a1")

	cr.EXPECT().GetHeaderByNumber(uint64(96)).Return(&types.Header{
		Number: big.NewInt(96),
		Time:   1744000000,
	})
	br.EXPECT().EventsWithinTime(gomock.Any(), time.Unix(1744000000-128, 0), time.Unix(1744000028-128, 0)).Return(
		[]*types.Message{
			types.NewMessage(
				common.HexToAddress(""),
				&contractAddr,
				0,
				uint256.NewInt(0),
				0,
				nil,
				nil,
				nil,
				nil,
				nil,
				false, // checkNonce
				false, // checkGas
				false, // isFree
				nil,
			),
		}, nil,
	)

	called := 0

	syscall := func(contract common.Address, data []byte) ([]byte, error) {
		require.Equal(t, contract, contractAddr)
		called++

		return nil, nil
	}

	_, err := bor.CommitStates(header, statefull.ChainContext{Chain: cr}, syscall, true)
	require.NoError(t, err)
	require.Equal(t, 1, called)
}

// fixedSuccession implements ValidateHeaderTimeSignerSuccessionNumber for tests.
type fixedSuccession struct{ n int }

func (f fixedSuccession) GetSignerSuccessionNumber(common.Address, uint64, *borcfg.BorConfig) (int, error) {
	return f.n, nil
}

// signTestHeader creates a header with a valid bor signature in Extra.
func signTestHeader(t *testing.T, header *types.Header, config *borcfg.BorConfig) common.Address {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	sealHash := SealHash(header, config)
	sig, err := crypto.Sign(sealHash[:], key)
	require.NoError(t, err)
	// extra must be at least ExtraSealLength (65) bytes; put sig at the end
	if len(header.Extra) < types.ExtraSealLength {
		header.Extra = make([]byte, types.ExtraSealLength)
	}
	copy(header.Extra[len(header.Extra)-types.ExtraSealLength:], sig)
	return crypto.PubkeyToAddress(key.PublicKey)
}

// newTestBorConfig returns a minimal BorConfig active from block 0 with
// period=2, using the provided HF block numbers (nil = fork disabled).
func newTestBorConfig(bhilaiBlock, giuglianoBlock *big.Int) *borcfg.BorConfig {
	return &borcfg.BorConfig{
		Period:           map[string]uint64{"0": 2},
		ProducerDelay:    map[string]uint64{"0": 1},
		Sprint:           map[string]uint64{"0": 16},
		BackupMultiplier: map[string]uint64{"0": 2},
		BhilaiBlock:      bhilaiBlock,
		GiuglianoBlock:   giuglianoBlock,
	}
}

func newTestSigCache(t *testing.T) *lru.ARCCache[common.Hash, common.Address] {
	t.Helper()
	cache, err := lru.NewARC[common.Hash, common.Address](16)
	require.NoError(t, err)
	return cache
}

// TestValidateHeaderTime_PreBhilai verifies the strict "no future blocks" rule
// that applies before the Bhilai hard fork.
func TestValidateHeaderTime_PreBhilai(t *testing.T) {
	config := newTestBorConfig(nil, nil) // no HFs enabled
	now := time.Now()

	tests := []struct {
		name     string
		headerTs uint64
		wantErr  bool
	}{
		{"at now", uint64(now.Unix()), false},
		{"1s past", uint64(now.Unix()) - 1, false},
		{"1s future", uint64(now.Unix()) + 1, true},
		{"30s future", uint64(now.Unix()) + 30, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := &types.Header{
				Number: big.NewInt(1),
				Time:   tc.headerTs,
			}
			err := ValidateHeaderTime(header, now, nil, fixedSuccession{0}, config, newTestSigCache(t))
			if tc.wantErr {
				assert.ErrorIs(t, err, consensus.ErrFutureBlock, "expected ErrFutureBlock")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateHeaderTime_Bhilai verifies that a one-period early-announcement
// buffer is allowed and that non-primary producers are rejected when their
// block is early (succession check).
func TestValidateHeaderTime_Bhilai(t *testing.T) {
	config := newTestBorConfig(big.NewInt(0), nil) // Bhilai from block 0, no Giugliano
	period := config.CalculatePeriod(1)            // 2s
	now := time.Now()
	nowTs := uint64(now.Unix())

	t.Run("within period buffer", func(t *testing.T) {
		header := &types.Header{Number: big.NewInt(1), Time: nowTs + period}
		err := ValidateHeaderTime(header, now, nil, fixedSuccession{0}, config, newTestSigCache(t))
		assert.NoError(t, err)
	})

	t.Run("exceeds period buffer", func(t *testing.T) {
		header := &types.Header{Number: big.NewInt(1), Time: nowTs + period + 1}
		err := ValidateHeaderTime(header, now, nil, fixedSuccession{0}, config, newTestSigCache(t))
		assert.ErrorIs(t, err, consensus.ErrFutureBlock)
	})

	// Succession check: Bhilai rejects early blocks from non-primary producers.
	// We need a signed header and a parent so that Ecrecover runs.
	t.Run("non-primary producer early block rejected", func(t *testing.T) {
		cache := newTestSigCache(t)
		header := &types.Header{
			Number:     big.NewInt(1),
			Time:       nowTs + 1, // header is 1s in the future
			Difficulty: big.NewInt(1),
			Extra:      make([]byte, types.ExtraSealLength),
		}
		addr := signTestHeader(t, header, config)
		// inject the signer directly into the cache to bypass Ecrecover's hash dependency
		cache.Add(header.Hash(), addr)
		parent := &types.Header{Number: big.NewInt(0), Time: nowTs - 2}
		err := ValidateHeaderTime(header, now, parent, fixedSuccession{1}, config, cache)
		assert.ErrorIs(t, err, consensus.ErrFutureBlock)
	})

	// Succession check: Bhilai does not fire ErrFutureBlock when header.Time == now,
	// even for non-primary producers. A different error (BlockTooSoonError) may still
	// fire, but that is a separate check unrelated to the future-block gate.
	t.Run("non-primary producer at now: no ErrFutureBlock", func(t *testing.T) {
		cache := newTestSigCache(t)
		header := &types.Header{
			Number:     big.NewInt(1),
			Time:       nowTs,
			Difficulty: big.NewInt(1),
			Extra:      make([]byte, types.ExtraSealLength),
		}
		addr := signTestHeader(t, header, config)
		cache.Add(header.Hash(), addr)
		parent := &types.Header{Number: big.NewInt(0), Time: nowTs - 2}
		err := ValidateHeaderTime(header, now, parent, fixedSuccession{1}, config, cache)
		assert.NotErrorIs(t, err, consensus.ErrFutureBlock)
	})
}

// TestValidateHeaderTime_Giugliano verifies:
//   - 30-second future cap replaces the per-period buffer
//   - announcement before parent block time is rejected
//   - non-primary producers are no longer rejected for early blocks
func TestValidateHeaderTime_Giugliano(t *testing.T) {
	config := newTestBorConfig(big.NewInt(0), big.NewInt(0)) // both HFs from block 0
	now := time.Now()
	nowTs := uint64(now.Unix())

	t.Run("29s future accepted", func(t *testing.T) {
		header := &types.Header{Number: big.NewInt(1), Time: nowTs + 29}
		err := ValidateHeaderTime(header, now, nil, fixedSuccession{0}, config, newTestSigCache(t))
		assert.NoError(t, err)
	})

	t.Run("exactly 30s future accepted", func(t *testing.T) {
		header := &types.Header{Number: big.NewInt(1), Time: nowTs + 30}
		err := ValidateHeaderTime(header, now, nil, fixedSuccession{0}, config, newTestSigCache(t))
		assert.NoError(t, err)
	})

	t.Run("31s future rejected", func(t *testing.T) {
		header := &types.Header{Number: big.NewInt(1), Time: nowTs + 31}
		err := ValidateHeaderTime(header, now, nil, fixedSuccession{0}, config, newTestSigCache(t))
		assert.ErrorIs(t, err, consensus.ErrFutureBlock)
	})

	t.Run("announcement before parent time rejected", func(t *testing.T) {
		// parent.Time is 1s after now: announcement arrives before parent was produced
		parent := &types.Header{Number: big.NewInt(0), Time: nowTs + 1}
		header := &types.Header{Number: big.NewInt(1), Time: nowTs + 5}
		err := ValidateHeaderTime(header, now, parent, fixedSuccession{0}, config, newTestSigCache(t))
		assert.ErrorIs(t, err, consensus.ErrFutureBlock)
	})

	// Giugliano lifts the Bhilai restriction: non-primary producers may announce
	// early within the 30s cap.
	t.Run("non-primary producer early block accepted", func(t *testing.T) {
		cache := newTestSigCache(t)
		header := &types.Header{
			Number:     big.NewInt(1),
			Time:       nowTs + 10, // early but within 30s cap
			Difficulty: big.NewInt(1),
			Extra:      make([]byte, types.ExtraSealLength),
		}
		addr := signTestHeader(t, header, config)
		cache.Add(header.Hash(), addr)
		parent := &types.Header{Number: big.NewInt(0), Time: nowTs - 2}
		err := ValidateHeaderTime(header, now, parent, fixedSuccession{1}, config, cache)
		assert.NoError(t, err)
	})
}
