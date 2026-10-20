package pi_goal_x

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storage/goal-root.ts has no test file of its own in the original (the external-root behavior is exercised through the
// extension, which is not ported yet); these are unit checks of the port, not twins.

type fixedSession struct{ id string }

func (s fixedSession) getSessionId() (string, bool) { return s.id, s.id != "" }

func TestGoalRootDefaultAndExternal(t *testing.T) {
	isolateHome(t)
	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	plain := &goalStorageContext{cwd: cwd}
	root, err := goalStorageRoot(plain)
	if err != nil || root != filepath.Join(cwd, ".pi", "goals") {
		t.Fatalf("default root %q %v", root, err)
	}
	if ext, _ := isExternalGoalStorage(plain); ext {
		t.Error("the default pool is not external")
	}
	snap, _ := goalPoolSnapshotPath(plain)
	eq(t, snap, filepath.Join(cwd, ".pi", ".goals-pool-snapshot.json"), "default snapshot path")

	external, _ := filepath.EvalSymlinks(t.TempDir())
	settingsWrite(t, goalSettingsPath(cwd), `{"goalsRoot":"`+external+`"}`)
	invalidateGoalSettingsCache()
	ctx := &goalStorageContext{cwd: cwd, sessionManager: fixedSession{"s1"}}
	root, err = goalStorageRoot(ctx)
	if err != nil || root != external {
		t.Fatalf("external root %q %v", root, err)
	}
	if _, err := os.Stat(filepath.Join(external, ".metadata")); err != nil {
		t.Error("metadata directory created")
	}
	if ext, _ := isExternalGoalStorage(ctx); !ext {
		t.Error("external")
	}
	snap, _ = goalPoolSnapshotPath(ctx)
	eq(t, snap, filepath.Join(external, ".metadata", ".goals-pool-snapshot.json"), "external snapshot path")
	p, err := goalStoragePath(ctx, `.pi\goals\a\b.md`)
	eq(t, p, filepath.Join(external, "a", "b.md"), "logical path")
	if _, err := goalStoragePath(ctx, ".pi/goals/../x"); err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Errorf("escape: %v", err)
	}
	if _, err := goalStoragePath(ctx, "other/x"); err == nil || !strings.Contains(err.Error(), "Invalid goal storage path") {
		t.Errorf("invalid: %v", err)
	}
	if err := ensureGoalStorageDirectory(ctx, ".pi/goals/sub/dir", true); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(external, "sub", "dir")); err != nil {
		t.Error("directory created")
	}
	pinned, _ := goalStorageContextOf(ctx)
	eq(t, *pinned.goalStorageRoot, external, "snapshot context")
}

func TestGoalRootRejectsRelativeAndInvalid(t *testing.T) {
	isolateHome(t)
	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	settingsWrite(t, goalSettingsPath(cwd), `{"goalsRoot":"relative/dir"}`)
	invalidateGoalSettingsCache()
	if _, err := goalStorageRoot(&goalStorageContext{cwd: cwd, sessionManager: fixedSession{"s"}}); err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Errorf("relative: %v", err)
	}
	settingsWrite(t, goalSettingsPath(cwd), `{"goalsRoot":5}`)
	invalidateGoalSettingsCache()
	if _, err := goalStorageRoot(&goalStorageContext{cwd: cwd, sessionManager: fixedSession{"s"}}); err == nil || !strings.Contains(err.Error(), "Invalid goalsRoot: goalsRoot must be a non-empty string") {
		t.Errorf("invalid: %v", err)
	}
}
