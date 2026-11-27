package prediction

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// Cross-area hooks wired once by wiring.go.
var (
	// Setting reads a settings key for the session (wired to settings.Any).
	Setting func(ctx sdk.Context, key string) any
	// IsSubagent reports whether ctx belongs to a spawned child subagent
	// session (wired to agents.IsSubagent).
	IsSubagent func(ctx sdk.Context) bool
)

var (
	defaultMu   sync.Mutex
	defaultCtrl = NewController()
)

func getController() *Controller {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultCtrl == nil {
		defaultCtrl = NewController()
	}
	return defaultCtrl
}

// Register wires composer prediction lifecycle hooks on e.
func Register(e *sdk.Extension) {
	registerWithController(e, getController())
}

func registerWithController(e *sdk.Extension, ctrl *Controller) {
	lifecycle.OnEnd("prediction", ctrl.Dispose)

	e.OnEvent(sdk.EventAgentEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		ctrl.Request(ctx, data)
		return nil, nil
	})

	cancelHandler := func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctrl.Cancel(ctx)
		return nil, nil
	}

	e.OnEvent(sdk.EventAgentStart, cancelHandler)
	e.OnEvent(sdk.EventBeforeAgentStart, cancelHandler)
	e.OnEvent(sdk.EventInput, cancelHandler)
	e.OnEvent(sdk.EventSessionBeforeSwitch, cancelHandler)
	e.OnEvent(sdk.EventSessionBeforeCompact, cancelHandler)
	e.OnEvent(sdk.EventSessionCompact, cancelHandler)
	e.OnEvent(sdk.EventSessionBeforeTree, cancelHandler)
	e.OnEvent(sdk.EventSessionTree, cancelHandler)

	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := sessionKey(ctx); sid != "" {
			ctrl.Dispose(sid)
		}
		return nil, nil
	})
}
