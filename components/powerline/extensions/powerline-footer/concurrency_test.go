package powerline_footer_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// The SDK lays the footer out again on its own goroutine after every width change (surface.go refresh), while the port's
// handlers and its OnWidthChange callback rebuild the bar's snapshot and invalidate the git caches. The footer renderer must not
// share mutable state with them: run under `go test -race`, which reports any such access.
func TestFooterRendersWhileHandlersRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	cwd := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "git@github.com:o/r.git"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	h := StartHost(t, powerline.Extension(), HostOptions{Mode: "tui", Cwd: cwd})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	for i := range 40 {
		h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "width_change", "args": map[string]any{"width": 30 + i%3*40}}})
		h.Fire("agent_start", nil)
		h.Fire("user_bash", map[string]any{"command": "git checkout main"})
		h.Fire("agent_end", nil)
	}
	time.Sleep(200 * time.Millisecond) // let the last width deliveries finish
	if len(h.CallsTo("ui.setFooter")) == 0 {
		t.Fatal("the footer was never installed")
	}
	for _, c := range h.Calls() {
		if msg, _ := c.Args["message"].(string); strings.Contains(msg, "render failed") {
			t.Errorf("a render failed: %s", msg)
		}
	}
}
