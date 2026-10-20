package pi_subagents

import "testing"

// Twins of test/unit/watchdog-turn-delta.test.ts.

func wdList(t *testing.T, raw string) []any {
	t.Helper()
	l, _ := wdObj(t, raw).([]any)
	return l
}

func TestWatchdogTurnDeltaTwins(t *testing.T) {
	const f = "watchdog-turn-delta"
	tw(t, f, "formats user prompt, assistant text, thinking, tool calls, and tool results", func(t *testing.T) {
		delta := formatWatchdogTurnDelta(watchdogTurnDeltaInput{
			IncludeUserPrompt: true, UserPrompt: "Implement the parser.",
			Messages: wdList(t, `[{"role":"assistant","content":[
				{"type":"text","text":"I will inspect the settings."},
				{"type":"thinking","thinking":"The settings parser is the risky part."},
				{"type":"toolCall","name":"bash","arguments":{"command":"printf 'a\\nb'"}}]}]`),
			Events: wdList(t, `[{"type":"tool_execution_end","toolName":"bash","result":{"content":[{"type":"text","text":"a\nb"}]},"isError":false}]`),
		})
		wdMatch(t, delta, `User prompt:\nImplement the parser\.`)
		wdMatch(t, delta, `Assistant:\nI will inspect the settings\.`)
		wdMatch(t, delta, `Thinking:\nThe settings parser is the risky part\.`)
		wdMatch(t, delta, `Tool call: bash\nArguments:\ncommand:`)
		wdMatch(t, delta, `printf 'a\\nb'`)
		wdMatch(t, delta, `Tool result: bash\nResult:\na\nb`)
	})
	tw(t, f, "uses a successful tool diff instead of noisy raw edit blobs", func(t *testing.T) {
		delta := formatWatchdogTurnDelta(watchdogTurnDeltaInput{Messages: wdList(t, `[
			{"role":"assistant","content":[{"type":"toolCall","name":"edit","arguments":{"path":"src/file.ts","oldText":"huge old text","newText":"huge new text","edits":[{"oldText":"nested old text","newText":"nested new text"}]}}]},
			{"role":"toolResult","toolName":"edit","content":"oldText: huge\nnewText: huge","details":{"diff":"--- a/file.ts\n+++ b/file.ts\n@@\n-old\n+new"},"isError":false}]`)})
		wdMatch(t, delta, `Tool result: edit\nDiff:\n--- a/file\.ts`)
		wdMatch(t, delta, `oldText: \[omitted 13 chars; use tool result diff\]`)
		wdMatch(t, delta, `newText: \[omitted 13 chars; use tool result diff\]`)
		wdMatch(t, delta, `oldText: \[omitted 15 chars; use tool result diff\]`)
		wdMatch(t, delta, `newText: \[omitted 15 chars; use tool result diff\]`)
		for _, re := range []string{`huge old text`, `huge new text`, `nested old text`, `nested new text`, `oldText: huge`} {
			wdNoMatch(t, delta, re)
		}
	})
	tw(t, f, "marks failed tool results from isError and does not replace them with diffs", func(t *testing.T) {
		delta := formatWatchdogTurnDelta(watchdogTurnDeltaInput{Messages: wdList(t, `[{"role":"toolResult","toolName":"edit","isError":true,"content":[{"type":"text","text":"edit failed"}],"details":{"diff":"should not render"}}]`)})
		wdMatch(t, delta, `Tool result: edit\nError: tool reported an error\nOutput:\nedit failed`)
		wdNoMatch(t, delta, `should not render`)
	})
	tw(t, f, "formats turn_end events with assistant message and tool results", func(t *testing.T) {
		delta := formatWatchdogTurnDelta(watchdogTurnDeltaInput{Events: wdList(t, `[{"type":"turn_end","message":{"role":"assistant","content":[
			{"type":"text","text":"I will run a check."},{"type":"toolCall","name":"bash","arguments":{"command":"npm test"}}]},
			"toolResults":[{"role":"toolResult","toolName":"bash","isError":true,"content":[{"type":"text","text":"tests failed"}]}]}]`)})
		wdMatch(t, delta, `Assistant:\nI will run a check\.`)
		wdMatch(t, delta, `Tool call: bash\nArguments:\ncommand: npm test`)
		wdMatch(t, delta, `Tool result: bash\nError: tool reported an error\nOutput:\ntests failed`)
	})
	tw(t, f, "filters watchdog warning custom messages from its own review stream", func(t *testing.T) {
		msg := wdObj(t, `{"role":"custom","customType":"subagent_watchdog_warning","content":"prior watchdog warning"}`)
		if _, ok := formatWatchdogReviewMessage(msg, false); ok {
			t.Error("a watchdog warning message was formatted")
		}
		wdEq(t, formatWatchdogTurnDelta(watchdogTurnDeltaInput{Messages: []any{msg}}), "", "delta")
	})
	tw(t, f, "marks final assistant stop when represented", func(t *testing.T) {
		delta := formatWatchdogTurnDelta(watchdogTurnDeltaInput{Messages: wdList(t, `[{"role":"assistant","content":"Done.","stopReason":"stop"}]`), FinalAssistantStop: true})
		wdMatch(t, delta, `Assistant stop: stop`)
		wdMatch(t, delta, `Final assistant stop: stop without tool call`)
	})
	tw(t, f, "distinguishes a validated structured terminal from an ordinary empty terminal", func(t *testing.T) {
		messages := wdList(t, `[{"role":"assistant","content":[],"stopReason":"stop"}]`)
		ordinary := formatWatchdogTurnDelta(watchdogTurnDeltaInput{Messages: messages, FinalAssistantStop: true})
		structured := formatWatchdogTurnDelta(watchdogTurnDeltaInput{Messages: messages, FinalAssistantStop: true, StructuredTerminal: true})
		wdMatch(t, ordinary, `Assistant: \(no text\)`)
		wdMatch(t, ordinary, `stop without tool call`)
		wdNoMatch(t, structured, `\(no text\)|stop without tool call`)
		wdMatch(t, structured, `validated structured output is the terminal response; no prose is required`)
		wdMatch(t, structured, `validated structured output completed the response`)
	})
	tw(t, f, "keeps independent tool evidence visible at a structured terminal", func(t *testing.T) {
		delta := formatWatchdogTurnDelta(watchdogTurnDeltaInput{StructuredTerminal: true, Messages: wdList(t, `[
			{"role":"toolResult","toolName":"bash","isError":true,"content":"tests failed"},
			{"role":"assistant","content":[],"stopReason":"stop"}]`)})
		wdMatch(t, delta, `Tool result: bash\nError: tool reported an error\nOutput:\ntests failed`)
		wdMatch(t, delta, `validated structured output is the terminal response`)
	})
}
