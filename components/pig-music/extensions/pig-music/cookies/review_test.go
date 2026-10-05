package cookies

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// On a Mac whose default browser was never changed, LaunchServices records no https handler, and the default is Safari
// (seen on the owner's Mac, macOS 26: NSWorkspace URLForApplicationToOpenURL returns Safari.app). Detection asks the
// system first (osascript, JavaScript for Automation, no cgo) and falls back to the plist, where no https handler means
// Safari.
func TestMacOSAsksTheSystemFirstAndANeverChangedDefaultIsSafari(t *testing.T) {
	f := &fakeEnv{goos: "darwin", output: map[string]string{"osascript": "com.apple.Safari\n"}}
	d := Detect(f.env())
	if d.Browser != "safari" || d.Problem != "" || d.Raw != "com.apple.Safari" {
		t.Errorf("system answer Safari: %+v", d)
	}
	if len(f.ran) != 1 || !strings.HasPrefix(f.ran[0], "osascript -l JavaScript") || !strings.Contains(f.ran[0], "URLForApplicationToOpenURL") {
		t.Errorf("ran %v", f.ran)
	}

	f = &fakeEnv{goos: "darwin", output: map[string]string{"osascript": "com.google.Chrome\n"}}
	if d := Detect(f.env()); d.Browser != "chrome" || d.Raw != "com.google.Chrome" {
		t.Errorf("system answer Chrome: %+v", d)
	}

	// the system's answer is the answer: an unsupported browser is reported, not replaced by the plist's
	f = &fakeEnv{goos: "darwin", output: map[string]string{
		"osascript": "company.thebrowser.Browser\n",
		"plutil":    macPlist(`{"LSHandlerRoleAll":"com.google.chrome","LSHandlerURLScheme":"https"}`),
	}}
	if d := Detect(f.env()); d.Browser != "" || d.Raw != "company.thebrowser.Browser" || !strings.Contains(d.Problem, "cookieBrowser") {
		t.Errorf("unsupported system answer: %+v", d)
	}

	// osascript unavailable or silent: the plist; no https handler there is Safari, macOS's built-in default
	for name, out := range map[string]map[string]string{
		"osascript fails": {"plutil": macPlist(`{"LSHandlerRoleAll":"x.other","LSHandlerURLScheme":"mailto"}`)},
		"osascript empty": {"osascript": "\n", "plutil": macPlist(`{"LSHandlerRoleAll":"x.other","LSHandlerURLScheme":"mailto"}`)},
	} {
		f = &fakeEnv{goos: "darwin", output: out}
		d := Detect(f.env())
		if d.Browser != "safari" || d.Problem != "" || !strings.Contains(strings.ToLower(d.Raw), "safari") {
			t.Errorf("%s, no https handler recorded: %+v", name, d)
		}
		if len(f.ran) != 2 || !strings.HasPrefix(f.ran[1], "plutil -convert json") {
			t.Errorf("%s: ran %v", name, f.ran)
		}
	}
	f = &fakeEnv{goos: "darwin", output: map[string]string{"plutil": macPlist(`{"LSHandlerRoleAll":"org.mozilla.firefox","LSHandlerURLScheme":"https"}`)}}
	if d := Detect(f.env()); d.Browser != "firefox" {
		t.Errorf("plist fallback with a handler: %+v", d)
	}

	// neither answers: said, not guessed
	f = &fakeEnv{goos: "darwin", fail: map[string]bool{"osascript": true, "plutil": true}}
	if d := Detect(f.env()); d.Browser != "" || !strings.Contains(d.Problem, "cookieBrowser") {
		t.Errorf("nothing readable: %+v", d)
	}
}

// yt-dlp reads one fixed profile directory per browser name (google-chrome, BraveSoftware/Brave-Browser, opera, "Opera
// Stable", ...). A beta or developer channel, Opera GX, or a Flatpak or Snap package keeps its cookies elsewhere, so mapping
// it to the stable name would make yt-dlp read another browser's cookies (possibly another account's) than the default.
// That is a guess; it must be reported instead.
func TestOtherChannelsAndPackagingsOfABrowserAreNotTakenForIt(t *testing.T) {
	for _, desktop := range []string{
		"google-chrome-beta.desktop", "google-chrome-unstable.desktop", "com.google.Chrome.desktop",
		"microsoft-edge-beta.desktop", "microsoft-edge-dev.desktop", "brave-browser-beta.desktop", "brave-browser-nightly.desktop",
		"com.brave.Browser.desktop", "opera-beta.desktop", "opera-developer.desktop", "vivaldi-snapshot.desktop",
		"chromium_chromium.desktop", "org.chromium.Chromium.desktop",
	} {
		f := &fakeEnv{goos: "linux", vars: map[string]string{"DISPLAY": ":0"}, output: map[string]string{"xdg-settings get default-web-browser": desktop + "\n"}}
		if d := Detect(f.env()); d.Browser != "" || !strings.Contains(d.Problem, desktop) || !strings.Contains(d.Problem, "cookieBrowser") {
			t.Errorf("linux %s: %+v", desktop, d)
		}
	}
	for _, progID := range []string{"OperaGXStable", "ChromeBHTML", "MSEdgeDHTML"} {
		out := "    ProgId    REG_SZ    " + progID + "\r\n"
		f := &fakeEnv{goos: "windows", output: map[string]string{"reg query": out}}
		if d := Detect(f.env()); d.Browser != "" || !strings.Contains(d.Problem, progID) {
			t.Errorf("windows %s: %+v", progID, d)
		}
	}
	for _, bundle := range []string{"com.operasoftware.OperaGX", "com.google.Chrome.beta", "com.microsoft.edgemac.Dev"} {
		f := &fakeEnv{goos: "darwin", output: map[string]string{"osascript": bundle + "\n"}}
		if d := Detect(f.env()); d.Browser != "" || !strings.Contains(d.Problem, bundle) {
			t.Errorf("macOS %s: %+v", bundle, d)
		}
	}
}

// Consent is given to the browser the prompt named. If the default browser changed while the prompt was open, a yes must
// not be recorded for the new browser, which the user was never shown; the new browser is a new question.
func TestConsentIsRecordedOnlyForTheBrowserThePromptNamed(t *testing.T) {
	f := &fakeEnv{goos: "linux", vars: map[string]string{"DISPLAY": ":0"}, output: map[string]string{"xdg-settings": "google-chrome.desktop"}}
	a := Access{Env: f.env(), Consent: Consent{Path: filepath.Join(t.TempDir(), "c.json")}}
	_, err := a.Spec(context.Background())
	var need *music.NeedsConsentError
	if !errors.As(err, &need) || need.Browser != "chrome" {
		t.Fatalf("%v", err)
	}
	f.output["xdg-settings"] = "firefox.desktop" // changed while the prompt for chrome was shown
	err = a.Grant(context.Background(), need.Browser)
	if !errors.As(err, &need) || need.Browser != "firefox" {
		t.Errorf("a yes for chrome while firefox is the browser: %v", err)
	}
	if a.Consent.Granted("firefox") || a.Consent.Granted("chrome") {
		t.Error("consent recorded for a browser the user was not asked about, or for one no longer in use")
	}
	if err := a.Grant(context.Background(), "firefox"); err != nil || !a.Consent.Granted("firefox") {
		t.Errorf("a yes for the browser shown: %v", err)
	}
}
