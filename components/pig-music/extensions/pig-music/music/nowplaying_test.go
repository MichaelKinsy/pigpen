package music

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNowPlayingModeDefaultsToTheFooterAndAutoMeansFooterForNow(t *testing.T) {
	for in, want := range map[string]string{"": "footer", "auto": "footer", "footer": "footer", "widget": "widget"} {
		if got := (Settings{NowPlaying: in}).NowPlayingMode(); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestANowPlayingValueThatIsNoneOfTheThreeIsRefusedNamingTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"nowPlaying": "sideways"}`), 0o600)
	_, err := LoadSettings(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "auto, footer or widget") {
		t.Fatalf("%v", err)
	}
}

func TestUpdateSettingsValueWritesStringsAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p", "settings.json")
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte(`{"future": {"a": 1}, "stopOnExit": false}`), 0o600)
	if err := UpdateSettingsValue(path, "cookieBrowser", "chrome"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSettingsValue(path, "nowPlaying", "widget"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"future"`, `"cookieBrowser": "chrome"`, `"nowPlaying": "widget"`, `"stopOnExit": false`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%s lacks %s", data, want)
		}
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	s, err := LoadSettings(path)
	if err != nil || s.CookieBrowser != "chrome" || s.NowPlayingMode() != "widget" {
		t.Errorf("%+v %v", s, err)
	}
}

func TestAnEmptyStringRemovesTheKeySoTheDefaultReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	UpdateSettingsValue(path, "cookieBrowser", "chrome")
	if err := UpdateSettingsValue(path, "cookieBrowser", ""); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "cookieBrowser") {
		t.Errorf("%s", data)
	}
}

func TestThePaletteIsOnByDefaultAndCanBeSwitchedOff(t *testing.T) {
	if !(Settings{}).PaletteOrDefault() {
		t.Error("the default is on")
	}
	off := false
	if (Settings{Palette: &off}).PaletteOrDefault() {
		t.Error("false is off")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"palette": false}`), 0o600)
	if s, err := LoadSettings(path); err != nil || s.PaletteOrDefault() {
		t.Errorf("%+v %v", s, err)
	}
}

func TestPulseIsOnAndCalmIsOffByDefault(t *testing.T) {
	if !(Settings{}).PulseOrDefault() || (Settings{}).CalmOrDefault() {
		t.Error("defaults: pulse on, calm off")
	}
	yes, no := true, false
	if (Settings{Pulse: &no}).PulseOrDefault() || !(Settings{Calm: &yes}).CalmOrDefault() {
		t.Error("explicit values")
	}
}
