package websearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Port of mistral-search.ts: Mistral's Conversations API with its built-in web_search tool.

const (
	mistralConversationsURL = "https://api.mistral.ai/v1/conversations"
	mistralSearchTimeout    = 60 * time.Second
	mistralDefaultModel     = "mistral-small-latest"
	mistralMaxResults       = 20
)

func mistralSearchModel(cfg map[string]any) (string, error) {
	value, present := cfg["mistralSearchModel"]
	if !present {
		return mistralDefaultModel, nil
	}
	s, ok := mpString(value)
	if !ok || jsTrim(s) == "" {
		return "", fmt.Errorf("mistralSearchModel in %s must be a non-empty string", ConfigPath())
	}
	return jsTrim(s), nil
}

func mistralSearchTool(cfg map[string]any) (string, error) {
	value, present := cfg["mistralSearchTool"]
	if !present {
		return "web_search", nil
	}
	if s, _ := mpString(value); s != "web_search" && s != "web_search_premium" {
		return "", fmt.Errorf("mistralSearchTool in %s must be either web_search or web_search_premium", ConfigPath())
	}
	return value.(string), nil
}

// normalizeMistralCount clamps to 1..20 with 5 for a missing or non-finite value.
func normalizeMistralCount(v *float64) int { return NormalizeSearchResultCount(v) }

func mistralCredentialOptions(cfg map[string]any) CredentialOptions {
	return CredentialOptions{Provider: "Mistral", ConfiguredValue: cfg["mistralApiKey"], EnvironmentValue: envOrNil("MISTRAL_API_KEY")}
}

// IsMistralAvailable is isMistralAvailable: a configuration error only makes it unavailable.
func IsMistralAvailable() bool {
	cfg, err := providerConfig()
	if err != nil {
		return false
	}
	if _, err := mistralSearchModel(cfg); err != nil {
		return false
	}
	if _, err := mistralSearchTool(cfg); err != nil {
		return false
	}
	return HasCredentialSource(mistralCredentialOptions(cfg))
}

func buildMistralPrompt(query string, o SearchOptions, numResults int) string {
	var lines []string
	if o.RecencyFilter != "" {
		label, ok := recencyLabels[o.RecencyFilter]
		if !ok {
			label = o.RecencyFilter
		}
		lines = append(lines, "Prefer sources from the "+label+".")
	}
	if _, ok := mpFinitePositive(o.NumResults); ok {
		lines = append(lines, fmt.Sprintf("Prefer up to %d distinct sources.", numResults))
	}
	f := normalizeDomainFilters(o.DomainFilter)
	if len(f.allowed) > 0 {
		lines = append(lines, "Only use sources from: "+strings.Join(capDomains(f.allowed), ", ")+".")
	}
	if len(f.blocked) > 0 {
		lines = append(lines, "Do not use sources from: "+strings.Join(capDomains(f.blocked), ", ")+".")
	}
	if len(lines) == 0 {
		return query
	}
	return strings.Join(lines, " ") + "\n\n" + query
}

func mistralInvalidResponse(msg string) error {
	return errors.New("Mistral API returned invalid response: " + msg)
}

// trimmedMistralURL is normalizeResultUrl of mistral-search.ts: the trimmed original text of an
// absolute http(s) URL.
func trimmedMistralURL(v any) (string, bool) {
	s, ok := mpString(v)
	if !ok {
		return "", false
	}
	u := jsTrim(s)
	if u == "" {
		return "", false
	}
	if p, err := ParseURL(u, nil); err != nil || (p.Scheme != "http" && p.Scheme != "https") {
		return "", false
	}
	return u, true
}

func parseMistralConversation(value any, o SearchOptions, numResults int) (*SearchResponse, error) {
	envelope, ok := mpObject(value)
	if !ok {
		return nil, mistralInvalidResponse("expected an object envelope")
	}
	outputs, ok := mpArray(envelope["outputs"])
	if !ok {
		return nil, mistralInvalidResponse("outputs must be an array")
	}
	var answerParts []string
	results := []SearchResult{}
	seen := map[string]bool{}
	filters := normalizeDomainFilters(o.DomainFilter)
	for _, out := range outputs {
		entry, ok := mpObject(out)
		if !ok || entry["type"] != "message.output" {
			continue
		}
		if s, ok := mpString(entry["content"]); ok {
			if t := jsTrim(s); t != "" {
				answerParts = append(answerParts, t)
			}
			continue
		}
		content, ok := mpArray(entry["content"])
		if !ok {
			continue
		}
		for _, c := range content {
			part, ok := mpObject(c)
			if !ok {
				continue
			}
			if part["type"] == "text" {
				if s, ok := mpString(part["text"]); ok && jsTrim(s) != "" {
					answerParts = append(answerParts, jsTrim(s))
				}
				continue
			}
			if part["type"] != "tool_reference" || len(results) >= numResults {
				continue
			}
			u, ok := trimmedMistralURL(part["url"])
			if !ok || !filters.matches(u) || seen[u] {
				continue
			}
			seen[u] = true
			title := u
			if s, ok := mpString(part["title"]); ok && jsTrim(s) != "" {
				title = jsTrim(s)
			}
			snippet, _ := mpString(part["description"])
			results = append(results, SearchResult{Title: title, URL: u, Snippet: snippet})
		}
	}
	answer := jsTrim(strings.Join(answerParts, "\n"))
	if answer == "" && len(results) == 0 {
		return nil, mistralInvalidResponse("no answer or sources")
	}
	return &SearchResponse{Answer: answer, Results: results}, nil
}

type mistralInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type mistralRequestBody struct {
	Inputs []mistralInput      `json:"inputs"`
	Stream bool                `json:"stream"`
	Model  string              `json:"model"`
	Tools  []map[string]string `json:"tools"`
}

// SearchWithMistral is searchWithMistral.
func SearchWithMistral(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	cfg, err := providerConfig()
	if err != nil {
		return nil, err
	}
	model, err := mistralSearchModel(cfg)
	if err != nil {
		return nil, err
	}
	tool, err := mistralSearchTool(cfg)
	if err != nil {
		return nil, err
	}
	numResults := normalizeMistralCount(o.NumResults)
	apiKey, err := ResolveCredential(ctx, mistralCredentialOptions(cfg))
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Mistral API key not found. Either:\n  1. Create %s with { \"mistralApiKey\": \"your-key\" }\n  2. Set MISTRAL_API_KEY environment variable\nGet a key at https://console.mistral.ai/api-keys", ConfigPath())
	}
	payload, err := encodeJSONBody(mistralRequestBody{
		Inputs: []mistralInput{{Role: "user", Content: buildMistralPrompt(query, o, numResults)}},
		Model:  model,
		Tools:  []map[string]string{{"type": tool}},
	})
	if err != nil {
		return nil, err
	}
	headers := newJSHeaders()
	headers.set("Authorization", "Bearer "+apiKey)
	headers.set("Content-Type", "application/json")

	tctx, cancel := withTimeout(ctx, mistralSearchTimeout)
	defer cancel()
	result, err := func() (*SearchResponse, error) {
		resp, err := FetchWithCredentialRedirects(tctx, mistralConversationsURL, RequestInit{Method: "POST", Header: headers.http(), Body: payload}, plainFetchCredentialHeaders)
		if err != nil {
			return nil, timeoutFix(ctx, tctx, err)
		}
		text, err := readBody(resp)
		if err != nil {
			return nil, timeoutFix(ctx, tctx, err)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("Mistral API error %d: %s", resp.StatusCode, slice300(RedactCredential(text, apiKey)))
		}
		parsed, err := decodeResponseJSON(text)
		if err != nil {
			return nil, fmt.Errorf("Mistral API returned invalid JSON: %s", err.Error())
		}
		return parseMistralConversation(parsed, o, numResults)
	}()
	if err != nil {
		return nil, redactErr(err, apiKey)
	}
	return result, nil
}
