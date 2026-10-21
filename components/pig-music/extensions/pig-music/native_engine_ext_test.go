package pig_music_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pig_music "github.com/MichaelKinsy/pigpen/pig-music"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

// The /music player and its quick commands honor engine "native": the player runs as `pigmusic serve`, a daemon on a local
// socket that the extension attaches to (the engine itself is linked into the pigmusic program only). These tests run that
// daemon in this process, with the synthetic opener and the paced null output, so nothing needs a network or a sound card.

type nativeRig struct {
	h     *overlayHost
	env   map[string]string
	paths music.Paths
	done  chan error
}

func nativeLookPath(name string) (string, error) {
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return "", err
		}
		return name, nil
	}
	return "", os.ErrNotExist // no mpv, no yt-dlp on this machine
}

// nativeEnv starts the daemon and the extension over it. mpv is absent on purpose: a test that reached for it would fail.
func nativeEnv(t *testing.T, extra map[string]string) *nativeRig {
	t.Helper()
	dir, err := os.MkdirTemp("", "pmn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	env := map[string]string{"XDG_RUNTIME_DIR": dir, "HOME": dir, "PIG_MUSIC_ENGINE": "native", "PIG_MUSIC_SERVE": os.Args[0]}
	for k, v := range extra {
		env[k] = v
	}
	paths, err := music.DefaultPaths(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	r := &nativeRig{env: env, paths: paths, done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		r.done <- native.RunDaemon(ctx, native.DaemonConfig{Paths: paths, Opener: native.SyntheticOpener{}, Output: native.NewNullOutput(), Tick: 20 * time.Millisecond})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
		}
	})
	waitFor(t, "the native daemon", func() bool { return native.Running(context.Background(), paths) })
	r.h = startHost(t, pig_music.ExtensionWith(fixedSource{songs(5)}, nil, env, nativeLookPath, nil), "tui", 100, 30, false)
	return r
}

func (r *nativeRig) client(t *testing.T) *native.Player {
	t.Helper()
	p := native.NewPlayer(native.ClientConfig{Paths: r.paths})
	if err := p.Attach(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func (r *nativeRig) stopped(within time.Duration) bool {
	select {
	case <-r.done:
		return true
	case <-time.After(within):
		return false
	}
}

func TestNativeEngineQuickCommandsDriveTheNativePlayer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the daemon's socket is a named pipe there; covered by the native package's own tests")
	}
	r := nativeEnv(t, nil)
	h := r.h
	if out := say(t, h, "play", "night"); !strings.Contains(out, "playing") || !strings.Contains(out, "Song 1") || strings.Contains(out, "\n") {
		t.Fatalf("play: %q", out)
	}
	c := r.client(t)
	waitFor(t, "five tracks queued in the native player", func() bool { st := c.State(); return len(st.Queue) == 5 && st.Index == 0 })
	if out := say(t, h, "pause"); !strings.Contains(out, "paused") {
		t.Errorf("pause: %q", out)
	}
	waitFor(t, "the native player paused", func() bool { return c.State().Paused })
	if out := say(t, h, "next"); !strings.Contains(out, "Song 2") {
		t.Errorf("next: %q", out)
	}
	say(t, h, "vol", "30")
	waitFor(t, "the native player at 30", func() bool { return c.State().Volume == 30 })
	if out := say(t, h, "now"); !strings.Contains(out, "Some Artist - Song 2") {
		t.Errorf("now: %q", out)
	}
}

func TestNativeEngineFromTheSettingsFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_ENGINE": ""})
	writeSettings(t, r.env, `{"engine": "native"}`)
	if out := say(t, r.h, "play", "night"); !strings.Contains(out, "Song 1") {
		t.Fatalf("play: %q", out)
	}
	waitFor(t, "the native player queue", func() bool { return len(r.client(t).State().Queue) == 5 })
}

func TestNativeEngineDrivesThePlayerScreen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, nil)
	h := r.h
	done := playFirstResult(t, h)
	c := r.client(t)
	waitFor(t, "the queue in the native player", func() bool { return len(c.State().Queue) == 5 })
	h.input(" ")
	h.waitSnapshot("paused", hasText("Paused"))
	waitFor(t, "the native player paused by the space key", func() bool { return c.State().Paused })
	h.input("q")
	<-done
}

func TestNativeEngineShowsTheTrackInTheFooterAndStopEndsTheDaemonsMusic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, nil)
	h := r.h
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	waitFor(t, "the track in the footer", statusHas(h, " Song 1 - Some Artist"))
	if failure := <-h.command("music", "stop"); failure != "" {
		t.Fatal(failure)
	}
	if !r.stopped(5 * time.Second) {
		t.Fatal("/music stop did not stop the native player")
	}
	waitFor(t, "the footer cleared", func() bool { s, _ := h.lastStatus(); return s == "" })
	if !strings.Contains(lastNotice(h), "stopped") {
		t.Errorf("notice %q", lastNotice(h))
	}
}

func TestNativeEngineQuitStopsTheMusicThisPiGOwns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, nil)
	h := r.h
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "reload"}); failure != "" {
		t.Fatal(failure)
	}
	if r.stopped(400 * time.Millisecond) {
		t.Fatal("the native player stopped on a reload")
	}
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if !r.stopped(5 * time.Second) {
		t.Fatal("quit did not stop the native player although stopOnExit defaults to true")
	}
}

func TestNativeMusicThatKeptPlayingIsFoundAfterAReload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, nil)
	c := r.client(t)
	if err := c.Replace(context.Background(), songs(3), 1); err != nil {
		t.Fatal(err)
	}
	r.h.emit("session_start", map[string]any{"reason": "reload"})
	waitFor(t, "the footer after the reload", statusHas(r.h, "Song 2"))
}

// With engine auto and neither mpv nor yt-dlp installed, the native player is used when the pigmusic program is there; and a
// native player that is already running is the one a quick command controls.
func TestAutoEngineFallsBackToTheNativePlayerWhenMPVIsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_ENGINE": "auto"})
	if out := say(t, r.h, "play", "night"); !strings.Contains(out, "Song 1") {
		t.Fatalf("play: %q", out)
	}
	waitFor(t, "five tracks in the native player", func() bool { return len(r.client(t).State().Queue) == 5 })
}

func TestNativeEngineWithoutThePigmusicProgramSaysSoInsteadOfUsingMPV(t *testing.T) {
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_SERVE": filepath.Join(t.TempDir(), "no-such-pigmusic")})
	out := say(t, r.h, "play", "night")
	if !strings.Contains(out, "pigmusic") || strings.Contains(out, "mpv: ") || strings.Contains(out, "does not support") {
		t.Fatalf("play without the program: %q", out)
	}
	h := r.h
	done := h.command("music")
	waitFor(t, "a notice", func() bool { return strings.Contains(lastNotice(h), "pigmusic") })
	<-done
}

func TestNativeEngineIsNotRefusedAnyMore(t *testing.T) {
	r := nativeEnv(t, nil)
	out := say(t, r.h, "now")
	if strings.Contains(out, "does not support") {
		t.Fatalf("the native engine is still refused: %q", out)
	}
}

// /music doctor shows the native engine's health: the platform, the audio output, the player program and, when the engine is
// native, the stream probe the pigmusic program runs.
func TestDoctorShowsTheNativeEnginesHealth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake pigmusic is a shell script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "pigmusic")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"version\":1,\"check\":{\"Name\":\"stream probe\",\"OK\":true,\"Detail\":\"resolved and read 64 KiB\"}}'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_SERVE": fake})
	h := r.h
	if failure := <-h.command("music", "doctor"); failure != "" {
		t.Fatal(failure)
	}
	var text string
	waitFor(t, "the native health", func() bool {
		for _, n := range h.notifications() {
			if m, _ := n["message"].(string); strings.Contains(m, "doctor, native engine") {
				text = m
				return true
			}
		}
		return false
	})
	for _, want := range []string{"audio output", "player program: pigmusic serve is available", "stream probe: resolved and read 64 KiB"} {
		if !strings.Contains(text, want) {
			t.Errorf("the native health lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "yt-dlp") {
		t.Errorf("the native engine does not use yt-dlp:\n%s", text)
	}
}

func TestDoctorOnTheMPVEngineStillShowsTheNativeEnginesHealthAsInformation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_ENGINE": "mpv"})
	if failure := <-r.h.command("music", "doctor"); failure != "" {
		t.Fatal(failure)
	}
	var text string
	waitFor(t, "the native health", func() bool {
		for _, n := range r.h.notifications() {
			if m, _ := n["message"].(string); strings.Contains(m, "doctor, native engine") {
				text = m
				return true
			}
		}
		return false
	})
	if !strings.Contains(text, "audio output") || strings.Contains(text, "stream probe") {
		t.Errorf("on the mpv engine the native health is shown without its network probe:\n%s", text)
	}
}

// listenMPV starts a fake mpv on the rig's mpv socket, as a second player that is already running.
func (r *nativeRig) listenMPV(t *testing.T) *mpvfake.Server {
	t.Helper()
	srv, err := mpvfake.Listen(r.paths.Socket, mpvfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// mpvPresent is a machine where mpv and yt-dlp are installed (any name resolves), so that auto has a healthy mpv to prefer.
func mpvPresent(name string) (string, error) { return os.Args[0], nil }

// With engine auto, a native player that is already running is the one to control even when mpv is healthy: the footer follows it.
func TestAutoEngineControlsARunningNativePlayerEvenWhenMPVIsHealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_ENGINE": "auto"})
	r.h = startHost(t, pig_music.ExtensionWith(fixedSource{songs(5)}, nil, r.env, mpvPresent, nil), "tui", 100, 30, false)
	r.h.emit("session_start", nil)
	say(t, r.h, "play", "night")
	waitFor(t, "the native player's track in the footer", statusHas(r.h, " Song 1 - Some Artist"))
	if st := r.client(t).State(); len(st.Queue) != 5 {
		t.Fatalf("the native player has %d tracks", len(st.Queue))
	}
}

// A running mpv comes first, ahead of a running native player: the footer follows mpv.
func TestAutoEngineControlsARunningMPVAheadOfARunningNativePlayer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_ENGINE": "auto"})
	srv := r.listenMPV(t)
	r.h = startHost(t, pig_music.ExtensionWith(fixedSource{songs(5)}, nil, r.env, mpvPresent, nil), "tui", 100, 30, false)
	say(t, r.h, "play", "night")
	waitFor(t, "the mpv's track in the footer", statusHas(r.h, " Song 1 - Some Artist"))
	if urls, _ := srv.Playlist(); len(urls) != 5 {
		t.Fatalf("mpv has %d entries", len(urls))
	}
	if st := r.client(t).State(); len(st.Queue) != 0 {
		t.Fatalf("the native player was given %d tracks", len(st.Queue))
	}
}

// Changing the engine in settings.json while the player is held moves the footer to the new engine's player at the next command.
func TestSwitchingTheEngineInTheSettingsMovesThePlayerFromMPVToNative(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_ENGINE": ""})
	r.listenMPV(t)
	writeSettings(t, r.env, `{"engine": "mpv"}`)
	r.h = startHost(t, pig_music.ExtensionWith(fixedSource{songs(5)}, nil, r.env, mpvPresent, nil), "tui", 100, 30, false)
	<-r.h.emit("session_start", nil)
	time.Sleep(300 * time.Millisecond) // session_start attaches in the background
	writeSettings(t, r.env, `{"engine": "native"}`)
	say(t, r.h, "play", "night")
	waitFor(t, "the native player's track in the footer", statusHas(r.h, " Song 1 - Some Artist"))
}

func TestSwitchingTheEngineInTheSettingsMovesThePlayerFromNativeToMPV(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_ENGINE": ""})
	writeSettings(t, r.env, `{"engine": "native"}`)
	r.h = startHost(t, pig_music.ExtensionWith(fixedSource{songs(5)}, nil, r.env, mpvPresent, nil), "tui", 100, 30, false)
	<-r.h.emit("session_start", nil) // the native daemon is running: the footer attaches to it
	time.Sleep(300 * time.Millisecond)
	srv := r.listenMPV(t)
	writeSettings(t, r.env, `{"engine": "mpv"}`)
	say(t, r.h, "play", "night")
	waitFor(t, "the mpv's track in the footer", statusHas(r.h, " Song 1 - Some Artist"))
	if urls, _ := srv.Playlist(); len(urls) != 5 {
		t.Fatalf("mpv has %d entries", len(urls))
	}
}

// Termux has mpv only: a native player that happens to be running there is not used, and the player says what is missing.
func TestTermuxIsMPVOnlyEvenWithANativePlayerRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_ENGINE": "auto", "TERMUX_VERSION": "0.118"})
	h := r.h
	<-h.command("music")
	h.mu.Lock()
	opened := len(h.opens)
	h.mu.Unlock()
	if opened != 0 || lastNotice(h) == "" {
		t.Fatalf("the player opened %d times on Termux with no mpv, notice %q", opened, lastNotice(h))
	}
}

func notice(h *overlayHost, contains string) (map[string]any, bool) {
	for _, n := range h.notifications() {
		if m, _ := n["message"].(string); strings.Contains(m, contains) {
			return n, true
		}
	}
	return nil, false
}

func TestDoctorSaysErrorWhenTheNativeEngineInUseIsBrokenAndWarningWhenItIsOnlyTheFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see above")
	}
	missing := filepath.Join(t.TempDir(), "no-such-pigmusic")
	r := nativeEnv(t, map[string]string{"PIG_MUSIC_SERVE": missing})
	<-r.h.command("music", "doctor")
	waitFor(t, "the native health", func() bool { _, ok := notice(r.h, "doctor, native engine"); return ok })
	if n, _ := notice(r.h, "doctor, native engine"); n["level"] != "error" {
		t.Errorf("a broken native engine that is the engine in use: level %v", n["level"])
	}
	r = nativeEnv(t, map[string]string{"PIG_MUSIC_SERVE": missing, "PIG_MUSIC_ENGINE": "mpv"})
	<-r.h.command("music", "doctor")
	waitFor(t, "the native health", func() bool { _, ok := notice(r.h, "doctor, native engine"); return ok })
	if n, _ := notice(r.h, "doctor, native engine"); n["level"] != "warning" {
		t.Errorf("a native engine that is only the fallback: level %v", n["level"])
	}
}
