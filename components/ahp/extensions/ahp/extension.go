// Package ahp is a Go port of pi-ahp (Bang Lee / Qusic, MIT): a host for Microsoft's Agent
// Host Protocol that serves the running PiG session to remote AHP clients over WebSocket.
//
// The extension opens no listener unless the user asks: with the --ahp flag, or with /ahp start.
// Remote filesystem and terminal access are further opt-ins in the settings file.
package ahp

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	flagStart    = "ahp"
	flagSettings = "ahp-settings"
)

// Extension registers the extension. It starts no listener unless the user configured one.
func Extension() *sdk.Extension {
	e := sdk.New("ahp")
	rt := newRuntime()

	e.Flag(flagStart, sdk.FlagOptions{Type: sdk.FlagBoolean, Description: "Start the AHP listener for this session (settings file: see /ahp)"})
	e.Flag(flagSettings, sdk.FlagOptions{Type: sdk.FlagString, Description: "AHP settings file (default: <pig home>/ahp/settings.json)"})

	rt.live.Register(e, rt.onSessionStart, rt.onSessionShutdown)
	rt.live.OnModelChanged(rt.onModelChanged)

	e.Command("ahp", "AHP host: /ahp [status|start|stop|tunnel [name]]", func(ctx sdk.Context, args string) error {
		command, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
		switch command {
		case "", "status":
			ctx.Notify(rt.status(), "info")
		case "start":
			addr, err := rt.start(ctx)
			if err != nil {
				ctx.Notify("AHP: "+err.Error(), "error")
				return nil
			}
			ctx.Notify(addr, "info")
		case "stop":
			if rt.stop() {
				ctx.Notify("AHP listener stopped", "info")
			} else {
				ctx.Notify("AHP listener is not running", "info")
			}
		case "tunnel":
			name, err := tunnelName(strings.TrimSpace(rest))
			if err != nil {
				return err
			}
			if err := rt.startTunnel(ctx, name); err != nil {
				ctx.Notify("AHP: "+err.Error(), "error")
				return nil
			}
			ctx.Notify("AHP: setting up the dev tunnel (needs `devtunnel` on PATH and a login)", "info")
		default:
			return fmt.Errorf("usage: /ahp [status|start|stop|tunnel [name]]")
		}
		return nil
	})
	return e
}

// tunnelName is the display name given to /ahp tunnel: the rest of the line, optionally as
// `--name <label>` as pi-ahp-tunnel takes it.
func tunnelName(rest string) (string, error) {
	if name, ok := strings.CutPrefix(rest, "--name"); ok {
		name = strings.TrimSpace(name)
		if name == "" {
			return "", fmt.Errorf("usage: /ahp tunnel [--name <label>]")
		}
		return name, nil
	}
	return rest, nil
}
