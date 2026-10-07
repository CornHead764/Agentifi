package service

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A stand-in for a due table: an item is due until a run stamps it.
type dueBoard struct {
	mu      sync.Mutex
	stamped map[int]bool
	runs    map[int]int
}

func (d *dueBoard) list(context.Context) ([]int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var due []int
	for item := range 5 {
		if !d.stamped[item] {
			due = append(due, item)
		}
	}
	return due, nil
}

func (d *dueBoard) run(_ context.Context, item int) bool {
	// Long enough that an unguarded second pass would list the same items.
	time.Sleep(5 * time.Millisecond)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.runs[item]++
	d.stamped[item] = true
	return item != 4
}

func TestTwoScheduledPassesAtOnceRunEachItemOnce(t *testing.T) {
	st := db(t)
	board := &dueBoard{stamped: map[int]bool{}, runs: map[int]int{}}
	pass := dueRun[int]{kind: "test-" + t.Name(), list: board.list, run: board.run}

	counts := make([]int, 2)
	var wg sync.WaitGroup
	for i := range counts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counts[i] = runDue(t.Context(), st, slog.Default(), pass)
		}()
	}
	wg.Wait()

	require.Equal(t, map[int]int{0: 1, 1: 1, 2: 1, 3: 1, 4: 1}, board.runs)
	require.ElementsMatch(t, []int{4, 0}, counts, "one pass ran everything; one it passed over is not counted")
}

func TestAScheduledPassStopsWhenItsContextEnds(t *testing.T) {
	st := db(t)
	board := &dueBoard{stamped: map[int]bool{}, runs: map[int]int{}}
	ctx, cancel := context.WithCancel(t.Context())
	pass := dueRun[int]{
		kind: "test-" + t.Name(), list: board.list,
		run: func(ctx context.Context, item int) bool {
			board.run(ctx, item)
			cancel()
			return true
		},
		between: time.Hour,
	}
	require.Equal(t, 1, runDue(ctx, st, slog.Default(), pass))
	require.Len(t, board.runs, 1)
}
