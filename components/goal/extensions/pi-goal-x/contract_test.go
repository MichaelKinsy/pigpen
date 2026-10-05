package pi_goal_x

import (
	"strings"
	"testing"
)

func TestVerificationContract(t *testing.T) {
	tw(t, "verification-contract", "extractVerificationContract: no contract section returns objective unchanged", func(t *testing.T) {
		obj := "=== Goal ===\nObjective: Test\nSuccess criteria: Nothing"
		o, c := extractVerificationContract(obj)
		eq(t, o, obj, "objective")
		eq(t, c, "", "contract")
	})
	tw(t, "verification-contract", "extractVerificationContract: extracts contract from objective", func(t *testing.T) {
		o, c := extractVerificationContract("=== Goal ===\nObjective: Test\nSuccess criteria: Do the thing\nVerification contract: Run npm test (0 failures), grep for remaining references")
		eq(t, c, "Run npm test (0 failures), grep for remaining references", "contract")
		eq(t, strings.Contains(o, "Verification contract:"), false, "contract line removed")
		eq(t, strings.Contains(o, "Objective: Test"), true, "objective kept")
		eq(t, strings.Contains(o, "Success criteria: Do the thing"), true, "criteria kept")
	})
	tw(t, "verification-contract", "extractVerificationContract: handles multi-line objective with contract at end", func(t *testing.T) {
		obj := strings.Join([]string{"=== Goal ===", "Objective: Refactor STP module", "Success criteria:", "- Remove dead code", "- All tests pass", "Boundaries: src/ only",
			"Verification contract: npm test passes, no remaining STP references in grep"}, "\n")
		o, c := extractVerificationContract(obj)
		eq(t, c, "npm test passes, no remaining STP references in grep", "contract")
		eq(t, strings.Contains(o, "Verification contract:"), false, "removed")
		eq(t, strings.Contains(o, "Objective: Refactor STP module"), true, "objective")
		eq(t, strings.Contains(o, "Boundaries: src/ only"), true, "boundaries")
	})
	tw(t, "verification-contract", "extractVerificationContract: contract with empty value returns undefined", func(t *testing.T) {
		o, c := extractVerificationContract("=== Goal ===\nObjective: Test\nVerification contract:   \nSuccess criteria: OK")
		eq(t, c, "", "contract")
		eq(t, strings.Contains(o, "Verification contract:"), true, "the line stays")
	})
	tw(t, "verification-contract", "extractVerificationContract: handles Sisyphus goal format", func(t *testing.T) {
		o, c := extractVerificationContract("=== Sisyphus Goal ===\nObjective: Clean up STP\nOrdered steps:\n1. Remove dead code\n2. Update tests\nVerification contract: All tests pass (npm test, 0 failures), codebase has no references to removed methods")
		eq(t, c, "All tests pass (npm test, 0 failures), codebase has no references to removed methods", "contract")
		eq(t, strings.Contains(o, "Verification contract:"), false, "removed")
		eq(t, strings.Contains(o, "Ordered steps:"), true, "steps kept")
	})
}

func TestGoalDraftHelpers(t *testing.T) {
	tw(t, "goal-draft", "extractVerificationContract splits contract line from objective", func(t *testing.T) {
		o, c := extractVerificationContract("Do the thing.\nVerification contract: Run npm test (0 failures)")
		eq(t, strings.Contains(o, "Do the thing"), true, "objective")
		eq(t, strings.Contains(c, "npm test"), true, "contract")
		o, c = extractVerificationContract("Just a plain objective")
		eq(t, c, "", "plain contract")
		eq(t, o, "Just a plain objective", "plain objective")
	})
	tskip(t, "goal-draft", "promptSafeObjective escapes only untrusted objective tags", "promptSafeObjective guards the objective text injected into the drafting and execution prompts, which this port does not build (no model prompts)")
	tskip(t, "goal-draft", "renderConfirmationTasks renders a flat and nested task tree", "renderConfirmationTasks is the task-confirmation dialog of /goal drafting, which this port does not implement")
	tw(t, "goal-draft", "sisyphusObjectiveSufficient accepts inline and block ordered steps", func(t *testing.T) {
		eq(t, sisyphusObjectiveSufficient("Refactor the auth flow: 1) extract token validation. 2) wire it into login. 3) update tests."), true, "inline")
		eq(t, sisyphusObjectiveSufficient("Step 1: extract\nStep 2: wire\nStep 3: test"), true, "Step N")
		eq(t, sisyphusObjectiveSufficient("1. extract token validation\n2. wire it into login"), true, "block")
		eq(t, sisyphusObjectiveSufficient("Just do the thing cleanly"), false, "no markers")
		eq(t, sisyphusObjectiveSufficient(""), false, "empty")
	})
	tskip(t, "goal-draft", "goalDraftingPrompt routes the complete presentation through the tool renderer (PR E §57)", "goalDraftingPrompt is the model prompt that starts /goal drafting; drafting is not ported")
}
