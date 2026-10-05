package rpiv_todo_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	rpiv_todo "github.com/MichaelKinsy/pigpen/rpiv-todo"
)

// rig is a fake host for the todo extension: it answers the session reads the port makes (session id,
// branch, tool-output expansion) and keeps the widget and notify calls for the assertions.
type rig struct {
	*Host
	mu         sync.Mutex
	id         string
	branch     []map[string]any
	expanded   bool
	toolCalls  int
	gate       chan struct{} // when set, the first session-id read waits for it
	gated      bool
	reads      int
	second     chan struct{} // closed once a second session-id read is pending
	reading    chan struct{} // closed once the first session-id read is pending
	failRead   string        // when set, every sessionRead fails with this message
	failWidget bool          // when set, every setWidget call fails
	failClear  bool          // when set, a setWidget call without rows (a clear) fails
}

func startRig(t *testing.T, opts HostOptions) *rig {
	t.Helper()
	r := &rig{id: "session-1"}
	opts.OnCallValue = func(method string, args map[string]any) (any, string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch method {
		case "sessionRead":
			if r.failRead != "" {
				return nil, r.failRead
			}
			switch args["method"] {
			case "getSessionId":
				r.reads++
				if r.reads == 2 && r.second != nil {
					close(r.second)
				}
				if r.gate != nil && !r.gated {
					r.gated = true
					close(r.reading)
					gate := r.gate
					r.mu.Unlock()
					<-gate // the first session-id read is slow
					r.mu.Lock()
				}
				return r.id, ""
			case "getBranch":
				if r.branch == nil {
					return []any{}, ""
				}
				return r.branch, ""
			}
		case "ui.setWidget":
			if r.failWidget {
				return nil, "widget refused"
			}
			if _, hasRows := args["content"].([]any); !hasRows && r.failClear {
				return nil, "dispose failed"
			}
		case "ui.getToolsExpanded":
			return map[string]any{"expanded": r.expanded}, ""
		}
		return map[string]any{}, ""
	}
	r.Host = StartHost(t, rpiv_todo.Extension(), opts)
	return r
}

func (r *rig) setSession(id string)         { r.mu.Lock(); r.id = id; r.mu.Unlock() }
func (r *rig) setBranch(b []map[string]any) { r.mu.Lock(); r.branch = b; r.mu.Unlock() }
func (r *rig) setFailClear(v bool)          { r.mu.Lock(); r.failClear = v; r.mu.Unlock() }
func (r *rig) setFail(msg string)           { r.mu.Lock(); r.failRead = msg; r.mu.Unlock() }

// fire delivers an event and returns the handler's failure text instead of failing the test.
func (r *rig) fire(event string, data ...map[string]any) string {
	r.t.Helper()
	r.Host.mu.Lock()
	id, ok := r.Host.handlers[event]
	r.Host.mu.Unlock()
	if !ok {
		r.t.Fatalf("no handler registered for %s", event)
	}
	payload := map[string]any{"type": event}
	for _, d := range data {
		for k, v := range d {
			payload[k] = v
		}
	}
	args, _ := json.Marshal(payload)
	_, failure := r.Host.roundTrip(map[string]any{"method": "event", "event": event, "handler_id": id, "args": json.RawMessage(args)})
	return failure
}

func (r *rig) fireTool(name string, data map[string]any) {
	r.t.Helper()
	r.Fire(name, data)
}

// shortcut runs a registered shortcut; known is false when no such shortcut is registered.
func (r *rig) shortcut(key string) (known bool, failure string) {
	r.t.Helper()
	_, failure = r.Host.roundTrip(map[string]any{"method": "shortcut", "tool": key, "args": json.RawMessage(`{}`)})
	return !strings.Contains(failure, "unknown shortcut"), failure
}

// widgetCalls are the setWidget calls in order; each carries the key and the rows (nil for a clear).
func (r *rig) widgetCalls() []map[string]any {
	var out []map[string]any
	for _, c := range r.CallsTo("ui.setWidget") {
		out = append(out, c.Args)
	}
	return out
}

func (r *rig) notifies() []map[string]any {
	var out []map[string]any
	for _, c := range r.CallsTo("ui.notify") {
		out = append(out, c.Args)
	}
	return out
}

func rowsOf(call map[string]any) []string {
	var out []string
	if l, ok := call["content"].([]any); ok {
		for _, s := range l {
			out = append(out, s.(string))
		}
	}
	return out
}

// call runs the todo tool and returns its text and details.
func (r *rig) call(params map[string]any) (string, map[string]any) {
	r.t.Helper()
	r.mu.Lock()
	r.toolCalls++
	callID := fmt.Sprintf("call-%d", r.toolCalls)
	r.mu.Unlock()
	argv, _ := json.Marshal(params)
	raw, failure := r.Host.roundTrip(map[string]any{"method": "tool_call", "tool": "todo", "tool_call_id": callID, "args": json.RawMessage(argv)})
	if failure != "" {
		r.t.Fatalf("todo %v failed: %s", params, failure)
	}
	// The host reports every finished call; the extension holds a call's place in the ordering until then.
	r.Fire("tool_execution_end", map[string]any{"toolCallId": callID, "toolName": "todo", "isError": false})
	// On the extension wire a tool result carries its text as a string; the host builds the content blocks.
	var res struct {
		Content string         `json:"content"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		r.t.Fatalf("tool result %s: %v", raw, err)
	}
	return res.Content, res.Details
}

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !jsonEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", what, got, want)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (r *rig) setFailWidget(v bool) { r.mu.Lock(); r.failWidget = v; r.mu.Unlock() }

func at[T any](s []T, i int) T {
	var zero T
	if i < 0 || i >= len(s) {
		return zero
	}
	return s[i]
}
