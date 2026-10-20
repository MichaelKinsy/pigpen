package websearch

import (
	"context"
	"time"
)

// GeminiWebCookies are the Google cookies Gemini Web authenticates with (CookieMap upstream).
type GeminiWebCookies map[string]string

// GeminiWebClient is the surface of gemini-web.ts that gemini-search.ts and gemini-url-context.ts
// call. The cookie-based client (Chrome cookie extraction, the opt-in config, the web protocol)
// is cookieGeminiWeb (gemini_web.go), the default; SetGeminiWebClient replaces it.
type GeminiWebClient interface {
	// Available is isGeminiWebAvailable: the cookies, or nil when Gemini Web cannot be used
	// (browser cookie access off, no signed-in profile). An error is an unexpected failure.
	Available(ctx context.Context) (GeminiWebCookies, error)
	// Diagnostic is getGeminiWebAvailabilityDiagnostic: why Gemini Web is unavailable, or "".
	Diagnostic() string
	// Query is queryWithCookies with the default model.
	Query(ctx context.Context, prompt string, cookies GeminiWebCookies, timeout time.Duration) (string, error)
}

var geminiWebClient GeminiWebClient = cookieGeminiWeb{}

// SetGeminiWebClient installs the Gemini Web client and returns a restore function.
func SetGeminiWebClient(c GeminiWebClient) func() {
	prev := geminiWebClient
	if c == nil {
		c = cookieGeminiWeb{}
	}
	geminiWebClient = c
	return func() { geminiWebClient = prev }
}
