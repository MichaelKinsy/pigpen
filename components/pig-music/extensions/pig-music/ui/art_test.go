package ui

import (
	"context"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// fakeArt is an Artwork that answers from a map and counts the requests per track.
type fakeArt struct {
	mu   sync.Mutex
	imgs map[string]image.Image
	errs map[string]error
	asks []string
}

func (f *fakeArt) Image(_ context.Context, t music.Track) (image.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asks = append(f.asks, t.ID)
	if err := f.errs[t.ID]; err != nil {
		return nil, err
	}
	if img, ok := f.imgs[t.ID]; ok {
		return img, nil
	}
	return nil, context.DeadlineExceeded
}

func (f *fakeArt) count(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.asks {
		if a == id {
			n++
		}
	}
	return n
}

func paint(c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// ticker stands in for tea.Tick: it counts the ticks asked for, and its command delivers nothing, so the tests send each
// TickMsg themselves (the rig runs commands inline, so a real tick chain would never end).
type ticker struct {
	mu sync.Mutex
	n  int
	d  []time.Duration
}

func (k *ticker) tick(d time.Duration) tea.Cmd {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.n++
	k.d = append(k.d, d)
	return func() tea.Msg { return nil }
}

func (k *ticker) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.n
}

func artRig(t *testing.T, w, h int, a music.Artwork, mode art.Mode) (*rig, *ticker) {
	t.Helper()
	r := newRig(t, w, h)
	k := &ticker{}
	r.m = New(Deps{Source: r.source, Player: r.player, Art: a, ArtMode: mode, Tick: k.tick})
	r.send(tea.WindowSizeMsg{Width: w, Height: h})
	return r, k
}

func hasBraille(s string) bool {
	for _, r := range s {
		if r >= 0x2800 && r <= 0x28FF {
			return true
		}
	}
	return false
}

func (r *rig) raw() string { return r.m.View().Content }

// ── the disc ────────────────────────────────────────────────────────────────

func TestThePlayerScreenShowsADiscWhenThereIsNoCoverArt(t *testing.T) {
	r, _ := artRig(t, 100, 30, nil, art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	txt := r.text()
	if !hasBraille(txt) {
		t.Fatalf("no disc on the Player screen:\n%s", txt)
	}
	if !strings.Contains(txt, "Song 1") || !strings.Contains(txt, "Artist") {
		t.Errorf("the title and artist go with the disc:\n%s", txt)
	}
}

func TestTheDiscIsLeftOutWhereThereIsNoRoomForIt(t *testing.T) {
	for _, sz := range [][2]int{{100, 14}, {59, 30}, {40, 12}} {
		r, _ := artRig(t, sz[0], sz[1], nil, art.TrueColor)
		r.state(playing(tracks(3), 0))
		r.send(ShowPlayerMsg{})
		if hasBraille(r.text()) {
			t.Errorf("%v: a disc where it cannot fit:\n%s", sz, r.text())
		}
	}
}

func TestTheDiscSpinsWhilePlayingOnATickAndTheChainContinues(t *testing.T) {
	r, k := artRig(t, 100, 30, nil, art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	if k.count() != 1 {
		t.Fatalf("%d ticks requested after the Player screen showed a playing track, want 1", k.count())
	}
	if k.d[0] < 50*time.Millisecond || k.d[0] > 500*time.Millisecond {
		t.Errorf("tick every %v", k.d[0])
	}
	before := r.raw()
	r.send(TickMsg{})
	if after := r.raw(); after == before {
		t.Error("a tick did not turn the disc")
	}
	if k.count() != 2 {
		t.Errorf("the chain stopped while playing: %d ticks", k.count())
	}
	// more state updates while a tick is on its way do not start a second chain
	r.state(playing(tracks(3), 0))
	r.state(playing(tracks(3), 0))
	if k.count() != 2 {
		t.Errorf("a second chain was started: %d ticks", k.count())
	}
}

func TestNothingTicksOrRedrawsWhilePausedStoppedHiddenOrOffThePlayerScreen(t *testing.T) {
	r, k := artRig(t, 100, 30, nil, art.TrueColor)
	st := playing(tracks(3), 0)
	r.state(st)
	if k.count() != 0 {
		t.Fatalf("a tick on the Search screen: %d", k.count())
	}
	r.send(ShowPlayerMsg{})
	n := k.count() // 1, on its way

	// paused: the tick on its way arrives, turns nothing, and asks for no more
	st.Paused = true
	r.state(st)
	paused := r.raw()
	r.send(TickMsg{})
	if k.count() != n {
		t.Errorf("a tick requested while paused")
	}
	if r.raw() != paused {
		t.Error("the picture changed while paused")
	}
	// resuming starts the chain again, once
	st.Paused = false
	r.state(st)
	if k.count() != n+1 {
		t.Errorf("resume: %d ticks, want %d", k.count(), n+1)
	}
	r.send(TickMsg{})
	n = k.count()

	// hidden
	r.send(ClosedMsg{})
	r.send(TickMsg{})
	if k.count() != n {
		t.Error("a tick requested while hidden")
	}
	r.send(OpenedMsg{})
	if k.count() != n+1 {
		t.Errorf("showing the screen again should restart the chain: %d, want %d", k.count(), n+1)
	}
	r.send(TickMsg{})
	n = k.count()

	// stopped: nothing is loaded
	r.state(music.State{Index: -1})
	r.send(TickMsg{})
	if k.count() != n {
		t.Error("a tick requested with nothing playing")
	}
	// another screen
	r.state(st)
	r.send(TickMsg{})
	n = k.count()
	r.key("tab") // Player -> Search
	r.send(TickMsg{})
	if k.count() != n {
		t.Error("a tick requested away from the Player screen")
	}
}

func TestTheDiscStandsStillWhileNothingPlaysAndShowsOnlyAfterTheFirstFrame(t *testing.T) {
	r, k := artRig(t, 100, 30, nil, art.TrueColor)
	r.send(ShowPlayerMsg{})
	if !hasBraille(r.text()) {
		t.Errorf("an idle Player screen still shows its disc:\n%s", r.text())
	}
	if k.count() != 0 {
		t.Error("a tick with nothing playing")
	}
}

// ── cover art ───────────────────────────────────────────────────────────────

func TestCoverArtReplacesTheDiscOnceItHasLoadedAndIsFetchedOncePerTrack(t *testing.T) {
	fa := &fakeArt{imgs: map[string]image.Image{"id00": paint(color.RGBA{255, 0, 0, 255})}}
	r, k := artRig(t, 100, 30, fa, art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	r.state(playing(tracks(3), 0))
	r.send(TickMsg{})
	if fa.count("id00") != 1 {
		t.Fatalf("asked %d times for the same track", fa.count("id00"))
	}
	raw := r.raw()
	if !strings.Contains(raw, "38;2;255;0;0") || strings.Count(ansi.Strip(raw), "▀") < 20 {
		t.Errorf("the cover is not drawn:\n%s", r.text())
	}
	if hasBraille(r.text()) {
		t.Errorf("the disc is still drawn beside the cover:\n%s", r.text())
	}
	n := k.count()
	r.send(TickMsg{})
	if k.count() != n {
		t.Error("a still picture should not keep a tick chain")
	}
}

func TestAnotherTrackFetchesItsOwnCoverAndShowsTheDiscMeanwhile(t *testing.T) {
	fa := &fakeArt{imgs: map[string]image.Image{
		"id00": paint(color.RGBA{255, 0, 0, 255}),
		"id01": paint(color.RGBA{0, 0, 255, 255}),
	}}
	r, _ := artRig(t, 100, 30, fa, art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	r.state(playing(tracks(3), 1))
	raw := r.raw()
	if !strings.Contains(raw, "38;2;0;0;255") || strings.Contains(raw, "38;2;255;0;0") {
		t.Errorf("the previous cover is still up or the new one is missing:\n%s", r.text())
	}
	if fa.count("id01") != 1 {
		t.Errorf("asked %d times", fa.count("id01"))
	}
}

func TestACoverThatArrivesLateForAnotherTrackIsIgnored(t *testing.T) {
	fa := &fakeArt{}
	r, _ := artRig(t, 100, 30, fa, art.TrueColor)
	r.state(playing(tracks(3), 1))
	r.send(ShowPlayerMsg{})
	r.send(artMsg{id: "id00", img: paint(color.RGBA{255, 0, 0, 255})})
	if strings.Contains(r.raw(), "38;2;255;0;0") {
		t.Error("a cover for another track was drawn")
	}
}

func TestACoverThatFailsFallsBackToTheDiscAndIsNotAskedForAgain(t *testing.T) {
	fa := &fakeArt{errs: map[string]error{"id00": context.DeadlineExceeded}}
	r, _ := artRig(t, 100, 30, fa, art.TrueColor)
	for i := 0; i < 3; i++ {
		r.state(playing(tracks(3), 0))
	}
	r.send(ShowPlayerMsg{})
	r.state(playing(tracks(3), 0))
	if fa.count("id00") != 1 {
		t.Errorf("asked %d times after a failure", fa.count("id00"))
	}
	if !hasBraille(r.text()) {
		t.Errorf("no disc after the cover failed:\n%s", r.text())
	}
	if strings.Contains(r.text(), "deadline") {
		t.Errorf("a missing cover is not worth an error on screen:\n%s", r.text())
	}
}

func TestCoverArtInMonochromeUsesTheBlockRampAndNoColourEscapes(t *testing.T) {
	fa := &fakeArt{imgs: map[string]image.Image{"id00": paint(color.White)}}
	r, _ := artRig(t, 100, 30, fa, art.Mono)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	if !strings.Contains(r.text(), "████") {
		t.Errorf("no block art:\n%s", r.text())
	}
	rows := 0
	for _, l := range r.screen() {
		if strings.Contains(l, "████") && strings.Contains(l, "\x1b[2m│") { // not the play bar's progress
			rows++
			if left, _, ok := strings.Cut(l, "\x1b[2m│"); !ok || strings.Contains(left, "\x1b") {
				t.Errorf("an escape in a monochrome cover row: %q", l)
			}
		}
	}
	if rows < 4 {
		t.Errorf("%d cover rows found", rows)
	}
}

func TestNoGraphicsProtocolEscapeIsEverWritten(t *testing.T) {
	fa := &fakeArt{imgs: map[string]image.Image{"id00": paint(color.RGBA{9, 99, 199, 255})}}
	for _, mode := range []art.Mode{art.Mono, art.Color256, art.TrueColor} {
		r, _ := artRig(t, 100, 30, fa, mode)
		r.state(playing(tracks(3), 0))
		r.send(ShowPlayerMsg{})
		raw := r.raw()
		for _, bad := range []string{"\x1b_G", "\x1b]1337", "\x1bP", "\x1b_"} { // Kitty, iTerm2, sixel, APC
			if strings.Contains(raw, bad) {
				t.Errorf("mode %v: an image protocol escape %q", mode, bad)
			}
		}
	}
}

func TestTheCoverAndTheDiscFitEverySizeExactly(t *testing.T) {
	fa := &fakeArt{imgs: map[string]image.Image{"id00": paint(color.RGBA{200, 100, 50, 255})}}
	for _, sz := range [][2]int{{120, 40}, {100, 30}, {80, 24}, {60, 16}, {61, 20}, {59, 30}, {200, 14}, {70, 60}} {
		for _, a := range []music.Artwork{nil, fa} {
			r, _ := artRig(t, sz[0], sz[1], a, art.TrueColor)
			r.state(playing(tracks(30), 0))
			r.send(ShowPlayerMsg{})
			lines := r.screen()
			if len(lines) != sz[1] {
				t.Errorf("%v: %d lines", sz, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != sz[0] {
					t.Errorf("%v line %d is %d wide: %q", sz, i, w, ansi.Strip(l))
					break
				}
			}
		}
	}
}

func TestCoverArtIsNeverAskedForWhileDrawing(t *testing.T) {
	fa := &fakeArt{}
	r, _ := artRig(t, 100, 30, fa, art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	n := len(fa.asks)
	for i := 0; i < 10; i++ {
		_ = r.m.View()
	}
	if len(fa.asks) != n {
		t.Error("View fetched a cover")
	}
}
