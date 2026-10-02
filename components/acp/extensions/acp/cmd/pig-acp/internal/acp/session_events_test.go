package acp

// Twins of test/component/session-events.test.ts. Each test keeps the upstream test name.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cwdNow(t testing.TB) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func textDelta(s string) Event {
	return Event{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": s}}
}

func chunk(text string) Update {
	return Update{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}
}

func TestSessionEvents(t *testing.T) {
	tw(t, "component/session-events", "PiAcpSession: emits agent_message_chunk for text_delta", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(textDelta("hi"))
		settle(t, s)
		ups := conn.all()
		if len(ups) != 1 || ups[0].SessionID != "s1" {
			t.Fatalf("updates = %+v", ups)
		}
		jsonEqual(t, ups[0].Update, chunk("hi"))
	})

	tw(t, "component/session-events", "PiAcpSession: emits agent_thought_chunk for thinking_delta", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "thinking_delta", "delta": "thinking..."}})
		settle(t, s)
		ups := conn.all()
		if len(ups) != 1 || ups[0].SessionID != "s1" {
			t.Fatalf("updates = %+v", ups)
		}
		jsonEqual(t, ups[0].Update, Update{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "thinking..."}})
	})

	tw(t, "component/session-events", "PiAcpSession: emits tool_call + tool_call_update + completes", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		cwd := cwdNow(t)
		s := newTestSession(cwd, proc, conn)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "bash", "args": map[string]any{"command": "ls"}})
		proc.emit(Event{"type": "tool_execution_update", "toolCallId": "t1", "partialResult": map[string]any{"content": []any{map[string]any{"type": "text", "text": "running"}}}})
		proc.emit(Event{"type": "tool_execution_end", "toolCallId": "t1", "isError": false, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "done"}}}})
		settle(t, s)
		ups := conn.all()
		if len(ups) != 3 {
			t.Fatalf("got %d updates: %+v", len(ups), ups)
		}
		u0 := ups[0].Update
		if u0["sessionUpdate"] != "tool_call" || u0["toolCallId"] != "t1" || u0["title"] != "ls" || u0["kind"] != "execute" || u0["status"] != "in_progress" {
			t.Errorf("update 0 = %v", u0)
		}
		if _, ok := u0["locations"]; ok {
			t.Error("locations must be undefined for a bash call without a path")
		}
		jsonEqual(t, u0["content"], []any{map[string]any{"type": "terminal", "terminalId": "t1"}})
		jsonEqual(t, u0["_meta"], map[string]any{"terminal_info": map[string]any{"terminal_id": "t1", "cwd": cwd}})
		if _, ok := u0["rawInput"]; ok {
			t.Error("rawInput must be undefined")
		}
		u1 := ups[1].Update
		if u1["sessionUpdate"] != "tool_call_update" || u1["toolCallId"] != "t1" || u1["status"] != "in_progress" {
			t.Errorf("update 1 = %v", u1)
		}
		if _, ok := u1["content"]; ok {
			t.Error("content must be undefined")
		}
		jsonEqual(t, u1["_meta"], map[string]any{"terminal_output": map[string]any{"terminal_id": "t1", "data": "running"}})
		if _, ok := u1["rawOutput"]; ok {
			t.Error("rawOutput must be undefined")
		}
		u2 := ups[2].Update
		if u2["sessionUpdate"] != "tool_call_update" || u2["toolCallId"] != "t1" || u2["status"] != "completed" {
			t.Errorf("update 2 = %v", u2)
		}
		if _, ok := u2["content"]; ok {
			t.Error("content must be undefined")
		}
		jsonEqual(t, u2["_meta"], map[string]any{
			"terminal_output": map[string]any{"terminal_id": "t1", "data": "done"},
			"terminal_exit":   map[string]any{"terminal_id": "t1", "exit_code": 0, "signal": nil},
		})
		if _, ok := u2["rawOutput"]; ok {
			t.Error("rawOutput must be undefined")
		}
	})

	tw(t, "component/session-events", "PiAcpSession: emits tool locations from pi path args", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		cwd := cwdNow(t)
		s := newTestSession(cwd, proc, conn)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "read", "args": map[string]any{"path": "src/acp/session.ts"}})
		settle(t, s)
		ups := conn.all()
		if len(ups) != 1 || ups[0].Update["sessionUpdate"] != "tool_call" {
			t.Fatalf("updates = %+v", ups)
		}
		jsonEqual(t, ups[0].Update["locations"], []any{map[string]any{"path": cwd + "/src/acp/session.ts"}})
	})

	tw(t, "component/session-events", "PiAcpSession: handles extension select via ACP permission request", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		conn.nextPermission = PermissionResponse{Outcome: PermissionOutcome{Outcome: "selected", OptionID: "choice-1"}}
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "ui-1", "method": "select", "title": "Pick one", "options": []any{"Alpha", "Beta"}})
		eventually(t, "the extension UI response", func() bool { return len(proc.uiResponses()) == 1 })
		settle(t, s)
		reqs := conn.permissions()
		if len(reqs) != 1 {
			t.Fatalf("%d permission requests", len(reqs))
		}
		jsonEqual(t, reqs[0], map[string]any{
			"sessionId": "s1",
			"toolCall": map[string]any{
				"toolCallId": "pi-ui-ui-1", "title": "Pick one", "kind": "other", "status": "pending",
				"rawInput": map[string]any{"method": "select", "title": "Pick one", "options": []any{"Alpha", "Beta"}},
			},
			"options": []any{
				map[string]any{"optionId": "choice-0", "name": "Alpha", "kind": "allow_once"},
				map[string]any{"optionId": "choice-1", "name": "Beta", "kind": "allow_once"},
			},
		})
		jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "ui-1", "value": "Beta"}})
	})

	tw(t, "component/session-events", "PiAcpSession: handles extension confirm via ACP permission request", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		conn.nextPermission = PermissionResponse{Outcome: PermissionOutcome{Outcome: "selected", OptionID: "no"}}
		newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "ui-2", "method": "confirm", "title": "Clear session?", "message": "All messages will be lost."})
		eventually(t, "the extension UI response", func() bool { return len(proc.uiResponses()) == 1 })
		reqs := conn.permissions()
		if len(reqs) != 1 {
			t.Fatalf("%d permission requests", len(reqs))
		}
		jsonEqual(t, reqs[0].Options, []any{
			map[string]any{"optionId": "yes", "name": "Yes", "kind": "allow_once"},
			map[string]any{"optionId": "no", "name": "No", "kind": "reject_once"},
		})
		jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "ui-2", "confirmed": false}})
	})

	tw(t, "component/session-events", "PiAcpSession: sends cancelled response when ACP confirm is cancelled", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		conn.nextPermission = PermissionResponse{Outcome: PermissionOutcome{Outcome: "cancelled"}}
		newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "ui-5", "method": "confirm", "title": "Continue?"})
		eventually(t, "the extension UI response", func() bool { return len(proc.uiResponses()) == 1 })
		jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "ui-5", "cancelled": true}})
	})

	tw(t, "component/session-events", "PiAcpSession: cancels unsupported input and editor extension UI requests with visible fallback", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "ui-3", "method": "input", "title": "Enter name"})
		proc.emit(Event{"type": "extension_ui_request", "id": "ui-4", "method": "editor", "title": "Edit text"})
		eventually(t, "both responses", func() bool { return len(proc.uiResponses()) == 2 })
		settle(t, s)
		jsonEqual(t, proc.uiResponses(), []any{
			map[string]any{"id": "ui-3", "cancelled": true},
			map[string]any{"id": "ui-4", "cancelled": true},
		})
		ups := conn.all()
		if len(ups) != 2 {
			t.Fatalf("%d updates", len(ups))
		}
		if !strings.Contains(str(ups[0].Update["content"].(map[string]any)["text"]), "input UI request is not supported") {
			t.Errorf("update 0 = %v", ups[0].Update)
		}
		if !strings.Contains(str(ups[1].Update["content"].(map[string]any)["text"]), "editor UI request is not supported") {
			t.Errorf("update 1 = %v", ups[1].Update)
		}
	})

	retry := func(ev Event, want string) func(*testing.T) {
		return func(t *testing.T) {
			conn, proc := newFakeConn(), newFakeProc()
			s := newTestSession(cwdNow(t), proc, conn)
			proc.emit(ev)
			settle(t, s)
			ups := conn.all()
			if len(ups) != 1 {
				t.Fatalf("%d updates", len(ups))
			}
			jsonEqual(t, ups[0].Update, chunk(want))
		}
	}
	tw(t, "component/session-events", "PiAcpSession: emits agent_message_chunk for auto_retry_start with attempt/maxAttempts and rounded delay", retry(
		Event{"type": "auto_retry_start", "attempt": 2, "maxAttempts": 5, "delayMs": 2400}, "Retrying (attempt 2/5, waiting 2s)..."))
	tw(t, "component/session-events", "PiAcpSession: formats a positive sub-second auto_retry_start delay as waiting 1s", retry(
		Event{"type": "auto_retry_start", "attempt": 1, "maxAttempts": 3, "delayMs": 1}, "Retrying (attempt 1/3, waiting 1s)..."))
	tw(t, "component/session-events", "PiAcpSession: falls back to a generic retry message when auto_retry_start fields are missing or malformed", retry(
		Event{"type": "auto_retry_start", "attempt": "oops", "maxAttempts": nil, "delayMs": "bad"}, "Retrying..."))
	tw(t, "component/session-events", "PiAcpSession: omits raw errorMessage content from surfaced auto_retry_start status text", retry(
		Event{"type": "auto_retry_start", "attempt": 1, "maxAttempts": 4, "delayMs": 1500, "errorMessage": "provider overloaded: 529"}, "Retrying (attempt 1/4, waiting 2s)..."))
	tw(t, "component/session-events", "PiAcpSession: emits agent_message_chunk for auto_retry_end", retry(
		Event{"type": "auto_retry_end"}, "Retry finished, resuming."))
	tw(t, "component/session-events", "PiAcpSession: emits agent_message_chunk for auto_compaction_start", retry(
		Event{"type": "auto_compaction_start"}, "Context nearing limit, running automatic compaction..."))
	tw(t, "component/session-events", "PiAcpSession: emits agent_message_chunk for auto_compaction_end", retry(
		Event{"type": "auto_compaction_end"}, "Automatic compaction finished; context was summarized to continue the session."))

	t.Run("gap F1: compaction_start and compaction_end (pi 0.84+ and PiG) produce the compaction notices", func(t *testing.T) {
		t.Skip("upstream defect kept as ported: pi-acp handles auto_compaction_start/auto_compaction_end, but Pi 0.84 to 0.99 and PiG 0.3.0 emit " +
			"compaction_start (reason manual, threshold or overflow) and compaction_end, so the two notices never appear against these hosts. " +
			"Fixing it changes observable behavior and needs an owner decision (port/PORT.md, F1). The twins above keep the upstream event names.")
	})

	tw(t, "component/session-events", "PiAcpSession: preserves ordering when auto_retry_start is interleaved with text_delta events", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(textDelta("before "))
		proc.emit(Event{"type": "auto_retry_start", "attempt": 1, "maxAttempts": 2, "delayMs": 2000})
		proc.emit(textDelta("after"))
		settle(t, s)
		var got []Update
		for _, u := range conn.all() {
			got = append(got, u.Update)
		}
		jsonEqual(t, got, []Update{chunk("before "), chunk("Retrying (attempt 1/2, waiting 2s)..."), chunk("after")})
	})

	tw(t, "component/session-events", "PiAcpSession: emits streamed tool locations from pi path args", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "message_update", "assistantMessageEvent": map[string]any{
			"type":     "toolcall_start",
			"toolCall": map[string]any{"id": "t1", "name": "write", "arguments": map[string]any{"path": "/tmp/test.txt", "content": "hello"}},
		}})
		settle(t, s)
		ups := conn.all()
		if len(ups) != 1 || ups[0].Update["sessionUpdate"] != "tool_call" {
			t.Fatalf("updates = %+v", ups)
		}
		jsonEqual(t, ups[0].Update["locations"], []any{map[string]any{"path": "/tmp/test.txt"}})
	})

	editLine := func(file string, args map[string]any, wantLine int) func(*testing.T) {
		return func(t *testing.T) {
			conn, proc := newFakeConn(), newFakeProc()
			cwd := tmp(t)
			path := filepath.Join(cwd, "a.txt")
			if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
				t.Fatal(err)
			}
			s := newTestSession(cwd, proc, conn)
			proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "edit", "args": args})
			settle(t, s)
			ups := conn.all()
			if len(ups) != 1 || ups[0].Update["sessionUpdate"] != "tool_call" {
				t.Fatalf("updates = %+v", ups)
			}
			loc := map[string]any{"path": path}
			if wantLine > 0 {
				loc["line"] = wantLine
			}
			jsonEqual(t, ups[0].Update["locations"], []any{loc})
		}
	}
	tw(t, "component/session-events", "PiAcpSession: emits edit tool line when oldText matches uniquely", editLine("one\ntwo\nneedle\nthree\n",
		map[string]any{"path": "a.txt", "oldText": "needle"}, 3))
	tw(t, "component/session-events", "PiAcpSession: emits edit tool line from edits array when oldText matches uniquely", editLine("one\ntwo\nneedle\nthree\n",
		map[string]any{"path": "a.txt", "edits": []any{map[string]any{"oldText": "needle", "newText": "replacement"}}}, 3))
	tw(t, "component/session-events", "PiAcpSession: emits edit tool line from stringified edits array", editLine("one\ntwo\nneedle\nthree\n",
		map[string]any{"path": "a.txt", "edits": `[{"oldText":"needle","newText":"replacement"}]`}, 3))
	tw(t, "component/session-events", "PiAcpSession: omits edit tool line when oldText matches multiple times", editLine("one\nneedle\ntwo\nneedle\n",
		map[string]any{"path": "a.txt", "oldText": "needle"}, 0))

	tw(t, "component/session-events", "PiAcpSession: prompt stays open through retry runs until agent_settled", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("hello", nil)
		proc.emit(Event{"type": "agent_start"})
		proc.emit(Event{"type": "auto_retry_start", "attempt": 1, "maxAttempts": 3, "delayMs": 2000})
		proc.emit(Event{"type": "agent_end", "willRetry": true})
		settle(t, s)
		select {
		case <-p:
			t.Fatal("resolved before agent_settled")
		default:
		}
		proc.emit(Event{"type": "agent_start"})
		proc.emit(Event{"type": "turn_end"})
		proc.emit(Event{"type": "agent_end", "willRetry": false})
		settle(t, s)
		select {
		case <-p:
			t.Fatal("resolved before agent_settled")
		default:
		}
		proc.emit(Event{"type": "agent_settled"})
		if r := wait(t, p); r.Reason != StopEndTurn {
			t.Fatalf("reason = %v", r)
		}
	})

	tw(t, "component/session-events", "PiAcpSession: does not re-emit startup info on first prompt after it was already sent", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		notice := "New version available: v0.74.0 (installed v0.73.1)."
		s.SetStartupInfo(notice)
		s.SendStartupInfoIfPending()
		settle(t, s)
		p := s.Prompt("hello", nil)
		eventually(t, "the prompt", func() bool { return len(proc.promptList()) == 1 })
		if got := promptsOf(t, proc, 1); got[0].Message != "hello" {
			t.Errorf("prompt = %v", got)
		}
		n := 0
		for _, u := range conn.all() {
			if u.Update["sessionUpdate"] == "agent_message_chunk" {
				if c, _ := u.Update["content"].(map[string]any); c["type"] == "text" && c["text"] == notice {
					n++
				}
			}
		}
		if n != 1 {
			t.Errorf("startup info sent %d times", n)
		}
		proc.emit(Event{"type": "agent_start"})
		proc.emit(Event{"type": "turn_end"})
		proc.emit(Event{"type": "agent_end"})
		proc.emit(Event{"type": "agent_settled"})
		if r := wait(t, p); r.Reason != StopEndTurn {
			t.Fatalf("reason = %v", r)
		}
	})

	tw(t, "component/session-events", "PiAcpSession: cancel flips stopReason to cancelled", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("hello", nil)
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
		settledTurn(proc)
		r := wait(t, p)
		if proc.aborts() != 1 || r.Reason != StopCancelled {
			t.Fatalf("aborts=%d reason=%v", proc.aborts(), r)
		}
	})

	tw(t, "component/session-events", "PiAcpSession: queues concurrent prompt and starts it after agent_settled", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		first := s.Prompt("one", nil)
		second := s.Prompt("two", nil)
		eventually(t, "the first prompt", func() bool { return len(proc.promptList()) >= 1 })
		if got := promptsOf(t, proc, 1); len(got) != 1 || got[0].Message != "one" {
			t.Fatalf("prompts = %v", got)
		}
		settledTurn(proc)
		if r := wait(t, first); r.Reason != StopEndTurn {
			t.Fatalf("first = %v", r)
		}
		eventually(t, "the queued prompt", func() bool { return len(proc.promptList()) == 2 })
		if got := promptsOf(t, proc, 2); got[1].Message != "two" {
			t.Fatalf("prompts = %v", got)
		}
		settledTurn(proc)
		if r := wait(t, second); r.Reason != StopEndTurn {
			t.Fatalf("second = %v", r)
		}
	})

	tw(t, "component/session-events", "PiAcpSession: cancel clears queued prompts", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		first := s.Prompt("one", nil)
		second := s.Prompt("two", nil)
		eventually(t, "the first prompt", func() bool { return len(proc.promptList()) == 1 })
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
		settledTurn(proc)
		r1, r2 := wait(t, first), wait(t, second)
		if r1.Reason != StopCancelled || r2.Reason != StopCancelled {
			t.Fatalf("r1=%v r2=%v", r1, r2)
		}
	})

	tw(t, "component/session-events", "PiAcpSession: expands /command before sending to pi", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn, FileSlashCommand{Name: "hello", Description: "test", Content: "Say hello to $1", Source: "(project)"})
		p := s.Prompt("/hello world", nil)
		eventually(t, "the prompt", func() bool { return len(proc.promptList()) == 1 })
		if got := promptsOf(t, proc, 1); got[0].Message != "Say hello to world" {
			t.Errorf("prompt = %q", got[0].Message)
		}
		settledTurn(proc)
		if r := wait(t, p); r.Reason != StopEndTurn {
			t.Fatalf("reason = %v", r)
		}
	})

	tw(t, "component/session-events", "PiAcpSession: tags extension notify chunks with severity in _meta", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "n1", "method": "notify", "message": "MCP: connection failed", "notifyType": "error"})
		eventually(t, "the acknowledgement", func() bool { return len(proc.uiResponses()) == 1 })
		settle(t, s)
		ups := conn.all()
		if len(ups) != 1 {
			t.Fatalf("%d updates", len(ups))
		}
		jsonEqual(t, ups[0].Update, Update{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "MCP: connection failed"},
			"_meta":         map[string]any{"piAcp": map[string]any{"notify": map[string]any{"level": "error"}}},
		})
		jsonEqual(t, proc.uiResponses()[0], map[string]any{"id": "n1", "cancelled": true})
	})

	tw(t, "component/session-events", "PiAcpSession: defaults notify severity to info when notifyType is absent", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "n2", "method": "notify", "message": "heads up"})
		eventually(t, "the acknowledgement", func() bool { return len(proc.uiResponses()) == 1 })
		settle(t, s)
		ups := conn.all()
		if len(ups) != 1 {
			t.Fatalf("%d updates", len(ups))
		}
		jsonEqual(t, ups[0].Update["_meta"], map[string]any{"piAcp": map[string]any{"notify": map[string]any{"level": "info"}}})
	})
}
