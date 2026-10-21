package pi_goal_x_test

import (
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

// get_goal's detail sections go through the shared page cache: a repeated read of tasks, objective or history compiles nothing
// new (the history keyed by the ledger's revision, as goal-core-tools.ts passes history?.revision).
func TestGetGoalDetailSectionsAreCached(t *testing.T) {
	goal.ResetDetailCache()
	t.Cleanup(goal.ResetDetailCache)
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	f := newToolsFixture(t, "Cache goal", false, goalAt(2026, time.August, 4, 11, 0, 0),
		`{"taskList":{"tasks":[{"id":"t1","title":"Task one","status":"pending"}],"blockCompletion":false,"proposedAt":"`+now+`"}}`)
	r := newToolsRig(t, f, rigOptions{})
	r.startToolsSession()
	for _, section := range []string{"tasks", "objective", "history"} {
		before := goal.DetailCacheLen()
		first := r.callTool("get_goal", map[string]any{"section": section}).Content
		if goal.DetailCacheLen() != before+1 {
			t.Fatalf("%s: first read cached %d entries, want 1", section, goal.DetailCacheLen()-before)
		}
		if again := r.callTool("get_goal", map[string]any{"section": section}).Content; again != first || goal.DetailCacheLen() != before+1 {
			t.Errorf("%s: a repeated read must reuse the entry and return the same page", section)
		}
	}
}
