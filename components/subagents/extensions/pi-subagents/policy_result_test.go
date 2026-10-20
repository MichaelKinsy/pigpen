package pi_subagents

import (
	"strings"
	"testing"
)

func asst(t *testing.T, parts ...any) any {
	t.Helper()
	m := newObject()
	m.set("role", "assistant")
	m.set("content", parts)
	return m
}

func textPart(s string) any {
	o := newObject()
	o.set("type", "text")
	o.set("text", s)
	return o
}

func finalOf(msgs ...any) string { return getFinalOutput(msgs) }

func TestGetFinalOutput(t *testing.T) {
	footer := func(s string) string {
		return "\x1b[38;2;136;136;136m✻ Turn took " + s + "\x1b[0m"
	}
	tw(t, "get-final-output", "uses the last non-empty text part in the latest assistant message", func(t *testing.T) {
		equal(t, finalOf(asst(t, textPart(""), textPart("Summary"))), "Summary", "output")
	})
	tw(t, "get-final-output", "removes a trailing Pi turn-timing footer from final output", func(t *testing.T) {
		report := "## Review\n\nVERDICT: FINDINGS"
		equal(t, finalOf(asst(t, textPart(report+"\n\n"+footer("5m 54s (Total time 5m 54s · 2 turns)")))), report, "output")
	})
	tw(t, "get-final-output", "ignores a separate Pi turn-timing footer text part", func(t *testing.T) {
		report := "## Review\n\nVERDICT: CLEAN"
		equal(t, finalOf(asst(t, textPart(report), textPart(footer("2m 19s (Total time 2m 18s · 1 turn)")))), report, "output")
	})
	tw(t, "get-final-output", "preserves ordinary timing prose", func(t *testing.T) {
		text := "The benchmark turn took 5m 54s."
		equal(t, finalOf(asst(t, textPart(text))), text, "output")
	})
	tw(t, "get-final-output", "prefers final text over progress text in a multi-part assistant message", func(t *testing.T) {
		thinking := newObject()
		thinking.set("type", "thinking")
		thinking.set("thinking", "Cursor shell: shell $ npm test")
		equal(t, finalOf(asst(t, textPart("Working on the fix..."), thinking, textPart("Implemented: patch applied."))), "Implemented: patch applied.", "output")
	})
	tw(t, "get-final-output", "falls back to an older assistant message when the latest text is whitespace-only", func(t *testing.T) {
		equal(t, finalOf(asst(t, textPart("Earlier")), asst(t, textPart(" \n\t "))), "Earlier", "output")
	})
	tw(t, "get-final-output", "falls back to an older assistant message when the latest assistant message is tool-only", func(t *testing.T) {
		call := js(t, `{"type":"toolCall","name":"read","arguments":{"path":"README.md"}}`)
		equal(t, finalOf(asst(t, textPart("Earlier")), asst(t, call)), "Earlier", "output")
	})
	acceptanceBlock := func(fence string, body string) string {
		return strings.Join([]string{"Done", "```" + fence, body, "```"}, "\n")
	}
	tw(t, "get-final-output", "prefers an earlier explicit acceptance report over later summary-only text", func(t *testing.T) {
		report := acceptanceBlock("acceptance-report", `{"criteriaSatisfied":[{"id":"criterion-1","status":"satisfied","evidence":"verified"}],"changedFiles":["src/file.ts"]}`)
		equal(t, finalOf(asst(t, textPart(report)), asst(t, textPart("Done."))), report, "output")
	})
	tw(t, "get-final-output", "prefers an earlier json-fenced acceptance report over later summary-only text", func(t *testing.T) {
		report := acceptanceBlock("json", `{"criteriaSatisfied":[{"id":"criterion-1","status":"satisfied","evidence":"verified"}],"validationOutput":["tests passed"]}`)
		equal(t, finalOf(asst(t, textPart(report)), asst(t, textPart("Done."))), report, "output")
	})
	tw(t, "get-final-output", "preserves prose from a sibling text part when selecting an acceptance report", func(t *testing.T) {
		report := "```acceptance-report\n{}\n```"
		equal(t, finalOf(asst(t, textPart("Human-readable answer."), textPart(report))), "Human-readable answer.\n"+report, "output")
	})
	tw(t, "get-final-output", "recognizes underscore fences and snake_case generic report keys", func(t *testing.T) {
		for _, report := range []string{"```acceptance_report\n{}\n```", "```json\n" + `{"criteria_satisfied":[],"validation_output":["passed"]}` + "\n```"} {
			equal(t, finalOf(asst(t, textPart(report)), asst(t, textPart("Later summary."))), report, report)
		}
	})
	tw(t, "get-final-output", "does not prefer provider-error acceptance reports", func(t *testing.T) {
		bad := newObject()
		bad.set("role", "assistant")
		bad.set("content", []any{textPart("```acceptance-report\n{}\n```")})
		bad.set("stopReason", "error")
		bad.set("errorMessage", "provider transport failed")
		equal(t, finalOf(bad, asst(t, textPart("Done."))), "Done.", "output")
	})
	tw(t, "get-final-output", "returns empty output when all assistant text is empty or whitespace-only", func(t *testing.T) {
		equal(t, finalOf(asst(t, textPart("")), asst(t, textPart("\n\t "))), "", "output")
	})
	tw(t, "get-final-output", "does not use provider-error assistant text as fallback output", func(t *testing.T) {
		bad := newObject()
		bad.set("role", "assistant")
		bad.set("content", []any{textPart("temporary provider failure")})
		bad.set("stopReason", "error")
		bad.set("errorMessage", "provider transport failed")
		equal(t, finalOf(bad, asst(t, textPart(""))), "", "output")
	})
	tw(t, "get-final-output", "preserves surrounding whitespace on the selected non-empty text", func(t *testing.T) {
		equal(t, finalOf(asst(t, textPart(" \n Summary \n "))), " \n Summary \n ", "output")
	})
}

func TestForegroundToolCallCompaction(t *testing.T) {
	argsOf := func(s string) *jsObject { return js(t, s).(*jsObject) }
	tw(t, "foreground-tool-call-compaction", "stores compact tool-call summaries instead of raw message payloads", func(t *testing.T) {
		result := newObject()
		result.set("agent", "tester")
		result.set("task", "run checks")
		result.set("exitCode", 0.0)
		call := newObject()
		call.set("type", "toolCall")
		call.set("name", "write")
		call.set("arguments", js(t, `{"path":"/tmp/report.md","content":"`+strings.Repeat("x", 50000)+`"}`))
		msg := newObject()
		msg.set("role", "assistant")
		msg.set("content", []any{call})
		result.set("messages", []any{msg})
		out := compactForegroundResult(result)
		equal(t, isUndefined(out.vals["messages"]), true, "messages")
		equal(t, marshalJSON(out.vals["toolCalls"], ""), `[{"text":"write /tmp/report.md","expandedText":"write /tmp/report.md"}]`, "toolCalls")
	})
	tw(t, "foreground-tool-call-compaction", "keeps expanded generic tool-call previews bounded", func(t *testing.T) {
		args := argsOf(`{"payload":"` + strings.Repeat("x", 500) + `"}`)
		collapsed, expanded := formatToolCall("custom", args, false), formatToolCall("custom", args, true)
		if !(len(expanded) > len(collapsed) && len(expanded) < 200) {
			t.Errorf("collapsed %d, expanded %d", len(collapsed), len(expanded))
		}
	})
	tw(t, "foreground-tool-call-compaction", "does not keep an empty toolCalls array after compaction", func(t *testing.T) {
		result := newObject()
		result.set("agent", "tester")
		result.set("task", "run checks")
		result.set("messages", []any{})
		equal(t, isUndefined(compactForegroundResult(result).vals["toolCalls"]), true, "toolCalls")
	})
	tw(t, "foreground-tool-call-compaction", "keeps live tool argument previews on one terminal line", func(t *testing.T) {
		preview := extractToolArgsPreview(argsOf(`{"command":"set +e\r\n\t\u001b[31mrun\u001b[0m \u001b]0;title\u0007now\u0000"}`))
		equal(t, preview, "set +e run now", "preview")
		polNotMatches(t, preview, "[\r\n\t\x1b]")
	})
	tw(t, "foreground-tool-call-compaction", "keeps completed bash tool-call previews on one terminal line", func(t *testing.T) {
		preview := formatToolCall("bash", argsOf(`{"command":"first\n\u001b[32msecond\u001b[0m"}`), false)
		equal(t, preview, "$ first second", "preview")
		polNotMatches(t, preview, "[\r\n\t\x1b]")
	})
	tw(t, "foreground-tool-call-compaction", "does not split surrogate pairs when truncating previews", func(t *testing.T) {
		preview := extractToolArgsPreview(argsOf(`{"command":"` + strings.Repeat("😀", 31) + `"}`))
		equal(t, preview, strings.Repeat("😀", 28)+"...", "preview")
		// a lone surrogate is whitespace (read from its WTF-8 form)
		lone := newObject()
		lone.set("command", "safe\xed\xa0\x80tail\xed\xb0\x80end")
		equal(t, extractToolArgsPreview(lone), "safe tail end", "lone surrogates")
	})
	tw(t, "foreground-tool-call-compaction", "normalizes fallback argument keys before display", func(t *testing.T) {
		preview := extractToolArgsPreview(argsOf(`{"bad\r\n\t\u001b[31mkey\u001b[0m\u0000":"value"}`))
		equal(t, preview, "bad key=value", "preview")
		polNotMatches(t, preview, "[\x00-\x1f\x7f-\u009f]")
	})
	tw(t, "foreground-tool-call-compaction", "discards terminal string payloads while preserving readable suffixes", func(t *testing.T) {
		for _, control := range []string{"\x1b]0;title\x07", "\x1bPpayload\x1b\\", "\u009d0;title\u009c"} {
			o := newObject()
			o.set("command", "before"+control+"after")
			equal(t, extractToolArgsPreview(o), "before after", control)
		}
	})
	tw(t, "foreground-tool-call-compaction", "formats array-based web search previews clearly", func(t *testing.T) {
		equal(t, extractToolArgsPreview(argsOf(`{"queries":["Chrome native messaging manifest path macOS","Chromium native messaging path macOS"],"workflow":"none"}`)), "Chrome native messaging manifest path macOS (+1 more)", "preview")
	})
	tw(t, "foreground-tool-call-compaction", "formats fetch_content urls clearly", func(t *testing.T) {
		equal(t, extractToolArgsPreview(argsOf(`{"urls":["https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging","https://example.com/backup"]}`)), "https://developer.chrome.com/docs/extensions/develop/conc...", "preview")
	})
	tw(t, "foreground-tool-call-compaction", "falls back to generic array previews", func(t *testing.T) {
		equal(t, extractToolArgsPreview(argsOf(`{"ids":["run-a","run-b","run-c"]}`)), "ids=run-a (+2 more)", "preview")
	})
}
