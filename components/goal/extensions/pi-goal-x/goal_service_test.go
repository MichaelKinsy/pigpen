package pi_goal_x

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoalService(t *testing.T) {
	tw(t, "goal-service", "keeps an old locked buffer when focus changes, then flushes without stealing focus", func(t *testing.T) {
		f := gsNewFixture(t)
		f.service.beginTurn(f.ctx, gstr(f.written, "id"))
		out := gsMust(f.service.apply(f.ctx, goalMutationSpec{mutate: gsSetObjective("Buffered old-goal work")}))
		eq(t, out.ok, true, "buffered apply ok")
		other := gsMust(f.ctx.writeActiveGoalFile(createGoal("Other goal", false, false, false, clockMs())))
		f.ref.setFocused(other)
		lock := gsMust(acquireGoalLock(f.ctx, gstr(f.written, "id"), 0, 0))
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: gsSetObjective("Should wait")}))
		eq(t, res.ok, false, "apply under contention fails")
		eq(t, f.service.isTurnBuffered(), true, "turn stays buffered")
		lock.release()
		gsMust(f.service.flushTurn(f.ctx))
		eq(t, f.ref.getFocusedGoalId(), gstr(other, "id"), "focus not stolen")
		eq(t, gstr(parseGoalFile(filepath.Join(f.cwd, gstr(f.written, "activePath"))), "objective"), "Buffered old-goal work", "old goal flushed")
		eq(t, gstr(parseGoalFile(filepath.Join(f.cwd, gstr(other, "activePath"))), "objective"), "Other goal", "other untouched")
	})

	tw(t, "goal-service", "apply writes the file, appends the ledger, then commits memory", func(t *testing.T) {
		f := gsNewFixture(t)
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{
			reconcile: new(false),
			mutate:    gsSetObjective("=== Goal ===\nObjective: Mutated"),
			ledger:    []*jsObject{gsPausedEvent(gstr(f.written, "id"), "test", nowIso())},
		}))
		if !res.ok {
			t.Fatal("apply should succeed")
		}
		eq(t, len(f.activeFiles()), 1, "one active goal file")
		parsed := parseGoalFile(f.activePath(0))
		if parsed == nil || !strings.Contains(gstr(parsed, "objective"), "Mutated") {
			t.Fatal("file must contain the mutated objective")
		}
		events := f.ledgerEvents()
		eq(t, len(events), 1, "one ledger event")
		eq(t, gstr(events[0], "type"), "goal_paused", "event type")
		focused := f.ref.getFocused()
		if focused == nil || !strings.Contains(gstr(focused, "objective"), "Mutated") {
			t.Fatal("memory goal must reflect the mutation")
		}
	})

	tw(t, "goal-service", "ledger factory failure does not roll back the write or the memory commit", func(t *testing.T) {
		f := gsNewFixture(t)
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{
			reconcile: new(false),
			mutate:    gsSetObjective("=== Goal ===\nObjective: Ledger-broken"),
			ledgerFn:  func(*jsObject) ([]*jsObject, error) { return nil, os.ErrInvalid },
		}))
		if !res.ok {
			t.Fatal("apply must still succeed when the ledger factory fails")
		}
		if !strings.Contains(gstr(f.ref.getFocused(), "objective"), "Ledger-broken") {
			t.Fatal("memory must be committed")
		}
	})

	tw(t, "goal-service", "batches multiple ledger events from one mutation", func(t *testing.T) {
		f := gsNewFixture(t)
		at := nowIso()
		id := gstr(f.written, "id")
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{
			reconcile: new(false),
			mutate:    func(g *jsObject) *jsObject { return goalWith(g, "status", "paused", "autoContinue", false) },
			ledger: []*jsObject{
				gsPausedEvent(id, "first", at),
				ledgerEvent("goal_resumed", "goalId", id, "reason", "second", "at", at),
			},
		}))
		if !res.ok {
			t.Fatal("mutation must succeed")
		}
		var types []string
		for _, e := range f.ledgerEvents() {
			types = append(types, gstr(e, "type"))
		}
		eq(t, types, []string{"goal_paused", "goal_resumed"}, "event types")
	})

	tw(t, "goal-service", "preserves a buffered turn when the lock is temporarily contended", func(t *testing.T) {
		f := gsNewFixture(t)
		id := gstr(f.written, "id")
		f.service.beginTurn(f.ctx, id)
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: gsSetObjective("=== Goal ===\nObjective: Retried flush")}))
		if !res.ok {
			t.Fatal("in-turn mutation should be buffered")
		}
		lock := gsMust(acquireGoalLock(f.ctx, id, 0, 0))
		if got := gsMust(f.service.flushTurn(f.ctx)); got != nil {
			t.Fatal("contended flush should defer")
		}
		eq(t, f.service.isTurnBuffered(), true, "deferred transaction must remain buffered")
		lock.release()
		if flushed := gsMust(f.service.flushTurn(f.ctx)); flushed == nil {
			t.Fatal("the buffered transaction should flush after contention clears")
		}
		if !strings.Contains(gstr(parseGoalFile(f.activePath(0)), "objective"), "Retried flush") {
			t.Fatal("flushed objective not on disk")
		}
		eq(t, f.service.isTurnBuffered(), false, "turn closed")
	})

	tw(t, "goal-service", "expected goal id mismatch rejects without writing or appending", func(t *testing.T) {
		f := gsNewFixture(t)
		before := serializeGoalFile(f.written)
		other := "some-other-goal"
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{
			reconcile:      new(false),
			expectedGoalId: &other,
			mutate:         gsSetObjective("=== Goal ===\nObjective: Nope"),
			ledger:         []*jsObject{gsPausedEvent(gstr(f.written, "id"), "x", nowIso())},
		}))
		eq(t, res.ok, false, "expected id mismatch must reject")
		eq(t, len(f.activeFiles()), 1, "one active file")
		eq(t, gsReadFile(t, f.activePath(0)), before, "file must be unchanged")
		eq(t, len(f.ledgerEvents()), 0, "no ledger event appended")
	})

	tw(t, "goal-service", "stale focus revision rejects without writing", func(t *testing.T) {
		f := gsNewFixture(t)
		id := gstr(f.written, "id")
		token := f.ref.focusToken(id)
		f.ref.assignFocusedGoalId("")
		f.ref.assignFocusedGoalId(id)
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), focusToken: &token, mutate: gsSetObjective("=== Goal ===\nObjective: Stale")}))
		eq(t, res.ok, false, "stale focus token must reject")
		focused := f.ref.getFocused()
		if focused == nil || strings.Contains(gstr(focused, "objective"), "Stale") {
			t.Fatal("memory must be unchanged")
		}
	})

	tw(t, "goal-service", "reconcile runs first: a focused goal deleted on disk aborts the mutation", func(t *testing.T) {
		f := gsNewFixture(t)
		eq(t, len(f.activeFiles()), 1, "one active file")
		if err := os.Remove(f.activePath(0)); err != nil {
			t.Fatal(err)
		}
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{mutate: gsSetObjective("=== Goal ===\nObjective: Should not land")}))
		eq(t, res.ok, false, "mutation must be rejected after the goal is lost")
		eq(t, len(f.log.goalLost), 1, "onFocusedGoalLost hook must fire")
	})

	tw(t, "goal-service", "persist merges the authoritative objective from disk before writing", func(t *testing.T) {
		f := gsNewFixture(t)
		p := f.activePath(0)
		edited := strings.Replace(gsReadFile(t, p), "=== Goal ===\nObjective: Service test", "=== Goal ===\nObjective: User-edited objective", 1)
		if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		persisted := gsMust(f.service.persist(f.ctx))
		if persisted == nil {
			t.Fatal("persist must return the written goal")
		}
		if !strings.Contains(gstr(persisted, "objective"), "User-edited objective") {
			t.Fatal("persist must adopt the user edit")
		}
		if !strings.Contains(gstr(f.ref.getFocused(), "objective"), "User-edited objective") {
			t.Fatal("memory must adopt the user edit")
		}
	})

	tw(t, "goal-service", "create writes the active file, appends goal_created, and commits focus", func(t *testing.T) {
		f := gsNewFixture(t)
		next := createGoal("=== Goal ===\nObjective: Second goal", false, false, false, ms(2026, 8, 1, 10, 0, 0))
		res := gsMust(f.service.create(f.ctx, next, []*jsObject{
			ledgerEvent("goal_created", "goalId", gstr(next, "id"), "objective", gstr(next, "objective"), "sisyphus", false, "autoContinue", false, "at", gstr(next, "createdAt")),
		}))
		if !res.ok {
			t.Fatal("create must succeed")
		}
		eq(t, len(f.activeFiles()), 2, "both active files must exist")
		eq(t, len(f.ledgerEvents()), 1, "goal_created event appended")
		eq(t, gstr(f.ref.getFocused(), "id"), gstr(next, "id"), "focus moves to the new goal")
		eq(t, len(f.log.focusChanges), 1, "focus change effect must fire")
	})

	tw(t, "goal-service", "archive mode writes the archived file and does not commit focus", func(t *testing.T) {
		f := gsNewFixture(t)
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{
			reconcile:     new(false),
			archive:       true,
			commitFocused: new(false),
			mutate:        func(g *jsObject) *jsObject { return goalWith(g, "status", "paused", "stopReason", "user") },
			ledger:        []*jsObject{ledgerEvent("goal_completed", "goalId", gstr(f.written, "id"), "at", nowIso())},
		}))
		if !res.ok {
			t.Fatal("archive apply must succeed")
		}
		eq(t, len(f.activeFiles()), 0, "active file must be removed on archive")
		entries, _ := os.ReadDir(filepath.Join(f.cwd, ".pi", "goals", "archived"))
		n := 0
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "goal_") {
				n++
			}
		}
		eq(t, n, 1, "archived file must exist")
		eq(t, gstr(f.ref.getFocused(), "id"), gstr(f.written, "id"), "memory focus untouched")
	})

	tw(t, "goal-service", "ledger append failure after the authoritative write keeps the state transition and emits a diagnostic", func(t *testing.T) {
		f := gsNewFixture(t)
		// A directory at the ledger path blocks the append.
		if err := os.MkdirAll(goalLedgerPath(f.ctx), 0o755); err != nil {
			t.Fatal(err)
		}
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{
			reconcile: new(false),
			mutate: func(g *jsObject) *jsObject {
				return goalWith(g, "pauseReason", "updated despite ledger failure", "updatedAt", nowIso())
			},
			ledgerFn: func(w *jsObject) ([]*jsObject, error) {
				return []*jsObject{gsPausedEvent(gstr(w, "id"), "test", gstr(w, "updatedAt"))}, nil
			},
		}))
		if !res.ok {
			t.Fatal("state write must not be rolled back by a ledger failure")
		}
		eq(t, len(f.activeFiles()), 1, "active file still present")
		if !strings.Contains(gsReadFile(t, f.activePath(0)), "updated despite ledger failure") {
			t.Fatal("mutation persisted despite ledger failure")
		}
		if len(f.log.diagnostics) < 1 {
			t.Fatal("ledger failure must emit a diagnostic")
		}
		eq(t, f.log.diagnostics[0].source, "ledger", "diagnostic source")
		eq(t, f.log.diagnostics[0].eventType, "goal_paused", "diagnostic event type")
	})
}

func TestGoalServiceCrossProcess(t *testing.T) {
	tw(t, "goal-service", "objective race: exactly one initial write succeeds; the stale writer gets a typed conflict", func(t *testing.T) {
		f := gsNewFixture(t)
		bSvc, bRef, _ := f.secondWriter()
		id := gstr(f.written, "id")
		eq(t, goalRevision(f.ref.getFocused()), 0.0, "writer A read revision 0")
		eq(t, goalRevision(bRef.getFocused()), 0.0, "writer B read revision 0")
		tokA := f.ref.focusToken(id)
		a := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), focusToken: &tokA, mutate: gsSetObjective("=== Goal ===\nObjective: Writer A")}))
		if !a.ok {
			t.Fatal("first writer succeeds")
		}
		tokB := bRef.focusToken(id)
		b := gsMust(bSvc.apply(f.ctx, goalMutationSpec{reconcile: new(false), focusToken: &tokB, mutate: gsSetObjective("=== Goal ===\nObjective: Writer B")}))
		eq(t, b.ok, false, "stale writer must not overwrite blindly")
		if !strings.Contains(b.message, "revision") || !strings.Contains(b.message, "current revision is 1") {
			t.Fatalf("conflict message carries the revision: %s", b.message)
		}
		disk := parseGoalFile(f.activePath(0))
		if !strings.Contains(gstr(disk, "objective"), "Writer A") {
			t.Fatal("only writer A's mutation landed")
		}
		eq(t, goalRevision(disk), 1.0, "revision incremented exactly once")
	})

	tw(t, "goal-service", "task replacement race (set_goal_tasks-style): stale writer conflicts instead of merging", func(t *testing.T) {
		f := gsNewFixture(t)
		bSvc, bRef, _ := f.secondWriter()
		id := gstr(f.written, "id")
		tokA := f.ref.focusToken(id)
		a := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), focusToken: &tokA,
			mutate: func(g *jsObject) *jsObject { return goalWith(g, "taskList", gsTaskList("t1", "Writer A task", false)) }}))
		if !a.ok {
			t.Fatal("writer A must succeed")
		}
		tokB := bRef.focusToken(id)
		b := gsMust(bSvc.apply(f.ctx, goalMutationSpec{reconcile: new(false), focusToken: &tokB,
			mutate: func(g *jsObject) *jsObject { return goalWith(g, "taskList", gsTaskList("t9", "Writer B task", true)) }}))
		eq(t, b.ok, false, "concurrent task replacement must conflict, not silently merge unknown structure")
		if !strings.Contains(b.message, "revision") {
			t.Fatal(b.message)
		}
		tasks := parseGoalFile(f.activePath(0)).obj("taskList").vals["tasks"].([]any)
		eq(t, len(tasks), 1, "only writer A's structure landed")
		eq(t, gstr(tasks[0].(*jsObject), "id"), "t1", "task id")
	})

	tw(t, "goal-service", "task status race: exactly one task completion write succeeds; the second gets a typed failure", func(t *testing.T) {
		f := gsNewFixture(t)
		id := gstr(f.written, "id")
		tok := f.ref.focusToken(id)
		seeded := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), focusToken: &tok,
			mutate: func(g *jsObject) *jsObject { return goalWith(g, "taskList", gsTaskList("t1", "Shared task", false)) }}))
		if !seeded.ok {
			t.Fatal("seed must succeed")
		}
		bSvc, _, _ := f.secondWriter()
		complete := func(task *jsObject) goalGate {
			if gstr(task, "status") == "pending" {
				return goalGate{ok: true}
			}
			return goalGate{message: "Task " + gstr(task, "id") + " is already " + gstr(task, "status") + "; completion does not apply."}
		}
		update := func(task *jsObject) (*jsObject, goalGate) {
			return goalWith(task, "status", "complete", "completedAt", nowIso()), goalGate{ok: true}
		}
		a := gsMust(f.service.updateTask(f.ctx, goalTaskUpdateSpec{taskId: "t1", validate: complete, update: update}))
		if !a.ok {
			t.Fatalf("first writer completes the task: %s", a.message)
		}
		b := gsMust(bSvc.updateTask(f.ctx, goalTaskUpdateSpec{taskId: "t1", validate: complete, update: update}))
		eq(t, b.ok, false, "second writer must fail")
		if !strings.Contains(b.message, "already complete") {
			t.Fatalf("typed transition failure: %s", b.message)
		}
		tasks := parseGoalFile(f.activePath(0)).obj("taskList").vals["tasks"].([]any)
		eq(t, gstr(tasks[0].(*jsObject), "status"), "complete", "task complete on disk")
	})

	tw(t, "goal-service", "archive race: a goal archived by another process conflicts instead of being resurrected", func(t *testing.T) {
		f := gsNewFixture(t)
		bSvc, bRef, _ := f.secondWriter()
		a := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), archive: true, commitFocused: new(false),
			mutate: func(g *jsObject) *jsObject { return goalWith(g, "status", "complete", "stopReason", "user") }}))
		if !a.ok {
			t.Fatal("archive must succeed")
		}
		eq(t, len(f.activeFiles()), 0, "archived by writer A")
		tok := bRef.focusToken(gstr(f.written, "id"))
		b := gsMust(bSvc.apply(f.ctx, goalMutationSpec{reconcile: new(false), focusToken: &tok, mutate: gsSetObjective("=== Goal ===\nObjective: Resurrected")}))
		eq(t, b.ok, false, "stale writer must not resurrect the archived goal")
		if !strings.Contains(b.message, "deleted or archived") {
			t.Fatal(b.message)
		}
	})

	tw(t, "goal-service", "delete race: an externally deleted goal conflicts instead of being recreated", func(t *testing.T) {
		f := gsNewFixture(t)
		if err := os.Remove(f.activePath(0)); err != nil {
			t.Fatal(err)
		}
		tok := f.ref.focusToken(gstr(f.written, "id"))
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), focusToken: &tok, mutate: gsSetObjective("=== Goal ===\nObjective: Recreated")}))
		eq(t, res.ok, false, "deleted goal must not be resurrected")
		if !strings.Contains(res.message, "deleted or archived") {
			t.Fatal(res.message)
		}
		eq(t, len(f.activeFiles()), 0, "no active file recreated")
	})

	tw(t, "goal-service", "stale locks are recovered: a dead-pid lock does not block acquisition", func(t *testing.T) {
		f := gsNewFixture(t)
		lockDir := filepath.Join(f.cwd, ".pi", "goals", ".locks")
		if err := os.MkdirAll(lockDir, 0o755); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(lockDir, gstr(f.written, "id")+".lock")
		if err := os.WriteFile(lockPath, []byte(`{"pid":99999999,"startedAt":"2020-01-01T00:00:00.000Z"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		lock := gsMust(acquireGoalLock(f.ctx, gstr(f.written, "id"), 3, 5))
		lock.release()
		if _, err := os.Stat(lockPath); err == nil {
			t.Fatal("stale lock removed and released")
		}
	})

	tw(t, "goal-service", "a live lock is honored and released locks are reusable", func(t *testing.T) {
		f := gsNewFixture(t)
		id := gstr(f.written, "id")
		first := gsMust(acquireGoalLock(f.ctx, id, 0, 0))
		_, err := acquireGoalLock(f.ctx, id, 3, 5)
		if err == nil || !strings.Contains(err.Error(), "Timed out acquiring") {
			t.Fatalf("a live holder must not be stolen: %v", err)
		}
		first.release()
		again := gsMust(acquireGoalLock(f.ctx, id, 3, 0))
		again.release()
	})

	tw(t, "goal-service", "revision round-trips through the file and legacy records normalize to zero", func(t *testing.T) {
		f := gsNewFixture(t)
		raw := gsReadFile(t, f.activePath(0))
		if !strings.Contains(raw, `"revision": 0`) {
			t.Fatal("revision persisted in the goal file")
		}
		legacy := strings.Replace(raw, `"revision": 0,`, "", 1)
		legacyPath := filepath.Join(f.cwd, ".pi", "goals", "legacy_goal.md")
		if err := os.WriteFile(legacyPath, []byte(legacy), 0o644); err != nil {
			t.Fatal(err)
		}
		parsed := parseGoalFile(legacyPath)
		if parsed == nil {
			t.Fatal("legacy file must parse")
		}
		eq(t, goalRevision(parsed), 0.0, "missing revision normalizes to zero")
		eq(t, parsed.vals["revision"], 0.0, "revision key present as 0")
	})
}

func TestGoalServicePersistUsageMerge(t *testing.T) {
	usageOf := func(g *jsObject) (float64, float64) {
		u := g.obj("usage")
		return gnum(u, "tokensUsed"), gnum(u, "activeSeconds")
	}
	setUsage := func(g *jsObject, tokens, seconds float64) {
		u := newObject()
		u.set("tokensUsed", tokens)
		u.set("activeSeconds", seconds)
		g.set("usage", u)
	}

	tw(t, "goal-service", "merges only the session usage delta onto the disk record when the revision moved", func(t *testing.T) {
		f := gsNewFixture(t)
		a1 := cloneGoal(f.ref.getFocused())
		setUsage(a1, 50, 10)
		f.ref.setFocused(a1)
		first := gsMust(f.service.persist(f.ctx))
		if first == nil {
			t.Fatal("first persist must succeed")
		}
		tk, _ := usageOf(first)
		eq(t, tk, 50.0, "first persist tokens")

		diskGoal := parseGoalFile(f.activePath(0))
		bDisk := cloneGoal(diskGoal)
		bDisk.set("objective", "=== Goal ===\nObjective: Writer B objective")
		setUsage(bDisk, 70, 12)
		bDisk.set("revision", goalRevision(diskGoal)+1)
		gsMust(f.ctx.writeActiveGoalFile(bDisk))

		a2 := cloneGoal(f.ref.getFocused())
		setUsage(a2, 80, 17)
		f.ref.setFocused(a2)

		merged := gsMust(f.service.persist(f.ctx))
		if merged == nil {
			t.Fatal("persist must merge instead of returning null")
		}
		tk, sec := usageOf(merged)
		eq(t, tk, 100.0, "disk 70 + session delta 30")
		eq(t, sec, 19.0, "disk 12 + session delta 7")
		eq(t, goalRevision(merged), goalRevision(diskGoal)+2, "revision advances past the conflict")
		if !strings.Contains(gstr(merged, "objective"), "Writer B objective") {
			t.Fatal("authoritative disk objective must be preserved")
		}
		onDisk := parseGoalFile(f.activePath(0))
		if onDisk == nil {
			t.Fatal("merged goal must parse from disk")
		}
		tk, _ = usageOf(onDisk)
		eq(t, tk, 100.0, "merged usage on disk")
		if !strings.Contains(gstr(onDisk, "objective"), "Writer B objective") {
			t.Fatal("B's objective must be on disk")
		}
		again := gsMust(f.service.persist(f.ctx))
		if again == nil {
			t.Fatal("persist after merge succeeds on the success path")
		}
		tk, sec = usageOf(again)
		eq(t, tk, 100.0, "usage must not double-count after the merge")
		eq(t, sec, 19.0, "activeSeconds must not double-count after the merge")
	})

	tw(t, "goal-service", "never clobbers authoritative fields on conflict (usage only), even with no prior baseline", func(t *testing.T) {
		f := gsNewFixture(t)
		a := cloneGoal(f.ref.getFocused())
		setUsage(a, 100, 30)
		f.ref.setFocused(a)
		diskGoal := parseGoalFile(f.activePath(0))
		bDisk := cloneGoal(diskGoal)
		bDisk.set("objective", "=== Goal ===\nObjective: B wins")
		bDisk.set("revision", goalRevision(diskGoal)+1)
		gsMust(f.ctx.writeActiveGoalFile(bDisk))
		merged := gsMust(f.service.persist(f.ctx))
		if merged == nil {
			t.Fatal("persist must merge instead of returning null")
		}
		tk, sec := usageOf(merged)
		eq(t, tk, 100.0, "session usage must be added, not dropped")
		eq(t, sec, 30.0, "activeSeconds")
		if !strings.Contains(gstr(merged, "objective"), "B wins") {
			t.Fatal("authoritative disk objective must be preserved")
		}
		eq(t, goalRevision(merged), goalRevision(diskGoal)+2, "revision")
	})
}
