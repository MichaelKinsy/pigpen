// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// The ten search arms. Each keeps the original's guard, request shape, status check and normalisation: a missing key
// throws before any request, a non-2xx becomes "<Label> Search API error (<status>): <body>", and every vendor row
// normalizes its missing fields to empty strings rather than dropping the row.

// missingKeyError is the guard every keyed provider throws before it makes a request. upstream: the
// `${this.envVar} is not set…` throw each provider opens with.
func missingKeyError(envVar string) error {
	return fmt.Errorf("%s is not set. Run /web-tools to configure, or export the env var.", envVar)
}

// searchAPIError is the non-2xx wrapper the nine keyed providers share. upstream: the
// `${this.label} Search API error (${res.status}): ${text}` throw.
func searchAPIError(label string, res httpResponse) error {
	return fmt.Errorf("%s Search API error (%d): %s", label, res.Status, res.Body)
}

// searchResponseOf builds the response envelope for a query. upstream: the `{ query, results }` return.
func searchResponseOf(query string, results []searchResult) searchResponse {
	if results == nil {
		results = []searchResult{}
	}
	return searchResponse{Query: query, Results: results}
}

// str reads a string field, defaulting an absent or mistyped one to the empty string, which is how every normalizer
// treats a missing vendor field. upstream: the `?? ""` in each normalize function.
func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// obj reads an object field.
func obj(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key].(map[string]any)
	return v, ok
}

// arr reads an array field.
func arr(m map[string]any, key string) ([]any, bool) {
	v, ok := m[key].([]any)
	return v, ok
}

// decodeJSONObject parses a response body into a decoded object, with the two failure modes the arms treat alike.
func decodeJSONObject(body string) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// The vendor endpoints. upstream: the *_API_URL constants of providers/*.ts.
const (
	braveSearchAPIURL     = "https://api.search.brave.com/res/v1/web/search"
	serperAPIURL          = "https://google.serper.dev/search"
	perplexityAPIURL      = "https://api.perplexity.ai/search"
	tavilyAPIURL          = "https://api.tavily.com/search"
	tavilyExtractAPIURL   = "https://api.tavily.com/extract"
	exaAPIURL             = "https://api.exa.ai/search"
	exaContentsAPIURL     = "https://api.exa.ai/contents"
	jinaSearchAPIURL      = "https://s.jina.ai/"
	jinaReaderAPIURL      = "https://r.jina.ai/"
	youComSearchURL       = "https://ydc-index.io/v1/search"
	youComContentsURL     = "https://ydc-index.io/v1/contents"
	firecrawlAPIURL       = "https://api.firecrawl.dev/v1"
	searxngSearchPath     = "/search"
	searxngFormatJSON     = "json"
	searxngSafesearchOff  = "0"
	ollamaLocalSearchPath = "/api/experimental/web_search"
	ollamaCloudSearchPath = "/api/web_search"
)

// exaMaxSnippetCharacters is the snippet cap. upstream: exa.ts EXA_MAX_SNIPPET_CHARACTERS, with its reasoning: the
// documented maximum is 10 000 (OpenAPI) but the live API accepts up to 1 000 000, so the port leaves truncation to the
// tool's own 50 KiB budget, which appends a footer and spills to a temp file.
const exaMaxSnippetCharacters = 300

// searchBrave queries the Brave Web Search API. upstream: providers/brave.ts BraveProvider.search.
func searchBrave(client httpDoer, apiKey, query string, maxResults int) (searchResponse, error) {
	if apiKey == "" {
		return searchResponse{}, missingKeyError(braveAPIKeyEnvVar)
	}
	endpoint, err := url.Parse(braveSearchAPIURL)
	if err != nil {
		return searchResponse{}, err
	}
	q := endpoint.Query()
	q.Set("q", query)
	q.Set("count", strconv.Itoa(maxResults))
	endpoint.RawQuery = q.Encode()

	res, err := client.Do(httpRequest{
		Method: "GET",
		URL:    endpoint.String(),
		Headers: map[string]string{
			"Accept":               "application/json",
			"Accept-Encoding":      "gzip",
			"X-Subscription-Token": apiKey,
		},
	})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, searchAPIError("Brave", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var results []searchResult
	if web, ok := obj(raw, "web"); ok {
		if rows, ok := arr(web, "results"); ok {
			for _, row := range rows {
				if m, ok := row.(map[string]any); ok {
					results = append(results, searchResult{
						Title: str(m, "title"), URL: str(m, "url"), Snippet: str(m, "description"),
					})
				}
			}
		}
	}
	return searchResponseOf(query, results), nil
}

// searchSerper queries the Serper API. upstream: providers/serper.ts SerperProvider.search.
func searchSerper(client httpDoer, apiKey, query string, maxResults int) (searchResponse, error) {
	if apiKey == "" {
		return searchResponse{}, missingKeyError(serperAPIKeyEnvVar)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     serperAPIURL,
		Headers: map[string]string{"X-API-KEY": apiKey, "Content-Type": "application/json"},
		Body:    fmt.Sprintf(`{"q":%s}`, mustJSONString(query)),
	})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, searchAPIError("Serper", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var results []searchResult
	if rows, ok := arr(raw, "organic"); ok {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				results = append(results, searchResult{
					Title: str(m, "title"), URL: str(m, "link"), Snippet: str(m, "snippet"),
				})
			}
		}
	}
	return searchResponseOf(query, results), nil
}

// searchPerplexity queries the Perplexity search API. upstream: providers/perplexity.ts PerplexityProvider.search.
func searchPerplexity(client httpDoer, apiKey, query string, maxResults int) (searchResponse, error) {
	if apiKey == "" {
		return searchResponse{}, missingKeyError(perplexityAPIKeyEnvVar)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     perplexityAPIURL,
		Headers: map[string]string{"Authorization": "Bearer " + apiKey, "Content-Type": "application/json"},
		Body:    fmt.Sprintf(`{"query":%s,"max_results":%d}`, mustJSONString(query), maxResults),
	})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, searchAPIError("Perplexity", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var results []searchResult
	if rows, ok := arr(raw, "results"); ok {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				results = append(results, searchResult{
					Title: str(m, "title"), URL: str(m, "url"), Snippet: str(m, "snippet"),
				})
			}
		}
	}
	return searchResponseOf(query, results), nil
}

// searchTavily queries the Tavily search API. upstream: providers/tavily.ts TavilyProvider.search.
func searchTavily(client httpDoer, apiKey, query string, maxResults int) (searchResponse, error) {
	if apiKey == "" {
		return searchResponse{}, missingKeyError(tavilyAPIKeyEnvVar)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     tavilyAPIURL,
		Headers: map[string]string{"Authorization": "Bearer " + apiKey, "Content-Type": "application/json"},
		Body:    fmt.Sprintf(`{"query":%s,"max_results":%d}`, mustJSONString(query), maxResults),
	})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, searchAPIError("Tavily", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var results []searchResult
	if rows, ok := arr(raw, "results"); ok {
		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			// A failed_results entry carries the failure inline rather than dropping out of the list.
			if failed, ok := m["failed_results"].(map[string]any); ok {
				results = append(results, searchResult{
					Title: str(failed, "title"), URL: str(failed, "url"), Snippet: str(failed, "error"),
				})
				continue
			}
			results = append(results, searchResult{
				Title: str(m, "title"), URL: str(m, "url"), Snippet: str(m, "content"),
			})
		}
	}
	return searchResponseOf(query, results), nil
}

// searchExa queries the Exa search API, capping the snippet at 300 characters. upstream: providers/exa.ts
// ExaProvider.search.
func searchExa(client httpDoer, apiKey, query string, maxResults int) (searchResponse, error) {
	if apiKey == "" {
		return searchResponse{}, missingKeyError(exaAPIKeyEnvVar)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     exaAPIURL,
		Headers: map[string]string{"x-api-key": apiKey, "Content-Type": "application/json"},
		Body: fmt.Sprintf(`{"query":%s,"numResults":%d,"contents":{"text":{"maxCharacters":%d}}}`,
			mustJSONString(query), maxResults, exaMaxSnippetCharacters),
	})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, searchAPIError("Exa", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var results []searchResult
	if rows, ok := arr(raw, "results"); ok {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				snippet := str(m, "snippet")
				results = append(results, searchResult{
					Title: str(m, "title"), URL: str(m, "url"), Snippet: truncateRunes(snippet, exaMaxSnippetCharacters),
				})
			}
		}
	}
	return searchResponseOf(query, results), nil
}

// searchJina queries the Jina search endpoint, which takes the query in the path. upstream: providers/jina.ts
// JinaProvider.search.
func searchJina(client httpDoer, apiKey, query string, maxResults int) (searchResponse, error) {
	if apiKey == "" {
		return searchResponse{}, missingKeyError(jinaAPIKeyEnvVar)
	}
	endpoint, err := url.Parse(jinaSearchAPIURL + url.PathEscape(query))
	if err != nil {
		return searchResponse{}, err
	}
	q := endpoint.Query()
	q.Set("num", strconv.Itoa(maxResults))
	endpoint.RawQuery = q.Encode()

	res, err := client.Do(httpRequest{
		Method:  "GET",
		URL:     endpoint.String(),
		Headers: map[string]string{"Accept": "application/json", "Authorization": "Bearer " + apiKey},
	})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, searchAPIError("Jina", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var rows []any
	if data, ok := obj(raw, "data"); ok {
		if list, ok := arr(data, "results"); ok {
			rows = list
		} else if list, ok := arr(raw, "data"); ok {
			rows = list
		}
	} else if list, ok := arr(raw, "data"); ok {
		rows = list
	}
	var results []searchResult
	for _, row := range rows {
		if m, ok := row.(map[string]any); ok {
			results = append(results, searchResult{
				Title: str(m, "title"), URL: str(m, "url"), Snippet: str(m, "description"),
			})
		}
	}
	return searchResponseOf(query, results), nil
}

// searchYouCom queries the You.com index. upstream: providers/youcom.ts YouComProvider.search.
func searchYouCom(client httpDoer, apiKey, query string, maxResults int) (searchResponse, error) {
	if apiKey == "" {
		return searchResponse{}, missingKeyError(youComAPIKeyEnvVar)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     youComSearchURL,
		Headers: map[string]string{"X-API-Key": apiKey, "Content-Type": "application/json"},
		Body:    fmt.Sprintf(`{"query":%s}`, mustJSONString(query)),
	})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, searchAPIError("You.com", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var results []searchResult
	if resultsObj, ok := obj(raw, "results"); ok {
		if rows, ok := arr(resultsObj, "web"); ok {
			for _, row := range rows {
				if m, ok := row.(map[string]any); ok {
					results = append(results, searchResult{
						Title: str(m, "title"), URL: str(m, "url"), Snippet: str(m, "description"),
					})
				}
			}
		}
	}
	return searchResponseOf(query, results), nil
}

// searchFirecrawl queries the Firecrawl search endpoint. upstream: providers/firecrawl.ts FirecrawlProvider.search.
func searchFirecrawl(client httpDoer, apiKey, query string, maxResults int) (searchResponse, error) {
	if apiKey == "" {
		return searchResponse{}, missingKeyError(firecrawlAPIKeyEnvVar)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     firecrawlAPIURL + "/search",
		Headers: map[string]string{"Authorization": "Bearer " + apiKey, "Content-Type": "application/json"},
		Body:    fmt.Sprintf(`{"query":%s,"limit":%d}`, mustJSONString(query), maxResults),
	})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, searchAPIError("Firecrawl", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var results []searchResult
	if rows, ok := arr(raw, "data"); ok {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				results = append(results, searchResult{
					Title: str(m, "title"), URL: str(m, "url"), Snippet: str(m, "description"),
				})
			}
		}
	}
	return searchResponseOf(query, results), nil
}

// searxngHintForStatus is the per-status hint the self-hosted backend attaches to its error, which is what makes its
// failures diagnosable. upstream: hintForSearchStatus in providers/searxng.ts.
func searxngHintForStatus(status int) string {
	switch status {
	case 403:
		return " — JSON output may be disabled on this instance; set format=json in its settings"
	case 401:
		return " — the reverse proxy rejected the Bearer token"
	}
	return ""
}

// searchSearxng queries a self-hosted SearXNG instance. The API offers only pageno, never count or limit, so one page is
// requested and sliced client-side. upstream: providers/searxng.ts SearxngProvider.search.
func searchSearxng(client httpDoer, baseURL, apiKey, query string, maxResults int) (searchResponse, error) {
	baseURL = stripTrailingSlashes(strings.TrimSpace(baseURL))
	if baseURL == "" {
		return searchResponse{}, missingKeyError(searxngURLEnvVar)
	}
	if err := assertHTTPURL(baseURL); err != nil {
		return searchResponse{}, err
	}
	endpoint, err := url.Parse(baseURL + searxngSearchPath)
	if err != nil {
		return searchResponse{}, err
	}
	q := endpoint.Query()
	q.Set("q", query)
	q.Set("format", searxngFormatJSON)
	q.Set("safesearch", searxngSafesearchOff)
	endpoint.RawQuery = q.Encode()

	headers := map[string]string{"Accept": "application/json"}
	// SearXNG has no auth of its own; the optional Bearer key is for instances behind a gating proxy.
	if strings.TrimSpace(apiKey) != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(apiKey)
	}
	res, err := client.Do(httpRequest{Method: "GET", URL: endpoint.String(), Headers: headers})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, fmt.Errorf("SearXNG Search API error (%d)%s: %s",
			res.Status, searxngHintForStatus(res.Status), res.Body)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var results []searchResult
	if rows, ok := arr(raw, "results"); ok {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				results = append(results, searchResult{
					Title: str(m, "title"), URL: str(m, "url"), Snippet: str(m, "content"),
				})
			}
		}
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	return searchResponseOf(query, results), nil
}

// searchOllama queries a local or cloud Ollama instance. upstream: providers/ollama.ts OllamaProvider.search.
func searchOllama(client httpDoer, baseURL, apiKey, query string, maxResults int, local bool) (searchResponse, error) {
	baseURL = stripTrailingSlashes(strings.TrimSpace(baseURL))
	if baseURL == "" {
		return searchResponse{}, missingKeyError(ollamaHostEnvVar)
	}
	path := ollamaCloudSearchPath
	if local {
		path = ollamaLocalSearchPath
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if strings.TrimSpace(apiKey) != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(apiKey)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     baseURL + path,
		Headers: headers,
		Body:    fmt.Sprintf(`{"query":%s,"max_results":%d}`, mustJSONString(query), maxResults),
	})
	if err != nil {
		return searchResponse{}, err
	}
	if !isOK(res.Status) {
		return searchResponse{}, fmt.Errorf("Ollama Search API error (%d): %s", res.Status, res.Body)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var results []searchResult
	if rows, ok := arr(raw, "results"); ok {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				results = append(results, searchResult{
					Title: str(m, "title"), URL: str(m, "url"), Snippet: str(m, "snippet"),
				})
			}
		}
	}
	return searchResponseOf(query, results), nil
}

// stripTrailingSlashes removes every trailing slash, so a base URL typed with one or five behaves the same. upstream:
// stripTrailingSlashes in providers/searxng.ts.
func stripTrailingSlashes(s string) string {
	for strings.HasSuffix(s, "/") {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}

// assertHTTPURL refuses anything that is not http or https. upstream: assertHttpUrl in providers/searxng.ts.
func assertHTTPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("Invalid URL: %s", raw)
	}
	switch parsed.Scheme {
	case "http", "https":
		return nil
	}
	return fmt.Errorf("Invalid URL scheme %q: only http and https are allowed", parsed.Scheme)
}

// mustJSONString encodes a Go string as a JSON string literal, which is what every request body needs. It cannot fail:
// encoding a string always succeeds.
func mustJSONString(v string) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// truncateRunes caps a string at n runes, the unit the original's slice uses for a JS string of code units only when
// the text is ASCII; for text with astral characters the original caps code units, so the port caps runes and records
// the difference in port/PORT.md.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
