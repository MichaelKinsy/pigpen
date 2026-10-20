package websearch

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Twins of chrome-cookie-extraction.test.mjs. The upstream tests run the module in a child process
// with shell-script programs on PATH; here the host command runner is canned (see
// cookies_fixture_test.go) and the cookie databases are synthetic.

var googleRequired = []string{"__Secure-1PSID", "__Secure-1PSIDTS"}

func cookieFixtureEnv(t *testing.T, goos string) (home string, s *shims) {
	t.Helper()
	home, _ = isolate(t)
	s = installShims(t, goos)
	return home, s
}

// passwordCommand is the program that holds the browser's safe-storage password on a platform.
func passwordCommand(goos string) string {
	if goos == "darwin" {
		return "security"
	}
	return "secret-tool"
}

func wantText(t *testing.T, text, re string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(text) {
		t.Fatalf("%q does not match %q", text, re)
	}
}

func wantCookies(t *testing.T, res *BrowserCookies, want map[string]string) {
	t.Helper()
	if res == nil {
		t.Fatalf("no cookies; diagnostic %q", LastGoogleCookieDiagnostic())
	}
	if !reflect.DeepEqual(res.Cookies, want) {
		t.Fatalf("cookies = %v, want %v", res.Cookies, want)
	}
}

func getGoogle(t *testing.T, o GoogleCookieOptions) *BrowserCookies {
	t.Helper()
	res, err := GetGoogleCookies(bg(), o)
	noErr(t, err)
	return res
}

func hasAttempt(d *BrowserCookieDiagnosticDetails, browser, profile, status string) bool {
	if d == nil {
		return false
	}
	for _, a := range d.Attempts {
		if a.Browser == browser && a.Profile == profile && a.Status == status {
			return true
		}
	}
	return false
}

func wantShimCalls(t *testing.T, s *shims, command string, want int) {
	t.Helper()
	if got := s.count(command); got != want {
		t.Fatalf("%s ran %d times, want %d", command, got, want)
	}
}

func TestChromeCookieExtraction(t *testing.T) {
	const F = "chrome-cookie-extraction"
	defaultOS := runtime.GOOS
	pair := map[string]string{"__Secure-1PSIDTS": "two", "__Secure-1PSID": "one"}

	tw(t, F, "cookie extraction remains opt-in even when a browser database exists", func(t *testing.T) {
		home, _ := cookieFixtureEnv(t, defaultOS)
		createCookieFixture(t, home, "Default", [][]any{{"__Secure-1PSID", "one", ".google.com", nil, 1}}, fixtureOptions{})
		unsetenv(t, "PI_ALLOW_BROWSER_COOKIES", "FEYNMAN_ALLOW_BROWSER_COOKIES")
		if res := getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired}); res != nil {
			t.Fatal("cookies were read without the opt-in")
		}
		wantText(t, LastGoogleCookieDiagnostic(), `disabled`)
	})

	tw(t, F, "auto-discovery finds a non-default Chromium profile", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, defaultOS)
		createCookieFixture(t, home, "Profile 2", plainGoogleRows("one", "two"), fixtureOptions{})
		s.password(passwordCommand(defaultOS), "peanuts")
		wantCookies(t, getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired}), pair)
	})

	tw(t, F, "KDE ${scenario.browser} decrypts v11 cookies with the KWallet safe-storage password", func(t *testing.T) {
		for _, sc := range []struct{ browser, id, folder, entry string }{
			{"Chrome", "chrome", "Chrome Keys", "Chrome Safe Storage"},
			{"Chromium", "chromium", "Chromium Keys", "Chromium Safe Storage"},
		} {
			t.Run(sc.browser, func(t *testing.T) {
				home, s := cookieFixtureEnv(t, "linux")
				password := "synthetic-kwallet-password"
				createCookieFixture(t, home, "Default", googleLinuxRows(password, "one", "two"), fixtureOptions{browser: sc.browser, goos: "linux"})
				t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
				s.on("secret-tool", func(int, []string) ExecResult { return ExecResult{Stderr: "sensitive stderr", Code: 1} })
				s.password("kwallet-query", password)
				res := getGoogle(t, GoogleCookieOptions{Browser: sc.id, Profile: "Default", RequiredCookies: googleRequired})
				wantCookies(t, res, pair)
				wantShimCalls(t, s, "secret-tool", 1)
				wantShimCalls(t, s, "kwallet-query", 1)
				want := []string{"-f", sc.folder, "-r", sc.entry, "kdewallet"}
				if got := s.lastArgs("kwallet-query"); !reflect.DeepEqual(got, want) {
					t.Fatalf("kwallet-query args = %v, want %v", got, want)
				}
				if strings.Contains(strings.Join(s.lastArgs("kwallet-query"), "\n"), password) {
					t.Fatal("the password reached the program's arguments")
				}
			})
		}
	})

	tw(t, F, "KDE keeps Secret Service ahead of KWallet", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, "linux")
		createCookieFixture(t, home, "Default", googleLinuxRows("peanuts", "one", "two"), fixtureOptions{goos: "linux"})
		t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
		s.password("secret-tool", "peanuts")
		s.password("kwallet-query", "unused-kwallet-password")
		wantCookies(t, getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired}), pair)
		wantShimCalls(t, s, "kwallet-query", 0)
	})

	tw(t, F, "KDE caches both Secret Service and KWallet passwords", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, "linux")
		password, secretPassword := "synthetic-kwallet-password", "synthetic-secret-service-password"
		createCookieFixture(t, home, "Profile 1", googleLinuxRows(password, "one", "two"), fixtureOptions{goos: "linux"})
		createCookieFixture(t, home, "Profile 2", googleLinuxRows(secretPassword, "three", "four"), fixtureOptions{goos: "linux"})
		t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
		s.password("secret-tool", secretPassword)
		s.password("kwallet-query", password)
		first := getGoogle(t, GoogleCookieOptions{Profile: "Profile 1", RequiredCookies: googleRequired})
		second := getGoogle(t, GoogleCookieOptions{Profile: "Profile 2", RequiredCookies: googleRequired})
		third := getGoogle(t, GoogleCookieOptions{Profile: "Profile 1", RequiredCookies: googleRequired})
		wantCookies(t, first, pair)
		wantCookies(t, second, map[string]string{"__Secure-1PSIDTS": "four", "__Secure-1PSID": "three"})
		wantCookies(t, third, first.Cookies)
		wantShimCalls(t, s, "secret-tool", 1)
		wantShimCalls(t, s, "kwallet-query", 1)
	})

	tw(t, F, "failed KWallet reads retry and successful passwords are cached", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, "linux")
		password := "synthetic-kwallet-password"
		createCookieFixture(t, home, "Default", googleLinuxRows(password, "one", "two"), fixtureOptions{goos: "linux"})
		t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
		s.on("secret-tool", func(int, []string) ExecResult { return ExecResult{Stderr: "sensitive stderr", Code: 1} })
		s.on("kwallet-query", func(n int, _ []string) ExecResult {
			if n == 1 {
				return ExecResult{Code: 1}
			}
			return ExecResult{Stdout: password}
		})
		o := GoogleCookieOptions{Profile: "Default", RequiredCookies: googleRequired}
		first := getGoogle(t, o)
		firstDiagnostic := LastGoogleCookieDiagnostic()
		second := getGoogle(t, o)
		third := getGoogle(t, o)
		if first != nil {
			t.Fatal("cookies were decrypted without the KWallet password")
		}
		wantText(t, firstDiagnostic, `Could not read Chrome KWallet cookie encryption password`)
		if strings.Contains(firstDiagnostic, "sensitive stderr") {
			t.Fatal("the program's stderr reached the diagnostic")
		}
		wantCookies(t, second, pair)
		wantCookies(t, third, pair)
		wantShimCalls(t, s, "secret-tool", 2)
		wantShimCalls(t, s, "kwallet-query", 2)
	})

	tw(t, F, "auto-discovery finds a Brave profile on macOS", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, "darwin")
		createCookieFixture(t, home, "Default", plainGoogleRows("one", "two"), fixtureOptions{browser: "Brave", goos: "darwin"})
		s.password("security", "peanuts")
		wantCookies(t, getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired}), pair)
		want := []string{"find-generic-password", "-w", "-a", "Brave", "-s", "Brave Safe Storage"}
		if got := s.lastArgs("security"); !reflect.DeepEqual(got, want) {
			t.Fatalf("security args = %v, want %v", got, want)
		}
	})

	tw(t, F, "browser selection checks only the requested preset", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, "darwin")
		createCookieFixture(t, home, "Profile 1", plainGoogleRows("helium-one", "helium-two"), fixtureOptions{browser: "Helium", goos: "darwin"})
		createCookieFixture(t, home, "Profile 1", plainGoogleRows("chrome-one", "chrome-two"), fixtureOptions{browser: "Chrome", goos: "darwin"})
		s.password("security", "peanuts")
		res := getGoogle(t, GoogleCookieOptions{Browser: "helium", Profile: "Profile 1", RequiredCookies: googleRequired})
		wantCookies(t, res, map[string]string{"__Secure-1PSIDTS": "helium-two", "__Secure-1PSID": "helium-one"})
		want := []string{"find-generic-password", "-w", "-a", "Helium", "-s", "Helium Storage Key"}
		if got := s.lastArgs("security"); !reflect.DeepEqual(got, want) {
			t.Fatalf("security args = %v, want %v", got, want)
		}
		wantShimCalls(t, s, "security", 1)
	})

	tw(t, F, "Windows Chrome profiles decrypt v10 cookies with DPAPI keys", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, "windows")
		key := repeatByte(3, 32)
		createCookieFixture(t, home, "Default", [][]any{
			{"__Secure-1PSID", "", ".google.com", "hex:" + encryptWindowsCookie("one", key, "v10", ".google.com"), 1},
			{"__Secure-1PSIDTS", "", ".google.com", "hex:" + encryptWindowsCookie("two", key, "v10", ".google.com"), 2},
		}, fixtureOptions{goos: "windows", windowsKey: key})
		t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		protected := base64.StdEncoding.EncodeToString([]byte("protected"))
		powershellDPAPI(s, protected, key)
		wantCookies(t, getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired}), pair)
		args := s.lastArgs("powershell.exe")
		if len(args) != 4 || !reflect.DeepEqual(args[:3], []string{"-NoProfile", "-NonInteractive", "-EncodedCommand"}) {
			t.Fatalf("powershell.exe args = %v", args)
		}
		for _, a := range args {
			if a == protected {
				t.Fatal("the protected key is an argument of its own")
			}
		}
	})

	tw(t, F, "cookie queries keep high Chromium expiry values safe", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, defaultOS)
		createCookieFixture(t, home, "Profile 2", [][]any{
			{"__Secure-1PSID", "expired", ".google.com", nil, 1},
			{"__Secure-1PSID", "future", ".google.com", nil, "9223372036854775807"},
			{"__Secure-1PSIDTS", "session", ".google.com", nil, 0},
		}, fixtureOptions{})
		s.password(passwordCommand(defaultOS), "peanuts")
		u, _ := url.Parse("https://www.google.com/")
		res, err := GetBrowserCookiesForHosts(bg(), BrowserCookieOptions{Hosts: []string{"www.google.com"}, RequestURL: u, RequiredCookies: googleRequired})
		noErr(t, err)
		wantCookies(t, res, map[string]string{"__Secure-1PSID": "future", "__Secure-1PSIDTS": "session"})
	})

	tw(t, F, "cookie expiry filtering preserves same-second validity", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, defaultOS)
		nowMs := int64(1_700_000_000_500)
		sameSecondFutureChromeMicros := (nowMs + 400 + 11644473600000) * 1000
		createCookieFixture(t, home, "Profile 2", [][]any{
			{"__Secure-1PSID", "future", ".google.com", nil, sameSecondFutureChromeMicros},
			{"__Secure-1PSIDTS", "session", ".google.com", nil, 0},
		}, fixtureOptions{})
		s.password(passwordCommand(defaultOS), "peanuts")
		prev := cookieNow
		cookieNow = func() time.Time { return time.UnixMilli(nowMs) }
		t.Cleanup(func() { cookieNow = prev })
		u, _ := url.Parse("https://www.google.com/")
		res, err := GetBrowserCookiesForHosts(bg(), BrowserCookieOptions{Hosts: []string{"www.google.com"}, RequestURL: u, RequiredCookies: googleRequired})
		noErr(t, err)
		wantCookies(t, res, map[string]string{"__Secure-1PSID": "future", "__Secure-1PSIDTS": "session"})
	})

	tw(t, F, "Windows Edge uses the USERPROFILE AppData fallback and reports unsupported v20 cookies", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, "windows")
		key := repeatByte(4, 32)
		createCookieFixture(t, home, "Default", [][]any{
			{"__Secure-1PSID", "", ".google.com", "hex:" + encryptWindowsCookie("one", key, "v20", ""), 1},
			{"__Secure-1PSIDTS", "", ".google.com", "hex:" + encryptWindowsCookie("two", key, "v20", ""), 2},
		}, fixtureOptions{browser: "Edge", goos: "windows", windowsKey: key})
		powershellDPAPI(s, base64.StdEncoding.EncodeToString([]byte("protected")), key)
		if res := getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired}); res != nil {
			t.Fatal("v20 cookies were decrypted")
		}
		wantText(t, LastGoogleCookieDiagnostic(), `v20 app-bound cookies are not supported`)
	})

	tw(t, F, "required-cookie preflight avoids password invocation for unrelated profiles", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, "linux")
		createCookieFixture(t, home, "Profile 1", [][]any{{"NID", "unrelated", ".google.com", nil, 1}}, fixtureOptions{goos: "linux"})
		t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
		s.password("kwallet-query", "unused")
		s.password("secret-tool", "peanuts")
		if res := getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired}); res != nil {
			t.Fatal("cookies were returned for a profile without the required ones")
		}
		if !strings.Contains(LastGoogleCookieDiagnostic(), "required Gemini cookies") {
			t.Fatalf("diagnostic = %q", LastGoogleCookieDiagnostic())
		}
		if !hasAttempt(LastGoogleCookieDiagnosticDetails(), "Chrome", "Profile 1", "missing-required-cookies") {
			t.Fatalf("attempts = %+v", LastGoogleCookieDiagnosticDetails())
		}
		wantShimCalls(t, s, "secret-tool", 0)
		wantShimCalls(t, s, "kwallet-query", 0)
	})

	tw(t, F, "diagnostics report browser profile when password-store access fails", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, "darwin")
		createCookieFixture(t, home, "Default", plainGoogleRows("one", "two"), fixtureOptions{browser: "Helium", goos: "darwin"})
		s.on("security", func(int, []string) ExecResult { return ExecResult{Stderr: "sensitive stderr", Code: 1} })
		res := getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired})
		if res != nil {
			t.Fatal("cookies were returned without the password")
		}
		if got := LastGoogleCookieDiagnostic(); got != "Could not read Helium cookie encryption password" {
			t.Fatalf("diagnostic = %q", got)
		}
		details := LastGoogleCookieDiagnosticDetails()
		if want := (BrowserCookieAttempt{Browser: "Helium", Profile: "Default", Status: "password-read-failed"}); details.Attempts[0] != want {
			t.Fatalf("attempt = %+v, want %+v", details.Attempts[0], want)
		}
		dump, _ := json.Marshal(map[string]any{"result": res, "diagnostic": LastGoogleCookieDiagnostic(), "details": details})
		if regexp.MustCompile(`one|two|peanuts|sensitive stderr`).Match(dump) {
			t.Fatalf("a secret reached the diagnostics: %s", dump)
		}
	})

	tw(t, F, "diagnostics distinguish required cookie decryption failure", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, defaultOS)
		bad := "hex:" + hex.EncodeToString([]byte("v10bad-ciphertext"))
		createCookieFixture(t, home, "Profile 2", [][]any{
			{"__Secure-1PSID", "", ".google.com", bad, 1},
			{"__Secure-1PSIDTS", "", ".google.com", bad, 2},
		}, fixtureOptions{})
		s.password(passwordCommand(defaultOS), "peanuts")
		res := getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired})
		if res != nil {
			t.Fatal("cookies were returned although they cannot be decrypted")
		}
		wantText(t, LastGoogleCookieDiagnostic(), `could not be decrypted`)
		if !hasAttempt(LastGoogleCookieDiagnosticDetails(), "Chrome", "Profile 2", "decryption-failed") {
			t.Fatalf("attempts = %+v", LastGoogleCookieDiagnosticDetails())
		}
		dump, _ := json.Marshal(map[string]any{"result": res, "diagnostic": LastGoogleCookieDiagnostic(), "details": LastGoogleCookieDiagnosticDetails()})
		if regexp.MustCompile(`peanuts|bad-ciphertext`).Match(dump) {
			t.Fatalf("a secret reached the diagnostics: %s", dump)
		}
	})

	tw(t, F, "browser encryption password is cached within a process", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, defaultOS)
		createCookieFixture(t, home, "Profile 2", plainGoogleRows("one", "two"), fixtureOptions{})
		s.password(passwordCommand(defaultOS), "peanuts")
		o := GoogleCookieOptions{Profile: "Profile 2", RequiredCookies: googleRequired}
		getGoogle(t, o)
		getGoogle(t, o)
		wantShimCalls(t, s, passwordCommand(defaultOS), 1)
	})

	tw(t, F, "failed password lookups are retried instead of cached (${targetPlatform})", func(t *testing.T) {
		for _, target := range []string{"linux", "darwin"} {
			t.Run(target, func(t *testing.T) {
				home, s := cookieFixtureEnv(t, target)
				createCookieFixture(t, home, "Profile 2", plainGoogleRows("one", "two"), fixtureOptions{goos: target})
				s.on(passwordCommand(target), func(n int, _ []string) ExecResult {
					if n == 1 {
						return ExecResult{Code: 1}
					}
					return ExecResult{Stdout: "peanuts"}
				})
				s.password("kwallet-query", "unused")
				o := GoogleCookieOptions{Profile: "Profile 2", RequiredCookies: googleRequired}
				first, second, third := getGoogle(t, o), getGoogle(t, o), getGoogle(t, o)
				if target == "darwin" {
					if first != nil {
						t.Fatal("the first lookup failed but cookies were returned")
					}
				} else {
					// Linux falls back to its basic-store password; these plaintext cookies stay readable.
					wantCookies(t, first, pair)
				}
				wantCookies(t, second, pair)
				wantCookies(t, third, pair)
				// The failed lookup is retried; the subsequent success is cached.
				wantShimCalls(t, s, passwordCommand(target), 2)
				if target == "linux" {
					wantShimCalls(t, s, "kwallet-query", 0)
				}
			})
		}
	})

	tw(t, F, "configured profile names cannot escape the browser profile root", func(t *testing.T) {
		cookieFixtureEnv(t, defaultOS)
		if res := getGoogle(t, GoogleCookieOptions{Profile: "../Profile 2", RequiredCookies: []string{"__Secure-1PSID"}}); res != nil {
			t.Fatal("a profile outside the root was read")
		}
		wantText(t, LastGoogleCookieDiagnostic(), `profile directory name`)
	})

	tw(t, F, "explicit profile symlinks cannot escape the browser profile root", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, defaultOS)
		outsideHome := t.TempDir()
		createCookieFixture(t, outsideHome, "Outside", plainGoogleRows("one", "two"), fixtureOptions{})
		browserRoot := browserBase(home, "Chrome", defaultOS)
		if err := os.MkdirAll(browserRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(browserBase(outsideHome, "Chrome", defaultOS), "Outside"), filepath.Join(browserRoot, "link")); err != nil {
			t.Fatal(err)
		}
		s.password(passwordCommand(defaultOS), "peanuts")
		if res := getGoogle(t, GoogleCookieOptions{Profile: "link", RequiredCookies: googleRequired}); res != nil {
			t.Fatal("a profile outside the root was read through a symlink")
		}
		wantText(t, LastGoogleCookieDiagnostic(), `inside the browser profile root`)
	})

	tw(t, F, "sqlite3 CLI fallback reads a copied database when node sqlite is unavailable", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, defaultOS)
		createCookieFixture(t, home, "Profile 2", plainGoogleRows("one", "two"), fixtureOptions{})
		s.password(passwordCommand(defaultOS), "peanuts")
		wantCookies(t, getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired}), pair)
		args := s.lastArgs("sqlite3")
		if len(args) != 4 || args[0] != "-readonly" || args[1] != "-json" || filepath.Base(args[2]) != "Cookies" {
			t.Fatalf("sqlite3 args = %v", args)
		}
		if filepath.Dir(args[2]) == filepath.Join(browserBase(home, "Chrome", defaultOS), "Profile 2") {
			t.Fatal("sqlite3 read the browser's own database instead of a copy")
		}
	})

	tw(t, F, "unavailable SQLite backends produce actionable sanitized diagnostics", func(t *testing.T) {
		home, s := cookieFixtureEnv(t, defaultOS)
		createCookieFixture(t, home, "Profile 2", [][]any{{"__Secure-1PSID", "one", ".google.com", nil, 1}}, fixtureOptions{})
		s.noSQL = true
		if res := getGoogle(t, GoogleCookieOptions{RequiredCookies: googleRequired}); res != nil {
			t.Fatal("cookies were returned without a SQLite backend")
		}
		diagnostic := LastGoogleCookieDiagnostic()
		wantText(t, diagnostic, `SQLite backend unavailable`)
		details := LastGoogleCookieDiagnosticDetails()
		if !hasAttempt(details, "Chrome", "Profile 2", "sqlite-unavailable") {
			t.Fatalf("attempts = %+v", details)
		}
		if hasAttempt(details, "Chrome", "Profile 2", "missing-required-cookies") {
			t.Fatal("an unavailable backend was reported as missing cookies")
		}
		if regexp.MustCompile(`(?i)one|peanuts|stderr|password`).MatchString(diagnostic) {
			t.Fatalf("diagnostic %q is not sanitized", diagnostic)
		}
	})
}

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// powershellDPAPI is a canned powershell.exe: it answers with the key only when the encoded
// script carries the one protected blob the fixture's Local State holds.
func powershellDPAPI(s *shims, protectedB64 string, key []byte) {
	s.on("powershell.exe", func(_ int, args []string) ExecResult {
		if len(args) != 4 || args[2] != "-EncodedCommand" {
			return ExecResult{Code: 1}
		}
		raw, err := base64.StdEncoding.DecodeString(args[3])
		if err != nil || len(raw)%2 != 0 {
			return ExecResult{Code: 1}
		}
		units := make([]byte, 0, len(raw)/2)
		for i := 0; i < len(raw); i += 2 {
			units = append(units, raw[i])
		}
		if !strings.Contains(string(units), protectedB64) {
			return ExecResult{Code: 1}
		}
		return ExecResult{Stdout: base64.StdEncoding.EncodeToString(key)}
	})
}
