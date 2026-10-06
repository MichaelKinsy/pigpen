package tintinweb_tasks

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func sp(s string) *string { return &s }

// fakeClock replaces the store's clock with one that advances only when told to (vi.useFakeTimers).
func fakeClock(t *testing.T, start int64) func(d int64) {
	t.Helper()
	var now atomic.Int64
	now.Store(start)
	t.Cleanup(setClock(now.Load))
	return func(d int64) { now.Add(d) }
}

func subjects(tasks []*task) []string {
	out := []string{}
	for _, t := range tasks {
		out = append(out, t.Subject)
	}
	return out
}

func taskIDs(tasks []*task) []string {
	out := []string{}
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func TestTaskStoreInMemory(t *testing.T) {
	const f = "task-store"
	tw(t, f, "creates tasks with auto-incrementing IDs", func(t *testing.T) {
		s := newTaskStore("")
		t1, t2 := s.create("First task", "Description 1", "", nil), s.create("Second task", "Description 2", "", nil)
		eq(t, t1.ID, "1")
		eq(t, t2.ID, "2")
		eq(t, t1.Status, "pending")
		eq(t, t1.Subject, "First task")
		eq(t, t1.Description, "Description 1")
	})
	tw(t, f, "creates tasks with optional fields", func(t *testing.T) {
		s := newTaskStore("")
		tk := s.create("Task", "Desc", "Running task", obj{"key": "value"})
		eq(t, tk.ActiveForm, "Running task")
		eq(t, tk.Metadata, obj{"key": "value"})
	})
	tw(t, f, "gets a task by ID", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Test", "Desc", "", nil)
		tk := s.get("1")
		if tk == nil {
			t.Fatal("task 1 missing")
		}
		eq(t, tk.Subject, "Test")
	})
	tw(t, f, "returns undefined for non-existent task", func(t *testing.T) {
		if newTaskStore("").get("999") != nil {
			t.Fatal("expected no task")
		}
	})
	tw(t, f, "lists all tasks sorted by ID", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Task 3", "Desc", "", nil)
		s.create("Task 1", "Desc", "", nil)
		s.create("Task 2", "Desc", "", nil)
		eq(t, taskIDs(s.list(nil)), []string{"1", "2", "3"})
	})
	tw(t, f, "lists tasks sorted by status when sortOrder is 'status'", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Pending", "Desc", "", nil)     // #1
		s.create("Completed", "Desc", "", nil)   // #2
		s.create("In progress", "Desc", "", nil) // #3
		s.update("2", updateFields{Status: sp("completed")})
		s.update("3", updateFields{Status: sp("in_progress")})
		eq(t, subjects(s.list("status")), []string{"Completed", "In progress", "Pending"})
	})
	tw(t, f, "lists tasks sorted by most recently updated when sortOrder is 'recent'", func(t *testing.T) {
		advance := fakeClock(t, 1000)
		s := newTaskStore("")
		s.create("First", "Desc", "", nil) // #1 created at 1000
		advance(100)
		s.create("Second", "Desc", "", nil) // #2 created at 1100
		advance(100)
		s.create("Third", "Desc", "", nil) // #3 created at 1200
		advance(100)
		s.update("1", updateFields{Subject: sp("First updated")}) // #1 updated at 1300
		advance(100)
		s.update("3", updateFields{Subject: sp("Third updated")}) // #3 updated at 1400
		// Most recently updated first: #3 (1400), #1 (1300), #2 (1100)
		eq(t, taskIDs(s.list("recent")), []string{"3", "1", "2"})
	})
	tw(t, f, "lists tasks sorted by least recently updated when sortOrder is 'oldest'", func(t *testing.T) {
		advance := fakeClock(t, 1000)
		s := newTaskStore("")
		s.create("First", "Desc", "", nil)
		advance(100)
		s.create("Second", "Desc", "", nil)
		advance(100)
		s.create("Third", "Desc", "", nil)
		advance(100)
		s.update("1", updateFields{Subject: sp("First updated")})
		advance(100)
		s.update("3", updateFields{Subject: sp("Third updated")})
		// Least recently updated first: #2 (1100), #1 (1300), #3 (1400)
		eq(t, taskIDs(s.list("oldest")), []string{"2", "1", "3"})
	})
	tw(t, f, "updates task status", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Test", "Desc", "", nil)
		r := s.update("1", updateFields{Status: sp("in_progress")})
		eq(t, r.Task.Status, "in_progress")
		eq(t, r.ChangedFields, []string{"status"})
	})
	tw(t, f, "updates multiple fields at once", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Test", "Desc", "", nil)
		r := s.update("1", updateFields{Subject: sp("Updated subject"), Description: sp("Updated desc"), Owner: sp("agent-1")})
		for _, want := range []string{"subject", "description", "owner"} {
			if !slices.Contains(r.ChangedFields, want) {
				t.Fatalf("changedFields %v lacks %s", r.ChangedFields, want)
			}
		}
		tk := s.get("1")
		eq(t, tk.Subject, "Updated subject")
		eq(t, tk.Owner, "agent-1")
	})
	tw(t, f, "deletes a task with status: deleted", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Test", "Desc", "", nil)
		r := s.update("1", updateFields{Status: sp("deleted")})
		eq(t, r.ChangedFields, []string{"deleted"})
		if s.get("1") != nil {
			t.Fatal("task 1 still stored")
		}
		eq(t, len(s.list(nil)), 0)
	})
	tw(t, f, "preserves ID counter after deletion", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Task 1", "Desc", "", nil)
		s.create("Task 2", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("deleted")})
		eq(t, s.create("Task 3", "Desc", "", nil).ID, "3") // Not "1": the counter continues
	})
	tw(t, f, "merges metadata with null key deletion", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Test", "Desc", "", obj{"a": 1, "b": 2, "c": 3})
		s.update("1", updateFields{Metadata: obj{"b": nil, "d": 4}})
		eq(t, s.get("1").Metadata, obj{"a": 1, "c": 3, "d": 4})
	})
	tw(t, f, "sets up bidirectional blocks via addBlocks", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Blocker", "Desc", "", nil)
		s.create("Blocked", "Desc", "", nil)
		s.update("1", updateFields{AddBlocks: []string{"2"}})
		eq(t, slices.Contains(s.get("1").Blocks, "2"), true)
		eq(t, slices.Contains(s.get("2").BlockedBy, "1"), true)
	})
	tw(t, f, "sets up bidirectional blocks via addBlockedBy", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Blocker", "Desc", "", nil)
		s.create("Blocked", "Desc", "", nil)
		s.update("2", updateFields{AddBlockedBy: []string{"1"}})
		eq(t, slices.Contains(s.get("1").Blocks, "2"), true)
		eq(t, slices.Contains(s.get("2").BlockedBy, "1"), true)
	})
	tw(t, f, "does not duplicate dependency edges", func(t *testing.T) {
		s := newTaskStore("")
		s.create("A", "Desc", "", nil)
		s.create("B", "Desc", "", nil)
		s.update("1", updateFields{AddBlocks: []string{"2"}})
		s.update("1", updateFields{AddBlocks: []string{"2"}}) // duplicate
		eq(t, s.get("1").Blocks, []string{"2"})
	})
	tw(t, f, "cleans up dependency edges on deletion", func(t *testing.T) {
		s := newTaskStore("")
		s.create("A", "Desc", "", nil)
		s.create("B", "Desc", "", nil)
		s.update("1", updateFields{AddBlocks: []string{"2"}})
		s.update("1", updateFields{Status: sp("deleted")})
		eq(t, s.get("2").BlockedBy, []string{})
	})
	tw(t, f, "clears completed tasks", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Completed", "Desc", "", nil)
		s.create("Pending", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		eq(t, s.clearCompleted(), 1)
		eq(t, len(s.list(nil)), 1)
		eq(t, s.list(nil)[0].ID, "2")
	})
	tw(t, f, "returns not found for update on non-existent task", func(t *testing.T) {
		r := newTaskStore("").update("999", updateFields{Status: sp("completed")})
		if r.Task != nil {
			t.Fatal("expected no task")
		}
		eq(t, r.ChangedFields, []string{})
	})
	tw(t, f, "delete method works", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Test", "Desc", "", nil)
		eq(t, s.delete("1"), true)
		eq(t, s.delete("1"), false) // already deleted
		eq(t, len(s.list(nil)), 0)
	})
	tw(t, f, "creates tasks with metadata via TaskCreate", func(t *testing.T) {
		s := newTaskStore("")
		tk := s.create("With meta", "Desc", "", obj{"pr": "123", "reviewer": "alice"})
		eq(t, tk.Metadata, obj{"pr": "123", "reviewer": "alice"})
		eq(t, s.get("1").Metadata, obj{"pr": "123", "reviewer": "alice"})
	})
	tw(t, f, "allows circular dependencies with warning", func(t *testing.T) {
		s := newTaskStore("")
		s.create("A", "Desc", "", nil)
		s.create("B", "Desc", "", nil)
		s.update("1", updateFields{AddBlocks: []string{"2"}})
		r := s.update("2", updateFields{AddBlocks: []string{"1"}})
		eq(t, slices.Contains(s.get("1").Blocks, "2"), true)
		eq(t, slices.Contains(s.get("2").Blocks, "1"), true)
		eq(t, slices.Contains(r.Warnings, "cycle: #2 and #1 block each other"), true)
	})
	tw(t, f, "allows self-dependency with warning", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Self", "Desc", "", nil)
		r := s.update("1", updateFields{AddBlocks: []string{"1"}})
		eq(t, slices.Contains(s.get("1").Blocks, "1"), true)
		eq(t, slices.Contains(r.Warnings, "#1 blocks itself"), true)
	})
	tw(t, f, "stores dangling edge IDs with warning", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Real", "Desc", "", nil)
		r := s.update("1", updateFields{AddBlocks: []string{"9999"}})
		eq(t, slices.Contains(s.get("1").Blocks, "9999"), true)
		eq(t, slices.Contains(r.Warnings, "#9999 does not exist"), true)
	})
	tw(t, f, "returns no warnings for valid dependencies", func(t *testing.T) {
		s := newTaskStore("")
		s.create("A", "Desc", "", nil)
		s.create("B", "Desc", "", nil)
		eq(t, s.update("1", updateFields{AddBlocks: []string{"2"}}).Warnings, []string{})
	})
	tw(t, f, "accepts whitespace-only subjects (matches Claude Code)", func(t *testing.T) {
		eq(t, newTaskStore("").create("   ", "Desc", "", nil).Subject, "   ")
	})
	tw(t, f, "updates activeForm field", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Test", "Desc", "", nil)
		r := s.update("1", updateFields{ActiveForm: sp("Running tests")})
		eq(t, slices.Contains(r.ChangedFields, "activeForm"), true)
		eq(t, s.get("1").ActiveForm, "Running tests")
	})
	tw(t, f, "updates description field", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Test", "Original desc", "", nil)
		r := s.update("1", updateFields{Description: sp("Updated desc")})
		eq(t, slices.Contains(r.ChangedFields, "description"), true)
		eq(t, s.get("1").Description, "Updated desc")
	})
	tw(t, f, "returns empty changedFields when updating non-existent task", func(t *testing.T) {
		r := newTaskStore("").update("999", updateFields{Status: sp("completed")})
		if r.Task != nil {
			t.Fatal("expected no task")
		}
		eq(t, r.ChangedFields, []string{})
		eq(t, r.Warnings, []string{})
	})
	tw(t, f, "clearCompleted cleans up dependency edges", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Blocker", "Desc", "", nil)
		s.create("Blocked", "Desc", "", nil)
		s.update("1", updateFields{AddBlocks: []string{"2"}})
		s.update("1", updateFields{Status: sp("completed")})
		s.clearCompleted()
		eq(t, s.get("2").BlockedBy, []string{})
	})
	tw(t, f, "handles multiple addBlocks in one call", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Blocker", "Desc", "", nil)
		s.create("B1", "Desc", "", nil)
		s.create("B2", "Desc", "", nil)
		s.update("1", updateFields{AddBlocks: []string{"2", "3"}})
		eq(t, s.get("1").Blocks, []string{"2", "3"})
		eq(t, slices.Contains(s.get("2").BlockedBy, "1"), true)
		eq(t, slices.Contains(s.get("3").BlockedBy, "1"), true)
	})
	tw(t, f, "addBlockedBy warns on self-dependency", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Self", "Desc", "", nil)
		r := s.update("1", updateFields{AddBlockedBy: []string{"1"}})
		eq(t, slices.Contains(s.get("1").BlockedBy, "1"), true)
		eq(t, slices.Contains(r.Warnings, "#1 blocks itself"), true)
	})
	tw(t, f, "addBlockedBy warns on dangling ref", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Real", "Desc", "", nil)
		r := s.update("1", updateFields{AddBlockedBy: []string{"9999"}})
		eq(t, slices.Contains(s.get("1").BlockedBy, "9999"), true)
		eq(t, slices.Contains(r.Warnings, "#9999 does not exist"), true)
	})
	tw(t, f, "addBlockedBy warns on cycle", func(t *testing.T) {
		s := newTaskStore("")
		s.create("A", "Desc", "", nil)
		s.create("B", "Desc", "", nil)
		s.update("1", updateFields{AddBlocks: []string{"2"}})
		r := s.update("1", updateFields{AddBlockedBy: []string{"2"}})
		eq(t, slices.Contains(r.Warnings, "cycle: #1 and #2 block each other"), true)
	})
	tw(t, f, "clearCompleted returns 0 when no completed tasks", func(t *testing.T) {
		s := newTaskStore("")
		s.create("Pending", "Desc", "", nil)
		eq(t, s.clearCompleted(), 0)
	})
	tw(t, f, "list sorts pending → in_progress → completed with all three present", func(t *testing.T) {
		s := newTaskStore("")
		for _, name := range []string{"Pending task", "Completed task", "In-progress task", "Another pending"} {
			s.create(name, "Desc", "", nil)
		}
		s.update("2", updateFields{Status: sp("completed")})
		s.update("3", updateFields{Status: sp("in_progress")})
		// The store returns by ID; the TaskList tool sorts by status group. Verify the raw list order, then the grouped sort.
		order := map[string]int{"pending": 0, "in_progress": 1, "completed": 2}
		sorted := slices.Clone(s.list(nil))
		slices.SortStableFunc(sorted, func(a, b *task) int {
			if d := order[a.Status] - order[b.Status]; d != 0 {
				return d
			}
			x, _ := strconv.Atoi(a.ID)
			y, _ := strconv.Atoi(b.ID)
			return x - y
		})
		eq(t, taskIDs(sorted), []string{"1", "4", "3", "2"})
		statuses := []string{}
		for _, tk := range sorted {
			statuses = append(statuses, tk.Status)
		}
		eq(t, statuses, []string{"pending", "pending", "in_progress", "completed"})
	})
}

func TestTaskStoreFileBacked(t *testing.T) {
	const f = "task-store"
	listID := "test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir()) // named lists live under <agent dir>/tasks (the original's ~/.pi/tasks)
	filePath := func() string { return filepath.Join(agentDir(), "tasks", listID+".json") }
	tw(t, f, "persists tasks to disk", func(t *testing.T) {
		newTaskStore(listID).create("Persistent task", "Should survive reload", "", nil)
		tasks := newTaskStore(listID).list(nil) // a new store instance pointing to the same file
		eq(t, len(tasks), 1)
		eq(t, tasks[0].Subject, "Persistent task")
	})
	tw(t, f, "persists in_progress updates to disk", func(t *testing.T) {
		os.Remove(filePath())
		s1 := newTaskStore(listID)
		s1.create("Task", "Desc", "", nil)
		s1.update("1", updateFields{Status: sp("in_progress")})
		eq(t, newTaskStore(listID).get("1").Status, "in_progress")
	})
	tw(t, f, "persists completed tasks to disk", func(t *testing.T) {
		os.Remove(filePath())
		s1 := newTaskStore(listID)
		s1.create("Done task", "Desc", "", nil)
		s1.create("Pending task", "Desc", "", nil)
		s1.update("1", updateFields{Status: sp("completed")})
		s2 := newTaskStore(listID)
		eq(t, s2.get("1").Status, "completed")
		if s2.get("2") == nil {
			t.Fatal("task 2 missing")
		}
		eq(t, len(s2.list(nil)), 2)
	})
	tw(t, f, "restores all tasks across instances", func(t *testing.T) {
		os.Remove(filePath())
		s1 := newTaskStore(listID)
		s1.create("Pending", "Desc", "", nil)
		s1.create("In progress", "Desc", "", nil)
		s1.create("Done", "Desc", "", nil)
		s1.update("2", updateFields{Status: sp("in_progress")})
		s1.update("3", updateFields{Status: sp("completed")})
		tasks := newTaskStore(listID).list(nil)
		eq(t, len(tasks), 3)
		for _, id := range []string{"1", "2", "3"} {
			eq(t, slices.Contains(taskIDs(tasks), id), true)
		}
	})
	tw(t, f, "persists ID counter across instances", func(t *testing.T) {
		os.Remove(filePath())
		s1 := newTaskStore(listID)
		s1.create("Task 1", "Desc", "", nil)
		s1.create("Task 2", "Desc", "", nil)
		eq(t, newTaskStore(listID).create("Task 3", "Desc", "", nil).ID, "3")
	})
}

func TestTaskStoreAbsolutePath(t *testing.T) {
	const f = "task-store"
	tw(t, f, "accepts absolute path and persists tasks", func(t *testing.T) {
		p := filepath.Join(scratch(t), "tasks.json")
		newTaskStore(p).create("Abs path task", "Desc", "", nil)
		s2 := newTaskStore(p)
		eq(t, len(s2.list(nil)), 1)
		eq(t, s2.list(nil)[0].Subject, "Abs path task")
	})
	tw(t, f, "persists completed tasks when using absolute path", func(t *testing.T) {
		p := filepath.Join(scratch(t), "tasks.json")
		s := newTaskStore(p)
		s.create("Pending", "Desc", "", nil)
		s.create("Completed", "Desc", "", nil)
		s.update("2", updateFields{Status: sp("completed")})
		eq(t, len(readJSON(t, p).(obj)["tasks"].(arr)), 2)
	})
	tw(t, f, "recreates the parent directory before later mutations", func(t *testing.T) {
		parent := filepath.Join(scratch(t), "missing-parent")
		p := filepath.Join(parent, "tasks.json")
		s := newTaskStore(p)
		s.create("Task", "Desc", "", nil)
		os.RemoveAll(parent)
		s.clearCompleted() // must not throw
		eq(t, fileExists(p), true)
	})
	tw(t, f, "normalizes legacy task records missing blockedBy/blocks/metadata on load", func(t *testing.T) {
		p := filepath.Join(scratch(t), "tasks.json")
		// A task file written before the blocking feature: no blockedBy/blocks/metadata.
		writeJSON(t, p, obj{"nextId": 2, "tasks": arr{obj{"id": "1", "subject": "Legacy task", "description": "From an older version", "status": "pending"}}})
		tk := newTaskStore(p).get("1")
		eq(t, tk.BlockedBy, []string{})
		eq(t, tk.Blocks, []string{})
		eq(t, tk.Metadata, obj{})
		eq(t, tk.Subject, "Legacy task") // existing fields preserved
	})
	tw(t, f, "creates the backing directory lazily — not on construction, but on first write", func(t *testing.T) {
		parent := filepath.Join(scratch(t), "lazy")
		p := filepath.Join(parent, "tasks.json")
		s := newTaskStore(p)
		// Constructing a store must not create the directory for a session that never persists a task.
		eq(t, fileExists(parent), false)
		s.create("Task", "Desc", "", nil)
		eq(t, fileExists(parent), true)
		eq(t, fileExists(p), true)
	})
}

func TestTaskStoreListIDResolution(t *testing.T) {
	tw(t, "task-store", "resolves a bare list ID under the user's home directory, not the working directory", func(t *testing.T) {
		// PI_TASKS=my-list is a shared-list name, not a path: it must land in <agent dir>/tasks/ (the original's
		// ~/.pi/tasks/, moved under PiG's agent directory), not under the working directory.
		dir := scratch(t)
		t.Setenv("PIG_CODING_AGENT_DIR", dir)
		newTaskStore("my-list").create("Shared list task", "d", "", nil)
		eq(t, fileExists(filepath.Join(dir, "tasks", "my-list.json")), true)
	})
}

func TestTaskStoreMalformedFiles(t *testing.T) {
	const f = "task-store"
	file := func(t *testing.T) string { return filepath.Join(scratch(t), "tasks.json") }
	tw(t, f, "continues IDs after the highest existing task when nextId is missing", func(t *testing.T) {
		// A truncated write, a bad merge or a hand edit can drop the envelope fields. `nextId` decides every
		// future ID, so an unusable one used to produce the task ID "NaN", then IDs restarting at "0".
		p := file(t)
		writeJSON(t, p, obj{"tasks": arr{obj{"id": "1", "subject": "One", "description": "d", "status": "completed"},
			obj{"id": "7", "subject": "Seven", "description": "d", "status": "pending"}}})
		eq(t, newTaskStore(p).create("Next", "d", "", nil).ID, "8")
	})
	tw(t, f, "starts from 1 when nextId is missing and there are no tasks", func(t *testing.T) {
		p := file(t)
		writeJSON(t, p, obj{"tasks": arr{}})
		eq(t, newTaskStore(p).create("First", "d", "", nil).ID, "1")
	})
	tw(t, f, "does not reissue an ID that a task already holds", func(t *testing.T) {
		p := file(t)
		writeJSON(t, p, obj{"nextId": 2, "tasks": arr{obj{"id": "1", "subject": "One", "description": "d", "status": "pending"},
			obj{"id": "5", "subject": "Five", "description": "d", "status": "pending"}}})
		eq(t, newTaskStore(p).create("Next", "d", "", nil).ID, "6")
	})
	tw(t, f, "keeps the tasks it has when the file has no task array", func(t *testing.T) {
		p := file(t)
		s := newTaskStore(p)
		s.create("Keep me", "d", "", nil)
		writeJSON(t, p, obj{"nextId": 5})
		eq(t, subjects(s.list(nil)), []string{"Keep me"})
	})
	tw(t, f, "keeps the tasks it has when the file is not valid JSON", func(t *testing.T) {
		p := file(t)
		s := newTaskStore(p)
		s.create("Keep me", "d", "", nil)
		os.WriteFile(p, []byte("{ this is not json"), 0o644)
		eq(t, subjects(s.list(nil)), []string{"Keep me"})
	})
	tw(t, f, "keeps the tasks it has when the file holds a JSON array", func(t *testing.T) {
		p := file(t)
		s := newTaskStore(p)
		s.create("Keep me", "d", "", nil)
		writeJSON(t, p, arr{obj{"id": "1"}})
		eq(t, subjects(s.list(nil)), []string{"Keep me"})
	})
	tw(t, f, "skips entries that are not task records", func(t *testing.T) {
		p := file(t)
		writeJSON(t, p, obj{"nextId": 3, "tasks": arr{nil, 5, "nope", obj{"subject": "no id"},
			obj{"id": "2", "subject": "Real", "description": "d", "status": "pending"}}})
		eq(t, subjects(newTaskStore(p).list(nil)), []string{"Real"})
	})
	tw(t, f, "respects a valid nextId", func(t *testing.T) {
		p := file(t)
		writeJSON(t, p, obj{"nextId": 42, "tasks": arr{obj{"id": "1", "subject": "One", "description": "d", "status": "pending"}}})
		eq(t, newTaskStore(p).create("Next", "d", "", nil).ID, "42")
	})
}
