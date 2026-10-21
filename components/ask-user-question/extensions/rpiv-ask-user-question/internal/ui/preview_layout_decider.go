package ui

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

const (
	// PreviewMinWidth is the min terminal/pane width for the side-by-side layout to engage.
	PreviewMinWidth = 100
	// PreviewColumnGap is the visual gap between options column and preview column in side-by-side.
	PreviewColumnGap = 2
	// PreviewPaddingLeft is the 1 col padding inside the preview column (between gap and `│`).
	PreviewPaddingLeft = 1
	// StackedGapRows is the empty rows between options and preview blocks in stacked layout.
	StackedGapRows = 1

	// MinLeft is the floor for the adaptive left column width.
	MinLeft = 30
	// MaxLeftRatio: the left column never exceeds this fraction of pane width.
	MaxLeftRatio = 0.5
	// MinPreviewWidth is the floor for the preview column width.
	MinPreviewWidth = 45
	// ConfirmedOverhead is visibleWidth(" ✔") = 2, reserved on the longest-label measurement.
	ConfirmedOverhead = 2
)

// PreviewLayoutMode is "side-by-side" or "stacked".
type PreviewLayoutMode string

const (
	LayoutSideBySide PreviewLayoutMode = "side-by-side"
	LayoutStacked    PreviewLayoutMode = "stacked"
)

// DecideLayout decides layout mode from terminal + pane widths. upstream: decideLayout.
func DecideLayout(terminalWidth, paneWidth int) PreviewLayoutMode {
	if terminalWidth >= PreviewMinWidth && paneWidth >= PreviewMinWidth {
		return LayoutSideBySide
	}
	return LayoutStacked
}

// AdaptiveLeftWidth computes the adaptive left column width from option labels. upstream: adaptiveLeftWidth.
func AdaptiveLeftWidth(items []WrappingSelectItem, totalForNumbering, paneWidth int) int {
	prefixW := len(strconv.Itoa(max(1, totalForNumbering))) + 4 // digits + "❯ " + ". "
	maxLabel := 0
	for _, item := range items {
		if w := widthx.VisibleWidth(item.Label); w > maxLabel {
			maxLabel = w
		}
	}
	desired := maxLabel + prefixW + ConfirmedOverhead
	ratioCapped := min(desired, int(float64(paneWidth)*MaxLeftRatio))
	available := paneWidth - PreviewColumnGap - MinPreviewWidth
	return max(MinLeft, min(ratioCapped, max(1, available)))
}

// CrossTabMaxLeftWidth is the widest AdaptiveLeftWidth over every tab (tabCount is `tabs.length`; a missing
// itemsByTab entry is an empty tab). upstream: crossTabMaxLeftWidth.
func CrossTabMaxLeftWidth(tabCount int, itemsByTab [][]WrappingSelectItem, paneWidth int) int {
	m := MinLeft
	for i := range tabCount {
		var items []WrappingSelectItem
		if i < len(itemsByTab) {
			items = itemsByTab[i]
		}
		if w := AdaptiveLeftWidth(items, len(items), paneWidth); w > m {
			m = w
		}
	}
	return m
}

// PreviewSourceWidth measures the widest source line across all options' previews (0 when none).
func PreviewSourceWidth(question QuestionData) int {
	m := 0
	for _, option := range question.Options {
		if option.Preview == "" {
			continue
		}
		for _, line := range strings.Split(option.Preview, "\n") {
			if w := widthx.VisibleWidth(line); w > m {
				m = w
			}
		}
	}
	return m
}

// CrossTabPreviewBudget is the widest preview appetite across questions plus box + padding overhead, floored at
// MinPreviewWidth.
func CrossTabPreviewBudget(questions []QuestionData, paneWidth int) int {
	m := MinPreviewWidth
	for _, question := range questions {
		capped := min(PreviewSourceWidth(question), paneWidth-PreviewColumnGap-MinLeft)
		budget := capped + BorderHorizontalOverhead + 2*BorderInnerPaddingHorizontal + PreviewPaddingLeft
		if budget > m {
			m = budget
		}
	}
	return m
}

// CrossTabLeftWidthWithDonation combines the label-driven width with the slack donated by narrow previews.
func CrossTabLeftWidthWithDonation(tabCount int, itemsByTab [][]WrappingSelectItem, questions []QuestionData, paneWidth int) int {
	labelDriven := CrossTabMaxLeftWidth(tabCount, itemsByTab, paneWidth)
	previewBudget := CrossTabPreviewBudget(questions, paneWidth)
	slackDonation := paneWidth - PreviewColumnGap - previewBudget
	previewSafetyCeiling := paneWidth - PreviewColumnGap - MinPreviewWidth
	ratioCeiling := int(float64(paneWidth) * MaxLeftRatio)
	ceiling := min(previewSafetyCeiling, ratioCeiling)
	return min(max(labelDriven, slackDonation), max(1, ceiling))
}

// ColumnWidths is the width allocation for side-by-side mode.
func ColumnWidths(paneWidth, adaptiveLeft int) (leftWidth, rightWidth, gap int) {
	gap = PreviewColumnGap
	leftWidth = min(adaptiveLeft, max(1, paneWidth-gap-1))
	rightWidth = max(1, paneWidth-leftWidth-gap)
	return leftWidth, rightWidth, gap
}

// BodyWidths returns the widths passed to the option list and the preview block inside Render.
func BodyWidths(paneWidth int, mode PreviewLayoutMode, adaptiveLeft int) (optionsWidth, previewWidth int) {
	if mode == LayoutStacked {
		return paneWidth, paneWidth
	}
	leftWidth, rightWidth, _ := ColumnWidths(paneWidth, adaptiveLeft)
	return leftWidth, max(1, rightWidth-PreviewPaddingLeft)
}
