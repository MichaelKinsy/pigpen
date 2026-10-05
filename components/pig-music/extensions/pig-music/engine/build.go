package engine

import (
	"context"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

// NewNative builds the native engine's Source and Player. The Player attaches to
// a running `pigmusic serve`, or starts one.
func NewNative(s music.Settings, paths music.Paths, d Deps) (music.Source, *native.Player) {
	d.fill()
	player := native.NewPlayer(native.ClientConfig{
		Paths:  paths,
		Binary: first(d.Getenv("PIG_MUSIC_SERVE"), s.NativePath),
	})
	return native.NewSource(first(d.Getenv("PIG_MUSIC_SERVE"), s.NativePath)), player
}

// NativeHealth is the hook pig-music's doctor calls for the native engine. With
// probe set it also resolves one track and reads 64 KiB of it, which uses the
// network (and takes a second or two).
func NativeHealth(ctx context.Context, s music.Settings, d Deps, probe bool) native.Report {
	d.fill()
	goos := d.GOOS
	if IsTermux(d) {
		goos = "android" // a linux build run under Termux is no different: mpv only
	}
	opts := native.HealthOptions{
		GOOS: goos, Getenv: d.Getenv,
		ServeFound: func() error { return d.ServeFound(s) },
	}
	if probe {
		// The stream probe needs WaxTap, which the extension does not link: the pigmusic program runs it.
		h := native.Helper{Configured: first(d.Getenv("PIG_MUSIC_SERVE"), s.NativePath), Getenv: d.Getenv, LookPath: d.LookPath}
		opts.Probe = func(ctx context.Context) native.Check { return h.Probe(ctx, probeVideo) }
	}
	return native.CheckHealth(ctx, opts)
}

// probeVideo is a long-lived public track the probe resolves.
const probeVideo = "dQw4w9WgXcQ"
