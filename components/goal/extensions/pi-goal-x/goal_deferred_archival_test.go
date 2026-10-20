package pi_goal_x

import "testing"

const gdaF = "goal-deferred-archival"

func TestGoalDeferredArchivalTwins(t *testing.T) {
	mk := func(kv ...any) *jsObject {
		return gtpMakeGoal(ms(2026, 6, 2, 10, 0, 0), "Deferred archival test goal", kv...)
	}
	tw(t, gdaF, "deferred archival lifecycle via writeActiveGoalFile then archiveGoalFile", func(t *testing.T) {
		s := tempStore(t)
		goal := mk()
		active := mustWrite(t, s, goal)
		matches(t, gstr(active, "activePath"), `^\.pi/goals/active_goal_`, "activePath")
		eq(t, hasAnyKey(active, "archivedPath"), false, "fresh goal has no archivedPath")

		if !s.readActiveGoalPool().has(gstr(goal, "id")) {
			t.Error("goal should be in active pool before completion")
		}

		completed := mustWrite(t, s, with(active, "status", "complete"))
		matches(t, gstr(completed, "activePath"), `^\.pi/goals/active_goal_`, "deferred activePath")
		eq(t, hasAnyKey(completed, "archivedPath"), false, "complete goal not archived yet")

		contains(t, readRel(t, s, gstr(completed, "activePath")), `"status": "complete"`, "disk")
		if s.readActiveGoalPool().has(gstr(goal, "id")) {
			t.Error("complete goal should NOT be in active pool")
		}
		eq(t, fileExistsAt(s, ".pi/goals/archived/null"), false, "not in archive dir yet")

		archived := mustArchive(t, s, completed)
		eq(t, hasAnyKey(archived, "activePath"), false, "archived goal has no activePath")
		matches(t, gstr(archived, "archivedPath"), `^\.pi/goals/archived/goal_`, "archivedPath")
		eq(t, fileExistsAt(s, gstr(completed, "activePath")), false, "active file removed")
		eq(t, fileExistsAt(s, gstr(archived, "archivedPath")), true, "archived file exists")
		for _, g := range s.scanActiveGoalFiles() {
			if gstr(g, "id") == gstr(goal, "id") {
				t.Error("goal should not appear in active files after archival")
			}
		}
	})
	tw(t, gdaF, "approval path: buildCompletionReport includes auditor report", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: "Goal: Audit approval test\nStatus: active", auditorReport: "Auditor: I have verified all requirements.\n\n<approved/>"})
		for _, w := range []string{"Goal audit approved.", "<approved/>", "Auditor: I have verified all requirements.", "Goal complete."} {
			contains(t, r, w, "approval")
		}
	})
	tw(t, gdaF, "disabled-bypass path: buildCompletionReport includes auditSkippedReason", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: "Goal: Disabled bypass test\nStatus: active", auditSkippedReason: "auditor disabled in settings"})
		for _, w := range []string{"Goal audit skipped.", "auditor disabled in settings", "Goal complete."} {
			contains(t, r, w, "disabled")
		}
		notContains(t, r, "<approved/>", "disabled")
		notContains(t, r, "Auditor approval:", "disabled")
	})
	tw(t, gdaF, "Esc-skip path: buildCompletionReport includes Esc-abort reason", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: "Goal: Esc abort test\nStatus: active", auditSkippedReason: "auditor bypassed (user pressed Escape during audit)"})
		for _, w := range []string{"Goal audit skipped.", "auditor bypassed (user pressed Escape during audit)", "Goal complete."} {
			contains(t, r, w, "esc")
		}
	})
	tw(t, gdaF, "all three paths produce distinct tool output text", func(t *testing.T) {
		d := "Goal: Distinct output test\nStatus: active"
		approval := buildCompletionReport(completionReportArgs{detailedSummary: d, auditorReport: "Inspected and verified.\n\n<approved/>"})
		disabled := buildCompletionReport(completionReportArgs{detailedSummary: d, auditSkippedReason: "auditor disabled in settings"})
		esc := buildCompletionReport(completionReportArgs{detailedSummary: d, auditSkippedReason: "auditor bypassed (user pressed Escape during audit)"})
		contains(t, approval, "Goal audit approved.", "approval header")
		contains(t, disabled, "Goal audit skipped.", "disabled header")
		contains(t, esc, "Goal audit skipped.", "esc header")
		contains(t, disabled, "auditor disabled in settings", "disabled reason")
		contains(t, esc, "auditor bypassed (user pressed Escape during audit)", "esc reason")
		notContains(t, approval, "Goal audit skipped.", "approval")
		notContains(t, disabled, "<approved/>", "disabled")
		notContains(t, esc, "<approved/>", "esc")
	})
	tw(t, gdaF, "readActiveGoalFiles filters complete goals regardless of archivedPath", func(t *testing.T) {
		s := tempStore(t)
		active := mustWrite(t, s, mk("id", "complete-filter-test"))
		if !s.readActiveGoalPool().has("complete-filter-test") {
			t.Error("active goal should be in pool")
		}
		done := mustWrite(t, s, with(active, "status", "complete"))
		matches(t, gstr(done, "activePath"), `^\.pi/goals/active_goal_`, "activePath")
		if s.readActiveGoalPool().has("complete-filter-test") {
			t.Error("complete goal should be filtered from pool even if not archived")
		}
		mustArchive(t, s, done)
		if s.readActiveGoalPool().has("complete-filter-test") {
			t.Error("archived goal should not be in pool")
		}
	})
	tw(t, gdaF, "detect complete-but-not-archived goal for turn_end archival", func(t *testing.T) {
		s := tempStore(t)
		active := mustWrite(t, s, mk("id", "pending-archival-detect"))
		deferred := mustWrite(t, s, with(active, "status", "complete"))
		eq(t, gstr(deferred, "status"), "complete", "status")
		eq(t, hasAnyKey(deferred, "archivedPath"), false, "archivedPath")
		matches(t, gstr(deferred, "activePath"), `^\.pi/goals/active_goal_`, "activePath")
		r := mustArchive(t, s, deferred)
		eq(t, hasAnyKey(r, "activePath"), false, "activePath removed")
		matches(t, gstr(r, "archivedPath"), `^\.pi/goals/archived/goal_`, "archivedPath")
	})
}
