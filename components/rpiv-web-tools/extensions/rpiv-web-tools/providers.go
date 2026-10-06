package rpiv_web_tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall"
)

// The ten search backends of @juicesharp/rpiv-web-tools. upstream: providers/*.ts.

// searchResult is one titled result. upstream: providers/types.ts:1-5.
type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type searchResponse struct {
	Query   string
	Results []searchResult
}

// fetchResponse is a fetched page; the pointer fields are absent in the original's `undefined`.
type fetchResponse struct {
	Text          string
	Title         string // "" is undefined
	ContentType   string // "" is undefined
	ContentLength *float64
}

// provider is a search backend, and a fetch backend when it has the fetch role.
type provider interface {
	name() string
	label() string
	envVar() string
	search(ctx context.Context, query string, maxResults int) (*searchResponse, error)
}

type fetchProvider interface {
	provider
	fetch(ctx context.Context, rawURL string, raw bool) (*fetchResponse, error)
}

// providerMeta describes a backend for the config command and the key resolution. upstream: providers/types.ts:60-70.
type providerMeta struct {
	Name, Label    string
	EnvVar         string
	BaseURLEnvVar  string
	DefaultBaseURL string
	Roles          []string
	// configure runs the backend's own prompts (SearXNG, Ollama); nil for a plain API-key backend.
	configure func(ui configUI, current providerConfigCurrent) (*providerConfigChange, error)
}

const (
	braveKeyEnv      = "BRAVE_SEARCH_API_KEY"
	tavilyKeyEnv     = "TAVILY_API_KEY"
	serperKeyEnv     = "SERPER_API_KEY"
	exaKeyEnv        = "EXA_API_KEY"
	youcomKeyEnv     = "YOUCOM_API_KEY"
	jinaKeyEnv       = "JINA_API_KEY"
	firecrawlKeyEnv  = "FIRECRAWL_API_KEY"
	perplexityKeyEnv = "PERPLEXITY_API_KEY"
	searxngKeyEnv    = "SEARXNG_API_KEY"
	searxngURLEnv    = "SEARXNG_URL"
	searxngDefault   = "http://localhost:8080"
	ollamaKeyEnv     = "OLLAMA_API_KEY"
	ollamaHostEnv    = "OLLAMA_HOST"
	ollamaDefault    = "http://localhost:11434"
)

// providers is the ordered list of backends. upstream: providers/index.ts:63-74.
var providers = []providerMeta{
	{Name: "brave", Label: "Brave", EnvVar: braveKeyEnv, Roles: []string{"search"}},
	{Name: "tavily", Label: "Tavily", EnvVar: tavilyKeyEnv, Roles: []string{"search", "fetch"}},
	{Name: "serper", Label: "Serper", EnvVar: serperKeyEnv, Roles: []string{"search"}},
	{Name: "exa", Label: "Exa", EnvVar: exaKeyEnv, Roles: []string{"search", "fetch"}},
	{Name: "youcom", Label: "You.com", EnvVar: youcomKeyEnv, Roles: []string{"search", "fetch"}},
	{Name: "jina", Label: "Jina", EnvVar: jinaKeyEnv, Roles: []string{"search", "fetch"}},
	{Name: "firecrawl", Label: "Firecrawl", EnvVar: firecrawlKeyEnv, Roles: []string{"search", "fetch"}},
	{Name: "perplexity", Label: "Perplexity", EnvVar: perplexityKeyEnv, Roles: []string{"search"}},
	{Name: "searxng", Label: "SearXNG", EnvVar: searxngKeyEnv, BaseURLEnvVar: searxngURLEnv, DefaultBaseURL: searxngDefault, Roles: []string{"search"}, configure: configureSearxng},
	{Name: "ollama", Label: "Ollama", EnvVar: ollamaKeyEnv, BaseURLEnvVar: ollamaHostEnv, DefaultBaseURL: ollamaDefault, Roles: []string{"search", "fetch"}, configure: configureOllama},
}

func metaOf(name string) *providerMeta {
	for i := range providers {
		if providers[i].Name == name {
			return &providers[i]
		}
	}
	return nil
}

// httpClient performs the providers' requests (a test replaces its transport); redirects are followed.
var httpClient = &http.Client{Timeout: 0}

// base is what the ten providers share.
type base struct {
	nm, lb, env string
	apiKey      string
}

func (b base) name() string   { return b.nm }
func (b base) label() string  { return b.lb }
func (b base) envVar() string { return b.env }

func (b base) requireKey() error {
	if b.apiKey == "" {
		return fmt.Errorf("%s is not set. Run /web-tools to configure, or export the env var.", b.env)
	}
	return nil
}

// do sends one request and returns the response when it is ok; otherwise the API error carrying the body.
// kind is "Search" or "Fetch". upstream: every provider's `!res.ok` branch.
func (b base) do(ctx context.Context, kind, method, rawURL string, headers map[string]string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		data, err := marshalJS(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		text, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return nil, fmt.Errorf("%s %s API error (%d): %s", b.lb, kind, res.StatusCode, text)
	}
	return res, nil
}

// fetchFailed is Node's `TypeError: fetch failed`: any network failure of fetch() reads that way, the real error
// being its cause.
type fetchFailed struct{ cause error }

func (e *fetchFailed) Error() string { return "fetch failed" }
func (e *fetchFailed) Unwrap() error { return e.cause }

// doRequest sends a request; a transport failure becomes the original's "fetch failed", a cancelled call stays
// the cancellation (an AbortError in Node).
func doRequest(req *http.Request) (*http.Response, error) {
	res, err := httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("This operation was aborted")
		}
		return nil, &fetchFailed{err}
	}
	return res, nil
}

// getJSON decodes a response body as a JSON object (maps stay generic; an array decodes too).
func decodeJSON(res *http.Response) (any, error) {
	defer res.Body.Close()
	var v any
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func asObj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asStr(v any) string         { s, _ := v.(string); return s }
func asList(v any) []any         { l, _ := v.([]any); return l }

func results(items []any, title, urlKey, snip func(m map[string]any) string) []searchResult {
	out := make([]searchResult, 0, len(items))
	for _, it := range items {
		m := asObj(it)
		out = append(out, searchResult{Title: title(m), URL: urlKey(m), Snippet: snip(m)})
	}
	return out
}

func key(k string) func(map[string]any) string {
	return func(m map[string]any) string { return asStr(m[k]) }
}

// ---- Brave ---- upstream: providers/brave.ts

type braveProvider struct{ base }

func newBrave(k string) *braveProvider {
	return &braveProvider{base{"brave", "Brave", braveKeyEnv, k}}
}

func (p *braveProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	u, _ := url.Parse("https://api.search.brave.com/res/v1/web/search")
	q := u.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprint(maxResults))
	u.RawQuery = q.Encode()
	res, err := p.do(ctx, "Search", "GET", u.String(), map[string]string{"Accept": "application/json", "Accept-Encoding": "gzip", "X-Subscription-Token": p.apiKey}, nil)
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	return &searchResponse{query, results(asList(asObj(asObj(raw)["web"])["results"]), key("title"), key("url"), key("description"))}, nil
}

// ---- Tavily ---- upstream: providers/tavily.ts

type tavilyProvider struct{ base }

func newTavily(k string) *tavilyProvider {
	return &tavilyProvider{base{"tavily", "Tavily", tavilyKeyEnv, k}}
}

func (p *tavilyProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Search", "POST", "https://api.tavily.com/search", map[string]string{"Content-Type": "application/json"},
		ordered{{"api_key", p.apiKey}, {"query", query}, {"max_results", maxResults}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	return &searchResponse{query, results(asList(asObj(raw)["results"]), key("title"), key("url"), key("content"))}, nil
}

func (p *tavilyProvider) fetch(ctx context.Context, rawURL string, _ bool) (*fetchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Fetch", "POST", "https://api.tavily.com/extract", map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + p.apiKey}, ordered{{"urls", []string{rawURL}}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	data := asObj(raw)
	if failed := asList(data["failed_results"]); len(failed) > 0 {
		f := asObj(failed[0])
		u, e := asStr(f["url"]), asStr(f["error"])
		if u == "" {
			u = rawURL
		}
		if e == "" {
			e = "unknown error"
		}
		return nil, fmt.Errorf("%s Fetch API error: extraction failed for %s: %s", p.lb, u, e)
	}
	var first map[string]any
	if rs := asList(data["results"]); len(rs) > 0 {
		first = asObj(rs[0])
	}
	if asStr(first["raw_content"]) == "" {
		return nil, fmt.Errorf("%s Fetch API error: no content returned for %s", p.lb, rawURL)
	}
	return &fetchResponse{Text: asStr(first["raw_content"]), ContentType: "text/plain"}, nil
}

// ---- Serper ---- upstream: providers/serper.ts

type serperProvider struct{ base }

func newSerper(k string) *serperProvider {
	return &serperProvider{base{"serper", "Serper", serperKeyEnv, k}}
}

func (p *serperProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Search", "POST", "https://google.serper.dev/search", map[string]string{"Content-Type": "application/json", "X-API-KEY": p.apiKey}, ordered{{"q", query}, {"num", maxResults}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	return &searchResponse{query, results(asList(asObj(raw)["organic"]), key("title"), key("link"), key("snippet"))}, nil
}

// ---- Exa ---- upstream: providers/exa.ts

type exaProvider struct{ base }

func newExa(k string) *exaProvider { return &exaProvider{base{"exa", "Exa", exaKeyEnv, k}} }

func (p *exaProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Search", "POST", "https://api.exa.ai/search", map[string]string{"Content-Type": "application/json", "x-api-key": p.apiKey},
		ordered{{"query", query}, {"numResults", maxResults}, {"contents", ordered{{"text", ordered{{"maxCharacters", 300}}}}}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	return &searchResponse{query, results(asList(asObj(raw)["results"]), key("title"), key("url"), key("text"))}, nil
}

func (p *exaProvider) fetch(ctx context.Context, rawURL string, _ bool) (*fetchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Fetch", "POST", "https://api.exa.ai/contents", map[string]string{"Content-Type": "application/json", "x-api-key": p.apiKey},
		ordered{{"ids", []string{rawURL}}, {"text", ordered{{"maxCharacters", 1000000}}}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	var first map[string]any
	if rs := asList(asObj(raw)["results"]); len(rs) > 0 {
		first = asObj(rs[0])
	}
	if asStr(first["text"]) == "" {
		return nil, fmt.Errorf("%s Fetch API error: no content returned for %s", p.lb, rawURL)
	}
	return &fetchResponse{Text: asStr(first["text"]), Title: asStr(first["title"]), ContentType: "text/plain"}, nil
}

// ---- You.com ---- upstream: providers/youcom.ts

type youcomProvider struct{ base }

func newYouCom(k string) *youcomProvider {
	return &youcomProvider{base{"youcom", "You.com", youcomKeyEnv, k}}
}

func (p *youcomProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Search", "POST", "https://ydc-index.io/v1/search", map[string]string{"Content-Type": "application/json", "X-API-Key": p.apiKey}, ordered{{"query", query}, {"count", maxResults}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	snippet := func(m map[string]any) string {
		// `r.snippets?.[0] ?? r.description ?? ""`: a first snippet that exists wins, even when empty.
		if s := asList(m["snippets"]); len(s) > 0 && s[0] != nil {
			return asStr(s[0])
		}
		return asStr(m["description"])
	}
	return &searchResponse{query, results(asList(asObj(asObj(raw)["results"])["web"]), key("title"), key("url"), snippet)}, nil
}

func (p *youcomProvider) fetch(ctx context.Context, rawURL string, _ bool) (*fetchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Fetch", "POST", "https://ydc-index.io/v1/contents", map[string]string{"Content-Type": "application/json", "X-API-Key": p.apiKey}, ordered{{"urls", []string{rawURL}}, {"formats", []string{"markdown"}}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	var item map[string]any
	if items := asList(raw); len(items) > 0 {
		item = asObj(items[0])
	}
	if asStr(item["markdown"]) == "" {
		return nil, fmt.Errorf("%s Fetch API error: no content returned for %s", p.lb, rawURL)
	}
	return &fetchResponse{Text: asStr(item["markdown"]), Title: asStr(item["title"]), ContentType: "text/markdown"}, nil
}

// ---- Jina ---- upstream: providers/jina.ts

type jinaProvider struct{ base }

func newJina(k string) *jinaProvider { return &jinaProvider{base{"jina", "Jina", jinaKeyEnv, k}} }

func (p *jinaProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	u, _ := url.Parse("https://s.jina.ai/" + encodeURIComponent(query))
	q := u.Query()
	q.Set("num", fmt.Sprint(maxResults))
	u.RawQuery = q.Encode()
	res, err := p.do(ctx, "Search", "GET", u.String(), map[string]string{"Accept": "application/json", "Authorization": "Bearer " + p.apiKey}, nil)
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	data := asObj(raw)["data"]
	items := asList(data)
	if _, isArr := data.([]any); !isArr {
		items = asList(asObj(data)["results"])
	}
	rs := results(items, key("title"), key("url"), key("description"))
	if len(rs) > maxResults {
		rs = rs[:maxResults]
	}
	return &searchResponse{query, rs}, nil
}

func (p *jinaProvider) fetch(ctx context.Context, rawURL string, _ bool) (*fetchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Fetch", "GET", "https://r.jina.ai/"+rawURL, map[string]string{"Authorization": "Bearer " + p.apiKey}, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if jsTrim(string(data)) == "" {
		return nil, fmt.Errorf("%s Fetch API error: no content returned for %s", p.lb, rawURL)
	}
	return &fetchResponse{Text: string(data), ContentType: "text/markdown"}, nil
}

// ---- Firecrawl ---- upstream: providers/firecrawl.ts

type firecrawlProvider struct{ base }

func newFirecrawl(k string) *firecrawlProvider {
	return &firecrawlProvider{base{"firecrawl", "Firecrawl", firecrawlKeyEnv, k}}
}

func (p *firecrawlProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Search", "POST", "https://api.firecrawl.dev/v1/search", map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + p.apiKey}, ordered{{"query", query}, {"limit", maxResults}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	return &searchResponse{query, results(asList(asObj(raw)["data"]), key("title"), key("url"), key("description"))}, nil
}

func (p *firecrawlProvider) fetch(ctx context.Context, rawURL string, _ bool) (*fetchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Fetch", "POST", "https://api.firecrawl.dev/v1/scrape", map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + p.apiKey}, ordered{{"url", rawURL}, {"formats", []string{"markdown"}}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	data := asObj(raw)
	if ok, _ := data["success"].(bool); !ok {
		msg := asStr(data["error"])
		if msg == "" {
			msg = "scrape failed"
		}
		return nil, fmt.Errorf("%s Fetch API error: %s", p.lb, msg)
	}
	d := asObj(data["data"])
	if asStr(d["markdown"]) == "" {
		return nil, fmt.Errorf("%s Fetch API error: no content returned for %s", p.lb, rawURL)
	}
	return &fetchResponse{Text: asStr(d["markdown"]), Title: asStr(asObj(d["metadata"])["title"]), ContentType: "text/markdown"}, nil
}

// ---- Perplexity ---- upstream: providers/perplexity.ts

type perplexityProvider struct{ base }

func newPerplexity(k string) *perplexityProvider {
	return &perplexityProvider{base{"perplexity", "Perplexity", perplexityKeyEnv, k}}
}

func (p *perplexityProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if err := p.requireKey(); err != nil {
		return nil, err
	}
	res, err := p.do(ctx, "Search", "POST", "https://api.perplexity.ai/search", map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + p.apiKey}, ordered{{"query", query}, {"max_results", maxResults}})
	if err != nil {
		return nil, err
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	return &searchResponse{query, results(asList(asObj(raw)["results"]), key("title"), key("url"), key("snippet"))}, nil
}

// ---- SearXNG ---- upstream: providers/searxng.ts

type searxngProvider struct {
	base
	baseURL string
}

func stripTrailingSlashes(u string) string { return strings.TrimRight(u, "/") }

// assertHTTPURL: the instance URL must be an http(s) URL; env names the variable in the message.
func assertHTTPURL(env, raw string) error {
	u, err := parseJSURL(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL (got: %s)", env, raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s must use http:// or https:// (got: %s://)", env, u.Scheme)
	}
	return nil
}

func newSearxng(apiKey, baseURL string) (*searxngProvider, error) {
	trimmed := stripTrailingSlashes(jsTrim(baseURL))
	if trimmed != "" {
		if err := assertHTTPURL(searxngURLEnv, trimmed); err != nil {
			return nil, err
		}
	}
	return &searxngProvider{base{"searxng", "SearXNG", searxngKeyEnv, jsTrim(apiKey)}, trimmed}, nil
}

func hintForSearxng(status int) string {
	switch status {
	case 401:
		return " (the SearXNG instance's reverse-proxy rejected the Bearer token; check " + searxngKeyEnv + " or apiKeys.searxng)"
	case 403:
		return " (the SearXNG instance may have JSON output disabled; enable 'json' under 'search.formats' in its settings.yml)"
	}
	return ""
}

func (p *searxngProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if p.baseURL == "" {
		return nil, fmt.Errorf("%s is not set. Run /web-tools to configure, or export the env var.", searxngURLEnv)
	}
	u, _ := url.Parse(p.baseURL + "/search")
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("safesearch", "0")
	u.RawQuery = q.Encode()
	headers := map[string]string{"Accept": "application/json"}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return nil, fmt.Errorf("%s Search API error (%d)%s: %s", p.lb, res.StatusCode, hintForSearxng(res.StatusCode), body)
	}
	raw, err := decodeJSON(res)
	if err != nil {
		return nil, err
	}
	items := asList(asObj(raw)["results"])
	if len(items) > maxResults {
		items = items[:maxResults]
	}
	return &searchResponse{query, results(items, key("title"), key("url"), key("content"))}, nil
}

// ---- Ollama ---- upstream: providers/ollama.ts

type ollamaProvider struct {
	base
	baseURL string
	local   bool
}

func isLocalHost(baseURL string) bool {
	u, err := parseJSURL(baseURL)
	if err != nil {
		return true // default to local paths if the URL is somehow invalid
	}
	h := u.Hostname
	return h == "localhost" || h == "127.0.0.1" || h == "0.0.0.0" || h == "[::1]"
}

func newOllama(apiKey, baseURL string) (*ollamaProvider, error) {
	trimmed := stripTrailingSlashes(jsTrim(baseURL))
	if trimmed != "" {
		if err := assertHTTPURL(ollamaHostEnv, trimmed); err != nil {
			return nil, err
		}
	}
	return &ollamaProvider{base{"ollama", "Ollama", ollamaKeyEnv, jsTrim(apiKey)}, trimmed, isLocalHost(trimmed)}, nil
}

func (p *ollamaProvider) requireBase() error {
	if p.baseURL == "" {
		return fmt.Errorf("%s is not set. Run /web-tools to configure, or export the env var.", ollamaHostEnv)
	}
	return nil
}

func (p *ollamaProvider) headers() map[string]string {
	h := map[string]string{"Content-Type": "application/json"}
	if p.apiKey != "" {
		h["Authorization"] = "Bearer " + p.apiKey
	}
	return h
}

func hintForOllama(status int) string {
	switch status {
	case 401:
		return " (run `ollama signin` to authenticate)"
	case 404:
		return " (the Ollama instance may not support web search; ensure you are running a recent version)"
	}
	return ""
}

// call posts to one of the instance's endpoints; a refused connection becomes the original's message.
func (p *ollamaProvider) call(ctx context.Context, kind, path string, body any) (any, error) {
	data, _ := marshalJS(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+path, bytes.NewReader(data))
	for k, v := range p.headers() {
		req.Header.Set(k, v)
	}
	res, err := doRequest(req)
	if err != nil {
		if isConnectionRefused(err) {
			return nil, fmt.Errorf("Could not connect to Ollama at %s. Make sure Ollama is running (ollama serve).", p.baseURL)
		}
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		text, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return nil, fmt.Errorf("%s %s API error (%d)%s: %s", p.lb, kind, res.StatusCode, hintForOllama(res.StatusCode), text)
	}
	return decodeJSON(res)
}

func (p *ollamaProvider) search(ctx context.Context, query string, maxResults int) (*searchResponse, error) {
	if err := p.requireBase(); err != nil {
		return nil, err
	}
	path := "/api/web_search"
	if p.local {
		path = "/api/experimental/web_search"
	}
	raw, err := p.call(ctx, "Search", path, ordered{{"query", query}, {"max_results", maxResults}})
	if err != nil {
		return nil, err
	}
	return &searchResponse{query, results(asList(asObj(raw)["results"]), key("title"), key("url"), key("content"))}, nil
}

func (p *ollamaProvider) fetch(ctx context.Context, rawURL string, _ bool) (*fetchResponse, error) {
	if err := p.requireBase(); err != nil {
		return nil, err
	}
	path := "/api/web_fetch"
	if p.local {
		path = "/api/experimental/web_fetch"
	}
	raw, err := p.call(ctx, "Fetch", path, ordered{{"url", rawURL}})
	if err != nil {
		return nil, err
	}
	d := asObj(raw)
	if asStr(d["content"]) == "" {
		return nil, fmt.Errorf("%s Fetch API error: no content returned for %s", p.lb, rawURL)
	}
	return &fetchResponse{Text: asStr(d["content"]), Title: asStr(d["title"]), ContentType: "text/plain"}, nil
}

func isConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "connection refused")
}

// createSearchProvider builds a backend by name. upstream: providers/factory.ts.
func createSearchProvider(name, apiKey, baseURL string) (provider, error) {
	switch name {
	case "brave":
		return newBrave(apiKey), nil
	case "tavily":
		return newTavily(apiKey), nil
	case "serper":
		return newSerper(apiKey), nil
	case "exa":
		return newExa(apiKey), nil
	case "youcom":
		return newYouCom(apiKey), nil
	case "jina":
		return newJina(apiKey), nil
	case "firecrawl":
		return newFirecrawl(apiKey), nil
	case "perplexity":
		return newPerplexity(apiKey), nil
	case "searxng":
		return newSearxng(apiKey, baseURL)
	case "ollama":
		return newOllama(apiKey, baseURL)
	}
	return nil, fmt.Errorf("Unknown search provider: \"%s\"", name)
}
