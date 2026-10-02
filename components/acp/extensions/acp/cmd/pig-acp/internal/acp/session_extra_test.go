package acp

// Layer-2 cases for session.ts branches the original's tests do not reach.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func toolcall(kind string, tc map[string]any) Event {
	ame := map[string]any{"type": kind}
	for k, v := range tc {
		ame[k] = v
	}
	return Event{"type": "message_update", "assistantMessageEvent": ame}
}

func TestSessionToolCalls(t *testing.T) {
	t.Run("a streamed tool call starts pending and never goes back after tool_execution_start", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		call := map[string]any{"toolCall": map[string]any{"id": "t1", "name": "read", "arguments": map[string]any{"path": "/a"}}}
		proc.emit(toolcall("toolcall_start", call))
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "read", "args": map[string]any{"path": "/a"}})
		proc.emit(toolcall("toolcall_delta", call)) // late delta after execution started
		settle(t, s)
		var statuses []any
		var kinds []string
		for _, u := range conn.all() {
			statuses = append(statuses, u.Update["status"])
			kinds = append(kinds, str(u.Update["sessionUpdate"]))
		}
		jsonEqual(t, statuses, []any{"pending", "in_progress", "in_progress"})
		jsonEqual(t, kinds, []any{"tool_call", "tool_call_update", "tool_call_update"})
		if conn.all()[0].Update["kind"] != "read" {
			t.Errorf("kind = %v", conn.all()[0].Update["kind"])
		}
	})

	t.Run("streamed arguments come from the partial message when the event has no toolCall", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "message_update", "assistantMessageEvent": map[string]any{
			"type": "toolcall_delta", "contentIndex": 1,
			"partial": map[string]any{"content": []any{
				map[string]any{"type": "text"},
				map[string]any{"type": "toolCall", "id": "t9", "name": "write", "partialArgs": `{"path":"/x"}`}}}}})
		settle(t, s)
		ups := conn.all()
		if len(ups) != 1 || ups[0].Update["toolCallId"] != "t9" || ups[0].Update["kind"] != "edit" {
			t.Fatalf("updates = %+v", ups)
		}
		jsonEqual(t, ups[0].Update["rawInput"], map[string]any{"path": "/x"})
	})

	t.Run("unparseable partial arguments are passed as {partialArgs}", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(toolcall("toolcall_delta", map[string]any{"toolCall": map[string]any{"id": "t1", "name": "bash", "partialArgs": `{"comm`}}))
		settle(t, s)
		ups := conn.all()
		if len(ups) != 1 {
			t.Fatalf("updates = %+v", ups)
		}
		// bash keeps the tool name as the title when there is no command yet.
		if ups[0].Update["title"] != "bash" || ups[0].Update["kind"] != "execute" || ups[0].Update["status"] != "pending" {
			t.Errorf("update = %v", ups[0].Update)
		}
	})

	t.Run("a tool call without an id is ignored", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(toolcall("toolcall_start", map[string]any{"toolCall": map[string]any{"name": "read"}}))
		settle(t, s)
		if n := len(conn.all()); n != 0 {
			t.Errorf("%d updates", n)
		}
	})

	t.Run("a failed tool ends failed with its text and raw output", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "read", "args": map[string]any{"path": "/nope"}})
		proc.emit(Event{"type": "tool_execution_end", "toolCallId": "t1", "isError": true,
			"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "ENOENT"}}}})
		settle(t, s)
		end := conn.all()[1].Update
		if end["status"] != "failed" {
			t.Errorf("status = %v", end["status"])
		}
		jsonEqual(t, end["content"], []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "ENOENT"}}})
		if _, ok := end["rawOutput"]; !ok {
			t.Error("rawOutput missing")
		}
	})

	t.Run("a failed edit gets no diff even when the file changed", func(t *testing.T) {
		dir := tmp(t)
		file := filepath.Join(dir, "a.txt")
		write(t, file, "before\n")
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(dir, proc, conn)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "edit", "args": map[string]any{"path": "a.txt"}})
		write(t, file, "after\n")
		proc.emit(Event{"type": "tool_execution_end", "toolCallId": "t1", "isError": true, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "failed"}}}})
		settle(t, s)
		end := conn.all()[1].Update
		if findDiff(end) != nil || end["status"] != "failed" {
			t.Errorf("end = %v", end)
		}
	})

	t.Run("an unchanged file after edit gives text content, not a diff", func(t *testing.T) {
		dir := tmp(t)
		write(t, filepath.Join(dir, "a.txt"), "same\n")
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(dir, proc, conn)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "edit", "args": map[string]any{"path": "a.txt"}})
		proc.emit(okResult("no change"))
		settle(t, s)
		end := completedToolUpdate(conn, "t1")
		if findDiff(end) != nil {
			t.Error("diff for an unchanged file")
		}
		jsonEqual(t, end["content"], []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "no change"}}})
	})

	t.Run("tool_execution_update on an edit carries no content and no raw output", func(t *testing.T) {
		dir := tmp(t)
		write(t, filepath.Join(dir, "a.txt"), "x\n")
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(dir, proc, conn)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "edit", "args": map[string]any{"path": "a.txt"}})
		proc.emit(Event{"type": "tool_execution_update", "toolCallId": "t1", "partialResult": map[string]any{"content": []any{map[string]any{"type": "text", "text": "partial"}}}})
		settle(t, s)
		u := conn.all()[1].Update
		if _, ok := u["content"]; ok {
			t.Errorf("content = %v", u["content"])
		}
		if _, ok := u["rawOutput"]; ok {
			t.Errorf("rawOutput = %v", u["rawOutput"])
		}
	})

	t.Run("non-bash tool updates carry text and raw output", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "grep", "args": map[string]any{}})
		proc.emit(Event{"type": "tool_execution_update", "toolCallId": "t1", "partialResult": map[string]any{"content": []any{map[string]any{"type": "text", "text": "hit"}}}})
		settle(t, s)
		ups := conn.all()
		if ups[0].Update["kind"] != "other" {
			t.Errorf("kind = %v", ups[0].Update["kind"])
		}
		jsonEqual(t, ups[1].Update["content"], []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "hit"}}})
		if _, ok := ups[1].Update["rawOutput"]; !ok {
			t.Error("rawOutput missing")
		}
	})

	t.Run("bash output is sent as appended deltas, and a failed bash ends with its exit code", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		text := func(v string) map[string]any {
			return map[string]any{"content": []any{map[string]any{"type": "text", "text": v}}}
		}
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "b1", "toolName": "bash", "args": map[string]any{"command": "make"}})
		proc.emit(Event{"type": "tool_execution_update", "toolCallId": "b1", "partialResult": text("one\n")})
		proc.emit(Event{"type": "tool_execution_update", "toolCallId": "b1", "partialResult": text("one\ntwo\n")})
		proc.emit(Event{"type": "tool_execution_end", "toolCallId": "b1", "isError": true, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "one\ntwo\nboom\n"}}, "details": map[string]any{"exitCode": 2}}})
		settle(t, s)
		var data []string
		for _, u := range conn.all()[1:] {
			meta, _ := u.Update["_meta"].(map[string]any)
			if o, ok := meta["terminal_output"].(map[string]any); ok {
				data = append(data, str(o["data"]))
			}
		}
		jsonEqual(t, data, []any{"one\n", "two\n", "boom\n"})
		last := conn.all()[len(conn.all())-1].Update
		if last["status"] != "failed" {
			t.Errorf("status = %v", last["status"])
		}
		jsonEqual(t, last["_meta"].(map[string]any)["terminal_exit"], map[string]any{"terminal_id": "b1", "exit_code": 2, "signal": nil})
	})

	t.Run("a bash tool call that streamed first gets a terminal only once", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		call := map[string]any{"toolCall": map[string]any{"id": "b1", "name": "bash", "arguments": map[string]any{"command": "ls"}}}
		proc.emit(toolcall("toolcall_start", call))
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "b1", "toolName": "bash", "args": map[string]any{"command": "ls"}})
		settle(t, s)
		ups := conn.all()
		if _, ok := ups[0].Update["content"]; !ok {
			t.Error("the first bash update has no terminal content")
		}
		if _, ok := ups[1].Update["content"]; ok {
			t.Error("the second bash update repeats the terminal content")
		}
		if ups[0].Update["sessionUpdate"] != "tool_call" || ups[1].Update["sessionUpdate"] != "tool_call_update" {
			t.Errorf("kinds = %v", conn.kinds())
		}
	})

	t.Run("tool events without an id are ignored", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "tool_execution_update", "partialResult": map[string]any{}})
		proc.emit(Event{"type": "tool_execution_end", "result": map[string]any{}})
		settle(t, s)
		if n := len(conn.all()); n != 0 {
			t.Errorf("%d updates", n)
		}
	})
}

func TestSessionTurns(t *testing.T) {
	t.Run("a turn publishes queue depth around its start and end", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("hello", nil)
		settledTurn(proc)
		wait(t, p)
		var metas []any
		for _, u := range conn.ofKind("session_info_update") {
			metas = append(metas, u.Update["_meta"])
		}
		jsonEqual(t, metas, []any{
			map[string]any{"piAcp": map[string]any{"queueDepth": 0, "running": true}},
			map[string]any{"piAcp": map[string]any{"queueDepth": 0, "running": false}}})
	})

	t.Run("a queued prompt is announced, then started with a remaining count", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		first := s.Prompt("one", nil)
		second := s.Prompt("two", nil)
		settledTurn(proc)
		wait(t, first)
		eventually(t, "the second prompt", func() bool { return len(proc.promptList()) == 2 })
		settledTurn(proc)
		wait(t, second)
		var texts []string
		for _, u := range conn.ofKind("agent_message_chunk") {
			texts = append(texts, str(u.Update["content"].(map[string]any)["text"]))
		}
		jsonEqual(t, texts, []any{"Queued message (position 1).", "Starting queued message. (0 remaining)"})
	})

	t.Run("cancel with queued prompts says the queue was cleared", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		first := s.Prompt("one", nil)
		s.Prompt("two", nil)
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
		settledTurn(proc)
		wait(t, first)
		found := false
		for _, u := range conn.ofKind("agent_message_chunk") {
			if u.Update["content"].(map[string]any)["text"] == "Cleared queued prompts." {
				found = true
			}
		}
		if !found {
			t.Error("no 'Cleared queued prompts.' notice")
		}
	})

	t.Run("cancel with nothing running still aborts pi and is harmless", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
		if proc.aborts() != 1 || len(conn.all()) != 0 {
			t.Errorf("aborts=%d updates=%v", proc.aborts(), conn.all())
		}
	})

	t.Run("a prompt pi rejects ends as an error turn and clears the queue depth", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		proc.promptErr = errors.New("pi prompt failed: boom")
		s := newTestSession(cwdNow(t), proc, conn)
		r := wait(t, s.Prompt("hi", nil))
		if r.Reason != StopError || r.Err != nil {
			t.Fatalf("r = %+v", r)
		}
		s.flushEmits()
		last := conn.ofKind("session_info_update")
		if len(last) == 0 {
			t.Fatal("no session_info_update")
		}
		jsonEqual(t, last[len(last)-1].Update["_meta"], map[string]any{"piAcp": map[string]any{"queueDepth": 0, "running": false}})
	})

	t.Run("a prompt pi rejects for lack of credentials is AUTH_REQUIRED", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		proc.promptErr = errors.New("pi prompt failed: No API key found for provider")
		s := newTestSession(cwdNow(t), proc, conn)
		r := wait(t, s.Prompt("hi", nil))
		re, ok := r.Err.(*RequestError)
		if !ok || re.Code != -32000 {
			t.Fatalf("r = %+v", r)
		}
	})

	t.Run("a cancelled prompt that pi rejects reports cancelled", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		release := make(chan struct{})
		proc.promptErr = errors.New("aborted")
		proc.promptHook = func() { <-release }
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("hi", nil)
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
		close(release)
		if r := wait(t, p); r.Reason != StopCancelled {
			t.Errorf("r = %+v", r)
		}
	})

	t.Run("the next turn starts with the cancel flag cleared", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("one", nil)
		s.Cancel()
		settledTurn(proc)
		wait(t, p)
		p2 := s.Prompt("two", nil)
		if s.WasCancelRequested() {
			t.Error("cancel flag leaked into the next turn")
		}
		settledTurn(proc)
		if r := wait(t, p2); r.Reason != StopEndTurn {
			t.Errorf("r = %+v", r)
		}
	})

	t.Run("startup info is sent once, on the first prompt when not sent earlier", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		s.SetStartupInfo("hello startup")
		p := s.Prompt("go", nil)
		settledTurn(proc)
		wait(t, p)
		n := 0
		for _, u := range conn.ofKind("agent_message_chunk") {
			if u.Update["content"].(map[string]any)["text"] == "hello startup" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("startup info sent %d times", n)
		}
	})
}

func TestSessionExtensionUI(t *testing.T) {
	t.Run("a select without options is cancelled without asking the client", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "u1", "method": "select", "title": "x", "options": []any{}})
		eventually(t, "the response", func() bool { return len(proc.uiResponses()) == 1 })
		jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "u1", "cancelled": true}})
		if n := len(conn.permissions()); n != 0 {
			t.Errorf("%d permission requests", n)
		}
	})
	t.Run("a cancelled select answers cancelled, a foreign option id too", func(t *testing.T) {
		for _, outcome := range []PermissionOutcome{{Outcome: "cancelled"}, {Outcome: "selected", OptionID: "allow"}, {Outcome: "selected", OptionID: "choice-9"}, {Outcome: "selected", OptionID: "choice-01"}} {
			conn, proc := newFakeConn(), newFakeProc()
			conn.nextPermission = PermissionResponse{Outcome: outcome}
			newTestSession(cwdNow(t), proc, conn)
			proc.emit(Event{"type": "extension_ui_request", "id": "u1", "method": "select", "title": "x", "options": []any{"A"}})
			eventually(t, "the response", func() bool { return len(proc.uiResponses()) == 1 })
			jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "u1", "cancelled": true}}, outcome)
		}
	})
	t.Run("a failing permission request cancels the dialog", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		failing := &failingPermissionConn{fakeConn: conn}
		newTestSession(cwdNow(t), proc, failing)
		proc.emit(Event{"type": "extension_ui_request", "id": "u1", "method": "confirm", "title": "x"})
		eventually(t, "the response", func() bool { return len(proc.uiResponses()) == 1 })
		jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "u1", "cancelled": true}})
	})
	t.Run("confirm yes answers confirmed true", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		conn.nextPermission = PermissionResponse{Outcome: PermissionOutcome{Outcome: "selected", OptionID: "yes"}}
		newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "u1", "method": "confirm", "title": "x"})
		eventually(t, "the response", func() bool { return len(proc.uiResponses()) == 1 })
		jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "u1", "confirmed": true}})
	})
	t.Run("a fire-and-forget or unknown method is cancelled, and a request without id is dropped", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "u1", "method": "setStatus", "statusKey": "k"})
		proc.emit(Event{"type": "extension_ui_request", "method": "select", "options": []any{"A"}})
		eventually(t, "the response", func() bool { return len(proc.uiResponses()) == 1 })
		time.Sleep(20 * time.Millisecond)
		jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "u1", "cancelled": true}})
	})
	t.Run("a notify without a message says Pi notification", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "n", "method": "notify"})
		eventually(t, "the ack", func() bool { return len(proc.uiResponses()) == 1 })
		settle(t, s)
		if got := conn.all()[0].Update["content"].(map[string]any)["text"]; got != "Pi notification" {
			t.Errorf("text = %v", got)
		}
	})
	t.Run("the permission request carries method, title, message, options, placeholder and prefill as rawInput", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "u1", "method": "confirm", "title": "T", "message": "M", "placeholder": "P", "prefill": "F", "extra": "no"})
		eventually(t, "the request", func() bool { return len(conn.permissions()) == 1 })
		jsonEqual(t, conn.permissions()[0].ToolCall["rawInput"], map[string]any{"method": "confirm", "title": "T", "message": "M", "placeholder": "P", "prefill": "F"})
		if conn.permissions()[0].ToolCall["title"] != "T" {
			t.Errorf("title = %v", conn.permissions()[0].ToolCall["title"])
		}
	})
	t.Run("a request without a title is titled after its method", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "u1", "method": "confirm"})
		eventually(t, "the request", func() bool { return len(conn.permissions()) == 1 })
		if conn.permissions()[0].ToolCall["title"] != "Pi confirm" {
			t.Errorf("title = %v", conn.permissions()[0].ToolCall["title"])
		}
	})
}

type failingPermissionConn struct{ *fakeConn }

func (f *failingPermissionConn) RequestPermission(PermissionRequest) (PermissionResponse, error) {
	return PermissionResponse{}, errors.New("client went away")
}

func TestSessionManager(t *testing.T) {
	spawnBy := func(procs ...*fakeProc) (SpawnFunc, *[]SpawnParams) {
		var got []SpawnParams
		i := 0
		return func(p SpawnParams) (Proc, error) {
			got = append(got, p)
			pr := procs[i]
			i++
			return pr, nil
		}, &got
	}
	stateWith := func(id, file string) *fakeProc {
		p := newFakeProc()
		p.getStateFn = func() (map[string]any, error) { return map[string]any{"sessionId": id, "sessionFile": file}, nil }
		return p
	}
	t.Run("Create spawns a child in the cwd, takes the id from pi, and records the file", func(t *testing.T) {
		store := newMemStore()
		spawn, got := spawnBy(stateWith("sid", "/f/s.jsonl"))
		m := NewSessionManager(spawn, store)
		s, err := m.Create(SessionCreateParams{Cwd: "/w", Conn: newFakeConn(), PiCommand: "pigx"})
		if err != nil || s.ID() != "sid" {
			t.Fatalf("s=%v err=%v", s, err)
		}
		if len(*got) != 1 || (*got)[0] != (SpawnParams{Cwd: "/w", PiCommand: "pigx"}) {
			t.Errorf("spawns = %+v", *got)
		}
		if e := store.Get("sid"); e == nil || e.SessionFile != "/f/s.jsonl" || e.Cwd != "/w" {
			t.Errorf("store = %+v", e)
		}
		if m.MaybeGet("sid") != s {
			t.Error("not registered")
		}
	})
	t.Run("Create falls back to a random id when pi gives none, and stores nothing without a file", func(t *testing.T) {
		store := newMemStore()
		p := newFakeProc()
		p.getStateFn = func() (map[string]any, error) { return nil, errors.New("no state") }
		spawn, _ := spawnBy(p)
		s, err := NewSessionManager(spawn, store).Create(SessionCreateParams{Cwd: "/w", Conn: newFakeConn()})
		if err != nil || len(s.ID()) < 16 || len(store.upserts) != 0 {
			t.Fatalf("id=%q upserts=%v err=%v", s.ID(), store.upserts, err)
		}
	})
	t.Run("a spawn error becomes an internal error with the code", func(t *testing.T) {
		m := NewSessionManager(func(SpawnParams) (Proc, error) {
			return nil, &spawnFailure{code: "ENOENT", msg: "Could not start pig: executable not found (command: pig)."}
		}, newMemStore())
		_, err := m.Create(SessionCreateParams{Cwd: "/w", Conn: newFakeConn()})
		re, ok := err.(*RequestError)
		if !ok || re.Code != -32603 || re.Data.(map[string]any)["code"] != "ENOENT" {
			t.Errorf("err = %#v", err)
		}
	})
	t.Run("Get of an unknown id is invalid params; Close disposes the child once", func(t *testing.T) {
		p := stateWith("a", "")
		spawn, _ := spawnBy(p)
		m := NewSessionManager(spawn, newMemStore())
		if _, err := m.Get("zz"); err == nil || err.(*RequestError).Code != -32602 {
			t.Errorf("err = %v", err)
		}
		m.Create(SessionCreateParams{Cwd: "/w", Conn: newFakeConn()})
		m.Close("a")
		m.Close("a")
		if p.disposeCount() != 1 || m.MaybeGet("a") != nil {
			t.Errorf("disposed %d, registered %v", p.disposeCount(), m.MaybeGet("a"))
		}
	})
	t.Run("CloseAllExcept keeps one session; DisposeAll closes the rest", func(t *testing.T) {
		a, b := stateWith("a", ""), stateWith("b", "")
		spawn, _ := spawnBy(a, b)
		m := NewSessionManager(spawn, newMemStore())
		m.Create(SessionCreateParams{Cwd: "/w", Conn: newFakeConn()})
		m.Create(SessionCreateParams{Cwd: "/w", Conn: newFakeConn()})
		m.CloseAllExcept("b")
		if a.disposeCount() != 1 || b.disposeCount() != 0 {
			t.Errorf("a=%d b=%d", a.disposeCount(), b.disposeCount())
		}
		m.DisposeAll()
		if b.disposeCount() != 1 {
			t.Errorf("b=%d", b.disposeCount())
		}
	})
	t.Run("GetOrCreate returns the registered session for an id it already has", func(t *testing.T) {
		m := NewSessionManager(nil, newMemStore())
		s1 := m.GetOrCreate("x", SessionCreateParams{Cwd: "/w", Conn: newFakeConn(), Proc: newFakeProc()})
		s2 := m.GetOrCreate("x", SessionCreateParams{Cwd: "/other", Conn: newFakeConn(), Proc: newFakeProc()})
		if s1 != s2 || s1.Cwd() != "/w" {
			t.Errorf("s1=%v s2=%v", s1, s2)
		}
	})
}

type spawnFailure struct{ code, msg string }

func (e *spawnFailure) Error() string     { return e.msg }
func (e *spawnFailure) SpawnCode() string { return e.code }

var _ = os.Stat
