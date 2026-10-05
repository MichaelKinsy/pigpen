// Package doctor checks that this machine can play music, and says how to fix what it cannot: mpv, yt-dlp (version,
// age, who keeps it current), a JavaScript runtime for yt-dlp (YouTube needs one for most formats), yt-dlp's EJS
// scripts, a way to make sound, and, when asked, one real resolve and stream read. Every failure comes with one
// copy-paste fix for the operating system. The checks take their world from Env, so tests use no network and no real
// programs.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/cookies"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/ytdlp"
)

// Status is how a check came out.
type Status int

const (
	OK Status = iota
	// Warn: playback may work but is likely to break or lose formats. It does not stop the player.
	Warn
	// Fail: the player cannot work and refuses to start.
	Fail
	// Skipped: not checked, because something it needs failed first.
	Skipped
)

func (s Status) String() string { return [...]string{"ok", "warn", "FAIL", "skip"}[s] }

// Check is one result: what was looked at, what was found, and one line that fixes it.
type Check struct {
	ID     string // mpv, yt-dlp, js-runtime, ejs, audio, probe, native
	Name   string
	Status Status
	Detail string
	Fix    string
}

// Config is the user's settings that matter here.
type Config struct {
	MPVPath   string // "mpvPath": a program to use instead of mpv on PATH
	YtdlpPath string // "ytdlpPath"
	// JSRuntime is the "jsRuntime" setting, in yt-dlp's --js-runtimes syntax: "deno", "node:/path/to/node", ...
	JSRuntime string
	// DataDir is the extension's own data directory. A yt-dlp or Deno in DataDir/bin is the one pig-music keeps.
	DataDir string
	// CookieBrowser is the "cookieBrowser" setting (yt-dlp's BROWSER[:PROFILE]); empty means the system's default browser.
	CookieBrowser string
}

// Options choose the slower checks.
type Options struct {
	// Probe resolves one track and reads 64 KiB of its stream (network).
	Probe bool
}

// Env is the machine, as the checks see it.
type Env struct {
	GOOS, GOARCH string
	Getenv       func(string) string
	LookPath     func(string) (string, error)
	// Run runs a program and returns its output; err is non-nil when it could not run or exited non-zero.
	Run    func(ctx context.Context, bin string, args []string) (stdout, stderr []byte, err error)
	Now    func() time.Time
	Exists func(path string) bool
	Glob   func(pattern string) ([]string, error)
	// Probe resolves one track with yt-dlp and reads a first piece of its stream.
	Probe func(ctx context.Context, ytdlpPath, jsRuntimeArg string) error
	// Native, when set, reports the health of the native engine (a separate lane provides it).
	Native func(ctx context.Context) *Check
}

// SystemEnv is the real machine.
func SystemEnv() Env {
	return Env{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Run:      ytdlp.ExecRunner,
		Now:      time.Now,
		Exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
		Glob:     filepath.Glob,
		Probe:    ProbeStream,
	}
}

// Report is what Run found.
type Report struct {
	Checks []Check
	// The programs the player should use, resolved.
	MPVPath, YtdlpPath, YtdlpVersion string
	// JSRuntimeArg is what to pass as --js-runtimes (and to mpv as js-runtimes=...); "" lets yt-dlp choose.
	JSRuntimeArg string
	// SelfManaged: the yt-dlp is the copy pig-music downloaded, which pig-music keeps current.
	SelfManaged bool
	GOOS        string
}

// Check returns the check with the given ID.
func (r Report) Check(id string) (Check, bool) {
	for _, c := range r.Checks {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

// Failed reports whether any check is a Fail.
func (r Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

// SetupError is the error that refuses to start: every failure, each with its fix.
type SetupError struct{ Failures []Check }

func (e *SetupError) Error() string {
	var b strings.Builder
	b.WriteString("pig-music cannot play yet:")
	for _, c := range e.Failures {
		fmt.Fprintf(&b, "\n- %s: %s", c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(&b, "\n  fix: %s", c.Fix)
		}
	}
	return b.String()
}

// Err is nil when nothing failed (warnings do not stop the player), else a *SetupError.
func (r Report) Err() error {
	var failed []Check
	for _, c := range r.Checks {
		if c.Status == Fail {
			failed = append(failed, c)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return &SetupError{Failures: failed}
}

// Format is the report as lines of text.
func (r Report) Format() string {
	var b strings.Builder
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "%-4s %s: %s\n", c.Status, c.Name, c.Detail)
		if c.Fix != "" && c.Status != OK && c.Status != Skipped {
			fmt.Fprintf(&b, "     fix: %s\n", c.Fix)
		}
	}
	return b.String()
}

const (
	minMPV         = "0.35"
	ytdlpWarnAge   = 60 * 24 * time.Hour
	ytdlpFailAge   = 150 * 24 * time.Hour
	programTimeout = 20 * time.Second
)

// Run checks the machine.
func Run(ctx context.Context, cfg Config, env Env, opts Options) Report {
	r := Report{GOOS: env.GOOS}
	// The programs are asked together where one does not need another's answer: mpv, `yt-dlp --version` and yt-dlp's debug
	// header (the slowest, about a second) each take most of their time starting up. The checks write different Report fields.
	mpvDone := make(chan Check, 1)
	go func() { mpvDone <- checkMPV(ctx, cfg, env, &r) }()
	pre := startVerbose(ctx, cfg, env)
	yt := checkYtdlp(ctx, cfg, env, &r)
	r.add(<-mpvDone)
	r.add(yt)
	if yt.Status == Fail {
		r.add(Check{ID: "js-runtime", Name: "JavaScript runtime", Status: Skipped, Detail: "needs yt-dlp"})
		r.add(Check{ID: "ejs", Name: "yt-dlp-ejs", Status: Skipped, Detail: "needs yt-dlp"})
	} else {
		js, ejs := checkJS(ctx, cfg, env, &r, pre)
		r.add(js)
		r.add(ejs)
	}
	if c := checkAudio(ctx, env); c != nil {
		r.add(*c)
	}
	r.add(checkLibrary(ctx, cfg, env))
	if opts.Probe && yt.Status != Fail && env.Probe != nil {
		r.add(checkProbe(ctx, env, &r))
	}
	if env.Native != nil {
		if c := env.Native(ctx); c != nil {
			r.add(*c)
		}
	}
	return r
}

func (r *Report) add(c Check) { r.Checks = append(r.Checks, c) }

func runProg(ctx context.Context, env Env, bin string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, programTimeout)
	defer cancel()
	return env.Run(ctx, bin, args)
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

func checkMPV(ctx context.Context, cfg Config, env Env, r *Report) Check {
	c := Check{ID: "mpv", Name: "mpv"}
	look := cfg.MPVPath
	if look == "" {
		look = "mpv"
	}
	path, err := env.LookPath(look)
	if err != nil {
		c.Status, c.Detail, c.Fix = Fail, "not found ("+look+")", fixMPV(env.GOOS)
		return c
	}
	r.MPVPath = path
	stdout, stderr, err := runProg(ctx, env, path, "--version")
	if err != nil && len(stdout) == 0 {
		c.Status, c.Detail, c.Fix = Fail, fmt.Sprintf("%s did not run: %v %s", path, err, firstLine(stderr)), fixMPV(env.GOOS)
		return c
	}
	v, ok := parseMPVVersion(string(stdout))
	if !ok {
		c.Status, c.Detail, c.Fix = Fail, fmt.Sprintf("%s printed no version: %q", path, firstLine(stdout)), fixMPV(env.GOOS)
		return c
	}
	min, _ := parseVersion(minMPV)
	if !v.AtLeast(min) {
		c.Status, c.Detail, c.Fix = Fail, fmt.Sprintf("mpv %s is older than %s (%s)", v, minMPV, path), fixMPV(env.GOOS)
		return c
	}
	c.Status, c.Detail = OK, fmt.Sprintf("%s (%s)", v, path)
	return c
}

func exeName(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// resolveYtdlp: a configured path wins; then the copy pig-music keeps in DataDir/bin; then PATH.
func resolveYtdlp(cfg Config, env Env) (path string, self bool, err error) {
	if cfg.YtdlpPath != "" {
		p, err := env.LookPath(cfg.YtdlpPath)
		return p, false, err
	}
	if cfg.DataDir != "" {
		own := filepath.Join(cfg.DataDir, "bin", exeName("yt-dlp", env.GOOS))
		if env.Exists(own) {
			return own, true, nil
		}
	}
	p, err := env.LookPath("yt-dlp")
	return p, false, err
}

func managedBy(path string, self bool) string {
	if self {
		return "self-managed by pig-music"
	}
	for _, prefix := range []string{"/usr/bin/", "/bin/", "/opt/homebrew/", "/home/linuxbrew/", "/snap/", "/data/data/com.termux/files/usr/bin/"} {
		if strings.HasPrefix(path, prefix) {
			return "from a package manager"
		}
	}
	return "installed by you"
}

func checkYtdlp(ctx context.Context, cfg Config, env Env, r *Report) Check {
	c := Check{ID: "yt-dlp", Name: "yt-dlp"}
	path, self, err := resolveYtdlp(cfg, env)
	if err != nil {
		c.Status, c.Detail, c.Fix = Fail, "not found", fixYtdlp(env.GOOS)
		return c
	}
	stdout, stderr, err := runProg(ctx, env, path, "--version")
	version := firstLine(stdout)
	if err != nil || version == "" {
		c.Status, c.Fix = Fail, fixYtdlp(env.GOOS)
		c.Detail = fmt.Sprintf("%s did not run: %v %s", path, err, firstLine(stderr))
		return c
	}
	r.YtdlpPath, r.YtdlpVersion, r.SelfManaged = path, version, self
	who := managedBy(path, self)
	c.Status, c.Detail = OK, fmt.Sprintf("%s, %s (%s)", version, who, path)
	released, ok := parseYtdlpDate(version)
	if !ok {
		return c
	}
	age := env.Now().Sub(released)
	days := int(age.Hours() / 24)
	switch {
	case age > ytdlpFailAge:
		c.Status, c.Fix = Fail, fixYtdlpUpdate(env.GOOS, self)
		c.Detail = fmt.Sprintf("%s is %d days old, and YouTube changes faster than that; %s (%s)", version, days, who, path)
	case age > ytdlpWarnAge:
		c.Status, c.Fix = Warn, fixYtdlpUpdate(env.GOOS, self)
		c.Detail = fmt.Sprintf("%s is %d days old; %s (%s)", version, days, who, path)
	default:
		c.Detail = fmt.Sprintf("%s, %d days old, %s (%s)", version, days, who, path)
	}
	return c
}

// ytdlpVerbose runs `yt-dlp -v` with no URL, which prints its debug header and then complains about the missing URL.
func ytdlpVerbose(ctx context.Context, env Env, ytdlpPath, jsArg string) Verbose {
	// --ignore-config, as the Source runs it: a user's config can name --cookies-from-browser, and yt-dlp loads the
	// cookies as soon as it sets up its network layer, which -v does; it would also show runtimes the Source never gets.
	args := []string{"--ignore-config", "-v"}
	if jsArg != "" {
		args = []string{"--ignore-config", "--js-runtimes", jsArg, "-v"}
	}
	stdout, stderr, _ := runProg(ctx, env, ytdlpPath, args...) // the exit status is 2 by design
	return ParseVerbose(string(stderr) + "\n" + string(stdout))
}

// firstVerbose is yt-dlp's debug header for the JavaScript check, started before yt-dlp's version is known. When no runtime
// is named and one other than Deno is on PATH, the run that uses it is started at the same time, since it is the one that is
// needed whenever the plain run finds yt-dlp without a runtime.
type firstVerbose struct {
	path, arg  string
	notes      []string
	result     chan Verbose
	pathArg    string   // the --js-runtimes argument of a runtime found on PATH, "" when none
	pathNotes  []string // runtimes on PATH that are older than yt-dlp wants
	pathResult chan Verbose
}

// startVerbose starts those runs for the yt-dlp the config and PATH give (nil when there is none). The runtime argument is the
// setting's or the Deno pig-music downloaded.
func startVerbose(ctx context.Context, cfg Config, env Env) *firstVerbose {
	path, _, err := resolveYtdlp(cfg, env)
	if err != nil {
		return nil
	}
	return startVerboseFor(ctx, cfg, env, path)
}

func startVerboseFor(ctx context.Context, cfg Config, env Env, path string) *firstVerbose {
	arg, notes := ownRuntime(ctx, cfg, env)
	f := &firstVerbose{path: path, arg: arg, notes: notes, result: make(chan Verbose, 1)}
	go func() { f.result <- ytdlpVerbose(ctx, env, path, arg) }()
	if arg == "" {
		for _, spec := range runtimeSpecs[1:] {
			rt, err := env.LookPath(spec.Program)
			if err != nil {
				continue
			}
			if ver, ok := runtimeVersion(ctx, env, rt, spec); ok {
				f.pathArg, f.pathResult = spec.Name, make(chan Verbose, 1)
				go func() { f.pathResult <- ytdlpVerbose(ctx, env, path, f.pathArg) }()
				break
			} else {
				f.pathNotes = append(f.pathNotes, fmt.Sprintf("%s %s is older than %s", spec.Program, ver, spec.Min))
			}
		}
	}
	return f
}

// ownRuntime is the --js-runtimes argument that is set by the user or by pig-music's own Deno download.
func ownRuntime(ctx context.Context, cfg Config, env Env) (arg string, notes []string) {
	arg = cfg.JSRuntime
	if arg == "" && cfg.DataDir != "" {
		own := filepath.Join(cfg.DataDir, "bin", exeName("deno", env.GOOS))
		if env.Exists(own) {
			if v, ok := runtimeVersion(ctx, env, own, runtimeSpecs[0]); ok {
				arg = "deno:" + own
			} else {
				notes = append(notes, fmt.Sprintf("the Deno in %s is %s, older than %s", own, v, runtimeSpecs[0].Min))
			}
		}
	}
	return arg, notes
}

func checkJS(ctx context.Context, cfg Config, env Env, r *Report, pre *firstVerbose) (js, ejs Check) {
	js = Check{ID: "js-runtime", Name: "JavaScript runtime"}
	ejs = Check{ID: "ejs", Name: "yt-dlp-ejs"}
	if pre == nil || pre.path != r.YtdlpPath {
		pre = startVerboseFor(ctx, cfg, env, r.YtdlpPath)
	}
	arg, notes, v := pre.arg, pre.notes, <-pre.result
	if arg == "" && len(v.Runtimes) == 0 { // none by default: the one on PATH, if there is one
		notes = append(notes, pre.pathNotes...)
		if pre.pathResult != nil {
			arg, v = pre.pathArg, <-pre.pathResult
		}
	}
	switch {
	case len(v.Runtimes) > 0:
		r.JSRuntimeArg = arg
		js.Status, js.Detail = OK, strings.Join(v.Runtimes, ", ")
		if arg != "" {
			js.Detail += " (pig-music passes --js-runtimes " + arg + ")"
		}
	default:
		js.Status, js.Fix = Warn, fixJS(env.GOOS)
		js.Detail = "yt-dlp has none enabled: some YouTube formats will be missing and playback may fail"
		if len(notes) > 0 {
			js.Detail += " (" + strings.Join(notes, "; ") + ")"
		}
		if cfg.JSRuntime != "" {
			js.Detail += fmt.Sprintf(" (the jsRuntime setting %q is not one yt-dlp can use)", cfg.JSRuntime)
		}
	}
	if v.EJS != "" {
		ejs.Status, ejs.Detail = OK, v.EJS
	} else {
		ejs.Status, ejs.Detail, ejs.Fix = Warn, "yt-dlp has no yt-dlp-ejs, the scripts that solve YouTube's challenges for the runtime", fixEJS(env.GOOS)
	}
	return js, ejs
}

// runtimeVersion runs `<program> --version` and reports its version and whether it meets spec.Min.
func runtimeVersion(ctx context.Context, env Env, path string, spec runtimeSpec) (Version, bool) {
	stdout, stderr, _ := runProg(ctx, env, path, spec.Args...)
	v, ok := parseVersion(firstLine(append(stdout, stderr...)))
	return v, ok && v.AtLeast(spec.Min)
}

func checkAudio(ctx context.Context, env Env) *Check {
	c := Check{ID: "audio", Name: "audio output"}
	if args := env.Getenv("PIG_MUSIC_MPV_ARGS"); strings.Contains(args, "--ao=") || strings.Contains(args, "--ao ") {
		c.Status, c.Detail = Skipped, "an audio output is chosen in PIG_MUSIC_MPV_ARGS"
		return &c
	}
	switch env.GOOS {
	case "android":
		if p, err := env.LookPath("pactl"); err == nil {
			if _, _, err := runProg(ctx, env, p, "info"); err == nil {
				c.Status, c.Detail = OK, "PulseAudio answers"
				return &c
			}
		}
		c.Status, c.Detail, c.Fix = Fail, "no PulseAudio server answers (Termux needs one, with module-aaudio-sink on Android 16)", fixAudio(env.GOOS)
		return &c
	case "linux":
	default:
		return nil
	}
	if env.Getenv("PULSE_SERVER") != "" {
		c.Status, c.Detail = OK, "PULSE_SERVER is set"
		return &c
	}
	if run := env.Getenv("XDG_RUNTIME_DIR"); run != "" {
		for _, name := range []string{"pulse/native", "pipewire-0"} {
			if p := filepath.Join(run, name); env.Exists(p) {
				c.Status, c.Detail = OK, p
				return &c
			}
		}
	}
	for _, pattern := range []string{"/usr/lib/*/libasound.so.2", "/lib/*/libasound.so.2", "/usr/lib64/libasound.so.2", "/usr/lib/libasound.so.2"} {
		if m, _ := env.Glob(pattern); len(m) > 0 {
			c.Status, c.Detail = OK, "ALSA ("+m[0]+")"
			return &c
		}
	}
	c.Status, c.Detail, c.Fix = Fail, "no PulseAudio or PipeWire socket and no libasound.so.2", fixAudio(env.GOOS)
	return &c
}

func checkProbe(ctx context.Context, env Env, r *Report) Check {
	c := Check{ID: "probe", Name: "resolve and stream"}
	err := env.Probe(ctx, r.YtdlpPath, r.JSRuntimeArg)
	if err == nil {
		c.Status, c.Detail = OK, "resolved a track and read 64 KiB of its stream"
		return c
	}
	c.Status = Fail
	text := strings.TrimSpace(err.Error())
	if LooksLikeExtractionFailure(text) {
		c.Detail = Explain(r.YtdlpVersion, r.SelfManaged, env.GOOS) + " (" + firstLine([]byte(text)) + ")"
		c.Fix = fixYtdlpUpdate(env.GOOS, r.SelfManaged)
	} else {
		c.Detail = firstLine([]byte(text))
		c.Fix = "check the network connection, then run the doctor again"
	}
	return c
}

var errNoURL = errors.New("yt-dlp printed no stream URL")

// ConfigFrom is the doctor's configuration from the environment and the settings file: the PIG_MUSIC_* variables win.
func ConfigFrom(getenv func(string) string, s music.Settings, p music.Paths) Config {
	pick := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	return Config{
		MPVPath:   pick(getenv("PIG_MUSIC_MPV"), s.MPVPath),
		YtdlpPath: pick(getenv("PIG_MUSIC_YTDLP"), s.YtdlpPath),
		JSRuntime: pick(getenv("PIG_MUSIC_JS_RUNTIME"), s.JSRuntime),
		DataDir:   p.Data,

		CookieBrowser: s.CookieBrowser,
	}
}

// EnvFrom is the real machine with the given environment and program lookup (either may be nil).
func EnvFrom(getenv func(string) string, lookPath func(string) (string, error)) Env {
	e := SystemEnv()
	if getenv != nil {
		e.Getenv = getenv
	}
	if lookPath != nil {
		e.LookPath = lookPath
	}
	return e
}

// MPVArgs are the extra arguments mpv needs so that its yt-dlp hook uses the runtime the doctor found.
func (r Report) MPVArgs() []string {
	if r.JSRuntimeArg == "" {
		return nil
	}
	// -append adds one entry without replacing the list (mpv adds ignore-config there) and takes the value as it
	// is, so a runtime path with a comma survives.
	return []string{"--ytdl-raw-options-append=js-runtimes=" + r.JSRuntimeArg}
}

// CookieEnv is the machine as the cookies package asks about it: the default browser, and nothing more.
func CookieEnv(ctx context.Context, env Env) cookies.Env {
	return cookies.Env{
		GOOS: env.GOOS, Getenv: env.Getenv,
		Run: func(name string, args ...string) (string, error) {
			stdout, _, err := runProg(ctx, env, name, args...)
			return string(stdout), err
		},
		Home: env.Getenv("HOME"),
	}
}

// ConsentPath is where the user's agreement to read a browser's cookies is kept.
func ConsentPath(dataDir string) string { return filepath.Join(dataDir, "cookie-consent.json") }

// checkLibrary says which browser the library would be read from, by name only. It never reads a cookie and it never
// fails: without a browser the player works and the Library tab says why it cannot list anything.
func checkLibrary(ctx context.Context, cfg Config, env Env) Check {
	c := Check{ID: "library", Name: "library (browser cookies)"}
	choice := cookies.Resolve(cfg.CookieBrowser, CookieEnv(ctx, env))
	if choice.Spec == "" {
		c.Status, c.Detail = Warn, choice.Problem
		c.Fix = `set "cookieBrowser" in the pig-music settings to one of: ` + strings.Join(cookies.Supported, ", ")
		return c
	}
	agreed := "you have not yet agreed to it being read; the Library tab will ask once"
	if (cookies.Consent{Path: ConsentPath(cfg.DataDir)}).Granted(choice.Browser) {
		agreed = "you have agreed to it being read for library listings"
	}
	c.Status, c.Detail = OK, choice.Describe()+"; "+agreed
	return c
}
