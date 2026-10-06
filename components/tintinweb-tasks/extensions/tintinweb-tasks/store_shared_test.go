package tintinweb_tasks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// deadPID is the pid of a process that has already exited (the original spawns `node -e ""`).
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestTaskStoreSharedFileAccess(t *testing.T) {
	const f = "task-store-concurrency"
	setup := func(t *testing.T) (dir, file string) {
		dir = scratch(t)
		return dir, filepath.Join(dir, "tasks.json")
	}
	mustNotPanic := func(t *testing.T, fn func()) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("threw: %v", r)
			}
		}()
		fn()
	}
	tw(t, f, "assigns distinct IDs when two sessions create tasks in turn", func(t *testing.T) {
		_, file := setup(t)
		a, b := newTaskStore(file), newTaskStore(file)
		first, second := a.create("From A", "d", "", nil), b.create("From B", "d", "", nil)
		eq(t, first.ID, "1")
		eq(t, second.ID, "2")
		eq(t, subjects(newTaskStore(file).list(nil)), []string{"From A", "From B"})
	})
	tw(t, f, "does not lose the other session's writes when both mutate the same task", func(t *testing.T) {
		_, file := setup(t)
		a, b := newTaskStore(file), newTaskStore(file)
		a.create("Original", "d", "", nil)
		a.update("1", updateFields{Status: sp("in_progress")})
		b.update("1", updateFields{Subject: sp("Renamed by B")})
		tk := newTaskStore(file).list(nil)[0]
		eq(t, tk.Status, "in_progress")
		eq(t, tk.Subject, "Renamed by B")
	})
	tw(t, f, "sees another session's new tasks without being reconstructed", func(t *testing.T) {
		_, file := setup(t)
		a, b := newTaskStore(file), newTaskStore(file)
		a.create("From A", "d", "", nil)
		eq(t, subjects(b.list(nil)), []string{"From A"})
		eq(t, b.get("1").Subject, "From A")
	})
	tw(t, f, "sees another session's deletions", func(t *testing.T) {
		_, file := setup(t)
		a, b := newTaskStore(file), newTaskStore(file)
		a.create("Doomed", "d", "", nil)
		a.delete("1")
		eq(t, len(b.list(nil)), 0)
		if b.get("1") != nil {
			t.Fatal("deleted task still visible")
		}
	})
	tw(t, f, "reclaims a lock left behind by a dead process", func(t *testing.T) {
		// A crashed session leaves its lock file on disk. Without stale-lock detection every later
		// mutation would block for the full retry budget and then throw.
		_, file := setup(t)
		pid := deadPID(t)
		if pid <= 0 {
			t.Fatal("no pid")
		}
		os.WriteFile(file+".lock", []byte(strconv.Itoa(pid)), 0o644)
		s := newTaskStore(file)
		mustNotPanic(t, func() { s.create("After crash", "d", "", nil) })
		eq(t, subjects(newTaskStore(file).list(nil)), []string{"After crash"})
	})
	tw(t, f, "reclaims a lock file that never got a PID written to it", func(t *testing.T) {
		// acquireLock creates the lock file and then writes its PID. A crash in between leaves an empty
		// lock naming nobody, which used to be unrecoverable.
		_, file := setup(t)
		os.WriteFile(file+".lock", nil, 0o644)
		s := newTaskStore(file)
		mustNotPanic(t, func() { s.create("After crash", "d", "", nil) })
		eq(t, subjects(newTaskStore(file).list(nil)), []string{"After crash"})
	})
	tw(t, f, "reclaims a lock file holding garbage", func(t *testing.T) {
		_, file := setup(t)
		os.WriteFile(file+".lock", []byte("not-a-pid"), 0o644)
		s := newTaskStore(file)
		mustNotPanic(t, func() { s.create("After garbage lock", "d", "", nil) })
	})
	tw(t, f, "still reads the PID out of a lock written in the pid:token format", func(t *testing.T) {
		// The lock token carries a unique suffix so a holder can recognise its own lock. Staleness detection
		// reads the PID off the front of that token.
		_, file := setup(t)
		os.WriteFile(file+".lock", []byte(strconv.Itoa(deadPID(t))+":11111111-2222-3333-4444-555555555555"), 0o644)
		s := newTaskStore(file)
		started := time.Now()
		mustNotPanic(t, func() { s.create("After crash", "d", "", nil) })
		// Reclaimed on the first poll, not after the full retry budget (5s) expired.
		if time.Since(started) >= time.Second {
			t.Fatalf("reclaim took %v", time.Since(started))
		}
	})
	tw(t, f, "does not delete a lock that a successor now holds", func(t *testing.T) {
		// A lock can be reclaimed out from under a live holder (another PID namespace reads our PID as dead).
		// Releasing must then be a no-op instead of deleting the successor's lock.
		_, file := setup(t)
		s := newTaskStore(file)
		successor := strconv.Itoa(os.Getpid()) + ":00000000-0000-0000-0000-000000000000"
		// The rename is the last thing save() does, so this fires inside the critical section.
		setRenameHook(func() { os.WriteFile(file+".lock", []byte(successor), 0o644) })
		defer setRenameHook(nil)
		s.create("Task", "d", "", nil)
		setRenameHook(nil)
		data, _ := os.ReadFile(file + ".lock")
		eq(t, string(data), successor)
	})
	tw(t, f, "removes its own lock even after reclaiming a stale one", func(t *testing.T) {
		_, file := setup(t)
		os.WriteFile(file+".lock", []byte(strconv.Itoa(deadPID(t))), 0o644)
		newTaskStore(file).create("Task", "d", "", nil)
		eq(t, fileExists(file+".lock"), false)
	})
	tw(t, f, "leaves no lock or temp file behind after a mutation", func(t *testing.T) {
		dir, file := setup(t)
		s := newTaskStore(file)
		s.create("Task", "d", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		s.clearCompleted()
		eq(t, fileExists(file+".lock"), false)
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Fatalf("temp file left behind: %s", e.Name())
			}
		}
	})
	tw(t, f, "snapshots the latest state written by another session", func(t *testing.T) {
		_, file := setup(t)
		a, b := newTaskStore(file), newTaskStore(file)
		a.create("Written by A", "d", "", nil)
		snap := b.snapshot()
		eq(t, len(snap.Tasks), 1)
		eq(t, snap.Tasks[0].Subject, "Written by A")
	})
	tw(t, f, "seeds an empty store and carries the ID counter over", func(t *testing.T) {
		dir, file := setup(t)
		parent := newTaskStore(file)
		parent.create("One", "d", "", nil)
		parent.create("Two", "d", "", nil)
		snapshot := parent.snapshot()
		child := newTaskStore(filepath.Join(dir, "child.json"))
		child.seed(snapshot)
		eq(t, subjects(child.list(nil)), []string{"One", "Two"})
		// Continues from the parent's counter rather than colliding on "1".
		eq(t, child.create("Three", "d", "", nil).ID, "3")
	})
	tw(t, f, "is a no-op on a store that already has tasks, so re-seeding never duplicates", func(t *testing.T) {
		dir, file := setup(t)
		parent := newTaskStore(file)
		parent.create("One", "d", "", nil)
		snapshot := parent.snapshot()
		childFile := filepath.Join(dir, "child.json")
		newTaskStore(childFile).seed(snapshot)
		// Re-pointing at the already-seeded file and seeding again must change nothing.
		reopened := newTaskStore(childFile)
		reopened.seed(snapshot)
		eq(t, subjects(reopened.list(nil)), []string{"One"})
	})
	tw(t, f, "does not write the parent's file when the seeded copy is mutated", func(t *testing.T) {
		dir, file := setup(t)
		parent := newTaskStore(file)
		parent.create("Shared", "d", "", nil)
		child := newTaskStore(filepath.Join(dir, "child.json"))
		child.seed(parent.snapshot())
		child.create("Child only", "d", "", nil)
		eq(t, subjects(newTaskStore(file).list(nil)), []string{"Shared"})
	})
}
