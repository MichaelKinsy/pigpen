package pi_goal_x

// Twins of the objective-sync cases of tests/goal-e2e.test.ts (the SDK-run cases live in goal_e2e_test.go).

import (
	"strings"
	"testing"
)

const geF = "goal-e2e"

func TestGoalE2ESyncTwins(t *testing.T) {
	mk := func(kv ...any) *jsObject {
		return gtpMakeGoal(ms(2026, 6, 26, 8, 0, 0), "Initial objective for e2e test", kv...)
	}
	completeOf := func(g *jsObject) *jsObject {
		return with(g, "status", "complete", "stopReason", "agent", "updatedAt", isoFromMs(ms(2026, 6, 26, 9, 0, 0)))
	}
	const summary = "Goal: Build feature X\nUpdated objective: Build feature Y\nStatus: active"

	tw(t, geF, "sequential quick-syncs: two updates, only latest objective on disk", func(t *testing.T) {
		s := tempStore(t)
		obj1, obj2, obj3 := "First objective", "Second objective after more drift", "Third objective — final version"
		goal := mk("objective", obj1)
		active := mustWrite(t, s, goal)
		eq(t, gstr(active, "objective"), obj1, "initial objective")

		after1 := mustWrite(t, s, with(active, "objective", obj2))
		eq(t, gstr(after1, "objective"), obj2, "update 1")
		eq(t, gstr(after1, "status"), "active", "status 1")

		after2 := mustWrite(t, s, with(after1, "objective", obj3))
		eq(t, gstr(after2, "objective"), obj3, "update 2")
		eq(t, gstr(after2, "status"), "active", "status 2")

		disk := readRel(t, s, gstr(after2, "activePath"))
		tpNotContains(t, disk, obj1, "disk")
		tpNotContains(t, disk, obj2, "disk")
		tpContains(t, disk, obj3, "disk")
		eq(t, s.readActiveGoalPool().has(gstr(goal, "id")), true, "pool membership unchanged")
		eq(t, gstr(after2, "activePath"), gstr(active, "activePath"), "same active file path")
	})
	tw(t, geF, "quick-sync then later complete: archived file has updated objective", func(t *testing.T) {
		s := tempStore(t)
		originalObj, updatedObj := "Original objective", "Updated objective after requirements changed"
		active := mustWrite(t, s, mk("objective", originalObj))

		synced := mustWrite(t, s, with(active, "objective", updatedObj))
		eq(t, gstr(synced, "objective"), updatedObj, "synced objective")
		eq(t, gstr(synced, "status"), "active", "synced status")
		eq(t, hasAnyKey(synced, "archivedPath"), false, "synced archivedPath")

		completed := mustWrite(t, s, completeOf(synced))
		eq(t, gstr(completed, "objective"), updatedObj, "completed file must have updated objective")
		eq(t, gstr(completed, "status"), "complete", "completed status")
		matches(t, gstr(completed, "activePath"), `^\.pi/goals/active_goal_`, "deferred archival: still in active dir")
		eq(t, hasAnyKey(completed, "archivedPath"), false, "deferred archival: not archived yet")

		archived := mustArchive(t, s, completed)
		eq(t, hasAnyKey(archived, "activePath"), false, "archived activePath")
		matches(t, gstr(archived, "archivedPath"), `^\.pi/goals/archived/goal_`, "archivedPath")

		content := readRel(t, s, gstr(archived, "archivedPath"))
		tpContains(t, content, updatedObj, "archived file must have the updated objective")
		tpNotContains(t, content, originalObj, "archived file must NOT have the original objective")
		tpContains(t, content, `"status": "complete"`, "archived file must have complete status")
	})
	tw(t, geF, "sync + approval path: report includes new objective and approval", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: summary, auditorReport: "Inspected and verified.\n\n<approved/>"})
		tpContains(t, r, "Goal audit approved.", "must say approved")
		tpContains(t, r, "<approved/>", "must include approval marker")
		tpContains(t, r, "Goal complete.", "must conclude with Goal complete.")
		tpContains(t, r, "Build feature Y", "must reference the updated objective")
	})
	tw(t, geF, "sync + disabled bypass: report includes new objective and skip reason", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: summary, auditSkippedReason: "auditor disabled in settings"})
		tpContains(t, r, "Goal audit skipped.", "must say skipped")
		tpContains(t, r, "auditor disabled in settings", "must include skip reason")
		tpContains(t, r, "Goal complete.", "complete")
		tpContains(t, r, "Build feature Y", "must reference the updated objective")
		tpNotContains(t, r, "<approved/>", "must NOT include approval marker")
	})
	tw(t, geF, "sync + Esc bypass: report includes new objective and Esc reason", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: summary, auditSkippedReason: "auditor bypassed (user pressed Escape during audit)"})
		tpContains(t, r, "Goal audit skipped.", "skipped")
		tpContains(t, r, "auditor bypassed (user pressed Escape during audit)", "reason")
		tpContains(t, r, "Goal complete.", "complete")
		tpContains(t, r, "Build feature Y", "objective")
		tpNotContains(t, r, "<approved/>", "no marker")
	})
	tw(t, geF, "multiple syncs then complete: final objective in archived file", func(t *testing.T) {
		s := tempStore(t)
		objs := []string{"First objective", "Second objective", "Third and final objective"}
		current := mustWrite(t, s, mk("objective", objs[0]))
		for _, obj := range objs {
			current = mustWrite(t, s, with(current, "objective", obj))
			eq(t, gstr(current, "objective"), obj, "sync objective")
			eq(t, gstr(current, "status"), "active", "sync status")
		}
		completed := mustWrite(t, s, completeOf(current))
		eq(t, gstr(completed, "objective"), objs[2], "completed objective")

		archived := mustArchive(t, s, completed)
		content := readRel(t, s, gstr(archived, "archivedPath"))
		tpContains(t, content, objs[2], "archived must have the final objective")
		tpNotContains(t, content, objs[0], "archived must NOT have obj1")
		tpNotContains(t, content, objs[1], "archived must NOT have obj2")
	})
	tw(t, geF, "sync while paused: status stays paused, objective changed on disk", func(t *testing.T) {
		s := tempStore(t)
		originalObj, newObj := "Paused goal objective", "Updated while paused"
		paused := mustWrite(t, s, mk("objective", originalObj, "status", "paused"))
		eq(t, gstr(paused, "status"), "paused", "initial status")
		eq(t, gstr(paused, "objective"), originalObj, "initial objective")

		updated := mustWrite(t, s, with(paused, "objective", newObj))
		eq(t, gstr(updated, "status"), "paused", "status must stay paused")
		eq(t, gstr(updated, "objective"), newObj, "objective must be updated")

		disk := readRel(t, s, gstr(updated, "activePath"))
		tpContains(t, disk, newObj, "disk must have new objective")
		tpContains(t, disk, `"status": "paused"`, "disk must show paused status")
	})
	tw(t, geF, "deferred archival after sync: verify active then archived", func(t *testing.T) {
		s := tempStore(t)
		updatedObj := "Objective updated before completion"
		goal := mk("objective", "Original")
		active := mustWrite(t, s, goal)

		synced := mustWrite(t, s, with(active, "objective", updatedObj))
		eq(t, hasAnyKey(synced, "archivedPath"), false, "synced archivedPath")

		completed := mustWrite(t, s, completeOf(synced))
		matches(t, gstr(completed, "activePath"), `^\.pi/goals/active_goal_`, "after mark complete: still active file")
		eq(t, hasAnyKey(completed, "archivedPath"), false, "after mark complete: no archivedPath")
		eq(t, s.readActiveGoalPool().has(gstr(goal, "id")), false, "complete goal filtered from pool")

		archived := mustArchive(t, s, completed)
		eq(t, hasAnyKey(archived, "activePath"), false, "after archive: no activePath")
		matches(t, gstr(archived, "archivedPath"), `^\.pi/goals/archived/goal_`, "after archive: has archivedPath")

		content := readRel(t, s, gstr(archived, "archivedPath"))
		tpContains(t, content, updatedObj, "archived file must have the synced objective")
		tpContains(t, content, `"status": "complete"`, "archived file must have complete status")
	})
	tw(t, geF, "all three bypass paths produce correct distinct reports", func(t *testing.T) {
		base := "Base detailed summary"
		approval := buildCompletionReport(completionReportArgs{detailedSummary: base, auditorReport: "All verified.\n\n<approved/>"})
		disabled := buildCompletionReport(completionReportArgs{detailedSummary: base, auditSkippedReason: "auditor disabled in settings"})
		esc := buildCompletionReport(completionReportArgs{detailedSummary: base, auditSkippedReason: "auditor bypassed (user pressed Escape during audit)"})

		tpContains(t, approval, "Goal audit approved.", "approval: header")
		tpContains(t, disabled, "Goal audit skipped.", "disabled: header")
		tpContains(t, esc, "Goal audit skipped.", "esc: header")
		tpContains(t, disabled, "auditor disabled in settings", "disabled: specific reason")
		tpContains(t, esc, "auditor bypassed (user pressed Escape during audit)", "esc: specific reason")
		tpContains(t, approval, "<approved/>", "approval: marker")
		tpNotContains(t, disabled, "<approved/>", "disabled: no marker")
		tpNotContains(t, esc, "<approved/>", "esc: no marker")
		tpContains(t, approval, "Goal complete.", "approval: complete")
		tpContains(t, disabled, "Goal complete.", "disabled: complete")
		tpContains(t, esc, "Goal complete.", "esc: complete")

		for _, label := range []string{"approval", "disabled", "esc"} {
			s := tempStore(t)
			obj := "Bypass test: " + label
			active := mustWrite(t, s, mk("objective", obj))
			eq(t, gstr(active, "objective"), obj, label+": objective")

			completed := mustWrite(t, s, completeOf(active))
			eq(t, gstr(completed, "status"), "complete", label+": status")
			eq(t, hasAnyKey(completed, "archivedPath"), false, label+": not yet archived")

			archived := mustArchive(t, s, completed)
			matches(t, gstr(archived, "archivedPath"), `^\.pi/goals/archived/goal_`, label+": archivedPath")
			if c := readRel(t, s, gstr(archived, "archivedPath")); !strings.Contains(c, obj) {
				t.Errorf("%s: archived has correct objective: %q", label, c)
			}
		}
	})
}
