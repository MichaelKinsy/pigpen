package tintinweb_subagents

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for `pig --mode rpc`: with FAKE_PIG_RPC set it speaks the RPC protocol the
// runner uses (JSON lines on stdin and stdout) instead of running the tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_PIG_RPC"); mode != "" {
		fakePig(mode)
		return
	}
	os.Exit(m.Run())
}

func fakePig(mode string) {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		if os.Getenv("PIG_EXT_SOCKET") != "" { // a probe must not hand an extension's connection to the probed program
			os.Exit(9)
		}
		if mode == "banner" { // some other program that exits 0 on --version
			os.Stdout.WriteString("GNU bash, version 5\n")
			os.Exit(0)
		}
		os.Stdout.WriteString("0.4.1+1.0.3\n")
		os.Exit(0)
	}
	if p := os.Getenv("FAKE_PIG_ARGS"); p != "" {
		os.WriteFile(p, []byte(strings.Join(os.Args[1:], "\n")+"\nDEPTH="+os.Getenv(depthEnv)), 0o644)
	}
	if mode == "crash" {
		os.Stderr.WriteString("no such model")
		os.Exit(3)
	}
	var outMu sync.Mutex
	enc := json.NewEncoder(os.Stdout)
	out := lockedEncoder{enc: enc, mu: &outMu}
	var aborted atomic.Bool
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var cmd map[string]any
		json.Unmarshal(sc.Bytes(), &cmd)
		resp := func(data any) {
			out.Encode(map[string]any{"type": "response", "id": cmd["id"], "command": cmd["type"], "success": true, "data": data})
		}
		switch cmd["type"] {
		case "prompt":
			resp(nil)
			if mode == "hang" {
				continue
			}
			if mode == "hang-grandchild" { // starts a process of its own, as a bash tool call would, and keeps working
				gc := exec.Command("sleep", "60")
				if os.Getenv("FAKE_PIG_GRANDCHILD_IGNORES_TERM") != "" { // only SIGKILL stops it
					gc = exec.Command("sh", "-c", `trap "" TERM; sleep 60`)
				}
				gc.Start()
				os.WriteFile(os.Getenv("FAKE_PIG_GRANDCHILD"), []byte(strconv.Itoa(gc.Process.Pid)), 0o644)
				continue
			}
			if mode == "runaway" { // keeps taking turns: FAKE_PIG_TURNS of them, then settles unless it was aborted
				n, _ := strconv.Atoi(os.Getenv("FAKE_PIG_TURNS"))
				for i := 0; i < n; i++ {
					out.Encode(map[string]any{"type": "turn_end"})
				}
				time.AfterFunc(500*time.Millisecond, func() {
					if !aborted.Load() {
						out.Encode(map[string]any{"type": "agent_settled"})
					}
				})
				continue
			}
			if mode == "provider-error" || mode == "provider-error-empty" || mode == "length-empty" {
				msg := map[string]any{"role": "assistant", "stopReason": "error", "errorMessage": " 429 rate limited ", "content": []any{map[string]any{"type": "text", "text": "half"}}}
				switch mode {
				case "provider-error-empty":
					msg["errorMessage"] = ""
				case "length-empty":
					msg = map[string]any{"role": "assistant", "stopReason": "length", "content": []any{map[string]any{"type": "text", "text": "  "}}}
				}
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "user", "content": "x"}})
				out.Encode(map[string]any{"type": "message_end", "message": msg})
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
			out.Encode(map[string]any{"type": "tool_execution_start"})
			out.Encode(map[string]any{"type": "turn_end"})
			out.Encode(map[string]any{"type": "turn_end"})
			out.Encode(map[string]any{"type": "agent_settled"})
		case "get_last_assistant_text":
			resp(map[string]any{"text": "fake answer to " + os.Getenv("FAKE_PIG_TAG")})
		case "get_session_stats":
			resp(map[string]any{"tokens": map[string]any{"total": 42}, "toolCalls": 2})
		case "steer":
			if p := os.Getenv("FAKE_PIG_STEER"); p != "" {
				f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
				f.WriteString(cmd["message"].(string) + "\n")
				f.Close()
			}
			resp(nil)
		case "abort":
			resp(nil)
			if mode == "runaway" { // an abort ends the run, not the process
				aborted.Store(true)
				if p := os.Getenv("FAKE_PIG_STEER"); p != "" {
					f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
					f.WriteString("<abort>\n")
					f.Close()
				}
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
			os.Exit(0)
		}
	}
}

type lockedEncoder struct {
	enc *json.Encoder
	mu  *sync.Mutex
}

func (l lockedEncoder) Encode(v any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enc.Encode(v)
}

func useFakePig(t *testing.T, mode string) string {
	t.Helper()
	exe, _ := os.Executable()
	t.Setenv("TINTINWEB_SUBAGENTS_PIG_BINARY", exe)
	t.Setenv("FAKE_PIG_RPC", mode)
	t.Setenv("FAKE_PIG_TAG", "the task")
	args := t.TempDir() + "/args"
	t.Setenv("FAKE_PIG_ARGS", args)
	return args
}

func TestChildArgs(t *testing.T) {
	eq(t, childArgs(childSpec{}), []string{"--mode", "rpc", "--no-session"})
	eq(t, childArgs(childSpec{Model: "p/m", Thinking: "high", Tools: []string{"read", "grep"}, SystemPrompt: "SP", PromptMode: "replace"}),
		[]string{"--mode", "rpc", "--no-session", "--model", "p/m", "--thinking", "high", "--tools", "read,grep", "--system-prompt", "SP"})
	eq(t, childArgs(childSpec{Tools: []string{}, SystemPrompt: "SP", PromptMode: "append"}),
		[]string{"--mode", "rpc", "--no-session", "--no-tools", "--append-system-prompt", "SP"})
	eq(t, childArgs(childSpec{SystemPrompt: "   ", PromptMode: "replace"}), []string{"--mode", "rpc", "--no-session"})
}

func TestRPCChildRunsToCompletion(t *testing.T) {
	argsFile := useFakePig(t, "ok")
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "Task: x", Tools: []string{"read"}, Cwd: t.TempDir()})
	eq(t, err, nil)
	res, err := c.wait()
	eq(t, err, nil)
	eq(t, res.Text, "fake answer to the task")
	eq(t, res.Tokens, 42)
	eq(t, res.ToolUses, 2)
	eq(t, res.Turns, 2)
	got, _ := os.ReadFile(argsFile)
	eq(t, strings.Contains(string(got), "--mode\nrpc\n--no-session\n--tools\nread"), true)
	eq(t, strings.HasSuffix(string(got), "DEPTH=1"), true) // a child registers no agent tools
}

func TestRPCChildCrashIsAnError(t *testing.T) {
	useFakePig(t, "crash")
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "p", Cwd: t.TempDir()})
	if err == nil {
		_, err = c.wait()
	}
	eq(t, err != nil, true)
}

func TestRPCChildSteerAndAbort(t *testing.T) {
	useFakePig(t, "hang")
	steer := t.TempDir() + "/steer"
	t.Setenv("FAKE_PIG_STEER", steer)
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "p", Cwd: t.TempDir()})
	eq(t, err, nil)
	eq(t, c.steer("go left"), nil)
	b, _ := os.ReadFile(steer)
	eq(t, string(b), "go left\n")
	done := make(chan error, 1)
	go func() { _, err := c.wait(); done <- err }()
	c.abort()
	select {
	case err := <-done:
		eq(t, err != nil, true) // an aborted child reports as such
	case <-time.After(10 * time.Second):
		t.Fatal("abort did not release wait")
	}
}

func TestTurnLimitAsksTheAgentToWrapUp(t *testing.T) {
	useFakePig(t, "ok")
	steer := t.TempDir() + "/steer"
	t.Setenv("FAKE_PIG_STEER", steer)
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "p", Cwd: t.TempDir(), MaxTurns: 2})
	eq(t, err, nil)
	res, err := c.wait()
	eq(t, err, nil)
	eq(t, res.WrappedUp, true)
	var b []byte
	for i := 0; i < 500 && !strings.Contains(string(b), "turn limit"); i++ {
		time.Sleep(10 * time.Millisecond)
		b, _ = os.ReadFile(steer)
	}
	eq(t, string(b), turnLimitSteer+"\n")
	eq(t, turnLimitSteer, "You have reached your turn limit. Wrap up immediately — provide your final answer now.") // upstream: agent-runner.ts:1061
}

func TestARunnerChildStartsNoAgentTools(t *testing.T) {
	t.Setenv(depthEnv, "1")
	e := Extension()
	eq(t, e != nil, true)
}

func TestPigIsRecognisedByItsVersion(t *testing.T) {
	exe, _ := os.Executable()
	t.Setenv("FAKE_PIG_RPC", "")
	os.Unsetenv("FAKE_PIG_RPC")
	eq(t, isPig(exe), false) // the test binary is not pig: it does not print a version
	eq(t, isPig(exe+".missing"), false)
	t.Setenv("FAKE_PIG_RPC", "banner")
	eq(t, isPig(exe), false) // exits 0 but prints no version number
	t.Setenv("FAKE_PIG_RPC", "ok")
	eq(t, isPig(exe), true)
}

func TestThePigThatStartedTheExtensionIsPreferred(t *testing.T) {
	exe, _ := os.Executable()
	os.Unsetenv("TINTINWEB_SUBAGENTS_PIG_BINARY")
	t.Setenv("FAKE_PIG_RPC", "ok")
	harness := t.TempDir() + "/pig" // another path to the same fake, so the answer shows which candidate won
	if err := os.Symlink(exe, harness); err != nil {
		t.Skip(err)
	}
	t.Setenv("PIG_HARNESS_BINARY", harness)
	t.Setenv("PIG_EXT_SOCKET", "/nonexistent/ext.sock")
	t.Setenv("PATH", t.TempDir())
	pigOnce, pigPath, pigErr = sync.Once{}, "", nil
	t.Cleanup(func() { pigOnce, pigPath, pigErr = sync.Once{}, "", nil })
	got, err := pigBinary()
	if err != nil { // (the error text is not printed: the mutation runner reads "cannot find" as a build failure)
		t.Fatal("no pig was found")
	}
	eq(t, got, harness)
}
