package pi_goal_x

import (
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ported from goal-recovery.ts: the read-only storage/recovery report and the guarded repair. Malformed goal files and
// ledger lines are reported, never rewritten.

const (
	recoveryBackupDir = ".pi/goals/.recovery-backup"
	lockStaleTtlMs    = 30_000
	// goalLockDir is GOAL_LOCK_DIR (storage/goal-lock.ts).
	goalLockDir = lockDir
)

type malformedGoalFileEntry struct{ relPath, err string }

type staleLockEntry struct {
	fileName  string
	pid       float64
	startedAt string
	ageMs     float64
}

type orphanedSnapshotEntry struct{ goalID, activePath string }

type recoveryReport struct {
	scannedAt             string
	malformedGoalFiles    []malformedGoalFileEntry
	malformedLedgerLines  int
	staleLocks            []staleLockEntry
	orphanedSnapshotGoals []orphanedSnapshotEntry
	healthy               bool
}

type recoveryRepairResult struct {
	applied   []string
	backupDir *string // nil = null
	confirmed bool
}

// jsDateMs is `new Date(s).getTime()` for the ISO forms the ledger writes; ok=false is NaN.
func jsDateMs(s string) (float64, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.UnixMilli()), true
		}
	}
	return 0, false
}

func scanMalformedGoalFiles(ctx storage) []malformedGoalFileEntry {
	root := ctx.mustPath(goalsDir)
	out := []malformedGoalFileEntry{}
	names, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range names {
		name := e.Name()
		if !activeNameRE.MatchString(name) {
			continue
		}
		if parseGoalFile(filepath.Join(root, name)) == nil {
			out = append(out, malformedGoalFileEntry{relPath: path.Join(goalsDir, name), err: "file does not parse as a goal record"})
		}
	}
	return out
}

func scanStaleLocks(ctx storage) []staleLockEntry {
	dir := ctx.mustPath(goalLockDir)
	out := []staleLockEntry{}
	names, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	now := float64(clockMs())
	for _, e := range names {
		name := e.Name()
		if !strings.HasSuffix(name, ".lock") {
			continue
		}
		var raw *jsObject
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			if v, err := parseJSON(data); err == nil {
				raw, _ = v.(*jsObject)
			}
		}
		if raw == nil {
			// Unreadable lock body: report as stale (pid unknown).
			out = append(out, staleLockEntry{fileName: name, ageMs: now})
			continue
		}
		pid, _ := raw.num("pid")
		startedAt, _ := raw.str("startedAt")
		ageMs := now
		if startedMs, ok := jsDateMs(startedAt); ok {
			ageMs = math.Max(0, now-startedMs)
		}
		alive := pid == math.Trunc(pid) && pid > 0 && pidAlive(int(pid))
		if !alive || ageMs > lockStaleTtlMs {
			out = append(out, staleLockEntry{fileName: name, pid: pid, startedAt: startedAt, ageMs: ageMs})
		}
	}
	return out
}

func scanOrphanedSnapshotGoals(ctx storage) []orphanedSnapshotEntry {
	root := ctx.mustPath(goalsDir)
	out := []orphanedSnapshotEntry{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	present := map[string]bool{}
	for _, e := range entries {
		present[e.Name()] = true
	}
	data, err := os.ReadFile(ctx.snapshotPath())
	if err != nil {
		return out
	}
	v, err := parseJSON(data)
	if err != nil {
		return out
	}
	snap, _ := v.(*jsObject)
	if snap == nil {
		return out
	}
	goals, _ := snap.vals["goals"].([]any)
	for _, g := range goals {
		goal, ok := g.(*jsObject)
		if !ok {
			// `goal.activePath` of null/non-object: a throw (null) or undefined; the TS stops at the first throw.
			if g == nil {
				return out
			}
			continue
		}
		activePath, _ := goal.str("activePath")
		base := path.Base(activePath)
		if activePath == "" {
			base = ""
		}
		if base != "" && !present[base] {
			id, ok := goal.str("goalId")
			if !ok {
				id = base
			}
			out = append(out, orphanedSnapshotEntry{goalID: id, activePath: activePath})
		}
	}
	return out
}

// runRecoveryReport never mutates goal storage.
func runRecoveryReport(ctx storage) recoveryReport {
	ledger := readGoalLedger(ctx)
	malformedGoalFiles := scanMalformedGoalFiles(ctx)
	staleLocks := scanStaleLocks(ctx)
	orphans := scanOrphanedSnapshotGoals(ctx)
	return recoveryReport{
		scannedAt:             nowIso(),
		malformedGoalFiles:    malformedGoalFiles,
		malformedLedgerLines:  ledger.malformed,
		staleLocks:            staleLocks,
		orphanedSnapshotGoals: orphans,
		healthy:               len(malformedGoalFiles) == 0 && ledger.malformed == 0 && len(staleLocks) == 0 && len(orphans) == 0,
	}
}

func copyFileBytes(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// runRecoveryRepair removes stale locks and refreshes the pool snapshot after confirmation, backing the files up first. An
// error is the TS throw (the backup directory could not be made).
func runRecoveryRepair(ctx storage, report recoveryReport, confirm func() bool) (recoveryRepairResult, error) {
	if len(report.staleLocks) == 0 && len(report.orphanedSnapshotGoals) == 0 {
		return recoveryRepairResult{applied: []string{}}, nil
	}
	if !confirm() {
		return recoveryRepairResult{applied: []string{}}, nil
	}
	stamp := strings.NewReplacer(":", "-", ".", "-").Replace(nowIso())
	rel := recoveryBackupDir + "/" + stamp
	backupDir := ctx.mustPath(rel)
	if err := ctx.ensureDir(rel, true); err != nil {
		return recoveryRepairResult{}, err
	}
	applied := []string{}
	for _, lock := range report.staleLocks {
		source := filepath.Join(ctx.mustPath(goalLockDir), lock.fileName)
		if err := copyFileBytes(source, filepath.Join(backupDir, "lock-"+safeLockName(lock.fileName))); err != nil {
			continue
		}
		if err := os.Remove(source); err != nil {
			continue
		}
		applied = append(applied, "removed stale lock "+lock.fileName)
	}
	if len(report.orphanedSnapshotGoals) > 0 {
		snapshotPath := ctx.snapshotPath()
		ok := true
		if _, err := os.Stat(snapshotPath); err == nil {
			ok = copyFileBytes(snapshotPath, filepath.Join(backupDir, "pool-snapshot.json")) == nil
		}
		if ok {
			// Invalidate first so the re-read goes cold and rewrites the snapshot.
			invalidateGoalPoolCache()
			ctx.readActiveGoalPool()
			n := len(report.orphanedSnapshotGoals)
			plural := "ies"
			if n == 1 {
				plural = "y"
			}
			applied = append(applied, fmt.Sprintf("refreshed pool snapshot (%d orphaned entr%s dropped)", n, plural))
		}
	}
	return recoveryRepairResult{applied: applied, backupDir: &backupDir, confirmed: true}, nil
}

func formatRecoveryReport(report recoveryReport) string {
	lines := []string{}
	if report.healthy {
		lines = append(lines, "Recovery report: OK \u2014 no issues found.")
	} else {
		lines = append(lines, "Recovery report: issues found.")
	}
	if n := len(report.malformedGoalFiles); n > 0 {
		lines = append(lines, fmt.Sprintf("  - %d malformed goal file(s):", n))
		for _, f := range report.malformedGoalFiles {
			lines = append(lines, "      "+f.relPath+" \u2014 "+f.err)
		}
	}
	if report.malformedLedgerLines > 0 {
		lines = append(lines, fmt.Sprintf("  - %d malformed ledger line(s) (read-only; manual review advised)", report.malformedLedgerLines))
	}
	if n := len(report.staleLocks); n > 0 {
		lines = append(lines, fmt.Sprintf("  - %d stale lock(s):", n))
		for _, l := range report.staleLocks {
			lines = append(lines, fmt.Sprintf("      %s (pid %s, %ss old)", l.fileName, jsNumber(l.pid), jsNumber(jsRound(l.ageMs/1000))))
		}
	}
	if n := len(report.orphanedSnapshotGoals); n > 0 {
		plural := "ies"
		if n == 1 {
			plural = "y"
		}
		lines = append(lines, fmt.Sprintf("  - %d orphaned snapshot entr%s:", n, plural))
		for _, o := range report.orphanedSnapshotGoals {
			lines = append(lines, "      "+o.goalID+" ("+o.activePath+")")
		}
	}
	if len(report.staleLocks) > 0 || len(report.orphanedSnapshotGoals) > 0 {
		lines = append(lines, "Run `/goal-recovery repair` to remove stale locks and refresh the pool snapshot (confirmation + backup required).")
	}
	return strings.Join(lines, "\n")
}
