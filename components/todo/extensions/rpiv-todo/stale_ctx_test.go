package rpiv_todo_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// After a session replacement or reload Pi invalidates the captured context: its getters, `ctx.sessionManager`
// among them, throw "This extension ctx is stale after session replacement or reload ..."
// (runner.ts assertActive). The original treats that phrase as "nothing to bind" in session_start,
// session_compact, session_tree and session_shutdown (index.ts:111-114, 131-133, 197-201, 224-228) and lets
// the tool fail with it. The Go SDK throws the same host message from its own getters (Context.Cwd,
// HasUI, Mode), not from session reads, so the port asks the context first.
const staleMessage = "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload()."

// invalidate tells the extension its session was replaced, as the host does before the replacement's events.
func (r *rig) invalidate() {
	r.Host.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "invalidate", "args": map[string]any{"message": staleMessage}}})
	// The notify is applied on the extension's read loop: wait until a getter throws.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.stale() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	r.t.Fatal("the extension did not take the invalidation")
}

// stale reports whether a handler now sees the stale context: session_tree over an empty branch makes no
// host call that a stale ctx would not also make, so probe through a hasUI read of the shortcut handler.
func (r *rig) stale() bool {
	known, failure := r.shortcut("ctrl+shift+t")
	return known && strings.Contains(failure, "stale after session replacement")
}

func (r *rig) sessionReads() int {
	n := 0
	for _, c := range r.CallsTo("sessionRead") {
		if c.Args["method"] == "getSessionId" || c.Args["method"] == "getBranch" {
			n++
		}
	}
	return n
}

func TestStaleContextReadsNothingFromTheDeadSession(t *testing.T) {
	t.Run("session_compact keeps the state and reads nothing from the dead session", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "one")))
		r.Fire("session_start", nil)
		n, w := r.sessionReads(), len(r.widgetCalls())
		r.invalidate()
		if f := r.fire("session_compact"); f != "" {
			t.Fatalf("session_compact failed: %s", f)
		}
		eq(t, r.sessionReads(), n, "session reads")
		eq(t, len(r.widgetCalls()), w, "widget calls")
	})
	t.Run("session_tree is the same", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.invalidate()
		if f := r.fire("session_tree"); f != "" {
			t.Fatalf("session_tree failed: %s", f)
		}
		eq(t, r.sessionReads(), 0, "session reads")
	})
	t.Run("session_start has nothing to bind", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "one")))
		r.invalidate()
		if f := r.fire("session_start"); f != "" {
			t.Fatalf("session_start failed: %s", f)
		}
		eq(t, len(r.widgetCalls()), 0, "widget calls")
		eq(t, r.sessionReads(), 0, "session reads")
	})
	t.Run("session_shutdown treats an unknown session as the foreground and clears the widget", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.setBranch(snapshot(2, jtask(1, "one")))
		r.Fire("session_start", nil)
		n := r.sessionReads()
		r.invalidate()
		if f := r.fire("session_shutdown"); f != "" {
			t.Fatalf("session_shutdown failed: %s", f)
		}
		eq(t, r.sessionReads(), n, "session reads")
		calls := r.widgetCalls()
		last := at(calls, len(calls)-1)
		eq(t, last["key"], "rpiv-todos", "key")
		eq(t, len(rowsOf(last)), 0, "a clear carries no rows")
	})
	t.Run("the tool fails with the stale message instead of touching the dead session", func(t *testing.T) {
		r := startRig(t, HostOptions{})
		r.invalidate()
		argv, _ := json.Marshal(map[string]any{"action": "list"})
		_, failure := r.Host.roundTrip(map[string]any{"method": "tool_call", "tool": "todo", "tool_call_id": "call-s", "args": json.RawMessage(argv)})
		if !strings.Contains(failure, "stale after session replacement") {
			t.Fatalf("failure = %q, want the stale message", failure)
		}
		eq(t, r.sessionReads(), 0, "session reads")
	})
}
