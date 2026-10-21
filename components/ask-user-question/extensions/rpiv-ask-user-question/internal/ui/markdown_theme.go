package ui

import (
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// LoadHighlightLanguages loads every highlight.js language, as Pi's interactive mode does in the background when it
// starts (utils/syntax-highlight.ts loadAllHighlightLanguages). A variable so the frame harness, which replays
// recordings made without Pi's interactive mode (only the eager languages exist there), can switch the load off.
var LoadHighlightLanguages = tui.LoadAllHighlightLanguages

// GetMarkdownTheme is the Go equivalent of pi-coding-agent's getMarkdownTheme(): a pi-tui MarkdownTheme whose
// callbacks colour through the given Theme's md* tokens, and whose highlightCode colours fenced code of a language
// highlight.js knows through the theme's syntax* tokens (cli-highlight's theme, built as theme.js
// buildCliHighlightTheme builds it). upstream: theme.js getMarkdownTheme.
func GetMarkdownTheme(theme Theme) *tui.MarkdownTheme {
	fg := func(token string) func(string) string {
		return func(text string) string { return theme.Fg(token, text) }
	}
	cli := cliHighlightTheme(theme)
	return &tui.MarkdownTheme{
		Heading:         fg("mdHeading"),
		Link:            fg("mdLink"),
		LinkUrl:         fg("mdLinkUrl"),
		Code:            fg("mdCode"),
		CodeBlock:       fg("mdCodeBlock"),
		CodeBlockBorder: fg("mdCodeBlockBorder"),
		Quote:           fg("mdQuote"),
		QuoteBorder:     fg("mdQuoteBorder"),
		Hr:              fg("mdHr"),
		ListBullet:      fg("mdListBullet"),
		Bold:            theme.Bold,
		Italic:          theme.Italic,
		Underline:       theme.Underline,
		Strikethrough:   theme.Strikethrough,
		HighlightCode: func(code, lang string) []string {
			// Pi has loaded every highlight.js language by the time a questionnaire opens (interactive mode loads
			// them in the background at startup); a fenced block that renders first waits for that load, once.
			LoadHighlightLanguages()
			return tui.HighlightMarkdownCode(code, lang, cli, fg("mdCodeBlock"))
		},
	}
}

// cliHighlightTheme is theme.js buildCliHighlightTheme: highlight.js scope names to the theme's styles.
func cliHighlightTheme(t Theme) tui.HighlightTheme {
	fg := func(token string) tui.HighlightFormatter {
		return func(s string) string { return t.Fg(token, s) }
	}
	return tui.HighlightTheme{
		"keyword":     fg("syntaxKeyword"),
		"built_in":    fg("syntaxType"),
		"literal":     fg("syntaxNumber"),
		"number":      fg("syntaxNumber"),
		"regexp":      fg("syntaxString"),
		"string":      fg("syntaxString"),
		"subst":       fg("text"),
		"comment":     fg("syntaxComment"),
		"doctag":      fg("syntaxComment"),
		"meta":        fg("muted"),
		"function":    fg("syntaxFunction"),
		"title":       fg("syntaxFunction"),
		"class":       fg("syntaxType"),
		"type":        fg("syntaxType"),
		"tag":         fg("syntaxPunctuation"),
		"name":        fg("syntaxKeyword"),
		"attr":        fg("syntaxVariable"),
		"variable":    fg("syntaxVariable"),
		"params":      fg("syntaxVariable"),
		"operator":    fg("syntaxOperator"),
		"punctuation": fg("syntaxPunctuation"),
		"emphasis":    t.Italic,
		"strong":      t.Bold,
		"link":        t.Underline,
		"addition":    fg("toolDiffAdded"),
		"deletion":    fg("toolDiffRemoved"),
	}
}
