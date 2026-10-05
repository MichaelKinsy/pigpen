package acp

// Cases added after the first mutation run (port/mutations.json) showed that the ported twins left
// a defect alive. They are not twins of upstream tests; each names the mutant it kills.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T) (*Agent, *fakeConn, *memStore, *fakeSessions, *fakeProc, string) {
	t.Helper()
	cwd := cwdNow(t)
	proc := newFakeProc()
	proc.getStateFn = func() (map[string]any, error) { return map[string]any{"thinkingLevel": "medium"}, nil }
	proc.getModelsFn = func() (map[string]any, error) { return map[string]any{"models": []any{}}, nil }
	conn := newFakeConn()
	a, _ := testAgent(conn)
	store := newMemStore(StoredSession{SessionID: "s1", Cwd: cwd, SessionFile: "/tmp/s.jsonl"})
	a.store = store
	sessions := &fakeSessions{session: &fakeSession{id: "s1", cwd: cwd, proc: proc}, anyID: true}
	a.sessions = sessions
	return a, conn, store, sessions, proc, cwd
}

func TestMutationExtras(t *testing.T) {
	t.Run("a session/new whose get_state fails with a credentials error is AUTH_REQUIRED [state-auth-error-ignored]", func(t *testing.T) {
		proc := newFakeProc()
		proc.getStateFn = func() (map[string]any, error) { return nil, errors.New("pi get_state failed: No API key found") }
		a, _ := testAgent(newFakeConn())
		a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}}
		_, err := a.NewSession(newSessionReq(cwdNow(t)))
		if re, ok := err.(*RequestError); !ok || re.Code != -32000 {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("session/load closes a live copy of the session first [load-does-not-close-live-copy]", func(t *testing.T) {
		a, _, _, sessions, _, cwd := loadFixture(t)
		if _, err := a.LoadSession(LoadSessionRequest{SessionID: "s1", Cwd: cwd}); err != nil {
			t.Fatal(err)
		}
		if len(sessions.closeCalls) == 0 || sessions.closeCalls[0] != "s1" {
			t.Errorf("closeCalls = %v", sessions.closeCalls)
		}
	})

	t.Run("session/load refreshes the session map with the cwd it was loaded with [load-does-not-refresh-map]", func(t *testing.T) {
		a, _, store, _, _, cwd := loadFixture(t)
		if _, err := a.LoadSession(LoadSessionRequest{SessionID: "s1", Cwd: cwd}); err != nil {
			t.Fatal(err)
		}
		if len(store.upserts) == 0 || store.upserts[len(store.upserts)-1] != (StoredSession{SessionID: "s1", Cwd: cwd, SessionFile: "/tmp/s.jsonl"}) {
			t.Errorf("upserts = %+v", store.upserts)
		}
	})

	t.Run("a replayed failed tool result is a failed tool_call_update [tool-history-not-replayed-as-error]", func(t *testing.T) {
		a, conn, _, _, proc, cwd := loadFixture(t)
		proc.getMessagesFn = func() (map[string]any, error) {
			return map[string]any{"messages": []any{
				map[string]any{"role": "toolResult", "toolCallId": "c1", "toolName": "read", "isError": true, "content": []any{map[string]any{"type": "text", "text": "no such file"}}}}}, nil
		}
		if _, err := a.LoadSession(LoadSessionRequest{SessionID: "s1", Cwd: cwd}); err != nil {
			t.Fatal(err)
		}
		up := conn.ofKind("tool_call_update")
		if len(up) != 1 || up[0].Update["status"] != "failed" {
			t.Fatalf("updates = %+v", up)
		}
	})

	t.Run("the idle notice reaches the client before the prompt result [idle-notice-not-flushed-before-response]", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		conn.sessionUpdateHook = func(u Update) {
			if u["sessionUpdate"] == "session_info_update" {
				time.Sleep(60 * time.Millisecond)
			}
		}
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("hello", nil)
		settledTurn(proc)
		wait(t, p)
		if n := len(conn.ofKind("session_info_update")); n != 2 {
			t.Errorf("%d session_info_update at the moment the turn resolved, want 2", n)
		}
	})

	t.Run("a select answer with a leading-zero option id is cancelled [select-index-leading-zero-accepted]", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		conn.nextPermission = PermissionResponse{Outcome: PermissionOutcome{Outcome: "selected", OptionID: "choice-01"}}
		newTestSession(cwdNow(t), proc, conn)
		proc.emit(Event{"type": "extension_ui_request", "id": "u1", "method": "select", "title": "x", "options": []any{"A", "B"}})
		eventually(t, "the response", func() bool { return len(proc.uiResponses()) == 1 })
		jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "u1", "cancelled": true}})
	})

	t.Run("session files are listed most recent first [sessions-oldest-first]", func(t *testing.T) {
		root := t.TempDir()
		withAgentDir(t, root)
		for id, ts := range map[string]string{"old": "2026-01-01T00:00:00.000Z", "new": "2026-03-01T00:00:00.000Z", "mid": "2026-02-01T00:00:00.000Z"} {
			writeSessionFile(t, filepath.Join(root, "sessions", "--x--", id+".jsonl"), sessionHeader(id, "/cwd", ts),
				jl(map[string]any{"type": "message", "id": "m1", "timestamp": ts, "message": map[string]any{"role": "user", "content": "hi"}}))
		}
		var ids []string
		for _, s := range ListPiSessions() {
			ids = append(ids, s.SessionID)
		}
		if strings.Join(ids, ",") != "new,mid,old" {
			t.Errorf("order = %v", ids)
		}
	})
}

func TestBase64ByteLength(t *testing.T) {
	t.Run("byte lengths of padded base64 data [base64-length-padding]", func(t *testing.T) {
		for in, want := range map[string]int{"": 0, "YQ==": 1, "YWI=": 2, "YWJj": 3, "YWJjZA==": 4} {
			if got := base64ByteLength(in); got != want {
				t.Errorf("%q: got %d, want %d", in, got, want)
			}
		}
	})
}

func TestExportNeedsMessages(t *testing.T) {
	t.Run("/export with a session file but no messages says there is nothing to export [export-empty-session-exported]", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "s.jsonl")
		write(t, file, "{\"type\":\"session\"}\n")
		proc := newFakeProc()
		proc.getStateFn = func() (map[string]any, error) {
			return map[string]any{"sessionFile": file, "messageCount": 0}, nil
		}
		a, conn := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, "/export")
		if got := lastChunkText(t, conn); !strings.HasPrefix(got, "Nothing to export yet (no session messages)") {
			t.Errorf("text = %q", got)
		}
	})
}
