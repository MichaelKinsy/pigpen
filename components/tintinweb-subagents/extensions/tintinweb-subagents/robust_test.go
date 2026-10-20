package tintinweb_subagents

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// ---- H1: the viewer never shares a live map with the child's reader goroutine ----

// streaming feeds one assistant message into a uiAgent as the child's rpc stream would: start, many text deltas,
// end, from its own goroutine.
func streaming(u *uiAgent, deltas int, done chan<- struct{}) {
	defer close(done)
	u.observe(obj{"type": "message_start", "message": obj{"role": "assistant", "content": []any{}}}, nil)
	for i := range deltas {
		u.observe(obj{"type": "message_update", "assistantMessageEvent": obj{"type": "text_delta", "contentIndex": 0.0, "delta": fmt.Sprintf("word%d ", i)}}, nil)
	}
	u.observe(obj{"type": "message_end", "message": obj{"role": "assistant", "content": []any{obj{"type": "text", "text": "final"}}}}, nil)
}

func TestViewerRendersWhileTheChildStreams(t *testing.T) {
	u := newUIAgent(nil, nil)
	host := &fakeHost{rows: 30}
	v := newConversationViewer(host, u, mockRecordView(nil), nil, plainTheme(), func() {}, viewerOptions{})
	done := make(chan struct{})
	go streaming(u, 400, done)
	renders := 0
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		v.Render(80)
		v.HandleInput("k")
		renders++
	}
	if renders == 0 {
		t.Fatal("no render ran")
	}
	if got := strings.Join(v.Render(80), "\n"); !strings.Contains(got, "final") {
		t.Errorf("the final text is missing:\n%s", got)
	}
}

func TestMessagesAreCopiesWithStableIDs(t *testing.T) {
	u := newUIAgent(nil, nil)
	u.observe(obj{"type": "message_start", "message": obj{"role": "assistant", "content": []any{}}}, nil)
	u.observe(obj{"type": "message_update", "assistantMessageEvent": obj{"type": "text_delta", "contentIndex": 0.0, "delta": "he"}}, nil)
	first := u.Messages()
	if len(first) != 1 {
		t.Fatalf("%d messages", len(first))
	}
	id := first[0][viewerIDKey]
	if id == nil {
		t.Fatal("no id on the copy")
	}
	// A change the reader makes never shows in a copy already handed out.
	u.observe(obj{"type": "message_update", "assistantMessageEvent": obj{"type": "text_delta", "contentIndex": 0.0, "delta": "llo"}}, nil)
	eqStr(t, textOf(first[0]["content"]), "he")
	second := u.Messages()
	eqStr(t, textOf(second[0]["content"]), "hello")
	if second[0][viewerIDKey] != id {
		t.Errorf("the id changed: %v then %v", id, second[0][viewerIDKey])
	}
	// A reader's edit of a copy never reaches the live message: the next change copies the live one again.
	second[0]["content"].([]any)[0].(map[string]any)["text"] = "vandalised"
	u.observe(obj{"type": "message_update", "assistantMessageEvent": obj{"type": "text_delta", "contentIndex": 0.0, "delta": "!"}}, nil)
	eqStr(t, textOf(u.Messages()[0]["content"]), "hello!")
	// The same copy is reused while the message is unchanged (no per-render copying of a settled transcript).
	a, b := u.Messages(), u.Messages()
	if fmt.Sprintf("%p", a[0]) != fmt.Sprintf("%p", b[0]) {
		t.Error("an unchanged message was copied again")
	}
}

func TestStreamingKeepsOneMarkdownComponent(t *testing.T) {
	u := newUIAgent(nil, nil)
	v := newConversationViewer(&fakeHost{rows: 30}, u, mockRecordView(nil), nil, plainTheme(), func() {}, viewerOptions{})
	p := &markdownProbe{}
	p.install(v)
	u.observe(obj{"type": "message_start", "message": obj{"role": "assistant", "content": []any{}}}, nil)
	for i := range 5 {
		u.observe(obj{"type": "message_update", "assistantMessageEvent": obj{"type": "text_delta", "contentIndex": 0.0, "delta": fmt.Sprintf("part %d ", i)}}, nil)
		v.Render(80)
	}
	if p.constructions != 1 {
		t.Errorf("%d Markdown components for one streaming message", p.constructions)
	}
}

// ---- no panic leaves the extension ----

func TestCatchTurnsAPanicIntoAnErrorAndNotifiesOnce(t *testing.T) {
	var notices []string
	var mu sync.Mutex
	notice := func(m string) { mu.Lock(); notices = append(notices, m); mu.Unlock() }
	panicNotice.Store(&notice)
	t.Cleanup(func() { panicNotice.Store(nil); panicSeen = sync.Map{} })
	panicSeen = sync.Map{}
	for range 3 {
		err := catch("test place", func() { panic("boom") })
		if err == nil || !strings.Contains(err.Error(), "internal error in test place: boom") {
			t.Fatalf("err: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 || !strings.Contains(notices[0], "test place") {
		t.Errorf("the user is told once per place: %q", notices)
	}
}

func TestSafeGoAndAfterFuncSurvivePanics(t *testing.T) {
	var n atomic.Int32
	safeGo("goroutine", func() { n.Add(1); panic("in goroutine") })
	afterFunc(time.Millisecond, "timer", func() { n.Add(1); panic("in timer") })
	deadline := time.Now().Add(2 * time.Second)
	for n.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n.Load() != 2 {
		t.Fatalf("ran %d of 2", n.Load())
	}
	time.Sleep(20 * time.Millisecond) // had a panic escaped, the test binary would be gone
}

func TestTickerKeepsTickingAfterAPanic(t *testing.T) {
	var n atomic.Int32
	stop := startTicker(2*time.Millisecond, func() {
		if n.Add(1) == 1 {
			panic("first tick")
		}
	})
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	for n.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n.Load() < 3 {
		t.Fatalf("the ticker stopped after a panic: %d ticks", n.Load())
	}
}

// panickingSession is a viewer session whose reads panic.
type panickingSession struct{ subscribed atomic.Bool }

func (s *panickingSession) Messages() []map[string]any { panic("messages") }
func (s *panickingSession) Subscribe(func()) func()    { s.subscribed.Store(true); return func() {} }

func TestPanickingViewerNeitherKillsNorBlocksTheOverlay(t *testing.T) {
	ui := &ctxUI{widgets: map[string]*uiWidgetState{}}
	o := &overlayComponent{ui: ui}
	o.component = newConversationViewer(o, &panickingSession{}, mockRecordView(nil), nil, plainTheme(), o.close, viewerOptions{})
	lines := o.Render(80)
	if len(lines) != 1 || !strings.Contains(lines[0], "failed to render") {
		t.Fatalf("render fallback: %q", lines)
	}
	// The next frame still works (the viewer's lock was released by the panic).
	o.Render(80)
	res, err := o.HandleInput("j") // scrolling reads the transcript, which panics
	if err != nil || !res.Done {
		t.Errorf("a viewer that panicked on input is closed: %+v %v", res, err)
	}
	o.Dispose()
}

type panickingComponent struct{}

func (panickingComponent) Render(int) []string { panic("render") }
func (panickingComponent) HandleInput(string)  { panic("input") }
func (panickingComponent) Dispose()            { panic("dispose") }

func TestOverlayRecoversEveryComponentEntry(t *testing.T) {
	o := &overlayComponent{ui: &ctxUI{}, component: panickingComponent{}}
	o.Render(10)
	if res, _ := o.HandleInput("x"); !res.Done {
		t.Error("input panic should close")
	}
	o.Dispose()
}

func TestSubscribersAndObserverPanicsStayInsideTheReader(t *testing.T) {
	u := newUIAgent(nil, nil)
	u.Subscribe(func() { panic("subscriber") })
	u.onChange = func() { panic("change") }
	u.onFirstEvent = func() { panic("first") }
	u.observe(obj{"type": "agent_start"}, nil)
	u.observe(obj{"type": "turn_end"}, nil)
	if !u.hasSession {
		t.Error("the event was not consumed")
	}
}

func TestPanickingHandlersBecomeErrors(t *testing.T) {
	r := startRig(t)
	good := r.app.mgr
	r.app.mgr = nil // every handler that reads the manager now panics with a nil dereference
	_, failure := r.tool("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "general-purpose", "run_in_background": false})
	if !strings.Contains(failure, "internal error in Agent") {
		t.Errorf("the Agent tool: %q", failure)
	}
	_, failure = r.tool("get_subagent_result", obj{"agent_id": "x"})
	if !strings.Contains(failure, "internal error in get_subagent_result") {
		t.Errorf("get_subagent_result: %q", failure)
	}
	_ = r.Command("agents", "") // must not take the process down
	r.app.mgr = good
	// The extension keeps serving.
	if text, failure := r.tool("get_subagent_result", obj{"agent_id": "nope"}); failure != "" || !strings.Contains(text, "Agent not found") {
		t.Errorf("after the panics: %q %q", text, failure)
	}
}

func TestPanickingChildStartFailsTheAgentNotTheProcess(t *testing.T) {
	r := startRig(t)
	r.app.mgr.start = func(context.Context, childSpec) (child, error) { panic("start") }
	out := r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "general-purpose", "run_in_background": false})
	hasStr(t, out, "Agent failed: internal error in starting the child: start")
}

func TestPanickingSettleHookStillSettlesTheAgent(t *testing.T) {
	r := startRig(t)
	orig := r.app.mgr.onSettle
	r.app.mgr.onSettle = func(rec *agentRecord) { orig(rec); panic("settle hook") }
	idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "general-purpose"}))
	c := r.fleet.wait(t, 1)[0]
	c.finish("done")
	r.waitEmitted("subagents:completed")
	// The panic came after the record settled; the next agent still runs (the slot was released, the process is up).
	hasStr(t, r.must("get_subagent_result", obj{"agent_id": "general-purpose"}), "Status: completed")
}

func TestPanickingFleetReadsDoNotLeaveTheListLocked(t *testing.T) {
	m := &panicLister{}
	f := newFleetList(m, activityMap{}, nil, nil, nil)
	f.mu.Lock()
	rows := f.rosterLocked() // the lister panics; it must not escape while the lock is held
	f.mu.Unlock()
	if len(rows) != 1 || rows[0].kind != "main" {
		t.Errorf("roster: %+v", rows)
	}
	w := newAgentWidget(m, activityMap{}, nil, nil, nil)
	if got := w.renderWidget(80, plainTheme()); got != nil {
		t.Errorf("widget: %q", got)
	}
}

type panicLister struct{ fleetManager }

func (*panicLister) listAgents() []*agentView { panic("list") }

// ---- M1: the steer composer is pi-tui's Input ----

func typed(in *steerInput, s string) {
	for _, r := range s {
		in.handleInput(string(r))
	}
}

func TestSteerInputBracketedPaste(t *testing.T) {
	in := newSteerInput()
	typed(in, "ab")
	in.handleInput("\x1b[200~line1\nline2\r\n\tx\x1b[201~")
	eqStr(t, in.value, "abline1line2    x")
	// A paste split over chunks, and typing after it in the same chunk.
	in = newSteerInput()
	in.handleInput("\x1b[200~foo")
	in.handleInput("bar\x1b[201~z")
	eqStr(t, in.value, "foobarz")
	if in.inPaste {
		t.Error("still in paste")
	}
}

func TestSteerInputKittyCSIuPrintable(t *testing.T) {
	in := newSteerInput()
	in.handleInput("\x1b[97u")
	in.handleInput("\x1b[98;2u") // Shift+b
	in.handleInput("\x1b[99u")
	eqStr(t, in.value, "aBc")
	in.handleInput("\x1b[1;5A") // an unrelated control sequence is dropped, not typed
	eqStr(t, in.value, "aBc")
}

func TestSteerInputRejectsControlCharacters(t *testing.T) {
	in := newSteerInput()
	in.handleInput("\u0085") // C1
	in.handleInput("a\x01b")
	in.handleInput("\x7f\x7f") // DEL-bearing chunk that is not backspace is rejected
	in.handleInput("ok é 日本")
	eqStr(t, in.value, "ok é 日本")
}

func TestSteerInputGraphemeMovementAndDeletion(t *testing.T) {
	in := newSteerInput()
	in.handleInput("a e\u0301👨‍👩‍👧")
	in.handleInput("\x1b[D") // left: over the whole family emoji
	eqStr(t, slice16(in.value, in.cursor), "👨‍👩‍👧")
	in.handleInput("\x1b[D") // left: over e + combining acute
	eqStr(t, slice16(in.value, in.cursor), "e\u0301👨‍👩‍👧")
	in.handleInput("\x1b[3~") // forward delete the whole grapheme
	eqStr(t, in.value, "a 👨‍👩‍👧")
	in.handleInput("\x7f") // backspace deletes the space before the cursor
	eqStr(t, in.value, "a👨‍👩‍👧")
	in.handleInput("\x05") // ctrl+e
	in.handleInput("\x7f")
	eqStr(t, in.value, "a")
}

func TestSteerInputUndoKillRingAndWords(t *testing.T) {
	in := newSteerInput()
	typed(in, "hello world")
	in.handleInput("\x17") // ctrl+w: delete the word before the cursor
	eqStr(t, in.value, "hello ")
	in.handleInput("\x19") // ctrl+y: yank it back
	eqStr(t, in.value, "hello world")
	in.handleInput("\x1f") // ctrl+-: undo the yank
	eqStr(t, in.value, "hello ")
	in.handleInput("\x01") // ctrl+a
	in.handleInput("\x0b") // ctrl+k: kill to the end
	eqStr(t, in.value, "")
	in.handleInput("\x19")
	eqStr(t, in.value, "hello ")
	// Consecutive typed word characters are one undo unit.
	in = newSteerInput()
	typed(in, "abc")
	in.handleInput("\x1f")
	eqStr(t, in.value, "")
}

func TestSteerInputSubmitAndCancelUseTheKeybindings(t *testing.T) {
	in := newSteerInput()
	var sent []string
	cancelled := 0
	in.onSubmit = func(v string) { sent = append(sent, v) }
	in.onEscape = func() { cancelled++ }
	typed(in, "go")
	in.handleInput("\r")
	in.handleInput("\n")
	in.handleInput("\x1b")
	in.handleInput("\x03") // ctrl+c is tui.select.cancel too
	if len(sent) != 2 || sent[0] != "go" || cancelled != 2 {
		t.Errorf("sent %q, cancelled %d", sent, cancelled)
	}
}

func TestSteerInputRenderScrollsAndMarksTheCursor(t *testing.T) {
	in := newSteerInput()
	typed(in, strings.Repeat("x", 3)+"é")
	line := in.render(20)
	hasStr(t, line, tui.CursorMarker)
	hasStr(t, line, tui.FakeCursorStart+" "+tui.FakeCursorEnd) // the cursor is past the end: a space
	if widthx.VisibleWidth(line) != 20 {
		t.Errorf("width %d", widthx.VisibleWidth(line))
	}
	in = newSteerInput()
	typed(in, strings.Repeat("a", 50)+"日本")
	for _, w := range []int{3, 4, 10, 20, 40} {
		if vw := widthx.VisibleWidth(in.render(w)); vw > w {
			t.Errorf("width %d: rendered %d", w, vw)
		}
	}
	line = in.render(20)
	hasStr(t, line, "日本") // the end of the text is the visible window
	lacksStr(t, line, strings.Repeat("a", 30))
	in.focused = false
	lacksStr(t, in.render(20), tui.CursorMarker)
}
