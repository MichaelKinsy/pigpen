package web

// Unified web_search tool execution, provider chain dispatch, and SDK registration.
// upstream: packages/coding-agent/src/web/search/index.ts

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

//go:embed web-search.md
var webSearchDescription string

//go:embed web-search-system.md
var webSearchSystemPrompt string

// httpClient is the single HTTP client used by all search providers and Fetch.
// Tests replace it (or its Transport) to route against httptest servers.
var httpClient = &http.Client{
	Timeout: DefaultWebSearchTimeoutSeconds * time.Second,
}

// ExecuteSearch runs the web search pipeline over the configured provider chain.
// upstream: packages/coding-agent/src/web/search/index.ts:168-338
func ExecuteSearch(ctx context.Context, params SearchParams) (string, SearchResultDetails) {
	return executeSearchWithProviders(ctx, httpClient, defaultProviders, params)
}

func executeSearchWithProviders(
	ctx context.Context,
	client *http.Client,
	providers []SearchProvider,
	params SearchParams,
) (string, SearchResultDetails) {
	parsed := ParseSearchQuery(params.Query)
	params.ParsedQuery = &parsed
	if params.SystemPrompt == "" {
		params.SystemPrompt = strings.TrimSpace(webSearchSystemPrompt)
	}
	if params.Timeout <= 0 {
		params.Timeout = DefaultWebSearchTimeoutSeconds * time.Second
	} else if maxD := MaxWebSearchTimeoutSeconds * time.Second; params.Timeout > maxD {
		params.Timeout = maxD
	}

	var failures []providerFailure
	availableCount := 0
	var lastID, lastLabel string
	failedProvider := "none"

	for _, prov := range providers {
		if !prov.IsAvailable() {
			continue
		}
		availableCount++
		lastID = prov.ID()
		lastLabel = prov.Label()

		response, err := prov.Search(ctx, client, params)
		if err != nil {
			if ctx.Err() != nil {
				msg := ctx.Err().Error()
				return "Error: " + msg, SearchResultDetails{
					Response: SearchResponse{Provider: prov.ID(), Sources: []SearchSource{}},
					Error:    msg,
				}
			}
			failedProvider = prov.ID()
			failures = append(failures, providerFailure{id: prov.ID(), label: prov.Label(), err: err})
			continue
		}

		// Lenient constraint post-filter matching omp's central pipeline.
		// upstream: packages/coding-agent/src/web/search/index.ts:280-290
		finalResponse := response
		var constraintNotes []string
		if parsed.HasConstraints && len(response.Sources) > 0 {
			filtered := ApplyQueryConstraints(response.Sources, parsed)
			if len(filtered.Sources) != len(response.Sources) {
				finalResponse.Sources = filtered.Sources
			}
			for _, label := range filtered.Dropped {
				constraintNotes = append(constraintNotes, fmt.Sprintf("no results matched `%s`; the constraint was relaxed", label))
			}
		}

		if !hasRenderableSearchContent(finalResponse) {
			err := &SearchProviderError{
				Provider: prov.ID(),
				Message:  fmt.Sprintf("%s returned no renderable search content.", prov.Label()),
				Status:   204,
			}
			failedProvider = prov.ID()
			failures = append(failures, providerFailure{id: prov.ID(), label: prov.Label(), err: err})
			continue
		}

		text := formatForLLM(finalResponse, constraintNotes)
		return text, SearchResultDetails{Response: finalResponse}
	}

	if availableCount == 0 && len(failures) == 0 {
		// upstream: packages/coding-agent/src/web/search/index.ts:314-322
		msg := "No web search model configured."
		return "Error: " + msg, SearchResultDetails{
			Response: SearchResponse{Provider: "none", Sources: []SearchSource{}},
			Error:    msg,
		}
	}

	var msg string
	if len(failures) > 1 {
		msg = "All web search providers failed: " + formatSearchProviderFailures(failures)
	} else if len(failures) == 1 {
		f := failures[0]
		msg = formatSearchProviderFailure(f.err, f.id, f.label)
	} else {
		if lastLabel == "" {
			lastLabel = "web search provider"
		}
		_ = lastID
		msg = "Unknown error from " + lastLabel
	}

	return "Error: " + msg, SearchResultDetails{
		Response: SearchResponse{Provider: failedProvider, Sources: []SearchSource{}},
		Error:    msg,
	}
}

// webSearchSchema mirrors omp's webSearchSchema verbatim.
// upstream: packages/coding-agent/src/web/search/index.ts:44-51
func webSearchSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"recency": map[string]any{
				"type": "string",
				"enum": []any{"day", "week", "month", "year"},
			},
			"limit":              map[string]any{"type": "number"},
			"max_tokens":         map[string]any{"type": "number"},
			"temperature":        map[string]any{"type": "number"},
			"num_search_results": map[string]any{"type": "number"},
		},
		"required":             []any{"query"},
		"additionalProperties": false,
	}
}

func readIntParam(params map[string]any, key string) int {
	v, ok := params[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0
		}
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func readFloatParam(params map[string]any, key string) *float64 {
	v, ok := params[key]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
		f := n
		return &f
	case int:
		f := float64(n)
		return &f
	}
	return nil
}

func webSearchToolDef() sdk.ToolDefinition {
	readOnly := true
	openWorld := true
	return sdk.ToolDefinition{
		Name:          "web_search",
		Label:         "Web Search",
		Description:   strings.TrimSpace(webSearchDescription),
		PromptSnippet: "Search the web for up-to-date information",
		Parameters:    webSearchSchema(),
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint:  &readOnly,
			OpenWorldHint: &openWorld,
		},
		Execute: handleWebSearch,
	}
}

func handleWebSearch(ctx sdk.Context, params map[string]any) (any, error) {
	query, _ := params["query"].(string)
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query is required")
	}
	recency, _ := params["recency"].(string)
	switch recency {
	case "", "day", "week", "month", "year":
	default:
		return nil, fmt.Errorf("invalid recency %q: must be day, week, month, or year", recency)
	}

	goCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if done := ctx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-goCtx.Done():
			}
		}()
	}

	text, details := ExecuteSearch(goCtx, SearchParams{
		Query:            query,
		Recency:          recency,
		Limit:            readIntParam(params, "limit"),
		MaxOutputTokens:  readIntParam(params, "max_tokens"),
		Temperature:      readFloatParam(params, "temperature"),
		NumSearchResults: readIntParam(params, "num_search_results"),
	})
	return sdk.ToolResult{
		Content: text,
		Details: details,
	}, nil
}

// Register installs the web_search tool on e without performing any I/O.
func Register(e *sdk.Extension) {
	e.RegisterTool(webSearchToolDef())
}
