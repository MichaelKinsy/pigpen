package pi_goal_x_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

// The SDK adapter (extension.go) behind the real SDK, through the Skill's fake host. The replay test drives the core through its
// own host interface and the Pi-recorded scenarios run in RPC mode and cannot see files, so these tests are the check of what
// the adapter reads from the host (session id, mode, UI, idle state, dialogs, branch) and what it writes.

type rig struct {
	mode     string
	hasUI    *bool
	idle     any    // the isIdle answer: true, false, or "error" for a host failure
	branch   []any  // the session branch getBranch serves
	pick     int    // the option index ui.select answers, -1 to cancel
	confirm  bool   // the ui.confirm answer
	settings string // .pi/pi-goal-x-settings.json ("" for maxAutonomousRuns 0)
	seed     map[string]string
}

const sessionID = "adapter-session"

func (r rig) start(t *testing.T) (*Host, string) {
	t.Helper()
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	settings := r.settings
	if settings == "" {
		settings = `{"maxAutonomousRuns": 0}`
	}
	write := func(rel, text string) {
		p := filepath.Join(cwd, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".pi/pi-goal-x-settings.json", settings)
	for rel, text := range r.seed {
		write(rel, text)
	}
	mode := r.mode
	if mode == "" {
		mode = "tui"
	}
	h := StartHost(t, goal.Extension(), HostOptions{Mode: mode, HasUI: r.hasUI, Cwd: cwd, OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getSessionID":
			return map[string]any{"sessionId": sessionID}, ""
		case "isIdle":
			if r.idle == "error" {
				return nil, "no agent"
			}
			idle := true
			if b, ok := r.idle.(bool); ok {
				idle = b
			}
			return map[string]any{"idle": idle}, ""
		case "sessionRead":
			if args["method"] == "getBranch" {
				if r.branch == nil {
					return []any{}, ""
				}
				return r.branch, ""
			}
		case "ui.select":
			opts, _ := args["options"].([]any)
			if r.pick < 0 || r.pick >= len(opts) {
				return map[string]any{"selected": "", "ok": false}, ""
			}
			return map[string]any{"selected": opts[r.pick], "ok": true}, ""
		case "ui.confirm":
			return map[string]any{"confirmed": r.confirm}, ""
		}
		return map[string]any{}, ""
	}})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	return h, cwd
}

func notices(h *Host) []string {
	var out []string
	for _, c := range h.CallsTo("ui.notify") {
		out = append(out, c.Args["level"].(string)+": "+c.Args["message"].(string))
	}
	return out
}

func lastStatus(t *testing.T, h *Host) string {
	t.Helper()
	text, found := "", false
	for _, c := range h.CallsTo("ui.setStatus") {
		if c.Args["key"] == "goal" {
			text, _ = c.Args["text"].(string)
			found = true
		}
	}
	if !found {
		t.Fatal("no ui.setStatus for goal")
	}
	return text
}

// goalFiles reads the active goal files' JSON headers.
func goalFiles(t *testing.T, cwd, dir string) []map[string]any {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(cwd, dir))
	var out []map[string]any
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(cwd, dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		head := string(b)[:strings.Index(string(b), "\n}\n")+2]
		var m map[string]any
		if err := json.Unmarshal([]byte(head), &m); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		out = append(out, m)
	}
	return out
}

func seedGoal(id, objective string) (string, string) {
	rel := ".pi/goals/active_goal_20260102030405_" + id + ".md"
	head := `{
  "version": 3,
  "id": "` + id + `",
  "objective": "` + objective + `",
  "status": "active",
  "autoContinue": true,
  "usage": {"tokensUsed": 0, "activeSeconds": 0},
  "sisyphus": false,
  "revision": 0,
  "createdAt": "2026-01-02T03:04:05.000Z",
  "updatedAt": "2026-01-02T03:04:05.000Z",
  "activePath": "` + rel + `"
}`
	return rel, head + "\n\n# Goal Prompt\n\n" + objective + "\n\n## Progress\n"
}

func focusEntry(id string) map[string]any {
	return map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1, "focusedGoalId": id, "reason": "selected"}}
}

func TestAdapterCreatesAGoalOwnedByTheHostSession(t *testing.T) {
	h, cwd := rig{}.start(t)
	if failure := h.Command("goal-direct", "Write the release notes"); failure != "" {
		t.Fatal(failure)
	}
	files := goalFiles(t, cwd, ".pi/goals")
	if len(files) != 1 {
		t.Fatalf("goal files: %d", len(files))
	}
	scheduler, _ := files[0]["scheduler"].(map[string]any)
	if scheduler["owner"] != sessionID {
		t.Errorf("scheduler owner %v, want the host's session id %q", scheduler["owner"], sessionID)
	}
	entries := h.CallsTo("appendEntry")
	if len(entries) != 1 || entries[0].Args["customType"] != "pi-goal-focus" {
		t.Fatalf("focus entry: %+v", entries)
	}
	data, _ := entries[0].Args["data"].(map[string]any)
	if data["focusedGoalId"] != files[0]["id"] || data["reason"] != "created" {
		t.Errorf("focus entry data %v", data)
	}
	// A terminal shows the goal as one status line (the original's dashboard is a component).
	if got := lastStatus(t, h); got != "goal: running - Write the release notes" {
		t.Errorf("status %q", got)
	}
	want := "info: ● Goal running\n├─ ⟡ Write the release notes\n└─ auto-continue on\nBudget: none"
	if n := notices(h); len(n) == 0 || n[0] != want {
		t.Errorf("notices %q", n)
	}
}

func TestAdapterRPCShowsNoStatusLine(t *testing.T) {
	h, _ := rig{mode: "rpc"}.start(t)
	h.Command("goal-direct", "Over RPC")
	if got := lastStatus(t, h); got != "" {
		t.Errorf("status over RPC %q, want it cleared as Pi's RPC host shows no dashboard", got)
	}
}

func TestAdapterRestoresFocusFromTheBranch(t *testing.T) {
	a, at := seedGoal("seed-a", "First")
	b, bt := seedGoal("seed-b", "Second")
	h, _ := rig{branch: []any{focusEntry("seed-b")}, seed: map[string]string{a: at, b: bt}}.start(t)
	h.Command("goal-list", "")
	n := notices(h)
	if len(n) == 0 || !strings.Contains(n[len(n)-1], "\n* seed-b") || strings.Contains(n[len(n)-1], "\n* seed-a") {
		t.Errorf("list %q", n)
	}
	if got := lastStatus(t, h); got != "goal: running - Second" {
		t.Errorf("status %q", got)
	}
}

func TestAdapterUnfocusAbortsOnlyABusyAgent(t *testing.T) {
	a, at := seedGoal("seed-a", "Busy goal")
	for _, c := range []struct {
		idle  any
		abort int
	}{{false, 1}, {true, 0}, {"error", 0}} {
		h, _ := rig{idle: c.idle, branch: []any{focusEntry("seed-a")}, seed: map[string]string{a: at}}.start(t)
		h.Command("goal-unfocus", "")
		if got := len(h.CallsTo("abort")); got != c.abort {
			t.Errorf("idle=%v: %d abort calls, want %d", c.idle, got, c.abort)
		}
	}
}

func TestAdapterSelectPicksTheGoalToPause(t *testing.T) {
	a, at := seedGoal("seed-a", "First")
	b, bt := seedGoal("seed-b", "Second")
	h, cwd := rig{pick: 1, seed: map[string]string{a: at, b: bt}}.start(t)
	h.Command("goal-pause", "")
	sel := h.CallsTo("ui.select")
	if len(sel) != 1 || sel[0].Args["title"] != "Pause which open goal?" {
		t.Fatalf("select %+v", sel)
	}
	status := map[string]any{}
	for _, f := range goalFiles(t, cwd, ".pi/goals") {
		status[f["id"].(string)] = f["status"]
	}
	if status["seed-b"] != "paused" || status["seed-a"] != "active" {
		t.Errorf("statuses %v", status)
	}
	h2, _ := rig{pick: -1, seed: map[string]string{a: at, b: bt}}.start(t)
	h2.Command("goal-pause", "")
	if n := notices(h2); len(n) == 0 || n[len(n)-1] != "info: Goal focus unchanged." {
		t.Errorf("cancelled select %q", n)
	}
}

func TestAdapterConfirmClearsAndArchives(t *testing.T) {
	a, at := seedGoal("seed-a", "Throwaway")
	h, cwd := rig{confirm: true, branch: []any{focusEntry("seed-a")}, seed: map[string]string{a: at}}.start(t)
	h.Command("goal-clear", "")
	if c := h.CallsTo("ui.confirm"); len(c) != 1 || c[0].Args["title"] != "Clear goal?" || c[0].Args["message"] != "running - Throwaway" {
		t.Fatalf("confirm %+v", c)
	}
	if n := notices(h); len(n) == 0 || n[len(n)-1] != "info: Goal cleared and archived." {
		t.Errorf("notices %q", n)
	}
	if len(goalFiles(t, cwd, ".pi/goals")) != 0 || len(goalFiles(t, cwd, ".pi/goals/archived")) != 1 {
		t.Error("the goal was not archived")
	}
	h2, cwd2 := rig{confirm: false, branch: []any{focusEntry("seed-a")}, seed: map[string]string{a: at}}.start(t)
	h2.Command("goal-clear", "")
	if n := notices(h2); len(n) == 0 || n[len(n)-1] != "info: Goal clear cancelled." || len(goalFiles(t, cwd2, ".pi/goals")) != 1 {
		t.Errorf("declined clear %q", n)
	}
}

func TestAdapterWithoutUIAsksForAnInteractiveSession(t *testing.T) {
	no := false
	a, at := seedGoal("seed-a", "Keep me")
	h, cwd := rig{mode: "print", hasUI: &no, branch: []any{focusEntry("seed-a")}, seed: map[string]string{a: at}}.start(t)
	h.Command("goal-clear", "")
	if len(h.CallsTo("ui.confirm")) != 0 {
		t.Error("a host without UI was asked to confirm")
	}
	if n := notices(h); len(n) == 0 || n[len(n)-1] != "warning: Run /goal-clear in an interactive session to confirm clearing: running - Keep me" {
		t.Errorf("notices %q", n)
	}
	if len(goalFiles(t, cwd, ".pi/goals")) != 1 {
		t.Error("the goal was cleared without confirmation")
	}
}

func TestAdapterShutdownPersistsTheFocusedGoal(t *testing.T) {
	a, at := seedGoal("seed-a", "Persist me")
	h, cwd := rig{branch: []any{focusEntry("seed-a")}, seed: map[string]string{a: at}}.start(t)
	h.Fire("session_shutdown", map[string]any{"reason": "quit"})
	files := goalFiles(t, cwd, ".pi/goals")
	if len(files) != 1 || files[0]["revision"] != 1.0 {
		t.Errorf("after shutdown: %v", files)
	}
	if got := lastStatus(t, h); got != "" {
		t.Errorf("status after shutdown %q, want cleared", got)
	}
}
