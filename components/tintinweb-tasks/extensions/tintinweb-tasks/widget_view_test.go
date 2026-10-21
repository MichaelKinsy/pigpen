package tintinweb_tasks_test

import (
	"fmt"
	"strings"
	"testing"
)

// The original registers a component widget (ctx.ui.setWidget(key, factory)): Pi renders it every frame at the
// terminal's width and does not cut it at ten rows. The port sends the same as a kit view.
func TestWidgetIsAComponentView(t *testing.T) {
	boot := func(t *testing.T, cfg obj) *rig {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, cfg)
		return startRig(t, "")
	}
	create := func(r *rig, n int) {
		for i := 1; i <= n; i++ {
			r.must("TaskCreate", obj{"subject": fmt.Sprintf("Task number %d", i), "description": "d"})
		}
	}
	t.Run("the rows are a lines view under the same key and placement", func(t *testing.T) {
		r := boot(t, obj{})
		create(r, 2)
		kind, rows, call := r.widgetView()
		eqv(t, kind, "lines")
		eqv(t, call["key"], "tasks")
		eqv(t, call["options"].(map[string]any)["placement"], "aboveEditor")
		// No string rows: the host would lay those out itself and show ten of them.
		_, hasContent := call["content"]
		_, hasLines := call["lines"]
		eqv(t, [2]bool{hasContent, hasLines}, [2]bool{false, false})
		eqv(t, len(rows), 3)
		eqv(t, strings.Contains(rows[0], "2 tasks (2 open)"), true)
		eqv(t, strings.Contains(rows[1], "#1 Task number 1"), true)
	})
	t.Run("a maxVisible above ten shows that many rows", func(t *testing.T) {
		r := boot(t, obj{"maxVisible": float64(25)})
		create(r, 15)
		_, rows, _ := r.widgetView()
		eqv(t, len(rows), 16) // the header and fifteen tasks
		for _, row := range rows {
			eqv(t, strings.Contains(row, "widget truncated"), false)
		}
	})
	t.Run("maxVisible still cuts the list and counts what is hidden", func(t *testing.T) {
		r := boot(t, obj{"maxVisible": float64(12)})
		create(r, 15)
		_, rows, _ := r.widgetView()
		eqv(t, len(rows), 14) // header, twelve tasks, the overflow line
		eqv(t, strings.Contains(rows[13], "and 3 more"), true)
	})
	t.Run("showAll lists every task past ten", func(t *testing.T) {
		r := boot(t, obj{"showAll": true})
		create(r, 12)
		_, rows, _ := r.widgetView()
		eqv(t, len(rows), 13)
	})
	t.Run("a removed list removes the widget", func(t *testing.T) {
		r := boot(t, obj{})
		create(r, 1)
		r.must("TaskUpdate", obj{"taskId": "1", "status": "deleted"})
		calls := r.widgetCalls()
		last := calls[len(calls)-1]
		_, hasView := last["view"]
		eqv(t, hasView, false)
		eqv(t, last["key"], "tasks")
	})
	t.Run("a terminal resize lays the rows out again at the new width", func(t *testing.T) {
		r := boot(t, obj{})
		r.must("TaskCreate", obj{"subject": "A subject that is far longer than a narrow terminal can hold", "description": "d"})
		_, rows, _ := r.widgetView()
		eqv(t, len(rows) == 2 && cells(rows[1]) > 30, true)
		before := len(r.widgetCalls())
		r.widthChange(30)
		deadline := 200
		for len(r.widgetCalls()) == before && deadline > 0 {
			sleepMs(10)
			deadline--
		}
		_, rows, _ = r.widgetView()
		if len(rows) != 2 {
			t.Fatalf("rows after the resize: %q", rows)
		}
		for _, row := range rows {
			eqv(t, cells(row) <= 30, true)
		}
	})
	t.Run("RPC mode sends no rows, as Pi's RPC mode drops a component widget", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRigMode(t, "", "rpc")
		create(r, 2)
		eqv(t, len(r.widgetCalls()), 0)
	})
}

func TestMaxVisibleAboveTenIsHonouredNotRefused(t *testing.T) {
	useEnv(t, "PI_TASKS", "off")
	useConfig(t, obj{"maxVisible": float64(50)})
	r := startRig(t, "")
	text, failure := r.tool("TaskCreate", obj{"subject": "Plain", "description": "d"})
	eqv(t, [2]string{text, failure}, [2]string{"Task #1 created successfully: Plain", ""})
	r.setScript(nil)
	r.runTasks()
	eqv(t, len(r.notifies()), 0)
	eqv(t, len(r.selects), 1)
}
