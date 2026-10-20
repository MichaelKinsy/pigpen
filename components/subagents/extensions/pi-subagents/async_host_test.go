//go:build unix

package pi_subagents_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The background runs through the host boundary: a fake pig (a shell script that speaks the JSON event stream) is started
// by the real detached launcher, so the supervisor, the files, the notification and the actions all run for real.

const fakePigScript = `#!/bin/sh
task=$(cat)
dir=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--session-dir" ]; then dir="$a"; fi
  prev="$a"
done
if [ -n "$dir" ]; then mkdir -p "$dir"; echo '{"type":"session"}' > "$dir/fake-session.jsonl"; fi
printf '%s\n' "$*" > "$FAKE_PIG_ARGS"
echo '{"type":"session","version":3,"id":"fake-session","timestamp":"2026-01-01T00:00:00Z","cwd":"."}'
echo '{"type":"turn_start"}'
echo '{"type":"tool_execution_start","toolCallId":"c1","toolName":"read","args":{"path":"a.txt"}}'
echo '{"type":"tool_execution_end","toolCallId":"c1","toolName":"read","result":{"content":[]},"isError":false}'
sleep "${FAKE_PIG_SLEEP:-0}"
line=$(printf '%s' "$task" | head -1 | tr -d '"\\')
printf '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"built it: %s"}],"stopReason":"stop","usage":{"input":10,"output":2,"totalTokens":12}}}\n' "$line"
echo '{"type":"agent_settled"}'
exit "${FAKE_PIG_EXIT:-0}"
`

type asyncEnv struct {
	cwd, temp, argsFile string
}

func newAsyncEnv(t *testing.T) asyncEnv {
	t.Helper()
	cwd := tmp(t)
	withTempHome(t)
	temp := tmp(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", temp)
	script := filepath.Join(tmp(t), "fake-pig")
	writeFile(t, script, fakePigScript)
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SUBAGENT_PIG_BINARY", script)
	args := filepath.Join(tmp(t), "args.txt")
	t.Setenv("FAKE_PIG_ARGS", args)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	t.Cleanup(func() { killRuns(temp) })
	return asyncEnv{cwd, temp, args}
}

// killRuns ends every child a test left running (each leads its own process group).
func killRuns(temp string) {
	root := filepath.Join(temp, "async-subagent-runs")
	entries, _ := os.ReadDir(root)
	defer func() {
		// the supervisors finalize the killed runs; let them finish writing before the directory goes
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			running := false
			for _, e := range entries {
				if b, err := os.ReadFile(filepath.Join(root, e.Name(), "status.json")); err == nil && strings.Contains(string(b), `"state": "running"`) {
					running = true
				}
			}
			if !running {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
	}()
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(temp, "async-subagent-runs", e.Name(), "status.json"))
		if err != nil {
			continue
		}
		var st struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal(b, &st) == nil && st.PID > 1 {
			_ = syscall.Kill(-st.PID, syscall.SIGKILL)
		}
	}
}

func (e asyncEnv) host(t *testing.T) *Host {
	t.Helper()
	return StartHost(t, Extension(), HostOptions{Cwd: e.cwd, OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		switch method {
		case "getSessionFile":
			return map[string]any{"sessionFile": "/sessions/parent-a.jsonl"}, ""
		case "getSessionID":
			return map[string]any{"sessionId": "parent-a"}, ""
		}
		return map[string]any{}, ""
	}})
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func notices(h *Host) []map[string]any {
	var out []map[string]any
	for _, c := range h.CallsTo("sendMessage") {
		m, _ := c.Args["message"].(map[string]any)
		cp := map[string]any{"options": c.Args["options"]}
		for k, v := range m {
			cp[k] = v
		}
		out = append(out, cp)
	}
	return out
}

var asyncIDPattern = regexp.MustCompile(`Async: helper \[([0-9a-f-]{36})\]`)

func launch(t *testing.T, h *Host, params map[string]any) (id string, text string) {
	t.Helper()
	params["agent"], params["task"] = "helper", "build the thing"
	raw, failure := h.Tool("subagent", params)
	eq(t, failure, "", "launch failure")
	text = toolText(t, raw)
	m := asyncIDPattern.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("not an async launch: %s", text)
	}
	return m[1], text
}

func TestOmittedAsyncRunsInTheBackgroundAndWakesTheSession(t *testing.T) {
	e := newAsyncEnv(t)
	h := e.host(t)
	id, text := launch(t, h, map[string]any{})
	if !strings.Contains(text, "The async run is detached and running in the background.") || !strings.Contains(text, "Return control to the user now") {
		t.Errorf("launch text: %s", text)
	}
	eventually(t, "the completion notice", func() bool { return len(notices(h)) > 0 })
	n := notices(h)[0]
	eq(t, n["customType"], "subagent-notify", "custom type")
	content := n["content"].(string)
	if !strings.HasPrefix(content, "Background task completed: **helper**\n\nhelper:\nbuilt it: Task: build the thing") {
		t.Errorf("notice: %q", content)
	}
	if !strings.Contains(content, "Retention-managed async directory: "+filepath.Join(e.temp, "async-subagent-runs", id)) {
		t.Errorf("no async directory line: %q", content)
	}
	opts, _ := n["options"].(map[string]any)
	eq(t, opts["triggerTurn"], true, "wakes the session")
	args, _ := os.ReadFile(e.argsFile)
	if !strings.Contains(string(args), "--mode json") || strings.Contains(string(args), "--print") || !strings.Contains(string(args), "--no-extensions") {
		t.Errorf("child args: %s", args)
	}
	dir := filepath.Join(e.temp, "async-subagent-runs", id)
	b, err := os.ReadFile(filepath.Join(dir, "status.json"))
	eq(t, err, nil, "status.json")
	var st map[string]any
	_ = json.Unmarshal(b, &st)
	eq(t, st["state"], "complete", "state")
	eq(t, st["mode"], "single", "mode")
	for _, f := range []string{"events.jsonl", "output-0.log", "process-terminal.json", "recovery-descriptor.json", "runner-config.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	eventually(t, "the consumed result file to be removed", func() bool {
		_, err := os.Stat(filepath.Join(e.temp, "async-subagent-results", id+".json"))
		return os.IsNotExist(err)
	})
}

func TestAsyncFalseAndAsyncByDefaultFalseBlockTheParent(t *testing.T) {
	e := newAsyncEnv(t)
	calls := fakeChildren(t, map[string]string{"build": "built it"})
	h := e.host(t)
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": false})
	eq(t, failure, "", "failure")
	if !strings.Contains(toolText(t, raw), "built it") {
		t.Errorf("foreground: %s", raw)
	}
	eq(t, len(*calls), 1, "foreground child")
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".pi", "agent", "extensions", "subagent", "config.json"), `{"asyncByDefault": false}`)
	h2 := e.host(t)
	raw, failure = h2.Tool("subagent", map[string]any{"agent": "helper", "task": "build"})
	eq(t, failure, "", "failure")
	if !strings.Contains(toolText(t, raw), "built it") {
		t.Errorf("asyncByDefault false: %s", raw)
	}
	eq(t, len(*calls), 2, "second foreground child")
	_, failure = h2.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": true})
	eq(t, failure, "", "async true still works")
}

func TestStatusAndWaitForABackgroundRun(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "1")
	h := e.host(t)
	id, _ := launch(t, h, map[string]any{"async": true})
	raw, failure := h.Tool("subagent", map[string]any{"action": "status", "id": id})
	eq(t, failure, "", "status")
	text := toolText(t, raw)
	for _, want := range []string{"Status target: run " + id, "Spawn budget: unlimited", "Run: " + id, "State: running", "Mode: single", "Progress: step 1/1", "Dir: " + filepath.Join(e.temp, "async-subagent-runs", id)} {
		if !strings.Contains(text, want) {
			t.Errorf("status lacks %q:\n%s", want, text)
		}
	}
	raw, failure = h.Tool("subagent", map[string]any{"action": "status"})
	eq(t, failure, "", "list")
	if !strings.Contains(toolText(t, raw), "Active async runs: 1") {
		t.Errorf("list: %s", raw)
	}
	raw, failure = h.Tool("bg_wait", map[string]any{"id": id, "timeoutMs": 20000})
	eq(t, failure, "", "bg_wait")
	wt := toolText(t, raw)
	if !strings.Contains(wt, "done.") || !strings.Contains(wt, "Outcome: 1 complete.") {
		t.Errorf("bg_wait: %s", wt)
	}
	raw, _ = h.Tool("bg_wait", map[string]any{"id": id})
	if !strings.Contains(toolText(t, raw), "is terminal. Outcome: 1 complete.") {
		t.Errorf("bg_wait for a finished run: %s", raw)
	}
	raw, _ = h.Tool("subagent", map[string]any{"action": "status", "id": id})
	if t2 := toolText(t, raw); !strings.Contains(t2, "State: complete") || !strings.Contains(t2, "Revive: subagent({ action: \"resume\"") {
		t.Errorf("finished status: %s", t2)
	}
}

func TestStopAndInterruptABackgroundRun(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "30")
	h := e.host(t)
	id, _ := launch(t, h, map[string]any{})
	raw, failure := h.Tool("subagent", map[string]any{"action": "stop", "id": id})
	eq(t, failure, "", "stop")
	eq(t, toolText(t, raw), "Stop requested for async run "+id+".", "stop text")
	eventually(t, "the stopped notice", func() bool { return len(notices(h)) > 0 })
	content := notices(h)[0]["content"].(string)
	if !strings.HasPrefix(content, "Background task stopped: **helper**") || !strings.Contains(content, "Subagent stopped by user.") {
		t.Errorf("stop notice: %q", content)
	}
	_, failure = h.Tool("subagent", map[string]any{"action": "stop", "id": id})
	if !strings.Contains(failure, "No running or queued async run was found for") {
		t.Errorf("second stop: %q", failure)
	}

	id2, _ := launch(t, h, map[string]any{})
	raw, failure = h.Tool("subagent", map[string]any{"action": "interrupt", "id": id2})
	eq(t, failure, "", "interrupt")
	eq(t, toolText(t, raw), "Interrupt requested for async run "+id2+".", "interrupt text")
	eventually(t, "the paused notice", func() bool { return len(notices(h)) > 1 })
	content = notices(h)[1]["content"].(string)
	if !strings.HasPrefix(content, "Background task paused: **helper**") || !strings.Contains(content, "Paused after interrupt.") {
		t.Errorf("interrupt notice: %q", content)
	}
}

func TestARunOutlivesItsSessionAndTheNextSessionAdoptsIt(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "2")
	h := e.host(t)
	id, _ := launch(t, h, map[string]any{})
	h.FireAll("session_shutdown", nil) // the session ends; the detached child keeps going
	h2 := e.host(t)
	h2.FireAll("session_start", map[string]any{"reason": "resume"})
	eventually(t, "the adopted run's notice", func() bool { return len(notices(h2)) > 0 })
	content := notices(h2)[0]["content"].(string)
	if !strings.HasPrefix(content, "Background task completed: **helper**") {
		t.Errorf("notice: %q", content)
	}
	if !strings.Contains(content, id) {
		t.Errorf("notice does not name the run: %q", content)
	}
}

func TestResumeRevivesAFinishedChildFromItsSession(t *testing.T) {
	e := newAsyncEnv(t)
	h := e.host(t)
	id, _ := launch(t, h, map[string]any{})
	eventually(t, "the first notice", func() bool { return len(notices(h)) > 0 })
	raw, failure := h.Tool("subagent", map[string]any{"action": "resume", "id": id, "message": "now polish it"})
	eq(t, failure, "", "resume")
	text := toolText(t, raw)
	if !strings.Contains(text, "Revived helper run "+id+" from its persisted session as async run ") {
		t.Errorf("resume text: %s", text)
	}
	eventually(t, "the revived run's notice", func() bool { return len(notices(h)) > 1 })
	args, _ := os.ReadFile(e.argsFile)
	if !strings.Contains(string(args), "--session ") || !strings.Contains(string(args), "fake-session.jsonl") {
		t.Errorf("revived child args: %s", args)
	}
	content := notices(h)[1]["content"].(string)
	if !strings.Contains(content, "You are reviving a previous subagent conversation.") && !strings.Contains(content, "built it: Task: You are reviving a previous subagent conversation.") {
		t.Errorf("revived notice: %q", content)
	}
}

func TestOnlyCurrentSessionRunsAreControlled(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "30")
	h := e.host(t)
	id, _ := launch(t, h, map[string]any{})
	other := StartHost(t, Extension(), HostOptions{Cwd: e.cwd, OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "getSessionFile" {
			return map[string]any{"sessionFile": "/sessions/other.jsonl"}, ""
		}
		return map[string]any{}, ""
	}})
	_, failure := other.Tool("subagent", map[string]any{"action": "stop", "id": id})
	if !strings.Contains(failure, "was not found in the active session.") {
		t.Errorf("stop from another session: %q", failure)
	}
	_, failure = h.Tool("subagent", map[string]any{"action": "stop", "id": id})
	eq(t, failure, "", "stop from the owner")
}

func TestTimeoutAndCapacity(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "30")
	h := e.host(t)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "timeoutMs": 5, "maxRuntimeMs": 6})
	if !strings.Contains(failure, "timeoutMs and maxRuntimeMs are aliases") {
		t.Errorf("alias check: %q", failure)
	}
	id, _ := launch(t, h, map[string]any{"maxRuntimeMs": 700})
	eventually(t, "the timeout notice", func() bool { return len(notices(h)) > 0 })
	content := notices(h)[0]["content"].(string)
	if !strings.HasPrefix(content, "Background task failed: **helper**") || !strings.Contains(content, "Subagent timed out after 700ms.") {
		t.Errorf("timeout notice: %q", content)
	}
	_ = id
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".pi", "agent", "extensions", "subagent", "config.json"), `{"maxActiveAsyncRunsPerSession": 1}`)
	h2 := e.host(t)
	_, _ = launch(t, h2, map[string]any{})
	_, failure = h2.Tool("subagent", map[string]any{"agent": "helper", "task": "second", "async": true})
	eq(t, failure, "Active async run capacity exhausted: 1/1 used.", "capacity")
}
