package powerline_footer

import "testing"

// visibleWidth decides which segments fit the bar, so it must measure as pi-tui's visibleWidth does (Pi 1.0.0,
// @earendil-works/pi-tui 1.0.0 utils.js: a tab is 3 cells, a control or format character none, and only CSI sequences ending in
// m, G, K, H or J, OSC and APC sequences are escape codes). The widths below were printed by pi-tui 1.0.0 itself.
func TestVisibleWidthMeasuresAsPiTui(t *testing.T) {
	for _, c := range []struct {
		text  string
		width int
	}{
		{"a\tb", 5},
		{"x\x1b]0;T\x07y", 2},
		{"x\x1b]0;T\x1b\\y", 2},
		{"apc\x1b_hidden\x1b\\x", 4},
		{"apc\x1b_hidden\x07x", 4},
		{"line1\r\nline2", 10},
		{"up\x1b[2Ax", 6},
		{"\x1b[2Jcls", 3},
		{"esc\x1bz", 4},
		{"bell\x07", 4},
		{"n\x00u\x7fl", 3},
		{"c1\u0085\u009bz", 3},
		{"soft\u00adhy", 6},
		{"e\u0301e\u0301", 2},
		{"\u6f22\u5b57", 4},
		{"\u115f", 0},
		{"\ufeffbom", 3},
		{"\u200bzw", 2},
		{"\x1b[31mred\x1b[0m", 3},
		{"\x1b[1;31Hpos", 3},
		{"\x1b[?25lhide", 9},
	} {
		if got := visibleWidth(c.text); got != c.width {
			t.Errorf("visibleWidth(%q) = %d, pi-tui says %d", c.text, got, c.width)
		}
	}
}

// strictTheme stands in for Pi's Theme.fg, which throws on a token it does not know (the original then falls back to "text").
// The tokens are Pi 1.0.0's ThemeColor (pi-coding-agent 1.0.0, dist/modes/interactive/theme/theme.d.ts).
func TestStrictThemeKnowsPi100Tokens(t *testing.T) {
	for _, token := range []string{"accent", "border", "borderAccent", "borderMuted", "success", "error", "warning", "muted", "dim", "text", "thinkingText", "scrollbarTrack", "scrollbarThumb", "searchMatchText", "userMessageText", "customMessageText", "customMessageLabel", "toolTitle", "toolOutput", "mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock", "mdCodeBlockBorder", "mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet", "toolDiffAdded", "toolDiffRemoved", "toolDiffContext", "syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable", "syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation", "thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium", "thinkingHigh", "thinkingXhigh", "thinkingMax", "bashMode"} {
		if _, err := (strictTheme{}).Fg(token, "x"); err != nil {
			t.Errorf("Pi 1.0.0 knows %q: %v", token, err)
		}
	}
	if _, err := (strictTheme{}).Fg("nonsense", "x"); err == nil {
		t.Error("an unknown token must fail, as Theme.fg throws")
	}
}
