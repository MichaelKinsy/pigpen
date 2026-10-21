package powerline_footer_test

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Delayed send and the capture of prompts typed during a compaction, through the wire: the powerline editor over a default
// editor that answers from a text buffer.

func pendingSendRow(t *testing.T, h *Host) string {
	t.Helper()
	var row string
	for _, c := range h.CallsTo("ui.setWidget") {
		if c.Args["key"] == "powerline-pending-send" {
			row = ""
			if lines, ok := c.Args["content"].([]any); ok && len(lines) > 0 {
				row, _ = lines[0].(string)
			}
		}
	}
	return row
}

func (b *bashHost) setText(text string) {
	b.mu.Lock()
	b.text = text
	b.mu.Unlock()
}

func (b *bashHost) draft() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text
}

func TestADelayedPromptIsSentThroughTheEditorAfterTheDelay(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false,"sendDelayMs":1500}}`)
	b.setText("fix the test")
	editorInput(b.h, "\r")
	waitUntil(t, "the prompt to wait", func() bool { return strings.Contains(pendingSendRow(t, b.h), "sending in") })
	if got := b.draft(); got != "" {
		t.Errorf("the editor still holds %q", got)
	}
	if ops := b.opsLog(); !slices.Equal(ops, []string{"addToHistory:fix the test", "setText:"}) {
		t.Errorf("before the delay: %q", ops)
	}
	b.setText("also lint") // typed during the countdown
	waitUntil(t, "the prompt to be sent", func() bool { return slices.Contains(b.opsLog(), "handleInput:\r") })
	waitUntil(t, "the draft to come back", func() bool { return b.draft() == "also lint" })
	ops := b.opsLog()
	want := []string{"addToHistory:fix the test", "setText:", "setText:fix the test", "handleInput:\r", "setText:also lint"}
	if !slices.Equal(ops, want) {
		t.Errorf("ops %q, want %q", ops, want)
	}
	waitUntil(t, "the widget to clear", func() bool { return pendingSendRow(t, b.h) == "" })
}

func TestTheCountdownRepaintsEverySecond(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false,"sendDelayMs":4000}}`)
	b.setText("hold on")
	editorInput(b.h, "\r")
	waitUntil(t, "the first row", func() bool { return strings.Contains(pendingSendRow(t, b.h), "sending in 4s") })
	waitUntil(t, "the countdown to tick", func() bool { return strings.Contains(pendingSendRow(t, b.h), "sending in 3s") })
	editorInput(b.h, "\x1b") // take it back so the test does not wait for the send
}

func TestEscapeTakesADelayedPromptBack(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false,"sendDelayMs":600}}`)
	b.setText("fix the test")
	editorInput(b.h, "\r")
	waitUntil(t, "the prompt to wait", func() bool { return strings.Contains(pendingSendRow(t, b.h), "sending in") })
	b.setText("also lint")
	editorInput(b.h, "\x1b")
	waitUntil(t, "the prompt to return", func() bool { return b.draft() == "fix the test\n\nalso lint" })
	time.Sleep(900 * time.Millisecond)
	if slices.Contains(b.opsLog(), "handleInput:\r") {
		t.Errorf("the prompt was sent after Esc: %q", b.opsLog())
	}
	waitUntil(t, "the widget to clear", func() bool { return pendingSendRow(t, b.h) == "" })
}

func TestEnterOnAnEmptyEditorSendsTheWaitingPromptNow(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false,"sendDelayMs":30000}}`)
	b.setText("now please")
	editorInput(b.h, "\r")
	waitUntil(t, "the prompt to wait", func() bool { return strings.Contains(pendingSendRow(t, b.h), "sending in") })
	editorInput(b.h, "\r") // the editor is empty
	waitUntil(t, "the prompt to be sent", func() bool { return slices.Contains(b.opsLog(), "handleInput:\r") })
}

func TestSlashCommandsAreNotDelayed(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false,"sendDelayMs":30000}}`)
	b.setText("/model")
	editorInput(b.h, "\r")
	waitUntil(t, "Enter to reach the default editor", func() bool { return slices.Contains(b.opsLog(), "handleInput:\r") })
	if pendingSendRow(t, b.h) != "" {
		t.Errorf("a slash command waited: %q", pendingSendRow(t, b.h))
	}
}

func TestBashModeSubmitsAtOnceEvenWithADelay(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false,"sendDelayMs":30000}}`)
	b.h.Command("bash-mode", "on")
	b.setText("echo hi")
	editorInput(b.h, "\r")
	waitUntil(t, "the command to run", func() bool { return b.draft() == "" })
	if pendingSendRow(t, b.h) != "" || slices.Contains(b.opsLog(), "addToHistory:echo hi") {
		t.Errorf("the shell command was delayed: row %q ops %q", pendingSendRow(t, b.h), b.opsLog())
	}
}

func TestAPromptTypedWhileCompactingIsQueuedForAfterIt(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	b.h.Fire("session_before_compact", map[string]any{})
	b.setText("run the tests")
	editorInput(b.h, "\r")
	waitUntil(t, "the queue notice", func() bool { return hasNote(b.h, "info: Queued for after compaction (") })
	if got := b.draft(); got != "" {
		t.Errorf("the editor still holds %q", got)
	}
	if slices.Contains(b.opsLog(), "handleInput:\r") {
		t.Errorf("the prompt also reached Pi's submit path: %q", b.opsLog())
	}
	if !slices.Contains(b.opsLog(), "addToHistory:run the tests") {
		t.Errorf("the editor did not remember it: %q", b.opsLog())
	}
}

func TestASlashCommandTypedWhileCompactingGoesToTheEditor(t *testing.T) {
	b := startBashHost(t, `{"powerline":{"welcome":false}}`)
	b.h.Fire("session_before_compact", map[string]any{})
	b.setText("/model")
	editorInput(b.h, "\r")
	waitUntil(t, "Enter to reach the default editor", func() bool { return slices.Contains(b.opsLog(), "handleInput:\r") })
	if hasNote(b.h, "Queued for after compaction") {
		t.Error("a slash command was queued")
	}
}
