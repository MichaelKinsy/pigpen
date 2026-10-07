package frontend

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// wire is TSP messages one per line, as "verb body", the body as JSON with
// sorted keys, so a test states the exact messages.
func wire(t *testing.T, msgs []message) string {
	t.Helper()
	lines := make([]string, len(msgs))
	for i, m := range msgs {
		body, err := json.Marshal(m.body)
		if err != nil {
			t.Fatal(err)
		}
		lines[i] = m.verb + " " + string(body)
	}
	return strings.Join(lines, "\n")
}

func expectWire(t *testing.T, what string, msgs []message, want ...string) {
	t.Helper()
	if got := wire(t, msgs); got != strings.Join(want, "\n") {
		t.Fatalf("%s:\n got %s\nwant %s", what, strings.ReplaceAll(got, "\n", "\n     "), strings.Join(want, "\n     "))
	}
}

// handedOff readies a session showing a transcript node, the editor and a
// status line in the dock, acknowledges everything, and lends the terminal
// out.
func handedOff(t *testing.T) *harness {
	t.Helper()
	h := ready(t)
	h.apply(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"answer"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"status"}}},
		editorOp(fe.Insert, fe.Editor{Text: "draft", Cursor: 5, Sendable: true}),
	)
	h.ack()
	h.messages()
	h.session.Suspend()
	return h
}

// Lending the terminal out closes the surface, keeping the transcript in the
// pane: Tern then draws the other program on the grid and sends nothing
// about the surface. Taking it back adopts the surface: the transcript is
// not sent again, the dock comes back with the editor's current text and
// the keys, and the frames continue their sequence.
func TestSuspendClosesTheSurfaceAndResumeAdoptsIt(t *testing.T) {
	h := ready(t)
	h.apply(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"answer"}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"status"}}},
		editorOp(fe.Insert, fe.Editor{Text: "draft", Cursor: 5, Sendable: true}),
	)
	h.ack()
	h.messages()
	sent := h.session.out.frames.Load()

	h.session.Suspend()
	expectWire(t, "suspend", h.messages(), `x {"id":"pig","keep":true}`)
	h.session.Suspend()
	expectWire(t, "second suspend", h.messages())

	// The external editor changed the draft meanwhile; PiG's frame after
	// Resume carries only that change.
	h.session.Resume()
	expectWire(t, "resume", h.messages(),
		`o {"adopt":true,"id":"pig","mode":"inline","role":"pig.session","title":"pig"}`,
		`f {"ops":[["add","dock","pig",null,{"c":[{"id":"dock.dock","k":"ansi","p":{"key":"dock","text":"status"}},{"id":"dock.editor","k":"editor","p":{"key":"editor","sendable":true}}],"id":"dock","k":"col"}],["add","layer","pig",null,{"id":"layer","k":"col"}]],"s":`+itoa(sent+1)+`,"sf":"pig"}`,
		`f {"ops":[["text","dock.editor","replace","draft"],["set","dock.editor",{"cursor":5}],["focus","dock.editor"]],"s":`+itoa(sent+2)+`,"sf":"pig"}`,
	)
	h.apply(editorOp(fe.Update, fe.Editor{Text: "draft\nedited", Cursor: 12, Sendable: true}))
	expectWire(t, "the edit", h.messages(),
		`f {"ops":[["text","dock.editor","append","\nedited"],["set","dock.editor",{"cursor":12}]],"s":`+itoa(sent+3)+`,"sf":"pig"}`,
	)
	// Tern acknowledges the adopted surface's frames by their sequence.
	h.ack()
	h.session.mu.Lock()
	acked, adopting := h.session.acked, h.session.adopting
	h.session.mu.Unlock()
	if acked != sent+3 || adopting {
		t.Fatalf("acked = %d, adopting = %v; want %d, false", acked, adopting, sent+3)
	}
}

// What waits for credits when PiG lends the terminal out goes out before the
// close, so the pane shows the latest transcript meanwhile; nothing is left
// to send twice after Resume. Tern acknowledges none of the frames before
// the close, so the adopted surface has every credit.
func TestSuspendSendsWhatWaitsBeforeTheClose(t *testing.T) {
	h := ready(t)
	h.apply(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"x"}}},
		editorOp(fe.Insert, fe.Editor{}),
	)
	h.ack()
	h.messages()
	h.session.mu.Lock()
	h.session.credits = 0
	h.session.mu.Unlock()
	h.apply(
		fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"y"}}},
		editorOp(fe.Update, fe.Editor{Text: "z", Cursor: 1}),
	)
	expectWire(t, "held", h.messages())
	sent := h.session.out.frames.Load()
	h.session.Suspend()
	expectWire(t, "suspend", h.messages(),
		`f {"ops":[["text","n.a","replace","y"],["text","dock.editor","replace","z"],["set","dock.editor",{"cursor":1}]],"s":`+itoa(sent+1)+`,"sf":"pig"}`,
		`x {"id":"pig","keep":true}`,
	)
	h.session.mu.Lock()
	h.session.credits = 2
	h.session.mu.Unlock()
	h.session.Resume()
	expectWire(t, "resume", h.messages(),
		`o {"adopt":true,"id":"pig","mode":"inline","role":"pig.session","title":"pig"}`,
		`f {"ops":[["add","dock","pig",null,{"c":[{"id":"dock.editor","k":"editor","p":{"key":"editor"}}],"id":"dock","k":"col"}],["add","layer","pig",null,{"id":"layer","k":"col"}]],"s":`+itoa(sent+2)+`,"sf":"pig"}`,
		`f {"ops":[["text","dock.editor","replace","z"],["set","dock.editor",{"cursor":1}],["focus","dock.editor"]],"s":`+itoa(sent+3)+`,"sf":"pig"}`,
	)
}

// While PiG lends the terminal out, the session writes nothing: the
// terminal progress keepalive pauses and reports again on Resume.
func TestSuspendPausesTheProgressKeepalive(t *testing.T) {
	h := ready(t)
	h.session.keepalive = time.Millisecond
	h.apply(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Working"}))
	h.eventually("a keepalive", func() bool { return strings.Count(h.out.peek(), progressActive) >= 2 })
	h.session.Suspend()
	h.out.take()
	time.Sleep(20 * time.Millisecond)
	if got := h.out.take(); got != "" {
		t.Fatalf("written while suspended: %q", got)
	}
	h.session.Resume()
	got := h.out.take()
	if !strings.HasPrefix(got, progressActive+"\x1b_tsp;o;") {
		t.Fatalf("resume = %q, want the progress, then the adopt", got)
	}
	h.eventually("the keepalive again", func() bool { return strings.Contains(h.out.peek(), progressActive) })
	h.apply(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "working"})
	h.eventually("the clear", func() bool { return strings.Contains(h.out.peek(), progressClear) })
}

// After a job-control stop, the shell's prompt discards the surface, and Tern
// answers the adopt with gone. The member opens a new surface and sends it
// the whole document from what it keeps: the palette, the regions, the
// transcript in order, the session file's node before it, and the dock with
// the editor's text and the keys. Its frames count from 1, and acks count
// them.
func TestAdoptAnsweredGoneOpensANewSurfaceWithTheWholeDocument(t *testing.T) {
	h := ready(t)
	file := "/tmp/s.jsonl"
	if err := h.session.Apply(fe.Frame{
		SessionFile: &file,
		Theme:       &fe.Theme{Name: "dark", Dark: true, Colors: map[string]string{"accent": "#8abeb7"}},
		Ops: []fe.Op{
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"one"}}},
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "two"}},
			editorOp(fe.Insert, fe.Editor{Text: "draft", Cursor: 5}),
		},
	}); err != nil {
		t.Fatal(err)
	}
	h.ack()
	h.messages()
	h.session.Suspend()
	h.session.Resume()
	h.messages()

	// Two credits, as Tern grants: the new surface's frames fit them.
	h.session.mu.Lock()
	h.session.credits = 2
	h.session.mu.Unlock()
	h.event(`{"ev":"gone","ids":["pig"]}`)
	h.eventually("the new surface", func() bool { return strings.Count(h.out.peek(), "\x1b_tsp;f;") == 2 })
	expectWire(t, "reopen", h.messages(),
		`o {"id":"pig","mode":"inline","role":"pig.session","title":"pig"}`,
		`t {"dark":{"accent":"#8abeb7"},"name":{"dark":"dark"},"sf":"pig"}`,
		`f {"ops":[["add","main","pig",null,{"id":"main","k":"col"}],["add","dock","pig",null,{"c":[{"id":"dock.editor","k":"editor","p":{"key":"editor"}}],"id":"dock","k":"col"}],["add","layer","pig",null,{"id":"layer","k":"col"}]],"s":1,"sf":"pig"}`,
		`f {"ops":[["add","n.a","main",null,{"id":"n.a","k":"ansi","p":{"hidden":false,"text":"one"}}],["add","n.b","main",null,{"id":"n.b","k":"md","p":{"role":"pig.assistant","stream":false,"text":"two"}}],["settle","n.a"],["settle","n.b"],["add","pig.session","main","n.a",{"id":"pig.session","k":"text","p":{"hidden":true,"href":"/tmp/s.jsonl","role":"pig.session","text":""}}],["text","dock.editor","replace","draft"],["set","dock.editor",{"cursor":5}],["focus","dock.editor"]],"s":2,"sf":"pig"}`,
	)
	h.session.mu.Lock()
	fellBack := h.session.fellBack
	h.session.mu.Unlock()
	if fellBack {
		t.Fatal("fell back")
	}
	// Tern counts the new surface's frames from 1.
	h.event(`{"ev":"ack","sf":"pig","s":2}`)
	h.synced()
	h.apply(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"one!"}}})
	// A patch clears the settle hint on the node, so it settles again.
	expectWire(t, "next frame", h.messages(), `f {"ops":[["text","n.a","append","!"],["settle","n.a"]],"s":3,"sf":"pig"}`)

	// Once Tern drew a frame of the adopted surface, a gone means the
	// surface went for good, and PiG's ANSI renderer takes over.
	h.event(`{"ev":"gone","sf":"pig","ids":["pig"]}`)
	h.eventually("the fallback", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.fallbacks) > 0 })
}

// A gone after an adopt Tern drew is not an answer to it.
func TestGoneAfterTheAdoptShowedFallsBack(t *testing.T) {
	h := handedOff(t)
	h.session.Resume()
	h.ack()
	h.messages()
	h.event(`{"ev":"gone","sf":"pig","ids":["pig"]}`)
	h.eventually("the fallback", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.fallbacks) == 1 })
	expectWire(t, "after gone", h.messages())
}

// A hello answered while PiG lends the terminal out opens no surface, which
// would cover the other program, until Resume; the hello timeout waits too.
func TestSuspendBeforeTheSurfaceOpensDefersIt(t *testing.T) {
	h := newHarness(t)
	h.session.InputReady()
	h.session.Suspend()
	h.session.mu.Lock()
	timer := h.session.timer
	h.session.mu.Unlock()
	if timer != nil {
		t.Fatal("the hello timeout runs while suspended")
	}
	h.out.take()
	if !h.session.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"credits\":64}\x1b\\") {
		t.Fatal("hello reply not taken")
	}
	h.eventually("the connection", func() bool { h.session.mu.Lock(); defer h.session.mu.Unlock(); return h.session.tsp != nil })
	expectWire(t, "suspended", h.messages())
	h.session.Resume()
	h.apply()
	expectWire(t, "resume", h.messages(),
		`o {"id":"pig","mode":"inline","role":"pig.session","title":"pig"}`,
		`f {"ops":[["add","main","pig",null,{"id":"main","k":"col"}],["add","dock","pig",null,{"id":"dock","k":"col"}],["add","layer","pig",null,{"id":"layer","k":"col"}]],"s":1,"sf":"pig"}`,
	)
}

// Close while the terminal is lent out writes nothing more: Suspend already
// closed the surface, keeping it. After a fallback, Close adopts it without
// listening and removes it, so PiG's ANSI renderer has the grid.
func TestCloseWhileSuspended(t *testing.T) {
	h := handedOff(t)
	h.messages()
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	expectWire(t, "close", h.messages())

	h2 := handedOff(t)
	h2.messages()
	h2.session.fallback("test")
	if err := h2.session.Close(); err != nil {
		t.Fatal(err)
	}
	expectWire(t, "close after fallback", h2.messages(),
		`o {"adopt":true,"id":"pig","listen":false,"mode":"inline"}`,
		`x {"id":"pig","keep":false}`,
	)
}

// A new surface after a handoff gets what Tern still had: nodes it evicted
// under its retention budget stay out, the session file's node goes before
// the first node it has, and every final node is settled again, so the new
// surface can be evicted too.
func TestReopenAfterEvictionSendsOnlyLiveNodesSettled(t *testing.T) {
	h := ready(t)
	file := "/tmp/s.jsonl"
	if err := h.session.Apply(fe.Frame{
		SessionFile: &file,
		Ops: []fe.Op{
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "one"}},
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "b", Index: 1, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "two"}},
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "c", Index: 2, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "three", Streaming: true}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	h.ack()
	h.event(`{"ev":"gone","sf":"pig","ids":["n.a"]}`)
	h.synced()
	h.messages()
	h.session.Suspend()
	h.session.Resume()
	h.messages()
	h.session.mu.Lock()
	h.session.credits = 2
	h.session.mu.Unlock()
	h.event(`{"ev":"gone","ids":["pig"]}`)
	h.eventually("the new surface", func() bool { return strings.Count(h.out.peek(), "\x1b_tsp;f;") == 2 })
	msgs := h.messages()
	last := msgs[len(msgs)-1]
	want := `f {"ops":[["add","n.b","main",null,{"id":"n.b","k":"md","p":{"role":"pig.assistant","stream":false,"text":"two"}}],["add","n.c","main",null,{"id":"n.c","k":"md","p":{"role":"pig.assistant","stream":true,"text":"three"}}],["settle","n.b"],["add","pig.session","main","n.b",{"id":"pig.session","k":"text","p":{"hidden":true,"href":"/tmp/s.jsonl","role":"pig.session","text":""}}]],"s":2,"sf":"pig"}`
	expectWire(t, "reopen after eviction", []message{last}, want)
}
