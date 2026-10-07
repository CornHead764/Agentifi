package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// dueRun is one connector's scheduled pass: what is due, and running one of it.
type dueRun[T any] struct {
	// kind names the pass in logs and in its lock.
	kind string
	list func(context.Context) ([]T, error)
	// run runs one item and says whether anything ran; an item it passes over
	// is not counted and is not followed by the pause.
	run func(context.Context, T) bool
	// between is the pause after each item that ran.
	between time.Duration
}

// runDue lists what is due and runs each item in turn, and reports how many
// ran. Passes of one kind never overlap, in this process or across servers: a
// second waits for the first and then lists again, by which time what the first
// ran carries its stamp and is no longer due.
func runDue[T any](ctx context.Context, st *store.Store, log *slog.Logger, d dueRun[T]) int {
	release, err := st.NamedLock(ctx, "run-due:"+d.kind)
	if err != nil {
		log.Error(d.kind+": waiting for the scheduled pass", "error", err)
		return 0
	}
	defer release()

	due, err := d.list(ctx)
	if err != nil {
		log.Error(d.kind+": listing what is due", "error", err)
		return 0
	}
	ran := 0
	for _, item := range due {
		if ctx.Err() != nil {
			return ran
		}
		if !d.run(ctx, item) {
			continue
		}
		ran++
		if d.between > 0 {
			select {
			case <-ctx.Done():
				return ran
			case <-time.After(d.between):
			}
		}
	}
	return ran
}
