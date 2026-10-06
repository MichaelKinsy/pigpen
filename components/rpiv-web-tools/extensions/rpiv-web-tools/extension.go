// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The PiG registration layer: the two tools, the slash command, and the wiring that turns the ported pure functions
// into a running extension. upstream: web-tools.ts index.ts (the registerWebTools entry point), registerWebSearchTool,
// registerWebFetchTool and registerWebSearchConfigCommand.

// toolNames and labels. upstream: the name/label fields of the two tool registrations.
const (
	searchToolName = "web_search"
	fetchToolName  = "web_fetch"
)

// toolDescriptions are the model-facing descriptions. upstream: the description fields of the two registrations.
const (
	searchToolDescription = "Search the web for information. Returns a list of results with titles, URLs, and snippets. " +
		"Use when you need current information not in your training data."
	fetchToolDescription = "Fetch and read content from a specific URL, turning it into readable text for the model. " +
		"Use for documentation pages, blog posts and API references that web_search found."
	commandDescription = "Configure the search provider and API key used by web_search"
)

// httpClient is the client the arms run through. It is the seam the twins replace with fakeHTTP, so every request
// shape in the port is proven without a network. upstream: the global fetch the arms call.
type httpClient struct {
	doer httpDoer
}

// newHTTPClient returns the production client: net/http behind the arms' interface.
func newHTTPClient() httpClient {
	return httpClient{doer: netHTTPDoer{}}
}

// netHTTPDoer performs an arm's request over net/http, with the shared headers the arm asked for. The redirect
// behaviour matches the original's `redirect: "follow"`. upstream: fetch's default redirect behaviour.
type netHTTPDoer struct{}

// Do performs one request and reads the body and the two headers the arms care about.
func (netHTTPDoer) Do(req httpRequest) (httpResponse, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	var body *strings.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	var httpReq *http.Request
	var err error
	if body != nil {
		httpReq, err = http.NewRequest(method, req.URL, body)
	} else {
		httpReq, err = http.NewRequest(method, req.URL, nil)
	}
	if err != nil {
		return httpResponse{}, err
	}
	for name, value := range req.Headers {
		httpReq.Header.Set(name, value)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return httpResponse{}, err
	}
	defer resp.Body.Close()
	data := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		data = append(data, buf[:n]...)
		if readErr != nil {
			break
		}
	}
	out := httpResponse{Status: resp.StatusCode, Body: string(data)}
	if v := resp.Header.Get("Content-Type"); v != "" {
		out.ContentType, out.HasContentType = v, true
	}
	if v := resp.Header.Get("Content-Length"); v != "" {
		out.ContentLength, out.HasContentLength = v, true
	}
	return out, nil
}

// envReader reads the process environment, which is where the providers' keys and URLs come from. upstream: the
// process.env reads in the key and base-URL resolvers.
func envReader(name string) string { return os.Getenv(name) }

// maxFetchBytes is the body budget the fetch tool keeps inline; the rest is spilled. upstream: the DEFAULT_MAX_BYTES
// the original's truncation branch compares against.
const maxFetchBytes = 50 * 1024

// Extension returns the rpiv-web-tools extension: web_search, web_fetch and /web-tools. upstream: index.ts
// registerWebTools.
func Extension() *sdk.Extension {
	e := sdk.New("rpiv-web-tools")
	app := newApp()
	e.RegisterTool(app.searchToolDefinition())
	e.RegisterTool(app.fetchToolDefinition())
	e.RegisterCommand(webToolsCommandName, sdk.CommandOptions{
		Description: commandDescription,
		Handler:     app.commandHandler,
	})
	return e
}

// app holds what the two tools share: the client, the interceptor chain and the config path. upstream: the module-level
// singletons and the per-request lookups in web-tools.ts.
type app struct {
	client           httpClient
	registry         interceptorRegistry
	collapseRequests []string
}

func newApp() *app {
	a := &app{client: newHTTPClient()}
	a.installInterceptors()
	return a
}

// installInterceptors resolves the GitHub opt-in once at registration, the way the original does at startup. upstream:
// the buildInterceptors call in registerWebTools.
func (a *app) installInterceptors() {
	a.registry.build(readUserGitHubConfig(), false)
}

// config loads the config for this tick. upstream: loadConfig, the readConfig alias.
func (a *app) config() config { return ReadConfig() }

// searchToolDefinition is the registered web_search: its schema, its guidance and its renderer. upstream:
// registerWebSearchTool.
func (a *app) searchToolDefinition() sdk.ToolDefinition {
	search, _ := guidanceForTools(a.config())
	maxResults := searchSchemaMaxResultsParameter()
	return sdk.ToolDefinition{
		Name:             searchToolName,
		Label:            "Web Search",
		Description:      searchToolDescription,
		PromptSnippet:    search.PromptSnippet,
		PromptGuidelines: search.PromptGuidelines,
		Parameters: sdk.Schema{
			"type":     "object",
			"required": []any{"query"},
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query. Be specific and use natural language.",
				},
				"max_results": map[string]any{
					"type":        "number",
					"description": maxResults.Description,
					"minimum":     maxResults.Minimum,
					"maximum":     maxResults.Maximum,
					"default":     maxResults.Default,
				},
				"provider": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string", "enum": enumOf(providerEnumValues()...)},
					"description": providerOverrideDescription(),
				},
			},
		},
		Execute:      a.executeSearch,
		RenderCall:   a.renderSearchCall,
		RenderResult: a.renderSearchResult,
	}
}

// fetchToolDefinition is the registered web_fetch. upstream: registerWebFetchTool.
func (a *app) fetchToolDefinition() sdk.ToolDefinition {
	_, fetch := guidanceForTools(a.config())
	return sdk.ToolDefinition{
		Name:             fetchToolName,
		Label:            "Web Fetch",
		Description:      fetchToolDescription,
		PromptSnippet:    fetch.PromptSnippet,
		PromptGuidelines: fetch.PromptGuidelines,
		Parameters: sdk.Schema{
			"type":     "object",
			"required": []any{"url"},
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch and read.",
				},
				"raw": map[string]any{
					"type":        "boolean",
					"description": "Return the raw body instead of converting an HTML page to text.",
					"default":     false,
				},
			},
		},
		Execute:      a.executeFetch,
		RenderCall:   a.renderFetchCall,
		RenderResult: a.renderFetchResult,
	}
}

// executeSearch is the web_search body: resolve the provider, run the arm, and answer with the envelope. upstream:
// the execute callback of registerWebSearchTool.
func (a *app) executeSearch(ctx sdk.Context, params map[string]any) (any, error) {
	query, _ := params["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, sdk.NewToolError("web_search: query must not be empty")
	}
	cfg := a.config()

	override, hasOverride := providerOverrideParam(params)
	providerName, err := instantiateProvider(cfg, override, hasOverride, envReader)
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}
	label := providerLabel(providerName)

	var requested *float64
	if v, ok := numberFromParams(params["max_results"]); ok {
		requested = &v
	}
	maxResults := clampSearchResultCount(requested)

	_ = ctx.OnUpdate(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "Searching " + label + " for: \"" + query + "\"..."}},
		"details": map[string]any{"query": query, "backend": providerName, "resultCount": 0},
	})

	response, err := a.runSearch(providerName, cfg, query, maxResults)
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}
	if len(response.Results) == 0 {
		text, details := emptyResultsEnvelope(query, providerName)
		return sdk.ToolResult{Content: text, Details: details}, nil
	}
	return sdk.ToolResult{
		Content: searchResultsBody(response),
		Details: map[string]any{
			"query": query, "backend": providerName,
			"resultCount": len(response.Results), "results": response.Results,
		},
	}, nil
}

// runSearch dispatches to the arm of the resolved provider, giving the two hosted ones their base URL. upstream:
// the createSearchProvider call and the provider's search().
func (a *app) runSearch(providerName string, cfg config, query string, maxResults int) (searchResponse, error) {
	switch providerName {
	case "searxng":
		meta, _ := providerMetaByName("searxng")
		return searchSearxng(a.client.doer, resolveProviderBaseURL(meta, cfg, envReader), envReader(meta.EnvVar), query, maxResults)
	case "ollama":
		meta, _ := providerMetaByName("ollama")
		local := true
		if strings.HasPrefix(resolveProviderBaseURL(meta, cfg, envReader), "https://") {
			local = false
		}
		return searchOllama(a.client.doer, resolveProviderBaseURL(meta, cfg, envReader), envReader(meta.EnvVar), query, maxResults, local)
	}
	provider, err := newSearchProvider(providerName, providerCredentials{
		APIKey:    resolveProviderAPIKey(providerName, cfg, envReader),
		HasAPIKey: resolveProviderAPIKey(providerName, cfg, envReader) != "",
	}, a.client.doer)
	if err != nil {
		return searchResponse{}, err
	}
	return provider.Search(query, maxResults)
}

// executeFetch is the web_fetch body: guard the URL, run the interceptor chain, then the provider, then the generic
// path, and cap the body. upstream: the execute callback of registerWebFetchTool.
func (a *app) executeFetch(ctx sdk.Context, params map[string]any) (any, error) {
	target, _ := params["url"].(string)
	raw, _ := params["raw"].(bool)
	target = strings.TrimSpace(target)
	if _, err := parseAndAssertHTTPURL(target); err != nil {
		return nil, sdk.NewToolError(err.Error())
	}
	// The raw listener is subscribed for the length of this call: pi-tui routes no input to a hidden overlay, so the
	// collapse key needs this path while the fetch is in flight. upstream: the ctx.ui.onTerminalInput registration
	// in execute().
	if unsubscribe, err := a.subscribeTerminalInput(ctx); err == nil && unsubscribe != nil {
		defer unsubscribe()
	}

	_ = ctx.OnUpdate(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "Fetching " + target + "..."}},
		"details": map[string]any{"url": target},
	})

	response, err := a.runFetch(target, raw)
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}

	body, truncation, err := truncateBody(response.Text, maxFetchBytes)
	if err != nil {
		return nil, sdk.NewToolError("web_fetch: could not spill the full content: " + err.Error())
	}
	text := fetchHeader(target, response.Title, response.ContentType, response.HasTitle, response.HasContentType) + body
	if truncation.Truncated {
		text += formatTruncationFooter(truncation)
	}

	details := map[string]any{"url": target}
	if response.HasTitle {
		details["title"] = response.Title
	}
	if response.HasContentType {
		details["contentType"] = response.ContentType
	}
	if response.ContentLength != nil {
		details["contentLength"] = *response.ContentLength
	}
	if truncation.Truncated {
		details["truncation"] = map[string]any{
			"truncated": true, "outputLines": truncation.OutputLines, "totalLines": truncation.TotalLines,
			"outputBytes": truncation.OutputBytes, "totalBytes": truncation.TotalBytes,
			"fullOutputPath": truncation.TempFilePath,
		}
		details["fullOutputPath"] = truncation.TempFilePath
	}
	return sdk.ToolResult{Content: text, Details: details}, nil
}

// runFetch is the three-stage fetch dispatch, and the one place a network request is initiated, so the URL guard
// lives here rather than only at the tool entry: a second caller cannot skip it. upstream: the interceptor loop, the
// provider fetch and the generic path, behind parseAndAssertHttpUrl.
func (a *app) runFetch(target string, raw bool) (fetchResponse, error) {
	if _, err := parseAndAssertHTTPURL(target); err != nil {
		return fetchResponse{}, err
	}
	cfg := a.config()
	var providerFetch func(string, bool) (fetchResponse, error)
	if meta, ok := providerMetaByName(resolveActiveProviderName(cfg, envReader).Name); ok && hasRole(meta, roleFetch) {
		providerFetch = func(t string, r bool) (fetchResponse, error) { return a.fetchViaProvider(t, r) }
	}
	return fetchDispatch(a.client.doer, a.registry.interceptors(), providerFetch, target, raw)
}

// fetchViaProvider runs the active provider's own fetch arm when it has one. upstream: the `"fetch" in provider` branch.
func (a *app) fetchViaProvider(target string, _ bool) (fetchResponse, error) {
	cfg := a.config()
	name := resolveActiveProviderName(cfg, envReader).Name
	key := resolveProviderAPIKey(name, cfg, envReader)
	switch name {
	case "tavily":
		return fetchTavily(a.client.doer, key, target)
	case "exa":
		return fetchExa(a.client.doer, key, target)
	case "jina":
		return fetchJina(a.client.doer, key, target)
	case "firecrawl":
		return fetchFirecrawl(a.client.doer, key, target)
	case "ollama":
		meta, _ := providerMetaByName("ollama")
		baseURL := resolveProviderBaseURL(meta, cfg, envReader)
		return fetchOllama(a.client.doer, baseURL, key, target, !strings.HasPrefix(baseURL, "https://"))
	}
	// The extraction providers ignore raw: their body is already the extracted form.
	return fetchResponse{}, nil
}

// commandHandler is the /web-tools handler, driven through the host interface. upstream: the command registration in
// registerWebSearchConfigCommand.
func (a *app) commandHandler(ctx sdk.Context, args string) error {
	runConfigCommand(&sdkCommandHost{ctx: ctx}, args, a.config(), envReader, ConfigPath(), WriteConfig)
	return nil
}

// sdkCommandHost adapts the PiG context to the command's host interface.
type sdkCommandHost struct {
	ctx sdk.Context
}

func (h *sdkCommandHost) hasUI() bool { return h.ctx.HasUI() }

func (h *sdkCommandHost) selectOne(title string, options []string) (string, bool) {
	value, ok, err := h.ctx.Select(title, options)
	if err != nil || !ok {
		return "", false
	}
	return value, true
}

func (h *sdkCommandHost) input(label, placeholder string) (string, bool) {
	value, ok, err := h.ctx.Input(label, placeholder)
	if err != nil || !ok {
		return "", false
	}
	return value, true
}

func (h *sdkCommandHost) notify(message, level string) { h.ctx.Notify(message, level) }

// renderCall and renderResult callbacks of the two registrations.
func (a *app) renderSearchCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) ([]string, error) {
	query, _ := args["query"].(string)
	provider, hasProvider := providerOverrideParam(args)
	th := contextTheme(ctx)
	return []string{searchRenderCall(th, query, provider, hasProvider)}, nil
}

func (a *app) renderSearchResult(ctx sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, _ int) ([]string, error) {
	details, results := searchRenderDetails(result)
	th := contextTheme(ctx)
	return []string{searchRenderResult(th, options.IsPartial, options.Expanded, details, results)}, nil
}

func (a *app) renderFetchCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) ([]string, error) {
	target, _ := args["url"].(string)
	return []string{fetchRenderCall(contextTheme(ctx), target)}, nil
}

func (a *app) renderFetchResult(ctx sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, _ int) ([]string, error) {
	title, hasTitle, truncated, content, hasContent := fetchRenderDetails(result)
	th := contextTheme(ctx)
	return []string{fetchRenderResult(th, options.IsPartial, options.Expanded, title, hasTitle, truncated, content, hasContent)}, nil
}

// providerOverrideParam reads the per-call provider override, which may arrive as a string or as a one-element array.
func providerOverrideParam(params map[string]any) (string, bool) {
	switch v := params["provider"].(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return "", false
		}
		return v, true
	case []string:
		if len(v) == 0 {
			return "", false
		}
		return v[0], true
	case []any:
		if len(v) == 0 {
			return "", false
		}
		if s, ok := v[0].(string); ok && strings.TrimSpace(s) != "" {
			return s, true
		}
	}
	return "", false
}

// numberFromParams reads a numeric parameter that may arrive as a float64 or as a JSON number. upstream: the
// params.max_results the original passes straight to clampSearchResultCount.
func numberFromParams(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// enumOf builds the JSON Schema enum of the per-call provider override. upstream: the Type.Union of Type.Literal over
// the known provider names.
func enumOf(values ...string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// contextTheme is the host's theme behind the renderer's two styling calls. A render hook must never take the
// host down, so every failure here — no host, no theme, a panicking lookup — falls back to the plain theme and the
// text is produced without styling. upstream: the theme argument of renderCall and renderResult.
func contextTheme(ctx sdk.Context) theme {
	return safeContextTheme(ctx)
}

// safeContextTheme performs the lookup behind a guard, because a zero or headless Context has no host connection and
// the SDK's host-required calls panic on one.
func safeContextTheme(ctx sdk.Context) (th theme) {
	th = plainTheme()
	defer func() {
		if recover() != nil {
			th = plainTheme()
		}
	}()
	loaded, err := ctx.GetTheme("default")
	if err != nil || loaded == nil {
		return th
	}
	if ui, ok := loaded.(*sdk.UITheme); ok && ui != nil {
		return theme{
			fg:   func(token, text string) string { return ui.Fg(token, text) },
			bold: func(text string) string { return ui.Bold(text) },
		}
	}
	return th
}

// searchRenderDetails pulls the search details and the rows out of a render result. upstream: the details object the
// renderResult callback reads.
func searchRenderDetails(result sdk.ToolRenderResult) (searchEnvelopeDetails, []searchResult) {
	details := searchEnvelopeDetails{}
	var results []searchResult
	if m, ok := result.Details.(map[string]any); ok {
		if v, ok := numberFromParams(m["resultCount"]); ok {
			details.ResultCount = int(v)
		}
		if q, ok := m["query"].(string); ok {
			details.Query = q
		}
		if backend, ok := m["backend"].(string); ok {
			details.Backend = backend
		}
		rows, _ := m["results"].([]searchResult)
		results = rows
	}
	return details, results
}

// fetchRenderDetails pulls the title, the truncation flag and the first text block out of a render result. upstream:
// the details object and result.content[0] the renderResult callback reads.
func fetchRenderDetails(result sdk.ToolRenderResult) (string, bool, bool, string, bool) {
	title, hasTitle, truncated := "", false, false
	if m, ok := result.Details.(map[string]any); ok {
		if v, ok := m["title"].(string); ok && v != "" {
			title, hasTitle = v, true
		}
		if t, ok := m["truncation"].(map[string]any); ok {
			if v, ok := t["truncated"].(bool); ok {
				truncated = v
			}
		}
	}
	for _, block := range result.Content {
		if block["type"] == "text" {
			if v, ok := block["text"].(string); ok {
				return title, hasTitle, truncated, v, true
			}
		}
	}
	return title, hasTitle, truncated, "", false
}

// subscribeTerminalInput installs the raw-input listener the collapse key needs while the overlay is hidden, and
// returns the unsubscribe. pi-tui routes no input to a hidden overlay, so this is the only path that reaches the
// session then. upstream: the ctx.ui.onTerminalInput registration in execute().
func (a *app) subscribeTerminalInput(ctx sdk.Context) (func(), error) {
	return ctx.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		return a.onTerminalInput(data)
	})
}

// onTerminalInput is the raw listener's body: it lets the collapse key reach the session while the overlay is
// hidden, and leaves every other chunk to the host. upstream: the raw listener's handler in execute().
func (a *app) onTerminalInput(data string) sdk.TerminalInputResult {
	if a.registry.activeGitHubInterceptor() == nil {
		return sdk.TerminalInputResult{}
	}
	// The session owns the toggle; the listener only has to say whether it consumed the chunk.
	a.collapseRequests = append(a.collapseRequests, data)
	return sdk.TerminalInputResult{Consume: true}
}

// drainCollapseRequests returns the chunks the raw listener held back, which the next session tick consumes. It
// exists so the raw path stays testable without a host.
func (a *app) drainCollapseRequests() []string {
	out := a.collapseRequests
	a.collapseRequests = nil
	return out
}
