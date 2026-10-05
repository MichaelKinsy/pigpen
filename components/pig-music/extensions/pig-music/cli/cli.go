// Package cli is the pigmusic command line: the player core (mpv) and the
// source (yt-dlp) with no UI around them. It exists to prove that mpv outlives
// the process that started it: run `pigmusic play`, let the command end, and the
// music keeps going; the next command finds the same mpv.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/cookies"
	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
	"github.com/MichaelKinsy/pigpen/pig-music/engine"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"github.com/MichaelKinsy/pigpen/pig-music/prefetch"
	"github.com/MichaelKinsy/pigpen/pig-music/selfmanage"
	"github.com/MichaelKinsy/pigpen/pig-music/ytdlp"
)

// IO is the command's standard streams.
type IO struct {
	In       io.Reader
	Out, Err io.Writer
}

// Env is what the command reads from its surroundings. The zero value is the real one.
type Env struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// Source replaces the yt-dlp source (tests).
	Source music.Source
	// Searcher replaces the direct YouTube Music search (tests); nil means the real one (native.NewSearcher).
	Searcher music.Source
	// Configure adjusts the mpv player's configuration (tests).
	Configure func(*mpv.Config)
	// Doctor replaces the machine the doctor looks at (tests).
	Doctor *doctor.Env
	// Serve is `pigmusic serve`, the native player's daemon, and Native is `pigmusic native <playlist|probe> <id>`, the helper
	// it answers the extension with. Both link WaxTap, WaxFlow and oto, so only the pigmusic program supplies them: a
	// caller without them (the extension) does not link the engine at all.
	Serve  func(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int
	Native func(ctx context.Context, args []string, stdout, stderr io.Writer) int
	// Name is how hints name this command: "pigmusic" (the default) or "/music" inside PiG.
	Name string
}

const usage = `usage: pigmusic <command> [arguments]

  search <query...> [-n N]   search YouTube Music and list the results
  play [<number>|<query...>] play result <number> of the last search (the whole list
                             queues behind it), or search and play; no argument resumes
  add <number>|<query...>    append a result to the queue
  queue                      show the queue
  status                     show what is playing
  pause | resume | toggle
  next | prev
  now                        the same as status: one line
  shuffle on|off | repeat off|one|all
  jump <n> | remove <n> | move <from> <to>   queue positions, counted from 1
  seek <seconds>             forward, or back with a minus sign
  volume <0-100> (vol)
  stop                       stop the player and forget the queue
  check                      report which engine is used and whether it is ready
  serve [--dir D] [--output auto|oto|null] [--idle-exit 30m]
                             run the native engine's player in the foreground
                             (the native engine starts it detached by itself)
  doctor [--probe] [--timings]  check mpv, yt-dlp (version, age), a JavaScript runtime, audio;
                             --probe also resolves a track and reads its stream (network);
                             --timings lists how long each program took, then times a search
                             and a track's details, direct and through yt-dlp (network)
  library-counts             how many playlists and liked songs your account has (counts
                             only, never titles); asks first before reading the default
                             browser's cookies, once per browser
  setup                      the doctor, then offers (asking each time) to download yt-dlp
                             and Deno into pig-music's own directory

The player keeps playing when this command ends. Engines: mpv with yt-dlp, or a
native one with no external program; "engine" in settings.json or PIG_MUSIC_ENGINE
is auto (mpv when healthy, else native), mpv or native. Environment: PIG_MUSIC_MPV
and PIG_MUSIC_YTDLP name the programs, PIG_MUSIC_MPV_ARGS adds mpv arguments (for
example --ao=null), PIG_MUSIC_SERVE names the pigmusic program that runs the native
player, PIG_MUSIC_NATIVE_OUTPUT=null plays the native engine without sound.
`

// Run executes one command and returns the exit status: 0 on success, 1 on
// failure, 2 for a usage mistake.
func Run(ctx context.Context, args []string, io IO, env Env) int {
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	if env.LookPath == nil {
		env.LookPath = exec.LookPath
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(io.Err, usage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	if args[0] == "serve" { // the native player itself: no engine selection, no Player of ours
		if env.Serve == nil {
			fmt.Fprintln(io.Err, "pigmusic: serve is not part of this program; run the pigmusic program")
			return 1
		}
		return env.Serve(ctx, args[1:], env.Getenv, io.Err)
	}
	if args[0] == "native" { // the one-shot helper the extension runs (playlist listing, the doctor's stream probe)
		if env.Native == nil {
			fmt.Fprintln(io.Err, "pigmusic: native is not part of this program; run the pigmusic program")
			return 1
		}
		return env.Native(ctx, args[1:], io.Out, io.Err)
	}
	c := &command{ctx: ctx, io: io, env: env, verb: args[0]}
	if err := c.setup(); err != nil {
		fmt.Fprintln(io.Err, "pigmusic:", err)
		return 1
	}
	defer c.close()
	err := c.dispatch(args[0], args[1:])
	var u usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &u):
		fmt.Fprintf(io.Err, "pigmusic: %s\n\n%s", u.msg, usage)
		return 2
	default:
		fmt.Fprintln(io.Err, "pigmusic:", err)
		return 1
	}
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

type command struct {
	verb     string // the command being run
	ctx      context.Context
	io       IO
	env      Env
	paths    music.Paths
	settings music.Settings
	source   music.Source
	player   music.Player
	mpvPath  string
	ytdlPath string
	// choice is the engine in use; engineErr is why none could be chosen, which
	// only the commands that need a player report.
	choice    engine.Choice
	engineErr error
	running   func(context.Context) bool
	stopped   string // what a stopped player is called in messages

	baseCfg  mpv.Config
	prepared bool
}

func (c *command) setup() error {
	paths, err := music.DefaultPaths(c.env.Getenv)
	if err != nil {
		return err
	}
	c.paths = paths
	if c.settings, err = music.LoadSettings(paths.Settings); err != nil {
		return err
	}
	c.mpvPath = first(c.env.Getenv("PIG_MUSIC_MPV"), c.settings.MPVPath)
	c.ytdlPath = first(c.env.Getenv("PIG_MUSIC_YTDLP"), c.settings.YtdlpPath)
	deps := engine.Deps{Getenv: c.env.Getenv, LookPath: c.env.LookPath}
	mode, _ := engine.ParseMode(first(c.env.Getenv("PIG_MUSIC_ENGINE"), c.settings.Engine))
	if c.env.Source == nil && mode == engine.Auto { // auto falls back to native when the doctor finds mpv or yt-dlp unfit, not only absent
		deps.MPVHealthy = func(ctx context.Context, s music.Settings) error {
			return doctor.Run(ctx, doctor.ConfigFrom(c.env.Getenv, s, paths), c.doctorEnv(), doctor.Options{}).Err()
		}
	}
	// In auto mode, a player that is already running is the one to control. It is looked for first: choosing an engine runs
	// the doctor (seconds: mpv, yt-dlp twice, the browser lookup), which controlling a running player does not need. A command
	// that starts something (play, add) still runs the doctor in prepare.
	var choice engine.Choice
	switch {
	case mode != engine.Auto || engine.IsTermux(deps):
	case c.verb == "search" && c.env.Source == nil:
		// A search is the same whichever engine plays (the direct listing), so it does not wait for the doctor to judge mpv
		// and yt-dlp (about 2 s); if the direct listing fails, the yt-dlp fallback runs the doctor itself (prepare).
		choice = engine.Choice{Kind: engine.MPVEngine, Mode: engine.Auto, Reason: "a search needs no engine"}
	case mpv.Running(c.ctx, paths.Socket):
		choice = engine.Choice{Kind: engine.MPVEngine, Mode: engine.Auto, Reason: "an mpv is already running"}
	case native.Running(c.ctx, paths):
		choice = engine.Choice{Kind: engine.NativeEngine, Mode: engine.Auto, Reason: "the native player is already running"}
	}
	if choice.Kind == "" {
		var err error
		choice, err = engine.Select(c.ctx, c.settings, deps)
		switch {
		case err != nil && isModeError(err):
			return err
		case err != nil:
			c.engineErr = err
			choice = engine.Choice{Kind: engine.MPVEngine, Mode: engine.Auto}
		}
	}
	c.choice = choice
	if choice.Kind == engine.NativeEngine {
		src, player := engine.NewNative(c.settings, paths, deps)
		c.source, c.player = c.env.Source, player
		if c.source == nil {
			c.source = src
		}
		c.running = func(ctx context.Context) bool { return native.Running(ctx, paths) }
		c.stopped = "the native player is not running"
		return nil
	}
	c.source = c.env.Source
	if c.source == nil {
		inner := c.env.Searcher
		if inner == nil {
			inner = native.NewSearcher(c.env.Getenv)
		}
		access := c.cookieAccess()
		c.source = &ytdlp.Source{Bin: c.ytdlPath, Cookies: access.Spec, Inner: inner, Account: ytdlp.NewAccount(c.env.Getenv, c.ytdlPath, access.Spec)}
	}
	cfg := mpv.Config{
		MPVPath: c.mpvPath, YtdlPath: c.ytdlPath, Paths: paths, PlayURL: c.source.PlayURL,
		ExtraArgs: strings.Fields(c.env.Getenv("PIG_MUSIC_MPV_ARGS")), LookPath: c.env.LookPath,
	}
	if c.env.Configure != nil {
		c.env.Configure(&cfg)
	}
	c.baseCfg = cfg
	c.player = mpv.New(cfg)
	c.running = func(ctx context.Context) bool { return mpv.Running(ctx, paths.Socket) }
	c.stopped = "mpv is not running"
	return nil
}

// cookieAccess is the library's way to the browser's cookies: the cookieBrowser setting or the default browser, and the
// user's recorded consent. Only library calls use it.
func (c *command) cookieAccess() cookies.Access {
	return cookies.Access{
		Setting: c.settings.CookieBrowser,
		Env:     doctor.CookieEnv(c.ctx, c.doctorEnv()),
		Consent: cookies.Consent{Path: doctor.ConsentPath(c.paths.Data)},
	}
}

// isModeError reports a setting that names no engine, which fails every command.
func isModeError(err error) bool {
	return strings.Contains(err.Error(), "is not one of auto, mpv or native")
}

func (c *command) doctorEnv() doctor.Env {
	if c.env.Doctor != nil {
		return *c.env.Doctor
	}
	return doctor.EnvFrom(c.env.Getenv, c.env.LookPath)
}

// prepare runs the doctor before anything that needs yt-dlp or starts mpv, refuses with its message when something
// failed, and points the source and the player at the programs and the JavaScript runtime it found. A test that
// supplies its own Source skips it.
func (c *command) prepare() error {
	if c.prepared || c.env.Source != nil || c.choice.Kind != engine.MPVEngine {
		return nil
	}
	c.prepared = true
	rep := doctor.Run(c.ctx, doctor.ConfigFrom(c.env.Getenv, c.settings, c.paths), c.doctorEnv(), doctor.Options{})
	if err := rep.Err(); err != nil {
		return err
	}
	if src, ok := c.source.(*ytdlp.Source); ok {
		src.Bin, src.JSRuntime = rep.YtdlpPath, rep.JSRuntimeArg
	}
	c.mpvPath, c.ytdlPath = rep.MPVPath, rep.YtdlpPath
	cfg := c.baseCfg
	cfg.MPVPath, cfg.YtdlPath = rep.MPVPath, rep.YtdlpPath
	cfg.ExtraArgs = append(append([]string(nil), cfg.ExtraArgs...), rep.MPVArgs()...)
	_ = c.player.Close()
	c.player = mpv.New(cfg)
	return nil
}

func (c *command) doctor(args []string) error {
	opts := doctor.Options{}
	timings := false
	for _, a := range args {
		switch a {
		case "--probe":
			opts.Probe = true
		case "--timings":
			timings = true
		default:
			return usagef("doctor takes only --probe and --timings")
		}
	}
	env := c.doctorEnv()
	var steps []timedStep
	var stepsMu sync.Mutex // the doctor runs some programs together
	if timings {
		inner := env.Run
		env.Run = func(ctx context.Context, bin string, a []string) ([]byte, []byte, error) {
			start := time.Now()
			out, errOut, err := inner(ctx, bin, a)
			stepsMu.Lock()
			steps = append(steps, timedStep{name: stepName(bin, a), took: time.Since(start), err: err})
			stepsMu.Unlock()
			return out, errOut, err
		}
	}
	began := time.Now()
	rep := doctor.Run(c.ctx, doctor.ConfigFrom(c.env.Getenv, c.settings, c.paths), env, opts)
	wall := time.Since(began)
	fmt.Fprint(c.io.Out, rep.Format())
	if timings {
		c.printTimings(steps, wall, rep)
	}
	if rep.Failed() {
		return errors.New("the doctor found problems that stop playback; fix them and run `pigmusic doctor` again")
	}
	return nil
}

type timedStep struct {
	name string
	took time.Duration
	err  error
}

// stepName is "mpv --version" for a program the doctor ran: the program's name and its first words, nothing from a path.
func stepName(bin string, args []string) string {
	words := []string{filepath.Base(bin)}
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(words) < 2 {
			words = append(words, a)
		}
		if len(words) >= 3 {
			break
		}
	}
	return strings.Join(words, " ")
}

// printTimings lists what the doctor ran, then times the two things a person waits for, the way they are done now (one HTTP
// request each) and the way they were done (a yt-dlp run each).
func (c *command) printTimings(steps []timedStep, wall time.Duration, rep doctor.Report) {
	line := func(name string, took time.Duration, err error) {
		note := ""
		if err != nil {
			note = "  (failed: " + firstLine(err.Error()) + ")"
		}
		fmt.Fprintf(c.io.Out, "  %-34s %6d ms%s\n", name, took.Milliseconds(), note)
	}
	fmt.Fprintln(c.io.Out, "\ntimings (this machine, now):")
	for _, st := range steps {
		line("doctor: "+st.name, st.took, st.err)
	}
	line("doctor, all of it (some together)", wall, nil)
	const query = "never gonna give you up"
	video := "dQw4w9WgXcQ"
	searcher := c.env.Searcher
	if searcher == nil {
		searcher = native.NewSearcher(c.env.Getenv)
	}
	if searcher == nil {
		fmt.Fprintln(c.io.Out, "  search (direct): off (PIG_MUSIC_SEARCH=ytdlp)")
	} else {
		start := time.Now()
		tracks, err := searcher.Search(c.ctx, query, 10)
		line("search (direct, 10 results)", time.Since(start), err)
		if err == nil && len(tracks) > 0 {
			video = tracks[0].ID
		}
		if e, ok := searcher.(music.Enricher); ok {
			start = time.Now()
			_, err = e.Enrich(c.ctx, music.Track{ID: video})
			line("details (direct, one track)", time.Since(start), err)
		}
	}
	if rep.YtdlpPath == "" {
		fmt.Fprintln(c.io.Out, "  yt-dlp search and details: skipped (yt-dlp not found)")
		return
	}
	y := &ytdlp.Source{Bin: rep.YtdlpPath, JSRuntime: rep.JSRuntimeArg}
	start := time.Now()
	_, err := y.SearchViaYtdlp(c.ctx, query, 10)
	line("search (yt-dlp, 10 results)", time.Since(start), err)
	start = time.Now()
	_, err = y.Enrich(c.ctx, music.Track{ID: video})
	line("details (yt-dlp, one track)", time.Since(start), err)
	c.timeStream(rep.YtdlpPath, rep.JSRuntimeArg, video, line)
	fmt.Fprintln(c.io.Out, "  library (account): not timed here, it needs your cookies; /music's Library tab shows the first rows from the last session's cache and updates them behind")
}

// timeStream times what a track change waits for: yt-dlp resolving the stream (what mpv runs when a track starts), and the
// same answer when it was prefetched (what mpv's yt-dlp stand-in does when the next track was resolved while the last played).
func (c *command) timeStream(ytdl, jsRuntime, video string, line func(string, time.Duration, error)) {
	dir, err := os.MkdirTemp("", "pm-stream")
	if err != nil {
		return
	}
	defer os.RemoveAll(dir)
	cache := &prefetch.Cache{Dir: filepath.Join(dir, "ytdl"), Bin: ytdl, JSRuntime: jsRuntime, Runner: ytdlp.ExecRunner}
	watch := "https://music.youtube.com/watch?v=" + video
	start := time.Now()
	err = cache.Warm(c.ctx, watch)
	line("stream, what mpv waits for", time.Since(start), err)
	if err != nil {
		line("stream, prefetched", 0, errors.New("not measured: the stream was not resolved"))
		return
	}
	shim, err := cache.Shim()
	if err != nil {
		line("stream, prefetched", 0, err)
		return
	}
	start = time.Now()
	_, err = exec.CommandContext(c.ctx, shim, "--no-warnings", "-J", "--flat-playlist", "--sub-format", "ass/srt/best", "--format", "bestaudio", "--ignore-config", "--all-subs", "--no-playlist", "--", watch).Output()
	line("stream, prefetched", time.Since(start), err)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

// setup runs the doctor, then offers the downloads it can make, asking before each.
func (c *command) setupCmd() error {
	env := c.doctorEnv()
	cfg := doctor.ConfigFrom(c.env.Getenv, c.settings, c.paths)
	rep := doctor.Run(c.ctx, cfg, env, doctor.Options{})
	fmt.Fprint(c.io.Out, rep.Format())
	in := bufio.NewReader(c.io.In)
	ask := func(title, message string) (bool, error) {
		fmt.Fprintf(c.io.Out, "\n%s\n%s\nProceed? [y/N] ", title, message)
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return false, nil // no answer is a no
		}
		a := strings.ToLower(strings.TrimSpace(line))
		return a == "y" || a == "yes", nil
	}
	did, err := selfmanage.NewInstaller(c.paths.Data, env).Setup(c.ctx, rep, ask)
	for _, line := range did {
		fmt.Fprintln(c.io.Out, line)
	}
	if err != nil {
		return err
	}
	if len(did) > 0 {
		fmt.Fprintln(c.io.Out, "\nChecking again:")
		rep = doctor.Run(c.ctx, cfg, env, doctor.Options{})
		fmt.Fprint(c.io.Out, rep.Format())
	}
	return rep.Err()
}

func (c *command) close() { _ = c.player.Close() }

// name is how hints name this command.
func (c *command) name() string {
	if c.env.Name != "" {
		return c.env.Name
	}
	return "pigmusic"
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (c *command) println(a ...any) { fmt.Fprintln(c.io.Out, a...) }

func (c *command) dispatch(name string, args []string) error {
	switch name {
	case "search":
		return c.search(args)
	case "play":
		return c.play(args)
	case "add":
		return c.add(args)
	case "queue":
		return c.queue()
	case "status", "now":
		return c.status()
	case "shuffle":
		on, err := onOff(args, "shuffle")
		if err != nil {
			return err
		}
		return c.modes(func(m music.Modes) error { return m.SetShuffle(c.ctx, on) })
	case "repeat":
		mode, err := repeatMode(args)
		if err != nil {
			return err
		}
		return c.modes(func(m music.Modes) error { return m.SetRepeat(c.ctx, mode) })
	case "pause":
		return c.withPlayer(func(p music.Player) error { return p.SetPaused(c.ctx, true) })
	case "resume":
		return c.withPlayer(func(p music.Player) error { return p.SetPaused(c.ctx, false) })
	case "toggle":
		return c.withPlayer(func(p music.Player) error { return p.TogglePause(c.ctx) })
	case "next":
		return c.withPlayer(func(p music.Player) error { return p.Next(c.ctx) })
	case "prev":
		return c.withPlayer(func(p music.Player) error { return p.Prev(c.ctx) })
	case "jump":
		n, err := oneNumber(args, "jump")
		if err != nil {
			return err
		}
		return c.withPlayer(func(p music.Player) error { return p.Jump(c.ctx, n-1) })
	case "remove":
		n, err := oneNumber(args, "remove")
		if err != nil {
			return err
		}
		return c.withPlayer(func(p music.Player) error { return p.Remove(c.ctx, n-1) })
	case "move":
		if len(args) != 2 {
			return usagef("move takes two queue positions")
		}
		from, e1 := strconv.Atoi(args[0])
		to, e2 := strconv.Atoi(args[1])
		if e1 != nil || e2 != nil {
			return usagef("move takes two queue positions, counted from 1")
		}
		return c.withPlayer(func(p music.Player) error { return p.Move(c.ctx, from-1, to-1) })
	case "seek":
		if len(args) != 1 {
			return usagef("seek takes a number of seconds")
		}
		secs, err := strconv.ParseFloat(args[0], 64)
		if err != nil {
			return usagef("seek takes a number of seconds, not %q", args[0])
		}
		return c.withPlayer(func(p music.Player) error { return p.SeekRelative(c.ctx, time.Duration(secs*float64(time.Second))) })
	case "volume", "vol":
		n, err := oneNumber(args, "volume")
		if err != nil {
			return err
		}
		return c.withPlayer(func(p music.Player) error { return p.SetVolume(c.ctx, n) })
	case "stop":
		return c.stop()
	case "check":
		return c.check()
	case "doctor":
		return c.doctor(args)
	case "setup":
		return c.setupCmd()
	case "library-counts":
		return c.libraryCounts()
	}
	return usagef("unknown command %q", name)
}

// onOff reads "on" or "off".
func onOff(args []string, name string) (bool, error) {
	if len(args) == 1 {
		switch args[0] {
		case "on":
			return true, nil
		case "off":
			return false, nil
		}
	}
	return false, usagef("%s takes on or off", name)
}

// repeatMode reads "off", "one" or "all".
func repeatMode(args []string) (music.RepeatMode, error) {
	if len(args) == 1 {
		switch args[0] {
		case "off":
			return music.RepeatOff, nil
		case "one":
			return music.RepeatOne, nil
		case "all":
			return music.RepeatAll, nil
		}
	}
	return "", usagef("repeat takes off, one or all")
}

// modes runs op on a player that has shuffle and repeat, and prints the status that follows.
func (c *command) modes(op func(music.Modes) error) error {
	if err := c.attachRunning(); err != nil {
		return err
	}
	m, ok := c.player.(music.Modes)
	if !ok {
		return errors.New("this engine has no shuffle or repeat")
	}
	if err := op(m); err != nil {
		return err
	}
	return c.printStatus(true)
}

func oneNumber(args []string, name string) (int, error) {
	if len(args) != 1 {
		return 0, usagef("%s takes one number", name)
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, usagef("%s takes a number, not %q", name, args[0])
	}
	return n, nil
}

// attachRunning connects to the mpv that is already running, and fails when there is none.
func (c *command) attachRunning() error {
	if !c.running(c.ctx) {
		return fmt.Errorf("nothing is playing (%s); start with `%s play`", c.stopped, c.name())
	}
	return c.player.Attach(c.ctx)
}

// withPlayer runs op against the running mpv and prints the status that follows.
func (c *command) withPlayer(op func(music.Player) error) error {
	if err := c.attachRunning(); err != nil {
		return err
	}
	if err := op(c.player); err != nil {
		return err
	}
	return c.printStatus(true)
}

func (c *command) status() error {
	if !c.running(c.ctx) {
		c.println("stopped (" + c.stopped + ")")
		return nil
	}
	if err := c.player.Attach(c.ctx); err != nil {
		return err
	}
	return c.printStatus(true)
}

func (c *command) queue() error {
	if err := c.attachRunning(); err != nil {
		return err
	}
	st, err := c.player.Sync(c.ctx)
	if err != nil {
		return err
	}
	if len(st.Queue) == 0 {
		c.println("the queue is empty")
		return nil
	}
	for i, t := range st.Queue {
		marker := " "
		if i == st.Index {
			marker = ">"
		}
		c.println(fmt.Sprintf("%s %2d  %s", marker, i+1, describe(t)))
	}
	return nil
}

// printStatus prints the state mpv reports. After a command, mpv may not have
// published its change yet, so the live state is read once more.
func (c *command) printStatus(sync bool) error {
	st := c.player.State()
	if sync {
		var err error
		if st, err = c.player.Sync(c.ctx); err != nil {
			return err
		}
	}
	defer c.printLastError()
	if st.Track == nil {
		c.println(fmt.Sprintf("idle  queue %d  volume %d", len(st.Queue), st.Volume))
		return nil
	}
	mark := "playing"
	if st.Paused {
		mark = "paused"
	}
	line := fmt.Sprintf("%s  %s  %s / %s  volume %d  [%d/%d]", mark, describe(*st.Track), clock(st.Position), clock(st.Duration), st.Volume, st.Index+1, len(st.Queue))
	if st.Shuffle {
		line += "  shuffle"
	}
	if st.Repeat != music.RepeatOff {
		line += "  repeat " + string(st.Repeat)
	}
	c.println(line)
	return nil
}

// printLastError says why the native engine skipped a track, when it did.
func (c *command) printLastError() {
	if r, ok := c.player.(interface{ LastError() string }); ok {
		if msg := r.LastError(); msg != "" {
			c.println("last error:", msg)
		}
	}
}

func describe(t music.Track) string {
	if len(t.Artists) == 0 {
		return t.Title
	}
	return strings.Join(t.Artists, ", ") + " - " + t.Title
}

func clock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// ── search and the result cache ─────────────────────────────────────────────

func (c *command) search(args []string) error {
	query, limit, err := queryArgs(args)
	if err != nil {
		return err
	}
	tracks, err := c.find(query, limit)
	if err != nil {
		return err
	}
	if len(tracks) == 0 {
		c.println("no results")
		return nil
	}
	for i, t := range tracks {
		dur := ""
		if t.Duration > 0 {
			dur = clock(t.Duration)
		}
		c.println(fmt.Sprintf("%2d  %-11s  %5s  %s", i+1, t.ID, dur, describe(t)))
	}
	return nil
}

// queryArgs splits "words... [-n N]" into the query and the limit.
func queryArgs(args []string) (string, int, error) {
	limit := 10
	var words []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-n" {
			if i+1 >= len(args) {
				return "", 0, usagef("-n takes a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return "", 0, usagef("-n takes a positive number, not %q", args[i+1])
			}
			limit = n
			i++
			continue
		}
		words = append(words, args[i])
	}
	query := strings.TrimSpace(strings.Join(words, " "))
	if query == "" {
		return "", 0, usagef("a search needs words to look for")
	}
	return query, limit, nil
}

func (c *command) find(query string, limit int) ([]music.Track, error) {
	if c.engineErr != nil {
		return nil, c.engineErr
	}
	// The direct listing needs no program, so it is asked before the doctor (seconds: mpv, yt-dlp twice, the JavaScript
	// runtime). Only when it cannot answer does the search go on to yt-dlp, which the doctor has to find first.
	var tracks []music.Track
	var direct error // why the direct listing could not answer, named if what follows fails too
	asked := false
	if y, ok := c.source.(*ytdlp.Source); ok && y.Inner != nil && !c.prepared {
		asked = true
		if got, err := y.Inner.Search(c.ctx, query, limit); err == nil && len(got) > 0 {
			tracks = got
		} else if c.ctx.Err() != nil {
			return nil, c.ctx.Err()
		} else {
			direct = err
		}
	}
	if tracks == nil {
		if err := c.prepare(); err != nil {
			return nil, ytdlp.AlsoDirect(err, direct)
		}
		var err error
		if y, ok := c.source.(*ytdlp.Source); ok && y.Inner != nil && asked {
			tracks, err = y.SearchViaYtdlp(c.ctx, query, limit) // the direct listing has just failed
			err = ytdlp.AlsoDirect(err, direct)
		} else {
			tracks, err = c.source.Search(c.ctx, query, limit)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := c.saveResults(tracks); err != nil {
		return nil, err
	}
	return tracks, nil
}

func (c *command) saveResults(tracks []music.Track) error {
	if err := c.paths.EnsureDir(); err != nil {
		return err
	}
	return music.SaveSearch(c.paths.Search, tracks)
}

func (c *command) lastResults() ([]music.Track, error) {
	tracks, err := music.LoadSearch(c.paths.Search)
	if errors.Is(err, fs.ErrNotExist) {
		if c.env.Name != "" && c.env.Name != "pigmusic" { // /music has no search subcommand: play <words> searches
			return nil, fmt.Errorf("there is no search to take a number from: run `%s play <words>` or search in the player first", c.name())
		}
		return nil, errors.New("there is no search to take a number from: run `pigmusic search <query>` first")
	}
	return tracks, err
}

// pick resolves play/add arguments: a result number from the last search, or a
// query that is searched now. It returns the list and the chosen index.
func (c *command) pick(args []string) ([]music.Track, int, error) {
	if len(args) == 1 {
		if n, err := strconv.Atoi(args[0]); err == nil {
			tracks, err := c.lastResults()
			if err != nil {
				return nil, 0, err
			}
			if n < 1 || n > len(tracks) {
				return nil, 0, fmt.Errorf("result %d does not exist (the last search found %d)", n, len(tracks))
			}
			return tracks, n - 1, nil
		}
	}
	query, limit, err := queryArgs(args)
	if err != nil {
		return nil, 0, err
	}
	tracks, err := c.find(query, limit)
	if err != nil {
		return nil, 0, err
	}
	if len(tracks) == 0 {
		return nil, 0, fmt.Errorf("no results for %q", query)
	}
	return tracks, 0, nil
}

// ── playing ─────────────────────────────────────────────────────────────────

// attachOrStart connects to mpv, starting it when it is not running. A missing
// mpv or yt-dlp is reported with what to install before anything starts.
func (c *command) attachOrStart() error {
	if c.engineErr != nil {
		return c.engineErr
	}
	if err := c.prepare(); err != nil { // mpv engine only: the doctor's refusal names what to fix
		return err
	}
	return c.player.Attach(c.ctx)
}

func (c *command) play(args []string) error {
	if len(args) == 0 {
		return c.withPlayer(func(p music.Player) error { return p.SetPaused(c.ctx, false) })
	}
	tracks, at, err := c.pick(args)
	if err != nil {
		return err
	}
	if err := c.attachOrStart(); err != nil {
		return err
	}
	if err := c.player.Replace(c.ctx, tracks, at); err != nil {
		return err
	}
	return c.printStatus(true)
}

func (c *command) add(args []string) error {
	if len(args) == 0 {
		return usagef("add takes a result number or a query")
	}
	tracks, at, err := c.pick(args)
	if err != nil {
		return err
	}
	if err := c.attachOrStart(); err != nil {
		return err
	}
	if err := c.player.Enqueue(c.ctx, tracks[at]); err != nil {
		return err
	}
	c.println("queued:", describe(tracks[at]))
	return nil
}

func (c *command) stop() error {
	if !c.running(c.ctx) {
		c.println(c.stopped)
		return nil
	}
	if err := c.player.Attach(c.ctx); err != nil {
		return err
	}
	if err := c.player.Shutdown(c.ctx); err != nil {
		return err
	}
	c.println("stopped")
	return nil
}

func (c *command) check() error {
	if c.engineErr != nil {
		return c.engineErr
	}
	if c.choice.Kind == engine.NativeEngine {
		deps := engine.Deps{Getenv: c.env.Getenv, LookPath: c.env.LookPath}
		report := engine.NativeHealth(c.ctx, c.settings, deps, false)
		c.println("engine: native (" + c.choice.Reason + ")")
		fmt.Fprint(c.io.Out, report.String())
		if !report.OK() {
			return errors.New("the native engine is not ready")
		}
		return nil
	}
	if c.choice.Warning != "" { // selection already looked: mpv or yt-dlp is missing or unfit
		return errors.New(c.choice.Warning)
	}
	c.println("mpv and yt-dlp are installed")
	return nil
}

// libraryCounts prints how many playlists and liked songs the account has, and nothing else about them. It is the first
// real check of the library listing on a signed-in machine: it asks before reading a browser's cookies, once per browser.
func (c *command) libraryCounts() error {
	if err := c.prepare(); err != nil {
		return err
	}
	access := c.cookieAccess()
	_, err := access.Spec(c.ctx)
	var need *music.NeedsConsentError
	if errors.As(err, &need) {
		fmt.Fprintf(c.io.Out, "pig-music needs your OK before it reads your YouTube Music account.\nTo list your library, yt-dlp would read the cookies of %s.\n"+
			"It opens that browser's whole cookie store for each library request, in memory only; pig-music saves no cookie and never sees one.\n"+
			"Only library listings use it: search, playback and mpv never use cookies.\n%s\nAllow reading %s? [y/N] ", need.Description, need.Notes, need.Browser)
		line, _ := bufio.NewReader(c.io.In).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return errors.New("not allowed; nothing was read")
		}
		if err := access.Grant(c.ctx, need.Browser); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	var failed bool
	cols, err := c.source.Library(c.ctx)
	if err != nil {
		failed = true
		fmt.Fprintln(c.io.Out, "playlists: error:", err)
	} else {
		playlists := 0
		for _, col := range cols {
			if col.Kind == "playlist" {
				playlists++
			}
		}
		fmt.Fprintln(c.io.Out, "playlists:", playlists)
	}
	liked, err := c.source.Tracks(c.ctx, "LM")
	if err != nil {
		failed = true
		fmt.Fprintln(c.io.Out, "liked songs: error:", err)
	} else {
		fmt.Fprintln(c.io.Out, "liked songs:", len(liked))
	}
	if failed {
		return errors.New("the library could not be listed (see above); nothing about its contents was printed")
	}
	return nil
}
