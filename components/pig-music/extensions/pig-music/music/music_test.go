package music

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

func TestDefaultPathsPreferTheRuntimeDirectory(t *testing.T) {
	run := t.TempDir()
	p, err := DefaultPaths(env(map[string]string{"XDG_RUNTIME_DIR": run, "PIG_HOME": "/pighome", "HOME": "/home/u"}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != filepath.Join(run, "pig-music") || p.Socket != filepath.Join(run, "pig-music", "mpv.sock") || p.Meta != filepath.Join(run, "pig-music", "tracks.json") {
		t.Errorf("paths %+v", p)
	}
	// Settings are the extension's own file under the agent directory, not in the volatile runtime directory.
	if p.Settings != filepath.Join("/pighome", "agent", "pig-music", "settings.json") {
		t.Errorf("settings %s", p.Settings)
	}
}

func TestDefaultPathsFallBackToTheAgentDirectory(t *testing.T) {
	for name, tc := range map[string]struct {
		vars map[string]string
		want string
	}{
		"agent dir":      {map[string]string{"PIG_CODING_AGENT_DIR": "/agent", "PIG_HOME": "/ignored"}, "/agent/pig-music"},
		"pig home":       {map[string]string{"PIG_HOME": "/pighome"}, "/pighome/agent/pig-music"},
		"home":           {map[string]string{"HOME": "/home/u"}, "/home/u/.pig/agent/pig-music"},
		"runtime absent": {map[string]string{"XDG_RUNTIME_DIR": "/no/such/dir", "PIG_HOME": "/pighome"}, "/pighome/agent/pig-music"},
	} {
		p, err := DefaultPaths(env(tc.vars))
		if err != nil || p.Dir != tc.want {
			t.Errorf("%s: %+v %v, want %s", name, p, err, tc.want)
		}
	}
}

func TestATooLongSocketPathIsRefusedWithAWayOut(t *testing.T) {
	run := filepath.Join(t.TempDir(), strings.Repeat("d", 90))
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := DefaultPaths(env(map[string]string{"XDG_RUNTIME_DIR": run}))
	if err == nil || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureDirIsPrivate(t *testing.T) {
	p := PathsIn(filepath.Join(t.TempDir(), "a", "pig-music"))
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p.Dir)
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestSettings(t *testing.T) {
	dir := t.TempDir()
	if s, err := LoadSettings(filepath.Join(dir, "none.json")); err != nil || !s.StopOnExitOrDefault() || s.MPVPath != "" {
		t.Fatalf("missing file: %+v %v", s, err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"stopOnExit":false,"cookieBrowser":"firefox","mpvPath":"/m","ytdlpPath":"/y","unknown":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(path)
	if err != nil || s.StopOnExitOrDefault() || s.CookieBrowser != "firefox" || s.MPVPath != "/m" || s.YtdlpPath != "/y" {
		t.Fatalf("%+v %v", s, err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSettings(path); err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("malformed: %v", err)
	}
}

func TestCheckDependenciesNamesWhatIsMissingAndHowToGetIt(t *testing.T) {
	none := func(string) (string, error) { return "", errors.New("no") }
	err := CheckDependencies("", "", none)
	var m *MissingError
	if !errors.As(err, &m) || len(m.Names) != 2 {
		t.Fatalf("%v", err)
	}
	m.GOOS = "linux"
	msg := m.Error()
	for _, want := range []string{"mpv and yt-dlp", "are not installed", "apt-get install mpv", "github.com/yt-dlp/yt-dlp"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	m.GOOS = "darwin"
	if msg := m.Error(); !strings.Contains(msg, "brew install mpv") || !strings.Contains(msg, "brew install yt-dlp") {
		t.Errorf("darwin message: %s", msg)
	}
	onlyMpv := func(p string) (string, error) {
		if p == "yt-dlp" {
			return "/bin/yt-dlp", nil
		}
		return "", errors.New("no")
	}
	err = CheckDependencies("", "", onlyMpv)
	if !errors.As(err, &m) || len(m.Names) != 1 || m.Names[0] != "mpv" || !strings.Contains(err.Error(), "which is not installed") {
		t.Fatalf("one missing: %v", err)
	}
	var asked []string
	_ = CheckDependencies("/opt/mpv", "/opt/yt", func(p string) (string, error) { asked = append(asked, p); return p, nil })
	if strings.Join(asked, " ") != "/opt/mpv /opt/yt" {
		t.Errorf("asked %v", asked)
	}
	if err := CheckDependencies("", "", func(p string) (string, error) { return p, nil }); err != nil {
		t.Fatal(err)
	}
}
