package websearch

import (
	"context"

	"errors"
	"time"
)

// GeminiWebCookies are the Google cookies Gemini Web authenticates with (CookieMap upstream).
type GeminiWebCookies map[string]string

// GeminiWebClient is the surface of gemini-web.ts that gemini-search.ts and gemini-url-context.ts
// call. The cookie-based client (Chrome cookie extraction, the opt-in config, the web protocol)
// is a separate slice that installs itself with SetGeminiWebClient; until then the default
// client below is used.
type GeminiWebClient interface {
	// Available is isGeminiWebAvailable: the cookies, or nil when Gemini Web cannot be used
	// (browser cookie access off, no signed-in profile). An error is an unexpected failure.
	Available(ctx context.Context) (GeminiWebCookies, error)
	// Diagnostic is getGeminiWebAvailabilityDiagnostic: why Gemini Web is unavailable, or "".
	Diagnostic() string
	// Query is queryWithCookies with the default model.
	Query(ctx context.Context, prompt string, cookies GeminiWebCookies, timeout time.Duration) (string, error)
}

var geminiWebClient GeminiWebClient = refusingGeminiWeb{}

// SetGeminiWebClient installs the Gemini Web client and returns a restore function.
func SetGeminiWebClient(c GeminiWebClient) func() { return *new(func()) }

// refusingGeminiWeb is the client of a build without browser cookie support: Gemini Web is
// unavailable while browser cookie access is off (as in the original, where it is off by
// default) and refused with the port's refusal message when the user switched it on.
type refusingGeminiWeb struct{}

// geminiBrowserCookiesAllowed is isBrowserCookieAccessAllowed (gemini-web-config.ts).
func geminiBrowserCookiesAllowed() (bool, error) {
	return *new(bool), errors.New("websearch-gemini: not implemented")
}

func (refusingGeminiWeb) Available(context.Context) (GeminiWebCookies, error) {
	return *new(GeminiWebCookies), errors.New("websearch-gemini: not implemented")
}

func (refusingGeminiWeb) Diagnostic() string { return *new(string) }

func (refusingGeminiWeb) Query(context.Context, string, GeminiWebCookies, time.Duration) (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}
