package pig_music_test

import (
	"strings"
	"testing"
	"time"
)

// sayOrClose runs `/music <words>`; when it opens the overlay instead of answering, the overlay is closed again (so the test
// fails instead of hanging) and opened is true.
func sayOrClose(t *testing.T, h *overlayHost, words ...string) (out string, opened bool) {
	t.Helper()
	h.mu.Lock()
	before, notes := len(h.opens), len(h.notices)
	h.mu.Unlock()
	done := h.command("music", words...)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			waitFor(t, "a notification", func() bool { return notices(h) > notes })
			return lastNotice(h), false
		default:
		}
		h.mu.Lock()
		n := len(h.opens)
		h.mu.Unlock()
		if n > before {
			time.Sleep(300 * time.Millisecond) // let the screen take input before it is closed
			h.input("q")
			<-done
			return "", true
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("/music %v neither answered nor opened", words)
	return "", false
}

// rev-pig-music-m7: any word after /music that was not a known subcommand opened the full-screen player, so a typo of a quick
// command (/music pasue) or the skipped /music like took over the terminal: the opposite of "never opens the overlay". Such a
// word now gets one line naming what exists, and /music like says why it is not offered.
func TestUnknownSubcommandsAnswerInOneLineAndNeverOpenThePlayer(t *testing.T) {
	h, _, _, _ := playerEnv(t)
	for _, words := range [][]string{{"pasue"}, {"like"}, {"Pause"}, {"stop", "now"}, {"whatever", "else"}} {
		out, opened := sayOrClose(t, h, words...)
		if opened {
			t.Errorf("/music %v opened the player", words)
			continue
		}
		if out == "" || strings.Contains(out, "\n") || !strings.Contains(out, "pause") {
			t.Errorf("/music %v: %q", words, out)
		}
	}
	if out, _ := sayOrClose(t, h, "like"); !strings.Contains(out, "account") {
		t.Errorf("/music like should say why it is not offered: %q", out)
	}
	// /music alone still opens the player
	h.mu.Lock()
	n := len(h.opens)
	h.mu.Unlock()
	done := h.command("music")
	h.waitOpenN(n + 1)
	time.Sleep(300 * time.Millisecond)
	h.input("q")
	<-done
}

// The quick commands' hints name /music, not the command line.
func TestQuickCommandHintsNameSlashMusic(t *testing.T) {
	h, _, _, _ := playerEnv(t)
	out := say(t, h, "play", "2") // no search yet
	if strings.Contains(out, "pigmusic") || !strings.Contains(out, "/music") {
		t.Errorf("%q", out)
	}
}

// The owner's check for M7a/M7b: while the music is paused the widget is never written again (no redraws), and it moves on
// again when the music resumes.
func TestAPausedWidgetIsNeverRewritten(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	writeSettings(t, env, `{"nowPlaying": "widget"}`)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input(" ")
	h.waitSnapshot("paused", hasText("Paused"))
	h.input("q")
	<-done
	waitFor(t, "paused in the widget", widgetHas(h, "|| Song 1"))
	before := h.widgetCount()
	for i := 0; i < 5; i++ {
		srv.Advance(0) // mpv keeps talking while paused; nothing new to show
		time.Sleep(100 * time.Millisecond)
	}
	if n := h.widgetCount() - before; n != 0 {
		t.Errorf("%d widget writes while paused", n)
	}
	say(t, h, "resume")
	srv.Advance(3)
	waitFor(t, "the widget moving again", widgetHas(h, "0:03/"))
}

// Surviving mutant: /music play claiming the music was untested. The PiG whose /music play started the music is the one whose
// quit stops it (stopOnExit); the PiG that opened the player before no longer owns it.
func TestSlashMusicPlayMakesThisPiGTheOwnerOfTheMusic(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	h.emit("session_start", nil)
	done := playFirstResult(t, h) // this PiG opened the player: it owns the music
	h.input("q")
	<-done
	gone := quitSignal(srv)

	b, _ := another(t, env, "a second interactive pig", "tui")
	<-b.emit("session_start", map[string]any{"reason": "startup"})
	if out := say(t, b, "play", "night"); !strings.Contains(out, "playing") {
		t.Fatalf("play: %q", out)
	}
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if exited(gone, 600*time.Millisecond) {
		t.Fatal("the first PiG stopped music that the second one's /music play now owns")
	}
	if failure := <-b.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if !exited(gone, 5*time.Second) {
		t.Fatal("the PiG whose /music play started the music did not stop it on quit")
	}
}

// The /music player always drives mpv (the engine setting is wired into pigmusic only), but the quick commands ran the
// command line's engine choice: with "engine": "native" (offered by /music settings) /music play went to a native player that
// the overlay, the footer and stopOnExit never see, or failed where the player plays. Inside PiG the quick commands use the
// player's engine.
func TestQuickCommandsUseTheSameEngineAsThePlayer(t *testing.T) {
	h, _, env, p := playerEnv(t)
	writeSettings(t, env, `{"engine": "native"}`)
	if out := say(t, h, "play", "night"); !strings.Contains(out, "playing") {
		t.Fatalf("play with engine native: %q", out)
	}
	waitFor(t, "the player's mpv playing", func() bool { st := p.State(); return st.Track != nil })
}

// /music settings must not promise that the engine takes effect after /reload: the /music player does not use it yet.
func TestTheEngineRowSaysWhatItChanges(t *testing.T) {
	h, _, _, _ := playerEnv(t)
	done := h.command("music", "settings")
	h.waitOpen()
	h.waitSnapshot("the quick settings", hasText("quick settings"))
	for i := 0; i < 3; i++ {
		h.input("j") // down to Engine
	}
	h.input("l")
	h.waitSnapshot("the engine note", hasText("pigmusic only"))
	h.input("q")
	<-done
}
