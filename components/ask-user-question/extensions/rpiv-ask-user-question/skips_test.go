package ask_user_question_test

import "testing"

// Upstream cases of slice-A files that have no Go twin, each with its reason. The interactive questionnaire
// (factory, state, view) is slice B: see port/slices.json.
func TestNamedSkips(t *testing.T) {
	const f = "ask-user-question.session-load"
	tskip(t, f, "returns error: session_load_failed (not a throw) when the lazy import rejects", "no lazy module import in Go: the questionnaire is compiled into the extension")
	tskip(t, f, "returns error: stale_module_cache when the namespace resolves without a constructable class", "a jiti module-cache failure mode; Go has no module cache")
	tskip(t, f, "loads the real session graph and reaches ctx.ui.custom when the module is healthy", "no session graph to load; the custom component is slice B, not ported in this revision")
	tskip(t, f, "schedules a background import of the session graph PREWARM_DELAY_MS after registration", "no lazy graph to pre-warm")
	tskip(t, f, "swallows a pre-warm failure so registration-time churn never crashes the extension", "no pre-warm in Go")
	tskip(t, "banned-flags", "no production source references the pre-1.0.3 boolean flags", "checks the TypeScript sources for flags the project bans; no Go counterpart")
	tskip(t, "ship-manifest", "`package.json` `files` array covers every production .ts module across the tree", "checks the npm `files` manifest; the Go module ships whole")
	tskip(t, "locales", "en.json defines both templated hint keys (English is the fallback base for every locale)", "locales need @juicesharp/rpiv-i18n: English only by the owner's ruling E1 (approved exclusion)")

	// The custom() dispatch of the execute file: the interactive questionnaire, slice B (not ported in this revision).
	tskip(t, "ask-user-question.execute", "User cancels (cancelled: true) → decline envelope",
		"slice B: the result comes from the interactive questionnaire's custom() call; the RPC dismissal decline is the rpc-fallback twin 'dismiss (select resolves undefined) → decline envelope'")
	tskip(t, "ask-user-question.execute", "Normal selection → CC envelope wrapper with quoted question and answer",
		"slice B: an option chosen in the custom() component; the envelope itself is the tool/response-envelope twins and the RPC selection the rpc-fallback single-select twin")
	tskip(t, "ask-user-question.execute", "Custom typed answer sets kind:'custom'",
		"slice B: a custom answer typed into the component; the RPC 'Type something.' follow-up is the rpc-fallback sentinel twin")
	tskip(t, "ask-user-question.execute", "multi-select free-text yields kind:'custom' (not 'multi')",
		"slice B: the key router's input mode on a multi-select tab; the RPC walker's free-text answer is the rpc-fallback non-index twin")
	tskip(t, "ask-user-question.execute", "returns error: no_custom_ui (not a decline) when custom resolves undefined and no dialog primitives exist",
		"a Go Context always has Select and Input, so a host without dialog primitives cannot occur, and slice A never calls custom()")
}
