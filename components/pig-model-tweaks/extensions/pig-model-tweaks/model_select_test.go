package pigmodeltweaks

import "testing"

// The model-select confirmation is three separate decisions that must not leak
// into each other: whether the guard questions a selection at all, whose answer
// is still current, and which switch a decline returns to. Each is pure state
// on the store, so each is pinned here.

func TestGuardQuestionsOnlyOutsideTheList(t *testing.T) {
	st := newStore()
	state := defaultSettings()
	state.ModelGuard.AllowedModels = []modelRef{{Provider: "openrouter", Model: "gpt-x"}}

	if guardQuestions(state, st, modelRef{Provider: "openrouter", Model: "gpt-x"}) {
		t.Error("a listed model must not be questioned")
	}
	if !guardQuestions(state, st, modelRef{Provider: "openrouter", Model: "gpt-y"}) {
		t.Error("an unlisted model is what the guard exists for")
	}
	// A provider-only entry trusts every model of that backend.
	state.ModelGuard.AllowedModels = []modelRef{{Provider: "acme", Model: ""}}
	if guardQuestions(state, st, modelRef{Provider: "acme", Model: "whatever"}) {
		t.Error("a provider-only entry trusts the whole provider")
	}

	state.ModelGuard.Enabled = false
	if guardQuestions(state, st, modelRef{Provider: "openrouter", Model: "gpt-y"}) {
		t.Error("a disabled guard questions nothing")
	}

	state.ModelGuard.Enabled = true
	state.ModelGuard.AllowedModels = nil
	if guardQuestions(state, st, modelRef{Provider: "openrouter", Model: "gpt-y"}) {
		t.Error("an empty list allows every model")
	}
}

func TestGuardQuestionsSparesTheStartupModel(t *testing.T) {
	st := newStore()
	state := defaultSettings()
	state.ModelGuard.AllowedModels = []modelRef{{Provider: "openrouter", Model: "gpt-x"}}
	// The session opened on a model the owner configured; the guard trusts it
	// exactly as the prompt guard does, or remember-model and the guard would
	// contradict each other on every prompt.
	st.startup = modelRef{Provider: "acme", Model: "start-here"}

	if guardQuestions(state, st, st.startup) {
		t.Error("the model the session started on must not be questioned")
	}
	if !guardQuestions(state, st, modelRef{Provider: "acme", Model: "other"}) {
		t.Error("a switch away from the startup model is questioned")
	}
}

func TestRevertFlagIsSeenOnceAndOnlyByTheGuard(t *testing.T) {
	st := newStore()
	state := defaultSettings()
	back := modelRef{Provider: "acme", Model: "a"}
	target := modelRef{Provider: "acme", Model: "b"}

	st.markReverting(back)
	// remember-model runs first on the revert's model_select event: it may
	// look, but only the guard's take clears, or the flag would vanish before
	// the guard's handler saw it.
	if !st.peekReverting(state, back) {
		t.Fatal("remember-model must see the pending revert")
	}
	if !st.peekReverting(state, back) {
		t.Fatal("peek must not consume")
	}
	// A stray event for another model is not the revert: it must leave the flag
	// for the switch-back that is still coming.
	if st.takeReverting(state, target) {
		t.Error("a different model is not the revert we are waiting for")
	}
	if !st.peekReverting(state, back) {
		t.Fatal("a mismatched event must not consume the pending revert")
	}
	if !st.takeReverting(state, back) {
		t.Fatal("the revert's own event consumes the flag")
	}
	if st.takeReverting(state, back) {
		t.Error("the flag must be single-use, or a later selection of the same model is swallowed")
	}
}

func TestAskingMarkerSupersedesOlderDialogs(t *testing.T) {
	st := newStore()
	first := modelRef{Provider: "acme", Model: "first"}
	second := modelRef{Provider: "acme", Model: "second"}

	st.markAsking(first)
	st.markAsking(second) // a newer selection while the first dialog is open
	if st.takeAsking(first) {
		t.Error("the answer about a replaced switch must not act: it would revert a model the user has left")
	}
	if !st.takeAsking(second) {
		t.Error("the newest dialog's answer owns the marker")
	}
	if st.takeAsking(second) {
		t.Error("the marker is single-use")
	}
}

func TestApprovalIsSessionScopedAndSuffixBlind(t *testing.T) {
	st := newStore()
	state := defaultSettings()
	// An OpenRouter lock is recorded as base:provider, the same model the event
	// may report without the suffix: one approval, not two.
	state.OpenRouterModelProviderPref.Locks = map[string]string{"deepseek/deepseek-v4": "deepseek"}

	approved := modelRef{Provider: "openrouter", Model: "deepseek/deepseek-v4:deepseek"}
	st.approveForSession(approved)
	if !st.approvedForSession(state, approved) {
		t.Fatal("the approved model must read as approved")
	}
	if !st.approvedForSession(state, modelRef{Provider: "openrouter", Model: "deepseek/deepseek-v4"}) {
		t.Error("the base id is the same model the user approved")
	}
	if st.approvedForSession(state, modelRef{Provider: "openrouter", Model: "gpt-y"}) {
		t.Error("another model was never approved")
	}
}

func TestDeclineNeedsSomethingToReturnTo(t *testing.T) {
	// declineSelect's guard clauses decide between falling back to the startup
	// model, keeping the switch with a notice, and reverting. What it refuses is
	// exactly: an empty previous that is also an empty startup, and a previous
	// that names the current model (reverting to it would be a no-op SetModel).
	state := defaultSettings()
	current := modelRef{Provider: "acme", Model: "b"}

	if !sameModel(state, current, current) {
		t.Error("sameModel must identify a model with itself: declining must not 'switch back' to where it already is")
	}
	if sameModel(state, modelRef{}, modelRef{Provider: "acme", Model: "start"}) {
		t.Error("the empty reference never matches a named model, so an absent previousModel falls through to the startup fallback instead of matching the current model")
	}
}
