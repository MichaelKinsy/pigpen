package websearch

import (
	"context"
	"errors"
)

// errAborted is the cancellation every side call reports (`new Error("Aborted")`).
var errAborted = errors.New("Aborted")

// awaitWithAbort is abortable.ts. Stub: it does not observe cancellation yet.
func awaitWithAbort[T any](_ context.Context, operation func() (T, error)) (T, error) {
	return operation()
}
