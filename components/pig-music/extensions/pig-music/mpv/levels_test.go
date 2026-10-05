package mpv

import (
	"math"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Levels: the loudness of what mpv is playing, read from an astats filter (measured on mpv 0.37: the filter's metadata is
// readable as the af-metadata/<label> property, with Overall.RMS_level and Overall.Peak_level in dBFS).

func TestThePlayerOffersLevels(t *testing.T) {
	var _ music.Levels = (*Player)(nil)
}

func TestNoLevelIsReportedUntilMeasuringIsSwitchedOn(t *testing.T) {
	p, srv, _ := inProcess(t)
	if err := p.Replace(ctx5(t), tracks(2), 0); err != nil {
		t.Fatal(err)
	}
	srv.SetLevelDB(-20, -10)
	if _, ok := p.Level(ctx5(t)); ok {
		t.Error("a level without a filter")
	}
	if n := srv.Filters(); len(n) != 0 {
		t.Errorf("filters %v before anyone asked", n)
	}
}

func TestSwitchingMeasuringOnAddsOneAstatsFilterAndOffRemovesIt(t *testing.T) {
	p, srv, _ := inProcess(t)
	if err := p.Replace(ctx5(t), tracks(2), 0); err != nil {
		t.Fatal(err)
	}
	if err := p.SetLevels(ctx5(t), true); err != nil {
		t.Fatal(err)
	}
	if err := p.SetLevels(ctx5(t), true); err != nil { // twice: still one filter
		t.Fatal(err)
	}
	f := srv.Filters()
	if len(f) != 1 || !strings.Contains(f[0], "astats") || !strings.Contains(f[0], "metadata=1") {
		t.Fatalf("filters %v", f)
	}
	if err := p.SetLevels(ctx5(t), false); err != nil {
		t.Fatal(err)
	}
	if n := srv.Filters(); len(n) != 0 {
		t.Errorf("filters %v after switching off", n)
	}
	if err := p.SetLevels(ctx5(t), false); err != nil { // off when already off is not an error
		t.Errorf("%v", err)
	}
}

func TestTheLevelIsReadAsLinearRMSAndPeak(t *testing.T) {
	p, srv, _ := inProcess(t)
	p.Replace(ctx5(t), tracks(2), 0)
	if err := p.SetLevels(ctx5(t), true); err != nil {
		t.Fatal(err)
	}
	srv.SetLevelDB(-20, -6)
	l, ok := p.Level(ctx5(t))
	if !ok {
		t.Fatal("no level while measuring")
	}
	if math.Abs(l.RMS-0.1) > 0.001 || math.Abs(l.Peak-math.Pow(10, -6.0/20)) > 0.001 {
		t.Errorf("%+v", l)
	}
	srv.SetLevelDB(math.Inf(-1), math.Inf(-1)) // silence: astats says -inf
	if l, ok := p.Level(ctx5(t)); !ok || l.RMS != 0 || l.Peak != 0 {
		t.Errorf("silence: %+v %v", l, ok)
	}
	srv.SetLevelDB(3, 4) // above full scale (clipping): clamped to 1
	if l, _ := p.Level(ctx5(t)); l.RMS != 1 || l.Peak != 1 {
		t.Errorf("clamped: %+v", l)
	}
}

func TestNoLevelWhileNothingPlays(t *testing.T) {
	p, srv, _ := inProcess(t)
	if err := p.SetLevels(ctx5(t), true); err != nil {
		t.Fatalf("switching on while idle is allowed: %v", err)
	}
	srv.SetLevelDB(-20, -10)
	if _, ok := p.Level(ctx5(t)); ok {
		t.Error("a level with nothing loaded")
	}
}
