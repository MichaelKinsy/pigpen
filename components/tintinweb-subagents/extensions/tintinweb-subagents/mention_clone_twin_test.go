package tintinweb_subagents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// Twins of test/mention-clone.test.ts and test/e2e/mention-clone-tool-reachability.e2e.test.ts. The original mocks Pi's
// createAgentSession and hands its model turn a stand-in; here the clone runs through the host's model API, so a fake
// host stands in for the session and a scripted model for the turn.

type fakeCloneHost struct {
	mu        sync.Mutex
	conv      []map[string]any
	convErr   error
	convCalls int
	system    string
	script    []func() (map[string]any, error) // the model's replies, in order; afterwards it answers in prose
	requests  []map[string]any
}

func (h *fakeCloneHost) conversation() ([]map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.convCalls++
	return h.conv, h.convErr
}
func (h *fakeCloneHost) systemPrompt() string { return h.system }
func (h *fakeCloneHost) turn(_ context.Context, request map[string]any) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = append(h.requests, request)
	if len(h.script) == 0 {
		return prose("done"), nil
	}
	next := h.script[0]
	h.script = h.script[1:]
	return next()
}

func prose(text string) map[string]any {
	return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "stopReason": "stop"}
}

func callsOf(calls ...map[string]any) func() (map[string]any, error) {
	return func() (map[string]any, error) {
		var content []any
		for i, c := range calls {
			content = append(content, map[string]any{"type": "toolCall", "id": "clone-tool-call-" + string(rune('1'+i)), "name": "Agent", "arguments": c})
		}
		return map[string]any{"role": "assistant", "content": content, "stopReason": "toolUse"}, nil
	}
}

func replyErr(msg string) func() (map[string]any, error) {
	return func() (map[string]any, error) { return nil, errors.New(msg) }
}

var conversation = []map[string]any{
	{"role": "user", "content": []any{map[string]any{"type": "text", "text": "hi"}}},
	{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "hello"}}},
}

type cloneRig struct {
	host *fakeCloneHost
	ran  []map[string]any
	opts mentionCloneOptions
}

func newCloneRig(script ...func() (map[string]any, error)) *cloneRig {
	r := &cloneRig{host: &fakeCloneHost{conv: conversation, system: "the live system prompt", script: script}}
	r.opts = mentionCloneOptions{Host: r.host, Type: "Explore", Message: "find the flaky test",
		Tool: cloneTool{Name: "Agent", Description: "d", Parameters: map[string]any{"type": "object"}, Run: func(p map[string]any) (string, bool) {
			r.ran = append(r.ran, p)
			return "Agent ID: a1", false
		}}}
	return r
}

func (r *cloneRig) run() mentionCloneResult { return runMentionClone(context.Background(), r.opts) }

var explorePlan = map[string]any{"subagent_type": "Explore", "prompt": "go"}

func TestMentionCloneTwins(t *testing.T) {
	const f = "mention-clone"
	first := func(r *cloneRig) map[string]any { return r.host.requests[0] }

	tw(t, f, "carries the conversation's own messages, not a rendering of them", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan))
		r.run()
		msgs := first(r)["messages"].([]any)
		eq(t, msgs[0], conversation[0])
		eq(t, msgs[1], conversation[1])
	})
	tw(t, f, "takes the conversation from memory, never from the session file", func(t *testing.T) {
		// The host's conversation is the session's own resolved context (buildSessionContext), asked once.
		r := newCloneRig(callsOf(explorePlan))
		r.run()
		eq(t, r.host.convCalls, 1)
	})
	tw(t, f, "clones a conversation that has not started yet", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan))
		r.host.conv = nil
		eq(t, r.run(), mentionCloneResult{Spawned: true})
		eq(t, len(first(r)["messages"].([]any)), 1) // only the prompt
		eq(t, first(r)["systemPrompt"], "the live system prompt")
	})
	tw(t, f, "carries the live system prompt rather than the one it rebuilt", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan))
		r.run()
		eq(t, first(r)["systemPrompt"], "the live system prompt")
	})
	tw(t, f, "gives the clone the Agent tool and nothing else", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan))
		r.run()
		tools := first(r)["tools"].([]any)
		eq(t, len(tools), 1)
		eq(t, tools[0].(map[string]any)["name"], "Agent")
	})
	tw(t, f, "names its own tool in the allowlist, or Pi hands it nothing", func(t *testing.T) {
		// There is no allowlist to get wrong: the request carries the tool itself. What the original guards is that the
		// tool is reachable, so a call to it must spawn.
		r := newCloneRig(callsOf(explorePlan))
		eq(t, r.run(), mentionCloneResult{Spawned: true})
		eq(t, len(first(r)["tools"].([]any)), 1)
	})
	tw(t, f, "prompts it with the message, then the reminder", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan))
		r.run()
		msgs := first(r)["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		eq(t, last["role"], "user")
		eq(t, last["content"].([]any)[0].(map[string]any)["text"], "find the flaky test\n\n"+agentMentionReminder("Explore"))
	})
	tskip(t, f, "thinks at the level the session is really on",
		"asserts createAgentSession's options; this port passes the level as the model request's reasoning option, asserted in 'a mention in model mode reaches the model with the session's own level' (mention_wiring_test.go)")
	tskip(t, f, "omits the level rather than taking buildSessionContext's, which lies",
		"asserts createAgentSession's options; the Go clone never reads buildSessionContext's level and omits reasoning for an 'off' session, asserted in mention_wiring_test.go")
	tskip(t, f, "inherits the parent's model, thinking level and providers",
		"asserts createAgentSession's options (model, modelRuntime); the Go clone asks the host for the session's model and credentials itself, asserted in mention_wiring_test.go")

	tw(t, f, "runs the real Agent handler with the MAIN context, not the fork's", func(t *testing.T) {
		// The handler is bound to the main context by the hook (a closure over it); the clone only calls it, once.
		r := newCloneRig(callsOf(explorePlan))
		r.run()
		eq(t, len(r.ran), 1)
	})
	tw(t, f, "passes no tool-call id, since the real session issued none", func(t *testing.T) {
		// The handler takes the parameters alone: no id of the clone's call can reach the record. The record's
		// ToolCallID staying empty is asserted through the hook in mention_wiring_test.go.
		r := newCloneRig(callsOf(explorePlan))
		r.run()
		_, has := r.ran[0]["tool_call_id"]
		eq(t, has, false)
	})
	tw(t, f, "forwards the parameters the clone chose", func(t *testing.T) {
		r := newCloneRig(callsOf(map[string]any{"subagent_type": "Plan", "prompt": "sketch the migration"}))
		r.run()
		eq(t, r.ran[0], map[string]any{"subagent_type": "Plan", "prompt": "sketch the migration", "run_in_background": true})
	})
	tw(t, f, "forces the spawn into the background — a foreground result goes nowhere", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan))
		r.run()
		eq(t, r.ran[0]["run_in_background"], true)
	})
	tw(t, f, "overrides a clone that explicitly asked for a foreground run", func(t *testing.T) {
		r := newCloneRig(callsOf(map[string]any{"subagent_type": "Explore", "prompt": "go", "run_in_background": false}))
		r.run()
		eq(t, r.ran[0]["run_in_background"], true)
	})
	tw(t, f, "refuses a second spawn from the same mention", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan, explorePlan))
		eq(t, r.run(), mentionCloneResult{Spawned: true})
		eq(t, len(r.ran), 1)
	})
	tw(t, f, "tells the clone why the second call was refused", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan, explorePlan))
		r.run()
		msgs := r.host.requests[1]["messages"].([]any) // the turn after the tool results
		second := msgs[len(msgs)-1].(map[string]any)
		eq(t, second["role"], "toolResult")
		eq(t, second["isError"], true)
		eq(t, strings.Contains(second["content"].([]any)[0].(map[string]any)["text"].(string), "Already started an agent"), true)
	})

	tw(t, f, "reports a turn that never called the tool", func(t *testing.T) {
		r := newCloneRig()
		res := r.run()
		eq(t, res.Spawned, false)
		eq(t, strings.Contains(res.Error, "did not start it"), true)
	})
	tw(t, f, "returns a thrown error rather than rejecting", func(t *testing.T) {
		r := newCloneRig(replyErr("no provider configured"))
		eq(t, r.run(), mentionCloneResult{Error: "no provider configured"})
	})
	tw(t, f, "keeps a spawn that already happened when the turn then fails", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan), replyErr("turn aborted"))
		eq(t, r.run(), mentionCloneResult{Spawned: true, Error: "turn aborted"})
	})
	tskip(t, f, "disposes the clone even when the turn throws",
		"there is no session object to dispose: the clone is a request the extension builds and drops, so nothing outlives a failed turn")
}

// The e2e original builds a REAL pi session and asserts the tool set it exposes; the Go clone has no session, so the
// equivalent is the request the model sees.
func TestMentionCloneToolReachabilityTwin(t *testing.T) {
	tw(t, "e2e/mention-clone-tool-reachability.e2e", "the clone's Agent tool is live on the real session, and it is the only one", func(t *testing.T) {
		r := newCloneRig()
		r.run()
		tools := r.host.requests[0]["tools"].([]any)
		var names []string
		for _, x := range tools {
			names = append(names, x.(map[string]any)["name"].(string))
		}
		eq(t, names, []string{"Agent"})
	})
}

// This port's own checks of the loop around the one spawn.
func TestMentionCloneLoop(t *testing.T) {
	t.Run("a model that keeps calling tools stops at the turn cap", func(t *testing.T) {
		var script []func() (map[string]any, error)
		for i := 0; i < 3*maxCloneTurns; i++ {
			script = append(script, callsOf(explorePlan))
		}
		r := newCloneRig(script...)
		eq(t, r.run(), mentionCloneResult{Spawned: true})
		eq(t, len(r.host.requests), maxCloneTurns)
		eq(t, len(r.ran), 1)
	})
	t.Run("an unknown tool is answered as an error and does not spawn", func(t *testing.T) {
		r := newCloneRig(func() (map[string]any, error) {
			return map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{map[string]any{"type": "toolCall", "id": "x", "name": "bash", "arguments": map[string]any{}}}}, nil
		})
		eq(t, r.run().Spawned, false)
		eq(t, len(r.ran), 0)
	})
	t.Run("a provider error stops the clone and is reported", func(t *testing.T) {
		r := newCloneRig(func() (map[string]any, error) {
			return map[string]any{"role": "assistant", "stopReason": "error", "errorMessage": "429 slow down", "content": []any{}}, nil
		})
		eq(t, r.run(), mentionCloneResult{Error: "429 slow down"})
	})
	t.Run("a panic in the spawn is a failed clone, not a crash", func(t *testing.T) {
		r := newCloneRig(callsOf(explorePlan))
		r.opts.Tool.Run = func(map[string]any) (string, bool) { panic("boom") }
		eq(t, r.run(), mentionCloneResult{Spawned: true, Error: "boom"})
	})
	t.Run("a conversation that cannot be read is a failed clone", func(t *testing.T) {
		r := newCloneRig()
		r.host.convErr = errors.New("no session")
		eq(t, r.run(), mentionCloneResult{Error: "no session"})
	})
}
