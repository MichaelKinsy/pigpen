package acp

// Twins of test/component/agent-steering-followup-modes.test.ts and
// test/unit/builtin-commands.test.ts, test/unit/pi-enable-embed-context-flag.test.ts,
// test/component/session-thinking-modes.test.ts.

import (
	"regexp"
	"testing"
)

func promptText(sessionID, text string) PromptRequest {
	return PromptRequest{SessionID: sessionID, Prompt: []ContentBlock{{"type": "text", "text": text}}}
}

func lastChunkText(t *testing.T, c *fakeConn) string {
	t.Helper()
	ups := c.all()
	if len(ups) == 0 {
		t.Fatal("no updates")
	}
	last := ups[len(ups)-1].Update
	if last["sessionUpdate"] != "agent_message_chunk" {
		t.Fatalf("last update = %v", last)
	}
	return str(last["content"].(map[string]any)["text"])
}

func modeAgent(state map[string]any, proc *fakeProc) (*Agent, *fakeConn) {
	conn := newFakeConn()
	proc.getStateFn = func() (map[string]any, error) { return state, nil }
	a, _ := testAgent(conn)
	a.sessions = &fakeSessions{session: &fakeSession{id: "s1", proc: proc}, anyID: true}
	return a, conn
}

func TestSteeringFollowUpModes(t *testing.T) {
	tw(t, "component/agent-steering-followup-modes", "PiAcpAgent: /steering reports current steeringMode", func(t *testing.T) {
		a, conn := modeAgent(map[string]any{"steeringMode": "all"}, newFakeProc())
		res, err := a.Prompt(promptText("s1", "/steering"))
		if err != nil || res.StopReason != StopEndTurn {
			t.Fatalf("res=%v err=%v", res, err)
		}
		if got := lastChunkText(t, conn); !regexp.MustCompile(`Steering mode: all`).MatchString(got) {
			t.Errorf("text = %q", got)
		}
	})
	tw(t, "component/agent-steering-followup-modes", "PiAcpAgent: /steering sets steering mode", func(t *testing.T) {
		proc := newFakeProc()
		var setTo string
		proc.setSteeringFn = func(m string) error { setTo = m; return nil }
		a, conn := modeAgent(map[string]any{"steeringMode": "all"}, proc)
		res, err := a.Prompt(promptText("s1", "/steering one-at-a-time"))
		if err != nil || res.StopReason != StopEndTurn || setTo != "one-at-a-time" {
			t.Fatalf("res=%v err=%v setTo=%q", res, err, setTo)
		}
		if got := lastChunkText(t, conn); !regexp.MustCompile(`Steering mode set to: one-at-a-time`).MatchString(got) {
			t.Errorf("text = %q", got)
		}
	})
	tw(t, "component/agent-steering-followup-modes", "PiAcpAgent: /steering rejects invalid value", func(t *testing.T) {
		proc := newFakeProc()
		called := false
		proc.setSteeringFn = func(string) error { called = true; return nil }
		a, conn := modeAgent(map[string]any{"steeringMode": "all"}, proc)
		res, err := a.Prompt(promptText("s1", "/steering nope"))
		if err != nil || res.StopReason != StopEndTurn || called {
			t.Fatalf("res=%v err=%v called=%v", res, err, called)
		}
		if got := lastChunkText(t, conn); !regexp.MustCompile(`Usage: /steering`).MatchString(got) {
			t.Errorf("text = %q", got)
		}
	})
	tw(t, "component/agent-steering-followup-modes", "PiAcpAgent: /follow-up reports current followUpMode", func(t *testing.T) {
		a, conn := modeAgent(map[string]any{"followUpMode": "one-at-a-time"}, newFakeProc())
		res, err := a.Prompt(promptText("s1", "/follow-up"))
		if err != nil || res.StopReason != StopEndTurn {
			t.Fatalf("res=%v err=%v", res, err)
		}
		if got := lastChunkText(t, conn); !regexp.MustCompile(`Follow-up mode: one-at-a-time`).MatchString(got) {
			t.Errorf("text = %q", got)
		}
	})
	tw(t, "component/agent-steering-followup-modes", "PiAcpAgent: /follow-up sets follow-up mode", func(t *testing.T) {
		proc := newFakeProc()
		var setTo string
		proc.setFollowUpFn = func(m string) error { setTo = m; return nil }
		a, conn := modeAgent(map[string]any{"followUpMode": "one-at-a-time"}, proc)
		res, err := a.Prompt(promptText("s1", "/follow-up all"))
		if err != nil || res.StopReason != StopEndTurn || setTo != "all" {
			t.Fatalf("res=%v err=%v setTo=%q", res, err, setTo)
		}
		if got := lastChunkText(t, conn); !regexp.MustCompile(`Follow-up mode set to: all`).MatchString(got) {
			t.Errorf("text = %q", got)
		}
	})
	tw(t, "component/agent-steering-followup-modes", "PiAcpAgent: /follow-up rejects invalid value", func(t *testing.T) {
		proc := newFakeProc()
		called := false
		proc.setFollowUpFn = func(string) error { called = true; return nil }
		a, conn := modeAgent(map[string]any{"followUpMode": "one-at-a-time"}, proc)
		res, err := a.Prompt(promptText("s1", "/follow-up ???"))
		if err != nil || res.StopReason != StopEndTurn || called {
			t.Fatalf("res=%v err=%v called=%v", res, err, called)
		}
		if got := lastChunkText(t, conn); !regexp.MustCompile(`Usage: /follow-up`).MatchString(got) {
			t.Errorf("text = %q", got)
		}
	})
}

func TestBuiltinCommands(t *testing.T) {
	tw(t, "unit/builtin-commands", "PiAcpAgent: /steering is handled adapter-side", func(t *testing.T) {
		proc := newFakeProc()
		a, conn := modeAgent(map[string]any{"steeringMode": "one-at-a-time"}, proc)
		res, err := a.Prompt(promptText("s1", "/steering"))
		if err != nil || res.StopReason != StopEndTurn {
			t.Fatalf("res=%v err=%v", res, err)
		}
		if n := len(proc.promptList()); n != 0 {
			t.Errorf("%d prompts reached pi", n)
		}
		if got := lastChunkText(t, conn); !regexp.MustCompile(`Steering mode: one-at-a-time`).MatchString(got) {
			t.Errorf("text = %q", got)
		}
	})
	tw(t, "unit/builtin-commands", "PiAcpAgent: /name sets session display name adapter-side", func(t *testing.T) {
		proc := newFakeProc()
		var setTo string
		proc.setSessionNameFn = func(n string) error { setTo = n; return nil }
		a, conn := modeAgent(map[string]any{}, proc)
		res, err := a.Prompt(promptText("s1", "/name My Session"))
		if err != nil || res.StopReason != StopEndTurn {
			t.Fatalf("res=%v err=%v", res, err)
		}
		if n := len(proc.promptList()); n != 0 || setTo != "My Session" {
			t.Errorf("prompts=%d setTo=%q", n, setTo)
		}
		info := conn.ofKind("session_info_update")
		if len(info) == 0 || info[0].Update["title"] != "My Session" {
			t.Errorf("session_info_update = %v", info)
		}
		if got := lastChunkText(t, conn); !regexp.MustCompile(`Session name set: My Session`).MatchString(got) {
			t.Errorf("text = %q", got)
		}
	})
}

func TestEmbeddedContextFlag(t *testing.T) {
	initWith := func(t *testing.T, key, value string, set bool) bool {
		t.Helper()
		t.Setenv("PI_ACP_ENABLE_EMBEDDED_CONTEXT", "")
		t.Setenv("PIG_ACP_ENABLE_EMBEDDED_CONTEXT", "")
		if set {
			t.Setenv(key, value)
		}
		a, _ := testAgent(newFakeConn())
		res, err := a.Initialize(InitializeRequest{ProtocolVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		return res.AgentCapabilities.PromptCapabilities.EmbeddedContext
	}
	tw(t, "unit/pi-enable-embed-context-flag", "PI_ACP_ENABLE_EMBEDDED_CONTEXT: defaults embeddedContext to false when undefined", func(t *testing.T) {
		if initWith(t, "", "", false) {
			t.Error("enabled by default")
		}
	})
	tw(t, "unit/pi-enable-embed-context-flag", "PI_ACP_ENABLE_EMBEDDED_CONTEXT: 'false' keeps embeddedContext disabled", func(t *testing.T) {
		if initWith(t, "PI_ACP_ENABLE_EMBEDDED_CONTEXT", "false", true) {
			t.Error("enabled")
		}
	})
	tw(t, "unit/pi-enable-embed-context-flag", "PI_ACP_ENABLE_EMBEDDED_CONTEXT: 'true' enables embeddedContext", func(t *testing.T) {
		if !initWith(t, "PI_ACP_ENABLE_EMBEDDED_CONTEXT", "true", true) {
			t.Error("not enabled")
		}
	})
	// PiG spelling: PIG_ACP_* is the name, PI_ACP_* stays accepted (Pi compatibility rule).
	t.Run("PIG_ACP_ENABLE_EMBEDDED_CONTEXT: 'true' enables embeddedContext", func(t *testing.T) {
		if !initWith(t, "PIG_ACP_ENABLE_EMBEDDED_CONTEXT", "true", true) {
			t.Error("not enabled")
		}
	})
}

func TestSessionThinkingModes(t *testing.T) {
	tw(t, "component/session-thinking-modes", "PiAcpAgent: setSessionMode maps to pi setThinkingLevel + emits current_mode_update", func(t *testing.T) {
		// The original only asserts that an unknown session is rejected as invalid params.
		a, _ := testAgent(newFakeConn())
		a.sessions = NewSessionManager(nil, newMemStore())
		a.store = newMemStore()
		_, err := a.SetSessionMode(SetSessionModeRequest{SessionID: "nope", ModeID: "invalid"})
		re, ok := err.(*RequestError)
		if !ok || re.Code != -32602 || !regexp.MustCompile(`(?i)invalid params`).MatchString(re.Message) {
			t.Fatalf("err = %v", err)
		}
	})
}
