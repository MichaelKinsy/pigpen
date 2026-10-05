package mpv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func playURL(t music.Track) string { return "https://music.youtube.com/watch?v=" + t.ID }

func tracks(n int) []music.Track {
	out := make([]music.Track, n)
	for i := range out {
		out[i] = music.Track{ID: fmt.Sprintf("track%05d", i), Title: fmt.Sprintf("Title %d", i), Artists: []string{"Artist"}, Duration: 200 * time.Second}
	}
	return out
}

func ids(ts []music.Track) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

// inProcess starts a fake mpv in this process and returns a player attached to it.
func inProcess(t *testing.T) (*Player, *mpvfake.Server, music.Paths) {
	t.Helper()
	paths := music.PathsIn(shortDir(t))
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	p := New(Config{Paths: paths, PlayURL: playURL})
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, srv, paths
}

func queueIDs(p *Player) []string { return ids(p.State().Queue) }

func waitState(t *testing.T, p *Player, what string, ok func(music.State) bool) music.State {
	t.Helper()
	var st music.State
	waitUntil(t, what, func() bool { st = p.State(); return ok(st) })
	return st
}

func TestAttachReadsTheStateOfARunningMpv(t *testing.T) {
	p, _, _ := inProcess(t)
	st := p.State()
	if !st.Connected || st.Index != -1 || st.Track != nil || len(st.Queue) != 0 || st.Volume != 100 || st.Paused {
		t.Fatalf("idle state = %+v", st)
	}
}

func TestReplaceBuildsTheQueueAroundTheStartTrack(t *testing.T) {
	for _, tc := range []struct{ n, start int }{{1, 0}, {5, 0}, {5, 2}, {5, 4}, {2, 1}} {
		t.Run(fmt.Sprintf("%d from %d", tc.n, tc.start), func(t *testing.T) {
			p, srv, _ := inProcess(t)
			ts := tracks(tc.n)
			if err := p.Replace(ctx5(t), ts, tc.start); err != nil {
				t.Fatal(err)
			}
			st := waitState(t, p, "the queue", func(s music.State) bool { return len(s.Queue) == tc.n && s.Index == tc.start })
			if !reflect.DeepEqual(ids(st.Queue), ids(ts)) {
				t.Fatalf("queue %v, want %v", ids(st.Queue), ids(ts))
			}
			if st.Track == nil || st.Track.ID != ts[tc.start].ID || st.Track.Title != ts[tc.start].Title {
				t.Fatalf("track %+v, want %v", st.Track, ts[tc.start])
			}
			urls, pos := srv.Playlist()
			if pos != tc.start || urls[tc.start] != playURL(ts[tc.start]) {
				t.Fatalf("mpv plays %d of %v", pos, urls)
			}
			// The track that plays is the first thing mpv is told: sound starts at once.
			if first := srv.Commands()[firstLoad(srv.Commands())]; !strings.Contains(first, ts[tc.start].ID) || !strings.Contains(first, "replace") {
				t.Errorf("first load = %s", first)
			}
		})
	}
}

func firstLoad(cmds []string) int {
	for i, c := range cmds {
		if strings.Contains(c, "loadfile") {
			return i
		}
	}
	return 0
}

func TestReplaceRejectsWhatItCannotPlay(t *testing.T) {
	p, srv, _ := inProcess(t)
	for name, call := range map[string]func() error{
		"empty":        func() error { return p.Replace(ctx5(t), nil, 0) },
		"negative":     func() error { return p.Replace(ctx5(t), tracks(2), -1) },
		"past the end": func() error { return p.Replace(ctx5(t), tracks(2), 2) },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(srv.Commands()) > len(propertyNames)+len(propertyNames) {
		t.Errorf("a rejected Replace still reached mpv: %v", srv.Commands())
	}
	noURL := New(Config{Paths: music.PathsIn(shortDir(t)), PlayURL: func(music.Track) string { return "" }})
	noURL.client = &Client{closed: false}
	if err := noURL.Replace(ctx5(t), tracks(1), 0); err == nil || !strings.Contains(err.Error(), "no URL") {
		t.Errorf("empty URL: %v", err)
	}
}

func TestEnqueueAppendsAndStartsWhenIdle(t *testing.T) {
	p, srv, _ := inProcess(t)
	ts := tracks(3)
	if err := p.Enqueue(ctx5(t), ts[0]); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "playing the first", func(s music.State) bool { return s.Index == 0 && len(s.Queue) == 1 })
	if err := p.Enqueue(ctx5(t), ts[1], ts[2]); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, p, "three queued", func(s music.State) bool { return len(s.Queue) == 3 })
	if st.Index != 0 || !slices.Equal(ids(st.Queue), ids(ts)) {
		t.Fatalf("state %+v", st)
	}
	if urls, _ := srv.Playlist(); len(urls) != 3 {
		t.Fatal(urls)
	}
	if err := p.Enqueue(ctx5(t)); err != nil {
		t.Fatalf("enqueue of nothing: %v", err)
	}
}

func TestMoveEndsWithTheEntryWhereTheCallerSaidForEveryPair(t *testing.T) {
	const n = 5
	for from := 0; from < n; from++ {
		for to := 0; to < n; to++ {
			t.Run(fmt.Sprintf("%d to %d", from, to), func(t *testing.T) {
				p, _, _ := inProcess(t)
				ts := tracks(n)
				if err := p.Replace(ctx5(t), ts, 1); err != nil {
					t.Fatal(err)
				}
				waitState(t, p, "queue", func(s music.State) bool { return len(s.Queue) == n })
				want := slices.Clone(ids(ts))
				moved := want[from]
				want = slices.Delete(want, from, from+1)
				want = slices.Insert(want, to, moved)
				if err := p.Move(ctx5(t), from, to); err != nil {
					t.Fatal(err)
				}
				// What plays is still the track that played. mpv reports playlist and
				// playlist-pos as separate events, so wait for both to settle.
				waitState(t, p, "moved, the same track playing", func(s music.State) bool {
					return slices.Equal(ids(s.Queue), want) && s.Track != nil && s.Track.ID == ts[1].ID &&
						s.Index >= 0 && s.Index < len(s.Queue) && s.Queue[s.Index].ID == ts[1].ID
				})
			})
		}
	}
}

func TestRemoveJumpNextPrev(t *testing.T) {
	p, _, _ := inProcess(t)
	ts := tracks(4)
	if err := p.Replace(ctx5(t), ts, 0); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "queue", func(s music.State) bool { return len(s.Queue) == 4 && s.Index == 0 })
	if err := p.Next(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "next", func(s music.State) bool { return s.Index == 1 })
	if err := p.Jump(ctx5(t), 3); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "jump", func(s music.State) bool { return s.Index == 3 && s.Track.ID == ts[3].ID })
	if err := p.Next(ctx5(t)); !errors.Is(err, ErrEndOfQueue) {
		t.Fatalf("Next on the last entry: %v", err)
	}
	if err := p.Prev(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "prev", func(s music.State) bool { return s.Index == 2 })
	if err := p.Remove(ctx5(t), 0); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, p, "removed", func(s music.State) bool { return len(s.Queue) == 3 })
	if st.Index != 1 || st.Track.ID != ts[2].ID || st.Queue[0].ID != ts[1].ID {
		t.Fatalf("after removing the first entry: %+v", st)
	}
	for _, bad := range []func() error{
		func() error { return p.Remove(ctx5(t), 3) },
		func() error { return p.Jump(ctx5(t), -1) },
		func() error { return p.Move(ctx5(t), 0, 9) },
	} {
		if err := bad(); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("out of range: %v", err)
		}
	}
}

func TestPrevRestartsAPlayingTrackAndStopsAtTheFirst(t *testing.T) {
	p, srv, _ := inProcess(t)
	if err := p.Replace(ctx5(t), tracks(3), 1); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "queue", func(s music.State) bool { return len(s.Queue) == 3 && s.Index == 1 })
	srv.Advance(10)
	waitState(t, p, "ten seconds in", func(s music.State) bool { return s.Position == 10*time.Second })
	if err := p.Prev(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, p, "restarted", func(s music.State) bool { return s.Position == 0 })
	if st.Index != 1 {
		t.Fatalf("Prev past the first seconds changed the track: %d", st.Index)
	}
	if err := p.Prev(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "previous", func(s music.State) bool { return s.Index == 0 })
	if err := p.Prev(ctx5(t)); err != nil {
		t.Fatalf("Prev on the first entry: %v", err)
	}
	waitState(t, p, "still first", func(s music.State) bool { return s.Index == 0 })
}

func TestNextAndPrevOnAnIdlePlayerSayNothingPlays(t *testing.T) {
	p, _, _ := inProcess(t)
	for name, call := range map[string]func() error{
		"next": func() error { return p.Next(ctx5(t)) },
		"prev": func() error { return p.Prev(ctx5(t)) },
		"seek": func() error { return p.SeekRelative(ctx5(t), time.Second) },
	} {
		if err := call(); !errors.Is(err, ErrNothingPlaying) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPauseSeekVolume(t *testing.T) {
	p, srv, _ := inProcess(t)
	if err := p.Replace(ctx5(t), tracks(1), 0); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "playing", func(s music.State) bool { return s.Index == 0 && s.Duration == 200*time.Second })
	if err := p.TogglePause(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "paused", func(s music.State) bool { return s.Paused })
	if err := p.SetPaused(ctx5(t), false); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "resumed", func(s music.State) bool { return !s.Paused })
	if err := p.SetPaused(ctx5(t), true); err != nil || !srv.Paused() {
		t.Fatalf("SetPaused(true): %v paused=%v", err, srv.Paused())
	}
	if err := p.SeekRelative(ctx5(t), 30*time.Second); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "seek forward", func(s music.State) bool { return s.Position == 30*time.Second })
	if err := p.SeekRelative(ctx5(t), -45*time.Second); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "seek back clamps at zero", func(s music.State) bool { return s.Position == 0 })
	for in, want := range map[int]int{50: 50, 0: 0, 250: 100, -5: 0} {
		if err := p.SetVolume(ctx5(t), in); err != nil {
			t.Fatal(err)
		}
		waitState(t, p, fmt.Sprintf("volume %d", want), func(s music.State) bool { return s.Volume == want })
	}
}

func TestSubscribeDeliversTheCurrentStateThenLatestChangesAndClosesWithMpv(t *testing.T) {
	p, srv, _ := inProcess(t)
	ch := p.Subscribe()
	first := <-ch
	if !first.Connected || first.Index != -1 {
		t.Fatalf("first state %+v", first)
	}
	if err := p.Replace(ctx5(t), tracks(2), 0); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case st := <-ch:
			if len(st.Queue) == 2 && st.Index == 0 && st.Track != nil {
				goto got
			}
		case <-deadline:
			t.Fatal("no state with the queue arrived")
		}
	}
got:
	// A reader that never reads does not block mpv's events or the player.
	slow := p.Subscribe()
	for i := 0; i < 200; i++ {
		srv.Advance(0.1)
	}
	waitState(t, p, "twenty seconds of ticks", func(s music.State) bool { return s.Position >= 19*time.Second })
	<-slow
	srv.Close()
	closedBy := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-slow:
		case <-closedBy:
			t.Fatal("the subscription did not close when mpv went away")
		}
	}
	if st := p.State(); st.Connected {
		t.Fatalf("still connected: %+v", st)
	}
}

func TestSubscribeBeforeAttachReturnsAClosedChannelWithTheEmptyState(t *testing.T) {
	p := New(Config{Paths: music.PathsIn(shortDir(t))})
	ch := p.Subscribe()
	st, ok := <-ch
	if !ok || st.Connected || st.Index != -1 {
		t.Fatalf("state %+v ok=%v", st, ok)
	}
	if _, ok := <-ch; ok {
		t.Fatal("not closed")
	}
}

func TestCallsBeforeAttachFail(t *testing.T) {
	p := New(Config{Paths: music.PathsIn(shortDir(t)), PlayURL: playURL})
	if err := p.TogglePause(ctx5(t)); !errors.Is(err, ErrNotAttached) {
		t.Fatalf("%v", err)
	}
	if err := p.Replace(ctx5(t), tracks(1), 0); !errors.Is(err, ErrNotAttached) {
		t.Fatalf("%v", err)
	}
	if _, err := p.Sync(ctx5(t)); !errors.Is(err, ErrNotAttached) {
		t.Fatalf("%v", err)
	}
}

func TestAReattachedPlayerNamesTheQueueFromTheMetadataFile(t *testing.T) {
	p, srv, paths := inProcess(t)
	ts := tracks(3)
	if err := p.Replace(ctx5(t), ts, 1); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "queue", func(s music.State) bool { return len(s.Queue) == 3 })
	srv.Advance(42)
	waitState(t, p, "position", func(s music.State) bool { return s.Position == 42*time.Second })
	_ = p.Close() // the "CLI" ends; mpv keeps its queue

	again := New(Config{Paths: paths, PlayURL: playURL})
	if err := again.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	st := again.State()
	if st.Index != 1 || st.Position != 42*time.Second || len(st.Queue) != 3 {
		t.Fatalf("reattached state %+v", st)
	}
	for i, q := range st.Queue {
		if q.Title != ts[i].Title || len(q.Artists) != 1 {
			t.Errorf("queue[%d] = %+v, want the metadata of %+v", i, q, ts[i])
		}
	}
	if st.Track == nil || st.Track.Title != "Title 1" {
		t.Fatalf("track %+v", st.Track)
	}
	info, err := os.Stat(paths.Meta)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("metadata file: %v %v", info, err)
	}
}

func TestATrackTheMetadataFileLacksIsNamedByItsID(t *testing.T) {
	p, srv, paths := inProcess(t)
	if err := p.Replace(ctx5(t), tracks(2), 0); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "queue", func(s music.State) bool { return len(s.Queue) == 2 })
	_ = p.Close()
	if err := os.WriteFile(paths.Meta, []byte("{damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	again := New(Config{Paths: paths, PlayURL: playURL})
	if err := again.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	st := again.State()
	if len(st.Queue) != 2 || st.Queue[1].ID != "track00001" || st.Queue[1].Title != "track00001" {
		t.Fatalf("queue %+v", st.Queue)
	}
	_ = srv
}

func TestSyncRefreshesWhatEventsMissed(t *testing.T) {
	p, srv, _ := inProcess(t)
	if err := p.Replace(ctx5(t), tracks(1), 0); err != nil {
		t.Fatal(err)
	}
	srv.Advance(7)
	st, err := p.Sync(ctx5(t))
	if err != nil || st.Position != 7*time.Second {
		t.Fatalf("Sync = %+v, %v", st, err)
	}
}

func TestMetadataIsPrunedPastItsCap(t *testing.T) {
	p, _, paths := inProcess(t)
	big := tracks(maxMeta + 10)
	if err := p.Replace(ctx5(t), big[:3], 0); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "queue", func(s music.State) bool { return len(s.Queue) == 3 })
	p.mu.Lock()
	for _, t := range big { // pretend a long history
		p.meta[t.ID] = t
	}
	p.mu.Unlock()
	if err := p.remember(big[:1], []string{playURL(big[0])}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(paths.Meta)
	var saved map[string]music.Track
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) > 4 || saved[big[0].ID].Title == "" || saved[big[1].ID].Title == "" {
		t.Fatalf("saved %d entries", len(saved))
	}
}

func TestSeekWhileTheTrackIsStillLoadingSaysSo(t *testing.T) {
	// mpv reports no duration until the stream has opened; the player must not send a seek it knows will fail.
	paths := music.PathsIn(shortDir(t))
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{Duration: func(string) float64 { return 0 }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	p := New(Config{Paths: paths, PlayURL: playURL})
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Replace(ctx5(t), tracks(1), 0); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "playing", func(s music.State) bool { return s.Index == 0 })
	before := len(srv.Commands())
	if err := p.SeekRelative(ctx5(t), 5*time.Second); !errors.Is(err, ErrNotSeekable) {
		t.Fatalf("err = %v", err)
	}
	if len(srv.Commands()) != before {
		t.Error("a seek was sent to mpv anyway")
	}
}
