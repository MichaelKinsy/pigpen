package pig_music

import (
	"context"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
	"github.com/MichaelKinsy/pigpen/pig-music/engine"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"github.com/MichaelKinsy/pigpen/pig-music/selfmanage"
)

// setup is /music setup (download: true) and /music doctor (download: false): check this machine, say what is wrong
// with one fix each, and, for setup, offer the downloads that can fix it, each behind a confirmation.
func (a *app) setup(ctx sdk.Context, download bool) error {
	paths, settings, err := a.load()
	if err != nil {
		ctx.Notify("pig-music: "+err.Error(), "error")
		return nil
	}
	deps := engine.Deps{Getenv: a.d.Getenv, LookPath: a.d.LookPath}
	mode, err := engine.ParseMode(first(a.d.Getenv("PIG_MUSIC_ENGINE"), settings.Engine))
	if err != nil {
		ctx.Notify("pig-music: "+err.Error(), "error")
		return nil
	}
	if mode == engine.Native {
		// mpv and yt-dlp are not used: the health that matters is the native engine's, with its stream probe (the pigmusic
		// program resolves one track and reads a few KB).
		ctx.Notify("pig-music: checking the native engine (this resolves one track and reads a few KB from YouTube)...", "info")
		notifyNative(ctx, engine.NativeHealth(context.Background(), settings, deps, true), true)
		if download {
			ctx.Notify("pig-music setup: the native engine needs no downloads; yt-dlp and Deno are for the mpv engine (engine is native in settings.json or PIG_MUSIC_ENGINE).", "info")
		}
		return nil
	}
	env := a.doctorEnv()
	cfg := doctor.ConfigFrom(a.d.Getenv, settings, paths)
	ctx.Notify("pig-music: checking this machine (this resolves one track and reads a few KB from YouTube)...", "info")
	rep := doctor.Run(context.Background(), cfg, env, doctor.Options{Probe: true})
	notify(ctx, "pig-music doctor", rep)
	// The native engine is the fallback of auto and the choice of engine = native: its health is shown beside mpv's, as
	// information (no network probe) while mpv is the engine.
	notifyNative(ctx, engine.NativeHealth(context.Background(), settings, deps, false), false)
	if !download {
		return nil
	}
	in := selfmanage.NewInstaller(paths.Data, env)
	ask := func(title, message string) (bool, error) {
		answer, err := ctx.Confirm(title, message)
		confirmed, _ := answer.(bool)
		return confirmed, err
	}
	did, err := in.Setup(context.Background(), rep, ask)
	if len(did) > 0 {
		ctx.Notify("pig-music setup:\n"+strings.Join(did, "\n"), "info")
		notify(ctx, "pig-music doctor (after setup)", doctor.Run(context.Background(), cfg, env, doctor.Options{}))
		a.mu.Lock()
		running := a.host != nil
		a.mu.Unlock()
		if running {
			ctx.Notify("pig-music: the player is already running; run /reload to make it use the new programs.", "info")
		}
	}
	if err != nil {
		ctx.Notify("pig-music setup: "+err.Error(), "error")
	}
	return nil
}

// notifyNative shows the native engine's health. A failed check is an error when the native engine is the engine in use, and a
// warning otherwise.
func notifyNative(ctx sdk.Context, rep native.Report, inUse bool) {
	level := "info"
	if !rep.OK() {
		level = "warning"
		if inUse {
			level = "error"
		}
	}
	ctx.Notify("pig-music doctor, native engine:\n"+strings.TrimRight(rep.String(), "\n"), level)
}

func notify(ctx sdk.Context, title string, rep doctor.Report) {
	level := "info"
	if rep.Failed() {
		level = "error"
	} else {
		for _, c := range rep.Checks {
			if c.Status == doctor.Warn {
				level = "warning"
			}
		}
	}
	ctx.Notify(title+":\n"+strings.TrimRight(rep.Format(), "\n"), level)
}
