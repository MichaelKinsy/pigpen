package posthorse

// Call cards, message rows, colors, display construction sites and the text helpers, pinned (see ui_cards_test.go).

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

func TestUiCallCards(t *testing.T) {
	th := sdk.UITheme{}
	type row = []string
	for _, tc := range []struct {
		name  string
		tool  string
		args  map[string]any
		state cardCallState
		width int
		want  row
	}{
		{"notes read", "notes", map[string]any{"op": "read", "path": "plan.md"}, cardCallState{}, 80, row{"▸ Notes · read · plan.md"}},
		{"notes list", "notes", map[string]any{"op": "list"}, cardCallState{}, 80, row{"▸ Notes · list"}},
		{"notes list path", "notes", map[string]any{"op": "list", "path": "sub"}, cardCallState{}, 80, row{"▸ Notes · list · sub"}},
		{"notes list null path", "notes", map[string]any{"op": "list", "path": nil}, cardCallState{}, 80, row{"▸ Notes · list · …"}},
		{"notes search", "notes", map[string]any{"op": "search", "query": "needle"}, cardCallState{}, 80, row{"▸ Notes · search · “needle”"}},
		{"history search", "history", map[string]any{"op": "search", "query": "q"}, cardCallState{}, 80, row{"▸ History · search · “q”"}},
		{"history read", "history", map[string]any{"op": "read", "id": "e1"}, cardCallState{}, 80, row{"▸ History · read · e1"}},
		{"history read no id", "history", map[string]any{"op": "read"}, cardCallState{}, 80, row{"▸ History · read · …"}},
		{"history other", "history", map[string]any{"op": "recover"}, cardCallState{}, 80, row{"▸ History · recover"}},
		{"context", "get_context_remaining", map[string]any{}, cardCallState{}, 80, row{"▸ Context budget"}},
		{"new context", "new_context", map[string]any{"handoff": "h"}, cardCallState{}, 80, row{"▸ New context · request"}},
		{"streamed nothing", "notes", map[string]any{}, cardCallState{isPartial: true}, 80, row{"… Notes · … · preparing · …"}},
		{"invalid values", "notes", map[string]any{"op": 3.0, "path": map[string]any{}}, cardCallState{}, 80, row{"▸ Notes · [invalid op] · [invalid path]"}},
		{"invalid query", "history", map[string]any{"op": "search", "query": []any{"bad"}}, cardCallState{}, 80, row{"▸ History · search · “[invalid query]”"}},
		{"preparing", "notes", map[string]any{"op": "write", "path": "d.md"}, cardCallState{isPartial: true}, 80, row{"… Notes · write · preparing · d.md"}},
		{"running", "notes", map[string]any{"op": "write", "path": "d.md"}, cardCallState{isPartial: true, executionStarted: true}, 80, row{"… Notes · write · running · d.md"}},
		{"started but final", "notes", map[string]any{"op": "write", "path": "d.md"}, cardCallState{executionStarted: true}, 80, row{"▸ Notes · write · d.md"}},
		{"error", "notes", map[string]any{"op": "read", "path": "m.md"}, cardCallState{isError: true}, 80, row{"▸ Notes · read · error · m.md"}},
		{"error beats partial", "notes", map[string]any{"op": "read", "path": "m.md"}, cardCallState{isError: true, isPartial: true}, 80, row{"… Notes · read · error · m.md"}},
		{"controls", "notes", map[string]any{"op": "read", "path": "a\x1b[2Jb\x00c"}, cardCallState{}, 80, row{"▸ Notes · read · abc"}},
		{"collapsed whitespace", "notes", map[string]any{"op": "read", "path": "a\n  b\t\tc"}, cardCallState{}, 80, row{"▸ Notes · read · a  b c"}},
		{"collapsed truncation", "notes", map[string]any{"op": "read", "path": "a-very-long-name.md"}, cardCallState{}, 20, row{"▸ Notes · read · ..."}},
		{"expanded", "notes", map[string]any{"op": "read", "path": "plan.md"}, cardCallState{expanded: true}, 80, row{"▾ Notes · read · plan.md"}},
		{"expanded partial", "notes", map[string]any{"op": "read", "path": "plan.md"}, cardCallState{expanded: true, isPartial: true}, 80, row{"… Notes · read · preparing · plan.md"}},
		{"expanded error", "notes", map[string]any{"op": "read", "path": "m.md"}, cardCallState{expanded: true, isError: true}, 80, row{"▾ Notes · read · error · m.md"}},
		{"expanded wraps", "notes", map[string]any{"op": "read", "path": "one two three four five"}, cardCallState{expanded: true}, 20, row{"▾ Notes · read · one", "two three four five"}},
		{"expanded scope all", "history", map[string]any{"op": "search", "query": "q", "all": true}, cardCallState{expanded: true}, 80, row{"▾ History · search · “q”", "Scope: all project sessions"}},
		{"expanded scope branch", "history", map[string]any{"op": "search", "query": "q"}, cardCallState{expanded: true}, 80, row{"▾ History · search · “q”", "Scope: current branch"}},
		{"expanded scope not bool", "history", map[string]any{"op": "search", "query": "q", "all": "yes"}, cardCallState{expanded: true}, 80, row{"▾ History · search · “q”", "Scope: current branch"}},
		{"expanded offsets", "history", map[string]any{"op": "read", "id": "e1", "offset": 12345.0, "imageOffset": 2.0, "limit": 5.0}, cardCallState{expanded: true}, 80, row{"▾ History · read · e1", "Offset: 12,345 · Image offset: 2 · Limit: 5"}},
		{"expanded invalid offsets", "history", map[string]any{"op": "read", "id": "e1", "offset": "x", "imageOffset": []any{}, "limit": nil}, cardCallState{expanded: true}, 100, row{"▾ History · read · e1", "Offset: [invalid offset] · Image offset: [invalid imageOffset] · Limit: [invalid limit]"}},
		{"expanded notes no scope", "notes", map[string]any{"op": "search", "query": "q", "limit": 3.0}, cardCallState{expanded: true}, 80, row{"▾ Notes · search · “q”", "Limit: 3"}},
	} {
		uiSame(t, tc.name, uiPlain(cardCallComponent(tc.tool, tc.args, th, tc.state, tc.width)), tc.want)
	}
	if got := cardTitle("other"); got != "New context" {
		t.Errorf("default title %q", got)
	}
	for tool, want := range map[string]string{"notes": "Notes", "history": "History", "get_context_remaining": "Context budget", "new_context": "New context"} {
		if got := cardTitle(tool); got != want {
			t.Errorf("title of %s = %q", tool, got)
		}
	}
}

func uiKeys(t *testing.T) {
	t.Helper()
	tui.SetKeybindings(tui.NewKeybindingsManager(tui.KeybindingDefinitions{"app.tools.expand": {DefaultKeys: []string{"ctrl+o"}, Description: "Expand tools"}}, tui.KeybindingsConfig{}))
}

func TestUiMessageRows(t *testing.T) {
	uiKeys(t)
	th := sdk.UITheme{}
	reminder := func(content any) map[string]any {
		return map[string]any{"customType": "posthorse-reminder", "content": content}
	}
	window := func(content any) map[string]any {
		return map[string]any{"customType": "context-window", "content": content}
	}
	label := "Context window window-2 starts here.\n\nHandoff from the previous window:\n"
	type row = []string
	for _, tc := range []struct {
		name    string
		message map[string]any
		options sdk.MessageRenderOptions
		width   int
		want    row
	}{
		{"reminder collapsed", reminder("Checkpoint now\nl2\nl3\nl4\nl5"), sdk.MessageRenderOptions{OutputPad: 1}, 40,
			row{"", " ▸ Checkpoint reminder", " Checkpoint now", " l2", " l3", " … ctrl+o to expand", ""}},
		{"reminder short", reminder("one"), sdk.MessageRenderOptions{OutputPad: 1}, 40,
			row{"", " ▸ Checkpoint reminder", " one", " … ctrl+o to expand", ""}},
		{"reminder expanded", reminder("Checkpoint now\nl2\nl3\nl4\nl5"), sdk.MessageRenderOptions{Expanded: true, OutputPad: 1}, 40,
			row{"", " ▾ Checkpoint reminder", " Checkpoint now", " l2", " l3", " l4", " l5", ""}},
		{"reminder no pad", reminder("a\nb\nc\nd"), sdk.MessageRenderOptions{OutputPad: 0}, 40,
			row{"", "▸ Checkpoint reminder", "a", "b", "c", "… ctrl+o to expand", ""}},
		{"reminder pad 2", reminder("a"), sdk.MessageRenderOptions{OutputPad: 2}, 40,
			row{"", "  ▸ Checkpoint reminder", "  a", "  … ctrl+o to expand", ""}},
		{"reminder pad clamped", reminder("abcdef"), sdk.MessageRenderOptions{OutputPad: 50}, 10,
			row{"", "    ..", "    ab", "    cd", "    ef", "    ..", ""}},
		{"reminder narrow", reminder("abcdef"), sdk.MessageRenderOptions{OutputPad: 1}, 8,
			row{"", " ▸ C...", " abcdef", " … c...", ""}},
		{"reminder wraps", reminder("aaaa bbbb cccc dddd"), sdk.MessageRenderOptions{Expanded: true, OutputPad: 1}, 12,
			row{"", " ▾ Check...", " aaaa bbbb", " cccc dddd", ""}},
		{"reminder blocks", reminder([]any{map[string]any{"type": "text", "text": "FIRST"}, map[string]any{"type": "text", "text": ""}, map[string]any{"type": "text", "text": "LAST"}}), sdk.MessageRenderOptions{Expanded: true, OutputPad: 1}, 40,
			row{"", " ▾ Checkpoint reminder", " FIRST", "", " LAST", ""}},
		{"reminder controls", reminder("a\x1b[2Jb\x00c"), sdk.MessageRenderOptions{Expanded: true, OutputPad: 1}, 40,
			row{"", " ▾ Checkpoint reminder", " abc", ""}},
		{"window collapsed", window(label + "H1\nH2\nH3\nH4"), sdk.MessageRenderOptions{OutputPad: 1}, 60,
			row{"", " ▸ Context window started", " Earlier conversation remains in history.", " H1", " H2", " … ctrl+o to expand", ""}},
		{"window history wraps", window(label + "H1\nH2\nH3\nH4"), sdk.MessageRenderOptions{OutputPad: 1}, 30,
			row{"", " ▸ Context window started", " Earlier conversation remains", " in history.", " H1", " H2", " … ctrl+o to expand", ""}},
		{"window expanded", window(label + "H1\nH2"), sdk.MessageRenderOptions{Expanded: true, OutputPad: 1}, 60,
			row{"", " ▾ Context window started", " Context window window-2 starts here.", "", " Handoff from the previous window:", " H1", " H2", ""}},
		{"window no label", window("Context window w starts here."), sdk.MessageRenderOptions{OutputPad: 1}, 60,
			row{"", " ▸ Context window started", " Earlier conversation remains in history.", " … ctrl+o to expand", ""}},
		{"window label at start", window("\n\nHandoff from the previous window:\nH1"), sdk.MessageRenderOptions{OutputPad: 1}, 60,
			row{"", " ▸ Context window started", " Earlier conversation remains in history.", " H1", " … ctrl+o to expand", ""}},
		{"window one handoff row", window(label + "only"), sdk.MessageRenderOptions{OutputPad: 1}, 60,
			row{"", " ▸ Context window started", " Earlier conversation remains in history.", " only", " … ctrl+o to expand", ""}},
	} {
		uiSame(t, tc.name, uiPlain(cardMessageRows(th, tc.message, tc.options, tc.width)), tc.want)
	}
}

// Every row of a message is padded to the width, so the background reaches the edge.
func TestUiMessageRowsFillTheWidth(t *testing.T) {
	uiKeys(t)
	rows := cardMessageRows(sdk.UITheme{}, map[string]any{"customType": "posthorse-reminder", "content": "abc"}, sdk.MessageRenderOptions{OutputPad: 1}, 30)
	for i, r := range rows {
		if w := tui.VisibleWidth(r); w != 30 {
			t.Errorf("row %d is %d columns: %q", i, w, r)
		}
	}
	rows = cardMessageRows(sdk.UITheme{}, map[string]any{"customType": "posthorse-reminder", "content": "abc"}, sdk.MessageRenderOptions{OutputPad: 1}, 1)
	if len(rows) != 7 {
		t.Errorf("width 1: %q", rows)
	}
	for i, r := range rows {
		if w := tui.VisibleWidth(r); w != 1 {
			t.Errorf("width 1 row %d is %d columns: %q", i, w, r)
		}
	}
}

func TestUiBoxRows(t *testing.T) {
	bg := func(s string) string { return "[" + s + "]" }
	got := piBox(2, 1, []string{"ab", "c"}, 8, bg)
	uiSame(t, "box", got, []string{"[        ]", "[  ab    ]", "[  c     ]", "[        ]"})
	if got := piBox(1, 1, nil, 8, bg); len(got) != 0 {
		t.Errorf("a box without rows draws nothing: %q", got)
	}
	if got := piBox(0, 0, []string{"abcdefghij"}, 8, bg); len(got) != 1 || got[0] != "[abcdefghij]" {
		t.Errorf("a wide row is not cut: %q", got)
	}
}

func TestUiRegisterMessages(t *testing.T) {
	u := newUIHost(t)
	for _, customType := range []string{"context-window", "posthorse-reminder"} {
		if rows := uiLines(t, u.message(customType, "text", 1), 40); len(rows) < 3 {
			t.Errorf("%s: %q", customType, rows)
		}
	}
}

func TestUiTextHelpers(t *testing.T) {
	for _, r := range []rune{'\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2000, 0x2005, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff} {
		if !uiSpace(r) {
			t.Errorf("%U is JS whitespace", r)
		}
		if got := uiCollapseSpaces("a" + string(r) + string(r) + "b"); got != "a b" {
			t.Errorf("collapse %U: %q", r, got)
		}
	}
	for _, r := range []rune{'a', 0x200b, 0x2060, 0x180e, 0x85, 0x200c} {
		if uiSpace(r) {
			t.Errorf("%U is not JS whitespace", r)
		}
	}
	if got := uiCollapseSpaces(" \t a  \n b \u00a0 "); got != " a b " {
		t.Errorf("collapse %q", got)
	}
	if got := uiTrimEnd("a \u00a0\n"); got != "a" {
		t.Errorf("trimEnd %q", got)
	}
	if got := uiTrimBoth("\ufeff a \u3000"); got != "a" {
		t.Errorf("trim %q", got)
	}
	for _, tc := range []struct{ in, want string }{
		{"plain\ttab\nline", "plain\ttab\nline"},
		{"a\x00b", "ab"}, {"a\x07b", "ab"}, {"a\x08b", "ab"}, {"a\x0bb", "ab"}, {"a\x0cb", "ab"}, {"a\rb", "ab"}, {"a\x1fb", "ab"}, {"a\x7fb", "ab"},
		{"a\u0080b", "ab"}, {"a\u0085b", "ab"}, {"a\u009fb", "ab"}, {"a\u00a0b", "a\u00a0b"}, {"a\u00a9b", "a\u00a9b"},
		{"a\ufff8b", "a\ufff8b"}, {"a\ufff9b", "ab"}, {"a\ufffab", "ab"}, {"a\ufffbb", "ab"}, {"a\ufffcb", "a\ufffcb"}, {"a\u00efb", "a\u00efb"},
		{"x\x1b[2Jy", "xy"}, {"x\x1b[31mred\x1b[0m", "xred"}, {"x\x1b]52;c;c2VjcmV0\x07y", "xy"}, {"x\x1b_hidden\x1b\\y", "xy"}, {"x\x1by", "xy"},
		{"e\u0301 界 👩🏽‍💻", "e\u0301 界 👩🏽‍💻"},
		{"\xed\xa0\xbd tail\x00", "\xed\xa0\xbd tail"},
	} {
		if got := cleanText(tc.in); got != tc.want {
			t.Errorf("cleanText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := cardArgsOf(nil); got == nil || len(got) != 0 {
		t.Errorf("argsOf(nil) = %v", got)
	}
	if got := cardArgsOf("str"); got == nil || len(got) != 0 {
		t.Errorf("argsOf(string) = %v", got)
	}
	if got := cardArgsOf(map[string]any{"a": 1}); got["a"] != 1 {
		t.Errorf("argsOf(map) = %v", got)
	}
	for _, tc := range []struct {
		value any
		want  string
	}{{"a\x1b[2Jb", "ab"}, {nil, "…"}, {3.0, "[invalid op]"}, {true, "[invalid op]"}, {map[string]any{}, "[invalid op]"}, {[]any{}, "[invalid op]"}} {
		if got := cardArgument(tc.value, "op"); got != tc.want {
			t.Errorf("argument(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
	blocks := []any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "image", "text": "x"}, map[string]any{"type": "text", "text": 5.0}, map[string]any{"type": "text", "text": "b"}, "str", nil}
	if got := cardTextOf(blocks); got != "a\nb" {
		t.Errorf("textOf %q", got)
	}
	if got := cardTextOf("plain"); got != "plain" {
		t.Errorf("textOf string %q", got)
	}
	if got := cardTextOf(5.0); got != "" {
		t.Errorf("textOf number %q", got)
	}
	typed := []map[string]any{{"type": "text", "text": "a"}, {"type": "image"}, {"type": "text", "text": "b"}, {"type": "text", "text": 1.0}}
	if got := cardTextOfBlocks(typed); got != "a\nb" {
		t.Errorf("textOfBlocks %q", got)
	}
}

func TestUiPiText(t *testing.T) {
	uiSame(t, "tab", piText("a\tb", 10), []string{"a   b     "})
	uiSame(t, "pad", piText("abc", 6), []string{"abc   "})
	uiSame(t, "wrap", piText("aaaa bbbb", 6), []string{"aaaa  ", "bbbb  "})
	uiSame(t, "width 1", piText("abcd", 1), []string{"a", "b", "c", "d"})
	uiSame(t, "width 0", piText("ab", 0), []string{"a", "b"})
	if got := piText("", 10); len(got) != 0 {
		t.Errorf("empty text: %q", got)
	}
	if got := piText(" \t\u00a0\n", 10); len(got) != 0 {
		t.Errorf("blank text: %q", got)
	}
	if got := piText("\ufeff", 10); len(got) != 0 {
		t.Errorf("BOM text: %q", got)
	}
	th := sdk.UITheme{}
	if got := cardBlock("", th, colorMuted)(10); len(got) != 0 {
		t.Errorf("empty block: %q", got)
	}
	uiSame(t, "block", uiPlain(cardBlock("a\x1b[2Jb", th, colorMuted)(10)), []string{"ab"})
	if got := cardHint(th, 40, "ctrl+o"); uiPlain([]string{got})[0] != "… ctrl+o to expand" {
		t.Errorf("hint %q", got)
	}
	if got := cardHint(th, 40, ""); uiPlain([]string{got})[0] != "… details hidden" {
		t.Errorf("hint without key %q", got)
	}
	if got := cardHint(th, 40, "x"); uiPlain([]string{got})[0] != "… x to expand" {
		t.Errorf("hint one-character key %q", got)
	}
	if got := uiPlain([]string{cardHint(th, 10, "ctrl+o")})[0]; got != "… ctrl+..." {
		t.Errorf("hint is cut to the width: %q", got)
	}
}

func TestUiLeadingRowsCounts(t *testing.T) {
	th := sdk.UITheme{}
	// Four lines are enough to show that the text continues: the rest is not wrapped.
	rows := cardLeadingRows("1\n2\n3\n4\n5\n6\n7", th, colorToolOutput, 3)(20)
	uiSame(t, "lines", uiPlain(rows), []string{"1", "2", "3", "4"})
	rows = cardLeadingRows("1\n2\n3", th, colorToolOutput, 3)(20)
	uiSame(t, "short", uiPlain(rows), []string{"1", "2", "3"})
	rows = cardLeadingRows("1\n2\n3\n", th, colorToolOutput, 3)(20)
	uiSame(t, "trailing newline", uiPlain(rows), []string{"1", "2", "3", ""})
	if rows := cardLeadingRows("", th, colorToolOutput, 3)(20); len(rows) != 0 {
		t.Errorf("empty: %q", rows)
	}
	// A long line is wrapped as far as (count+1)*width+1024 units, then up to the cut.
	long := strings.Repeat("abcdefghi ", 300)
	if got := len(cardLeadingRows(long, th, colorToolOutput, 3)(20)); got != 56 {
		t.Errorf("a long line wraps its first 1,104 units: %d rows", got)
	}
	if got := len(cardLeadingRows(long, th, colorToolOutput, 3)(100)); got != 15 {
		t.Errorf("width 100: %d rows", got)
	}
	// Zero-width text has no rows to show until the whole head is wrapped.
	zero := "a" + strings.Repeat("\u0301", 5000) + "\nb\nc\nd\ne"
	if got := len(cardLeadingRows(zero, th, colorToolOutput, 3)(20)); got != 4 {
		t.Errorf("a grapheme wider than the scan: %d rows", got)
	}
}

func TestUiSafeCutPlaces(t *testing.T) {
	s := strings.Repeat("ab ", 20)
	if got := safeCut(s, 10); got != 10 { // after "ab ab ab ab" the next boundary is at once
		t.Errorf("safeCut = %d", got)
	}
	if got := safeCut("abc", 10); got != 3 {
		t.Errorf("past the end: %d", got)
	}
	// "e" + 5000 combining marks has no boundary within the scan window: the whole text is the cut.
	zalgo := "e" + strings.Repeat("\u0301", 5000) + "tail"
	if got := safeCut(zalgo, 3); got != len(zalgo) {
		t.Errorf("one grapheme: %d of %d", got, len(zalgo))
	}
	// a cut never lands inside a regional-indicator pair, a ZWJ sequence or a lone surrogate's bytes.
	flags := strings.Repeat("🇺🇸", 50)
	cut := safeCut(flags, 7)
	if cut%8 != 0 {
		t.Errorf("flag pair cut at byte %d", cut)
	}
	pair := strings.Repeat("\xed\xa0\xbd\xed\xb8\x80", 10)
	if got := safeCut(pair, 3); got != len(pair) {
		t.Errorf("surrogate halves never separate: %d", got)
	}
}

func uiDetailsJSON(t *testing.T, res fixResult) string {
	t.Helper()
	var shape struct {
		Details json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal(res.Raw, &shape); err != nil || len(shape.Details) == 0 {
		t.Fatalf("no details in %s (%v)", res.Raw, err)
	}
	return string(shape.Details)
}

// The `details` each tool puts on its result (the visible trace shape: key order and members).
func TestUiDisplaySites(t *testing.T) {
	dir := uiRealDir(t)
	run := func(name string, args rec, opts ...fixOpt) string {
		t.Helper()
		return uiDetailsJSON(t, uiRun(t, name, args, append([]fixOpt{uiCwd(dir)}, opts...)...))
	}
	if got := run("notes", rec{"op": "write", "path": "a.md", "content": "hello"}); got != `{"kind":"note-write"}` {
		t.Errorf("write %s", got)
	}
	if got := run("notes", rec{"op": "append", "path": "a.md", "content": " more"}); got != `{"kind":"note-append"}` {
		t.Errorf("append %s", got)
	}
	header := u16len("File: " + dir + "/.pi/notes/a.md\n")
	if got, want := run("notes", rec{"op": "read", "path": "a.md"}), `{"kind":"note-read","headerLength":`+uiNumberPlain(header)+`,"offset":0,"end":12,"total":12}`; got != want {
		t.Errorf("read %s want %s", got, want)
	}
	if got := run("notes", rec{"op": "read", "path": "a.md", "offset": 4.0}); !strings.Contains(got, `"offset":4,"end":12,"total":12}`) {
		t.Errorf("read offset %s", got)
	}
	listHeader := u16len("Notes directory: " + dir + "/.pi/notes\n")
	listRe := regexp.MustCompile(`^\{"kind":"notes-list","headerLength":` + uiNumberPlain(listHeader) + `,"count":1,"page":\{"offset":0,"end":\d+,"total":\d+\}\}$`)
	if got := run("notes", rec{"op": "list"}); !listRe.MatchString(got) {
		t.Errorf("list %s", got)
	}
	searchRe := regexp.MustCompile(`^\{"kind":"notes-search","headerLength":0,"count":1,"page":\{"offset":0,"end":\d+,"total":\d+\}\}$`)
	if got := run("notes", rec{"op": "search", "query": "hello"}); !searchRe.MatchString(got) {
		t.Errorf("search %s", got)
	}
	if got := run("new_context", rec{"handoff": "next"}); got != `{"kind":"new-context"}` {
		t.Errorf("new_context %s", got)
	}
	ctxRe := regexp.MustCompile(`^\{"kind":"context","usage":\{"tokens":1000,"contextWindow":100000,"percent":1\},"rollover":"enabled","rolloverAt":\d+\}$`)
	if got := run("get_context_remaining", rec{}); !ctxRe.MatchString(got) {
		t.Errorf("context %s", got)
	}
	if got := run("get_context_remaining", rec{}, withEnabled(false)); !strings.Contains(got, `"rollover":"disabled","rolloverAt":`) {
		t.Errorf("context disabled %s", got)
	}
	if got := run("get_context_remaining", rec{}, withUsageUnknown(100_000)); got != `{"kind":"context"}` {
		t.Errorf("context unknown %s", got)
	}
	entries := []rec{fxMessageEntry("e1", fxUser("Relay station one")), fxMessageEntry("e2", fxUser("Relay station two"))}
	histRe := regexp.MustCompile(`^\{"kind":"history-search","entries":\[\{"headerLength":\d+,"length":\d+\},\{"headerLength":\d+,"length":\d+\}\],"footerLength":0,"more":false,"skipped":0\}$`)
	if got := run("history", rec{"op": "search", "query": "Relay station"}, withSession(fxIndexedSession(entries...)), withEmptyProjection()); !histRe.MatchString(got) {
		t.Errorf("history search %s", got)
	}
	readRe := regexp.MustCompile(`^\{"kind":"history-read","headerLength":\d+,"offset":0,"end":\d+,"total":\d+,"imageOffset":0,"imageEnd":1,"imageTotal":1\}$`)
	picture := rec{"type": "message", "id": "pic", "timestamp": "2026-09-06T17:43:46Z", "message": rec{"role": "user", "content": []any{fxText("Relay station one"), fxImage("iVBORw0KGgo=", "image/png")}}}
	if got := run("history", rec{"op": "read", "id": "pic"}, withSession(fxIndexedSession(picture)), withEmptyProjection()); !readRe.MatchString(got) {
		t.Errorf("history read %s", got)
	}
}

func uiNumberPlain(n int) string { return jsToLocaleInt(n)[:0] + cardItoa(n) }
