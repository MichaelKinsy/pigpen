package browser

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/relay"
)

// DefaultTabName is the default tab name when omitted.
// upstream: DEFAULT_TAB_NAME in browser.ts:111
const DefaultTabName = "main"

// DefaultViewport is omp's default browser viewport.
// upstream: DEFAULT_VIEWPORT in launch.ts:50
var DefaultViewport = Viewport{
	Width:             1365,
	Height:            768,
	DeviceScaleFactor: 1.25,
}

// TerminalOwnerEnv is the env var naming the PID of the interactive session
// that owns the terminal pane.
// upstream: TERMINAL_OWNER_ENV in terminal-owner.ts:10
const TerminalOwnerEnv = "PI_TERMINAL_OWNER_PID"

// Settings mirrors omp's `browser.*` configuration keys.
// upstream: packages/coding-agent/src/tools/browser/settings.ts
type Settings struct {
	Enabled         bool   // browser.enabled (default true)
	CdpURL          string // browser.cdpUrl (default "")
	Relay           bool   // browser.relay (default false)
	RelayURL        string // browser.relayUrl (default "")
	Headless        bool   // browser.headless (default true)
	Cmux            bool   // browser.cmux (default true)
	Tern            bool   // browser.tern (default true)
	WebKit          bool   // browser.webkit (default true)
	WebKitPath      string // browser.webkitPath (default "")
	FreezeOnTurnEnd bool   // browser.freezeOnTurnEnd (default true)
	IdleCloseSec    int    // browser.idleCloseSec (default 1800)
	ScreenshotDir   string // browser.screenshotDir (default "")
	MaxTimeoutSec   int    // tools.maxTimeout (default 0 = unclamped beyond 300s)
}

// DefaultSettings returns omp's default browser settings.
func DefaultSettings() Settings {
	return Settings{
		Enabled:         true,
		Headless:        true,
		Cmux:            true,
		Tern:            true,
		WebKit:          true,
		FreezeOnTurnEnd: true,
		IdleCloseSec:    1800,
	}
}

// SettingsFromMap parses browser.* and tools.maxTimeout keys from an SDK
// settings map (supporting both dotted keys like "browser.headless" and nested
// maps like {"browser": {"headless": false}}). Unset keys keep DefaultSettings.
// upstream: packages/coding-agent/src/tools/browser/settings.ts
func SettingsFromMap(raw map[string]any) Settings {
	s := DefaultSettings()
	if raw == nil {
		return s
	}
	lookup := func(dotted string) (any, bool) {
		if v, ok := raw[dotted]; ok {
			return v, true
		}
		parts := strings.Split(dotted, ".")
		var cur any = raw
		for _, p := range parts {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			v, ok := m[p]
			if !ok {
				return nil, false
			}
			cur = v
		}
		return cur, true
	}
	getBool := func(key string, dst *bool) {
		if v, ok := lookup(key); ok {
			if b, ok := v.(bool); ok {
				*dst = b
			}
		}
	}
	getString := func(key string, dst *string) {
		if v, ok := lookup(key); ok {
			if str, ok := v.(string); ok {
				*dst = str
			}
		}
	}
	getInt := func(key string, dst *int) {
		if v, ok := lookup(key); ok {
			switch n := v.(type) {
			case float64:
				*dst = int(n)
			case int:
				*dst = n
			case int64:
				*dst = int(n)
			}
		}
	}

	getBool("browser.enabled", &s.Enabled)
	getString("browser.cdpUrl", &s.CdpURL)
	getBool("browser.relay", &s.Relay)
	getString("browser.relayUrl", &s.RelayURL)
	getBool("browser.headless", &s.Headless)
	getBool("browser.cmux", &s.Cmux)
	getBool("browser.tern", &s.Tern)
	getBool("browser.webkit", &s.WebKit)
	getString("browser.webkitPath", &s.WebKitPath)
	getBool("browser.freezeOnTurnEnd", &s.FreezeOnTurnEnd)
	getInt("browser.idleCloseSec", &s.IdleCloseSec)
	getString("browser.screenshotDir", &s.ScreenshotDir)
	getInt("tools.maxTimeout", &s.MaxTimeoutSec)
	return s
}

// ResolvedKind is the concrete browser target selected by ResolveBrowserKind.
// upstream: BrowserKind in registry.ts:41
type ResolvedKind struct {
	Kind       Kind
	Headless   bool
	Insecure   bool
	Path       string
	Args       []string
	CdpURL     string
	SocketPath string
	Pane       int
	Surface    string
	Password   string
	Viewer     string
}

// Key returns the deduplication key for this browser kind.
// upstream: browserKey in registry.ts:116
func (k ResolvedKind) Key() string {
	switch k.Kind {
	case KindHeadless:
		h := "0"
		if k.Headless {
			h = "1"
		}
		tls := ""
		if k.Insecure {
			tls = "tls"
		}
		return "headless:" + h + ":" + tls
	case KindSpawned:
		return fmt.Sprintf("spawned:%q:%q", k.Path, k.Args)
	case KindConnected:
		return "connected:" + k.CdpURL
	case KindRelay:
		return "relay:" + k.CdpURL
	case KindCmux:
		return "cmux:" + k.SocketPath
	case KindTern:
		return fmt.Sprintf("tern:%s:%d", k.SocketPath, k.Pane)
	case KindWebKit:
		return "webkit:" + k.Path
	default:
		return string(k.Kind)
	}
}

// Describe returns the short human description used in error messages.
// upstream: describeKind in browser.ts:1066
func (k ResolvedKind) Describe() string {
	switch k.Kind {
	case KindHeadless:
		if k.Headless {
			return "managed Chromium (headless)"
		}
		return "managed Chromium (in a window)"
	case KindSpawned:
		return "spawned:" + k.Path
	case KindConnected:
		return "connected:" + k.CdpURL
	case KindRelay:
		return "relay:" + k.CdpURL
	case KindCmux:
		surf := k.Surface
		if surf == "" {
			surf = "split"
		}
		return "cmux:" + surf
	case KindTern:
		return fmt.Sprintf("tern:pane %d", k.Pane)
	case KindWebKit:
		return "omp's headless WebKit"
	default:
		return string(k.Kind)
	}
}

// ResolveParams are the subset of open/targets parameters that influence backend choice.
type ResolveParams struct {
	App       string
	HasApp    bool
	Argv      []string
	Target    string
	HasTarget bool
	Insecure  bool
	Headed    *bool
}

// ResolveSession carries session-level inputs to ResolveBrowserKind.
type ResolveSession struct {
	Cwd       string
	TaskDepth int
	Settings  Settings
	PID       int
	Platform  string // defaults to runtime.GOOS
}

var (
	urlSchemeRE = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
	digitsRE    = regexp.MustCompile(`^\d+$`)
)

func isAppKeyword(s string) bool {
	switch s {
	case "relay", "tern", "webkit", "chromium", "own", "auto":
		return true
	}
	return false
}

// ParseFlag parses an omp boolean env flag ("1"/"true"/"yes"/"on" vs "0"/"false"/"no"/"off").
// upstream: parseFlag in @oh-my-pi/pi-utils
func ParseFlag(raw string, defaultVal bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultVal
	}
}

// IsRelayOnly reports whether PI_BROWSER_RELAY=only is active.
// upstream: isRelayOnly in relay/kind.ts:33
func IsRelayOnly(lookup func(string) (string, bool)) bool {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	v, _ := lookup("PI_BROWSER_RELAY")
	return strings.EqualFold(strings.TrimSpace(v), "only")
}

// OwnsTerminal reports whether the terminal pane in env belongs to pid.
// upstream: ownsTerminal in terminal-owner.ts:18
func OwnsTerminal(lookup func(string) (string, bool), pid int) bool {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if pid == 0 {
		pid = os.Getpid()
	}
	owner, ok := lookup(TerminalOwnerEnv)
	owner = strings.TrimSpace(owner)
	return !ok || owner == "" || owner == strconv.Itoa(pid)
}

// ResolveTernKind resolves a Tern browser PiP from env and browser.tern.
// upstream: resolveTernKind in tern/kind.ts:43
func ResolveTernKind(settingEnabled bool, lookup func(string) (string, bool)) *ResolvedKind {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	flagVal, _ := lookup("PI_BROWSER_TERN")
	if !ParseFlag(flagVal, settingEnabled) {
		return nil
	}
	sock, _ := lookup("TERN_PANE_SOCKET")
	paneStr, _ := lookup("TERN_PANE")
	sock = strings.TrimSpace(sock)
	paneStr = strings.TrimSpace(paneStr)
	if sock == "" || paneStr == "" || !digitsRE.MatchString(paneStr) {
		return nil
	}
	pane, err := strconv.Atoi(paneStr)
	if err != nil || pane < 0 {
		return nil
	}
	return &ResolvedKind{
		Kind:       KindTern,
		SocketPath: sock,
		Pane:       pane,
	}
}

// ResolveRelayKind resolves the omp browser relay kind from env and settings.
// When settingURL is empty, discovers the most recently started local relay
// endpoint from relay.DiscoverRelayEndpoints().
// upstream: resolveRelayKind in relay/kind.ts:49
func ResolveRelayKind(settingEnabled bool, settingURL string, lookup func(string) (string, bool)) *ResolvedKind {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	relayEnv, _ := lookup("PI_BROWSER_RELAY")
	if !IsRelayOnly(lookup) && !ParseFlag(relayEnv, settingEnabled) {
		return nil
	}
	u := strings.TrimSpace(settingURL)
	if u == "" {
		if endpoints := relay.DiscoverRelayEndpoints(); len(endpoints) > 0 {
			u = endpoints[0]
		}
	}
	u = strings.TrimRight(u, "/")
	return &ResolvedKind{
		Kind:   KindRelay,
		CdpURL: u,
	}
}

// ResolveCmuxKind resolves a cmux browser surface from env and browser.cmux.
// upstream: resolveCmuxKind in cmux/rpc.ts:138
func ResolveCmuxKind(settingEnabled bool, lookup func(string) (string, bool)) *ResolvedKind {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	flagVal, _ := lookup("PI_BROWSER_CMUX")
	if !ParseFlag(flagVal, settingEnabled) {
		return nil
	}
	sock, _ := lookup("CMUX_SOCKET_PATH")
	sock = strings.TrimSpace(sock)
	if sock == "" {
		return nil
	}
	pw, _ := lookup("CMUX_SOCKET_PASSWORD")
	surf, _ := lookup("CMUX_SURFACE_ID")
	return &ResolvedKind{
		Kind:       KindCmux,
		SocketPath: sock,
		Password:   strings.TrimSpace(pw),
		Surface:    strings.TrimSpace(surf),
	}
}

// ResolveWebKitKind resolves omp's WebKit host executable from env and settings.
// upstream: resolveWebKitKind in webkit/kind.ts:91
func ResolveWebKitKind(settingEnabled bool, settingPath string, lookup func(string) (string, bool), platform string) *ResolvedKind {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if platform == "" {
		platform = runtime.GOOS
	}
	flagVal, _ := lookup("PI_BROWSER_WEBKIT")
	if !ParseFlag(flagVal, settingEnabled) {
		return nil
	}
	envPath, _ := lookup("PI_BROWSER_WEBKIT_PATH")
	loc := strings.TrimSpace(envPath)
	if loc == "" {
		loc = strings.TrimSpace(settingPath)
	}
	if loc == "" {
		loc = defaultWebKitHostPath(platform)
	}
	if loc == "" {
		return nil
	}
	found := hostExecutableAt(loc, platform)
	if found == "" {
		return nil
	}
	realPath, err := filepath.EvalSymlinks(found)
	if err != nil {
		realPath = found
	}
	viewer := viewerOf(realPath)
	return &ResolvedKind{
		Kind:   KindWebKit,
		Path:   realPath,
		Viewer: viewer,
	}
}

func defaultWebKitHostPath(platform string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	name := "ABHost"
	if platform == "windows" {
		name = "ABHost.exe"
	}
	return filepath.Join(home, ".o2", "lib", "abkit", name)
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func hostExecutableAt(location, platform string) string {
	resolved, err := filepath.Abs(expandTilde(location))
	if err != nil {
		return ""
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return ""
	}
	candidate := resolved
	if st.IsDir() {
		name := "ABHost"
		if platform == "windows" {
			name = "ABHost.exe"
		}
		candidate = filepath.Join(resolved, name)
	}
	if !isExecutableFile(candidate, platform) {
		return ""
	}
	return candidate
}

func viewerOf(executable string) string {
	candidate := filepath.Join(filepath.Dir(executable), "ABView")
	if isExecutableFile(candidate, runtime.GOOS) {
		return candidate
	}
	return ""
}

func isExecutableFile(p, platform string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	if platform == "windows" {
		return true
	}
	return st.Mode()&0o111 != 0
}

func chromiumKind(params ResolveParams, settings Settings) ResolvedKind {
	headless := settings.Headless
	if params.Headed != nil {
		headless = !*params.Headed
	}
	return ResolvedKind{
		Kind:     KindHeadless,
		Headless: headless,
		Insecure: params.Insecure,
	}
}

func ownBrowserKind(params ResolveParams, sess ResolveSession, lookup func(string) (string, bool)) ResolvedKind {
	chromium := chromiumKind(params, sess.Settings)
	wk := ResolveWebKitKind(sess.Settings.WebKit, sess.Settings.WebKitPath, lookup, sess.Platform)
	if wk == nil || (!chromium.Headless && wk.Viewer == "") {
		return chromium
	}
	return *wk
}

// ResolveBrowserKind determines which browser backend an open or targets call uses.
// upstream: resolveBrowserKind in browser.ts:185
func ResolveBrowserKind(params ResolveParams, sess ResolveSession, lookup func(string) (string, bool)) (ResolvedKind, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if sess.Platform == "" {
		sess.Platform = runtime.GOOS
	}
	app := params.App
	if IsRelayOnly(lookup) {
		if params.HasApp && app != "relay" {
			return ResolvedKind{}, fmt.Errorf(
				`This session drives the user's Chrome through the omp extension and has no other browser; app: %q is unavailable. Omit app or pass app: "relay".`,
				app,
			)
		}
		panelURL, _ := lookup("PI_PANEL_RELAY_URL")
		u := strings.TrimSpace(panelURL)
		if u == "" {
			u = sess.Settings.RelayURL
		}
		if rk := ResolveRelayKind(true, u, lookup); rk != nil {
			return *rk, nil
		}
	}
	if params.HasApp && urlSchemeRE.MatchString(app) {
		return ResolvedKind{
			Kind:   KindConnected,
			CdpURL: strings.TrimRight(app, "/"),
		}, nil
	}
	if params.HasApp && app != "" && !isAppKeyword(app) {
		exe := resolveToCwd(app, sess.Cwd)
		args := ResolveSpawnArgs(exe, params.Argv, sess.Cwd)
		if params.Insecure && !containsStr(args, "--ignore-certificate-errors") {
			args = append(args, "--ignore-certificate-errors")
		}
		if !containsStr(args, "--allow-file-access-from-files") {
			args = append(args, "--allow-file-access-from-files")
		}
		return ResolvedKind{
			Kind: KindSpawned,
			Path: exe,
			Args: args,
		}, nil
	}
	relayURL := sess.Settings.RelayURL
	if app == "relay" {
		if rk := ResolveRelayKind(true, relayURL, lookup); rk != nil {
			return *rk, nil
		}
	}
	if app == "tern" {
		tk := ResolveTernKind(true, lookup)
		if tk == nil {
			return ResolvedKind{}, fmt.Errorf(
				`app: "tern" requires running inside a Tern pane (TERN_PANE_SOCKET and TERN_PANE are not set) with PI_BROWSER_TERN not set to 0.`,
			)
		}
		return *tk, nil
	}
	if app == "webkit" {
		wk := ResolveWebKitKind(true, sess.Settings.WebKitPath, lookup, sess.Platform)
		if wk == nil {
			return ResolvedKind{}, fmt.Errorf(
				`app: "webkit" requires omp's WebKit engine, and none is installed: its host executable must be %s, or be named by PI_BROWSER_WEBKIT_PATH or the browser.webkitPath setting, with PI_BROWSER_WEBKIT not set to 0.`,
				defaultWebKitHostPath(sess.Platform),
			)
		}
		return *wk, nil
	}
	if app == "chromium" {
		return chromiumKind(params, sess.Settings), nil
	}
	if app == "own" {
		return ownBrowserKind(params, sess, lookup), nil
	}

	subagent := sess.TaskDepth > 0
	if app != "auto" && !subagent {
		if rk := ResolveRelayKind(sess.Settings.Relay, relayURL, lookup); rk != nil {
			return *rk, nil
		}
	}
	configuredCdp := strings.TrimSpace(sess.Settings.CdpURL)
	if configuredCdp != "" {
		return ResolvedKind{
			Kind:   KindConnected,
			CdpURL: strings.TrimRight(configuredCdp, "/"),
		}, nil
	}
	ownsPane := OwnsTerminal(lookup, sess.PID)
	if !subagent || params.HasTarget {
		settingOn := params.HasTarget || (ownsPane && sess.Settings.Tern)
		if tk := ResolveTernKind(settingOn, lookup); tk != nil {
			return *tk, nil
		}
	}
	if !subagent {
		if ck := ResolveCmuxKind(ownsPane && sess.Settings.Cmux, lookup); ck != nil {
			return *ck, nil
		}
	}
	return ownBrowserKind(params, sess, lookup), nil
}

// NormalizeConnectedCdpURL validates and trims a CDP HTTP discovery endpoint URL.
// upstream: normalizeConnectedCdpUrl in registry.ts:224
func NormalizeConnectedCdpURL(raw string) (string, error) {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") {
		return "", fmt.Errorf(
			"browser app CDP URL must be the HTTP CDP discovery endpoint (for example http://127.0.0.1:9222), not a ws:// browser websocket URL.",
		)
	}
	return u, nil
}

// IsLoopbackURL reports whether rawURL targets localhost/loopback.
// upstream: isLoopbackRelayUrl in relay/kind.ts:19
func IsLoopbackURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

var (
	sensitiveQueryParamRE = regexp.MustCompile(`(?i)key|token|secret|auth`)
	unparseableUserinfoRE = regexp.MustCompile(`^([^:/?#]+://)[^/@]*@`)
)

// RedactURLForLog drops userinfo and fragments and redacts credential-like
// query parameter values (`key|token|secret|auth`) without reordering other
// query parameters. Unparseable URLs are cut at `?`/`#` with userinfo stripped.
// upstream: redactUrlForLog in utils/redact-url.ts:9-28
func RedactURLForLog(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		cut := rawURL
		if idx := strings.IndexAny(cut, "?#"); idx >= 0 {
			cut = cut[:idx]
		}
		return unparseableUserinfoRE.ReplaceAllString(cut, "$1")
	}
	u.User = nil
	u.Fragment = ""
	if u.RawQuery != "" {
		parts := strings.Split(u.RawQuery, "&")
		for i, part := range parts {
			if part == "" {
				continue
			}
			k, _, hasEq := strings.Cut(part, "=")
			decodedKey, derr := url.QueryUnescape(k)
			if derr != nil {
				decodedKey = k
			}
			if sensitiveQueryParamRE.MatchString(decodedKey) {
				if hasEq {
					parts[i] = k + "=" + url.QueryEscape("[redacted]")
				} else {
					parts[i] = k + "=" + url.QueryEscape("[redacted]")
				}
			}
		}
		u.RawQuery = strings.Join(parts, "&")
	}
	return u.String()
}

// SameAddress reports whether requested and current denote the same URL.
// upstream: sameAddress in navigation.ts:8
func SameAddress(requested, current string) bool {
	if requested == current {
		return true
	}
	u1, err1 := url.Parse(requested)
	u2, err2 := url.Parse(current)
	if err1 != nil || err2 != nil {
		return false
	}
	normalize := func(u *url.URL) string {
		cp := *u
		cp.Scheme = strings.ToLower(cp.Scheme)
		cp.Host = strings.ToLower(cp.Host)
		if cp.Path == "" && (cp.Scheme == "http" || cp.Scheme == "https") {
			cp.Path = "/"
		}
		return cp.String()
	}
	return normalize(u1) == normalize(u2)
}

func resolveToCwd(p, cwd string) string {
	p = expandTilde(p)
	if filepath.IsAbs(p) || cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}

func containsStr(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
