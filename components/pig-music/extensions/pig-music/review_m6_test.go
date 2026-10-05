package pig_music_test

import (
	"strings"
	"testing"
	"time"

	pig_music "github.com/MichaelKinsy/pigpen/pig-music"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// rev-pig-music-m6: stopOnExit belongs to the PiG that plays the music, and a /reload leaves nothing of the old
// generation behind.

func pathsOf(t *testing.T, env map[string]string) music.Paths {
	t.Helper()
	paths, err := music.DefaultPaths(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// another starts the extension again over the same mpv socket: a /reload of this PiG (same token) or another PiG process.
func another(t *testing.T, env map[string]string, token, mode string) (*overlayHost, *mpv.Player) {
	t.Helper()
	src := fixedSource{songs(5)}
	p := mpv.New(mpv.Config{Paths: pathsOf(t, env), PlayURL: src.PlayURL})
	t.Cleanup(func() { _ = p.Close() })
	return startOverlayHost(t, pig_music.ExtensionAs(token, src, p, env), mode, 100, 30), p
}

// `pig -p "..."`, `pig --mode json` and RPC runs load the extension too and end with session_shutdown "quit". Seen with
// real pig 0.4.0: `pig -p hello` in a second shell stopped the music the interactive PiG was playing.
func TestAPrintModeRunOfPiGDoesNotStopTheMusic(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	h.emit("session_start", map[string]any{"reason": "startup"})
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	gone := quitSignal(srv)

	b, _ := another(t, env, "a pig -p process", "print")
	<-b.emit("session_start", map[string]any{"reason": "startup"})
	if failure := <-b.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if exited(gone, 600*time.Millisecond) {
		t.Fatal("a print-mode PiG stopped the music on exit")
	}
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if !exited(gone, 5*time.Second) {
		t.Fatal("the PiG that plays the music did not stop it on quit")
	}
}

// Two interactive PiGs: the second shows the track in its footer, but quitting it must not end the first one's music.
func TestAnotherPiGThatNeverOpenedThePlayerLeavesTheMusicPlayingWhenItQuits(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	gone := quitSignal(srv)

	b, _ := another(t, env, "another interactive pig", "tui")
	<-b.emit("session_start", map[string]any{"reason": "startup"})
	waitFor(t, "the second PiG's footer", statusHas(b, "Song 1"))
	if failure := <-b.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if exited(gone, 600*time.Millisecond) {
		t.Fatal("a PiG that never opened the player stopped the music on quit")
	}
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if !exited(gone, 5*time.Second) {
		t.Fatal("the PiG that plays the music did not stop it on quit")
	}
}

// The PiG that opened the player last owns the music: after the second one opens it, quitting the first leaves it.
func TestThePiGThatOpenedThePlayerLastIsTheOneWhoseQuitStopsIt(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	gone := quitSignal(srv)

	b, _ := another(t, env, "another interactive pig", "tui")
	<-b.emit("session_start", nil)
	done = b.command("music")
	b.waitOpen()
	b.waitSnapshot("the queue", hasText("Up Next"))
	b.input("q")
	<-done
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if exited(gone, 600*time.Millisecond) {
		t.Fatal("the first PiG stopped music the second one had taken over")
	}
	if failure := <-b.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if !exited(gone, 5*time.Second) {
		t.Fatal("the PiG that opened the player last did not stop it on quit")
	}
}

// pig 0.4.0 runs a /reload as a new generation of the extension in the same process; quitting after it still stops the music.
func TestQuitAfterAReloadStillStopsTheMusicThisPiGPlays(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	gone := quitSignal(srv)
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "reload"}); failure != "" {
		t.Fatal(failure)
	}
	next, _ := another(t, env, "", "tui") // "" is this process's own token
	<-next.emit("session_start", map[string]any{"reason": "reload"})
	waitFor(t, "the footer after the reload", statusHas(next, "Song 1"))
	if exited(gone, 300*time.Millisecond) {
		t.Fatal("the reload stopped the music")
	}
	if failure := <-next.emit("session_shutdown", map[string]any{"reason": "quit"}); failure != "" {
		t.Fatal(failure)
	}
	if !exited(gone, 5*time.Second) {
		t.Fatal("quit after a reload did not stop the music")
	}
}

// Seen with real pig 0.4.0: the extension process (the Go cell runner) stays across /reload and gains one mpv connection
// per reload. The generation that ends must drop its connection, its model and its footer.
func TestAReloadReleasesTheEndingGenerationsConnectionAndFooter(t *testing.T) {
	h, srv, _, p := playerEnv(t)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	waitFor(t, "the footer", statusHas(h, "Song 1"))
	if failure := <-h.emit("session_shutdown", map[string]any{"reason": "reload"}); failure != "" {
		t.Fatal(failure)
	}
	waitFor(t, "the old generation's mpv connection closed", func() bool { return !p.State().Connected })
	before := len(h.allStatuses())
	srv.Advance(40)
	time.Sleep(300 * time.Millisecond)
	if after := h.allStatuses(); len(after) != before {
		t.Errorf("the ended generation still writes the footer: %q", strings.Join(after[before:], " | "))
	}
}

// A print, JSON or RPC run has no footer and no player screen: its session_start attaches to nothing (a mutant that
// attached survived the lane's tests).
func TestAPrintModeRunAttachesToNothing(t *testing.T) {
	h, _, env, _ := playerEnv(t)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	b, pb := another(t, env, "a pig -p process", "print")
	<-b.emit("session_start", map[string]any{"reason": "startup"})
	time.Sleep(400 * time.Millisecond)
	if pb.State().Connected {
		t.Error("a print-mode run attached to the running mpv")
	}
}
