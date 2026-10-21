package powerline_footer_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// Bash mode through the wire: the commands, the editor's keys and the transcript widget.

type bashHost struct {
	h    *Host
	mu   sync.Mutex
	text string
	ops  []string // what the powerline editor asked of the default editor: "setText:...", "handleInput:...", "addToHistory:..."
}

func (b *bashHost) opsLog() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.ops...)
}

func startBashHost(t *testing.T, settings string) *bashHost {
	t.Helper()
	home := t.TempDir()
	agent := filepath.Join(home, "agent")
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	t.Setenv("SHELL", bash)
	os.MkdirAll(agent, 0o755)
	os.WriteFile(filepath.Join(agent, "settings.json"), []byte(settings), 0o644)
	b := &bashHost{}
	b.h = StartHost(t, powerline.Extension(), HostOptions{Mode: "tui", Cwd: t.TempDir(), OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getModelInfo":
			return map[string]any{"id": "m", "name": "m", "provider": "p", "contextWindow": 1000}, ""
		case "getContextUsage":
			return nil, "unknown"
		case "getSessionID":
			return map[string]any{"sessionId": "bash-session"}, ""
		case "getThinkingLevel":
			return map[string]any{"level": "off"}, ""
		case "ui.editor.base":
			b.mu.Lock()
			defer b.mu.Unlock()
			switch args["op"] {
			case "getExpandedText", "getText":
				return map[string]any{"text": b.text}, ""
			case "getLines":
				return map[string]any{"lines": strings.Split(b.text, "\n")}, ""
			case "getCursor":
				return map[string]any{"line": 0, "col": len(b.text)}, ""
			case "setText":
				if a, ok := args["args"].(map[string]any); ok {
					b.text, _ = a["text"].(string)
					b.ops = append(b.ops, "setText:"+b.text)
				}
			case "handleInput":
				if a, ok := args["args"].(map[string]any); ok {
					data, _ := a["data"].(string)
					b.ops = append(b.ops, "handleInput:"+data)
				}
			case "addToHistory":
				if a, ok := args["args"].(map[string]any); ok {
					text, _ := a["text"].(string)
					b.ops = append(b.ops, "addToHistory:"+text)
				}
			}
		case "ui.getEditorText":
			b.mu.Lock()
			defer b.mu.Unlock()
			return map[string]any{"text": b.text}, ""
		}
		return map[string]any{}, ""
	}})
	b.h.Fire("session_start", map[string]any{"reason": "startup"})
	return b
}

func hasNote(h *Host, want string) bool {
	for _, n := range notifications(h) {
		if strings.Contains(n, want) {
			return true
		}
	}
	return false
}

func TestBashModeCommandsAnswerInsteadOfRefusing(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	if failure := b.h.Command("bash-mode", "on"); failure != "" {
		t.Fatal(failure)
	}
	if !hasNote(b.h, "info: Bash mode enabled (") {
		t.Errorf("notifications %q", notifications(b.h))
	}
	if failure := b.h.Command("bash-mode", "off"); failure != "" {
		t.Fatal(failure)
	}
	if !hasNote(b.h, "info: Bash mode disabled") {
		t.Errorf("notifications %q", notifications(b.h))
	}
	b.h.Command("bash-mode", "sideways")
	if !hasNote(b.h, "warning: Usage: /bash-mode [on|off|toggle]") {
		t.Errorf("notifications %q", notifications(b.h))
	}
	b.h.Command("bash-reset", "")
	if !hasNote(b.h, "info: Bash session reset") {
		t.Errorf("notifications %q", notifications(b.h))
	}
	for _, n := range notifications(b.h) {
		if strings.Contains(n, "does not support") {
			t.Errorf("a ported command was refused: %q", n)
		}
	}
}

func TestTheBashModeSettingIsNoLongerRefused(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false},"bashMode":{"completions":true}}`)
	for _, n := range notifications(b.h) {
		if strings.Contains(n, "bashMode") {
			t.Errorf("notification %q", n)
		}
	}
}

func TestEscapeInTheEditorLeavesBashMode(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	b.h.Command("bash-mode", "on")
	editorInput(b.h, "\x1b")
	waitUntil(t, "bash mode to end", func() bool { return hasNote(b.h, "info: Bash mode disabled") })
	for _, c := range baseCalls(b.h, "handleInput") {
		if args, _ := c.Args["args"].(map[string]any); args["data"] == "\x1b" {
			t.Error("Escape also reached the default editor")
		}
	}
}

func TestEnterInTheEditorRunsTheCommandAndTheTranscriptShowsIt(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	b.h.Command("bash-mode", "on")
	b.mu.Lock()
	b.text = "echo hello-from-bash"
	b.mu.Unlock()
	editorInput(b.h, "\r")
	waitUntil(t, "the transcript widget to show the command", func() bool {
		var content string
		for _, c := range b.h.CallsTo("ui.setWidget") {
			if c.Args["key"] == "powerline-bash-transcript" {
				if lines, ok := c.Args["content"].([]any); ok {
					var parts []string
					for _, l := range lines {
						parts = append(parts, l.(string))
					}
					content = strings.Join(parts, "\n")
				}
			}
		}
		return strings.Contains(content, "echo hello-from-bash") && strings.Contains(content, "\n   hello-from-bash")
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.text != "" {
		t.Errorf("the editor was not cleared: %q", b.text)
	}
}

func TestTheBashModeShortcutTogglesTheMode(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	editorInput(b.h, "\x1b[98;6u") // ctrl+shift+b
	waitUntil(t, "bash mode to start", func() bool { return hasNote(b.h, "info: Bash mode enabled (") })
}

func countNotes(h *Host, want string) int {
	n := 0
	for _, note := range notifications(h) {
		if strings.Contains(note, want) {
			n++
		}
	}
	return n
}

func topBar(t *testing.T, h *Host) string {
	t.Helper()
	content, _ := lastWidget(t, h, "powerline-top")
	return content
}

func TestTheShellModeSegmentFollowsBashMode(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	b.h.Command("bash-mode", "on")
	waitUntil(t, "the shell segment", func() bool {
		return strings.Contains(topBar(t, b.h), "bash") && strings.Contains(topBar(t, b.h), "idle")
	})
	b.h.Command("bash-mode", "off")
	waitUntil(t, "the shell segment to go", func() bool { return !strings.Contains(topBar(t, b.h), "idle") })
}

func TestTurningPowerlineOffEndsBashMode(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	b.h.Command("bash-mode", "on")
	b.h.Command("powerline", "") // off
	b.h.Command("powerline", "") // on again
	if strings.Contains(topBar(t, b.h), "idle") {
		t.Errorf("the shell segment is back after the bar was turned off and on: %q", topBar(t, b.h))
	}
	b.h.Command("bash-mode", "")
	if got := countNotes(b.h, "Bash mode enabled ("); got != 2 {
		t.Errorf("toggling after the bar was turned off did not start the mode again (%d starts): %q", got, notifications(b.h))
	}
}

func TestANewSessionStartsOutsideBashMode(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	b.h.Command("bash-mode", "on")
	b.h.Fire("session_start", map[string]any{"reason": "new"})
	b.h.Command("bash-mode", "")
	if got := countNotes(b.h, "Bash mode enabled ("); got != 2 {
		t.Errorf("%d starts: %q", got, notifications(b.h))
	}
}

func TestSessionShutdownEndsBashMode(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	b.h.Command("bash-mode", "on")
	b.h.Fire("session_shutdown", map[string]any{"reason": "quit"})
	b.h.Command("bash-mode", "")
	if got := countNotes(b.h, "Bash mode enabled ("); got != 2 {
		t.Errorf("%d starts: %q", got, notifications(b.h))
	}
}
