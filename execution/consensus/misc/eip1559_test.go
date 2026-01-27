// Copyright 2021 The go-ethereum Authors
// (original work)
// Copyright 2024 The Erigon Authors
// (modifications)
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

package misc

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/jinzhu/copier"
	"github.com/stretchr/testify/require"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon/execution/chain"
	"github.com/erigontech/erigon/execution/chain/params"
	"github.com/erigontech/erigon/execution/types"
	"github.com/erigontech/erigon/polygon/bor/borcfg"
)

// copyConfig does a _shallow_ copy of a given config. Safe to set new values, but
// do not use e.g. SetInt() on the numbers. For testing only
func copyConfig(original *chain.Config) *chain.Config {
	var copy chain.Config
	copier.Copy(&copy, original)
	return &copy
}

func config() *chain.Config {
	config := copyConfig(chain.TestChainConfig)
	config.LondonBlock = big.NewInt(5)
	return config
}

func borConfig() *chain.Config {
	return copyConfig(chain.TestChainBorConfig)
}

// TestBlockGasLimits tests the gasLimit checks for blocks both across
// the EIP-1559 boundary and post-1559 blocks
func TestBlockGasLimits(t *testing.T) {
	initial := new(big.Int).SetUint64(params.InitialBaseFee)

	for i, tc := range []struct {
		pGasLimit uint64
		pNum      int64
		gasLimit  uint64
		ok        bool
	}{
		// Transitions from non-london to london
		{10000000, 4, 20000000, true},  // No change
		{10000000, 4, 20019530, true},  // Upper limit
		{10000000, 4, 20019531, false}, // Upper +1
		{10000000, 4, 19980470, true},  // Lower limit
		{10000000, 4, 19980469, false}, // Lower limit -1
		// London to London
		{20000000, 5, 20000000, true},
		{20000000, 5, 20019530, true},  // Upper limit
		{20000000, 5, 20019531, false}, // Upper limit +1
		{20000000, 5, 19980470, true},  // Lower limit
		{20000000, 5, 19980469, false}, // Lower limit -1
		{40000000, 5, 40039061, true},  // Upper limit
		{40000000, 5, 40039062, false}, // Upper limit +1
		{40000000, 5, 39960939, true},  // lower limit
		{40000000, 5, 39960938, false}, // Lower limit -1
	} {
		parent := &types.Header{
			GasUsed:  tc.pGasLimit / 2,
			GasLimit: tc.pGasLimit,
			BaseFee:  initial,
			Number:   big.NewInt(tc.pNum),
		}
		header := &types.Header{
			GasUsed:  tc.gasLimit / 2,
			GasLimit: tc.gasLimit,
			BaseFee:  initial,
			Number:   big.NewInt(tc.pNum + 1),
		}
		err := VerifyEip1559Header(config(), parent, header, false /*skipGasLimit*/)
		if tc.ok && err != nil {
			t.Errorf("test %d: Expected valid header: %s", i, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("test %d: Expected invalid header", i)
		}
	}
}

// TestCalcBaseFee assumes all blocks are 1559-blocks
func TestCalcBaseFee(t *testing.T) {
	tests := []struct {
		parentBaseFee   int64
		parentGasLimit  uint64
		parentGasUsed   uint64
		expectedBaseFee int64
	}{
		{params.InitialBaseFee, 20000000, 10000000, params.InitialBaseFee}, // usage == target
		{params.InitialBaseFee, 20000000, 9000000, 987500000},              // usage below target
		{params.InitialBaseFee, 20000000, 11000000, 1012500000},            // usage above target
	}
	for i, test := range tests {
		parent := &types.Header{
			Number:   common.Big32,
			GasLimit: test.parentGasLimit,
			GasUsed:  test.parentGasUsed,
			BaseFee:  big.NewInt(test.parentBaseFee),
		}
		if have, want := CalcBaseFee(config(), parent), big.NewInt(test.expectedBaseFee); have.Cmp(want) != 0 {
			t.Errorf("test %d: have %d  want %d, ", i, have, want)
		}
	}
}

func TestCalcParentGasTarget(t *testing.T) {
	t.Parallel()

	testConfig := borConfig()
	if borConfig, ok := testConfig.Bor.(*borcfg.BorConfig); ok {
		borConfig.DandeliBlock = big.NewInt(20)
	} else {
		t.Fatalf("Unable to load bor config for test")
	}
	defaultGasLimit := uint64(60_000_000)

	t.Run("gas target calculation pre dandeli HF", func(t *testing.T) {
		block := &types.Header{
			Number:   big.NewInt(9),
			GasLimit: defaultGasLimit,
			GasUsed:  defaultGasLimit / 2,
			BaseFee:  big.NewInt(params.InitialBaseFee),
		}
		gasTarget := calcParentGasTarget(testConfig.Bor, block)
		expected := block.GasLimit / 2 // because elasticity multiplier is set to 2 by default
		require.Equal(t, expected, gasTarget, "expected gas target = gaslimit/2")
	})

	t.Run("gas target calculation post dandeli HF", func(t *testing.T) {
		block := &types.Header{
			Number:   big.NewInt(20),
			GasLimit: defaultGasLimit,
			GasUsed:  defaultGasLimit / 2,
			BaseFee:  big.NewInt(params.InitialBaseFee),
		}
		gasTarget := calcParentGasTarget(testConfig.Bor, block)
		expected := block.GasLimit * params.TargetGasPercentagePostDandeli / 100 // because gas target is derived by this protocol parameter
		require.Equal(t, expected, gasTarget, "case #1: expected gas target = 60 percent of gas limit")

		block = &types.Header{
			Number:   big.NewInt(21),
			GasLimit: defaultGasLimit,
			GasUsed:  defaultGasLimit / 2,
			BaseFee:  big.NewInt(params.InitialBaseFee),
		}
		gasTarget = calcParentGasTarget(testConfig.Bor, block)
		expected = block.GasLimit * params.TargetGasPercentagePostDandeli / 100 // because gas target is derived by this protocol parameter
		require.Equal(t, expected, gasTarget, "case #2: expected gas target = 60 percent of gas limit")
	})

	t.Run("nil bor config", func(t *testing.T) {
		testConfig.Bor = nil
		block := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: defaultGasLimit,
			GasUsed:  defaultGasLimit / 2,
			BaseFee:  big.NewInt(params.InitialBaseFee),
		}
		gasTarget := calcParentGasTarget(testConfig.Bor, block)
		expected := block.GasLimit / 2 // because elasticity multiplier is set to 2 by default
		require.Equal(t, expected, gasTarget, "expected gas target = gaslimit/2 when bor config is nil")
	})
}

// simpleBaseFeeCalculator contains an overly simplified logic of base fee calculations useful for generating
// expected values in test cases. It assumes all blocks are post-bhilai HF.
func simpleBaseFeeCalculator(initialBaseFee int64, gasLimit, gasUsed uint64, targetGasPercentage uint64) uint64 {
	initial := big.NewInt(initialBaseFee)
	val := big.NewInt(1)
	val.Mul(val, initial)

	// Assuming tests are running post bhilai
	bfd := int64(params.BaseFeeChangeDenominatorPostBhilai)

	// Define a target gas based on given percentage
	target := gasLimit * targetGasPercentage / 100
	if gasUsed == target {
		return initial.Uint64()
	}

	// follow the simple formula to get the new base fee:
	// base fee = initialBaseFee +/- (initialBaseFee * gasUsedDelta / gasTarget / baseFeeChangeDenominator)

	var delta int64
	if gasUsed > target {
		delta = int64(gasUsed - target)
	} else {
		delta = int64(target - gasUsed)
	}

	val.Mul(val, big.NewInt(delta))
	val.Div(val, big.NewInt(int64(bfd)))
	val.Div(val, big.NewInt(int64(target)))

	if gasUsed > target {
		return initial.Add(initial, val).Uint64()
	} else {
		return initial.Sub(initial, val).Uint64()
	}
}

func TestCalcBaseFeeDandeli(t *testing.T) {
	t.Parallel()

	testConfig := borConfig()
	if borConfig, ok := testConfig.Bor.(*borcfg.BorConfig); ok {
		borConfig.DandeliBlock = big.NewInt(20)
	} else {
		t.Fatalf("Unable to load bor config for test")
	}

	// Case 1: Create pre-dandeli cases where HF is defined in future. Validate
	// base fee calculations before HF kicks in. Base fee should be calculated
	// based on default elasticity multiplier.
	tests := []struct {
		name            string
		parentBaseFee   int64
		parentGasLimit  uint64
		parentGasUsed   uint64
		expectedBaseFee uint64
	}{
		{"usage == target", params.InitialBaseFee, 60_000_000, 30_000_000, params.InitialBaseFee},
		{"usage below target #1", params.InitialBaseFee, 60_000_000, 20_000_000, 994791667},
		{"usage below target #2", params.InitialBaseFee, 60_000_000, 10_000_000, 989583334},
		{"usage above target #1", params.InitialBaseFee, 60_000_000, 40_000_000, 1005208333},
		{"usage above target #2", params.InitialBaseFee, 60_000_000, 50_000_000, 1010416666},
		{"usage full", params.InitialBaseFee, 60_000_000, 60_000_000, 1015625000},
		{"usage 0", params.InitialBaseFee, 60_000_000, 0, 984375000},
	}
	for _, test := range tests {
		block := &types.Header{
			Number:   big.NewInt(8),
			GasLimit: test.parentGasLimit,
			GasUsed:  test.parentGasUsed,
			BaseFee:  big.NewInt(test.parentBaseFee),
		}
		baseFee := CalcBaseFee(testConfig, block).Uint64()
		expectedBaseFee := simpleBaseFeeCalculator(block.BaseFee.Int64(), block.GasLimit, block.GasUsed, params.DefaultTargetGasPercentage)
		require.Equal(
			t,
			expectedBaseFee,
			baseFee,
			fmt.Sprintf("pre-dandeli base fee mismatch with expected value, test: %s, got: %d, want: %d", test.name, baseFee, expectedBaseFee),
		)
		// Also check with manually calculated base fee
		require.Equal(
			t,
			test.expectedBaseFee,
			baseFee,
			fmt.Sprintf("pre-dandeli base fee mismatch with manually calculated value, test: %s, got: %d, want: %d", test.name, baseFee, expectedBaseFee),
		)
	}

	// Case 2: Create post-dandeli cases where HF has kicked in. Validate base fee changes
	// based on the newly introduced protocol param: TargetGasPrecentage. Target gas limit
	// should be calculated based on this percentage value out of total gas limit. Base
	// fee should be changed accordingly.
	tests = []struct {
		name            string
		parentBaseFee   int64
		parentGasLimit  uint64
		parentGasUsed   uint64
		expectedBaseFee uint64
	}{
		{"usage == target (65%)", params.InitialBaseFee, 60_000_000, 39_000_000, params.InitialBaseFee},
		{"usage below target #1", params.InitialBaseFee, 60_000_000, 30_000_000, 996394231},
		{"usage below target #2", params.InitialBaseFee, 60_000_000, 10_000_000, 988381411},
		{"usage above target #1", params.InitialBaseFee, 60_000_000, 40_000_000, 1000400641},
		{"usage above target #2", params.InitialBaseFee, 60_000_000, 50_000_000, 1004407051},
		{"usage full", params.InitialBaseFee, 60_000_000, 60_000_000, 1008413461},
		{"usage 0", params.InitialBaseFee, 60_000_000, 0, 984375000},
	}
	for _, test := range tests {
		// Post-dandeli block #1
		block := &types.Header{
			Number:   big.NewInt(20),
			GasLimit: test.parentGasLimit,
			GasUsed:  test.parentGasUsed,
			BaseFee:  big.NewInt(test.parentBaseFee),
		}
		baseFee := CalcBaseFee(testConfig, block).Uint64()
		expectedBaseFee := simpleBaseFeeCalculator(block.BaseFee.Int64(), block.GasLimit, block.GasUsed, params.TargetGasPercentagePostDandeli)
		require.Equal(
			t,
			expectedBaseFee,
			baseFee,
			fmt.Sprintf("post-dandeli #1: base fee mismatch with expected value, test: %s, got: %d, want: %d", test.name, baseFee, expectedBaseFee),
		)
		// Also check with manually calculated base fee
		require.Equal(
			t,
			test.expectedBaseFee,
			baseFee,
			fmt.Sprintf("post-dandeli #1: base fee mismatch with manually calculated value, test: %s, got: %d, want: %d", test.name, baseFee, expectedBaseFee),
		)

		// Post-dandeli block #2
		block = &types.Header{
			Number:   big.NewInt(21),
			GasLimit: test.parentGasLimit,
			GasUsed:  test.parentGasUsed,
			BaseFee:  big.NewInt(test.parentBaseFee),
		}
		baseFee = CalcBaseFee(testConfig, block).Uint64()
		expectedBaseFee = simpleBaseFeeCalculator(block.BaseFee.Int64(), block.GasLimit, block.GasUsed, params.TargetGasPercentagePostDandeli)
		require.Equal(
			t,
			expectedBaseFee,
			baseFee,
			fmt.Sprintf("post-dandeli #2: base fee mismatch with expected value, test: %s, got: %d, want: %d", test.name, baseFee, expectedBaseFee),
		)
		// Also check with manually calculated base fee
		require.Equal(
			t,
			test.expectedBaseFee,
			baseFee,
			fmt.Sprintf("post-dandeli #2: base fee mismatch with manually calculated value, test: %s, got: %d, want: %d", test.name, baseFee, expectedBaseFee),
		)
	}
}

// TestVerifyEip1559HeaderBaseFeeWithinBoundaries tests that post-Dandeli blocks enforce
// a 5% boundary check on base fee changes to prevent excessive volatility
func TestVerifyEip1559HeaderBaseFeeWithinBoundaries(t *testing.T) {
	t.Parallel()

	testConfig := borConfig()
	if borConfig, ok := testConfig.Bor.(*borcfg.BorConfig); ok {
		borConfig.DandeliBlock = big.NewInt(20)
	} else {
		t.Fatalf("Unable to load bor config for test")
	}

	parent := &types.Header{
		Number:   big.NewInt(20),
		GasLimit: 30_000_000,
		GasUsed:  15_000_000,
		BaseFee:  big.NewInt(1_000_000_000), // 1 gwei
	}

	// With parent base fee of 1_000_000_000 (1 gwei):
	// Max allowed change: 5% = 50_000_000
	// Max base fee: 1_050_000_000 (1.05 gwei)
	// Min base fee: 950_000_000 (0.95 gwei)

	t.Run("accepts base fee at upper boundary (exactly 5% increase)", func(t *testing.T) {
		header := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  big.NewInt(1_050_000_000), // Exactly 5% increase
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.NoError(t, err, "should accept base fee at exactly 5% increase")
	})

	t.Run("accepts base fee at lower boundary (exactly 5% decrease)", func(t *testing.T) {
		header := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  big.NewInt(950_000_000), // Exactly 5% decrease
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.NoError(t, err, "should accept base fee at exactly 5% decrease")
	})

	t.Run("accepts base fee within boundaries (3% increase)", func(t *testing.T) {
		header := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  big.NewInt(1_030_000_000), // 3% increase
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.NoError(t, err, "should accept base fee within boundaries")
	})

	t.Run("rejects base fee exceeding upper boundary (6% increase)", func(t *testing.T) {
		header := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  big.NewInt(1_060_000_000), // 6% increase - exceeds limit
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.Error(t, err, "should reject base fee exceeding 5% increase")
		require.Contains(t, err.Error(), "exceeds 5% limit", "error should mention 5% limit")
	})

	t.Run("rejects base fee exceeding lower boundary (6% decrease)", func(t *testing.T) {
		header := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  big.NewInt(940_000_000), // 6% decrease - exceeds limit
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.Error(t, err, "should reject base fee exceeding 5% decrease")
		require.Contains(t, err.Error(), "exceeds 5% limit", "error should mention 5% limit")
	})

	t.Run("rejects nil base fee", func(t *testing.T) {
		header := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  nil, // Nil base fee should still be rejected
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.Error(t, err, "should reject header with nil base fee")
		require.Contains(t, err.Error(), "baseFee", "error should mention baseFee")
	})

	t.Run("accepts unchanged base fee", func(t *testing.T) {
		header := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  big.NewInt(1_000_000_000), // Same as parent
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.NoError(t, err, "should accept unchanged base fee")
	})
}

// TestBaseFeeValidationPreDandeli tests that base fee validation still works before Dandeli HF
func TestBaseFeeValidationPreDandeli(t *testing.T) {
	t.Parallel()

	testConfig := borConfig()
	if borConfig, ok := testConfig.Bor.(*borcfg.BorConfig); ok {
		borConfig.DandeliBlock = big.NewInt(20)
	} else {
		t.Fatalf("Unable to load bor config for test")
	}

	parent := &types.Header{
		Number:   big.NewInt(10), // Pre-Dandeli
		GasLimit: 30_000_000,
		GasUsed:  15_000_000,
		BaseFee:  big.NewInt(1_000_000_000),
	}

	t.Run("pre-Dandeli: rejects incorrect base fee", func(t *testing.T) {
		calculatedBaseFee := CalcBaseFee(testConfig, parent)
		incorrectBaseFee := new(big.Int).Mul(calculatedBaseFee, big.NewInt(2))

		header := &types.Header{
			Number:   big.NewInt(11),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  incorrectBaseFee, // Wrong base fee
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.Error(t, err, "should reject incorrect base fee pre-Dandeli")
		require.Contains(t, err.Error(), "invalid baseFee", "error should mention invalid baseFee")
	})

	t.Run("pre-Dandeli: accepts correct base fee", func(t *testing.T) {
		calculatedBaseFee := CalcBaseFee(testConfig, parent)

		header := &types.Header{
			Number:   big.NewInt(11),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  calculatedBaseFee, // Correct base fee
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.NoError(t, err, "should accept correct base fee pre-Dandeli")
	})

	t.Run("post-Dandeli: accepts base fee within 5% boundary", func(t *testing.T) {
		parent := &types.Header{
			Number:   big.NewInt(20), // Post-Dandeli
			GasLimit: 30_000_000,
			GasUsed:  15_000_000,
			BaseFee:  big.NewInt(1_000_000_000),
		}

		// Use a base fee within 5% of parent (not calculated value)
		// Parent is 1_000_000_000, so 4% increase = 1_040_000_000
		baseFeeWithin5Percent := big.NewInt(1_040_000_000)

		header := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  baseFeeWithin5Percent, // 4% increase from parent
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.NoError(t, err, "should accept base fee within 5% boundary post-Dandeli")
	})

	t.Run("post-Dandeli: rejects base fee exceeding 5% boundary", func(t *testing.T) {
		parent := &types.Header{
			Number:   big.NewInt(20), // Post-Dandeli
			GasLimit: 30_000_000,
			GasUsed:  15_000_000,
			BaseFee:  big.NewInt(1_000_000_000),
		}

		// Use a base fee that exceeds 5% boundary
		// Parent is 1_000_000_000, so 10% increase = 1_100_000_000 (exceeds 5%)
		baseFeeExceeding5Percent := big.NewInt(1_100_000_000)

		header := &types.Header{
			Number:   big.NewInt(21),
			GasLimit: 30_000_000,
			GasUsed:  20_000_000,
			BaseFee:  baseFeeExceeding5Percent, // 10% increase - exceeds limit
		}

		err := VerifyEip1559Header(testConfig, parent, header, false /*skipGasLimit*/)
		require.Error(t, err, "should reject base fee exceeding 5% boundary post-Dandeli")
		require.Contains(t, err.Error(), "exceeds 5% limit", "error should mention 5% limit")
	})
}
