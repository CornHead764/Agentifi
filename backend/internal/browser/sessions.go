package browser

import (
	"sync"
	"time"
)

// SessionTTL is how long a sign-in — or a pull parked on a challenge — waits
// for somebody.
const SessionTTL = 20 * time.Minute

// ClassifyEvery throttles reading a live sign-in's page: the dialog polls for a
// frame twice a second and a classify is a script in the page.
const ClassifyEvery = 2 * time.Second

// Session is what an engine holds between requests.
type Session interface {
	// Shut lets go of everything the session holds. A second Shut does
	// nothing, which is what a cancel racing the reaper needs.
	Shut()
}

// Sessions is an engine's sessions by id. Every way a session leaves — closed,
// reaped or shut down with the process — goes through its Shut, so nothing a
// session holds outlives it by the way it went. The zero value is ready.
type Sessions[S Session] struct {
	mu   sync.Mutex
	held map[string]*heldSession[S]
}

// heldSession's active is under the registry's lock, not the session's: the
// reaper sweeps the map under that one.
type heldSession[S Session] struct {
	session S
	active  time.Time
}

func (r *Sessions[S]) Add(id string, s S, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.held == nil {
		r.held = map[string]*heldSession[S]{}
	}
	r.held[id] = &heldSession[S]{session: s, active: now}
}

// Find is a session by id, touched.
func (r *Sessions[S]) Find(id string, now time.Time) (S, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	held, ok := r.held[id]
	if !ok {
		var none S
		return none, false
	}
	held.active = now
	return held.session, true
}

// Where is every session keep says yes to.
func (r *Sessions[S]) Where(keep func(S) bool) []S {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []S
	for _, held := range r.held {
		if keep(held.session) {
			out = append(out, held.session)
		}
	}
	return out
}

// Close shuts s and forgets id. s is shut even when the reaper took id first.
func (r *Sessions[S]) Close(id string, s S) {
	s.Shut()
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.held, id)
}

// Reap shuts every session nobody has touched within SessionTTL, except those
// busy says are still being driven. busy may be nil.
func (r *Sessions[S]) Reap(now time.Time, busy func(S, time.Time) bool) {
	var stale []S
	r.mu.Lock()
	for id, held := range r.held {
		if busy != nil && busy(held.session, now) {
			continue
		}
		if now.Sub(held.active) > SessionTTL {
			stale = append(stale, held.session)
			delete(r.held, id)
		}
	}
	r.mu.Unlock()
	for _, s := range stale {
		s.Shut()
	}
}

// CloseAll shuts every session, for a process shutting down: each browser is
// closed rather than killed, so a kept profile's last cookies are written and
// its claim released for the next process.
func (r *Sessions[S]) CloseAll() {
	r.mu.Lock()
	all := make(map[string]S, len(r.held))
	for id, held := range r.held {
		all[id] = held.session
	}
	r.mu.Unlock()
	for id, s := range all {
		r.Close(id, s)
	}
}

func (r *Sessions[S]) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.held)
}
