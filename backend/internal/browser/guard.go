package browser

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Guard runs one module call and turns a panic in it into what fail makes of
// the value raised, rather than a 500 from the router's recoverer. The stack
// goes to log only, never into the error: an error becomes a note, which the
// household sees and the connection stores.
func Guard(log *slog.Logger, message string, attrs []any, fail func(raised any) error, run func() error) (err error) {
	defer func() {
		raised := recover()
		if raised == nil {
			return
		}
		log.Error(message, append(attrs, "panic", fmt.Sprint(raised), "stack", string(debug.Stack()))...)
		err = fail(raised)
	}()
	return run()
}

// Guarded is a guard for a call that answers a value; a function because a
// method cannot be generic.
func Guarded[T any](guard func(run func() error) error, run func() (T, error)) (T, error) {
	var out T
	err := guard(func() error {
		var err error
		out, err = run()
		return err
	})
	return out, err
}
