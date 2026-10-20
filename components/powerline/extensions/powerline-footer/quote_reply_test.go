package powerline_footer

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// Behaviour of quote-reply.ts beyond the one upstream case: candidates, ids, formatting, the select fallback, the picker.

type pickerPlainTheme struct{}

func (pickerPlainTheme) Fg(_, text string) string  { return text }
func (pickerPlainTheme) Bold(text string) string   { return text }
func (pickerPlainTheme) Italic(text string) string { return text }

func TestReplyWithoutMessagesWarns(t *testing.T) {
	ctx := &fakeQuoteCtx{mode: "rpc", branch: []map[string]any{messageEntry("t1", "2026-08-23T00:00:00.000Z", "tool", textBlocks("x")), messageEntry("e1", "2026-08-23T00:00:00.000Z", "user", textBlocks("  "))}}
	if err := reply("", ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.notifications) != 1 || ctx.notifications[0] != "No user or assistant messages are available to quote." || ctx.levels[0] != "warning" {
		t.Errorf("notifications = %q %q", ctx.notifications, ctx.levels)
	}
}

func TestReplyIdMatching(t *testing.T) {
	branch := []map[string]any{
		messageEntry("abc111", "2026-08-23T00:00:00.000Z", "user", "first"),
		messageEntry("abc222", "2026-08-23T00:01:00.000Z", "assistant", textBlocks("second", "third")),
		messageEntry("DEF333", "2026-08-23T00:02:00.000Z", "user", "fourth"),
	}
	cases := []struct {
		args, wantNote, wantLevel, wantText string
	}{
		{"abc", `Message id "abc" matches 2 messages. Type more of the id.`, "warning", "draft"},
		{"zzz", `No quotable message matches id "zzz".`, "warning", "draft"},
		{"#abc2", "Inserted quote from assistant message abc222.", "info", "Quoted previous assistant message abc222 for reference only:\n\n> second\n>\n> third\n\nMy reply:\ndraft"},
		{"  #def  ", "Inserted quote from user message DEF333.", "info", "Quoted previous user message DEF333 for reference only:\n\n> fourth\n\nMy reply:\ndraft"},
		{"##abc", `No quotable message matches id "#abc".`, "warning", "draft"},
	}
	for _, c := range cases {
		ctx := &fakeQuoteCtx{mode: "rpc", text: "draft", branch: branch}
		if err := reply(c.args, ctx); err != nil {
			t.Fatal(err)
		}
		if len(ctx.notifications) != 1 || ctx.notifications[0] != c.wantNote || ctx.levels[0] != c.wantLevel || ctx.text != c.wantText {
			t.Errorf("reply(%q): notes %q %q text %q", c.args, ctx.notifications, ctx.levels, ctx.text)
		}
	}
}

func TestReplyEmptyIdIsAnError(t *testing.T) {
	ctx := &fakeQuoteCtx{mode: "rpc", branch: []map[string]any{messageEntry("  ", "2026-08-23T00:00:00.000Z", "user", "x")}}
	if err := reply("", ctx); err == nil || err.Error() != "Session entry id must not be empty." {
		t.Errorf("err = %v", err)
	}
}

func TestCandidatesAreNewestFirstAndCapped(t *testing.T) {
	var branch []map[string]any
	for i := range 250 {
		branch = append(branch, messageEntry(fmt.Sprintf("id%03d", i), "2026-08-23T00:00:00.000Z", "user", fmt.Sprintf("m%d", i)))
	}
	candidates, err := collectCandidates(&fakeQuoteCtx{branch: branch})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 200 || candidates[0].ID != "id249" || candidates[199].ID != "id050" {
		t.Errorf("%d candidates, first %s last %s", len(candidates), candidates[0].ID, candidates[len(candidates)-1].ID)
	}
	if candidates[0].SearchText != "id249 user m249" {
		t.Errorf("search text %q", candidates[0].SearchText)
	}
}

func TestSelectFallback(t *testing.T) {
	var branch []map[string]any
	for i := range 100 {
		branch = append(branch, messageEntry(fmt.Sprintf("id%03d", i), "2026-08-23T00:00:00.000Z", "user", fmt.Sprintf("line one\n\n  line\ttwo %d", i)))
	}
	ctx := &fakeQuoteCtx{mode: "rpc", text: "", branch: branch, selectOK: true, selectResult: "id097 user: line one line two 97"}
	if err := reply("", ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.selectTitle != "Reply to previous message" || len(ctx.selectOptions) != 80 || ctx.selectOptions[0] != "id099 user: line one line two 99" {
		t.Errorf("select %q %d %q", ctx.selectTitle, len(ctx.selectOptions), ctx.selectOptions[0])
	}
	if !strings.HasPrefix(ctx.text, "Quoted previous user message id097 for reference only:") || len(ctx.notifications) != 1 {
		t.Errorf("text %q notes %q", ctx.text, ctx.notifications)
	}

	dismissed := &fakeQuoteCtx{mode: "rpc", text: "keep", branch: branch}
	if err := reply("", dismissed); err != nil {
		t.Fatal(err)
	}
	if dismissed.text != "keep" || len(dismissed.notifications) != 0 {
		t.Errorf("a dismissed select changed %q %q", dismissed.text, dismissed.notifications)
	}

	unknown := &fakeQuoteCtx{mode: "rpc", text: "keep", branch: branch, selectOK: true, selectResult: "nope user: x"}
	_ = reply("", unknown)
	if unknown.text != "keep" {
		t.Errorf("an unknown option changed the draft: %q", unknown.text)
	}
}

func TestTUIUsesThePickerAndClosesThroughItsResult(t *testing.T) {
	branch := []map[string]any{messageEntry("aa11", "2026-08-23T00:00:00.000Z", "user", "one"), messageEntry("bb22", "2026-08-23T00:01:00.000Z", "assistant", "two")}
	ctx := &fakeQuoteCtx{mode: "tui", canCustom: true, text: "d", branch: branch, pickResult: "aa11"}
	if err := reply("", ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.picked) != 2 || ctx.picked[0].ID != "bb22" || ctx.selectTitle != "" {
		t.Errorf("picked %+v, select title %q", ctx.picked, ctx.selectTitle)
	}
	if !strings.HasPrefix(ctx.text, "Quoted previous user message aa11") || !strings.HasSuffix(ctx.text, "My reply:\nd") {
		t.Errorf("text %q", ctx.text)
	}
	cancelled := &fakeQuoteCtx{mode: "tui", canCustom: true, text: "d", branch: branch}
	_ = reply("", cancelled)
	if cancelled.text != "d" || len(cancelled.notifications) != 0 {
		t.Errorf("a cancelled picker changed %q %q", cancelled.text, cancelled.notifications)
	}
	// A mode other than tui never opens the overlay, even where custom exists.
	print := &fakeQuoteCtx{mode: "print", canCustom: true, branch: branch}
	_ = reply("", print)
	if print.picked != nil || print.selectTitle == "" {
		t.Error("print mode must use select")
	}
}

func TestFormatQuote(t *testing.T) {
	c := quoteCandidate{ID: "x1", Role: "assistant", Text: "a\r\n\r\nb\nc"}
	if got, want := formatQuote(c), "Quoted previous assistant message x1 for reference only:\n\n> a\n>\n> b\n> c\n\nMy reply:\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	long := quoteCandidate{ID: "x2", Role: "user", Text: strings.Repeat("ab ", 2500)} // 7500 characters
	got := formatQuote(long)
	if !strings.Contains(got, "\n\n> [Quote truncated from 7,500 characters.]\n\nMy reply:\n") {
		t.Errorf("truncation note missing: %q", got[len(got)-80:])
	}
	if body := strings.TrimPrefix(strings.SplitN(got, "\n\n", 3)[1], "> "); len([]rune(body)) != 5999 { // 6000 cut, then the trailing space trimmed
		t.Errorf("quoted %d characters", len([]rune(body)))
	}
}

func TestExcerpt(t *testing.T) {
	if got := excerpt("  a \n\t b  ", 220); got != "a b" {
		t.Errorf("got %q", got)
	}
	if got := excerpt("hello world again", 10); got != "hello wor…" {
		t.Errorf("got %q", got)
	}
	if got := excerpt("hello worl", 7); got != "hello…" {
		t.Errorf("trailing blanks before the ellipsis are trimmed: %q", got)
	}
}

func TestFuzzyFilter(t *testing.T) {
	items := []string{"abc111 user the quick fox", "abc222 assistant lazy dog", "zzz user quick"}
	get := func(s string) string { return s }
	if got := fuzzyFilter(items, "  ", get); len(got) != 3 {
		t.Errorf("blank query keeps all: %q", got)
	}
	got := fuzzyFilter(items, "quick", get)
	if len(got) != 2 || got[0] != "zzz user quick" {
		t.Errorf("quick = %q (the exact word near the end wins)", got)
	}
	if got := fuzzyFilter(items, "assistant dog", get); len(got) != 1 || got[0] != items[1] {
		t.Errorf("two tokens = %q", got)
	}
	if got := fuzzyFilter(items, "abc/222", get); len(got) != 1 {
		t.Errorf("slash separates tokens: %q", got)
	}
	if m := fuzzyMatch("12abc", "abc12"); !m.matches || m.score == 0 {
		t.Errorf("a swapped letters/digits query matches: %+v", m)
	}
}

func newTestPicker() *quotePicker {
	var candidates []quoteCandidate
	for i := range 12 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		candidates = append(candidates, quoteCandidate{ID: fmt.Sprintf("id%02d", i), Role: role, Text: fmt.Sprintf("message number %d with some text to excerpt", i),
			TimestampMs: time.Date(2026, 10, 20, 9, 5, 0, 0, time.Local).UnixMilli(), SearchText: fmt.Sprintf("id%02d %s message number %d with some text to excerpt", i, role, i)})
	}
	now := func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local) }
	return newQuotePicker(candidates, pickerPlainTheme{}, now, nil, nil)
}

func TestPickerRenders(t *testing.T) {
	p := newTestPicker()
	lines := p.Render(90)
	// top border, blank, search, blank, divider, blank, 8 rows, blank, scroll row, blank, divider, blank, help, bottom
	if len(lines) != 5+1+8+1+2+4 {
		t.Fatalf("%d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for i, line := range lines {
		if w := visibleWidth(line); w != 90 {
			t.Errorf("line %d is %d wide: %q", i, w, line)
		}
	}
	if !strings.Contains(lines[0], " Reply ") || !strings.HasPrefix(lines[0], "╭") || !strings.HasSuffix(lines[0], "╮") {
		t.Errorf("top border %q", lines[0])
	}
	if !strings.Contains(lines[2], "◎  │type to filter messages...") {
		t.Errorf("search line %q", lines[2])
	}
	if !strings.Contains(lines[6], "▸ user id00  09:05 AM  —  ") || strings.Contains(lines[7], "▸") || !strings.Contains(lines[7], "· assistant id01") {
		t.Errorf("rows %q / %q", lines[6], lines[7])
	}
	if !strings.Contains(lines[15], "● ○ ○ ○ ○ ○ ○ ○ ○ ○  1/12") {
		t.Errorf("scroll row %q", lines[16])
	}
	if !strings.Contains(lines[len(lines)-2], "↑↓ navigate  enter quote  esc cancel  ⌫ edit") {
		t.Errorf("help row %q", lines[len(lines)-2])
	}
	if got := p.Render(40); !strings.Contains(strings.Join(got, "\n"), "id00") {
		t.Error("a narrow render keeps the rows")
	}
}

func TestPickerKeys(t *testing.T) {
	p := newTestPicker()
	selected := func(data string) *quoteCandidate {
		t.Helper()
		s, _ := p.HandleInput(data)
		return s
	}
	selected("\x1b[B")
	selected("\x1bOB")
	if s := selected("\r"); s == nil || s.ID != "id02" {
		t.Errorf("after two downs Enter chose %+v", s)
	}
	selected("\x1b[A")
	selected("\x1b[A")
	selected("\x1b[A") // wraps to the last
	if s := selected("\n"); s == nil || s.ID != "id11" {
		t.Errorf("wrapped up chose %+v", s)
	}
	selected("\x1b[1;1B") // kitty arrow, no modifier
	if s := selected("\x1b[13u"); s == nil || s.ID != "id00" {
		t.Errorf("kitty enter after wrapping down chose %+v", s)
	}

	for _, key := range []string{"i", "d", "0", "5"} {
		selected(key)
	}
	if got := strings.Join(p.Render(80), "\n"); !strings.Contains(got, "id05") || strings.Contains(got, "id04") || !strings.Contains(got, "id05│") && !strings.Contains(got, "id05") {
		t.Errorf("filter id05:\n%s", got)
	}
	if s := selected("\r"); s == nil || s.ID != "id05" {
		t.Errorf("filtered Enter chose %+v", s)
	}
	selected("\x7f") // backspace removes a character
	selected("\x7f")
	selected("\x7f")
	selected("\x7f")
	if got := strings.Join(p.Render(80), "\n"); !strings.Contains(got, "type to filter messages...") {
		t.Errorf("after four backspaces:\n%s", got)
	}
	selected("x")
	selected("y")
	selected("z")
	if got := strings.Join(p.Render(80), "\n"); !strings.Contains(got, "No matching messages") {
		t.Errorf("nothing matches xyz:\n%s", got)
	}
	if s := selected("\r"); s != nil {
		t.Errorf("Enter on no matches chose %+v", s)
	}
	selected("\x15") // ctrl+u clears
	if got := strings.Join(p.Render(80), "\n"); !strings.Contains(got, "type to filter messages...") {
		t.Errorf("after ctrl+u:\n%s", got)
	}
	selected("\x1b[97;1u") // kitty printable "a"
	if p.query != "a" {
		t.Errorf("a kitty printable did not type: %q", p.query)
	}
	selected("\x1b[98;2:2u") // shift+b as the shifted key "B" is not given: the base codepoint
	if p.query != "ab" {
		t.Errorf("a shifted kitty key without a shifted codepoint types the base: %q", p.query)
	}
	selected("\x1b[98:66;2u") // shift+b with the shifted codepoint 66
	if p.query != "abB" {
		t.Errorf("the shifted codepoint is typed: %q", p.query)
	}
	selected("\x1b[97;5u") // ctrl+a is not text
	selected("\x1b[97;3u") // alt+a is not text
	if p.query != "abB" {
		t.Errorf("a ctrl or alt kitty key typed: %q", p.query)
	}
	if _, cancelled := p.HandleInput("\x1b[27;5;99~"); !cancelled {
		t.Error("modifyOtherKeys ctrl+c did not cancel")
	}
	if _, cancelled := p.HandleInput("\x1b"); !cancelled {
		t.Error("escape did not cancel")
	}
	if _, cancelled := p.HandleInput("\x03"); !cancelled {
		t.Error("ctrl+c did not cancel")
	}
	if _, cancelled := p.HandleInput("\x1b[99;5u"); !cancelled {
		t.Error("kitty ctrl+c did not cancel")
	}
	if _, cancelled := p.HandleInput("\x1b[27u"); !cancelled {
		t.Error("kitty escape did not cancel")
	}
	// A chunk with a control character is not typed.
	before := p.query
	p.HandleInput("a\x01b")
	p.HandleInput("\x1b[1;5C")
	if p.query != before {
		t.Errorf("a control chunk changed the query to %q", p.query)
	}
}

func TestPickerComponentClosesThroughItsResult(t *testing.T) {
	c := &quotePickerComponent{picker: newTestPicker()}
	if r, _ := c.HandleInput("\x1b[B"); r.Done {
		t.Error("navigation closed the overlay")
	}
	r, err := c.HandleInput("\r")
	if err != nil || !r.Done || r.Value != "id01" {
		t.Errorf("Enter result %+v %v", r, err)
	}
	if r, _ := c.HandleInput("\r"); r.Done {
		t.Error("a finished picker must ignore later input (finish() runs once)")
	}
	esc := &quotePickerComponent{picker: newTestPicker()}
	if r, _ := esc.HandleInput("\x1b"); !r.Done || r.Value != nil {
		t.Errorf("Escape result %+v", r)
	}
}

func TestFormatTimestamp(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)
	same := time.Date(2026, 10, 20, 21, 5, 0, 0, time.Local).UnixMilli()
	other := time.Date(2026, 3, 4, 0, 7, 0, 0, time.Local).UnixMilli()
	if got := formatTimestamp(same, now); got != "09:05 PM" {
		t.Errorf("same day: %q", got)
	}
	if got := formatTimestamp(other, now); got != "3/4/2026 12:07 AM" {
		t.Errorf("another day: %q", got)
	}
}

func TestPickerScrollsToTheSelection(t *testing.T) {
	p := newTestPicker()
	for range 9 {
		p.HandleInput("\x1b[B")
	}
	got := strings.Join(p.Render(90), "\n")
	for _, want := range []string{"id04", "id09", "id11"} {
		if !strings.Contains(got, want) {
			t.Errorf("the window around row 10 lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "id03") || !strings.Contains(got, "▸ user id09") && !strings.Contains(got, "▸ assistant id09") || !strings.Contains(got, "10/12") {
		t.Errorf("window or indicator wrong:\n%s", got)
	}
}

func TestPickerScrollIndicatorRoundsHalvesUp(t *testing.T) {
	var candidates []quoteCandidate
	for i := range 20 {
		candidates = append(candidates, quoteCandidate{ID: fmt.Sprintf("i%02d", i), Role: "user", Text: "t", SearchText: "i user t"})
	}
	p := newQuotePicker(candidates, pickerPlainTheme{}, time.Now, nil, nil)
	p.HandleInput("\x1b[B")
	p.HandleInput("\x1b[B") // 3 of 20: 1.5 dots, Math.round gives 2
	if got := strings.Join(p.Render(90), "\n"); !strings.Contains(got, "● ● ○ ○ ○ ○ ○ ○ ○ ○  3/20") {
		t.Errorf("indicator:\n%s", got)
	}
}

func TestPickerSmallWidthsAndEmptyList(t *testing.T) {
	p := newQuotePicker(nil, pickerPlainTheme{}, time.Now, nil, nil)
	lines := p.Render(1) // innerWidth 0
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	if got := strings.Join(newQuotePicker(nil, pickerPlainTheme{}, time.Now, nil, nil).Render(60), "\n"); !strings.Contains(got, "No matching messages") {
		t.Errorf("an empty list:\n%s", got)
	}
	if s, c := p.HandleInput("\r"); s != nil || c {
		t.Errorf("Enter on an empty list: %v %v", s, c)
	}
	p.HandleInput("\x1b[A")
	p.HandleInput("\x1b[B")
}

func TestFuzzyMatchScoresAreThoseOfPiTui(t *testing.T) {
	// Values computed by pi-tui's fuzzy.js (fuzzyMatch) for the same inputs.
	for _, c := range []struct {
		query, text string
		matches     bool
		score       float64
	}{
		{"quick", "abc111 user the quick fox", true, -51},
		{"quick", "zzz user quick", true, -54.5},
		{"abc", "abc111 user", true, -39.699999999999996},
		{"a1", "1a xx", true, -19.9},
		{"12abc", "abc12 foo", true, -79},
		{"ab", "a-b", true, -22.8},
		{"ab", "axxb", true, -10.7},
		{"abc", "aXbXc abc", true, -10.4},
		{"id05", "id05 user message", true, -59.4},
		{"usr", "id01 user x", true, -11.1},
		{"x", "abc", false, 0},
		{"abc", "ab", false, 0},
		{"tool", "tool", true, -159.4},
		{"é", "café", true, 0.30000000000000004},
		{"a b", "a b", true, -149.7},
	} {
		m := fuzzyMatch(c.query, c.text)
		if m.matches != c.matches || math.Abs(m.score-c.score) > 1e-9 {
			t.Errorf("fuzzyMatch(%q, %q) = %+v, pi-tui gives matches=%v score=%v", c.query, c.text, m, c.matches, c.score)
		}
	}
}

func TestCandidateDetails(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 1500)
	entry := messageEntry("ID1", "2026-10-20T10:00:01.500Z", "assistant", long)
	c, err := buildCandidate(entry, now)
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if utf16Len(c.SearchText) != 1000 || !strings.HasPrefix(c.SearchText, "id1 assistant xxx") {
		t.Errorf("search text %d %q", utf16Len(c.SearchText), c.SearchText[:20])
	}
	if c.TimestampMs != time.Date(2026, 10, 20, 10, 0, 1, 500_000_000, time.UTC).UnixMilli() {
		t.Errorf("an entry timestamp is read as ISO: %d", c.TimestampMs)
	}
	entry["message"].(map[string]any)["timestamp"] = float64(1234)
	if c, _ := buildCandidate(entry, now); c.TimestampMs != 1234 {
		t.Errorf("the message timestamp wins: %d", c.TimestampMs)
	}
	delete(entry["message"].(map[string]any), "timestamp")
	entry["timestamp"] = "not a date"
	if c, _ := buildCandidate(entry, now); c.TimestampMs != now.UnixMilli() {
		t.Errorf("an unreadable timestamp falls back to now: %d", c.TimestampMs)
	}
	opts := &fakeQuoteCtx{mode: "rpc", branch: []map[string]any{messageEntry("o1", "2026-10-20T10:00:01.000Z", "user", strings.Repeat("word ", 100))}}
	_ = reply("", opts)
	if got, want := opts.selectOptions[0], "o1 user: "+strings.TrimSpace(strings.Repeat("word ", 44))[:219]+"…"; got != want {
		t.Errorf("option %q, want %q", got, want)
	}
}
