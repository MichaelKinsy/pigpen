package pi_goal_x_test

// Twins of tests/integration/extension.test.ts, second half: the goal-settings menu rows, the confirmation and audit UX, the
// completion transaction hardening and the capability parity cases.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

// pickQueue is `queue.find(q => options.includes(q))`: it answers the first queued item the menu offers, else "Done".
func pickQueue(items ...string) func(string, []string) (string, bool) {
	var mu sync.Mutex
	queue := append([]string{}, items...)
	return func(_ string, options []string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		for i, q := range queue {
			if contains(options, q) {
				queue = append(queue[:i:i], queue[i+1:]...)
				return q, true
			}
		}
		return "Done", true
	}
}

func TestGoalIntegrationSettings(t *testing.T) {
	const f = "integration/extension"

	tw(t, f, "displays every persisted settings row and reflects file values", func(t *testing.T) {
		fx := newIntFixture(t, false)
		settings := `{"disableTasks": true, "subtaskDepth": 3, "provider": "anthropic", "thinking_level": "high", "disabled": true}`
		fx.writeSettings(t, settings)
		var first []string
		r := intRig(t, fx, intOpts{hasUI: true, settings: settings, select_: func(_ string, options []string) (string, bool) {
			if first == nil {
				first = append([]string{}, options...)
			}
			return "Done", true
		}})
		startInt(r)
		r.Command("goal-settings", "")
		var lines []string
		for _, o := range first {
			if strings.HasPrefix(o, "  ") && !strings.HasPrefix(o, "  ───") {
				lines = append(lines, o)
			}
		}
		if len(lines) != 20 {
			t.Errorf("all twenty rows rendered, got: %s", strings.Join(lines, " | "))
		}
		for _, want := range []string{
			"  show autonomous runs line: true (default)", "  auditor disabled: true (project override)", "  provider: anthropic (project override)",
			"  model: (default) (default)", "  thinking_level: high (project override)", "  disableTasks: true (project override)",
			"  disableContracts: false (default)", "  explicit execution contracts (opt-in): false (default)", "  subtaskDepth: 3 (project override)",
			"  autoSelectSingleGoal: false (default)", "  hideUnfocusedBanner: false (default)", "  hideUnfocusedPrompt: false (default)",
			"  stall timeout (minutes): 0 (default)", "  max objective length (0 = none): 0 (default)",
		} {
			if !contains(lines, want) {
				t.Errorf("missing row %q in %v", want, lines)
			}
		}
	})

	tw(t, f, "toggles every boolean row directly and persists each value", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{hasUI: true, select_: shiftQueue(
			"  disableTasks: false (default)", "Set project override to true",
			"  disableContracts: false (default)", "Set project override to true",
			"  auditor disabled: false (default)", "Set project override to true",
			"  autoSelectSingleGoal: false (default)", "Set project override to true",
			"Done")})
		startInt(r)
		r.Command("goal-settings", "")
		saved := fx.readSettings(t)
		for _, k := range []string{"disableTasks", "disableContracts", "disabled", "autoSelectSingleGoal"} {
			if saved[k] != true {
				t.Errorf("%s = %v", k, saved[k])
			}
		}
		// Second pass removes the local overrides via the action menu's inherit choice; the file then omits both keys.
		r2 := intRig(t, fx, intOpts{hasUI: true, settings: `{"disableTasks":true,"disableContracts":true,"disabled":true,"autoSelectSingleGoal":true}`, select_: shiftQueue(
			"  disableTasks: true (project override)", "Use inherited value",
			"  autoSelectSingleGoal: true (project override)", "Use inherited value", "Done")})
		startInt(r2)
		r2.Command("goal-settings", "")
		saved2 := fx.readSettings(t)
		if _, has := saved2["disableTasks"]; has {
			t.Error("disableTasks toggled back off clears the key")
		}
		if _, has := saved2["autoSelectSingleGoal"]; has {
			t.Error("autoSelectSingleGoal toggled back off clears the key")
		}
		if saved2["disableContracts"] != true || saved2["disabled"] != true {
			t.Errorf("untouched boolean preserved: %v", saved2)
		}
	})

	tw(t, f, "edits and clears provider and model text fields", func(t *testing.T) {
		fx := newIntFixture(t, false)
		// Set both via the manual provider/model entry: filter first, then pick "✎ Enter provider/model manually (advanced)", then
		// type the pair.
		r := intRig(t, fx, intOpts{hasUI: true,
			select_: shiftQueue("  provider: (default) (default)", "✎ Enter provider/model manually (advanced)",
				"  model: claude-sonnet-4 (project override)", "✎ Enter provider/model manually (advanced)", "Done"),
			input: shiftInput("", "anthropic/claude-sonnet-4", "", "anthropic/claude-sonnet-4")})
		startInt(r)
		r.Command("goal-settings", "")
		saved := fx.readSettings(t)
		if saved["provider"] != "anthropic" || saved["model"] != "claude-sonnet-4" {
			t.Errorf("provider/model %v", saved)
		}
		// Clear both by returning to the current-session/default choice from either row: the default choice deletes provider and model
		// together.
		r2 := intRig(t, fx, intOpts{hasUI: true, settings: `{"provider":"anthropic","model":"claude-sonnet-4"}`,
			select_: shiftQueue("  provider: anthropic (project override)", "  Current session / default (system default)", "Done"),
			input:   shiftInput("")})
		startInt(r2)
		r2.Command("goal-settings", "")
		saved2 := fx.readSettings(t)
		if _, has := saved2["provider"]; has {
			t.Error("default choice clears provider")
		}
		if _, has := saved2["model"]; has {
			t.Error("default choice clears model")
		}
	})

	tw(t, f, "accepts every thinking level and rejects unknown values", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{hasUI: true,
			select_: shiftQueue(
				"  thinking_level: (default) (default)", "off",
				"  thinking_level: off (project override)", "minimal",
				"  thinking_level: minimal (project override)", "low",
				"  thinking_level: low (project override)", "medium",
				"  thinking_level: medium (project override)", "high",
				"  thinking_level: high (project override)", "xhigh", "Done"),
			input: shiftInput("off", "minimal", "low", "medium", "high", "xhigh")})
		startInt(r)
		r.Command("goal-settings", "")
		if fx.readSettings(t)["thinking_level"] != "xhigh" {
			t.Errorf("all six levels accepted in sequence: %v", fx.readSettings(t))
		}
		// Unknown value is rejected with a warning and nothing is persisted.
		r2 := intRig(t, fx, intOpts{hasUI: true, settings: `{"thinking_level":"xhigh"}`,
			select_: shiftQueue("  thinking_level: xhigh (project override)", "bogus", "Done"), input: shiftInput("bogus")})
		startInt(r2)
		r2.Command("goal-settings", "")
		if !hasNoticeLevel(r2, "warning", "thinking_level must be one of") {
			t.Errorf("unknown thinking level warns: %v", r2.Notices())
		}
		if fx.readSettings(t)["thinking_level"] != "xhigh" {
			t.Error("rejected value is not persisted")
		}
	})

	tw(t, f, "subtaskDepth accepts 1 and rejects 1.5, 1x, zero, negative, infinity, and unsafe values", func(t *testing.T) {
		fx := newIntFixture(t, false)
		rejects := []string{"1.5", "1x", "0", "-3", "Infinity", "9007199254740992", "", "1"}
		var selects []string
		for range rejects {
			selects = append(selects, "  subtaskDepth: 1 (default)", "Set project override...")
		}
		selects = append(selects, "Done")
		r := intRig(t, fx, intOpts{hasUI: true, select_: shiftQueue(selects...), input: shiftInput(rejects...)})
		startInt(r)
		r.Command("goal-settings", "")
		warnings := 0
		for _, n := range r.Notices() {
			if strings.HasPrefix(n, "warning: ") && strings.Contains(n, "subtaskDepth") {
				warnings++
			}
		}
		if warnings != len(rejects)-1 {
			t.Errorf("every invalid input warns: %d", warnings)
		}
		if fx.readSettings(t)["subtaskDepth"] != 1.0 {
			t.Errorf("only the valid input is persisted: %v", fx.readSettings(t))
		}
	})

	tw(t, f, "toggling tasks off, on, and off in one menu session reinstalls the correct fixed profile each time", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{hasUI: true, select_: shiftQueue(
			"  disableTasks: false (default)", "Set project override to true",
			"  disableTasks: true (project override)", "Use inherited value",
			"  disableTasks: false (default)", "Set project override to true", "Done")})
		startInt(r)
		// session_start installed the five-tool profile: history[0].
		if n := len(toolsHistory(r)); n != 1 {
			t.Fatalf("one install at session_start: %d", n)
		}
		r.Command("goal-settings", "")
		// Each real change reinstalls exactly once: three, five, three.
		hist := toolsHistory(r)
		if len(hist) != 4 {
			t.Fatalf("three reinstalls after session_start: %d", len(hist))
		}
		var installed []string
		for _, tools := range hist[1:] {
			if contains(tools, "set_goal_tasks") {
				installed = append(installed, "five")
			} else {
				installed = append(installed, "three")
			}
		}
		if strings.Join(installed, ",") != "three,five,three" {
			t.Errorf("profile alternates three/five/three, never skipping a toggle: %v", installed)
		}
		if fx.readSettings(t)["disableTasks"] != true {
			t.Error("final file state has tasks disabled")
		}
	})

	tw(t, f, "hideUnfocusedBanner hides and restores the unfocused chrome live (PR #29)", func(t *testing.T) {
		fx := newIntFixture(t, false)
		// Two open goals, nothing focused: the unfocused banner renders.
		second := map[string]any{}
		for k, v := range fx.goal {
			second[k] = v
		}
		second["id"], second["objective"] = "second-open-goal", "Second open goal for the banner test"
		delete(second, "activePath")
		goal.WriteGoalFor(fx.cwd, second)
		entries := []any{map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1.0, "focusedGoalId": nil, "reason": "unfocused"}}}
		unfocusedShown := func(r *goalRig) bool {
			for _, s := range r.Statuses() {
				if s.Key == "goal" && strings.Contains(s.Text, "goal: unfocused") {
					return true
				}
			}
			return false
		}
		r := intRig(t, fx, intOpts{hasUI: true, entries: entries, select_: func(string, []string) (string, bool) { return "Done", true }})
		startInt(r)
		r.Host.Calls() // the coalesced UI update has run when the handler returned
		if !unfocusedShown(r) {
			t.Errorf("unfocused hint visible before the setting: %v", r.Statuses())
		}

		// Turn the setting on through the menu; the hide must be visible in captured UI state before the handler resolves.
		r2 := intRig(t, fx, intOpts{hasUI: true, entries: entries, select_: pickQueue("  hideUnfocusedBanner: false (default)", "Set project override to true", "Done")})
		startInt(r2)
		r2.Command("goal-settings", "")
		cleared := false
		for _, s := range r2.Statuses() {
			cleared = cleared || (s.Key == "goal" && s.Text == "")
		}
		widgetCleared := false
		for _, w := range r2.Widgets() {
			widgetCleared = widgetCleared || w.Clear
		}
		if !cleared || !widgetCleared {
			t.Errorf("live hide clears status + widget: %v / %v", r2.Statuses(), r2.Widgets())
		}
		if fx.readSettings(t)["hideUnfocusedBanner"] != true {
			t.Error("hideUnfocusedBanner persisted")
		}

		// Restore via the action menu's inherit choice.
		r3 := intRig(t, fx, intOpts{hasUI: true, entries: entries, settings: `{"hideUnfocusedBanner": true}`,
			select_: pickQueue("  hideUnfocusedBanner: true (project override)", "Use inherited value", "Done")})
		startInt(r3)
		r3.Command("goal-settings", "")
		if !unfocusedShown(r3) {
			t.Errorf("live restore re-renders the unfocused hint: %v", r3.Statuses())
		}
	})

	tw(t, f, "headless /goal-settings reports the settings file path without editing", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{})
		startInt(r)
		r.Command("goal-settings", "")
		found := false
		for _, n := range r.Notices() {
			found = found || (strings.Contains(n, "project file: ") && strings.Contains(n, "pi-goal-x-settings.json") && strings.Contains(n, "Global file: "))
		}
		if !found {
			t.Errorf("headless notification reports both layer paths: %v", r.Notices())
		}
		if fileExists(fx.settingsPath()) {
			t.Error("headless invocation must not create the settings file")
		}
	})

	tw(t, f, "environment overrides take precedence in the profile-reinstall decision", func(t *testing.T) {
		fx := newIntFixture(t, false)
		env := map[string]string{"PI_GOAL_DISABLE_TASKS": "true"}
		// session_start installs the three-tool profile because the env override forces disableTasks even though the file is absent.
		r := intRig(t, fx, intOpts{hasUI: true, env: env})
		startInt(r)
		hist := toolsHistory(r)
		if len(hist) != 1 {
			t.Fatalf("three-tool install at session_start: %d", len(hist))
		}
		if contains(hist[0], "set_goal_tasks") {
			t.Error("env override installs the core profile")
		}
		// The env override makes the disableTasks row read-only: selecting it warns and never mutates, so no reinstall may happen.
		r2 := intRig(t, fx, intOpts{hasUI: true, env: env, select_: shiftQueue("  disableTasks: false (environment; read-only)", "Done")})
		startInt(r2)
		r2.Command("goal-settings", "")
		if n := len(toolsHistory(r2)); n != 1 {
			t.Errorf("no reinstall when the effective setting never changed: %d", n)
		}
	})

	// ── confirmation and audit UX (follow-up Stage 2) ──

	tw(t, f, "/goal-clear asks for confirmation; cancelling is a byte-for-byte no-op", func(t *testing.T) {
		fx := newIntFixture(t, false)
		before := readFile(t, fx.activeGoalPath(t))
		ledgerBefore := len(fx.ledger())
		asked := false
		r := intRig(t, fx, intOpts{hasUI: true, confirm: func(string, string) bool { asked = true; return false }})
		startInt(r)
		r.Command("goal-clear", "")
		if !asked {
			t.Error("confirmation must be requested before archiving")
		}
		if len(fx.activeFiles()) != 1 {
			t.Error("goal must stay open after cancel")
		}
		if after := readFile(t, fx.activeGoalPath(t)); after != before {
			t.Error("active goal file unchanged byte-for-byte")
		}
		if len(fx.ledger()) != ledgerBefore {
			t.Error("no ledger entry appended on cancel")
		}
		if !anyHas(r.Notices(), "cancelled") {
			t.Error("cancel acknowledged")
		}
	})

	tw(t, f, "/goal-clear confirms and archives the selected goal", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{hasUI: true, confirm: func(string, string) bool { return true }})
		startInt(r)
		r.Command("goal-clear", "")
		if n := len(fx.activeFiles()); n != 0 {
			t.Errorf("goal archived after confirm: %d", n)
		}
		archived, _ := os.ReadDir(filepath.Join(fx.cwd, ".pi", "goals", "archived"))
		if len(archived) != 1 {
			t.Errorf("exactly one goal archived: %d", len(archived))
		}
		if !anyHas(r.Notices(), "cleared and archived") {
			t.Error("archive acknowledged")
		}
	})

	tw(t, f, "/goal-clear headless returns guidance without mutation", func(t *testing.T) {
		fx := newIntFixture(t, false)
		before := readFile(t, fx.activeGoalPath(t))
		ledgerBefore := len(fx.ledger())
		r := intRig(t, fx, intOpts{})
		startInt(r)
		r.Command("goal-clear", "")
		if len(fx.activeFiles()) != 1 {
			t.Error("headless clear must not archive")
		}
		if readFile(t, fx.activeGoalPath(t)) != before {
			t.Error("file unchanged")
		}
		if len(fx.ledger()) != ledgerBefore {
			t.Error("no ledger entry in headless clear")
		}
		if !anyHas(r.Notices(), "interactive session") {
			t.Error("headless guidance notification")
		}
	})

	// Escape during an audit: the fake auditor runs until the abort, as the original's waits on the abort signal.
	escapeAudit := func(t *testing.T, choice string) (*intFixture, *goalRig, string, bool) {
		t.Helper()
		fx := newIntFixture(t, false)
		started := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		r := intRig(t, fx, intOpts{hasUI: true,
			custom: func(map[string]any) map[string]any { return map[string]any{"ok": true, "result": choice} },
			auditor: func(map[string]any, string, string) goal.RigAuditResult {
				once.Do(func() { close(started) })
				<-release
				return goal.RigAuditResult{Output: "Auditor aborted.", Err: "Auditor aborted."}
			}})
		startInt(r)
		var out string
		var terminate bool
		call := goAsync(func() {
			raw, failure := r.Tool("update_goal", map[string]any{"status": "complete"})
			if failure != "" {
				t.Errorf("update_goal: %s", failure)
			}
			out, terminate = toolText(t, raw), toolTerminate(raw)
		})
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("the auditor never started")
		}
		// The widget's Escape handler matches the raw ESC byte.
		r.terminalInput(escapeKey)
		close(release)
		call.wait(t)
		return fx, r, out, terminate
	}

	tw(t, f, "Escape abort during audit appends exactly one audit_skipped and completes without audit", func(t *testing.T) {
		fx, _, text, _ := escapeAudit(t, "complete_without_audit")
		if !strings.Contains(text, "Goal audit skipped") {
			t.Errorf("skip report: %.80s", text)
		}
		var skipped []map[string]any
		for _, e := range fx.ledger() {
			if e["type"] == "audit_skipped" {
				skipped = append(skipped, e)
			}
		}
		if len(skipped) != 1 {
			t.Fatalf("exactly one canonical audit_skipped event (no duplicate from the abort callback): %d", len(skipped))
		}
		if skipped[0]["reason"] != "user_aborted" {
			t.Errorf("reason %v", skipped[0]["reason"])
		}
	})

	tw(t, f, "Escape abort then continue working leaves the goal active with no skip event", func(t *testing.T) {
		fx, _, text, terminate := escapeAudit(t, "continue_working")
		if !strings.Contains(text, "remains active") {
			t.Errorf("continue-working report: %s", text)
		}
		if terminate {
			t.Error("continue working must not terminate the turn")
		}
		g := goal.ParseGoalFileFor(fx.activeGoalPath(t))
		if g["status"] != "active" {
			t.Errorf("goal stays active after continue working: %v", g["status"])
		}
		if fx.hasEvent("audit_skipped") {
			t.Error("no skip event for continue working")
		}
		if fx.hasEvent("goal_completed") {
			t.Error("goal not completed")
		}
	})

	// ── completion transaction hardening (follow-up Stage 3) ──

	tw(t, f, "completion commit write failure never reports success and never clears focus", func(t *testing.T) {
		fx := newIntFixture(t, false)
		goalsDir := filepath.Join(fx.cwd, ".pi", "goals")
		// Pre-create the ledger file so its fallback append path works even when the goals directory is read-only (append needs write on
		// the file, not the directory; the atomic goal write needs a writable directory and therefore fails).
		if err := os.WriteFile(goal.LedgerPathFor(fx.cwd), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		r := intRig(t, fx, intOpts{auditor: auditApproved})
		startInt(r)
		// Block the authoritative active-file write (reads still work).
		if err := os.Chmod(goalsDir, 0o555); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(goalsDir, 0o755) }()
		out := updateGoal(t, r, map[string]any{"status": "complete"})
		text := toolText(t, out)
		if !strings.Contains(text, "Goal completion failed") {
			t.Errorf("failure report expected: %.120s", text)
		}
		if !strings.Contains(text, "not completed") {
			t.Error("must state the goal was not completed")
		}
		if strings.Contains(text, "Goal audit approved") {
			t.Error("no success report when the state mutation failed")
		}
		if toolTerminate(out) {
			t.Error("no termination request on failed completion")
		}
		// The deferred archival at turn_end must not emit goal_completed.
		turnEnd(r)
		if fx.hasEvent("goal_completed") {
			t.Error("no goal_completed event")
		}
		if !fx.hasEvent("audit_result") {
			t.Error("audit itself ran and recorded its result")
		}
		// The goal stays open and focused (get_goal still reports it, not complete).
		snap, failure := r.Tool("get_goal", map[string]any{})
		if failure != "" {
			t.Fatal(failure)
		}
		matchText(t, toolText(t, snap), `Goal \S+: running`, "goal still open and active")
	})

	tw(t, f, "deferred-archive failure at turn_end keeps the goal open and records no goal_completed", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{auditor: auditApproved})
		startInt(r)
		text := toolText(t, updateGoal(t, r, map[string]any{"status": "complete"}))
		if !strings.Contains(text, "Goal audit approved") {
			t.Errorf("commit itself succeeded: %s", text)
		}
		// Sabotage the archived directory: the deferred archive write fails.
		archivedDir := filepath.Join(fx.cwd, ".pi", "goals", "archived")
		if err := os.RemoveAll(archivedDir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archivedDir, []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		turnEnd(r)
		if fx.hasEvent("goal_completed") {
			t.Error("no goal_completed when archive failed")
		}
		if !anyHas(r.Notices(), "Failed to archive completed goal") {
			t.Errorf("archive failure is observable: %v", r.Notices())
		}
		if n := len(fx.activeFiles()); n != 1 {
			t.Fatalf("goal file stays in the active pool: %d", n)
		}
		// The active file still holds the completed goal (not archived).
		if g := goal.ParseGoalFileFor(fx.activeGoalPath(t)); g["status"] != "complete" {
			t.Errorf("completed goal remains on disk, unarchived: %v", g["status"])
		}
	})

	// ── capability parity (follow-up Stage 5.1-C) ──

	tw(t, f, "update_goal(paused) pauses an active goal immediately with an agent ledger event", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{})
		startInt(r)
		out := updateGoal(t, r, map[string]any{"status": "paused", "reason": "Waiting on credentials", "suggested_action": "Set FOO_API_KEY"})
		matchText(t, toolText(t, out), `Goal paused by the agent`, "result")
		if !toolTerminate(out) {
			t.Error("continuation stops")
		}
		if len(fx.activeFiles()) != 1 {
			t.Error("goal file remains open")
		}
		g := goal.ParseGoalFileFor(fx.activeGoalPath(t))
		if g["status"] != "paused" || g["autoContinue"] != false || g["stopReason"] != "agent" || g["pauseReason"] != "Waiting on credentials" || g["pauseSuggestedAction"] != "Set FOO_API_KEY" {
			t.Errorf("paused goal %v", g)
		}
		var paused []map[string]any
		for _, e := range fx.ledger() {
			if e["type"] == "goal_paused" {
				paused = append(paused, e)
			}
		}
		if len(paused) != 1 {
			t.Fatalf("exactly one goal_paused event: %d", len(paused))
		}
		if paused[0]["source"] != "agent" || paused[0]["reason"] != "Waiting on credentials" {
			t.Errorf("event %v", paused[0])
		}
	})

	tw(t, f, "update_goal(paused) requires a reason and applies only to an active goal", func(t *testing.T) {
		fx := newIntFixture(t, false)
		r := intRig(t, fx, intOpts{})
		startInt(r)
		matchText(t, toolText(t, updateGoal(t, r, map[string]any{"status": "paused"})), `requires a "reason"`, "no reason")
		if len(fx.activeFiles()) != 1 {
			t.Error("goal unchanged")
		}
		updateGoal(t, r, map[string]any{"status": "paused", "reason": "First pause"})
		matchText(t, toolText(t, updateGoal(t, r, map[string]any{"status": "paused", "reason": "Again"})), `applies only to an active goal`, "second pause")
	})

	tw(t, f, "completion_summary reaches the auditor as an untrusted claim and cannot approve", func(t *testing.T) {
		fx := newIntFixture(t, false)
		var gotCompletion string
		r := intRig(t, fx, intOpts{auditor: func(_ map[string]any, _, completion string) goal.RigAuditResult {
			gotCompletion = completion
			return goal.RigAuditResult{Disapproved: true, Output: "Still missing evidence\n<disapproved/>", Model: "fixture"}
		}})
		startInt(r)
		out := updateGoal(t, r, map[string]any{"status": "complete", "completion_summary": "All tests pass and the docs are updated."})
		if gotCompletion != "All tests pass and the docs are updated." {
			t.Errorf("claim reaches the auditor: %q", gotCompletion)
		}
		matchText(t, toolText(t, out), `Goal audit rejected`, "a claim cannot override a disapproval")
		if len(fx.activeFiles()) != 1 {
			t.Error("goal stays open")
		}
	})

	tw(t, f, "prompt guidance directs abandonment to /goal-clear and requirement changes to /goal-tweak", func(t *testing.T) {
		policy := goal.LifecyclePolicyBlock()
		if !strings.Contains(policy, "/goal-clear") {
			t.Error("abandonment guidance directs the user command")
		}
		if !strings.Contains(policy, "/goal-tweak") {
			t.Error("requirement changes stay user-started")
		}
		r := newGoalRig(t, rigOptions{})
		for _, name := range []string{"propose_goal_tweak", "abort_goal"} {
			if r.Host.tools[name] {
				t.Errorf("no %s tool", name)
			}
		}
	})
}
