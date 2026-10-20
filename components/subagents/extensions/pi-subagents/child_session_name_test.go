package pi_subagents

import (
	"strings"
	"testing"
)

// Twins of child-session-name and opencode-session-headers.

func TestChildSessionNameTwins(t *testing.T) {
	const f = "child-session-name"
	tw(t, f, "combines agent and task excerpt", func(t *testing.T) {
		eq(t, deriveChildSessionName("reviewer", "Review the diff since main", ""), "reviewer: Review the diff since main", "name")
	})
	tw(t, f, "prefers a workflow label over the task", func(t *testing.T) {
		eq(t, deriveChildSessionName("scout", "ignored task text", "find-callers"), "scout: find-callers", "name")
	})
	tw(t, f, "falls back to the bare agent when no task or label is usable", func(t *testing.T) {
		eq(t, deriveChildSessionName("worker", "", ""), "worker", "no task")
		eq(t, deriveChildSessionName("worker", "   ", ""), "worker", "blank task")
	})
	tw(t, f, "never builds a name from a redacted prompt", func(t *testing.T) {
		eq(t, deriveChildSessionName("worker", promptRedacted, ""), "worker", "agent only")
		eq(t, deriveChildSessionName("", promptRedacted, ""), "", "nothing")
	})
	tw(t, f, "collapses newlines and control characters into a single line", func(t *testing.T) {
		name := deriveChildSessionName("scout", "line one\nline two", "")
		eq(t, name, "scout: line one line two", "name")
		if strings.Contains(name, "\n") {
			t.Error("newline kept")
		}
	})
	tw(t, f, "caps the final name at CHILD_SESSION_NAME_MAX_CHARS", func(t *testing.T) {
		name := deriveChildSessionName("worker", strings.Repeat("x", 500), "")
		if name == "" || jsLength(name) > 80 || !strings.HasSuffix(name, "...") {
			t.Errorf("name %q", name)
		}
	})
	tw(t, f, "returns undefined when there is nothing to name", func(t *testing.T) {
		eq(t, deriveChildSessionName("", "", ""), "", "name")
	})
}

func TestOpencodeSessionHeadersTwins(t *testing.T) {
	const f = "opencode-session-headers"
	want := map[string]any{"x-opencode-session": "session-1", "x-opencode-client": "pi"}
	tw(t, f, "emits pi's OpenCode session-routing headers for OpenCode models", func(t *testing.T) {
		eq(t, opencodeSessionHeaders("opencode", "https://opencode.ai/zen/v1", "session-1"), want, "opencode")
		eq(t, opencodeSessionHeaders("opencode-go", "https://opencode.ai/go/v1", "session-1"), want, "opencode-go")
		eq(t, opencodeSessionHeaders("custom-gateway", "https://opencode.ai/zen/v1", "session-1"), want, "gateway on the host")
	})
	tw(t, f, "returns undefined for every other provider or without a session id", func(t *testing.T) {
		eq(t, opencodeSessionHeaders("opencode", "https://opencode.ai/zen/v1", "") == nil, true, "no session")
		eq(t, opencodeSessionHeaders("anthropic", "https://api.anthropic.com", "session-1") == nil, true, "anthropic")
		eq(t, opencodeSessionHeaders("openai-compat", "https://proxy.example/v1", "session-1") == nil, true, "proxy")
		eq(t, opencodeSessionHeaders("openai-compat", "https://opencode.ai.evil.example/v1", "session-1") == nil, true, "lookalike host")
	})
}
