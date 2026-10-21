package pi_goal_x

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const recoveryFile = "goal-recovery"

func recoveryTempCtx(t *testing.T) storage {
	t.Helper()
	cwd := t.TempDir()
	must(0, os.MkdirAll(filepath.Join(cwd, goalsDir, "archived"), 0o755))
	must(0, os.MkdirAll(filepath.Join(cwd, goalsDir, ".locks"), 0o755))
	invalidateGoalPoolCache()
	invalidateGoalLedgerCache()
	return storage{cwd: cwd}
}

func recoveryWriteGoal(ctx storage) {
	must(ctx.writeActiveGoalFile(baseGoal("healthy goal", false, ms(2026, time.September, 6, 9, 0, 0))))
}

func recoveryWriteStaleLock(ctx storage, name string) {
	payload := marshalJSON(detailsOf("pid", 999999999.0, "startedAt", isoFromMs(clockMs()-5*60_000)), "")
	must(0, os.WriteFile(filepath.Join(ctx.cwd, goalsDir, ".locks", name), []byte(payload), 0o644))
}

func TestGoalRecovery(t *testing.T) {
	tw(t, recoveryFile, "recovery report: healthy fixture reports no issues", func(t *testing.T) {
		ctx := recoveryTempCtx(t)
		recoveryWriteGoal(ctx)
		report := runRecoveryReport(ctx)
		eq(t, report.healthy, true, "healthy")
		eq(t, len(report.malformedGoalFiles), 0, "malformedGoalFiles")
		eq(t, report.malformedLedgerLines, 0, "malformedLedgerLines")
		eq(t, len(report.staleLocks), 0, "staleLocks")
		eq(t, len(report.orphanedSnapshotGoals), 0, "orphanedSnapshotGoals")
		eq(t, regexp.MustCompile(`OK \x{2014} no issues found`).MatchString(formatRecoveryReport(report)), true, "format")
	})
	tw(t, recoveryFile, "recovery report: malformed goal file, bad ledger line, stale lock, and orphaned snapshot are all identified", func(t *testing.T) {
		ctx := recoveryTempCtx(t)
		recoveryWriteGoal(ctx)
		// Malformed goal file.
		must(0, os.WriteFile(filepath.Join(ctx.cwd, goalsDir, "active_goal_bad.md"), []byte("this is not a goal file"), 0o644))
		// Malformed ledger line.
		must(0, appendFileString(filepath.Join(ctx.cwd, goalsDir, "goal_events.jsonl"), "{{{ not json\n"))
		// Stale lock (dead pid + old startedAt).
		recoveryWriteStaleLock(ctx, "g1.lock")
		// Orphaned snapshot entry: a snapshot goal whose file does not exist.
		recoveryWriteGoal(ctx)
		ctx.readActiveGoalPool() // cold read writes the pool snapshot
		snapshotPath := filepath.Join(ctx.cwd, ".pi", ".goals-pool-snapshot.json")
		if _, err := os.Stat(snapshotPath); err != nil {
			t.Fatal("cold read wrote a snapshot")
		}
		snapshot := jo(t, string(must(os.ReadFile(snapshotPath))))
		goals := snapshot.vals["goals"].([]any)
		snapshot.set("goals", append(goals, detailsOf("goalId", "gone", "activePath", "active_goal_gone.md", "latestStatus", "active")))
		must(0, os.WriteFile(snapshotPath, []byte(marshalJSON(snapshot, "")), 0o644))

		report := runRecoveryReport(ctx)
		eq(t, report.healthy, false, "healthy")
		eq(t, len(report.malformedGoalFiles), 1, "malformedGoalFiles")
		eq(t, regexp.MustCompile(`active_goal_bad\.md$`).MatchString(report.malformedGoalFiles[0].relPath), true, "relPath")
		eq(t, report.malformedLedgerLines, 1, "malformedLedgerLines")
		eq(t, len(report.staleLocks), 1, "staleLocks")
		eq(t, report.staleLocks[0].fileName, "g1.lock", "lock name")
		eq(t, len(report.orphanedSnapshotGoals), 1, "orphans")
		eq(t, report.orphanedSnapshotGoals[0].goalID, "gone", "orphan id")
		eq(t, strings.Contains(formatRecoveryReport(report), "issues found"), true, "format")
	})
	tw(t, recoveryFile, "recovery repair: confirmation rejection touches nothing", func(t *testing.T) {
		ctx := recoveryTempCtx(t)
		recoveryWriteGoal(ctx)
		recoveryWriteStaleLock(ctx, "g1.lock")
		report := runRecoveryReport(ctx)
		eq(t, len(report.staleLocks), 1, "staleLocks")

		result := must(runRecoveryRepair(ctx, report, func() bool { return false }))
		eq(t, result.confirmed, false, "confirmed")
		eq(t, len(result.applied), 0, "applied")
		eq(t, result.backupDir == nil, true, "backupDir")
		if _, err := os.Stat(filepath.Join(ctx.cwd, goalsDir, ".locks", "g1.lock")); err != nil {
			t.Error("lock untouched")
		}
	})
	tw(t, recoveryFile, "recovery repair: confirmed repair backs up, removes stale locks, and refreshes the snapshot", func(t *testing.T) {
		ctx := recoveryTempCtx(t)
		recoveryWriteGoal(ctx)
		recoveryWriteStaleLock(ctx, "g1.lock")
		// Orphaned snapshot entry.
		ctx.readActiveGoalPool() // cold read writes the pool snapshot
		snapshotPath := filepath.Join(ctx.cwd, ".pi", ".goals-pool-snapshot.json")
		snapshot := jo(t, string(must(os.ReadFile(snapshotPath))))
		goals := snapshot.vals["goals"].([]any)
		orphanCount := len(goals)
		snapshot.set("goals", append(goals, detailsOf("goalId", "gone", "activePath", "active_goal_gone.md", "latestStatus", "active")))
		must(0, os.WriteFile(snapshotPath, []byte(marshalJSON(snapshot, "")), 0o644))

		report := runRecoveryReport(ctx)
		eq(t, len(report.staleLocks), 1, "staleLocks")
		eq(t, len(report.orphanedSnapshotGoals), 1, "orphans")

		result := must(runRecoveryRepair(ctx, report, func() bool { return true }))
		eq(t, result.confirmed, true, "confirmed")
		if result.backupDir == nil || !strings.HasPrefix(*result.backupDir, filepath.Join(ctx.cwd, recoveryBackupDir)) {
			t.Fatalf("backup dir under .recovery-backup: %v", result.backupDir)
		}
		exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
		eq(t, exists(filepath.Join(*result.backupDir, "lock-g1.lock")), true, "stale lock backed up")
		eq(t, exists(filepath.Join(ctx.cwd, goalsDir, ".locks", "g1.lock")), false, "stale lock removed")
		eq(t, exists(filepath.Join(*result.backupDir, "pool-snapshot.json")), true, "snapshot backed up before refresh")
		eq(t, len(result.applied), 2, "applied")

		// The refreshed snapshot no longer contains the orphan.
		refreshed := jo(t, string(must(os.ReadFile(snapshotPath))))
		rgoals := refreshed.vals["goals"].([]any)
		eq(t, len(rgoals), orphanCount, "refreshed goal count")
		for _, g := range rgoals {
			if id, _ := g.(*jsObject).str("goalId"); id == "gone" {
				t.Error("orphan dropped from the refreshed snapshot")
			}
		}
		// A follow-up report is healthy for the repaired classes.
		after := runRecoveryReport(ctx)
		eq(t, len(after.staleLocks), 0, "staleLocks after")
		eq(t, len(after.orphanedSnapshotGoals), 0, "orphans after")
	})
	tw(t, recoveryFile, "recovery report: never appends ledger events or rewrites goal files", func(t *testing.T) {
		ctx := recoveryTempCtx(t)
		recoveryWriteGoal(ctx)
		ledgerBefore := len(readGoalLedger(storage{cwd: ctx.cwd}).events)
		runRecoveryReport(ctx)
		runRecoveryReport(ctx)
		eq(t, len(readGoalLedger(storage{cwd: ctx.cwd}).events), ledgerBefore, "report is read-only for the ledger")
		n := 0
		for _, e := range must(os.ReadDir(filepath.Join(ctx.cwd, goalsDir))) {
			if strings.HasPrefix(e.Name(), "active_goal_") {
				n++
			}
		}
		eq(t, n, 1, "active goal files")
	})
}
