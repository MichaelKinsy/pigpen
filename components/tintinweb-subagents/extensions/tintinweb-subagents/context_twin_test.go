package tintinweb_subagents

import (
	"strings"
	"testing"
)

// Twins of test/context.test.ts and test/child-context.test.ts.

func TestContextTwins(t *testing.T) {
	const f = "context"
	userMsg := func(content any) map[string]any {
		return obj{"type": "message", "message": obj{"role": "user", "content": content}}
	}
	assistantMsg := func(blocks ...any) map[string]any {
		return obj{"type": "message", "message": obj{"role": "assistant", "content": blocks}}
	}
	text := func(s string) any { return obj{"type": "text", "text": s} }
	build := func(entries ...map[string]any) string { return buildParentContext(entries) }

	tw(t, f, "joins multiple text blocks with newlines", func(t *testing.T) {
		eq(t, extractText([]any{text("a"), text("b")}), "a\nb")
	})
	tw(t, f, "filters out non-text blocks (tool_use, etc.)", func(t *testing.T) {
		eq(t, extractText([]any{text("keep"), obj{"type": "tool_use", "name": "x", "input": obj{}}, text("also keep")}), "keep\nalso keep")
	})
	tw(t, f, "treats a text block with missing text field as empty", func(t *testing.T) {
		eq(t, extractText([]any{obj{"type": "text"}, text("x")}), "\nx")
	})
	tw(t, f, "returns empty string for an empty content array", func(t *testing.T) {
		eq(t, extractText([]any{}), "")
	})
	tw(t, f, "returns empty string for an empty branch", func(t *testing.T) {
		eq(t, build(), "")
	})
	tw(t, f, "returns empty string when no entries produce extractable content", func(t *testing.T) {
		// toolResult is skipped, empty-summary compaction is skipped
		eq(t, build(obj{"type": "message", "message": obj{"role": "tool_result", "content": "..."}}, obj{"type": "compaction", "summary": ""}), "")
	})
	tw(t, f, "wraps a user+assistant exchange with the parent-context header and task footer", func(t *testing.T) {
		out := build(userMsg("hello"), assistantMsg(text("hi back")))
		for _, want := range []string{"# Parent Conversation Context", "[User]: hello", "[Assistant]: hi back", "[User]: hello\n\n[Assistant]: hi back"} {
			eq(t, strings.Contains(out, want), true)
		}
		eq(t, strings.HasSuffix(out, "# Your Task (below)\n"), true)
		eq(t, out, "# Parent Conversation Context\nThe following is the conversation history from the parent session that spawned you.\n"+
			"Use this context to understand what has been discussed and decided so far.\n\n[User]: hello\n\n[Assistant]: hi back\n\n---\n# Your Task (below)\n")
	})
	tw(t, f, "accepts user messages whose content is content-blocks (not just a string)", func(t *testing.T) {
		eq(t, strings.Contains(build(userMsg([]any{text("from blocks")})), "[User]: from blocks"), true)
	})
	tw(t, f, "includes compaction summaries inline in their original position", func(t *testing.T) {
		out := build(userMsg("orig question"), obj{"type": "compaction", "summary": "we discussed X"}, assistantMsg(text("follow-up")))
		u, s, a := strings.Index(out, "[User]: orig question"), strings.Index(out, "[Summary]: we discussed X"), strings.Index(out, "[Assistant]: follow-up")
		eq(t, u >= 0 && u < s && s < a, true)
	})
	tw(t, f, "skips tool_result messages — they're too verbose for inherited context", func(t *testing.T) {
		out := build(userMsg("real user"), obj{"type": "message", "message": obj{"role": "tool_result", "content": "noisy tool output"}}, assistantMsg(text("real assistant")))
		eq(t, strings.Contains(out, "noisy tool output"), false)
		eq(t, strings.Contains(out, "[User]: real user") && strings.Contains(out, "[Assistant]: real assistant"), true)
	})
	tw(t, f, "trims and skips whitespace-only messages", func(t *testing.T) {
		out := build(userMsg("   \n  "), assistantMsg(text("non-empty")))
		eq(t, strings.Contains(out, "[User]:"), false)
		eq(t, strings.Contains(out, "[Assistant]: non-empty"), true)
	})
	tw(t, f, "ignores assistant messages whose only content is non-text blocks", func(t *testing.T) {
		// Assistant emitted only a tool_use — nothing extractable, so it shouldn't appear
		out := build(userMsg("question"), assistantMsg(obj{"type": "tool_use", "name": "x", "input": obj{}}))
		eq(t, strings.Contains(out, "[User]: question"), true)
		eq(t, strings.Contains(out, "[Assistant]:"), false)
	})

	const c = "child-context"
	tskip(t, c, "is scoped to the child async branch", "the original marks a child's resource loading with an AsyncLocalStorage scope in the parent process; a child here is a separate process marked by the TINTINWEB_SUBAGENT_DEPTH environment variable (TestAChildProcessRegistersNothing), which has no async scope to test")
	tw(t, c, "prevents a child resource load from creating another extension manager", func(t *testing.T) {
		// A child pig loads this extension too; it must be a no-op there: nothing registered, no listener.
		t.Setenv(depthEnv, "1")
		e := Extension()
		host := StartHost(t, e, HostOptions{Cwd: t.TempDir()})
		host.FireAll("session_start", obj{"reason": "startup"})
		eq(t, len(host.CallsTo("registerTool")), 0)
		eq(t, len(host.CallsTo("events.on")), 0)
	})
}
