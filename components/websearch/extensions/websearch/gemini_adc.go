package websearch

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Port of gemini-adc.ts: Google Application Default Credentials for the Gemini API on Vertex AI
// (an authorized_user refresh token or a service account JWT, exchanged for a bearer token).

const (
	geminiTokenEndpoint   = "https://oauth2.googleapis.com/token"
	geminiVertexHost      = "https://aiplatform.googleapis.com"
	geminiVertexVersion   = "v1"
	geminiRefreshSkewMs   = 60_000
	geminiADCRejectedKind = "oauth-credential-rejected"
)

// geminiConfig is the part of web-search.json the Gemini files read (loadConfig upstream).
// A missing file is an empty config; a file that is not JSON fails with the original's message.
// gemini-adc.ts also rejects a root that is not an object; gemini-api.ts reads whatever JSON.parse
// returned, so a non-object root reads as empty there.
func loadGeminiConfig(strictRoot bool) (map[string]any, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("Failed to parse %s: %s", path, jsonErrorText(err))
	}
	root, ok := parsed.(map[string]any)
	if !ok {
		if strictRoot {
			return nil, fmt.Errorf("Failed to parse %s: config root must be an object", path)
		}
		return map[string]any{}, nil
	}
	return root, nil
}

// geminiIdentifier is normalizeIdentifier: the configured value, else the environment variable.
func geminiIdentifier(value any, envName string) string {
	if s, ok := value.(string); ok {
		if t := jsTrim(s); t != "" {
			return t
		}
	}
	return jsTrim(os.Getenv(envName))
}

func geminiADCSelected() (bool, error) {
	cfg, err := loadGeminiConfig(true)
	if err != nil {
		return false, err
	}
	v, present := cfg["geminiAuth"]
	if !present || v == nil {
		return false, nil
	}
	return strings.ToLower(jsTrim(jsString(v))) == "adc", nil
}

// geminiADCProject is getAdcProject: geminiProject, GOOGLE_CLOUD_PROJECT, then GCLOUD_PROJECT.
func geminiADCProject() (string, error) {
	cfg, err := loadGeminiConfig(true)
	if err != nil {
		return "", err
	}
	if p := geminiIdentifier(cfg["geminiProject"], "GOOGLE_CLOUD_PROJECT"); p != "" {
		return p, nil
	}
	return geminiIdentifier(nil, "GCLOUD_PROJECT"), nil
}

func geminiADCLocation() (string, error) {
	cfg, err := loadGeminiConfig(true)
	if err != nil {
		return "", err
	}
	return geminiIdentifier(cfg["geminiLocation"], "GOOGLE_CLOUD_LOCATION"), nil
}

func geminiADCPath() string {
	if p := jsTrim(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")); p != "" {
		return p
	}
	return filepath.Join(homeDir(), ".config", "gcloud", "application_default_credentials.json")
}

func geminiVertexAPIBase(project, location string) string {
	return geminiVertexHost + "/" + geminiVertexVersion + "/projects/" + url.PathEscape(project) + "/locations/" + url.PathEscape(location) + "/publishers/google"
}

func isGeminiVertexHost(origin string) bool { return origin == geminiVertexHost }

type geminiADCFile struct {
	typ                                  string
	clientID, clientSecret, refreshToken string
	clientEmail, privateKey, tokenURI    string
}

func adcString(rec map[string]any, name string) (string, bool) {
	s, ok := rec[name].(string)
	return s, ok
}

func adcRequired(rec map[string]any, name, context string) (string, error) {
	s, _ := adcString(rec, name)
	if s = jsTrim(s); s == "" {
		return "", fmt.Errorf("%s is missing %s", context, name)
	}
	return s, nil
}

func parseGeminiADCFile(raw []byte) (*geminiADCFile, error) {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, errors.New(jsonErrorText(err))
	}
	rec, ok := parsed.(map[string]any)
	if !ok {
		return nil, errors.New("credential root must be an object")
	}
	typ, has := adcString(rec, "type")
	if !has {
		typ = "authorized_user"
	}
	f := &geminiADCFile{typ: typ}
	var err error
	switch typ {
	case "authorized_user":
		const c = "Gemini ADC authorized_user file"
		if f.clientID, err = adcRequired(rec, "client_id", c); err != nil {
			return nil, err
		}
		if f.clientSecret, err = adcRequired(rec, "client_secret", c); err != nil {
			return nil, err
		}
		if f.refreshToken, err = adcRequired(rec, "refresh_token", c); err != nil {
			return nil, err
		}
	case "service_account":
		const c = "Gemini ADC service_account file"
		if f.clientEmail, err = adcRequired(rec, "client_email", c); err != nil {
			return nil, err
		}
		if f.privateKey, err = adcRequired(rec, "private_key", c); err != nil {
			return nil, err
		}
		f.tokenURI, _ = adcString(rec, "token_uri")
	}
	return f, nil
}

func loadGeminiADCFile() (*geminiADCFile, error) {
	path := geminiADCPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if path == "" || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("Google Application Default Credentials file not found at %s", path)
		}
		return nil, err
	}
	f, err := parseGeminiADCFile(data)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse Google Application Default Credentials file at %s: %s", path, err.Error())
	}
	return f, nil
}

type geminiCachedToken struct {
	token     string
	expiresAt int64
}

var (
	geminiTokenMu sync.Mutex
	geminiToken   *geminiCachedToken
)

func clearGeminiADCTokenCache() {
	geminiTokenMu.Lock()
	geminiToken = nil
	geminiTokenMu.Unlock()
}

func parseGeminiTokenResponse(text, context string) (*geminiCachedToken, error) {
	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("%s returned invalid JSON: %s", context, jsonErrorText(err))
	}
	rec, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s returned a non-object response", context)
	}
	tokenStr, _ := rec["access_token"].(string)
	token := jsTrim(tokenStr)
	if token == "" {
		return nil, fmt.Errorf("%s returned no access_token", context)
	}
	expiresIn := 3600.0
	if v, present := rec["expires_in"]; present && v != nil {
		n, isNum := v.(float64)
		if !isNum || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("%s returned invalid expires_in", context)
		}
		expiresIn = n
	}
	return &geminiCachedToken{token: token, expiresAt: nowMs() + int64(math.Max(expiresIn*1000-geminiRefreshSkewMs, 60_000))}, nil
}

// geminiTokenExchangeFailure classifies a failed exchange: only an explicit credential or
// authorization rejection (400, 401, 403) is a CredentialResolutionError that callers surface;
// everything else stays a plain error so per-provider fallbacks keep working
// (OAUTH_CREDENTIAL_REJECTION_STATUSES upstream).
func geminiTokenExchangeFailure(status int, detail string) error {
	if status == 400 || status == 401 || status == 403 {
		return &CredentialResolutionError{Provider: "Gemini ADC", Category: geminiADCRejectedKind}
	}
	return fmt.Errorf("Gemini ADC token exchange failed (%d): %s", status, slice300(detail))
}

func postGeminiTokenForm(ctx context.Context, endpoint string, form url.Values, context string) (*geminiCachedToken, error) {
	resp, err := FetchWithCredentialRedirects(ctx, endpoint, RequestInit{Method: "POST", Body: []byte(form.Encode()),
		Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}}, nil)
	if err != nil {
		return nil, err
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, geminiTokenExchangeFailure(resp.StatusCode, text)
	}
	return parseGeminiTokenResponse(text, context)
}

func exchangeGeminiRefreshToken(ctx context.Context, cfg *geminiADCFile) (*geminiCachedToken, error) {
	return postGeminiTokenForm(ctx, geminiTokenEndpoint, url.Values{
		"client_id": {cfg.clientID}, "client_secret": {cfg.clientSecret},
		"refresh_token": {cfg.refreshToken}, "grant_type": {"refresh_token"},
	}, "Gemini ADC token exchange")
}

func parseRSAPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("error:1E08010C:DECODER routines::unsupported")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("private key is not an RSA key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func exchangeGeminiServiceAccountJWT(ctx context.Context, cfg *geminiADCFile) (*geminiCachedToken, error) {
	if cfg.clientEmail == "" || cfg.privateKey == "" {
		return nil, errors.New("Gemini ADC service_account file is missing client_email or private_key")
	}
	now := nowMs() / 1000
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": cfg.clientEmail, "scope": "https://www.googleapis.com/auth/cloud-platform", "aud": geminiTokenEndpoint, "iat": now, "exp": now + 3600})
	assertion := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	key, err := parseRSAPrivateKey(cfg.privateKey)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(assertion))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, err
	}
	endpoint := cfg.tokenURI
	if endpoint == "" {
		endpoint = geminiTokenEndpoint
	}
	return postGeminiTokenForm(ctx, endpoint, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion + "." + enc.EncodeToString(sig)},
	}, "Gemini ADC service account token exchange")
}

// getGeminiADCAccessToken returns a cached bearer token or exchanges the ADC file for a new one.
func getGeminiADCAccessToken(ctx context.Context) (string, error) {
	geminiTokenMu.Lock()
	defer geminiTokenMu.Unlock()
	if geminiToken != nil && geminiToken.expiresAt > nowMs() {
		return geminiToken.token, nil
	}
	cfg, err := loadGeminiADCFile()
	if err != nil {
		return "", err
	}
	var tok *geminiCachedToken
	switch cfg.typ {
	case "authorized_user":
		tok, err = exchangeGeminiRefreshToken(ctx, cfg)
	case "service_account":
		tok, err = exchangeGeminiServiceAccountJWT(ctx, cfg)
	default:
		return "", fmt.Errorf(`Gemini ADC unsupported credential type "%s" (expected authorized_user or service_account)`, cfg.typ)
	}
	if err != nil {
		return "", err
	}
	geminiToken = tok
	return tok.token, nil
}

func geminiHasAPIKeySourceADC(cfg map[string]any) bool {
	if s, ok := cfg["geminiApiKey"].(string); ok && jsTrim(s) != "" {
		return true
	}
	return jsTrim(os.Getenv("GEMINI_API_KEY")) != ""
}

func geminiHasExplicitAPIBase(cfg map[string]any) bool {
	if jsTrim(os.Getenv("GOOGLE_GEMINI_BASE_URL")) != "" {
		return true
	}
	s, ok := cfg["geminiBaseUrl"].(string)
	return ok && jsTrim(s) != ""
}

// geminiADCAvailable is isGeminiAdcAvailable: ADC is selected, no API key and no explicit base URL
// takes precedence, a project and location are known and the ADC file exists. A config that
// cannot be parsed is an error, as upstream (the failure stays visible).
func geminiADCAvailable() (bool, error) {
	selected, err := geminiADCSelected()
	if err != nil || !selected {
		return false, err
	}
	cfg, err := loadGeminiConfig(true)
	if err != nil {
		return false, err
	}
	if geminiHasAPIKeySourceADC(cfg) || geminiHasExplicitAPIBase(cfg) {
		return false, nil
	}
	project, err := geminiADCProject()
	if err != nil {
		return false, err
	}
	location, err := geminiADCLocation()
	if err != nil {
		return false, err
	}
	if project == "" || location == "" {
		return false, nil
	}
	return fileExists(geminiADCPath()), nil
}

// redactGeminiADCToken removes the cached access token from text.
func redactGeminiADCToken(text string) string {
	geminiTokenMu.Lock()
	defer geminiTokenMu.Unlock()
	if geminiToken == nil {
		return text
	}
	return RedactCredential(text, geminiToken.token)
}
