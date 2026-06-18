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

package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/erigontech/erigon-lib/log/v3"
	"github.com/erigontech/erigon-lib/testlog"
)

func windowTestEvent(id uint64, unixTime int64, dataLen int) *EventRecordWithTime {
	return &EventRecordWithTime{
		EventRecord: EventRecord{
			ID:      id,
			ChainID: "80002",
			Data:    make([]byte, dataLen),
		},
		Time: time.Unix(unixTime, 0),
	}
}

func newWindowTestStore(t *testing.T) (context.Context, *MdbxStore) {
	t.Helper()
	ctx := context.Background()
	logger := testlog.Logger(t, log.LvlError)
	store := NewMdbxStore(t.TempDir(), logger, false, 1)
	require.NoError(t, store.Prepare(ctx))
	t.Cleanup(store.Close)
	return ctx, store
}

// TestLastEventIdWithinWindowAndBudget exercises the Valencia per-block state-sync
// byte budget: events are included only while they fit entirely within the budget,
// the first overflowing record (and all after it) is deferred, and the time window
// still bounds selection. A budget of 0 must reproduce the unbounded behavior.
func TestLastEventIdWithinWindowAndBudget(t *testing.T) {
	t.Parallel()

	// five 100-byte events, all timestamped inside the [.., 1000) window.
	events := []*EventRecordWithTime{
		windowTestEvent(1, 10, 100),
		windowTestEvent(2, 20, 100),
		windowTestEvent(3, 30, 100),
		windowTestEvent(4, 40, 100),
		windowTestEvent(5, 50, 100),
	}

	ctx, store := newWindowTestStore(t)
	require.NoError(t, store.PutEvents(ctx, events))

	wideWindow := time.Unix(1000, 0)

	t.Run("budget 0 is unbounded and equals the plain window query", func(t *testing.T) {
		plain, err := store.LastEventIdWithinWindow(ctx, 1, wideWindow)
		require.NoError(t, err)
		require.Equal(t, uint64(5), plain)

		budgeted, err := store.LastEventIdWithinWindowAndBudget(ctx, 1, wideWindow, 0)
		require.NoError(t, err)
		require.Equal(t, plain, budgeted)
	})

	t.Run("budget caps mid-window and defers the overflow", func(t *testing.T) {
		// 100+100=200 fits in 250, the third (300) overflows -> defer at id 2.
		got, err := store.LastEventIdWithinWindowAndBudget(ctx, 1, wideWindow, 250)
		require.NoError(t, err)
		require.Equal(t, uint64(2), got)
	})

	t.Run("a record is included only if it fits entirely", func(t *testing.T) {
		exact, err := store.LastEventIdWithinWindowAndBudget(ctx, 1, wideWindow, 300)
		require.NoError(t, err)
		require.Equal(t, uint64(3), exact)

		one, err := store.LastEventIdWithinWindowAndBudget(ctx, 1, wideWindow, 100)
		require.NoError(t, err)
		require.Equal(t, uint64(1), one)

		none, err := store.LastEventIdWithinWindowAndBudget(ctx, 1, wideWindow, 99)
		require.NoError(t, err)
		require.Equal(t, uint64(0), none)
	})

	t.Run("deferred records are picked up by the next window", func(t *testing.T) {
		first, err := store.LastEventIdWithinWindowAndBudget(ctx, 1, wideWindow, 250)
		require.NoError(t, err)
		require.Equal(t, uint64(2), first)

		second, err := store.LastEventIdWithinWindowAndBudget(ctx, first+1, wideWindow, 250)
		require.NoError(t, err)
		require.Equal(t, uint64(4), second)

		third, err := store.LastEventIdWithinWindowAndBudget(ctx, second+1, wideWindow, 250)
		require.NoError(t, err)
		require.Equal(t, uint64(5), third)
	})

	t.Run("time window bounds selection independently of the budget", func(t *testing.T) {
		// events 1..3 have time < 35; event 4 (t=40) is outside the window.
		narrow := time.Unix(35, 0)
		byTime, err := store.LastEventIdWithinWindowAndBudget(ctx, 1, narrow, 1_000_000)
		require.NoError(t, err)
		require.Equal(t, uint64(3), byTime)

		// whichever limit is tighter wins: budget 150 stops at id 1 before the time bound.
		byBudget, err := store.LastEventIdWithinWindowAndBudget(ctx, 1, narrow, 150)
		require.NoError(t, err)
		require.Equal(t, uint64(1), byBudget)
	})
}
