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

package heimdall

import (
	"testing"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon/polygon/bor/borcfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatorSetOverride_NilConfig(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	blockNumber := uint64(100)

	// Test with nil config
	assert.False(t, isAllowedByValidatorSetOverride(addr, blockNumber, nil))
}

func TestValidatorSetOverride_EmptyOverrides(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	blockNumber := uint64(100)

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{},
	}

	assert.False(t, isAllowedByValidatorSetOverride(addr, blockNumber, config))
}

func TestValidatorSetOverride_NilOverridesField(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	blockNumber := uint64(100)

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: nil,
	}

	assert.False(t, isAllowedByValidatorSetOverride(addr, blockNumber, config))
}

func TestValidatorSetOverride_ValidatorInRange(t *testing.T) {
	overrideValidator := common.HexToAddress("0x1234567890123456789012345678901234567890")
	blockNumber := uint64(100)

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 50,
				EndBlock:   150,
				Validators: []common.Address{overrideValidator},
			},
		},
	}

	// Validator should be allowed in range
	assert.True(t, isAllowedByValidatorSetOverride(overrideValidator, blockNumber, config))
}

func TestValidatorSetOverride_ValidatorNotInRange_Before(t *testing.T) {
	overrideValidator := common.HexToAddress("0x1234567890123456789012345678901234567890")
	blockNumber := uint64(40) // Before range

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 50,
				EndBlock:   150,
				Validators: []common.Address{overrideValidator},
			},
		},
	}

	assert.False(t, isAllowedByValidatorSetOverride(overrideValidator, blockNumber, config))
}

func TestValidatorSetOverride_ValidatorNotInRange_After(t *testing.T) {
	overrideValidator := common.HexToAddress("0x1234567890123456789012345678901234567890")
	blockNumber := uint64(200) // After range

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 50,
				EndBlock:   150,
				Validators: []common.Address{overrideValidator},
			},
		},
	}

	assert.False(t, isAllowedByValidatorSetOverride(overrideValidator, blockNumber, config))
}

func TestValidatorSetOverride_BoundaryConditions(t *testing.T) {
	overrideValidator := common.HexToAddress("0x1234567890123456789012345678901234567890")

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 100,
				EndBlock:   200,
				Validators: []common.Address{overrideValidator},
			},
		},
	}

	// Test start block boundary
	assert.True(t, isAllowedByValidatorSetOverride(overrideValidator, 100, config),
		"Validator should be allowed at StartBlock")

	// Test end block boundary
	assert.True(t, isAllowedByValidatorSetOverride(overrideValidator, 200, config),
		"Validator should be allowed at EndBlock")

	// Test one before start
	assert.False(t, isAllowedByValidatorSetOverride(overrideValidator, 99, config),
		"Validator should not be allowed before StartBlock")

	// Test one after end
	assert.False(t, isAllowedByValidatorSetOverride(overrideValidator, 201, config),
		"Validator should not be allowed after EndBlock")
}

func TestValidatorSetOverride_MultipleValidators(t *testing.T) {
	validator1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	validator2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	validator3 := common.HexToAddress("0x3333333333333333333333333333333333333333")

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 100,
				EndBlock:   200,
				Validators: []common.Address{validator1, validator2},
			},
		},
	}

	blockNumber := uint64(150)

	// Both validators should be allowed
	assert.True(t, isAllowedByValidatorSetOverride(validator1, blockNumber, config))
	assert.True(t, isAllowedByValidatorSetOverride(validator2, blockNumber, config))

	// Validator not in list should not be allowed
	assert.False(t, isAllowedByValidatorSetOverride(validator3, blockNumber, config))
}

func TestValidatorSetOverride_MultipleRanges(t *testing.T) {
	validator1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	validator2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 100,
				EndBlock:   200,
				Validators: []common.Address{validator1},
			},
			{
				StartBlock: 300,
				EndBlock:   400,
				Validators: []common.Address{validator2},
			},
		},
	}

	// validator1 in first range
	assert.True(t, isAllowedByValidatorSetOverride(validator1, 150, config))
	assert.False(t, isAllowedByValidatorSetOverride(validator1, 350, config))

	// validator2 in second range
	assert.False(t, isAllowedByValidatorSetOverride(validator2, 150, config))
	assert.True(t, isAllowedByValidatorSetOverride(validator2, 350, config))

	// Outside both ranges
	assert.False(t, isAllowedByValidatorSetOverride(validator1, 250, config))
	assert.False(t, isAllowedByValidatorSetOverride(validator2, 250, config))
}

func TestValidatorSetOverride_SingleBlockRange(t *testing.T) {
	validator := common.HexToAddress("0x1111111111111111111111111111111111111111")

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 100,
				EndBlock:   100, // Single block
				Validators: []common.Address{validator},
			},
		},
	}

	assert.True(t, isAllowedByValidatorSetOverride(validator, 100, config))
	assert.False(t, isAllowedByValidatorSetOverride(validator, 99, config))
	assert.False(t, isAllowedByValidatorSetOverride(validator, 101, config))
}

func TestGetSignerSuccessionNumber_WithOverride(t *testing.T) {
	// Create a normal validator set
	normalValidator1 := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	normalValidator2 := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	overrideValidator := common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")

	validators := []*Validator{
		NewValidator(normalValidator1, 10),
		NewValidator(normalValidator2, 10),
	}

	validatorSet := NewValidatorSet(validators)

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 100,
				EndBlock:   200,
				Validators: []common.Address{overrideValidator},
			},
		},
	}

	blockNumber := uint64(150)

	// Normal validators should work
	succession1, err := validatorSet.GetSignerSuccessionNumber(normalValidator1, blockNumber, config)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, succession1, 0)

	// Override validator should be allowed (though succession number logic may need review)
	succession2, err := validatorSet.GetSignerSuccessionNumber(overrideValidator, blockNumber, config)
	require.NoError(t, err)
	// Note: The succession number for override validators is currently calculated as len(validators)-1
	// This may need adjustment based on intended behavior
	assert.GreaterOrEqual(t, succession2, 0)

	// Outside override range, override validator should fail
	_, err = validatorSet.GetSignerSuccessionNumber(overrideValidator, 50, config)
	require.Error(t, err)
	assert.IsType(t, &UnauthorizedSignerError{}, err)
}

func TestGetSignerSuccessionNumber_WithoutOverride(t *testing.T) {
	normalValidator := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	unknownValidator := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")

	validators := []*Validator{
		NewValidator(normalValidator, 10),
	}

	validatorSet := NewValidatorSet(validators)

	// Config without overrides
	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{},
	}

	blockNumber := uint64(100)

	// Normal validator should work
	_, err := validatorSet.GetSignerSuccessionNumber(normalValidator, blockNumber, config)
	require.NoError(t, err)

	// Unknown validator should fail
	_, err = validatorSet.GetSignerSuccessionNumber(unknownValidator, blockNumber, config)
	require.Error(t, err)
	assert.IsType(t, &UnauthorizedSignerError{}, err)
}

func TestDifficulty_WithOverride(t *testing.T) {
	normalValidator := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	overrideValidator := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")

	validators := []*Validator{
		NewValidator(normalValidator, 10),
	}

	validatorSet := NewValidatorSet(validators)

	// Note: Difficulty() internally calls GetSignerSuccessionNumber with blockNumber = 0
	// So the override range must include block 0 for the override validator to work
	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 0,
				EndBlock:   1000,
				Validators: []common.Address{overrideValidator},
			},
		},
	}

	// Normal validator difficulty
	diff1, err := validatorSet.Difficulty(normalValidator, config)
	require.NoError(t, err)
	assert.Greater(t, diff1, uint64(0))

	// Override validator difficulty
	// Note: Since override validators have high succession numbers, they get low difficulty
	diff2, err := validatorSet.Difficulty(overrideValidator, config)
	require.NoError(t, err)
	assert.Greater(t, diff2, uint64(0))
}

func TestSafeDifficulty_WithOverride(t *testing.T) {
	normalValidator := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	overrideValidator := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	unknownValidator := common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")

	validators := []*Validator{
		NewValidator(normalValidator, 10),
	}

	validatorSet := NewValidatorSet(validators)

	// Note: SafeDifficulty() internally calls Difficulty() which calls GetSignerSuccessionNumber with blockNumber = 0
	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 0,
				EndBlock:   1000,
				Validators: []common.Address{overrideValidator},
			},
		},
	}

	// Normal validator
	diff1 := validatorSet.SafeDifficulty(normalValidator, config)
	assert.Greater(t, diff1, uint64(0))

	// Override validator
	diff2 := validatorSet.SafeDifficulty(overrideValidator, config)
	assert.Greater(t, diff2, uint64(0))

	// Unknown validator should return 0
	diff3 := validatorSet.SafeDifficulty(unknownValidator, config)
	assert.Equal(t, uint64(0), diff3)

	// Empty address should return 1
	emptyAddr := common.Address{}
	diff4 := validatorSet.SafeDifficulty(emptyAddr, config)
	assert.Equal(t, uint64(1), diff4)
}

func TestValidatorSetOverride_RealWorldScenario(t *testing.T) {
	// Simulate the actual mainnet override scenario
	overrideValidator := common.HexToAddress("0x41018795fa95783117242244303fd7e26e964ee8")

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 80440819,
				EndBlock:   80440834,
				Validators: []common.Address{overrideValidator},
			},
		},
	}

	// Test within range
	assert.True(t, isAllowedByValidatorSetOverride(overrideValidator, 80440819, config),
		"Should be allowed at start block")
	assert.True(t, isAllowedByValidatorSetOverride(overrideValidator, 80440826, config),
		"Should be allowed in middle of range")
	assert.True(t, isAllowedByValidatorSetOverride(overrideValidator, 80440834, config),
		"Should be allowed at end block")

	// Test outside range
	assert.False(t, isAllowedByValidatorSetOverride(overrideValidator, 80440818, config),
		"Should not be allowed before start block")
	assert.False(t, isAllowedByValidatorSetOverride(overrideValidator, 80440835, config),
		"Should not be allowed after end block")
}

func TestValidatorSetOverride_ConsecutiveRanges(t *testing.T) {
	validator := common.HexToAddress("0x1111111111111111111111111111111111111111")

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 100,
				EndBlock:   200,
				Validators: []common.Address{validator},
			},
			{
				StartBlock: 201, // Consecutive
				EndBlock:   300,
				Validators: []common.Address{validator},
			},
		},
	}

	// Should work in both ranges
	assert.True(t, isAllowedByValidatorSetOverride(validator, 100, config))
	assert.True(t, isAllowedByValidatorSetOverride(validator, 200, config))
	assert.True(t, isAllowedByValidatorSetOverride(validator, 201, config))
	assert.True(t, isAllowedByValidatorSetOverride(validator, 300, config))
}

func TestValidatorSetOverride_OverlappingRanges(t *testing.T) {
	// IMPORTANT: This tests documents the current behavior with overlapping ranges
	// The implementation returns false after checking the FIRST matching range
	// if the validator is not in that range's list. This means only the first
	// matching range is effective for overlapping ranges.
	validator1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	validator2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 100,
				EndBlock:   200,
				Validators: []common.Address{validator1},
			},
			{
				StartBlock: 150, // Overlapping
				EndBlock:   250,
				Validators: []common.Address{validator2},
			},
		},
	}

	// In the overlap region (150-200), the FIRST matching range takes precedence
	// validator1 is allowed because it's in the first range
	assert.True(t, isAllowedByValidatorSetOverride(validator1, 175, config))

	// validator2 is NOT allowed at 175, even though it's in a matching range,
	// because the first matching range doesn't include it and returns false
	assert.False(t, isAllowedByValidatorSetOverride(validator2, 175, config))

	// validator2 is only allowed after the first range ends
	assert.True(t, isAllowedByValidatorSetOverride(validator2, 225, config))
}

func TestValidatorSetOverride_SuccessionNumberBehavior(t *testing.T) {
	// IMPORTANT: This test documents the current succession number calculation
	// for override validators. Override validators are NOT in the normal validator
	// set, so they get signerIndex = -1, which affects their succession number.
	normalValidator := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	normalValidator2 := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	overrideValidator := common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")

	validators := []*Validator{
		NewValidator(normalValidator, 10),
		NewValidator(normalValidator2, 10),
	}
	validatorSet := NewValidatorSet(validators)

	config := &borcfg.BorConfig{
		OverrideValidatorSetInRange: []borcfg.BlockRangeOverrideValidatorSet{
			{
				StartBlock: 100,
				EndBlock:   200,
				Validators: []common.Address{overrideValidator},
			},
		},
	}

	blockNumber := uint64(150)

	// Normal validators work as expected
	succession1, err := validatorSet.GetSignerSuccessionNumber(normalValidator, blockNumber, config)
	require.NoError(t, err)
	t.Logf("Normal validator 1 succession: %d", succession1)
	// First validator (0xAAAA...) is the proposer (lexicographically smallest), so succession = 0
	assert.Equal(t, 0, succession1, "normalValidator should have succession number 0")

	succession2, err := validatorSet.GetSignerSuccessionNumber(normalValidator2, blockNumber, config)
	require.NoError(t, err)
	t.Logf("Normal validator 2 succession: %d", succession2)
	// Second validator is one position after the proposer, so succession = 1
	assert.Equal(t, 1, succession2, "normalValidator2 should have succession number 1")

	// Override validator is allowed but gets a calculated succession number
	// Since it's not in the validator set, signerIndex = -1
	// indexDiff = -1 - proposerIndex(0) = -1, then wraps: -1 + len(validators)(2) = 1
	overrideSuccession, err := validatorSet.GetSignerSuccessionNumber(overrideValidator, blockNumber, config)
	require.NoError(t, err)
	t.Logf("Override validator succession: %d (validator set size: %d)", overrideSuccession, len(validatorSet.Validators))
	assert.Equal(t, 1, overrideSuccession, "override validator should have succession number 1 (wrapped from -1)")
}
