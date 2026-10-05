//go:build !windows

package prefetch_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/prefetch"
	"github.com/MichaelKinsy/pigpen/pig-music/ytdlp"
)

// TestGapBetweenTwoTracks measures the silence between two real tracks with a real mpv (--ao=null) and a real yt-dlp, with and
// without prefetch. It needs the network and plays two public videos; PIG_MUSIC_SMOKE=1 turns it on.
//
//	PIG_MUSIC_SMOKE=1 go test ./prefetch -run GapBetween -v
func TestGapBetweenTwoTracks(t *testing.T) {
	if os.Getenv("PIG_MUSIC_SMOKE") != "1" {
		t.Skip("set PIG_MUSIC_SMOKE=1 to run against a real mpv, yt-dlp and the network")
	}
	yt, err := exec.LookPath("yt-dlp")
	if err != nil {
		t.Skip("needs yt-dlp")
	}
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("needs mpv")
	}
	for _, withPrefetch := range []bool{false, true} {
		name := map[bool]string{false: "plain", true: "prefetch"}[withPrefetch]
		t.Run(name, func(t *testing.T) {
			dir, _ := os.MkdirTemp("", "pm")
			defer os.RemoveAll(dir)
			defer func() {
				if t.Failed() {
					log, _ := os.ReadFile(dir + "/mpv.log")
					t.Logf("mpv.log:\n%s", log)
				}
			}()
			paths := music.PathsIn(dir)
			cfg := mpv.Config{Paths: paths, YtdlPath: yt, ExtraArgs: append([]string{"--ao=null"}, strings.Fields(os.Getenv("PIG_MUSIC_MPV_ARGS"))...),
				PlayURL: func(tr music.Track) string { return "https://music.youtube.com/watch?v=" + tr.ID }}
			cache := &prefetch.Cache{Dir: dir + "/ytdl", Bin: yt, Runner: ytdlp.ExecRunner}
			if withPrefetch {
				shim, err := cache.Shim()
				if err != nil {
					t.Fatal(err)
				}
				cfg.YtdlPath = shim
				cfg.Prefetch = func(u string) { _ = cache.Warm(context.Background(), u) }
			}
			p := mpv.New(cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
			defer cancel()
			if err := p.Attach(ctx); err != nil {
				t.Fatal(err)
			}
			defer p.Shutdown(context.Background())
			sub := p.Subscribe()
			tracks := []music.Track{{ID: "fOT0BUpITw8"}, {ID: "jWdxXdp8sZI"}}
			if err := p.Replace(ctx, tracks, 0); err != nil {
				t.Fatal(err)
			}
			wait := func(what string, ok func(music.State) bool) time.Time {
				for {
					select {
					case st, open := <-sub:
						if !open {
							t.Fatalf("closed waiting for %s", what)
						}
						if ok(st) {
							return time.Now()
						}
					case <-ctx.Done():
						t.Fatalf("timed out waiting for %s", what)
					}
				}
			}
			wait("the first track plays", func(s music.State) bool { return s.Index == 0 && s.Duration > 0 && s.Position > 0 })
			if withPrefetch {
				for i := 0; i < 120 && !cache.Has("https://music.youtube.com/watch?v="+tracks[1].ID); i++ {
					time.Sleep(500 * time.Millisecond)
				}
				if !cache.Has("https://music.youtube.com/watch?v=" + tracks[1].ID) {
					t.Fatal("the next track was not prefetched")
				}
			} else {
				time.Sleep(8 * time.Second)
			}
			if err := p.SeekRelative(ctx, 100*time.Hour); err != nil { // to the end: mpv then moves on
				// seeking past the end is clamped by mpv; fall back to a late absolute seek
				t.Logf("seek: %v", err)
			}
			start := wait("the second track is loading", func(s music.State) bool { return s.Index == 1 })
			end := wait("the second track plays", func(s music.State) bool { return s.Index == 1 && s.Position > 0 })
			t.Logf("%s: gap %v", strings.ToUpper(name), end.Sub(start).Round(10*time.Millisecond))
		})
	}
}
