package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Port of gemini-api.ts: the Gemini REST transport shared by search, URL context, PDF and video
// extraction. Auth is header-only (x-goog-api-key, the Cloudflare AI Gateway header, or an ADC
// bearer token on Vertex AI); a credential never travels in a URL.

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

func normalizeGeminiString(v any) string {
	s, _ := v.(string)
	return jsTrim(s)
}

func normalizeGeminiBaseURL(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimRight(jsTrim(s), "/")
}

func (c *geminiCfg) value(key string) (any, error) {
	cfg, err := c.lax()
	if err != nil {
		return nil, err
	}
	return cfg[key], nil
}

// geminiAPIHost is getApiHost: GOOGLE_GEMINI_BASE_URL, then geminiBaseUrl, then the public host.
func (c *geminiCfg) apiHost() (string, error) {
	if h := normalizeGeminiBaseURL(envOrNil("GOOGLE_GEMINI_BASE_URL")); h != "" {
		return h, nil
	}
	cfg, err := c.value("geminiBaseUrl")
	if err != nil {
		return "", err
	}
	if h := normalizeGeminiBaseURL(cfg); h != "" {
		return h, nil
	}
	return geminiDefaultHost, nil
}

func (c *geminiCfg) isCloudflareGateway() (bool, error) {
	host, err := c.apiHost()
	return strings.Contains(host, geminiCloudflareMarker), err
}

// geminiVersionedAPIBase is getVersionedApiBase: the Vertex AI base when ADC is in use.
func (c *geminiCfg) versionedAPIBase() (string, error) {
	project, err := c.adcProject()
	if err != nil {
		return "", err
	}
	location, err := c.adcLocation()
	if err != nil {
		return "", err
	}
	adc, err := c.adcAvailable()
	if err != nil {
		return "", err
	}
	if adc && project != "" && location != "" {
		return geminiVertexAPIBase(project, location), nil
	}
	host, err := c.apiHost()
	return host + "/" + geminiAPIVersion, err
}

// geminiAPIKeyOptions selects the Gemini key (getApiKey upstream).
func (c *geminiCfg) apiKeyOptions() (CredentialOptions, error) {
	cfg, err := c.value("geminiApiKey")
	return CredentialOptions{Provider: "Gemini", ConfiguredValue: cfg, EnvironmentValue: envOrNil("GEMINI_API_KEY")}, err
}

func (c *geminiCfg) cloudflareOptions() (CredentialOptions, error) {
	cfg, err := c.value("cloudflareApiKey")
	return CredentialOptions{Provider: "Cloudflare", ConfiguredValue: cfg, EnvironmentValue: envOrNil("CLOUDFLARE_API_KEY")}, err
}

// GetAPIKey resolves the Gemini API key; "" means none is configured.
func GetAPIKey(ctx context.Context) (string, error) { return loadGeminiCfg().getAPIKey(ctx) }

func (c *geminiCfg) getAPIKey(ctx context.Context) (string, error) {
	o, err := c.apiKeyOptions()
	if err != nil {
		return "", err
	}
	return ResolveCredential(ctx, o)
}

// GetAPIHost is getApiHost; a config that cannot be read falls back to the public host (the
// error surfaces from the request paths, which read the config themselves).
func GetAPIHost() string {
	h, err := loadGeminiCfg().apiHost()
	if err != nil {
		return geminiDefaultHost
	}
	return h
}

// GetVersionedAPIBase is getVersionedApiBase.
func GetVersionedAPIBase() string {
	b, err := loadGeminiCfg().versionedAPIBase()
	if err != nil {
		return GetAPIHost() + "/" + geminiAPIVersion
	}
	return b
}

// GetUploadBase is getUploadBase.
func GetUploadBase() string { return GetAPIHost() + "/upload/" + geminiAPIVersion }

// geminiLegacyCloudflareKey is getLegacyCloudflareApiKey: the literal value, no command source.
func (c *geminiCfg) legacyCloudflareKey() (string, error) {
	if k := normalizeGeminiString(envOrNil("CLOUDFLARE_API_KEY")); k != "" {
		return k, nil
	}
	cfg, err := c.value("cloudflareApiKey")
	return normalizeGeminiString(cfg), err
}

func (c *geminiCfg) resolveCloudflareKey(ctx context.Context) (string, error) {
	o, err := c.cloudflareOptions()
	if err != nil {
		return "", err
	}
	return ResolveCredential(ctx, o)
}

// geminiGatewayConfigured is isGatewayConfigured: a Cloudflare gateway host with a key source.
func (c *geminiCfg) gatewayConfigured() (bool, error) {
	gw, err := c.isCloudflareGateway()
	if err != nil || !gw {
		return false, err
	}
	o, err := c.cloudflareOptions()
	return HasCredentialSource(o), err
}

func (c *geminiCfg) hasAPIKeySource() (bool, error) {
	o, err := c.apiKeyOptions()
	return HasCredentialSource(o), err
}

// geminiBuildAuthHeaders is buildAuthHeaders: the Gemini key header, or on a Cloudflare gateway
// the gateway header.
func geminiBuildAuthHeaders(apiKey, cloudflareKey string, gateway bool) http.Header {
	h := http.Header{}
	if !gateway {
		if apiKey != "" {
			h.Set("x-goog-api-key", apiKey)
		}
		return h
	}
	if cloudflareKey != "" {
		h.Set("cf-aig-authorization", "Bearer "+cloudflareKey)
	}
	return h
}

type geminiCredentials struct{ apiKey, cloudflareKey, adcToken string }

func (c geminiCredentials) redact(text string) string {
	out := RedactCredential(RedactCredential(text, c.apiKey), c.cloudflareKey)
	if c.adcToken != "" {
		out = RedactCredential(out, c.adcToken)
	}
	return out
}

// geminiBody remembers which credentials a request carried (the WeakMap upstream), so the error
// text read from the response can be redacted without the caller passing them again.
type geminiBody struct {
	io.ReadCloser
	creds geminiCredentials
}

// RedactGeminiAPIResponse is redactGeminiApiResponse: text with the credentials of the request
// that produced resp removed; apiKey covers a response that did not come from FetchGeminiAPI.
func RedactGeminiAPIResponse(resp *http.Response, text, apiKey string) string {
	if b, ok := resp.Body.(*geminiBody); ok {
		creds := b.creds
		if creds.apiKey == "" {
			creds.apiKey = apiKey
		}
		return creds.redact(text)
	}
	return geminiCredentials{apiKey: apiKey}.redact(text)
}

// FetchGeminiAPI sends one Gemini API request with header auth. apiKey nil resolves the key
// (undefined upstream); a pointer to "" is an explicit "no key" (null upstream).
func FetchGeminiAPI(ctx context.Context, rawURL string, init RequestInit, apiKey *string) (*http.Response, error) {
	return fetchGeminiAPI(ctx, loadGeminiCfg(), rawURL, init, apiKey)
}

func fetchGeminiAPI(ctx context.Context, c *geminiCfg, rawURL string, init RequestInit, apiKey *string) (*http.Response, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("Invalid URL: %s", rawURL)
	}
	for name := range parsed.Query() {
		if l := strings.ToLower(name); l == "key" || l == "api_key" {
			return nil, errors.New("Gemini API credential query parameters are not allowed")
		}
	}
	project, err := c.adcProject()
	if err != nil {
		return nil, err
	}
	location, err := c.adcLocation()
	if err != nil {
		return nil, err
	}
	adcAvail, err := c.adcAvailable()
	if err != nil {
		return nil, err
	}
	adcMode := adcAvail && project != "" && location != "" && isGeminiVertexHost(Origin(parsed))
	var adcToken string
	if adcMode {
		if adcToken, err = getGeminiADCAccessToken(ctx); err != nil {
			return nil, err
		}
	}
	var resolvedKey string
	if !adcMode {
		if apiKey == nil {
			if resolvedKey, err = c.getAPIKey(ctx); err != nil {
				return nil, err
			}
		} else {
			resolvedKey = *apiKey
		}
	}
	gateway, err := c.isCloudflareGateway()
	if err != nil {
		return nil, err
	}
	var cloudflareKey string
	if gateway {
		if cloudflareKey, err = c.resolveCloudflareKey(ctx); err != nil {
			return nil, err
		}
	}
	host, err := c.apiHost()
	if err != nil {
		return nil, err
	}
	hostURL, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("Invalid URL: %s", host)
	}
	configured, err := c.gatewayConfigured()
	if err != nil {
		return nil, err
	}
	origin := Origin(parsed)
	allowed := origin == Origin(hostURL) || origin == geminiDefaultHost || adcMode
	if (resolvedKey != "" || configured || adcMode) && !allowed {
		return nil, errors.New("Gemini API request host is not allowed")
	}
	headers := init.Header.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	headers.Del("x-goog-api-key")
	headers.Del("cf-aig-authorization")
	headers.Del("authorization")
	if adcMode && adcToken != "" {
		headers.Set("Authorization", "Bearer "+adcToken)
	} else {
		for name, vs := range geminiBuildAuthHeaders(resolvedKey, cloudflareKey, gateway) {
			headers[name] = vs
		}
	}
	init.Header = headers
	creds := geminiCredentials{apiKey: resolvedKey, cloudflareKey: cloudflareKey, adcToken: adcToken}
	resp, err := FetchWithCredentialRedirects(ctx, parsed.String(), init, []string{"x-goog-api-key", "cf-aig-authorization", "Authorization"})
	if err != nil {
		if msg := err.Error(); creds.redact(msg) != msg {
			return nil, &redactedError{creds.redact(msg), err}
		}
		return nil, err
	}
	resp.Body = &geminiBody{ReadCloser: resp.Body, creds: creds}
	return resp, nil
}

func (c *geminiCfg) apiAvailable() (bool, error) {
	if adc, err := c.adcAvailable(); err != nil || adc {
		return adc, err
	}
	if has, err := c.hasAPIKeySource(); err != nil || has {
		return has, err
	}
	return c.gatewayConfigured()
}

// IsGeminiAPIAvailable is isGeminiApiAvailable. A config that cannot be parsed reads as
// unavailable; the parse error surfaces from the request that follows.
func IsGeminiAPIAvailable() bool {
	ok, err := loadGeminiCfg().apiAvailable()
	return ok && err == nil
}

// IsGeminiAPIAvailableWithVideo is isGeminiApiAvailableWithVideo: the Files API paths (YouTube
// and local video) do not support ADC, so they need a key or a Cloudflare gateway.
func IsGeminiAPIAvailableWithVideo() bool {
	c := loadGeminiCfg()
	has, err := c.hasAPIKeySource()
	if err != nil {
		return false
	}
	if has {
		return true
	}
	gw, err := c.gatewayConfigured()
	return gw && err == nil
}

func geminiNotConfigured(withADC bool) error {
	msg := "Gemini API not configured. Either:\n" +
		"  1. Configure geminiApiKey in " + ConfigPath() + " or set GEMINI_API_KEY\n" +
		"  2. Set GOOGLE_GEMINI_BASE_URL + CLOUDFLARE_API_KEY for Cloudflare AI Gateway routing"
	if withADC {
		msg += "\n  3. Set geminiAuth to \"adc\" with a Google Cloud ADC (GOOGLE_APPLICATION_CREDENTIALS) + project/location"
	}
	return errors.New(msg)
}

// geminiJSON reads the response body as JSON (res.json()).
func geminiJSON(resp *http.Response) (map[string]any, error) {
	text, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	v, err := unmarshalAny(text)
	if err != nil {
		return nil, fmt.Errorf("Gemini API returned a response that is not valid JSON: %s", jsonErrorText(err))
	}
	m, _ := v.(map[string]any)
	return m, nil
}

func jsonPath(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

// geminiCandidateText joins the non-empty text parts of the first candidate with newlines.
func geminiCandidateText(data map[string]any) string {
	parts, _ := jsonPath(data, "candidates", 0, "content", "parts").([]any)
	var texts []string
	for _, p := range parts {
		if s, ok := jsonPath(p, "text").(string); ok && s != "" {
			texts = append(texts, s)
		}
	}
	return strings.Join(texts, "\n")
}

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
func geminiRequestBody(tools []any, parts ...any) []byte {
	b, _ := json.Marshal(geminiRequest{Contents: []geminiContent{{Role: "user", Parts: parts}}, Tools: tools})
	return b
}

func geminiGenerate(ctx context.Context, c *geminiCfg, model string, body []byte, apiKey *string) (*http.Response, error) {
	base, err := c.versionedAPIBase()
	if err != nil {
		return nil, err
	}
	return fetchGeminiAPI(ctx, c, base+"/models/"+model+":generateContent", RequestInit{Method: "POST", Body: body, Header: http.Header{"Content-Type": {"application/json"}}}, apiKey)
}

func geminiHTTPError(resp *http.Response, apiKey string) error {
	text, _ := readBody(resp)
	return fmt.Errorf("Gemini API error %d: %s", resp.StatusCode, slice300(RedactGeminiAPIResponse(resp, text, apiKey)))
}

func isHTTPOK(resp *http.Response) bool { return resp.StatusCode >= 200 && resp.StatusCode <= 299 }

func geminiTimeout(o GeminiAPIOptions) time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return geminiDefaultTimeout
}

// QueryGeminiAPIWithInlineData sends inline data (a PDF, base64) with a prompt (queryGeminiApiWithInlineData).
func QueryGeminiAPIWithInlineData(ctx context.Context, prompt, data, mimeType string, o GeminiAPIOptions) (*GeminiGenerateContentResult, error) {
	tctx, cancel := withTimeout(ctx, geminiTimeout(o))
	defer cancel()
	res, err := queryGeminiInline(tctx, prompt, data, mimeType, o)
	return res, timeoutFix(ctx, tctx, err)
}

func queryGeminiInline(ctx context.Context, prompt, data, mimeType string, o GeminiAPIOptions) (*GeminiGenerateContentResult, error) {
	c := loadGeminiCfg()
	adc, err := c.adcAvailable()
	if err != nil {
		return nil, err
	}
	apiKey := o.APIKey
	if adc {
		apiKey = ""
	} else if apiKey == "" {
		if apiKey, err = c.getAPIKey(ctx); err != nil {
			return nil, err
		}
	}
	gw, err := c.gatewayConfigured()
	if err != nil {
		return nil, err
	}
	if apiKey == "" && !gw && !adc {
		return nil, geminiNotConfigured(true)
	}
	model := o.Model
	if model == "" {
		model = DefaultGeminiModel
	}
	body := geminiRequestBody(nil, map[string]any{"inlineData": geminiInlineData{MimeType: mimeType, Data: data}}, geminiTextPart{prompt})
	res, err := geminiGenerate(ctx, c, model, body, &apiKey)
	if err != nil {
		return nil, err
	}
	if !isHTTPOK(res) {
		return nil, geminiHTTPError(res, apiKey)
	}
	parsed, err := geminiJSON(res)
	if err != nil {
		return nil, err
	}
	return &GeminiGenerateContentResult{
		Text:         geminiCandidateText(parsed),
		FinishReason: anyString(jsonPath(parsed, "candidates", 0, "finishReason")),
		BlockReason:  anyString(jsonPath(parsed, "promptFeedback", "blockReason")),
	}, nil
}

// QueryGeminiAPIWithVideo asks about a video by file or YouTube URI (queryGeminiApiWithVideo).
func QueryGeminiAPIWithVideo(ctx context.Context, prompt, videoURI string, o GeminiAPIOptions) (string, error) {
	tctx, cancel := withTimeout(ctx, geminiTimeout(o))
	defer cancel()
	text, err := queryGeminiVideo(tctx, prompt, videoURI, o)
	return text, timeoutFix(ctx, tctx, err)
}

func queryGeminiVideo(ctx context.Context, prompt, videoURI string, o GeminiAPIOptions) (string, error) {
	c := loadGeminiCfg()
	apiKey := o.APIKey
	var err error
	if apiKey == "" {
		if apiKey, err = c.getAPIKey(ctx); err != nil {
			return "", err
		}
	}
	gw, err := c.gatewayConfigured()
	if err != nil {
		return "", err
	}
	if apiKey == "" && !gw {
		return "", geminiNotConfigured(false)
	}
	model := o.Model
	if model == "" {
		model = DefaultGeminiModel
	}
	res, err := geminiGenerate(ctx, c, model, geminiRequestBody(nil, map[string]any{"fileData": geminiFileData{FileURI: videoURI, MimeType: o.MimeType}}, geminiTextPart{prompt}), &apiKey)
	if err != nil {
		return "", err
	}
	if !isHTTPOK(res) {
		return "", geminiHTTPError(res, apiKey)
	}
	parsed, err := geminiJSON(res)
	if err != nil {
		return "", err
	}
	text := geminiCandidateText(parsed)
	if text == "" {
		return "", errors.New("Gemini API returned empty response")
	}
	return text, nil
}
