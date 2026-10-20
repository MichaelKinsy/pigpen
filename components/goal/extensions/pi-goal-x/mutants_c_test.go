package pi_goal_x

// Mutation-survivor tests (lane C). Plain extra tests, not twins: every expectation comes from the original's code
// (port/oracle/extensions/*.ts), not from the Go port.

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mcLines(text string) []string { return strings.Split(text, "\n") }

func mcHas(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("expected %q in:\n%s", want, text)
	}
}

func mcNot(t *testing.T, text, bad string) {
	t.Helper()
	if strings.Contains(text, bad) {
		t.Errorf("did not expect %q in:\n%s", bad, text)
	}
}

func mcHasLine(t *testing.T, text, want string) {
	t.Helper()
	for _, l := range mcLines(text) {
		if l == want {
			return
		}
	}
	t.Errorf("expected line %q in:\n%s", want, text)
}

func TestMutantsCStatusStandard(t *testing.T) {
	// GOAL_STATUS_WIDTH = 78: the compact dashboard box is exactly 78 columns wide.
	text := buildGoalStatusText(gsBase())
	first := mcLines(text)[0]
	if w := visibleWidth(first); w != 78 {
		t.Errorf("status box width = %d, want 78: %q", w, first)
	}
	// footerHint
	mcHas(t, text, "╰─ Run /goal-status verbose for full deta")
	// parts joined by a blank line
	mcHas(t, text, "\n\n├─ Current task ")
	mcHas(t, text, "\n\n╭─ Audit result ─ CHANGES REQUIRED ")
	// the audit block carries the date and the report, whitespace-collapsed
	events := gsEvents()
	events[3].set("report", "Line one.\n\n   Line two. "+strings.Repeat("x", 120))
	o := gsBase()
	o.ledgerEvents = events
	o.width = new(200)
	text = buildGoalStatusText(o)
	var audit string
	for _, l := range mcLines(text) {
		if strings.HasPrefix(l, "│ Last audit:") {
			audit = l
		}
	}
	want := "Last audit: CHANGES REQUIRED (2026-01-02) — Line one. Line two. " + strings.Repeat("x", 90-len("Line one. Line two. ")-3) + "..."
	if !strings.Contains(audit, want) {
		t.Errorf("audit line = %q, want it to contain %q", audit, want)
	}
}

func TestMutantsCStatusHealth(t *testing.T) {
	present, absent := true, false
	// not focused is a warning
	text := buildGoalStatusText(goalStatusTextOptions{goal: dmGoal(), focused: false, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "WARN Focus: not focused")
	mcHas(t, text, "Goal health: WARN")
	// focused is ok; lifecycle reports auto-continue on / off
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal(), focused: true, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "OK Focus: focused")
	mcHasLine(t, text, "OK Lifecycle: active · auto-continue on")
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal("autoContinue", false), focused: true, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "OK Lifecycle: active · auto-continue off")
	// an unknown active-file presence (nil) warns and shows the plain path
	g := dmGoal("activePath", ".pi/goals/active_goal_g1.md")
	text = buildGoalStatusText(goalStatusTextOptions{goal: g, focused: true, health: true})
	mcHasLine(t, text, "WARN Goal file: .pi/goals/active_goal_g1.md")
	mcHas(t, text, "Goal health: WARN")
	text = buildGoalStatusText(goalStatusTextOptions{goal: g, focused: true, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "OK Goal file: .pi/goals/active_goal_g1.md")
	text = buildGoalStatusText(goalStatusTextOptions{goal: g, focused: true, health: true, activeFilePresent: &absent})
	mcHasLine(t, text, "ERROR Goal file: missing · .pi/goals/active_goal_g1.md")
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal(), focused: true, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "OK Goal file: missing active path")
	// tasks: one pending task with a contract is "1 contracted"; blockCompletion=false stays ok even with pending tasks
	tasks := dmTasks(
		dmTask("t1", "A", "verificationContract", "  run it  "),
		dmTask("t2", "B", "verificationContract", "   "),
		dmTask("t3", "C", "status", "skipped", "verificationContract", "x"),
	)
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal(dmWithTasks(tasks)...), focused: true, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "OK Tasks: 1/3 terminal · 2 pending · 1 contracted")
	// blockCompletion with pending warns
	list := dmObj("tasks", tasks, "blockCompletion", true, "proposedAt", "2026-01-01T00:00:00.000Z")
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal("taskList", list), focused: true, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "WARN Tasks: 1/3 terminal · 2 pending · 1 contracted")
	// no contracted tasks: no suffix
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal(dmWithTasks(dmTasks(dmTask("t1", "A")))...), focused: true, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "OK Tasks: 0/1 terminal · 1 pending")
	// a negative token count shows as 0% (Math.max(0, percentage)) and as 0 tokens
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal("tokenBudget", 1000.0, "usage", dmUsage(-100, 0)), focused: true, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "OK Budget: 0 / 1K (0%)")
	// the objective is truncated to max(20, width-12)
	long := strings.Repeat("o", 100)
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal("objective", long), focused: true, health: true, activeFilePresent: &present})
	mcHasLine(t, text, "Goal: "+strings.Repeat("o", 63)+"...")
	// checkpoint report only when total > 0 (a single checkpoint counts)
	sf := ""
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal(), focused: true, health: true, activeFilePresent: &present, checkpointHealth: &checkpointHealth{total: 1}, checkpointSessionFile: &sf})
	mcHas(t, text, "Session checkpoints:\n  total: 1\n")
	text = buildGoalStatusText(goalStatusTextOptions{goal: dmGoal(), focused: true, health: true, activeFilePresent: &present, checkpointHealth: &checkpointHealth{}, checkpointSessionFile: &sf})
	mcNot(t, text, "Session checkpoints:")
}

func TestMutantsCStatusVerbose(t *testing.T) {
	o := gsBase()
	o.verbose = true
	// a pending task's evidence is not shown in the tree (only complete tasks show evidence)
	tasks := gsFiveTasks()
	tasks[3].(*jsObject).set("evidence", "stale evidence")
	o.goal = gsWithTasks(tasks, "currentTaskId", "t3")
	text := buildGoalStatusText(o)
	mcHas(t, text, "· t4  Add documentation\n")
	mcNot(t, text, "stale evidence")
	mcHas(t, text, "✓ t1  Review reports page and data source — evidence: Reviewed source")
	// ledger lines carry the timestamp to the second
	mcHas(t, text, "Recent ledger:\n  2026-01-01T09:00:00 goal_created\n")
	mcHas(t, text, "\n  2026-01-02T10:00:00 audit_result")
}

// ---- Probe extensions for the external (rig) mutation tests ----

func (p *Probe) McRunning() string {
	var id string
	p.c.locked(func() {
		if p.c.runningGoalID != nil {
			id = *p.c.runningGoalID
		}
	})
	return id
}

func (p *Probe) McSetRunning(id string) { p.c.locked(func() { p.c.runningGoalID = &id }) }

func (p *Probe) McCheckpoint() string {
	var id string
	p.c.locked(func() {
		if g := p.c.runtime.getCheckpointGoalId(); g != nil {
			id = *g
		}
	})
	return id
}

func (p *Probe) McSetCheckpoint(id string) { p.c.locked(func() { p.c.runtime.setCheckpoint(&id) }) }

func (p *Probe) McPostCompact() bool {
	var v bool
	p.c.locked(func() { v = p.c.runtime.postCompactReminderPending })
	return v
}

func (p *Probe) McSetPostCompact(v bool) {
	p.c.locked(func() { p.c.runtime.postCompactReminderPending = v })
}

func (p *Probe) McAccountingActive(id string) bool {
	var v bool
	p.c.locked(func() { v = p.c.accounting.isActiveFor(id) })
	return v
}

// ---- goal-refresh ----

func TestMutantsCRefreshDiffSorted(t *testing.T) {
	ids := []string{"g8", "g3", "g6", "g1", "g7", "g2", "g5", "g4"}
	got := diffGoalRefreshState(goalRefreshState{poolIds: ids}, goalRefreshState{})
	want := []string{"pool: 8 goal(s) removed — g1, g2, g3, g4, g5, g6, g7, g8"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("removed = %q, want %q", got, want)
	}
	got = diffGoalRefreshState(goalRefreshState{}, goalRefreshState{poolIds: ids})
	want = []string{"pool: 8 goal(s) added — g1, g2, g3, g4, g5, g6, g7, g8"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("added = %q, want %q", got, want)
	}
}

// ---- goal service (goal-service.ts) ----

func mcSvcID(f *gsFixture) string { return gstr(f.written, "id") }

func mcSvcBuffer(t *testing.T, f *gsFixture, mutate func(*jsObject) *jsObject, events ...*jsObject) {
	t.Helper()
	f.service.beginTurn(f.ctx, mcSvcID(f))
	res := gsMust(f.service.apply(f.ctx, goalMutationSpec{mutate: mutate, ledger: events}))
	if !res.ok {
		t.Fatalf("buffered apply: %s", res.message)
	}
}

func mcSvcSetUsage(tokens, seconds float64) func(*jsObject) *jsObject {
	return func(g *jsObject) *jsObject {
		u := newObject()
		u.set("tokensUsed", tokens)
		u.set("activeSeconds", seconds)
		return goalWith(g, "usage", u)
	}
}

func mcSvcUsage(g *jsObject) (float64, float64) {
	u := g.obj("usage")
	return gnum(u, "tokensUsed"), gnum(u, "activeSeconds")
}

func TestMutantsCServiceFlushLocked(t *testing.T) {
	f := gsNewFixture(t)
	id := mcSvcID(f)
	mcSvcBuffer(t, f, gsSetObjective("Buffered work"))
	lock := gsMust(acquireGoalLock(f.ctx, id, 0, 0))
	msg, err := f.service.flushForAudit(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, msg, "Goal storage is locked; pending changes have not been persisted.", "flush error under contention")
	eq(t, f.service.isTurnBuffered(), true, "the buffer is kept for the next boundary")
	lock.release()
	msg, _ = f.service.flushForAudit(f.ctx)
	eq(t, msg, "", "flush error after the lock is released")
	eq(t, f.service.isTurnBuffered(), false, "flushed")
}

func TestMutantsCServiceFlushRejected(t *testing.T) {
	f := gsNewFixture(t)
	id := mcSvcID(f)
	event := ledgerEvent("goal_paused", "goalId", id, "reason", "x", "status", "paused", "at", nowIso())
	mcSvcBuffer(t, f, mcSvcSetUsage(50, 7), event)
	bSvc, _, _ := f.secondWriter()
	res := gsMust(bSvc.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: mcSvcSetUsage(100, 20)}))
	eq(t, res.ok, true, "the other writer lands")
	_, err := f.service.flushTurn(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "Goal " + id + " changed in another process; buffered changes were rejected. Refresh and retry."
	eq(t, f.service.flushError, want, "flush error")
	eq(t, f.service.turn.active, false, "turn closed")
	eq(t, f.service.turn.goal == nil, true, "buffered goal dropped")
	eq(t, f.service.turn.ledger == nil, true, "buffered ledger dropped")
	if len(f.ledgerEvents()) != 0 {
		t.Error("the rejected ledger events are not written")
	}
	// the session's own usage delta is folded onto the winner's record
	tokens, seconds := mcSvcUsage(f.ref.getFocused())
	eq(t, tokens, 150.0, "tokens: disk 100 + session delta 50")
	eq(t, seconds, 27.0, "seconds: disk 20 + session delta 7")
	last := f.log.diagnostics[len(f.log.diagnostics)-1]
	eq(t, last, goalDiagnostic{severity: "warning", source: "storage", goalId: id, message: want}, "diagnostic")
}

func TestMutantsCServiceFlushRejectedClampsDelta(t *testing.T) {
	f := gsNewFixture(t)
	id := mcSvcID(f)
	// the goal starts the turn with 40 tokens recorded; the buffer lowers it (a negative session delta must not subtract)
	gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: mcSvcSetUsage(40, 30)}))
	mcSvcBuffer(t, f, mcSvcSetUsage(10, 5))
	bSvc, _, _ := f.secondWriter()
	res := gsMust(bSvc.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: mcSvcSetUsage(100, 60)}))
	eq(t, res.ok, true, "the other writer lands")
	gsMust(f.service.flushTurn(f.ctx))
	eq(t, f.service.flushError != "", true, "rejected")
	tokens, seconds := mcSvcUsage(f.ref.getFocused())
	eq(t, tokens, 100.0, "negative token delta clamps to 0")
	eq(t, seconds, 60.0, "negative seconds delta clamps to 0")
	_ = id
}

func TestMutantsCServiceFlushDeletedGoal(t *testing.T) {
	f := gsNewFixture(t)
	id := mcSvcID(f)
	mcSvcBuffer(t, f, gsSetObjective("Buffered work"))
	if err := os.Remove(f.activePath(0)); err != nil {
		t.Fatal(err)
	}
	gsMust(f.service.flushTurn(f.ctx))
	eq(t, f.ref.getPool().has(id), false, "the deleted goal leaves the pool")
	eq(t, f.ref.getFocusedGoalId(), "", "focus is dropped")
	eq(t, f.log.goalLost, []string{id}, "the focused-goal-lost hook ran")
}

func TestMutantsCServiceFlushArchivesCompleteGoal(t *testing.T) {
	f := gsNewFixture(t)
	mcSvcBuffer(t, f, func(g *jsObject) *jsObject { return goalWith(g, "status", "complete", "stopReason", "user") })
	eq(t, len(f.activeFiles()), 1, "still buffered")
	gsMust(f.service.flushTurn(f.ctx))
	eq(t, len(f.activeFiles()), 0, "a complete goal is archived when flushed")
	entries, _ := os.ReadDir(filepath.Join(f.cwd, ".pi", "goals", "archived"))
	eq(t, len(entries), 1, "archived file")
}

func TestMutantsCServiceBeginTurn(t *testing.T) {
	f := gsNewFixture(t)
	id := mcSvcID(f)
	mcSvcBuffer(t, f, gsSetObjective("First turn work"))
	f.service.beginTurn(f.ctx, id)
	// beginning the next turn flushes the previous transaction first
	disk := parseGoalFile(f.activePath(0))
	eq(t, gstr(disk, "objective"), "First turn work", "the previous turn was persisted")
	eq(t, goalRevision(disk), 1.0, "one revision bump")
	eq(t, f.service.turn.active, true, "a new turn is open")
	eq(t, f.service.turn.goal == nil, true, "with an empty buffer")
	// the turn base owns its usage object
	f.ref.getFocused().obj("usage").set("tokensUsed", 7.0)
	eq(t, gnum(f.service.turnBase.obj("usage"), "tokensUsed"), 0.0, "turn base usage is a copy")
}

func TestMutantsCServiceUsageDelta(t *testing.T) {
	f := gsNewFixture(t)
	cur, disk := newObject(), newObject()
	cur.set("id", "g1")
	cur.set("usage", dmUsage(5, 3))
	disk.set("usage", dmUsage(10, 8))
	tokens, seconds := f.service.usageDelta(cur, disk)
	eq(t, tokens, 0.0, "tokens are clamped at zero")
	eq(t, seconds, 0.0, "seconds are clamped at zero")
	// a baseline of another goal is ignored
	f.service.lastPersistedUsage = &goalPersistedUsage{goalId: "other", tokensUsed: 1000, activeSeconds: 1000}
	cur.set("usage", dmUsage(50, 30))
	tokens, seconds = f.service.usageDelta(cur, disk)
	eq(t, tokens, 40.0, "delta against the disk record")
	eq(t, seconds, 22.0, "seconds against the disk record")
	// this goal's own baseline wins
	f.service.lastPersistedUsage = &goalPersistedUsage{goalId: "g1", tokensUsed: 45, activeSeconds: 29}
	tokens, seconds = f.service.usageDelta(cur, disk)
	eq(t, tokens, 5.0, "delta against the session baseline")
	eq(t, seconds, 1.0, "seconds against the session baseline")
}

// mcSvcExternalEdit rewrites the goal's active file behind the service's back (the pool cache is warmed first, so it stays stale).
func mcSvcExternalEdit(t *testing.T, f *gsFixture, edit func(raw string) string) {
	t.Helper()
	f.ctx.readActiveGoalPool()
	raw := gsReadFile(t, f.activePath(0))
	next := edit(raw)
	if next == raw {
		t.Fatal("the external edit changed nothing")
	}
	if err := os.WriteFile(f.activePath(0), []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMutantsCServiceLedgerFailures(t *testing.T) {
	f := gsNewFixture(t)
	id := mcSvcID(f)
	// an empty batch writes nothing, not even an empty ledger file
	eq(t, appendGoalEvents(f.ctx, nil).ok, true, "empty batch is ok")
	if _, err := os.Stat(goalLedgerPath(f.ctx)); err == nil {
		t.Error("an empty batch must not create the ledger")
	}
	// a ledger that cannot be appended to: every event is reported, with its goal when it has one
	if err := os.MkdirAll(goalLedgerPath(f.ctx), 0o755); err != nil {
		t.Fatal(err)
	}
	f.service.appendEvents(f.ctx, []*jsObject{
		ledgerEvent("goal_paused", "goalId", id, "reason", "x", "status", "paused", "at", nowIso()),
		ledgerEvent("goal_resumed", "reason", "y", "at", nowIso()),
	})
	eq(t, len(f.log.diagnostics), 2, "one diagnostic per event")
	d0, d1 := f.log.diagnostics[0], f.log.diagnostics[1]
	eq(t, d0.goalId, id, "goal id of the first")
	eq(t, d0.eventType, "goal_paused", "event type of the first")
	eq(t, strings.HasPrefix(d0.message, "Ledger append failed for goal_paused (goal "+id+"): "), true, "first message: "+d0.message)
	eq(t, d1.goalId, "", "no goal id on the second")
	eq(t, strings.HasPrefix(d1.message, "Ledger append failed for goal_resumed: "), true, "second message: "+d1.message)
}

func TestMutantsCServiceReconcile(t *testing.T) {
	// a pool entry the disk does not have is dropped even when everything else matches (the sizes differ)
	f := gsNewFixture(t)
	f.ref.replacePool(f.ctx.readActiveGoalPoolView().copy()) // the same goal objects as the disk view: nothing differs yet
	eq(t, f.service.reconcileFocused(f.ctx, false), true, "unchanged pool")
	ghost := cloneGoal(f.written)
	ghost.set("id", "ghost")
	f.ref.pool.set("ghost", ghost)
	eq(t, f.service.reconcileFocused(f.ctx, false), true, "reconcile succeeds")
	eq(t, f.ref.getPool().has("ghost"), false, "ghost goal dropped")

	// the buffered goal of the open turn is overlaid on the fresh pool
	f = gsNewFixture(t)
	mcSvcBuffer(t, f, gsSetObjective("Buffered work"))
	gsMust(f.ctx.writeActiveGoalFile(createGoal("Another goal", false, false, false, clockMs())))
	eq(t, f.service.reconcileFocused(f.ctx, false), true, "reconcile succeeds")
	eq(t, gstr(f.ref.getFocused(), "objective"), "Buffered work", "buffered objective survives the reload")

	// a focused goal that left the disk is lost: the hook runs and reconcile reports it
	f = gsNewFixture(t)
	id := mcSvcID(f)
	if err := os.Remove(f.activePath(0)); err != nil {
		t.Fatal(err)
	}
	invalidateGoalPoolCache()
	eq(t, f.service.reconcileFocused(f.ctx, false), false, "a lost focused goal is reported")
	eq(t, f.ref.getFocusedGoalId(), "", "focus cleared")
	eq(t, f.log.goalLost, []string{id}, "focused-goal-lost hook")
}

func TestMutantsCServiceApplyBuffered(t *testing.T) {
	// the archive request of a buffered mutation is remembered until the flush
	f := gsNewFixture(t)
	f.service.beginTurn(f.ctx, mcSvcID(f))
	gsMust(f.service.apply(f.ctx, goalMutationSpec{archive: true, mutate: gsSetObjective("Archive me")}))
	eq(t, f.service.turn.archive, true, "archive flag buffered")
	gsMust(f.service.flushTurn(f.ctx))
	eq(t, len(f.activeFiles()), 0, "archived on flush")

	// a failing ledger factory contributes nothing, even when it returns events with its error
	f = gsNewFixture(t)
	id := mcSvcID(f)
	f.service.beginTurn(f.ctx, id)
	res := gsMust(f.service.apply(f.ctx, goalMutationSpec{mutate: gsSetObjective("x"), ledgerFn: func(*jsObject) ([]*jsObject, error) {
		return []*jsObject{ledgerEvent("goal_paused", "goalId", id, "reason", "x", "at", nowIso())}, os.ErrInvalid
	}}))
	eq(t, res.ok, true, "apply succeeds")
	eq(t, len(f.service.turn.ledger), 0, "the factory error drops its events")
}

func TestMutantsCServiceApplyDirect(t *testing.T) {
	// the objective is re-read from disk when the spec asks for a refresh
	f := gsNewFixture(t)
	f.ctx.readActiveGoalPool()
	raw := gsReadFile(t, f.activePath(0))
	if err := os.WriteFile(f.activePath(0), []byte(strings.ReplaceAll(raw, "Service test", "Disk edit")), 0o644); err != nil {
		t.Fatal(err)
	}
	invalidateGoalPoolCache()
	res := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), refreshFromDisk: true, mutate: func(g *jsObject) *jsObject { return goalWith(g, "autoContinue", false) }}))
	eq(t, res.ok, true, "apply succeeds")
	eq(t, strings.Contains(gstr(parseGoalFile(f.activePath(0)), "objective"), "Disk edit"), true, "disk objective kept")

	// the usage written is the baseline of the next merge
	f = gsNewFixture(t)
	id := mcSvcID(f)
	gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: mcSvcSetUsage(77, 9)}))
	eq(t, *f.service.lastPersistedUsage, goalPersistedUsage{goalId: id, tokensUsed: 77, activeSeconds: 9}, "baseline")

	// a mutation that changes the goal id moves the focus (and reports the previous id)
	f = gsNewFixture(t)
	id = mcSvcID(f)
	res = gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject { return goalWith(g, "id", "renamed-goal") }}))
	eq(t, res.ok, true, "apply succeeds")
	eq(t, res.focusChanged, true, "focus changed")
	eq(t, res.previousGoalId, id, "previous id")
	eq(t, res.goalId, "renamed-goal", "committed id")
	eq(t, f.log.focusChanges, [][2]string{{id, "renamed-goal"}}, "focus-changed hook")

	// without commit the session focus stays where it was
	f = gsNewFixture(t)
	id = mcSvcID(f)
	res = gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), commitFocused: new(false), mutate: func(g *jsObject) *jsObject { return goalWith(g, "id", "renamed-goal") }}))
	eq(t, res.ok, true, "apply succeeds")
	eq(t, res.goalId, id, "uncommitted: the focused id is reported")
	eq(t, res.focusChanged, false, "uncommitted: focus unchanged")
	eq(t, f.ref.getFocusedGoalId(), id, "focus stays")
}

func TestMutantsCServiceUpdateTask(t *testing.T) {
	complete := func(task *jsObject) (*jsObject, goalGate) {
		return goalWith(task, "status", "complete", "completedAt", nowIso()), goalGate{ok: true}
	}
	seed := func(f *gsFixture) {
		gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject { return goalWith(g, "taskList", gsTaskList("t1", "Shared task", false)) }}))
	}

	// the written revision is one past the disk record
	f := gsNewFixture(t)
	seed(f)
	out := gsMust(f.service.updateTask(f.ctx, goalTaskUpdateSpec{taskId: "t1", update: complete}))
	eq(t, out.ok, true, "update ok")
	eq(t, goalRevision(out.goal), 2.0, "written revision")
	eq(t, goalRevision(parseGoalFile(f.activePath(0))), 2.0, "disk revision")

	// an externally deleted goal is reported
	f = gsNewFixture(t)
	seed(f)
	f.ctx.readActiveGoalPool()
	if err := os.Remove(f.activePath(0)); err != nil {
		t.Fatal(err)
	}
	out = gsMust(f.service.updateTask(f.ctx, goalTaskUpdateSpec{taskId: "t1", update: complete}))
	eq(t, out.ok, false, "update fails")
	eq(t, out.message, "Goal "+mcSvcID(f)+" was deleted or archived by another process while the task update was in progress; the task was not updated.", "message")

	// a revision that moved under a stale cache: the last attempt fails with both revisions
	f = gsNewFixture(t)
	seed(f)
	id := mcSvcID(f)
	mcSvcExternalEdit(t, f, func(raw string) string { return strings.Replace(raw, `"revision": 1,`, `"revision": 5,`, 1) })
	out = gsMust(f.service.updateTaskAttempt(f.ctx, goalTaskUpdateSpec{taskId: "t1", update: complete}, 0))
	eq(t, out.ok, false, "update fails")
	eq(t, out.message, "Goal "+id+" was modified by another process (revision 1 -> 5); current revision is 5. The task was not updated.", "message")

	// ... and updateTask retries once: the retry runs while the first attempt still holds the goal lock (as the original's does), so it
	// times out acquiring it instead of reporting the conflict
	f = gsNewFixture(t)
	seed(f)
	mcSvcExternalEdit(t, f, func(raw string) string { return strings.Replace(raw, `"revision": 1,`, `"revision": 5,`, 1) })
	_, err := f.service.updateTask(f.ctx, goalTaskUpdateSpec{taskId: "t1", update: complete})
	if err == nil || !strings.Contains(err.Error(), "Timed out acquiring the goal lock for "+mcSvcID(f)) {
		t.Errorf("the retry must run (and meet the held lock): %v", err)
	}
}

func TestMutantsCServiceUpdateTaskInTurn(t *testing.T) {
	f := gsNewFixture(t)
	id := mcSvcID(f)
	gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject { return goalWith(g, "taskList", gsTaskList("t1", "Shared task", false)) }}))
	f.service.beginTurn(f.ctx, id)
	out := gsMust(f.service.updateTask(f.ctx, goalTaskUpdateSpec{
		taskId: "t1",
		update: func(task *jsObject) (*jsObject, goalGate) { return goalWith(task, "status", "complete"), goalGate{ok: true} },
		ledger: func(written, updated *jsObject) ([]*jsObject, error) { return nil, os.ErrInvalid },
	}))
	eq(t, out.ok, true, "the update stands")
	last := f.log.diagnostics[len(f.log.diagnostics)-1]
	eq(t, last, goalDiagnostic{severity: "warning", source: "ledger", goalId: "t1", message: "Ledger spec error during task update: Error: " + os.ErrInvalid.Error()}, "diagnostic")
}

func TestMutantsCServiceCurrentTaskRule(t *testing.T) {
	skipped := dmTask("t1", "A", "status", "skipped")
	got := resolveUpdatedCurrentTaskId(goalTaskUpdateSpec{taskId: "t1"}, "t1", skipped)
	if _, ok := got.(undef); !ok {
		t.Errorf("a skipped current task releases the focus: %#v", got)
	}
	got = resolveUpdatedCurrentTaskId(goalTaskUpdateSpec{taskId: "t1"}, "t2", skipped)
	eq(t, got, any("t2"), "another task stays current")
}

func TestMutantsCServiceUpdateTasks(t *testing.T) {
	seed := func(f *gsFixture) {
		gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject { return goalWith(g, "taskList", gsTaskList("t1", "Shared task", false)) }}))
	}
	touch := func(id string) goalTaskUpdateSpec {
		return goalTaskUpdateSpec{taskId: id, update: func(task *jsObject) (*jsObject, goalGate) { return goalWith(task, "note", "x"), goalGate{ok: true} }}
	}

	// the persisted goal is no longer active
	f := gsNewFixture(t)
	seed(f)
	mcSvcExternalEdit(t, f, func(raw string) string { return strings.Replace(raw, `"status": "active"`, `"status": "paused"`, 1) })
	out := gsMust(f.service.updateTasks(f.ctx, []goalTaskUpdateSpec{touch("t1")}))
	eq(t, out.ok, false, "not applied")
	eq(t, out.message, "The persisted goal is no longer active with tasks; no updates applied.", "message")

	// the persisted goal moved to another revision
	f = gsNewFixture(t)
	seed(f)
	mcSvcExternalEdit(t, f, func(raw string) string { return strings.Replace(raw, `"revision": 1,`, `"revision": 4,`, 1) })
	out = gsMust(f.service.updateTasks(f.ctx, []goalTaskUpdateSpec{touch("t1")}))
	eq(t, out.ok, false, "not applied")
	eq(t, out.message, "The goal changed in another process; no updates applied. Refresh and retry.", "message")

	// a stale focus token cancels the batch
	f = gsNewFixture(t)
	seed(f)
	tok := f.ref.focusToken(mcSvcID(f))
	f.ref.revision++
	spec := touch("t1")
	spec.focusToken = &tok
	out = gsMust(f.service.updateTasks(f.ctx, []goalTaskUpdateSpec{spec}))
	eq(t, out.ok, false, "not applied")
	eq(t, out.message, "The focused goal changed; no updates applied.", "message")

	// the session's usage is kept over the disk's
	f = gsNewFixture(t)
	seed(f)
	f.ctx.readActiveGoalPool()
	f.service.reconcileFocused(f.ctx, false)
	f.ref.getFocused().obj("usage").set("tokensUsed", 99.0)
	out = gsMust(f.service.updateTasks(f.ctx, []goalTaskUpdateSpec{touch("t1")}))
	eq(t, out.ok, true, "applied: "+out.message)
	tokens, _ := mcSvcUsage(parseGoalFile(f.activePath(0)))
	eq(t, tokens, 99.0, "usage written from memory")

	// later specs of a batch see the subtasks and renames of earlier ones
	f = gsNewFixture(t)
	seed(f)
	sub := dmTask("t1.1", "Child")
	addSub := goalTaskUpdateSpec{taskId: "t1", update: func(task *jsObject) (*jsObject, goalGate) {
		return goalWith(task, "subtasks", []any{sub}), goalGate{ok: true}
	}}
	rename := goalTaskUpdateSpec{taskId: "t1.1", update: func(task *jsObject) (*jsObject, goalGate) {
		return goalWith(task, "id", "t1.renamed"), goalGate{ok: true}
	}}
	done := goalTaskUpdateSpec{taskId: "t1.renamed", update: func(task *jsObject) (*jsObject, goalGate) {
		return goalWith(task, "status", "complete"), goalGate{ok: true}
	}}
	out = gsMust(f.service.updateTasks(f.ctx, []goalTaskUpdateSpec{addSub, rename, done}))
	eq(t, out.ok, true, "batch applied: "+out.message)
	tasks := parseGoalFile(f.activePath(0)).obj("taskList").vals["tasks"].([]any)
	subs := tasks[0].(*jsObject).vals["subtasks"].([]any)
	eq(t, gstr(subs[0].(*jsObject), "id"), "t1.renamed", "renamed subtask")
	eq(t, gstr(subs[0].(*jsObject), "status"), "complete", "completed after the rename")
}

func TestMutantsCServicePersist(t *testing.T) {
	// a lock held for a short while is waited out (4 attempts, 25 ms apart)
	f := gsNewFixture(t)
	id := mcSvcID(f)
	lock := gsMust(acquireGoalLock(f.ctx, id, 0, 0))
	done := make(chan struct{})
	go func() {
		time.Sleep(60 * time.Millisecond)
		lock.release()
		close(done)
	}()
	written := gsMust(f.service.persist(f.ctx))
	<-done
	eq(t, written != nil, true, "persist waited for the lock")

	// a moved revision with only a token delta still merges it
	f = gsNewFixture(t)
	f.service.lastPersistedUsage = &goalPersistedUsage{goalId: mcSvcID(f)}
	f.ref.setFocused(goalWith(f.ref.getFocused(), "usage", dmUsage(50, 0)))
	bSvc, _, _ := f.secondWriter()
	gsMust(bSvc.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: gsSetObjective("Other writer")}))
	written = gsMust(f.service.persist(f.ctx))
	eq(t, written != nil, true, "token-only delta is merged")
	tokens, seconds := mcSvcUsage(parseGoalFile(f.activePath(0)))
	eq(t, tokens, 50.0, "tokens")
	eq(t, seconds, 0.0, "seconds")

	// a complete goal is archived by persist
	f = gsNewFixture(t)
	f.ref.setFocused(goalWith(f.ref.getFocused(), "status", "complete"))
	gsMust(f.service.persist(f.ctx))
	eq(t, len(f.activeFiles()), 0, "archived")

	// a goal without an active path has no fresh disk record
	f = gsNewFixture(t)
	eq(t, f.service.readFreshDiskGoal(f.ctx, goalWith(f.written, "activePath", "")) == nil, true, "no active path")
}

// ---- ledger (goal-ledger.ts) ----

func TestMutantsCLedgerValidation(t *testing.T) {
	budget := func(old any) *jsObject {
		return ledgerEv("goal_budget_changed", "g1", "2026-01-01T00:00:00.000Z", "oldBudget", old, "newBudget", nil, "tokensUsed", 5.0)
	}
	eq(t, isValidLedgerEvent(budget(nil)), true, "null budget")
	eq(t, isValidLedgerEvent(budget(100.0)), true, "positive budget")
	eq(t, isValidLedgerEvent(budget(0.0)), false, "zero budget")
	eq(t, isValidLedgerEvent(budget(-5.0)), false, "negative budget")
	eq(t, isValidLedgerEvent(budget(1.5)), false, "fractional budget")
	blocked := func(source string) *jsObject {
		return ledgerEv("goal_blocked", "g1", "2026-01-01T00:00:00.000Z", "reason", "r", "source", source)
	}
	eq(t, isValidLedgerEvent(blocked("agent")), true, "agent source")
	eq(t, isValidLedgerEvent(blocked("system")), true, "system source")
	eq(t, isValidLedgerEvent(blocked("user")), false, "user source")
}

func TestMutantsCLedgerReconstruction(t *testing.T) {
	at := func(n int) string { return "2026-01-01T00:00:0" + string(rune('0'+n)) + ".000Z" }
	// focus generations: only the goal focused last is the latest focus
	st := reconstructGoalLedger([]*jsObject{
		createdEv("g1", "one", at(1)), ledgerEv("goal_focused", "g1", at(2), "reason", "created"),
		createdEv("g2", "two", at(3)), ledgerEv("goal_focused", "g2", at(4), "reason", "created"),
	})
	g1, _ := st.goals.get("g1")
	g2, _ := st.goals.get("g2")
	eq(t, g1.latestFocus, false, "g1 lost the focus")
	eq(t, g2.latestFocus, true, "g2 holds the focus")
	eq(t, derefStr(st.focusedGoalID), "g2", "focused goal")
	// an unfocus ends every focus
	st = reconstructGoalLedger([]*jsObject{
		createdEv("g1", "one", at(1)), ledgerEv("goal_focused", "g1", at(2), "reason", "created"),
		ledgerEvent("goal_unfocused", "reason", "x", "at", at(3)),
	})
	g1, _ = st.goals.get("g1")
	eq(t, g1.latestFocus, false, "unfocused")
	eq(t, st.focusedGoalID == nil, true, "no focused goal")
	// a terminal goal can be the latest focus, and its audit result is still recorded
	st = reconstructGoalLedger([]*jsObject{
		createdEv("g1", "one", at(1)), ledgerEv("goal_completed", "g1", at(2)),
		ledgerEv("goal_focused", "g1", at(3), "reason", "selected"),
		ledgerEv("audit_result", "g1", at(4), "verdict", "approved", "report", "fine"),
	})
	done, _ := st.terminalGoals.get("g1")
	eq(t, done.latestFocus, true, "a completed goal keeps its focus flag")
	eq(t, done.latestAuditorResult != nil && gstr(done.latestAuditorResult, "verdict") == "approved", true, "audit result of a terminal goal")
}

func TestMutantsCLedgerRecentTail(t *testing.T) {
	recent := newOrderedMap[[]*jsObject]()
	for i := 0; i < 15; i++ {
		appendRecent(recent, "g1", ledgerEv("task_started", "g1", "2026-01-01T00:00:00.000Z", "taskId", "t"+jsNumber(float64(i))))
	}
	tail, _ := recent.get("g1")
	eq(t, len(tail), 12, "the recent tail is capped at 12")
	eq(t, evStr(tail[0], "taskId"), "t3", "the oldest events are dropped")
	eq(t, evStr(tail[11], "taskId"), "t14", "the newest is kept")
}

func TestMutantsCLedgerCheckpointRead(t *testing.T) {
	events := []*jsObject{createdEv("g1", "one", "2026-01-01T00:00:01.000Z")}
	mk := func(edit func(o *jsObject)) *ledgerCheckpoint {
		v, err := parseJSON([]byte(marshalJSON(checkpointToJSON(buildCheckpointFromEvents(events, 100)), "")))
		if err != nil {
			t.Fatal(err)
		}
		o := v.(*jsObject)
		edit(o)
		return checkpointFromJSON(o)
	}
	eq(t, mk(func(*jsObject) {}) != nil, true, "an unmodified checkpoint reads back")
	eq(t, mk(func(o *jsObject) { o.set("format", "something-else") }) == nil, true, "wrong format")
	eq(t, mk(func(o *jsObject) { o.set("coveredBytes", -1.0) }) == nil, true, "negative covered bytes")
	eq(t, mk(func(o *jsObject) { o.set("coveredBytes", 0.0) }) != nil, true, "zero covered bytes")
}

func mcLedgerFixture(t *testing.T) storage {
	t.Helper()
	invalidateGoalLedgerCache()
	t.Cleanup(invalidateGoalLedgerCache)
	return ledgerTempCtx(t)
}

func TestMutantsCLedgerCheckpointMaintenance(t *testing.T) {
	// a cache hit whose checkpoint disagrees on the event count is rebuilt from the cached events
	ctx := mcLedgerFixture(t)
	appendGoal(ctx, "g1")
	appendGoal(ctx, "g2")
	invalidateGoalLedgerCache()
	eq(t, loadLedgerState(ctx).source, "full", "warm")
	fp := goalLedgerPath(ctx)
	cp := checkpointCache[fp]
	cp.coveredEvents = 1
	cp.acc = freshAccumulator()
	hit := loadLedgerState(ctx)
	eq(t, hit.source, "cache", "source")
	eq(t, hit.state.goals.size(), 2, "the checkpoint was rebuilt from the cached events")

	// appends keep the warm checkpoint current: events counted per event, recent tail extended
	ctx = mcLedgerFixture(t)
	appendGoal(ctx, "g1")
	invalidateGoalLedgerCache()
	loadLedgerState(ctx)
	fp = goalLedgerPath(ctx)
	before := checkpointCache[fp].coveredEvents
	eq(t, appendGoalEvents(ctx, []*jsObject{
		ledgerEv("task_started", "g1", "2026-01-01T00:00:05.000Z", "taskId", "a"),
		ledgerEv("task_started", "g1", "2026-01-01T00:00:06.000Z", "taskId", "b"),
	}).ok, true, "batch appended")
	eq(t, checkpointCache[fp].coveredEvents, before+2, "a two-event batch counts two events")
	got := loadLedgerState(ctx)
	eq(t, got.source, "cache", "source")
	eq(t, len(got.recentEventsByGoal["g1"]), 4, "recent events of g1 include the appended ones")
}

func TestMutantsCLedgerCheckpointThrottle(t *testing.T) {
	ctx := mcLedgerFixture(t)
	appendGoal(ctx, "g1")
	invalidateGoalLedgerCache()
	loadLedgerState(ctx)
	base := clockMs()
	old := clockMs
	clockMs = func() int64 { return base + 3000 }
	t.Cleanup(func() { clockMs = old })
	appendGoalEvent(ctx, ledgerEv("task_started", "g1", "2026-01-01T00:00:05.000Z", "taskId", "a"))
	data, err := os.ReadFile(ledgerCheckpointPath(ctx))
	if err != nil {
		t.Fatal(err)
	}
	covered, _ := jo(t, string(data)).num("coveredEvents")
	eq(t, covered, 3.0, "a checkpoint older than two seconds is rewritten on the next append")
}

func TestMutantsCLedgerRuntimeIndexDropsCache(t *testing.T) {
	ctx := mcLedgerFixture(t)
	appendGoal(ctx, "g1")
	invalidateGoalLedgerCache()
	runtimeIndex(ctx)
	eq(t, ledgerCache[goalLedgerPath(ctx)] == nil, true, "the full event cache is released once the runtime index is built")
}

func TestMutantsCStorageLedgerLines(t *testing.T) {
	ctx := ledgerTempCtx(t)
	eq(t, ctx.appendGoalEvents([]*jsObject{createdEv("g1", "one", "2026-01-01T00:00:01.000Z"), createdEv("g2", "two", "2026-01-01T00:00:02.000Z")}), nil, "append")
	eq(t, len(ledgerLines(t, goalLedgerPath(ctx))), 2, "one JSON line per event")
}

// ---- ledger index ----

func TestMutantsCLedgerIndex(t *testing.T) {
	ev := func(typ string, n int, kv ...any) *jsObject {
		return ledgerEv(typ, "g1", "2026-01-01T00:00:"+jsNumber(float64(10+n))+".000Z", kv...)
	}
	// the recent window holds the last 12 events
	var events []*jsObject
	for i := 0; i < 15; i++ {
		events = append(events, ev("task_started", i, "taskId", "t"+jsNumber(float64(i))))
	}
	idx := buildLedgerIndex(events)
	g, _ := idx.get("g1")
	eq(t, len(g.recent), 12, "recent window")
	eq(t, evStr(g.recent[0], "taskId"), "t3", "oldest in the window")

	// lifecycle, audit and completion slots
	for _, typ := range []string{"goal_budget_limited", "goal_completed", "goal_created"} {
		idx = buildLedgerIndex([]*jsObject{ev(typ, 0, "objective", "o", "sisyphus", false, "autoContinue", true, "budget", 1.0, "tokensUsed", 1.0)})
		g, _ = idx.get("g1")
		eq(t, g.lifecycle != nil, true, typ+" is a lifecycle event")
	}
	idx = buildLedgerIndex([]*jsObject{ev("audit_result", 0, "verdict", "approved", "report", "ok"), ev("completion_requested", 1)})
	g, _ = idx.get("g1")
	eq(t, g.audit != nil && gstr(g.audit, "type") == "audit_result", true, "audit slot")
	eq(t, g.completion != nil && gstr(g.completion, "type") == "completion_requested", true, "completion slot")

	// oracle consult state
	fp := "fp1"
	idx = buildLedgerIndex([]*jsObject{ev("oracle_followup_attempted", 0, "fingerprint", fp, "adviceId", "a", "firstToolName", "read")})
	g, _ = idx.get("g1")
	st, _ := g.oracle.get(fp)
	eq(t, st.vals["followupAttempted"], any(true), "follow-up recorded")

	// out-of-order activity is inserted after events with an equal timestamp
	a := ledgerEv("task_started", "g1", "2026-01-01T00:00:20.000Z", "taskId", "a")
	b := ledgerEv("task_started", "g1", "2026-01-01T00:00:40.000Z", "taskId", "b")
	c := ledgerEv("task_started", "g1", "2026-01-01T00:00:20.000Z", "taskId", "c")
	idx = buildLedgerIndex([]*jsObject{a, b, c})
	g, _ = idx.get("g1")
	var order []string
	for _, e := range g.activity {
		order = append(order, evStr(e, "taskId"))
	}
	eq(t, order, []string{"a", "c", "b"}, "activity order")
}

func TestMutantsCLedgerIndexFromJSON(t *testing.T) {
	read := func(recent, activity int, last bool) *goalLedgerIndex {
		mk := func(n int) []any {
			out := []any{}
			for i := 0; i < n; i++ {
				out = append(out, ledgerEv("task_started", "g1", "2026-01-01T00:00:00.000Z", "taskId", "t"))
			}
			return out
		}
		o := newObject()
		o.set("recent", mk(recent))
		o.set("activity", mk(activity))
		o.set("oracle", []any{})
		if last {
			o.set("lastActivityEvent", ledgerEv("task_started", "g1", "2026-01-01T00:00:00.000Z", "taskId", "t"))
		}
		return goalLedgerIndexFromJSON(o)
	}
	eq(t, read(12, 0, false) != nil, true, "12 recent events fit")
	eq(t, read(13, 0, false) == nil, true, "13 recent events do not")
	eq(t, read(0, 1, true) != nil, true, "an activity window with its last event")
	eq(t, read(0, 1, false) == nil, true, "an activity window without its last event")
}

// ---- settings ----

func TestMutantsCSettingsPureFunctions(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
		ok   bool
	}{{"-5", -5, true}, {"+7", 7, true}, {"  \t 12px", 12, true}, {"\u00a0\n9", 9, true}, {"x1", 0, false}, {"-", 0, false}} {
		got, ok := settingsParseInt(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseInt(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	// a null settings file is an empty layer without diagnostics
	layer, diags := parseSettingsLayer(nil, settingsScopeProject, "p")
	eq(t, len(layer.keys), 0, "layer")
	eq(t, len(diags), 0, "diagnostics")
	// the strict legacy parse only rejects unknown TOP-LEVEL keys
	nested := newObject()
	oracle := newObject()
	oracle.set("bogus", 1.0)
	nested.set("oracle", oracle)
	_, err := parseGoalSettings(nested)
	eq(t, err == nil, true, "an unknown nested key is not a strict-parse error")
	top := newObject()
	top.set("bogusKey", 1.0)
	_, err = parseGoalSettings(top)
	eq(t, err != nil && strings.Contains(err.Error(), "bogusKey"), true, "an unknown top-level key is")
	// status of a read
	eq(t, statusFor(nil, false), "missing", "empty content")
	eq(t, statusFor(nil, true), "ok", "content")
	// the persisted form drops falsy provider values
	s := newObject()
	s.set("provider", 0.0)
	s.set("model", 7.0)
	p := buildPersistedLayer(s)
	eq(t, p.has("provider"), false, "a 0 provider is falsy")
	eq(t, p.has("model"), true, "a non-zero model is truthy")
	// pid liveness of a lock holder
	eq(t, settingsPidAlive(1.5), false, "a fractional pid is not a pid")
	eq(t, settingsPidAlive(-3.0), false, "a negative pid")
	eq(t, settingsPidAlive(float64(os.Getpid())), true, "this process")
	// symlink refusal text
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "link.json")
	if err := os.WriteFile(real, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	err = refuseSymlinkTarget(link)
	eq(t, err != nil && err.Error() == "refusing symlink settings target: "+link, true, "symlink refusal text")
}

func TestMutantsCSettingsReads(t *testing.T) {
	isolateHome(t)
	missing := filepath.Join(t.TempDir(), "none.json")
	read := readSettingsLayerFresh(missing, settingsScopeProject)
	eq(t, read.status, "missing", "status")
	eq(t, read.fingerprint, "missing", "fingerprint of a missing file")
	// defaults
	cwd := t.TempDir()
	invalidateGoalSettingsCache()
	eq(t, loadGoalSettings(cwd).oracle.maxFailedAttemptsPerBlocker, 2, "default oracle attempts")
}

func TestMutantsCSettingsMutationLock(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	target := goalSettingsPath(cwd)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	// a live holder of <settings>.lock blocks the mutation
	holder := marshalJSON(detailsOf("pid", float64(os.Getpid()), "startedAt", nowIso()), "")
	if err := os.WriteFile(target+".lock", []byte(holder), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := mutateSettingsLayer(settingsMutateInput{scope: settingsScopeProject, cwd: cwd, mutation: settingsMutation{op: "set", path: []string{"disabled"}, value: true}})
	eq(t, err != nil && strings.Contains(err.Error(), "Timed out acquiring the settings lock at "+target+".lock"), true, "lock path")
}

// ---- goal root ----

func TestMutantsCGoalRoot(t *testing.T) {
	isolateHome(t)
	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	// "~foo" is not "~/": it is relative, hence rejected
	settingsWrite(t, goalSettingsPath(cwd), `{"goalsRoot":"~foo/pool"}`)
	invalidateGoalSettingsCache()
	_, err := goalStorageRoot(&goalStorageContext{cwd: cwd, sessionManager: fixedSession{"s1"}})
	eq(t, err != nil && strings.Contains(err.Error(), "must be an absolute path"), true, "tilde without a slash")

	// the logical prefix must end at a path boundary
	ctx := &goalStorageContext{cwd: cwd}
	_, err = goalStoragePath(ctx, ".pi/goalsX/a.md")
	eq(t, err != nil && strings.Contains(err.Error(), "Invalid goal storage path"), true, "prefix lookalike")
	_, err = goalStoragePath(ctx, ".pi/goals")
	eq(t, err, nil, "the root itself")

	// a pinned root is only reused for the same cwd
	cwdA, _ := filepath.EvalSymlinks(t.TempDir())
	cwdB, _ := filepath.EvalSymlinks(t.TempDir())
	extA, _ := filepath.EvalSymlinks(t.TempDir())
	extB, _ := filepath.EvalSymlinks(t.TempDir())
	settingsWrite(t, goalSettingsPath(cwdA), `{"goalsRoot":"`+extA+`"}`)
	settingsWrite(t, goalSettingsPath(cwdB), `{"goalsRoot":"`+extB+`"}`)
	invalidateGoalSettingsCache()
	sm := fixedSession{"same-session"}
	rootA, err := configuredGoalRoot(&goalStorageContext{cwd: cwdA, sessionManager: sm})
	eq(t, err, nil, "A")
	eq(t, rootA, extA, "root of A")
	rootB, err := configuredGoalRoot(&goalStorageContext{cwd: cwdB, sessionManager: sm})
	eq(t, err, nil, "B")
	eq(t, rootB, extB, "root of B")
}

// ---- format ----

func TestMutantsCFormat(t *testing.T) {
	eq(t, goalDetails(nil, "").vals["version"], any(3.0), "details version")
	goal := dmGoal()
	var events []*jsObject
	events = append(events, dmEv("audit_result", "2026-03-04T05:06:07.000Z", "verdict", "approved", "report", strings.Repeat("r", 100)))
	for i := 0; i < 8; i++ {
		events = append(events, dmEv("task_started", "2026-03-04T10:2"+jsNumber(float64(i))+":3"+jsNumber(float64(i))+".000Z", "taskId", "t"+jsNumber(float64(i))))
	}
	got := buildGoalHistoryBlock(goal, events)
	want := "Last audit: approved (2026-03-04) — " + strings.Repeat("r", 87) + "...\n" +
		"Recent events:\n" +
		"  10:22:32 task_started\n  10:23:33 task_started\n  10:24:34 task_started\n  10:25:35 task_started\n  10:26:36 task_started\n  10:27:37 task_started"
	eq(t, got, want, "history block")
}

func TestMutantsCFormatMarkers(t *testing.T) {
	id, ok := matchV2SelfClosingContinuation(`<pi_goal_continuation goal_id="g1" kind="checkpoint" v="2">`)
	eq(t, ok, true, "the slash of a v2 marker is optional")
	eq(t, id, "g1", "goal id")
	_, ok = matchV2SelfClosingContinuation(`<pi_goal_continuation goal_id="g1" kind="checkpoint" v="3"/>`)
	eq(t, ok, false, "another version")
	user := newObject()
	user.set("role", "user")
	user.set("stopReason", "aborted")
	eq(t, isAbortedAssistantMessage(user), false, "a user message is not an aborted assistant message")
	asst := newObject()
	asst.set("role", "assistant")
	asst.set("stopReason", "stop")
	eq(t, isErrorAssistantMessage(asst), false, "a normal stop is not an error")
	asst.set("stopReason", "error")
	eq(t, isErrorAssistantMessage(asst), true, "an error stop")
	asst.set("rawStopReason", "too many")
	asst.set("errorMessage", "requests")
	eq(t, isNetworkErrorAssistantMessage(asst), true, "raw reason and message are joined by a space")
}

// ---- goal detail ----

func TestMutantsCDetailCursor(t *testing.T) {
	cursor := base64.RawURLEncoding.EncodeToString([]byte(marshalJSON(goalDetailCursorObject("k", 10), "")))
	off, ok := goalDetailParseCursor(cursor, "k", 10)
	eq(t, ok, true, "a cursor at the end of the source is valid")
	eq(t, off, 10, "offset")
	_, ok = goalDetailParseCursor(cursor, "k", 9)
	eq(t, ok, false, "a cursor past the end is not")
}

// ---- goal draft text, task confirmation, questionnaire ----

func TestMutantsCDraftText(t *testing.T) {
	got := buildDraftConfirmationText("sisyphus", "  my topic  ", "Objective: x\n\n│ already piped\n   \nSuccess: y", true)
	want := strings.Join([]string{
		"● Goal draft ready for confirmation.", "", "─── Draft Details ───",
		"│   Mode: Sisyphus (prompt/criteria style)",
		"│   Auto-continue: yes",
		"", "─── Original Topic ───", "", "│   my topic",
		"", "─── Proposed Goal ───", "", "│   Objective: x", "│ already piped", "│   Success: y",
	}, "\n")
	eq(t, got, want, "sisyphus draft text")
	got = buildDraftConfirmationText("goal", "t", "o", false)
	mcHas(t, got, "│   Mode: Normal goal\n│   Auto-continue: no\n")

	task := dmTask("t1", "Do it")
	tweak := buildTweakConfirmationText("old", "new", "why", false, []*jsObject{task})
	wantTweak := strings.Join([]string{
		"● Goal tweak ready for confirmation.", "", "─── Draft Details ───", "│   Mode: Normal goal",
		"", "─── Change ───", "", "│   why",
		"", "─── Current Objective ───", "", "│   old",
		"", "─── Proposed New Objective ───", "", "│   new",
		"", "┌─ TASKS ─────────────────────────────────────┐",
		"[ ] t1: Do it",
		"└──────────────────────────────────────────────┘",
	}, "\n")
	eq(t, tweak, wantTweak, "tweak text")
}

func TestMutantsCDraftValidationAndSummary(t *testing.T) {
	v := validateGoalDraftProposal(draftProposalInput{})
	eq(t, v, draftProposalValidation{message: "propose_goal_draft REJECTED: no /goal, /sisyphus, or /goal-tweak guided draft is in progress."}, "no intent")
	intent := &goalConfirmationIntentLike{focus: "goal"}
	v = validateGoalDraftProposal(draftProposalInput{intent: intent, objective: "x", sisyphus: true})
	eq(t, v.message, "propose_goal_draft REJECTED (focus gate): the user invoked /goal but you passed sisyphus=true. Set sisyphus=false to match their choice.", "focus gate")
	v = validateGoalDraftProposal(draftProposalInput{intent: &goalConfirmationIntentLike{focus: "sisyphus"}, objective: "x", sisyphus: false})
	eq(t, v.message, "propose_goal_draft REJECTED (focus gate): the user invoked /sisyphus but you passed sisyphus=false. Set sisyphus=true to match their choice.", "focus gate sisyphus")
	v = validateGoalDraftProposal(draftProposalInput{intent: intent, objective: " \n "})
	eq(t, v.message, "propose_goal_draft REJECTED: objective is empty.", "empty objective")
	v = validateGoalDraftProposal(draftProposalInput{intent: intent, objective: " ship it "})
	eq(t, v, draftProposalValidation{ok: true, objective: "ship it"}, "valid")

	tl := newObject()
	child := dmTask("a.1", "Child")
	parent := dmTask("a", "Parent", "subtasks", []any{child})
	tl.set("tasks", []any{parent, dmTask("b", "Second")})
	got := buildProposalSummary(proposalSummaryArgs{objective: " Obj ", taskList: tl, verificationContract: " run it ", autoContinue: true, auditorEnabled: false})
	eq(t, got, strings.Join([]string{
		"Proposed objective:", "Obj", "", "Proposed plan:", "1. Parent", "   - Child", "2. Second",
		"", "Verification:", "run it", "", "Automatic continuation: enabled", "Independent auditor: disabled",
	}, "\n"), "summary")
	got = buildProposalSummary(proposalSummaryArgs{objective: "Obj", autoContinue: false, auditorEnabled: true})
	eq(t, got, "Proposed objective:\nObj\n\nAutomatic continuation: disabled\nIndependent auditor: enabled", "summary without plan")
}

func TestMutantsCConfirmationTasks(t *testing.T) {
	sub := dmTask("t1.1", "Sub", "verificationContract", "check it")
	top := dmTask("t1", "Top", "lightweightSubtasks", true, "subtasks", []any{sub})
	eq(t, renderConfirmationTasks([]*jsObject{top, dmTask("t2", "Plain")}, 0), []string{
		"[ ] t1: Top (lightweight)",
		"  [ ] t1.1: Sub contract: check it",
		"[ ] t2: Plain",
	}, "task lines")
}

func TestMutantsCTaskConfirmationDialog(t *testing.T) {
	// PI_GOAL_AUTO_CONFIRM=1 confirms without any dialog
	t.Setenv("PI_GOAL_AUTO_CONFIRM", "1")
	ctx := &mockDialogCtx{uiFlag: true, u: &mockDialogUI{}}
	res, err := showTaskConfirmation(ctx, "text")
	eq(t, err, nil, "error")
	eq(t, res.decision, "confirm", "auto confirm")
	t.Setenv("PI_GOAL_AUTO_CONFIRM", "")
	res, _ = showTaskConfirmation(ctx, "text")
	eq(t, res.decision, "cancel", "a UI without dialogs cancels")

	// rendering: the body is capped at 16 lines, the width at 80, Escape cancels
	body := make([]string, 20)
	for i := range body {
		body[i] = "line " + jsNumber(float64(i+1))
	}
	var got any
	d := &taskConfirmationDialog{ui: ctx.u, t: &mockDialogTUI{}, theme: plainTestTheme{}, done: func(r any) { got = r }, body: body}
	out := strings.Join(d.Render(100), "\n")
	mcHas(t, out, "line 16")
	mcNot(t, out, "line 17")
	mcHas(t, out, "+4 more lines")
	eq(t, d.Render(81), d.Render(80), "the dialog is at most 80 columns wide")
	d.HandleInput("\x1b")
	eq(t, got.(*taskConfirmationResult).decision, "cancel", "Escape cancels")
	d.HandleInput("\x1b[B")
	d.HandleInput("\r")
	eq(t, got.(*taskConfirmationResult).decision, "cancel", "the second option keeps the tasks")
	d.HandleInput("\x1b[A")
	d.HandleInput("\r")
	eq(t, got.(*taskConfirmationResult).decision, "confirm", "the first option confirms")
}

func TestMutantsCQuestionnaireLayout(t *testing.T) {
	// the tasks segment ends before a border line that follows no task line
	lines := []string{"Tasks proposed for confirmation:", "└─────┘", "tail"}
	seg := findProposalPresentationSegments(lines, 2)
	eq(t, *seg, proposalPresentationSegments{tasksStart: 0, tasksEnd: 0, tailStart: 2}, "border without tasks")
	// the auditor hint directly below the last task line is pulled into the tail
	lines = []string{"Tasks proposed for confirmation:", "[ ] a", "  press 'a' to toggle the auditor", "option"}
	seg = findProposalPresentationSegments(lines, 3)
	eq(t, *seg, proposalPresentationSegments{tasksStart: 0, tasksEnd: 1, tailStart: 2}, "auditor hint")

	// a frame that exactly fits is returned as is
	five := []string{"a", "b", "c", "d", "e"}
	eq(t, fitDialogLines(five, 5, 1, nil, &dialogScrollState{}, nil), five, "exact fit with a viewport")
	eq(t, fitDialogLines(five, 5, 1, &proposalPresentationSegments{tasksStart: 1, tasksEnd: 1, tailStart: 3}, nil, nil), five, "exact fit with a proposal")

	// a followed option that ends on the first line below the window scrolls into view
	lines = make([]string, 30)
	for i := range lines {
		lines[i] = "line " + jsNumber(float64(i))
	}
	st := &dialogScrollState{needsFollow: true, optionRanges: [][2]int{{5, 9}}, followIndex: 0}
	fitDialogViewport(lines, 10, st, func(s string) string { return s })
	eq(t, st.scrollTop, 1, "scroll top after following an option on the window edge")

	// protected blank lines of the head survive blank stripping
	pl := []string{"", "H", "Tasks proposed for confirmation:", "[ ] a", "", "o1", "o2"}
	eq(t, fitProposalPresentation(pl, 6, 2, proposalPresentationSegments{tasksStart: 2, tasksEnd: 3, tailStart: 4}),
		[]string{"", "H", "Tasks proposed for confirmation:", "[ ] a", "o1", "o2"}, "head blank kept")
	// the auditor toggle line is kept at the top of a squeezed tail
	pl = []string{"H", "Tasks proposed for confirmation:", "[ ] a", "[ ] b", "  press 'a' to toggle auditor", "o1", "o2", "o3"}
	eq(t, fitProposalPresentation(pl, 4, 1, proposalPresentationSegments{tasksStart: 1, tasksEnd: 3, tailStart: 4}),
		[]string{"H", "  press 'a' to toggle auditor", "o2", "o3"}, "auditor line kept")

	// the option block fit never drops the first line of the dialog
	eq(t, fitOptionsInFrame([]string{"Q", "a", "o1", "o2", "h1"}, 3, 0, 2, 3) == nil, true, "no fit that would drop the question")
	eq(t, fitOptionsInFrame([]string{"Q", "", "m", "o1", "o2"}, 4, 0, 3, 4), []string{"Q", "m", "o1", "o2"}, "fit")

	// headless drafting needs a topic of at least 20 characters
	eq(t, isHeadlessQuestionSufficientForDraft(strings.Repeat("x", 19), "q"), false, "19 characters")
	eq(t, isHeadlessQuestionSufficientForDraft(strings.Repeat("x", 20), "q"), true, "20 characters")
}

func TestMutantsCProposalDialog(t *testing.T) {
	h := newScriptedHost([]int{0})
	res, err := showProposalDialog(h.ctx, "OBJECTIVE", "sisyphus", nil)
	eq(t, err, nil, "error")
	eq(t, res.decision, proposalDecisionConfirm, "decision")
	eq(t, res.auditorEnabled, true, "the auditor defaults to enabled")
	eq(t, strings.Contains(h.dialogs()[0].title, "Confirm Sisyphus Goal Draft"), true, "sisyphus title: "+h.dialogs()[0].title)
	h = newScriptedHost([]int{0})
	showProposalDialog(h.ctx, "OBJECTIVE", "goal", nil)
	eq(t, strings.Contains(h.dialogs()[0].title, "Confirm Goal Draft"), true, "goal title")
	eq(t, strings.Contains(h.dialogs()[0].title, "Sisyphus"), false, "goal title has no sisyphus")
}

func mcDialogOpen(t *testing.T, rows, base int, qs []goalQuestionnaireQuestion) (*questionnaireDialog, *any) {
	t.Helper()
	c := newCapturedCtx(t)
	f := c.open(func(ctx goalDialogContext) { runGoalQuestionnaire(ctx, qs, nil) })
	var got any
	m := &mockDialogTUI{rows: &rows, prevLines: &base}
	comp := f(m, plainTestTheme{}, func(r any) { got = r })
	return comp.(*questionnaireDialog), &got
}

func TestMutantsCQuestionnaireDialogKeys(t *testing.T) {
	opts := []string{"one", "two"}
	d, _ := mcDialogOpen(t, 24, 19, []goalQuestionnaireQuestion{{id: "q", question: "Pick", options: opts}})
	// maxDialogLines is 10 here: a page is nine lines
	d.HandleInput("\x1b[6~")
	eq(t, d.scrollTop, 9, "page step")
	// the option marker stays on the list (two options and the free-text row)
	d.HandleInput("\x1b[A")
	eq(t, d.optionIndex, 0, "up from the first option")
	for i := 0; i < 6; i++ {
		d.HandleInput("\x1b[B")
	}
	eq(t, d.optionIndex, 2, "down stops at the last row")

	// a blank free-text answer is ignored
	d, got := mcDialogOpen(t, 24, 19, []goalQuestionnaireQuestion{{id: "q", question: "Say it"}})
	d.HandleInput(" ")
	d.HandleInput("\r")
	eq(t, *got == nil, true, "a blank answer does not submit")
	d.HandleInput("x")
	d.HandleInput("\r")
	res, _ := (*got).(*goalQuestionnaireResult)
	eq(t, res != nil && len(res.answers) == 1 && res.answers[0].answer == "x", true, "a real answer does")

	// the submit tab needs every question answered
	d, got = mcDialogOpen(t, 24, 19, []goalQuestionnaireQuestion{{id: "a", question: "A", options: opts}, {id: "b", question: "B", options: opts}})
	d.HandleInput("\r") // answers A, moves to B
	d.HandleInput("\t") // submit tab, B unanswered
	d.HandleInput("\r")
	eq(t, *got == nil, true, "submit is refused while a question is unanswered")
	d.HandleInput("\t") // back to A
	d.HandleInput("\t") // B
	d.HandleInput("\r") // answers B, lands on the submit tab
	d.HandleInput("\r")
	eq(t, *got != nil, true, "submit once everything is answered")

	// rendering is never narrower than 20 columns
	d, _ = mcDialogOpen(t, 0, 0, []goalQuestionnaireQuestion{{id: "q", question: "A fairly long question text that needs wrapping", options: opts}})
	widest := 0
	for _, l := range d.Render(5) {
		widest = max(widest, visibleWidth(l))
	}
	eq(t, widest > 10 && widest <= 20, true, "widest line of a 5-column render is within 11..20, got "+jsNumber(float64(widest)))
}

func TestMutantsCServiceTaskLedger(t *testing.T) {
	f := gsNewFixture(t)
	gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject { return goalWith(g, "taskList", gsTaskList("t1", "Shared task", false)) }}))
	id := mcSvcID(f)
	out := gsMust(f.service.updateTask(f.ctx, goalTaskUpdateSpec{
		taskId: "t1",
		update: func(task *jsObject) (*jsObject, goalGate) { return goalWith(task, "status", "complete"), goalGate{ok: true} },
		ledger: func(written, updated *jsObject) ([]*jsObject, error) {
			return []*jsObject{ledgerEvent("task_complete", "goalId", id, "taskId", "t1", "at", nowIso())}, nil
		},
	}))
	eq(t, out.ok, true, "update ok")
	events := f.ledgerEvents()
	eq(t, len(events), 1, "the task ledger event is appended")
	eq(t, gstr(events[0], "type"), "task_complete", "event type")
}
