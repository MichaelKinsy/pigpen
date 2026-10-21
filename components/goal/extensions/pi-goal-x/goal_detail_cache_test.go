package pi_goal_x

import (
	"strconv"
	"strings"
	"testing"
)

// The shared detail page cache of goal-detail.ts (detailCache): inputs compared with ===, the ledger revision and the task index
// taken by identity, at most 16 entries and 16,000,000 UTF-16 units, histories without a revision never cached.

func resetGoalDetailCache(t *testing.T) {
	t.Helper()
	goalDetailCache, goalDetailCachedUnit = nil, 0
	t.Cleanup(func() { goalDetailCache, goalDetailCachedUnit = nil, 0 })
}

func detailGoal(t *testing.T, objective string) *jsObject {
	t.Helper()
	g := jo(t, `{"id":"g1","objective":"x","currentTaskId":"a","taskList":{"tasks":[{"id":"a","title":"A","status":"pending"},{"id":"b","title":"B","status":"pending","subtasks":[{"id":"c","title":"C","status":"pending"}]}],"blockCompletion":true,"proposedAt":"p"}}`)
	g.set("objective", objective)
	return g
}

func TestGoalDetailCacheObjective(t *testing.T) {
	resetGoalDetailCache(t)
	g := detailGoal(t, "obj")
	q := goalDetailQuery{section: "objective"}
	first := goalDetailCompile(g, q, nil, nil)
	if first == nil || len(goalDetailCache) != 1 || goalDetailCachedUnit != len(first.units) {
		t.Fatalf("not cached: %d entries, %d units", len(goalDetailCache), goalDetailCachedUnit)
	}
	if goalDetailCompile(g, q, nil, nil) != first || len(goalDetailCache) != 1 {
		t.Error("equal inputs must reuse the entry")
	}
	g.set("objective", "obj2")
	second := goalDetailCompile(g, q, nil, nil)
	if second == first || second.source != "obj2" || len(goalDetailCache) != 2 {
		t.Errorf("a changed objective must compile again (%q, %d entries)", second.source, len(goalDetailCache))
	}
	g.set("verificationContract", "vc")
	third := goalDetailCompile(g, q, nil, nil)
	if third == second || !strings.Contains(third.source, "Verification contract:\nvc") {
		t.Error("a changed verification contract must compile again")
	}
	other := cloneGoal(g)
	other.set("id", "g2")
	if goalDetailCompile(other, q, nil, nil) == third || third.key == goalDetailCompile(other, q, nil, nil).key {
		t.Error("another goal id must not share an entry or a key")
	}
	g.set("verificationContract", "vc")
	if goalDetailCompile(g, q, nil, nil) != third {
		t.Error("restoring the same values must find the earlier entry")
	}
	if p := goalDetailPage(g, q, nil, nil); !p.ok || !strings.HasPrefix(p.text, "objective for g1 (0–") {
		t.Errorf("page %q", p.text)
	}
}

func TestGoalDetailCacheTasks(t *testing.T) {
	resetGoalDetailCache(t)
	g := detailGoal(t, "x")
	q := goalDetailQuery{section: "tasks"}
	first := goalDetailCompile(g, q, nil, nil)
	if goalDetailCompile(g, q, nil, nil) != first || len(goalDetailCache) != 1 {
		t.Error("the same task list and current task must reuse the entry")
	}
	if !strings.Contains(first.source, `"current":true`) {
		t.Error("the current task is marked")
	}
	g.set("currentTaskId", "b")
	moved := goalDetailCompile(g, q, nil, nil)
	if moved == first || len(goalDetailCache) != 2 || strings.Count(moved.source, `"current":true`) != 1 {
		t.Errorf("a changed current task must compile again: %q", moved.source)
	}
	// an in-place edit of a task changes the task index, so the entry cannot be reused
	tasks := g.obj("taskList").vals["tasks"].([]any)
	tasks[0].(*jsObject).set("title", "A edited")
	edited := goalDetailCompile(g, q, nil, nil)
	if edited == moved || !strings.Contains(edited.source, "A edited") {
		t.Error("an in-place task edit must be seen")
	}
	// a usage-only change reuses the index and the entry
	tasks[0].(*jsObject).set("unindexed", 1.0)
	if goalDetailCompile(g, q, nil, nil) != edited {
		t.Error("a field outside the index must not invalidate the entry")
	}
	// task_id: absent, "" and a real id are different inputs; an unknown id is not cached
	empty := ""
	withEmpty := goalDetailCompile(g, goalDetailQuery{section: "tasks", taskId: &empty}, nil, nil)
	if withEmpty == edited || withEmpty.source != edited.source || withEmpty.key == edited.key {
		t.Error("task_id \"\" is its own entry with its own key")
	}
	n := len(goalDetailCache)
	missing := "zzz"
	if goalDetailCompile(g, goalDetailQuery{section: "tasks", taskId: &missing}, nil, nil) != nil || len(goalDetailCache) != n {
		t.Error("an unknown task id must not be cached")
	}
	one := "c"
	sel := goalDetailCompile(g, goalDetailQuery{section: "tasks", taskId: &one}, nil, nil)
	if sel == nil || strings.Contains(sel.source, "\n") || goalDetailCompile(g, goalDetailQuery{section: "tasks", taskId: &one}, nil, nil) != sel {
		t.Error("a selected task is cached by id")
	}
	// the goal with no task list
	none := jo(t, `{"id":"g9","objective":"o"}`)
	if r := goalDetailCompile(none, q, nil, nil); r == nil || r.source != "" {
		t.Error("no tasks: empty source")
	}
}

func TestGoalDetailCacheHistory(t *testing.T) {
	resetGoalDetailCache(t)
	g := detailGoal(t, "x")
	q := goalDetailQuery{section: "history"}
	ev := func(ids ...string) []*jsObject {
		var out []*jsObject
		for _, id := range ids {
			out = append(out, ledgerEvent("task_complete", "goalId", "g1", "taskId", id, "at", "2026-09-07"))
		}
		return out
	}
	// without a revision the caller's events are content checked and nothing is cached
	a := goalDetailCompile(g, q, ev("t1"), nil)
	b := goalDetailCompile(g, q, ev("t1", "t2"), nil)
	if len(goalDetailCache) != 0 || a.source == b.source || !strings.Contains(b.source, "t2") {
		t.Errorf("no revision: %d entries", len(goalDetailCache))
	}
	// with a revision the generation stands for the content: a second call with the same revision reuses the entry
	r1, r2 := &ledgerRevision{}, &ledgerRevision{}
	c := goalDetailCompile(g, q, ev("t1"), r1)
	if len(goalDetailCache) != 1 || goalDetailCompile(g, q, ev("t1", "t2"), r1) != c {
		t.Error("the same revision reuses the entry whatever events the caller passes")
	}
	d := goalDetailCompile(g, q, ev("t1", "t2"), r2)
	if d == c || !strings.Contains(d.source, "t2") || len(goalDetailCache) != 2 {
		t.Error("another revision compiles again")
	}
	// events of other goals are not part of the page
	mixed := append(ev("t1"), ledgerEvent("task_complete", "goalId", "other", "taskId", "zz", "at", "x"), ledgerEvent("note", "at", "x"))
	if m := goalDetailCompile(g, q, mixed, &ledgerRevision{}); strings.Contains(m.source, "zz") || strings.Contains(m.source, "note") {
		t.Error("only this goal's events")
	}
	// paging a cached history is stable across calls
	var long []*jsObject
	for i := range 300 {
		long = append(long, ledgerEvent("task_complete", "goalId", "g1", "taskId", "t"+strconv.Itoa(i), "evidence", strings.Repeat("🧭", 50), "at", "x"))
	}
	r3 := &ledgerRevision{}
	first := goalDetailPage(g, q, long, r3)
	if !first.ok || !first.hasNext {
		t.Fatal("first page")
	}
	n := len(goalDetailCache)
	second := goalDetailPage(g, goalDetailQuery{section: "history", cursor: first.nextCursor}, long, r3)
	if !second.ok || len(goalDetailCache) != n {
		t.Error("the second page must reuse the compiled history")
	}
	if p := goalDetailPage(g, goalDetailQuery{section: "history", cursor: first.nextCursor}, long, &ledgerRevision{}); !p.ok {
		t.Error("a new revision with the same content keeps the cursor valid (the key is the content hash)")
	}
}

func TestGoalDetailCacheBounds(t *testing.T) {
	resetGoalDetailCache(t)
	q := goalDetailQuery{section: "objective"}
	g := detailGoal(t, "")
	var first *goalDetailCompiled
	for i := range 17 {
		g.set("objective", "o"+strconv.Itoa(i))
		r := goalDetailCompile(g, q, nil, nil)
		if i == 0 {
			first = r
		}
		if want := min(i+1, 16); len(goalDetailCache) != want {
			t.Fatalf("after %d: %d entries, want %d", i+1, len(goalDetailCache), want)
		}
	}
	g.set("objective", "o0")
	if again := goalDetailCompile(g, q, nil, nil); again == first {
		t.Error("the oldest entry must have been evicted at 16 entries")
	}
	if len(goalDetailCache) != 16 || goalDetailCache[len(goalDetailCache)-1].result.source != "o0" {
		t.Error("the newest entry is last")
	}

	// the character budget: two 9,000,000-unit sources do not fit together
	resetGoalDetailCache(t)
	big := func(c string) *jsObject { return detailGoal(t, strings.Repeat(c, 9_000_000)) }
	a := goalDetailCompile(big("a"), q, nil, nil)
	if len(goalDetailCache) != 1 || goalDetailCachedUnit != len(a.units) {
		t.Fatalf("a: %d entries %d units", len(goalDetailCache), goalDetailCachedUnit)
	}
	b := goalDetailCompile(big("b"), q, nil, nil)
	if len(goalDetailCache) != 1 || goalDetailCachedUnit != len(b.units) || goalDetailCache[0].result != b {
		t.Errorf("the older entry must be evicted by the budget: %d entries %d units", len(goalDetailCache), goalDetailCachedUnit)
	}
	// a source beyond the budget is returned in full and never cached
	huge := goalDetailCompile(detailGoal(t, strings.Repeat("h", 16_000_001)), q, nil, nil)
	if huge == nil || len(huge.units) != 16_000_001 || len(goalDetailCache) != 1 || goalDetailCache[0].result != b {
		t.Error("an oversized source is not cached and does not evict")
	}
}

// Astral characters count two units, as the original's string length does.
func TestGoalDetailCacheUnitsAreUTF16(t *testing.T) {
	resetGoalDetailCache(t)
	g := detailGoal(t, "🧪🧪")
	if r := goalDetailCompile(g, goalDetailQuery{section: "objective"}, nil, nil); len(r.units) != 4 || goalDetailCachedUnit != 4 {
		t.Errorf("units %d, accounted %d", len(r.units), goalDetailCachedUnit)
	}
}
