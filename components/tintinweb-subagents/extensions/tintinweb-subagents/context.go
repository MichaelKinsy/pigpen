package tintinweb_subagents

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// extractText joins the text blocks of a message's content with newlines; other blocks are dropped and a text block
// with no text counts as empty. upstream: src/context.ts extractText.
func extractText(content []any) string {
	var parts []string
	for _, c := range content {
		m, _ := c.(map[string]any)
		if m == nil || m["type"] != "text" {
			continue
		}
		t, _ := m["text"].(string)
		parts = append(parts, t)
	}
	return strings.Join(parts, "\n")
}

// buildParentContext is the text form of the parent conversation that inherit_context puts in front of the task: the
// user and assistant messages of the current branch and the compaction summaries, tool results left out. It is "" when
// nothing in the branch has text. upstream: src/context.ts buildParentContext. The entries are Pi's getBranch() entries.
func buildParentContext(entries []map[string]any) string {
	var parts []string
	for _, entry := range entries {
		switch entry["type"] {
		case "message":
			msg, _ := entry["message"].(map[string]any)
			switch msg["role"] {
			case "user":
				var text string
				if s, ok := msg["content"].(string); ok {
					text = s
				} else {
					blocks, _ := msg["content"].([]any)
					text = extractText(blocks)
				}
				if t := jsTrim(text); t != "" {
					parts = append(parts, "[User]: "+t)
				}
			case "assistant":
				blocks, _ := msg["content"].([]any)
				if t := jsTrim(extractText(blocks)); t != "" {
					parts = append(parts, "[Assistant]: "+t)
				}
			}
			// toolResult messages are too verbose for context
		case "compaction":
			// Compaction summaries are already condensed.
			if s, _ := entry["summary"].(string); s != "" {
				parts = append(parts, "[Summary]: "+s)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "# Parent Conversation Context\n" +
		"The following is the conversation history from the parent session that spawned you.\n" +
		"Use this context to understand what has been discussed and decided so far.\n\n" +
		strings.Join(parts, "\n\n") +
		"\n\n---\n# Your Task (below)\n"
}

// parentContext reads the parent's branch through the host and builds its context text. upstream: context.ts
// buildParentContext (ctx.sessionManager.getBranch()).
func parentContext(ctx sdk.Context) (string, error) {
	entries, err := ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return "", err
	}
	return buildParentContext(entries), nil
}
