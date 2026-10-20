package pi_goal_x

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const gsrF = "goal-storage-root"

// gsrFixture is fixture() of tests/goal-storage-root.test.ts: a pool root outside the projects and contexts (a project plus a session)
// that configure it.
type gsrFixture struct {
	t    *testing.T
	dir  string
	root string
}

func newGsrFixture(t *testing.T) *gsrFixture {
	t.Helper()
	isolateHome(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	t.Cleanup(func() { invalidateGoalSettingsCache(); invalidateGoalPoolCache() })
	return &gsrFixture{t: t, dir: dir, root: filepath.Join(dir, "pool")}
}

// context is f.context(name, configured): a project whose settings configure goalsRoot (the JSON text; "" for no setting) and a session.
func (f *gsrFixture) context(name, goalsRootJSON string) *goalStorageContext {
	f.t.Helper()
	cwd := filepath.Join(f.dir, name)
	settings := `{}`
	if goalsRootJSON != "" {
		settings = `{"goalsRoot": ` + goalsRootJSON + `}`
	}
	settingsWrite(f.t, filepath.Join(cwd, ".pi", "pi-goal-x-settings.json"), settings)
	invalidateGoalSettingsCache()
	return &goalStorageContext{cwd: cwd, sessionManager: &fixedSession{name}}
}

func (f *gsrFixture) rootJSON() string { b, _ := json.Marshal(f.root); return string(b) }

func gsrStorage(t *testing.T, ctx *goalStorageContext) storage {
	t.Helper()
	st, err := newGoalStorage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func gsrErr(t *testing.T, err error, re string, what string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: no error, want /%s/", what, re)
		return
	}
	if !regexp.MustCompile(re).MatchString(err.Error()) {
		t.Errorf("%s: error %q does not match /%s/", what, err, re)
	}
}

// TestGoalStorageRootLockWorker is the second process of the shared-pool twin (the original's `node -e` worker): it tries to take the
// goal's lock in the pool the parent passes and exits 2 if it got it.
func TestGoalStorageRootLockWorker(t *testing.T) {
	arg := os.Getenv("GOAL_LOCK_WORKER")
	if arg == "" {
		t.Skip("helper process of the goal-storage-root twin")
	}
	var in struct{ Cwd, Root, Goal string }
	if err := json.Unmarshal([]byte(arg), &in); err != nil {
		t.Fatal(err)
	}
	st := storage{cwd: in.Cwd, root0: in.Root}
	lock, err := st.acquireLock(in.Goal, 1, 0)
	if err == nil {
		lock.release()
		os.Exit(2)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "lock") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGoalStorageRootTwins(t *testing.T) {
	tw(t, gsrF, "shared pool keeps goals, ledger, locks, archives and snapshots together", func(t *testing.T) {
		f := newGsrFixture(t)
		a, b := f.context("a", f.rootJSON()), f.context("b", f.rootJSON())
		sa, sb := gsrStorage(t, a), gsrStorage(t, b)
		goal, err := sa.writeActiveGoalFile(createGoal("Shared work", true, false, false, clockMs()))
		if err != nil {
			t.Fatal(err)
		}
		id := gstr(goal, "id")
		if g := sb.readActiveGoalPool().get(id); g == nil || gstr(g, "objective") != "Shared work" {
			t.Errorf("the second project does not see the goal: %v", g)
		}
		ev := ledgerEvent("goal_created", "goalId", id, "objective", gstr(goal, "objective"), "autoContinue", true, "sisyphus", false, "at", gstr(goal, "createdAt"))
		eq(t, appendGoalEvent(sa, ev).ok, true, "ledger append")
		if events := readGoalLedger(sb).events; len(events) == 0 || gstr(events[0], "goalId") != id {
			t.Errorf("the second project does not read the ledger: %v", events)
		}
		lock, err := sa.acquireLock(id, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer lock.release()
			_, err := sb.acquireLock(id, 1, 0)
			gsrErr(t, err, `(?i)lock`, "the same goal's lock is shared")
			arg, _ := json.Marshal(map[string]string{"Cwd": b.cwd, "Root": sb.root(), "Goal": id})
			cmd := exec.Command(os.Args[0], "-test.run=^TestGoalStorageRootLockWorker$")
			cmd.Env = append(os.Environ(), "GOAL_LOCK_WORKER="+string(arg))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("another process took the lock held in the shared pool: %v\n%s", err, out)
			}
		}()
		snap, err := goalPoolSnapshotPath(a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(snap); err != nil {
			t.Errorf("the snapshot is not in the shared pool: %v", err)
		}
		if !strings.HasPrefix(snap, f.root+string(filepath.Separator)) {
			t.Errorf("snapshot %s is outside the pool %s", snap, f.root)
		}
		eq(t, len(runRecoveryReport(sb).malformedGoalFiles), 0, "malformed goal files")
		done := cloneGoal(goal)
		done.set("status", "complete")
		if _, err := sb.archiveGoalFile(done); err != nil {
			t.Fatal(err)
		}
		eq(t, sa.readActiveGoalPool().size(), 0, "the archived goal left the pool")
		archived, _ := os.ReadDir(filepath.Join(f.root, "archived"))
		eq(t, len(archived), 1, "archived files in the shared pool")
		for _, ctx := range []*goalStorageContext{a, b} {
			if _, err := os.Stat(filepath.Join(ctx.cwd, ".pi", "goals")); err == nil {
				t.Errorf("%s has its own .pi/goals", ctx.cwd)
			}
		}
	})

	tw(t, gsrF, "root is pinned per session and separate sessions can select different pools", func(t *testing.T) {
		f := newGsrFixture(t)
		a := f.context("a", f.rootJSON())
		first, err := goalStorageRoot(a)
		if err != nil {
			t.Fatal(err)
		}
		plain, _ := goalStorageContextOf(a)
		next := filepath.Join(f.dir, "new-pool")
		nextJSON, _ := json.Marshal(next)
		f.context("a", string(nextJSON)) // the settings change under the running session
		got, _ := goalStorageRoot(a)
		eq(t, got, f.root, "the session's root stays")
		got, _ = goalStorageRoot(plain)
		eq(t, got, f.root, "the plain context keeps it")
		fresh, err := goalStorageRoot(&goalStorageContext{cwd: a.cwd, sessionManager: &fixedSession{"new"}})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, fresh, next, "a new session selects the new pool")
		eq(t, first, f.root, "first")
		if err := os.RemoveAll(f.root); err != nil {
			t.Fatal(err)
		}
		_, err = gsrStorage(t, a).writeActiveGoalFile(createGoal("Must not recreate", true, false, false, clockMs()))
		gsrErr(t, err, `(?i)no such file|ENOENT|not exist`, "writing into a removed pool")
		if _, err := os.Stat(f.root); err == nil {
			t.Error("the pool was recreated")
		}
	})

	tw(t, gsrF, "invalid roots and symlinked managed directories fail instead of falling back", func(t *testing.T) {
		f := newGsrFixture(t)
		for _, invalid := range []string{`"relative/path"`, `""`, `12`} {
			_, err := goalStorageRoot(f.context("invalid-"+jsNumber(float64(len(invalid))), invalid))
			gsrErr(t, err, `goalsRoot|PI_GOAL_ROOT`, "invalid root "+invalid)
		}
		a := f.context("a", f.rootJSON())
		if _, err := goalStorageRoot(a); err != nil {
			t.Fatal(err)
		}
		_, err := goalStoragePath(a, ".pi/goals/../../escape")
		gsrErr(t, err, `escapes`, "path escape")
		outside := filepath.Join(f.dir, "outside")
		if err := os.Mkdir(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(f.root, "archived")); err != nil {
			t.Fatal(err)
		}
		_, err = gsrStorage(t, a).archiveGoalFile(createGoal("No escape", true, false, false, clockMs()))
		gsrErr(t, err, `symlink`, "archiving through a symlinked directory")
		entries, _ := os.ReadDir(outside)
		eq(t, len(entries), 0, "nothing was written outside the pool")
		if err := os.Symlink(outside, filepath.Join(f.dir, "linked-root")); err != nil {
			t.Fatal(err)
		}
		linkedJSON, _ := json.Marshal(filepath.Join(f.dir, "linked-root"))
		_, err = goalStorageRoot(f.context("linked", string(linkedJSON)))
		gsrErr(t, err, `symlink`, "a symlinked root")
	})

	tw(t, gsrF, "default paths stay compatible; root configuration honors environment precedence", func(t *testing.T) {
		f := newGsrFixture(t)
		a := f.context("default", "null")
		settingsWrite(t, filepath.Join(a.cwd, ".pi", "pi-goal-x-settings.json"), `{}`)
		invalidateGoalSettingsCache()
		got, _ := goalStorageRoot(a)
		eq(t, got, filepath.Join(a.cwd, ".pi", "goals"), "default root")
		snap, _ := goalPoolSnapshotPath(a)
		eq(t, snap, filepath.Join(a.cwd, ".pi", ".goals-pool-snapshot.json"), "default snapshot")
		b := f.context("configured", f.rootJSON())
		if r := loadGoalSettings(b.cwd, map[string]string{"PI_GOAL_ROOT": "/env-pool"}).goalsRoot; r == nil || *r != "/env-pool" {
			t.Errorf("the environment wins: %v", r)
		}
		if r := loadGoalSettings(b.cwd, map[string]string{}).goalsRoot; r == nil || *r != f.root {
			t.Errorf("the project setting: %v", r)
		}
	})

	tw(t, gsrF, "shared worktrees reject stale revisions and refresh the winning write", func(t *testing.T) {
		f := newGsrFixture(t)
		a, b := f.context("writer-a", f.rootJSON()), f.context("writer-b", f.rootJSON())
		goal, err := gsrStorage(t, a).writeActiveGoalFile(createGoal("Shared initial", true, false, false, clockMs()))
		if err != nil {
			t.Fatal(err)
		}
		id := gstr(goal, "id")
		coreFor := func(ctx *goalStorageContext) *core {
			h := newKernelHost(ctx.cwd, ctx.sessionManager.(*fixedSession).id)
			h.noUI = true
			entry := focusEntryFor(id)
			entry.obj("data").set("storageRoot", f.root)
			h.branch = []any{entry}
			c := newCore(h)
			if err := c.loadState(); err != nil {
				t.Fatal(err)
			}
			return c
		}
		first, second := coreFor(a), coreFor(b)
		eq(t, gstr(first.state(), "id"), id, "first focuses the goal")
		eq(t, gstr(second.state(), "id"), id, "second focuses the goal")
		mutate := func(objective string) goalMutationSpec {
			return goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject { return goalWith(g, "objective", objective) }}
		}
		won, err := first.goalService.apply(first.st, mutate("Winning edit"))
		if err != nil || !won.ok {
			t.Fatalf("the winning edit: %v %v", err, won.message)
		}
		stale, err := second.goalService.apply(second.st, mutate("Stale overwrite"))
		if err != nil {
			t.Fatal(err)
		}
		eq(t, stale.ok, false, "the stale edit is rejected")
		gsrErr(t, mustErr(stale.message), `(?i)revision`, "stale message")
		invalidateGoalPoolCache()
		if err := second.loadState(); err != nil {
			t.Fatal(err)
		}
		eq(t, gstr(second.state(), "objective"), "Winning edit", "the reload shows the winning write")
		fresh, err := second.goalService.apply(second.st, mutate("Fresh edit"))
		if err != nil || !fresh.ok {
			t.Fatalf("the fresh edit: %v %v", err, fresh.message)
		}
		pool, err := gsrStorage(t, a).readActiveGoalPoolChecked()
		if err != nil {
			t.Fatal(err)
		}
		eq(t, gstr(pool.get(id), "objective"), "Fresh edit", "the pool has the last write")
		if err := os.RemoveAll(f.root); err != nil {
			t.Fatal(err)
		}
		invalidateGoalPoolCache()
		_, err = gsrStorage(t, a).readActiveGoalPoolChecked()
		gsrErr(t, err, `(?i)no such file|ENOENT|not exist`, "reading a removed pool")
		if _, err := os.Stat(f.root); err == nil {
			t.Error("refresh recreated a missing pinned pool")
		}
	})
}

func mustErr(msg string) error { return &gsrMessage{msg} }

type gsrMessage struct{ s string }

func (m *gsrMessage) Error() string { return m.s }
