package frontend

import (
	"reflect"
	"strings"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
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

	col := node(ops, "dock.editor")
	if col == nil || col["k"] != "col" || props(col)["role"] != "omp.editor" {
		t.Fatalf("composer root = %#v", col)
	}
	if _, hasTone := props(col)["tone"]; hasTone {
		t.Fatalf("idle composer should have no tone: %#v", props(col))
	}
	// Context meter above the input line.
	ctx := node(ops, "dock.editor.context")
	if ctx == nil || ctx["k"] != "meter" || props(ctx)["role"] != "omp.composer.context" ||
		props(ctx)["style"] != "bar" || props(ctx)["value"] != 0.25 ||
		props(ctx)["label"] != "25%" || props(ctx)["total"] != "200K" {
		t.Fatalf("context meter = %#v", ctx)
	}

	// Input line and editor node.
	line := node(ops, "dock.editor.line")
	if line == nil || line["k"] != "row" || props(line)["role"] != "omp.composer.line" {
		t.Fatalf("composer line = %#v", line)
	}
	input := node(ops, "dock.editor.line.input")
	if input == nil || input["k"] != "editor" || props(input)["sendable"] != true ||
		props(input)["placeholder"] != ompComposerPlaceholder {
		t.Fatalf("composer input = %#v", input)
	}

	// Raw text, cursor, and focus ops target dock.editor.line.input.
	doc := shownBy(ops)
	if doc["dock.editor.line.input"]["text"] != "hello" || doc["dock.editor.line.input"]["cursor"] != float64(5) {
		t.Fatalf("input text/cursor = %#v", doc["dock.editor.line.input"])
	}
	foundFocus := false
	for _, op := range ops {
		if o := op.([]any); o[0] == "focus" && o[1] == "dock.editor.line.input" {
			foundFocus = true
		}
	}
	if !foundFocus {
		t.Fatalf("missing focus on dock.editor.line.input: %#v", ops)
	}

	// Composer bar children in omp order: model (with nested effort), extras, usage, send.
	bar := node(ops, "dock.editor.bar")
	if bar == nil || bar["k"] != "row" || props(bar)["role"] != "omp.composer.bar" {
		t.Fatalf("composer bar = %#v", bar)
	}
	var barIDs []string
	for _, c := range bar["c"].([]any) {
		barIDs = append(barIDs, c.(map[string]any)["id"].(string))
	}
	if want := []string{
		"dock.editor.bar.model",
		"dock.editor.bar.extras",
		"dock.editor.bar.usage",
		"dock.editor.bar.send",
	}; !reflect.DeepEqual(barIDs, want) {
		t.Fatalf("bar children = %v, want %v", barIDs, want)
	}

	modelChip := node(ops, "dock.editor.bar.model")
	if props(modelChip)["role"] != "omp.composer.model" {
		t.Fatalf("model chip = %#v", modelChip)
	}
	effortGlyph := node(ops, "dock.editor.bar.model.effort")
	if effortGlyph == nil || effortGlyph["k"] != "effort" || props(effortGlyph)["role"] != "omp.composer.model.effort" || props(effortGlyph)["level"] != "high" {
		t.Fatalf("effort glyph = %#v", effortGlyph)
	}
	extras := node(ops, "dock.editor.bar.extras")
	if extras == nil || extras["k"] != "status" || props(extras)["role"] != "omp.composer.extras" || props(extras)["transparent"] != true {
		t.Fatalf("extras = %#v", extras)
	}
	gitFact := node(ops, "dock.editor.bar.extras.git")
	if gitFact == nil || gitFact["k"] != "seg" || props(gitFact)["role"] != "omp.composer.fact" {
		t.Fatalf("git fact = %#v", gitFact)
	}
	usageNode := node(ops, "dock.editor.bar.usage")
	if usageNode == nil || usageNode["k"] != "text" || props(usageNode)["role"] != "omp.composer.usage" || props(usageNode)["text"] != "S0.01" {
		t.Fatalf("usage node = %#v", usageNode)
	}
	sendNode := node(ops, "dock.editor.bar.send")
	if sendNode == nil || sendNode["k"] != "kbd" || props(sendNode)["role"] != "omp.composer.send" ||
		!reflect.DeepEqual(props(sendNode)["keys"], []any{"enter"}) {
		t.Fatalf("send node = %#v", sendNode)
	}

	// Typing updates patch dock.editor.line.input without rebuilding the composer.
	renders := h.session.renders
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Text: "hello!", Cursor: 6, Sendable: true}})
	if want := jsonOps(t,
		[]any{"text", "dock.editor.line.input", "append", "!"},
		[]any{"set", "dock.editor.line.input", map[string]any{"cursor": 6}},
	); !reflect.DeepEqual(ops, want) {
		t.Fatalf("typing ops = %#v, want %#v", ops, want)
	}
	if h.session.renders != renders {
		t.Fatalf("typing triggered %d renders, want 0", h.session.renders-renders)
	}

	// Native edit, undo, send, and clicks on model/effort/send route properly.
	h.event(`{"ev":"edit","sf":"pig","id":"dock.editor.line.input","from":0,"to":6,"text":"hi","cursor":2,"len":6}`)
	h.event(`{"ev":"undo","sf":"pig","id":"dock.editor.line.input"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.bar.model","act":"status.model"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.bar.model.effort","act":"thinking.cycle"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.bar.send","act":"submit"}`)
	h.synced()
	if want := []fe.Edit{{From: 0, To: 6, Text: "hi", Cursor: 2, Len: 6}}; !reflect.DeepEqual(h.edits, want) {
		t.Fatalf("edits = %#v, want %#v", h.edits, want)
	}
	if h.undos != 1 {
		t.Fatalf("undos = %d, want 1", h.undos)
	}
	if want := []string{"/model", "/thinking", "hello!"}; !reflect.DeepEqual(h.sends, want) {
		t.Fatalf("sends = %q, want %q", h.sends, want)
	}

	// Working state gives omp.editor tone="pending" and blocks model/effort clicks
	// (matching footer_click_test.go); removing Working clears the tone.
	ops = h.applyOps(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Working"}))
	if setOps := opsOn(ops, "dock.editor"); len(setOps) == 0 || setOps[0].([]any)[2].(map[string]any)["tone"] != "pending" {
		t.Fatalf("working did not set composer tone=pending: %#v", ops)
	}
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.bar.model","act":"status.model"}`)
	h.event(`{"ev":"action","sf":"pig","id":"dock.editor.bar.model.effort","act":"thinking.cycle"}`)
	h.synced()
	h.mu.Lock()
	sendsWhileWorking := len(h.sends)
	h.mu.Unlock()
	if sendsWhileWorking != 3 {
		t.Fatalf("clicks while working sent %q", h.sends[3:])
	}
	ops = h.applyOps(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "working"})
	if setOps := opsOn(ops, "dock.editor"); len(setOps) == 0 || setOps[0].([]any)[2].(map[string]any)["tone"] != nil {
		t.Fatalf("stopping work did not clear composer tone: %#v", ops)
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
	if got := shownBy(ops)["dock.editor.bar.model.name"]; got != nil {
		if spans, _ := got["spans"].([]any); len(spans) == 0 || !strings.Contains(spans[0].(map[string]any)["t"].(string), "claude-opus") {
			t.Fatalf("composer model did not update: %#v", got)
		}
	}

	// When a selector replaces the editor, dock.footer mounts with omp.status;
	// when the editor returns, dock.footer unmounts again.
	ops = h.applyOps(
		fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "editor", Index: 0},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "selector.1", Index: 0, Node: fe.Selector{Title: "Models", Items: []fe.SelectorItem{{ID: "a", Label: "A"}}}},
	)
	if ft := node(ops, "dock.footer"); ft == nil || ft["k"] != "status" || props(ft)["role"] != "omp.status" {
		t.Fatalf("dock.footer did not mount when selector replaced editor: %#v", ops)
	}
	ops = h.applyOps(
		fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "selector.1", Index: 0},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
	)
	deletedFooter := false
	for _, op := range ops {
		if o := op.([]any); o[0] == "del" && o[1] == "dock.footer" {
			deletedFooter = true
		}
	}
	if !deletedFooter {
		t.Fatalf("dock.footer was not removed when composer returned: %#v", ops)
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
	row := node(ops, "dock.working")
	if row == nil || row["k"] != "row" || props(row)["role"] != "omp.working" || props(row)["align"] != "center" || props(row)["gap"] != "sm" {
		t.Fatalf("working row = %#v", row)
	}
	kids := row["c"].([]any)
	if len(kids) != 5 {
		t.Fatalf("working children count = %d, want 5: %#v", len(kids), kids)
	}
	if kids[0].(map[string]any)["k"] != "spinner" || props(kids[0].(map[string]any))["style"] != "starburst" {
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
	want := jsonOps(t, []any{"set", "dock.working.label", map[string]any{
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
	prog := node(ops, "dock.working.progress")
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
	errCard := node(ops, "n.a1.e")
	if errCard == nil || errCard["k"] != "card" || props(errCard)["role"] != "omp.error" || props(errCard)["tone"] != "error" {
		t.Fatalf("error card = %#v", errCard)
	}
	codeBadge := node(ops, "n.a1.e.head.code")
	if codeBadge == nil || codeBadge["k"] != "badge" || props(codeBadge)["role"] != "omp.error.code" || props(codeBadge)["text"] != "503" {
		t.Fatalf("error code badge = %#v", codeBadge)
	}
	errMsg := node(ops, "n.a1.e.message")
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

	// 1a. edit with hashline input + Diff having empty Path -> target & diff.path come from [PATH#TAG].
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
		props(e1)["target"] != "notes.txt" || props(e1)["targetKind"] != "path" ||
		props(e1)["href"] != "/tmp/work/notes.txt" ||
		!reflect.DeepEqual(props(e1)["meta"], []any{"+1 −1"}) {
		t.Fatalf("edit single = %#v", e1)
	}
	diffNode := node(ops, "n.e1.diff")
	if diffNode == nil || diffNode["k"] != "diff" || props(diffNode)["path"] != "notes.txt" {
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
	if e3 := node(ops, "n.e3"); e3 == nil || props(e3)["title"] != "Delete" || props(e3)["target"] != "old.txt" {
		t.Fatalf("edit REM = %#v", e3)
	}
	editMv := fe.ToolCard{
		Name:      "edit",
		Arguments: map[string]any{"input": "[a.txt#ABCD]\nMV b.txt\n"},
		Status:    fe.ToolDone,
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e4", Index: 3, Node: editMv})
	if e4 := node(ops, "n.e4"); e4 == nil || props(e4)["target"] != "a.txt" || !reflect.DeepEqual(props(e4)["meta"], []any{"→ b.txt"}) {
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
		props(r1)["targetKind"] != "path" || props(r1)["href"] != "/tmp/work/pkg/file.go" ||
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
			id:         "f1",
			tool:       fe.ToolCard{Name: "find", Arguments: map[string]any{"query": "auth handler", "path": "pkg"}, Status: fe.ToolDone},
			title:      "Find",
			target:     "auth handler",
			targetKind: "query",
			frame:      "inline",
			meta:       []any{"in pkg"},
		},
		{
			id:         "w1",
			tool:       fe.ToolCard{Name: "write", Arguments: map[string]any{"path": "out.txt", "content": "a\nb\nc\n"}, Status: fe.ToolDone},
			title:      "Write",
			target:     "out.txt",
			targetKind: "path",
			frame:      "card",
			meta:       []any{"3 lines"},
		},
		{
			id:         "td1",
			tool:       fe.ToolCard{Name: "todo", Arguments: map[string]any{"op": "start", "task": "Wire heads"}, Status: fe.ToolDone},
			title:      "Todo",
			target:     "start Wire heads",
			targetKind: "text",
			frame:      "card",
		},
		{
			id: "ask1",
			tool: fe.ToolCard{
				Name: "ask",
				Arguments: map[string]any{
					"questions": []any{
						map[string]any{"id": "q1", "question": "Pick a DB?"},
						map[string]any{"id": "q2", "question": "Pick a port?"},
					},
				},
				Status: fe.ToolDone,
			},
			title:      "Ask",
			target:     "Pick a DB?",
			targetKind: "text",
			frame:      "inline",
			meta:       []any{"2 questions"},
		},
		{
			id:         "ws1",
			tool:       fe.ToolCard{Name: "web_search", Arguments: map[string]any{"query": "golang 1.26"}, Status: fe.ToolDone},
			title:      "Web search",
			target:     "golang 1.26",
			targetKind: "query",
			frame:      "inline",
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
	h := readyOmpRoles(t)
	footer := sampleFooter()
	footer.SessionName = "my-session"
	ops := h.applyOps(footerOp(fe.Insert, footer))
	st := node(ops, "dock.footer")
	if st == nil || st["k"] != "status" || props(st)["role"] != "omp.status" {
		t.Fatalf("status = %#v", st)
	}
	wantRoles := map[string]any{
		"dock.footer.cwd":      "omp.status.path",
		"dock.footer.branch":   "omp.status.git",
		"dock.footer.session":  "omp.status.session_name",
		"dock.footer.status.0": "omp.status.hook",
		"dock.footer.usage":    "omp.status.cost",
		"dock.footer.context":  "omp.status.context_pct",
		"dock.footer.xp":       nil,
		"dock.footer.model":    "omp.status.model",
	}
	gotRoles := map[string]any{}
	for _, c := range st["c"].([]any) {
		seg := c.(map[string]any)
		gotRoles[seg["id"].(string)] = props(seg)["role"]
	}
	if !reflect.DeepEqual(gotRoles, wantRoles) {
		t.Fatalf("status segment roles = %#v, want %#v", gotRoles, wantRoles)
	}
	// Path splits parent (dim) and leaf (strong).
	cwd := node(ops, "dock.footer.cwd")
	wantSpans := []any{
		map[string]any{"t": "~/src/", "s": "statusLinePath dim"},
		map[string]any{"t": "pig", "s": "statusLinePath strong"},
	}
	if !reflect.DeepEqual(props(cwd)["spans"], wantSpans) {
		t.Fatalf("cwd spans = %#v, want %#v", props(cwd)["spans"], wantSpans)
	}
}

// TestNonOmpRolesFullFrameUnchanged verifies that with ompRoles false, a full
// frame covering every transcript and dock node kind emits exact pig-tern wire
// frames with zero omp.* roles or omp-only node shapes.
func TestNonOmpRolesFullFrameUnchanged(t *testing.T) {
	h := ready(t)
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u", Index: 0, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "hi"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "th", Index: 1, Node: fe.Thinking{Text: "hmm", Streaming: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Index: 2, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "ok", Streaming: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a.e", Index: 3, Node: fe.Lines{Lines: []string{"Error: 500 boom"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "div", Index: 4, Node: fe.Lines{Lines: []string{"──────────"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t", Index: 5, Node: fe.ToolCard{Name: "bash", Arguments: map[string]any{"command": "ls"}, Status: fe.ToolRunning}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "working", Index: 0, Node: fe.Working{Kind: fe.WorkingAgent, Message: "Working"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{Text: "draft", Cursor: 5, Placeholder: "Ask", Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 2, Node: sampleFooter()},
	)

	// Verify exact pig-tern node shapes and roles.
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
	if w := node(ops, "dock.working"); w["k"] != "spinner" || props(w)["role"] != "pig.working" || props(w)["style"] != "braille" {
		t.Fatalf("working = %#v", w)
	}
	if ed := node(ops, "dock.editor"); ed["k"] != "editor" || props(ed)["role"] != nil || props(ed)["placeholder"] != "Ask" {
		t.Fatalf("editor = %#v", ed)
	}
	if ft := node(ops, "dock.footer"); ft["k"] != "status" || props(ft)["role"] != "pig.footer" {
		t.Fatalf("footer = %#v", ft)
	}

	// Ensure no "omp." role leaked anywhere in the non-omp frame.
	doc := shownBy(ops)
	for id, p := range doc {
		if role, ok := p["role"].(string); ok && strings.HasPrefix(role, "omp.") {
			t.Fatalf("non-omp frame leaked omp role on %s: %q", id, role)
		}
	}
}
