// Package cookies decides which browser's YouTube session yt-dlp may read to list the user's library, and records the
// user's consent to that. It never reads a cookie, a cookie file or a browser profile: it only asks the operating system
// which browser is the default, and hands yt-dlp a browser name for --cookies-from-browser, for library calls only.
package cookies

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Supported are the browsers yt-dlp's --cookies-from-browser takes.
var Supported = []string{"brave", "chrome", "chromium", "edge", "firefox", "opera", "safari", "vivaldi", "whale"}

func supported(name string) bool {
	for _, s := range Supported {
		if s == name {
			return true
		}
	}
	return false
}

// Env is what detection asks of the machine.
type Env struct {
	GOOS   string
	Getenv func(string) string
	// Run runs a program and returns its standard output.
	Run  func(name string, args ...string) (string, error)
	Home string
}

// Where the browser came from.
const (
	FromSetting   = "setting"
	FromDetection = "detected"
)

// Detection is the system's default browser, or why there is none to use.
type Detection struct {
	Browser string // a yt-dlp browser name, "" when none
	Raw     string // what the system reported (bundle ID, .desktop file, ProgId)
	Problem string
}

func hint() string {
	return `Set "cookieBrowser" in the pig-music settings to one of: ` + strings.Join(Supported, ", ") + ` (yt-dlp's BROWSER[:PROFILE] form; PROFILE may be the profile's directory).`
}

func unsupported(raw string) Detection {
	return Detection{Raw: raw, Problem: fmt.Sprintf("the default browser (%s) is not one whose cookies yt-dlp reads under a browser name it knows "+
		"(other browsers, beta or developer channels, and Flatpak or Snap builds of Chromium browsers keep them elsewhere). %s", raw, hint())}
}

func unknown(why string) Detection {
	return Detection{Problem: why + " " + hint()}
}

// Detect asks the operating system for the default https handler and maps it to a yt-dlp browser name. It does not guess:
// anything it cannot read or map is reported as a problem.
func Detect(env Env) Detection {
	switch env.GOOS {
	case "darwin":
		return detectDarwin(env)
	case "linux":
		return detectLinux(env)
	case "windows":
		return detectWindows(env)
	}
	return unknown("there is no browser to read cookies from on " + env.GOOS + ".")
}

var darwinBundles = map[string]string{
	"com.apple.safari": "safari", "com.google.chrome": "chrome", "org.mozilla.firefox": "firefox", "com.brave.browser": "brave",
	"com.microsoft.edgemac": "edge", "com.vivaldi.vivaldi": "vivaldi", "com.operasoftware.opera": "opera",
	"org.chromium.chromium": "chromium", "com.naver.whale": "whale",
}

// darwinAsk asks LaunchServices, through JavaScript for Automation (no cgo), which application opens an https URL, and
// prints its bundle ID. This is what macOS itself uses, including when no handler was ever recorded (Safari).
const darwinAsk = `ObjC.import('AppKit');
var app = $.NSWorkspace.sharedWorkspace.URLForApplicationToOpenURL($.NSURL.URLWithString('https://music.youtube.com/'));
var b = app.isNil() ? null : $.NSBundle.bundleWithURL(app);
(b && !b.isNil() && ObjC.unwrap(b.bundleIdentifier)) || ''`

var bundleID = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9.-]{0,200}$`)

func darwinBrowser(id string) Detection {
	if b, ok := darwinBundles[strings.ToLower(id)]; ok {
		return Detection{Browser: b, Raw: id}
	}
	return unsupported(id)
}

// safariByDefault is what macOS uses when no https handler was ever recorded: Safari, its built-in default.
const safariByDefault = "com.apple.Safari (no https handler recorded, so macOS's built-in default)"

func detectDarwin(env Env) Detection {
	if out, err := env.Run("osascript", "-l", "JavaScript", "-e", darwinAsk); err == nil {
		if id := strings.TrimSpace(out); bundleID.MatchString(id) {
			return darwinBrowser(id)
		}
	}
	// The system could not be asked (no osascript, no window server): read LaunchServices' own record.
	plist := filepath.Join(env.Home, "Library", "Preferences", "com.apple.LaunchServices", "com.apple.launchservices.secure.plist")
	out, err := env.Run("plutil", "-convert", "json", "-o", "-", "--", plist)
	if err != nil {
		return unknown("could not read macOS's default-browser setting (" + err.Error() + ").")
	}
	var doc struct {
		Handlers []struct {
			Scheme string `json:"LSHandlerURLScheme"`
			Role   string `json:"LSHandlerRoleAll"`
		} `json:"LSHandlers"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return unknown("could not read macOS's default-browser setting (not JSON).")
	}
	for _, h := range doc.Handlers {
		if strings.EqualFold(h.Scheme, "https") && h.Role != "" {
			return darwinBrowser(h.Role)
		}
	}
	return Detection{Browser: "safari", Raw: safariByDefault}
}

// linuxDesktops are the desktop entries of the builds whose cookies yt-dlp reads under that name. yt-dlp reads one fixed
// directory per Chromium-based browser (~/.config/google-chrome, BraveSoftware/Brave-Browser, opera, ...), so a beta or
// developer channel, a Flatpak or a Snap keeps its cookies where yt-dlp does not look under that name, and mapping it there
// would read another browser's cookies: those are reported, not mapped. Firefox's profiles are found wherever its channels
// and packages keep them (yt-dlp searches the native, Flatpak and Snap places), so any Firefox entry maps to firefox.
var linuxDesktops = map[string]string{
	"google-chrome.desktop": "chrome", "chromium.desktop": "chromium", "chromium-browser.desktop": "chromium",
	"brave-browser.desktop": "brave", "microsoft-edge.desktop": "edge", "vivaldi-stable.desktop": "vivaldi",
	"vivaldi.desktop": "vivaldi", "opera.desktop": "opera", "naver-whale.desktop": "whale",
}

func detectLinux(env Env) Detection {
	if env.Getenv("DISPLAY") == "" && env.Getenv("WAYLAND_DISPLAY") == "" {
		return unknown("there is no graphical session here (no DISPLAY or WAYLAND_DISPLAY), so there is no default browser to read.")
	}
	out, err := env.Run("xdg-settings", "get", "default-web-browser")
	if err != nil {
		return unknown("could not ask xdg-settings for the default browser (" + err.Error() + ").")
	}
	raw := strings.TrimSpace(out)
	l := strings.ToLower(raw)
	if b, ok := linuxDesktops[l]; ok {
		return Detection{Browser: b, Raw: raw}
	}
	if strings.Contains(l, "firefox") {
		return Detection{Browser: "firefox", Raw: raw}
	}
	if raw == "" {
		return unknown("xdg-settings reports no default browser.")
	}
	return unsupported(raw)
}

var windowsProgIDs = []struct{ prefix, browser string }{
	{"firefoxurl", "firefox"}, {"chromehtml", "chrome"}, {"msedgehtm", "edge"}, {"bravehtml", "brave"}, {"vivaldihtm", "vivaldi"},
	{"operastable", "opera"}, {"chromiumhtm", "chromium"}, {"whalehtml", "whale"}, // not Opera GX: yt-dlp reads "Opera Stable"
}

func detectWindows(env Env) Detection {
	out, err := env.Run("reg", "query", `HKCU\Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`, "/v", "ProgId")
	if err != nil {
		return unknown("could not read Windows's default-browser setting (" + err.Error() + ").")
	}
	var progID string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "ProgId" {
			progID = f[2]
		}
	}
	if progID == "" {
		return unknown("Windows records no default browser for https.")
	}
	l := strings.ToLower(progID)
	for _, p := range windowsProgIDs {
		if strings.HasPrefix(l, p.prefix) {
			return Detection{Browser: p.browser, Raw: progID}
		}
	}
	return unsupported(progID)
}

// Choice is the browser the library would be read from.
type Choice struct {
	// Spec is what goes after --cookies-from-browser; "" when there is no usable browser.
	Spec string
	// Browser is the yt-dlp browser name (the spec without keyring, profile and container).
	Browser string
	From    string // FromSetting or FromDetection
	Raw     string // what the system reported, for a detected browser
	Problem string
}

// Resolve prefers the cookieBrowser setting, which yt-dlp takes as BROWSER[+KEYRING][:PROFILE][::CONTAINER], and otherwise
// the system's default browser with its default profile.
func Resolve(setting string, env Env) Choice {
	setting = strings.TrimSpace(setting)
	if setting != "" {
		name := setting
		if i := strings.IndexAny(name, "+:"); i >= 0 {
			name = name[:i]
		}
		name = strings.ToLower(name)
		if !supported(name) {
			return Choice{From: FromSetting, Problem: fmt.Sprintf(`the cookieBrowser setting %q is not a browser yt-dlp supports (%s)`, setting, strings.Join(Supported, ", "))}
		}
		return Choice{Spec: setting, Browser: name, From: FromSetting}
	}
	d := Detect(env)
	if d.Browser == "" {
		return Choice{From: FromDetection, Raw: d.Raw, Problem: d.Problem}
	}
	return Choice{Spec: d.Browser, Browser: d.Browser, From: FromDetection, Raw: d.Raw}
}

// Describe names the browser, for doctor and the consent prompt: the name only.
func (c Choice) Describe() string {
	switch {
	case c.Problem != "":
		return c.Problem
	case c.From == FromSetting:
		return c.Spec + " (the cookieBrowser setting)"
	}
	return c.Browser + " (the default browser, " + c.Raw + ")"
}

// Notes is what to expect when yt-dlp reads that browser's cookies on this system.
func Notes(browser, goos string) string {
	switch browser {
	case "firefox":
		return "Firefox: no prompt; the most recently used profile is read."
	case "safari":
		return "Safari: macOS needs Full Disk Access for the terminal app running PiG (System Settings, Privacy & Security, Full Disk Access)."
	}
	switch goos {
	case "darwin":
		return strings.ToUpper(browser[:1]) + browser[1:] + ": macOS asks to let yt-dlp use the browser's \"Safe Storage\" item in your Keychain; choose Allow."
	case "linux":
		return strings.ToUpper(browser[:1]) + browser[1:] + ": yt-dlp reads your keyring (GNOME Keyring or KWallet) to decrypt the cookies; it may ask to unlock it."
	case "windows":
		return strings.ToUpper(browser[:1]) + browser[1:] + ": reading can fail while the browser is open (its cookie database is locked); close it and try again."
	}
	return ""
}

// Consent records which browsers the user agreed to let yt-dlp read for library calls. The file holds browser names only.
type Consent struct{ Path string }

type consentFile struct {
	Browsers []string `json:"browsers"`
}

func (c Consent) load() consentFile {
	var f consentFile
	if b, err := os.ReadFile(c.Path); err == nil {
		if json.Unmarshal(b, &f) != nil {
			return consentFile{} // a broken file grants nothing
		}
	}
	return f
}

// Granted reports whether the user agreed for this browser.
func (c Consent) Granted(browser string) bool {
	for _, b := range c.load().Browsers {
		if b == browser {
			return true
		}
	}
	return false
}

func (c Consent) save(f consentFile) error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.Path), ".consent-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.Path)
}

// Grant records consent for the browser.
func (c Consent) Grant(browser string) error {
	if browser == "" {
		return errors.New("no browser to grant")
	}
	f := c.load()
	if c.Granted(browser) {
		return nil
	}
	f.Browsers = append(f.Browsers, browser)
	return c.save(f)
}

// Revoke withdraws consent for the browser.
func (c Consent) Revoke(browser string) error {
	f := c.load()
	var keep []string
	for _, b := range f.Browsers {
		if b != browser {
			keep = append(keep, b)
		}
	}
	f.Browsers = keep
	return c.save(f)
}

// Access is the library's way to the user's browser session: which browser, and whether the user agreed. It is what
// ytdlp.Source asks (through Spec) before every library call.
type Access struct {
	// Setting is the cookieBrowser setting; empty means the system's default browser.
	Setting string
	Env     Env
	Consent Consent
}

// Choice resolves the browser now, so a changed default browser or setting takes effect at the next call.
func (a Access) Choice() Choice { return Resolve(a.Setting, a.Env) }

// Spec returns the --cookies-from-browser value, or says why not: *music.NoBrowserError when there is no usable browser,
// *music.NeedsConsentError until the user agrees to this browser.
func (a Access) Spec(context.Context) (string, error) {
	c := a.Choice()
	if c.Spec == "" {
		return "", &music.NoBrowserError{Reason: c.Problem}
	}
	if !a.Consent.Granted(c.Browser) {
		return "", &music.NeedsConsentError{Browser: c.Browser, Description: c.Describe(), Notes: Notes(c.Browser, a.Env.GOOS)}
	}
	return c.Spec, nil
}

// Grant records the user's agreement for browser, the one the prompt named. If the browser in use is no longer that one
// (the default browser or the setting changed while the prompt was shown), nothing is recorded and the answer is the
// question for the browser in use now.
func (a Access) Grant(_ context.Context, browser string) error {
	c := a.Choice()
	if c.Spec == "" {
		return &music.NoBrowserError{Reason: c.Problem}
	}
	if c.Browser != browser {
		return &music.NeedsConsentError{Browser: c.Browser, Description: c.Describe(), Notes: Notes(c.Browser, a.Env.GOOS)}
	}
	return a.Consent.Grant(c.Browser)
}
