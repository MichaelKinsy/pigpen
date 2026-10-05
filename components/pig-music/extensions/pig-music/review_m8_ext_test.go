package pig_music_test

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Review of M8: mpv outlives the extension. A pig-music that ended while it was measuring (killed, crashed, PiG closed with
// the player on screen) leaves its astats filter in mpv, where it keeps running for as long as the music plays. The next
// pig-music to find that mpv takes it away before it does anything else.
func TestAMeasuringFilterLeftInMpvByAnEndedExtensionIsRemoved(t *testing.T) {
	h, srv, env, _ := playerEnvWith(t, map[string]string{"COLORTERM": "truecolor"})
	paths, err := music.DefaultPaths(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ended := mpv.New(mpv.Config{Paths: paths})
	if err := ended.Attach(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ended.SetLevels(ctx, true); err != nil {
		t.Fatal(err)
	}
	_ = ended.Close() // the extension that was measuring is gone; its filter is not
	if len(srv.Filters()) != 1 {
		t.Fatalf("set-up: filters %v", srv.Filters())
	}
	done := h.command("music")
	h.waitOpen()
	h.waitSnapshot("the player", hasText("Press / to search"))
	waitFor(t, "the left-over filter removed", func() bool { return len(srv.Filters()) == 0 })
	h.input("q")
	<-done
}

// Calm is "no motion" (the owner's word): the status line's record spinner is motion too, so with calm it stands still.
func TestCalmStopsTheStatusLineSpinner(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	writeSettings(t, env, `{"calm": true}`)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	for i := 1; i <= 4; i++ {
		srv.Advance(1)
		want := "0:0" + string(rune('0'+i)) + "/"
		waitFor(t, "the position "+want, func() bool { s, _ := h.lastStatus(); return strings.Contains(s, want) })
	}
	marks := map[string]bool{}
	for _, s := range h.allStatuses() {
		if mark, _, ok := strings.Cut(s, " Song 1"); ok {
			marks[mark] = true
		}
	}
	if len(marks) != 1 {
		t.Errorf("with calm the status line still spins: marks %v", marks)
	}
}

// NO_COLOR (no-color.org: "prevents the addition of ANSI color") and TERM=dumb: the player writes no colour at all, not
// even the accent of the title, the progress bar and the disc, which predates the palette.
func TestNoColourMeansNoColourEscapeAnywhereOnThePlayer(t *testing.T) {
	for _, env := range []map[string]string{{"NO_COLOR": "1", "COLORTERM": "truecolor"}, {"TERM": "dumb"}} {
		h, _, _, _ := playerEnvWith(t, env)
		done := playFirstResult(t, h)
		snap := h.waitSnapshot("the player screen", hasText("Up Next"))
		h.input("q")
		<-done
		for i, l := range snap.Lines {
			for _, m := range sgrParams.FindAllStringSubmatch(l, -1) {
				for _, p := range strings.Split(m[1], ";") {
					if n, err := strconv.Atoi(p); err == nil && (n >= 30 && n <= 49 || n >= 90 && n <= 107) {
						t.Fatalf("%v: row %d has the colour escape %q", env, i, m[0])
					}
				}
			}
		}
	}
}

var sgrParams = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// After a /reload or a restart with music playing, the player is built in the background for the footer, with no screen on
// the terminal. Nothing may animate or measure until the player is opened: seen in a real pig 0.4.0 Binary, the astats
// filter was added at session start and the model ticked at 15 frames a second while hidden (4.5% of a core against 1.8%).
func TestAPlayerBuiltForTheFooterDoesNotMeasureUntilItIsOpened(t *testing.T) {
	h, srv, _, p := playerEnvWith(t, map[string]string{"COLORTERM": "truecolor"})
	if err := p.Attach(ctxBackground()); err != nil {
		t.Fatal(err)
	}
	if err := p.Replace(ctxBackground(), songs(3), 1); err != nil {
		t.Fatal(err)
	}
	h.emit("session_start", map[string]any{"reason": "reload"})
	waitFor(t, "the footer after the reload", statusHas(h, "Song 2"))
	time.Sleep(300 * time.Millisecond)
	if hasCommand(srv, "astats") {
		t.Fatalf("measuring started with no screen open: filters %v", srv.Filters())
	}
	done := h.command("music")
	h.waitOpen()
	waitFor(t, "measuring once the player is open", func() bool { return hasCommand(srv, "astats") })
	h.input("q")
	<-done
	waitFor(t, "the filter removed on hide", func() bool { return len(srv.Filters()) == 0 })
}
