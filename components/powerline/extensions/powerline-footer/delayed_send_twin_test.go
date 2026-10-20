package powerline_footer

import "testing"

// Twins of port/oracle/tests/delayed-send.test.ts. Both cases type into Pi's real CustomEditor (editor.handleInput, the
// editor's onSubmit) with powerline.sendDelayMs set. An extension cannot replace or wrap the editor (Context.SetEditorComponent
// refuses a component), so powerline.sendDelayMs stays refused (refusal.go) and the cases are skipped. The countdown logic itself
// (submitWithDelay, takePendingSend, restorePendingSend, sendPendingNow) is ported and tested in queue_editor_test.go.
func TestDelayedSendTwins(t *testing.T) {
	tskip(t, "delayed-send", "Esc during the send delay returns the prompt to the editor without sending it",
		"SDK: setEditorComponent. Needs the editor's handleInput and the app.interrupt keybinding match; the Esc restore is covered with a fake editor by TestRestorePendingSendKeepsTheDraft")
	tskip(t, "delayed-send", "delayed prompts send after the delay, or right away on Enter with an empty editor",
		"SDK: setEditorComponent. Needs the editor's onSubmit hook (the submit wrap of index.ts 3270-3281); the countdown and Enter-sends-now are covered with a fake editor by TestSubmitWithDelay")
}
