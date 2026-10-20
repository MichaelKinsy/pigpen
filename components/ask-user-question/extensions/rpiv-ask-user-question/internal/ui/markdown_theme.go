package ui

import (
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// GetMarkdownTheme is the Go equivalent of pi-coding-agent's getMarkdownTheme(): a pi-tui MarkdownTheme whose
// callbacks colour through the given Theme's md* tokens. upstream: theme.js getMarkdownTheme.
//
// Deviation (needs an owner decision): pi's highlightCode colours fenced code of a language known to highlight.js
// through cli-highlight. That is not reproduced; HighlightCode stays nil, so every code block renders as pi does
// for a block without a valid language (each line through the mdCodeBlock colour). Output differs from pi only for
// fenced blocks that name a highlightable language.
func GetMarkdownTheme(theme Theme) *tui.MarkdownTheme {
	fg := func(token string) func(string) string {
		return func(text string) string { return theme.Fg(token, text) }
	}
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
	}
}
