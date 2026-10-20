package pi_subagents

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// Twins of test/unit/prompt-template-bridge.test.ts, driven through the delegation bridge of policy_delegation.go (the fake bus
// and helpers are those of policy_delegation_test.go).

func structuredRequest(t *testing.T, overrides string) map[string]any {
	t.Helper()
	base := jsonMap(t, `{"requestId":"r1","ownerRunId":"owner-1","nodeId":"node-1","agent":"worker","task":"do work","context":"fresh","model":"openai/gpt-5","cwd":"/repo","result":{"kind":"text"}}`)
	if overrides != "" {
		for k, v := range jsonMap(t, overrides) {
			base[k] = v
		}
	}
	return base
}

func TestPromptTemplateBridge(t *testing.T) {
	const f = "prompt-template-bridge"
	// registered builds the bridge of the original's test: an active context unless noContext, structured executor structured, and a
	// legacy executor that must never run.
	registered := func(events *fakeEvents, noContext bool, structured delegationExecutor) *delegationBridge {
		return registerPromptTemplateDelegationBridge(delegationBridgeOptions{
			Events: events,
			GetContext: func() (any, bool) {
				if noContext {
					return nil, false
				}
				return map[string]any{"cwd": "/repo"}, true
			},
			Execute: func(string, map[string]any, context.Context, any, func(*bridgeResult)) (*bridgeResult, error) {
				return nil, errors.New("structured request should use executeStructured")
			},
			ExecuteStructured: structured,
		})
	}
	tw(t, f, "emits started/update/response on successful structured request", func(t *testing.T) {
		events := &fakeEvents{}
		var calls atomic.Int32
		bridge := registered(events, false, func(_ string, _ map[string]any, _ context.Context, _ any, onUpdate func(*bridgeResult)) (*bridgeResult, error) {
			calls.Add(1)
			onUpdate(&bridgeResult{Details: &bridgeDetails{Results: []bridgeChild{{Agent: "worker", Model: "openai/gpt-5-mini"}},
				Progress: []bridgeProgress{{Index: new(0.0), Agent: "worker", CurrentTool: "read", CurrentToolArgs: "src/extension/index.ts", RecentOutput: []string{"line 1"},
					RecentTools: []bridgeTool{{"read", `{"path":"src/extension/index.ts"}`}}, ToolCount: new(1.0), DurationMs: new(10.0), Tokens: new(42.0)}}}})
			return &bridgeResult{Details: &bridgeDetails{Results: []bridgeChild{{Agent: "worker", FinalOutput: new("ok"), ExitCode: new(0.0)}}}}, nil
		})
		defer bridge.dispose()
		started, update, response := events.once(subagentDelegationStartedEvent), events.once(subagentDelegationUpdateEvent), events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, structuredRequest(t, ""))
		eq(t, receive(t, started, "started"), jsonMap(t, `{"requestId":"r1","ownerRunId":"owner-1","nodeId":"node-1"}`), "started")
		u := receive(t, update, "update").(map[string]any)
		for k, want := range map[string]any{"requestId": "r1", "ownerRunId": "owner-1", "nodeId": "node-1", "currentTool": "read"} {
			eq(t, u[k], want, k)
		}
		eq(t, u["toolCount"], any(float64(1)), "toolCount")
		eq(t, u["recentOutputLines"], any([]any{"line 1"}), "recentOutputLines")
		eq(t, u["recentTools"], any([]any{map[string]any{"tool": "read", "args": `{"path":"src/extension/index.ts"}`}}), "recentTools")
		r := receive(t, response, "response").(map[string]any)
		for k, want := range map[string]any{"requestId": "r1", "ownerRunId": "owner-1", "nodeId": "node-1", "status": "completed"} {
			eq(t, r[k], want, k)
		}
		eq(t, r["result"], any(map[string]any{"kind": "text", "text": "ok"}), "result")
		eq(t, calls.Load(), int32(1), "executeStructured calls")
	})
	tw(t, f, "returns structured error when no active context", func(t *testing.T) {
		events := &fakeEvents{}
		bridge := registered(events, true, nil)
		defer bridge.dispose()
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, structuredRequest(t, `{"requestId":"r2"}`))
		r := receive(t, response, "response").(map[string]any)
		eq(t, r["status"], any("unavailable_context"), "status")
		polMatches(t, r["error"].(string), `No active extension context`)
	})
	tw(t, f, "applies pending cancel when cancel arrives before structured request", func(t *testing.T) {
		events := &fakeEvents{}
		var calls atomic.Int32
		bridge := registered(events, false, func(string, map[string]any, context.Context, any, func(*bridgeResult)) (*bridgeResult, error) {
			calls.Add(1)
			return &bridgeResult{Details: &bridgeDetails{Results: []bridgeChild{{}}}}, nil
		})
		defer bridge.dispose()
		events.Emit(subagentDelegationCancelEvent, jsonMap(t, `{"requestId":"r4","ownerRunId":"owner-1","nodeId":"node-1"}`))
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, structuredRequest(t, `{"requestId":"r4"}`))
		eq(t, receive(t, response, "response").(map[string]any)["status"], any("cancelled"), "status")
		eq(t, calls.Load(), int32(0), "executeCalls")
	})
	tw(t, f, "cancels in-flight structured delegated execution", func(t *testing.T) {
		events := &fakeEvents{}
		bridge := registered(events, false, func(_ string, _ map[string]any, ctx context.Context, _ any, _ func(*bridgeResult)) (*bridgeResult, error) {
			<-ctx.Done()
			return nil, errors.New("aborted")
		})
		defer bridge.dispose()
		started, response := events.once(subagentDelegationStartedEvent), events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, structuredRequest(t, `{"requestId":"r5"}`))
		receive(t, started, "started")
		events.Emit(subagentDelegationCancelEvent, jsonMap(t, `{"requestId":"r5","ownerRunId":"owner-1","nodeId":"node-1"}`))
		eq(t, receive(t, response, "response").(map[string]any)["status"], any("cancelled"), "status")
	})
	tw(t, f, "rejects legacy direct payloads without executor dispatch", func(t *testing.T) {
		events := &fakeEvents{}
		var calls atomic.Int32
		bridge := registered(events, false, func(string, map[string]any, context.Context, any, func(*bridgeResult)) (*bridgeResult, error) {
			calls.Add(1)
			return &bridgeResult{}, nil
		})
		defer bridge.dispose()
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, jsonMap(t, `{"requestId":"legacy-1","agent":"worker","task":"do work","context":"fresh","model":"openai/gpt-5","cwd":"/repo"}`))
		r := receive(t, response, "response").(map[string]any)
		eq(t, r["isError"], any(true), "isError")
		polMatches(t, r["errorText"].(string), `Legacy prompt-template direct delegation was removed`)
		eq(t, calls.Load(), int32(0), "executeCalls")
	})
	tw(t, f, "rejects removed tasks and worktree payloads without executor dispatch", func(t *testing.T) {
		events := &fakeEvents{}
		var calls atomic.Int32
		bridge := registered(events, false, func(string, map[string]any, context.Context, any, func(*bridgeResult)) (*bridgeResult, error) {
			calls.Add(1)
			return &bridgeResult{}, nil
		})
		defer bridge.dispose()
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, jsonMap(t, `{"requestId":"r6","tasks":[{"agent":"worker-a","task":"A"}],"context":"fresh","model":"openai/gpt-5","cwd":"/repo"}`))
		r := receive(t, response, "response").(map[string]any)
		eq(t, r["isError"], any(true), "isError")
		eq(t, strings.Contains(strings.ToLower(r["errorText"].(string)), "removed"), true, "removed")
		polMatches(t, r["errorText"].(string), `(?i)removed.*workflowScript`)
		eq(t, calls.Load(), int32(0), "executeCalls")
	})
}
