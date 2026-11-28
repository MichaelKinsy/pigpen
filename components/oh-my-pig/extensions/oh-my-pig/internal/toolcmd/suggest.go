package toolcmd

import (
	"slices"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/agent/src/agent-loop.ts:2996-3069
// upstream: packages/coding-agent/src/sdk.ts:4327, 5229

const (
	// minToolNameSuggestionSegment is omp's MIN_TOOL_NAME_SUGGESTION_SEGMENT.
	// Segments shorter than 3 chars (`id`, `to`) are noise.
	minToolNameSuggestionSegment = 3
	// maxToolNameSuggestions is omp's MAX_TOOL_NAME_SUGGESTIONS.
	maxToolNameSuggestions = 3
)

// suggestToolNames ports omp's `suggestToolNames` (`packages/agent/src/agent-loop.ts:3019-3054`):
// returns candidate tool names sharing a trailing `__` or `_` delimited segment
// with `name`, trying longest tail first and ranking `advertised` ahead of
// `mounted` (fallback) names. Purely advisory.
func suggestToolNames(name string, advertised, mounted []string) []string {
	candidates := make([]string, 0, len(advertised)+len(mounted))
	candidates = append(candidates, advertised...)
	// Mounted commands rank after the advertised set: when one verb matches both,
	// the tool the model was actually offered is the better guess.
	candidates = append(candidates, mounted...)
	if len(candidates) == 0 {
		return nil
	}

	var segments []string
	for _, boundary := range []string{"__", "_"} {
		idx := strings.LastIndex(name, boundary)
		if idx < 0 {
			continue
		}
		segment := name[idx+len(boundary):]
		if len(segment) >= minToolNameSuggestionSegment && !slices.Contains(segments, segment) {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return nil
	}
	// Longest tail first: a distinctive `__` tail (`resolve_library_get`) ranks
	// ahead of a generic `_` tail (`get`) so truncation preserves the strong match.
	sort.SliceStable(segments, func(i, j int) bool {
		return len(segments[i]) > len(segments[j])
	})

	var matches []string
	for _, segment := range segments {
		suffix := "_" + segment
		for _, candidate := range candidates {
			if candidate == name || slices.Contains(matches, candidate) {
				continue
			}
			if candidate == segment || strings.HasSuffix(candidate, suffix) {
				matches = append(matches, candidate)
			}
		}
	}
	return matches
}

// formatToolNotFoundMessage ports omp's `formatToolNotFoundMessage`
// (`packages/agent/src/agent-loop.ts:3060-3069`).
func formatToolNotFoundMessage(name string, advertised, mounted []string) string {
	suggestions := suggestToolNames(name, advertised, mounted)
	if len(suggestions) == 0 {
		return "Tool " + name + " not found"
	}
	if len(suggestions) == 1 {
		return "Tool " + name + " not found. Did you mean " + suggestions[0] + "?"
	}
	n := min(len(suggestions), maxToolNameSuggestions)
	return "Tool " + name + " not found. Closest available: " + strings.Join(suggestions[:n], ", ")
}

// SuggestToolName returns omp's unknown-tool suggestion message for `name`
// against ctx's current advertised tools and mounted tool commands
// (`Tool <name> not found`, `Tool <name> not found. Did you mean <cand>?`, or
// `Tool <name> not found. Closest available: <c1>, <c2>, <c3>`).
//
// upstream: packages/agent/src/agent-loop.ts:3019-3069, sdk.ts:4327, 5229
func SuggestToolName(ctx sdk.Context, name string) string {
	var active []string
	var zero sdk.Context
	if ctx != zero {
		if tools, err := ctx.GetActiveTools(); err == nil {
			active = tools
		}
	}
	advertised := AdvertisedTools(ctx, active)
	mounted := MountedNames(ctx)
	return formatToolNotFoundMessage(name, advertised, mounted)
}

// rewriteUnknownToolResultsInPlace edits PiG's bare `Tool <name> not found`
// error toolResult messages in `messages` in place to append omp's
// `Did you mean <tool>?` / `Closest available: ...` suggestion before the
// messages reach the model (called from `sdk.EventContext`).
//
// PiG's `agent/tool_execution.go:366-369` produces `Tool <name> not found`
// before any `tool_call` or `tool_result` hook runs; editing the text block in
// place during `EventContext` delivers omp's exact text to the model while
// preserving PiG's `_pigContextUnchanged` message-list identity.
// Note: the host's persisted session entry and `tool_execution_end` JSON event
// still carry PiG's original host text.
func rewriteUnknownToolResultsInPlace(ctx sdk.Context, messages []any) bool {
	if len(messages) == 0 {
		return false
	}
	var advertised, mounted []string
	loaded := false
	changed := false

	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok || msg == nil {
			continue
		}
		if role, _ := msg["role"].(string); role != "toolResult" {
			continue
		}
		if isErr, _ := msg["isError"].(bool); !isErr {
			continue
		}
		toolName, _ := msg["toolName"].(string)
		if toolName == "" {
			continue
		}
		bare := "Tool " + toolName + " not found"

		// Check content blocks ([]any of {type: "text", text: ...}) or bare string.
		switch content := msg["content"].(type) {
		case []any:
			if len(content) != 1 {
				continue
			}
			block, ok := content[0].(map[string]any)
			if !ok || block == nil {
				continue
			}
			if typ, _ := block["type"].(string); typ != "text" {
				continue
			}
			text, _ := block["text"].(string)
			if text != bare {
				continue
			}
			if !loaded {
				var active []string
				var zero sdk.Context
				if ctx != zero {
					if tools, err := ctx.GetActiveTools(); err == nil {
						active = tools
					}
				}
				advertised = AdvertisedTools(ctx, active)
				mounted = MountedNames(ctx)
				loaded = true
			}
			formatted := formatToolNotFoundMessage(toolName, advertised, mounted)
			if formatted != bare {
				block["text"] = formatted
				changed = true
			}
		case string:
			if content != bare {
				continue
			}
			if !loaded {
				var active []string
				var zero sdk.Context
				if ctx != zero {
					if tools, err := ctx.GetActiveTools(); err == nil {
						active = tools
					}
				}
				advertised = AdvertisedTools(ctx, active)
				mounted = MountedNames(ctx)
				loaded = true
			}
			formatted := formatToolNotFoundMessage(toolName, advertised, mounted)
			if formatted != bare {
				msg["content"] = formatted
				changed = true
			}
		}
	}
	return changed
}
