//go:build unix

package driver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The tests drive a scripted fake `pig`: the test binary re-executes itself
// with FAKE_PIG_MODE set (see TestMain). The fake speaks pig's RPC protocol with
// the record shapes of the real v0.7.0 release (testdata/basket-events-v0.7.0.jsonl)
// and writes session files in PiG's format, so the driver and the session
// package are exercised end to end.

const (
	fakeCallProbe = "call_probe_1"
	fakeProbeName = "mcp__localhost__kagent_probe"
)

type fakePigState struct {
	mu       sync.Mutex
	out      *bufio.Writer
	logPath  string
	args     []string
	dir      string
	id       string
	file     string
	counter  int
	commands chan map[string]any
}

func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *fakePigState) emit(v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, _ := json.Marshal(v)
	f.out.Write(append(data, '\n'))
	f.out.Flush()
}

func (f *fakePigState) logf(kind string, fields map[string]any) {
	if f.logPath == "" {
		return
	}
	fields["kind"] = kind
	data, _ := json.Marshal(fields)
	handle, err := os.OpenFile(f.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		handle.Write(append(data, '\n'))
		handle.Close()
	}
}

func (f *fakePigState) entryID() string {
	f.counter++
	return fmt.Sprintf("f%07d", time.Now().UnixNano()%1e7+int64(f.counter))
}

// ensureSession creates the session file like pig does on first use.
func (f *fakePigState) ensureSession() {
	matches, _ := filepath.Glob(filepath.Join(f.dir, "*_"+f.id+".jsonl"))
	if len(matches) > 0 {
		f.file = matches[0]
		return
	}
	f.file = filepath.Join(f.dir, "2026-11-07T01-35-56-101Z_"+f.id+".jsonl")
	cwd, _ := os.Getwd()
	header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": f.id, "timestamp": "2026-11-07T01:35:56.101Z", "cwd": cwd})
	os.WriteFile(f.file, append(header, '\n'), 0o600)
}

func (f *fakePigState) lastEntryID() string {
	data, _ := os.ReadFile(f.file)
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry struct {
		ID string `json:"id"`
	}
	for i := len(rows) - 1; i > 0; i-- {
		if json.Unmarshal([]byte(rows[i]), &entry) == nil && entry.ID != "" {
			return entry.ID
		}
	}
	return ""
}

func (f *fakePigState) append(id string, message map[string]any) {
	entry := map[string]any{"type": "message", "id": id, "parentId": nilIfEmpty(f.lastEntryID()), "timestamp": "2026-11-07T01:35:57.000Z", "message": message}
	data, _ := json.Marshal(entry)
	handle, _ := os.OpenFile(f.file, os.O_APPEND|os.O_WRONLY, 0)
	handle.Write(append(data, '\n'))
	handle.Close()
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func usage(in, out, cacheRead int) map[string]any {
	return map[string]any{"input": in, "output": out, "cacheRead": cacheRead, "cacheWrite": 0, "reasoning": 2, "totalTokens": in + out + cacheRead,
		"cost": map[string]any{"total": 0}}
}

func (f *fakePigState) assistant(content []any, stop string, u map[string]any) map[string]any {
	return map[string]any{"role": "assistant", "content": content, "api": "openai-completions", "provider": argValue(f.args, "--provider"), "model": argValue(f.args, "--model"),
		"usage": u, "stopReason": stop, "timestamp": time.Now().UnixMilli(), "responseId": "resp-" + stop, "durationMs": 40}
}

func (f *fakePigState) textMessage(text string) {
	f.emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
	half := len(text) / 2
	f.emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": text[:half]}})
	f.emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": text[half:]}})
	message := f.assistant([]any{map[string]any{"type": "text", "text": text}}, "stop", usage(10, 5, 3))
	f.append(f.entryID(), message)
	f.emit(map[string]any{"type": "message_end", "message": message})
}

func (f *fakePigState) callMessage(calls ...map[string]any) (string, map[string]any) {
	content := make([]any, 0, len(calls))
	for _, c := range calls {
		content = append(content, map[string]any{"type": "toolCall", "id": c["id"], "name": c["name"], "arguments": c["arguments"]})
	}
	message := f.assistant(content, "toolUse", usage(20, 8, 0))
	id := f.entryID()
	f.append(id, message)
	f.emit(map[string]any{"type": "message_end", "message": message})
	return id, message
}

func (f *fakePigState) confirm(kind, callID, name string, input any) {
	payload, _ := json.Marshal(map[string]any{"v": 1, "kind": kind, "toolCallId": callID, "toolName": name, "input": input})
	f.emit(map[string]any{"type": "extension_ui_request", "id": "ui-" + callID, "method": "confirm", "title": "Approve " + name, "message": string(payload)})
}

func fakePig(mode string) int {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "version" {
		fmt.Println("0.7.0+1.5.0 (fake)")
		return 0
	}
	f := &fakePigState{out: bufio.NewWriterSize(os.Stdout, 1<<20), logPath: os.Getenv("FAKE_PIG_LOG"), args: args}
	f.dir, f.id = argValue(args, "--session-dir"), argValue(args, "--session-id")
	cwd, _ := os.Getwd()
	f.logf("start", map[string]any{"args": args, "env": os.Environ(), "cwd": cwd, "pid": os.Getpid(), "pgid": mustPgid()})
	f.ensureSession()
	if mode == "resume" || mode == "resume-approve-again" || mode == "resume-crash" {
		data, _ := os.ReadFile(f.file)
		f.logf("session_at_start", map[string]any{"session": string(data)})
	}
	if mode == "hang-stubborn" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
	}
	commands := make(chan map[string]any, 16)
	f.commands = commands
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 1<<20), 32<<20)
		for scanner.Scan() {
			var command map[string]any
			if json.Unmarshal(scanner.Bytes(), &command) == nil {
				commands <- command
			}
		}
		close(commands)
	}()
	onEOF := func() int {
		f.logf("stdin_eof", map[string]any{})
		if mode == "late-usage" {
			// usage that arrives only once stdin closes, i.e. after the cancel grace
			f.emit(map[string]any{"type": "message_end", "message": f.assistant([]any{}, "aborted", usage(9, 1, 0))})
		}
		return 0
	}
	for command := range commands {
		kind, _ := command["type"].(string)
		f.logf("command", map[string]any{"type": kind, "message": command["message"], "cancelled": command["cancelled"], "confirmed": command["confirmed"]})
		switch kind {
		case "get_state":
			model, session := argValue(args, "--model"), f.id
			switch mode {
			case "wrong-model":
				model = "something-else"
			case "wrong-session":
				session = "00000000-0000-4000-8000-000000000000"
			}
			f.emit(map[string]any{"id": command["id"], "type": "response", "command": "get_state", "success": true,
				"data": map[string]any{"model": map[string]any{"id": model, "provider": argValue(args, "--provider")}, "sessionId": session}})
		case "abort":
			f.emit(map[string]any{"id": command["id"], "type": "response", "command": "abort", "success": true})
			if mode == "hang" || mode == "hang-tool" {
				message := f.assistant([]any{}, "aborted", usage(7, 1, 0))
				f.emit(map[string]any{"type": "message_end", "message": message})
				f.emit(map[string]any{"type": "agent_settled", "aborted": true})
			}
		case "prompt":
			if code, done := f.onPrompt(mode, command); done {
				return code
			}
		}
	}
	return onEOF()
}

func mustPgid() int { id, _ := syscall.Getpgid(0); return id }

// onPrompt scripts one prompt. done=true ends the process with code.
func (f *fakePigState) onPrompt(mode string, command map[string]any) (int, bool) {
	if mode == "crash-before-accept" {
		fmt.Fprintln(os.Stderr, "boom: died before answering the prompt")
		return 5, true
	}
	if mode == "reject" {
		f.emit(map[string]any{"id": command["id"], "type": "response", "command": "prompt", "success": false, "error": "SECRET-REASON"})
		return 0, false
	}
	f.emit(map[string]any{"id": command["id"], "type": "response", "command": "prompt", "success": true, "data": map[string]any{"disposition": "started"}})
	f.emit(map[string]any{"type": "agent_start"})
	f.append(f.entryID(), map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": command["message"]}}, "timestamp": time.Now().UnixMilli()})
	switch mode {
	case "text", "resume":
		f.textMessage("hello world")
	case "text-reconcile":
		f.emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
		f.emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "hello "}})
		f.emit(map[string]any{"type": "message_end", "message": f.assistant([]any{map[string]any{"type": "text", "text": "hello world"}}, "stop", usage(1, 1, 0))})
		f.emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
		f.emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "second"}})
		f.emit(map[string]any{"type": "message_end", "message": f.assistant([]any{map[string]any{"type": "text", "text": "second"}}, "stop", usage(1, 1, 0))})
	case "tool":
		f.callMessage(map[string]any{"id": "call_bash", "name": "bash", "arguments": map[string]any{"command": "echo hi"}},
			map[string]any{"id": "call_mcp", "name": fakeProbeName, "arguments": map[string]any{"note": "n"}})
		f.emit(map[string]any{"type": "tool_execution_start", "toolCallId": "call_bash", "toolName": "bash", "args": map[string]any{"command": "echo hi"}})
		f.emit(map[string]any{"type": "tool_execution_end", "toolCallId": "call_bash", "toolName": "bash", "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "hi\n"}}}, "isError": false})
		f.emit(map[string]any{"type": "tool_execution_start", "toolCallId": "call_mcp", "toolName": fakeProbeName, "args": map[string]any{"note": "n"}})
		f.emit(map[string]any{"type": "tool_execution_end", "toolCallId": "call_mcp", "toolName": fakeProbeName, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "probe:n"}}}, "isError": true})
		f.textMessage("done")
	case "error":
		message := f.assistant([]any{}, "error", usage(4, 0, 0))
		message["errorMessage"] = "SECRET-PROVIDER-TEXT"
		f.emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
		f.emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "partial answer"}})
		f.emit(map[string]any{"type": "message_end", "message": message})
	case "approve", "approve-slow-exit", "resume-approve-again":
		id := fakeCallProbe
		if mode == "resume-approve-again" {
			id = "call_probe_2"
		}
		f.callMessage(map[string]any{"id": id, "name": fakeProbeName, "arguments": map[string]any{"note": "basket"}})
		f.confirm("approval", id, fakeProbeName, map[string]any{"note": "basket"})
		return f.waitForEOF(mode)
	case "approve-wrong-name":
		f.callMessage(map[string]any{"id": fakeCallProbe, "name": "bash", "arguments": map[string]any{"command": "ls"}})
		f.confirm("approval", fakeCallProbe, fakeProbeName, map[string]any{"note": "basket"})
		return f.waitForEOF(mode)
	case "approve-unknown-call":
		f.callMessage(map[string]any{"id": "real_call", "name": fakeProbeName, "arguments": map[string]any{"note": "basket"}})
		f.confirm("approval", "other_call", fakeProbeName, map[string]any{"note": "basket"})
		return f.waitForEOF(mode)
	case "approve-array-input":
		f.callMessage(map[string]any{"id": fakeCallProbe, "name": fakeProbeName, "arguments": map[string]any{"note": "basket"}})
		f.confirm("approval", fakeCallProbe, fakeProbeName, []any{"basket"})
		return f.waitForEOF(mode)
	case "approve-batch":
		f.callMessage(map[string]any{"id": "c1", "name": "bash", "arguments": map[string]any{"command": "echo"}},
			map[string]any{"id": "c2", "name": fakeProbeName, "arguments": map[string]any{"note": "basket"}},
			map[string]any{"id": "c3", "name": "bash", "arguments": map[string]any{"command": "echo 3"}})
		f.confirm("approval", "c2", fakeProbeName, map[string]any{"note": "basket"})
		return f.waitForEOF(mode)
	case "approve-batch-started":
		// PiG announces each call before its hooks run: c1 and the gated c2 are announced, c3 never is.
		f.callMessage(map[string]any{"id": "c1", "name": "bash", "arguments": map[string]any{"command": "echo"}},
			map[string]any{"id": "c2", "name": fakeProbeName, "arguments": map[string]any{"note": "basket"}},
			map[string]any{"id": "c3", "name": "bash", "arguments": map[string]any{"command": "echo 3"}})
		f.emit(map[string]any{"type": "tool_execution_start", "toolCallId": "c1", "toolName": "bash", "args": map[string]any{"command": "echo"}})
		f.emit(map[string]any{"type": "tool_execution_start", "toolCallId": "c2", "toolName": fakeProbeName, "args": map[string]any{"note": "basket"}})
		f.confirm("approval", "c2", fakeProbeName, map[string]any{"note": "basket"})
		return f.waitForEOF(mode)
	case "hang-tool":
		f.callMessage(map[string]any{"id": "call_bash", "name": "bash", "arguments": map[string]any{"command": "sleep 99"}})
		f.emit(map[string]any{"type": "tool_execution_start", "toolCallId": "call_bash", "toolName": "bash", "args": map[string]any{"command": "sleep 99"}})
		f.logf("hanging", map[string]any{})
		return 0, false
	case "late-usage":
		f.emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
		f.logf("hanging", map[string]any{})
		return 0, false
	case "ask":
		f.callMessage(map[string]any{"id": "call_ask", "name": "ask_user", "arguments": map[string]any{}})
		f.emit(map[string]any{"type": "tool_execution_start", "toolCallId": "call_ask", "toolName": "ask_user", "args": map[string]any{}})
		f.confirm("ask_user", "call_ask", "ask_user", map[string]any{"questions": []any{
			map[string]any{"question": "Which env?", "choices": []any{"dev", "prod"}},
			map[string]any{"question": "Ticket id?"}}})
		return f.waitForEOF(mode)
	case "approve-garbage":
		f.emit(map[string]any{"type": "extension_ui_request", "id": "ui-x", "method": "confirm", "message": "not json"})
		f.emit(map[string]any{"type": "extension_ui_request", "id": "ui-y", "method": "select", "options": []any{"a"}})
		f.emit(map[string]any{"type": "extension_ui_request", "id": "ui-z", "method": "notify", "notifyType": "error", "message": "policy broken"})
		f.textMessage("after dialogs")
	case "hang":
		f.emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
		f.emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "partial"}})
		f.logf("hanging", map[string]any{})
		return 0, false
	case "hang-stubborn":
		f.emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
		f.logf("hanging", map[string]any{})
		for {
			time.Sleep(time.Hour)
		}
	case "crash":
		f.emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
		fmt.Fprintln(os.Stderr, "boom: fake pig crashed")
		return 3, true
	case "silent-exit":
		return 0, true
	case "huge":
		f.mu.Lock()
		f.out.WriteString(`{"type":"message_update","pad":"` + strings.Repeat("x", MaxFrameBytes+10) + `"}` + "\n")
		f.out.Flush()
		f.mu.Unlock()
		time.Sleep(time.Minute)
	case "text-with-child":
		child := exec.Command("sleep", "60")
		child.Start()
		os.WriteFile(os.Getenv("FAKE_PIG_CHILD_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		f.textMessage("spawned")
	case "resume-crash":
		fmt.Fprintln(os.Stderr, "boom: continuation crashed")
		return 5, true
	default:
		fmt.Fprintln(os.Stderr, "unknown fake mode "+mode)
		return 9, true
	}
	f.emit(map[string]any{"type": "agent_end", "messages": []any{}, "willRetry": false})
	f.emit(map[string]any{"type": "agent_settled", "aborted": false})
	return 0, false
}

// waitForEOF models a pig blocked on an unanswered confirm: it replies to
// nothing and only leaves when stdin closes.
func (f *fakePigState) waitForEOF(mode string) (int, bool) {
	for command := range f.commands {
		kind, _ := command["type"].(string)
		f.logf("late_command", map[string]any{"type": kind})
	}
	f.logf("stdin_eof", map[string]any{})
	if mode == "approve-slow-exit" {
		time.Sleep(150 * time.Millisecond)
		os.WriteFile(os.Getenv("FAKE_PIG_EXIT_MARKER"), []byte("x"), 0o600)
	}
	return 0, true
}
