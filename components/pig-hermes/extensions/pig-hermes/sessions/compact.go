package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// compactToolResultMaxChars is the threshold above which tool outputs are
	// pruned in Hermes-style compaction summaries.
	compactToolResultMaxChars = 2000
	compactMaxProgressItems   = 12
	compactMaxDecisionItems   = 8
	compactMaxActiveItems     = 6
)

// handleBeforeCompact runs registered memory.Provider.OnPreCompress hooks and,
// when PiG passes `messagesToSummarize`, builds a deterministic Hermes-style
// structured summary with pruned oversized tool outputs. When
// `messagesToSummarize` is absent or empty, it returns (nil, nil) to delegate
// safely to PiG's native compactor.
func handleBeforeCompact(ctx sdk.Context, data map[string]any) (any, error) {
	sessionID := safeSessionID(ctx)
	// Notify any registered external memory providers before compaction runs.
	// Errors are intentionally ignored so compaction is never blocked by a provider failure.
	providers := snapshotMemoryProviders()
	if len(providers) > 0 {
		bgCtx := context.Background()
		for _, prov := range providers {
			_ = prov.OnPreCompress(bgCtx, sessionID)
		}
	}

	// Delegate to PiG's native compactor when merging a prior compaction summary or
	// splitting an active turn so previousSummary and turnPrefixMessages are never dropped.
	if extractPreviousSummary(data) != "" || extractIsSplitTurn(data) {
		return nil, nil
	}

	rawMessages := extractMessagesToSummarize(data)
	if len(rawMessages) == 0 {
		// Delegate safely to PiG's native compactor when no message list was provided.
		return nil, nil
	}

	summary := buildHermesCompactionSummary(rawMessages)
	if strings.TrimSpace(summary) == "" {
		return nil, nil
	}

	compaction := map[string]any{
		"summary": summary,
	}
	if firstKept := extractFirstKeptEntryID(data); firstKept != "" {
		compaction["firstKeptEntryId"] = firstKept
	}
	if tokensBefore := extractTokensBefore(data); tokensBefore > 0 {
		compaction["tokensBefore"] = tokensBefore
	}
	return map[string]any{
		"compaction": compaction,
	}, nil
}

func extractMessagesToSummarize(data map[string]any) []any {
	if data == nil {
		return nil
	}
	if list, ok := data["messagesToSummarize"].([]any); ok && len(list) > 0 {
		return list
	}
	if prep, ok := data["preparation"].(map[string]any); ok {
		if list, ok := prep["messagesToSummarize"].([]any); ok && len(list) > 0 {
			return list
		}
	}
	return nil
}

func extractPreviousSummary(data map[string]any) string {
	if data == nil {
		return ""
	}
	if v, ok := data["previousSummary"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if prep, ok := data["preparation"].(map[string]any); ok {
		if v, ok := prep["previousSummary"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func extractIsSplitTurn(data map[string]any) bool {
	if data == nil {
		return false
	}
	if v, ok := data["isSplitTurn"].(bool); ok && v {
		return true
	}
	if prep, ok := data["preparation"].(map[string]any); ok {
		if v, ok := prep["isSplitTurn"].(bool); ok && v {
			return true
		}
	}
	return false
}

func extractFirstKeptEntryID(data map[string]any) string {
	if data == nil {
		return ""
	}
	if v, ok := data["firstKeptEntryId"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if prep, ok := data["preparation"].(map[string]any); ok {
		if v, ok := prep["firstKeptEntryId"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func extractTokensBefore(data map[string]any) int {
	if data == nil {
		return 0
	}
	if n := anyToInt(data["tokensBefore"]); n > 0 {
		return n
	}
	if prep, ok := data["preparation"].(map[string]any); ok {
		if n := anyToInt(prep["tokensBefore"]); n > 0 {
			return n
		}
	}
	return 0
}

// pruneCompactToolOutput truncates oversized tool outputs (> compactToolResultMaxChars)
// cleanly with a `[pruned N chars]` notice.
func pruneCompactToolOutput(text string, limit int) string {
	if limit <= 0 {
		limit = compactToolResultMaxChars
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	pruned := len(runes) - limit
	return strings.TrimRight(string(runes[:limit]), "\r\n\t ") + fmt.Sprintf("\n[pruned %d chars]", pruned)
}

type compactMessage struct {
	role      string
	text      string
	toolName  string
	toolCalls []ToolCallSummary
}

func parseCompactMessages(raw []any) []compactMessage {
	out := make([]compactMessage, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		// Support both direct message maps and wrapped {"message": {...}} entries.
		if inner, ok := m["message"].(map[string]any); ok {
			m = inner
		}
		rawRole, _ := m["role"].(string)
		role := normalizeRole(rawRole)
		if role == "" {
			continue
		}
		toolName, _ := m["toolName"].(string)
		if toolName == "" {
			toolName, _ = m["tool_name"].(string)
		}
		text, toolCalls := extractCompactContentAndCalls(m["content"], m["tool_calls"])
		if role == "tool" {
			text = pruneCompactToolOutput(text, compactToolResultMaxChars)
		}
		if strings.TrimSpace(text) == "" && len(toolCalls) == 0 {
			continue
		}
		out = append(out, compactMessage{
			role:      role,
			text:      strings.TrimSpace(text),
			toolName:  strings.TrimSpace(toolName),
			toolCalls: toolCalls,
		})
	}
	return out
}

func extractCompactContentAndCalls(contentVal, rawToolCalls any) (string, []ToolCallSummary) {
	var textParts []string
	var calls []ToolCallSummary

	switch c := contentVal.(type) {
	case string:
		if strings.TrimSpace(c) != "" {
			textParts = append(textParts, c)
		}
	case []any:
		for _, blkAny := range c {
			blk, ok := blkAny.(map[string]any)
			if !ok {
				continue
			}
			btype, _ := blk["type"].(string)
			switch btype {
			case "text", "":
				if t, ok := blk["text"].(string); ok && strings.TrimSpace(t) != "" {
					textParts = append(textParts, t)
				}
			case "toolCall", "tool_use":
				id, _ := blk["id"].(string)
				name, _ := blk["name"].(string)
				args := compactArgsString(blk["arguments"])
				if args == "" {
					args = compactArgsString(blk["input"])
				}
				if name != "" {
					calls = append(calls, ToolCallSummary{ID: id, Name: name, Arguments: args})
				}
			}
		}
	}

	if tcList, ok := rawToolCalls.([]any); ok {
		for _, tcAny := range tcList {
			tc, ok := tcAny.(map[string]any)
			if !ok {
				continue
			}
			id, _ := tc["id"].(string)
			name, _ := tc["name"].(string)
			args := compactArgsString(tc["arguments"])
			if fn, ok := tc["function"].(map[string]any); ok {
				if name == "" {
					name, _ = fn["name"].(string)
				}
				if args == "" {
					args = compactArgsString(fn["arguments"])
				}
			}
			if name != "" {
				calls = append(calls, ToolCallSummary{ID: id, Name: name, Arguments: args})
			}
		}
	}

	return strings.Join(textParts, "\n"), calls
}

func compactArgsString(v any) string {
	switch a := v.(type) {
	case nil:
		return ""
	case string:
		return a
	default:
		if b, err := json.Marshal(a); err == nil {
			return string(b)
		}
		return ""
	}
}

func buildHermesCompactionSummary(raw []any) string {
	msgs := parseCompactMessages(raw)
	if len(msgs) == 0 {
		return ""
	}

	var goal string
	var progress []string
	var filesModified []string
	seenFiles := make(map[string]bool)
	var decisions []string
	var activeContext []string

	addFile := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seenFiles[p] {
			return
		}
		seenFiles[p] = true
		filesModified = append(filesModified, p)
	}

	for _, m := range msgs {
		switch m.role {
		case "user":
			if goal == "" && m.text != "" && !isCompactionSummary(m.text) {
				goal = truncateRunes(cleanText(m.text), 400)
			}
		case "assistant":
			if m.text != "" {
				line := truncateRunes(cleanText(m.text), 260)
				if len(progress) < compactMaxProgressItems {
					progress = append(progress, line)
				}
				if looksLikeDecision(m.text) && len(decisions) < compactMaxDecisionItems {
					decisions = append(decisions, truncateRunes(cleanText(m.text), 240))
				}
			}
			for _, tc := range m.toolCalls {
				if len(progress) < compactMaxProgressItems {
					progress = append(progress, fmt.Sprintf("Called `%s`", tc.Name))
				}
				if isFileMutatingTool(tc.Name) {
					for _, fp := range extractPathsFromArgs(tc.Arguments) {
						addFile(fp)
					}
				}
			}
		case "tool":
			if m.text != "" {
				label := m.toolName
				if label == "" {
					label = "tool"
				}
				// Preserve pruned tool output notice in active context if it was truncated or errored.
				if len(activeContext) < compactMaxActiveItems {
					if idx := strings.LastIndex(m.text, "[pruned "); idx >= 0 {
						suffix := m.text[idx:]
						prefix := truncateRunes(strings.TrimSpace(m.text[:idx]), 320)
						activeContext = append(activeContext, fmt.Sprintf("Tool `%s` output: %s %s", label, prefix, suffix))
					} else {
						activeContext = append(activeContext, fmt.Sprintf("Tool `%s`: %s", label, truncateRunes(cleanText(m.text), 220)))
					}
				}
			}
		}
	}

	// Capture lean tail of the last few user/assistant messages for Active Context.
	tailStart := len(msgs) - 4
	if tailStart < 0 {
		tailStart = 0
	}
	for _, m := range msgs[tailStart:] {
		if m.role == "tool" {
			continue
		}
		if m.text == "" {
			continue
		}
		if len(activeContext) >= compactMaxActiveItems+4 {
			break
		}
		activeContext = append(activeContext, fmt.Sprintf("[%s] %s", m.role, truncateRunes(cleanText(m.text), 300)))
	}

	var b strings.Builder
	b.WriteString(HermesCompactionGuidance)
	b.WriteString("\n\n## Goal\n")
	if goal != "" {
		b.WriteString("- ")
		b.WriteString(goal)
		b.WriteString("\n")
	} else {
		b.WriteString("- Continue the active conversation task.\n")
	}

	b.WriteString("\n## Progress\n")
	if len(progress) > 0 {
		for _, item := range progress {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("- Reviewed prior messages.\n")
	}

	b.WriteString("\n## Files Modified\n")
	if len(filesModified) > 0 {
		for _, fp := range filesModified {
			b.WriteString("- `")
			b.WriteString(fp)
			b.WriteString("`\n")
		}
	} else {
		b.WriteString("- None recorded.\n")
	}

	b.WriteString("\n## Key Decisions\n")
	if len(decisions) > 0 {
		for _, d := range decisions {
			b.WriteString("- ")
			b.WriteString(d)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("- None explicitly recorded.\n")
	}

	b.WriteString("\n## Active Context\n")
	if len(activeContext) > 0 {
		for _, item := range activeContext {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("- Ready for next user instruction.\n")
	}

	return b.String()
}

func isFileMutatingTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write_file", "patch", "write", "edit", "ast_edit":
		return true
	default:
		return false
	}
}

func extractPathsFromArgs(args string) []string {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil {
		return nil
	}
	var out []string
	for _, key := range []string{"path", "file_path", "target", "filename"} {
		if v, ok := obj[key].(string); ok && strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	if patchText, ok := obj["patch"].(string); ok && patchText != "" {
		for _, line := range strings.Split(patchText, "\n") {
			for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "} {
				if strings.HasPrefix(line, prefix) {
					if p := strings.TrimSpace(strings.TrimPrefix(line, prefix)); p != "" {
						out = append(out, p)
					}
				}
			}
		}
	}
	return out
}

func looksLikeDecision(text string) bool {
	lower := strings.ToLower(text)
	for _, kw := range []string{"decided", "decision", "chosen", "chose ", "will use ", "switching to ", "kept ", "constraint"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
