package websearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Port of xai-search.ts: xAI's Agent Tools API, a hosted `web_search` (and opt-in `x_search`)
// tool on an OpenAI-compatible Responses endpoint. The request body is deliberately minimal
// (model, input, tools): it is the shape the original verified against a live subscription, and
// a 400 from an unsupported field would cost the user the search (xai-search.ts:16-27).

const (
	xaiResponsesURL  = "https://api.x.ai/v1/responses"
	xaiSearchTimeout = 60 * time.Second
)

// The preference lists are ordered best-first; the first model the registry knows wins
// (xai-search.ts:29-38).
var (
	xaiWebSearchModelCandidates = []string{"grok-4.5", "grok-4.3", "grok-build-0.1"}
	xaiXSearchModelCandidates   = []string{"grok-4.6", "grok-4.5", "grok-4.3", "grok-build-0.1"}
)

type xaiAuth struct {
	apiKey  string
	model   string
	headers providerHeaders
}

func xaiSearchModelOverride(cfg map[string]any) (string, error) {
	value := cfg["xaiSearchModel"]
	if value == nil {
		return "", nil
	}
	s, ok := mpString(value)
	if !ok || jsTrim(s) == "" {
		return "", fmt.Errorf("xaiSearchModel in %s must be a non-empty string", ConfigPath())
	}
	return jsTrim(s), nil
}

// xaiSearchTools is resolveConfiguredSearchTools: web_search by default.
func xaiSearchTools(cfg map[string]any) ([]string, error) {
	value, present := cfg["xaiSearchTools"]
	if !present {
		return []string{"web_search"}, nil
	}
	list, ok := mpArray(value)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("xaiSearchTools in %s must be a non-empty array containing only web_search or x_search", ConfigPath())
	}
	var tools []string
	for _, v := range list {
		tool, _ := mpString(v)
		if tool != "web_search" && tool != "x_search" {
			return nil, fmt.Errorf("xaiSearchTools in %s may only contain web_search or x_search", ConfigPath())
		}
		if sliceHas(tools, tool) {
			return nil, fmt.Errorf("xaiSearchTools in %s must not contain duplicates: %s", ConfigPath(), tool)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func xaiModelCandidates(tools []string) []string {
	if sliceHas(tools, "x_search") {
		return xaiXSearchModelCandidates
	}
	return xaiWebSearchModelCandidates
}

// resolveXaiPiAuth resolves auth from the model registry first, so a Grok subscription pays for
// its own searches and no API key has to be configured (xai-search.ts:94-114). Any failure on
// one candidate moves on to the next.
func resolveXaiPiAuth(ec ExtensionContext, modelOverride string, tools []string) *xaiAuth {
	reg := ec.ModelRegistry()
	for _, id := range xaiModelCandidates(tools) {
		model := reg.Find("xai", id)
		if model == nil {
			continue
		}
		resolved, err := registryAuthFor(reg, model)
		if err != nil || !resolved.OK || resolved.APIKey == "" {
			continue
		}
		picked := id
		if modelOverride != "" {
			picked = modelOverride
		}
		return &xaiAuth{apiKey: resolved.APIKey, model: picked, headers: resolved.Headers}
	}
	return nil
}

func xaiCredentialOptions(cfg map[string]any) CredentialOptions {
	return CredentialOptions{Provider: "xAI", ConfiguredValue: cfg["xaiApiKey"], EnvironmentValue: envOrNil("XAI_API_KEY")}
}

func resolveXaiAuth(ctx context.Context, ec ExtensionContext, cfg map[string]any) (*xaiAuth, error) {
	modelOverride, err := xaiSearchModelOverride(cfg)
	if err != nil {
		return nil, err
	}
	tools, err := xaiSearchTools(cfg)
	if err != nil {
		return nil, err
	}
	if ec != nil {
		if auth := resolveXaiPiAuth(ec, modelOverride, tools); auth != nil {
			return auth, nil
		}
	}
	if !HasCredentialSource(xaiCredentialOptions(cfg)) {
		return nil, nil
	}
	apiKey, err := ResolveCredential(ctx, xaiCredentialOptions(cfg))
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, nil
	}
	model := xaiModelCandidates(tools)[0]
	if modelOverride != "" {
		model = modelOverride
	}
	return &xaiAuth{apiKey: apiKey, model: model, headers: providerHeaders{}}, nil
}

// IsXaiSearchAvailable is isXaiSearchAvailable: a configuration error only makes xAI
// unavailable.
func IsXaiSearchAvailable(ec ExtensionContext) bool {
	cfg, err := providerConfig()
	if err != nil {
		return false
	}
	tools, err := xaiSearchTools(cfg)
	if err != nil {
		return false
	}
	if ec != nil && resolveXaiPiAuth(ec, "", tools) != nil {
		return true
	}
	return HasCredentialSource(xaiCredentialOptions(cfg))
}

// buildXaiInput folds recency, result count and domain filters into the prompt rather than
// tool parameters: the filter field names are not verified against the live API (xai-search.ts:
// 163-180).
func buildXaiInput(query string, o SearchOptions, tools []string) string {
	web, x := sliceHas(tools, "web_search"), sliceHas(tools, "x_search")
	var instruction string
	switch {
	case x && web:
		instruction = "Search the web and X and answer using only what the search results say."
	case x:
		instruction = "Search X and answer using only what the X results say."
	default:
		instruction = "Search the web and answer using only what the web results say."
	}
	lines := []string{instruction, "Cite your sources inline."}
	if o.RecencyFilter != "" {
		lines = append(lines, "Prefer sources from the "+recencyLabelOrUndefined(o.RecencyFilter)+".")
	}
	if n, ok := mpFinitePositive(o.NumResults); ok {
		lines = append(lines, fmt.Sprintf("Prefer around %d distinct sources.", mpCap(n)))
	}
	f := normalizeDomainFilters(o.DomainFilter)
	if len(f.allowed) > 0 {
		lines = append(lines, "Only use sources from: "+strings.Join(capDomains(f.allowed), ", ")+".")
	}
	if len(f.blocked) > 0 {
		lines = append(lines, "Do not use sources from: "+strings.Join(capDomains(f.blocked), ", ")+".")
	}
	return strings.Join(lines, " ") + "\n\n" + query
}

func addXaiResult(results *[]SearchResult, seen map[string]bool, rawURL, title any, snippet string) {
	u, ok := mpString(rawURL)
	if !ok || jsTrim(u) == "" || seen[u] {
		return
	}
	seen[u] = true
	t := u
	if s, ok := mpString(title); ok && jsTrim(s) != "" {
		t = s
	}
	*results = append(*results, SearchResult{Title: t, URL: u, Snippet: snippet})
}

func addXaiSources(results *[]SearchResult, seen map[string]bool, sources any) {
	list, ok := mpArray(sources)
	if !ok {
		return
	}
	for _, s := range list {
		if str, ok := mpString(s); ok {
			addXaiResult(results, seen, str, nil, "")
			continue
		}
		if m, ok := mpObject(s); ok {
			addXaiResult(results, seen, firstDefined(m["url"], m["source_website_url"]), firstDefined(m["title"], m["caption"]), "")
		}
	}
}

// extractXaiSearchResults keeps annotation sources first (they carry titles and offsets and win
// the URL de-duplication), then the raw sources of web and X search calls, then the response-level
// `citations` list.
func extractXaiSearchResults(output []any, citations any, numResults *float64) []SearchResult {
	results := []SearchResult{}
	seen := map[string]bool{}
	for _, item := range output {
		m, ok := mpObject(item)
		if !ok || m["type"] != "message" {
			continue
		}
		content, ok := mpArray(m["content"])
		if !ok {
			continue
		}
		for _, p := range content {
			part, ok := mpObject(p)
			if !ok {
				continue
			}
			text, _ := mpString(part["text"])
			annotations, ok := mpArray(part["annotations"])
			if !ok {
				continue
			}
			for _, a := range annotations {
				ann, ok := mpObject(a)
				if !ok || ann["type"] != "url_citation" {
					continue
				}
				addXaiResult(&results, seen, ann["url"], ann["title"], extractSnippetAround(text, ann["start_index"], ann["end_index"]))
			}
		}
	}
	for _, item := range output {
		m, ok := mpObject(item)
		if !ok || (m["type"] != "web_search_call" && m["type"] != "x_search_call") {
			continue
		}
		var actionSources any
		if action, ok := mpObject(m["action"]); ok {
			actionSources = action["sources"]
		}
		for _, group := range []any{actionSources, m["sources"], m["results"]} {
			addXaiSources(&results, seen, group)
		}
	}
	addXaiSources(&results, seen, citations)
	return limitResults(results, numResults)
}

type xaiRequestBody struct {
	Model string              `json:"model"`
	Input string              `json:"input"`
	Tools []map[string]string `json:"tools"`
}

// SearchWithXai is searchWithXai.
func SearchWithXai(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	cfg, err := providerConfig()
	if err != nil {
		return nil, err
	}
	tools, err := xaiSearchTools(cfg)
	if err != nil {
		return nil, err
	}
	auth, err := resolveXaiAuth(ctx, ExtensionContextFrom(ctx), cfg)
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, fmt.Errorf("xAI search unavailable. Either:\n  1. Use /login to sign in with a SuperGrok or X Premium subscription\n  2. Create %s with { \"xaiApiKey\": \"your-key\" }\n  3. Set XAI_API_KEY environment variable", ConfigPath())
	}

	body := xaiRequestBody{Model: auth.model, Input: buildXaiInput(query, o, tools)}
	for _, t := range tools {
		body.Tools = append(body.Tools, map[string]string{"type": t})
	}
	payload, err := encodeJSONBody(body)
	if err != nil {
		return nil, err
	}
	headers := requestHeaderObject(auth.headers)
	headers.set("Authorization", "Bearer "+auth.apiKey)
	headers.set("Content-Type", "application/json")

	act := logAPIStart(query)
	status := 0
	tctx, cancel := withTimeout(ctx, xaiSearchTimeout)
	defer cancel()
	result, err := func() (*SearchResponse, error) {
		resp, err := FetchWithCredentialRedirects(tctx, xaiResponsesURL, RequestInit{Method: "POST", Header: headersFromObject(headers).http(), Body: payload}, plainFetchCredentialHeaders)
		if err != nil {
			return nil, timeoutFix(ctx, tctx, err)
		}
		status = resp.StatusCode
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			act.logError(fmt.Sprintf("HTTP %d", resp.StatusCode))
		}
		text, err := readBody(resp)
		if err != nil {
			return nil, timeoutFix(ctx, tctx, err)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("xAI API error %d: %s", resp.StatusCode, slice300(RedactCredential(text, auth.apiKey)))
		}
		parsed, err := decodeResponseJSON(text)
		if err != nil {
			return nil, fmt.Errorf("xAI API returned invalid JSON: %s", err.Error())
		}
		var output []any
		var citations any
		switch p := parsed.(type) {
		case nil:
			return nil, errors.New("Cannot read properties of null (reading 'output')")
		case map[string]any:
			output, _ = mpArray(p["output"])
			citations = p["citations"]
		}
		answer := extractOpenAIAnswer(output)
		results := extractXaiSearchResults(output, citations, o.NumResults)
		if answer == "" && len(results) == 0 {
			return nil, fmt.Errorf("xAI %s returned no answer or sources", strings.Join(tools, "+"))
		}
		return &SearchResponse{Answer: answer, Results: results}, nil
	}()
	if err != nil {
		return nil, act.failedErr(redactErr(err, auth.apiKey))
	}
	act.complete(status)
	return result, nil
}
