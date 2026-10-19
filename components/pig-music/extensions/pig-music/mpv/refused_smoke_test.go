//go:build !windows

package mpv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// refusingServer serves one audio file at /v/<video ID>?n=<resolve>. /v/missingAAAA is not there (404). For the resolves in
// refuse of /v/refusedAAAA it behaves as a stream URL YouTube has stopped honouring part way: the first request gets the first
// chunk and the connection ends there; any request for bytes after the start is refused with 403. Everything else is whole.
type refusingServer struct {
	data   []byte
	chunk  int
	refuse map[string]bool
	t0     time.Time

	mu  sync.Mutex
	log []string
}

func (s *refusingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, n := strings.TrimPrefix(r.URL.Path, "/v/"), r.URL.Query().Get("n")
	start := 0
	if rg := r.Header.Get("Range"); strings.HasPrefix(rg, "bytes=") {
		from, _, _ := strings.Cut(strings.TrimPrefix(rg, "bytes="), "-")
		start, _ = strconv.Atoi(from)
	}
	cut := id == "refusedAAAA" && s.refuse[n]
	status := http.StatusPartialContent
	switch {
	case id == "missingAAAA":
		status = http.StatusNotFound
	case cut && start != 0:
		status = http.StatusForbidden
	}
	s.note(fmt.Sprintf("GET %s n=%s bytes=%d- -> %d", id, n, start, status))
	switch {
	case status != http.StatusPartialContent:
		http.Error(w, "no", status)
	case !cut:
		http.ServeContent(w, r, "a.webm", time.Time{}, strings.NewReader(string(s.data)))
	default:
		w.Header().Set("Content-Type", "audio/webm")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(s.data)-1, len(s.data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(s.data)))
		w.WriteHeader(status)
		_, _ = w.Write(s.data[:min(s.chunk, len(s.data))])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // the connection ends after the first chunk
	}
}

func (s *refusingServer) note(line string) {
	s.mu.Lock()
	s.log = append(s.log, fmt.Sprintf("%5.1fs %s", time.Since(s.t0).Seconds(), line))
	s.mu.Unlock()
}

func (s *refusingServer) timeline() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.log, "\n")
}

// refusedRig is a real mpv with --ao=null on a temporary socket, playing through its yt-dlp hook: a stand-in yt-dlp lists the
// local server's URL for the watch URL's video ID (requested_downloads and uploader make the hook build an EDL, as for YouTube),
// a new URL at each resolve. It needs mpv and ffmpeg on PATH; no network, yt-dlp or sound card.
type refusedRig struct {
	p       *Player
	srv     *refusingServer
	dir     string
	reasons *seenURLs
	forgot  *seenURLs
}

func smokeAudio(t *testing.T) []byte {
	t.Helper()
	if os.Getenv("PIG_MUSIC_SMOKE") != "1" {
		t.Skip("set PIG_MUSIC_SMOKE=1 to run against a real mpv")
	}
	for _, prog := range []string{"mpv", "ffmpeg"} {
		if _, err := exec.LookPath(prog); err != nil {
			t.Fatalf("the smoke test needs %s on PATH", prog)
		}
	}
	audio := filepath.Join(shortDir(t), "tone.webm")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=60", "-c:a", "libopus", "-b:a", "64k", audio).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	data, err := os.ReadFile(audio)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func newRefusedRig(t *testing.T, audio []byte, refuse map[string]bool) *refusedRig {
	t.Helper()
	r := &refusedRig{dir: shortDir(t), srv: &refusingServer{data: audio, chunk: 32 << 10, refuse: refuse, t0: time.Now()}, reasons: &seenURLs{}, forgot: &seenURLs{}}
	hs := httptest.NewServer(r.srv)
	t.Cleanup(hs.Close)
	format := map[string]any{"format_id": "251", "url": hs.URL + "/v/ID?expire=" + strconv.FormatInt(time.Now().Add(6*time.Hour).Unix(), 10) + "&n=N",
		"ext": "webm", "acodec": "opus", "vcodec": "none", "protocol": "http", "asr": 48000, "abr": 64.0}
	listing, _ := json.Marshal(map[string]any{"id": "ID", "title": "ID", "duration": 60, "uploader": "Someone",
		"formats": []any{format}, "requested_downloads": []any{format}})
	if err := os.WriteFile(filepath.Join(r.dir, "listing.json"), listing, 0o600); err != nil {
		t.Fatal(err)
	}
	ytdl := filepath.Join(r.dir, "yt-dlp")
	script := fmt.Sprintf(`#!/bin/sh
for a in "$@"; do last=$a; done
id=${last#*v=}
echo "$id" >> '%[1]s/resolves'
n=$(wc -l < '%[1]s/resolves' | tr -d ' ')
sed "s/ID/$id/g; s/n=N/n=$n/g" '%[1]s/listing.json'
`, r.dir)
	if err := os.WriteFile(ytdl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	paths := music.PathsIn(filepath.Join(r.dir, "run"))
	// No prefetch cache: StreamFailed reports that nothing was kept.
	r.p = New(Config{Paths: paths, YtdlPath: ytdl, ExtraArgs: []string{"--ao=null"}, Recover: true, Unplayable: r.reasons.add,
		StreamFailed: func(u string) bool { r.forgot.add(u); return false },
		PlayURL:      func(tr music.Track) string { return "https://music.youtube.com/watch?v=" + tr.ID }})
	if err := r.p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.p.Shutdown(ctx)
	})
	watch, err := Dial(ctx5(t), paths.Socket, func(ev Event) {
		switch ev.Event {
		case "start-file", "end-file", "seek":
			r.srv.note(strings.TrimSpace(fmt.Sprintf("mpv %s %s entry %d", ev.Event, ev.Reason, ev.PlaylistEntryID)))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watch.Close() })
	return r
}

// resolves is how many times yt-dlp was run for the video ID.
func (r *refusedRig) resolves(id string) int {
	b, _ := os.ReadFile(filepath.Join(r.dir, "resolves"))
	return strings.Count(string(b), id+"\n")
}

// watch polls the position for d and notes where it jumps. A replay is the position going back to the start and then playing
// on from there for more than a second and a half: mpv's loop-file seeks back at once, before the player can act, and the
// moment until it does is not a replay.
func (r *refusedRig) watch(d time.Duration, done func(music.State) bool) (replays int, furthest float64) {
	last, back := -1.0, false
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		st := r.p.State()
		pos := st.Position.Seconds()
		switch {
		case last > 1 && pos < last-1:
			back = true
			r.srv.note(fmt.Sprintf("position went back from %.1f s to %.1f s", last, pos))
		case back && pos-last >= 1:
			back = false
		case back && pos >= 1.5:
			back = false
			replays++
			r.srv.note(fmt.Sprintf("the opening is playing again (at %.1f s)", pos))
		}
		if pos > 0 && last <= 0 || pos-last >= 1 {
			r.srv.note(fmt.Sprintf("playing entry %d from %.1f s", st.Index, pos))
		}
		last, furthest = pos, max(furthest, pos)
		if done != nil && done(st) {
			break
		}
	}
	return replays, furthest
}

// TestRealMpvRefusedStreamSmoke plays one track whose stream the server ends after its first chunk (about 4 s of sound) and
// refuses after, with repeat one and repeat all, as the owner had it when the first seconds of a song played on repeat.
//
//	PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvRefusedStreamSmoke -v
func TestRealMpvRefusedStreamSmoke(t *testing.T) {
	audio := smokeAudio(t)
	for _, c := range []struct {
		name   string
		mode   music.RepeatMode
		refuse map[string]bool // the resolves whose stream is refused after the first chunk
	}{
		{"repeat one, refused at every resolve", music.RepeatOne, map[string]bool{"1": true, "2": true, "3": true}},
		{"repeat all, refused at every resolve", music.RepeatAll, map[string]bool{"1": true, "2": true, "3": true}},
		{"repeat one, refused at the first resolve only", music.RepeatOne, map[string]bool{"1": true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRefusedRig(t, audio, c.refuse)
			if err := r.p.SetRepeat(ctx5(t), c.mode); err != nil {
				t.Fatal(err)
			}
			if err := r.p.Replace(ctx5(t), []music.Track{{ID: "refusedAAAA", Title: "Refused"}}, 0); err != nil {
				t.Fatal(err)
			}
			replays, furthest := r.watch(16*time.Second, nil)
			st := r.p.State()
			t.Logf("timeline:\n%s", r.srv.timeline())
			t.Logf("yt-dlp ran %d times; the opening played again %d times; furthest position %.1f s; reported %q; state index %d of %d, repeat %q",
				r.resolves("refusedAAAA"), replays, furthest, r.reasons.get(), st.Index, len(st.Queue), st.Repeat)
			if replays > 0 {
				t.Errorf("the opening played again %d times", replays)
			}
			if st.Repeat != c.mode {
				t.Errorf("repeat %q, want %q", st.Repeat, c.mode)
			}
			if c.refuse["2"] { // refused after the fresh resolve too: stopped (there is nothing else to play), with a reason
				if got := r.reasons.get(); len(got) != 1 || !strings.Contains(got[0], "Refused") {
					t.Errorf("reported %q", got)
				}
				if st.Index != -1 || len(st.Queue) != 1 || r.resolves("refusedAAAA") != 2 {
					t.Errorf("not stopped after one fresh resolve: index %d of %d, %d resolves", st.Index, len(st.Queue), r.resolves("refusedAAAA"))
				}
				return
			}
			// The fresh resolve is honoured: the track went on from where its stream was cut (3.4 s), past it, with nothing reported.
			if got := r.reasons.get(); len(got) != 0 || furthest < 7 || r.resolves("refusedAAAA") != 2 {
				t.Errorf("did not resume: reported %q, furthest %.1f s, %d resolves", got, furthest, r.resolves("refusedAAAA"))
			}
		})
	}
}

// TestRealMpvRecoveryLeavesOtherTracksAloneSmoke checks what recovery must not do, on a real mpv: take a track that is not
// there (a 404, which a fresh resolve does not mend) for a refusal, and start the reloaded track part way in when it is
// played again.
//
//	PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvRecoveryLeavesOtherTracksAloneSmoke -v
func TestRealMpvRecoveryLeavesOtherTracksAloneSmoke(t *testing.T) {
	audio := smokeAudio(t)
	t.Run("a track that is not there is left to mpv", func(t *testing.T) {
		r := newRefusedRig(t, audio, nil)
		ts := []music.Track{{ID: "goodAAAAAAA", Title: "A"}, {ID: "missingAAAA", Title: "Missing"}, {ID: "goodBBBBBBB", Title: "B"}}
		if err := r.p.Replace(ctx5(t), ts, 1); err != nil {
			t.Fatal(err)
		}
		replays, furthest := r.watch(8*time.Second, nil)
		st := r.p.State()
		t.Logf("timeline:\n%s", r.srv.timeline())
		t.Logf("resolves: missing %d, B %d; B restarted %d times; furthest %.1f s; forgotten %q; reported %q; state index %d of %d",
			r.resolves("missingAAAA"), r.resolves("goodBBBBBBB"), replays, furthest, r.forgot.get(), r.reasons.get(), st.Index, len(st.Queue))
		if st.Index != 2 || len(st.Queue) != 3 || replays > 0 || furthest < 5 || r.resolves("missingAAAA") != 1 || r.resolves("goodBBBBBBB") != 1 || len(r.reasons.get()) != 0 {
			t.Error("the missing track was not simply passed over")
		}
	})
	t.Run("a track loaded again plays from its start the next time", func(t *testing.T) {
		r := newRefusedRig(t, audio, map[string]bool{"1": true})
		ts := []music.Track{{ID: "refusedAAAA", Title: "Refused"}, {ID: "goodBBBBBBB", Title: "B"}}
		if err := r.p.Replace(ctx5(t), ts, 0); err != nil {
			t.Fatal(err)
		}
		if _, furthest := r.watch(16*time.Second, func(st music.State) bool { return st.Index == 0 && st.Position > 6*time.Second }); furthest <= 6 {
			t.Fatalf("not resumed past the cut:\n%s", r.srv.timeline())
		}
		if err := r.p.Jump(ctx5(t), 1); err != nil {
			t.Fatal(err)
		}
		r.watch(8*time.Second, func(st music.State) bool { return st.Index == 1 && st.Position > time.Second })
		if err := r.p.Jump(ctx5(t), 0); err != nil {
			t.Fatal(err)
		}
		r.watch(8*time.Second, func(st music.State) bool { return st.Index == 0 && st.Position > 0 })
		first := r.p.State().Position.Seconds()
		r.watch(time.Second, nil)
		t.Logf("timeline:\n%s", r.srv.timeline())
		t.Logf("played again from %.1f s; resolves %d", first, r.resolves("refusedAAAA"))
		if first > 1.5 {
			t.Errorf("played again from %.1f s, want the start", first)
		}
	})
}

// TestRealMpvTwoPlayersRecoverOnceSmoke attaches a second long-lived Player (a second PiG) to the same real mpv. Both see the
// cut; only the one holding the recovery lease reloads it: the queue keeps its two tracks in order, and the cut one plays on
// from where it stopped.
//
//	PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvTwoPlayersRecoverOnceSmoke -v
func TestRealMpvTwoPlayersRecoverOnceSmoke(t *testing.T) {
	audio := smokeAudio(t)
	for _, mode := range []music.RepeatMode{music.RepeatOff, music.RepeatOne} {
		for round := range 3 {
			t.Run(fmt.Sprintf("repeat %q round %d", mode, round), func(t *testing.T) {
				r := newRefusedRig(t, audio, map[string]bool{"1": true})
				other := New(Config{Paths: r.p.cfg.Paths, YtdlPath: r.p.cfg.YtdlPath, Recover: true, Unplayable: r.reasons.add,
					StreamFailed: func(u string) bool { r.forgot.add(u); return false }, PlayURL: r.p.cfg.PlayURL})
				if err := other.Attach(ctx5(t)); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = other.Close() })
				if err := r.p.SetRepeat(ctx5(t), mode); err != nil {
					t.Fatal(err)
				}
				ts := []music.Track{{ID: "refusedAAAA", Title: "Refused"}, {ID: "goodBBBBBBB", Title: "B"}}
				if err := r.p.Replace(ctx5(t), ts, 0); err != nil {
					t.Fatal(err)
				}
				replays, furthest := r.watch(12*time.Second, func(st music.State) bool { return st.Index == 0 && st.Position > 7*time.Second })
				st := r.p.State()
				var queue []string
				for _, q := range st.Queue {
					queue = append(queue, q.ID)
				}
				t.Logf("timeline:\n%s", r.srv.timeline())
				t.Logf("queue %v, index %d, furthest %.1f s, replays %d, resolves %d, reported %q", queue, st.Index, furthest, replays, r.resolves("refusedAAAA"), r.reasons.get())
				if strings.Join(queue, " ") != "refusedAAAA goodBBBBBBB" || st.Index != 0 || furthest < 7 || replays > 0 || r.resolves("refusedAAAA") != 2 {
					t.Error("not one reload in place, playing on")
				}
			})
		}
	}
}
