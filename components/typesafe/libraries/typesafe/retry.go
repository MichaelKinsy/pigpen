package typesafe

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"net/http"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RetryPolicy is the resolved retry configuration.
type RetryPolicy struct {
	// MaxRetries is the number of retries after the first attempt; 0 disables retries. Default 2.
	MaxRetries int
	// BackoffInitial is the first backoff delay, doubled per retry up to BackoffMax. Default 500ms.
	BackoffInitial time.Duration
	// BackoffMax is the largest backoff delay. Default 5s.
	BackoffMax time.Duration
	// BackoffJitter is the fraction of each delay randomly subtracted, from 0 to 1. Default 0.25.
	BackoffJitter float64
	// HTTPStatuses are the retried status codes, ascending. Default 408, 429 and 500 to 599.
	HTTPStatuses []int
	// RespectRetryAfter honors Retry-After and retry-after-ms up to MaxRetryAfter. Default true.
	RespectRetryAfter bool
	// MaxRetryAfter is the longest server delay honored; longer ones use backoff. Default 60s.
	MaxRetryAfter time.Duration
	// APIConnectionError retries connection failures, including interrupted bodies. Default true.
	APIConnectionError bool
	// APITimeoutError retries per-attempt timeouts. Default true.
	APITimeoutError bool
}

// DefaultTimeout is the per-attempt timeout when none is configured (10s).
const DefaultTimeout = 10 * time.Second

// DefaultRetryPolicy returns a fresh copy of the SDK defaults.
func DefaultRetryPolicy() RetryPolicy {
	statuses := []int{408, 429}
	for s := 500; s < 600; s++ {
		statuses = append(statuses, s)
	}
	return RetryPolicy{
		MaxRetries:         2,
		BackoffInitial:     500 * time.Millisecond,
		BackoffMax:         5 * time.Second,
		BackoffJitter:      0.25,
		HTTPStatuses:       statuses,
		RespectRetryAfter:  true,
		MaxRetryAfter:      60 * time.Second,
		APIConnectionError: true,
		APITimeoutError:    true,
	}
}

// RetryOverrides changes some fields of a policy: nil pointers and a nil HTTPStatuses
// inherit; a non-nil empty HTTPStatuses retries no status.
type RetryOverrides struct {
	MaxRetries         *int
	BackoffInitial     *time.Duration
	BackoffMax         *time.Duration
	BackoffJitter      *float64
	HTTPStatuses       []int
	RespectRetryAfter  *bool
	MaxRetryAfter      *time.Duration
	APIConnectionError *bool
	APITimeoutError    *bool
}

// Ptr returns a pointer to v, for the pointer fields of [RetryOverrides].
func Ptr[T any](v T) *T { return &v }

func nonNegativeDuration(name string, d time.Duration) (time.Duration, error) {
	if d < 0 {
		return 0, errorf("`%s` must be a non-negative duration, got %s.", name, d)
	}
	return d, nil
}

// Resolve applies o over p and validates every field; the error names the field
// ("Retry.MaxRetries", "Retry.BackoffJitter", "Retry.HTTPStatuses", ...). The result
// shares no memory with p or o.
func (p RetryPolicy) Resolve(o RetryOverrides) (RetryPolicy, error) {
	out := p
	out.HTTPStatuses = slices.Clone(p.HTTPStatuses)
	var err error
	if o.MaxRetries != nil {
		if *o.MaxRetries < 0 {
			return RetryPolicy{}, errorf("`Retry.MaxRetries` must be a non-negative integer, got %d.", *o.MaxRetries)
		}
		out.MaxRetries = *o.MaxRetries
	}
	if o.BackoffInitial != nil {
		if out.BackoffInitial, err = nonNegativeDuration("Retry.BackoffInitial", *o.BackoffInitial); err != nil {
			return RetryPolicy{}, err
		}
	}
	if o.BackoffMax != nil {
		if out.BackoffMax, err = nonNegativeDuration("Retry.BackoffMax", *o.BackoffMax); err != nil {
			return RetryPolicy{}, err
		}
	}
	if o.BackoffJitter != nil {
		j := *o.BackoffJitter
		if math.IsNaN(j) || math.IsInf(j, 0) || j < 0 || j > 1 {
			return RetryPolicy{}, errorf("`Retry.BackoffJitter` must be between 0 and 1, got %v.", j)
		}
		out.BackoffJitter = j
	}
	if o.HTTPStatuses != nil {
		for _, s := range o.HTTPStatuses {
			if s < 100 || s > 999 {
				return RetryPolicy{}, errorf("`Retry.HTTPStatuses` must contain HTTP status codes, got %d.", s)
			}
		}
		out.HTTPStatuses = slices.Clone(o.HTTPStatuses)
		slices.Sort(out.HTTPStatuses)
		out.HTTPStatuses = slices.Compact(out.HTTPStatuses)
	}
	if o.RespectRetryAfter != nil {
		out.RespectRetryAfter = *o.RespectRetryAfter
	}
	if o.MaxRetryAfter != nil {
		if out.MaxRetryAfter, err = nonNegativeDuration("Retry.MaxRetryAfter", *o.MaxRetryAfter); err != nil {
			return RetryPolicy{}, err
		}
	}
	if o.APIConnectionError != nil {
		out.APIConnectionError = *o.APIConnectionError
	}
	if o.APITimeoutError != nil {
		out.APITimeoutError = *o.APITimeoutError
	}
	return out, nil
}

// RetriesStatus reports whether the policy retries an HTTP status code.
func (p RetryPolicy) RetriesStatus(status int) bool {
	if slices.IsSorted(p.HTTPStatuses) {
		_, found := slices.BinarySearch(p.HTTPStatuses, status)
		return found
	}
	return slices.Contains(p.HTTPStatuses, status)
}

var (
	jsDecimal = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)
	jsSpace   = strings.NewReplacer("\ufeff", "")
)

// jsNumber is JavaScript's Number(string): whitespace-trimmed, "" is 0, decimal and
// 0x/0o/0b literals, "Infinity"; anything else is NaN.
func jsNumber(s string) float64 {
	s = strings.TrimSpace(jsSpace.Replace(s))
	if s == "" {
		return 0
	}
	if len(s) > 2 && s[0] == '0' {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		if base != 0 {
			n, err := strconv.ParseUint(s[2:], base, 64)
			if err != nil {
				return math.NaN()
			}
			return float64(n)
		}
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if !jsDecimal.MatchString(s) {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return f
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

var httpDateLayouts = []string{http.TimeFormat, time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC, time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05"}

func parseHTTPDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range httpDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// msToDuration converts milliseconds, saturating at the largest Duration: converting an
// out-of-range float to an integer wraps (to a negative value on amd64), which would turn a
// huge Retry-After into an immediate retry where JavaScript falls back to backoff.
func msToDuration(ms float64) time.Duration {
	ns := ms * float64(time.Millisecond)
	if ns >= math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ns)
}

// ParseRetryAfter reads retry-after-ms, else Retry-After (seconds, or an HTTP date
// relative to now). ok is false when neither holds a valid delay.
func ParseRetryAfter(h http.Header, now time.Time) (d time.Duration, ok bool) {
	if vals := h.Values("retry-after-ms"); len(vals) > 0 {
		if ms := jsNumber(vals[0]); finite(ms) && ms >= 0 {
			return msToDuration(ms), true
		}
	}
	vals := h.Values("retry-after")
	if len(vals) == 0 {
		return 0, false
	}
	raw := vals[0]
	if seconds := jsNumber(raw); finite(seconds) {
		if seconds >= 0 {
			return msToDuration(seconds * 1000), true
		}
		return 0, false
	}
	if date, ok := parseHTTPDate(raw); ok {
		if d := date.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// Delay returns the wait before retry number attempt+1 (attempt is zero-based): an
// allowed server delay, else capped exponential backoff with jitter. random returns
// [0,1); nil uses math/rand.
func (p RetryPolicy) Delay(attempt int, h http.Header, random func() float64) time.Duration {
	if p.RespectRetryAfter && h != nil {
		if ra, ok := ParseRetryAfter(h, time.Now()); ok && ra <= p.MaxRetryAfter {
			return ra
		}
	}
	if random == nil {
		random = rand.Float64
	}
	initial := float64(p.BackoffInitial) / float64(time.Millisecond)
	maxMs := float64(p.BackoffMax) / float64(time.Millisecond)
	exponential := math.Min(initial*math.Pow(2, float64(attempt)), maxMs)
	ms := math.Floor(exponential*(1-random()*p.BackoffJitter) + 0.5) // Math.round
	if math.IsNaN(ms) || ms < 0 {
		ms = 0
	}
	return msToDuration(ms)
}

// RetryHooks observe a retry loop.
type RetryHooks struct {
	// Random supplies jitter in [0,1); nil uses math/rand.
	Random func() float64
	// OnRetry is called before each wait: retry is 1-based, of total retries. reason is
	// the status code of an *APIError, else the error's message.
	OnRetry func(retry, total int, delay time.Duration, reason string)
	// OnAbortDuringWait is called when the context ends while waiting to retry.
	OnAbortDuringWait func()
}

func (p RetryPolicy) retries(err error) bool {
	var abort *APIUserAbortError
	if errors.As(err, &abort) {
		return false
	}
	var timeout *APITimeoutError
	if errors.As(err, &timeout) {
		return p.APITimeoutError
	}
	var conn *APIConnectionError
	if errors.As(err, &conn) {
		return p.APIConnectionError
	}
	var api *APIError
	if errors.As(err, &api) {
		return p.RetriesStatus(api.Status)
	}
	return false
}

// Retry runs fn with the policy: fn gets the zero-based attempt number. Errors are
// retried when they are a *TypeSafeError-family API error whose status the policy
// retries, an *APITimeoutError (APITimeoutError policy flag) or another
// *APIConnectionError (APIConnectionError flag); an *APIUserAbortError, any other
// error, and a cancelled ctx are never retried. The wait honors Retry-After from an
// *APIError's headers per the policy, and a cancelled ctx during the wait returns an
// *APIUserAbortError. The last error is returned when retries run out.
func Retry[T any](ctx context.Context, p RetryPolicy, hooks RetryHooks, fn func(ctx context.Context, attempt int) (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		v, err := fn(ctx, attempt)
		if err == nil {
			return v, nil
		}
		if p.MaxRetries-attempt <= 0 || !p.retries(err) {
			return zero, err
		}
		var header http.Header
		reason := err.Error()
		var api *APIError
		if errors.As(err, &api) {
			header = api.Header
			reason = strconv.Itoa(api.Status)
		}
		delay := p.Delay(attempt, header, hooks.Random)
		if hooks.OnRetry != nil {
			hooks.OnRetry(attempt+1, p.MaxRetries, delay, reason)
		}
		if werr := sleepCtx(ctx, delay); werr != nil {
			if hooks.OnAbortDuringWait != nil {
				hooks.OnAbortDuringWait()
			}
			return zero, newAbortError(werr)
		}
	}
}

// sleepCtx waits d, returning context.Cause(ctx) as soon as ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// describeRuntime returns the value of the X-TypeSafe-Runtime header: "go/<version> (<os>; <arch>)".
func describeRuntime() string {
	return "go/" + strings.TrimPrefix(runtime.Version(), "go") + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}
