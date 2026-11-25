package web

// Additional v19.1.1 search providers: Keenable, Sofya, and Hacker News (Algolia).
// upstream: packages/coding-agent/src/web/search/providers/{keenable,sofya,hackernews}.ts

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	keenableSearchURL       = "https://api.keenable.ai/v1/search"
	keenablePublicSearchURL = "https://api.keenable.ai/v1/search/public"
	sofyaSearchURL          = "https://sofya.co/v1/search"
	hnSearchURL             = "https://hn.algolia.com/api/v1/search"
)

const (
	keenableAppTitle = "oh-my-pi"
	hnItemURLPrefix  = "https://news.ycombinator.com/item?id="
)

// normalizeHttpURL validates and normalizes an http(s) URL, returning "" otherwise.
// upstream: packages/coding-agent/src/web/search/providers/utils.ts
func normalizeHttpURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return u.String()
}

// keywordQueryText renders plain terms for search APIs that only accept keywords/phrases/negation,
// returning a 400 SearchProviderError for date-only queries.
// upstream: packages/coding-agent/src/web/search/providers/utils.ts:184-192
func keywordQueryText(provider string, parsed StructuredQuery, syntax QuerySyntax) (string, error) {
	var q string
	if parsed.HasDirectives {
		p := parsed
		p.Raw = ""
		q = FormatQuery(p, syntax)
	} else {
		q = strings.TrimSpace(parsed.Raw)
	}
	if q != "" {
		return q, nil
	}
	return "", &SearchProviderError{
		Provider: provider,
		Message:  fmt.Sprintf("%s needs search terms; date-only queries are not supported", GetSearchProviderLabel(provider)),
		Status:   400,
	}
}

// =============================================================================
// Keenable (KEENABLE_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/keenable.ts
// =============================================================================

type keenableProvider struct{}

func (keenableProvider) ID() string        { return "keenable" }
func (keenableProvider) Label() string     { return "Keenable" }
func (keenableProvider) IsAvailable() bool { return envTrimmed("KEENABLE_API_KEY") != "" }
func (keenableProvider) IsKeyless() bool   { return envTrimmed("KEENABLE_API_KEY") == "" }

func (keenableProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	parsed := parsedOrParse(params)
	maxResults := clampNumResults(params.requestedResults(), 10, 50)
	apiKey := envTrimmed("KEENABLE_API_KEY")

	qText, err := keywordQueryText("keenable", parsed, QuerySyntax{Phrases: true})
	if err != nil {
		return SearchResponse{}, err
	}
	body := map[string]any{
		"query":       qText,
		"max_results": maxResults,
	}
	if sites := siteHosts(parsed.Sites); len(sites) == 1 {
		body["site"] = sites[0]
	}
	after := parsed.After
	if after == "" && params.Recency != "" {
		switch params.Recency {
		case "day":
			after = "1d"
		case "week":
			after = "7d"
		case "month":
			after = "1mo"
		case "year":
			after = "1y"
		}
	}
	if after != "" {
		body["published_after"] = after
	}
	if parsed.Before != "" {
		body["published_before"] = parsed.Before
	}

	headers := map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}
	endpoint := keenablePublicSearchURL
	authMode := "anonymous"
	if apiKey != "" {
		endpoint = keenableSearchURL
		headers["X-API-Key"] = apiKey
		authMode = "api_key"
	} else {
		headers["X-Keenable-Title"] = keenableAppTitle
	}

	reqCtx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(reqCtx, client, http.MethodPost, endpoint, headers, body)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "keenable", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("keenable", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "keenable",
			Message:  fmt.Sprintf("Keenable search API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "keenable", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}

	var rawObj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &rawObj); err != nil {
		return SearchResponse{}, &SearchProviderError{
			Provider: "keenable",
			Message:  "Keenable search API returned invalid JSON",
			Status:   500,
		}
	}
	resultsRaw, ok := rawObj["results"]
	if !ok {
		return SearchResponse{}, &SearchProviderError{
			Provider: "keenable",
			Message:  "Keenable search API returned an unexpected response shape",
			Status:   500,
		}
	}
	var results []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Snippet     string `json:"snippet"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(resultsRaw, &results); err != nil || results == nil {
		return SearchResponse{}, &SearchProviderError{
			Provider: "keenable",
			Message:  "Keenable search API returned an unexpected response shape",
			Status:   500,
		}
	}

	sources := make([]SearchSource, 0, len(results))
	seen := map[string]bool{}
	for _, r := range results {
		u := normalizeHttpURL(r.URL)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		title := normalizeSearchText(r.Title)
		if title == "" {
			title = u
		}
		sources = append(sources, SearchSource{
			Title:         title,
			URL:           u,
			Snippet:       normalizeSearchText(r.Snippet),
			PublishedDate: strings.TrimSpace(r.PublishedAt),
			AgeSeconds:    dateToAgeSeconds(r.PublishedAt),
		})
		if len(sources) >= maxResults {
			break
		}
	}

	return SearchResponse{
		Provider: "keenable",
		Sources:  sources,
		AuthMode: authMode,
	}, nil
}

// =============================================================================
// Sofya (SOFYA_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/sofya.ts
// =============================================================================

type sofyaProvider struct{}

func (sofyaProvider) ID() string        { return "sofya" }
func (sofyaProvider) Label() string     { return "Sofya" }
func (sofyaProvider) IsAvailable() bool { return envTrimmed("SOFYA_API_KEY") != "" }

func sofyaFreshness(parsed StructuredQuery, recency string) string {
	if parsed.After == "" && parsed.Before == "" {
		return recency
	}
	start := parsed.After
	if start == "" {
		start = "1970-01-01"
	}
	var end string
	if parsed.Before != "" {
		if t, err := time.Parse("2006-01-02", parsed.Before); err == nil {
			end = t.AddDate(0, 0, -1).Format("2006-01-02")
		} else {
			end = parsed.Before
		}
	} else {
		end = nowFunc().UTC().Format("2006-01-02")
	}
	return start + ":" + end
}

func (sofyaProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("SOFYA_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "sofya",
			Message:  `Sofya credentials not found. Set SOFYA_API_KEY or configure an API key for provider "sofya".`,
		}
	}

	parsed := parsedOrParse(params)
	maxResults := clampNumResults(params.requestedResults(), 10, 20)
	qText, err := keywordQueryText("sofya", parsed, QuerySyntax{Phrases: true, Negation: true})
	if err != nil {
		return SearchResponse{}, err
	}
	body := map[string]any{
		"query":        qText,
		"search_depth": "snippets",
		"max_results":  maxResults,
	}
	if inc := siteHosts(parsed.Sites); len(inc) > 0 {
		if len(inc) > 10 {
			inc = inc[:10]
		}
		body["include_domains"] = inc
	}
	if exc := siteHosts(parsed.ExcludedSites); len(exc) > 0 {
		if len(exc) > 10 {
			exc = exc[:10]
		}
		body["exclude_domains"] = exc
	}
	if f := sofyaFreshness(parsed, params.Recency); f != "" {
		body["freshness"] = f
	}

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
	}

	reqCtx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(reqCtx, client, http.MethodPost, sofyaSearchURL, headers, body)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "sofya", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("sofya", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "sofya",
			Message:  fmt.Sprintf("Sofya search API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "sofya", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}

	var rawObj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &rawObj); err != nil {
		return SearchResponse{}, &SearchProviderError{
			Provider: "sofya",
			Message:  "Sofya search API returned invalid JSON",
			Status:   500,
		}
	}
	if rawObj == nil {
		return SearchResponse{}, &SearchProviderError{
			Provider: "sofya",
			Message:  "Sofya search API returned an unexpected response shape",
			Status:   500,
		}
	}
	resultsRaw, ok := rawObj["results"]
	if !ok {
		return SearchResponse{}, &SearchProviderError{
			Provider: "sofya",
			Message:  "Sofya search API returned an unexpected response shape",
			Status:   500,
		}
	}
	var results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Description   string `json:"description"`
		Content       string `json:"content"`
		PublishedDate string `json:"published_date"`
	}
	if err := json.Unmarshal(resultsRaw, &results); err != nil || results == nil {
		return SearchResponse{}, &SearchProviderError{
			Provider: "sofya",
			Message:  "Sofya search API returned an unexpected response shape",
			Status:   500,
		}
	}
	var payload struct {
		AlteredQuery string `json:"altered_query"`
		RelaxedQuery string `json:"relaxed_query"`
	}
	_ = json.Unmarshal([]byte(raw), &payload)

	sources := make([]SearchSource, 0, len(results))
	seen := map[string]bool{}
	for _, r := range results {
		u := normalizeHttpURL(r.URL)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		title := normalizeSearchText(r.Title)
		if title == "" {
			title = u
		}
		snippet := normalizeSearchText(r.Description)
		if snippet == "" {
			snippet = normalizeSearchText(r.Content)
		}
		sources = append(sources, SearchSource{
			Title:         title,
			URL:           u,
			Snippet:       snippet,
			PublishedDate: strings.TrimSpace(r.PublishedDate),
			AgeSeconds:    dateToAgeSeconds(r.PublishedDate),
		})
		if len(sources) >= maxResults {
			break
		}
	}

	var queries []string
	if eq := normalizeSearchText(payload.RelaxedQuery); eq != "" {
		queries = []string{eq}
	} else if eq := normalizeSearchText(payload.AlteredQuery); eq != "" {
		queries = []string{eq}
	}

	return SearchResponse{
		Provider:      "sofya",
		Sources:       sources,
		SearchQueries: queries,
		AuthMode:      "api_key",
	}, nil
}

// =============================================================================
// Hacker News (keyless Algolia HN Search API)
// upstream: packages/coding-agent/src/web/search/providers/hackernews.ts
// =============================================================================

type hackerNewsProvider struct{}

func (hackerNewsProvider) ID() string        { return "hackernews" }
func (hackerNewsProvider) Label() string     { return "Hacker News" }
func (hackerNewsProvider) IsAvailable() bool { return true }
func (hackerNewsProvider) IsKeyless() bool   { return true }

var (
	hnLinkRe = regexp.MustCompile(`<a href="([^"]+)"[^>]*>([^<]*)</a>`)
	hnTagRe  = regexp.MustCompile(`<[^>]+>`)
)

// decodeHNText converts HN's HTML comment/story body into markdown-flavored text.
// upstream: packages/coding-agent/src/web/scrapers/hackernews.ts:43-57
func decodeHNText(rawHTML string) string {
	s := rawHTML
	s = strings.ReplaceAll(s, "<p>", "\n\n")
	s = strings.ReplaceAll(s, "</p>", "")
	s = strings.ReplaceAll(s, "<pre><code>", "\n```\n")
	s = strings.ReplaceAll(s, "</code></pre>", "\n```\n")
	s = strings.ReplaceAll(s, "<code>", "`")
	s = strings.ReplaceAll(s, "</code>", "`")
	s = strings.ReplaceAll(s, "<i>", "*")
	s = strings.ReplaceAll(s, "</i>", "*")
	s = hnLinkRe.ReplaceAllString(s, "[$2]($1)")
	s = hnTagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}

type hnHit struct {
	ObjectID    string `json:"objectID"`
	Author      string `json:"author"`
	CreatedAt   string `json:"created_at"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	StoryText   string `json:"story_text"`
	Points      int    `json:"points"`
	NumComments int    `json:"num_comments"`
	CommentText string `json:"comment_text"`
	StoryTitle  string `json:"story_title"`
}

func (hackerNewsProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	parsed := parsedOrParse(params)
	count := clampNumResults(params.requestedResults(), 10, 30)

	scoped := make([]QueryTerm, 0, len(parsed.Terms)+len(parsed.InTitle)+len(parsed.InText)+len(parsed.InURL))
	scoped = append(scoped, parsed.Terms...)
	for _, t := range parsed.InTitle {
		scoped = append(scoped, QueryTerm{Text: t})
	}
	for _, t := range parsed.InText {
		scoped = append(scoped, QueryTerm{Text: t})
	}
	for _, t := range parsed.InURL {
		scoped = append(scoped, QueryTerm{Text: t})
	}
	keywords := ""
	if len(scoped) > 0 {
		qCopy := parsed
		qCopy.Terms = scoped
		keywords = FormatQuery(qCopy, QuerySyntax{Phrases: true, Negation: true})
	}

	q := url.Values{}
	q.Set("query", keywords)
	q.Set("advancedSyntax", "true")
	q.Set("hitsPerPage", strconv.Itoa(count))

	var optional []string
	for _, t := range parsed.Terms {
		if t.Group != 0 && !t.Negated {
			optional = append(optional, t.Text)
		}
	}
	if len(optional) > 0 {
		q.Set("optionalWords", strings.Join(optional, " "))
	}

	var filters []string
	start := parsed.After
	if start == "" && params.Recency != "" && parsed.Before == "" {
		start = recencyAfterDate(params.Recency)
	}
	if start != "" {
		if t, err := time.Parse("2006-01-02", start); err == nil {
			filters = append(filters, fmt.Sprintf("created_at_i>=%d", t.UTC().Unix()))
		}
	}
	if parsed.Before != "" {
		if t, err := time.Parse("2006-01-02", parsed.Before); err == nil {
			filters = append(filters, fmt.Sprintf("created_at_i<%d", t.UTC().Unix()))
		}
	}
	if len(filters) > 0 {
		q.Set("numericFilters", strings.Join(filters, ","))
	}

	reqCtx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	fetchKind := func(kind string) ([]SearchSource, error) {
		target := hnSearchURL + "?" + q.Encode() + "&tags=" + kind
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errText, _ := readLimitedText(resp, "hackernews", maxProviderErrBytes, true)
			if classified := classifyProviderHttpError("hackernews", resp.StatusCode, errText); classified != nil {
				return nil, classified
			}
			return nil, &SearchProviderError{
				Provider: "hackernews",
				Message:  fmt.Sprintf("Hacker News search error (%d): %s", resp.StatusCode, errText),
				Status:   resp.StatusCode,
			}
		}

		raw, err := readLimitedText(resp, "hackernews", maxProviderRespBytes, false)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Hits []hnHit `json:"hits"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, &SearchProviderError{
				Provider: "hackernews",
				Message:  "Hacker News search returned invalid JSON",
				Status:   502,
			}
		}

		var out []SearchSource
		for _, hit := range payload.Hits {
			if hit.ObjectID == "" {
				continue
			}
			itemURL := hnItemURLPrefix + hit.ObjectID
			var title, snippet string
			if kind == "story" {
				title = hit.Title
				if title == "" {
					title = itemURL
				}
				stats := []string{
					fmt.Sprintf("%d points", hit.Points),
					fmt.Sprintf("%d comments", hit.NumComments),
				}
				if hit.Author != "" {
					stats = append(stats, "by "+hit.Author)
				}
				parts := []string{strings.Join(stats, ", ")}
				if hit.URL != "" {
					parts = append(parts, hit.URL)
				}
				if hit.StoryText != "" {
					if txt := normalizeSearchText(decodeHNText(hit.StoryText)); txt != "" {
						parts = append(parts, txt)
					}
				}
				snippet = strings.Join(parts, " — ")
			} else {
				by := "Comment"
				if hit.Author != "" {
					by = "Comment by " + hit.Author
				}
				if hit.StoryTitle != "" {
					title = by + " on: " + hit.StoryTitle
				} else {
					title = by
				}
				if hit.CommentText != "" {
					snippet = normalizeSearchText(decodeHNText(hit.CommentText))
				}
			}
			out = append(out, SearchSource{
				Title:         title,
				URL:           itemURL,
				Snippet:       snippet,
				PublishedDate: hit.CreatedAt,
				AgeSeconds:    dateToAgeSeconds(hit.CreatedAt),
				Author:        hit.Author,
			})
		}
		return out, nil
	}

	stories, err := fetchKind("story")
	if err != nil {
		return SearchResponse{}, err
	}
	var comments []SearchSource
	if keywords != "" {
		comments, err = fetchKind("comment")
		if err != nil {
			return SearchResponse{}, err
		}
	}

	// Stories lead and take half the slots; either kind backfills the other's shortfall.
	storySlots := (count + 1) / 2
	if count-len(comments) > storySlots {
		storySlots = count - len(comments)
	}
	if storySlots > len(stories) {
		storySlots = len(stories)
	}
	picked := make([]SearchSource, 0, count)
	picked = append(picked, stories[:storySlots]...)
	rem := count - len(picked)
	if rem > len(comments) {
		rem = len(comments)
	}
	picked = append(picked, comments[:rem]...)

	return SearchResponse{
		Provider: "hackernews",
		Sources:  picked,
	}, nil
}
