// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// web_fetch's URL guard and the native fetch arms. upstream: web-tools.ts parseAndAssertHttpUrl and
// isPrivateOrLoopbackHostname, plus the fetch() half of providers/tavily.ts, exa.ts, jina.ts, firecrawl.ts and the
// generic path the other three delegate to.

// isPrivateOrLoopbackHostname refuses the addresses a public fetch must never reach: localhost, the IPv6 loopback,
// unspecified, link-local and unique-local ranges, and the IPv4 private blocks including the cloud metadata address.
// upstream: web-tools.ts isPrivateOrLoopbackHostname.
func isPrivateOrLoopbackHostname(hostname string) bool {
	h := strings.ToLower(hostname)
	// Strip the brackets an IPv6 literal carries in a URL authority.
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	// IPv6 loopback, unspecified, link-local and unique-local.
	if h == "::1" || h == "::" || strings.HasPrefix(h, "fe80:") ||
		strings.HasPrefix(h, "fc") || strings.HasPrefix(h, "fd") {
		return true
	}
	m := ipv4LiteralRegex.FindStringSubmatch(h)
	if m == nil {
		return false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	switch {
	case a == 0, a == 127, a == 10: // 0.0.0.0/8, loopback, RFC1918
		return true
	case a == 169 && b == 254: // link-local, including the 169.254.169.254 metadata address
		return true
	case a == 172 && b >= 16 && b <= 31: // RFC1918 172.16.0.0/12
		return true
	case a == 192 && b == 168: // RFC1918 192.168.0.0/16
		return true
	}
	return false
}

var ipv4LiteralRegex = regexp.MustCompile(`^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$`)

// supportedHTTPProtocols is the only two the fetch tool accepts. upstream: web-tools.ts SUPPORTED_HTTP_PROTOCOLS.
var supportedHTTPProtocols = map[string]bool{"http:": true, "https:": true}

// parseAndAssertHTTPURL is the fetch tool's entry guard: it must parse, must be http or https, and must not point at a
// private or loopback address. The protocol is checked before the host, so a file URL reports the protocol it was
// refused for rather than an unparseable-URL error. upstream: web-tools.ts parseAndAssertHttpUrl.
func parseAndAssertHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("Invalid URL: %s", raw)
	}
	// The URL standard requires a scheme for an absolute URL, so a bare word is unparseable rather than a URL with
	// an empty protocol: "not a url" reports as invalid, not as an unsupported protocol.
	if parsed.Scheme == "" {
		return nil, fmt.Errorf("Invalid URL: %s", raw)
	}
	if !supportedHTTPProtocols[parsed.Scheme+":"] {
		return nil, fmt.Errorf("Unsupported URL protocol: %s. Only http and https are supported.", parsed.Scheme+":")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("Invalid URL: %s", raw)
	}
	if isPrivateOrLoopbackHostname(parsed.Hostname()) {
		return nil, fmt.Errorf("Refusing to fetch private/loopback address: %s", parsed.Hostname())
	}
	return parsed, nil
}

// fetchAPIError is the non-2xx wrapper the native fetch arms share. upstream: the
// `${this.label} Fetch API error (${res.status}): ${text}` throw.
func fetchAPIError(label string, res httpResponse) error {
	return fmt.Errorf("%s Fetch API error (%d): %s", label, res.Status, res.Body)
}

// exaMaxFetchCharacters is the character budget the Exa contents endpoint is asked for. upstream: exa.ts
// EXA_MAX_FETCH_CHARACTERS.
const exaMaxFetchCharacters = 10000

// fetchTavily reads a URL through Tavily's extract endpoint. upstream: providers/tavily.ts TavilyProvider.fetch.
func fetchTavily(client httpDoer, apiKey, target string) (fetchResponse, error) {
	if apiKey == "" {
		return fetchResponse{}, missingKeyError(tavilyAPIKeyEnvVar)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     tavilyExtractAPIURL,
		Headers: map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + apiKey},
		// The Bearer header is the current Tavily form; search() still sends api_key in the body, which Tavily keeps
		// accepting.
		Body: `{"urls":[` + mustJSONString(target) + `]}`,
	})
	if err != nil {
		return fetchResponse{}, err
	}
	if !isOK(res.Status) {
		return fetchResponse{}, fetchAPIError("Tavily", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return fetchResponse{}, err
	}
	if failed, ok := arr(raw, "failed_results"); ok && len(failed) > 0 {
		entry, _ := failed[0].(map[string]any)
		failedURL := target
		if u := str(entry, "url"); u != "" {
			failedURL = u
		}
		reason := str(entry, "error")
		if reason == "" {
			reason = "unknown error"
		}
		return fetchResponse{}, fmt.Errorf("Tavily Fetch API error: extraction failed for %s: %s", failedURL, reason)
	}
	var results []fetchResponse
	if rows, ok := arr(raw, "results"); ok {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				out := fetchResponse{Text: str(m, "raw_content")}
				if out.Text == "" {
					out.Text = str(m, "content")
				}
				results = append(results, out)
			}
		}
	}
	if len(results) == 0 {
		return fetchResponse{}, fmt.Errorf("Tavily Fetch API error: no content returned for %s", target)
	}
	return results[0], nil
}

// fetchExa reads a URL through Exa's contents endpoint. upstream: providers/exa.ts ExaProvider.fetch.
func fetchExa(client httpDoer, apiKey, target string) (fetchResponse, error) {
	if apiKey == "" {
		return fetchResponse{}, missingKeyError(exaAPIKeyEnvVar)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     exaContentsAPIURL,
		Headers: map[string]string{"Content-Type": "application/json", "x-api-key": apiKey},
		Body: fmt.Sprintf(`{"ids":[%s],"text":{"maxCharacters":%d}}`,
			mustJSONString(target), exaMaxFetchCharacters),
	})
	if err != nil {
		return fetchResponse{}, err
	}
	if !isOK(res.Status) {
		return fetchResponse{}, fetchAPIError("Exa", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return fetchResponse{}, err
	}
	var result map[string]any
	if rows, ok := arr(raw, "results"); ok && len(rows) > 0 {
		result, _ = rows[0].(map[string]any)
	}
	if result == nil || str(result, "text") == "" {
		return fetchResponse{}, fmt.Errorf("Exa Fetch API error: no content returned for %s", target)
	}
	return fetchResponse{Text: str(result, "text"), Title: str(result, "title"), HasTitle: true}, nil
}

// fetchJina reads a URL through the Jina reader. upstream: providers/jina.ts JinaProvider.fetch.
func fetchJina(client httpDoer, apiKey, target string) (fetchResponse, error) {
	if apiKey == "" {
		return fetchResponse{}, missingKeyError(jinaAPIKeyEnvVar)
	}
	// No Accept header: the reader returns markdown by default, and asking for text/plain would contradict the
	// text/markdown content type reported below.
	res, err := client.Do(httpRequest{
		Method:  "GET",
		URL:     jinaReaderAPIURL + target,
		Headers: map[string]string{"Authorization": "Bearer " + apiKey},
	})
	if err != nil {
		return fetchResponse{}, err
	}
	if !isOK(res.Status) {
		return fetchResponse{}, fetchAPIError("Jina", res)
	}
	if strings.TrimSpace(res.Body) == "" {
		return fetchResponse{}, fmt.Errorf("Jina Fetch API error: no content returned for %s", target)
	}
	return fetchResponse{Text: res.Body, ContentType: "text/markdown", HasContentType: true}, nil
}

// fetchFirecrawl reads a URL through Firecrawl's scrape endpoint. upstream: providers/firecrawl.ts
// FirecrawlProvider.fetch.
func fetchFirecrawl(client httpDoer, apiKey, target string) (fetchResponse, error) {
	if apiKey == "" {
		return fetchResponse{}, missingKeyError(firecrawlAPIKeyEnvVar)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     firecrawlAPIURL + "/scrape",
		Headers: map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + apiKey},
		Body:    `{"url":` + mustJSONString(target) + `,"formats":["markdown"]}`,
	})
	if err != nil {
		return fetchResponse{}, err
	}
	if !isOK(res.Status) {
		return fetchResponse{}, fetchAPIError("Firecrawl", res)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return fetchResponse{}, err
	}
	if success, ok := raw["success"].(bool); !ok || !success {
		reason := str(raw, "error")
		if reason == "" {
			reason = "scrape failed"
		}
		return fetchResponse{}, fmt.Errorf("Firecrawl Fetch API error: %s", reason)
	}
	data, _ := obj(raw, "data")
	if data == nil || str(data, "markdown") == "" {
		return fetchResponse{}, fmt.Errorf("Firecrawl Fetch API error: no content returned for %s", target)
	}
	return fetchResponse{Text: str(data, "markdown"), ContentType: "text/markdown", HasContentType: true}, nil
}

// fetchViaProviderGeneric is the path for providers with no native fetch endpoint (Brave, Serper, SearXNG): the raw
// HTTP fetch, which never authenticates to the target and therefore never throws for a missing key. upstream:
// providers/fetch-helpers.ts fetchViaGenericHtml.
func fetchViaProviderGeneric(client httpDoer, target string, raw bool) (fetchResponse, error) {
	return fetchViaGenericHTML(client, target, raw)
}

// fetchHeader renders the model-facing header above a fetched body. upstream: web-tools.ts formatFetchHeader.
func fetchHeader(target, title, contentType string, hasTitle, hasContentType bool) string {
	lines := []string{"**Fetched:** " + target}
	if hasTitle {
		lines = append(lines, "**Title:** "+title)
	}
	if hasContentType {
		lines = append(lines, "**Content-Type:** "+contentType)
	}
	return strings.Join(lines, "\n") + "\n\n"
}

// searchResultsBody renders the model-facing search body. upstream: web-tools.ts formatSearchResultsBody.
func searchResultsBody(res searchResponse) string {
	text := "**Search results for \"" + res.Query + "\":**\n\n"
	for i, r := range res.Results {
		text += itoa(i+1) + ". **" + r.Title + "**\n   " + r.URL + "\n   " + r.Snippet + "\n\n"
	}
	return strings.TrimRight(text, "\n")
}

// emptyResultsEnvelope is the no-results answer: the model's text plus the details block. upstream: web-tools.ts
// buildEmptyResultsEnvelope.
func emptyResultsEnvelope(query, providerName string) (string, searchEnvelopeDetails) {
	return "No results found for \"" + query + "\".", searchEnvelopeDetails{Query: query, Backend: providerName, ResultCount: 0}
}

// searchEnvelopeDetails is the search tool's details block. upstream: the details object of the search response.
type searchEnvelopeDetails struct {
	Query       string
	Backend     string
	ResultCount int
}
