package frontend

import (
	"reflect"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

func TestOmpBrandSheetWelcomeLockupNoClipping(t *testing.T) {
	sheet := ompBrandSheet(fe.Mark{})
	for _, want := range []string{
		`[data-role='omp.welcome.lockup']{height:auto;overflow:visible;align-items:center}`,
		`.sf-text[data-role='omp.welcome.wordmark']{line-height:1.15;overflow:visible}`,
		`[data-role='omp.welcome.logo']{cursor:default;pointer-events:none;margin-top:2px}`,
		`[data-role='omp.welcome.logo'] .sf-img-svg{width:48px !important;height:44px !important}`,
		`[data-role='omp.welcome.logo'] .omp-pi{fill:light-dark(#1b1c20,#eedaad)}`,
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("ompBrandSheet missing %q", want)
		}
	}
	// Spine overrides (`.sf-chat-spine [data-role='omp.welcome.lockup']{height:28px}`)
	// must not be clobbered by min-height or !important rules.
	for _, bad := range []string{"min-height:52px", "line-height:1.15 !important", "height:auto !important"} {
		if strings.Contains(sheet, bad) {
			t.Errorf("ompBrandSheet must not contain %q: %s", bad, sheet)
		}
	}
}

func TestOmpDiscoverySectionsFramedWithDividers(t *testing.T) {
	h := readyOmpRoles(t)
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d0", Index: 0, Node: fe.Lines{
			Lines: []string{"\x1b[1m[Context]\x1b[0m", "  AGENTS.md"},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "sp0", Index: 1, Node: fe.Lines{
			Lines: []string{""},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d1", Index: 2, Node: fe.Lines{
			Lines: []string{"\x1b[1m[Skills]\x1b[0m", "  rex"},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "sp1", Index: 3, Node: fe.Lines{
			Lines: []string{""},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d2", Index: 4, Node: fe.Lines{
			Lines: []string{"\x1b[1m[Extensions]\x1b[0m", "  builtin:piglet"},
		}},
	)
	top := node(ops, "omp.discovery.top")
	bot := node(ops, "omp.discovery.bottom")
	if top == nil || top["k"] != "rule" || props(top)["role"] != "omp.divider" {
		t.Fatalf("top divider = %#v, want rule[omp.divider]", top)
	}
	if bot == nil || bot["k"] != "rule" || props(bot)["role"] != "omp.divider" {
		t.Fatalf("bottom divider = %#v, want rule[omp.divider]", bot)
	}
	// Count rule adds: must be exactly 2 (top and bottom), never duplicated per section.
	ruleAdds := 0
	for _, op := range ops {
		arr, ok := op.([]any)
		if ok && len(arr) >= 3 && arr[0] == "add" {
			if m, ok := arr[len(arr)-1].(map[string]any); ok && m["k"] == "rule" {
				ruleAdds++
			}
		}
	}
	if ruleAdds != 2 {
		t.Fatalf("ruleAdds = %d, want 2 (top + bottom)", ruleAdds)
	}
	// Appending a normal user message after discovery does NOT duplicate dividers.
	ops2 := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u1", Index: 5, Node: fe.MarkdownText{
		Role: fe.RoleUser, Text: "hello",
	}})
	if node(ops2, "omp.discovery.top") != nil {
		t.Fatalf("top divider should not be re-added on user message: %#v", ops2)
	}

	// Removing all discovery sections cleans up both dividers.
	ops3 := h.applyOps(
		fe.Op{Kind: fe.Remove, Region: fe.RegionMain, ID: "d2", Index: 4},
		fe.Op{Kind: fe.Remove, Region: fe.RegionMain, ID: "d1", Index: 2},
		fe.Op{Kind: fe.Remove, Region: fe.RegionMain, ID: "d0", Index: 0},
	)
	var delTop, delBot bool
	for _, op := range ops3 {
		arr, ok := op.([]any)
		if ok && len(arr) >= 2 && arr[0] == "del" {
			if arr[1] == "omp.discovery.top" {
				delTop = true
			}
			if arr[1] == "omp.discovery.bottom" {
				delBot = true
			}
		}
	}
	if !delTop || !delBot {
		t.Fatalf("expected both discovery dividers deleted on removal, got %#v", ops3)
	}

	// Reload scenario: clear existing discovery sections and re-insert new ones
	// in a single frame ahead of an existing user card -> exact order must be
	// omp.discovery.top, new discovery sections, omp.discovery.bottom, user card.
	h2 := readyOmpRoles(t)
	h2.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d0", Index: 0, Node: fe.Lines{
			Lines: []string{"[Context]", "  AGENTS.md"},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u1", Index: 1, Node: fe.MarkdownText{
			Role: fe.RoleUser, Text: "hi",
		}},
	)
	reloadOps := h2.applyOps(
		fe.Op{Kind: fe.Remove, Region: fe.RegionMain, ID: "d0", Index: 0},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d1", Index: 0, Node: fe.Lines{
			Lines: []string{"[Context]", "  AGENTS.md"},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d2", Index: 1, Node: fe.Lines{
			Lines: []string{"[Extensions]", "  builtin:piglet"},
		}},
	)
	// Replay main ops into an ordered slice to verify exact sibling order.
	mainOrder := []string{"omp.discovery.top", "n.d0", "omp.discovery.bottom", "n.u1"}
	for _, op := range reloadOps {
		arr, ok := op.([]any)
		if !ok || len(arr) < 2 {
			continue
		}
		switch arr[0] {
		case "del":
			id, _ := arr[1].(string)
			for i, cur := range mainOrder {
				if cur == id {
					mainOrder = append(mainOrder[:i], mainOrder[i+1:]...)
					break
				}
			}
		case "add":
			// TSP wire format: ["add", id, parent, before, node] (or ["add", id, parent, node])
			if len(arr) < 4 || arr[2] != "main" {
				continue
			}
			id, _ := arr[1].(string)
			before := ""
			if len(arr) == 5 {
				before, _ = arr[3].(string)
			}
			if before == "" {
				mainOrder = append(mainOrder, id)
			} else {
				idx := len(mainOrder)
				for i, cur := range mainOrder {
					if cur == before {
						idx = i
						break
					}
				}
				mainOrder = append(mainOrder[:idx], append([]string{id}, mainOrder[idx:]...)...)
			}
		}
	}
	wantOrder := []string{"omp.discovery.top", "n.d1", "n.d2", "omp.discovery.bottom", "n.u1"}
	if !reflect.DeepEqual(mainOrder, wantOrder) {
		t.Fatalf("post-reload main order = %v, want %v (ops=%#v)", mainOrder, wantOrder, reloadOps)
	}
}
func TestOmpAutocompleteOverlayFromDockBelow(t *testing.T) {
	h := readyOmpRoles(t)
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{
			Text: "/se", Cursor: 3, Sendable: true,
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock.below", Index: 1, Node: fe.Lines{
			Lines: []string{
				"\x1b[36m→ settings            Open settings menu\x1b[0m",
				"  session             \x1b[2mShow session info\x1b[0m",
				"  (1/96)",
			},
		}},
	)

	// overlay[omp.autocomplete] mounted in layer, anchored to editor caret.
	ov := node(ops, "layer.complete")
	if ov == nil || ov["k"] != "overlay" || props(ov)["role"] != "omp.autocomplete" {
		t.Fatalf("autocomplete overlay = %#v", ov)
	}
	if anchor, ok := props(ov)["anchor"].(map[string]any); !ok || anchor["caret"] != "dock.editor.editor.line.input" {
		t.Fatalf("overlay anchor = %#v, want caret=dock.editor.editor.line.input", props(ov)["anchor"])
	}
	selCol := node(ops, "layer.complete.select")
	if selCol == nil || selCol["k"] != "col" || props(selCol)["role"] != "omp.select" {
		t.Fatalf("omp.select col = %#v", selCol)
	}
	list := node(ops, "layer.complete.select.list")
	if list == nil || list["k"] != "list" || props(list)["selected"] != "layer.complete.select.list.settings" {
		t.Fatalf("autocomplete list = %#v, want selected=layer.complete.select.list.settings", list)
	}
	item0 := node(ops, "layer.complete.select.list.settings")
	if item0 == nil || item0["k"] != "item" || props(item0)["icon"] != "settings" {
		t.Fatalf("settings item = %#v", item0)
	}
	// Prefix "se" marked with {s:"mark"} in label spans.
	wantLabel := []any{
		map[string]any{"t": "se", "s": "mark"},
		map[string]any{"t": "ttings"},
	}
	if !reflect.DeepEqual(props(item0)["label"], wantLabel) {
		t.Fatalf("settings label = %#v, want %#v", props(item0)["label"], wantLabel)
	}
	wantDetail := []any{map[string]any{"t": "Open settings menu", "s": "muted"}}
	if !reflect.DeepEqual(props(item0)["detail"], wantDetail) {
		t.Fatalf("settings detail = %#v, want %#v", props(item0)["detail"], wantDetail)
	}
	status := node(ops, "layer.complete.select.status")
	if status == nil || status["k"] != "text" {
		t.Fatalf("status node = %#v", status)
	}
	// Raw ANSI dock.below must NOT be rendered while overlay is active.
	if raw := node(ops, "dock.below.dock.below"); raw != nil {
		t.Fatalf("raw dock.below should be hidden while autocomplete overlay is active: %#v", raw)
	}

	// Clicking an item (Tern sends the full item node ID) sends Env.Edit completing the slash command.
	h.event(`{"ev":"select","sf":"pig","id":"layer.complete.select.list","item":"layer.complete.select.list.session"}`)
	h.synced()
	if want := []fe.Edit{{From: 0, To: 3, Text: "/session ", Cursor: 9, Len: 3}}; !reflect.DeepEqual(h.edits, want) {
		t.Fatalf("click edits = %#v, want %#v", h.edits, want)
	}

	// @ file completion with duplicate basenames must generate unique child keys
	// without crashing Apply, set selected to the full node ID, and clicking an
	// @ file row must NOT corrupt `@src/ma` into `@main.go`.
	h.edits = nil
	opsAt := h.applyOps(
		fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{
			Text: "open @src/ma", Cursor: 12, Sendable: true,
		}},
		fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "dock.below", Index: 1, Node: fe.Lines{
			Lines: []string{
				"  main.go             cmd/a/main.go",
				"→ main.go             cmd/b/main.go",
				"  man/                Directory",
			},
		}},
	)
	if h.session.fellBack {
		t.Fatal("duplicate autocomplete labels caused fallback")
	}
	fileItem0 := node(opsAt, "layer.complete.select.list.main.go")
	if fileItem0 == nil || props(fileItem0)["icon"] != "file" {
		t.Fatalf("@file item0 = %#v, want icon=file", fileItem0)
	}
	fileItem1 := node(opsAt, "layer.complete.select.list.main.go#1")
	if fileItem1 == nil || props(fileItem1)["icon"] != "file" {
		t.Fatalf("@file item1 = %#v, want unique suffixed key main.go#1", fileItem1)
	}
	dirItem := node(opsAt, "layer.complete.select.list.man/")
	if dirItem == nil || props(dirItem)["icon"] != "folder" {
		t.Fatalf("@dir item = %#v, want icon=folder", dirItem)
	}
	listSet := opsOn(opsAt, "layer.complete.select.list")
	sawSelectedFullID := false
	for _, op := range listSet {
		arr := op.([]any)
		if arr[0] == "set" && arr[2].(map[string]any)["selected"] == "layer.complete.select.list.main.go#1" {
			sawSelectedFullID = true
		}
	}
	if !sawSelectedFullID {
		t.Fatalf("expected selected=layer.complete.select.list.main.go#1, got ops=%#v", listSet)
	}
	h.event(`{"ev":"select","sf":"pig","id":"layer.complete.select.list","item":"layer.complete.select.list.main.go#1"}`)
	h.synced()
	if len(h.edits) != 0 {
		t.Fatalf("clicking @ file row must not corrupt prompt with Env.Edit, got %#v", h.edits)
	}

	// Dismissing autocomplete (removing dock.below) removes overlay from layer.
	opsDismiss := h.applyOps(
		fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "dock.below", Index: 1},
	)
	var sawDel bool
	for _, op := range opsDismiss {
		arr, ok := op.([]any)
		if ok && len(arr) >= 2 && arr[0] == "del" && arr[1] == "layer.complete" {
			sawDel = true
		}
	}
	if !sawDel {
		t.Fatalf("expected layer.complete deleted on dismiss, got %#v", opsDismiss)
	}
}

func TestOmpComposerGhostPredictionAndTabRightAccept(t *testing.T) {
	h := readyOmpRoles(t)
	predView := &fe.View{
		Root: fe.ViewNode{
			Kind: fe.ViewKindContainer,
			ID:   "omp.composer.prediction",
			Rows: 0,
			Children: []fe.ViewNode{
				{Kind: fe.ViewKindText, ID: "omp.composer.prediction.choice#0", Text: "run the full test suite"},
				{Kind: fe.ViewKindText, ID: "omp.composer.prediction.choice#1", Text: "review the git diff"},
			},
		},
	}

	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Index: 0, Node: fe.Lines{
			View: predView,
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{
			Text: "", Cursor: 0, Sendable: true,
		}},
	)

	// Hidden prediction widget must NOT render any visible dock node.
	if n := node(ops, "dock.above.w0"); n != nil {
		t.Fatalf("prediction widget leaked visible dock node: %#v", n)
	}
	input := node(ops, "dock.editor.editor.line.input")
	if input == nil || props(input)["ghost"] != "run the full test suite" {
		t.Fatalf("editor ghost = %#v, want %q", props(input)["ghost"], "run the full test suite")
	}

	// Typing a case-insensitive prefix narrows ghost to remaining suffix via raw OpSet.
	ops2 := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{
		Text: "REV", Cursor: 3, Sendable: true,
	}})
	var gotGhost any
	for _, op := range opsOn(ops2, "dock.editor.editor.line.input") {
		arr := op.([]any)
		if arr[0] == "set" {
			if g, ok := arr[2].(map[string]any)["ghost"]; ok {
				gotGhost = g
			}
		}
	}
	if gotGhost != "iew the git diff" {
		t.Fatalf("prefix ghost = %#v, want %q (ops=%#v)", gotGhost, "iew the git diff", ops2)
	}

	// Right arrow accepts the remaining suffix via Env.Edit without submitting.
	if !h.session.HandleInput("\x1b[C") {
		t.Fatal("Right arrow should be consumed when ghost prediction is active")
	}
	if want := []fe.Edit{{From: 3, To: 3, Text: "iew the git diff", Cursor: 19, Len: 3}}; !reflect.DeepEqual(h.edits, want) {
		t.Fatalf("Right accept edits = %#v, want %#v", h.edits, want)
	}

	// Reset to empty editor, accept with Tab, then cycle to alternative with second Tab.
	h.edits = nil
	h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{
		Text: "", Cursor: 0, Sendable: true,
	}})
	if !h.session.HandleInput("\t") {
		t.Fatal("first Tab should accept choice[0]")
	}
	if want := []fe.Edit{{From: 0, To: 0, Text: "run the full test suite", Cursor: 23, Len: 0}}; !reflect.DeepEqual(h.edits, want) {
		t.Fatalf("first Tab edits = %#v, want %#v", h.edits, want)
	}
	// Simulate PiG applying the edit.
	h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{
		Text: "run the full test suite", Cursor: 23, Sendable: true,
	}})
	if !h.session.HandleInput("\t") {
		t.Fatal("second Tab should cycle to choice[1]")
	}
	if len(h.edits) != 2 || h.edits[1] != (fe.Edit{From: 0, To: 23, Text: "review the git diff", Cursor: 19, Len: 23}) {
		t.Fatalf("second Tab edits = %#v", h.edits)
	}

	// Fullscreen overlay (`s.screen != nil`) must NOT let Tab/Right steal keys.
	h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{
		Text: "", Cursor: 0, Sendable: true,
	}})
	h.session.mu.Lock()
	h.session.screen = &screen{}
	h.session.mu.Unlock()
	if h.session.HandleInput("\t") || h.session.HandleInput("\x1b[C") {
		t.Fatal("Tab/Right must not be consumed while fullscreen overlay screen is open")
	}
	h.session.mu.Lock()
	h.session.screen = nil
	h.session.mu.Unlock()

	// Clearing prediction widget clears ghost.
	h.applyOps(
		fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "dock", Index: 0},
	)
	if h.session.HandleInput("\t") {
		t.Fatal("Tab must not be consumed after prediction widget is removed")
	}
}

func TestOmpModelChipOmitsProviderPrefix(t *testing.T) {
	h := readyOmpRoles(t)
	h.session.modelDisplayName = func(id string) string {
		if id == "claude-opus-5-5" {
			return "Claude Opus 5.5"
		}
		return ""
	}
	ft := sampleFooter()
	ft.Model = "claude-opus-5-5"
	ft.Provider = "github-copilot"
	ft.Routed = fe.RoutedModel{}
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: ft},
	)
	nameNode := node(ops, "dock.editor.editor.bar.model.name")
	want := []any{map[string]any{"t": "Opus 5.5", "s": "statusLineModel"}}
	if !reflect.DeepEqual(props(nameNode)["spans"], want) {
		t.Fatalf("model chip spans = %#v, want %#v", props(nameNode)["spans"], want)
	}
}
