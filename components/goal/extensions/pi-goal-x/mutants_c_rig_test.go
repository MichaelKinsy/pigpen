package pi_goal_x_test

// Mutation-survivor tests (lane C), rig level: commands, drafting, settings menu, goal service. Plain extra tests, not twins:
// every expectation comes from the original's code (port/oracle/extensions/*.ts).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const mcSettings = `{"autoSelectSingleGoal": false, "disabled": true}`

// mcRigSF is newGoalRig with a session file (the rig's host answers getSessionFile with "").
func mcRigSF(t *testing.T, opts rigOptions, sessionFile string) *goalRig {
	t.Helper()
	r := &goalRig{t: t, opts: opts, branch: append([]any{}, opts.Branch...), activeTools: opts.ActiveTools, sendHook: opts.OnSendMessage, model: opts.Model, idle: opts.Idle, pending: opts.Pending}
	if r.activeTools == nil {
		r.activeTools = []string{"read", "write", "bash"}
	}
	if opts.SessionID == "" {
		opts.SessionID = "rig-session"
	}
	r.sessionID = opts.SessionID
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	r.Cwd = opts.Cwd
	if opts.Settings != "-" {
		p := filepath.Join(opts.Cwd, ".pi", "pi-goal-x-settings.json")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(opts.Settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ext, probe := goal.NewGoalExtensionForTest(opts.Deps)
	r.Probe = probe
	r.Host = StartHost(t, ext, HostOptions{Mode: "tui", HasUI: opts.HasUI, Cwd: opts.Cwd, OnCallValue: func(method string, args map[string]any) (any, string) {
		if method == "getSessionFile" {
			return map[string]any{"sessionFile": sessionFile}, ""
		}
		return r.answer(method, args)
	}})
	t.Cleanup(probe.Quiesce)
	probe.WidgetTap(func(key string, lines []string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.widgets = append(r.widgets, rigWidget{Key: key, Lines: append([]string{}, lines...)})
	})
	return r
}

// mcSessionFile writes a session JSONL with n legacy full checkpoints (and v2 markers when v2 is set).
func mcSessionFile(t *testing.T, legacy int, v2 int) string {
	t.Helper()
	var lines []string
	lines = append(lines, `{"type":"session","id":"s1"}`)
	for i := 0; i < legacy; i++ {
		lines = append(lines, `{"type":"custom_message","customType":"pi-goal-event","content":"[GOAL CHECKPOINT goalId=g1]\n`+strings.Repeat("y", 300)+`","details":{"kind":"checkpoint"}}`)
	}
	for i := 0; i < v2; i++ {
		lines = append(lines, `{"type":"custom_message","customType":"pi-goal-event","content":"<pi_goal_continuation goal_id=\"g1\" kind=\"checkpoint\" v=\"2\"/>","details":{"version":2}}`)
	}
	p := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// mcGoal writes one more goal file into the fixture's project (id, status).
func (f *gufFixture) mcGoal(id, objective, status string, autoContinue bool) {
	rel, text := gufGoalFile(id, objective, status, autoContinue, "")
	f.write(rel, text)
}

func mcNotices(r *goalRig) string { return strings.Join(r.Notices(), "\n---\n") }

func mcHasNotice(t *testing.T, r *goalRig, want string) {
	t.Helper()
	for _, n := range r.Notices() {
		if n == want {
			return
		}
	}
	t.Errorf("expected notice %q in:\n%s", want, mcNotices(r))
}

func mcSessionStart(r *goalRig) { r.Fire("session_start", map[string]any{"reason": "startup"}) }

func TestMutantsCRefreshUpdatesUI(t *testing.T) {
	f := newGufFixture(t, "active", true, "")
	r := f.rig(rigOptions{Branch: f.focusedBranch("created")})
	mcSessionStart(r)
	before := len(r.Host.Calls())
	if failure := r.Command("goal-refresh", ""); failure != "" {
		t.Fatal(failure)
	}
	mcHasNotice(t, r, "info: goal-refresh: no changes detected — caches were already current.")
	ui := false
	for _, c := range r.Host.Calls()[before:] {
		if strings.HasPrefix(c.Method, "ui.set") {
			ui = true
		}
	}
	if !ui {
		t.Error("goal-refresh must update the UI (core.updateUI) after notifying")
	}
}

func mcStaleLock(t *testing.T, f *gufFixture, name string) {
	t.Helper()
	old := time.Now().Add(-10 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	f.write(".pi/goals/.locks/"+name, fmt.Sprintf(`{"pid": 999999999, "startedAt": %q}`, old))
}

func TestMutantsCRecoveryRepair(t *testing.T) {
	for _, arg := range []string{"repair", "REPAIR", "  Repair "} {
		f := newGufFixture(t, "active", true, "")
		mcStaleLock(t, f, "g1.lock")
		var title, message string
		confirm := true
		r := f.rig(rigOptions{Confirm: func(ti, m string) bool { title, message = ti, m; return confirm }})
		mcSessionStart(r)
		if failure := r.Command("goal-recovery", arg); failure != "" {
			t.Fatal(failure)
		}
		same(t, title, "Remove 1 stale lock(s) and refresh the pool snapshot?", "confirm title for "+arg)
		same(t, message, "Files are backed up to .pi/goals/.recovery-backup first.", "confirm message")
		n := r.LastNotice()
		if !strings.HasPrefix(n, "info: goal-recovery repair: 1 operation(s) applied.\n  - removed stale lock g1.lock\nBackup: ") {
			t.Errorf("applied notice = %q", n)
		}
		if i := strings.Index(n, "\nBackup: "); i < 0 || !strings.Contains(n[i:], ".pi/goals/.recovery-backup/") {
			t.Errorf("backup path missing: %q", n)
		}
		if fileExists(filepath.Join(f.cwd, ".pi", "goals", ".locks", "g1.lock")) {
			t.Error("the stale lock was not removed")
		}
	}
	// declined
	f := newGufFixture(t, "active", true, "")
	mcStaleLock(t, f, "g1.lock")
	r := f.rig(rigOptions{Confirm: func(string, string) bool { return false }})
	mcSessionStart(r)
	r.Command("goal-recovery", "repair")
	mcHasNotice(t, r, "info: goal-recovery repair: cancelled — nothing changed.")
	if !fileExists(filepath.Join(f.cwd, ".pi", "goals", ".locks", "g1.lock")) {
		t.Error("a declined repair must not remove the lock")
	}
	// confirmed, but no operation succeeds (the lock directory is read-only): "nothing to repair"
	f = newGufFixture(t, "active", true, "")
	mcStaleLock(t, f, "g1.lock")
	locks := filepath.Join(f.cwd, ".pi", "goals", ".locks")
	if err := os.Chmod(locks, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locks, 0o755) })
	r = f.rig(rigOptions{Confirm: func(string, string) bool { return true }})
	mcSessionStart(r)
	r.Command("goal-recovery", "repair")
	mcHasNotice(t, r, "info: goal-recovery repair: nothing to repair.")
}

func TestMutantsCRecoveryLevels(t *testing.T) {
	run := func(legacy, v2 int, session bool) (*goalRig, string) {
		f := newGufFixture(t, "active", true, "")
		sf := ""
		if session {
			sf = mcSessionFile(t, legacy, v2)
		}
		r := mcRigSF(t, rigOptions{Cwd: f.cwd, Settings: mcSettings}, sf)
		mcSessionStart(r)
		r.Command("goal-recovery", "")
		return r, r.LastNotice()
	}
	// healthy storage + a legacy full checkpoint: warning with the checkpoint section
	_, n := run(1, 0, true)
	if !strings.HasPrefix(n, "warning: Recovery report: OK — no issues found.\n\nSession checkpoints:\n  total: 1\n  legacy full checkpoints: 1\n") {
		t.Errorf("legacy checkpoint notice = %q", n)
	}
	// healthy storage + only v2 markers: info, section shown
	_, n = run(0, 2, true)
	if !strings.HasPrefix(n, "info: Recovery report: OK — no issues found.\n\nSession checkpoints:\n  total: 2\n") {
		t.Errorf("v2 checkpoint notice = %q", n)
	}
	// a session file with no checkpoints: no section
	_, n = run(0, 0, true)
	same(t, n, "info: Recovery report: OK — no issues found.", "no checkpoints")
	// no session file at all
	_, n = run(0, 0, false)
	same(t, n, "info: Recovery report: OK — no issues found.", "no session file")
}

func TestMutantsCStatusCommand(t *testing.T) {
	f := newGufFixture(t, "active", true, "")
	r := f.rig(rigOptions{Branch: f.focusedBranch("created")})
	mcSessionStart(r)
	r.Command("goal-status", "verbose")
	n := r.LastNotice()
	if !strings.Contains(n, "Goal id: unfocus-goal\n") || !strings.Contains(n, "Status: In progress (focused)") {
		t.Errorf("verbose status:\n%s", n)
	}
	if !strings.Contains(n, "\nEffective settings:\n") || !strings.Contains(n, "\n  effective goal pool: "+filepath.Join(f.cwd, ".pi", "goals")+"\n") && !strings.Contains(n, "  effective goal pool: ") {
		t.Errorf("verbose status lacks the effective settings report:\n%s", n)
	}
	r.Command("goal-status", " VERBOSE")
	if !strings.Contains(r.LastNotice(), "Goal id: unfocus-goal\n") {
		t.Errorf("a case-insensitive, whitespace-led flag selects verbose:\n%s", r.LastNotice())
	}
	// /^\s*verbose\b/i: a longer word is not the flag
	for _, arg := range []string{"verbosely", "verbose_x", "verbose1", "verbos", "xverbose"} {
		r.Command("goal-status", arg)
		if strings.Contains(r.LastNotice(), "Goal id:") {
			t.Errorf("%q must not select verbose", arg)
		}
	}
	r.Command("goal-status", "verbose-more")
	if !strings.Contains(r.LastNotice(), "Goal id:") {
		t.Error("verbose followed by a non-word char still selects verbose")
	}
	// the standard form carries no settings noise and the focused flag shows "focused" in the box
	r.Command("goal-status", "")
	mcNot(t, r.LastNotice(), "Effective settings")
}

func mcNot(t *testing.T, text, bad string) {
	t.Helper()
	if strings.Contains(text, bad) {
		t.Errorf("did not expect %q in:\n%s", bad, text)
	}
}

func TestMutantsCStatusHealthCommand(t *testing.T) {
	f := newGufFixture(t, "active", true, "")
	sf := mcSessionFile(t, 1, 0)
	r := mcRigSF(t, rigOptions{Cwd: f.cwd, Settings: mcSettings, Branch: f.focusedBranch("created")}, sf)
	mcSessionStart(r)
	r.Command("goal-status", "health")
	n := r.LastNotice()
	if !strings.Contains(n, "OK Focus: focused\n") || !strings.Contains(n, "\nSession checkpoints:\n  total: 1\n  legacy full checkpoints: 1\n") {
		t.Errorf("health:\n%s", n)
	}
	// an open goal that is not the focused one: "not focused"
	f2 := newGufFixture(t, "active", true, "")
	r2 := f2.rig(rigOptions{})
	mcSessionStart(r2)
	r2.Command("goal-status", "health")
	mcHasNotice(t, r2, "info: Goal health: WARN\nFocus: WARN — no goal is focused; 1 open goal remain.\nRun /goal-status for the normal dashboard.")
}

func TestMutantsCFocusCommand(t *testing.T) {
	// no open goals
	cwd := archivedCwd(t)
	r := newGoalRig(t, rigOptions{Cwd: cwd, Settings: mcSettings})
	mcSessionStart(r)
	r.Command("goal-focus", "")
	mcHasNotice(t, r, "warning: No open goals. Use /goal to draft one, or /goal-direct <objective> to start immediately.")

	// a single open goal is focused, and continuation is armed (accounting begins)
	f := newGufFixture(t, "active", true, "")
	r = f.rig(rigOptions{})
	mcSessionStart(r)
	same(t, r.Probe.McAccountingActive(f.id), false, "unfocused session has no accounting")
	r.Command("goal-focus", "")
	same(t, r.Probe.FocusedGoalID(), f.id, "focused")
	same(t, r.Probe.McAccountingActive(f.id), true, "focusing a single goal arms its continuation")
	if n := r.LastNotice(); !strings.HasPrefix(n, "info: Focused goal: ") || !strings.Contains(n, "Keep this shared goal open") {
		t.Errorf("notice = %q", n)
	}

	// several goals, headless: the list is the answer
	f = newGufFixture(t, "active", true, "")
	f.mcGoal("second-goal", "Second objective", "active", true)
	r = f.rig(rigOptions{HasUI: boolPtr(false)})
	mcSessionStart(r)
	r.Command("goal-focus", "")
	n := r.LastNotice()
	if !strings.HasPrefix(n, "info: Open goals: 2\n\n") || !strings.Contains(n, "second-goal — ") || !strings.Contains(n, "unfocus-goal — ") {
		t.Errorf("headless focus notice = %q", n)
	}
	same(t, r.Probe.FocusedGoalID(), "", "headless focus changes nothing")

	// several goals with a UI: the picked goal is focused and armed; an empty pick leaves focus unchanged
	var titles []string
	pick := func(label string) func(string, []string) (string, bool) {
		return func(title string, options []string) (string, bool) {
			titles = append(titles, title)
			if label == "" {
				return "", true
			}
			for _, o := range options {
				if strings.Contains(o, label) {
					return o, true
				}
			}
			return "", label == ""
		}
	}
	r = f.rig(rigOptions{Select: pick("second-goal")})
	mcSessionStart(r)
	r.Command("goal-focus", "")
	same(t, r.Probe.FocusedGoalID(), "second-goal", "picked goal focused")
	same(t, r.Probe.McAccountingActive("second-goal"), true, "picking arms the continuation")
	same(t, titles, []string{"Focus open goal"}, "picker title")
	r = f.rig(rigOptions{Select: pick("")})
	mcSessionStart(r)
	r.Command("goal-focus", "")
	mcHasNotice(t, r, "info: Goal focus unchanged.")
	same(t, r.Probe.FocusedGoalID(), "", "an empty pick leaves focus unchanged")
}

func TestMutantsCChooseOpenGoalModal(t *testing.T) {
	f := newGufFixture(t, "active", true, "")
	f.mcGoal("second-goal", "Second objective", "active", true)
	var r *goalRig
	depth := -1
	r = f.rig(rigOptions{Select: func(title string, options []string) (string, bool) {
		depth = r.Probe.GoalModalDepth()
		return "", false
	}})
	mcSessionStart(r)
	r.Command("goal-pause", "")
	same(t, depth, 1, "the picker of an open-goal choice runs inside the goal modal")
	same(t, r.Probe.GoalModalDepth(), 0, "the modal is left")
	mcHasNotice(t, r, "info: Goal focus unchanged.")
}

func TestMutantsCUnfocusRuntime(t *testing.T) {
	// no focused goal, but a run is in flight: the detached goal id comes from runningGoalID, else from the checkpoint
	for _, how := range []string{"running", "checkpoint"} {
		f := newGufFixture(t, "active", true, "")
		r := f.rig(rigOptions{Idle: func() bool { return false }})
		mcSessionStart(r)
		if how == "running" {
			r.Probe.McSetRunning(f.id)
		} else {
			r.Probe.McSetCheckpoint(f.id)
		}
		r.Command("goal-unfocus", "")
		same(t, r.aborts(), 1, how+": a busy session with a detached run is aborted")
	}
	// the runtime state is cleared
	f := newGufFixture(t, "active", true, "")
	r := f.rig(rigOptions{Branch: f.focusedBranch("created")})
	mcSessionStart(r)
	r.Probe.McSetRunning(f.id)
	r.Probe.McSetCheckpoint(f.id)
	r.Probe.McSetPostCompact(true)
	r.Command("goal-unfocus", "")
	same(t, r.Probe.McRunning(), "", "runningGoalId")
	same(t, r.Probe.McCheckpoint(), "", "checkpoint goal id")
	same(t, r.Probe.McPostCompact(), false, "post-compact reminder")
}

func TestMutantsCDirectClearsAccounting(t *testing.T) {
	f := newGufFixture(t, "active", true, "")
	r := f.rig(rigOptions{Branch: f.focusedBranch("created")})
	mcSessionStart(r)
	same(t, r.Probe.McAccountingActive(f.id), true, "accounting of the focused goal")
	goals := filepath.Join(f.cwd, ".pi", "goals")
	if err := os.Chmod(goals, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(goals, 0o755) })
	r.Command("goal-direct", "A brand new objective")
	if !strings.HasPrefix(r.LastNotice(), "error: Could not create the goal: ") {
		t.Fatalf("creation must fail in a read-only goals directory: %v", r.Notices())
	}
	same(t, r.Probe.McAccountingActive(f.id), false, "/goal-direct clears the active accounting before replacing the goal")
}

func TestMutantsCPauseResume(t *testing.T) {
	// no goal at all
	r := newGoalRig(t, rigOptions{Cwd: archivedCwd(t), Settings: mcSettings})
	mcSessionStart(r)
	r.Command("goal-resume", "")
	mcHasNotice(t, r, "warning: No goal is focused.")

	// a complete focused goal cannot be paused
	f := newGufFixture(t, "active", true, "")
	r = f.rig(rigOptions{Branch: f.focusedBranch("created")})
	mcSessionStart(r)
	r.Probe.SetGoalField("status", "complete")
	r.Command("goal-pause", "")
	mcHasNotice(t, r, "warning: Goal is complete.")

	// resuming a paused goal renews the allowance and records goal_resumed (reason "user") in the ledger
	f = newGufFixture(t, "paused", true, "Paused by the user.")
	r = f.rig(rigOptions{Branch: f.focusedBranch("created")})
	mcSessionStart(r)
	r.Command("goal-resume", "")
	mcHasNotice(t, r, "info: Goal resumed; autonomous allowance renewed.")
	led := f.ledger()
	if !strings.Contains(led, `"type":"goal_resumed"`) || !strings.Contains(led, `"reason":"user"`) || !strings.Contains(led, `"goalId":"unfocus-goal"`) {
		t.Errorf("ledger = %s", led)
	}
}

func TestMutantsCTweakCommand(t *testing.T) {
	f := newGufFixture(t, "active", true, "")
	r := f.rig(rigOptions{Branch: f.focusedBranch("created")})
	mcSessionStart(r)
	r.Probe.SetGoalField("status", "complete")
	r.Command("goal-tweak", "change it")
	mcHasNotice(t, r, "warning: Goal is complete. Use /goal to draft a new one, or /goal-direct <objective> to create one immediately.")

	f = newGufFixture(t, "active", true, "")
	r = f.rig(rigOptions{Branch: f.focusedBranch("created"), Settings: `{"autoSelectSingleGoal": false, "disabled": true, "objectiveMaxChars": 30}`})
	mcSessionStart(r)
	r.Command("goal-tweak", "   ")
	mcHasNotice(t, r, "info: Describe the requested change: /goal-tweak <change>, for example remove the token budget")
	r.Command("goal-tweak", strings.Repeat("x", 31))
	mcHasNotice(t, r, "warning: Requested change exceeds 30 characters (31).")
	before := len(r.Notices())
	r.Command("goal-tweak", strings.Repeat("x", 30))
	for _, n := range r.Notices()[before:] {
		if strings.Contains(n, "exceeds") {
			t.Errorf("a change of exactly the limit is accepted: %s", n)
		}
	}
}

// ---- /goal-settings menu ----

// mcMenu scripts the /goal-settings dialogs: each step answers one select (or input) from the options it is shown; every dialog the
// extension opens is recorded.
type mcMenu struct {
	t       *testing.T
	steps   []func(title string, options []string) string
	Titles  []string
	Options [][]string
	Inputs  []string // "title|placeholder"
	inputs  []string
}

func (m *mcMenu) selectFn(title string, options []string) (string, bool) {
	m.Titles = append(m.Titles, title)
	m.Options = append(m.Options, append([]string{}, options...))
	if len(m.steps) == 0 {
		return "", false
	}
	step := m.steps[0]
	m.steps = m.steps[1:]
	v := step(title, options)
	return v, v != ""
}

func (m *mcMenu) inputFn(title, placeholder string) (string, bool) {
	m.Inputs = append(m.Inputs, title+"|"+placeholder)
	if len(m.inputs) == 0 {
		return "", false
	}
	v := m.inputs[0]
	m.inputs = m.inputs[1:]
	return v, true
}

// row picks the option starting with prefix.
func (m *mcMenu) row(prefix string) {
	m.steps = append(m.steps, func(title string, options []string) string {
		for _, o := range options {
			if strings.HasPrefix(o, prefix) {
				return o
			}
		}
		m.t.Errorf("no option starting with %q in %q", prefix, options)
		return ""
	})
}

// say answers the next select with exactly v (it need not be an offered option).
func (m *mcMenu) say(v string) { m.steps = append(m.steps, func(string, []string) string { return v }) }

func (m *mcMenu) input(v string) { m.inputs = append(m.inputs, v) }

func (m *mcMenu) rig(t *testing.T, cwd, settings string, env map[string]string) *goalRig {
	m.t = t
	if cwd == "" {
		cwd = archivedCwd(t)
	}
	r := newGoalRig(t, rigOptions{Cwd: cwd, Settings: settings, Env: env, Select: m.selectFn, Input: m.inputFn})
	mcSessionStart(r)
	return r
}

func mcReadProject(t *testing.T, r *goalRig) string {
	t.Helper()
	b, _ := os.ReadFile(goal.SettingsPath(r.Cwd))
	return string(b)
}

func mcWriteGlobal(t *testing.T, text string) {
	t.Helper()
	p := filepath.Join(os.Getenv("PI_CODING_AGENT_DIR"), "pi-goal-x-settings.json")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	goal.InvalidateCaches()
}

func TestMutantsCSettingsMenuRows(t *testing.T) {
	m := &mcMenu{}
	m.say("Done")
	r := m.rig(t, "", "-", map[string]string{"PI_GOAL_DISABLE_TASKS": "true"})
	r.Command("goal-settings", "")
	opts := m.Options[0]
	same(t, opts[0], "─── Editing: project ("+goal.SettingsPath(r.Cwd)+") ───", "editing header")
	n := 0
	for _, o := range opts {
		if strings.HasPrefix(o, "  disableTasks:") {
			n++
			same(t, o, "  disableTasks: true (environment; read-only)", "env row")
		}
	}
	same(t, n, 1, "an environment-overridden row is listed once")
	if !contains(opts, "  autonomous run allowance: unlimited (default) (default)") {
		t.Errorf("allowance row missing from %q", opts)
	}
	same(t, opts[len(opts)-1], "Done", "last option")
}

func TestMutantsCSettingsMenuBoolean(t *testing.T) {
	// no local override: no inherit action; Cancel changes nothing
	m := &mcMenu{}
	m.row("  hideUnfocusedBanner:")
	m.say("Cancel")
	m.say("Done")
	r := m.rig(t, "", `{"disabled": true}`, nil)
	r.Command("goal-settings", "")
	same(t, m.Titles[1], "hideUnfocusedBanner (project)", "bool title")
	same(t, m.Options[1], []string{"Set project override to true", "Set project override to false", "Cancel"}, "actions without override")

	// a local override adds the inherit action, which removes it
	m = &mcMenu{}
	m.row("  hideUnfocusedBanner:")
	m.row("Use inherited value")
	m.say("Done")
	r = m.rig(t, "", `{"disabled": true, "hideUnfocusedBanner": true}`, nil)
	r.Command("goal-settings", "")
	same(t, m.Options[1], []string{"Set project override to true", "Set project override to false", "Use inherited value", "Cancel"}, "actions with override")
	if strings.Contains(mcReadProject(t, r), "hideUnfocusedBanner") {
		t.Errorf("the override was not removed: %s", mcReadProject(t, r))
	}

	// choosing the value that is already the project override rewrites nothing (the file keeps its bytes)
	m = &mcMenu{}
	m.row("  hideUnfocusedBanner:")
	m.say("Set project override to false")
	m.say("Done")
	const compact = `{"hideUnfocusedBanner":false,"disabled":true}`
	r = m.rig(t, "", compact, nil)
	r.Command("goal-settings", "")
	same(t, mcReadProject(t, r), compact, "an unchanged override is not rewritten")

	// choosing the default value without an override still records the explicit override
	m = &mcMenu{}
	m.row("  hideUnfocusedBanner:")
	m.say("Set project override to false")
	m.say("Done")
	r = m.rig(t, "", `{"disabled": true}`, nil)
	r.Command("goal-settings", "")
	same(t, savedSettings(t, r.Cwd)["hideUnfocusedBanner"], false, "explicit override recorded")
}

func TestMutantsCSettingsMenuInvalidLocalValueLabel(t *testing.T) {
	// the project value is invalid, so the effective source is the global layer: the row is "inherited", not a project override
	m := &mcMenu{}
	m.say("Done")
	r := m.rig(t, "", `{"disabled": true, "hideUnfocusedBanner": "maybe"}`, nil)
	mcWriteGlobal(t, `{"hideUnfocusedBanner": true}`)
	r.Command("goal-settings", "")
	var row string
	for _, o := range m.Options[0] {
		if strings.HasPrefix(o, "  hideUnfocusedBanner:") {
			row = o
		}
	}
	same(t, row, "  hideUnfocusedBanner: true (inherited from global)", "row label")
}

func TestMutantsCSettingsMenuOracleAttempts(t *testing.T) {
	m := &mcMenu{}
	m.row("  max failed attempts per blocker:")
	m.say("Set project override...")
	m.row("  max failed attempts per blocker:")
	m.say("Set project override...")
	m.say("Done")
	m.input("4")
	m.input("3")
	r := m.rig(t, "", `{"disabled": true}`, nil)
	r.Command("goal-settings", "")
	mcHasNotice(t, r, "warning: max failed attempts per blocker must be an integer between 1 and 3")
	same(t, m.Inputs[0], "Set max failed attempts per blocker|2", "placeholder is the effective value")
	oracle, _ := savedSettings(t, r.Cwd)["oracle"].(map[string]any)
	same(t, oracle["maxFailedAttemptsPerBlocker"], 3.0, "3 is accepted")
}

func TestMutantsCSettingsMenuThinking(t *testing.T) {
	// no thinking level yet: nothing is checked, and choosing (default) without an override changes nothing
	m := &mcMenu{}
	m.row("  thinking_level:")
	m.say("  (default)")
	m.row("  thinking_level:")
	m.say("  oops")
	m.say("Done")
	const text = `{"disabled":true}`
	r := m.rig(t, "", text, nil)
	r.Command("goal-settings", "")
	same(t, m.Titles[1], "Set thinking_level (project)", "title")
	same(t, m.Options[1], []string{"  (default)", "  off", "  minimal", "  low", "  medium", "  high", "  xhigh"}, "choices with no current value")
	same(t, mcReadProject(t, r), text, "default without an override touches nothing")
	mcHasNotice(t, r, `warning: thinking_level must be one of: off, minimal, low, medium, high, xhigh (or "(default)")`)
	same(t, mcReadProject(t, r), text, "an unknown level changes nothing")

	// with an override, (default) removes it
	m = &mcMenu{}
	m.row("  thinking_level:")
	m.say("  (default)")
	m.say("Done")
	r = m.rig(t, "", `{"disabled": true, "thinkingLevel": "high"}`, nil)
	r.Command("goal-settings", "")
	same(t, m.Options[1][5], "✓ high", "current level is checked")
	if strings.Contains(mcReadProject(t, r), "thinkingLevel") {
		t.Errorf("the override was not removed: %s", mcReadProject(t, r))
	}

	// a known level is stored
	m = &mcMenu{}
	m.row("  thinking_level:")
	m.say("  medium")
	m.say("Done")
	r = m.rig(t, "", text, nil)
	r.Command("goal-settings", "")
	same(t, savedSettings(t, r.Cwd)["thinking_level"], "medium", "level stored (persisted as thinking_level)")
}

func TestMutantsCSettingsMenuModelSelector(t *testing.T) {
	manual := "✎ Enter provider/model manually (advanced)"
	for _, c := range []struct {
		row    string
		oracle bool
	}{{"  provider:", false}, {"  oracle provider/model:", true}} {
		m := &mcMenu{}
		m.row(c.row)
		m.say(manual)
		m.say("Done")
		m.input("")
		m.input("acme/model-1")
		r := m.rig(t, "", `{"disabled": true}`, nil)
		r.Command("goal-settings", "")
		same(t, m.Inputs[1], "Set auditor provider/model|provider/model", "manual placeholder for "+c.row)
		s := savedSettings(t, r.Cwd)
		if c.oracle {
			o, _ := s["oracle"].(map[string]any)
			same(t, o["provider"], "acme", "oracle.provider")
			same(t, o["model"], "model-1", "oracle.model")
			same(t, s["provider"], nil, "the auditor provider is untouched")
		} else {
			same(t, s["provider"], "acme", "provider")
			same(t, s["model"], "model-1", "model")
			same(t, s["oracle"], nil, "no oracle block")
		}
	}
}

func TestMutantsCSettingsMenuFailure(t *testing.T) {
	cwd := archivedCwd(t)
	target := filepath.Join(cwd, "real-settings.json")
	if err := os.WriteFile(target, []byte(`{"disabled": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, goal.SettingsPath(cwd)); err != nil {
		t.Fatal(err)
	}
	m := &mcMenu{}
	m.row("  hideUnfocusedBanner:")
	m.say("Set project override to true")
	m.say("Done")
	r := m.rig(t, cwd, "-", nil)
	r.Command("goal-settings", "")
	last := ""
	for _, n := range r.Notices() {
		if strings.HasPrefix(n, "warning: Settings change failed: ") {
			last = n
		}
	}
	if !strings.Contains(last, "refusing symlink settings target: ") {
		t.Errorf("notices = %v", r.Notices())
	}
}

// ---- guided drafting ----

func mcDraftEntry(data map[string]any) map[string]any {
	return map[string]any{"type": "custom", "customType": "pi-goal-draft", "data": data}
}

func TestMutantsCDraftRehydrate(t *testing.T) {
	good := map[string]any{"version": 1.0, "mode": "goal", "seed": "Ship it", "startedAt": "2026-01-01T00:00:00.000Z", "auditorEnabled": true}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range good {
			m[a] = b
		}
		m[k] = v
		return m
	}
	// a valid, un-cleared draft is restored: /goal-cancel finds it and tombstones it
	r, _ := draftRig(t, draftOpts{hasUI: true, branch: []any{mcDraftEntry(good)}})
	startDraftSession(r)
	r.Command("goal-cancel", "")
	mcHasNotice(t, r, "info: Draft cancelled; no goal was created. The execution profile is restored.")
	es := draftEntries(r)
	last := es[len(es)-1].Data
	if v, _ := last["clearedAt"].(string); v == "" || last["seed"] != "Ship it" || last["mode"] != "goal" {
		t.Errorf("the cancelled draft is tombstoned with clearedAt: %v", last)
	}

	// a draft of another entry version is not restored
	r, _ = draftRig(t, draftOpts{hasUI: true, branch: []any{mcDraftEntry(with("version", 2.0))}})
	startDraftSession(r)
	r.Command("goal-cancel", "")
	mcHasNotice(t, r, "info: No active draft to cancel.")

	// a cleared draft is not restored either
	r, _ = draftRig(t, draftOpts{hasUI: true, branch: []any{mcDraftEntry(with("clearedAt", "2026-01-01T00:01:00.000Z"))}})
	startDraftSession(r)
	r.Command("goal-cancel", "")
	mcHasNotice(t, r, "info: No active draft to cancel.")

	// a tweak draft whose target goal is not focused is stale: tombstoned and discarded with a notice
	tweak := with("mode", "tweak")
	tweak["targetGoalId"] = "gone-goal"
	r, _ = draftRig(t, draftOpts{hasUI: true, branch: []any{mcDraftEntry(tweak)}})
	startDraftSession(r)
	mcHasNotice(t, r, "warning: The goal tweak draft is stale (its target goal changed); it was discarded.")
	es = draftEntries(r)
	if len(es) != 1 || es[0].Data["clearedAt"] == nil || es[0].Data["targetGoalId"] != "gone-goal" {
		t.Errorf("the stale tweak draft is tombstoned: %v", es)
	}
	r.Command("goal-cancel", "")
	mcHasNotice(t, r, "info: No active draft to cancel.")
}

func TestMutantsCDraftStart(t *testing.T) {
	// the notice carries the first 60 characters of the topic
	topic := strings.Repeat("a", 59) + "b" + strings.Repeat("c", 10)
	r, _ := draftRig(t, draftOpts{hasUI: true})
	startDraftSession(r)
	r.Command("goal", topic)
	mcHasNotice(t, r, "info: Goal draft started: "+strings.Repeat("a", 59)+"b. The agent will clarify, propose a goal and tasks where useful, then ask you to confirm.")

	// a second start with a dismissed dialog resumes the first draft
	r, _ = draftRig(t, draftOpts{hasUI: true, select_: func(title string, options []string) (string, bool) {
		if title != "A goal draft is already active" {
			t.Errorf("select title = %q", title)
		}
		return "", false
	}})
	startDraftSession(r)
	r.Command("goal", "First thing")
	r.Command("goal", "Second thing")
	mcHasNotice(t, r, "info: A draft is already active; resuming it. Use /goal-cancel to discard it.")
}

func mcDeliverAs(t *testing.T, r *goalRig) string {
	t.Helper()
	calls := r.Host.CallsTo("sendUserMessage")
	if len(calls) == 0 {
		t.Fatal("no sendUserMessage call")
	}
	c := calls[len(calls)-1]
	return fmt.Sprint(c.Args["options"])
}

func TestMutantsCDraftDelivery(t *testing.T) {
	for _, c := range []struct {
		idle bool
		want string
	}{{true, "followUp"}, {false, "steer"}} {
		cwd := archivedCwd(t)
		idle := c.idle
		r := newGoalRig(t, rigOptions{Cwd: cwd, Settings: "-", Idle: func() bool { return idle }})
		startDraftSession(r)
		r.Command("goal", "Ship a thing")
		got := mcDeliverAs(t, r)
		if !strings.Contains(got, c.want) {
			t.Errorf("idle=%v: options = %s, want deliverAs %s", c.idle, got, c.want)
		}
	}
}

func TestMutantsCDraftSendFailure(t *testing.T) {
	cwd := archivedCwd(t)
	r := newGoalRig(t, rigOptions{Cwd: cwd, Settings: "-", OnCall: func(method string, args map[string]any) (any, string) {
		if method == "sendUserMessage" {
			return nil, "host refused the message"
		}
		return map[string]any{}, ""
	}})
	startDraftSession(r)
	r.Command("goal", "Ship a thing")
	n := r.LastNotice()
	if !strings.HasPrefix(n, "error: Could not start goal draft: ") || !strings.Contains(n, "host refused the message") {
		t.Errorf("notices = %v", r.Notices())
	}
	r.Command("goal-cancel", "")
	mcHasNotice(t, r, "info: No active draft to cancel.")
	if contains(r.ActiveTools(), "goal_questionnaire") {
		t.Error("the drafting tools are removed again")
	}
}

func TestMutantsCDraftClearsAccounting(t *testing.T) {
	r, _ := draftRig(t, draftOpts{hasUI: true})
	startDraftSession(r)
	r.Command("goal-direct", "Initial objective")
	id := r.Probe.FocusedGoalID()
	same(t, r.Probe.McAccountingActive(id), true, "a running goal accounts")
	r.Command("goal", "Something else")
	same(t, r.Probe.McAccountingActive(id), false, "starting a draft clears the active accounting")
}

// mcGoalFile is a goal file (rel path, text) with the given extra fields over a plain active goal.
func mcGoalFile(id, objective string, extra map[string]any) (string, string) {
	rel := ".pi/goals/active_goal_20260102030405_" + id + ".md"
	head := map[string]any{
		"version": 3, "id": id, "objective": objective, "status": "active", "autoContinue": true,
		"usage": map[string]any{"tokensUsed": 0, "activeSeconds": 0}, "sisyphus": false, "revision": 0,
		"createdAt": "2026-01-02T03:04:05.000Z", "updatedAt": "2026-01-02T03:04:05.000Z", "activePath": rel,
	}
	for k, v := range extra {
		head[k] = v
	}
	b, _ := json.MarshalIndent(head, "", "  ")
	return rel, string(b) + "\n\n# Goal Prompt\n\n" + objective + "\n\n## Progress\n"
}

func mcTask(id, title string, kv ...any) map[string]any {
	t := map[string]any{"id": id, "title": title, "status": "pending"}
	for i := 0; i+1 < len(kv); i += 2 {
		t[kv[i].(string)] = kv[i+1]
	}
	return t
}

func mcTaskList(tasks ...any) map[string]any {
	return map[string]any{"tasks": tasks, "blockCompletion": false, "proposedAt": "2026-01-02T03:04:05.000Z"}
}

// mcTweakRig starts a headless session focused on a seeded goal and begins a /goal-tweak draft for it.
func mcTweakRig(t *testing.T, settings string, extra map[string]any, env map[string]string) *goalRig {
	t.Helper()
	cwd := archivedCwd(t)
	rel, text := mcGoalFile("tweak-goal", "Original objective", extra)
	r := newGoalRig(t, rigOptions{Cwd: cwd, Settings: settings, Seed: map[string]string{rel: text}, Branch: []any{focusEntry("tweak-goal")}, HasUI: boolPtr(false), Env: env})
	r.Probe.FakeClock(time.Now().UnixMilli())
	startDraftSession(r)
	r.Command("goal-tweak", "Revise the plan")
	return r
}

func mcPropose(t *testing.T, r *goalRig, params map[string]any) string {
	t.Helper()
	out, failure := r.Tool("propose_goal_draft", params)
	if failure != "" {
		t.Fatalf("propose_goal_draft: %s", failure)
	}
	return toolText(t, out)
}

func TestMutantsCTweakCurrentTaskAndContract(t *testing.T) {
	extra := map[string]any{
		"taskList": mcTaskList(mcTask("t1", "First"), mcTask("t2", "Second")), "currentTaskId": "t1",
		"verificationContract": "Run npm test (0 failures)",
	}
	// the new list drops the current task: the focus on it is cleared; the old contract survives a contract-less objective
	r := mcTweakRig(t, "-", extra, nil)
	text := mcPropose(t, r, map[string]any{"objective": "Revised objective", "tasks": []any{map[string]any{"id": "t2", "title": "Second"}}})
	matchText(t, text, `tweak confirmed`, "result")
	g := firstGoal(t, r.Cwd)
	if _, has := g["currentTaskId"]; has {
		t.Errorf("currentTaskId must be cleared when its task left the list: %v", g["currentTaskId"])
	}
	// (the file is normalized when read back; the session's own copy is what the mutation produced)
	if _, has := r.Probe.Goal()["currentTaskId"]; has {
		t.Errorf("the focused goal still names the removed task: %v", r.Probe.Goal()["currentTaskId"])
	}
	same(t, g["verificationContract"], "Run npm test (0 failures)", "an objective without a contract keeps the goal's contract")
	same(t, taskIDs(g), []string{"t2"}, "task ids")

	// the new list keeps it: still current
	r = mcTweakRig(t, "-", extra, nil)
	mcPropose(t, r, map[string]any{"objective": "Revised objective", "tasks": []any{map[string]any{"id": "t1", "title": "First"}, map[string]any{"id": "t2", "title": "Second"}}})
	same(t, firstGoal(t, r.Cwd)["currentTaskId"], "t1", "current task kept")

	// a contract in the revised objective replaces it
	r = mcTweakRig(t, "-", extra, nil)
	mcPropose(t, r, map[string]any{"objective": "Revised objective\nVerification contract: Run the linter"})
	same(t, firstGoal(t, r.Cwd)["verificationContract"], "Run the linter", "new contract")
}

func TestMutantsCTweakCompleteAndTarget(t *testing.T) {
	r := mcTweakRig(t, "-", nil, nil)
	r.Probe.SetGoalField("status", "complete")
	text := mcPropose(t, r, map[string]any{"objective": "Revised objective"})
	same(t, text, "Completed goals cannot be tweaked.", "complete goal")
	same(t, firstGoal(t, r.Cwd)["objective"], "Original objective", "goal untouched")
}

func TestMutantsCTweakTargetChanged(t *testing.T) {
	cwd := archivedCwd(t)
	relA, textA := mcGoalFile("goal-a", "Objective A", nil)
	relB, textB := mcGoalFile("goal-b", "Objective B", nil)
	var r *goalRig
	r = newGoalRig(t, rigOptions{Cwd: cwd, Settings: mcSettings, Seed: map[string]string{relA: textA, relB: textB}, Branch: []any{focusEntry("goal-a")},
		Select: func(title string, options []string) (string, bool) {
			for _, o := range options {
				if strings.Contains(o, "goal-b") {
					return o, true
				}
			}
			return "", false
		}})
	r.Probe.FakeClock(time.Now().UnixMilli())
	startDraftSession(r)
	r.Command("goal-tweak", "Revise the plan")
	r.Command("goal-focus", "")
	same(t, r.Probe.FocusedGoalID(), "goal-b", "focus moved")
	text := mcPropose(t, r, map[string]any{"objective": "Revised objective"})
	same(t, text, "The goal changed while drafting; review it and start /goal-tweak again.", "target moved")
	for _, g := range goal.DiskGoals(cwd) {
		if g["objective"] != "Objective A" && g["objective"] != "Objective B" {
			t.Errorf("no goal may be revised: %v", g["objective"])
		}
	}
}

func TestMutantsCProposalValidation(t *testing.T) {
	// the proposal's mode must be the command's
	r, _ := draftRig(t, draftOpts{})
	startDraftSession(r)
	r.Command("goal", "Ship a thing")
	same(t, mcPropose(t, r, map[string]any{"objective": "Ship a thing.", "sisyphus": true}), "Proposal mode does not match the command that began this draft.", "goal draft, sisyphus proposal")
	r, _ = draftRig(t, draftOpts{})
	startDraftSession(r)
	r.Command("sisyphus", "Ship a thing")
	same(t, mcPropose(t, r, map[string]any{"objective": "Ship a thing.", "sisyphus": false}), "Proposal mode does not match the command that began this draft.", "sisyphus draft, goal proposal")

	// auto_continue false is honored
	r, _ = draftRig(t, draftOpts{})
	startDraftSession(r)
	r.Command("goal", "Ship a thing")
	mcPropose(t, r, map[string]any{"objective": "Ship a thing.", "auto_continue": false})
	same(t, firstGoal(t, r.Cwd)["autoContinue"], false, "autoContinue")

	// the configured subtask depth bounds the proposed tree
	r, _ = draftRig(t, draftOpts{settings: `{"subtaskDepth": 2}`})
	startDraftSession(r)
	r.Command("goal", "Ship a thing")
	text := mcPropose(t, r, map[string]any{"objective": "Ship a thing.", "tasks": taskParams([3]string{"a", "Root", ""}, [3]string{"b", "Child", "a"}, [3]string{"c", "Grandchild", "b"})})
	if strings.Contains(text, "depth") || strings.Contains(text, "cannot") {
		t.Errorf("a two-level subtask tree is allowed with subtaskDepth 2: %s", text)
	}
	same(t, len(goal.ActiveGoalFiles(r.Cwd)), 1, "goal created")
}

func mcPausedExtra(used float64, phase string) map[string]any {
	return map[string]any{
		"status": "paused", "stopReason": "user", "pauseReason": "Paused by the user.",
		"scheduler": map[string]any{"version": 1, "owner": "rig-session", "phase": phase, "used": used, "generation": "gen-1", "repairUsed": false},
	}
}

func mcTweakWith(t *testing.T, settings string, extra map[string]any) (*goalRig, *goal.FakeClock) {
	t.Helper()
	cwd := archivedCwd(t)
	rel, text := mcGoalFile("tweak-goal", "Original objective", extra)
	r := newGoalRig(t, rigOptions{Cwd: cwd, Settings: settings, Seed: map[string]string{rel: text}, Branch: []any{focusEntry("tweak-goal")}, HasUI: boolPtr(false)})
	clock := r.Probe.FakeClock(time.Now().UnixMilli())
	t.Cleanup(clock.Restore)
	startDraftSession(r)
	r.Command("goal-tweak", "Revise the plan")
	return r, clock
}

func TestMutantsCTweakResumesPausedGoal(t *testing.T) {
	r, clock := mcTweakWith(t, "-", mcPausedExtra(0, "idle"))
	before := len(r.Sent())
	text := mcPropose(t, r, map[string]any{"objective": "Revised objective"})
	if strings.Contains(text, "remains paused") {
		t.Errorf("a resumed goal is not reported paused: %s", text)
	}
	same(t, firstGoal(t, r.Cwd)["status"], "active", "status")
	same(t, r.Probe.McAccountingActive("tweak-goal"), true, "resume restarts accounting")
	same(t, len(r.Sent())-before, 0, "nothing is sent before the continuation timer runs")
	clock.Tick(5000)
	if sent := r.Sent()[before:]; len(sent) != 1 || !strings.Contains(fmt.Sprint(sent[0].Message["content"]), `goal_id="tweak-goal"`) {
		t.Errorf("resume queues the auto-continuation: %v", sent)
	}
}

func TestMutantsCTweakPausedWithoutAllowance(t *testing.T) {
	// the autonomous-run allowance is spent: the goal stays paused
	r, clock := mcTweakWith(t, `{"maxAutonomousRuns": 1}`, mcPausedExtra(1, "idle"))
	before := len(r.Sent())
	text := mcPropose(t, r, map[string]any{"objective": "Revised objective"})
	if !strings.HasSuffix(text, "\nGoal remains paused. Use /goal-resume to continue when scheduling permits.") {
		t.Errorf("tail = %q", text)
	}
	same(t, firstGoal(t, r.Cwd)["status"], "paused", "status")
	clock.Tick(5000)
	same(t, len(r.Sent())-before, 0, "nothing is continued")
	if hasEvent(r.Cwd, "goal_resumed") {
		t.Error("no goal_resumed event for a goal that stayed paused")
	}
}

func TestMutantsCTweakBudgetLedger(t *testing.T) {
	// a goal that is already budget-limited records no second goal_budget_limited event
	r, _ := mcTweakWith(t, "-", map[string]any{
		"status": "budget_limited", "tokenBudget": 100, "usage": map[string]any{"tokensUsed": 500, "activeSeconds": 0},
		"scheduler": map[string]any{"version": 1, "owner": "rig-session", "phase": "idle", "used": 0, "generation": "gen-1", "repairUsed": false},
	})
	text := mcPropose(t, r, map[string]any{"objective": "Revised objective", "token_budget": 200})
	if !strings.Contains(text, "Consumed tokens already meet this budget; the goal remains budget-limited.") {
		t.Errorf("text = %s", text)
	}
	same(t, len(eventsOfType(r.Cwd, "goal_budget_limited")), 0, "goal_budget_limited events")
	same(t, len(eventsOfType(r.Cwd, "goal_budget_changed")), 1, "goal_budget_changed events")

	// an active goal whose usage already meets the new budget becomes budget-limited, with one event
	r, _ = mcTweakWith(t, "-", map[string]any{"usage": map[string]any{"tokensUsed": 500, "activeSeconds": 0}})
	mcPropose(t, r, map[string]any{"objective": "Revised objective", "token_budget": 100})
	same(t, firstGoal(t, r.Cwd)["status"], "budget_limited", "status")
	same(t, len(eventsOfType(r.Cwd, "goal_budget_limited")), 1, "goal_budget_limited events")
}

func TestMutantsCDraftToolsWithoutDraft(t *testing.T) {
	r, _ := draftRig(t, draftOpts{hasUI: true})
	startDraftSession(r)
	out, failure := r.Tool("goal_question", map[string]any{"question": "Which one?", "options": []any{"a", "b"}})
	if failure != "" {
		t.Fatal(failure)
	}
	same(t, toolText(t, out), "No guided goal draft is active. Ask the user to run /goal or /sisyphus.", "goal_question without a draft")
	out, failure = r.Tool("goal_questionnaire", map[string]any{"questions": []any{map[string]any{"id": "q", "question": "Which one?", "options": []any{"a"}}}})
	if failure != "" {
		t.Fatal(failure)
	}
	same(t, toolText(t, out), "No guided goal draft is active.", "goal_questionnaire without a draft")
}

func TestMutantsCProposeCallText(t *testing.T) {
	r, _ := draftRig(t, draftOpts{hasUI: true})
	startDraftSession(r)
	r.Command("goal", "Ship a thing")
	rendered := renderedCall(t, r, proposalParams("Ship a thing.", map[string]any{"auto_continue": false, "block_completion": true}))
	for _, want := range []string{"Automatic continuation: disabled", "Block completion on pending tasks: enabled"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("%q missing from the proposal call:\n%s", want, rendered)
		}
	}
	rendered = renderedCall(t, r, proposalParams("Ship a thing.", map[string]any{"auto_continue": true, "block_completion": false}))
	for _, bad := range []string{"Automatic continuation", "Block completion"} {
		if strings.Contains(rendered, bad) {
			t.Errorf("%q must not be in:\n%s", bad, rendered)
		}
	}
}
