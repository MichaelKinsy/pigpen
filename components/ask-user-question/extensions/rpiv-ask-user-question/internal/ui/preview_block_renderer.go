package ui

import (
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// NotesAffordanceText is shown below the bordered preview when focused on a preview-bearing option.
const NotesAffordanceText = "Notes: press n to add notes"

func contentBudgetFor(mode PreviewLayoutMode) int {
	cap := MaxPreviewHeightStacked
	if mode == LayoutSideBySide {
		cap = MaxPreviewHeightSideBySide
	}
	return max(1, cap-BorderVerticalOverhead-NotesAffordanceOverhead)
}

func innerWidthFor(width int) int {
	return max(1, width-BorderHorizontalOverhead-2*BorderInnerPaddingHorizontal)
}

// PreviewBlockRendererConfig configures NewPreviewBlockRenderer.
type PreviewBlockRendererConfig struct {
	Question      QuestionData
	Theme         Theme
	MarkdownTheme *tui.MarkdownTheme
}

// PreviewBlockRenderer renders the bordered markdown preview block for one question. Not a Component: a pure
// render-and-measure helper consumed by PreviewPane. upstream: PreviewBlockRenderer.
type PreviewBlockRenderer struct {
	theme Theme
	cache *MarkdownContentCache
}

func NewPreviewBlockRenderer(config PreviewBlockRendererConfig) *PreviewBlockRenderer {
	return &PreviewBlockRenderer{theme: config.Theme, cache: NewMarkdownContentCache(config.Question, config.Theme, config.MarkdownTheme)}
}

func (r *PreviewBlockRenderer) HasAnyPreview() bool      { return r.cache.HasAnyPreview() }
func (r *PreviewBlockRenderer) Has(optionIndex int) bool { return r.cache.Has(optionIndex) }
func (r *PreviewBlockRenderer) Invalidate()              { r.cache.Invalidate() }

// BlockHeight is BorderVerticalOverhead + contentRows + NotesAffordanceOverhead; always equals len(RenderBlock).
func (r *PreviewBlockRenderer) BlockHeight(width, optionIndex int, mode PreviewLayoutMode) int {
	contentBudget := contentBudgetFor(mode)
	rawRows := len(r.cache.BodyFor(optionIndex, innerWidthFor(width)))
	return BorderVerticalOverhead + min(rawRows, contentBudget) + NotesAffordanceOverhead
}

// RenderBlock renders bordered box + blank separator + affordance row (always emitted, empty when gated).
func (r *PreviewBlockRenderer) RenderBlock(width, optionIndex int, mode PreviewLayoutMode, focused, notesVisible bool) []string {
	contentBudget := contentBudgetFor(mode)
	maxInnerWidth := innerWidthFor(width)

	raw := r.cache.BodyFor(optionIndex, maxInnerWidth)
	truncated := len(raw) > contentBudget
	hidden := 0
	contentLines := raw
	if truncated {
		hidden = len(raw) - contentBudget
		contentLines = raw[:contentBudget]
	}

	_, boxWidth := ComputeBoxDimensions(contentLines, maxInnerWidth)
	colorFn := func(s string) string { return r.theme.Fg("accent", s) }
	boxedLines := RenderBorderedBox(contentLines, boxWidth, colorFn, hidden)

	affordance := ""
	if focused && !notesVisible && r.cache.Has(optionIndex) {
		affordance = r.theme.Fg("muted", T("preview.notes_affordance", NotesAffordanceText))
	}
	out := make([]string, 0, len(boxedLines)+2)
	out = append(out, boxedLines...)
	return append(out, "", affordance)
}
