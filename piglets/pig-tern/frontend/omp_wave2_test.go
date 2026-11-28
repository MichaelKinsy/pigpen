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

func TestOmpDiscoverySectionsCollapsibleNoticesKeepSfFresh(t *testing.T) {
	h := readyOmpRoles(t)
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d0", Index: 0, Node: fe.Lines{
			Lines: []string{"\x1b[1m[Context]\x1b[0m", "  AGENTS.md"},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "sp0", Index: 1, Node: fe.Lines{
			Lines: []string{""},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d1", Index: 2, Node: fe.Lines{
			Lines: []string{"\x1b[1m[Skills]\x1b[0m", "  rex", "  review"},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "sp1", Index: 3, Node: fe.Lines{
			Lines: []string{""},
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d2", Index: 4, Node: fe.Lines{
			Lines: []string{"\x1b[1m[Extensions]\x1b[0m", "  builtin:piglet"},
		}},
	)
	// Every discovery section must become a collapsed section[omp.notice.discovery]
	// and blank spacers must become hidden spacer nodes (never plain ansi or rule
	// nodes) so Tern 0.9.27 activates `.sf-fresh` + `.sf-slot.open`.
	for _, id := range []string{"n.d0", "n.d1", "n.d2"} {
		n := node(ops, id)
		if n == nil || n["k"] != "section" {
			t.Fatalf("%s = %#v, want section", id, n)
		}
		pr := props(n)
		if pr["role"] != "omp.notice.discovery" || pr["collapsible"] != true || pr["collapsed"] != true {
			t.Fatalf("%s props = %#v, want role=omp.notice.discovery collapsible=true collapsed=true", id, pr)
		}
	}
	for _, id := range []string{"n.sp0", "n.sp1"} {
		sp := node(ops, id)
		if sp == nil || sp["k"] != "spacer" || props(sp)["hidden"] != true {
			t.Fatalf("%s = %#v, want hidden spacer", id, sp)
		}
	}
	if node(ops, "omp.discovery.top") != nil || node(ops, "omp.discovery.bottom") != nil {
		t.Fatalf("rule dividers must not be emitted under omp.notice.discovery: %#v", ops)
	}
	// Session file hidden node must use spacer kind under ompRoles so sf-fresh stays on.
	sf := "/tmp/session-fresh.jsonl"
	_ = h.out.take()
	if err := h.session.Apply(fe.Frame{SessionFile: &sf}); err != nil {
		t.Fatal(err)
	}
	var sfOps []any
	for _, f := range h.frames() {
		sfOps = append(sfOps, f.body["ops"].([]any)...)
	}
	sn := node(sfOps, "pig.session")
	if sn == nil || sn["k"] != "spacer" || props(sn)["hidden"] != true || props(sn)["role"] != "pig.session" || props(sn)["href"] != sf {
		t.Fatalf("pig.session node = %#v, want hidden spacer with role=pig.session href=%q", sn, sf)
	}
}

func TestOmpSurfaceTitleUsesPigletName(t *testing.T) {
	h := newHarnessEnv(t, map[string]string{"PIG_TERN_ROLES": "omp"})
	h.session.pigletName = "oh-my-pig"
	h.session.pigletVersion = "0.1.0"
	h.reply(`,"credits":64,"cols":120,"cell":{"w":8,"h":16}`)
	h.session.InputReady()
	h.apply()
	var openMsg *message
	for _, m := range h.messages() {
		if m.verb == "o" {
			cp := m
			openMsg = &cp
			break
		}
	}
	if openMsg == nil || openMsg.body["title"] != "oh-my-pig" {
		t.Fatalf("open message = %#v, want title \"oh-my-pig\"", openMsg)
	}
}

func TestOmpModelPickerRichPropsRailAndSlider(t *testing.T) {
	var gotThinking string
	catalog := map[string]ModelMeta{
		"github-copilot/claude-opus-4.6": {
			ID: "claude-opus-4.6", Provider: "github-copilot", Name: "Opus 4.6",
			ContextWindow: 200000, InputCostPer1M: 0, OutputCostPer1M: 0,
			Reasoning: true, ThinkingLevels: []string{"off", "low", "medium", "high"},
		},
		"anthropic/claude-sonnet-4": {
			ID: "claude-sonnet-4", Provider: "anthropic", Name: "Sonnet 4",
			ContextWindow: 200000, InputCostPer1M: 3, OutputCostPer1M: 15,
			Reasoning: true, ThinkingLevels: []string{"off", "minimal", "low", "medium", "high"},
		},
		"openai/gpt-4.1-mini": {
			ID: "gpt-4.1-mini", Provider: "openai", Name: "GPT-4.1 Mini",
			ContextWindow: 64000, InputCostPer1M: 0.4, OutputCostPer1M: 1.6,
			Reasoning: false,
		},
	}
	h := readyOmpRoles(t)
	h.session.modelMeta = func(provider, id string) (ModelMeta, bool) {
		m, ok := catalog[provider+"/"+id]
		return m, ok
	}
	h.session.onThinkingLevel = func(level string) { gotThinking = level }
	h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: fe.Footer{
			Model: "claude-opus-4.6", Provider: "github-copilot", ThinkingLevel: "medium",
			ContextUsage: fe.ContextUsage{Tokens: 100000, ContextWindow: 200000, Percent: 50},
		}},
	)
	sel := fe.Selector{
		Title:      "Select model",
		Searchable: true,
		Selected:   "github-copilot/claude-opus-4.6",
		Items: []fe.SelectorItem{
			{ID: "github-copilot/claude-opus-4.6", Label: "claude-opus-4.6", Detail: "github-copilot", Checked: true},
			{ID: "anthropic/claude-sonnet-4", Label: "claude-sonnet-4", Detail: "anthropic"},
			{ID: "openai/gpt-4.1-mini", Label: "gpt-4.1-mini", Detail: "openai"},
		},
	}
	ops := h.applyOps(
		fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "editor", Index: 0},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "selector.model", Index: 0, Node: sel},
	)
	p := node(ops, "layer.selector.model")
	if p == nil || p["k"] != "picker" {
		t.Fatalf("picker node = %#v", p)
	}
	pr := props(p)
	if pr["anchor"] != "composer" || pr["search"] != "list" || pr["density"] != "snug" || pr["scopeStyle"] != "rail" {
		t.Fatalf("picker shell props = %#v", pr)
	}
	if pr["title"] != nil {
		t.Fatalf("expected clean search-only head without title, got %v", pr["title"])
	}
	// Verify raw set op carries enriched items, scopes rail, grouped order, and level slider.
	var rawSet map[string]any
	for _, op := range ops {
		arr, ok := op.([]any)
		if ok && len(arr) >= 3 && arr[0] == "set" && arr[1] == "layer.selector.model" {
			rawSet, _ = arr[2].(map[string]any)
		}
	}
	if rawSet == nil {
		t.Fatalf("expected raw set op on layer.selector.model, got %#v", ops)
	}
	scopes, _ := rawSet["scopes"].([]any)
	if len(scopes) != 3 {
		t.Fatalf("scopes = %#v, want 3 provider rail entries", scopes)
	}
	level, _ := rawSet["level"].(map[string]any)
	if level == nil || level["label"] != "Thinking" || level["value"] != "medium" || level["detail"] != "github-copilot" {
		t.Fatalf("level slider = %#v", level)
	}
	order, _ := rawSet["order"].([]any)
	if len(order) < 5 {
		t.Fatalf("grouped order = %#v, want Recent + All headers and 3 rows", order)
	}
	if grp, _ := order[0].(map[string]any); grp["group"] != "recent" || grp["label"] != "Recent" {
		t.Fatalf("first order group = %#v, want Recent", order[0])
	}
	items, _ := rawSet["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items = %#v", items)
	}
	mini := items[2].(map[string]any)
	badges, _ := mini["badges"].([]any)
	if len(badges) == 0 || badges[0].(map[string]any)["text"] != "ctx>64k" {
		t.Fatalf("over-context model badges = %#v, want ctx>64k warning", badges)
	}
	// Clicking a provider scope filters order to only that provider.
	_ = h.out.take()
	h.session.ompModelPickerSetScope("selector.model", "provider:anthropic")
	var scopeOps []any
	for _, f := range h.frames() {
		scopeOps = append(scopeOps, f.body["ops"].([]any)...)
	}
	var scopeSet map[string]any
	for _, op := range scopeOps {
		if arr, ok := op.([]any); ok && len(arr) >= 3 && arr[0] == "set" && arr[1] == "layer.selector.model" {
			scopeSet, _ = arr[2].(map[string]any)
		}
	}
	if scopeSet == nil || scopeSet["scope"] != "provider:anthropic" {
		t.Fatalf("scope filter op = %#v", scopeOps)
	}
	if filteredOrder, _ := scopeSet["order"].([]any); len(filteredOrder) != 2 || filteredOrder[1] != "anthropic/claude-sonnet-4" {
		t.Fatalf("filtered order = %#v, want [All header, anthropic/claude-sonnet-4]", filteredOrder)
	}
	// Clicking the active provider scope again toggles back to "all".
	h.session.ompModelPickerSetScope("selector.model", "provider:anthropic")
	_ = h.out.take()
	// Clicking the thinking slider updates level and invokes OnThinkingLevel.
	h.session.ompModelPickerSetThinking("selector.model", "high")
	if gotThinking != "high" {
		t.Fatalf("OnThinkingLevel = %q, want high", gotThinking)
	}
	// Verify non-reasoning model shows disabled slider message.
	sel.Selected = "openai/gpt-4.1-mini"
	lvlOps := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "selector.model", Index: 0, Node: sel})
	var disabledLvl map[string]any
	for _, op := range lvlOps {
		if arr, ok := op.([]any); ok && len(arr) >= 3 && arr[0] == "set" && arr[1] == "layer.selector.model" {
			if m, ok := arr[2].(map[string]any); ok {
				disabledLvl, _ = m["level"].(map[string]any)
			}
		}
	}
	if disabledLvl == nil || disabledLvl["disabled"] != "GPT-4.1 Mini does not reason" {
		t.Fatalf("non-reasoning slider = %#v", disabledLvl)
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
