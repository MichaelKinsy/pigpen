package pi_goal_x

import (
	"os"
	"strings"
	"testing"
)

// Faithfulness-review regressions (not upstream twins): they assert properties of the Go source the same way the existing
// source-text twins do (goal_update_objective_test.go).

func rvToolsSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// set_goal_tasks' task items are a bare Type.Object in the original: no additionalProperties:false on the items.
func TestRvToolsSetGoalTasksItemsAllowExtraProperties(t *testing.T) {
	src := rvToolsSource(t, "goal_task_tools.go")
	start := strings.Index(src, `"tasks": schemaMap{"type": "array", "description": "Flat parent-linked task list"`)
	if start < 0 {
		t.Fatal("must find the set_goal_tasks tasks schema")
	}
	block := src[start:]
	end := strings.Index(block, `"block_completion"`)
	if end < 0 {
		t.Fatal("must find block_completion after the tasks schema")
	}
	block = block[:end]
	if strings.Contains(block, "schemaObject(") || strings.Contains(block, "additionalProperties") {
		t.Fatal("set_goal_tasks task items must not forbid additional properties")
	}
}

// update_goal's blocked and paused outcomes call core.clearContinuationState() with its default (resetNetworkErrorBackoff = true).
func TestRvToolsUpdateGoalStopResetsNetworkErrorBackoff(t *testing.T) {
	src := rvToolsSource(t, "goal_core_tools.go")
	if strings.Contains(src, "c.clearContinuationState(false)") {
		t.Fatal("update_goal blocked/paused must reset the network-error backoff: clearContinuationState(true)")
	}
	if got := strings.Count(src, "c.clearContinuationState(true)"); got != 2 {
		t.Fatalf("expected the blocked and paused flows to call clearContinuationState(true), found %d", got)
	}
}
