package posthorse

// Unit tests of src/ui/display.ts and src/ui/text.ts (lane E): the shape of `details` as the tools emit it, its
// validation on the way back, and the UTF-16 slicing of history sections.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDisplayJSONKeyOrder(t *testing.T) {
	tokens, percent, at := 1000, 1.0, 91000
	usage := &contextUsage{tokens: &tokens, contextWindow: 100000, percent: &percent}
	for _, tc := range []struct {
		name    string
		display *posthorseDisplay
		want    string
	}{
		{"context", displayContext(usage, "enabled", &at), `{"kind":"context","usage":{"tokens":1000,"contextWindow":100000,"percent":1},"rollover":"enabled","rolloverAt":91000}`},
		{"context no rolloverAt", displayContext(usage, "unsupported", nil), `{"kind":"context","usage":{"tokens":1000,"contextWindow":100000,"percent":1},"rollover":"unsupported"}`},
		{"context unknown", displayKind("context"), `{"kind":"context"}`},
		{"new-context", displayKind("new-context"), `{"kind":"new-context"}`},
		{"note-write", displayKind("note-write"), `{"kind":"note-write"}`},
		{"notes-search", displayNotes("notes-search", 0, 2, 0, 34, 400, true), `{"kind":"notes-search","headerLength":0,"count":2,"page":{"offset":0,"end":34,"total":400},"more":true}`},
		{"notes-list", displayNotes("notes-list", 33, 3, 0, 100, 100, false), `{"kind":"notes-list","headerLength":33,"count":3,"page":{"offset":0,"end":100,"total":100}}`},
		{"note-read", displayNoteRead(10, 0, 5, 5), `{"kind":"note-read","headerLength":10,"offset":0,"end":5,"total":5}`},
		{"history-search", displayHistorySearch([]historySpan{{3, 9}}, 0, false, 0), `{"kind":"history-search","entries":[{"headerLength":3,"length":9}],"footerLength":0,"more":false,"skipped":0}`},
		{"history-read", displayHistoryRead(60, 0, 40, 9000, 0, 1, 2), `{"kind":"history-read","headerLength":60,"offset":0,"end":40,"total":9000,"imageOffset":0,"imageEnd":1,"imageTotal":2}`},
	} {
		got, err := json.Marshal(tc.display)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if string(got) != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
		// What the host stores comes back as decoded JSON: it must validate and re-encode unchanged.
		var decoded any
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatal(err)
		}
		back := displayOf(decoded)
		if back == nil {
			t.Errorf("%s: decoded display is rejected", tc.name)
			continue
		}
		again, _ := json.Marshal(back)
		if tc.name != "context unknown" && string(again) != string(got) {
			t.Errorf("%s: round trip %s", tc.name, again)
		}
	}
}

func TestDisplayOfRejectsMalformed(t *testing.T) {
	for name, value := range map[string]any{
		"nil":                nil,
		"string":             "context",
		"unknown kind":       map[string]any{"kind": "other"},
		"usage tokens text":  map[string]any{"kind": "context", "usage": map[string]any{"tokens": "x", "contextWindow": 1.0, "percent": nil}},
		"usage missing":      map[string]any{"kind": "context", "usage": map[string]any{"contextWindow": 1.0}},
		"rollover bad":       map[string]any{"kind": "context", "rollover": "maybe"},
		"rolloverAt null":    map[string]any{"kind": "context", "rolloverAt": nil},
		"notes count text":   map[string]any{"kind": "notes-list", "count": "3"},
		"notes more text":    map[string]any{"kind": "notes-list", "count": 1.0, "more": "yes"},
		"note-read no total": map[string]any{"kind": "note-read", "offset": 0.0, "end": 1.0},
		"history-read no hl": map[string]any{"kind": "history-read", "offset": 0.0, "end": 1.0, "total": 1.0},
		"search null entry":  map[string]any{"kind": "history-search", "entries": []any{nil}},
		"search no entries":  map[string]any{"kind": "history-search"},
		"search skipped nil": map[string]any{"kind": "history-search", "entries": []any{}, "skipped": nil},
	} {
		if displayOf(value) != nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHistorySections(t *testing.T) {
	raw := "H1 body 界1\nH2 body 👩🏽‍💻2\nfooter"
	h1, h2 := u16len("H1 "), u16len("H2 ")
	b1, b2 := u16len("body 界1"), u16len("body 👩🏽‍💻2")
	display := displayHistorySearch([]historySpan{{float64(h1), float64(h1 + b1)}, {float64(h2), float64(h2 + b2)}}, u16len("\nfooter"), true, 0)
	got := historySections(raw, display)
	want := []historySection{{header: "H1", body: "body 界1"}, {header: "H2", body: "body 👩🏽‍💻2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sections %q want %q", got, want)
	}
	// A span that does not tile the text is rejected whole.
	bad := displayHistorySearch([]historySpan{{float64(h1), float64(h1 + b1 + 1)}}, 0, false, 0)
	if historySections(raw, bad) != nil {
		t.Fatal("a span that does not cover the body is accepted")
	}
	if historySections(raw, displayHistorySearch(nil, 0, false, 0)) != nil {
		t.Fatal("no spans, no sections")
	}
	// history-read has one span over the whole text.
	read := displayHistoryRead(3, 0, 5, 5, 0, 0, 0)
	if s := historySections("H1 body", read); len(s) != 1 || s[0].header != "H1" || s[0].body != "body" {
		t.Fatalf("history-read sections %v", s)
	}
}

func TestLocaleNumber(t *testing.T) {
	for in, want := range map[float64]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 56186: "56,186", 1234567: "1,234,567", -1500: "-1,500", 1234.5678: "1,234.568", 0.5: "0.5"} {
		if got := n(in); got != want {
			t.Errorf("n(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanText(t *testing.T) {
	for in, want := range map[string]string{
		"plain\ttab\nline":                 "plain\ttab\nline",
		"a\x00b\rc\x7fd\u0085e":            "abcde",
		"x\x1b[2Jy\x1b]52;c;c2VjcmV0\x07z": "xyz",
		"a\ufff9b\ufffbc":                  "abc",
		"é 界 👩🏽‍💻":                         "é 界 👩🏽‍💻",
	} {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripHistoryPrefixes(t *testing.T) {
	in := "archive.jsonl 2026 [window old] [owner-1] [user] first\nsecond [window a] [b] [chars 0-5 of 9] text\r[window x] [y] after cr\nplain"
	want := "[user] first\ntext\rafter cr\nplain"
	if got := cardStripHistoryPrefixes(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := cardStripHistoryPrefixes("no header here"); got != "no header here" {
		t.Fatal(got)
	}
	if strings.Contains(cardStripHistoryPrefixes("[window w] [id] "), "window") {
		t.Fatal("a header at the start of the text is removed")
	}
}
