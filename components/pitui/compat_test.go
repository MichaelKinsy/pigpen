package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Test shims for PiG helpers whose subjects were pruned or reshaped.

func editorContentRows(rows []string) []string {
	if len(rows) >= 3 {
		return rows[1 : len(rows)-1]
	}
	return rows
}

func stripANSI(s string) string { return widthx.StripAnsi(s) }

func upstreamContains(t *testing.T, text, part string) {
	t.Helper()
	if !strings.Contains(text, part) {
		t.Fatalf("missing %q in %q", part, text)
	}
}

func upstreamExcludes(t *testing.T, text, part string) {
	t.Helper()
	if strings.Contains(text, part) {
		t.Fatalf("unexpected %q in %q", part, text)
	}
}

// TerminalCapabilities/GetCapabilities/SetCapabilities: PiG's capability cache,
// reduced to the one capability pitui keeps (hyperlinks).
type TerminalCapabilities struct {
	Images     string
	TrueColor  bool
	Hyperlinks bool
}

func GetCapabilities() TerminalCapabilities {
	return TerminalCapabilities{Hyperlinks: HyperlinksSupported()}
}
func SetCapabilities(c TerminalCapabilities) { SetHyperlinksSupported(&c.Hyperlinks) }
func ResetCapabilitiesCache()                { SetHyperlinksSupported(nil) }

// testTheme stands in for PiG's ActiveTheme() markdown tokens.
type testThemeTokens struct{ MDHeading, MDLink, MDLinkUrl, MDCode, MDCodeBlock, MDCodeBlockBorder, MDQuote, MDQuoteBorder, MDHr, MDListBullet string }

func ActiveTheme() testThemeTokens {
	return testThemeTokens{
		MDHeading: "\x1b[38;5;1m", MDLink: "\x1b[38;5;2m", MDLinkUrl: "\x1b[38;5;3m", MDCode: "\x1b[38;5;4m",
		MDCodeBlock: "\x1b[38;5;5m", MDCodeBlockBorder: "\x1b[38;5;6m", MDQuote: "\x1b[38;5;7m",
		MDQuoteBorder: "\x1b[38;5;8m", MDHr: "\x1b[38;5;9m", MDListBullet: "\x1b[38;5;10m",
	}
}

func testMarkdownTheme() *MarkdownTheme {
	t := ActiveTheme()
	fg := func(open string) func(string) string {
		return func(s string) string { return markdownForeground(open, s) }
	}
	return &MarkdownTheme{
		Heading: fg(t.MDHeading), Link: fg(t.MDLink), LinkUrl: fg(t.MDLinkUrl), Code: fg(t.MDCode),
		CodeBlock: fg(t.MDCodeBlock), CodeBlockBorder: fg(t.MDCodeBlockBorder), Quote: fg(t.MDQuote),
		QuoteBorder: fg(t.MDQuoteBorder), Hr: fg(t.MDHr), ListBullet: fg(t.MDListBullet),
		Bold:          func(s string) string { return markdownDecoration("\x1b[1m", SGRBoldDimReset, s) },
		Italic:        func(s string) string { return markdownDecoration("\x1b[3m", SGRItalicReset, s) },
		Strikethrough: func(s string) string { return markdownDecoration("\x1b[9m", SGRStrikeReset, s) },
		Underline:     func(s string) string { return markdownDecoration("\x1b[4m", SGRUnderlineReset, s) },
	}
}

func testNewMarkdown(content string) *Markdown {
	return NewMarkdownWithOptions(content, 0, 0, testMarkdownTheme(), nil, nil)
}

func newTestEditor() *Editor {
	return NewEditor(EditorTheme{BorderColor: func(s string) string { return s }})
}

// EditorCursor groups Editor.GetCursor's results the way PiG's tests read them.
type EditorCursor struct{ Line, Col int }

func cursorOf(e *Editor) EditorCursor {
	line, col := e.GetCursor()
	return EditorCursor{Line: line, Col: col}
}

// Clear resets the editor (PiG's Editor.Clear, not part of pi-tui).
func (e *Editor) Clear() {
	if e.GetText() != "" {
		e.saveHistory()
	}
	e.lastAction = ""
	e.lines = []string{""}
	e.cursor = [2]int{0, 0}
	e.jumpMode = ""
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
	e.exitHistoryBrowsing()
	e.pastes = nil
	e.pasteCounter = 0
	e.scrollOffset = 0
	e.notifyChange()
}

// renderEditor renders and resolves fake cursor markers as the host TUI does.
func renderEditor(e *Editor, width int) []string {
	return ResolveFakeCursors(e.Render(width), false)
}
