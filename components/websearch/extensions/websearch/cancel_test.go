package websearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Cancellation: before the work, during it, and after it; children stop with the parent.

// blockingDoer answers only when released; a request whose context ends returns that error.
type blockingDoer struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
	once    sync.Once
	body    string
}

func newBlocking(body string) *blockingDoer {
	return &blockingDoer{started: make(chan struct{}), release: make(chan struct{}), body: body}
}

func (b *blockingDoer) Do(r *http.Request) (*http.Response, error) {
	b.calls.Add(1)
	b.once.Do(func() { close(b.started) })
	select {
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-b.release:
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(b.body)), Request: r}, nil
	}
}

const tavilyOKBody = `{"answer":"a","results":[{"title":"T","url":"https://93.184.216.34/x","content":"c"}]}`

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestSearchCancelledBeforeAnyRequest(t *testing.T) {
	isolate(t)
	t.Setenv("TAVILY_API_KEY", "k")
	b := newBlocking(tavilyOKBody)
	t.Cleanup(SetHTTP(b))
	ctx, cancel := context.WithCancel(bg())
	cancel()
	_, err := Search(ctx, "q", FullSearchOptions{Provider: ProviderSelection{Name: "tavily"}})
	if err == nil || !isAbortError(err) && !errors.Is(err, context.Canceled) {
		t.Fatalf("want an abort, got %v", err)
	}
	if n := b.calls.Load(); n != 0 {
		t.Fatalf("%d requests were sent after cancellation", n)
	}
}

func TestSearchCancelledDuringRequestAbortsWithoutFallback(t *testing.T) {
	isolate(t)
	t.Setenv("TAVILY_API_KEY", "k")
	t.Setenv("BRAVE_API_KEY", "b")
	b := newBlocking(tavilyOKBody)
	t.Cleanup(SetHTTP(b))
	ctx, cancel := context.WithCancel(bg())
	done := make(chan error, 1)
	go func() {
		_, err := Search(ctx, "q", FullSearchOptions{Provider: Auto})
		done <- err
	}()
	waitClosed(t, b.started, "the first request")
	cancel()
	select {
	case err := <-done:
		if err == nil || !isAbortError(err) && !errors.Is(err, context.Canceled) {
			t.Fatalf("want an abort, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Search did not stop after cancellation")
	}
	if n := b.calls.Load(); n != 1 {
		t.Fatalf("cancellation fell through to another provider: %d requests", n)
	}
}

func TestSearchCancelledAfterCompletionChangesNothing(t *testing.T) {
	isolate(t)
	t.Setenv("TAVILY_API_KEY", "k")
	b := newBlocking(tavilyOKBody)
	close(b.release)
	t.Cleanup(SetHTTP(b))
	ctx, cancel := context.WithCancel(bg())
	res, err := Search(ctx, "q", FullSearchOptions{Provider: ProviderSelection{Name: "tavily"}})
	noErr(t, err)
	cancel()
	if len(res.Results) != 1 || res.Results[0].URL != "https://93.184.216.34/x" {
		t.Fatalf("%+v", res)
	}
}

func TestWebSearchToolCancelledMidQuery(t *testing.T) {
	r, _ := started(t, `{"provider":"tavily"}`)
	t.Setenv("TAVILY_API_KEY", "k")
	b := newBlocking(tavilyOKBody)
	t.Cleanup(SetHTTP(b))
	ctx, cancel := context.WithCancel(bg())
	type result struct {
		out ToolOutput
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := mustTool(t, r, "web_search").Execute(ctx, map[string]any{"queries": []any{"one", "two", "three", "four"}, "workflow": "none"}, nil)
		done <- result{out, err}
	}()
	waitClosed(t, b.started, "the first request")
	cancel()
	select {
	case res := <-done:
		if res.err == nil && !strings.Contains(strings.ToLower(res.out.Text()), "abort") {
			t.Fatalf("expected an abort: %+v", res.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("web_search did not stop after cancellation")
	}
	if n := b.calls.Load(); n > 3 {
		t.Fatalf("queries kept starting after cancellation: %d requests (concurrency is 3)", n)
	}
	if GetAllResults() != nil && len(GetAllResults()) != 0 {
		t.Fatalf("a cancelled search must not store results: %d", len(GetAllResults()))
	}
}

func TestFetchAllContentCancelStopsChildren(t *testing.T) {
	extractEnv(t, "")
	var mu sync.Mutex
	started, stopped := 0, 0
	allStarted := make(chan struct{})
	t.Cleanup(SetPageFetch(func(ctx context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
		mu.Lock()
		started++
		if started == 3 {
			close(allStarted)
		}
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		stopped++
		mu.Unlock()
		return nil, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(bg())
	done := make(chan []ExtractedContent, 1)
	go func() {
		done <- FetchAllContent(ctx, []string{"https://93.184.216.34/a", "https://93.184.216.35/b", "https://93.184.216.36/c", "https://93.184.216.37/d"}, ExtractOptions{})
	}()
	waitClosed(t, allStarted, "three concurrent fetches")
	cancel()
	select {
	case res := <-done:
		if len(res) != 4 {
			t.Fatalf("results must keep input order and length: %d", len(res))
		}
		for _, r := range res {
			if r.Error == nil {
				t.Fatalf("a cancelled fetch must carry an error: %+v", r)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("FetchAllContent did not stop after cancellation")
	}
	mu.Lock()
	defer mu.Unlock()
	if stopped != started || started > 3 {
		t.Fatalf("started %d, stopped %d (concurrency is 3, children must stop with the parent)", started, stopped)
	}
}

func TestBackgroundFetchStopsAtSessionShutdown(t *testing.T) {
	r, h := started(t, `{"provider":"tavily"}`)
	t.Setenv("TAVILY_API_KEY", "k")
	t.Cleanup(SetHTTP(fakeAPI{tavilyOKBody}))
	begun := make(chan struct{})
	stopped := make(chan struct{})
	t.Cleanup(SetPageFetch(func(ctx context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
		close(begun)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}))
	out := run(t, mustTool(t, r, "web_search"), map[string]any{"query": "bg", "includeContent": true, "workflow": "none"})
	matchRE(t, `Content fetching in background`, out.Text())
	waitClosed(t, begun, "the background fetch")
	r.SessionShutdown()
	waitClosed(t, stopped, "the background fetch to stop")
	r.WaitBackground()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.messages {
		if m.Type == "web-search-content-ready" || m.Type == "web-search-error" {
			t.Fatalf("a shut-down session must not receive %s", m.Type)
		}
	}
}

// fakeAPI answers every provider request with a fixed body (also used by extension_test.go).
type fakeAPI struct{ reply string }

func (f fakeAPI) Do(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(f.reply)), Request: r}, nil
}
