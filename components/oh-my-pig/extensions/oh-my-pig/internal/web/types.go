// Package web ports omp's web_search tool and URL fetcher.
//
// upstream: packages/coding-agent/src/web/search/, packages/coding-agent/src/tools/fetch.ts
package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Default timeout bounds for web-search providers.
// upstream: packages/coding-agent/src/web/search/types.ts:12-15
const (
	DefaultWebSearchTimeoutSeconds = 60
	MaxWebSearchTimeoutSeconds     = 300
)

// SearchProviderLabels maps provider ids to display labels.
// upstream: packages/tui/src/tools/web-search-types.ts:12-40
var SearchProviderLabels = map[string]string{
	"parallel":   "Parallel",
	"perplexity": "Perplexity",
	"gemini":     "Gemini",
	"anthropic":  "Anthropic",
	"codex":      "OpenAI Codex",
	"openai":     "OpenAI API",
	"xai":        "xAI",
	"openrouter": "OpenRouter",
	"zai":        "Z.AI",
	"exa":        "Exa",
	"tinyfish":   "TinyFish",
	"keenable":   "Keenable",
	"sofya":      "Sofya",
	"jina":       "Jina",
	"kagi":       "Kagi",
	"tavily":     "Tavily",
	"firecrawl":  "Firecrawl",
	"brave":      "Brave",
	"deepseek":   "DeepSeek",
	"kimi":       "Kimi",
	"synthetic":  "Synthetic",
	"ollama":     "Ollama",
	"searxng":    "SearXNG",
	"startpage":  "Startpage",
	"duckduckgo": "DuckDuckGo",
	"ecosia":     "Ecosia",
	"google":     "Google",
	"mojeek":     "Mojeek",
	"public":     "Public Web",
	"hackernews": "Hacker News",
}

// GetSearchProviderLabel returns the human-readable label for a provider id.
// upstream: packages/tui/src/tools/web-search-types.ts:46-48
func GetSearchProviderLabel(id string) string {
	if label, ok := SearchProviderLabels[id]; ok {
		return label
	}
	return id
}

// SearchSource is a single web result returned by any provider.
// upstream: packages/tui/src/tools/web-search-types.ts:51-60
type SearchSource struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Snippet       string `json:"snippet,omitempty"`
	PublishedDate string `json:"publishedDate,omitempty"`
	AgeSeconds    *int   `json:"ageSeconds,omitempty"`
	Author        string `json:"author,omitempty"`
}

// SearchCitation is a text citation returned by LLM-mediated search providers.
// upstream: packages/tui/src/tools/web-search-types.ts:63-67
type SearchCitation struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	CitedText string `json:"citedText,omitempty"`
}

// SearchUsage holds token/request metrics when returned by a provider.
// upstream: packages/tui/src/tools/web-search-types.ts:70-77
type SearchUsage struct {
	InputTokens    int `json:"inputTokens,omitempty"`
	OutputTokens   int `json:"outputTokens,omitempty"`
	SearchRequests int `json:"searchRequests,omitempty"`
	TotalTokens    int `json:"totalTokens,omitempty"`
}

// SearchResponse is the unified result across all web search providers.
// upstream: packages/tui/src/tools/web-search-types.ts:80-100
type SearchResponse struct {
	Provider         string           `json:"provider"`
	Answer           string           `json:"answer,omitempty"`
	Sources          []SearchSource   `json:"sources"`
	Citations        []SearchCitation `json:"citations,omitempty"`
	SearchQueries    []string         `json:"searchQueries,omitempty"`
	RelatedQuestions []string         `json:"relatedQuestions,omitempty"`
	Usage            *SearchUsage     `json:"usage,omitempty"`
	Model            string           `json:"model,omitempty"`
	RequestID        string           `json:"requestId,omitempty"`
	AuthMode         string           `json:"authMode,omitempty"`
}

// SearchFailureDetail records one provider that failed before another answered, or before the search gave up.
// upstream: packages/coding-agent/src/web/search/types.ts:7-15
type SearchFailureDetail struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
	Reason   string `json:"reason"`
	Status   int    `json:"status,omitempty"`
}

// SearchResultDetails is stored in ToolResult.Details for web_search calls.
// upstream: packages/coding-agent/src/web/search/types.ts:17-22
type SearchResultDetails struct {
	Response SearchResponse        `json:"response"`
	Error    string                `json:"error,omitempty"`
	Failures []SearchFailureDetail `json:"failures,omitempty"`
}

// SearchProviderError carries provider id and optional HTTP status code.
// upstream: packages/coding-agent/src/web/search/types.ts:21-30
type SearchProviderError struct {
	Provider string
	Message  string
	Status   int
}

func (e *SearchProviderError) Error() string {
	return e.Message
}

// SearchParams holds normalized inputs passed to a search provider.
// upstream: packages/coding-agent/src/web/search/providers/base.ts:17-83
type SearchParams struct {
	Query            string
	ParsedQuery      *StructuredQuery
	Limit            int
	Recency          string // "", "day", "week", "month", "year"
	SystemPrompt     string
	MaxOutputTokens  int
	NumSearchResults int
	Temperature      *float64
	Timeout          time.Duration
}

func (p SearchParams) requestedResults() int {
	if p.NumSearchResults > 0 {
		return p.NumSearchResults
	}
	return p.Limit
}

// SearchProvider is implemented by each ported web search backend.
// upstream: packages/coding-agent/src/web/search/providers/base.ts:86-118
type SearchProvider interface {
	ID() string
	Label() string
	IsAvailable() bool
	Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error)
}

// nowFunc allows deterministic time in unit tests.
var nowFunc = time.Now

// dateToAgeSeconds calculates age in seconds from a date string.
// upstream: packages/coding-agent/src/web/search/utils.ts:2-11
func dateToAgeSeconds(dateStr string) *int {
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" {
		return nil
	}
	t, ok := parseFlexibleTime(dateStr)
	if !ok {
		return nil
	}
	secs := int(math.Floor(nowFunc().Sub(t).Seconds()))
	return &secs
}

func parseFlexibleTime(s string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Jan 2, 2006",
		"January 2, 2006",
		"2 Jan 2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// clampNumResults clamps a result count to [1, maxVal], returning defaultVal when absent/non-positive.
// upstream: packages/coding-agent/src/web/search/utils.ts:14-17
func clampNumResults(value, defaultVal, maxVal int) int {
	if value <= 0 {
		return defaultVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}

var recencyDays = map[string]int{
	"day":   1,
	"week":  7,
	"month": 30,
	"year":  365,
}

// recencyAfterDate returns the UTC YYYY-MM-DD lower bound for a relative recency window.
// upstream: packages/coding-agent/src/web/search/utils.ts:27-29
func recencyAfterDate(recency string) string {
	days, ok := recencyDays[recency]
	if !ok {
		return ""
	}
	return nowFunc().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format("2006-01-02")
}

// formatAge formats age in seconds into a compact human-readable string.
// upstream: packages/utils/src/format.ts:106-122
func formatAge(ageSeconds *int) string {
	if ageSeconds == nil || *ageSeconds == 0 {
		return ""
	}
	secs := *ageSeconds
	mins := secs / 60
	hours := mins / 60
	days := hours / 24
	weeks := days / 7
	months := days / 30
	years := days / 365

	if years > 0 {
		return fmt.Sprintf("%dy ago", years)
	}
	if months > 0 {
		return fmt.Sprintf("%dmo ago", months)
	}
	if weeks > 0 {
		return fmt.Sprintf("%dw ago", weeks)
	}
	if days > 0 {
		return fmt.Sprintf("%dd ago", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh ago", hours)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm ago", mins)
	}
	return "just now"
}

// pluralize matches pi-utils pluralize.
// upstream: packages/utils/src/format.ts:121-126
func pluralize(label string, count int) string {
	if count == 1 {
		return label
	}
	lower := strings.ToLower(label)
	for _, suf := range []string{"ch", "sh", "s", "x", "z"} {
		if strings.HasSuffix(lower, suf) {
			return label + "es"
		}
	}
	if len(lower) >= 2 && strings.HasSuffix(lower, "y") {
		prev := lower[len(lower)-2]
		if prev != 'a' && prev != 'e' && prev != 'i' && prev != 'o' && prev != 'u' {
			return label[:len(label)-1] + "ies"
		}
	}
	return label + "s"
}

// formatCount formats a count with its pluralized label.
// upstream: packages/utils/src/format.ts:94-97
func formatCount(label string, count int) string {
	return fmt.Sprintf("%d %s", count, pluralize(label, count))
}

// truncateText truncates s to maxLen runes, appending ellipsis when cut.
// upstream: packages/utils/src/format.ts:85-89
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}

// formatForLLM formats a SearchResponse and leading notes for model consumption.
// upstream: packages/coding-agent/src/web/search/index.ts:60-111
func formatForLLM(response SearchResponse, notes []string) string {
	var parts []string
	for _, note := range notes {
		parts = append(parts, "Note: "+note)
	}

	if response.Answer != "" {
		parts = append(parts, response.Answer)
		if len(response.Sources) > 0 {
			parts = append(parts, "\n## Sources")
			parts = append(parts, formatCount("source", len(response.Sources)))
		}
	}

	for i, src := range response.Sources {
		age := formatAge(src.AgeSeconds)
		if age == "" {
			age = src.PublishedDate
		}
		agePart := ""
		if age != "" {
			agePart = " (" + age + ")"
		}
		parts = append(parts, fmt.Sprintf("[%d] %s%s\n    %s", i+1, src.Title, agePart, src.URL))
		if src.Snippet != "" {
			parts = append(parts, "    "+truncateText(src.Snippet, 240))
		}
	}

	if len(response.Citations) > 0 {
		parts = append(parts, "\n## Citations")
		parts = append(parts, formatCount("citation", len(response.Citations)))
		for i, c := range response.Citations {
			title := c.Title
			if title == "" {
				title = c.URL
			}
			parts = append(parts, fmt.Sprintf("[%d] %s\n    %s", i+1, title, c.URL))
			if c.CitedText != "" {
				parts = append(parts, "    "+truncateText(c.CitedText, 240))
			}
		}
	}

	if len(response.RelatedQuestions) > 0 {
		parts = append(parts, "\n## Related")
		parts = append(parts, formatCount("question", len(response.RelatedQuestions)))
		for _, q := range response.RelatedQuestions {
			parts = append(parts, "- "+q)
		}
	}

	if len(response.SearchQueries) > 0 {
		parts = append(parts, fmt.Sprintf("Search queries: %d", len(response.SearchQueries)))
		limit := len(response.SearchQueries)
		if limit > 3 {
			limit = 3
		}
		for _, q := range response.SearchQueries[:limit] {
			parts = append(parts, "- "+truncateText(q, 120))
		}
	}

	return strings.Join(parts, "\n")
}

// hasRenderableSearchContent reports whether the response has any content to render.
// upstream: packages/coding-agent/src/web/search/index.ts:113-120
func hasRenderableSearchContent(response SearchResponse) bool {
	if strings.TrimSpace(response.Answer) != "" {
		return true
	}
	if len(response.Sources) > 0 {
		return true
	}
	if len(response.Citations) > 0 {
		return true
	}
	for _, q := range response.RelatedQuestions {
		if strings.TrimSpace(q) != "" {
			return true
		}
	}
	for _, q := range response.SearchQueries {
		if strings.TrimSpace(q) != "" {
			return true
		}
	}
	return false
}

// providerFailure pairs a failed provider with its error for fallback summaries.
type providerFailure struct {
	id    string
	label string
	err   error
}

// formatSearchProviderFailure formats a single provider error.
// upstream: packages/coding-agent/src/web/search/provider.ts:63-78
func formatSearchProviderFailure(err error, id, label string) string {
	var spErr *SearchProviderError
	if errors.As(err, &spErr) {
		if spErr.Provider == "anthropic" && spErr.Status == 404 {
			return "Anthropic web search returned 404 (model or endpoint not found)."
		}
		if spErr.Status == 401 || spErr.Status == 403 {
			if spErr.Provider == "zai" {
				return spErr.Message
			}
			return fmt.Sprintf("%s authorization failed (%d). Check API key or base URL.", GetSearchProviderLabel(spErr.Provider), spErr.Status)
		}
		return spErr.Message
	}
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("Unknown error from %s", label)
}

// formatSearchProviderFailures formats multiple fallback failures joined with "; ".
// upstream: packages/coding-agent/src/web/search/provider.ts:81-85
func formatSearchProviderFailures(failures []providerFailure) string {
	parts := make([]string, len(failures))
	for i, f := range failures {
		parts[i] = fmt.Sprintf("%s: %s", f.id, formatSearchProviderFailure(f.err, f.id, f.label))
	}
	return strings.Join(parts, "; ")
}

var creditBodyPattern = regexp.MustCompile(`(?i)credits?\s*(?:exhausted|exceeded)|quota|insufficient`)

// classifyProviderHttpError maps known quota/auth HTTP failures into compact provider-tagged errors.
// upstream: packages/coding-agent/src/web/search/providers/utils.ts:107-127
func classifyProviderHttpError(provider string, status int, body string) *SearchProviderError {
	if creditBodyPattern.MatchString(body) {
		return &SearchProviderError{Provider: provider, Message: provider + ": credits exhausted", Status: status}
	}
	switch status {
	case 402:
		return &SearchProviderError{Provider: provider, Message: provider + ": 402 credits exhausted", Status: status}
	case 401:
		return &SearchProviderError{Provider: provider, Message: provider + ": 401 unauthorized", Status: status}
	case 403:
		return &SearchProviderError{Provider: provider, Message: provider + ": 403 forbidden", Status: status}
	}
	return nil
}

var whitespaceRunRe = regexp.MustCompile(`\s+`)

// normalizeSearchText collapses whitespace runs and trims a string.
// upstream: packages/coding-agent/src/web/search/providers/utils.ts:134-138
func normalizeSearchText(value string) string {
	return strings.TrimSpace(whitespaceRunRe.ReplaceAllString(value, " "))
}

// siteHosts returns deduplicated bare hosts from normalized site: values.
// upstream: packages/coding-agent/src/web/search/providers/utils.ts:144-151
func siteHosts(sites []string) []string {
	seen := make(map[string]struct{}, len(sites))
	var hosts []string
	for _, s := range sites {
		host := s
		if idx := strings.IndexByte(s, '/'); idx >= 0 {
			host = s[:idx]
		}
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	return hosts
}

var (
	charsetHeaderRe = regexp.MustCompile(`(?i)charset\s*=\s*"?([\w-]+)"?`)
	metaCharsetRe   = regexp.MustCompile(`(?i)<meta[^>]+charset\s*=\s*["']?([\w-]+)`)
)

// readBodyCapped reads up to maxBytes from r, reporting whether the stream exceeded maxBytes.
// upstream: packages/coding-agent/src/web/response-body.ts:33-72
func readBodyCapped(r io.Reader, maxBytes int64) ([]byte, bool, error) {
	if r == nil {
		return nil, false, nil
	}
	limited := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > maxBytes {
		return data[:maxBytes], true, nil
	}
	return data, false, nil
}

// decodeBody decodes response bytes honoring UTF-8 BOM stripping and ISO-8859-1/Windows-1252
// declared in Content-Type or HTML <meta charset>, falling back to UTF-8.
// upstream: packages/coding-agent/src/web/response-body.ts:6-26
func decodeBody(data []byte, contentTypeHeader string) string {
	// Strip UTF-8 BOM so JSON decoders do not reject it.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	label := ""
	if m := charsetHeaderRe.FindStringSubmatch(contentTypeHeader); len(m) > 1 {
		label = strings.ToLower(m[1])
	}
	if label == "" && (strings.TrimSpace(contentTypeHeader) == "" || strings.Contains(strings.ToLower(contentTypeHeader), "html")) {
		prefix := data
		if len(prefix) > 2048 {
			prefix = prefix[:2048]
		}
		if m := metaCharsetRe.FindStringSubmatch(string(prefix)); len(m) > 1 {
			label = strings.ToLower(m[1])
		}
	}
	switch label {
	case "windows-1252", "cp1252", "iso-8859-1", "latin1", "latin-1":
		return decodeWindows1252(data)
	}
	if utf8.Valid(data) {
		return string(data)
	}
	return strings.ToValidUTF8(string(data), "\uFFFD")
}

// windows1252Table maps 0x80..0x9F to their Unicode code points per WHATWG encoding standard.
var windows1252Table = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

func decodeWindows1252(data []byte) string {
	var b strings.Builder
	b.Grow(len(data))
	for _, c := range data {
		if c >= 0x80 && c <= 0x9F {
			b.WriteRune(windows1252Table[c-0x80])
		} else {
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}

// readLimitedText reads a provider response body up to maxBytes, truncating or erroring if exceeded.
// upstream: packages/coding-agent/src/web/search/providers/utils.ts:157-169
func readLimitedText(resp *http.Response, provider string, maxBytes int64, allowTruncate bool) (string, error) {
	if resp == nil || resp.Body == nil {
		return "", nil
	}
	data, truncated, err := readBodyCapped(resp.Body, maxBytes)
	if err != nil {
		return "", err
	}
	if truncated && !allowTruncate {
		return "", &SearchProviderError{
			Provider: provider,
			Message:  fmt.Sprintf("%s API response exceeded 2 MiB", GetSearchProviderLabel(provider)),
			Status:   500,
		}
	}
	return decodeBody(data, resp.Header.Get("Content-Type")), nil
}

// formatBytes matches pi-tui formatBytes for binary and truncation notices.
func formatBytes(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	val := float64(n)
	for _, u := range units {
		val /= 1024
		if val < 1024 || u == "TB" {
			if val == math.Trunc(val) {
				return fmt.Sprintf("%.0f %s", val, u)
			}
			return fmt.Sprintf("%.1f %s", val, u)
		}
	}
	return fmt.Sprintf("%d B", n)
}
