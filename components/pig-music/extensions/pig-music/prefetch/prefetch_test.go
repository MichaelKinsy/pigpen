package prefetch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

// mpvHook is what mpv 0.41's yt-dlp hook passes for ytdl-format=bestaudio with ytdl-raw-options ignore-config (recorded from a
// real mpv 0.41.0, with the watch URL last).
var mpvHook = []string{"--no-warnings", "-J", "--flat-playlist", "--sub-format", "ass/srt/best", "--format", "bestaudio", "--ignore-config", "--sub-langs", "all", "--write-srt", "--no-playlist", "--"}

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

// answerExpiring is a listing whose two stream URLs YouTube signed to expire at first and at second (format URLs carry
// expire=<unix seconds>; the second as yt-dlp's JSON may escape the &).
func answerExpiring(first, second time.Time) string {
	return `{"id":"fOT0BUpITw8","title":"x","formats":[` +
		`{"url":"https://rr1.googlevideo.com/videoplayback?expire=` + strconv.FormatInt(second.Unix(), 10) + `&id=abc","format_id":"140"},` +
		`{"url":"https://rr1.googlevideo.com/videoplayback?id=abc\u0026expire=` + strconv.FormatInt(first.Unix(), 10) + `\u0026ip=1.2.3.4","format_id":"251"}]}`
}

// An answer is kept for MaxAge (1 h) on the assumption that its stream URLs last about six hours. One whose URLs expire
// sooner must not be answered from once they are about to: mpv would play a stream YouTube refuses part way through.
func TestTheShimDoesNotServeAnAnswerWhoseStreamsExpireWithinTheMargin(t *testing.T) {
	c, shim, _ := shimRig(t)
	f := &fakeRun{out: answerExpiring(time.Now().Add(30*time.Minute), time.Now().Add(6*time.Hour))}
	c.Runner = f.run
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	_ = c.Warm(context.Background(), url)
	if c.Has(url) {
		t.Error("an answer whose first stream expires in 30 minutes is offered")
	}
	if got := runShim(t, shim, append(mpvHook, url)...); !strings.Contains(got, "REAL") {
		t.Errorf("the shim answered from it: %q", got)
	}
}

// The first stream expires 90 minutes after the resolve: the answer goes stale half an hour in, before MaxAge.
func TestAnAnswerGoesStaleAMarginBeforeItsFirstStreamExpires(t *testing.T) {
	start := time.Now()
	const life = 90 * time.Minute
	f := &fakeRun{out: answerExpiring(start.Add(life), start.Add(6*time.Hour))}
	c := newCache(t, f)
	now := start
	c.Now = func() time.Time { return now }
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	if err := c.Warm(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	if !c.Has(url) {
		t.Fatal("a fresh answer is not offered")
	}
	now = start.Add(life - expiryMargin + time.Minute)
	if c.Has(url) {
		t.Errorf("offered %s before its first stream expires", life-(now.Sub(start)))
	}
	// The shim's age check reads the same deadline: the file is as old as an answer that has reached MaxAge.
	st, err := os.Stat(filepath.Join(c.Dir, "fOT0BUpITw8.json"))
	if err != nil {
		t.Fatal(err)
	}
	if deadline := st.ModTime().Add(c.maxAge()); deadline.Sub(start.Add(life-expiryMargin)).Abs() > 2*time.Second {
		t.Errorf("the shim would answer until %s, want %s", deadline, start.Add(life-expiryMargin))
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

// Forget says whether a fresh answer was kept: only then did mpv play from it, and a refusal is one a fresh resolve can mend.
func TestForgetDropsTheKeptAnswerAndSaysWhetherThereWasOne(t *testing.T) {
	f := &fakeRun{out: answer}
	c := newCache(t, f)
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	_ = c.Warm(context.Background(), url)
	if !c.Has(url) {
		t.Fatal("not kept")
	}
	if !c.Forget(url) {
		t.Error("Forget says nothing was kept")
	}
	if c.Has(url) {
		t.Error("still kept after Forget")
	}
	if c.Forget(url) {
		t.Error("Forget says an answer was kept the second time")
	}
	if c.Forget("not a url") { // nothing happens
		t.Error("Forget says an answer was kept for no URL")
	}
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

// Warm runs the command mpv's hook runs, so that what the shim hands mpv is what mpv's own run would have listed. mpv 0.41
// asks for every subtitle with --sub-langs all --write-srt (mpv 0.37 said --all-subs, which yt-dlp reads the same way).
func TestWarmRunsTheCommandMpv041sHookRuns(t *testing.T) {
	f := &fakeRun{out: answer}
	c := newCache(t, f)
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	if err := c.Warm(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(f.calls[0][1:], " "), strings.Join(append(append([]string{}, mpvHook...), url), " "); got != want {
		t.Errorf("Warm ran\n%s\nmpv 0.41's hook runs\n%s", got, want)
	}
}

// ── available_at: YouTube refuses a stream until its pre-roll ad could have been skipped ───────────────────────────────

// listingAvailableAt is a listing whose requested stream YouTube serves from the unix second at (yt-dlp's available_at, 0 for a
// yt-dlp that leaves it out), and whose other format, which mpv does not play, only from other.
func listingAvailableAt(at, other int64) string {
	field := func(v int64) string {
		if v == 0 {
			return ""
		}
		return `, "available_at": ` + strconv.FormatInt(v, 10)
	}
	want := `{"url": "https://rr1.googlevideo.com/videoplayback?id=abc", "format_id": "251"` + field(at) + `, "http_headers": {"User-Agent": "x]}"}}`
	return `{"id": "fOT0BUpITw8", "title": "a ] title \" with } brackets", "formats": [` +
		`{"url": "https://rr1.googlevideo.com/videoplayback?id=def", "format_id": "140"` + field(other) + `}, ` + want + `], ` +
		`"requested_downloads": [` + want + `], "format_id": "251"` + field(other) + `}`
}

// timedShim runs the shim and says when it finished.
func timedShim(t *testing.T, shim string, args ...string) (out string, done time.Time) {
	t.Helper()
	out = runShim(t, shim, args...)
	return out, time.Now()
}

func TestTheShimHoldsAKeptAnswerUntilItsStreamIsAvailable(t *testing.T) {
	c, shim, log := shimRig(t)
	at := time.Now().Unix() + 2
	listing := listingAvailableAt(at, 0)
	c.Runner = (&fakeRun{out: listing}).run
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	if err := c.Warm(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	got, done := timedShim(t, shim, append(mpvHook, url)...)
	if strings.TrimSpace(got) != listing {
		t.Errorf("answer %q", got)
	}
	if done.Unix() < at {
		t.Errorf("handed to mpv %s before its stream is available", time.Unix(at, 0).Sub(done))
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("yt-dlp ran")
	}
}

// A stream that is available already, or a listing without available_at (yt-dlp before it had it), is handed over at once.
// Only the requested stream counts: the listing's other formats (other clients' too) may be held back far longer.
func TestTheShimDoesNotHoldAnAvailableStream(t *testing.T) {
	for name, listing := range map[string]string{
		"available":         listingAvailableAt(time.Now().Unix()-5, 0),
		"no available_at":   listingAvailableAt(0, 0),
		"another held back": listingAvailableAt(time.Now().Unix()-5, time.Now().Unix()+3600),
	} {
		c, shim, log := shimRig(t)
		c.Runner = (&fakeRun{out: listing}).run
		url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
		if err := c.Warm(context.Background(), url); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		got, done := timedShim(t, shim, append(mpvHook, url)...)
		if strings.TrimSpace(got) != listing || done.Sub(start) > 900*time.Millisecond {
			t.Errorf("%s: answered %q after %s", name, got, done.Sub(start))
		}
		if _, err := os.Stat(log); err == nil {
			t.Errorf("%s: yt-dlp ran", name)
		}
	}
}

// fakeRealYtdlp makes the rig's yt-dlp print the listing (and exit with code), as a fresh resolve would.
func fakeRealYtdlp(t *testing.T, c *Cache, listing string, code int) {
	t.Helper()
	script := "#!/bin/sh\necho \"$@\" >> '" + filepath.Join(filepath.Dir(c.Bin), "real.log") + "'\ncat <<'EOF'\n" + listing + "\nEOF\necho resolved >&2\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(c.Bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A resolve the shim passes to yt-dlp (nothing kept, or recovery's fresh resolve after Forget) is held the same way.
func TestTheShimHoldsAFreshResolveUntilItsStreamIsAvailable(t *testing.T) {
	c, shim, _ := shimRig(t)
	at := time.Now().Unix() + 2
	listing := listingAvailableAt(at, 0)
	fakeRealYtdlp(t, c, listing, 0)
	got, done := timedShim(t, shim, append(mpvHook, "https://music.youtube.com/watch?v=otherOTHER1")...)
	if strings.TrimSpace(got) != listing {
		t.Errorf("answer %q", got)
	}
	if done.Unix() < at {
		t.Errorf("handed to mpv %s before its stream is available", time.Unix(at, 0).Sub(done))
	}
}

// A failed resolve is passed on as it was: what yt-dlp printed, and its exit status.
func TestTheShimPassesOnAFailedResolve(t *testing.T) {
	c, shim, _ := shimRig(t)
	fakeRealYtdlp(t, c, "partial", 3)
	cmd := exec.Command(shim, append(mpvHook, "https://music.youtube.com/watch?v=otherOTHER1")...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || strings.TrimSpace(string(out)) != "partial" || !strings.Contains(stderr.String(), "resolved") {
		t.Errorf("exit %v, stdout %q, stderr %q", err, out, stderr.String())
	}
}

// A stream held back for longer than maxHold is not waited for: a kept answer is resolved afresh, and a fresh one fails,
// saying why, so that mpv moves on instead of sitting silent.
func TestTheShimDoesNotWaitLongForAStream(t *testing.T) {
	c, shim, log := shimRig(t)
	c.Runner = (&fakeRun{out: listingAvailableAt(time.Now().Unix()+3600, 0)}).run
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	if err := c.Warm(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	if got := runShim(t, shim, append(mpvHook, url)...); !strings.Contains(got, "REAL") {
		t.Errorf("answered from a listing held back for an hour: %q", got)
	}
	if data, _ := os.ReadFile(log); strings.Count(string(data), "\n") != 1 {
		t.Errorf("yt-dlp ran: %q", data)
	}
	fakeRealYtdlp(t, c, listingAvailableAt(time.Now().Unix()+3600, 0), 0)
	start := time.Now()
	cmd := exec.Command(shim, append(mpvHook, url)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil || len(out) != 0 || !strings.Contains(stderr.String(), "available") || time.Since(start) > 900*time.Millisecond {
		t.Errorf("exit %v after %s, stdout %q, stderr %q", err, time.Since(start), out, stderr.String())
	}
}

// An answer is used for an hour at most, by Has and by the shim alike: refusals were seen with older answers, and R4 found
// stream URLs served at every age it measured, up to 2 h, so an hour leaves a margin.
func TestAnAnswerIsUsedForAnHour(t *testing.T) {
	c, shim, _ := shimRig(t)
	url := "https://music.youtube.com/watch?v=fOT0BUpITw8"
	if err := c.Warm(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(c.Dir, "fOT0BUpITw8.json")
	for _, age := range []time.Duration{59 * time.Minute, 61 * time.Minute} {
		born := time.Now().Add(-age)
		if err := os.Chtimes(file, born, born); err != nil {
			t.Fatal(err)
		}
		fresh := age < time.Hour
		if c.Has(url) != fresh {
			t.Errorf("%s old: Has %v", age, !fresh)
		}
		if got := runShim(t, shim, append(mpvHook, url)...); (strings.TrimSpace(got) == answer) != fresh {
			t.Errorf("%s old: the shim answered %q", age, got)
		}
	}
}
