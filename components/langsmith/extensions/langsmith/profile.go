package langsmith

// Port of utils/profiles.js (langsmith 0.10.5): the LangSmith CLI profile (~/.langsmith/config.json,
// LANGSMITH_CONFIG_FILE, LANGSMITH_PROFILE) that supplies api_url, workspace_id and an API key or OAuth tokens
// (with refresh) when the environment sets none. The cross-process lock is profile_lock.go; the JSON, URL and
// UTF-8 primitives with JavaScript semantics are profile_json.go and profile_url.go.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultAPIURL = "https://api.smith.langchain.com" // DEFAULT_API_URL (profiles.js:4)

const (
	oauthClientID      = "langsmith-cli"                           // profiles.js:5
	tokenRefreshLeeway = 60_000                                    // TOKEN_REFRESH_LEEWAY_MS, profiles.js:6
	wellKnownOAuthPath = "/.well-known/oauth-authorization-server" // profiles.js:9
	// profileMaxBodyBytes bounds a response body read into memory (the original reads it unbounded).
	profileMaxBodyBytes = 8 << 20
)

// Timeouts of the refresh. Variables so tests can shorten them; nothing else assigns them.
var (
	tokenRefreshTimeout   = 10 * time.Second // TOKEN_REFRESH_TIMEOUT_MS, profiles.js:7
	oauthDiscoveryTimeout = 5 * time.Second  // OAUTH_DISCOVERY_TIMEOUT_MS, profiles.js:8
)

// errTrimNotFunction is the TypeError `trimConfigValue` throws for a configured value that is neither a string nor
// nullish (profiles.js:62-64: `value?.trim()` on a number, boolean, object or array).
var errTrimNotFunction = errors.New("TypeError: value?.trim is not a function")

// authHeader is {name, value}.
type authHeader struct{ Name, Value string }

// profileClientConfig is loadProfileClientConfig's result; nil pointers are `undefined`.
type profileClientConfig struct {
	APIURL            *string
	APIKey            *string
	WorkspaceID       *string
	OAuthAccessToken  *string
	OAuthRefreshToken *string
	Auth              *profileAuth
}

// profileState is profiles.js ProfileState: the parsed file, the resolved profile name and the profile object
// (an *omap inside config).
type profileState struct {
	configPath  string
	config      *omap
	profileName string
	profile     any
}

// getProfileConfigPath is profiles.js:14-26.
func getProfileConfigPath() (string, bool) {
	if p, ok := getEnvironmentVariable("LANGSMITH_CONFIG_FILE"); ok && p != "" {
		return p, true
	}
	home, ok := getEnvironmentVariable("HOME") // `??`: an empty HOME does not fall through to USERPROFILE
	if !ok {
		home, ok = getEnvironmentVariable("USERPROFILE")
	}
	if !ok || home == "" {
		return "", false
	}
	return filepath.Join(home, ".langsmith", "config.json"), true
}

// profileByName is `profiles?.[name]`: undef when missing. A name that only exists on Object.prototype (`constructor`,
// `toString`, ...) is a truthy function in JavaScript with no profile fields; it reads as "no profile" here, which no
// reader of the result can tell apart.
func profileByName(profiles any, name string) any {
	switch p := profiles.(type) {
	case *omap:
		return p.val(name)
	case []any:
		if isArrayIndex(name) {
			if n, ok := arrayIndex(name); ok && n < len(p) {
				return p[n]
			}
		}
	}
	return undef
}

func arrayIndex(k string) (int, bool) {
	n := 0
	for i := range len(k) {
		n = n*10 + int(k[i]-'0')
		if n > math.MaxInt32 {
			return 0, false
		}
	}
	return n, true
}

// resolveProfileName is profiles.js:27-39.
func resolveProfileName(config *omap) (string, bool) {
	if p, ok := getEnvironmentVariable("LANGSMITH_PROFILE"); ok && p != "" {
		return p, true
	}
	if cp := config.val("current_profile"); pfTruthy(cp) {
		return pfToString(cp), true
	}
	if pfTruthy(profileByName(config.val("profiles"), "default")) {
		return "default", true
	}
	return "", false
}

// loadProfileState is profiles.js:40-58. The runtime is never a browser, so the isBrowserLikeRuntime guard is dead.
func loadProfileState() *profileState {
	configPath, ok := getProfileConfigPath()
	if !ok || !pathExists(configPath) {
		return nil
	}
	text, err := readFileUTF8(configPath)
	if err != nil {
		return nil
	}
	parsed, ok := parseJSONOrdered(text)
	if !ok {
		return nil
	}
	config, ok := parsed.(*omap) // JSON `null` makes `config.current_profile` throw; any other non-object has no profiles
	if !ok {
		return nil
	}
	name, ok := resolveProfileName(config)
	if !ok {
		return nil
	}
	profile := profileByName(config.val("profiles"), name)
	if !pfTruthy(profile) {
		return nil
	}
	return &profileState{configPath: configPath, config: config, profileName: name, profile: profile}
}

// hasValue is profiles.js hasValue: not undefined/null and not blank.
func hasValue(s *string) bool { return s != nil && pfTrim(*s) != "" }

// trimConfigValue is profiles.js trimConfigValue: ok false is `undefined`; err is the TypeError of a value that is
// not a string.
func trimConfigValue(v any) (s string, ok bool, err error) {
	switch x := v.(type) {
	case nil, undefinedT:
		return "", false, nil
	case string:
		s = pfTrim(x)
		// .replace(/^["']|["']$/g, ""): one leading and one trailing quote.
		if s != "" && (s[0] == '"' || s[0] == '\'') {
			s = s[1:]
		}
		if s != "" && (s[len(s)-1] == '"' || s[len(s)-1] == '\'') {
			s = s[:len(s)-1]
		}
		return s, true, nil
	}
	return "", false, errTrimNotFunction
}

// trimmedPtr is trimConfigValue as a *string.
func trimmedPtr(v any) (*string, error) {
	s, ok, err := trimConfigValue(v)
	if err != nil || !ok {
		return nil, err
	}
	return &s, nil
}

// optionalString is a config value the caller returns as is: a string, or nil for null/undefined. Any other type has
// no *string form: the original passes it through unchanged and fails later in code this port does not reach, so it
// is refused by name instead of silently coerced.
func optionalString(v any, field string) (*string, error) {
	switch x := v.(type) {
	case nil, undefinedT:
		return nil, nil
	case string:
		return &x, nil
	}
	return nil, fmt.Errorf("unsupported langsmith profile: %s must be a string", field)
}

// loadProfileClientConfig is profiles.js loadProfileClientConfig; it reads the config file synchronously. The error is
// the TypeError the original throws for an api_key or OAuth token that is not a string, or the refusal of an api_url or
// workspace_id that is not.
func loadProfileClientConfig() (profileClientConfig, error) {
	state := loadProfileState()
	if state == nil {
		return profileClientConfig{}, nil
	}
	if !isRecord(state.profile) {
		return profileClientConfig{}, nil // every field of a non-object reads undefined
	}
	profile := state.profile
	apiKey, err := trimmedPtr(recGet(profile, "api_key"))
	if err != nil {
		return profileClientConfig{}, err
	}
	oauth := recGet(profile, "oauth")
	access, err := trimmedPtr(recGet(oauth, "access_token"))
	if err != nil {
		return profileClientConfig{}, err
	}
	refresh, err := trimmedPtr(recGet(oauth, "refresh_token"))
	if err != nil {
		return profileClientConfig{}, err
	}
	apiURL, err := optionalString(recGet(profile, "api_url"), "api_url")
	if err != nil {
		return profileClientConfig{}, err
	}
	workspaceID, err := optionalString(recGet(profile, "workspace_id"), "workspace_id")
	if err != nil {
		return profileClientConfig{}, err
	}
	cfg := profileClientConfig{APIURL: apiURL, APIKey: apiKey, WorkspaceID: workspaceID, OAuthAccessToken: access, OAuthRefreshToken: refresh}
	if truthyStr(apiKey) || truthyStr(access) || truthyStr(refresh) {
		cfg.Auth, err = newProfileAuth(state)
		if err != nil {
			return profileClientConfig{}, err
		}
	}
	return cfg, nil
}

func truthyStr(s *string) bool { return s != nil && *s != "" }

// shouldRefreshProfileToken is profiles.js:65-83.
func shouldRefreshProfileToken(profile any) (bool, error) {
	oauth := recGet(profile, "oauth")
	if !pfTruthy(recGet(oauth, "refresh_token")) {
		return false, nil
	}
	if !pfTruthy(recGet(oauth, "access_token")) {
		return true, nil
	}
	expiresAtVal := recGet(oauth, "expires_at")
	if !pfTruthy(expiresAtVal) {
		return false, nil
	}
	expiresAt, ok, err := parseDateMs(pfToString(expiresAtVal))
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return expiresAt <= float64(profileNowMs())+tokenRefreshLeeway, nil
}

// normalizeConfigUrl is profiles.js:84-93.
func normalizeConfigURL(apiURL string) string {
	n := strings.TrimRight(apiURL, "/")
	return strings.TrimSuffix(n, "/api/v1")
}

// oauthDiscoveryCandidates is profiles.js:100-111: metadata base URLs to probe, most specific first.
func oauthDiscoveryCandidates(apiURL string) []string {
	given := normalizeConfigURL(apiURL)
	origin := strings.TrimSuffix(given, "/api")
	candidates := make([]string, 0, 3)
	for _, c := range [...]string{given, origin + "/api", origin} {
		if c == "" || c == "/api" {
			continue
		}
		dup := false
		for _, e := range candidates {
			if e == c {
				dup = true
				break
			}
		}
		if !dup {
			candidates = append(candidates, c)
		}
	}
	return candidates
}

// isTrustedOAuthMetadata is profiles.js:119-148: the issuer must match the probed base (RFC 8414) and the device and
// token endpoints must share the issuer's scheme and host.
func isTrustedOAuthMetadata(doc any, base string) bool {
	issuer, ok := recGet(doc, "issuer").(string)
	if !ok || strings.TrimRight(issuer, "/") != strings.TrimRight(base, "/") {
		return false
	}
	issuerURL, ok := parseWhatwgURL(issuer)
	if !ok {
		return false
	}
	for _, k := range [...]string{"device_authorization_endpoint", "token_endpoint"} {
		endpoint, ok := recGet(doc, k).(string)
		if !ok || endpoint == "" {
			return false
		}
		u, ok := parseWhatwgURL(endpoint)
		if !ok || u.protocol() != issuerURL.protocol() || u.hostPort() != issuerURL.hostPort() {
			return false
		}
	}
	return true
}

// oauthMetadataUrls is profiles.js:158-169: the RFC 8414 form (well-known inserted after the origin) first, then the
// appended form.
func oauthMetadataUrls(base string) []string {
	appended := base + wellKnownOAuthPath
	u, ok := parseWhatwgURL(base)
	if !ok {
		return []string{appended}
	}
	path := u.path
	if path == "/" {
		path = ""
	}
	inserted := u.origin() + wellKnownOAuthPath + path
	if inserted == appended {
		return []string{inserted}
	}
	return []string{inserted, appended}
}

// profileFetch is `fetchImplementation(url, {method, headers, body, signal})`: a request that follows redirects and
// fails like fetch for a URL fetch rejects. The caller closes the body.
func profileFetch(ctx context.Context, hc *http.Client, method, rawURL string, headers [][2]string, body string) (*http.Response, error) {
	target, err := fetchRequestURL(rawURL)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != "" || method == http.MethodPost {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*") // fetch's default Accept
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(req)
}

// responseOK is `response.ok`.
func responseOK(r *http.Response) bool { return r.StatusCode >= 200 && r.StatusCode <= 299 }

// responseJSON is `await response.json()`: the body decoded as UTF-8 (a BOM dropped) and parsed. ok is false where the
// original throws.
func responseJSON(r *http.Response) (any, bool) {
	b, err := io.ReadAll(io.LimitReader(r.Body, profileMaxBodyBytes+1))
	if err != nil || len(b) > profileMaxBodyBytes {
		return nil, false
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	return parseJSONOrdered(decodeUTF8Replacing(b))
}

// fetchOAuthMetadata is profiles.js:170-196.
func fetchOAuthMetadata(ctx context.Context, hc *http.Client, url, base string) any {
	ctx, cancel := context.WithTimeout(ctx, oauthDiscoveryTimeout)
	defer cancel()
	resp, err := profileFetch(ctx, hc, http.MethodGet, url, [][2]string{{"Accept", "application/json"}}, "")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if !responseOK(resp) {
		return nil
	}
	// Self-hosted answers unknown paths with an HTML 200 from the SPA.
	doc, ok := responseJSON(resp)
	if !ok || !isTrustedOAuthMetadata(doc, base) { // a non-object (or an array) has no issuer: untrusted
		return nil
	}
	return doc
}

// resolveTokenEndpoint is profiles.js:197-210: the token endpoint of the deployment's metadata document, else
// `<mount>/oauth/token` (keeping a trailing `/api`).
func resolveTokenEndpoint(ctx context.Context, apiURL string, hc *http.Client) string {
	for _, base := range oauthDiscoveryCandidates(apiURL) {
		for _, url := range oauthMetadataUrls(base) {
			if doc := fetchOAuthMetadata(ctx, hc, url, base); doc != nil {
				return recGet(doc, "token_endpoint").(string)
			}
		}
	}
	return normalizeConfigURL(apiURL) + "/oauth/token"
}

// currentAuthHeaderFromProfile is profiles.js:355-364.
func currentAuthHeaderFromProfile(profile any) (*authHeader, error) {
	oauth := recGet(profile, "oauth")
	access, ok, err := trimConfigValue(recGet(oauth, "access_token"))
	if err != nil {
		return nil, err
	}
	if ok && access != "" {
		return &authHeader{"Authorization", "Bearer " + access}, nil
	}
	refresh, ok, err := trimConfigValue(recGet(oauth, "refresh_token"))
	if err != nil {
		return nil, err
	}
	if ok && refresh != "" {
		return nil, nil
	}
	return authHeaderFromProfile(profile)
}

// authHeaderFromProfile is profiles.js:365-376.
func authHeaderFromProfile(profile any) (*authHeader, error) {
	oauth := recGet(profile, "oauth")
	access, ok, err := trimConfigValue(recGet(oauth, "access_token"))
	if err != nil {
		return nil, err
	}
	if ok && access != "" {
		return &authHeader{"Authorization", "Bearer " + access}, nil
	}
	key, ok, err := trimConfigValue(recGet(profile, "api_key"))
	if err != nil {
		return nil, err
	}
	if ok && key != "" {
		return &authHeader{"x-api-key", key}, nil
	}
	return nil, nil
}

// refreshFlight is the shared in-flight refresh (`refreshPromise`).
type refreshFlight struct {
	done chan struct{}
	err  error
}

// profileAuth is the ProfileAuth class. It is safe for concurrent use: callers share one refresh, as every caller
// of the original awaits the same `refreshPromise`.
type profileAuth struct {
	mu         sync.Mutex
	state      *profileState
	flight     *refreshFlight
	managed    string // managedAuthorizationValue
	hasManaged bool

	ctx    context.Context // ends the refresh's waits at close()
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newProfileAuth is `new ProfileAuth(state)`.
func newProfileAuth(state *profileState) (*profileAuth, error) {
	p := &profileAuth{state: state}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	if _, err := p.currentAuthHeader(); err != nil {
		p.cancel()
		return nil, err
	}
	return p, nil
}

// close cancels a refresh in progress and waits for it. The profile stays usable for reading.
func (p *profileAuth) close() {
	p.cancel()
	p.wg.Wait()
}

func (p *profileAuth) rememberProfileAuthHeader(h *authHeader) {
	if h != nil && h.Name == "Authorization" {
		p.managed, p.hasManaged = h.Value, true
	} else {
		p.managed, p.hasManaged = "", false
	}
}

// currentAuthHeader is ProfileAuth.currentAuthHeader (no refresh). The error is a TypeError of the original.
func (p *profileAuth) currentAuthHeader() (*authHeader, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, err := currentAuthHeaderFromProfile(p.state.profile)
	if err != nil {
		return nil, err
	}
	p.rememberProfileAuthHeader(h)
	return h, nil
}

// getAuthHeader is ProfileAuth.getAuthHeader: it refreshes an expiring OAuth token first (cross-process lock,
// discovery, POST to the token endpoint, atomic config rewrite), using hc for requests. ctx plays the fetch
// `signal`: its end while a refresh is awaited returns context.Cause(ctx) and leaves the refresh running.
func (p *profileAuth) getAuthHeader(ctx context.Context, hc *http.Client) (*authHeader, error) {
	p.mu.Lock()
	should, err := shouldRefreshProfileToken(p.state.profile)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	var flight *refreshFlight
	if should {
		if p.flight == nil {
			p.flight = &refreshFlight{done: make(chan struct{})}
			p.wg.Add(1)
			go p.runRefresh(p.flight, hc)
		}
		flight = p.flight
	}
	p.mu.Unlock()
	if flight != nil {
		if ctx.Err() != nil { // waitForAbortSignal: an already aborted signal throws before waiting
			return nil, context.Cause(ctx)
		}
		select {
		case <-flight.done:
			if flight.err != nil {
				return nil, flight.err
			}
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	h, err := authHeaderFromProfile(p.state.profile)
	if err != nil {
		return nil, err
	}
	p.rememberProfileAuthHeader(h)
	return h, nil
}

// isProfileAuthorizationHeader is ProfileAuth.isProfileAuthorizationHeader.
func (p *profileAuth) isProfileAuthorizationHeader(value string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hasManaged && value == p.managed
}

func (p *profileAuth) runRefresh(f *refreshFlight, hc *http.Client) {
	defer p.wg.Done()
	err := p.refreshOAuthToken(hc)
	p.mu.Lock()
	p.flight = nil
	f.err = err
	p.mu.Unlock()
	close(f.done)
}

// reloadProfile is ProfileAuth.reloadProfile (profiles.js:274-289); the caller holds p.mu.
func (p *profileAuth) reloadProfile() any {
	text, err := readFileUTF8(p.state.configPath)
	if err != nil {
		return nil
	}
	parsed, ok := parseJSONOrdered(text)
	if !ok {
		return nil
	}
	config, ok := parsed.(*omap)
	if !ok {
		return nil
	}
	profile := profileByName(config.val("profiles"), p.state.profileName)
	if !pfTruthy(profile) {
		return nil
	}
	p.state.config = config
	p.state.profile = profile
	return profile
}

// applyTokenResponse is profiles.js:211-221 on the OAuth object of the profile; the caller holds p.mu. ok is false
// where the original throws (after the assignments made so far). `profile.oauth ??= {}` never creates the object here:
// a refresh only runs for a profile whose oauth object holds a refresh_token, so oauth is always an object.
func applyTokenResponse(profile *omap, token any) (ok bool) {
	oauth, isObject := profile.val("oauth").(*omap)
	if !isObject {
		return false
	}
	if at := recGet(token, "access_token"); pfTruthy(at) {
		oauth.set("access_token", at)
	}
	if rt := recGet(token, "refresh_token"); pfTruthy(rt) {
		oauth.set("refresh_token", rt)
	}
	if expiresIn, isNum := recGet(token, "expires_in").(float64); isNum && expiresIn > 0 {
		// new Date(Date.now() + expires_in * 1000).toISOString(): TimeClip, and a RangeError past ±8.64e15 ms.
		t := float64(profileNowMs()) + expiresIn*1000
		if math.IsNaN(t) || math.Abs(t) > 8.64e15 {
			return false
		}
		oauth.set("expires_at", jsISOString(int64(t)))
	}
	return true
}

// refreshOAuthToken is ProfileAuth.refreshOAuthToken (profiles.js:290-346). Its error is only what the original lets
// escape: the trim of api_url, which sits outside the try block, and a failing lock release in `finally`.
func (p *profileAuth) refreshOAuthToken(hc *http.Client) (err error) {
	p.mu.Lock()
	refreshToken := recGet(recGet(p.state.profile, "oauth"), "refresh_token")
	if !pfTruthy(refreshToken) {
		p.mu.Unlock()
		return nil
	}
	apiURLVal, hasAPIURL, terr := trimConfigValue(recGet(p.state.profile, "api_url"))
	configPath := p.state.configPath
	p.mu.Unlock()
	if terr != nil {
		return terr
	}
	refreshAPIURL := defaultAPIURL
	if hasAPIURL {
		refreshAPIURL = apiURLVal
	}
	deadline := profileNowMs() + tokenRefreshTimeout.Milliseconds()

	var lock *oauthRefreshLock
	defer func() { // finally { await lock?.release() }
		if rerr := lock.release(); rerr != nil {
			err = rerr
		}
	}()
	lock, lerr := acquireOAuthRefreshLock(p.ctx, configPath, deadline)
	if lerr != nil {
		return nil
	}

	p.mu.Lock()
	fresh := p.reloadProfile()
	if fresh != nil {
		should, serr := shouldRefreshProfileToken(p.state.profile)
		if serr != nil || !should {
			p.mu.Unlock()
			return nil
		}
	}
	// `oauth?.refresh_token ?? refreshToken`: the reload only succeeds, and the check above only passes, for a profile
	// that still holds a refresh token, so the fallback never applies.
	rt := recGet(recGet(p.state.profile, "oauth"), "refresh_token")
	p.mu.Unlock()

	body := make([]byte, 0, 96)
	body = append(body, "grant_type=refresh_token&client_id="...)
	body = formURLEncode(body, oauthClientID)
	body = append(body, "&refresh_token="...)
	body = formURLEncode(body, pfToString(rt))

	tokenEndpoint := resolveTokenEndpoint(p.ctx, refreshAPIURL, hc)
	remaining := max(0, deadline-profileNowMs())
	ctx, cancel := context.WithTimeout(p.ctx, time.Duration(remaining)*time.Millisecond)
	defer cancel()
	resp, ferr := profileFetch(ctx, hc, http.MethodPost, tokenEndpoint, [][2]string{{"Content-Type", "application/x-www-form-urlencoded"}}, string(body))
	if ferr != nil {
		return nil
	}
	defer resp.Body.Close()
	if !responseOK(resp) {
		return nil
	}
	token, ok := responseJSON(resp)
	if !ok || !pfTruthy(recGet(token, "access_token")) {
		return nil
	}

	p.mu.Lock()
	profile, isProfile := p.state.profile.(*omap)
	if !isProfile || !applyTokenResponse(profile, token) {
		p.mu.Unlock()
		return nil
	}
	// `config.profiles ??= {}; config.profiles[name] = profile` stores the object that was found in config.profiles
	// under the name it was found by: a no-op here, so nothing is done.
	content, serr := stringifyIndent2(make([]byte, 0, 512), p.state.config)
	p.mu.Unlock()
	if serr != nil {
		return nil
	}
	content = append(content, '\n')
	_ = writeFileAtomic(configPath, content) // the original swallows a failed write; the token stays in memory
	return nil
}
