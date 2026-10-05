package pig_music

import (
	"context"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
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
	env := a.doctorEnv()
	cfg := doctor.ConfigFrom(a.d.Getenv, settings, paths)
	ctx.Notify("pig-music: checking this machine (this resolves one track and reads a few KB from YouTube)...", "info")
	rep := doctor.Run(context.Background(), cfg, env, doctor.Options{Probe: true})
	notify(ctx, "pig-music doctor", rep)
	if !download {
		return nil
	}
	in := selfmanage.NewInstaller(paths.Data, env)
	ask := func(title, message string) (bool, error) { return ctx.Confirm(title, message) }
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
