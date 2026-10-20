package websearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of curator-server-timeout (the upstream tests start the real server and talk to it over
// HTTP) and tests of the server's API.

const curatorToken = "test-token"

func curatorBaseOptions(timeout float64) CuratorServerOptions {
	return CuratorServerOptions{
		Queries:             []string{"test query"},
		SessionToken:        curatorToken,
		Timeout:             timeout,
		AvailableProviders:  NewProviderAvailability("all", true, "openai", false, "brave", false, "exa", true, "gemini", false),
		DefaultProvider:     "exa",
		SearchProvider:      "exa",
		SummaryModels:       []SummaryModelChoice{},
		DefaultSummaryModel: nil,
	}
}

// curatorCallbacks records what the server called.
type curatorCallbacks struct {
	mu       sync.Mutex
	cancels  chan string
	submits  chan CuratorSubmitPayload
	provider chan string
	indexed  []IndexedCuratorSearchEntry
	cb       CuratorServerCallbacks
}

func newCuratorCallbacks() *curatorCallbacks {
	c := &curatorCallbacks{cancels: make(chan string, 8), submits: make(chan CuratorSubmitPayload, 8), provider: make(chan string, 8)}
	c.cb = CuratorServerCallbacks{
		OnSubmit:         func(p CuratorSubmitPayload) { c.submits <- p },
		OnCancel:         func(reason string) { c.cancels <- reason },
		OnProviderChange: func(p string) { c.provider <- p },
		OnAddSearch: func(string, *string) ([]CuratorSearchEntry, error) {
			return []CuratorSearchEntry{{Answer: "", Results: []CuratorSource{}, Provider: "exa"}}, nil
		},
		OnAddSearchResults: func(entries []IndexedCuratorSearchEntry) {
			c.mu.Lock()
			c.indexed = append(c.indexed, entries...)
			c.mu.Unlock()
		},
		OnSummarize: func(context.Context, []int, string, string) (SummaryDraft, error) {
			return SummaryDraft{Meta: SummaryMeta{FallbackUsed: true}}, nil
		},
		OnRewriteQuery: func(_ context.Context, q string) (string, error) { return q, nil },
	}
	return c
}

func startCurator(t *testing.T, opts CuratorServerOptions, cbs CuratorServerCallbacks) *CuratorServerHandle {
	t.Helper()
	h, err := StartCuratorServer(opts, cbs)
	noErr(t, err)
	t.Cleanup(h.Close)
	return h
}

// curatorURL resolves a path against the handle's URL.
func curatorURL(t *testing.T, h *CuratorServerHandle, path string) string {
	t.Helper()
	base, err := url.Parse(h.URL)
	noErr(t, err)
	ref, err := url.Parse(path)
	noErr(t, err)
	return base.ResolveReference(ref).String()
}

func postJSON(t *testing.T, target string, body any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	noErr(t, err)
	return postRaw(t, target, string(raw))
}

func postRaw(t *testing.T, target, raw string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(target, "application/json", strings.NewReader(raw))
	noErr(t, err)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func getJSON(t *testing.T, target string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(target)
	noErr(t, err)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

// readEventsUntil reads an event stream until marker appears, failing after 8 seconds.
func readEventsUntil(t *testing.T, resp *http.Response, marker, label string) string {
	t.Helper()
	if resp.StatusCode != 200 {
		t.Fatalf("%s: status %d", label, resp.StatusCode)
	}
	defer resp.Body.Close()
	done := make(chan string, 1)
	go func() {
		var text strings.Builder
		r := bufio.NewReader(resp.Body)
		for !strings.Contains(text.String(), marker) {
			line, err := r.ReadString('\n')
			text.WriteString(line)
			if err != nil {
				break
			}
		}
		done <- text.String()
	}()
	select {
	case text := <-done:
		return text
	case <-time.After(8 * time.Second):
		resp.Body.Close()
		t.Fatalf("%s timed out", label)
		return ""
	}
}

func waitCancel(t *testing.T, ch <-chan string, label string) string {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		t.Fatalf("%s timed out", label)
		return ""
	}
}

func resultEntry(answer string) CuratorPush {
	return CuratorPush{CuratorSearchEntry: CuratorSearchEntry{Answer: answer, Results: []CuratorSource{}, Provider: "exa"}}
}

func TestUpstream_curator_server_timeout(t *testing.T) {
	const F = "curator-server-timeout"
	tw(t, F, "curator times out when searches finish but no browser connects", func(t *testing.T) {
		cbs := newCuratorCallbacks()
		h := startCurator(t, curatorBaseOptions(1), cbs.cb)
		h.PushResult(0, resultEntry("answer"))
		h.SearchesDone()
		if reason := waitCancel(t, cbs.cancels, "no-browser timeout"); reason != "timeout" {
			t.Fatal(reason)
		}
	})
	tw(t, F, "curator replays search events after SSE reconnect", func(t *testing.T) {
		h := startCurator(t, curatorBaseOptions(20), newCuratorCallbacks().cb)
		events := curatorURL(t, h, "/events?session="+curatorToken)
		first, err := http.Get(events)
		noErr(t, err)
		if first.StatusCode != 200 {
			t.Fatal(first.StatusCode)
		}
		h.PushResult(0, resultEntry("answer"))
		first.Body.Close()
		second, err := http.Get(events)
		noErr(t, err)
		body := readEventsUntil(t, second, `"answer":"answer"`, "sse replay")
		matchRE(t, `event: result`, body)
		matchRE(t, `"answer":"answer"`, body)
	})
	tw(t, F, "curator add-search indexes do not collide with reserved initial provider results", func(t *testing.T) {
		opts := curatorBaseOptions(20)
		opts.Queries = []string{"initial one", "initial two"}
		opts.InitialResultIndexCapacity = new(6)
		h := startCurator(t, opts, newCuratorCallbacks().cb)
		status, added := postJSON(t, curatorURL(t, h, "/search"), map[string]any{"token": curatorToken, "query": "added search"})
		if status != 200 || added["queryIndex"] != 6.0 {
			t.Fatalf("%d %v", status, added)
		}
		// Index 2 is a later provider result for the first initial query. Before reserving the
		// initial range, the added search also received index 2.
		slot := 0
		h.PushResult(2, CuratorPush{CuratorSearchEntry: CuratorSearchEntry{Answer: "late provider answer", Results: []CuratorSource{}, Provider: "brave"}, Query: "initial one", SlotIndex: &slot})
		h.SearchesDone()
		resp, err := http.Get(curatorURL(t, h, "/events?session="+curatorToken))
		noErr(t, err)
		events := readEventsUntil(t, resp, "event: done", "reserved-index replay")
		matchRE(t, `(?s)"query":"initial one".*"queryIndex":2`, events)
	})
	tw(t, F, "curator submit rejects contradictory summary metadata", func(t *testing.T) {
		h := startCurator(t, curatorBaseOptions(20), newCuratorCallbacks().cb)
		for _, meta := range []map[string]any{
			{"model": nil, "durationMs": 0, "tokenEstimate": 0, "fallbackUsed": false, "phase": "deterministic-fallback"},
			{"model": "model", "durationMs": 0, "tokenEstimate": 0, "fallbackUsed": true, "phase": "summary-model"},
		} {
			status, body := postJSON(t, curatorURL(t, h, "/submit"), map[string]any{"token": curatorToken, "selected": []int{}, "summaryMeta": meta})
			if status != 400 || body["ok"] != false || body["error"] != "Invalid summaryMeta" {
				t.Fatalf("%d %v", status, body)
			}
		}
	})
	tw(t, F, "curator assigns one selectable result index per provider", func(t *testing.T) {
		cbs := newCuratorCallbacks()
		var summarySelection []int
		cbs.cb.OnAddSearch = func(string, *string) ([]CuratorSearchEntry, error) {
			return []CuratorSearchEntry{
				{Answer: "Exa answer", Results: []CuratorSource{{Title: "Exa", URL: "https://example.com/exa", Domain: "example.com"}}, Provider: "exa"},
				{Answer: "Brave answer", Results: []CuratorSource{{Title: "Brave", URL: "https://example.com/brave", Domain: "example.com"}}, Provider: "brave"},
			}, nil
		}
		cbs.cb.OnSummarize = func(_ context.Context, selected []int, _, _ string) (SummaryDraft, error) {
			summarySelection = selected
			return SummaryDraft{Summary: "Combined summary", Meta: SummaryMeta{FallbackUsed: true}}, nil
		}
		opts := curatorBaseOptions(20)
		opts.AvailableProviders.Set("brave", true)
		h := startCurator(t, opts, cbs.cb)
		status, body := postJSON(t, curatorURL(t, h, "/search"), map[string]any{"token": curatorToken, "query": "combined query", "provider": "all"})
		if status != 200 {
			t.Fatal(status)
		}
		var providers []string
		var indexes []float64
		for _, e := range body["entries"].([]any) {
			m := e.(map[string]any)
			providers = append(providers, m["provider"].(string))
			indexes = append(indexes, m["queryIndex"].(float64))
		}
		if !eqStrings(providers, []string{"exa", "brave"}) || len(indexes) != 2 || indexes[0] != 1 || indexes[1] != 2 {
			t.Fatalf("%v %v", providers, indexes)
		}
		cbs.mu.Lock()
		got := []int{cbs.indexed[0].QueryIndex, cbs.indexed[1].QueryIndex}
		cbs.mu.Unlock()
		if got[0] != 1 || got[1] != 2 {
			t.Fatal(got)
		}
		status, _ = postJSON(t, curatorURL(t, h, "/summarize"), map[string]any{"token": curatorToken, "selected": []int{1, 2}})
		if status != 200 || len(summarySelection) != 2 || summarySelection[0] != 1 || summarySelection[1] != 2 {
			t.Fatalf("%d %v", status, summarySelection)
		}
	})
	tw(t, F, "curator heartbeat timeout finalizes connected idle browser sessions", func(t *testing.T) {
		cbs := newCuratorCallbacks()
		h := startCurator(t, curatorBaseOptions(20), cbs.cb)
		resp, err := http.Get(h.URL)
		noErr(t, err)
		resp.Body.Close()
		h.PushResult(0, resultEntry("answer"))
		h.SearchesDone()
		status, _ := postJSON(t, curatorURL(t, h, "/heartbeat"), map[string]any{"token": curatorToken, "idleMs": 21000, "timeoutSec": 20})
		if status != 200 {
			t.Fatal(status)
		}
		if reason := waitCancel(t, cbs.cancels, "idle heartbeat timeout"); reason != "timeout" {
			t.Fatal(reason)
		}
	})
	tw(t, F, "curator state replay keeps current results and compacts superseded history", func(t *testing.T) {
		h := startCurator(t, curatorBaseOptions(20), newCuratorCallbacks().cb)
		for i := range 205 {
			e := resultEntry("answer " + jsNumber(float64(i)))
			e.Query = "query " + jsNumber(float64(i))
			h.PushResult(i, e)
		}
		h.PushError(0, "updated", "exa", "query 0", nil)
		status, body := getJSON(t, curatorURL(t, h, "/state?session="+curatorToken))
		events := body["events"].([]any)
		first := events[0].(map[string]any)
		last := events[204].(map[string]any)["data"].(map[string]any)
		if status != 200 || len(events) != 205 || first["event"] != "search-error" || first["data"].(map[string]any)["queryIndex"] != 0.0 || last["queryIndex"] != 204.0 {
			t.Fatalf("%d %d %v", status, len(events), first)
		}
	})
	tw(t, F, "curator state replay keeps all-provider entries that share one slot", func(t *testing.T) {
		h := startCurator(t, curatorBaseOptions(20), newCuratorCallbacks().cb)
		slot := 0
		exa := CuratorPush{CuratorSearchEntry: CuratorSearchEntry{Answer: "exa answer", Results: []CuratorSource{}, Provider: "exa"}, Query: "query", SlotIndex: &slot}
		brave := CuratorPush{CuratorSearchEntry: CuratorSearchEntry{Answer: "brave answer", Results: []CuratorSource{}, Provider: "brave"}, Query: "query", SlotIndex: &slot}
		h.PushResult(0, exa)
		h.PushResult(1, brave)
		status, body := getJSON(t, curatorURL(t, h, "/state?session="+curatorToken))
		if status != 200 {
			t.Fatal(status)
		}
		var providers []string
		for i, e := range body["events"].([]any) {
			data := e.(map[string]any)["data"].(map[string]any)
			providers = append(providers, data["provider"].(string))
			if data["queryIndex"] != float64(i) || data["slotIndex"] != 0.0 {
				t.Fatalf("%v", data)
			}
		}
		if !eqStrings(providers, []string{"exa", "brave"}) {
			t.Fatal(providers)
		}
	})
	tw(t, F, "curator exposes and signals approve remaining searches for this prompt", func(t *testing.T) {
		cbs := newCuratorCallbacks()
		h := startCurator(t, curatorBaseOptions(20), cbs.cb)
		resp, err := http.Get(h.URL)
		noErr(t, err)
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !bytes.Contains(page, []byte("Approve + auto-summary remaining searches for this prompt")) {
			t.Fatal(resp.StatusCode)
		}
		status, _ := postJSON(t, curatorURL(t, h, "/submit"), map[string]any{"token": curatorToken, "selected": []int{0}, "summary": "Approved draft", "autoApproveRemainingSearches": true})
		if status != 200 {
			t.Fatal(status)
		}
		select {
		case p := <-cbs.submits:
			if p.Summary != "Approved draft" || !p.AutoApproveRemainingSearches {
				t.Fatalf("%+v", p)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("approve remaining submit timed out")
		}
	})
}

func TestCuratorServerRequestValidation(t *testing.T) {
	cbs := newCuratorCallbacks()
	h := startCurator(t, curatorBaseOptions(20), cbs.cb)
	u := func(p string) string { return curatorURL(t, h, p) }

	// The page and the event stream need the session token.
	for _, p := range []string{"/", "/events", "/?session=wrong"} {
		resp, err := http.Get(u(p))
		noErr(t, err)
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
	}
	if status, body := getJSON(t, u("/state")); status != 403 || body["error"] != "Invalid session" {
		t.Fatalf("%d %v", status, body)
	}
	resp, err := http.Get(u("/nowhere"))
	noErr(t, err)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}

	// Bodies: invalid JSON, too large, wrong token, no object.
	if status, body := postRaw(t, u("/cancel-not"), "{}"); status != 404 {
		t.Fatalf("%d %v", status, body)
	}
	status, body := postRaw(t, u("/search"), "{not json")
	if status != 400 || !strings.HasPrefix(body["error"].(string), "Invalid JSON: ") {
		t.Fatalf("%d %v", status, body)
	}
	status, body = postRaw(t, u("/search"), `{"token":"`+strings.Repeat("x", 70*1024)+`"}`)
	if status != 413 || body["error"] != "Request body too large" {
		t.Fatalf("%d %v", status, body)
	}
	if status, body = postJSON(t, u("/search"), map[string]any{"token": "wrong", "query": "q"}); status != 403 || body["error"] != "Invalid session" {
		t.Fatalf("%d %v", status, body)
	}
	for _, raw := range []string{`"text"`, `7`, `null`, `false`} {
		if status, body = postRaw(t, u("/search"), raw); status != 400 || body["error"] != "Invalid body" {
			t.Fatalf("%s: %d %v", raw, status, body)
		}
	}
	if status, body = postRaw(t, u("/search"), `[]`); status != 403 {
		t.Fatalf("%d %v", status, body)
	}

	// Provider checks: unknown, unavailable, and SerpApi, which the original's check never lists.
	_ = cbs
	for _, c := range []struct {
		path string
		body map[string]any
		want string
	}{
		{"/provider", map[string]any{"token": curatorToken}, "Invalid provider"},
		{"/provider", map[string]any{"token": curatorToken, "provider": "brave"}, "Provider unavailable: brave"},
		{"/provider", map[string]any{"token": curatorToken, "provider": "serpapi"}, "Provider unavailable: serpapi"},
		{"/search", map[string]any{"token": curatorToken, "query": "  "}, "Invalid query"},
		{"/search", map[string]any{"token": curatorToken, "query": "q", "provider": ""}, "Invalid provider"},
		{"/search", map[string]any{"token": curatorToken, "query": "q", "provider": "brave"}, "Provider unavailable: brave"},
		{"/summarize", map[string]any{"token": curatorToken, "selected": []int{}}, "Invalid selection"},
		{"/summarize", map[string]any{"token": curatorToken, "selected": []int{99}}, "Invalid selection"},
		{"/summarize", map[string]any{"token": curatorToken, "selected": []float64{0.5}}, "Invalid selection"},
		{"/summarize", map[string]any{"token": curatorToken, "selected": []int{0}, "model": 3}, "Invalid model"},
		{"/rewrite", map[string]any{"token": curatorToken, "query": ""}, "Invalid query"},
		{"/submit", map[string]any{"token": curatorToken, "selected": "0"}, "Invalid selection"},
		{"/submit", map[string]any{"token": curatorToken, "selected": []int{0}, "summary": 5}, "Invalid summary"},
	} {
		status, body := postJSON(t, u(c.path), c.body)
		if status != 400 || body["error"] != c.want {
			t.Errorf("%s %v: %d %v, want %q", c.path, c.body, status, body, c.want)
		}
	}
	if status, body := postJSON(t, u("/provider"), map[string]any{"token": curatorToken, "provider": "exa"}); status != 200 || body["ok"] != true {
		t.Fatalf("%d %v", status, body)
	}
	select {
	case p := <-cbs.provider:
		if p != "exa" {
			t.Fatal(p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no provider change")
	}
}

func TestCuratorServerSearchFailureAndSessionEnd(t *testing.T) {
	cbs := newCuratorCallbacks()
	cbs.cb.OnAddSearch = func(string, *string) ([]CuratorSearchEntry, error) { return nil, errors.New("provider down") }
	h := startCurator(t, curatorBaseOptions(20), cbs.cb)
	u := func(p string) string { return curatorURL(t, h, p) }

	// A failed added search still gets an index and is reported as an entry with the error.
	status, body := postJSON(t, u("/search"), map[string]any{"token": curatorToken, "query": " retry ", "provider": "exa"})
	entries, _ := body["entries"].([]any)
	if status != 200 || body["error"] != "provider down" || body["provider"] != "exa" || body["query"] != "retry" || body["queryIndex"] != 1.0 || len(entries) != 1 {
		t.Fatalf("%d %v", status, body)
	}
	cbs.mu.Lock()
	n := len(cbs.indexed)
	cbs.mu.Unlock()
	if n != 1 {
		t.Fatal(n)
	}

	// Cancel ends the session once; later calls see it closed.
	if status, _ = postJSON(t, u("/cancel"), map[string]any{"token": curatorToken, "reason": "timeout"}); status != 200 {
		t.Fatal(status)
	}
	if reason := waitCancel(t, cbs.cancels, "cancel"); reason != "timeout" {
		t.Fatal(reason)
	}
	if status, _ = postJSON(t, u("/cancel"), map[string]any{"token": curatorToken}); status != 200 {
		t.Fatal(status)
	}
	select {
	case r := <-cbs.cancels:
		t.Fatalf("second cancel reported %q", r)
	case <-time.After(300 * time.Millisecond):
	}
	for _, p := range []string{"/search", "/summarize", "/rewrite"} {
		if status, body = postJSON(t, u(p), map[string]any{"token": curatorToken, "query": "q", "selected": []int{0}}); status != 409 || body["error"] != "Session closed" {
			t.Errorf("%s: %d %v", p, status, body)
		}
	}
	if status, body = postJSON(t, u("/submit"), map[string]any{"token": curatorToken, "selected": []int{}}); status != 409 {
		t.Fatalf("%d %v", status, body)
	}
	if status, _ = getJSON(t, u("/events?session="+curatorToken)); status != 409 {
		t.Fatal(status)
	}
}

func TestCuratorServerSubmitCarriesTheSelection(t *testing.T) {
	cbs := newCuratorCallbacks()
	opts := curatorBaseOptions(20)
	opts.Queries = []string{"a", "b", "c"}
	h := startCurator(t, opts, cbs.cb)
	meta := map[string]any{"model": "m/x", "durationMs": 12, "tokenEstimate": 3, "fallbackUsed": false, "phase": "summary-model", "edited": true}
	status, _ := postJSON(t, curatorURL(t, h, "/submit"), map[string]any{
		"token": curatorToken, "selected": []int{2, 0, 2}, "summary": "  final text \n", "summaryMeta": meta, "rawResults": true,
	})
	if status != 200 {
		t.Fatal(status)
	}
	select {
	case p := <-cbs.submits:
		if len(p.SelectedQueryIndices) != 2 || p.SelectedQueryIndices[0] != 2 || p.SelectedQueryIndices[1] != 0 {
			t.Fatalf("%v", p.SelectedQueryIndices)
		}
		if p.Summary != "final text" || !p.RawResults || p.AutoApproveRemainingSearches {
			t.Fatalf("%+v", p)
		}
		if p.SummaryMeta == nil || *p.SummaryMeta.Model != "m/x" || p.SummaryMeta.DurationMs != 12 || !p.SummaryMeta.Edited || p.SummaryMeta.Phase != "summary-model" {
			t.Fatalf("%+v", p.SummaryMeta)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no submit")
	}
}

func TestCuratorServerSummarizeAndRewrite(t *testing.T) {
	cbs := newCuratorCallbacks()
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var seen []string
	var mu sync.Mutex
	cbs.cb.OnSummarize = func(ctx context.Context, selected []int, model, feedback string) (SummaryDraft, error) {
		mu.Lock()
		seen = append(seen, model+"|"+feedback)
		first := len(seen) == 1
		mu.Unlock()
		entered <- struct{}{}
		if first {
			// The first request is superseded by the second: its context is cancelled.
			select {
			case <-ctx.Done():
				return SummaryDraft{}, errors.New("Aborted")
			case <-release:
			}
		}
		return SummaryDraft{Summary: "s", Meta: SummaryMeta{DurationMs: 5, FallbackUsed: true, Phase: "deterministic-fallback"}}, nil
	}
	cbs.cb.OnRewriteQuery = func(_ context.Context, q string) (string, error) {
		if q == "boom" {
			return "", errors.New("rewrite failed badly")
		}
		return "better " + q, nil
	}
	h := startCurator(t, curatorBaseOptions(20), cbs.cb)
	u := func(p string) string { return curatorURL(t, h, p) }

	type result struct {
		status int
		body   map[string]any
	}
	firstDone := make(chan result, 1)
	go func() {
		status, body := postJSON(t, u("/summarize"), map[string]any{"token": curatorToken, "selected": []int{0}, "model": " a/b "})
		firstDone <- result{status, body}
	}()
	await(t, entered, "the first summarize")
	status, body := postJSON(t, u("/summarize"), map[string]any{"token": curatorToken, "selected": []int{0}, "feedback": "  shorter "})
	meta, _ := body["meta"].(map[string]any)
	if status != 200 || body["summary"] != "s" || meta["durationMs"] != 5.0 || meta["phase"] != "deterministic-fallback" {
		t.Fatalf("%d %v", status, body)
	}
	select {
	case r := <-firstDone:
		if r.status != 409 {
			t.Fatalf("superseded request: %d %v", r.status, r.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the superseded summarize never answered")
	}
	close(release)
	mu.Lock()
	if seen[0] != "a/b|" || seen[1] != "|shorter" {
		t.Fatal(seen)
	}
	mu.Unlock()

	if status, body = postJSON(t, u("/rewrite"), map[string]any{"token": curatorToken, "query": " x "}); status != 200 || body["query"] != "better x" {
		t.Fatalf("%d %v", status, body)
	}
	if status, body = postJSON(t, u("/rewrite"), map[string]any{"token": curatorToken, "query": "boom"}); status != 500 || body["error"] != "rewrite failed badly" {
		t.Fatalf("%d %v", status, body)
	}
}

func TestCuratorServerCloseReportsStale(t *testing.T) {
	cbs := newCuratorCallbacks()
	h, err := StartCuratorServer(curatorBaseOptions(20), cbs.cb)
	noErr(t, err)
	h.Close()
	if reason := waitCancel(t, cbs.cancels, "close"); reason != "stale" {
		t.Fatal(reason)
	}
	h.Close()
	select {
	case r := <-cbs.cancels:
		t.Fatalf("a second close reported %q", r)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := http.Get(h.URL); err == nil {
		t.Fatal("the server still accepts connections")
	}
}

func TestCuratorServerPageEmbedsItsSettings(t *testing.T) {
	opts := curatorBaseOptions(20)
	opts.SummaryModels = []SummaryModelChoice{{Value: "a/b", Label: "a/b"}}
	opts.DefaultSummaryModel = new("a/b")
	h := startCurator(t, opts, newCuratorCallbacks().cb)
	resp, err := http.Get(h.URL)
	noErr(t, err)
	defer resp.Body.Close()
	page, _ := io.ReadAll(resp.Body)
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(ct)
	}
	matchRE(t, regexp.QuoteMeta(`"sessionToken":"test-token"`), string(page))
	matchRE(t, regexp.QuoteMeta(`"defaultSummaryModel":"a/b"`), string(page))
	matchRE(t, `class="provider-btn loading is-default" data-provider="exa"`, string(page))
}

func TestCuratorServerReportsBindFailures(t *testing.T) {
	opts := curatorBaseOptions(20)
	opts.Network = CuratorNetwork{Host: "localhost", Bind: "256.256.256.256"}
	_, err := StartCuratorServer(opts, newCuratorCallbacks().cb)
	wantErr(t, err, `^Curator server failed to start: `)
}
