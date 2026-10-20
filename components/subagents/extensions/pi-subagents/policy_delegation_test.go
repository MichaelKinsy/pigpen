package pi_subagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Twins of test/unit/delegation-api.test.ts. The original awaits `tick()` (a macrotask) for the executor to run; the twins wait
// for the observable condition instead (waitFor).

// fakeEvents is the in-process event bus of the original's FakeEvents: listeners run in registration order and Emit returns when
// they have.
type fakeEvents struct {
	mu       sync.Mutex
	next     int
	handlers map[string][]fakeHandler
}

type fakeHandler struct {
	id int
	fn func(any)
}

func (f *fakeEvents) On(event string, handler func(any)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handlers == nil {
		f.handlers = map[string][]fakeHandler{}
	}
	f.next++
	id := f.next
	f.handlers[event] = append(f.handlers[event], fakeHandler{id, handler})
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		var kept []fakeHandler
		for _, h := range f.handlers[event] {
			if h.id != id {
				kept = append(kept, h)
			}
		}
		f.handlers[event] = kept
	}
}

func (f *fakeEvents) Emit(event string, data any) {
	f.mu.Lock()
	handlers := append([]fakeHandler(nil), f.handlers[event]...)
	f.mu.Unlock()
	for _, h := range handlers {
		h.fn(data)
	}
}

// once resolves with the next payload of an event.
func (f *fakeEvents) once(event string) <-chan any {
	ch := make(chan any, 1)
	var done sync.Once
	f.On(event, func(p any) {
		done.Do(func() { ch <- p })
	})
	return ch
}

// recorder collects the payloads of an event.
type recorder struct {
	mu   sync.Mutex
	list []map[string]any
}

func (r *recorder) add(p any) {
	m, _ := p.(map[string]any)
	r.mu.Lock()
	r.list = append(r.list, m)
	r.mu.Unlock()
}

func (r *recorder) all() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.list...)
}

func (r *recorder) find(requestID string) map[string]any {
	for _, m := range r.all() {
		if m["requestId"] == requestID {
			return m
		}
	}
	return nil
}

func (r *recorder) count(requestID string) int {
	n := 0
	for _, m := range r.all() {
		if m["requestId"] == requestID {
			n++
		}
	}
	return n
}

func (r *recorder) last() map[string]any {
	all := r.all()
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func receive(t *testing.T, ch <-chan any, what string) any {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

func jm(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// with is `{ ...base, ...overrides }`.
func with(base map[string]any, kv ...any) map[string]any {
	out := make(map[string]any, len(base)+len(kv)/2)
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func delegationRequest(t *testing.T) map[string]any {
	return jm(t, `{"requestId":"attempt-1","ownerRunId":"owner-1","nodeId":"node-1","agent":"reviewer","task":"Review evidence","context":"fresh","cwd":"/repo",
		"model":"openai/gpt-5","thinking":"high","timeoutMs":1000,"toolBudget":{"soft":3,"hard":5,"block":"*"},"skill":["review"],"artifacts":true,
		"result":{"kind":"structured","schema":{"type":"object","properties":{"ok":{"type":"boolean"}}}}}`)
}

var textDelegation = map[string]any{"kind": "text"}

func childUsage(in, out, cr, cw, cost, turns float64) *bridgeUsage {
	return &bridgeUsage{in, out, cr, cw, cost, turns}
}

func oneChild(c bridgeChild, runID string) *bridgeResult {
	return &bridgeResult{Details: &bridgeDetails{Mode: "single", RunID: runID, Results: []bridgeChild{c}}}
}

func newBridge(events *fakeEvents, executeStructured delegationExecutor) *delegationBridge {
	return registerPromptTemplateDelegationBridge(delegationBridgeOptions{
		Events:     events,
		GetContext: func() (any, bool) { return map[string]any{"cwd": "/repo"}, true },
		Execute: func(string, map[string]any, context.Context, any, func(*bridgeResult)) (*bridgeResult, error) {
			return nil, errors.New("legacy executor must remain separate")
		},
		ExecuteStructured: executeStructured,
	})
}

func TestDelegationAPI(t *testing.T) {
	tw(t, "delegation-api", "uses the existing prompt-template event family as the only transport", func(t *testing.T) {
		equal(t, subagentDelegationRequestEvent, "prompt-template:subagent:request", "request")
		equal(t, subagentDelegationStartedEvent, "prompt-template:subagent:started", "started")
		equal(t, subagentDelegationUpdateEvent, "prompt-template:subagent:update", "update")
		equal(t, subagentDelegationResponseEvent, "prompt-template:subagent:response", "response")
		equal(t, subagentDelegationCancelEvent, "prompt-template:subagent:cancel", "cancel")
	})

	tw(t, "delegation-api", "strictly parses the structured owned-leaf request", func(t *testing.T) {
		request := delegationRequest(t)
		parsed := parseSubagentDelegationRequest(request)
		equal(t, parsed.OK, true, "valid request")
		equal(t, parsed.Request, request, "parsed request")
		many := make([]any, 257)
		for i := range many {
			many[i] = "x"
		}
		wide := make([]any, 256)
		for i := range wide {
			wide[i] = strings.Repeat("x", 257)
		}
		malformed := []struct {
			input map[string]any
			want  string
		}{
			{with(request, "version", float64(99)), `Unsupported delegation field: version`},
			{with(request, "ownerRunId", "bad\nowner"), `ownerRunId.*256 characters without newlines`},
			{with(request, "nodeId", strings.Repeat("x", 257)), `nodeId.*256 characters without newlines`},
			{with(request, "thinking", "extreme"), `thinking must be one of`},
			{with(request, "output", false), `Unsupported delegation field: output`},
			{with(request, "acceptance", false), `Unsupported delegation field: acceptance`},
			{with(request, "agentContract", jm(t, `{"version":1}`)), `Unsupported delegation field: agentContract`},
			{with(request, "turnBudget", jm(t, `{"maxTurns":5}`)), `Unsupported delegation field: turnBudget`},
			{with(request, "result", jm(t, `{"kind":"text","schema":{}}`)), `result.schema is not supported`},
			{with(request, "result", jm(t, `{"kind":"structured"}`)), `result.schema must be a JSON Schema object`},
			{with(request, "task", strings.Repeat("é", 524_289)), `task exceeds 1 MiB`},
			{with(request, "cwd", strings.Repeat("é", 16_385)), `cwd exceeds 32 KiB`},
			{with(request, "agent", strings.Repeat("é", 513)), `agent exceeds 1 KiB`},
			{with(request, "model", strings.Repeat("é", 513)), `model exceeds 1 KiB`},
			{with(request, "skill", strings.Repeat("é", 513)), `skill entry exceeds 1 KiB`},
			{with(request, "skill", many), `skill supports at most 256 entries`},
			{with(request, "skill", wide), `skill entries exceed 64 KiB in aggregate`},
			{with(request, "result", map[string]any{"kind": "structured", "schema": map[string]any{"value": strings.Repeat("x", 65_536)}}), `result.schema exceeds 64 KiB`},
			{with(request, "timeoutMs", float64(2_147_483_648)), `timeoutMs must be <= 2147483647`},
		}
		for _, c := range malformed {
			p := parseSubagentDelegationRequest(c.input)
			equal(t, p.OK, false, c.want+" rejected")
			polMatches(t, p.Error, c.want)
		}
	})

	tw(t, "delegation-api", "accepts exact zero tool budgets for structured delegated leaves", func(t *testing.T) {
		request := delegationRequest(t)
		zero := map[string]any{"hard": float64(0), "block": "*"}
		parsed := parseSubagentDelegationRequest(with(request, "toolBudget", zero))
		equal(t, parsed.OK, true, "zero budget accepted")
		equal(t, parsed.Request["toolBudget"], zero, "zero budget kept")
		for _, soft := range []float64{0, 1} {
			equal(t, parseSubagentDelegationRequest(with(request, "toolBudget", with(zero, "soft", soft))).OK, false, fmt.Sprintf("soft %v rejected", soft))
		}
	})

	tw(t, "delegation-api", "accepts a per-launch intercomBridge override and forwards it to execution", func(t *testing.T) {
		request := delegationRequest(t)
		bridge := map[string]any{"mode": "off"}
		parsed := parseSubagentDelegationRequest(with(request, "intercomBridge", bridge))
		equal(t, parsed.OK, true, "bridge accepted")
		got := parsed.Request["intercomBridge"].(map[string]any)
		equal(t, got, bridge, "bridge kept")
		got["mode"] = "always"
		equal(t, bridge["mode"], "off", "parsed request must not alias the caller's object")
		got["mode"] = "off"
		equal(t, toSubagentDelegationExecutionParams(parsed.Request)["intercomBridge"], bridge, "forwarded to execution")
		_, has := toSubagentDelegationExecutionParams(request)["intercomBridge"]
		equal(t, has, false, "no bridge unless supplied")
		malformed := []struct {
			input map[string]any
			want  string
		}{
			{with(request, "intercomBridge", map[string]any{"mode": "loud"}), `intercomBridge\.mode is invalid`},
			{with(request, "intercomBridge", map[string]any{"extra": true}), `intercomBridge\.extra is not supported`},
			{with(request, "intercomBridge", "off"), `intercomBridge must be an object`},
			{with(request, "intercomBridge", map[string]any{"instructionFile": strings.Repeat("x", 1025)}), `intercomBridge\.instructionFile exceeds 1 KiB`},
		}
		for _, c := range malformed {
			p := parseSubagentDelegationRequest(c.input)
			equal(t, p.OK, false, c.want+" rejected")
			polMatches(t, p.Error, c.want)
		}
	})

	tw(t, "delegation-api", "rejects non-JSON schemas without executing toJSON hooks", func(t *testing.T) {
		var calls int
		parsed := parseSubagentDelegationRequest(with(delegationRequest(t), "result", map[string]any{"kind": "structured",
			"schema": map[string]any{"toJSON": func() any { calls++; return map[string]any{} }}}))
		equal(t, parsed.OK, false, "rejected")
		polMatches(t, parsed.Error, `result.schema must be plain JSON data`)
		equal(t, calls, 0, "hook calls")
	})

	tw(t, "delegation-api", "runs structured delegation through the concurrent executor and preserves literal text metadata", func(t *testing.T) {
		events := &fakeEvents{}
		var ordinaryCalls atomic.Int32
		var observed map[string]any
		bridge := registerPromptTemplateDelegationBridge(delegationBridgeOptions{
			Events:     events,
			GetContext: func() (any, bool) { return map[string]any{"cwd": "/repo"}, true },
			Execute: func(string, map[string]any, context.Context, any, func(*bridgeResult)) (*bridgeResult, error) {
				ordinaryCalls.Add(1)
				return &bridgeResult{Details: &bridgeDetails{Mode: "single"}}, nil
			},
			ExecuteStructured: func(_ string, params map[string]any, _ context.Context, _ any, onUpdate func(*bridgeResult)) (*bridgeResult, error) {
				observed = params
				onUpdate(&bridgeResult{Details: &bridgeDetails{Mode: "single", RunID: "run-1", Results: []bridgeChild{{Agent: "reviewer", Model: "openai/gpt-5", Thinking: "high"}},
					Progress: []bridgeProgress{{CurrentTool: "read"}}}})
				return oneChild(bridgeChild{Agent: "reviewer", ExitCode: new(0.0), Model: "openai/gpt-5", Thinking: "high", LaunchContractDigest: "launch-contract-digest",
					FinalOutput: new(`{"looks":"json"}`), Usage: childUsage(2, 3, 4, 5, 0.01, 2),
					ProgressSummary: &bridgeProgressSummary{ToolCount: new(6.0), Tokens: new(5.0), DurationMs: new(7.0)}}, "run-1"), nil
			},
		})
		defer bridge.dispose()
		textRequest := with(delegationRequest(t), "result", textDelegation)
		started := events.once(subagentDelegationStartedEvent)
		update := events.once(subagentDelegationUpdateEvent)
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, textRequest)
		equal(t, receive(t, started, "started"), jm(t, `{"requestId":"attempt-1","ownerRunId":"owner-1","nodeId":"node-1"}`), "started")
		equal(t, receive(t, update, "update"), jm(t, `{"requestId":"attempt-1","ownerRunId":"owner-1","nodeId":"node-1","runId":"run-1","currentTool":"read","model":"openai/gpt-5"}`), "update")
		equal(t, receive(t, response, "response"), jm(t, `{"requestId":"attempt-1","ownerRunId":"owner-1","nodeId":"node-1","status":"completed","runId":"run-1","agent":"reviewer",
			"model":"openai/gpt-5","thinking":"high","exitCode":0,"launchContractDigest":"launch-contract-digest","result":{"kind":"text","text":"{\"looks\":\"json\"}"},
			"usage":{"input":2,"output":3,"cacheRead":4,"cacheWrite":5,"cost":0.01,"turns":2,"toolCalls":6,"durationMs":7}}`), "response")
		equal(t, ordinaryCalls.Load(), int32(0), "ordinary executor calls")
		equal(t, observed, jm(t, `{"agent":"reviewer","task":"Review evidence","context":"fresh","cwd":"/repo","model":"openai/gpt-5","timeoutMs":1000,
			"toolBudget":{"soft":3,"hard":5,"block":"*"},"skill":["review"],"acceptance":false,"artifacts":true,"delegatedThinkingOverride":"high",
			"delegatedAllowZeroToolBudget":true,"async":false,"foregroundOnly":true,"clarify":false}`), "executor params")
	})

	tw(t, "delegation-api", "reports a blocked foreground tool attempt as tool_budget_exhausted", func(t *testing.T) {
		events := &fakeEvents{}
		responses := &recorder{}
		events.On(subagentDelegationResponseEvent, responses.add)
		bridge := newBridge(events, func(string, map[string]any, context.Context, any, func(*bridgeResult)) (*bridgeResult, error) {
			r := oneChild(bridgeChild{Agent: "reviewer", ExitCode: new(0.0), ToolBudgetBlocked: true, FinalOutput: new("The required tool was blocked."), Usage: childUsage(0, 0, 0, 0, 0, 1)}, "")
			return r, nil
		})
		defer bridge.dispose()
		events.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "result", textDelegation))
		waitFor(t, "response", func() bool { return len(responses.all()) > 0 })
		equal(t, responses.all(), []map[string]any{jm(t, `{"requestId":"attempt-1","ownerRunId":"owner-1","nodeId":"node-1","status":"tool_budget_exhausted","agent":"reviewer","exitCode":0,
			"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"cost":0,"turns":1,"toolCalls":0,"durationMs":0}}`)}, "responses")
	})

	heartbeat := func(extra bridgeProgress) bridgeProgress {
		extra.Index, extra.Agent = new(0.0), "reviewer"
		return extra
	}
	update := func(runID string, p bridgeProgress) *bridgeResult {
		return &bridgeResult{Details: &bridgeDetails{Mode: "single", RunID: runID, Results: []bridgeChild{{Agent: "reviewer", Model: "openai/gpt-5"}}, Progress: []bridgeProgress{p}}}
	}
	done := func(runID string, usage *bridgeUsage) *bridgeResult {
		return oneChild(bridgeChild{Agent: "reviewer", ExitCode: new(0.0), Model: "openai/gpt-5", FinalOutput: new("done"), Usage: usage}, runID)
	}

	tw(t, "delegation-api", "suppresses unchanged structured delegation heartbeat snapshots", func(t *testing.T) {
		events := &fakeEvents{}
		updates := &recorder{}
		events.On(subagentDelegationUpdateEvent, updates.add)
		bridge := newBridge(events, func(_ string, _ map[string]any, _ context.Context, _ any, onUpdate func(*bridgeResult)) (*bridgeResult, error) {
			progress := heartbeat(bridgeProgress{CurrentTool: "read", CurrentToolArgs: `{"path":"evidence.md"}`, RecentOutput: []string{"Reading evidence"},
				RecentTools: []bridgeTool{{"read", "evidence.md"}}, ToolCount: new(1.0), DurationMs: new(1000.0), Tokens: new(8.0)})
			onUpdate(update("run-heartbeat", progress))
			progress.DurationMs = new(2000.0)
			onUpdate(update("run-heartbeat", progress))
			return done("run-heartbeat", childUsage(4, 4, 0, 0, 0, 1)), nil
		})
		defer bridge.dispose()
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "result", textDelegation))
		r := receive(t, response, "response").(map[string]any)
		equal(t, len(updates.all()), 1, "update count")
		equal(t, updates.all()[0]["durationMs"], float64(1000), "duration")
		equal(t, r["status"], "completed", "status")
	})

	tw(t, "delegation-api", "delivers structured delegation updates when progress or bounded output changes", func(t *testing.T) {
		events := &fakeEvents{}
		updates := &recorder{}
		events.On(subagentDelegationUpdateEvent, updates.add)
		bridge := newBridge(events, func(_ string, _ map[string]any, _ context.Context, _ any, onUpdate func(*bridgeResult)) (*bridgeResult, error) {
			common := func(p bridgeProgress) bridgeProgress {
				p.ToolCount, p.Tokens = new(1.0), new(8.0)
				return heartbeat(p)
			}
			read := []bridgeTool{{"read", "evidence.md"}}
			onUpdate(update("run-progress", common(bridgeProgress{CurrentTool: "read", RecentOutput: []string{"Reading evidence"}, RecentTools: read, DurationMs: new(1000.0)})))
			onUpdate(update("run-progress", common(bridgeProgress{CurrentTool: "read", RecentOutput: []string{"Reading evidence"}, RecentTools: read, DurationMs: new(2000.0)})))
			onUpdate(update("run-progress", heartbeat(bridgeProgress{CurrentTool: "write", CurrentToolArgs: `{"path":"report.md"}`, RecentOutput: []string{"Wrote report"},
				RecentTools: []bridgeTool{{"read", "evidence.md"}, {"write", "report.md"}}, ToolCount: new(2.0), Tokens: new(16.0), DurationMs: new(3000.0)})))
			return done("run-progress", childUsage(8, 8, 0, 0, 0, 1)), nil
		})
		defer bridge.dispose()
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "result", textDelegation))
		equal(t, receive(t, response, "response").(map[string]any)["status"], "completed", "status")
		got := updates.all()
		equal(t, len(got), 2, "update count")
		equal(t, got[1]["currentTool"], "write", "current tool")
		equal(t, got[1]["recentOutput"], "Wrote report", "recent output")
		equal(t, got[1]["recentTools"], []any{map[string]any{"tool": "read", "args": "evidence.md"}, map[string]any{"tool": "write", "args": "report.md"}}, "recent tools")
	})

	tw(t, "delegation-api", "emits a structured delegation update when only cumulative cache or turn counters change", func(t *testing.T) {
		events := &fakeEvents{}
		updates := &recorder{}
		events.On(subagentDelegationUpdateEvent, updates.add)
		bridge := newBridge(events, func(_ string, _ map[string]any, _ context.Context, _ any, onUpdate func(*bridgeResult)) (*bridgeResult, error) {
			base := heartbeat(bridgeProgress{CurrentTool: "read", ToolCount: new(1.0), DurationMs: new(1000.0), Tokens: new(16.0), InputTokens: new(8.0), OutputTokens: new(8.0),
				CacheRead: new(0.0), CacheWrite: new(0.0), TurnCount: new(1.0)})
			onUpdate(update("run-usage", base))
			// Exact duplicate: tokens/tool progress and usage counters are unchanged, so this is a heartbeat.
			onUpdate(update("run-usage", base))
			b2 := base
			b2.CacheRead = new(4.0)
			onUpdate(update("run-usage", b2))
			b3 := b2
			b3.CacheWrite = new(2.0)
			onUpdate(update("run-usage", b3))
			b4 := b3
			b4.TurnCount = new(2.0)
			onUpdate(update("run-usage", b4))
			return done("run-usage", childUsage(8, 8, 4, 2, 0, 2)), nil
		})
		defer bridge.dispose()
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "result", textDelegation))
		equal(t, receive(t, response, "response").(map[string]any)["status"], "completed", "status")
		got := updates.all()
		equal(t, len(got), 4, "update count")
		equal(t, got[0]["usage"], jm(t, `{"input":8,"output":8,"cacheRead":0,"cacheWrite":0,"turns":1}`), "usage 0")
		equal(t, got[1]["usage"], jm(t, `{"input":8,"output":8,"cacheRead":4,"cacheWrite":0,"turns":1}`), "usage 1")
		equal(t, got[2]["usage"], jm(t, `{"input":8,"output":8,"cacheRead":4,"cacheWrite":2,"turns":1}`), "usage 2")
		equal(t, got[3]["usage"], jm(t, `{"input":8,"output":8,"cacheRead":4,"cacheWrite":2,"turns":2}`), "usage 3")
	})

	tw(t, "delegation-api", "omits usage from a structured delegation update when counters are absent or invalid", func(t *testing.T) {
		events := &fakeEvents{}
		updates := &recorder{}
		events.On(subagentDelegationUpdateEvent, updates.add)
		bridge := newBridge(events, func(_ string, _ map[string]any, _ context.Context, _ any, onUpdate func(*bridgeResult)) (*bridgeResult, error) {
			onUpdate(update("run-invalid", heartbeat(bridgeProgress{CurrentTool: "read", ToolCount: new(1.0), Tokens: new(8.0)})))
			onUpdate(update("run-invalid", heartbeat(bridgeProgress{CurrentTool: "write", ToolCount: new(2.0), Tokens: new(16.0), InputTokens: new(-1.0), OutputTokens: new(8.0), CacheRead: new(0.0), CacheWrite: new(0.0), TurnCount: new(1.0)})))
			onUpdate(update("run-invalid", heartbeat(bridgeProgress{CurrentTool: "grep", ToolCount: new(3.0), Tokens: new(16.0), InputTokens: new(8.0), OutputTokens: new(math.NaN()), CacheRead: new(0.0), CacheWrite: new(0.0), TurnCount: new(1.0)})))
			return done("run-invalid", childUsage(8, 8, 0, 0, 0, 1)), nil
		})
		defer bridge.dispose()
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "result", textDelegation))
		equal(t, receive(t, response, "response").(map[string]any)["status"], "completed", "status")
		got := updates.all()
		equal(t, len(got), 3, "update count")
		for _, u := range got {
			_, has := u["usage"]
			equal(t, has, false, "usage omitted")
		}
	})

	tw(t, "delegation-api", "always delivers the complete structured terminal error after quiet heartbeats", func(t *testing.T) {
		events := &fakeEvents{}
		updates := &recorder{}
		events.On(subagentDelegationUpdateEvent, updates.add)
		bridge := newBridge(events, func(_ string, _ map[string]any, _ context.Context, _ any, onUpdate func(*bridgeResult)) (*bridgeResult, error) {
			progress := heartbeat(bridgeProgress{CurrentTool: "read", RecentOutput: []string{"Reading evidence"}, ToolCount: new(1.0), Tokens: new(8.0), DurationMs: new(1000.0)})
			onUpdate(update("run-error", progress))
			progress.DurationMs = new(2000.0)
			onUpdate(update("run-error", progress))
			return &bridgeResult{IsError: true, Content: []any{map[string]any{"type": "text", "text": "full provider error"}},
				Details: &bridgeDetails{Mode: "single", RunID: "run-error", Results: []bridgeChild{{Agent: "reviewer", ExitCode: new(1.0), Error: "full provider error", Model: "openai/gpt-5", Usage: childUsage(4, 2, 1, 0, 0.01, 1)}}}}, nil
		})
		defer bridge.dispose()
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "result", textDelegation))
		got := receive(t, response, "response")
		equal(t, len(updates.all()), 1, "update count")
		equal(t, got, jm(t, `{"requestId":"attempt-1","ownerRunId":"owner-1","nodeId":"node-1","status":"failed","runId":"run-error","agent":"reviewer","model":"openai/gpt-5","exitCode":1,
			"error":"full provider error","usage":{"input":4,"output":2,"cacheRead":1,"cacheWrite":0,"cost":0.01,"turns":1,"toolCalls":0,"durationMs":0}}`), "response")
	})

	tw(t, "delegation-api", "projects structured values and fails missing or oversized captures", func(t *testing.T) {
		cases := []struct {
			name   string
			child  bridgeChild
			status string
			result any
		}{
			{"value", bridgeChild{StructuredOutput: map[string]any{"ok": true}, StructuredOutputSet: true}, "completed", map[string]any{"kind": "structured", "value": map[string]any{"ok": true}}},
			{"missing", bridgeChild{}, "failed", nil},
			{"oversized", bridgeChild{StructuredOutput: map[string]any{"value": strings.Repeat("x", 1024*1024)}, StructuredOutputSet: true}, "failed", nil},
		}
		for _, c := range cases {
			events := &fakeEvents{}
			child := c.child
			child.Agent, child.ExitCode, child.Usage = "reviewer", new(0.0), childUsage(0, 0, 0, 0, 0, 1)
			bridge := newBridge(events, func(string, map[string]any, context.Context, any, func(*bridgeResult)) (*bridgeResult, error) {
				return oneChild(child, ""), nil
			})
			response := events.once(subagentDelegationResponseEvent)
			events.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "requestId", "structured-"+c.status+"-"+c.name))
			got := receive(t, response, "response").(map[string]any)
			equal(t, got["status"], c.status, c.name+" status")
			equal(t, got["result"], c.result, c.name+" result")
			if c.status == "failed" {
				polMatches(t, got["error"].(string), `structured result`)
			}
			bridge.dispose()
		}
	})

	// A gate blocks an executor until the test frees it or the attempt is cancelled.
	type gate struct {
		mu       sync.Mutex
		releases map[string]chan struct{}
	}
	newGate := func() *gate { return &gate{releases: map[string]chan struct{}{}} }
	register := func(g *gate, id string) chan struct{} {
		ch := make(chan struct{})
		g.mu.Lock()
		g.releases[id] = ch
		g.mu.Unlock()
		return ch
	}
	has := func(g *gate, id string) bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.releases[id] != nil
	}
	release := func(g *gate, id string) {
		g.mu.Lock()
		ch := g.releases[id]
		g.mu.Unlock()
		close(ch)
	}

	tw(t, "delegation-api", "isolates logical-node ownership, exact cancellation, pre-cancellation, and reuse", func(t *testing.T) {
		events := &fakeEvents{}
		g := newGate()
		responses := &recorder{}
		events.On(subagentDelegationResponseEvent, responses.add)
		bridge := newBridge(events, func(id string, params map[string]any, ctx context.Context, _ any, _ func(*bridgeResult)) (*bridgeResult, error) {
			ch := register(g, id)
			select {
			case <-ch:
				return oneChild(bridgeChild{Agent: params["agent"].(string), ExitCode: new(0.0), FinalOutput: new(id), Usage: childUsage(0, 0, 0, 0, 0, 1)}, ""), nil
			case <-ctx.Done():
				return nil, errors.New("aborted")
			}
		})
		defer bridge.dispose()
		req := func(id string, kv ...any) map[string]any {
			return with(delegationRequest(t), append([]any{"requestId", id, "result", textDelegation}, kv...)...)
		}
		events.Emit(subagentDelegationRequestEvent, req("owner-a"))
		events.Emit(subagentDelegationRequestEvent, req("owner-b"))
		events.Emit(subagentDelegationRequestEvent, req("owner-b"))
		events.Emit(subagentDelegationRequestEvent, req("other-node", "nodeId", "node-2"))
		waitFor(t, "executors", func() bool { return has(g, "owner-a") && has(g, "other-node") })
		equal(t, responses.find("owner-b")["status"], "duplicate_node", "duplicate node")
		equal(t, responses.count("owner-b"), 1, "one duplicate response")
		events.Emit(subagentDelegationCancelEvent, map[string]any{"requestId": "owner-a", "ownerRunId": "wrong", "nodeId": "node-1"})
		events.Emit(subagentDelegationCancelEvent, map[string]any{"requestId": "owner-a", "ownerRunId": "owner-1", "nodeId": "wrong"})
		equal(t, responses.find("owner-a") == nil, true, "a wrong identity cancels nothing")
		events.Emit(subagentDelegationCancelEvent, map[string]any{"requestId": "owner-a", "ownerRunId": "owner-1", "nodeId": "node-1"})
		waitFor(t, "owner-a response", func() bool { return responses.find("owner-a") != nil })
		equal(t, responses.find("owner-a")["status"], "cancelled", "exact cancellation")
		release(g, "other-node")
		waitFor(t, "other-node response", func() bool { return responses.find("other-node") != nil })

		events.Emit(subagentDelegationCancelEvent, map[string]any{"requestId": "pre", "ownerRunId": "owner-1", "nodeId": "node-1"})
		events.Emit(subagentDelegationRequestEvent, req("pre"))
		waitFor(t, "pre response", func() bool { return responses.find("pre") != nil })
		equal(t, responses.find("pre")["status"], "cancelled", "pre-cancellation")

		events.Emit(subagentDelegationRequestEvent, req("reuse"))
		waitFor(t, "reuse executor", func() bool { return has(g, "reuse") })
		release(g, "reuse")
		waitFor(t, "reuse response", func() bool { return responses.find("reuse") != nil })
		equal(t, responses.find("reuse")["status"], "completed", "node reuse")
	})

	tw(t, "delegation-api", "keys concurrent structured attempts and retransmissions by the full identity tuple", func(t *testing.T) {
		events := &fakeEvents{}
		g := newGate()
		responses := &recorder{}
		var executeCalls atomic.Int32
		events.On(subagentDelegationResponseEvent, responses.add)
		bridge := newBridge(events, func(_ string, params map[string]any, _ context.Context, _ any, _ func(*bridgeResult)) (*bridgeResult, error) {
			executeCalls.Add(1)
			task := params["task"].(string)
			<-register(g, task)
			return oneChild(bridgeChild{Agent: params["agent"].(string), ExitCode: new(0.0), FinalOutput: new(task), Usage: childUsage(0, 0, 0, 0, 0, 1)}, ""), nil
		})
		defer bridge.dispose()
		first := with(delegationRequest(t), "requestId", "shared-attempt", "task", "first", "result", textDelegation)
		second := with(first, "ownerRunId", "owner-2", "nodeId", "node-2", "task", "second")
		events.Emit(subagentDelegationRequestEvent, first)
		events.Emit(subagentDelegationRequestEvent, second)
		events.Emit(subagentDelegationRequestEvent, first)
		waitFor(t, "both executors", func() bool { return has(g, "first") && has(g, "second") })
		equal(t, executeCalls.Load(), int32(2), "execute calls")
		release(g, "first")
		release(g, "second")
		waitFor(t, "two responses", func() bool { return len(responses.all()) >= 2 })
		byOwner := map[string]string{}
		for _, r := range responses.all() {
			byOwner[r["ownerRunId"].(string)+"/"+r["nodeId"].(string)] = r["status"].(string)
		}
		equal(t, byOwner, map[string]string{"owner-1/node-1": "completed", "owner-2/node-2": "completed"}, "responses by identity")
		equal(t, len(responses.all()), 2, "no extra responses")
		events.Emit(subagentDelegationRequestEvent, first)
		equal(t, executeCalls.Load(), int32(2), "retransmission after settling")
		equal(t, len(responses.all()), 2, "retransmission answered once")
	})

	tw(t, "delegation-api", "fails closed instead of evicting structured identity state", func(t *testing.T) {
		events := &fakeEvents{}
		responses := &recorder{}
		var executeCalls atomic.Int32
		events.On(subagentDelegationResponseEvent, responses.add)
		executor := func(id string, _ map[string]any, _ context.Context, _ any, _ func(*bridgeResult)) (*bridgeResult, error) {
			executeCalls.Add(1)
			return oneChild(bridgeChild{Agent: "reviewer", ExitCode: new(0.0), FinalOutput: new(id), Usage: childUsage(0, 0, 0, 0, 0, 1)}, ""), nil
		}
		bridge := newBridge(events, executor)
		for i := range 8_192 {
			events.Emit(subagentDelegationCancelEvent, map[string]any{"requestId": fmt.Sprintf("cancelled-%d", i), "ownerRunId": "saturated-owner", "nodeId": fmt.Sprintf("cancelled-node-%d", i)})
		}
		events.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "requestId", "cancelled-0", "ownerRunId", "saturated-owner", "nodeId", "cancelled-node-0", "result", textDelegation))
		waitFor(t, "cancelled-0 response", func() bool { return responses.find("cancelled-0") != nil })
		equal(t, responses.last()["status"], "cancelled", "pre-cancelled")

		events.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "requestId", "cancel-overflow", "ownerRunId", "saturated-owner", "nodeId", "cancel-overflow-node", "result", textDelegation))
		waitFor(t, "cancel-overflow response", func() bool { return responses.find("cancel-overflow") != nil })
		equal(t, responses.last()["status"], "unavailable_context", "saturated")
		polMatches(t, responses.last()["error"].(string), `identity capacity`, "i")
		equal(t, executeCalls.Load(), int32(0), "nothing executed")
		bridge.dispose()

		settledEvents := &fakeEvents{}
		settledResponses := &recorder{}
		var settledCalls atomic.Int32
		settledEvents.On(subagentDelegationResponseEvent, settledResponses.add)
		settledBridge := newBridge(settledEvents, func(id string, p map[string]any, c context.Context, x any, u func(*bridgeResult)) (*bridgeResult, error) {
			settledCalls.Add(1)
			return executor(id, p, c, x, u)
		})
		defer settledBridge.dispose()
		for i := range 8_192 {
			settledEvents.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "requestId", fmt.Sprintf("settled-%d", i), "ownerRunId", "settled-owner",
				"nodeId", fmt.Sprintf("settled-node-%d", i), "result", textDelegation))
		}
		waitFor(t, "8192 responses", func() bool { return len(settledResponses.all()) == 8_192 })
		equal(t, settledCalls.Load(), int32(8_192), "settled executions")

		settledEvents.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "requestId", "settled-overflow", "ownerRunId", "settled-owner", "nodeId", "settled-overflow-node", "result", textDelegation))
		equal(t, settledResponses.last()["status"], "unavailable_context", "settled overflow")
		equal(t, settledCalls.Load(), int32(8_192), "overflow not executed")

		settledEvents.Emit(subagentDelegationRequestEvent, with(delegationRequest(t), "requestId", "settled-0", "ownerRunId", "settled-owner", "nodeId", "settled-node-0", "result", textDelegation))
		equal(t, len(settledResponses.all()), 8_193, "retransmission of a settled attempt is dropped")
		equal(t, settledCalls.Load(), int32(8_192), "retransmission not executed")
	})

	tw(t, "delegation-api", "rejects the unversioned prompt-template direct delegation fallback", func(t *testing.T) {
		events := &fakeEvents{}
		var executeCalls atomic.Int32
		bridge := registerPromptTemplateDelegationBridge(delegationBridgeOptions{
			Events:     events,
			GetContext: func() (any, bool) { return map[string]any{"cwd": "/repo"}, true },
			Execute: func(string, map[string]any, context.Context, any, func(*bridgeResult)) (*bridgeResult, error) {
				executeCalls.Add(1)
				return &bridgeResult{Content: []any{map[string]any{"type": "text", "text": "unreachable"}}, Details: &bridgeDetails{Mode: "single"}}, nil
			},
		})
		defer bridge.dispose()
		response := events.once(subagentDelegationResponseEvent)
		events.Emit(subagentDelegationRequestEvent, jm(t, `{"requestId":"legacy-1","agent":"reviewer","task":"Legacy prompt-template delegation","context":"fresh","model":"openai/gpt-5","cwd":"/repo"}`))
		got := receive(t, response, "response").(map[string]any)
		equal(t, got["requestId"], "legacy-1", "request id")
		equal(t, got["isError"], true, "is error")
		polMatches(t, got["errorText"].(string), `Legacy prompt-template direct delegation was removed`)
		equal(t, executeCalls.Load(), int32(0), "execute calls")
	})
}
