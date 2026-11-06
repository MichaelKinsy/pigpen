package langsmith

// Port of utils/async_caller.js, utils/p-retry/index.js, utils/is-network-error, utils/p-queue.js and the
// error helpers of utils/error.js (langsmith 0.10.5). OWNER: caller lane.

import (
	"context"
	"net/http"
)

// statusError is the Error raiseForStatus throws: Message is `Failed to <context>. Received status [<n>]:
// <statusText>. Message: <body>`; Status is the HTTP status; Name is "LangSmithNotFoundError" (404),
// "LangSmithConflictError" (409) or "" (any other status). It carries no response (utils/error.js raiseForStatus).
type statusError struct {
	Status  int
	Name    string
	Message string
}

func (e *statusError) Error() string { return e.Message }

// raiseForStatus is error.js raiseForStatus. With consumeOnSuccess it reads and discards a 2xx body.
// A 403 whose JSON body has error == "org_scoped_key_requires_workspace" gets the replacement message.
func raiseForStatus(resp *http.Response, context string, consumeOnSuccess bool) error {
	panic("unported: caller.go raiseForStatus")
}

// isLangSmithNotFoundError is error.js isLangSmithNotFoundError.
func isLangSmithNotFoundError(err error) bool { panic("unported: caller.go isLangSmithNotFoundError") }

// callerParams is the AsyncCaller constructor's params. MaxQueueSizeBytes < 0 means unlimited; MaxConcurrency <= 0
// means Infinity. OnFailedResponse mirrors onFailedResponseHook: it receives the response the error carries
// (always nil for raiseForStatus errors) and returns whether it handled the failure.
type callerParams struct {
	MaxRetries        int
	MaxConcurrency    int
	MaxQueueSizeBytes int64
	OnFailedResponse  func(resp *http.Response) bool
	Debug             bool
}

// asyncCaller runs calls with bounded concurrency, retries and exponential backoff with jitter
// (minTimeout 1000 ms, factor 2, randomize in [1, 2), maxRetries per the params).
type asyncCaller struct{}

func newAsyncCaller(p callerParams) *asyncCaller { panic("unported: caller.go newAsyncCaller") }

// call is AsyncCaller.call: fn is retried on retryable failures. fn returns the response (already checked with
// raiseForStatus) or an error. A *statusError with a status outside [408 425 429 500 502 503 504] is not retried;
// a context deadline or cancellation is not retried; transport errors are.
func (c *asyncCaller) call(ctx context.Context, fn func(ctx context.Context) (*http.Response, error)) (*http.Response, error) {
	panic("unported: caller.go call")
}

// callWithOptions is AsyncCaller.callWithOptions with options.sizeBytes: it fails with `Queue size limit (<max>
// bytes) exceeded. Current queue size: <n> bytes, attempted addition: <m> bytes.` when the byte budget is spent,
// and releases sizeBytes when the call finishes.
func (c *asyncCaller) callWithOptions(ctx context.Context, sizeBytes int64, fn func(ctx context.Context) (*http.Response, error)) (*http.Response, error) {
	panic("unported: caller.go callWithOptions")
}

// onIdle is PQueue.onIdle: it returns when no call is queued or running, or ctx ends.
func (c *asyncCaller) onIdle(ctx context.Context) error { panic("unported: caller.go onIdle") }
