package stretchtext_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A section the host already holds from an earlier turn is taken away again when the reader opened nothing.
func TestAStaleReadNoteIsTakenOutOfTheSystemPrompt(t *testing.T) {
	h := startExtension(t, HostOptions{})
	h.theme()
	h.Fire("session_start", map[string]any{"reason": "startup"})
	raw := h.Fire("before_agent_start", map[string]any{
		"prompt":              "hello",
		"systemPrompt":        "base",
		"systemPromptOptions": map[string]any{"sections": map[string]any{"stretchtext_read": "stale"}},
	})
	var result struct {
		Options struct {
			Sections map[string]string `json:"sections"`
		} `json:"_pigPromptOptions"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("before_agent_start result %s: %v", raw, err)
	}
	if note, ok := result.Options.Sections["stretchtext_read"]; ok {
		t.Fatalf("the stale read note stayed in the system prompt: %q", note)
	}
}

// Shutdown joins the redraw worker: a redraw in flight finishes before the session is gone.
func TestShutdownWaitsForARedrawInFlight(t *testing.T) {
	release := make(chan struct{})
	h := startExtension(t, HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.tui.invalidate" {
			<-release
		}
		return map[string]any{}, ""
	}})
	h.theme()
	h.Fire("session_start", map[string]any{"reason": "startup"})
	waitFor(t, "the attach redraw to reach the host", count(h, "ui.tui.invalidate"))
	done := make(chan struct{})
	go func() {
		h.Fire("session_shutdown", map[string]any{"reason": "quit"})
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("session_shutdown returned while a redraw was still in flight")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("session_shutdown never returned after the redraw finished")
	}
	if !strings.Contains(strings.Join(methods(h), " "), "ui.tui.requestRender") {
		t.Errorf("the redraw in flight did not finish: %v", methods(h))
	}
}

func methods(h *Host) []string {
	var out []string
	for _, c := range h.Calls() {
		out = append(out, c.Method)
	}
	return out
}
