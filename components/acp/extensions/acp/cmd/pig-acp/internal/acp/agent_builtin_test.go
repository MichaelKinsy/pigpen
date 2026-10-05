package acp

// Layer-2 cases: every branch of the adapter-side slash commands in agent.ts that the original's
// tests do not cover (/compact, /session, /name failure, /export guards, /autocompact, /changelog).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func builtinAgent(t *testing.T, proc *fakeProc, cwd string) (*Agent, *fakeConn) {
	conn := newFakeConn()
	a, _ := testAgent(conn)
	a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwd, proc: proc}, anyID: true}
	return a, conn
}

func runCmd(t *testing.T, a *Agent, text string) PromptResponse {
	t.Helper()
	res, err := a.Prompt(promptText("s1", text))
	if err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return res
}

func TestBuiltinCompact(t *testing.T) {
	t.Run("/compact reports tokens before and the summary", func(t *testing.T) {
		proc := newFakeProc()
		proc.compactFn = func(string) (map[string]any, error) {
			return map[string]any{"tokensBefore": 1234, "summary": "Short summary"}, nil
		}
		a, conn := builtinAgent(t, proc, cwdNow(t))
		if res := runCmd(t, a, "/compact"); res.StopReason != StopEndTurn {
			t.Fatalf("res = %v", res)
		}
		if got, want := lastChunkText(t, conn), "Compaction completed.\nTokens before: 1234\n\nShort summary"; got != want {
			t.Errorf("text = %q, want %q", got, want)
		}
		if n := len(proc.promptList()); n != 0 {
			t.Errorf("%d prompts reached pi", n)
		}
	})
	t.Run("/compact passes custom instructions and says so", func(t *testing.T) {
		proc := newFakeProc()
		var got string
		proc.compactFn = func(instr string) (map[string]any, error) { got = instr; return map[string]any{}, nil }
		a, conn := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, "/compact keep file names")
		if got != "keep file names" {
			t.Errorf("instructions = %q", got)
		}
		if text := lastChunkText(t, conn); text != "Compaction completed. (custom instructions applied)" {
			t.Errorf("text = %q", text)
		}
	})
	t.Run("/compact failure is a prompt error, not a chunk", func(t *testing.T) {
		proc := newFakeProc()
		proc.compactFn = func(string) (map[string]any, error) { return nil, errors.New("pi compact failed: nothing to compact") }
		a, _ := builtinAgent(t, proc, cwdNow(t))
		if _, err := a.Prompt(promptText("s1", "/compact")); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestBuiltinSession(t *testing.T) {
	t.Run("/session lists id, file, messages, cost and tokens", func(t *testing.T) {
		proc := newFakeProc()
		proc.sessionStats = SessionStats{"sessionId": "sid", "sessionFile": "/x/s.jsonl", "totalMessages": 4, "cost": 0.5,
			"tokens": map[string]any{"input": 1, "output": 2, "cacheRead": 3, "cacheWrite": 4, "total": 10}}
		a, conn := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, "/session")
		want := "Session: sid\nSession file: /x/s.jsonl\nMessages: 4\nCost: 0.5\nTokens: in 1, out 2, cache read 3, cache write 4, total 10"
		if got := lastChunkText(t, conn); got != want {
			t.Errorf("text = %q, want %q", got, want)
		}
	})
	t.Run("/session falls back to the raw stats when the shape is unknown", func(t *testing.T) {
		proc := newFakeProc()
		proc.sessionStats = SessionStats{"weird": true}
		a, conn := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, "/session")
		if got := lastChunkText(t, conn); !strings.HasPrefix(got, "Session stats:\n{") || !strings.Contains(got, `"weird": true`) {
			t.Errorf("text = %q", got)
		}
	})
}

func TestBuiltinName(t *testing.T) {
	t.Run("/name without an argument prints usage and does not call pi", func(t *testing.T) {
		proc := newFakeProc()
		called := false
		proc.setSessionNameFn = func(string) error { called = true; return nil }
		a, conn := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, "/name")
		if called || lastChunkText(t, conn) != "Usage: /name <name>" {
			t.Errorf("called=%v text=%q", called, lastChunkText(t, conn))
		}
	})
	t.Run("/name failure says why, with a hint when set_session_name is unsupported", func(t *testing.T) {
		proc := newFakeProc()
		proc.setSessionNameFn = func(string) error { return errors.New("pi set_session_name failed: unknown command") }
		a, conn := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, "/name X")
		got := lastChunkText(t, conn)
		if !strings.HasPrefix(got, "Failed to set session name: pi set_session_name failed: unknown command") ||
			!strings.Contains(got, "requires a newer pi version that supports `set_session_name`") {
			t.Errorf("text = %q", got)
		}
		if n := len(conn.ofKind("session_info_update")); n != 0 {
			t.Errorf("%d session_info_update after a failure", n)
		}
	})
	t.Run("/name with quotes keeps the words", func(t *testing.T) {
		proc := newFakeProc()
		var got string
		proc.setSessionNameFn = func(n string) error { got = n; return nil }
		a, _ := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, `/name "Fix the bug" now`)
		if got != "Fix the bug now" {
			t.Errorf("name = %q", got)
		}
	})
}

func TestBuiltinAutocompact(t *testing.T) {
	for in, want := range map[string]bool{"on": true, "true": true, "enable": true, "enabled": true, "off": false, "false": false, "disable": false, "disabled": false} {
		t.Run("/autocompact "+in, func(t *testing.T) {
			proc := newFakeProc()
			var got *bool
			proc.setAutoCompactFn = func(b bool) error { got = &b; return nil }
			a, conn := builtinAgent(t, proc, cwdNow(t))
			runCmd(t, a, "/autocompact "+in)
			text := "Auto-compaction disabled."
			if want {
				text = "Auto-compaction enabled."
			}
			if got == nil || *got != want || lastChunkText(t, conn) != text {
				t.Errorf("got=%v text=%q", got, lastChunkText(t, conn))
			}
		})
	}
	t.Run("/autocompact toggles the current state", func(t *testing.T) {
		proc := newFakeProc()
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"autoCompactionEnabled": true}, nil }
		var got *bool
		proc.setAutoCompactFn = func(b bool) error { got = &b; return nil }
		a, conn := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, "/autocompact")
		if got == nil || *got || lastChunkText(t, conn) != "Auto-compaction disabled." {
			t.Errorf("got=%v text=%q", got, lastChunkText(t, conn))
		}
	})
}

func TestBuiltinExport(t *testing.T) {
	notes := func(conn *fakeConn) string { return lastChunkText(t, conn) }
	t.Run("/export with no messages says there is nothing to export", func(t *testing.T) {
		proc := newFakeProc()
		proc.getStateFn = func() (map[string]any, error) {
			return map[string]any{"sessionFile": "/nope.jsonl", "messageCount": 0}, nil
		}
		a, conn := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, "/export")
		if got := notes(conn); got != "Nothing to export yet (no session messages). Send a prompt first." {
			t.Errorf("text = %q", got)
		}
	})
	t.Run("/export with an empty session file says so", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "s.jsonl")
		write(t, file, "  \n")
		proc := newFakeProc()
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"sessionFile": file, "messageCount": 2}, nil }
		a, conn := builtinAgent(t, proc, cwdNow(t))
		runCmd(t, a, "/export")
		if got := notes(conn); got != "Nothing to export yet (empty session file). Send a prompt first." {
			t.Errorf("text = %q", got)
		}
	})
	t.Run("/export writes into the session cwd and links the file", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "s.jsonl")
		write(t, file, "{}\n")
		proc := newFakeProc()
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"sessionFile": file, "messageCount": 2}, nil }
		var asked string
		proc.exportHTMLFn = func(p string) (string, error) { asked = p; return p, nil }
		a, conn := builtinAgent(t, proc, dir)
		runCmd(t, a, "/export")
		if want := filepath.Join(dir, "pi-session-s1.html"); asked != want {
			t.Errorf("export path = %q, want %q", asked, want)
		}
		ups := conn.all()
		if len(ups) != 2 {
			t.Fatalf("%d updates", len(ups))
		}
		jsonEqual(t, ups[0].Update, chunk("Session exported: "))
		jsonEqual(t, ups[1].Update, Update{"sessionUpdate": "agent_message_chunk", "content": map[string]any{
			"type": "resource_link", "name": "pi-session-s1.html", "uri": "file://" + asked, "mimeType": "text/html", "title": "Session exported"}})
	})
	t.Run("/export failure and an empty returned path are reported", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "s.jsonl")
		write(t, file, "{}\n")
		for _, tc := range []struct {
			fn   func(string) (string, error)
			want string
		}{
			{func(string) (string, error) { return "", errors.New("disk full") }, "Export failed: disk full"},
			{func(string) (string, error) { return "", nil }, "Export failed: no output path returned by pi."},
		} {
			proc := newFakeProc()
			proc.getStateFn = func() (map[string]any, error) { return map[string]any{"sessionFile": file, "messageCount": 2}, nil }
			proc.exportHTMLFn = tc.fn
			a, conn := builtinAgent(t, proc, dir)
			runCmd(t, a, "/export")
			if got := notes(conn); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
		}
	})
}

func TestBuiltinChangelog(t *testing.T) {
	t.Run("/changelog says so when no installation is found", func(t *testing.T) {
		proc := newFakeProc()
		a, conn := builtinAgent(t, proc, cwdNow(t))
		a.changelogPath = func() string { return "" }
		runCmd(t, a, "/changelog")
		if got := lastChunkText(t, conn); got != "Changelog not found (couldn't locate pig installation)." {
			t.Errorf("text = %q", got)
		}
	})
	t.Run("/changelog truncates a long file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "CHANGELOG.md")
		write(t, file, strings.Repeat("a", 25000))
		a, conn := builtinAgent(t, newFakeProc(), cwdNow(t))
		a.changelogPath = func() string { return file }
		runCmd(t, a, "/changelog")
		got := lastChunkText(t, conn)
		if !strings.HasSuffix(got, "\n\n...(truncated)...") || len(got) != 20000+len("\n\n...(truncated)...") {
			t.Errorf("len = %d, tail = %q", len(got), got[len(got)-30:])
		}
	})
	t.Run("/changelog finds a CHANGELOG.md next to the pig binary", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "bin", "pig")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, bin, "")
		write(t, filepath.Join(dir, "CHANGELOG.md"), "# Changes\n")
		// FindChangelog resolves the binary's symlinks (as Pi does with realpath), so compare resolved paths:
		// macOS's TMPDIR is a symlink (/var -> /private/var).
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := FindChangelog(bin); got != filepath.Join(realDir, "CHANGELOG.md") {
			t.Errorf("found %q, want %q", got, filepath.Join(realDir, "CHANGELOG.md"))
		}
		if got := FindChangelog(filepath.Join(dir, "elsewhere", "pig")); got != "" {
			t.Errorf("found %q for a missing installation", got)
		}
	})
}

func TestPromptRouting(t *testing.T) {
	t.Run("a slash command with an image goes to pi as a prompt", func(t *testing.T) {
		proc := newFakeProc()
		sess := &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}
		a, _ := testAgent(newFakeConn())
		a.sessions = &fakeSessions{session: sess, anyID: true}
		_, err := a.Prompt(PromptRequest{SessionID: "s1", Prompt: []ContentBlock{
			{"type": "text", "text": "/steering"}, {"type": "image", "mimeType": "image/png", "data": "AA=="}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(sess.prompts) != 1 || sess.prompts[0].Message != "/steering" || len(sess.prompts[0].Images) != 1 {
			t.Errorf("prompts = %+v", sess.prompts)
		}
	})
	t.Run("an unknown slash command goes to the session unchanged", func(t *testing.T) {
		sess := &fakeSession{id: "s1", cwd: cwdNow(t), proc: newFakeProc()}
		a, _ := testAgent(newFakeConn())
		a.sessions = &fakeSessions{session: sess, anyID: true}
		if _, err := a.Prompt(promptText("s1", "/unknown thing")); err != nil {
			t.Fatal(err)
		}
		if len(sess.prompts) != 1 || sess.prompts[0].Message != "/unknown thing" {
			t.Errorf("prompts = %+v", sess.prompts)
		}
	})
	t.Run("an error turn ends as end_turn, or cancelled when cancel was requested", func(t *testing.T) {
		for _, cancelled := range []bool{false, true} {
			sess := &fakeSession{id: "s1", cwd: cwdNow(t), proc: newFakeProc(), promptFn: func(string, []Image) TurnResult { return TurnResult{Reason: StopError} }}
			cs := &cancelFlagSession{fakeSession: sess, cancelled: cancelled}
			a, _ := testAgent(newFakeConn())
			a.sessions = &fakeSessions{session: cs, anyID: true}
			res, err := a.Prompt(promptText("s1", "hi"))
			want := StopEndTurn
			if cancelled {
				want = StopCancelled
			}
			if err != nil || res.StopReason != want {
				t.Errorf("cancelled=%v: res=%v err=%v", cancelled, res, err)
			}
		}
	})
	t.Run("a turn that fails with an auth error surfaces AUTH_REQUIRED", func(t *testing.T) {
		sess := &fakeSession{id: "s1", cwd: cwdNow(t), proc: newFakeProc(), promptFn: func(string, []Image) TurnResult {
			return TurnResult{Err: ErrAuthRequired(map[string]any{"authMethods": AuthMethods(true)}, "Configure an API key or log in with an OAuth provider.")}
		}}
		a, _ := testAgent(newFakeConn())
		a.sessions = &fakeSessions{session: sess, anyID: true}
		_, err := a.Prompt(promptText("s1", "hi"))
		if re, ok := err.(*RequestError); !ok || re.Code != -32000 {
			t.Errorf("err = %v", err)
		}
	})
}

type cancelFlagSession struct {
	*fakeSession
	cancelled bool
}

func (c *cancelFlagSession) WasCancelRequested() bool { return c.cancelled }

func TestAgentCommandsAdvertised(t *testing.T) {
	t.Run("session/new advertises pi commands merged with the built-ins, extension commands hidden", func(t *testing.T) {
		conn := newFakeConn()
		proc := newFakeProc()
		proc.getStateFn = func() (map[string]any, error) {
			return map[string]any{"thinkingLevel": "medium", "model": map[string]any{"provider": "test", "id": "model"}}, nil
		}
		proc.getCommandsFn = func() (map[string]any, error) {
			return map[string]any{"commands": []any{
				map[string]any{"name": "ext", "description": "E", "source": "extension"},
				map[string]any{"name": "review", "description": "R", "source": "prompt"},
				map[string]any{"name": "compact", "description": "dup", "source": "prompt"}}}, nil
		}
		a, sch := testAgent(conn)
		a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}}
		if _, err := a.NewSession(newSessionReq(cwdNow(t))); err != nil {
			t.Fatal(err)
		}
		sch.drain()
		ups := conn.ofKind("available_commands_update")
		if len(ups) != 1 {
			t.Fatalf("%d available_commands_update", len(ups))
		}
		var names []string
		for _, c := range ups[0].Update["availableCommands"].([]AvailableCommand) {
			names = append(names, c.Name)
		}
		want := "review,compact,autocompact,export,session,name,steering,follow-up,changelog"
		if strings.Join(names, ",") != want {
			t.Errorf("commands = %v", names)
		}
	})
	t.Run("session/new falls back to the prompt template files when get_commands fails", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "prompts", "hello.md"), "---\ndescription: Say hello\n---\nHello $1\n")
		withAgentDir(t, dir)
		conn := newFakeConn()
		proc := newFakeProc() // get_commands is unsupported on the default fake
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"thinkingLevel": "medium"}, nil }
		a, sch := testAgent(conn)
		a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}}
		if _, err := a.NewSession(newSessionReq(cwdNow(t))); err != nil {
			t.Fatal(err)
		}
		sch.drain()
		cmds := conn.ofKind("available_commands_update")[0].Update["availableCommands"].([]AvailableCommand)
		if cmds[0].Name != "hello" || cmds[0].Description != "Say hello (user)" {
			t.Errorf("commands = %+v", cmds)
		}
	})
	t.Run("skill commands are hidden when enableSkillCommands is false", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "settings.json"), `{"enableSkillCommands": false}`)
		withAgentDir(t, dir)
		conn := newFakeConn()
		proc := newFakeProc()
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"thinkingLevel": "medium"}, nil }
		proc.getCommandsFn = func() (map[string]any, error) {
			return map[string]any{"commands": []any{map[string]any{"name": "skill:foo", "source": "skill"}, map[string]any{"name": "y", "source": "prompt"}}}, nil
		}
		a, sch := testAgent(conn)
		a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}}
		if _, err := a.NewSession(newSessionReq(cwdNow(t))); err != nil {
			t.Fatal(err)
		}
		sch.drain()
		for _, c := range conn.ofKind("available_commands_update")[0].Update["availableCommands"].([]AvailableCommand) {
			if c.Name == "skill:foo" {
				t.Error("skill command advertised")
			}
		}
	})
}

func TestNewSessionPolicies(t *testing.T) {
	t.Run("session/new rejects a relative cwd", func(t *testing.T) {
		a, _ := testAgent(newFakeConn())
		_, err := a.NewSession(NewSessionRequest{Cwd: "rel/dir"})
		if re, ok := err.(*RequestError); !ok || re.Code != -32602 || !strings.Contains(re.Message, "cwd must be an absolute path: rel/dir") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("session/new keeps one live pi child per connection", func(t *testing.T) {
		proc := newFakeProc()
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"thinkingLevel": "medium"}, nil }
		sessions := &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}}
		a, _ := testAgent(newFakeConn())
		a.sessions = sessions
		if _, err := a.NewSession(newSessionReq(cwdNow(t))); err != nil {
			t.Fatal(err)
		}
		if len(sessions.closeOthers) != 1 || sessions.closeOthers[0] != "s1" {
			t.Errorf("closeAllExcept = %v", sessions.closeOthers)
		}
	})
	t.Run("session/load rejects a relative cwd and an unknown session", func(t *testing.T) {
		a, _ := testAgent(newFakeConn())
		a.sessions = NewSessionManager(nil, a.store)
		_, err := a.LoadSession(LoadSessionRequest{SessionID: "x", Cwd: "rel"})
		if re, ok := err.(*RequestError); !ok || re.Code != -32602 || !strings.Contains(re.Message, "cwd must be an absolute path: rel") {
			t.Errorf("relative cwd: err = %v", err)
		}
		_, err = a.LoadSession(LoadSessionRequest{SessionID: "missing", Cwd: "/tmp/x"})
		if re, ok := err.(*RequestError); !ok || re.Code != -32602 || !strings.Contains(re.Message, "Unknown sessionId: missing") {
			t.Errorf("unknown session: err = %v", err)
		}
	})
	t.Run("session/list pages by 50 with an opaque numeric cursor", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i < 120; i++ {
			writeSessionFile(t, filepath.Join(root, "sessions", "--p--", jl(i)+".jsonl"),
				sessionHeader("s-"+jl(i), "/cwd/p", "2026-01-01T00:00:00.000Z"))
		}
		withAgentDir(t, root)
		a, _ := testAgent(newFakeConn())
		p1, err := a.ListSessions(ListSessionsRequest{})
		if err != nil || len(p1.Sessions) != 50 || p1.NextCursor == nil || *p1.NextCursor != "50" {
			t.Fatalf("page 1: n=%d next=%v err=%v", len(p1.Sessions), p1.NextCursor, err)
		}
		p3, _ := a.ListSessions(ListSessionsRequest{Cursor: p1.NextCursor})
		last := "100"
		p3, _ = a.ListSessions(ListSessionsRequest{Cursor: &last})
		if len(p3.Sessions) != 20 || p3.NextCursor != nil {
			t.Errorf("page 3: n=%d next=%v", len(p3.Sessions), p3.NextCursor)
		}
		bad := "garbage"
		p0, _ := a.ListSessions(ListSessionsRequest{Cursor: &bad})
		if len(p0.Sessions) != 50 {
			t.Errorf("an invalid cursor must read as offset 0, got %d sessions", len(p0.Sessions))
		}
	})
	t.Run("session/list filters by the cwd param", func(t *testing.T) {
		root := t.TempDir()
		writeSessionFile(t, filepath.Join(root, "sessions", "--a--", "a.jsonl"), sessionHeader("sa", "/cwd/a", "2026-01-01T00:00:00.000Z"))
		writeSessionFile(t, filepath.Join(root, "sessions", "--b--", "b.jsonl"), sessionHeader("sb", "/cwd/b", "2026-01-01T00:00:00.000Z"))
		withAgentDir(t, root)
		a, _ := testAgent(newFakeConn())
		cwd := "/cwd/b"
		r, _ := a.ListSessions(ListSessionsRequest{Cwd: &cwd})
		if len(r.Sessions) != 1 || r.Sessions[0].SessionID != "sb" {
			t.Errorf("sessions = %+v", r.Sessions)
		}
		r, _ = a.ListSessions(ListSessionsRequest{})
		if len(r.Sessions) != 2 {
			t.Errorf("without a cwd and no session yet, want all: %+v", r.Sessions)
		}
	})
}

func TestSetModelResolution(t *testing.T) {
	setup := func() (*Agent, *fakeProc, *[]string) {
		proc := newFakeProc()
		var calls []string
		proc.getStateFn = func() (map[string]any, error) {
			return map[string]any{"thinkingLevel": "medium", "model": map[string]any{"provider": "p", "id": "a"}}, nil
		}
		proc.getModelsFn = func() (map[string]any, error) {
			return models([3]string{"p", "a", ""}, [3]string{"q", "b/c", ""}), nil
		}
		proc.setModelFn = func(p, id string) error { calls = append(calls, p+"|"+id); return nil }
		a, _ := testAgent(newFakeConn())
		a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}, anyID: true}
		return a, proc, &calls
	}
	t.Run("a bare model id resolves through the available models", func(t *testing.T) {
		a, _, calls := setup()
		if err := a.UnstableSetSessionModel(SetSessionModelRequest{SessionID: "s1", ModelID: "a"}); err != nil {
			t.Fatal(err)
		}
		if strings.Join(*calls, ",") != "p|a" {
			t.Errorf("calls = %v", *calls)
		}
	})
	t.Run("a provider/model id keeps slashes inside the model id", func(t *testing.T) {
		a, _, calls := setup()
		if err := a.UnstableSetSessionModel(SetSessionModelRequest{SessionID: "s1", ModelID: "q/b/c"}); err != nil {
			t.Fatal(err)
		}
		if strings.Join(*calls, ",") != "q|b/c" {
			t.Errorf("calls = %v", *calls)
		}
	})
	t.Run("an unknown bare model id is invalid params", func(t *testing.T) {
		a, _, calls := setup()
		err := a.UnstableSetSessionModel(SetSessionModelRequest{SessionID: "s1", ModelID: "zzz"})
		if re, ok := err.(*RequestError); !ok || re.Code != -32602 || !strings.Contains(re.Message, "Unknown modelId: zzz") || len(*calls) != 0 {
			t.Errorf("err = %v calls=%v", err, *calls)
		}
	})
	t.Run("set_config_option rejects a non-string value and an unknown option", func(t *testing.T) {
		a, _, calls := setup()
		for _, req := range []SetSessionConfigOptionRequest{
			{SessionID: "s1", ConfigID: "model", Value: 3}, {SessionID: "s1", ConfigID: "bogus", Value: "x"}} {
			_, err := a.SetSessionConfigOption(req)
			if re, ok := err.(*RequestError); !ok || re.Code != -32602 {
				t.Errorf("%+v: err = %v", req, err)
			}
		}
		if len(*calls) != 0 {
			t.Errorf("calls = %v", *calls)
		}
	})
}
