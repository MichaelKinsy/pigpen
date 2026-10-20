package websearch

import (
	"context"
	"errors"
	"strings"
)

// Port of query-rewrite.ts: ask a small model for a better search query.

type modelCandidate struct{ provider, id string }

// rewriteModelCandidates are tried in order (query-rewrite.ts:21-25).
var rewriteModelCandidates = []modelCandidate{
	{"anthropic", "claude-haiku-4-5"},
	{"google", "gemini-3.6-flash"},
	{"openai", "gpt-5-mini"},
}

// resolveFirstAvailableModel returns the first candidate that resolves, is enabled and has
// credentials (query-rewrite.ts:6-18).
func resolveFirstAvailableModel(ctx context.Context, sc SideCallContext, candidates []modelCandidate) (map[string]any, error) {
	registry := sc.Registry()
	scoped, scopedErr := sc.ScopedModels()
	for _, candidate := range candidates {
		model, err := FindModelWithProviderRouting(registry, candidate.provider, candidate.id)
		if err != nil {
			return nil, err
		}
		if model == nil {
			continue
		}
		if scopedErr != nil {
			return nil, scopedErr
		}
		if !IsModelInScope(model, scoped) {
			continue
		}
		auth, err := awaitWithAbort(ctx, func() (map[string]any, error) { return registry.GetApiKeyAndHeaders(model) })
		if err != nil {
			return nil, err
		}
		if ok, _ := auth["ok"].(bool); ok && auth["apiKey"] != nil && auth["apiKey"] != "" {
			return model, nil
		}
	}
	names := make([]string, len(candidates))
	for i, candidate := range candidates {
		names[i] = candidate.provider + "/" + candidate.id
	}
	return nil, errors.New("No enabled model available: " + strings.Join(names, ", "))
}

// RewriteSearchQuery rewrites query with the first available model of rewriteModelCandidates.
func RewriteSearchQuery(ctx context.Context, query string, sc SideCallContext) (string, error) {
	model, err := resolveFirstAvailableModel(ctx, sc, rewriteModelCandidates)
	if err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", errAborted
	}
	response, err := sc.Registry().Complete(ctx, model, map[string]any{
		"messages": []any{userMessage("Rewrite this web search query to get better, more specific results. Add relevant year qualifiers, precise technical terms, and specificity. Return ONLY the improved query text, nothing else.\n\nQuery: " + query)},
	}, CompleteOptions{})
	if err != nil {
		return "", err
	}
	if response["stopReason"] == "aborted" {
		return "", errAborted
	}
	parts, _ := response["content"].([]any)
	var text strings.Builder
	for _, part := range parts {
		object, _ := part.(map[string]any)
		if value, ok := object["text"].(string); ok && object["type"] == "text" {
			text.WriteString(value)
		}
	}
	rewritten := jsTrim(text.String())
	if rewritten == "" {
		return "", errors.New("Rewrite returned empty response")
	}
	return rewritten, nil
}
