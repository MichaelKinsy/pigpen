package websearch

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// W11: a provider's own deadline is an abort in the original (gemini-search.ts classifyProviderError:
// a fetch cut off by AbortSignal.timeout rejects with "The operation was aborted due to timeout",
// which isAbortError reads as an abort), so configured routing does not fall through on it and
// the search stops. The port had classified it as a network failure.

// stallDoer never answers: the request ends when its context does.
type stallDoer struct{}

func (stallDoer) Do(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func shortProviderDeadline(t *testing.T) {
	t.Helper()
	providerTimeoutOverride = 30 * time.Millisecond
	t.Cleanup(func() { providerTimeoutOverride = 0 })
}

func TestProviderOwnTimeoutIsAnAbort(t *testing.T) {
	t.Run("a provider deadline reads as the original's abort", func(t *testing.T) {
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		shortProviderDeadline(t)
		t.Cleanup(SetHTTP(stallDoer{}))
		_, err := SearchWithBrave(bg(), "stalled", SearchOptions{})
		wantErr(t, err, `^The operation was aborted due to timeout$`)
		if !isAbortError(err) {
			t.Fatalf("not an abort: %v", err)
		}
		if k := ClassifyProviderError("brave", err).Kind; k != "aborted" {
			t.Fatalf("kind %q", k)
		}
	})

	t.Run("configured routing does not fall through a provider deadline, even when network is selected", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["brave","tavily"],"fallbackOn":["network","transient","invalid-response"]}}`)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		shortProviderDeadline(t)
		fn := &fakeNet{fn: func(c netCall) netReply { return reply(200, `{"answer":"tavily must not run","results":[]}`) }}
		t.Cleanup(SetHTTP(routeDoer{stall: "api.search.brave.com", rest: fn}))
		_, err := Search(bg(), "stalled route", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `brave search failed \(aborted\): The operation was aborted due to timeout`)
		if len(fn.calls) != 0 {
			t.Fatalf("a later provider ran: %v", fn.urls())
		}
	})

	t.Run("a response body that outlives the deadline is an abort too", func(t *testing.T) {
		isolate(t)
		shortProviderDeadline(t)
		t.Cleanup(SetHTTP(stallDoer{}))
		_, err := SearchWithDuckDuckGo(bg(), "stalled", SearchOptions{})
		if k := ClassifyProviderError("duckduckgo", err).Kind; k != "aborted" {
			t.Fatalf("kind %q: %v", k, err)
		}
	})

	t.Run("the caller's own cancellation stays an abort", func(t *testing.T) {
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		t.Cleanup(SetHTTP(stallDoer{}))
		ctx, cancel := cancelAfter(30 * time.Millisecond)
		defer cancel()
		_, err := SearchWithBrave(ctx, "cancelled", SearchOptions{})
		if k := ClassifyProviderError("brave", err).Kind; k != "aborted" {
			t.Fatalf("kind %q: %v", k, err)
		}
	})
}

// cancelAfter is a context the caller cancels after d.
func cancelAfter(d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(bg())
	time.AfterFunc(d, cancel)
	return ctx, cancel
}

// routeDoer stalls requests to one host and answers the rest from rest.
type routeDoer struct {
	stall string
	rest  HTTPDoer
}

func (d routeDoer) Do(r *http.Request) (*http.Response, error) {
	if r.URL.Host == d.stall {
		return stallDoer{}.Do(r)
	}
	return d.rest.Do(r)
}
