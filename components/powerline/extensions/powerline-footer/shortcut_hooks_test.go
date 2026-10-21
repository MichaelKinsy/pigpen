package powerline_footer_test

import (
	"os"
	"path/filepath"
	"testing"
)

// The shortcut actions that belong to other features reach them: ctrl+alt+q opens the queue picker and the reply shortcut quotes.

func TestTheQueuePickerShortcutOpensThePicker(t *testing.T) {
	agent := t.TempDir()
	opened := make(chan string, 4)
	r := &keysRig{agent: agent}
	r.custom = func(args map[string]any) map[string]any {
		opened <- "custom"
		return CustomResult(nil)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"powerline":{"welcome":false}}`), 0o644)
	startKeysRig(t, r, "queue-shortcut")
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	if !r.input(t, "\x1b[113;7u") { // ctrl+alt+q
		t.Fatal("ctrl+alt+q was not consumed")
	}
	waitUntil(t, "the picker or its notice", func() bool { return len(opened) > 0 || len(r.h.CallsTo("ui.select")) > 0 || len(r.notes()) > 0 })
}

func TestTheReplyShortcutRunsReply(t *testing.T) {
	agent := t.TempDir()
	os.MkdirAll(agent, 0o755)
	os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"powerline":{"welcome":false},"powerlineShortcuts":{"reply":"ctrl+alt+r"}}`), 0o644)
	r := newKeysRig(t, agent, "reply-shortcut", "")
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	if !r.input(t, "\x1b[114;7u") { // ctrl+alt+r
		t.Fatal("the reply shortcut was not consumed")
	}
	waitUntil(t, "reply to answer", func() bool { return len(r.notes()) > 0 })
	if got := len(baseCalls(r.h, "handleInput")); got != 0 {
		t.Errorf("the shortcut also reached the default editor (%d calls)", got)
	}
}
