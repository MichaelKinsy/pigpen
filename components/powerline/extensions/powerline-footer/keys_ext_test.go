package powerline_footer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// The keys slice through the SDK: the terminal-input listener, /stash-history and the stash across a reload, with a fake host
// that keeps an editor.

type keysRig struct {
	agent  string
	mu     sync.Mutex
	editor string
	h      *Host
}

func (r *keysRig) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.editor
}

func newKeysRig(t *testing.T, agent, sessionID, editor string) *keysRig {
	t.Helper()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	os.MkdirAll(agent, 0o755)
	if _, err := os.Stat(filepath.Join(agent, "settings.json")); err != nil {
		os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"powerline":{"welcome":false}}`), 0o644)
	}
	r := &keysRig{agent: agent, editor: editor}
	r.h = StartHost(t, powerline.Extension(), HostOptions{Mode: "tui", Cwd: t.TempDir(), OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getSessionID":
			return map[string]any{"sessionId": sessionID}, ""
		case "ui.getEditorText":
			r.mu.Lock()
			defer r.mu.Unlock()
			return map[string]any{"text": r.editor}, ""
		case "ui.setEditorText":
			r.mu.Lock()
			r.editor, _ = args["text"].(string)
			r.mu.Unlock()
		case "getModelInfo":
			return map[string]any{"id": "m", "name": "m", "provider": "p", "contextWindow": 1000}, ""
		case "getContextUsage":
			return nil, "unknown"
		case "getThinkingLevel":
			return map[string]any{"level": "off"}, ""
		}
		return map[string]any{}, ""
	}})
	return r
}

// input delivers one raw chunk the way the host does and returns whether the extension consumed it.
func (r *keysRig) input(t *testing.T, data string) bool {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"data": data})
	raw, failure := r.h.roundTrip(map[string]any{"method": "terminal_input", "args": json.RawMessage(args)})
	if failure != "" {
		t.Fatalf("terminal_input failed: %s", failure)
	}
	var out struct {
		Consume bool `json:"consume"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("terminal_input answer %s: %v", raw, err)
	}
	return out.Consume
}

func (r *keysRig) notes() []string {
	var out []string
	for _, c := range r.h.CallsTo("ui.notify") {
		out = append(out, c.Args["level"].(string)+": "+c.Args["message"].(string))
	}
	return out
}

func (r *keysRig) lastStatus(key string) (string, bool) {
	text, found := "", false
	for _, c := range r.h.CallsTo("ui.setStatus") {
		if c.Args["key"] == key {
			text, _ = c.Args["text"].(string)
			found = true
		}
	}
	return text, found
}

func TestTerminalInputInstallsOnTheBarAndStashes(t *testing.T) {
	r := newKeysRig(t, t.TempDir(), "sess-1", "draft")
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	if n := len(r.h.CallsTo("ui.onTerminalInput")); n != 1 {
		t.Fatalf("the listener is installed once, got %d", n)
	}

	if r.input(t, "x") || r.input(t, "\x13") {
		t.Error("ordinary keys and ctrl+s are left to the focused component")
	}
	if r.text() != "draft" {
		t.Fatalf("editor = %q", r.text())
	}
	if !r.input(t, "\x1bs") {
		t.Fatal("alt+s must be consumed")
	}
	if r.text() != "" {
		t.Errorf("editor = %q after the stash", r.text())
	}
	if s, _ := r.lastStatus("stash"); s != "stash" {
		t.Errorf("stash status = %q", s)
	}
	if !r.input(t, "\x1b[115;3u") {
		t.Fatal("the CSI-u form of alt+s must be consumed")
	}
	if r.text() != "draft" {
		t.Errorf("editor = %q after the restore", r.text())
	}
	if s, _ := r.lastStatus("stash"); s != "" {
		t.Errorf("stash status = %q after the restore", s)
	}
	got := strings.Join(r.notes(), "|")
	if !strings.Contains(got, "info: Text stashed|info: Stash restored") {
		t.Errorf("notifications = %q", got)
	}
	if r.input(t, "\x1b[115;3:3u") {
		t.Error("a key release is not a stash key")
	}

	// The stash history was persisted.
	data, err := os.ReadFile(filepath.Join(r.agent, "powerline-footer", "stash-history.json"))
	if err != nil || !strings.Contains(string(data), `"draft"`) {
		t.Errorf("stash history file: %q %v", data, err)
	}
}

func TestTerminalInputDisableAndEnable(t *testing.T) {
	r := newKeysRig(t, t.TempDir(), "sess-2", "draft")
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	r.input(t, "\x1bs")

	if failure := r.h.Command("powerline", ""); failure != "" {
		t.Fatal(failure)
	}
	if s, found := r.lastStatus("stash"); !found || s != "" {
		t.Errorf("disable clears the stash status, got %q", s)
	}
	if n := len(r.h.CallsTo("ui.offTerminalInput")); n != 1 {
		t.Errorf("disable removes the listener, got %d offTerminalInput calls", n)
	}
	if failure := r.h.Command("stash-history", ""); failure != "" {
		t.Fatal(failure)
	}
	if got := r.notes(); got[len(got)-1] != "info: Powerline is disabled" {
		t.Errorf("/stash-history while disabled: %q", got)
	}

	if failure := r.h.Command("powerline", ""); failure != "" {
		t.Fatal(failure)
	}
	if n := len(r.h.CallsTo("ui.onTerminalInput")); n != 2 {
		t.Errorf("enable installs the listener again, got %d", n)
	}
	// The stash did not survive the disable.
	r.mu.Lock()
	r.editor = ""
	r.mu.Unlock()
	r.input(t, "\x1bs")
	if got := r.notes(); got[len(got)-1] != "info: Nothing to stash" {
		t.Errorf("after disable: %q", got)
	}
}

func TestReloadCarriesTheStashAcrossProcesses(t *testing.T) {
	agent := t.TempDir()
	first := newKeysRig(t, agent, "reload-session", "draft across reload")
	first.h.Fire("session_start", map[string]any{"reason": "startup"})
	first.input(t, "\x1bs")
	first.h.Fire("session_shutdown", map[string]any{"reason": "reload"})

	second := newKeysRig(t, agent, "reload-session", "")
	second.h.Fire("session_start", map[string]any{"reason": "reload"})
	if s, _ := second.lastStatus("stash"); s != "stash" {
		t.Fatalf("the new generation's stash status = %q", s)
	}
	second.input(t, "\x1bs")
	if second.text() != "draft across reload" {
		t.Errorf("editor = %q", second.text())
	}

	// Taken once: a third generation finds nothing.
	third := newKeysRig(t, agent, "reload-session", "")
	third.h.Fire("session_start", map[string]any{"reason": "reload"})
	if s, _ := third.lastStatus("stash"); s != "" {
		t.Errorf("third generation status = %q", s)
	}
	if _, err := os.Stat(filepath.Join(agent, "powerline-footer", "active-stash-reload.json")); err == nil {
		t.Error("the hand-off file must be gone once consumed")
	}
}

func TestCopyShortcutUsesTheHostExec(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("clipboard command differs per platform")
	}
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "")
	r := newKeysRig(t, t.TempDir(), "sess-3", "copy me")
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	if !r.input(t, "\x1b[99;7u") {
		t.Fatal("ctrl+alt+c must be consumed")
	}
	calls := r.h.CallsTo("exec")
	if len(calls) == 0 {
		t.Fatal("no exec call")
	}
	args, _ := calls[0].Args["args"].([]any)
	want := "xclip"
	if runtime.GOOS == "darwin" {
		want = "pbcopy"
	}
	if calls[0].Args["command"] != "sh" || len(args) < 3 || args[0] != "-c" || args[2] != want {
		t.Errorf("exec = %v", calls[0].Args)
	}
	if script, _ := args[1].(string); !strings.Contains(script, "Y29weSBtZQ==") { // base64("copy me")
		t.Errorf("the script does not carry the text: %q", script)
	}
	if got := r.notes(); got[len(got)-1] != "info: Copied editor text" {
		t.Errorf("notes = %q", got)
	}
	if r.text() != "copy me" {
		t.Errorf("copy changed the editor: %q", r.text())
	}

	r.input(t, "\x1b[120;7u")
	if r.text() != "" {
		t.Errorf("cut left %q", r.text())
	}
	if got := r.notes(); got[len(got)-1] != "info: Cut editor text" {
		t.Errorf("notes = %q", got)
	}
}

func TestStashHistoryCommandWithNothingSaved(t *testing.T) {
	r := newKeysRig(t, t.TempDir(), "sess-4", "")
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	done := make(chan string, 1)
	go func() { done <- r.h.Command("stash-history", "") }()
	select {
	case failure := <-done:
		if failure != "" {
			t.Fatal(failure)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the command did not finish")
	}
	if got := r.notes(); got[len(got)-1] != "info: No prompt history yet" {
		t.Errorf("notes = %q", got)
	}
}

func TestTheRefusalsOfThePortedFeaturesAreGone(t *testing.T) {
	r := newKeysRig(t, t.TempDir(), "sess-5", "")
	// powerlineShortcuts and stashSharpSShortcut are no longer refused at session start.
	if err := os.WriteFile(filepath.Join(r.agent, "settings.json"), []byte(`{"powerline":{"welcome":false,"stashSharpSShortcut":true},"powerlineShortcuts":{"copyEditor":"ctrl+alt+y"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	for _, n := range r.notes() {
		if strings.Contains(n, "does not support") {
			t.Errorf("unexpected refusal: %s", n)
		}
	}
	if !r.input(t, "ß") {
		t.Error("stashSharpSShortcut makes ß a stash key")
	}
	if !r.input(t, "\x1b[121;7u") {
		t.Error("the configured copy shortcut ctrl+alt+y is live")
	}
}
