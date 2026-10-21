package powerline_footer

import (
	"testing"
	"time"
)

// editorKey: the queue's part of the powerline editor's handleInput, over a scripted editor whose own submit is a plain send.

type keyEditor struct {
	fakeEditor
	showing bool
	sent    []string
}

func (k *keyEditor) ShowingAutocomplete() bool { return k.showing }
func (k *keyEditor) SubmitText(text string)    { k.sent = append(k.sent, text) }

type keyRig struct {
	*delayRig
	ke *keyEditor
}

func newKeyRig(t *testing.T, delayMs float64) *keyRig {
	r := &keyRig{delayRig: newDelayRig(t, delayMs), ke: &keyEditor{}}
	r.q.editor = &r.ke.fakeEditor
	return r
}

// type puts text into the editor as the user does.
func (r *keyRig) typeText(text string) { r.ke.text += text }

// key delivers one key; a key the queue leaves to the editor is what Pi's editor does with it: Enter submits the text.
func (r *keyRig) key(data string) editorVerdict {
	r.q.mu.Lock()
	verdict := r.q.editorKey(r.ke, data, false)
	r.q.mu.Unlock()
	if verdict == editorContinue && data == "\r" {
		r.ke.sent = append(r.ke.sent, r.ke.text)
		r.ke.text = ""
	}
	return verdict
}

func TestDelayedSendTwins(t *testing.T) {
	tw(t, "delayed-send", "Esc during the send delay returns the prompt to the editor without sending it", func(t *testing.T) {
		r := newKeyRig(t, 4000)
		r.typeText("fix the test")
		r.key("\r")
		eq(t, r.ke.text, "", "the editor is empty while the prompt waits")
		r.typeText("also lint")
		r.clock.tick(3999 * time.Millisecond)
		r.key("\x1b")
		eq(t, r.ke.text, "fix the test\n\nalso lint", "the prompt returns before the draft")
		r.clock.tick(10 * time.Second)
		eq(t, r.ke.sent, []string(nil), "nothing is sent")
	})
	tw(t, "delayed-send", "delayed prompts send after the delay, or right away on Enter with an empty editor", func(t *testing.T) {
		r := newKeyRig(t, 4000)
		r.typeText("first")
		r.key("\r")
		r.clock.tick(3999 * time.Millisecond)
		eq(t, r.ke.sent, []string(nil), "nothing before the delay")
		r.clock.tick(time.Millisecond)
		eq(t, r.ke.sent, []string{"first"}, "sent after the delay")

		r.typeText("second")
		r.key("\r")
		r.key("\r")
		eq(t, r.ke.sent, []string{"first", "second"}, "Enter on an empty editor sends now")
	})
}

func TestEditorKeyLeavesCommandsAndOtherKeysToTheEditor(t *testing.T) {
	r := newKeyRig(t, 4000)
	r.typeText("/model")
	eq(t, r.key("\r"), editorContinue, "a slash command is not delayed")
	r.typeText("!ls")
	eq(t, r.key("\r"), editorContinue, "a bang command is not delayed")
	eq(t, r.ke.sent, []string{"/model", "!ls"}, "both went through the editor's own submit")
	eq(t, r.key("a"), editorContinue, "a letter")
	eq(t, r.key("\x1b"), editorContinue, "Esc with nothing waiting")
	eq(t, r.key("\x1b[A"), editorContinue, "up")
}

func TestEditorKeyDoesNotDelayWithoutADelayOrInBashMode(t *testing.T) {
	off := newKeyRig(t, 0)
	off.typeText("now")
	eq(t, off.key("\r"), editorContinue, "no delay configured")

	bash := newKeyRig(t, 4000)
	bash.typeText("ls")
	bash.q.mu.Lock()
	verdict := bash.q.editorKey(bash.ke, "\r", true)
	bash.q.mu.Unlock()
	eq(t, verdict, editorContinue, "bash mode submits at once")
}

func TestEditorKeyEscapeStaysWithAnOpenAutocomplete(t *testing.T) {
	r := newKeyRig(t, 4000)
	r.typeText("hold on")
	r.key("\r")
	r.ke.showing = true
	eq(t, r.key("\x1b"), editorContinue, "Esc closes the popup first")
	eq(t, r.q.pendingSend != nil, true, "the prompt still waits")
	r.ke.showing = false
	eq(t, r.key("\x1b"), editorHandled, "Esc takes it back")
	eq(t, r.ke.text, "hold on", "restored")
}

func TestEditorKeyCapturesAPromptTypedWhileCompacting(t *testing.T) {
	r := newKeyRig(t, 0)
	r.p.compacting = true
	r.typeText("run the tests")
	eq(t, r.key("\r"), editorHandled, "queued for after the compaction")
	eq(t, r.ke.text, "", "the editor is cleared")
	eq(t, r.ke.history, []string{"run the tests"}, "and remembers it")
	items, _ := r.store.queuedDeliveryItems(r.q.context(), intentPostCompact)
	eq(t, len(items), 1, "one queued item")

	r.typeText("/model")
	eq(t, r.key("\r"), editorPassThrough, "a slash command goes to the editor during compaction")
	r.ke.text = ""
	eq(t, r.key("\r"), editorHandled, "Enter on an empty editor sends a waiting prompt now")
	r.typeText("steer this")
	eq(t, r.key("\x1b[13;3u"), editorHandled, "alt+enter is the follow-up submit")
}

func TestEditorKeyStartsACompaction(t *testing.T) {
	r := newKeyRig(t, 0)
	r.typeText("/compact")
	eq(t, r.key("\r"), editorHandled, "/compact")
	eq(t, r.p.compacting, true, "compacting")
	eq(t, r.host.compacts, 1, "ctx.compact")
}

func TestEditorKeyDelaysAPromptAndKeepsItInHistory(t *testing.T) {
	r := newKeyRig(t, 4000)
	r.typeText("  fix the test \n")
	eq(t, r.key("\r"), editorHandled, "the prompt waits")
	eq(t, r.ke.text, "", "the editor is cleared")
	eq(t, r.ke.history, []string{"fix the test"}, "the editor remembers the trimmed prompt")
	eq(t, r.q.pendingSend.text, "fix the test", "and the queue holds it")
	r.q.mu.Lock()
	lines := renderPendingSendLines(r.q.pendingSend, 80, plainTheme{})
	r.q.mu.Unlock()
	eq(t, len(lines), 1, "the pending-send widget has a row")
}

func TestEditorKeyCtrlJIsANewLineNotASubmit(t *testing.T) {
	r := newKeyRig(t, 4000)
	r.typeText("two\nlines")
	eq(t, r.key("\n"), editorContinue, "ctrl+j goes to the editor, which makes a new line")
	eq(t, r.q.pendingSend == nil, true, "and nothing waits")
}
