package tintinweb_tasks

import (
	"math"
	"slices"
	"strconv"
)

// Task ordering for the widget. upstream: task-sort.ts. A sort order is a preset name or a sort spec (an
// ordered list of comparison keys); specs are pure data, since `.pi/` lives inside cloned repositories.

type sortKey struct {
	field     string // id | status | updatedAt
	direction string // "" or asc, desc
	rank      []string
}

var (
	sortFields   = []string{"id", "status", "updatedAt"}
	taskStatuses = []string{statusPending, statusInProgress, statusCompleted}
	// defaultStatusRank is also used when a status key omits rank. upstream: task-sort.ts:33.
	defaultStatusRank = []string{statusCompleted, statusInProgress, statusPending}
	// sortPresets are the built-in presets as specs. upstream: task-sort.ts:37-43.
	sortPresets = map[string][]sortKey{
		"id":     {{field: "id"}},
		"status": {{field: "status", rank: defaultStatusRank}, {field: "id"}},
		"active": {{field: "status", rank: []string{statusInProgress, statusPending, statusCompleted}}, {field: "id"}},
		"recent": {{field: "updatedAt", direction: "desc"}, {field: "id", direction: "desc"}},
		"oldest": {{field: "updatedAt"}, {field: "id"}},
	}
	// builtInSortOrders lists the preset names, in the original's order. upstream: task-sort.ts:45.
	builtInSortOrders = []string{"id", "status", "active", "recent", "oldest"}
)

// parseSortKey reads one key of a spec, or reports it invalid. upstream: task-sort.ts:47-54 (isSortKey).
func parseSortKey(v any) (sortKey, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return sortKey{}, false
	}
	field, _ := m["field"].(string)
	if !slices.Contains(sortFields, field) {
		return sortKey{}, false
	}
	k := sortKey{field: field}
	if d, present := m["direction"]; present && d != nil {
		s, _ := d.(string)
		if s != "asc" && s != "desc" {
			return sortKey{}, false
		}
		k.direction = s
	}
	if r, present := m["rank"]; present && r != nil {
		list, isList := r.([]any)
		if !isList {
			return sortKey{}, false
		}
		for _, e := range list {
			s, _ := e.(string)
			if !slices.Contains(taskStatuses, s) {
				return sortKey{}, false
			}
			k.rank = append(k.rank, s)
		}
		if k.rank == nil {
			k.rank = []string{}
		}
	}
	return k, true
}

// toSpec resolves a configured order to a spec. It never fails: a hand-edited config must not break the
// widget. upstream: task-sort.ts:57-66.
func toSpec(order any) []sortKey {
	switch o := order.(type) {
	case string:
		if spec, ok := sortPresets[o]; ok {
			return spec
		}
	case []any:
		if len(o) > 0 {
			spec := make([]sortKey, 0, len(o))
			for _, e := range o {
				k, ok := parseSortKey(e)
				if !ok {
					return sortPresets["id"]
				}
				spec = append(spec, k)
			}
			return spec
		}
	}
	return sortPresets["id"]
}

func statusIndex(status string, rank []string) int {
	if i := slices.Index(rank, status); i >= 0 {
		return i
	}
	return len(rank)
}

// jsNumber is Number(s): the numeric value of a string, NaN when it is not one.
func jsNumber(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

func compareKey(a, b *task, k sortKey) float64 {
	switch k.field {
	case "status":
		rank := k.rank
		if rank == nil {
			rank = defaultStatusRank
		}
		return float64(statusIndex(a.Status, rank) - statusIndex(b.Status, rank))
	case "id":
		return jsNumber(a.ID) - jsNumber(b.ID)
	}
	return float64(a.UpdatedAt - b.UpdatedAt)
}

// sortBy returns a sorted copy of items by a configured order. upstream: task-sort.ts:88-98.
func sortBy[T any](items []T, get func(T) *task, order any) []T {
	spec := toSpec(order)
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(x, y T) int {
		a, b := get(x), get(y)
		for _, k := range spec {
			delta := compareKey(a, b, k)
			if math.IsNaN(delta) {
				return 0 // Array.prototype.sort treats a NaN comparison as equal
			}
			if delta != 0 {
				if k.direction == "desc" {
					delta = -delta
				}
				if delta < 0 {
					return -1
				}
				return 1
			}
		}
		return 0
	})
	return out
}

// sortTasks returns a sorted copy, leaving the input untouched.
func sortTasks(tasks []task, order any) []task {
	return sortBy(tasks, func(t task) *task { return &t }, order)
}
