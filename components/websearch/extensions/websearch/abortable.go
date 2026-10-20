package websearch

import (
	"context"
	"errors"
)

// errAborted is the cancellation every side call reports (`new Error("Aborted")`).
var errAborted = errors.New("Aborted")

// awaitWithAbort observes cancellation while waiting for work that cannot itself be cancelled
// (abortable.ts): a host call has no cancel signal, so the wait ends with "Aborted" as soon as
// ctx is done and the call's late result is dropped. A result that arrives for a cancelled ctx is
// dropped too.
func awaitWithAbort[T any](ctx context.Context, operation func() (T, error)) (T, error) {
	var zero T
	if ctx.Done() == nil {
		return operation()
	}
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		value, err := operation()
		done <- outcome{value, err}
	}()
	select {
	case o := <-done:
		if o.err != nil {
			return zero, o.err
		}
		if ctx.Err() != nil {
			return zero, errAborted
		}
		return o.value, nil
	case <-ctx.Done():
		return zero, errAborted
	}
}
