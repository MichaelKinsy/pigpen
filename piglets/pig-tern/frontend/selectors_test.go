package frontend

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

func selectorOp(kind fe.OpKind, id string, node fe.Node) fe.Op {
	return fe.Op{Kind: kind, Region: fe.RegionDock, ID: id, Index: 1, Node: node}
}

func sampleSelector() fe.Selector {
	return fe.Selector{
		Title: "Select model", Description: "Pick one", Status: "refreshed", Searchable: true,
		Items: []fe.SelectorItem{
			{ID: "a/one", Label: "one", Detail: "a", Checked: true},
			{ID: "b/two", Label: "two", Detail: "b"},
			{ID: "b/three", Label: "three", Depth: 1},
		},
		Selected: "a/one",
		Tabs:     []fe.SelectorTab{{ID: "all", Label: "all"}, {ID: "scoped", Label: "scoped"}},
		Tab:      "all",
	}
}

// A selector draws as a picker sheet in the layer and takes the focus from
// any field, so the keys reach PiG. The rendered picker carries the title,
// the description and status, the views as tabs, a tree layout for nested
// items, and Escape and Enter actions; one raw set then gives it the items
// as a catalog, PiG's order, the checked items as current, the selection
// and the query.
func TestSelectorIsAPickerSheetInTheLayer(t *testing.T) {
	h := dockWithLines(t)
	ops := h.applyOps(selectorOp(fe.Insert, "selector.1", sampleSelector()))
	if len(ops) != 3 {
		t.Fatalf("ops = %#v", ops)
	}
	add := ops[0].([]any)
	if add[0] != "add" || add[1] != "layer.selector.1" || add[2] != "layer" {
		t.Fatalf("sheet op = %#v", add)
	}
	p := props(add[4].(map[string]any))
	if add[4].(map[string]any)["k"] != "picker" || p["title"] != "Select model" || p["subtitle"] != "Pick one · refreshed" || p["layout"] != "tree" || p["tab"] != "all" || p["size"] != "md" || p["preview"] != "none" {
		t.Fatalf("picker = %#v", p)
	}
	if !reflect.DeepEqual(p["tabs"], []any{map[string]any{"id": "all", "label": "all"}, map[string]any{"id": "scoped", "label": "scoped"}}) {
		t.Fatalf("tabs = %#v", p["tabs"])
	}
	actions := p["actions"].([]any)
	if len(actions) != 2 || actions[0].(map[string]any)["id"] != "close" || actions[1].(map[string]any)["id"] != "choose" || actions[1].(map[string]any)["primary"] != true {
		t.Fatalf("actions = %#v", actions)
	}
	set := ops[1].([]any)
	want := map[string]any{
		"items": []any{
			map[string]any{"id": "a/one", "label": "one", "detail": "a"},
			map[string]any{"id": "b/two", "label": "two", "detail": "b"},
			map[string]any{"id": "b/three", "label": "three", "depth": float64(1)},
		},
		"order": []any{"a/one", "b/two", "b/three"}, "current": []any{"a/one"}, "selected": "a/one", "query": "",
	}
	if set[0] != "set" || set[1] != "layer.selector.1" || !reflect.DeepEqual(set[2], want) {
		t.Fatalf("raw props = %#v", set)
	}
	if !reflect.DeepEqual(ops[2], []any{"focus", nil}) {
		t.Fatalf("focus = %#v", ops[2])
	}

	plain := fe.Selector{Title: "Pick", Items: []fe.SelectorItem{{ID: "0", Label: "only"}}, Selected: "0", Confirm: "Delete it?", Loading: true}
	ops = h.applyOps(selectorOp(fe.Insert, "selector.2", plain))
	p = props(node(ops, "layer.selector.2"))
	for _, absent := range []string{"subtitle", "layout", "tabs", "tab"} {
		if _, ok := p[absent]; ok {
			t.Fatalf("%s set on a plain selector: %#v", absent, p)
		}
	}
	if p["state"] != "loading" || !reflect.DeepEqual(p["confirm"], map[string]any{"text": "Delete it?"}) {
		t.Fatalf("plain picker = %#v", p)
	}
	if q, ok := opsOn(ops, "layer.selector.2")[1].([]any)[2].(map[string]any)["query"]; !ok || q != nil {
		t.Fatalf("a plain selector's query = %#v, want a null that hides the search", q)
	}
}

// A selection move is one set of the selected prop; filtering sets the
// order and query without resending items; an item not seen before resends
// the catalog; removing the selector deletes its sheet. None renders.
func TestPickerChangesAreSmallSets(t *testing.T) {
	h := dockWithLines(t)
	sel := sampleSelector()
	h.applyOps(selectorOp(fe.Insert, "selector.1", sel))
	renders := h.session.renders

	sel.Selected = "b/two"
	ops := h.applyOps(selectorOp(fe.Update, "selector.1", sel))
	if want := jsonOps(t, []any{"set", "layer.selector.1", map[string]any{"selected": "b/two"}}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("move = %#v", ops)
	}

	sel.Query, sel.Items, sel.Selected = "t", sel.Items[1:], "b/two"
	ops = h.applyOps(selectorOp(fe.Update, "selector.1", sel))
	if want := jsonOps(t, []any{"set", "layer.selector.1", map[string]any{"query": "t", "order": []any{"b/two", "b/three"}, "current": []any{}}}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("filter = %#v", ops)
	}

	sel.Items = append(sel.Items, fe.SelectorItem{ID: "c/four", Label: "four"})
	ops = h.applyOps(selectorOp(fe.Update, "selector.1", sel))
	set := ops[0].([]any)[2].(map[string]any)
	if len(ops) != 1 || len(set["items"].([]any)) != 4 || len(set["order"].([]any)) != 3 {
		t.Fatalf("new item = %#v", ops)
	}

	sel.Items[0].Label = "TWO"
	ops = h.applyOps(selectorOp(fe.Update, "selector.1", sel))
	if items := ops[0].([]any)[2].(map[string]any)["items"].([]any); items[1].(map[string]any)["label"] != "TWO" {
		t.Fatalf("changed item = %#v", ops)
	}

	sel.Items = slices.Clone(sel.Items)
	sel.Items[1].Checked = true
	ops = h.applyOps(selectorOp(fe.Update, "selector.1", sel))
	if want := jsonOps(t, []any{"set", "layer.selector.1", map[string]any{"current": []any{"b/three"}}}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("checked = %#v", ops)
	}
	if h.session.renders != renders {
		t.Fatalf("row changes rendered %d times", h.session.renders-renders)
	}

	ops = h.applyOps(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "selector.1", Index: 1})
	if want := jsonOps(t, []any{"del", "layer.selector.1"}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("remove = %#v", ops)
	}
}

// The picker's pointer events become PiG's actions on the selector: a click
// highlights, a second click or a double click chooses, the bar's Select and
// the confirm strip's button choose the selection, Cancel, the backdrop and
// Keep dismiss, Clear search empties the query, and a tab switches the
// view. Events for another surface, or after the selector closed, do
// nothing.
func TestPickerEventsBecomeActions(t *testing.T) {
	h := dockWithLines(t)
	h.applyOps(selectorOp(fe.Insert, "selector.1", sampleSelector()))
	sheet := `"sf":"pig","id":"layer.selector.1"`
	for _, event := range []string{
		`{"ev":"select",` + sheet + `,"item":"b/two"}`,
		`{"ev":"activate",` + sheet + `,"item":"b/three"}`,
		`{"ev":"action",` + sheet + `,"act":"choose"}`,
		`{"ev":"action",` + sheet + `,"act":"close"}`,
		`{"ev":"action",` + sheet + `,"act":"clear"}`,
		`{"ev":"action",` + sheet + `,"act":"tab","value":"scoped"}`,
		`{"ev":"action",` + sheet + `,"act":"confirm"}`,
		`{"ev":"action",` + sheet + `,"act":"cancel"}`,
		`{"ev":"select","sf":"other","id":"layer.selector.1","item":"b/two"}`,
		`{"ev":"action",` + sheet + `,"act":"scope","value":"x"}`,
	} {
		if !h.event(event) {
			t.Fatalf("event %s not taken", event)
		}
	}
	h.synced()
	node := "selector.1"
	want := []fe.Action{
		{Node: node, Kind: fe.Highlight, Item: "b/two"},
		{Node: node, Kind: fe.Choose, Item: "b/three"},
		{Node: node, Kind: fe.Choose},
		{Node: node, Kind: fe.Dismiss},
		{Node: node, Kind: fe.Filter},
		{Node: node, Kind: fe.SwitchTab, Item: "scoped"},
		{Node: node, Kind: fe.Choose},
		{Node: node, Kind: fe.Dismiss},
	}
	if !reflect.DeepEqual(h.actions, want) {
		t.Fatalf("actions = %#v", h.actions)
	}
	h.applyOps(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "selector.1", Index: 1})
	h.event(`{"ev":"select",` + sheet + `,"item":"b/two"}`)
	h.synced()
	if len(h.actions) != len(want) {
		t.Fatalf("action after close: %#v", h.actions[len(want):])
	}
}

func sampleSettings() fe.Settings {
	return fe.Settings{
		Searchable: true,
		Items: []fe.Setting{
			{ID: "auto", Label: "Auto-compact", Description: "Compact when full", Value: "true", Values: []string{"true", "false"}},
			{ID: "mode", Label: "Mode", Value: "b", Values: []string{"a", "b", "c"}},
			{ID: "theme", Label: "Theme", Value: "dark", Values: []string{"dark", "light"}, Submenu: true},
		},
		Selected: "mode",
	}
}

// Settings are a one-page prefs sheet in the layer: a true/false setting is
// a switch, a submenu an action button showing its value, any other a
// choice; the selected setting has the focus ring.
func TestSettingsArePrefsSheet(t *testing.T) {
	h := dockWithLines(t)
	ops := h.applyOps(selectorOp(fe.Insert, "selector.1", sampleSettings()))
	n := node(ops, "layer.selector.1")
	p := props(n)
	if n["k"] != "prefs" || p["page"] != "settings" || p["focus"] != "mode" || p["title"] != "Settings" {
		t.Fatalf("prefs = %#v", n)
	}
	rows := p["sections"].([]any)[0].(map[string]any)["rows"].([]any)
	controls := []any{
		map[string]any{"k": "switch", "on": true},
		map[string]any{"k": "choice", "value": "b", "style": "auto", "options": []any{map[string]any{"value": "a"}, map[string]any{"value": "b"}, map[string]any{"value": "c"}}},
		map[string]any{"k": "action", "act": "open", "label": "dark"},
	}
	for i, row := range rows {
		if !reflect.DeepEqual(row.(map[string]any)["control"], controls[i]) {
			t.Fatalf("row %d control = %#v", i, row)
		}
	}
	if rows[0].(map[string]any)["hint"] != "Compact when full" {
		t.Fatalf("hint = %#v", rows[0])
	}
	if !reflect.DeepEqual(ops[len(ops)-1], []any{"focus", nil}) {
		t.Fatalf("focus = %#v", ops)
	}
}

// Prefs events become actions: a switch or choice change sets the value, a
// Reset sets nothing, a row click highlights, a submenu button chooses its
// row and Close dismisses. A popup click opens its menu locally, and a
// change or a click outside closes it. When the settings open a submenu,
// the sheet becomes a picker with the picker's handlers.
func TestPrefsEventsBecomeActions(t *testing.T) {
	h := dockWithLines(t)
	h.applyOps(selectorOp(fe.Insert, "selector.1", sampleSettings()))
	sheet := `"sf":"pig","id":"layer.selector.1"`
	for _, event := range []string{
		`{"ev":"change",` + sheet + `,"item":"auto","value":false}`,
		`{"ev":"change",` + sheet + `,"item":"mode","value":"c"}`,
		`{"ev":"change",` + sheet + `,"item":"mode","value":null}`,
		`{"ev":"select",` + sheet + `,"item":"auto"}`,
		`{"ev":"select",` + sheet + `,"item":"mode=a"}`,
		`{"ev":"action",` + sheet + `,"act":"open","value":"theme"}`,
		`{"ev":"action",` + sheet + `,"act":"close"}`,
	} {
		h.event(event)
	}
	h.synced()
	node := "selector.1"
	want := []fe.Action{
		{Node: node, Kind: fe.SetValue, Item: "auto", Value: "false"},
		{Node: node, Kind: fe.SetValue, Item: "mode", Value: "c"},
		{Node: node, Kind: fe.Highlight, Item: "auto"},
		{Node: node, Kind: fe.Choose, Item: "theme"},
		{Node: node, Kind: fe.Dismiss},
	}
	if !reflect.DeepEqual(h.actions, want) {
		t.Fatalf("actions = %#v", h.actions)
	}

	h.messages()
	h.event(`{"ev":"activate",` + sheet + `,"item":"mode"}`)
	h.synced()
	got := h.frames()
	if len(got) != 1 || !reflect.DeepEqual(got[0].body["ops"], jsonOps(t, []any{"set", "layer.selector.1", map[string]any{"editing": map[string]any{"row": "mode"}}})) {
		t.Fatalf("menu open = %#v", got)
	}
	h.event(`{"ev":"action",` + sheet + `,"act":"close","value":"mode"}`)
	h.synced()
	got = h.frames()
	if len(got) != 1 || !reflect.DeepEqual(got[0].body["ops"], jsonOps(t, []any{"set", "layer.selector.1", map[string]any{"editing": nil}})) {
		t.Fatalf("menu close = %#v", got)
	}
	if len(h.actions) != len(want) {
		t.Fatalf("menu clicks acted: %#v", h.actions[len(want):])
	}

	ops := h.applyOps(selectorOp(fe.Update, "selector.1", sampleSelector()))
	if len(ops) < 2 || ops[0].([]any)[0] != "del" || ops[1].([]any)[4].(map[string]any)["k"] != "picker" {
		t.Fatalf("submenu = %#v", ops)
	}
	if set := opsOn(ops, "layer.selector.1"); len(set) != 3 || set[2].([]any)[0] != "set" {
		t.Fatalf("submenu raw props = %#v", ops)
	}
	h.event(`{"ev":"select",` + sheet + `,"item":"b/two"}`)
	h.synced()
	if last := h.actions[len(h.actions)-1]; last != (fe.Action{Node: node, Kind: fe.Highlight, Item: "b/two"}) {
		t.Fatalf("submenu action = %#v", last)
	}
}

// Overlays are cards in the layer: modal unless they leave the keys, at the
// top, center or bottom as Pi anchors them, in the smallest card that holds
// their width, with their lines as ANSI and the role pig.lines, which the
// overlay sheet's full-sheet rules hold. An overlay that draws its view has
// no role, so a full sheet of native elements keeps Tern's padding, unless
// it is a full sheet whose view keeps a row of lines wider than the padded
// body holds; one that turns from lines to its view drops the role, and
// back. They stay below selector sheets, and a change to their lines
// replaces the text.
func TestOverlaysAreLayerCards(t *testing.T) {
	h := dockWithLines(t)
	h.applyOps(selectorOp(fe.Insert, "selector.1", sampleSelector()))
	overlay := fe.Overlay{Lines: []string{"custom", "\x1b[1mbold\x1b[0m"}, Width: 30, Anchor: "top-right"}
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: overlay})
	want := jsonOps(t, []any{"add", "layer.overlay.1", "layer", "layer.selector.1", map[string]any{
		"id": "layer.overlay.1", "k": "overlay",
		"p": map[string]any{"key": "overlay.1", "modal": true, "anchor": "top", "size": "sm", "role": "pig.lines"},
		"c": []any{map[string]any{"id": "layer.overlay.1.lines", "k": "ansi", "p": map[string]any{"key": "lines", "text": "custom\n\x1b[1mbold\x1b[0m"}}},
	}})
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("overlay = %#v", ops)
	}
	for _, tc := range []struct {
		overlay fe.Overlay
		cols    int
		anchor  string
		size    string
		modal   any
	}{
		{fe.Overlay{Width: 36, Anchor: "center"}, 150, "center", "sm", true},
		{fe.Overlay{Width: 37, Anchor: "center"}, 150, "center", "md", true},
		{fe.Overlay{Width: 56, Anchor: "bottom-left", NonCapturing: true}, 150, "bottom", "md", nil},
		{fe.Overlay{Width: 57, Anchor: "bottom-left", NonCapturing: true}, 150, "bottom", "lg", nil},
		{fe.Overlay{Width: 86, Anchor: "top-center"}, 150, "top", "lg", true},
		{fe.Overlay{Width: 87, Anchor: "top-center"}, 150, "top", "full", true},
		{fe.Overlay{Width: 149, Anchor: "left-center"}, 150, "center", "full", true},
		// A narrow layer narrows the cards: 64 cells fit lg in a 150-column
		// pane, not in a 68-column one.
		{fe.Overlay{Width: 64, Anchor: "center"}, 150, "center", "lg", true},
		{fe.Overlay{Width: 61, Anchor: "center"}, 68, "center", "lg", true},
		{fe.Overlay{Width: 62, Anchor: "center"}, 68, "center", "full", true},
		// Before Tern says the pane's width, the cards count only their own.
		{fe.Overlay{Width: 86, Anchor: "center"}, 0, "center", "lg", true},
	} {
		p := overlayElement("x", tc.overlay, tc.cols, nil).Node().Props
		if p["anchor"] != tc.anchor || p["size"] != tc.size || p["modal"] != tc.modal || p["role"] != "pig.lines" {
			t.Fatalf("%#v in %d columns = %#v", tc.overlay, tc.cols, p)
		}
		if p := overlayElement("x", tc.overlay, tc.cols, ui.Text{}).Node().Props; p["role"] != nil || p["size"] != tc.size {
			t.Fatalf("%#v with a view body in %d columns = %#v", tc.overlay, tc.cols, p)
		}
	}
	// A full sheet whose view keeps a row of lines wider than its padded
	// body holds (143 columns of a 150-column pane) is one of lines; a
	// narrower row, a hidden one, a card's, or one in a pane of no known
	// width leaves it native.
	ansi := func(cells int, hidden bool) tern.Node {
		return tern.Node{Kind: tern.KindAnsi, Props: hiddenIf(map[string]any{"text": "a\n" + strings.Repeat("─", cells)}, hidden)}
	}
	col := func(children ...tern.Element) tern.Node { return tern.Node{Kind: tern.KindCol, Children: children} }
	for _, tc := range []struct {
		what  string
		width int
		cols  int
		body  tern.Node
		role  any
	}{
		{"a row of 144 cells", 149, 150, col(ansi(144, false)), "pig.lines"},
		{"a row of 144 cells in a row", 149, 150, col(tern.Node{Kind: tern.KindRow, Children: []tern.Element{ansi(3, false), ansi(144, false)}}), "pig.lines"},
		{"a row of 62 cells in 68 columns", 62, 68, col(ansi(62, false)), "pig.lines"},
		{"a row of 143 cells", 149, 150, col(ansi(143, false)), nil},
		{"a row of 61 cells in 68 columns", 62, 68, col(ansi(61, false)), nil},
		{"a hidden row of 150 cells", 149, 150, col(ansi(150, true)), nil},
		{"a row of 150 cells under a hidden col", 149, 150, col(tern.Node{Kind: tern.KindCol, Props: map[string]any{"hidden": true}, Children: []tern.Element{ansi(150, false)}}), nil},
		{"a row of 150 cells in an lg card", 64, 150, col(ansi(150, false)), nil},
		{"a row of 150 cells in a pane of no known width", 149, 0, col(ansi(150, false)), nil},
	} {
		if p := overlayElement("x", fe.Overlay{Width: tc.width}, tc.cols, tc.body).Node().Props; p["role"] != tc.role {
			t.Errorf("%s: %#v", tc.what, p)
		}
	}
	view, viewLines := overlayView("tracks")
	withView := fe.Overlay{Lines: viewLines, Width: 30, Anchor: "top-right", View: view}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionOverlay, ID: "overlay.1", Node: withView})
	if want := []any{"set", "layer.overlay.1", map[string]any{"role": nil}}; !slices.ContainsFunc(ops, func(op any) bool { return reflect.DeepEqual(op, want) }) {
		t.Fatalf("overlay to its view = %#v", ops)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionOverlay, ID: "overlay.1", Node: overlay})
	if want := []any{"set", "layer.overlay.1", map[string]any{"role": "pig.lines"}}; !slices.ContainsFunc(ops, func(op any) bool { return reflect.DeepEqual(op, want) }) {
		t.Fatalf("overlay back to its lines = %#v", ops)
	}
	overlay.Lines = []string{"changed"}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionOverlay, ID: "overlay.1", Node: overlay})
	if want := jsonOps(t, []any{"text", "layer.overlay.1.lines", "replace", "changed"}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("overlay update = %#v", ops)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.0", Index: 0, Node: overlay})
	if ops[0].([]any)[3] != "layer.overlay.1" {
		t.Fatalf("overlay below another = %#v", ops)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Remove, Region: fe.RegionOverlay, ID: "overlay.1", Index: 1})
	if want := jsonOps(t, []any{"del", "layer.overlay.1"}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("overlay remove = %#v", ops)
	}
}

// An overlay's lines must fit the card's ansi block, which wraps at its
// width. Each case is a geometry measured in Tern 0.4.5 (pane width and font
// size, under Reader and Spine, anchored at the center, top and bottom
// alike), with the pane's cols and the pty's rows, the columns the sm, md
// and lg cards' blocks held (0: not measured), and the columns and lines a
// full sheet held under the overlay sheet (without it: a column or two
// fewer than cols, and a line fewer than rows at 16 and 20px). No width the
// member puts in a card is wider than its block, and a full sheet holds an
// extension's overlay over the whole terminal, cols by rows.
func TestOverlayCardsHoldTheirWidth(t *testing.T) {
	for _, tc := range []struct {
		what              string
		cols, rows        int
		sm, md, lg        int
		fullCols, fullRow int
	}{
		{"560px 13px", 68, 45, 39, 60, 65, 70, 46},
		{"1200px 13px", 150, 45, 39, 60, 91, 152, 46},
		{"1400px 13px", 176, 45, 39, 60, 91, 178, 46},
		{"560px 11px", 80, 56, 39, 60, 78, 83, 57},
		{"1200px 11px", 178, 56, 39, 60, 91, 180, 57},
		{"1400px 11px", 208, 56, 39, 60, 91, 211, 57},
		{"560px 16px", 55, 37, 39, 52, 52, 56, 38},
		{"1200px 16px", 122, 37, 39, 60, 91, 123, 38},
		{"1400px 16px", 143, 37, 39, 60, 91, 144, 38},
		{"560px 20px", 44, 29, 39, 41, 41, 45, 29},
		{"1200px 20px", 97, 29, 39, 60, 91, 98, 29},
		{"1400px 20px", 114, 29, 39, 60, 91, 115, 29},
		{"560px 24px", 36, 23, 33, 0, 0, 37, 24},
		{"1200px 24px", 81, 23, 39, 0, 0, 82, 24},
		{"1400px 24px", 95, 23, 39, 0, 0, 96, 24},
	} {
		if tc.fullCols < tc.cols || tc.fullRow < tc.rows {
			t.Fatalf("%s: a full sheet holds %d by %d of %d by %d", tc.what, tc.fullCols, tc.fullRow, tc.cols, tc.rows)
		}
		held := map[any]int{"sm": tc.sm, "md": tc.md, "lg": tc.lg}
		for width := 1; width <= tc.cols; width++ {
			size := overlayElement("x", fe.Overlay{Width: width}, tc.cols, nil).Node().Props["size"]
			if size != "full" && held[size] > 0 && width > held[size] {
				t.Fatalf("%s: %d columns in %s, whose block holds %d", tc.what, width, size, held[size])
			}
		}
	}
}

// A terminal cuts an overlay's lines at its width, as pig-music's quick
// settings showed: their highlighted row ran across the whole terminal and
// wrapped in the card. The cut keeps every escape sequence, so the colors
// and links end as the whole line's do; a wide character that does not fit
// goes. An overlay as wide as the dock spans the pane's cols, at which an
// extension's overlay over the whole terminal renders.
func TestOverlayLinesAreCutAtTheirWidth(t *testing.T) {
	for _, tc := range []struct {
		what        string
		width, cols int
		lines       []string
		want        string
	}{
		{"a highlighted row", 10, 137, []string{"\x1b[7mabcdefghijklmnop\x1b[0m", "short"}, "\x1b[7mabcdefghij\x1b[0m\nshort"},
		{"a wide character over the edge", 3, 137, []string{"ab中d"}, "ab"},
		{"a link", 2, 137, []string{"\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\"}, "\x1b]8;;https://x\x1b\\li\x1b]8;;\x1b\\"},
		{"the dock's width spans the pane", 136, 137, []string{strings.Repeat("x", 137), strings.Repeat("y", 138)}, strings.Repeat("x", 137) + "\n" + strings.Repeat("y", 137)},
		{"no width", 0, 137, []string{"abc"}, "abc"},
	} {
		el := buildNode(overlayElement("x", fe.Overlay{Lines: tc.lines, Width: tc.width}, tc.cols, nil))
		if got := el.Children[0].(tern.Node).Props["text"]; got != tc.want {
			t.Errorf("%s: %q, want %q", tc.what, got, tc.want)
		}
	}
	// Lines that fit are not copied.
	lines := []string{"abc", "de"}
	if got := clipLines(lines, 3); &got[0] != &lines[0] {
		t.Fatal("lines that fit were copied")
	}
}

// The overlay sheet goes out once a surface's layer holds an overlay,
// before the frame that adds it, and not again; a session that shows no
// overlay sends none, and a new surface gets it again with its overlays.
func TestOverlaySheetGoesBeforeTheFirstOverlay(t *testing.T) {
	h := ready(t)
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"status"}}})
	for _, m := range h.messages() {
		if m.verb == "s" {
			t.Fatalf("a sheet with no overlay: %v", m.body)
		}
	}
	h.ack()
	// The rules hold full sheets of lines only (role pig.lines).
	sheet := `s {"css":".sf-overlay.sz-full[data-role='pig.lines']{padding:2px}.sf-overlay.sz-full[data-role='pig.lines'] .sf-ov-body{padding:0}","name":"pig-overlay","sf":"pig"}`
	full := fe.Overlay{Lines: []string{"x"}, Width: 136, Anchor: "top-left"}
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: full})
	expectWire(t, "the first overlay", h.messages(), sheet,
		`f {"ops":[["add","layer.overlay.1","layer",null,{"c":[{"id":"layer.overlay.1.lines","k":"ansi","p":{"key":"lines","text":"x"}}],"id":"layer.overlay.1","k":"overlay","p":{"anchor":"top","key":"overlay.1","modal":true,"role":"pig.lines","size":"full"}}]],"s":3,"sf":"pig"}`)
	h.ack()
	h.apply(fe.Op{Kind: fe.Remove, Region: fe.RegionOverlay, ID: "overlay.1"})
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.2", Node: full})
	h.ack()
	// A surface adopted after a handoff keeps its sheets.
	h.session.Suspend()
	h.session.Resume()
	for _, m := range h.messages() {
		if m.verb == "s" {
			t.Fatalf("the sheet again: %v", m.body)
		}
	}
	h.session.mu.Lock()
	h.session.credits = 2
	h.session.mu.Unlock()
	h.event(`{"ev":"gone","ids":["pig"]}`)
	h.eventually("the new surface", func() bool { return strings.Contains(h.out.peek(), `"layer.overlay.2"`) })
	msgs := h.messages()
	if len(msgs) < 3 || msgs[0].verb != "o" || wire(t, msgs[1:2]) != sheet || !strings.Contains(wire(t, msgs[2:]), `"layer.overlay.2"`) {
		t.Fatalf("reopen = %s", wire(t, msgs))
	}
}

// A card holds columns of the pane, so a resize that narrows the pane past
// an overlay's card turns it into a full sheet, and back.
func TestOverlayCardFollowsThePane(t *testing.T) {
	h := ready(t)
	overlay := fe.Overlay{Lines: []string{"x"}, Width: 64, Anchor: "center"}
	if p := props(node(h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: overlay}), "layer.overlay.1")); p["size"] != "lg" {
		t.Fatalf("in 137 columns = %v", p)
	}
	for _, tc := range []struct {
		cols int
		size string
	}{{68, "full"}, {137, "lg"}} {
		h.event(`{"ev":"resize","cols":` + itoa(int64(tc.cols)) + `,"cell":{"w":8,"h":16},"visible":true}`)
		if ops, want := h.applyOps(), jsonOps(t, []any{"set", "layer.overlay.1", map[string]any{"size": tc.size}}); !reflect.DeepEqual(ops, want) {
			t.Fatalf("in %d columns = %#v", tc.cols, ops)
		}
	}
}

// A hidden agent's startup still fits Tern's credits with a selector open.
func TestHiddenStartupWithASelectorStaysTwoFrames(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":2`)
	h.messages()
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Node: fe.Editor{}})
	h.session.InputReady()
	sel := sampleSelector()
	for i := range 3 {
		kind := fe.Update
		if i == 0 {
			kind = fe.Insert
		}
		sel.Selected = sel.Items[i].ID
		h.apply(selectorOp(kind, "selector.1", sel))
	}
	got := h.frames()
	if len(got) == 0 || len(got) > 2 {
		t.Fatalf("frames = %d, want one or two", len(got))
	}
	data, _ := json.Marshal(got)
	if !strings.Contains(string(data), `"picker"`) {
		t.Fatalf("frames = %s", data)
	}
}
