package websearch

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func activityClock(t *testing.T, times ...int64) {
	t.Helper()
	old := nowMs
	var mu sync.Mutex
	i := 0
	nowMs = func() int64 {
		mu.Lock()
		defer mu.Unlock()
		v := times[min(i, len(times)-1)]
		i++
		return v
	}
	t.Cleanup(func() { nowMs = old })
}

func TestActivityMonitorLogsAndKeepsTheLastTen(t *testing.T) {
	m := NewActivityMonitor()
	activityClock(t, 100, 200, 300, 400)
	api := m.LogStart("api", "hello")
	fetch := m.LogStart("fetch", "https://example.com/x")
	if api != "act-1" || fetch != "act-2" {
		t.Fatal(api, fetch)
	}
	m.LogComplete(api, 200)
	m.LogError(fetch, "boom")
	m.LogComplete("act-99", 500) // an unknown id changes nothing
	got := m.Entries()
	if len(got) != 2 || got[0].Type != "api" || got[0].Query != "hello" || got[0].Status == nil || *got[0].Status != 200 || got[0].StartTime != 100 || got[0].EndTime != 300 ||
		got[1].Type != "fetch" || got[1].URL != "https://example.com/x" || got[1].Error != "boom" || got[1].Status != nil || got[1].EndTime != 400 {
		t.Fatalf("%+v", got)
	}
	// Entries are copies.
	*got[0].Status = 500
	if *m.Entries()[0].Status != 200 {
		t.Fatal("Entries exposes the monitor's state")
	}
	for i := range 11 {
		m.LogStart("api", "q"+jsNumber(float64(i)))
	}
	entries := m.Entries()
	if len(entries) != 10 || entries[0].Query != "q1" || entries[9].Query != "q10" || entries[9].ID != "act-13" {
		t.Fatalf("%d %+v", len(entries), entries[0])
	}
	// Clear empties the log and the rate limit but keeps counting ids.
	m.UpdateRateLimit(RateLimitInfo{Used: 3, Max: 10, WindowMs: 60000})
	m.Clear()
	if len(m.Entries()) != 0 || m.RateLimit() != (RateLimitInfo{Used: 0, Max: 10, WindowMs: 60000}) || m.LogStart("api", "x") != "act-14" {
		t.Fatal("clear")
	}
}

func TestActivityMonitorListeners(t *testing.T) {
	m := NewActivityMonitor()
	var order []string
	off1 := m.OnUpdate(func() { order = append(order, "first") })
	m.OnUpdate(func() { order = append(order, "second"); panic("a listener's error is ignored") })
	m.OnUpdate(func() { order = append(order, "third") })
	id := m.LogStart("api", "q")
	m.LogComplete(id, 200)
	if strings.Join(order, ",") != "first,second,third,first,second,third" {
		t.Fatal(order)
	}
	off1()
	order = nil
	m.UpdateRateLimit(RateLimitInfo{Used: 1, Max: 10, WindowMs: 60000})
	m.Clear()
	if strings.Join(order, ",") != "second,third,second,third" {
		t.Fatal(order)
	}
	// An unknown id does not notify.
	order = nil
	m.LogError("nope", "x")
	if order != nil {
		t.Fatal(order)
	}
}

func TestActivityWidgetLines(t *testing.T) {
	m := NewActivityMonitor()
	if got := ActivityWidgetLines(m, markTheme{}, 5000); len(got) != 4 || got[0] != "[accent:─── Web Search Activity "+strings.Repeat("─", 36)+"]" || got[1] != "[muted:  No activity yet]" ||
		got[2] != "[accent:"+strings.Repeat("─", 60)+"]" || got[3] != "[muted:Rate: 0/10]" {
		t.Fatalf("%q", got)
	}
	activityClock(t, 1000, 1000, 1000, 1000, 1000, 2250, 2000, 3000, 3000)
	ok := m.LogStart("api", "hello world")                                            // 1000
	pending := m.LogStart("fetch", "https://example.com/a/rather/long/pathat/is/cut") // 1000
	failed := m.LogStart("api", strings.Repeat("é", 40))                              // 1000
	aborted := m.LogStart("fetch", "http://example.com")                              // 1000
	notFound := m.LogStart("fetch", "https://example.com/404")                        // 1000
	_ = pending
	m.LogComplete(ok, 200)       // 2250: 1.25s rounds up to 1.3s like toFixed
	m.LogError(failed, "boom")   // 2000
	m.LogComplete(aborted, 0)    // 3000
	m.LogComplete(notFound, 404) // 3000
	oldest := int64(1000)
	m.UpdateRateLimit(RateLimitInfo{Used: 7, Max: 10, OldestTimestamp: &oldest, WindowMs: 60000})
	got := ActivityWidgetLines(m, markTheme{}, 4001)
	pad := func(s string, n int) string { return s + strings.Repeat(" ", n-len([]rune(s))) }
	want := []string{
		"[accent:─── Web Search Activity " + strings.Repeat("─", 36) + "]",
		"  API  " + pad(`"hello world"`, 32) + "   200  1.3s [success:✓]",
		"  GET  " + pad("example.com/a/rather/long/path", 32) + "   ...  3.0s [warning:⋯]",
		"  API  " + pad(`"`+strings.Repeat("é", 28)+`"`, 32) + "   err  1.0s [error:✗]",
		"  GET  " + pad("example.com", 32) + " abort  2.0s [muted:○]",
		"  GET  " + pad("example.com/404", 32) + "   404  2.0s [error:✗]",
		"[accent:" + strings.Repeat("─", 60) + "]",
		"[muted:Rate: 7/10][dim: (resets in 57s)]",
	}
	if len(got) != len(want) {
		t.Fatalf("%d lines: %q", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

func TestJSToFixedMatchesNumberToFixed(t *testing.T) {
	for in, want := range map[float64]string{0: "0.0", 0.25: "0.3", 0.15: "0.1", 0.05: "0.1", 1.25: "1.3", 1.2: "1.2", 59.96: "60.0", 0.04: "0.0", -0.25: "-0.3", 12.35: "12.3", 0.35: "0.3", 0.45: "0.5"} {
		if got := jsToFixed1(in); got != want {
			t.Errorf("%v: %s, want %s", in, got, want)
		}
	}
}

func TestTruncateToWidthCountsCells(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 3, "hel"}, {"hello", 9, "hello"}, {"日本語です", 6, "日本語"}, {"日本語です", 5, "日本"}, {"e\u0301e\u0301", 1, "e\u0301"}, {"", 4, ""},
	} {
		if got := truncateToWidth(c.in, c.max); got != c.want {
			t.Errorf("%q/%d: %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestActivityWidgetFollowsTheMonitorAndTheSession(t *testing.T) {
	r, _ := newRuntime(t, ``)
	Activity.Clear()
	t.Cleanup(Activity.Clear)
	ui := &fakeWidgetUI{}
	r.ToggleActivityWidget(ui)
	before := ui.count()
	Activity.LogStart("api", "live")
	if ui.count() <= before || !strings.Contains(strings.Join(ui.last().lines, "\n"), `"live"`) {
		t.Fatalf("the widget did not follow the monitor: %+v", ui.last())
	}
	// A new session starts with an empty log; a visible widget is redrawn for the new UI.
	ui2 := &fakeWidgetUI{}
	r.ResetActivity(ui2)
	if got := strings.Join(ui2.last().lines, "\n"); !strings.Contains(got, "No activity yet") {
		t.Fatalf("%s", got)
	}
	n := ui.count()
	Activity.LogStart("api", "after")
	if ui.count() != n {
		t.Fatal("the old UI is still updated")
	}
	if !strings.Contains(strings.Join(ui2.last().lines, "\n"), `"after"`) {
		t.Fatal("the new UI is not updated")
	}
	// Shutdown hides the widget state, clears the log and stops updates.
	r.ShutdownActivity()
	m := ui2.count()
	Activity.LogStart("api", "later")
	if ui2.count() != m || len(Activity.Entries()) != 1 {
		t.Fatalf("%d %d", ui2.count()-m, len(Activity.Entries()))
	}
}

func TestProvidersLogTheirCallsToTheActivityMonitor(t *testing.T) {
	last := func(t *testing.T) ActivityEntry {
		t.Helper()
		entries := Activity.Entries()
		if len(entries) == 0 {
			t.Fatal("nothing logged")
		}
		return entries[len(entries)-1]
	}
	t.Run("brave success logs the query it sent and the status", func(t *testing.T) {
		Activity.Clear()
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		useNet(t, func(netCall) netReply { return jsonReply(map[string]any{"web": map[string]any{"results": []any{}}}) })
		_, err := SearchWithBrave(bg(), "sdk docs", SearchOptions{DomainFilter: []string{"github.com"}})
		noErr(t, err)
		e := last(t)
		if e.Type != "api" || e.Query != "sdk docs site:github.com" || e.Status == nil || *e.Status != 200 || e.Error != "" || e.EndTime == 0 {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("brave error keeps the thrown message", func(t *testing.T) {
		Activity.Clear()
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		useNet(t, func(netCall) netReply { return reply(500, "down brave-test-key") })
		_, err := SearchWithBrave(bg(), "q", SearchOptions{})
		wantErr(t, err, `Brave Search API error 500`)
		e := last(t)
		if e.Error != err.Error() || strings.Contains(e.Error, "brave-test-key") || e.Status != nil {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("a transport error mentioning an abort closes the entry as aborted", func(t *testing.T) {
		Activity.Clear()
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		useNet(t, func(netCall) netReply { return netReply{Err: errors.New("This operation was aborted")} })
		_, err := SearchWithBrave(bg(), "q", SearchOptions{})
		if err == nil {
			t.Fatal("no error")
		}
		if e := last(t); e.Status == nil || *e.Status != 0 || e.Error != "" {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("tavily logs the status of a failure and 200 for success", func(t *testing.T) {
		Activity.Clear()
		isolate(t)
		t.Setenv("TAVILY_API_KEY", "tavily-key")
		useNet(t, func(netCall) netReply { return reply(432, "plan") })
		_, err := SearchWithTavily(bg(), "q", SearchOptions{})
		wantErr(t, err, `Tavily API error 432`)
		if e := last(t); e.Status == nil || *e.Status != 432 || e.Query != "q" {
			t.Fatalf("%+v", e)
		}
		Activity.Clear()
		useNet(t, func(netCall) netReply { return reply(200, tavilyBody("a")) })
		_, err = SearchWithTavily(bg(), "q2", SearchOptions{})
		noErr(t, err)
		if e := last(t); e.Status == nil || *e.Status != 200 || e.Query != "q2" {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("kagi logs once with the response status", func(t *testing.T) {
		Activity.Clear()
		isolate(t)
		t.Setenv("KAGI_API_KEY", "kagi-key")
		useNet(t, func(netCall) netReply { return reply(200, kagiBody) })
		_, err := SearchWithKagi(bg(), "kagi q", SearchOptions{})
		noErr(t, err)
		if entries := Activity.Entries(); len(entries) != 1 || entries[0].Status == nil || *entries[0].Status != 200 || entries[0].Query != "kagi q" {
			t.Fatalf("%+v", entries)
		}
	})
	t.Run("perplexity reports the rate limit it keeps", func(t *testing.T) {
		Activity.Clear()
		isolate(t)
		t.Setenv("PERPLEXITY_API_KEY", "pplx-key")
		useNet(t, func(netCall) netReply {
			return reply(200, `{"choices":[{"message":{"content":"answer"}}],"citations":[]}`)
		})
		_, err := SearchWithPerplexity(bg(), "q", SearchOptions{})
		noErr(t, err)
		rate := Activity.RateLimit()
		if rate.Used != 1 || rate.Max != 10 || rate.WindowMs != 60000 || rate.OldestTimestamp == nil {
			t.Fatalf("%+v", rate)
		}
		if e := last(t); e.Status == nil || *e.Status != 200 {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("a page fetch logs a fetch entry with its status", func(t *testing.T) {
		Activity.Clear()
		extractEnv(t, "")
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "plain text body", Header: http.Header{"Content-Type": {"text/plain"}}}
		})
		FetchAllContent(bg(), []string{"https://example.com/data"}, pageOpts())
		if e := last(t); e.Type != "fetch" || e.URL != "https://example.com/data" || e.Status == nil || *e.Status != 200 {
			t.Fatalf("%+v", e)
		}
		Activity.Clear()
		useNet(t, func(netCall) netReply { return reply(404, "gone") })
		FetchAllContent(bg(), []string{"https://example.com/gone"}, pageOpts())
		if e := last(t); e.Status == nil || *e.Status != 404 {
			t.Fatalf("%+v", e)
		}
	})
}
