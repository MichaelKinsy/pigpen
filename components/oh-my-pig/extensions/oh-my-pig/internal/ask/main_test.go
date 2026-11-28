package ask

import (
	"os"
	"testing"
)

// The surface is the terminal unless a test says otherwise, whatever terminal
// the tests run in.
func TestMain(m *testing.M) {
	os.Setenv("PIG_TUI_NATIVE", "0")
	os.Exit(m.Run())
}
