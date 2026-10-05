package pi_goal_x

import (
	"errors"
	"fmt"
	"strings"
)

// The goal session state and its service (goal-state.ts, goal-service.ts: the immediate path, no turn buffer) and the commands
// (goal-commands.ts). The host is a small interface so that the same code runs under the SDK and under the tests.

type host interface {
	Cwd() string
	SessionID() string
	HasUI() bool
	// Rich reports a host with a footer for a status line (a terminal); an RPC host shows status text as an event only.
	Rich() bool
	Notify(msg, level string)
	SetStatus(key, text string) // empty text clears
	ClearWidget(key string)
	Confirm(title, msg string) bool
	Select(title string, options []string) (string, bool)
	Branch() []any
	AppendEntry(customType string, data *jsObject)
	IsIdle() bool
	Abort()
}

const (
	focusEntryType = "pi-goal-focus"
	stateEntryType = "pi-goal-state"
	widgetKey      = "goal"
)

type core struct {
	h         host
	st        storage
	goals     *goalPool
	focused   *string
	focusRev  int
	explicit  bool
	uiPending bool
}

func newCore(h host) *core {
	return &core{h: h, st: storage{cwd: h.Cwd()}, goals: newPool()}
}

func (c *core) state() *jsObject {
	if c.focused == nil {
		return nil
	}
	return c.goals.get(*c.focused)
}

func (c *core) assignFocused(id *string) {
	if (c.focused == nil) != (id == nil) || (c.focused != nil && *c.focused != *id) {
		c.focusRev++
	}
	c.focused = id
}

func strp(s string) *string { return &s }

// setStateGoal is the `state.goal = next` setter.
func (c *core) setStateGoal(next *jsObject) {
	if next != nil {
		c.goals.set(gstr(next, "id"), next)
		c.assignFocused(strp(gstr(next, "id")))
		return
	}
	if c.focused != nil {
		c.goals.delete(*c.focused)
	}
	c.assignFocused(nil)
}

// ---- UI ----

func (c *core) updateUI() {
	if c.h.HasUI() {
		c.uiPending = true
	}
}

// flush runs the UI update the original queues as a microtask: once, after the handler's synchronous work.
func (c *core) flush() {
	if !c.uiPending {
		return
	}
	c.uiPending = false
	c.renderUI()
}

func (c *core) clearGoalWidget() {
	if c.h.HasUI() {
		c.h.SetStatus(widgetKey, "")
		c.h.ClearWidget(widgetKey)
	}
}

func (c *core) renderUI() {
	g := c.state()
	totalOpen := otherOpenGoalCount(c.goals, nil)
	if g == nil && totalOpen == 0 {
		c.clearGoalWidget()
		return
	}
	if g == nil {
		c.h.SetStatus(widgetKey, fmt.Sprintf("goal: unfocused [%d open] - /goal-focus", totalOpen))
		return
	}
	// The original shows a dashboard component here. A terminal gets one status line instead; an RPC host shows nothing, as Pi does.
	if c.h.Rich() {
		c.h.SetStatus(widgetKey, footerStatus(g))
		return
	}
	c.h.SetStatus(widgetKey, "")
}

func footerStatus(g *jsObject) string {
	tokens, seconds := usageOf(g)
	var bits []string
	if seconds > 0 {
		bits = append(bits, formatDuration(seconds))
	}
	if tokens > 0 {
		bits = append(bits, firstWord(formatTokenValue(tokens)))
	}
	usage := ""
	if len(bits) > 0 {
		usage = " [" + strings.Join(bits, " ") + "]"
	}
	prefix := "goal"
	if gflag(g, "sisyphus") {
		prefix = "goal✊"
	}
	return prefix + ": " + statusLabel(g) + usage + " - " + truncateText(gstr(g, "objective"), 60)
}

// ---- focus entries and ledger ----

func (c *core) appendFocusEntry(id *string, reason string) {
	c.explicit = true
	c.h.AppendEntry(focusEntryType, goalFocusDetails(id, reason))
}

func (c *core) appendEvents(events ...*jsObject) { _ = c.st.appendGoalEvents(events) }

// ---- session start ----

func resolveSessionFocus(pool *goalPool, entry *focusEntry, legacy *jsObject, autoSelect bool) *string {
	var focusedID *string
	if entry != nil {
		focusedID = entry.goalID
	}
	if focusedID != nil {
		if g := pool.get(*focusedID); g != nil && gstr(g, "status") != "complete" {
			return focusedID
		}
	}
	if entry != nil {
		return nil
	}
	if legacy != nil && gstr(legacy, "status") != "complete" {
		id := gstr(legacy, "id")
		if !pool.has(id) {
			pool.set(id, cloneGoal(legacy))
		}
		return &id
	}
	if autoSelect {
		if open := openGoalsFromPool(pool); len(open) == 1 {
			return strp(gstr(open[0], "id"))
		}
	}
	return nil
}

func (c *core) loadState() {
	c.clearGoalWidget()
	root := c.st.root()
	c.goals = c.st.readActiveGoalPool()
	c.focusRev++
	c.assignFocused(nil)
	c.explicit = false
	var entry *focusEntry
	var legacy *jsObject
	legacySeen := false
	branch := c.h.Branch()
	for i := len(branch) - 1; i >= 0; i-- {
		e, _ := branch[i].(*jsObject)
		if e == nil {
			continue
		}
		if t, _ := e.str("type"); t != "custom" {
			continue
		}
		ct, _ := e.str("customType")
		if entry == nil && ct == focusEntryType {
			entry = normalizeGoalFocusEntry(e.vals["data"])
		}
		if !legacySeen && ct == stateEntryType {
			if d := e.obj("data"); d != nil {
				legacy = normalizeGoalRecord(d.vals["goal"])
			}
			legacySeen = true
		}
		if entry != nil && legacySeen {
			break
		}
	}
	if legacy != nil && gstr(legacy, "status") != "complete" {
		legacy = sanitizeGoalPaths(c.st, c.st.mergeGoalPromptFromDisk(legacy))
	}
	settings := loadGoalSettings(c.h.Cwd())
	if entry != nil {
		entryRoot := root
		if entry.hasStorage {
			entryRoot = entry.root
		}
		if entryRoot != root {
			entry = &focusEntry{goalID: nil, reason: entry.reason}
			legacy = nil
		}
	}
	c.explicit = entry != nil
	c.assignFocused(resolveSessionFocus(c.goals, entry, legacy, settings.autoSelectSingle))
	if entry == nil && c.focused != nil {
		reason := "selected"
		if legacy != nil && gstr(legacy, "id") == *c.focused {
			reason = "migrated"
		}
		c.appendFocusEntry(c.focused, reason)
	}
	for _, g := range c.goals.values() {
		if gstr(g, "status") == "complete" {
			c.goals.delete(gstr(g, "id"))
		}
	}
	c.updateUI()
}

// ---- service ----

type mutation struct {
	reconcile       bool
	expectedGoalID  string
	refreshFromDisk bool
	mutate          func(*jsObject) (*jsObject, error)
	ledger          func(written *jsObject) []*jsObject
	archive         bool
	noCommitFocused bool
}

func (c *core) reconcileFocused() bool {
	current := c.state()
	source := c.st.readActiveGoalPoolView()
	unchanged := source.size() == c.goals.size() && (c.focused == nil || source.has(*c.focused))
	if unchanged {
		for _, id := range source.ids {
			if c.goals.get(id) != source.get(id) {
				unchanged = false
				break
			}
		}
	}
	if unchanged {
		return true
	}
	fresh := source.copy()
	if c.focused == nil {
		c.goals = fresh
		return true
	}
	disk := fresh.get(*c.focused)
	if disk == nil {
		if current != nil && !(current.has("activePath") && truthy(current.vals["activePath"])) {
			c.goals = fresh
			fresh.set(gstr(current, "id"), current)
			c.assignFocused(strp(gstr(current, "id")))
			return true
		}
		c.goals = fresh
		c.assignFocused(nil)
		c.updateUI()
		return false
	}
	c.goals = fresh
	fresh.set(gstr(disk, "id"), disk)
	c.assignFocused(strp(gstr(disk, "id")))
	return true
}

type mutationResult struct {
	goal         *jsObject
	focusChanged bool
}

func (c *core) apply(spec mutation) (*mutationResult, error) {
	if spec.reconcile && !c.reconcileFocused() {
		return nil, errors.New("The focused goal was lost during reconciliation; the mutation was not applied.")
	}
	current := c.state()
	if current == nil {
		return nil, errors.New("No focused goal to mutate.")
	}
	if spec.expectedGoalID != "" && gstr(current, "id") != spec.expectedGoalID {
		return nil, fmt.Errorf("Mutation rejected: expected goal %s but the focused goal is %s.", spec.expectedGoalID, gstr(current, "id"))
	}
	captured := gnum(current, "revision")
	lock, err := c.st.acquireLock(gstr(current, "id"), 0, 0)
	if err != nil {
		return nil, err
	}
	defer lock.release()
	var fresh *jsObject
	if p, ok := current.vals["activePath"].(string); ok {
		if file, err := c.st.resolveGoalPath(goalsDir, p); err == nil {
			fresh = parseGoalFile(file)
		}
	}
	if fresh == nil {
		return nil, fmt.Errorf("Goal %s was deleted or archived by another process while this mutation was in progress; the mutation was not applied.", gstr(current, "id"))
	}
	if diskRev := gnum(fresh, "revision"); diskRev != captured {
		return nil, fmt.Errorf("Goal %s was modified by another process (revision %s -> %s); current revision is %s. Refresh and retry; the mutation was not applied.", gstr(current, "id"), jsNumber(captured), jsNumber(diskRev), jsNumber(diskRev))
	}
	base := current
	if spec.refreshFromDisk {
		base = c.st.mergeGoalPromptFromDisk(current)
	}
	m, err := spec.mutate(cloneGoal(base))
	if err != nil {
		return nil, err
	}
	mutated := m.clone()
	mutated.set("revision", captured+1)
	var written *jsObject
	if spec.archive {
		written, err = c.st.archiveGoalFile(mutated)
	} else {
		written, err = c.st.writeActiveGoalFile(mutated)
	}
	if err != nil {
		return nil, err
	}
	if spec.ledger != nil {
		c.appendEvents(spec.ledger(written)...)
	}
	changed := false
	if !spec.noCommitFocused {
		c.setStateGoal(written)
		changed = gstr(current, "id") != gstr(written, "id")
	}
	return &mutationResult{goal: written, focusChanged: changed}, nil
}

// persist writes the focused goal back (revision + 1, new updatedAt) when nobody else changed it; used when the session ends.
func (c *core) persist() {
	cur := c.state()
	if cur == nil {
		return
	}
	captured := gnum(cur, "revision")
	lock, err := c.st.acquireLock(gstr(cur, "id"), 4, 25)
	if err != nil {
		return
	}
	defer lock.release()
	p, _ := cur.vals["activePath"].(string)
	file, err := c.st.resolveGoalPath(goalsDir, p)
	if err != nil {
		return
	}
	fresh := parseGoalFile(file)
	// A revision that moved belongs to another writer: the original merges only this session's usage delta, which this port
	// does not track, so there is nothing to write.
	if fresh == nil || gnum(fresh, "revision") != captured {
		return
	}
	n := cur.clone()
	n.set("updatedAt", nowIso())
	n.set("revision", captured+1)
	merged := c.st.mergeGoalPromptFromDisk(n)
	var written *jsObject
	if gstr(merged, "status") == "complete" {
		written, err = c.st.archiveGoalFile(merged)
	} else {
		written, err = c.st.writeActiveGoalFile(merged)
	}
	if err == nil {
		c.setStateGoal(written)
	}
}

// shutdown is session_shutdown: persist, then clear the goal UI.
func (c *core) shutdown() {
	c.persist()
	c.clearGoalWidget()
}

// create writes a new goal's file and ledger and focuses it.
func (c *core) create(goal *jsObject, events []*jsObject) (*mutationResult, error) {
	prev := c.state()
	written, err := c.st.writeActiveGoalFile(goal)
	if err != nil {
		return nil, err
	}
	c.appendEvents(events...)
	c.setStateGoal(written)
	return &mutationResult{goal: written, focusChanged: prev == nil || gstr(prev, "id") != gstr(written, "id")}, nil
}

// ---- scheduler (the part of goal-scheduler.ts the ported commands reach) ----

var errScheduling = errors.New("Goal scheduling belongs to another session or was interrupted. Use /goal-resume to take ownership.")

func (c *core) schedulerState(g *jsObject) (*jsObject, error) {
	s := g.obj("scheduler")
	if !g.has("scheduler") || s == nil {
		s = newGoalScheduler(c.h.SessionID())
	}
	if owner, _ := s.str("owner"); owner != c.h.SessionID() {
		return nil, errScheduling
	}
	if phase, _ := s.str("phase"); phase == "interrupted" {
		return nil, errScheduling
	}
	return s, nil
}

// schedulerUpdate is scheduler.update: a revision-checked write of the focused goal's scheduler state.
func (c *core) schedulerUpdate(change func(s *jsObject)) error {
	cur := c.state()
	if cur == nil {
		return errors.New("No focused goal.")
	}
	_, err := c.apply(mutation{reconcile: true, expectedGoalID: gstr(cur, "id"), mutate: func(g *jsObject) (*jsObject, error) {
		s, e := c.schedulerState(g)
		if e != nil {
			return nil, e
		}
		change(s)
		n := g.clone()
		n.set("scheduler", s)
		return n, nil
	}, ledger: func(w *jsObject) []*jsObject {
		// scheduler.write records a pause event for any write that leaves the goal paused, as the original does.
		if gstr(w, "status") != "paused" {
			return nil
		}
		r := "Scheduling paused"
		if w.has("pauseReason") && truthy(w.vals["pauseReason"]) {
			r = gstr(w, "pauseReason")
		}
		return []*jsObject{ledgerEvent("goal_paused", "goalId", gstr(w, "id"), "reason", r, "source", "agent", "at", nowIso())}
	}})
	if err != nil {
		return err
	}
	c.updateUI()
	return nil
}

func (c *core) schedulerSafe(fn func() error) {
	if err := fn(); err != nil {
		c.h.Notify("Goal scheduling stopped: "+err.Error(), "warning")
	}
}

// takeover supersedes the old scheduling claim of a goal this session owns, when the focus leaves it.
func (c *core) takeover() {
	g := c.state()
	if g == nil || !g.has("scheduler") {
		return
	}
	s := g.obj("scheduler")
	if s == nil {
		return
	}
	if owner, _ := s.str("owner"); owner != c.h.SessionID() {
		return
	}
	c.schedulerSafe(func() error {
		return c.schedulerUpdate(func(s *jsObject) {
			s.set("generation", newUUID())
			s.set("phase", "idle")
			s.set("decision", undef{})
			s.set("dispatch", undef{})
			s.set("wait", undef{})
			s.set("repairUsed", false)
		})
	})
}

func allowanceReason(limit *float64) string {
	if limit != nil && *limit == 0 {
		return "Automatic continuation disabled by maxAutonomousRuns=0. Change the setting in /goal-settings, then use /goal-resume."
	}
	return "Autonomous-run allowance exhausted. Increase or remove maxAutonomousRuns, or use /goal-resume to renew."
}

// kickoff starts the first autonomous run. This port has no scheduler: with the allowance at 0 it does what the original does
// (a no-op write and a notice); otherwise it records the goal as ready, as the original would, and says it will not run.
func (c *core) kickoff() {
	c.schedulerSafe(func() error {
		g := c.state()
		if g == nil || gstr(g, "status") != "active" || !gflag(g, "autoContinue") {
			return nil
		}
		limit := loadGoalSettings(c.h.Cwd()).maxAutonomousRuns
		if limit != nil && *limit == 0 {
			if err := c.schedulerUpdate(func(*jsObject) {}); err != nil {
				return err
			}
			c.h.Notify(allowanceReason(limit), "info")
			return nil
		}
		if err := c.schedulerUpdate(func(s *jsObject) {
			s.set("phase", "ready")
			d := newObject()
			d.set("kind", "ready")
			d.set("purpose", "kickoff")
			s.set("decision", d)
		}); err != nil {
			return err
		}
		c.h.Notify("pi-goal-x (Go port): automatic continuation is not ported, so this goal is stored and ready but will not start by itself. Set maxAutonomousRuns to 0 to silence this notice.", "warning")
		return nil
	})
}

// pause is scheduler.pause: the agent-owned stop of a goal whose allowance or wait ran out.
func (c *core) schedulerPause(reason string) {
	g := c.state()
	if g == nil {
		return
	}
	_, err := c.apply(mutation{reconcile: true, expectedGoalID: gstr(g, "id"), mutate: func(g *jsObject) (*jsObject, error) {
		if s := g.obj("scheduler"); s != nil && g.has("scheduler") {
			if owner, _ := s.str("owner"); owner != c.h.SessionID() {
				return nil, errors.New("Scheduling ownership changed; no pause was applied.")
			}
		}
		n := g.clone()
		n.set("status", "paused")
		n.set("autoContinue", false)
		n.set("stopReason", "agent")
		n.set("pauseReason", reason)
		if s := g.obj("scheduler"); s != nil && g.has("scheduler") {
			ns := s.clone()
			ns.set("generation", newUUID())
			ns.set("phase", "idle")
			ns.set("decision", undef{})
			ns.set("dispatch", undef{})
			ns.set("wait", undef{})
			n.set("scheduler", ns)
		}
		return n, nil
	}, ledger: func(w *jsObject) []*jsObject {
		r := reason
		if w.has("pauseReason") && truthy(w.vals["pauseReason"]) {
			r = gstr(w, "pauseReason")
		}
		return []*jsObject{ledgerEvent("goal_paused", "goalId", gstr(w, "id"), "reason", r, "source", "agent", "at", nowIso())}
	}})
	if err != nil {
		c.h.Notify("Goal scheduling stopped: "+err.Error(), "warning")
		return
	}
	c.updateUI()
	c.h.Notify(reason, "warning")
}

func budgetReached(g *jsObject) bool {
	b, ok := g.num("tokenBudget")
	if !ok || !g.has("tokenBudget") {
		return false
	}
	tokens, _ := usageOf(g)
	return b > 0 && tokens >= b
}

// sessionStart is the session_start handler: load the goals, let the queued UI update run (the original awaits the load), then
// restore the scheduler.
func (c *core) sessionStart() {
	c.loadState()
	c.flush()
	c.restore()
}

// restore is scheduler.restore at session start: what a session that finds its own goal does about it. The branches that would
// dispatch a run are not ported: the port says so instead of starting nothing silently.
func (c *core) restore() {
	c.schedulerSafe(func() error {
		c.reconcileFocused()
		g := c.state()
		if g == nil || gstr(g, "status") != "active" || !gflag(g, "autoContinue") || !g.has("scheduler") {
			return nil
		}
		s := g.obj("scheduler")
		if owner, _ := s.str("owner"); owner != c.h.SessionID() {
			c.h.Notify("Goal owned by another session. Use /goal-resume to take ownership.", "info")
			return nil
		}
		phase, _ := s.str("phase")
		if inList(phase, "claimed", "running", "interrupted") {
			c.schedulerPause("Previous autonomous execution was interrupted. Use /goal-resume; its dispatch will not be replayed.")
			return nil
		}
		return c.schedule()
	})
}

// schedule is the part of scheduler.schedule that stops the goal; a goal that would run is reported, not run.
func (c *core) schedule() error {
	c.reconcileFocused()
	g := c.state()
	if g == nil || gstr(g, "status") != "active" || !gflag(g, "autoContinue") || !g.has("scheduler") {
		return nil
	}
	s, err := c.schedulerState(g)
	if err != nil {
		return err
	}
	// A goal left ready for a repair run goes back to plain ready first (implicitReady), unless the user opted into strict
	// execution contracts or a wait is pending; the original writes this as its own update.
	if phase, _ := s.str("phase"); phase == "ready" && repairPending(s) && !c.strict(s) {
		if err := c.schedulerUpdate(implicitReady); err != nil {
			return err
		}
		if g = c.state(); g == nil {
			return nil
		}
		s = g.obj("scheduler")
	}
	if phase, _ := s.str("phase"); phase != "ready" && phase != "waiting" {
		return nil
	}
	if budgetReached(g) {
		c.schedulerPause("Goal token budget exhausted.")
		return nil
	}
	limit := loadGoalSettings(c.h.Cwd()).maxAutonomousRuns
	used, _ := s.num("used")
	if limit != nil && used >= *limit {
		c.schedulerPause(allowanceReason(limit))
		return nil
	}
	if w := s.obj("wait"); s.has("wait") && w != nil {
		if dl, _ := w.num("deadline"); float64(clockMs()) >= dl {
			c.schedulerPause("Wait deadline reached.")
			return nil
		}
	}
	c.h.Notify("pi-goal-x (Go port): automatic continuation is not ported, so this goal is stored and ready but will not start by itself. Set maxAutonomousRuns to 0 to silence this notice.", "warning")
	return nil
}

func repairPending(s *jsObject) bool {
	d := s.obj("decision")
	if d == nil {
		return false
	}
	kind, _ := d.str("kind")
	purpose, _ := d.str("purpose")
	return kind == "ready" && purpose == "repair"
}

// strict is the scheduler's strict(): the strictExecutionContract setting, or a pending wait.
func (c *core) strict(s *jsObject) bool {
	return loadGoalSettings(c.h.Cwd()).strictExecutionContract || (s.has("wait") && truthy(s.vals["wait"]))
}

// implicitReady turns a repair decision into a plain ready one with a new generation.
func implicitReady(s *jsObject) {
	s.set("phase", "ready")
	s.set("dispatch", undef{})
	s.set("generation", newUUID())
	d := newObject()
	d.set("kind", "ready")
	d.set("purpose", "ready")
	s.set("decision", d)
}

// ---- state operations ----

func (c *core) setFocusedGoalID(id *string, reason string, recordLedger bool) {
	prev := c.focused
	if (prev == nil) != (id == nil) || (prev != nil && *prev != *id) {
		c.takeover()
	}
	if id != nil && c.goals.has(*id) {
		c.assignFocused(id)
	} else {
		c.assignFocused(nil)
	}
	c.appendFocusEntry(c.focused, reason)
	if recordLedger && c.focused != nil {
		c.appendEvents(ledgerEvent("goal_focused", "goalId", *c.focused, "reason", reason, "at", nowIso()))
	} else if recordLedger && prev != nil {
		c.appendEvents(ledgerEvent("goal_unfocused", "reason", reason, "at", nowIso()))
	}
	c.updateUI()
}

func (c *core) openGoals() []*jsObject { return openGoalsFromPool(c.goals) }

func (c *core) replaceGoal(objective string, sisyphus bool, contract string) error {
	goal := createGoal(objective, true, sisyphus, false, clockMs())
	goal.set("scheduler", newGoalScheduler(c.h.SessionID()))
	if contract != "" {
		goal.set("verificationContract", contract)
	}
	ev := ledgerEvent("goal_created", "goalId", gstr(goal, "id"), "objective", gstr(goal, "objective"), "sisyphus", sisyphus, "autoContinue", true, "at", gstr(goal, "createdAt"))
	res, err := c.create(goal, []*jsObject{ev})
	if err != nil {
		return err
	}
	if res.focusChanged {
		c.appendFocusEntry(strp(gstr(res.goal, "id")), "created")
	}
	c.h.Notify(buildGoalRunningNotification(objective, sisyphus, true)+"\nBudget: none", "info")
	if g := c.state(); g != nil && gflag(g, "autoContinue") {
		c.kickoff()
	}
	return nil
}

func (c *core) pauseActiveGoal() {
	g := c.state()
	if g == nil || gstr(g, "status") != "active" {
		return
	}
	next := g.clone()
	next.set("autoContinue", false)
	next.set("pauseReason", undef{})
	next.set("pauseSuggestedAction", undef{})
	c.setStateGoal(next)
	res, err := c.apply(mutation{refreshFromDisk: true, mutate: func(g *jsObject) (*jsObject, error) {
		n := g.clone()
		n.set("status", "paused")
		n.set("stopReason", "user")
		n.set("updatedAt", nowIso())
		return n, nil
	}, ledger: func(w *jsObject) []*jsObject {
		return []*jsObject{ledgerEvent("goal_paused", "goalId", gstr(w, "id"), "reason", "user", "suggestedAction", pickOpt(w, "pauseSuggestedAction"), "status", "paused", "at", gstr(w, "updatedAt"))}
	}})
	_ = err
	if res != nil {
		c.updateUI()
	}
	c.h.Notify("Goal paused.", "info")
}

func pickOpt(g *jsObject, k string) any {
	if g.has(k) {
		return g.vals[k]
	}
	return undef{}
}

func (c *core) archiveCurrentGoal(reason string) *jsObject {
	if c.state() == nil {
		return nil
	}
	res, err := c.apply(mutation{refreshFromDisk: true, archive: true, noCommitFocused: true, mutate: func(g *jsObject) (*jsObject, error) {
		status := "paused"
		if gstr(g, "status") == "complete" {
			status = "complete"
		}
		n := g.clone()
		n.set("status", status)
		n.set("stopReason", reason)
		return n, nil
	}})
	if err != nil {
		return nil
	}
	return res.goal
}

// ---- commands ----

func (c *core) chooseOpenGoal(title string) *jsObject {
	c.reconcileFocused()
	if g := c.state(); g != nil && gstr(g, "status") != "complete" {
		return g
	}
	open := c.openGoals()
	if len(open) == 0 {
		return nil
	}
	if len(open) == 1 {
		c.setFocusedGoalID(strp(gstr(open[0], "id")), "selected", true)
		return c.state()
	}
	if !c.h.HasUI() {
		c.h.Notify(buildUnfocusedOpenGoalsSummary(len(open)), "warning")
		return nil
	}
	labels := make([]string, len(open))
	for i, g := range open {
		labels[i] = goalSelectorLabel(g, c.focused)
	}
	c.flush()
	picked, ok := c.h.Select(title, labels)
	var selected *string
	if ok {
		for i, l := range labels {
			if l == picked {
				selected = strp(gstr(open[i], "id"))
				break
			}
		}
	}
	if selected == nil {
		c.h.Notify("Goal focus unchanged.", "info")
		return nil
	}
	c.setFocusedGoalID(selected, "selected", true)
	return c.state()
}

func (c *core) direct(raw string, sisyphus bool) error {
	raw = jsTrim(raw)
	if raw == "" {
		cmd := "/goal <objective>"
		if sisyphus {
			cmd = "/sisyphus <objective>"
		}
		c.h.Notify("No objective provided. Use "+cmd+".", "warning")
		return nil
	}
	if sisyphus && !sisyphusObjectiveSufficient(raw) {
		c.h.Notify("A Sisyphus objective needs ordered steps with per-step done criteria. Use /sisyphus for guided drafting, or provide numbered steps (1) ..., 2) ...) in the objective.", "warning")
		return nil
	}
	objective, contract := raw, ""
	if !loadGoalSettings(c.h.Cwd()).disableContracts {
		objective, contract = extractVerificationContract(raw)
	}
	return c.replaceGoal(objective, sisyphus, contract)
}

func (c *core) list() {
	c.reconcileFocused()
	c.h.Notify(buildGoalListText(c.goals, c.focused), "info")
	c.updateUI()
}

func (c *core) pause() {
	c.reconcileFocused()
	if c.state() == nil {
		if otherOpenGoalCount(c.goals, nil) > 0 {
			picked := c.chooseOpenGoal("Pause which open goal?")
			c.flush() // the original awaits here, which lets the queued UI update run
			if picked == nil {
				return
			}
		} else {
			c.h.Notify("No goal is set.", "warning")
			return
		}
	}
	g := c.state()
	if g == nil {
		return
	}
	switch gstr(g, "status") {
	case "complete":
		c.h.Notify("Goal is complete.", "warning")
		return
	case "paused":
		c.h.Notify("Goal is already paused. Use /goal-resume to continue.", "info")
		return
	}
	c.pauseActiveGoal()
}

func (c *core) clear() {
	c.reconcileFocused()
	if c.state() == nil && otherOpenGoalCount(c.goals, nil) > 0 {
		picked := c.chooseOpenGoal("Clear which open goal?")
		c.flush()
		if picked == nil {
			return
		}
	}
	target := c.state()
	if target == nil {
		c.h.Notify(clearGoalCommandMessage(false), "warning")
		return
	}
	id, rev := gstr(target, "id"), c.focusRev
	if !c.h.HasUI() {
		c.h.Notify("Run /goal-clear in an interactive session to confirm clearing: "+oneLineSummary(target), "warning")
		return
	}
	c.flush() // awaiting the dialog lets a queued UI update run first
	if !c.h.Confirm("Clear goal?", oneLineSummary(target)) {
		c.h.Notify("Goal clear cancelled.", "info")
		return
	}
	c.reconcileFocused()
	cur := c.state()
	if c.focused == nil || *c.focused != id || c.focusRev != rev || cur == nil || gstr(cur, "id") != id {
		c.h.Notify("Goal changed while confirming; nothing was cleared.", "warning")
		return
	}
	archived := c.archiveCurrentGoal("user")
	c.setGoalNull("cleared")
	msg := clearGoalCommandMessage(archived != nil)
	level := "info"
	if archived == nil {
		level = "warning"
	}
	c.h.Notify(msg, level)
}

// setGoalNull is setGoal(null, ctx, true, reason): the focus leaves the goal and the session records why.
func (c *core) setGoalNull(reason string) {
	prev := c.state()
	c.setStateGoal(nil)
	if prev != nil && c.focused == nil {
		c.appendFocusEntry(nil, reason)
	}
	c.updateUI()
}

func (c *core) unfocus() {
	current := c.state()
	var runtimeID string
	if current != nil {
		runtimeID = gstr(current, "id")
	}
	c.reconcileFocused()
	current = c.state()
	detached := runtimeID
	if current != nil {
		detached = gstr(current, "id")
	}
	busy := !c.h.IsIdle()
	c.setFocusedGoalID(nil, "unfocused", false)
	if detached != "" && busy {
		c.h.Abort()
	}
	if current == nil {
		if n := otherOpenGoalCount(c.goals, nil); n > 0 {
			c.h.Notify(buildUnfocusedOpenGoalsSummary(n), "info")
		} else {
			c.h.Notify(detailedSummary(nil), "info")
		}
		return
	}
	c.h.Notify("Goal unfocused for this session. It remains open in .pi/goals: "+gstr(current, "id"), "info")
}
