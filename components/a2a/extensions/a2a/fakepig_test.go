package a2aext

// The fake `pig --mode rpc`: the test binary re-executed (Skill step 3, "fake external CLI").
// It speaks the JSONL protocol of `pig --mode rpc` for the commands the worker uses and logs
// what it saw to $A2A_FAKE_LOG so tests can assert on the argv, environment and abort.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("A2A_FAKE_PIG") == "1" {
		fakePig()
		return
	}
	os.Exit(m.Run())
}

type fakeLog struct {
	Terminated bool              `json:"terminated"`
	Args       []string          `json:"args"`
	Cwd        string            `json:"cwd"`
	Env        map[string]string `json:"env"`
	PID        int               `json:"pid"`
	Prompt     string            `json:"prompt"`
	Started    bool              `json:"started"`
	Aborted    bool              `json:"aborted"`
	Finished   bool              `json:"finished"`
	ChildPID   int               `json:"childPid"`
}

func fakePig() {
	logPath := os.Getenv("A2A_FAKE_LOG")
	cwd, _ := os.Getwd()
	entry := fakeLog{Args: os.Args[1:], Cwd: cwd, PID: os.Getpid(), Env: map[string]string{}}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		entry.Env[k] = v
	}
	save := func() {
		if logPath == "" {
			return
		}
		b, _ := json.Marshal(entry)
		_ = os.WriteFile(logPath, b, 0o600)
	}
	save()
	// A graceful stop (SIGTERM) is recorded; "IGNORE_TERM" prompts ignore it too, so only SIGKILL ends them.
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	go func() {
		<-term
		entry.Terminated = true
		save()
		if !strings.Contains(entry.Prompt, "IGNORE_TERM") {
			os.Exit(0)
		}
	}()
	out := bufio.NewWriter(os.Stdout)
	emit := func(v map[string]any) {
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	settle := func(stop string, text, errMsg string) {
		msg := map[string]any{"role": "assistant", "stopReason": stop, "content": []any{map[string]any{"type": "text", "text": text}}}
		if errMsg != "" {
			msg["errorMessage"] = errMsg
		}
		emit(map[string]any{"type": "message_end", "message": msg})
		emit(map[string]any{"type": "agent_end", "willRetry": false})
		emit(map[string]any{"type": "agent_settled"})
		entry.Finished = true
		save()
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<24)
	for in.Scan() {
		var cmd struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(in.Bytes(), &cmd) != nil {
			continue
		}
		switch cmd.Type {
		case "prompt":
			entry.Prompt, entry.Started = cmd.Message, true
			save()
			if strings.Contains(cmd.Message, "REJECT") {
				emit(map[string]any{"id": cmd.ID, "type": "response", "command": "prompt", "success": false, "error": "secret internal reason"})
				continue
			}
			if strings.Contains(cmd.Message, "ORPHAN") {
				// A tool that outlives the turn: a child in the worker's process group that nobody waits for.
				child := exec.Command("sleep", "60")
				if child.Start() == nil {
					entry.ChildPID = child.Process.Pid
					save()
				}
			}
			emit(map[string]any{"id": cmd.ID, "type": "response", "command": "prompt", "success": true})
			emit(map[string]any{"type": "agent_start"})
			emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant"}})
			switch {
			case strings.Contains(cmd.Message, "NEEDUI"):
				emit(map[string]any{"type": "extension_ui_request", "id": "ui-1", "method": "confirm", "title": "sure?"})
				select {} // a real PiG would wait for the answer
			case strings.Contains(cmd.Message, "IGNORE_ABORT"), strings.Contains(cmd.Message, "IGNORE_TERM"):
				select {} // hang until killed
			case strings.Contains(cmd.Message, "SLOW"):
				emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "working"}})
				// wait for abort in the read loop
			case strings.Contains(cmd.Message, "FAIL"):
				settle("error", "", "provider exploded with secret sk-123")
			case strings.Contains(cmd.Message, "TOOL"):
				emit(map[string]any{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "read", "args": map[string]any{"path": "x"}})
				emit(map[string]any{"type": "tool_execution_end", "toolCallId": "t1", "toolName": "read", "result": map[string]any{"content": []any{}}, "isError": false})
				emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "done"}})
				settle("stop", "done", "")
			default:
				h := len(cmd.Message) / 2
				emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "echo: " + cmd.Message[:h]}})
				emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": cmd.Message[h:]}})
				settle("stop", "echo: "+cmd.Message, "")
			}
		case "abort":
			entry.Aborted = true
			save()
			emit(map[string]any{"id": cmd.ID, "type": "response", "command": "abort", "success": true})
			settle("aborted", "working", "")
		}
	}
}

// fakeWorkerConfig points a WorkerConfig at the fake pig and returns the log path.
func fakeWorkerConfig(t *testing.T) (WorkerConfig, string, func(string) string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := t.TempDir() + "/fake.log"
	env := map[string]string{
		"A2A_FAKE_PIG": "1", "A2A_FAKE_LOG": logPath, "PATH": os.Getenv("PATH"), "HOME": "/home/x",
		"PIG_HOME": "/pig/home", "PIG_CODING_AGENT_DIR": "/pig/agent",
		"TOKEN_A": tokenA, "OPENAI_API_KEY": "sk-passthrough", "SOMETHING_SECRET": "leak",
	}
	cfg := WorkerConfig{Command: exe, Tools: []string{"read", "grep", "find", "ls"}, Cwd: t.TempDir(), GraceSeconds: 1,
		PassEnv: []string{"A2A_FAKE_PIG", "A2A_FAKE_LOG", "OPENAI_API_KEY"}}
	return cfg, logPath, func(k string) string { return env[k] }
}

func readFakeLog(t *testing.T, path string) fakeLog {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(path)
		var l fakeLog
		if err == nil && json.Unmarshal(b, &l) == nil && l.PID != 0 {
			return l
		}
		if time.Now().After(deadline) {
			t.Fatalf("no fake pig log at %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitFakeLog(t *testing.T, path string, ok func(fakeLog) bool) fakeLog {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		l := readFakeLog(t, path)
		if ok(l) {
			return l
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not reached: %+v", l)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

var _ = fmt.Sprintf
