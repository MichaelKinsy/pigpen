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

	e.Command("ahp", "AHP host: /ahp [status|start|stop]", func(ctx sdk.Context, args string) error {
		switch strings.TrimSpace(args) {
		case "", "status":
			ctx.Notify(rt.status(), "info")
		case "start":
			addr, err := rt.start(ctx)
			if err != nil {
				ctx.Notify("AHP: "+err.Error(), "error")
				return nil
			}
			ctx.Notify("AHP listening on "+addr, "info")
		case "stop":
			if rt.stop() {
				ctx.Notify("AHP listener stopped", "info")
			} else {
				ctx.Notify("AHP listener is not running", "info")
			}
		default:
			return fmt.Errorf("usage: /ahp [status|start|stop]")
		}
		return nil
	})
	return e
}
