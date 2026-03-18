package stagedsync

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/erigontech/erigon/eth/ethconfig"
	"github.com/erigontech/erigon/turbo/services"
)

// mockBlockReader implements services.FullBlockReader with only FrozenBlocks() used.
type mockBlockReader struct {
	services.FullBlockReader
	frozenBlocks uint64
}

func (m *mockBlockReader) FrozenBlocks() uint64 {
	return m.frozenBlocks
}

func TestShouldGenerateChangeSets(t *testing.T) {
	tests := []struct {
		name              string
		alwaysGenerate    bool
		frozenBlocks      uint64
		initialCycle      bool
		useFinality       bool
		finalizedBlockNum uint64
		maxReorgDepth     uint64
		blockNum          uint64
		maxBlockNum       uint64
		want              bool
	}{
		{
			name:           "AlwaysGenerateChangesets overrides everything",
			alwaysGenerate: true,
			frozenBlocks:   1000,
			blockNum:       500, // below frozen
			maxBlockNum:    2000,
			want:           true,
		},
		{
			name:         "block below frozen returns false",
			frozenBlocks: 1000,
			blockNum:     999,
			maxBlockNum:  2000,
			want:         false,
		},
		{
			name:         "initialCycle returns false",
			initialCycle: true,
			blockNum:     1500,
			maxBlockNum:  2000,
			want:         false,
		},
		{
			name:              "UseForkchoiceFinality: block below finalized returns false",
			useFinality:       true,
			finalizedBlockNum: 1000,
			blockNum:          900,
			maxBlockNum:       2000,
			want:              false,
		},
		{
			name:              "UseForkchoiceFinality: block equal to finalized returns false",
			useFinality:       true,
			finalizedBlockNum: 1000,
			blockNum:          1000,
			maxBlockNum:       2000,
			want:              false,
		},
		{
			name:              "UseForkchoiceFinality: block above finalized falls through to MaxReorgDepth",
			useFinality:       true,
			finalizedBlockNum: 1000,
			maxReorgDepth:     100,
			blockNum:          1001,
			maxBlockNum:       2000,
			want:              false, // 1001 + 100 = 1101 < 2000
		},
		{
			name:          "MaxReorgDepth: block in reorg window returns true",
			maxReorgDepth: 100,
			blockNum:      1950,
			maxBlockNum:   2000,
			want:          true, // 1950 + 100 = 2050 >= 2000
		},
		{
			name:          "MaxReorgDepth: block outside reorg window returns false",
			maxReorgDepth: 100,
			blockNum:      1800,
			maxBlockNum:   2000,
			want:          false, // 1800 + 100 = 1900 < 2000
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ExecuteBlockCfg{
				blockReader: &mockBlockReader{frozenBlocks: tt.frozenBlocks},
				syncCfg: ethconfig.Sync{
					AlwaysGenerateChangesets: tt.alwaysGenerate,
					UseForkchoiceFinality:    tt.useFinality,
					MaxReorgDepth:            tt.maxReorgDepth,
				},
			}
			got := shouldGenerateChangeSets(cfg, tt.finalizedBlockNum, tt.blockNum, tt.maxBlockNum, tt.initialCycle)
			assert.Equal(t, tt.want, got, "shouldGenerateChangeSets(%d, %d, %d)", tt.blockNum, tt.maxBlockNum, tt.initialCycle)
		})
	}
}
