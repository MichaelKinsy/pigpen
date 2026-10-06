package frontend

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

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
		{"unset", map[string]string{}, false},
		{"opted out", map[string]string{"TERM_PROGRAM": "tern", "PIG_TUI_NATIVE": "0"}, false},
		{"TSP off", map[string]string{"TERM_PROGRAM": "tern", "TERN_TSP": "0"}, false},
		{"tmux", map[string]string{"TERM_PROGRAM": "tern", "TMUX": "/tmp/tmux"}, false},
		{"screen", map[string]string{"TERM_PROGRAM": "tern", "STY": "1.pts"}, false},
		{"zellij", map[string]string{"TERM_PROGRAM": "tern", "ZELLIJ": "0"}, false},
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
			if s != nil {
				if s.(*session).timer != nil {
					t.Fatal("hello timeout started before PiG reads input")
				}
				_ = s.Close()
			}
		})
	}
}

// The SDK says hello (with a DA1 request, which tells a terminal without TSP
// from a slow one) and opens the surface once Tern answers. The first frame
// adds the regions with the dock's nodes, and the transcript's raw ops follow
// under the main region the view created.
func TestHelloOpensTheSurfaceAndTheFirstFrameAddsTheRegions(t *testing.T) {
	h := newHarness(t)
	out := h.out.take()
	got := parse(t, out)
	if len(got) != 1 || got[0].verb != "q" || got[0].body["app"] != "pig" || got[0].body["ver"] != "test" {
		t.Fatalf("hello = %#v", got)
	}
	if features := got[0].body["features"]; !reflect.DeepEqual(features, []any{"edit", "undo", "send"}) {
		t.Fatalf("features = %#v", features)
	}
	if !strings.HasSuffix(out, "\x1b[c") {
		t.Fatalf("hello without the DA1 request: %q", out)
	}
	h.reply(`,"credits":2`)
	if got := h.messages(); len(got) != 1 || got[0].verb != "o" || got[0].body["id"] != surfaceID || got[0].body["mode"] != "inline" || got[0].body["role"] != "pig.session" {
		t.Fatalf("open = %#v", got)
	}
	h.session.InputReady()
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"a"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"x"}}},
	)
	var ids []any
	for _, op := range ops {
		ids = append(ids, op.([]any)[1])
	}
	if !reflect.DeepEqual(ids, []any{"main", "dock", "layer", "n.a"}) {
		t.Fatalf("first frames' ops = %v, want the regions, then the transcript", ids)
	}
	if dock := node(ops, "dock.dock"); dock == nil || props(dock)["text"] != "a" {
		t.Fatalf("dock = %#v", ops[1])
	}
	if !reflect.DeepEqual(ops[3], jsonOps(t, []any{"add", "n.a", "main", nil, map[string]any{"id": "n.a", "k": "ansi", "p": map[string]any{"text": "x", "hidden": false}}})[0]) {
		t.Fatalf("transcript op = %#v", ops[3])
	}
}

// Tern acknowledges no frame while the pane is hidden, so an agent started in
// a background tab gets two frames to Tern: nothing goes out before PiG's
// first frame after InputReady, which makes the editor sendable and so
// releases the agent's prompt. This replays PiG's startup as recorded in
// Tern: the hello reply arrives before PiG's first frame, startup draws once
// more, then the input loop starts and the editor turns sendable.
func TestHiddenFromStartTheSendableFrameFitsTheCredits(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":2`)
	h.messages()
	editor := func(kind fe.OpKind, sendable bool) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionDock, ID: "editor", Node: fe.Editor{Sendable: sendable}}
	}
	h.apply(editor(fe.Insert, false))
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "resources", Node: fe.Lines{Lines: []string{"loaded"}}})
	h.session.InputReady()
	if got := h.messages(); len(got) != 0 {
		t.Fatalf("before PiG's first frame after InputReady = %#v", got)
	}
	h.apply(editor(fe.Update, true))
	got := h.frames()
	if len(got) == 0 || len(got) > 2 {
		t.Fatalf("startup frames without an ack = %d, want one or two", len(got))
	}
	all, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := json.Marshal(got[0].body)
	if !strings.Contains(string(first), `"sendable":true`) || !strings.Contains(string(all), `"n.resources"`) {
		t.Fatalf("frames = %s, want the sendable flip in the first and the startup transcript", all)
	}
	// Later frames wait for an ack; the second one carries them.
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "later", Node: fe.Lines{Lines: []string{"x"}}})
	if got := h.frames(); len(got) != 0 {
		t.Fatalf("sent without a credit: %#v", got)
	}
}

// A hello reply that arrives after PiG's first frame after InputReady opens
// the surface and sends everything at once.
func TestLateHelloReplySendsTheStartupFrames(t *testing.T) {
	h := newHarness(t)
	h.session.InputReady()
	h.apply(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Node: fe.Editor{Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"x"}}},
	)
	h.reply(`,"credits":2`)
	h.eventually("the startup frames", func() bool { return strings.Count(h.out.peek(), "\x1b_tsp;f;") == 2 })
	data, _ := json.Marshal(h.frames())
	if !strings.Contains(string(data), `"sendable":true`) || !strings.Contains(string(data), `"n.a"`) {
		t.Fatalf("frames = %s", data)
	}
}

// Tern acknowledges frames it drew. The SDK keeps at most credits frames
// unacknowledged: a render meanwhile waits in the SDK, which sends the latest
// view's difference on the ack, and the raw ops fold in the member and
// follow.
func TestApplyHonorsCreditsAndFoldsWhileWaiting(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":2`)
	h.session.InputReady()
	// The render adds the editor, and its cursor and the keys follow.
	h.apply(editorOp(fe.Insert, fe.Editor{}))
	if got := h.frames(); len(got) != 2 || got[1].body["s"] != float64(2) {
		t.Fatalf("first frames = %#v", got)
	}
	editor := func(placeholder, text string) fe.Op {
		return editorOp(fe.Update, fe.Editor{Placeholder: placeholder, Text: text})
	}
	md := func(kind fe.OpKind, text string) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: "m", Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: text, Streaming: true}}
	}
	h.apply(editor("c", "x"), md(fe.Insert, "a"))
	h.apply(editor("d", "xy"), md(fe.Update, "ab"))
	h.apply(md(fe.Update, "abc"))
	h.apply(md(fe.Update, "abcd"))
	if got := h.frames(); len(got) != 0 {
		t.Fatalf("sent without a credit: %#v", got)
	}
	if len(h.session.pending) != 3 {
		t.Fatalf("pending = %#v, want the editor's text, the add and one folded append", h.session.pending)
	}
	h.event(`{"ev":"ack","sf":"pig","s":2}`)
	h.synced()
	var ops []any
	for _, f := range h.frames() {
		ops = append(ops, f.body["ops"].([]any)...)
	}
	want := jsonOps(t,
		[]any{"set", "dock.editor", map[string]any{"placeholder": "d"}},
		[]any{"text", "dock.editor", "replace", "xy"},
		[]any{"add", "n.m", "main", nil, map[string]any{"id": "n.m", "k": "md", "p": map[string]any{"text": "a", "stream": true, "role": "pig.assistant"}}},
		[]any{"text", "n.m", "append", "bcd"},
	)
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("after the ack = %#v, want %#v", ops, want)
	}
}

func TestInsertPlacesNodesBeforeTheirNextSibling(t *testing.T) {
	h := ready(t)
	lines := fe.Lines{Lines: []string{"x"}}
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Index: 0, Node: lines},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "c", Index: 1, Node: lines},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.ToolCard{Name: "bash", Arguments: map[string]any{"command": "ls"}, Header: "$ ls", Status: fe.ToolRunning}},
		fe.Op{Kind: fe.Remove, Region: fe.RegionMain, ID: "a", Index: 0},
	)
	befores := []any{ops[0].([]any)[3], ops[1].([]any)[3], ops[2].([]any)[3]}
	if !reflect.DeepEqual(befores, []any{nil, nil, "n.c"}) {
		t.Fatalf("before siblings = %#v", befores)
	}
	tool := ops[2].([]any)[4].(map[string]any)
	if p := props(tool); tool["k"] != "tool" || p["targetKind"] != "command" || p["target"] != "ls" {
		t.Fatalf("tool node = %#v", tool)
	}
	if !reflect.DeepEqual(ops[3], []any{"del", "n.a"}) {
		t.Fatalf("remove = %#v", ops[3])
	}
	if !reflect.DeepEqual(h.session.order, []string{"n.b", "n.c"}) {
		t.Fatalf("order = %v", h.session.order)
	}
}

// The view's main is an empty list: a render after the transcript grew
// touches only the dock, never the main region or the raw nodes under it.
func TestRenderLeavesTheTranscriptAlone(t *testing.T) {
	h := ready(t)
	h.applyOps(editorOp(fe.Insert, fe.Editor{Placeholder: "a"}))
	h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Index: 0, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "hi"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.Lines{Lines: []string{"x"}}},
	)
	for _, text := range []string{"b", "c"} {
		renders := h.session.renders
		ops := h.applyOps(editorOp(fe.Update, fe.Editor{Placeholder: text}))
		if want := jsonOps(t, []any{"set", "dock.editor", map[string]any{"placeholder": text}}); !reflect.DeepEqual(ops, want) || h.session.renders != renders+1 {
			t.Fatalf("dock render = %#v, want only the dock's change", ops)
		}
	}
}

func TestMissingHelloReplyFallsBackOnce(t *testing.T) {
	h := newHarness(t)
	h.messages()
	if h.timeout != nil {
		t.Fatal("hello timeout started before PiG reads input")
	}
	h.session.InputReady()
	h.timeout()
	h.eventually("the fallback", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.fallbacks) > 0 })
	h.timeout()
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"x"}}})
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.fallbacks, []string{"Tern did not answer the surface protocol hello"}) {
		t.Fatalf("fallbacks = %q", h.fallbacks)
	}
	if got := h.messages(); len(got) != 0 {
		t.Fatalf("after fallback = %#v, want nothing: no surface was opened", got)
	}
}

// A reply that PiG queued during startup reaches HandleInput only once its
// input loop runs, which InputReady announces; either order connects.
func TestHelloReplyCancelsTheFallback(t *testing.T) {
	for _, replyFirst := range []bool{false, true} {
		h := newHarness(t)
		if replyFirst {
			h.reply("")
			h.session.InputReady()
			if h.timeout != nil {
				t.Fatal("a timeout started after the reply")
			}
		} else {
			h.session.InputReady()
			h.reply("")
			h.timeout()
		}
		h.apply()
		if err := h.session.Close(); err != nil {
			t.Fatal(err)
		}
		if len(h.fallbacks) != 0 {
			t.Fatalf("fallbacks = %q", h.fallbacks)
		}
		if got := h.messages(); got[len(got)-1].verb != "x" || got[len(got)-1].body["keep"] != true {
			t.Fatalf("close = %#v", got)
		}
	}
}

// After a fallback, Close removes the surface, so PiG's ANSI renderer shows.
func TestGoneSurfaceFallsBackAndCloseRemovesIt(t *testing.T) {
	h := ready(t)
	h.event(`{"ev":"gone","sf":"pig","ids":["pig"]}`)
	h.eventually("the fallback", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.fallbacks) > 0 })
	if !reflect.DeepEqual(h.fallbacks, []string{"Tern closed the surface"}) {
		t.Fatalf("fallbacks = %q", h.fallbacks)
	}
	h2 := ready(t)
	h2.session.fallback("test")
	if err := h2.session.Close(); err != nil {
		t.Fatal(err)
	}
	if got := h2.messages(); len(got) != 1 || got[0].verb != "x" || got[0].body["keep"] != false {
		t.Fatalf("close after fallback = %#v", got)
	}
}

// Close writes the surface's last frame and its close before it returns,
// and does not wait out the SDK's input drain: PiG drains the terminal
// itself, so the wait would only delay its exit.
func TestCloseWritesEverythingWithoutWaitingForTheDrain(t *testing.T) {
	h := ready(t)
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"x"}}})
	h.messages()
	h.session.mu.Lock()
	h.session.credits = 0
	h.session.mu.Unlock()
	h.apply(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"y"}}})
	start := time.Now()
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took >= tern.DrainDelay {
		t.Fatalf("Close took %v, at least the drain's %v", took, tern.DrainDelay)
	}
	got := h.messages()
	if len(got) != 2 || got[0].verb != "f" || got[1].verb != "x" || got[1].body["keep"] != true {
		t.Fatalf("close = %#v, want the held frame, then the close", got)
	}
}

// HandleInput claims Tern's replies and events and the DA1 answer the
// hello asked for; keys, other APCs and PiG's own DA1 answers stay PiG's.
func TestHandleInputTakesOnlyTSP(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		data string
		want bool
	}{
		{"a", false},
		{"\x1b[A", false},
		{"\x1b", false},
		{"\x1b_Gi=1;OK\x1b\\", false},
		{"\x1b[?62;22c", false}, // before the hello reply: PiG's own query's
		{"\x1b_tsp;e;{\"ev\":\"resize\",\"cols\":80}\x1b\\", true},
		{"\x1b]877;tsp;e;{\"ev\":\"theme\",\"dark\":true}\x07", true},
		{"\x1b_tsp;e;not json\x1b\\", true},
		{"\x1b_tsp;r;{\"r\":\"hello\",\"v\":1}\x1b\\", true},
		{"\x1b[?62;22c", true},  // the hello's
		{"\x1b[?62;22c", false}, // PiG's again
	} {
		if got := h.session.HandleInput(tc.data); got != tc.want {
			t.Fatalf("HandleInput(%q) = %v", tc.data, got)
		}
	}
	h.eventually("the surface", func() bool { return strings.Contains(h.out.peek(), "\x1b_tsp;o;") })
}

func TestBlankLinesBecomeHiddenNodes(t *testing.T) {
	for text, want := range map[string]bool{"": true, "  \n ": true, "\x1b[0m \x1b[49m": true, "x": false, "\x1b[31mx\x1b[0m": false} {
		if got := blank(text); got != want {
			t.Fatalf("blank(%q) = %v", text, got)
		}
	}
}

// Lines must fit Tern's layout: main at the Reader measure (720px), the dock
// inside the regions' 16px padding, both following resize events, which
// HandleInput reads before PiG asks.
func TestColumnsFollowTernLayout(t *testing.T) {
	h := newHarness(t)
	if main, dock := h.session.Columns(); main != 0 || dock != 0 {
		t.Fatalf("before any width = %d, %d", main, dock)
	}
	h.reply(`,"cols":137,"cell":{"w":8,"h":16}`)
	if main, dock := h.session.Columns(); main != 90 || dock != 133 {
		t.Fatalf("hello widths = %d, %d", main, dock)
	}
	h.event(`{"ev":"resize","cols":60,"cell":{"w":10,"h":20},"visible":true}`)
	if main, dock := h.session.Columns(); main != 56 || dock != 56 {
		t.Fatalf("narrow pane widths = %d, %d", main, dock)
	}
	h.event(`{"ev":"resize","cols":100,"cell":{"w":7.5,"h":15},"visible":true}`)
	if main, dock := h.session.Columns(); main != 95 || dock != 95 {
		t.Fatalf("fractional cell widths = %d, %d", main, dock)
	}
}
