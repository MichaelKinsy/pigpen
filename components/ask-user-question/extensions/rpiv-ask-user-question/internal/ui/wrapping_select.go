package ui

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// WrappingSelectItem is the row-intent discriminated union; Kind is the single discriminator (KindOption,
// KindOther, KindNext). Description is `description?`: empty means absent.
//
//   - option: a regular author-defined option row.
//   - other: the inline free-text input row appended to every question (label "Type something."). Renders the
//     headless multiline editor when active.
//   - next: the explicit commit-and-advance row appended to multi-select questions (label "Next").
//
// upstream: view/components/wrapping-select.ts.
type WrappingSelectItem struct {
	Kind        string
	Label       string
	Description string
}

// WrappingSelectTheme is the styling of a WrappingSelect.
type WrappingSelectTheme struct {
	SelectedText func(string) string
	Description  func(string) string
	ScrollInfo   func(string) string
}

// WrappingSelectOptions are the numbering controls. Use NumberStartOffset and TotalItemsForNumbering when a list
// is logically a slice of a larger numbered sequence.
type WrappingSelectOptions struct {
	// NumberStartOffset starts numbering at this offset + 1 (default 0: rows labeled 1, 2, 3 ...).
	NumberStartOffset int
	// TotalItemsForNumbering overrides the total used to pad the number column; nil is the item count.
	TotalItemsForNumbering *int
}

const (
	wsActivePointer   = "❯ "
	wsInactivePointer = "  "
	wsNumberSeparator = ". "
	wsConfirmedMark   = " ✔"
	wsMinContentWidth = 1
)

// WrappingSelect is a windowed, wrapping, numbered single-select list.
type WrappingSelect struct {
	items      []WrappingSelectItem
	maxVisible int
	theme      WrappingSelectTheme

	numberStartOffset      int
	totalItemsForNumbering int

	selectedIndex     int
	focused           bool
	inputBuffer       string
	inputCursorOffset *int
	// confirmedIndex is the row previously confirmed for this list. It renders `<label> ✔` in the active-row
	// styling but without the pointer: the pointer is reserved for the live cursor. When selectedIndex ==
	// confirmedIndex and focused, the active rendering wins.
	confirmedIndex *int
	// confirmedLabelOverride, with confirmedIndex, replaces the row's static label at render time (the `other`
	// sentinel shows the prior custom answer instead of "Type something.").
	confirmedLabelOverride *string
}

// NewWrappingSelect builds a WrappingSelect over items.
func NewWrappingSelect(items []WrappingSelectItem, maxVisible int, theme WrappingSelectTheme, options WrappingSelectOptions) *WrappingSelect {
	total := len(items)
	if options.TotalItemsForNumbering != nil {
		total = *options.TotalItemsForNumbering
	}
	return &WrappingSelect{
		items:                  items,
		maxVisible:             max(1, maxVisible),
		theme:                  theme,
		numberStartOffset:      options.NumberStartOffset,
		totalItemsForNumbering: total,
		focused:                true,
	}
}

// SetNumbering updates the numbering offset and total padding width without rebuilding the component.
func (w *WrappingSelect) SetNumbering(numberStartOffset, totalItemsForNumbering int) {
	w.numberStartOffset = numberStartOffset
	w.totalItemsForNumbering = max(1, totalItemsForNumbering)
}

func (w *WrappingSelect) SetSelectedIndex(index int) {
	w.selectedIndex = max(0, min(index, len(w.items)-1))
}

func (w *WrappingSelect) SetFocused(focused bool) { w.focused = focused }

// SetConfirmedIndex marks a previously-confirmed row; nil clears. labelOverride replaces the row's static label
// at render time (used for the `other` sentinel).
func (w *WrappingSelect) SetConfirmedIndex(index *int, labelOverride *string) {
	if index == nil {
		w.confirmedIndex = nil
		w.confirmedLabelOverride = nil
		return
	}
	i := max(0, min(*index, len(w.items)-1))
	w.confirmedIndex = &i
	w.confirmedLabelOverride = labelOverride
}

func (w *WrappingSelect) SetInputBuffer(text string) { w.inputBuffer = text }

// SetInputCursorOffset sets the cursor offset for the inline input row; nil is the end-of-buffer fallback.
func (w *WrappingSelect) SetInputCursorOffset(offset *int) { w.inputCursorOffset = offset }

// HandleInput is intentionally empty: input is routed at the container level.
func (w *WrappingSelect) HandleInput(string) {}

func (w *WrappingSelect) Invalidate() {}

func (w *WrappingSelect) numberWidth() int {
	return len(strconv.Itoa(max(1, w.totalItemsForNumbering)))
}

func (w *WrappingSelect) Render(width int) []string {
	if len(w.items) == 0 {
		return []string{}
	}
	startIndex, endIndex := w.computeVisibleWindow()
	numberWidth := w.numberWidth()
	lines := []string{}
	for i := startIndex; i < endIndex; i++ {
		isActive := i == w.selectedIndex && w.focused
		lines = append(lines, w.renderItem(w.items[i], i, isActive, width, numberWidth)...)
	}
	if w.hasItemsOutsideWindow(startIndex, endIndex) {
		lines = append(lines, w.theme.ScrollInfo("  ("+strconv.Itoa(w.selectedIndex+1)+"/"+strconv.Itoa(len(w.items))+")"))
	}
	return lines
}

// FocusedItemRowRange returns the [startRow, endRow) range of the focused item within Render(width), by
// summing per-item row counts over the visible window.
func (w *WrappingSelect) FocusedItemRowRange(width int) (int, int) {
	if len(w.items) == 0 {
		return 0, 0
	}
	startIndex, endIndex := w.computeVisibleWindow()
	numberWidth := w.numberWidth()
	row := 0
	for i := startIndex; i < endIndex; i++ {
		isActive := i == w.selectedIndex && w.focused
		count := len(w.renderItem(w.items[i], i, isActive, width, numberWidth))
		if i == w.selectedIndex {
			return row, row + count
		}
		row += count
	}
	return 0, 1
}

func (w *WrappingSelect) computeVisibleWindow() (int, int) {
	half := w.maxVisible / 2
	startIndex := max(0, min(w.selectedIndex-half, len(w.items)-w.maxVisible))
	endIndex := min(startIndex+w.maxVisible, len(w.items))
	return startIndex, endIndex
}

func (w *WrappingSelect) hasItemsOutsideWindow(startIndex, endIndex int) bool {
	return startIndex > 0 || endIndex < len(w.items)
}

func (w *WrappingSelect) renderItem(item WrappingSelectItem, index int, isActive bool, width, numberWidth int) []string {
	rowPrefix := w.buildRowPrefix(index, isActive, numberWidth)
	continuationPrefix := strings.Repeat(" ", widthx.VisibleWidth(rowPrefix))
	contentWidth := max(wsMinContentWidth, width-widthx.VisibleWidth(rowPrefix))

	if item.Kind == KindOther && isActive {
		return RenderInlineInputRow(RenderInlineInputOptions{
			Buffer:             w.inputBuffer,
			CursorOffset:       w.inputCursorOffset,
			RowPrefix:          rowPrefix,
			ContinuationPrefix: continuationPrefix,
			ContentWidth:       contentWidth,
			SelectedText:       w.theme.SelectedText,
		})
	}

	label, isConfirmed := w.deriveConfirmedState(item, index)
	applySelectedStyle := isActive || isConfirmed
	out := w.renderLabelBlock(label, rowPrefix, continuationPrefix, contentWidth, applySelectedStyle)
	return append(out, w.renderDescriptionBlock(item.Description, continuationPrefix, contentWidth)...)
}

// deriveConfirmedState derives the final label (custom draft or confirmed-override aware) and whether the ✔ mark
// applies. An in-flight custom draft stays visible while the cursor browses another row; if it differs from a
// previously confirmed custom answer the confirmation mark is omitted so the draft is not presented as committed.
func (w *WrappingSelect) deriveConfirmedState(item WrappingSelectItem, index int) (string, bool) {
	customDraft := ""
	if item.Kind == KindOther {
		customDraft = w.inputBuffer
	}
	atConfirmed := w.confirmedIndex != nil && index == *w.confirmedIndex
	overrideOrEmpty := ""
	if w.confirmedLabelOverride != nil {
		overrideOrEmpty = *w.confirmedLabelOverride
	}
	differs := item.Kind == KindOther && customDraft != "" && atConfirmed && customDraft != overrideOrEmpty
	isConfirmed := atConfirmed && !differs
	baseLabel := item.Label
	if customDraft != "" {
		baseLabel = customDraft
	}
	label := baseLabel
	if isConfirmed {
		if w.confirmedLabelOverride != nil {
			label = *w.confirmedLabelOverride + wsConfirmedMark
		} else {
			label = baseLabel + wsConfirmedMark
		}
	}
	return label, isConfirmed
}

func (w *WrappingSelect) buildRowPrefix(index int, isActive bool, numberWidth int) string {
	pointer := wsInactivePointer
	if isActive {
		pointer = wsActivePointer
	}
	return pointer + padStartSpaces(strconv.Itoa(w.numberStartOffset+index+1), numberWidth) + wsNumberSeparator
}

// padStartSpaces is JavaScript's String.prototype.padStart(n, " ") for ASCII digit strings.
func padStartSpaces(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}

func (w *WrappingSelect) renderLabelBlock(label, rowPrefix, continuationPrefix string, contentWidth int, applySelectedStyle bool) []string {
	wrapped := widthx.WrapTextWithAnsi(label, contentWidth)
	out := make([]string, len(wrapped))
	for index, segment := range wrapped {
		prefix := continuationPrefix
		if index == 0 {
			prefix = rowPrefix
		}
		line := prefix + segment
		if applySelectedStyle {
			line = w.theme.SelectedText(line)
		}
		out[index] = line
	}
	return out
}

func (w *WrappingSelect) renderDescriptionBlock(description, continuationPrefix string, contentWidth int) []string {
	if description == "" {
		return nil
	}
	wrapped := widthx.WrapTextWithAnsi(description, contentWidth)
	out := make([]string, len(wrapped))
	for i, segment := range wrapped {
		out[i] = continuationPrefix + w.theme.Description(segment)
	}
	return out
}
