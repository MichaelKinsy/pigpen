// SPDX-License-Identifier: MIT

package rpiv_web_tools

import "strings"

// The provider contract. upstream: providers/types.ts.
//
// The role split is the shape of the API: a search provider exposes search(), a fetch provider exposes fetch(), and a
// full provider is the intersection — for vendors whose hosted API has a native fetch endpoint worth using directly
// (Tavily, Exa, Jina, Firecrawl, Ollama). Capability is checked structurally at dispatch time, so a provider that
// grows a fetch arm needs no orchestrator change.

// searchResult is one hit. upstream: providers/types.ts SearchResult.
type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

// searchResponse is the search tool's provider answer. upstream: providers/types.ts SearchResponse.
type searchResponse struct {
	Query   string
	Results []searchResult
}

// fetchResponse is the fetch tool's provider answer. upstream: providers/types.ts FetchResponse.
type fetchResponse struct {
	Text           string
	Title          string
	HasTitle       bool
	ContentType    string
	HasContentType bool
	ContentLength  *float64
}

// providerRole is one half of a provider's capability set. upstream: providers/types.ts ProviderRole.
type providerRole string

const (
	roleSearch providerRole = "search"
	roleFetch  providerRole = "fetch"
)

// searchProvider is the search-only contract. upstream: providers/types.ts SearchProvider.
type searchProvider interface {
	Name() string
	Label() string
	EnvVar() string
	Search(query string, maxResults int) (searchResponse, error)
}

// fetchProvider is the fetch-only contract. upstream: providers/types.ts FetchProvider.
type fetchProvider interface {
	Name() string
	Label() string
	EnvVar() string
	Fetch(url string, raw bool) (fetchResponse, error)
}

// fullProvider is the intersection. upstream: providers/types.ts FullProvider.
type fullProvider interface {
	searchProvider
	fetchProvider
}

// userInput is a prompt answer from a provider's configure() helper. A cancellation is reported as an absent value,
// so one helper reads both shapes the UI may return. upstream: providers/types.ts UserInput + isCancellation.
type userInput struct {
	Value string
	Set   bool
}

// isCancellation reports whether the user cancelled, i.e. returned nothing. upstream: providers/types.ts
// isCancellation.
func (u userInput) isCancellation() bool { return !u.Set }

// providerConfigUI is the narrow UI surface a provider's configure() may depend on, so providers/ stays free of
// web-tools internals and the contract grows deliberately. upstream: providers/types.ts ProviderConfigUi.
type providerConfigUI interface {
	Input(label, placeholder string) userInput
}

// providerConfigCurrent is the provider's persisted state as handed to configure(). upstream: providers/types.ts
// ProviderConfigCurrent.
type providerConfigCurrent struct {
	BaseURL    string
	HasBaseURL bool
	APIKey     string
	HasAPIKey  bool
}

// providerConfigChange is what configure() returns for the orchestrator to merge: an absent base URL means the provider
// has no URL knob, and an absent key leaves it unset. upstream: providers/types.ts ProviderConfigChange.
type providerConfigChange struct {
	BaseURL    string
	HasBaseURL bool
	APIKey     string
	HasAPIKey  bool
	UnsetKey   bool
}

// providerMeta is the per-provider metadata declared beside its implementation. It drives generic dispatch, so adding
// a provider does not touch the orchestrator. upstream: providers/types.ts ProviderMeta.
type providerMeta struct {
	Name           string
	Label          string
	EnvVar         string
	BaseURLEnvVar  string
	DefaultBaseURL string
	// Roles keeps the metadata honest; the orchestrator checks capability structurally instead of consulting it.
	Roles []providerRole
	// HasConfigure reports whether /web-tools dispatches to this provider's interactive setup instead of the default
	// single-key prompt.
	HasConfigure bool
}

// The ten providers, in the order upstream declares them: the list order is the schema enum order and the
// /web-tools picker order. upstream: providers/index.ts PROVIDERS.
var providers = []providerMeta{
	{Name: "brave", Label: "Brave", EnvVar: braveAPIKeyEnvVar, Roles: []providerRole{roleSearch}},
	{Name: "tavily", Label: "Tavily", EnvVar: tavilyAPIKeyEnvVar, Roles: []providerRole{roleSearch, roleFetch}},
	{Name: "serper", Label: "Serper", EnvVar: serperAPIKeyEnvVar, Roles: []providerRole{roleSearch}},
	{Name: "exa", Label: "Exa", EnvVar: exaAPIKeyEnvVar, Roles: []providerRole{roleSearch, roleFetch}},
	{Name: "youcom", Label: "You.com", EnvVar: youComAPIKeyEnvVar, Roles: []providerRole{roleSearch, roleFetch}},
	{Name: "jina", Label: "Jina", EnvVar: jinaAPIKeyEnvVar, Roles: []providerRole{roleSearch, roleFetch}},
	{Name: "firecrawl", Label: "Firecrawl", EnvVar: firecrawlAPIKeyEnvVar, Roles: []providerRole{roleSearch, roleFetch}},
	{Name: "perplexity", Label: "Perplexity", EnvVar: perplexityAPIKeyEnvVar, Roles: []providerRole{roleSearch}},
	{
		Name: "searxng", Label: "SearXNG", EnvVar: searxngAPIKeyEnvVar,
		BaseURLEnvVar: searxngURLEnvVar, DefaultBaseURL: searxngDefaultURL,
		Roles: []providerRole{roleSearch}, HasConfigure: true,
	},
	{
		Name: "ollama", Label: "Ollama", EnvVar: ollamaAPIKeyEnvVar,
		BaseURLEnvVar: ollamaHostEnvVar, DefaultBaseURL: ollamaDefaultURL,
		Roles: []providerRole{roleSearch, roleFetch}, HasConfigure: true,
	},
}

// The provider env var and default-URL constants, one per provider file. upstream: the *_API_KEY_ENV_VAR,
// *_URL_ENV_VAR and *_DEFAULT_URL exports of providers/*.ts.
const (
	braveAPIKeyEnvVar      = "BRAVE_SEARCH_API_KEY"
	tavilyAPIKeyEnvVar     = "TAVILY_API_KEY"
	serperAPIKeyEnvVar     = "SERPER_API_KEY"
	exaAPIKeyEnvVar        = "EXA_API_KEY"
	youComAPIKeyEnvVar     = "YOUCOM_API_KEY"
	jinaAPIKeyEnvVar       = "JINA_API_KEY"
	firecrawlAPIKeyEnvVar  = "FIRECRAWL_API_KEY"
	perplexityAPIKeyEnvVar = "PERPLEXITY_API_KEY"

	searxngAPIKeyEnvVar = "SEARXNG_API_KEY"
	searxngURLEnvVar    = "SEARXNG_URL"
	searxngDefaultURL   = "http://localhost:8080"
	ollamaAPIKeyEnvVar  = "OLLAMA_API_KEY"
	ollamaHostEnvVar    = "OLLAMA_HOST"
	ollamaDefaultURL    = "http://localhost:11434"
)

// providerMetaByName is the by-name lookup the key and base-URL resolvers use. upstream: the PROVIDERS.find in
// web-tools.ts resolveProviderApiKey.
func providerMetaByName(name string) (providerMeta, bool) {
	for _, meta := range providers {
		if meta.Name == name {
			return meta, true
		}
	}
	return providerMeta{}, false
}

// knownProviderNames is derived once from the table so the schema enum, the per-call override validation and the
// error message cannot drift apart when a provider is added or removed. upstream: web-tools.ts
// KNOWN_PROVIDER_NAMES.
func knownProviderNames() []string {
	out := make([]string, 0, len(providers))
	for _, meta := range providers {
		out = append(out, meta.Name)
	}
	return out
}

// hasRole reports whether a provider declares a role. upstream: the roles array in ProviderMeta.
func hasRole(meta providerMeta, role providerRole) bool {
	for _, r := range meta.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// providerCredentials is what the factory is handed. upstream: providers/factory.ts ProviderCredentials.
type providerCredentials struct {
	APIKey     string
	HasAPIKey  bool
	BaseURL    string
	HasBaseURL bool
}

// newSearchProvider builds the provider for a name. The unknown-name branch is the factory's uniform error. upstream:
// providers/factory.ts createSearchProvider.
func newSearchProvider(name string, creds providerCredentials) (searchProvider, error) {
	if _, ok := providerMetaByName(name); !ok {
		return nil, &unknownFactoryProviderError{Name: name}
	}
	// Slice 2 ports the table and the factory's dispatch; the provider arms themselves (Search and Fetch) arrive with
	// slice 3 and slice 4, which is where the index ledger's per-provider twins live.
	return newProviderStub(name, creds), nil
}

// unknownFactoryProviderError is the factory's own failure for an unregistered name. It differs from
// unknownProviderError on purpose: the factory never has the valid list, the orchestrator does. upstream:
// providers/factory.ts createSearchProvider's default branch.
type unknownFactoryProviderError struct {
	Name string
}

func (e *unknownFactoryProviderError) Error() string {
	return `Unknown search provider: "` + e.Name + `"`
}

// newProviderStub stands in for a provider arm that slice 3 and slice 4 bring. It answers the identity half of the
// contract exactly, so the table, the key resolution and the factory are exercisable now, and refuses the network
// half rather than pretending to work.
func newProviderStub(name string, creds providerCredentials) searchProvider {
	meta, _ := providerMetaByName(name)
	return &providerStub{meta: meta, creds: creds}
}

// providerStub is the not-yet-ported provider arm. upstream: none yet; see port/PORT.md slice 3 and slice 4.
type providerStub struct {
	meta  providerMeta
	creds providerCredentials
}

func (p *providerStub) Name() string   { return p.meta.Name }
func (p *providerStub) Label() string  { return p.meta.Label }
func (p *providerStub) EnvVar() string { return p.meta.EnvVar }

// Search refuses until the provider's search arm is ported. upstream: providers/*.ts search().
func (p *providerStub) Search(query string, maxResults int) (searchResponse, error) {
	return searchResponse{}, &notPortedError{Provider: p.meta.Name, Arm: "search"}
}

// notPortedError says which arm of which provider slice 3 or slice 4 brings, so a premature call is loud instead of
// silently empty.
type notPortedError struct {
	Provider string
	Arm      string
}

func (e *notPortedError) Error() string {
	return e.Provider + " " + e.Arm + ": not ported yet (see components/rpiv-web-tools/port/PORT.md, slices 3 and 4)"
}

// providerLabel returns a provider's display label, falling back to the name for an unknown provider. upstream: the
// label lookup in the search and fetch registration paths.
func providerLabel(name string) string {
	if meta, ok := providerMetaByName(name); ok {
		return meta.Label
	}
	return name
}

// providerEnumValues is the schema enum for the per-call provider override. upstream: web-tools.ts
// KNOWN_PROVIDER_NAMES.map((name) => Type.Literal(name)).
func providerEnumValues() []string { return knownProviderNames() }

// providerOverrideDescription is the schema description of the per-call override, listing every valid name. upstream:
// web-tools.ts registerWebSearchTool's provider parameter description.
func providerOverrideDescription() string {
	return "Search provider to use for this call only, overriding the active provider set via /web-tools. " +
		"Valid values: " + strings.Join(knownProviderNames(), ", ") + ". " +
		"Omit to use the configured active provider. The named provider must have its API key/URL configured (via env var or /web-tools) or the call throws — there is no silent fallback."
}
