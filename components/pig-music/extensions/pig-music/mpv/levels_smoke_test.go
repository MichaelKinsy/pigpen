package mpv

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// TestRealMpvLevelsSmoke measures the levels of real tone files with a real mpv: no network, no sound card (--ao=null).
//
//	PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvLevels -v
//
// A sine of amplitude 0.5 has an RMS of 0.354 and a peak of 0.5; one of 0.05 is ten times quieter.
func TestRealMpvLevelsSmoke(t *testing.T) {
	if os.Getenv("PIG_MUSIC_SMOKE") != "1" {
		t.Skip("set PIG_MUSIC_SMOKE=1 to run against a real mpv")
	}
	for _, prog := range []string{"mpv", "ffmpeg"} {
		if _, err := exec.LookPath(prog); err != nil {
			t.Fatalf("the smoke test needs %s on PATH", prog)
		}
	}
	dir := shortDir(t)
	files := map[string]string{}
	for name, vol := range map[string]float64{"loud": 4, "quiet": 0.4} { // sine is 0.125 at volume 1
		f := filepath.Join(dir, name+".wav")
		out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:duration=60,volume=%g", vol), "-ar", "22050", f).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, out)
		}
		files[name] = f
	}
	paths := music.PathsIn(filepath.Join(dir, "run"))
	p := New(Config{Paths: paths, PlayURL: func(t music.Track) string { return t.ID }, ExtraArgs: []string{"--ao=null"}})
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx5(t)) })

	read := func(what string) music.Level {
		t.Helper()
		var l music.Level
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if got, ok := p.Level(ctx5(t)); ok && got.RMS > 0 {
				l = got
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if l.RMS == 0 {
			t.Fatalf("%s: no level read in 10 s", what)
		}
		return l
	}
	if err := p.Replace(ctx5(t), []music.Track{{ID: files["loud"], Title: "loud"}}, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Level(ctx5(t)); ok {
		t.Error("a level before measuring was switched on")
	}
	if err := p.SetLevels(ctx5(t), true); err != nil {
		t.Fatal(err)
	}
	loud := read("loud")
	t.Logf("amplitude 0.5: %+v", loud)
	if math.Abs(loud.Peak-0.5) > 0.06 || math.Abs(loud.RMS-0.354) > 0.05 {
		t.Errorf("a 0.5 sine measured %+v, want RMS 0.354 and peak 0.5", loud)
	}
	if err := p.Replace(ctx5(t), []music.Track{{ID: files["quiet"], Title: "quiet"}}, 0); err != nil {
		t.Fatal(err)
	}
	quiet := read("quiet")
	t.Logf("amplitude 0.05: %+v", quiet)
	if quiet.RMS > loud.RMS/5 {
		t.Errorf("the quiet track measured %+v against %+v: the filter must keep measuring across a new file", quiet, loud)
	}
	if err := p.SetLevels(ctx5(t), false); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Level(ctx5(t)); ok {
		t.Error("a level after measuring was switched off")
	}
	if err := p.SetLevels(ctx5(t), false); err != nil {
		t.Errorf("off twice: %v", err)
	}
}
