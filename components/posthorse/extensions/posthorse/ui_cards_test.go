package posthorse

// Exact rows of the cards (cards.ts, display.ts, ui.ts): wording, glyphs, row caps, hints, and the collapsed and expanded
// forms for every display kind and state. The rows are the original's (ui_differential_test.go agrees with them over 656
// cases) at 40 columns, with the zero UITheme (no palette) and terminal sequences stripped.

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

func uiPlain(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(tui.StripTerminalSequences(r), " ")
	}
	return out
}

func uiSame(t *testing.T, name string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", name, got, want)
	}
}

type uiResultCase struct {
	name string
	f    *cardResultFacts
}

type uiFlagCase struct {
	name string
	mod  func(*cardResultFacts)
	base string
}

func uiResultCases() ([]uiResultCase, []uiFlagCase) {
	tok := 1000
	pct := 1.0
	at := 91000
	usage := &contextUsage{tokens: &tok, contextWindow: 100000, percent: &pct}
	long := strings.Repeat("word ", 40)
	mk := func(name string, raw string, args map[string]any, d *posthorseDisplay, exp bool) *cardResultFacts {
		return &cardResultFacts{name: name, raw: raw, args: args, display: d, expanded: exp, expandKey: "ctrl+o", showImages: true}
	}
	cases := []uiResultCase{
		{"note-read", mk("notes", "File: /x\nhello", map[string]any{"op": "read"}, displayNoteRead(9, 0, 5, 5), false)},
		{"note-read-page", mk("notes", "File: /x\nhello\n[chars 0-5 of 9; continue with offset 5]", map[string]any{"op": "read"}, displayNoteRead(9, 0, 5, 9), false)},
		{"note-read-last", mk("notes", "File: /x\nllo", map[string]any{"op": "read"}, displayNoteRead(9, 6, 9, 9), false)},
		{"note-empty", mk("notes", "", map[string]any{"op": "read"}, displayNoteRead(0, 0, 0, 0), false)},
		{"notes-list", mk("notes", "Notes directory: /w\na.md  1B  t\nb.md  2B  t", map[string]any{"op": "list"}, displayNotes("notes-list", 20, 2, 0, 50, 50, false), false)},
		{"notes-list-one", mk("notes", "Notes directory: /w\na.md  1B  t", map[string]any{"op": "list"}, displayNotes("notes-list", 20, 1, 0, 50, 50, false), false)},
		{"notes-list-none", mk("notes", "(no notes yet)", map[string]any{"op": "list"}, displayNotes("notes-list", 0, 0, 0, 14, 14, false), false)},
		{"notes-search-more", mk("notes", "a:1: n\nb:2: n", map[string]any{"op": "search"}, displayNotes("notes-search", 0, 2, 0, 12, 400, true), false)},
		{"notes-search-page", mk("notes", "a:1: n\nb:2: n", map[string]any{"op": "search"}, displayNotes("notes-search", 0, 2, 0, 12, 40, false), false)},
		{"notes-search-none", mk("notes", "No notes match", map[string]any{"op": "search"}, displayNotes("notes-search", 0, 0, 0, 14, 14, false), false)},
		{"write", mk("notes", "Wrote p", map[string]any{"op": "write", "content": "hi"}, displayKind("note-write"), false)},
		{"write-clear", mk("notes", "Wrote p", map[string]any{"op": "write", "content": ""}, displayKind("note-write"), false)},
		{"append", mk("notes", "Appended to p", map[string]any{"op": "append", "content": "more"}, displayKind("note-append"), false)},
		{"new-context", mk("new_context", "Requested", map[string]any{"handoff": "h"}, displayKind("new-context"), false)},
		{"ctx", mk("get_context_remaining", "t", map[string]any{}, displayContext(usage, "enabled", &at), false)},
		{"ctx-disabled", mk("get_context_remaining", "t", map[string]any{}, displayContext(usage, "disabled", nil), false)},
		{"ctx-nopercent", mk("get_context_remaining", "t", map[string]any{}, displayContext(&contextUsage{tokens: &tok, contextWindow: 100000}, "disabled", nil), false)},
		{"ctx-norollover", mk("get_context_remaining", "t", map[string]any{}, displayContext(usage, "", nil), false)},
		{"ctx-unknown", mk("get_context_remaining", "t", map[string]any{}, displayKind("context"), false)},
		{"hist-search", mk("history", "H1 body one\nH2 body two\nH3 body three\nH4 body four\nfoot", map[string]any{"op": "search"}, displayHistorySearch([]historySpan{{3, 11}, {3, 11}, {3, 13}, {3, 12}}, 5, false, 0), false)},
		{"hist-search-more", mk("history", "H1 body one\nfoot", map[string]any{"op": "search", "all": true}, displayHistorySearch([]historySpan{{3, 11}}, 5, true, 3), false)},
		{"hist-search-more-default", mk("history", "H1 body one\nfoot", map[string]any{"op": "search"}, &posthorseDisplay{kind: "history-search", entries: []historySpan{{3, 11}}, footerLength: fl(5)}, false)},
		{"hist-search-none", mk("history", "No history matches", map[string]any{"op": "search"}, displayHistorySearch(nil, 0, false, 0), false)},
		{"hist-search-none-all", mk("history", "No history matches", map[string]any{"op": "search", "all": true}, displayHistorySearch(nil, 0, false, 0), false)},
		{"hist-search-bad", mk("history", "x", map[string]any{"op": "search"}, displayHistorySearch([]historySpan{{50, 60}}, 0, false, 0), false)},
		{"hist-read", mk("history", "H body text", map[string]any{"op": "read"}, displayHistoryRead(2, 0, 9, 9, 0, 0, 0), false)},
		{"hist-read-img", mk("history", "H body text", map[string]any{"op": "read"}, displayHistoryRead(2, 0, 4, 9, 0, 1, 2), false)},
		{"hist-read-img-done", mk("history", "H body text", map[string]any{"op": "read"}, displayHistoryRead(2, 0, 9, 9, 1, 2, 2), false)},
		{"hist-read-img-more-offset", mk("history", "H body text", map[string]any{"op": "read"}, displayHistoryRead(2, 0, 9, 9, 0, 1, 2), false)},
		{"legacy-hist", mk("history", "arch 2026 [window old] [o1] [user] first\nsecond line", map[string]any{"op": "search"}, nil, false)},
		{"plain-long", mk("notes", strings.Repeat("line "+long+"\n", 6), map[string]any{"op": "read"}, nil, false)},
		{"plain-short", mk("notes", "one\ntwo", map[string]any{"op": "read"}, nil, false)},
		{"plain-four-lines", mk("notes", "1\n2\n3\n4", map[string]any{"op": "read"}, nil, false)},
		{"empty-plain", mk("history", "  ", map[string]any{"op": "read"}, nil, false)},
	}
	flags := []uiFlagCase{
		{"error-empty", func(f *cardResultFacts) { f.isError = true; f.raw = "" }, "write"},
		{"error-text", func(f *cardResultFacts) { f.isError = true; f.raw = "boom" }, "write"},
		{"partial-empty", func(f *cardResultFacts) { f.isPartial = true; f.raw = "" }, "write"},
		{"partial-text", func(f *cardResultFacts) { f.isPartial = true; f.raw = "so far" }, "write"},
		{"images-2", func(f *cardResultFacts) { f.imageCount = 2 }, "note-read"},
		{"images-1-hidden", func(f *cardResultFacts) { f.imageCount = 1; f.showImages = false }, "note-read"},
		{"images-3-nodisplay", func(f *cardResultFacts) { f.imageCount = 3; f.display = nil }, "plain-short"},
		{"nokey", func(f *cardResultFacts) { f.expandKey = "" }, "plain-four-lines"},
	}
	return cases, flags
}

var uiResultWant = map[string][2][]string{
	"note-read":                 {[]string{"5 chars · complete", "hello", "… ctrl+o to expand"}, []string{"File: /x", "hello"}},
	"note-read-page":            {[]string{"Chars 0–5 of 9", "Next offset 5", "hello", "[chars 0-5 of 9; continue with offset 5]", "… ctrl+o to expand"}, []string{"File: /x", "hello", "[chars 0-5 of 9; continue with offset 5]"}},
	"note-read-last":            {[]string{"Chars 6–9 of 9 · final page", "llo", "… ctrl+o to expand"}, []string{"File: /x", "llo"}},
	"note-empty":                {[]string{"Empty note", "… ctrl+o to expand"}, []string{"Empty note"}},
	"notes-list":                {[]string{"2 notes returned", "a.md  1B  t", "b.md  2B  t", "… ctrl+o to expand"}, []string{"Notes directory: /w", "a.md  1B  t", "b.md  2B  t"}},
	"notes-list-one":            {[]string{"1 note returned", "a.md  1B  t", "… ctrl+o to expand"}, []string{"Notes directory: /w", "a.md  1B  t"}},
	"notes-list-none":           {[]string{"No notes yet", "… ctrl+o to expand"}, []string{"(no notes yet)"}},
	"notes-search-more":         {[]string{"2 matches returned", "Chars 0–12 of 400+", "Next offset 12", "a:1: n", "b:2: n"}, []string{"a:1: n", "b:2: n"}},
	"notes-search-page":         {[]string{"2 matches returned", "Chars 0–12 of 40", "Next offset 12", "a:1: n", "b:2: n"}, []string{"a:1: n", "b:2: n"}},
	"notes-search-none":         {[]string{"No matches", "… ctrl+o to expand"}, []string{"No notes match"}},
	"write":                     {[]string{"Saved note", "… ctrl+o to expand"}, []string{"Wrote p", "", "Submitted content:", "hi"}},
	"write-clear":               {[]string{"Cleared note", "… ctrl+o to expand"}, []string{"Wrote p", "", "Submitted content:", "(empty)"}},
	"append":                    {[]string{"Appended to note", "… ctrl+o to expand"}, []string{"Appended to p", "", "Submitted content:", "more"}},
	"new-context":               {[]string{"Requested after foreground tools", "succeed.", "Background work continues.", "… ctrl+o to expand"}, []string{"Requested", "", "Handoff supplied:", "h"}},
	"ctx":                       {[]string{"≈90,000 tokens to rollover", "≈99,000 tokens to configured context", "limit", "≈1,000 / 100,000 used (≈1%)", "… ctrl+o to expand"}, []string{"t"}},
	"ctx-disabled":              {[]string{"Automatic rollover disabled", "≈99,000 tokens to configured context", "limit", "≈1,000 / 100,000 used (≈1%)", "… ctrl+o to expand"}, []string{"t"}},
	"ctx-nopercent":             {[]string{"Automatic rollover disabled", "≈99,000 tokens to configured context", "limit", "≈1,000 / 100,000 used", "… ctrl+o to expand"}, []string{"t"}},
	"ctx-norollover":            {[]string{"Automatic rollover unknown", "≈99,000 tokens to configured context", "limit", "≈1,000 / 100,000 used (≈1%)", "… ctrl+o to expand"}, []string{"t"}},
	"ctx-unknown":               {[]string{"Context usage unknown", "… ctrl+o to expand"}, []string{"t"}},
	"hist-search":               {[]string{"4 matches · current branch", "body one", "body two", "body three", "… ctrl+o to expand"}, []string{"body one", "H1", "", "body two", "H2", "", "body three", "H3", "", "body four", "H4", "", "foot"}},
	"hist-search-more":          {[]string{"1 matches · all sessions · 3 in context", "skipped", "More results; continue with cursor", "body one", "… ctrl+o to expand"}, []string{"body one", "H1", "", "foot"}},
	"hist-search-more-default":  {[]string{"1 matches · current branch", "More results; continue with cursor", "body one", "… ctrl+o to expand"}, []string{"body one", "H1", "", "foot"}},
	"hist-search-none":          {[]string{"No matches · current branch", "… ctrl+o to expand"}, []string{"No history matches"}},
	"hist-search-none-all":      {[]string{"No matches · all sessions", "… ctrl+o to expand"}, []string{"No history matches"}},
	"hist-search-bad":           {[]string{"x"}, []string{"x"}},
	"hist-read":                 {[]string{"9 chars · complete", "body text", "… ctrl+o to expand"}, []string{"body text", "H"}},
	"hist-read-img":             {[]string{"Next offset 4", "imageOffset 1", "Chars 0–4 of 9", "Images 0–1 of 2", "body text", "… ctrl+o to expand"}, []string{"body text", "H"}},
	"hist-read-img-done":        {[]string{"9 chars · complete", "Images 1–2 of 2", "body text", "… ctrl+o to expand"}, []string{"body text", "H"}},
	"hist-read-img-more-offset": {[]string{"Next offset 9", "imageOffset 1", "9 chars · complete", "Images 0–1 of 2", "body text", "… ctrl+o to expand"}, []string{"body text", "H"}},
	"legacy-hist":               {[]string{"[user] first", "second line"}, []string{"arch 2026 [window old] [o1] [user] first", "second line"}},
	"plain-long":                {[]string{"line word word word word word word word", "word word word word word word word word", "word word word word word word word word", "… ctrl+o to expand"}, []string{"line word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word", "line word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word", "line word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word", "line word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word", "line word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word", "line word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word word word word word word word word", "word", ""}},
	"plain-short":               {[]string{"one", "two"}, []string{"one", "two"}},
	"plain-four-lines":          {[]string{"1", "2", "3", "… ctrl+o to expand"}, []string{"1", "2", "3", "4"}},
	"empty-plain":               {[]string{"No text returned."}, []string{"No text returned."}},
	"error-empty":               {[]string{"No error details returned.", "… ctrl+o to expand"}, []string{"No error details returned.", "", "Submitted content:", "hi"}},
	"error-text":                {[]string{"boom", "… ctrl+o to expand"}, []string{"boom", "", "Submitted content:", "hi"}},
	"partial-empty":             {[]string{"Running…", "… ctrl+o to expand"}, []string{"Running…", "", "Submitted content:", "hi"}},
	"partial-text":              {[]string{"so far", "… ctrl+o to expand"}, []string{"so far", "", "Submitted content:", "hi"}},
	"images-2":                  {[]string{"5 chars · complete", "hello", "2 images attached", "… ctrl+o to expand"}, []string{"File: /x", "hello", "2 images attached"}},
	"images-1-hidden":           {[]string{"5 chars · complete", "hello", "1 image attached (display hidden)", "… ctrl+o to expand"}, []string{"File: /x", "hello", "1 image attached (display hidden)"}},
	"images-3-nodisplay":        {[]string{"one", "two", "3 images attached"}, []string{"one", "two", "3 images attached"}},
	"nokey":                     {[]string{"1", "2", "3", "… details hidden"}, []string{"1", "2", "3", "4"}},
}

func TestUiResultCards(t *testing.T) {
	th := sdk.UITheme{}
	cases, flags := uiResultCases()
	check := func(name string, f *cardResultFacts) {
		t.Helper()
		want, ok := uiResultWant[name]
		if !ok {
			t.Fatalf("no expected rows for %s", name)
		}
		collapsed := *f
		collapsed.expanded = false
		expanded := *f
		expanded.expanded = true
		uiSame(t, name+" collapsed", uiPlain(cardResultComponent(&collapsed, th, 40)), want[0])
		uiSame(t, name+" expanded", uiPlain(cardResultComponent(&expanded, th, 40)), want[1])
	}
	for _, c := range cases {
		check(c.name, c.f)
	}
	for _, fc := range flags {
		var base *cardResultFacts
		for _, c := range cases {
			if c.name == fc.base {
				copied := *c.f
				base = &copied
			}
		}
		fc.mod(base)
		check(fc.name, base)
	}
}
