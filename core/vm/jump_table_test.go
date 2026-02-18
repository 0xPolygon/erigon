// Copyright 2025 The Erigon Authors
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

package vm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLisovoAndLisovoProInstructionSetsAreIdentical verifies that the lisovo
// and lisovoPro instruction sets are exactly the same.
func TestLisovoAndLisovoProInstructionSetsAreIdentical(t *testing.T) {
	lisovo := newLisovoInstructionSet()
	lisovoPro := newLisovoProInstructionSet()

	// Compare all 256 opcodes
	for i := 0; i < 256; i++ {
		opcode := OpCode(i)
		lisovoOp := lisovo[i]
		lisovoProOp := lisovoPro[i]

		// Check that both operations exist
		assert.NotNil(t, lisovoOp, "lisovo operation %s should not be nil", opcode.String())
		assert.NotNil(t, lisovoProOp, "lisovoPro operation %s should not be nil", opcode.String())

		// Compare operation properties
		assert.Equal(t, lisovoOp.constantGas, lisovoProOp.constantGas,
			"constantGas mismatch for opcode %s", opcode.String())
		assert.Equal(t, lisovoOp.numPop, lisovoProOp.numPop,
			"numPop mismatch for opcode %s", opcode.String())
		assert.Equal(t, lisovoOp.numPush, lisovoProOp.numPush,
			"numPush mismatch for opcode %s", opcode.String())
		assert.Equal(t, lisovoOp.isPush, lisovoProOp.isPush,
			"isPush mismatch for opcode %s", opcode.String())
		assert.Equal(t, lisovoOp.isSwap, lisovoProOp.isSwap,
			"isSwap mismatch for opcode %s", opcode.String())
		assert.Equal(t, lisovoOp.isDup, lisovoProOp.isDup,
			"isDup mismatch for opcode %s", opcode.String())
		assert.Equal(t, lisovoOp.opNum, lisovoProOp.opNum,
			"opNum mismatch for opcode %s", opcode.String())
		assert.Equal(t, lisovoOp.maxStack, lisovoProOp.maxStack,
			"maxStack mismatch for opcode %s", opcode.String())
	}
}
