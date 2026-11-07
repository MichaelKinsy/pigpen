package langsmith

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// ---- helpers ----------------------------------------------------------------------------------------------

// fakeClock is the injected sleeper/jitter of an asyncCaller: it records the backoff delays and never sleeps.
type fakeClock struct {
	mu     sync.Mutex
	delays []time.Duration
	jit    []float64
	next   int
	block  chan struct{} // when non-nil every sleep waits for it (or ctx)
}

func (f *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.delays = append(f.delays, d)
	blk := f.block
	f.mu.Unlock()
	if blk != nil {
		select {
		case <-blk:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (f *fakeClock) jitter() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.jit) == 0 {
		return 0
	}
	j := f.jit[f.next%len(f.jit)]
	f.next++
	return j
}

func (f *fakeClock) got() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.delays...)
}

func newTestCaller(p callerParams, jitter ...float64) (*asyncCaller, *fakeClock) {
	c := newAsyncCaller(p)
	f := &fakeClock{jit: jitter}
	c.sleep, c.jitter = f.sleep, f.jitter
	return c, f
}

func params(maxRetries int) callerParams {
	return callerParams{MaxRetries: maxRetries, MaxQueueSizeBytes: -1}
}

// failing returns a fn that fails with the given errors in turn (the last repeats; nil entries succeed) and
// counts its attempts.
func failing(attempts *atomic.Int32, errs ...error) func(context.Context) (*http.Response, error) {
	return func(context.Context) (*http.Response, error) {
		n := int(attempts.Add(1)) - 1
		e := errs[min(n, len(errs)-1)]
		if e == nil {
			return &http.Response{StatusCode: 200}, nil
		}
		return nil, e
	}
}

func se(status int) error {
	return &statusError{Status: status, Message: fmt.Sprintf("Failed to op. Received status [%d]: x. Message: ", status)}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
	}
}

func (c *asyncCaller) state() (running, total, queued int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running, c.total, c.waiters.Len(), c.queueSizeBytes
}

// ---- retry policy -----------------------------------------------------------------------------------------

func TestRetryDelay(t *testing.T) {
	cases := []struct {
		consumed int
		jitter   float64
		want     time.Duration
	}{
		{0, 0, 1000 * time.Millisecond},
		{0, 0.5, 1500 * time.Millisecond},
		{1, 0, 2000 * time.Millisecond},
		{1, 0.25, 2500 * time.Millisecond},
		{2, 0.5, 6000 * time.Millisecond},
		{3, 0.999, 15992 * time.Millisecond},
		{5, 0, 32000 * time.Millisecond},
		{-1, 0, 1000 * time.Millisecond}, // attempt = max(1, retriesConsumed + 1)
		{0, 0.9999999, 2000 * time.Millisecond},
	}
	for _, c := range cases {
		if got := retryDelay(c.consumed, c.jitter); got != c.want {
			t.Errorf("retryDelay(%d, %v) = %v, want %v", c.consumed, c.jitter, got, c.want)
		}
	}
	for n := 0; n < 20; n++ {
		lo, hi := 1000*math.Pow(2, float64(n)), 2000*math.Pow(2, float64(n))
		for _, j := range []float64{0, 0.001, 0.3, 0.7, 0.999999, math.Nextafter(1, 0)} {
			ms := float64(retryDelay(n, j) / time.Millisecond)
			if ms < lo || ms > hi {
				t.Errorf("retryDelay(%d, %v) = %v ms outside [%v, %v]", n, j, ms, lo, hi)
			}
		}
	}
	if got := retryDelay(5000, 0.5); got != time.Duration(math.MaxInt64) {
		t.Errorf("huge attempt not clamped: %v", got)
	}
}

func TestStatusRetryableTable(t *testing.T) {
	retried := []int{408, 425, 429, 500, 502, 503, 504}
	terminal := []int{400, 401, 403, 404, 409, 422, 501, 505, 302, 418, 0}
	for _, s := range retried {
		var n atomic.Int32
		c, f := newTestCaller(params(4), 0)
		resp, err := c.call(context.Background(), failing(&n, se(s), nil))
		if err != nil || resp == nil || n.Load() != 2 || len(f.got()) != 1 {
			t.Errorf("status %d: attempts=%d delays=%v err=%v, want 2 attempts, 1 delay", s, n.Load(), f.got(), err)
		}
	}
	for _, s := range terminal {
		var n atomic.Int32
		c, f := newTestCaller(params(4), 0)
		_, err := c.call(context.Background(), failing(&n, se(s), nil))
		var got *statusError
		if !errors.As(err, &got) || got.Status != s || n.Load() != 1 || len(f.got()) != 0 {
			t.Errorf("status %d: attempts=%d delays=%v err=%v, want 1 attempt and the status error", s, n.Load(), f.got(), err)
		}
	}
	if !isStatusRetryable(503) || isStatusRetryable(501) {
		t.Fatal("isStatusRetryable")
	}
}

func TestRetryCountsAndDelays(t *testing.T) {
	cases := []struct {
		name       string
		maxRetries int
		attempts   int32
		delays     []time.Duration
	}{
		{"zero retries", 0, 1, nil},
		{"one retry", 1, 2, []time.Duration{1000}},
		{"four retries", 4, 5, []time.Duration{1000, 2500, 6000, 15992}},
		{"default (6)", -1, 7, []time.Duration{1000, 2500, 6000, 15992, 16000, 40000}},
	}
	for _, c := range cases {
		var n atomic.Int32
		ac, f := newTestCaller(params(c.maxRetries), 0, 0.25, 0.5, 0.999)
		_, err := ac.call(context.Background(), failing(&n, se(500)))
		if err == nil || n.Load() != c.attempts {
			t.Errorf("%s: attempts=%d err=%v, want %d attempts and an error", c.name, n.Load(), err, c.attempts)
		}
		got := f.got()
		if len(got) != len(c.delays) {
			t.Errorf("%s: delays %v, want %v ms", c.name, got, c.delays)
			continue
		}
		for i := range got {
			if got[i] != c.delays[i]*time.Millisecond {
				t.Errorf("%s: delay %d = %v, want %v ms", c.name, i, got[i], c.delays[i])
			}
		}
	}
}

func TestRetryDelaysWithinBounds(t *testing.T) {
	c := newAsyncCaller(params(6)) // real jitter
	f := &fakeClock{}
	c.sleep = f.sleep
	var n atomic.Int32
	if _, err := c.call(context.Background(), failing(&n, se(503))); err == nil {
		t.Fatal("expected failure")
	}
	for i, d := range f.got() {
		lo, hi := time.Duration(1000*math.Pow(2, float64(i)))*time.Millisecond, time.Duration(2000*math.Pow(2, float64(i)))*time.Millisecond
		if d < lo || d > hi {
			t.Errorf("delay %d = %v outside [%v, %v]", i, d, lo, hi)
		}
	}
}

type codeErr struct{ code string }

func (e codeErr) Error() string     { return "boom " + e.code }
func (e codeErr) ErrorCode() string { return e.code }

type nameErr struct{ name string }

func (e nameErr) Error() string     { return "x" }
func (e nameErr) ErrorName() string { return e.name }

type respErr struct {
	msg  string
	resp *http.Response
}

func (e *respErr) Error() string                 { return e.msg }
func (e *respErr) ErrorResponse() *http.Response { return e.resp }

func TestNoRetryClassification(t *testing.T) {
	noRetry := map[string]error{
		"deadline":                context.DeadlineExceeded,
		"wrapped deadline":        &url.Error{Op: "Post", URL: "http://x", Err: context.DeadlineExceeded},
		"canceled":                context.Canceled,
		"wrapped canceled":        fmt.Errorf("send: %w", context.Canceled),
		"message Cancel":          errors.New("Cancelled by user"),
		"message TimeoutError":    errors.New("TimeoutError: slow"),
		"message AbortError":      errors.New("AbortError"),
		"name TimeoutError":       nameErr{"TimeoutError"},
		"code ECONNABORTED":       codeErr{"ECONNABORTED"},
		"wrapped ECONNABORTED":    fmt.Errorf("w: %w", codeErr{"ECONNABORTED"}),
		"statusError 400":         se(400),
		"wrapped statusError 422": fmt.Errorf("w: %w", se(422)),
		"non network TypeError":   &fetchTypeError{Message: "Invalid URL"},
		"Load failed with stack":  &fetchTypeError{Message: "Load failed"},
	}
	for name, e := range noRetry {
		var n atomic.Int32
		c, f := newTestCaller(params(4))
		_, err := c.call(context.Background(), failing(&n, e))
		if !errors.Is(err, e) || n.Load() != 1 || len(f.got()) != 0 {
			t.Errorf("%s: attempts=%d delays=%v err=%v, want 1 attempt, the error itself, no backoff", name, n.Load(), f.got(), err)
		}
	}
	retried := map[string]error{
		"lowercase cancel message": errors.New("cancelled by user"), // startsWith("Cancel") is case-sensitive
		"plain error":              errors.New("something"),
		"io.EOF":                   io.EOF,
		"unexpected EOF":           io.ErrUnexpectedEOF,
		"ECONNRESET":               &url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}},
		"ECONNREFUSED":             &url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		"i/o timeout (not ctx)":    &net.OpError{Op: "read", Net: "tcp", Err: timeoutNetErr{}},
		"other code":               codeErr{"ECONNRESET"},
		"other name":               nameErr{"Whatever"},
		"fetch failed":             &fetchTypeError{Message: "fetch failed"},
		"terminated":               &fetchTypeError{Message: "terminated"},
		"Load failed no stack":     &fetchTypeError{Message: "Load failed", NoStack: true},
		"deno":                     &fetchTypeError{Message: "error sending request for url (http://x)"},
		"wrapped fetch failed":     fmt.Errorf("x: %w", &fetchTypeError{Message: "fetch failed", Cause: io.EOF}),
	}
	for name, e := range retried {
		var n atomic.Int32
		c, f := newTestCaller(params(3), 0)
		_, err := c.call(context.Background(), failing(&n, e))
		if err == nil || n.Load() != 4 || len(f.got()) != 3 {
			t.Errorf("%s: attempts=%d delays=%v err=%v, want 4 attempts, 3 backoffs", name, n.Load(), f.got(), err)
		}
	}
}

type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

func TestIsNetworkError(t *testing.T) {
	yes := []string{"network error", "Failed to fetch", "NetworkError when attempting to fetch resource.",
		"The Internet connection appears to be offline.", "Network request failed", "fetch failed", "terminated",
		" A network error occurred.", "Network connection lost", "error sending request for url (http://a)", "error sending request for url"}
	no := []string{"", "Fetch failed", "fetch failed ", "network  error", "A network error occurred.", "Invalid URL", "Load failed"}
	for _, m := range yes {
		if !isNetworkError(&fetchTypeError{Message: m}) {
			t.Errorf("%q must be a network error", m)
		}
	}
	for _, m := range no {
		if isNetworkError(&fetchTypeError{Message: m}) {
			t.Errorf("%q must not be a network error", m)
		}
	}
	if !isNetworkError(&fetchTypeError{Message: "Load failed", NoStack: true}) {
		t.Error("Load failed without a stack is a network error")
	}
	if isNetworkError(errors.New("fetch failed")) || isNetworkError(nil) || isNetworkError(io.EOF) {
		t.Error("only a TypeError is a network error")
	}
}

func TestFailedResponseHook(t *testing.T) {
	// The hook gets nil for raiseForStatus errors (no `.response`), on every failed attempt incl. the last, but
	// not for the errors thrown before it (timeouts etc.).
	var seen []*http.Response
	p := params(2)
	p.OnFailedResponse = func(r *http.Response) bool { seen = append(seen, r); return false }
	var n atomic.Int32
	c, _ := newTestCaller(p)
	if _, err := c.call(context.Background(), failing(&n, se(503))); err == nil {
		t.Fatal("expected failure")
	}
	if len(seen) != 3 || n.Load() != 3 {
		t.Fatalf("hook calls %d attempts %d, want 3 and 3", len(seen), n.Load())
	}
	for _, r := range seen {
		if r != nil {
			t.Fatal("raiseForStatus errors carry no response: hook must receive nil")
		}
	}
	seen = nil
	n.Store(0)
	if _, err := c.call(context.Background(), failing(&n, context.DeadlineExceeded)); err == nil || len(seen) != 0 {
		t.Fatalf("timeout must be thrown before the hook (hook calls %d, err %v)", len(seen), err)
	}
	// A hook that "handled" the failure skips the status check: the 400 is retried (async_caller.js:107-109).
	p.OnFailedResponse = func(*http.Response) bool { return true }
	c, _ = newTestCaller(p)
	n.Store(0)
	if _, err := c.call(context.Background(), failing(&n, se(400))); err == nil || n.Load() != 3 {
		t.Fatalf("handled 400: attempts %d err %v, want 3 attempts", n.Load(), err)
	}
	// ... but not the errors thrown before it.
	n.Store(0)
	if _, err := c.call(context.Background(), failing(&n, context.Canceled)); err == nil || n.Load() != 1 {
		t.Fatalf("handled cancel: attempts %d, want 1", n.Load())
	}
	// retriesLeft <= 0 still ends a handled failure.
	p.MaxRetries = 0
	c, _ = newTestCaller(p)
	n.Store(0)
	if _, err := c.call(context.Background(), failing(&n, se(400))); err == nil || n.Load() != 1 {
		t.Fatalf("handled with no retries: attempts %d", n.Load())
	}
}

func TestResponseCarryingError(t *testing.T) {
	// An error that does carry a response: the hook receives it and its status beats the error's own status.
	var got *http.Response
	p := params(2)
	p.OnFailedResponse = func(r *http.Response) bool { got = r; return false }
	r503 := &http.Response{StatusCode: 503}
	var n atomic.Int32
	c, _ := newTestCaller(p)
	_, err := c.call(context.Background(), failing(&n, &respErr{msg: "x", resp: r503}))
	if err == nil || got != r503 || n.Load() != 3 {
		t.Fatalf("503 response: attempts %d hook %v err %v", n.Load(), got, err)
	}
	n.Store(0)
	if _, err := c.call(context.Background(), failing(&n, &respErr{msg: "x", resp: &http.Response{StatusCode: 400}})); err == nil || n.Load() != 1 {
		t.Fatalf("400 response must end the call: attempts %d", n.Load())
	}
	n.Store(0) // response.status wins over error.status (`response?.status ?? error.status`)
	both := &respErr{msg: "x", resp: &http.Response{StatusCode: 400}}
	if _, err := c.call(context.Background(), failing(&n, both)); n.Load() != 1 || err == nil {
		t.Fatalf("response 400 vs error status: attempts %d", n.Load())
	}
	n.Store(0) // a nil response falls back to the error's status
	if _, err := c.call(context.Background(), failing(&n, &respErr{msg: "x"})); n.Load() != 3 || err == nil {
		t.Fatalf("no response, no status: attempts %d", n.Load())
	}
}

func TestPreCancelledContextRunsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var n atomic.Int32
	c, _ := newTestCaller(params(4))
	if _, err := c.call(ctx, failing(&n, nil)); !errors.Is(err, context.Canceled) || n.Load() != 0 {
		t.Fatalf("err %v attempts %d, want Canceled and no attempt", err, n.Load())
	}
	if _, total, _, _ := c.state(); total != 0 {
		t.Fatal("call leaked")
	}
}

func TestCancelDuringBackoffReturnsPromptly(t *testing.T) {
	c := newAsyncCaller(params(4)) // real sleeper: the first backoff is >= 1 s
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := c.call(ctx, func(context.Context) (*http.Response, error) { n.Add(1); return nil, se(500) })
		done <- err
	}()
	waitFor(t, "first attempt", func() bool { return n.Load() == 1 })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the backoff")
	}
	if err := c.onIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatalf("attempts %d, want 1", n.Load())
	}
}

func TestResponseBodyClosedOnFailedAttempt(t *testing.T) {
	var closed atomic.Int32
	c, _ := newTestCaller(params(1))
	_, err := c.call(context.Background(), func(context.Context) (*http.Response, error) {
		return &http.Response{Body: closeCounter{Reader: strings.NewReader(""), n: &closed}}, se(500)
	})
	if err == nil || closed.Load() != 2 {
		t.Fatalf("err %v closed %d, want both failed attempts' bodies closed", err, closed.Load())
	}
}

type closeCounter struct {
	io.Reader
	n *atomic.Int32
}

func (c closeCounter) Close() error { c.n.Add(1); return nil }

func TestPanicInCallReleasesSlot(t *testing.T) {
	c, _ := newTestCaller(callerParams{MaxRetries: 0, MaxConcurrency: 1, MaxQueueSizeBytes: 10})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic must propagate")
			}
		}()
		c.callWithOptions(context.Background(), 4, func(context.Context) (*http.Response, error) { panic("x") })
	}()
	if r, total, q, b := c.state(); r != 0 || total != 0 || q != 0 || b != 0 {
		t.Fatalf("state after panic: running %d total %d queued %d bytes %d", r, total, q, b)
	}
	var n atomic.Int32
	if _, err := c.call(context.Background(), failing(&n, nil)); err != nil {
		t.Fatal(err)
	}
}

// ---- size budget ------------------------------------------------------------------------------------------

func TestQueueSizeLimit(t *testing.T) {
	c, _ := newTestCaller(callerParams{MaxRetries: 0, MaxConcurrency: 1, MaxQueueSizeBytes: 100})
	release := make(chan struct{})
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := c.callWithOptions(context.Background(), 60, func(context.Context) (*http.Response, error) {
			close(started)
			<-release
			return &http.Response{}, nil
		})
		done <- err
	}()
	<-started
	_, err := c.callWithOptions(context.Background(), 50, func(context.Context) (*http.Response, error) {
		t.Error("rejected call must not run")
		return nil, nil
	})
	const want = "Queue size limit (100 bytes) exceeded. Current queue size: 60 bytes, attempted addition: 50 bytes."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %q", err, want)
	}
	if _, total, _, b := c.state(); total != 1 || b != 60 {
		t.Fatalf("a rejected call must not change the accounting: total %d bytes %d", total, b)
	}
	// 40 fits (60 + 40 = 100 is not > 100) and queues behind the running call.
	var ran atomic.Bool
	second := make(chan error, 1)
	go func() {
		_, err := c.callWithOptions(context.Background(), 40, func(context.Context) (*http.Response, error) { ran.Store(true); return &http.Response{}, nil })
		second <- err
	}()
	waitFor(t, "second call queued", func() bool { _, _, q, _ := c.state(); return q == 1 })
	if _, _, _, b := c.state(); b != 100 {
		t.Fatalf("queued bytes counted: %d, want 100", b)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil || !ran.Load() {
		t.Fatalf("queued call: %v ran=%v", err, ran.Load())
	}
	if err := c.onIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, total, _, b := c.state(); total != 0 || b != 0 {
		t.Fatalf("bytes not released: total %d bytes %d", total, b)
	}
}

func TestQueueSizeLimitEdges(t *testing.T) {
	ok := func(context.Context) (*http.Response, error) { return &http.Response{}, nil }
	// sizeBytes <= 0 is never limited (`sizeBytes > 0 &&`), even with a zero budget.
	c, _ := newTestCaller(callerParams{MaxQueueSizeBytes: 0})
	for _, sz := range []int64{0, -5} {
		if _, err := c.callWithOptions(context.Background(), sz, ok); err != nil {
			t.Errorf("size %d: %v", sz, err)
		}
	}
	if _, err := c.callWithOptions(context.Background(), 1, ok); err == nil ||
		err.Error() != "Queue size limit (0 bytes) exceeded. Current queue size: 0 bytes, attempted addition: 1 bytes." {
		t.Fatalf("zero budget: %v", err)
	}
	// Unlimited (undefined) budget.
	c, _ = newTestCaller(callerParams{MaxQueueSizeBytes: -1})
	if _, err := c.callWithOptions(context.Background(), math.MaxInt64/2, ok); err != nil {
		t.Fatal(err)
	}
	// A failing call releases its bytes (promise.finally).
	c, _ = newTestCaller(callerParams{MaxRetries: 0, MaxQueueSizeBytes: 10})
	if _, err := c.callWithOptions(context.Background(), 10, func(context.Context) (*http.Response, error) { return nil, se(500) }); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := c.callWithOptions(context.Background(), 10, ok); err != nil {
		t.Fatalf("budget not released after failure: %v", err)
	}
}

// ---- queue (PQueue) ---------------------------------------------------------------------------------------

func TestConcurrencyBound(t *testing.T) {
	for _, limit := range []int{1, 3, 8} {
		c, _ := newTestCaller(callerParams{MaxRetries: 0, MaxConcurrency: limit, MaxQueueSizeBytes: -1})
		var cur, peak, done atomic.Int32
		var wg sync.WaitGroup
		gate := make(chan struct{})
		const calls = 40
		for range calls {
			wg.Go(func() {
				_, err := c.call(context.Background(), func(context.Context) (*http.Response, error) {
					v := cur.Add(1)
					for {
						p := peak.Load()
						if v <= p || peak.CompareAndSwap(p, v) {
							break
						}
					}
					<-gate
					cur.Add(-1)
					done.Add(1)
					return &http.Response{}, nil
				})
				if err != nil {
					t.Error(err)
				}
			})
		}
		waitFor(t, "slots filled", func() bool { r, total, _, _ := c.state(); return r == limit && total == calls })
		if cur.Load() != int32(limit) {
			t.Fatalf("limit %d: %d running", limit, cur.Load())
		}
		close(gate)
		wg.Wait()
		if int(peak.Load()) != limit || done.Load() != calls {
			t.Fatalf("limit %d: peak %d done %d", limit, peak.Load(), done.Load())
		}
	}
}

func TestUnlimitedConcurrency(t *testing.T) {
	for _, mc := range []int{0, -1} {
		c, _ := newTestCaller(callerParams{MaxRetries: 0, MaxConcurrency: mc, MaxQueueSizeBytes: -1})
		var wg sync.WaitGroup
		gate := make(chan struct{})
		const calls = 64
		for range calls {
			wg.Go(func() {
				c.call(context.Background(), func(context.Context) (*http.Response, error) { <-gate; return &http.Response{}, nil })
			})
		}
		waitFor(t, "all running", func() bool { r, _, q, _ := c.state(); return r == calls && q == 0 })
		close(gate)
		wg.Wait()
	}
}

func TestQueueIsFIFO(t *testing.T) {
	c, _ := newTestCaller(callerParams{MaxRetries: 0, MaxConcurrency: 1, MaxQueueSizeBytes: -1})
	gate := make(chan struct{})
	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			c.call(context.Background(), func(context.Context) (*http.Response, error) {
				mu.Lock()
				order = append(order, i)
				mu.Unlock()
				if i == 0 {
					<-gate
				}
				return &http.Response{}, nil
			})
		})
		// Each call is parked (or running) before the next one is submitted.
		waitFor(t, "call submitted", func() bool { _, total, _, _ := c.state(); return total == i+1 })
	}
	close(gate)
	wg.Wait()
	for i, v := range order {
		if v != i {
			t.Fatalf("order %v, want submission order", order)
		}
	}
}

func TestSlotHeldDuringBackoff(t *testing.T) {
	// pRetry runs inside queue.add: a call that waits to retry keeps its concurrency slot.
	c, f := newTestCaller(callerParams{MaxRetries: 1, MaxConcurrency: 1, MaxQueueSizeBytes: -1})
	f.block = make(chan struct{})
	var first, second atomic.Int32
	var wg sync.WaitGroup
	wg.Go(func() {
		c.call(context.Background(), func(context.Context) (*http.Response, error) {
			if first.Add(1) == 1 {
				return nil, se(500)
			}
			return &http.Response{}, nil
		})
	})
	waitFor(t, "first call backing off", func() bool { return len(f.got()) == 1 })
	wg.Go(func() {
		c.call(context.Background(), func(context.Context) (*http.Response, error) { second.Add(1); return &http.Response{}, nil })
	})
	waitFor(t, "second call parked", func() bool { _, _, q, _ := c.state(); return q == 1 })
	if second.Load() != 0 {
		t.Fatal("second call ran while the first holds the slot during its backoff")
	}
	close(f.block)
	wg.Wait()
	if first.Load() != 2 || second.Load() != 1 {
		t.Fatalf("attempts first %d second %d", first.Load(), second.Load())
	}
}

func TestCancelWhileQueued(t *testing.T) {
	c, _ := newTestCaller(callerParams{MaxRetries: 0, MaxConcurrency: 1, MaxQueueSizeBytes: 100})
	gate := make(chan struct{})
	holder := make(chan struct{})
	go c.call(context.Background(), func(context.Context) (*http.Response, error) { close(holder); <-gate; return &http.Response{}, nil })
	<-holder
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.callWithOptions(ctx, 30, func(context.Context) (*http.Response, error) {
			t.Error("cancelled queued call must not run")
			return nil, nil
		})
		errc <- err
	}()
	waitFor(t, "parked", func() bool { _, _, q, b := c.state(); return q == 1 && b == 30 })
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if r, total, q, b := c.state(); r != 1 || total != 1 || q != 0 || b != 0 {
		t.Fatalf("after cancel: running %d total %d queued %d bytes %d", r, total, q, b)
	}
	close(gate)
	if err := c.onIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestCancelRaceKeepsSlots hammers the grant-vs-cancel race: no slot, byte or call may leak.
func TestCancelRaceKeepsSlots(t *testing.T) {
	c, _ := newTestCaller(callerParams{MaxRetries: 0, MaxConcurrency: 2, MaxQueueSizeBytes: 1 << 20})
	var wg sync.WaitGroup
	for i := range 400 {
		wg.Go(func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if i%2 == 0 {
				go cancel()
			}
			c.callWithOptions(ctx, 10, func(context.Context) (*http.Response, error) { runtime.Gosched(); return &http.Response{}, nil })
		})
	}
	wg.Wait()
	if err := c.onIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r, total, q, b := c.state(); r != 0 || total != 0 || q != 0 || b != 0 {
		t.Fatalf("leak: running %d total %d queued %d bytes %d", r, total, q, b)
	}
	// Both slots still usable.
	gate := make(chan struct{})
	var in atomic.Int32
	for range 2 {
		go c.call(context.Background(), func(context.Context) (*http.Response, error) { in.Add(1); <-gate; return &http.Response{}, nil })
	}
	waitFor(t, "both slots", func() bool { return in.Load() == 2 })
	close(gate)
}

func TestOnIdle(t *testing.T) {
	c, _ := newTestCaller(callerParams{MaxRetries: 0, MaxConcurrency: 1, MaxQueueSizeBytes: -1})
	if err := c.onIdle(context.Background()); err != nil {
		t.Fatalf("idle caller: %v", err)
	}
	gate := make(chan struct{})
	var finished atomic.Int32
	for range 3 {
		go c.call(context.Background(), func(context.Context) (*http.Response, error) { <-gate; finished.Add(1); return &http.Response{}, nil })
	}
	waitFor(t, "3 calls", func() bool { _, total, _, _ := c.state(); return total == 3 })
	// Bounded by ctx.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if err := c.onIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("onIdle with busy caller: %v", err)
	}
	cancel()
	// Several waiters are all released, and only when every queued call has finished.
	var wg sync.WaitGroup
	var released atomic.Int32
	for range 4 {
		wg.Go(func() {
			if err := c.onIdle(context.Background()); err != nil {
				t.Error(err)
			}
			if finished.Load() != 3 {
				t.Errorf("onIdle returned with %d of 3 calls finished", finished.Load())
			}
			released.Add(1)
		})
	}
	close(gate)
	wg.Wait()
	if released.Load() != 4 {
		t.Fatal("waiters not released")
	}
	// Reusable.
	gate2 := make(chan struct{})
	go c.call(context.Background(), func(context.Context) (*http.Response, error) { <-gate2; return &http.Response{}, nil })
	waitFor(t, "call", func() bool { _, total, _, _ := c.state(); return total == 1 })
	idle := make(chan error, 1)
	go func() { idle <- c.onIdle(context.Background()) }()
	select {
	case <-idle:
		t.Fatal("onIdle returned while a call runs")
	case <-time.After(20 * time.Millisecond):
	}
	close(gate2)
	if err := <-idle; err != nil {
		t.Fatal(err)
	}
}

func TestOnIdleWaitsForRetryingCall(t *testing.T) {
	c, f := newTestCaller(callerParams{MaxRetries: 2, MaxQueueSizeBytes: -1})
	f.block = make(chan struct{})
	go c.call(context.Background(), func(context.Context) (*http.Response, error) { return nil, se(500) })
	waitFor(t, "backoff", func() bool { return len(f.got()) == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.onIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("onIdle during a backoff: %v", err)
	}
	close(f.block)
	if err := c.onIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()
	for range 3 {
		c, _ := newTestCaller(callerParams{MaxRetries: 2, MaxConcurrency: 3, MaxQueueSizeBytes: 1 << 20}, 0)
		var wg sync.WaitGroup
		for i := range 60 {
			wg.Go(func() {
				ctx, cancel := context.WithCancel(context.Background())
				if i%5 == 0 {
					cancel()
				}
				defer cancel()
				c.callWithOptions(ctx, 5, func(context.Context) (*http.Response, error) {
					if i%3 == 0 {
						return nil, se(503)
					}
					return &http.Response{}, nil
				})
			})
		}
		wg.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := c.onIdle(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, total, _, _ := c.state(); total != 0 {
			t.Fatal("calls leaked")
		}
	}
	waitFor(t, "goroutines to settle", func() bool { return runtime.NumGoroutine() <= before })
}

func TestRealSleeperReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("sleepCtx: %v after %v", err, time.Since(start))
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestNewAsyncCallerDefaults(t *testing.T) {
	c := newAsyncCaller(callerParams{MaxRetries: -1, MaxQueueSizeBytes: -1})
	if c.maxRetries != 6 {
		t.Fatalf("default maxRetries %d, want 6", c.maxRetries)
	}
	if c.sleep == nil || c.jitter == nil {
		t.Fatal("default clock missing")
	}
	for range 1000 {
		if j := c.jitter(); j < 0 || j >= 1 {
			t.Fatalf("jitter %v outside [0, 1)", j)
		}
	}
}

// ---- raiseForStatus ---------------------------------------------------------------------------------------

type errReader struct{ after string }

func (e *errReader) Read(p []byte) (int, error) {
	if e.after != "" {
		n := copy(p, e.after)
		e.after = e.after[n:]
		return n, nil
	}
	return 0, io.ErrUnexpectedEOF
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func mkResp(status int, statusLine, body string) (*http.Response, *trackedBody) {
	b := &trackedBody{Reader: strings.NewReader(body)}
	return &http.Response{StatusCode: status, Status: statusLine, Body: b}, b
}

func TestRaiseForStatus(t *testing.T) {
	const org = "This API key is org-scoped and requires workspace specification. Please provide 'workspaceId' parameter, or set LANGSMITH_WORKSPACE_ID environment variable."
	cases := []struct {
		name         string
		status       int
		line, body   string
		wantMsg      string
		wantName     string
		wantNotFound bool
		wantConflict bool
	}{
		{"400", 400, "400 Bad Request", "bad", "Failed to op. Received status [400]: Bad Request. Message: bad", "Error", false, false},
		{"404", 404, "404 Not Found", "nope", "Failed to op. Received status [404]: Not Found. Message: nope", "LangSmithNotFoundError", true, false},
		{"409", 409, "409 Conflict", "dup", "Failed to op. Received status [409]: Conflict. Message: dup", "LangSmithConflictError", false, true},
		{"empty body", 500, "500 Internal Server Error", "", "Failed to op. Received status [500]: Internal Server Error. Message: ", "Error", false, false},
		{"custom reason", 418, "418 I'm a teapot", "x", "Failed to op. Received status [418]: I'm a teapot. Message: x", "Error", false, false},
		{"no reason", 500, "500", "x", "Failed to op. Received status [500]: . Message: x", "Error", false, false},
		{"no status line", 503, "", "x", "Failed to op. Received status [503]: Service Unavailable. Message: x", "Error", false, false},
		{"403 org scoped", 403, "403 Forbidden", `{"error":"org_scoped_key_requires_workspace"}`, "Failed to op. Received status [403]: Forbidden. Message: " + org, "Error", false, false},
		{"403 org scoped extra keys", 403, "403 Forbidden", ` {"a":[1,{"b":null}], "error":"org_scoped_key_requires_workspace"} `, "Failed to op. Received status [403]: Forbidden. Message: " + org, "Error", false, false},
		{"403 org scoped escaped", 403, "403 Forbidden", `{"error":"org_scoped_key_requires_\u0077orkspace"}`, "Failed to op. Received status [403]: Forbidden. Message: " + org, "Error", false, false},
		{"403 last duplicate wins", 403, "403 Forbidden", `{"error":"x","error":"org_scoped_key_requires_workspace"}`, "Failed to op. Received status [403]: Forbidden. Message: " + org, "Error", false, false},
		{"403 other json", 403, "403 Forbidden", `{"error":"nope"}`, `Failed to op. Received status [403]: Forbidden. Message: {"error":"nope"}`, "Error", false, false},
		{"403 key case", 403, "403 Forbidden", `{"ERROR":"org_scoped_key_requires_workspace"}`, `Failed to op. Received status [403]: Forbidden. Message: {"ERROR":"org_scoped_key_requires_workspace"}`, "Error", false, false},
		{"403 non string", 403, "403 Forbidden", `{"error":5}`, `Failed to op. Received status [403]: Forbidden. Message: {"error":5}`, "Error", false, false},
		{"403 html", 403, "403 Forbidden", `<html>`, "Failed to op. Received status [403]: Forbidden. Message: <html>", "Error", false, false},
		{"403 array", 403, "403 Forbidden", `[]`, "Failed to op. Received status [403]: Forbidden. Message: []", "Error", false, false},
		{"403 null", 403, "403 Forbidden", `null`, "Failed to op. Received status [403]: Forbidden. Message: null", "Error", false, false},
		{"403 string", 403, "403 Forbidden", `"org_scoped_key_requires_workspace"`, `Failed to op. Received status [403]: Forbidden. Message: "org_scoped_key_requires_workspace"`, "Error", false, false},
		{"401 org scoped body untouched", 401, "401 Unauthorized", `{"error":"org_scoped_key_requires_workspace"}`, `Failed to op. Received status [401]: Unauthorized. Message: {"error":"org_scoped_key_requires_workspace"}`, "Error", false, false},
		{"bom", 500, "500 Internal Server Error", "\xef\xbb\xbfhi", "Failed to op. Received status [500]: Internal Server Error. Message: hi", "Error", false, false},
		{"redirect is an error", 302, "302 Found", "", "Failed to op. Received status [302]: Found. Message: ", "Error", false, false},
		{"1xx is an error", 199, "199 X", "", "Failed to op. Received status [199]: X. Message: ", "Error", false, false},
		{"300 is an error", 300, "300 Multiple Choices", "", "Failed to op. Received status [300]: Multiple Choices. Message: ", "Error", false, false},
	}
	for _, c := range cases {
		resp, body := mkResp(c.status, c.line, c.body)
		err := raiseForStatus(resp, "op", false)
		var got *statusError
		if !errors.As(err, &got) {
			t.Errorf("%s: err %v", c.name, err)
			continue
		}
		if got.Message != c.wantMsg || got.Error() != c.wantMsg || got.Status != c.status || got.ErrorName() != c.wantName {
			t.Errorf("%s:\n got %q (%d %s)\nwant %q (%d %s)", c.name, got.Message, got.Status, got.ErrorName(), c.wantMsg, c.status, c.wantName)
		}
		if isLangSmithNotFoundError(err) != c.wantNotFound || isLangSmithConflictError(err) != c.wantConflict {
			t.Errorf("%s: not-found %v conflict %v", c.name, isLangSmithNotFoundError(err), isLangSmithConflictError(err))
		}
		if !body.closed {
			t.Errorf("%s: error body not closed", c.name)
		}
	}
}

func TestRaiseForStatusSuccess(t *testing.T) {
	for _, status := range []int{200, 201, 204, 299} {
		resp, body := mkResp(status, fmt.Sprint(status, " OK"), "payload")
		if err := raiseForStatus(resp, "op", false); err != nil {
			t.Errorf("%d: %v", status, err)
		}
		if body.closed {
			t.Errorf("%d: an unconsumed success body belongs to the caller", status)
		}
		if rest, _ := io.ReadAll(resp.Body); string(rest) != "payload" {
			t.Errorf("%d: body consumed: %q", status, rest)
		}
		resp, body = mkResp(status, fmt.Sprint(status, " OK"), "payload")
		if err := raiseForStatus(resp, "op", true); err != nil {
			t.Errorf("%d consume: %v", status, err)
		}
		if rest, _ := io.ReadAll(body); len(rest) != 0 || !body.closed {
			t.Errorf("%d consume: body not drained/closed", status)
		}
	}
	// nil body
	if err := raiseForStatus(&http.Response{StatusCode: 200}, "op", true); err != nil {
		t.Fatal(err)
	}
	err := raiseForStatus(&http.Response{StatusCode: 500, Status: "500 Internal Server Error"}, "op", false)
	if err == nil || err.Error() != "Failed to op. Received status [500]: Internal Server Error. Message: " {
		t.Fatalf("nil error body: %v", err)
	}
	// A body that fails while being consumed on success rejects (`await response.text()`, error.js:90)...
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(&errReader{after: "ab"})}
	if err := raiseForStatus(resp, "op", true); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("consume error: %v", err)
	}
	// ... and is a retryable (network) failure for the caller.
	var n atomic.Int32
	c, _ := newTestCaller(params(2), 0)
	_, cerr := c.call(context.Background(), func(context.Context) (*http.Response, error) {
		n.Add(1)
		r := &http.Response{StatusCode: 200, Body: io.NopCloser(&errReader{})}
		if err := raiseForStatus(r, "op", true); err != nil {
			return nil, err
		}
		return r, nil
	})
	if !errors.Is(cerr, io.ErrUnexpectedEOF) || n.Load() != 3 {
		t.Fatalf("attempts %d err %v", n.Load(), cerr)
	}
	// ... while on the failure path it yields an empty message body (error.js:93-97).
	resp = &http.Response{StatusCode: 500, Status: "500 Internal Server Error", Body: io.NopCloser(&errReader{after: "partial"})}
	if err := raiseForStatus(resp, "op", false); err == nil || err.Error() != "Failed to op. Received status [500]: Internal Server Error. Message: " {
		t.Fatalf("failing error body: %v", err)
	}
}

func TestStatusErrorNames(t *testing.T) {
	if (&statusError{}).ErrorName() != "Error" || (&statusError{Name: "LangSmithNotFoundError"}).ErrorName() != "LangSmithNotFoundError" {
		t.Fatal("ErrorName")
	}
	for _, e := range []error{nil, errors.New("x"), se(500), io.EOF} {
		if isLangSmithNotFoundError(e) || isLangSmithConflictError(e) {
			t.Errorf("%v is neither", e)
		}
	}
	nf := fmt.Errorf("wrap: %w", &statusError{Status: 404, Name: "LangSmithNotFoundError"})
	if !isLangSmithNotFoundError(nf) || isLangSmithConflictError(nf) {
		t.Fatal("wrapped not-found")
	}
}

func TestUTF8DecodeLossy(t *testing.T) {
	same := []string{"", "plain", "caf\u00e9 \U0001F600", "\u2028\uFFFD"}
	for _, s := range same {
		if got := utf8DecodeLossy([]byte(s)); got != s {
			t.Errorf("%q -> %q", s, got)
		}
	}
	// Node 24 strips up to two leading BOMs (golden "utf8 double bom"); a third and later ones stay.
	for in, want := range map[string]string{
		"\xef\xbb\xbfa": "a", "\xef\xbb\xbf\xef\xbb\xbfa": "a", "\xef\xbb\xbf\xef\xbb\xbf\xef\xbb\xbfa": "\ufeffa",
		"\xef\xbb\xbfa\xef\xbb\xbf": "a\ufeff", "\xef\xbb\xbf\xef\xbb\xbf": "", "\xef\xbb": "\ufffd",
	} {
		if got := utf8DecodeLossy([]byte(in)); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	for _, b := range [][]byte{{0xff}, {0xc0, 0x80}, {0xe2, 0x82}, {0xed, 0xa0, 0x80}, {0xf4, 0x90, 0x80, 0x80}, []byte("a\xe2\x28\xa1")} {
		if s := utf8DecodeLossy(b); !utf8.ValidString(s) {
			t.Errorf("% x -> invalid %q", b, s)
		}
	}
}

// ---- benchmarks -------------------------------------------------------------------------------------------

func BenchmarkCallSuccess(b *testing.B) {
	c := newAsyncCaller(callerParams{MaxRetries: 4, MaxQueueSizeBytes: -1})
	resp := &http.Response{StatusCode: 200}
	fn := func(context.Context) (*http.Response, error) { return resp, nil }
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		c.call(ctx, fn)
	}
}

func BenchmarkCallWithOptionsSuccess(b *testing.B) {
	c := newAsyncCaller(callerParams{MaxRetries: 4, MaxConcurrency: 4, MaxQueueSizeBytes: 1 << 30})
	resp := &http.Response{StatusCode: 200}
	fn := func(context.Context) (*http.Response, error) { return resp, nil }
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		c.callWithOptions(ctx, 1024, fn)
	}
}

func BenchmarkCallParallelBounded(b *testing.B) {
	c := newAsyncCaller(callerParams{MaxRetries: 4, MaxConcurrency: 4, MaxQueueSizeBytes: -1})
	resp := &http.Response{StatusCode: 200}
	fn := func(context.Context) (*http.Response, error) { return resp, nil }
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.call(ctx, fn)
		}
	})
}

func BenchmarkCallRetryOnce(b *testing.B) {
	c, _ := newTestCaller(callerParams{MaxRetries: 4, MaxQueueSizeBytes: -1}, 0.5)
	bad, good := se(503), &http.Response{StatusCode: 200}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		first := true
		c.call(ctx, func(context.Context) (*http.Response, error) {
			if first {
				first = false
				return nil, bad
			}
			return good, nil
		})
	}
}

func BenchmarkRetryDelay(b *testing.B) {
	b.ReportAllocs()
	var sink time.Duration
	for i := 0; b.Loop(); i++ {
		sink += retryDelay(i&7, 0.37)
	}
	_ = sink
}

func BenchmarkRaiseForStatusOK(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil))}
		raiseForStatus(resp, "create run", true)
	}
}

func BenchmarkRaiseForStatusError(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		resp := &http.Response{StatusCode: 500, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader(`{"detail":"boom"}`))}
		raiseForStatus(resp, "create run", true)
	}
}

func BenchmarkRaiseForStatus403(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		resp := &http.Response{StatusCode: 403, Status: "403 Forbidden", Body: io.NopCloser(strings.NewReader(`{"error":"org_scoped_key_requires_workspace"}`))}
		raiseForStatus(resp, "create run", true)
	}
}

func BenchmarkUTF8DecodeLossyValid(b *testing.B) {
	data := []byte(strings.Repeat("caf\u00e9 ok ", 100))
	b.ReportAllocs()
	for b.Loop() {
		utf8DecodeLossy(data)
	}
}

func BenchmarkUTF8DecodeLossyInvalid(b *testing.B) {
	data := []byte(strings.Repeat("caf\u00e9 \xff ok ", 100))
	b.ReportAllocs()
	for b.Loop() {
		utf8DecodeLossy(data)
	}
}
