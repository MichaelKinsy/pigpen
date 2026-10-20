package websearch

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// Twins of curator-fallback and parallel-curator-state (the upstream tests read index.ts as text;
// these run the behaviour the matched source lines implement) and the curated web_search workflow.

const readmePath = "../../README.md"

func TestUpstream_curator_fallback(t *testing.T) {
	const F = "curator-fallback"
	tw(t, F, "web_search curator auto-open failures keep the curator alive with a manual URL", func(t *testing.T) {
		e := newCuratorEnv(t, ``)
		e.failBrowser(errBrowser)
		useFakeProvider(t, "brave", answerWith("answer", source("One", "https://example.com/1")))
		run := e.runWebSearch("call", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
		pageURL := run.curatorURL()
		fallback := run.waitUpdate(func(u ToolOutput) bool { return u.Details["phase"] == "curator-fallback" })
		if want := "Search curator is running, but the browser did not open automatically.\nOpen manually: " + pageURL; fallback.Text() != want {
			t.Fatalf("%q", fallback.Text())
		}
		if fallback.Details["browserOpenError"] != "spawn xdg-open ENOENT" || fallback.Details["curatorUrl"] != pageURL ||
			fallback.Details["timeoutSeconds"] != 20 || fallback.Details["shortcut"] != "ctrl+shift+s" {
			t.Fatalf("%v", fallback.Details)
		}
		// The curator is not closed: its page is served and the call is still waiting.
		c := newCuratorClient(t, pageURL)
		matchRE(t, `Review summary draft`, c.page())
		select {
		case out := <-run.out:
			t.Fatalf("the call ended: %v", out.Details)
		case <-time.After(300 * time.Millisecond):
		}
		run.cancel()
		run.result()
	})
	tw(t, F, "curator add-search receives the active extension context", func(t *testing.T) {
		e := newCuratorEnv(t, ``)
		seen := make(chan bool, 4)
		useFakeProvider(t, "brave", func(ctx context.Context, q string, _ SearchOptions) (*SearchResponse, error) {
			seen <- SideCallContextFrom(ctx) != nil
			return &SearchResponse{Answer: "a " + q}, nil
		})
		run := e.runWebSearch("call", map[string]any{"query": "first", "provider": "brave", "workflow": "summary-review"})
		c := newCuratorClient(t, run.curatorURL())
		c.waitDone()
		if !<-seen {
			t.Fatal("the initial search had no extension context")
		}
		status, body := c.post("/search", map[string]any{"query": "added"})
		if status != 200 || body["error"] != nil || body["query"] != "added" {
			t.Fatalf("%d %v", status, body)
		}
		if !<-seen {
			t.Fatal("the added search had no extension context")
		}
		run.cancel()
		run.result()
	})
	tw(t, F, "curator fallback helper is visible to the browser-open catch block", func(t *testing.T) {
		// A browser that cannot be opened reaches the fallback update only once the server runs: the
		// update carries the URL of the running curator.
		e := newCuratorEnv(t, ``)
		e.failBrowser(errBrowser)
		useFakeProvider(t, "brave", answerWith("a"))
		run := e.runWebSearch("call", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
		fallback := run.waitUpdate(func(u ToolOutput) bool { return u.Details["phase"] == "curator-fallback" })
		if !strings.HasSuffix(fallback.Text(), detailString(fallback, "curatorUrl")) || detailString(fallback, "curatorUrl") == "" {
			t.Fatalf("%q", fallback.Text())
		}
		// Every update before the server runs is a progress update, never a fallback without URL.
		for _, u := range run.allUpdates() {
			if u.Details["phase"] == "curator-fallback" && detailString(u, "curatorUrl") == "" {
				t.Fatalf("a fallback update without a URL: %v", u.Details)
			}
		}
		run.cancel()
		run.result()
	})
	tw(t, F, "cancel diagnostics include curator URL and browser-open error", func(t *testing.T) {
		e := newCuratorEnv(t, ``)
		e.failBrowser(errBrowser)
		useFakeProvider(t, "brave", answerWith("answer", source("One", "https://example.com/1")))
		run := e.runWebSearch("call", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
		pageURL := run.curatorURL()
		c := newCuratorClient(t, pageURL)
		c.waitDone()
		c.page()
		if status, _ := c.post("/cancel", map[string]any{}); status != 200 {
			t.Fatal(status)
		}
		out := run.result()
		d := out.Details
		extra, _ := d["extraLines"].([]string)
		if out.Text() != "Search curation cancelled (user)." || d["error"] != "Search curation cancelled (user)." || d["cancelled"] != true ||
			d["cancelReason"] != "user" || d["browserConnected"] != true || d["queryCount"] != 1 ||
			len(extra) != 2 || extra[0] != "curator: "+pageURL || extra[1] != "browser open error: spawn xdg-open ENOENT" {
			t.Fatalf("%q %v", out.Text(), d)
		}
		queries, _ := d["cancelledQueries"].([]map[string]any)
		if len(queries) != 1 || queries[0]["query"] != "q" || queries[0]["provider"] != "brave" || queries[0]["resultCount"] != 1 || queries[0]["error"] != nil {
			t.Fatalf("%v", d["cancelledQueries"])
		}
	})
	tw(t, F, "manual websearch command reports browser-open fallback without closing curator", func(t *testing.T) {
		e := newCuratorEnv(t, `{"provider":"brave"}`)
		e.failBrowser(errBrowser)
		useFakeProvider(t, "brave", answerWith("answer", source("One", "https://example.com/1")))
		noErr(t, e.rt.WebsearchCommand(e.side, "a query, another"))
		note := e.host.waitNote(t, "the browser did not open automatically")
		pageURL := recvURL(t, e.opened)
		if note.level != "info" || note.message != "Search curator is running, but the browser did not open automatically. Open manually: "+pageURL {
			t.Fatalf("%+v", note)
		}
		// The curator keeps running and the queries are searched.
		c := newCuratorClient(t, pageURL)
		state := c.waitDone()
		if n := len(state["events"].([]any)); n != 2 {
			t.Fatalf("%d events", n)
		}
	})
	tw(t, F, "remote curator mode prints the manual URL unless auto-open is explicit", func(t *testing.T) {
		old := hostnameFunc
		hostnameFunc = func() (string, error) { return "dev-box", nil }
		t.Cleanup(func() { hostnameFunc = old })

		e := newCuratorEnv(t, `{"curatorRemote":true}`)
		useFakeProvider(t, "brave", answerWith("a"))
		run := e.runWebSearch("call", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
		pageURL := run.curatorURL()
		matchRE(t, `^http://dev-box:\d+/\?session=[0-9a-f-]{36}$`, pageURL)
		fallback := run.waitUpdate(func(u ToolOutput) bool { return u.Details["phase"] == "curator-fallback" })
		if fallback.Text() != "Search curator is running. Open the curator URL manually.\nOpen manually: "+pageURL || fallback.Details["timeoutSeconds"] != 60 {
			t.Fatalf("%q %v", fallback.Text(), fallback.Details)
		}
		noURL(t, e.opened)
		run.cancel()
		run.result()

		// autoOpenBrowser: true opens the browser of a remote curator.
		e2 := newCuratorEnv(t, `{"curatorRemote":{"host":" demo.example ","bind":"127.0.0.1"},"autoOpenBrowser":true}`)
		useFakeProvider(t, "brave", answerWith("a"))
		run2 := e2.runWebSearch("call", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
		u := recvURL(t, e2.opened)
		matchRE(t, `^http://demo\.example:\d+/\?session=`, u)
		run2.cancel()
		run2.result()

		// The /websearch command says so in a notification.
		e3 := newCuratorEnv(t, `{"curatorRemote":true,"provider":"brave"}`)
		useFakeProvider(t, "brave", answerWith("a"))
		noErr(t, e3.rt.WebsearchCommand(e3.side, ""))
		note := e3.host.waitNote(t, "Search curator is running. Open manually: ")
		matchRE(t, `^Search curator is running\. Open manually: http://dev-box:\d+/\?session=`, note.message)
		noURL(t, e3.opened)
	})
	tw(t, F, "Linux curator browser launch detaches xdg-open but keeps immediate failures", func(t *testing.T) {
		host := &curatorHostFake{}
		rt, err := NewRuntime(host)
		noErr(t, err)
		switch runtime.GOOS {
		case "darwin", "windows":
			adapted(t, "macOS and Windows start the browser through pi.exec (open, cmd /c start), as the original does; xdg-open is the other systems' path")
			host.execReply = func(command string, args []string) (int, string, error) { return 3, "no browser", nil }
			wantErr(t, rt.openInBrowser("http://localhost:1/"), `no browser`)
			if len(host.execs) != 1 {
				t.Fatalf("%+v", host.execs)
			}
			return
		}
		dir := t.TempDir()
		write := func(script string) {
			noErr(t, os.WriteFile(filepath.Join(dir, "xdg-open"), []byte("#!/bin/sh\n"+script+"\n"), 0o755))
		}
		t.Setenv("PATH", dir)
		sleep := "/bin/sleep"
		if _, err := os.Stat(sleep); err != nil {
			sleep = "/usr/bin/sleep"
		}
		// Missing: the spawn error.
		wantErr(t, rt.openInBrowser("http://localhost:1/"), `^spawn xdg-open ENOENT$`)
		// Fails at once: the exit code.
		write("exit 3")
		wantErr(t, rt.openInBrowser("http://localhost:1/"), `^Failed to open browser \(exit code 3\)$`)
		// Succeeds at once.
		write("exit 0")
		noErr(t, rt.openInBrowser("http://localhost:1/"))
		// Keeps running (a browser that stays attached): the launch returns after the grace period
		// without waiting for it.
		marker := filepath.Join(dir, "started")
		write("echo $1 > '" + marker + "'; " + sleep + " 5")
		started := time.Now()
		noErr(t, rt.openInBrowser("http://localhost:1/page"))
		if d := time.Since(started); d < 90*time.Millisecond || d > 3*time.Second {
			t.Fatalf("returned after %v", d)
		}
		if got := strings.TrimSpace(readFile(t, marker)); got != "http://localhost:1/page" {
			t.Fatalf("xdg-open got %q", got)
		}
	})
	tw(t, F, "README documents manual browser fallback", func(t *testing.T) {
		readme := readReadme(t)
		matchRE(t, `Docker, WSL, SSH, or headless environments`, readme)
		matchRE(t, `Copy it into a browser that can reach the Pi host`, readme)
		matchRE(t, `Remote curator sessions print the URL instead of trying to open a browser by default`, readme)
	})
}

func TestUpstream_parallel_curator_state(t *testing.T) {
	const F = "parallel-curator-state"
	activeIDs := func(rt *Runtime) []string {
		rt.cur.mu.Lock()
		defer rt.cur.mu.Unlock()
		var ids []string
		for id := range rt.cur.active {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return ids
	}
	tw(t, F, "web_search curator state is keyed per tool call for parallel calls", func(t *testing.T) {
		e := newCuratorEnv(t, ``)
		useFakeProvider(t, "brave", func(_ context.Context, q string, _ SearchOptions) (*SearchResponse, error) {
			return &SearchResponse{Answer: "answer for " + q, Results: []SearchResult{source(q, "https://example.com/"+q)}}, nil
		})
		a := e.runWebSearch("call-a", map[string]any{"query": "alpha", "provider": "brave", "workflow": "summary-review"})
		b := e.runWebSearch("call-b", map[string]any{"query": "beta", "provider": "brave", "workflow": "summary-review"})
		ca, cb := newCuratorClient(t, a.curatorURL()), newCuratorClient(t, b.curatorURL())
		if ca.token == cb.token || ca.origin == cb.origin {
			t.Fatal("two calls share one curator")
		}
		ca.waitDone()
		cb.waitDone()
		if ids := activeIDs(e.rt); strings.Join(ids, ",") != "call-a,call-b" {
			t.Fatalf("%v", ids)
		}
		// Ending one call leaves the other untouched.
		if status, _ := ca.post("/cancel", map[string]any{}); status != 200 {
			t.Fatal(status)
		}
		outA := a.result()
		if outA.Details["cancelReason"] != "user" {
			t.Fatalf("%v", outA.Details)
		}
		matchRE(t, `Review summary draft`, cb.page())
		select {
		case <-b.out:
			t.Fatal("the other call ended")
		case <-time.After(300 * time.Millisecond):
		}
		// Each call's results are its own.
		if status, _ := cb.post("/submit", map[string]any{"selected": []int{0}, "summary": "beta summary"}); status != 200 {
			t.Fatal(status)
		}
		outB := b.result()
		if outB.Text() != "beta summary" {
			t.Fatalf("%q", outB.Text())
		}
		queries, _ := outB.Details["curatedQueries"].([]map[string]any)
		if len(queries) != 1 || queries[0]["query"] != "beta" {
			t.Fatalf("%v", outB.Details["curatedQueries"])
		}
	})
	tw(t, F, "manual websearch curator command uses a distinct map key", func(t *testing.T) {
		e := newCuratorEnv(t, `{"provider":"brave","autoOpenBrowser":false}`)
		useFakeProvider(t, "brave", answerWith("a"))
		run := e.runWebSearch("call-a", map[string]any{"query": "alpha", "provider": "brave", "workflow": "summary-review"})
		run.curatorURL()
		noErr(t, e.rt.WebsearchCommand(e.side, ""))
		e.host.waitNote(t, "Open manually: ")
		ids := activeIDs(e.rt)
		if len(ids) != 2 || ids[0] != "call-a" || !strings.HasPrefix(ids[1], "cmd:") {
			t.Fatalf("%v", ids)
		}
		matchRE(t, `^cmd:[0-9a-f-]{36}$`, ids[1])
		run.cancel()
		run.result()
	})
}

func TestCuratedWebSearchSubmit(t *testing.T) {
	e := newCuratorEnv(t, ``)
	useFakeProvider(t, "brave", answerWith("Brave says hi.", source("One", "https://example.com/1"), source("Two", "https://example.com/2")))
	run := e.runWebSearch("call-1", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
	pageURL := run.curatorURL()
	if got := recvURL(t, e.opened); got != pageURL {
		t.Fatalf("opened %s, named %s", got, pageURL)
	}
	c := newCuratorClient(t, pageURL)
	state := c.waitDone()
	events := state["events"].([]any)
	data := events[0].(map[string]any)["data"].(map[string]any)
	if len(events) != 1 || events[0].(map[string]any)["event"] != "result" || data["provider"] != "brave" || data["query"] != "q" ||
		data["queryIndex"] != 0.0 || data["slotIndex"] != 0.0 || len(data["results"].([]any)) != 2 {
		t.Fatalf("%v", events)
	}
	if d := data["results"].([]any)[0].(map[string]any); d["domain"] != "example.com" || d["snippet"] != "One snippet" {
		t.Fatalf("%v", d)
	}
	matchRE(t, `"defaultSummaryModel":"anthropic/claude-haiku-4-5"`, c.page())

	status, body := c.post("/summarize", map[string]any{"selected": []int{0}})
	meta, _ := body["meta"].(map[string]any)
	if status != 200 || body["summary"] != "generated summary" || meta["model"] != "anthropic/claude-haiku-4-5" || meta["fallbackUsed"] != false {
		t.Fatalf("%d %v", status, body)
	}
	if status, _ = c.post("/submit", map[string]any{"selected": []int{0}, "summary": " approved summary ", "summaryMeta": meta}); status != 200 {
		t.Fatal(status)
	}
	out := run.result()
	d := out.Details
	summary, _ := d["summary"].(map[string]any)
	if out.Text() != "approved summary" || d["curated"] != true || d["curatedFrom"] != 1 || d["queryCount"] != 1 || d["successfulQueries"] != 1 || d["totalResults"] != 2 ||
		summary["workflow"] != "summary-review" || summary["model"] != "anthropic/claude-haiku-4-5" || summary["text"] != "approved summary" || summary["fallbackUsed"] != false {
		t.Fatalf("%q %v", out.Text(), d)
	}
	queries, _ := d["curatedQueries"].([]map[string]any)
	sources, _ := queries[0]["sources"].([]map[string]any)
	if len(queries) != 1 || queries[0]["provider"] != "brave" || queries[0]["answer"] != "Brave says hi." || len(sources) != 2 || sources[1]["url"] != "https://example.com/2" {
		t.Fatalf("%v", queries)
	}
	// The call's state is gone and the server is closed.
	if _, err := newCuratorGet(pageURL); err == nil {
		t.Fatal("the curator still serves")
	}
	e.rt.cur.mu.Lock()
	left := len(e.rt.cur.pending) + len(e.rt.cur.active)
	e.rt.cur.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d entries left", left)
	}
}

func newCuratorGet(u string) (string, error) {
	resp, err := http.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data), nil
}

func TestCuratedWebSearchRawResults(t *testing.T) {
	e := newCuratorEnv(t, ``)
	useFakeProvider(t, "brave", func(_ context.Context, q string, _ SearchOptions) (*SearchResponse, error) {
		return &SearchResponse{Answer: "answer " + q, Results: []SearchResult{source(q, "https://example.com/"+q)}}, nil
	})
	run := e.runWebSearch("call", map[string]any{"queries": []any{"one", "two"}, "provider": "brave", "workflow": "summary-review"})
	c := newCuratorClient(t, run.curatorURL())
	c.waitDone()
	// Only the second query is kept and sent without a summary.
	if status, _ := c.post("/submit", map[string]any{"selected": []int{1}, "rawResults": true}); status != 200 {
		t.Fatal(status)
	}
	out := run.result()
	text := out.Text()
	if !strings.HasPrefix(text, "[These results were manually curated by the user in the browser. Use them as-is — do not re-search or discard.]\n\n**Provider:** brave\n\n") ||
		!strings.Contains(text, "answer two") || strings.Contains(text, "answer one") {
		t.Fatalf("%q", text)
	}
	if _, has := out.Details["summary"]; has || out.Details["curated"] != true || out.Details["curatedFrom"] != 2 || out.Details["queryCount"] != 1 {
		t.Fatalf("%v", out.Details)
	}
}

func TestCuratedWebSearchTimesOutWithTheDeterministicSummary(t *testing.T) {
	e := newCuratorEnv(t, `{"curatorTimeoutSeconds":1}`)
	useFakeProvider(t, "brave", answerWith("answer", source("One", "https://example.com/1")))
	started := time.Now()
	run := e.runWebSearch("call", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
	out := run.result()
	// The browser never connected: the session ends after the 5 second minimum.
	if d := time.Since(started); d < 4*time.Second {
		t.Fatalf("ended after %v", d)
	}
	summary, _ := out.Details["summary"].(map[string]any)
	if !strings.HasPrefix(out.Text(), "Summary based on the currently selected search results.") || summary["workflow"] != "summary-review" ||
		summary["model"] != nil || summary["fallbackUsed"] != true || summary["phase"] != "deterministic-fallback" || summary["fallbackReason"] != "deterministic-submit-fallback" {
		t.Fatalf("%q %v", out.Text(), out.Details)
	}
	if out.Details["curated"] != true || out.Details["cancelled"] != nil {
		t.Fatalf("%v", out.Details)
	}
}

func TestCuratedWebSearchAbortEndsAsStale(t *testing.T) {
	e := newCuratorEnv(t, ``)
	gate := make(chan struct{})
	useFakeProvider(t, "brave", func(ctx context.Context, q string, _ SearchOptions) (*SearchResponse, error) {
		<-ctx.Done()
		close(gate)
		return nil, ctx.Err()
	})
	run := e.runWebSearch("call", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
	pageURL := run.curatorURL()
	run.cancel()
	out := run.result()
	<-gate
	if out.Text() != "Search curation cancelled (stale)." || out.Details["cancelReason"] != "stale" || out.Details["curatorUrl"] != nil {
		t.Fatalf("%q %v", out.Text(), out.Details)
	}
	extra, _ := out.Details["extraLines"].([]string)
	if len(extra) != 1 || extra[0] != "curator: "+pageURL {
		t.Fatalf("%v", out.Details["extraLines"])
	}
}

func TestCuratedWebSearchKeepsTheErrorOfAFailedSearch(t *testing.T) {
	e := newCuratorEnv(t, ``)
	useFakeProvider(t, "brave", func(context.Context, string, SearchOptions) (*SearchResponse, error) {
		return nil, errBrowser
	})
	run := e.runWebSearch("call", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
	c := newCuratorClient(t, run.curatorURL())
	state := c.waitDone()
	ev := state["events"].([]any)[0].(map[string]any)
	data := ev["data"].(map[string]any)
	if ev["event"] != "search-error" || data["error"] != "spawn xdg-open ENOENT" || data["provider"] != "brave" || data["queryIndex"] != 0.0 {
		t.Fatalf("%v", ev)
	}
	if status, _ := c.post("/submit", map[string]any{"selected": []int{}}); status != 200 {
		t.Fatal(status)
	}
	out := run.result()
	if out.Details["successfulQueries"] != 0 || out.Details["curated"] != true {
		t.Fatalf("%v", out.Details)
	}
	if !strings.Contains(out.Text(), "spawn xdg-open ENOENT") {
		t.Fatalf("%q", out.Text())
	}
}

func TestCuratedWebSearchStreamsEveryProviderOfAnAllSearch(t *testing.T) {
	e := newCuratorEnv(t, ``)
	useFakeProvider(t, "brave", answerWith("brave answer", source("B", "https://example.com/b")))
	useFakeProvider(t, "exa", answerWith("exa answer", source("E", "https://example.com/e")))
	run := e.runWebSearch("call", map[string]any{"queries": []any{"one", "two"}, "provider": []any{"brave", "exa"}, "workflow": "summary-review"})
	c := newCuratorClient(t, run.curatorURL())
	state := c.waitDone()
	got := map[float64]string{}
	for _, ev := range state["events"].([]any) {
		d := ev.(map[string]any)["data"].(map[string]any)
		got[d["queryIndex"].(float64)] = d["query"].(string) + "/" + d["provider"].(string)
		if d["slotIndex"] == nil {
			t.Fatalf("no slot: %v", d)
		}
	}
	// Result index = entry index * query count + query index, so each provider answer of a query
	// has its own selectable index and the first answer keeps the query's own index.
	want := map[float64]string{0: "one/brave", 1: "two/brave", 2: "one/exa", 3: "two/exa"}
	if len(got) != 4 {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("index %v: %q, want %q", k, got[k], v)
		}
	}
	matchRE(t, `"defaultProvider":"all"`, c.page())
	run.cancel()
	run.result()
}

func TestCuratedWebSearchPersistsTheProviderChoiceInPlace(t *testing.T) {
	e := newCuratorEnv(t, `{"zeta": 1, "workflow": "summary-review", "alpha": [1, 2]}`)
	useFakeProvider(t, "brave", answerWith("a"))
	useFakeProvider(t, "exa", answerWith("b"))
	run := e.runWebSearch("call", map[string]any{"query": "q", "provider": "brave"})
	c := newCuratorClient(t, run.curatorURL())
	c.waitDone()
	if status, body := c.post("/provider", map[string]any{"provider": "exa"}); status != 200 {
		t.Fatalf("%d %v", status, body)
	}
	path := filepath.Join(ConfigDir(), "web-search.json")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(readFile(t, path), `"provider"`) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	want := "{\n  \"zeta\": 1,\n  \"workflow\": \"summary-review\",\n  \"alpha\": [\n    1,\n    2\n  ],\n  \"provider\": \"exa\"\n}\n"
	if got := readFile(t, path); got != want {
		t.Fatalf("%q", got)
	}
	run.cancel()
	run.result()
}

func TestWebSearchWorkflowResolution(t *testing.T) {
	// summary-review needs a UI; auto-summary does not; unknown values are none.
	for _, c := range []struct {
		input any
		ui    bool
		want  string
	}{
		{"summary-review", true, "summary-review"}, {" Summary-Review ", true, "summary-review"}, {"summary-review", false, "none"},
		{"auto-summary", false, "auto-summary"}, {"none", true, "none"}, {"bogus", true, "none"}, {nil, true, "none"}, {7.0, true, "none"},
	} {
		if got := ResolveWebSearchWorkflow(c.input, c.ui); got != c.want {
			t.Errorf("%v %v: %s, want %s", c.input, c.ui, got, c.want)
		}
	}
}

func TestWebSearchWorkflowIsReadFromTheConfigOnEveryCall(t *testing.T) {
	e := newCuratorEnv(t, ``)
	useFakeProvider(t, "brave", answerWith("answer", source("One", "https://example.com/1")))
	// workflow none: the plain result, no curator.
	out := e.runWebSearch("c1", map[string]any{"query": "q", "provider": "brave"}).result()
	if _, has := out.Details["summary"]; has || out.Details["curated"] != nil {
		t.Fatalf("%v", out.Details)
	}
	// /curator on writes the config; the next call, which names no workflow, auto-summarizes.
	e.rt.CuratorCommand("auto-summary")
	out = e.runWebSearch("c2", map[string]any{"query": "q", "provider": "brave"}).result()
	summary, _ := out.Details["summary"].(map[string]any)
	if out.Text() != "generated summary" || summary["workflow"] != "auto-summary" || out.Details["curated"] != nil {
		t.Fatalf("%q %v", out.Text(), out.Details)
	}
	noURL(t, e.opened)
	// An explicit workflow wins over the config.
	out = e.runWebSearch("c3", map[string]any{"query": "q", "provider": "brave", "workflow": "none"}).result()
	if _, has := out.Details["summary"]; has {
		t.Fatalf("%v", out.Details)
	}
}

func TestAutoSummaryNeedsNoCuratorAndFallsBackWithoutAModel(t *testing.T) {
	e := newCuratorEnv(t, `{"summaryModel":"anthropic/claude-haiku-4-5:high"}`)
	useFakeProvider(t, "brave", answerWith("answer", source("One", "https://example.com/1")))
	run := e.runWebSearch("c", map[string]any{"query": "q", "provider": "brave", "workflow": "auto-summary"})
	out := run.result()
	if !strings.HasPrefix(out.Text(), "generated summary") {
		t.Fatalf("%q", out.Text())
	}
	run.waitUpdate(func(u ToolOutput) bool {
		return u.Text() == "Generating summary..." && u.Details["phase"] == "generating-summary" && u.Details["progress"] == 1
	})
	noURL(t, e.opened)
	// The configured model, thinking level included, is the one asked.
	if got := modelValue(e.reg.lastCall().model); got != "anthropic/claude-haiku-4-5" {
		t.Fatal(got)
	}

	// No usable model: the deterministic summary, not an error.
	e2 := newCuratorEnv(t, ``)
	e2.reg.models = nil
	useFakeProvider(t, "brave", answerWith("answer", source("One", "https://example.com/1")))
	out = e2.runWebSearch("c", map[string]any{"query": "q", "provider": "brave", "workflow": "auto-summary"}).result()
	summary, _ := out.Details["summary"].(map[string]any)
	if !strings.HasPrefix(out.Text(), "Summary based on the currently selected search results.") || summary["fallbackUsed"] != true || summary["workflow"] != "auto-summary" {
		t.Fatalf("%q %v", out.Text(), out.Details)
	}
}

func TestCuratorWorkflowsNeedAUI(t *testing.T) {
	extractEnv(t, ``)
	host := &noUIHost{}
	rt, err := NewRuntime(host)
	noErr(t, err)
	rt.openBrowser = func(string) error { t.Error("the browser was opened"); return nil }
	useFakeProvider(t, "brave", answerWith("answer", source("One", "https://example.com/1")))
	out, err := mustTool(t, rt, "web_search").Execute(bg(), map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"}, nil)
	noErr(t, err)
	if _, curated := out.Details["curated"]; curated || !strings.Contains(out.Text(), "**Provider:** brave") {
		t.Fatalf("%q %v", out.Text(), out.Details)
	}
	// auto-summary runs without a UI but needs the extension context for its model.
	out, err = mustTool(t, rt, "web_search").Execute(bg(), map[string]any{"query": "q", "provider": "brave", "workflow": "auto-summary"}, nil)
	noErr(t, err)
	if out.Text() != "Error: Auto-summary requires an active extension context." || out.Details["error"] != "Missing extension context" {
		t.Fatalf("%q %v", out.Text(), out.Details)
	}
}

type noUIHost struct{ curatorHostFake }

func (*noUIHost) HasUI() bool { return false }

func TestApprovedRemainingSearchesApplyToInheritedWorkflowsOnly(t *testing.T) {
	e := newCuratorEnv(t, `{"workflow":"summary-review"}`)
	useFakeProvider(t, "brave", answerWith("answer", source("One", "https://example.com/1")))
	run := e.runWebSearch("c1", map[string]any{"query": "q", "provider": "brave"})
	c := newCuratorClient(t, run.curatorURL())
	c.waitDone()
	if status, _ := c.post("/submit", map[string]any{"selected": []int{0}, "summary": "ok", "autoApproveRemainingSearches": true}); status != 200 {
		t.Fatal(status)
	}
	run.result()
	<-e.opened
	// Inherited summary-review now auto-summarizes: no curator opens.
	out := e.runWebSearch("c2", map[string]any{"query": "q", "provider": "brave"}).result()
	if s, _ := out.Details["summary"].(map[string]any); s["workflow"] != "auto-summary" {
		t.Fatalf("%v", out.Details)
	}
	noURL(t, e.opened)
	// An explicit summary-review still opens the curator.
	run3 := e.runWebSearch("c3", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
	recvURL(t, e.opened)
	run3.cancel()
	run3.result()
	// The run ending resets the approval.
	e.rt.CuratorRun().Reset()
	run4 := e.runWebSearch("c4", map[string]any{"query": "q", "provider": "brave"})
	recvURL(t, e.opened)
	run4.cancel()
	run4.result()
}

func TestSessionChangeClosesEveryCurator(t *testing.T) {
	e := newCuratorEnv(t, ``)
	useFakeProvider(t, "brave", answerWith("answer"))
	run := e.runWebSearch("c1", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
	pageURL := run.curatorURL()
	newCuratorClient(t, pageURL).waitDone()
	e.rt.SessionStarted(nil)
	out := run.result()
	if out.Details["cancelReason"] != "stale" {
		t.Fatalf("%v", out.Details)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := newCuratorGet(pageURL); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the curator still serves after the session changed")
}

func TestCuratorURLEscapesNothingInTheHost(t *testing.T) {
	e := newCuratorEnv(t, ``)
	useFakeProvider(t, "brave", answerWith("a"))
	run := e.runWebSearch("c", map[string]any{"query": "q", "provider": "brave", "workflow": "summary-review"})
	u, err := url.Parse(run.curatorURL())
	noErr(t, err)
	if u.Hostname() != "localhost" || u.Path != "/" || u.Query().Get("session") == "" {
		t.Fatal(u)
	}
	run.cancel()
	run.result()
}
