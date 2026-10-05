package acp

// Twins of test/component/session-list-and-load.test.ts, session-load-toolresult.test.ts,
// session-list-scoped.test.ts, session-list-custom-session-dir.test.ts,
// session-title-long-session.test.ts, session-updatedAt-message-only.test.ts and
// test/unit/startup-info-*.test.ts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func jl(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func sessionHeader(id, cwd, ts string) string {
	return jl(map[string]any{"type": "session", "version": 3, "id": id, "timestamp": ts, "cwd": cwd})
}

func TestListAndLoad(t *testing.T) {
	tw(t, "component/session-list-and-load", "PiAcpAgent: listSessions lists pi sessions and loadSession replays history", func(t *testing.T) {
		root := t.TempDir()
		sessionFile := filepath.Join(root, "sessions", "--tmp--project--", "0000_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jsonl")
		writeSessionFile(t, sessionFile,
			sessionHeader("sess-1", "/tmp/project", "2026-02-11T00:00:00.000Z"),
			jl(map[string]any{"type": "message", "id": "a1b2c3d4", "parentId": nil, "timestamp": "2026-02-11T00:00:01.000Z", "message": map[string]any{"role": "user", "content": "Hello"}}),
			jl(map[string]any{"type": "message", "id": "b2c3d4e5", "parentId": "a1b2c3d4", "timestamp": "2026-02-11T00:00:02.000Z", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Hi there!"}}}}),
			jl(map[string]any{"type": "session_info", "id": "c3d4e5f6", "parentId": "b2c3d4e5", "timestamp": "2026-02-11T00:00:03.000Z", "name": "My Named Session"}))
		withAgentDir(t, root)
		conn := newFakeConn()
		a, sch := testAgent(conn)
		a.store = NewFileStore(filepath.Join(root, "map.json"))

		listed, err := a.ListSessions(ListSessionsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var found *SessionInfo
		for i := range listed.Sessions {
			if listed.Sessions[i].SessionID == "sess-1" {
				found = &listed.Sessions[i]
			}
		}
		if found == nil || found.Cwd != "/tmp/project" || found.Title == nil || *found.Title != "My Named Session" {
			t.Fatalf("listed = %+v", listed.Sessions)
		}

		proc := newFakeProc()
		proc.getMessagesFn = func() (map[string]any, error) {
			return map[string]any{"messages": []any{
				map[string]any{"role": "user", "content": "Hello"},
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Hi there!"}}}}}, nil
		}
		proc.getLevelsFn = func() ([]string, error) { return []string{"medium"}, nil }
		proc.getModelsFn = func() (map[string]any, error) { return map[string]any{"models": []any{}}, nil }
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"thinkingLevel": "medium"}, nil }
		proc.sessionStats = SessionStats{"contextUsage": map[string]any{"tokens": 12345, "contextWindow": 200000}}
		a.spawn = func(p SpawnParams) (Proc, error) {
			if !strings.HasSuffix(p.SessionPath, "/0000_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jsonl") {
				t.Errorf("session path = %q", p.SessionPath)
			}
			return proc, nil
		}
		a.sessions = NewSessionManager(a.spawn, a.store)
		if _, err := a.LoadSession(LoadSessionRequest{SessionID: "sess-1", Cwd: "/tmp/project", McpServers: []any{}}); err != nil {
			t.Fatal(err)
		}
		hasKind := func(kind, text string) bool {
			for _, u := range conn.ofKind(kind) {
				if c, _ := u.Update["content"].(map[string]any); c["text"] == text {
					return true
				}
			}
			return false
		}
		if !hasKind("user_message_chunk", "Hello") || !hasKind("agent_message_chunk", "Hi there!") {
			t.Errorf("history not replayed: %v", conn.kinds())
		}
		if len(usageUpdates(conn)) != 0 {
			t.Error("usage published before session/load returned")
		}
		sch.drain()
		got := conn.ofKind("usage_update")
		if len(got) != 1 || got[0].SessionID != "sess-1" {
			t.Fatalf("usage updates = %+v", got)
		}
		jsonEqual(t, got[0].Update, map[string]any{"sessionUpdate": "usage_update", "used": 12345, "size": 200000})
	})

	tw(t, "component/session-load-toolresult", "PiAcpAgent: loadSession replays toolResult as tool_call + tool_call_update", func(t *testing.T) {
		proc := newFakeProc()
		proc.getMessagesFn = func() (map[string]any, error) {
			return map[string]any{"messages": []any{map[string]any{
				"role": "toolResult", "toolCallId": "call_1", "toolName": "bash", "args": map[string]any{"command": "echo hello"},
				"content": []any{map[string]any{"type": "text", "text": "hello from bash"}}, "isError": false}}}, nil
		}
		proc.getLevelsFn = func() ([]string, error) { return []string{"medium"}, nil }
		proc.getModelsFn = func() (map[string]any, error) { return map[string]any{"models": []any{}}, nil }
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"thinkingLevel": "medium"}, nil }
		conn := newFakeConn()
		a, _ := testAgent(conn)
		a.store = newMemStore(StoredSession{SessionID: "s1", Cwd: "/tmp/project", SessionFile: "/tmp/s.jsonl"})
		a.spawn = func(SpawnParams) (Proc, error) { return proc, nil }
		a.sessions = NewSessionManager(a.spawn, a.store)
		if _, err := a.LoadSession(LoadSessionRequest{SessionID: "s1", Cwd: "/tmp/project", McpServers: []any{}}); err != nil {
			t.Fatal(err)
		}
		tc := conn.ofKind("tool_call")
		tu := conn.ofKind("tool_call_update")
		if len(tc) == 0 || len(tu) == 0 {
			t.Fatalf("kinds = %v", conn.kinds())
		}
		u := tc[0].Update
		if u["toolCallId"] != "call_1" || u["title"] != "echo hello" || u["kind"] != "execute" {
			t.Errorf("tool_call = %v", u)
		}
		jsonEqual(t, u["content"], []any{map[string]any{"type": "terminal", "terminalId": "call_1"}})
		jsonEqual(t, u["_meta"], map[string]any{"terminal_info": map[string]any{"terminal_id": "call_1", "cwd": "/tmp/project"}})
		if _, ok := u["rawOutput"]; ok {
			t.Error("rawOutput must be undefined")
		}
		v := tu[0].Update
		if v["toolCallId"] != "call_1" || v["status"] != "completed" {
			t.Errorf("tool_call_update = %v", v)
		}
		jsonEqual(t, v["_meta"], map[string]any{
			"terminal_output": map[string]any{"terminal_id": "call_1", "data": "hello from bash"},
			"terminal_exit":   map[string]any{"terminal_id": "call_1", "exit_code": 0, "signal": nil}})
		if _, ok := v["rawOutput"]; ok {
			t.Error("rawOutput must be undefined")
		}
	})
}

func TestListPiSessions(t *testing.T) {
	tw(t, "component/session-list-scoped", "PiAcpAgent: listSessions defaults to lastSessionCwd when cwd param is omitted", func(t *testing.T) {
		root := t.TempDir()
		for name, id := range map[string]string{"a": "sess-a", "b": "sess-b"} {
			writeSessionFile(t, filepath.Join(root, "sessions", "--"+name+"--", strings.ToUpper(name)+".jsonl"),
				sessionHeader(id, "/cwd/"+name, "2026-01-01T00:00:00.000Z"),
				jl(map[string]any{"type": "session_info", "id": name + "1b2c3d4", "parentId": nil, "timestamp": "2026-01-01T00:00:01.000Z", "name": strings.ToUpper(name)}))
		}
		withAgentDir(t, root)
		a, _ := testAgent(newFakeConn())
		a.lastSessionCwd = "/cwd/a"
		listed, err := a.ListSessions(ListSessionsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed.Sessions) != 1 || listed.Sessions[0].SessionID != "sess-a" {
			t.Fatalf("sessions = %+v", listed.Sessions)
		}
	})

	tw(t, "component/session-list-custom-session-dir", "listPiSessions: respects sessionDir from pi settings.json", func(t *testing.T) {
		root := t.TempDir()
		custom := filepath.Join(root, "somewhere-else", "--p--")
		write(t, filepath.Join(root, "settings.json"), jl(map[string]any{"sessionDir": filepath.Join(root, "somewhere-else")}))
		writeSessionFile(t, filepath.Join(custom, "s.jsonl"),
			sessionHeader("sess-custom", "/tmp/project", "2026-01-01T00:00:00.000Z"),
			jl(map[string]any{"type": "message", "id": "m1", "parentId": nil, "timestamp": "2026-01-01T00:00:01.000Z", "message": map[string]any{"role": "user", "content": "hi"}}))
		withAgentDir(t, root)
		var got *PiSessionListItem
		for _, s := range ListPiSessions() {
			if s.SessionID == "sess-custom" {
				s := s
				got = &s
			}
		}
		if got == nil || got.SessionFile != filepath.Join(custom, "s.jsonl") {
			t.Fatalf("got %+v", got)
		}
	})

	tw(t, "component/session-title-long-session", "listPiSessions: finds session_info.name even when it is outside the tail window", func(t *testing.T) {
		root := t.TempDir()
		header := sessionHeader("sess-1", "/tmp/project", "2026-01-01T00:00:00.000Z")
		info := jl(map[string]any{"type": "session_info", "id": "i1", "parentId": nil, "timestamp": "2026-01-01T00:00:01.000Z", "name": "Named Early"})
		filler := jl(map[string]any{"type": "message", "id": "m", "parentId": nil, "timestamp": "2026-01-01T00:00:02.000Z", "message": map[string]any{"role": "user", "content": strings.Repeat("x", 2000)}})
		lines := []string{header, info}
		for i := 0; i < 400; i++ {
			lines = append(lines, filler)
		}
		writeSessionFile(t, filepath.Join(root, "sessions", "--p--", "s.jsonl"), lines...)
		withAgentDir(t, root)
		var got *PiSessionListItem
		for _, s := range ListPiSessions() {
			if s.SessionID == "sess-1" {
				s := s
				got = &s
			}
		}
		if got == nil || got.Title == nil || *got.Title != "Named Early" {
			t.Fatalf("got %+v", got)
		}
	})

	tw(t, "component/session-updatedAt-message-only", "listPiSessions: updatedAt prefers last message timestamp over later non-message entries", func(t *testing.T) {
		root := t.TempDir()
		writeSessionFile(t, filepath.Join(root, "sessions", "--p--", "s.jsonl"),
			sessionHeader("sess-1", "/tmp/project", "2026-01-01T00:00:00.000Z"),
			jl(map[string]any{"type": "message", "id": "a1b2c3d4", "parentId": nil, "timestamp": "2026-01-01T00:00:02.000Z", "message": map[string]any{"role": "user", "content": "hi"}}),
			jl(map[string]any{"type": "session_info", "id": "b1b2c3d4", "parentId": "a1b2c3d4", "timestamp": "2026-01-01T00:00:10.000Z", "name": "named"}))
		withAgentDir(t, root)
		var matches []PiSessionListItem
		for _, s := range ListPiSessions() {
			if s.SessionID == "sess-1" {
				matches = append(matches, s)
			}
		}
		if len(matches) != 1 || matches[0].UpdatedAt == nil || *matches[0].UpdatedAt != "2026-01-01T00:00:02.000Z" {
			t.Fatalf("matches = %+v", matches)
		}
	})
}

func TestStartupInfo(t *testing.T) {
	agentWithSession := func(t *testing.T, cwd string, proc *fakeProc) (*Agent, *scheduler, *fakeSession) {
		conn := newFakeConn()
		sess := &fakeSession{id: "s1", cwd: cwd, proc: proc}
		a, sch := testAgent(conn)
		a.sessions = &fakeSessions{session: sess}
		return a, sch, sess
	}
	basicProc := func() *fakeProc {
		p := newFakeProc()
		p.getLevelsFn = func() ([]string, error) { return allLevels, nil }
		p.getModelsFn = func() (map[string]any, error) { return models([3]string{"test", "model", "model"}), nil }
		p.getStateFn = func() (map[string]any, error) {
			return map[string]any{"thinkingLevel": "medium", "model": map[string]any{"provider": "test", "id": "model"}}, nil
		}
		p.getCommandsFn = func() (map[string]any, error) { return map[string]any{"commands": []any{}}, nil }
		return p
	}

	tw(t, "unit/startup-info-env", "PiAcpAgent: quietStartup=true disables startup info generation/emission", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "settings.json"), jl(map[string]any{"quietStartup": true}))
		withAgentDir(t, dir)
		a, sch, sess := agentWithSession(t, cwdNow(t), basicProc())
		res, err := a.NewSession(newSessionReq(cwdNow(t)))
		if err != nil {
			t.Fatal(err)
		}
		// PiG has no npm update check (`pig update` uses its own signed manifest), so the
		// "New version available" branch of the original does not exist: see the skipped twin.
		if startupInfoOf(res.Meta) != nil {
			t.Errorf("startupInfo = %v", res.Meta)
		}
		if len(sess.startupInfo) != 0 {
			t.Error("setStartupInfo was called")
		}
		if sch.count() != 1 {
			t.Errorf("%d scheduled tasks, want the available_commands_update only", sch.count())
		}
	})

	t.Run("PiAcpAgent: quietStartup=true still surfaces a New version available notice", func(t *testing.T) {
		t.Skip("gap: pi-acp asks the npm registry for a newer pi (`npm view @earendil-works/pi-coding-agent version`) and surfaces the " +
			"notice even with quietStartup. PiG updates through `pig update` and a signed release manifest, not npm; an editor adapter " +
			"must not add a network lookup to session/new (PIG_OFFLINE). The adapter emits no update notice. Owner decision if wanted.")
	})

	tw(t, "unit/startup-info-load-session", "PiAcpAgent: does not emit startup info on loadSession", func(t *testing.T) {
		proc := newFakeProc()
		proc.getLevelsFn = func() ([]string, error) { return []string{"medium"}, nil }
		proc.getModelsFn = func() (map[string]any, error) { return map[string]any{"models": []any{}}, nil }
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"thinkingLevel": "medium"}, nil }
		conn := newFakeConn()
		a, sch := testAgent(conn)
		a.store = newMemStore(StoredSession{SessionID: "s1", Cwd: "/tmp/project", SessionFile: "/tmp/s.jsonl"})
		a.spawn = func(SpawnParams) (Proc, error) { return proc, nil }
		a.sessions = NewSessionManager(a.spawn, a.store)
		res, err := a.LoadSession(LoadSessionRequest{SessionID: "s1", Cwd: "/tmp/project", McpServers: []any{}})
		if err != nil {
			t.Fatal(err)
		}
		if startupInfoOf(res.Meta) != nil {
			t.Errorf("startupInfo = %v", res.Meta)
		}
		if sch.count() != 1 {
			t.Errorf("%d scheduled tasks, want the available_commands_update only", sch.count())
		}
	})

	tw(t, "unit/startup-info-project-packages", "PiAcpAgent: startup info includes project-level packages from .pi/settings.json", func(t *testing.T) {
		agentDir := t.TempDir()
		write(t, filepath.Join(agentDir, "settings.json"), jl(map[string]any{"packages": []string{"npm:global-ext"}}))
		withAgentDir(t, agentDir)
		project := t.TempDir()
		// PiG's project directory is .pig (the original reads .pi).
		if err := os.MkdirAll(filepath.Join(project, ".pig"), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(project, ".pig", "settings.json"), jl(map[string]any{"packages": []string{"/path/to/local-extension"}}))
		a, _, _ := agentWithSession(t, project, basicProc())
		res, err := a.NewSession(newSessionReq(project))
		if err != nil {
			t.Fatal(err)
		}
		info, _ := startupInfoOf(res.Meta).(string)
		if !strings.Contains(info, "npm:global-ext") || !strings.Contains(info, "/path/to/local-extension") {
			t.Errorf("startup info = %q", info)
		}
	})

	t.Run("PiAcpAgent: session/new startup info is sent after the response, not before", func(t *testing.T) {
		a, sch, sess := agentWithSession(t, cwdNow(t), basicProc())
		if _, err := a.NewSession(newSessionReq(cwdNow(t))); err != nil {
			t.Fatal(err)
		}
		if sess.startupSent != 0 {
			t.Error("startup info was sent before the response")
		}
		sch.drain()
		if sess.startupSent != 1 {
			t.Errorf("startup info sent %d times after the response", sess.startupSent)
		}
	})
	_ = time.Second
}

// startupInfoOf reads _meta.piAcp.startupInfo without panicking on a missing _meta.
func startupInfoOf(meta map[string]any) any {
	m, _ := meta["piAcp"].(map[string]any)
	return m["startupInfo"]
}
