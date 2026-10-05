package pig_music_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pig_music "github.com/MichaelKinsy/pigpen/pig-music"
	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

type fixedSource struct{ tracks []music.Track }

func (s fixedSource) Search(context.Context, string, int) ([]music.Track, error) {
	return s.tracks, nil
}
func (s fixedSource) Library(context.Context) ([]music.Collection, error) {
	return nil, errors.New("needs cookies")
}
func (s fixedSource) Tracks(context.Context, string) ([]music.Track, error) { return nil, nil }
func (s fixedSource) PlayURL(t music.Track) string {
	return "https://music.youtube.com/watch?v=" + t.ID
}

func songs(n int) []music.Track {
	out := make([]music.Track, n)
	for i := range out {
		out[i] = music.Track{ID: fmt.Sprintf("song%07d", i), Title: fmt.Sprintf("Song %d", i+1), Artists: []string{"Some Artist"}, Duration: 200 * time.Second}
	}
	return out
}

// player starts a fake mpv and the extension over it.
func player(t *testing.T) (*overlayHost, *mpvfake.Server) {
	h, srv, _, _ := playerEnv(t)
	return h, srv
}

// playerEnv is player with the environment (settings and the mpv socket live under it) and the real Player the extension uses.
func playerEnv(t *testing.T) (*overlayHost, *mpvfake.Server, map[string]string, *mpv.Player) {
	t.Helper()
	return playerEnvWith(t, nil)
}

// playerEnvWith is playerEnv with more environment variables (COLORTERM, NO_COLOR...).
func playerEnvWith(t *testing.T, extra map[string]string) (*overlayHost, *mpvfake.Server, map[string]string, *mpv.Player) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	env := map[string]string{"XDG_RUNTIME_DIR": dir, "HOME": dir}
	for k, v := range extra {
		env[k] = v
	}
	paths, err := music.DefaultPaths(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	src := fixedSource{songs(5)}
	p := mpv.New(mpv.Config{Paths: paths, PlayURL: src.PlayURL})
	t.Cleanup(func() { _ = p.Close() })
	return startOverlayHost(t, pig_music.ExtensionWith(src, p, env, nil, nil), "tui", 100, 30), srv, env, p
}

func screenText(s snapshot) string { return strings.Join(s.Lines, "\n") }

func hasText(want string) func(snapshot) bool {
	return func(s snapshot) bool { return strings.Contains(screenText(s), want) }
}

func TestMusicOpensThePlayerSearchesPlaysAndHidesWhileTheMusicContinues(t *testing.T) {
	h, srv := player(t)
	done := h.command("music")
	args := h.waitOpen()
	if layout, _ := args["overlayOptions"].(map[string]any); layout["width"] != "100%" || layout["maxHeight"] != "100%" || args["overlay"] != true {
		t.Fatalf("overlay request %v", args)
	}
	first := h.waitSnapshot("the search screen", hasText("Press / to search"))
	if len(first.Lines) != 30 || first.Width != 100 {
		t.Fatalf("first snapshot %d lines at width %d", len(first.Lines), first.Width)
	}
	for _, in := range []string{"/", "n", "i", "g", "h", "t", "\r"} {
		h.input(in)
	}
	h.waitSnapshot("results", hasText("Song 5"))
	h.input("\r") // play the first result
	h.waitSnapshot("the player screen", func(s snapshot) bool {
		return strings.Contains(screenText(s), "Up Next") && strings.Contains(screenText(s), "Playing")
	})
	if urls, pos := srv.Playlist(); len(urls) != 5 || pos != 0 {
		t.Fatalf("mpv has %d entries, playing %d", len(urls), pos)
	}
	h.input(" ")
	h.waitSnapshot("paused", hasText("Paused"))
	if !srv.Paused() {
		t.Fatal("space did not pause mpv")
	}
	h.input(" ")
	h.waitSnapshot("playing again", func(s snapshot) bool { return strings.Contains(screenText(s), "Playing") && !srv.Paused() })

	// The progress bar moves without input: the player's states reach the screen.
	srv.Advance(30)
	h.waitSnapshot("0:30 on the play bar", hasText("0:30"))

	h.input("q")
	if failure := <-done; failure != "" {
		t.Fatal(failure)
	}
	// Hidden: the music and the model go on, and no frame is sent.
	hiddenAt := h.snapshotCount()
	srv.Advance(10)
	time.Sleep(300 * time.Millisecond)
	if got := h.snapshotCount(); got != hiddenAt {
		t.Fatalf("%d snapshots were sent while hidden", got-hiddenAt)
	}

	// /music again: the same screen, queue and track, at the position mpv has reached.
	done = h.command("music")
	h.waitOpenN(2)
	reopened := h.waitSnapshot("the reopened player", func(s snapshot) bool {
		return len(h.snapshots) > hiddenAt && strings.Contains(screenText(s), "0:40")
	})
	text := screenText(reopened)
	for _, want := range []string{"Up Next", "Song 1", "Song 5", "Playing"} {
		if !strings.Contains(text, want) {
			t.Errorf("reopened screen lacks %q:\n%s", want, text)
		}
	}
	h.input("q")
	<-done
}

func TestResizeAndHeightChangeRelayOutThePlayer(t *testing.T) {
	h, _ := player(t)
	done := h.command("music")
	h.waitOpen()
	h.waitSnapshot("search screen", hasText("Press / to search"))
	h.resize(70, 0)
	w70 := h.waitSnapshot("width 70", func(s snapshot) bool { return s.Width == 70 })
	h.resize(0, 18)
	tall := h.waitSnapshot("height 18", func(s snapshot) bool { return len(s.Lines) == 18 })
	if w70.Width != 70 || len(tall.Lines) != 18 {
		t.Fatal("not relaid out")
	}
	for _, s := range h.allSnapshots() {
		for i, l := range s.Lines {
			if visible(l) > s.Width {
				t.Fatalf("snapshot %d line %d is %d cells for width %d", s.Seq, i, visible(l), s.Width)
			}
		}
	}
	h.input("q")
	<-done
}

func TestMissingProgramsAreNamedWithAFixAndNothingOpens(t *testing.T) {
	ext := pig_music.ExtensionWith(nil, nil, map[string]string{"HOME": t.TempDir()}, func(string) (string, error) { return "", errors.New("no") }, nil)
	h := startOverlayHost(t, ext, "tui", 100, 30)
	if failure := <-h.command("music"); failure != "" {
		t.Fatal(failure)
	}
	notes := h.notifications()
	msg := fmt.Sprint(notes[0]["message"])
	if len(notes) != 1 || notes[0]["level"] != "warning" || !strings.Contains(msg, "mpv") || !strings.Contains(msg, "yt-dlp") || !strings.Contains(msg, "fix:") {
		t.Fatalf("notifications %v", notes)
	}
	h.mu.Lock()
	opened := len(h.opens)
	h.mu.Unlock()
	if opened != 0 {
		t.Fatal("a component opened")
	}
}

// fakePrograms writes an mpv and a yt-dlp that pass the doctor, and the doctor's machine for them.
func fakePrograms(t *testing.T, mpvBody string) (map[string]string, *doctor.Env) {
	t.Helper()
	dir, _ := os.MkdirTemp("", "pm")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mpvPath := write("mpv", mpvBody)
	ytPath := write("yt-dlp", `if [ "$1" = "--version" ]; then echo 2026.08.19; exit 0; fi
echo "[debug] Optional libraries: yt_dlp_ejs-0.8.0" >&2
echo "[debug] JS runtimes: deno-2.5.0" >&2
exit 2`)
	env := map[string]string{"XDG_RUNTIME_DIR": dir, "HOME": dir, "PIG_MUSIC_MPV": mpvPath, "PIG_MUSIC_YTDLP": ytPath}
	d := doctor.EnvFrom(func(k string) string { return env[k] }, nil)
	d.Now = func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }
	d.Exists = func(p string) bool { _, err := os.Stat(p); return err == nil || strings.Contains(p, "/pulse/") }
	return env, &d
}

func TestAnMpvThatWillNotStartIsReportedNotCrashed(t *testing.T) {
	env, d := fakePrograms(t, `if [ "$1" = "--version" ]; then echo "mpv 0.37.0 Copyright"; exit 0; fi
exit 1`)
	h := startOverlayHost(t, pig_music.ExtensionWith(nil, nil, env, nil, d), "tui", 100, 30)
	if failure := <-h.command("music"); failure != "" {
		t.Fatal(failure)
	}
	notes := h.notifications()
	if len(notes) != 1 || !strings.Contains(fmt.Sprint(notes[0]["message"]), "mpv exited before it opened its socket") {
		t.Fatalf("notifications %v", notes)
	}
}

func TestADoctorFailureRefusesToStartAndNamesTheFix(t *testing.T) {
	env, d := fakePrograms(t, `echo "mpv 0.33.1 Copyright"`)
	h := startOverlayHost(t, pig_music.ExtensionWith(nil, nil, env, nil, d), "tui", 100, 30)
	if failure := <-h.command("music"); failure != "" {
		t.Fatal(failure)
	}
	notes := h.notifications()
	msg := fmt.Sprint(notes[0]["message"])
	if len(notes) != 1 || !strings.Contains(msg, "mpv 0.33.1 is older than 0.35") || !strings.Contains(msg, "fix:") {
		t.Fatalf("notifications %v", notes)
	}
}

func TestMusicDoctorReportsWithoutDownloadingAnything(t *testing.T) {
	env, d := fakePrograms(t, `echo "mpv 0.37.0 Copyright"`)
	d.Probe = func(context.Context, string, string) error { return errors.New("HTTP Error 403: Forbidden") }
	h := startOverlayHost(t, pig_music.ExtensionWith(nil, nil, env, nil, d), "tui", 100, 30)
	if failure := <-h.command("music", "doctor"); failure != "" {
		t.Fatal(failure)
	}
	var report string
	for _, n := range h.notifications() {
		if m := fmt.Sprint(n["message"]); strings.HasPrefix(m, "pig-music doctor:") {
			report = m
		}
	}
	for _, want := range []string{"ok   mpv", "yt-dlp 2026.08.19 is old or has no JS runtime", "403"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}

func TestMusicSetupAsksBeforeDownloadingAndNothingHappensOnNo(t *testing.T) {
	env := map[string]string{}
	pm, d := fakePrograms(t, `echo "mpv 0.37.0 Copyright"`)
	for k, v := range pm {
		env[k] = v
	}
	// A yt-dlp that finds no JavaScript runtime, so that setup offers Deno.
	if err := os.WriteFile(env["PIG_MUSIC_YTDLP"], []byte(`#!/bin/sh
if [ "$1" = "--version" ]; then echo 2026.08.19; exit 0; fi
echo "[debug] Optional libraries: yt_dlp_ejs-0.8.0" >&2
echo "[debug] JS runtimes: none" >&2
exit 2`), 0o755); err != nil {
		t.Fatal(err)
	}
	d.Probe = func(context.Context, string, string) error { return nil }
	h := startOverlayHost(t, pig_music.ExtensionWith(nil, nil, env, nil, d), "tui", 100, 30)
	if failure := <-h.command("music", "setup"); failure != "" {
		t.Fatal(failure)
	}
	h.mu.Lock()
	confirms := h.confirms
	h.mu.Unlock()
	if len(confirms) != 1 {
		t.Fatalf("%d confirmations: %v", len(confirms), confirms)
	}
	msg := fmt.Sprint(confirms[0]["message"])
	for _, want := range []string{"github.com/denoland/deno", "MIT", ".sha256sum", "no sudo"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the dialog lacks %q:\n%s", want, msg)
		}
	}
	var did bool
	for _, n := range h.notifications() {
		if strings.Contains(fmt.Sprint(n["message"]), "did not download Deno") {
			did = true
		}
	}
	if !did {
		t.Errorf("notifications %v", h.notifications())
	}
	if _, err := os.Stat(filepath.Join(env["HOME"], ".pig", "agent", "pig-music", "bin")); err == nil {
		t.Error("something was downloaded after a no")
	}
}
