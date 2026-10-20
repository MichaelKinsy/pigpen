package websearch

import (
	"context"

	"errors"

	"io"
	"net/http"

	"time"
)

const (
	geminiDefaultHost = "https://generativelanguage.googleapis.com"
	geminiAPIVersion  = "v1beta"
	// DefaultGeminiModel is DEFAULT_MODEL upstream.
	DefaultGeminiModel     = "gemini-3.6-flash"
	geminiDefaultTimeout   = 120 * time.Second
	geminiCloudflareMarker = "gateway.ai.cloudflare.com"
)

// GeminiAPIOptions are GeminiApiOptions upstream. A zero Timeout is 120 seconds.
type GeminiAPIOptions struct {
	APIKey   string
	Model    string
	MimeType string
	Timeout  time.Duration
}

// GeminiGenerateContentResult is GeminiGenerateContentResult upstream.
type GeminiGenerateContentResult struct {
	Text         string
	FinishReason string
	BlockReason  string
}

func normalizeGeminiString(v any) string { return *new(string) }

func normalizeGeminiBaseURL(v any) string { return *new(string) }

func (c *geminiCfg) value(key string) (any, error) {
	return *new(any), errors.New("websearch-gemini: not implemented")
}

// geminiAPIHost is getApiHost: GOOGLE_GEMINI_BASE_URL, then geminiBaseUrl, then the public host.
func (c *geminiCfg) apiHost() (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

func (c *geminiCfg) isCloudflareGateway() (bool, error) {
	return *new(bool), errors.New("websearch-gemini: not implemented")
}

// geminiVersionedAPIBase is getVersionedApiBase: the Vertex AI base when ADC is in use.
func (c *geminiCfg) versionedAPIBase() (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

// geminiAPIKeyOptions selects the Gemini key (getApiKey upstream).
func (c *geminiCfg) apiKeyOptions() (CredentialOptions, error) {
	return *new(CredentialOptions), errors.New("websearch-gemini: not implemented")
}

func (c *geminiCfg) cloudflareOptions() (CredentialOptions, error) {
	return *new(CredentialOptions), errors.New("websearch-gemini: not implemented")
}

// GetAPIKey resolves the Gemini API key; "" means none is configured.
func GetAPIKey(ctx context.Context) (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

func (c *geminiCfg) getAPIKey(ctx context.Context) (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

// GetAPIHost is getApiHost; a config that cannot be read falls back to the public host (the
// error surfaces from the request paths, which read the config themselves).
func GetAPIHost() string { return *new(string) }

// GetVersionedAPIBase is getVersionedApiBase.
func GetVersionedAPIBase() string { return *new(string) }

// GetUploadBase is getUploadBase.
func GetUploadBase() string { return *new(string) }

// geminiLegacyCloudflareKey is getLegacyCloudflareApiKey: the literal value, no command source.
func (c *geminiCfg) legacyCloudflareKey() (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

func (c *geminiCfg) resolveCloudflareKey(ctx context.Context) (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

// geminiGatewayConfigured is isGatewayConfigured: a Cloudflare gateway host with a key source.
func (c *geminiCfg) gatewayConfigured() (bool, error) {
	return *new(bool), errors.New("websearch-gemini: not implemented")
}

func (c *geminiCfg) hasAPIKeySource() (bool, error) {
	return *new(bool), errors.New("websearch-gemini: not implemented")
}

// geminiBuildAuthHeaders is buildAuthHeaders: the Gemini key header, or on a Cloudflare gateway
// the gateway header.
func geminiBuildAuthHeaders(apiKey, cloudflareKey string, gateway bool) http.Header {
	return *new(http.Header)
}

type geminiCredentials struct{ apiKey, cloudflareKey, adcToken string }

func (c geminiCredentials) redact(text string) string { return *new(string) }

// geminiBody remembers which credentials a request carried (the WeakMap upstream), so the error
// text read from the response can be redacted without the caller passing them again.
type geminiBody struct {
	io.ReadCloser
	creds geminiCredentials
}

// RedactGeminiAPIResponse is redactGeminiApiResponse: text with the credentials of the request
// that produced resp removed; apiKey covers a response that did not come from FetchGeminiAPI.
func RedactGeminiAPIResponse(resp *http.Response, text, apiKey string) string { return *new(string) }

// FetchGeminiAPI sends one Gemini API request with header auth. apiKey nil resolves the key
// (undefined upstream); a pointer to "" is an explicit "no key" (null upstream).
func FetchGeminiAPI(ctx context.Context, rawURL string, init RequestInit, apiKey *string) (*http.Response, error) {
	return *new(*http.Response), errors.New("websearch-gemini: not implemented")
}

func fetchGeminiAPI(ctx context.Context, c *geminiCfg, rawURL string, init RequestInit, apiKey *string) (*http.Response, error) {
	return *new(*http.Response), errors.New("websearch-gemini: not implemented")
}

func (c *geminiCfg) apiAvailable() (bool, error) {
	return *new(bool), errors.New("websearch-gemini: not implemented")
}

// IsGeminiAPIAvailable is isGeminiApiAvailable. A config that cannot be parsed reads as
// unavailable; the parse error surfaces from the request that follows.
func IsGeminiAPIAvailable() bool { return *new(bool) }

// IsGeminiAPIAvailableWithVideo is isGeminiApiAvailableWithVideo: the Files API paths (YouTube
// and local video) do not support ADC, so they need a key or a Cloudflare gateway.
func IsGeminiAPIAvailableWithVideo() bool { return *new(bool) }

func geminiNotConfigured(withADC bool) error { return errors.New("websearch-gemini: not implemented") }

// geminiJSON reads the response body as JSON (res.json()).
func geminiJSON(resp *http.Response) (map[string]any, error) {
	return *new(map[string]any), errors.New("websearch-gemini: not implemented")
}

func jsonPath(v any, path ...any) any { return *new(any) }

func strField(v any) string { return *new(string) }

// geminiCandidateText joins the non-empty text parts of the first candidate with newlines.
func geminiCandidateText(data map[string]any) string { return *new(string) }

// geminiContent is one user turn, with the key order of the original's JSON.stringify.
type geminiContent struct {
	Role  string `json:"role"`
	Parts []any  `json:"parts"`
}

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
	Tools    []any           `json:"tools,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFileData struct {
	FileURI  string `json:"fileUri"`
	MimeType string `json:"mimeType,omitempty"`
}

type geminiTextPart struct {
	Text string `json:"text"`
}

// geminiRequestBody is the generateContent body: one user turn with parts, and optional tools.
func geminiRequestBody(tools []any, parts ...any) []byte { return *new([]byte) }

func geminiGenerate(ctx context.Context, c *geminiCfg, model string, body []byte, apiKey *string) (*http.Response, error) {
	return *new(*http.Response), errors.New("websearch-gemini: not implemented")
}

func geminiHTTPError(resp *http.Response, apiKey string) error {
	return errors.New("websearch-gemini: not implemented")
}

func isHTTPOK(resp *http.Response) bool { return *new(bool) }

func geminiTimeout(o GeminiAPIOptions) time.Duration { return *new(time.Duration) }

// QueryGeminiAPIWithInlineData sends inline data (a PDF, base64) with a prompt (queryGeminiApiWithInlineData).
func QueryGeminiAPIWithInlineData(ctx context.Context, prompt, data, mimeType string, o GeminiAPIOptions) (*GeminiGenerateContentResult, error) {
	return *new(*GeminiGenerateContentResult), errors.New("websearch-gemini: not implemented")
}

func queryGeminiInline(ctx context.Context, prompt, data, mimeType string, o GeminiAPIOptions) (*GeminiGenerateContentResult, error) {
	return *new(*GeminiGenerateContentResult), errors.New("websearch-gemini: not implemented")
}

// QueryGeminiAPIWithVideo asks about a video by file or YouTube URI (queryGeminiApiWithVideo).
func QueryGeminiAPIWithVideo(ctx context.Context, prompt, videoURI string, o GeminiAPIOptions) (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

func queryGeminiVideo(ctx context.Context, prompt, videoURI string, o GeminiAPIOptions) (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}
