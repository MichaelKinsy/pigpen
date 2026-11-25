package agents

import (
	"encoding/json"
	"strings"
)

// upstream: packages/coding-agent/src/task/result-summary.ts,
// packages/coding-agent/src/session/harness-tags.ts,
// packages/tui/src/tools/task-repair-args.ts

const (
	// AsyncInlineResultMaxChars matches omp's ASYNC_INLINE_RESULT_MAX_CHARS.
	// upstream: packages/coding-agent/src/session/async-job-delivery.ts:28
	AsyncInlineResultMaxChars = 12_000
	// InlineOutputChars is the largest output inlined whole in `<task-result>`.
	// upstream: packages/coding-agent/src/task/result-summary.ts:24
	InlineOutputChars = AsyncInlineResultMaxChars - 500
	// PreviewChars is the head shown when an output exceeds InlineOutputChars.
	// upstream: packages/coding-agent/src/task/result-summary.ts:26
	PreviewChars = 5_000
)

// EscapeHarnessTags escapes `<` opening or closing a harness tag (`irc`,
// `tangent-notice`, or `system-<letters/hyphens>`) so subagent output cannot
// close or forge harness-authored blocks.
// upstream: packages/coding-agent/src/session/harness-tags.ts:14
func EscapeHarnessTags(text string) string {
	if !strings.ContainsRune(text, '<') {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := range len(text) {
		if text[i] == '<' && isHarnessTagStart(text[i+1:]) {
			b.WriteString("&lt;")
			continue
		}
		b.WriteByte(text[i])
	}
	return b.String()
}

func isHarnessTagStart(rest string) bool {
	j := 0
	for j < len(rest) && isASCIIWhitespace(rest[j]) {
		j++
	}
	if j < len(rest) && rest[j] == '/' {
		j++
		for j < len(rest) && isASCIIWhitespace(rest[j]) {
			j++
		}
	}
	tail := rest[j:]
	if tail == "" {
		return false
	}
	if hasPrefixFoldWord(tail, "irc") || hasPrefixFoldWord(tail, "tangent-notice") {
		return true
	}
	if len(tail) >= len("system-")+1 && strings.EqualFold(tail[:len("system-")], "system-") {
		k := len("system-")
		if !isASCIILetter(tail[k]) {
			return false
		}
		k++
		for k < len(tail) && (isASCIILetter(tail[k]) || tail[k] == '-') {
			k++
		}
		if k < len(tail) && isTagWordChar(tail[k]) {
			return false
		}
		return true
	}
	return false
}

func hasPrefixFoldWord(s, prefix string) bool {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return false
	}
	if len(s) == len(prefix) {
		return true
	}
	return !isTagWordChar(s[len(prefix)])
}

func isASCIIWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isTagWordChar(c byte) bool {
	return isASCIILetter(c) || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

// FormatResultOutputFallback returns the model-facing output fallback text.
// upstream: packages/coding-agent/src/task/result-summary.ts:33
func FormatResultOutputFallback(r SingleResult) string {
	if base := strings.TrimSpace(r.Output); base != "" {
		return base
	}
	if base := strings.TrimSpace(r.Stderr); base != "" {
		return base
	}
	if r.Requests > 0 {
		return "(no output) after " + itoa(int64(r.Requests)) + " req"
	}
	return "(no output)"
}

func previewHead(output string) string {
	runes := []rune(output)
	if len(runes) <= PreviewChars {
		return output
	}
	slice := string(runes[:PreviewChars])
	lastNewline := strings.LastIndexByte(slice, '\n')
	if lastNewline >= len(slice)/2 {
		return slice[:lastNewline]
	}
	return slice
}

// FormatTaskResultSummary renders omp's `<task-result>` envelope for a settled run.
// upstream: packages/coding-agent/src/task/result-summary.ts:52, prompts/tools/task-summary.md
func FormatTaskResultSummary(r SingleResult, totalDurationMs int64, mergeSummary string, resumable bool) string {
	var status string
	switch {
	case r.Aborted:
		status = "cancelled"
	case r.ExitCode == 0 && r.Error != "":
		status = "merge failed"
	case r.ExitCode == 0:
		status = "completed"
	default:
		status = "failed (exit " + itoa(int64(r.ExitCode)) + ")"
	}

	output := FormatResultOutputFallback(r)
	var errText string
	if r.ExitCode != 0 && !r.Aborted && strings.TrimSpace(r.Output) != "" {
		errText = r.Error
	}
	outputCharCount := len([]rune(output))
	if r.OutputMeta != nil {
		outputCharCount = r.OutputMeta.CharCount
	}

	render := func(truncated bool) string {
		var b strings.Builder
		b.WriteString(`<task-result id="`)
		b.WriteString(r.ID)
		b.WriteString(`" agent="`)
		b.WriteString(r.Agent)
		b.WriteString(`" status="`)
		b.WriteString(status)
		b.WriteString(`" duration="`)
		b.WriteString(formatDurationMs(totalDurationMs))
		b.WriteString("\">\n")

		if truncated && r.OutputMeta != nil {
			b.WriteString(`<meta lines="`)
			b.WriteString(itoa(int64(r.OutputMeta.LineCount)))
			b.WriteString(`" size="`)
			b.WriteString(formatBytesSize(r.OutputMeta.CharCount))
			b.WriteString("\" />\n")
		} else {
			// Match omp's prompts/tools/task-summary.md line 2: the inline
			// `{{#if truncated}}{{#if meta}}<meta .../>{{/if}}{{/if}}` renders an
			// empty line that `prompt.format` preserves.
			b.WriteByte('\n')
		}
		if r.Aborted {
			reason := EscapeHarnessTags(r.AbortReason)
			if reason != "" {
				b.WriteString("<abort-reason>")
				b.WriteString(reason)
				if resumable && !r.Isolated {
					b.WriteString(" — the agent is still live with its full context; message it via `write agent://")
					b.WriteString(r.ID)
					b.WriteString("` to resume instead of redoing the work.")
				}
				b.WriteString("</abort-reason>\n")
			}
		}

		if errText != "" {
			b.WriteString("<error>")
			b.WriteString(EscapeHarnessTags(errText))
			b.WriteString("</error>\n")
		}

		body := output
		if truncated {
			body = previewHead(output)
		}
		escapedBody := EscapeHarnessTags(body)
		if truncated {
			b.WriteString(`<preview full-output="agent://`)
			b.WriteString(r.ID)
			b.WriteString("\">\n")
			b.WriteString(escapedBody)
			b.WriteString("\n</preview>\n")
		} else {
			b.WriteString("<output>\n")
			b.WriteString(escapedBody)
			b.WriteString("\n</output>\n")
		}

		if mergeSummary != "" {
			b.WriteString("<merge-summary>\n")
			b.WriteString(EscapeHarnessTags(mergeSummary))
			b.WriteString("\n</merge-summary>\n")
		}

		b.WriteString("</task-result>")
		return b.String()
	}

	if r.OutputPath == "" {
		return render(false)
	}
	if outputCharCount > InlineOutputChars {
		return render(true)
	}
	whole := render(false)
	if len([]rune(whole)) > AsyncInlineResultMaxChars && outputCharCount > PreviewChars {
		return render(true)
	}
	return whole
}

// FormatFollowUpHint renders omp's prompts/tools/task-follow-up.md line.
// upstream: packages/coding-agent/src/task/index.ts:1503, prompts/tools/task-follow-up.md
func FormatFollowUpHint(agentID string, aborted, isolated, ircEnabled, resumable, transcriptAvailable bool) string {
	transcriptPart := "transcript unavailable"
	if transcriptAvailable {
		transcriptPart = "transcript at history://" + agentID
	}
	if aborted {
		if resumable && !isolated {
			msg := agentID + " was stopped but is still resumable — "
			if ircEnabled {
				msg += "message it via `write agent://" + agentID + "` to resume; "
			}
			return msg + transcriptPart
		}
		return agentID + " was aborted — " + transcriptPart
	}
	if isolated {
		return agentID + " ran isolated and cannot be resumed or messaged — transcript at history://" + agentID
	}
	if resumable {
		msg := agentID + " is now idle — "
		if ircEnabled {
			msg += "message it via `write agent://" + agentID + "` to follow up; "
		}
		return msg + "transcript at history://" + agentID
	}
	return agentID + " stopped and cannot be messaged — " + transcriptPart
}

// FormatAsyncResultMessage renders the `<system-notice>` delivered as a follow-up
// when a background agent job settles.
// upstream: packages/coding-agent/src/prompts/tools/async-result.md
func FormatAsyncResultMessage(jobID, agentURLID, resultText string, outputPublished bool) string {
	var b strings.Builder
	b.WriteString("<system-notice>\nBackground job ")
	b.WriteString(jobID)
	b.WriteString(" has completed.\nResume your work using the result below.\n")
	b.WriteString(resultText)
	if agentURLID != "" {
		if outputPublished {
			b.WriteString("\n\nAgent: agent://")
			b.WriteString(agentURLID)
			b.WriteString(", history://")
			b.WriteString(agentURLID)
		} else {
			b.WriteString("\n\nAgent: history://")
			b.WriteString(agentURLID)
		}
	}
	b.WriteString("\n</system-notice>")
	return b.String()
}

// RepairDoubleEncodedJSONString unescapes a double-encoded JSON string value when
// it exhibits a whole-string double-encoding signature.
// upstream: packages/tui/src/tools/task-repair-args.ts:69
func RepairDoubleEncodedJSONString(value string) string {
	if !strings.ContainsRune(value, '\\') {
		return value
	}
	if !hasDoubleEncodeSignature(value) {
		return value
	}
	var decoded string
	if err := json.Unmarshal([]byte(`"`+value+`"`), &decoded); err != nil {
		return value
	}
	if decoded != value {
		return decoded
	}
	return value
}

func hasDoubleEncodeSignature(value string) bool {
	escapes := 0
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 >= len(value) {
			continue
		}
		next := value[i+1]
		if next == '"' || next == '\\' || next == '/' {
			return true
		}
		if next == 'u' && i+5 < len(value) && isHex4(value[i+2:i+6]) {
			return true
		}
		escapes++
		if escapes >= 2 {
			return true
		}
		i++
	}
	return false
}

func isHex4(s string) bool {
	for i := range len(s) {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) == 4
}

// RepairTaskParams repairs double-encoded natural-language fields (`task`, `steps`, `context`).
// upstream: packages/tui/src/tools/task-repair-args.ts:111
func RepairTaskParams(p TaskParams) TaskParams {
	if p.HasTask {
		p.Task = RepairDoubleEncodedJSONString(p.Task)
	}
	if p.HasContext {
		p.Context = RepairDoubleEncodedJSONString(p.Context)
	}
	if len(p.Steps) > 0 {
		steps := make([]string, len(p.Steps))
		for i, s := range p.Steps {
			steps[i] = RepairDoubleEncodedJSONString(s)
		}
		p.Steps = steps
	}
	if len(p.Tasks) > 0 {
		tasks := make([]TaskItem, len(p.Tasks))
		for i, item := range p.Tasks {
			if item.HasTask {
				item.Task = RepairDoubleEncodedJSONString(item.Task)
			}
			if len(item.Steps) > 0 {
				steps := make([]string, len(item.Steps))
				for j, s := range item.Steps {
					steps[j] = RepairDoubleEncodedJSONString(s)
				}
				item.Steps = steps
			}
			tasks[i] = item
		}
		p.Tasks = tasks
	}
	return p
}

// OneLineLabel returns a sanitized single-line preview of text (≤80 runes).
func OneLineLabel(text string) string {
	flat := strings.Join(strings.Fields(text), " ")
	runes := []rune(flat)
	if len(runes) > 80 {
		return string(runes[:79]) + "…"
	}
	return flat
}
