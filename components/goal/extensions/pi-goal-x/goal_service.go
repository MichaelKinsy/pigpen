package pi_goal_x

import "fmt"

// ported from goal-service.ts. GoalService is the sole mutation boundary for goal records: write -> ledger -> memory.
// Differences forced by Go: a TS throw is a returned error; a nullable goal id is "" (goal ids are never empty);
// a thrown ledger factory is an error returned by the factory (swallowed like the TS catch).

// goalFocusToken is { goalId, revision }.
type goalFocusToken struct {
	goalId   string
	revision int
}

// goalDiagnostic is GoalDiagnostic; severity is always "warning". goalId "" is undefined.
type goalDiagnostic struct {
	severity  string
	source    string // "ledger" | "storage"
	goalId    string
	eventType string
	message   string
}

// goalServiceRef is GoalServiceRef: the session state access and runtime hooks the service needs.
type goalServiceRef interface {
	getFocused() *jsObject
	// setFocused mirrors the state.goal setter: pool upsert + focus assignment (nil clears the focused goal).
	setFocused(goal *jsObject)
	getPool() *goalPool
	replacePool(pool *goalPool)
	// getFocusedGoalId is "" when nothing is focused.
	getFocusedGoalId() string
	// assignFocusedGoalId assigns the session focus ("" clears it); bumps the focus revision when it changes.
	assignFocusedGoalId(goalId string)
	focusToken(goalId string) goalFocusToken
	isTokenCurrent(token goalFocusToken) bool
	appendFocusEntry(goalId string, reason string)
	onFocusedGoalLost(lostGoalId string, ctx goalServiceContext)
	onReconciled(goal *jsObject)
	onFocusChanged(from, to string)
	onDiagnostic(diagnostic goalDiagnostic)
}

// goalServiceContext is GoalServiceContext (GoalFileContext).
type goalServiceContext = storage

// goalMutationSpec is GoalMutationSpec. Ledger is the fixed event list, LedgerFn the factory (used when non-nil).
type goalMutationSpec struct {
	expectedGoalId  *string
	focusToken      *goalFocusToken
	reconcile       *bool // nil = true
	refreshFromDisk bool
	mutate          func(goal *jsObject) *jsObject
	ledger          []*jsObject
	ledgerFn        func(written *jsObject) ([]*jsObject, error)
	archive         bool
	commitFocused   *bool // nil = true
}

// goalMutationOutcome is GoalMutationOutcome: GoalMutationResult when ok, GoalMutationFailure (message) otherwise.
type goalMutationOutcome struct {
	ok             bool
	goal           *jsObject
	previousGoalId string
	goalId         string
	focusChanged   bool
	message        string
}

func goalMutationFail(message string) goalMutationOutcome {
	return goalMutationOutcome{message: message}
}

// goalGate is { ok: true } | { ok: false, message }.
type goalGate struct {
	ok      bool
	message string
}

// goalTaskUpdateSpec is GoalTaskUpdateSpec.
type goalTaskUpdateSpec struct {
	focusToken *goalFocusToken
	taskId     string
	validate   func(task *jsObject) goalGate
	// update transforms the fresh task; a gate with ok=false aborts.
	update           func(task *jsObject) (*jsObject, goalGate)
	setCurrentTaskId *string
	ledger           func(written, updatedTask *jsObject) ([]*jsObject, error)
}

// goalTaskUpdateOutcome is GoalTaskUpdateOutcome.
type goalTaskUpdateOutcome struct {
	ok      bool
	goal    *jsObject
	task    *jsObject
	message string
}

func goalTaskFail(message string) goalTaskUpdateOutcome {
	return goalTaskUpdateOutcome{message: message}
}

// resolveUpdatedCurrentTaskId is the section 8 execution-focus rule; ok=false is undefined.
func resolveUpdatedCurrentTaskId(spec goalTaskUpdateSpec, current any, updatedTask *jsObject) any {
	if spec.setCurrentTaskId != nil {
		return *spec.setCurrentTaskId
	}
	st := gstr(updatedTask, "status")
	if (st == "complete" || st == "skipped") && current == spec.taskId {
		return undef{}
	}
	if current == nil {
		return undef{}
	}
	return current
}

type goalPersistedUsage struct {
	goalId        string
	tokensUsed    float64
	activeSeconds float64
}

type goalTurn struct {
	active  bool
	goalId  string
	goal    *jsObject
	archive bool
	ledger  []*jsObject
}

// goalService is GoalService.
type goalService struct {
	ref goalServiceRef
	// lastPersistedUsage is the usage this session last wrote to disk (or reconciled), the baseline for additive merging.
	lastPersistedUsage *goalPersistedUsage
	// flushError is "" when there is none.
	flushError string
	turnBase   *jsObject
	turn       goalTurn
	// applyOverride replaces apply (tests simulate a rejected mutation: lock contention, a revision conflict).
	applyOverride func(spec goalMutationSpec) (goalMutationOutcome, error)
}

func newGoalService(ref goalServiceRef) *goalService { return &goalService{ref: ref} }

// goalWith is {...goal, key: value}.
func goalWith(g *jsObject, kv ...any) *jsObject {
	n := g.clone()
	for i := 0; i+1 < len(kv); i += 2 {
		n.set(kv[i].(string), kv[i+1])
	}
	return n
}

func goalUsageCopy(g *jsObject) *jsObject {
	if u := g.obj("usage"); u != nil {
		return u.clone()
	}
	return emptyUsage()
}

func goalRevision(g *jsObject) float64 { return gnum(g, "revision") }

func (s *goalService) beginTurn(ctx goalServiceContext, goalId string) {
	if s.turn.active {
		_, _ = s.flushTurn(ctx)
		// Lock contention must not discard the previous transaction; keep it open for the next boundary.
		if s.turn.active {
			return
		}
	}
	s.turn = goalTurn{active: true, goalId: goalId}
	if focused := s.ref.getFocused(); focused != nil {
		s.turnBase = goalWith(focused, "usage", goalUsageCopy(focused))
	} else {
		s.turnBase = nil
	}
	s.flushError = ""
}

// flushTurn flushes the buffered transaction: one lock acquire, one goal-file write with a revision bump, one ledger batch.
// nil when nothing was written. An error is a failed authoritative write (the TS throw).
func (s *goalService) flushTurn(ctx goalServiceContext) (*jsObject, error) {
	if !s.turn.active {
		return nil, nil
	}
	s.flushError = ""
	goal := s.turn.goal
	if goal == nil {
		s.turn.active = false
		return nil, nil
	}
	lock, err := acquireGoalLock(ctx, gstr(goal, "id"), 0, 0)
	if err != nil {
		s.flushError = "Goal storage is locked; pending changes have not been persisted."
		// Another writer holds the lock; keep the buffer so a later turn boundary retries.
		return nil, nil
	}
	defer lock.release()
	freshDisk := s.readFreshDiskGoal(ctx, goal)
	expected := s.turnBase
	if expected == nil {
		expected = goal
	}
	id := gstr(goal, "id")
	if freshDisk == nil || goalRevision(freshDisk) != goalRevision(expected) {
		s.flushError = fmt.Sprintf("Goal %s changed in another process; buffered changes were rejected. Refresh and retry.", id)
		// Reject the speculative transaction, never overwrite another writer.
		s.turn.active = false
		s.turn.goal = nil
		s.turn.ledger = nil
		if freshDisk != nil && s.ref.getFocusedGoalId() == id {
			tokens := max(0, gnum(goal.obj("usage"), "tokensUsed")-gnum(expected.obj("usage"), "tokensUsed"))
			seconds := max(0, gnum(goal.obj("usage"), "activeSeconds")-gnum(expected.obj("usage"), "activeSeconds"))
			fu := freshDisk.obj("usage")
			usage := newObject()
			usage.set("tokensUsed", gnum(fu, "tokensUsed")+tokens)
			usage.set("activeSeconds", gnum(fu, "activeSeconds")+seconds)
			s.ref.setFocused(goalWith(freshDisk, "usage", usage))
			s.trackBaseline(gstr(freshDisk, "id"), freshDisk.obj("usage"))
		} else if freshDisk == nil {
			s.ref.getPool().delete(id)
			if s.ref.getFocusedGoalId() == id {
				s.ref.assignFocusedGoalId("")
				s.ref.onFocusedGoalLost(id, ctx)
			}
		}
		s.ref.onDiagnostic(goalDiagnostic{severity: "warning", source: "storage", goalId: id, message: s.flushError})
		return nil, nil
	}
	next := goalWith(goal, "revision", goalRevision(freshDisk)+1)
	var written *jsObject
	if s.turn.archive || gstr(next, "status") == "complete" {
		written, err = ctx.archiveGoalFile(next)
	} else {
		written, err = ctx.writeActiveGoalFile(next)
	}
	if err != nil {
		return nil, err
	}
	s.turn.active = false
	s.appendLedgerEventsBestEffort(ctx, s.turn.ledger)
	s.trackBaseline(gstr(written, "id"), written.obj("usage"))
	if s.ref.getFocusedGoalId() == gstr(written, "id") {
		s.ref.setFocused(written)
	} else {
		s.ref.getPool().set(gstr(written, "id"), written)
	}
	return written, nil
}

// flushForAudit: completion must never audit state that failed to flush. Returns the flush error text ("" = none).
func (s *goalService) flushForAudit(ctx goalServiceContext) (string, error) {
	if _, err := s.flushTurn(ctx); err != nil {
		return "", err
	}
	return s.flushError, nil
}

func (s *goalService) endTurn(ctx goalServiceContext) { _, _ = s.flushTurn(ctx) }

func (s *goalService) isTurnBuffered() bool { return s.turn.active }

func (s *goalService) trackBaseline(goalId string, usage *jsObject) {
	s.lastPersistedUsage = &goalPersistedUsage{goalId: goalId, tokensUsed: gnum(usage, "tokensUsed"), activeSeconds: gnum(usage, "activeSeconds")}
}

// usageDelta is the session-local usage delta since the last baseline, clamped at zero.
func (s *goalService) usageDelta(current, disk *jsObject) (tokens, seconds float64) {
	base := goalPersistedUsage{goalId: gstr(current, "id"), tokensUsed: gnum(disk.obj("usage"), "tokensUsed"), activeSeconds: gnum(disk.obj("usage"), "activeSeconds")}
	if s.lastPersistedUsage != nil && s.lastPersistedUsage.goalId == gstr(current, "id") {
		base = *s.lastPersistedUsage
	}
	cu := current.obj("usage")
	return max(0, gnum(cu, "tokensUsed")-base.tokensUsed), max(0, gnum(cu, "activeSeconds")-base.activeSeconds)
}

// appendLedgerEventsBestEffort appends a batch, retrying singly so every failure stays observable.
func (s *goalService) appendLedgerEventsBestEffort(ctx goalServiceContext, events []*jsObject) {
	if len(events) == 0 {
		return
	}
	if len(events) > 1 && appendGoalEvents(ctx, events).ok {
		return
	}
	for _, event := range events {
		if res := appendGoalEvent(ctx, event); !res.ok {
			typ := gstr(event, "type")
			d := goalDiagnostic{severity: "warning", source: "ledger", eventType: typ}
			suffix := ""
			if _, has := event.vals["goalId"]; has {
				d.goalId = gstr(event, "goalId")
				suffix = fmt.Sprintf(" (goal %s)", d.goalId)
			}
			d.message = fmt.Sprintf("Ledger append failed for %s%s: %v", typ, suffix, res.err)
			s.ref.onDiagnostic(d)
		}
	}
}

// reconcileFocused is the safe focused record reconciliation from disk.
func (s *goalService) reconcileFocused(ctx goalServiceContext, preserveMemoryUsage bool) bool {
	current := s.ref.getFocused()
	source := ctx.readActiveGoalPoolView()
	focused := s.ref.getFocusedGoalId()
	view := s.ref.getPool()
	var buffered *jsObject
	if s.turn.active {
		buffered = s.turn.goal
	}
	unchanged := source.size() == view.size() && (focused == "" || source.has(focused))
	if unchanged {
		for _, id := range source.ids {
			goal := source.get(id)
			want := goal
			if buffered != nil && gstr(buffered, "id") == id {
				want = buffered
			}
			if view.get(id) != want {
				unchanged = false
				break
			}
		}
	}
	if unchanged {
		if focused != "" {
			if same := view.get(focused); same != nil {
				s.ref.onReconciled(same)
			}
		}
		return true
	}
	fresh := source.copy()
	// Overlay the buffered goal so in-turn mutations are visible without a disk round trip.
	if s.turn.active && s.turn.goal != nil {
		fresh.set(gstr(s.turn.goal, "id"), s.turn.goal)
	}
	focusedGoalId := s.ref.getFocusedGoalId()
	if focusedGoalId == "" {
		s.ref.replacePool(fresh)
		return true
	}
	diskGoal := fresh.get(focusedGoalId)
	if diskGoal == nil {
		if current != nil && !current.has("activePath") {
			s.ref.replacePool(fresh)
			fresh.set(gstr(current, "id"), current)
			s.ref.assignFocusedGoalId(gstr(current, "id"))
			return true
		}
		lost := ""
		if current != nil {
			lost = gstr(current, "id")
		}
		s.ref.replacePool(fresh)
		s.ref.assignFocusedGoalId("")
		s.ref.onFocusedGoalLost(lost, ctx)
		return false
	}
	reconciled := diskGoal
	if current != nil && preserveMemoryUsage {
		reconciled = mergeFocusedGoalWithDisk(current, diskGoal)
	}
	s.ref.replacePool(fresh)
	fresh.set(gstr(reconciled, "id"), reconciled)
	s.ref.assignFocusedGoalId(gstr(reconciled, "id"))
	s.ref.onReconciled(reconciled)
	s.trackBaseline(gstr(reconciled, "id"), reconciled.obj("usage"))
	return true
}

// readFreshDiskGoal reads the goal's active file directly (no complete-status filter); nil when gone or without activePath.
func (s *goalService) readFreshDiskGoal(ctx goalServiceContext, current *jsObject) *jsObject {
	p, ok := current.str("activePath")
	if !ok || p == "" {
		return nil
	}
	file, err := ctx.resolveGoalPath(goalsDir, p)
	if err != nil {
		return nil
	}
	return parseGoalFile(file)
}

func goalStaleTokenMessage(token goalFocusToken) string {
	return fmt.Sprintf("Mutation cancelled because goal %s is no longer focused in this session. The shared goal was not modified.", token.goalId)
}

// apply is the single ordered mutation pipeline for a focused goal.
func (s *goalService) apply(ctx goalServiceContext, spec goalMutationSpec) (goalMutationOutcome, error) {
	if s.applyOverride != nil {
		return s.applyOverride(spec)
	}
	if (spec.reconcile == nil || *spec.reconcile) && !s.reconcileFocused(ctx, false) {
		return goalMutationFail("The focused goal was lost during reconciliation; the mutation was not applied."), nil
	}
	current := s.ref.getFocused()
	if current == nil {
		return goalMutationFail("No focused goal to mutate."), nil
	}
	curId := gstr(current, "id")
	if spec.expectedGoalId != nil && curId != *spec.expectedGoalId {
		return goalMutationFail(fmt.Sprintf("Mutation rejected: expected goal %s but the focused goal is %s.", *spec.expectedGoalId, curId)), nil
	}
	if spec.focusToken != nil && !s.ref.isTokenCurrent(*spec.focusToken) {
		return goalMutationFail(goalStaleTokenMessage(*spec.focusToken)), nil
	}
	commitFocused := spec.commitFocused == nil || *spec.commitFocused

	if s.turn.active {
		if s.turn.goalId != "" && curId != s.turn.goalId {
			// Focus changed mid-turn: persist the previous buffer first.
			if _, err := s.flushTurn(ctx); err != nil {
				return goalMutationOutcome{}, err
			}
			if s.turn.active {
				msg := s.flushError
				if msg == "" {
					msg = "Previous goal changes are still awaiting persistence."
				}
				return goalMutationFail(msg), nil
			}
			s.turn = goalTurn{active: true, goalId: curId, goal: current}
			s.turnBase = current
		}
		if s.turn.goal == nil {
			s.turn.goal = current
			s.turn.goalId = curId
			if s.turnBase == nil || gstr(s.turnBase, "id") != curId {
				s.turnBase = goalWith(current, "usage", goalUsageCopy(current))
			}
		}
		mutated := sanitizeGoalPaths(ctx, goalWith(spec.mutate(cloneGoal(current)), "revision", goalRevision(current)+1))
		if spec.ledgerFn != nil || spec.ledger != nil {
			if events, ok := goalSpecEvents(spec, mutated); ok {
				s.turn.ledger = append(s.turn.ledger, events...)
			}
		}
		s.turn.goal = mutated
		s.turn.archive = spec.archive
		previousGoalId := curId
		if commitFocused {
			s.ref.setFocused(mutated)
			s.trackBaseline(gstr(mutated, "id"), mutated.obj("usage"))
		}
		goalId := s.ref.getFocusedGoalId()
		if commitFocused {
			goalId = gstr(mutated, "id")
		}
		focusChanged := commitFocused && previousGoalId != gstr(mutated, "id")
		if focusChanged {
			s.ref.onFocusChanged(previousGoalId, gstr(mutated, "id"))
		}
		return goalMutationOutcome{ok: true, goal: mutated, previousGoalId: previousGoalId, goalId: goalId, focusChanged: focusChanged}, nil
	}

	// Exclusive per-goal lock + optimistic revision check.
	capturedRevision := goalRevision(current)
	lock, err := acquireGoalLock(ctx, curId, 0, 0)
	if err != nil {
		return goalMutationOutcome{}, err
	}
	defer lock.release()
	freshDisk := s.readFreshDiskGoal(ctx, current)
	if freshDisk == nil {
		return goalMutationFail(fmt.Sprintf("Goal %s was deleted or archived by another process while this mutation was in progress; the mutation was not applied.", curId)), nil
	}
	diskRevision := goalRevision(freshDisk)
	if diskRevision != capturedRevision {
		return goalMutationFail(fmt.Sprintf("Goal %s was modified by another process (revision %s -> %s); current revision is %s. Refresh and retry; the mutation was not applied.", curId, jsNumber(capturedRevision), jsNumber(diskRevision), jsNumber(diskRevision))), nil
	}
	base := current
	if spec.refreshFromDisk {
		base = ctx.mergeGoalPromptFromDisk(current)
	}
	mutated := goalWith(spec.mutate(cloneGoal(base)), "revision", capturedRevision+1)
	var written *jsObject
	if spec.archive {
		written, err = ctx.archiveGoalFile(mutated)
	} else {
		written, err = ctx.writeActiveGoalFile(mutated)
	}
	if err != nil {
		return goalMutationOutcome{}, err
	}
	if spec.ledgerFn != nil || spec.ledger != nil {
		events, _ := goalSpecEvents(spec, written)
		s.appendLedgerEventsBestEffort(ctx, events)
	}
	previousGoalId := curId
	if commitFocused {
		s.ref.setFocused(written)
		s.trackBaseline(gstr(written, "id"), written.obj("usage"))
	}
	goalId := s.ref.getFocusedGoalId()
	if commitFocused {
		goalId = gstr(written, "id")
	}
	focusChanged := commitFocused && previousGoalId != gstr(written, "id")
	if focusChanged {
		s.ref.onFocusChanged(previousGoalId, gstr(written, "id"))
	}
	return goalMutationOutcome{ok: true, goal: written, previousGoalId: previousGoalId, goalId: goalId, focusChanged: focusChanged}, nil
}

// goalSpecEvents evaluates the ledger spec; ok=false when the factory failed (nothing appended).
func goalSpecEvents(spec goalMutationSpec, written *jsObject) ([]*jsObject, bool) {
	if spec.ledgerFn != nil {
		events, err := spec.ledgerFn(written)
		return events, err == nil
	}
	return spec.ledger, true
}

// updateTask is the disk-fresh single-task transaction.
func (s *goalService) updateTask(ctx goalServiceContext, spec goalTaskUpdateSpec) (goalTaskUpdateOutcome, error) {
	if s.turn.active {
		current := s.turn.goal
		if current == nil {
			current = s.ref.getFocused()
		}
		if current == nil {
			return goalTaskFail("No focused goal to mutate."), nil
		}
		if spec.focusToken != nil && !s.ref.isTokenCurrent(*spec.focusToken) {
			return goalTaskFail(goalStaleTokenMessage(*spec.focusToken)), nil
		}
		taskList := current.obj("taskList")
		if taskList == nil {
			return goalTaskFail("The goal has no task list."), nil
		}
		tasks, _ := taskList.vals["tasks"].([]any)
		task := findTaskInTree(tasks, spec.taskId)
		if task == nil {
			return goalTaskFail(fmt.Sprintf("Task \"%s\" not found.", spec.taskId)), nil
		}
		if spec.validate != nil {
			if gate := spec.validate(task); !gate.ok {
				return goalTaskFail(gate.message), nil
			}
		}
		updatedTask, gate := spec.update(task)
		if !gate.ok {
			return goalTaskFail(gate.message), nil
		}
		updatedTasks := updateTaskInTree(tasks, spec.taskId, func(*jsObject) *jsObject { return updatedTask })
		mutated := sanitizeGoalPaths(ctx, goalWith(current,
			"taskList", goalWith(taskList, "tasks", updatedTasks),
			"currentTaskId", resolveUpdatedCurrentTaskId(spec, current.vals["currentTaskId"], updatedTask),
			"updatedAt", nowIso(),
			"revision", goalRevision(current)+1))
		if spec.ledger != nil {
			events, err := spec.ledger(mutated, updatedTask)
			if err != nil {
				s.ref.onDiagnostic(goalDiagnostic{severity: "warning", source: "ledger", goalId: spec.taskId, message: fmt.Sprintf("Ledger spec error during task update: Error: %v", err)})
			} else {
				s.turn.ledger = append(s.turn.ledger, events...)
			}
		}
		s.turn.goal = mutated
		s.ref.setFocused(mutated)
		return goalTaskUpdateOutcome{ok: true, goal: mutated, task: updatedTask}, nil
	}
	return s.updateTaskAttempt(ctx, spec, 1)
}

// goalSameTasks reports whether two task arrays are the same array (JS identity).
func goalSameTasks(a, b []any) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return &a[0] == &b[0] && len(a) == len(b)
}

type goalTaskLocation struct {
	tasks []any
	index int
}

// updateTasks is an ordered all-or-nothing batch; each validator sees earlier changes in the clone.
func (s *goalService) updateTasks(ctx goalServiceContext, specs []goalTaskUpdateSpec) (goalMutationOutcome, error) {
	if !s.reconcileFocused(ctx, false) {
		return goalMutationFail("No focused goal to mutate."), nil
	}
	current := s.ref.getFocused()
	if current == nil || current.obj("taskList") == nil {
		return goalMutationFail("The goal has no task list."), nil
	}
	curId := gstr(current, "id")
	base := current
	if !s.turn.active {
		lock, err := acquireGoalLock(ctx, curId, 0, 0)
		if err != nil {
			return goalMutationOutcome{}, err
		}
		defer lock.release()
		fresh := s.readFreshDiskGoal(ctx, current)
		if fresh == nil || goalRevision(fresh) != goalRevision(current) {
			return goalMutationFail("The goal changed in another process; no updates applied. Refresh and retry."), nil
		}
		if gstr(fresh, "status") != "active" || fresh.obj("taskList") == nil {
			return goalMutationFail("The persisted goal is no longer active with tasks; no updates applied."), nil
		}
		base = goalWith(fresh, "usage", current.vals["usage"])
	}
	next := cloneGoal(base)
	locations := map[string]*goalTaskLocation{}
	var indexTasks func(tasks []any)
	indexTasks = func(tasks []any) {
		for i, t := range tasks {
			task := t.(*jsObject)
			locations[gstr(task, "id")] = &goalTaskLocation{tasks: tasks, index: i}
			if subs, ok := task.vals["subtasks"].([]any); ok {
				indexTasks(subs)
			}
		}
	}
	var removeTasks func(tasks []any)
	removeTasks = func(tasks []any) {
		for _, t := range tasks {
			task := t.(*jsObject)
			delete(locations, gstr(task, "id"))
			if subs, ok := task.vals["subtasks"].([]any); ok {
				removeTasks(subs)
			}
		}
	}
	nextList := next.obj("taskList")
	nextTasks, _ := nextList.vals["tasks"].([]any)
	indexTasks(nextTasks)
	var events []*jsObject
	for _, spec := range specs {
		if spec.focusToken != nil && !s.ref.isTokenCurrent(*spec.focusToken) {
			return goalMutationFail("The focused goal changed; no updates applied."), nil
		}
		location := locations[spec.taskId]
		var task *jsObject
		if location != nil {
			task, _ = location.tasks[location.index].(*jsObject)
		}
		if task == nil {
			return goalMutationFail(fmt.Sprintf("Task \"%s\" not found.", spec.taskId)), nil
		}
		if spec.validate != nil {
			if gate := spec.validate(task); !gate.ok {
				return goalMutationFail(gate.message), nil
			}
		}
		updatedTask, gate := spec.update(task)
		if !gate.ok {
			return goalMutationFail(gate.message), nil
		}
		location.tasks[location.index] = updatedTask
		if gstr(updatedTask, "id") != gstr(task, "id") {
			delete(locations, gstr(task, "id"))
			locations[gstr(updatedTask, "id")] = location
		}
		oldSubs, _ := task.vals["subtasks"].([]any)
		newSubs, _ := updatedTask.vals["subtasks"].([]any)
		if !goalSameTasks(oldSubs, newSubs) {
			if oldSubs != nil {
				removeTasks(oldSubs)
			}
			if newSubs != nil {
				indexTasks(newSubs)
			}
		}
		next.set("currentTaskId", resolveUpdatedCurrentTaskId(spec, next.vals["currentTaskId"], updatedTask))
		next.set("updatedAt", nowIso())
		if spec.ledger != nil {
			evs, err := spec.ledger(next, updatedTask)
			if err != nil {
				return goalMutationOutcome{}, err
			}
			events = append(events, evs...)
		}
	}
	if s.turn.active {
		var focusToken *goalFocusToken
		if len(specs) > 0 {
			focusToken = specs[0].focusToken
		}
		return s.apply(ctx, goalMutationSpec{reconcile: new(false), expectedGoalId: &curId, focusToken: focusToken,
			mutate: func(*jsObject) *jsObject { return next }, ledger: events})
	}
	written, err := ctx.writeActiveGoalFile(goalWith(next, "revision", goalRevision(base)+1))
	if err != nil {
		return goalMutationOutcome{}, err
	}
	s.appendLedgerEventsBestEffort(ctx, events)
	s.trackBaseline(gstr(written, "id"), written.obj("usage"))
	s.ref.setFocused(written)
	return goalMutationOutcome{ok: true, goal: written, previousGoalId: curId, goalId: gstr(written, "id")}, nil
}

// updateTaskAttempt: lock + optimistic revision check; a stale writer retries once with the fresh state.
func (s *goalService) updateTaskAttempt(ctx goalServiceContext, spec goalTaskUpdateSpec, retriesLeft int) (goalTaskUpdateOutcome, error) {
	if !s.reconcileFocused(ctx, false) {
		return goalTaskFail("The focused goal was lost during reconciliation; the task was not updated."), nil
	}
	current := s.ref.getFocused()
	if current == nil {
		return goalTaskFail("No focused goal to mutate."), nil
	}
	if spec.focusToken != nil && !s.ref.isTokenCurrent(*spec.focusToken) {
		return goalTaskFail(goalStaleTokenMessage(*spec.focusToken)), nil
	}
	curId := gstr(current, "id")
	capturedRevision := goalRevision(current)
	lock, err := acquireGoalLock(ctx, curId, 0, 0)
	if err != nil {
		return goalTaskUpdateOutcome{}, err
	}
	defer lock.release()
	freshDisk := s.readFreshDiskGoal(ctx, current)
	if freshDisk == nil {
		return goalTaskFail(fmt.Sprintf("Goal %s was deleted or archived by another process while the task update was in progress; the task was not updated.", curId)), nil
	}
	diskRevision := goalRevision(freshDisk)
	if diskRevision != capturedRevision {
		if retriesLeft > 0 {
			// Retry once against the fresh state; the transition validation is the guard.
			return s.updateTaskAttempt(ctx, spec, retriesLeft-1)
		}
		return goalTaskFail(fmt.Sprintf("Goal %s was modified by another process (revision %s -> %s); current revision is %s. The task was not updated.", curId, jsNumber(capturedRevision), jsNumber(diskRevision), jsNumber(diskRevision))), nil
	}
	base := ctx.mergeGoalPromptFromDisk(current)
	taskList := base.obj("taskList")
	if taskList == nil {
		return goalTaskFail("The goal has no task list."), nil
	}
	tasks, _ := taskList.vals["tasks"].([]any)
	task := findTaskInTree(tasks, spec.taskId)
	if task == nil {
		return goalTaskFail(fmt.Sprintf("Task \"%s\" not found.", spec.taskId)), nil
	}
	if spec.validate != nil {
		if gate := spec.validate(task); !gate.ok {
			return goalTaskFail(gate.message), nil
		}
	}
	updatedTask, gate := spec.update(task)
	if !gate.ok {
		return goalTaskFail(gate.message), nil
	}
	updatedTasks := updateTaskInTree(tasks, spec.taskId, func(*jsObject) *jsObject { return updatedTask })
	mutated := goalWith(base,
		"taskList", goalWith(taskList, "tasks", updatedTasks),
		"currentTaskId", resolveUpdatedCurrentTaskId(spec, base.vals["currentTaskId"], updatedTask),
		"updatedAt", nowIso(),
		"revision", capturedRevision+1)
	written, err := ctx.writeActiveGoalFile(mutated)
	if err != nil {
		return goalTaskUpdateOutcome{}, err
	}
	if spec.ledger != nil {
		events, lerr := spec.ledger(written, updatedTask)
		if lerr != nil {
			// A ledger-spec error after the authoritative write keeps the successful state transition.
			s.ref.onDiagnostic(goalDiagnostic{severity: "warning", source: "ledger", goalId: spec.taskId, message: fmt.Sprintf("Ledger spec error during task update: Error: %v", lerr)})
		} else {
			s.appendLedgerEventsBestEffort(ctx, events)
		}
	}
	s.ref.setFocused(written)
	return goalTaskUpdateOutcome{ok: true, goal: written, task: updatedTask}, nil
}

// persist writes the focused goal: bumps updatedAt, merges the objective from disk. On a revision conflict only the
// session's additive usage delta since the last baseline is merged onto the disk record. nil = nothing written.
func (s *goalService) persist(ctx goalServiceContext) (*jsObject, error) {
	current := s.ref.getFocused()
	if current == nil {
		return nil, nil
	}
	if s.turn.active {
		next := goalWith(current, "updatedAt", nowIso())
		s.turn.goal = next
		s.ref.setFocused(next)
		return next, nil
	}
	capturedRevision := goalRevision(current)
	lock, err := acquireGoalLock(ctx, gstr(current, "id"), 4, 25) // 100ms bound
	if err != nil {
		// Another writer holds the goal lock; skip this persist tick.
		return nil, nil
	}
	defer lock.release()
	freshDisk := s.readFreshDiskGoal(ctx, current)
	if freshDisk == nil {
		return nil, nil
	}
	var merged *jsObject
	if goalRevision(freshDisk) != capturedRevision {
		tokens, seconds := s.usageDelta(current, freshDisk)
		if tokens == 0 && seconds == 0 {
			return nil, nil
		}
		fu := freshDisk.obj("usage")
		usage := newObject()
		usage.set("tokensUsed", gnum(fu, "tokensUsed")+tokens)
		usage.set("activeSeconds", gnum(fu, "activeSeconds")+seconds)
		merged = ctx.mergeGoalPromptFromDisk(goalWith(freshDisk, "usage", usage, "updatedAt", nowIso(), "revision", goalRevision(freshDisk)+1))
	} else {
		merged = ctx.mergeGoalPromptFromDisk(goalWith(current, "updatedAt", nowIso(), "revision", capturedRevision+1))
	}
	var written *jsObject
	if gstr(merged, "status") == "complete" {
		written, err = ctx.archiveGoalFile(merged)
	} else {
		written, err = ctx.writeActiveGoalFile(merged)
	}
	if err != nil {
		return nil, err
	}
	s.trackBaseline(gstr(written, "id"), written.obj("usage"))
	s.ref.setFocused(written)
	return written, nil
}

// create writes the active file, appends the ledger and commits focus.
func (s *goalService) create(ctx goalServiceContext, goal *jsObject, ledger []*jsObject) (goalMutationOutcome, error) {
	previousGoalId := ""
	if f := s.ref.getFocused(); f != nil {
		previousGoalId = gstr(f, "id")
	}
	written, err := ctx.writeActiveGoalFile(goal)
	if err != nil {
		return goalMutationOutcome{}, err
	}
	if len(ledger) > 0 {
		s.appendLedgerEventsBestEffort(ctx, ledger)
	}
	s.ref.setFocused(written)
	s.trackBaseline(gstr(written, "id"), written.obj("usage"))
	id := gstr(written, "id")
	focusChanged := previousGoalId != id
	if focusChanged {
		s.ref.onFocusChanged(previousGoalId, id)
	}
	return goalMutationOutcome{ok: true, goal: written, previousGoalId: previousGoalId, goalId: id, focusChanged: focusChanged}, nil
}

// appendEvents appends ledger events best-effort; within a turn they join the transaction's batch.
func (s *goalService) appendEvents(ctx goalServiceContext, events []*jsObject) {
	if s.turn.active {
		s.turn.ledger = append(s.turn.ledger, events...)
		return
	}
	s.appendLedgerEventsBestEffort(ctx, events)
}

const goalDebugDir = ".pi/goals/debug"

// writeDebugFile is the diagnostic write for the debug widget toggle (not a goal mutation).
func (s *goalService) writeDebugFile(ctx goalServiceContext, relPath, content string) error {
	if err := ctx.ensureDir(goalDebugDir, false); err != nil {
		return err
	}
	return ctx.atomicWrite(goalDebugDir, relPath, content)
}

// removeDebugFile is best-effort.
func (s *goalService) removeDebugFile(ctx goalServiceContext, relPath string) {
	_ = ctx.safeUnlink(goalDebugDir, relPath)
}
