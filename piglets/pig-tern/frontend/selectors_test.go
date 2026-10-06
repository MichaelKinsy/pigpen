package frontend

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
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
// top, center or bottom as Pi anchors them, sized to their width, with
// their lines as ANSI. They stay below selector sheets, and a change to
// their lines replaces the text.
func TestOverlaysAreLayerCards(t *testing.T) {
	h := dockWithLines(t)
	h.applyOps(selectorOp(fe.Insert, "selector.1", sampleSelector()))
	overlay := fe.Overlay{Lines: []string{"custom", "\x1b[1mbold\x1b[0m"}, Width: 30, Anchor: "top-right"}
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: overlay})
	want := jsonOps(t, []any{"add", "layer.overlay.1", "layer", "layer.selector.1", map[string]any{
		"id": "layer.overlay.1", "k": "overlay",
		"p": map[string]any{"key": "overlay.1", "modal": true, "anchor": "top", "size": "sm"},
		"c": []any{map[string]any{"id": "layer.overlay.1.lines", "k": "ansi", "p": map[string]any{"key": "lines", "text": "custom\n\x1b[1mbold\x1b[0m"}}},
	}})
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("overlay = %#v", ops)
	}
	for _, tc := range []struct {
		overlay fe.Overlay
		anchor  string
		size    string
		modal   any
	}{
		{fe.Overlay{Width: 50, Anchor: "center"}, "center", "md", true},
		{fe.Overlay{Width: 80, Anchor: "bottom-left", NonCapturing: true}, "bottom", "lg", nil},
		{fe.Overlay{Width: 120, Anchor: "left-center"}, "center", "full", true},
	} {
		p := overlayElement("x", tc.overlay, 8).Node().Props
		if p["anchor"] != tc.anchor || p["size"] != tc.size || p["modal"] != tc.modal {
			t.Fatalf("%#v = %#v", tc.overlay, p)
		}
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
