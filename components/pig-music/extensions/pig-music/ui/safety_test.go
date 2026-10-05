package ui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
)

// M8c: safety and settings. Colours stay inside the overlay's cells, nothing outside colour is written, NO_COLOR and
// 16-colour terminals are honoured, and calm / low-power / palette / pulse can be changed while the player runs.

func TestTheSettingsChangeTheRunningModelAtOnce(t *testing.T) {
	r, lp, k := pulseRig(t, nil)
	r.state(playing(tracks(3), 0))
	if sets(lp) != "+" {
		t.Fatalf("pulse should be running: %q", sets(lp))
	}
	// pulse switched off: measuring stops, the tick chain ends
	r.send(StyleMsg{Palette: true, Pulse: false})
	if sets(lp) != "+-" {
		t.Errorf("measuring after the pulse was switched off: %q", sets(lp))
	}
	// palette off: nothing is painted any more
	r.send(StyleMsg{Palette: false, Pulse: true})
	for _, l := range r.screen() {
		if strings.Contains(l, "48;2;") {
			t.Fatalf("a background after the palette was switched off: %q", l)
		}
	}
	if sets(lp) != "+-" {
		t.Errorf("no palette means no pulse: %q", sets(lp))
	}
	// and back on
	r.send(StyleMsg{Palette: true, Pulse: true})
	if sets(lp) != "+-+" {
		t.Errorf("pulse back on: %q", sets(lp))
	}
	if _, bg := firstColours(t, r.screen()[0]); bg == (art.RGB{}) && k.count() == 0 {
		t.Error("the palette did not come back")
	}
}

func TestCalmSwitchedOnMidFadeSnapsAndStopsEveryTick(t *testing.T) {
	r, _, k := pulseRig(t, func(d *Deps) { d.Art = redCover(); d.Pulse = false })
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	r.state(playing(tracks(3), 1)) // a fade starts
	n := k.count()
	r.send(StyleMsg{Palette: true, Calm: true})
	want := art.Derive(art.Extract(paint(color.RGBA{30, 60, 210, 255}), 4))
	if _, bg := firstColours(t, r.screen()[0]); bg != want.Top {
		t.Errorf("calm should land on the new palette at once: %v, want %v", bg, want.Top)
	}
	r.send(TickMsg{})
	if k.count() != n {
		t.Errorf("%d ticks after calm was switched on", k.count()-n)
	}
}

func TestLowPowerSlowsThePulseTheDiscAndEndsTheFade(t *testing.T) {
	r, lp, k := pulseRig(t, func(d *Deps) { d.LowPower = true })
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	lp.setLevel(0.3)
	for i := 0; i < 5; i++ {
		r.send(TickMsg{})
	}
	for _, d := range k.d {
		if d < 150*time.Millisecond {
			t.Fatalf("a tick every %v in low-power mode (about 6 frames a second at most)", d)
		}
	}
	// the disc: two ticks of a normal-speed chain turn it two frames; low power turns it one
	slow, _, _ := pulseRig(t, func(d *Deps) { d.LowPower = true; d.Pulse = false })
	fast, _, _ := pulseRig(t, func(d *Deps) { d.Pulse = false })
	for _, rg := range []*rig{slow, fast} {
		rg.state(playing(tracks(3), 0))
		rg.send(ShowPlayerMsg{})
	}
	f0s, f0f := slow.model().frame, fast.model().frame
	for i := 0; i < 4; i++ {
		slow.send(TickMsg{})
		fast.send(TickMsg{})
	}
	if ds, df := slow.model().frame-f0s, fast.model().frame-f0f; ds*2 != df {
		t.Errorf("low power turned the disc %d frames where normal turns %d (half expected)", ds, df)
	}
	// a palette change snaps, no fade
	fa, _, kf := pulseRig(t, func(d *Deps) { d.LowPower = true; d.Pulse = false; d.Art = redCover() })
	fa.state(playing(tracks(3), 0))
	fa.send(ShowPlayerMsg{})
	fa.state(playing(tracks(3), 1))
	n := kf.count()
	fa.send(TickMsg{})
	next := art.Derive(art.Extract(paint(color.RGBA{30, 60, 210, 255}), 4))
	if _, bg := firstColours(t, fa.screen()[0]); bg != next.Top {
		t.Errorf("low power fades nothing: %v, want %v", bg, next.Top)
	}
	_ = n
}

func TestSixteenColourTerminalsGetTheBasicCodesOnly(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, redCover(), art.Color16)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	for i, l := range r.screen() {
		if strings.Contains(l, "38;2;") || strings.Contains(l, "38;5;") || strings.Contains(l, "48;2;") || strings.Contains(l, "48;5;") {
			t.Fatalf("row %d uses more than 16 colours: %q", i, l)
		}
		if !strings.HasPrefix(l, "\x1b[97;4") && !strings.HasPrefix(l, "\x1b[97;40") {
			t.Fatalf("row %d does not open with white on a basic background: %q", i, firstN(l, 20))
		}
		if w := ansi.StringWidth(l); w != 100 {
			t.Fatalf("row %d is %d wide", i, w)
		}
	}
}

// Whatever the mode and the state, nothing but colours is written, and no colour leaves the overlay's lines: every line ends
// with a reset, so no colour can run into the terminal's own cells.
func TestNoModeWritesAnythingButContainedColours(t *testing.T) {
	for _, mode := range []art.Mode{art.Mono, art.Color16, art.Color256, art.TrueColor} {
		r, lp, _ := pulseRig(t, func(d *Deps) { d.ArtMode = mode; d.Art = redCover() })
		r.state(playing(tracks(30), 0))
		r.send(ShowPlayerMsg{})
		lp.setLevel(0.9)
		r.send(TickMsg{})
		for i, l := range r.screen() {
			for _, bad := range []string{"\x1b]", "\x1b_", "\x1bP", "\x1b[?", "\x1b[2J", "\x1b[H", "\x1b[3J", "\x07"} {
				if strings.Contains(l, bad) {
					t.Fatalf("mode %v row %d: %q is not a colour", mode, i, bad)
				}
			}
			if mode != art.Mono && !strings.HasSuffix(l, "\x1b[m") {
				t.Fatalf("mode %v row %d: the colours are not closed at the end of the line: %q", mode, i, l[max(0, len(l)-20):])
			}
		}
	}
}

func TestStyleMsgToleratesBeingFirst(t *testing.T) {
	var m tea.Model = New(Deps{})
	m, _ = m.Update(StyleMsg{Palette: true, Pulse: true, Calm: true, LowPower: true})
	_ = m.View()
}
