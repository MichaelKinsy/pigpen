package frontend

import (
	"fmt"
	"slices"
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// PiG's selectors and overlays float over the transcript in Tern's layer
// region: a Selector is a picker sheet, Settings a prefs sheet, an Overlay a
// card around its ANSI lines. Keys still reach PiG on the pty, which drives
// the selector and sends its new state; Tern's pointer events on a sheet run
// its handlers, which turn them into Env.Act actions that PiG performs as
// those keys.

// pickerCatalog keeps the picker's items prop: every item shown since the
// selector opened, by id. Filtering only changes the order prop, so typing
// over a long list sends ids, not items.
type pickerCatalog struct {
	items []any
	at    map[string]int
	seen  []fe.SelectorItem
}

// update adds or replaces the items of sel. A change copies the items slice
// first, so a set op still queued keeps the catalog it was made with. Items
// usually come in the catalog's order (a selection move resends them all),
// so the entry after the last one found is tried before the index.
func (c *pickerCatalog) update(sel fe.Selector) {
	if c.at == nil {
		c.at = map[string]int{}
	}
	copied := false
	next := 0
	for _, item := range sel.Items {
		i, ok := next, next < len(c.seen) && c.seen[next].ID == item.ID
		if !ok {
			i, ok = c.at[item.ID]
		}
		if ok {
			next = i + 1
			if sameCatalogItem(c.seen[i], item) {
				continue
			}
		}
		if !copied {
			c.items = append(make([]any, 0, len(c.items)+len(sel.Items)), c.items...)
			copied = true
		}
		if ok {
			c.items[i], c.seen[i] = pickerItem(item), item
			continue
		}
		c.at[item.ID] = len(c.items)
		c.items = append(c.items, pickerItem(item))
		c.seen = append(c.seen, item)
	}
}

// sameCatalogItem reports whether two items make the same catalog entry;
// whether an item is checked is the current prop's.
func sameCatalogItem(a, b fe.SelectorItem) bool {
	return a.ID == b.ID && a.Label == b.Label && a.Detail == b.Detail && a.Depth == b.Depth
}

func pickerItem(item fe.SelectorItem) map[string]any {
	out := map[string]any{"id": item.ID, "label": item.Label}
	if item.Detail != "" {
		out["detail"] = item.Detail
	}
	if item.Depth > 0 {
		out["depth"] = item.Depth
	}
	return out
}

func isLayerNode(node fe.Node) bool {
	switch node.(type) {
	case fe.Selector, fe.Settings:
		return true
	}
	return false
}

// act performs a pointer event as PiG's action on the node with PiG id id.
func (s *session) act(id string, kind fe.ActionKind, item, value string) {
	if s.env.Act != nil {
		s.env.Act(fe.Action{Node: id, Kind: kind, Item: item, Value: value})
	}
}

// pickerElement draws a selector as a palette-sized picker sheet: the title,
// the description and status as its subtitle, the views as tabs, and Cancel
// and Select actions bound to Escape and Enter, which PiG handles as keys.
// Nested rows lay it out as a tree. The rows, the query and the selection
// are raw props (see queuePicker).
func pickerElement(id string, sel fe.Selector, tree bool) tern.Element {
	picker := ui.Picker{
		PickerCommon: ui.PickerCommon{Key: id},
		Size:         ui.SizeMD,
		Preview:      ui.PaneNone,
		Noun:         "items",
		Title:        ui.T(sel.Title),
		Subtitle:     ui.T(joinNonEmpty(" · ", sel.Description, sel.Status)),
		Tab:          sel.Tab,
		Actions: []ui.PickerAction{
			{ID: "close", Label: "Cancel", Keys: []string{"esc"}},
			{ID: "choose", Label: "Select", Keys: []string{"enter"}, Primary: true},
		},
	}
	if tree {
		picker.Layout = ui.LayoutTree
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

// samePickerShell reports whether two selectors draw the same picker shell,
// all but its rows (and the tree layout they may ask for).
func samePickerShell(a, b fe.Selector) bool {
	return a.Title == b.Title && a.Description == b.Description && a.Status == b.Status &&
		a.Tab == b.Tab && a.Loading == b.Loading && a.Confirm == b.Confirm && slices.Equal(a.Tabs, b.Tabs)
}

// pickerRows are a selector's rows as raw props, the ids in PiG's order and
// the checked ones, and whether a nested row lays the picker out as a tree.
type pickerRows struct {
	order, current []string
	tree           bool
}

// rowsOf returns sel's rows, reusing the order and current slices last sent
// in prev while they still hold, as they do on a selection move.
func rowsOf(sel fe.Selector, prev *rawProps) pickerRows {
	rows := pickerRows{}
	sameOrder := prev.order != nil && len(prev.order) == len(sel.Items)
	sameCurrent := prev.current != nil
	checked := 0
	for i, item := range sel.Items {
		if sameOrder && prev.order[i] != item.ID {
			sameOrder = false
		}
		if item.Checked {
			if sameCurrent && (checked >= len(prev.current) || prev.current[checked] != item.ID) {
				sameCurrent = false
			}
			checked++
		}
		if item.Depth > 0 {
			rows.tree = true
		}
	}
	if sameOrder {
		rows.order = prev.order
	} else {
		rows.order = make([]string, len(sel.Items))
		for i, item := range sel.Items {
			rows.order[i] = item.ID
		}
	}
	if sameCurrent && checked == len(prev.current) {
		rows.current = prev.current
	} else {
		rows.current = make([]string, 0, checked)
		for _, item := range sel.Items {
			if item.Checked {
				rows.current = append(rows.current, item.ID)
			}
		}
	}
	return rows
}

// queuePicker queues the picker's raw props as one set of what changed: the
// catalog of every item seen, the rows in PiG's order as ids, the checked
// ones as current, the selection, and the query while the selector
// searches. Typing over a long list sends the order, not the items.
func (s *session) queuePicker(e *entry, sel fe.Selector, rows pickerRows, fresh bool) {
	e.catalog.update(sel)
	changed := map[string]any{}
	if fresh || !sameSlice(e.catalog.items, e.raw.items) {
		changed["items"] = e.catalog.items
	}
	if fresh || !sameSlice(rows.order, e.raw.order) {
		changed["order"] = rows.order
	}
	if fresh || !sameSlice(rows.current, e.raw.current) {
		changed["current"] = rows.current
	}
	if fresh || sel.Selected != e.raw.selected {
		changed["selected"] = nullable(sel.Selected)
	}
	if fresh || sel.Searchable != e.raw.searchable || sel.Query != e.raw.query {
		changed["query"] = nil
		if sel.Searchable {
			changed["query"] = sel.Query
		}
	}
	if len(changed) > 0 {
		s.queue(tern.OpSet(e.tid, changed))
	}
	e.raw.items, e.raw.order, e.raw.current = e.catalog.items, rows.order, rows.current
	e.raw.selected, e.raw.query, e.raw.searchable = sel.Selected, sel.Query, sel.Searchable
}

// sameSlice reports whether a and b are the same slice. rowsOf reuses the
// rows sent while they hold, so other rows differ.
func sameSlice[T any](a, b []T) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// nullable is "" as a null, which removes a prop.
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// pickerHandlers turn the picker's pointer events into PiG's actions on the
// selector: a click highlights, a second click or a double click chooses,
// the bar's Select and the confirm strip's button choose the selection,
// Cancel, the backdrop and Keep dismiss, Clear search empties the query,
// and a tab switches the view.
func (s *session) pickerHandlers(id string) tern.Handlers {
	dismiss := func(*tern.Action) { s.act(id, fe.Dismiss, "", "") }
	choose := func(*tern.Action) { s.act(id, fe.Choose, "", "") }
	return tern.Handlers{
		OnSelect:   func(e *tern.Select) { s.act(id, fe.Highlight, e.Item, "") },
		OnActivate: func(e *tern.Activate) { s.act(id, fe.Choose, e.Item, "") },
		OnAction: map[string]func(*tern.Action){
			"close": dismiss, "cancel": dismiss,
			"choose": choose, "confirm": choose,
			"clear": func(*tern.Action) { s.act(id, fe.Filter, "", "") },
			"tab":   func(e *tern.Action) { s.act(id, fe.SwitchTab, e.Value, "") },
		},
	}
}

func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, strings.ReplaceAll(part, "\n", " "))
		}
	}
	return strings.Join(kept, sep)
}

// prefsElement draws settings as a prefs sheet with one page: a setting that
// toggles between true and false is a switch, one that opens a submenu an
// action button showing its value, and any other a choice of its values.
// The selected setting has the focus ring, and editing is the row whose
// choice menu is open.
func prefsElement(id string, settings fe.Settings, editing string) tern.Element {
	rows := make([]ui.PrefsRow, len(settings.Items))
	for i, setting := range settings.Items {
		rows[i] = ui.PrefsRow{ID: setting.ID, Label: setting.Label, Hint: setting.Description, Control: settingControl(setting)}
	}
	prefs := ui.Prefs{
		CommonWithoutTitle: ui.CommonWithoutTitle{Key: id},
		Title:              "Settings",
		Pages:              []ui.PrefsPage{{ID: "settings", Label: "Settings"}},
		Page:               "settings",
		Sections:           []ui.PrefsSection{{ID: "settings", Page: "settings", Rows: rows}},
		Query:              settings.Query,
		Focus:              settings.Selected,
	}
	if editing != "" {
		prefs.Editing = &ui.PrefsEditing{Row: editing}
	}
	return prefs
}

// prefsHandlers turn prefs events into PiG's actions: a switch or choice
// change sets the value, a Reset sets nothing, a row click highlights, a
// submenu button chooses its row and Close dismisses. A popup click opens
// its menu locally, and a change or a click outside closes it.
func (s *session) prefsHandlers(id string) tern.Handlers {
	return tern.Handlers{
		OnChange: func(e *tern.Change) {
			if value, ok := valueText(e.Value); ok {
				s.act(id, fe.SetValue, e.Item, value)
			}
			s.setEditing(id, "")
		},
		OnActivate: func(e *tern.Activate) { s.setEditing(id, e.Item) },
		OnSelect: func(e *tern.Select) {
			if !strings.Contains(e.Item, "=") {
				s.act(id, fe.Highlight, e.Item, "")
			}
		},
		OnAction: map[string]func(*tern.Action){
			"open": func(e *tern.Action) { s.act(id, fe.Choose, e.Value, "") },
			"close": func(e *tern.Action) {
				if e.Value != "" {
					s.setEditing(id, "")
				} else {
					s.act(id, fe.Dismiss, "", "")
				}
			},
		},
	}
}

func settingControl(setting fe.Setting) ui.PrefsControl {
	if setting.Submenu {
		return ui.PrefAction{Act: "open", Label: setting.Value}
	}
	if isBoolean(setting.Values) {
		return ui.PrefSwitch{On: setting.Value == "true"}
	}
	options := make([]ui.PrefOption, len(setting.Values))
	for i, value := range setting.Values {
		options[i] = ui.PrefOption{Value: value}
	}
	return ui.PrefChoice{Value: setting.Value, Options: options, Style: ui.ChoiceAuto}
}

func isBoolean(values []string) bool {
	return len(values) == 2 && (values[0] == "true" && values[1] == "false" || values[0] == "false" && values[1] == "true")
}

// valueText is a prefs change's value as PiG's setting value: a switch's
// boolean, a choice's string; a Reset (null) has none.
func valueText(value any) (string, bool) {
	switch v := value.(type) {
	case bool:
		return fmt.Sprint(v), true
	case string:
		return v, true
	case float64:
		return fmt.Sprint(v), true
	}
	return "", false
}

// setEditing opens or closes a choice row's menu, which only Tern draws. It
// runs on the SDK's input goroutine, inside a handler.
func (s *session) setEditing(id, row string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := slices.IndexFunc(s.dock, func(e *entry) bool { return e.id == id })
	if at < 0 || s.dock[at].editing == row {
		return
	}
	if _, ok := s.dock[at].node.(fe.Settings); !ok {
		return
	}
	s.dock[at].editing = row
	s.rebuild(s.dock[at], s.dock[at].node, true)
	_ = s.flushLocked()
}

// overlayElement draws an overlay as a card around its lines: modal while
// it takes the keys, placed at the top, center or bottom as Pi anchors it,
// and as wide as the lines need.
func overlayElement(id string, node fe.Node, cellPx float64) tern.Element {
	overlay, _ := node.(fe.Overlay)
	anchor := ui.AnchorCenter
	switch {
	case strings.HasPrefix(overlay.Anchor, "top"):
		anchor = ui.AnchorTop
	case strings.HasPrefix(overlay.Anchor, "bottom"):
		anchor = ui.AnchorBottom
	}
	// The card pads its body by 6px on each side.
	px := float64(overlay.Width)*cellPx + 12
	size := ui.SizeFull
	switch {
	case px <= 320:
		size = ui.SizeSM
	case px <= 480:
		size = ui.SizeMD
	case px <= 720:
		size = ui.SizeLG
	}
	return ui.Overlay{
		Common: ui.Common{Key: id}, Modal: !overlay.NonCapturing, Anchor: anchor, Size: size,
		Children: []tern.Element{ui.Ansi{Common: ui.Common{Key: "lines"}, Text: strings.Join(overlay.Lines, "\n")}},
	}
}

// sameSettings reports whether two settings nodes draw the same prefs sheet.
func sameSettings(a, b fe.Settings) bool {
	return a.Searchable == b.Searchable && a.Query == b.Query && a.Selected == b.Selected &&
		slices.EqualFunc(a.Items, b.Items, func(x, y fe.Setting) bool {
			return x.ID == y.ID && x.Label == y.Label && x.Description == y.Description &&
				x.Value == y.Value && x.Submenu == y.Submenu && slices.Equal(x.Values, y.Values)
		})
}

// sameOverlay reports whether two overlays draw the same card.
func sameOverlay(a, b fe.Overlay) bool {
	return a.Width == b.Width && a.Anchor == b.Anchor && a.NonCapturing == b.NonCapturing && slices.Equal(a.Lines, b.Lines)
}
