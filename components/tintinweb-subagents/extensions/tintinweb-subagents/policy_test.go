package tintinweb_subagents

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The settings this slice owns, as they reach the model-facing behavior: the join of completion notifications, the
// background default, the model and its scope, the description mode.

// A fake clock for the batch window, the group timeout and the nudge hold.
func (r *rig) fakeTime() *fakeClock {
	c := &fakeClock{}
	r.app.after = c.after
	r.app.groups.after = c.after
	return c
}

func TestJoinModesShapeTheCompletionNotifications(t *testing.T) {
	spawnTwo := func(t *testing.T, r *rig) (a, b *agentRecord) {
		r.must("Agent", obj{"prompt": "one", "description": "first", "subagent_type": "Plan"})
		r.must("Agent", obj{"prompt": "two", "description": "second", "subagent_type": "Plan"})
		recs := r.app.mgr.list()
		return recs[0], recs[1]
	}
	finishBoth := func(r *rig, a, b *agentRecord) {
		r.childFor("one").finish("A-RESULT")
		r.childFor("two").finish("B-RESULT")
		<-a.done
		<-b.done
	}
	contents := func(r *rig) []string {
		var out []string
		for _, m := range r.notifications() {
			out = append(out, m["message"].(map[string]any)["content"].(string))
		}
		return out
	}
	t.Run("smart (the default) joins the agents of one turn into one notification", func(t *testing.T) {
		r := startRig(t)
		clock := r.fakeTime()
		a, b := spawnTwo(t, r)
		clock.advance(batchWindow) // the batch closes: two smart agents are a group
		finishBoth(r, a, b)
		clock.advance(nudgeHold)
		got := contents(r)
		eq(t, len(got), 1)
		eq(t, strings.HasPrefix(got[0], "Background agent group completed: 2 agent(s) finished\n\n<task-notification>"), true)
		eq(t, strings.HasSuffix(got[0], "\n\nUse get_subagent_result for full output."), true)
		eq(t, strings.Contains(got[0], "<result>A-RESULT</result>") && strings.Contains(got[0], "<result>B-RESULT</result>"), true)
		eq(t, a.GroupID != "" && a.GroupID == b.GroupID, true)
	})
	t.Run("a group whose members finish before the batch closes is joined when it does", func(t *testing.T) {
		r := startRig(t)
		clock := r.fakeTime()
		a, b := spawnTwo(t, r)
		finishBoth(r, a, b) // both settle inside the debounce window: their notifications are deferred to finalizeBatch
		clock.advance(nudgeHold)
		eq(t, len(contents(r)), 0)
		clock.advance(batchWindow)
		clock.advance(nudgeHold)
		eq(t, len(contents(r)), 1)
	})
	t.Run("async sends one notification per agent", func(t *testing.T) {
		r := rigWithSettings(t, `{"defaultJoinMode":"async"}`)
		clock := r.fakeTime()
		a, b := spawnTwo(t, r)
		finishBoth(r, a, b)
		clock.advance(nudgeHold)
		got := contents(r)
		eq(t, len(got), 2)
		for _, c := range got {
			eq(t, strings.HasPrefix(c, "<task-notification>"), true)
		}
		eq(t, a.GroupID, "")
	})
	t.Run("a lone smart agent is announced on its own", func(t *testing.T) {
		r := startRig(t)
		clock := r.fakeTime()
		r.must("Agent", obj{"prompt": "one", "description": "first", "subagent_type": "Plan"})
		clock.advance(batchWindow)
		r.childFor("one").finish("A-RESULT")
		<-r.app.mgr.list()[0].done
		clock.advance(nudgeHold)
		got := contents(r)
		eq(t, len(got), 1)
		eq(t, strings.HasPrefix(got[0], "<task-notification>"), true)
	})
	t.Run("the group notification is partial when the timeout fires first", func(t *testing.T) {
		r := startRig(t)
		clock := r.fakeTime()
		a, _ := spawnTwo(t, r)
		clock.advance(batchWindow)
		r.childFor("one").finish("A-RESULT")
		<-a.done
		clock.advance(groupJoinTimeout)
		clock.advance(nudgeHold)
		got := contents(r)
		eq(t, len(got), 1)
		eq(t, strings.HasPrefix(got[0], "Background agent group completed: 1 agent(s) finished (partial — others still running)\n\n"), true)
	})
	t.Run("a result read before the notification is due cancels it, for a group too", func(t *testing.T) {
		r := startRig(t)
		clock := r.fakeTime()
		a, b := spawnTwo(t, r)
		clock.advance(batchWindow)
		finishBoth(r, a, b)
		r.must("get_subagent_result", obj{"agent_id": a.ID})
		r.must("get_subagent_result", obj{"agent_id": b.ID})
		clock.advance(nudgeHold)
		eq(t, len(contents(r)), 0)
	})
	t.Run("a foreground agent is never part of a batch", func(t *testing.T) {
		r := startRig(t)
		clock := r.fakeTime()
		call := r.fgCall("one")
		r.childFor("one").finish("x")
		call.result()
		clock.advance(batchWindow + nudgeHold)
		eq(t, len(contents(r)), 0)
		eq(t, len(r.app.batch), 0)
	})
}

func TestBackgroundByDefaultFalseBlocksTheCall(t *testing.T) {
	r := rigWithSettings(t, `{"backgroundByDefault":false}`)
	call := r.startTool("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose"})
	r.childFor("go").finish("THE-PAYLOAD")
	out, _ := call.result()
	eq(t, contains(out, "THE-PAYLOAD"), true)
	// an explicit call parameter overrides the setting in both directions
	out = r.must("Agent", obj{"prompt": "bg", "description": "d", "subagent_type": "general-purpose", "run_in_background": true})
	eq(t, contains(out, "started in background"), true)
}

func TestAgentFileOutranksTheCallForBackgroundThinkingAndTurns(t *testing.T) {
	r := startRig(t)
	cwd := r.Host.opts.Cwd
	writeSettings(t, filepath.Join(cwd, ".pi", "agents", "locked.md"), "---\ndescription: Locked\nrun_in_background: false\nthinking: low\nmax_turns: 7\n---\nBody.\n")
	call := r.startTool("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "locked", "run_in_background": true, "thinking": "high", "max_turns": 99})
	c := r.childFor("go")
	eq(t, [2]any{c.spec.Thinking, c.spec.MaxTurns}, [2]any{"low", 7})
	c.finish("blocked")
	out, _ := call.result()
	eq(t, contains(out, "Agent completed in"), true) // the file's foreground wins over the call's run_in_background: true
}

// A host with two models, one of them available.
func modelRig(t *testing.T, settings string) *rig {
	r := rigWithSettings(t, settings)
	r.modelState = obj{
		"models": []any{
			obj{"id": "claude-opus-4-6", "name": "Claude Opus 4.6", "provider": "anthropic"},
			obj{"id": "claude-haiku-4-5", "name": "Claude Haiku 4.5", "provider": "anthropic"},
			obj{"id": "gpt-4o", "name": "GPT-4o", "provider": "openai"},
		},
		"providers": obj{"anthropic": obj{"configured": true}, "openai": obj{"configured": true}},
	}
	return r
}

func TestAgentModelParameterIsResolvedAndScoped(t *testing.T) {
	t.Run("a fuzzy name resolves to the model the child runs with", func(t *testing.T) {
		r := modelRig(t, `{}`)
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "Plan", "model": "haiku"})
		eq(t, r.childFor("go").spec.Model, "anthropic/claude-haiku-4-5")
	})
	t.Run("a name that matches nothing is an error the model reads, listing the models", func(t *testing.T) {
		r := modelRig(t, `{}`)
		out := r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "Plan", "model": "nonexistent-xyz"})
		eq(t, strings.HasPrefix(out, "Model not found: \"nonexistent-xyz\".\n\nAvailable models:\n  anthropic/claude-haiku-4-5\n"), true)
		r.noMoreChildren(0)
	})
	t.Run("the agent file's model that does not resolve falls back silently", func(t *testing.T) {
		r := modelRig(t, `{}`)
		writeSettings(t, filepath.Join(r.Host.opts.Cwd, ".pi", "agents", "pinned.md"), "---\ndescription: Pinned\nmodel: nowhere/ghost\n---\nBody.\n")
		out := r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "pinned"})
		eq(t, contains(out, "started in background"), true)
		r.childFor("go")
	})
	t.Run("the file's model wins over the call's", func(t *testing.T) {
		r := modelRig(t, `{}`)
		writeSettings(t, filepath.Join(r.Host.opts.Cwd, ".pi", "agents", "pinned.md"), "---\ndescription: Pinned\nmodel: openai/gpt-4o\n---\nBody.\n")
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "pinned", "model": "haiku"})
		eq(t, r.childFor("go").spec.Model, "openai/gpt-4o")
	})
	t.Run("the default Explore model is used when it is available", func(t *testing.T) {
		r := modelRig(t, `{}`)
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "Explore"})
		eq(t, r.childFor("go").spec.Model, "anthropic/claude-haiku-4-5")
	})
	scope := func(t *testing.T) *rig {
		r := modelRig(t, `{"scopeModels":true}`)
		writeJSONFile(t, filepath.Join(r.Host.opts.Cwd, ".pi", "settings.json"), obj{"enabledModels": []string{"anthropic/claude-haiku-4-5"}})
		return r
	}
	t.Run("scopeModels refuses a model the call chose outside enabledModels", func(t *testing.T) {
		r := scope(t)
		out := r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "Plan", "model": "gpt-4o"})
		eq(t, out, "Model not in scope: \"gpt-4o\".\n\nAllowed models (from enabledModels):\n  anthropic/claude-haiku-4-5")
		r.noMoreChildren(0)
	})
	t.Run("scopeModels lets an in-scope choice through", func(t *testing.T) {
		r := scope(t)
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "Plan", "model": "haiku"})
		r.childFor("go")
	})
	t.Run("scopeModels only warns for a model the agent file pins, and the agent runs", func(t *testing.T) {
		r := scope(t)
		writeSettings(t, filepath.Join(r.Host.opts.Cwd, ".pi", "agents", "pinned.md"), "---\ndescription: Pinned\nmodel: openai/gpt-4o\n---\nBody.\n")
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "pinned"})
		r.childFor("go")
		var warned bool
		for _, c := range r.CallsTo("ui.notify") {
			if m, _ := c.Args["message"].(string); m == `Agent "pinned" using out-of-scope model "openai/gpt-4o"` && c.Args["level"] == "warning" {
				warned = true
			}
		}
		eq(t, warned, true)
	})
	t.Run("the RPC spawn resolves a model override and applies the scope's hard error", func(t *testing.T) {
		r := scope(t)
		reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "go", "options": obj{"description": "d", "model": "gpt-4o"}})
		eq(t, reply["success"], false)
		eq(t, reply["error"], "Model not in scope: \"gpt-4o\".\n\nAllowed models (from enabledModels):\n  anthropic/claude-haiku-4-5")
		reply = r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "go", "options": obj{"description": "d", "model": "nonexistent-xyz"}})
		eq(t, reply["success"], false)
		eq(t, strings.HasPrefix(reply["error"].(string), "Model not found:"), true)
		reply = r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "go", "options": obj{"description": "d", "model": "haiku"}})
		eq(t, reply["success"], true)
		eq(t, r.childFor("go").spec.Model, "anthropic/claude-haiku-4-5")
	})
	t.Run("a null model in the RPC options means inherit", func(t *testing.T) {
		r := scope(t)
		reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "go", "options": obj{"description": "d", "model": nil}})
		eq(t, reply["success"], true)
	})
}

func TestMaxConcurrentSettingQueuesTheTail(t *testing.T) {
	r := rigWithSettings(t, `{"maxConcurrent":2}`)
	var outs []string
	for i := 0; i < 3; i++ {
		outs = append(outs, r.must("Agent", obj{"prompt": "go" + itoa(i), "description": "d", "subagent_type": "Plan"}))
	}
	eq(t, [3]bool{contains(outs[0], "queued"), contains(outs[1], "queued"), contains(outs[2], "Position: queued (max 2 concurrent)")}, [3]bool{false, false, true})
	_ = time.Second
}

func TestSettingsLoadedIsAnnouncedOnceAfterTheRegistration(t *testing.T) {
	r := startRigNoSession(t)
	r.Fire("session_start", obj{"reason": "startup"})
	r.waitEmitted("subagents:ready")
	var order []string
	for _, c := range r.emits {
		if c == "subagents:settings_loaded" || c == "subagents:ready" {
			order = append(order, c)
		}
	}
	eq(t, order, []string{"subagents:settings_loaded", "subagents:ready"})
	r.Fire("session_start", obj{"reason": "resume"})
	n := 0
	for _, c := range r.emits {
		if c == "subagents:settings_loaded" {
			n++
		}
	}
	eq(t, n, 1)
}

func TestTheBatchWindowIsDebounced(t *testing.T) {
	// Every new agent resets the window, so parallel tool calls dispatched across several ticks are captured together.
	r := startRig(t)
	clock := r.fakeTime()
	for _, p := range []string{"one", "two", "three"} {
		r.must("Agent", obj{"prompt": p, "description": p, "subagent_type": "Plan"})
		clock.advance(60 * time.Millisecond) // past the window since the first agent, inside it since the last
	}
	r.app.mu.Lock()
	n := len(r.app.batch)
	r.app.mu.Unlock()
	eq(t, n, 3)
	clock.advance(batchWindow)
	for _, p := range []string{"one", "two", "three"} {
		r.childFor(p).finish(p)
	}
	for _, rec := range r.app.mgr.list() {
		<-rec.done
	}
	clock.advance(nudgeHold)
	got := r.notifications()
	eq(t, len(got), 1)
	eq(t, strings.HasPrefix(got[0]["message"].(map[string]any)["content"].(string), "Background agent group completed: 3 agent(s) finished"), true)
}

func TestEnabledModelsAreReReadWhenTheFileChanges(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	project := t.TempDir()
	reg := makeRegistry(enabledModelsRegistryModels)
	file := filepath.Join(project, ".pi", "settings.json")
	writeJSONFile(t, file, obj{"enabledModels": []string{"google/gemma-4-31b-it"}})
	first, _ := readEnabledModels(project)
	eq(t, resolveEnabledModels(first, true, reg, project), map[string]bool{"google/gemma-4-31b-it": true})
	// The same patterns, but another registry and a changed file (here only its size): the memo must not serve the old set.
	writeJSONFile(t, file, obj{"enabledModels": []string{"google/gemma-4-31b-it"}, "padding": "xxxxxxxx"})
	eq(t, resolveEnabledModels(first, true, makeRegistry(enabledModelsRegistryModels[:0]), project) == nil, true)
}

func TestAnOwnedChildStopsWithItsParent(t *testing.T) {
	r := startRig(t)
	parent := r.app.mgr.spawnWith("general-purpose", "parent", childSpec{Prompt: "parent"}, spawnOptions{Background: true})
	child := r.app.mgr.spawnWith("general-purpose", "child", childSpec{Prompt: "child"}, spawnOptions{Background: true, ParentAgentID: parent.ID, Depth: 2})
	r.childFor("parent")
	r.childFor("child")
	r.childFor("parent").finish("done")
	<-parent.done
	<-child.done
	eq(t, r.status(child), statusStopped)
}

func (r *rig) status(rec *agentRecord) string {
	r.app.mgr.mu.Lock()
	defer r.app.mgr.mu.Unlock()
	return rec.Status
}

func TestTheStopRPCLeavesOwnedChildrenAlone(t *testing.T) {
	r := startRig(t)
	parent := r.app.mgr.spawnWith("general-purpose", "parent", childSpec{Prompt: "parent"}, spawnOptions{Background: true})
	child := r.app.mgr.spawnWith("general-purpose", "child", childSpec{Prompt: "child"}, spawnOptions{Background: true, ParentAgentID: parent.ID, Depth: 2})
	r.childFor("child")
	reply := r.request("subagents:rpc:stop", obj{"agentId": child.ID})
	eq(t, [2]any{reply["success"], reply["error"]}, [2]any{false, "Agent is owned by another agent or workflow"})
	eq(t, r.status(child), statusRunning)
	reply = r.request("subagents:rpc:consume", obj{"agentId": child.ID})
	eq(t, reply["success"], false)
}

func TestShutdownUnsubscribesTheRPCListeners(t *testing.T) {
	r := startRig(t)
	eq(t, len(r.CallsTo("events.off")), 0)
	r.Fire("session_shutdown", obj{})
	eq(t, len(r.CallsTo("events.off")), 4) // ping, spawn, stop, consume
}

func TestAQueuedAgentHasNoOutputYet(t *testing.T) {
	r := rigWithSettings(t, `{"maxConcurrent":1}`)
	r.bgSpawn("one")
	id, queued := r.bgSpawn("two")
	eq(t, queued, true)
	out := r.must("get_subagent_result", obj{"agent_id": id})
	eq(t, strings.HasSuffix(out, "\n\nNo output."), true) // only a running agent is told to check back later
	eq(t, contains(out, "still running"), false)
}
