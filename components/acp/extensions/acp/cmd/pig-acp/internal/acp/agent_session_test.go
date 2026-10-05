package acp

// Twins of test/unit/session-restore.test.ts, session-delete.test.ts, new-session-*.test.ts,
// startup-info-*.test.ts and the load-configuration cases of model-thinking-levels.test.ts.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func headerLine(id, cwd string) string {
	return fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":"2026-06-16T00:00:00.000Z","cwd":%q}`, id, cwd)
}

func TestSessionRestore(t *testing.T) {
	tw(t, "unit/session-restore", "PiAcpAgent: prompt auto-restores a missing session from SessionStore", func(t *testing.T) {
		conn := newFakeConn()
		var spawns []SpawnParams
		var prompted []promptCall
		store := newMemStore(StoredSession{SessionID: "stored-session", Cwd: "/tmp/store-project", SessionFile: "/tmp/store-project/session.jsonl"})
		a, _ := testAgent(conn)
		a.store = store
		a.spawn = func(p SpawnParams) (Proc, error) { spawns = append(spawns, p); return newFakeProc(), nil }
		a.sessions = &fakeSessions{build: func(id string, p SessionCreateParams) ActiveSession {
			return &fakeSession{id: id, cwd: p.Cwd, proc: p.Proc, promptFn: func(m string, im []Image) TurnResult {
				prompted = append(prompted, promptCall{m, im})
				return TurnResult{Reason: StopEndTurn}
			}}
		}}
		res, err := a.Prompt(promptText("stored-session", "hello again"))
		if err != nil || res.StopReason != StopEndTurn {
			t.Fatalf("res=%v err=%v", res, err)
		}
		if !reflect.DeepEqual(spawns, []SpawnParams{{Cwd: "/tmp/store-project", SessionPath: "/tmp/store-project/session.jsonl"}}) {
			t.Errorf("spawns = %+v", spawns)
		}
		if len(prompted) != 1 || prompted[0].Message != "hello again" || len(prompted[0].Images) != 0 {
			t.Errorf("prompted = %+v", prompted)
		}
		want := StoredSession{SessionID: "stored-session", Cwd: "/tmp/store-project", SessionFile: "/tmp/store-project/session.jsonl"}
		if len(store.upserts) != 1 || store.upserts[0].SessionID != want.SessionID || store.upserts[0].Cwd != want.Cwd || store.upserts[0].SessionFile != want.SessionFile {
			t.Errorf("upserts = %+v", store.upserts)
		}
	})

	tw(t, "unit/session-restore", "PiAcpAgent: setSessionConfigOption auto-restores via pi session discovery when SessionStore misses", func(t *testing.T) {
		conn := newFakeConn()
		root := t.TempDir()
		sessionFile := filepath.Join(root, "sessions", "--tmp--fallback-project--", "0000_restore_fallback.jsonl")
		writeSessionFile(t, sessionFile, headerLine("fallback-session", "/tmp/fallback-project"))
		withAgentDir(t, root)
		state := map[string]any{"thinkingLevel": "medium", "model": map[string]any{"provider": "test", "id": "alpha"}}
		var setModel []map[string]string
		var spawns []SpawnParams
		proc := newFakeProc()
		proc.getLevelsFn = func() ([]string, error) { return []string{"medium"}, nil }
		proc.getModelsFn = func() (map[string]any, error) {
			return models([3]string{"test", "alpha", "Alpha"}, [3]string{"test", "beta", "Beta"}), nil
		}
		proc.getStateFn = func() (map[string]any, error) { return state, nil }
		proc.setModelFn = func(p, id string) error {
			setModel = append(setModel, map[string]string{"provider": p, "modelId": id})
			state["model"] = map[string]any{"provider": p, "id": id}
			return nil
		}
		store := newMemStore()
		a, _ := testAgent(conn)
		a.store = store
		a.spawn = func(p SpawnParams) (Proc, error) { spawns = append(spawns, p); return proc, nil }
		a.sessions = &fakeSessions{build: func(id string, p SessionCreateParams) ActiveSession {
			return &fakeSession{id: id, cwd: p.Cwd, proc: p.Proc}
		}}
		result, err := a.SetSessionConfigOption(SetSessionConfigOptionRequest{SessionID: "fallback-session", ConfigID: "model", Value: "test/beta"})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(spawns, []SpawnParams{{Cwd: "/tmp/fallback-project", SessionPath: sessionFile}}) {
			t.Errorf("spawns = %+v", spawns)
		}
		jsonEqual(t, setModel, []any{map[string]any{"provider": "test", "modelId": "beta"}})
		if optionValue(result.ConfigOptions, "model") != "test/beta" {
			t.Errorf("model = %q", optionValue(result.ConfigOptions, "model"))
		}
		if len(store.upserts) != 2 {
			t.Fatalf("upserts = %+v", store.upserts)
		}
		for _, u := range store.upserts {
			if u.SessionID != "fallback-session" || u.Cwd != "/tmp/fallback-project" || u.SessionFile != sessionFile {
				t.Errorf("upsert = %+v", u)
			}
		}
		var got []Update
		for _, u := range conn.all() {
			got = append(got, u.Update)
		}
		jsonEqual(t, got, []any{
			map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "medium"},
			map[string]any{"sessionUpdate": "config_option_update", "configOptions": result.ConfigOptions},
		})
	})

	tw(t, "unit/session-restore", "PiAcpAgent: cancel ignores stale session IDs without spawning a restore process", func(t *testing.T) {
		conn := newFakeConn()
		var spawns []SpawnParams
		a, _ := testAgent(conn)
		a.spawn = func(p SpawnParams) (Proc, error) { spawns = append(spawns, p); return newFakeProc(), nil }
		a.sessions = &fakeSessions{build: func(string, SessionCreateParams) ActiveSession {
			t.Error("cancel should not restore a missing session")
			return nil
		}}
		if err := a.Cancel("stale-session"); err != nil {
			t.Fatal(err)
		}
		if len(spawns) != 0 || len(conn.all()) != 0 {
			t.Errorf("spawns=%v updates=%v", spawns, conn.all())
		}
	})
}

func TestSessionDelete(t *testing.T) {
	deleteAgent := func(store *memStore) *Agent {
		a, _ := testAgent(newFakeConn())
		a.store = store
		return a
	}
	tw(t, "unit/session-delete", "PiAcpAgent: deleteSession removes stored session and session file", func(t *testing.T) {
		root := t.TempDir()
		sessionFile := filepath.Join(root, "sessions", "--tmp--delete-project--", "0000_delete_me.jsonl")
		writeSessionFile(t, sessionFile, headerLine("sess-del-store", "/tmp/delete-project"))
		withAgentDir(t, root)
		store := newMemStore(StoredSession{SessionID: "stored-session", Cwd: "/tmp/delete-project", SessionFile: sessionFile})
		res, err := deleteAgent(store).DeleteSession(DeleteSessionRequest{SessionID: "stored-session"})
		if err != nil || len(res) != 0 {
			t.Fatalf("res=%v err=%v", res, err)
		}
		if !reflect.DeepEqual(store.deletes, []string{"stored-session"}) {
			t.Errorf("deletes = %v", store.deletes)
		}
		if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
			t.Error("session file still exists")
		}
	})
	tw(t, "unit/session-delete", "PiAcpAgent: deleteSession finds session via pi discovery when SessionStore misses", func(t *testing.T) {
		root := t.TempDir()
		sessionFile := filepath.Join(root, "sessions", "--tmp--delete-discovery--", "0000_pi_discovery.jsonl")
		writeSessionFile(t, sessionFile, headerLine("pi-discovered-session", "/tmp/delete-discovery"))
		withAgentDir(t, root)
		store := newMemStore()
		res, err := deleteAgent(store).DeleteSession(DeleteSessionRequest{SessionID: "pi-discovered-session"})
		if err != nil || len(res) != 0 {
			t.Fatalf("res=%v err=%v", res, err)
		}
		if !reflect.DeepEqual(store.deletes, []string{"pi-discovered-session"}) {
			t.Errorf("deletes = %v", store.deletes)
		}
		if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
			t.Error("session file still exists")
		}
	})
	tw(t, "unit/session-delete", "PiAcpAgent: deleteSession succeeds idempotently for unknown sessionId", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "sessions", "--tmp--delete-unknown--"), 0o755); err != nil {
			t.Fatal(err)
		}
		withAgentDir(t, root)
		store := newMemStore()
		res, err := deleteAgent(store).DeleteSession(DeleteSessionRequest{SessionID: "non-existent-session"})
		if err != nil || len(res) != 0 || len(store.deletes) != 0 {
			t.Fatalf("res=%v err=%v deletes=%v", res, err, store.deletes)
		}
	})
	tw(t, "unit/session-delete", "PiAcpAgent: deleteSession survives missing session file", func(t *testing.T) {
		root := t.TempDir()
		missing := filepath.Join(root, "sessions", "--tmp--delete-missingfile--", "0000_non_existent.jsonl")
		if err := os.MkdirAll(filepath.Dir(missing), 0o755); err != nil {
			t.Fatal(err)
		}
		withAgentDir(t, root)
		store := newMemStore(StoredSession{SessionID: "missing-file-session", Cwd: "/tmp/delete-missingfile", SessionFile: missing})
		res, err := deleteAgent(store).DeleteSession(DeleteSessionRequest{SessionID: "missing-file-session"})
		if err != nil || len(res) != 0 {
			t.Fatalf("res=%v err=%v", res, err)
		}
		if !reflect.DeepEqual(store.deletes, []string{"missing-file-session"}) {
			t.Errorf("deletes = %v", store.deletes)
		}
	})
}

func TestNewSessionFailures(t *testing.T) {
	failing := func(models func() (map[string]any, error), state map[string]any) *fakeProc {
		p := newFakeProc()
		p.getModelsFn = models
		p.getStateFn = func() (map[string]any, error) { return state, nil }
		return p
	}
	errCode := func(err error) int {
		if re, ok := err.(*RequestError); ok {
			return re.Code
		}
		return 0
	}

	tw(t, "unit/new-session-auth-required-when-no-models", "PiAcpAgent: newSession throws AUTH_REQUIRED when pi reports zero available models", func(t *testing.T) {
		proc := failing(func() (map[string]any, error) { return map[string]any{"models": []any{}}, nil }, map[string]any{"thinkingLevel": "medium", "model": nil})
		sessions := &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}}
		a, _ := testAgent(newFakeConn())
		a.sessions = sessions
		_, err := a.NewSession(newSessionReq(cwdNow(t)))
		if errCode(err) != -32000 || !strings.Contains(strings.ToLower(err.Error()), "configure an api key or log in with an oauth provider") {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(sessions.closeCalls, []string{"s1"}) {
			t.Errorf("closeCalls = %v", sessions.closeCalls)
		}
	})

	tw(t, "unit/new-session-pi-not-found", "PiAcpAgent: newSession returns a helpful Internal error when pi is not installed", func(t *testing.T) {
		t.Setenv("PI_ACP_PI_COMMAND", "pi-does-not-exist-12345")
		a, _ := testAgent(newFakeConn())
		a.sessions = NewSessionManager(nil, newMemStore())
		_, err := a.NewSession(newSessionReq(cwdNow(t)))
		if errCode(err) != -32603 || !strings.Contains(strings.ToLower(err.Error()), "executable not found") {
			t.Fatalf("err = %v", err)
		}
	})

	tw(t, "unit/new-session-runtime-startup-errors", "PiAcpAgent: newSession returns AUTH_REQUIRED when pi reports an auth error after spawn", func(t *testing.T) {
		root := t.TempDir()
		sessionFile := filepath.Join(root, "sessions", "failed.jsonl")
		writeSessionFile(t, sessionFile, headerLine("s-auth", cwdNow(t)))
		proc := failing(func() (map[string]any, error) { return nil, errors.New("Authentication required: missing key") },
			map[string]any{"thinkingLevel": "medium", "model": nil, "sessionFile": sessionFile})
		sessions := &fakeSessions{session: &fakeSession{id: "s-auth", cwd: cwdNow(t), proc: proc}}
		store := NewFileStore(filepath.Join(root, "session-map.json"))
		store.Upsert(StoredSession{SessionID: "s-auth", Cwd: cwdNow(t), SessionFile: sessionFile})
		a, _ := testAgent(newFakeConn())
		a.sessions, a.store = sessions, store
		_, err := a.NewSession(newSessionReq(cwdNow(t)))
		if errCode(err) != -32000 {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(sessions.closeCalls, []string{"s-auth"}) {
			t.Errorf("closeCalls = %v", sessions.closeCalls)
		}
		if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
			t.Error("the failed session file was not removed")
		}
		if store.Get("s-auth") != nil {
			t.Error("the failed session is still in the store")
		}
	})

	tw(t, "unit/new-session-runtime-startup-errors", "PiAcpAgent: newSession returns Internal error on non-auth model probe failures after spawn", func(t *testing.T) {
		proc := failing(func() (map[string]any, error) { return nil, errors.New("socket hang up") }, map[string]any{"thinkingLevel": "medium", "model": nil})
		sessions := &fakeSessions{session: &fakeSession{id: "s-internal", cwd: cwdNow(t), proc: proc}}
		a, _ := testAgent(newFakeConn())
		a.sessions = sessions
		_, err := a.NewSession(newSessionReq(cwdNow(t)))
		if errCode(err) != -32603 || !strings.Contains(err.Error(), "socket hang up") {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(sessions.closeCalls, []string{"s-internal"}) {
			t.Errorf("closeCalls = %v", sessions.closeCalls)
		}
	})

	for _, failure := range []string{"discovery", "auth", "invalid-current", "inconsistent-current", "state"} {
		tw(t, "unit/new-session-runtime-startup-errors", "PiAcpAgent: cleans up only the new session after ${failure} configuration failure", func(t *testing.T) {
			t.Run(fmt.Sprintf("PiAcpAgent: cleans up only the new session after %s configuration failure", failure), func(t *testing.T) {
				root := t.TempDir()
				sessionFile, existingFile := filepath.Join(root, "failed.jsonl"), filepath.Join(root, "existing.jsonl")
				write(t, sessionFile, "new session\n")
				write(t, existingFile, "existing session\n")
				store := NewFileStore(filepath.Join(root, "map.json"))
				store.Upsert(StoredSession{SessionID: "failed", Cwd: root, SessionFile: sessionFile})
				store.Upsert(StoredSession{SessionID: "existing", Cwd: root, SessionFile: existingFile})
				proc := newFakeProc()
				proc.getModelsFn = func() (map[string]any, error) { return models([3]string{"test", "model", ""}), nil }
				proc.getStateFn = func() (map[string]any, error) {
					if failure == "state" {
						return nil, errors.New("state read failed")
					}
					level := "max"
					if failure == "invalid-current" {
						level = ""
					} else if failure == "inconsistent-current" {
						level = "medium"
					}
					return map[string]any{"sessionFile": sessionFile, "thinkingLevel": level}, nil
				}
				proc.getLevelsFn = func() ([]string, error) {
					if failure == "discovery" {
						return nil, errors.New("discovery failed")
					}
					if failure == "auth" {
						return nil, errors.New("Authentication required: missing key")
					}
					return []string{"low", "high", "max"}, nil
				}
				sessions := &fakeSessions{session: &fakeSession{id: "failed", cwd: root, proc: proc}}
				conn := newFakeConn()
				a, _ := testAgent(conn)
				a.sessions, a.store = sessions, store
				_, err := a.NewSession(newSessionReq(root))
				want := -32603
				if failure == "auth" {
					want = -32000
				}
				if errCode(err) != want {
					t.Fatalf("err = %v (code %d), want code %d", err, errCode(err), want)
				}
				if !reflect.DeepEqual(sessions.closeCalls, []string{"failed"}) {
					t.Errorf("closeCalls = %v", sessions.closeCalls)
				}
				if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
					t.Error("the failed session file was not removed")
				}
				if store.Get("failed") != nil {
					t.Error("failed still in the store")
				}
				if _, err := os.Stat(existingFile); err != nil {
					t.Error("the existing session file was removed")
				}
				if e := store.Get("existing"); e == nil || e.SessionFile != existingFile {
					t.Errorf("existing = %+v", e)
				}
				if len(conn.all()) != 0 {
					t.Error("updates sent for a failed session")
				}
			})
		})
	}
}

func TestLoadConfigurationFailure(t *testing.T) {
	for _, failure := range []string{"discovery", "missing-current", "inconsistent-current", "state"} {
		tw(t, "unit/model-thinking-levels", "load configuration ${failure} failure closes restored child and preserves history for retry", func(t *testing.T) {
			t.Run(fmt.Sprintf("load configuration %s failure closes restored child and preserves history for retry", failure), func(t *testing.T) {
				root := t.TempDir()
				sessionFile := filepath.Join(root, "history.jsonl")
				write(t, sessionFile, "persisted history\n")
				store := NewFileStore(filepath.Join(root, "map.json"))
				store.Upsert(StoredSession{SessionID: "s1", Cwd: root, SessionFile: sessionFile})
				f := newLevelsFixture(t)
				conn := f.conn
				sch := f.sch
				mgr := NewSessionManager(nil, store)
				f.agent.sessions, f.agent.store = mgr, store
				existing := newFakeProc()
				mgr.GetOrCreate("existing", SessionCreateParams{Cwd: root, McpServers: []any{}, Conn: conn, Proc: existing})
				defer mgr.DisposeAll()
				restoredDisposed, historyReads, spawns := 0, 0, 0
				shouldFail := true
				f.agent.spawn = func(SpawnParams) (Proc, error) {
					spawns++
					p := newFakeProc()
					p.getModelsFn = f.proc.getModelsFn
					p.getStateFn = func() (map[string]any, error) {
						if shouldFail && failure == "state" {
							return nil, errors.New("state unavailable")
						}
						if shouldFail && failure == "missing-current" {
							return map[string]any{}, nil
						}
						if shouldFail && failure == "inconsistent-current" {
							return map[string]any{"thinkingLevel": "medium"}, nil
						}
						return f.proc.GetState()
					}
					p.getLevelsFn = func() ([]string, error) {
						if shouldFail && failure == "discovery" {
							return nil, errors.New("discovery unavailable")
						}
						return f.proc.GetAvailableThinkingLevels()
					}
					p.getMessagesFn = func() (map[string]any, error) {
						historyReads++
						return map[string]any{"messages": []any{map[string]any{"role": "user", "content": "persisted prompt"}}}, nil
					}
					p.disposeHook = func() { restoredDisposed++ }
					return p, nil
				}
				mgr.spawn = f.agent.spawn
				if _, err := f.agent.LoadSession(LoadSessionRequest{SessionID: "s1", Cwd: root, McpServers: []any{}}); err == nil {
					t.Fatal("expected the load to fail")
				}
				if restoredDisposed != 1 {
					t.Errorf("restored child disposed %d times", restoredDisposed)
				}
				if mgr.MaybeGet("s1") != nil {
					t.Error("s1 is still registered")
				}
				if mgr.MaybeGet("existing") == nil || existing.disposeCount() != 0 {
					t.Error("the existing session was disturbed")
				}
				if historyReads != 0 || len(conn.all()) != 0 {
					t.Errorf("historyReads=%d updates=%d", historyReads, len(conn.all()))
				}
				if b, _ := os.ReadFile(sessionFile); string(b) != "persisted history\n" {
					t.Errorf("history file = %q", b)
				}
				if e := store.Get("s1"); e == nil || e.SessionFile != sessionFile {
					t.Errorf("store entry = %+v", e)
				}
				shouldFail = false
				r, err := f.agent.LoadSession(LoadSessionRequest{SessionID: "s1", Cwd: root, McpServers: []any{}})
				if err != nil {
					t.Fatal(err)
				}
				if r.Modes.CurrentModeID != "max" || spawns != 2 || restoredDisposed != 1 || historyReads != 1 {
					t.Errorf("mode=%q spawns=%d disposed=%d reads=%d", r.Modes.CurrentModeID, spawns, restoredDisposed, historyReads)
				}
				if len(conn.ofKind("user_message_chunk")) == 0 {
					t.Error("history was not replayed on the retry")
				}
				if b, _ := os.ReadFile(sessionFile); string(b) != "persisted history\n" {
					t.Errorf("history file = %q", b)
				}
				sch.drain()
			})
		})
	}
}
