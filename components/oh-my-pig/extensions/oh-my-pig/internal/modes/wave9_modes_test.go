package modes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ── 1. /prewalk tests ────────────────────────────────────────────────────────

func TestPrewalkDefaultOffZeroHookWork(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	Setting = nil

	_, h := newTestExtension(t, hostOptions{
		SessionID: "s-prewalk-default",
	})

	// SessionStart with prewalk.enabled=false (default) must NOT arm prewalk or switch models.
	h.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
	res := h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"type":                "before_agent_start",
		"systemPromptOptions": map[string]any{"cwd": h.opts.Cwd},
	})
	var env0 struct {
		Result any `json:"_pigPromptResult"`
	}
	if err := json.Unmarshal(res, &env0); err != nil {
		t.Fatalf("unmarshal before_agent_start: %v (%s)", err, string(res))
	}
	if env0.Result != nil {
		t.Fatalf("expected nil _pigPromptResult on default session, got %#v", env0.Result)
	}
	h.fireEvent(sdk.EventToolResult, map[string]any{
		"type":     "tool_result",
		"toolName": "edit",
		"isError":  false,
	})
	h.fireEvent(sdk.EventTurnEnd, map[string]any{
		"type":    "turn_end",
		"message": map[string]any{"role": "assistant", "stopReason": "stop"},
		"toolResults": []any{
			map[string]any{"toolName": "edit", "isError": false},
		},
	})
	if len(h.callsTo("setModel")) != 0 {
		t.Fatalf("expected zero setModel calls on default session, got %d", len(h.callsTo("setModel")))
	}
	if len(h.callsTo("sendMessage")) != 0 {
		t.Fatalf("expected zero sendMessage calls on default session, got %d", len(h.callsTo("sendMessage")))
	}
}

func TestPrewalkLifecycleTodoGatedHandoffAndRestartOffStatus(t *testing.T) {
	origSetting := Setting
	origResolve := ResolveRole
	t.Cleanup(func() {
		Setting = origSetting
		ResolveRole = origResolve
	})

	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "prewalk.from":
			return "@default"
		case "prewalk.into":
			return "@smol"
		}
		return nil
	}
	ResolveRole = func(_ sdk.Context, selector string) (string, string, string, bool) {
		switch selector {
		case "@smol":
			return "openai", "gpt-5-mini", "low", true
		case "@default":
			return "anthropic", "claude-opus-4-6", "high", true
		}
		return "", "", "", false
	}

	var switchedModels []string
	_, h := newTestExtension(t, hostOptions{
		SessionID:     "s-prewalk-1",
		ModelProvider: "anthropic",
		Model:         "claude-opus-4-6",
		ThinkingLevel: "high",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "resolveModel" {
				spec, _ := args["spec"].(string)
				prov, id := "", spec
				if idx := strings.IndexByte(spec, '/'); idx > 0 {
					prov, id = spec[:idx], spec[idx+1:]
				}
				return map[string]any{"model": map[string]any{"provider": prov, "id": id}}, ""
			}
			if method == "setModel" {
				if m, ok := args["model"].(map[string]any); ok {
					p, _ := m["provider"].(string)
					id, _ := m["id"].(string)
					switchedModels = append(switchedModels, p+"/"+id)
				}
				return map[string]any{"success": true}, ""
			}
			return nil, ""
		},
	})

	// 1. Arm via `/prewalk` (defaults to prewalk.into = @smol).
	h.callCommand("prewalk", "")
	if got := h.lastNotify(); !strings.Contains(got, "Prewalk on: switching to openai/gpt-5-mini") {
		t.Fatalf("unexpected /prewalk notify: %q", got)
	}

	// Status reports armed.
	h.callCommand("prewalk", "status")
	if got := h.lastNotify(); !strings.Contains(got, "armed for openai/gpt-5-mini") {
		t.Fatalf("unexpected /prewalk status: %q", got)
	}

	// 2. First before_agent_start injects hidden prewalk-plan.md.
	rawBefore := h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"type":                "before_agent_start",
		"systemPromptOptions": map[string]any{"cwd": h.opts.Cwd},
	})
	var beforeRes struct {
		Result struct {
			Message struct {
				CustomType string `json:"customType"`
				Content    string `json:"content"`
				Display    bool   `json:"display"`
			} `json:"message"`
		} `json:"_pigPromptResult"`
	}
	if err := json.Unmarshal(rawBefore, &beforeRes); err != nil {
		t.Fatalf("unmarshal before_agent_start: %v (%s)", err, string(rawBefore))
	}
	if beforeRes.Result.Message.CustomType != prewalkPlanMessageType || beforeRes.Result.Message.Display {
		t.Fatalf("expected hidden prewalk-plan message, got %+v", beforeRes.Result.Message)
	}
	if !strings.Contains(beforeRes.Result.Message.Content, "write complete plan") {
		t.Fatalf("prewalk-plan content missing expected text: %q", beforeRes.Result.Message.Content)
	}

	// 3. Edit BEFORE todo must NOT hand off yet (todo-gated).
	h.fireEvent(sdk.EventToolResult, map[string]any{
		"type":     "tool_result",
		"toolName": "edit",
		"isError":  false,
	})
	h.fireEvent(sdk.EventTurnEnd, map[string]any{
		"type":    "turn_end",
		"message": map[string]any{"role": "assistant", "stopReason": "stop"},
		"toolResults": []any{
			map[string]any{"toolName": "edit", "isError": false},
		},
	})
	if len(switchedModels) != 0 {
		t.Fatalf("expected no model switch before todo tool, got %v", switchedModels)
	}

	// 4. Non-error todo + write with touchesNoFiles:true must NOT hand off.
	h.fireEvent(sdk.EventToolResult, map[string]any{
		"type":     "tool_result",
		"toolName": "todo",
		"isError":  false,
	})
	h.fireEvent(sdk.EventToolResult, map[string]any{
		"type":     "tool_result",
		"toolName": "write",
		"isError":  false,
		"details":  map[string]any{"touchesNoFiles": true},
	})
	h.fireEvent(sdk.EventTurnEnd, map[string]any{
		"type":    "turn_end",
		"message": map[string]any{"role": "assistant", "stopReason": "stop"},
		"toolResults": []any{
			map[string]any{"toolName": "todo", "isError": false},
			map[string]any{"toolName": "write", "isError": false, "details": map[string]any{"touchesNoFiles": true}},
		},
	})
	if len(switchedModels) != 0 {
		t.Fatalf("touchesNoFiles write must not trigger prewalk handoff, got %v", switchedModels)
	}

	// 5. Mutating bash tool command (edit/write/ast_edit dispatch) triggers handoff!
	h.fireEvent(sdk.EventTurnEnd, map[string]any{
		"type":    "turn_end",
		"message": map[string]any{"role": "assistant", "stopReason": "stop"},
		"toolResults": []any{
			map[string]any{
				"toolName": "bash",
				"isError":  false,
				"details": map[string]any{
					"toolCommands": []any{
						map[string]any{"tool": "ast_edit", "mode": "execute", "exitCode": 0},
					},
				},
			},
		},
	})
	if len(switchedModels) != 1 || switchedModels[0] != "openai/gpt-5-mini" {
		t.Fatalf("expected handoff to openai/gpt-5-mini, got %v", switchedModels)
	}
	h.mu.Lock()
	gotThinking := h.thinkingLevel
	h.mu.Unlock()
	if gotThinking != "low" {
		t.Fatalf("expected thinking level set to low, got %q", gotThinking)
	}
	// Verify prewalk-checklist was steered.
	foundChecklist := false
	for _, c := range h.callsTo("sendMessage") {
		if msg, ok := c.Args["message"].(map[string]any); ok {
			if ct, _ := msg["customType"].(string); ct == prewalkChecklistMessageType {
				foundChecklist = true
			}
		}
	}
	if !foundChecklist {
		t.Fatalf("expected prewalk-checklist steer message after handoff")
	}

	// Status reports handed off.
	h.callCommand("prewalk", "status")
	if got := h.lastNotify(); !strings.Contains(got, "handed off to openai/gpt-5-mini") {
		t.Fatalf("unexpected handed-off status: %q", got)
	}

	// 6. `/prewalk restart` switches back to @default and re-arms @smol.
	h.callCommand("prewalk", "restart")
	if len(switchedModels) != 2 || switchedModels[1] != "anthropic/claude-opus-4-6" {
		t.Fatalf("expected restart switch back to anthropic/claude-opus-4-6, got %v", switchedModels)
	}
	if got := h.lastNotify(); !strings.Contains(got, "Prewalk restarted:") {
		t.Fatalf("unexpected restart notify: %q", got)
	}

	// 7. `/prewalk off` disarms and marks suppressed.
	h.callCommand("prewalk", "off")
	if got := h.lastNotify(); !strings.Contains(got, "Prewalk off: canceled") {
		t.Fatalf("unexpected off notify: %q", got)
	}
	h.callCommand("prewalk", "status")
	if got := h.lastNotify(); !strings.Contains(got, "suppressed") {
		t.Fatalf("expected suppressed status after /prewalk off, got %q", got)
	}
}

func TestPrewalkEnabledOnSessionStart(t *testing.T) {
	origSetting := Setting
	origResolve := ResolveRole
	t.Cleanup(func() {
		Setting = origSetting
		ResolveRole = origResolve
	})
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "prewalk.enabled":
			return true
		case "prewalk.into":
			return "@smol"
		}
		return nil
	}
	ResolveRole = func(_ sdk.Context, selector string) (string, string, string, bool) {
		if selector == "@smol" {
			return "openai", "gpt-5-mini", "", true
		}
		return "", "", "", false
	}

	m, h := newTestExtension(t, hostOptions{
		SessionID:     "s-prewalk-autostart",
		ModelProvider: "anthropic",
		Model:         "claude-opus-4-6",
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})

	m.mu.Lock()
	armed := m.stateLocked("s-prewalk-autostart").prewalk.armed
	m.mu.Unlock()
	if !armed {
		t.Fatalf("expected prewalk armed on session start when prewalk.enabled=true")
	}

	// Cleanup via session_shutdown releases per-session state.
	h.fireEvent(sdk.EventSessionShutdown, map[string]any{"type": "session_shutdown"})
	m.mu.Lock()
	_, stillExists := m.sessions["s-prewalk-autostart"]
	m.mu.Unlock()
	if stillExists {
		t.Fatalf("expected session state cleaned up on session_shutdown")
	}
}

// ── 2. /omfg tests ───────────────────────────────────────────────────────────

func TestOmfgParsingValidationAndNameSanitization(t *testing.T) {
	// Traversal / slash names MUST be rejected.
	for _, bad := range []string{"../escape", "foo/bar", `a\b`, "", ".."} {
		if _, err := SanitizeOmfgRuleName(bad); err == nil {
			t.Errorf("SanitizeOmfgRuleName(%q) expected error, got nil", bad)
		}
		if err := ValidateOmfgRuleName(bad); err == nil {
			t.Errorf("ValidateOmfgRuleName(%q) expected error, got nil", bad)
		}
	}

	// Valid kebab-case and normalization.
	gotName, err := SanitizeOmfgRuleName("No_Eval_Calls")
	if err != nil || gotName != "no-eval-calls" {
		t.Fatalf("SanitizeOmfgRuleName = %q, %v", gotName, err)
	}

	// Balanced JSON extraction from fenced markdown.
	fenced := "Here you go:\n```json\n{\"name\":\"ts-no-any\",\"description\":\"Avoid any\",\"condition\":\": any\",\"scope\":[\"tool:edit(*.ts)\"],\"body\":\"Use unknown.\"}\n```\nDone."
	parsed, err := ParseGeneratedOmfgRule(fenced)
	if err != nil {
		t.Fatalf("ParseGeneratedOmfgRule failed: %v", err)
	}
	if parsed.Payload.Name != "ts-no-any" {
		t.Fatalf("unexpected name: %q", parsed.Payload.Name)
	}
	if !strings.Contains(parsed.FileContent, "---\nname: ts-no-any\n") || !strings.Contains(parsed.FileContent, "Use unknown.") {
		t.Fatalf("unexpected rule markdown:\n%s", parsed.FileContent)
	}

	// Invalid regex rejected cleanly.
	badRegex := `{"name":"bad-re","description":"d","condition":"[unclosed","scope":"text","body":"fix"}`
	if _, err := ParseGeneratedOmfgRule(badRegex); err == nil {
		t.Fatalf("expected error for invalid Go RE2 regex")
	}
}

func TestOmfgCommandRetryValidationAndSave(t *testing.T) {
	origComplete := completeOmfg
	origAgentDir := AgentDirForContext
	t.Cleanup(func() {
		completeOmfg = origComplete
		AgentDirForContext = origAgentDir
	})

	cwd := t.TempDir()
	agentDir := t.TempDir()
	AgentDirForContext = func(_ sdk.Context) string { return agentDir }

	attempts := 0
	var secondPrompt string
	completeOmfg = func(_ sdk.Context, _ map[string]any, req map[string]any, _ map[string]any) (json.RawMessage, error) {
		attempts++
		if attempts == 1 {
			// Attempt 1: regex does NOT match history -> should trigger retry with feedback.
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"name\":\"no-foo\",\"description\":\"Never write foo\",\"condition\":\"does-not-exist-xyz\",\"scope\":\"text\",\"body\":\"Avoid foo.\"}"}]}`), nil
		}
		if msgs, ok := req["messages"].([]map[string]any); ok && len(msgs) > 0 {
			if content, ok := msgs[len(msgs)-1]["content"].([]map[string]any); ok && len(content) > 0 {
				secondPrompt, _ = content[0]["text"].(string)
			}
		}
		// Attempt 2: matches assistant prose in branch.
		return json.RawMessage(`{"content":[{"type":"text","text":"{\"name\":\"no-eval-claims\",\"description\":\"Stop saying trust me\",\"condition\":\"trust me\",\"scope\":\"text\",\"body\":\"Show command output instead of saying trust me.\"}"}]}`), nil
	}

	selectedOption := omfgProjectOption
	overwriteConfirmed := true
	confirmCalls := 0

	_, h := newTestExtension(t, hostOptions{
		SessionID: "s-omfg-1",
		Cwd:       cwd,
		Branch: []map[string]any{
			{
				"id":   "a1",
				"type": "message",
				"message": map[string]any{
					"role":    "assistant",
					"content": []any{map[string]any{"type": "text", "text": "All tests pass, trust me!"}},
				},
			},
		},
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				return map[string]any{"ok": true, "selected": selectedOption}, ""
			case "ui.confirm":
				confirmCalls++
				return map[string]any{"confirmed": overwriteConfirmed}, ""
			}
			return nil, ""
		},
	})

	// Empty complaint -> usage notice.
	h.callCommand("omfg", "   ")
	if got := h.lastNotify(); got != "Usage: /omfg <complaint>" {
		t.Fatalf("empty /omfg notify = %q, want Usage: /omfg <complaint>", got)
	}

	// Valid complaint -> retries once, validates against history, saves to project .omp/rules/.
	h.callCommand("omfg", "stop saying trust me without running tests")
	if attempts != 2 {
		t.Fatalf("expected 2 completion attempts, got %d", attempts)
	}
	if !strings.Contains(secondPrompt, "Attempt 1 failed validation") {
		t.Fatalf("expected retry feedback in attempt 2 prompt, got: %q", secondPrompt)
	}
	projFile := filepath.Join(cwd, ".omp", "rules", "no-eval-claims.md")
	data, err := os.ReadFile(projFile)
	if err != nil {
		t.Fatalf("expected project rule file at %s: %v", projFile, err)
	}
	if !strings.Contains(string(data), "name: no-eval-claims") || !strings.Contains(string(data), `condition: "trust me"`) {
		t.Fatalf("unexpected project rule content:\n%s", string(data))
	}

	// Second save to same project path prompts overwrite confirmation.
	attempts = 1 // return valid on first call
	overwriteConfirmed = false
	h.callCommand("omfg", "stop saying trust me")
	if confirmCalls != 1 {
		t.Fatalf("expected 1 overwrite confirmation call, got %d", confirmCalls)
	}
	if got := h.lastNotify(); !strings.Contains(got, "Cancelled overwriting") {
		t.Fatalf("expected overwrite cancellation notice, got %q", got)
	}

	// Save to global <agentDir>/rules/<name>.md.
	attempts = 1
	selectedOption = omfgGlobalOption
	h.callCommand("omfg", "stop saying trust me")
	globalFile := filepath.Join(agentDir, "rules", "no-eval-claims.md")
	if _, err := os.Stat(globalFile); err != nil {
		t.Fatalf("expected global rule file at %s: %v", globalFile, err)
	}

	// Headless / non-UI session returns clean error notice without calling Complete.
	hasUI := false
	_, hHeadless := newTestExtension(t, hostOptions{
		SessionID: "s-omfg-headless",
		HasUI:     &hasUI,
	})
	hHeadless.callCommand("omfg", "stop doing X")
	if got := hHeadless.lastNotify(); !strings.Contains(got, "interactive session") {
		t.Fatalf("expected headless error notice, got %q", got)
	}
}

// ── 3. Goal todo_context tests ───────────────────────────────────────────────

func TestGoalTodoContextInjection(t *testing.T) {
	origTodo := TodoContextSnapshot
	origSetting := Setting
	t.Cleanup(func() {
		TodoContextSnapshot = origTodo
		Setting = origSetting
	})
	Setting = nil

	TodoContextSnapshot = func(_ sdk.Context) (bool, []GoalTodoPhase) {
		return true, []GoalTodoPhase{
			{
				Name: "Phase <1> & Setup",
				Tasks: []GoalTodoTask{
					{Status: "completed", Content: "Done <item>"},
					{Status: "in_progress", Content: "Work on A & B\nnext line"},
					{Status: "pending", Content: "Verify"},
				},
			},
			{
				Name:  "Empty Phase",
				Tasks: nil,
			},
		}
	}

	_, h := newTestExtension(t, hostOptions{
		SessionID: "s-goal-todo-2",
	})
	h.callCommand("goal", "Ship feature")

	raw := h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"type":                "before_agent_start",
		"systemPromptOptions": map[string]any{"cwd": h.opts.Cwd},
	})
	var res struct {
		Result struct {
			Message struct {
				CustomType string `json:"customType"`
				Content    string `json:"content"`
			} `json:"message"`
		} `json:"_pigPromptResult"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal before_agent_start: %v", err)
	}
	if res.Result.Message.CustomType != "goal-mode-context" {
		t.Fatalf("expected goal-mode-context, got %q", res.Result.Message.CustomType)
	}
	c := res.Result.Message.Content
	for _, want := range []string{
		"<todo_context>",
		"Overall: 1/3 done, 2 open.",
		"- Phase &lt;1&gt; &amp; Setup",
		"  - [completed] Done &lt;item&gt;",
		`  - [in_progress] Work on A &amp; B\nnext line`,
		"  - [pending] Verify",
		"</todo_context>",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("goal-mode-context missing %q in:\n%s", want, c)
		}
	}
	if strings.Contains(c, "Empty Phase") {
		t.Errorf("empty phase should be omitted from todo_context:\n%s", c)
	}

	// When TodoContextSnapshot returns active=false, <todo_context> is omitted.
	TodoContextSnapshot = func(_ sdk.Context) (bool, []GoalTodoPhase) {
		return false, nil
	}
	raw2 := h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"type":                "before_agent_start",
		"systemPromptOptions": map[string]any{"cwd": h.opts.Cwd},
	})
	if strings.Contains(string(raw2), "<todo_context>") {
		t.Fatalf("expected no <todo_context> when inactive, got %s", string(raw2))
	}
}

// ── 4. btw.inject tests ──────────────────────────────────────────────────────

func TestBtwInjectSettingAndSteering(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })

	// Unit tests for ExtractBtwInject.
	if got := ExtractBtwInject("Prose <inject>Pause and fix tests.</inject> trailing"); got != "Pause and fix tests." {
		t.Fatalf("ExtractBtwInject = %q", got)
	}
	if got := ExtractBtwInject("Unclosed <inject>never closed"); got != "" {
		t.Fatalf("unclosed inject should return empty, got %q", got)
	}
	if got := ExtractBtwInject("First <inject>old</inject> second <inject>new prompt</inject>"); got != "new prompt" {
		t.Fatalf("expected last closed inject block, got %q", got)
	}

	// Default (btw.inject = false): prompt omits inject instructions, <inject> is NOT captured.
	Setting = nil
	var capturedPrompt string
	_, hOff := newTestExtension(t, hostOptions{
		SessionID: "s-btw-off",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "complete" {
				if req, ok := args["request"].(map[string]any); ok {
					if msgs, ok := req["messages"].([]any); ok && len(msgs) > 0 {
						if last, ok := msgs[len(msgs)-1].(map[string]any); ok {
							if parts, ok := last["content"].([]any); ok && len(parts) > 0 {
								if p, ok := parts[0].(map[string]any); ok {
									capturedPrompt, _ = p["text"].(string)
								}
							}
						}
					}
				}
				return map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "Here: <inject>Steer me</inject>"}},
				}, ""
			}
			return nil, ""
		},
	})
	hOff.callCommand("btw", "draft a steering prompt")
	if strings.Contains(capturedPrompt, "<inject>") {
		t.Fatalf("btw-user.md should omit <inject> instructions when btw.inject=false:\n%s", capturedPrompt)
	}
	hOff.callCommand("btw", "inject")
	if got := hOff.lastNotify(); got != "No drafted <inject> prompt from /btw." {
		t.Fatalf("expected no drafted prompt when btw.inject=false, got %q", got)
	}

	// btw.inject = true: prompt includes <inject> instructions, captures block, `/btw inject` steers once and clears.
	Setting = func(_ sdk.Context, key string) any {
		if key == "btw.inject" {
			return true
		}
		return nil
	}
	capturedPrompt = ""
	_, hOn := newTestExtension(t, hostOptions{
		SessionID: "s-btw-on",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "complete" {
				if req, ok := args["request"].(map[string]any); ok {
					if msgs, ok := req["messages"].([]any); ok && len(msgs) > 0 {
						if last, ok := msgs[len(msgs)-1].(map[string]any); ok {
							if parts, ok := last["content"].([]any); ok && len(parts) > 0 {
								if p, ok := parts[0].(map[string]any); ok {
									capturedPrompt, _ = p["text"].(string)
								}
							}
						}
					}
				}
				return map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "Sure:\n<inject>Stop refactoring and run go test first.</inject>"}},
				}, ""
			}
			return nil, ""
		},
	})
	hOn.callCommand("btw", "tell the agent to stop refactoring")
	if !strings.Contains(capturedPrompt, "<inject>") {
		t.Fatalf("btw-user.md should include <inject> instructions when btw.inject=true:\n%s", capturedPrompt)
	}

	hOn.callCommand("btw", "inject")
	userMsgs := hOn.callsTo("sendUserMessage")
	if len(userMsgs) != 1 {
		t.Fatalf("expected 1 sendUserMessage call, got %d", len(userMsgs))
	}
	if content, _ := userMsgs[0].Args["content"].(string); content != "Stop refactoring and run go test first." {
		t.Fatalf("steered content = %q", content)
	}
	optsMap, _ := userMsgs[0].Args["options"].(map[string]any)
	if deliverAs, _ := optsMap["deliverAs"].(string); deliverAs != "steer" {
		t.Fatalf("deliverAs = %q, want steer (args=%#v)", deliverAs, userMsgs[0].Args)
	}

	// Second `/btw inject` finds nothing (cleared after first send).
	hOn.callCommand("btw", "inject")
	if got := hOn.lastNotify(); got != "No drafted <inject> prompt from /btw." {
		t.Fatalf("expected cleared notice on second /btw inject, got %q", got)
	}
}

// ── 5. plan.defaultOnStartup tests ───────────────────────────────────────────

func TestPlanDefaultOnStartup(t *testing.T) {
	origSetting := Setting
	origIsSub := IsSubagent
	t.Cleanup(func() {
		Setting = origSetting
		IsSubagent = origIsSub
	})

	// 1. Default (plan.defaultOnStartup = false): zero effect.
	Setting = nil
	IsSubagent = nil
	m0, h0 := newTestExtension(t, hostOptions{SessionID: "s-startup-default", Mode: "tui"})
	h0.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
	m0.mu.Lock()
	on0 := m0.stateLocked("s-startup-default").planEnabled
	m0.mu.Unlock()
	if on0 {
		t.Fatalf("plan mode must remain off when plan.defaultOnStartup is default false")
	}

	// 2. plan.defaultOnStartup = true && plan.enabled = true on fresh TUI session -> enters plan mode.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "plan.defaultOnStartup":
			return true
		case "plan.enabled":
			return true
		}
		return nil
	}
	m1, h1 := newTestExtension(t, hostOptions{SessionID: "s-startup-on", Mode: "tui"})
	h1.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
	m1.mu.Lock()
	on1 := m1.stateLocked("s-startup-on").planEnabled
	m1.mu.Unlock()
	if !on1 {
		t.Fatalf("expected plan mode auto-entered on fresh TUI session")
	}

	// 3. Non-empty branch (existing conversation) -> does NOT auto-enter plan mode.
	m2, h2 := newTestExtension(t, hostOptions{
		SessionID: "s-startup-nonempty",
		Mode:      "tui",
		Branch: []map[string]any{
			{"id": "u1", "type": "message", "message": map[string]any{"role": "user", "content": "hi"}},
		},
	})
	h2.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
	m2.mu.Lock()
	on2 := m2.stateLocked("s-startup-nonempty").planEnabled
	m2.mu.Unlock()
	if on2 {
		t.Fatalf("must not auto-enter plan mode when branch already has messages")
	}

	// 4. Non-TUI mode (e.g. rpc/print) -> does NOT auto-enter plan mode.
	m3, h3 := newTestExtension(t, hostOptions{SessionID: "s-startup-rpc", Mode: "rpc"})
	h3.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
	m3.mu.Lock()
	on3 := m3.stateLocked("s-startup-rpc").planEnabled
	m3.mu.Unlock()
	if on3 {
		t.Fatalf("must not auto-enter plan mode in non-tui mode")
	}

	// 5. Subagent -> does NOT auto-enter plan mode.
	IsSubagent = func(_ sdk.Context) bool { return true }
	m4, h4 := newTestExtension(t, hostOptions{SessionID: "s-startup-sub", Mode: "tui"})
	h4.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
	m4.mu.Lock()
	on4 := m4.stateLocked("s-startup-sub").planEnabled
	m4.mu.Unlock()
	if on4 {
		t.Fatalf("must not auto-enter plan mode in subagent session")
	}
	IsSubagent = nil

	// 6. plan.enabled = false overrides plan.defaultOnStartup = true.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "plan.defaultOnStartup":
			return true
		case "plan.enabled":
			return false
		}
		return nil
	}
	m5, h5 := newTestExtension(t, hostOptions{SessionID: "s-startup-plan-disabled", Mode: "tui"})
	h5.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
	m5.mu.Lock()
	on5 := m5.stateLocked("s-startup-plan-disabled").planEnabled
	m5.mu.Unlock()
	if on5 {
		t.Fatalf("must not auto-enter plan mode when plan.enabled=false")
	}
}

func TestOmfgSymlinkRefusedAndReservedNamesAndUnescapedHTML_M5_L15(t *testing.T) {
	// L15: Windows reserved device names rejected.
	for _, bad := range []string{"con", "nul", "prn", "aux", "com1", "lpt9"} {
		if err := ValidateOmfgRuleName(bad); err == nil {
			t.Fatalf("expected reserved name %q to be rejected", bad)
		}
	}

	// L15: YAML frontmatter strings must not HTML-escape <, >, &.
	md := AssembleOmfgRuleMarkdown(OmfgRulePayload{
		Name:        "no-html-escape",
		Description: "Check a < b && c > d",
		Condition:   []string{"a<b&c>d"},
		Scope:       []string{"tool:edit"},
		Body:        "Keep it clean.",
	})
	if strings.Contains(md, `\u003c`) || strings.Contains(md, `\u003e`) || strings.Contains(md, `\u0026`) {
		t.Fatalf("frontmatter HTML-escaped characters unexpectedly:\n%s", md)
	}
	if !strings.Contains(md, `"a<b&c>d"`) {
		t.Fatalf("expected raw <>& in condition, got:\n%s", md)
	}

	// M5: Symlinked .omp/rules must be refused.
	cwd := t.TempDir()
	outside := t.TempDir()
	ompDir := filepath.Join(cwd, ".omp")
	if err := os.MkdirAll(ompDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ompDir, "rules")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	if _, err := validateProjectRulesDir(cwd); err == nil {
		t.Fatal("expected validateProjectRulesDir to refuse symlinked .omp/rules")
	}
}

func TestPrewalkRejectsBogusSelectorAndClearsPendingOnAbort_L9_L10_L11(t *testing.T) {
	origResolve := ResolveRole
	ResolveRole = nil
	t.Cleanup(func() { ResolveRole = origResolve })

	// L11: bogus bare selector without provider/model fails immediately at arm time.
	if _, err := resolvePrewalkSelector(sdk.Context{}, "bogus-model-name"); err == nil {
		t.Fatal("expected error for bogus non-role selector without provider/model")
	}
	if _, err := resolvePrewalkSelector(sdk.Context{}, "openai/gpt-5-mini"); err != nil {
		t.Fatalf("expected provider/model selector to succeed, got %v", err)
	}

	// L9: pendingAction cleared on aborted turn_end.
	m, h := newTestExtension(t, hostOptions{SessionID: "s-prewalk-abort", Mode: "tui"})
	m.mu.Lock()
	st := m.stateLocked("s-prewalk-abort")
	st.prewalk = prewalkState{
		gen:           1,
		armed:         true,
		target:        prewalkTarget{Selector: "openai/gpt-5-mini", Provider: "openai", ModelID: "gpt-5-mini"},
		todoSeen:      true,
		pendingAction: "edit",
	}
	m.mu.Unlock()

	h.fireEvent(sdk.EventTurnEnd, map[string]any{
		"message": map[string]any{"role": "assistant", "stopReason": "aborted"},
	})
	m.mu.Lock()
	pending := m.stateLocked("s-prewalk-abort").prewalk.pendingAction
	m.mu.Unlock()
	if pending != "" {
		t.Fatalf("expected pendingAction cleared on aborted turn, got %q", pending)
	}
}

func TestSanitizeBtwInjectText_L14(t *testing.T) {
	raw := "hello \x1b[31mred\x1b[0m\x07\x00\n\tworld"
	got := sanitizeBtwInjectText(raw)
	if got != "hello red\n\tworld" {
		t.Fatalf("unexpected sanitized inject text: %q", got)
	}
}
