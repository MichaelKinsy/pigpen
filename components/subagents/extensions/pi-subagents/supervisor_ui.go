package pi_subagents

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/intercom/supervisor-ui.ts: the cards a supervisor request (a message in the transcript) and its reply (a journal
// entry) render as. The original draws them with the theme's accent color; the extension's renderers return plain rows, the host
// styles them.

const (
	supervisorRequestMessageType = "subagent_supervisor_request"
	supervisorReplyEntryType     = "subagent_supervisor_reply"

	supervisorMaxFieldChars     = 512
	supervisorMaxBodyChars      = 8000
	supervisorMaxInterviewChars = 4000
	supervisorMaxRenderLines    = 36
	supervisorTruncationMarker  = "[truncated]"
)

func supervisorReplyHint(requestID string) string {
	return fmt.Sprintf(`subagent_supervisor({ action: "reply", replyTo: "%s", message: "..." })`, requestID)
}

func supervisorBoundedText(value string, maxChars int) string {
	safe := safeTerminalText(value)
	if jsLength(safe) <= maxChars {
		return safe
	}
	prefixLength := max(0, maxChars-len(supervisorTruncationMarker)-1)
	var b strings.Builder
	n := 0
	for _, r := range safe {
		w := len(string(r))
		if r > 0xffff {
			w = 2
		} else {
			w = 1
		}
		if n+w > prefixLength {
			break
		}
		b.WriteRune(r)
		n += w
	}
	return b.String() + " " + supervisorTruncationMarker
}

func supervisorDisplayText(value string, maxChars int, expanded bool) string {
	if expanded {
		return safeTerminalText(value)
	}
	return supervisorBoundedText(value, maxChars)
}

func supervisorBoundedField(v any, fallback string) string {
	switch x := v.(type) {
	case nil:
		return supervisorBoundedText(fallback, supervisorMaxFieldChars)
	case string:
		return supervisorBoundedText(x, supervisorMaxFieldChars)
	case float64:
		return supervisorBoundedText(jsNumber(x), supervisorMaxFieldChars)
	}
	return supervisorBoundedText(fmt.Sprint(v), supervisorMaxFieldChars)
}

func supervisorContentText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				if t, ok := m["text"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func supervisorInterviewText(interview any, expanded bool) string {
	b, err := json.MarshalIndent(interview, "", "  ")
	serialized := string(b)
	if err != nil {
		serialized = "[unavailable]"
	}
	return supervisorDisplayText(serialized, supervisorMaxInterviewChars, expanded)
}

func isSupervisorReason(v any) bool {
	s, _ := v.(string)
	return s == "need_decision" || s == "interview_request" || s == "progress_update"
}

func optionalStringField(m map[string]any, k string) bool {
	v, has := m[k]
	if !has || v == nil {
		return true
	}
	_, ok := v.(string)
	return ok
}

// supervisorRequestDetails validates a request message's details (nil when they are not well-formed).
func supervisorRequestDetails(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return nil
	}
	for _, k := range []string{"id", "requestId", "replyHint", "requestBody", "runId", "agent", "childTarget"} {
		if !optionalStringField(m, k) {
			return nil
		}
	}
	if r, has := m["reason"]; has && r != nil && !isSupervisorReason(r) {
		return nil
	}
	if e, has := m["expectsReply"]; has && e != nil {
		if _, ok := e.(bool); !ok {
			return nil
		}
	}
	if c, has := m["childIndex"]; has && c != nil {
		f, ok := c.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
	}
	return m
}

// supervisorReplyData validates a reply entry's data (nil when it is not well-formed).
func supervisorReplyData(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return nil
	}
	for _, k := range []string{"requestId", "runId", "agent", "message"} {
		if _, ok := m[k].(string); !ok {
			return nil
		}
	}
	if r, has := m["reason"]; has && r != nil && !isSupervisorReason(r) {
		return nil
	}
	if t, has := m["childTarget"]; has && t != nil {
		if _, ok := t.(string); !ok {
			return nil
		}
	}
	for _, k := range []string{"childIndex", "createdAt"} {
		f, ok := m[k].(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
	}
	return m
}

func supervisorRequestHeading(reason string) string {
	switch reason {
	case "interview_request":
		return "⚠ Supervisor interview request"
	case "progress_update":
		return "ℹ Supervisor progress update"
	}
	return "⚠ Supervisor decision request"
}

func detailString(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func supervisorRequestLines(content any, d map[string]any, expanded bool) []string {
	requestID := supervisorBoundedField(firstAny(d["requestId"], d["id"]), "unknown")
	lines := []string{
		"Reason: " + supervisorBoundedField(d["reason"], "unknown"),
		"Run: " + supervisorBoundedField(d["runId"], "unknown"),
		"Agent: " + supervisorBoundedField(d["agent"], "unknown"),
		"Child index: " + supervisorBoundedField(d["childIndex"], "unknown"),
	}
	if t := detailString(d, "childTarget"); t != "" {
		lines = append(lines, "Child target: "+supervisorBoundedField(t, "unknown"))
	}
	lines = append(lines, "Request ID: "+requestID)
	if d["expectsReply"] == true {
		hint := detailString(d, "replyHint")
		if hint == "" {
			hint = supervisorReplyHint(requestID)
		}
		lines = append(lines, "Reply with: "+supervisorDisplayText(hint, supervisorMaxBodyChars, expanded))
	}
	body := detailString(d, "requestBody")
	if _, has := d["requestBody"].(string); !has {
		body = supervisorContentText(content)
	}
	if body == "" {
		body = "(no request body)"
	}
	lines = append(lines, "", "Request:", supervisorDisplayText(body, supervisorMaxBodyChars, expanded))
	if iv, has := d["interview"]; has {
		lines = append(lines, "", "Interview shape:", supervisorInterviewText(iv, expanded))
	}
	return lines
}

func firstAny(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func supervisorReplyLines(d map[string]any, expanded bool) []string {
	var lines []string
	if r := detailString(d, "reason"); r != "" {
		lines = append(lines, "Reason: "+supervisorBoundedField(r, "unknown"))
	}
	lines = append(lines, "Run: "+supervisorBoundedField(d["runId"], "unknown"), "Agent: "+supervisorBoundedField(d["agent"], "unknown"),
		"Child index: "+supervisorBoundedField(d["childIndex"], "unknown"))
	if t := detailString(d, "childTarget"); t != "" {
		lines = append(lines, "Child target: "+supervisorBoundedField(t, "unknown"))
	}
	msg := detailString(d, "message")
	if msg == "" {
		msg = "(empty reply)"
	}
	lines = append(lines, "Reply to: "+supervisorBoundedField(d["requestId"], "unknown"), "", "Reply:", supervisorDisplayText(msg, supervisorMaxBodyChars, expanded))
	return lines
}

// renderSupervisorCard draws the box: a heading row, the wrapped lines (at most 36 unless expanded, then a marker row) and the foot.
func renderSupervisorCard(lines []string, heading string, width int, expanded bool) []string {
	w := max(0, width)
	if w < 3 {
		return []string{uiTruncateToWidth(heading, w)}
	}
	body := w - 2
	header := uiTruncateToWidth(" "+heading+" ", body)
	rendered := []string{"╭" + header + strings.Repeat("─", max(0, body-uiVisibleWidth(header))) + "╮"}
	row := func(text string) string {
		t := uiTruncateToWidth(text, body)
		return "│" + t + strings.Repeat(" ", max(0, body-uiVisibleWidth(t))) + "│"
	}
	hidden, interior := false, 0
	for _, line := range lines {
		wrapped := uiWrapText(line, max(1, body))
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, wl := range wrapped {
			if !expanded && interior >= supervisorMaxRenderLines {
				hidden = true
				break
			}
			rendered = append(rendered, row(wl))
			interior++
		}
		if hidden {
			break
		}
	}
	if hidden {
		rendered = append(rendered, row(supervisorTruncationMarker))
	}
	return append(rendered, "╰"+strings.Repeat("─", body)+"╯")
}

// renderSupervisorRequest renders a request message; ok is false for details that are not well-formed (the host then shows the
// message as it is).
func renderSupervisorRequest(message map[string]any, expanded bool, width int) (lines []string, ok bool) {
	d := supervisorRequestDetails(message["details"])
	if d == nil {
		return nil, false
	}
	return renderSupervisorCard(supervisorRequestLines(message["content"], d, expanded), supervisorRequestHeading(detailString(d, "reason")), width, expanded), true
}

// renderSupervisorReply renders a reply journal entry.
func renderSupervisorReply(entry map[string]any, expanded bool, width int) (lines []string, ok bool) {
	d := supervisorReplyData(entry["data"])
	if d == nil {
		return nil, false
	}
	return renderSupervisorCard(supervisorReplyLines(d, expanded), "↩ Supervisor reply to child", width, expanded), true
}

func init() {
	extensionHooks = append(extensionHooks, func(e *sdk.Extension) {
		e.MessageRenderer(supervisorRequestMessageType, func(_ sdk.Context, message map[string]any, opts sdk.MessageRenderOptions, width int) ([]string, error) {
			if lines, ok := renderSupervisorRequest(message, opts.Expanded, width); ok {
				return lines, nil
			}
			content, _ := message["content"].(string)
			return strings.Split(content, "\n"), nil
		})
		e.EntryRenderer(supervisorReplyEntryType, func(_ sdk.Context, entry map[string]any, opts sdk.EntryRenderOptions, width int) ([]string, error) {
			if lines, ok := renderSupervisorReply(entry, opts.Expanded, width); ok {
				return lines, nil
			}
			return nil, nil
		})
	})
}
