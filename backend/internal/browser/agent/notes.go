package agent

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Notes is what a connector tells the household about a pull that
// half-worked. A nil Notes takes nothing and lists nothing.
//
// Never a credential, never a code, never a cookie's name: a note is read by a
// person on a settings card and stored on the connection. What only someone
// diagnosing the pull needs (which token a refresh returned, which cookies
// were pinned, what a query was asked) is Tracef, which goes to the log and
// is never listed.
type Notes struct {
	// Log hears Tracef; nil is slog.Default().
	Log    *slog.Logger
	mu     sync.Mutex
	list   []string
	traces []string
}

func (n *Notes) Addf(format string, args ...any) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.list = append(n.list, fmt.Sprintf(format, args...))
}

// Add is Addf of one line as it stands.
func (n *Notes) Add(line string) { n.Addf("%s", line) }

// Tracef logs a line about how the pull went without listing it.
func (n *Notes) Tracef(format string, args ...any) {
	if n == nil {
		return
	}
	line := fmt.Sprintf(format, args...)
	log := n.Log
	if log == nil {
		log = slog.Default()
	}
	log.Info(line)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.traces = append(n.traces, line)
}

// Trace is Tracef of one line as it stands.
func (n *Notes) Trace(line string) { n.Tracef("%s", line) }

// List is the notes so far, oldest first.
func (n *Notes) List() []string {
	if n == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.list...)
}

// Traces is the lines Tracef logged so far, oldest first.
func (n *Notes) Traces() []string {
	if n == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.traces...)
}

func (n *Notes) Len() int {
	if n == nil {
		return 0
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.list)
}

// Lead moves the notes from index from onwards ahead of the ones before it,
// keeping each part's own order.
//
// The connection keeps a pull's first note as its last word, so an attempt
// that succeeded after one that failed has to speak first.
func (n *Notes) Lead(from int) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if from <= 0 || from >= len(n.list) {
		return
	}
	n.list = append(append([]string(nil), n.list[from:]...), n.list[:from]...)
}

// Clock is a call's idea of now; nil is the wall clock.
type Clock func() time.Time

// At is now by this clock.
func (c Clock) At() time.Time {
	if c != nil {
		return c()
	}
	return time.Now()
}
