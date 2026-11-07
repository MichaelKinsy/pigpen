package posthorse

// Layer-1 unit tests of the history lane's pure pieces (prefilter, line matcher, JS lowercasing,
// session line normalization, cursors). The twins of the upstream cases are in
// twin_posthorse_history_test.go and twin_archive_test.go.

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func TestPrefilterNeedles(t *testing.T) {
	lone := "half \xed\xa0\xbd"
	cases := []struct {
		query string
		want  []string
		ok    bool
	}{
		{"needle", []string{"needle"}, true},
		{`say "needle" twice`, []string{"needle"}, true},
		{"no handoff", []string{"no handoff", `"type":"context_window"`}, true},
		{"unknown", []string{"unknown", `"type":"image"`, `"type":"custom_message"`}, true},
		{"unknown type", []string{"unknown type", `"type":"image"`}, true},
		{"1 image: image/png", []string{"image/png", "\u0130"}, true},
		{"3 images", []string{"3 images", `"type":"image"`, "\u0130"}, true},
		{"a,b", []string{"a,b", `"type":"image"`}, true},
		{"i\u0307stanbul", []string{"i\u0307stanbul", "\u0130"}, true},
		{"istanbul", []string{"istanbul", "\u0130"}, true},
		{lone, []string{"half"}, true},
		{"[]{}()", nil, false},
		{"", nil, false},
		{"ab\x00longer part\x7f", []string{"longer part"}, true},
		{"  tie1  :tie2", []string{"tie1", "\u0130"}, true},
	}
	for _, c := range cases {
		got, ok := prefilterNeedles(c.query)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("prefilterNeedles(%q) = %q, %v; want %q, %v", c.query, got, ok, c.want, c.ok)
		}
	}
}

func TestNeedleLinesMatcher(t *testing.T) {
	cases := []struct {
		needles []string
		text    string
		want    bool
		enc     string
	}{
		{[]string{"needle"}, `{"x":"a plain NEEDLE here"}`, true, "latin1"},
		{[]string{"needle"}, `{"x":"nothing"}`, false, "latin1"},
		{[]string{"kelvin"}, "\xe2\x84\xaaELVIN", true, "latin1"},
		{[]string{"kelvin"}, "kelvin", true, "latin1"},
		{[]string{"sail"}, "\xc5\xbfAIL", true, "latin1"},
		{[]string{"sail"}, "SAIL", true, "latin1"},
		{[]string{"sail"}, "\xc5\xbfAI", false, "latin1"},
		{[]string{"istanbul", "\u0130"}, "xx \xc4\xb0STANBUL", true, "latin1"},
		{[]string{"éclair 🐴"}, "ÉCLAIR 🐴", true, "utf8"},
		{[]string{"🐴 beacon"}, "🐴 BEACON", true, "utf8"},
		{[]string{"éclair 🐴"}, "eclair", false, "utf8"},
		{[]string{"stanbul", "\u0130"}, "\u0130STANBUL", true, "latin1"},
	}
	for _, c := range cases {
		m := needleLines(c.needles)
		if m.encoding != c.enc {
			t.Errorf("needleLines(%q).encoding = %s, want %s", c.needles, m.encoding, c.enc)
		}
		if got := m.test(c.text); got != c.want {
			t.Errorf("needleLines(%q).test(%q) = %v, want %v", c.needles, c.text, got, c.want)
		}
	}
	if got := needleLines([]string{"ab", "abcdef"}).overlap; got != 4*6+4 {
		t.Errorf("overlap = %d", got)
	}
}

func TestFoldIndexMatchesNaive(t *testing.T) {
	texts := []string{"", "a", "Hello World", strings.Repeat("xyzzy ", 200) + "NeEdLe tail", "NEEDLE", "needl"}
	for _, text := range texts {
		for _, p := range []string{"needle", "n", "zz", "lo wo", "xyzzy xyzzy"} {
			want := strings.Index(strings.ToLower(text), p)
			if got := foldIndexString(text, p); got != want {
				t.Errorf("foldIndexString(%.20q, %q) = %d, want %d", text, p, got, want)
			}
			if got := newFoldPattern(p).index(text); got != want {
				t.Errorf("foldPattern(%q).index(%.20q) = %d, want %d", p, text, got, want)
			}
		}
	}
}

// TestHistoryIndexLowercasing: the excerpt index of a hit is measured in the lowercased text (İ is two
// units there), with the full lowercase mapping of js.go.
func TestHistoryIndexLowercasing(t *testing.T) {
	cases := []struct {
		text, query string
		want        int
	}{
		{"İSTANBUL", "i\u0307stanbul", 0},
		{"xİx", "x", 0},
		{"İx", "x", 2},
		{"ΑΣ\u0387Β", "ασ\u0387β", 0},
		{"ÉCLAIR 🐴", "🐴", 7},
		{"plain TEXT", "text", 6},
		{"plain TEXT", "absent", -1},
	}
	for _, c := range cases {
		if got := historyIndex(c.text, c.query); got != c.want {
			t.Errorf("historyIndex(%q, %q) = %d, want %d", c.text, c.query, got, c.want)
		}
	}
}

func TestParseEntryHead(t *testing.T) {
	cases := []struct {
		in   string
		want entryHead
		ok   bool
	}{
		{`{"type":"message","id":"abc","parentId":null,"x":1}`, entryHead{"abc", ""}, true},
		{`{"type":"message","id":"abc","parentId":"par"}`, entryHead{"abc", "par"}, true},
		{`{"type":"message","id":"","parentId":null,`, entryHead{"", ""}, true},
		{`{"type":"compaction","id":"c","parentId":null,`, entryHead{}, false},
		{`{"type":"context_window","id":"c","parentId":null,`, entryHead{}, false},
		{`{"type":"message","id":"a\"b","parentId":null,`, entryHead{}, false},
		{`{"type":"message","id":"abc","parentId":nullx`, entryHead{}, false},
		{`{"type":"message","id":"abc","parentId":"par`, entryHead{}, false},
		{`{"id":"abc","type":"message","parentId":null,`, entryHead{}, false},
		{`{"type":"message","id":"abc","parentId":"p"`, entryHead{}, false},
	}
	for _, c := range cases {
		got, ok := parseEntryHead(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseEntryHead(%q) = %+v, %v", c.in, got, ok)
		}
	}
}

func TestParseSessionLineNormalizes(t *testing.T) {
	entry, ok := parseSessionLine(`{"type":"message","id":7,"parentId":null,"timestamp":"t","extra":1,"display":"yes",` +
		`"message":{"role":"user","content":"hi","timestamp":9,"isError":"x","toolName":5},` +
		`"replacement":null,"cwd":"/x","firstKeptEntryId":null}`)
	if !ok {
		t.Fatal("not parsed")
	}
	want := rec{
		"type": "message", "parentId": nil, "timestamp": "t", "display": false, "replacement": nil, "cwd": "/x",
		"message": rec{"role": "user", "content": "hi", "isError": false, "excludeFromContext": false},
	}
	if !reflect.DeepEqual(entry, want) {
		t.Errorf("entry = %#v\nwant %#v", entry, want)
	}
	entry, _ = parseSessionLine(`{"type":"context_edit","replacement":{"content":"c","other":1},"display":true}`)
	if !reflect.DeepEqual(entry, rec{"type": "context_edit", "replacement": rec{"content": "c"}, "display": true}) {
		t.Errorf("edit = %#v", entry)
	}
	for _, bad := range []string{"", "null", "[]", "7", `{"a":`, "not json"} {
		if _, ok := parseSessionLine(bad); ok {
			t.Errorf("parseSessionLine(%q) parsed", bad)
		}
	}
}

func TestDecodeCursor(t *testing.T) {
	key := shortKey(`["q",false]`)
	good := base64.RawURLEncoding.EncodeToString([]byte(`["abc",1,5,"` + key + `"]`))
	cursor, ok := decodeCursor(good, key)
	if !ok || cursor == nil || *cursor != (historyCursor{"abc", 1, 5, key}) {
		t.Fatalf("decodeCursor(good) = %+v, %v", cursor, ok)
	}
	if cursor, ok := decodeCursor("", key); !ok || cursor != nil {
		t.Errorf("empty cursor = %+v, %v", cursor, ok)
	}
	for name, raw := range map[string]string{
		"not json":   "not-json",
		"wrong key":  `["abc",1,5,"other"]`,
		"negative":   `["abc",1,-1,"` + key + `"]`,
		"fraction":   `["abc",1,1.5,"` + key + `"]`,
		"priority":   `["abc",2,5,"` + key + `"]`,
		"empty id":   `["",1,5,"` + key + `"]`,
		"short":      `["abc",1,5]`,
		"not array":  `{"a":1}`,
		"huge":       `["abc",1,9007199254740992,"` + key + `"]`,
		"string off": `["abc",1,"5","` + key + `"]`,
	} {
		encoded := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if name == "not json" {
			encoded = raw
		}
		if cursor, ok := decodeCursor(encoded, key); ok {
			t.Errorf("%s: decodeCursor accepted %+v", name, cursor)
		}
	}
	// Node's base64url decoder also accepts the standard alphabet and padding.
	std := base64.StdEncoding.EncodeToString([]byte(`["a>>?",0,0,"` + key + `"]`))
	if cursor, ok := decodeCursor(std, key); !ok || cursor.id != "a>>?" {
		t.Errorf("standard alphabet = %+v, %v", cursor, ok)
	}
}

func TestSearchAccumulatorCursorSeeking(t *testing.T) {
	failed := ""
	fail := func(m string) error { failed = m; return errTest(m) }
	acc := newSearchAccumulator(1, &historyCursor{id: "b", priority: 0, offset: 3}, fail)
	for _, hit := range []historyHit{{id: "a", text: "aaaa"}, {id: "b", text: "bbbb"}, {id: "c", text: "cccc"}} {
		if err := acc.add(hit); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := acc.results()
	if err != nil || len(hits) != 2 || hits[0].id != "b" || hits[1].id != "c" {
		t.Fatalf("results = %+v, %v", hits, err)
	}
	acc = newSearchAccumulator(1, &historyCursor{id: "gone", priority: 0, offset: 1}, fail)
	_ = acc.add(historyHit{id: "a", text: "x"})
	if _, err := acc.results(); err == nil || failed != "History cursor entry no longer matches; restart the search without it." {
		t.Errorf("missing anchor: %v %q", err, failed)
	}
	acc = newSearchAccumulator(1, &historyCursor{id: "a", priority: 0, offset: 9}, fail)
	if err := acc.add(historyHit{id: "a", text: "xx"}); err == nil || failed != "History cursor is past the entry; restart the search without it." {
		t.Errorf("past entry: %v %q", err, failed)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

// hlInt is a pointer to n, for the optional integer members of historyParams.
func hlInt(n int) *int { return &n }

// TestFoldPatternDifferential checks foldPattern against strings.Index of the lowercased text on
// random texts over a small alphabet (many near-matches, anchors in both cases, edges).
func TestFoldPatternDifferential(t *testing.T) {
	seed := uint64(88172645463325252)
	next := func(n int) int {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(n))
	}
	tokens := []string{"a", "A", "b", "B", "k", "K", " ", ",", "-", "\"", "é"}
	for round := 0; round < 4000; round++ {
		var text strings.Builder
		for range next(120) {
			text.WriteString(tokens[next(len(tokens))])
		}
		pat := make([]byte, 1+next(6))
		for i := range pat {
			pat[i] = "abk ,-"[next(6)]
		}
		want := strings.Index(strings.ToLower(text.String()), string(pat))
		if got := newFoldPattern(string(pat)).index(text.String()); got != want {
			t.Fatalf("index(%q, %q) = %d, want %d", text.String(), pat, got, want)
		}
		if got := foldIndexString(text.String(), string(pat)); got != want {
			t.Fatalf("foldIndexString(%q, %q) = %d, want %d", text.String(), pat, got, want)
		}
	}
}

// TestAsciiLinesWorstCase: a window made of the needle's own letters, or lacking its anchor byte,
// is decided without error in either direction.
func TestAsciiLinesWorstCase(t *testing.T) {
	m := needleLines([]string{"small marker"})
	if m.test(strings.Repeat("A", 1<<20)) || m.test(strings.Repeat("small marke", 5000)) {
		t.Error("matched text without the needle")
	}
	if !m.test(strings.Repeat("A", 1<<20) + "SMALL MARKER" + strings.Repeat("A", 7)) {
		t.Error("missed the needle after a long run")
	}
}

func TestWindowTrackerKeepsOnlyNonInitialWindows(t *testing.T) {
	w := windowProjection()
	steps := []struct {
		id, parent string
		isWin      bool
		want       string
	}{
		{"a", "", false, "initial"},
		{"w1", "a", true, "w1"},
		{"b", "w1", false, "w1"},
		{"c", "b", false, "w1"},
		{"w2", "c", true, "w2"},
		{"d", "w2", false, "w2"},
		{"c", "a", false, "initial"}, // a repeated id replaces its earlier window
		{"e", "c", false, "initial"},
		{"", "w1", false, "w1"},
		{"f", "unknown", false, "initial"},
	}
	for i, s := range steps {
		if got := w.track(s.id, s.parent, s.isWin); got != s.want {
			t.Errorf("step %d (%+v) = %q", i, s, got)
		}
	}
	if len(w.windows) != 4 { // w1, b, w2, d (c was replaced by an initial entry)
		t.Errorf("windows = %v", w.windows)
	}
}

func TestWindowProjectionWindowWithoutID(t *testing.T) {
	w := windowProjection()
	w.project(rec{"type": "context_window", "id": "w", "parentId": nil})
	if got := w.project(rec{"type": "context_window", "parentId": "w"}); got != "w" {
		t.Errorf("a window without an id inherits, got %q", got)
	}
	if got := w.project(rec{"type": "context_window", "id": "", "parentId": "w"}); got != "" {
		t.Errorf("a window with an empty id keeps it, got %q", got)
	}
}
