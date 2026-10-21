package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Port of parallel-mcp.ts: the Parallel MCP server (anonymous, with an optional Bearer key) as a
// search provider and as an explicit hosted fetch provider.

const (
	parallelMCPURL     = "https://search.parallel.ai/mcp"
	parallelMCPTimeout = 60 * time.Second
)

// IsParallelMCPAvailable is always true: the server needs no credential.
func IsParallelMCPAvailable() bool { return true }

type parallelMCPResult struct {
	URL, Title string
	Excerpts   []string
	FullText   string
}

func parallelMCPQuery(query string, o SearchOptions) string {
	parts := []string{query}
	for _, raw := range o.DomainFilter {
		if d := NormalizeDomain(raw); d != "" {
			if strings.HasPrefix(jsTrim(raw), "-") {
				parts = append(parts, "-site:"+d)
			} else {
				parts = append(parts, "site:"+d)
			}
		}
	}
	if o.RecencyFilter != "" {
		labels := map[string]string{"day": "past 24 hours", "week": "past week", "month": "past month", "year": "past year"}
		parts = append(parts, labels[o.RecencyFilter])
	}
	return strings.Join(parts, " ")
}

type mcpEnvelope struct {
	Result *struct {
		Content           []struct{ Type, Text string } `json:"content"`
		StructuredContent json.RawMessage               `json:"structuredContent"`
		IsError           bool                          `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    *float64 `json:"code"`
		Message string   `json:"message"`
	} `json:"error"`
}

// callParallelMCP is callParallelMcp: one JSON-RPC tools/call, answered as JSON or an event stream.
func callParallelMCP(ctx context.Context, tool string, args map[string]any) (any, error) {
	key, err := resolveParallelAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"}}
	if key != "" {
		h.Set("Authorization", "Bearer "+key)
	}
	body := jsonBytes(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	status, text, err := hostedSend(ctx, hostedCall{URL: parallelMCPURL, Body: body, Header: h, Key: key, Timeout: parallelMCPTimeout, Credentials: []string{"Authorization"}})
	if err != nil {
		return nil, err
	}
	safe := RedactCredential(text, key)
	if key == "" {
		safe = text
	}
	if status < 200 || status > 299 {
		if status == 429 {
			return nil, fmt.Errorf("Parallel MCP rate limit reached (429). Add parallelApiKey to %s or set PARALLEL_API_KEY for higher limits: %s", ConfigPath(), jsSlice(safe, 0, 200))
		}
		return nil, fmt.Errorf("Parallel MCP error %d: %s", status, jsSlice(safe, 0, 300))
	}
	var candidates []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "data:") {
			candidates = append(candidates, jsTrim(line[5:]))
		}
	}
	candidates = append(candidates, text)
	var parsed *mcpEnvelope
	for _, c := range candidates {
		if c == "" {
			continue
		}
		var env mcpEnvelope
		if json.Unmarshal([]byte(c), &env) == nil && (env.Result != nil || env.Error != nil) {
			parsed = &env
			break
		}
	}
	if parsed == nil {
		return nil, errors.New("Parallel MCP returned invalid JSON-RPC content")
	}
	redact := func(s string) string {
		if key == "" {
			return s
		}
		return RedactCredential(s, key)
	}
	if parsed.Error != nil {
		code := ""
		if parsed.Error.Code != nil {
			code = " " + jsNumber(*parsed.Error.Code)
		}
		msg := parsed.Error.Message
		if msg == "" {
			msg = "Unknown error"
		}
		return nil, fmt.Errorf("Parallel MCP error%s: %s", code, redact(msg))
	}
	if parsed.Result.IsError {
		msg := ""
		for _, c := range parsed.Result.Content {
			if c.Type == "text" {
				msg = jsTrim(c.Text)
				break
			}
		}
		if msg == "" {
			msg = "Parallel MCP returned an error"
		}
		return nil, errors.New(redact(msg))
	}
	if sc := parsed.Result.StructuredContent; len(sc) > 0 && (sc[0] == '{' || sc[0] == '[') {
		var v any
		if json.Unmarshal(sc, &v) == nil {
			return v, nil
		}
	}
	for _, c := range parsed.Result.Content {
		if c.Type == "text" && jsTrim(c.Text) != "" {
			var v any
			if json.Unmarshal([]byte(c.Text), &v) == nil {
				return v, nil
			}
			return c.Text, nil
		}
	}
	return nil, errors.New("Parallel MCP returned empty content")
}

var (
	pmcpURLRE   = regexp.MustCompile(`(?m)^URL: ([^\n\r\x{2028}\x{2029}]+)`)
	pmcpTitleRE = regexp.MustCompile(`(?m)^Title: ([^\n\r\x{2028}\x{2029}]+)`)
	mcpTitleAt  = regexp.MustCompile(`(?m)^Title: `)
	mcpTailRE   = regexp.MustCompile(`\n---\s*$`)
)

// parallelMCPTextResults is parseTextResults: blocks that start at each "Title: " line.
func parallelMCPTextResults(text string) []parallelMCPResult {
	var starts []int
	for _, m := range mcpTitleAt.FindAllStringIndex(text, -1) {
		if m[0] > 0 {
			starts = append(starts, m[0])
		}
	}
	var blocks []string
	prev := 0
	for _, s := range starts {
		blocks = append(blocks, text[prev:s])
		prev = s
	}
	blocks = append(blocks, text[prev:])
	var out []parallelMCPResult
	for _, b := range blocks {
		m := pmcpURLRE.FindStringSubmatch(b)
		if m == nil || jsTrim(m[1]) == "" {
			continue
		}
		r := parallelMCPResult{URL: jsTrim(m[1])}
		if t := pmcpTitleRE.FindStringSubmatch(b); t != nil {
			r.Title = jsTrim(t[1])
		}
		if i := strings.Index(b, "\nText: "); i >= 0 {
			if ex := jsTrim(mcpTailRE.ReplaceAllString(b[i+7:], "")); ex != "" {
				r.Excerpts = []string{ex}
			}
		}
		out = append(out, r)
	}
	return out
}

func parallelMCPResults(payload any) []parallelMCPResult {
	if s, ok := payload.(string); ok {
		return parallelMCPTextResults(s)
	}
	items, _ := asMap(payload)["results"].([]any)
	var out []parallelMCPResult
	for _, it := range items {
		m := asMap(it)
		u, ok := strField(m, "url")
		if !ok {
			continue
		}
		t, _ := strField(m, "title")
		full, _ := strField(m, "full_content")
		var ex []string
		for _, e := range asList(m["excerpts"]) {
			if s, ok := e.(string); ok && jsTrim(s) != "" {
				ex = append(ex, s)
			}
		}
		out = append(out, parallelMCPResult{URL: u, Title: t, Excerpts: ex, FullText: full})
	}
	return out
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// SearchWithParallelMCP searches through the Parallel MCP server.
func SearchWithParallelMCP(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	act := logAPIStart(query)
	return logged(act, "", func() (*SearchResponse, error) {
		payload, err := callParallelMCP(ctx, "web_search", map[string]any{"objective": query, "search_queries": []string{parallelMCPQuery(query, o)}})
		if err != nil {
			return nil, err
		}
		max := parallelCount(o.NumResults)
		f := normalizeDomainFilters(o.DomainFilter)
		var results []parallelMCPResult
		for _, r := range parallelMCPResults(payload) {
			if f.matches(r.URL) {
				results = append(results, r)
			}
		}
		if math.IsNaN(max) {
			results = nil
		} else if int(max) < len(results) {
			results = results[:int(max)]
		}
		resp := &SearchResponse{Results: []SearchResult{}}
		var answer []string
		var inline []ExtractedContent
		for i, r := range results {
			title := r.Title
			if title == "" {
				title = fmt.Sprintf("Source %d", i+1)
			}
			snippet := ""
			if len(r.Excerpts) > 0 {
				snippet = jsSlice(collapseWS(r.Excerpts[0]), 0, 200)
			}
			resp.Results = append(resp.Results, SearchResult{Title: title, URL: r.URL, Snippet: snippet})
			if c := jsTrim(strings.Join(r.Excerpts, " ")); c != "" {
				answer = append(answer, c+"\nSource: "+title+" ("+r.URL+")")
			}
			if c := strings.Join(r.Excerpts, "\n\n"); c != "" {
				inline = append(inline, ExtractedContent{URL: r.URL, Title: r.Title, Content: c})
			}
		}
		resp.Answer = strings.Join(answer, "\n\n")
		if o.IncludeContent && len(inline) > 0 {
			resp.InlineContent = inline
		}
		return resp, nil
	})
}

// ExtractWithParallelMCP fetches one URL through the MCP server's web_fetch tool.
func ExtractWithParallelMCP(ctx context.Context, rawURL string, o ExtractOptions) (*ExtractedContent, error) {
	act := logFetchStart(rawURL)
	return logged(act, "", func() (*ExtractedContent, error) {
		args := map[string]any{"urls": []string{rawURL}, "full_content": true}
		if obj := jsTrim(o.Prompt); obj != "" {
			args["objective"] = jsSlice(obj, 0, 200)
		}
		payload, err := callParallelMCP(ctx, "web_fetch", args)
		if err != nil {
			return nil, err
		}
		results := parallelMCPResults(payload)
		var result *parallelMCPResult
		for i := range results {
			if results[i].URL == rawURL {
				result = &results[i]
				break
			}
		}
		if result == nil && len(results) > 0 {
			result = &results[0]
		}
		if result == nil {
			return nil, nil
		}
		content := jsTrim(result.FullText)
		if content == "" {
			content = strings.Join(result.Excerpts, "\n\n")
		}
		if content == "" {
			return nil, nil
		}
		return &ExtractedContent{URL: result.URL, Title: result.Title, Content: content}, nil
	})
}
