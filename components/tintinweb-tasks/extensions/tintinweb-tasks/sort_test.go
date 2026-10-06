package tintinweb_tasks

import (
	"slices"
	"strconv"
	"testing"
)

// mk builds a task as the original's test helper does. upstream: test/task-sort.test.ts:5-17.
func mk(id, status string, updatedAt int64) task {
	n, _ := strconv.Atoi(id)
	return task{ID: id, Subject: "Task " + id, Description: "Desc", Status: status, Metadata: map[string]any{},
		Blocks: []string{}, BlockedBy: []string{}, CreatedAt: int64(n), UpdatedAt: updatedAt}
}

func num(id string) int { n, _ := strconv.Atoi(id); return n }

// sortSample is every status/timestamp combination, with ties on updatedAt. upstream: task-sort.test.ts:33-40.
func sortSample() []task {
	return []task{mk("1", "pending", 30), mk("2", "completed", 10), mk("3", "in_progress", 30),
		mk("4", "completed", 20), mk("5", "pending", 10), mk("6", "in_progress", 20)}
}

// originalComparators are the comparators the module replaced, the equivalence oracle. upstream: task-sort.test.ts:20-27.
var originalComparators = map[string]func(a, b task) int{
	"id": func(a, b task) int { return num(a.ID) - num(b.ID) },
	"status": func(a, b task) int {
		rank := func(s string) int {
			switch s {
			case "completed":
				return 0
			case "in_progress":
				return 1
			}
			return 2
		}
		if d := rank(a.Status) - rank(b.Status); d != 0 {
			return d
		}
		return num(a.ID) - num(b.ID)
	},
	"recent": func(a, b task) int {
		if d := b.UpdatedAt - a.UpdatedAt; d != 0 {
			return int(d)
		}
		return num(b.ID) - num(a.ID)
	},
	"oldest": func(a, b task) int {
		if d := a.UpdatedAt - b.UpdatedAt; d != 0 {
			return int(d)
		}
		return num(a.ID) - num(b.ID)
	},
}

func TestTaskSort(t *testing.T) {
	const f = "task-sort"
	t.Run("presets match the original comparators (it.each: not in the title ledger)", func(t *testing.T) {
		for _, order := range []string{"id", "status", "recent", "oldest"} {
			want := sortSample()
			slices.SortStableFunc(want, originalComparators[order])
			eq(t, ids(sortTasks(sortSample(), order)), ids(want))
		}
	})
	tw(t, f, "'status' keeps completed first with ids ascending inside each group", func(t *testing.T) {
		eq(t, ids(sortTasks(sortSample(), "status")), []string{"2", "4", "3", "6", "1", "5"})
	})
	tw(t, f, "'active' puts in-progress first, then pending, then completed", func(t *testing.T) {
		eq(t, ids(sortTasks(sortSample(), "active")), []string{"3", "6", "1", "5", "2", "4"})
	})
	tw(t, f, "'recent' breaks updatedAt ties by descending id", func(t *testing.T) {
		eq(t, ids(sortTasks(sortSample(), "recent")), []string{"3", "1", "6", "4", "5", "2"})
	})
	tw(t, f, "defaults to id order", func(t *testing.T) {
		eq(t, ids(sortTasks(sortSample(), nil)), []string{"1", "2", "3", "4", "5", "6"})
	})
	tw(t, f, "returns a copy without mutating the input", func(t *testing.T) {
		input := []task{mk("3", "pending", 3), mk("1", "pending", 1), mk("2", "pending", 2)}
		eq(t, ids(sortTasks(input, "id")), []string{"1", "2", "3"})
		eq(t, ids(input), []string{"3", "1", "2"})
	})
	tw(t, f, "applies a custom status rank with an id tie-break", func(t *testing.T) {
		spec := arr{obj{"field": "status", "rank": arr{"in_progress", "pending", "completed"}}, obj{"field": "id"}}
		eq(t, ids(sortTasks(sortSample(), spec)), []string{"3", "6", "1", "5", "2", "4"})
	})
	tw(t, f, "sorts statuses left out of the rank last, tied among themselves", func(t *testing.T) {
		spec := arr{obj{"field": "status", "rank": arr{"pending"}}, obj{"field": "id"}}
		// pending first, then the unranked completed/in_progress in id order.
		eq(t, ids(sortTasks(sortSample(), spec)), []string{"1", "5", "2", "3", "4", "6"})
	})
	tw(t, f, "reverses a single key with direction 'desc'", func(t *testing.T) {
		eq(t, ids(sortTasks(sortSample(), arr{obj{"field": "id", "direction": "desc"}})), []string{"6", "5", "4", "3", "2", "1"})
	})
	tw(t, f, "reverses the status rank when the status key is descending", func(t *testing.T) {
		spec := arr{obj{"field": "status", "rank": arr{"completed", "in_progress", "pending"}, "direction": "desc"}, obj{"field": "id"}}
		eq(t, ids(sortTasks(sortSample(), spec)), []string{"1", "5", "3", "6", "2", "4"})
	})
	tw(t, f, "falls through to later keys only on a tie", func(t *testing.T) {
		spec := arr{obj{"field": "updatedAt"}, obj{"field": "id", "direction": "desc"}}
		eq(t, ids(sortTasks(sortSample(), spec)), []string{"5", "2", "6", "4", "3", "1"})
	})
	tw(t, f, "defaults an omitted rank to the 'status' preset order", func(t *testing.T) {
		eq(t, ids(sortTasks(sortSample(), arr{obj{"field": "status"}, obj{"field": "id"}})), ids(sortTasks(sortSample(), "status")))
	})
	t.Run("malformed orders fall back to id order (it.each: not in the title ledger)", func(t *testing.T) {
		byID := ids(sortTasks(sortSample(), "id"))
		for name, order := range map[string]any{
			"an unknown preset name":    "newest",
			"a non-array object":        obj{"by": arr{obj{"field": "id"}}},
			"an empty spec":             arr{},
			"an unknown field":          arr{obj{"field": "subject"}},
			"a bad direction":           arr{obj{"field": "id", "direction": "ascending"}},
			"a non-array rank":          arr{obj{"field": "status", "rank": "completed"}},
			"an invalid rank entry":     arr{obj{"field": "status", "rank": arr{"completed", "done"}}},
			"a non-object key":          arr{arr{"id"}},
			"a prototype property name": "toString",
			"null":                      nil,
		} {
			t.Run(name, func(t *testing.T) { eq(t, ids(sortTasks(sortSample(), order)), byID) })
		}
	})
	tw(t, f, "rejects a spec if any key is invalid", func(t *testing.T) {
		byID := ids(sortTasks(sortSample(), "id"))
		eq(t, ids(sortTasks(sortSample(), arr{obj{"field": "status"}, obj{"field": "nope"}})), byID)
	})
}
