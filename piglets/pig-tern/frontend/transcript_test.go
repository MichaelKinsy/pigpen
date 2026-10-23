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
	node := mainNode("n.t", fe.ToolCard{Name: "bash", Status: fe.ToolCancelled, Output: "Operation aborted"})
	if status := node.P["status"]; status != "cancelled" {
		t.Fatalf("status = %v", status)
	}
}

// Assistant text is an md node that streams by text append while its source
// only grows; a rewrite replaces it, and the end of streaming turns the
// caret off.
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
		{md("Rewritten.", false), jsonOps(t, []any{"text", "n.n1.0", "append", "."}, []any{"set", "n.n1.0", map[string]any{"stream": false}})},
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

// A user message is an md node in a user-toned card; editing its text
// patches the md child.
func TestUserMarkdownIsACard(t *testing.T) {
	h := ready(t)
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u", Node: fe.MarkdownText{Role: fe.RoleUser, Text: "Fix it"}})
	card := ops[0].([]any)[4].(map[string]any)
	child := card["c"].([]any)[0].(map[string]any)
	if card["k"] != "card" || props(card)["tone"] != "user" || child["k"] != "md" || props(child)["text"] != "Fix it" {
		t.Fatalf("user node = %#v", card)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "u", Node: fe.MarkdownText{Role: fe.RoleUser, Text: "Fix it now"}})
	if !reflect.DeepEqual(ops, jsonOps(t, []any{"text", "n.u.md", "append", " now"})) {
		t.Fatalf("user update = %#v", ops)
	}
}

// A node whose kind changes is replaced in place, before its next sibling;
// so is a message whose role changes.
func TestKindChangeReplacesTheNode(t *testing.T) {
	h := ready(t)
	h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Index: 0, Node: fe.Lines{Lines: []string{"x"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.Lines{Lines: []string{"y"}}},
	)
	ops := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Index: 0, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "x"}})
	if len(ops) != 2 || !reflect.DeepEqual(ops[0], []any{"del", "n.a"}) || ops[1].([]any)[0] != "add" || ops[1].([]any)[3] != "n.b" {
		t.Fatalf("kind change = %#v", ops)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Index: 0, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "x"}})
	if len(ops) != 2 || !reflect.DeepEqual(ops[0], []any{"del", "n.a"}) || ops[1].([]any)[4].(map[string]any)["k"] != "card" {
		t.Fatalf("role change = %#v", ops)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.Lines{Lines: []string{"", " "}}})
	if !reflect.DeepEqual(ops, jsonOps(t, []any{"text", "n.b", "replace", "\n "}, []any{"set", "n.b", map[string]any{"hidden": true}})) {
		t.Fatalf("lines update = %#v", ops)
	}
}

// A thinking run is a muted collapsible card whose md child streams by
// append; hiding thinking folds the card.
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
	)
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("thinking update = %#v", ops)
	}
}

// A tool card's status and output change as a set and a text append; when a
// result carrying a diff arrives, the card is replaced by one with a Tern
// diff child and a diff stat.
func TestToolCardPatchesAndDiffReplaces(t *testing.T) {
	h := ready(t)
	patch := "Index: a.go\n===\n--- a.go\n+++ a.go\n@@ -1,2 +1,2 @@\n-old\n+new\n same\n"
	running := fe.ToolCard{Name: "edit", Arguments: map[string]any{"path": "a.go"}, Status: fe.ToolRunning, Output: "a"}
	h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t", Node: running})
	more := running
	more.Output = "ab"
	more.Status = fe.ToolDone
	ops := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "t", Node: more})
	if !reflect.DeepEqual(ops, jsonOps(t, []any{"set", "n.t", map[string]any{"status": "done"}}, []any{"text", "n.t.out", "append", "b"})) {
		t.Fatalf("tool update = %#v", ops)
	}
	done := more
	done.Diff = &fe.Diff{Path: "a.go", Text: patch}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "t", Node: done})
	if len(ops) != 2 || !reflect.DeepEqual(ops[0], []any{"del", "n.t"}) {
		t.Fatalf("diff arrival = %#v", ops)
	}
	tool := ops[1].([]any)[4].(map[string]any)
	child := tool["c"].([]any)[0].(map[string]any)
	if props(tool)["meta"].([]any)[0] != "+1 −1" || child["k"] != "diff" || props(child)["text"] != patch || props(child)["path"] != "a.go" {
		t.Fatalf("diff tool = %#v", tool)
	}
}
