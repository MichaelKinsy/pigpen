package powerline_footer

import (
	"fmt"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The select overlay of the pickers (stash history, project prompts). upstream: index.ts showSelectOverlay (1431-1483) around
// pi-tui's SelectList. This file is the keys slice's own minimal version, with the signature the queue slice announced; the
// lead unifies the two.

// selectItem is pi-tui's SelectItem.
type selectItem struct{ Value, Label, Description string }

// overlayTheme is the part of the theme the overlay draws with.
type overlayTheme interface {
	Fg(token, text string) string
	Bold(text string) string
}

var whitespaceRuns = regexp.MustCompile(`\s+`)

// buildStashPreview is the one-line preview of a stashed prompt: whitespace collapsed, cut to maxWidth with an ellipsis.
func buildStashPreview(text string, maxWidth int) string {
	compact := jsTrim(whitespaceRuns.ReplaceAllString(text, " "))
	if compact == "" {
		return "(empty)"
	}
	return truncateToWidth(compact, maxWidth, "…")
}

// selectOverlay is the framed SelectList: title, the visible items, a hint.
type selectOverlay struct {
	th         overlayTheme
	title      string
	hint       string
	items      []selectItem
	maxVisible int
	selected   int
}

func newSelectOverlay(th overlayTheme, title, hint string, items []selectItem, maxVisible int) *selectOverlay {
	return &selectOverlay{th: th, title: title, hint: hint, items: items, maxVisible: max(1, maxVisible)}
}

func (o *selectOverlay) Render(width int) []string {
	inner := max(1, width-2)
	border := func(s string) string { return o.th.Fg("dim", s) }
	row := func(s string) string {
		return border("│") + padTo(truncateToWidth(s, inner, "…"), inner) + border("│")
	}

	lines := []string{border("╭" + strings.Repeat("─", inner) + "╮"), row(o.th.Fg("accent", o.th.Bold(o.title))), border("├" + strings.Repeat("─", inner) + "┤")}
	start := max(0, min(o.selected-o.maxVisible/2, len(o.items)-o.maxVisible))
	end := min(start+o.maxVisible, len(o.items))
	for i := start; i < end; i++ {
		it := o.items[i]
		text := it.Label
		if it.Description != "" {
			text += "  " + o.th.Fg("muted", it.Description)
		}
		if i == o.selected {
			lines = append(lines, row(o.th.Fg("accent", "→ ")+o.th.Fg("accent", text)))
		} else {
			lines = append(lines, row("  "+text))
		}
	}
	if len(o.items) > o.maxVisible {
		lines = append(lines, row(o.th.Fg("muted", fmt.Sprintf("  (%d/%d)", o.selected+1, len(o.items)))))
	}
	return append(lines, border("├"+strings.Repeat("─", inner)+"┤"), row(o.th.Fg("dim", o.hint)), border("╰"+strings.Repeat("─", inner)+"╯"))
}

func padTo(s string, width int) string {
	if w := visibleWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// HandleInput moves the selection (up/down, wrapping), confirms with enter and cancels with escape or ctrl+c.
func (o *selectOverlay) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	n := len(o.items)
	switch {
	case matchesKey(data, "up") && n > 0:
		o.selected = (o.selected - 1 + n) % n
	case matchesKey(data, "down") && n > 0:
		o.selected = (o.selected + 1) % n
	case matchesKey(data, "enter") && n > 0:
		it := o.items[o.selected]
		return sdk.RemoteComponentResult{Done: true, Value: map[string]any{"value": it.Value, "label": it.Label, "description": it.Description}}, nil
	case matchesKey(data, "escape") || matchesKey(data, "ctrl+c"):
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// showSelectOverlay opens the overlay and returns the chosen item, nil when the user cancelled.
func showSelectOverlay(ctx sdk.Context, title, hint string, items []selectItem, maxVisible int) (*selectItem, error) {
	ui := ctx.UITheme()
	result, err := ctx.Custom(newSelectOverlay(ui, title, hint, items, maxVisible), sdk.RemoteOverlayOptions{
		Overlay:        true,
		OverlayOptions: &sdk.OverlayOptions{Anchor: "center"},
	})
	if err != nil {
		return nil, err
	}
	m, ok := result.(map[string]any)
	if !ok {
		return nil, nil
	}
	value, _ := m["value"].(string)
	label, _ := m["label"].(string)
	description, _ := m["description"].(string)
	return &selectItem{Value: value, Label: label, Description: description}, nil
}
