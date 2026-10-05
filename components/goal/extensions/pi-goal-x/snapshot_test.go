package pi_goal_x

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of tests/goal-pool-snapshot.test.ts. The snapshot file is a cache the replay test does not compare, so these are the
// checks of where it lives, the directory time it records and the legacy file it replaces.

const snapshotName = ".goals-pool-snapshot.json"

func snapshotPaths(cwd string) (newPath, legacyPath, goalsDir string) {
	goalsDir = filepath.Join(cwd, ".pi", "goals")
	return filepath.Join(cwd, ".pi", snapshotName), filepath.Join(goalsDir, snapshotName), goalsDir
}

func writeSnapshotGoal(t *testing.T, cwd, id string) {
	t.Helper()
	g := createGoal("objective "+id, true, false, false, ms(2026, 9, 5, 9, 0, 0))
	if _, err := (storage{cwd: cwd}).writeActiveGoalFile(g); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func readSnapshotJSON(t *testing.T, file string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func dirMtime(t *testing.T, dir string) float64 {
	t.Helper()
	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	return mtimeMs(fi)
}

func TestGoalPoolSnapshot(t *testing.T) {
	tw(t, "goal-pool-snapshot", "pool snapshot lives outside the watched goals dir and records its mtime", func(t *testing.T) {
		cwd := t.TempDir()
		writeSnapshotGoal(t, cwd, "g1")
		invalidateGoalPoolCache()
		pool := (storage{cwd: cwd}).readActiveGoalPool()
		eq(t, pool.size(), 1, "pool size")
		newPath, legacyPath, goalsDir := snapshotPaths(cwd)
		eq(t, exists(newPath), true, "snapshot written outside the goals dir")
		eq(t, exists(legacyPath), false, "no legacy in-dir snapshot")
		snap := readSnapshotJSON(t, newPath)
		eq(t, snap["dirMtimeMs"], dirMtime(t, goalsDir), "dir mtime key matches after the snapshot write")
		eq(t, snap["version"], 1.0, "version")
		eq(t, len(snap["goals"].([]any)), 1, "goals")
	})
	tw(t, "goal-pool-snapshot", "pool snapshot: a subsequent goal write keeps the fast-path key valid", func(t *testing.T) {
		cwd := t.TempDir()
		writeSnapshotGoal(t, cwd, "g1")
		invalidateGoalPoolCache()
		(storage{cwd: cwd}).readActiveGoalPool()
		writeSnapshotGoal(t, cwd, "g2")
		newPath, _, goalsDir := snapshotPaths(cwd)
		snap := readSnapshotJSON(t, newPath)
		eq(t, snap["dirMtimeMs"], dirMtime(t, goalsDir), "delta update keeps the mtime key fresh")
		eq(t, len(snap["goals"].([]any)), 2, "goals")
		invalidateGoalPoolCache()
		eq(t, (storage{cwd: cwd}).readActiveGoalPool().size(), 2, "served from the snapshot")
	})
	tw(t, "goal-pool-snapshot", "pool snapshot: legacy in-dir snapshot is served as a one-time fallback", func(t *testing.T) {
		cwd := t.TempDir()
		writeSnapshotGoal(t, cwd, "g1")
		_, legacyPath, goalsDir := snapshotPaths(cwd)
		entries, _ := os.ReadDir(goalsDir)
		var diskFile string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "active_goal_") {
				diskFile = e.Name()
				break
			}
		}
		marker := parseGoalFile(filepath.Join(goalsDir, diskFile)).clone()
		marker.set("snapshotMarker", true)
		legacy := newObject()
		legacy.set("version", 1.0)
		legacy.set("dirMtimeMs", dirMtime(t, goalsDir))
		legacy.set("goals", []any{marker})
		if err := os.WriteFile(legacyPath, []byte(marshalJSON(legacy, "")), 0o644); err != nil {
			t.Fatal(err)
		}
		invalidateGoalPoolCache()
		pool := (storage{cwd: cwd}).readActiveGoalPool()
		eq(t, pool.size(), 1, "legacy snapshot hydrates the pool")
		eq(t, gflag(pool.values()[0], "snapshotMarker"), true, "served from the legacy snapshot, not a rescan")
	})
	tw(t, "goal-pool-snapshot", "pool snapshot: writing removes the legacy in-dir file", func(t *testing.T) {
		cwd := t.TempDir()
		writeSnapshotGoal(t, cwd, "g1")
		newPath, legacyPath, _ := snapshotPaths(cwd)
		if err := os.WriteFile(legacyPath, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		invalidateGoalPoolCache()
		(storage{cwd: cwd}).readActiveGoalPool()
		eq(t, exists(legacyPath), false, "legacy file cleaned up after the new write")
		eq(t, exists(newPath), true, "new snapshot")
	})
}
