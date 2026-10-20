package pi_goal_x

import "strings"

// ported from goal-scheduler-state.ts. newGoalScheduler and normalizeGoalScheduler live in record.go.

// schedulerSummaryParts splits scheduling text into an append-safe counter line and the standing instructions.
// s nil is undefined; limit nil is an unlimited allowance; showRuns is true when the setting is undefined.
func schedulerSummaryParts(s *jsObject, limit *float64, showRuns bool) (runs, instructions string) {
	if limit != nil && showRuns {
		used := 0.0
		if s != nil {
			used = gnum(s, "used")
		}
		suffix := ""
		if *limit == 0 {
			suffix = " (automatic continuation disabled)"
		}
		runs = "Autonomous runs: " + jsNumber(used) + "/" + jsNumber(*limit) + suffix + "."
	}
	var lines []string
	if s != nil {
		if w := s.obj("wait"); w != nil {
			lines = append(lines, "Waiting: "+gstr(w, "reason")+"; wait_id="+gstr(w, "id")+"; deadline="+isoFromMs(int64(gnum(w, "deadline")))+".")
			if next, ok := w.num("nextCheckAt"); ok {
				lines = append(lines, "Next check: "+isoFromMs(int64(next))+"; "+jsNumber(gnum(w, "remainingChecks"))+" checks remaining.")
			}
		}
		if phase := gstr(s, "phase"); phase == "interrupted" || phase == "claimed" {
			lines = append(lines, "Execution requires dispatch admission or explicit /goal-resume after interruption.")
		}
	}
	return runs, strings.Join(lines, "\n")
}

func schedulerSummary(s *jsObject, limit *float64, showRuns bool) string {
	runs, instructions := schedulerSummaryParts(s, limit, showRuns)
	var parts []string
	if runs != "" {
		parts = append(parts, runs)
	}
	if instructions != "" {
		parts = append(parts, instructions)
	}
	return strings.Join(parts, "\n")
}
