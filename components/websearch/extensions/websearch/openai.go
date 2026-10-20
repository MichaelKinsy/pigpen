package websearch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Port of openai-search.ts: hosted web search on the OpenAI Responses API (an API key, or a
// Codex or OpenAI login resolved through the model registry), the alpha/search protocol, the
// provider-URL reuse switch and the OpenCode attribution.

const (
	openAIResponsesURL = "https://api.openai.com/v1/responses"
	codexResponsesURL  = "https://chatgpt.com/backend-api/codex/responses"
)

// openAISearchTimeout is SEARCH_TIMEOUT_MS; a variable so a test can shorten the deadline.
var openAISearchTimeout = 60 * time.Second

var defaultOpenAISearchProviders = []string{"openai-codex", "openai"}

// customOpenAIBaseURLError is CustomOpenAIBaseUrlError.
type customOpenAIBaseURLError struct{ msg string }

func (e *customOpenAIBaseURLError) Error() string { return e.msg }

// OpenAIAlphaSearchUnsupportedError is OpenAIAlphaSearchUnsupportedError: the endpoint or a
// filter cannot use alpha/search, which routing classifies as "unsupported".
type OpenAIAlphaSearchUnsupportedError struct{ Msg string }

func (e *OpenAIAlphaSearchUnsupportedError) Error() string { return e.Msg }

// ---- model choice (openai-search.ts:15-37) ------------------------------------------------

var (
	excludedModelSegments = map[string]bool{"pro": true, "ultra": true}
	mainlineGPTRE         = regexp.MustCompile(`^gpt-\d+(\.\d+)?$`)
	versionedGPTRE        = regexp.MustCompile(`^gpt-\d`)
)

// pickSearchModel prefers the newest "terra" model, then the newest bare mainline GPT id, then
// any other versioned GPT id, and excludes the "pro" and "ultra" tiers. Ids sort descending with
// a numeric-aware comparison so gpt-5.10 comes before gpt-5.9. With none of the preferences it
// returns the newest candidate.
func pickSearchModel(models []map[string]any) map[string]any {
	var candidates []map[string]any
	for _, m := range models {
		id, ok := mpString(m["id"])
		if !ok {
			continue
		}
		excluded := false
		for _, seg := range strings.Split(id, "-") {
			if excludedModelSegments[seg] {
				excluded = true
			}
		}
		if !excluded {
			candidates = append(candidates, m)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return compareNumericLocale(candidates[j]["id"].(string), candidates[i]["id"].(string)) < 0
	})
	for _, prefers := range []func(string) bool{
		func(id string) bool { return strings.Contains(id, "terra") },
		mainlineGPTRE.MatchString,
		versionedGPTRE.MatchString,
	} {
		for _, m := range candidates {
			if prefers(m["id"].(string)) {
				return m
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return nil
}

// ---- configuration (openai-search.ts:110-127, 174-249) ------------------------------------

// providerConfig is the loadConfig() of the provider files: web-search.json, or an empty object.
func providerConfig() (map[string]any, error) {
	root, err := ReadConfigRoot()
	if err != nil {
		return nil, err
	}
	if root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

func resolveConfiguredResponsesURL(cfg map[string]any) (string, error) {
	value, present := cfg["openaiResponsesUrl"]
	if !present {
		return openAIResponsesURL, nil
	}
	bad := fmt.Errorf("openaiResponsesUrl in %s must be an absolute http(s) URL", ConfigPath())
	s, ok := mpString(value)
	if !ok || jsTrim(s) == "" {
		return "", bad
	}
	u, err := ParseURL(jsTrim(s), nil)
	if err != nil {
		return "", bad
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("openaiResponsesUrl in %s must use http or https", ConfigPath())
	}
	if JSHostname(u) == "opencode.ai" && u.Scheme != "https" {
		return "", fmt.Errorf("openaiResponsesUrl in %s must use HTTPS for opencode.ai", ConfigPath())
	}
	return u.String(), nil
}

func resolveUseProviderBaseURL(cfg map[string]any) (bool, error) {
	if _, present := cfg["openaiResponsesUrl"]; present {
		return false, nil
	}
	value, present := cfg["openaiUseProviderBaseUrl"]
	if !present {
		return false, nil
	}
	b, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("openaiUseProviderBaseUrl in %s must be a boolean", ConfigPath())
	}
	return b, nil
}

func resolveConfiguredSearchProviders(cfg map[string]any) ([]string, error) {
	value, present := cfg["openaiSearchProviders"]
	if !present {
		return defaultOpenAISearchProviders, nil
	}
	list, ok := mpArray(value)
	bad := fmt.Errorf("openaiSearchProviders in %s must be an array of non-empty Pi provider ids", ConfigPath())
	if !ok {
		return nil, bad
	}
	out := make([]string, len(list))
	for i, entry := range list {
		s, ok := mpString(entry)
		if !ok || jsTrim(s) == "" {
			return nil, bad
		}
		out[i] = jsTrim(s)
	}
	return out, nil
}

// resolveConfiguredSearchModel returns "" for an absent or null value.
func resolveConfiguredSearchModel(cfg map[string]any) (string, error) {
	value := cfg["openaiSearchModel"]
	if value == nil {
		return "", nil
	}
	s, ok := mpString(value)
	if !ok || jsTrim(s) == "" {
		return "", fmt.Errorf("openaiSearchModel in %s must be a non-empty string", ConfigPath())
	}
	return jsTrim(s), nil
}

func isAlphaSearchEnabled() (bool, error) {
	cfg, err := providerConfig()
	if err != nil {
		return false, err
	}
	value, present := cfg["openaiUseAlphaSearch"]
	if !present {
		return false, nil
	}
	b, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("openaiUseAlphaSearch in %s must be a boolean", ConfigPath())
	}
	return b, nil
}

// ---- Codex tokens (openai-search.ts:149-172) ----------------------------------------------

func decodeJWTPayload(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return nil
	}
	raw, err := base64URLDecodeLax(parts[1])
	if err != nil {
		return nil
	}
	payload, err := unmarshalAny(bytesToString(raw))
	if err != nil {
		return nil
	}
	m, _ := mpObject(payload)
	return m
}

func isCodexJWT(token string) bool {
	payload := decodeJWTPayload(token)
	v, ok := payload["https://api.openai.com/auth"]
	if !ok || v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	}
	return true
}

func extractAccountID(token string) string {
	auth, ok := mpObject(decodeJWTPayload(token)["https://api.openai.com/auth"])
	if !ok {
		return ""
	}
	id, _ := mpString(auth["chatgpt_account_id"])
	return jsTrim(id)
}

// ---- endpoints ----------------------------------------------------------------------------

// escapedPathOf is `url.pathname` (the percent-encoded form).
func escapedPathOf(u *url.URL) string { return u.EscapedPath() }

func setEscapedPath(u *url.URL, p string) {
	decoded, err := url.PathUnescape(p)
	if err != nil {
		u.Path, u.RawPath = p, ""
		return
	}
	u.Path, u.RawPath = decoded, p
}

func trimTrailingSlashes(p string) string { return strings.TrimRight(p, "/") }

// resolveProviderResponsesURL is resolveProviderResponsesUrl: the Responses endpoint of a Pi
// provider's baseUrl.
func resolveProviderResponsesURL(baseURL any, useCodexEndpoint bool) (string, error) {
	invalid := &customOpenAIBaseURLError{"OpenAI search configuration: openaiUseProviderBaseUrl requires an absolute http(s) provider baseUrl"}
	s, ok := mpString(baseURL)
	if !ok || jsTrim(s) == "" {
		return "", invalid
	}
	u, err := ParseURL(jsTrim(s), nil)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", invalid
	}
	if JSHostname(u) == "opencode.ai" && u.Scheme != "https" {
		return "", &customOpenAIBaseURLError{"OpenAI search configuration: opencode.ai provider baseUrl must use HTTPS"}
	}
	path := trimTrailingSlashes(escapedPathOf(u))
	switch {
	case strings.HasSuffix(path, "/responses"):
		setEscapedPath(u, path)
	case useCodexEndpoint && strings.EqualFold(JSHostname(u), "chatgpt.com") && path == "/backend-api":
		setEscapedPath(u, "/backend-api/codex/responses")
	default:
		if path == "" {
			path = "/v1"
		}
		setEscapedPath(u, path+"/responses")
	}
	return u.String(), nil
}

// currentModelTarget is the endpoint of the selected model's official Responses API.
type currentModelTarget struct {
	responsesURL     string
	useCodexEndpoint bool
}

// resolveCurrentModelSearchTarget accepts only the official OpenAI Responses endpoint of the
// "openai" provider and the Codex endpoint of "openai-codex", over HTTPS.
func resolveCurrentModelSearchTarget(model map[string]any) (currentModelTarget, error) {
	baseURL, _ := mpString(model["baseUrl"])
	u, err := ParseURL(baseURL, nil)
	if err != nil {
		return currentModelTarget{}, errors.New("Invalid URL")
	}
	if u.Scheme != "https" {
		return currentModelTarget{}, errors.New("Current model base URL must use HTTPS")
	}
	provider, _ := mpString(model["provider"])
	api, _ := mpString(model["api"])
	host := strings.ToLower(JSHostname(u))
	if provider == "openai" && api == "openai-responses" && host == "api.openai.com" {
		setEscapedPath(u, trimTrailingSlashes(escapedPathOf(u))+"/responses")
		return currentModelTarget{responsesURL: u.String()}, nil
	}
	if provider == "openai-codex" && api == "openai-codex-responses" && host == "chatgpt.com" && trimTrailingSlashes(escapedPathOf(u)) == "/backend-api" {
		return currentModelTarget{responsesURL: codexResponsesURL, useCodexEndpoint: true}, nil
	}
	return currentModelTarget{}, errors.New("Current model is not backed by an official OpenAI Responses endpoint")
}

var gptPrefixRE = regexp.MustCompile(`(?i)^gpt-`)

// IsCurrentModelHostedSearchEligible is isCurrentModelHostedSearchEligible.
func IsCurrentModelHostedSearchEligible(ec ExtensionContext) bool {
	if ec == nil {
		return false
	}
	model := ec.Model()
	if model == nil {
		return false
	}
	id, _ := mpString(model["id"])
	if !gptPrefixRE.MatchString(id) {
		return false
	}
	_, err := resolveCurrentModelSearchTarget(model)
	return err == nil
}

// ---- credentials --------------------------------------------------------------------------

// openAIAuth is OpenAIAuth.
type openAIAuth struct {
	provider string
	apiKey   string
	model    string
	headers  providerHeaders
	// responsesURL is the endpoint before the Codex and alpha rewrites.
	responsesURL       string
	useCodexEndpoint   *bool
	useProviderBaseURL bool
}

// resolvePiAuth tries the configured Pi providers in order and returns the first whose
// credentials resolve (openai-search.ts:264-317). A custom baseUrl never reuses Pi credentials
// unless the user opted in with openaiResponsesUrl or openaiUseProviderBaseUrl.
func resolvePiAuth(ec ExtensionContext, responsesURL string, providers []string, modelOverride string, hasExplicitResponsesURL, useProviderBaseURL bool) (*openAIAuth, error) {
	reg := ec.ModelRegistry()
	models, err := reg.GetAll()
	if err != nil {
		return nil, nil
	}
	var invalidProviderURL error
	for _, provider := range providers {
		var ofProvider []map[string]any
		for _, m := range models {
			if p, _ := mpString(m["provider"]); p == provider {
				ofProvider = append(ofProvider, m)
			}
		}
		preferred := pickSearchModel(ofProvider)
		if preferred == nil {
			continue
		}
		resolved, err := registryAuthFor(reg, preferred)
		if err != nil || !resolved.OK || resolved.APIKey == "" {
			continue
		}
		// Auth can override the model's base URL. Do not guess Responses/web_search support from a
		// gateway URL, or pair its credential with the official API.
		var baseURL any
		baseSet := false
		if resolved.BaseURLSet {
			baseURL, baseSet = resolved.BaseURL, true
		} else if v, present := preferred["baseUrl"]; present {
			baseURL, baseSet = v, true
		}
		useCodex := provider == "openai-codex" || isCodexJWT(resolved.APIKey)
		if !hasExplicitResponsesURL && !useProviderBaseURL && !useCodex && baseSet {
			official := false
			if s, ok := mpString(baseURL); ok {
				if u, err := ParseURL(s, nil); err == nil && trimTrailingSlashes(u.String()) == "https://api.openai.com/v1" {
					official = true
				}
			}
			if !official {
				return nil, &customOpenAIBaseURLError{fmt.Sprintf("OpenAI web search cannot reuse Pi credentials with a custom baseUrl by default. Set openaiResponsesUrl in %s to the full Responses endpoint for this credential.", ConfigPath())}
			}
		}
		providerResponsesURL := responsesURL
		if useProviderBaseURL {
			u, err := resolveProviderResponsesURL(baseURL, useCodex)
			if err != nil {
				var custom *customOpenAIBaseURLError
				if !errors.As(err, &custom) {
					return nil, err
				}
				if invalidProviderURL == nil {
					invalidProviderURL = err
				}
				continue
			}
			providerResponsesURL = u
		}
		model, _ := mpString(preferred["id"])
		if modelOverride != "" {
			model = modelOverride
		}
		return &openAIAuth{
			provider:           provider,
			apiKey:             resolved.APIKey,
			model:              model,
			headers:            resolved.Headers,
			responsesURL:       providerResponsesURL,
			useProviderBaseURL: useProviderBaseURL,
		}, nil
	}
	if invalidProviderURL != nil {
		return nil, invalidProviderURL
	}
	return nil, nil
}

func openAICredentialOptions(cfg map[string]any) CredentialOptions {
	return CredentialOptions{Provider: "OpenAI", ConfiguredValue: cfg["openaiApiKey"], EnvironmentValue: envOrNil("OPENAI_API_KEY")}
}

// resolveOpenAIAuth is resolveOpenAIAuth: Pi credentials first, then the standalone key.
func resolveOpenAIAuth(ctx context.Context, ec ExtensionContext) (*openAIAuth, error) {
	cfg, err := providerConfig()
	if err != nil {
		return nil, err
	}
	responsesURL, err := resolveConfiguredResponsesURL(cfg)
	if err != nil {
		return nil, err
	}
	useProviderBaseURL, err := resolveUseProviderBaseURL(cfg)
	if err != nil {
		return nil, err
	}
	modelOverride, err := resolveConfiguredSearchModel(cfg)
	if err != nil {
		return nil, err
	}
	providers, err := resolveConfiguredSearchProviders(cfg)
	if err != nil {
		return nil, err
	}
	_, explicit := cfg["openaiResponsesUrl"]
	if ec != nil {
		auth, err := resolvePiAuth(ec, responsesURL, providers, modelOverride, explicit, useProviderBaseURL)
		if err != nil {
			return nil, err
		}
		if auth != nil {
			return auth, nil
		}
	}
	if useProviderBaseURL {
		return nil, &customOpenAIBaseURLError{"OpenAI search configuration: openaiUseProviderBaseUrl requires selected Pi provider credentials and a baseUrl; set openaiResponsesUrl for standalone API keys"}
	}
	if !HasCredentialSource(openAICredentialOptions(cfg)) {
		return nil, nil
	}
	apiKey, err := ResolveCredential(ctx, openAICredentialOptions(cfg))
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, nil
	}
	model := "gpt-5.6-terra"
	if modelOverride != "" {
		model = modelOverride
	}
	return &openAIAuth{provider: "openai", apiKey: apiKey, model: model, headers: providerHeaders{}, responsesURL: responsesURL}, nil
}

// IsOpenAISearchAvailable is isOpenAISearchAvailable. A configuration error is an error, not
// "unavailable" (the routing code lets it surface), except an invalid provider URL, which only
// makes OpenAI unavailable.
func IsOpenAISearchAvailable(ec ExtensionContext) (bool, error) {
	cfg, err := providerConfig()
	if err != nil {
		return false, err
	}
	responsesURL, err := resolveConfiguredResponsesURL(cfg)
	if err != nil {
		return false, err
	}
	useProviderBaseURL, err := resolveUseProviderBaseURL(cfg)
	if err != nil {
		return false, err
	}
	providers, err := resolveConfiguredSearchProviders(cfg)
	if err != nil {
		return false, err
	}
	_, explicit := cfg["openaiResponsesUrl"]
	if ec != nil {
		auth, err := resolvePiAuth(ec, responsesURL, providers, "", explicit, useProviderBaseURL)
		if err != nil {
			var custom *customOpenAIBaseURLError
			if errors.As(err, &custom) {
				return false, nil
			}
			return false, err
		}
		if auth != nil {
			return true, nil
		}
	}
	if useProviderBaseURL {
		return false, nil
	}
	return HasCredentialSource(openAICredentialOptions(cfg)), nil
}

func resolveCurrentModelAuth(ctx context.Context, ec ExtensionContext) (*openAIAuth, error) {
	model := ec.Model()
	if model == nil || !IsCurrentModelHostedSearchEligible(ec) {
		return nil, errors.New("Current model is not eligible for official OpenAI Hosted web search")
	}
	target, err := resolveCurrentModelSearchTarget(model)
	if err != nil {
		return nil, err
	}
	resolved, err := registryAuthFor(ec.ModelRegistry(), model)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, abortError(err)
	}
	if !resolved.OK {
		return nil, fmt.Errorf("OpenAI current model authentication failed: %s", resolved.Error)
	}
	if resolved.APIKey == "" {
		return nil, errors.New("OpenAI current model authentication failed: API key unavailable")
	}
	id, _ := mpString(model["id"])
	useCodex := target.useCodexEndpoint
	return &openAIAuth{provider: "openai", apiKey: resolved.APIKey, model: id, headers: resolved.Headers, responsesURL: target.responsesURL, useCodexEndpoint: &useCodex}, nil
}

// ---- request bodies -----------------------------------------------------------------------

func buildInstructions(o SearchOptions, f domainFilters) string {
	lines := []string{
		"Search the web and return a concise answer grounded only in the web results.",
		"Include clickable source citations in the response text when possible.",
	}
	if o.RecencyFilter != "" {
		lines = append(lines, "Prefer sources from the "+recencyLabelOrUndefined(o.RecencyFilter)+".")
	}
	if n, ok := mpFinitePositive(o.NumResults); ok {
		lines = append(lines, fmt.Sprintf("Prefer around %d distinct sources.", mpCap(n)))
	}
	if len(f.allowed) > 0 {
		lines = append(lines, "Only use sources from: "+strings.Join(capDomains(f.allowed), ", ")+".")
	}
	if len(f.blocked) > 0 {
		lines = append(lines, "Do not use sources from: "+strings.Join(capDomains(f.blocked), ", ")+".")
	}
	return strings.Join(lines, " ")
}

// recencyLabelOrUndefined is `labels[recencyFilter]`: a value outside the four prints as the
// string "undefined", as the template literal of the original does.
func recencyLabelOrUndefined(v string) string {
	if l, ok := recencyLabels[v]; ok {
		return l
	}
	return "undefined"
}

func capDomains(d []string) []string {
	if len(d) > 100 {
		return d[:100]
	}
	return d
}

type openAIWebSearchFilters struct {
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

type openAIWebSearchTool struct {
	Type    string                  `json:"type"`
	Filters *openAIWebSearchFilters `json:"filters,omitempty"`
}

type openAIInputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAIInputMessage struct {
	Role    string            `json:"role"`
	Content []openAIInputText `json:"content"`
}

// openAIResponsesBody is the Responses request, in the original's key order.
type openAIResponsesBody struct {
	Model             string                `json:"model"`
	Instructions      string                `json:"instructions"`
	Input             []openAIInputMessage  `json:"input"`
	Tools             []openAIWebSearchTool `json:"tools"`
	Include           []string              `json:"include"`
	Store             bool                  `json:"store"`
	Stream            bool                  `json:"stream"`
	ToolChoice        string                `json:"tool_choice"`
	ParallelToolCalls bool                  `json:"parallel_tool_calls"`
}

func buildResponsesBody(query string, o SearchOptions, model string) openAIResponsesBody {
	tool := openAIWebSearchTool{Type: "web_search"}
	f := normalizeDomainFilters(o.DomainFilter)
	if len(f.allowed) > 0 || len(f.blocked) > 0 {
		tool.Filters = &openAIWebSearchFilters{AllowedDomains: capDomains(f.allowed), BlockedDomains: capDomains(f.blocked)}
	}
	return openAIResponsesBody{
		Model:        model,
		Instructions: buildInstructions(o, f),
		Input:        []openAIInputMessage{{Role: "user", Content: []openAIInputText{{Type: "input_text", Text: query}}}},
		Tools:        []openAIWebSearchTool{tool},
		Include:      []string{"web_search_call.action.sources"},
		Store:        false, Stream: true, ToolChoice: "required", ParallelToolCalls: true,
	}
}

type alphaSearchQuery struct {
	Q       string   `json:"q"`
	Recency *int     `json:"recency,omitempty"`
	Domains []string `json:"domains,omitempty"`
}

type alphaSearchBody struct {
	ID       string `json:"id"`
	Model    string `json:"model"`
	Commands struct {
		SearchQuery []alphaSearchQuery `json:"search_query"`
	} `json:"commands"`
}

var alphaRecencyDays = map[string]int{"day": 1, "week": 7, "month": 30, "year": 365}

func buildAlphaSearchBody(query string, o SearchOptions, model string) (alphaSearchBody, error) {
	f := normalizeDomainFilters(o.DomainFilter)
	if len(f.blocked) > 0 {
		return alphaSearchBody{}, &OpenAIAlphaSearchUnsupportedError{"OpenAI alpha/search: unsupported excluded domains; use another search provider or disable openaiUseAlphaSearch"}
	}
	q := alphaSearchQuery{Q: query}
	if days, ok := alphaRecencyDays[o.RecencyFilter]; ok {
		q.Recency = &days
	}
	if len(f.allowed) > 0 {
		q.Domains = capDomains(f.allowed)
	}
	body := alphaSearchBody{ID: uuidV4(), Model: model}
	body.Commands.SearchQuery = []alphaSearchQuery{q}
	return body, nil
}

// resolveAlphaSearchURL swaps the trailing /responses of the endpoint for /alpha/search.
func resolveAlphaSearchURL(responsesURL string) (string, error) {
	u, err := ParseURL(responsesURL, nil)
	if err != nil {
		return "", err
	}
	path := trimTrailingSlashes(escapedPathOf(u))
	if !strings.HasSuffix(path, "/responses") {
		return "", errors.New("OpenAI alpha/search configuration: endpoint path must end with /responses")
	}
	setEscapedPath(u, strings.TrimSuffix(path, "/responses")+"/alpha/search")
	return u.String(), nil
}

// ---- headers (openai-search.ts:250-262, 616-626) ------------------------------------------

// applyOpenCodeDestinationHeaders drops any OpenCode attribution the registry supplied and adds
// the session's own, but only when the destination is https://opencode.ai.
func applyOpenCodeDestinationHeaders(h *jsHeaders, requestURL string, ec ExtensionContext) {
	h.del("x-opencode-session")
	h.del("x-opencode-client")
	if ec == nil || !isOpenCodeHost(requestURL) {
		return
	}
	if id := ec.SessionID(); id != "" {
		h.set("x-opencode-session", id)
		h.set("x-opencode-client", "pi")
	}
}

func buildAlphaSearchHeaders(h *jsHeaders, apiKey string) *jsHeaders {
	for _, name := range []string{"OpenAI-Beta", "Session_ID", "Conversation_ID", "X-Codex-Beta-Features", "X-Codex-Turn-State", "x-openai-internal-codex-responses-lite"} {
		h.del(name)
	}
	h.set("Authorization", "Bearer "+apiKey)
	h.set("Content-Type", "application/json")
	h.set("Accept", "application/json")
	h.set("Originator", "codex_cli_rs")
	return h
}

// ---- responses ----------------------------------------------------------------------------

type parsedOpenAIResponse struct {
	output            []any
	webSearchCallSeen bool
}

func isWebSearchCall(item any) bool {
	m, ok := mpObject(item)
	return ok && m["type"] == "web_search_call"
}

func anyWebSearchCall(items []any) bool {
	for _, it := range items {
		if isWebSearchCall(it) {
			return true
		}
	}
	return false
}

// parseOpenAIResponse reads either a JSON body or a server-sent-event stream
// (openai-search.ts:437-482).
func parseOpenAIResponse(text string) (*parsedOpenAIResponse, error) {
	trimmed := jsTrim(text)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		parsed, err := unmarshalAny(trimmed)
		if err != nil {
			return nil, fmt.Errorf("OpenAI API returned invalid JSON: %s", jsonErrorText(err))
		}
		var output []any
		if list, ok := mpArray(parsed); ok {
			output = list
		} else if m, ok := mpObject(parsed); ok {
			output, _ = mpArray(m["output"])
		}
		return &parsedOpenAIResponse{output: output, webSearchCallSeen: anyWebSearchCall(output)}, nil
	}

	var outputItems []any
	var completed map[string]any
	seen := false
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := jsTrim(line[6:])
		if data == "" || data == "[DONE]" {
			continue
		}
		parsed, err := unmarshalAny(data)
		if err != nil {
			continue
		}
		m, ok := mpObject(parsed)
		if !ok {
			continue
		}
		typ, _ := mpString(m["type"])
		if strings.HasPrefix(typ, "response.web_search_call") {
			seen = true
		}
		if typ == "response.output_item.done" && m["item"] != nil {
			outputItems = append(outputItems, m["item"])
			seen = seen || isWebSearchCall(m["item"])
		}
		if typ == "response.done" || typ == "response.completed" {
			if r, ok := mpObject(m["response"]); ok {
				completed = r
			}
		}
	}
	if completed != nil {
		output, _ := mpArray(completed["output"])
		if len(output) == 0 {
			output = outputItems
		}
		return &parsedOpenAIResponse{output: output, webSearchCallSeen: seen || anyWebSearchCall(output)}, nil
	}
	if len(outputItems) > 0 {
		return &parsedOpenAIResponse{output: outputItems, webSearchCallSeen: seen || anyWebSearchCall(outputItems)}, nil
	}
	return nil, errors.New("OpenAI API returned no parseable response output")
}

// cleanSourceURL drops the utm_source=openai tracking parameter.
func cleanSourceURL(raw string) string {
	u, err := ParseURL(raw, nil)
	if err != nil {
		return trimUTMSourceSuffix(raw)
	}
	pairs := parseFormQuery(u.RawQuery)
	value, found := "", false
	for _, p := range pairs {
		if p.name == "utm_source" {
			value, found = p.value, true
			break
		}
	}
	if !found || value != "openai" {
		return u.String()
	}
	kept := pairs[:0:0]
	for _, p := range pairs {
		if p.name != "utm_source" {
			kept = append(kept, p)
		}
	}
	u.RawQuery = serializeFormQuery(kept)
	return u.String()
}

var utmSourceSuffixRE = regexp.MustCompile(`[?&]utm_source=openai$`)

func trimUTMSourceSuffix(raw string) string { return utmSourceSuffixRE.ReplaceAllString(raw, "") }

type formPair struct{ name, value string }

// parseFormQuery is the application/x-www-form-urlencoded parser behind URLSearchParams.
func parseFormQuery(q string) []formPair {
	var out []formPair
	for _, part := range strings.Split(q, "&") {
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		out = append(out, formPair{formDecode(name), formDecode(value)})
	}
	return out
}

func formDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if d, err := url.PathUnescape(s); err == nil {
		return d
	}
	return s
}

func serializeFormQuery(pairs []formPair) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = formEscape(p.name) + "=" + formEscape(p.value)
	}
	return strings.Join(parts, "&")
}

var markdownLinkRE = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// extractSnippetAround cuts 100 UTF-16 units either side of a citation and flattens links.
func extractSnippetAround(text string, start, end any) string {
	s, ok1 := start.(float64)
	e, ok2 := end.(float64)
	if !ok1 || !ok2 || text == "" {
		return ""
	}
	before := int(math.Max(0, math.Trunc(s)-100))
	after := int(math.Min(float64(jsLen(text)), math.Trunc(e)+100))
	window := jsSlice(text, before, after)
	if strings.Contains(window, "](") { // a markdown link needs "](": nothing to flatten without it
		window = markdownLinkRE.ReplaceAllString(window, "$1")
	}
	snippet := jsTrim(window)
	if jsLen(snippet) > 300 {
		return jsSlice(snippet, 0, 297) + "..."
	}
	return snippet
}

func addOpenAIResult(results *[]SearchResult, seen map[string]bool, rawURL, title any, snippet string) {
	u, ok := mpString(rawURL)
	if !ok || jsTrim(u) == "" {
		return
	}
	clean := cleanSourceURL(u)
	if seen[clean] {
		return
	}
	seen[clean] = true
	t := clean
	if s, ok := mpString(title); ok && jsTrim(s) != "" {
		t = s
	}
	*results = append(*results, SearchResult{Title: t, URL: clean, Snippet: snippet})
}

func firstDefined(a, b any) any {
	if a != nil {
		return a
	}
	return b
}

func limitResults(results []SearchResult, numResults *float64) []SearchResult {
	if n, ok := mpFinitePositive(numResults); ok {
		c := mpCap(n)
		if c < len(results) {
			return results[:c]
		}
	}
	return results
}

func extractOpenAISearchResults(output []any, numResults *float64) []SearchResult {
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
				addOpenAIResult(&results, seen, ann["url"], ann["title"], extractSnippetAround(text, ann["start_index"], ann["end_index"]))
			}
		}
	}
	for _, item := range output {
		m, ok := mpObject(item)
		if !ok || m["type"] != "web_search_call" {
			continue
		}
		var actionSources any
		if action, ok := mpObject(m["action"]); ok {
			actionSources = action["sources"]
		}
		for _, group := range []any{actionSources, m["sources"], m["results"]} {
			list, ok := mpArray(group)
			if !ok {
				continue
			}
			for _, s := range list {
				source, ok := mpObject(s)
				if !ok {
					continue
				}
				addOpenAIResult(&results, seen, firstDefined(source["url"], source["source_website_url"]), firstDefined(source["title"], source["caption"]), "")
			}
		}
	}
	return limitResults(results, numResults)
}

func extractOpenAIAnswer(output []any) string {
	var parts []string
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
			if text, ok := mpString(part["text"]); ok && jsTrim(text) != "" {
				parts = append(parts, text)
			}
		}
	}
	return jsTrim(strings.Join(parts, "\n"))
}

// parseAlphaSearchResponse reads the alpha/search JSON (openai-search.ts:628-662); numResults
// defaults to 5 when unset.
func parseAlphaSearchResponse(text string, numResults *float64) (*SearchResponse, error) {
	payload, err := unmarshalAny(text)
	if err != nil {
		return nil, errors.New("OpenAI alpha/search returned invalid JSON")
	}
	record, ok := mpObject(payload)
	if !ok {
		return nil, errors.New("OpenAI alpha/search returned an invalid response")
	}
	answer := ""
	if s, ok := mpString(record["output"]); ok {
		answer = jsTrim(s)
	}
	results := []SearchResult{}
	seen := map[string]bool{}
	items, _ := mpArray(record["results"])
	for _, it := range items {
		item, ok := mpObject(it)
		if !ok || item["type"] != "text_result" {
			continue
		}
		rawURL, ok := mpString(item["url"])
		if !ok {
			continue
		}
		if u, err := ParseURL(rawURL, nil); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		snippet, _ := mpString(item["snippet"])
		addOpenAIResult(&results, seen, rawURL, item["title"], snippet)
	}
	if answer == "" && len(results) == 0 {
		return nil, errors.New("OpenAI alpha/search returned no parseable results or plaintext output")
	}
	limit := numResults
	if limit == nil {
		five := 5.0
		limit = &five
	}
	return &SearchResponse{Answer: answer, Results: limitResults(results, limit)}, nil
}

// ---- the request --------------------------------------------------------------------------

func runOpenAISearch(ctx context.Context, query string, o SearchOptions, auth *openAIAuth, ec ExtensionContext) (*SearchResponse, error) {
	useAlpha, err := isAlphaSearchEnabled()
	if err != nil {
		return nil, err
	}
	if useAlpha {
		if err := ctx.Err(); err != nil {
			return nil, abortError(err)
		}
	}
	useCodex := auth.provider == "openai-codex" || isCodexJWT(auth.apiKey)
	if auth.useCodexEndpoint != nil {
		useCodex = *auth.useCodexEndpoint
	}
	responsesURL := auth.responsesURL
	if useCodex && !auth.useProviderBaseURL {
		responsesURL = codexResponsesURL
	}
	requestURL := responsesURL
	if useAlpha {
		if requestURL, err = resolveAlphaSearchURL(responsesURL); err != nil {
			return nil, err
		}
	}
	var body any
	if useAlpha {
		b, err := buildAlphaSearchBody(query, o, auth.model)
		if err != nil {
			return nil, err
		}
		body = b
	} else {
		body = buildResponsesBody(query, o, auth.model)
	}
	payload, err := encodeJSONBody(body)
	if err != nil {
		return nil, err
	}

	headers := requestHeaderObject(auth.headers)
	headers.set("Authorization", "Bearer "+auth.apiKey)
	headers.set("Content-Type", "application/json")
	headers.set("OpenAI-Beta", "responses=experimental")
	if useCodex {
		if id := extractAccountID(auth.apiKey); id != "" {
			headers.set("chatgpt-account-id", id)
		}
		headers.set("originator", "pi")
	}
	requestHeaders := headersFromObject(headers)
	if useAlpha {
		requestHeaders = buildAlphaSearchHeaders(requestHeaders, auth.apiKey)
	}
	applyOpenCodeDestinationHeaders(requestHeaders, requestURL, ec)

	credentialHeaders := []string{"Authorization", "x-opencode-session", "x-opencode-client", "chatgpt-account-id"}
	credentialHeaders = append(credentialHeaders, auth.headers.sortedNames()...)

	tctx, cancel := withTimeout(ctx, openAISearchTimeout)
	defer cancel()
	result, err := func() (*SearchResponse, error) {
		resp, err := FetchWithCredentialRedirects(tctx, requestURL, RequestInit{Method: "POST", Header: requestHeaders.http(), Body: payload}, credentialHeaders)
		if err != nil {
			return nil, timeoutFix(ctx, tctx, err)
		}
		text, err := readBody(resp)
		if err != nil {
			return nil, timeoutFix(ctx, tctx, err)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			message := fmt.Sprintf("OpenAI %sAPI error %d: %s", map[bool]string{true: "alpha/search ", false: ""}[useAlpha], resp.StatusCode, slice300(RedactCredential(text, auth.apiKey)))
			if useAlpha && (resp.StatusCode == 404 || resp.StatusCode == 405 || resp.StatusCode == 501) {
				return nil, &OpenAIAlphaSearchUnsupportedError{message}
			}
			return nil, errors.New(message)
		}
		if useAlpha {
			return parseAlphaSearchResponse(text, o.NumResults)
		}
		parsed, err := parseOpenAIResponse(text)
		if err != nil {
			return nil, err
		}
		if !parsed.webSearchCallSeen {
			return nil, errors.New("OpenAI web_search returned no web_search_call")
		}
		answer := extractOpenAIAnswer(parsed.output)
		results := extractOpenAISearchResults(parsed.output, o.NumResults)
		if answer == "" && len(results) == 0 {
			return nil, errors.New("OpenAI web_search returned no answer or sources")
		}
		return &SearchResponse{Answer: answer, Results: results}, nil
	}()
	if err != nil {
		return nil, redactErr(err, auth.apiKey)
	}
	return result, nil
}

// SearchWithOpenAI is searchWithOpenAI: hosted search through a Pi login or an API key. The
// session's ExtensionContext (WithExtensionContext) supplies the model registry.
func SearchWithOpenAI(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	ec := ExtensionContextFrom(ctx)
	auth, err := resolveOpenAIAuth(ctx, ec)
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, fmt.Errorf("OpenAI web search unavailable. Either:\n  1. Use /login to sign in with a Codex subscription\n  2. Create %s with { \"openaiApiKey\": \"your-key\" }\n  3. Set OPENAI_API_KEY environment variable", ConfigPath())
	}
	return runOpenAISearch(ctx, query, o, auth, ec)
}

// SearchWithCurrentModelOpenAI is searchWithCurrentModelOpenAI: hosted search with the selected
// model, only when it is backed by an official OpenAI endpoint.
func SearchWithCurrentModelOpenAI(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	ec := ExtensionContextFrom(ctx)
	if ec == nil {
		return nil, errors.New("OpenAI current-model search requires an extension context")
	}
	auth, err := resolveCurrentModelAuth(ctx, ec)
	if err != nil {
		return nil, err
	}
	return runOpenAISearch(ctx, query, o, auth, ec)
}

type currentModelSearchKey struct{}

// withCurrentModelSearch marks a call as `searchRouting.useCurrentModel` for the openai provider.
func withCurrentModelSearch(ctx context.Context) context.Context {
	return context.WithValue(ctx, currentModelSearchKey{}, true)
}

func currentModelSearchRequested(ctx context.Context) bool {
	v, _ := ctx.Value(currentModelSearchKey{}).(bool)
	return v
}

// searchOpenAIProvider is the openai entry of the provider table.
func searchOpenAIProvider(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	if currentModelSearchRequested(ctx) {
		return SearchWithCurrentModelOpenAI(ctx, query, o)
	}
	return SearchWithOpenAI(ctx, query, o)
}

// openAIAvailable is the availability of the openai entry: current-model eligibility when the
// routing asks for the selected model, otherwise a usable login or key.
func openAIAvailable(ctx context.Context, _ FullSearchOptions) (bool, error) {
	ec := ExtensionContextFrom(ctx)
	if currentModelSearchRequested(ctx) {
		return IsCurrentModelHostedSearchEligible(ec), nil
	}
	return IsOpenAISearchAvailable(ec)
}

// openAICodexSelected is isOpenAICodexSelected.
func openAICodexSelected(ec ExtensionContext) bool {
	if ec == nil {
		return false
	}
	model := ec.Model()
	provider, _ := mpString(model["provider"])
	return provider == "openai-codex"
}
