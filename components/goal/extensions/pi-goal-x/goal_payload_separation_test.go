package pi_goal_x

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func payloadGoal(t *testing.T) *jsObject {
	g := createGoal("Deliver the audited thing.", true, false, false, ms(2026, 8, 23, 9, 0, 0))
	g.set("taskList", jo(t, `{"tasks":[{"id":"t1","title":"First task","status":"complete"},{"id":"t2","title":"Second task","status":"pending"}],"blockCompletion":true,"proposedAt":"2026-08-23T09:01:00.000Z"}`))
	g.set("verificationContract", "All suites green.")
	return g
}

func TestGoalPayloadSeparation(t *testing.T) {
	tw(t, "goal-payload-separation", "the report_auditor_progress tool no longer exists in any shipped surface", func(t *testing.T) {
		files, _ := filepath.Glob("*.go")
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, _ := os.ReadFile(f)
			if regexp.MustCompile(`name:\s*"report_auditor_progress"`).Match(src) || strings.Contains(string(src), "report_auditor_progress") {
				t.Errorf("%s mentions report_auditor_progress", f)
			}
		}
	})
	tw(t, "goal-payload-separation", "objective appears exactly once; task tree exactly once", func(t *testing.T) {
		g := payloadGoal(t)
		prompt := buildGoalAuditorPrompt(goalAuditorPromptArgs{
			goal:              g,
			detailedSummary:   "Goal: " + gstr(g, "objective") + "\nStatus: running\nTasks: 1/2 complete",
			completionSummary: "I claim it is done.",
		})
		eq(t, strings.Count(prompt, "Deliver the audited thing."), 1, "objective occurs once")
		eq(t, strings.Count(prompt, "[ ] t2"), 1, "task tree rendered once")
		eq(t, strings.Contains(prompt, "report_auditor_progress"), false, "no progress protocol")
	})
	tw(t, "goal-payload-separation", "carries focus/events/findings without repeating objective or policy", func(t *testing.T) {
		g := payloadGoal(t)
		g.set("currentTaskId", "t2")
		tasks := g.obj("taskList").vals["tasks"].([]any)
		tasks[1].(*jsObject).set("verificationContract", "Prove t2.")
		id := gstr(g, "id")
		ledger := oracleEvents(t,
			`{"type":"goal_resumed","goalId":"`+id+`","reason":"user","at":"2026-08-23T10:00:00.000Z"}`,
			`{"type":"audit_result","goalId":"`+id+`","verdict":"disapproved","report":"Missing evidence for t2.","at":"2026-08-23T11:00:00.000Z"}`,
		)
		delta := buildPostCompactionGoalDelta(postCompactionDeltaArgs{goal: g, ledgerEvents: ledger, otherOpenCount: 3})
		for _, re := range []string{`\[POST-COMPACTION RESYNC goalId=`, `Current task: t2 — Second task \(contract: Prove t2\.\)`, `goal_resumed`,
			`Latest unresolved auditor finding: Missing evidence for t2\.`, `Other open goals: 3`} {
			eq(t, regexp.MustCompile(re).MatchString(delta), true, re)
		}
		eq(t, strings.Contains(delta, gstr(g, "objective")), false, "delta must not repeat the objective")
		eq(t, strings.Contains(delta, "update_goal"), false, "delta must not restate lifecycle policy")
	})
}
