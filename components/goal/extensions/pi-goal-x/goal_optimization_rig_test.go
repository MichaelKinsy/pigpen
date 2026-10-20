package pi_goal_x_test

// The cases of tests/goal-optimization.test.ts that drive get_goal, update_goal_task and update_goal through the extension.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const optFile = "goal-optimization"

const optRigTasks = `[{"id":"parent","title":"Parent","status":"pending","subtasks":[{"id":"child","title":"Child","status":"pending","verificationContract":"Prove the child"}]},{"id":"next","title":"Next","status":"pending"}]`

// optRigFixture is fixture(): focusedFixture() plus the task tree, with the harness started.
func optRigFixture(t *testing.T) (*goalRig, toolsFixture) {
	t.Helper()
	f := newToolsFixture(t, "Benchmark goal 1 — implement the thing end to end.", false, goalAt(2026, time.September, 1, 0, 0, 0), "")
	f.goal = goal.ToolsGoal(f.cwd, "Benchmark goal 1 — implement the thing end to end.", false, goalAt(2026, time.September, 1, 0, 0, 0),
		`{"id":"`+f.goal["id"].(string)+`","taskList":{"tasks":`+optRigTasks+`,"blockCompletion":true,"proposedAt":"2026-09-07T00:00:00Z"}}`)
	r := newToolsRig(t, f, rigOptions{})
	r.startToolsSession()
	return r, f
}

func taskEventTypes(cwd string) []string {
	var out []string
	for _, e := range goal.ToolsLedgerEvents(cwd) {
		if typ, _ := e["type"].(string); len(typ) > 5 && typ[:5] == "task_" {
			out = append(out, typ)
		}
	}
	return out
}

func TestGoalOptimizationRig(t *testing.T) {
	tw(t, optFile, "get_goal exposes full task evidence through bounded sections and keeps legacy forms", func(t *testing.T) {
		r, _ := optRigFixture(t)
		get := func(in map[string]any) string { return r.callTool("get_goal", in).Content }
		matches(t, get(map[string]any{"section": "tasks", "task_id": "child"}), `Prove the child`)
		matches(t, get(map[string]any{"section": "tasks", "task_id": "missing"}), `not found`)
		matches(t, get(map[string]any{"verbose": true}), `Lifecycle:`)
		matches(t, get(map[string]any{"include_history": true}), `Goal`)
	})

	tw(t, optFile, "ordered batch completes child then parent and starts the next task in one mutation", func(t *testing.T) {
		r, f := optRigFixture(t)
		before := goal.ToolsParseGoalFile(filepath.Join(f.cwd, f.goal["activePath"].(string)))
		out := r.callTool("update_goal_task", map[string]any{"updates": []any{
			map[string]any{"task_id": "child", "status": "complete", "evidence": "Verified the artifact"},
			map[string]any{"task_id": "parent", "status": "complete"},
			map[string]any{"task_id": "next", "status": "start"}}})
		matches(t, out.Content, `parent complete`)
		disk := goal.ToolsParseGoalFile(filepath.Join(f.cwd, f.goal["activePath"].(string)))
		equal(t, disk["currentTaskId"], "next", "currentTaskId")
		equal(t, tasksOf(disk)[0].(map[string]any)["status"], "complete", "parent status")
		rev, _ := before["revision"].(float64)
		equal(t, disk["revision"], rev+1, "one mutation, one revision")
		equal(t, taskEventTypes(f.cwd), []string{"task_complete", "task_complete", "task_started"}, "ledger")
	})

	tw(t, optFile, "invalid batches reject all changes, including mixed forms and missing evidence", func(t *testing.T) {
		r, f := optRigFixture(t)
		path := filepath.Join(f.cwd, f.goal["activePath"].(string))
		disk := func() string { b, _ := os.ReadFile(path); return string(b) }
		before := disk()
		for _, updates := range [][]any{
			{map[string]any{"task_id": "next", "status": "start"}, map[string]any{"task_id": "parent", "status": "complete"}},
			{map[string]any{"task_id": "next", "status": "start"}, map[string]any{"task_id": "child", "status": "complete"}},
			{map[string]any{"task_id": "next", "status": "start"}, map[string]any{"task_id": "missing", "status": "complete"}},
		} {
			r.callTool("update_goal_task", map[string]any{"updates": updates})
			equal(t, disk(), before, "the file is untouched")
		}
		mixed := r.callTool("update_goal_task", map[string]any{"task_id": "next", "status": "start", "updates": []any{map[string]any{"task_id": "next", "status": "start"}}})
		matches(t, mixed.Content, `never both`)
		equal(t, len(taskEventTypes(f.cwd)), 0, "no task events")
	})

	tw(t, optFile, "batch validates a fresh disk tree under the mutation lock", func(t *testing.T) {
		r, f := optRigFixture(t)
		external := goal.ToolsParseGoalFile(filepath.Join(f.cwd, f.goal["activePath"].(string)))
		parent := tasksOf(external)[0].(map[string]any)
		parent["subtasks"].([]any)[0].(map[string]any)["verificationContract"] = "New externally edited contract"
		parent["title"] = "Externally edited parent title"
		if err := os.WriteFile(filepath.Join(f.cwd, external["activePath"].(string)), []byte(goal.ToolsSerializeGoal(jsonOf(external))), 0o644); err != nil {
			t.Fatal(err)
		}
		r.callTool("update_goal_task", map[string]any{"updates": []any{map[string]any{"task_id": "child", "status": "start"}}})
		disk := goal.ToolsParseGoalFile(filepath.Join(f.cwd, external["activePath"].(string)))
		diskParent := tasksOf(disk)[0].(map[string]any)
		equal(t, diskParent["title"], "Externally edited parent title", "title")
		equal(t, diskParent["subtasks"].([]any)[0].(map[string]any)["verificationContract"], "New externally edited contract", "contract")
	})

	tw(t, optFile, "buffered batch detects a competing writer at flush and cannot overwrite its objective", func(t *testing.T) {
		r, f := optRigFixture(t)
		r.Fire("turn_start", map[string]any{})
		r.callTool("update_goal_task", map[string]any{"updates": []any{map[string]any{"task_id": "child", "status": "complete", "evidence": "verified"}}})
		foreignGoal := map[string]any{}
		for k, v := range f.goal {
			foreignGoal[k] = v
		}
		rev, _ := f.goal["revision"].(float64)
		foreignGoal["objective"], foreignGoal["revision"] = "External writer wins", rev+1
		foreign := goal.ToolsWriteGoal(f.cwd, jsonOf(foreignGoal))
		out := r.callTool("update_goal", map[string]any{"status": "complete"})
		matches(t, out.Content, `changed in another process`)
		disk := goal.ToolsParseGoalFile(filepath.Join(f.cwd, foreign["activePath"].(string)))
		equal(t, disk["objective"], "External writer wins", "objective")
		equal(t, tasksOf(disk)[0].(map[string]any)["subtasks"].([]any)[0].(map[string]any)["status"], "pending", "child")
		equal(t, hasEvent(f.cwd, "task_complete"), false, "no task_complete")
	})
}

func matches(t *testing.T, s, pattern string) {
	t.Helper()
	if !regexpMatch(pattern, s) {
		t.Errorf("%q does not match /%s/", s, pattern)
	}
}
