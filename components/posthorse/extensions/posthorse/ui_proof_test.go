package posthorse

// Proofs of the UI port against values the original produces (Node 24, Pi 1.5.0), independent of the generated
// differential cases of ui_differential_test.go.

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// text.ts:9-12 (clean) and cards.ts:104, 246 (`.replace(/\s+/g, " ")`): a page cut through an astral character (notes.ts and
// history-pages.ts slice by UTF-16 units) holds a lone surrogate, which the original's regexes leave alone. The SDK's
// WTF-8 form (ED A0..BF xx) must come through clean and collapse unchanged, whatever else the text holds (controls, U+0080-U+00BF,
// U+F000-U+FFFF characters).
func TestUiCleanTextLoneSurrogate(t *testing.T) {
	high := "\xed\xa0\xbd" // the high half of U+1F600 in the SDK's WTF-8 form
	for _, in := range []string{"plain " + high, "页面结束，" + high, "a · b " + high, "❤️ love " + high} {
		if got := cleanText(in); got != in {
			t.Errorf("cleanText(%q) = %q; the original keeps the lone unit", in, got)
		}
		if got := uiCollapseSpaces(in); got != in {
			t.Errorf("uiCollapseSpaces(%q) = %q; the original keeps the lone unit", in, got)
		}
	}
}

// display.ts:231 prints `${Math.round(usage.percent)}`: no grouping, JS rounding (0.49999999999999994 is 0, 999.5 is 1000).
func TestUiContextSummaryPercent(t *testing.T) {
	for _, tc := range []struct {
		tokens, window, percent float64
		want                    string
	}{
		{200000, 16000, 1250.4, "≈200,000 / 16,000 used (≈1250%)"},
		{1, 2, 999.5, "≈1 / 2 used (≈1000%)"},
		{1, 2, 0.49999999999999994, "≈1 / 2 used (≈0%)"},
	} {
		tokens, percent := tc.tokens, tc.percent
		d := &posthorseDisplay{kind: "context", usage: true, usageTokens: &tokens, usageWindow: tc.window, usagePercent: &percent, rollover: "disabled"}
		got := contextSummary(d)
		if !strings.HasSuffix(got, "\n"+tc.want) {
			t.Errorf("percent %v: got %q, want last line %q", tc.percent, got, tc.want)
		}
	}
}

// display.ts:209 / cards.ts:30 `value.toLocaleString("en-US")` formats the shortest decimal of the double and rounds half-expand
// to three fraction digits (Node 24 values). Reachable from model-supplied call arguments (cards.ts:69-73 labels).
func TestUiLocaleNumberShortest(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		want string
	}{
		{0.0625, "0.063"},
		{1.0005, "1.001"},
		{12345678901234567890, "12,345,678,901,234,567,000"},
		{1e23, "100,000,000,000,000,000,000,000"},
	} {
		if got := n(tc.v); got != tc.want {
			t.Errorf("n(%v) = %q, want %q (Node 24 toLocaleString)", tc.v, got, tc.want)
		}
	}
}

// cards.ts:186-191 compares `headerLength > 0` on the number and slices with ToIntegerOrInfinity: a saved 0.5 keeps the
// "… to expand" hint, and 1e19 slices the whole body away.
func TestUiNoteHeaderLengthNonInteger(t *testing.T) {
	for _, hl := range []float64{0.5, 1e19} {
		h := hl
		facts := &cardResultFacts{name: "notes", raw: "Notes directory: /d\na.md", args: map[string]any{"op": "list"},
			display: &posthorseDisplay{kind: "notes-list", count: 2, headerLength: &h}, expandKey: "ctrl+o"}
		if cardNoteHeaderLength(facts) <= 0 {
			t.Errorf("headerLength %v: noteHeaderLength(facts) > 0 is true in the original", hl)
		}
	}
}

// display.ts:183-189 slices spans by UTF-16 units, so a span boundary inside a surrogate pair leaves one half in the
// header and the other at the start of the body (WTF-8 lone surrogates).
func TestUiHistorySectionsSplitPair(t *testing.T) {
	d := displayOf(map[string]any{"kind": "history-search", "entries": []any{
		map[string]any{"headerLength": 2.0, "length": 4.0}, map[string]any{"headerLength": 0.0, "length": 1.0}}})
	s := historySections("a😀b\nc", d)
	if len(s) != 2 || s[0].body != "\xed\xb8\x80b" {
		t.Errorf("sections %q: the original's first body is the low surrogate + \"b\"", s)
	}
}

// boundaryBetween answers "certain boundary" only: every pair the UAX #29 rules glue answers false.
func TestUiBoundaryBetween(t *testing.T) {
	for _, tc := range []struct {
		a, b rune
		want bool
	}{
		{' ', 'a', true}, {'я', ' ', true}, {'界', '界', true}, {'ก', 'ข', true}, {'한', ' ', true}, {'👩', ' ', true}, {' ', '👩', true},
		{'a', 0x0301, false},      // GB9 Extend
		{'e', 0xfe0f, false},      // variation selector
		{0x200d, '👩', false},      // GB11 after ZWJ
		{'👩', 0x200d, false},      // GB9 ZWJ
		{'👩', 0x1f3fd, false},     // emoji modifier
		{0x1f1fa, 0x1f1f8, false}, // GB12 regional indicators
		{0x0e01, 0x0e33, false},   // SARA AM, a SpacingMark
		{0x0915, 0x094d, false},   // Devanagari virama (Extend)
		{0x094d, 0x0937, false},   // GB9c: after a Linker
		{0x0600, 'a', false},      // GB9b Prepend
		{0x0d4e, 'a', false},      // Prepend outside Cf
		{0x1100, 0x1161, false},   // GB6 Hangul L V
		{0xac00, 0x11a8, false},   // GB7 LV T
		{'\r', '\n', false},       // GB3
		{0xd83d, 'a', false},      // a lone surrogate may pair
		{'a', 0xe0067, false},     // tag characters extend
		{0x1f3f4, 0xe0067, false},
	} {
		if got := boundaryBetween(tc.a, tc.b); got != tc.want {
			t.Errorf("boundaryBetween(%U, %U) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// The collapsed preview of a long single-line page shows the rows of the whole text, in every script, at every width,
// and wraps a bounded prefix (text.ts:45-81).
func TestUiLeadingRowsScripts(t *testing.T) {
	theme := sdk.UITheme{}
	for _, word := range []string{"route station ", "станция маршрут ", "ภาษาไทยเป็นภาษา ", "مرحبا بالعالم، ", "한국어 텍스트 ", "👩🏽‍💻 ", "क्षत्रिय परिवार ", "🇺🇸🇯🇵 ", "e\u0301\u0302 ", "界界界界", "ก็ก็ก็ "} {
		page := uiDiffPage(word)
		for _, width := range []int{1, 2, 7, 24, 100} {
			got := cardLeadingRows(page, theme, colorToolOutput, 3)(width)
			want := piText(theme.Fg(colorToolOutput, page), width)
			if len(got) < 4 || len(want) < 4 {
				t.Fatalf("%q width %d: %d/%d rows", word, width, len(got), len(want))
			}
			if strings.Join(got[:3], "\n") != strings.Join(want[:3], "\n") {
				t.Errorf("%q width %d: first rows differ\n got %q\nwant %q", word, width, got[:3], want[:3])
			}
		}
		cut := safeCut(page, 1424)
		if units := u16len(page[:cut]); units > 1424+64 {
			t.Errorf("%q: safeCut at unit %d, the original cuts at 1,424", word, units)
		}
	}
}
