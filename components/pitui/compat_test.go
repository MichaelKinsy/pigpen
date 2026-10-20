package tui

import (
	"reflect"
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

// GetMarkdownTheme is PiG's GetMarkdownTheme with the test ActiveTheme: capture-free style functions, which the
// package recognizes as pure (registered in init as PiG's own theme functions are).
func GetMarkdownTheme() MarkdownTheme {
	return MarkdownTheme{
		Heading:         func(s string) string { return markdownForeground(ActiveTheme().MDHeading, s) },
		Link:            func(s string) string { return markdownForeground(ActiveTheme().MDLink, s) },
		LinkUrl:         func(s string) string { return markdownForeground(ActiveTheme().MDLinkUrl, s) },
		Code:            func(s string) string { return markdownForeground(ActiveTheme().MDCode, s) },
		CodeBlock:       func(s string) string { return markdownForeground(ActiveTheme().MDCodeBlock, s) },
		CodeBlockBorder: func(s string) string { return markdownForeground(ActiveTheme().MDCodeBlockBorder, s) },
		Quote:           func(s string) string { return markdownForeground(ActiveTheme().MDQuote, s) },
		QuoteBorder:     func(s string) string { return markdownForeground(ActiveTheme().MDQuoteBorder, s) },
		Hr:              func(s string) string { return markdownForeground(ActiveTheme().MDHr, s) },
		ListBullet:      func(s string) string { return markdownForeground(ActiveTheme().MDListBullet, s) },
		Bold:            markdownBold,
		Italic:          markdownItalic,
		Strikethrough:   markdownStrikethrough,
		Underline:       func(s string) string { return markdownDecoration("\x1b[4m", SGRUnderlineReset, s) },
	}
}

var testActiveMarkdownTheme = GetMarkdownTheme()

func init() {
	t := testActiveMarkdownTheme
	for _, style := range []func(string) string{t.Heading, t.Link, t.LinkUrl, t.Code, t.CodeBlock, t.CodeBlockBorder, t.Quote, t.QuoteBorder, t.Hr, t.ListBullet, t.Bold, t.Italic, t.Strikethrough, t.Underline} {
		markdownPureStyles[reflect.ValueOf(style).Pointer()] = true
	}
}

func testMarkdownTheme() *MarkdownTheme { t := GetMarkdownTheme(); return &t }

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

// renderEditor is Editor.Render (PiG v0.5.0's tests read the fake-cursor markers directly).
func renderEditor(e *Editor, width int) []string { return e.Render(width) }

// testMDWithOptions is NewMarkdownWithOptions where a nil theme means the test active theme, as in PiG.
func testMDWithOptions(content string, paddingX, paddingY int, theme *MarkdownTheme, style *DefaultTextStyle, options *MarkdownOptions) *Markdown {
	if theme == nil {
		theme = testMarkdownTheme()
	}
	return NewMarkdownWithOptions(content, paddingX, paddingY, theme, style, options)
}
