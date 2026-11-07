package posthorse

// display.ts pinned: summaries, the locale number format, history sections and the validation of foreign details.

import (
	"strings"
	"testing"
)

func TestUiLocaleNumberFormat(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		want string
	}{
		{0, "0"}, {1, "1"}, {12, "12"}, {123, "123"}, {1234, "1,234"}, {12345, "12,345"}, {123456, "123,456"}, {1234567, "1,234,567"}, {100000, "100,000"}, {1000000000, "1,000,000,000"},
		{-5, "-5"}, {-1234.5, "-1,234.5"},
		{0.5, "0.5"}, {12.3, "12.3"}, {1.25, "1.25"}, {1.2345, "1.235"}, {1.2344, "1.234"}, {2.0005, "2.001"}, {0.0004, "0"}, {0.0005, "0.001"},
		{9.9996, "10"}, {999.9995, "1,000"}, {99999.9996, "100,000"}, {1.1, "1.1"}, {3.10, "3.1"}, {0.1 + 0.2, "0.3"},
	} {
		if got := n(tc.v); got != tc.want {
			t.Errorf("n(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
	neg0 := -1.0 * 0
	if got := n(neg0 * -1 * -1); got != "0" {
		// +0 stays "0"
		t.Errorf("n(0) = %q", got)
	}
	if got := n(-0.0004); got != "-0" {
		t.Errorf("a negative value that rounds to zero keeps its sign: %q", got)
	}
}

func TestUiPageSummary(t *testing.T) {
	for _, tc := range []struct {
		page pageRange
		more bool
		want string
	}{
		{pageRange{0, 0, 0}, false, "Empty note"},
		{pageRange{0, 5, 5}, false, "5 chars · complete"},
		{pageRange{0, 1234, 1234}, true, "1,234 chars · complete"},
		{pageRange{0, 5, 9}, false, "Chars 0–5 of 9\nNext offset 5"},
		{pageRange{0, 5, 9}, true, "Chars 0–5 of 9+\nNext offset 5"},
		{pageRange{5, 9, 9}, false, "Chars 5–9 of 9 · final page"},
		{pageRange{5, 9, 9}, true, "Chars 5–9 of 9+ · final page"},
		{pageRange{0, 9, 9000}, false, "Chars 0–9 of 9,000\nNext offset 9"},
		{pageRange{1000, 2000, 5000}, false, "Chars 1,000–2,000 of 5,000\nNext offset 2,000"},
		{pageRange{3, 5, 5}, false, "Chars 3–5 of 5 · final page"},
		{pageRange{0, 5, 5}, true, "5 chars · complete"},
	} {
		if got := pageSummary(tc.page, tc.more); got != tc.want {
			t.Errorf("pageSummary(%v, %v) = %q, want %q", tc.page, tc.more, got, tc.want)
		}
	}
}

func TestUiContextSummary(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name string
		d    *posthorseDisplay
		want string
	}{
		{"enabled", &posthorseDisplay{kind: "context", usage: true, usageTokens: f(1000), usageWindow: 100000, usagePercent: f(1), rollover: "enabled", rolloverAt: f(91000)},
			"≈90,000 tokens to rollover\n≈99,000 tokens to configured context limit\n≈1,000 / 100,000 used (≈1%)"},
		{"past the line", &posthorseDisplay{kind: "context", usage: true, usageTokens: f(95000), usageWindow: 100000, usagePercent: f(95), rollover: "enabled", rolloverAt: f(91000)},
			"≈0 tokens to rollover\n≈5,000 tokens to configured context limit\n≈95,000 / 100,000 used (≈95%)"},
		{"over the window", &posthorseDisplay{kind: "context", usage: true, usageTokens: f(120000), usageWindow: 100000, usagePercent: f(120), rollover: "disabled"},
			"Automatic rollover disabled\n≈0 tokens to configured context limit\n≈120,000 / 100,000 used (≈120%)"},
		{"enabled without a line", &posthorseDisplay{kind: "context", usage: true, usageTokens: f(1000), usageWindow: 100000, usagePercent: f(1), rollover: "enabled"},
			"Automatic rollover enabled\n≈99,000 tokens to configured context limit\n≈1,000 / 100,000 used (≈1%)"},
		{"unsupported", &posthorseDisplay{kind: "context", usage: true, usageTokens: f(1192), usageWindow: 8192, usagePercent: f(14.55), rollover: "unsupported"},
			"Automatic rollover unsupported\n≈7,000 tokens to configured context limit\n≈1,192 / 8,192 used (≈15%)"},
		{"no percent", &posthorseDisplay{kind: "context", usage: true, usageTokens: f(1000), usageWindow: 100000, rollover: "disabled"},
			"Automatic rollover disabled\n≈99,000 tokens to configured context limit\n≈1,000 / 100,000 used"},
		{"no rollover", &posthorseDisplay{kind: "context", usage: true, usageTokens: f(1000), usageWindow: 100000, usagePercent: f(1)},
			"Automatic rollover unknown\n≈99,000 tokens to configured context limit\n≈1,000 / 100,000 used (≈1%)"},
		{"percent rounds", &posthorseDisplay{kind: "context", usage: true, usageTokens: f(1), usageWindow: 3, usagePercent: f(12.5), rollover: "disabled"},
			"Automatic rollover disabled\n≈2 tokens to configured context limit\n≈1 / 3 used (≈13%)"},
		{"unknown tokens", &posthorseDisplay{kind: "context", usage: true, usageWindow: 100000, rollover: "enabled", rolloverAt: f(5)}, "Context usage unknown"},
		{"no usage", &posthorseDisplay{kind: "context", rollover: "enabled"}, "Context usage unknown"},
	} {
		if got := contextSummary(tc.d); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestUiHistorySummaries(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	b := func(v bool) *bool { return &v }
	span := []historySpan{{3, 11}}
	for _, tc := range []struct {
		name  string
		d     *posthorseDisplay
		all   bool
		valid bool
		want  string
	}{
		{"one branch", &posthorseDisplay{kind: "history-search", entries: span}, false, true, "1 matches · current branch"},
		{"all", &posthorseDisplay{kind: "history-search", entries: span}, true, true, "1 matches · all sessions"},
		{"many", &posthorseDisplay{kind: "history-search", entries: make([]historySpan, 1234)}, false, true, "1,234 matches · current branch"},
		{"none", &posthorseDisplay{kind: "history-search"}, false, true, "No matches · current branch"},
		{"none all", &posthorseDisplay{kind: "history-search"}, true, true, "No matches · all sessions"},
		{"skipped", &posthorseDisplay{kind: "history-search", entries: span, skipped: f(3)}, false, true, "1 matches · current branch · 3 in context skipped"},
		{"skipped zero", &posthorseDisplay{kind: "history-search", entries: span, skipped: f(0)}, false, true, "1 matches · current branch"},
		{"more", &posthorseDisplay{kind: "history-search", entries: span, more: b(true)}, false, true, "1 matches · current branch\nMore results; continue with cursor"},
		{"not more", &posthorseDisplay{kind: "history-search", entries: span, more: b(false), footerLength: f(5)}, false, true, "1 matches · current branch"},
		{"more from footer", &posthorseDisplay{kind: "history-search", entries: span, footerLength: f(5)}, false, true, "1 matches · current branch\nMore results; continue with cursor"},
		{"no footer", &posthorseDisplay{kind: "history-search", entries: span, footerLength: f(0)}, false, true, "1 matches · current branch"},
		{"invalid sections", &posthorseDisplay{kind: "history-search", entries: span}, false, false, ""},
		{"none invalid sections", &posthorseDisplay{kind: "history-search"}, false, false, "No matches · current branch"},
	} {
		if got := historySearchSummary(tc.d, tc.all, tc.valid); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	read := func(end, total, imgOff, imgEnd, imgTotal float64) *posthorseDisplay {
		return &posthorseDisplay{kind: "history-read", pageRange: pageRange{0, end, total}, headerLength: f(2), imageOffset: f(imgOff), imageEnd: f(imgEnd), imageTotal: f(imgTotal)}
	}
	for _, tc := range []struct {
		name string
		d    *posthorseDisplay
		want string
	}{
		{"no images", read(9, 9, 0, 0, 0), "9 chars · complete"},
		{"no images paged", read(4, 9, 0, 0, 0), "Chars 0–4 of 9\nNext offset 4"},
		{"images", read(9, 9, 0, 1, 1), "9 chars · complete\nImages 0–1 of 1"},
		{"images more", read(4, 9, 0, 1, 2), "Next offset 4\nimageOffset 1\nChars 0–4 of 9\nImages 0–1 of 2"},
		{"images only more", read(9, 9, 0, 1, 2), "Next offset 9\nimageOffset 1\n9 chars · complete\nImages 0–1 of 2"},
		{"text only more", read(4, 9, 1, 2, 2), "Next offset 4\nimageOffset 2\nChars 0–4 of 9\nImages 1–2 of 2"},
		{"done", read(9, 9, 1, 2, 2), "9 chars · complete\nImages 1–2 of 2"},
		{"empty", read(0, 0, 0, 1, 1), "Empty note\nImages 0–1 of 1"},
		{"no image end", &posthorseDisplay{kind: "history-read", pageRange: pageRange{0, 9, 9}, headerLength: f(2), imageTotal: f(2)}, "9 chars · complete"},
		{"no image offset", &posthorseDisplay{kind: "history-read", pageRange: pageRange{0, 9, 9}, headerLength: f(2), imageEnd: f(1), imageTotal: f(1)}, "9 chars · complete\nImages 0–1 of 1"},
	} {
		if got := historyReadSummary(tc.d); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUiDisplaySummaryAndSummaryOnly(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name string
		d    *posthorseDisplay
		args map[string]any
		want string
		only bool
	}{
		{"nil", nil, nil, "", false},
		{"notes-list many", displayNotes("notes-list", 0, 3, 0, 5, 5, false), nil, "3 notes returned", false},
		{"notes-list one", displayNotes("notes-list", 0, 1, 0, 5, 5, false), nil, "1 note returned", false},
		{"notes-list thousand", displayNotes("notes-list", 0, 1000, 0, 5, 5, false), nil, "1,000 notes returned", false},
		{"notes-list none", displayNotes("notes-list", 0, 0, 0, 5, 5, false), nil, "No notes yet", true},
		{"notes-search", displayNotes("notes-search", 0, 2, 0, 5, 5, false), nil, "2 matches returned", false},
		{"notes-search one", displayNotes("notes-search", 0, 1, 0, 5, 5, false), nil, "1 matches returned", false},
		{"notes-search none", displayNotes("notes-search", 0, 0, 0, 5, 5, false), nil, "No matches", true},
		{"note-read", displayNoteRead(0, 0, 5, 5), nil, "5 chars · complete", false},
		{"note-read empty", displayNoteRead(0, 0, 0, 0), nil, "Empty note", true},
		{"note-write", displayKind("note-write"), map[string]any{"content": "x"}, "Saved note", true},
		{"note-write no args", displayKind("note-write"), map[string]any{}, "Saved note", true},
		{"note-write cleared", displayKind("note-write"), map[string]any{"content": ""}, "Cleared note", true},
		{"note-write nil content", displayKind("note-write"), map[string]any{"content": nil}, "Saved note", true},
		{"note-append", displayKind("note-append"), nil, "Appended to note", true},
		{"new-context", displayKind("new-context"), nil, "Requested after foreground tools succeed.\nBackground work continues.", true},
		{"history-search", displayHistorySearch([]historySpan{{1, 2}}, 0, false, 0), map[string]any{"all": true}, "1 matches · all sessions", false},
		{"history-search not bool", displayHistorySearch([]historySpan{{1, 2}}, 0, false, 0), map[string]any{"all": "true"}, "1 matches · current branch", false},
		{"history-search none", displayHistorySearch(nil, 0, false, 0), nil, "No matches · current branch", true},
		{"history-read", displayHistoryRead(2, 0, 9, 9, 0, 0, 0), nil, "9 chars · complete", false},
		{"context", displayKind("context"), nil, "Context usage unknown", true},
		{"context usage", &posthorseDisplay{kind: "context", usage: true, usageTokens: f(1), usageWindow: 2, rollover: "disabled"}, nil, "Automatic rollover disabled\n≈1 tokens to configured context limit\n≈1 / 2 used", true},
	} {
		if got := displaySummary(tc.d, tc.args, true); got != tc.want {
			t.Errorf("%s: summary %q, want %q", tc.name, got, tc.want)
		}
		if got := summaryOnly(tc.d); got != tc.only {
			t.Errorf("%s: summaryOnly %v, want %v", tc.name, got, tc.only)
		}
	}
}

func TestUiHistorySectionsExact(t *testing.T) {
	raw := "H1 body one\nH2 body two\nfoot"
	spans := []historySpan{{3, 11}, {3, 11}}
	got := historySections(raw, displayHistorySearch(spans, 5, false, 0))
	if len(got) != 2 || got[0].header != "H1" || got[0].body != "body one" || got[1].header != "H2" || got[1].body != "body two" {
		t.Fatalf("sections %q", got)
	}
	// the footer is not part of any section
	if historySections(raw, displayHistorySearch(spans, 0, false, 0)) != nil {
		t.Error("spans that stop before the footer are not tiling the text")
	}
	if historySections(raw, displayHistorySearch(spans, 6, false, 0)) != nil {
		t.Error("a footer that is too long leaves the spans short")
	}
	if historySections(raw, displayHistorySearch(spans, 100, false, 0)) != nil {
		t.Error("a footer longer than the text")
	}
	fl := func(v float64) *float64 { return &v }
	mk := func(entries []historySpan, footer *float64) *posthorseDisplay {
		return &posthorseDisplay{kind: "history-search", entries: entries, footerLength: footer}
	}
	for name, d := range map[string]*posthorseDisplay{
		"fractional footer":    mk(spans, fl(5.5)),
		"negative footer":      mk(spans, fl(-5)),
		"header past length":   mk([]historySpan{{12, 11}, {3, 11}}, fl(5)),
		"negative header":      mk([]historySpan{{-1, 11}, {3, 11}}, fl(5)),
		"fractional header":    mk([]historySpan{{2.5, 11}, {3, 11}}, fl(5)),
		"fractional length":    mk([]historySpan{{3, 11.5}, {3, 11}}, fl(5)),
		"length past the body": mk([]historySpan{{3, 12}, {3, 11}}, fl(5)),
		"second too long":      mk([]historySpan{{3, 11}, {3, 12}}, fl(5)),
		"infinite":             mk([]historySpan{{3, 1e400 * 0}}, fl(5)),
	} {
		if s := historySections(raw, d); s != nil && name != "infinite" {
			t.Errorf("%s: accepted %q", name, s)
		}
	}
	// header-only span: the header equals the whole part, the body is empty; the header loses its trailing blanks only
	one := historySections("H1  \nfoot", displayHistorySearch([]historySpan{{4, 4}}, 5, false, 0))
	if len(one) != 1 || one[0].header != "H1" || one[0].body != "" {
		t.Errorf("header-only %q", one)
	}
	keep := historySections("  H1\nfoot", displayHistorySearch([]historySpan{{4, 4}}, 5, false, 0))
	if len(keep) != 1 || keep[0].header != "  H1" {
		t.Errorf("leading blanks of a header stay: %q", keep)
	}
	// history-read: one span over the whole text, no footer
	read := historySections("H1 body", displayHistoryRead(3, 0, 4, 4, 0, 0, 0))
	if len(read) != 1 || read[0].header != "H1" || read[0].body != "body" {
		t.Errorf("history-read %q", read)
	}
	if historySections("H1 body", displayHistoryRead(20, 0, 4, 4, 0, 0, 0)) != nil {
		t.Error("a header longer than the text")
	}
	if historySections("x", displayKind("note-write")) != nil || historySections("x", nil) != nil {
		t.Error("other kinds have no sections")
	}
	// UTF-16 spans
	wide := historySections("H 😀 b\nf", displayHistorySearch([]historySpan{{2, 6}}, 2, false, 0))
	if len(wide) != 1 || wide[0].header != "H" || wide[0].body != "😀 b" {
		t.Errorf("astral section %q", wide)
	}
}

func TestUiDisplayOfAccepts(t *testing.T) {
	for name, value := range map[string]map[string]any{
		"context minimal":     {"kind": "context"},
		"context unsupported": {"kind": "context", "rollover": "unsupported"},
		"context null usage":  {"kind": "context", "usage": map[string]any{"tokens": nil, "contextWindow": 1.0, "percent": nil}},
		"context rolloverAt":  {"kind": "context", "rolloverAt": 5.0},
		"notes minimal":       {"kind": "notes-list", "count": 0.0},
		"notes search":        {"kind": "notes-search", "count": 1.0, "headerLength": 2.0, "page": map[string]any{"offset": 0.0, "end": 1.0, "total": 1.0}, "more": true},
		"note-read minimal":   {"kind": "note-read", "offset": 0.0, "end": 1.0, "total": 1.0},
		"history-read":        {"kind": "history-read", "headerLength": 1.0, "offset": 0.0, "end": 1.0, "total": 1.0, "imageOffset": 0.0, "imageEnd": 1.0, "imageTotal": 1.0},
		"history-search":      {"kind": "history-search", "entries": []any{}},
		"history-search full": {"kind": "history-search", "entries": []any{map[string]any{"headerLength": 1.0, "length": 2.0}}, "footerLength": 1.0, "more": false, "skipped": 1.0},
		"note-write":          {"kind": "note-write"},
		"note-append":         {"kind": "note-append"},
		"new-context":         {"kind": "new-context"},
	} {
		if displayOf(value) == nil {
			t.Errorf("%s: rejected", name)
		}
	}
	for name, value := range map[string]map[string]any{
		"context usage no tokens":    {"kind": "context", "usage": map[string]any{"contextWindow": 1.0, "percent": nil}},
		"context usage no percent":   {"kind": "context", "usage": map[string]any{"tokens": 1.0, "contextWindow": 1.0}},
		"context usage no window":    {"kind": "context", "usage": map[string]any{"tokens": 1.0, "percent": nil}},
		"context usage percent text": {"kind": "context", "usage": map[string]any{"tokens": 1.0, "contextWindow": 1.0, "percent": "1"}},
		"context rollover":           {"kind": "context", "rollover": "later"},
		"context rolloverAt text":    {"kind": "context", "rolloverAt": "5"},
		"notes no count":             {"kind": "notes-list"},
		"notes headerLength text":    {"kind": "notes-list", "count": 1.0, "headerLength": "2"},
		"notes page invalid":         {"kind": "notes-list", "count": 1.0, "page": map[string]any{"offset": 0.0}},
		"notes more text":            {"kind": "notes-search", "count": 1.0, "more": "yes"},
		"note-read no offset":        {"kind": "note-read", "end": 1.0, "total": 1.0},
		"note-read headerLength":     {"kind": "note-read", "offset": 0.0, "end": 1.0, "total": 1.0, "headerLength": "2"},
		"history-read no header":     {"kind": "history-read", "offset": 0.0, "end": 1.0, "total": 1.0},
		"history-read image text":    {"kind": "history-read", "headerLength": 1.0, "offset": 0.0, "end": 1.0, "total": 1.0, "imageEnd": "1"},
		"history-read page invalid":  {"kind": "history-read", "headerLength": 1.0, "offset": 0.0, "end": 1.0},
		"search no entries":          {"kind": "history-search"},
		"search entry null":          {"kind": "history-search", "entries": []any{nil}},
		"search entry no length":     {"kind": "history-search", "entries": []any{map[string]any{"headerLength": 1.0}}},
		"search footer text":         {"kind": "history-search", "entries": []any{}, "footerLength": "1"},
		"search skipped null":        {"kind": "history-search", "entries": []any{}, "skipped": nil},
		"search more text":           {"kind": "history-search", "entries": []any{}, "more": 1.0},
	} {
		if displayOf(value) != nil {
			t.Errorf("%s: accepted", name)
		}
	}
	d := displayOf(map[string]any{"kind": "notes-search", "count": 3.0, "headerLength": 2.0, "page": map[string]any{"offset": 1.0, "end": 2.0, "total": 3.0}, "more": true})
	if d.kind != "notes-search" || d.count != 3 || *d.headerLength != 2 || d.page.offset != 1 || d.page.end != 2 || d.page.total != 3 || !*d.more {
		t.Errorf("decoded %+v", d)
	}
	r := displayOf(map[string]any{"kind": "history-read", "headerLength": 4.0, "offset": 1.0, "end": 2.0, "total": 3.0, "imageOffset": 5.0, "imageEnd": 6.0, "imageTotal": 7.0})
	if *r.headerLength != 4 || r.offset != 1 || r.end != 2 || r.total != 3 || *r.imageOffset != 5 || *r.imageEnd != 6 || *r.imageTotal != 7 {
		t.Errorf("decoded read %+v", r)
	}
	c := displayOf(map[string]any{"kind": "context", "usage": map[string]any{"tokens": 7.0, "contextWindow": 9.0, "percent": 3.5}, "rollover": "enabled", "rolloverAt": 8.0})
	if !c.usage || *c.usageTokens != 7 || c.usageWindow != 9 || *c.usagePercent != 3.5 || c.rollover != "enabled" || *c.rolloverAt != 8 {
		t.Errorf("decoded context %+v", c)
	}
	s := displayOf(map[string]any{"kind": "history-search", "entries": []any{map[string]any{"headerLength": 1.0, "length": 2.0}}, "footerLength": 3.0, "more": true, "skipped": 4.0})
	if len(s.entries) != 1 || s.entries[0] != (historySpan{1, 2}) || *s.footerLength != 3 || !*s.more || *s.skipped != 4 {
		t.Errorf("decoded search %+v", s)
	}
}

func TestUiDisplayJSONMore(t *testing.T) {
	zero, win := 0, 100000
	for _, tc := range []struct {
		name string
		d    *posthorseDisplay
		want string
	}{
		{"context null usage", displayContext(&contextUsage{contextWindow: win}, "disabled", nil), `{"kind":"context","usage":{"tokens":null,"contextWindow":100000,"percent":null},"rollover":"disabled"}`},
		{"context zero", displayContext(&contextUsage{tokens: &zero, contextWindow: win}, "", nil), `{"kind":"context","usage":{"tokens":0,"contextWindow":100000,"percent":null}}`},
		{"notes-list no more", displayNotes("notes-list", 5, 0, 0, 14, 14, false), `{"kind":"notes-list","headerLength":5,"count":0,"page":{"offset":0,"end":14,"total":14}}`},
		{"history-search more", displayHistorySearch([]historySpan{{1, 2}, {3, 4}}, 7, true, 2), `{"kind":"history-search","entries":[{"headerLength":1,"length":2},{"headerLength":3,"length":4}],"footerLength":7,"more":true,"skipped":2}`},
		{"history-search empty", displayHistorySearch(nil, 0, false, 0), `{"kind":"history-search","entries":[],"footerLength":0,"more":false,"skipped":0}`},
		{"history-read", displayHistoryRead(1, 2, 3, 4, 5, 6, 7), `{"kind":"history-read","headerLength":1,"offset":2,"end":3,"total":4,"imageOffset":5,"imageEnd":6,"imageTotal":7}`},
		{"note-append", displayKind("note-append"), `{"kind":"note-append"}`},
	} {
		got, err := tc.d.MarshalJSON()
		if err != nil || string(got) != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
	if !strings.HasPrefix(string(mustJSON(t, displayNoteRead(1, 2, 3, 4))), `{"kind":"note-read","headerLength":1,"offset":2,"end":3,"total":4`) {
		t.Error("note-read")
	}
}

func mustJSON(t *testing.T, d *posthorseDisplay) []byte {
	t.Helper()
	b, err := d.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
