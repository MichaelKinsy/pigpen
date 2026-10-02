package typesafe

import (
	"context"
	"errors"
	"math"
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"
)

func h(pairs ...string) http.Header {
	out := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i], pairs[i+1])
	}
	return out
}

func policyWith(mutate func(*RetryPolicy)) RetryPolicy {
	p := DefaultRetryPolicy()
	mutate(&p)
	return p
}

func ms(n int64) time.Duration { return time.Duration(n) * time.Millisecond }

func TestDefaultRetryPolicy_UsesTheSDKRetryDefaults(t *testing.T) {
	twin(t, "retry.test.ts | DEFAULT_RETRY_POLICY uses the SDK retry defaults")
	p := DefaultRetryPolicy()
	want := []int{408, 429}
	for s := 500; s < 600; s++ {
		want = append(want, s)
	}
	statuses := append([]int(nil), p.HTTPStatuses...)
	sort.Ints(statuses)
	eq(t, statuses, want)
	p.HTTPStatuses = nil
	eq(t, p, RetryPolicy{MaxRetries: 2, BackoffInitial: ms(500), BackoffMax: ms(5000), BackoffJitter: 0.25, RespectRetryAfter: true, MaxRetryAfter: ms(60000), APIConnectionError: true, APITimeoutError: true})
}

func TestIsRetryableStatus_ByDefault(t *testing.T) {
	twin(t,
		"retry.test.ts | isRetryableStatus retries 408 by default", "retry.test.ts | isRetryableStatus retries 429 by default",
		"retry.test.ts | isRetryableStatus retries 500 by default", "retry.test.ts | isRetryableStatus retries 502 by default",
		"retry.test.ts | isRetryableStatus retries 503 by default", "retry.test.ts | isRetryableStatus retries 504 by default",
		"retry.test.ts | isRetryableStatus retries 529 by default", "retry.test.ts | isRetryableStatus retries 599 by default",
		"retry.test.ts | isRetryableStatus does not retry 200 by default", "retry.test.ts | isRetryableStatus does not retry 400 by default",
		"retry.test.ts | isRetryableStatus does not retry 401 by default", "retry.test.ts | isRetryableStatus does not retry 403 by default",
		"retry.test.ts | isRetryableStatus does not retry 404 by default", "retry.test.ts | isRetryableStatus does not retry 409 by default",
		"retry.test.ts | isRetryableStatus does not retry 422 by default", "retry.test.ts | isRetryableStatus does not retry 600 by default")
	p := DefaultRetryPolicy()
	for _, s := range []int{408, 429, 500, 502, 503, 504, 529, 599} {
		if !p.RetriesStatus(s) {
			t.Fatalf("%d must be retried", s)
		}
	}
	for _, s := range []int{200, 400, 401, 403, 404, 409, 422, 600} {
		if p.RetriesStatus(s) {
			t.Fatalf("%d must not be retried", s)
		}
	}
}

func TestIsRetryableStatus_ConsultsThePolicysStatusSet(t *testing.T) {
	twin(t, "retry.test.ts | isRetryableStatus consults the policy's status set")
	p := policyWith(func(p *RetryPolicy) { p.HTTPStatuses = []int{409} })
	if !p.RetriesStatus(409) || p.RetriesStatus(503) {
		t.Fatal("only 409 is retried")
	}
	if policyWith(func(p *RetryPolicy) { p.HTTPStatuses = []int{} }).RetriesStatus(503) {
		t.Fatal("an empty set retries nothing")
	}
}

func TestParseRetryAfter_ReadsRetryAfterInSeconds(t *testing.T) {
	twin(t, "retry.test.ts | parseRetryAfter reads Retry-After in seconds")
	now := time.Now()
	for _, tc := range []struct {
		v    string
		want time.Duration
	}{{"3", 3000 * time.Millisecond}, {"0", 0}, {"1.5", 1500 * time.Millisecond}} {
		d, ok := ParseRetryAfter(h("retry-after", tc.v), now)
		if !ok || d != tc.want {
			t.Fatalf("%q: got %v %v, want %v", tc.v, d, ok, tc.want)
		}
	}
}

func TestParseRetryAfter_PrefersRetryAfterMsWhenPresent(t *testing.T) {
	twin(t, "retry.test.ts | parseRetryAfter prefers retry-after-ms when present")
	d, ok := ParseRetryAfter(h("retry-after-ms", "250", "retry-after", "3"), time.Now())
	if !ok || d != ms(250) {
		t.Fatalf("got %v %v", d, ok)
	}
}

func TestParseRetryAfter_ReadsAnHTTPDateRelativeToNow(t *testing.T) {
	twin(t, "retry.test.ts | parseRetryAfter reads an HTTP date relative to now")
	now := time.Date(2026, 10, 21, 7, 28, 0, 0, time.UTC)
	d, ok := ParseRetryAfter(h("retry-after", "Wed, 21 Oct 2026 07:28:05 GMT"), now)
	if !ok || d != 5*time.Second {
		t.Fatalf("got %v %v", d, ok)
	}
	d, ok = ParseRetryAfter(h("retry-after", "Wed, 21 Oct 2026 07:27:00 GMT"), now)
	if !ok || d != 0 {
		t.Fatalf("a past date is zero: %v %v", d, ok)
	}
}

func TestParseRetryAfter_ReturnsUndefinedForMissingOrGarbageValues(t *testing.T) {
	twin(t, "retry.test.ts | parseRetryAfter returns undefined for missing or garbage values")
	now := time.Now()
	for _, hdr := range []http.Header{h(), h("retry-after", "soon"), h("retry-after", "-5"), h("retry-after-ms", "nope")} {
		if d, ok := ParseRetryAfter(hdr, now); ok {
			t.Fatalf("%v: want none, got %v", hdr, d)
		}
	}
}

func TestParseRetryAfter_FollowsJavaScriptNumberSemantics(t *testing.T) {
	// Number(" 5 "), Number("1e3"), Number("0x10") and Number("") are finite in JavaScript.
	now := time.Now()
	for _, tc := range []struct {
		v    string
		want time.Duration
		ok   bool
	}{{" 5 ", 5 * time.Second, true}, {"1e3", 1000 * time.Second, true}, {"0x10", 16 * time.Second, true}, {"", 0, true}, {"Infinity", 0, false}, {"inf", 0, false}, {"nan", 0, false}, {"1_0", 0, false}} {
		d, ok := ParseRetryAfter(h("retry-after", tc.v), now)
		if ok != tc.ok || (ok && d != tc.want) {
			t.Fatalf("%q: got %v %v, want %v %v", tc.v, d, ok, tc.want, tc.ok)
		}
	}
}

func TestRetryDelayMs_GrowsExponentiallyFrom500msAndCapsAt5s(t *testing.T) {
	twin(t, "retry.test.ts | retryDelayMs grows exponentially from 500ms and caps at 5s")
	var got []time.Duration
	for a := 0; a < 6; a++ {
		got = append(got, DefaultRetryPolicy().Delay(a, nil, func() float64 { return 0 }))
	}
	eq(t, got, []time.Duration{ms(500), ms(1000), ms(2000), ms(4000), ms(5000), ms(5000)})
}

func TestRetryDelayMs_ShavesOffAtMost25PercentAsJitter(t *testing.T) {
	twin(t, "retry.test.ts | retryDelayMs shaves off at most 25% as jitter")
	p := DefaultRetryPolicy()
	eq(t, p.Delay(0, nil, func() float64 { return 1 }), ms(375))
	eq(t, p.Delay(1, nil, func() float64 { return 0.5 }), ms(875))
}

func TestRetryDelayMs_HonorsRetryAfterExactlyWithNoJitter(t *testing.T) {
	twin(t, "retry.test.ts | retryDelayMs honors Retry-After exactly, with no jitter")
	p := DefaultRetryPolicy()
	one := func() float64 { return 1 }
	eq(t, p.Delay(0, h("retry-after", "2"), one), ms(2000))
	eq(t, p.Delay(3, h("retry-after-ms", "10"), one), ms(10))
}

func TestRetryDelayMs_FallsBackToBackoffWhenRetryAfterExceedsOneMinute(t *testing.T) {
	twin(t, "retry.test.ts | retryDelayMs falls back to backoff when Retry-After exceeds one minute")
	p := DefaultRetryPolicy()
	zero := func() float64 { return 0 }
	eq(t, p.Delay(0, h("retry-after", "61"), zero), ms(500))
	eq(t, p.Delay(0, h("retry-after", "60"), zero), ms(60000))
	eq(t, p.Delay(0, h("retry-after-ms", "60000"), zero), ms(60000))
	eq(t, p.Delay(0, h("retry-after-ms", "60001"), func() float64 { return 1 }), ms(375))
}

func TestRetryDelayMs_UsesThePolicysInitialDelayCapAndJitter(t *testing.T) {
	twin(t, "retry.test.ts | retryDelayMs uses the policy's initial delay, cap, and jitter")
	p := policyWith(func(p *RetryPolicy) { p.BackoffInitial, p.BackoffMax, p.BackoffJitter = ms(100), ms(350), 0.5 })
	var got []time.Duration
	for a := 0; a < 4; a++ {
		got = append(got, p.Delay(a, nil, func() float64 { return 0 }))
	}
	eq(t, got, []time.Duration{ms(100), ms(200), ms(350), ms(350)})
	eq(t, p.Delay(0, nil, func() float64 { return 1 }), ms(50))
	eq(t, policyWith(func(p *RetryPolicy) { p.BackoffJitter = 0 }).Delay(0, nil, func() float64 { return 1 }), ms(500))
}

func TestRetryDelayMs_CanIgnoreRetryAfterEntirely(t *testing.T) {
	twin(t, "retry.test.ts | retryDelayMs can ignore Retry-After entirely")
	p := policyWith(func(p *RetryPolicy) { p.RespectRetryAfter = false })
	eq(t, p.Delay(0, h("retry-after", "2"), func() float64 { return 0 }), ms(500))
}

func TestRetryDelayMs_UsesThePolicysRetryAfterCeiling(t *testing.T) {
	twin(t, "retry.test.ts | retryDelayMs uses the policy's Retry-After ceiling")
	p := policyWith(func(p *RetryPolicy) { p.MaxRetryAfter = ms(1000) })
	zero := func() float64 { return 0 }
	eq(t, p.Delay(0, h("retry-after", "1"), zero), ms(1000))
	eq(t, p.Delay(0, h("retry-after", "2"), zero), ms(500))
}

// Go-specific (review): a server delay beyond time.Duration's range must not wrap to a
// negative duration. JavaScript compares the huge number with the ceiling and falls back
// to backoff; a wrapped Go value passed the ceiling check and retried at once.
func TestRetryDelayMs_HugeRetryAfterFallsBackToBackoffNotANegativeDelay(t *testing.T) {
	p := DefaultRetryPolicy()
	zero := func() float64 { return 0 }
	for _, hdr := range []http.Header{
		h("retry-after", "1e12"), h("retry-after", "9999999999999"), h("retry-after", "1e300"),
		h("retry-after-ms", "1e13"), h("retry-after-ms", "1e300"),
	} {
		if d, ok := ParseRetryAfter(hdr, time.Now()); !ok || d <= 0 {
			t.Errorf("ParseRetryAfter(%v) = %v, %v; want a large positive delay", hdr, d, ok)
		}
		eq(t, p.Delay(0, hdr, zero), ms(500))
	}
	err := NewAPIError(429, nil, h("retry-after", "1e12"))
	if rl := mustAs[*RateLimitError](t, err); !rl.HasRetryAfter || rl.RetryAfter <= 0 {
		t.Errorf("RateLimitError.RetryAfter = %v, %v; want a large positive delay", rl.RetryAfter, rl.HasRetryAfter)
	}
	// A backoff cap at the top of the range must not wrap either.
	huge := policyWith(func(p *RetryPolicy) {
		p.BackoffInitial, p.BackoffMax, p.BackoffJitter = time.Duration(math.MaxInt64), time.Duration(math.MaxInt64), 0
	})
	if d := huge.Delay(0, nil, zero); d <= 0 {
		t.Errorf("Delay with a maximal backoff = %v; want positive", d)
	}
}

func TestSleep_RejectsWithTheSignalsReasonWhenAbortedMidSleep(t *testing.T) {
	twin(t, "retry.test.ts | sleep rejects with the signal's reason when aborted mid-sleep")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() { done <- sleepCtx(ctx, 10*time.Second) }()
	cancel(errors.New("stop"))
	err := <-done
	if err == nil || err.Error() != "stop" {
		t.Fatalf("got %v", err)
	}
}

func TestSleep_RejectsImmediatelyIfTheSignalIsAlreadyAborted(t *testing.T) {
	twin(t, "retry.test.ts | sleep rejects immediately if the signal is already aborted")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("already"))
	err := sleepCtx(ctx, 10*time.Second)
	if err == nil || err.Error() != "already" {
		t.Fatalf("got %v", err)
	}
}

func TestSleep_WaitsTheDuration(t *testing.T) {
	start := time.Now()
	noErr(t, sleepCtx(context.Background(), 20*time.Millisecond))
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("returned early")
	}
}

// Go-specific: the exported Retry loop.

func TestRetry_LoopRetriesRetryableErrorsAndReportsEachRetry(t *testing.T) {
	var reasons []string
	calls := 0
	out, err := Retry(context.Background(), policyWith(func(p *RetryPolicy) { p.BackoffInitial, p.BackoffJitter = 0, 0 }), RetryHooks{
		OnRetry: func(retry, total int, d time.Duration, reason string) { reasons = append(reasons, reason) },
	}, func(ctx context.Context, attempt int) (string, error) {
		calls++
		if attempt < 2 {
			return "", NewAPIError(503, nil, http.Header{})
		}
		return "done", nil
	})
	noErr(t, err)
	eq(t, out, "done")
	eq(t, calls, 3)
	eq(t, len(reasons), 2)
}

func TestRetry_LoopDoesNotRetryOtherErrorsOrAborts(t *testing.T) {
	for _, e := range []error{errors.New("plain"), NewAPIError(400, nil, http.Header{}), &APIUserAbortError{TypeSafeError: &TypeSafeError{Message: "Request was aborted."}}} {
		calls := 0
		_, err := Retry(context.Background(), DefaultRetryPolicy(), RetryHooks{}, func(ctx context.Context, attempt int) (int, error) { calls++; return 0, e })
		if err == nil || calls != 1 {
			t.Fatalf("%v: calls=%d err=%v", e, calls, err)
		}
	}
}

func TestRetry_LoopHonorsTheContextDuringTheWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := Retry(ctx, DefaultRetryPolicy(), RetryHooks{OnRetry: func(int, int, time.Duration, string) { cancel() }}, func(ctx context.Context, attempt int) (int, error) {
		calls++
		return 0, NewAPIError(503, nil, http.Header{})
	})
	mustAs[*APIUserAbortError](t, err)
	eq(t, calls, 1)
}

var _ = reflect.DeepEqual
