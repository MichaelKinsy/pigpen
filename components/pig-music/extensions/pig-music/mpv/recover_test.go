package mpv

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// A stream YouTube refuses reaches mpv in one of two ways (measured with a real mpv 0.41 against a server that refuses every
// byte after the first chunk, refused_smoke_test.go): the entry cannot be opened (end-file, reason "error"), or the stream
// simply ends where the refusal began and mpv takes that for the end of the track: end-file "eof" long before the track's
// length, or, with repeat one (loop-file), a seek back to its start, which plays the opening again and again.

type recoverRig struct {
	p          *Player
	srv        *mpvfake.Server
	forgot     *forgetter
	unplayable *seenURLs
}

// forgetter is the prefetch cache as StreamFailed sees it: a URL had a kept answer (kept) until it was forgotten once.
type forgetter struct {
	seenURLs
	kept bool
}

func (f *forgetter) forget(u string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	first := true
	for _, seen := range f.urls {
		first = first && seen != u
	}
	f.urls = append(f.urls, u)
	return f.kept && first
}

func newRecoverRig(t *testing.T, opts ...mpvfake.Options) recoverRig {
	t.Helper()
	paths := music.PathsIn(shortDir(t))
	srv, err := mpvfake.Listen(paths.Socket, append(opts, mpvfake.Options{})[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	r := recoverRig{srv: srv, forgot: &forgetter{kept: true}, unplayable: &seenURLs{}}
	r.p = New(Config{Paths: paths, PlayURL: playURL, Recover: true, StreamFailed: r.forgot.forget, Unplayable: r.unplayable.add})
	if err := r.p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.p.Close() })
	return r
}

// play makes ts the queue, playing ts[at] from seconds in, as the player has heard it.
func (r recoverRig) play(t *testing.T, ts []music.Track, at int, seconds float64) {
	t.Helper()
	if err := r.p.Replace(ctx5(t), ts, at); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the start track to play", func() bool { return r.p.State().Index == at })
	r.srv.Advance(seconds)
	waitUntil(t, "the position to be heard", func() bool { return r.p.State().Position >= time.Duration(seconds*float64(time.Second)) })
}

func (r recoverRig) loads() int {
	n := 0
	for _, c := range r.srv.Commands() {
		if strings.HasPrefix(c, `["loadfile"`) {
			n++
		}
	}
	return n
}

// reloaded waits until the entry at index has an ID other than was, the queue being urls in the same order.
func (r recoverRig) reloaded(t *testing.T, index, was int, urls []string) {
	t.Helper()
	waitUntil(t, "the entry to be loaded again in its place", func() bool {
		got, pos := r.srv.Playlist()
		ids := r.srv.IDs()
		return pos == index && strings.Join(got, " ") == strings.Join(urls, " ") && ids[index] != was
	})
}

func urlsOf(ts []music.Track) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = playURL(t)
	}
	return out
}

func TestARefusedStreamIsLoadedAgainInItsPlaceAndResumesWhereItStopped(t *testing.T) {
	for name, c := range map[string]struct {
		paused bool
		fail   func(*mpvfake.Server)
	}{
		"refused at open":         {fail: (*mpvfake.Server).FailCurrent},
		"refused at open, paused": {paused: true, fail: (*mpvfake.Server).FailCurrent},
		"cut short, repeat off":   {fail: func(s *mpvfake.Server) { s.CutCurrent(42) }},
		"cut short, repeat queue": {fail: func(s *mpvfake.Server) { s.CutCurrent(42) }},
		"cut short, repeat track": {fail: func(s *mpvfake.Server) { s.CutCurrent(42) }},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRecoverRig(t)
			mode := music.RepeatOff
			switch {
			case strings.HasSuffix(name, "repeat queue"):
				mode = music.RepeatAll
			case strings.HasSuffix(name, "repeat track"):
				mode = music.RepeatOne
			}
			if err := r.p.SetRepeat(ctx5(t), mode); err != nil {
				t.Fatal(err)
			}
			ts := tracks(3)
			r.play(t, ts, 1, 42)
			if c.paused {
				if err := r.p.SetPaused(ctx5(t), true); err != nil {
					t.Fatal(err)
				}
				waitUntil(t, "pause", func() bool { return r.p.State().Paused })
			}
			was := r.srv.IDs()
			c.fail(r.srv)
			r.reloaded(t, 1, was[1], urlsOf(ts))
			if ids := r.srv.IDs(); ids[0] != was[0] || ids[2] != was[2] {
				t.Errorf("other entries were touched: %v, were %v", ids, was)
			}
			waitUntil(t, "the position to be where the stream stopped", func() bool { return r.srv.TimePos() == 42 })
			if got := r.forgot.get(); len(got) != 1 || got[0] != playURL(ts[1]) {
				t.Errorf("forgotten %v", got)
			}
			waitUntil(t, "the pause state to be kept", func() bool { return r.srv.Paused() == c.paused })
			loops := map[music.RepeatMode][2]string{music.RepeatOff: {"no", "no"}, music.RepeatAll: {"inf", "no"}, music.RepeatOne: {"no", "inf"}}
			if got := r.srv.Loop(); got != loops[mode] {
				t.Errorf("repeat changed: %v", got)
			}
			if got := r.unplayable.get(); len(got) != 0 {
				t.Errorf("reported %v", got)
			}
		})
	}
}

func TestAStreamRefusedAgainSoonAfterItWasLoadedAgainIsSkippedWithAReason(t *testing.T) {
	r := newRecoverRig(t)
	ts := tracks(3)
	r.play(t, ts, 1, 10)
	was := r.srv.IDs()
	r.srv.FailCurrent()
	r.reloaded(t, 1, was[1], urlsOf(ts))
	loads := r.loads()
	r.srv.FailCurrent() // mpv goes on to the next entry
	reasons := waitSeen(t, r.unplayable, 1)
	if !strings.Contains(reasons[0], "Title 1") || strings.Contains(reasons[0], "\n") {
		t.Errorf("reason %q", reasons[0])
	}
	// Unplayable is called once the player has decided: nothing more is loaded after it.
	if n := r.loads(); n != loads {
		t.Errorf("loaded again: %d loadfile commands, were %d", n, loads)
	}
	if _, pos := r.srv.Playlist(); pos != 2 {
		t.Errorf("playing entry %d, want the next one (2)", pos)
	}
}

// With repeat one, mpv itself plays the opening of a cut stream again and again (loop-file seeks back to the start), and with
// repeat all and one track it loads the same entry again and again: neither may go on.
func TestRepeatDoesNotLoopAnEntryWhoseStreamKeepsBeingCut(t *testing.T) {
	for _, mode := range []music.RepeatMode{music.RepeatOne, music.RepeatAll} {
		t.Run(string(mode), func(t *testing.T) {
			r := newRecoverRig(t)
			if err := r.p.SetRepeat(ctx5(t), mode); err != nil {
				t.Fatal(err)
			}
			ts := tracks(1)
			r.play(t, ts, 0, 3.3)
			was := r.srv.IDs()
			r.srv.CutCurrent(3.3)
			r.reloaded(t, 0, was[0], urlsOf(ts))
			waitUntil(t, "the position to be where the stream stopped", func() bool { return r.srv.TimePos() == 3.3 })
			loads := r.loads()
			r.srv.CutCurrent(4)
			reasons := waitSeen(t, r.unplayable, 1)
			if !strings.Contains(reasons[0], "Title 0") {
				t.Errorf("reason %q", reasons[0])
			}
			waitUntil(t, "playback to stop", func() bool { _, pos := r.srv.Playlist(); return pos == -1 })
			if urls, pos := r.srv.Playlist(); pos != -1 || len(urls) != 1 || r.loads() != loads {
				t.Errorf("after the second cut: queue %v at %d, %d loads (were %d)", urls, pos, r.loads(), loads)
			}
			if got := r.p.State().Repeat; got != mode {
				t.Errorf("repeat %q, want %q", got, mode)
			}
		})
	}
}

// A track that ends at its length is not a refusal. ended decides on the end-file event, before the events of the next track
// are read, so the next track playing means it has decided.
func TestATrackThatEndsAtItsLengthIsNotLoadedAgain(t *testing.T) {
	r := newRecoverRig(t)
	ts := tracks(2)
	r.play(t, ts, 0, 150)
	r.srv.CutCurrent(mpvfake.DefaultDuration - 0.5)
	waitUntil(t, "the next track", func() bool { return r.p.State().Index == 1 })
	if n := r.loads(); n != 2 {
		t.Errorf("%d loadfile commands, want the 2 of Replace", n)
	}
	if got := r.forgot.get(); len(got) != 0 {
		t.Errorf("forgotten %v", got)
	}
}

// mpv keeps the options loadfile was given with the entry, for every play of it: a resume position given that way would start
// the track part way in at its next play (the next round of repeat all, a jump back to it).
func TestAnEntryLoadedAgainPlaysFromItsStartWhenItIsPlayedAgain(t *testing.T) {
	r := newRecoverRig(t)
	ts := tracks(3)
	r.play(t, ts, 1, 42)
	was := r.srv.IDs()
	r.srv.FailCurrent()
	r.reloaded(t, 1, was[1], urlsOf(ts))
	waitUntil(t, "the position to be where the stream stopped", func() bool { return r.srv.TimePos() == 42 })
	if err := r.p.Jump(ctx5(t), 2); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the next track", func() bool { return r.p.State().Index == 2 })
	if err := r.p.Jump(ctx5(t), 1); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the reloaded track again", func() bool { return r.p.State().Index == 1 })
	time.Sleep(100 * time.Millisecond) // a resume, wrongly made again, would seek now
	if got := r.srv.TimePos(); got != 0 {
		t.Errorf("played again from %v s, want the start", got)
	}
}

// A track that cannot be opened for another reason than a refused prefetched answer (removed, blocked in the country, a 404) is
// not helped by resolving it again: mpv goes on to the next track as before, which is not restarted, and nothing is reported.
func TestAnEntryThatCannotBeOpenedAndWasNotPrefetchedIsLeftToMpv(t *testing.T) {
	r := newRecoverRig(t)
	r.forgot.kept = false
	ts := tracks(3)
	r.play(t, ts, 1, 0)
	was := r.srv.IDs()
	r.srv.FailCurrent()
	waitSeen(t, &r.forgot.seenURLs, 1)
	time.Sleep(150 * time.Millisecond) // nothing more may happen: no event follows to wait for
	if ids, pos := r.srv.IDs(), r.p.State().Index; !slices.Equal(ids, was) || pos != 2 || r.loads() != 3 {
		t.Errorf("queue %v (was %v) at %d, %d loads", ids, was, pos, r.loads())
	}
	if got := r.unplayable.get(); len(got) != 0 {
		t.Errorf("reported %v", got)
	}
}

// Two long-lived players on one mpv (two PiGs) both see the refusal, at the same moment. Only one may load it again (the one
// holding the recovery lease in mpv): one reload, in the refused entry's place, playing, and no other entry touched.
func TestTwoPlayersOnOneMpvLoadARefusedEntryAgainOnce(t *testing.T) {
	for name, c := range map[string]struct {
		mode music.RepeatMode
		fail func(*mpvfake.Server)
	}{
		"refused at open":         {fail: (*mpvfake.Server).FailCurrent},
		"cut short, repeat off":   {fail: func(s *mpvfake.Server) { s.CutCurrent(42) }},
		"cut short, repeat track": {mode: music.RepeatOne, fail: func(s *mpvfake.Server) { s.CutCurrent(42) }},
	} {
		t.Run(name, func(t *testing.T) {
			for round := range 20 {
				r := newRecoverRig(t)
				other := New(Config{Paths: r.p.cfg.Paths, PlayURL: playURL, Recover: true, StreamFailed: r.forgot.forget, Unplayable: r.unplayable.add})
				if err := other.Attach(ctx5(t)); err != nil {
					t.Fatal(err)
				}
				if err := r.p.SetRepeat(ctx5(t), c.mode); err != nil {
					t.Fatal(err)
				}
				ts := tracks(3)
				r.play(t, ts, 1, 42)
				waitUntil(t, "the other player to hear it", func() bool { return other.State().Position > 41*time.Second })
				was := r.srv.IDs()
				c.fail(r.srv)
				r.reloaded(t, 1, was[1], urlsOf(ts))
				time.Sleep(100 * time.Millisecond) // the other player's turn: nothing more may change
				urls, pos := r.srv.Playlist()
				ids := r.srv.IDs()
				if strings.Join(urls, " ") != strings.Join(urlsOf(ts), " ") || pos != 1 || ids[0] != was[0] || ids[2] != was[2] || r.srv.TimePos() != 42 {
					t.Fatalf("round %d: queue %v ids %v (were %v) playing %d at %v s, want %v playing 1 at 42 s", round, urls, ids, was, pos, r.srv.TimePos(), urlsOf(ts))
				}
				_ = other.Close()
			}
		})
	}
}

// The player that holds the lease gives it up when it closes (a /reload, a PiG that quits): the other one takes it, and
// recovers from then on.
func TestWhenThePlayerHoldingTheLeaseClosesAnotherRecovers(t *testing.T) {
	r := newRecoverRig(t)
	other := New(Config{Paths: r.p.cfg.Paths, PlayURL: playURL, Recover: true, StreamFailed: r.forgot.forget, Unplayable: r.unplayable.add})
	if err := other.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	ts := tracks(3)
	r.play(t, ts, 1, 42)
	_ = r.p.Close()
	waitUntil(t, "the other player to hold the lease", func() bool { return other.holdsLease() })
	was := r.srv.IDs()
	r.srv.CutCurrent(42)
	r.reloaded(t, 1, was[1], urlsOf(ts))
}

// mpv 0.38 and later take an index with loadfile: the reload is inserted in the refused entry's place in one step, so that
// nothing is left at the end of the queue if the connection ends half way.
func TestOnMpv038TheReloadIsInsertedInItsPlace(t *testing.T) {
	r := newRecoverRig(t, mpvfake.Options{Version: "mpv v0.41.0"})
	ts := tracks(3)
	r.play(t, ts, 1, 42)
	was, before := r.srv.IDs(), len(r.srv.Commands())
	r.srv.FailCurrent()
	r.reloaded(t, 1, was[1], urlsOf(ts))
	inserted, moved := false, false
	recovery := r.srv.Commands()[before:]
	for _, c := range recovery {
		inserted = inserted || strings.HasPrefix(c, `["loadfile","`+playURL(ts[1])+`","insert-at",1`)
		moved = moved || strings.HasPrefix(c, `["playlist-move"`)
	}
	if !inserted || moved {
		t.Errorf("inserted in place %v, moved %v: %v", inserted, moved, recovery)
	}
}

// A seek in a stream whose demuxer is at an early end is not a refusal unless playback was at that end: a person seeking in
// a stream that is still loading is not.
func TestASeekAwayFromWhereTheStreamEndsIsNotARefusal(t *testing.T) {
	r := newRecoverRig(t)
	ts := tracks(2)
	r.play(t, ts, 0, 10)
	r.srv.CutAhead(42)
	if err := r.p.SeekRelative(ctx5(t), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // nothing more may happen: no event follows to wait for
	if n := r.loads(); n != 2 {
		t.Errorf("%d loadfile commands, want the 2 of Replace", n)
	}
}

// mpv leaves cache-end out when it has no timestamp for it: that says nothing about where the stream ends.
func TestADemuxerStateWithoutCacheEndIsNotARefusal(t *testing.T) {
	paths := music.PathsIn(shortDir(t))
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{NoCacheEnd: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	p := New(Config{Paths: paths, PlayURL: playURL, Recover: true})
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	r := recoverRig{p: p, srv: srv}
	if err := p.SetRepeat(ctx5(t), music.RepeatOne); err != nil {
		t.Fatal(err)
	}
	r.play(t, tracks(1), 0, 3.3)
	srv.CutCurrent(3.3)
	time.Sleep(150 * time.Millisecond) // nothing more may happen: no event follows to wait for
	if n := r.loads(); n != 1 {
		t.Errorf("%d loadfile commands, want the 1 of Replace", n)
	}
}

// A PiG that holds the lease and is killed leaves it in mpv. A Player already attached beside it takes it over at the next
// refusal, when the process named in it has ended, and recovers.
func TestALeaseLeftByAKilledHolderIsTakenOverAtTheNextRefusal(t *testing.T) {
	r := newRecoverRig(t)
	other := New(Config{Paths: r.p.cfg.Paths, PlayURL: playURL, Recover: true, StreamFailed: r.forgot.forget, Unplayable: r.unplayable.add})
	if err := other.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	ts := tracks(3)
	r.play(t, ts, 1, 42)
	if err := r.p.command(ctx5(t), "set_property", leaseProp, "2147483646:1:1"); err != nil { // a holder whose process has ended
		t.Fatal(err)
	}
	waitUntil(t, "the other player to hear who holds the lease", func() bool {
		other.mu.Lock()
		defer other.mu.Unlock()
		return other.lease == "2147483646:1:1"
	})
	c, err := r.p.conn()
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close() // killed: the connection ends, and nothing gives the lease up
	was := r.srv.IDs()
	r.srv.CutCurrent(42)
	r.reloaded(t, 1, was[1], urlsOf(ts))
}

// The same song twice in a row, the second one playing (Replace gives the entries before the start track the higher IDs): its
// cut stream is loaded again like any other, not taken for a reload another client made of it.
func TestTheSecondOfTwoAdjacentCopiesOfASongIsLoadedAgain(t *testing.T) {
	r := newRecoverRig(t)
	if err := r.p.SetRepeat(ctx5(t), music.RepeatOne); err != nil {
		t.Fatal(err)
	}
	ts := []music.Track{tracks(1)[0], tracks(1)[0]}
	r.play(t, ts, 1, 3.3)
	was := r.srv.IDs()
	r.srv.CutCurrent(3.3)
	r.reloaded(t, 1, was[1], urlsOf(ts))
	if ids := r.srv.IDs(); ids[0] != was[0] || len(ids) != 2 || r.loads() != 3 {
		t.Errorf("ids %v (were %v), %d loads", ids, was, r.loads())
	}
}

// A PiG that was killed never gave the lease up: a Player that attaches takes it over when the process named in it has ended,
// and leaves it alone while that process runs.
func TestALeaseLeftByAProcessThatEndedIsTakenOver(t *testing.T) {
	for name, c := range map[string]struct {
		holder string
		takes  bool
	}{
		"ended":   {holder: "2147483646:1:1", takes: true},
		"running": {holder: strconv.Itoa(os.Getppid()) + ":1:1", takes: false},
	} {
		t.Run(name, func(t *testing.T) {
			paths := music.PathsIn(shortDir(t))
			srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.Close)
			setter := New(Config{Paths: paths})
			if err := setter.Attach(ctx5(t)); err != nil {
				t.Fatal(err)
			}
			if err := setter.command(ctx5(t), "set_property", leaseProp, c.holder); err != nil {
				t.Fatal(err)
			}
			_ = setter.Close()
			p := New(Config{Paths: paths, PlayURL: playURL, Recover: true})
			if err := p.Attach(ctx5(t)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = p.Close() })
			if c.takes {
				waitUntil(t, "the lease to be taken over", p.holdsLease)
			} else if time.Sleep(100 * time.Millisecond); p.holdsLease() {
				t.Error("took the lease of a running process")
			}
		})
	}
}
