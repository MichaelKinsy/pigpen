package tintinweb_tasks

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Cases of this port that the original's tests do not carry: each pins a branch that a mutation of the port
// showed to be unchecked (port/mutations.json), or a behavior of the Go implementation (lock waits, seeding).

func TestAutoClearExtra(t *testing.T) {
	listComplete := func() (*taskStore, *autoClearManager) {
		s := newTaskStore("")
		return s, newAutoClearManager(func() *taskStore { return s }, fixedMode("on_list_complete"), 4)
	}
	t.Run("a second completion does not restart the batch countdown", func(t *testing.T) {
		s, m := listComplete()
		s.create("A", "d", "", nil)
		s.create("B", "d", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		s.update("2", updateFields{Status: sp("completed")})
		m.trackCompletion("2", 2) // the list is complete: the countdown starts here
		m.trackCompletion("2", 4) // tracked again: it must not start over
		eq(t, m.onTurnStart(6), true)
		eq(t, len(s.list(nil)), 0)
	})
	t.Run("a task reverted to pending loses its countdown", func(t *testing.T) {
		s := newTaskStore("")
		m := newAutoClearManager(func() *taskStore { return s }, fixedMode("on_task_complete"), 4)
		s.create("Task", "d", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		s.update("1", updateFields{Status: sp("pending")})
		m.onTurnStart(2) // the stale entry is dropped here
		s.update("1", updateFields{Status: sp("completed")})
		m.onTurnStart(6) // completed again without being tracked: the old countdown must not clear it
		eq(t, s.get("1") != nil, true)
	})
}

func TestStoreExtra(t *testing.T) {
	t.Run("clearCompleted removes the edges that other tasks hold to the cleared ones", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Open", "d", "", nil)
		s.create("Done", "d", "", nil)
		s.update("2", updateFields{AddBlockedBy: []string{"1"}}) // #1 blocks #2
		s.update("2", updateFields{Status: sp("completed")})
		s.clearCompleted()
		eq(t, s.get("1").Blocks, []string{})
	})
	t.Run("a lock held by a live process is waited for, not reclaimed", func(t *testing.T) {
		file := filepath.Join(scratch(t), "tasks.json")
		lock := file + ".lock"
		os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())+":someone-else"), 0o644)
		time.AfterFunc(250*time.Millisecond, func() { os.Remove(lock) })
		started := time.Now()
		newTaskStore(file).create("Waited", "d", "", nil)
		if time.Since(started) < 200*time.Millisecond {
			t.Fatalf("the live holder's lock was taken after %v", time.Since(started))
		}
	})
	t.Run("seeding a store that has tasks keeps its own", func(t *testing.T) {
		dir := scratch(t)
		other := newTaskStore(filepath.Join(dir, "a.json"))
		other.create("Theirs", "d", "", nil)
		mine := newTaskStore(filepath.Join(dir, "b.json"))
		mine.create("Mine", "d", "", nil)
		mine.seed(other.snapshot())
		eq(t, subjects(mine.list(nil)), []string{"Mine"})
	})
	t.Run("a task saved by one store keeps the optional fields it was given", func(t *testing.T) {
		file := filepath.Join(scratch(t), "tasks.json")
		s := newTaskStore(file)
		s.create("Task", "d", "Doing", obj{"k": "v"})
		s.update("1", updateFields{Owner: sp("me")})
		got := newTaskStore(file).get("1")
		eq(t, got.ActiveForm, "Doing")
		eq(t, got.Owner, "me")
		eq(t, got.Metadata, obj{"k": "v"})
	})
}

func TestWidgetTimerStops(t *testing.T) {
	e := newWidgetEnv(t, tasksConfig{})
	e.create("Job", "Working", nil)
	e.setStatus("1", "in_progress")
	e.widget.setActiveTask("1", true)
	eq(t, e.timers.fn != nil, true) // a spinner is showing: the timer runs
	e.setStatus("1", "completed")
	e.widget.update()
	eq(t, e.timers.fn == nil, true) // nothing animates: the timer is stopped
}
