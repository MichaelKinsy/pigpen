package web

// Keyless search providers ported from omp v19.1.1:
//   - Parallel public MCP (https://search.parallel.ai/mcp, stateless JSON-RPC 2.0 over HTTPS, JSON + SSE)
//   - DuckDuckGo HTML frontend (https://html.duckduckgo.com/html/)
//
// upstream:
//   packages/coding-agent/src/mcp/json-rpc.ts
//   packages/coding-agent/src/web/search/providers/parallel.ts:73-143
//   packages/coding-agent/src/web/search/providers/duckduckgo.ts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
)

var (
	parallelMCPURL    = "https://search.parallel.ai/mcp"
	duckduckgoHTMLURL = "https://html.duckduckgo.com/html/"
	mcpReqCounter     atomic.Uint64
)

var parallelMCPQuerySyntax = QuerySyntax{
	Phrases:   true,
	Negation:  true,
	Or:        true,
	Site:      true,
	DateRange: true,
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func jsonRPCIDMatches(raw json.RawMessage, want string) bool {
	if len(raw) == 0 {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s == want
	}
	return strings.TrimSpace(string(raw)) == want
}

func selectJSONRPCMessage(msgBytes []byte, expectedID string) (*jsonRPCResponse, bool, error) {
	var r jsonRPCResponse
	if err := json.Unmarshal(msgBytes, &r); err != nil {
		return nil, false, err
	}
	if r.JSONRPC != "2.0" {
		return nil, false, fmt.Errorf("malformed JSON-RPC message")
	}
	// Skip server notifications/requests that have neither result nor error.
	if len(r.Result) == 0 && r.Error == nil {
		return nil, false, nil
	}
	if jsonRPCIDMatches(r.ID, expectedID) {
		return &r, true, nil
	}
	return nil, false, nil
}

func selectJSONRPCPayload(data []byte, expectedID string) (*jsonRPCResponse, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, err
		}
		for _, item := range arr {
			if r, ok, err := selectJSONRPCMessage(item, expectedID); err != nil {
				return nil, err
			} else if ok {
				return r, nil
			}
		}
		return nil, nil
	}
	r, _, err := selectJSONRPCMessage(trimmed, expectedID)
	return r, err
}

// readMCPSSEResponse streams SSE events and returns immediately upon the first matching JSON-RPC reply.
// upstream: packages/coding-agent/src/mcp/json-rpc.ts:96-107
func readMCPSSEResponse(body io.Reader, expectedID string, maxBytes int64) (*jsonRPCResponse, error) {
	limited := io.LimitReader(body, maxBytes+1)
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64*1024), int(maxBytes))
	var dataLines []string
	flush := func() (*jsonRPCResponse, bool, error) {
		if len(dataLines) == 0 {
			return nil, false, nil
		}
		joined := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if joined == "" || joined == "[DONE]" {
			return nil, joined == "[DONE]", nil
		}
		r, err := selectJSONRPCPayload([]byte(joined), expectedID)
		return r, false, err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			r, done, err := flush()
			if err != nil {
				return nil, err
			}
			if r != nil {
				return r, nil
			}
			if done {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if r, _, err := flush(); err != nil {
		return nil, err
	} else if r != nil {
		return r, nil
	}
	return nil, fmt.Errorf("MCP SSE response did not include a matching result")
}

// searchParallelPublicMCP calls Parallel's keyless public MCP endpoint.
// upstream: packages/coding-agent/src/web/search/providers/parallel.ts:73-143
func searchParallelPublicMCP(
	ctx context.Context,
	client *http.Client,
	objective string,
	mcpQuery string,
	numResults int,
) (SearchResponse, error) {
	reqID := fmt.Sprintf("omp-%d", mcpReqCounter.Add(1))
	rpcBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "web_search",
			"arguments": map[string]any{
				"objective":      objective,
				"search_queries": []string{mcpQuery},
			},
		},
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json, text/event-stream",
		"User-Agent":   fetchUserAgents[0],
	}
	resp, err := doJSONRequest(ctx, client, http.MethodPost, parallelMCPURL, headers, rpcBody)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "parallel", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("parallel", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return SearchResponse{}, &SearchProviderError{
				Provider: "parallel",
				Message:  "parallel: MCP rate limit reached (429); configure a Parallel API key for higher limits",
				Status:   http.StatusTooManyRequests,
			}
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "parallel",
			Message:  fmt.Sprintf("Parallel MCP request failed (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	var rpcResp *jsonRPCResponse
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		rpcResp, err = readMCPSSEResponse(resp.Body, reqID, maxProviderRespBytes)
	} else {
		var raw string
		raw, err = readLimitedText(resp, "parallel", maxProviderRespBytes, false)
		if err != nil {
			return SearchResponse{}, err
		}
		rpcResp, err = selectJSONRPCPayload([]byte(raw), reqID)
		if err == nil && rpcResp == nil {
			err = fmt.Errorf("MCP response did not include a matching result")
		}
	}
	if err != nil {
		return SearchResponse{}, &SearchProviderError{
			Provider: "parallel",
			Message:  "Failed to parse Parallel MCP response",
		}
	}
	if rpcResp.Error != nil {
		return SearchResponse{}, &SearchProviderError{
			Provider: "parallel",
			Message:  "Parallel MCP error: " + rpcResp.Error.Message,
		}
	}
	var toolResult struct {
		StructuredContent json.RawMessage `json:"structuredContent"`
		Content           []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(rpcResp.Result, &toolResult); err != nil {
		return SearchResponse{}, &SearchProviderError{
			Provider: "parallel",
			Message:  "Parallel MCP search returned an unexpected response shape.",
		}
	}
	if toolResult.IsError {
		msg := "Parallel MCP returned an error"
		for _, item := range toolResult.Content {
			if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
				msg = strings.TrimSpace(item.Text)
				break
			}
		}
		return SearchResponse{}, &SearchProviderError{Provider: "parallel", Message: msg}
	}

	parsePayload := func(data []byte) (SearchResponse, bool) {
		var payload struct {
			SearchID string `json:"search_id"`
			Results  []struct {
				Title       string   `json:"title"`
				URL         string   `json:"url"`
				PublishDate string   `json:"publish_date"`
				Excerpts    []string `json:"excerpts"`
			} `json:"results"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return SearchResponse{}, false
		}
		sources := make([]SearchSource, 0, len(payload.Results))
		for _, r := range payload.Results {
			if r.URL == "" {
				continue
			}
			title := r.Title
			if title == "" {
				title = r.URL
			}
			sources = append(sources, SearchSource{
				Title:         title,
				URL:           r.URL,
				Snippet:       strings.Join(r.Excerpts, "\n\n"),
				PublishedDate: r.PublishDate,
				AgeSeconds:    dateToAgeSeconds(r.PublishDate),
			})
			if len(sources) >= numResults {
				break
			}
		}
		return SearchResponse{
			Provider:  "parallel",
			Sources:   sources,
			RequestID: payload.SearchID,
			AuthMode:  "keyless",
		}, true
	}

	if len(bytes.TrimSpace(toolResult.StructuredContent)) > 0 && string(bytes.TrimSpace(toolResult.StructuredContent)) != "null" {
		if res, ok := parsePayload(toolResult.StructuredContent); ok {
			return res, nil
		}
	}
	for _, item := range toolResult.Content {
		if item.Type != "text" || strings.TrimSpace(item.Text) == "" {
			continue
		}
		if res, ok := parsePayload([]byte(item.Text)); ok {
			return res, nil
		}
	}

	return SearchResponse{Provider: "parallel", Sources: []SearchSource{}, AuthMode: "keyless"}, nil
}

// =============================================================================
// DuckDuckGo HTML Search Provider (keyless)
// upstream: packages/coding-agent/src/web/search/providers/duckduckgo.ts
// =============================================================================

type duckDuckGoProvider struct{}

func (duckDuckGoProvider) ID() string        { return "duckduckgo" }
func (duckDuckGoProvider) Label() string     { return "DuckDuckGo" }
func (duckDuckGoProvider) IsAvailable() bool { return true }
func (duckDuckGoProvider) IsKeyless() bool   { return true }

var ddgQuerySyntax = QuerySyntax{
	Phrases:  true,
	Negation: true,
	Or:       true,
	Site:     true,
	InURL:    true,
	InTitle:  true,
	InText:   true,
	Filetype: true,
}

var ddgRecencyDF = map[string]string{
	"day":   "d",
	"week":  "w",
	"month": "m",
	"year":  "y",
}

var (
	ddgBlockSplitRe = regexp.MustCompile(`(?i)<div\b[^>]*\bclass="[^"]*\b(?:result|nav-link)\b[^"]*"[^>]*>`)
	ddgTitleRe      = regexp.MustCompile(`(?is)<a\b[^>]*\bclass="[^"]*\bresult__a\b[^"]*"[^>]*\bhref="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRe    = regexp.MustCompile(`(?is)<(?:a|div|span)\b[^>]*\bclass="[^"]*\bresult__snippet\b[^"]*"[^>]*>(.*?)</(?:a|div|span)>`)
	ddgExtrasURLRe  = regexp.MustCompile(`(?is)<div\b[^>]*\bclass="[^"]*\bresult__extras__url\b[^"]*"[^>]*>(.*?)</div>`)
	ddgSpanRe       = regexp.MustCompile(`(?is)<span\b[^>]*>(.*?)</span>`)
	ddgDateRe       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}|$)`)
	ddgAdRe         = regexp.MustCompile(`(?i)^(?:https?:)?//(?:www\.)?duckduckgo\.com/y\.js(?:[?#]|$)`)
	ddgUddgRe       = regexp.MustCompile(`[?&]uddg=([^&]+)`)
	ddgTagStripRe   = regexp.MustCompile(`<[^>]*>`)
	ddgFormRe       = regexp.MustCompile(`(?is)<form\b[^>]*>(.*?)</form>`)
	ddgInputRe      = regexp.MustCompile(`(?i)<input\b[^>]*>`)
	ddgAttrNameRe   = regexp.MustCompile(`(?i)\bname\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	ddgAttrValRe    = regexp.MustCompile(`(?i)\bvalue\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

func ddgDecodeHTMLText(v string) string {
	s := ddgTagStripRe.ReplaceAllString(v, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.TrimSpace(wsCollapseRe.ReplaceAllString(s, " "))
}

func ddgUnwrapURL(href string) string {
	if href == "" {
		return ""
	}
	decoded := strings.ReplaceAll(href, "&amp;", "&")
	if ddgAdRe.MatchString(decoded) {
		return ""
	}
	if m := ddgUddgRe.FindStringSubmatch(decoded); m != nil {
		if u, err := url.QueryUnescape(m[1]); err == nil {
			return u
		}
		return ""
	}
	if strings.HasPrefix(decoded, "//") {
		return "https:" + decoded
	}
	if strings.HasPrefix(decoded, "http://") || strings.HasPrefix(decoded, "https://") {
		return decoded
	}
	return ""
}

func ddgExtractDate(block string) string {
	em := ddgExtrasURLRe.FindStringSubmatch(block)
	if em == nil {
		return ""
	}
	for _, sm := range ddgSpanRe.FindAllStringSubmatch(em[1], -1) {
		text := ddgDecodeHTMLText(sm[1])
		if ddgDateRe.MatchString(text) {
			return text
		}
	}
	return ""
}

func ddgParseHTMLResults(rawHTML string) []SearchSource {
	idxs := ddgBlockSplitRe.FindAllStringIndex(rawHTML, -1)
	if len(idxs) == 0 {
		return nil
	}
	var out []SearchSource
	for i, loc := range idxs {
		tag := rawHTML[loc[0]:loc[1]]
		if strings.Contains(strings.ToLower(tag), "nav-link") {
			continue
		}
		end := len(rawHTML)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		block := rawHTML[loc[1]:end]
		tm := ddgTitleRe.FindStringSubmatch(block)
		if tm == nil {
			continue
		}
		targetURL := ddgUnwrapURL(tm[1])
		if targetURL == "" {
			continue
		}
		title := ddgDecodeHTMLText(tm[2])
		if title == "" {
			continue
		}
		snippet := ""
		if sm := ddgSnippetRe.FindStringSubmatch(block); sm != nil {
			snippet = ddgDecodeHTMLText(sm[1])
		}
		pubDate := ddgExtractDate(block)
		out = append(out, SearchSource{
			Title:         title,
			URL:           targetURL,
			Snippet:       snippet,
			PublishedDate: pubDate,
			AgeSeconds:    dateToAgeSeconds(pubDate),
		})
	}
	return out
}

func ddgParseContinuationForm(rawHTML string) url.Values {
	for _, fm := range ddgFormRe.FindAllStringSubmatch(rawHTML, -1) {
		form := url.Values{}
		for _, inp := range ddgInputRe.FindAllString(fm[1], -1) {
			nm := ddgAttrNameRe.FindStringSubmatch(inp)
			vm := ddgAttrValRe.FindStringSubmatch(inp)
			if nm == nil || vm == nil {
				continue
			}
			name := nm[1]
			if name == "" {
				name = nm[2]
			}
			val := vm[1]
			if val == "" {
				val = vm[2]
			}
			if name != "" {
				form.Add(ddgDecodeHTMLText(name), ddgDecodeHTMLText(val))
			}
		}
		if form.Has("s") && form.Has("vqd") {
			return form
		}
	}
	return nil
}

func (duckDuckGoProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	numResults := clampNumResults(params.requestedResults(), 10, 20)
	parsed := parsedOrParse(params)

	form := url.Values{}
	form.Set("q", FormatScraperQuery(params.Query, &parsed, ddgQuerySyntax))
	form.Set("kl", "us-en")
	if df, ok := ddgRecencyDF[params.Recency]; ok {
		form.Set("df", df)
	}
	form.Set("b", "")

	reqCtx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	sources := make([]SearchSource, 0, numResults)
	seen := map[string]bool{}

	for form != nil && len(sources) < numResults {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, duckduckgoHTMLURL, strings.NewReader(form.Encode()))
		if err != nil {
			return SearchResponse{}, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Referer", "https://html.duckduckgo.com/")
		req.Header.Set("User-Agent", fetchUserAgents[2])
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		resp, err := client.Do(req)
		if err != nil {
			return SearchResponse{}, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxProviderRespBytes))
		resp.Body.Close()
		if readErr != nil {
			return SearchResponse{}, readErr
		}
		body := string(raw)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if classified := classifyProviderHttpError("duckduckgo", resp.StatusCode, body); classified != nil {
				return SearchResponse{}, classified
			}
			return SearchResponse{}, &SearchProviderError{
				Provider: "duckduckgo",
				Message:  fmt.Sprintf("DuckDuckGo HTML error (%d)", resp.StatusCode),
				Status:   resp.StatusCode,
			}
		}
		if strings.Contains(body, "anomaly-modal") || strings.Contains(body, "anomaly.js") {
			return SearchResponse{}, &SearchProviderError{
				Provider: "duckduckgo",
				Message:  "DuckDuckGo blocked the request with a bot-detection challenge. DuckDuckGo throttles automated HTML searches from datacenter/shared-egress IPs; configure a credentialed provider such as Brave, Tavily, Exa, or Kagi for reliable web search.",
				Status:   http.StatusTooManyRequests,
			}
		}

		before := len(sources)
		for _, src := range ddgParseHTMLResults(body) {
			if seen[src.URL] {
				continue
			}
			seen[src.URL] = true
			sources = append(sources, src)
			if len(sources) >= numResults {
				break
			}
		}
		if len(sources) == before {
			break
		}
		form = ddgParseContinuationForm(body)
	}

	return SearchResponse{
		Provider: "duckduckgo",
		Sources:  sources,
	}, nil
}
