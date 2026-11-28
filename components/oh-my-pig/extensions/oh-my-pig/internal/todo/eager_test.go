package todo

import (
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
)

func TestWave5TodoEagerModesAndGuards(t *testing.T) {
	origSetting := Setting
	origForced := SetForcedToolChoice
	t.Cleanup(func() {
		Setting = origSetting
		SetForcedToolChoice = origForced
		gate.ResetForTests()
	})

	beforePayload := func(prompt string) map[string]any {
		return map[string]any{
			"prompt":              prompt,
			"systemPrompt":        "",
			"systemPromptOptions": map[string]any{},
		}
	}
	extractPrelude := func(res map[string]any) map[string]any {
		if res == nil {
			return nil
		}
		if inner, ok := res["_pigPromptResult"].(map[string]any); ok {
			if msg, ok := inner["message"].(map[string]any); ok {
				return msg
			}
			return nil
		}
		if msg, ok := res["message"].(map[string]any); ok {
			return msg
		}
		return nil
	}

	// 1. Default ("default") is a strict no-op: no prelude message, no forced tool choice.
	Setting = nil
	var mu sync.Mutex
	var forcedCalls []string
	SetForcedToolChoice = func(sessionID, toolName string) {
		mu.Lock()
		forcedCalls = append(forcedCalls, sessionID+":"+toolName)
		mu.Unlock()
	}

	ext := sdk.New("todo-eager-default")
	Register(ext)
	hDefault := startHost(t, ext, hostOptions{
		SessionID:   "sess-eager-default",
		ActiveTools: []string{"read", "edit", "todo"},
	})
	res, errMsg := hDefault.emitEvent(sdk.EventBeforeAgentStart, beforePayload("Implement feature X across all packages."))
	if errMsg != "" || extractPrelude(res) != nil {
		t.Fatalf("todo.eager=default must be strict no-op, got res=%#v err=%q", res, errMsg)
	}
	mu.Lock()
	if len(forcedCalls) != 0 {
		mu.Unlock()
		t.Fatalf("todo.eager=default must not arm SetForcedToolChoice, got %v", forcedCalls)
	}
	mu.Unlock()

	// 2. "preferred": injects non-forced eager-todo-prelude, does NOT arm SetForcedToolChoice.
	Setting = func(_ sdk.Context, key string) any {
		if key == "todo.eager" {
			return "preferred"
		}
		return nil
	}
	extPref := sdk.New("todo-eager-preferred")
	Register(extPref)
	hPref := startHost(t, extPref, hostOptions{
		SessionID:   "sess-eager-preferred",
		ActiveTools: []string{"read", "edit", "todo"},
	})
	res, errMsg = hPref.emitEvent(sdk.EventBeforeAgentStart, beforePayload("Implement feature X across all packages."))
	msg := extractPrelude(res)
	if errMsg != "" || msg == nil {
		t.Fatalf("todo.eager=preferred should return prelude, got res=%#v err=%q", res, errMsg)
	}
	if msg["customType"] != "eager-todo-prelude" || msg["display"] != false {
		t.Fatalf("unexpected prelude message metadata: %#v", msg)
	}
	content, _ := msg["content"].(string)
	if !strings.Contains(content, "Consider calling `todo` first") || strings.Contains(content, "You MUST call `todo` first") {
		t.Fatalf("expected non-forced eager-todo prelude, got:\n%s", content)
	}
	if strings.Contains(content, "{{") {
		t.Fatalf("unexpanded template tokens in rendered prelude:\n%s", content)
	}
	mu.Lock()
	if len(forcedCalls) != 0 {
		mu.Unlock()
		t.Fatalf("todo.eager=preferred must not arm SetForcedToolChoice, got %v", forcedCalls)
	}
	mu.Unlock()

	// 3. "always": injects forced eager-todo-prelude AND calls SetForcedToolChoice(sid, "todo").
	Setting = func(_ sdk.Context, key string) any {
		if key == "todo.eager" {
			return "always"
		}
		return nil
	}
	extAlways := sdk.New("todo-eager-always")
	Register(extAlways)
	hAlways := startHost(t, extAlways, hostOptions{
		SessionID:   "sess-eager-always",
		ActiveTools: []string{"read", "edit", "todo"},
	})
	res, errMsg = hAlways.emitEvent(sdk.EventBeforeAgentStart, beforePayload("Refactor the billing pipeline."))
	msg = extractPrelude(res)
	if errMsg != "" || msg == nil {
		t.Fatalf("todo.eager=always should return prelude, got res=%#v err=%q", res, errMsg)
	}
	content, _ = msg["content"].(string)
	if !strings.Contains(content, "You MUST call `todo` first in this turn.") || strings.Contains(content, "Consider calling `todo` first") {
		t.Fatalf("expected forced eager-todo prelude, got:\n%s", content)
	}
	mu.Lock()
	gotForced := append([]string(nil), forcedCalls...)
	mu.Unlock()
	if len(gotForced) != 1 || gotForced[0] != "sess-eager-always:todo" {
		t.Fatalf("expected SetForcedToolChoice(sess-eager-always, todo), got %v", gotForced)
	}

	// 4. Prompt ending in `?` or `!` skips eager prelude and forced tool_choice.
	for _, prompt := range []string{"What does this function do?", "Where is the config?   ", "Stop right now!"} {
		res, errMsg = hAlways.emitEvent(sdk.EventBeforeAgentStart, beforePayload(prompt))
		if errMsg != "" || extractPrelude(res) != nil {
			t.Fatalf("prompt %q should skip eager prelude, got res=%#v err=%q", prompt, res, errMsg)
		}
	}
	mu.Lock()
	if len(forcedCalls) != 1 {
		mu.Unlock()
		t.Fatalf("? / ! prompts should not arm SetForcedToolChoice, got %v", forcedCalls)
	}
	mu.Unlock()

	// 5. Prior user message in branch skips eager prelude.
	hAlways.setBranch([]map[string]any{
		{
			"id":   "u1",
			"type": "message",
			"message": map[string]any{
				"role":    "user",
				"content": "First user prompt",
			},
		},
	})
	res, errMsg = hAlways.emitEvent(sdk.EventBeforeAgentStart, beforePayload("Second user prompt."))
	if errMsg != "" || extractPrelude(res) != nil {
		t.Fatalf("prior user message should skip eager prelude, got res=%#v err=%q", res, errMsg)
	}
	hAlways.setBranch(nil)

	// 6. Existing todo phases skip eager prelude.
	_, toolErr := hAlways.callTool("todo", map[string]any{
		"op":    "init",
		"items": []any{"Step one"},
	})
	if toolErr != "" {
		t.Fatalf("todo init failed: %s", toolErr)
	}
	res, errMsg = hAlways.emitEvent(sdk.EventBeforeAgentStart, beforePayload("Continue with more work."))
	if errMsg != "" || extractPrelude(res) != nil {
		t.Fatalf("existing phases should skip eager prelude, got res=%#v err=%q", res, errMsg)
	}

	// 7. Plan mode skips eager prelude.
	extPlan := sdk.New("todo-eager-plan")
	Register(extPlan)
	hPlan := startHost(t, extPlan, hostOptions{
		SessionID:   "sess-eager-plan",
		ActiveTools: []string{"read", "todo"},
	})
	gate.SetPlanActive(func(_ sdk.Context) bool { return true })
	res, errMsg = hPlan.emitEvent(sdk.EventBeforeAgentStart, beforePayload("Implement feature Y."))
	if errMsg != "" || extractPrelude(res) != nil {
		t.Fatalf("plan mode should skip eager prelude, got res=%#v err=%q", res, errMsg)
	}
	gate.ResetForTests()

	// 8. Missing `todo` in active tools skips eager prelude.
	hPlan.setActiveTools([]string{"read", "bash"})
	res, errMsg = hPlan.emitEvent(sdk.EventBeforeAgentStart, beforePayload("Implement feature Y."))
	if errMsg != "" || extractPrelude(res) != nil {
		t.Fatalf("inactive todo tool should skip eager prelude, got res=%#v err=%q", res, errMsg)
	}

	// 9. todo.enabled = false skips eager prelude.
	hPlan.setActiveTools([]string{"read", "todo"})
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "todo.eager":
			return "always"
		case "todo.enabled":
			return false
		}
		return nil
	}
	res, errMsg = hPlan.emitEvent(sdk.EventBeforeAgentStart, beforePayload("Implement feature Y."))
	if errMsg != "" || extractPrelude(res) != nil {
		t.Fatalf("todo.enabled=false should skip eager prelude, got res=%#v err=%q", res, errMsg)
	}
}
