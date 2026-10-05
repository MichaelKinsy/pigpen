//go:build !windows

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

const fakeEnv = "PIG_MUSIC_FAKE_MPV"

// serveEnv makes the test binary act as `pigmusic serve` for the native engine.
const serveEnv = "PIG_MUSIC_CLI_SERVE"

func TestMain(m *testing.M) {
	if os.Getenv(serveEnv) == "1" {
		os.Exit(Run(context.Background(), os.Args[1:], IO{Out: os.Stdout, Err: os.Stderr}, Env{
			Serve: func(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
				return native.Serve(ctx, args, getenv, stderr, nil)
			},
		}))
	}
	if os.Getenv(fakeEnv) == "1" {
		if err := mpvfake.Run(os.Args[1:]); err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

// stubSource answers every search with the same three tracks.
type stubSource struct {
	searches []string
	err      error
}

var results = []music.Track{
	{ID: "aaaaaaaaaaa", Title: "Night Drive", Artists: []string{"Example Artist"}, Duration: 215 * time.Second},
	{ID: "bbbbbbbbbbb", Title: "Night Drive (Live)", Artists: []string{"Second Artist"}, Duration: 301 * time.Second},
	{ID: "ccccccccccc", Title: "Night Drive Lofi", Duration: 90 * time.Second},
}

func (s *stubSource) Search(_ context.Context, q string, limit int) ([]music.Track, error) {
	s.searches = append(s.searches, q+"|"+strconv.Itoa(limit))
	return results, s.err
}
func (s *stubSource) Library(context.Context) ([]music.Collection, error)   { return nil, nil }
func (s *stubSource) Tracks(context.Context, string) ([]music.Track, error) { return nil, nil }
func (s *stubSource) PlayURL(t music.Track) string {
	return "https://music.youtube.com/watch?v=" + t.ID
}

// rig runs commands the way separate processes would: every call builds a new
// command, and so a new Player, against the same directories.
type rig struct {
	t      *testing.T
	runDir string
	agent  string
	record string
	source *stubSource
}

func newRig(t *testing.T) *rig {
	t.Helper()
	run, err := os.MkdirTemp("", "pm")
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, runDir: run, agent: t.TempDir(), record: filepath.Join(run, "rec"), source: &stubSource{}}
	t.Cleanup(func() {
		files, _ := filepath.Glob(r.record + ".*")
		for _, f := range files {
			if pid, err := strconv.Atoi(strings.TrimPrefix(f, r.record+".")); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		_ = os.RemoveAll(run)
	})
	return r
}

func (r *rig) env() Env {
	// PIG_MUSIC_SEARCH=ytdlp: a unit test reaches no network, so search goes through the fake yt-dlp, not YouTube Music.
	vars := map[string]string{"XDG_RUNTIME_DIR": r.runDir, "PIG_CODING_AGENT_DIR": r.agent, "HOME": r.agent, "PIG_MUSIC_ENGINE": "mpv", "PIG_MUSIC_SEARCH": "ytdlp", "PIG_MUSIC_LIBRARY": "ytdlp"}
	return Env{
		Getenv: func(k string) string { return vars[k] },
		Source: r.source,
		Configure: func(c *mpv.Config) {
			c.MPVPath = os.Args[0]
			c.Env = []string{fakeEnv + "=1", "MPVFAKE_RECORD=" + r.record}
		},
	}
}

func (r *rig) run(args ...string) (stdout, stderr string, code int) {
	r.t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code = Run(ctx, args, IO{Out: &out, Err: &errOut}, r.env())
	return out.String(), errOut.String(), code
}

func (r *rig) ok(args ...string) string {
	r.t.Helper()
	out, errOut, code := r.run(args...)
	if code != 0 {
		r.t.Fatalf("pigmusic %s exited %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func (r *rig) processes() int {
	files, _ := filepath.Glob(r.record + ".*")
	return len(files)
}

func TestSearchListsResultsAndRemembersThemForPlayByNumber(t *testing.T) {
	r := newRig(t)
	out := r.ok("search", "night", "drive", "-n", "5")
	for _, want := range []string{" 1  aaaaaaaaaaa", "3:35  Example Artist - Night Drive", " 3  ccccccccccc", "1:30  Night Drive Lofi"} {
		if !strings.Contains(out, want) {
			t.Errorf("search output lacks %q:\n%s", want, out)
		}
	}
	if got := r.source.searches; len(got) != 1 || got[0] != "night drive|5" {
		t.Errorf("searches %v", got)
	}
	info, err := os.Stat(filepath.Join(r.runDir, "pig-music", "search.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("result cache: %v %v", info, err)
	}
	if r.processes() != 0 {
		t.Fatal("a search started mpv")
	}
}

func TestPlayThenKillTheCLIAndTheMusicContinuesAndAnotherCommandReattaches(t *testing.T) {
	r := newRig(t)
	r.ok("search", "night drive")
	out := r.ok("play", "2")
	if !strings.Contains(out, "playing  Second Artist - Night Drive (Live)") || !strings.Contains(out, "[2/3]") || !strings.Contains(out, "volume 100") {
		t.Fatalf("play output:\n%s", out)
	}
	if r.processes() != 1 {
		t.Fatalf("%d mpv processes", r.processes())
	}
	// The command above has returned and closed its player: that is the CLI being killed. mpv is still there.
	status := r.ok("status")
	if !strings.Contains(status, "playing  Second Artist - Night Drive (Live)  0:00 / 3:20") || !strings.Contains(status, "[2/3]") {
		t.Fatalf("status after reattach:\n%s", status)
	}
	queue := r.ok("queue")
	want := "   1  Example Artist - Night Drive\n>  2  Second Artist - Night Drive (Live)\n   3  Night Drive Lofi\n"
	if queue != want {
		t.Fatalf("queue\n%s\nwant\n%s", queue, want)
	}
	if r.processes() != 1 {
		t.Fatalf("a second mpv was started: %d", r.processes())
	}
}

func TestEveryTransportCommand(t *testing.T) {
	r := newRig(t)
	r.ok("play", "night drive") // a query: searches, plays the first, queues all three
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"pause"}, "paused  Example Artist - Night Drive"},
		{[]string{"resume"}, "playing  Example Artist - Night Drive"},
		{[]string{"toggle"}, "paused"},
		{[]string{"play"}, "playing"},
		{[]string{"seek", "30"}, "0:30 / 3:20"},
		{[]string{"seek", "-10.5"}, "0:20 / 3:20"},
		{[]string{"next"}, "[2/3]"},
		{[]string{"next"}, "[3/3]"},
		{[]string{"prev"}, "[2/3]"},
		{[]string{"jump", "1"}, "[1/3]"},
		{[]string{"volume", "40"}, "volume 40"},
		{[]string{"volume", "400"}, "volume 100"},
		{[]string{"move", "1", "3"}, "[3/3]"},
		{[]string{"remove", "1"}, "[2/2]"},
	}
	for _, s := range steps {
		if out := r.ok(s.args...); !strings.Contains(out, s.want) {
			t.Errorf("pigmusic %s: %q lacks %q", strings.Join(s.args, " "), out, s.want)
		}
	}
	if q := r.ok("queue"); !strings.Contains(q, ">  2  Example Artist - Night Drive") {
		t.Errorf("queue after move and remove:\n%s", q)
	}
	r.source.searches = nil
	out := r.ok("add", "another song")
	if !strings.Contains(out, "queued: Example Artist - Night Drive") || len(r.source.searches) != 1 {
		t.Errorf("add: %q %v", out, r.source.searches)
	}
	if q := r.ok("queue"); strings.Count(q, "\n") != 3 {
		t.Errorf("queue after add:\n%s", q)
	}
}

func TestErrorsAreReportedNotHidden(t *testing.T) {
	r := newRig(t)
	r.ok("play", "night drive")
	if _, errOut, code := r.run("next"); code != 0 {
		t.Fatalf("first next: %d %s", code, errOut)
	}
	r.ok("next")
	if _, errOut, code := r.run("next"); code != 1 || !strings.Contains(errOut, "end of the queue") {
		t.Errorf("next at the end: %d %q", code, errOut)
	}
	if _, errOut, code := r.run("jump", "9"); code != 1 || !strings.Contains(errOut, "does not exist") {
		t.Errorf("jump past the end: %d %q", code, errOut)
	}
	r.source.err = errors.New("yt-dlp: ERROR: HTTP Error 429: Too Many Requests")
	if _, errOut, code := r.run("search", "x"); code != 1 || !strings.Contains(errOut, "429") {
		t.Errorf("a failing source: %d %q", code, errOut)
	}
}

func TestCommandsThatNeedAPlayerDoNotStartOne(t *testing.T) {
	r := newRig(t)
	for _, args := range [][]string{{"pause"}, {"next"}, {"queue"}, {"volume", "5"}, {"seek", "5"}, {"jump", "1"}} {
		if _, errOut, code := r.run(args...); code != 1 || !strings.Contains(errOut, "mpv is not running") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
	if out := r.ok("status"); out != "stopped (mpv is not running)\n" {
		t.Errorf("status: %q", out)
	}
	if out := r.ok("stop"); out != "mpv is not running\n" {
		t.Errorf("stop: %q", out)
	}
	if r.processes() != 0 {
		t.Fatal("something started mpv")
	}
}

func TestStopEndsMpvAndForgetsTheQueue(t *testing.T) {
	r := newRig(t)
	r.ok("play", "night drive")
	if out := r.ok("stop"); out != "stopped\n" {
		t.Fatalf("stop: %q", out)
	}
	if out := r.ok("status"); out != "stopped (mpv is not running)\n" {
		t.Fatalf("status after stop: %q", out)
	}
	// A new play starts a fresh mpv.
	r.ok("play", "1")
	if r.processes() != 2 {
		t.Fatalf("%d mpv processes after stop and play, want 2 in total", r.processes())
	}
}

func TestUsageMistakesExitWithTwoAndNameTheCommands(t *testing.T) {
	r := newRig(t)
	for _, args := range [][]string{{}, {"frobnicate"}, {"search"}, {"search", "-n"}, {"search", "-n", "zero", "x"}, {"jump"}, {"jump", "x"}, {"move", "1"}, {"seek", "fast"}, {"volume"}, {"add"}} {
		_, errOut, code := r.run(args...)
		if code != 2 || !strings.Contains(errOut, "usage: pigmusic") {
			t.Errorf("%v: code %d stderr %q", args, code, errOut)
		}
	}
	if out, errOut, code := r.run("help"); code != 0 || out != "" || !strings.Contains(errOut, "search <query...>") {
		t.Errorf("help: %d %q %q", code, out, errOut)
	}
}

func TestPlayByNumberNeedsASearchAndAValidNumber(t *testing.T) {
	r := newRig(t)
	if _, errOut, code := r.run("play", "1"); code != 1 || !strings.Contains(errOut, "run `pigmusic search") {
		t.Errorf("no search yet: %d %q", code, errOut)
	}
	r.ok("search", "x")
	if _, errOut, code := r.run("play", "7"); code != 1 || !strings.Contains(errOut, "result 7 does not exist (the last search found 3)") {
		t.Errorf("number out of range: %d %q", code, errOut)
	}
	if r.processes() != 0 {
		t.Error("a bad number started mpv")
	}
}

func TestMissingProgramsAreNamedBeforeAnythingStarts(t *testing.T) {
	r := newRig(t)
	env := r.env()
	env.Source = nil // the real yt-dlp source, with a PATH that has neither program
	env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"check"}, IO{Out: &out, Err: &errOut}, env); code != 1 ||
		!strings.Contains(errOut.String(), "mpv and yt-dlp") || !strings.Contains(errOut.String(), "not installed") {
		t.Fatalf("check: %d %q", code, errOut.String())
	}
	// A cached search so that play gets as far as starting the player.
	r.ok("search", "x")
	errOut.Reset()
	if code := Run(context.Background(), []string{"play", "1"}, IO{Out: &out, Err: &errOut}, env); code != 1 || !strings.Contains(errOut.String(), "mpv") || !strings.Contains(errOut.String(), "yt-dlp") || !strings.Contains(errOut.String(), "fix:") {
		t.Fatalf("play: %d %q", code, errOut.String())
	}
	if r.processes() != 0 {
		t.Error("mpv started although a program is missing")
	}
	env.LookPath = func(string) (string, error) { return "/usr/bin/x", nil }
	out.Reset()
	if code := Run(context.Background(), []string{"check"}, IO{Out: &out, Err: &errOut}, env); code != 0 || out.String() != "mpv and yt-dlp are installed\n" {
		t.Fatalf("check with both present: %d %q", code, out.String())
	}
}

func TestSettingsFileNamesTheProgramsAndAMalformedOneIsAnError(t *testing.T) {
	r := newRig(t)
	dir := filepath.Join(r.agent, "pig-music")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"mpvPath":"/opt/mpv","ytdlpPath":"/opt/yt-dlp"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := r.env()
	env.Source = nil
	var seen []string
	env.LookPath = func(p string) (string, error) { seen = append(seen, p); return "", errors.New("x") }
	var errOut bytes.Buffer
	Run(context.Background(), []string{"check"}, IO{Out: &bytes.Buffer{}, Err: &errOut}, env)
	if len(seen) != 2 || seen[0] != "/opt/mpv" || seen[1] != "/opt/yt-dlp" {
		t.Errorf("looked for %v, want the settings paths", seen)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"mpvPath":`), 0o600); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := Run(context.Background(), []string{"status"}, IO{Out: &bytes.Buffer{}, Err: &errOut}, env); code != 1 || !strings.Contains(errOut.String(), "settings.json") {
		t.Errorf("malformed settings: %d %q", code, errOut.String())
	}
}

// fakeBin makes a directory of fake programs and the environment that finds them.
func fakeBin(t *testing.T, r *rig, programs map[string]string) Env {
	t.Helper()
	dir := t.TempDir()
	for name, body := range programs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := r.env()
	env.Source = nil
	env.LookPath = func(name string) (string, error) {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			return "", err
		}
		return p, nil
	}
	d := doctor.EnvFrom(env.Getenv, env.LookPath)
	d.Exists = func(p string) bool { // an audio socket is not what these tests are about; everything else is as it is
		if strings.Contains(p, "/pulse/") {
			return true
		}
		_, err := os.Stat(p)
		return err == nil
	}
	d.Now = func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }
	env.Doctor = &d
	return env
}

const fakeYtdlp = `if [ "$1" = "--version" ]; then echo 2026.08.19; exit 0; fi
echo "[debug] yt-dlp version stable@2026.08.19 from yt-dlp/yt-dlp [x] (zip)" >&2
echo "[debug] Optional libraries: yt_dlp_ejs-0.8.0" >&2
echo "[debug] JS runtimes: none" >&2
exit 2`

func TestDoctorReportsAndExitsOneOnlyForFailures(t *testing.T) {
	r := newRig(t)
	env := fakeBin(t, r, map[string]string{"mpv": `echo "mpv 0.37.0 Copyright"`, "yt-dlp": fakeYtdlp})
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"doctor"}, IO{Out: &out, Err: &errOut}, env)
	if code != 0 || !strings.Contains(out.String(), "ok   mpv") || !strings.Contains(out.String(), "warn JavaScript runtime") || !strings.Contains(out.String(), "fix:") {
		t.Fatalf("%d\n%s%s", code, out.String(), errOut.String())
	}
	env = fakeBin(t, r, map[string]string{"yt-dlp": fakeYtdlp})
	out.Reset()
	errOut.Reset()
	code = Run(context.Background(), []string{"doctor"}, IO{Out: &out, Err: &errOut}, env)
	if code != 1 || !strings.Contains(out.String(), "FAIL mpv") || !strings.Contains(out.String(), "fix:") {
		t.Fatalf("%d\n%s%s", code, out.String(), errOut.String())
	}
	if code := Run(context.Background(), []string{"doctor", "--nope"}, IO{Out: &out, Err: &errOut}, env); code != 2 {
		t.Errorf("a bad flag: %d", code)
	}
}

func TestSetupAsksAndDownloadsNothingOnNo(t *testing.T) {
	r := newRig(t)
	env := fakeBin(t, r, map[string]string{"mpv": `echo "mpv 0.37.0 Copyright"`, "yt-dlp": fakeYtdlp})
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"setup"}, IO{In: strings.NewReader("n\n"), Out: &out, Err: &errOut}, env)
	if code != 0 || !strings.Contains(out.String(), "Download Deno?") || !strings.Contains(out.String(), ".sha256sum") ||
		!strings.Contains(out.String(), "did not download Deno") {
		t.Fatalf("%d\n%s%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(r.agent, "pig-music", "bin")); err == nil {
		t.Error("something was downloaded after a no")
	}
	// no answer at all (stdin closed) is a no as well
	out.Reset()
	if code := Run(context.Background(), []string{"setup"}, IO{In: strings.NewReader(""), Out: &out, Err: &errOut}, env); code != 0 || !strings.Contains(out.String(), "did not download Deno") {
		t.Fatalf("%d\n%s", code, out.String())
	}
}

// libraryYtdlp is a yt-dlp that records its arguments and answers the library pages. It holds no cookies and reads none.
func libraryYtdlp(log string) string {
	return `echo "$@" >> ` + log + `
case "$*" in
  --version) echo 2026.08.19 ;;
  -v|*" -v")
    echo "[debug] Optional libraries: yt_dlp_ejs-0.8.0" >&2
    echo "[debug] JS runtimes: deno-2.5.0" >&2
    exit 2 ;;
  *feed/playlists*) echo '{"entries":[{"id":"PLaaaaaaaaaaaaaa1","title":"SECRET TITLE ONE"},{"id":"PLbbbbbbbbbbbbbb2","title":"SECRET TITLE TWO"}]}' ;;
  *list=LM*) echo '{"entries":[{"id":"aaaaaaaaaaa","title":"SECRET LIKED"},{"id":"bbbbbbbbbbb","title":"SECRET LIKED 2"},{"id":"ccccccccccc","title":"SECRET LIKED 3"}]}' ;;
  *search?q=*) echo '{"entries":[{"id":"aaaaaaaaaaa","title":"A song"}]}' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac`
}

func macChromeEnv(t *testing.T, r *rig, log string) Env {
	t.Helper()
	env := fakeBin(t, r, map[string]string{"mpv": `echo "mpv 0.37.0 Copyright"`, "yt-dlp": libraryYtdlp(log)})
	d := *env.Doctor
	d.GOOS = "darwin"
	inner := d.Run
	d.Run = func(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
		if bin == "plutil" {
			return []byte(`{"LSHandlers":[{"LSHandlerRoleAll":"com.google.chrome","LSHandlerURLScheme":"https"}]}`), nil, nil
		}
		return inner(ctx, bin, args)
	}
	d.Getenv = func(k string) string {
		if k == "HOME" {
			return "/Users/u"
		}
		return env.Getenv(k)
	}
	env.Doctor = &d
	return env
}

func TestLibraryCountsAsksOnceThenPrintsCountsOnlyAndOnlyLibraryCallsCarryTheFlag(t *testing.T) {
	r := newRig(t)
	log := filepath.Join(t.TempDir(), "args.log")
	env := macChromeEnv(t, r, log)
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"library-counts"}, IO{In: strings.NewReader("y\n"), Out: &out, Err: &errOut}, env)
	if code != 0 {
		t.Fatalf("%d\n%s%s", code, out.String(), errOut.String())
	}
	for _, want := range []string{"chrome (the default browser, com.google.chrome)", "Keychain", "Allow reading chrome? [y/N]", "playlists: 2", "liked songs: 3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String()+errOut.String(), "SECRET") {
		t.Errorf("a title was printed:\n%s%s", out.String(), errOut.String())
	}
	consent, _ := os.ReadFile(filepath.Join(r.agent, "pig-music", "cookie-consent.json"))
	if strings.TrimSpace(string(consent)) != `{"browsers":["chrome"]}` {
		t.Errorf("consent file %q", consent)
	}
	// the second run is not asked again
	out.Reset()
	if code := Run(context.Background(), []string{"library-counts"}, IO{In: strings.NewReader(""), Out: &out, Err: &errOut}, env); code != 0 || strings.Contains(out.String(), "[y/N]") || !strings.Contains(out.String(), "liked songs: 3") {
		t.Fatalf("%d\n%s", code, out.String())
	}
	// a search now: no cookie flag anywhere on it
	if code := Run(context.Background(), []string{"search", "x"}, IO{Out: &out, Err: &errOut}, env); code != 0 {
		t.Fatalf("search %d %s", code, errOut.String())
	}
	logged, _ := os.ReadFile(log)
	for _, line := range strings.Split(strings.TrimSpace(string(logged)), "\n") {
		hasFlag := strings.Contains(line, "--cookies")
		isLibrary := strings.Contains(line, "feed/playlists") || strings.Contains(line, "list=LM")
		if hasFlag != isLibrary {
			t.Errorf("cookie flag on a non-library call, or missing on a library call: %q", line)
		}
		if hasFlag && !strings.Contains(line, "--cookies-from-browser chrome ") {
			t.Errorf("flag is not exactly --cookies-from-browser chrome: %q", line)
		}
	}
}

func TestLibraryCountsWithoutAYesReadsNothing(t *testing.T) {
	r := newRig(t)
	log := filepath.Join(t.TempDir(), "args.log")
	env := macChromeEnv(t, r, log)
	var out, errOut bytes.Buffer
	for _, in := range []string{"n\n", "", "maybe\n"} {
		out.Reset()
		errOut.Reset()
		code := Run(context.Background(), []string{"library-counts"}, IO{In: strings.NewReader(in), Out: &out, Err: &errOut}, env)
		if code != 1 || !strings.Contains(errOut.String(), "not allowed") {
			t.Errorf("%q: %d %s", in, code, errOut.String())
		}
	}
	logged, _ := os.ReadFile(log)
	if strings.Contains(string(logged), "feed/playlists") || strings.Contains(string(logged), "--cookies") {
		t.Errorf("yt-dlp read the library without consent:\n%s", logged)
	}
	if _, err := os.Stat(filepath.Join(r.agent, "pig-music", "cookie-consent.json")); err == nil {
		t.Error("a consent file was written")
	}
}

func TestLibraryCountsWithNoBrowserSaysSoAndNamesTheSetting(t *testing.T) {
	r := newRig(t)
	env := fakeBin(t, r, map[string]string{"mpv": `echo "mpv 0.37.0 Copyright"`, "yt-dlp": libraryYtdlp(filepath.Join(t.TempDir(), "log"))})
	d := *env.Doctor
	d.GOOS = "freebsd" // a system with no browser to read (Termux also has none; its audio check is not what this test is about)
	env.Doctor = &d
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"library-counts"}, IO{In: strings.NewReader("y\n"), Out: &out, Err: &errOut}, env); code != 1 ||
		!strings.Contains(errOut.String(), "cookieBrowser") {
		t.Errorf("%d\n%s%s", code, out.String(), errOut.String())
	}
}

// ── the quick commands the slash command shares: vol, now, shuffle, repeat ──

func TestVolAndNowAreShortFormsOfVolumeAndStatus(t *testing.T) {
	r := newRig(t)
	r.ok("play", "night", "drive")
	if out := r.ok("vol", "30"); !strings.Contains(out, "volume 30") {
		t.Errorf("vol: %q", out)
	}
	now := r.ok("now")
	if !strings.Contains(now, "playing  Example Artist - Night Drive") || strings.Count(now, "\n") != 1 {
		t.Errorf("now should be one line: %q", now)
	}
	if _, _, code := r.run("vol"); code != 2 {
		t.Errorf("vol with no number: exit %d, want 2", code)
	}
}

func TestShuffleAndRepeatSetTheModesAndTheStatusLineSaysSo(t *testing.T) {
	r := newRig(t)
	r.ok("play", "night", "drive")
	if out := r.ok("shuffle", "on"); !strings.Contains(out, "shuffle") {
		t.Errorf("shuffle on: %q", out)
	}
	// mpv has no property that says the playlist was shuffled, so a later command (a new process) cannot show it; repeat it can
	if out := r.ok("shuffle", "off"); strings.Contains(out, "shuffle") {
		t.Errorf("shuffle off: %q", out)
	}
	for _, mode := range []string{"all", "one"} {
		if out := r.ok("repeat", mode); !strings.Contains(out, "repeat "+mode) {
			t.Errorf("repeat %s: %q", mode, out)
		}
	}
	if out := r.ok("now"); !strings.Contains(out, "repeat one") {
		t.Errorf("repeat is read back from mpv by the next command: %q", out)
	}
	if out := r.ok("repeat", "off"); strings.Contains(out, "repeat") {
		t.Errorf("repeat off: %q", out)
	}
}

func TestShuffleAndRepeatNeedAValidWordAndARunningPlayer(t *testing.T) {
	r := newRig(t)
	for _, args := range [][]string{{"shuffle"}, {"shuffle", "maybe"}, {"repeat"}, {"repeat", "sideways"}, {"shuffle", "on", "off"}} {
		if _, _, code := r.run(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	_, errOut, code := r.run("shuffle", "on")
	if code != 1 || !strings.Contains(errOut, "nothing is playing") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

// The extension runs this package in-process for the quick commands and must not link the native engine, so `serve` and
// `native` exist only where the caller supplies them.
func TestServeAndNativeNeedTheProgramThatLinksTheEngine(t *testing.T) {
	for _, cmd := range [][]string{{"serve", "--dir", t.TempDir()}, {"native", "playlist", "PLabcdefghijklmnop"}} {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), cmd, IO{Out: &out, Err: &errOut}, Env{Getenv: func(string) string { return "" }})
		if code != 1 || !strings.Contains(errOut.String(), "pigmusic program") || out.Len() != 0 {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", cmd, code, out.String(), errOut.String())
		}
	}
	var got []string
	env := Env{
		Getenv: func(string) string { return "" },
		Native: func(_ context.Context, args []string, stdout, _ io.Writer) int {
			got = args
			fmt.Fprint(stdout, "{}")
			return 0
		},
	}
	var out bytes.Buffer
	if code := Run(context.Background(), []string{"native", "probe", "dQw4w9WgXcQ"}, IO{Out: &out, Err: io.Discard}, env); code != 0 || strings.Join(got, " ") != "probe dQw4w9WgXcQ" || out.String() != "{}" {
		t.Errorf("exit %d, args %v, out %q", code, got, out.String())
	}
}
