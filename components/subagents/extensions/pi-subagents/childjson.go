package pi_subagents

// childjson.go runs a child in pig's JSON event stream mode so a background run shows progress while it works, as
// the original's background runner sees its child session's events live: activity lines land in output-<n>.log (the
// file the transcript view tails), child events are mirrored into events.jsonl, and status heartbeats with them.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// trimLineEnd cuts the record's trailing newline (and carriage return) as stream framing delivers it.
func trimLineEnd(line []byte) []byte {
	return bytes.TrimRight(line, "\r\n")
}

// sortedKeys iterates a JSON object's keys in a stable order.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// childEventSink is what a JSON-mode child reports live: every event it emits and the human-readable activity lines
// derived from them. It is the original's runChildSession pair, appendChildEvent and writeOutputLine.
type childEventSink struct {
	OnEvent      func(event map[string]any)
	OnOutputLine func(line string)
}

// childJSONHandler turns the child's JSON event stream into the original's live observability: activity lines, the
// final assistant message as the run's answer (json mode makes message_end authoritative), and the terminal signals
// print mode carried as its exit status.
type childJSONHandler struct {
	sink         childEventSink
	sawEvent     bool
	sawAssistant bool
	answer       string
	stopReason   string
	aborted      bool
}

func newChildJSONHandler(sink childEventSink) *childJSONHandler {
	return &childJSONHandler{sink: sink}
}

// onLine consumes one stdout line of the child's JSON event stream.
func (h *childJSONHandler) onLine(line []byte) {
	line = trimLineEnd(line)
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var event map[string]any
	if err := json.Unmarshal(line, &event); err != nil {
		return
	}
	h.sawEvent = true
	if h.sink.OnEvent != nil {
		h.sink.OnEvent(event)
	}
	typ, _ := event["type"].(string)
	switch typ {
	case "tool_execution_start":
		// The original's live output line: `toolName: preview`, or the bare name.
		name, _ := event["toolName"].(string)
		if name == "" {
			return
		}
		args, _ := event["args"].(map[string]any)
		if preview := toolArgsPreview(args); preview != "" {
			h.output(name + ": " + preview)
		} else {
			h.output(name)
		}
	case "message_end":
		message, _ := event["message"].(map[string]any)
		if message == nil {
			return
		}
		text := extractTextFromContent(message["content"])
		if text != "" {
			for _, line := range strings.Split(text, "\n") {
				h.output(line)
			}
		}
		if role, _ := message["role"].(string); role == "assistant" {
			h.sawAssistant = true
			if text != "" {
				h.answer = text
			}
			if reason, ok := message["stopReason"].(string); ok {
				h.stopReason = reason
			}
		}
	case "agent_settled":
		if aborted, ok := event["aborted"].(bool); ok {
			h.aborted = aborted
		}
	}
}

// output reports one activity line, skipping empties as the original's writeOutputText does.
func (h *childJSONHandler) output(line string) {
	if h.sink.OnOutputLine == nil {
		return
	}
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" {
		return
	}
	h.sink.OnOutputLine(line)
}

// runAgentJSON runs the child in pig's JSON event stream mode (the same options as print mode, stdout as events) so
// the caller sees the child's work while it happens. The answer is the last assistant message_end, which json mode
// makes authoritative. A child that produced no JSON events keeps the print-mode answer on stdout.
func runAgentJSON(done <-chan struct{}, cwd string, a *AgentConfig, task, model, channelDir string, sink childEventSink) (string, error) {
	if err := enforceModelScope(cwd, a, model); err != nil {
		return "", err
	}
	if a.Runner != nil && a.Runner.Type == "external-cli" {
		// An external CLI does not speak pig's JSON mode; its stdout stays the answer.
		return runAgentExternalCLI(done, cwd, a, task)
	}
	if a.Runner != nil && a.Runner.Type == "external-job" {
		return "", fmt.Errorf("External-job runner '%s' is not available in the Go port. Install the original extension, %s, which runs unchanged in PiG.", a.Runner.Command, originalSpec)
	}
	select {
	case childSlots <- struct{}{}:
	case <-done:
		return "", fmt.Errorf("%s was cancelled", a.Name)
	}
	defer func() { <-childSlots }()
	depth, _ := subagentDepth(os.Getenv)
	stdin := childPrompt(task)
	if channelDir != "" {
		stdin += supervisorPromptInstructions(channelDir)
	}
	handler := newChildJSONHandler(sink)
	res, err := runChild(done, childRequest{
		Bin: pigBinary(), Args: buildChildJSONArgs(a, model), Stdin: stdin, Cwd: cwd,
		Env: childEnv(os.Environ(), depth, a.Name, channelDir), OnLine: handler.onLine,
	})
	switch {
	case errors.Is(err, errCancelled):
		return handler.answer, fmt.Errorf("%s was cancelled", a.Name)
	case err != nil:
		return "", fmt.Errorf("could not start %s: %w", a.Name, err)
	case res.Exit != 0:
		detail := res.Stderr
		if detail == "" {
			detail = res.Stdout
		}
		return handler.answer, &childExit{agent: a.Name, code: res.Exit, detail: detail}
	}
	if !handler.sawEvent {
		// No JSON events arrived: the stdout is the print-mode answer (a stubbed or non-JSON child).
		return res.Stdout, nil
	}
	// Print mode turns a bad final message into exit 1; json mode exits 0, so the handler maps it here.
	if handler.stopReason == "error" || handler.stopReason == "aborted" {
		detail := res.Stderr
		if detail == "" {
			detail = fmt.Sprintf("final assistant message stopped with reason %q", handler.stopReason)
		}
		return handler.answer, &childExit{agent: a.Name, code: 1, detail: detail}
	}
	if handler.aborted {
		return handler.answer, &childExit{agent: a.Name, code: 1, detail: "run aborted before it settled"}
	}
	if !handler.sawAssistant {
		return handler.answer, &childExit{agent: a.Name, code: 1, detail: "child ended without an assistant message"}
	}
	return handler.answer, nil
}

// extractTextFromContent is the original's text extraction: text parts, tool_result contents, bare text fields, or a
// plain string.
func extractTextFromContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		texts := []string{}
		for _, part := range v {
			m, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if typ, _ := m["type"].(string); typ == "tool_result" {
				if inner := extractTextFromContent(m["content"]); inner != "" {
					texts = append(texts, inner)
				}
				continue
			}
			if s, ok := m["text"].(string); ok && s != "" {
				texts = append(texts, s)
			}
		}
		return strings.Join(texts, "")
	}
	return ""
}

// toolArgsPreview is the original's extractToolArgsPreview: the first recognizable argument, sanitized and cut.
func toolArgsPreview(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	previewValue := func(value any) (string, bool) {
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return "", false
			}
			return sanitizeDisplayText(v), true
		case float64, bool:
			return fmt.Sprint(v), true
		}
		return "", false
	}
	previewArray := func(value any) (string, bool) {
		list, ok := value.([]any)
		if !ok || len(list) == 0 {
			return "", false
		}
		first, ok := previewValue(list[0])
		if !ok {
			return "", false
		}
		if len(list) > 1 {
			return fmt.Sprintf("%s (+%d more)", first, len(list)-1), true
		}
		return first, true
	}
	if server, ok := args["tool"].(string); ok {
		prefix := ""
		if s, ok := args["server"].(string); ok {
			prefix = sanitizeDisplayText(s) + "/"
		}
		suffix := ""
		if a, ok := args["args"].(string); ok && strings.TrimSpace(a) != "" {
			suffix = " " + previewDisplayText(a, 40)
		}
		return sanitizeDisplayText(prefix + server + suffix)
	}
	if p, ok := previewArray(args["queries"]); ok {
		return previewDisplayText(p, 60)
	}
	if q, ok := previewValue(args["query"]); ok {
		return previewDisplayText(q, 60)
	}
	if w, ok := args["workflow"].(string); ok && strings.TrimSpace(w) != "" {
		return "workflow=" + previewDisplayText(w, 48)
	}
	if u, ok := previewValue(args["url"]); ok {
		return previewDisplayText(u, 60)
	}
	if p, ok := previewArray(args["urls"]); ok {
		return previewDisplayText(p, 60)
	}
	if p, ok := previewValue(args["prompt"]); ok {
		return previewDisplayText(p, 60)
	}
	for _, key := range []string{"command", "path", "file_path", "pattern", "query", "url", "task", "describe", "search"} {
		if v, ok := previewValue(args[key]); ok {
			return previewDisplayText(v, 60)
		}
	}
	for _, key := range sortedKeys(args) {
		if p, ok := previewArray(args[key]); ok {
			return sanitizeDisplayText(key) + "=" + previewDisplayText(p, 50)
		}
		if v, ok := previewValue(args[key]); ok {
			return sanitizeDisplayText(key) + "=" + previewDisplayText(v, 50)
		}
	}
	return ""
}

// sanitizeDisplayText collapses whitespace and control sequences (the original strips ANSI escapes too) so a preview
// stays one line.
func sanitizeDisplayText(value string) string {
	out := []rune{}
	pendingSpace := false
	appendSpace := func() {
		if len(out) > 0 {
			pendingSpace = true
		}
	}
	rs := []rune(value)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == 0x1b { // ESC: drop the escape sequence that follows it
			appendSpace()
			if i+1 < len(rs) && (rs[i+1] == '[' || rs[i+1] == ']' || rs[i+1] == 'P' || rs[i+1] == 'X' || rs[i+1] == '^' || rs[i+1] == '_') {
				i = skipEscapeSequence(rs, i+2, rs[i+1])
				continue
			}
			i++
			continue
		}
		if r < 0x20 || r == 0x7f || r == 0x85 || r == 0xa0 || (r >= 0x200b && r <= 0x200f) || r == 0xfeff {
			appendSpace()
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == 0x85 || r == 0x2028 || r == 0x2029 {
			appendSpace()
			continue
		}
		if pendingSpace {
			out = append(out, ' ')
			pendingSpace = false
		}
		out = append(out, r)
	}
	return strings.TrimSpace(string(out))
}

// skipEscapeSequence returns the index of the last consumed rune of an ANSI escape body (CSI ends at a final byte, OSC
// and other control strings at a BEL or ST).
func skipEscapeSequence(rs []rune, start int, kind rune) int {
	end := start
	for ; end < len(rs); end++ {
		r := rs[end]
		if kind == '[' {
			if r >= 0x40 && r <= 0x7e {
				return end
			}
			continue
		}
		if r == 0x07 {
			return end
		}
		if r == 0x1b && end+1 < len(rs) && rs[end+1] == '\\' {
			return end + 1
		}
	}
	return len(rs)
}

// truncateDisplayText cuts after max runes, as the original cuts after max characters.
func truncateDisplayText(value string, max int) string {
	if max <= 0 {
		return ""
	}
	rs := []rune(value)
	if len(rs) <= max {
		return value
	}
	return string(rs[:max])
}

// previewDisplayText sanitizes and cuts a preview to max with the original's "..." marker.
func previewDisplayText(value string, max int) string {
	normalized := sanitizeDisplayText(value)
	if len([]rune(normalized)) <= max {
		return normalized
	}
	if max <= 3 {
		return truncateDisplayText(normalized, max)
	}
	return truncateDisplayText(normalized, max-3) + "..."
}
