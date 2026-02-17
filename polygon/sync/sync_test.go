package sync

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/log/v3"
	"github.com/erigontech/erigon/execution/types"
	"go.uber.org/mock/gomock"
)

// stubExecutionClient is a minimal no-op implementation of ExecutionClient for unit tests.
type stubExecutionClient struct{}

func (s *stubExecutionClient) Prepare(context.Context) error { return nil }
func (s *stubExecutionClient) InsertBlocks(context.Context, []*types.Block) error {
	return nil
}
func (s *stubExecutionClient) UpdateForkChoice(_ context.Context, tip *types.Header, _ *types.Header) (common.Hash, error) {
	return tip.Hash(), nil
}
func (s *stubExecutionClient) CurrentHeader(context.Context) (*types.Header, error) {
	return nil, nil
}
func (s *stubExecutionClient) GetHeader(context.Context, uint64) (*types.Header, error) {
	return nil, nil
}
func (s *stubExecutionClient) GetTd(context.Context, uint64, common.Hash) (*big.Int, error) {
	return big.NewInt(0), nil
}

func newTestSync(t *testing.T) *Sync {
	t.Helper()
	ctrl := gomock.NewController(t)
	store := NewMockStore(ctrl)
	store.EXPECT().Flush(gomock.Any()).Return(nil).AnyTimes()

	return &Sync{
		store:     store,
		execution: &stubExecutionClient{},
		logger:    log.New(),
	}
}

func TestCommitExecutionTracksLastTipAge(t *testing.T) {
	s := newTestSync(t)

	// Create a header with timestamp 60 seconds in the past.
	oldTime := time.Now().Add(-60 * time.Second)
	header := &types.Header{
		Number: big.NewInt(100),
		Time:   uint64(oldTime.Unix()),
	}

	err := s.commitExecution(context.Background(), header, header)
	if err != nil {
		t.Fatalf("commitExecution failed: %v", err)
	}

	if s.lastTipAge <= catchUpAgeThreshold {
		t.Errorf("expected lastTipAge > %v for a 60s-old block, got %v", catchUpAgeThreshold, s.lastTipAge)
	}
}

func TestCommitExecutionRecentBlockHasLowAge(t *testing.T) {
	s := newTestSync(t)

	// Create a header with timestamp 2 seconds in the past.
	recentTime := time.Now().Add(-2 * time.Second)
	header := &types.Header{
		Number: big.NewInt(200),
		Time:   uint64(recentTime.Unix()),
	}

	err := s.commitExecution(context.Background(), header, header)
	if err != nil {
		t.Fatalf("commitExecution failed: %v", err)
	}

	if s.lastTipAge > catchUpAgeThreshold {
		t.Errorf("expected lastTipAge < %v for a 2s-old block, got %v", catchUpAgeThreshold, s.lastTipAge)
	}
}

func TestCatchUpAgeThresholdValue(t *testing.T) {
	if catchUpAgeThreshold != 30*time.Second {
		t.Errorf("expected catchUpAgeThreshold = 30s, got %v", catchUpAgeThreshold)
	}
}
