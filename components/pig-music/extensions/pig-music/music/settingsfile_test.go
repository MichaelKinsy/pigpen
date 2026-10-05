package music

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateSettingsFileKeepsUnknownKeysAndWritesPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pig-music", "settings.json")
	// a missing file and directory are created
	if err := UpdateSettingsFile(path, "stopOnExit", false); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(path)
	if err != nil || s.StopOnExitOrDefault() {
		t.Fatalf("%+v %v", s, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	// keys the screen does not know are kept, whatever their type
	if err := os.WriteFile(path, []byte(`{"stopOnExit": true, "cookieBrowser": "firefox:work", "futureThing": {"a": [1, 2]}, "mpvPath": "/m"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSettingsFile(path, "footerStatus", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{`"cookieBrowser": "firefox:work"`, `"futureThing"`, `"mpvPath": "/m"`, `"footerStatus": false`, `"stopOnExit": true`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the file lost %q:\n%s", want, b)
		}
	}
	s, _ = LoadSettings(path)
	if s.FooterStatusOrDefault() || !s.StopOnExitOrDefault() || s.CookieBrowser != "firefox:work" {
		t.Errorf("%+v", s)
	}
}

func TestUpdateSettingsFileNeverOverwritesAFileItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	broken := []byte(`{"stopOnExit": tru`)
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	err := UpdateSettingsFile(path, "stopOnExit", false)
	if err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(broken) {
		t.Errorf("the broken file was overwritten: %q", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temporary files left: %v", entries)
	}
	if err := UpdateSettingsFile(path, "", true); err == nil {
		t.Error("an empty key was accepted")
	}
}
