package pi_goal_x

// What the GoalTools twins (the external package, goalRig) need from the package's internals: goal fixtures written and read the
// way the original's tests do through goal-record/goal-files/goal-ledger, and Probe hooks over the core (the original's
// `pi._goalCore`, `core.goalService.apply = ...`).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ToolsGoal is createGoal(...) written as an active goal file; overrides is a JSON object whose keys replace the record's.
// The result is the written record (with activePath) as decoded JSON.
func ToolsGoal(cwd, objective string, sisyphus bool, nowMs int64, overrides string) map[string]any {
	invalidateGoalPoolCache()
	goal := createGoal(objective, true, sisyphus, false, nowMs)
	applyToolsOverrides(goal, overrides)
	written, err := storage{cwd: cwd}.writeActiveGoalFile(goal)
	if err != nil {
		panic(err)
	}
	return jsObjectMap(written)
}

func applyToolsOverrides(goal *jsObject, overrides string) {
	if overrides == "" {
		return
	}
	v, err := parseJSON([]byte(overrides))
	if err != nil {
		panic(err)
	}
	o := v.(*jsObject)
	for _, k := range o.keys {
		goal.set(k, o.vals[k])
	}
}

// ToolsWriteGoal writes a goal record (JSON) over its active file: the original's writeActiveGoalFile(ctx, record).
func ToolsWriteGoal(cwd string, goalJSON string) map[string]any {
	v, err := parseJSON([]byte(goalJSON))
	if err != nil {
		panic(err)
	}
	written, err := storage{cwd: cwd}.writeActiveGoalFile(v.(*jsObject))
	if err != nil {
		panic(err)
	}
	return jsObjectMap(written)
}

// ToolsActiveGoalFiles are the names of the active goal files, sorted.
func ToolsActiveGoalFiles(cwd string) []string {
	entries, _ := os.ReadDir(filepath.Join(cwd, ".pi", "goals"))
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "active_goal_") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// ToolsActiveGoal parses the first active goal file (parseGoalFile) as decoded JSON; nil when there is none.
func ToolsActiveGoal(cwd string) map[string]any {
	files := ToolsActiveGoalFiles(cwd)
	if len(files) == 0 {
		return nil
	}
	return ToolsParseGoalFile(filepath.Join(cwd, ".pi", "goals", files[0]))
}

// ToolsParseGoalFile is parseGoalFile(path) as decoded JSON (nil when it does not parse).
func ToolsParseGoalFile(path string) map[string]any {
	g := parseGoalFile(path)
	if g == nil {
		return nil
	}
	return jsObjectMap(g)
}

// ToolsLedgerEvents are the ledger's lines, decoded.
func ToolsLedgerEvents(cwd string) []map[string]any {
	data, err := os.ReadFile(goalLedgerPath(storage{cwd: cwd}))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// ToolsAppendLedger is appendGoalEvent(ctx, event) for a JSON event.
func ToolsAppendLedger(cwd string, eventJSON string) {
	v, err := parseJSON([]byte(eventJSON))
	if err != nil {
		panic(err)
	}
	appendGoalEvents(storage{cwd: cwd}, []*jsObject{v.(*jsObject)})
}

// ToolsFocusEntry is the session entry goalFocusDetails(goalId, "created") as the branch holds it.
func ToolsFocusEntry(goalID string) map[string]any {
	return map[string]any{"type": "custom", "customType": focusEntryType, "data": jsObjectMap(goalFocusDetails(&goalID, "created"))}
}

// ToolsStateEntry is the legacy session state entry { version: 3, goal }.
func ToolsStateEntry(goal map[string]any) map[string]any {
	return map[string]any{"type": "custom", "customType": stateEntryType, "data": map[string]any{"version": 3, "goal": goal}}
}

// ToolsSerializeGoal is serializeGoalFile for a goal record (JSON).
func ToolsSerializeGoal(goalJSON string) string {
	v, err := parseJSON([]byte(goalJSON))
	if err != nil {
		panic(err)
	}
	return serializeGoalFile(v.(*jsObject))
}

// ToolsNormalize is normalizeGoalRecord(JSON.parse(json)) as decoded JSON (nil for an unreadable record).
func ToolsNormalize(goalJSON string) map[string]any {
	v, err := parseJSON([]byte(goalJSON))
	if err != nil {
		panic(err)
	}
	g := normalizeGoalRecord(v)
	if g == nil {
		return nil
	}
	return jsObjectMap(g)
}

// ---- Probe hooks over the core ----

// HasAuditResult is core.auditResult != null.
func (p *Probe) HasAuditResult() (has bool) {
	p.c.locked(func() { has = p.c.auditResult != nil })
	return has
}

// ClearAuditResult is core.clearAuditResult().
func (p *Probe) ClearAuditResult() { p.c.locked(p.c.clearAuditResult) }

// RigAuditCall is what the injected completion auditor is given.
type RigAuditCall struct {
	Goal                                            map[string]any
	DetailedSummary, CompletionSummary, WarmContext string
}

// SetCompletionAuditor injects runCompletionAuditor after the extension was built.
func (p *Probe) SetCompletionAuditor(fn func(call RigAuditCall) RigAuditResult) {
	p.c.deps.runCompletionAuditor = func(a runGoalCompletionAuditorArgs) goalAuditorResult {
		r := fn(RigAuditCall{Goal: jsObjectMap(a.goal), DetailedSummary: a.detailedSummary, CompletionSummary: a.completionSummary, WarmContext: a.warmContext})
		return goalAuditorResult{approved: r.Approved, disapproved: r.Disapproved, output: r.Output, model: r.Model, thinkingLevel: r.ThinkingLevel, err: r.Err}
	}
}

// RejectMutations makes core.goalService.apply fail with message until the returned function restores it.
func (p *Probe) RejectMutations(message string) (restore func()) {
	p.c.locked(func() {
		p.c.goalService.applyOverride = func(goalMutationSpec) (goalMutationOutcome, error) { return goalMutationFail(message), nil }
	})
	return func() { p.c.locked(func() { p.c.goalService.applyOverride = nil }) }
}
