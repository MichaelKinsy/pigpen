package ask_user_question_test

import "testing"

// Upstream cases that have no Go twin, each with its reason. The twins of the interactive questionnaire
// (factory, listener, the custom() dispatch of execute, the healthy session load) are in factory_twin_test.go,
// listener_twin_test.go, execute_twin_test.go and sessionload_twin_test.go; the locales twin is TestLocales in
// internal/ui/i18n_bridge_test.go.
func TestNamedSkips(t *testing.T) {
	const f = "ask-user-question.session-load"
	tskip(t, f, "returns error: session_load_failed (not a throw) when the lazy import rejects", "no lazy module import in Go: the questionnaire is compiled into the extension")
	tskip(t, f, "returns error: stale_module_cache when the namespace resolves without a constructable class", "a jiti module-cache failure mode; Go has no module cache")
	tskip(t, f, "schedules a background import of the session graph PREWARM_DELAY_MS after registration", "no lazy graph to pre-warm")
	tskip(t, f, "swallows a pre-warm failure so registration-time churn never crashes the extension", "no pre-warm in Go")
	tskip(t, "banned-flags", "no production source references the pre-1.0.3 boolean flags", "checks the TypeScript sources for flags the project bans; no Go counterpart")
	tskip(t, "ship-manifest", "`package.json` `files` array covers every production .ts module across the tree", "checks the npm `files` manifest; the Go module ships whole")
	tskip(t, "ask-user-question.execute", "returns error: no_custom_ui (not a decline) when custom resolves undefined and no dialog primitives exist",
		"a Go Context always has Select and Input, so a host without dialog primitives cannot occur: when Custom resolves nil the dialog walker takes over (runQuestionnaire), which the rpc-fallback twins cover")
}
