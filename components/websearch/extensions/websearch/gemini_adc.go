package websearch

import (
	"context"

	"crypto/rsa"

	"errors"

	"sync"
)

const (
	geminiTokenEndpoint   = "https://oauth2.googleapis.com/token"
	geminiVertexHost      = "https://aiplatform.googleapis.com"
	geminiVertexVersion   = "v1"
	geminiRefreshSkewMs   = 60_000
	geminiADCRejectedKind = "oauth-credential-rejected"
)

// geminiCfg is one read of web-search.json, as the Gemini files see it (loadConfig upstream). A
// missing file is an empty config; a file that is not JSON fails with the original's message.
// gemini-adc.ts also rejects a root that is not an object; gemini-api.ts reads whatever JSON.parse
// returned, so a non-object root reads as empty there. One operation reads the file once and
// passes the snapshot down (the original caches it for the process).
type geminiCfg struct {
	root      map[string]any
	nonObject bool
	err       error
}

func loadGeminiCfg() *geminiCfg { return *new(*geminiCfg) }

// lax is the config as gemini-api.ts reads it.
func (c *geminiCfg) lax() (map[string]any, error) {
	return *new(map[string]any), errors.New("websearch-gemini: not implemented")
}

// strict is the config as gemini-adc.ts reads it: the root must be an object.
func (c *geminiCfg) strict() (map[string]any, error) {
	return *new(map[string]any), errors.New("websearch-gemini: not implemented")
}

// loadGeminiConfig is the lax or strict config in one call, for callers that need nothing else.
func loadGeminiConfig(strictRoot bool) (map[string]any, error) {
	return *new(map[string]any), errors.New("websearch-gemini: not implemented")
}

// geminiIdentifier is normalizeIdentifier: the configured value, else the environment variable.
func geminiIdentifier(value any, envName string) string { return *new(string) }

func (c *geminiCfg) adcSelected() (bool, error) {
	return *new(bool), errors.New("websearch-gemini: not implemented")
}

// geminiADCProject is getAdcProject: geminiProject, GOOGLE_CLOUD_PROJECT, then GCLOUD_PROJECT.
func (c *geminiCfg) adcProject() (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

func (c *geminiCfg) adcLocation() (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

func geminiADCPath() string { return *new(string) }

func geminiVertexAPIBase(project, location string) string { return *new(string) }

func isGeminiVertexHost(origin string) bool { return *new(bool) }

type geminiADCFile struct {
	typ                                  string
	clientID, clientSecret, refreshToken string
	clientEmail, privateKey, tokenURI    string
}

func adcString(rec map[string]any, name string) (string, bool) { return *new(string), *new(bool) }

func adcRequired(rec map[string]any, name, context string) (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

func parseGeminiADCFile(raw []byte) (*geminiADCFile, error) {
	return *new(*geminiADCFile), errors.New("websearch-gemini: not implemented")
}

func loadGeminiADCFile() (*geminiADCFile, error) {
	return *new(*geminiADCFile), errors.New("websearch-gemini: not implemented")
}

type geminiCachedToken struct {
	token     string
	expiresAt int64
}

var (
	geminiTokenMu sync.Mutex
	geminiToken   *geminiCachedToken
)

func clearGeminiADCTokenCache() { return }

func parseGeminiTokenResponse(text, context string) (*geminiCachedToken, error) {
	return *new(*geminiCachedToken), errors.New("websearch-gemini: not implemented")
}

// geminiTokenExchangeFailure classifies a failed exchange: only an explicit credential or
// authorization rejection (400, 401, 403) is a CredentialResolutionError that callers surface;
// everything else stays a plain error so per-provider fallbacks keep working
// (OAUTH_CREDENTIAL_REJECTION_STATUSES upstream).
func geminiTokenExchangeFailure(status int, detail string) error {
	return errors.New("websearch-gemini: not implemented")
}

// geminiForm is a URLSearchParams body: the pairs keep their order.
func geminiForm(pairs ...string) []byte { return *new([]byte) }

func postGeminiTokenForm(ctx context.Context, endpoint string, form []byte, context string) (*geminiCachedToken, error) {
	return *new(*geminiCachedToken), errors.New("websearch-gemini: not implemented")
}

func exchangeGeminiRefreshToken(ctx context.Context, cfg *geminiADCFile) (*geminiCachedToken, error) {
	return *new(*geminiCachedToken), errors.New("websearch-gemini: not implemented")
}

func parseRSAPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	return *new(*rsa.PrivateKey), errors.New("websearch-gemini: not implemented")
}

func exchangeGeminiServiceAccountJWT(ctx context.Context, cfg *geminiADCFile) (*geminiCachedToken, error) {
	return *new(*geminiCachedToken), errors.New("websearch-gemini: not implemented")
}

// getGeminiADCAccessToken returns a cached bearer token or exchanges the ADC file for a new one.
func getGeminiADCAccessToken(ctx context.Context) (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

func geminiHasAPIKeySourceADC(cfg map[string]any) bool { return *new(bool) }

func geminiHasExplicitAPIBase(cfg map[string]any) bool { return *new(bool) }

// geminiADCAvailable is isGeminiAdcAvailable: ADC is selected, no API key and no explicit base URL
// takes precedence, a project and location are known and the ADC file exists. A config that
// cannot be parsed is an error, as upstream (the failure stays visible).
func (c *geminiCfg) adcAvailable() (bool, error) {
	return *new(bool), errors.New("websearch-gemini: not implemented")
}

// redactGeminiADCToken removes the cached access token from text.
func redactGeminiADCToken(text string) string { return *new(string) }

func geminiADCAvailable() (bool, error) {
	return *new(bool), errors.New("websearch-gemini: not implemented")
}
func geminiADCProject() (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}
func geminiADCLocation() (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}
