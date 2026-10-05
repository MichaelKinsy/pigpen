//go:build !windows

package mpv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// TestRealMpvSmoke runs the player against a real mpv with --ao=null and three
// generated tone files, so no network, yt-dlp or sound card is involved. It
// checks that the fake mpv the other tests use behaves as the real one does.
//
//	PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvSmoke -v
//
// It needs mpv and ffmpeg on PATH and is skipped otherwise.
func TestRealMpvSmoke(t *testing.T) {
	if os.Getenv("PIG_MUSIC_SMOKE") != "1" {
		t.Skip("set PIG_MUSIC_SMOKE=1 to run against a real mpv")
	}
	for _, prog := range []string{"mpv", "ffmpeg"} {
		if _, err := exec.LookPath(prog); err != nil {
			t.Fatalf("the smoke test needs %s on PATH", prog)
		}
	}
	dir := shortDir(t)
	var files []string
	for i, freq := range []int{330, 440, 550} {
		f := filepath.Join(dir, fmt.Sprintf("tone%d.wav", i))
		out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:duration=40", freq), "-ar", "8000", f).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, out)
		}
		files = append(files, f)
	}
	ts := make([]music.Track, len(files))
	for i, f := range files {
		ts[i] = music.Track{ID: f, Title: filepath.Base(f)}
	}
	paths := music.PathsIn(filepath.Join(dir, "run"))
	cfg := Config{Paths: paths, PlayURL: func(t music.Track) string { return t.ID }, ExtraArgs: []string{"--ao=null"}}
	p := New(cfg)
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx5(t)) })

	step := func(what string, ok func(music.State) bool) music.State {
		t.Helper()
		var st music.State
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if st = p.State(); ok(st) {
				return st
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s: state %+v", what, st)
		return st
	}
	if st := p.State(); st.Index != -1 || len(st.Queue) != 0 || st.Volume != 100 {
		t.Fatalf("idle state %+v", st)
	}

	if err := p.Replace(ctx5(t), ts, 1); err != nil {
		t.Fatal(err)
	}
	st := step("queue built around the start track", func(s music.State) bool {
		return len(s.Queue) == 3 && s.Index == 1 && s.Duration > 0
	})
	for i, q := range st.Queue {
		if q.ID != files[i] {
			t.Fatalf("queue[%d] = %s, want %s", i, q.ID, files[i])
		}
	}
	if st.Duration < 39*time.Second || st.Duration > 41*time.Second {
		t.Errorf("duration %s", st.Duration)
	}
	step("position advances", func(s music.State) bool { return s.Position > 1500*time.Millisecond })

	if err := p.SetPaused(ctx5(t), true); err != nil {
		t.Fatal(err)
	}
	step("paused", func(s music.State) bool { return s.Paused })
	if err := p.TogglePause(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	step("resumed", func(s music.State) bool { return !s.Paused })

	if err := p.SeekRelative(ctx5(t), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	step("seek forward", func(s music.State) bool { return s.Position >= 10*time.Second })
	if err := p.SetVolume(ctx5(t), 35); err != nil {
		t.Fatal(err)
	}
	step("volume", func(s music.State) bool { return s.Volume == 35 })

	if err := p.Next(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	step("next", func(s music.State) bool { return s.Index == 2 && s.Position < 3*time.Second })
	if err := p.Next(ctx5(t)); err != ErrEndOfQueue {
		t.Fatalf("Next on the last: %v", err)
	}
	// Right after a track change the position is under three seconds, so Prev goes back.
	if err := p.Prev(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	step("prev", func(s music.State) bool { return s.Index == 1 })

	// Move: entry 0 to the end, then back to the front; the playing track stays the playing track.
	if err := p.Move(ctx5(t), 0, 2); err != nil {
		t.Fatal(err)
	}
	st = step("moved down", func(s music.State) bool { return len(s.Queue) == 3 && s.Queue[2].ID == files[0] })
	if st.Queue[0].ID != files[1] || st.Queue[1].ID != files[2] || st.Track.ID != files[1] {
		t.Fatalf("after Move(0,2): %v playing %v", ids(st.Queue), st.Track)
	}
	if err := p.Move(ctx5(t), 2, 0); err != nil {
		t.Fatal(err)
	}
	st = step("moved up", func(s music.State) bool { return len(s.Queue) == 3 && s.Queue[0].ID == files[0] })
	if st.Queue[1].ID != files[1] || st.Queue[2].ID != files[2] || st.Track.ID != files[1] || st.Index != 1 {
		t.Fatalf("after Move(2,0): %v playing %v index %d", ids(st.Queue), st.Track, st.Index)
	}

	if err := p.Remove(ctx5(t), 0); err != nil {
		t.Fatal(err)
	}
	step("removed", func(s music.State) bool { return len(s.Queue) == 2 && s.Index == 0 && s.Track.ID == files[1] })
	if err := p.Enqueue(ctx5(t), ts[0]); err != nil {
		t.Fatal(err)
	}
	step("enqueued", func(s music.State) bool { return len(s.Queue) == 3 && s.Queue[2].ID == files[0] })
	if err := p.Jump(ctx5(t), 2); err != nil {
		t.Fatal(err)
	}
	step("jumped", func(s music.State) bool { return s.Index == 2 })

	// Reattach: a second player sees the same queue, track and a position that kept moving.
	before := p.State().Position
	_ = p.Close()
	time.Sleep(1200 * time.Millisecond)
	again := New(cfg)
	if err := again.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	st = again.State()
	if len(st.Queue) != 3 || st.Index != 2 || st.Position <= before {
		t.Fatalf("reattached: %+v (position was %s)", st, before)
	}
	p = again

	// What mpv does at the end of the last track is logged, not asserted: the player's
	// Next and Prev do not depend on it.
	if err := p.SeekRelative(ctx5(t), 38*time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	t.Logf("after the last track ended: %+v", p.State())
}
