package pi_goal_x

import "testing"

const geF = "goal-e2e"

func TestGoalE2ETwins(t *testing.T) {
	mk := func(objective string, kv ...any) *jsObject {
		return gtpMakeGoal(ms(2026, 6, 26, 8, 0, 0), objective, kv...)
	}
	complete := func(g *jsObject) *jsObject {
		return with(g, "status", "complete", "stopReason", "agent", "updatedAt", nowIso())
	}
	tw(t, geF, "sequential quick-syncs: two updates, only latest objective on disk", func(t *testing.T) {
		s := tempStore(t)
		obj1, obj2, obj3 := "First objective", "Second objective after more drift", "Third objective — final version"
		goal := mk(obj1)
		active := mustWrite(t, s, goal)
		eq(t, gstr(active, "objective"), obj1, "obj1")
		after1 := mustWrite(t, s, with(active, "objective", obj2))
		eq(t, gstr(after1, "objective"), obj2, "obj2")
		eq(t, gstr(after1, "status"), "active", "status1")
		after2 := mustWrite(t, s, with(after1, "objective", obj3))
		eq(t, gstr(after2, "objective"), obj3, "obj3")
		eq(t, gstr(after2, "status"), "active", "status2")
		disk := readRel(t, s, gstr(after2, "activePath"))
		notContains(t, disk, obj1, "disk")
		notContains(t, disk, obj2, "disk")
		contains(t, disk, obj3, "disk")
		if !s.readActiveGoalPool().has(gstr(goal, "id")) {
			t.Error("pool membership unchanged")
		}
		eq(t, gstr(after2, "activePath"), gstr(active, "activePath"), "same active path")
	})
	tw(t, geF, "quick-sync then later complete: archived file has updated objective", func(t *testing.T) {
		s := tempStore(t)
		originalObj, updatedObj := "Original objective", "Updated objective after requirements changed"
		active := mustWrite(t, s, mk(originalObj))
		synced := mustWrite(t, s, with(active, "objective", updatedObj))
		eq(t, gstr(synced, "objective"), updatedObj, "objective")
		eq(t, gstr(synced, "status"), "active", "status")
		eq(t, hasAnyKey(synced, "archivedPath"), false, "archivedPath")
		completed := mustWrite(t, s, complete(synced))
		eq(t, gstr(completed, "objective"), updatedObj, "completed objective")
		eq(t, gstr(completed, "status"), "complete", "completed status")
		matches(t, gstr(completed, "activePath"), `^\.pi/goals/active_goal_`, "deferred archival: still in active dir")
		eq(t, hasAnyKey(completed, "archivedPath"), false, "not archived yet")
		archived := mustArchive(t, s, completed)
		eq(t, hasAnyKey(archived, "activePath"), false, "activePath")
		matches(t, gstr(archived, "archivedPath"), `^\.pi/goals/archived/goal_`, "archivedPath")
		content := readRel(t, s, gstr(archived, "archivedPath"))
		contains(t, content, updatedObj, "archived")
		notContains(t, content, originalObj, "archived")
		contains(t, content, `"status": "complete"`, "archived")
	})
	d := "Goal: Build feature X\nUpdated objective: Build feature Y\nStatus: active"
	tw(t, geF, "sync + approval path: report includes new objective and approval", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: d, auditorReport: "Inspected and verified.\n\n<approved/>"})
		for _, w := range []string{"Goal audit approved.", "<approved/>", "Goal complete.", "Build feature Y"} {
			contains(t, r, w, "report")
		}
	})
	tw(t, geF, "sync + disabled bypass: report includes new objective and skip reason", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: d, auditSkippedReason: "auditor disabled in settings"})
		for _, w := range []string{"Goal audit skipped.", "auditor disabled in settings", "Goal complete.", "Build feature Y"} {
			contains(t, r, w, "report")
		}
		notContains(t, r, "<approved/>", "report")
	})
	tw(t, geF, "sync + Esc bypass: report includes new objective and Esc reason", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: d, auditSkippedReason: "auditor bypassed (user pressed Escape during audit)"})
		for _, w := range []string{"Goal audit skipped.", "auditor bypassed (user pressed Escape during audit)", "Goal complete.", "Build feature Y"} {
			contains(t, r, w, "report")
		}
		notContains(t, r, "<approved/>", "report")
	})
	tw(t, geF, "multiple syncs then complete: final objective in archived file", func(t *testing.T) {
		s := tempStore(t)
		objs := []string{"First objective", "Second objective", "Third and final objective"}
		current := mustWrite(t, s, mk(objs[0]))
		for _, o := range objs {
			current = mustWrite(t, s, with(current, "objective", o))
			eq(t, gstr(current, "objective"), o, "objective")
			eq(t, gstr(current, "status"), "active", "status")
		}
		completed := mustWrite(t, s, complete(current))
		eq(t, gstr(completed, "objective"), objs[2], "completed objective")
		archived := mustArchive(t, s, completed)
		content := readRel(t, s, gstr(archived, "archivedPath"))
		contains(t, content, objs[2], "archived")
		notContains(t, content, objs[0], "archived")
		notContains(t, content, objs[1], "archived")
	})
	tw(t, geF, "sync while paused: status stays paused, objective changed on disk", func(t *testing.T) {
		s := tempStore(t)
		paused := mustWrite(t, s, mk("Paused goal objective", "status", "paused"))
		eq(t, gstr(paused, "status"), "paused", "status")
		eq(t, gstr(paused, "objective"), "Paused goal objective", "objective")
		updated := mustWrite(t, s, with(paused, "objective", "Updated while paused"))
		eq(t, gstr(updated, "status"), "paused", "status must stay paused")
		eq(t, gstr(updated, "objective"), "Updated while paused", "objective")
		disk := readRel(t, s, gstr(updated, "activePath"))
		contains(t, disk, "Updated while paused", "disk")
		contains(t, disk, `"status": "paused"`, "disk")
	})
	tw(t, geF, "deferred archival after sync: verify active then archived", func(t *testing.T) {
		s := tempStore(t)
		updatedObj := "Objective updated before completion"
		goal := mk("Original")
		active := mustWrite(t, s, goal)
		synced := mustWrite(t, s, with(active, "objective", updatedObj))
		eq(t, hasAnyKey(synced, "archivedPath"), false, "archivedPath")
		completed := mustWrite(t, s, complete(synced))
		matches(t, gstr(completed, "activePath"), `^\.pi/goals/active_goal_`, "still active file")
		eq(t, hasAnyKey(completed, "archivedPath"), false, "no archivedPath")
		if s.readActiveGoalPool().has(gstr(goal, "id")) {
			t.Error("complete goal filtered from pool")
		}
		archived := mustArchive(t, s, completed)
		eq(t, hasAnyKey(archived, "activePath"), false, "no activePath")
		matches(t, gstr(archived, "archivedPath"), `^\.pi/goals/archived/goal_`, "archivedPath")
		content := readRel(t, s, gstr(archived, "archivedPath"))
		contains(t, content, updatedObj, "archived")
		contains(t, content, `"status": "complete"`, "archived")
	})
	tw(t, geF, "all three bypass paths produce correct distinct reports", func(t *testing.T) {
		base := "Base detailed summary"
		approval := buildCompletionReport(completionReportArgs{detailedSummary: base, auditorReport: "All verified.\n\n<approved/>"})
		disabled := buildCompletionReport(completionReportArgs{detailedSummary: base, auditSkippedReason: "auditor disabled in settings"})
		esc := buildCompletionReport(completionReportArgs{detailedSummary: base, auditSkippedReason: "auditor bypassed (user pressed Escape during audit)"})
		contains(t, approval, "Goal audit approved.", "approval header")
		contains(t, disabled, "Goal audit skipped.", "disabled header")
		contains(t, esc, "Goal audit skipped.", "esc header")
		contains(t, disabled, "auditor disabled in settings", "disabled reason")
		contains(t, esc, "auditor bypassed (user pressed Escape during audit)", "esc reason")
		contains(t, approval, "<approved/>", "approval marker")
		notContains(t, disabled, "<approved/>", "disabled")
		notContains(t, esc, "<approved/>", "esc")
		for _, r := range []string{approval, disabled, esc} {
			contains(t, r, "Goal complete.", "complete")
		}
		for _, label := range []string{"approval", "disabled", "esc"} {
			s := tempStore(t)
			obj := "Bypass test: " + label
			active := mustWrite(t, s, mk(obj))
			eq(t, gstr(active, "objective"), obj, "objective")
			completed := mustWrite(t, s, complete(active))
			eq(t, gstr(completed, "status"), "complete", "status")
			eq(t, hasAnyKey(completed, "archivedPath"), false, "archivedPath")
			archived := mustArchive(t, s, completed)
			matches(t, gstr(archived, "archivedPath"), `^\.pi/goals/archived/goal_`, "archivedPath")
			contains(t, readRel(t, s, gstr(archived, "archivedPath")), obj, label+": archived objective")
		}
	})
}
