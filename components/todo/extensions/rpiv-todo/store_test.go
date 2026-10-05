package rpiv_todo

import "testing"

const fStore = "state/store"
const sID = "s1"

func mkTask(id int, subject string) task { return tk(id, subject) }

func TestStoreAccessors(t *testing.T) {
	tw(t, fStore, "__resetState() restores EMPTY_STATE shape (independent of EMPTY_STATE.tasks identity)", func(t *testing.T) {
		s := newStore()
		s.reset()
		eq(t, len(s.getTodos(sID)), 0, "todos")
		eq(t, s.getNextID(sID), 1, "nextId")
		// A fresh copy each time: appending to one read never shows in the next.
		a := s.getState(sID)
		a.Tasks = append(a.Tasks, mkTask(1, "leak"))
		eq(t, len(s.getState(sID).Tasks), 0, "a read does not alias a shared empty state")
	})
	tw(t, fStore, "getTodos(sid) returns the live tasks reference (read-only typed)", func(t *testing.T) {
		s := newStore()
		next := taskState{Tasks: []task{mkTask(1, "t1")}, NextID: 2}
		s.commitState(sID, next)
		got := s.getTodos(sID)
		if len(got) != 1 || &got[0] != &next.Tasks[0] {
			t.Fatalf("getTodos did not return the committed slice: %#v", got)
		}
	})
	tw(t, fStore, "getNextId(sid) reflects the current slot value", func(t *testing.T) {
		s := newStore()
		s.commitState(sID, taskState{Tasks: []task{}, NextID: 42})
		eq(t, s.getNextID(sID), 42, "nextId")
	})
	tw(t, fStore, "getState(sid) returns the same slot that getTodos/getNextId read from", func(t *testing.T) {
		s := newStore()
		next := taskState{Tasks: []task{mkTask(7, "lucky")}, NextID: 8}
		s.commitState(sID, next)
		snap := s.getState(sID)
		eq(t, snap, next, "state")
		eq(t, s.getTodos(sID), snap.Tasks, "todos")
		eq(t, s.getNextID(sID), snap.NextID, "nextId")
	})
	tw(t, fStore, "replaceState(sid, next) publishes a new slot wholesale (replay seam)", func(t *testing.T) {
		s := newStore()
		replayed := taskState{Tasks: []task{mkTask(10, "from-branch"), mkTask(11, "from-branch-2")}, NextID: 12}
		s.replaceState(sID, replayed)
		eq(t, s.getState(sID), replayed, "state")
		eq(t, s.getNextID(sID), 12, "nextId")
	})
	tw(t, fStore, "commitState() and replaceState() are interchangeable seams over the same slot", func(t *testing.T) {
		s := newStore()
		s.commitState(sID, taskState{Tasks: []task{mkTask(1, "t1")}, NextID: 2})
		eq(t, s.getNextID(sID), 2, "after commit")
		s.replaceState(sID, taskState{Tasks: []task{}, NextID: 99})
		eq(t, len(s.getTodos(sID)), 0, "todos")
		eq(t, s.getNextID(sID), 99, "after replace")
	})
	tw(t, fStore, "__resetState() after a commit clears the slot (test-isolation contract)", func(t *testing.T) {
		s := newStore()
		s.commitState(sID, taskState{Tasks: []task{mkTask(1, "t1")}, NextID: 2})
		s.reset()
		eq(t, len(s.getTodos(sID)), 0, "todos")
		eq(t, s.getNextID(sID), 1, "nextId")
	})
}

func TestStorePerSessionIsolation(t *testing.T) {
	tw(t, fStore, "commitState/replaceState to one session never affects another session's slot", func(t *testing.T) {
		s := newStore()
		s1 := taskState{Tasks: []task{mkTask(1, "s1-task")}, NextID: 2}
		s2 := taskState{Tasks: []task{mkTask(1, "s2-task")}, NextID: 5}
		s.commitState("s1", s1)
		s.commitState("s2", s2)
		eq(t, s.getState("s1"), s1, "s1")
		eq(t, s.getState("s2"), s2, "s2")
		s.replaceState("s1", taskState{Tasks: []task{mkTask(9, "new-s1")}, NextID: 10})
		eq(t, s.getState("s2"), s2, "s2 after a write to s1")
		eq(t, s.getNextID("s2"), 5, "s2 nextId")
		s.commitState("s2", taskState{Tasks: []task{}, NextID: 77})
		eq(t, s.getTodos("s1"), []task{mkTask(9, "new-s1")}, "s1 todos")
		eq(t, s.getNextID("s1"), 10, "s1 nextId")
	})
	tw(t, fStore, "a missing slot returns a fresh EMPTY_STATE copy, never aliasing EMPTY_STATE.tasks", func(t *testing.T) {
		s := newStore()
		slot := s.getState("never-seen")
		eq(t, len(slot.Tasks), 0, "tasks")
		eq(t, slot.NextID, 1, "nextId")
		eq(t, len(s.getTodos("absent")), 0, "todos")
		eq(t, s.getNextID("absent"), 1, "nextId")
		slot.Tasks = append(slot.Tasks, mkTask(1, "x"))
		eq(t, len(s.getState("never-seen").Tasks), 0, "a missing slot is not stored")
	})
}

func TestStoreEvictSession(t *testing.T) {
	tw(t, fStore, "evictSession(sid) frees the slot; a later read returns a fresh EMPTY_STATE copy", func(t *testing.T) {
		s := newStore()
		s.commitState(sID, taskState{Tasks: []task{mkTask(1, "t1")}, NextID: 2})
		eq(t, len(s.getState(sID).Tasks), 1, "before")
		s.evictSession(sID)
		after := s.getState(sID)
		eq(t, len(after.Tasks), 0, "tasks")
		eq(t, after.NextID, 1, "nextId")
	})
	tw(t, fStore, "evictSession on an absent slot is a no-op", func(t *testing.T) {
		s := newStore()
		s.evictSession("absent")
		eq(t, len(s.getTodos("absent")), 0, "todos")
	})
}

func TestStoreRenderPointer(t *testing.T) {
	tw(t, fStore, "getRenderState() returns a fresh EMPTY_STATE copy before any pointer is set", func(t *testing.T) {
		s := newStore()
		r := s.getRenderState()
		eq(t, len(r.Tasks), 0, "tasks")
		eq(t, r.NextID, 1, "nextId")
	})
	tw(t, fStore, "setActiveRenderSession(sid) makes getRenderState() read that session's slot", func(t *testing.T) {
		s := newStore()
		s.commitState("rendered", taskState{Tasks: []task{mkTask(3, "shown")}, NextID: 4})
		s.setActiveRenderSession("rendered")
		r := s.getRenderState()
		eq(t, r.Tasks, []task{mkTask(3, "shown")}, "tasks")
		eq(t, r.NextID, 4, "nextId")
	})
	tw(t, fStore, "setActiveRenderSession re-points the render slot to a different session", func(t *testing.T) {
		s := newStore()
		s.commitState("a", taskState{Tasks: []task{mkTask(1, "a")}, NextID: 2})
		s.commitState("b", taskState{Tasks: []task{mkTask(1, "b")}, NextID: 2})
		s.setActiveRenderSession("a")
		eq(t, at(s.getRenderState().Tasks, 0).Subject, "a", "a")
		s.setActiveRenderSession("b")
		eq(t, at(s.getRenderState().Tasks, 0).Subject, "b", "b")
	})
	tw(t, fStore, "__resetState() clears BOTH the Map and the render pointer", func(t *testing.T) {
		s := newStore()
		s.commitState("a", taskState{Tasks: []task{mkTask(1, "t1")}, NextID: 2})
		s.setActiveRenderSession("a")
		eq(t, len(s.getRenderState().Tasks), 1, "before")
		s.reset()
		eq(t, len(s.getState("a").Tasks), 0, "the old slot is gone")
		eq(t, len(s.getRenderState().Tasks), 0, "the pointer is cleared")
	})
}

type fakeSession struct {
	id  string
	err error
}

func (f fakeSession) GetSessionID() (string, error) { return f.id, f.err }

func TestStoreSid(t *testing.T) {
	tw(t, fStore, "sid(ctx) returns ctx.sessionManager.getSessionId()", func(t *testing.T) {
		got, err := sid(fakeSession{id: "abc"})
		if err != nil || got != "abc" {
			t.Fatalf("sid = %q, %v", got, err)
		}
	})
	tskip(t, fStore, "sid(ctx) coerces a null/undefined session id to empty string (defensive)", "a Go string cannot be null or undefined; the empty id is covered by TestSidOfAnEmptyIDAndAHostError")
}

func TestSidOfAnEmptyIDAndAHostError(t *testing.T) {
	got, err := sid(fakeSession{id: ""})
	if err != nil || got != "" {
		t.Fatalf("sid = %q, %v, want empty and no error", got, err)
	}
	if _, err := sid(fakeSession{err: errHost}); err == nil {
		t.Fatal("a host failure must be returned, not turned into an empty id")
	}
}

func TestStoreForegroundPointer(t *testing.T) {
	tw(t, fStore, "getActiveRenderSession() returns the session set by setActiveRenderSession()", func(t *testing.T) {
		s := newStore()
		s.setActiveRenderSession("s1")
		eq(t, s.getActiveRenderSession(), "s1", "pointer")
		eq(t, s.getRenderState(), s.getState("s1"), "render state")
	})
	tw(t, fStore, "clearActiveRenderSession() resets the pointer; getRenderState() returns a fresh EMPTY_STATE", func(t *testing.T) {
		s := newStore()
		s.commitState("s1", taskState{Tasks: []task{mkTask(1, "t1")}, NextID: 2})
		s.setActiveRenderSession("s1")
		eq(t, len(s.getRenderState().Tasks), 1, "before")
		s.clearActiveRenderSession()
		eq(t, s.getActiveRenderSession(), "", "pointer")
		r := s.getRenderState()
		eq(t, len(r.Tasks), 0, "tasks")
		eq(t, r.NextID, 1, "nextId")
	})
	tw(t, fStore, "__resetState() clears the foreground pointer", func(t *testing.T) {
		s := newStore()
		s.setActiveRenderSession("s1")
		s.reset()
		eq(t, s.getActiveRenderSession(), "", "pointer")
	})
}
