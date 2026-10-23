package frontend

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

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
	// Lines a settles once c follows it; the running tool b does not.
	befores := []any{ops[0].([]any)[3], ops[1].([]any)[3], ops[3].([]any)[3]}
	if !reflect.DeepEqual(befores, []any{nil, nil, "n.c"}) || !reflect.DeepEqual(ops[2], []any{"settle", "n.a"}) {
		t.Fatalf("ops = %#v", ops)
	}
	tool := ops[3].([]any)[4].(map[string]any)
	if p := props(tool); tool["k"] != "tool" || p["targetKind"] != "command" || p["target"] != "ls" {
		t.Fatalf("tool node = %#v", tool)
	}
	if len(ops) != 5 || !reflect.DeepEqual(ops[4], []any{"del", "n.a"}) {
		t.Fatalf("remove = %#v", ops[4:])
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
	// One fallback, and its reason tells the user which Tern setting makes Tern silent.
	if len(h.fallbacks) != 1 || !strings.Contains(h.fallbacks[0], "did not answer the surface protocol hello") || !strings.Contains(h.fallbacks[0], `"Native program surfaces"`) {
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
// hello asked for; keys, other APCs and PiG's own DA1 answers stay PiG's, as
// do the answers to PiG's terminal color query and Tern's light/dark reports,
// which PiG reads the terminal's appearance from (Tern 0.4.5's bytes).
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
		{"\x1b]10;rgb:3b3b/3b3b/3b3b\x07", false},
		{"\x1b]11;rgb:f8f8/f8f8/f8f8\x07", false},
		{"\x1b]4;15;rgb:a5a5/a5a5/a5a5\x07", false},
		{"\x1b[?62;22c", false}, // before the hello reply: PiG's own query's
		{"\x1b_tsp;e;{\"ev\":\"resize\",\"cols\":80}\x1b\\", true},
		{"\x1b]877;tsp;e;{\"ev\":\"theme\",\"dark\":true}\x07", true},
		{"\x1b_tsp;e;not json\x1b\\", true},
		{"\x1b_tsp;r;{\"r\":\"hello\",\"v\":1}\x1b\\", true},
		{"\x1b[?62;22c", true},  // the hello's
		{"\x1b[?62;22c", false}, // PiG's again
		{"\x1b[?997;1n", false},
		{"\x1b_tsp;e;{\"ev\":\"theme\",\"dark\":true}\x1b\\", true},
		{"\x1b]11;rgb:1515/1818/2020\x1b\\", false},
		{"\x1b[?997;2n", false},
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

// Lines must fit Tern's ansi blocks, which wrap at their width: the dock's
// hold Tern's cols, and main's fewer under every chat style. The widths
// follow the hello and resize events, which HandleInput reads before PiG
// asks. Each case is a geometry measured in Tern 0.4.5 (its pane width,
// font size, layout, style or face), with the columns its blocks held:
// main's under Reader, Spine and Console (0: not measured) and the dock's.
func TestColumnsFitTernBlocks(t *testing.T) {
	h := newHarness(t)
	if main, dock := h.session.Columns(); main != 0 || dock != 0 {
		t.Fatalf("before any width = %d, %d", main, dock)
	}
	h.reply(`,"cols":68,"cell":{"w":8,"h":16}`)
	// 560px at 13px: Reader's block held 63 columns, so the 64 the member
	// gave wrapped every padded line into a blank row.
	if main, dock := h.session.Columns(); main != 55 || dock != 67 {
		t.Fatalf("hello widths = %d, %d", main, dock)
	}
	for _, tc := range []struct {
		what                   string
		cols                   int
		cell                   string
		reader, spine, console int
		measuredDock, wantMain int
	}{
		{"480px 13px", 57, `{"w":8,"h":16}`, 53, 47, 54, 57, 44},
		{"560px 13px", 68, `{"w":8,"h":16}`, 63, 57, 65, 68, 55},
		{"640px 13px", 78, `{"w":8,"h":16}`, 74, 0, 0, 78, 64},
		{"760px 13px", 93, `{"w":8,"h":16}`, 89, 81, 90, 93, 78},
		{"1000px 13px", 124, `{"w":8,"h":16}`, 92, 103, 121, 124, 87},
		{"1400px 13px", 176, `{"w":8,"h":16}`, 92, 103, 131, 176, 87},
		{"560px 11px", 80, `{"w":7,"h":13}`, 76, 0, 0, 80, 66},
		{"1400px 11px", 208, `{"w":7,"h":13}`, 92, 0, 0, 208, 87},
		{"560px 12px", 73, `{"w":7,"h":14}`, 69, 0, 0, 73, 59},
		{"1400px 12px", 191, `{"w":7,"h":14}`, 92, 0, 0, 191, 87},
		{"560px 14px", 63, `{"w":8,"h":17}`, 59, 0, 0, 63, 50},
		{"1400px 14px", 163, `{"w":8,"h":17}`, 92, 0, 0, 163, 87},
		{"560px 15px", 58, `{"w":9,"h":18}`, 54, 0, 0, 58, 45},
		{"1400px 15px", 152, `{"w":9,"h":18}`, 92, 0, 0, 152, 87},
		{"560px 16px", 55, `{"w":10,"h":19}`, 51, 45, 0, 55, 42},
		{"1400px 16px", 143, `{"w":10,"h":19}`, 92, 0, 130, 143, 87},
		{"560px 17px", 51, `{"w":10,"h":20}`, 47, 0, 0, 51, 38},
		{"1400px 17px", 134, `{"w":10,"h":20}`, 92, 0, 0, 134, 87},
		{"560px 20px", 44, `{"w":12,"h":24}`, 40, 0, 0, 44, 31},
		{"1400px 20px", 114, `{"w":12,"h":24}`, 92, 0, 0, 114, 87},
		{"560px rail layout", 42, `{"w":8,"h":16}`, 38, 0, 0, 42, 29},
		{"560px float or studio layout", 69, `{"w":8,"h":16}`, 65, 0, 0, 69, 56},
		{"560px islands style", 66, `{"w":8,"h":16}`, 62, 0, 0, 66, 53},
		{"560px Menlo", 67, `{"w":8,"h":16}`, 63, 57, 0, 67, 54},
		{"560px Monaspace Neon", 65, `{"w":8,"h":16}`, 61, 55, 0, 65, 52},
		{"1400px Monaspace Neon", 170, `{"w":8,"h":16}`, 89, 0, 0, 170, 87},
	} {
		h.event(`{"ev":"resize","cols":` + itoa(int64(tc.cols)) + `,"cell":` + tc.cell + `,"visible":true}`)
		main, dock := h.session.Columns()
		if main != tc.wantMain || dock != tc.cols-1 {
			t.Fatalf("%s: widths = %d, %d, want %d, %d", tc.what, main, dock, tc.wantMain, tc.cols-1)
		}
		for _, held := range []int{tc.reader, tc.spine, tc.console} {
			if held > 0 && main > held {
				t.Fatalf("%s: main's %d columns wrap in a block of %d", tc.what, main, held)
			}
		}
		if dock > tc.measuredDock {
			t.Fatalf("%s: the dock's %d columns wrap in a block of %d", tc.what, dock, tc.measuredDock)
		}
	}
	// A pane too narrow for any chrome still gets a column.
	h.event(`{"ev":"resize","cols":1,"cell":{"w":8,"h":16},"visible":true}`)
	if main, dock := h.session.Columns(); main != 1 || dock != 1 {
		t.Fatalf("one column = %d, %d", main, dock)
	}
}

// Reader shows main's lines inside its 16px padding, centered on its 720px
// measure: at the column of their left edge, from the dock's, as measured in
// Tern 0.4.5 at 13px (dock region at x=16, a column 7.76px).
func TestReaderMainColumnIsWhereTernShowsMain(t *testing.T) {
	for _, tc := range []struct {
		what       string
		cols, want int
		mainX      float64
	}{
		{"480px", 57, 2, 32},
		{"560px", 68, 2, 32},
		{"1000px", 124, 15, 140},
		{"1400px", 176, 41, 340},
	} {
		got := readerMainColumn(tc.cols)
		if measured := int((tc.mainX - 16) / 7.76); got != tc.want || got != measured {
			t.Fatalf("%s: readerMainColumn(%d) = %d, want %d (measured %d)", tc.what, tc.cols, got, tc.want, measured)
		}
	}
}

// Tern draws nothing and acknowledges nothing while the pane is hidden, so
// the raw ops and the renders wait in the member, folding, and go out when
// it shows again. A render that turns the editor sendable still goes out
// while hidden, so a prompt sent to the hidden pane arrives.
func TestHiddenPaneHoldsFramesUntilShown(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":2`)
	h.session.InputReady()
	h.apply(editorOp(fe.Insert, fe.Editor{}))
	h.ack()
	h.messages()
	md := func(kind fe.OpKind, text string) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: "m", Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: text, Streaming: true}}
	}
	h.applyOps(md(fe.Insert, "Look"))
	h.ack()
	if !h.event(`{"ev":"visible","sf":"pig","visible":false}`) {
		t.Fatal("visible event not taken")
	}
	for _, text := range []string{"Look at", "Rewr", "Rewritten", "Rewritten it"} {
		h.apply(md(fe.Update, text))
	}
	h.apply(editorOp(fe.Update, fe.Editor{Text: "draft", Cursor: 5}))
	h.apply(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Working"}))
	h.apply(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "working"})
	h.apply(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Thinking"}))
	if got := h.frames(); len(got) != 0 {
		t.Fatalf("sent while hidden: %#v", got)
	}
	ops := h.applyOps(editorOp(fe.Update, fe.Editor{Text: "draft", Cursor: 5, Sendable: true}))
	if len(opsOn(ops, "dock.editor")) != 1 || !reflect.DeepEqual(opsOn(ops, "dock.editor")[0], []any{"set", "dock.editor", map[string]any{"sendable": true}}) || node(ops, "dock.working") == nil {
		t.Fatalf("render while hidden = %#v, want the sendable flip and the held structure", ops)
	}
	h.event(`{"ev":"send","sf":"pig","id":"dock.editor","text":"next"}`)
	h.synced()
	h.mu.Lock()
	sends := slices.Clone(h.sends)
	h.mu.Unlock()
	if !reflect.DeepEqual(sends, []string{"next"}) {
		t.Fatalf("sends = %#v", sends)
	}
	if !h.event(`{"ev":"visible","sf":"pig","visible":true}`) {
		t.Fatal("visible event not taken")
	}
	got := h.frames()
	want := jsonOps(t,
		[]any{"text", "n.m", "replace", "Rewritten it"},
		[]any{"text", "dock.editor", "replace", "draft"},
		[]any{"set", "dock.editor", map[string]any{"cursor": 5}},
	)
	if len(got) != 1 || !reflect.DeepEqual(got[0].body["ops"], want) {
		t.Fatalf("on show = %#v, want one frame %#v", got, want)
	}
}

// Appends held back fold into the pending op of the node without copying:
// the folded op appends from the same offset of the newest text.
func TestHeldAppendsShareTheNewestText(t *testing.T) {
	h := ready(t)
	md := func(kind fe.OpKind, text string) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionMain, ID: "m", Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: text, Streaming: true}}
	}
	h.applyOps(md(fe.Insert, "a"))
	h.event(`{"ev":"visible","sf":"pig","visible":false}`)
	// Each frame's text is a new string, as PiG's are.
	var text string
	for _, n := range []int{2, 4, 7} {
		text = strings.Clone("abcdefg"[:n])
		h.apply(md(fe.Update, text))
	}
	h.session.mu.Lock()
	pending := slices.Clone(h.session.pending)
	h.session.mu.Unlock()
	if len(pending) != 1 || pending[0][3] != "bcdefg" || unsafe.StringData(pending[0][3].(string)) != unsafe.StringData(text[1:]) {
		t.Fatalf("pending = %#v, want one append sharing the newest text", pending)
	}
}
