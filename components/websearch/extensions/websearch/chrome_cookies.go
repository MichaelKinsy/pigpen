package websearch

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// Port of chrome-cookies.ts: Chromium cookie extraction for Gemini Web and authenticated fetch.
// It is opt-in (isBrowserCookieAccessAllowed) and runs the original's programs through the host
// command runner: `security` (macOS keychain), `secret-tool` and `kwallet-query` (Linux),
// `powershell.exe` (Windows DPAPI), and `sqlite3` or `python3` to read a copy of the cookie
// database. Nothing here prints or logs a password or a cookie value.
//
// Differences from the original, all recorded in port/PORT.md: node:sqlite is not available to
// a Go extension, so the SQLite CLI is tried first and python3 second; the Windows DPAPI blob is
// embedded in the encoded script because the host command runner has no environment option; a
// command that cannot start is told from a failing one by looking the program up on PATH,
// because the host reports both as exit code 1.

// CookieMap maps a cookie name to its value.
type CookieMap = map[string]string

// BrowserCookieAttempt is one profile tried (BrowserCookieAttemptDiagnostic upstream).
type BrowserCookieAttempt struct {
	Browser string `json:"browser"`
	Profile string `json:"profile"`
	Status  string `json:"status"`
}

// BrowserCookieDiagnosticDetails is the last extraction's diagnostic with its attempts.
type BrowserCookieDiagnosticDetails struct {
	Message  string                 `json:"message"`
	Attempts []BrowserCookieAttempt `json:"attempts"`
}

// BrowserCookieOptions are the options of getBrowserCookiesForHosts.
type BrowserCookieOptions struct {
	Hosts           []string
	Browser         string
	Profile         string
	RequiredCookies []string
	CookieNames     []string
	RequiredLabel   string
	RequestURL      *url.URL
}

// BrowserCookies is a successful extraction.
type BrowserCookies struct {
	Cookies      CookieMap
	Warnings     []string
	CookieHeader string
}

// GoogleCookieOptions are the options of getGoogleCookies.
type GoogleCookieOptions struct {
	Browser         string
	Profile         string
	RequiredCookies []string
}

// Test seams: the platform, the program lookup and the clock.
var (
	cookieGOOS     = runtime.GOOS
	cookieLookPath = exec.LookPath
	cookieNow      = time.Now
)

const (
	cookieCommandTimeout = 5 * time.Second
	cookieMaxBuffer      = 1024 * 1024
	chromeEpochMillis    = 11644473600000
)

var googleOrigins = []string{"gemini.google.com", "accounts.google.com", "www.google.com"}

var allCookieNames = []string{
	"__Secure-1PSID", "__Secure-1PSIDTS", "__Secure-1PSIDCC", "__Secure-1PAPISID", "NID", "AEC", "SOCS",
	"__Secure-BUCKET", "__Secure-ENID", "SID", "HSID", "SSID", "APISID", "SAPISID", "__Secure-3PSID",
	"__Secure-3PSIDTS", "__Secure-3PAPISID", "SIDCC",
}

type browserConfig struct {
	id, name, baseDir string
	usesLocalAppData  bool
	keychainService   string
	keychainAccount   string
	secretToolApp     string
	kwalletFolder     string
	kwalletEntry      string
}

var macosBrowserConfigs = []browserConfig{
	{id: "helium", name: "Helium", baseDir: "Library/Application Support/net.imput.helium", keychainService: "Helium Storage Key", keychainAccount: "Helium"},
	{id: "chrome", name: "Chrome", baseDir: "Library/Application Support/Google/Chrome", keychainService: "Chrome Safe Storage", keychainAccount: "Chrome"},
	{id: "brave", name: "Brave", baseDir: "Library/Application Support/BraveSoftware/Brave-Browser", keychainService: "Brave Safe Storage", keychainAccount: "Brave"},
	{id: "arc", name: "Arc", baseDir: "Library/Application Support/Arc/User Data", keychainService: "Arc Safe Storage", keychainAccount: "Arc"},
}

var linuxBrowserConfigs = []browserConfig{
	{id: "chromium", name: "Chromium", baseDir: ".config/chromium", secretToolApp: "chromium", kwalletFolder: "Chromium Keys", kwalletEntry: "Chromium Safe Storage"},
	{id: "chrome", name: "Chrome", baseDir: ".config/google-chrome", secretToolApp: "chrome", kwalletFolder: "Chrome Keys", kwalletEntry: "Chrome Safe Storage"},
}

var windowsBrowserConfigs = []browserConfig{
	{id: "chrome", name: "Chrome", baseDir: "Google/Chrome/User Data", usesLocalAppData: true},
	{id: "edge", name: "Edge", baseDir: "Microsoft/Edge/User Data", usesLocalAppData: true},
}

// Attempt statuses (BrowserCookieAttemptStatus).
const (
	statusMissingDatabase  = "missing-database"
	statusMissingRequired  = "missing-required-cookies"
	statusPasswordFailed   = "password-read-failed"
	statusDecryptionFailed = "decryption-failed"
	statusSQLiteMissing    = "sqlite-unavailable"
	statusSQLiteQuery      = "sqlite-query-failed"
	statusUnsafeProfile    = "unsafe-profile"
	statusNoHostCookies    = "no-host-cookies"
)

type browserPassword struct {
	value   string
	source  string
	warning string
}

var (
	cookieStateMu        sync.Mutex
	lastCookieDetails    *BrowserCookieDiagnosticDetails
	passwordCacheMu      sync.Mutex
	browserPasswordCache = map[string][]browserPassword{}
)

func resetCookieState() {
	cookieStateMu.Lock()
	lastCookieDetails = nil
	cookieStateMu.Unlock()
	passwordCacheMu.Lock()
	browserPasswordCache = map[string][]browserPassword{}
	passwordCacheMu.Unlock()
}

func setCookieDiagnostic(message string, attempts []BrowserCookieAttempt) {
	if attempts == nil {
		attempts = []BrowserCookieAttempt{}
	}
	cookieStateMu.Lock()
	lastCookieDetails = &BrowserCookieDiagnosticDetails{Message: message, Attempts: attempts}
	cookieStateMu.Unlock()
}

// LastGoogleCookieDiagnostic is getLastGoogleCookieDiagnostic ("" for null).
func LastGoogleCookieDiagnostic() string {
	cookieStateMu.Lock()
	defer cookieStateMu.Unlock()
	if lastCookieDetails == nil {
		return ""
	}
	return lastCookieDetails.Message
}

// LastBrowserCookieDiagnostic is getLastBrowserCookieDiagnostic.
func LastBrowserCookieDiagnostic() string { return LastGoogleCookieDiagnostic() }

// LastGoogleCookieDiagnosticDetails is getLastGoogleCookieDiagnosticDetails.
func LastGoogleCookieDiagnosticDetails() *BrowserCookieDiagnosticDetails {
	cookieStateMu.Lock()
	defer cookieStateMu.Unlock()
	if lastCookieDetails == nil {
		return nil
	}
	c := *lastCookieDetails
	c.Attempts = append([]BrowserCookieAttempt{}, c.Attempts...)
	return &c
}

// GetGoogleCookies is getGoogleCookies.
func GetGoogleCookies(ctx context.Context, o GoogleCookieOptions) (*BrowserCookies, error) {
	return GetBrowserCookiesForHosts(ctx, BrowserCookieOptions{
		Hosts: googleOrigins, Browser: o.Browser, Profile: o.Profile, RequiredCookies: o.RequiredCookies,
		CookieNames: allCookieNames, RequiredLabel: "Gemini",
	})
}

type extractionState struct {
	sawCookieDatabase, sawRequiredCookies, sawAnyHostCookie bool
	sawBackendFailure                                       string
	sawUnsafeProfilePath, sawWindowsAppBoundCookie          bool
	sawDecryptionFailure                                    bool
	warnings                                                []string
	attempts                                                []BrowserCookieAttempt
}

func (s *extractionState) warn(w string) {
	for _, x := range s.warnings {
		if x == w {
			return
		}
	}
	s.warnings = append(s.warnings, w)
}

func (s *extractionState) record(c browserConfig, profile, status string) {
	s.attempts = append(s.attempts, BrowserCookieAttempt{Browser: c.name, Profile: profile, Status: status})
}

func (s *extractionState) recordBackend(c browserConfig, profile, failure string) {
	s.sawBackendFailure = failure
	status := statusSQLiteQuery
	if failure == failUnavailable {
		status = statusSQLiteMissing
	}
	s.record(c, profile, status)
}

// GetBrowserCookiesForHosts is getBrowserCookiesForHosts: nil, nil when no cookies were
// obtained (LastGoogleCookieDiagnostic says why). An error is a configuration that cannot be
// read or a cookie database that cannot be copied.
func GetBrowserCookiesForHosts(ctx context.Context, o BrowserCookieOptions) (*BrowserCookies, error) {
	cookieStateMu.Lock()
	lastCookieDetails = nil
	cookieStateMu.Unlock()
	allowed, err := IsBrowserCookieAccessAllowed()
	if err != nil {
		return nil, err
	}
	if !allowed {
		setCookieDiagnostic("Browser cookie access is disabled; enable allowBrowserCookies to use browser cookies.", nil)
		return nil, nil
	}

	goos := cookieGOOS
	var platformConfigs []browserConfig
	switch goos {
	case "darwin":
		platformConfigs = macosBrowserConfigs
	case "linux":
		platformConfigs = linuxBrowserConfigs
	case "windows":
		platformConfigs = windowsBrowserConfigs
	}
	configs := platformConfigs
	if o.Browser != "" {
		configs = nil
		for _, c := range platformConfigs {
			if c.id == o.Browser {
				configs = append(configs, c)
			}
		}
		if len(configs) == 0 {
			setCookieDiagnostic("Browser preset '"+o.Browser+"' is not supported on this platform.", nil)
			return nil, nil
		}
	}
	if len(configs) == 0 {
		setCookieDiagnostic("Chromium cookie extraction is unsupported on this platform.", nil)
		return nil, nil
	}

	rawProfile := jsTrim(o.Profile)
	requestedProfile := normalizeProfileName(o.Profile)
	if rawProfile != "" && requestedProfile == "" {
		setCookieDiagnostic("Configured Chromium profile must be a profile directory name, not a path.", nil)
		return nil, nil
	}
	required := normalizeCookieNames(o.RequiredCookies)
	cookieNames := normalizeCookieNames(o.CookieNames)
	hosts := normalizeHosts(o.Hosts)
	if len(hosts) == 0 {
		setCookieDiagnostic("No valid cookie hosts were requested.", nil)
		return nil, nil
	}
	home := cookieHome(goos)
	st := &extractionState{}
	label := o.RequiredLabel
	if label == "" {
		label = "browser"
	}

	for _, cfg := range configs {
		profiles := []string{requestedProfile}
		if requestedProfile == "" {
			profiles = listBrowserProfiles(home, cfg)
		}
		for _, profile := range profiles {
			res, err := tryProfile(ctx, st, cfg, profile, home, goos, hosts, required, cookieNames, o.RequestURL)
			if err != nil {
				return nil, err
			}
			if res != nil {
				res.Warnings = st.warnings
				return res, nil
			}
		}
	}

	switch {
	case st.sawBackendFailure == failUnavailable:
		setCookieDiagnostic("SQLite backend unavailable: install sqlite3 or use a runtime with SQLite support.", st.attempts)
	case st.sawBackendFailure == failQuery:
		setCookieDiagnostic("SQLite query failed while reading the copied Chromium cookie database.", st.attempts)
	case st.sawUnsafeProfilePath:
		setCookieDiagnostic("Configured Chromium profile must resolve inside the browser profile root.", st.attempts)
	case !st.sawCookieDatabase:
		if requestedProfile != "" {
			setCookieDiagnostic("Chromium profile '"+requestedProfile+"' does not contain a cookie database.", st.attempts)
		} else {
			setCookieDiagnostic("No detected Chromium profile contains a cookie database.", st.attempts)
		}
	case len(required) > 0 && !st.sawRequiredCookies:
		setCookieDiagnostic("No detected Chromium profile contains the required "+label+" cookies.", st.attempts)
	case st.sawWindowsAppBoundCookie:
		setCookieDiagnostic("Windows Chromium v20 app-bound cookies are not supported.", st.attempts)
	case len(st.warnings) > 0:
		setCookieDiagnostic(st.warnings[0], st.attempts)
	case st.sawDecryptionFailure:
		setCookieDiagnostic("Required "+label+" cookies could not be decrypted.", st.attempts)
	case !st.sawAnyHostCookie:
		if o.RequestURL != nil {
			setCookieDiagnostic("No detected Chromium profile contains cookies for the requested URL.", st.attempts)
		} else {
			setCookieDiagnostic("No detected Chromium profile contains cookies for the requested host.", st.attempts)
		}
	default:
		setCookieDiagnostic("Required Gemini cookies were not available or could not be decrypted.", st.attempts)
	}
	return nil, nil
}

func cookieHome(goos string) string {
	if goos == "windows" {
		if p := os.Getenv("USERPROFILE"); p != "" {
			return p
		}
	}
	return homeDir()
}

// tryProfile reads one profile. A nil result with a nil error means "try the next profile".
func tryProfile(ctx context.Context, st *extractionState, cfg browserConfig, profile, home, goos string, hosts, required, cookieNames []string, requestURL *url.URL) (*BrowserCookies, error) {
	profilePath, status := resolveProfilePath(home, cfg, profile)
	switch status {
	case profileOutside:
		st.sawUnsafeProfilePath = true
		st.record(cfg, profile, statusUnsafeProfile)
		return nil, nil
	case profileMissing:
		st.record(cfg, profile, statusMissingDatabase)
		return nil, nil
	}
	cookiesPath := cookieDatabasePath(profilePath, cfg)
	if cookiesPath == "" {
		st.record(cfg, profile, statusMissingDatabase)
		return nil, nil
	}
	st.sawCookieDatabase = true

	tempDir, err := os.MkdirTemp("", "pi-chrome-cookies-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	tempDB := filepath.Join(tempDir, "Cookies")
	if err := copyFile(cookiesPath, tempDB); err != nil {
		return nil, err
	}
	copySidecar(cookiesPath, tempDB, "-wal")
	copySidecar(cookiesPath, tempDB, "-shm")

	if len(required) > 0 {
		present, failure := hasCookieNames(ctx, tempDB, hosts, required)
		if failure != "" {
			st.recordBackend(cfg, profile, failure)
			return nil, nil
		}
		if !present {
			st.record(cfg, profile, statusMissingRequired)
			return nil, nil
		}
		st.sawRequiredCookies = true
	}

	var passwords []browserPassword
	var windowsKey []byte
	if goos != "windows" {
		passwords = readBrowserPasswords(ctx, cfg, goos)
		for _, p := range passwords {
			if p.warning != "" {
				st.warn(p.warning)
			}
		}
	} else {
		windowsKey = readWindowsEncryptionKey(ctx, cfg, home)
	}
	var keys [][]byte
	iterations := 1
	if goos == "darwin" {
		iterations = 1003
	}
	for _, p := range passwords {
		if p.value != "" {
			keys = append(keys, pbkdf2SHA1(p.value, iterations))
		}
	}
	if (goos == "windows" && windowsKey == nil) || (goos != "windows" && len(keys) == 0) {
		if goos == "windows" {
			st.warn("Could not read " + cfg.name + " Windows cookie encryption key")
		} else {
			st.warn("Could not read " + cfg.name + " cookie encryption password")
		}
		st.record(cfg, profile, statusPasswordFailed)
		return nil, nil
	}
	version, failure := readMetaVersion(ctx, tempDB)
	if failure != "" {
		st.recordBackend(cfg, profile, failure)
	}
	if failure == failUnavailable {
		return nil, nil
	}
	rows, failure := queryCookieRows(ctx, tempDB, hosts, cookieNames, requestURL != nil)
	if failure != "" {
		st.recordBackend(cfg, profile, failure)
		return nil, nil
	}

	type entry struct{ name, value, path string }
	var entries []entry
	cookies := CookieMap{}
	requiredDecryptFailures := map[string]bool{}
	kwalletAttempted := false
	for _, p := range passwords {
		if p.source == "kwallet" {
			kwalletAttempted = true
		}
	}
	for _, row := range rows {
		name, _ := row["name"].(string)
		if name == "" {
			continue
		}
		value, _ := row["value"].(string)
		if hexValue, ok := row["encrypted_value_hex"].(string); value == "" && ok && isHex(hexValue) {
			encrypted, _ := hex.DecodeString(hexValue)
			if goos == "windows" && bytes.HasPrefix(encrypted, []byte("v20")) {
				st.sawWindowsAppBoundCookie = true
			}
			if goos == "windows" {
				value = decryptWindowsCookieValue(encrypted, windowsKey, version >= 24)
			} else {
				for _, key := range keys {
					if value = decryptCookieValue(encrypted, key, version >= 24); value != "" {
						break
					}
				}
			}
			if value == "" && goos == "linux" && !kwalletAttempted && hasSource(passwords, "secret-service") && containsString(required, name) {
				kwalletAttempted = true
				fallback := readKWalletPassword(ctx, cfg)
				if fallback.warning != "" {
					st.warn(fallback.warning)
				}
				if fallback.value != "" {
					key := pbkdf2SHA1(fallback.value, 1)
					keys = append(keys, key)
					value = decryptCookieValue(encrypted, key, version >= 24)
					if value != "" {
						passwords = append(passwords, fallback)
						storeBrowserPasswords(cfg, goos, passwords)
					}
				}
			}
			if value == "" && containsString(required, name) {
				requiredDecryptFailures[name] = true
			}
		}
		if value == "" {
			continue
		}
		path := "/"
		if p, ok := row["path"].(string); ok && strings.HasPrefix(p, "/") {
			path = p
		}
		if requestURL != nil {
			rp := requestURL.EscapedPath()
			if rp == "" {
				rp = "/"
			}
			if !pathMatches(rp, path) {
				continue
			}
		}
		entries = append(entries, entry{name, value, path})
		if cookies[name] == "" {
			cookies[name] = value
		}
	}

	if len(entries) > 0 {
		st.sawAnyHostCookie = true
	}
	if len(required) > 0 {
		all := true
		for _, n := range required {
			if cookies[n] == "" {
				all = false
			}
		}
		if !all {
			decryptFailed := false
			for _, n := range required {
				if cookies[n] == "" && requiredDecryptFailures[n] {
					decryptFailed = true
				}
			}
			if decryptFailed {
				st.sawDecryptionFailure = true
				st.record(cfg, profile, statusDecryptionFailed)
			} else {
				st.record(cfg, profile, statusMissingRequired)
			}
			return nil, nil
		}
	}
	if len(entries) == 0 {
		if len(requiredDecryptFailures) > 0 {
			st.record(cfg, profile, statusDecryptionFailed)
			st.sawDecryptionFailure = true
		} else {
			st.record(cfg, profile, statusNoHostCookies)
		}
		return nil, nil
	}
	res := &BrowserCookies{Cookies: cookies}
	if requestURL != nil {
		sort.SliceStable(entries, func(i, j int) bool { return len(entries[i].path) > len(entries[j].path) })
		parts := make([]string, len(entries))
		for i, e := range entries {
			parts[i] = e.name + "=" + e.value
		}
		res.CookieHeader = strings.Join(parts, "; ")
	}
	return res, nil
}

func hasSource(ps []browserPassword, source string) bool {
	for _, p := range ps {
		if p.source == source {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var hexRE = regexp.MustCompile(`^[0-9a-fA-F]*$`)

func isHex(s string) bool { return hexRE.MatchString(s) }

func normalizeProfileName(value string) string {
	v := jsTrim(value)
	if v == "" || filepath.IsAbs(v) || v == "." || v == ".." || strings.ContainsAny(v, `/\`) {
		return ""
	}
	return v
}

const (
	profileOK = iota
	profileOutside
	profileMissing
)

func browserBasePath(home string, c browserConfig) string {
	if c.usesLocalAppData {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(local, filepath.FromSlash(c.baseDir))
	}
	return filepath.Join(home, filepath.FromSlash(c.baseDir))
}

func cookieDatabasePath(profilePath string, c browserConfig) string {
	network := filepath.Join(profilePath, "Network", "Cookies")
	if c.usesLocalAppData && pathExists(network) {
		return network
	}
	legacy := filepath.Join(profilePath, "Cookies")
	if pathExists(legacy) {
		return legacy
	}
	return ""
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func resolveProfilePath(home string, c browserConfig, profile string) (string, int) {
	base := browserBasePath(home, c)
	profilePath := filepath.Join(base, profile)
	if cookieDatabasePath(profilePath, c) == "" {
		return "", profileMissing
	}
	baseReal, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", profileMissing
	}
	profileReal, err := filepath.EvalSymlinks(profilePath)
	if err != nil {
		return "", profileMissing
	}
	if profileReal != baseReal && !strings.HasPrefix(profileReal, baseReal+string(filepath.Separator)) {
		return "", profileOutside
	}
	return profileReal, profileOK
}

func normalizeCookieNames(names []string) []string {
	var out []string
	for _, n := range names {
		if n = jsTrim(n); n != "" && !containsString(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func normalizeHosts(hosts []string) []string {
	var out []string
	for _, h := range hosts {
		h = strings.ToLower(jsTrim(h))
		h = strings.TrimPrefix(h, "[")
		h = strings.TrimSuffix(h, "]")
		h = strings.TrimSuffix(h, ".")
		if h != "" && !containsString(out, h) {
			out = append(out, h)
		}
	}
	return out
}

func listBrowserProfiles(home string, c browserConfig) []string {
	base := browserBasePath(home, c)
	if !pathExists(base) {
		return []string{"Default"}
	}
	var profiles []string
	if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			if e.IsDir() && cookieDatabasePath(filepath.Join(base, e.Name()), c) != "" {
				profiles = append(profiles, e.Name())
			}
		}
	}
	if len(profiles) == 0 {
		return []string{"Default"}
	}
	sort.SliceStable(profiles, func(i, j int) bool { return compareProfileNames(profiles[i], profiles[j]) < 0 })
	return profiles
}

var (
	profileNumRE   = regexp.MustCompile(`(?i)^Profile\s+(\d+)$`)
	personNumRE    = regexp.MustCompile(`(?i)^Person\s+(\d+)$`)
	digitRunSplit  = regexp.MustCompile(`\d+|\D+`)
	maxSafeInteger = 9007199254740991.0
)

func profileKey(name string) (int, float64) {
	if name == "Default" {
		return 0, 0
	}
	if m := profileNumRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		return 1, n
	}
	if m := personNumRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		return 2, n
	}
	return 3, maxSafeInteger
}

// compareProfileNames orders Default, Profile N, Person N, then the rest; the last tie-break is
// a case-insensitive natural comparison (localeCompare with base sensitivity and numeric
// collation upstream).
func compareProfileNames(a, b string) int {
	ap, ai := profileKey(a)
	bp, bi := profileKey(b)
	if ap != bp {
		return ap - bp
	}
	if ai != bi {
		if ai < bi {
			return -1
		}
		return 1
	}
	return naturalCompare(strings.ToLower(a), strings.ToLower(b))
}

func naturalCompare(a, b string) int {
	pa, pb := digitRunSplit.FindAllString(a, -1), digitRunSplit.FindAllString(b, -1)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, y := pa[i], pb[i]
		if x[0] >= '0' && x[0] <= '9' && y[0] >= '0' && y[0] <= '9' {
			nx, _ := strconv.ParseFloat(x, 64)
			ny, _ := strconv.ParseFloat(y, 64)
			if nx != ny {
				if nx < ny {
					return -1
				}
				return 1
			}
			continue
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return len(pa) - len(pb)
}

// pbkdf2SHA1 is the Chromium safe-storage key: pbkdf2-sha1 of the password with the salt
// "saltysalt" and a 16-byte output.
func pbkdf2SHA1(password string, iterations int) []byte {
	key, err := pbkdf2.Key(sha1.New, password, []byte("saltysalt"), iterations, 16)
	if err != nil {
		return nil
	}
	return key
}

func decodeStrictUTF8(b []byte) (string, bool) {
	if !utf8.Valid(b) {
		return "", false
	}
	return strings.TrimPrefix(string(b), "\ufeff"), true
}

// decryptCookieValue is decryptCookieValue: AES-128-CBC behind a "vNN" prefix; "" is a failure.
func decryptCookieValue(encrypted, key []byte, stripHash bool) string {
	if len(encrypted) < 3 || len(key) != 16 || !versionPrefixRE.Match(encrypted[:3]) {
		return ""
	}
	ciphertext := encrypted[3:]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, bytes.Repeat([]byte{0x20}, aes.BlockSize)).CryptBlocks(plain, ciphertext)
	plain = removePKCS7Padding(plain)
	if stripHash && len(plain) >= 32 {
		plain = plain[32:]
	}
	decoded, ok := decodeStrictUTF8(plain)
	if !ok {
		return ""
	}
	i := 0
	for _, r := range decoded {
		if r >= 0x20 {
			break
		}
		i += utf8.RuneLen(r)
	}
	return decoded[i:]
}

var versionPrefixRE = regexp.MustCompile(`^v\d\d$`)

func decryptWindowsCookieValue(encrypted, key []byte, stripHash bool) string {
	if len(encrypted) < 3+12+16 || string(encrypted[:3]) != "v10" {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	plain, err := gcm.Open(nil, encrypted[3:15], encrypted[15:], nil)
	if err != nil {
		return ""
	}
	if stripHash && len(plain) >= 32 {
		plain = plain[32:]
	}
	decoded, _ := decodeStrictUTF8(plain)
	return decoded
}

func removePKCS7Padding(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > 16 {
		return b
	}
	return b[:len(b)-pad]
}

// --- passwords -----------------------------------------------------------------------------

func passwordCacheKey(c browserConfig, goos string) string { return goos + ":" + c.name }

func storeBrowserPasswords(c browserConfig, goos string, ps []browserPassword) {
	passwordCacheMu.Lock()
	browserPasswordCache[passwordCacheKey(c, goos)] = append([]browserPassword(nil), ps...)
	passwordCacheMu.Unlock()
}

// readBrowserPasswords looks the safe-storage password up once per process: only a found
// password is cached, a failed lookup is retried by the next call. The lookup runs under the
// cache lock, so concurrent callers share one program run.
func readBrowserPasswords(ctx context.Context, c browserConfig, goos string) []browserPassword {
	passwordCacheMu.Lock()
	defer passwordCacheMu.Unlock()
	key := passwordCacheKey(c, goos)
	if cached, ok := browserPasswordCache[key]; ok {
		return append([]browserPassword(nil), cached...)
	}
	var pw browserPassword
	cacheable := false
	switch goos {
	case "darwin":
		if c.keychainAccount != "" && c.keychainService != "" {
			pw.value = readKeychainPassword(ctx, c.keychainAccount, c.keychainService)
			cacheable = pw.value != ""
		}
	case "linux":
		pw, cacheable = readLinuxPassword(ctx, c)
	}
	out := []browserPassword{pw}
	if cacheable {
		browserPasswordCache[key] = append([]browserPassword(nil), out...)
	}
	return out
}

func readKeychainPassword(ctx context.Context, account, service string) string {
	value, failed := readCommandPassword(ctx, "security", []string{"find-generic-password", "-w", "-a", account, "-s", service})
	if failed {
		return ""
	}
	return value
}

// readCommandPassword runs a password program: the trimmed stdout (empty counts as none) and
// whether the program failed. Its stderr is never read.
func readCommandPassword(ctx context.Context, command string, args []string) (string, bool) {
	res, err := runHostCommand(ctx, command, args, cookieCommandTimeout)
	if err != nil || res.Code != 0 || res.Killed || len(res.Stdout) > cookieMaxBuffer {
		return "", true
	}
	return jsTrim(res.Stdout), false
}

func isKdeSession() bool {
	if strings.EqualFold(os.Getenv("KDE_FULL_SESSION"), "true") {
		return true
	}
	for _, d := range strings.Split(strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP")), ":") {
		if d == "kde" {
			return true
		}
	}
	return false
}

func readKWalletPassword(ctx context.Context, c browserConfig) browserPassword {
	if !isKdeSession() || c.kwalletFolder == "" || c.kwalletEntry == "" {
		return browserPassword{}
	}
	value, failed := readCommandPassword(ctx, "kwallet-query", []string{"-f", c.kwalletFolder, "-r", c.kwalletEntry, "kdewallet"})
	pw := browserPassword{value: value}
	if value != "" {
		pw.source = "kwallet"
	}
	if failed {
		pw.warning = "Could not read " + c.name + " KWallet cookie encryption password"
	}
	return pw
}

func readLinuxPassword(ctx context.Context, c browserConfig) (browserPassword, bool) {
	if c.secretToolApp == "" {
		return browserPassword{value: "peanuts"}, false
	}
	if value, _ := readCommandPassword(ctx, "secret-tool", []string{"lookup", "application", c.secretToolApp}); value != "" {
		return browserPassword{value: value, source: "secret-service"}, true
	}
	kwallet := readKWalletPassword(ctx, c)
	if kwallet.value != "" {
		return kwallet, true
	}
	return browserPassword{value: "peanuts", warning: kwallet.warning}, false
}

// readWindowsEncryptionKey unwraps the DPAPI-protected cookie key of Local State through
// powershell.exe. The protected blob travels inside the encoded script: the host command runner
// has no environment option for the original's PIWA_PROTECTED variable.
func readWindowsEncryptionKey(ctx context.Context, c browserConfig, home string) []byte {
	data, err := os.ReadFile(filepath.Join(browserBasePath(home, c), "Local State"))
	if err != nil {
		return nil
	}
	var state struct {
		OSCrypt struct {
			EncryptedKey any `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if json.Unmarshal(data, &state) != nil {
		return nil
	}
	encoded, ok := state.OSCrypt.EncryptedKey.(string)
	if !ok {
		return nil
	}
	protected := lenientBase64(encoded)
	if !bytes.HasPrefix(protected, []byte("DPAPI")) {
		return nil
	}
	decrypted := unprotectWindowsData(ctx, protected[5:])
	if len(decrypted) != 32 {
		return nil
	}
	return decrypted
}

func lenientBase64(s string) []byte {
	var clean strings.Builder
	for _, r := range s {
		switch {
		case r == '-':
			clean.WriteByte('+')
		case r == '_':
			clean.WriteByte('/')
		case r == '=' || r == ' ' || r == '\n' || r == '\r' || r == '\t':
		default:
			clean.WriteRune(r)
		}
	}
	b, err := base64.RawStdEncoding.DecodeString(clean.String())
	if err != nil {
		return nil
	}
	return b
}

func unprotectWindowsData(ctx context.Context, protected []byte) []byte {
	script := "Add-Type -AssemblyName System.Security;$data=[Convert]::FromBase64String('" + base64.StdEncoding.EncodeToString(protected) +
		"');$clear=[System.Security.Cryptography.ProtectedData]::Unprotect($data,$null,[System.Security.Cryptography.DataProtectionScope]::CurrentUser);[Console]::Write([Convert]::ToBase64String($clear))"
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 0, len(units)*2)
	for _, u := range units {
		raw = append(raw, byte(u), byte(u>>8))
	}
	res, err := runHostCommand(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw)}, cookieCommandTimeout)
	if err != nil || res.Code != 0 || res.Killed || len(res.Stdout) > cookieMaxBuffer {
		return nil
	}
	return lenientBase64(jsTrim(res.Stdout))
}

// --- SQLite --------------------------------------------------------------------------------

const (
	failUnavailable = "unavailable"
	failQuery       = "query"
)

type sqlRow = map[string]any

const pythonSQLiteScript = "import json,sqlite3,sys\ntry:\n c=sqlite3.connect('file:'+sys.argv[1]+'?mode=ro',uri=True)\n c.row_factory=sqlite3.Row\n print(json.dumps([dict(r) for r in c.execute(sys.argv[2]).fetchall()]))\nexcept Exception:\n sys.exit(1)"

// runSqliteQuery reads rows from a copied database: the sqlite3 CLI first, python3 second.
func runSqliteQuery(ctx context.Context, db, sql string) ([]sqlRow, string) {
	queryFailed := false
	rows, failure := runQueryProgram(ctx, "sqlite3", []string{"-readonly", "-json", db, sql})
	if failure == "" {
		return rows, ""
	}
	if failure == failQuery {
		queryFailed = true
	}
	rows, failure = runQueryProgram(ctx, "python3", []string{"-c", pythonSQLiteScript, db, sql})
	if failure == "" {
		return rows, ""
	}
	if failure == failQuery {
		queryFailed = true
	}
	if queryFailed {
		return nil, failQuery
	}
	return nil, failUnavailable
}

func runQueryProgram(ctx context.Context, command string, args []string) ([]sqlRow, string) {
	res, err := runHostCommand(ctx, command, args, cookieCommandTimeout)
	if err != nil {
		return nil, failUnavailable
	}
	if res.Code != 0 || res.Killed || len(res.Stdout) > cookieMaxBuffer {
		if res.Code == 1 && res.Stdout == "" && res.Stderr == "" && !res.Killed {
			if _, lookErr := cookieLookPath(command); lookErr != nil {
				return nil, failUnavailable
			}
		}
		return nil, failQuery
	}
	text := res.Stdout
	if text == "" {
		text = "[]"
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, failQuery
	}
	parsed, ok := value.([]any)
	if !ok {
		return nil, failQuery
	}
	rows := make([]sqlRow, 0, len(parsed))
	for _, p := range parsed {
		r, _ := p.(map[string]any)
		rows = append(rows, r)
	}
	return rows, ""
}

func readMetaVersion(ctx context.Context, db string) (int, string) {
	rows, failure := runSqliteQuery(ctx, db, "SELECT value FROM meta WHERE key = 'version'")
	if failure != "" {
		if failure == failUnavailable {
			return 0, failure
		}
		return 0, ""
	}
	if len(rows) == 0 {
		return 0, ""
	}
	switch v := rows[0]["value"].(type) {
	case float64:
		return int(math.Floor(v)), ""
	case string:
		n, _ := strconv.Atoi(leadingInt(v))
		return n, ""
	}
	return 0, ""
}

var leadingIntRE = regexp.MustCompile(`^[ \t\n\r]*[+-]?\d+`)

func leadingInt(s string) string { return strings.TrimSpace(leadingIntRE.FindString(s)) }

func hasCookieNames(ctx context.Context, db string, hosts, names []string) (bool, string) {
	rows, failure := runSqliteQuery(ctx, db, "SELECT DISTINCT name FROM cookies WHERE "+buildCookieWhere(hosts, names))
	if failure != "" {
		return false, failure
	}
	present := map[string]bool{}
	for _, r := range rows {
		n, _ := r["name"].(string)
		present[n] = true
	}
	for _, n := range names {
		if !present[n] {
			return false, ""
		}
	}
	return true, ""
}

func queryCookieRows(ctx context.Context, db string, hosts, names []string, filterExpired bool) ([]sqlRow, string) {
	colRows, failure := runSqliteQuery(ctx, db, "PRAGMA table_info(cookies)")
	if failure != "" {
		return nil, failure
	}
	columns := map[string]bool{}
	for _, r := range colRows {
		n, _ := r["name"].(string)
		columns[n] = true
	}
	pathExpr := "'/' AS path"
	if columns["path"] {
		pathExpr = "path"
	}
	expiresExpr := "0"
	if columns["expires_utc"] {
		expiresExpr = chromeExpiryMillisExpr
	}
	expiryFilter := ""
	if filterExpired && columns["expires_utc"] {
		expiryFilter = " AND (" + expiresExpr + " = 0 OR " + expiresExpr + " > " + strconv.FormatInt(cookieNow().UnixMilli()+chromeEpochMillis, 10) + ")"
	}
	partition := ""
	if filterExpired {
		partition = unpartitionedCookieFilter(columns)
	}
	return runSqliteQuery(ctx, db, "SELECT name, value, host_key, "+pathExpr+", "+expiresExpr+" AS expires_utc, hex(encrypted_value) AS encrypted_value_hex FROM cookies WHERE "+
		buildCookieWhere(hosts, names)+expiryFilter+partition+" ORDER BY length(path) DESC, "+expiresExpr+" ASC")
}

const chromeExpiryMillisExpr = "CASE WHEN expires_utc = 0 THEN 0 ELSE CAST(expires_utc / 1000 AS INTEGER) + CASE WHEN expires_utc % 1000 = 0 THEN 0 ELSE 1 END END"

func unpartitionedCookieFilter(columns map[string]bool) string {
	var clauses []string
	if columns["top_frame_site_key"] {
		clauses = append(clauses, "(top_frame_site_key IS NULL OR top_frame_site_key = '')")
	}
	if columns["partition_key"] {
		clauses = append(clauses, "(partition_key IS NULL OR partition_key = '')")
	}
	if columns["is_partitioned"] {
		clauses = append(clauses, "(is_partitioned IS NULL OR is_partitioned = 0)")
	}
	if len(clauses) == 0 {
		return ""
	}
	return " AND " + strings.Join(clauses, " AND ")
}

func pathMatches(requestPath, cookiePath string) bool {
	if requestPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	if strings.HasSuffix(cookiePath, "/") {
		return true
	}
	return requestPath[len(cookiePath)] == '/'
}

func buildCookieWhere(hosts, cookieNames []string) string {
	var clauses []string
	add := func(c string) {
		if !containsString(clauses, c) {
			clauses = append(clauses, c)
		}
	}
	for _, host := range hosts {
		add("host_key = '" + escapeSQLString(host) + "'")
		for _, candidate := range domainCookieHosts(host) {
			add("host_key = '." + escapeSQLString(candidate) + "'")
		}
	}
	where := "(" + strings.Join(clauses, " OR ") + ")"
	var names []string
	for _, n := range cookieNames {
		if n != "" {
			names = append(names, "'"+escapeSQLString(n)+"'")
		}
	}
	if len(names) > 0 {
		where += " AND name IN (" + strings.Join(names, ", ") + ")"
	}
	return where
}

func escapeSQLString(v string) string { return strings.ReplaceAll(v, "'", "''") }

func domainCookieHosts(host string) []string {
	var parts []string
	for _, p := range strings.Split(host, ".") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) <= 1 {
		return nil
	}
	var out []string
	for i := 0; i <= len(parts)-2; i++ {
		c := strings.Join(parts[i:], ".")
		if !containsString(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copySidecar(srcDB, targetDB, suffix string) {
	sidecar := srcDB + suffix
	if pathExists(sidecar) {
		_ = copyFile(sidecar, targetDB+suffix)
	}
}
