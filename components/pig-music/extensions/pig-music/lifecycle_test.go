package pig_music_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// playFirstResult opens /music, searches, plays the first result and returns the command's result channel.
func playFirstResult(t *testing.T, h *overlayHost) <-chan string {
	t.Helper()
	done := h.command("music")
	h.waitOpen()
	h.waitSnapshot("the search screen", hasText("Press / to search"))
	for _, in := range []string{"/", "n", "i", "g", "h", "t", "\r"} {
		h.input(in)
	}
	h.waitSnapshot("results", hasText("Song 5"))
	h.input("\r")
	h.waitSnapshot("playing", hasText("Playing"))
	return done
}

func statusHas(h *overlayHost, want string) func() bool {
	return func() bool {
		s, _ := h.lastStatus()
		return strings.Contains(s, want)
	}
}

func quitSignal(srv *mpvfake.Server) <-chan struct{} {
	ch := make(chan struct{})
	go func() { srv.Wait(); close(ch) }()
	return ch
}

func exited(ch <-chan struct{}, within time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(within):
		return false
	}
}

func TestTheFooterShowsTheTrackWhileHiddenAndIsClearedWhileTheScreenIsOpen(t *testing.T) {
	h, srv := player(t)
	h.emit("session_start", map[string]any{"reason": "startup"}) // gives the extension a context for the footer
	done := playFirstResult(t, h)
	if s, _ := h.lastStatus(); s != "" {
		t.Fatalf("the footer says %q while the screen is open", s)
	}
	h.input("q")
	<-done
	waitFor(t, "the track in the footer", statusHas(h, " Song 1 - Some Artist"))
	srv.Advance(30)
	waitFor(t, "the position moving in the footer", statusHas(h, "0:30/3:20"))
	h.input("") // (no screen is open: nothing receives it)

	done = h.command("music")
	h.waitOpenN(2)
	waitFor(t, "the footer cleared as the screen opens", func() bool { s, ok := h.lastStatus(); return ok && s == "" })
	h.input("q")
	<-done
	waitFor(t, "the footer back after hiding", statusHas(h, "Song 1"))
}

func TestPausedAndControlCharactersInTheFooter(t *testing.T) {
	h, srv := player(t)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input(" ")
	h.waitSnapshot("paused", hasText("Paused"))
	h.input("q")
	<-done
	waitFor(t, "paused in the footer", statusHas(h, "|| Song 1"))
	_ = srv
	for _, s := range h.allStatuses() {
		if strings.ContainsAny(s, "\x1b\x07\n") {
			t.Errorf("control characters in the footer: %q", s)
		}
	}
}

func TestFooterStatusCanBeSwitchedOff(t *testing.T) {
	h, _, env, _ := playerEnv(t)
	settings := filepath.Join(env["HOME"], ".pig", "agent", "pig-music", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"footerStatus": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	time.Sleep(300 * time.Millisecond)
	for _, s := range h.allStatuses() {
		if s != "" {
			t.Errorf("a footer status %q although footerStatus is false", s)
		}
	}
}

func TestMusicStopEndsTheMusicAndClearsTheFooter(t *testing.T) {
	h, srv := player(t)
	h.emit("session_start", nil)
	gone := quitSignal(srv)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	waitFor(t, "the footer", statusHas(h, "Song 1"))
	if failure := <-h.command("music", "stop"); failure != "" {
		t.Fatal(failure)
	}
	if !exited(gone, 5*time.Second) {
		t.Fatal("mpv was not told to quit")
	}
	waitFor(t, "the footer cleared", func() bool { s, _ := h.lastStatus(); return s == "" })
	if !strings.Contains(lastNotice(h), "stopped") {
		t.Errorf("notice %q", lastNotice(h))
	}
}

func TestMusicStopWithNothingPlayingSaysSoAndStartsNothing(t *testing.T) {
	h, srv, _, _ := playerEnv(t)
	srv.Close() // no mpv answers on the socket
	if failure := <-h.command("music", "stop"); failure != "" {
		t.Fatal(failure)
	}
	if !strings.Contains(lastNotice(h), "nothing is playing") {
		t.Errorf("notice %q", lastNotice(h))
	}
}

func TestSessionShutdownStopsTheMusicOnQuitOnly(t *testing.T) {
	h, srv := player(t)
	h.emit("session_start", nil)
	gone := quitSignal(srv)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	for _, reason := range []string{"reload", "new", "resume", "fork"} {
		if failure := <-h.emit("session_shutdown", map[string]any{"reason": reason}); failure != "" {
			t.Fatal(failure)
		}
	}
	if exited(gone, 400*time.Millisecond) {
		t.Fatal("the music stopped on a reload or a session switch")
	}
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if !exited(gone, 5*time.Second) {
		t.Fatal("quit did not stop the music although stopOnExit defaults to true")
	}
}

func TestStopOnExitFalseLeavesTheMusicPlayingWhenPiGQuits(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	settings := filepath.Join(env["HOME"], ".pig", "agent", "pig-music", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"stopOnExit": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.emit("session_start", nil)
	gone := quitSignal(srv)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if exited(gone, 600*time.Millisecond) {
		t.Fatal("stopOnExit is false but the music stopped")
	}
}

// After a /reload the extension is a new generation (in pig 0.4.0 the same process, a new app): it finds the mpv that kept playing, shows it in the footer without a
// /music, and /music shows the same queue.
func TestAfterAReloadTheRunningMusicIsFoundAndShownInTheFooter(t *testing.T) {
	h, srv, _, p := playerEnv(t)
	if err := p.Attach(ctxBackground()); err != nil {
		t.Fatal(err)
	}
	if err := p.Replace(ctxBackground(), songs(3), 1); err != nil {
		t.Fatal(err)
	}
	srv.Advance(75)
	h.emit("session_start", map[string]any{"reason": "reload"})
	waitFor(t, "the footer after the reload", statusHas(h, "Song 2"))
	done := h.command("music")
	h.waitOpen()
	text := screenText(h.waitSnapshot("the queue", hasText("Up Next")))
	for _, want := range []string{"Song 1", "Song 2", "Song 3"} {
		if !strings.Contains(text, want) {
			t.Errorf("queue lacks %q:\n%s", want, text)
		}
	}
	h.input("q")
	<-done
	_ = music.State{}
}

func TestTheShortcutOpensThePlayerLikeTheCommand(t *testing.T) {
	h, _ := player(t)
	done := h.shortcut("alt+m")
	h.waitOpen()
	h.waitSnapshot("the search screen", hasText("Press / to search"))
	h.input("q")
	if failure := <-done; failure != "" {
		t.Fatal(failure)
	}
}

// The owner left the screen on a Library tab showing an error, hid it, and /music opened on that error: it looked like there
// was no music option. The error stays on the Library tab; /music opens on Search.
func TestReopeningAfterALibraryErrorShowsSearchAndKeepsTheErrorOnTheLibraryTab(t *testing.T) {
	h, _ := player(t)
	done := h.command("music")
	h.waitOpen()
	h.waitSnapshot("search", hasText("Press / to search"))
	h.input("\t")
	h.waitSnapshot("the library error", hasText("needs cookies"))
	h.input("q")
	<-done
	hidden := len(h.allSnapshots()) // the snapshots of the first opening; a second opening numbers its own from 1
	done = h.command("music")
	h.waitOpenN(2)
	var reopened snapshot
	waitFor(t, "the reopened screen", func() bool {
		snaps := h.allSnapshots()
		if len(snaps) <= hidden {
			return false
		}
		reopened = snaps[len(snaps)-1]
		return strings.Contains(screenText(reopened), "Press / to search")
	})
	text := screenText(reopened)
	if strings.Contains(text, "needs cookies") || !strings.Contains(text, "type a song or artist") {
		t.Errorf("the reopened screen:\n%s", text)
	}
	h.input("\t")
	h.waitSnapshot("the error is still on the Library tab", hasText("needs cookies"))
	h.input("q")
	<-done
}

func TestTheSettingsScreenSavesToTheFileKeepsOtherKeysAndAppliesTheFooterAtOnce(t *testing.T) {
	h, _, env, _ := playerEnv(t)
	settings := filepath.Join(env["HOME"], ".pig", "agent", "pig-music", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"cookieBrowser":"firefox","unknownKey":[1,2,3]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	waitFor(t, "the footer", statusHas(h, "Song 1"))

	done = h.command("music")
	h.waitOpenN(2)
	h.input("o")
	h.waitSnapshot("the settings screen", hasText("[x] Show the track in PiG's footer"))
	h.input("j")
	h.input(" ") // footerStatus off
	waitFor(t, "the file", func() bool {
		b, _ := os.ReadFile(settings)
		return strings.Contains(string(b), `"footerStatus": false`)
	})
	b, _ := os.ReadFile(settings)
	for _, keep := range []string{`"cookieBrowser": "firefox"`, `"unknownKey"`} {
		if !strings.Contains(string(b), keep) {
			t.Errorf("the file lost %q:\n%s", keep, b)
		}
	}
	h.input("\x1b") // esc closes the settings, then q hides the player
	time.Sleep(100 * time.Millisecond)
	h.input("q")
	<-done
	time.Sleep(300 * time.Millisecond)
	if s, _ := h.lastStatus(); s != "" {
		t.Errorf("the footer says %q although it was switched off", s)
	}
}
