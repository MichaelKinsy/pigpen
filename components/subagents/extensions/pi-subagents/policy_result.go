package pi_subagents

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Port of the result helpers of src/shared/utils.ts (final output, display items, error detection, argument previews, compaction
// of a foreground result) and formatToolCall of src/shared/formatters.ts. Messages and results are decoded JSON objects.

const piTurnTimingFooterPattern = "(?:\r?\n)*\x1b\\[38;2;136;136;136m✻ Turn took [^()\r\n]+ \\(Total time [^·\r\n]+ · \\d+ turns?\\)\x1b\\[0m[ \t]*$"

var piTurnTimingFooter = regexp.MustCompile(piTurnTimingFooterPattern)

func stripPiTurnTimingFooter(text string) string {
	return piTurnTimingFooter.ReplaceAllString(text, "")
}

var (
	reportFenceRe      = regexp.MustCompile("(?is)```acceptance[-_]report\\s*\n.*?```")
	jsonFenceBodyRe    = regexp.MustCompile("(?is)```(?:json|jsonc|json5)\\s*\n(.*?)```")
	criteriaKeyRe      = regexp.MustCompile(`"(?:criteriaSatisfied|criteria_satisfied)"`)
	reportSignalKeysRe = regexp.MustCompile(`"(?:changedFiles|changed_files|testsAddedOrUpdated|tests_added_or_updated|commandsRun|commands_run|validationOutput|validation_output|residualRisks|residual_risks|noStagedFiles|no_staged_files|diffSummary|diff_summary|reviewFindings|review_findings|manualNotes|manual_notes)"`)
)

func assistantHasError(m *jsObject) bool {
	if s, ok := m.vals["errorMessage"].(string); ok && s != "" {
		return true
	}
	return m.vals["stopReason"] == "error"
}

// getFinalOutput is the child's answer: the text of the latest assistant message that has any, except that an earlier message
// holding an acceptance report wins over later summary-only text. Provider-error messages never count.
func getFinalOutput(messages []any) string {
	var validTextParts []string
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := asObject(messages[i])
		if !ok || msg.vals["role"] != "assistant" || assistantHasError(msg) {
			continue
		}
		content, _ := asArray(msg.vals["content"])
		var texts []string
		for _, p := range content {
			if part, ok := asObject(p); ok && part.vals["type"] == "text" {
				text := stripPiTurnTimingFooter(jsToString(part.vals["text"]))
				if jsTrim(text) != "" {
					texts = append(texts, text)
				}
			}
		}
		messageText := strings.Join(texts, "\n")
		for j := len(content) - 1; j >= 0; j-- {
			part, ok := asObject(content[j])
			if !ok || part.vals["type"] != "text" {
				continue
			}
			text := stripPiTurnTimingFooter(jsToString(part.vals["text"]))
			if jsTrim(text) == "" {
				continue
			}
			validTextParts = append(validTextParts, text)
			if reportFenceRe.MatchString(text) {
				return messageText
			}
			for _, m := range jsonFenceBodyRe.FindAllStringSubmatch(text, -1) {
				if criteriaKeyRe.MatchString(m[1]) && reportSignalKeysRe.MatchString(m[1]) {
					return messageText
				}
			}
			if markerRe.MatchString(text) {
				return messageText
			}
		}
	}
	if len(validTextParts) > 0 {
		return validTextParts[0]
	}
	return ""
}

// getSingleResultOutput is the result's recorded finalOutput, else the final output of its messages.
func getSingleResultOutput(result *jsObject) string {
	if s, ok := result.vals["finalOutput"].(string); ok {
		return s
	}
	m, _ := asArray(result.vals["messages"])
	return getFinalOutput(m)
}

// displayItem is a piece of a transcript for display: text, or a tool call.
type displayItem struct {
	Type string // text, tool
	Text string
	Name string
	Args any
}

func getDisplayItems(messages []any) []displayItem {
	var items []displayItem
	for _, m := range messages {
		msg, ok := asObject(m)
		if !ok || msg.vals["role"] != "assistant" {
			continue
		}
		content, _ := asArray(msg.vals["content"])
		for _, p := range content {
			part, ok := asObject(p)
			if !ok {
				continue
			}
			switch part.vals["type"] {
			case "text":
				items = append(items, displayItem{Type: "text", Text: jsToString(part.vals["text"])})
			case "toolCall":
				items = append(items, displayItem{Type: "tool", Name: jsToString(part.vals["name"]), Args: part.vals["arguments"]})
			}
		}
	}
	return items
}

// hasEmptyTerminalAssistantResponse: the last assistant message has no output at all.
func hasEmptyTerminalAssistantResponse(messages []any) bool {
	lastIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if m, ok := asObject(messages[i]); ok && m.vals["role"] == "assistant" {
			lastIdx = i
			break
		}
	}
	if lastIdx < 0 {
		return false
	}
	last, _ := asObject(messages[lastIdx])
	content, isArr := asArray(last.vals["content"])
	if !isArr {
		return false
	}
	if len(content) == 0 {
		u, _ := asObject(last.vals["usage"])
		out := 0.0
		if u != nil {
			out, _ = asNumber(u.vals["output"])
		}
		if out == 0 {
			return true
		}
	}
	if lastIdx == len(messages)-1 && last.vals["stopReason"] == "stop" && !truthyString(last.vals["errorMessage"]) && len(content) > 0 {
		for _, p := range content {
			part, ok := asObject(p)
			if !ok || part.vals["type"] != "text" || part.vals["text"] != "" {
				return false
			}
		}
		return true
	}
	return false
}

func truthyString(v any) bool { s, ok := v.(string); return ok && s != "" }

// formatEmptyTerminalAssistantResponseError explains an empty terminal response.
func formatEmptyTerminalAssistantResponseError(messages []any) string {
	var last *jsObject
	for i := len(messages) - 1; i >= 0; i-- {
		if m, ok := asObject(messages[i]); ok && m.vals["role"] == "assistant" {
			last = m
			break
		}
	}
	if last != nil {
		if e, ok := last.vals["errorMessage"].(string); ok && jsTrim(e) != "" {
			return jsTrim(e)
		}
		if s, ok := last.vals["stopReason"].(string); ok && jsTrim(s) != "" && jsTrim(s) != "stop" {
			return fmt.Sprintf("Subagent produced no output after terminal assistant stopReason %q.", jsTrim(s))
		}
	}
	return "Subagent produced no output (possible model cold-start or empty response)."
}

// errorInfo is the original's ErrorInfo.
type errorInfo struct {
	HasError  bool
	ExitCode  int
	ErrorType string
	Details   string
}

var exitCodeRe = regexp.MustCompile(`(?i)exit(?:ed)?\s*(?:with\s*)?(?:code|status)?\s*[:\s]?\s*(\d+)`)

// detectSubagentError is the last failed tool result with no later assistant text.
func detectSubagentError(messages []any) errorInfo {
	lastText := -1
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := asObject(messages[i])
		if !ok || msg.vals["role"] != "assistant" {
			continue
		}
		content, _ := asArray(msg.vals["content"])
		for _, p := range content {
			if part, ok := asObject(p); ok && part.vals["type"] == "text" {
				if s, isStr := part.vals["text"].(string); isStr && jsTrim(s) != "" {
					lastText = i
				}
			}
		}
		if lastText == i {
			break
		}
	}
	start := 0
	if lastText >= 0 {
		start = lastText + 1
	}
	for i := len(messages) - 1; i >= start; i-- {
		msg, ok := asObject(messages[i])
		if !ok || msg.vals["role"] != "toolResult" || msg.vals["isError"] != true {
			continue
		}
		toolName, _ := msg.vals["toolName"].(string)
		var details string
		var hasDetails bool
		content, _ := asArray(msg.vals["content"])
		for _, c := range content {
			if part, ok := asObject(c); ok && part.vals["type"] == "text" {
				if t, ok := part.vals["text"]; ok {
					details, hasDetails = jsToString(t), true
				}
				break
			}
		}
		exit := 1
		if hasDetails {
			if m := exitCodeRe.FindStringSubmatch(details); m != nil {
				fmt.Sscanf(m[1], "%d", &exit)
			}
		}
		errType := toolName
		if errType == "" {
			errType = "tool"
		}
		d := details
		if u := []rune(d); len(u) > 200 {
			d = string(u[:200])
		}
		return errorInfo{HasError: true, ExitCode: exit, ErrorType: errType, Details: d}
	}
	return errorInfo{}
}

// extractTextFromContent is the text of a message content in any of its shapes.
func extractTextFromContent(content any) string {
	if content == nil || content == "" {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	parts, ok := asArray(content)
	if !ok {
		return ""
	}
	var texts []string
	for _, p := range parts {
		part, ok := asObject(p)
		if !ok {
			continue
		}
		switch {
		case part.vals["type"] == "text" && part.hasOwn("text"):
			texts = append(texts, jsToString(part.vals["text"]))
		case part.vals["type"] == "tool_result" && part.hasOwn("content"):
			if inner := extractTextFromContent(part.vals["content"]); inner != "" {
				texts = append(texts, inner)
			}
		case part.hasOwn("text"):
			texts = append(texts, jsToString(part.vals["text"]))
		}
	}
	return strings.Join(texts, "\n")
}

// ---- argument previews ----------------------------------------------------------------------------------------------------

func stringifyPreviewValue(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		if jsTrim(x) != "" {
			return sanitizeDisplayText(x), true
		}
	case bool:
		return jsToString(x), true
	}
	if f, ok := asNumber(v); ok {
		return jsNumber(f), true
	}
	return "", false
}

func previewArray(v any) (string, bool) {
	arr, ok := asArray(v)
	if !ok || len(arr) == 0 {
		return "", false
	}
	first, ok := stringifyPreviewValue(arr[0])
	if !ok || first == "" {
		return "", false
	}
	suffix := ""
	if len(arr) > 1 {
		suffix = fmt.Sprintf(" (+%d more)", len(arr)-1)
	}
	return first + suffix, true
}

// extractToolArgsPreview is a one-line preview of a tool call's arguments.
func extractToolArgsPreview(args *jsObject) string {
	str := func(k string) (string, bool) { s, ok := args.vals[k].(string); return s, ok }
	if _, ok := str("tool"); ok {
		server := ""
		if s, ok := str("server"); ok {
			server = sanitizeDisplayText(s) + "/"
		}
		toolArgs := ""
		if s, ok := str("args"); ok {
			toolArgs = " " + truncateDisplayText(sanitizeDisplayText(s), 40)
		}
		tool, _ := str("tool")
		return sanitizeDisplayText(server + tool + toolArgs)
	}
	if p, ok := previewArray(args.vals["queries"]); ok {
		return previewDisplayText(p, 60)
	}
	if s, ok := str("query"); ok && jsTrim(s) != "" {
		return previewDisplayText(s, 60)
	}
	if s, ok := str("workflow"); ok && jsTrim(s) != "" {
		return "workflow=" + previewDisplayText(s, 48)
	}
	if s, ok := str("url"); ok && jsTrim(s) != "" {
		return previewDisplayText(s, 60)
	}
	if p, ok := previewArray(args.vals["urls"]); ok {
		return previewDisplayText(p, 60)
	}
	if s, ok := str("prompt"); ok && jsTrim(s) != "" {
		return previewDisplayText(s, 60)
	}
	for _, k := range []string{"command", "path", "file_path", "pattern", "query", "url", "task", "describe", "search"} {
		if s, ok := str(k); ok {
			return previewDisplayText(s, 60)
		}
	}
	for _, k := range args.order() {
		display := sanitizeDisplayText(k)
		if p, ok := previewArray(args.vals[k]); ok {
			return display + "=" + previewDisplayText(p, 50)
		}
		if s, ok := args.vals[k].(string); ok && s != "" {
			return display + "=" + previewDisplayText(s, 50)
		}
	}
	return ""
}

// shortenPath replaces the home directory with ~.
func shortenPath(p string) string {
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// formatToolCall is the display line of a completed tool call.
func formatToolCall(name string, args *jsObject, expanded bool) string {
	switch name {
	case "bash":
		cmd, _ := args.vals["command"].(string)
		n := 60
		if expanded {
			n = 240
		}
		return "$ " + previewDisplayText(cmd, n)
	case "read", "write", "edit":
		target := ""
		if s, ok := args.vals["path"].(string); ok {
			target = s
		} else if s, ok := args.vals["file_path"].(string); ok {
			target = s
		}
		return name + " " + sanitizeDisplayText(shortenPath(target))
	}
	n := 40
	if expanded {
		n = 160
	}
	return name + " " + previewDisplayText(marshalJSON(args, ""), n)
}

// ---- compaction of a foreground result ---------------------------------------------------------------------------------

func extractToolCallSummaries(messages []any) []any {
	var out []any
	for _, m := range messages {
		msg, ok := asObject(m)
		if !ok || msg.vals["role"] != "assistant" {
			continue
		}
		content, _ := asArray(msg.vals["content"])
		for _, p := range content {
			part, ok := asObject(p)
			if !ok || part.vals["type"] != "toolCall" {
				continue
			}
			args, ok := asObject(part.vals["arguments"])
			if !ok {
				args = newObject()
			}
			name := jsToString(part.vals["name"])
			s := newObject()
			s.set("text", formatToolCall(name, args, false))
			s.set("expandedText", formatToolCall(name, args, true))
			out = append(out, s)
		}
	}
	return out
}

// compactForegroundResult stores compact tool-call summaries instead of the raw messages, and redacts the task. A running
// result is returned as it is.
func compactForegroundResult(result *jsObject) *jsObject {
	if p, ok := asObject(result.vals["progress"]); ok && p.vals["status"] == "running" {
		return result
	}
	calls, _ := asArray(result.vals["toolCalls"])
	if len(calls) == 0 {
		msgs, _ := asArray(result.vals["messages"])
		calls = extractToolCallSummaries(msgs)
	}
	out := result.clone()
	out.set("task", "[prompt redacted]")
	out.set("messages", undef{})
	out.set("progress", undef{})
	if len(calls) > 0 {
		out.set("toolCalls", calls)
	} else {
		out.set("toolCalls", undef{})
	}
	return out
}

const (
	maxStreamedRecentTools     = 32
	maxStreamedToolCalls       = 64
	maxStreamedOutputLineChars = 2000
)

// boundStreamedRecentOutput caps the length of each recent output line of a streamed snapshot.
func boundStreamedRecentOutput(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if u := displayLen(l); u > maxStreamedOutputLineChars {
			out[i] = truncateDisplayText(l, maxStreamedOutputLineChars) + "… [truncated]"
		} else {
			out[i] = l
		}
	}
	return out
}
