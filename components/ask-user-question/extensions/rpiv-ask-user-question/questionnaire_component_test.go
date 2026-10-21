package ask_user_question

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/ask-user-question/internal/ui"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

func newTestQuestionnaire(t *testing.T, f *fakeUIHost) (*questionnaireComponent, *sessionRef) {
	t.Helper()
	typed := questionParams{Questions: []question{
		{Question: "Which?", Header: "Pick", Options: []option{{Label: "A", Description: "a"}, {Label: "B", Description: "b"}}},
	}}
	ref := &sessionRef{}
	return newQuestionnaireComponent(f, typed, [][]ui.WrappingSelectItem{buildItemsForQuestion(typed.Questions[0])}, "off", false, ref), ref
}

// A Kitty key release is dropped before the session sees it (pi-tui does not deliver releases to a component that
// did not ask for them): releasing Enter must not confirm the highlighted option.
func TestQuestionnaireComponentDropsKeyReleases(t *testing.T) {
	tui.SetKittyProtocolActive(true)
	t.Cleanup(func() { tui.SetKittyProtocolActive(false) })
	f := newFakeUI(t)
	c, _ := newTestQuestionnaire(t, f)
	const enterRelease = "\x1b[13;1:3u"
	if !tui.IsKeyRelease(enterRelease) || !tui.MatchesKey(enterRelease, "enter") {
		t.Fatal("the fixture must be an Enter release that matches Enter")
	}
	res, err := c.HandleInput(enterRelease)
	if err != nil || res.Done {
		t.Fatalf("a key release closed the questionnaire: %+v, %v", res, err)
	}
	if res, _ := c.HandleInput("\r"); !res.Done {
		t.Fatalf("a key press of Enter must confirm: %+v", res)
	}
}

// Ctrl+G cannot stop the host's TUI, so the key reports the original's failure path: an error notification
// carrying the Pigpen refusal text, and the draft stays.
func TestQuestionnaireExternalEditorKeyNotifiesTheRefusal(t *testing.T) {
	f := newFakeUI(t)
	var got, edited string
	var ok bool
	called := false
	editInput(f)("draft", func(e string, k bool) { called, edited, ok = true, e, k })
	if !called || ok || edited != "" {
		t.Fatalf("finish(%q, %v) called=%v, want finish(\"\", false)", edited, ok, called)
	}
	if len(f.notices) != 1 {
		t.Fatalf("notices = %+v", f.notices)
	}
	got = f.notices[0].Message
	if f.notices[0].Level != "error" || !strings.HasPrefix(got, "External editor failed: ") || !strings.Contains(got, "external editor for custom answers") {
		t.Fatalf("notification = %+v", f.notices[0])
	}
}
