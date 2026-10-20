package powerline_footer

import (
	"strings"
	"testing"
	"time"
)

// The editor-facing queue code against a fake editor: delayed send and the capture of prompts typed while compacting.

type fakeEditor struct {
	text    string
	history []string
}

func (e *fakeEditor) GetExpandedText() string { return e.text }
func (e *fakeEditor) SetText(text string)     { e.text = text }
func (e *fakeEditor) AddToHistory(text string) {
	e.history = append(e.history, text)
}

type delayRig struct {
	*queueRig
	ed      *fakeEditor
	sent    []string
	renders int
}

func newDelayRig(t *testing.T, delayMs float64) *delayRig {
	r := &delayRig{queueRig: newQueueRig(t), ed: &fakeEditor{}}
	r.p.config.SendDelayMs = delayMs
	r.q.editor = r.ed
	r.q.requestRender = func() { r.renders++ }
	r.emit("session_start", map[string]any{"reason": "new"})
	return r
}

func (r *delayRig) submit(text string) {
	r.q.mu.Lock()
	defer r.q.mu.Unlock()
	r.q.submitWithDelay(text, func(s string) { r.sent = append(r.sent, s) })
}

func (r *delayRig) interrupt() editorVerdict {
	r.q.mu.Lock()
	defer r.q.mu.Unlock()
	return r.q.interceptEditorInput(r.ed, editorInput{Interrupt: true})
}

func TestSubmitWithDelay(t *testing.T) {
	r := newDelayRig(t, 4000)
	r.submit("first")
	r.clock.tick(3999 * time.Millisecond)
	eq(t, r.sent, []string(nil), "nothing before the delay")
	r.clock.tick(time.Millisecond)
	eq(t, r.sent, []string{"first"}, "sent after the delay")

	r.submit("second")
	r.submit("") // Enter with an empty editor sends the waiting prompt now
	eq(t, r.sent, []string{"first", "second"}, "Enter on an empty editor sends now")

	r.submit("/compact")
	r.submit("!ls")
	eq(t, r.sent, []string{"first", "second", "/compact", "!ls"}, "commands are never delayed")

	r.submit("third")
	r.submit("fourth") // a new prompt sends the waiting one first
	eq(t, r.sent[4], "third", "the waiting prompt goes first")
	eq(t, r.q.pendingSend.text, "fourth", "and the new one waits")

	off := newDelayRig(t, 0)
	off.submit("now")
	eq(t, off.sent, []string{"now"}, "no delay configured")
}

func TestRestorePendingSendKeepsTheDraft(t *testing.T) {
	r := newDelayRig(t, 4000)
	r.submit("fix the test")
	r.ed.text = "also lint" // typed during the countdown
	r.clock.tick(3999 * time.Millisecond)
	eq(t, r.interrupt(), editorHandled, "Esc is the queue's while a prompt waits")
	eq(t, r.ed.text, "fix the test\n\nalso lint", "the prompt returns before the draft")
	r.clock.tick(10 * time.Second)
	eq(t, r.sent, []string(nil), "nothing is sent")
	eq(t, r.interrupt(), editorContinue, "Esc is the editor's again")

	r.submit("alone")
	r.ed.text = "  "
	r.interrupt()
	eq(t, r.ed.text, "alone", "an empty draft leaves just the prompt")
}

func TestPendingSendDuringCompactionIsQueuedForAfterIt(t *testing.T) {
	r := newDelayRig(t, 4000)
	r.submit("later")
	r.emit("session_before_compact", nil)
	r.clock.tick(4 * time.Second)
	eq(t, r.sent, []string(nil), "a delayed prompt never reaches the native compaction queue")
	eq(t, r.ed.history, []string{"later"}, "it is kept in the editor history")
	active, _ := r.store.queuedDeliveryItems(r.q.context(), intentPostCompact)
	if len(active) != 1 || active[0].Text != "later" {
		t.Fatalf("queued = %+v, want the prompt", active)
	}
	eq(t, r.host.notices(), []string{"info: Queued for after compaction (" + active[0].ID + ")"}, "notice")
}

func TestPendingSendLines(t *testing.T) {
	oldClock := clock
	defer func() { clock = oldClock }()
	now := int64(1_000_000)
	clock = func() int64 { return now }
	r := newDelayRig(t, 4000)
	r.submit("  fix \n the   test ")
	line := func() string { return stripAnsi(r.p.renderPendingSend(200)[0]) }
	eq(t, line(), " sending in 4s · esc to edit · enter to send now  fix the test", "4 s")
	now += 2500
	eq(t, strings.Contains(line(), "sending in 2s"), true, "ceil of the rest")
	now += 100000
	eq(t, strings.Contains(line(), "sending in 1s"), true, "never below one second")
	eq(t, len(r.p.renderPendingSend(10)), 1, "one row")
	eq(t, visibleWidth(r.p.renderPendingSend(10)[0]) <= 10, true, "truncated to the width")
	r.q.mu.Lock()
	r.q.takePendingSend()
	r.q.mu.Unlock()
	eq(t, r.p.renderPendingSend(80), []string{}, "no row without a waiting prompt")
}

func TestEditorSubmitDuringCompaction(t *testing.T) {
	r := newDelayRig(t, 0)
	intercept := func(in editorInput) editorVerdict {
		r.q.mu.Lock()
		defer r.q.mu.Unlock()
		return r.q.interceptEditorInput(r.ed, in)
	}
	// /compact starts a compaction the queue knows of.
	r.ed.text = "/compact"
	eq(t, intercept(editorInput{Submit: true}), editorHandled, "/compact")
	eq(t, r.ed.text, "", "the editor is cleared")
	eq(t, r.ed.history, []string{"/compact"}, "history")
	eq(t, r.p.compacting, true, "compacting")
	eq(t, r.host.compacts, 1, "ctx.compact")

	// A prompt typed during it is queued for after it.
	r.ed.text = "  run the tests  "
	eq(t, intercept(editorInput{Submit: true}), editorHandled, "typed prompt")
	eq(t, r.ed.history, []string{"/compact", "run the tests"}, "history")
	r.ed.text = "also lint"
	eq(t, intercept(editorInput{FollowUpSubmit: true}), editorHandled, "follow-up submit")
	items, _ := r.store.queuedDeliveryItems(r.q.context(), intentPostCompact)
	eq(t, len(items), 2, "two queued")
	eq(t, items[0].Text, "run the tests", "text is trimmed")
	// A slash command is the editor's, an empty editor sends a waiting prompt, bash mode is left alone.
	r.ed.text = "/model"
	eq(t, intercept(editorInput{Submit: true}), editorPassThrough, "slash command")
	r.ed.text = " "
	eq(t, intercept(editorInput{Submit: true}), editorHandled, "empty editor")
	r.ed.text = "ls"
	eq(t, intercept(editorInput{Submit: true, BashModeActive: true}), editorContinue, "bash mode")
	eq(t, intercept(editorInput{}), editorContinue, "other keys")

	// A compaction that cannot start releases the queue.
	r.host.compactCB(queueError("nothing to compact"))
	eq(t, r.p.compacting, false, "failed compaction")
	eq(t, r.status(items[0].ID), statusBlocked, "blocked")
	eq(t, r.host.notices()[len(r.host.notices())-1], "error: nothing to compact", "error notice")
}

func TestCompactWithAPromptQueuesIt(t *testing.T) {
	r := newDelayRig(t, 0)
	r.ed.text = "/compact great lets proceed"
	r.q.mu.Lock()
	verdict := r.q.interceptEditorInput(r.ed, editorInput{Submit: true})
	r.q.mu.Unlock()
	eq(t, verdict, editorHandled, "handled")
	items, _ := r.store.queuedDeliveryItems(r.q.context(), intentPostCompact)
	eq(t, len(items), 1, "queued")
	eq(t, items[0].Text, "great lets proceed", "the prompt")
	eq(t, r.host.compacts, 1, "compaction started")
	eq(t, r.p.compacting, true, "compacting")

	native := newDelayRig(t, 0)
	native.p.config.CompactPromptMode = "native"
	native.ed.text = "/compact great lets proceed"
	native.q.mu.Lock()
	verdict = native.q.interceptEditorInput(native.ed, editorInput{Submit: true})
	native.q.mu.Unlock()
	eq(t, verdict, editorContinue, "native mode leaves /compact <prompt> to Pi")
}

func TestTeardownReturnsAWaitingPromptToTheEditor(t *testing.T) {
	r := newDelayRig(t, 4000)
	r.submit("keep me")
	r.q.mu.Lock()
	r.p.teardown()
	r.q.mu.Unlock()
	eq(t, r.ed.text, "keep me", "/powerline off hands the waiting prompt back")
	eq(t, r.q.pendingSend == nil, true, "and stops the countdown")
}

// A compaction that fails to start reports only for the operation that started it, in the session that started it.
func TestCompactStartErrorIsIgnoredForALaterOperationOrSession(t *testing.T) {
	start := func(t *testing.T) *delayRig {
		r := newDelayRig(t, 0)
		r.ed.text = "/compact"
		r.q.mu.Lock()
		r.q.interceptEditorInput(r.ed, editorInput{Submit: true})
		r.q.mu.Unlock()
		eq(t, r.host.compacts, 1, "compaction started")
		return r
	}
	fail := func(r *delayRig) { r.host.compactCB(queueError("late failure")) }

	r := start(t)
	r.emit("session_before_compact", nil)
	r.emit("session_before_compact", nil) // a newer operation: two increments after the one that started it
	fail(r)
	eq(t, r.p.compacting, true, "an error of an older operation is ignored")
	eq(t, len(r.host.notices()), 0, "silently")

	r = start(t)
	r.emit("session_start", map[string]any{"reason": "reload"})
	r.p.compacting = true
	fail(r)
	eq(t, r.p.compacting, true, "an error from before the session was replaced is ignored")
}
