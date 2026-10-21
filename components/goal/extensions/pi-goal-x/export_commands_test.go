package pi_goal_x

// What the GoalCommands twins (external package, through goalRig) need beyond export_test.go: the goal files, the ledger and the
// session-draft state as plain decoded JSON, and the original's `core.*` calls the tests make.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func mapJSObject(m map[string]any) *jsObject {
	b, _ := json.Marshal(m)
	v, _ := parseJSON(b)
	o, _ := v.(*jsObject)
	return o
}

// ParseGoalFileFor is parseGoalFile (nil when the file is missing or malformed).
func ParseGoalFileFor(path string) map[string]any { return jsObjectMap(parseGoalFile(path)) }

// ActiveGoalFiles are the active_goal_* names under .pi/goals, sorted.
func ActiveGoalFiles(cwd string) []string {
	entries, _ := os.ReadDir(filepath.Join(cwd, ".pi", "goals"))
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "active_goal_") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// DiskGoals are the parsed active goal files, in name order.
func DiskGoals(cwd string) []map[string]any {
	var out []map[string]any
	for _, n := range ActiveGoalFiles(cwd) {
		out = append(out, ParseGoalFileFor(filepath.Join(cwd, ".pi", "goals", n)))
	}
	return out
}

// WriteGoalFor is writeActiveGoalFile({cwd}, goal); it returns the written goal.
func WriteGoalFor(cwd string, goal map[string]any) map[string]any {
	w, err := storage{cwd: cwd}.writeActiveGoalFile(mapJSObject(goal))
	if err != nil {
		panic(err)
	}
	return jsObjectMap(w)
}

// NewGoalFor is createGoal({objective, autoContinue, sisyphus}, nowMs) as JSON.
func NewGoalFor(objective string, autoContinue, sisyphus bool, nowMs int64) map[string]any {
	return jsObjectMap(createGoal(objective, autoContinue, sisyphus, false, nowMs))
}

// NewSchedulerFor is newGoalScheduler(owner) as JSON.
func NewSchedulerFor(owner string) map[string]any { return jsObjectMap(newGoalScheduler(owner)) }

// LedgerFor is readGoalLedger({cwd}): the events and the malformed count.
func LedgerFor(cwd string) ([]map[string]any, int) {
	r := readGoalLedger(storage{cwd: cwd})
	var out []map[string]any
	for _, e := range r.events {
		out = append(out, jsObjectMap(e))
	}
	return out, r.malformed
}

// InvalidateCaches drops the pool, ledger and settings caches.
func InvalidateCaches() {
	invalidateGoalPoolCache()
	invalidateGoalLedgerCache()
	invalidateGoalSettingsCache()
}

// ReconcileFromDisk is core.reconcileFocusedGoalFromDisk(ctx).
func (p *Probe) ReconcileFromDisk() { p.c.locked(func() { p.c.reconcileFocusedGoalFromDisk(false) }) }

func (p *Probe) ArmPostBudgetReminder() { p.c.runtime.armPostBudgetReminder() }
func (p *Probe) ConsumePostBudgetReminder() bool {
	return p.c.runtime.consumePostBudgetReminder()
}

// HasActiveDraft is hasActiveDraft(core).
func (p *Probe) HasActiveDraft() bool {
	var v bool
	p.c.locked(func() { v = p.c.hasActiveDraft() })
	return v
}

// DraftMode is the mode of the active draft ("" when none).
func (p *Probe) DraftMode() string {
	var v string
	p.c.locked(func() {
		if p.c.draft != nil {
			v = p.c.draft.mode
		}
	})
	return v
}

// ProposalText is proposalText(draft, objective, autoContinue, taskList, current, tokenBudget); budget is nil (undefined), a pointer to
// a number, or null with budgetNull.
func ProposalText(mode, topic, objective string, autoContinue bool, taskList, current map[string]any, budgetSet bool, budget *float64) string {
	d := &activeGoalDraft{mode: mode, originalTopic: topic, startedAt: "now", auditorEnabled: true}
	var tl, cur *jsObject
	if taskList != nil {
		tl = mapJSObject(taskList)
	}
	if current != nil {
		cur = mapJSObject(current)
	}
	return proposalText(d, objective, autoContinue, tl, cur, draftBudget{set: budgetSet, value: budget})
}

// SettingsPath is goalSettingsPath(cwd).
func SettingsPath(cwd string) string { return goalSettingsPath(cwd) }

// SaveSettings is saveGoalSettingsFileConfig(cwd, settings).
func SaveSettings(cwd string, settings map[string]any) {
	if _, err := saveGoalSettingsFileConfig(cwd, mapJSObject(settings)); err != nil {
		panic(err)
	}
}

// DiffGoalRefreshState is diffGoalRefreshState with the pool ids, ledger counts and fingerprints.
func DiffGoalRefreshState(beforeIDs []string, beforeEvents, beforeMalformed int, beforeSettings string, afterIDs []string, afterEvents, afterMalformed int, afterSettings string) []string {
	return diffGoalRefreshState(
		goalRefreshState{poolIds: beforeIDs, ledgerEvents: beforeEvents, ledgerMalformed: beforeMalformed, settings: beforeSettings},
		goalRefreshState{poolIds: afterIDs, ledgerEvents: afterEvents, ledgerMalformed: afterMalformed, settings: afterSettings})
}

// PoolIDs are the ids of the active goal files on disk (a cold read).
func PoolIDs(cwd string) []string { return poolIDs(storage{cwd: cwd}.readActiveGoalPool()) }

// ProposeDraftGuidelines are the registered prompt guidelines of propose_goal_draft.
func ProposeDraftGuidelines() []string { return append([]string(nil), proposeDraftGuidelines...) }

// RawExternalGoal writes a goal file and its goal_created ledger line with raw file operations, bypassing the extension's own cache
// invalidation (the original's writeFileSync/appendFileSync in the refresh test). It returns the goal id.
func RawExternalGoal(cwd, objective string, autoContinue bool, nowMs int64) string {
	st := storage{cwd: cwd}
	g := createGoal(objective, autoContinue, false, false, nowMs)
	file := st.mustPath(activePathForGoal(st, g))
	if err := os.WriteFile(file, []byte(serializeGoalFile(g)), 0o644); err != nil {
		panic(err)
	}
	line := marshalJSON(ledgerEvent("goal_created", "goalId", gstr(g, "id"), "objective", objective, "sisyphus", false, "autoContinue", autoContinue, "at", nowIso()), "") + "\n"
	f, err := os.OpenFile(goalLedgerPath(st), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		panic(err)
	}
	return gstr(g, "id")
}

// LifecyclePolicyBlock is the lifecycle policy the goal prompt carries for an autonomous, non-strict goal.
func LifecyclePolicyBlock() string { return lifecyclePolicyBlock(true, false) }

// LedgerPathFor is goalLedgerPath({cwd}).
func LedgerPathFor(cwd string) string { return goalLedgerPath(storage{cwd: cwd}) }

// DashboardText is the lifecycle test's dashboardText(goal, expanded, cwd): the dashboard model derived from the persisted goal and
// its ledger events, rendered with the plain theme at width 100 (the same pipeline the widget uses).
func DashboardText(cwd string, g map[string]any, expanded bool) string {
	var events []*jsObject
	for _, e := range readGoalLedger(storage{cwd: cwd}).events {
		if gstr(e, "goalId") == g["id"] {
			events = append(events, e)
		}
	}
	model := deriveGoalDashboardModel(mapJSObject(g), goalDashboardModelOptions{focused: true, otherOpenGoals: 0, ledgerEvents: events})
	if model == nil {
		panic("dashboard model derives from the persisted goal")
	}
	if expanded {
		return strings.Join(renderExpandedDashboard(model, plainTheme, 100, goalExpandedOptions{}), "\n")
	}
	return strings.Join(renderCompactDashboard(model, plainTheme, 100, goalCompactOptions{}), "\n")
}

// AuditorCheckStates are the check states of the audit dashboard for a running audit at percentage.
func AuditorCheckStates(label string, percentage float64) []string {
	m := deriveAuditorDashboardModel(auditorProgress{phase: "running", elapsedMs: 1000, label: label, percentage: &percentage, recentOutput: []string{}}, deriveAuditorDashboardOptions{auditorLabel: "mock/auditor"})
	var out []string
	for _, c := range m.checks {
		out = append(out, c.state)
	}
	return out
}

// AuditResultVerdict is core.auditResult?.verdict ("" when none).
func (p *Probe) AuditResultVerdict() string {
	var v string
	p.c.locked(func() {
		if p.c.auditResult != nil {
			v = p.c.auditResult.verdict
		}
	})
	return v
}
