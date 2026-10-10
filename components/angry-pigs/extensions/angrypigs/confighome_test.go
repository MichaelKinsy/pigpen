package angrypigs_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/angrypigs"
)

// Context.ConfigHome fails when no config directory can be found (SDK 0.5). The command returns that error
// before it opens any UI, so the game never reads or writes saved state under a guessed relative directory.
func TestCommandWithoutAConfigHomeFailsBeforeOpeningTheGame(t *testing.T) {
	for _, k := range []string{"PIG_HOME", "XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(k, "")
	}
	t.Chdir(t.TempDir())
	h := StartHost(t, angrypigs.Extension(), HostOptions{})
	e := h.Command("angry-pigs", "")
	if !strings.Contains(e, "home directory") {
		t.Fatalf("command error = %q, want the missing home directory", e)
	}
	if n := len(h.CallsTo("ui.custom")); n != 0 {
		t.Errorf("the game opened %d time(s) without a config directory", n)
	}
}
