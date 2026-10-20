package pi_goal_x_test

// Twins of tests/integration/extension.test.ts: the handler-level integration suite. The registered tools, the commands and the
// events run through the real SDK and the fake host, with an injected auditor fixture.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

var (
	hostTools  = []string{"read", "bash", "edit", "write"}
	threeTools = []string{"create_goal", "get_goal", "update_goal"}
)

type intFixture struct {
	cwd     string
	goal    map[string]any
	entries []any
}

// newIntFixture is fixture(opts): a project with one active goal and the focus entry that selects it.
func newIntFixture(t *testing.T, skipAuditor bool) *intFixture {
	t.Helper()
	cwd := archivedCwd(t)
	g := goal.NewGoalFor("=== Goal ===\nObjective: Integration test", true, false, time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC).UnixMilli())
	if skipAuditor {
		g["skipAuditor"] = true
	}
	written := goal.WriteGoalFor(cwd, g)
	return &intFixture{cwd: cwd, goal: written, entries: []any{focusEntryFor(g["id"].(string))}}
}

func focusEntryFor(id string) map[string]any {
	return map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1.0, "focusedGoalId": id, "reason": "created"}}
}

func (f *intFixture) settingsPath() string { return goal.SettingsPath(f.cwd) }

func (f *intFixture) writeSettings(t *testing.T, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.settingsPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.settingsPath(), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *intFixture) readSettings(t *testing.T) map[string]any {
	t.Helper()
	if !fileExists(f.settingsPath()) {
		return map[string]any{}
	}
	return savedSettings(t, f.cwd)
}

func (f *intFixture) activeFiles() []string { return goal.ActiveGoalFiles(f.cwd) }

func (f *intFixture) activeGoalPath(t *testing.T) string {
	t.Helper()
	files := f.activeFiles()
	if len(files) == 0 {
		t.Fatal("no active goal file")
	}
	return filepath.Join(f.cwd, ".pi", "goals", files[0])
}

func (f *intFixture) ledger() []map[string]any {
	events, _ := goal.LedgerFor(f.cwd)
	return events
}

func (f *intFixture) hasEvent(typ string) bool {
	for _, e := range f.ledger() {
		if e["type"] == typ {
			return true
		}
	}
	return false
}

type intOpts struct {
	hasUI    bool
	entries  []any
	settings string // the settings file content; "" none
	env      map[string]string
	auditor  func(ctx context.Context, goal map[string]any, detailed, completion string) goal.RigAuditResult
	select_  func(string, []string) (string, bool)
	input    func(string, string) (string, bool)
	confirm  func(string, string) bool
	custom   func(map[string]any) map[string]any
}

// intRig is createHarness(options): integration-session, tools read/bash/edit/write.
func intRig(t *testing.T, f *intFixture, o intOpts) *goalRig {
	t.Helper()
	entries := o.entries
	if entries == nil {
		entries = f.entries
	}
	settings := o.settings
	if settings == "" {
		settings = "-"
	}
	return newGoalRig(t, rigOptions{
		Cwd: f.cwd, HasUI: boolPtr(o.hasUI), SessionID: "integration-session", Branch: entries, Settings: settings, Env: o.env,
		ActiveTools: append([]string{}, hostTools...), Select: o.select_, Input: o.input, Confirm: o.confirm, Custom: o.custom,
		Deps: goal.RigDeps{Auditor: o.auditor},
	})
}

// startInt is start(h): session_start, then before_agent_start.
func startInt(r *goalRig) {
	r.Fire("session_start", map[string]any{"reason": "start"})
	r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "go", "systemPromptOptions": map[string]any{}})
}

func turnEnd(r *goalRig) {
	r.Fire("turn_end", map[string]any{"message": map[string]any{"role": "assistant", "stopReason": "stop", "usage": map[string]any{"input": 0.0, "output": 0.0}}})
}

// toolsHistory is the captured setActiveTools calls.
func toolsHistory(r *goalRig) [][]string {
	var out [][]string
	for _, c := range r.Host.CallsTo("setActiveTools") {
		var names []string
		for _, n := range anySlice(c.Args["tools"]) {
			names = append(names, n.(string))
		}
		out = append(out, names)
	}
	return out
}

func installedProfileContains(history [][]string, names []string) bool {
	for _, snap := range history {
		all := true
		for _, n := range names {
			all = all && contains(snap, n)
		}
		if all {
			return true
		}
	}
	return false
}

func installedProfileExcludes(history [][]string, names []string) bool {
	for _, snap := range history {
		for _, n := range names {
			if contains(snap, n) {
				return false
			}
		}
	}
	return true
}

// shiftQueue is `select: async () => selects.shift()`: each call answers the next item, an exhausted queue cancels.
func shiftQueue(items ...string) func(string, []string) (string, bool) {
	var mu sync.Mutex
	return func(string, []string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if len(items) == 0 {
			return "", false
		}
		v := items[0]
		items = items[1:]
		return v, true
	}
}

func shiftInput(items ...string) func(string, string) (string, bool) {
	var mu sync.Mutex
	return func(string, string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if len(items) == 0 {
			return "", false
		}
		v := items[0]
		items = items[1:]
		return v, true
	}
}

func hasNoticeLevel(r *goalRig, level, sub string) bool {
	for _, n := range r.Notices() {
		if strings.HasPrefix(n, level+": ") && strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func auditApproved(_ context.Context, _ map[string]any, _, _ string) goal.RigAuditResult {
	return goal.RigAuditResult{Approved: true, Output: "All good\n<approved/>", Model: "fixture"}
}

func updateGoal(t *testing.T, r *goalRig, params map[string]any) json.RawMessage {
	t.Helper()
	out, failure := r.Tool("update_goal", params)
	if failure != "" {
		t.Fatalf("update_goal: %s", failure)
	}
	return out
}

func TestGoalIntegrationExtension(t *testing.T) {
	const f = "integration/extension"

	tw(t, f, "extension loading defers getActiveTools until session_start", func(t *testing.T) {
		// The fixed profile is installed at session_start; extension loading itself must not touch the active-tool API (host
		// compatibility).
		cwd := archivedCwd(t)
		calls := 0
		var mu sync.Mutex
		r := newGoalRig(t, rigOptions{Cwd: cwd, HasUI: boolPtr(false), SessionID: "s", Settings: "-"})
		prev := r.Host.opts.OnCallValue
		r.Host.opts.OnCallValue = func(method string, args map[string]any) (any, string) {
			if method == "getActiveTools" {
				mu.Lock()
				calls++
				mu.Unlock()
				return nil, "Extension runtime not initialized."
			}
			return prev(method, args)
		}
		mu.Lock()
		if calls != 0 {
			t.Errorf("no getActiveTools call during extension loading: %d", calls)
		}
		mu.Unlock()
		r.Fire("session_start", map[string]any{"reason": "start"})
		mu.Lock()
		defer mu.Unlock()
		if calls < 1 {
			t.Error("profile install at session_start calls getActiveTools once")
		}
	})

	tw(t, f, "/goal-status health is wired as a read-only command mode", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{})
		startInt(r)
		r.Command("goal-status", "health")
		n := r.LastNotice()
		matchText(t, strings.TrimPrefix(n, "info: "), `^Goal health: OK`, "health header")
		matchText(t, n, `OK Goal file:`, "goal file check")
		if len(fx.activeFiles()) != 1 {
			t.Error("health mode must not mutate goal storage")
		}
		if len(fx.ledger()) != 0 {
			t.Error("health mode must not append ledger events")
		}
	})

	tw(t, f, "/goal-refresh invalidates caches and reports external changes", func(t *testing.T) {
		fx := newIntFixture(t, false)
		// Initial settings file so the settings diff compares content, not absence -> presence.
		fx.writeSettings(t, `{"subtaskDepth":2}`)
		r := intRig(t, fx, intOpts{})
		startInt(r)

		r.Command("goal-refresh", "")
		matchText(t, r.LastNotice(), `no changes detected`, "unchanged state reports no changes")

		// External edits (raw fs, bypassing the extension's own cache invalidation): a second goal file, a ledger line, and a settings
		// rewrite with different content.
		secondID := goal.RawExternalGoal(fx.cwd, "External goal", false, time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC).UnixMilli())
		fx.writeSettings(t, `{"subtaskDepth":3,"provider":"anthropic"}`)

		r.Command("goal-refresh", "")
		report := r.LastNotice()
		matchText(t, report, `goal-refresh: re-read caches from disk — 3 change\(s\):`, "header")
		if !strings.Contains(report, "pool: 1 goal(s) added — "+secondID) {
			t.Errorf("pool addition reported for %s: %s", secondID, report)
		}
		matchText(t, report, `ledger: 0 -> 1 events`, "ledger growth")
		matchText(t, report, `settings: effective settings changed \(external edit\)`, "settings")

		// A second refresh with no new external edits reports no changes again.
		r.Command("goal-refresh", "")
		matchText(t, r.LastNotice(), `no changes detected`, "again")
	})

	tw(t, f, "update_goal(complete) with an approved auditor fixture completes and archives at turn_end", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{auditor: auditApproved})
		startInt(r)
		if !installedProfileContains(toolsHistory(r), append(append([]string{}, hostTools...), "create_goal", "get_goal", "update_goal", "set_goal_tasks")) {
			t.Error("session_start advertises active goal operations before a task tree exists")
		}
		text := toolText(t, updateGoal(t, r, map[string]any{"status": "complete"}))
		if !strings.Contains(text, "Goal audit approved") {
			t.Errorf("approval report: %s", text)
		}
		if !strings.Contains(text, "<approved/>") {
			t.Error("auditor output included")
		}
		turnEnd(r)
		if n := len(fx.activeFiles()); n != 0 {
			t.Errorf("archived at turn_end: %d", n)
		}
		approved := false
		for _, e := range fx.ledger() {
			approved = approved || (e["type"] == "audit_result" && e["verdict"] == "approved")
		}
		if !approved {
			t.Error("audit_result approved")
		}
		if !fx.hasEvent("goal_completed") {
			t.Error("goal_completed")
		}
	})

	tw(t, f, "update_goal(complete) with a disapproved auditor fixture stays open with feedback", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{auditor: func(context.Context, map[string]any, string, string) goal.RigAuditResult {
			return goal.RigAuditResult{Disapproved: true, Output: "Missing requirement\n<disapproved/>", Model: "fixture"}
		}})
		startInt(r)
		text := toolText(t, updateGoal(t, r, map[string]any{"status": "complete"}))
		if !strings.Contains(text, "Goal audit rejected") {
			t.Errorf("rejection feedback: %s", text)
		}
		if n := len(fx.activeFiles()); n != 1 {
			t.Errorf("goal stays open: %d", n)
		}
	})

	tw(t, f, "update_goal(complete) with settings.disabled skips the auditor and records audit_skipped", func(t *testing.T) {
		fx := newIntFixture(t, false)
		called := 0
		r := intRig(t, fx, intOpts{settings: `{"disabled": true}`, auditor: func(context.Context, map[string]any, string, string) goal.RigAuditResult {
			called++
			panic("must not run")
		}})
		startInt(r)
		text := toolText(t, updateGoal(t, r, map[string]any{"status": "complete"}))
		if !strings.Contains(text, "Goal audit skipped") {
			t.Errorf("skip report: %s", text)
		}
		if !strings.Contains(text, "auditor disabled in settings") {
			t.Errorf("reason: %s", text)
		}
		if called != 0 {
			t.Errorf("auditor called %d times", called)
		}
		skipped := false
		for _, e := range fx.ledger() {
			skipped = skipped || (e["type"] == "audit_skipped" && e["reason"] == "disabled")
		}
		if !skipped {
			t.Error("audit_skipped disabled recorded")
		}
	})

	tw(t, f, "update_goal(complete) honors a legacy persisted skipAuditor record", func(t *testing.T) {
		fx := newIntFixture(t, true)
		called := 0
		r := intRig(t, fx, intOpts{auditor: func(context.Context, map[string]any, string, string) goal.RigAuditResult {
			called++
			panic("must not run")
		}})
		startInt(r)
		text := toolText(t, updateGoal(t, r, map[string]any{"status": "complete"}))
		if !strings.Contains(text, "per-goal auditor disabled") {
			t.Errorf("legacy skip honored: %s", text)
		}
		if called != 0 {
			t.Errorf("auditor called %d times", called)
		}
	})

	tw(t, f, "set_goal_tasks + update_goal_task work end-to-end through the registered handlers", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{})
		startInt(r)
		out, failure := r.Tool("set_goal_tasks", map[string]any{"tasks": taskParams([3]string{"t1", "Alpha", ""}, [3]string{"t2", "Beta", ""})})
		if failure != "" {
			t.Fatal(failure)
		}
		if !toolTerminate(out) {
			t.Error("structural change terminates the turn")
		}
		r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "go2", "systemPromptOptions": map[string]any{}})
		upd, failure := r.Tool("update_goal_task", map[string]any{"task_id": "t1", "status": "complete", "evidence": "verified"})
		if failure != "" {
			t.Fatal(failure)
		}
		if text := toolText(t, upd); !strings.Contains(text, "t1 complete") {
			t.Errorf("task update result: %s", text)
		}
		g := goal.ParseGoalFileFor(fx.activeGoalPath(t))
		if taskByID(g, "t1")["status"] != "complete" || taskByID(g, "t2")["status"] != "pending" {
			t.Errorf("task statuses: %v", g["taskList"])
		}
	})

	tw(t, f, "tasks-disabled settings install the three-core profile", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{settings: `{"disableTasks": true}`})
		startInt(r)
		for _, present := range threeTools {
			if !r.Host.tools[present] {
				t.Errorf("%s registered", present)
			}
		}
		// Captured setActiveTools calls (not just registered names): the three-core profile is installed and task tools are never
		// active.
		if !installedProfileContains(toolsHistory(r), append(append([]string{}, hostTools...), threeTools...)) {
			t.Error("captured profile includes host + three core tools")
		}
		if !installedProfileExcludes(toolsHistory(r), []string{"set_goal_tasks", "update_goal_task"}) {
			t.Error("task tools are never installed when tasks are disabled")
		}
		// The executors reject task calls when disabled.
		out, failure := r.Tool("set_goal_tasks", map[string]any{"tasks": taskParams([3]string{"t1", "X", ""})})
		if failure != "" {
			t.Fatal(failure)
		}
		if text := toolText(t, out); !strings.Contains(text, "disabled by settings") {
			t.Errorf("task tool disabled message: %s", text)
		}
	})

	tw(t, f, "goal-settings menu toggles the disabled switch through the registered handler", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{hasUI: true, select_: shiftQueue("  auditor disabled: false (default)", "Set project override to true", "Done")})
		startInt(r)
		if !r.Host.cmds["goal-settings"] {
			t.Fatal("goal-settings command registered")
		}
		r.Command("goal-settings", "") // must not throw (regression: recursive saveSettings)
		if fx.readSettings(t)["disabled"] != true {
			t.Errorf("menu toggle must persist disabled: true: %v", fx.readSettings(t))
		}
	})

	tw(t, f, "goal-settings menu editing persists a field without stack overflow", func(t *testing.T) {
		fx := newIntFixture(t, false)
		fx.writeSettings(t, `{"disabled": true}`)
		r := intRig(t, fx, intOpts{hasUI: true, settings: `{"disabled": true}`,
			select_: shiftQueue("  thinking_level: (default) (default)", "off", "Done"), input: shiftInput("off")})
		startInt(r)
		r.Command("goal-settings", "")
		saved := fx.readSettings(t)
		if saved["disabled"] != true {
			t.Error("existing setting preserved")
		}
		if saved["thinking_level"] != "off" {
			t.Errorf("thinking_level edited through the menu: %v", saved)
		}
	})
}
