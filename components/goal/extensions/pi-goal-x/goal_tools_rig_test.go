package pi_goal_x_test

// Shared helpers of the GoalTools twins (the original's createHarness/makeFixture of tests/goal-core-tools.test.ts and friends).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

func boolPtr(b bool) *bool { return &b }

// toolOut is the AgentToolResult a tool returned: its text, the terminate flag (nil when undefined) and its details.
type toolOut struct {
	Content   string         `json:"content"`
	Terminate *bool          `json:"terminate"`
	Details   map[string]any `json:"details"`
	IsError   bool           `json:"is_error"`
}

func (o toolOut) terminated() bool { return o.Terminate != nil && *o.Terminate }

// callTool executes a registered tool; a thrown error fails the test.
func (r *goalRig) callTool(name string, params map[string]any) toolOut {
	r.t.Helper()
	raw, failure := r.Tool(name, params)
	if failure != "" {
		r.t.Fatalf("%s failed: %s", name, failure)
	}
	return decodeToolOut(r.t, raw)
}

// callToolErr executes a tool that is expected to throw; it returns the error text.
func (r *goalRig) callToolErr(name string, params map[string]any) string {
	r.t.Helper()
	_, failure := r.Tool(name, params)
	return failure
}

func decodeToolOut(t *testing.T, raw json.RawMessage) toolOut {
	t.Helper()
	var out toolOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("tool result %s: %v", raw, err)
	}
	return out
}

// toolsFixture is makeFixture: a temp cwd with one active goal, and the session entries that focus it.
type toolsFixture struct {
	cwd     string
	goal    map[string]any
	entries []any
}

func goalAt(y int, m time.Month, d, h, mi, s int) int64 {
	return time.Date(y, m, d, h, mi, s, 0, time.UTC).UnixMilli()
}

// newToolsFixture writes the goal (overrides: a JSON object of record fields) into a fresh cwd.
func newToolsFixture(t *testing.T, objective string, sisyphus bool, at int64, overrides string) toolsFixture {
	t.Helper()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "goals", "archived"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := goal.ToolsGoal(cwd, objective, sisyphus, at, overrides)
	return toolsFixture{cwd: cwd, goal: g, entries: []any{goal.ToolsFocusEntry(g["id"].(string))}}
}

// start is the original's start(h): session_start, then before_agent_start.
func (r *goalRig) startToolsSession() {
	r.t.Helper()
	r.Fire("session_start", map[string]any{"reason": "start"})
	r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "test", "systemPromptOptions": map[string]any{}})
}

// newToolsRig is createHarness({cwd, sessionEntries}) with no UI and no settings file unless given.
func newToolsRig(t *testing.T, f toolsFixture, opts rigOptions) *goalRig {
	t.Helper()
	opts.Cwd = f.cwd
	opts.Branch = f.entries
	if opts.HasUI == nil {
		opts.HasUI = boolPtr(false)
	}
	if opts.Settings == "" {
		opts.Settings = "-"
	}
	if opts.ActiveTools == nil {
		opts.ActiveTools = []string{"read", "bash", "edit", "write"}
	}
	return newGoalRig(t, opts)
}

func writeSettings(t *testing.T, cwd, json string) {
	t.Helper()
	p := filepath.Join(cwd, ".pi", "pi-goal-x-settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
}

func eventsOfType(cwd, typ string) []map[string]any {
	var out []map[string]any
	for _, e := range goal.ToolsLedgerEvents(cwd) {
		if e["type"] == typ {
			out = append(out, e)
		}
	}
	return out
}

func hasEvent(cwd, typ string) bool { return len(eventsOfType(cwd, typ)) > 0 }

func contains(t *testing.T, s, sub, msg string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%s: %q does not contain %q", msg, s, sub)
	}
}

func notContains(t *testing.T, s, sub, msg string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("%s: %q contains %q", msg, s, sub)
	}
}

func equal(t *testing.T, got, want any, msg string) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("%s: got %s want %s", msg, g, w)
	}
}

// tasksOf is goal.taskList.tasks of a decoded goal.
func tasksOf(g map[string]any) []any {
	tl, _ := g["taskList"].(map[string]any)
	tasks, _ := tl["tasks"].([]any)
	return tasks
}

func taskByID(tasks []any, id string) map[string]any {
	for _, t := range tasks {
		if m, ok := t.(map[string]any); ok && m["id"] == id {
			return m
		}
	}
	return nil
}

// newEmptyToolsFixture is a cwd with the goals directory and no goal (sessionEntries: []).
func newEmptyToolsFixture(t *testing.T) toolsFixture {
	t.Helper()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "goals", "archived"), 0o755); err != nil {
		t.Fatal(err)
	}
	return toolsFixture{cwd: cwd}
}

// turnEnd fires the turn_end handler with a stopped assistant message (deferred archival runs there).
func (r *goalRig) turnEnd() {
	r.t.Helper()
	r.Fire("turn_end", map[string]any{"message": map[string]any{"role": "assistant", "stopReason": "stop", "usage": map[string]any{"input": 0, "output": 0}}})
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func regexpMatch(pattern, s string) bool { return regexp.MustCompile(`(?s)` + pattern).MatchString(s) }
