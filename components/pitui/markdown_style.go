package tui

// Ports packages/tui/src/components/markdown.ts (style contexts and invalidation).

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

func markdownForeground(open, text string) string {
	if open == "" {
		return text
	}
	return open + text + FgClose(open)
}
func markdownDecoration(open, close, text string) string {
	if text == "" {
		return ""
	}
	return ansiSpan(open, close, text)
}

// plainMarkdownTheme is used when a Markdown has no theme: pi-tui requires
// one, here every style is the identity.
func plainMarkdownTheme() MarkdownTheme {
	id := func(s string) string { return s }
	return MarkdownTheme{Heading: id, Link: id, LinkUrl: id, Code: id, CodeBlock: id, CodeBlockBorder: id,
		Quote: id, QuoteBorder: id, Hr: id, ListBullet: id, Bold: id, Italic: id, Strikethrough: id, Underline: id}
}
func (m *Markdown) markdownTheme() MarkdownTheme {
	if m.theme != nil {
		return *m.theme
	}
	return plainMarkdownTheme()
}
func markdownStylePrefix(style func(string) string) string {
	styled := style("\x00")
	before, _, ok := strings.Cut(styled, "\x00")
	if !ok {
		return ""
	}
	return before
}
func (m *Markdown) defaultInlineStyleContext() inlineStyleContext {
	if m.styleContext != nil {
		return *m.styleContext
	}
	if m.styleOwner != nil {
		return m.styleOwner.defaultInlineStyleContext()
	}
	if !m.hasDefaultStylePrefix {
		m.defaultStylePrefix = markdownStylePrefix(m.applyDefaultStyle)
		m.hasDefaultStylePrefix = true
	}
	return inlineStyleContext{applyText: m.applyDefaultStyle, stylePrefix: m.defaultStylePrefix}
}
func (m *Markdown) childMarkdown(content string) *Markdown {
	child := NewMarkdownWithOptions(content, 0, 0, m.theme, m.defaultTextStyle, &m.options)
	child.Transform = nil
	child.defaultColor, child.defaultColorSet = m.defaultColor, m.defaultColorSet
	child.defaultItalic = m.defaultItalic
	child.styleContext = m.styleContext
	child.styleOwner = m
	if m.styleOwner != nil {
		child.styleOwner = m.styleOwner
	}
	return child
}
func (m *Markdown) latexEnabled() bool { return m.options.RenderLatex == nil || *m.options.RenderLatex }

func (m *Markdown) renderQuote(content string, width int) []string {
	theme := m.markdownTheme()
	quoteStyle := func(text string) string { return theme.Quote(theme.Italic(text)) }
	prefix := markdownStylePrefix(quoteStyle)
	child := m.childMarkdown(content)
	child.styleContext = &inlineStyleContext{applyText: func(s string) string { return s }, stylePrefix: prefix}
	contentWidth := max(1, width-2)
	logical := child.renderContent(content, contentWidth)
	var out []string
	for _, line := range logical {
		if prefix != "" {
			line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+prefix)
		}
		for _, wrapped := range widthx.WrapTextWithAnsi(quoteStyle(line), contentWidth) {
			out = append(out, theme.QuoteBorder("│ ")+wrapped)
		}
	}
	return out
}
