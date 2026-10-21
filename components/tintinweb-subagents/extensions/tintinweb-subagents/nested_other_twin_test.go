package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// Twins of the cases in other files of the original's suite that are about nested delegation: the runner's handling of
// `nestedRuntime` (agent-runner), the stop RPC (cross-extension-rpc), the defaults (documented-defaults), the pools
// (foreground-concurrency), the forged-ownership guard (manager-registry-guard), the settings sanitizer (settings) and
// the hidden records (status-note-wiring). The original's runner builds an in-process session and the tests mock it;
// here the runner's decision is what a child is started with: its command line (`childArgs`) and its environment
// (`childEnv`). The rest of each file stays deferred (slices.json).

var nestedToolList = []string{"Agent", "get_subagent_result", "steer_subagent"}

// childOf starts a top-level agent from an agent file and returns the process spec the runner was given.
func childOf(t *testing.T, file string) childSpec {
	t.Helper()
	cwd := t.TempDir()
	writeAgentFile(t, cwd, "worker", file)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	r := startRigIn(t, cwd)
	r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "worker"})
	return r.fleet.waitPrompt(t, "go").spec
}

// envOf is the TINTINWEB_SUBAGENT_* part of a child's environment.
func envOf(spec childSpec) map[string]string {
	out := map[string]string{}
	for _, kv := range childEnv(spec, "/tmp/usage") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "TINTINWEB_SUBAGENT_") {
			out[k] = v
		}
	}
	return out
}

// withTools is whether the child's command line admits all three nested tools.
func withTools(args []string) bool {
	i := slices.Index(args, "--tools")
	if i < 0 || i+1 >= len(args) {
		return false
	}
	list := strings.Split(args[i+1], ",")
	for _, name := range nestedToolList {
		if !slices.Contains(list, name) {
			return false
		}
	}
	return true
}

func TestAgentRunnerNestedTwins(t *testing.T) {
	const f = "agent-runner"

	tw(t, f, "leaves a nested child in memory, since nothing can address it later", func(t *testing.T) {
		// A nested child keeps its conversation in the process (no Persist): nothing can address it later.
		nested := childSpec{Allowed: "all", Depth: 2, MaxDepth: 3}
		for _, spec := range []childSpec{nested, {Allowed: "all", Depth: 2, MaxDepth: 3, NoExtensions: true}, {}} {
			eq(t, slices.Contains(childArgs(spec), "--no-session"), true)
		}
	})

	tw(t, f, "still persists a nested child that asks for it in frontmatter", func(t *testing.T) {
		r, cwd := scoutRig(t)
		writeAgentFile(t, cwd, "keeper", "persist_session: true\n")
		out := nestedAsync(r, "Agent", obj{"subagent_type": "keeper", "description": "d", "prompt": "keep"}, nil)
		c := r.fleet.waitPrompt(t, "keep")
		eq(t, c.spec.Persist, true)
		c.finish("x")
		recv(t, out)
	})

	tw(t, f, "keeps nested tools unavailable without explicit opt-in", func(t *testing.T) {
		spec := childOf(t, "extensions: false\n")
		eq(t, spec.Allowed, nil)
		eq(t, spec.nestedActive(), false)
		eq(t, strings.Contains(strings.Join(childArgs(spec), " "), "get_subagent_result"), false)
		eq(t, envOf(spec), map[string]string{depthEnv: "1"}) // a child, with no allowance and no place to delegate from
	})

	tw(t, f, "injects scoped nested tools for an opted-in non-isolated agent", func(t *testing.T) {
		spec := childOf(t, "extensions: false\nallowed_subagents: scout\n")
		// The runtime the runner is given: the owner's id, the depth, the allowlist and the config root.
		spec.Depth, spec.MaxDepth = 1, 3
		eq(t, spec.nestedActive(), true)
		eq(t, spec.Allowed, []string{"scout"})
		eq(t, withTools(childArgs(spec)), true)
		env := envOf(spec)
		eq(t, env[maxDepthEnv], "3")
		eq(t, env[allowedEnv], `["scout"]`)
		eq(t, env[depthEnv], "1")
		eq(t, env[configCwdEnv] != "", true)
		// The child loads the extension that carries the tools: `extensions: false` is held by the tool list.
		eq(t, slices.Contains(childArgs(spec), "--no-extensions"), false)
	})

	tw(t, f, "keeps opt-in nested tools active UNDER EXTENSIONS despite the EXCLUDED-name collision", func(t *testing.T) {
		spec := childOf(t, "extensions: true\nallowed_subagents: all\n")
		spec.AllowTools, spec.ExcludeTools, spec.Depth, spec.MaxDepth = nil, nil, 1, 2 // the file's own tool list is not the point: every tool stays active
		spec.SystemPrompt, spec.Model, spec.Thinking = "", "", ""
		args := childArgs(spec)
		// No tool list narrows the child, so its extension tools and the three nested tools are all active, and no
		// extension is excluded.
		eq(t, slices.Contains(args, "--tools") || slices.Contains(args, "--exclude-tools"), false)
		eq(t, slices.Contains(args, "--no-extensions"), false)
		eq(t, envOf(spec)[allowedEnv], `"all"`)
		// And the child's own extension registers exactly the three, not an orchestration tool more.
		h := hostFor(t, "1", "2", `"all"`)
		eq(t, h.tools, map[string]bool{"Agent": true, "get_subagent_result": true, "steer_subagent": true})
	})

	tw(t, f, "denies the tool under the isolated static allowlist", func(t *testing.T) {
		spec := childOf(t, "extensions: false\nallowed_subagents: all\ndisallowed_tools: Agent\n")
		eq(t, slices.Contains(spec.AllowTools, "Agent"), false)
		// The siblings the agent did NOT deny stay available.
		eq(t, hasAll(spec.AllowTools, "get_subagent_result", "steer_subagent"), true)
	})
	tw(t, f, "denies the tool at the registry gate under extensions", func(t *testing.T) {
		spec := childOf(t, "extensions: true\nallowed_subagents: all\ndisallowed_tools: Agent\n")
		eq(t, slices.Contains(spec.ExcludeTools, "Agent"), true)
		eq(t, hasAny(spec.ExcludeTools, "get_subagent_result", "steer_subagent"), false)
	})
	tskip(t, f, "blocks the tool at runtime even though it was injected as a customTool",
		"the live beforeToolCall gate is a hook of the in-process session that a child process does not have (PORT.md 11, tool scope); the denial is the child's scope flags, twinned in the two cases above")
	tw(t, f, "a partial denial does not take down the whole nested set", func(t *testing.T) {
		spec := childOf(t, "extensions: false\nallowed_subagents: all\ndisallowed_tools: steer_subagent\n")
		eq(t, spec.nestedActive(), true)
		eq(t, slices.Contains(spec.AllowTools, "steer_subagent"), false)
		eq(t, hasAll(spec.AllowTools, "Agent", "get_subagent_result"), true)
	})

	tw(t, f, "still strips the orchestration tools under extensions when nesting is OFF", func(t *testing.T) {
		// A child without an allowance registers nothing: a tool of its own extensions named Agent is not ours to
		// register, and none of the three is on offer.
		spec := childOf(t, "extensions: true\n")
		eq(t, spec.Allowed, nil)
		eq(t, hasAll(spec.ExcludeTools, nestedToolNames...), true) // all three stay out of the child's scope
		h := hostFor(t, "1", "2", "")
		eq(t, len(h.tools), 0)
	})

	tw(t, f, "suppresses nested tools in isolated mode even when opted in", func(t *testing.T) {
		cwd := t.TempDir()
		writeAgentFile(t, cwd, "worker", "extensions: false\nallowed_subagents: all\n")
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		r := startRigIn(t, cwd)
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "worker", "isolated": true})
		spec := r.fleet.waitPrompt(t, "go").spec
		eq(t, spec.nestedActive(), false)
		eq(t, spec.Allowed, nil)
		args := childArgs(spec)
		eq(t, slices.Contains(args, "--no-extensions"), true)
		eq(t, withTools(args), false)
	})

	tw(t, f, "passes the inherited depth cap through to the nested tools", func(t *testing.T) {
		spec := childOf(t, "extensions: false\nallowed_subagents: all\n")
		spec.Depth, spec.MaxDepth = 1, 3
		eq(t, envOf(spec)[maxDepthEnv], "3")
		// The child's extension reads it back as its cap.
		t.Setenv(depthEnv, "1")
		t.Setenv(maxDepthEnv, "3")
		t.Setenv(allowedEnv, `"all"`)
		eq(t, nestedConfigFromEnv().MaxDepth, 3)
	})

	tw(t, f, "injects no nested tools once the effective cap is reached", func(t *testing.T) {
		// At the cap the agent can never spawn, and so can never own a child to fetch from or steer.
		cwd := t.TempDir()
		writeAgentFile(t, cwd, "worker", "extensions: false\nallowed_subagents: all\n")
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(`{"maxSubagentDepth": 1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		r := startRigIn(t, cwd)
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "worker"})
		spec := r.fleet.waitPrompt(t, "go").spec
		eq(t, spec.nestedActive(), false)
		eq(t, withTools(childArgs(spec)), false)
		eq(t, envOf(spec), map[string]string{depthEnv: "1"})
	})
}

func TestCrossExtensionRPCNestedTwins(t *testing.T) {
	tw(t, "cross-extension-rpc", "refuses to stop another agent's nested child", func(t *testing.T) {
		r := startRig(t)
		child := r.app.mgr.spawnOwned("general-purpose", "child", "", childSpec{Prompt: "child"}, true, &owner{ParentID: "agent-1", Depth: 2, MaxDepth: 3})
		r.fleet.wait(t, 1)
		reply := r.request("subagents:rpc:stop", obj{"agentId": child.ID})
		eq(t, reply["success"], false)
		eq(t, reply["error"], "Agent is owned by another agent or workflow")
		eq(t, child.Status, statusRunning) // not aborted
		eq(t, r.fleet.wait(t, 1)[0].aborted, false)
	})
}

func TestDocumentedDefaultsNestedTwins(t *testing.T) {
	const f = "documented-defaults"
	tw(t, f, "nested subagent depth defaults to 2", func(t *testing.T) {
		// In a fresh process: other tests set the cap, so this one asks a copy of the test binary that has set nothing.
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "TW_PRINT_DEPTH=1")
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		eq(t, string(out), "2")
	})
	tw(t, f, "top-level spawns default to background, nested spawns to foreground", func(t *testing.T) {
		// The top-level Agent tool starts a background agent unless the call or the file says otherwise (an id comes
		// back at once); the nested one blocks and returns the child's answer inline, unless it is told to run in the
		// background. An explicit parameter wins over either default.
		r := startRig(t)
		r.must("Agent", obj{"prompt": "top", "description": "d", "subagent_type": "general-purpose"})
		top := r.app.mgr.list()[0]
		eq(t, top.Background, true)
		// An explicit parameter wins over the default.
		done := make(chan string, 1)
		go func() {
			text, _ := r.tool("Agent", obj{"prompt": "top-fg", "description": "d", "subagent_type": "general-purpose", "run_in_background": false})
			done <- text
		}()
		fgChild := r.fleet.waitPrompt(t, "top-fg")
		eq(t, r.app.mgr.list()[1].Background, false)
		fgChild.finish("x")
		<-done

		n, _ := scoutRig(t)
		pending := nestedAsync(n, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "nested"}, nil)
		c := n.fleet.waitPrompt(t, "nested")
		eq(t, n.app.mgr.list()[0].Background, false)
		c.finish("answer")
		eq(t, recv(t, pending).must(t).Text, "answer")

		bg := nestedExec(n, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "nested-bg", "run_in_background": true}, nil).must(t)
		eq(t, strings.HasPrefix(bg.Text, "Nested agent started in background. Agent ID: "), true)
		n.fleet.waitPrompt(t, "nested-bg")
		fg := n.app.mgr.list()[1]
		eq(t, fg.Background, true)
	})
}

func TestForegroundConcurrencyNestedTwins(t *testing.T) {
	tw(t, "foreground-concurrency", "never queues a nested child, however full the pool is", func(t *testing.T) {
		// The deadlock guard: a nested child's parent is blocked awaiting it, so queueing it behind its own parent can
		// never make progress. The pool here is the background pool (a foreground agent takes no slot at all), full.
		r := startRig(t)
		r.app.mgr.maxConcurrent = 1
		parent := r.app.mgr.spawn("general-purpose", "parent", "", childSpec{Prompt: "parent"}, true)
		r.fleet.wait(t, 1)
		eq(t, parent.Status, statusRunning)
		fgChild := r.app.mgr.spawnOwned("general-purpose", "child", "", childSpec{Prompt: "child"}, false, &owner{ParentID: parent.ID, Depth: 2, MaxDepth: 3})
		bgChild := r.app.mgr.spawnOwned("general-purpose", "child-bg", "", childSpec{Prompt: "child-bg"}, true, &owner{ParentID: parent.ID, Depth: 2, MaxDepth: 3})
		r.fleet.wait(t, 3)
		eq(t, fgChild.Status, statusRunning)
		eq(t, bgChild.Status, statusRunning)
		eq(t, r.fleet.count(), 3)
	})
}

func TestRegistryGuardNestedTwins(t *testing.T) {
	tw(t, "manager-registry-guard", "refuses a forged nesting, so the agent cannot hide under someone else's id", func(t *testing.T) {
		// A nested record is filtered out of every top-level surface and inherits its parent's delegation budget; the
		// spawn RPC is the one entry any other extension can reach, and it drops the four internal capabilities.
		r := startRig(t)
		reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "go", "options": obj{
			"description": "forged", "isBackground": true, "parentAgentId": "victim-agent-id", "depth": 9.0, "maxSubagentDepth": 99.0}})
		eq(t, reply["success"], true)
		id, _ := reply["data"].(map[string]any)["id"].(string)
		rec := r.app.mgr.get(id)
		eq(t, rec.ParentAgentID, "")
		eq(t, rec.Depth, 1)
		eq(t, rec.MaxSubagentDepth, getMaxSubagentDepth()) // the session's own cap, not the forged 99
		eq(t, rec.topLevel(), true)
		eq(t, r.fleet.wait(t, 1)[0].spec.MaxDepth, getMaxSubagentDepth())
	})
}

func TestSettingsNestedTwins(t *testing.T) {
	tw(t, "settings", "keeps maxSubagentDepth 0 (nesting off) but drops negative, fractional, and over-ceiling values", func(t *testing.T) {
		depth := func(v any) *int { return sanitizeSettings(map[string]any{"maxSubagentDepth": v}).MaxSubagentDepth }
		eq(t, *depth(0.0), 0)
		eq(t, depth(-1.0) == nil, true)
		eq(t, depth(1.5) == nil, true)
		eq(t, depth(17.0) == nil, true)
		eq(t, sanitizeSettings(map[string]any{"maxSubagentDepth": 0.0}), settings{MaxSubagentDepth: ptrInt(0)})
		eq(t, sanitizeSettings(map[string]any{"maxSubagentDepth": -1.0}), settings{})
	})
}

func TestStatusNoteWiringNestedTwins(t *testing.T) {
	const f = "status-note-wiring"

	tw(t, f, "hides nested records from top-level tools, registry, transcripts, and lifecycle", func(t *testing.T) {
		r := startRig(t)
		// External callers cannot mint internal ownership metadata.
		reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "top", "options": obj{
			"description": "top-level owner", "isBackground": true, "parentAgentId": "forged-parent", "depth": 99.0, "maxSubagentDepth": 99.0, "configCwd": "/untrusted/config"}})
		topID, _ := reply["data"].(map[string]any)["id"].(string)
		top := r.app.mgr.get(topID)
		eq(t, top.ParentAgentID, "")
		eq(t, top.Depth, 1)
		r.fleet.wait(t, 1)

		// The manager's own spawn is what the nested tools use; the nested child is owned by the top-level agent.
		emitsBefore := r.emitCount()
		msgsBefore := len(r.messages())
		child := r.app.mgr.spawnOwned("general-purpose", "nested child", "", childSpec{Prompt: "nested"}, true, &owner{ParentID: topID, Depth: 2, MaxDepth: 2})
		r.fleet.wait(t, 2)
		eq(t, r.app.mgr.resolve(child.ID) == nil, true) // not in the session's registry

		text, _ := r.tool("get_subagent_result", obj{"agent_id": child.ID})
		eq(t, strings.Contains(text, "Agent not found"), true)
		text, _ = r.tool("steer_subagent", obj{"agent_id": child.ID, "message": "stop"})
		eq(t, strings.Contains(text, "Agent not found"), true)
		// `resume` of the id finds nothing: the nested child is untouched.
		text, _ = r.tool("Agent", obj{"resume": child.ID, "prompt": "continue", "description": "resume", "subagent_type": "general-purpose"})
		eq(t, strings.Contains(text, "not found"), true)
		eq(t, child.Status, statusRunning)

		r.fleet.waitPrompt(t, "nested").finish("nested result")
		settledRec(t, child)
		eq(t, r.emitCount(), emitsBefore) // no started, completed or failed event for it
		eq(t, len(r.messages()), msgsBefore)
		eq(t, r.emitted("subagents:completed"), false)
		eq(t, r.emitted("subagents:failed"), false)
	})

	tw(t, f, "stays silent for a nested child, like every other lifecycle event", func(t *testing.T) {
		r := startRig(t)
		top := r.app.mgr.spawn("general-purpose", "top", "", childSpec{Prompt: "top"}, true)
		child := r.app.mgr.spawnOwned("general-purpose", "nested", "", childSpec{Prompt: "nested"}, true, &owner{ParentID: top.ID, Depth: 2, MaxDepth: 3})
		r.fleet.wait(t, 2)
		before := r.emitCount()
		r.app.mgr.onCompact(child, "threshold", 1000, 1) // what a nested child's compaction reports
		eq(t, r.emitCount(), before)
	})
}

func TestUsageAndGCNestedTwins(t *testing.T) {
	tw(t, "usage-reporting", "counts a nested child's spend once, on the top-level agent's report", func(t *testing.T) {
		// The whole tree's spend reaches the top-level record once, through the owners' usage files (nested_tools twin:
		// attributes spend up the whole ancestor chain); the session's report is pooled from the top-level agent's own
		// messages only, so the nested child's message is not pooled a second time.
		r := startRig(t)
		var seen int
		r.app.mgr.onUsage = func(rec *agentRecord, _ lifetimeUsage) {
			if rec.topLevel() {
				seen++
			}
		}
		top := r.app.mgr.spawn("general-purpose", "top", "", childSpec{Prompt: "top"}, true)
		child := r.app.mgr.spawnOwned("general-purpose", "nested", "", childSpec{Prompt: "nested"}, true, &owner{ParentID: top.ID, Depth: 2, MaxDepth: 3})
		r.fleet.waitPrompt(t, "nested").finish("x")
		settledRec(t, child)
		eq(t, seen, 0)
	})
	tskip(t, "agent-manager-gc", "ignores a reclaim on a nested child, which has no name to hold",
		"`reclaim` (tombstones and @mention revival) is not ported; the invariant that a nested child holds no handle or alias is twinned in agent-manager: never names a nested child")
}

// emitCount is how many events the extension has emitted on the bus.
func (r *rig) emitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.emits)
}
