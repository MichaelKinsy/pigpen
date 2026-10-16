//go:build !windows

package mpv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/prefetch"
)

// TestLiveYouTubeCanary plays real YouTube tracks to their end through pig-music's own Player, mpv arguments and yt-dlp shim,
// with the yt-dlp named by PIG_MUSIC_CANARY_YTDLP, as fast as mpv can decode (--ao=pcm into a scratch file), and prints one
// CANARY line per track: the client that served the stream (c= of its URL), its format, how long after the resolve YouTube
// served it (available_at), how far it played, the resolves, and the HTTP errors and reconnects in mpv's log.
// It is the daily live canary of R4's test plan; components/pig-music/tests/youtube-canary.sh runs it for the stable and the
// nightly yt-dlp and compares them. It uses the network and no account or cookie.
//
//	PIG_MUSIC_CANARY_YTDLP=$(command -v yt-dlp) go test ./mpv -run LiveYouTubeCanary -count=1 -v
//
// PIG_MUSIC_CANARY_TRACKS overrides the video IDs (default: a short track, one longer than 12 MB, and one Made for Kids);
// PIG_MUSIC_CANARY_JS names the JavaScript runtime the doctor would give yt-dlp (empty: yt-dlp's own choice).
func TestLiveYouTubeCanary(t *testing.T) {
	ytdl := os.Getenv("PIG_MUSIC_CANARY_YTDLP")
	if ytdl == "" {
		t.Skip("set PIG_MUSIC_CANARY_YTDLP to a yt-dlp to play real YouTube tracks with it")
	}
	ids := strings.Fields(os.Getenv("PIG_MUSIC_CANARY_TRACKS"))
	if len(ids) == 0 {
		ids = []string{"dQw4w9WgXcQ", "KBca3xf-j3o", "XqZsoesa55w"}
	}
	js := os.Getenv("PIG_MUSIC_CANARY_JS")
	version, _ := exec.Command(ytdl, "--version").Output()
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			line := canaryPlay(t, ytdl, js, id)
			fmt.Printf("CANARY ytdlp=%s id=%s %s\n", strings.TrimSpace(string(version)), id, line)
		})
	}
}

// canaryPlay plays one track and describes it; a track that does not reach its end fails the test.
func canaryPlay(t *testing.T, ytdl, js, id string) string {
	dir := shortDir(t)
	// The yt-dlp the shim runs records when each resolve ended and keeps its answer.
	wrapper := filepath.Join(dir, "yt-dlp")
	script := fmt.Sprintf(`#!/bin/sh
n=$(ls '%[1]s' | grep -c '^answer-.*json$')
"$0.real" "$@" > '%[1]s/answer-'$n.json
rc=$?
date +%%s > '%[1]s/answer-'$n.at
cat '%[1]s/answer-'$n.json
exit $rc
`, dir)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ytdl, wrapper+".real"); err != nil {
		t.Fatal(err)
	}
	cache := &prefetch.Cache{Dir: filepath.Join(dir, "ytdl"), Bin: wrapper, JSRuntime: js}
	shim, err := cache.Shim()
	if err != nil {
		t.Fatal(err)
	}
	// mpv decodes as fast as the stream arrives into an 8 kHz mono 16-bit file, whose size says how far the stream played (the
	// player's position, reported in real time, lags such a run). Recovery is off: a cut stream shows as a short one.
	pcm := filepath.Join(dir, "out.raw")
	extra := []string{"--ao=pcm", "--ao-pcm-file=" + pcm, "--ao-pcm-waveheader=no", "--audio-format=s16", "--audio-channels=mono",
		"--audio-samplerate=8000"}
	if js != "" {
		extra = append(extra, "--ytdl-raw-options-append=js-runtimes="+js)
	}
	paths := music.PathsIn(filepath.Join(dir, "run"))
	p := New(Config{Paths: paths, YtdlPath: shim, ExtraArgs: extra,
		PlayURL: func(tr music.Track) string { return "https://music.youtube.com/watch?v=" + tr.ID }})
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	}()
	ended := make(chan string, 8)
	watch, err := Dial(ctx5(t), paths.Socket, func(ev Event) {
		if ev.Event == "end-file" {
			ended <- ev.Reason
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	if err := p.Replace(ctx5(t), []music.Track{{ID: id, Title: id}}, 0); err != nil {
		t.Fatal(err)
	}
	// The track has ended when mpv has gone idle.
	reason, started := "timeout", false
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		st := p.State()
		started = started || st.Index >= 0
		for drained := false; !drained; {
			select {
			case reason = <-ended:
			default:
				drained = true
			}
		}
		if started && st.Index < 0 {
			break
		}
	}
	var reached float64
	if st, err := os.Stat(pcm); err == nil {
		reached = float64(st.Size()) / (8000 * 2)
	}
	// The last resolve is the one that played.
	answers, _ := filepath.Glob(filepath.Join(dir, "answer-*.json"))
	var chosen struct {
		Duration  float64 `json:"duration"`
		Requested []struct {
			URL         string  `json:"url"`
			FormatID    string  `json:"format_id"`
			FormatNote  string  `json:"format_note"`
			AvailableAt float64 `json:"available_at"`
		} `json:"requested_downloads"`
	}
	client, format, wait := "?", "?", "?"
	if n := len(answers); n > 0 {
		last := answers[n-1]
		raw, _ := os.ReadFile(last)
		at, _ := os.ReadFile(strings.TrimSuffix(last, ".json") + ".at")
		resolved, _ := strconv.ParseInt(strings.TrimSpace(string(at)), 10, 64)
		if json.Unmarshal(raw, &chosen) == nil && len(chosen.Requested) > 0 {
			r := chosen.Requested[0]
			if u, err := url.Parse(r.URL); err == nil {
				client = u.Query().Get("c")
			}
			format = r.FormatID + "/" + strings.ReplaceAll(r.FormatNote, " ", "_")
			wait = "none"
			if r.AvailableAt > 0 {
				wait = fmt.Sprintf("%+.0fs", r.AvailableAt-float64(resolved))
			}
		}
	}
	log, _ := os.ReadFile(filepath.Join(paths.Dir, "mpv.log"))
	httpErrors := bytes.Count(bytes.ToLower(log), []byte("http error"))
	reconnects := bytes.Count(bytes.ToLower(log), []byte("reconnect"))
	whole := chosen.Duration > 0 && reason == "eof" && reached >= chosen.Duration-cutSlack
	if !whole {
		t.Errorf("%s did not play to its end: %q at %.0f of %.0f s", id, reason, reached, chosen.Duration)
	}
	return fmt.Sprintf("client=%s format=%s available_after_resolve=%s reached=%.0f/%.0fs end=%s resolves=%d http_errors=%d reconnects=%d ok=%v",
		client, format, wait, reached, chosen.Duration, reason, len(answers), httpErrors, reconnects, whole)
}
