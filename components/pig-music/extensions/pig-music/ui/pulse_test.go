package ui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// M8b: the overlay moves with the music. Real levels (music.Levels) drive a subtle pulse: the background brightens on a
// beat, the disc turns faster, a slim meter shows the loudness. Everything stops when nothing is playing and shown.

type levelPlayer struct {
	*stubPlayer
	mu    sync.Mutex
	sets  []bool
	reads int
	lvl   music.Level
	ok    bool
}

func (p *levelPlayer) SetLevels(_ context.Context, on bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sets = append(p.sets, on)
	return nil
}

func (p *levelPlayer) Level(context.Context) (music.Level, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	return p.lvl, p.ok
}

func (p *levelPlayer) setLevel(rms float64) {
	p.mu.Lock()
	p.lvl, p.ok = music.Level{RMS: rms, Peak: rms}, true
	p.mu.Unlock()
}

func (p *levelPlayer) calls() ([]bool, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bool(nil), p.sets...), p.reads
}

func pulseRig(t *testing.T, mutate func(*Deps)) (*rig, *levelPlayer, *ticker) {
	t.Helper()
	r := newRig(t, 100, 30)
	lp := &levelPlayer{stubPlayer: r.player}
	k := &ticker{}
	d := Deps{Source: r.source, Player: lp, ArtMode: art.TrueColor, Vibes: true, Pulse: true, Tick: k.tick}
	if mutate != nil {
		mutate(&d)
	}
	r.m = New(d)
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.player = lp.stubPlayer
	return r, lp, k
}

func (r *rig) model() Model { return r.m.(Model) }

func sets(lp *levelPlayer) string {
	s, _ := lp.calls()
	out := ""
	for _, on := range s {
		if on {
			out += "+"
		} else {
			out += "-"
		}
	}
	return out
}

func TestMeasuringRunsOnlyWhilePlayingAndShown(t *testing.T) {
	r, lp, _ := pulseRig(t, nil)
	if sets(lp) != "" {
		t.Fatalf("measuring before anything plays: %q", sets(lp))
	}
	st := playing(tracks(3), 0)
	r.state(st)
	if sets(lp) != "+" {
		t.Fatalf("playing: %q", sets(lp))
	}
	r.state(st) // more state updates: still once
	st.Paused = true
	r.state(st)
	if sets(lp) != "+-" {
		t.Errorf("paused: %q", sets(lp))
	}
	st.Paused = false
	r.state(st)
	r.send(ClosedMsg{})
	r.send(OpenedMsg{})
	r.state(music.State{Index: -1})
	if sets(lp) != "+-+-+-" {
		t.Errorf("resume, hide, show, stop: %q, want +-+-+-", sets(lp))
	}
}

func TestPulseTicksAreCappedAtAboutFifteenFramesASecond(t *testing.T) {
	r, lp, k := pulseRig(t, nil)
	r.state(playing(tracks(3), 0))
	lp.setLevel(0.3)
	for i := 0; i < 20; i++ {
		r.send(TickMsg{})
	}
	if len(k.d) == 0 {
		t.Fatal("no ticks")
	}
	for _, d := range k.d {
		if d < 50*time.Millisecond {
			t.Fatalf("a tick every %v is over 20 frames a second", d)
		}
	}
	if last := k.d[len(k.d)-1]; last > 70*time.Millisecond {
		t.Errorf("while pulsing the tick is %v; the pulse needs about 15 frames a second", last)
	}
}

func TestEachTickReadsTheLevelOnceAndNothingIsReadWhenNothingMoves(t *testing.T) {
	r, lp, _ := pulseRig(t, nil)
	st := playing(tracks(3), 0)
	r.state(st)
	lp.setLevel(0.3)
	_, before := lp.calls()
	for i := 0; i < 5; i++ {
		r.send(TickMsg{})
	}
	if _, after := lp.calls(); after-before != 5 {
		t.Errorf("%d reads for 5 ticks", after-before)
	}
	st.Paused = true
	r.state(st)
	_, before = lp.calls()
	r.send(TickMsg{}) // the tick that was on its way
	r.send(TickMsg{})
	if _, after := lp.calls(); after != before {
		t.Errorf("the level was read while paused")
	}
}

func bgOf(t *testing.T, r *rig, row int) art.RGB {
	t.Helper()
	_, bg := firstColours(t, r.screen()[row])
	return bg
}

func TestABeatBrightensTheBackgroundThenItSettlesAndSteadyLoudnessIsNoBeat(t *testing.T) {
	r, lp, _ := pulseRig(t, nil)
	r.state(playing(tracks(3), 0))
	pal := art.FromTrack("Artist", "Song 1")
	lp.setLevel(0.05)
	for i := 0; i < 40; i++ {
		r.send(TickMsg{})
	}
	if bg := bgOf(t, r, 0); bg != pal.At(0, 30) {
		t.Fatalf("quiet music should leave the background alone: %v, want %v", bg, pal.At(0, 30))
	}
	lp.setLevel(0.6) // a sudden loud beat
	r.send(TickMsg{})
	hit := bgOf(t, r, 0)
	if art.Luminance(hit) <= art.Luminance(pal.At(0, 30)) {
		t.Errorf("the beat did not brighten the background: %v -> %v", pal.At(0, 30), hit)
	}
	for i := 0; i < 150; i++ { // the same loudness, held: not a beat any more
		r.send(TickMsg{})
	}
	if bg := bgOf(t, r, 0); bg != pal.At(0, 30) {
		t.Errorf("steady loudness should not keep pulsing: %v, want %v", bg, pal.At(0, 30))
	}
}

func TestEveryLineStaysReadableAtTheTopOfABeat(t *testing.T) {
	r, lp, _ := pulseRig(t, nil)
	r.state(playing(tracks(3), 0))
	lp.setLevel(0.02)
	for i := 0; i < 30; i++ {
		r.send(TickMsg{})
	}
	lp.setLevel(1)
	for step := 0; step < 4; step++ {
		r.send(TickMsg{})
		for i, l := range r.screen() {
			fg, bg := firstColours(t, l)
			if c := art.Contrast(fg, bg); c < 7 {
				t.Fatalf("step %d row %d: contrast %.2f", step, i, c)
			}
		}
	}
}

func TestTheDiscTurnsFasterOnABeat(t *testing.T) {
	still, _, _ := pulseRig(t, nil)
	still.state(playing(tracks(3), 0))
	still.send(ShowPlayerMsg{})
	beat, lp, _ := pulseRig(t, nil)
	beat.state(playing(tracks(3), 0))
	beat.send(ShowPlayerMsg{})
	quiet := func(r *rig, l *levelPlayer) {
		l.setLevel(0.05)
		for i := 0; i < 30; i++ {
			r.send(TickMsg{})
		}
	}
	quiet(beat, lp)
	stillLP := still.model().deps.Player.(*levelPlayer)
	quiet(still, stillLP)
	f0s, f0b := still.model().frame, beat.model().frame
	lp.setLevel(0.9)
	stillLP.setLevel(0.05)
	for i := 0; i < 6; i++ {
		still.send(TickMsg{})
		beat.send(TickMsg{})
	}
	moved := func(r *rig, from int) int { return (r.model().frame - from + art.DiscFrames) % art.DiscFrames }
	if moved(beat, f0b) <= moved(still, f0s) {
		t.Errorf("the disc moved %d frames on a beat and %d without one", moved(beat, f0b), moved(still, f0s))
	}
}

func TestASlimMeterShowsTheLoudnessWhileMeasuring(t *testing.T) {
	r, lp, _ := pulseRig(t, nil)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	lp.setLevel(0.9)
	r.send(TickMsg{})
	loud := r.text()
	if !strings.Contains(loud, "Level") {
		t.Fatalf("no meter:\n%s", loud)
	}
	count := func(s string) int {
		for _, l := range strings.Split(s, "\n") {
			if strings.Contains(l, "Level") {
				return strings.Count(l, "█")
			}
		}
		return -1
	}
	hi := count(loud)
	lp.setLevel(0.1)
	for i := 0; i < 40; i++ {
		r.send(TickMsg{})
	}
	if lo := count(r.text()); lo >= hi || hi < 6 {
		t.Errorf("the meter should shrink with the loudness: %d then %d full cells", hi, lo)
	}
	st := playing(tracks(3), 0)
	st.Paused = true
	r.state(st)
	if strings.Contains(r.text(), "Level") {
		t.Error("a meter while paused")
	}
}

func TestNothingPulsesWithoutLevelsCalmNoColourOrTheSwitchOff(t *testing.T) {
	for name, mutate := range map[string]func(*Deps){
		"calm":   func(d *Deps) { d.Calm = true },
		"mono":   func(d *Deps) { d.ArtMode = art.Mono },
		"off":    func(d *Deps) { d.Pulse = false },
		"no pal": func(d *Deps) { d.Vibes = false },
	} {
		r, lp, _ := pulseRig(t, mutate)
		r.state(playing(tracks(3), 0))
		r.send(ShowPlayerMsg{})
		lp.setLevel(0.9)
		r.send(TickMsg{})
		if sets(lp) != "" {
			t.Errorf("%s: measuring started: %q", name, sets(lp))
		}
		if _, reads := lp.calls(); reads != 0 {
			t.Errorf("%s: %d level reads", name, reads)
		}
		if strings.Contains(r.text(), "Level") {
			t.Errorf("%s: a meter", name)
		}
	}
	// a player that cannot measure
	plain := newRig(t, 100, 30)
	k := &ticker{}
	plain.m = New(Deps{Source: plain.source, Player: plain.player, ArtMode: art.TrueColor, Vibes: true, Pulse: true, Tick: k.tick})
	plain.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	plain.state(playing(tracks(3), 0))
	plain.send(TickMsg{})
	if strings.Contains(plain.text(), "Level") {
		t.Error("a meter for a player without levels")
	}
}

func TestAMissingLevelLetsThePulseDieAwayWithoutAnError(t *testing.T) {
	r, lp, _ := pulseRig(t, nil)
	r.state(playing(tracks(3), 0))
	lp.setLevel(0.05)
	for i := 0; i < 30; i++ {
		r.send(TickMsg{})
	}
	lp.setLevel(0.9)
	r.send(TickMsg{})
	lp.mu.Lock()
	lp.ok = false // mpv stopped answering
	lp.mu.Unlock()
	for i := 0; i < 40; i++ {
		r.send(TickMsg{})
	}
	pal := art.FromTrack("Artist", "Song 1")
	if bg := bgOf(t, r, 0); bg != pal.At(0, 30) {
		t.Errorf("the pulse should have died away: %v", bg)
	}
	if strings.Contains(r.text(), "level") && strings.Contains(strings.ToLower(r.text()), "error") {
		t.Error("an error on screen for a missing level")
	}
}

func TestThePulseAndMeterFitEverySizeExactly(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {60, 16}, {40, 12}, {30, 8}, {24, 6}, {200, 10}} {
		r, lp, _ := pulseRig(t, nil)
		r.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		r.state(playing(tracks(30), 0))
		r.send(ShowPlayerMsg{})
		lp.setLevel(0.7)
		r.send(TickMsg{})
		lines := r.screen()
		if len(lines) != sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != sz[0] {
				t.Errorf("%v line %d is %d wide", sz, i, w)
				break
			}
		}
	}
}

func TestCalmStopsTheDiscTheFadeAndEveryTick(t *testing.T) {
	r, _, k := pulseRig(t, func(d *Deps) { d.Calm = true; d.Art = redCover() })
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	if !hasBraille(r.text()) && !strings.Contains(r.text(), "▀") {
		t.Errorf("calm still draws the picture:\n%s", r.text())
	}
	cover0 := art.Derive(art.Extract(paint(color_(200, 30, 40)), 4))
	r.state(playing(tracks(3), 1)) // another track: the palette changes
	r.send(TickMsg{})
	if k.count() != 0 {
		t.Errorf("%d ticks were asked for in calm mode", k.count())
	}
	next := art.Derive(art.Extract(paint(color_(30, 60, 210)), 4))
	if _, bg := firstColours(t, r.screen()[0]); bg != next.Top {
		t.Errorf("a calm palette change is instant: %v, want %v (was %v)", bg, next.Top, cover0.Top)
	}
}
