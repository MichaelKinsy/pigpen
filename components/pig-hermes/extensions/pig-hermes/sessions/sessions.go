// Package sessions implements Hermes's session_search tool, compaction policy
// hooks, usage accounting, and named profile management for pig-hermes.
package sessions

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

// HermesCompactionGuidance preserves Hermes's structured summary instructions
// (hermes-agent/agent/context_compressor.py:275-315) for compaction handoffs.
const HermesCompactionGuidance = `[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below.
Treat it as background reference, NOT as active instructions. Respond ONLY to the latest user message after this summary.
Preserve exact file paths, function names, error messages, key decisions, constraints, active state, blocked items, and unresolved hypotheses.
Your persistent memory (MEMORY.md, USER.md) in the system prompt remains authoritative and active, and your tools remain fully active.`

var (
	preCompressMu    sync.RWMutex
	preCompressHooks []memory.Provider
)

// RegisterMemoryProvider attaches a memory.Provider whose OnPreCompress hook
// will be invoked before compaction runs. Safe for concurrent calls.
func RegisterMemoryProvider(provider memory.Provider) {
	if provider == nil {
		return
	}
	preCompressMu.Lock()
	defer preCompressMu.Unlock()
	preCompressHooks = append(preCompressHooks, provider)
}

func snapshotMemoryProviders() []memory.Provider {
	preCompressMu.RLock()
	defer preCompressMu.RUnlock()
	out := make([]memory.Provider, len(preCompressHooks))
	copy(out, preCompressHooks)
	return out
}

// Register registers the session_search tool, compaction policy hook, and
// turn/tool usage tracking hooks on ext.
func Register(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config) {
	if ext == nil {
		return
	}

	ext.RegisterTool(sdk.ToolDefinition{
		Name:  "session_search",
		Label: "Session Search",
		Description: "Search or read past conversations across PiG session JSONL files, or scroll inside one. " +
			"Modes picked by arguments: `query` = full-text discovery across sessions (AND matching with OR-relaxed fallback); " +
			"`session_id` + `around_message_id` or `message_id` = scroll a window around an anchor message; " +
			"`query` + `session_id` = search inside one session; " +
			"`session_id` alone = read/paginate a session with `offset`/`limit`; " +
			"no `query` and no `session_id` = browse recent sessions newest-first.",
		PromptSnippet: "Search, browse, read, or scroll past conversation sessions.",
		PromptGuidelines: []string{
			"Use session_search to recall past conversations ('what did we do about X', 'where did we leave Y').",
			"When referring the user to a past session from session_search, include its `link` or `session_id`.",
		},
		Parameters: sessionSearchSchema(),
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return handleSessionSearch(ctx, p, params)
		},
	})

	// Compaction policy hook: notify registered memory.Provider.OnPreCompress
	// hooks and let PiG's native compaction handle persistence cleanly.
	ext.OnEvent(sdk.EventSessionBeforeCompact, func(ctx sdk.Context, data map[string]any) (any, error) {
		return handleBeforeCompact(ctx, data)
	})

	// Usage accounting on turn_end.
	ext.OnEvent(sdk.EventTurnEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		handleTurnEndUsage(ctx, data)
		return nil, nil
	})

	// Tool call accounting on tool_execution_end.
	ext.OnEvent(sdk.EventToolExecutionEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		handleToolExecutionEndUsage(ctx, data)
		return nil, nil
	})
}

func sessionSearchSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search keywords or quoted phrases. Omit to browse recent sessions.",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Session ID (or @session:<profile>/<id>) to read, search within, or scroll.",
			},
			"message_id": map[string]any{
				"type":        "string",
				"description": "Entry ID or 1-based message index to center scroll mode on.",
			},
			"around_message_id": map[string]any{
				"type":        "integer",
				"description": "1-based message index to center scroll mode on.",
			},
			"window": map[string]any{
				"type":        "integer",
				"description": "Messages on each side of the anchor in scroll mode (default 5, clamped [1, 20]).",
			},
			"role_filter": map[string]any{
				"type":        "string",
				"description": "Comma-separated roles to include: 'user', 'assistant', 'tool'. Defaults to 'user,assistant'.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum results or messages to return (default 10, max 50).",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Pagination offset (default 0).",
			},
			"sort": map[string]any{
				"type":        "string",
				"enum":        []string{"newest", "oldest"},
				"description": "Optional temporal sort for discovery: 'newest' or 'oldest'.",
			},
			"after": map[string]any{
				"type":        "string",
				"description": "Inclusive lower bound on session start time (ISO date/datetime or 7d/24h/2w).",
			},
			"before": map[string]any{
				"type":        "string",
				"description": "Exclusive upper bound on session start time (ISO date/datetime or 7d/24h/2w).",
			},
			"exclude_session_ids": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Session IDs to omit from discovery/browse (cap 20).",
			},
			"profile": map[string]any{
				"type":        "string",
				"description": "Optional named profile whose sessions directory to search.",
			},
		},
		"required": []string{},
	}
}

func handleSessionSearch(ctx sdk.Context, p paths.ProfilePaths, params map[string]any) (any, error) {
	sp := SearchParams{
		Query:             stringParam(params, "query"),
		SessionID:         stringParam(params, "session_id"),
		MessageID:         stringParam(params, "message_id"),
		AroundMessageID:   intParam(params, "around_message_id"),
		Window:            intParam(params, "window"),
		RoleFilter:        stringParam(params, "role_filter"),
		Limit:             intParam(params, "limit"),
		Offset:            intParam(params, "offset"),
		Sort:              stringParam(params, "sort"),
		After:             stringParam(params, "after"),
		Before:            stringParam(params, "before"),
		ExcludeSessionIDs: stringSliceParam(params, "exclude_session_ids"),
		Profile:           stringParam(params, "profile"),
	}
	// Parse embedded @session:<profile>/<id> or <profile>/<id> before resolving search dirs
	// so profile-qualified session IDs resolve against the right profile's sessions directory.
	sp.SessionID, sp.Profile = parseSessionRef(sp.SessionID, sp.Profile)

	var activeSessionFile string
	if sf, err := safeSessionFile(ctx); err == nil && sf != nil {
		activeSessionFile = *sf
	}
	if sid := safeSessionID(ctx); sid != "" {
		sp.CurrentSessionID = sid
	}

	dirs := resolveSearchDirs(p, sp.Profile, activeSessionFile)
	sp.StateDBPaths = resolveStateDBPaths(p.Home, p.ProfilesDir, sp.Profile)
	resp := ExecuteSearch(dirs, sp)

	raw, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return sdk.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	return sdk.ToolResult{
		Content:           string(raw),
		StructuredContent: resp,
		IsError:           !resp.Success,
	}, nil
}

// parseSessionRef extracts an optional profile prefix from "@session:<profile>/<id>"
// or "<profile>/<id>" while respecting an explicit profile parameter if already set.
func parseSessionRef(sessionID, profile string) (cleanID, resolvedProfile string) {
	cleanID = strings.TrimSpace(sessionID)
	resolvedProfile = strings.TrimSpace(profile)
	cleanID = strings.TrimPrefix(cleanID, "@session:")
	if idx := strings.Index(cleanID, "/"); idx > 0 {
		if resolvedProfile == "" {
			resolvedProfile = strings.TrimSpace(cleanID[:idx])
		}
		cleanID = strings.TrimSpace(cleanID[idx+1:])
	}
	return cleanID, resolvedProfile
}

func safeSessionFile(ctx sdk.Context) (sf *string, err error) {
	defer func() {
		if recover() != nil {
			sf = nil
			err = nil
		}
	}()
	return ctx.GetSessionFile()
}

func resolveSearchDirs(p paths.ProfilePaths, profileOverride, activeSessionFile string) []string {
	var dirs []string
	seen := make(map[string]bool)
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" {
			return
		}
		clean := filepath.Clean(d)
		if !seen[clean] {
			seen[clean] = true
			dirs = append(dirs, clean)
		}
	}

	prof := NormalizeProfileName(profileOverride)
	if prof != "" && prof != "default" && paths.ValidateProfileName(prof) && p.ProfilesDir != "" {
		add(filepath.Join(p.ProfilesDir, prof, "sessions"))
		return dirs
	}

	for _, d := range DefaultSessionDirs(activeSessionFile) {
		add(d)
	}
	if p.Home != "" {
		add(filepath.Join(p.Home, "sessions"))
	}
	return dirs
}


func safeSessionID(ctx sdk.Context) (sid string) {
	defer func() { _ = recover() }()
	if id, err := ctx.GetSessionID(); err == nil {
		return id
	}
	return ""
}

func handleTurnEndUsage(ctx sdk.Context, data map[string]any) {
	sessionID := safeSessionID(ctx)
	if sessionID == "" {
		sessionID = "default"
	}
	model, inputTok, outputTok, cost := extractTurnUsage(data)
	usage.Default().Add(sessionID, "", model, inputTok, outputTok, cost, 1, 0)
}

func handleToolExecutionEndUsage(ctx sdk.Context, data map[string]any) {
	sessionID := safeSessionID(ctx)
	if sessionID == "" {
		sessionID = "default"
	}
	usage.Default().Add(sessionID, "", "", 0, 0, 0, 0, 1)
}

// extractTurnUsage extracts model name, input/output tokens, and total cost from
// a PiG turn_end event payload map.
func extractTurnUsage(data map[string]any) (model string, inputTok, outputTok int, cost float64) {
	if data == nil {
		return "", 0, 0, 0
	}
	msg, ok := data["message"].(map[string]any)
	if !ok {
		return "", 0, 0, 0
	}
	if m, ok := msg["model"].(string); ok {
		model = m
	}
	u, ok := msg["usage"].(map[string]any)
	if !ok {
		return model, 0, 0, 0
	}
	inputTok = anyToInt(u["input"])
	outputTok = anyToInt(u["output"])
	switch c := u["cost"].(type) {
	case float64:
		cost = c
	case map[string]any:
		cost = anyToFloat(c["total"])
	}
	return model, inputTok, outputTok, cost
}

func stringParam(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func intParam(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	return anyToInt(m[key])
}

func stringSliceParam(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{v}
	}
	return nil
}

func anyToInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func anyToFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
