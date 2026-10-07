package api

import (
	"context"
	"time"
)

// The in-process browser engines' upkeep, which serve runs for as long as it
// is serving.

// browserSessions is what an in-process engine does besides serving requests.
type browserSessions interface {
	Reap()
	CloseAll()
}

// reapEvery is how often sessions nobody came back for are closed.
const reapEvery = time.Minute

// KeepBrowsers closes abandoned sign-ins every minute until ctx ends, then
// closes every session and the browser engine, so a deploy closes its Chromes
// and releases its profile locks instead of leaving them for the next
// container to wait out.
func (e *Env) KeepBrowsers(ctx context.Context) {
	engines := e.browserSessions()
	ticker := time.NewTicker(reapEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for _, one := range engines {
				one.Reap()
			}
		case <-ctx.Done():
			for _, one := range engines {
				one.CloseAll()
			}
			_ = e.browserEngine().Close()
			return
		}
	}
}

func (e *Env) browserSessions() []browserSessions {
	var out []browserSessions
	if engine, ok := e.billsEngine().(browserSessions); ok {
		out = append(out, engine)
	}
	if engine, ok := e.merchantEngine().(browserSessions); ok {
		out = append(out, engine)
	}
	return out
}
