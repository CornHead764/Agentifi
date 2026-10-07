package connector

import (
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// guard runs one module call and turns a panic in it into an error naming the
// provider or the merchant.
func (e *Engine) guard(name, step string, run func() error) error {
	if name == "" {
		name = "a provider"
	}
	return browser.Guard(e.log(), "a connector panicked", []any{"provider", name, "step", step},
		func(raised any) error {
			return agentError(provider.ErrAgentFailed,
				"%s failed inside the built-in browser engine (%s): %v", name, step, raised)
		}, run)
}

func guarded[T any](e *Engine, name, step string, run func() (T, error)) (T, error) {
	return browser.Guarded(func(run func() error) error { return e.guard(name, step, run) }, run)
}
