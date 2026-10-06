package websearch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Port of the routing half of gemini-search.ts: provider selection, the configured routing, the
// auto order, "all" and provider arrays, and the error classification that drives fallback.
//
// Providers that are not ported yet stay in the table so that selection, availability and
// configuration behave like the original; searching with one fails with a clear "unsupported"
// error (a named gap in PORT.md).

// ResolvedProviders is RESOLVED_SEARCH_PROVIDERS, in the original's order.
var ResolvedProviders = []string{"openai", "brave", "parallel", "parallel-mcp", "tinyfish", "search1api", "searchinfinity", "querit", "tavily", "you", "firecrawl", "jina", "searxng", "duckduckgo", "perplexity", "gemini", "kimi", "exa", "serpdive", "kagi", "ollama", "anysearch", "xai", "mistral", "brightdata", "serpbase", "serpapi", "serper", "serply", "valyu", "bocha", "xcrawl", "baizhi"}

// AllSearchProviders is ALL_SEARCH_PROVIDERS: what provider "all" fans out to. Explicit-only
// providers (opt-in or paid) are deliberately absent.
var AllSearchProviders = []string{"searxng", "openai", "exa", "brave", "parallel", "tinyfish", "search1api", "searchinfinity", "querit", "tavily", "firecrawl", "jina", "serpdive", "kagi", "ollama", "perplexity", "gemini", "bocha"}

// ProviderSelection is a provider name ("auto", "all", or one provider) or a list of providers.
// The zero value means "not specified".
type ProviderSelection struct {
	Name string
	List []string
}

// Auto selects the configured default, else the automatic order.
var Auto = ProviderSelection{Name: "auto"}

// IsList reports whether the selection is a provider array.
func (s ProviderSelection) IsList() bool { return s.List != nil }

// FullSearchOptions are SearchOptions plus the provider selection.
type FullSearchOptions struct {
	SearchOptions
	Provider ProviderSelection
}

// ProviderSearchResponse is one provider's answer within an aggregate response.
type ProviderSearchResponse struct {
	SearchResponse
	Provider string `json:"provider"`
}

// ProviderSearchFailure is one provider's failure within an aggregate response.
type ProviderSearchFailure struct {
	Provider string `json:"provider"`
	Error    string `json:"error"`
}

// AttributedSearchResponse names the provider that answered ("all" for aggregates).
type AttributedSearchResponse struct {
	SearchResponse
	Provider          string                   `json:"provider"`
	ProviderResponses []ProviderSearchResponse `json:"providerResponses,omitempty"`
	ProviderErrors    []ProviderSearchFailure  `json:"providerErrors,omitempty"`
}

// SearchProviderError is a classified provider failure.
type SearchProviderError struct {
	Provider string
	Kind     string
	Status   int
	Message  string
	Cause    error
}

func (e *SearchProviderError) Error() string {
	return fmt.Sprintf("%s search failed (%s): %s", e.Provider, e.Kind, e.Message)
}
func (e *SearchProviderError) Unwrap() error { return e.Cause }

// SearchRouting is the configured ordered routing.
type SearchRouting struct {
	Providers       []string
	UseCurrentModel *bool
	FallbackOn      []string
}

var validRoutingKinds = []string{"transient", "quota", "network", "invalid-response", "unsupported"}

type providerDef struct {
	name, label string
	// available approximates the original's availability check for providers that are not ported
	// yet (a credential source of the original's names is configured).
	available func() bool
	search    func(ctx context.Context, q string, o SearchOptions) (*SearchResponse, error)
}

func credAvail(provider, cfgKey, env string) func() bool {
	return func() bool { return hasProviderCredential(provider, cfgKey, env) }
}

var providerTable = map[string]*providerDef{}

func init() {
	def := func(name, label string, avail func() bool, search func(context.Context, string, SearchOptions) (*SearchResponse, error)) {
		providerTable[name] = &providerDef{name: name, label: label, available: avail, search: search}
	}
	def("brave", "Brave", IsBraveAvailable, SearchWithBrave)
	def("tavily", "Tavily", IsTavilyAvailable, SearchWithTavily)
	def("perplexity", "Perplexity", IsPerplexityAvailable, SearchWithPerplexity)
	def("exa", "Exa", IsExaAvailable, func(ctx context.Context, q string, o SearchOptions) (*SearchResponse, error) {
		r, err := SearchWithExa(ctx, q, o)
		if err == nil && r == nil {
			return nil, errors.New("Exa search returned no results.")
		}
		return r, err
	})
	def("duckduckgo", "DuckDuckGo", IsDuckDuckGoAvailable, SearchWithDuckDuckGo)
	def("serpdive", "SERPdive", IsSerpdiveAvailable, SearchWithSerpdive)
	def("kagi", "Kagi", IsKagiAvailable, SearchWithKagi)
	// Not ported yet: label and credential names as in the original.
	for _, p := range []struct{ name, label, cfg, env string }{
		{"openai", "OpenAI", "openaiApiKey", "OPENAI_API_KEY"},
		{"parallel", "Parallel", "parallelApiKey", "PARALLEL_API_KEY"},
		{"parallel-mcp", "Parallel MCP", "", ""},
		{"tinyfish", "TinyFish", "tinyfishApiKey", "TINYFISH_API_KEY"},
		{"search1api", "Search1API", "search1apiApiKey", "SEARCH1API_KEY"},
		{"searchinfinity", "Searchinfinity", "searchinfinityApiKey", "SEARCHINFINITY_API_KEY"},
		{"querit", "Querit", "queritApiKey", "QUERIT_API_KEY"},
		{"you", "You.com", "youApiKey", "YOU_API_KEY"},
		{"firecrawl", "Firecrawl", "firecrawlApiKey", "FIRECRAWL_API_KEY"},
		{"jina", "Jina", "jinaApiKey", "JINA_API_KEY"},
		{"searxng", "SearXNG", "searxngBaseUrl", "SEARXNG_BASE_URL"},
		{"gemini", "Gemini", "geminiApiKey", "GEMINI_API_KEY"},
		{"kimi", "Kimi", "kimiApiKey", "KIMI_API_KEY"},
		{"ollama", "Ollama", "ollamaApiKey", "OLLAMA_API_KEY"},
		{"anysearch", "Anysearch", "", ""},
		{"xai", "xAI", "xaiApiKey", "XAI_API_KEY"},
		{"mistral", "Mistral", "mistralApiKey", "MISTRAL_API_KEY"},
		{"brightdata", "Bright Data", "brightdataApiKey", "BRIGHTDATA_API_KEY"},
		{"serpbase", "SerpBase", "serpbaseApiKey", "SERPBASE_API_KEY"},
		{"serpapi", "SerpApi", "serpapiApiKey", "SERPAPI_API_KEY"},
		{"serper", "Serper", "serperApiKey", "SERPER_API_KEY"},
		{"serply", "Serply", "serplyApiKey", "SERPLY_API_KEY"},
		{"valyu", "Valyu", "valyuApiKey", "VALYU_API_KEY"},
		{"bocha", "Bocha", "bochaApiKey", "BOCHA_API_KEY"},
		{"xcrawl", "XCrawl", "", ""},
		{"baizhi", "Baizhi", "baizhiApiKey", "BAIZHI_API_KEY"},
	} {
		avail := func() bool { return false }
		if p.cfg != "" {
			avail = credAvail(p.label, p.cfg, p.env)
		}
		def(p.name, p.label, avail, nil)
	}
}

// ProviderLabel is providerLabel().
func ProviderLabel(provider string) string {
	if d := providerTable[provider]; d != nil {
		return d.label
	}
	if provider == "" {
		return provider
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}

func isResolvedProvider(name string) bool { return sliceHas(ResolvedProviders, name) }

func normalizeResolvedProviderList(value []any, label string) ([]string, error) {
	if len(value) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty array", label)
	}
	var out []string
	for _, p := range value {
		s, _ := p.(string)
		n := strings.ToLower(jsTrim(s))
		if !isResolvedProvider(n) {
			return nil, fmt.Errorf("%s contains an invalid provider: %v", label, jsString(p))
		}
		if sliceHas(out, n) {
			return nil, fmt.Errorf("%s must not contain duplicates: %s", label, n)
		}
		out = append(out, n)
	}
	return out, nil
}

func jsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	default:
		return fmt.Sprint(x)
	}
}

// NormalizeSearchProviderSelection accepts an array of provider names or one of "auto", "all" or a
// provider name (anything else is "auto").
func NormalizeSearchProviderSelection(value any, label string) (ProviderSelection, error) {
	if list, ok := value.([]any); ok {
		names, err := normalizeResolvedProviderList(list, label)
		return ProviderSelection{List: names}, err
	}
	if list, ok := value.([]string); ok {
		anyList := make([]any, len(list))
		for i, s := range list {
			anyList[i] = s
		}
		names, err := normalizeResolvedProviderList(anyList, label)
		return ProviderSelection{List: names}, err
	}
	s, _ := value.(string)
	n := strings.ToLower(jsTrim(s))
	if n == "auto" || n == "all" || isResolvedProvider(n) {
		return ProviderSelection{Name: n}, nil
	}
	return Auto, nil
}

type searchConfig struct {
	provider    ProviderSelection
	configured  bool
	routing     *SearchRouting
	searchModel string
	allowed     []string
}

func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func loadSearchConfig() (*searchConfig, error) {
	root, err := ReadConfigRoot()
	if err != nil {
		return nil, err
	}
	cfg := &searchConfig{provider: Auto}
	if root == nil {
		return cfg, nil
	}
	path := ConfigPath()
	if m, ok := root["searchModel"].(string); ok {
		cfg.searchModel = jsTrim(m)
	}
	if ws, present := root["webSearch"]; present {
		obj, ok := ws.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("webSearch in %s must be an object", path)
		}
		if hasKey(obj, "allowedProviders") {
			list, _ := obj["allowedProviders"].([]any)
			cfg.allowed, err = normalizeResolvedProviderList(list, "webSearch.allowedProviders in "+path)
			if err != nil {
				return nil, err
			}
		}
	}
	cfg.configured = hasKey(root, "searchProvider") || hasKey(root, "provider")
	raw := root["searchProvider"]
	if raw == nil {
		raw = root["provider"]
	}
	cfg.provider, err = NormalizeSearchProviderSelection(raw, "provider in "+path)
	if err != nil {
		return nil, err
	}
	if hasKey(root, "searchRouting") {
		cfg.routing, err = normalizeSearchRouting(root["searchRouting"], path)
		if err != nil {
			return nil, err
		}
	}
	if cfg.allowed != nil {
		for _, key := range []string{"searchProvider", "provider"} {
			if hasKey(root, key) {
				sel, _ := NormalizeSearchProviderSelection(root[key], "provider")
				if err := AssertProviderSelectionAllowed(sel, key+" in "+path, cfg.allowed); err != nil {
					return nil, err
				}
			}
		}
		if cfg.routing != nil {
			if err := AssertProviderSelectionAllowed(ProviderSelection{List: cfg.routing.Providers}, "searchRouting.providers in "+path, cfg.allowed); err != nil {
				return nil, err
			}
		}
	}
	return cfg, nil
}

func normalizeSearchRouting(value any, path string) (*SearchRouting, error) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("searchRouting in %s must be an object", path)
	}
	list, _ := raw["providers"].([]any)
	providers, err := normalizeResolvedProviderList(list, "searchRouting.providers in "+path)
	if err != nil {
		return nil, err
	}
	r := &SearchRouting{Providers: providers}
	if v, present := raw["useCurrentModel"]; present {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("searchRouting.useCurrentModel in %s must be a boolean", path)
		}
		r.UseCurrentModel = &b
	}
	kinds, ok := raw["fallbackOn"].([]any)
	if !ok || len(kinds) == 0 {
		return nil, fmt.Errorf("searchRouting.fallbackOn in %s must be a non-empty array", path)
	}
	for _, k := range kinds {
		s, ok := k.(string)
		if !ok || !sliceHas(validRoutingKinds, s) {
			return nil, fmt.Errorf("searchRouting.fallbackOn in %s may only contain transient, quota, network, invalid-response, or unsupported", path)
		}
		if !sliceHas(r.FallbackOn, s) {
			r.FallbackOn = append(r.FallbackOn, s)
		}
	}
	return r, nil
}

// AssertProviderSelectionAllowed rejects a selection that names a provider outside
// webSearch.allowedProviders. A nil allowed list allows everything.
func AssertProviderSelectionAllowed(sel ProviderSelection, label string, allowed []string) error {
	if allowed == nil {
		return nil
	}
	var requested []string
	if sel.IsList() {
		requested = sel.List
	} else if sel.Name != "auto" && sel.Name != "all" && sel.Name != "" {
		requested = []string{sel.Name}
	}
	var disabled []string
	for _, p := range requested {
		if !sliceHas(allowed, p) {
			disabled = append(disabled, p)
		}
	}
	if len(disabled) == 0 {
		return nil
	}
	what := fmt.Sprintf(`references disabled provider "%s"`, disabled[0])
	if len(disabled) > 1 {
		what = "references disabled providers: " + strings.Join(disabled, ", ")
	}
	return fmt.Errorf("%s %s; allowed by webSearch.allowedProviders: %s", label, what, strings.Join(allowed, ", "))
}

// AllowedSearchProviders is the allowlist, or every provider.
func AllowedSearchProviders() ([]string, error) {
	cfg, err := loadSearchConfig()
	if err != nil {
		return nil, err
	}
	if cfg.allowed != nil {
		return cfg.allowed, nil
	}
	return ResolvedProviders, nil
}

func errNotPorted(provider string) *SearchProviderError {
	label := ProviderLabel(provider)
	return &SearchProviderError{Provider: provider, Kind: "unsupported", Message: label + " search is not available in this Go port yet (planned for a later slice)"}
}

var (
	statusRE            = regexp.MustCompile(`(?i)\b(?:error|status|http)\s+(\d{3})\b`)
	unsupportedSearchRE = regexp.MustCompile(`(?i)(?:web[_ -]?search|web[_ -]?search_preview|(?:the )?tool)\b.*\b(?:unsupported|not supported|does not support|doesn't support|unknown|unrecognized|unavailable|not found)|\b(?:unsupported|not supported|does not support|doesn't support|unknown|unrecognized|unavailable|not found)\b.*\b(?:web[_ -]?search|web[_ -]?search_preview|(?:the )?tool)`)
	credentialRE        = regexp.MustCompile(`(?:api )?key (?:not found|missing)|credential resolution`)
	xaiQuotaRE          = regexp.MustCompile(`spending[- ]limit|(?:no|out of) credits?|insufficient quota|quota (?:exceeded|exhausted)|credits? (?:exhausted|depleted|used up)`)
	rateLimitRE         = regexp.MustCompile(`rate limit|quota|too many requests`)
	authTextRE          = regexp.MustCompile(`unauthorized|forbidden|permission denied`)
	badRequestRE        = regexp.MustCompile(`bad request|invalid request`)
	invalidResponseRE   = regexp.MustCompile(`invalid json|no parseable response|no parseable results|invalid response|returned empty response|no web_search_call`)
	transientTextRE     = regexp.MustCompile(`temporar|service unavailable|server error`)
	networkTextRE       = regexp.MustCompile(`fetch failed|network|econnreset|econnrefused|enotfound|etimedout|timed out|socket`)
	configTextRE        = regexp.MustCompile(`invalid or missing|invalid config|failed to parse|must be an? |configuration`)
)

// ClassifyProviderError maps a provider failure to a SearchProviderError kind, which decides
// whether configured routing falls through to the next provider.
func ClassifyProviderError(provider string, err error) *SearchProviderError {
	var already *SearchProviderError
	if errors.As(err, &already) {
		return already
	}
	message := err.Error()
	lower := strings.ToLower(message)
	status := 0
	if m := statusRE.FindStringSubmatch(message); m != nil {
		status, _ = strconv.Atoi(m[1])
	}
	kind := "unknown"
	var credErr *CredentialResolutionError
	var netErr interface{ Timeout() bool }
	switch {
	case errors.As(err, &credErr) || credentialRE.MatchString(lower):
		kind = "credential"
	case isAbortError(err):
		kind = "aborted"
	case provider == "xai" && status == 403 && xaiQuotaRE.MatchString(lower):
		kind = "quota"
	case status == 401 || status == 403:
		kind = "auth"
	case provider == "openai" && (status == 400 || status == 422) && unsupportedSearchRE.MatchString(lower):
		kind = "unsupported"
	case status == 400 || status == 422:
		kind = "invalid-request"
	case status == 402 || status == 429 || (provider == "tavily" && status == 432):
		kind = "quota"
	case status == 408 || status == 425 || status >= 500 && status != 0:
		kind = "transient"
	case rateLimitRE.MatchString(lower):
		kind = "quota"
	case authTextRE.MatchString(lower):
		kind = "auth"
	case badRequestRE.MatchString(lower):
		kind = "invalid-request"
	case invalidResponseRE.MatchString(lower):
		kind = "invalid-response"
	case transientTextRE.MatchString(lower):
		kind = "transient"
	case errors.As(err, &netErr) || networkTextRE.MatchString(lower):
		kind = "network"
	case configTextRE.MatchString(lower):
		kind = "config"
	}
	return &SearchProviderError{Provider: provider, Kind: kind, Status: status, Message: message, Cause: err}
}

func isProviderAvailable(provider string) bool {
	d := providerTable[provider]
	return d != nil && d.available()
}

func searchWithResolvedProvider(ctx context.Context, provider, query string, o FullSearchOptions) (*ProviderSearchResponse, error) {
	d := providerTable[provider]
	if d == nil || d.search == nil {
		return nil, errNotPorted(provider)
	}
	r, err := d.search(ctx, query, o.SearchOptions)
	if err != nil {
		return nil, err
	}
	return &ProviderSearchResponse{SearchResponse: *r, Provider: provider}, nil
}

func searchWithProviders(ctx context.Context, query string, o FullSearchOptions, selected []string) (*AttributedSearchResponse, error) {
	allowed, err := AllowedSearchProviders()
	if err != nil {
		return nil, err
	}
	providers := selected
	if providers == nil {
		for _, p := range AllSearchProviders {
			if sliceHas(allowed, p) && isProviderAvailable(p) {
				providers = append(providers, p)
			}
		}
	}
	if len(providers) == 0 {
		return nil, errors.New(`No configured search provider available for provider "all". Parallel MCP, DuckDuckGo, Kimi, AnySearch, xAI, Mistral, Bright Data, SerpBase, SerpApi, Serper, Serply, You.com, Valyu, and XCrawl are excluded.`)
	}
	responses := make([]*ProviderSearchResponse, len(providers))
	errs := make([]error, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			defer recoverInto(ProviderLabel(p)+" search", func(err error) { responses[i], errs[i] = nil, err })
			responses[i], errs[i] = searchWithResolvedProvider(ctx, p, query, o)
		}(i, p)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, errors.New("Aborted")
	}
	var successes []ProviderSearchResponse
	var failures []ProviderSearchFailure
	for i := range providers {
		if errs[i] != nil {
			failures = append(failures, ProviderSearchFailure{providers[i], errs[i].Error()})
		} else {
			successes = append(successes, *responses[i])
		}
	}
	if len(successes) == 0 {
		label := "All-provider"
		if selected != nil {
			label = "Selected-provider"
		}
		lines := make([]string, len(failures))
		for i, f := range failures {
			lines[i] = ProviderLabel(f.Provider) + ": " + f.Error
		}
		return nil, fmt.Errorf("%s search failed:\n  - %s", label, strings.Join(lines, "\n  - "))
	}
	out := &AttributedSearchResponse{Provider: "all", ProviderResponses: successes, ProviderErrors: failures}
	out.Results = []SearchResult{}
	seenResults, seenInline := map[string]bool{}, map[string]bool{}
	for _, r := range successes {
		for _, x := range r.Results {
			if !seenResults[x.URL] {
				seenResults[x.URL] = true
				out.Results = append(out.Results, x)
			}
		}
		for _, c := range r.InlineContent {
			if !seenInline[c.URL] {
				seenInline[c.URL] = true
				out.InlineContent = append(out.InlineContent, c)
			}
		}
	}
	var sections []string
	for _, r := range successes {
		answer := r.Answer
		if answer == "" {
			answer = "(No answer text returned.)"
		}
		sections = append(sections, "## "+ProviderLabel(r.Provider)+"\n\n"+answer)
	}
	if len(failures) > 0 {
		lines := make([]string, len(failures))
		for i, f := range failures {
			lines[i] = "- **" + ProviderLabel(f.Provider) + ":** " + f.Error
		}
		sections = append(sections, "## Provider errors\n\n"+strings.Join(lines, "\n"))
	}
	out.Answer = strings.Join(sections, "\n\n")
	return out, nil
}

func searchWithConfiguredRouting(ctx context.Context, query string, o FullSearchOptions, routing *SearchRouting) (*AttributedSearchResponse, error) {
	var diagnostics []string
	for _, provider := range routing.Providers {
		if !isProviderAvailable(provider) {
			diagnostics = append(diagnostics, provider+": unavailable")
			continue
		}
		r, err := searchWithResolvedProvider(ctx, provider, query, o)
		if err == nil {
			return &AttributedSearchResponse{SearchResponse: r.SearchResponse, Provider: provider}, nil
		}
		classified := ClassifyProviderError(provider, err)
		diagnostics = append(diagnostics, fmt.Sprintf("%s [%s]: %s", provider, classified.Kind, err.Error()))
		if !sliceHas(routing.FallbackOn, classified.Kind) {
			return nil, classified
		}
	}
	return nil, fmt.Errorf("Configured search routing exhausted:\n  - %s", strings.Join(diagnostics, "\n  - "))
}

// Search runs one query: an explicit provider, a list, "all", the configured provider or routing,
// or the automatic order.
func Search(ctx context.Context, query string, o FullSearchOptions) (*AttributedSearchResponse, error) {
	// A caller that is already gone gets an abort before any configuration or network work.
	if err := ctx.Err(); err != nil {
		return nil, abortError(err)
	}
	cfg, err := loadSearchConfig()
	if err != nil {
		return nil, err
	}
	provider := o.Provider
	if provider.IsList() {
		// keep
	} else if provider.Name == "" || provider.Name == "auto" {
		provider = cfg.provider
	}
	if err := AssertProviderSelectionAllowed(provider, "Requested provider", cfg.allowed); err != nil {
		return nil, err
	}
	if provider.IsList() {
		anyList := make([]any, len(provider.List))
		for i, s := range provider.List {
			anyList[i] = s
		}
		names, err := normalizeResolvedProviderList(anyList, "provider")
		if err != nil {
			return nil, err
		}
		return searchWithProviders(ctx, query, o, names)
	}
	if provider.Name == "all" {
		return searchWithProviders(ctx, query, o, nil)
	}
	if provider.Name != "auto" {
		r, err := searchWithResolvedProvider(ctx, provider.Name, query, o)
		if err != nil {
			return nil, err
		}
		return &AttributedSearchResponse{SearchResponse: r.SearchResponse, Provider: provider.Name}, nil
	}
	if !cfg.configured && cfg.routing != nil {
		return searchWithConfiguredRouting(ctx, query, o, cfg.routing)
	}
	return searchAuto(ctx, query, o, cfg)
}

// autoOrder is the automatic provider order of the original.
var autoOrder = []string{"searxng", "openai", "exa", "brave", "parallel", "tinyfish", "search1api", "searchinfinity", "querit", "tavily", "firecrawl", "jina", "serpdive", "kagi", "bocha", "ollama", "perplexity", "gemini"}

func searchAuto(ctx context.Context, query string, o FullSearchOptions, cfg *searchConfig) (*AttributedSearchResponse, error) {
	var fallbackErrors []string
	allowed := cfg.allowed
	if allowed == nil {
		allowed = ResolvedProviders
	}
	for _, p := range autoOrder {
		if !sliceHas(allowed, p) || !isProviderAvailable(p) {
			continue
		}
		r, err := searchWithResolvedProvider(ctx, p, query, o)
		if err != nil {
			var credErr *CredentialResolutionError
			if errors.As(err, &credErr) && p == "exa" || isAbortError(err) {
				return nil, err
			}
			fallbackErrors = append(fallbackErrors, ProviderLabel(p)+": "+err.Error())
			continue
		}
		return &AttributedSearchResponse{SearchResponse: r.SearchResponse, Provider: p}, nil
	}
	if len(fallbackErrors) > 0 {
		return nil, fmt.Errorf("Auto provider search failed:\n  - %s", strings.Join(fallbackErrors, "\n  - "))
	}
	// The original's list also names OpenAI/Codex, Gemini (API, Cloudflare gateway and Gemini Web
	// browser cookies) and 20 more providers; none is ported, so only the ported ones are offered
	// (docs/PORT.md, deliberate differences).
	return nil, errors.New("No search provider available. Either:\n" +
		"  1. Set braveApiKey, tavilyApiKey, serpdiveApiKey, kagiApiKey, perplexityApiKey, or exaApiKey in " + ConfigPath() + "\n" +
		"  2. Set BRAVE_API_KEY, TAVILY_API_KEY, SERPDIVE_API_KEY, KAGI_API_KEY, PERPLEXITY_API_KEY, or EXA_API_KEY env vars\n" +
		"  3. Allow \"exa\" in webSearch.allowedProviders to use the keyless Exa MCP (queries go to mcp.exa.ai)\n" +
		"  4. Explicitly select provider: \"duckduckgo\" for keyless DuckDuckGo search\n" +
		"Other pi-web-access providers are not ported to this Go extension yet.")
}
