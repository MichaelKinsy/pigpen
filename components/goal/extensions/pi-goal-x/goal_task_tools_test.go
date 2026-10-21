package pi_goal_x

// Twins of tests/goal-task-tools.test.ts and tests/goal-task-lifecycle.test.ts that do not drive the extension: the flat conversion,
// the merge, the task confirmation, and the goal file round trips. The cases that drive the tools are in goal_task_tools_rig_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const gttFile = "goal-task-tools"
const gtlFile = "goal-task-lifecycle"

// ttFlat is a flat task list from JSON.
func ttFlat(t *testing.T, s string) []any {
	t.Helper()
	v, err := parseJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v.([]any)
}

func ttTasks(t *testing.T, s string) []any { return ttFlat(t, s) }

func ttConvert(t *testing.T, s string, depth int) []any {
	t.Helper()
	tasks, msg := convertFlatTasks(ttFlat(t, s), depth)
	if msg != "" {
		t.Fatalf("conversion failed: %s", msg)
	}
	return tasks
}

func ttIDs(tasks any) []string {
	var ids []string
	list, _ := tasks.([]any)
	for _, x := range list {
		ids = append(ids, gstr(x.(*jsObject), "id"))
	}
	return ids
}

func ttSubtasks(o *jsObject) []any {
	s, _ := o.vals["subtasks"].([]any)
	return s
}

func TestGoalTaskToolsPure(t *testing.T) {
	tw(t, gttFile, "flat input converts to the same recursive tree", func(t *testing.T) {
		tasks := ttConvert(t, `[{"id":"a","title":"A"},{"id":"b","title":"B","parent_id":"a"},{"id":"c","title":"C","parent_id":"a"},{"id":"d","title":"D","parent_id":"b"}]`, 2)
		eq(t, len(tasks), 1, "roots")
		a := tasks[0].(*jsObject)
		eq(t, gstr(a, "id"), "a", "id")
		eq(t, strings.Join(ttIDs(a.vals["subtasks"]), ","), "b,c", "children")
		eq(t, strings.Join(ttIDs(ttSubtasks(ttSubtasks(a)[0].(*jsObject))), ","), "d", "grandchild")
	})

	tw(t, gttFile, "flat conversion rejects duplicate ids, missing titles, and missing parents", func(t *testing.T) {
		for _, bad := range []string{
			`[{"id":"a","title":"A"},{"id":"a","title":"A2"}]`,
			`[{"id":"","title":"X"}]`,
			`[{"id":"a","title":""}]`,
			`[{"id":"a","title":"A","parent_id":"ghost"}]`,
		} {
			if _, msg := convertFlatTasks(ttFlat(t, bad), 1); msg == "" {
				t.Errorf("%s must be rejected", bad)
			}
		}
	})

	tw(t, gttFile, "flat conversion rejects cyclic parent relationships", func(t *testing.T) {
		_, msg := convertFlatTasks(ttFlat(t, `[{"id":"a","title":"A","parent_id":"b"},{"id":"b","title":"B","parent_id":"a"}]`), 1)
		if msg == "" {
			t.Fatal("cyclic must be rejected")
		}
		assertTrue(t, strings.Contains(msg, "Cyclic"), "message "+msg)
	})

	tw(t, gttFile, "flat conversion enforces the 50-task cap, depth cap, and lightweight placement", func(t *testing.T) {
		var many []string
		for i := 0; i < 51; i++ {
			many = append(many, `{"id":"t`+jsNumber(float64(i))+`","title":"T`+jsNumber(float64(i))+`"}`)
		}
		_, msg := convertFlatTasks(ttFlat(t, "["+strings.Join(many, ",")+"]"), 1)
		assertTrue(t, msg != "", "51 tasks must reject")
		_, msg = convertFlatTasks(ttFlat(t, `[{"id":"a","title":"A"},{"id":"b","title":"B","parent_id":"a"},{"id":"c","title":"C","parent_id":"b"}]`), 1)
		assertTrue(t, msg != "", "depth beyond configured maximum must reject")
		_, msg = convertFlatTasks(ttFlat(t, `[{"id":"a","title":"A","lightweight_subtasks":true}]`), 1)
		assertTrue(t, msg != "", "lightweight_subtasks without children must reject")
		_, msg = convertFlatTasks(ttFlat(t, `[{"id":"a","title":"A","lightweight_subtasks":true},{"id":"b","title":"B","parent_id":"a"}]`), 1)
		eq(t, msg, "", "lightweight placement on a parent is valid")
	})

	tw(t, gttFile, "mergeTasksWithExisting preserves status and evidence for matching ids", func(t *testing.T) {
		existing := ttTasks(t, `[{"id":"a","title":"Old A","status":"complete","evidence":"verified","completedAt":"2026-01-01T00:00:00.000Z","subtasks":[{"id":"a1","title":"A1 done","status":"complete","evidence":"yep"}]}]`)
		incoming := ttConvert(t, `[{"id":"a","title":"New A title"},{"id":"a1","title":"A1 renamed","parent_id":"a"},{"id":"b","title":"Brand new B"}]`, 1)
		merged := mergeTasksWithExisting(existing, incoming)
		m0 := merged[0].(*jsObject)
		eq(t, gstr(m0, "status"), "complete", "matching id keeps its status")
		eq(t, gstr(m0, "evidence"), "verified", "matching id keeps its evidence")
		eq(t, gstr(m0, "title"), "New A title", "title updates from input")
		eq(t, gstr(ttSubtasks(m0)[0].(*jsObject), "status"), "complete", "matching child keeps its status")
		eq(t, gstr(merged[1].(*jsObject), "status"), "pending", "new id starts pending")
	})

	tw(t, gttFile, "mergeTasksWithExisting: structural omission clears contract, flag, and children while progress survives", func(t *testing.T) {
		existing := ttTasks(t, `[{"id":"a","title":"Old A","status":"complete","evidence":"verified","completedAt":"2026-01-01T00:00:00.000Z","verificationContract":"Must verify A","lightweightSubtasks":true,"subtasks":[`+
			`{"id":"a1","title":"Child","status":"complete","evidence":"child-evidence","completedAt":"2026-01-01T00:00:00.000Z"},{"id":"a2","title":"Child2","status":"pending"}]}]`)
		incoming := ttConvert(t, `[{"id":"a","title":"A restructured"}]`, 1)
		a := mergeTasksWithExisting(existing, incoming)[0].(*jsObject)
		eq(t, gstr(a, "status"), "complete", "matching id keeps its runtime status")
		eq(t, gstr(a, "evidence"), "verified", "matching id keeps its evidence")
		eq(t, gstr(a, "completedAt"), "2026-01-01T00:00:00.000Z", "matching id keeps its completion timestamp")
		eq(t, gstr(a, "title"), "A restructured", "title is structural and authoritative")
		eq(t, a.has("verificationContract"), false, "omitted contract is cleared")
		eq(t, a.has("lightweightSubtasks"), false, "omitted lightweight flag is cleared")
		eq(t, a.has("subtasks"), false, "omitted children delete the subtree")
	})

	tw(t, gttFile, "mergeTasksWithExisting: moving a task between parents relocates it with progress", func(t *testing.T) {
		existing := ttTasks(t, `[{"id":"a","title":"A","status":"pending","subtasks":[{"id":"a1","title":"A1","status":"complete","evidence":"done-a1"}]},{"id":"b","title":"B","status":"pending"}]`)
		incoming := ttConvert(t, `[{"id":"a","title":"A"},{"id":"b","title":"B"},{"id":"a1","title":"A1 moved","parent_id":"b"}]`, 1)
		merged := mergeTasksWithExisting(existing, incoming)
		var a, b *jsObject
		for _, m := range merged {
			switch gstr(m.(*jsObject), "id") {
			case "a":
				a = m.(*jsObject)
			case "b":
				b = m.(*jsObject)
			}
		}
		moved := ttSubtasks(b)[0].(*jsObject)
		eq(t, gstr(moved, "id"), "a1", "task moved under b")
		eq(t, gstr(moved, "status"), "complete", "moved task keeps progress")
		eq(t, gstr(moved, "evidence"), "done-a1", "moved task keeps evidence")
		eq(t, a.has("subtasks"), false, "a no longer owns a1")
	})

	tw(t, gttFile, "mergeTasksWithExisting: skipping state is preserved for matching ids", func(t *testing.T) {
		existing := ttTasks(t, `[{"id":"s","title":"Old S","status":"skipped","skipReason":"user direction","skippedAt":"2026-01-02T00:00:00.000Z"}]`)
		incoming := ttConvert(t, `[{"id":"s","title":"New S title"}]`, 1)
		m := mergeTasksWithExisting(existing, incoming)[0].(*jsObject)
		eq(t, gstr(m, "status"), "skipped", "status")
		eq(t, gstr(m, "skipReason"), "user direction", "skipReason")
		eq(t, gstr(m, "skippedAt"), "2026-01-02T00:00:00.000Z", "skippedAt")
		eq(t, gstr(m, "title"), "New S title", "title")
	})

	tw(t, gttFile, "countTasks counts every node including descendants", func(t *testing.T) {
		tree := ttTasks(t, `[{"id":"a","title":"A","status":"pending","subtasks":[{"id":"a1","title":"A1","status":"pending","subtasks":[{"id":"a1x","title":"A1x","status":"pending"}]}]},{"id":"b","title":"B","status":"pending"}]`)
		eq(t, countTasks(tree), 4, "3 nested under a plus b")
		eq(t, countTasks(ttTasks(t, `[{"id":"c","title":"C","status":"pending"}]`)), 1, "one")
		eq(t, countTasks(nil), 0, "undefined")
	})

	tw(t, gttFile, "task approval supports RPC, absent custom UI, and explicit headless approval", func(t *testing.T) {
		t.Setenv("PI_GOAL_AUTO_CONFIRM", "")
		os.Unsetenv("PI_GOAL_AUTO_CONFIRM")
		for _, mode := range []string{"rpc", "interactive"} {
			for _, answer := range []*string{ptrString("Confirm task list"), ptrString("Keep current tasks"), nil} {
				ctx := &ttDialogCtx{hasUIFlag: true, modeName: mode, answer: answer, t: t}
				got, err := showTaskConfirmation(ctx, "probe: Inspect sample")
				if err != nil {
					t.Fatal(err)
				}
				want := "cancel"
				if answer != nil && *answer == "Confirm task list" {
					want = "confirm"
				}
				eq(t, got.decision, want, "decision")
				eq(t, ctx.shown, true, "select shown")
			}
		}
		headless := &ttDialogCtx{hasUIFlag: false, t: t}
		got, _ := showTaskConfirmation(headless, "proposal")
		eq(t, got.decision, "confirm", "headless default")
		t.Setenv("PI_GOAL_AUTO_CONFIRM", "0")
		got, _ = showTaskConfirmation(headless, "proposal")
		eq(t, got.decision, "cancel", "PI_GOAL_AUTO_CONFIRM=0")
		t.Setenv("PI_GOAL_AUTO_CONFIRM", "1")
		got, _ = showTaskConfirmation(headless, "proposal")
		eq(t, got.decision, "confirm", "PI_GOAL_AUTO_CONFIRM=1")
	})
}

func ptrString(s string) *string { return &s }

// ttDialogCtx is the ctx of the approval case: a custom UI that must not open under RPC and returns nothing, and a select that
// records that it was shown and answers (nil: dismissed).
type ttDialogCtx struct {
	hasUIFlag bool
	modeName  string
	answer    *string
	shown     bool
	t         *testing.T
}

func (c *ttDialogCtx) hasUI() bool      { return c.hasUIFlag }
func (c *ttDialogCtx) mode() string     { return c.modeName }
func (c *ttDialogCtx) cwd() string      { return "" }
func (c *ttDialogCtx) ui() goalDialogUI { return c }

func (c *ttDialogCtx) hasCustom() bool { return true }
func (c *ttDialogCtx) hasSelect() bool { return true }
func (c *ttDialogCtx) hasInput() bool  { return false }
func (c *ttDialogCtx) custom(goalCustomFactory, goalCustomOptions) (any, error) {
	if c.modeName == "rpc" {
		c.t.Error("RPC must not open terminal UI")
	}
	return nil, nil
}
func (c *ttDialogCtx) selectOption(title string, options []string) (string, bool, error) {
	c.shown = true
	if !strings.Contains(title, "probe: Inspect sample") {
		c.t.Errorf("title %q", title)
	}
	if c.answer == nil {
		return "", false, nil
	}
	return *c.answer, true, nil
}
func (c *ttDialogCtx) input(string, string) (string, bool, error) { return "", false, nil }
func (c *ttDialogCtx) notify(string, string)                      {}
func (c *ttDialogCtx) setWorkingVisible(bool)                     {}
func (c *ttDialogCtx) onTerminalInput(func(string) *goalInputResult) func() {
	return func() {}
}

// ---- goal-task-lifecycle: pure cases ----

func ltTasks(t *testing.T, s string) *jsObject {
	return jo(t, `{"tasks":`+s+`,"blockCompletion":false,"proposedAt":"`+time.Now().UTC().Format("2006-01-02T15:04:05.000Z")+`"}`)
}

func TestGoalTaskLifecyclePure(t *testing.T) {
	at := ms(2026, time.August, 5, 9, 0, 0)

	tw(t, gtlFile, "mergeTasksWithExisting preserves status, evidence, and timestamps", func(t *testing.T) {
		existing := ttTasks(t, `[{"id":"a","title":"Old","status":"complete","evidence":"verified","completedAt":"2026-01-01T00:00:00.000Z"}]`)
		incoming := ttConvert(t, `[{"id":"a","title":"Renamed"},{"id":"b","title":"New"}]`, 1)
		merged := mergeTasksWithExisting(existing, incoming)
		m0 := merged[0].(*jsObject)
		eq(t, gstr(m0, "status"), "complete", "status")
		eq(t, gstr(m0, "evidence"), "verified", "evidence")
		eq(t, gstr(m0, "completedAt"), "2026-01-01T00:00:00.000Z", "completedAt")
		eq(t, gstr(m0, "title"), "Renamed", "title")
		eq(t, gstr(merged[1].(*jsObject), "status"), "pending", "new")
	})

	tw(t, gtlFile, "legacy goal files load without currentTaskId and preserve task state", func(t *testing.T) {
		s := tempStore(t)
		goal := createGoal("Legacy goal", true, false, false, at)
		goal.set("taskList", ltTasks(t, `[{"id":"a","title":"A","status":"complete","evidence":"kept"},{"id":"b","title":"B"}]`))
		goal.set("verificationContract", "Run the suite.")
		goal.set("usage", jo(t, `{"tokensUsed":4200,"activeSeconds":300}`))
		// No currentTaskId anywhere: a historical file.
		written := mustWrite(t, s, goal)
		parsed := parseGoalFile(filepath.Join(s.cwd, gstr(written, "activePath")))
		assertTrue(t, parsed != nil, "parsed")
		eq(t, parsed.has("currentTaskId"), false, "legacy file has no focus")
		first := parsed.obj("taskList").vals["tasks"].([]any)[0].(*jsObject)
		eq(t, gstr(first, "status"), "complete", "status")
		eq(t, gstr(first, "evidence"), "kept", "evidence")
		eq(t, gstr(parsed, "verificationContract"), "Run the suite.", "contract")
		eq(t, marshalJSON(parsed.obj("usage"), ""), `{"tokensUsed":4200,"activeSeconds":300}`, "usage")
		eq(t, gstr(parsed, "status"), "active", "status")
	})

	tw(t, gtlFile, "currentTaskId persists through write→parse round trips", func(t *testing.T) {
		s := tempStore(t)
		goal := createGoal("Persist goal", true, false, false, at)
		goal.set("taskList", ltTasks(t, `[{"id":"a","title":"A"},{"id":"b","title":"B"}]`))
		goal.set("currentTaskId", "b")
		written := mustWrite(t, s, goal)
		parsed := parseGoalFile(filepath.Join(s.cwd, gstr(written, "activePath")))
		eq(t, gstr(parsed, "currentTaskId"), "b", "focus survives a write→read round trip")
	})

	tw(t, gtlFile, "stale currentTaskId is cleared during normalization", func(t *testing.T) {
		base := createGoal("Stale goal", true, false, false, at)
		list := `{"tasks":[{"id":"a","title":"A"},{"id":"b","title":"B"}],"blockCompletion":false,"proposedAt":"2026-08-05T09:00:00.000Z"}`
		base.set("taskList", jo(t, list))
		with2 := func(kv ...any) *jsObject { return with(base, kv...) }
		listWith := func(tasks string) *jsObject {
			return jo(t, `{"tasks":`+tasks+`,"blockCompletion":false,"proposedAt":"2026-08-05T09:00:00.000Z"}`)
		}
		// Removed id.
		removed := normalizeGoalRecord(with2("currentTaskId", "ghost"))
		eq(t, removed.has("currentTaskId"), false, "removed id is cleared")
		// Completed id.
		completeTask := normalizeGoalRecord(with2("taskList", listWith(`[{"id":"a","title":"A","status":"complete"},{"id":"b","title":"B"}]`), "currentTaskId", "a"))
		eq(t, completeTask.has("currentTaskId"), false, "complete id is cleared")
		// Skipped id.
		skippedTask := normalizeGoalRecord(with2("taskList", listWith(`[{"id":"a","title":"A","status":"skipped"},{"id":"b","title":"B"}]`), "currentTaskId", "a"))
		eq(t, skippedTask.has("currentTaskId"), false, "skipped id is cleared")
		// Nested pending id.
		nested := normalizeGoalRecord(with2("taskList", listWith(`[{"id":"p","title":"P","subtasks":[{"id":"p.1","title":"P1"}]}]`), "currentTaskId", "p.1"))
		eq(t, gstr(nested, "currentTaskId"), "p.1", "nested pending id is accepted")
		// Pending id.
		valid := normalizeGoalRecord(with2("currentTaskId", "a"))
		eq(t, gstr(valid, "currentTaskId"), "a", "pending id is accepted")
		// No task list.
		noTasks := normalizeGoalRecord(with2("taskList", undef{}, "currentTaskId", "a"))
		eq(t, noTasks.has("currentTaskId"), false, "no task list means no focus")
	})

	tw(t, gtlFile, "normalization never rewrites legacy files", func(t *testing.T) {
		s := tempStore(t)
		goal := createGoal("Lifecycle goal", true, false, false, at)
		goal.set("taskList", ltTasks(t, `[{"id":"a","title":"A"},{"id":"b","title":"B"}]`))
		written := mustWrite(t, s, goal)
		file := filepath.Join(s.cwd, gstr(written, "activePath"))
		raw, _ := os.ReadFile(file)
		parsed := parseGoalFile(file)
		assertTrue(t, parsed != nil, "parsed")
		parsed.set("currentTaskId", "ghost")
		again := normalizeGoalRecord(jo(t, marshalJSON(parsed, "")))
		eq(t, again.has("currentTaskId"), false, "stale focus normalized away")
		// Reading must not modify the file.
		parseGoalFile(file)
		parseGoalFile(file)
		after, _ := os.ReadFile(file)
		eq(t, string(after), string(raw), "parse/normalize never rewrites the file")
	})

	tw(t, gtlFile, "serializeGoalFile round-trips currentTaskId in the JSON header", func(t *testing.T) {
		goal := createGoal("Round trip", true, false, false, at)
		goal.set("taskList", ltTasks(t, `[{"id":"a","title":"A"}]`))
		goal.set("currentTaskId", "a")
		serialized := serializeGoalFile(goal)
		assertTrue(t, strings.Contains(serialized, `"currentTaskId": "a"`), "JSON header carries currentTaskId")
		end := strings.Index(serialized, "\n\n# Goal Prompt")
		raw := jo(t, serialized[:end])
		raw.set("objective", "Round trip")
		reparsed := normalizeGoalRecord(raw)
		eq(t, gstr(reparsed, "currentTaskId"), "a", "currentTaskId")
	})
}
