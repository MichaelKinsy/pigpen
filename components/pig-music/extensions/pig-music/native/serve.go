package native

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

var errNoEngine = errors.New("this program was built without the native engine (use the pigmusic program)")

// ServeDeps lets a test run Serve with a scripted opener and a software output.
type ServeDeps struct {
	NewOpener func() (Opener, error)
	NewOutput func(kind string) (Output, error)
}

func (d *ServeDeps) fill() {
	// The extension links neither WaxTap nor WaxFlow nor oto: the program that does (the wax package) fills these in.
	if d.NewOpener == nil {
		d.NewOpener = func() (Opener, error) { return nil, errNoEngine }
	}
	if d.NewOutput == nil {
		d.NewOutput = func(kind string) (Output, error) {
			if kind == "null" {
				return NewNullOutput(), nil
			}
			return nil, errNoEngine
		}
	}
}

// Serve is `pigmusic serve`: it runs the native player as a detached daemon
// until it is stopped, and returns the process exit status. Everything it
// prints is free of URLs and addresses.
func Serve(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer, deps *ServeDeps) int {
	if deps == nil {
		deps = &ServeDeps{}
	}
	deps.fill()
	if getenv == nil {
		getenv = os.Getenv
	}
	logf := func(format string, a ...any) {
		fmt.Fprintf(stderr, "%s %s\n", time.Now().Format("2006-01-02T15:04:05"), Redact(fmt.Sprintf(format, a...)))
	}
	if IsTermux(runtime.GOOS, getenv) {
		logf("%s", TermuxMessage)
		return 1
	}
	SanitizeEnvironment()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "runtime directory (default: pig-music's)")
	idle := fs.Duration("idle-exit", 30*time.Minute, "exit after this long with no client and nothing playing; 0 never")
	output := fs.String("output", getenv("PIG_MUSIC_NATIVE_OUTPUT"), "audio output: auto, oto or null")
	source := fs.String("opener", getenv("PIG_MUSIC_NATIVE_OPENER"), "where audio comes from: auto (YouTube) or synthetic (a test tone, no network)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var paths music.Paths
	if *dir != "" {
		paths = music.PathsIn(*dir)
	} else {
		var err error
		if paths, err = music.DefaultPaths(getenv); err != nil {
			logf("%v", err)
			return 1
		}
	}
	var opener Opener
	var err error
	switch *source {
	case "", "auto":
		opener, err = deps.NewOpener()
	case "synthetic":
		opener = SyntheticOpener{}
	default:
		err = fmt.Errorf("unknown opener %q (use auto or synthetic)", *source)
	}
	if err != nil {
		logf("native player: %v", RedactError(err))
		return 1
	}
	out, err := deps.NewOutput(*output)
	if err != nil {
		logf("native player: %v (PIG_MUSIC_NATIVE_OUTPUT=null runs without sound)", RedactError(err))
		return 1
	}
	err = RunDaemon(ctx, DaemonConfig{Paths: paths, Opener: opener, Output: out, IdleExit: *idle, Log: func(s string) { logf("%s", s) }})
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrAlreadyRunning):
		logf("%v", err)
		return 0 // not a failure: the other daemon serves
	}
	logf("native player: %v", RedactError(err))
	return 1
}
