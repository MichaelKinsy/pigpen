package tintinweb_tasks_test

// Helpers the template fake host does not have: a request that is cancelled while it runs, a terminal resize,
// and a rig that starts in another mode. (The template stays unchanged; see the porting Skill.)

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	tintinweb_tasks "github.com/MichaelKinsy/pigpen/tintinweb-tasks"
)

// toolAborted runs a tool call, cancels its request after the given delay (the host does that when Pi's signal
// aborts) and returns the result.
func (r *rig) toolAborted(name string, params obj, after time.Duration) (text string, failure string) {
	r.t.Helper()
	h := r.Host
	r.fireEvent("tool_execution_start", obj{"toolName": name})
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	argv, _ := json.Marshal(params)
	h.write(obj{"type": "request", "id": id, "request": obj{"method": "tool_call", "tool": name, "tool_call_id": "call-1", "args": json.RawMessage(argv)}})
	time.Sleep(after)
	h.write(obj{"type": "cancel", "id": id, "cancel": obj{"request_id": id, "reason": "test"}})
	select {
	case raw := <-ch:
		h.mu.Lock()
		failure = h.failures[id]
		h.mu.Unlock()
		if failure != "" {
			return "", failure
		}
		var res struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			r.t.Fatalf("tool result %s: %v", raw, err)
		}
		return res.Content, ""
	case <-time.After(10 * time.Second):
		r.t.Fatalf("%s did not return after its request was cancelled", name)
	}
	return "", ""
}

// widthChange tells the extension the terminal is now width columns wide (the host's width_change notification).
func (r *rig) widthChange(width int) {
	r.Host.write(obj{"type": "notify", "notify": obj{"method": "width_change", "args": obj{"width": width}}})
}

// startRigMode is startRig with the host in another mode ("rpc", "print").
func startRigMode(t *testing.T, cwd, mode string) *rig {
	t.Helper()
	if cwd == "" {
		cwd = t.TempDir()
	}
	r := &rig{cwd: cwd, sessionID: "s1", persisted: true, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	r.Host = StartHost(t, tintinweb_tasks.Extension(), HostOptions{Cwd: cwd, Mode: mode, OnCallValue: r.answer})
	return r
}

// widgetView is the view of the latest ui.setWidget call (nil when that call carried none): the root node's
// kind and its rows.
func (r *rig) widgetView() (kind string, rows []string, call map[string]any) {
	calls := r.widgetCalls()
	if len(calls) == 0 {
		return "", nil, nil
	}
	call = calls[len(calls)-1]
	view, _ := call["view"].(map[string]any)
	root, _ := view["root"].(map[string]any)
	kind, _ = root["kind"].(string)
	content, _ := root["content"].([]any)
	for _, row := range content {
		rows = append(rows, row.(string))
	}
	return kind, rows, call
}

var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

// cells is the width of a row in terminal cells for the plain text these tests use.
func cells(row string) int { return len([]rune(ansiSeq.ReplaceAllString(row, ""))) }

func sleepMs(n int) { time.Sleep(time.Duration(n) * time.Millisecond) }
