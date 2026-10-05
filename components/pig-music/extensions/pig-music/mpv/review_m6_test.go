package mpv

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// rev-pig-music-m6: mpv's playlist-shuffle moves the playing entry to a random place (seen with mpv 0.37.0: playing
// index 2 of 8, after a shuffle it was at 3, 2, 1, 1 and 7). With repeat off the queue then ends after that entry, so
// every track shuffled in front of it never plays. Shuffle keeps the current track playing and puts it first, so the
// whole rest of the queue follows it.
func TestShuffleOnAnMpvThatMovesThePlayingEntryPutsItFirst(t *testing.T) {
	p, _, _ := inProcess(t)
	ts := tracks(5)
	if err := p.Replace(ctx5(t), ts, 0); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "playing", func(s music.State) bool { return len(s.Queue) == 5 && s.Index == 0 })
	if err := p.SetShuffle(ctx5(t), true); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "the queue shuffled with the playing track first (mpv put it last, so nothing would follow it)", func(s music.State) bool {
		return s.Shuffle && s.Index == 0 && s.Track != nil && s.Track.ID == ts[0].ID && strings.Join(ids(s.Queue), ",") != strings.Join(ids(ts), ",")
	})
	if err := p.SetShuffle(ctx5(t), false); err != nil {
		t.Fatal(err)
	}
	// The shuffle flag and the restored order reach the state in separate mpv events, so wait for the order itself
	// (it failed about once in thirty runs when the full suite ran in parallel, with the flag seen first).
	waitState(t, p, "unshuffled with the original order restored", func(s music.State) bool {
		return !s.Shuffle && s.Index == 0 && strings.Join(ids(s.Queue), ",") == strings.Join(ids(ts), ",")
	})
}

// With repeat all, next on the last track goes to the first (mpv wraps with loop-playlist); it is not the end of the queue.
func TestNextOnTheLastTrackWithRepeatAllWrapsToTheFirst(t *testing.T) {
	p, _, _ := inProcess(t)
	if err := p.Replace(ctx5(t), tracks(3), 2); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "on the last track", func(s music.State) bool { return s.Index == 2 })
	if err := p.SetRepeat(ctx5(t), music.RepeatAll); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "repeat all", func(s music.State) bool { return s.Repeat == music.RepeatAll })
	if err := p.Next(ctx5(t)); err != nil {
		t.Fatalf("next with repeat all on the last track: %v", err)
	}
	waitState(t, p, "back at the first track", func(s music.State) bool { return s.Index == 0 })
	if err := p.SetRepeat(ctx5(t), music.RepeatOff); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "repeat off", func(s music.State) bool { return s.Repeat == music.RepeatOff })
	if err := p.Jump(ctx5(t), 2); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "on the last track again", func(s music.State) bool { return s.Index == 2 })
	if err := p.Next(ctx5(t)); err != ErrEndOfQueue {
		t.Errorf("next on the last track with repeat off: %v, want the end of the queue", err)
	}
}
