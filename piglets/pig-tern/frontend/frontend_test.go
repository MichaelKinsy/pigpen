package frontend

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

type message struct {
	verb string
	body map[string]any
}

// messages splits terminal output into TSP messages.
func messages(t *testing.T, out *bytes.Buffer) []message {
	t.Helper()
	var got []message
	for _, part := range strings.Split(out.String(), "\x1b_tsp;")[1:] {
		part = strings.TrimSuffix(part, "\x1b\\")
		verb, body, _ := strings.Cut(part, ";")
		var decoded map[string]any
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			t.Fatalf("message %q: %v", part, err)
		}
		got = append(got, message{verb, decoded})
	}
	out.Reset()
	return got
}

type harness struct {
	session   *session
	out       *bytes.Buffer
	fallbacks []string
	timeout   func()
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{out: &bytes.Buffer{}}
	h.session = newSession(h.out, func(reason string) { h.fallbacks = append(h.fallbacks, reason) }, func(_ time.Duration, fn func()) *time.Timer {
		h.timeout = fn
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		return timer
	})
	if err := h.session.start("test"); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) event(body string) bool {
	return h.session.HandleInput("\x1b_tsp;e;" + body + "\x1b\\")
}

func env(vars map[string]string) fe.Env {
	return fe.Env{Out: &bytes.Buffer{}, Getenv: func(key string) string { return vars[key] }, Fallback: func(string) {}}
}

func TestOpenRunsOnlyDirectlyInTern(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars map[string]string
		want bool
	}{
		{"tern", map[string]string{"TERM_PROGRAM": "tern"}, true},
		{"other terminal", map[string]string{"TERM_PROGRAM": "iTerm.app"}, false},
		{"opted out", map[string]string{"TERM_PROGRAM": "tern", "PIG_TUI_NATIVE": "0"}, false},
		{"tmux", map[string]string{"TERM_PROGRAM": "tern", "TMUX": "/tmp/tmux"}, false},
		{"herdr", map[string]string{"TERM_PROGRAM": "tern", "HERDR_ENV": "1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := member{}.Open(env(tc.vars))
			if err != nil {
				t.Fatal(err)
			}
			if (s != nil) != tc.want {
				t.Fatalf("session = %v, want %v", s, tc.want)
			}
			if s != nil && s.(*session).timer != nil {
				t.Fatal("hello timeout started before PiG reads input")
			}
		})
	}
}

func TestStartSendsHelloAndSurfaceAndTheFirstFrameAddsTheRegions(t *testing.T) {
	h := newHarness(t)
	got := messages(t, h.out)
	if len(got) != 2 || got[0].verb != "q" || got[1].verb != "o" {
		t.Fatalf("start = %#v", got)
	}
	if got[0].body["app"] != "pig" || got[1].body["mode"] != "inline" || got[1].body["id"] != surfaceID {
		t.Fatalf("hello/open = %#v", got)
	}
	ops := applyOps(t, h, fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"a"}}})
	if len(ops) != 3 || ops[0].([]any)[1] != "main" || ops[1].([]any)[1] != "dock" || ops[2].([]any)[1] != "n.dock" {
		t.Fatalf("first frame ops = %#v, want the main and dock regions, then the node", ops)
	}
}

// Tern acknowledges no frame while the pane is hidden, so an agent started in
// a background tab gets two frames to Tern: its first tree and the frame that
// makes the editor sendable, which releases the agent's prompt. This replays
// PiG's startup as recorded in Tern: the hello reply arrives before PiG's
// first frame, startup draws once more, then the input loop starts and the
// editor turns sendable.
func TestHiddenFromStartTheSendableFrameFitsTheCredits(t *testing.T) {
	h := newHarness(t)
	h.session.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"credits\":2}\x1b\\")
	if got := messages(t, h.out); len(got) != 2 {
		t.Fatalf("before PiG's first frame = %#v, want only the hello and the surface", got)
	}
	editor := func(kind fe.OpKind, sendable bool) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionDock, ID: "editor", Node: fe.Editor{Sendable: sendable}}
	}
	frames := []fe.Frame{
		{Ops: []fe.Op{editor(fe.Insert, false)}},
		{Ops: []fe.Op{{Kind: fe.Insert, Region: fe.RegionMain, ID: "resources", Node: fe.Lines{Lines: []string{"loaded"}}}}},
	}
	for _, frame := range frames {
		if err := h.session.Apply(frame); err != nil {
			t.Fatal(err)
		}
	}
	h.session.InputReady()
	if err := h.session.Apply(fe.Frame{Ops: []fe.Op{editor(fe.Update, true)}}); err != nil {
		t.Fatal(err)
	}
	got := messages(t, h.out)
	if len(got) != 2 {
		t.Fatalf("startup frames without an ack = %#v, want two", got)
	}
	ops, err := json.Marshal(got[1].body["ops"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ops), `"n.resources"`) || !strings.Contains(string(ops), `"sendable":true`) {
		t.Fatalf("second frame = %s, want the later startup node and the sendable flip", ops)
	}
}

// Tern acknowledges frames it drew; a session keeps at most credits frames
// unacknowledged, folds what arrives meanwhile, and sends it on the ack.
func TestApplyHonorsCreditsAndFoldsWhileWaiting(t *testing.T) {
	h := newHarness(t)
	h.session.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"credits\":2}\x1b\\")
	h.session.InputReady()
	messages(t, h.out)
	dock := func(text string) fe.Frame {
		return fe.Frame{Ops: []fe.Op{{Kind: fe.Update, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{text}}}}}
	}
	if err := h.session.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"a"}}}}}); err != nil {
		t.Fatal(err)
	}
	if got := messages(t, h.out); len(got) != 1 || got[0].body["s"] != float64(1) {
		t.Fatalf("first frame = %#v", got)
	}
	if err := h.session.Apply(dock("b")); err != nil {
		t.Fatal(err)
	}
	if got := messages(t, h.out); len(got) != 1 || got[0].body["s"] != float64(2) {
		t.Fatalf("second frame = %#v", got)
	}
	for _, text := range []string{"c", "d"} {
		if err := h.session.Apply(dock(text)); err != nil {
			t.Fatal(err)
		}
	}
	if got := messages(t, h.out); len(got) != 0 {
		t.Fatalf("sent without a credit: %#v", got)
	}
	if !h.event(`{"ev":"ack","sf":"pig","s":2}`) {
		t.Fatal("ack not consumed")
	}
	got := messages(t, h.out)
	if len(got) != 1 {
		t.Fatalf("after ack = %#v", got)
	}
	ops := got[0].body["ops"].([]any)
	// PiG's dock node is "dock", the TSP region's id; the node must not
	// address the region. Its hidden prop never changed, so no set goes out.
	want := []any{
		[]any{"text", "n.dock", "replace", "d"},
	}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("folded ops = %#v", ops)
	}
}

func TestInsertPlacesNodesBeforeTheirNextSibling(t *testing.T) {
	h := ready(t)
	lines := fe.Lines{Lines: []string{"x"}}
	if err := h.session.Apply(fe.Frame{Ops: []fe.Op{
		{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Index: 0, Node: lines},
		{Kind: fe.Insert, Region: fe.RegionMain, ID: "c", Index: 1, Node: lines},
		{Kind: fe.Insert, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.ToolCard{Name: "bash", Arguments: map[string]any{"command": "ls"}, Header: "$ ls", Status: fe.ToolRunning}},
		{Kind: fe.Remove, Region: fe.RegionMain, ID: "a", Index: 0},
	}}); err != nil {
		t.Fatal(err)
	}
	ops := messages(t, h.out)[0].body["ops"].([]any)
	befores := []any{ops[0].([]any)[3], ops[1].([]any)[3], ops[2].([]any)[3]}
	if !reflect.DeepEqual(befores, []any{nil, nil, "n.c"}) {
		t.Fatalf("before siblings = %#v", befores)
	}
	tool := ops[2].([]any)[4].(map[string]any)
	if props := tool["p"].(map[string]any); tool["k"] != "tool" || props["targetKind"] != "command" || props["target"] != "ls" {
		t.Fatalf("tool node = %#v", tool)
	}
	if !reflect.DeepEqual(ops[3], []any{"del", "n.a"}) {
		t.Fatalf("remove = %#v", ops[3])
	}
	if !reflect.DeepEqual(h.session.order[fe.RegionMain], []string{"n.b", "n.c"}) {
		t.Fatalf("order = %v", h.session.order[fe.RegionMain])
	}
}

func TestMissingHelloReplyFallsBackOnceAndRemovesTheSurface(t *testing.T) {
	h := newHarness(t)
	messages(t, h.out)
	if h.timeout != nil {
		t.Fatal("hello timeout started before PiG reads input")
	}
	h.session.InputReady()
	h.timeout()
	h.timeout()
	if len(h.fallbacks) != 1 {
		t.Fatalf("fallbacks = %q", h.fallbacks)
	}
	if err := h.session.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"x"}}}}}); err != nil {
		t.Fatal(err)
	}
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	got := messages(t, h.out)
	if len(got) != 1 || got[0].verb != "x" || got[0].body["keep"] != false {
		t.Fatalf("after fallback = %#v", got)
	}
}

// A reply that PiG queued during startup reaches HandleInput only once its
// input loop runs, which InputReady announces.
func TestHelloReplyCancelsTheFallback(t *testing.T) {
	for _, replyFirst := range []bool{false, true} {
		h := newHarness(t)
		reply := func() { h.session.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1}\x1b\\") }
		if replyFirst {
			reply()
			h.session.InputReady()
			if h.timeout != nil {
				t.Fatal("a timeout started after the reply")
			}
		} else {
			h.session.InputReady()
			reply()
			h.timeout()
		}
		if len(h.fallbacks) != 0 {
			t.Fatalf("fallbacks = %q", h.fallbacks)
		}
		messages(t, h.out)
		if err := h.session.Close(); err != nil {
			t.Fatal(err)
		}
		if got := messages(t, h.out); got[len(got)-1].verb != "x" || got[len(got)-1].body["keep"] != true {
			t.Fatalf("close = %#v", got)
		}
	}
}

func TestGoneSurfaceFallsBack(t *testing.T) {
	h := newHarness(t)
	h.event(`{"ev":"gone","sf":"pig","ids":["pig"]}`)
	if !reflect.DeepEqual(h.fallbacks, []string{"Tern closed the surface"}) {
		t.Fatalf("fallbacks = %q", h.fallbacks)
	}
}

func TestHandleInputTakesOnlyTSP(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		data string
		want bool
	}{
		{"a", false},
		{"\x1b[A", false},
		{"\x1b_Gi=1;OK\x1b\\", false},
		{"\x1b_tsp;e;{\"ev\":\"resize\",\"cols\":80}\x1b\\", true},
		{"\x1b]877;tsp;e;{\"ev\":\"ack\",\"s\":1}\x07", true},
	} {
		if got := h.session.HandleInput(tc.data); got != tc.want {
			t.Fatalf("HandleInput(%q) = %v", tc.data, got)
		}
	}
	if h.session.acked != 1 {
		t.Fatalf("OSC 877 ack not applied: acked = %d", h.session.acked)
	}
}

func TestBlankLinesBecomeHiddenNodes(t *testing.T) {
	for text, want := range map[string]bool{"": true, "  \n ": true, "\x1b[0m \x1b[49m": true, "x": false, "\x1b[31mx\x1b[0m": false} {
		if got := blank(text); got != want {
			t.Fatalf("blank(%q) = %v", text, got)
		}
	}
}

// Lines must fit Tern's layout: main at the Reader measure (720px), the dock
// inside the regions' 16px padding, both following resize events.
func TestColumnsFollowTernLayout(t *testing.T) {
	h := newHarness(t)
	if main, dock := h.session.Columns(); main != 0 || dock != 0 {
		t.Fatalf("before any width = %d, %d", main, dock)
	}
	h.session.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"cols\":137,\"cell\":{\"w\":8,\"h\":16}}\x1b\\")
	if main, dock := h.session.Columns(); main != 90 || dock != 133 {
		t.Fatalf("hello widths = %d, %d", main, dock)
	}
	h.event(`{"ev":"resize","cols":60,"cell":{"w":10,"h":20},"visible":true}`)
	if main, dock := h.session.Columns(); main != 56 || dock != 56 {
		t.Fatalf("narrow pane widths = %d, %d", main, dock)
	}
}

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
	node := tspNode("n.t", fe.ToolCard{Name: "bash", Status: fe.ToolCancelled, Output: "Operation aborted"})
	if status := node["p"].(map[string]any)["status"]; status != "cancelled" {
		t.Fatalf("status = %v", status)
	}
}

// ready returns a harness whose hello is answered and whose regions went out
// with an empty first frame, so the next Apply goes out at once and holds
// only its own ops.
func ready(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.session.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"credits\":8}\x1b\\")
	h.session.InputReady()
	if err := h.session.Apply(fe.Frame{}); err != nil {
		t.Fatal(err)
	}
	messages(t, h.out)
	return h
}

// applyOps applies one frame and returns the TSP ops it sent.
func applyOps(t *testing.T, h *harness, ops ...fe.Op) []any {
	t.Helper()
	if err := h.session.Apply(fe.Frame{Ops: ops}); err != nil {
		t.Fatal(err)
	}
	got := messages(t, h.out)
	if len(got) != 1 {
		t.Fatalf("frames = %#v", got)
	}
	return got[0].body["ops"].([]any)
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
	ops := applyOps(t, h, insert)
	want := []any{"add", "n.n1.0", "main", nil, map[string]any{"id": "n.n1.0", "k": "md", "p": map[string]any{"text": "Looking", "stream": true, "role": "pig.assistant"}}}
	if !reflect.DeepEqual(ops, []any{want}) {
		t.Fatalf("insert = %#v", ops)
	}
	for _, tc := range []struct {
		op   fe.Op
		want []any
	}{
		{md("Looking at **it**", true), []any{[]any{"text", "n.n1.0", "append", " at **it**"}}},
		{md("Rewritten", true), []any{[]any{"text", "n.n1.0", "replace", "Rewritten"}}},
		{md("Rewritten.", false), []any{[]any{"text", "n.n1.0", "append", "."}, []any{"set", "n.n1.0", map[string]any{"stream": false}}}},
	} {
		if got := applyOps(t, h, tc.op); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: ops = %#v, want %#v", tc.op.Node.(fe.MarkdownText).Text, got, tc.want)
		}
	}
}

// Appends that arrive while Tern holds no credit fold into one.
func TestPendingAppendsFold(t *testing.T) {
	h := newHarness(t)
	h.session.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"credits\":1}\x1b\\")
	h.session.InputReady()
	if err := h.session.Apply(fe.Frame{}); err != nil {
		t.Fatal(err)
	}
	h.event(`{"ev":"ack","sf":"pig","s":1}`)
	messages(t, h.out)
	op := func(kind fe.OpKind, text string) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: "m", Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: text, Streaming: true}}
	}
	applyOps(t, h, op(fe.Insert, "a"))
	for _, text := range []string{"ab", "abc", "abcd"} {
		if err := h.session.Apply(fe.Frame{Ops: []fe.Op{op(fe.Update, text)}}); err != nil {
			t.Fatal(err)
		}
	}
	h.event(`{"ev":"ack","sf":"pig","s":2}`)
	got := messages(t, h.out)
	if len(got) != 1 || !reflect.DeepEqual(got[0].body["ops"], []any{[]any{"text", "n.m", "append", "bcd"}}) {
		t.Fatalf("folded = %#v", got)
	}
}

// A user message is an md node in a user-toned card; editing its text
// patches the md child.
func TestUserMarkdownIsACard(t *testing.T) {
	h := ready(t)
	ops := applyOps(t, h, fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u", Node: fe.MarkdownText{Role: fe.RoleUser, Text: "Fix it"}})
	card := ops[0].([]any)[4].(map[string]any)
	child := card["c"].([]any)[0].(map[string]any)
	if card["k"] != "card" || card["p"].(map[string]any)["tone"] != "user" || child["k"] != "md" || child["p"].(map[string]any)["text"] != "Fix it" {
		t.Fatalf("user node = %#v", card)
	}
	ops = applyOps(t, h, fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "u", Node: fe.MarkdownText{Role: fe.RoleUser, Text: "Fix it now"}})
	if !reflect.DeepEqual(ops, []any{[]any{"text", "n.u.md", "append", " now"}}) {
		t.Fatalf("user update = %#v", ops)
	}
}

// A node whose kind changes is replaced in place, before its next sibling.
func TestKindChangeReplacesTheNode(t *testing.T) {
	h := ready(t)
	applyOps(t, h,
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Index: 0, Node: fe.Lines{Lines: []string{"x"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.Lines{Lines: []string{"y"}}},
	)
	ops := applyOps(t, h, fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Index: 0, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "x"}})
	if len(ops) != 2 || !reflect.DeepEqual(ops[0], []any{"del", "n.a"}) || ops[1].([]any)[0] != "add" || ops[1].([]any)[3] != "n.b" {
		t.Fatalf("kind change = %#v", ops)
	}
}

// A thinking run is a muted collapsible card whose md child streams by
// append; hiding thinking folds the card.
func TestThinkingIsACollapsibleCard(t *testing.T) {
	h := ready(t)
	op := func(kind fe.OpKind, thinking fe.Thinking) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: "a.0", Node: thinking}
	}
	ops := applyOps(t, h, op(fe.Insert, fe.Thinking{Text: "Reading", Streaming: true}))
	card := ops[0].([]any)[4].(map[string]any)
	props := card["p"].(map[string]any)
	if card["k"] != "card" || props["tone"] != "muted" || props["collapsible"] != true || props["collapsed"] != false || props["head"] != "Thinking" {
		t.Fatalf("thinking card = %#v", card)
	}
	if child := card["c"].([]any)[0].(map[string]any); child["k"] != "md" || child["p"].(map[string]any)["stream"] != true {
		t.Fatalf("thinking body = %#v", child)
	}
	ops = applyOps(t, h, op(fe.Update, fe.Thinking{Text: "Reading it", Hidden: true}))
	want := []any{
		[]any{"set", "n.a.0", map[string]any{"collapsed": true}},
		[]any{"text", "n.a.0.md", "append", " it"},
		[]any{"set", "n.a.0.md", map[string]any{"stream": false}},
	}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("thinking update = %#v", ops)
	}
}

// An edit whose result carries a diff draws it as a Tern diff child with a
// diff stat; when the result arrives, the running card's output child is
// replaced by the diff.
func TestToolDiffIsATernDiff(t *testing.T) {
	h := ready(t)
	patch := "Index: a.go\n===\n--- a.go\n+++ a.go\n@@ -1,2 +1,2 @@\n-old\n+new\n same\n"
	running := fe.ToolCard{Name: "edit", Arguments: map[string]any{"path": "a.go"}, Status: fe.ToolRunning}
	applyOps(t, h, fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t", Node: running})
	done := running
	done.Status = fe.ToolDone
	done.Diff = &fe.Diff{Path: "a.go", Text: patch}
	ops := applyOps(t, h, fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "t", Node: done})
	if len(ops) != 2 || !reflect.DeepEqual(ops[0], []any{"del", "n.t"}) {
		t.Fatalf("diff arrival = %#v", ops)
	}
	tool := ops[1].([]any)[4].(map[string]any)
	child := tool["c"].([]any)[0].(map[string]any)
	if tool["p"].(map[string]any)["meta"].([]any)[0] != "+1 −1" || child["k"] != "diff" || child["p"].(map[string]any)["text"] != patch || child["p"].(map[string]any)["path"] != "a.go" {
		t.Fatalf("diff tool = %#v", tool)
	}
}

// The hello declares that the session applies native edits, undo and sends.
func TestHelloDeclaresEditingFeatures(t *testing.T) {
	h := newHarness(t)
	got := messages(t, h.out)
	if features := got[0].body["features"]; !reflect.DeepEqual(features, []any{"edit", "undo", "send"}) {
		t.Fatalf("features = %#v", features)
	}
}

func editorOp(kind fe.OpKind, editor fe.Editor) fe.Op {
	return fe.Op{Kind: kind, Region: fe.RegionDock, ID: "editor", Index: 1, Node: editor}
}

// The editor is a Tern editor in the dock that takes the focus when added.
// Typing travels as a text change and a cursor set, and the sendable flag as
// a set of its own.
func TestEditorIsAFocusedTernEditor(t *testing.T) {
	h := ready(t)
	applyOps(t, h, fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"status"}}})
	ops := applyOps(t, h, editorOp(fe.Insert, fe.Editor{Text: "hi", Cursor: 2}))
	want := []any{
		[]any{"add", "n.editor", "dock", nil, map[string]any{"id": "n.editor", "k": "editor", "p": map[string]any{"text": "hi", "cursor": float64(2), "placeholder": "", "sendable": false}}},
		[]any{"focus", "n.editor"},
	}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("insert = %#v", ops)
	}
	ops = applyOps(t, h, editorOp(fe.Update, fe.Editor{Text: "hi!", Cursor: 3}))
	want = []any{[]any{"text", "n.editor", "append", "!"}, []any{"set", "n.editor", map[string]any{"cursor": float64(3)}}}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("typing = %#v", ops)
	}
	ops = applyOps(t, h, editorOp(fe.Update, fe.Editor{Text: "hi!", Cursor: 3, Sendable: true}))
	if want := []any{[]any{"set", "n.editor", map[string]any{"sendable": true}}}; !reflect.DeepEqual(ops, want) {
		t.Fatalf("sendable = %#v", ops)
	}
}

type editorActions struct {
	edits []fe.Edit
	undos int
	sends []string
}

func withEditorActions(h *harness) *editorActions {
	a := &editorActions{}
	h.session.edit = func(edit fe.Edit) { a.edits = append(a.edits, edit) }
	h.session.undo = func() { a.undos++ }
	h.session.send = func(text string) { a.sends = append(a.sends, text) }
	return a
}

// Tern's edit, undo and send events on the editor become PiG's editor
// actions. An edit computed against text other than the editor's last text,
// in UTF-16 code units, is dropped, and so is an event for another node or
// surface, or one after the editor left the dock. A focus click on the
// editor gets the focus back.
func TestEditorEventsBecomeEditorActions(t *testing.T) {
	h := ready(t)
	a := withEditorActions(h)
	applyOps(t, h, fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"status"}}})
	applyOps(t, h, editorOp(fe.Insert, fe.Editor{Text: "héllo 😀", Cursor: 8}))

	h.event(`{"ev":"edit","sf":"pig","id":"n.editor","from":0,"to":5,"text":"hey","cursor":3,"len":8}`)
	h.event(`{"ev":"edit","sf":"pig","id":"n.editor","from":0,"to":5,"text":"stale","cursor":5,"len":7}`)
	h.event(`{"ev":"edit","sf":"pig","id":"n.dock","from":0,"to":0,"text":"x","cursor":1,"len":8}`)
	h.event(`{"ev":"edit","sf":"other","id":"n.editor","from":0,"to":0,"text":"x","cursor":1,"len":8}`)
	h.event(`{"ev":"undo","sf":"pig","id":"n.editor"}`)
	h.event(`{"ev":"send","sf":"pig","id":"n.editor","text":"/model\nsecond line"}`)
	if want := []fe.Edit{{From: 0, To: 5, Text: "hey", Cursor: 3, Len: 8}}; !reflect.DeepEqual(a.edits, want) {
		t.Fatalf("edits = %#v", a.edits)
	}
	if a.undos != 1 || !reflect.DeepEqual(a.sends, []string{"/model\nsecond line"}) {
		t.Fatalf("undos = %d, sends = %q", a.undos, a.sends)
	}
	messages(t, h.out)
	h.event(`{"ev":"focus","sf":"pig","id":"n.editor"}`)
	got := messages(t, h.out)
	if len(got) != 1 || !reflect.DeepEqual(got[0].body["ops"], []any{[]any{"focus", "n.editor"}}) {
		t.Fatalf("focus answer = %#v", got)
	}

	applyOps(t, h, fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "editor", Index: 1})
	h.event(`{"ev":"send","sf":"pig","id":"n.editor","text":"late"}`)
	h.event(`{"ev":"undo","sf":"pig","id":"n.editor"}`)
	if a.undos != 1 || len(a.sends) != 1 {
		t.Fatalf("after removal: undos = %d, sends = %q", a.undos, a.sends)
	}
}
