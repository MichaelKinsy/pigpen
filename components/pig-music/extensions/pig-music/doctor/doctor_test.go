package doctor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/cookies"
	"github.com/MichaelKinsy/pigpen/pig-music/ytdlp"
)

var now = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

const verboseNone = `[debug] Command-line config: ['-v']
[debug] yt-dlp version stable@2026.08.19 from yt-dlp/yt-dlp [594bd50c2] (zip)
[debug] Optional libraries: Cryptodome-3.20.0, requests-2.31.0 (unsupported), yt_dlp_ejs-0.8.0
[debug] JS runtimes: none
`

func verbose(runtimes, ejs string) string {
	libs := "Cryptodome-3.20.0"
	if ejs != "" {
		libs += ", yt_dlp_ejs-" + ejs
	}
	return "[debug] yt-dlp version stable@2026.08.19 from yt-dlp/yt-dlp [594bd50c2] (zip)\n[debug] Optional libraries: " + libs + "\n[debug] JS runtimes: " + runtimes + "\n"
}

// world is a pretend machine: the programs on PATH and what each prints.
type world struct {
	goos     string
	programs map[string]string // name -> path, as LookPath finds it
	mpvOut   string
	ytVer    string
	ytVerb   string            // what `yt-dlp -v` prints (stderr)
	runtimes map[string]string // program path -> --version output
	exists   map[string]bool
	probeErr error
	probed   int
}

func healthy() *world {
	return &world{
		goos:     "linux",
		programs: map[string]string{"mpv": "/usr/bin/mpv", "yt-dlp": "/usr/local/bin/yt-dlp"},
		mpvOut:   "mpv 0.37.0 Copyright © 2000-2023 mpv/MPlayer/mplayer2 projects\n built on ...",
		ytVer:    "2026.08.19",
		ytVerb:   verbose("deno-2.5.0", "0.8.0"),
		runtimes: map[string]string{},
		exists:   map[string]bool{"/run/user/1000/pulse/native": true},
	}
}

func (w *world) env() Env {
	return Env{
		GOOS: w.goos, GOARCH: "amd64",
		Getenv: func(k string) string {
			if k == "XDG_RUNTIME_DIR" {
				return "/run/user/1000"
			}
			return ""
		},
		LookPath: func(name string) (string, error) {
			if p, ok := w.programs[name]; ok {
				return p, nil
			}
			if w.exists[name] || name == "/custom/mpv" || name == "/custom/yt-dlp" {
				return name, nil
			}
			return "", exec.ErrNotFound
		},
		Run: func(_ context.Context, bin string, args []string) ([]byte, []byte, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.HasSuffix(bin, "mpv"):
				return []byte(w.mpvOut), nil, nil
			case strings.HasSuffix(bin, "yt-dlp"):
				if joined == "--version" {
					return []byte(w.ytVer + "\n"), nil, nil
				}
				if i := strings.Index(joined, "--js-runtimes "); i >= 0 {
					rt := strings.Fields(joined[i+len("--js-runtimes "):])[0]
					name, _, _ := strings.Cut(rt, ":")
					return nil, []byte(strings.Replace(w.ytVerb, "JS runtimes: none", "JS runtimes: "+name+"-99.0.0", 1)), errors.New("exit 2")
				}
				return nil, []byte(w.ytVerb), errors.New("exit 2")
			}
			if out, ok := w.runtimes[bin]; ok {
				return []byte(out), nil, nil
			}
			return nil, nil, exec.ErrNotFound
		},
		Now:    func() time.Time { return now },
		Exists: func(p string) bool { return w.exists[p] },
		Glob:   func(string) ([]string, error) { return nil, nil },
		Probe: func(context.Context, string, string) error {
			w.probed++
			return w.probeErr
		},
	}
}

func run(t *testing.T, w *world, cfg Config, opts Options) Report {
	t.Helper()
	return Run(context.Background(), cfg, w.env(), opts)
}

func statusOf(t *testing.T, r Report, id string) Check {
	t.Helper()
	c, ok := r.Check(id)
	if !ok {
		t.Fatalf("no %q check in:\n%s", id, r.Format())
	}
	return c
}

func TestAHealthyMachinePassesAndNeedsNoJSArgument(t *testing.T) {
	r := run(t, healthy(), Config{}, Options{})
	if r.Failed() || r.Err() != nil {
		t.Fatalf("%s", r.Format())
	}
	for _, id := range []string{"mpv", "yt-dlp", "js-runtime", "ejs", "audio"} {
		if c := statusOf(t, r, id); c.Status != OK {
			t.Errorf("%s: %v %s", id, c.Status, c.Detail)
		}
	}
	if r.YtdlpVersion != "2026.08.19" || r.MPVPath != "/usr/bin/mpv" || r.YtdlpPath != "/usr/local/bin/yt-dlp" || r.JSRuntimeArg != "" {
		t.Errorf("%+v", r)
	}
}

func TestMissingMPVIsOneFailureWithTheFixForEachSystem(t *testing.T) {
	for goos, want := range map[string]string{
		"linux": "apt install mpv", "darwin": "brew install mpv", "windows": "winget install", "android": "pkg install mpv",
	} {
		w := healthy()
		w.goos = goos
		delete(w.programs, "mpv")
		r := run(t, w, Config{}, Options{})
		c := statusOf(t, r, "mpv")
		if c.Status != Fail || !strings.Contains(c.Fix, want) || !r.Failed() {
			t.Errorf("%s: %+v", goos, c)
		}
		if err := r.Err(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v", goos, err)
		}
	}
}

func TestAnOldMPVFails(t *testing.T) {
	w := healthy()
	w.mpvOut = "mpv 0.34.1 Copyright © 2000-2022"
	c := statusOf(t, run(t, w, Config{}, Options{}), "mpv")
	if c.Status != Fail || !strings.Contains(c.Detail, "0.34.1") || !strings.Contains(c.Detail, "0.35") {
		t.Errorf("%+v", c)
	}
	w.mpvOut = "mpv 0.35.0 Copyright"
	if c := statusOf(t, run(t, w, Config{}, Options{}), "mpv"); c.Status != OK {
		t.Errorf("0.35.0: %+v", c)
	}
	w.mpvOut = "mpv v0.38.0-dev-g12345 Copyright"
	if c := statusOf(t, run(t, w, Config{}, Options{}), "mpv"); c.Status != OK {
		t.Errorf("dev build: %+v", c)
	}
	w.mpvOut = "garbage"
	if c := statusOf(t, run(t, w, Config{}, Options{}), "mpv"); c.Status != Fail || !strings.Contains(c.Detail, "version") {
		t.Errorf("garbage: %+v", c)
	}
}

func TestMissingYtdlpFailsAndNothingElseAboutItIsChecked(t *testing.T) {
	w := healthy()
	delete(w.programs, "yt-dlp")
	r := run(t, w, Config{}, Options{})
	c := statusOf(t, r, "yt-dlp")
	if c.Status != Fail || !strings.Contains(c.Fix, "/music setup") {
		t.Fatalf("%+v", c)
	}
	if js := statusOf(t, r, "js-runtime"); js.Status != Skipped {
		t.Errorf("js-runtime %+v", js)
	}
}

func TestYtdlpAgeWarnsThenFails(t *testing.T) {
	for _, tc := range []struct {
		today time.Time
		want  Status
	}{
		{time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC), OK},
		{time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), Warn},
		{time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC), Fail},
	} {
		w := healthy()
		env := w.env()
		env.Now = func() time.Time { return tc.today }
		c := statusOf(t, Run(context.Background(), Config{}, env, Options{}), "yt-dlp")
		if c.Status != tc.want {
			t.Errorf("%s: %+v", tc.today.Format("2006-01-02"), c)
		}
		if tc.want != OK && !strings.Contains(c.Fix, "yt-dlp -U") {
			t.Errorf("fix %q", c.Fix)
		}
	}
}

func TestYtdlpSaysWhoManagesIt(t *testing.T) {
	for path, want := range map[string]string{
		"/data/pm/bin/yt-dlp":      "self-managed",
		"/usr/bin/yt-dlp":          "package manager",
		"/opt/homebrew/bin/yt-dlp": "package manager",
		"/usr/local/bin/yt-dlp":    "installed by you",
		"/home/u/bin/yt-dlp":       "installed by you",
	} {
		w := healthy()
		w.programs["yt-dlp"] = path
		w.exists["/data/pm/bin/yt-dlp"] = path == "/data/pm/bin/yt-dlp"
		c := statusOf(t, run(t, w, Config{DataDir: "/data/pm"}, Options{}), "yt-dlp")
		if !strings.Contains(c.Detail, want) {
			t.Errorf("%s: %q lacks %q", path, c.Detail, want)
		}
	}
}

func TestASelfManagedYtdlpIsPreferredAndItsFixIsSetup(t *testing.T) {
	w := healthy()
	w.exists["/data/pm/bin/yt-dlp"] = true
	r := run(t, w, Config{DataDir: "/data/pm"}, Options{})
	if r.YtdlpPath != "/data/pm/bin/yt-dlp" || !r.SelfManaged {
		t.Fatalf("%+v", r)
	}
	// ...but a configured path wins over it.
	r = run(t, w, Config{DataDir: "/data/pm", YtdlpPath: "/custom/yt-dlp"}, Options{})
	if r.YtdlpPath != "/custom/yt-dlp" || r.SelfManaged {
		t.Fatalf("%+v", r)
	}
	later := w.env()
	later.Now = func() time.Time { return time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC) }
	c := statusOf(t, Run(context.Background(), Config{DataDir: "/data/pm"}, later, Options{}), "yt-dlp")
	if c.Status != Fail || !strings.Contains(c.Fix, "/music setup") {
		t.Errorf("%+v", c)
	}
}

func TestNoJSRuntimeWarnsAndNamesTheFix(t *testing.T) {
	w := healthy()
	w.ytVerb = verbose("none", "0.8.0")
	r := run(t, w, Config{}, Options{})
	c := statusOf(t, r, "js-runtime")
	if c.Status != Warn || !strings.Contains(c.Fix, "Deno") || r.Failed() || r.JSRuntimeArg != "" {
		t.Fatalf("%+v failed=%v arg=%q", c, r.Failed(), r.JSRuntimeArg)
	}
	w.goos = "android"
	if c := statusOf(t, run(t, w, Config{}, Options{}), "js-runtime"); !strings.Contains(c.Fix, "pkg install nodejs") {
		t.Errorf("termux: %+v", c)
	}
}

func TestANodeOnPathIsEnabledWhenYtdlpFoundNoRuntime(t *testing.T) {
	w := healthy()
	w.ytVerb = verbose("none", "0.8.0")
	w.programs["node"] = "/usr/bin/node"
	w.runtimes["/usr/bin/node"] = "v24.19.0\n"
	r := run(t, w, Config{}, Options{})
	if c := statusOf(t, r, "js-runtime"); c.Status != OK || r.JSRuntimeArg != "node" {
		t.Fatalf("%+v arg=%q", c, r.JSRuntimeArg)
	}
	w.runtimes["/usr/bin/node"] = "v20.11.0\n" // too old: Node 22 is the floor
	r = run(t, w, Config{}, Options{})
	if c := statusOf(t, r, "js-runtime"); c.Status != Warn || r.JSRuntimeArg != "" || !strings.Contains(c.Detail, "20.11") {
		t.Fatalf("old node: %+v arg=%q", c, r.JSRuntimeArg)
	}
}

func TestQuickJSNeedsVersion012(t *testing.T) {
	w := healthy()
	w.ytVerb = verbose("none", "0.8.0")
	w.programs["qjs"] = "/usr/bin/qjs"
	w.runtimes["/usr/bin/qjs"] = "QuickJS-ng version v0.11.0\n"
	if r := run(t, w, Config{}, Options{}); r.JSRuntimeArg != "" {
		t.Errorf("0.11 accepted: %q", r.JSRuntimeArg)
	}
	w.runtimes["/usr/bin/qjs"] = "QuickJS-ng version v0.12.1\n"
	if r := run(t, w, Config{}, Options{}); r.JSRuntimeArg != "quickjs" {
		t.Errorf("0.12.1 not chosen: %q", r.JSRuntimeArg)
	}
}

func TestADenoInTheDataDirIsUsedByItsPath(t *testing.T) {
	w := healthy()
	w.ytVerb = verbose("none", "0.8.0")
	w.exists["/data/pm/bin/deno"] = true
	w.runtimes["/data/pm/bin/deno"] = "deno 2.5.0 (stable, release, x86_64-unknown-linux-gnu)\n"
	r := run(t, w, Config{DataDir: "/data/pm"}, Options{})
	if r.JSRuntimeArg != "deno:/data/pm/bin/deno" || statusOf(t, r, "js-runtime").Status != OK {
		t.Fatalf("arg=%q\n%s", r.JSRuntimeArg, r.Format())
	}
	w.runtimes["/data/pm/bin/deno"] = "deno 2.2.9\n"
	if r := run(t, w, Config{DataDir: "/data/pm"}, Options{}); r.JSRuntimeArg != "" || statusOf(t, r, "js-runtime").Status != Warn {
		t.Errorf("deno 2.2 accepted: %q", r.JSRuntimeArg)
	}
}

func TestAConfiguredJSRuntimeIsUsedAsGiven(t *testing.T) {
	w := healthy()
	w.ytVerb = verbose("none", "0.8.0")
	r := run(t, w, Config{JSRuntime: "node:/opt/node/bin/node"}, Options{})
	if r.JSRuntimeArg != "node:/opt/node/bin/node" || statusOf(t, r, "js-runtime").Status != OK {
		t.Fatalf("arg=%q\n%s", r.JSRuntimeArg, r.Format())
	}
}

func TestMissingEJSWithARuntimeWarns(t *testing.T) {
	w := healthy()
	w.ytVerb = verbose("deno-2.5.0", "")
	c := statusOf(t, run(t, w, Config{}, Options{}), "ejs")
	if c.Status != Warn || !strings.Contains(c.Fix, "yt-dlp") {
		t.Errorf("%+v", c)
	}
}

func TestAudioOnLinuxNeedsASocketOrALib(t *testing.T) {
	w := healthy()
	w.exists = map[string]bool{}
	c := statusOf(t, run(t, w, Config{}, Options{}), "audio")
	if c.Status != Fail || !strings.Contains(c.Fix, "pipewire-pulse") {
		t.Fatalf("%+v", c)
	}
	w.exists["/run/user/1000/pipewire-0"] = true
	if c := statusOf(t, run(t, w, Config{}, Options{}), "audio"); c.Status != OK {
		t.Errorf("pipewire: %+v", c)
	}
	w.exists = map[string]bool{}
	env := w.env()
	env.Glob = func(p string) ([]string, error) {
		if strings.HasSuffix(p, "libasound.so.2") {
			return []string{"/usr/lib/x86_64-linux-gnu/libasound.so.2"}, nil
		}
		return nil, nil
	}
	if c := statusOf(t, Run(context.Background(), Config{}, env, Options{}), "audio"); c.Status != OK {
		t.Errorf("alsa: %+v", c)
	}
	w.goos = "darwin"
	if _, ok := run(t, w, Config{}, Options{}).Check("audio"); ok {
		t.Error("audio checked on macOS")
	}
	w.goos = "android"
	c = statusOf(t, run(t, w, Config{}, Options{}), "audio")
	if c.Status != Fail || !strings.Contains(c.Fix, "module-aaudio-sink") {
		t.Errorf("termux: %+v", c)
	}
}

func TestTheProbeRunsOnlyWhenAskedAndAFailureIsExplained(t *testing.T) {
	w := healthy()
	if _, ok := run(t, w, Config{}, Options{}).Check("probe"); ok || w.probed != 0 {
		t.Fatalf("probe ran without being asked")
	}
	if c := statusOf(t, run(t, w, Config{}, Options{Probe: true}), "probe"); c.Status != OK || w.probed != 1 {
		t.Fatalf("%+v", c)
	}
	w.probeErr = errors.New("ERROR: unable to download video data: HTTP Error 403: Forbidden")
	r := run(t, w, Config{}, Options{Probe: true})
	c := statusOf(t, r, "probe")
	if c.Status != Fail || !r.Failed() {
		t.Fatalf("%+v", c)
	}
	if !strings.Contains(c.Detail, "yt-dlp 2026.08.19 is old or has no JS runtime:") || !strings.Contains(c.Detail, "403") {
		t.Errorf("detail %q", c.Detail)
	}
	w.probeErr = errors.New("dial tcp: no route to host")
	c = statusOf(t, run(t, w, Config{}, Options{Probe: true}), "probe")
	if c.Status != Fail || strings.Contains(c.Detail, "is old or has no JS runtime") {
		t.Errorf("a network error was blamed on yt-dlp: %+v", c)
	}
}

func TestTheNativeEngineHook(t *testing.T) {
	w := healthy()
	env := w.env()
	if _, ok := Run(context.Background(), Config{}, env, Options{}).Check("native"); ok {
		t.Error("a native check without an engine")
	}
	env.Native = func(context.Context) *Check {
		return &Check{ID: "native", Name: "native engine", Status: Warn, Detail: "not built", Fix: "none"}
	}
	r := Run(context.Background(), Config{}, env, Options{})
	if c := statusOf(t, r, "native"); c.Status != Warn || r.Failed() {
		t.Errorf("%+v", c)
	}
}

func TestErrNamesEveryFailureWithItsFixAndNotTheWarnings(t *testing.T) {
	w := healthy()
	delete(w.programs, "mpv")
	delete(w.programs, "yt-dlp")
	err := run(t, w, Config{}, Options{}).Err()
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"mpv", "yt-dlp", "apt install mpv", "/music setup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	w = healthy()
	w.ytVerb = verbose("none", "0.8.0")
	if err := run(t, w, Config{}, Options{}).Err(); err != nil {
		t.Errorf("a warning refused playback: %v", err)
	}
}

func TestExplainAndRecogniseExtractionFailures(t *testing.T) {
	got := Explain("2026.08.19", false, "linux")
	if !strings.HasPrefix(got, "yt-dlp 2026.08.19 is old or has no JS runtime: ") || !strings.Contains(got, "yt-dlp -U") {
		t.Errorf("%q", got)
	}
	if got := Explain("2026.08.19", true, "linux"); !strings.Contains(got, "/music setup") {
		t.Errorf("%q", got)
	}
	for text, want := range map[string]bool{
		"ERROR: unable to download video data: HTTP Error 403: Forbidden":           true,
		"ERROR: [youtube] x: Requested format is not available. Use --list-formats": true,
		"Sign in to confirm you’re not a bot":                                       true,
		"WARNING: [youtube] n challenge solving failed":                             true,
		"ERROR: Unable to connect to proxy":                                         false,
		"HTTP Error 404: Not Found":                                                 false,
		"":                                                                          false,
	} {
		if LooksLikeExtractionFailure(text) != want {
			t.Errorf("%q: want %v", text, want)
		}
	}
}

func TestParsers(t *testing.T) {
	if v := ParseVerbose(verboseNone); v.Version != "2026.08.19" || len(v.Runtimes) != 0 || v.EJS != "0.8.0" {
		t.Errorf("%+v", v)
	}
	if v := ParseVerbose("[debug] yt-dlp version nightly@2026.09.02.232103 from yt-dlp/yt-dlp-nightly-builds [abc] (win_exe)\n[debug] JS runtimes: deno-2.5.0, node-24.1.0\n"); v.Version != "2026.09.02.232103" || len(v.Runtimes) != 2 || v.Runtimes[1] != "node-24.1.0" || v.EJS != "" {
		t.Errorf("%+v", v)
	}
	if d, ok := parseYtdlpDate("2026.09.02.232103"); !ok || d.Day() != 2 {
		t.Errorf("date %v %v", d, ok)
	}
	if _, ok := parseYtdlpDate("stable"); ok {
		t.Error("parsed 'stable'")
	}
	if v, _ := parseVersion("v24.19.0"); v != (Version{24, 19, 0}) {
		t.Errorf("%v", v)
	}
	if !(Version{2, 3, 0}).AtLeast(Version{2, 3, 0}) || (Version{2, 2, 9}).AtLeast(Version{2, 3, 0}) || !(Version{3, 0, 0}).AtLeast(Version{2, 9, 9}) {
		t.Error("AtLeast")
	}
}

// ── fake programs on PATH ───────────────────────────────────────────────────

func script(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRealProgramsOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	script(t, dir, "mpv", `echo "mpv 0.37.0 Copyright"`)
	script(t, dir, "yt-dlp", `if [ "$1" = "--version" ]; then echo 2026.08.19; exit 0; fi
echo "[debug] yt-dlp version stable@2026.08.19 from yt-dlp/yt-dlp [x] (zip)" >&2
echo "[debug] Optional libraries: yt_dlp_ejs-0.8.0" >&2
echo "[debug] JS runtimes: none" >&2
exit 2`)
	script(t, dir, "node", `echo v24.1.0`)
	env := SystemEnv()
	env.Now = func() time.Time { return now }
	env.Exists = func(string) bool { return true } // audio sockets are not what this test is about
	r := Run(context.Background(), Config{}, env, Options{})
	if runtime.GOOS == "linux" && r.Failed() {
		t.Fatalf("%s", r.Format())
	}
	if c := statusOf(t, r, "js-runtime"); c.Status == Fail {
		t.Errorf("%+v", c)
	}
	if r.MPVPath != filepath.Join(dir, "mpv") || r.YtdlpVersion != "2026.08.19" {
		t.Errorf("%+v", r)
	}
	// Remove mpv: one failure, found the way a user's shell would not find it.
	if err := os.Remove(filepath.Join(dir, "mpv")); err != nil {
		t.Fatal(err)
	}
	r = Run(context.Background(), Config{}, env, Options{})
	if c := statusOf(t, r, "mpv"); c.Status != Fail {
		t.Errorf("%+v", c)
	}
	// A yt-dlp that crashes is a failure with its output, not a hang.
	script(t, dir, "yt-dlp", `echo boom >&2; exit 1`)
	r = Run(context.Background(), Config{}, env, Options{})
	if c := statusOf(t, r, "yt-dlp"); c.Status != Fail || !strings.Contains(c.Detail, "boom") {
		t.Errorf("%+v", c)
	}
}

// ── the probe ───────────────────────────────────────────────────────────────

func TestProbeStreamReadsARangeAndReportsA403(t *testing.T) {
	var gotRange string
	srv := httptestServer(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		if r.URL.Path == "/forbidden" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, 64<<10))
	})
	defer srv.Close()
	runner := func(path string) ytdlp.Runner {
		return func(_ context.Context, bin string, args []string) ([]byte, []byte, error) {
			if !strings.Contains(strings.Join(args, " "), "--js-runtimes node") {
				t.Errorf("js runtime missing from %v", args)
			}
			return []byte(srv.URL + path + "\n"), nil, nil
		}
	}
	if err := probeStream(context.Background(), runner("/ok"), srv.Client(), "yt-dlp", "node", "https://x/watch?v=1"); err != nil || gotRange != "bytes=0-65535" {
		t.Fatalf("err %v range %q", err, gotRange)
	}
	err := probeStream(context.Background(), runner("/forbidden"), srv.Client(), "yt-dlp", "node", "https://x/watch?v=1")
	if err == nil || !LooksLikeExtractionFailure(err.Error()) {
		t.Fatalf("403: %v", err)
	}
	failing := func(context.Context, string, []string) ([]byte, []byte, error) {
		return nil, []byte("ERROR: [youtube] x: Requested format is not available\n"), errors.New("exit status 1")
	}
	if err := probeStream(context.Background(), failing, srv.Client(), "yt-dlp", "", "u"); err == nil || !LooksLikeExtractionFailure(err.Error()) {
		t.Fatalf("resolve failure: %v", err)
	}
}

func httptestServer(h http.HandlerFunc) *httptest.Server { return httptest.NewServer(h) }

func TestAnAudioOutputChosenForMPVIsNotSecondGuessed(t *testing.T) {
	w := healthy()
	w.exists = map[string]bool{}
	env := w.env()
	env.Getenv = func(k string) string {
		if k == "PIG_MUSIC_MPV_ARGS" {
			return "--ao=null"
		}
		return ""
	}
	r := Run(context.Background(), Config{}, env, Options{})
	if c := statusOf(t, r, "audio"); c.Status != Skipped || r.Failed() {
		t.Errorf("%+v", c)
	}
}

func TestMPVArgsPassTheDetectedRuntime(t *testing.T) {
	if got := (Report{JSRuntimeArg: "deno:/d/deno"}).MPVArgs(); len(got) != 1 || got[0] != "--ytdl-raw-options-append=js-runtimes=deno:/d/deno" {
		t.Errorf("%v", got)
	}
	if got := (Report{}).MPVArgs(); got != nil {
		t.Errorf("%v", got)
	}
}

// ── the library's browser ───────────────────────────────────────────────────

func TestDoctorNamesTheDefaultBrowserAndNeverReadsACookie(t *testing.T) {
	w := healthy()
	w.goos = "darwin"
	var ran []string
	env := w.env()
	inner := env.Run
	env.Run = func(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
		if strings.HasSuffix(bin, "plutil") {
			ran = append(ran, strings.Join(args, " "))
			return []byte(`{"LSHandlers":[{"LSHandlerRoleAll":"com.google.chrome","LSHandlerURLScheme":"https"}]}`), nil, nil
		}
		return inner(ctx, bin, args)
	}
	env.Getenv = func(k string) string {
		if k == "HOME" {
			return "/Users/u"
		}
		return ""
	}
	dir := t.TempDir()
	c := statusOf(t, Run(context.Background(), Config{DataDir: dir}, env, Options{}), "library")
	if c.Status != OK || !strings.Contains(c.Detail, "chrome (the default browser, com.google.chrome)") || !strings.Contains(c.Detail, "not yet agreed") {
		t.Fatalf("%+v", c)
	}
	if len(ran) != 1 || !strings.Contains(ran[0], "launchservices.secure.plist") {
		t.Errorf("ran %v", ran)
	}
	if err := (cookies.Consent{Path: filepath.Join(dir, "cookie-consent.json")}).Grant("chrome"); err != nil {
		t.Fatal(err)
	}
	if c := statusOf(t, Run(context.Background(), Config{DataDir: dir}, env, Options{}), "library"); !strings.Contains(c.Detail, "agreed") || strings.Contains(c.Detail, "not yet") {
		t.Errorf("%+v", c)
	}
}

func TestDoctorSaysWhenThereIsNoBrowserAndWhichSettingToUse(t *testing.T) {
	w := healthy() // linux, no DISPLAY in the fake environment
	c := statusOf(t, run(t, w, Config{DataDir: t.TempDir()}, Options{}), "library")
	if c.Status != Warn || !strings.Contains(c.Detail, "no graphical session") || !strings.Contains(c.Fix, "cookieBrowser") {
		t.Fatalf("%+v", c)
	}
	w.goos = "android"
	if c := statusOf(t, run(t, w, Config{DataDir: t.TempDir()}, Options{}), "library"); c.Status != Warn || !strings.Contains(c.Fix, "cookieBrowser") {
		t.Errorf("termux: %+v", c)
	}
	c = statusOf(t, run(t, w, Config{DataDir: t.TempDir(), CookieBrowser: "firefox:default-release"}, Options{}), "library")
	if c.Status != OK || !strings.Contains(c.Detail, "firefox:default-release (the cookieBrowser setting)") {
		t.Errorf("setting: %+v", c)
	}
	w.goos = "linux"
	if r := run(t, w, Config{DataDir: t.TempDir(), CookieBrowser: "arc"}, Options{}); r.Failed() || statusOf(t, r, "library").Status != Warn {
		t.Errorf("an unsupported setting must warn, not fail: %s", r.Format())
	}
}

// M9c: yt-dlp --version and yt-dlp -v are independent runs (0.8 s and 1.2 s on a laptop, one after the other before), so they
// run together; the answers are the same.
func TestTheYtdlpVersionAndItsDebugHeaderAreAskedTogether(t *testing.T) {
	w := healthy()
	env := w.env()
	inner := env.Run
	var mu sync.Mutex
	started := map[string]bool{}
	both := make(chan struct{})
	env.Run = func(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
		if strings.HasSuffix(bin, "yt-dlp") {
			kind := "version"
			if args[len(args)-1] == "-v" {
				kind = "verbose"
			}
			mu.Lock()
			started[kind] = true
			if len(started) == 2 {
				close(both)
			}
			mu.Unlock()
			select {
			case <-both:
			case <-time.After(2 * time.Second):
				return nil, []byte("the other yt-dlp run never started while this one was waiting"), errors.New("serial")
			}
		}
		return inner(ctx, bin, args)
	}
	rep := Run(context.Background(), Config{}, env, Options{})
	if rep.Failed() {
		t.Fatalf("%s", rep.Format())
	}
	if rep.YtdlpVersion != "2026.08.19" || rep.JSRuntimeArg != "" || !strings.Contains(rep.Format(), "deno") {
		t.Errorf("a different report:\n%s", rep.Format())
	}
}

// With no runtime by default and node on PATH, the run that names node is the one that is needed, so it is asked at the same
// time as the plain one, not after it.
func TestTheRunThatNamesNodeIsAskedWithThePlainOne(t *testing.T) {
	w := healthy()
	w.ytVerb = verboseNone
	w.programs["node"] = "/usr/bin/node"
	w.runtimes["/usr/bin/node"] = "v24.19.0"
	env := w.env()
	inner := env.Run
	var mu sync.Mutex
	started := map[string]bool{}
	both := make(chan struct{})
	env.Run = func(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
		if strings.HasSuffix(bin, "yt-dlp") && args[len(args)-1] == "-v" {
			mu.Lock()
			started[strings.Join(args, " ")] = true
			if len(started) == 2 {
				close(both)
			}
			mu.Unlock()
			select {
			case <-both:
			case <-time.After(2 * time.Second):
				return nil, []byte("serial"), errors.New("serial")
			}
		}
		return inner(ctx, bin, args)
	}
	rep := Run(context.Background(), Config{}, env, Options{})
	if rep.JSRuntimeArg != "node" {
		t.Errorf("runtime %q:\n%s", rep.JSRuntimeArg, rep.Format())
	}
}
