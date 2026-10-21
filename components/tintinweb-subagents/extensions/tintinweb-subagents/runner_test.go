package tintinweb_subagents

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
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
	// Transcripts go under the temp dir: keep them out of the real one.
	dir, err := os.MkdirTemp("", "tw-obs-tmp-")
	if err != nil {
		panic(err)
	}
	os.Setenv("TMPDIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
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
	if p, f := os.Getenv("FAKE_PIG_SCHEMA"), os.Getenv(structuredSchemaFileEnv); p != "" && f != "" { // what the child would read
		b, _ := os.ReadFile(f)
		os.WriteFile(p, b, 0o644)
	}
	if mode == "crash" {
		os.Stderr.WriteString("no such model")
		os.Exit(3)
	}
	var outMu sync.Mutex
	enc := json.NewEncoder(os.Stdout)
	logCmd := func(cmd map[string]any) { // FAKE_PIG_LOG: every command, in order, as `type` or `type name`
		p := os.Getenv("FAKE_PIG_LOG")
		if p == "" {
			return
		}
		line, _ := cmd["type"].(string)
		if n, ok := cmd["name"].(string); ok {
			line += " " + n
		}
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		f.WriteString(line + "\n")
		f.Close()
	}
	out := lockedEncoder{enc: enc, mu: &outMu}
	var aborted atomic.Bool
	n := 0 // prompts answered
	prompts := 0
	var emitted []any // the messages a scripted run produced, which get_messages answers
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var cmd map[string]any
		json.Unmarshal(sc.Bytes(), &cmd)
		logCmd(cmd)
		resp := func(data any) {
			out.Encode(map[string]any{"type": "response", "id": cmd["id"], "command": cmd["type"], "success": true, "data": data})
		}
		switch cmd["type"] {
		case "prompt":
			resp(nil)
			if mode == "hang" || mode == "deaf" {
				continue
			}
			if mode == "stoppable" { // the first run hangs until it is aborted; later runs answer, as in "events"
				n++
				if n == 1 {
					continue
				}
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "answer " + strconv.Itoa(n) + " to " + cmd["message"].(string)}}}})
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
			if mode == "hang-partial" || mode == "die-partial" { // a run that has said something, then stalls or dies
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "system", "content": "SYSTEM PROMPT"}})
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "user", "content": "the task"}})
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "partial findings"}, map[string]any{"type": "toolCall", "name": "bash"}}}})
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "toolResult", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": "listing"}}}})
				if mode == "die-partial" {
					os.Exit(2)
				}
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
			if mode == "compaction" { // six exchanges, a compaction that rewrites the history, then one more exchange
				ex := func(q, a string) {
					out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "user", "content": q}})
					out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": a}}}})
					out.Encode(map[string]any{"type": "turn_end"})
				}
				for i := 0; i < 6; i++ {
					ex("question "+strconv.Itoa(i), "answer-"+strconv.Itoa(i)+" filler")
				}
				out.Encode(map[string]any{"type": "compaction_start", "reason": "manual"})
				out.Encode(map[string]any{"type": "compaction_end", "reason": "manual", "aborted": true, "result": map[string]any{"summary": "never kept", "tokensBefore": 1}})
				out.Encode(map[string]any{"type": "compaction_end", "reason": "manual", "aborted": false, "errorMessage": "failed", "tokensBefore": 2})
				out.Encode(map[string]any{"type": "compaction_end", "reason": "manual", "aborted": false, "willRetry": false, "result": map[string]any{"summary": "summary of everything so far", "tokensBefore": 4321}})
				ex("final question", "POST-COMPACTION-ANSWER")
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
			if strings.HasPrefix(mode, "structured") { // answers in prose first; the retry prompt gets a StructuredOutput call
				prompts++
				if p := os.Getenv("FAKE_PIG_PROMPTS"); p != "" {
					f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
					f.WriteString(strings.ReplaceAll(cmd["message"].(string), "\n", " ") + "\n")
					f.Close()
				}
				end := func(isErr bool, text, json string) {
					out.Encode(map[string]any{"type": "tool_execution_end", "toolName": "StructuredOutput", "isError": isErr,
						"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "details": map[string]any{"json": json}}})
				}
				switch {
				case mode == "structured-none" && prompts == 1:
					end(true, "StructuredOutput did not match the required schema:\n$: must have required properties file\nCall it again with a corrected value.", "")
				case mode == "structured" && prompts == 2:
					end(false, "Recorded.", `{"file":"a.ts"}`)
				}
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
			if mode == "fatal-only" || mode == "fatal-masked" { // the #144 shapes: a final turn that errors with no content
				fatal := "invalid request: provider rejected the prompt"
				emit := func(msg map[string]any) {
					emitted = append(emitted, msg)
					out.Encode(map[string]any{"type": "message_end", "message": msg})
				}
				emit(map[string]any{"role": "user", "content": "x"})
				if mode == "fatal-masked" {
					emit(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "EARLIER-PARTIAL-TEXT"}, map[string]any{"type": "toolCall", "name": "bash"}}})
					emit(map[string]any{"role": "toolResult", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": "hi"}}})
				}
				emit(map[string]any{"role": "assistant", "stopReason": "error", "errorMessage": fatal, "content": []any{}})
				out.Encode(map[string]any{"type": "agent_settled"})
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
			if mode == "events" { // answers each prompt with a message of its own, as pig does
				n++
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "answer " + strconv.Itoa(n) + " to " + cmd["message"].(string)}}}})
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
			if mode == "resume-fail-empty" || mode == "resume-fail-partial" { // a good first turn, then a failed resume
				n++
				text := func(role, s string, extra map[string]any) {
					msg := map[string]any{"role": role, "content": []any{map[string]any{"type": "text", "text": s}}}
					for k, v := range extra {
						msg[k] = v
					}
					out.Encode(map[string]any{"type": "message_end", "message": msg})
				}
				switch {
				case n == 1:
					text("assistant", "PREVIOUS ANSWER", map[string]any{"stopReason": "stop"})
				case mode == "resume-fail-partial":
					text("assistant", "new partial", nil)
					out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "toolResult", "content": []any{}}})
					fallthrough
				default:
					out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": "retries exhausted: 529 overloaded"}})
				}
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
			if mode == "events-silent" { // the second run produces no message at all
				n++
				if n == 1 {
					out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "first answer"}}}})
				}
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
			if mode == "usage-nonassistant" { // a tool result that carries a usage object is not an assistant message's spend
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "toolResult", "toolName": "bash", "usage": map[string]any{"input": 999, "output": 999}, "content": []any{}}})
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}, "usage": map[string]any{"input": 7, "output": 3}}})
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
			out.Encode(map[string]any{"type": "tool_execution_start"})
			// Two assistant messages with usage, as a provider reports them.
			for i := 0; i < 2; i++ {
				out.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "step"}},
					"usage": map[string]any{"input": 30, "output": 12, "cacheRead": 5, "cacheWrite": 3, "cost": map[string]any{"total": 0.01}}}})
				out.Encode(map[string]any{"type": "turn_end"})
			}
			out.Encode(map[string]any{"type": "agent_settled"})
		case "set_session_name":
			resp(nil)
		case "get_state":
			resp(map[string]any{"sessionFile": os.Getenv("FAKE_PIG_SESSION")})
		case "new_session":
			if p := os.Getenv("FAKE_PIG_NEWSESSION"); p != "" {
				b, _ := json.Marshal(cmd)
				os.WriteFile(p, b, 0o644)
			}
			resp(map[string]any{"cancelled": false})
		case "get_last_assistant_text":
			if strings.HasPrefix(mode, "fatal") { // pi answers {} for a final turn with no text
				resp(map[string]any{})
				continue
			}
			resp(map[string]any{"text": "fake answer to " + os.Getenv("FAKE_PIG_TAG")})
		case "get_session_stats":
			resp(map[string]any{"tokens": map[string]any{"total": 42}, "toolCalls": 2, "contextUsage": map[string]any{"tokens": 100, "contextWindow": 1000, "percent": 10.4}})
		case "get_messages":
			if len(emitted) > 0 {
				resp(map[string]any{"messages": emitted})
				continue
			}
			resp(map[string]any{"messages": []any{map[string]any{"role": "user", "content": "x"}, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "step"}}}}})
		case "steer":
			if p := os.Getenv("FAKE_PIG_STEER"); p != "" {
				f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
				f.WriteString(cmd["message"].(string) + "\n")
				f.Close()
			}
			resp(nil)
		case "abort":
			resp(nil)
			if mode == "deaf" { // never settles, never exits: only a kill ends it
				continue
			}
			if mode == "stoppable" { // an abort ends the run, not the process
				out.Encode(map[string]any{"type": "agent_settled"})
				continue
			}
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
	base := []string{"--mode", "rpc", "--no-session", "--no-context-files", "--no-prompt-templates", "--no-themes", "--append-system-prompt", ""}
	eq(t, childArgs(childSpec{}), base)
	eq(t, childArgs(childSpec{Model: "p/m", Thinking: "high", AllowTools: []string{"read", "grep"}, NoExtensions: true, SystemPrompt: "SP", PromptMode: "replace"}),
		append(slices.Clone(base), "--no-extensions", "--model", "p/m", "--thinking", "high", "--tools", "read,grep", "--system-prompt", "SP"))
	eq(t, childArgs(childSpec{AllowTools: []string{}, SystemPrompt: "SP", PromptMode: "append"}),
		append(slices.Clone(base), "--no-tools", "--append-system-prompt", "SP"))
	eq(t, childArgs(childSpec{SystemPrompt: "   ", PromptMode: "replace"}), base)
}

func TestRPCChildRunsToCompletion(t *testing.T) {
	argsFile := useFakePig(t, "ok")
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "Task: x", AllowTools: []string{"read"}, Cwd: t.TempDir()})
	eq(t, err, nil)
	res, err := c.wait()
	eq(t, err, nil)
	eq(t, res.Text, "fake answer to the task")
	eq(t, res.ToolUses, 2)
	eq(t, res.Turns, 2)
	got, _ := os.ReadFile(argsFile)
	eq(t, strings.Contains(string(got), "--no-session\n--no-context-files\n--no-prompt-templates\n--no-themes\n--append-system-prompt\n\n--tools\nread"), true)
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
	for i := 0; i < 6000 && !strings.Contains(string(b), "turn limit"); i++ {
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

func TestRPCChildPersistedStartLinksTheParentAndReportsItsSessionFile(t *testing.T) {
	argsFile := useFakePig(t, "ok")
	t.Setenv("FAKE_PIG_SESSION", "/sessions/child.jsonl")
	newSession := t.TempDir() + "/new"
	t.Setenv("FAKE_PIG_NEWSESSION", newSession)
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "p", Cwd: t.TempDir(), Persist: true, ParentSession: "/sessions/parent.jsonl", SessionName: "Explore#abcd1234"})
	eq(t, err, nil)
	eq(t, c.sessionFile(), "/sessions/child.jsonl")
	b, _ := os.ReadFile(newSession)
	var cmd map[string]any
	_ = json.Unmarshal(b, &cmd)
	eq(t, cmd["parentSession"], "/sessions/parent.jsonl")
	got, _ := os.ReadFile(argsFile)
	eq(t, strings.Contains(string(got), "--name\nExplore#abcd1234\n"), true)
	eq(t, strings.Contains(string(got), "--no-session"), false)
	res, err := c.wait()
	eq(t, err, nil)
	eq(t, res.Text, "fake answer to the task")
	// A persisted conversation is reopened from its file, so its process ends with the run.
	eq(t, c.(*rpcChild).alive(), false)
	eq(t, c.resume("more") != nil, true)
}

func TestRPCChildReopensAFile(t *testing.T) {
	argsFile := useFakePig(t, "events")
	t.Setenv("FAKE_PIG_SESSION", "/sessions/child.jsonl")
	newSession := t.TempDir() + "/new"
	t.Setenv("FAKE_PIG_NEWSESSION", newSession)
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "carry on", Cwd: t.TempDir(), ResumeFile: "/sessions/child.jsonl", Persist: true, ParentSession: "/sessions/parent.jsonl"})
	eq(t, err, nil)
	got, _ := os.ReadFile(argsFile)
	eq(t, strings.Contains(string(got), "--session\n/sessions/child.jsonl\n"), true)
	_, statErr := os.Stat(newSession)
	eq(t, os.IsNotExist(statErr), true) // a reopened conversation keeps its own header: no new session, no parent link
	res, err := c.wait()
	eq(t, err, nil)
	eq(t, res.Text, "answer 1 to carry on")
	eq(t, res.Turns, 0)
}

func TestRPCChildInMemoryConversationOutlivesARunAndTakesAResume(t *testing.T) {
	useFakePig(t, "events")
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "one", Cwd: t.TempDir(), MaxTurns: 1})
	eq(t, err, nil)
	t.Cleanup(c.close)
	eq(t, c.sessionFile(), "")
	res, err := c.wait()
	eq(t, err, nil)
	eq(t, res.Text, "answer 1 to one")
	eq(t, c.alive(), true) // the conversation lives in this process alone
	eq(t, c.resume("two"), nil)
	res, err = c.wait()
	eq(t, err, nil)
	eq(t, res.Text, "answer 2 to two") // the same session answered, not a new process
	eq(t, c.alive(), true)
}

func TestARunThatProducesNothingNeverReturnsThePreviousAnswer(t *testing.T) {
	useFakePig(t, "events-silent")
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "one", Cwd: t.TempDir()})
	eq(t, err, nil)
	t.Cleanup(c.close)
	res, err := c.wait()
	eq(t, err, nil)
	eq(t, res.Text, "first answer")
	eq(t, c.resume("two"), nil)
	res, err = c.wait()
	eq(t, err, nil)
	eq(t, res.Text, "") // #144: a resume that failed empty does not inherit the prior turn's text
}

func TestAChildThatEndsCannotBeResumed(t *testing.T) {
	useFakePig(t, "events")
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "one", Cwd: t.TempDir()})
	eq(t, err, nil)
	_, err = c.wait()
	eq(t, err, nil)
	c.close()
	for i := 0; i < 500 && c.alive(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	eq(t, c.alive(), false)
	eq(t, c.resume("two") != nil, true)
}
