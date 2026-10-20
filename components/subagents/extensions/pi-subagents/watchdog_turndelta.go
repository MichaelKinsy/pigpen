package pi_subagents

import (
	"strings"
)

// The turn delta the reviewer reads (watchdog/turn-delta.ts). Messages and events are decoded JSON in the ordered form of
// jsjson.go (*jsObject, []any, string, float64, bool, nil).

type watchdogTurnDeltaInput struct {
	UserPrompt         string
	IncludeUserPrompt  bool
	Messages           []any
	Events             []any
	FinalAssistantStop bool
	// StructuredTerminal: a validated structured output is the authoritative terminal response.
	StructuredTerminal bool
}

// wdProp is `value.key` for an object value; present is false for a missing key and for undefined.
func wdProp(v any, key string) (any, bool) {
	o, ok := v.(*jsObject)
	if !ok {
		return nil, false
	}
	return dynGet(o, key)
}

func wdPropV(v any, key string) any { x, _ := wdProp(v, key); return x }

// wdNullish is the first of values that is neither null nor undefined (`a ?? b ?? c`); nil when all are.
func wdNullish(values ...any) any {
	for _, v := range values {
		if v != nil {
			if _, u := v.(undef); !u {
				return v
			}
		}
	}
	return nil
}

func wdPropStr(v any, key string) string { s, _ := wdPropV(v, key).(string); return s }

func wdTextFromContent(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, block := range c {
			text := ""
			switch b := block.(type) {
			case string:
				text = b
			case *jsObject:
				typ, _ := wdPropV(b, "type").(string)
				switch {
				case typ == "text":
					if s, ok := wdPropV(b, "text").(string); ok {
						text = s
					}
				case typ == "thinking":
					if s, ok := wdPropV(b, "thinking").(string); ok {
						text = "Thinking:\n" + s
					}
				case typ == "toolCall":
					text = wdFormatToolCall(wdPropV(b, "name"), wdNullish(wdPropV(b, "input"), wdPropV(b, "args"), wdPropV(b, "arguments")))
				}
				if text == "" {
					if s, ok := wdPropV(b, "content").(string); ok {
						text = s
					}
				}
			}
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case nil, undef:
		return ""
	}
	return wdFormatValue(content, "")
}

func wdFormatValue(value any, indent string) string {
	switch v := value.(type) {
	case string:
		return v
	case undef:
		return "undefined"
	case nil:
		return "null"
	case float64:
		return jsNumber(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []any:
		lines := make([]string, len(v))
		for i, item := range v {
			lines[i] = indent + "- " + wdFormatValue(item, indent+"  ")
		}
		return strings.Join(lines, "\n")
	case *jsObject:
		var lines []string
		for _, key := range v.order() {
			item := v.vals[key]
			if s, ok := item.(string); ok && strings.Contains(s, "\n") {
				lines = append(lines, indent+key+":\n"+s)
			} else if _, u := item.(undef); u {
				lines = append(lines, indent+key+": undefined")
			} else {
				lines = append(lines, indent+key+": "+wdFormatValue(item, indent+"  "))
			}
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func wdRedactEditWriteInput(input any) any {
	switch v := input.(type) {
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = wdRedactEditWriteInput(e)
		}
		return out
	case *jsObject:
		out := newObject()
		for _, key := range v.order() {
			value := v.vals[key]
			if s, ok := value.(string); ok && (key == "oldText" || key == "newText" || key == "content") {
				out.set(key, "[omitted "+itoa(wdLen(s))+" chars; use tool result diff]")
			} else {
				out.set(key, wdRedactEditWriteInput(value))
			}
		}
		return out
	}
	return input
}

func wdFormatToolCall(name, input any) string {
	toolName := "tool"
	if s, ok := name.(string); ok && s != "" {
		toolName = s
	}
	if input == nil {
		input = newObject()
	}
	if toolName == "edit" || toolName == "write" {
		input = wdRedactEditWriteInput(input)
	}
	return strings.Join([]string{"Tool call: " + toolName, "Arguments:", wdFormatValue(input, "")}, "\n")
}

func wdFormatToolResult(name string, content, details, errValue any, isError any) string {
	failed := isError == true || jsTruthy(errValue)
	lines := []string{"Tool result: " + name}
	if failed {
		msg := "tool reported an error"
		if jsTruthy(errValue) {
			if s, ok := errValue.(string); ok {
				msg = s
			} else {
				msg = wdFormatValue(errValue, "")
			}
		}
		lines = append(lines, "Error: "+msg)
	}
	if diff, ok := wdPropV(details, "diff").(string); !failed && ok && wdIsObject(details) {
		lines = append(lines, "Diff:", diff)
	} else if body := wdTextFromContent(content); body != "" {
		label := "Result:"
		if failed {
			label = "Output:"
		}
		lines = append(lines, label, body)
	}
	return strings.Join(lines, "\n")
}

func wdIsObject(v any) bool { _, ok := v.(*jsObject); return ok }

func wdMsg(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func wdMessagesFromEvent(event any) []any {
	in, ok := event.(*jsObject)
	if !ok {
		return nil
	}
	evType := func(name string) bool { return wdPropV(in, "type") == name || wdPropV(in, "event") == name }
	if evType("turn_end") {
		var out []any
		if m := wdPropV(in, "message"); jsTruthy(m) {
			out = append(out, m)
		}
		if results, ok := wdPropV(in, "toolResults").([]any); ok {
			for _, r := range results {
				if jsTruthy(r) {
					out = append(out, r)
				}
			}
		}
		return out
	}
	if m := wdPropV(in, "message"); jsTruthy(m) {
		return []any{m}
	}
	if evType("tool_execution_start") {
		return []any{wdMsg("role", "toolCall", "name", wdNullish(wdPropV(in, "toolName"), wdPropV(in, "name")), "input", wdNullish(wdPropV(in, "args"), wdPropV(in, "input")))}
	}
	if evType("tool_execution_end") {
		result, _ := wdPropV(in, "result").(*jsObject)
		var rContent, rDetails any
		if result != nil {
			rContent, rDetails = wdPropV(result, "content"), wdPropV(result, "details")
		}
		return []any{wdMsg("role", "toolResult", "toolName", wdNullish(wdPropV(in, "toolName"), wdPropV(in, "name")),
			"content", wdNullish(rContent, wdPropV(in, "content"), wdPropV(in, "result")), "details", wdNullish(rDetails, wdPropV(in, "details")),
			"error", wdPropV(in, "error"), "isError", wdPropV(in, "isError"))}
	}
	if evType("tool_result") {
		return []any{wdMsg("role", "toolResult", "toolName", wdNullish(wdPropV(in, "toolName"), wdPropV(in, "name")), "content", wdPropV(in, "content"),
			"details", wdPropV(in, "details"), "error", wdPropV(in, "error"), "isError", wdPropV(in, "isError"))}
	}
	return nil
}

// formatWatchdogReviewMessage formats one message for the reviewer; ok is false for a message the reviewer does not see.
func formatWatchdogReviewMessage(message any, structuredTerminal bool) (string, bool) {
	in, ok := message.(*jsObject)
	if !ok {
		return "", false
	}
	role, _ := wdPropV(in, "role").(string)
	if role == "custom" && wdPropV(in, "customType") == subagentWatchdogWarningType {
		return "", false
	}
	switch role {
	case "assistant":
		text := wdTextFromContent(wdPropV(in, "content"))
		var lines []string
		switch {
		case text != "":
			lines = []string{"Assistant:", text}
		case structuredTerminal:
			lines = []string{"Assistant: (validated structured output is the terminal response; no prose is required)"}
		default:
			lines = []string{"Assistant: (no text)"}
		}
		if wdPropV(in, "stopReason") == "stop" {
			lines = append(lines, "Assistant stop: stop")
		}
		return strings.Join(lines, "\n"), true
	case "toolCall":
		return wdFormatToolCall(wdNullish(wdPropV(in, "name"), wdPropV(in, "toolName")), wdNullish(wdPropV(in, "input"), wdPropV(in, "args"), wdPropV(in, "arguments"))), true
	case "toolResult", "tool":
		name := "tool"
		if n, ok := wdNullish(wdPropV(in, "toolName"), wdPropV(in, "name")).(string); ok {
			name = n
		}
		return wdFormatToolResult(name, wdPropV(in, "content"), wdPropV(in, "details"), wdPropV(in, "error"), wdPropV(in, "isError")), true
	case "user":
		if text := wdTextFromContent(wdPropV(in, "content")); text != "" {
			return "User:\n" + text, true
		}
	}
	return "", false
}

func formatWatchdogTurnDelta(in watchdogTurnDeltaInput) string {
	var sections []string
	if in.IncludeUserPrompt && jsTrim(in.UserPrompt) != "" {
		sections = append(sections, "User prompt:\n"+in.UserPrompt)
	}
	for _, m := range in.Messages {
		if s, ok := formatWatchdogReviewMessage(m, in.StructuredTerminal); ok {
			sections = append(sections, s)
		}
	}
	for _, e := range in.Events {
		for _, m := range wdMessagesFromEvent(e) {
			if s, ok := formatWatchdogReviewMessage(m, in.StructuredTerminal); ok {
				sections = append(sections, s)
			}
		}
	}
	if in.FinalAssistantStop {
		if in.StructuredTerminal {
			sections = append(sections, "Final assistant stop: validated structured output completed the response")
		} else {
			sections = append(sections, "Final assistant stop: stop without tool call")
		}
	}
	return strings.Join(sections, "\n\n---\n\n")
}

// formatWatchdogOrchestrationActivity: only completed, exactly paired native orchestration calls are activity evidence.
func formatWatchdogOrchestrationActivity(event any) string {
	turn, ok := event.(*jsObject)
	if !ok || wdPropV(turn, "type") != "turn_end" {
		return ""
	}
	content, ok := wdPropV(wdPropV(turn, "message"), "content").([]any)
	results, ok2 := wdPropV(turn, "toolResults").([]any)
	if !ok || !ok2 {
		return ""
	}
	var sections []string
	for _, call := range content {
		if wdPropV(call, "type") != "toolCall" {
			continue
		}
		id, isString := wdPropV(call, "id").(string)
		if !isString {
			continue
		}
		args, isObj := wdPropV(call, "arguments").(*jsObject)
		if !isObj {
			continue
		}
		name, _ := wdPropV(call, "name").(string)
		action, hasAction := dynGet(args, "action")
		actionStr, _ := action.(string)
		_, agentIsString := wdPropV(args, "agent").(string)
		_, scriptIsString := wdPropV(args, "workflowScript").(string)
		_, scriptPathIsString := wdPropV(args, "workflowScriptPath").(string)
		eligible := name == "bg_wait" ||
			(name == "subagent_supervisor" && wdContains([]string{"pending", "list", "reply"}, actionStr)) ||
			(name == "subagent" && (wdContains([]string{"status", "resume", "interrupt", "steer", "stop"}, actionStr) ||
				(!hasAction && (agentIsString || scriptIsString || scriptPathIsString))))
		if !eligible {
			continue
		}
		for _, r := range results {
			if wdPropV(r, "role") == "toolResult" && wdPropV(r, "toolCallId") == id && wdPropV(r, "toolName") == name {
				formatted, _ := formatWatchdogReviewMessage(r, false)
				sections = append(sections, wdFormatToolCall(name, args), formatted)
				break
			}
		}
	}
	return strings.Join(sections, "\n\n")
}
