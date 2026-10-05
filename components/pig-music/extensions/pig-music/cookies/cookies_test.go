package cookies

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// fakeEnv answers the way each platform's tools do; no real browser, registry or LaunchServices is read.
type fakeEnv struct {
	goos   string
	vars   map[string]string
	output map[string]string // "prog arg arg" -> stdout
	fail   map[string]bool
	ran    []string
}

func (f *fakeEnv) env() Env {
	return Env{
		GOOS:   f.goos,
		Getenv: func(k string) string { return f.vars[k] },
		Run: func(name string, args ...string) (string, error) {
			key := strings.Join(append([]string{name}, args...), " ")
			f.ran = append(f.ran, key)
			if f.fail[name] {
				return "", errors.New("not found")
			}
			for k, v := range f.output {
				if strings.HasPrefix(key, k) {
					return v, nil
				}
			}
			return "", errors.New("unexpected command " + key)
		},
		Home: "/Users/u",
	}
}

func macPlist(handlers string) string {
	return `{"LSHandlers":[{"LSHandlerContentType":"public.html","LSHandlerRoleAll":"com.apple.dt.xcode"},` + handlers + `]}`
}

func TestMacOSDefaultBrowserFromLaunchServices(t *testing.T) {
	for bundle, want := range map[string]string{
		"com.google.chrome": "chrome", "com.apple.safari": "safari", "org.mozilla.firefox": "firefox", "com.brave.browser": "brave",
		"com.microsoft.edgemac": "edge", "com.vivaldi.vivaldi": "vivaldi", "com.operasoftware.opera": "opera", "org.chromium.chromium": "chromium",
	} {
		f := &fakeEnv{goos: "darwin", output: map[string]string{"plutil": macPlist(`{"LSHandlerRoleAll":"` + bundle + `","LSHandlerURLScheme":"https"},{"LSHandlerRoleAll":"x.other","LSHandlerURLScheme":"mailto"}`)}}
		d := Detect(f.env())
		if d.Browser != want || d.Problem != "" || d.Raw != bundle {
			t.Errorf("%s: %+v", bundle, d)
		}
		// the system is asked first (the fake cannot answer), then the plist is read
		if len(f.ran) != 2 || !strings.Contains(f.ran[1], "com.apple.launchservices.secure.plist") || !strings.HasPrefix(f.ran[1], "plutil -convert json") {
			t.Errorf("ran %v", f.ran)
		}
	}
}

// No https entry is Safari, macOS's built-in default: see TestMacOSAsksTheSystemFirstAndANeverChangedDefaultIsSafari.
func TestMacOSWithAnUnsupportedOrUnreadableDefaultIsNotGuessed(t *testing.T) {
	f := &fakeEnv{goos: "darwin", output: map[string]string{"plutil": macPlist(`{"LSHandlerRoleAll":"company.thebrowser.browser","LSHandlerURLScheme":"https"}`)}}
	d := Detect(f.env())
	if d.Browser != "" || d.Raw != "company.thebrowser.browser" || !strings.Contains(d.Problem, "company.thebrowser.browser") || !strings.Contains(d.Problem, "cookieBrowser") {
		t.Errorf("unsupported: %+v", d)
	}
	f = &fakeEnv{goos: "darwin", fail: map[string]bool{"plutil": true}}
	if d := Detect(f.env()); d.Browser != "" || d.Problem == "" {
		t.Errorf("plutil failed: %+v", d)
	}
	f = &fakeEnv{goos: "darwin", output: map[string]string{"plutil": "not json"}}
	if d := Detect(f.env()); d.Browser != "" || d.Problem == "" {
		t.Errorf("garbage: %+v", d)
	}
}

func TestLinuxDefaultBrowserFromXdgSettings(t *testing.T) {
	for desktop, want := range map[string]string{
		"firefox.desktop": "firefox", "org.mozilla.firefox.desktop": "firefox", "firefox_firefox.desktop": "firefox",
		"google-chrome.desktop": "chrome", "chromium.desktop": "chromium", "chromium-browser.desktop": "chromium",
		"brave-browser.desktop": "brave", "microsoft-edge.desktop": "edge", "vivaldi-stable.desktop": "vivaldi", "opera.desktop": "opera",
	} {
		f := &fakeEnv{goos: "linux", vars: map[string]string{"DISPLAY": ":0"}, output: map[string]string{"xdg-settings get default-web-browser": desktop + "\n"}}
		if d := Detect(f.env()); d.Browser != want || d.Raw != desktop {
			t.Errorf("%s: %+v", desktop, d)
		}
	}
}

func TestLinuxHeadlessMissingToolAndUnknownBrowserAreSaidClearly(t *testing.T) {
	f := &fakeEnv{goos: "linux", output: map[string]string{"xdg-settings": "firefox.desktop"}}
	if d := Detect(f.env()); d.Browser != "" || !strings.Contains(d.Problem, "no graphical session") || len(f.ran) != 0 {
		t.Errorf("headless: %+v ran %v", d, f.ran)
	}
	f = &fakeEnv{goos: "linux", vars: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, fail: map[string]bool{"xdg-settings": true}}
	if d := Detect(f.env()); d.Browser != "" || !strings.Contains(d.Problem, "xdg-settings") {
		t.Errorf("no xdg-settings: %+v", d)
	}
	f = &fakeEnv{goos: "linux", vars: map[string]string{"DISPLAY": ":0"}, output: map[string]string{"xdg-settings": "qutebrowser.desktop"}}
	if d := Detect(f.env()); d.Browser != "" || !strings.Contains(d.Problem, "qutebrowser.desktop") || !strings.Contains(d.Problem, "cookieBrowser") {
		t.Errorf("unsupported: %+v", d)
	}
}

func TestWindowsDefaultBrowserFromTheHTTPSUserChoice(t *testing.T) {
	for progID, want := range map[string]string{
		"ChromeHTML": "chrome", "FirefoxURL-308046B0AF4A39CB": "firefox", "MSEdgeHTM": "edge", "BraveHTML": "brave",
		"VivaldiHTM.ABC": "vivaldi", "OperaStable": "opera",
	} {
		out := "\r\nHKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\Shell\\Associations\\UrlAssociations\\https\\UserChoice\r\n    ProgId    REG_SZ    " + progID + "\r\n\r\n"
		f := &fakeEnv{goos: "windows", output: map[string]string{"reg query": out}}
		d := Detect(f.env())
		if d.Browser != want || d.Raw != progID {
			t.Errorf("%s: %+v", progID, d)
		}
		if !strings.Contains(f.ran[0], `UrlAssociations\https\UserChoice`) || !strings.Contains(f.ran[0], "ProgId") {
			t.Errorf("ran %v", f.ran)
		}
	}
	f := &fakeEnv{goos: "windows", output: map[string]string{"reg query": "    ProgId    REG_SZ    IE.HTTPS\r\n"}}
	if d := Detect(f.env()); d.Browser != "" || !strings.Contains(d.Problem, "IE.HTTPS") {
		t.Errorf("%+v", d)
	}
}

func TestTermuxAndOtherSystemsHaveNoBrowser(t *testing.T) {
	for _, goos := range []string{"android", "freebsd"} {
		f := &fakeEnv{goos: goos}
		d := Detect(f.env())
		if d.Browser != "" || !strings.Contains(d.Problem, "cookieBrowser") || len(f.ran) != 0 {
			t.Errorf("%s: %+v", goos, d)
		}
	}
}

func TestResolveUsesTheSettingFirstAndNeverGuesses(t *testing.T) {
	f := &fakeEnv{goos: "darwin", output: map[string]string{"plutil": macPlist(`{"LSHandlerRoleAll":"com.google.chrome","LSHandlerURLScheme":"https"}`)}}
	c := Resolve("firefox:work", f.env())
	if c.Spec != "firefox:work" || c.Browser != "firefox" || c.From != FromSetting || c.Problem != "" || len(f.ran) != 0 {
		t.Errorf("%+v ran %v", c, f.ran)
	}
	if c := Resolve("chrome+gnomekeyring:Profile 1", f.env()); c.Browser != "chrome" || c.Spec != "chrome+gnomekeyring:Profile 1" {
		t.Errorf("%+v", c)
	}
	if c := Resolve("arc", f.env()); c.Spec != "" || !strings.Contains(c.Problem, `"arc"`) || !strings.Contains(c.Problem, "brave, chrome") {
		t.Errorf("unsupported setting: %+v", c)
	}
	c = Resolve("", f.env())
	if c.Spec != "chrome" || c.Browser != "chrome" || c.From != FromDetection || !strings.Contains(c.Describe(), "chrome") || !strings.Contains(c.Describe(), "com.google.chrome") {
		t.Errorf("%+v", c)
	}
	f = &fakeEnv{goos: "android"}
	if c := Resolve("", f.env()); c.Spec != "" || c.Problem == "" || c.Describe() == "" {
		t.Errorf("%+v", c)
	}
}

func TestNotesPerBrowserAndSystem(t *testing.T) {
	for _, tc := range []struct{ browser, goos, want string }{
		{"chrome", "darwin", "Keychain"}, {"brave", "darwin", "Safe Storage"}, {"safari", "darwin", "Full Disk Access"},
		{"firefox", "darwin", "no prompt"}, {"chromium", "linux", "keyring"}, {"edge", "windows", "browser is open"},
	} {
		if n := Notes(tc.browser, tc.goos); !strings.Contains(n, tc.want) {
			t.Errorf("%s/%s: %q lacks %q", tc.browser, tc.goos, n, tc.want)
		}
	}
}

func TestConsentIsPerBrowserPersistedAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "cookie-consent.json")
	c := Consent{Path: path}
	if c.Granted("chrome") {
		t.Fatal("granted before asking")
	}
	if err := c.Grant("chrome"); err != nil {
		t.Fatal(err)
	}
	if !c.Granted("chrome") || c.Granted("firefox") {
		t.Fatal("consent is per browser")
	}
	if !(Consent{Path: path}).Granted("chrome") {
		t.Fatal("not persisted")
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	b, _ := os.ReadFile(path)
	if strings.TrimSpace(string(b)) != `{"browsers":["chrome"]}` {
		t.Errorf("file holds %q: only browser names", b)
	}
	_ = c.Grant("chrome")
	_ = c.Grant("firefox")
	if b, _ := os.ReadFile(path); strings.Count(string(b), "chrome") != 1 {
		t.Errorf("duplicate: %s", b)
	}
	if err := c.Revoke("chrome"); err != nil || c.Granted("chrome") || !c.Granted("firefox") {
		t.Errorf("revoke: %v", err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if (Consent{Path: path}).Granted("firefox") {
		t.Error("a broken file granted consent")
	}
}

func TestAccessAsksForConsentOncePerBrowserThenReturnsTheSpec(t *testing.T) {
	f := &fakeEnv{goos: "darwin", output: map[string]string{"plutil": macPlist(`{"LSHandlerRoleAll":"com.google.chrome","LSHandlerURLScheme":"https"}`)}}
	a := Access{Env: f.env(), Consent: Consent{Path: filepath.Join(t.TempDir(), "c.json")}}
	_, err := a.Spec(nil)
	var need *music.NeedsConsentError
	if !errors.As(err, &need) || need.Browser != "chrome" || !strings.Contains(need.Description, "com.google.chrome") || !strings.Contains(need.Notes, "Keychain") {
		t.Fatalf("%v", err)
	}
	if err := a.Grant(nil, "chrome"); err != nil {
		t.Fatal(err)
	}
	if spec, err := a.Spec(nil); err != nil || spec != "chrome" {
		t.Fatalf("%q %v", spec, err)
	}
	// another browser (the user changed their default) is a new question
	f.output["plutil"] = macPlist(`{"LSHandlerRoleAll":"org.mozilla.firefox","LSHandlerURLScheme":"https"}`)
	if _, err := a.Spec(nil); !errors.As(err, &need) || need.Browser != "firefox" {
		t.Fatalf("%v", err)
	}
	// the setting overrides detection and is its own consent
	a.Setting = "brave:Work"
	if _, err := a.Spec(nil); !errors.As(err, &need) || need.Browser != "brave" {
		t.Fatalf("%v", err)
	}
	_ = a.Grant(nil, "brave")
	if spec, _ := a.Spec(nil); spec != "brave:Work" {
		t.Errorf("%q", spec)
	}
	a2 := Access{Env: (&fakeEnv{goos: "android"}).env(), Consent: a.Consent}
	var nb *music.NoBrowserError
	if _, err := a2.Spec(nil); !errors.As(err, &nb) || !strings.Contains(nb.Reason, "cookieBrowser") {
		t.Errorf("%v", err)
	}
	if err := a2.Grant(nil, "chrome"); !errors.As(err, &nb) {
		t.Errorf("grant without a browser: %v", err)
	}
}

const osascriptKey = "osascript -l JavaScript"

func TestMacOSAsksTheSystemWhichAppOpensHTTPSBeforeReadingThePlist(t *testing.T) {
	// A Mac whose default browser was never changed has no https entry in the LaunchServices plist; the system still
	// answers Safari when asked (NSWorkspace). The plist alone said "unknown" there.
	f := &fakeEnv{goos: "darwin", output: map[string]string{
		osascriptKey: "com.apple.Safari\n",
		"plutil":     macPlist(`{"LSHandlerRoleAll":"x.other","LSHandlerURLScheme":"mailto"}`),
	}}
	d := Detect(f.env())
	if d.Browser != "safari" || d.Raw != "com.apple.Safari" || d.Problem != "" {
		t.Fatalf("%+v", d)
	}
	if len(f.ran) != 1 || !strings.HasPrefix(f.ran[0], osascriptKey) || !strings.Contains(f.ran[0], "URLForApplicationToOpenURL") || !strings.Contains(f.ran[0], "https://") {
		t.Errorf("ran %v", f.ran)
	}
	// the system's answer wins over a different plist entry, and an app yt-dlp cannot read is not papered over
	f = &fakeEnv{goos: "darwin", output: map[string]string{
		osascriptKey: "company.thebrowser.browser",
		"plutil":     macPlist(`{"LSHandlerRoleAll":"com.google.chrome","LSHandlerURLScheme":"https"}`),
	}}
	if d := Detect(f.env()); d.Browser != "" || d.Raw != "company.thebrowser.browser" || !strings.Contains(d.Problem, "cookieBrowser") {
		t.Errorf("%+v", d)
	}
	// when osascript cannot answer (no output, or fails), the plist is used
	for name, f := range map[string]*fakeEnv{
		"empty": {goos: "darwin", output: map[string]string{osascriptKey: "\n", "plutil": macPlist(`{"LSHandlerRoleAll":"com.google.chrome","LSHandlerURLScheme":"https"}`)}},
		"fails": {goos: "darwin", fail: map[string]bool{"osascript": true}, output: map[string]string{"plutil": macPlist(`{"LSHandlerRoleAll":"com.google.chrome","LSHandlerURLScheme":"https"}`)}},
	} {
		if d := Detect(f.env()); d.Browser != "chrome" {
			t.Errorf("%s: %+v", name, d)
		}
	}
	// both fail: said clearly, with the setting
	f = &fakeEnv{goos: "darwin", fail: map[string]bool{"osascript": true, "plutil": true}}
	if d := Detect(f.env()); d.Browser != "" || !strings.Contains(d.Problem, "cookieBrowser") {
		t.Errorf("%+v", d)
	}
}
