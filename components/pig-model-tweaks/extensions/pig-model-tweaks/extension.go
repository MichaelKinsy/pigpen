// Package pigmodeltweaks fixes three everyday annoyances when working with a
// mixed model set, as a PiG extension.
//
//  1. PiG forgets your model. Every new session starts on whatever is in
//     settings.json, so it is re-picked by hand. Remember-model saves the
//     selection and restores it next session.
//  2. OpenRouter routes to a random upstream provider. The same model id can be
//     served by backends with different speed, quality and price. The provider
//     lock pins a model to the provider the user chose.
//  3. A prompt can go to the wrong model. A stray Ctrl+P or /model can send work
//     to a costly or weak model. The model guard asks for confirmation first.
//
// It is a port of the three model extensions in github.com/liyu1981/pi-tweaks,
// rewritten against PiG's Go extension SDK. The commands use the pmt- prefix
// where the original used pt-, and the settings file is pmt-settings.json where
// the original wrote pi-tweaks-settings.json.
//
// It registers commands and event handlers only: no tools, no providers, no
// renderers.
package pigmodeltweaks

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// extensionName must match the identity PiG resolves for this directory.
const extensionName = "pig-model-tweaks"

// Host event names this extension subscribes to. The SDK ships helpers for the
// session lifecycle but not for the agent loop or the provider pipeline, so
// these are the host's names spelled out.
const (
	eventModelSelect           = "model_select"
	eventBeforeProviderRequest = "before_provider_request"
	eventInput                 = "input"
	eventSessionShutdown       = "session_shutdown"
)

// modeTUI is the only run mode with a user present to confirm a model switch.
const modeTUI = "tui"

// Extension builds the extension.
func Extension() *sdk.Extension {
	ext := sdk.New(extensionName)
	st := newStore()

	registerRememberModel(ext, st)
	registerOpenRouterLockProvider(ext, st)
	registerModelGuard(ext, st)

	// The settings file lives in PiG's agent directory, which only a session
	// context knows. Taking it from the first session_start keeps one
	// resolution of PIG_HOME and means a run that never starts a session writes
	// nothing at all.
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		agent := agentDir(ctx.ConfigHome())
		st.bind(agent)
		st.rememberStartupModel(agent)
		return nil, nil
	})

	// A confirmation still open at shutdown is released by the host; this only
	// stops the extension from starting or reporting another one.
	ext.OnEvent(eventSessionShutdown, func(_ sdk.Context, _ map[string]any) (any, error) {
		st.stopApprovals()
		return nil, nil
	})
	return ext
}
