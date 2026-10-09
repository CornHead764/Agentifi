package provider

import "context"

// PullProgress hears what a running pull is doing, in a line a person can
// read ("Reading order history: 2024, page 3"). It must be cheap and safe to
// call from the pull's goroutine.
type PullProgress func(line string)

type pullProgressKey struct{}

// WithPullProgress carries a listener down to the module that runs the pull.
func WithPullProgress(ctx context.Context, hear PullProgress) context.Context {
	return context.WithValue(ctx, pullProgressKey{}, hear)
}

// ReportPull tells the pull's listener what it is doing now; with none
// listening it does nothing.
func ReportPull(ctx context.Context, line string) {
	if ctx == nil {
		return
	}
	if hear, ok := ctx.Value(pullProgressKey{}).(PullProgress); ok && hear != nil {
		hear(line)
	}
}
