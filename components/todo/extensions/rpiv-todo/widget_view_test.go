package rpiv_todo_test

import (
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The original registers a component widget: Pi lays it out at the terminal width on every frame and shows
// every row it returns (todo-overlay.ts:99-110, 138-205; Pi shows only the first ten rows of a string[]
// widget, interactive-mode.ts:2321-2336). The port sends a kit view of verbatim rows, which the host draws
// uncapped, and lays the rows out again when the terminal is resized.

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func cells(s string) int { return utf8.RuneCountInString(ansiSeq.ReplaceAllString(s, "")) }

func manyTasks(n int) []map[string]any {
	var tasks []map[string]any
	for i := 1; i <= n; i++ {
		tasks = append(tasks, jtask(float64(i), "task"))
	}
	return tasks
}

func TestOverlayIsAComponentViewWidget(t *testing.T) {
	r := startRig(t, HostOptions{})
	r.setBranch(snapshot(3, jtask(1, "one"), jtask(2, "two")))
	r.Fire("session_start", nil)
	calls := r.widgetCalls()
	eq(t, len(calls), 1, "widget calls")
	if _, has := calls[0]["content"]; has {
		t.Fatalf("the overlay went out as a list of rows the host caps and wraps: %v", calls[0])
	}
	eq(t, calls[0]["key"], "rpiv-todos", "key")
	eq(t, calls[0]["options"], map[string]any{"placement": "aboveEditor"}, "options")
	rows := rowsOf(calls[0])
	if len(rows) != 4 {
		t.Fatalf("view rows = %q, want heading, two tasks and the spacer", rows)
	}
	contains(t, rows[0], "Todos (0/2)")
	eq(t, rows[3], "", "trailing spacer")
}

func TestOverlayShowsTheOriginalsFullRowBudget(t *testing.T) {
	r := startRig(t, HostOptions{})
	r.setBranch(snapshot(18, manyTasks(17)...))
	r.Fire("session_start", nil)
	rows := lastRows(t, r)
	// maxWidgetLines 12 content rows (heading, eight tasks, the summary is one of the twelve) plus the spacer.
	eq(t, len(rows), 13, "rows at the default maxWidgetLines of 12")
	contains(t, strings.Join(rows, "\n"), "more")
	r.mu.Lock()
	r.expanded = true
	r.mu.Unlock()
	r.call(map[string]any{"action": "create", "subject": "again"})
	r.fireTool("tool_execution_end", map[string]any{"toolName": "todo", "isError": false})
	rows = lastRows(t, r)
	eq(t, len(rows), 1+18+1, "expanded: heading, every task, spacer")
}

func TestMaxWidgetLinesAboveTwelveIsHonoured(t *testing.T) {
	homeWith(t, `{"maxWidgetLines":30}`)
	r := startRig(t, HostOptions{})
	r.setBranch(snapshot(26, manyTasks(25)...))
	r.Fire("session_start", nil)
	rows := lastRows(t, r)
	eq(t, len(rows), 1+25+1, "heading, 25 tasks, spacer")
	// The tool and the command run; neither refuses the configuration.
	text, _ := r.call(map[string]any{"action": "list"})
	contains(t, text, "[pending] #1 task")
	if f := r.Command("todos", ""); f != "" {
		t.Fatalf("/todos failed: %s", f)
	}
	for _, n := range r.notifies() {
		if strings.Contains(n["message"].(string), "does not support") {
			t.Fatalf("a refusal reached the user: %v", n)
		}
	}
}

func TestOverlayIsLaidOutAgainAfterAResize(t *testing.T) {
	r := startRig(t, HostOptions{})
	long := strings.Repeat("long subject ", 12)
	r.setBranch(snapshot(2, jtask(1, long)))
	r.Fire("session_start", nil)
	for _, row := range lastRows(t, r) {
		if cells(row) > 80 {
			t.Fatalf("row of %d cells at width 80: %q", cells(row), row)
		}
	}
	r.widthChange(30)
	calls := r.waitWidgetCalls(2)
	rows := rowsOf(calls[len(calls)-1])
	if len(rows) != 3 {
		t.Fatalf("rows after the resize = %q", rows)
	}
	for _, row := range rows {
		if cells(row) > 30 {
			t.Fatalf("row of %d cells at width 30: %q", cells(row), row)
		}
	}
	contains(t, rows[1], "…")
	r.widthChange(200)
	calls = r.waitWidgetCalls(3)
	rows = rowsOf(calls[len(calls)-1])
	if strings.Contains(strings.Join(rows, ""), "…") {
		t.Fatalf("rows still truncated at width 200: %q", rows)
	}
}

// A resize is delivered on its own goroutine, so "nothing was pushed" is checked after a short settle.
func settled() { time.Sleep(150 * time.Millisecond) }

func TestResizeWithoutAnOverlayPushesNothing(t *testing.T) {
	r := startRig(t, HostOptions{})
	r.Fire("session_start", nil)
	r.widthChange(30)
	r.widthChange(90)
	settled()
	eq(t, len(r.widgetCalls()), 0, "widget calls")
}

func TestResizeAfterShutdownPushesNothing(t *testing.T) {
	r := startRig(t, HostOptions{})
	r.setBranch(snapshot(2, jtask(1, "one")))
	r.Fire("session_start", nil)
	r.Fire("session_shutdown", nil)
	n := len(r.widgetCalls())
	r.widthChange(30)
	settled()
	eq(t, len(r.widgetCalls()), n, "widget calls after the shutdown")
}

// A session that starts again after a shutdown subscribes once: one resize is one push.
func TestRestartedSessionIsSubscribedOnce(t *testing.T) {
	r := startRig(t, HostOptions{})
	r.setBranch(snapshot(2, jtask(1, "one")))
	r.Fire("session_start", nil)
	r.Fire("session_shutdown", nil)
	r.setSession("next")
	r.Fire("session_start", nil)
	n := len(r.widgetCalls())
	r.widthChange(33)
	r.waitWidgetCalls(n + 1)
	settled()
	eq(t, len(r.widgetCalls()), n+1, "widget calls after one resize")
}
