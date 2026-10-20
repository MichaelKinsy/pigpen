package websearch

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const (
	// GeminiMaxHeaderSize is GEMINI_MAX_HEADER_SIZE: the response header budget of Gemini Web.
	GeminiMaxHeaderSize = 4 * 1024 * 1024
	// DefaultGeminiWebModel is DEFAULT_GEMINI_WEB_MODEL.
	DefaultGeminiWebModel = "gemini-3.1-pro"
)

// GeminiWebRequest is one request of Gemini Web as the transport sees it.
type GeminiWebRequest struct {
	Method   string
	URL      string
	Header   http.Header
	Body     []byte
	Redirect string
}

// GeminiWebFetch performs one Gemini Web request.
type GeminiWebFetch func(ctx context.Context, r GeminiWebRequest) (*http.Response, error)

// GeminiWebOptions are the options of queryWithCookies.
type GeminiWebOptions struct {
	YoutubeURL string
	Model      string
	Files      []string
	Timeout    time.Duration
}

var errGeminiWebStub = errors.New("Gemini Web is not implemented")

// SetGeminiWebFetch replaces the Gemini Web transport and returns a restore function.
func SetGeminiWebFetch(GeminiWebFetch) func() { return func() {} }

func newGeminiWebTransport() (*http.Transport, error) { return nil, errGeminiWebStub }

func resolveGeminiWebClient() (*http.Client, error) { return nil, errGeminiWebStub }

// IsGeminiWebAvailable is isGeminiWebAvailable.
func IsGeminiWebAvailable(context.Context, string) (CookieMap, error) {
	return nil, errGeminiWebStub
}

// QueryGeminiWebWithCookies is queryWithCookies.
func QueryGeminiWebWithCookies(context.Context, string, CookieMap, GeminiWebOptions) (string, error) {
	return "", errGeminiWebStub
}

// ActiveGoogleEmail is getActiveGoogleEmail: "" when none is found.
func ActiveGoogleEmail(context.Context, CookieMap) string { return "" }

// GeminiWebAvailabilityDiagnosticDetails is getGeminiWebAvailabilityDiagnosticDetails.
func GeminiWebAvailabilityDiagnosticDetails() *BrowserCookieDiagnosticDetails { return nil }

// cookieGeminiWeb is the GeminiWebClient of this port.
type cookieGeminiWeb struct{}

func (cookieGeminiWeb) Available(context.Context) (GeminiWebCookies, error) { return nil, nil }

func (cookieGeminiWeb) Diagnostic() string { return "" }

func (cookieGeminiWeb) Query(context.Context, string, GeminiWebCookies, time.Duration) (string, error) {
	return "", errGeminiWebStub
}
