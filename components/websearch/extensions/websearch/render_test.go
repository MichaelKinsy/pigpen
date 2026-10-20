package websearch

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// renderedText flattens a rendered component the way a terminal shows its text: a Text is its text, a
// Box the text of its children one per line.
func renderedText(t *testing.T, n kit.Node) []string {
	t.Helper()
	switch x := n.(type) {
	case *kit.Text:
		return []string{x.Text}
	case *kit.Box:
		var out []string
		for _, c := range x.Children {
			out = append(out, renderedText(t, c)...)
		}
		return out
	}
	t.Fatalf("unexpected node %T", n)
	return nil
}

// markTheme marks every styled piece with its token so a test sees which color the original uses.
type markTheme struct{}

func (markTheme) Fg(token, text string) string { return "[" + token + ":" + text + "]" }
func (markTheme) Bg(token, text string) string { return "{" + token + ":" + text + "}" }
func (markTheme) Bold(text string) string      { return "<b>" + text + "</b>" }

var staleCancel = &SearchErrorDetails{
	Error:              new("Search curation cancelled (stale)."),
	Cancelled:          true,
	CancelReason:       "stale",
	BrowserConnected:   new(false),
	LastHeartbeatAgeMs: new(31200.0),
	QueryCount:         3,
	CancelledQueries: []CancelledQueryDetail{
		{Query: "z.ai API usage endpoint coding plan credits", Provider: "perplexity", ResultCount: 5},
		{Query: "z.ai GLM coding plan API key usage limits", Provider: "perplexity", Error: "Connection error"},
	},
}

func TestUpstream_search_error_render(t *testing.T) {
	const F = "search-error-render"
	tw(t, F, "cancel/error path is NOT a dead-end single line: expanded plan has >1 line", func(t *testing.T) {
		plan := BuildSearchErrorPlan(staleCancel)
		if plan == nil || len(plan.Expanded) <= 1 {
			t.Fatalf("%+v", plan)
		}
		matchRE(t, `Search curation cancelled \(stale\)`, strings.Join(plan.Expanded, "\n"))
	})
	tw(t, F, "expanded plan surfaces the diagnostics that were previously discarded", func(t *testing.T) {
		expanded := strings.Join(BuildSearchErrorPlan(staleCancel).Expanded, "\n")
		for _, re := range []string{
			`cancel reason\s*:\s*stale`, `browser\s*:\s*never connected`, `queries started\s*:\s*3`, `queries done\s*:\s*2`,
			`z\.ai API usage endpoint`, `5 sources`, `\[err\]`, `Connection error`, `last heartbeat  : 31s ago`, `queries errored : 1`,
		} {
			matchRE(t, re, expanded)
		}
	})
	tw(t, F, "collapsed view is a short summary WITH a ctrl+o expand hint (Ctrl+O now does something)", func(t *testing.T) {
		plan := BuildSearchErrorPlan(staleCancel)
		collapsed := strings.Join(plan.Collapsed, "\n")
		matchRE(t, `2/3 queries completed`, collapsed)
		matchRE(t, `browser never connected`, collapsed)
		if plan.ExpandHint == nil {
			t.Fatal("no expand hint")
		}
		matchRE(t, `(?i)ctrl\+o to expand`, *plan.ExpandHint)
		matchRE(t, `\d+ more lines`, *plan.ExpandHint)
		// The hint counts the hidden lines: everything but the headline and the preview.
		want := len(plan.Expanded) - (1 + len(plan.Collapsed))
		if *plan.ExpandHint != "... ("+jsNumber(float64(want))+" more lines, "+jsNumber(float64(len(plan.Expanded)))+" total, ctrl+o to expand)" {
			t.Fatal(*plan.ExpandHint)
		}
	})
	tw(t, F, "plain (non-cancel) error stays a clean single line — no diagnostic noise", func(t *testing.T) {
		plan := BuildSearchErrorPlan(&SearchErrorDetails{Error: new("No query provided")})
		if plan == nil || len(plan.Expanded) != 1 || len(plan.Collapsed) != 0 || plan.ExpandHint != nil {
			t.Fatalf("%+v", plan)
		}
	})
	tw(t, F, "fetch_content-style error (extras, no cancel) is expandable without browser diagnostics", func(t *testing.T) {
		plan := BuildSearchErrorPlan(&SearchErrorDetails{
			Error:      new("Failed to fetch: https://example.com/x (404)"),
			ExtraLines: []string{"urls: 1/2 succeeded", "response id: resp_abc", "  \u25b8 https://example.com/x"},
		})
		expanded := strings.Join(plan.Expanded, "\n")
		if len(plan.Expanded) <= 1 {
			t.Fatal(plan.Expanded)
		}
		matchRE(t, `urls: 1/2 succeeded`, expanded)
		matchRE(t, `resp_abc`, expanded)
		// Curator and browser diagnostics belong to cancelled searches.
		noMatchRE(t, `browser|cancel reason|queries started`, expanded)
		if plan.ExpandHint == nil {
			t.Fatal("no hint")
		}
		matchRE(t, `(?i)ctrl\+o to expand`, *plan.ExpandHint)
	})
	tw(t, F, "non-search error with extras shows detail without browser diagnostics", func(t *testing.T) {
		plan := BuildSearchErrorPlan(&SearchErrorDetails{Error: new("Exa search failed: connection reset"), ExtraLines: []string{"query: how to use p-limit concurrency"}})
		matchRE(t, `query: how to use p-limit concurrency`, strings.Join(plan.Expanded, "\n"))
		noMatchRE(t, `browser`, strings.Join(plan.Expanded, "\n"))
		if plan.ExpandHint == nil {
			t.Fatal("no hint")
		}
	})
	tw(t, F, "error with NO extras and NO cancel stays single-line", func(t *testing.T) {
		plan := BuildSearchErrorPlan(&SearchErrorDetails{Error: new("Some transient error")})
		if plan == nil || len(plan.Expanded) != 1 || plan.ExpandHint != nil {
			t.Fatalf("%+v", plan)
		}
	})
	tw(t, F, "non-error result yields null so the caller falls through to the success renderer", func(t *testing.T) {
		if BuildSearchErrorPlan(&SearchErrorDetails{QueryCount: 3}) != nil || BuildSearchErrorPlan(nil) != nil {
			t.Fatal("a result without an error got a plan")
		}
	})
	tw(t, F, "index.ts imports buildSearchErrorPlan and wires it into the web_search error path", func(t *testing.T) {
		// Behaviour of the wiring: the error results of the three tools that render results go
		// through the plan (expanded text with diagnostics, collapsed box with the hint), and a
		// cancelled curation carries the partial diagnostics the plan needs.
		r, _ := newRuntime(t, ``)
		cancelled := buildCurationCancelledReturn("stale", cancelPartial{
			queries: []QueryResultData{{Query: "q1", Provider: "perplexity", Results: []SearchResult{{}, {}}}}, queryCount: 3,
			browserConnected: new(false), lastHeartbeatAgeMs: new(int64(31200)), curatorURL: "http://localhost:1/?session=x", browserOpenError: "no browser",
		})
		call := func(tool string, out ToolOutput, expanded bool) []string {
			t.Helper()
			content := []map[string]any{{"type": "text", "text": out.Text()}}
			n, err := r.RenderResult(tool, content, normalizeDetails(out.Details), expanded, false, plainTheme{})
			noErr(t, err)
			return renderedText(t, n)
		}
		exp := strings.Join(call("web_search", cancelled, true), "\n")
		for _, re := range []string{`Search curation cancelled \(stale\)\.`, `cancel reason   : stale`, `browser         : never connected`, `queries started : 3`, `queries done    : 1`, `\[ok\]  "q1" \(perplexity\) — 2 sources`, `curator: http://localhost:1/\?session=x`, `browser open error: no browser`} {
			matchRE(t, re, exp)
		}
		col := call("web_search", cancelled, false)
		if len(col) < 3 || col[0] != "Search curation cancelled (stale)." || !regexp.MustCompile(`^\.\.\. \(\d+ more lines, \d+ total, ctrl\+o to expand\)$`).MatchString(col[len(col)-1]) {
			t.Fatalf("%q", col)
		}
		fetchErr := ToolOutput{Content: []ContentBlock{{Type: "text", Text: "Error: Failed to fetch"}}, Details: map[string]any{
			"error": "Failed to fetch", "urlCount": 2, "successful": 1, "responseId": "resp_abc", "urls": []string{"https://a", "https://b"},
		}}
		fexp := strings.Join(call("fetch_content", fetchErr, true), "\n")
		for _, re := range []string{`Failed to fetch`, `urls: 1/2 succeeded`, `response id: resp_abc`, `▸ https://a`, `▸ https://b`} {
			matchRE(t, re, fexp)
		}
		getErr := ToolOutput{Details: map[string]any{"error": "Not found", "query": "q", "title": "T"}}
		gexp := strings.Join(call("get_search_content", getErr, true), "\n")
		matchRE(t, `query: q`, gexp)
		matchRE(t, `resource: T`, gexp)
	})
}

// normalizeDetails turns the details a tool returns into what the renderer receives over the wire:
// plain decoded JSON.
func normalizeDetails(d map[string]any) any {
	raw, _ := marshalNoEscape(d)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func TestSearchErrorPlanMatchesTheOriginalOnItsEdges(t *testing.T) {
	// A cancelled result without partial data: headline default and the diagnostics block.
	plan := BuildSearchErrorPlan(&SearchErrorDetails{Cancelled: true})
	want := []string{"Search cancelled.", "", "Diagnostics:", "  cancel reason   : unknown", "  browser         : unknown"}
	if !reflect.DeepEqual(plan.Expanded, want) {
		t.Fatalf("%q", plan.Expanded)
	}
	if len(plan.Collapsed) != 0 || plan.ExpandHint == nil || *plan.ExpandHint != "... (4 more lines, 5 total, ctrl+o to expand)" {
		t.Fatalf("%+v", plan)
	}
	// An empty error is not an error.
	if BuildSearchErrorPlan(&SearchErrorDetails{Error: new("")}) != nil {
		t.Fatal("an empty error produced a plan")
	}
	// Long query and error texts are cut with an ellipsis; one source is singular.
	long := strings.Repeat("x", 80)
	plan = BuildSearchErrorPlan(&SearchErrorDetails{Error: new("e"), CancelledQueries: []CancelledQueryDetail{
		{Query: long, ResultCount: 1}, {Query: "q", Error: strings.Repeat("y", 90)},
	}})
	all := strings.Join(plan.Expanded, "\n")
	matchRE(t, `\[ok\]  "x{51}…" — 1 source\n`, all)
	matchRE(t, `\[err\] "q" — y{59}…`, all)
	// Details alone give a two-line preview.
	plan = BuildSearchErrorPlan(&SearchErrorDetails{Error: new("e"), ExtraLines: []string{"a", "b", "c"}})
	if !reflect.DeepEqual(plan.Collapsed, []string{"a", "b"}) {
		t.Fatalf("%q", plan.Collapsed)
	}
}

func TestRenderCallColors(t *testing.T) {
	r, _ := newRuntime(t, ``)
	got, err := r.callText("web_search", map[string]any{"queries": []any{"a", "b"}}, markTheme{})
	noErr(t, err)
	want := "[toolTitle:<b>search </b>][accent:2 queries]\n[muted:  \"a\"]\n[muted:  \"b\"]"
	if got != want {
		t.Fatalf("%q", got)
	}
	got, _ = r.callText("fetch_content", map[string]any{"url": "https://example.com", "mode": "raw", "timestamp": "1:00", "frames": 2.0, "prompt": "p", "model": "m", "auth": true}, markTheme{})
	for _, piece := range []string{
		"[toolTitle:<b>fetch </b>][accent:https://example.com]", "[dim:  mode: ][warning:raw]", "[dim:  timestamp: ][warning:1:00]",
		"[dim:  frames: ][warning:2]", `[dim:  prompt: ][muted:"p"]`, "[dim:  model: ][warning:m]", "[dim:  auth: ][warning:true]",
	} {
		if !strings.Contains(got, piece) {
			t.Errorf("%q lacks %q", got, piece)
		}
	}
	got, _ = r.callText("fetch_content", map[string]any{}, markTheme{})
	if got != "[toolTitle:<b>fetch </b>][error:(no URL)]" {
		t.Fatalf("%q", got)
	}
	got, err = r.callText("get_search_content", map[string]any{"responseId": "abcdefghijkl", "url": "https://example.com/a/very/long/path/to/a/page", "offset": 10.0, "findText": []any{"a", "b"}}, markTheme{})
	noErr(t, err)
	if got != "[toolTitle:<b>get_content </b>][accent:https://example.com/a/very/... @ 10 · find 2]" {
		t.Fatalf("%q", got)
	}
}

func TestRenderCallOfGetContentFollowsJavaScriptTruthiness(t *testing.T) {
	r, _ := newRuntime(t, ``)
	call := func(args map[string]any) (string, error) { return r.callText("get_search_content", args, plainTheme{}) }
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"responseId": "abcdefghijkl"}, "get_content abcdefgh"},
		{map[string]any{"responseId": "r", "query": ""}, "get_content r"},
		{map[string]any{"responseId": "r", "query": "q"}, `get_content query="q"`},
		{map[string]any{"responseId": "r", "queryIndex": 0.0}, "get_content queryIndex=0"},
		{map[string]any{"responseId": "r", "queryIndex": nil}, "get_content queryIndex=null"},
		{map[string]any{"responseId": "r", "urlIndex": 2.0, "offset": 5.0}, "get_content urlIndex=2 @ 5"},
		{map[string]any{"responseId": "r", "offset": 7.0}, "get_content offset=7"},
		{map[string]any{"responseId": "r", "findText": "x"}, "get_content find 1"},
		{map[string]any{"responseId": "r", "findText": nil}, "get_content find 1"},
		{map[string]any{"responseId": "r", "url": "https://example.com/1234567890"}, "get_content https://example.com/1234567890"},
	} {
		got, err := call(c.args)
		if err != nil || got != c.want {
			t.Errorf("%v: %q %v, want %q", c.args, got, err, c.want)
		}
	}
	// The original throws on a missing responseId when nothing else names a target: the renderer fails.
	if _, err := call(map[string]any{}); err == nil {
		t.Fatal("a missing responseId rendered")
	}
}

func TestRenderResultOfWebSearch(t *testing.T) {
	r, _ := newRuntime(t, ``)
	text := "**Provider:** brave\n\nAnswer.\n\n---\n\n**Sources:**\n1. One\n   https://example.com/1"
	content := []map[string]any{{"type": "text", "text": text}}
	details := map[string]any{"queryCount": 1.0, "successfulQueries": 1.0, "totalResults": 1.0}
	render := func(content []map[string]any, details map[string]any, expanded, partial bool) []string {
		t.Helper()
		n, err := r.RenderResult("web_search", content, details, expanded, partial, markTheme{})
		noErr(t, err)
		return renderedText(t, n)
	}
	// Collapsed: status, the first content line without markup, and how much is hidden.
	got := render(content, details, false, false)
	want := []string{"[success:1 sources]", "[dim:Provider: brave]", "[muted:\n... (8 more lines, 10 total, ctrl+o to expand)]"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	// Expanded: the status then every line of the preview.
	got = render(content, details, true, false)
	if len(got) != 1 || !strings.HasPrefix(got[0], "[success:1 sources]\n[dim:**Provider:** brave]\n[dim:]\n[dim:Answer.]") {
		t.Fatalf("%q", got)
	}
	// Several queries and a curated result with a summary.
	details = map[string]any{
		"queryCount": 1.0, "successfulQueries": 1.0, "totalResults": 2.0, "curated": true, "curatedFrom": 3.0,
		"fetchId": "f", "fetchUrls": []any{"https://a", "https://b"},
		"summary":        map[string]any{"text": "line one\nline two", "workflow": "summary-review", "model": nil, "durationMs": 12.0, "tokenEstimate": 5.0, "fallbackUsed": true, "fallbackReason": "why", "phase": "deterministic-fallback", "edited": false},
		"curatedQueries": []any{map[string]any{"query": "q", "provider": "brave", "answer": "ans", "sources": []any{map[string]any{"title": "T", "url": "https://example.com/p"}}, "error": nil}},
	}
	got = render(content, details, true, false)
	exp := got[0]
	for _, piece := range []string{
		"[success:2 sources][muted: (1/3 queries curated)][muted: (fetching 2 URLs)]",
		"[accent:── Summary (summary-review) " + strings.Repeat("─", 32) + "]",
		"  line one\n  line two",
		"[dim:  model=deterministic · duration=12ms · tokens~5 · fallback=true · phase=deterministic-fallback · edited=false · reason=why]",
		"[accent:── Curated Results (1 of 3 queries kept) " + strings.Repeat("─", 24) + "]",
		"[accent:  \"q\" (brave)]", "  ans", "[muted:  ▸ T][dim: · example.com]", "[muted:Fetching 2 URLs in background]",
	} {
		if !strings.Contains(exp, piece) {
			t.Errorf("expanded lacks %q:\n%s", piece, exp)
		}
	}
	// Collapsed with a summary previews the summary.
	got = render(content, details, false, false)
	if got[0] != "[success:2 sources][muted: (1/3 queries curated)][muted: (fetching 2 URLs)]" || got[1] != "[dim:line one\nline two]" {
		t.Fatalf("%q", got)
	}
	// Partial results: progress bars and the curator phases.
	partials := []struct {
		details map[string]any
		want    string
	}{
		{map[string]any{"phase": "searching", "progress": 0.5, "currentQuery": strings.Repeat("q", 50)}, "[accent:[█████░░░░░] " + strings.Repeat("q", 37) + "...]"},
		{map[string]any{"phase": "search", "progress": 0.0}, "[accent:[░░░░░░░░░░] search]"},
		{map[string]any{"progress": 1.0}, "[accent:[██████████] searching]"},
		{map[string]any{"phase": "curating", "curatorUrl": "http://x", "timeoutSeconds": 20.0, "shortcut": "ctrl+shift+s"}, "[accent:waiting for summary approval in browser...]\n[muted:  http://x]\n[dim:  auto-submits after 20s idle; ctrl+shift+s reopens]"},
		{map[string]any{"phase": "generating-summary"}, "[accent:generating summary draft...]\n[dim:  ctrl+shift+s reopens]"},
		{map[string]any{"phase": "waiting-for-approval", "timeoutSeconds": 5.0}, "[accent:summary draft ready; approve in browser...]\n[dim:  auto-submits after 5s idle; ctrl+shift+s reopens]"},
		{map[string]any{"phase": "curator-fallback", "curatorUrl": "http://x", "browserOpenError": "boom"}, "[warning:Open the search curator manually:]\n[muted:  http://x]\n[dim:  auto-open failed: boom]\n[dim:  ctrl+shift+s reopens]"},
	}
	for _, c := range partials {
		if got := render(nil, c.details, false, true); len(got) != 1 || got[0] != c.want {
			t.Errorf("%v: %q, want %q", c.details, got, c.want)
		}
	}
	// A progress outside 0..1 fails the renderer (the original's String.repeat throws).
	if _, err := r.RenderResult("web_search", nil, map[string]any{"progress": 1.5}, false, true, markTheme{}); err == nil {
		t.Fatal("progress 1.5 rendered")
	}
	// Without details the original prints "undefined".
	got = render(content, nil, false, false)
	if got[0] != "[success:undefined/undefined queries, 0 sources]" {
		t.Fatalf("%q", got)
	}
}

func TestRenderResultOfFetchAndGetContent(t *testing.T) {
	r, _ := newRuntime(t, ``)
	text := strings.Repeat("a", 250)
	content := []map[string]any{{"type": "text", "text": text}}
	render := func(tool string, details map[string]any, expanded, partial bool) string {
		t.Helper()
		n, err := r.RenderResult(tool, content, details, expanded, partial, markTheme{})
		noErr(t, err)
		return strings.Join(renderedText(t, n), "\n")
	}
	one := map[string]any{"urlCount": 1.0, "totalChars": 250.0, "title": "Page", "truncated": true, "duration": 65.7, "imageCount": 2.0, "prompt": "ask", "timestamp": "1:00", "frames": 3.0}
	if got := render("fetch_content", one, false, false); got != "[success:Page][muted: (250 chars)][accent: [2 images]][warning: [truncated]][muted: | 1:05 total]\n[dim:"+strings.Repeat("a", 200)+"...]" {
		t.Fatalf("%q", got)
	}
	exp := render("fetch_content", one, true, false)
	for _, piece := range []string{`[dim:  prompt: "ask"]`, "[dim:  timestamp: 1:00]", "[dim:  frames: 3]", "[dim:" + text + "]"} {
		if !strings.Contains(exp, piece) {
			t.Errorf("%q lacks %q", exp, piece)
		}
	}
	if got := render("fetch_content", map[string]any{"urlCount": 1.0, "totalChars": 3.0, "hasImage": true}, false, false); !strings.HasPrefix(got, "[success:Untitled][muted: (3 chars)][accent: [image]]") {
		t.Fatalf("%q", got)
	}
	many := map[string]any{"urlCount": 3.0, "successful": 2.0}
	if got := render("fetch_content", many, false, false); got != "[success:2/3 URLs][muted: (content stored)]" {
		t.Fatalf("%q", got)
	}
	if got := render("fetch_content", map[string]any{"urlCount": 3.0, "successful": 0.0}, true, false); !strings.HasPrefix(got, "[error:0/3 URLs][muted: (content stored)]\n[dim:") {
		t.Fatalf("%q", got)
	}
	if got := render("fetch_content", map[string]any{"phase": "extracting", "progress": 0.3}, false, true); got != "[accent:[███░░░░░░░] extracting]" {
		t.Fatalf("%q", got)
	}
	if got := render("fetch_content", map[string]any{"progress": 0.0}, false, true); got != "[accent:[░░░░░░░░░░] fetching]" {
		t.Fatalf("%q", got)
	}
	// The retrieval tool is not registered: the note changes.
	r2, _ := newRuntime(t, `{"tools":{"getSearchContent":{"enabled":false}}}`)
	n, err := r2.RenderResult("fetch_content", content, many, false, false, markTheme{})
	noErr(t, err)
	if got := renderedText(t, n)[0]; got != "[success:2/3 URLs][muted: (content fetched)]" {
		t.Fatalf("%q", got)
	}

	for _, c := range []struct {
		details map[string]any
		want    string
	}{
		{map[string]any{"matchCount": 4.0, "returnedMatches": 2.0, "title": "Doc"}, "[success:Doc][muted: (4 matches, 2 shown)]"},
		{map[string]any{"matchCount": 0.0, "query": "q"}, "[success:q][muted: (0 matches, 0 shown)]"},
		{map[string]any{"query": "q", "resultCount": 3.0}, `[success:"q"][muted: (3 results)]`},
		{map[string]any{"title": "Doc", "contentLength": 900.0, "offset": 100.0, "returnedChars": 300.0, "nextOffset": 400.0}, "[success:Doc][muted: (900 chars, showing 100-400)]"},
		{map[string]any{"contentLength": 50.0}, "[success:Content][muted: (50 chars)]"},
		{map[string]any{"contentLength": 50.0, "nextOffset": nil}, "[success:Content][muted: (50 chars, showing 0-50)]"},
	} {
		if got := render("get_search_content", c.details, false, false); got != c.want {
			t.Errorf("%v: %q, want %q", c.details, got, c.want)
		}
	}
	if got := render("get_search_content", map[string]any{"contentLength": 5.0}, true, false); !strings.HasSuffix(got, "\n[dim:"+text+"]") {
		t.Fatalf("%q", got)
	}
}
