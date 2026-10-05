package pig_music_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	pig_music "github.com/MichaelKinsy/pigpen/pig-music"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// playerNoSpawn is player() with an mpv that can never be started, so a test
// that makes the fake mpv go away cannot start a real one.
func playerNoSpawn(t *testing.T) (*overlayHost, *mpv.Player, music.Paths, *mpvfake.Server) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	paths := music.PathsIn(dir)
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	src := fixedSource{songs(5)}
	p := mpv.New(mpv.Config{
		Paths: paths, PlayURL: src.PlayURL, MPVPath: "mpv-that-is-not-there",
		LookPath: func(string) (string, error) { return "", errors.New("not installed") },
	})
	t.Cleanup(func() { _ = p.Close() })
	return startOverlayHost(t, pig_music.ExtensionWith(src, p, nil, nil, nil), "tui", 100, 30), p, paths, srv
}

func hideAndWaitGone(t *testing.T, h *overlayHost, done <-chan string, p *mpv.Player, srv *mpvfake.Server) {
	t.Helper()
	h.input("q")
	if failure := <-done; failure != "" {
		t.Fatal(failure)
	}
	srv.Close() // mpv goes away while the player is hidden: pigmusic stop, a crash, a kill
	waitFor(t, "the player to notice", func() bool { return !p.State().Connected })
}

// The extension holds its player for the life of the process. When mpv went away
// meanwhile, /music must attach again (here to the mpv now on the socket) instead
// of showing the old screen, on which every key says "not attached to mpv".
func TestMusicAttachesAgainAfterMpvWentAway(t *testing.T) {
	h, p, paths, srv := playerNoSpawn(t)
	done := h.command("music")
	h.waitOpen()
	h.waitSnapshot("the search screen", hasText("Press / to search"))
	hideAndWaitGone(t, h, done, p, srv)

	srv2, err := mpvfake.Listen(paths.Socket, mpvfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv2.Close)
	done = h.command("music")
	h.waitOpenN(2)
	h.input("-")
	h.waitSnapshot("the volume key to reach the new mpv", hasText("vol 95%"))
	if cmds := srv2.Commands(); !strings.Contains(strings.Join(cmds, "\n"), "volume") {
		t.Fatalf("the new mpv got %v", cmds)
	}
	h.input("q")
	<-done
}

// When mpv went away and cannot be started again, /music says why, as on the first use.
func TestMusicSaysSoWhenMpvWentAwayAndCannotStartAgain(t *testing.T) {
	h, p, _, srv := playerNoSpawn(t)
	done := h.command("music")
	h.waitOpen()
	h.waitSnapshot("the search screen", hasText("Press / to search"))
	hideAndWaitGone(t, h, done, p, srv)

	done = h.command("music")
	select {
	case failure := <-done:
		if failure != "" {
			t.Fatal(failure)
		}
	case <-time.After(5 * time.Second):
		h.input("q") // it opened the old screen instead; close it so the test ends
		<-done
	}
	notes := h.notifications()
	if len(notes) != 1 || !strings.Contains(fmt.Sprint(notes[0]["message"]), "mpv") {
		t.Fatalf("notifications %v", notes)
	}
	h.mu.Lock()
	opened := len(h.opens)
	h.mu.Unlock()
	if opened != 1 {
		t.Fatalf("the stale player opened again (%d opens)", opened)
	}
}

// Keys reach the search box as typed, spaces and all, through PiG's raw input.
func TestASearchWithSpacesIsTypedThroughRawInput(t *testing.T) {
	h, _ := player(t)
	done := h.command("music")
	h.waitOpen()
	h.waitSnapshot("the search screen", hasText("Press / to search"))
	h.input("/")
	for _, c := range "daft punk" {
		h.input(string(c))
	}
	h.waitSnapshot("the query", hasText("/ daft punk"))
	h.input("\x1b")
	h.input("\x1b")
	<-done
}

// The player screen re-lays out on a height change alone, with no state change
// and no input to trigger a frame: PiG does not push height changes to the SDK.
func TestAHeightChangeAloneRelaysOutThePlayer(t *testing.T) {
	h, _ := player(t)
	done := h.command("music")
	h.waitOpen()
	h.waitSnapshot("search screen", func(s snapshot) bool { return len(s.Lines) == 30 })
	time.Sleep(100 * time.Millisecond)
	h.resize(0, 18)
	h.waitSnapshot("height 18", func(s snapshot) bool { return len(s.Lines) == 18 })
	h.input("q")
	<-done
}
