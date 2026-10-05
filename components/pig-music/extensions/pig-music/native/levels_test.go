package native

import (
	"context"
	"io"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Levels for the native engine: RMS and peak computed from the PCM it already decodes, measured before the volume (as mpv's
// filter is), so the loudness of the music does not depend on the volume setting.

func TestLevelOfKnownSignals(t *testing.T) {
	cases := []struct {
		name      string
		in        []float32
		rms, peak float64
	}{
		{"silence", make([]float32, 64), 0, 0},
		{"constant half", []float32{.5, .5, .5, .5, .5, .5}, 0.5, 0.5},
		{"alternating", []float32{1, -1, 1, -1}, 1, 1},
		{"one spike", []float32{0, 0, 0, 0, 0, 0.8, 0, 0}, math.Sqrt(0.64 / 8), 0.8},
		{"above full scale", []float32{3, 3, -3, 3}, 1, 1},
	}
	for _, c := range cases {
		rms, peak := levelOf(c.in)
		if math.Abs(rms-c.rms) > 1e-6 || math.Abs(peak-c.peak) > 1e-6 {
			t.Errorf("%s: %.6f %.6f, want %.6f %.6f", c.name, rms, peak, c.rms, c.peak)
		}
	}
	if rms, peak := levelOf(nil); rms != 0 || peak != 0 {
		t.Error("no samples")
	}
}

// toneDec is a track of constant amplitude.
type toneDec struct {
	mu     sync.Mutex
	amp    float32
	frames int64
	pos    int64
}

func (d *toneDec) Read(p []float32) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := min(int64(len(p)/2), d.frames-d.pos)
	if n <= 0 {
		return 0, io.EOF
	}
	for i := range p[:2*n] {
		p[i] = d.amp
	}
	d.pos += n
	return int(n), nil
}
func (d *toneDec) SeekFrame(f int64) error { d.mu.Lock(); d.pos = f; d.mu.Unlock(); return nil }
func (d *toneDec) Frames() int64           { return d.frames }
func (d *toneDec) Close() error            { return nil }

type toneOpener struct{ amp float32 }

func (o toneOpener) Open(context.Context, music.Track) (Decoded, error) {
	return &toneDec{amp: o.amp, frames: OutputRate * 60}, nil
}

func newToneRig(t *testing.T, amp float32) *rig {
	t.Helper()
	d := &fakeOut{}
	e, err := NewEngine(EngineConfig{Opener: toneOpener{amp}, Output: d, Tick: 5 * time.Millisecond, OpenTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return &rig{e: e, d: d}
}

func TestTheEngineReportsLevelsOnlyWhileMeasuringAndPlaying(t *testing.T) {
	r := newToneRig(t, 0.5)
	if err := r.e.Replace(bg, tracks(1), 0); err != nil {
		t.Fatal(err)
	}
	r.playing(t)
	r.d.pull(t, 2000)
	if _, _, ok := r.e.Level(); ok {
		t.Fatal("a level before measuring was switched on")
	}
	r.e.SetMeter(true)
	r.d.pull(t, 2000)
	rms, peak, ok := r.e.Level()
	if !ok || math.Abs(rms-0.5) > 1e-4 || math.Abs(peak-0.5) > 1e-4 {
		t.Fatalf("%v %v %v, want 0.5 0.5", rms, peak, ok)
	}
	// the volume does not change what the music measures
	if err := r.e.SetVolume(bg, 20); err != nil {
		t.Fatal(err)
	}
	r.d.pull(t, 2000)
	if rms, _, _ := r.e.Level(); math.Abs(rms-0.5) > 1e-4 {
		t.Errorf("volume 20 changed the level to %v", rms)
	}
	// nothing pulled for a while: the reading is stale, so there is none
	time.Sleep(400 * time.Millisecond)
	if _, _, ok := r.e.Level(); ok {
		t.Error("a stale reading is reported as current")
	}
	r.d.pull(t, 2000)
	r.e.SetMeter(false)
	if _, _, ok := r.e.Level(); ok {
		t.Error("a level after measuring was switched off")
	}
}

func TestLevelsCrossTheWireToTheClient(t *testing.T) {
	r := startDaemon(t, 0)
	p := r.player()
	if err := p.Attach(bg); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var _ music.Levels = p
	if _, ok := p.Level(bg); ok {
		t.Error("a level before measuring")
	}
	if err := p.Replace(bg, tracks(1), 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, "playing", func() bool { st := p.State(); return st.Duration > 0 })
	if err := p.SetLevels(bg, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a level", func() bool {
		r.d.pull(t, 500)
		l, ok := p.Level(bg)
		return ok && l.RMS == 1 && l.Peak == 1 // the test decoder's ramp is far above full scale: clamped to 1
	})
	if err := p.SetLevels(bg, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Level(bg); ok {
		t.Error("a level after switching off")
	}
	_ = strconv.Itoa
}
