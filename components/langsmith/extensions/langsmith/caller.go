package langsmith

// Port of utils/async_caller.js, utils/p-retry/index.js, utils/is-network-error/index.js, utils/p-queue.js
// (p-queue 6.6.2, the part AsyncCaller reaches) and the error helpers of utils/error.js (langsmith 0.10.5).
//
// Differences that follow from Go, all listed in port/lane-caller.md:
//   - a call runs on the calling goroutine (no goroutine per call, nothing to leak); the queue is a FIFO of
//     parked callers;
//   - the AbortSignal of callWithOptions and the cancellation of the transport are one thing here: the ctx.

import (
	"container/list"
	"context"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// namedError is an error with a JavaScript `name` property (Error.name).
type namedError interface{ ErrorName() string }

// codedError is an error with a JavaScript `code` property (e.g. "ECONNABORTED").
type codedError interface{ ErrorCode() string }

// responseError is an error with a JavaScript `response` property (an axios style error). The errors
// raiseForStatus builds never carry one (error.js:106-120), so in this SDK path the onFailedResponse hook always
// receives nil; the interface exists so an error that does carry a response is classified like the original.
type responseError interface{ ErrorResponse() *http.Response }

// statusError is the Error raiseForStatus throws: Message is `Failed to <context>. Received status [<n>]:
// <statusText>. Message: <body>`; Status is the HTTP status; Name is "LangSmithNotFoundError" (404),
// "LangSmithConflictError" (409) or "" (any other status, a plain Error). It carries no response
// (utils/error.js raiseForStatus).
type statusError struct {
	Status  int
	Name    string
	Message string
}

func (e *statusError) Error() string { return e.Message }

// ErrorName is the error's JavaScript `name`.
func (e *statusError) ErrorName() string {
	if e.Name == "" {
		return "Error"
	}
	return e.Name
}

// fetchTypeError is the TypeError `fetch` throws (undici: `TypeError: fetch failed` with a cause). Message is the
// JavaScript message; NoStack mirrors `error.stack === undefined` (is-network-error checks it for "Load failed").
// A transport error the Go client wraps in it is classified by isNetworkError exactly like the original.
type fetchTypeError struct {
	Message string
	Cause   error
	NoStack bool
}

func (e *fetchTypeError) Error() string     { return e.Message }
func (e *fetchTypeError) Unwrap() error     { return e.Cause }
func (e *fetchTypeError) ErrorName() string { return "TypeError" }

// networkErrorMessages is is-network-error/index.js:7-17.
var networkErrorMessages = map[string]struct{}{
	"network error": {}, "Failed to fetch": {}, "NetworkError when attempting to fetch resource.": {},
	"The Internet connection appears to be offline.": {}, "Network request failed": {}, "fetch failed": {},
	"terminated": {}, " A network error occurred.": {}, "Network connection lost": {},
}

// isNetworkError is is-network-error/index.js:19-39. Only a TypeError qualifies.
func isNetworkError(err error) bool {
	var te *fetchTypeError
	if !errors.As(err, &te) {
		return false
	}
	// Safari 17+ has generic message but no stack for network errors (index.js:29-34).
	if te.Message == "Load failed" {
		return te.NoStack
	}
	if strings.HasPrefix(te.Message, "error sending request for url") { // Deno (index.js:36-38)
		return true
	}
	_, ok := networkErrorMessages[te.Message]
	return ok
}

// utf8DecodeLossy is `await response.text()` as Node 24 runs it: the WHATWG UTF-8 decode (leading BOM(s)
// stripped, one U+FFFD per maximal ill-formed subpart).
func utf8DecodeLossy(b []byte) string {
	// Node 24's undici strips a leading BOM itself and its TextDecoder strips another: up to two (observed on
	// node v24.22.1, port/tools/gen/caller-differential.mjs "utf8 double bom"; a third and a BOM later in the text stay).
	for range 2 {
		if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
			b = b[3:]
		}
	}
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		var need int
		var cp rune
		lo, hi := byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need, cp = 1, rune(c&0x1F)
		case c >= 0xE0 && c <= 0xEF:
			if c == 0xE0 {
				lo = 0xA0
			} else if c == 0xED {
				hi = 0x9F
			}
			need, cp = 2, rune(c&0x0F)
		case c >= 0xF0 && c <= 0xF4:
			if c == 0xF0 {
				lo = 0x90
			} else if c == 0xF4 {
				hi = 0x8F
			}
			need, cp = 3, rune(c&0x07)
		default:
			sb.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for range need {
			if j >= len(b) || b[j] < lo || b[j] > hi {
				ok = false
				break
			}
			cp = cp<<6 | rune(b[j]&0x3F)
			lo, hi = 0x80, 0xBF
			j++
		}
		if !ok {
			sb.WriteRune(utf8.RuneError) // the offending byte is processed again
		} else {
			sb.WriteRune(cp)
		}
		i = j
	}
	return sb.String()
}

// statusText is Response.statusText: the reason phrase of the status line.
func statusText(resp *http.Response) string {
	if resp.Status == "" {
		return http.StatusText(resp.StatusCode)
	}
	s := strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode))
	return strings.TrimPrefix(s, " ")
}

const orgScopedKeyMessage = "This API key is org-scoped and requires workspace specification. " +
	"Please provide 'workspaceId' parameter, " +
	"or set LANGSMITH_WORKSPACE_ID environment variable."

// raiseForStatus is error.js raiseForStatus (error.js:86-132). With consumeOnSuccess it reads and discards a 2xx
// body (and closes it); a non-2xx body is read, closed and put in the error. A 2xx response without
// consumeOnSuccess is returned untouched: the caller owns its body. A 403 whose JSON body has error ==
// "org_scoped_key_requires_workspace" gets the replacement message. A body read failure on the success path
// is returned (the original's `await response.text()` rejects, error.js:90); on the error path it yields an
// empty message body (error.js:93-97).
func raiseForStatus(resp *http.Response, op string, consumeOnSuccess bool) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 { // response.ok
		if consumeOnSuccess && resp.Body != nil {
			_, err := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return err
		}
		return nil
	}
	errorBody := ""
	if resp.Body != nil {
		if b, err := io.ReadAll(resp.Body); err == nil {
			errorBody = utf8DecodeLossy(b)
		}
		resp.Body.Close()
	}
	if resp.StatusCode == 403 {
		// A non-JSON body falls through and is surfaced raw (error.js:101-109).
		var data any
		if json.Unmarshal([]byte(errorBody), &data) == nil {
			if m, ok := data.(map[string]any); ok {
				if e, ok := m["error"].(string); ok && e == "org_scoped_key_requires_workspace" {
					errorBody = orgScopedKeyMessage
				}
			}
		}
	}
	text := statusText(resp)
	var sb strings.Builder
	sb.Grow(len("Failed to . Received status [000]: . Message: ") + len(op) + len(text) + len(errorBody))
	sb.WriteString("Failed to ")
	sb.WriteString(op)
	sb.WriteString(". Received status [")
	sb.WriteString(strconv.Itoa(resp.StatusCode))
	sb.WriteString("]: ")
	sb.WriteString(text)
	sb.WriteString(". Message: ")
	sb.WriteString(errorBody)
	e := &statusError{Status: resp.StatusCode, Message: sb.String()}
	switch resp.StatusCode {
	case 404:
		e.Name = "LangSmithNotFoundError"
	case 409:
		e.Name = "LangSmithConflictError"
	}
	return e
}

func hasErrorName(err error, name string) bool {
	var n namedError
	return errors.As(err, &n) && n.ErrorName() == name
}

// isLangSmithNotFoundError is error.js isLangSmithNotFoundError (error.js:72-76).
func isLangSmithNotFoundError(err error) bool { return hasErrorName(err, "LangSmithNotFoundError") }

// isLangSmithConflictError is error.js isLangSmithConflictError (error.js:77-81).
func isLangSmithConflictError(err error) bool { return hasErrorName(err, "LangSmithConflictError") }

// statusRetryable is STATUS_RETRYABLE (async_caller.js:3-11).
var statusRetryable = [...]int{408, 425, 429, 500, 502, 503, 504}

func isStatusRetryable(status int) bool {
	for _, s := range statusRetryable {
		if s == status {
			return true
		}
	}
	return false
}

const (
	callerDefaultMaxRetries = 6      // async_caller.js:65 (`maxRetries ?? 6`)
	retryMinTimeoutMs       = 1000.0 // p-retry/index.js:117 (`minTimeout ??= 1000`)
	retryFactor             = 2.0    // p-retry/index.js:116 (`factor ??= 2`)
)

// retryDelay is p-retry calculateDelay (p-retry/index.js:46-51) with randomize on and maxTimeout Infinity:
// Math.round(random * minTimeout * factor ** (attempt - 1)), random = jitter + 1 with jitter in [0, 1).
func retryDelay(retriesConsumed int, jitter float64) time.Duration {
	attempt := max(1, retriesConsumed+1)
	random := jitter + 1
	ms := math.Floor(random*retryMinTimeoutMs*math.Pow(retryFactor, float64(attempt-1)) + 0.5) // Math.round
	const maxMs = float64(math.MaxInt64 / int64(time.Millisecond))
	if ms >= maxMs {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ms) * time.Millisecond
}

// callerParams is the AsyncCaller constructor's params. MaxRetries < 0 means the default (6; `maxRetries ?? 6`),
// 0 means no retry; MaxConcurrency <= 0 means Infinity; MaxQueueSizeBytes < 0 means unlimited (undefined).
// OnFailedResponse mirrors onFailedResponseHook: it receives the response the error carries (always nil for
// raiseForStatus errors) and returns whether it handled the failure. Debug is accepted for the constructor
// contract and ignored: AsyncCaller never reads `debug` (async_caller.js).
type callerParams struct {
	MaxRetries        int
	MaxConcurrency    int
	MaxQueueSizeBytes int64
	OnFailedResponse  func(resp *http.Response) bool
	Debug             bool
}

// asyncCaller runs calls with bounded concurrency, retries and exponential backoff with jitter
// (minTimeout 1000 ms, factor 2, randomize in [1, 2), maxRetries per the params).
type asyncCaller struct {
	maxRetries        int
	maxConcurrency    int
	maxQueueSizeBytes int64
	onFailedResponse  func(resp *http.Response) bool

	// Test seams: the backoff sleeper and the jitter source ([0, 1), Math.random).
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func() float64

	mu             sync.Mutex
	queueSizeBytes int64
	running        int        // calls holding a concurrency slot (PQueue _pendingCount)
	total          int        // queued + running calls
	waiters        *list.List // FIFO of *callWaiter parked for a slot
	idle           chan struct{}
}

type callWaiter struct {
	ready   chan struct{}
	granted bool
}

func newAsyncCaller(p callerParams) *asyncCaller {
	c := &asyncCaller{
		maxRetries:        p.MaxRetries,
		maxConcurrency:    p.MaxConcurrency,
		maxQueueSizeBytes: p.MaxQueueSizeBytes,
		onFailedResponse:  p.OnFailedResponse,
		sleep:             sleepCtx,
		jitter:            rand.Float64,
		waiters:           list.New(),
	}
	if c.maxRetries < 0 {
		c.maxRetries = callerDefaultMaxRetries
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
}

// call is AsyncCaller.call: fn is retried on retryable failures. fn returns the response (already checked with
// raiseForStatus) or an error. A *statusError with a status outside [408 425 429 500 502 503 504] is not retried;
// a context deadline or cancellation is not retried; transport errors are. Ending ctx while the call waits for a
// slot, runs or backs off ends it with ctx.Err().
func (c *asyncCaller) call(ctx context.Context, fn func(ctx context.Context) (*http.Response, error)) (*http.Response, error) {
	return c.callWithOptions(ctx, 0, fn)
}

// callWithOptions is AsyncCaller.callWithOptions with options.sizeBytes: it fails with `Queue size limit (<max>
// bytes) exceeded. Current queue size: <n> bytes, attempted addition: <m> bytes.` when the byte budget is spent,
// and releases sizeBytes when the call finishes.
func (c *asyncCaller) callWithOptions(ctx context.Context, sizeBytes int64, fn func(ctx context.Context) (*http.Response, error)) (*http.Response, error) {
	c.mu.Lock()
	if c.maxQueueSizeBytes >= 0 && sizeBytes > 0 && c.queueSizeBytes+sizeBytes > c.maxQueueSizeBytes { // async_caller.js:73-79
		cur := c.queueSizeBytes
		c.mu.Unlock()
		return nil, errors.New("Queue size limit (" + strconv.FormatInt(c.maxQueueSizeBytes, 10) + " bytes) exceeded. " +
			"Current queue size: " + strconv.FormatInt(cur, 10) + " bytes, attempted addition: " + strconv.FormatInt(sizeBytes, 10) + " bytes.")
	}
	if sizeBytes > 0 { // async_caller.js:81-83
		c.queueSizeBytes += sizeBytes
	}
	c.total++
	if c.maxConcurrency > 0 && c.running >= c.maxConcurrency {
		w := &callWaiter{ready: make(chan struct{})}
		el := c.waiters.PushBack(w)
		c.mu.Unlock()
		select {
		case <-w.ready:
		case <-ctx.Done():
			c.mu.Lock()
			if w.granted {
				c.mu.Unlock()
				c.finish(sizeBytes, true)
			} else {
				c.waiters.Remove(el)
				c.mu.Unlock()
				c.finish(sizeBytes, false)
			}
			return nil, ctx.Err()
		}
	} else {
		c.running++
		c.mu.Unlock()
	}
	defer c.finish(sizeBytes, true)
	return c.retry(ctx, fn)
}

// finish ends one call: it releases sizeBytes (async_caller.js:107-111), hands the concurrency slot to the next
// parked call, and signals idle once nothing is queued or running.
func (c *asyncCaller) finish(sizeBytes int64, holdsSlot bool) {
	c.mu.Lock()
	if sizeBytes > 0 {
		c.queueSizeBytes -= sizeBytes
	}
	if holdsSlot {
		if front := c.waiters.Front(); front != nil {
			w := c.waiters.Remove(front).(*callWaiter)
			w.granted = true
			close(w.ready)
		} else {
			c.running--
		}
	}
	c.total--
	if c.total == 0 && c.idle != nil {
		close(c.idle)
		c.idle = nil
	}
	c.mu.Unlock()
}

// onIdle is PQueue.onIdle: it returns when no call is queued or running, or ctx ends.
func (c *asyncCaller) onIdle(ctx context.Context) error {
	c.mu.Lock()
	if c.total == 0 {
		c.mu.Unlock()
		return nil
	}
	if c.idle == nil {
		c.idle = make(chan struct{})
	}
	ch := c.idle
	c.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// retry is pRetry(fn, {onFailedAttempt, retries: maxRetries, randomize: true}) (p-retry/index.js:96-150,
// async_caller.js:85-123): at most maxRetries+1 attempts.
func (c *asyncCaller) retry(ctx context.Context, fn func(ctx context.Context) (*http.Response, error)) (*http.Response, error) {
	for retriesConsumed := 0; ; retriesConsumed++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := fn(ctx)
		if err == nil {
			return resp, nil
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		// onAttemptFailure (p-retry/index.js:54-95).
		if c.rethrow(err) { // options.onFailedAttempt threw
			return nil, err
		}
		if c.maxRetries-retriesConsumed <= 0 { // retriesLeft <= 0 (index.js:79)
			return nil, err
		}
		var te *fetchTypeError
		if errors.As(err, &te) && !isNetworkError(err) { // a TypeError that is no network error (index.js:82-88)
			return nil, err
		}
		if serr := c.sleep(ctx, retryDelay(retriesConsumed, c.jitter())); serr != nil {
			return nil, serr
		}
	}
}

// rethrow is the onFailedAttempt of callWithOptions (async_caller.js:88-122): true when the error must be
// thrown instead of retried.
func (c *asyncCaller) rethrow(err error) bool {
	msg := err.Error()
	if strings.HasPrefix(msg, "Cancel") || strings.HasPrefix(msg, "TimeoutError") || strings.HasPrefix(msg, "AbortError") {
		return true
	}
	// A deadline is the TimeoutError of AbortSignal.timeout; a cancelled context is the abort.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || hasErrorName(err, "TimeoutError") {
		return true
	}
	var ce codedError
	if errors.As(err, &ce) && ce.ErrorCode() == "ECONNABORTED" {
		return true
	}
	var resp *http.Response
	var re responseError
	if errors.As(err, &re) {
		resp = re.ErrorResponse()
	}
	if c.onFailedResponse != nil && c.onFailedResponse(resp) { // handled: skip the status check (async_caller.js:107-109)
		return false
	}
	status, has := 0, false
	if resp != nil {
		status, has = resp.StatusCode, true
	} else {
		var se *statusError
		if errors.As(err, &se) {
			status, has = se.Status, true
		}
	}
	return has && !isStatusRetryable(status)
}
