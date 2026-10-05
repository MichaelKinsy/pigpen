package rpiv_todo_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fCommand   = "todo.command"
	fIsolation = "todo.session-isolation"
	fShortcut  = "todo-overlay.shortcut"
	fInval     = "todo.invalidation"
	fRegister  = "todo.register"
)

func boolp(b bool) *bool { return &b }

// homeWith points HOME at a temp dir holding the todo config (upstream: ~/.config/rpiv-todo/config.json).
func homeWith(t *testing.T, config string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if config == "" {
		return
	}
	dir := filepath.Join(home, ".config", "rpiv-todo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
}

func snapshot(nextID float64, tasks ...map[string]any) []map[string]any {
	ts := []any{}
	for _, t := range tasks {
		ts = append(ts, t)
	}
	return []map[string]any{
		{"type": "message", "message": map[string]any{"role": "user", "content": "hi"}},
		{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "todo", "details": map[string]any{"action": "create", "params": map[string]any{}, "tasks": ts, "nextId": nextID}}},
	}
}

func jtask(id float64, subject string) map[string]any {
	return map[string]any{"id": id, "subject": subject, "status": "pending"}
}

func hasRow(rows []string, sub string) bool { return strings.Contains(strings.Join(rows, "\n"), sub) }

func lastRows(t *testing.T, r *rig) []string {
	t.Helper()
	calls := r.widgetCalls()
	if len(calls) == 0 {
		t.Fatal("no widget call")
	}
	return rowsOf(at(calls, len(calls)-1))
}

func TestCommand(t *testing.T) {
	tw(t, fCommand, "registers a command named 'todos' with a description", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		if f := r.Command("todos", ""); f != "" {
			t.Fatalf("/todos failed: %s", f)
		}
	})
	tw(t, fCommand, "notifies an error when the session has no UI", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "print"})
		r.Command("todos", "")
		n := r.notifies()
		eq(t, len(n), 1, "notifies")
		eq(t, at(n, 0), map[string]any{"message": "/todos requires interactive mode", "level": "error"}, "notify")
	})
	tw(t, fCommand, "notifies an info message when there are no visible tasks", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.Command("todos", "")
		eq(t, at(r.notifies(), 0), map[string]any{"message": "No todos yet. Ask the agent to add some!", "level": "info"}, "notify")
	})
	tw(t, fCommand, "treats all-deleted tasks as empty (info notify, not group render)", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.call(map[string]any{"action": "create", "subject": "gone"})
		r.call(map[string]any{"action": "delete", "id": 1.0})
		r.Command("todos", "")
		eq(t, at(r.notifies(), 0)["level"], "info", "level")
		eq(t, at(r.notifies(), 0)["message"], "No todos yet. Ask the agent to add some!", "message")
	})
	grouped := func(t *testing.T) string {
		r := startRig(t, HostOptions{})
		r.call(map[string]any{"action": "create", "subject": "alpha"})
		r.call(map[string]any{"action": "create", "subject": "beta", "blockedBy": []any{1.0}})
		r.call(map[string]any{"action": "create", "subject": "gamma", "activeForm": "gamma-ing"})
		r.call(map[string]any{"action": "create", "subject": "delta"})
		r.call(map[string]any{"action": "create", "subject": "epsilon"})
		r.call(map[string]any{"action": "update", "id": 3.0, "status": "in_progress"})
		r.call(map[string]any{"action": "update", "id": 4.0, "status": "completed"})
		r.call(map[string]any{"action": "delete", "id": 5.0})
		r.Command("todos", "")
		return at(r.notifies(), 0)["message"].(string)
	}
	tw(t, fCommand, "renders 'Pending' group with ○ glyph and task id", func(t *testing.T) {
		out := grouped(t)
		contains(t, out, "── Pending ──\n  ○ #1 alpha")
	})
	tw(t, fCommand, "renders 'In Progress' group with ◐ glyph and activeForm suffix", func(t *testing.T) {
		contains(t, grouped(t), "── In Progress ──\n  ◐ #3 gamma (gamma-ing)")
	})
	tw(t, fCommand, "renders 'Completed' group with ✓ glyph and 'N/M completed' header", func(t *testing.T) {
		out := grouped(t)
		contains(t, out, "── Completed ──\n  ✓ #4 delta")
		contains(t, out, "1/4 completed")
	})
	tw(t, fCommand, "emits the header parts in 'completed · in progress · pending' order", func(t *testing.T) {
		eq(t, strings.SplitN(grouped(t), "\n", 2)[0], "1/4 completed · 1 in progress · 2 pending", "header")
	})
	tw(t, fCommand, "appends '⛓ #deps' suffix for tasks with blockedBy", func(t *testing.T) {
		contains(t, grouped(t), "  ○ #2 beta    ⛓ #1")
	})
	tw(t, fCommand, "omits deleted tombstones from the grouped output", func(t *testing.T) {
		notContains(t, grouped(t), "epsilon")
	})
}

func contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q does not contain %q", s, sub)
	}
}

func notContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("%q contains %q", s, sub)
	}
}

func TestRegisteredTool(t *testing.T) {
	tw(t, fRegister, "create → list returns the seeded row", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.call(map[string]any{"action": "create", "subject": "seeded"})
		text, details := r.call(map[string]any{"action": "list"})
		eq(t, text, "[pending] #1 seeded", "list text")
		eq(t, details["nextId"], 2.0, "nextId")
	})
	tw(t, fRegister, "clear resets module state and nextId", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.call(map[string]any{"action": "create", "subject": "a"})
		text, details := r.call(map[string]any{"action": "clear"})
		eq(t, text, "Cleared 1 tasks", "text")
		eq(t, details["nextId"], 1.0, "nextId")
		text, _ = r.call(map[string]any{"action": "list"})
		eq(t, text, "No tasks", "list after clear")
	})
}

func TestSessionIsolation(t *testing.T) {
	start := func(t *testing.T, opts HostOptions) *rig {
		r := startRig(t, opts)
		r.setSession("parent")
		return r
	}
	tw(t, fIsolation, "a child session_start (empty branch) leaves the parent's committed task intact", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.call(map[string]any{"action": "create", "subject": "parent task"})
		r.setSession("child")
		r.Fire("session_start", nil)
		r.setSession("parent")
		r.Command("todos", "")
		contains(t, at(r.notifies(), 0)["message"].(string), "parent task")
	})
	tw(t, fIsolation, "a child todo call mutates only the child's slot; the parent's /todos still shows only the parent's task", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.call(map[string]any{"action": "create", "subject": "parent task"})
		r.setSession("child")
		r.call(map[string]any{"action": "create", "subject": "child task"})
		r.setSession("parent")
		r.Command("todos", "")
		out := at(r.notifies(), 0)["message"].(string)
		contains(t, out, "parent task")
		notContains(t, out, "child task")
	})
	tw(t, fIsolation, "the render pointer stays on the parent slot even after a child creates tasks (creator-ownership)", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "parent task")))
		r.Fire("session_start", nil)
		r.setSession("child")
		r.call(map[string]any{"action": "create", "subject": "child task"})
		r.fireTool("tool_execution_end", map[string]any{"toolName": "todo", "isError": false})
		rows := lastRows(t, r)
		if !hasRow(rows, "parent task") || hasRow(rows, "child task") {
			t.Fatalf("overlay rows = %q, want the parent's task only", rows)
		}
	})
	tw(t, fIsolation, "session_shutdown evicts the shutting-down session's own slot (fresh EMPTY_STATE copy)", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.call(map[string]any{"action": "create", "subject": "mine"})
		r.Fire("session_shutdown", nil)
		text, _ := r.call(map[string]any{"action": "list"})
		eq(t, text, "No tasks", "list after shutdown")
	})
	tw(t, fIsolation, "first hasUI session_start claims the foreground and renders its slot", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "first")))
		r.Fire("session_start", nil)
		contains(t, strings.Join(lastRows(t, r), "\n"), "first")
	})
	tw(t, fIsolation, "a child session_start (distinct sid, hasUI) does not claim foreground or rebind the overlay", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "parent task")))
		r.Fire("session_start", nil)
		n := len(r.widgetCalls())
		r.setSession("child")
		r.setBranch(snapshot(2, jtask(1, "child task")))
		r.Fire("session_start", nil)
		eq(t, len(r.widgetCalls()), n, "widget calls after the child's session_start")
	})
	tw(t, fIsolation, "a child todo call writes the child's slot; the overlay still shows the parent's todos", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "parent task")))
		r.Fire("session_start", nil)
		r.setSession("child")
		r.call(map[string]any{"action": "create", "subject": "child task"})
		r.fireTool("tool_execution_end", map[string]any{"toolName": "todo", "isError": false})
		text, _ := r.call(map[string]any{"action": "list"})
		eq(t, text, "[pending] #1 child task", "the child's own list")
		contains(t, strings.Join(lastRows(t, r), "\n"), "parent task")
	})
	tw(t, fIsolation, "a child session_shutdown does not dispose the foreground overlay", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "parent task")))
		r.Fire("session_start", nil)
		n := len(r.widgetCalls())
		r.setSession("child")
		r.Fire("session_shutdown", nil)
		eq(t, len(r.widgetCalls()), n, "widget calls after the child's shutdown")
	})
	tw(t, fIsolation, "the foreground's own session_shutdown disposes the overlay and clears foreground", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "parent task")))
		r.Fire("session_start", nil)
		r.Fire("session_shutdown", nil)
		calls := r.widgetCalls()
		last := at(calls, len(calls)-1)
		eq(t, last["key"], "rpiv-todos", "key")
		eq(t, len(rowsOf(last)), 0, "a clear carries no rows")
		// The pointer is clear: the next UI session claims the foreground.
		r.setSession("next")
		r.setBranch(snapshot(2, jtask(1, "next task")))
		r.Fire("session_start", nil)
		contains(t, strings.Join(lastRows(t, r), "\n"), "next task")
	})
	tw(t, fIsolation, "foreground shutdown still clears the pointer + evicts the slot when dispose() throws (try/finally)", func(t *testing.T) {
		r := start(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "parent task")))
		r.Fire("session_start", nil)
		r.setFailClear(true)
		if f := r.fire("session_shutdown"); f == "" {
			t.Fatal("the dispose failure must propagate, as the original's try/finally rethrows it")
		}
		text, _ := r.call(map[string]any{"action": "list"})
		eq(t, text, "No tasks", "slot evicted")
		r.setFailClear(false)
		r.setSession("next")
		r.setBranch(snapshot(2, jtask(1, "next task")))
		r.Fire("session_start", nil)
		contains(t, strings.Join(lastRows(t, r), "\n"), "next task")
	})
	tw(t, fIsolation, "a headless launcher (hasUI:false) never constructs an overlay, nor does a headless child", func(t *testing.T) {
		r := start(t, HostOptions{Mode: "print"})
		r.setBranch(snapshot(2, jtask(1, "parent task")))
		r.Fire("session_start", nil)
		r.call(map[string]any{"action": "create", "subject": "more"})
		r.fireTool("tool_execution_end", map[string]any{"toolName": "todo", "isError": false})
		r.setSession("child")
		r.Fire("session_start", nil)
		eq(t, len(r.widgetCalls()), 0, "widget calls")
	})
}

func TestSessionLifecycleReplay(t *testing.T) {
	t.Run("session_start, session_compact and session_tree replay the branch", func(t *testing.T) {
		for _, event := range []string{"session_start", "session_compact", "session_tree"} {
			r := startRig(t, HostOptions{})
			r.setBranch(snapshot(3, jtask(1, "kept"), jtask(2, "also")))
			if event != "session_start" {
				r.Fire("session_start", nil)
				r.setBranch(snapshot(3, jtask(1, "kept"), jtask(2, "also"), jtask(3, "added")))
			}
			r.Fire(event, nil)
			text, _ := r.call(map[string]any{"action": "list"})
			if !strings.Contains(text, "kept") || !strings.Contains(text, "also") {
				t.Fatalf("%s: list = %q", event, text)
			}
			if event != "session_start" && !strings.Contains(text, "added") {
				t.Fatalf("%s: list = %q, want the rebuilt branch", event, text)
			}
		}
	})
	t.Run("a compact refreshes the overlay of the foreground", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "one")))
		r.Fire("session_start", nil)
		r.setBranch(snapshot(3, jtask(1, "one"), jtask(2, "two")))
		r.Fire("session_compact", nil)
		contains(t, strings.Join(lastRows(t, r), "\n"), "two")
	})
	t.Run("a failed todo tool call does not refresh the overlay and other tools are ignored", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "one")))
		r.Fire("session_start", nil)
		n := len(r.widgetCalls())
		r.fireTool("tool_execution_end", map[string]any{"toolName": "todo", "isError": true})
		r.fireTool("tool_execution_end", map[string]any{"toolName": "bash", "isError": false})
		eq(t, len(r.widgetCalls()), n, "widget calls")
	})
	t.Run("agent_start hides the completed tasks of the previous turn", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.setBranch(snapshot(2, map[string]any{"id": 1.0, "subject": "done", "status": "completed"}))
		r.Fire("session_start", nil)
		contains(t, strings.Join(lastRows(t, r), "\n"), "done")
		r.Fire("agent_start", nil)
		eq(t, len(rowsOf(lastOr(r))), 0, "rows after the next turn starts")
	})
	t.Run("rpc mode shows no overlay, like Pi (a component widget is not available there)", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "rpc"})
		r.setBranch(snapshot(2, jtask(1, "one")))
		r.Fire("session_start", nil)
		r.call(map[string]any{"action": "create", "subject": "two"})
		r.fireTool("tool_execution_end", map[string]any{"toolName": "todo", "isError": false})
		eq(t, len(rowsOf(lastOr(r))), 0, "rows")
	})
}

func lastOr(r *rig) map[string]any {
	c := r.widgetCalls()
	if len(c) == 0 {
		return map[string]any{}
	}
	return at(c, len(c)-1)
}

func TestShortcut(t *testing.T) {
	collapsedRows := 3
	tw(t, fShortcut, "registers 'ctrl+shift+t' with a description at factory scope", func(t *testing.T) {
		homeWith(t, "")
		known, _ := startRig(t, HostOptions{}).shortcut("ctrl+shift+t")
		eq(t, known, true, "registered")
	})
	tw(t, fShortcut, "handler is a no-op in headless mode (!ctx.hasUI)", func(t *testing.T) {
		homeWith(t, "")
		r := startRig(t, HostOptions{Mode: "print"})
		known, failure := r.shortcut("ctrl+shift+t")
		eq(t, known, true, "registered")
		eq(t, failure, "", "failure")
		eq(t, len(r.widgetCalls()), 0, "widget calls")
	})
	tw(t, fShortcut, "handler is a no-op before any session_start created the overlay (!todoOverlay)", func(t *testing.T) {
		homeWith(t, "")
		r := startRig(t, HostOptions{})
		_, failure := r.shortcut("ctrl+shift+t")
		eq(t, failure, "", "failure")
		eq(t, len(r.widgetCalls()), 0, "widget calls")
	})
	tw(t, fShortcut, "handler is a no-op when an empty session has not loaded the overlay", func(t *testing.T) {
		homeWith(t, "")
		r := startRig(t, HostOptions{})
		r.Fire("session_start", nil)
		_, failure := r.shortcut("ctrl+shift+t")
		eq(t, failure, "", "failure")
		eq(t, len(r.widgetCalls()), 0, "widget calls")
	})
	tw(t, fShortcut, "handler toggles the overlay when it is registered (render shape flips to the collapsed hint)", func(t *testing.T) {
		homeWith(t, "")
		r := startRig(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "one")))
		r.Fire("session_start", nil)
		_, failure := r.shortcut("ctrl+shift+t")
		eq(t, failure, "", "failure")
		rows := lastRows(t, r)
		eq(t, len(rows), collapsedRows, "collapsed rows")
		contains(t, at(rows, 1), "ctrl+shift+t to expand")
	})
	tw(t, fShortcut, "registers the configured key (collapseKey: 'alt+o') instead of the default", func(t *testing.T) {
		homeWith(t, `{"collapseKey":"alt+o"}`)
		r := startRig(t, HostOptions{})
		known, _ := r.shortcut("alt+o")
		eq(t, known, true, "alt+o")
		known, _ = r.shortcut("ctrl+shift+t")
		eq(t, known, false, "default key")
	})
	tw(t, fShortcut, "skips registerShortcut entirely when collapseKey is 'off'", func(t *testing.T) {
		homeWith(t, `{"collapseKey":"off"}`)
		r := startRig(t, HostOptions{})
		for _, k := range []string{"off", "ctrl+shift+t"} {
			known, _ := r.shortcut(k)
			eq(t, known, false, k)
		}
	})
	tw(t, fShortcut, "falls back to the default key when collapseKey is invalid", func(t *testing.T) {
		homeWith(t, `{"collapseKey":"ctr+t"}`)
		known, _ := startRig(t, HostOptions{}).shortcut("ctrl+shift+t")
		eq(t, known, true, "default key")
	})
	tw(t, fShortcut, "default config registers the default key (ctrl+shift+t)", func(t *testing.T) {
		homeWith(t, "")
		known, _ := startRig(t, HostOptions{}).shortcut("ctrl+shift+t")
		eq(t, known, true, "default key")
	})
}

func TestStaleContext(t *testing.T) {
	tw(t, fInval, "keeps current state on a stale ctx (replacement session replays)", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.call(map[string]any{"action": "create", "subject": "kept"})
		r.setFail("This extension ctx is stale after session replacement or reload.")
		for _, event := range []string{"session_compact", "session_tree"} {
			if f := r.fire(event); f != "" {
				t.Fatalf("%s must keep the current state on a stale ctx, failed: %s", event, f)
			}
		}
		r.setFail("")
		text, _ := r.call(map[string]any{"action": "list"})
		eq(t, text, "[pending] #1 kept", "state")
	})
	tw(t, fInval, "propagates a non-stale replay error", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.setFail("replay exploded")
		if f := r.fire("session_compact"); !strings.Contains(f, "replay exploded") {
			t.Fatalf("failure = %q, want the replay error", f)
		}
	})
}

// upstream: index.ts:227-243: the tool succeeded, a failed overlay refresh only costs that refresh.
func TestOverlayPushFailureDoesNotFailTheTool(t *testing.T) {
	r := startRig(t, HostOptions{})
	r.setBranch(snapshot(2, jtask(1, "one")))
	r.Fire("session_start", nil)
	r.setFailWidget(true)
	if f := r.fire("tool_execution_end", map[string]any{"toolName": "todo", "isError": false}); f != "" {
		t.Fatalf("a refused widget push failed the handler: %s", f)
	}
	r.setFailWidget(false)
	r.call(map[string]any{"action": "create", "subject": "two"})
	r.fireTool("tool_execution_end", map[string]any{"toolName": "todo", "isError": false})
	contains(t, strings.Join(lastRows(t, r), "\n"), "two")
}

// Pi starts the calls of a parallel batch in source order and its reducer is synchronous, so an update that
// follows a create in the same batch sees it. Here the first call's session-id read is slow: without the
// ordering the update would commit first and fail with "#1 not found". upstream: agent-loop.ts:619-647.
func TestParallelCallsCommitInStartOrder(t *testing.T) {
	r := startRig(t, HostOptions{})
	r.gate, r.reading, r.second = make(chan struct{}), make(chan struct{}), make(chan struct{})
	send := func(callID string, params map[string]any) chan string {
		out := make(chan string, 1)
		argv, _ := json.Marshal(params)
		go func() {
			raw, failure := r.Host.roundTrip(map[string]any{"method": "tool_call", "tool": "todo", "tool_call_id": callID, "args": json.RawMessage(argv)})
			var res struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(raw, &res)
			out <- res.Content + failure
		}()
		return out
	}
	first := send("call-a", map[string]any{"action": "create", "subject": "a"})
	<-r.reading // the second call is sent only once the first has taken its place (its session-id read is pending)
	second := send("call-b", map[string]any{"action": "update", "id": 1.0, "status": "completed"})
	<-r.second // both calls are inside their handlers: only now may the first read finish
	close(r.gate)
	eq(t, <-first, "Created #1: a (pending)", "first call")
	r.fireTool("tool_execution_end", map[string]any{"toolCallId": "call-a", "toolName": "todo", "isError": false})
	eq(t, <-second, "Updated #1 (pending → completed)", "second call")
	r.fireTool("tool_execution_end", map[string]any{"toolCallId": "call-b", "toolName": "todo", "isError": false})
}

// The overlay is pushed above the editor, and within the rows the host shows of a string[] widget
// (Pi interactive-mode.ts:2321-2336; todo-overlay.ts:107 placement "aboveEditor").
func TestOverlayPlacementAndRowBudget(t *testing.T) {
	r := startRig(t, HostOptions{})
	var tasks []map[string]any
	for i := 1; i <= 17; i++ {
		tasks = append(tasks, jtask(float64(i), "task"))
	}
	r.setBranch(snapshot(18, tasks...))
	r.Fire("session_start", nil)
	calls := r.widgetCalls()
	eq(t, len(calls), 1, "widget calls")
	eq(t, calls[0]["key"], "rpiv-todos", "key")
	eq(t, calls[0]["options"], map[string]any{"placement": "aboveEditor"}, "options")
	if n := len(rowsOf(calls[0])); n == 0 || n > 10 {
		t.Fatalf("%d rows pushed, the host shows 10", n)
	}
	contains(t, strings.Join(rowsOf(calls[0]), "\n"), "more")
}

// A host failure while reading the session id is an error of the call, not an empty session: the model sees
// the failure instead of a list that silently went to the wrong slot.
func TestToolReportsAHostFailure(t *testing.T) {
	r := startRig(t, HostOptions{})
	r.setFail("session read refused")
	argv, _ := json.Marshal(map[string]any{"action": "list"})
	_, failure := r.Host.roundTrip(map[string]any{"method": "tool_call", "tool": "todo", "tool_call_id": "call-x", "args": json.RawMessage(argv)})
	if !strings.Contains(failure, "session read refused") {
		t.Fatalf("failure = %q, want the host's error", failure)
	}
}
