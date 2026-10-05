//go:build !windows

package mpv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// TestRealMpvHookCarriesNoCookiesWhateverTheUsersMpvConfSays proves the rule with a real mpv: the user's mpv.conf (in a
// throwaway HOME) asks for --cookies-from-browser, and a fake yt-dlp records what mpv's hook runs it with. The fake reads
// nothing and exits 1; no browser, cookie or network is touched.
//
//	PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvHookCarriesNoCookies -v
func TestRealMpvHookCarriesNoCookiesWhateverTheUsersMpvConfSays(t *testing.T) {
	if os.Getenv("PIG_MUSIC_SMOKE") != "1" {
		t.Skip("set PIG_MUSIC_SMOKE=1 to run against a real mpv")
	}
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Fatal("the smoke test needs mpv on PATH")
	}
	dir := shortDir(t)
	home := filepath.Join(dir, "home")
	conf := filepath.Join(home, ".config", "mpv")
	if err := os.MkdirAll(filepath.Join(conf, "script-opts"), 0o700); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(conf, "mpv.conf"), []byte("ytdl-raw-options=cookies-from-browser=firefox\n[protocol.https]\nytdl-raw-options-append=cookies-from-browser=chrome\n"), 0o600))
	argv := filepath.Join(dir, "argv")
	fake := filepath.Join(dir, "yt-dlp")
	must(os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> '"+argv+"'\necho 'ERROR: fake' >&2\nexit 1\n"), 0o700))

	cfg := Config{
		Paths:     music.PathsIn(filepath.Join(dir, "run")),
		YtdlPath:  fake,
		PlayURL:   func(t music.Track) string { return "https://music.youtube.com/watch?v=" + t.ID },
		ExtraArgs: []string{"--ao=null"},
		Env:       []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config")},
	}
	p := New(cfg)
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx5(t)) })
	if err := p.Replace(ctx5(t), []music.Track{{ID: "dQw4w9WgXcQ", Title: "x"}}, 0); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if got, _ = os.ReadFile(argv); len(got) > 0 {
			break
		}
	}
	if len(got) == 0 {
		t.Fatal("mpv's hook never ran the fake yt-dlp")
	}
	if strings.Contains(string(got), "cookies") {
		t.Errorf("mpv's hook ran yt-dlp with cookies:\n%s", got)
	}
	if !strings.Contains(string(got), "--ignore-config") {
		t.Errorf("the hook's yt-dlp reads the user's yt-dlp config:\n%s", got)
	}
}
