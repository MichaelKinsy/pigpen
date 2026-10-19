//go:build !windows

package mpv

import (
	"bytes"
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
	"github.com/MichaelKinsy/pigpen/pig-music/prefetch"
)

// streamRig is a real mpv with --ao=null on a temporary socket, playing through pig-music's yt-dlp shim (package prefetch) as
// the extension does: a stand-in yt-dlp lists the local server's URL for the watch URL's video ID (a track of the given
// seconds), with available_at set hold seconds after the resolve, as yt-dlp 2026.10.10 does for a tv_samsung stream with a
// pre-roll ad. The URL carries that second (at=), so that the server can refuse it until then as YouTube does. It needs mpv
// and ffmpeg on PATH; no network or yt-dlp.
type streamRig struct {
	p       *Player
	cache   *prefetch.Cache
	dir     string
	t0      time.Time
	watch   *Client
	reasons *seenURLs

	mu  sync.Mutex
	log []string
}

func (r *streamRig) note(line string) {
	r.mu.Lock()
	r.log = append(r.log, fmt.Sprintf("%5.2fs %s", time.Since(r.t0).Seconds(), line))
	r.mu.Unlock()
}

func (r *streamRig) timeline() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.log, "\n")
}

func (r *streamRig) resolves() int {
	b, _ := os.ReadFile(filepath.Join(r.dir, "resolves"))
	return bytes.Count(b, []byte("\n"))
}

func newStreamRig(t *testing.T, serve func(*streamRig) http.HandlerFunc, seconds, hold int) *streamRig {
	t.Helper()
	r := &streamRig{dir: shortDir(t), t0: time.Now(), reasons: &seenURLs{}}
	hs := httptest.NewServer(serve(r))
	t.Cleanup(hs.Close)
	format := map[string]any{"format_id": "251", "url": hs.URL + "/v/ID?at=AT", "available_at": 1234567890,
		"ext": "webm", "acodec": "opus", "vcodec": "none", "protocol": "http", "asr": 48000, "abr": 64.0}
	listing, _ := json.Marshal(map[string]any{"id": "ID", "title": "ID", "duration": seconds, "uploader": "Someone",
		"formats": []any{format}, "requested_downloads": []any{format}})
	if err := os.WriteFile(filepath.Join(r.dir, "listing.json"), listing, 0o600); err != nil {
		t.Fatal(err)
	}
	ytdl := filepath.Join(r.dir, "yt-dlp")
	script := fmt.Sprintf(`#!/bin/sh
for a in "$@"; do last=$a; done
id=${last#*v=}
echo "$id" >> '%[1]s/resolves'
at=$(($(date +%%s) + %[2]d))
sed "s/ID/$id/g; s/AT/$at/g; s/1234567890/$at/g" '%[1]s/listing.json'
`, r.dir, hold)
	if err := os.WriteFile(ytdl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	r.cache = &prefetch.Cache{Dir: filepath.Join(r.dir, "ytdl"), Bin: ytdl, Runner: func(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		return out, stderr.Bytes(), err
	}}
	shim, err := r.cache.Shim()
	if err != nil {
		t.Fatal(err)
	}
	paths := music.PathsIn(filepath.Join(r.dir, "run"))
	r.p = New(Config{Paths: paths, YtdlPath: shim, ExtraArgs: []string{"--ao=null"}, Recover: true, Unplayable: r.reasons.add,
		StreamFailed: r.cache.Forget, PlayURL: func(tr music.Track) string { return "https://music.youtube.com/watch?v=" + tr.ID }})
	if err := r.p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.p.Shutdown(ctx)
	})
	r.watch, err = Dial(ctx5(t), paths.Socket, func(ev Event) {
		switch ev.Event {
		case "start-file", "end-file", "file-loaded":
			r.note(strings.TrimSpace(fmt.Sprintf("mpv %s %s entry %d", ev.Event, ev.Reason, ev.PlaylistEntryID)))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.watch.Close() })
	return r
}

// cacheAtEnd is whether mpv has read the whole stream into its demuxer cache.
func (r *streamRig) cacheAtEnd(t *testing.T) bool {
	raw, err := r.watch.Command(ctx5(t), "get_property", "demuxer-cache-state")
	var st struct {
		EOF bool `json:"eof"`
	}
	return err == nil && json.Unmarshal(raw, &st) == nil && st.EOF
}

// heldBackServer serves the audio file, and refuses it with 403 before the second its URL names (at=), as googlevideo does
// a tv_samsung stream before its pre-roll ad could have been skipped (R4, measured: 403 before available_at, 206 after).
func heldBackServer(audio []byte) func(*streamRig) http.HandlerFunc {
	return func(r *streamRig) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			at, _ := strconv.ParseInt(req.URL.Query().Get("at"), 10, 64)
			early := time.Now().Unix() < at
			r.note(fmt.Sprintf("GET %s %s, %+.2f s from available_at -> refused %v", req.URL.Path, req.Header.Get("Range"),
				float64(time.Now().UnixMilli()-at*1000)/1000, early))
			if early {
				http.Error(w, "no", http.StatusForbidden)
				return
			}
			http.ServeContent(w, req, "a.webm", time.Time{}, bytes.NewReader(audio))
		}
	}
}

// TestRealMpvWaitsForAvailableAtSmoke plays a track whose stream is refused for 3 s after each resolve, resolved when it
// starts (nothing kept, or recovery's fresh resolve) and prefetched just before it starts. It must play, without a request
// before available_at.
//
//	PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvWaitsForAvailableAtSmoke -v
func TestRealMpvWaitsForAvailableAtSmoke(t *testing.T) {
	audio := smokeAudio(t)
	for _, warm := range []bool{false, true} {
		t.Run(map[bool]string{false: "resolved when it starts", true: "prefetched a moment before"}[warm], func(t *testing.T) {
			r := newStreamRig(t, heldBackServer(audio), 60, 3)
			if warm {
				if err := r.cache.Warm(ctx5(t), "https://music.youtube.com/watch?v=heldAAAAAAA"); err != nil {
					t.Fatal(err)
				}
				r.note("warmed")
			}
			if err := r.p.Replace(ctx5(t), []music.Track{{ID: "heldAAAAAAA", Title: "Held"}}, 0); err != nil {
				t.Fatal(err)
			}
			var furthest float64
			for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline) && furthest < 3; time.Sleep(50 * time.Millisecond) {
				furthest = max(furthest, r.p.State().Position.Seconds())
			}
			t.Logf("timeline:\n%s", r.timeline())
			t.Logf("yt-dlp ran %d times; furthest position %.1f s; reported %q", r.resolves(), furthest, r.reasons.get())
			if furthest < 3 || r.resolves() != 1 || len(r.reasons.get()) != 0 || strings.Contains(r.timeline(), "refused true") {
				t.Error("not played from one resolve, with no request before available_at")
			}
		})
	}
}

// throttlingServer serves bounded ranges of up to 10 MiB at full speed, and anything else (an open-ended range, or no range)
// at 4 KiB/s, ending the connection after 3 s: googlevideo's treatment of a request for more than about 10 MiB (R4, measured:
// about 32 KiB/s, reset after about 700 s), sped up.
func throttlingServer(audio []byte) func(*streamRig) http.HandlerFunc {
	return func(r *streamRig) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			rg := req.Header.Get("Range")
			from, bounded := 0, false
			if spec, ok := strings.CutPrefix(rg, "bytes="); ok {
				a, b, _ := strings.Cut(spec, "-")
				from, _ = strconv.Atoi(a)
				if n, err := strconv.Atoi(b); err == nil {
					bounded = n-from+1 <= 10<<20
				}
			}
			r.note(fmt.Sprintf("GET %q -> throttled %v", rg, !bounded))
			if bounded {
				http.ServeContent(w, req, "a.webm", time.Time{}, bytes.NewReader(audio))
				return
			}
			w.Header().Set("Content-Type", "audio/webm")
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(len(audio)-from))
			status := http.StatusOK
			if rg != "" {
				status = http.StatusPartialContent
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(audio)-1, len(audio)))
			}
			w.WriteHeader(status)
			for end := time.Now().Add(3 * time.Second); time.Now().Before(end) && from < len(audio); time.Sleep(250 * time.Millisecond) {
				n, _ := w.Write(audio[from:min(from+1024, len(audio))])
				from += n
				w.(http.Flusher).Flush()
			}
			panic(http.ErrAbortHandler) // the connection is reset
		}
	}
}

// TestRealMpvReadsTheStreamInRangesSmoke plays an 11 MB track (as long a stream as 11 minutes of YouTube's 130 kbit/s Opus) from
// a server that throttles any request for more than 10 MiB, as googlevideo does. mpv must read the whole stream into its cache
// at once, in bounded ranges.
//
//	PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvReadsTheStreamInRangesSmoke -v
func TestRealMpvReadsTheStreamInRangesSmoke(t *testing.T) {
	smokeAudio(t) // skips without PIG_MUSIC_SMOKE=1
	file := filepath.Join(shortDir(t), "long.webm")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "anoisesrc=duration=200:sample_rate=48000", "-ac", "2", "-c:a", "libopus", "-b:a", "510k", file).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	audio, err := os.ReadFile(file)
	if err != nil || len(audio) <= 10<<20 {
		t.Fatalf("%d bytes, want more than 10 MiB: %v", len(audio), err)
	}
	r := newStreamRig(t, throttlingServer(audio), 200, 0)
	if err := r.p.Replace(ctx5(t), []music.Track{{ID: "longAAAAAAA", Title: "Long"}}, 0); err != nil {
		t.Fatal(err)
	}
	var cached time.Duration
	for start := time.Now(); time.Since(start) < 6*time.Second; time.Sleep(50 * time.Millisecond) {
		if r.cacheAtEnd(t) {
			cached = time.Since(start)
			r.note("the whole stream is in mpv's cache")
			break
		}
	}
	t.Logf("timeline:\n%s", r.timeline())
	t.Logf("%d bytes; whole stream cached after %s (0: not within 6 s)", len(audio), cached)
	if cached == 0 || cached > 3*time.Second || strings.Contains(r.timeline(), "throttled true") {
		t.Error("the stream was not read in full-speed ranges")
	}
}
