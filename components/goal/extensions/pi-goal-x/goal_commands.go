package pi_goal_x

// The sixteen commands (goal-commands.ts registerGoalCommands). Blocking dialogs yield the core through c.awaiting, as the
// original's `await` yields the event loop.

import (
	"os"
	"sort"
	"strconv"
	"strings"
)

// goalCommandOrder is the registration order of the original's 16 commands (pinned by goal-surface-baseline).
var goalCommandOrder = []string{"goal", "sisyphus", "goal-cancel", "goal-direct", "sisyphus-direct", "goal-list", "goal-status", "goal-refresh",
	"goal-recovery", "goal-focus", "goal-unfocus", "goal-settings", "goal-tweak", "goal-clear", "goal-pause", "goal-resume"}

// registerGoalCommands registers the commands on the core's extension, in the original's order.
func registerGoalCommands(c *core) {
	for _, name := range goalCommandOrder {
		for _, d := range commandDefs {
			if d.name == name {
				c.pi.command(d.name, d.desc, d.fn)
			}
		}
	}
}

// commandDefs are the commands, with the original's descriptions; the tests drive the same table.
var commandDefs = []struct {
	name, desc string
	fn         func(c *core, args string) error
}{
	{"goal", "Draft a regular goal with clarification, task planning, and confirmation.", func(c *core, args string) error { c.startGoalDrafting("goal", args, nil); return nil }},
	{"sisyphus", "Draft a Sisyphus goal with clarification, task planning, and confirmation.", func(c *core, args string) error { c.startGoalDrafting("sisyphus", args, nil); return nil }},
	{"goal-cancel", "Cancel the in-progress guided draft without creating or modifying a goal.", func(c *core, _ string) error { c.cancelDraftCommand(); return nil }},
	{"goal-direct", "Create and start a regular goal immediately, without drafting.", func(c *core, args string) error { return c.direct(args, false) }},
	{"sisyphus-direct", "Create and start a Sisyphus goal immediately, without drafting.", func(c *core, args string) error { return c.direct(args, true) }},
	{"goal-list", "List all open goals and the current focus.", func(c *core, _ string) error { c.list(); return nil }},
	{"goal-status", "Show the unified goal dashboard (read-only). Append \"verbose\" for full detail or \"health\" for storage/runtime checks.", func(c *core, args string) error { return c.showGoalStatus(args) }},
	{"goal-refresh", "Re-read goal storage caches (pool, ledger, settings) from disk and report what changed. Picks up external edits to .pi files — no file watchers needed.", func(c *core, _ string) error { c.runGoalRefresh(); return nil }},
	{"goal-recovery", "Read-only storage/recovery report (malformed goal files, malformed ledger lines, stale locks, orphaned snapshot data). Append \"repair\" to remove stale locks and refresh the pool snapshot after confirmation (with backup).", func(c *core, args string) error { return c.runGoalRecovery(jsTrim(args)) }},
	{"goal-focus", "Choose which open goal this session focuses on.", func(c *core, _ string) error { c.focusGoalCommand(); return nil }},
	{"goal-unfocus", "Stop focusing the current goal (session only; goal stays open).", func(c *core, _ string) error { c.unfocus(); return nil }},
	{"goal-settings", "Open pi-goal settings (auditor provider/model/thinking level).", func(c *core, _ string) error { c.handleSettingsMenu(); return nil }},
	{"goal-tweak", "Revise the current goal or its token budget with confirmation.", func(c *core, args string) error { c.runGoalTweak(args); return nil }},
	{"goal-clear", "Archive the current goal after confirmation (user-owned abandonment).", func(c *core, _ string) error { c.clear(); return nil }},
	{"goal-pause", "Pause the currently running goal. Esc also pauses while running.", func(c *core, _ string) error { c.pause(); return nil }},
	{"goal-resume", "Continue now and renew the configured autonomous-run allowance.", func(c *core, _ string) error { c.handleGoalResume(); return nil }},
}

// ---- goal-refresh ----

// goalRefreshState is GoalRefreshState.
type goalRefreshState struct {
	poolIds         []string
	ledgerEvents    int
	ledgerMalformed int
	// settings is the stable fingerprint of the effective settings (JSON of the parsed file).
	settings string
}

// diffGoalRefreshState is the pure diff of a goal-refresh cycle (before invalidation vs after re-read).
func diffGoalRefreshState(before, after goalRefreshState) []string {
	var changes []string
	beforeIds, afterIds := map[string]bool{}, map[string]bool{}
	for _, id := range before.poolIds {
		beforeIds[id] = true
	}
	for _, id := range after.poolIds {
		afterIds[id] = true
	}
	var added, removed []string
	for id := range afterIds {
		if !beforeIds[id] {
			added = append(added, id)
		}
	}
	for id := range beforeIds {
		if !afterIds[id] {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	if len(added) > 0 {
		changes = append(changes, "pool: "+strconv.Itoa(len(added))+" goal(s) added — "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		changes = append(changes, "pool: "+strconv.Itoa(len(removed))+" goal(s) removed — "+strings.Join(removed, ", "))
	}
	if after.ledgerEvents != before.ledgerEvents {
		changes = append(changes, "ledger: "+strconv.Itoa(before.ledgerEvents)+" -> "+strconv.Itoa(after.ledgerEvents)+" events")
	}
	if after.ledgerMalformed != before.ledgerMalformed {
		changes = append(changes, "ledger: malformed entries "+strconv.Itoa(before.ledgerMalformed)+" -> "+strconv.Itoa(after.ledgerMalformed))
	}
	if before.settings != after.settings {
		changes = append(changes, "settings: effective settings changed (external edit)")
	}
	return changes
}

func poolIDs(p *goalPool) []string {
	ids := make([]string, 0, p.size())
	for _, g := range p.values() {
		ids = append(ids, gstr(g, "id"))
	}
	return ids
}

// settingsFingerprint is the stable fingerprint of the effective settings (cache-served before invalidation).
func (c *core) settingsFingerprint() string {
	return marshalJSON(loadGoalSettingsFileConfig(c.h.Cwd()), "")
}

func (c *core) runGoalRefresh() {
	// Pre-state snapshots are cache-served (0 fs ops); the re-reads after invalidation go cold. This is the explicit user-owned path
	// for picking up external edits — no watchers, no per-turn polling.
	beforePool := c.st.readActiveGoalPool()
	beforeLedger := readGoalLedger(c.st)
	beforeSettings := c.settingsFingerprint()

	invalidateGoalPoolCache()
	invalidateGoalLedgerCache()
	invalidateGoalSettingsCache()

	afterPool := c.st.readActiveGoalPool()
	afterLedger := readGoalLedger(c.st)
	afterSettings := c.settingsFingerprint()

	changes := diffGoalRefreshState(
		goalRefreshState{poolIds: poolIDs(beforePool), ledgerEvents: len(beforeLedger.events), ledgerMalformed: beforeLedger.malformed, settings: beforeSettings},
		goalRefreshState{poolIds: poolIDs(afterPool), ledgerEvents: len(afterLedger.events), ledgerMalformed: afterLedger.malformed, settings: afterSettings})
	text := "goal-refresh: no changes detected — caches were already current."
	if len(changes) > 0 {
		lines := make([]string, len(changes))
		for i, ch := range changes {
			lines[i] = "  - " + ch
		}
		text = "goal-refresh: re-read caches from disk — " + strconv.Itoa(len(changes)) + " change(s):\n" + strings.Join(lines, "\n")
	}
	c.h.Notify(text, "info")
	c.updateUI()
}

// ---- goal-recovery / goal-status ----

func (c *core) runGoalRecovery(rawArgs string) error {
	report := runRecoveryReport(c.st)
	if strings.EqualFold(rawArgs, "repair") {
		var confirmed bool
		result, err := runRecoveryRepair(c.st, report, func() bool {
			c.awaiting(func() {
				confirmed = c.h.Confirm("Remove "+strconv.Itoa(len(report.staleLocks))+" stale lock(s) and refresh the pool snapshot?", "Files are backed up to .pi/goals/.recovery-backup first.")
			})
			return confirmed
		})
		if err != nil {
			return err
		}
		if result.confirmed {
			if len(result.applied) > 0 {
				lines := make([]string, len(result.applied))
				for i, a := range result.applied {
					lines[i] = "  - " + a
				}
				backup := "null"
				if result.backupDir != nil {
					backup = *result.backupDir
				}
				c.h.Notify("goal-recovery repair: "+strconv.Itoa(len(result.applied))+" operation(s) applied.\n"+strings.Join(lines, "\n")+"\nBackup: "+backup, "info")
			} else {
				c.h.Notify("goal-recovery repair: nothing to repair.", "info")
			}
		} else {
			c.h.Notify("goal-recovery repair: cancelled — nothing changed.", "info")
		}
		return nil
	}
	// Issue #30: surface persisted-checkpoint health for this session file (read-only). Legacy full checkpoints are repairable offline
	// with the shipped pi-goal-x-recover CLI; this command never writes.
	sessionFile := c.h.SessionFile()
	var checkpointHealth *checkpointHealth
	if sessionFile != "" {
		checkpointHealth = readSessionCheckpointHealth(sessionFile)
	}
	section := ""
	if checkpointHealth != nil && checkpointHealth.total > 0 {
		section = "\n\n" + formatCheckpointHealthReport(checkpointHealth, &sessionFile)
	}
	level := "warning"
	if report.healthy && (checkpointHealth == nil || checkpointHealth.legacyFull == 0) {
		level = "info"
	}
	c.h.Notify(formatRecoveryReport(report)+section, level)
	return nil
}

func (c *core) showGoalStatus(rawArgs string) error {
	c.reconcileFocusedGoalFromDisk(false)
	if c.state() != nil {
		c.syncGoalPromptFromDisk()
	}
	view := c.goalForDisplay()
	if view == nil {
		view = c.state()
	}
	var focusedID *string = c.focused
	otherCount := otherOpenGoalCount(c.goals, focusedID)
	verbose := statusFlagRe("verbose").MatchString(rawArgs)
	health := statusFlagRe("health").MatchString(rawArgs)
	ledger := readGoalLedger(c.st)
	opts := goalStatusTextOptions{
		maxAutonomousRuns: loadGoalSettings(c.h.Cwd()).maxAutonomousRuns,
		goal:              view,
		focused:           view != nil && c.focused != nil && *c.focused == gstr(view, "id"),
		otherOpenGoals:    otherCount,
		ledgerEvents:      ledger.events,
		ledgerMalformed:   ledger.malformed,
		verbose:           verbose,
		health:            health,
	}
	sessionFile := c.h.SessionFile()
	if health && view != nil && gstr(view, "activePath") != "" {
		present := false
		if p, err := c.st.path(gstr(view, "activePath")); err == nil {
			_, statErr := os.Stat(p)
			present = statErr == nil
		}
		opts.activeFilePresent = &present
	}
	if health {
		// Issue #30: checkpoint growth report (health view only, read-only).
		opts.checkpointHealth = readSessionCheckpointHealth(sessionFile)
	}
	if sessionFile != "" {
		opts.checkpointSessionFile = &sessionFile
	}
	if verbose {
		// §13.2: effective settings with provenance appear only in verbose mode; the standard mode stays free of settings noise (§13.1).
		opts.settingsReport = append(effectiveSettingsReport(c.h.Cwd()), "effective goal pool: "+c.st.root())
	}
	c.h.Notify(buildGoalStatusText(opts), "info")
	c.updateUI()
	return nil
}

// ---- focus ----

func (c *core) chooseOpenGoal(title string) *jsObject {
	c.reconcileFocusedGoalFromDisk(false)
	if g := c.state(); g != nil && gstr(g, "status") != "complete" {
		return g
	}
	open := c.openGoals()
	if len(open) == 0 {
		return nil
	}
	if len(open) == 1 {
		c.setFocusedGoalId(new(gstr(open[0], "id")), "selected", true)
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
	c.enterGoalModal()
	defer c.exitGoalModal()
	selectedID, ok := c.selectOpenGoalID(title, labels, open)
	if !ok {
		c.h.Notify("Goal focus unchanged.", "info")
		return nil
	}
	c.setFocusedGoalId(&selectedID, "selected", true)
	return c.state()
}

// selectOpenGoalID shows the picker and maps the chosen label back to its goal id (the first goal with that label).
func (c *core) selectOpenGoalID(title string, labels []string, open []*jsObject) (string, bool) {
	var picked string
	var ok bool
	c.awaiting(func() { picked, ok = c.h.Select(title, labels) })
	if !ok || picked == "" {
		return "", false
	}
	for i, l := range labels {
		if l == picked {
			return gstr(open[i], "id"), true
		}
	}
	return "", false
}

func (c *core) focusGoalCommand() {
	open := c.openGoals()
	if len(open) == 0 {
		c.h.Notify("No open goals. Use /goal to draft one, or /goal-direct <objective> to start immediately.", "warning")
		return
	}
	if len(open) == 1 {
		c.setFocusedGoalId(new(gstr(open[0], "id")), "selected", true)
		c.armFocusedContinuation()
		c.h.Notify("Focused goal: "+oneLineSummary(open[0]), "info")
		return
	}
	if !c.h.HasUI() {
		c.h.Notify(buildGoalListText(c.goals, c.focused), "info")
		return
	}
	labels := make([]string, len(open))
	for i, g := range open {
		labels[i] = goalSelectorLabel(g, c.focused)
	}
	func() {
		c.enterGoalModal()
		defer c.exitGoalModal()
		selectedID, ok := c.selectOpenGoalID("Focus open goal", labels, open)
		if !ok {
			c.h.Notify("Goal focus unchanged.", "info")
			return
		}
		c.setFocusedGoalId(&selectedID, "selected", true)
		c.armFocusedContinuation()
		c.h.Notify("Focused goal: "+oneLineSummary(c.state()), "info")
	}()
}

func (c *core) unfocus() {
	var runtimeID string
	switch {
	case c.state() != nil:
		runtimeID = gstr(c.state(), "id")
	case c.runningGoalID != nil:
		runtimeID = *c.runningGoalID
	case c.runtime.getCheckpointGoalId() != nil:
		runtimeID = *c.runtime.getCheckpointGoalId()
	}
	c.reconcileFocusedGoalFromDisk(false)
	current := c.state()
	detached := runtimeID
	if current != nil {
		detached = gstr(current, "id")
	}
	wasBusy := !c.h.IsIdle()
	if detached != "" && wasBusy {
		c.runtime.markTurnStopped(detached)
	}
	c.setFocusedGoalId(nil, "unfocused", false)
	c.runningGoalID = nil
	c.runtime.setCheckpoint(nil)
	c.runtime.clearPostCompactReminder()
	if c.auditAbortController != nil {
		c.auditAbortController()
	}
	if detached != "" && wasBusy {
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

// ---- create / list / pause / resume / clear / tweak ----

// direct is handleDirectGoalSet.
func (c *core) direct(rawObjective string, sisyphus bool) error {
	raw := jsTrim(rawObjective)
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
	c.clearGoalDrafting()
	c.clearContinuationState(true)
	c.clearActiveAccounting()
	c.replaceGoal(goalCreationConfig{objective: objective, autoContinue: true, sisyphus: sisyphus}, true, contract, -1)
	return nil
}

func (c *core) list() {
	c.reconcileFocusedGoalFromDisk(false)
	c.h.Notify(buildGoalListText(c.goals, c.focused), "info")
	c.updateUI()
}

func (c *core) pause() {
	c.reconcileFocusedGoalFromDisk(false)
	if c.state() == nil {
		if otherOpenGoalCount(c.goals, nil) > 0 {
			if c.chooseOpenGoalAwaited("Pause which open goal?") == nil {
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

func (c *core) handleGoalResume() {
	c.reconcileFocusedGoalFromDisk(false)
	if c.state() == nil && otherOpenGoalCount(c.goals, nil) > 0 {
		if c.chooseOpenGoalAwaited("Resume or focus open goal") == nil {
			return
		}
	}
	g := c.state()
	if g == nil {
		c.h.Notify("No goal is focused.", "warning")
		return
	}
	if !c.scheduler.resume() {
		return
	}
	c.h.Notify("Goal resumed; autonomous allowance renewed.", "info")
	// Append ledger event for resumption (a ledger append failure must not crash resume).
	c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("goal_resumed", "goalId", gstr(g, "id"), "reason", "user", "at", nowIso())})
}

func (c *core) clear() {
	c.reconcileFocusedGoalFromDisk(false)
	if c.state() == nil && otherOpenGoalCount(c.goals, nil) > 0 {
		if c.chooseOpenGoalAwaited("Clear which open goal?") == nil {
			return
		}
	}
	target := c.state()
	if target == nil {
		c.h.Notify(clearGoalCommandMessage(false), "warning")
		return
	}
	// Snapshot the selected goal id and focus revision before asking.
	token := c.focusedOperationToken(gstr(target, "id"))
	// Headless behavior is explicit: guidance without mutation. Clearing requires an interactive confirmation.
	if !c.h.HasUI() {
		c.h.Notify("Run /goal-clear in an interactive session to confirm clearing: "+oneLineSummary(target), "warning")
		return
	}
	var confirmed bool
	c.awaiting(func() { confirmed = c.h.Confirm("Clear goal?", oneLineSummary(target)) })
	if !confirmed {
		c.h.Notify("Goal clear cancelled.", "info")
		return
	}
	// Reconcile and validate the same focus token after confirmation, then archive. Cancellation above changes no file, focus entry,
	// ledger entry, or runtime state.
	c.reconcileFocusedGoalFromDisk(false)
	cur := c.state()
	if !c.isFocusedOperationCurrent(token) || cur == nil || gstr(cur, "id") != token.goalId {
		c.h.Notify("Goal changed while confirming; nothing was cleared.", "warning")
		return
	}
	archived := c.archiveCurrentGoal("user")
	c.setGoal(nil, true, "cleared")
	level := "info"
	if archived == nil {
		level = "warning"
	}
	c.h.Notify(clearGoalCommandMessage(archived != nil), level)
}

func (c *core) runGoalTweak(replacement string) {
	c.reconcileFocusedGoalFromDisk(false)
	if c.state() == nil {
		if otherOpenGoalCount(c.goals, nil) > 0 {
			if c.chooseOpenGoalAwaited("Tweak which open goal?") == nil {
				return
			}
		} else {
			c.h.Notify("No goal is set. Use /goal to draft one, or /goal-direct <objective> to create one immediately.", "warning")
			return
		}
	}
	current := c.state()
	if current == nil {
		return
	}
	if gstr(current, "status") == "complete" {
		c.h.Notify("Goal is complete. Use /goal to draft a new one, or /goal-direct <objective> to create one immediately.", "warning")
		return
	}
	trimmed := jsTrim(replacement)
	if trimmed == "" {
		c.h.Notify("Describe the requested change: /goal-tweak <change>, for example remove the token budget", "info")
		return
	}
	if max := loadGoalSettings(c.h.Cwd()).objectiveMaxChars; max > 0 && float64(utf16Len(trimmed)) > max {
		c.h.Notify("Requested change exceeds "+jsNumber(max)+" characters ("+strconv.Itoa(utf16Len(trimmed))+").", "warning")
		return
	}
	c.startGoalDrafting("tweak", trimmed, current)
}

func (c *core) cancelDraftCommand() {
	if !c.hasActiveDraft() {
		c.h.Notify("No active draft to cancel.", "info")
		return
	}
	c.clearGoalDrafting()
	c.clearContinuationState(true)
	c.h.Notify("Draft cancelled; no goal was created. The execution profile is restored.", "info")
}

// statusFlagRe is /^\s*<word>\b/i.
func statusFlagRe(word string) interface{ MatchString(string) bool } {
	return statusFlagMatcher{word}
}

type statusFlagMatcher struct{ word string }

func (m statusFlagMatcher) MatchString(s string) bool {
	rest := strings.TrimLeftFunc(s, isJSSpace)
	if len(rest) < len(m.word) || !strings.EqualFold(rest[:len(m.word)], m.word) {
		return false
	}
	after := rest[len(m.word):]
	if after == "" {
		return true
	}
	r := after[0]
	return !(r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'))
}

// chooseOpenGoalAwaited is `await chooseOpenGoal(...)`: even an immediately settled await lets the UI update queued meanwhile run.
func (c *core) chooseOpenGoalAwaited(title string) *jsObject {
	g := c.chooseOpenGoal(title)
	c.flush()
	return g
}
