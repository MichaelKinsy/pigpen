package frontend

import (
	"reflect"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

func TestToolTargetPrefersArgumentsAndFallsBackToTheHeader(t *testing.T) {
	for _, tc := range []struct {
		tool         fe.ToolCard
		target, kind string
	}{
		{fe.ToolCard{Arguments: map[string]any{"command": "go test"}, Header: "$ go test"}, "go test", "command"},
		{fe.ToolCard{Arguments: map[string]any{"path": "a.go", "offset": 2.0}, Header: "read a.go:2"}, "a.go", "path"},
		{fe.ToolCard{Arguments: map[string]any{"pattern": "TODO", "path": "."}, Header: "grep /TODO/ in ."}, "TODO", "pattern"},
		{fe.ToolCard{Arguments: map[string]any{"query": "x"}, Header: "web_search x"}, "web_search x", "text"},
		{fe.ToolCard{Header: "bash"}, "bash", "text"},
	} {
		if target, kind := toolTarget(tc.tool); target != tc.target || kind != tc.kind {
			t.Fatalf("toolTarget(%v) = %q, %q", tc.tool.Arguments, target, kind)
		}
	}
}

// Tern's tool kind has a cancelled status: a call the user aborted shows the
// stop glyph instead of a failure.
func TestCancelledToolKeepsItsStatus(t *testing.T) {
	node := (&session{}).mainNode("n.t", fe.ToolCard{Name: "bash", Status: fe.ToolCancelled, Output: "Operation aborted"})
	if status := node.P["status"]; status != "cancelled" {
		t.Fatalf("status = %v", status)
	}
}

// Assistant text is an md node that streams by text append while its source
// only grows; a rewrite replaces it, and the end of streaming turns the
// caret off and settles the node.
func TestAssistantMarkdownStreamsByAppend(t *testing.T) {
	h := ready(t)
	md := func(text string, streaming bool) fe.Op {
		return fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "n1.0", Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: text, Streaming: streaming}}
	}
	insert := md("Looking", true)
	insert.Kind = fe.Insert
	want := jsonOps(t, []any{"add", "n.n1.0", "main", nil, map[string]any{"id": "n.n1.0", "k": "md", "p": map[string]any{"text": "Looking", "stream": true, "role": "pig.assistant"}}})
	if ops := h.applyOps(insert); !reflect.DeepEqual(ops, want) {
		t.Fatalf("insert = %#v", ops)
	}
	for _, tc := range []struct {
		op   fe.Op
		want []any
	}{
		{md("Looking at **it**", true), jsonOps(t, []any{"text", "n.n1.0", "append", " at **it**"})},
		{md("Rewritten", true), jsonOps(t, []any{"text", "n.n1.0", "replace", "Rewritten"})},
		{md("Rewritten.", false), jsonOps(t, []any{"text", "n.n1.0", "append", "."}, []any{"set", "n.n1.0", map[string]any{"stream": false}}, []any{"settle", "n.n1.0"})},
	} {
		if got := h.applyOps(tc.op); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: ops = %#v, want %#v", tc.op.Node.(fe.MarkdownText).Text, got, tc.want)
		}
	}
}

// Appends that arrive while Tern holds no credit fold into one.
func TestPendingAppendsFold(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":1`)
	h.session.InputReady()
	h.apply()
	h.ack()
	h.eventually("the ack", func() bool { return h.session.out.frames.Load() == 1 })
	h.messages()
	op := func(kind fe.OpKind, text string) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: "m", Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: text, Streaming: true}}
	}
	h.applyOps(op(fe.Insert, "a"))
	for _, text := range []string{"ab", "abc", "abcd"} {
		h.apply(op(fe.Update, text))
	}
	if got := h.frames(); len(got) != 0 {
		t.Fatalf("sent without a credit: %#v", got)
	}
	h.ack()
	h.eventually("the folded frame", func() bool { return h.session.out.frames.Load() == 3 })
	got := h.frames()
	if len(got) != 1 || !reflect.DeepEqual(got[0].body["ops"], jsonOps(t, []any{"text", "n.m", "append", "bcd"})) {
		t.Fatalf("folded = %#v", got)
	}
}

// A user message is an md node in a user-toned card, settled at once;
// editing its text patches the md child and settles the card again.
func TestUserMarkdownIsACard(t *testing.T) {
	h := ready(t)
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u", Node: fe.MarkdownText{Role: fe.RoleUser, Text: "Fix it"}})
	if len(ops) != 2 || !reflect.DeepEqual(ops[1], []any{"settle", "n.u"}) {
		t.Fatalf("user insert = %#v", ops)
	}
	card := ops[0].([]any)[4].(map[string]any)
	child := card["c"].([]any)[0].(map[string]any)
	if card["k"] != "card" || props(card)["tone"] != "user" || child["k"] != "md" || props(child)["text"] != "Fix it" {
		t.Fatalf("user node = %#v", card)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "u", Node: fe.MarkdownText{Role: fe.RoleUser, Text: "Fix it now"}})
	if !reflect.DeepEqual(ops, jsonOps(t, []any{"text", "n.u.md", "append", " now"}, []any{"settle", "n.u"})) {
		t.Fatalf("user update = %#v", ops)
	}
}

// A node whose kind changes is replaced in place, before its next sibling,
// and settles again when final; so is a message whose role changes.
func TestKindChangeReplacesTheNode(t *testing.T) {
	h := ready(t)
	h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Index: 0, Node: fe.Lines{Lines: []string{"x"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.Lines{Lines: []string{"y"}}},
	)
	ops := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Index: 0, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "x"}})
	if len(ops) != 3 || !reflect.DeepEqual(ops[0], []any{"del", "n.a"}) || ops[1].([]any)[0] != "add" || ops[1].([]any)[3] != "n.b" || !reflect.DeepEqual(ops[2], []any{"settle", "n.a"}) {
		t.Fatalf("kind change = %#v", ops)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Index: 0, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "x"}})
	if len(ops) != 3 || !reflect.DeepEqual(ops[0], []any{"del", "n.a"}) || ops[1].([]any)[4].(map[string]any)["k"] != "card" || !reflect.DeepEqual(ops[2], []any{"settle", "n.a"}) {
		t.Fatalf("role change = %#v", ops)
	}
	// b is the last node: lines there may still change, so they do not settle.
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.Lines{Lines: []string{"", " "}}})
	if !reflect.DeepEqual(ops, jsonOps(t, []any{"text", "n.b", "replace", "\n"}, []any{"set", "n.b", map[string]any{"hidden": true}})) {
		t.Fatalf("lines update = %#v", ops)
	}
}

// A thinking run is a muted collapsible card whose md child streams by
// append; hiding thinking folds the card, and the run settles once it ends.
func TestThinkingIsACollapsibleCard(t *testing.T) {
	h := ready(t)
	op := func(kind fe.OpKind, thinking fe.Thinking) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: "a.0", Node: thinking}
	}
	ops := h.applyOps(op(fe.Insert, fe.Thinking{Text: "Reading", Streaming: true}))
	card := ops[0].([]any)[4].(map[string]any)
	p := props(card)
	if card["k"] != "card" || p["tone"] != "muted" || p["collapsible"] != true || p["collapsed"] != false || p["head"] != "Thinking" {
		t.Fatalf("thinking card = %#v", card)
	}
	if child := card["c"].([]any)[0].(map[string]any); child["k"] != "md" || props(child)["stream"] != true {
		t.Fatalf("thinking body = %#v", child)
	}
	ops = h.applyOps(op(fe.Update, fe.Thinking{Text: "Reading it", Hidden: true}))
	want := jsonOps(t,
		[]any{"set", "n.a.0", map[string]any{"collapsed": true}},
		[]any{"text", "n.a.0.md", "append", " it"},
		[]any{"set", "n.a.0.md", map[string]any{"stream": false}},
		[]any{"settle", "n.a.0"},
	)
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("thinking update = %#v", ops)
	}
}

// A tool card's status and output change as a set and a text append, and
// the card settles once its status is final; when a result carrying a diff
// arrives, the card is replaced by one with a Tern diff child and a diff
// stat, and settles again.
func TestToolCardPatchesAndDiffReplaces(t *testing.T) {
	h := ready(t)
	patch := "Index: a.go\n===\n--- a.go\n+++ a.go\n@@ -1,2 +1,2 @@\n-old\n+new\n same\n"
	running := fe.ToolCard{Name: "edit", Arguments: map[string]any{"path": "a.go"}, Status: fe.ToolRunning, Output: "a"}
	h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t", Node: running})
	more := running
	more.Output = "ab"
	more.Status = fe.ToolDone
	ops := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "t", Node: more})
	if !reflect.DeepEqual(ops, jsonOps(t, []any{"set", "n.t", map[string]any{"status": "done"}}, []any{"text", "n.t.out", "append", "b"}, []any{"settle", "n.t"})) {
		t.Fatalf("tool update = %#v", ops)
	}
	done := more
	done.Diff = &fe.Diff{Path: "a.go", Text: patch}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "t", Node: done})
	if len(ops) != 3 || !reflect.DeepEqual(ops[0], []any{"del", "n.t"}) || !reflect.DeepEqual(ops[2], []any{"settle", "n.t"}) {
		t.Fatalf("diff arrival = %#v", ops)
	}
	tool := ops[1].([]any)[4].(map[string]any)
	child := tool["c"].([]any)[0].(map[string]any)
	if props(tool)["meta"].([]any)[0] != "+1 −1" || child["k"] != "diff" || props(child)["text"] != patch || props(child)["path"] != "a.go" {
		t.Fatalf("diff tool = %#v", tool)
	}
}

// Every node PiG is done with settles in the frame that adds it, as a
// resumed session's transcript does; the streaming message, the running tool
// and the last lines stay live until they end or a node follows them.
func TestFinalNodesSettle(t *testing.T) {
	h := ready(t)
	insert := func(id string, at int, node fe.Node) fe.Op {
		return fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: id, Index: at, Node: node}
	}
	running := fe.ToolCard{Name: "bash", Arguments: map[string]any{"command": "ls"}, Status: fe.ToolRunning}
	ops := h.applyOps(
		insert("h", 0, fe.Lines{Lines: []string{"header"}}),
		insert("u", 1, fe.MarkdownText{Role: fe.RoleUser, Text: "Fix it"}),
		insert("a.0", 2, fe.Thinking{Text: "Hm"}),
		insert("a.1", 3, fe.ToolCard{Name: "bash", Arguments: map[string]any{"command": "ls"}, Status: fe.ToolError}),
		insert("a.2", 4, fe.MarkdownText{Role: fe.RoleAssistant, Text: "Done"}),
		insert("b.0", 5, fe.MarkdownText{Role: fe.RoleAssistant, Text: "Look", Streaming: true}),
		insert("b.1", 6, running),
		insert("l", 7, fe.Lines{Lines: []string{"loading"}}),
	)
	var settled []any
	for _, op := range ops {
		if o := op.([]any); o[0] == "settle" {
			settled = append(settled, o[1])
		}
	}
	if want := []any{"n.u", "n.h", "n.a.0", "n.a.1", "n.a.2"}; !reflect.DeepEqual(settled, want) {
		t.Fatalf("settled = %#v, want %#v", settled, want)
	}
	done := running
	done.Status = fe.ToolDone
	ops = h.applyOps(
		fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "b.0", Index: 5, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "Look"}},
		fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "b.1", Index: 6, Node: done},
		insert("m", 8, fe.MarkdownText{Role: fe.RoleUser, Text: "Next"}),
	)
	want := jsonOps(t,
		[]any{"set", "n.b.0", map[string]any{"stream": false}},
		[]any{"settle", "n.b.0"},
		[]any{"set", "n.b.1", map[string]any{"status": "done"}},
		[]any{"settle", "n.b.1"},
		[]any{"add", "n.m", "main", nil, map[string]any{"id": "n.m", "k": "card", "p": map[string]any{"role": "pig.user", "tone": "user"}, "c": []any{map[string]any{"id": "n.m.md", "k": "md", "p": map[string]any{"text": "Next"}}}}},
		[]any{"settle", "n.m"},
		[]any{"settle", "n.l"},
	)
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("ends = %#v, want %#v", ops, want)
	}
}

// A settled node PiG changes again (a fold, a new width) is settled again
// after the change, which ends the hint in Tern; an unchanged node sends
// nothing. While no credit is free, each change and its settle wait in
// order: a change never folds into one before a settle.
func TestPatchedSettledNodeSettlesAgain(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":1`)
	h.session.InputReady()
	h.apply()
	h.ack()
	h.messages()
	thinking := func(kind fe.OpKind, hidden bool) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: "a", Node: fe.Thinking{Text: "Hm", Hidden: hidden}}
	}
	h.applyOps(thinking(fe.Insert, false))
	h.ack()
	ops := h.applyOps(thinking(fe.Update, true))
	if want := jsonOps(t, []any{"set", "n.a", map[string]any{"collapsed": true}}, []any{"settle", "n.a"}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("fold = %#v, want %#v", ops, want)
	}
	h.apply(thinking(fe.Update, true))
	h.apply(thinking(fe.Update, false))
	h.apply(thinking(fe.Update, true))
	if got := h.frames(); len(got) != 0 {
		t.Fatalf("sent without a credit: %#v", got)
	}
	h.ack()
	h.eventually("the held frame", func() bool { return h.session.out.frames.Load() == 4 })
	got := h.frames()
	want := jsonOps(t,
		[]any{"set", "n.a", map[string]any{"collapsed": false}},
		[]any{"settle", "n.a"},
		[]any{"set", "n.a", map[string]any{"collapsed": true}},
		[]any{"settle", "n.a"},
	)
	if len(got) != 1 || !reflect.DeepEqual(got[0].body["ops"], want) {
		t.Fatalf("held = %#v, want %#v", got, want)
	}
}

// Tern drops the oldest settled nodes under its retention budget and says
// so with a gone event: the member sends nothing more on them, and inserts
// before the next node Tern still has.
func TestEvictedNodesGetNoOps(t *testing.T) {
	h := ready(t)
	user := func(kind fe.OpKind, id string, at int, text string) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: id, Index: at, Node: fe.MarkdownText{Role: fe.RoleUser, Text: text}}
	}
	h.applyOps(user(fe.Insert, "a", 0, "one"), user(fe.Insert, "b", 1, "two"), user(fe.Insert, "c", 2, "three"))
	if !h.event(`{"ev":"gone","sf":"pig","ids":["n.a","n.b"]}`) {
		t.Fatal("gone event not taken")
	}
	ops := h.applyOps(
		user(fe.Update, "a", 0, "one more"),
		user(fe.Insert, "x", 0, "first"),
		user(fe.Remove, "b", 2, ""),
		user(fe.Update, "c", 2, "three more"),
	)
	want := jsonOps(t,
		[]any{"add", "n.x", "main", "n.c", map[string]any{"id": "n.x", "k": "card", "p": map[string]any{"role": "pig.user", "tone": "user"}, "c": []any{map[string]any{"id": "n.x.md", "k": "md", "p": map[string]any{"text": "first"}}}}},
		[]any{"settle", "n.x"},
		[]any{"text", "n.c.md", "append", " more"},
		[]any{"settle", "n.c"},
	)
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("after eviction = %#v, want %#v", ops, want)
	}
	if !reflect.DeepEqual(h.session.order, []string{"n.x", "n.a", "n.c"}) {
		t.Fatalf("order = %v", h.session.order)
	}
}

// PiG pads its lines with spaces to the columns it renders at; Tern's ansi
// block wraps at its own width, so the member drops the padding that draws
// nothing. Spaces under a background, reverse video, underline,
// strikethrough or overline draw and stay, the SGR state carries from line
// to line, and the escape sequences among the padding stay.
func TestLinesDropPaddingThatDrawsNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"plain", []string{"plain   "}, "plain"},
		{"only spaces", []string{"   ", "x"}, "\nx"},
		{"inner spaces stay", []string{"a  b  "}, "a  b"},
		{"SGR after the cut", []string{"\x1b[2mdim\x1b[22;39m    "}, "\x1b[2mdim\x1b[22;39m"},
		{"escapes among the padding", []string{"x \x1b[1m \x1b[0m  "}, "x\x1b[1m\x1b[0m"},
		{"truecolor background", []string{"▀\x1b[48;2;72;163;129m  \x1b[0m   "}, "▀\x1b[48;2;72;163;129m  \x1b[0m"},
		{"256 background", []string{"\x1b[48;5;236mx  \x1b[49m  "}, "\x1b[48;5;236mx  \x1b[49m"},
		{"colon background", []string{"\x1b[48:2::1:2:3mx  \x1b[m "}, "\x1b[48:2::1:2:3mx  \x1b[m"},
		{"basic and bright backgrounds", []string{"x\x1b[41m \x1b[103m \x1b[0m "}, "x\x1b[41m \x1b[103m \x1b[0m"},
		{"background reset", []string{"\x1b[44mx\x1b[49m  "}, "\x1b[44mx\x1b[49m"},
		{"foreground arguments are not attributes", []string{"\x1b[38;2;7;48;4mx  ", "\x1b[38;5;7mx  "}, "\x1b[38;2;7;48;4mx\n\x1b[38;5;7mx"},
		{"reverse", []string{"\x1b[7mrev \x1b[27m  "}, "\x1b[7mrev \x1b[27m"},
		{"underline", []string{"\x1b[4mu  \x1b[24m ", "\x1b[4:3mc \x1b[4:0m "}, "\x1b[4mu  \x1b[24m\n\x1b[4:3mc \x1b[4:0m"},
		{"strikethrough and overline", []string{"x\x1b[9m \x1b[29m ", "x\x1b[53m \x1b[55m "}, "x\x1b[9m \x1b[29m\nx\x1b[53m \x1b[55m"},
		{"state across lines", []string{"\x1b[41mred ", "  ", "\x1b[0m  "}, "\x1b[41mred \n  \n\x1b[0m"},
		{"wide characters", []string{"日本  ", "日本\x1b[1m  \x1b[0m"}, "日本\n日本\x1b[1m\x1b[0m"},
		{"link", []string{"\x1b]8;;https://x\x1b\\link  \x1b]8;;\x1b\\   "}, "\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := ready(t)
			ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "l", Node: fe.Lines{Lines: tc.lines}})
			want := jsonOps(t, []any{"add", "n.l", "main", nil, map[string]any{"id": "n.l", "k": "ansi", "p": map[string]any{"text": tc.want, "hidden": false}}})
			if !reflect.DeepEqual(ops, want) {
				t.Fatalf("%q: ops = %#v, want %#v", tc.lines, ops, want)
			}
		})
	}
	// The dock's lines, tool renderings and overlays draw the same text.
	h := ready(t)
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"\x1b[33m[Context]\x1b[39m   "}}})
	if got := props(node(ops, "dock.dock")); !reflect.DeepEqual(got, map[string]any{"key": "dock", "text": "\x1b[33m[Context]\x1b[39m"}) {
		t.Fatalf("dock lines = %#v", got)
	}
	tool := (&session{}).mainNode("n.t", fe.ToolCard{Name: "read", Output: "raw  ", Result: []string{"rendered  "}})
	if got := tool.C[0].P["text"]; got != "rendered" {
		t.Fatalf("tool rendering = %q", got)
	}
	if got := linesText(nil); got != "" {
		t.Fatalf("no lines = %q", got)
	}
}
