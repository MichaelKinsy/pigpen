package pi_goal_x

import (
	"fmt"
	"regexp"
	"strings"
)

// ported from goal-task-derive.ts

var (
	deriveChecklistRE = regexp.MustCompile(`^(?:-` + jsS + `*)?\[` + jsS + `\]` + jsS + `+(` + jsDot + `+)$`)
	deriveNumberedRE  = regexp.MustCompile(`^(\d{1,2})` + jsS + `*[.):]` + jsS + `+(` + jsDot + `+)$`)
	deriveLineSplit   = regexp.MustCompile(`\r?\n`)
)

func derivedTask(n int, title string) *jsObject {
	t := newObject()
	t.set("id", fmt.Sprintf("step-%d", n))
	t.set("title", title)
	t.set("status", "pending")
	return t
}

// deriveTasksFromObjective returns nil (JS null) when the objective has no usable structure.
func deriveTasksFromObjective(objective string) []any {
	var tasks []any
	sawChecklist := false
	for _, raw := range deriveLineSplit.Split(objective, -1) {
		line := jsTrim(raw)
		if line == "" {
			continue
		}
		if m := deriveChecklistRE.FindStringSubmatch(line); m != nil {
			sawChecklist = true
			tasks = append(tasks, derivedTask(len(tasks)+1, jsTrim(m[1])))
			continue
		}
		if m := deriveNumberedRE.FindStringSubmatch(line); m != nil {
			tasks = append(tasks, derivedTask(len(tasks)+1, jsTrim(m[2])))
		}
	}
	if len(tasks) == 0 {
		return nil
	}
	if !sawChecklist && len(tasks) < 2 {
		return nil
	}
	return tasks
}

// derivedTaskCount is the number of derived tasks (0 when none).
func derivedTaskCount(objective string) int { return len(deriveTasksFromObjective(objective)) }

var _ = strings.TrimSpace
