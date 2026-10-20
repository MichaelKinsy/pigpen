package powerline_footer

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The select overlay of the original (showSelectOverlay, index.ts 1431) over a port of pi-tui's SelectList. The queue picker
// and the stash history use it. The pi-tui parts are small pure functions in this file: swapping them for the tui package of
// PiG is a local change.

// selectItem is pi-tui's SelectItem.
type selectItem struct{ Value, Label, Description string }

// overlayTheme is the theme the overlay needs: the host's UITheme.
type overlayTheme interface {
	Fg(token, text string) string
	Bold(text string) string
}

// buildStashPreview is buildStashPreview of index.ts 679: one line of at most maxWidth cells, "(empty)" for blank text.
func buildStashPreview(text string, maxWidth int) string {
	compact := queueCollapseSpace(text)
	if compact == "" {
		return "(empty)"
	}
	return truncateToWidth(compact, maxWidth, "…")
}

// pi-tui SelectList (components/select-list.js) with the overlay's theme (overlaySelectListTheme, index.ts 1417). The primary
// column is clamped to its default bounds, min = max = 32, as the overlay passes no layout.
const (
	selectPrimaryColumnWidth = 32 // DEFAULT_PRIMARY_COLUMN_WIDTH
	selectPrimaryColumnGap   = 2  // PRIMARY_COLUMN_GAP
	selectMinDescriptionWide = 10 // MIN_DESCRIPTION_WIDTH
)

type selectList struct {
	items         []selectItem
	selectedIndex int
	maxVisible    int
	theme         overlayTheme
}

func (l *selectList) visibleRange() (start, end int) {
	start = max(0, min(l.selectedIndex-l.maxVisible/2, len(l.items)-l.maxVisible))
	return start, min(start+l.maxVisible, len(l.items))
}

func selectDisplayValue(item selectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}

// normalizeToSingleLine is text.replace(/[\r\n]+/g, " ").trim().
func normalizeToSingleLine(text string) string {
	var b strings.Builder
	inBreak := false
	for _, r := range text {
		if r == '\r' || r == '\n' {
			if !inBreak {
				b.WriteByte(' ')
			}
			inBreak = true
			continue
		}
		inBreak = false
		b.WriteRune(r)
	}
	return jsTrim(b.String())
}

func (l *selectList) render(width int) []string {
	if len(l.items) == 0 {
		return []string{l.theme.Fg("warning", "  No matching commands")}
	}
	var lines []string
	start, end := l.visibleRange()
	for i := start; i < end; i++ {
		item := l.items[i]
		description := ""
		if item.Description != "" {
			description = normalizeToSingleLine(item.Description)
		}
		lines = append(lines, l.renderItem(item, i == l.selectedIndex, width, description))
	}
	if start > 0 || end < len(l.items) {
		scroll := "  (" + jsNumber(float64(l.selectedIndex+1)) + "/" + jsNumber(float64(len(l.items))) + ")"
		lines = append(lines, l.theme.Fg("dim", truncateToWidth(scroll, width-2, "")))
	}
	return lines
}

func (l *selectList) renderItem(item selectItem, selected bool, width int, description string) string {
	prefix := "  "
	if selected {
		prefix = "→ "
	}
	prefixWidth := visibleWidth(prefix)
	if description != "" && width > 40 {
		effective := max(1, min(selectPrimaryColumnWidth, width-prefixWidth-4))
		maxPrimary := max(1, effective-selectPrimaryColumnGap)
		value := truncateToWidth(selectDisplayValue(item), maxPrimary, "")
		valueWidth := visibleWidth(value)
		spacing := strings.Repeat(" ", max(1, effective-valueWidth))
		remaining := width - (prefixWidth + valueWidth + len(spacing)) - 2 // -2 for safety
		if remaining > selectMinDescriptionWide {
			truncated := truncateToWidth(description, remaining, "")
			if selected {
				return l.theme.Fg("accent", prefix+value+spacing+truncated)
			}
			return prefix + value + l.theme.Fg("muted", spacing+truncated)
		}
	}
	maxWidth := width - prefixWidth - 2
	value := truncateToWidth(selectDisplayValue(item), maxWidth, "")
	if selected {
		return l.theme.Fg("accent", prefix+value)
	}
	return prefix + value
}

// The keys of tui.select.* at Pi's defaults (an extension cannot read the user's keybindings): up, down, enter, escape and ctrl+c.
func selectKeyUp(data string) bool {
	switch data {
	case "\x1b[A", "\x1bOA", "\x1b[1;1A", "\x1b[1;1:1A", "\x1b[1;1:2A":
		return true
	}
	return false
}

func selectKeyDown(data string) bool {
	switch data {
	case "\x1b[B", "\x1bOB", "\x1b[1;1B", "\x1b[1;1:1B", "\x1b[1;1:2B":
		return true
	}
	return false
}

func selectKeyConfirm(data string) bool {
	switch data {
	case "\r", "\n", "\x1bOM", "\x1b[13u", "\x1b[13;1u", "\x1b[13;1:1u", "\x1b[13;1:2u", "\x1b[57414u":
		return true
	}
	return false
}

func selectKeyCancel(data string) bool {
	switch data {
	case "\x1b", "\x1b[27u", "\x1b[27;1u", "\x1b[27;1:1u", "\x1b[27;1:2u", "\x03", "\x1b[99;5u", "\x1b[99;5:1u", "\x1b[99;5:2u":
		return true
	}
	return false
}

// selectOverlay is the component showSelectOverlay opens: a bordered box with a title, the list and a hint line.
type selectOverlay struct {
	theme       overlayTheme
	title, hint string
	list        *selectList
}

func newSelectOverlay(th overlayTheme, title, hint string, items []selectItem, maxVisible int) *selectOverlay {
	return &selectOverlay{theme: th, title: title, hint: hint, list: &selectList{items: items, maxVisible: maxVisible, theme: th}}
}

func (o *selectOverlay) Render(width int) []string {
	inner := max(1, width-2)
	border := func(text string) string { return o.theme.Fg("dim", text) }
	row := func(text string) string {
		cut := truncateToWidth(text, inner, "…")
		if pad := inner - visibleWidth(cut); pad > 0 {
			cut += strings.Repeat(" ", pad)
		}
		return border("│") + cut + border("│")
	}
	bar := strings.Repeat("─", inner)
	lines := []string{border("╭" + bar + "╮"), row(o.theme.Fg("accent", o.theme.Bold(o.title))), border("├" + bar + "┤")}
	for _, line := range o.list.render(inner) {
		lines = append(lines, row(line))
	}
	return append(lines, border("├"+bar+"┤"), row(o.theme.Fg("dim", o.hint)), border("╰"+bar+"╯"))
}

// HandleInput moves the selection, or closes the overlay with the chosen item (Value is a map with value, label and
// description) or with nil.
func (o *selectOverlay) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	l := o.list
	switch {
	case selectKeyUp(data):
		if l.selectedIndex == 0 {
			l.selectedIndex = len(l.items) - 1
		} else {
			l.selectedIndex--
		}
	case selectKeyDown(data):
		if l.selectedIndex == len(l.items)-1 {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
	case selectKeyConfirm(data):
		if l.selectedIndex >= 0 && l.selectedIndex < len(l.items) {
			item := l.items[l.selectedIndex]
			return sdk.RemoteComponentResult{Done: true, Value: map[string]any{"value": item.Value, "label": item.Label, "description": item.Description}}, nil
		}
	case selectKeyCancel(data):
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// showSelectOverlay opens the overlay in the middle of the screen and waits for the choice; nil means it was cancelled (or
// there is no UI). It blocks, so the caller must not hold the core's lock.
func showSelectOverlay(ctx sdk.Context, title, hint string, items []selectItem, maxVisible int) (*selectItem, error) {
	result, err := ctx.Custom(newSelectOverlay(ctx.UITheme(), title, hint, items, maxVisible),
		sdk.RemoteOverlayOptions{Overlay: true, OverlayOptions: &sdk.OverlayOptions{Anchor: "center"}})
	if err != nil {
		return nil, err
	}
	chosen, ok := result.(map[string]any)
	if !ok {
		return nil, nil
	}
	value, _ := chosen["value"].(string)
	label, _ := chosen["label"].(string)
	description, _ := chosen["description"].(string)
	return &selectItem{Value: value, Label: label, Description: description}, nil
}
