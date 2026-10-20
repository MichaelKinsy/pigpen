package pi_goal_x

import (
	"strings"
	"testing"
)

const gpF = "goal-policy"

func gpolGoal(kv ...any) *jsObject {
	g := newObject()
	g.set("id", "g1")
	g.set("objective", "=== Goal ===\nObjective: test")
	g.set("status", "active")
	g.set("autoContinue", true)
	u := newObject()
	u.set("tokensUsed", 0.0)
	u.set("activeSeconds", 0.0)
	g.set("usage", u)
	g.set("sisyphus", false)
	g.set("updatedAt", "2026-05-12T00:00:00.000Z")
	for i := 0; i+1 < len(kv); i += 2 {
		g.set(kv[i].(string), kv[i+1])
	}
	return g
}

func gpSisyphus(kv ...any) *jsObject {
	return gpolGoal(append([]any{"objective", "=== Sisyphus Goal ===\nSteps:\n1. A\n2. B", "sisyphus", true}, kv...)...)
}

func TestGoalPolicyTwins(t *testing.T) {
	tw(t, gpF, "goal lifecycle completion gates reject unsafe transitions", func(t *testing.T) {
		hasMsg(t, validateGoalCompletion(gpolGoal("sisyphus", false), ""), true, "")
		hasMsg(t, validateGoalCompletion(nil, ""), false, "No goal is set")
		hasMsg(t, validateGoalCompletion(gpolGoal("id", "current"), "old"), false, "changed during this run")
		hasMsg(t, validateGoalCompletion(gpolGoal("status", "paused", "autoContinue", false), ""), true, "")
		hasMsg(t, validateGoalCompletion(gpolGoal("status", "complete", "autoContinue", false), ""), false, "complete")
		hasMsg(t, validateGoalCompletion(gpSisyphus(), ""), true, "")
	})
	tw(t, gpF, "update_goal(blocked) policy applies only to an active goal", func(t *testing.T) {
		hasMsg(t, validateGoalBlock(gpolGoal("status", "active")), true, "")
		hasMsg(t, validateGoalBlock(gpolGoal("status", "paused")), false, "applies only to an active goal")
		hasMsg(t, validateGoalBlock(gpolGoal("status", "complete")), false, "applies only to an active goal")
		hasMsg(t, validateGoalBlock(nil), false, "No goal is set")
	})
	tw(t, gpF, "resume and clear policy preserve human-owned lifecycle affordances", func(t *testing.T) {
		hasMsg(t, validateResumeGoal(nil), false, "No goal is set")
		hasMsg(t, validateResumeGoal(gpolGoal("status", "complete")), false, "Goal is complete")
		hasMsg(t, validateResumeGoal(gpolGoal("status", "active", "autoContinue", true)), false, "already running")
		hasMsg(t, validateResumeGoal(gpolGoal("status", "paused", "autoContinue", false)), true, "")

		eq(t, clearGoalCommandMessage(true), "Goal cleared and archived.", "archived")
		eq(t, clearGoalCommandMessage(false), "No goal is set.", "none")

		eq(t, buildCompletionReport(completionReportArgs{detailedSummary: "Goal: full objective\nStatus: complete"}),
			"Goal complete.\n\nGoal: full objective\nStatus: complete", "plain")
		eq(t, buildCompletionReport(completionReportArgs{detailedSummary: "Goal: full objective"}),
			"Goal complete.\n\nGoal: full objective", "plain2")
		eq(t, buildCompletionReport(completionReportArgs{detailedSummary: "Goal: full objective\nStatus: complete", auditorReport: "Audit Report\n\n<approved/>"}),
			"Goal audit approved.\n\nAuditor approval:\nAudit Report\n\n<approved/>\n\nGoal complete.\n\nGoal: full objective\nStatus: complete", "approved")
		long := strings.Join([]string{
			"I have inspected the codebase.",
			"The implementation covers all requirements:",
			"  - Feature A is fully implemented in src/a.ts",
			"  - Feature B is tested in tests/b.test.ts",
			"  - Documentation is updated in README.md",
			"<approved/>",
		}, "\n")
		r := buildCompletionReport(completionReportArgs{detailedSummary: "Goal: Build X\nStatus: active", auditorReport: long})
		for _, want := range []string{long, "<approved/>", "Goal audit approved."} {
			if !strings.Contains(r, want) {
				t.Fatalf("report lacks %q", want)
			}
		}
		eq(t, buildGoalCreatedReport(goalCreatedReportArgs{objective: "# Objective\nShip the feature.", detailedSummary: "Status: active"}),
			"Goal confirmed and created.\n\nFinalized goal:\n\n# Objective\nShip the feature.\n\nGoal details:\nBudget: none\nStatus: active", "created")

		skip := buildCompletionReport(completionReportArgs{detailedSummary: "Goal: Do the thing\nStatus: active", auditSkippedReason: "auditor disabled in settings"})
		for _, want := range []string{"Goal audit skipped.", "auditor disabled in settings", "Goal complete."} {
			if !strings.Contains(skip, want) {
				t.Fatalf("skip report lacks %q", want)
			}
		}
		prec := buildCompletionReport(completionReportArgs{detailedSummary: "Goal: Precedence test", auditorReport: "<approved/>", auditSkippedReason: "bypassed"})
		if !strings.Contains(prec, "Goal audit skipped.") {
			t.Fatal("skip must take precedence")
		}
		if strings.Contains(prec, "<approved/>") {
			t.Fatal("auditor report must be ignored")
		}
	})
	tw(t, gpF, "continuation and compaction policies are deterministic", func(t *testing.T) {
		eq(t, shouldQueueContinuation(gpolGoal("status", "active", "autoContinue", true)), true, "queue active")
		eq(t, shouldQueueContinuation(gpolGoal("status", "paused", "autoContinue", true)), false, "queue paused")
		eq(t, shouldArmPostCompactReminder(gpSisyphus("status", "active")), true, "arm active")
		eq(t, shouldArmPostCompactReminder(gpSisyphus("status", "paused", "autoContinue", false)), false, "arm paused")
		eq(t, shouldInjectPostCompactReminder(true, gpSisyphus()), true, "inject sisyphus")
		eq(t, shouldInjectPostCompactReminder(true, gpolGoal("sisyphus", false)), true, "inject plain")
		eq(t, shouldInjectPostCompactReminder(false, gpSisyphus()), false, "inject not pending")
	})
}
