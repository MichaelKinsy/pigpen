//go:build !windows

package mpv

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// TestRealMpvShufflePutsThePlayingTrackFirst checks the fake's model of playlist-shuffle against a real mpv: eight
// generated tones (no network, --ao=null), the first playing, shuffled three times. A real mpv moves the playing entry
// to a random place; the Player must put it first each time.
//
//	PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvShuffle -v
func TestRealMpvShufflePutsThePlayingTrackFirst(t *testing.T) {
	if os.Getenv("PIG_MUSIC_SMOKE") != "1" {
		t.Skip("set PIG_MUSIC_SMOKE=1 to run against a real mpv")
	}
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Fatal("the smoke test needs mpv on PATH")
	}
	ts := make([]music.Track, 8)
	for i := range ts {
		ts[i] = music.Track{ID: fmt.Sprintf("av://lavfi:sine=frequency=%d:duration=600", 200+10*i), Title: fmt.Sprintf("tone %d", i)}
	}
	p := New(Config{Paths: music.PathsIn(shortDir(t)), PlayURL: func(t music.Track) string { return t.ID }, ExtraArgs: []string{"--ao=null"}})
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx5(t)) })
	if err := p.Replace(ctx5(t), ts, 0); err != nil {
		t.Fatal(err)
	}
	wait := func(what string, ok func(music.State) bool) music.State {
		t.Helper()
		var st music.State
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if st = p.State(); ok(st) {
				return st
			}
		}
		t.Fatalf("%s: state index %d of %d, shuffle %v, track %v", what, st.Index, len(st.Queue), st.Shuffle, st.Track)
		return st
	}
	wait("playing the first tone", func(s music.State) bool { return len(s.Queue) == 8 && s.Index == 0 })
	for round := 0; round < 3; round++ {
		if err := p.SetShuffle(ctx5(t), true); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond) // let mpv's playlist events arrive
		st := wait(fmt.Sprintf("round %d: shuffled with the playing tone first", round), func(s music.State) bool {
			return s.Shuffle && s.Index == 0 && s.Track != nil && s.Track.ID == ts[0].ID
		})
		if len(st.Queue) != 8 {
			t.Fatalf("round %d: %d entries", round, len(st.Queue))
		}
		if err := p.SetShuffle(ctx5(t), false); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		st = wait(fmt.Sprintf("round %d: unshuffled", round), func(s music.State) bool { return !s.Shuffle && s.Index == 0 })
		for i, q := range st.Queue {
			if q.ID != ts[i].ID {
				t.Fatalf("round %d: unshuffle left %s at %d", round, q.ID, i)
			}
		}
	}
}
