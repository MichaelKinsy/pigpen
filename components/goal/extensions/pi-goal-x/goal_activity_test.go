package pi_goal_x

import (
	"strings"
	"testing"
)

const activityFile = "goal-activity"

// actEv is ev(type, at, extra): { type, goalId: "g1", at, ...extra }.
func actEv(typ, at string, kv ...any) *jsObject {
	return ledgerEvent(typ, append([]any{"goalId", "g1", "at", at}, kv...)...)
}

var activityTitles = map[string]string{
	"t1": "Review reports page and data source",
	"t2": "Implement filtered CSV export",
	"t3": "Add the download button",
}

func activityTexts(items []goalActivityItem) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.text)
	}
	return out
}

func activityLimit(n int) *int { return &n }

func TestGoalActivity(t *testing.T) {
	tw(t, activityFile, "lifecycle events map to readable activity text", func(t *testing.T) {
		events := []*jsObject{
			actEv("goal_created", "2026-01-01T09:00:00.000Z", "objective", "x", "sisyphus", false, "autoContinue", true),
			actEv("goal_tweaked", "2026-01-01T09:01:00.000Z", "changeSummary", "plan"),
			actEv("goal_paused", "2026-01-01T09:02:00.000Z", "reason", "User away", "suggestedAction", "Resume later"),
			actEv("goal_resumed", "2026-01-01T09:03:00.000Z", "reason", "Back"),
			actEv("goal_blocked", "2026-01-01T09:04:00.000Z", "reason", "Missing API key", "source", "agent"),
			actEv("goal_budget_limited", "2026-01-01T09:05:00.000Z", "budget", 10000.0, "tokensUsed", 10000.0),
			actEv("completion_requested", "2026-01-01T09:06:00.000Z", "summary", "done"),
			actEv("audit_started", "2026-01-01T09:07:00.000Z", "provider", "anthropic", "model", "x"),
			actEv("audit_result", "2026-01-01T09:08:00.000Z", "verdict", "approved", "report", "ok"),
			actEv("goal_completed", "2026-01-01T09:09:00.000Z", "archivePath", ".pi/goals/archived/goal_g1.md"),
			actEv("goal_archived", "2026-01-01T09:10:00.000Z", "archivePath", ".pi/goals/archived/goal_g1.md"),
		}
		texts := activityTexts(deriveGoalActivity(events, "g1", deriveGoalActivityOptions{limit: activityLimit(20)}))
		eq(t, texts, []string{
			"Created and focused the goal.",
			"Updated the goal objective and task plan.",
			"Paused the goal \u2014 User away",
			"Resumed the goal.",
			"Blocked \u2014 Missing API key",
			"Reached the configured token budget.",
			"Requested completion review.",
			"Started independent completion review.",
			"Independent auditor approved completion.",
			"Completed the goal.",
			"Archived the completed goal.",
		}, "texts")
	})
	tw(t, activityFile, "audit rejection and error verdicts map distinctly", func(t *testing.T) {
		rejected := deriveGoalActivity([]*jsObject{actEv("audit_result", "2026-01-01T09:00:00.000Z", "verdict", "disapproved", "report", "tests missing")}, "g1", deriveGoalActivityOptions{})
		eq(t, rejected[0].text, "Completion review requested additional work.", "rejected")
		errored := deriveGoalActivity([]*jsObject{actEv("audit_result", "2026-01-01T09:00:00.000Z", "verdict", "error", "report", "boom")}, "g1", deriveGoalActivityOptions{})
		eq(t, errored[0].text, "Completion review could not finish.", "errored")
	})
	tw(t, activityFile, "audit-skipped distinguishes disabled from user-aborted", func(t *testing.T) {
		disabled := deriveGoalActivity([]*jsObject{actEv("audit_skipped", "2026-01-01T09:00:00.000Z", "reason", "disabled")}, "g1", deriveGoalActivityOptions{})
		eq(t, disabled[0].text, "Skipped independent completion review (auditor disabled).", "disabled")
		aborted := deriveGoalActivity([]*jsObject{actEv("audit_skipped", "2026-01-01T09:00:00.000Z", "reason", "user_aborted")}, "g1", deriveGoalActivityOptions{})
		eq(t, aborted[0].text, "Completion review was aborted by the user.", "aborted")
	})
	tw(t, activityFile, "task events prefer task titles over ids", func(t *testing.T) {
		events := []*jsObject{
			actEv("task_started", "2026-01-01T09:00:00.000Z", "taskId", "t3"),
			actEv("task_complete", "2026-01-01T09:05:00.000Z", "taskId", "t2", "evidence", "All tests pass"),
			actEv("task_skipped", "2026-01-01T09:06:00.000Z", "taskId", "t1", "reason", "out of scope"),
			actEv("task_reopened", "2026-01-01T09:07:00.000Z", "taskId", "t1"),
		}
		texts := activityTexts(deriveGoalActivity(events, "g1", deriveGoalActivityOptions{taskTitles: activityTitles, limit: activityLimit(20)}))
		eq(t, texts, []string{
			"Started \u201cAdd the download button\u201d.",
			"Completed \u201cImplement filtered CSV export\u201d. \u2014 All tests pass",
			"Skipped \u201cReview reports page and data source\u201d \u2014 out of scope.",
			"Reopened \u201cReview reports page and data source\u201d.",
		}, "texts")
	})
	tw(t, activityFile, "unknown task ids fall back to the id in quotes", func(t *testing.T) {
		items := deriveGoalActivity([]*jsObject{actEv("task_started", "2026-01-01T09:00:00.000Z", "taskId", "t9")}, "g1", deriveGoalActivityOptions{})
		eq(t, items[0].text, "Started \u201ct9\u201d.", "text")
	})
	tw(t, activityFile, "evidence is included only when concise", func(t *testing.T) {
		short := deriveGoalActivity([]*jsObject{actEv("task_complete", "2026-01-01T09:00:00.000Z", "taskId", "t1", "evidence", "Done")}, "g1", deriveGoalActivityOptions{taskTitles: activityTitles})
		eq(t, short[0].text, "Completed \u201cReview reports page and data source\u201d. \u2014 Done", "short")
		longEvidence := strings.Repeat("x", 200)
		long := deriveGoalActivity([]*jsObject{actEv("task_complete", "2026-01-01T09:00:00.000Z", "taskId", "t1", "evidence", longEvidence)}, "g1", deriveGoalActivityOptions{taskTitles: activityTitles})
		eq(t, long[0].text, "Completed \u201cReview reports page and data source\u201d.", "long")
		eq(t, strings.Contains(long[0].text, longEvidence), false, "evidence omitted")
	})
	tw(t, activityFile, "long reasons are truncated safely", func(t *testing.T) {
		reason := strings.Repeat("because ", 60)
		items := deriveGoalActivity([]*jsObject{actEv("task_skipped", "2026-01-01T09:00:00.000Z", "taskId", "t1", "reason", reason)}, "g1", deriveGoalActivityOptions{taskTitles: activityTitles})
		eq(t, utf16Len(items[0].text) < 200, true, "length")
		eq(t, strings.HasSuffix(items[0].text, "..."), true, "ellipsis")
	})
	tw(t, activityFile, "items are ordered chronologically (oldest first)", func(t *testing.T) {
		events := []*jsObject{
			actEv("task_complete", "2026-01-01T09:05:00.000Z", "taskId", "t2"),
			actEv("task_started", "2026-01-01T09:00:00.000Z", "taskId", "t3"),
			actEv("goal_created", "2026-01-01T08:59:00.000Z", "objective", "x", "sisyphus", false, "autoContinue", true),
		}
		ats := []string{}
		for _, i := range deriveGoalActivity(events, "g1", deriveGoalActivityOptions{limit: activityLimit(20)}) {
			ats = append(ats, i.at)
		}
		eq(t, ats, []string{"2026-01-01T08:59:00.000Z", "2026-01-01T09:00:00.000Z", "2026-01-01T09:05:00.000Z"}, "ats")
	})
	tw(t, activityFile, "duplicate lifecycle events are merged", func(t *testing.T) {
		events := []*jsObject{
			actEv("goal_paused", "2026-01-01T09:00:00.000Z", "reason", "away"),
			actEv("goal_paused", "2026-01-01T09:01:00.000Z", "reason", "away"),
			actEv("goal_resumed", "2026-01-01T09:02:00.000Z", "reason", "back"),
			actEv("goal_paused", "2026-01-01T09:03:00.000Z", "reason", "away again"),
		}
		texts := activityTexts(deriveGoalActivity(events, "g1", deriveGoalActivityOptions{limit: activityLimit(20)}))
		eq(t, texts, []string{"Paused the goal \u2014 away", "Resumed the goal.", "Paused the goal \u2014 away again"}, "texts")
	})
	tw(t, activityFile, "checkpoint noise is excluded", func(t *testing.T) {
		events := []*jsObject{
			actEv("goal_created", "2026-01-01T09:00:00.000Z", "objective", "x", "sisyphus", false, "autoContinue", true),
			actEv("goal_focused", "2026-01-01T09:00:01.000Z", "reason", "selected"),
			actEv("goal_unfocused", "2026-01-01T09:00:02.000Z", "reason", "cleared"),
			actEv("task_list_set", "2026-01-01T09:00:03.000Z", "taskCount", 5.0, "blockCompletion", false),
			actEv("goal_stalled", "2026-01-01T09:00:04.000Z", "reason", "no work"),
			actEv("goal_budget_warning", "2026-01-01T09:00:05.000Z", "budget", 10000.0, "tokensUsed", 9000.0, "pct", 90.0),
		}
		texts := activityTexts(deriveGoalActivity(events, "g1", deriveGoalActivityOptions{limit: activityLimit(20)}))
		eq(t, texts, []string{"Created and focused the goal."}, "texts")
	})
	tw(t, activityFile, "default cap is the latest five entries; full history is available on request", func(t *testing.T) {
		events := []*jsObject{}
		for _, n := range []string{"1", "2", "3", "4", "5", "6", "7"} {
			events = append(events, actEv("task_complete", "2026-01-01T09:0"+n+":00.000Z", "taskId", "t"+n))
		}
		capped := deriveGoalActivity(events, "g1", deriveGoalActivityOptions{})
		eq(t, len(capped), 5, "capped")
		eq(t, capped[0].at, "2026-01-01T09:03:00.000Z", "first")
		full := deriveGoalActivity(events, "g1", deriveGoalActivityOptions{limit: activityLimit(100)})
		eq(t, len(full), 7, "full")
	})
	tw(t, activityFile, "only events for the requested goal are included", func(t *testing.T) {
		other := actEv("goal_created", "2026-01-01T09:01:00.000Z", "objective", "other", "sisyphus", false, "autoContinue", true)
		other.set("goalId", "g2")
		events := []*jsObject{
			actEv("goal_created", "2026-01-01T09:00:00.000Z", "objective", "x", "sisyphus", false, "autoContinue", true),
			other,
		}
		items := deriveGoalActivity(events, "g1", deriveGoalActivityOptions{})
		eq(t, len(items), 1, "items")
		eq(t, items[0].at, "2026-01-01T09:00:00.000Z", "at")
	})
}
