package typesafe

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

// These tests run in a synctest bubble: time is fake, so backoff and timeout delays
// are exact, the analog of the upstream vi.useFakeTimers().

// infoLogger records info, warn and error lines only (the upstream infoLogger).
func infoLogger() *recordingLogger { return &recordingLogger{} }

type result[T any] struct {
	v   T
	err error
}

// start runs fn in a goroutine and returns a channel for its result.
func start[T any](fn func() (T, error)) <-chan result[T] {
	ch := make(chan result[T], 1)
	go func() {
		v, err := fn()
		ch <- result[T]{v, err}
	}()
	return ch
}

func advance(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

func settle[T any](ch <-chan result[T]) (T, error) {
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(time.Hour):
		panic("call never settled")
	}
}

func failing(status int, headers ...string) func() *http.Response {
	return func() *http.Response { return jsonResp(status, map[string]any{}, headers...) }
}

func TestRetries_Retries429ThenSucceedsHonoringRetryAfter(t *testing.T) {
	twin(t, "reliability.test.ts | retries retries a 429 and then succeeds, honoring Retry-After")
	synctest.Test(t, func(t *testing.T) {
		logger := infoLogger()
		calls := 0
		m := newMock(func(*recordedRequest) (*http.Response, error) {
			calls++
			if calls == 1 {
				return jsonResp(429, map[string]any{}, "retry-after", "2"), nil
			}
			return textResp(200, modelsBody, "content-type", "application/json"), nil
		})
		c := newClient(t, m, func(cfg *Config) { cfg.Logger, cfg.LogLevel = logger, LogInfo })
		p := start(func() ([]ModelCard, error) { return c.Models().List(ctxBG(), nil) })
		advance(1999 * time.Millisecond)
		eq(t, m.count(), 1)
		advance(1 * time.Millisecond)
		got, err := settle(p)
		noErr(t, err)
		eq(t, got, modelCards)
		eq(t, m.count(), 2)
		lines := logger.messages("")
		eq(t, len(lines), 3)
		if !regexp.MustCompile(`^#1 GET /v1/models <- 429 in \d+ms$`).MatchString(lines[0]) || !regexp.MustCompile(`^#1 GET /v1/models <- 200 in \d+ms$`).MatchString(lines[2]) {
			t.Fatalf("lines %q", lines)
		}
		eq(t, lines[1], "#1 GET /v1/models retrying in 2000ms (retry 1/2) after 429")
	})
}

func TestRetries_UsesExponentialBackoffWhenThereIsNoRetryAfter(t *testing.T) {
	twin(t, "reliability.test.ts | retries uses exponential backoff when there is no Retry-After")
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		m := newMock(func(*recordedRequest) (*http.Response, error) {
			calls++
			if calls <= 2 {
				return jsonResp(503, map[string]any{}), nil
			}
			return textResp(200, modelsBody, "content-type", "application/json"), nil
		})
		c := newClient(t, m)
		p := start(func() ([]ModelCard, error) { return c.Models().List(ctxBG(), nil) })
		advance(499 * time.Millisecond)
		eq(t, m.count(), 1)
		advance(1 * time.Millisecond)
		eq(t, m.count(), 2)
		advance(999 * time.Millisecond)
		eq(t, m.count(), 2)
		advance(1 * time.Millisecond)
		eq(t, m.count(), 3)
		got, err := settle(p)
		noErr(t, err)
		eq(t, got, modelCards)
	})
}

func TestRetries_GivesUpAfterMaxRetriesAndThrowsTheLastError(t *testing.T) {
	twin(t, "reliability.test.ts | retries gives up after maxRetries and throws the last error")
	synctest.Test(t, func(t *testing.T) {
		m := always(func() *http.Response { return jsonResp(500, map[string]any{"message": "down"}) })
		c := newClient(t, m, func(cfg *Config) { cfg.Retry = RetryOverrides{MaxRetries: Ptr(3)} })
		_, err := c.Models().List(ctxBG(), nil)
		mustAs[*InternalServerError](t, err)
		eq(t, m.count(), 4)
	})
}

func TestRetries_DoesNotRetryNonRetryableStatuses(t *testing.T) {
	twin(t, "reliability.test.ts | retries does not retry non-retryable statuses")
	synctest.Test(t, func(t *testing.T) {
		m := always(failing(400))
		_, err := newClient(t, m).Models().List(ctxBG(), nil)
		mustAs[*BadRequestError](t, err)
		eq(t, m.count(), 1)
	})
}

func TestRetries_RetriesConnectionErrors(t *testing.T) {
	twin(t, "reliability.test.ts | retries retries connection errors")
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		m := newMock(func(*recordedRequest) (*http.Response, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("fetch failed")
			}
			return textResp(200, modelsBody, "content-type", "application/json"), nil
		})
		got, err := newClient(t, m).Models().List(ctxBG(), nil)
		noErr(t, err)
		eq(t, got, modelCards)
		eq(t, m.count(), 2)
	})
}

func TestRetries_NeverRetriesACallerAbort(t *testing.T) {
	twin(t, "reliability.test.ts | retries never retries a caller abort")
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		m := newMock(func(*recordedRequest) (*http.Response, error) { cancel(); return nil, context.Canceled })
		_, err := newClient(t, m).Models().List(ctx, nil)
		mustAs[*APIUserAbortError](t, err)
		eq(t, m.count(), 1)
	})
}

func TestRetries_AbortingDuringTheBackoffWaitThrowsAPIUserAbortError(t *testing.T) {
	twin(t, "reliability.test.ts | retries aborting during the backoff wait throws APIUserAbortError")
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		m := always(failing(503))
		c := newClient(t, m)
		p := start(func() ([]ModelCard, error) { return c.Models().List(ctx, nil) })
		advance(100 * time.Millisecond)
		cancel()
		_, err := settle(p)
		mustAs[*APIUserAbortError](t, err)
		eq(t, m.count(), 1)
	})
}

func TestRetries_PerCallMaxRetriesOverridesTheClientAndZeroDisablesRetries(t *testing.T) {
	twin(t, "reliability.test.ts | retries per-call maxRetries overrides the client, and 0 disables retries")
	synctest.Test(t, func(t *testing.T) {
		m := always(failing(503))
		c := newClient(t, m, func(cfg *Config) { cfg.Retry = RetryOverrides{MaxRetries: Ptr(5)} })
		_, err := c.Models().List(ctxBG(), &RequestOptions{Retry: RetryOverrides{MaxRetries: Ptr(0)}})
		mustAs[*InternalServerError](t, err)
		eq(t, m.count(), 1)
	})
}

func TestRetries_TellsTheServerWhichRetryThisIs(t *testing.T) {
	twin(t, "reliability.test.ts | retries tells the server which retry this is")
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		m := newMock(func(*recordedRequest) (*http.Response, error) {
			calls++
			if calls <= 2 {
				return jsonResp(503, map[string]any{}), nil
			}
			return textResp(200, modelsBody, "content-type", "application/json"), nil
		})
		_, err := newClient(t, m).Models().List(ctxBG(), nil)
		noErr(t, err)
		eq(t, m.req(0).Header.Get("X-TypeSafe-Retry-Count"), "")
		eq(t, m.req(1).Header.Get("X-TypeSafe-Retry-Count"), "1")
		eq(t, m.req(2).Header.Get("X-TypeSafe-Retry-Count"), "2")
	})
}

func TestRetries_RejectsInvalidMaxRetriesFromConfigOrPerCall(t *testing.T) {
	// Adapted: 1.5 is not representable (MaxRetries is an int); the messages name the Go fields.
	twin(t, "reliability.test.ts | retries rejects invalid maxRetries from config or per call")
	_, err := NewClient(Config{APIKey: "k", Getenv: noEnv, Retry: RetryOverrides{MaxRetries: Ptr(-1)}})
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "Retry.MaxRetries")
	c := newClient(t, modelsOK())
	_, err = c.Models().List(ctxBG(), &RequestOptions{Retry: RetryOverrides{MaxRetries: Ptr(-1)}})
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "Retry.MaxRetries")
}

func TestRetryPolicy_DefaultsToTheSDKPolicyAndExposesTheResolvedPolicyOnTheClient(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy defaults to the SDK policy and exposes the resolved policy on the client")
	c, err := NewClient(Config{APIKey: "k", Getenv: noEnv})
	noErr(t, err)
	eq(t, c.RetryPolicy(), DefaultRetryPolicy())
	c, err = NewClient(Config{APIKey: "k", Getenv: noEnv, Retry: RetryOverrides{MaxRetries: Ptr(7), BackoffJitter: Ptr(0.0)}})
	noErr(t, err)
	want := DefaultRetryPolicy()
	want.MaxRetries, want.BackoffJitter = 7, 0
	eq(t, c.RetryPolicy(), want)
}

func TestRetryPolicy_IsolatesThePolicyAndStatusSetBetweenDefaultClients(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy isolates the policy and status set between default clients")
	first, _ := NewClient(Config{APIKey: "k", Getenv: noEnv})
	second, _ := NewClient(Config{APIKey: "k", Getenv: noEnv})
	// Callers can mutate what the accessor returns; that must not reach the client.
	p := first.RetryPolicy()
	p.MaxRetries = 0
	p.HTTPStatuses[0] = 0
	p.HTTPStatuses = p.HTTPStatuses[:0]
	d := DefaultRetryPolicy()
	d.HTTPStatuses[0] = 1
	for _, c := range []*Client{first, second, mustClient(t, Config{APIKey: "k", Getenv: noEnv})} {
		got := c.RetryPolicy()
		eq(t, got.MaxRetries, 2)
		if !got.RetriesStatus(503) || !got.RetriesStatus(408) {
			t.Fatal("the status set changed")
		}
	}
}

func mustClient(t testing.TB, cfg Config) *Client {
	t.Helper()
	c, err := NewClient(cfg)
	noErr(t, err)
	return c
}

func TestRetryPolicy_CopiesCallerOwnedStatusSetsWhenConstructingAClient(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy copies caller-owned status sets when constructing a client")
	synctest.Test(t, func(t *testing.T) {
		statuses := []int{503}
		m := always(failing(503))
		c := newClient(t, m, func(cfg *Config) { cfg.Retry = RetryOverrides{HTTPStatuses: statuses, MaxRetries: Ptr(1)} })
		statuses[0] = 0
		_, err := c.Models().List(ctxBG(), nil)
		mustAs[*InternalServerError](t, err)
		eq(t, m.count(), 2)
	})
}

func TestRetryPolicy_SnapshotsThePolicyForAnInFlightRequest(t *testing.T) {
	twin(t,
		"reliability.test.ts | retry policy snapshots the client policy for an in-flight request",
		"reliability.test.ts | retry policy snapshots the call policy for an in-flight request")
	for _, source := range []string{"client", "call"} {
		synctest.Test(t, func(t *testing.T) {
			statuses := []int{503}
			m := always(failing(503))
			c := newClient(t, m, func(cfg *Config) { cfg.Retry = RetryOverrides{MaxRetries: Ptr(1), HTTPStatuses: []int{503}} })
			var opts *RequestOptions
			if source == "call" {
				opts = &RequestOptions{Retry: RetryOverrides{HTTPStatuses: statuses}}
			}
			p := start(func() ([]ModelCard, error) { return c.Models().List(ctxBG(), opts) })
			synctest.Wait() // first attempt is in its backoff wait
			// Mutating what the client exposes, or the caller's slice, must not change the call.
			pol := c.RetryPolicy()
			pol.MaxRetries = 0
			pol.HTTPStatuses = nil
			statuses[0] = 0
			_, err := settle(p)
			mustAs[*InternalServerError](t, err)
			eq(t, m.count(), 2)
		})
	}
}

func TestRetryPolicy_CanExtendTheDefaultStatusesBySpreadingTheClientsPolicy(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy can extend the default statuses by spreading the client's policy")
	client := mustClient(t, Config{APIKey: "k", Getenv: noEnv})
	extended := mustClient(t, Config{APIKey: "k", Getenv: noEnv, Retry: RetryOverrides{HTTPStatuses: append(client.RetryPolicy().HTTPStatuses, 409)}})
	if !extended.RetryPolicy().RetriesStatus(409) || !extended.RetryPolicy().RetriesStatus(503) {
		t.Fatal("extended set must retry 409 and 503")
	}
	if client.RetryPolicy().RetriesStatus(409) {
		t.Fatal("the original client must not retry 409")
	}
}

func TestRetryPolicy_RetriesOnlyTheStatusesInHTTPStatuses(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy retries only the statuses in httpStatuses")
	synctest.Test(t, func(t *testing.T) {
		m := always(failing(409))
		c := newClient(t, m, func(cfg *Config) { cfg.Retry = RetryOverrides{HTTPStatuses: []int{409}} })
		_, err := c.Models().List(ctxBG(), nil)
		mustAs[*APIError](t, err)
		eq(t, m.count(), 3)

		server := always(failing(503))
		none := newClient(t, server, func(cfg *Config) { cfg.Retry = RetryOverrides{HTTPStatuses: []int{}} })
		_, err = none.Models().List(ctxBG(), nil)
		mustAs[*InternalServerError](t, err)
		eq(t, server.count(), 1)
	})
}

func TestRetryPolicy_CanStopRetryingConnectionErrorsWhileStillRetryingTimeouts(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy can stop retrying connection errors while still retrying timeouts")
	synctest.Test(t, func(t *testing.T) {
		dropped := newMock(func(*recordedRequest) (*http.Response, error) { return nil, errors.New("fetch failed") })
		c := newClient(t, dropped, func(cfg *Config) { cfg.Retry = RetryOverrides{APIConnectionError: Ptr(false)} })
		_, err := c.Models().List(ctxBG(), nil)
		mustAs[*APIConnectionError](t, err)
		eq(t, dropped.count(), 1)

		hung := hangingDoer()
		timeouts := newClient(t, hung, func(cfg *Config) {
			cfg.Timeout = 10 * time.Millisecond
			cfg.Retry = RetryOverrides{APIConnectionError: Ptr(false), MaxRetries: Ptr(1)}
		})
		_, err = timeouts.Models().List(ctxBG(), nil)
		mustAs[*APITimeoutError](t, err)
		eq(t, hung.count(), 2)
	})
}

func TestRetryPolicy_CanStopRetryingTimeoutsWhileStillRetryingConnectionErrors(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy can stop retrying timeouts while still retrying connection errors")
	synctest.Test(t, func(t *testing.T) {
		hung := hangingDoer()
		c := newClient(t, hung, func(cfg *Config) {
			cfg.Timeout = 10 * time.Millisecond
			cfg.Retry = RetryOverrides{APITimeoutError: Ptr(false)}
		})
		_, err := c.Models().List(ctxBG(), nil)
		mustAs[*APITimeoutError](t, err)
		eq(t, hung.count(), 1)

		dropped := newMock(func(*recordedRequest) (*http.Response, error) { return nil, errors.New("fetch failed") })
		conns := newClient(t, dropped, func(cfg *Config) { cfg.Retry = RetryOverrides{APITimeoutError: Ptr(false), MaxRetries: Ptr(1)} })
		_, err = conns.Models().List(ctxBG(), nil)
		mustAs[*APIConnectionError](t, err)
		eq(t, dropped.count(), 2)
	})
}

func TestRetryPolicy_BacksOffFromTheConfiguredInitialDelayAndCap(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy backs off from the configured initial delay and cap")
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		m := newMock(func(*recordedRequest) (*http.Response, error) {
			calls++
			if calls <= 3 {
				return jsonResp(503, map[string]any{}), nil
			}
			return textResp(200, modelsBody, "content-type", "application/json"), nil
		})
		c := newClient(t, m, func(cfg *Config) {
			cfg.Retry = RetryOverrides{MaxRetries: Ptr(3), BackoffInitial: Ptr(100 * time.Millisecond), BackoffMax: Ptr(150 * time.Millisecond)}
		})
		p := start(func() ([]ModelCard, error) { return c.Models().List(ctxBG(), nil) })
		advance(99 * time.Millisecond)
		eq(t, m.count(), 1)
		advance(1 * time.Millisecond)
		eq(t, m.count(), 2)
		advance(150 * time.Millisecond)
		eq(t, m.count(), 3)
		advance(150 * time.Millisecond)
		eq(t, m.count(), 4)
		got, err := settle(p)
		noErr(t, err)
		eq(t, got, modelCards)
	})
}

func TestRetryPolicy_CanIgnoreRetryAfterAndCapsHowLongARetryAfterMayBe(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy can ignore Retry-After, and caps how long a Retry-After may be")
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		m := newMock(func(*recordedRequest) (*http.Response, error) {
			calls++
			if calls%2 == 1 {
				return jsonResp(429, map[string]any{}, "retry-after", "2"), nil
			}
			return textResp(200, modelsBody, "content-type", "application/json"), nil
		})
		c := newClient(t, m, func(cfg *Config) { cfg.Retry = RetryOverrides{RespectRetryAfter: Ptr(false)} })
		p := start(func() ([]ModelCard, error) { return c.Models().List(ctxBG(), nil) })
		advance(500 * time.Millisecond)
		eq(t, m.count(), 2)
		got, err := settle(p)
		noErr(t, err)
		eq(t, got, modelCards)

		calls = 0
		capped := newClient(t, m, func(cfg *Config) { cfg.Retry = RetryOverrides{MaxRetryAfter: Ptr(1000 * time.Millisecond)} })
		q := start(func() ([]ModelCard, error) { return capped.Models().List(ctxBG(), nil) })
		advance(500 * time.Millisecond)
		eq(t, m.count(), 4)
		got, err = settle(q)
		noErr(t, err)
		eq(t, got, modelCards)
	})
}

func TestRetryPolicy_PerCallRetryOverridesFieldByFieldAndLeavesTheClientsPolicyAlone(t *testing.T) {
	twin(t, "reliability.test.ts | retry policy per-call retry overrides field by field and leaves the client's policy alone")
	synctest.Test(t, func(t *testing.T) {
		logger := infoLogger()
		m := always(failing(503))
		c := newClient(t, m, func(cfg *Config) {
			cfg.Logger, cfg.LogLevel = logger, LogInfo
			cfg.Retry = RetryOverrides{MaxRetries: Ptr(5), BackoffInitial: Ptr(100 * time.Millisecond)}
		})
		_, err := c.Models().List(ctxBG(), &RequestOptions{Retry: RetryOverrides{MaxRetries: Ptr(1)}})
		mustAs[*InternalServerError](t, err)
		eq(t, m.count(), 2)
		found := false
		for _, l := range logger.messages("") {
			if l == "#1 GET /v1/models retrying in 100ms (retry 1/1) after 503" {
				found = true
			}
		}
		if !found {
			t.Fatalf("log lines %q", logger.messages(""))
		}
		eq(t, c.RetryPolicy().MaxRetries, 5)
	})
}

func TestRetryPolicy_ValidatesEveryNumericFieldAndNamesIt(t *testing.T) {
	// Adapted: durations are integers (no NaN or Infinity); the jitter float keeps its NaN and
	// Infinity cases; messages name the Go fields.
	twin(t, "reliability.test.ts | retry policy validates every numeric field and names it")
	bad := func(o RetryOverrides) error {
		_, err := NewClient(Config{APIKey: "k", Getenv: noEnv, Retry: o})
		return err
	}
	mustFail := func(o RetryOverrides, field string) {
		t.Helper()
		err := bad(o)
		if err == nil {
			t.Fatalf("want an error naming %s", field)
		}
		mustAs[*TypeSafeError](t, err)
		contains(t, err.Error(), field)
	}
	mustFail(RetryOverrides{BackoffInitial: Ptr(-time.Millisecond)}, "Retry.BackoffInitial")
	mustFail(RetryOverrides{BackoffMax: Ptr(-time.Millisecond)}, "Retry.BackoffMax")
	mustFail(RetryOverrides{BackoffJitter: Ptr(1.5)}, "Retry.BackoffJitter")
	mustFail(RetryOverrides{BackoffJitter: Ptr(-0.1)}, "Retry.BackoffJitter")
	mustFail(RetryOverrides{BackoffJitter: Ptr(nan())}, "Retry.BackoffJitter")
	mustFail(RetryOverrides{BackoffJitter: Ptr(inf())}, "Retry.BackoffJitter")
	mustFail(RetryOverrides{MaxRetryAfter: Ptr(-time.Millisecond)}, "Retry.MaxRetryAfter")
	mustFail(RetryOverrides{HTTPStatuses: []int{503, 42}}, "Retry.HTTPStatuses")
	mustFail(RetryOverrides{HTTPStatuses: []int{1000}}, "Retry.HTTPStatuses")
	noErr(t, bad(RetryOverrides{BackoffInitial: Ptr(time.Duration(0)), BackoffMax: Ptr(time.Duration(0)), BackoffJitter: Ptr(0.0)}))
	noErr(t, bad(RetryOverrides{BackoffJitter: Ptr(1.0), MaxRetryAfter: Ptr(time.Duration(0))}))
	c := newClient(t, modelsOK())
	_, err := c.Models().List(ctxBG(), &RequestOptions{Retry: RetryOverrides{BackoffJitter: Ptr(2.0)}})
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "Retry.BackoffJitter")
}

func TestTimeouts_ThrowsAPITimeoutErrorWhichIsAnAPIConnectionError(t *testing.T) {
	twin(t, "reliability.test.ts | timeouts throws APITimeoutError, which is an APIConnectionError")
	synctest.Test(t, func(t *testing.T) {
		c := newClient(t, hangingDoer(), func(cfg *Config) {
			cfg.Timeout = time.Second
			cfg.Retry = RetryOverrides{MaxRetries: Ptr(0)}
		})
		_, err := c.Models().List(ctxBG(), nil)
		to := mustAs[*APITimeoutError](t, err)
		mustAs[*APIConnectionError](t, err)
		eq(t, to.TimeoutMs, int64(1000))
		eq(t, err.Error(), "Request timed out after 1000ms.")
	})
}

func TestTimeouts_FiresAtExactlyTheConfiguredTimeout(t *testing.T) {
	twin(t, "reliability.test.ts | timeouts fires at exactly the configured timeout")
	synctest.Test(t, func(t *testing.T) {
		m := hangingDoer()
		c := newClient(t, m, func(cfg *Config) {
			cfg.Timeout = time.Second
			cfg.Retry = RetryOverrides{MaxRetries: Ptr(0)}
		})
		p := start(func() ([]ModelCard, error) { return c.Models().List(ctxBG(), nil) })
		advance(999 * time.Millisecond)
		if m.req(0).Ctx.Err() != nil {
			t.Fatal("fired early")
		}
		advance(1 * time.Millisecond)
		if m.req(0).Ctx.Err() == nil {
			t.Fatal("did not fire at the timeout")
		}
		_, err := settle(p)
		mustAs[*APITimeoutError](t, err)
	})
}

func TestTimeouts_RetriesAfterATimeoutEachAttemptGettingItsOwnTimeout(t *testing.T) {
	twin(t, "reliability.test.ts | timeouts retries after a timeout, each attempt getting its own timeout")
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		m := newMock(func(r *recordedRequest) (*http.Response, error) {
			calls++
			if calls == 1 {
				<-r.Ctx.Done()
				return nil, r.Ctx.Err()
			}
			return textResp(200, modelsBody, "content-type", "application/json"), nil
		})
		c := newClient(t, m, func(cfg *Config) { cfg.Timeout = time.Second })
		got, err := c.Models().List(ctxBG(), nil)
		noErr(t, err)
		eq(t, got, modelCards)
		eq(t, m.count(), 2)
	})
}

func TestTimeouts_PerCallTimeoutOverridesTheClient(t *testing.T) {
	twin(t, "reliability.test.ts | timeouts per-call timeout overrides the client")
	synctest.Test(t, func(t *testing.T) {
		m := hangingDoer()
		c := newClient(t, m, func(cfg *Config) {
			cfg.Timeout = time.Minute
			cfg.Retry = RetryOverrides{MaxRetries: Ptr(0)}
		})
		p := start(func() ([]ModelCard, error) {
			return c.Models().List(ctxBG(), &RequestOptions{Timeout: 50 * time.Millisecond})
		})
		advance(50 * time.Millisecond)
		if m.req(0).Ctx.Err() == nil {
			t.Fatal("the per-call timeout did not fire")
		}
		_, err := settle(p)
		mustAs[*APITimeoutError](t, err)
	})
}

func TestTimeouts_ACallerAbortDuringAHungRequestIsReportedAsAnAbortNotATimeout(t *testing.T) {
	twin(t, "reliability.test.ts | timeouts a caller abort during a hung request is reported as an abort, not a timeout")
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		c := newClient(t, hangingDoer(), func(cfg *Config) { cfg.Timeout = time.Minute })
		p := start(func() ([]ModelCard, error) { return c.Models().List(ctx, nil) })
		advance(10 * time.Millisecond)
		cancel()
		_, err := settle(p)
		mustAs[*APIUserAbortError](t, err)
		notAs[*APITimeoutError](t, err)
	})
}

func TestTimeouts_ClearsItsTimerAfterAFastResponse(t *testing.T) {
	// Adapted: no goroutine or timer may outlive the call (the analog of vi.getTimerCount() === 0).
	twin(t, "reliability.test.ts | timeouts clears its timer after a fast response")
	synctest.Test(t, func(t *testing.T) {
		m := always(func() *http.Response { return textResp(200, modelsBody, "content-type", "application/json") })
		c := newClient(t, m, func(cfg *Config) { cfg.Timeout = time.Second })
		before := runtime.NumGoroutine()
		_, err := c.Models().List(ctxBG(), nil)
		noErr(t, err)
		synctest.Wait()
		eq(t, runtime.NumGoroutine(), before)
		time.Sleep(10 * time.Second) // a leaked timer or goroutine would fire here
		if m.req(0).Ctx.Err() == nil {
			return
		}
		if errors.Is(m.req(0).Ctx.Err(), context.DeadlineExceeded) {
			t.Fatal("the attempt timer was still armed after the response")
		}
	})
}

func TestTimeouts_RejectsInvalidTimeoutsFromConfigOrPerCall(t *testing.T) {
	// Adapted: zero means the default in Go; a negative timeout is the invalid case.
	twin(t, "reliability.test.ts | timeouts rejects invalid timeouts from config or per call")
	_, err := NewClient(Config{APIKey: "k", Getenv: noEnv, Timeout: -time.Millisecond})
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "Timeout")
	c := newClient(t, modelsOK())
	_, err = c.Models().List(ctxBG(), &RequestOptions{Timeout: -5 * time.Millisecond})
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "Timeout")
}

func TestRateLimitError_ExposesTheParsedRetryAfterInMilliseconds(t *testing.T) {
	twin(t, "reliability.test.ts | RateLimitError exposes the parsed Retry-After in milliseconds")
	c := clientReturning(t, func() *http.Response { return jsonResp(429, map[string]any{}, "retry-after", "7") })
	_, err := c.Models().List(ctxBG(), nil)
	rl := mustAs[*RateLimitError](t, err)
	if !rl.HasRetryAfter || rl.RetryAfter != 7*time.Second {
		t.Fatalf("got %v %v", rl.RetryAfter, rl.HasRetryAfter)
	}
}

func TestRateLimitError_IsUndefinedWhenTheServerSentNoRetryAfter(t *testing.T) {
	twin(t, "reliability.test.ts | RateLimitError is undefined when the server sent no Retry-After")
	c := clientReturning(t, func() *http.Response { return jsonResp(429, map[string]any{}) })
	_, err := c.Models().List(ctxBG(), nil)
	if mustAs[*RateLimitError](t, err).HasRetryAfter {
		t.Fatal("no Retry-After header, no value")
	}
}

func TestBrowsers(t *testing.T) {
	skipTwin(t, "there is no browser in Go: the guard and dangerouslyAllowBrowser are not ported",
		"reliability.test.ts | browsers refuses to construct in a browser by default",
		"reliability.test.ts | browsers constructs in a browser when explicitly allowed",
		"reliability.test.ts | browsers calls the global fetch with a receiver it accepts")
}

func TestBrowsers_DoesNotMistakeNodeForABrowser(t *testing.T) {
	twin(t, "reliability.test.ts | browsers does not mistake Node for a browser")
	if _, err := NewClient(Config{APIKey: "k", Getenv: noEnv}); err != nil {
		t.Fatal(err)
	}
}

func TestBrowsers_FailsClearlyWhenNoFetchExistsAndNoneWasProvided(t *testing.T) {
	// Adapted: Go always has a default HTTP client, so there is no "no global fetch" failure.
	twin(t, "reliability.test.ts | browsers fails clearly when no fetch exists and none was provided")
	if _, err := NewClient(Config{APIKey: "k", Getenv: noEnv}); err != nil {
		t.Fatal("the default transport must always exist:", err)
	}
	if _, err := NewClient(Config{APIKey: "k", Getenv: noEnv, HTTPClient: modelsOK()}); err != nil {
		t.Fatal(err)
	}
}
