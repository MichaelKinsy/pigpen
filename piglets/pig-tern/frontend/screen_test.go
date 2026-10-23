package frontend

import (
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// cutColumns cuts at cells, carries the SGR sequences before a cut into the
// parts after it, and keeps a wide character that spans a cut in the part it
// starts in.
func TestCutColumnsCarriesSGRAndKeepsWideCharacters(t *testing.T) {
	for _, tc := range []struct {
		line, left, middle, right string
		from, to                  int
	}{
		{line: "\x1b[31mab\x1b[1mcd\x1b[0me", from: 1, to: 3,
			left: "\x1b[31ma", middle: "\x1b[31mb\x1b[1mc", right: "\x1b[31m\x1b[1md\x1b[0me"},
		// A reset ends the state the parts after it carry.
		{line: "\x1b[31ma\x1b[0mb\x1b[32mc", from: 1, to: 2, left: "\x1b[31ma\x1b[0m", middle: "b\x1b[32m", right: "\x1b[32mc"},
		// 界 takes cells 1 and 2: the cut at 2 leaves it in the middle part.
		{line: "a界b", from: 1, to: 2, left: "a", middle: "界", right: "b"},
		// 界 takes two cells, so the cell after it is the third.
		{line: "界ab", from: 2, to: 3, left: "界", middle: "a", right: "b"},
		// A cut at 2 that 界 starts at puts it right of the cut.
		{line: "ab界c", from: 0, to: 2, left: "", middle: "ab", right: "界c"},
		// A combining mark takes no cell and stays with its base.
		{line: "e\u0301x", from: 1, to: 2, left: "e\u0301", middle: "x", right: ""},
		// An OSC 8 link's escape takes no cell.
		{line: "\x1b]8;;http://x\x1b\\ab\x1b]8;;\x1b\\c", from: 1, to: 2,
			left: "\x1b]8;;http://x\x1b\\a", middle: "b\x1b]8;;\x1b\\", right: "c"},
	} {
		left, middle, right := cutColumns(tc.line, tc.from, tc.to)
		if left != tc.left || middle != tc.middle || right != tc.right {
			t.Errorf("cutColumns(%q, %d, %d) = %q, %q, %q; want %q, %q, %q", tc.line, tc.from, tc.to, left, middle, right, tc.left, tc.middle, tc.right)
		}
	}
}

// Lines with a Click area draw as the lines above and below the area and a
// row of the cells left of it, the area, which takes a click, and the rest,
// sized in cells; Tern's click on the area is PiG's click on its first cell.
func TestClickLinesDrawTheAreaAsItsOwnClickableNode(t *testing.T) {
	h := ready(t)
	lines := []string{"top", "\x1b[32m█▀\x1b[0m version", "\x1b[32m▄█\x1b[0m hints", "below   "}
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "hdr", Node: fe.Lines{Lines: lines, Click: &fe.Area{Row: 1, Column: 1, Rows: 2, Columns: 1}}})
	ansi := func(id, text string, extra map[string]any) map[string]any {
		p := map[string]any{"text": text, "hidden": false}
		maps.Copy(p, extra)
		return map[string]any{"id": id, "k": "ansi", "p": p}
	}
	cells := map[string]any{"w": "1ch"}
	want := jsonOps(t,
		[]any{"add", "n.hdr", "main", nil, map[string]any{"id": "n.hdr", "k": "col", "c": []any{
			ansi("n.hdr.above", "top", nil),
			map[string]any{"id": "n.hdr.area", "k": "row", "p": map[string]any{"align": "start"}, "c": []any{
				ansi("n.hdr.left", "\x1b[32m█\n▄", map[string]any{"min": cells, "max": cells}),
				ansi("n.hdr.hit", "\x1b[32m▀\n█\x1b[0m", map[string]any{"min": cells, "max": cells, "actions": map[string]any{"click": "click"}}),
				ansi("n.hdr.right", " version\n hints", map[string]any{"grow": 1}),
			}},
			ansi("n.hdr.below", "below", nil),
		}}},
	)
	if got := opsOn(ops, "n.hdr"); !reflect.DeepEqual(got, want) {
		t.Fatalf("click lines:\n got %v\nwant %v", got, want)
	}
	h.event(`{"ev":"action","sf":"pig","id":"n.hdr.hit","act":"click"}`)
	h.eventually("PiG's click", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.clicks) == 1
	})
	if !reflect.DeepEqual(h.clicks[0], fe.Click{Node: "hdr", Row: 1, Column: 1}) {
		t.Fatalf("click = %#v", h.clicks[0])
	}
	// A click on another node, on another surface or with another action is not PiG's.
	h.event(`{"ev":"action","sf":"pig","id":"n.hdr.left","act":"click"}`)
	h.event(`{"ev":"action","sf":"pig.screen","id":"n.hdr.hit","act":"click"}`)
	h.event(`{"ev":"action","sf":"pig","id":"n.hdr.hit","act":"copy"}`)
	h.synced()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clicks) != 1 {
		t.Fatalf("clicks = %#v", h.clicks)
	}
}

// fullscreen is a fullscreen overlay of lines that takes a click anywhere.
func fullscreen(lines ...string) fe.Overlay {
	return fe.Overlay{Lines: lines, Width: 10, Anchor: "top-left", Fullscreen: true, Click: &fe.Area{Rows: len(lines), Columns: 10}}
}

// screenFrames returns the screen surface's messages: its opens, frames and
// closes.
func screenMessages(msgs []message) []message {
	var out []message
	for _, m := range msgs {
		if m.body["sf"] == screenID || m.body["id"] == screenID {
			out = append(out, m)
		}
	}
	return out
}

// A fullscreen overlay opens a screen surface over the inline one with a node
// per line, sends only the lines that change, takes a click anywhere as
// PiG's click on the overlay, and is removed with its overlay; the inline
// surface's layer never draws it.
func TestFullscreenOverlayShowsOnAScreenSurface(t *testing.T) {
	h := ready(t)
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: fullscreen("a", "b")})
	msgs := h.messages()
	screen := screenMessages(msgs)
	if len(screen) != 2 || screen[0].verb != "o" || screen[1].verb != "f" {
		t.Fatalf("screen messages = %v", screen)
	}
	if want := map[string]any{"id": screenID, "mode": "screen", "title": "pig", "role": screenID}; !reflect.DeepEqual(screen[0].body, want) {
		t.Fatalf("open = %v", screen[0].body)
	}
	// The lines are one ansi node: Tern virtualizes main's blocks, and a node
	// per line left lines with many colors undrawn.
	want := jsonOps(t, []any{"add", "main", screenID, nil, map[string]any{"id": "main", "k": "col", "c": []any{
		map[string]any{"id": "main.screen", "k": "ansi", "p": map[string]any{"key": "screen", "text": "a\nb", "actions": map[string]any{"click": "click"}}},
	}}}, []any{"add", "dock", screenID, nil, map[string]any{"id": "dock", "k": "col"}})
	if got := screen[1].body["ops"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("first screen frame:\n got %v\nwant %v", got, want)
	}
	for _, m := range msgs {
		if m.verb == "f" && m.body["sf"] == surfaceID && strings.Contains(fmt.Sprint(m.body), "overlay.1") {
			t.Fatalf("the inline surface drew the overlay: %v", m.body)
		}
	}

	h.apply(fe.Op{Kind: fe.Update, Region: fe.RegionOverlay, ID: "overlay.1", Node: fullscreen("a", "B")})
	screen = screenMessages(h.messages())
	if len(screen) != 1 || !reflect.DeepEqual(screen[0].body["ops"], jsonOps(t, []any{"text", "main.screen", "replace", "a\nB"})) {
		t.Fatalf("a changed line = %v", screen)
	}

	h.event(`{"ev":"action","sf":"pig.screen","id":"main.screen","act":"click"}`)
	h.eventually("PiG's click", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.clicks) == 1
	})
	if !reflect.DeepEqual(h.clicks[0], fe.Click{Node: "overlay.1"}) {
		t.Fatalf("click = %#v", h.clicks[0])
	}

	h.apply(fe.Op{Kind: fe.Remove, Region: fe.RegionOverlay, ID: "overlay.1"})
	screen = screenMessages(h.messages())
	if len(screen) != 1 || screen[0].verb != "x" || !reflect.DeepEqual(screen[0].body, map[string]any{"id": screenID, "keep": false}) {
		t.Fatalf("removing the overlay = %v", screen)
	}
}

// While the pane is hidden, the screen surface sends nothing, and Screen
// reports the view hidden; once it shows, the latest lines go out in one
// frame.
func TestScreenHoldsFramesWhileHidden(t *testing.T) {
	h := ready(t)
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: fullscreen("a", "b")})
	h.messages()
	h.event(`{"ev":"visible","sf":"pig.screen","visible":false}`)
	if screen, ok := h.session.Screen(); !ok || !screen.Hidden {
		t.Fatalf("hidden screen = %#v, %v", screen, ok)
	}
	h.apply(fe.Op{Kind: fe.Update, Region: fe.RegionOverlay, ID: "overlay.1", Node: fullscreen("A", "b")})
	h.apply(fe.Op{Kind: fe.Update, Region: fe.RegionOverlay, ID: "overlay.1", Node: fullscreen("A", "B")})
	if got := h.frames(); len(got) != 0 {
		t.Fatalf("frames while hidden: %v", got)
	}
	h.event(`{"ev":"visible","sf":"pig.screen","visible":true}`)
	screen := screenMessages(h.messages())
	if len(screen) != 1 || !reflect.DeepEqual(screen[0].body["ops"], jsonOps(t, []any{"text", "main.screen", "replace", "A\nB"})) {
		t.Fatalf("frames on show = %v", screen)
	}
}

// Screen reports the pane less the screen surface's padding in rows, main's
// column on the Reader measure, and Tern's reduced motion from the hello and
// from motion events; it reports nothing before the surface opens.
func TestScreenReportsThePaneAndReducedMotion(t *testing.T) {
	h := newHarness(t)
	if _, ok := h.session.Screen(); ok {
		t.Fatal("a screen before the surface opened")
	}
	h.reply(`,"credits":64,"cols":150,"cell":{"w":8,"h":16},"reduceMotion":true`)
	h.session.InputReady()
	h.apply()
	screen, ok := h.session.Screen()
	// 6px of main's top padding and the 80px dock are 86px, six 16px rows.
	if want := (fe.Screen{Columns: -1, Rows: -6, MainColumn: 28, ShowsDock: true, ReduceMotion: true}); !ok || screen != want {
		t.Fatalf("screen = %#v, %v; want %#v", screen, ok, want)
	}
	h.event(`{"ev":"motion","reduce":false}`)
	h.event(`{"ev":"resize","sf":"pig","cols":150,"cell":{"w":8,"h":10},"visible":true}`)
	if screen, _ = h.session.Screen(); screen.ReduceMotion || screen.Rows != -9 {
		t.Fatalf("after motion and resize = %#v", screen)
	}
	h.event(`{"ev":"resize","sf":"pig","cols":150,"cell":{"w":8,"h":24},"visible":true}`)
	if screen, _ = h.session.Screen(); screen.Rows != -4 {
		t.Fatalf("tall cells = %#v", screen)
	}
	h.event(`{"ev":"resize","sf":"pig","cols":150,"cell":{"w":8,"h":16},"visible":true}`)
	// Two more lines of draft are 32px, and a working indicator a 24px strip: 142px, nine rows.
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Node: fe.Editor{Text: "a\nb\nc"}})
	if screen, _ = h.session.Screen(); screen.Rows != -8 {
		t.Fatalf("a three-line draft = %#v", screen)
	}
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "working", Node: fe.Working{Kind: fe.WorkingAgent, Message: "Working"}})
	if screen, _ = h.session.Screen(); screen.Rows != -9 {
		t.Fatalf("a working indicator = %#v", screen)
	}
}

// Only the inline surface's frames count against its credits: a screen
// surface's frames, chunked or not, have acks of their own.
func TestTermWriterCountsOnlyTheInlineSurfacesFrames(t *testing.T) {
	w := &termWriter{w: io.Discard}
	for _, msg := range []string{
		"\x1b_tsp;f;{\"sf\":\"pig.screen\",\"s\":1,\"ops\":[]}\x1b\\",
		"\x1b_tsp;f;c=a;m=1;{\"sf\":\"pig.screen\",\"s\":2\x1b\\",
		"\x1b_tsp;o;{\"id\":\"pig\"}\x1b\\",
		"\x1b_tsp;f;{\"sf\":\"pig\",\"s\":1,\"ops\":[]}\x1b\\",
		"\x1b_tsp;f;c=b;m=1;{\"sf\":\"pig\",\"s\":2\x1b\\\x1b_tsp;f;c=b;,\"ops\":[]}\x1b\\",
	} {
		if _, err := w.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.frames.Load(); got != 2 {
		t.Fatalf("frames = %d, want the inline surface's 2", got)
	}
}

// Lending the terminal out removes the screen surface; it opens again with
// the overlay's latest lines after Resume.
func TestSuspendRemovesTheScreenSurface(t *testing.T) {
	h := ready(t)
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: fullscreen("a")})
	h.messages()
	h.session.Suspend()
	msgs := h.messages()
	closes := slices.IndexFunc(msgs, func(m message) bool { return m.verb == "x" && m.body["id"] == screenID })
	inline := slices.IndexFunc(msgs, func(m message) bool { return m.verb == "x" && m.body["id"] == surfaceID })
	if closes < 0 || inline < 0 || closes > inline || !reflect.DeepEqual(msgs[closes].body, map[string]any{"id": screenID, "keep": false}) {
		t.Fatalf("suspend = %v", msgs)
	}
	h.session.Resume()
	screen := screenMessages(h.messages())
	if len(screen) != 2 || screen[0].verb != "o" || screen[1].verb != "f" {
		t.Fatalf("resume = %v", screen)
	}
}

// The screen surface's dock mirrors the inline one with current props: the
// draft shows in an editor that is neither focused nor sendable, and a click
// on a mirrored node, or the focus a click asks for, is a click on the
// overlay; the elements of a mirrored view take no click of their own. A
// change of the dock reaches the mirror.
func TestScreenMirrorsTheDock(t *testing.T) {
	h := ready(t)
	h.apply(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Node: fe.Editor{Text: "draft", Cursor: 5, Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: fe.Footer{Model: "faux-1", Cwd: "/w"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "w", Index: 2, Node: fe.Lines{Lines: []string{"now"}, View: &fe.View{Root: fe.ViewNode{Kind: fe.ViewKindText, Text: "now", Rows: 1, Width: 20}, Seq: 1}}},
	)
	h.messages()
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: fullscreen("a")})
	var ops []any
	for _, m := range screenMessages(h.messages()) {
		if m.verb == "f" {
			ops = append(ops, m.body["ops"].([]any)...)
		}
	}
	d := shownBy(ops)
	editor := d["dock.editor"]
	if editor == nil || editor["text"] != "draft" || editor["cursor"] != float64(5) || editor["sendable"] != nil || !reflect.DeepEqual(editor["actions"], map[string]any{"click": "click"}) {
		t.Fatalf("mirrored editor = %v (shown %v)", editor, d)
	}
	if footer := d["dock.footer"]; footer == nil || !reflect.DeepEqual(footer["actions"], map[string]any{"click": "click"}) {
		t.Fatalf("mirrored footer = %v", footer)
	}
	if w, text := d["dock.w"], d["dock.w.0"]; w == nil || !reflect.DeepEqual(w["actions"], map[string]any{"click": "click"}) || text == nil || text["actions"] != nil {
		t.Fatalf("mirrored view = %v / %v", w, text)
	}
	for _, op := range ops {
		if op.([]any)[0] == "focus" {
			t.Fatalf("the screen surface focuses: %v", op)
		}
	}
	h.event(`{"ev":"action","sf":"pig.screen","id":"dock.footer","act":"click"}`)
	h.event(`{"ev":"focus","sf":"pig.screen","id":"dock.editor"}`)
	h.eventually("PiG's clicks", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.clicks) == 2
	})
	if !reflect.DeepEqual(h.clicks, []fe.Click{{Node: "overlay.1"}, {Node: "overlay.1"}}) {
		t.Fatalf("clicks = %#v", h.clicks)
	}
	h.apply(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Node: fe.Editor{Text: "draft!", Cursor: 6}})
	ops = nil
	for _, m := range screenMessages(h.messages()) {
		ops = append(ops, m.body["ops"].([]any)...)
	}
	if !slices.ContainsFunc(ops, func(op any) bool { o := op.([]any); return o[1] == "dock.editor" }) {
		t.Fatalf("the mirror missed the draft's change: %v", ops)
	}
}

// compactSGR keeps only the sequences that change how the next character
// draws, so a line that sets and resets one color around every cell is
// drawn the same from far less text.
func TestCompactSGRDropsSequencesThatChangeNothing(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"\x1b[31ma\x1b[0m\x1b[31mb\x1b[0m\x1b[31mc\x1b[0m", "\x1b[31mabc\x1b[0m"},
		{"\x1b[31ma\x1b[0m\x1b[32mb\x1b[0m", "\x1b[31ma\x1b[0m\x1b[32mb\x1b[0m"},
		// Sequences that add to the state go out as they are.
		{"\x1b[31ma\x1b[1mb", "\x1b[31ma\x1b[1mb"},
		// A sequence that repeats the state changes nothing.
		{"\x1b[31ma\n\x1b[31mb", "\x1b[31ma\nb"},
		// A newline keeps the state.
		{"\x1b[31ma\x1b[0m\n\x1b[31mb", "\x1b[31ma\nb"},
		// A reset before plain text goes out.
		{"\x1b[31ma\x1b[0m b", "\x1b[31ma\x1b[0m b"},
		// Other escapes stay where they are.
		{"\x1b]8;;http://x\x1b\\a\x1b]8;;\x1b\\", "\x1b]8;;http://x\x1b\\a\x1b]8;;\x1b\\"},
		{"plain", "plain"},
	} {
		if got := compactSGR(tc.in); got != tc.want {
			t.Errorf("compactSGR(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
