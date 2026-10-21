package ui

import (
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

const (
	// MaxPreviewHeightSideBySide is CC parity in side-by-side layout.
	MaxPreviewHeightSideBySide = 20
	// MaxPreviewHeightStacked preserves narrow-terminal protection in stacked layout.
	MaxPreviewHeightStacked = 15
	NoPreviewText           = "No preview available"
	// NotesAffordanceOverhead is 1 blank separator + 1 affordance text row.
	NotesAffordanceOverhead = 2
)

// markdownRenderer is the part of pi-tui's Markdown the cache uses.
type markdownRenderer interface {
	Render(width int) []string
	Invalidate()
}

// newMarkdownRenderer builds the Markdown for one preview (`new Markdown(text, 0, 0, theme)`); a package variable
// so tests can count constructions, as the original's tests mock Markdown.
var newMarkdownRenderer = func(text string, theme *tui.MarkdownTheme) markdownRenderer {
	return tui.NewMarkdownWithOptions(text, 0, 0, theme, nil, nil)
}

// MarkdownContentCache is the per-question cache of rendered markdown previews: one Markdown per option, lazy on
// first request, never re-constructed. upstream: MarkdownContentCache.
type MarkdownContentCache struct {
	previewTexts  map[int]string
	markdownCache map[int]markdownRenderer
	cachedWidth   int
	hasWidth      bool
	theme         Theme
	markdownTheme *tui.MarkdownTheme
}

func NewMarkdownContentCache(question QuestionData, theme Theme, markdownTheme *tui.MarkdownTheme) *MarkdownContentCache {
	c := &MarkdownContentCache{
		theme: theme, markdownTheme: markdownTheme,
		previewTexts:  map[int]string{},
		markdownCache: map[int]markdownRenderer{},
	}
	for i, o := range question.Options {
		if o.Preview != "" {
			c.previewTexts[i] = o.Preview
		}
	}
	return c
}

func (c *MarkdownContentCache) HasAnyPreview() bool { return len(c.previewTexts) > 0 }

func (c *MarkdownContentCache) Has(optionIndex int) bool {
	_, ok := c.previewTexts[optionIndex]
	return ok
}

// BodyFor computes the body lines for an option at an inner width; a width change invalidates every cached render.
func (c *MarkdownContentCache) BodyFor(optionIndex, innerWidth int) []string {
	if !c.hasWidth || c.cachedWidth != innerWidth {
		for _, md := range c.markdownCache {
			md.Invalidate()
		}
		c.cachedWidth, c.hasWidth = innerWidth, true
	}
	text, ok := c.previewTexts[optionIndex]
	if !ok {
		placeholder := c.theme.Fg("dim", T("preview.no_preview", NoPreviewText))
		pad := max(0, innerWidth-widthx.VisibleWidth(placeholder))
		return []string{placeholder + strings.Repeat(" ", pad)}
	}
	md, ok := c.markdownCache[optionIndex]
	if !ok {
		md = newMarkdownRenderer(text, c.markdownTheme)
		c.markdownCache[optionIndex] = md
	}
	return StripFenceMarkers(md.Render(innerWidth))
}

func (c *MarkdownContentCache) Invalidate() {
	for _, md := range c.markdownCache {
		md.Invalidate()
	}
	c.hasWidth = false
}
