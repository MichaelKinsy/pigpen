package rpiv_todo

import (
	"math"
	"testing"
)

const fReducer = "state/state-reducer"

func TestApplyTaskMutationCreate(t *testing.T) {
	tw(t, fReducer, "rejects empty subject", func(t *testing.T) {
		r := applyTaskMutation(emptyState(), "create", params{"subject": ""})
		eq(t, r.Op, errOp("subject required for create"), "op")
		eq(t, len(r.State.Tasks), 0, "tasks")
		eq(t, r.State.NextID, 1, "nextId")
	})
	tw(t, fReducer, "rejects dangling blockedBy", func(t *testing.T) {
		r := applyTaskMutation(emptyState(), "create", params{"subject": "x", "blockedBy": []any{99.0}})
		eq(t, r.Op, errOp("blockedBy: #99 not found"), "op")
		eq(t, r.State.NextID, 1, "nextId")
	})
	tw(t, fReducer, "rejects deleted blockedBy", func(t *testing.T) {
		s := stateWith(tk(1, "done", withStatus(statusDeleted)))
		r := applyTaskMutation(s, "create", params{"subject": "new", "blockedBy": []any{1.0}})
		eq(t, r.Op, errOp("blockedBy: #1 is deleted"), "op")
	})
	tw(t, fReducer, "creates with next id and preserves immutability", func(t *testing.T) {
		s := emptyState()
		r := applyTaskMutation(s, "create", params{"subject": "write tests"})
		eq(t, len(r.State.Tasks), 1, "tasks")
		if g := at(r.State.Tasks, 0); g.ID != 1 || g.Subject != "write tests" || g.Status != statusPending {
			t.Fatalf("task = %#v", g)
		}
		eq(t, r.State.NextID, 2, "nextId")
		eq(t, len(s.Tasks), 0, "the input state's tasks are untouched")
		eq(t, r.Op, op{Kind: "create", TaskID: 1}, "op")
	})
}

func TestApplyTaskMutationUpdate(t *testing.T) {
	tw(t, fReducer, "rejects id-only update", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x")), "update", params{"id": 1.0})
		eq(t, r.Op, errOp("update requires at least one mutable field: subject, description, activeForm, status, owner, metadata, addBlockedBy, or removeBlockedBy"), "op")
	})
	tw(t, fReducer, "rejects illegal transition completed → in_progress", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x", withStatus(statusCompleted))), "update", params{"id": 1.0, "status": "in_progress"})
		eq(t, r.Op, errOp("illegal transition completed → in_progress"), "op")
	})
	tw(t, fReducer, "allows completed → deleted transition", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x", withStatus(statusCompleted))), "update", params{"id": 1.0, "status": "deleted"})
		eq(t, r.Op, op{Kind: "update", ID: 1, FromStatus: "completed", ToStatus: "deleted", Changed: true}, "op")
		eq(t, at(r.State.Tasks, 0).Status, "deleted", "status")
	})
	tw(t, fReducer, "flags a no-effect status update (status set to its current value) as changed:false", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x", withStatus(statusPending))), "update", params{"id": 1.0, "status": "pending"})
		eq(t, r.Op, op{Kind: "update", ID: 1, FromStatus: "pending", ToStatus: "pending", Changed: false}, "op")
	})
	tw(t, fReducer, "flags a re-sent identical field as changed:false", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x", withDescription("d"))), "update", params{"id": 1.0, "subject": "x", "description": "d"})
		if r.Op.Kind != "update" || r.Op.Changed {
			t.Fatalf("op = %#v", r.Op)
		}
	})
	tw(t, fReducer, "flags a blockedBy-only update as changed:true even when status is unchanged", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "a"), tk(2, "b")), "update", params{"id": 1.0, "addBlockedBy": []any{2.0}})
		eq(t, r.Op, op{Kind: "update", ID: 1, FromStatus: "pending", ToStatus: "pending", Changed: true}, "op")
	})
	tw(t, fReducer, "flags a subject-only update on a task with existing deps as changed:true (blockedBy unchanged)", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "old", withBlockedBy(2)), tk(2, "dep")), "update", params{"id": 1.0, "subject": "new"})
		eq(t, r.Op, op{Kind: "update", ID: 1, FromStatus: "pending", ToStatus: "pending", Changed: true}, "op")
		eq(t, at(r.State.Tasks, 0).BlockedBy, []float64{2}, "blockedBy")
	})
	tw(t, fReducer, "flags swapping one dependency for another (same length) as changed:true", func(t *testing.T) {
		s := stateWith(tk(1, "a", withBlockedBy(2)), tk(2, "b"), tk(3, "c"))
		r := applyTaskMutation(s, "update", params{"id": 1.0, "removeBlockedBy": []any{2.0}, "addBlockedBy": []any{3.0}})
		eq(t, r.Op, op{Kind: "update", ID: 1, FromStatus: "pending", ToStatus: "pending", Changed: true}, "op")
		eq(t, at(r.State.Tasks, 0).BlockedBy, []float64{3}, "blockedBy")
	})
	tw(t, fReducer, "rejects self-block via addBlockedBy", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x")), "update", params{"id": 1.0, "addBlockedBy": []any{1.0}})
		eq(t, r.Op, errOp("cannot block #1 on itself"), "op")
	})
	tw(t, fReducer, "rejects cycle in blockedBy graph", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "a", withBlockedBy(2)), tk(2, "b")), "update", params{"id": 2.0, "addBlockedBy": []any{1.0}})
		eq(t, r.Op, errOp("addBlockedBy would create a cycle in the blockedBy graph"), "op")
	})
	tw(t, fReducer, "drops blockedBy field when merged set becomes empty", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "a", withBlockedBy(2)), tk(2, "b")), "update", params{"id": 1.0, "removeBlockedBy": []any{2.0}})
		if at(r.State.Tasks, 0).BlockedBy != nil {
			t.Fatalf("blockedBy = %#v, want absent", at(r.State.Tasks, 0).BlockedBy)
		}
	})
	tw(t, fReducer, "drops metadata key when value is null", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x", withMetadata(map[string]any{"a": 1.0, "b": 2.0}))), "update", params{"id": 1.0, "metadata": map[string]any{"a": nil}})
		eq(t, at(r.State.Tasks, 0).Metadata, map[string]any{"b": 2.0}, "metadata")
	})
	tw(t, fReducer, "sets and overwrites metadata keys when value is non-null", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x", withMetadata(map[string]any{"a": 1.0, "b": 2.0}))), "update", params{"id": 1.0, "metadata": map[string]any{"a": 99.0, "c": 3.0}})
		eq(t, at(r.State.Tasks, 0).Metadata, map[string]any{"a": 99.0, "b": 2.0, "c": 3.0}, "metadata")
	})
	tw(t, fReducer, "collapses metadata to undefined when every key is deleted", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x", withMetadata(map[string]any{"a": 1.0}))), "update", params{"id": 1.0, "metadata": map[string]any{"a": nil}})
		if at(r.State.Tasks, 0).Metadata != nil {
			t.Fatalf("metadata = %#v, want absent", at(r.State.Tasks, 0).Metadata)
		}
	})
}

func TestApplyTaskMutationListGetDeleteClear(t *testing.T) {
	tw(t, fReducer, "list emits Op with includeDeleted flag and optional statusFilter", func(t *testing.T) {
		s := stateWith(tk(1, "a", withStatus(statusPending)), tk(2, "b", withStatus(statusDeleted)))
		r := applyTaskMutation(s, "list", params{"includeDeleted": true, "status": "deleted"})
		eq(t, r.Op, op{Kind: "list", IncludeDeleted: true, StatusFilter: "deleted"}, "op")
		eq(t, r.State, s, "the state is returned unchanged")
	})
	tw(t, fReducer, "delete on already-deleted task errors", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x", withStatus(statusDeleted))), "delete", params{"id": 1.0})
		eq(t, r.Op, errOp("#1 is already deleted"), "op")
	})
	tw(t, fReducer, "delete emits Op with id + subject", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(1, "x")), "delete", params{"id": 1.0})
		eq(t, r.Op, op{Kind: "delete", ID: 1, Subject: "x"}, "op")
		eq(t, at(r.State.Tasks, 0).Status, "deleted", "status")
	})
	tw(t, fReducer, "clear emits Op with prior count and resets nextId to 1", func(t *testing.T) {
		r := applyTaskMutation(stateWith(tk(5, "x")), "clear", params{})
		eq(t, r.Op, op{Kind: "clear", Count: 1}, "op")
		eq(t, len(r.State.Tasks), 0, "tasks")
		eq(t, r.State.NextID, 1, "nextId")
	})
	tw(t, fReducer, "get emits Op with the resolved task", func(t *testing.T) {
		s := stateWith(tk(1, "alpha"))
		r := applyTaskMutation(s, "get", params{"id": 1.0})
		eq(t, r.Op, op{Kind: "get", Task: at(s.Tasks, 0)}, "op")
	})
}

func TestIsTransitionValid(t *testing.T) {
	tw(t, fReducer, "is idempotent on same→same", func(t *testing.T) { eq(t, isTransitionValid("completed", "completed"), true, "completed→completed") })
	tw(t, fReducer, "rejects completed → in_progress", func(t *testing.T) {
		eq(t, isTransitionValid("completed", "in_progress"), false, "completed→in_progress")
	})
	tw(t, fReducer, "allows completed → deleted", func(t *testing.T) { eq(t, isTransitionValid("completed", "deleted"), true, "completed→deleted") })
}

// The reducer contracts the upstream tests never reach (the scenarios cover them through the tool too).
func TestApplyTaskMutationBranches(t *testing.T) {
	t.Run("update and get and delete need an id", func(t *testing.T) {
		for _, c := range []struct{ action, msg string }{{"update", "id required for update"}, {"get", "id required for get"}, {"delete", "id required for delete"}} {
			eq(t, applyTaskMutation(stateWith(tk(1, "x")), c.action, params{}).Op, errOp(c.msg), c.action)
		}
	})
	t.Run("an unknown or non-integral id is not found, formatted like JavaScript", func(t *testing.T) {
		eq(t, applyTaskMutation(emptyState(), "get", params{"id": 9.0}).Op, errOp("#9 not found"), "9")
		eq(t, applyTaskMutation(emptyState(), "get", params{"id": 1.5}).Op, errOp("#1.5 not found"), "1.5")
	})
	t.Run("a whitespace-only subject is empty (JavaScript trim)", func(t *testing.T) {
		eq(t, applyTaskMutation(emptyState(), "create", params{"subject": " \u00a0\ufeff\n"}).Op, errOp("subject required for create"), "op")
	})
	t.Run("create keeps only the fields given, copies blockedBy and metadata", func(t *testing.T) {
		in := params{"subject": "s", "description": "", "activeForm": "a", "owner": "o", "metadata": map[string]any{}, "blockedBy": []any{}}
		g := at(applyTaskMutation(emptyState(), "create", in).State.Tasks, 0)
		if g.Description != nil || g.BlockedBy != nil || g.Owner == nil || *g.Owner != "o" || g.ActiveForm == nil || g.Metadata == nil {
			t.Fatalf("task = %#v (empty description and blockedBy are dropped, an empty metadata object is kept)", g)
		}
	})
	t.Run("an empty string on update is kept", func(t *testing.T) {
		g := at(applyTaskMutation(stateWith(tk(1, "x", withDescription("d"))), "update", params{"id": 1.0, "description": ""}).State.Tasks, 0)
		if g.Description == nil || *g.Description != "" {
			t.Fatalf("description = %v, want empty string kept", g.Description)
		}
	})
	t.Run("addBlockedBy not found and deleted", func(t *testing.T) {
		s := stateWith(tk(1, "a"), tk(2, "b", withStatus(statusDeleted)))
		eq(t, applyTaskMutation(s, "update", params{"id": 1.0, "addBlockedBy": []any{8.0}}).Op, errOp("addBlockedBy: #8 not found"), "not found")
		eq(t, applyTaskMutation(s, "update", params{"id": 1.0, "addBlockedBy": []any{2.0}}).Op, errOp("addBlockedBy: #2 is deleted"), "deleted")
	})
	t.Run("a failed update leaves the state untouched", func(t *testing.T) {
		s := stateWith(tk(1, "a"))
		eq(t, applyTaskMutation(s, "update", params{"id": 1.0, "status": "bogus"}).State, s, "state")
	})
}

const fGraph = "state/task-graph"

func TestTaskGraph(t *testing.T) {
	tw(t, fGraph, "detects direct cycle", func(t *testing.T) {
		tasks := []task{tk(1, "a"), tk(2, "b", withBlockedBy(1))}
		eq(t, detectCycle(tasks, 1, []float64{2}), true, "cycle")
	})
	tw(t, fGraph, "returns false for acyclic graph", func(t *testing.T) {
		tasks := []task{tk(1, "a"), tk(2, "b", withBlockedBy(1))}
		eq(t, detectCycle(tasks, 2, []float64{1}), false, "cycle")
	})
	tw(t, fGraph, "returns an empty map when no task has blockedBy", func(t *testing.T) {
		eq(t, len(deriveBlocks([]task{tk(1, "a"), tk(2, "b")})), 0, "size")
	})
	tw(t, fGraph, "inverts blockedBy into a blocks map", func(t *testing.T) {
		b := deriveBlocks([]task{tk(1, "root"), tk(2, "dep", withBlockedBy(1)), tk(3, "dep2", withBlockedBy(1, 2))})
		eq(t, b[1], []int{2, 3}, "blocks of 1")
		eq(t, b[2], []int{3}, "blocks of 2")
		if _, ok := b[3]; ok {
			t.Fatalf("blocks of 3 = %v, want none", b[3])
		}
	})
}

func TestOwnerAloneIsAMutation(t *testing.T) {
	r := applyTaskMutation(stateWith(tk(1, "x")), "update", params{"id": 1.0, "owner": "someone"})
	eq(t, r.Op, op{Kind: "update", ID: 1, FromStatus: "pending", ToStatus: "pending", Changed: true}, "op")
	if o := at(r.State.Tasks, 0).Owner; o == nil || *o != "someone" {
		t.Fatalf("owner = %v", o)
	}
}

// A model may send any JSON number as an id. The error text formats it as JavaScript's `${n}` does
// (Number::toString): fixed notation from 1e-6 up to 1e21, otherwise an exponent without zero padding,
// and -0 reads "0". Node: `${0.000001}` === "0.000001", `${1e-7}` === "1e-7", `${-0}` === "0".
func TestErrorTextFormatsAnIdLikeJavaScript(t *testing.T) {
	for id, want := range map[float64]string{
		0.000001: "#0.000001 not found",
		1e-7:     "#1e-7 not found",
		1.5e-7:   "#1.5e-7 not found",
		1e21:     "#1e+21 not found",
		1.5:      "#1.5 not found",
		-2:       "#-2 not found",
	} {
		eq(t, applyTaskMutation(emptyState(), "get", params{"id": id}).Op, errOp(want), "get")
	}
	negZero := math.Copysign(0, -1)
	eq(t, applyTaskMutation(emptyState(), "get", params{"id": negZero}).Op, errOp("#0 not found"), "get -0")
	eq(t, applyTaskMutation(emptyState(), "create", params{"subject": "x", "blockedBy": []any{1e-7}}).Op,
		errOp("blockedBy: #1e-7 not found"), "blockedBy")
}
