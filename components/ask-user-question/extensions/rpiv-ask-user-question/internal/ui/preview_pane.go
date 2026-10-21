package ui

import (
	"errors"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// PreviewPaneProps is the per-tick projection of PreviewPane state. upstream: PreviewPaneProps. InputMode is true
// while the "other" row is focused and accepting input; it drives the full-width early returns.
type PreviewPaneProps struct {
	NotesVisible  bool
	SelectedIndex int
	Focused       bool
	InputMode     bool
}

// PreviewPaneConfig configures NewPreviewPane.
type PreviewPaneConfig struct {
	Question         QuestionData
	GetTerminalWidth func() int
	OptionListView   *OptionListView
	PreviewBlock     *PreviewBlockRenderer
}

// PreviewPane is the thin layout composer of option list and preview block. upstream: PreviewPane.
type PreviewPane struct {
	question         QuestionData
	getTerminalWidth func() int
	optionListView   *OptionListView
	previewBlock     *PreviewBlockRenderer
	props            PreviewPaneProps
	// globalLeftWidth is nil until SetGlobalLeftWidth: render is illegal before injection (upstream throws).
	globalLeftWidth func(paneWidth int) int
}

// ErrGlobalLeftWidthNotSet is the failure of rendering before SetGlobalLeftWidth; upstream throws this message.
var ErrGlobalLeftWidthNotSet = errors.New("PreviewPane.setGlobalLeftWidth must be called before render()")

func NewPreviewPane(config PreviewPaneConfig) *PreviewPane {
	return &PreviewPane{
		question: config.Question, getTerminalWidth: config.GetTerminalWidth,
		optionListView: config.OptionListView, previewBlock: config.PreviewBlock,
	}
}

func (p *PreviewPane) SetGlobalLeftWidth(getter func(paneWidth int) int) { p.globalLeftWidth = getter }

// getAdaptiveLeft panics with ErrGlobalLeftWidthNotSet before injection: the original throws, and a missing
// injection is a programming error in buildQuestionnaire, never a runtime condition.
func (p *PreviewPane) getAdaptiveLeft(paneWidth int) int {
	if p.globalLeftWidth == nil {
		panic(ErrGlobalLeftWidthNotSet)
	}
	return p.globalLeftWidth(paneWidth)
}

func (p *PreviewPane) SetProps(props PreviewPaneProps) { p.props = props }
func (p *PreviewPane) HandleInput(data string)         {}

func (p *PreviewPane) Invalidate() {
	p.previewBlock.Invalidate()
	p.optionListView.Invalidate()
}

// fullWidthOnly reports the cases that render the option list alone at the full pane width.
func (p *PreviewPane) fullWidthOnly() bool {
	return p.question.MultiSelect || !p.previewBlock.HasAnyPreview() || p.props.InputMode
}

func (p *PreviewPane) Render(width int) []string {
	if p.fullWidthOnly() {
		return p.optionListView.Render(width)
	}
	mode := DecideLayout(p.getTerminalWidth(), width)
	if mode == LayoutSideBySide {
		return p.renderSideBySide(width, mode)
	}
	out := append([]string{}, p.optionListView.Render(width)...)
	for range StackedGapRows {
		out = append(out, "")
	}
	return append(out, p.previewBlock.RenderBlock(width, p.props.SelectedIndex, mode, p.props.Focused, p.props.NotesVisible)...)
}

func (p *PreviewPane) FocusedItemRowRange(width int) (int, int) {
	if p.fullWidthOnly() {
		return p.optionListView.FocusedItemRowRange(width)
	}
	if DecideLayout(p.getTerminalWidth(), width) == LayoutStacked {
		return p.optionListView.FocusedItemRowRange(width)
	}
	leftWidth, _, _ := ColumnWidths(width, p.getAdaptiveLeft(width))
	return p.optionListView.FocusedItemRowRange(leftWidth)
}

func (p *PreviewPane) NaturalHeight(width int) int {
	if p.fullWidthOnly() {
		return len(p.optionListView.Render(width))
	}
	mode := DecideLayout(p.getTerminalWidth(), width)
	optionsWidth, previewWidth := BodyWidths(width, mode, p.getAdaptiveLeft(width))
	optionsHeight := len(p.optionListView.Render(optionsWidth))
	previewBlockHeight := p.previewBlock.BlockHeight(previewWidth, p.props.SelectedIndex, mode)
	if mode == LayoutSideBySide {
		return max(optionsHeight, previewBlockHeight)
	}
	return optionsHeight + StackedGapRows + previewBlockHeight
}

func (p *PreviewPane) MaxNaturalHeight(width int) int {
	if p.fullWidthOnly() {
		return len(p.optionListView.Render(width))
	}
	mode := DecideLayout(p.getTerminalWidth(), width)
	optionsWidth, previewWidth := BodyWidths(width, mode, p.getAdaptiveLeft(width))
	optionsHeight := len(p.optionListView.Render(optionsWidth))
	maxPreviewBlock := 0
	for i := range p.question.Options {
		if h := p.previewBlock.BlockHeight(previewWidth, i, mode); h > maxPreviewBlock {
			maxPreviewBlock = h
		}
	}
	if mode == LayoutSideBySide {
		return max(optionsHeight, maxPreviewBlock)
	}
	return optionsHeight + StackedGapRows + maxPreviewBlock
}

func (p *PreviewPane) renderSideBySide(width int, mode PreviewLayoutMode) []string {
	leftWidth, rightWidth, gap := ColumnWidths(width, p.getAdaptiveLeft(width))
	leftLines := p.optionListView.Render(leftWidth)
	rightLines := p.renderPaddedPreviewLines(rightWidth, mode)
	rows := max(len(leftLines), len(rightLines))
	gapStr := strings.Repeat(" ", gap)
	out := make([]string, 0, rows)
	for i := range rows {
		leftRaw, rightRaw := "", ""
		if i < len(leftLines) {
			leftRaw = leftLines[i]
		}
		if i < len(rightLines) {
			rightRaw = rightLines[i]
		}
		leftClamped := widthx.TruncateToWidth(leftRaw, leftWidth, "", false)
		leftPad := strings.Repeat(" ", max(0, leftWidth-widthx.VisibleWidth(leftClamped)))
		joined := leftClamped + leftPad + gapStr + rightRaw
		out = append(out, widthx.TruncateToWidth(joined, width, "", false))
	}
	return out
}

func (p *PreviewPane) renderPaddedPreviewLines(colWidth int, mode PreviewLayoutMode) []string {
	inner := max(1, colWidth-PreviewPaddingLeft)
	contentLines := p.previewBlock.RenderBlock(inner, p.props.SelectedIndex, mode, p.props.Focused, p.props.NotesVisible)
	first := ""
	if len(contentLines) > 0 {
		first = contentLines[0]
	}
	boxWidth := max(1, widthx.VisibleWidth(first))
	boxAlignedPad := max(PreviewPaddingLeft, colWidth-boxWidth)
	out := make([]string, len(contentLines))
	for i, line := range contentLines {
		if line == "" {
			continue
		}
		// A line wider than the box slides left to stay fully visible; truncation engages only when the column
		// itself runs out.
		pad := max(PreviewPaddingLeft, min(boxAlignedPad, colWidth-widthx.VisibleWidth(line)))
		out[i] = strings.Repeat(" ", pad) + widthx.TruncateToWidth(line, colWidth-pad, "", false)
	}
	return out
}
