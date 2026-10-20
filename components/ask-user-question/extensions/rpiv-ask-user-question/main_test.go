package ask_user_question

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/pigpen/ask-user-question/internal/ui"
)

// The suites assert the English texts; a developer's LANG must not select a locale for them. Locale tests set
// their own.
//
// The highlight.js languages load once per process, and the frame replay compares against recordings made without Pi's
// interactive mode (only the eager languages exist there), so no suite here loads them; internal/ui's tests do.
func TestMain(m *testing.M) {
	ui.SetLocale("en")
	ui.LoadHighlightLanguages = func() {}
	os.Exit(m.Run())
}
