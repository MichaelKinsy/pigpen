package mpv

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func TestRepeatModesAreMpvsLoopProperties(t *testing.T) {
	p, srv, _ := inProcess(t)
	if st := p.State(); st.Repeat != music.RepeatOff || st.Shuffle {
		t.Fatalf("a new queue starts with no repeat and no shuffle: %+v", st)
	}
	for _, tc := range []struct {
		mode          music.RepeatMode
		playlist, one string
	}{{music.RepeatAll, "inf", "no"}, {music.RepeatOne, "no", "inf"}, {music.RepeatOff, "no", "no"}} {
		if err := p.SetRepeat(ctx5(t), tc.mode); err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		waitState(t, p, "repeat "+string(tc.mode), func(s music.State) bool { return s.Repeat == tc.mode })
		if got := srv.Loop(); got != [2]string{tc.playlist, tc.one} {
			t.Errorf("%s: loop-playlist/loop-file are %v", tc.mode, got)
		}
	}
	if err := p.SetRepeat(ctx5(t), "forever"); err == nil || !strings.Contains(err.Error(), "forever") {
		t.Errorf("an unknown mode: %v", err)
	}
}

func TestShuffleKeepsTheCurrentTrackAndUnshuffleRestoresTheOrder(t *testing.T) {
	p, srv, _ := inProcess(t)
	ts := tracks(5)
	if err := p.Replace(ctx5(t), ts, 2); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "playing", func(s music.State) bool { return len(s.Queue) == 5 && s.Index == 2 })
	if err := p.SetShuffle(ctx5(t), true); err != nil {
		t.Fatal(err)
	}
	// mpv's events for the new order may arrive after the command's answer: wait for the order to change.
	st := waitState(t, p, "shuffled", func(s music.State) bool {
		return s.Shuffle && strings.Join(ids(s.Queue), ",") != strings.Join(ids(ts), ",")
	})
	if st.Track == nil || st.Track.ID != ts[2].ID {
		t.Errorf("shuffling changed the current track: %+v", st.Track)
	}
	if strings.Join(ids(st.Queue), ",") == strings.Join(ids(ts), ",") {
		t.Errorf("the queue was not reordered: %v", ids(st.Queue))
	}
	for _, q := range st.Queue {
		if q.Title == q.ID {
			t.Errorf("a shuffled entry lost its title: %+v", q)
		}
	}
	if !srv.Shuffled() {
		t.Error("mpv was not told to shuffle")
	}
	if err := p.SetShuffle(ctx5(t), false); err != nil {
		t.Fatal(err)
	}
	// as when shuffling, the restored order may arrive after the flag: wait for both (a timeout here is the failure)
	waitState(t, p, "unshuffled and the order back", func(s music.State) bool {
		return !s.Shuffle && strings.Join(ids(s.Queue), ",") == strings.Join(ids(ts), ",")
	})
}

func TestANewQueueIsNotShuffled(t *testing.T) {
	p, _, _ := inProcess(t)
	if err := p.Replace(ctx5(t), tracks(4), 0); err != nil {
		t.Fatal(err)
	}
	if err := p.SetShuffle(ctx5(t), true); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "shuffled", func(s music.State) bool { return s.Shuffle })
	if err := p.Replace(ctx5(t), tracks(3), 0); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "a new queue", func(s music.State) bool { return len(s.Queue) == 3 && !s.Shuffle })
}

func TestUnshuffleNeedsMpv037AndTheErrorSaysSo(t *testing.T) {
	paths := music.PathsIn(shortDir(t))
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{NoUnshuffle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	p := New(Config{Paths: paths, PlayURL: playURL})
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if err := p.Replace(ctx5(t), tracks(4), 0); err != nil {
		t.Fatal(err)
	}
	if err := p.SetShuffle(ctx5(t), true); err != nil {
		t.Fatal(err)
	}
	err = p.SetShuffle(ctx5(t), false)
	if err == nil || !strings.Contains(err.Error(), "0.37") {
		t.Fatalf("%v", err)
	}
	if !p.State().Shuffle {
		t.Error("a failed unshuffle cleared the flag")
	}
}

var _ music.Modes = (*Player)(nil)
