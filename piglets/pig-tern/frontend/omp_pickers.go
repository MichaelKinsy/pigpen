package frontend

import (
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// ompPickerElement draws a PiG Selector with omp v19.1.1's picker props:
//   - right-aligned Close action (upstream: packages/tui/src/native/picker.ts:78)
//   - primary Select action (upstream: packages/tui/src/native/picker.ts:235)
//   - default noun "options" (upstream: packages/tui/src/native/picker.ts:226)
//   - tree checked before session so PiG's "Session Tree" gets noun "entries"
//     and icon "git-branch" (upstream: packages/tui/src/overlays/tree-selector.ts:1632-1635)
//   - model ("models", icon "cpu", upstream: packages/tui/src/overlays/model-picker.ts:648-650),
//     thinking ("levels", icon "brain", upstream: packages/tui/src/overlays/model-picker.ts:693),
//     sessions ("sessions", icon "history", layout "cards", upstream: packages/tui/src/overlays/session-selector.ts:2196-2200).
func ompPickerElement(id string, sel fe.Selector, tree bool) tern.Element {
	size := ui.SizeMD
	noun := "options"
	var icon ui.IconName
	var layout ui.PickerLayout
	lowerTitle := strings.ToLower(sel.Title)
	switch {
	case tree || strings.Contains(lowerTitle, "tree") || strings.Contains(lowerTitle, "branch"):
		size = ui.SizeLG
		noun = "entries"
		icon = ui.IconName("git-branch")
		layout = ui.LayoutTree
	case strings.Contains(lowerTitle, "model"):
		size = ui.SizeMD
		noun = "models"
		icon = ui.IconName("cpu")
	case strings.Contains(lowerTitle, "thinking") || strings.Contains(lowerTitle, "effort"):
		size = ui.SizeMD
		noun = "levels"
		icon = ui.IconName("brain")
	case strings.Contains(lowerTitle, "session"):
		size = ui.SizeLG
		noun = "sessions"
		icon = ui.IconHistory
		layout = ui.LayoutCards
	}

	picker := ui.Picker{
		PickerCommon: ui.PickerCommon{Key: id},
		Size:         size,
		Layout:       layout,
		Preview:      ui.PaneNone,
		Icon:         icon,
		Noun:         noun,
		Title:        ui.T(sel.Title),
		Subtitle:     ui.T(joinNonEmpty(" · ", sel.Description, sel.Status)),
		Tab:          sel.Tab,
		Actions: []ui.PickerAction{
			{ID: "choose", Label: "Select", Keys: []string{"enter"}, Primary: true},
			{ID: "close", Label: "Close", Keys: []string{"esc"}, End: true},
		},
	}
	for _, tab := range sel.Tabs {
		picker.Tabs = append(picker.Tabs, ui.Tab{ID: tab.ID, Label: ui.T(tab.Label)})
	}
	if sel.Loading {
		picker.State = ui.StateLoading
	}
	if sel.Confirm != "" {
		picker.Confirm = &ui.Confirm{Text: ui.T(sel.Confirm)}
	}
	return picker
}

// ompPrefsElement delegates to pig-tern's prefsElement. PiG's frontend.Settings
// is a flat list without category/page metadata, so omp's multi-page settings
// sections cannot be derived from it.
func ompPrefsElement(id string, settings fe.Settings, editing string) tern.Element {
	return prefsElement(id, settings, editing)
}
