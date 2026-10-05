// Package engine chooses between pig-music's two playback engines: mpv driven
// over IPC with yt-dlp (the default when they are healthy), and the native
// pure-Go engine (WaxTap, WaxFlow, oto) that needs no external program.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

// Mode is what the user asked for: the `engine` setting or PIG_MUSIC_ENGINE.
type Mode string

const (
	Auto   Mode = "auto"
	MPV    Mode = "mpv"
	Native Mode = "native"
)

// Kind is the engine that was chosen.
type Kind string

const (
	MPVEngine    Kind = "mpv"
	NativeEngine Kind = "native"
)

// ParseMode reads an engine name; empty is auto.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case "":
		return Auto, nil
	case Auto, MPV, Native:
		return m, nil
	}
	return "", fmt.Errorf("engine %q is not one of auto, mpv or native", s)
}

// Choice is the outcome of Select.
type Choice struct {
	Kind Kind
	Mode Mode
	// Reason says why this engine, in a line fit to show.
	Reason string
	// Fallback is true when auto moved to the native engine because mpv was not usable.
	Fallback bool
	// Warning is set when the choice will not work yet (an engine forced with its program missing).
	Warning string
}

// Deps are the outside world; the zero value is the real one.
type Deps struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// MPVHealthy is the hook the doctor (M4a) uses to say that mpv and yt-dlp are
	// fit to use beyond being installed: versions, a JavaScript runtime, a probe.
	// Nil means installed is healthy.
	MPVHealthy func(ctx context.Context, s music.Settings) error
	// ServeFound reports whether the program that runs the native player can be
	// found; nil asks native.FindServeBinary.
	ServeFound func(s music.Settings) error
}

func (d *Deps) fill() {
	if d.GOOS == "" {
		d.GOOS = runtime.GOOS
	}
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}
	if d.ServeFound == nil {
		d.ServeFound = func(s music.Settings) error {
			_, err := native.FindServeBinary(first(d.Getenv("PIG_MUSIC_SERVE"), s.NativePath), d.Getenv, d.LookPath)
			return err
		}
	}
}

func first(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// IsTermux reports whether this is Termux on Android, where the native engine
// cannot run (audio there needs cgo) and mpv is the only route.
func IsTermux(d Deps) bool {
	d.fill()
	return native.IsTermux(d.GOOS, d.Getenv)
}

// TermuxMessage is shown when the native engine is asked for on Android.
const TermuxMessage = native.TermuxMessage

// MPVProblem is why mpv with yt-dlp cannot be used, nil when they can.
func MPVProblem(ctx context.Context, s music.Settings, d Deps) error {
	d.fill()
	if err := music.CheckDependencies(first(d.Getenv("PIG_MUSIC_MPV"), s.MPVPath), first(d.Getenv("PIG_MUSIC_YTDLP"), s.YtdlpPath), d.LookPath); err != nil {
		return err
	}
	if d.MPVHealthy != nil {
		return d.MPVHealthy(ctx, s)
	}
	return nil
}

// Select chooses the engine. It looks at what is installed and runs the health
// hook; it does not touch the network.
func Select(ctx context.Context, s music.Settings, d Deps) (Choice, error) {
	d.fill()
	mode, err := ParseMode(first(d.Getenv("PIG_MUSIC_ENGINE"), s.Engine))
	if err != nil {
		return Choice{}, err
	}
	termux := IsTermux(d)
	mpvErr := MPVProblem(ctx, s, d)
	switch mode {
	case MPV:
		c := Choice{Kind: MPVEngine, Mode: mode, Reason: "engine = mpv"}
		if mpvErr != nil {
			c.Warning = mpvErr.Error()
		}
		return c, nil
	case Native:
		if termux {
			return Choice{}, errors.New(TermuxMessage)
		}
		if err := d.ServeFound(s); err != nil {
			return Choice{}, err
		}
		return Choice{Kind: NativeEngine, Mode: mode, Reason: "engine = native"}, nil
	}
	// auto
	if mpvErr == nil {
		return Choice{Kind: MPVEngine, Mode: mode, Reason: "mpv and yt-dlp are installed and healthy"}, nil
	}
	if termux {
		return Choice{Kind: MPVEngine, Mode: mode, Reason: "Termux: mpv is the only engine", Warning: mpvErr.Error()}, nil
	}
	if serveErr := d.ServeFound(s); serveErr != nil {
		return Choice{}, fmt.Errorf("no engine can play: mpv: %v; native: %v", mpvErr, serveErr)
	}
	return Choice{Kind: NativeEngine, Mode: mode, Fallback: true, Reason: "mpv with yt-dlp is not usable (" + mpvErr.Error() + "), so the native engine is used"}, nil
}
