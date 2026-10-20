package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Port of the Gemini half of gemini-search.ts: searching with the Gemini API (Google Search
// grounding) and Gemini Web, and the explicit, auto and "all" entry points around them.

const (
	geminiSearchTimeout    = 120 * time.Second
	geminiRedirectTimeout  = 5 * time.Second
	groundingRedirectMatch = "vertexaisearch.cloud.google.com/grounding-api-redirect"
)

// geminiUnavailableError is the explicit-selection failure when neither the API nor Gemini Web
// can run (gemini-search.ts:searchWithResolvedProvider).
func geminiUnavailableError() error {
	return errors.New("Gemini search unavailable. Either:\n" +
		"  1. Configure geminiApiKey in " + ConfigPath() + " or set GEMINI_API_KEY\n" +
		"  2. Set GOOGLE_GEMINI_BASE_URL + CLOUDFLARE_API_KEY for Cloudflare AI Gateway routing\n" +
		"  3. Set geminiAuth to \"adc\" in web-search.json with a Google Cloud ADC + project/location\n" +
		"  4. Sign into gemini.google.com in a supported Chromium-based browser")
}

// geminiWebOptionallyAvailable is isGeminiWebOptionallyAvailable: a failure means unavailable.
func geminiWebOptionallyAvailable(ctx context.Context) bool {
	cookies, err := geminiWebClient.Available(ctx)
	return err == nil && cookies != nil
}

// geminiProviderAvailable is the availability of provider "gemini" for explicit selection and
// configured routing: the API (key, gateway or ADC) or Gemini Web.
func geminiProviderAvailable(ctx context.Context, _ FullSearchOptions) bool {
	return IsGeminiAPIAvailable() || geminiWebOptionallyAvailable(ctx)
}

// searchWithGeminiAPI is searchWithGeminiApi: nil, nil when no API credentials are configured
// or the answer is empty.
func searchWithGeminiAPI(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	tctx, cancel := withTimeout(ctx, geminiSearchTimeout)
	defer cancel()
	res, err := searchWithGeminiAPIOnce(tctx, ctx, query, o)
	return res, timeoutFix(ctx, tctx, err)
}

func searchWithGeminiAPIOnce(tctx, ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	c := loadGeminiCfg()
	adc, err := c.adcAvailable()
	if err != nil {
		return nil, err
	}
	var apiKey string
	if !adc {
		if apiKey, err = c.getAPIKey(tctx); err != nil {
			return nil, err
		}
	}
	gw, err := c.gatewayConfigured()
	if err != nil {
		return nil, err
	}
	if apiKey == "" && !gw && !adc {
		return nil, nil
	}
	// searchModel: Search has already validated the whole config (getSearchConfig upstream).
	model := DefaultGeminiModel
	if root, err := c.lax(); err == nil {
		if m, ok := root["searchModel"].(string); ok && jsTrim(m) != "" {
			model = jsTrim(m)
		}
	}
	body := geminiRequestBody([]any{map[string]any{"google_search": map[string]any{}}}, geminiTextPart{query})
	res, err := geminiGenerate(tctx, c, model, body, &apiKey)
	if err != nil {
		return nil, err
	}
	if !isHTTPOK(res) {
		return nil, geminiHTTPError(res, apiKey)
	}
	data, err := geminiJSON(res)
	if err != nil {
		return nil, err
	}
	answer := geminiCandidateText(data)
	chunks, _ := jsonPath(data, "candidates", 0, "groundingMetadata", "groundingChunks").([]any)
	results := resolveGroundingChunks(ctx, chunks)
	if answer == "" && len(results) == 0 {
		return nil, nil
	}
	return &SearchResponse{Answer: answer, Results: results}, nil
}

// resolveGroundingChunks turns grounding chunks into results, following Google's grounding
// redirect to the real page where it can.
func resolveGroundingChunks(ctx context.Context, chunks []any) []SearchResult {
	results := []SearchResult{}
	for _, chunk := range chunks {
		web, ok := jsonPath(chunk, "web").(map[string]any)
		if !ok {
			continue
		}
		title, _ := web["title"].(string)
		u, _ := web["uri"].(string)
		if strings.Contains(u, groundingRedirectMatch) {
			if resolved := resolveGeminiRedirect(ctx, u); resolved != "" {
				u = resolved
			}
		}
		if u != "" {
			results = append(results, SearchResult{Title: title, URL: u})
		}
	}
	return results
}

// resolveGeminiRedirect reads the Location of the grounding redirect without following it.
func resolveGeminiRedirect(ctx context.Context, proxyURL string) string {
	rctx, cancel := context.WithTimeout(ctx, geminiRedirectTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodHead, proxyURL, nil)
	if err != nil {
		return ""
	}
	resp, err := apiClientFor(rctx).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	return resp.Header.Get("Location")
}

// searchWithGeminiWeb is searchWithGeminiWeb: nil, nil when Gemini Web is unavailable.
func searchWithGeminiWeb(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	cookies, err := geminiWebClient.Available(ctx)
	if err != nil || cookies == nil {
		return nil, err
	}
	text, err := geminiWebClient.Query(ctx, buildGeminiSearchPrompt(query, o), cookies, geminiSearchTimeout)
	if err != nil {
		return nil, err
	}
	return &SearchResponse{Answer: text, Results: extractGeminiSourceURLs(text)}, nil
}

var geminiRecencyLabels = map[string]string{"day": "past 24 hours", "week": "past week", "month": "past month", "year": "past year"}

func buildGeminiSearchPrompt(query string, o SearchOptions) string {
	prompt := "Search the web and answer the following question. Include source URLs for your claims.\nFormat your response as:\n1. A direct answer to the question\n2. Cited sources as markdown links\n\nQuestion: " + query
	if o.RecencyFilter != "" {
		label, ok := geminiRecencyLabels[o.RecencyFilter]
		if !ok {
			label = "undefined"
		}
		prompt += "\n\nOnly include results from the " + label + "."
	}
	if len(o.DomainFilter) > 0 {
		var includes, excludes []string
		for _, d := range o.DomainFilter {
			if strings.HasPrefix(d, "-") {
				excludes = append(excludes, d[1:])
			} else {
				includes = append(includes, d)
			}
		}
		if len(includes) > 0 {
			prompt += "\n\nOnly cite sources from: " + strings.Join(includes, ", ")
		}
		if len(excludes) > 0 {
			prompt += "\n\nDo not cite sources from: " + strings.Join(excludes, ", ")
		}
	}
	return prompt
}

var geminiLinkRE = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`)

func extractGeminiSourceURLs(markdown string) []SearchResult {
	results := []SearchResult{}
	seen := map[string]bool{}
	for _, m := range geminiLinkRE.FindAllStringSubmatch(markdown, -1) {
		if seen[m[2]] {
			continue
		}
		seen[m[2]] = true
		results = append(results, SearchResult{Title: m[1], URL: m[2]})
	}
	return results
}

// searchWithGemini is searchWithGemini upstream: the API, then Gemini Web. With strict errors
// (explicit selection) failures are collected and thrown; otherwise (auto) they are dropped and
// nil, nil means "nothing answered". Credential failures and aborts always propagate.
func searchWithGemini(ctx context.Context, query string, o SearchOptions, strict bool) (*SearchResponse, error) {
	var errs []string
	api, err := searchWithGeminiAPI(ctx, query, o)
	if err != nil {
		var credErr *CredentialResolutionError
		if errors.As(err, &credErr) || isAbortError(err) {
			return nil, err
		}
		errs = append(errs, "Gemini API: "+err.Error())
	} else if api != nil {
		return api, nil
	}

	web, err := searchWithGeminiWeb(ctx, query, o)
	if err != nil {
		if isAbortError(err) {
			return nil, err
		}
		errs = append(errs, "Gemini Web: "+err.Error())
	} else if web != nil {
		return web, nil
	} else if d := geminiWebClient.Diagnostic(); d != "" {
		errs = append(errs, "Gemini Web: "+d)
	}

	if strict && len(errs) > 0 {
		return nil, fmt.Errorf("Gemini search failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil, nil
}

// searchGeminiExplicit is the search of an explicitly selected, listed or routed "gemini".
func searchGeminiExplicit(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	r, err := searchWithGemini(ctx, query, o, true)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, geminiUnavailableError()
	}
	return r, nil
}

// searchGeminiAllProvider is searchWithAllProvider for gemini: provider "all" uses the API only.
func searchGeminiAllProvider(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	r, err := searchWithGeminiAPI(ctx, query, o)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.New("Gemini API search returned no results.")
	}
	return r, nil
}
