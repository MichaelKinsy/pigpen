package tintinweb_tasks

import (
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// SetRPCTimeouts shortens the spawn and stop RPC timeouts (30 s and 10 s in the original) for a test and
// returns the function that restores them. The original's tests advance fake timers instead.
func SetRPCTimeouts(spawn, stop time.Duration) func() {
	ps, pt := spawnTimeout, stopTimeout
	spawnTimeout, stopTimeout = spawn, stop
	return func() { spawnTimeout, stopTimeout = ps, pt }
}

// NewSettingsPanel builds the /tasks → Settings component over a config, as /tasks does when Settings is picked
// (project overrides are saved under cwd, global defaults read from agentDir).
func NewSettingsPanel(cfg map[string]any, cwd, agentDir string) interface {
	sdk.ViewComponent
	sdk.ViewEventHandler
} {
	a := &app{cfg: tasksConfig(cfg)}
	return a.newSettingsPanel(cwd, agentDir, sdk.UITheme{})
}
