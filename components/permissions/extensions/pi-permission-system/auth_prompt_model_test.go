package pi_permission_system

import "testing"

// Twins of test/authority/permission-prompt-decision.test.ts.

func modelConfig(mut ...func(*PromptModelConfig)) PromptModelConfig {
	c := PromptModelConfig{DoublePressToConfirm: true, SessionLabel: "Yes, for this session", Keys: defaultDialogKeys()}
	for _, m := range mut {
		m(&c)
	}
	return c
}

func noDouble(c *PromptModelConfig) { c.DoublePressToConfirm = false }

func withWidth(c *PromptModelConfig) {
	c.WidthLabel = `Yes, allow reads and writes to "/tmp/*" for this session`
}

func withScope(c *PromptModelConfig) {
	c.SessionScope = &SessionScopeOption{SubagentLabel: "This subagent only", ServingSessionLabel: "The whole session"}
}

func hotkey(a PromptAction) PromptEvent { return PromptEvent{Type: EventHotkey, Action: a} }
func nav(d string) PromptEvent          { return PromptEvent{Type: EventNav, Direction: d} }

// asRender fails the test unless the outcome is a render.
func asRender(t *testing.T, o PromptOutcome) PromptViewState {
	t.Helper()
	if o.Decided {
		t.Fatalf("expected a render, got a decision %+v", o.Decision)
	}
	return o.State
}

func renderOf(s PromptViewState) PromptOutcome { return PromptOutcome{State: s} }

func decisionOf(d UnattributedDecision) PromptOutcome {
	return PromptOutcome{Decided: true, Decision: d}
}

func TestPromptModelTwins(t *testing.T) {
	f := "authority/permission-prompt-decision"
	approved := UnattributedDecision{Approved: true, State: DecisionApproved}
	denied := UnattributedDecision{State: DecisionDenied}
	forSession := UnattributedDecision{Approved: true, State: DecisionApprovedForSession}
	servingSession := UnattributedDecision{Approved: true, State: DecisionApprovedForServingSess}
	familySession := UnattributedDecision{Approved: true, State: DecisionApprovedForSession, SessionGrantWidth: "family"}
	state := func(step PromptStep, highlighted, armed PromptAction, hint, reasonErr string, serving bool) PromptViewState {
		return PromptViewState{Step: step, HighlightedAction: highlighted, ArmedAction: armed, Hint: hint, ReasonError: reasonErr, ScopeServing: serving, GrantWidth: "proven"}
	}
	initial := func(c PromptModelConfig) PromptViewState { return InitialPromptState(c) }
	opened := func(t *testing.T, c PromptModelConfig, a PromptAction) PromptViewState {
		t.Helper()
		return asRender(t, ReducePrompt(c, initial(c), hotkey(a)))
	}

	tw(t, f, "starts on the decision step highlighting approve with nothing armed", func(t *testing.T) {
		eq(t, InitialPromptState(modelConfig()), state(StepDecision, ActionApprove, "", "", "", false), "state")
	})
	tw(t, f, "arms the option on the first hotkey press without deciding", func(t *testing.T) {
		c := modelConfig()
		eq(t, ReducePrompt(c, initial(c), hotkey(ActionApprove)), renderOf(state(StepDecision, ActionApprove, ActionApprove, "Press y again to approve.", "", false)), "outcome")
	})
	tw(t, f, "commits the decision on the confirming second press of the same key", func(t *testing.T) {
		c := modelConfig()
		armed := opened(t, c, ActionApprove)
		eq(t, ReducePrompt(c, armed, hotkey(ActionApprove)), decisionOf(approved), "outcome")
	})
	tw(t, f, "re-arms when a different hotkey is pressed", func(t *testing.T) {
		c := modelConfig()
		armedY := opened(t, c, ActionApprove)
		eq(t, ReducePrompt(c, armedY, hotkey(ActionDeny)), renderOf(state(StepDecision, ActionDeny, ActionDeny, "Press n again to deny.", "", false)), "outcome")
	})
	tw(t, f, "commits deny on the second press of n", func(t *testing.T) {
		c := modelConfig()
		armed := opened(t, c, ActionDeny)
		eq(t, ReducePrompt(c, armed, hotkey(ActionDeny)), decisionOf(denied), "outcome")
	})
	tw(t, f, "commits immediately on the first hotkey press", func(t *testing.T) {
		c := modelConfig(noDouble)
		eq(t, ReducePrompt(c, initial(c), hotkey(ActionApprove)), decisionOf(approved), "outcome")
	})
	tw(t, f, "moves the highlight and clears any armed key without deciding", func(t *testing.T) {
		c := modelConfig()
		armed := opened(t, c, ActionApprove)
		eq(t, ReducePrompt(c, armed, nav("down")), renderOf(state(StepDecision, ActionApproveSession, "", "", "", false)), "outcome")
	})
	tw(t, f, "wraps the highlight from the last option back to the first", func(t *testing.T) {
		c := modelConfig()
		s := initial(c)
		for i := 0; i < 4; i++ {
			s = asRender(t, ReducePrompt(c, s, nav("up")))
		}
		// up from y wraps to r, then walks r, n, s, y over four presses
		eq(t, s.HighlightedAction, ActionApprove, "highlight")
	})
	tw(t, f, "confirms the highlighted option in a single enter press even when double-press is enabled", func(t *testing.T) {
		c := modelConfig()
		down := asRender(t, ReducePrompt(c, initial(c), nav("down")))
		down2 := asRender(t, ReducePrompt(c, down, nav("down")))
		eq(t, ReducePrompt(c, down2, PromptEvent{Type: EventConfirm}), decisionOf(denied), "outcome")
	})
	tw(t, f, "denies from the decision step", func(t *testing.T) {
		c := modelConfig()
		eq(t, ReducePrompt(c, initial(c), PromptEvent{Type: EventCancel}), decisionOf(denied), "outcome")
	})
	tw(t, f, "opens the reason step on confirming r (double-press enabled)", func(t *testing.T) {
		c := modelConfig()
		armed := opened(t, c, ActionDenyWithReason)
		eq(t, ReducePrompt(c, armed, hotkey(ActionDenyWithReason)), renderOf(state(StepReason, ActionDenyWithReason, "", "", "", false)), "outcome")
	})
	tw(t, f, "opens the reason step immediately when double-press is disabled", func(t *testing.T) {
		c := modelConfig(noDouble)
		if opened(t, c, ActionDenyWithReason).Step != StepReason {
			t.Error("not the reason step")
		}
	})
	tw(t, f, "rejects an empty reason and keeps the reason step open", func(t *testing.T) {
		c := modelConfig(noDouble)
		s := opened(t, c, ActionDenyWithReason)
		eq(t, ReducePrompt(c, s, PromptEvent{Type: EventSubmitReason, Draft: "   "}), renderOf(state(StepReason, ActionDenyWithReason, "", "", "A reason is required.", false)), "outcome")
	})
	tw(t, f, "commits a denied_with_reason decision for a non-empty reason", func(t *testing.T) {
		c := modelConfig(noDouble)
		s := opened(t, c, ActionDenyWithReason)
		eq(t, ReducePrompt(c, s, PromptEvent{Type: EventSubmitReason, Draft: "  not now  "}),
			decisionOf(UnattributedDecision{State: DecisionDeniedWithReason, DenialReason: sp("not now")}), "outcome")
	})
	tw(t, f, "navigates back to the decision step on escape from the reason step", func(t *testing.T) {
		c := modelConfig(noDouble)
		s := opened(t, c, ActionDenyWithReason)
		eq(t, ReducePrompt(c, s, PromptEvent{Type: EventCancel}), renderOf(state(StepDecision, ActionDenyWithReason, "", "", "", false)), "outcome")
	})

	tw(t, f, "opens the scope step when s is confirmed and a sessionScope is offered", func(t *testing.T) {
		c := modelConfig(noDouble, withScope)
		s := opened(t, c, ActionApproveSession)
		if s.Step != StepScope || s.ScopeServing {
			t.Errorf("state %+v", s)
		}
	})
	tw(t, f, "commits the least-privilege subagent scope by default", func(t *testing.T) {
		c := modelConfig(noDouble, withScope)
		eq(t, ReducePrompt(c, opened(t, c, ActionApproveSession), PromptEvent{Type: EventConfirm}), decisionOf(forSession), "outcome")
	})
	tw(t, f, "commits the serving-session scope when the second option is chosen", func(t *testing.T) {
		c := modelConfig(noDouble, withScope)
		moved := asRender(t, ReducePrompt(c, opened(t, c, ActionApproveSession), nav("down")))
		if !moved.ScopeServing {
			t.Error("not serving")
		}
		eq(t, ReducePrompt(c, moved, PromptEvent{Type: EventConfirm}), decisionOf(servingSession), "outcome")
	})
	tw(t, f, "navigates back to the decision step on escape from the scope step", func(t *testing.T) {
		c := modelConfig(noDouble, withScope)
		got := asRender(t, ReducePrompt(c, opened(t, c, ActionApproveSession), PromptEvent{Type: EventCancel}))
		if got.Step != StepDecision {
			t.Errorf("step %q", got.Step)
		}
	})
	tw(t, f, "commits approved_for_session directly when no sessionScope is offered", func(t *testing.T) {
		c := modelConfig(noDouble)
		eq(t, ReducePrompt(c, initial(c), hotkey(ActionApproveSession)), decisionOf(forSession), "outcome")
	})

	tw(t, f, "offers the width option only when the ask is widenable", func(t *testing.T) {
		eq(t, VisibleActions(modelConfig(withWidth)), []PromptAction{ActionApprove, ActionApproveSession, ActionApproveSessionBoth, ActionDeny, ActionDenyWithReason}, "wide")
		eq(t, VisibleActions(modelConfig()), []PromptAction{ActionApprove, ActionApproveSession, ActionDeny, ActionDenyWithReason}, "narrow")
	})
	tw(t, f, "commits the family width on b", func(t *testing.T) {
		c := modelConfig(noDouble, withWidth)
		eq(t, ReducePrompt(c, initial(c), hotkey(ActionApproveSessionBoth)), decisionOf(familySession), "outcome")
	})
	tw(t, f, "leaves s at the proven width, naming no width at all", func(t *testing.T) {
		c := modelConfig(noDouble, withWidth)
		eq(t, ReducePrompt(c, initial(c), hotkey(ActionApproveSession)), decisionOf(forSession), "outcome")
	})
	tw(t, f, "arms b like any other hotkey under double-press", func(t *testing.T) {
		c := modelConfig(withWidth)
		eq(t, ReducePrompt(c, initial(c), hotkey(ActionApproveSessionBoth)),
			renderOf(state(StepDecision, ActionApproveSessionBoth, ActionApproveSessionBoth, "Press b again to approve both directions for this session.", "", false)), "outcome")
	})
	tw(t, f, "ignores b when the ask is not widenable", func(t *testing.T) {
		c := modelConfig(noDouble)
		eq(t, ReducePrompt(c, initial(c), hotkey(ActionApproveSessionBoth)), renderOf(initial(c)), "outcome")
	})
	tw(t, f, "walks five options when the width option is offered", func(t *testing.T) {
		c := modelConfig(withWidth)
		s := initial(c)
		var seen []PromptAction
		for i := 0; i < 5; i++ {
			s = asRender(t, ReducePrompt(c, s, nav("down")))
			seen = append(seen, s.HighlightedAction)
		}
		eq(t, seen, []PromptAction{ActionApproveSession, ActionApproveSessionBoth, ActionDeny, ActionDenyWithReason, ActionApprove}, "walk")
	})
	tw(t, f, "skips b when navigating an ask that is not widenable", func(t *testing.T) {
		c := modelConfig()
		s := initial(c)
		var seen []PromptAction
		for i := 0; i < 4; i++ {
			s = asRender(t, ReducePrompt(c, s, nav("down")))
			seen = append(seen, s.HighlightedAction)
		}
		eq(t, seen, []PromptAction{ActionApproveSession, ActionDeny, ActionDenyWithReason, ActionApprove}, "walk")
	})
	tw(t, f, "carries the width chosen on b through the scope step", func(t *testing.T) {
		c := modelConfig(noDouble, withWidth, withScope)
		s := opened(t, c, ActionApproveSessionBoth)
		if s.Step != StepScope {
			t.Errorf("step %q", s.Step)
		}
		eq(t, ReducePrompt(c, s, PromptEvent{Type: EventConfirm}), decisionOf(familySession), "outcome")
	})
	tw(t, f, "carries the width onto a whole-serving-session grant too", func(t *testing.T) {
		c := modelConfig(noDouble, withWidth, withScope)
		moved := asRender(t, ReducePrompt(c, opened(t, c, ActionApproveSessionBoth), nav("down")))
		eq(t, ReducePrompt(c, moved, PromptEvent{Type: EventConfirm}),
			decisionOf(UnattributedDecision{Approved: true, State: DecisionApprovedForServingSess, SessionGrantWidth: "family"}), "outcome")
	})
	tw(t, f, "forgets a backed-out width when the scope step cancels", func(t *testing.T) {
		c := modelConfig(noDouble, withWidth, withScope)
		cancelled := asRender(t, ReducePrompt(c, opened(t, c, ActionApproveSessionBoth), PromptEvent{Type: EventCancel}))
		eq(t, cancelled.GrantWidth, SessionGrantWidth("proven"), "width")
		// The narrow option must not inherit the width the user backed out of.
		reopened := asRender(t, ReducePrompt(c, cancelled, hotkey(ActionApproveSession)))
		eq(t, ReducePrompt(c, reopened, PromptEvent{Type: EventConfirm}), decisionOf(forSession), "outcome")
	})
	tw(t, f, "names the bound character in the arming hint", func(t *testing.T) {
		c := modelConfig(func(c *PromptModelConfig) {
			c.Keys = DialogKeyBindings{ActionApprove: "1", ActionApproveSession: "2", ActionApproveSessionBoth: "3", ActionDeny: "4", ActionDenyWithReason: "5"}
		})
		eq(t, opened(t, c, ActionApprove).Hint, "Press 1 again to approve.", "hint")
	})
	tw(t, f, "names the default letter when no binding was configured", func(t *testing.T) {
		c := modelConfig()
		eq(t, opened(t, c, ActionDeny).Hint, "Press n again to deny.", "hint")
	})
}
