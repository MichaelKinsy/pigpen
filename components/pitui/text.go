package tui

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Text displays multi-line text with word wrapping. Port of pi-tui's Text.
type Text struct {
	text       string
	paddingX   int
	paddingY   int
	customBgFn func(string) string

	cachedText  string
	cachedWidth int
	cachedLines []string
	cacheValid  bool
}

// NewText mirrors `new Text(text, paddingX, paddingY, customBgFn)`; pi-tui's
// defaults are paddingX=1, paddingY=1 and no background (pass nil).
func NewText(text string, paddingX, paddingY int, customBgFn func(string) string) *Text {
	return &Text{text: text, paddingX: paddingX, paddingY: paddingY, customBgFn: customBgFn}
}

// SetText replaces the text.
func (t *Text) SetText(text string) { t.text = text; t.Invalidate() }

// SetCustomBgFn replaces the background function.
func (t *Text) SetCustomBgFn(fn func(string) string) { t.customBgFn = fn; t.Invalidate() }

// SetPaddingX changes horizontal padding.
func (t *Text) SetPaddingX(paddingX int) { t.paddingX = paddingX; t.Invalidate() }

// Invalidate drops the render cache.
func (t *Text) Invalidate() { t.cacheValid = false; t.cachedLines = nil }

func applyBackgroundToLine(line string, width int, bgFn func(string) string) string {
	pad := max(0, width-widthx.VisibleWidth(line))
	return bgFn(line + strings.Repeat(" ", pad))
}

// Render wraps, pads and (optionally) paints the text.
func (t *Text) Render(width int) []string {
	if t.cacheValid && t.cachedText == t.text && t.cachedWidth == width {
		return t.cachedLines
	}
	if t.text == "" || strings.TrimSpace(t.text) == "" {
		t.cachedText, t.cachedWidth, t.cachedLines, t.cacheValid = t.text, width, []string{}, true
		return t.cachedLines
	}
	normalized := strings.ReplaceAll(t.text, "\t", "   ")
	paddingX := min(t.paddingX, max(0, (width-1)/2))
	contentWidth := max(1, width-paddingX*2)
	wrapped := widthx.WrapTextWithAnsi(normalized, contentWidth)
	margin := strings.Repeat(" ", paddingX)
	content := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		withMargins := margin + line + margin
		if t.customBgFn != nil {
			content = append(content, applyBackgroundToLine(withMargins, width, t.customBgFn))
		} else {
			pad := max(0, width-widthx.VisibleWidth(withMargins))
			content = append(content, withMargins+strings.Repeat(" ", pad))
		}
	}
	emptyLine := strings.Repeat(" ", max(width, 0))
	if t.customBgFn != nil {
		emptyLine = applyBackgroundToLine(emptyLine, width, t.customBgFn)
	}
	result := make([]string, 0, len(content)+2*t.paddingY)
	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}
	result = append(result, content...)
	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}
	t.cachedText, t.cachedWidth, t.cachedLines, t.cacheValid = t.text, width, result, true
	if len(result) > 0 {
		return result
	}
	return []string{""}
}
