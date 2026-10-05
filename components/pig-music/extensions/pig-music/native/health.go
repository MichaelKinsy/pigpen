package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Check is one line of a health report.
type Check struct {
	Name   string
	OK     bool
	Detail string
	// Fix is a command or step the user can copy, empty when the check passes.
	Fix string
}

// Report is the native engine's health: what the doctor (`pigmusic doctor`,
// `/music setup`) shows.
type Report struct{ Checks []Check }

// OK is true when every check passed.
func (r Report) OK() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

func (r Report) String() string {
	var b strings.Builder
	for _, c := range r.Checks {
		mark := "ok  "
		if !c.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "%s  %s: %s\n", mark, c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(&b, "      fix: %s\n", c.Fix)
		}
	}
	return b.String()
}

// HealthOptions configures CheckHealth; the zero value checks this machine.
type HealthOptions struct {
	GOOS   string
	Getenv func(string) string
	// Glob lists files matching a pattern (to find libasound); filepath.Glob when nil.
	Glob func(string) []string
	// ServeFound reports whether the `pigmusic serve` program can be found; nil asks FindServeBinary.
	ServeFound func() error
	// Probe, when set, adds a stream probe: resolve one video and read 64 KiB of it (this uses the network). The program that
	// links WaxTap provides it (wax.Probe); the extension asks `pigmusic native probe`.
	Probe func(ctx context.Context) Check
}

// CheckHealth is the hook the doctor calls for the native engine. It never
// opens the audio device (oto allows one context per process) and never reads
// credentials.
func CheckHealth(ctx context.Context, o HealthOptions) Report {
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	glob := o.Glob
	if glob == nil {
		glob = func(p string) []string { m, _ := filepath.Glob(p); return m }
	}
	if o.ServeFound == nil {
		o.ServeFound = func() error { _, err := FindServeBinary("", o.Getenv, nil); return err }
	}
	var r Report
	if o.GOOS == "android" {
		r.Checks = append(r.Checks, Check{Name: "platform", Detail: "Termux/Android: the native engine's audio needs a C toolchain there", Fix: "use mpv: " + termuxInstall})
	} else {
		r.Checks = append(r.Checks, Check{Name: "platform", OK: true, Detail: o.GOOS + "/" + runtime.GOARCH + ", no C toolchain needed"})
	}
	r.Checks = append(r.Checks, audioCheck(o.GOOS, o.Getenv, glob))
	if err := o.ServeFound(); err != nil {
		r.Checks = append(r.Checks, Check{Name: "player program", Detail: err.Error(), Fix: BuildPigmusicHint})
	} else {
		r.Checks = append(r.Checks, Check{Name: "player program", OK: true, Detail: "pigmusic serve is available"})
	}
	if o.Probe != nil {
		r.Checks = append(r.Checks, o.Probe(ctx))
	}
	return r
}

// audioCheck looks for something oto can play through. macOS and Windows always
// have it; Linux needs a PulseAudio or PipeWire socket, or libasound.
func audioCheck(goos string, getenv func(string) string, glob func(string) []string) Check {
	c := Check{Name: "audio output"}
	switch goos {
	case "darwin", "windows":
		c.OK, c.Detail = true, map[string]string{"darwin": "CoreAudio", "windows": "WASAPI"}[goos]
	case "linux":
		if run := getenv("XDG_RUNTIME_DIR"); run != "" {
			if _, err := os.Stat(filepath.Join(run, "pulse", "native")); err == nil {
				c.OK, c.Detail = true, "PulseAudio or PipeWire (pulse socket)"
				return c
			}
		}
		for _, pat := range []string{"/usr/lib/*/libasound.so.2", "/usr/lib/libasound.so.2", "/usr/lib64/libasound.so.2", "/lib/*/libasound.so.2", "/usr/lib/*-linux-gnu/libasound.so.2"} {
			if m := glob(pat); len(m) > 0 {
				c.OK, c.Detail = true, "ALSA ("+m[0]+")"
				return c
			}
		}
		c.Detail = "no PulseAudio/PipeWire socket and no libasound.so.2"
		c.Fix = "sudo apt-get install libasound2 (Debian, Ubuntu) or sudo dnf install alsa-lib (Fedora), or start PulseAudio or PipeWire"
	case "android":
		c.Detail = "not supported on Termux"
		c.Fix = "use mpv; on Android 16 load PulseAudio's module-aaudio-sink"
	default:
		c.Detail = "no audio backend for " + goos
	}
	return c
}

// BuildPigmusicHint says how to get the pigmusic program, which holds the native engine (the extension itself does not).
const BuildPigmusicHint = "build pigmusic (Go 1.26 or later) in a Pigpen checkout: cd components/pig-music/extensions/pig-music/cmd/pigmusic && go build -o ~/.local/bin/pigmusic . ; then put it on PATH, or set PIG_MUSIC_SERVE (or nativePath in settings.json) to its path. Or use mpv instead: engine = mpv"

const termuxInstall = "pkg install mpv python-yt-dlp nodejs"

// TermuxMessage is why the native engine refuses on Termux/Android.
const TermuxMessage = "the native engine is not available on Termux/Android (its audio output needs a C toolchain there); use mpv: `" + termuxInstall + "`"

// IsTermux reports whether this is Termux on Android, where the native engine
// cannot run and mpv is the only route: an android build, or any build that
// Termux's environment runs.
func IsTermux(goos string, getenv func(string) string) bool {
	return goos == "android" || getenv("TERMUX_VERSION") != "" || strings.Contains(getenv("PREFIX"), "com.termux")
}
