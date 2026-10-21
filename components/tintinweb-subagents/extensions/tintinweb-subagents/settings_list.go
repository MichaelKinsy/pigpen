package tintinweb_subagents

import (
	"strconv"
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// pi-tui's SettingsList, as the `/agents` menus use it: a scrolling list of label and value rows, the selected row's
// description below, Enter or Space cycling the value, Escape cancelling. The search box, the submenus and the mouse
// are not ported: neither menu sets enableSearch or a submenu, and a mouse never reaches an extension's component.
// upstream: pi-tui src/components/settings-list.ts, and Pi's getSettingsListTheme (theme.ts).

type settingItem struct {
	ID    string
	Label string
	// Description is shown below the list while the row is selected.
	Description  string
	CurrentValue string
	// Values: Enter/Space cycles through these.
	Values []string
}

type settingsListTheme struct {
	Label       func(text string, selected bool) string
	Value       func(text string, selected bool) string
	Description func(text string) string
	Cursor      string
	Hint        func(text string) string
}

// settingsListThemeFor is Pi's getSettingsListTheme over the host theme.
func settingsListThemeFor(th uiTheme) settingsListTheme {
	return settingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return th.Fg("accent", text)
			}
			return text
		},
		Value: func(text string, selected bool) string {
			if selected {
				return th.Fg("accent", text)
			}
			return th.Fg("muted", text)
		},
		Description: func(text string) string { return th.Fg("dim", text) },
		Cursor:      th.Fg("accent", "→ "),
		Hint:        func(text string) string { return th.Fg("dim", text) },
	}
}

type settingsList struct {
	items         []*settingItem
	theme         settingsListTheme
	selectedIndex int
	maxVisible    int
	onChange      func(id, newValue string)
	onCancel      func()
}

func newSettingsList(items []*settingItem, maxVisible int, th settingsListTheme, onChange func(id, newValue string), onCancel func()) *settingsList {
	return &settingsList{items: items, maxVisible: maxVisible, theme: th, onChange: onChange, onCancel: onCancel}
}

func (l *settingsList) visibleRange() (start, end int) {
	start = max(0, min(l.selectedIndex-l.maxVisible/2, len(l.items)-l.maxVisible))
	return start, min(start+l.maxVisible, len(l.items))
}

func (l *settingsList) Render(width int) []string {
	var lines []string
	if len(l.items) == 0 {
		return append(lines, l.theme.Hint("  No settings available"))
	}
	start, end := l.visibleRange()
	// Pad the label to align the values.
	maxLabel := 0
	for _, item := range l.items {
		maxLabel = max(maxLabel, widthx.VisibleWidth(item.Label))
	}
	maxLabel = min(36, maxLabel)
	for i := start; i < end; i++ {
		item := l.items[i]
		selected := i == l.selectedIndex
		prefix := "  "
		if selected {
			prefix = l.theme.Cursor
		}
		labelPadded := item.Label + strings.Repeat(" ", max(0, maxLabel-widthx.VisibleWidth(item.Label)))
		const separator = "  "
		used := widthx.VisibleWidth(prefix) + maxLabel + widthx.VisibleWidth(separator)
		valueMax := width - used - 2
		value := l.theme.Value(widthx.TruncateToWidth(item.CurrentValue, valueMax, "", false), selected)
		lines = append(lines, widthx.TruncateToWidth(prefix+l.theme.Label(labelPadded, selected)+separator+value, width, "...", false))
	}
	if start > 0 || end < len(l.items) {
		scroll := "  (" + strconv.Itoa(l.selectedIndex+1) + "/" + strconv.Itoa(len(l.items)) + ")"
		lines = append(lines, l.theme.Hint(widthx.TruncateToWidth(scroll, width-2, "", false)))
	}
	if l.selectedIndex < len(l.items) {
		if desc := l.items[l.selectedIndex].Description; desc != "" {
			lines = append(lines, "")
			for _, line := range widthx.WrapTextWithAnsi(desc, width-4) {
				lines = append(lines, l.theme.Description("  "+line))
			}
		}
	}
	lines = append(lines, "")
	lines = append(lines, widthx.TruncateToWidth(l.theme.Hint("  Enter/Space to change · Esc to cancel"), width, "...", false))
	return lines
}

func (l *settingsList) HandleInput(data string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, tui.KBSelectUp):
		if len(l.items) == 0 {
			return
		}
		if l.selectedIndex == 0 {
			l.selectedIndex = len(l.items) - 1
		} else {
			l.selectedIndex--
		}
	case kb.Matches(data, tui.KBSelectDown):
		if len(l.items) == 0 {
			return
		}
		if l.selectedIndex == len(l.items)-1 {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
	case kb.Matches(data, tui.KBSelectConfirm) || data == " ":
		l.activate()
	case kb.Matches(data, tui.KBSelectCancel):
		l.onCancel()
	}
}

// activate cycles the selected row through its values.
func (l *settingsList) activate() {
	if l.selectedIndex >= len(l.items) {
		return
	}
	item := l.items[l.selectedIndex]
	if len(item.Values) == 0 {
		return
	}
	cur := -1
	for i, v := range item.Values {
		if v == item.CurrentValue {
			cur = i
			break
		}
	}
	next := item.Values[(cur+1)%len(item.Values)]
	item.CurrentValue = next
	l.onChange(item.ID, next)
}

// menuList is the `Container` of a title, an optional hint, a spacer and a SettingsList, as the menus build it.
type menuList struct {
	title, hint string
	list        *settingsList
	handleKey   func(data string) bool // consulted first: true means the key was taken
}

func (m *menuList) Render(width int) []string {
	var lines []string
	lines = append(lines, tui.NewText(m.title, 0, 0, nil).Render(width)...)
	if m.hint != "" {
		lines = append(lines, tui.NewText(m.hint, 0, 0, nil).Render(width)...)
	}
	lines = append(lines, tui.NewSpacer(1).Render(width)...)
	return append(lines, m.list.Render(width)...)
}

func (m *menuList) HandleInput(data string) {
	if m.handleKey != nil && m.handleKey(data) {
		return
	}
	m.list.HandleInput(data)
}

func (m *menuList) Dispose() {}
