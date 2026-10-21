package powerline_footer_test

import (
	"strings"
	"testing"
	"time"
)

// The powerline editor through the wire: the host installs it, sends it keys, and its Ctrl+S and shortcut handling reaches the
// host as the calls the original's handlers make.

func editorInput(h *Host, data string) {
	h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "ui.editor.input", "args": map[string]any{"key": "editor-1", "data": data}}})
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func baseCalls(h *Host, op string) []HostCall {
	var out []HostCall
	for _, c := range h.CallsTo("ui.editor.base") {
		if c.Args["op"] == op {
			out = append(out, c)
		}
	}
	return out
}

func TestPrintableKeysReachTheDefaultEditorAndCtrlSDoesNot(t *testing.T) {
	h := tuiRig{settings: `{"powerline":{"welcome":false}}`}.start(t)
	editorInput(h, "a")
	waitUntil(t, "the default editor to get the key", func() bool { return len(baseCalls(h, "handleInput")) == 1 })
	if args, _ := baseCalls(h, "handleInput")[0].Args["args"].(map[string]any); args["data"] != "a" {
		t.Errorf("handleInput args %v", args)
	}

	editorInput(h, "\x13") // Ctrl+S with nothing stashed and nothing typed
	waitUntil(t, "the stash notification", func() bool {
		for _, n := range notifications(h) {
			if strings.Contains(n, "Nothing to stash") {
				return true
			}
		}
		return false
	})
	if got := len(baseCalls(h, "handleInput")); got != 1 {
		t.Errorf("Ctrl+S reached the default editor (%d handleInput calls)", got)
	}
}

func TestOtherNonPrintableKeysReachTheDefaultEditor(t *testing.T) {
	h := tuiRig{settings: `{"powerline":{"welcome":false}}`}.start(t)
	editorInput(h, "\x1b[A") // up
	waitUntil(t, "the default editor to get the key", func() bool { return len(baseCalls(h, "handleInput")) == 1 })
}

func TestTheEditorIsClearedWhenPowerlineIsToggledOff(t *testing.T) {
	h := tuiRig{settings: `{"powerline":{"welcome":false}}`}.start(t)
	if failure := h.Command("powerline", ""); failure != "" {
		t.Fatal(failure)
	}
	var cleared bool
	for _, c := range h.CallsTo("ui.setEditorComponent") {
		if c.Args["clear"] == true {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("ui.setEditorComponent was not cleared: %v", h.CallsTo("ui.setEditorComponent"))
	}
}

func TestCtrlSInTheEditorStashesThroughTheWire(t *testing.T) {
	r := newKeysRig(t, t.TempDir(), "ctrl-s-session", "draft")
	r.h.Fire("session_start", map[string]any{"reason": "resume"})
	if r.input(t, "\x13") {
		t.Error("the terminal hook consumed Ctrl+S")
	}
	editorInput(r.h, "\x13")
	waitUntil(t, "the draft to be stashed", func() bool { return r.text() == "" })
	editorInput(r.h, "\x13")
	waitUntil(t, "the draft to come back", func() bool { return r.text() == "draft" })
}

func TestACopyShortcutPressedInTheEditorIsHandledThere(t *testing.T) {
	r := newKeysRig(t, t.TempDir(), "copy-session", "")
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	editorInput(r.h, "\x1b[99;7u") // ctrl+alt+c, with an empty editor
	waitUntil(t, "the empty editor note", func() bool {
		for _, n := range r.notes() {
			if strings.Contains(n, "Editor is empty") {
				return true
			}
		}
		return false
	})
	if got := len(baseCalls(r.h, "handleInput")); got != 0 {
		t.Errorf("the shortcut also reached the default editor (%d calls)", got)
	}
}
