package pi_goal_x

import (
	"strings"
	"testing"
)

func TestGoalDraft(t *testing.T) {
	const f = "goal-draft"

	tw(t, f, "extractVerificationContract splits contract line from objective", func(t *testing.T) {
		objective, contract := extractVerificationContract("Do the thing.\nVerification contract: Run npm test (0 failures)")
		contains(t, objective, "Do the thing", "objective")
		contains(t, contract, "npm test", "contract")
		plainObjective, plainContract := extractVerificationContract("Just a plain objective")
		eqDeep(t, plainContract, "", "plain contract undefined")
		eqDeep(t, plainObjective, "Just a plain objective", "plain objective")
	})

	tw(t, f, "promptSafeObjective escapes only untrusted objective tags", func(t *testing.T) {
		defer redOnStub(t)
		eqDeep(t, promptSafeObjective("<untrusted_objective>x</untrusted_objective><keep>"), "&lt;untrusted_objective&gt;x&lt;/untrusted_objective&gt;<keep>", "escaped")
	})

	tw(t, f, "renderConfirmationTasks renders a flat and nested task tree", func(t *testing.T) {
		lines := renderConfirmationTasks([]*jsObject{
			taskObj("a", "A", "pending"),
			taskObj("b", "B", "complete", taskObj("b1", "B1", "pending")),
		}, 0)
		for _, want := range []string{"a: A", "b: B", "b1: B1"} {
			if !anyLine(lines, want) {
				t.Errorf("%q missing in %v", want, lines)
			}
		}
	})

	tw(t, f, "sisyphusObjectiveSufficient accepts inline and block ordered steps", func(t *testing.T) {
		eqDeep(t, sisyphusObjectiveSufficient("Refactor the auth flow: 1) extract token validation. 2) wire it into login. 3) update tests."), true, "inline numbered items")
		eqDeep(t, sisyphusObjectiveSufficient("Step 1: extract\nStep 2: wire\nStep 3: test"), true, "Step N blocks")
		eqDeep(t, sisyphusObjectiveSufficient("1. extract token validation\n2. wire it into login"), true, "numbered block")
		eqDeep(t, sisyphusObjectiveSufficient("Just do the thing cleanly"), false, "no step markers")
		eqDeep(t, sisyphusObjectiveSufficient(""), false, "empty objective")
	})

	tw(t, f, "goalDraftingPrompt routes the complete presentation through the tool renderer (PR E §57)", func(t *testing.T) {
		defer redOnStub(t)
		for _, focus := range []goalDraftingFocus{"goal", "sisyphus"} {
			prompt := goalDraftingPrompt("some topic", focus)
			for _, want := range []string{
				"do NOT duplicate the full proposal in a preceding prose message",
				"The tool call renderer is the scrollable, complete human presentation",
				"tasks` parameter of `propose_goal_draft",
			} {
				if !strings.Contains(prompt, want) {
					t.Errorf("%s prompt lacks %q", focus, want)
				}
			}
		}
	})
}

// redOnStub turns a panic from another slice's temporary stub into a plain red test instead of crashing the package run.
func redOnStub(t *testing.T) {
	t.Helper()
	if r := recover(); r != nil {
		t.Errorf("%v", r)
	}
}
