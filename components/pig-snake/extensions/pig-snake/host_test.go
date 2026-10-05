package pig_snake_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pig_snake "github.com/MichaelKinsy/pigpen/pig-snake"
)

// Layer 1: the real extension through the fake PiG host (wire protocol over
// net.Pipe). The component itself is tested directly in component_test.go; here
// we check what crosses the host boundary: the overlay request, the result
// handling, the notification and the saved state.

// command runs a slash command the way PiG's host sends it: the name in "tool"
// and the argument text as a JSON string. The template's Host.Command sends
// {"name","args"} in "args", which the SDK answers with "unknown command"
// (recorded in pigpen-pig-snake-FRICTION.md); fakehost_test.go stays an
// unchanged copy of the template, so this file carries the correct helper.
func runCommand(h *Host, name, args string) string {
	h.t.Helper()
	if !h.cmds[name] {
		h.t.Fatalf("command %q is not registered", name)
	}
	encoded, _ := json.Marshal(args)
	_, failure := h.roundTrip(map[string]any{"method": "command", "tool": name, "args": json.RawMessage(encoded)})
	return failure
}

func customResult(result map[string]any) func(string, map[string]any) (map[string]any, string) {
	return func(method string, args map[string]any) (map[string]any, string) {
		if method == "ui.custom" {
			return map[string]any{"ok": true, "result": result}, ""
		}
		return map[string]any{}, ""
	}
}

func notifications(h *Host) []map[string]any {
	var out []map[string]any
	for _, c := range h.CallsTo("ui.notify") {
		out = append(out, c.Args)
	}
	return out
}

func TestCommandOpensAFullTerminalOverlayReportsAndSavesTheScores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	for _, command := range []string{"pig-snake", "snake"} {
		h := StartHost(t, pig_snake.Extension(), HostOptions{OnCall: customResult(map[string]any{
			"score": 12, "highScore": 12, "wrapHighScore": 3, "herd": 13, "wrap": false, "gameOver": true,
		})})
		if failure := runCommand(h, command, ""); failure != "" {
			t.Fatalf("/%s failed: %s", command, failure)
		}
		opens := h.CallsTo("ui.custom")
		if len(opens) != 1 {
			t.Fatalf("/%s: %d ui.custom calls", command, len(opens))
		}
		args := opens[0].Args
		if args["title"] != "Pig Snake" || args["overlay"] != true || args["widthFraction"] != float64(1) || args["heightFraction"] != float64(1) {
			t.Fatalf("/%s overlay options = %v", command, args)
		}
		notes := notifications(h)
		if len(notes) != 1 || notes[0]["message"] != "Pig Snake score 12 · herd 13 · high 12" || notes[0]["level"] != "info" {
			t.Fatalf("/%s notifications = %v", command, notes)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "state", "pigpen", "pig-snake.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]int
	if err := json.Unmarshal(data, &saved); err != nil || saved["highScore"] != 12 || saved["wrapHighScore"] != 3 || len(saved) != 2 {
		t.Fatalf("saved state %s (%v)", data, err)
	}
}

func TestWrapModeIsNamedInTheNotification(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	h := StartHost(t, pig_snake.Extension(), HostOptions{OnCall: customResult(map[string]any{
		"score": 4, "highScore": 9, "wrapHighScore": 6, "herd": 5, "wrap": true,
	})})
	if failure := runCommand(h, "pig-snake", ""); failure != "" {
		t.Fatal(failure)
	}
	notes := notifications(h)
	if len(notes) != 1 || notes[0]["message"] != "Pig Snake (wrap) score 4 · herd 5 · high 6" {
		t.Fatalf("notifications = %v", notes)
	}
}

func TestAnAbandonedGameStillSavesTheSavedHighScoreUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	state := filepath.Join(home, "state", "pigpen", "pig-snake.json")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte(`{"highScore":21,"wrapHighScore":4}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The host reports nothing (overlay dismissed): the component's own final state is used.
	h := StartHost(t, pig_snake.Extension(), HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.custom" {
			return map[string]any{"ok": false}, ""
		}
		return map[string]any{}, ""
	}})
	if failure := runCommand(h, "pig-snake", ""); failure != "" {
		t.Fatal(failure)
	}
	data, _ := os.ReadFile(state)
	if !strings.Contains(string(data), `"highScore":21`) || !strings.Contains(string(data), `"wrapHighScore":4`) {
		t.Fatalf("state %s", data)
	}
	notes := notifications(h)
	if len(notes) != 1 || notes[0]["message"] != "Pig Snake score 0 · herd 1 · high 21" {
		t.Fatalf("notifications = %v", notes)
	}
}

func TestOnlyTheInteractiveTUICanPlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	for _, mode := range []string{"print", "json", "rpc"} {
		h := StartHost(t, pig_snake.Extension(), HostOptions{Mode: mode})
		if failure := runCommand(h, "pig-snake", ""); failure != "" {
			t.Fatalf("%s: %s", mode, failure)
		}
		if n := len(h.CallsTo("ui.custom")); n != 0 {
			t.Fatalf("%s: opened %d overlays without a UI", mode, n)
		}
		notes := notifications(h)
		if len(notes) != 1 || notes[0]["level"] != "warning" || !strings.Contains(notes[0]["message"].(string), "interactive terminal") {
			t.Fatalf("%s notifications = %v", mode, notes)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "state")); err == nil {
		t.Fatal("a headless run wrote state")
	}
}

func TestAHostErrorOpeningTheOverlayIsReturnedAndNothingIsSaved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	h := StartHost(t, pig_snake.Extension(), HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.custom" {
			return nil, "overlay refused"
		}
		return map[string]any{}, ""
	}})
	failure := runCommand(h, "pig-snake", "")
	if !strings.Contains(failure, "overlay refused") {
		t.Fatalf("failure = %q", failure)
	}
	if _, err := os.Stat(filepath.Join(home, "state")); err == nil {
		t.Fatal("state written after a failed open")
	}
}

func TestOnlyAnExplicitCommandStartsAGame(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	h := StartHost(t, pig_snake.Extension(), HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	h.Fire("agent_start", nil)
	if n := len(h.Calls()); n != 0 {
		t.Fatalf("an idle extension made %d host calls: %v", n, h.Calls())
	}
}

func TestATUIHostWithoutAUIStillDoesNotPlay(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	noUI := false
	h := StartHost(t, pig_snake.Extension(), HostOptions{Mode: "tui", HasUI: &noUI})
	if failure := runCommand(h, "snake", ""); failure != "" {
		t.Fatal(failure)
	}
	if n := len(h.CallsTo("ui.custom")); n != 0 {
		t.Fatalf("opened %d overlays without a UI", n)
	}
	notes := notifications(h)
	if len(notes) != 1 || notes[0]["level"] != "warning" {
		t.Fatalf("a host without a UI must be told so, not shown a score: %v", notes)
	}
}
