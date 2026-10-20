package ask_user_question

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/pigpen/ask-user-question/internal/ui"
)

// The suites assert the English texts; a developer's LANG must not select a locale for them. Locale tests set
// their own.
func TestMain(m *testing.M) {
	ui.SetLocale("en")
	os.Exit(m.Run())
}
