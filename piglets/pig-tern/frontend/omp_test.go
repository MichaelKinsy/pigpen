package frontend

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// TestOmpComposerLayoutAndInteractions checks omp's composer under ompRoles:
// omp.editor col holding context meter, omp.composer.line with the editor
// input, and omp.composer.bar with model chip, effort chip, gap, usage, and
// send control; text/cursor/edit/focus route to dock.editor.line.input;
// model/effort clicks open PiG's /model and /thinking pickers; send click
// submits the draft; working turns omp.editor's tone to pending.
func TestOmpComposerLayoutAndInteractions(t *testing.T) {
	h := readyOmpRoles(t)
	footer := sampleFooter()
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Text: "hello", Cursor: 5, Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: footer},
	)
	// Under ompRoles, dock.footer is not mounted while the composer carries its facts.
	if ft := node(ops, "dock.footer"); ft != nil {
		t.Fatalf("dock.footer must not mount while composer is shown: %#v", ft)
	}

	// Fixed dock slot `dock.editor` was mounted at readyOmpRoles; inserting the
	// editor adds `col[omp.editor]` directly as child of `dock.editor`.
	col := node(ops, "dock.editor.editor")
	if col == nil || col["k"] != "col" || props(col)["role"] != "omp.editor" {
		t.Fatalf("composer root = %#v", col)
	}
	if _, hasTone := props(col)["tone"]; hasTone {
		t.Fatalf("idle composer should have no tone: %#v", props(col))
	}
	// Context meter above the input line, with status.context click action.
	ctx := node(ops, "dock.editor.editor.context")
	if ctx == nil || ctx["k"] != "meter" || props(ctx)["role"] != "omp.composer.context" ||
		props(ctx)["style"] != "bar" || props(ctx)["value"] != 0.25 ||
		props(ctx)["label"] != "25%" || props(ctx)["total"] != "200K" ||
		!reflect.DeepEqual(props(ctx)["actions"], map[string]any{"click": "status.context"}) {
		t.Fatalf("context meter = %#v", ctx)
	}

	// Input line and editor node.
	line := node(ops, "dock.editor.editor.line")
	if line == nil || line["k"] != "row" || props(line)["role"] != "omp.composer.line" {
		t.Fatalf("composer line = %#v", line)
	}
	input := node(ops, "dock.editor.editor.line.input")
	if input == nil || input["k"] != "editor" || props(input)["sendable"] != true ||
		props(input)["placeholder"] != ompComposerPlaceholder {
		t.Fatalf("composer input = %#v", input)
	}

	// Raw text, cursor, and focus ops target dock.editor.editor.line.input.
	doc := shownBy(ops)
	if doc["dock.editor.editor.line.input"]["text"] != "hello" || doc["dock.editor.editor.line.input"]["cursor"] != float64(5) {
		t.Fatalf("input text/cursor = %#v", doc["dock.editor.editor.line.input"])
	}
	foundFocus := false
	for _, op := range ops {
		if o := op.([]any); o[0] == "focus" && o[1] == "dock.editor.editor.line.input" {
			foundFocus = true
		}
	}
	if !foundFocus {
		t.Fatalf("missing focus on dock.editor.editor.line.input: %#v", ops)
	}

	// Composer bar children in omp order: model (with nested effort), extras, usage, send.
	bar := node(ops, "dock.editor.editor.bar")
	if bar == nil || bar["k"] != "row" || props(bar)["role"] != "omp.composer.bar" {
		t.Fatalf("composer bar = %#v", bar)
	}
	var barIDs []string
	for _, c := range bar["c"].([]any) {
		barIDs = append(barIDs, c.(map[string]any)["id"].(string))
	}
	if want := []string{
		"dock.editor.editor.bar.model",
		"dock.editor.editor.bar.extras",
		"dock.editor.editor.bar.usage",
		"dock.editor.editor.bar.send",
	}; !reflect.DeepEqual(barIDs, want) {
		t.Fatalf("bar children = %v, want %v", barIDs, want)
	}

	modelChip := node(ops, "dock.editor.editor.bar.model")
	if props(modelChip)["role"] != "omp.composer.model" {
		t.Fatalf("model chip = %#v", modelChip)
	}
	effortGlyph := node(ops, "dock.editor.editor.bar.model.effort")
	if effortGlyph == nil || effortGlyph["k"] != "effort" || props(effortGlyph)["role"] != "omp.composer.model.effort" || props(effortGlyph)["level"] != "high" {
		t.Fatalf("effort glyph = %#v", effortGlyph)
	}
	extras := node(ops, "dock.editor.editor.bar.extras")
	if extras == nil || extras["k"] != "status" || props(extras)["role"] != "omp.composer.extras" || props(extras)["transparent"] != true {
		t.Fatalf("extras = %#v", extras)
	}
	gitFact := node(ops, "dock.editor.editor.bar.extras.git")
	if gitFact == nil || gitFact["k"] != "seg" || props(gitFact)["role"] != "omp.composer.fact" {
		t.Fatalf("git fact = %#v", gitFact)
	}
	usageNode := node(ops, "dock.editor.editor.bar.usage")
	if usageNode == nil || usageNode["k"] != "text" || props(usageNode)["role"] != "omp.composer.usage" || props(usageNode)["text"] != ompSubscriptionIcon+" 0.01" {
		t.Fatalf("usage node = %#v", usageNode)
	}
	sendNode := node(ops, "dock.editor.editor.bar.send")
	if sendNode == nil || sendNode["k"] != "kbd" || props(sendNode)["role"] != "omp.composer.send" ||
		!reflect.DeepEqual(props(sendNode)["keys"], []any{"enter"}) {
		t.Fatalf("send node = %#v", sendNode)
	}

	// Typing updates patch dock.editor.editor.line.input without rebuilding the composer.
	renders := h.session.renders
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Text: "hello!", Cursor: 6, Sendable: true}})
	if want := jsonOps(t,
		[]any{"text", "dock.editor.editor.line.input", "append", "!"},
		[]any{"set", "dock.editor.editor.line.input", map[string]any{"cursor": 6}},
	); !reflect.DeepEqual(ops, want) {
		t.Fatalf("typing ops = %#v, want %#v", ops, want)
	}
	if h.session.renders != renders {
		t.Fatalf("typing triggered %d renders, want 0", h.session.renders-renders)
	}

	// Native edit, undo, send, and clicks on model/effort/context/send route properly.
	h.event(`{"ev":"edit","sf":"pig","id":"dock.editor.editor.line.input","from":0,"to":6,"text":"hi","cursor":2,"len":6}`)
	h.event(`{"ev":"undo","sf":"pig","id":"dock.editor.editor.line.input"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.bar.model","act":"status.model"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.bar.model.effort","act":"thinking.cycle"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.context","act":"status.context"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.bar.send","act":"submit"}`)
	h.synced()
	if want := []fe.Edit{{From: 0, To: 6, Text: "hi", Cursor: 2, Len: 6}}; !reflect.DeepEqual(h.edits, want) {
		t.Fatalf("edits = %#v, want %#v", h.edits, want)
	}
	if h.undos != 1 {
		t.Fatalf("undos = %d, want 1", h.undos)
	}
	if want := []string{"/model", "/thinking", "/context", "hello!"}; !reflect.DeepEqual(h.sends, want) {
		t.Fatalf("sends = %q, want %q", h.sends, want)
	}

	// Working state gives omp.editor tone="pending", swaps send -> text[omp.composer.stop],
	// allows PiG builtin slash commands (/model, /thinking) under bare ompRoles, and also
	// allows /context and /usage when running as a derived Piglet (ompDerived).
	// Removing Working restores send and clears the tone.
	ops = h.applyOps(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Working"}))
	if setOps := opsOn(ops, "dock.editor.editor"); len(setOps) == 0 || setOps[0].([]any)[2].(map[string]any)["tone"] != "pending" {
		t.Fatalf("working did not set composer tone=pending: %#v", ops)
	}
	stopNode := node(ops, "dock.editor.editor.bar.stop")
	if stopNode == nil || stopNode["k"] != "text" || props(stopNode)["role"] != "omp.composer.stop" ||
		props(stopNode)["text"] != "Stop" || props(stopNode)["tone"] != "error" || props(stopNode)["title"] != "Stop  esc" {
		t.Fatalf("working composer stop node = %#v", stopNode)
	}
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.bar.model","act":"status.model"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.bar.model.effort","act":"thinking.cycle"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.context","act":"status.context"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.bar.usage","act":"status.cost"}`)
	h.synced()
	h.mu.Lock()
	gotWhileWorking := append([]string(nil), h.sends[4:]...)
	h.mu.Unlock()
	if want := []string{"/model", "/thinking"}; !reflect.DeepEqual(gotWhileWorking, want) {
		t.Fatalf("bare ompRoles clicks while working sent %q, want %q", gotWhileWorking, want)
	}
	// Under ompDerived (Options.Omp = true), /context and /usage also work while busy.
	h.session.mu.Lock()
	h.session.ompDerived = true
	h.session.mu.Unlock()
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.context","act":"status.context"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.bar.usage","act":"status.cost"}`)
	h.synced()
	h.mu.Lock()
	gotDerivedWorking := append([]string(nil), h.sends[6:]...)
	h.mu.Unlock()
	if want := []string{"/context", "/usage"}; !reflect.DeepEqual(gotDerivedWorking, want) {
		t.Fatalf("ompDerived clicks while working sent %q, want %q", gotDerivedWorking, want)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "working"})
	if setOps := opsOn(ops, "dock.editor.editor"); len(setOps) == 0 || setOps[0].([]any)[2].(map[string]any)["tone"] != nil {
		t.Fatalf("stopping work did not clear composer tone: %#v", ops)
	}
	if sendBack := node(ops, "dock.editor.editor.bar.send"); sendBack == nil || sendBack["k"] != "kbd" {
		t.Fatalf("stopping work did not restore send control: %#v", ops)
	}

	// Updating Footer while the composer is mounted updates composer controls
	// and never emits raw ops targeting the unmounted dock.footer.
	footer.Model = "claude-opus"
	footer.Cwd = "~/src/other"
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "footer", Index: 1, Node: footer})
	for _, op := range ops {
		if id, ok := op.([]any)[1].(string); ok && (id == "dock.footer" || strings.HasPrefix(id, "dock.footer.")) {
			t.Fatalf("footer update emitted op for unmounted dock.footer: %#v", ops)
		}
	}
	if got := shownBy(ops)["dock.editor.editor.bar.model.name"]; got != nil {
		if spans, _ := got["spans"].([]any); len(spans) == 0 || !strings.Contains(spans[0].(map[string]any)["t"].(string), "claude-opus") {
			t.Fatalf("composer model did not update: %#v", got)
		}
	}

	// Under ompRoles, dock.footer stays suppressed even when a selector replaces
	// the editor (upstream status-line/component.ts:3810 — Tern draws no status strip).
	// For a composer-anchored model picker, the composer stays mounted in dock
	// so Tern can anchor `.sf-picker.at-composer` above it; closing the selector
	// refocuses the editor input without unmounting the composer.
	ops = h.applyOps(
		fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "editor", Index: 0},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "selector.1", Index: 0, Node: fe.Selector{Title: "Models", Items: []fe.SelectorItem{{ID: "a", Label: "A"}}}},
	)
	if ft := node(ops, "dock.footer"); ft != nil {
		t.Fatalf("dock.footer must remain suppressed under ompRoles when selector replaces editor: %#v", ft)
	}
	ops = h.applyOps(
		fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "selector.1", Index: 0},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
	)
	focused := false
	for _, op := range ops {
		if arr, ok := op.([]any); ok && len(arr) >= 2 && arr[0] == "focus" && arr[1] == "dock.editor.editor.line.input" {
			focused = true
		}
	}
	if !focused {
		t.Fatalf("editor did not refocus after model picker closed: %#v", ops)
	}
}

// TestOmpWorkingRowShapes verifies omp's working row: starburst spinner,
// elapsed timer, sep, shimmer label, fill spacer, compaction progress bar,
// spinnerless mode, and raw updates to label spans and tone.
func TestOmpWorkingRowShapes(t *testing.T) {
	h := readyOmpRoles(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	h.session.now = func() time.Time { return now }

	ops := h.applyOps(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Thinking"}))
	act := node(ops, "dock.activity")
	if act == nil || act["k"] != "row" || props(act)["role"] != "omp.hud.activity" {
		t.Fatalf("activity row = %#v", act)
	}
	statusCol := node(ops, "dock.activity.status")
	if statusCol == nil || statusCol["k"] != "col" || props(statusCol)["role"] != "omp.hud.status" {
		t.Fatalf("status col = %#v", statusCol)
	}
	row := node(ops, "dock.activity.status.working")
	if row == nil || row["k"] != "row" || props(row)["role"] != "omp.working" || props(row)["align"] != "center" || props(row)["gap"] != "sm" {
		t.Fatalf("working row = %#v", row)
	}
	kids := row["c"].([]any)
	if len(kids) != 5 {
		t.Fatalf("working children count = %d, want 5: %#v", len(kids), kids)
	}
	if kids[0].(map[string]any)["k"] != "el" || props(kids[0].(map[string]any))["tag"] != "span" || props(kids[0].(map[string]any))["role"] != "omp.working.pig" {
		t.Fatalf("spinner = %#v", kids[0])
	}
	if kids[1].(map[string]any)["k"] != "elapsed" || props(kids[1].(map[string]any))["format"] != "short" {
		t.Fatalf("elapsed = %#v", kids[1])
	}
	if kids[2].(map[string]any)["k"] != "text" || props(kids[2].(map[string]any))["text"] != "·" || props(kids[2].(map[string]any))["role"] != "omp.working.sep" {
		t.Fatalf("sep = %#v", kids[2])
	}
	label := kids[3].(map[string]any)
	if label["k"] != "shimmer" || props(label)["role"] != "omp.working.label" || props(label)["mode"] != "classic" {
		t.Fatalf("shimmer label = %#v", label)
	}
	if kids[4].(map[string]any)["k"] != "row" || props(kids[4].(map[string]any))["grow"] != float64(1) {
		t.Fatalf("fill = %#v", kids[4])
	}

	// Retry updates label tone to warning and text spans without a render.
	renders := h.session.renders
	ops = h.applyOps(workingOp(fe.Update, fe.Working{Kind: fe.WorkingRetry, Message: "Retrying in 3s"}))
	want := jsonOps(t, []any{"set", "dock.activity.status.working.label", map[string]any{
		"spans": []any{map[string]any{"t": "Retrying in 3s", "s": "muted"}},
		"tone":  "warning",
	}})
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("retry update = %#v, want %#v", ops, want)
	}
	if h.session.renders != renders {
		t.Fatalf("retry update rendered %d times, want 0", h.session.renders-renders)
	}

	// Compaction adds an indeterminate progress child.
	now = now.Add(2500 * time.Millisecond)
	ops = h.applyOps(workingOp(fe.Update, fe.Working{Kind: fe.WorkingCompaction, Message: "Compacting context"}))
	prog := node(ops, "dock.activity.status.working.progress")
	if prog == nil || prog["k"] != "progress" {
		t.Fatalf("compaction progress missing: %#v", ops)
	}
}

// TestOmpThinkingLifecycle verifies omp's thinking section: live role and
// starburst/elapsed head while streaming, text append while streaming,
// auto-collapse + omp.thinking role + "Thought for Ns" + took when streaming
// ends, and following PiG's Thinking.Hidden toggle afterwards.
func TestOmpThinkingLifecycle(t *testing.T) {
	h := readyOmpRoles(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	h.session.now = func() time.Time { return now }

	op := func(kind fe.OpKind, th fe.Thinking) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: "a.0", Node: th}
	}

	// Start streaming.
	ops := h.applyOps(op(fe.Insert, fe.Thinking{Text: "Step 1", Streaming: true}))
	sec := node(ops, "n.a.0")
	if sec == nil || sec["k"] != "section" || props(sec)["role"] != "omp.thinking.live" ||
		props(sec)["collapsible"] != true || props(sec)["collapsed"] != false || props(sec)["head"] != "n.a.0.head" {
		t.Fatalf("live thinking = %#v", sec)
	}
	head := node(ops, "n.a.0.head")
	if head == nil || head["k"] != "row" {
		t.Fatalf("live thinking head = %#v", head)
	}
	if spin := node(ops, "n.a.0.head.spin"); spin == nil || spin["k"] != "spinner" || props(spin)["role"] != "omp.thinking.spin" {
		t.Fatalf("live thinking spinner = %#v", spin)
	}
	if el := node(ops, "n.a.0.head.elapsed"); el == nil || el["k"] != "elapsed" {
		t.Fatalf("live thinking elapsed = %#v", el)
	}

	// Growing text streams by append.
	ops = h.applyOps(op(fe.Update, fe.Thinking{Text: "Step 1\nStep 2", Streaming: true}))
	if want := jsonOps(t, []any{"text", "n.a.0.md", "append", "\nStep 2"}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("streaming append = %#v, want %#v", ops, want)
	}

	// Streaming ends 4.2s later: collapses, switches to omp.thinking with took and "Thought for 4s".
	now = now.Add(4200 * time.Millisecond)
	ops = h.applyOps(op(fe.Update, fe.Thinking{Text: "Step 1\nStep 2", Streaming: false, Hidden: false}))
	sec = node(ops, "n.a.0")
	if sec == nil || sec["k"] != "section" || props(sec)["role"] != "omp.thinking" ||
		props(sec)["collapsed"] != true || props(sec)["took"] != float64(4200) || props(sec)["head"] != "n.a.0.head" {
		t.Fatalf("finished thinking = %#v", sec)
	}
	doneHead := node(ops, "n.a.0.head")
	if doneHead == nil || doneHead["k"] != "text" ||
		!reflect.DeepEqual(props(doneHead)["spans"], []any{map[string]any{"t": "Thought for 4s", "s": "muted"}}) {
		t.Fatalf("finished thinking head = %#v", doneHead)
	}

	// Subsequent PiG Hidden toggle updates collapsed on the finished section.
	ops = h.applyOps(op(fe.Update, fe.Thinking{Text: "Step 1\nStep 2", Streaming: false, Hidden: true}))
	if want := jsonOps(t, []any{"set", "n.a.0", map[string]any{"collapsed": true}}, []any{"settle", "n.a.0"}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("hide toggle = %#v, want %#v", ops, want)
	}
	ops = h.applyOps(op(fe.Update, fe.Thinking{Text: "Step 1\nStep 2", Streaming: false, Hidden: false}))
	if want := jsonOps(t, []any{"set", "n.a.0", map[string]any{"collapsed": false}}, []any{"settle", "n.a.0"}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("show toggle = %#v, want %#v", ops, want)
	}
}

// TestOmpErrorCardAndDividerAndTools checks omp's transcript elements under
// ompRoles: user images, error card (omp.error / omp.error.code /
// omp.error.message), abort text (omp.assistant.abort), divider rule
// (omp.divider), and tool cards (omp.tool.<name>, head props, age/took, no
// redundant status text badge).
func TestOmpErrorCardAndDividerAndTools(t *testing.T) {
	h := readyOmpRoles(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	h.session.now = func() time.Time { return now }

	// User message with attached image.
	ops := h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "u1", Index: 0,
		Node: fe.MarkdownText{
			Role:   fe.RoleUser,
			Text:   "look at this",
			Images: []fe.ViewImage{{Data: []byte("png-bytes"), MimeType: "image/png"}},
		},
	})
	if u := node(ops, "n.u1"); u == nil || u["k"] != "card" || props(u)["role"] != "omp.user" {
		t.Fatalf("user card = %#v", u)
	}
	if imgs := node(ops, "n.u1.images"); imgs == nil || imgs["k"] != "row" || props(imgs)["role"] != "omp.user.images" {
		t.Fatalf("user images = %#v", imgs)
	}

	// Error line emitted under "<msg>.e" becomes omp.error card with HTTP status code chip.
	ops = h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "a1.e", Index: 1,
		Node: fe.Lines{Lines: []string{"\x1b[31mError: 503 upstream overloaded\x1b[39m"}},
	})
	wrap := node(ops, "n.a1.e")
	if wrap == nil || wrap["k"] != "col" || props(wrap)["role"] != "omp.assistant" {
		t.Fatalf("error assistant wrapper = %#v", wrap)
	}
	errCard := node(ops, "n.a1.e.error")
	if errCard == nil || errCard["k"] != "card" || props(errCard)["role"] != "omp.error" || props(errCard)["tone"] != "error" {
		t.Fatalf("error card = %#v", errCard)
	}
	codeBadge := node(ops, "n.a1.e.error.head.code")
	if codeBadge == nil || codeBadge["k"] != "badge" || props(codeBadge)["role"] != "omp.error.code" || props(codeBadge)["text"] != "503" {
		t.Fatalf("error code badge = %#v", codeBadge)
	}
	errMsg := node(ops, "n.a1.e.error.message")
	if errMsg == nil || props(errMsg)["role"] != "omp.error.message" ||
		!reflect.DeepEqual(props(errMsg)["spans"], []any{map[string]any{"t": "upstream overloaded", "s": "mono"}}) {
		t.Fatalf("error message = %#v", errMsg)
	}

	// Abort line emitted under "<msg>.e" becomes omp.assistant.abort text.
	ops = h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "a2.e", Index: 2,
		Node: fe.Lines{Lines: []string{"\x1b[31mOperation aborted\x1b[39m"}},
	})
	abortNode := node(ops, "n.a2.e")
	if abortNode == nil || abortNode["k"] != "text" || props(abortNode)["role"] != "omp.assistant.abort" {
		t.Fatalf("abort node = %#v", abortNode)
	}

	// Horizontal divider line becomes rule with role omp.divider.
	ops = h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "div", Index: 3,
		Node: fe.Lines{Lines: []string{"────────── Session resumed"}},
	})
	divNode := node(ops, "n.div")
	if divNode == nil || divNode["k"] != "rule" || props(divNode)["role"] != "omp.divider" ||
		!reflect.DeepEqual(props(divNode)["label"], []any{map[string]any{"t": "Session resumed", "s": "muted"}}) {
		t.Fatalf("divider = %#v", divNode)
	}

	// Bash tool card: role omp.tool.bash, inline frame, omp.run box with command/output/foot.
	running := fe.ToolCard{
		Name:      "bash",
		Arguments: map[string]any{"command": "go test ./...", "i": "Running tests"},
		Status:    fe.ToolRunning,
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t1", Index: 4, Node: running})
	tool := node(ops, "n.t1")
	p := props(tool)
	if tool == nil || tool["k"] != "tool" || p["role"] != "omp.tool.bash" ||
		p["name"] != "bash" || p["title"] != "Bash" || p["target"] != "Running tests" ||
		p["targetKind"] != "text" || p["command"] != "go test ./..." || p["frame"] != "inline" ||
		p["intent"] != "Running tests" || p["status"] != "running" || p["age"] != float64(0) ||
		p["preview"] != "children" {
		t.Fatalf("running bash tool = %#v", tool)
	}
	if _, hasBadges := p["badges"]; hasBadges {
		t.Fatalf("omp tool should not carry pig-tern status badge: %#v", p["badges"])
	}
	if runBox := node(ops, "n.t1.run"); runBox == nil || runBox["k"] != "col" || props(runBox)["role"] != "omp.run" {
		t.Fatalf("bash run box = %#v", runBox)
	}
	if cmd := node(ops, "n.t1.run.input"); cmd == nil || cmd["k"] != "code" || props(cmd)["role"] != "omp.tool.bash.command" || props(cmd)["text"] != "go test ./..." {
		t.Fatalf("bash command = %#v", cmd)
	}
	if foot := node(ops, "n.t1.run.foot"); foot == nil || foot["k"] != "row" || props(foot)["role"] != "omp.run.foot" {
		t.Fatalf("bash foot = %#v", foot)
	}

	now = now.Add(1500 * time.Millisecond)
	done := running
	done.Status = fe.ToolDone
	done.Output = "ok\n"
	done.Elapsed = 1500 * time.Millisecond
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "t1", Index: 4, Node: done})
	tool = node(ops, "n.t1")
	if tool == nil || props(tool)["status"] != "done" || props(tool)["took"] != float64(1500) || props(tool)["age"] != nil {
		t.Fatalf("done bash tool = %#v", tool)
	}
	if out := node(ops, "n.t1.run.output"); out == nil || out["k"] != "ansi" || props(out)["role"] != "omp.tool.bash.output" || props(out)["text"] != "ok" {
		t.Fatalf("bash output = %#v", out)
	}
	if rt := node(ops, "n.t1.run.foot.time"); rt == nil || rt["k"] != "elapsed" || props(rt)["role"] != "omp.run.time" || props(rt)["stopped"] != float64(1500) {
		t.Fatalf("bash foot time = %#v", rt)
	}

	// Failing read tool: inline frame, tone="error", error gist in meta, no body when single-line.
	failRead := fe.ToolCard{
		Name:      "read",
		Arguments: map[string]any{"path": "missing.txt"},
		Status:    fe.ToolError,
		Output:    "ENOENT: no such file or directory",
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t2", Index: 5, Node: failRead})
	rt := node(ops, "n.t2")
	if rt == nil || rt["k"] != "tool" || props(rt)["role"] != "omp.tool.read" || props(rt)["frame"] != "inline" ||
		props(rt)["tone"] != "error" || props(rt)["status"] != "error" || props(rt)["collapsible"] != false ||
		!reflect.DeepEqual(props(rt)["meta"], []any{[]any{map[string]any{"t": "— ENOENT: no such file or directory", "s": "muted"}}}) {
		t.Fatalf("failing read tool = %#v", rt)
	}

	// Edit tool: card frame, role omp.tool.edit, diff child and meta diffStat.
	editCard := fe.ToolCard{
		Name:      "edit",
		Arguments: map[string]any{"path": "a.go"},
		Status:    fe.ToolDone,
		Elapsed:   50 * time.Millisecond,
		Diff:      &fe.Diff{Path: "a.go", Text: "@@ -1,1 +1,1 @@\n-old\n+new\n"},
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t3", Index: 6, Node: editCard})
	et := node(ops, "n.t3")
	if et == nil || et["k"] != "tool" || props(et)["role"] != "omp.tool.edit" || props(et)["frame"] != "card" ||
		!reflect.DeepEqual(props(et)["meta"], []any{"+1 −1"}) || node(ops, "n.t3.diff")["k"] != "diff" {
		t.Fatalf("edit tool = %#v", et)
	}
}

// TestOmpToolHeadsAndBodies verifies omp-shaped tool arguments and bodies:
// 1. edit hashline section headers (single file, multi-file, delete, rename) and Diff.Path fallback;
// 2. read with selector ranges (path + range in target, base path in href) and NO body child on success;
// 3. bash uses plain ToolCard.Output (ignoring PiG's Result padding / "Took" lines) and ToolCard.Elapsed in foot;
// 4. grep, glob, find, write, todo, ask, web_search heads follow omp's tool renderers.
func TestOmpToolHeadsAndBodies(t *testing.T) {
	h := readyOmpRoles(t)
	// Set cwd so pathHref produces absolute file paths.
	h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "/tmp/work"}))

	// 1a. edit with hashline input + Diff having empty Path -> target & diff.path resolve to absolute path (tools/edit.ts:1504).
	editSingle := fe.ToolCard{
		Name: "edit",
		Arguments: map[string]any{
			"input": "[notes.txt#4D58]\nPUT 2.=2:\n+status: verified\n",
		},
		Header:  "edit ...",
		Status:  fe.ToolDone,
		Elapsed: 42 * time.Millisecond,
		Diff:    &fe.Diff{Path: "", Text: "@@ -1,3 +1,3 @@\n title: x\n-status: draft\n+status: verified\n"},
	}
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e1", Index: 0, Node: editSingle})
	e1 := node(ops, "n.e1")
	if e1 == nil || props(e1)["role"] != "omp.tool.edit" || props(e1)["title"] != "Edit" ||
		props(e1)["target"] != "/tmp/work/notes.txt:2" || props(e1)["targetKind"] != "path" ||
		props(e1)["href"] != "file:///tmp/work/notes.txt" ||
		!reflect.DeepEqual(props(e1)["meta"], []any{"+1 −1"}) {
		t.Fatalf("edit single = %#v", e1)
	}
	diffNode := node(ops, "n.e1.diff")
	if diffNode == nil || diffNode["k"] != "diff" || props(diffNode)["path"] != "/tmp/work/notes.txt" {
		t.Fatalf("edit diff child = %#v", diffNode)
	}

	// 1b. edit with multiple hashline sections -> target "2 files", targetKind "text", no single href.
	editMulti := fe.ToolCard{
		Name: "edit",
		Arguments: map[string]any{
			"input": "[a.go#1111]\nPUT 1.=1:\n+x\n[b.go#2222]\nPUT 1.=1:\n+y\n",
		},
		Status: fe.ToolDone,
		Diff:   &fe.Diff{Text: "@@ -1 +1 @@\n-a\n+x\n@@ -1 +1 @@\n-b\n+y\n"},
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e2", Index: 1, Node: editMulti})
	e2 := node(ops, "n.e2")
	if e2 == nil || props(e2)["target"] != "2 files" || props(e2)["targetKind"] != "text" || props(e2)["href"] != nil {
		t.Fatalf("edit multi = %#v", e2)
	}

	// 1c. edit with REM -> title "Delete"; edit with MV -> rename in meta.
	editRem := fe.ToolCard{
		Name:      "edit",
		Arguments: map[string]any{"input": "[old.txt#ABCD]\nREM\n"},
		Status:    fe.ToolDone,
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e3", Index: 2, Node: editRem})
	if e3 := node(ops, "n.e3"); e3 == nil || props(e3)["title"] != "Delete" || props(e3)["target"] != "/tmp/work/old.txt" {
		t.Fatalf("edit REM = %#v", e3)
	}
	editMv := fe.ToolCard{
		Name:      "edit",
		Arguments: map[string]any{"input": "[a.txt#ABCD]\nMV b.txt\n"},
		Status:    fe.ToolDone,
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e4", Index: 3, Node: editMv})
	if e4 := node(ops, "n.e4"); e4 == nil || props(e4)["target"] != "/tmp/work/a.txt" || !reflect.DeepEqual(props(e4)["meta"], []any{"→ /tmp/work/b.txt"}) {
		t.Fatalf("edit MV = %#v", e4)
	}
	// 2. read success: shows path + selector in target, base path in href, and NO child nodes.
	readOk := fe.ToolCard{
		Name:      "read",
		Arguments: map[string]any{"path": "pkg/file.go:10-20"},
		Status:    fe.ToolDone,
		Output:    "[pkg/file.go#1A2B]\n10:line ten\n11:line eleven\n",
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r1", Index: 4, Node: readOk})
	r1 := node(ops, "n.r1")
	if r1 == nil || r1["k"] != "tool" || props(r1)["role"] != "omp.tool.read" ||
		props(r1)["frame"] != "inline" || props(r1)["target"] != "pkg/file.go:10-20" ||
		props(r1)["targetKind"] != "path" || props(r1)["href"] != "file:///tmp/work/pkg/file.go" ||
		props(r1)["collapsible"] != false || r1["c"] != nil {
		t.Fatalf("read success = %#v", r1)
	}

	// 3. bash: ignores PiG's Result padding & "Took 0.1s" row, uses plain Output & Elapsed in foot.
	bashDone := fe.ToolCard{
		Name:      "bash",
		Arguments: map[string]any{"command": "printf 'alpha\\nbeta\\n'"},
		Status:    fe.ToolDone,
		Output:    "alpha\nbeta\n",
		Result:    []string{"", "\x1b[38;2;113;113;113malpha\x1b[39m", "\x1b[38;2;113;113;113mbeta\x1b[39m", "", "\x1b[38;2;113;113;113mTook 0.1s\x1b[39m"},
		Elapsed:   120 * time.Millisecond,
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "b1", Index: 5, Node: bashDone})
	b1 := node(ops, "n.b1")
	if b1 == nil || props(b1)["took"] != float64(120) {
		t.Fatalf("bash tool = %#v", b1)
	}
	if out := node(ops, "n.b1.run.output"); out == nil || props(out)["text"] != "alpha\nbeta" {
		t.Fatalf("bash output = %#v, want plain 'alpha\\nbeta'", out)
	}
	if ft := node(ops, "n.b1.run.foot.time"); ft == nil || props(ft)["age"] != float64(120) || props(ft)["stopped"] != float64(120) {
		t.Fatalf("bash foot time = %#v", ft)
	}

	// 4. grep, glob, find, write, todo, ask, web_search heads.
	cases := []struct {
		id         string
		tool       fe.ToolCard
		title      string
		target     string
		targetKind string
		frame      string
		meta       []any
	}{
		{
			id:         "g1",
			tool:       fe.ToolCard{Name: "grep", Arguments: map[string]any{"pattern": "TODO", "path": "src", "case": false}, Status: fe.ToolDone},
			title:      "Grep",
			target:     "TODO",
			targetKind: "pattern",
			frame:      "inline",
			meta:       []any{"in src", "case:insensitive"},
		},
		{
			id:         "gl1",
			tool:       fe.ToolCard{Name: "glob", Arguments: map[string]any{"path": "**/*.go", "limit": 20.0}, Status: fe.ToolDone},
			title:      "Glob",
			target:     "**/*.go",
			targetKind: "pattern",
			frame:      "inline",
			meta:       []any{"limit:20"},
		},
		{
			// upstream: packages/tui/src/tools/find.ts:397-405
			id:         "f1",
			tool:       fe.ToolCard{Name: "find", Arguments: map[string]any{"query": "auth handler", "grep_keywords": []any{"jwt", "token"}, "path": "pkg"}, Status: fe.ToolDone},
			title:      "Find",
			target:     "auth handler",
			targetKind: "query",
			frame:      "inline",
			meta:       []any{"jwt token", "in pkg"},
		},
		{
			// upstream: packages/tui/src/tools/write.ts:156-159 — content.split("\n").length ("a\nb\nc\n" = 4 lines).
			id:         "w1",
			tool:       fe.ToolCard{Name: "write", Arguments: map[string]any{"path": "out.txt", "content": "a\nb\nc\n"}, Status: fe.ToolDone},
			title:      "Write",
			target:     "out.txt",
			targetKind: "path",
			frame:      "card",
			meta:       []any{"4 lines"},
		},
		{
			// upstream: packages/tui/src/tools/todo.ts:301-326 — ops[] batch.
			id:         "td1",
			tool:       fe.ToolCard{Name: "todo", Arguments: map[string]any{"ops": []any{map[string]any{"op": "start", "task": "Wire heads"}, map[string]any{"op": "done", "task": "Read brief"}}}, Status: fe.ToolDone},
			title:      "Todo",
			target:     "start Wire heads, done Read brief",
			targetKind: "text",
			frame:      "card",
		},
		{
			// upstream: packages/tui/src/tools/ask.ts:147-167 — pending ask is inline, answered is card.
			id: "ask1",
			tool: fe.ToolCard{
				Name: "ask",
				Arguments: map[string]any{
					"html": `<form><label>Pick a DB?<input type="radio" name="db" value="pg"></label><label>Pick a port?<input type="text" name="port"></label></form>`,
				},
				Status: fe.ToolRunning,
			},
			title:      "Ask",
			target:     "Pick a DB?",
			targetKind: "text",
			frame:      "inline",
			meta:       []any{"2 fields"},
		},
		{
			// upstream: packages/tui/src/tools/web-search.ts:334-418 — inline while running, card once settled.
			id:         "ws1",
			tool:       fe.ToolCard{Name: "web_search", Arguments: map[string]any{"query": "golang 1.26"}, Status: fe.ToolDone},
			title:      "Web search",
			target:     "golang 1.26",
			targetKind: "query",
			frame:      "card",
		},
	}
	for i, tc := range cases {
		ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: tc.id, Index: 6 + i, Node: tc.tool})
		n := node(ops, "n."+tc.id)
		p := props(n)
		if n == nil || p["role"] != "omp.tool."+tc.tool.Name || p["title"] != tc.title ||
			p["target"] != tc.target || p["targetKind"] != tc.targetKind || p["frame"] != tc.frame {
			t.Fatalf("%s: got %#v", tc.id, n)
		}
		if tc.meta != nil && !reflect.DeepEqual(p["meta"], tc.meta) {
			t.Fatalf("%s: meta = %#v, want %#v", tc.id, p["meta"], tc.meta)
		}
	}
}

// TestOmpStatusSegments checks omp's status strip segment roles, path spans,
// and extension hook role under ompRoles.
func TestOmpStatusSegments(t *testing.T) {
	// ompFooterElement is still unit-tested directly for segment roles and path spans
	// (dock.footer itself is suppressed in live Tern sessions under ompRoles).
	footer := sampleFooter()
	footer.SessionName = "my-session"
	st := buildNode(ompFooterElement("footer", footer, func(string) {}))
	if st.Kind != tern.KindStatus || st.Props["role"] != "omp.status" {
		t.Fatalf("status = %#v", st)
	}
	wantRoles := map[string]any{
		"cwd":      "omp.status.path",
		"branch":   "omp.status.git",
		"session":  "omp.status.session_name",
		"status.0": "omp.status.hook",
		"usage":    "omp.status.cost",
		"context":  "omp.status.context_pct",
		"xp":       nil,
		"model":    "omp.status.model",
	}
	gotRoles := map[string]any{}
	var cwdNode tern.Node
	for _, c := range st.Children {
		seg := c.(tern.Node)
		k := seg.Props["key"].(string)
		gotRoles[k] = seg.Props["role"]
		if k == "cwd" {
			cwdNode = seg
		}
	}
	if !reflect.DeepEqual(gotRoles, wantRoles) {
		t.Fatalf("status segment roles = %#v, want %#v", gotRoles, wantRoles)
	}
	// Path splits parent (dim) and leaf (strong).
	wantSpans := []any{
		map[string]any{"t": "~/src/", "s": "statusLinePath dim"},
		map[string]any{"t": "pig", "s": "statusLinePath strong"},
	}
	if !reflect.DeepEqual(cwdNode.Props["spans"], wantSpans) {
		t.Fatalf("cwd spans = %#v, want %#v", cwdNode.Props["spans"], wantSpans)
	}
}

// TestNonOmpRolesFullFrameUnchanged verifies that with ompRoles false, a wide
// frame covering transcript messages, tool cards (running, done, error, Diff,
// ResultView carrying omp.* IDs, JSON output, images), Selectors, Settings,
// working, editor, and footer emits zero omp.* roles and preserves exact
// pig-tern shapes.
func TestNonOmpRolesFullFrameUnchanged(t *testing.T) {
	h := ready(t)
	resView := &fe.View{
		Seq: 1,
		Root: fe.ViewNode{
			ID: "omp.run", Kind: fe.ViewKindVStack, Rows: 2, Width: 40, Gap: 0,
			Children: []fe.ViewNode{
				{ID: "omp.tool.bash.command", Kind: fe.ViewKindText, Rows: 1, Width: 40, Text: "echo hi"},
				{ID: "omp.tool.bash.output", Kind: fe.ViewKindLines, Rows: 1, Width: 40, Lines: []string{"hi"}},
			},
		},
	}
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u", Index: 0, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "hi"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "th", Index: 1, Node: fe.Thinking{Text: "hmm", Streaming: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Index: 2, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "ok", Streaming: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a.e", Index: 3, Node: fe.Lines{Lines: []string{"Error: 500 boom"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "div", Index: 4, Node: fe.Lines{Lines: []string{"──────────"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t", Index: 5, Node: fe.ToolCard{Name: "bash", Arguments: map[string]any{"command": "ls"}, Status: fe.ToolRunning}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "td", Index: 6, Node: fe.ToolCard{Name: "edit", Arguments: map[string]any{"path": "a.go"}, Status: fe.ToolDone, Diff: &fe.Diff{Path: "a.go", Text: "@@ -1,2 +1,2 @@\n keep\n-old\n+new\n"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "tv", Index: 7, Node: fe.ToolCard{Name: "bash", Status: fe.ToolDone, Result: []string{"echo hi", "hi"}, ResultView: resView}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "te", Index: 8, Node: fe.ToolCard{Name: "bash", Status: fe.ToolError, Output: "boom"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "tj", Index: 9, Node: fe.ToolCard{Name: "custom", Status: fe.ToolDone, Output: `{"ok":true}`}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ti", Index: 10, Node: fe.ToolCard{Name: "read", Status: fe.ToolDone, Result: []string{"[image]"}, ResultView: &fe.View{Seq: 2, Root: fe.ViewNode{ID: "omp.tool.read.image", Kind: fe.ViewKindImage, Rows: 1, Width: 20, Image: &fe.ViewImage{Data: []byte("png"), MimeType: "image/png"}}}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "working", Index: 0, Node: fe.Working{Kind: fe.WorkingAgent, Message: "Working"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{Text: "draft", Cursor: 5, Placeholder: "Ask", Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 2, Node: sampleFooter()},
	)

	if u := node(ops, "n.u"); u["k"] != "card" || props(u)["role"] != "pig.user" {
		t.Fatalf("user = %#v", u)
	}
	if th := node(ops, "n.th"); th["k"] != "card" || props(th)["role"] != "pig.thinking" || props(th)["head"] != "Thinking" {
		t.Fatalf("thinking = %#v", th)
	}
	if a := node(ops, "n.a"); a["k"] != "md" || props(a)["role"] != "pig.assistant" {
		t.Fatalf("assistant = %#v", a)
	}
	if errLine := node(ops, "n.a.e"); errLine["k"] != "ansi" || props(errLine)["text"] != "Error: 500 boom" {
		t.Fatalf("error line = %#v", errLine)
	}
	if div := node(ops, "n.div"); div["k"] != "ansi" || props(div)["text"] != "──────────" {
		t.Fatalf("divider line = %#v", div)
	}
	if tool := node(ops, "n.t"); tool["k"] != "tool" || props(tool)["role"] != nil ||
		!reflect.DeepEqual(props(tool)["badges"], []any{map[string]any{"text": "running", "tone": "info"}}) {
		t.Fatalf("tool = %#v", tool)
	}
	if tv := node(ops, "n.tv.view"); tv == nil || props(tv)["role"] != "pig.view" {
		t.Fatalf("omp-off ResultView must keep pig.view wrapper: %#v", tv)
	}
	if te := node(ops, "n.te"); te == nil || props(te)["role"] != nil ||
		!reflect.DeepEqual(props(te)["badges"], []any{map[string]any{"text": "error", "tone": "error"}}) {
		t.Fatalf("omp-off error tool = %#v", te)
	}
	if tj := node(ops, "n.tj.json"); tj == nil || tj["k"] != "json" {
		t.Fatalf("omp-off JSON tool = %#v", tj)
	}
	if ti := node(ops, "n.ti.view.0"); ti == nil || ti["k"] != "image" || props(ti)["role"] != nil {
		t.Fatalf("omp-off view image tool = %#v", ti)
	}
	if w := node(ops, "dock.working"); w["k"] != "spinner" || props(w)["role"] != "pig.working" || props(w)["style"] != "braille" {
		t.Fatalf("working = %#v", w)
	}
	if ed := node(ops, "dock.editor"); ed["k"] != "editor" || props(ed)["role"] != nil || props(ed)["placeholder"] != "Ask" {
		t.Fatalf("editor = %#v", ed)
	}
	if ft := node(ops, "dock.footer"); ft["k"] != "status" || props(ft)["role"] != "pig.footer" {
		t.Fatalf("footer = %#v", ft)
	}

	selOps := h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionDock, ID: "selector.model", Index: 1,
		Node: fe.Selector{Title: "Select Model", Items: []fe.SelectorItem{{ID: "m1", Label: "m1"}}},
	})
	if sp := node(selOps, "layer.selector.model"); sp == nil || props(sp)["noun"] != "items" || props(sp)["icon"] != nil {
		t.Fatalf("omp-off selector changed: %#v", sp)
	}
	setOps := h.applyOps(fe.Op{
		Kind: fe.Update, Region: fe.RegionDock, ID: "selector.model", Index: 1,
		Node: fe.Settings{Items: []fe.Setting{{ID: "s1", Label: "Wrap", Value: "true", Values: []string{"true", "false"}}}},
	})
	if st := node(setOps, "layer.selector.model"); st == nil || st["k"] != "prefs" {
		t.Fatalf("omp-off settings changed: %#v", st)
	}

	for _, frameOps := range [][]any{ops, selOps, setOps} {
		for id, p := range shownBy(frameOps) {
			if role, ok := p["role"].(string); ok && strings.HasPrefix(role, "omp.") {
				t.Fatalf("non-omp frame leaked omp role on %s: %q", id, role)
			}
		}
	}
}

// TestOmpV19ToolCards verifies omp v19.1.1 tool card heads, roles, frames,
// badges, and meta against upstream describeCall/describeResult in
// the omp source at tag v19.1.1 ($OMP_SRC), packages/tui/src/tools/*.ts.
func TestOmpV19ToolCards(t *testing.T) {
	h := readyOmpRoles(t)
	h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "/tmp/work"}))
	cases := []struct {
		id         string
		card       fe.ToolCard
		wantTitle  string
		wantTarget any
		wantKind   any
		wantFrame  string
		wantMeta   []any
		wantBadges []any
		wantTone   any
	}{
		{
			// upstream: packages/tui/src/tools/edit.ts:1419-1439, 1501-1504 — first changed line skips context lines; absolute path.
			id: "t.edit",
			card: fe.ToolCard{
				Name:      "edit",
				Arguments: map[string]any{"input": "[src/main.go#A1B2]\nPUT 43.=43:\n+fmt.Println()\n"},
				Status:    fe.ToolDone,
				Diff:      &fe.Diff{Path: "src/main.go", Text: "@@ -41,3 +41,3 @@\n line41\n line42\n-old43\n+fmt.Println()\n"},
			},
			wantTitle:  "Edit",
			wantTarget: "/tmp/work/src/main.go:43",
			wantKind:   "path",
			wantFrame:  "card",
			wantMeta:   []any{"+1 −1"},
		},
		{
			// upstream: packages/tui/src/tools/edit.ts:341-343 — op "create" -> "Create".
			id: "t.edit.create",
			card: fe.ToolCard{
				Name:      "edit",
				Arguments: map[string]any{"op": "create", "path": "new.go"},
				Status:    fe.ToolDone,
			},
			wantTitle:  "Create",
			wantTarget: "/tmp/work/new.go",
			wantKind:   "path",
			wantFrame:  "card",
		},
		{
			// upstream: packages/tui/src/tools/proc-render.ts:459-464 — bare proc:// -> "Proc jobs & services", card frame, no target.
			id: "t.proc.root",
			card: fe.ToolCard{
				Name:      "read",
				Arguments: map[string]any{"path": "proc://"},
				Status:    fe.ToolDone,
			},
			wantTitle:  "Proc jobs & services",
			wantTarget: nil,
			wantKind:   nil,
			wantFrame:  "card",
		},
		{
			// upstream: packages/tui/src/tools/proc-render.ts:459-464 — proc://bg_1 -> "Proc" + "bg_1", card frame.
			id: "t.proc.id",
			card: fe.ToolCard{
				Name:      "read",
				Arguments: map[string]any{"path": "proc://bg_1"},
				Status:    fe.ToolDone,
			},
			wantTitle:  "Proc",
			wantTarget: "bg_1",
			wantKind:   "text",
			wantFrame:  "card",
		},
		{
			// upstream: packages/tui/src/tools/cfg-render.ts:89-96, cfg-url.ts:19-30 — cfg://a/b?x=1#y -> "a.b", card frame.
			id: "t.cfg",
			card: fe.ToolCard{
				Name:      "read",
				Arguments: map[string]any{"path": "cfg://advisor/enabled?raw=1#sec"},
				Status:    fe.ToolDone,
			},
			wantTitle:  "Config",
			wantTarget: "advisor.enabled",
			wantKind:   "text",
			wantFrame:  "card",
		},
		{
			// upstream: packages/tui/src/tools/task.ts:507-511, 2100-2104 — multiple tasks -> "N agents".
			id: "t.task.multi",
			card: fe.ToolCard{
				Name: "task",
				Arguments: map[string]any{
					"tasks": []any{
						map[string]any{"name": "A", "task": "one"},
						map[string]any{"name": "B", "task": "two"},
					},
				},
				Status: fe.ToolDone,
			},
			wantTitle:  "Task",
			wantTarget: "2 agents",
			wantKind:   "text",
			wantFrame:  "card",
		},
		{
			// upstream: packages/tui/src/tools/task.ts:507-511 — flat args.agent.
			id: "t.task.flat",
			card: fe.ToolCard{
				Name:      "task",
				Arguments: map[string]any{"agent": "scout", "task": "find files"},
				Status:    fe.ToolDone,
			},
			wantTitle:  "Task",
			wantTarget: "scout",
			wantKind:   "text",
			wantFrame:  "card",
		},
		{
			// upstream: packages/tui/src/tools/wait.ts:864-905 — inline while running, card once settled.
			id: "t.wait",
			card: fe.ToolCard{
				Name:      "wait",
				Arguments: map[string]any{},
				Status:    fe.ToolRunning,
			},
			wantTitle:  "Wait",
			wantTarget: nil,
			wantKind:   nil,
			wantFrame:  "inline",
		},
		{
			// upstream: packages/tui/src/tools/eval.ts:760-775 — single cell title becomes head title, language badge, no target.
			id: "t.eval.single",
			card: fe.ToolCard{
				Name:      "eval",
				Arguments: map[string]any{"language": "py", "title": "Compute", "code": "print(1+1)"},
				Status:    fe.ToolDone,
				Output:    "2",
			},
			wantTitle:  "Compute",
			wantTarget: nil,
			wantKind:   nil,
			wantFrame:  "card",
			wantBadges: []any{map[string]any{"text": "python"}},
		},
		{
			// upstream: packages/tui/src/tools/eval.ts:760-775 — multi-cell -> title "Eval", meta ["2 cells"], deduped badges.
			id: "t.eval.multi",
			card: fe.ToolCard{
				Name: "eval",
				Arguments: map[string]any{
					"cells": []any{
						map[string]any{"language": "py", "title": "One", "code": "1"},
						map[string]any{"language": "js", "title": "Two", "code": "2"},
					},
				},
				Status: fe.ToolDone,
			},
			wantTitle:  "Eval",
			wantTarget: nil,
			wantKind:   nil,
			wantFrame:  "card",
			wantMeta:   []any{"2 cells"},
			wantBadges: []any{map[string]any{"text": "python"}, map[string]any{"text": "js"}},
		},
		{
			// upstream: packages/tui/src/tools/lsp.ts:797-837 — action title, file:line path target, symbol/new_name/apply meta.
			id: "t.lsp",
			card: fe.ToolCard{
				Name: "lsp",
				Arguments: map[string]any{
					"action":   "type_definition",
					"file":     "main.go",
					"line":     10,
					"symbol":   "Foo",
					"new_name": "Bar",
					"apply":    true,
				},
				Status: fe.ToolDone,
			},
			wantTitle:  "Type definition",
			wantTarget: "main.go:10",
			wantKind:   "path",
			wantFrame:  "inline",
			wantMeta:   []any{"Foo · → Bar · apply:true"},
		},
		{
			// upstream: packages/tui/src/tools/ast-grep.ts:184-190 — card frame (not inline).
			id: "t.astgrep",
			card: fe.ToolCard{
				Name:      "ast_grep",
				Arguments: map[string]any{"pat": "fmt.Println($X)", "path": "src"},
				Status:    fe.ToolDone,
			},
			wantTitle:  "AST Grep",
			wantTarget: "fmt.Println($X)",
			wantKind:   "pattern",
			wantFrame:  "card",
			wantMeta:   []any{"in src"},
		},
		{
			// upstream: packages/tui/src/tools/ast-edit.ts:92-102, 312-320 — ops[] single pat (whitespace collapsed) and paths[].
			id: "t.astedit.one",
			card: fe.ToolCard{
				Name: "ast_edit",
				Arguments: map[string]any{
					"ops":   []any{map[string]any{"pat": "old(\n\t$X\n)", "out": "new($X)"}},
					"paths": []any{"src", "pkg"},
				},
				Status: fe.ToolDone,
			},
			wantTitle:  "AST Edit",
			wantTarget: "old( $X )",
			wantKind:   "pattern",
			wantFrame:  "card",
			wantMeta:   []any{"in src, pkg"},
		},
		{
			// upstream: packages/tui/src/tools/ast-edit.ts:98-102 — multiple ops -> "N rewrites".
			id: "t.astedit.many",
			card: fe.ToolCard{
				Name: "ast_edit",
				Arguments: map[string]any{
					"ops": []any{
						map[string]any{"pat": "a()", "out": "b()"},
						map[string]any{"pat": "c()", "out": "d()"},
					},
				},
				Status: fe.ToolDone,
			},
			wantTitle:  "AST Edit",
			wantTarget: "2 rewrites",
			wantKind:   "text",
			wantFrame:  "card",
		},
		{
			// upstream: packages/tui/src/tools/debug.ts:104-132, 225-228 — card frame, spaces in action, program/file:line target in meta.
			id: "t.debug",
			card: fe.ToolCard{
				Name:      "debug",
				Arguments: map[string]any{"action": "set_breakpoint", "file": "main.go", "line": 42},
				Status:    fe.ToolDone,
			},
			wantTitle:  "Debug",
			wantTarget: "set breakpoint",
			wantKind:   "text",
			wantFrame:  "card",
			wantMeta:   []any{"main.go:42"},
		},
		{
			// upstream: packages/tui/src/tools/github.ts:43-58, 88-125 — fixed OP_TITLES + #N pr identifier + repo.
			id: "t.github.pr",
			card: fe.ToolCard{
				Name:      "github",
				Arguments: map[string]any{"op": "pr_checkout", "pr": "42", "repo": "can1357/oh-my-pi"},
				Status:    fe.ToolDone,
			},
			wantTitle:  "GitHub PR Checkout",
			wantTarget: "#42",
			wantKind:   "text",
			wantFrame:  "card",
			wantMeta:   []any{"can1357/oh-my-pi"},
		},
		{
			// upstream: packages/tui/src/tools/github.ts:55-58 — unknown op falls back to "GitHub".
			id: "t.github.other",
			card: fe.ToolCard{
				Name:      "github",
				Arguments: map[string]any{"op": "pr_view", "repo": "can1357/oh-my-pi"},
				Status:    fe.ToolDone,
			},
			wantTitle:  "GitHub",
			wantTarget: "can1357/oh-my-pi",
			wantKind:   "text",
			wantFrame:  "card",
		},
		{
			// upstream: packages/tui/src/tools/default-renderer.ts:196-203 — checkpoint/rewind use default inline args summary, card frame.
			id: "t.checkpoint",
			card: fe.ToolCard{
				Name:      "checkpoint",
				Arguments: map[string]any{"goal": "refactor parser"},
				Status:    fe.ToolDone,
			},
			wantTitle:  "Checkpoint",
			wantTarget: `goal="refactor parser"`,
			wantKind:   "text",
			wantFrame:  "card",
		},
		{
			// upstream: packages/tui/src/tools/bash.ts:1073-1076 — failed bash does NOT set tone="error" on tool wrapper.
			id: "t.bash.fail",
			card: fe.ToolCard{
				Name:      "bash",
				Arguments: map[string]any{"command": "false"},
				Status:    fe.ToolError,
				Output:    "failed",
			},
			wantTitle:  "Bash",
			wantTarget: nil,
			wantKind:   nil,
			wantFrame:  "inline",
			wantTone:   nil,
		},
	}

	ops := make([]fe.Op, len(cases))
	for i, tc := range cases {
		ops[i] = fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: tc.id, Index: i, Node: tc.card}
	}
	wire := h.applyOps(ops...)
	for _, tc := range cases {
		n := node(wire, "n."+tc.id)
		if n == nil || n["k"] != "tool" {
			t.Fatalf("%s: missing tool node: %#v", tc.id, n)
		}
		p := props(n)
		if p["role"] != "omp.tool."+tc.card.Name {
			t.Errorf("%s: role = %v, want omp.tool.%s", tc.id, p["role"], tc.card.Name)
		}
		if p["title"] != tc.wantTitle {
			t.Errorf("%s: title = %v, want %q", tc.id, p["title"], tc.wantTitle)
		}
		if p["target"] != tc.wantTarget {
			t.Errorf("%s: target = %#v, want %#v", tc.id, p["target"], tc.wantTarget)
		}
		if p["targetKind"] != tc.wantKind {
			t.Errorf("%s: targetKind = %#v, want %#v", tc.id, p["targetKind"], tc.wantKind)
		}
		if p["frame"] != tc.wantFrame {
			t.Errorf("%s: frame = %v, want %q", tc.id, p["frame"], tc.wantFrame)
		}
		if tc.wantMeta != nil && !reflect.DeepEqual(p["meta"], tc.wantMeta) {
			t.Errorf("%s: meta = %#v, want %#v", tc.id, p["meta"], tc.wantMeta)
		}
		if tc.wantBadges != nil && !reflect.DeepEqual(p["badges"], tc.wantBadges) {
			t.Errorf("%s: badges = %#v, want %#v", tc.id, p["badges"], tc.wantBadges)
		}
		if tc.id == "t.bash.fail" && p["tone"] != tc.wantTone {
			t.Errorf("%s: tone = %#v, want nil", tc.id, p["tone"])
		}
	}
	if runBox := node(wire, "n.t.bash.fail.run"); runBox == nil || props(runBox)["tone"] != "error" {
		t.Fatalf("failed bash run box = %#v, want tone=error", runBox)
	}
	if w, tone := ompRunState(fe.ToolCancelled); w != "Cancelled" || tone != "muted" {
		t.Fatalf("cancelled run state = (%q, %q), want (Cancelled, muted)", w, tone)
	}
}

// TestOmpPickers verifies omp v19.1.1 picker shapes (size, noun, icon, layout,
// composer-anchored model picker without action bar, right-aligned Close action
// for non-model pickers) for model, thinking, session, tree ("Session Tree"),
// and generic selectors.
func TestOmpPickers(t *testing.T) {
	cases := []struct {
		id           string
		sel          fe.Selector
		wantSize     string
		wantNoun     string
		wantIcon     any
		wantLayout   any
		wantNoAction bool
	}{
		{
			// upstream: packages/tui/src/overlays/model-picker.ts:648-675
			id:       "selector.model",
			sel:      fe.Selector{Title: "Select Model", Items: []fe.SelectorItem{{ID: "anthropic/sonnet", Label: "sonnet"}}},
			wantSize: "md", wantNoun: "models", wantIcon: "cpu", wantLayout: "rows", wantNoAction: true,
		},
		{
			// upstream: packages/tui/src/overlays/model-picker.ts:693
			id:       "selector.thinking",
			sel:      fe.Selector{Title: "Thinking Level", Items: []fe.SelectorItem{{ID: "high", Label: "High"}}},
			wantSize: "md", wantNoun: "levels", wantIcon: "brain", wantLayout: nil,
		},
		{
			// upstream: packages/tui/src/overlays/session-selector.ts:2196-2200
			id:       "selector.session",
			sel:      fe.Selector{Title: "Resume Session", Items: []fe.SelectorItem{{ID: "s1", Label: "s1"}}},
			wantSize: "lg", wantNoun: "sessions", wantIcon: "history", wantLayout: "cards",
		},
		{
			// upstream: packages/tui/src/overlays/tree-selector.ts:1630-1635 — PiG titles it "Session Tree" (fixes A12).
			id:       "selector.tree",
			sel:      fe.Selector{Title: "Session Tree", Items: []fe.SelectorItem{{ID: "e1", Label: "root", Depth: 0}, {ID: "e2", Label: "child", Depth: 1}}},
			wantSize: "lg", wantNoun: "entries", wantIcon: "git-branch", wantLayout: "tree",
		},
		{
			// upstream: packages/tui/src/native/picker.ts:226 — default noun is "options".
			id:       "selector.generic",
			sel:      fe.Selector{Title: "Pick One", Items: []fe.SelectorItem{{ID: "a", Label: "A"}}},
			wantSize: "md", wantNoun: "options", wantIcon: nil, wantLayout: nil,
		},
	}

	for _, tc := range cases {
		h := readyOmpRoles(t)
		ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: tc.id, Index: 0, Node: tc.sel})
		p := node(ops, "layer."+tc.id)
		if p == nil || p["k"] != "picker" {
			t.Fatalf("%s: picker = %#v", tc.id, p)
		}
		pr := props(p)
		if pr["size"] != tc.wantSize || pr["noun"] != tc.wantNoun || pr["icon"] != tc.wantIcon || pr["layout"] != tc.wantLayout {
			t.Errorf("%s: got size=%v noun=%v icon=%v layout=%v, want %v/%v/%v/%v",
				tc.id, pr["size"], pr["noun"], pr["icon"], pr["layout"], tc.wantSize, tc.wantNoun, tc.wantIcon, tc.wantLayout)
		}
		actions, _ := pr["actions"].([]any)
		if tc.wantNoAction {
			if len(actions) != 0 {
				t.Errorf("%s: actions = %#v, want empty []", tc.id, actions)
			}
			if pr["anchor"] != "composer" || pr["search"] != "list" || pr["density"] != "snug" || pr["scopeStyle"] != "rail" {
				t.Errorf("%s: model picker props = anchor:%v search:%v density:%v scopeStyle:%v",
					tc.id, pr["anchor"], pr["search"], pr["density"], pr["scopeStyle"])
			}
			continue
		}
		closeAct := actions[1].(map[string]any)
		if closeAct["id"] != "close" || closeAct["label"] != "Close" || closeAct["end"] != true {
			t.Errorf("%s: close action = %#v", tc.id, closeAct)
		}
	}
}

// TestOmpResultViewRoleMapping verifies ToolCard.ResultView mapping for real
// PiG Lines nodes (Text == "", using Lines / cut), props, nesting, and
// regressions for B1 (oversize image), B2 (Box / unknown omp.* ancestor),
// B3 (section head & Gap), B4 (role-less view props), B5 (non-dead ask pills),
// and A9-A11.
func TestOmpResultViewRoleMapping(t *testing.T) {
	var findByRole func(n map[string]any, role string) map[string]any
	findByRole = func(n map[string]any, role string) map[string]any {
		if n == nil {
			return nil
		}
		if p := props(n); p != nil && p["role"] == role {
			return n
		}
		if cs, ok := n["c"].([]any); ok {
			for _, c := range cs {
				if cm, ok := c.(map[string]any); ok {
					if found := findByRole(cm, role); found != nil {
						return found
					}
				}
			}
		}
		return nil
	}

	t.Run("bash_with_view_preview_intent_and_elapsed", func(t *testing.T) {
		// upstream: tools/bash.ts:503-541, 1071-1076; tools/native-view.ts:257-291
		h := readyOmpRoles(t)
		lines := []string{"$ echo hi", "step 1", "hi", "Done", "1.2s"}
		view := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.bash", Kind: fe.ViewKindContainer, Rows: 5, Width: 40,
				Children: []fe.ViewNode{
					{
						ID: "omp.run", Kind: fe.ViewKindVStack, Rows: 5, Width: 40,
						Children: []fe.ViewNode{
							{ID: "omp.tool.bash.command", Kind: fe.ViewKindLines, Rows: 1, Width: 40, Lines: []string{"$ echo hi"}},
							{ID: "omp.tool.bash.step#0", Kind: fe.ViewKindText, Rows: 1, Width: 40, Text: "step 1"},
							{ID: "omp.tool.bash.output", Kind: fe.ViewKindLines, Rows: 1, Width: 40, Lines: []string{"hi"}},
							{ID: "omp.run.foot", Kind: fe.ViewKindHStack, Rows: 2, Width: 40, Children: []fe.ViewNode{
								{ID: "omp.run.state", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "Done"},
								{ID: "omp.run.time", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "1.2s"},
							}},
						},
					},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "b1", Index: 0,
			Node: fe.ToolCard{
				Name:       "bash",
				Arguments:  map[string]any{"command": "echo hi", "i": "Say hello"},
				Status:     fe.ToolDone,
				Result:     lines,
				ResultView: view,
			},
		})
		root := node(ops, "n.b1")
		if props(root)["preview"] != "children" || props(root)["target"] != "Say hello" {
			t.Fatalf("bash tool props = %#v", props(root))
		}
		children := root["c"].([]any)
		first := children[0].(map[string]any)
		if first["k"] != "col" || props(first)["role"] != "omp.run" {
			t.Fatalf("expected omp.run directly under tool, got %#v", first)
		}
		cmd := findByRole(root, "omp.tool.bash.command")
		if cmd == nil || cmd["k"] != "code" || props(cmd)["lang"] != "bash" || props(cmd)["wrap"] != true ||
			!reflect.DeepEqual(props(cmd)["preview"], map[string]any{"lines": float64(6)}) {
			t.Fatalf("bash command = %#v", cmd)
		}
		out := findByRole(root, "omp.tool.bash.output")
		if out == nil || out["k"] != "ansi" || props(out)["follow"] != true || props(out)["text"] != "hi" ||
			!reflect.DeepEqual(props(out)["preview"], map[string]any{"lines": float64(10)}) {
			t.Fatalf("bash output = %#v", out)
		}
		tm := findByRole(root, "omp.run.time")
		if tm == nil || tm["k"] != "elapsed" || props(tm)["age"] != float64(1200) || props(tm)["stopped"] != float64(1200) || props(tm)["format"] != "short" {
			t.Fatalf("omp.run.time = %#v", tm)
		}
	})

	t.Run("eval_props_and_status_preview", func(t *testing.T) {
		// upstream: tools/eval.ts:743-748, 842-846, 922-927
		h := readyOmpRoles(t)
		lines := []string{"print(1)", "1", "phase=ok", "preview line"}
		view := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.run", Kind: fe.ViewKindVStack, Rows: 4, Width: 40,
				Children: []fe.ViewNode{
					{ID: "omp.tool.eval.input#0", Kind: fe.ViewKindLines, Rows: 1, Width: 40, Lines: []string{"print(1)"}},
					{ID: "omp.tool.eval.output#0", Kind: fe.ViewKindLines, Rows: 1, Width: 40, Lines: []string{"1"}},
					{ID: "omp.run.status#0", Kind: fe.ViewKindVStack, Rows: 2, Width: 40, Children: []fe.ViewNode{
						{ID: "omp.tool.eval.status.event#0", Kind: fe.ViewKindVStack, Rows: 2, Width: 40, Children: []fe.ViewNode{
							{ID: "omp.tool.eval.status#0", Kind: fe.ViewKindText, Rows: 1, Width: 40, Text: "phase=ok"},
							{ID: "omp.tool.eval.status.preview#0", Kind: fe.ViewKindLines, Rows: 1, Width: 40, Lines: []string{"preview line"}},
						}},
					}},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "ev1", Index: 0,
			Node: fe.ToolCard{
				Name:       "eval",
				Arguments:  map[string]any{"language": "py", "code": "print(1)"},
				Status:     fe.ToolDone,
				Result:     lines,
				ResultView: view,
			},
		})
		root := node(ops, "n.ev1")
		if props(root)["preview"] != "children" {
			t.Fatalf("eval preview = %v, want children", props(root)["preview"])
		}
		in := findByRole(root, "omp.tool.eval.input")
		if in == nil || in["k"] != "code" || props(in)["lang"] != "python" || props(in)["wrap"] != false ||
			!reflect.DeepEqual(props(in)["preview"], map[string]any{"lines": float64(6)}) {
			t.Fatalf("eval input = %#v", in)
		}
		out := findByRole(root, "omp.tool.eval.output")
		if out == nil || out["k"] != "ansi" || props(out)["follow"] != true ||
			!reflect.DeepEqual(props(out)["preview"], map[string]any{"lines": float64(10)}) {
			t.Fatalf("eval output = %#v", out)
		}
		prev := findByRole(root, "omp.tool.eval.status.preview")
		if prev == nil || prev["k"] != "code" || props(prev)["wrap"] != false ||
			!reflect.DeepEqual(props(prev)["preview"], map[string]any{"lines": float64(3)}) {
			t.Fatalf("eval status preview = %#v", prev)
		}
	})

	t.Run("B1_oversize_image_never_breaks_later_frames", func(t *testing.T) {
		h := readyOmpRoles(t)
		huge := &fe.ViewImage{Data: make([]byte, 16*1024*1024+1), MimeType: "image/png", Filename: "huge.png"}
		view := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.read.image", Kind: fe.ViewKindImage, Rows: 1, Width: 20, Image: huge,
			},
		}
		if err := h.session.Apply(fe.Frame{Ops: []fe.Op{
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "rimg", Index: 0, Node: fe.ToolCard{Name: "read", Status: fe.ToolDone, Result: []string{"[image]"}, ResultView: view, Images: []fe.ViewImage{*huge}}},
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "uimg", Index: 1, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "hi", Images: []fe.ViewImage{*huge}}},
		}}); err != nil {
			t.Fatalf("oversize image Apply failed: %v", err)
		}
		if err := h.session.Apply(fe.Frame{Ops: []fe.Op{
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "later", Index: 2, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "still alive"}},
		}}); err != nil {
			t.Fatalf("later frame Apply failed after oversize image: %v", err)
		}
	})

	t.Run("B2_roles_survive_box_and_unknown_omp_ancestors", func(t *testing.T) {
		h := readyOmpRoles(t)
		lines := []string{"Title", "Excerpt"}
		view := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				Kind: fe.ViewKindBox, Rows: 2, Width: 40,
				Children: []fe.ViewNode{
					{
						ID: "omp.tool.custom_box", Kind: fe.ViewKindVStack, Rows: 2, Width: 40,
						Children: []fe.ViewNode{
							{ID: "omp.tool.source#0", Kind: fe.ViewKindHStack, Rows: 1, Width: 40, Children: []fe.ViewNode{
								{ID: "omp.tool.source.head#0", Kind: fe.ViewKindText, Rows: 1, Width: 40, Text: "Title"},
							}},
							{ID: "omp.tool.excerpt#0", Kind: fe.ViewKindText, Rows: 1, Width: 40, Text: "Excerpt"},
						},
					},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "b2", Index: 0,
			Node: fe.ToolCard{Name: "web_search", Status: fe.ToolDone, Result: lines, ResultView: view},
		})
		root := node(ops, "n.b2")
		if findByRole(root, "omp.tool.custom_box") == nil {
			t.Fatalf("unknown omp container role dropped under Box: %#v", root)
		}
		if src := findByRole(root, "omp.tool.source"); src == nil || src["k"] != "row" {
			t.Fatalf("omp.tool.source dropped under unknown omp ancestor: %#v", src)
		}
		if ex := findByRole(root, "omp.tool.excerpt"); ex == nil || ex["k"] != "text" || props(ex)["lines"] != float64(3) {
			t.Fatalf("omp.tool.excerpt dropped under unknown omp ancestor: %#v", ex)
		}
	})

	t.Run("B3_section_head_and_gap_row_alignment", func(t *testing.T) {
		h := readyOmpRoles(t)
		lines := []string{"src/a.go +1 -1", "", "body line", "", "Done"}
		view := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.edit.file#0", Kind: fe.ViewKindVStack, Rows: 5, Width: 40, Gap: 1,
				Children: []fe.ViewNode{
					{ID: "omp.tool.edit.file.head#0", Kind: fe.ViewKindText, Rows: 1, Width: 40, Text: "src/a.go +1 -1"},
					{ID: "omp.tool.bash.output", Kind: fe.ViewKindLines, Rows: 1, Width: 40},
					{ID: "omp.tool.stats", Kind: fe.ViewKindLines, Rows: 1, Width: 40},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "b3", Index: 0,
			Node: fe.ToolCard{Name: "edit", Status: fe.ToolDone, Result: lines, ResultView: view},
		})
		root := node(ops, "n.b3")
		sec := findByRole(root, "omp.tool.edit.file")
		if sec == nil || sec["k"] != "section" {
			t.Fatalf("section = %#v", sec)
		}
		wantHead := []any{map[string]any{"t": "src/a.go +1 -1", "s": "path"}}
		if !reflect.DeepEqual(props(sec)["head"], wantHead) {
			t.Fatalf("section head = %#v, want %#v", props(sec)["head"], wantHead)
		}
		out := findByRole(sec, "omp.tool.bash.output")
		if out == nil || props(out)["text"] != "body line" {
			t.Fatalf("gap-shifted output = %#v, want 'body line'", out)
		}
		st := findByRole(sec, "omp.tool.stats")
		if st == nil || !reflect.DeepEqual(props(st)["spans"], []any{map[string]any{"t": "Done", "s": "muted"}}) {
			t.Fatalf("gap-shifted stats = %#v, want 'Done'", st)
		}
	})

	t.Run("B4_roleless_view_preserves_stack_props_and_hidden", func(t *testing.T) {
		h := readyOmpRoles(t)
		lines := []string{"left right"}
		view := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				Kind: fe.ViewKindVStack, Rows: 1, Width: 40, Gap: 1, Align: "center",
				Children: []fe.ViewNode{
					{
						Kind: fe.ViewKindHStack, Rows: 1, Width: 40, Gap: 2, Align: "end",
						Children: []fe.ViewNode{
							{Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "left", Stack: &fe.ViewStack{Basis: 10, Grow: 1, Shrink: 0, MaxSize: 12}},
							{Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "right"},
						},
					},
					{Kind: fe.ViewKindVStack, Rows: 0, Width: 40},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "b4", Index: 0,
			Node: fe.ToolCard{Name: "custom", Status: fe.ToolDone, Result: lines, ResultView: view},
		})
		vroot := node(ops, "n.b4.view")
		if vroot == nil || props(vroot)["align"] != "center" || props(vroot)["gap"] != "xs" {
			t.Fatalf("roleless vstack lost props: %#v", vroot)
		}
		rowNode := vroot["c"].([]any)[0].(map[string]any)
		if props(rowNode)["align"] != "end" || props(rowNode)["gap"] != "sm" {
			t.Fatalf("roleless hstack lost props: %#v", rowNode)
		}
		left := rowNode["c"].([]any)[0].(map[string]any)
		if props(left)["basis"] != 0.25 || props(left)["grow"] != float64(1) || props(left)["shrink"] != float64(0) {
			t.Fatalf("roleless stack child lost flex props: %#v", left)
		}
		hiddenCol := vroot["c"].([]any)[1].(map[string]any)
		if props(hiddenCol)["hidden"] != true {
			t.Fatalf("zero-row col lost hidden=true: %#v", hiddenCol)
		}
	})

	t.Run("B5_and_A10_ask_pills_proc_lists_glob_chips", func(t *testing.T) {
		h := readyOmpRoles(t)
		askView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.ask.pills", Kind: fe.ViewKindHStack, Rows: 1, Width: 40,
				Children: []fe.ViewNode{
					{ID: "omp.ask.pill#0", Kind: fe.ViewKindText, Rows: 1, Width: 15, Text: "Option A"},
				},
			},
		}
		procView := &fe.View{
			Seq: 2,
			Root: fe.ViewNode{
				ID: "omp.tool.proc.cancelled", Kind: fe.ViewKindLines, Rows: 1, Width: 40,
				Lines: []string{"bg_1 cancelled"},
			},
		}
		globChildren := make([]fe.ViewNode, 7)
		globLines := make([]string, 7)
		for i := range 7 {
			p := "dir/f" + string(rune('a'+i)) + ".go"
			globLines[i] = p
			globChildren[i] = fe.ViewNode{ID: "omp.tool.file#" + string(rune('0'+i)), Kind: fe.ViewKindText, Rows: 1, Width: 20, Text: p}
		}
		globView := &fe.View{
			Seq:  3,
			Root: fe.ViewNode{ID: "omp.tool.files", Kind: fe.ViewKindVStack, Rows: 7, Width: 20, Children: globChildren},
		}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "askp", Index: 0, Node: fe.ToolCard{Name: "ask", Status: fe.ToolRunning, Result: []string{"Option A"}, ResultView: askView}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "procc", Index: 1, Node: fe.ToolCard{Name: "write", Arguments: map[string]any{"path": "proc://bg_1/kill"}, Status: fe.ToolDone, Result: []string{"bg_1 cancelled"}, ResultView: procView}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "glob7", Index: 2, Node: fe.ToolCard{Name: "glob", Status: fe.ToolDone, Result: globLines, ResultView: globView}},
		)
		pills := findByRole(node(ops, "n.askp"), "omp.ask.pills")
		if pills == nil || pills["k"] != "row" || props(pills)["wrap"] != true {
			t.Fatalf("ask pills = %#v", pills)
		}
		pill := findByRole(pills, "omp.ask.pill")
		if pill == nil || pill["k"] != "text" {
			t.Fatalf("ask pill should be static text (no dead button): %#v", pill)
		}
		pc := findByRole(node(ops, "n.procc"), "omp.tool.proc.cancelled")
		if pc == nil || pc["k"] != "list" || len(pc["c"].([]any)) != 1 || pc["c"].([]any)[0].(map[string]any)["k"] != "item" {
			t.Fatalf("proc cancelled = %#v, want list of item", pc)
		}
		gf := findByRole(node(ops, "n.glob7"), "omp.tool.files")
		if gf == nil || gf["k"] != "row" || props(gf)["wrap"] != true || findByRole(gf, "omp.tool.chip") == nil {
			t.Fatalf("glob >6 files = %#v, want row wrap=true of omp.tool.chip", gf)
		}
	})
}

// TestOmpMultipleSessionsIsolated verifies that opening multiple omp frontend
// sessions in one process (across /new, /resume, or concurrent sessions) keeps
// all session state isolated: closing one session leaves the other fully
// usable, and calling FrontendWith multiple times has no shared mutable state.
func TestOmpMultipleSessionsIsolated(t *testing.T) {
	h1 := readyOmpRoles(t)
	h2 := readyOmpRoles(t)

	ops1 := h1.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "u1", Index: 0,
		Node: fe.MarkdownText{Role: fe.RoleUser, Text: "session 1"},
	})
	if u1 := node(ops1, "n.u1"); u1 == nil || props(u1)["role"] != "omp.user" {
		t.Fatalf("session 1 initial render failed: %#v", ops1)
	}

	ops2 := h2.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "u2", Index: 0,
		Node: fe.MarkdownText{Role: fe.RoleUser, Text: "session 2"},
	})
	if u2 := node(ops2, "n.u2"); u2 == nil || props(u2)["role"] != "omp.user" {
		t.Fatalf("session 2 initial render failed: %#v", ops2)
	}

	// Shut down session 1; session 2 must remain unaffected.
	if err := h1.session.Close(); err != nil {
		t.Fatalf("session 1 Close: %v", err)
	}

	ops2b := h2.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "t2", Index: 1,
		Node: fe.ToolCard{Name: "bash", Arguments: map[string]any{"command": "echo ok"}, Status: fe.ToolDone, Output: "ok"},
	})
	if t2 := node(ops2b, "n.t2"); t2 == nil || props(t2)["role"] != "omp.tool.bash" {
		t.Fatalf("session 2 broken after session 1 closed: %#v", ops2b)
	}

	// Calling FrontendWith twice and opening a third session works cleanly.
	f1 := FrontendWith(Options{Omp: true})
	f2 := FrontendWith(Options{Omp: true})
	if f1 == nil || f2 == nil {
		t.Fatal("FrontendWith returned nil")
	}
	h3 := readyOmpRoles(t)
	ops3 := h3.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "u3", Index: 0,
		Node: fe.MarkdownText{Role: fe.RoleUser, Text: "session 3"},
	})
	if u3 := node(ops3, "n.u3"); u3 == nil || props(u3)["role"] != "omp.user" {
		t.Fatalf("session 3 render failed: %#v", ops3)
	}
}

// TestOptionsOnSurfaceLifecycle checks Options.OnSurface transitions:
//   - declined Open (outside Tern) reports false;
//   - open native surface reports true;
//   - fallback reports false once;
//   - Close reports false once (and never duplicates after fallback).
func TestOptionsOnSurfaceLifecycle(t *testing.T) {
	var mu sync.Mutex
	var events []bool
	record := func(native bool) {
		mu.Lock()
		events = append(events, native)
		mu.Unlock()
	}
	takeEvents := func() []bool {
		mu.Lock()
		defer mu.Unlock()
		out := append([]bool(nil), events...)
		events = nil
		return out
	}

	// 1. Declined Open (not in Tern) reports false.
	feDeclined := FrontendWith(Options{Omp: true, OnSurface: record})
	sess, err := feDeclined.Open(fe.Env{
		Out:    &terminal{},
		Getenv: func(string) string { return "" },
	})
	if err != nil || sess != nil {
		t.Fatalf("declined Open = (%v, %v), want (nil, nil)", sess, err)
	}
	if got := takeEvents(); !reflect.DeepEqual(got, []bool{false}) {
		t.Fatalf("declined OnSurface events = %v, want [false]", got)
	}

	// 2. Successful Open + handshake reports true, then Close reports false.
	out := &terminal{}
	feOpen := FrontendWith(Options{Omp: true, OnSurface: record})
	raw, err := feOpen.Open(fe.Env{
		Out:     out,
		Columns: 120,
		Getenv: func(k string) string {
			if k == "TERM_PROGRAM" {
				return "tern"
			}
			return ""
		},
	})
	if err != nil || raw == nil {
		t.Fatalf("Open = (%v, %v)", raw, err)
	}
	s := raw.(*session)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.peek(), `"q":"hello"`) {
		time.Sleep(5 * time.Millisecond)
	}
	s.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"cols\":120,\"cell\":{\"w\":8,\"h\":16},\"credits\":64}\x1b\\\x1b[?62c")
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := takeEvents(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("open OnSurface events = %v, want [true]", got)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := takeEvents(); !reflect.DeepEqual(got, []bool{false}) {
		t.Fatalf("close OnSurface events = %v, want [false]", got)
	}

	// 3. Fallback reports false once; subsequent Close does not duplicate it.
	out2 := &terminal{}
	var fellBack sync.WaitGroup
	fellBack.Add(1)
	feFB := FrontendWith(Options{Omp: true, OnSurface: record})
	raw2, err := feFB.Open(fe.Env{
		Out:     out2,
		Columns: 120,
		Getenv: func(k string) string {
			if k == "TERM_PROGRAM" {
				return "tern"
			}
			return ""
		},
		Fallback: func(string) { fellBack.Done() },
	})
	if err != nil || raw2 == nil {
		t.Fatalf("Open fallback session = (%v, %v)", raw2, err)
	}
	s2 := raw2.(*session)
	s2.fallback("test fallback")
	fellBack.Wait()
	if got := takeEvents(); !reflect.DeepEqual(got, []bool{false}) {
		t.Fatalf("fallback OnSurface events = %v, want [false]", got)
	}
	_ = s2.Close()
	if got := takeEvents(); len(got) != 0 {
		t.Fatalf("close after fallback should not repeat OnSurface(false), got %v", got)
	}
}

// TestOmpAnsiNativeWaitTilesAndDebugKV verifies AnsiNative's native view trees
// for wait job tiles (omp.job.tiles, omp.job.tile.*, meter ring & bar) and
// debug session snapshot KV (omp.tool.debug.snapshot with Lines.List).
// upstream: packages/tui/src/tools/job-widget.ts:389-608, tools/debug.ts:134-174
func TestOmpAnsiNativeWaitTilesAndDebugKV(t *testing.T) {
	h := readyOmpRoles(t)
	waitView := &fe.View{
		Seq: 1,
		Root: fe.ViewNode{
			ID: "omp.tool.wait", Kind: fe.ViewKindContainer, Rows: 4, Width: 60,
			Children: []fe.ViewNode{
				{
					ID: "omp.job.tiles", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
					Children: []fe.ViewNode{
						{
							ID: "omp.job.tile#0", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
							Children: []fe.ViewNode{
								{
									ID: "omp.job.tile.head#0", Kind: fe.ViewKindHStack, Rows: 1, Width: 60,
									Children: []fe.ViewNode{
										{ID: "omp.job.tile.dot#0", Kind: fe.ViewKindText, Rows: 1, Width: 2, Text: "●"},
										{ID: "omp.job.tile.title#0", Kind: fe.ViewKindText, Rows: 1, Width: 20, Text: "Build"},
										{ID: "omp.job.tile.ring#0", Kind: fe.ViewKindLines, Rows: 1, Width: 6, Lines: []string{"50%"}, Progress: &fe.ViewProgress{Value: 50, Max: 100}},
										{ID: "omp.run.time#0", Kind: fe.ViewKindText, Rows: 1, Width: 6, Text: "2.5s"},
									},
								},
								{
									ID: "omp.job.tile.bars#0", Kind: fe.ViewKindVStack, Rows: 1, Width: 60,
									Children: []fe.ViewNode{
										{
											ID: "omp.job.tile.bar#0.0", Kind: fe.ViewKindHStack, Rows: 1, Width: 60,
											Children: []fe.ViewNode{
												{ID: "omp.job.tile.key#0.0", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "tests"},
												{ID: "omp.job.tile.meter#0.0", Kind: fe.ViewKindLines, Rows: 1, Width: 20, Lines: []string{"===="}, Progress: &fe.ViewProgress{Value: 3, Max: 4}},
												{ID: "omp.job.tile.num#0.0", Kind: fe.ViewKindText, Rows: 1, Width: 6, Text: "3/4"},
											},
										},
									},
								},
								{ID: "omp.job.tile.stage#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "running unit tests"},
							},
						},
					},
				},
				{ID: "omp.wait.more", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "+2 more jobs · /jobs"},
			},
		},
	}
	debugView := &fe.View{
		Seq: 2,
		Root: fe.ViewNode{
			ID: "omp.tool.debug", Kind: fe.ViewKindContainer, Rows: 2, Width: 60,
			Children: []fe.ViewNode{
				{
					ID: "omp.tool.debug.snapshot", Kind: fe.ViewKindLines, Rows: 2, Width: 60,
					Lines: []string{"Status: stopped", "Program: ./app"},
					List: &fe.ViewList{Items: []fe.ViewListItem{
						{Label: "Status", Detail: "stopped"},
						{Label: "Program", Detail: "./app"},
					}},
				},
			},
		},
	}
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "w1", Index: 0, Node: fe.ToolCard{
			Name: "wait", Status: fe.ToolRunning,
			Result: []string{"● Build 50% 2.5s", "tests ==== 3/4", "running unit tests", "+2 more jobs · /jobs"}, ResultView: waitView,
		}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "d1", Index: 1, Node: fe.ToolCard{
			Name: "debug", Arguments: map[string]any{"action": "status"}, Status: fe.ToolDone,
			Result: []string{"Status: stopped", "Program: ./app"}, ResultView: debugView,
		}},
	)

	var findByRole func(n map[string]any, role string) map[string]any
	findByRole = func(n map[string]any, role string) map[string]any {
		if n == nil {
			return nil
		}
		if p := props(n); p != nil && p["role"] == role {
			return n
		}
		if cs, ok := n["c"].([]any); ok {
			for _, c := range cs {
				if cm, ok := c.(map[string]any); ok {
					if found := findByRole(cm, role); found != nil {
						return found
					}
				}
			}
		}
		return nil
	}

	w := node(ops, "n.w1")
	if tiles := findByRole(w, "omp.job.tiles"); tiles == nil || tiles["k"] != "row" || props(tiles)["wrap"] != true {
		t.Fatalf("omp.job.tiles = %#v", tiles)
	}
	if ring := findByRole(w, "omp.job.tile.ring"); ring == nil || ring["k"] != "meter" || props(ring)["style"] != "ring" || props(ring)["value"] != 0.5 || props(ring)["label"] != "50%" {
		t.Fatalf("omp.job.tile.ring = %#v", ring)
	}
	if meter := findByRole(w, "omp.job.tile.meter"); meter == nil || meter["k"] != "meter" || props(meter)["value"] != 0.75 {
		t.Fatalf("omp.job.tile.meter = %#v", meter)
	}
	if more := findByRole(w, "omp.wait.more"); more == nil || more["k"] != "text" {
		t.Fatalf("omp.wait.more = %#v", more)
	}

	d := node(ops, "n.d1")
	snap := findByRole(d, "omp.tool.debug.snapshot")
	if snap == nil || snap["k"] != "kv" {
		t.Fatalf("omp.tool.debug.snapshot = %#v, want kind kv", snap)
	}
	items, ok := props(snap)["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("debug snapshot kv items = %#v", props(snap)["items"])
	}
}

// TestOmpPass2ReviewFixes tests N2, N3, N4, N5, and N6 from the pass-2 review.
func TestOmpPass2ReviewFixes(t *testing.T) {
	var findByRole func(n map[string]any, role string) map[string]any
	findByRole = func(n map[string]any, role string) map[string]any {
		if n == nil {
			return nil
		}
		if p := props(n); p != nil && p["role"] == role {
			return n
		}
		if cs, ok := n["c"].([]any); ok {
			for _, c := range cs {
				if cm, ok := c.(map[string]any); ok {
					if found := findByRole(cm, role); found != nil {
						return found
					}
				}
			}
		}
		return nil
	}

	t.Run("N2_eval_and_wait_without_view_clamp_output", func(t *testing.T) {
		h := readyOmpRoles(t)
		bigOut := strings.Repeat("line\n", 200)
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ev0", Index: 0, Node: fe.ToolCard{
				Name: "eval", Arguments: map[string]any{"language": "py", "code": "for i in range(200): print(i)"},
				Status: fe.ToolRunning, Output: bigOut,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "wt0", Index: 1, Node: fe.ToolCard{
				Name: "wait", Arguments: map[string]any{},
				Status: fe.ToolRunning, Output: bigOut,
			}},
		)
		for _, id := range []string{"n.ev0", "n.wt0"} {
			n := node(ops, id)
			if n == nil {
				t.Fatalf("missing %s", id)
			}
			if props(n)["preview"] == "children" {
				t.Fatalf("%s without ResultView must not use preview:\"children\" (unclamped body): %#v", id, props(n))
			}
			if !reflect.DeepEqual(props(n)["preview"], map[string]any{"tail": float64(5)}) {
				t.Fatalf("%s preview = %#v, want {tail: 5}", id, props(n)["preview"])
			}
		}
	})

	t.Run("N3_firstChangedDiffLine_handles_double_minus_and_double_plus_content", func(t *testing.T) {
		d1 := &fe.Diff{Text: "--- a/x.sql\n+++ b/x.sql\n@@ -10,3 +10,2 @@\n SELECT 1;\n--- old comment\n SELECT 2;\n"}
		if got := firstChangedDiffLine(d1); got != 11 {
			t.Fatalf("deleted '-- ' line = %d, want 11", got)
		}
		d2 := &fe.Diff{Text: "--- a/x.c\n+++ b/x.c\n@@ -5,2 +5,3 @@\n int i = 0;\n+++i;\n return i;\n"}
		if got := firstChangedDiffLine(d2); got != 6 {
			t.Fatalf("added '++' line = %d, want 6", got)
		}
	})

	t.Run("N4_settled_card_and_settled_cell_or_job_stop_timers", func(t *testing.T) {
		h := readyOmpRoles(t)
		settledEval := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.eval", Kind: fe.ViewKindContainer, Rows: 1, Width: 40,
				Children: []fe.ViewNode{
					{ID: "omp.run.foot", Kind: fe.ViewKindHStack, Rows: 1, Width: 40, Children: []fe.ViewNode{
						{ID: "omp.run.state", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "Done"},
						{ID: "omp.run.time", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "???"},
					}},
				},
			},
		}
		runningEval := &fe.View{
			Seq: 2,
			Root: fe.ViewNode{
				ID: "omp.tool.eval", Kind: fe.ViewKindContainer, Rows: 2, Width: 40,
				Children: []fe.ViewNode{
					{ID: "omp.run.foot#0", Kind: fe.ViewKindHStack, Rows: 1, Width: 40, Children: []fe.ViewNode{
						{ID: "omp.run.state#0", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "Done"},
						{ID: "omp.run.time#0", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "500ms"},
					}},
					{ID: "omp.run.foot#1", Kind: fe.ViewKindHStack, Rows: 1, Width: 40, Children: []fe.ViewNode{
						{ID: "omp.run.state#1", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "Running"},
						{ID: "omp.run.time#1", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "200ms"},
					}},
				},
			},
		}
		runningWait := &fe.View{
			Seq: 3,
			Root: fe.ViewNode{
				ID: "omp.tool.wait", Kind: fe.ViewKindContainer, Rows: 2, Width: 40,
				Children: []fe.ViewNode{
					{ID: "omp.job.tile.head#0", Kind: fe.ViewKindHStack, Rows: 1, Width: 40, Children: []fe.ViewNode{
						{ID: "omp.job.tile.dot#0", Kind: fe.ViewKindText, Rows: 1, Width: 2, Text: "✓"},
						{ID: "omp.run.time#0", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "1.5s"},
					}},
					{ID: "omp.job.tile.head#1", Kind: fe.ViewKindHStack, Rows: 1, Width: 40, Children: []fe.ViewNode{
						{ID: "omp.job.tile.dot#1", Kind: fe.ViewKindText, Rows: 1, Width: 2, Text: "●"},
						{ID: "omp.run.time#1", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "300ms"},
					}},
				},
			},
		}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "se", Index: 0, Node: fe.ToolCard{Name: "eval", Status: fe.ToolDone, Result: []string{"Done ???"}, ResultView: settledEval}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "re", Index: 1, Node: fe.ToolCard{Name: "eval", Status: fe.ToolRunning, Result: []string{"Done 500ms", "Running 200ms"}, ResultView: runningEval}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "rw", Index: 2, Node: fe.ToolCard{Name: "wait", Status: fe.ToolRunning, Result: []string{"✓ 1.5s", "● 300ms"}, ResultView: runningWait}},
		)

		tmSettled := findByRole(node(ops, "n.se"), "omp.run.time")
		if tmSettled == nil || props(tmSettled)["stopped"] != float64(0) {
			t.Fatalf("settled eval unparseable timer = %#v, want stopped:0", tmSettled)
		}
		reChildren := node(ops, "n.re")["c"].([]any)
		t0 := findByRole(reChildren[0].(map[string]any), "omp.run.time")
		t1 := findByRole(reChildren[1].(map[string]any), "omp.run.time")
		if props(t0)["stopped"] != float64(500) {
			t.Fatalf("done cell in running eval = %#v, want stopped:500", t0)
		}
		if props(t1)["stopped"] != nil {
			t.Fatalf("running cell in running eval = %#v, want live (no stopped)", t1)
		}
		rwChildren := node(ops, "n.rw")["c"].([]any)
		// upstream: tools/job-widget.ts:409,450 — tile head clock is a bare `elapsed` (no role).
		w0 := rwChildren[0].(map[string]any)["c"].([]any)[1].(map[string]any)
		w1 := rwChildren[1].(map[string]any)["c"].([]any)[1].(map[string]any)
		if w0["k"] != "elapsed" || props(w0)["role"] != nil || props(w0)["stopped"] != float64(1500) {
			t.Fatalf("completed job in running wait = %#v, want bare elapsed stopped:1500", w0)
		}
		if w1["k"] != "elapsed" || props(w1)["role"] != nil || props(w1)["stopped"] != nil {
			t.Fatalf("running job in running wait = %#v, want bare elapsed live (no stopped)", w1)
		}
	})

	t.Run("N5_formatOmpScalar_cuts_by_runes_valid_utf8", func(t *testing.T) {
		s := strings.Repeat("a", 22) + "é日z"
		got := formatOmpScalar(s, 24)
		want := `"` + strings.Repeat("a", 22) + `é…"`
		if got != want {
			t.Fatalf("formatOmpScalar = %q, want %q", got, want)
		}
	})

	t.Run("N6_onSurface_never_ends_true_after_close_or_fallback", func(t *testing.T) {
		var mu sync.Mutex
		var last bool
		var calls []bool
		s := newSession(fe.Env{Out: &terminal{}, Getenv: func(string) string { return "" }})
		s.onSurface = func(native bool) {
			mu.Lock()
			last = native
			calls = append(calls, native)
			mu.Unlock()
		}
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.notifySurface(true)
		s.notifySurface(false)
		mu.Lock()
		defer mu.Unlock()
		if last || !reflect.DeepEqual(calls, []bool{false}) {
			t.Fatalf("OnSurface after close: last=%v calls=%v, want last=false calls=[false]", last, calls)
		}
	})

	t.Run("N6_diagnostics_section_tone_and_per_severity_icons", func(t *testing.T) {
		// upstream: packages/tui/src/tools/native-view.ts:309-324, 380-388
		h := readyOmpRoles(t)
		diagView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.edit", Kind: fe.ViewKindContainer, Rows: 5, Width: 60,
				Children: []fe.ViewNode{
					{
						ID: "omp.tool.diagnostics", Kind: fe.ViewKindVStack, Rows: 5, Width: 60,
						Children: []fe.ViewNode{
							{ID: "omp.tool.diagnostics.head", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "Diagnostics"},
							{ID: "omp.tool.diagnostic#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "[error] 10:2 undefined: x"},
							{ID: "omp.tool.diagnostic#1", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "[warning] 12:1 unused var"},
							{ID: "omp.tool.diagnostic#2", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "[info] 14:1 note"},
							{ID: "omp.tool.diagnostic#3", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "[hint] 16:1 simplify"},
						},
					},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "diag1", Index: 0,
			Node: fe.ToolCard{
				Name: "edit", Status: fe.ToolDone,
				Result:     []string{"Diagnostics", "[error] 10:2 undefined: x", "[warning] 12:1 unused var", "[info] 14:1 note", "[hint] 16:1 simplify"},
				ResultView: diagView,
			},
		})
		sec := findByRole(node(ops, "n.diag1"), "omp.tool.diagnostics")
		if sec == nil || props(sec)["tone"] != "error" {
			t.Fatalf("diagnostics section with error row = %#v, want tone=error", sec)
		}
		wantIcons := [][2]string{
			{"x-circle", "error"},
			{"warn", "warning"},
			{"info", "info"},
			{"lightbulb", "muted"},
		}
		rows := sec["c"].([]any)
		if len(rows) != len(wantIcons) {
			t.Fatalf("diagnostic rows = %d, want %d", len(rows), len(wantIcons))
		}
		for i, want := range wantIcons {
			icon := rows[i].(map[string]any)["c"].([]any)[0].(map[string]any)
			if props(icon)["name"] != want[0] || props(icon)["tone"] != want[1] {
				t.Errorf("row %d icon = %#v, want name=%q tone=%q", i, icon, want[0], want[1])
			}
		}
	})
}

// TestOmpDrawRoundParityAgainstProof9 asserts the exact TSP node shapes and
// props captured from real omp v19.1.1 (Tern proof capture)
// for: read (success & short error with empty ResultView), edit (absolute
// home-shortened target + diff{hunks}), todo (checklist + closed/total),
// ask (question target + frame:card + section per answer), task (1 agent +
// background badge + agent node with hovercard), and welcome (card[omp.welcome]).
func TestOmpDrawRoundParityAgainstProof9(t *testing.T) {
	t.Run("finding1_read_bare_row_and_inline_error_even_with_ResultView", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//   tool[omp.tool.read]{target:"notes.txt", targetKind:"path", status:"done", frame:"inline", collapsible:false} (no children)
		//   tool[omp.tool.read]{target:"does-not-exist-proof.txt", targetKind:"path", meta:[[{"t":"— Path 'does-not-exist-proof.txt' not found","s":"muted"}]], status:"error", tone:"error", frame:"inline", collapsible:false} (no children)
		h := readyOmpRoles(t)
		h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "/tmp/proof/work"}))
		emptyReadView := &fe.View{
			Seq:  1,
			Root: fe.ViewNode{ID: "omp.tool.read", Kind: fe.ViewKindContainer, Rows: 0, Width: 80},
		}
		ops := h.applyOps(
			fe.Op{
				Kind: fe.Insert, Region: fe.RegionMain, ID: "r_ok", Index: 0,
				Node: fe.ToolCard{
					Name:       "read",
					Arguments:  map[string]any{"path": "notes.txt"},
					Status:     fe.ToolDone,
					Output:     "title: proof fixture\nstatus: draft\nvalue: 42",
					Result:     []string{},
					ResultView: emptyReadView,
				},
			},
			fe.Op{
				Kind: fe.Insert, Region: fe.RegionMain, ID: "r_err", Index: 1,
				Node: fe.ToolCard{
					Name:       "read",
					Arguments:  map[string]any{"path": "does-not-exist-proof.txt"},
					Status:     fe.ToolError,
					Output:     "Path 'does-not-exist-proof.txt' not found",
					Result:     []string{},
					ResultView: emptyReadView,
				},
			},
		)
		okNode := node(ops, "n.r_ok")
		if okNode == nil || okNode["k"] != "tool" ||
			props(okNode)["role"] != "omp.tool.read" ||
			props(okNode)["target"] != "notes.txt" ||
			props(okNode)["targetKind"] != "path" ||
			props(okNode)["frame"] != "inline" ||
			props(okNode)["collapsible"] != false ||
			okNode["c"] != nil {
			t.Fatalf("read success = %#v, want bare inline row with no children", okNode)
		}
		errNode := node(ops, "n.r_err")
		wantMeta := []any{[]any{map[string]any{"t": "— Path 'does-not-exist-proof.txt' not found", "s": "muted"}}}
		if errNode == nil || errNode["k"] != "tool" ||
			props(errNode)["role"] != "omp.tool.read" ||
			props(errNode)["target"] != "does-not-exist-proof.txt" ||
			props(errNode)["status"] != "error" ||
			props(errNode)["tone"] != "error" ||
			props(errNode)["frame"] != "inline" ||
			props(errNode)["collapsible"] != false ||
			!reflect.DeepEqual(props(errNode)["meta"], wantMeta) ||
			errNode["c"] != nil {
			t.Fatalf("failing read = %#v, want inline error gist with no children", errNode)
		}
	})

	t.Run("finding2_edit_absolute_target_and_diff_hunks", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//   tool[omp.tool.edit]{target:"/tmp/proof/work/notes.txt:2", targetKind:"path", meta:["+1 −1"], frame:"card"}
		//     diff{path:"/tmp/proof/work/notes.txt", hunks:[{oldStart:1, newStart:1, lines:[" title: proof fixture","-status: draft","+status: verified"," value: 42"]}]}
		h := readyOmpRoles(t)
		h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "/tmp/proof/work"}))
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "ed1", Index: 0,
			Node: fe.ToolCard{
				Name:      "edit",
				Arguments: map[string]any{"input": "[notes.txt#TAG]\nPUT 2.=2:\n+status: verified\n"},
				Status:    fe.ToolDone,
				Diff: &fe.Diff{
					Path: "notes.txt",
					Text: "--- notes.txt\n+++ notes.txt\n@@ -1,3 +1,3 @@\n title: proof fixture\n-status: draft\n+status: verified\n value: 42\n",
				},
			},
		})
		ed := node(ops, "n.ed1")
		if ed == nil || props(ed)["target"] != "/tmp/proof/work/notes.txt:2" ||
			props(ed)["targetKind"] != "path" ||
			!reflect.DeepEqual(props(ed)["meta"], []any{"+1 −1"}) {
			t.Fatalf("edit tool = %#v", ed)
		}
		diffNode := node(ops, "n.ed1.diff")
		wantHunks := []any{
			map[string]any{
				"oldStart": float64(1),
				"newStart": float64(1),
				"lines":    []any{" title: proof fixture", "-status: draft", "+status: verified", " value: 42"},
			},
		}
		if diffNode == nil || diffNode["k"] != "diff" ||
			props(diffNode)["path"] != "/tmp/proof/work/notes.txt" ||
			props(diffNode)["text"] != nil ||
			!reflect.DeepEqual(props(diffNode)["hunks"], wantHunks) {
			t.Fatalf("edit diff = %#v, want hunks=%#v", diffNode, wantHunks)
		}
	})

	t.Run("finding5_todo_native_checklist_and_closed_total_target", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//   tool[omp.tool.todo]{target:"0/2", targetKind:"text", frame:"card", collapsible:true, collapsed:false, preview:"none"}
		//     checklist{mode:"full", phases:[{id:"p0", title:"Verify", collapsed:false, items:[
		//       {id:"p0.0", text:"Check read card", status:"active"},
		//       {id:"p0.1", text:"Check todo card", status:"pending"}]}]}
		h := readyOmpRoles(t)
		lines := []string{"Verify", "◐ Check read card", "active", "☐ Check todo card", "pending"}
		todoView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.todo", Kind: fe.ViewKindContainer, Rows: 5, Width: 60,
				Children: []fe.ViewNode{
					{
						ID: "omp.todo.checklist", Kind: fe.ViewKindVStack, Rows: 5, Width: 60,
						Children: []fe.ViewNode{
							{
								ID: "omp.todo.phase#0", Kind: fe.ViewKindVStack, Rows: 5, Width: 60,
								Children: []fe.ViewNode{
									{ID: "omp.todo.phase.head#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "Verify"},
									{ID: "omp.todo.item#0.0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "◐ Check read card"},
									{ID: "omp.todo.item.status#0.0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "active"},
									{ID: "omp.todo.item#0.1", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "☐ Check todo card"},
									{ID: "omp.todo.item.status#0.1", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "pending"},
								},
							},
						},
					},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "td_p9", Index: 0,
			Node: fe.ToolCard{
				Name:       "todo",
				Arguments:  map[string]any{"op": "init"},
				Status:     fe.ToolDone,
				Result:     lines,
				ResultView: todoView,
			},
		})
		td := node(ops, "n.td_p9")
		if td == nil || props(td)["target"] != "0/2" || props(td)["targetKind"] != "text" ||
			props(td)["frame"] != "card" || props(td)["collapsible"] != true ||
			props(td)["collapsed"] != false || props(td)["preview"] != "none" {
			t.Fatalf("todo tool = %#v", td)
		}
		children := td["c"].([]any)
		if len(children) != 1 {
			t.Fatalf("todo children = %#v, want 1 checklist node", children)
		}
		cl := children[0].(map[string]any)
		wantPhases := []any{
			map[string]any{
				"id":        "p0",
				"title":     "Verify",
				"collapsed": false,
				"items": []any{
					map[string]any{"id": "p0.0", "text": "Check read card", "status": "active"},
					map[string]any{"id": "p0.1", "text": "Check todo card", "status": "pending"},
				},
			},
		}
		if cl["k"] != "checklist" || props(cl)["mode"] != "full" || !reflect.DeepEqual(props(cl)["phases"], wantPhases) {
			t.Fatalf("todo checklist = %#v, want mode=full phases=%#v", cl, wantPhases)
		}
	})

	t.Run("finding6_ask_question_target_card_frame_and_section_per_answer", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//   tool[omp.tool.ask]{target:"Choose a mode:", targetKind:"text", status:"done", frame:"card", collapsible:true, collapsed:true, preview:{lines:10}}
		//     section{}
		//       text{wrap:"word", spans:[{t:"Choose a mode:"}]}
		//       row[omp.tool.answer]{gap:"sm", align:"baseline"}
		//         icon{name:"check", tone:"success"}
		//         text{wrap:"word", spans:[{t:"Fast", s:"toolOutput"}]}
		h := readyOmpRoles(t)
		htmlForm := `<p>Choose a mode:</p><label><input type=radio name=mode value=fast checked> Fast</label><label><input type=radio name=mode value=safe> Safe</label>`
		// Pending call: inline frame, head target is the question, NOT "Fast".
		pendingOps := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "ask_p9", Index: 0,
			Node: fe.ToolCard{
				Name:      "ask",
				Arguments: map[string]any{"html": htmlForm},
				Status:    fe.ToolRunning,
			},
		})
		askPend := node(pendingOps, "n.ask_p9")
		if askPend == nil || props(askPend)["target"] != "Choose a mode:" || props(askPend)["frame"] != "inline" {
			t.Fatalf("pending ask = %#v, want target='Choose a mode:' frame=inline", askPend)
		}
		// Answered result: frame switches to card, body is direct section(text, row[omp.tool.answer]).
		askLines := []string{"Choose a mode:", "Fast"}
		askView := &fe.View{
			Seq: 2,
			Root: fe.ViewNode{
				ID: "omp.tool.ask", Kind: fe.ViewKindContainer, Rows: 2, Width: 60,
				Children: []fe.ViewNode{
					{
						ID: "omp.ask.card.body", Kind: fe.ViewKindVStack, Rows: 2, Width: 60,
						Children: []fe.ViewNode{
							{
								ID: "omp.ask.fold#0", Kind: fe.ViewKindVStack, Rows: 2, Width: 60,
								Children: []fe.ViewNode{
									{ID: "omp.ask.fold.question#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "Choose a mode:"},
									{ID: "omp.tool.answer#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "Fast"},
								},
							},
						},
					},
				},
			},
		}
		doneOps := h.applyOps(fe.Op{
			Kind: fe.Update, Region: fe.RegionMain, ID: "ask_p9", Index: 0,
			Node: fe.ToolCard{
				Name:       "ask",
				Arguments:  map[string]any{"html": htmlForm},
				Status:     fe.ToolDone,
				Result:     askLines,
				ResultView: askView,
			},
		})
		askDone := node(doneOps, "n.ask_p9")
		if askDone == nil || props(askDone)["target"] != "Choose a mode:" || props(askDone)["frame"] != "card" ||
			props(askDone)["collapsible"] != true || props(askDone)["collapsed"] != true {
			t.Fatalf("answered ask = %#v", askDone)
		}
		children := askDone["c"].([]any)
		if len(children) != 1 {
			t.Fatalf("ask children = %#v, want 1 section", children)
		}
		sec := children[0].(map[string]any)
		if sec["k"] != "section" {
			t.Fatalf("ask child = %#v, want section", sec)
		}
		secKids := sec["c"].([]any)
		if len(secKids) != 2 {
			t.Fatalf("ask section children = %#v, want [text, row[omp.tool.answer]]", secKids)
		}
		qNode := secKids[0].(map[string]any)
		ansRow := secKids[1].(map[string]any)
		if qNode["k"] != "text" || !reflect.DeepEqual(props(qNode)["spans"], []any{map[string]any{"t": "Choose a mode:"}}) {
			t.Fatalf("ask question node = %#v", qNode)
		}
		if ansRow["k"] != "row" || props(ansRow)["role"] != "omp.tool.answer" {
			t.Fatalf("ask answer row = %#v", ansRow)
		}
		rowKids := ansRow["c"].([]any)
		if rowKids[0].(map[string]any)["k"] != "icon" || props(rowKids[0].(map[string]any))["name"] != "check" ||
			!reflect.DeepEqual(props(rowKids[1].(map[string]any))["spans"], []any{map[string]any{"t": "Fast", "s": "toolOutput"}}) {
			t.Fatalf("ask answer row children = %#v", rowKids)
		}
	})

	t.Run("finding7_task_agent_node_and_head_target", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//   tool[omp.tool.task]{target:"1 agent", targetKind:"text", badges:[{text:"background"}], frame:"card", collapsible:true, collapsed:true, preview:"none"}
		//     text[omp.tool.context]{wrap:"word", lines:2, spans:[{t:"Proof subagent check.", s:"muted"}]}
		//     agent{name:"ProofChild", agent:"task", task:"Reply with PROOF_CHILD_OK.", status:"done", model:"proof/proof-1", thinking:"thinkingHigh", stats:{requests:1, took:39}, badges:[{text:"background"}], collapsible:true, collapsed:true, hovercard:{k:"md", p:{role:"omp.task.assignment", text:"Reply with PROOF_CHILD_OK."}}}
		//       md[omp.task.assignment]{text:"Reply with PROOF_CHILD_OK."}
		h := readyOmpRoles(t)
		lines := []string{
			"Proof subagent check.",
			"ProofChild (task) · done · proof/proof-1 · background",
			"ProofChild", "task", "done", "Reply with PROOF_CHILD_OK.",
			"proof/proof-1", "thinkingHigh", "1", "39", "background",
			"Reply with PROOF_CHILD_OK.",
		}
		taskView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.task", Kind: fe.ViewKindContainer, Rows: len(lines), Width: 80,
				Children: []fe.ViewNode{
					{ID: "omp.tool.context", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "Proof subagent check."},
					{
						ID: "omp.task.agent#0", Kind: fe.ViewKindVStack, Rows: len(lines) - 1, Width: 80,
						Children: []fe.ViewNode{
							{ID: "omp.task.agent.row#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: lines[1]},
							{ID: "omp.task.agent.name#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "ProofChild"},
							{ID: "omp.task.agent.kind#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "task"},
							{ID: "omp.task.agent.status#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "done"},
							{ID: "omp.task.agent.task#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "Reply with PROOF_CHILD_OK."},
							{ID: "omp.task.agent.model#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "proof/proof-1"},
							{ID: "omp.task.agent.thinking#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "thinkingHigh"},
							{ID: "omp.task.agent.requests#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "1"},
							{ID: "omp.task.agent.took#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "39"},
							{ID: "omp.task.agent.badges#0", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: "background"},
							{ID: "omp.task.assignment#0", Kind: fe.ViewKindMarkdown, Rows: 1, Width: 80, Text: "Reply with PROOF_CHILD_OK."},
						},
					},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "tk_p9", Index: 0,
			Node: fe.ToolCard{
				Name:       "task",
				Arguments:  map[string]any{"context": "Proof subagent check.", "tasks": []any{map[string]any{"name": "ProofChild", "task": "Reply with PROOF_CHILD_OK."}}},
				Status:     fe.ToolDone,
				Result:     lines,
				ResultView: taskView,
			},
		})
		tk := node(ops, "n.tk_p9")
		if tk == nil || props(tk)["target"] != "1 agent" || props(tk)["targetKind"] != "text" ||
			props(tk)["preview"] != "none" ||
			!reflect.DeepEqual(props(tk)["badges"], []any{map[string]any{"text": "background"}}) {
			t.Fatalf("task tool = %#v", tk)
		}
		children := tk["c"].([]any)
		if len(children) != 2 {
			t.Fatalf("task children = %#v, want [context, agent]", children)
		}
		ag := children[1].(map[string]any)
		if ag["k"] != "agent" || props(ag)["name"] != "ProofChild" || props(ag)["agent"] != "task" ||
			props(ag)["status"] != "done" || props(ag)["model"] != "proof/proof-1" ||
			props(ag)["thinking"] != "thinkingHigh" ||
			!reflect.DeepEqual(props(ag)["stats"], map[string]any{"requests": float64(1), "took": float64(39)}) ||
			!reflect.DeepEqual(props(ag)["badges"], []any{map[string]any{"text": "background"}}) ||
			!reflect.DeepEqual(props(ag)["hovercard"], map[string]any{"k": "md", "p": map[string]any{"role": "omp.task.assignment", "text": "Reply with PROOF_CHILD_OK."}}) {
			t.Fatalf("task agent node = %#v", ag)
		}
		agKids := ag["c"].([]any)
		if len(agKids) != 1 || agKids[0].(map[string]any)["k"] != "md" || props(agKids[0].(map[string]any))["role"] != "omp.task.assignment" {
			t.Fatalf("task agent children = %#v, want [md[omp.task.assignment]]", agKids)
		}
	})

	t.Run("welcome_card_replaces_pig_header_under_ompRoles", func(t *testing.T) {
		// Expected structure from omp v19.1.1 Tern capture:
		//   card[omp.welcome]
		//     row[omp.welcome.lockup]{align:"center", gap:"md"}
		//       image[omp.welcome.logo]{builtin:"omp", alt:"oh-my-pig", w:128}
		//       col[omp.welcome.mark]
		//         text[omp.welcome.wordmark]{wrap:"none", spans:[{t:"oh-my-pig", s:"strong"}]}
		//         text[omp.welcome.version]{wrap:"none", spans:[{t:"v0.1.0", s:"dim mono"}]}
		//     row[omp.welcome.tip]{gap:"sm", align:"start"}
		//       icon[omp.welcome.tip-icon]{name:"lightbulb"}
		//       text[omp.welcome.tip-text]{wrap:"word", text:<registered-command tip>}
		h := readyOmpRoles(t)
		h.session.env.AppName = "oh-my-pig"
		h.session.env.Version = "0.1.0"
		h.session.welcomeTipIdx = 4
		pigHeader := fe.Lines{
			Lines: []string{
				"   ▄▄▄▄  ▄▄▄▄  v0.7.0+1.5.2",
				"   escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash ·",
				"   ctrl+o more",
				"   Press ctrl+o to show full startup help and loaded resources.",
			},
			Click: &fe.Area{Row: 0, Column: 1, Rows: 4, Columns: 16},
		}
		ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "n2", Index: 0, Node: pigHeader})
		w := node(ops, "n.n2")
		if w == nil || w["k"] != "card" || props(w)["role"] != "omp.welcome" {
			t.Fatalf("welcome node = %#v, want card[omp.welcome]", w)
		}
		logo := node(ops, "n.n2.lockup/logo")
		if logo == nil || logo["k"] != "image" || props(logo)["blob"] == nil || props(logo)["w"] != float64(ompLogoWidthPx) || props(logo)["h"] != float64(ompLogoHeightPx) || props(logo)["role"] != "omp.welcome.logo" {
			t.Fatalf("welcome logo = %#v", logo)
		}
		wm := node(ops, "n.n2.lockup/mark/omp.welcome.wordmark")
		if wm == nil || !reflect.DeepEqual(props(wm)["spans"], []any{map[string]any{"t": "oh-my-pig", "s": "strong"}}) {
			t.Fatalf("welcome wordmark = %#v", wm)
		}
		ver := node(ops, "n.n2.lockup/mark/omp.welcome.version")
		if ver == nil || !reflect.DeepEqual(props(ver)["spans"], []any{map[string]any{"t": "v0.1.0", "s": "dim mono"}}) {
			t.Fatalf("welcome version = %#v", ver)
		}
		tipText := node(ops, "n.n2.tip/1")
		if tipText == nil || props(tipText)["role"] != "omp.welcome.tip-text" ||
			props(tipText)["text"] != "Try starting your prompt with a ->, and writing a list (1. Do X, 2. Do Y)" {
			t.Fatalf("welcome tip = %#v", tipText)
		}
		// Verify no tip references unregistered omp commands.
		for _, tip := range ompWelcomeTips {
			for _, bad := range []string{"/shake", "/tan", "/omfg", "/collab", "/advisor", "/cleanse", "omp stats", "omp -c", "omp auth-"} {
				if strings.Contains(tip, bad) {
					t.Errorf("ompWelcomeTips contains unregistered command %q in %q", bad, tip)
				}
			}
		}
	})
}

// TestOmpDrawRound2ParityAgainstProof10 asserts the exact TSP node shapes and
// props captured from real omp v19.1.1 (Tern proof capture)
// for wait, web_search (error), write, grep, glob, lsp, ast_edit, explicit
// `omp.head.*` overrides, and provider error card (`omp.error` code badge + actions).
func TestOmpDrawRound2ParityAgainstProof10(t *testing.T) {
	t.Run("item2_wait_settled_card_tone_and_title_spans", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//     row[omp.job.tiles](col[omp.job.tile]{tone:"success"}(row[omp.job.tile.head](text[omp.job.tile.dot]{spans:[{t:"✓",s:"success"}]}, text[omp.job.tile.title]{spans:[{t:"ProofChild"}]}, elapsed)))
		h := readyOmpRoles(t)
		lines := []string{"✓", "ProofChild", "37ms"}
		waitView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.wait", Kind: fe.ViewKindContainer, Rows: 3, Width: 60,
				Children: []fe.ViewNode{
					{
						ID: "omp.job.tiles", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
						Children: []fe.ViewNode{
							{
								ID: "omp.job.tile#0", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
								Children: []fe.ViewNode{
									{
										ID: "omp.job.tile.head#0", Kind: fe.ViewKindHStack, Rows: 3, Width: 60,
										Children: []fe.ViewNode{
											{ID: "omp.job.tile.dot#0", Kind: fe.ViewKindText, Rows: 1, Width: 4, Text: "✓"},
											{ID: "omp.job.tile.title#0", Kind: fe.ViewKindText, Rows: 1, Width: 20, Text: "ProofChild"},
											{ID: "omp.run.time#0", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "37ms"},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "wt10", Index: 0,
			Node: fe.ToolCard{Name: "wait", Status: fe.ToolDone, Result: lines, ResultView: waitView},
		})
		wt := node(ops, "n.wt10")
		wantTitle := []any{
			map[string]any{"t": "1 job settled", "s": "toolTitle strong"},
			map[string]any{"t": " 1 done", "s": "success"},
		}
		if wt == nil || props(wt)["frame"] != "card" || props(wt)["tone"] != "success" ||
			props(wt)["preview"] != "children" || !reflect.DeepEqual(props(wt)["title"], wantTitle) {
			t.Fatalf("wait tool = %#v", wt)
		}
		tile := wt["c"].([]any)[0].(map[string]any)["c"].([]any)[0].(map[string]any)
		if props(tile)["role"] != "omp.job.tile" || props(tile)["tone"] != "success" {
			t.Fatalf("wait tile = %#v, want tone=success", tile)
		}
		dot := tile["c"].([]any)[0].(map[string]any)["c"].([]any)[0].(map[string]any)
		if !reflect.DeepEqual(props(dot)["spans"], []any{map[string]any{"t": "✓", "s": "success"}}) {
			t.Fatalf("wait tile dot = %#v", dot)
		}
	})

	t.Run("item2_web_search_error_tone_and_card_frame", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//   tool[omp.tool.web_search]{target:"oh-my-pig proof offline query", targetKind:"query", status:"done", frame:"card", tone:"error"}
		//     text[omp.tool.error]{wrap:"word", spans:[{t:"No web search backend...", s:"error"}]}
		h := readyOmpRoles(t)
		msg := "No web search backend is available on the active model's provider under --offline."
		wsView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.web_search", Kind: fe.ViewKindContainer, Rows: 1, Width: 80,
				Children: []fe.ViewNode{
					{ID: "omp.tool.error", Kind: fe.ViewKindText, Rows: 1, Width: 80, Text: msg},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "ws10", Index: 0,
			Node: fe.ToolCard{
				Name:       "web_search",
				Arguments:  map[string]any{"query": "oh-my-pig proof offline query"},
				Status:     fe.ToolDone, // soft error in details.error
				Result:     []string{msg},
				ResultView: wsView,
			},
		})
		ws := node(ops, "n.ws10")
		if ws == nil || props(ws)["frame"] != "card" || props(ws)["tone"] != "error" ||
			props(ws)["target"] != "oh-my-pig proof offline query" || props(ws)["targetKind"] != "query" {
			t.Fatalf("web_search tool = %#v", ws)
		}
	})

	t.Run("item3_write_code_body_beats_host_diff", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//   tool[omp.tool.write]{target:"generated.txt", meta:["3 lines"], frame:"card", preview:{lines:8}}
		//     code{path:"generated.txt", numbers:true, wrap:true, text:"needle_alpha\nneedle_beta"}
		h := readyOmpRoles(t)
		writeView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.write", Kind: fe.ViewKindContainer, Rows: 2, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.tool.write.source", Kind: fe.ViewKindLines, Rows: 2, Width: 60, Lines: []string{"needle_alpha", "needle_beta"}},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "wr10", Index: 0,
			Node: fe.ToolCard{
				Name:       "write",
				Arguments:  map[string]any{"path": "generated.txt", "content": "needle_alpha\nneedle_beta\n"},
				Status:     fe.ToolDone,
				Diff:       &fe.Diff{Path: "generated.txt", Text: "@@ -0,0 +1,2 @@\n+needle_alpha\n+needle_beta\n"},
				Result:     []string{"needle_alpha", "needle_beta"},
				ResultView: writeView,
			},
		})
		wr := node(ops, "n.wr10")
		if wr == nil || !reflect.DeepEqual(props(wr)["meta"], []any{"3 lines"}) ||
			!reflect.DeepEqual(props(wr)["preview"], map[string]any{"lines": float64(8)}) {
			t.Fatalf("write tool = %#v", wr)
		}
		child := wr["c"].([]any)[0].(map[string]any)
		if child["k"] != "code" || props(child)["role"] != nil || props(child)["path"] != "generated.txt" ||
			props(child)["numbers"] != true || props(child)["wrap"] != true {
			t.Fatalf("write body child = %#v, want bare code node", child)
		}
	})

	t.Run("item3_grep_code_badge_and_head_meta", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//   tool[omp.tool.grep]{target:"needle_", meta:["in .", "2 matches · 1 file"], frame:"inline", preview:{lines:12}}
		//     col[omp.tool.search.file](row[omp.tool.file](icon, text, badge{text:"2"}), code{numbers:true, path:"generated.txt"})
		h := readyOmpRoles(t)
		grepView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.grep", Kind: fe.ViewKindContainer, Rows: 3, Width: 60,
				Children: []fe.ViewNode{
					{
						ID: "omp.tool.search.file#0", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
						Children: []fe.ViewNode{
							{ID: "omp.tool.file#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "generated.txt"},
							{ID: "omp.tool.search.lines#0", Kind: fe.ViewKindLines, Rows: 2, Width: 60, Lines: []string{"needle_alpha", "needle_beta"}},
						},
					},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "gr10", Index: 0,
			Node: fe.ToolCard{
				Name:       "grep",
				Arguments:  map[string]any{"pattern": "needle_"},
				Status:     fe.ToolDone,
				Result:     []string{"generated.txt", "needle_alpha", "needle_beta"},
				ResultView: grepView,
			},
		})
		gr := node(ops, "n.gr10")
		if gr == nil || !reflect.DeepEqual(props(gr)["meta"], []any{"in .", "2 matches · 1 file"}) ||
			!reflect.DeepEqual(props(gr)["preview"], map[string]any{"lines": float64(12)}) {
			t.Fatalf("grep tool = %#v", gr)
		}
		sf := gr["c"].([]any)[0].(map[string]any)
		fileRow := sf["c"].([]any)[0].(map[string]any)
		codeNode := sf["c"].([]any)[1].(map[string]any)
		if codeNode["k"] != "code" || props(codeNode)["role"] != nil || props(codeNode)["path"] != "generated.txt" || props(codeNode)["numbers"] != true {
			t.Fatalf("grep code node = %#v", codeNode)
		}
		frKids := fileRow["c"].([]any)
		if len(frKids) != 3 || frKids[2].(map[string]any)["k"] != "badge" || props(frKids[2].(map[string]any))["text"] != "2" {
			t.Fatalf("grep file row children = %#v, want trailing match badge '2'", frKids)
		}
	})

	t.Run("item3_glob_head_meta_lsp_bare_ansi_and_ast_edit_preview", func(t *testing.T) {
		h := readyOmpRoles(t)
		// Glob: meta ["2 files", "in ."], preview {lines: 8}.
		globView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.glob", Kind: fe.ViewKindContainer, Rows: 2, Width: 60,
				Children: []fe.ViewNode{
					{
						ID: "omp.tool.files", Kind: fe.ViewKindVStack, Rows: 2, Width: 60,
						Children: []fe.ViewNode{
							{ID: "omp.tool.file#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "generated.txt"},
							{ID: "omp.tool.file#1", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "notes.txt"},
						},
					},
				},
			},
		}
		// LSP: bare ansi (no role), preview {lines: 4}.
		lspView := &fe.View{
			Seq: 2,
			Root: fe.ViewNode{
				ID: "omp.tool.lsp", Kind: fe.ViewKindContainer, Rows: 1, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.tool.lsp.body", Kind: fe.ViewKindLines, Rows: 1, Width: 60, Lines: []string{"No language servers configured for this project"}},
				},
			},
		}
		// AST Edit preview: target "1 file", badge proposed(warning), meta ["1 replacement"], bare muted text body.
		astView := &fe.View{
			Seq: 3,
			Root: fe.ViewNode{
				ID: "omp.tool.ast_edit", Kind: fe.ViewKindContainer, Rows: 4, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.tool.context", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "console.log($A)"},
					{ID: "omp.tool.output", Kind: fe.ViewKindLines, Rows: 2, Width: 60, Lines: []string{`-1│console.log("hi")`, `+1│print("hi")`}},
					{ID: "omp.tool.stats", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "in sample.js · searched 1 file"},
				},
			},
		}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "gl10", Index: 0, Node: fe.ToolCard{
				Name: "glob", Arguments: map[string]any{"path": "*.txt"}, Status: fe.ToolDone,
				Result: []string{"generated.txt", "notes.txt"}, ResultView: globView,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "lsp10", Index: 1, Node: fe.ToolCard{
				Name: "lsp", Arguments: map[string]any{"action": "status"}, Status: fe.ToolDone,
				Result: []string{"No language servers configured for this project"}, ResultView: lspView,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ae10", Index: 2, Node: fe.ToolCard{
				Name: "ast_edit", Arguments: map[string]any{"ops": []any{map[string]any{"pat": "console.log($A)", "out": "print($A)"}}, "path": "sample.js"}, Status: fe.ToolDone,
				Result: []string{"console.log($A)", `-1│console.log("hi")`, `+1│print("hi")`, "in sample.js · searched 1 file"}, ResultView: astView,
			}},
		)
		gl := node(ops, "n.gl10")
		if gl == nil || !reflect.DeepEqual(props(gl)["meta"], []any{"2 files", "in ."}) ||
			!reflect.DeepEqual(props(gl)["preview"], map[string]any{"lines": float64(8)}) {
			t.Fatalf("glob tool = %#v", gl)
		}
		lsp := node(ops, "n.lsp10")
		lspChild := lsp["c"].([]any)[0].(map[string]any)
		if lspChild["k"] != "ansi" || props(lspChild)["role"] != nil ||
			!reflect.DeepEqual(props(lsp)["preview"], map[string]any{"lines": float64(4)}) {
			t.Fatalf("lsp tool = %#v", lsp)
		}
		ae := node(ops, "n.ae10")
		if ae == nil || props(ae)["target"] != "1 file" || props(ae)["targetKind"] != "text" ||
			!reflect.DeepEqual(props(ae)["meta"], []any{"1 replacement"}) ||
			!reflect.DeepEqual(props(ae)["badges"], []any{map[string]any{"text": "proposed", "tone": "warning"}}) ||
			!reflect.DeepEqual(props(ae)["preview"], map[string]any{"lines": float64(6)}) {
			t.Fatalf("ast_edit tool = %#v", ae)
		}
		aeBody := ae["c"].([]any)[1].(map[string]any)
		if aeBody["k"] != "text" || props(aeBody)["role"] != nil {
			t.Fatalf("ast_edit body = %#v, want bare muted text node", aeBody)
		}
	})

	t.Run("item3_explicit_omp_head_overrides_and_stripped_from_body", func(t *testing.T) {
		h := readyOmpRoles(t)
		v := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.grep", Kind: fe.ViewKindContainer, Rows: 4, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.head.meta#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "in src"},
					{ID: "omp.head.meta#1", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "15 matches · 4 files"},
					{ID: "omp.head.badge#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "truncated:warning"},
					{ID: "omp.tool.stats", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "2 more files"},
				},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "hd10", Index: 0,
			Node: fe.ToolCard{
				Name: "grep", Arguments: map[string]any{"pattern": "foo"}, Status: fe.ToolDone,
				Result: []string{"in src", "15 matches · 4 files", "truncated:warning", "2 more files"}, ResultView: v,
			},
		})
		hd := node(ops, "n.hd10")
		if hd == nil || !reflect.DeepEqual(props(hd)["meta"], []any{"in src", "15 matches · 4 files"}) ||
			!reflect.DeepEqual(props(hd)["badges"], []any{map[string]any{"text": "truncated", "tone": "warning"}}) {
			t.Fatalf("explicit omp.head.* tool = %#v", hd)
		}
		if kids := hd["c"].([]any); len(kids) != 1 || props(kids[0].(map[string]any))["role"] != "omp.tool.stats" {
			t.Fatalf("omp.head.* nodes leaked into body: %#v", kids)
		}
	})

	t.Run("item4_provider_error_code_badge_and_actions", func(t *testing.T) {
		// Expected from omp v19.1.1 Tern capture:
		//   card[omp.error]{tone:"error", head:...}
		//     row(text{"Request failed"}, badge[omp.error.code]{text:"400", tone:"error"})
		//     text[omp.error.message]{spans:[{t:"Proof simulated provider error (type=invalid_request_error)", s:"mono"}]}
		//     row[omp.error.actions](row[omp.error.action] "Copy error", row[omp.error.action] "Switch model" + kbd["⌥P"])
		h := readyOmpRoles(t)
		h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}})
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "n90.e", Index: 0,
			Node: fe.Lines{Lines: []string{`Error: 400: {"message":"Proof simulated provider error","type":"invalid_request_error"}`}},
		})
		wrap := node(ops, "n.n90.e")
		if wrap == nil || wrap["k"] != "col" || props(wrap)["role"] != "omp.assistant" {
			t.Fatalf("error assistant wrapper = %#v", wrap)
		}
		card := node(ops, "n.n90.e.error")
		if card == nil || card["k"] != "card" || props(card)["role"] != "omp.error" || props(card)["head"] != "n.n90.e.error.head" {
			t.Fatalf("error card = %#v", card)
		}
		codeBadge := node(ops, "n.n90.e.error.head.code")
		if codeBadge == nil || codeBadge["k"] != "badge" || props(codeBadge)["role"] != "omp.error.code" || props(codeBadge)["text"] != "400" {
			t.Fatalf("error code badge = %#v, want '400'", codeBadge)
		}
		msg := node(ops, "n.n90.e.error.message")
		wantMsg := "Proof simulated provider error (type=invalid_request_error)"
		if msg == nil || props(msg)["spans"].([]any)[0].(map[string]any)["t"] != wantMsg {
			t.Fatalf("error message = %#v, want %q", msg, wantMsg)
		}
		actions := node(ops, "n.n90.e.error.actions")
		if actions == nil || props(actions)["role"] != "omp.error.actions" || len(actions["c"].([]any)) != 2 {
			t.Fatalf("error actions = %#v, want 2 actions (Copy error, Switch model)", actions)
		}
		// Clicking Switch model sends /model via Env.Send.
		h.session.transcriptClick(&tern.Action{EventBase: tern.EventBase{SF: surfaceID}, ID: "n.n90.e.error.actions.switch-model", Act: "switch-model"})
		if !reflect.DeepEqual(h.sends, []string{"/model"}) {
			t.Fatalf("switch-model sends = %v, want [/model]", h.sends)
		}
		// Clicking Copy error writes OSC 52 to terminal output.
		h.out.take()
		h.session.transcriptClick(&tern.Action{EventBase: tern.EventBase{SF: surfaceID}, ID: "n.n90.e.error.actions.copy-error", Act: "copy-error"})
		if raw := h.out.take(); !strings.Contains(raw, "\x1b]52;c;") {
			t.Fatalf("copy-error output = %q, want OSC 52 sequence", raw)
		}
	})
}

// TestOmpDrawRound3ParityAgainstProof10 verifies every round-3 Tern sub-diff fix
// against exact node props captured from real omp v19.1.1 (Tern proof capture):
//  1. file:/// URIs on read/edit/write heads, and NO href on failed read.
//  2. write code body preserves trailing newline from args.content.
//  3. grep `omp.tool.search.meta` populates `start`/`nums` and `marks` on `code`
//     and counts marked matches for the per-file badge without leaking meta text.
//  4. eval tool sets `intent` from `args.title` (or `"running <lang>"`).
//  5. composer sets `maxLines:18`, tooltip key hints (`⌥P`, `⇧⇥`, `⏎`),
//     `usage` click action `status.cost` → `/usage`, and maps `PLAN`/`GOAL`
//     extension statuses to omp's `mode` seg with `icon:"plan"/"goal"`,
//     `accent`/`warning` span, and `priority:8`.
func TestOmpDrawRound3ParityAgainstProof10(t *testing.T) {
	t.Run("file_href_uri_and_no_href_on_read_error", func(t *testing.T) {
		h := readyOmpRoles(t)
		h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "/tmp/proof/work"}))
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r_ok", Index: 0, Node: fe.ToolCard{
				Name: "read", Arguments: map[string]any{"path": "notes.txt"}, Status: fe.ToolDone,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r_err", Index: 1, Node: fe.ToolCard{
				Name: "read", Arguments: map[string]any{"path": "does-not-exist-proof.txt"},
				Status: fe.ToolError, Output: "Path 'does-not-exist-proof.txt' not found",
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "w_ok", Index: 2, Node: fe.ToolCard{
				Name: "write", Arguments: map[string]any{"path": "generated.txt", "content": "needle_alpha\nneedle_beta\n"},
				Status: fe.ToolDone,
				Result: []string{"needle_alpha", "needle_beta"},
				ResultView: &fe.View{Seq: 1, Root: fe.ViewNode{
					ID: "omp.tool.write", Kind: fe.ViewKindContainer, Rows: 2, Width: 60,
					Children: []fe.ViewNode{
						{ID: "omp.tool.write.source", Kind: fe.ViewKindLines, Rows: 2, Width: 60, Lines: []string{"needle_alpha", "needle_beta"}},
					},
				}},
			}},
		)
		if got := props(node(ops, "n.r_ok"))["href"]; got != "file:///tmp/proof/work/notes.txt" {
			t.Fatalf("read ok href = %#v, want file:///tmp/proof/work/notes.txt", got)
		}
		if got, has := props(node(ops, "n.r_err"))["href"]; has {
			t.Fatalf("failed read must omit href, got %#v", got)
		}
		wNode := node(ops, "n.w_ok")
		if got := props(wNode)["href"]; got != "file:///tmp/proof/work/generated.txt" {
			t.Fatalf("write href = %#v, want file:///tmp/proof/work/generated.txt", got)
		}
		wCode := wNode["c"].([]any)[0].(map[string]any)
		if got := props(wCode)["text"]; got != "needle_alpha\nneedle_beta\n" {
			t.Fatalf("write code text = %q, want trailing newline preserved", got)
		}
	})

	t.Run("grep_search_meta_start_nums_and_marks", func(t *testing.T) {
		h := readyOmpRoles(t)
		// Contiguous match lines *1,*2 -> start:1, marks:[{line:1,tone:accent},{line:2,tone:accent}], badge:"2".
		vContig := &fe.View{Seq: 1, Root: fe.ViewNode{
			ID: "omp.tool.grep", Kind: fe.ViewKindContainer, Rows: 3, Width: 60,
			Children: []fe.ViewNode{{
				ID: "omp.tool.search.file#0", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.tool.file#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "generated.txt"},
					{ID: "omp.tool.search.meta#0", Kind: fe.ViewKindText, Rows: 0, Width: 0, Text: "*1,*2"},
					{ID: "omp.tool.search.lines#0", Kind: fe.ViewKindLines, Rows: 2, Width: 60, Lines: []string{"needle_alpha", "needle_beta"}},
				},
			}},
		}}
		// Non-contiguous lines 10,*11,15 -> nums:[10,11,15], marks:[{line:11,tone:accent}], badge:"1".
		vSparse := &fe.View{Seq: 2, Root: fe.ViewNode{
			ID: "omp.tool.grep", Kind: fe.ViewKindContainer, Rows: 4, Width: 60,
			Children: []fe.ViewNode{{
				ID: "omp.tool.search.file#0", Kind: fe.ViewKindVStack, Rows: 4, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.tool.file#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "sparse.txt"},
					{ID: "omp.tool.search.meta#0", Kind: fe.ViewKindText, Rows: 0, Width: 0, Text: "10,*11,15"},
					{ID: "omp.tool.search.lines#0", Kind: fe.ViewKindLines, Rows: 3, Width: 60, Lines: []string{"ctx", "hit", "ctx2"}},
				},
			}},
		}}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "g_contig", Index: 0, Node: fe.ToolCard{
				Name: "grep", Arguments: map[string]any{"pattern": "needle_"}, Status: fe.ToolDone,
				Result: []string{"generated.txt", "needle_alpha", "needle_beta"}, ResultView: vContig,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "g_sparse", Index: 1, Node: fe.ToolCard{
				Name: "grep", Arguments: map[string]any{"pattern": "hit"}, Status: fe.ToolDone,
				Result: []string{"sparse.txt", "ctx", "hit", "ctx2"}, ResultView: vSparse,
			}},
		)
		sf1 := node(ops, "n.g_contig")["c"].([]any)[0].(map[string]any)
		if len(sf1["c"].([]any)) != 2 {
			t.Fatalf("search.meta leaked into children: %#v", sf1["c"])
		}
		code1 := sf1["c"].([]any)[1].(map[string]any)
		wantMarks1 := []any{map[string]any{"line": float64(1), "tone": "accent"}, map[string]any{"line": float64(2), "tone": "accent"}}
		if props(code1)["start"] != float64(1) || props(code1)["nums"] != nil || !reflect.DeepEqual(props(code1)["marks"], wantMarks1) {
			t.Fatalf("contiguous grep code = %#v", code1)
		}
		sf2 := node(ops, "n.g_sparse")["c"].([]any)[0].(map[string]any)
		fileRow2 := sf2["c"].([]any)[0].(map[string]any)
		badge2 := fileRow2["c"].([]any)[2].(map[string]any)
		if props(badge2)["text"] != "1" || props(badge2)["title"] != "1 match" {
			t.Fatalf("sparse grep badge = %#v, want 1 match (context lines excluded)", badge2)
		}
		code2 := sf2["c"].([]any)[1].(map[string]any)
		wantMarks2 := []any{map[string]any{"line": float64(11), "tone": "accent"}}
		if props(code2)["start"] != nil || !reflect.DeepEqual(props(code2)["nums"], []any{float64(10), float64(11), float64(15)}) || !reflect.DeepEqual(props(code2)["marks"], wantMarks2) {
			t.Fatalf("sparse grep code = %#v", code2)
		}
	})

	t.Run("eval_intent_from_title_or_language", func(t *testing.T) {
		h := readyOmpRoles(t)
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ev1", Index: 0, Node: fe.ToolCard{
				Name: "eval", Arguments: map[string]any{"language": "py", "title": "Proof Python eval", "code": "print(1)"}, Status: fe.ToolDone,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ev2", Index: 1, Node: fe.ToolCard{
				Name: "eval", Arguments: map[string]any{"language": "py", "code": "print(2)"}, Status: fe.ToolDone,
			}},
		)
		if got := props(node(ops, "n.ev1"))["intent"]; got != "Proof Python eval" {
			t.Fatalf("eval title intent = %#v, want 'Proof Python eval'", got)
		}
		if got := props(node(ops, "n.ev2"))["intent"]; got != "running python" {
			t.Fatalf("eval fallback intent = %#v, want 'running python'", got)
		}
	})

	t.Run("composer_plan_mode_icon_maxlines_tooltips_and_usage_click", func(t *testing.T) {
		h := readyOmpRoles(t)
		ft := sampleFooter()
		ft.ExtensionStatuses = []string{"PLAN"}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: ft},
		)
		inp := node(ops, "dock.editor.editor.line.input")
		if props(inp)["maxLines"] != float64(18) {
			t.Fatalf("editor maxLines = %#v, want 18", props(inp)["maxLines"])
		}
		modeSeg := node(ops, "dock.editor.editor.bar.extras.mode")
		if modeSeg == nil || props(modeSeg)["role"] != "omp.composer.fact" || props(modeSeg)["icon"] != "plan" ||
			props(modeSeg)["priority"] != float64(8) ||
			!reflect.DeepEqual(props(modeSeg)["spans"], []any{map[string]any{"t": "Plan", "s": "accent"}}) {
			t.Fatalf("plan mode seg = %#v", modeSeg)
		}
		if got := props(node(ops, "dock.editor.editor.bar.model"))["title"]; got != "Switch model  ⌥P" {
			t.Fatalf("model title = %#v", got)
		}
		if got := props(node(ops, "dock.editor.editor.bar.send"))["title"]; got != "Send  ⏎" {
			t.Fatalf("send title = %#v", got)
		}
		usage := node(ops, "dock.editor.editor.bar.usage")
		if !reflect.DeepEqual(props(usage)["actions"], map[string]any{"click": "status.cost"}) {
			t.Fatalf("usage actions = %#v, want click:status.cost", props(usage)["actions"])
		}
		h.event(`{"ev":"action","sf":"pig","id":"dock.editor.editor.bar.usage","act":"status.cost"}`)
		h.synced()
		if !reflect.DeepEqual(h.sends, []string{"/usage"}) {
			t.Fatalf("usage click sends = %v, want [/usage]", h.sends)
		}
	})
}

// TestOmpDrawRound4ParityAgainstProof12 asserts the round-4 fixes against exact
// TSP nodes captured from omp v19.1.1 (Tern proof capture):
//   - wait tile head clock is a bare `elapsed` (no role)
//   - glob file rows and chips carry `href: "file://<WORK>/<name>"` on the name span
//   - grep file rows do NOT carry `href`
//   - provider error card omits `lines: 8` once Ctrl+O expansion is active
func TestOmpDrawRound4ParityAgainstProof12(t *testing.T) {
	t.Run("wait_tile_head_elapsed_is_bare_no_role", func(t *testing.T) {
		h := readyOmpRoles(t)
		waitView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.wait", Kind: fe.ViewKindContainer, Rows: 1, Width: 60,
				Children: []fe.ViewNode{{
					ID: "omp.job.tiles", Kind: fe.ViewKindVStack, Rows: 1, Width: 60,
					Children: []fe.ViewNode{{
						ID: "omp.job.tile#0", Kind: fe.ViewKindVStack, Rows: 1, Width: 60,
						Children: []fe.ViewNode{{
							ID: "omp.job.tile.head#0", Kind: fe.ViewKindHStack, Rows: 1, Width: 60,
							Children: []fe.ViewNode{
								{ID: "omp.job.tile.dot#0", Kind: fe.ViewKindText, Rows: 1, Width: 2, Text: "✓"},
								{ID: "omp.job.tile.title#0", Kind: fe.ViewKindText, Rows: 1, Width: 20, Text: "ProofChild"},
								{ID: "omp.run.time#0", Kind: fe.ViewKindText, Rows: 1, Width: 10, Text: "40ms"},
							},
						}},
					}},
				}},
			},
		}
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "wt12", Index: 0,
			Node: fe.ToolCard{Name: "wait", Status: fe.ToolDone, Result: []string{"✓ ProofChild 40ms"}, ResultView: waitView},
		})
		wt := node(ops, "n.wt12")
		head := wt["c"].([]any)[0].(map[string]any)["c"].([]any)[0].(map[string]any)["c"].([]any)[0].(map[string]any)
		clock := head["c"].([]any)[2].(map[string]any)
		if clock["k"] != "elapsed" || props(clock)["role"] != nil || props(clock)["format"] != "short" ||
			props(clock)["age"] != float64(40) || props(clock)["stopped"] != float64(40) {
			t.Fatalf("wait tile head clock = %#v, want bare elapsed without role", clock)
		}
	})

	t.Run("glob_file_rows_and_chips_have_file_href_grep_does_not", func(t *testing.T) {
		h := readyOmpRoles(t)
		h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "/tmp/proof/work"}))
		globView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "omp.tool.glob", Kind: fe.ViewKindContainer, Rows: 3, Width: 60,
				Children: []fe.ViewNode{{
					ID: "omp.tool.files", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
					Children: []fe.ViewNode{
						{ID: "omp.tool.file#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "generated.txt"},
						{ID: "omp.tool.file#1", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "sub/notes.txt"},
						{ID: "omp.tool.file#2", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "dir/"},
					},
				}},
			},
		}
		grepView := &fe.View{
			Seq: 2,
			Root: fe.ViewNode{
				ID: "omp.tool.grep", Kind: fe.ViewKindContainer, Rows: 2, Width: 60,
				Children: []fe.ViewNode{{
					ID: "omp.tool.search.file#0", Kind: fe.ViewKindVStack, Rows: 2, Width: 60,
					Children: []fe.ViewNode{
						{ID: "omp.tool.file#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "generated.txt"},
						{ID: "omp.tool.search.lines#0", Kind: fe.ViewKindLines, Rows: 1, Width: 60, Lines: []string{"needle"}},
					},
				}},
			},
		}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "gl12", Index: 0, Node: fe.ToolCard{
				Name: "glob", Arguments: map[string]any{"path": "*.txt"}, Status: fe.ToolDone,
				Result: []string{"generated.txt", "sub/notes.txt", "dir/"}, ResultView: globView,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "gr12", Index: 1, Node: fe.ToolCard{
				Name: "grep", Arguments: map[string]any{"pattern": "needle"}, Status: fe.ToolDone,
				Result: []string{"generated.txt", "needle"}, ResultView: grepView,
			}},
		)
		files := node(ops, "n.gl12")["c"].([]any)[0].(map[string]any)["c"].([]any)
		spans0 := props(files[0].(map[string]any)["c"].([]any)[1].(map[string]any))["spans"]
		want0 := []any{map[string]any{"t": "generated.txt", "s": "strong", "href": "file:///tmp/proof/work/generated.txt"}}
		if !reflect.DeepEqual(spans0, want0) {
			t.Fatalf("glob file#0 spans = %#v, want %#v", spans0, want0)
		}
		spans1 := props(files[1].(map[string]any)["c"].([]any)[1].(map[string]any))["spans"]
		want1 := []any{
			map[string]any{"t": "sub/", "s": "dim"},
			map[string]any{"t": "notes.txt", "s": "strong", "href": "file:///tmp/proof/work/sub/notes.txt"},
		}
		if !reflect.DeepEqual(spans1, want1) {
			t.Fatalf("glob file#1 spans = %#v, want %#v", spans1, want1)
		}
		spans2 := props(files[2].(map[string]any)["c"].([]any)[1].(map[string]any))["spans"]
		want2 := []any{map[string]any{"t": "dir/", "s": "strong"}}
		if !reflect.DeepEqual(spans2, want2) {
			t.Fatalf("glob dir spans = %#v, want %#v (dirs omit href)", spans2, want2)
		}
		grepFileSpans := props(node(ops, "n.gr12")["c"].([]any)[0].(map[string]any)["c"].([]any)[0].(map[string]any)["c"].([]any)[1].(map[string]any))["spans"]
		wantGrep := []any{map[string]any{"t": "generated.txt", "s": "strong"}}
		if !reflect.DeepEqual(grepFileSpans, wantGrep) {
			t.Fatalf("grep file spans = %#v, want %#v (no href)", grepFileSpans, wantGrep)
		}
	})

	t.Run("error_card_lines_clamped_when_collapsed_omitted_when_expanded", func(t *testing.T) {
		h := readyOmpRoles(t)
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "e1.e", Index: 0,
			Node: fe.Lines{Lines: []string{"Error: 400: bad request"}},
		})
		if got := props(node(ops, "n.e1.e.error.message"))["lines"]; got != float64(8) {
			t.Fatalf("collapsed error message lines = %#v, want 8", got)
		}
		h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "t1", Index: 1,
			Node: fe.ToolCard{Name: "bash", Status: fe.ToolDone, Expanded: true},
		})
		ops = h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "e2.e", Index: 2,
			Node: fe.Lines{Lines: []string{"Error: 400: bad request"}},
		})
		if got, has := props(node(ops, "n.e2.e.error.message"))["lines"]; has {
			t.Fatalf("expanded error message should omit lines, got %#v", got)
		}
	})
}

// TestOmpFinalCodeAndAllowlistFixes covers:
//   - Final Code Review Finding 11: `omp.head.*` stripped and consumed across
//     themed views, ask answer views, nested containers, unknown tools, and
//     line fallback (validView==false + read error fallback).
//   - Final Code Review Finding 12: rune-safe read-error gist truncation and
//     rune-safe capitalization of multi-byte tool/action names.
//   - Allowlist Audit Finding 6: JSON provider error formatting (`<message> (type=… param=… code=…)`)
//     including wrapped multi-line JSON and nested `{"error":{...}}` envelopes.
//   - Allowlist Audit Finding 20: `Options.ModelDisplayName` resolves model IDs
//     to human display names in the composer chip.
//   - Allowlist Audit Finding 21: `read` cards omit `took` and `age`; working row
//     `elapsed` always carries `age` (even when 0).
func TestOmpFinalCodeAndAllowlistFixes(t *testing.T) {
	t.Run("finding11_strip_omp_head_on_every_view_path", func(t *testing.T) {
		h := readyOmpRoles(t)

		nestedView := &fe.View{
			Seq: 1,
			Root: fe.ViewNode{
				ID: "custom.root", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
				Children: []fe.ViewNode{
					{
						ID: "omp.custom.box", Kind: fe.ViewKindVStack, Rows: 2, Width: 60,
						Children: []fe.ViewNode{
							{ID: "omp.head.tone", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "warning"},
							{ID: "omp.head.badge#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "truncated:warning"},
						},
					},
					{ID: "omp.tool.stats", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "kept body"},
				},
			},
		}
		themedView := &fe.View{
			Seq:   2,
			Theme: map[string]string{"accent": "#ff00aa"},
			Root: fe.ViewNode{
				ID: "omp.tool.custom_themed", Kind: fe.ViewKindVStack, Rows: 2, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.head.frame", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "inline"},
					{ID: "omp.tool.stats", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "themed body"},
				},
			},
		}
		askView := &fe.View{
			Seq: 3,
			Root: fe.ViewNode{
				ID: "omp.tool.ask", Kind: fe.ViewKindContainer, Rows: 3, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.head.badge#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "answered:success"},
					{
						ID: "omp.ask.card.body", Kind: fe.ViewKindVStack, Rows: 2, Width: 60,
						Children: []fe.ViewNode{{
							ID: "omp.ask.fold#0", Kind: fe.ViewKindVStack, Rows: 2, Width: 60,
							Children: []fe.ViewNode{
								{ID: "omp.ask.fold.question#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "Pick one?"},
								{ID: "omp.ask.fold.answer#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "Yes"},
							},
						}},
					},
				},
			},
		}
		mismatchView := &fe.View{
			Seq: 4,
			Root: fe.ViewNode{
				ID: "omp.tool.grep", Kind: fe.ViewKindVStack, Rows: 99, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.head.frame", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "card"},
					{ID: "omp.head.tone", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "warning"},
					{ID: "omp.tool.stats", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "actual output line"},
				},
			},
		}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "f11_nested", Index: 0, Node: fe.ToolCard{
				Name: "mystery", Status: fe.ToolDone, Result: []string{"warning", "truncated:warning", "kept body"}, ResultView: nestedView,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "f11_themed", Index: 1, Node: fe.ToolCard{
				Name: "custom_themed", Status: fe.ToolDone, Result: []string{"inline", "themed body"}, ResultView: themedView,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "f11_ask", Index: 2, Node: fe.ToolCard{
				Name: "ask", Status: fe.ToolDone, Result: []string{"answered:success", "Pick one?", "Yes"}, ResultView: askView,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "f11_fallback", Index: 3, Node: fe.ToolCard{
				Name: "grep", Status: fe.ToolDone, Result: []string{"card", "warning", "actual output line"}, ResultView: mismatchView,
			}},
		)

		hasText := func(n map[string]any, needle string) bool {
			raw := fmt.Sprintf("%#v", n["c"])
			return strings.Contains(raw, needle)
		}

		nNested := node(ops, "n.f11_nested")
		if props(nNested)["tone"] != "warning" || hasText(nNested, "truncated:warning") {
			t.Fatalf("nested omp.head.* not consumed/stripped: %#v", nNested)
		}
		nThemed := node(ops, "n.f11_themed")
		if props(nThemed)["frame"] != "inline" || hasText(nThemed, `"inline"`) {
			t.Fatalf("themed omp.head.* not consumed/stripped: %#v", nThemed)
		}
		nAsk := node(ops, "n.f11_ask")
		if !reflect.DeepEqual(props(nAsk)["badges"], []any{map[string]any{"text": "answered", "tone": "success"}}) || hasText(nAsk, "answered:success") {
			t.Fatalf("ask omp.head.* not consumed/stripped: %#v", nAsk)
		}
		nFall := node(ops, "n.f11_fallback")
		if props(nFall)["frame"] != "card" || props(nFall)["tone"] != "warning" {
			t.Fatalf("fallback omp.head.* not extracted to head: %#v", nFall)
		}
		outNode := node(ops, "n.f11_fallback.out")
		if outNode == nil || props(outNode)["text"] != "actual output line" {
			t.Fatalf("fallback body leaked omp.head.* rows: %#v", outNode)
		}
	})

	t.Run("finding12_rune_safe_gist_truncation_and_capitalization", func(t *testing.T) {
		h := readyOmpRoles(t)
		longErr := "Path '" + strings.Repeat("é", 70) + strings.Repeat("日", 70) + "' not found"
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r_utf8", Index: 0, Node: fe.ToolCard{
				Name: "read", Arguments: map[string]any{"path": "日本語.txt"},
				Status: fe.ToolError, Output: longErr,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t_utf8", Index: 1, Node: fe.ToolCard{
				Name: "über_tool", Status: fe.ToolDone,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "lsp_utf8", Index: 2, Node: fe.ToolCard{
				Name: "lsp", Arguments: map[string]any{"action": "über_action"}, Status: fe.ToolDone,
			}},
		)
		rNode := node(ops, "n.r_utf8")
		meta := props(rNode)["meta"].([]any)
		gistSpan := meta[0].([]any)[0].(map[string]any)["t"].(string)
		if !strings.HasSuffix(gistSpan, "…") || !utf8.ValidString(gistSpan) {
			t.Fatalf("read error gist invalid UTF-8 or missing ellipsis: %q", gistSpan)
		}
		if rc := utf8.RuneCountInString(gistSpan); rc != 122 {
			t.Fatalf("read error gist rune count = %d, want 122: %q", rc, gistSpan)
		}
		if got := props(node(ops, "n.t_utf8"))["title"]; got != "Über_tool" {
			t.Fatalf("multi-byte tool title = %#v, want 'Über_tool'", got)
		}
		if got := props(node(ops, "n.lsp_utf8"))["title"]; got != "Über action" {
			t.Fatalf("multi-byte lsp action title = %#v, want 'Über action'", got)
		}
	})

	t.Run("finding6_provider_error_json_formatted_like_omp", func(t *testing.T) {
		h := readyOmpRoles(t)
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e_wrap.e", Index: 0, Node: fe.Lines{
				Lines: []string{
					`Error: 400: {"message":"Proof simulated provider`,
					`error","type":"invalid_request_error"}`,
				},
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e_nested.e", Index: 1, Node: fe.Lines{
				Lines: []string{`Error: 429 {"error":{"message":"Rate limit exceeded","type":"rate_limit_error","param":"model","code":"too_many_requests"}}`},
			}},
		)
		msg1 := props(node(ops, "n.e_wrap.e.error.message"))["spans"].([]any)[0].(map[string]any)["t"]
		if want := "Proof simulated provider error (type=invalid_request_error)"; msg1 != want {
			t.Fatalf("wrapped JSON error = %q, want %q", msg1, want)
		}
		msg2 := props(node(ops, "n.e_nested.e.error.message"))["spans"].([]any)[0].(map[string]any)["t"]
		if want := "Rate limit exceeded (type=rate_limit_error param=model code=too_many_requests)"; msg2 != want {
			t.Fatalf("nested JSON error = %q, want %q", msg2, want)
		}
	})

	t.Run("finding20_model_display_name_option", func(t *testing.T) {
		h := readyOmpRoles(t)
		h.session.modelDisplayName = func(id string) string {
			if id == "proof-1" {
				return "Proof 1"
			}
			return ""
		}
		ft := sampleFooter()
		ft.Model = "proof-1"
		ft.Provider = ""
		ft.Routed = fe.RoutedModel{}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: ft},
		)
		nameNode := node(ops, "dock.editor.editor.bar.model.name")
		want := []any{map[string]any{"t": "Proof 1", "s": "statusLineModel"}}
		if !reflect.DeepEqual(props(nameNode)["spans"], want) {
			t.Fatalf("model chip spans = %#v, want %#v", props(nameNode)["spans"], want)
		}
	})

	t.Run("finding21_read_omits_took_and_working_elapsed_always_has_age", func(t *testing.T) {
		h := readyOmpRoles(t)
		now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
		h.session.now = func() time.Time { return now }
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r_notook", Index: 0, Node: fe.ToolCard{
				Name: "read", Arguments: map[string]any{"path": "notes.txt"},
				Status: fe.ToolDone, Elapsed: 5 * time.Millisecond,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r_err_notook", Index: 1, Node: fe.ToolCard{
				Name: "read", Arguments: map[string]any{"path": "missing.txt"},
				Status: fe.ToolError, Output: "not found", Elapsed: 1 * time.Millisecond,
			}},
			workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Working…"}),
		)
		if _, has := props(node(ops, "n.r_notook"))["took"]; has {
			t.Fatalf("read card must omit took: %#v", node(ops, "n.r_notook"))
		}
		if _, has := props(node(ops, "n.r_err_notook"))["took"]; has {
			t.Fatalf("failing read card must omit took: %#v", node(ops, "n.r_err_notook"))
		}
		wRow := node(ops, "dock.activity.status.working")
		el := wRow["c"].([]any)[1].(map[string]any)
		if el["k"] != "elapsed" || props(el)["age"] != float64(0) || props(el)["format"] != "short" {
			t.Fatalf("working elapsed = %#v, want explicit age:0", el)
		}
	})
}

// TestOmpDockTodoHudAndErrorBannerParityAgainstProof13 verifies that above-editor
// `kit.View` widgets delivered in PiG's `Lines("dock").View` map to omp's exact
// Tern dock nodes, props, interactive handlers, and dock order:
//
//	row[omp.errorBanner](text, button[omp.btn] Details?, button[omp.btn] Dismiss)
//	row[omp.hud.activity](checklist[omp.hud.todo]{mode:"hud", phases})
//	col[omp.editor]
//
// Expected values taken from omp v19.1.1 Tern proof capture at steps
// `07-tool-todo`, `15-provider-error`, and `16-plan-mode`.
func TestOmpDockTodoHudAndErrorBannerParityAgainstProof13(t *testing.T) {
	h := readyOmpRoles(t)
	// Combined dockView as PiG's TuiSurface.dockEntries produces when both
	// `oh-my-pig:todo` and `omp-error-banner` widgets are mounted (intentionally
	// putting todo before errorBanner in PiG's widget list to prove frontend
	// reorders to omp's nativeDock order: errorBanner above activity/todo above editor).
	todoRoot := fe.ViewNode{
		ID: "omp.hud.todo", Kind: fe.ViewKindVStack, Rows: 5, Width: 60,
		Children: []fe.ViewNode{{
			ID: "omp.todo.phase#0", Kind: fe.ViewKindVStack, Rows: 5, Width: 60,
			Children: []fe.ViewNode{
				{ID: "omp.todo.phase.head#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "Verify"},
				{ID: "omp.todo.item#0.0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "☑ Check read card"},
				{ID: "omp.todo.item.status#0.0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "done"},
				{ID: "omp.todo.item#0.1", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "☑ Check todo card"},
				{ID: "omp.todo.item.status#0.1", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "done"},
			},
		}},
	}
	bannerMsg := "400 Proof simulated provider error (type=invalid_request_error)\nsecond detail line"
	bannerRoot := fe.ViewNode{
		ID: "omp.errorBanner", Kind: fe.ViewKindContainer, Rows: 2, Width: 60,
		Children: []fe.ViewNode{
			{ID: "omp.errorBanner.message", Kind: fe.ViewKindText, Rows: 2, Width: 60, Text: bannerMsg},
		},
	}
	dockLines := []string{"Verify", "☑ Check read card", "done", "☑ Check todo card", "done", "400 Proof simulated provider error (type=invalid_request_error)", "second detail line"}
	dockView := &fe.View{
		Seq: 1,
		Root: fe.ViewNode{
			Kind:     fe.ViewKindContainer,
			Rows:     len(dockLines),
			Width:    60,
			Children: []fe.ViewNode{todoRoot, bannerRoot},
		},
	}
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Index: 0, Node: fe.Lines{Lines: dockLines, View: dockView}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{Sendable: true}},
	)

	// Verify fixed nativeDock slot order on session.view().Dock:
	// errorBanner sits in slot 3, activity (todo) in slot 7, editor in slot 9.
	var slotKeys []string
	for _, dn := range h.session.view().Dock.(tern.Nodes) {
		k, _ := dn.Node().Props["key"].(string)
		slotKeys = append(slotKeys, k)
	}
	wantSlots := []string{
		"btw", "omfg", "cleanse", "errorBanner",
		"modelCycle", "deferred", "above", "activity",
		"pending", "editor", "below",
	}
	if !reflect.DeepEqual(slotKeys, wantSlots) {
		t.Fatalf("dock slot order = %v, want %v", slotKeys, wantSlots)
	}
	// Verify checklist[omp.hud.todo] inside row[omp.hud.activity] matches omp proof13 capture.
	act := node(ops, "dock.activity")
	if act == nil || act["k"] != "row" || props(act)["role"] != "omp.hud.activity" ||
		props(act)["align"] != "center" || props(act)["gap"] != "sm" {
		t.Fatalf("activity row = %#v", act)
	}
	todo := node(ops, "dock.activity.todo")
	wantPhases := []any{
		map[string]any{
			"id":        "0",
			"title":     "Verify",
			"collapsed": true,
			"items": []any{
				map[string]any{"id": "0.0", "text": "Check read card", "status": "done"},
				map[string]any{"id": "0.1", "text": "Check todo card", "status": "done"},
			},
		},
	}
	if todo == nil || todo["k"] != "checklist" || props(todo)["role"] != "omp.hud.todo" ||
		props(todo)["mode"] != "hud" || !reflect.DeepEqual(props(todo)["phases"], wantPhases) {
		t.Fatalf("todo hud checklist = %#v, want phases=%#v", todo, wantPhases)
	}

	// Verify row[omp.errorBanner] inside its anonymous col slot + Details + Dismiss buttons.
	banner := node(ops, "dock.errorBanner.errorBanner")
	if banner == nil || banner["k"] != "row" || props(banner)["role"] != "omp.errorBanner" ||
		props(banner)["tone"] != "error" || props(banner)["gap"] != "sm" || props(banner)["align"] != "center" {
		t.Fatalf("errorBanner = %#v", banner)
	}
	msgNode := node(ops, "dock.errorBanner.errorBanner.msg")
	wantSpans := []any{map[string]any{"t": "400 Proof simulated provider error (type=invalid_request_error)", "s": "error"}}
	if msgNode == nil || msgNode["k"] != "text" || props(msgNode)["lines"] != float64(1) ||
		props(msgNode)["truncate"] != "end" || props(msgNode)["grow"] != float64(1) ||
		props(msgNode)["title"] != bannerMsg || !reflect.DeepEqual(props(msgNode)["spans"], wantSpans) {
		t.Fatalf("errorBanner msg = %#v", msgNode)
	}
	detBtn := node(ops, "dock.errorBanner.errorBanner.details")
	if detBtn == nil || detBtn["k"] != "button" || props(detBtn)["role"] != "omp.btn" ||
		props(detBtn)["text"] != "Details" || props(detBtn)["title"] != "Details" ||
		!reflect.DeepEqual(props(detBtn)["actions"], map[string]any{"click": "details"}) {
		t.Fatalf("errorBanner details btn = %#v", detBtn)
	}
	disBtn := node(ops, "dock.errorBanner.errorBanner.dismiss")
	if disBtn == nil || disBtn["k"] != "button" || props(disBtn)["role"] != "omp.btn" ||
		props(disBtn)["text"] != "Dismiss" || props(disBtn)["title"] != "Dismiss" ||
		!reflect.DeepEqual(props(disBtn)["actions"], map[string]any{"click": "dismiss"}) {
		t.Fatalf("errorBanner dismiss btn = %#v", disBtn)
	}

	// Clicking Details expands the banner in place (align:"start", button text:"Less").
	h.out.take()
	h.event(`{"ev":"action","sf":"pig","id":"dock.errorBanner.errorBanner.details","act":"details"}`)
	h.synced()
	var expOps []any
	for _, f := range h.frames() {
		expOps = append(expOps, f.body["ops"].([]any)...)
	}
	if !strings.Contains(fmt.Sprintf("%#v", expOps), `"Less"`) {
		t.Fatalf("clicking Details did not expand banner to Less: %#v", expOps)
	}

	// Clicking Dismiss removes dock.errorBanner.errorBanner while keeping dock.activity.todo.
	h.out.take()
	h.event(`{"ev":"action","sf":"pig","id":"dock.errorBanner.errorBanner.dismiss","act":"dismiss"}`)
	h.synced()
	var disOps []any
	for _, f := range h.frames() {
		disOps = append(disOps, f.body["ops"].([]any)...)
	}
	deletedBanner := false
	for _, raw := range disOps {
		op := raw.([]any)
		if op[0] == "del" && op[1] == "dock.errorBanner.errorBanner" {
			deletedBanner = true
		}
	}
	if !deletedBanner {
		t.Fatalf("clicking Dismiss did not delete dock.errorBanner.errorBanner: %#v", disOps)
	}
}

// TestOmpSkin4ParityFixes verifies the proof18->proof19 alignment fixes:
//   - B3: card[omp.welcome] is flanked by two `spacer{size:"sm"}` siblings in main.
//   - B9/B10/C6: Working mounts inside `row[omp.hud.activity] > col[omp.hud.status]`
//     alongside `checklist[omp.hud.todo]` when both are active.
//   - C7: Live `tool[omp.tool.ask]` carries muted `text` child "Waiting for your answer…".
func TestOmpSkin4ParityFixes(t *testing.T) {
	t.Run("welcome_flanked_by_two_sm_spacers", func(t *testing.T) {
		h := readyOmpRoles(t)
		pigHeader := fe.Lines{
			Lines: []string{
				"   ▄▄▄▄  ▄▄▄▄  v0.7.0+1.5.2",
				"   escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash ·",
				"   ctrl+o more",
			},
			Click: &fe.Area{Row: 0, Column: 1, Rows: 3, Columns: 16},
		}
		ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "n2", Index: 0, Node: pigHeader})
		var mainAdds []string
		for _, raw := range ops {
			op := raw.([]any)
			if op[0] == "add" && op[2] == "main" {
				mainAdds = append(mainAdds, op[1].(string))
			}
		}
		if want := []string{"n.n2.sp0", "n.n2", "n.n2.sp1"}; !reflect.DeepEqual(mainAdds, want) {
			t.Fatalf("welcome main adds = %v, want %v", mainAdds, want)
		}
		for _, spID := range []string{"n.n2.sp0", "n.n2.sp1"} {
			sp := node(ops, spID)
			if sp == nil || sp["k"] != "spacer" || props(sp)["size"] != "sm" {
				t.Fatalf("spacer %s = %#v, want spacer{size:sm}", spID, sp)
			}
		}
	})

	t.Run("working_beside_todo_in_hud_activity", func(t *testing.T) {
		h := readyOmpRoles(t)
		todoRoot := fe.ViewNode{
			ID: "omp.hud.todo", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
			Children: []fe.ViewNode{{
				ID: "omp.todo.phase#0", Kind: fe.ViewKindVStack, Rows: 3, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.todo.phase.head#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "Verify"},
					{ID: "omp.todo.item#0.0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "☑ Check read card"},
					{ID: "omp.todo.item.status#0.0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "done"},
				},
			}},
		}
		dockLines := []string{"Verify", "☑ Check read card", "done"}
		dockView := &fe.View{Seq: 1, Root: todoRoot}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Index: 0, Node: fe.Lines{Lines: dockLines, View: dockView}},
			workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Working"}),
		)
		act := node(ops, "dock.activity")
		if act == nil || act["k"] != "row" || props(act)["role"] != "omp.hud.activity" {
			t.Fatalf("activity row = %#v", act)
		}
		kids := act["c"].([]any)
		if len(kids) != 2 {
			t.Fatalf("activity children = %d, want 2 (status + todo): %#v", len(kids), kids)
		}
		if kids[0].(map[string]any)["id"] != "dock.activity.status" || props(kids[0].(map[string]any))["role"] != "omp.hud.status" {
			t.Fatalf("activity[0] = %#v, want col[omp.hud.status]", kids[0])
		}
		if kids[1].(map[string]any)["id"] != "dock.activity.todo" || props(kids[1].(map[string]any))["role"] != "omp.hud.todo" {
			t.Fatalf("activity[1] = %#v, want checklist[omp.hud.todo]", kids[1])
		}
	})

	t.Run("live_ask_tool_card_has_waiting_text_child", func(t *testing.T) {
		h := readyOmpRoles(t)
		ops := h.applyOps(fe.Op{
			Kind: fe.Insert, Region: fe.RegionMain, ID: "ask1", Index: 0,
			Node: fe.ToolCard{
				Name:      "ask",
				Arguments: map[string]any{"html": `<form><fieldset><legend>Choose a mode:</legend><input type="radio" name="mode" value="Fast"></fieldset></form>`},
				Status:    fe.ToolRunning,
			},
		})
		ask := node(ops, "n.ask1")
		if ask == nil || ask["k"] != "tool" || props(ask)["role"] != "omp.tool.ask" ||
			props(ask)["frame"] != "inline" || props(ask)["collapsible"] != true ||
			props(ask)["collapsed"] != true || props(ask)["preview"] != "none" {
			t.Fatalf("live ask tool = %#v", ask)
		}
		kids, ok := ask["c"].([]any)
		if !ok || len(kids) != 1 {
			t.Fatalf("live ask children = %#v, want 1 text child", ask["c"])
		}
		ch := kids[0].(map[string]any)
		wantSpans := []any{map[string]any{"t": "Waiting for your answer…", "s": "muted"}}
		if ch["k"] != "text" || !reflect.DeepEqual(props(ch)["spans"], wantSpans) {
			t.Fatalf("live ask waiting child = %#v, want muted Waiting for your answer…", ch)
		}
	})
}

// TestOmpHarness5SkinFixes verifies:
//   - Options.DisplayPath strips /private from read/edit/write/glob hrefs, edit target, and diff.path.
//   - Welcome tips with `{key:...}` expand into icon spans (`{t, s:"icon"}`).
//   - Displaced empty todo container hides the entire tool node (`hidden:true`, not `tool[omp.tool.todo]`).
//   - Bash nested tool-command output (`omp.tool.output`) and `omp.tool.read.code` omit custom roles.
func TestOmpHarness5SkinFixes(t *testing.T) {
	t.Run("display_path_strips_private_everywhere", func(t *testing.T) {
		h := readyOmpRoles(t)
		h.session.displayPath = func(p string) string {
			return strings.TrimPrefix(p, "/private")
		}
		h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "/private/tmp/proof/work"}))
		globView := &fe.View{Seq: 1, Root: fe.ViewNode{
			ID: "omp.tool.glob", Kind: fe.ViewKindContainer, Rows: 1, Width: 60,
			Children: []fe.ViewNode{{
				ID: "omp.tool.files", Kind: fe.ViewKindVStack, Rows: 1, Width: 60,
				Children: []fe.ViewNode{{ID: "omp.tool.file#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "notes.txt"}},
			}},
		}}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r1", Index: 0, Node: fe.ToolCard{
				Name: "read", Arguments: map[string]any{"path": "notes.txt"}, Status: fe.ToolDone,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e1", Index: 1, Node: fe.ToolCard{
				Name: "edit", Arguments: map[string]any{"input": "[notes.txt#TAG]\nPUT 2.=2:\n+ok\n"}, Status: fe.ToolDone,
				Diff: &fe.Diff{Path: "notes.txt", Text: "@@ -1,2 +1,2 @@\n a\n-b\n+ok\n"},
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "w1", Index: 2, Node: fe.ToolCard{
				Name: "write", Arguments: map[string]any{"path": "gen.txt", "content": "hi\n"}, Status: fe.ToolDone,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "g1", Index: 3, Node: fe.ToolCard{
				Name: "glob", Arguments: map[string]any{"path": "*.txt"}, Status: fe.ToolDone,
				Result: []string{"notes.txt"}, ResultView: globView,
			}},
			// Absolute path from tool args/details (e.g. temp local:// note) must NOT be rewritten.
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r_abs", Index: 4, Node: fe.ToolCard{
				Name: "read", Arguments: map[string]any{"path": "/private/tmp/local-temp/note.md"}, Status: fe.ToolDone,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e_abs", Index: 5, Node: fe.ToolCard{
				Name: "edit", Arguments: map[string]any{"input": "[/private/tmp/local-temp/note.md#TAG]\nPUT 1.=1:\n+ok\n"}, Status: fe.ToolDone,
				Diff: &fe.Diff{Path: "/private/tmp/local-temp/note.md", Text: "@@ -1 +1 @@\n-a\n+ok\n"},
			}},
		)
		if got := props(node(ops, "n.r1"))["href"]; got != "file:///tmp/proof/work/notes.txt" {
			t.Fatalf("read href = %#v", got)
		}
		e1 := node(ops, "n.e1")
		if props(e1)["href"] != "file:///tmp/proof/work/notes.txt" || props(e1)["target"] != "/tmp/proof/work/notes.txt:2" {
			t.Fatalf("edit head = %#v", e1)
		}
		if got := props(node(ops, "n.e1.diff"))["path"]; got != "/tmp/proof/work/notes.txt" {
			t.Fatalf("edit diff.path = %#v", got)
		}
		if got := props(node(ops, "n.w1"))["href"]; got != "file:///tmp/proof/work/gen.txt" {
			t.Fatalf("write href = %#v", got)
		}
		gSpans := props(node(ops, "n.g1")["c"].([]any)[0].(map[string]any)["c"].([]any)[0].(map[string]any)["c"].([]any)[1].(map[string]any))["spans"]
		wantG := []any{map[string]any{"t": "notes.txt", "s": "strong", "href": "file:///tmp/proof/work/notes.txt"}}
		if !reflect.DeepEqual(gSpans, wantG) {
			t.Fatalf("glob spans = %#v, want %#v", gSpans, wantG)
		}
		// Absolute paths from details/args retain their exact spelling (no DisplayPath rewrite).
		if got := props(node(ops, "n.r_abs"))["href"]; got != "file:///private/tmp/local-temp/note.md" {
			t.Fatalf("absolute read href must stay verbatim, got %#v", got)
		}
		eAbs := node(ops, "n.e_abs")
		if props(eAbs)["href"] != "file:///private/tmp/local-temp/note.md" || props(eAbs)["target"] != "/private/tmp/local-temp/note.md:1" {
			t.Fatalf("absolute edit head must stay verbatim, got %#v", eAbs)
		}
		if got := props(node(ops, "n.e_abs.diff"))["path"]; got != "/private/tmp/local-temp/note.md" {
			t.Fatalf("absolute edit diff.path must stay verbatim, got %#v", got)
		}
	})
	t.Run("welcome_tip_key_placeholder_emits_icon_spans", func(t *testing.T) {
		p := ompTipTextProps("Press {key:shift+tab} to cycle through reasoning effort levels")
		if p["text"] != nil {
			t.Fatalf("keycap tip should emit spans, not text: %#v", p)
		}
		wantSpans := []any{
			map[string]any{"t": "Press"},
			map[string]any{"t": "\U000f0636", "s": "icon"},
			map[string]any{"t": "\U000f0312", "s": "icon"},
			map[string]any{"t": "to cycle through reasoning effort levels"},
		}
		if !reflect.DeepEqual(p["spans"], wantSpans) {
			t.Fatalf("keycap tip spans = %#v, want %#v", p["spans"], wantSpans)
		}
	})

	t.Run("displaced_empty_todo_view_hides_tool_card", func(t *testing.T) {
		h := readyOmpRoles(t)
		emptyDisp := &fe.View{Seq: 1, Root: fe.ViewNode{Kind: fe.ViewKindContainer, Rows: 0, Width: 80}}
		ops := h.applyOps(
			fe.Op{
				Kind: fe.Insert, Region: fe.RegionMain, ID: "td_old", Index: 0,
				Node: fe.ToolCard{Name: "todo", Status: fe.ToolDone, Result: []string{}, ResultView: emptyDisp},
			},
			// PiG's toolRenderProxy.FrontendView returns nil when len(lines)==0,
			// leaving Result: []string{} (non-nil empty) and ResultView: nil.
			fe.Op{
				Kind: fe.Insert, Region: fe.RegionMain, ID: "td_old_nilview", Index: 1,
				Node: fe.ToolCard{Name: "todo", Status: fe.ToolDone, Output: "Remaining items (2)...", Result: []string{}, ResultView: nil},
			},
		)
		for _, id := range []string{"n.td_old", "n.td_old_nilview"} {
			nd := node(ops, id)
			if nd == nil || nd["k"] != "col" || props(nd)["hidden"] != true || props(nd)["role"] != nil {
				t.Fatalf("displaced todo node %s = %#v, want hidden empty col", id, nd)
			}
		}
	})

	t.Run("bash_command_tool_output_and_read_code_omit_custom_roles", func(t *testing.T) {
		h := readyOmpRoles(t)
		bashView := &fe.View{Seq: 1, Root: fe.ViewNode{
			ID: "omp.tool.bash", Kind: fe.ViewKindContainer, Rows: 2, Width: 60,
			Children: []fe.ViewNode{{
				ID: "omp.tool.bash.command-tool#0", Kind: fe.ViewKindVStack, Rows: 2, Width: 60,
				Children: []fe.ViewNode{
					{ID: "omp.tool.bash.command-tool.head#0", Kind: fe.ViewKindText, Rows: 1, Width: 60, Text: "Discard"},
					{ID: "omp.tool.output#0", Kind: fe.ViewKindLines, Rows: 1, Width: 60, Lines: []string{"Proof preview only"}},
				},
			}},
		}}
		readView := &fe.View{Seq: 2, Root: fe.ViewNode{
			ID: "omp.tool.read", Kind: fe.ViewKindContainer, Rows: 1, Width: 60,
			Children: []fe.ViewNode{
				{ID: "omp.tool.read.code", Kind: fe.ViewKindLines, Rows: 1, Width: 60, Lines: []string{"hello"}},
			},
		}}
		ops := h.applyOps(
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "b_cmd", Index: 0, Node: fe.ToolCard{
				Name: "bash", Status: fe.ToolDone, Result: []string{"Discard", "Proof preview only"}, ResultView: bashView,
			}},
			fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r_exp", Index: 1, Node: fe.ToolCard{
				Name: "read", Arguments: map[string]any{"path": "notes.txt"}, Status: fe.ToolDone, Expanded: true,
				Result: []string{"hello"}, ResultView: readView,
			}},
		)
		bOut := node(ops, "n.b_cmd")["c"].([]any)[0].(map[string]any)["c"].([]any)[1].(map[string]any)
		if bOut["k"] != "text" || props(bOut)["role"] != nil {
			t.Fatalf("bash command-tool output = %#v, want bare muted text (no role)", bOut)
		}
		rCode := node(ops, "n.r_exp")["c"].([]any)[0].(map[string]any)
		if rCode["k"] != "code" || props(rCode)["role"] != nil || props(rCode)["path"] != "notes.txt" {
			t.Fatalf("expanded read code = %#v, want bare code (no role)", rCode)
		}
	})
}
