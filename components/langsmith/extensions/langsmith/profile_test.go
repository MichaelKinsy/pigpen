package langsmith

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// profileEnv points the profile code at a fresh config file and clears every other variable it reads.
func profileEnv(t *testing.T, configText string) (configPath string) {
	t.Helper()
	configPath = filepath.Join(t.TempDir(), "config.json")
	if configText != "" {
		if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"LANGSMITH_PROFILE", "HOME", "USERPROFILE"} {
		profUnsetenv(t, k)
	}
	t.Setenv("LANGSMITH_CONFIG_FILE", configPath)
	return configPath
}

// oauthConfig is a one-profile config file; an empty expiresAt omits the field.
func oauthConfig(apiURL, access, refresh, expiresAt string) string {
	var oauth []string
	if access != "" {
		oauth = append(oauth, fmt.Sprintf("%q: %q", "access_token", access))
	}
	if refresh != "" {
		oauth = append(oauth, fmt.Sprintf("%q: %q", "refresh_token", refresh))
	}
	if expiresAt != "" {
		oauth = append(oauth, fmt.Sprintf("%q: %q", "expires_at", expiresAt))
	}
	api := ""
	if apiURL != "" {
		api = fmt.Sprintf("%q: %q,\n", "api_url", apiURL)
	}
	return fmt.Sprintf("{\n  \"current_profile\": \"default\",\n  \"profiles\": {\n    \"default\": {\n%s      \"oauth\": {%s}\n    }\n  }\n}\n", api, strings.Join(oauth, ", "))
}

func expiredAt() string { return jsISOString(nowMs() - 1000) }

type tokenServer struct {
	*httptest.Server
	posts, gets atomic.Int32
	hold        chan struct{} // a token POST blocks until it is closed (or the client goes away)
	arrived     chan int      // receives the ordinal of every token POST
	respond     func(n int) (int, string)
}

func newTokenServer(t *testing.T) *tokenServer {
	ts := &tokenServer{arrived: make(chan int, 64)}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/oauth/token" {
			ts.gets.Add(1)
			w.WriteHeader(404)
			return
		}
		io.Copy(io.Discard, r.Body)
		n := int(ts.posts.Add(1))
		select {
		case ts.arrived <- n:
		default:
		}
		if ts.hold != nil {
			select {
			case <-ts.hold:
			case <-r.Context().Done():
				return
			}
		}
		status, body := 200, fmt.Sprintf(`{"access_token":"AT-%d","refresh_token":"RT-%d","expires_in":3600}`, n, n)
		if ts.respond != nil {
			status, body = ts.respond(n)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func loadAuth(t *testing.T) *profileAuth {
	t.Helper()
	cfg, err := loadProfileClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth == nil {
		t.Fatal("no profileAuth")
	}
	t.Cleanup(cfg.Auth.close)
	return cfg.Auth
}

func bearer(h *authHeader) string {
	if h == nil {
		return "<none>"
	}
	return h.Name + ": " + h.Value
}

func TestProfileConfigPathResolution(t *testing.T) {
	write := func(path, key string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		doc := fmt.Sprintf(`{"profiles":{"default":{"api_key":%q}}}`, key)
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keyOf := func() string {
		cfg, err := loadProfileClientConfig()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Auth != nil {
			defer cfg.Auth.close()
		}
		if cfg.APIKey == nil {
			return "<none>"
		}
		return *cfg.APIKey
	}
	reset := func() {
		for _, k := range []string{"LANGSMITH_CONFIG_FILE", "LANGSMITH_PROFILE", "HOME", "USERPROFILE"} {
			profUnsetenv(t, k)
		}
	}
	root := t.TempDir()
	explicit := filepath.Join(root, "explicit.json")
	home := filepath.Join(root, "home")
	userprofile := filepath.Join(root, "userprofile")
	write(explicit, "explicit")
	write(filepath.Join(home, ".langsmith", "config.json"), "home")
	write(filepath.Join(userprofile, ".langsmith", "config.json"), "userprofile")

	reset()
	if got := keyOf(); got != "<none>" {
		t.Errorf("no variable: %s", got)
	}
	t.Setenv("USERPROFILE", userprofile)
	if got := keyOf(); got != "userprofile" {
		t.Errorf("USERPROFILE only: %s", got)
	}
	t.Setenv("HOME", home)
	if got := keyOf(); got != "home" {
		t.Errorf("HOME wins over USERPROFILE: %s", got)
	}
	t.Setenv("HOME", "") // `??`: an empty HOME is not nullish, so USERPROFILE is not consulted
	if got := keyOf(); got != "<none>" {
		t.Errorf("empty HOME: %s", got)
	}
	t.Setenv("LANGSMITH_CONFIG_FILE", explicit)
	if got := keyOf(); got != "explicit" {
		t.Errorf("explicit path: %s", got)
	}
	t.Setenv("LANGSMITH_CONFIG_FILE", "") // empty is falsy: fall through to HOME (empty here)
	t.Setenv("HOME", home)
	if got := keyOf(); got != "home" {
		t.Errorf("empty LANGSMITH_CONFIG_FILE: %s", got)
	}
	// A relative HOME is joined and cleaned like path.join.
	t.Setenv("LANGSMITH_CONFIG_FILE", "")
	t.Setenv("HOME", filepath.Join(home, "x", ".."))
	if got := keyOf(); got != "home" {
		t.Errorf("HOME with ..: %s", got)
	}
}

func TestProfileNameResolution(t *testing.T) {
	cfg := `{"current_profile":"b","profiles":{"a":{"api_key":"ka"},"b":{"api_key":"kb"},"default":{"api_key":"kd"}}}`
	profileEnv(t, cfg)
	get := func() string {
		c, err := loadProfileClientConfig()
		if err != nil {
			t.Fatal(err)
		}
		if c.Auth != nil {
			defer c.Auth.close()
		}
		if c.APIKey == nil {
			return "<none>"
		}
		return *c.APIKey
	}
	if got := get(); got != "kb" {
		t.Errorf("current_profile: %s", got)
	}
	t.Setenv("LANGSMITH_PROFILE", "a")
	if got := get(); got != "ka" {
		t.Errorf("LANGSMITH_PROFILE: %s", got)
	}
	t.Setenv("LANGSMITH_PROFILE", "") // falsy: current_profile
	if got := get(); got != "kb" {
		t.Errorf("empty LANGSMITH_PROFILE: %s", got)
	}
}

func TestProfileRefusesNonStringURLAndWorkspace(t *testing.T) {
	for field, doc := range map[string]string{
		"api_url":      `{"profiles":{"default":{"api_url":5,"api_key":"k"}}}`,
		"workspace_id": `{"profiles":{"default":{"workspace_id":{"a":1},"api_key":"k"}}}`,
	} {
		profileEnv(t, doc)
		_, err := loadProfileClientConfig()
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}

func TestProfileFreshTokenNeedsNoRefreshAndIgnoresCancelledContext(t *testing.T) {
	ts := newTokenServer(t)
	profileEnv(t, oauthConfig(ts.URL, "AT", "RT", jsISOString(nowMs()+3_600_000)))
	p := loadAuth(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // waitForAbortSignal is only reached when a refresh is awaited
	h, err := p.getAuthHeader(ctx, ts.Client())
	if err != nil || bearer(h) != "Authorization: Bearer AT" {
		t.Fatalf("%v %v", bearer(h), err)
	}
	if ts.posts.Load()+ts.gets.Load() != 0 {
		t.Fatal("a fresh token caused requests")
	}
	if !p.isProfileAuthorizationHeader("Bearer AT") || p.isProfileAuthorizationHeader("Bearer other") || p.isProfileAuthorizationHeader("") {
		t.Fatal("managed Authorization value not remembered")
	}
}

func TestProfileManagedHeaderTracksRefreshAndAPIKey(t *testing.T) {
	ts := newTokenServer(t)
	profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	p := loadAuth(t)
	if !p.isProfileAuthorizationHeader("Bearer OLD") {
		t.Fatal("initial header")
	}
	h, err := p.getAuthHeader(context.Background(), ts.Client())
	if err != nil || bearer(h) != "Authorization: Bearer AT-1" {
		t.Fatalf("%v %v", bearer(h), err)
	}
	if p.isProfileAuthorizationHeader("Bearer OLD") || !p.isProfileAuthorizationHeader("Bearer AT-1") {
		t.Fatal("managed value not replaced by the refresh")
	}

	profileEnv(t, `{"profiles":{"default":{"api_key":"K"}}}`)
	k := loadAuth(t)
	if k.isProfileAuthorizationHeader("K") || k.isProfileAuthorizationHeader("") {
		t.Fatal("an x-api-key profile has no managed Authorization value")
	}
}

func TestProfileConcurrentCallersShareOneRefresh(t *testing.T) {
	ts := newTokenServer(t)
	ts.hold = make(chan struct{})
	profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	p := loadAuth(t)
	const callers = 20
	var wg sync.WaitGroup
	results := make([]string, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := p.getAuthHeader(context.Background(), ts.Client())
			if err != nil {
				results[i] = err.Error()
				return
			}
			results[i] = bearer(h)
		}()
	}
	<-ts.arrived
	time.Sleep(30 * time.Millisecond) // let the other callers join the flight
	close(ts.hold)
	wg.Wait()
	for i, r := range results {
		if r != "Authorization: Bearer AT-1" {
			t.Fatalf("caller %d: %s", i, r)
		}
	}
	if n := ts.posts.Load(); n != 1 {
		t.Fatalf("%d token requests", n)
	}
}

// Two ProfileAuth values on one file (two processes): the second waits for the lock, reloads the fresh token the
// first wrote and sends nothing.
func TestProfileTwoInstancesSerialiseOnTheLock(t *testing.T) {
	ts := newTokenServer(t)
	ts.hold = make(chan struct{})
	path := profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	first, second := loadAuth(t), loadAuth(t)
	type res struct {
		h   *authHeader
		err error
	}
	r1, r2 := make(chan res, 1), make(chan res, 1)
	go func() {
		h, err := first.getAuthHeader(context.Background(), ts.Client())
		r1 <- res{h, err}
	}()
	<-ts.arrived // the first instance holds the lock and waits for the server
	if _, err := os.Stat(lockDirOf(path)); err != nil {
		t.Fatal("first instance does not hold the lock")
	}
	go func() {
		h, err := second.getAuthHeader(context.Background(), ts.Client())
		r2 <- res{h, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		second.mu.Lock()
		started := second.flight != nil
		second.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second instance never started its refresh")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // it is now polling for the lock
	close(ts.hold)
	for _, c := range []chan res{r1, r2} {
		r := <-c
		if r.err != nil || bearer(r.h) != "Authorization: Bearer AT-1" {
			t.Fatalf("%v %v", bearer(r.h), r.err)
		}
	}
	if n := ts.posts.Load(); n != 1 {
		t.Fatalf("%d token requests for two instances", n)
	}
	if _, err := os.Stat(lockDirOf(path)); !os.IsNotExist(err) {
		t.Fatal("lock left behind")
	}
}

func TestProfileRefreshFailureKeepsOldToken(t *testing.T) {
	ts := newTokenServer(t)
	ts.respond = func(int) (int, string) { return 500, `{"error":"boom"}` }
	text := oauthConfig(ts.URL, "OLD", "RT", expiredAt())
	path := profileEnv(t, text)
	p := loadAuth(t)
	for range 2 { // every call retries: the failure is not cached
		h, err := p.getAuthHeader(context.Background(), ts.Client())
		if err != nil || bearer(h) != "Authorization: Bearer OLD" {
			t.Fatalf("%v %v", bearer(h), err)
		}
	}
	if n := ts.posts.Load(); n != 2 {
		t.Fatalf("%d token requests", n)
	}
	if b, _ := os.ReadFile(path); string(b) != text {
		t.Fatalf("config rewritten after a failed refresh:\n%s", b)
	}
	if _, err := os.Stat(lockDirOf(path)); !os.IsNotExist(err) {
		t.Fatal("lock left behind")
	}
}

func TestProfileRefreshRewritesAtomicallyWithMode0600(t *testing.T) {
	ts := newTokenServer(t)
	path := profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	p := loadAuth(t)
	if _, err := p.getAuthHeader(context.Background(), ts.Client()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(b), "}\n") || !strings.Contains(string(b), `"access_token": "AT-1"`) {
		t.Fatalf("config:\n%s", b)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode().Perm())
		}
	}
}

func TestProfileContextEndsWhileRefreshing(t *testing.T) {
	ts := newTokenServer(t)
	ts.hold = make(chan struct{})
	path := profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	p := loadAuth(t)

	// Already aborted: the refresh starts anyway, the caller gets the abort reason.
	cancelled, cancel := context.WithCancelCause(context.Background())
	boom := errors.New("aborted by test")
	cancel(boom)
	if _, err := p.getAuthHeader(cancelled, ts.Client()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	// Aborting later leaves the refresh running.
	ctx, cancel2 := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := p.getAuthHeader(ctx, ts.Client())
		errc <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel2()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	close(ts.hold) // the refresh that outlived both callers completes
	h, err := p.getAuthHeader(context.Background(), ts.Client())
	if err != nil || bearer(h) != "Authorization: Bearer AT-1" {
		t.Fatalf("%v %v", bearer(h), err)
	}
	if n := ts.posts.Load(); n != 1 {
		t.Fatalf("%d token requests: an aborted caller restarted the refresh", n)
	}
	p.close()
	if _, err := os.Stat(lockDirOf(path)); !os.IsNotExist(err) {
		t.Fatal("lock left behind")
	}
}

func TestProfileRefreshDeadline(t *testing.T) {
	prev := tokenRefreshTimeout
	tokenRefreshTimeout = 300 * time.Millisecond
	t.Cleanup(func() { tokenRefreshTimeout = prev })
	ts := newTokenServer(t)
	ts.hold = make(chan struct{}) // the token endpoint never answers
	path := profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	p := loadAuth(t)
	start := time.Now()
	h, err := p.getAuthHeader(context.Background(), ts.Client())
	if err != nil || bearer(h) != "Authorization: Bearer OLD" {
		t.Fatalf("%v %v", bearer(h), err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 5*time.Second {
		t.Fatalf("returned after %v", d)
	}
	if _, err := os.Stat(lockDirOf(path)); !os.IsNotExist(err) {
		t.Fatal("lock left behind after the deadline")
	}
}

func TestProfileLockHeldElsewhereSkipsRefresh(t *testing.T) {
	prev := tokenRefreshTimeout
	tokenRefreshTimeout = 250 * time.Millisecond
	t.Cleanup(func() { tokenRefreshTimeout = prev })
	ts := newTokenServer(t)
	path := profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	dir := foreignLock(t, path, new(jsISOString(nowMs())+"\nanother-process\n"))
	p := loadAuth(t)
	start := time.Now()
	h, err := p.getAuthHeader(context.Background(), ts.Client())
	if err != nil || bearer(h) != "Authorization: Bearer OLD" {
		t.Fatalf("%v %v", bearer(h), err)
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Fatalf("gave up after %v", d)
	}
	if ts.posts.Load()+ts.gets.Load() != 0 {
		t.Fatal("refreshed without the lock")
	}
	if o, _ := lockOwner(dir); o != "another-process" {
		t.Fatalf("the other holder's lock was touched: %q", o)
	}
}

func TestProfileCloseCancelsLockWait(t *testing.T) {
	ts := newTokenServer(t)
	path := profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	foreignLock(t, path, new(jsISOString(nowMs())+"\nanother-process\n"))
	p := loadAuth(t)
	done := make(chan *authHeader, 1)
	go func() {
		h, _ := p.getAuthHeader(context.Background(), ts.Client())
		done <- h
	}()
	for {
		p.mu.Lock()
		started := p.flight != nil
		p.mu.Unlock()
		if started {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	start := time.Now()
	p.close()
	p.close() // idempotent
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("close took %v", d)
	}
	if h := <-done; bearer(h) != "Authorization: Bearer OLD" {
		t.Fatalf("%s", bearer(h))
	}
}

func TestProfileDefaultAPIURL(t *testing.T) {
	profileEnv(t, `{"profiles":{"default":{"oauth":{"access_token":"OLD","refresh_token":"RT","expires_at":"`+expiredAt()+`"}}}}`)
	p := loadAuth(t)
	var mu sync.Mutex
	var seen []string
	hc := &http.Client{Transport: profRT(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.String())
		mu.Unlock()
		return profTextResponse(404, ""), nil
	})}
	if _, err := p.getAuthHeader(context.Background(), hc); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET https://api.smith.langchain.com/.well-known/oauth-authorization-server",
		"GET https://api.smith.langchain.com/.well-known/oauth-authorization-server/api",
		"GET https://api.smith.langchain.com/api/.well-known/oauth-authorization-server",
		"POST https://api.smith.langchain.com/oauth/token",
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s", strings.Join(seen, "\n"))
	}
}

func TestProfileOversizedTokenResponseIsIgnored(t *testing.T) {
	ts := newTokenServer(t)
	ts.respond = func(int) (int, string) {
		return 200, `{"access_token":"HUGE","pad":"` + strings.Repeat("a", profileMaxBodyBytes+10) + `"}`
	}
	text := oauthConfig(ts.URL, "OLD", "RT", expiredAt())
	path := profileEnv(t, text)
	p := loadAuth(t)
	h, err := p.getAuthHeader(context.Background(), ts.Client())
	if err != nil || bearer(h) != "Authorization: Bearer OLD" {
		t.Fatalf("%v %v", bearer(h), err)
	}
	if b, _ := os.ReadFile(path); string(b) != text {
		t.Fatal("config rewritten")
	}
}

// api_url is trimmed outside the try block of refreshOAuthToken: a refresh that finds a non-string there (after a
// reload brought it in) rejects, and so does getAuthHeader.
func TestProfileRefreshWithNonStringAPIURLAfterReloadFails(t *testing.T) {
	ts := newTokenServer(t)
	ts.respond = func(int) (int, string) { return 200, `{"access_token":"AT","expires_in":1}` }
	path := profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	p := loadAuth(t)
	swapped := fmt.Sprintf(`{"current_profile":"default","profiles":{"default":{"api_url":5,"oauth":{"access_token":"X","refresh_token":"RT","expires_at":%q}}}}`, expiredAt())
	if err := os.WriteFile(path, []byte(swapped), 0o600); err != nil {
		t.Fatal(err)
	}
	// The first refresh read the original string, reloaded the swapped file, got the token and rewrote the file.
	if h, err := p.getAuthHeader(context.Background(), ts.Client()); err != nil || bearer(h) != "Authorization: Bearer AT" {
		t.Fatalf("%v %v", bearer(h), err)
	}
	// AT expires within the leeway: the next refresh trims api_url: 5.
	if _, err := p.getAuthHeader(context.Background(), ts.Client()); !errors.Is(err, errTrimNotFunction) {
		t.Fatalf("err = %v", err)
	}
}

func TestProfileExpiresAtDateParserFailureIsReturned(t *testing.T) {
	profileEnv(t, `{"profiles":{"default":{"oauth":{"access_token":"A","refresh_token":"R","expires_at":"next thursday"}}}}`)
	p := loadAuth(t)
	// No host to answer Date.parse for a non-ISO text: the error surfaces instead of a guessed answer.
	if _, err := p.getAuthHeader(context.Background(), http.DefaultClient); err == nil {
		t.Fatal("expected the date parser's error")
	}
}

func TestProfileFetchRequestURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://Example.com:443/a b?q=1 2#frag": "https://example.com/a%20b?q=1%202",
		"http://[::1]:8080/x":                    "http://[::1]:8080/x",
	} {
		if got, err := fetchRequestURL(in); err != nil || got != want {
			t.Errorf("%q -> %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/relative", "ftp://h/x", "file:///x", "http://u:p@h/", "http://u@h/", "http://:p@h/", "javascript:1", "http://"} {
		if got, err := fetchRequestURL(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func BenchmarkProfileGetAuthHeaderNoRefresh(b *testing.B) {
	path := filepath.Join(b.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"profiles":{"default":{"oauth":{"access_token":"AT","refresh_token":"RT","expires_at":"2099-01-01T00:00:00.000Z"}}}}`), 0o600)
	b.Setenv("LANGSMITH_CONFIG_FILE", path)
	cfg, err := loadProfileClientConfig()
	if err != nil || cfg.Auth == nil {
		b.Fatal(err)
	}
	defer cfg.Auth.close()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if h, err := cfg.Auth.getAuthHeader(ctx, nil); err != nil || h == nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProfileLoadClientConfig(b *testing.B) {
	path := filepath.Join(b.TempDir(), "config.json")
	os.WriteFile(path, []byte(oauthConfig("https://api.example.test", "AT", "RT", "2099-01-01T00:00:00.000Z")), 0o600)
	b.Setenv("LANGSMITH_CONFIG_FILE", path)
	b.ReportAllocs()
	for b.Loop() {
		cfg, err := loadProfileClientConfig()
		if err != nil {
			b.Fatal(err)
		}
		cfg.Auth.close()
	}
}

func BenchmarkProfileJSONRoundTrip(b *testing.B) {
	doc := oauthConfig("https://api.example.test", "AT", "RT", "2099-01-01T00:00:00.000Z")
	b.ReportAllocs()
	buf := make([]byte, 0, 1024)
	for b.Loop() {
		v, ok := parseJSONOrdered(doc)
		if !ok {
			b.Fatal("parse")
		}
		var err error
		if buf, err = stringifyIndent2(buf[:0], v); err != nil {
			b.Fatal(err)
		}
	}
}
