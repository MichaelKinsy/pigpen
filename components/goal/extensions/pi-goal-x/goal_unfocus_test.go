package pi_goal_x_test

// Twins of tests/goal-unfocus.test.ts: /goal-unfocus and what the session does after it, through the real SDK (goalRig). The prompt
// tests need the GoalEvents slice (before_agent_start, context, tool_call, message_end, turn_end, agent_end, session_tree) and the
// two async ones the GoalTools slice (update_goal, set_goal_tasks); the command-only ones pass on the kernel.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const gufF = "goal-unfocus"

const gufSession = "unfocus-test-session"

const gufDefaultSettings = `{"autoSelectSingleGoal": false, "disabled": true}`

type gufFixture struct {
	t        *testing.T
	cwd      string
	id       string
	rel      string // the goal file under cwd
	original []byte
}

// gufGoalFile is a goal file of the fixture's shape: status, autoContinue and pauseReason are the overrides the tests apply.
func gufGoalFile(id, objective, status string, autoContinue bool, pauseReason string) (rel, text string) {
	rel = ".pi/goals/active_goal_20260102030405_" + id + ".md"
	extra := ""
	if pauseReason != "" {
		extra = `  "pauseReason": "` + pauseReason + `",` + "\n"
	}
	head := `{
  "version": 3,
  "id": "` + id + `",
  "objective": "` + objective + `",
  "status": "` + status + `",
  "autoContinue": ` + fmt.Sprint(autoContinue) + `,
  "usage": {"tokensUsed": 0, "activeSeconds": 0},
  "sisyphus": false,
` + extra + `  "revision": 0,
  "createdAt": "2026-01-02T03:04:05.000Z",
  "updatedAt": "2026-01-02T03:04:05.000Z",
  "activePath": "` + rel + `"
}`
	return rel, head + "\n\n# Goal Prompt\n\n" + objective + "\n\n## Progress\n"
}

// newGufFixture is createFixture(): a project with an (empty) archive directory, the settings file and one open goal.
func newGufFixture(t *testing.T, status string, autoContinue bool, pauseReason string) *gufFixture {
	t.Helper()
	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "goals", "archived"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel, text := gufGoalFile("unfocus-goal", "Keep this shared goal open", status, autoContinue, pauseReason)
	f := &gufFixture{t: t, cwd: cwd, id: "unfocus-goal", rel: rel}
	f.write(rel, text)
	f.original = []byte(text)
	return f
}

func (f *gufFixture) write(rel, text string) {
	f.t.Helper()
	p := filepath.Join(f.cwd, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *gufFixture) file() []byte {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.cwd, filepath.FromSlash(f.rel)))
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *gufFixture) archived() int {
	entries, _ := os.ReadDir(filepath.Join(f.cwd, ".pi", "goals", "archived"))
	return len(entries)
}

func (f *gufFixture) ledgerExists() bool {
	_, err := os.Stat(filepath.Join(f.cwd, ".pi", "goals", "goal_events.jsonl"))
	return err == nil
}

func (f *gufFixture) ledger() string {
	b, _ := os.ReadFile(filepath.Join(f.cwd, ".pi", "goals", "goal_events.jsonl"))
	return string(b)
}

func (f *gufFixture) rig(opts rigOptions) *goalRig {
	f.t.Helper()
	opts.Cwd = f.cwd
	if opts.Settings == "" {
		opts.Settings = gufDefaultSettings
	}
	if opts.SessionID == "" {
		opts.SessionID = gufSession
	}
	return newGoalRig(f.t, opts)
}

func (f *gufFixture) focusedBranch(reason string) []any {
	return []any{map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1, "focusedGoalId": f.id, "reason": reason}}}
}

func latestFocus(r *goalRig) map[string]any {
	es := r.Entries()
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].CustomType == "pi-goal-focus" {
			return es[i].Data
		}
	}
	return nil
}

func focusCount(r *goalRig) int {
	n := 0
	for _, e := range r.Entries() {
		if e.CustomType == "pi-goal-focus" {
			n++
		}
	}
	return n
}

var gufUnfocused = map[string]any{"version": 1.0, "focusedGoalId": nil, "reason": "unfocused"}

// contextTail is the content of the last message the context handler returns ("" when none).
func contextTail(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	msgs := ctxMessages(t, raw)
	if len(msgs) == 0 {
		return ""
	}
	return content(msgs[len(msgs)-1])
}

func promptIncludes(t *testing.T, raw json.RawMessage, text string) bool {
	t.Helper()
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	msgs := ctxMessages(t, raw)
	if len(msgs) > 2 {
		msgs = msgs[len(msgs)-2:]
	}
	for _, m := range msgs {
		if strings.Contains(content(m), text) {
			return true
		}
	}
	return false
}

func hasUnfocusedReminder(raw json.RawMessage) bool {
	return strings.Contains(string(raw), "PI GOAL UNFOCUSED")
}

func (r *goalRig) beforeAgentStart(prompt string) {
	r.t.Helper()
	r.Fire("before_agent_start", map[string]any{"systemPrompt": "base prompt", "prompt": prompt})
}

func (r *goalRig) contextRequest() json.RawMessage {
	r.t.Helper()
	return r.Fire("context", map[string]any{"messages": []any{}})
}

func (r *goalRig) aborts() int { return len(r.Host.CallsTo("abort")) }

func TestGoalUnfocusTwins(t *testing.T) {
	tw(t, gufF, "/goal-unfocus is idempotent, session-local, and leaves the active goal byte-for-byte unchanged", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		r := f.rig(rigOptions{Branch: f.focusedBranch("created")})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		if failure := r.Command("goal-unfocus", ""); failure != "" {
			t.Fatal(failure)
		}
		same(t, latestFocus(r), gufUnfocused, "the explicit null focus entry")
		same(t, string(f.file()), string(f.original), "the goal file")
		same(t, f.ledgerExists(), false, "unfocus must not write the shared ledger")
		same(t, f.archived(), 0, "archived")
		reMatch(t, r.LastNotice(), `remains open in \.pi/goals`, "notice")

		count := focusCount(r)
		r.Command("goal-unfocus", "")
		same(t, focusCount(r), count+1, "repeating records another entry")
		same(t, latestFocus(r), gufUnfocused, "the repeated entry")
		same(t, string(f.file()), string(f.original), "the goal file")

		r.beforeAgentStart("ordinary user request")
		tail := contextTail(t, r.contextRequest())
		reMatch(t, tail, `\[PI GOAL UNFOCUSED\]`, "the unfocused reminder")
		if strings.Contains(tail, "[PI GOAL ACTIVE") {
			t.Error("an unfocused session carries the active prompt")
		}
	})

	tw(t, gufF, "hideUnfocusedPrompt suppresses the model-facing reminder on every request without touching goal state (#72)", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		r := f.rig(rigOptions{Settings: `{"autoSelectSingleGoal": false, "disabled": true, "hideUnfocusedPrompt": true}`})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		r.beforeAgentStart("ordinary user request")
		for i := range 3 {
			if hasUnfocusedReminder(r.contextRequest()) {
				t.Errorf("request %d must not carry the unfocused reminder", i+1)
			}
		}
		same(t, string(f.file()), string(f.original), "hiding the reminder must not mutate the goal file")
		same(t, focusCount(r), 0, "hiding the reminder must not change focus")
		same(t, f.ledgerExists(), false, "ledger")
	})

	tw(t, gufF, "hideUnfocusedPrompt takes effect on the next request after a settings change", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		settings := filepath.Join(f.cwd, ".pi", "pi-goal-x-settings.json")
		r := f.rig(rigOptions{})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		r.beforeAgentStart("hello")
		reMatch(t, contextTail(t, r.contextRequest()), `\[PI GOAL UNFOCUSED\]\n1 open pi goal exists,`, "default keeps the reminder")
		set := func(text string) {
			if err := os.WriteFile(settings, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		set(`{"autoSelectSingleGoal": false, "disabled": true, "hideUnfocusedPrompt": true}`)
		same(t, hasUnfocusedReminder(r.contextRequest()), false, "true hides it")
		set(`{"autoSelectSingleGoal": false, "disabled": true, "hideUnfocusedPrompt": false}`)
		reMatch(t, contextTail(t, r.contextRequest()), `\[PI GOAL UNFOCUSED\]`, "false restores it")
	})

	tw(t, gufF, "hideUnfocusedBanner alone does not suppress the model-facing reminder", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		r := f.rig(rigOptions{Settings: `{"autoSelectSingleGoal": false, "disabled": true, "hideUnfocusedBanner": true}`})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		r.beforeAgentStart("hello")
		reMatch(t, contextTail(t, r.contextRequest()), `\[PI GOAL UNFOCUSED\]`, "the banner setting must not reach the prompt path")
	})

	tw(t, gufF, "hideUnfocusedPrompt never suppresses stale-checkpoint rejection", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		r := f.rig(rigOptions{Settings: `{"autoSelectSingleGoal": false, "disabled": true, "hideUnfocusedPrompt": true}`})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		r.beforeAgentStart(`<pi_goal_continuation goal_id="ghost-goal" kind="checkpoint">continue`)
		same(t, r.aborts(), 1, "stale checkpoint must still abort the turn")
		reMatch(t, contextTail(t, r.contextRequest()), `\[GOAL STALE goalId=ghost-goal\]`, "the stale note")
	})

	tw(t, gufF, "/goal-unfocus aborts a busy goal turn, blocks later tools, and prevents abort handlers from pausing it", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		r := f.rig(rigOptions{Idle: func() bool { return false }, Branch: f.focusedBranch("created")})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		r.Command("goal-unfocus", "")
		same(t, r.aborts(), 1, "aborts")
		gate := gufToolGate(t, r, "write", map[string]any{"path": "late.txt", "content": "late"})
		same(t, gate["block"], true, "the later tool is blocked")
		reMatch(t, str(gate["reason"]), `already stopped earlier in this turn`, "reason")
		aborted := map[string]any{"role": "assistant", "stopReason": "aborted", "usage": map[string]any{"input": 0, "output": 0}}
		r.Fire("message_end", map[string]any{"message": aborted})
		r.Fire("turn_end", map[string]any{"message": aborted})
		r.Fire("agent_end", map[string]any{"messages": []any{aborted}})
		same(t, string(f.file()), string(f.original), "the goal file")
		same(t, f.archived(), 0, "archived")
	})

	tw(t, gufF, "/goal-unfocus preserves a paused goal and does not archive it", func(t *testing.T) {
		f := newGufFixture(t, "paused", false, "waiting")
		r := f.rig(rigOptions{Branch: f.focusedBranch("created")})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		r.Command("goal-unfocus", "")
		same(t, string(f.file()), string(f.original), "the goal file")
		same(t, f.archived(), 0, "archived")
	})

	tw(t, gufF, "/goal-unfocus records explicit null focus when the active goal file disappeared", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		r := f.rig(rigOptions{Branch: f.focusedBranch("selected")})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		if err := os.Remove(filepath.Join(f.cwd, filepath.FromSlash(f.rel))); err != nil {
			t.Fatal(err)
		}
		r.Command("goal-unfocus", "")
		same(t, latestFocus(r), gufUnfocused, "the explicit null focus entry")
	})

	tw(t, gufF, "busy missing-file detachment still aborts and blocks the former goal turn", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		r := f.rig(rigOptions{Idle: func() bool { return false }, Branch: f.focusedBranch("selected")})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		if err := os.Remove(filepath.Join(f.cwd, filepath.FromSlash(f.rel))); err != nil {
			t.Fatal(err)
		}
		r.Command("goal-unfocus", "")
		same(t, r.aborts(), 1, "aborts")
		gate := gufToolGate(t, r, "bash", map[string]any{"command": "echo late"})
		same(t, gate["block"], true, "the later tool is blocked")
	})

	tw(t, gufF, "already-unfocused command does not abort unrelated busy work", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		r := f.rig(rigOptions{Idle: func() bool { return false }, Branch: []any{map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1, "focusedGoalId": nil, "reason": "unfocused"}}}})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		r.Command("goal-unfocus", "")
		same(t, r.aborts(), 0, "aborts")
		same(t, latestFocus(r), gufUnfocused, "the explicit null focus entry")
	})

	tw(t, gufF, "one session can unfocus while another remains focused on the shared goal", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		first := f.rig(rigOptions{Branch: f.focusedBranch("selected")})
		second := f.rig(rigOptions{Branch: f.focusedBranch("selected")})
		first.Fire("session_start", map[string]any{"reason": "startup"})
		second.Fire("session_start", map[string]any{"reason": "startup"})
		first.Command("goal-unfocus", "")
		first.beforeAgentStart("hello")
		firstPrompt := first.contextRequest()
		second.beforeAgentStart("continue")
		secondPrompt := second.contextRequest()
		reMatch(t, contextTail(t, firstPrompt), `\[PI GOAL UNFOCUSED\]`, "the unfocused session")
		same(t, promptIncludes(t, secondPrompt, "[PI GOAL ACTIVE goalId="+f.id+"]"), true, "the focused session keeps the goal")
		same(t, f.ledgerExists(), false, "ledger")
	})

	tw(t, gufF, "autoSelectSingleGoal opt-in focuses one goal on resume when no focus entry exists", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		selects := 0
		r := f.rig(rigOptions{Settings: `{"autoSelectSingleGoal": true, "disabled": true}`, Select: func(string, []string) (string, bool) { selects++; return "", false }})
		r.Fire("session_start", map[string]any{"reason": "resume"})
		same(t, selects, 0, "no selection prompt")
		r.beforeAgentStart("continue")
		same(t, promptIncludes(t, r.contextRequest(), "[PI GOAL ACTIVE goalId="+f.id+"]"), true, "the goal is focused")
	})

	tw(t, gufF, "the null entry produced by unfocus survives resume/tree and suppresses opt-in auto-selection", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		settings := `{"autoSelectSingleGoal": true, "disabled": true}`
		producer := f.rig(rigOptions{Settings: settings, Branch: f.focusedBranch("selected")})
		producer.Fire("session_start", map[string]any{"reason": "startup"})
		producer.Command("goal-unfocus", "")
		produced := latestFocus(producer)
		if produced == nil {
			t.Fatal("no focus entry was produced")
		}
		entry := func(data map[string]any) []any {
			return []any{map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": data}}
		}
		selects := 0
		resumed := f.rig(rigOptions{Settings: settings, Branch: entry(produced), Select: func(string, []string) (string, bool) { selects++; return "", false }})
		resumed.Fire("session_start", map[string]any{"reason": "resume"})
		resumed.beforeAgentStart("hello")
		reMatch(t, contextTail(t, resumed.contextRequest()), `\[PI GOAL UNFOCUSED\]`, "one open goal must not auto-select despite opt-in setting")

		// The branch changes under the session: the host serves another leaf's entries.
		setBranch := func(data map[string]any) {
			resumed.mu.Lock()
			resumed.branch = entry(data)
			resumed.mu.Unlock()
		}
		focusedData := map[string]any{"version": 1, "focusedGoalId": f.id, "reason": "selected"}
		setBranch(focusedData)
		resumed.Fire("session_tree", map[string]any{"newLeafId": "focused-branch", "oldLeafId": "unfocused-branch"})
		resumed.beforeAgentStart("focused branch")
		same(t, promptIncludes(t, resumed.contextRequest(), "[PI GOAL ACTIVE goalId="+f.id+"]"), true, "the focused branch")

		setBranch(produced)
		resumed.Fire("session_tree", map[string]any{"newLeafId": "unfocused-branch", "oldLeafId": "focused-branch"})
		resumed.beforeAgentStart("after tree")
		reMatch(t, contextTail(t, resumed.contextRequest()), `\[PI GOAL UNFOCUSED\]`, "back on the unfocused branch")

		rel, text := gufGoalFile("second-goal", "Second shared goal", "active", true, "")
		f.write(rel, text)
		setBranch(map[string]any{"version": 1, "focusedGoalId": "second-goal", "reason": "selected"})
		resumed.Fire("session_tree", map[string]any{"newLeafId": "second-goal-branch", "oldLeafId": "unfocused-branch"})
		resumed.beforeAgentStart("second goal")
		same(t, promptIncludes(t, resumed.contextRequest(), "[PI GOAL ACTIVE goalId=second-goal]"), true, "the second goal")

		selects = 0
		multi := f.rig(rigOptions{Settings: settings, Branch: entry(produced), Select: func(string, []string) (string, bool) { selects++; return "", false }})
		multi.Fire("session_start", map[string]any{"reason": "resume"})
		same(t, selects, 0, "resume must not prompt to replace an explicit null focus")
	})

	tw(t, gufF, "an approved completion audit cannot complete or archive a goal after unfocus", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		started := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		var signal context.Context
		r := f.rig(rigOptions{
			Settings: `{"autoSelectSingleGoal": false}`, Idle: func() bool { return false }, Branch: f.focusedBranch("created"),
			Deps: goal.RigDeps{Auditor: func(ctx context.Context, _ map[string]any, _, _ string) goal.RigAuditResult {
				signal = ctx
				once.Do(func() { close(started) })
				<-release
				return goal.RigAuditResult{Approved: true, Output: "Looks good\n<approved/>", Model: "mock"}
			}},
		})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		r.beforeAgentStart("finish")
		done := r.ToolAsync("update_goal", map[string]any{"status": "complete"})
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("the audit did not start")
		}
		r.Command("goal-unfocus", "")
		same(t, signal.Err() != nil, true, "the audit's signal is aborted")
		close(release)
		out := <-done
		if out.Failure != "" {
			t.Fatal(out.Failure)
		}
		reMatch(t, decodeToolResult(t, out.Raw).Text, `Goal completion cancelled because goal .* is no longer focused`, "result")
		r.Fire("turn_end", map[string]any{"message": map[string]any{"role": "assistant", "stopReason": "stop", "usage": map[string]any{"input": 0, "output": 0}}})
		same(t, string(f.file()), string(f.original), "the goal file")
		same(t, f.archived(), 0, "archived")
		ledger := f.ledger()
		for _, typ := range []string{"audit_result", "goal_completed"} {
			if strings.Contains(ledger, `"type":"`+typ+`"`) {
				t.Errorf("the ledger has %s", typ)
			}
		}
	})

	tw(t, gufF, "an async task-list confirmation cannot mutate the goal after unfocus", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		resolve := make(chan struct{})
		hasUI := true
		r := f.rig(rigOptions{
			HasUI: &hasUI, Idle: func() bool { return false }, Branch: f.focusedBranch("created"),
			Custom: func(map[string]any) map[string]any {
				<-resolve
				return CustomResult(map[string]any{
					"cancelled": false, "questions": []any{},
					"answers":        []any{map[string]any{"id": "confirm", "question": "Confirm Goal Draft", "answer": "Confirm — create this goal now", "wasCustom": false}},
					"auditorEnabled": true,
				})
			},
		})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		done := r.ToolAsync("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "t1", "title": "Must not be applied"}}, "block_completion": false})
		time.Sleep(50 * time.Millisecond)
		r.Command("goal-unfocus", "")
		close(resolve)
		out := <-done
		if out.Failure != "" {
			t.Fatal(out.Failure)
		}
		reMatch(t, decodeToolResult(t, out.Raw).Text, `cancelled because goal .* is no longer focused`, "result")
		same(t, string(f.file()), string(f.original), "the goal file")
	})

	tw(t, gufF, "restoring default-pool focus cannot select a same-ID goal in an external pool", func(t *testing.T) {
		f := newGufFixture(t, "active", true, "")
		external := filepath.Join(f.cwd, "external-pool")
		if err := os.MkdirAll(external, 0o755); err != nil {
			t.Fatal(err)
		}
		_, text := gufGoalFile(f.id, "Keep this shared goal open", "active", true, "")
		f.write("external-pool/active_goal_20260102030405_"+f.id+".md", text)
		root, _ := json.Marshal(external)
		r := f.rig(rigOptions{Settings: `{"goalsRoot": ` + string(root) + `, "disabled": true}`, Branch: f.focusedBranch("selected")})
		r.Fire("session_start", map[string]any{"reason": "startup"})
		raw := r.contextRequest()
		same(t, hasUnfocusedReminder(raw), true, "the session is unfocused in the external pool")
		if regexp.MustCompile(`PI GOAL ACTIVE`).Match(raw) {
			t.Error("the same-id goal of the external pool was selected")
		}
	})
}

// gufToolGate fires tool_call and decodes the handler's {block, reason} (empty when it did not block).
func gufToolGate(t *testing.T, r *goalRig, name string, input map[string]any) map[string]any {
	t.Helper()
	raw := r.Fire("tool_call", map[string]any{"toolName": name, "toolCallId": "late", "input": input})
	var gate map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &gate)
	}
	return gate
}
