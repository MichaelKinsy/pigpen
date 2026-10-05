package prefetch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const answer = `{"id":"fOT0BUpITw8","title":"x","formats":[{"url":"https://rr1.googlevideo.com/videoplayback?id=abc","format_id":"251"}]}`

type fakeRun struct {
	mu    sync.Mutex
	calls [][]string
	out   string
	err   error
	wait  chan struct{}
}

func (f *fakeRun) run(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{bin}, args...))
	f.mu.Unlock()
	if f.wait != nil {
		select {
		case <-f.wait:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	return []byte(f.out), nil, f.err
}

func (f *fakeRun) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func newCache(t *testing.T, f *fakeRun) *Cache {
	return &Cache{Dir: filepath.Join(t.TempDir(), "ytdl"), Bin: "yt-dlp", Runner: f.run}
}

func TestIDIsTheVideoIDOfAWatchURLAndNothingElse(t *testing.T) {
	for url, want := range map[string]string{
		"https://music.youtube.com/watch?v=fOT0BUpITw8":        "fOT0BUpITw8",
		"https://www.youtube.com/watch?v=abc_-123456&list=PL1": "abc_-123456",
		"https://example.com/watch?v=abc":                      "",
		"https://music.youtube.com/watch?v=../../etc":          "",
		"https://music.youtube.com/watch?v=":                   "",
		"not a url":                                            "",
	} {
		if got := ID(url); got != want {
			t.Errorf("%s: %q, want %q", url, got, want)
		}
	}
}

func TestWarmRunsTheSameCommandMpvsHookRunsAndKeepsItsAnswer(t *testing.T) {
	f := &fakeRun{out: answer}
	c := newCache(t, f)
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	if err := c.Warm(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.calls[0], " ")
	for _, want := range []string{"-J", "--format bestaudio", "--no-playlist", "--ignore-config", "-- " + url} {
		if !strings.Contains(got, want) {
			t.Errorf("args lack %q: %s", want, got)
		}
	}
	if !c.Has(url) {
		t.Error("not kept")
	}
	if err := c.Warm(context.Background(), url); err != nil || f.count() != 1 {
		t.Errorf("asked again for a fresh answer: %d calls, %v", f.count(), err)
	}
	st, err := os.Stat(filepath.Join(c.Dir, "fOT0BUpITw8.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("%v %v", st, err)
	}
	if d, _ := os.Stat(c.Dir); d.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", d.Mode())
	}
}

func TestAStaleAnswerIsAskedForAgainAndAnExpiredOneIsNotUsed(t *testing.T) {
	f := &fakeRun{out: answer}
	c := newCache(t, f)
	now := time.Now()
	c.Now = func() time.Time { return now }
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	_ = c.Warm(context.Background(), url)
	now = now.Add(c.maxAge() + time.Minute)
	if c.Has(url) {
		t.Error("an expired answer is offered")
	}
	_ = c.Warm(context.Background(), url)
	if f.count() != 2 {
		t.Errorf("%d calls", f.count())
	}
}

func TestAFailedOrNonsenseAnswerIsNotKept(t *testing.T) {
	for name, f := range map[string]*fakeRun{
		"error":   {err: errors.New("403")},
		"empty":   {out: ""},
		"garbage": {out: "<html>"},
		"no url":  {out: `{"id":"x"}`},
	} {
		c := newCache(t, f)
		if err := c.Warm(context.Background(), "https://music.youtube.com/watch?v=fOT0BUpITw8"); err == nil {
			t.Errorf("%s: no error", name)
		}
		if left, _ := os.ReadDir(c.Dir); len(left) != 0 {
			t.Errorf("%s: kept %v", name, left)
		}
	}
}

func TestOneTrackIsWarmedOnceWhileItIsInFlight(t *testing.T) {
	f := &fakeRun{out: answer, wait: make(chan struct{})}
	c := newCache(t, f)
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Warm(context.Background(), url) }()
	}
	time.Sleep(100 * time.Millisecond)
	close(f.wait)
	wg.Wait()
	if f.count() != 1 {
		t.Errorf("%d runs", f.count())
	}
}

func TestOnlyTheNewestFewAnswersAreKept(t *testing.T) {
	f := &fakeRun{out: answer}
	c := newCache(t, f)
	c.MaxFiles = 3
	now := time.Now()
	c.Now = func() time.Time { return now }
	for i := 0; i < 6; i++ {
		now = now.Add(time.Minute)
		url := "https://music.youtube.com/watch?v=track" + string(rune('A'+i)) + "xxxxxx"
		if err := c.Warm(context.Background(), url); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(filepath.Join(c.Dir, ID(url)+".json"), now, now)
	}
	if left, _ := os.ReadDir(c.Dir); len(left) > 3 {
		t.Errorf("%d files", len(left))
	}
}

// ── the shim: what mpv runs in place of yt-dlp ──────────────────────────────────────────────────────────────────────

func shimRig(t *testing.T) (c *Cache, shim, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the shim is a shell script")
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "real.log")
	real := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho \"$@\" >> '"+log+"'\necho REAL\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c = &Cache{Dir: filepath.Join(dir, "ytdl"), Bin: real, Runner: (&fakeRun{out: answer}).run}
	shim, err := c.Shim()
	if err != nil {
		t.Fatal(err)
	}
	return c, shim, log
}

func runShim(t *testing.T, shim string, args ...string) string {
	t.Helper()
	out, err := exec.Command(shim, args...).Output()
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(out)
}

var mpvHook = []string{"--no-warnings", "-J", "--flat-playlist", "--sub-format", "ass/srt/best", "--format", "bestaudio", "--ignore-config", "--all-subs", "--no-playlist", "--"}

func TestTheShimAnswersMpvsHookFromTheKeptAnswerWithoutRunningYtdlp(t *testing.T) {
	c, shim, log := shimRig(t)
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	if err := c.Warm(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	if got := runShim(t, shim, append(mpvHook, url)...); strings.TrimSpace(got) != answer {
		t.Errorf("answer %q", got)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("yt-dlp ran")
	}
}

func TestTheShimRunsYtdlpForAnythingItDoesNotHaveOrKnow(t *testing.T) {
	c, shim, log := shimRig(t)
	_ = c.Warm(context.Background(), "https://music.youtube.com/watch?v=fOT0BUpITw8")
	for _, args := range [][]string{
		append(append([]string{}, mpvHook...), "https://music.youtube.com/watch?v=otherOTHER1"), // not warmed
		{"--version"},
		append(append([]string{}, mpvHook...), "https://music.youtube.com/watch?v=fOT0BUpITw8; touch pwned"),
		append(append([]string{}, mpvHook...), "https://music.youtube.com/watch?v=$(touch pwned)"),
		append(append([]string{}, mpvHook...), "https://example.com/watch?v=fOT0BUpITw8"),
		{"--no-warnings", "--", "https://music.youtube.com/watch?v=fOT0BUpITw8"}, // not the -J listing
	} {
		if got := runShim(t, shim, args...); !strings.Contains(got, "REAL") {
			t.Errorf("%v: %q", args, got)
		}
	}
	data, _ := os.ReadFile(log)
	if n := strings.Count(string(data), "\n"); n != 6 {
		t.Errorf("yt-dlp ran %d times:\n%s", n, data)
	}
	if _, err := os.Stat("pwned"); err == nil {
		t.Error("the URL was executed")
		_ = os.Remove("pwned")
	}
}

func TestTheShimDoesNotUseAnExpiredAnswer(t *testing.T) {
	c, shim, _ := shimRig(t)
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	_ = c.Warm(context.Background(), url)
	old := time.Now().Add(-c.maxAge() - time.Hour)
	_ = os.Chtimes(filepath.Join(c.Dir, "fOT0BUpITw8.json"), old, old)
	if got := runShim(t, shim, append(mpvHook, url)...); !strings.Contains(got, "REAL") {
		t.Errorf("answered from an expired file: %q", got)
	}
}

func TestTheShimQuotesTheProgramsItNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	dir := filepath.Join(t.TempDir(), "it's a dir")
	_ = os.MkdirAll(dir, 0o755)
	real := filepath.Join(dir, "yt dlp")
	_ = os.WriteFile(real, []byte("#!/bin/sh\necho REAL\n"), 0o755)
	c := &Cache{Dir: filepath.Join(dir, "cache dir"), Bin: real}
	shim, err := c.Shim()
	if err != nil {
		t.Fatal(err)
	}
	if got := runShim(t, shim, "--version"); !strings.Contains(got, "REAL") {
		t.Errorf("%q", got)
	}
}

func TestForgetDropsTheKeptAnswer(t *testing.T) {
	f := &fakeRun{out: answer}
	c := newCache(t, f)
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	_ = c.Warm(context.Background(), url)
	if !c.Has(url) {
		t.Fatal("not kept")
	}
	c.Forget(url)
	if c.Has(url) {
		t.Error("still kept after Forget")
	}
	c.Forget("not a url") // nothing happens
}

// mpv's hook runs yt-dlp with the JavaScript runtime the doctor found (--ytdl-raw-options-append=js-runtimes=...): yt-dlp
// enables only Deno by itself, and without a runtime it cannot solve YouTube's challenge and lists other (or no) formats. The
// prefetch must run the same command, or the shim answers mpv with what mpv's own run would not have chosen.
func TestWarmUsesTheJavaScriptRuntimeMpvsHookUses(t *testing.T) {
	f := &fakeRun{out: answer}
	c := newCache(t, f)
	c.JSRuntime = "node"
	if err := c.Warm(context.Background(), "https://music.youtube.com/watch?v=fOT0BUpITw8"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.calls[0], " ")
	if !strings.Contains(got, "--js-runtimes node --") {
		t.Errorf("args %s", got)
	}
}
