package pi_subagents

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDelegationEvents is the FakeEvents of the original's test: listeners in registration order, emit is synchronous.
type fakeDelegationEvents struct {
	mu       sync.Mutex
	handlers map[string][]*func(any)
}

func (f *fakeDelegationEvents) On(event string, handler func(data any)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handlers == nil {
		f.handlers = map[string][]*func(any){}
	}
	h := &handler
	f.handlers[event] = append(f.handlers[event], h)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		var keep []*func(any)
		for _, x := range f.handlers[event] {
			if x != h {
				keep = append(keep, x)
			}
		}
		f.handlers[event] = keep
	}
}

func (f *fakeDelegationEvents) Emit(event string, data any) {
	f.mu.Lock()
	list := append([]*func(any){}, f.handlers[event]...)
	f.mu.Unlock()
	for _, h := range list {
		(*h)(data)
	}
}

// once resolves with the next payload of an event.
func (f *fakeDelegationEvents) once(event string) <-chan *jsObject {
	c := make(chan *jsObject, 1)
	var unsub func()
	unsub = f.On(event, func(data any) {
		go unsub()
		o, _ := data.(*jsObject)
		select {
		case c <- o:
		default:
		}
	})
	return c
}

func recv(t *testing.T, c <-chan *jsObject) *jsObject {
	t.Helper()
	select {
	case o := <-c:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5s")
		return nil
	}
}

func structuredRequest(t *testing.T, overrides string) *jsObject {
	base := js(t, `{"requestId":"r1","ownerRunId":"owner-1","nodeId":"node-1","agent":"worker","task":"do work","context":"fresh","model":"openai/gpt-5","cwd":"/repo","result":{"kind":"text"}}`).(*jsObject)
	if overrides != "" {
		for k, v := range js(t, overrides).(*jsObject).vals {
			base.set(k, v)
		}
	}
	return base
}

func TestPromptTemplateBridge(t *testing.T) {
	const f = "prompt-template-bridge"
	ctx := func() any { return &delegationContext{Cwd: "/repo"} }
	tw(t, f, "emits started/update/response on successful structured request", func(t *testing.T) {
		events := &fakeDelegationEvents{}
		var calls int
		bridge := registerPromptTemplateDelegationBridge(events, ctx, func(_ string, _ *jsObject, _ <-chan struct{}, _ any, onUpdate func(*jsObject)) (*jsObject, error) {
			calls++
			onUpdate(js(t, `{"details":{"results":[{"agent":"worker","model":"openai/gpt-5-mini"}],"progress":[{"index":0,"agent":"worker","currentTool":"read","currentToolArgs":"src/extension/index.ts","recentOutput":["line 1"],"recentTools":[{"tool":"read","args":"{\"path\":\"src/extension/index.ts\"}"}],"toolCount":1,"durationMs":10,"tokens":42}]}}`).(*jsObject))
			return js(t, `{"details":{"results":[{"agent":"worker","finalOutput":"ok","exitCode":0}]}}`).(*jsObject), nil
		})
		defer bridge.dispose()
		started, update, response := events.once(delegationStartedEvent), events.once(delegationUpdateEvent), events.once(delegationResponseEvent)
		events.Emit(delegationRequestEvent, structuredRequest(t, ""))
		eq(t, jsonString(recv(t, started)), `{"requestId":"r1","ownerRunId":"owner-1","nodeId":"node-1"}`, "started")
		u := recv(t, update)
		for k, want := range map[string]any{"requestId": "r1", "ownerRunId": "owner-1", "nodeId": "node-1", "currentTool": "read"} {
			eq(t, u.vals[k], want, k)
		}
		eq(t, u.vals["toolCount"], any(float64(1)), "toolCount")
		eq(t, jsonString(u.vals["recentOutputLines"]), `["line 1"]`, "recentOutputLines")
		eq(t, jsonString(u.vals["recentTools"]), `[{"tool":"read","args":"{\"path\":\"src/extension/index.ts\"}"}]`, "recentTools")
		r := recv(t, response)
		eq(t, r.vals["requestId"], any("r1"), "requestId")
		eq(t, r.vals["ownerRunId"], any("owner-1"), "ownerRunId")
		eq(t, r.vals["nodeId"], any("node-1"), "nodeId")
		eq(t, r.vals["status"], any("completed"), "status")
		eq(t, jsonString(r.vals["result"]), `{"kind":"text","text":"ok"}`, "result")
		bridge.wg.Wait()
		eq(t, calls, 1, "executeStructured calls")
	})
	tw(t, f, "returns structured error when no active context", func(t *testing.T) {
		events := &fakeDelegationEvents{}
		bridge := registerPromptTemplateDelegationBridge(events, func() any { return nil }, nil)
		defer bridge.dispose()
		response := events.once(delegationResponseEvent)
		events.Emit(delegationRequestEvent, structuredRequest(t, `{"requestId":"r2"}`))
		r := recv(t, response)
		eq(t, r.vals["status"], any("unavailable_context"), "status")
		polMatches(t, r.vals["error"].(string), `No active extension context`)
	})
	tw(t, f, "applies pending cancel when cancel arrives before structured request", func(t *testing.T) {
		events := &fakeDelegationEvents{}
		calls := 0
		bridge := registerPromptTemplateDelegationBridge(events, ctx, func(string, *jsObject, <-chan struct{}, any, func(*jsObject)) (*jsObject, error) {
			calls++
			return js(t, `{"details":{"results":[{"messages":[]}]}}`).(*jsObject), nil
		})
		defer bridge.dispose()
		events.Emit(delegationCancelEvent, js(t, `{"requestId":"r4","ownerRunId":"owner-1","nodeId":"node-1"}`))
		response := events.once(delegationResponseEvent)
		events.Emit(delegationRequestEvent, structuredRequest(t, `{"requestId":"r4"}`))
		eq(t, recv(t, response).vals["status"], any("cancelled"), "status")
		eq(t, calls, 0, "executeCalls")
	})
	tw(t, f, "cancels in-flight structured delegated execution", func(t *testing.T) {
		events := &fakeDelegationEvents{}
		bridge := registerPromptTemplateDelegationBridge(events, ctx, func(_ string, _ *jsObject, done <-chan struct{}, _ any, _ func(*jsObject)) (*jsObject, error) {
			<-done
			return nil, errors.New("aborted")
		})
		defer bridge.dispose()
		started, response := events.once(delegationStartedEvent), events.once(delegationResponseEvent)
		events.Emit(delegationRequestEvent, structuredRequest(t, `{"requestId":"r5"}`))
		recv(t, started)
		events.Emit(delegationCancelEvent, js(t, `{"requestId":"r5","ownerRunId":"owner-1","nodeId":"node-1"}`))
		eq(t, recv(t, response).vals["status"], any("cancelled"), "status")
	})
	tw(t, f, "rejects legacy direct payloads without executor dispatch", func(t *testing.T) {
		events := &fakeDelegationEvents{}
		calls := 0
		bridge := registerPromptTemplateDelegationBridge(events, ctx, func(string, *jsObject, <-chan struct{}, any, func(*jsObject)) (*jsObject, error) {
			calls++
			return newObject(), nil
		})
		defer bridge.dispose()
		response := events.once(delegationResponseEvent)
		events.Emit(delegationRequestEvent, js(t, `{"requestId":"legacy-1","agent":"worker","task":"do work","context":"fresh","model":"openai/gpt-5","cwd":"/repo"}`))
		r := recv(t, response)
		eq(t, r.vals["isError"], any(true), "isError")
		polMatches(t, r.vals["errorText"].(string), `Legacy prompt-template direct delegation was removed`)
		eq(t, calls, 0, "executeCalls")
	})
	tw(t, f, "rejects removed tasks and worktree payloads without executor dispatch", func(t *testing.T) {
		events := &fakeDelegationEvents{}
		calls := 0
		bridge := registerPromptTemplateDelegationBridge(events, ctx, func(string, *jsObject, <-chan struct{}, any, func(*jsObject)) (*jsObject, error) {
			calls++
			return newObject(), nil
		})
		defer bridge.dispose()
		response := events.once(delegationResponseEvent)
		events.Emit(delegationRequestEvent, js(t, `{"requestId":"r6","tasks":[{"agent":"worker-a","task":"A"}],"context":"fresh","model":"openai/gpt-5","cwd":"/repo"}`))
		r := recv(t, response)
		eq(t, r.vals["isError"], any(true), "isError")
		eq(t, strings.Contains(strings.ToLower(r.vals["errorText"].(string)), "removed"), true, "removed")
		polMatches(t, r.vals["errorText"].(string), `(?i)removed.*workflowScript`)
		eq(t, calls, 0, "executeCalls")
	})
}
