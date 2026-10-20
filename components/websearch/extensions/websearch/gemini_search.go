package websearch

import (
	"context"
	"errors"

	"regexp"

	"time"
)

const (
	geminiSearchTimeout    = 120 * time.Second
	geminiRedirectTimeout  = 5 * time.Second
	groundingRedirectMatch = "vertexaisearch.cloud.google.com/grounding-api-redirect"
)

// geminiUnavailableError is the explicit-selection failure when neither the API nor Gemini Web
// can run (gemini-search.ts:searchWithResolvedProvider).
func geminiUnavailableError() error { return errors.New("websearch-gemini: not implemented") }

// geminiWebOptionallyAvailable is isGeminiWebOptionallyAvailable: a failure means unavailable.
func geminiWebOptionallyAvailable(ctx context.Context) bool { return *new(bool) }

// geminiProviderAvailable is the availability of provider "gemini" for explicit selection and
// configured routing: the API (key, gateway or ADC) or Gemini Web.
func geminiProviderAvailable(ctx context.Context, _ FullSearchOptions) bool { return *new(bool) }

// searchWithGeminiAPI is searchWithGeminiApi: nil, nil when no API credentials are configured
// or the answer is empty.
func searchWithGeminiAPI(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return *new(*SearchResponse), errors.New("websearch-gemini: not implemented")
}

func searchWithGeminiAPIOnce(tctx, ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return *new(*SearchResponse), errors.New("websearch-gemini: not implemented")
}

// resolveGroundingChunks turns grounding chunks into results, following Google's grounding
// redirect to the real page where it can.
func resolveGroundingChunks(ctx context.Context, chunks []any) []SearchResult {
	return *new([]SearchResult)
}

// resolveGeminiRedirect reads the Location of the grounding redirect without following it.
func resolveGeminiRedirect(ctx context.Context, proxyURL string) string { return *new(string) }

// searchWithGeminiWeb is searchWithGeminiWeb: nil, nil when Gemini Web is unavailable.
func searchWithGeminiWeb(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return *new(*SearchResponse), errors.New("websearch-gemini: not implemented")
}

var geminiRecencyLabels = map[string]string{"day": "past 24 hours", "week": "past week", "month": "past month", "year": "past year"}

func buildGeminiSearchPrompt(query string, o SearchOptions) string { return *new(string) }

var geminiLinkRE = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`)

func extractGeminiSourceURLs(markdown string) []SearchResult { return *new([]SearchResult) }

// searchWithGemini is searchWithGemini upstream: the API, then Gemini Web. With strict errors
// (explicit selection) failures are collected and thrown; otherwise (auto) they are dropped and
// nil, nil means "nothing answered". Credential failures and aborts always propagate.
func searchWithGemini(ctx context.Context, query string, o SearchOptions, strict bool) (*SearchResponse, error) {
	return *new(*SearchResponse), errors.New("websearch-gemini: not implemented")
}

// searchGeminiExplicit is the search of an explicitly selected, listed or routed "gemini".
func searchGeminiExplicit(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return *new(*SearchResponse), errors.New("websearch-gemini: not implemented")
}

// searchGeminiAllProvider is searchWithAllProvider for gemini: provider "all" uses the API only.
func searchGeminiAllProvider(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return *new(*SearchResponse), errors.New("websearch-gemini: not implemented")
}
