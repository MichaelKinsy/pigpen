package pi_goal_x

// Extra tests (not twins) that pin behavior of the original which the twins left unasserted; each test names the mutants of
// port/mutations.json (lane B) it exists for. Expected values come from the original's code (port/oracle), not from the Go code.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mbActEv(typ string, kv ...any) *jsObject {
	return ledgerEvent(typ, append([]any{"goalId", "g1", "at", "2026-01-01T09:00:00.000Z"}, kv...)...)
}

// activity-paused-reason, activity-blocker-default, activity-noise
func TestMutantsBActivity(t *testing.T) {
	long := strings.Repeat("a", 60) + "  \n\t " + strings.Repeat("b", 60)
	want := "Paused the goal \u2014 " + strings.Repeat("a", 60) + " " + strings.Repeat("b", 16) + "..."
	item, ok := mapActivityEvent(mbActEv("goal_paused", "reason", long), nil)
	eq(t, ok, true, "paused maps")
	eq(t, item.text, want, "paused reason is one line, cut to 80 chars with an ellipsis")
	item, _ = mapActivityEvent(mbActEv("goal_blocked", "reason", long), nil)
	eq(t, item.text, "Blocked \u2014 "+strings.Repeat("a", 60)+" "+strings.Repeat("b", 16)+"...", "blocked reason")
	item, _ = mapActivityEvent(mbActEv("goal_blocked"), nil)
	eq(t, item.text, "Reported a blocker.", "blocked without a reason")
	item, _ = mapActivityEvent(mbActEv("goal_blocked", "reason", ""), nil)
	eq(t, item.text, "Reported a blocker.", "blocked with an empty reason")
	for _, noise := range []string{"goal_focused", "goal_unfocused", "goal_stalled", "goal_budget_warning", "task_list_set"} {
		_, ok := mapActivityEvent(mbActEv(noise), nil)
		eq(t, ok, false, noise+" is low-value noise")
		eq(t, isActivityEvent(mbActEv(noise)), false, noise+" is not an activity event")
	}
	eq(t, isActivityEvent(mbActEv("goal_created")), true, "goal_created is an activity event")
}

// recovery-stale-ttl, recovery-lock-alive, recovery-malformed-message, recovery-healthy-ledger, recovery-nothing-to-repair,
// recovery-format-hint, recovery-age-round
func TestMutantsBRecovery(t *testing.T) {
	t.Run("a lock is stale when its pid is dead or it is older than 30 s", func(t *testing.T) {
		ctx := recoveryTempCtx(t)
		lock := func(name string, pid int, age int64) {
			payload := marshalJSON(detailsOf("pid", float64(pid), "startedAt", isoFromMs(clockMs()-age)), "")
			must(0, os.WriteFile(filepath.Join(ctx.cwd, goalsDir, ".locks", name), []byte(payload), 0o644))
		}
		self := os.Getpid()
		lock("a-live-old.lock", self, 60_000)
		lock("b-live-fresh.lock", self, 5_000)
		lock("c-dead-fresh.lock", 999999999, 1_000)
		var names []string
		for _, l := range scanStaleLocks(ctx) {
			names = append(names, l.fileName)
		}
		eq(t, names, []string{"a-live-old.lock", "c-dead-fresh.lock"}, "stale locks")
	})
	t.Run("a malformed goal file reads as the original's message and a malformed ledger line alone is unhealthy", func(t *testing.T) {
		ctx := recoveryTempCtx(t)
		recoveryWriteGoal(ctx)
		must(0, os.WriteFile(filepath.Join(ctx.cwd, goalsDir, "active_goal_bad.md"), []byte("not a goal file"), 0o644))
		report := runRecoveryReport(ctx)
		eq(t, report.malformedGoalFiles[0].err, "file does not parse as a goal record", "error")
		eq(t, formatRecoveryReport(report), "Recovery report: issues found.\n  - 1 malformed goal file(s):\n      .pi/goals/active_goal_bad.md \u2014 file does not parse as a goal record", "report without the repair hint")

		ctx = recoveryTempCtx(t)
		recoveryWriteGoal(ctx)
		must(0, appendFileString(filepath.Join(ctx.cwd, goalsDir, "goal_events.jsonl"), "{{{ not json\n"))
		report = runRecoveryReport(ctx)
		eq(t, report.malformedLedgerLines, 1, "ledger lines")
		eq(t, report.healthy, false, "a malformed ledger line makes the report unhealthy")
	})
	t.Run("repair with nothing to repair does not ask for confirmation", func(t *testing.T) {
		ctx := recoveryTempCtx(t)
		asked := false
		res, err := runRecoveryRepair(ctx, recoveryReport{healthy: true}, func() bool { asked = true; return true })
		eq(t, err, nil, "err")
		eq(t, asked, false, "confirm asked")
		eq(t, res.confirmed, false, "confirmed")
		eq(t, res.backupDir == nil, true, "no backup directory")
		eq(t, len(res.applied), 0, "applied")
		entries, _ := os.ReadDir(filepath.Join(ctx.cwd, recoveryBackupDir))
		eq(t, len(entries), 0, "no backup was made")
	})
	t.Run("the repair hint shows for stale locks alone and for orphans alone, and ages are rounded", func(t *testing.T) {
		hint := "Run `/goal-recovery repair` to remove stale locks and refresh the pool snapshot (confirmation + backup required)."
		stale := formatRecoveryReport(recoveryReport{staleLocks: []staleLockEntry{{fileName: "a.lock", pid: 12, ageMs: 45_500}}})
		eq(t, stale, "Recovery report: issues found.\n  - 1 stale lock(s):\n      a.lock (pid 12, 46s old)\n"+hint, "stale locks only")
		orphans := formatRecoveryReport(recoveryReport{orphanedSnapshotGoals: []orphanedSnapshotEntry{{goalID: "gone", activePath: "active_goal_gone.md"}}})
		eq(t, orphans, "Recovery report: issues found.\n  - 1 orphaned snapshot entry:\n      gone (active_goal_gone.md)\n"+hint, "orphans only")
		eq(t, strings.Contains(formatRecoveryReport(recoveryReport{malformedLedgerLines: 2}), "Run `/goal-recovery"), false, "no hint without locks or orphans")
	})
}

// health-v3, health-marker-version-required (equivalent), health-kb, health-hint-needs-legacy, health-hint-needs-file
func TestMutantsBSessionHealth(t *testing.T) {
	marker := checkpointTriggerPrompt("g1")
	h := inspectCheckpointHealth([]any{
		healthEntry("content", marker, "details", detailsOf("version", 3.0)),
		healthEntry("content", marker, "details", detailsOf("version", 2.0)),
		healthEntry("content", marker, "details", detailsOf("version", 1.0)),
		healthEntry("content", marker, "details", detailsOf()),
	})
	eq(t, h.v2Minimal, 2, "versions 2 and 3 with the marker shape are v2")
	eq(t, h.legacyFull, 2, "version 1 and a missing version are legacy")

	eq(t, formatChars(999), "999 chars", "below 1 KB")
	eq(t, formatChars(1_000), "1.0 KB", "1 KB")
	eq(t, formatChars(1_500), "1.5 KB", "KB")
	eq(t, formatChars(9_999), "10.0 KB", "KB below 10 KB")
	eq(t, formatChars(1_000_000), "1.0 MB", "MB")

	file := "/tmp/session.jsonl"
	empty := ""
	hint := "\n  close Pi, then run:\n    pi-goal-x-recover --session <path> --dry-run"
	allV2 := &checkpointHealth{total: 1, v2Minimal: 1}
	eq(t, strings.Contains(formatCheckpointHealthReport(allV2, &file), "close Pi"), false, "no hint when nothing is legacy")
	legacy := &checkpointHealth{total: 1, legacyFull: 1}
	eq(t, strings.Contains(formatCheckpointHealthReport(legacy, nil), "close Pi"), false, "no hint without a session file")
	eq(t, strings.Contains(formatCheckpointHealthReport(legacy, &empty), "close Pi"), false, "no hint for an empty session file")
	eq(t, strings.HasSuffix(formatCheckpointHealthReport(legacy, &file), hint), true, "hint with a legacy checkpoint and a session file")
}

func mbSessionEntry(id, content string, details *jsObject) string {
	e := healthEntry("id", id, "content", content)
	if details != nil {
		e.set("details", details)
	}
	return marshalJSON(e, "")
}

// recover-timestamp, recover-confirm, recover-saved-chars, recover-legacy-bracket, recover-malformed-count
func TestMutantsBSessionRecover(t *testing.T) {
	legacy := "[GOAL CHECKPOINT goalId=g1]\n" + strings.Repeat("x", 300)
	lines := []string{
		mbSessionEntry("a", legacy, detailsOf("goalId", "g1")),
		mbSessionEntry("b", legacy, detailsOf("goalId", "g1", "timestamp", "yesterday")),
		mbSessionEntry("c", legacy, detailsOf("goalId", "g1", "timestamp", 1234.0)),
		"{broken",
		"{also broken",
	}
	tr := transformSessionLines(strings.Join(lines, "\n"))
	eq(t, tr.changed, 3, "changed")
	eq(t, tr.malformed, 2, "malformed lines are counted and kept")
	eq(t, tr.outputLines[3], "{broken", "malformed line kept")
	saved := 0
	for i := range 3 {
		saved += utf16Len(lines[i]) - utf16Len(tr.outputLines[i])
	}
	eq(t, tr.savedChars, saved, "savedChars sums the shrunk characters")
	eq(t, tr.savedChars > 0, true, "savedChars positive")
	stamp := func(line string) any {
		return sessionRecord(sessionRecord(must(parseJSON([]byte(line)))).vals["details"]).vals["timestamp"]
	}
	if _, ok := stamp(tr.outputLines[0]).(float64); !ok {
		t.Errorf("a missing details.timestamp becomes the current time, got %#v", stamp(tr.outputLines[0]))
	}
	if _, ok := stamp(tr.outputLines[1]).(float64); !ok {
		t.Errorf("a non-numeric details.timestamp becomes the current time, got %#v", stamp(tr.outputLines[1]))
	}
	eq(t, stamp(tr.outputLines[2]), 1234.0, "a numeric timestamp is kept")

	for _, form := range []string{"GOAL CHECKPOINT", "GOAL CONTINUATION", "GOAL STALE"} {
		id, ok := recoverCheckpointGoalID(jo(t, `{"content":"[`+form+` goalId=abc-1] body"}`))
		eq(t, [2]any{id, ok}, [2]any{"abc-1", true}, form)
	}

	// The CLI: --apply without --confirm-pi-closed is refused before anything is read, and the dry run reports the malformed count.
	dir := t.TempDir()
	file := filepath.Join(dir, "s.jsonl")
	must(0, os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	var out, errb strings.Builder
	code := recoverSessionCheckpointsMain([]string{"--session", file, "--apply"}, dir, &out, &errb)
	eq(t, code, 1, "apply without confirmation exits 1")
	eq(t, strings.HasPrefix(errb.String(), "error: --apply requires --confirm-pi-closed."), true, errb.String())
	out.Reset()
	code = recoverSessionCheckpointsMain([]string{"--session", file}, dir, &out, &errb)
	eq(t, code, 0, "dry run exits 0")
	eq(t, strings.Contains(out.String(), "Estimated savings: "+jsNumber(float64(tr.savedChars))+" chars (2 malformed unrelated line(s) preserved)"), true, out.String())
}

// tasks-policy mutants of goal_policy.go
func TestMutantsBPolicy(t *testing.T) {
	check := func(v policyValidation, ok bool, msg string) {
		t.Helper()
		if v.ok != ok || v.message != msg {
			t.Errorf("got ok=%v %q, want ok=%v %q", v.ok, v.message, ok, msg)
		}
	}
	check(validateGoalCompletion(gpolGoal("status", "budget_limited"), ""), true, "")
	check(validateGoalCompletion(gpolGoal("status", "blocked"), ""), false, "Goal is blocked; update_goal(complete) does not apply.")
	check(validateGoalCompletion(gpolGoal("status", "complete", "autoContinue", false), ""), false, "Goal is complete; update_goal(complete) does not apply.")
	check(validateGoalCompletion(gpolGoal("id", "current"), "old"), false, "The active goal changed during this run; not marking it complete.")
	check(validateResumeGoal(gpolGoal("status", "active", "autoContinue", false)), true, "")
	eq(t, shouldQueueContinuation(gpolGoal("status", "active", "autoContinue", false)), false, "an active goal without autoContinue queues nothing")
	eq(t, shouldQueueContinuation(gpolGoal("status", "active", "autoContinue", true)), true, "an active autoContinue goal queues")

	g := gpolGoal("taskList", mbTaskListOf(`{"id":"t1","title":"T","status":"pending"}`))
	check(validateTaskSkip(g, "t1", "   \t"), false, "Skipping requires a non-empty reason.")
	check(validateTaskSkip(g, "t1", " why "), true, "")

	mk := func(n int) []any {
		out := []any{}
		for i := range n {
			out = append(out, jo(t, `{"id":"t`+jsNumber(float64(i))+`","title":"T"}`))
		}
		return out
	}
	check(validateTaskListProposal(g, mk(50), nil), true, "")
	check(validateTaskListProposal(g, mk(51), nil), false, "Task list cannot exceed 50 tasks.")
	check(validateTaskListProposal(g, []any{jo(t, `{"id":"a","title":"   "}`)}, nil), false, `Task "a" must have a non-empty title.`)
	check(validateTaskListProposal(g, []any{jo(t, `{"id":"  ","title":"T"}`)}, nil), false, "All tasks must have a non-empty id.")

	check0 := func(a completionReportArgs, want string) {
		t.Helper()
		eq(t, buildCompletionReport(a), want, "report")
	}
	check0(completionReportArgs{auditSkippedReason: "no auditor", detailedSummary: "D"}, "Goal audit skipped.\n\nReason: no auditor\n\nGoal complete.\n\nD")
	check0(completionReportArgs{taskSummary: "2/3 done", detailedSummary: "D"}, "Goal complete.\n\nTask summary: 2/3 done\n\nD")

	eq(t, countOrderedSteps("10. a\n11. b\n99) c"), 3, "two-digit ordered steps count")
	eq(t, countOrderedSteps("100. a\n101. b"), 0, "three digits are not an ordered step")
}

func mbTaskListOf(tasks ...string) *jsObject {
	return must(parseJSON([]byte(`{"tasks":[` + strings.Join(tasks, ",") + `]}`))).(*jsObject)
}

// tasks-policy mutants of goal_task_count.go, goal_task_derive.go, goal_tool_names.go, goal_pool.go, goal_task_index.go
func TestMutantsBTaskHelpers(t *testing.T) {
	tasks := []any{
		jo(t, `{"id":"a","status":"complete"}`),
		jo(t, `{"id":"b","status":"skipped"}`),
		jo(t, `{"id":"c","status":"pending","subtasks":[{"id":"c1","status":"complete"},{"id":"c2","status":"pending"}]}`),
	}
	c := countTaskSubtree(tasks)
	eq(t, [4]int{c.total, c.complete, c.skipped, c.pending}, [4]int{5, 2, 1, 3}, "counts walk the subtasks")
	c = countTaskSubtree(tasks, taskCountOpts{doneIncludesSkipped: true})
	eq(t, [4]int{c.total, c.complete, c.skipped, c.pending}, [4]int{5, 3, 1, 2}, "skipped tasks count as done")
	eq(t, countAllTasks(tasks), 5, "countAllTasks")

	eq(t, len(deriveTasksFromObjective("- [ ] one\n100. not a step")), 1, "three-digit numbering is not derived")
	eq(t, len(deriveTasksFromObjective("1. one\n12. two")), 2, "two-digit numbering is derived")

	g := gpolGoal("status", "paused", "taskList", mbTaskListOf(`{"id":"t1","title":"T","status":"pending"}`))
	eq(t, applicableGoalTools(g, true), []string{"create_goal", "get_goal", "update_goal", "set_goal_tasks"}, "a paused goal has no update_goal_task")
	g.set("status", "active")
	eq(t, applicableGoalTools(g, true), []string{"create_goal", "get_goal", "update_goal", "set_goal_tasks", "update_goal_task"}, "an active goal with tasks")

	done := gpolGoal("id", "done1", "status", "complete")
	open := gpolGoal("id", "open1")
	pool := goalPoolFromGoals([]*jsObject{done, open})
	eq(t, pool.get("done1") == nil, true, "complete goals are not pooled")
	eq(t, pool.get("open1") != nil, true, "open goals are pooled")

	// The index cache keeps up to 32 distinct task lists.
	mkList := func(n int) []any {
		return []any{jo(t, `{"id":"idx`+jsNumber(float64(n))+`-`+jsNumber(float64(time.Now().UnixNano()))+`","title":"T","status":"pending"}`)}
	}
	first := mkList(0)
	idx := taskIndex(first)
	for i := 1; i < 5; i++ {
		taskIndex(mkList(i))
	}
	if taskIndex(first) != idx {
		t.Error("the index of an earlier list is still cached after four others")
	}
}

// flat-cap-gt, flat-title, flat-contract-trim, count-recursive, proposal-marker-complete, proposal-lightweight, update-polling-nan
func TestMutantsBTaskTools(t *testing.T) {
	flat := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"id": "t" + jsNumber(float64(i)), "title": "Task"}
		}
		return out
	}
	tasks, msg := convertFlatTasks(flat(50), 1)
	eq(t, [2]any{len(tasks), msg}, [2]any{50, ""}, "fifty tasks are allowed")
	_, msg = convertFlatTasks(flat(51), 1)
	eq(t, msg, "Task list cannot exceed 50 tasks.", "fifty-one tasks")
	_, msg = convertFlatTasks([]any{map[string]any{"id": "a", "title": " \t "}}, 1)
	eq(t, msg, `Task "a" must have a non-empty title.`, "blank title")
	tasks, msg = convertFlatTasks([]any{map[string]any{"id": "a", "title": " A ", "verification_contract": "  run it \n"}}, 1)
	eq(t, msg, "", "valid")
	eq(t, marshalJSON(tasks[0], ""), `{"id":"a","title":"A","status":"pending","verificationContract":"run it"}`, "title and contract are trimmed")

	nested := []any{jo(t, `{"id":"a","subtasks":[{"id":"b","subtasks":[{"id":"c"}]},{"id":"d"}]}`), jo(t, `{"id":"e"}`)}
	eq(t, countTasks(nested), 5, "countTasks counts every node")

	proposal := renderTaskProposalLines([]any{
		jo(t, `{"id":"a","title":"Done","status":"complete"}`),
		jo(t, `{"id":"b","title":"Skipped","status":"skipped"}`),
		jo(t, `{"id":"c","title":"Open","status":"pending","lightweightSubtasks":true,"subtasks":[{"id":"d","title":"Child","status":"pending"}]}`),
	}, 0)
	eq(t, proposal, []string{"[x] a: Done", "[~] b: Skipped", "[ ] c: Open (lightweight)", "  [ ] d: Child"}, "proposal lines")

	in := decodeGoalContinuation(map[string]any{"kind": "wait", "reason": "r", "deadline": "d", "polling": map[string]any{"interval_seconds": "x", "max_checks": 2.0}})
	eq(t, in.polling != nil && math.IsNaN(in.polling.intervalSeconds), true, "a non-number interval is NaN")
	eq(t, in.polling.maxChecks, 2.0, "a number is kept")
}
