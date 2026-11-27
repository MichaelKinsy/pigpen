package media

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	defaultToolSearchLimit = 10
	maxToolSearchLimit     = 50
	maxDescribeNames       = 25
	maxCatalogDescChars    = 300
)

// ToolCatalogEntry is a compact summary entry returned by tool_search.
type ToolCatalogEntry struct {
	Name        string   `json:"name"`
	Source      string   `json:"source,omitempty"`
	Description string   `json:"description"`
	Required    []string `json:"required,omitempty"`
	Score       float64  `json:"score"`
}

// ToolSearchResult is the structured result returned by tool_search.
type ToolSearchResult struct {
	Query          string             `json:"query"`
	TotalAvailable int                `json:"total_available"`
	Matches        []ToolCatalogEntry `json:"matches"`
}

// ToolDescribeEntry is a detailed tool record returned by tool_describe.
type ToolDescribeEntry struct {
	Name             string          `json:"name"`
	Source           string          `json:"source,omitempty"`
	Description      string          `json:"description"`
	Parameters       json.RawMessage `json:"parameters,omitempty"`
	PromptGuidelines []string        `json:"prompt_guidelines,omitempty"`
}

// ToolDescribeResult is the structured result returned by tool_describe.
type ToolDescribeResult struct {
	Tools    []ToolDescribeEntry `json:"tools"`
	NotFound []string            `json:"not_found,omitempty"`
}

// ToolCallBridgeResult is the structured result returned by tool_call.
type ToolCallBridgeResult struct {
	Name       string `json:"name"`
	IsError    bool   `json:"is_error"`
	Content    string `json:"content"`
	DurationMs *int64 `json:"duration_ms,omitempty"`
}

type toolFetcher func() ([]sdk.ToolInfo, error)
type toolExecutor func(name string, args any, options *sdk.ExecuteToolOptions) (sdk.AgentToolCallOutcome, error)

func registerToolSearchTools(ext *sdk.Extension) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "tool_search",
		Label:         "Tool Search",
		Description:   "Search all registered session tools by keyword or capability and return a ranked compact catalog.",
		PromptSnippet: "Search available session tools by keyword",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query matching tool names, descriptions, or parameters.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of ranked matches to return (default 10, max 50).",
					"minimum":     1,
					"maximum":     maxToolSearchLimit,
					"default":     defaultToolSearchLimit,
				},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return handleToolSearch(params, ctx.GetAllTools), nil
		},
	})

	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "tool_describe",
		Label:         "Tool Describe",
		Description:   "Return full descriptions, parameter schemas, and metadata for one or more named session tools.",
		PromptSnippet: "Inspect full metadata and schema for named tools",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"names": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "List of exact tool names to describe.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Single tool name to describe (shorthand for names).",
				},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return handleToolDescribe(params, ctx.GetAllTools), nil
		},
	})

	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "tool_call",
		Label:         "Tool Call",
		Description:   "Invoke a discovered session tool by name with JSON arguments through the host tool dispatcher.",
		PromptSnippet: "Invoke a discovered tool by name and arguments",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Exact name of the tool to invoke.",
				},
				"arguments": map[string]any{
					"type":        "object",
					"description": "JSON object of arguments to pass to the target tool.",
				},
			},
			"required": []string{"name"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return handleToolCall(params, ctx.ExecuteTool), nil
		},
	})
}

func handleToolSearch(params map[string]any, fetch toolFetcher) sdk.ToolResult {
	query := strings.TrimSpace(stringParam(params, "query"))
	if query == "" {
		// Accept upstream compat_arg "queries" (array of strings) if "query" was omitted.
		if qs, err := stringSliceParam(params, "queries"); err == nil && len(qs) > 0 {
			query = strings.Join(qs, " ")
		}
	}
	if query == "" {
		return toolErrorResult("tool_search: query is required")
	}

	limit, err := intParam(params, "limit", defaultToolSearchLimit)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("tool_search: %v", err))
	}
	if limit <= 0 {
		limit = defaultToolSearchLimit
	}
	if limit > maxToolSearchLimit {
		limit = maxToolSearchLimit
	}

	allTools, err := fetch()
	if err != nil {
		return toolErrorResult(fmt.Sprintf("tool_search: list tools: %v", err))
	}

	queryLower := strings.ToLower(query)
	tokens := tokenizeSearchText(query)

	type scored struct {
		entry ToolCatalogEntry
	}
	matches := make([]scored, 0, len(allTools))
	for _, info := range allTools {
		if info.Name == "" {
			continue
		}
		score := scoreToolInfo(info, queryLower, tokens)
		if score <= 0 {
			continue
		}
		matches = append(matches, scored{
			entry: ToolCatalogEntry{
				Name:        info.Name,
				Source:      toolSourceLabel(info),
				Description: clipText(strings.TrimSpace(info.Description), maxCatalogDescChars),
				Required:    extractRequiredParams(info.Parameters),
				Score:       score,
			},
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].entry.Score != matches[j].entry.Score {
			return matches[i].entry.Score > matches[j].entry.Score
		}
		return matches[i].entry.Name < matches[j].entry.Name
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	out := ToolSearchResult{
		Query:          query,
		TotalAvailable: len(allTools),
		Matches:        make([]ToolCatalogEntry, 0, len(matches)),
	}
	for _, m := range matches {
		out.Matches = append(out.Matches, m.entry)
	}
	return jsonToolResult(out, false)
}

func handleToolDescribe(params map[string]any, fetch toolFetcher) sdk.ToolResult {
	names, err := stringSliceParam(params, "names")
	if err != nil {
		return toolErrorResult(fmt.Sprintf("tool_describe: %v", err))
	}
	if single := strings.TrimSpace(stringParam(params, "name")); single != "" {
		names = append(names, single)
	}

	// Deduplicate preserving order.
	seen := make(map[string]bool, len(names))
	unique := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		unique = append(unique, n)
	}
	if len(unique) == 0 {
		return toolErrorResult("tool_describe: name or names is required")
	}
	if len(unique) > maxDescribeNames {
		unique = unique[:maxDescribeNames]
	}

	allTools, err := fetch()
	if err != nil {
		return toolErrorResult(fmt.Sprintf("tool_describe: list tools: %v", err))
	}

	byName := make(map[string]sdk.ToolInfo, len(allTools))
	for _, info := range allTools {
		if info.Name != "" {
			byName[info.Name] = info
		}
	}

	res := ToolDescribeResult{
		Tools: make([]ToolDescribeEntry, 0, len(unique)),
	}
	for _, name := range unique {
		info, ok := byName[name]
		if !ok {
			res.NotFound = append(res.NotFound, name)
			continue
		}
		res.Tools = append(res.Tools, ToolDescribeEntry{
			Name:             info.Name,
			Source:           toolSourceLabel(info),
			Description:      info.Description,
			Parameters:       info.Parameters,
			PromptGuidelines: info.PromptGuidelines,
		})
	}
	return jsonToolResult(res, false)
}

func handleToolCall(params map[string]any, execFn toolExecutor) sdk.ToolResult {
	name := strings.TrimSpace(stringParam(params, "name"))
	if name == "" {
		return toolErrorResult("tool_call: name is required")
	}
	if strings.EqualFold(name, "tool_call") {
		return toolErrorResult("tool_call: recursive invocation of tool_call is not permitted")
	}

	args := map[string]any{}
	if rawArgs, ok := params["arguments"]; ok && rawArgs != nil {
		switch t := rawArgs.(type) {
		case map[string]any:
			args = t
		case string:
			trimmed := strings.TrimSpace(t)
			if trimmed != "" {
				if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
					return toolErrorResult(fmt.Sprintf("tool_call: arguments must be a JSON object: %v", err))
				}
			}
		default:
			return toolErrorResult("tool_call: arguments must be a JSON object")
		}
	}

	outcome, err := execFn(name, args, nil)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("tool_call: execute %q: %v", name, err))
	}

	content := outcome.Result.Text()
	isErr := outcome.IsError || outcome.Result.IsError
	var images []sdk.ImageContent
	for _, block := range outcome.Result.Content {
		if block.Type == "image" && block.Data != "" {
			images = append(images, sdk.ImageContent{
				Data:     block.Data,
				MimeType: block.MimeType,
			})
		}
	}

	structured := ToolCallBridgeResult{
		Name:       name,
		IsError:    isErr,
		Content:    content,
		DurationMs: outcome.DurationMs,
	}
	return sdk.ToolResult{
		Content:           content,
		Images:            images,
		StructuredContent: structured,
		IsError:           isErr,
	}
}

func tokenizeSearchText(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func scoreToolInfo(info sdk.ToolInfo, queryLower string, tokens []string) float64 {
	nameLower := strings.ToLower(info.Name)
	descLower := strings.ToLower(info.Description)
	paramLower := strings.ToLower(string(info.Parameters))

	var score float64

	// Exact or substring match against tool name carries highest weight.
	if nameLower == queryLower {
		score += 100
	} else if strings.Contains(nameLower, queryLower) {
		score += 45
	}
	if strings.Contains(descLower, queryLower) {
		score += 20
	}

	if len(tokens) == 0 {
		return score
	}

	nameTokens := make(map[string]bool)
	for _, t := range tokenizeSearchText(info.Name) {
		nameTokens[t] = true
	}

	matchedTokens := 0
	for _, tok := range tokens {
		tokMatched := false
		if nameTokens[tok] {
			score += 18
			tokMatched = true
		} else if strings.Contains(nameLower, tok) {
			score += 12
			tokMatched = true
		}
		if strings.Contains(descLower, tok) {
			score += 6
			tokMatched = true
		}
		if strings.Contains(paramLower, tok) {
			score += 2
			tokMatched = true
		}
		if tokMatched {
			matchedTokens++
		}
	}

	// Bonus when every query token matched somewhere.
	if matchedTokens == len(tokens) && len(tokens) > 1 {
		score += 10
	}
	return score
}

func toolSourceLabel(info sdk.ToolInfo) string {
	if info.SourceInfo.Source != "" {
		return info.SourceInfo.Source
	}
	return info.Source
}

func clipText(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "…"
}

func extractRequiredParams(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil
	}
	return schema.Required
}
