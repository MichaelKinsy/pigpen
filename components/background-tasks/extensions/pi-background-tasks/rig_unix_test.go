//go:build !windows

package pi_background_tasks_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	bg "github.com/MichaelKinsy/pigpen/pi-background-tasks"
)

// rigHost starts the port on a Rig (hand-driven timers and log appends) against the fake host. The shell stand-in for a
// task's child is `cat <fifo>`: the test writes chunks and closing the fifo is the child's exit, as the original's fixtures
// write to and close a fake child.
type rigged struct {
	*Host
	rig     *bg.Rig
	mu      sync.Mutex
	session string
	branch  []map[string]any
}

// setSession changes what the host answers for the session id and the branch, as a fork or a switch does.
func (r *rigged) setSession(id string, branch []map[string]any) {
	r.mu.Lock()
	r.session, r.branch = id, branch
	r.mu.Unlock()
}

type rigOptions struct {
	config map[string]any
	branch []map[string]any
	// session is the session id the host answers; empty answers an in-memory session.
	session string
	noStart bool
}

func (r *rigged) wakeLogs() []string { return r.rig.WakeLogs() }

func rigHost(t *testing.T, o rigOptions) *rigged {
	t.Helper()
	// The original's fixtures run with showWidget false (spawn-child-runner.ts), so the widget's expiry timer is not among the timers
	// they count; the widget twins turn it on.
	config := map[string]any{"resourceControlEnabled": false, "showWidget": false}
	for k, v := range o.config {
		config[k] = v
	}
	agentWith(t, config)
	bg.ResetSettings()
	r := &rigged{rig: bg.NewRig(t), session: o.session, branch: o.branch}
	cwd := t.TempDir()
	answer := func(method string, args map[string]any) (any, string) {
		r.mu.Lock()
		session, branch := r.session, r.branch
		r.mu.Unlock()
		switch method {
		case "isProjectTrusted":
			return map[string]any{"trusted": false}, ""
		case "getSessionID":
			return map[string]any{"sessionId": session}, ""
		case "sessionRead":
			switch args["method"] {
			case "getBranch":
				return branch, ""
			case "getCwd":
				return cwd, ""
			case "getSessionId":
				return session, ""
			case "getSessionFile":
				return nil, ""
			}
		}
		return map[string]any{}, ""
	}
	r.Host = StartHost(t, r.rig.Extension(), HostOptions{OnCallValue: answer, Cwd: cwd})
	if !o.noStart {
		r.Fire("session_start", map[string]any{"reason": "startup"})
	}
	t.Cleanup(func() {
		r.rig.ReleaseAppends()
		r.Fire("session_shutdown", map[string]any{"reason": "quit"})
	})
	return r
}

// fifoTask is a task whose output the test writes.
type fifoTask struct {
	path   string
	w      *os.File
	result toolOut
	logs   string
}

func (r *rigged) spawnFifo(t *testing.T, params map[string]any) *fifoTask {
	t.Helper()
	path := filepath.Join(t.TempDir(), "child.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	p := map[string]any{"action": "spawn", "command": "cat '" + path + "'"}
	for k, v := range params {
		p[k] = v
	}
	if c, ok := p["wrap"].(string); ok {
		delete(p, "wrap")
		p["command"] = strings.ReplaceAll(c, "{fifo}", path)
	}
	raw, failure := r.Tool("bg_task", p)
	eq(t, failure, "", "spawn")
	f := &fifoTask{path: path, result: text(t, raw)}
	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.w = w
	task, _ := f.result.Details["task"].(map[string]any)
	f.logs, _ = task["logFile"].(string)
	t.Cleanup(func() { _ = f.w.Close() })
	return f
}

func (f *fifoTask) write(t *testing.T, s string) {
	t.Helper()
	if _, err := f.w.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// exit is the child's close: the writer end of the fifo goes away.
func (f *fifoTask) exit() { _ = f.w.Close() }

func (r *rigged) status(id string) string {
	task, ok := r.rig.Task(id)
	if !ok {
		return ""
	}
	s, _ := task["status"].(string)
	return s
}

func (r *rigged) waitStatus(t *testing.T, id, want string) {
	t.Helper()
	eventually(t, id+" to be "+want, func() bool { return r.status(id) == want })
}

// persisted is the task as the last appendEntry wrote it, or nil.
func (r *rigged) persisted(id string) map[string]any {
	calls := r.CallsTo("appendEntry")
	for i := len(calls) - 1; i >= 0; i-- {
		data, _ := calls[i].Args["data"].(map[string]any)
		tasks, _ := data["tasks"].([]any)
		for _, raw := range tasks {
			if m, _ := raw.(map[string]any); m != nil && m["id"] == id {
				return m
			}
		}
		return nil
	}
	return nil
}

func (r *rigged) entries() int { r.rig.Settle(); return len(r.CallsTo("appendEntry")) }

func fileSize(path string) int {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return int(st.Size())
}

func waitFor(t *testing.T, what string, ok func() bool) { t.Helper(); eventually(t, what, ok) }

func (r *rigged) messages() []map[string]any {
	var out []map[string]any
	for _, c := range r.CallsTo("sendMessage") {
		m, _ := c.Args["message"].(map[string]any)
		out = append(out, m)
	}
	return out
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

var _ = sdk.Bool
var _ = time.Second

// toolAsync sends a tool call and returns a channel closed when it is answered; unlike Host.Tool it does not fail the test
// after ten seconds.
func (r *rigged) toolAsync(name string, params map[string]any) chan struct{} {
	r.mu.Lock()
	r.Host.mu.Lock()
	r.Host.nextID++
	id := fmt.Sprintf("r%d", r.Host.nextID)
	ch := make(chan json.RawMessage, 1)
	r.Host.pending[id] = ch
	r.Host.mu.Unlock()
	r.mu.Unlock()
	argv, _ := json.Marshal(params)
	r.Host.write(map[string]any{"type": "request", "id": id, "request": map[string]any{"method": "tool_call", "tool": name, "tool_call_id": "call-async", "args": json.RawMessage(argv)}})
	done := make(chan struct{})
	go func() { <-ch; close(done) }()
	return done
}
