package mpv

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

type seenURLs struct {
	mu   sync.Mutex
	urls []string
}

func (s *seenURLs) add(u string) { s.mu.Lock(); s.urls = append(s.urls, u); s.mu.Unlock() }
func (s *seenURLs) get() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.urls...)
}

func prefetchRig(t *testing.T) (*Player, *seenURLs) {
	t.Helper()
	paths := music.PathsIn(shortDir(t))
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	seen := &seenURLs{}
	p := New(Config{Paths: paths, PlayURL: playURL, Prefetch: seen.add})
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, seen
}

func waitSeen(t *testing.T, seen *seenURLs, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := seen.get(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("saw %v, wanted %d", seen.get(), n)
	return nil
}

func TestTheEntryAfterTheCurrentOneIsOfferedForPrefetchAndEachOnceOnly(t *testing.T) {
	p, seen := prefetchRig(t)
	ts := tracks(4)
	if err := p.Replace(ctx5(t), ts, 0); err != nil {
		t.Fatal(err)
	}
	got := waitSeen(t, seen, 1)
	if got[0] != playURL(ts[1]) {
		t.Fatalf("offered %v", got)
	}
	if err := p.Next(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	got = waitSeen(t, seen, 2)
	if got[1] != playURL(ts[2]) {
		t.Fatalf("offered %v", got)
	}
	time.Sleep(150 * time.Millisecond) // position updates and pauses must not offer it again
	if err := p.TogglePause(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if n := len(seen.get()); n != 2 {
		t.Errorf("offered %d times: %v", n, seen.get())
	}
}

func TestTheLastEntryOffersNothing(t *testing.T) {
	p, seen := prefetchRig(t)
	if err := p.Replace(ctx5(t), tracks(1), 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := seen.get(); len(got) != 0 {
		t.Errorf("offered %v", got)
	}
}

// mpv tries to open a watch URL as a plain page first (a 230 ms fetch of the HTML, measured) and only then runs its yt-dlp hook;
// try_ytdl_first goes straight to the hook, which is how every entry of this queue is resolved.
func TestMpvIsToldToRunItsHookFirstAndWhichYtdlpToUse(t *testing.T) {
	p := New(Config{Paths: music.PathsIn(shortDir(t)), YtdlPath: "/x/ytdl"})
	var opts []string
	for _, a := range p.mpvArgs() {
		if len(a) > 14 && a[:14] == "--script-opts=" {
			opts = append(opts, a)
		}
	}
	if len(opts) != 1 || opts[0] != "--script-opts=ytdl_hook-ytdl_path=/x/ytdl,ytdl_hook-try_ytdl_first=yes" {
		t.Errorf("script-opts %v", opts)
	}
	q := New(Config{Paths: music.PathsIn(shortDir(t))})
	for _, a := range q.mpvArgs() {
		if len(a) > 14 && a[:14] == "--script-opts=" && a != "--script-opts=ytdl_hook-try_ytdl_first=yes" {
			t.Errorf("without a path: %s", a)
		}
	}
}

// mpv's key-value list has no backslash escape: "a\,b" reads as the value "a\" and a key "b,...". A path with a comma (or a
// quote or bracket) is passed in mpv's length-prefixed form %n%, which a real mpv 0.37 reads back whole (checked over IPC).
func TestAYtdlPathWithACommaReachesMpvWhole(t *testing.T) {
	p := New(Config{Paths: music.PathsIn(shortDir(t)), YtdlPath: "/home/a,b/ytdl"})
	want := "--script-opts=ytdl_hook-ytdl_path=%14%/home/a,b/ytdl,ytdl_hook-try_ytdl_first=yes"
	found := false
	for _, a := range p.mpvArgs() {
		found = found || a == want
	}
	if !found {
		t.Errorf("args %v lack %s", p.mpvArgs(), want)
	}
}

// A prefetched answer whose stream is refused (the URLs are bound to the IP address that resolved them: a laptop that moved to
// another network, a VPN switched on) must not be answered again from the cache: going back to the track would fail the
// same way until the answer expires. mpv reports the failure as end-file with reason "error"; the player names the entry.
func TestAnEntryThatFailedToOpenIsReported(t *testing.T) {
	paths := music.PathsIn(shortDir(t))
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	failed := &seenURLs{}
	p := New(Config{Paths: paths, PlayURL: playURL, StreamFailed: failed.add})
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ts := tracks(3)
	if err := p.Replace(ctx5(t), ts, 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	srv.FailCurrent()
	if got := waitSeen(t, failed, 1); got[0] != playURL(ts[1]) {
		t.Errorf("reported %v", got)
	}
}
