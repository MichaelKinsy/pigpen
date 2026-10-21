package tintinweb_subagents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// What the extension does in a child, what the top-level session gives a child, and the nested tools' own rules.

func TestChildArgsForNestedDelegation(t *testing.T) {
	nested := func(s childSpec) childSpec { s.Allowed, s.Depth, s.MaxDepth = "all", 1, 2; return s }
	base := []string{"--mode", "rpc", "--no-session", "--no-context-files", "--no-prompt-templates", "--no-themes", "--append-system-prompt", ""}
	names := "Agent,get_subagent_result,steer_subagent"
	// The nested tools are admitted by the scope (planScope re-admits them), and the extension that carries them is loaded:
	// `extensions: false` is held by the tool allowlist, not by --no-extensions.
	eq(t, childArgs(nested(childSpec{NoExtensions: true, AllowTools: []string{"read", "Agent", "get_subagent_result", "steer_subagent"}})), append(slices.Clone(base), "--tools", "read,"+names))
	eq(t, childArgs(nested(childSpec{NoExtensions: true, AllowTools: []string{"read"}})), append(slices.Clone(base), "--tools", "read")) // what the allowlist says is all there is
	eq(t, childArgs(nested(childSpec{ExcludeTools: []string{"write"}})), append(slices.Clone(base), "--exclude-tools", "write"))
	// Without an allowance, or at the cap, nothing changes.
	eq(t, childArgs(childSpec{NoExtensions: true, AllowTools: []string{"read"}, Depth: 1, MaxDepth: 2}), append(slices.Clone(base), "--no-extensions", "--tools", "read"))
	eq(t, childArgs(childSpec{Allowed: "all", NoExtensions: true, Depth: 2, MaxDepth: 2}), append(slices.Clone(base), "--no-extensions"))
	eq(t, childArgs(childSpec{Allowed: "all", Depth: 5, MaxDepth: 2, AllowTools: []string{"read"}}), append(slices.Clone(base), "--tools", "read"))
	eq(t, childArgs(childSpec{Allowed: "all", Depth: 0, MaxDepth: 0}), base)
}

// subagentEnv is every variable of the namespace a child is given.
var subagentEnv = []string{depthEnv, maxDepthEnv, allowedEnv, agentIDEnv, configCwdEnv, usageEnv, rootSessionEnv}

func TestChildEnvironment(t *testing.T) {
	for _, name := range subagentEnv {
		eq(t, strings.HasPrefix(name, envPrefix), true)
	}
	for _, name := range subagentEnv {
		t.Setenv(name, "stale") // a parent that is itself a child must not hand its own place down
	}
	env := func(spec childSpec) map[string]string {
		out := map[string]string{}
		for _, kv := range childEnv(spec, "/tmp/usage") {
			if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "TINTINWEB_SUBAGENT_") {
				out[k] = v
			}
		}
		return out
	}
	eq(t, env(childSpec{}), map[string]string{depthEnv: "1"}) // a child is at least depth 1
	eq(t, env(childSpec{Depth: 3, MaxDepth: 5, AgentID: "x"}), map[string]string{depthEnv: "3"})
	eq(t, env(childSpec{Depth: 1, MaxDepth: 2, Allowed: []string{"worker", "scout"}, AgentID: "abc", ConfigCwd: "/cfg"}), map[string]string{
		depthEnv: "1", maxDepthEnv: "2", allowedEnv: `["worker","scout"]`, agentIDEnv: "abc", configCwdEnv: "/cfg", usageEnv: "/tmp/usage", rootSessionEnv: ""})
	eq(t, env(childSpec{Depth: 1, MaxDepth: 2, Allowed: "all", RootSessionID: "root-1"})[rootSessionEnv], "root-1")
	eq(t, env(childSpec{Depth: 1, MaxDepth: 2, Allowed: "all", AgentID: "abc", ConfigCwd: "/cfg"})[allowedEnv], `"all"`)
}

func TestNestedConfigFromEnv(t *testing.T) {
	set := func(depth, max, allowed string) {
		t.Setenv(depthEnv, depth)
		t.Setenv(maxDepthEnv, max)
		t.Setenv(allowedEnv, allowed)
		t.Setenv(agentIDEnv, "me")
		t.Setenv(configCwdEnv, "/cfg")
		t.Setenv(usageEnv, "/u")
		t.Setenv(rootSessionEnv, "root-1")
	}
	set("1", "2", `["a","b"]`)
	n := nestedConfigFromEnv()
	eq(t, n.Allowed, []string{"a", "b"})
	eq(t, []any{n.AgentID, n.Depth, n.MaxDepth, n.ConfigCwd, n.UsageFile}, []any{"me", 1, 2, "/cfg", "/u"})
	eq(t, n.RootSessionID, "root-1")
	eq(t, n.toolsEnabled(), true)
	set("1", "2", `"all"`)
	eq(t, nestedConfigFromEnv().Allowed, "all")
	for _, bad := range []string{"", `"none"`, `[]`, `[1]`, `{}`, `nope`, `true`} {
		set("1", "2", bad)
		eq(t, nestedConfigFromEnv() == nil, true)
	}
	set("2", "2", `"all"`)
	eq(t, nestedConfigFromEnv().toolsEnabled(), false) // at the cap
	set("3", "2", `"all"`)
	eq(t, nestedConfigFromEnv().toolsEnabled(), false)
	set("1", "2", `"all"`)
	t.Setenv(configCwdEnv, "")
	wd, _ := os.Getwd()
	eq(t, nestedConfigFromEnv().ConfigCwd, wd)
}

// hostFor starts the extension a child process of the given environment would load.
func hostFor(t *testing.T, depth, max, allowed string) *Host {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv(depthEnv, depth)
	t.Setenv(maxDepthEnv, max)
	t.Setenv(allowedEnv, allowed)
	t.Setenv(agentIDEnv, "me")
	h := StartHost(t, Extension(), HostOptions{Cwd: t.TempDir()})
	h.Fire("session_start", obj{"reason": "startup"})
	return h
}

func TestExtensionInAChild(t *testing.T) {
	t.Run("a child without an allowance registers nothing", func(t *testing.T) {
		h := hostFor(t, "1", "2", "")
		eq(t, len(h.tools), 0)
		eq(t, len(h.cmds), 0)
		eq(t, len(h.CallsTo("events.emit")), 0) // no subagents:ready, and no RPC to answer
		eq(t, len(h.CallsTo("events.on")), 0)
	})
	t.Run("a child with an allowance below the cap gets the nested tools, and only them", func(t *testing.T) {
		h := hostFor(t, "1", "2", `["worker"]`)
		eq(t, h.tools, map[string]bool{"Agent": true, "get_subagent_result": true, "steer_subagent": true})
		eq(t, len(h.cmds), 0)
		eq(t, len(h.CallsTo("events.emit")), 0) // nested agents emit no top-level events
		eq(t, len(h.CallsTo("events.on")), 0)   // and answer no RPC
	})
	t.Run("at the cap it gets none", func(t *testing.T) {
		h := hostFor(t, "2", "2", `"all"`)
		eq(t, len(h.tools), 0)
	})
	t.Run("a raised cap lets the next level delegate", func(t *testing.T) {
		h := hostFor(t, "2", "3", `"all"`)
		eq(t, len(h.tools), 3)
	})
	t.Run("the top-level session still registers its tools and the RPC", func(t *testing.T) {
		t.Setenv(depthEnv, "")
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		h := StartHost(t, Extension(), HostOptions{Cwd: t.TempDir()})
		h.Fire("session_start", obj{"reason": "startup"})
		eq(t, h.tools, map[string]bool{"Agent": true, "get_subagent_result": true, "steer_subagent": true})
		eq(t, h.cmds["agents"], true)
	})
}

// The tool definitions are the original's: the text, the parameter names and descriptions, the required list.
// upstream: nested-tools.ts.
func TestNestedAgentSchema(t *testing.T) {
	got, _ := json.Marshal(nestedAgentSchema([]string{"worker", "scout"}, false))
	want := `{"properties":{"description":{"description":"Short 3-5 word task description.","type":"string"},` +
		`"inherit_context":{"type":"boolean"},"isolated":{"type":"boolean"},"max_turns":{"minimum":1,"type":"number"},` +
		`"model":{"description":"Optional provider/model override.","type":"string"},` +
		`"prompt":{"description":"Self-contained task for the nested agent.","type":"string"},` +
		`"resume":{"description":"Resume a nested agent owned by this parent.","type":"string"},` +
		`"run_in_background":{"description":"Defaults to false for nested spawns — the call blocks and returns the child's result inline. Set true only for work you will collect later with get_subagent_result; a detached child is stopped when you finish.","type":"boolean"},` +
		`"subagent_type":{"description":"Allowed nested agent type. Available: worker, scout.","type":"string"},` +
		`"thinking":{"description":"Optional thinking level.","type":"string"}},"required":["prompt","description","subagent_type"],"type":"object"}`
	eq(t, string(got), want)
	var empty map[string]any
	b, _ := json.Marshal(nestedAgentSchema(nil, false))
	_ = json.Unmarshal(b, &empty)
	eq(t, empty["properties"].(map[string]any)["subagent_type"].(map[string]any)["description"], "Allowed nested agent type. Available: none.")
	// `isolation` is there while worktrees are allowed, with the top-level tool's own text.
	withIso := nestedAgentSchema([]string{"worker"}, true)["properties"].(map[string]any)
	eq(t, withIso["isolation"], texts.IsolationParam)
	eq(t, nestedAgentSchema([]string{"worker"}, false)["properties"].(map[string]any)["isolation"] == nil, true)
}

// registeredNestedRig is a child's session with the nested tools registered through the SDK, its children fake processes.
func registeredNestedRig(t *testing.T, cwd string, cfg *nestedConfig) *rig {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	resetTypes()
	t.Cleanup(resetTypes)
	r := &rig{fleet: &fleet{}, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	e := sdk.New("tintinweb-subagents")
	r.app = newApp(e, r.fleet.start)
	r.app.registerNested(e, cfg)
	r.Host = StartHost(t, e, HostOptions{Cwd: cwd, OnCallValue: r.answer})
	r.Fire("session_start", obj{"reason": "startup"})
	t.Cleanup(func() { r.app.mgr.abortAll() })
	return r
}

func TestNestedToolsThroughTheSDK(t *testing.T) {
	cwd := t.TempDir()
	writeAgentFile(t, cwd, "scout", "")
	r := registeredNestedRig(t, cwd, &nestedConfig{AgentID: "parent-1", Depth: 1, MaxDepth: 2, Allowed: "all", ConfigCwd: cwd})
	eq(t, r.tools, map[string]bool{"Agent": true, "get_subagent_result": true, "steer_subagent": true})

	// A foreground call blocks until the child answers, and is foreground by default.
	type out struct {
		text    string
		isError bool
	}
	got := make(chan out, 1)
	go func() {
		raw, failure := r.Host.Tool("Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "find it"})
		if failure != "" {
			t.Errorf("failed: %s", failure)
		}
		text, isErr := toolJSON(raw)
		got <- out{text, isErr}
	}()
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.Prompt, "find it")
	eq(t, r.app.mgr.list()[0].Background, false)
	c.finish("found it")
	o := <-got
	eq(t, o, out{"found it", false})

	// An unknown agent is a tool error, not a failed call.
	raw, failure := r.Host.Tool("Agent", obj{"subagent_type": "nope", "description": "d", "prompt": "p"})
	eq(t, failure, "")
	text, isErr := toolJSON(raw)
	eq(t, isErr, true)
	eq(t, strings.HasPrefix(text, `Unknown or disabled nested agent type: "nope".`), true)

	// An agent id of another owner.
	raw, _ = r.Host.Tool("get_subagent_result", obj{"agent_id": "zzz"})
	text, isErr = toolJSON(raw)
	eq(t, []any{text, isErr}, []any{`Nested agent not found or not owned by this parent: "zzz".`, true})
	raw, _ = r.Host.Tool("steer_subagent", obj{"agent_id": "zzz", "message": "m"})
	text, isErr = toolJSON(raw)
	eq(t, []any{text, isErr}, []any{`Running nested agent not found or not owned by this parent: "zzz".`, true})
}

// What keeps a child's own children from outliving it: the agent's settle and the session's end.
func TestWhatAnAgentOwnsStopsWithIt(t *testing.T) {
	setup := func(t *testing.T) (*rig, *agentRecord, *agentRecord) {
		cwd := t.TempDir()
		r := registeredNestedRig(t, cwd, &nestedConfig{AgentID: "parent-1", Depth: 1, MaxDepth: 3, Allowed: "all", ConfigCwd: cwd})
		mine := r.app.mgr.spawnOwned("scout", "mine", "", childSpec{Prompt: "mine"}, true, &owner{ParentID: "parent-1", Depth: 2, MaxDepth: 3})
		other := r.app.mgr.spawnOwned("scout", "other", "", childSpec{Prompt: "other"}, true, &owner{ParentID: "someone-else", Depth: 2, MaxDepth: 3})
		r.fleet.wait(t, 2)
		return r, mine, other
	}
	t.Run("agent_settled stops the children this agent owns, and only them", func(t *testing.T) {
		r, mine, other := setup(t)
		r.Fire(sdk.EventAgentSettled, obj{})
		settledRec(t, mine)
		eq(t, mine.Status, statusStopped)
		eq(t, other.Status, statusRunning)
	})
	t.Run("session_shutdown stops every child", func(t *testing.T) {
		r, mine, other := setup(t)
		r.Fire(sdk.EventSessionShutdown, obj{"reason": "quit"})
		settledRec(t, mine)
		settledRec(t, other)
		eq(t, []string{mine.Status, other.Status}, []string{statusStopped, statusStopped})
	})
}

func TestNestedChildrenAreForegroundAndReportOnlyToTheirOwner(t *testing.T) {
	r, cwd := scoutRig(t)
	writeAgentFile(t, cwd, "bgfile", "run_in_background: true\n")
	r.modelState = obj{
		"models": []any{
			obj{"id": "pinned", "name": "Pinned", "provider": "provider"},
			obj{"id": "model", "name": "Model", "provider": "other"},
		},
		"providers": obj{"provider": obj{"configured": true}, "other": obj{"configured": true}},
	}
	writeAgentFile(t, cwd, "pinned", "model: provider/pinned\nthinking: low\nmax_turns: 3\n")

	// Foreground without a word about it: the call blocks. An explicit true detaches it.
	out := nestedAsync(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p1"}, nil)
	c := r.fleet.waitPrompt(t, "p1")
	select {
	case <-out:
		t.Fatal("a foreground spawn returned before the child did")
	case <-time.After(50 * time.Millisecond):
	}
	c.finish("x")
	recv(t, out)
	eq(t, nestedExec(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p2", "run_in_background": true}, nil).must(t).IsError, false)
	eq(t, r.fleet.waitPrompt(t, "p2") != nil, true)
	// The agent file outranks the call. upstream: resolveAgentInvocationConfig.
	nestedExec(r, "Agent", obj{"subagent_type": "bgfile", "description": "d", "prompt": "p3", "run_in_background": false}, nil).must(t)
	eq(t, r.app.mgr.get(strings.TrimPrefix(nestedExec(r, "Agent", obj{"subagent_type": "bgfile", "description": "d", "prompt": "p4"}, nil).Text, "Nested agent started in background. Agent ID: ")) != nil, true)
	nestedExec(r, "Agent", obj{"subagent_type": "pinned", "description": "d", "prompt": "p5", "run_in_background": true,
		"model": "other/model", "thinking": "high", "max_turns": 9.0}, nil).must(t)
	spec := r.fleet.waitPrompt(t, "p5").spec
	eq(t, []any{spec.Model, spec.Thinking, spec.MaxTurns}, []any{"provider/pinned", "low", 3})
	// A caller's values apply where the file says nothing.
	nestedExec(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p6", "run_in_background": true,
		"model": "other/model", "thinking": "high", "max_turns": 9.0}, nil).must(t)
	spec = r.fleet.waitPrompt(t, "p6").spec
	eq(t, []any{spec.Model, spec.Thinking, spec.MaxTurns}, []any{"other/model", "high", 9})

	// None of them is announced: the owner is the only one who hears.
	r.fleet.waitPrompt(t, "p2").finish("x")
	r.fleet.waitPrompt(t, "p4").finish("x")
	time.Sleep(nudgeHold + 300*time.Millisecond)
	for _, ch := range []string{"subagents:created", "subagents:completed", "subagents:failed", "subagents:steered"} {
		eq(t, r.emitted(ch), false)
	}
	eq(t, len(r.messages()), 0)
}

func TestANestedCallThatIsCancelledStopsItsChild(t *testing.T) {
	r, _ := scoutRig(t)
	done := make(chan struct{})
	out := nestedAsync(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p"}, done)
	c := r.fleet.wait(t, 1)[0]
	close(done)
	o := recv(t, out).must(t)
	eq(t, c.aborted, true)
	eq(t, strings.HasPrefix(o.Text, "Nested agent (STOPPED BY THE USER — everything the agent produced is above"), true)
}

func TestNestedSteerReachesTheChild(t *testing.T) {
	r, _ := scoutRig(t)
	id := strings.TrimPrefix(nestedExec(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p", "run_in_background": true}, nil).Text, "Nested agent started in background. Agent ID: ")
	c := r.fleet.wait(t, 1)[0]
	// The child is up once its record has one; steers sent before that are queued and flushed.
	for i := 0; i < 200; i++ {
		r.app.mgr.mu.Lock()
		up := r.app.mgr.records[id].child != nil
		r.app.mgr.mu.Unlock()
		if up {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	o := nestedExec(r, "steer_subagent", obj{"agent_id": id, "message": "left"}, nil).must(t)
	eq(t, []any{o.Text, o.IsError}, []any{"Steering message sent to nested agent " + id + ".", false})
	c.mu.Lock()
	eq(t, c.steered, []string{"left"})
	c.mu.Unlock()
	// A finished agent is no longer running.
	c.finish("x")
	<-r.app.mgr.get(id).done
	o = nestedExec(r, "steer_subagent", obj{"agent_id": id, "message": "again"}, nil).must(t)
	eq(t, []any{o.Text, o.IsError}, []any{`Running nested agent not found or not owned by this parent: "` + id + `".`, true})
}

func TestAPendingSteerIsSentWhenTheChildIsUp(t *testing.T) {
	r := startRig(t)
	gate := make(chan struct{})
	slow := func(ctx context.Context, spec childSpec) (child, error) { <-gate; return r.fleet.start(ctx, spec) }
	r.app.mgr.start = slow
	rec := r.app.mgr.spawnOwned("scout", "d", "", childSpec{Prompt: "p"}, true, &owner{ParentID: "parent-1", Depth: 2, MaxDepth: 3})
	r.app.nested = &nestedConfig{AgentID: "parent-1", Depth: 1, MaxDepth: 3, Allowed: "all"}
	o := nestedExec(r, "steer_subagent", obj{"agent_id": rec.ID, "message": "early"}, nil).must(t)
	eq(t, o.Text, "Steering message queued for nested agent "+rec.ID+".")
	close(gate)
	c := r.fleet.wait(t, 1)[0]
	for i := 0; i < 200; i++ {
		c.mu.Lock()
		n := len(c.steered)
		c.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	eq(t, c.steered, []string{"early"})
	c.mu.Unlock()
}

func TestNestedResultsAndDefaults(t *testing.T) {
	r, _ := scoutRig(t)
	plant(r, &agentRecord{ID: "mine", Status: statusCompleted, Result: "r", ParentAgentID: "parent-1"})
	// A finished child's result is read once it is done, with the plain wording.
	o := nestedExec(r, "get_subagent_result", obj{"agent_id": "mine"}, nil).must(t)
	eq(t, []any{o.Text, o.IsError}, []any{"r", false})
	plant(r, &agentRecord{ID: "empty", Status: statusCompleted, ParentAgentID: "parent-1"})
	eq(t, nestedExec(r, "get_subagent_result", obj{"agent_id": "empty"}, nil).must(t).Text, "No output.")
	plant(r, &agentRecord{ID: "err", Status: statusError, Error: "bad", ParentAgentID: "parent-1"})
	o = nestedExec(r, "get_subagent_result", obj{"agent_id": "err"}, nil).must(t)
	eq(t, []any{o.Text, o.IsError}, []any{"Agent failed: bad", true})
	plant(r, &agentRecord{ID: "stopped", Status: statusStopped, Error: "e", ParentAgentID: "parent-1"})
	eq(t, nestedExec(r, "get_subagent_result", obj{"agent_id": "stopped"}, nil).must(t).Text, "Nested agent (STOPPED BY THE USER before completion — output is partial; the task was NOT finished).\n\ne")
	plant(r, &agentRecord{ID: "running", Status: statusRunning, ParentAgentID: "parent-1"})
	eq(t, nestedExec(r, "get_subagent_result", obj{"agent_id": "running"}, nil).must(t).Text, "Agent running is running.")
}

// The depth is the setting's: top-level agents hand the cap down, and a file's allowance only becomes tools below it.
func TestTheTopLevelSessionGivesItsChildAPlace(t *testing.T) {
	cwd := t.TempDir()
	writeAgentFile(t, cwd, "orchestrator", "allowed_subagents: worker, scout\n")
	writeAgentFile(t, cwd, "wild", "allowed_subagents: all\n")
	writeAgentFile(t, cwd, "plain", "")
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	r := startRigIn(t, cwd)
	spawn := func(typ string, extra obj) childSpec {
		p := obj{"prompt": "p-" + typ, "description": "d", "subagent_type": typ}
		for k, v := range extra {
			p[k] = v
		}
		r.must("Agent", p)
		return r.fleet.waitPrompt(t, "p-"+typ).spec
	}
	s := spawn("orchestrator", nil)
	eq(t, []any{s.Depth, s.MaxDepth, s.Allowed, s.ConfigCwd, s.nestedActive()}, []any{1, 2, []string{"worker", "scout"}, cwd, true})
	eq(t, s.AgentID != "", true)
	s = spawn("wild", nil)
	eq(t, s.Allowed, "all")
	s = spawn("plain", nil)
	eq(t, s.Allowed, nil)
	eq(t, s.nestedActive(), false)
}

func TestTheDepthCapComesFromTheSettingAndStopsTheAllowance(t *testing.T) {
	cwd := t.TempDir()
	writeAgentFile(t, cwd, "orchestrator", "allowed_subagents: all\n")
	writeAgentFile(t, cwd, "locked", "allowed_subagents: all\nisolated: true\n")
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(`{"maxSubagentDepth": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := startRigIn(t, cwd)
	eq(t, getMaxSubagentDepth(), 1)
	r.must("Agent", obj{"prompt": "p1", "description": "d", "subagent_type": "orchestrator"})
	s := r.fleet.waitPrompt(t, "p1").spec
	// Main is 0 and its subagents are 1: with a cap of 1 they may not delegate, and get no tools to try with.
	eq(t, []any{s.Depth, s.MaxDepth, s.nestedActive()}, []any{1, 1, false})
	// The orchestration tools stay out of its scope: at the cap the allowance is not given, so nothing is re-admitted.
	eq(t, hasAll(s.ExcludeTools, nestedToolNames...), true)
	setMaxSubagentDepth(0)
	r.must("Agent", obj{"prompt": "p0", "description": "d", "subagent_type": "orchestrator"})
	eq(t, r.fleet.waitPrompt(t, "p0").spec.nestedActive(), false)
	setMaxSubagentDepth(4)
	r.must("Agent", obj{"prompt": "p4", "description": "d", "subagent_type": "orchestrator", "isolated": true})
	s = r.fleet.waitPrompt(t, "p4").spec
	// Isolated: no extensions at all, so no nested tools whatever the file allows.
	eq(t, []any{s.NoExtensions, s.Allowed, s.nestedActive()}, []any{true, nil, false})
	r.must("Agent", obj{"prompt": "p5", "description": "d", "subagent_type": "locked"})
	s = r.fleet.waitPrompt(t, "p5").spec
	eq(t, []any{s.NoExtensions, s.Allowed, s.nestedActive()}, []any{true, nil, false})
}

func TestMaxSubagentDepthSetting(t *testing.T) {
	write := func(t *testing.T, global, project string) {
		dir := t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", dir)
		cwd := t.TempDir()
		if global != "" {
			_ = os.WriteFile(filepath.Join(dir, "subagents.json"), []byte(global), 0o644)
		}
		if project != "" {
			_ = os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755)
			_ = os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(project), 0o644)
		}
		t.Chdir(cwd)
	}
	cases := []struct {
		name, global, project string
		want                  *int
	}{
		{"unset", "", "", nil},
		{"zero is valid", `{"maxSubagentDepth": 0}`, "", ptrInt(0)},
		{"the ceiling is 16", `{"maxSubagentDepth": 16}`, "", ptrInt(16)},
		{"above the ceiling is dropped", `{"maxSubagentDepth": 17}`, "", nil},
		{"negative is dropped", `{"maxSubagentDepth": -1}`, "", nil},
		{"fractional is dropped", `{"maxSubagentDepth": 2.5}`, "", nil},
		{"a string is dropped", `{"maxSubagentDepth": "3"}`, "", nil},
		{"the project file wins", `{"maxSubagentDepth": 3}`, `{"maxSubagentDepth": 5}`, ptrInt(5)},
		{"a project file without it keeps the global one", `{"maxSubagentDepth": 3}`, `{"maxConcurrent": 2}`, ptrInt(3)},
		{"an invalid project value keeps the global one", `{"maxSubagentDepth": 3}`, `{"maxSubagentDepth": "x"}`, ptrInt(3)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			write(t, c.global, c.project)
			cwd, _ := os.Getwd()
			eq(t, loadSettings(cwd).MaxSubagentDepth, c.want)
		})
	}
}

func ptrInt(n int) *int { return &n }

// Every model-facing string of the nested tools is a string literal of the original's source.
func TestNestedToolTextIsTheOriginals(t *testing.T) {
	// testdata/nested-tools-literals.jsonl: every double-quoted string literal of src/nested-tools.ts, one JSON string per
	// line (the module is tested without the original's tree beside it).
	raw, err := os.ReadFile(filepath.Join("testdata", "nested-tools-literals.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	literals := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var lit string
		if err := json.Unmarshal([]byte(line), &lit); err != nil {
			t.Fatal(err)
		}
		literals[lit] = true
	}
	r, cwd := scoutRig(t)
	defs := r.app.nestedToolDefs(r.app.nested)
	_ = cwd
	var strs []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if d, ok := x["description"].(string); ok && !strings.HasPrefix(d, "Allowed nested agent type.") && d != texts.IsolationParam["description"] { // the isolation text is the top-level tool's own
				strs = append(strs, d)
			}
			for _, c := range x {
				walk(c)
			}
		}
	}
	for _, d := range defs {
		strs = append(strs, d.Label, d.Name)
		walk(map[string]any(d.Parameters))
		for _, part := range strings.SplitAfter(d.Description, ". ") {
			strs = append(strs, strings.TrimSuffix(part, " "))
		}
	}
	eq(t, len(defs), 3)
	for _, text := range strs {
		if !literals[text] && !literals[text+" "] {
			t.Errorf("not a string of nested-tools.ts: %q", text)
		}
	}
	eq(t, defs[0].Parameters["properties"].(map[string]any)["subagent_type"].(map[string]any)["description"], "Allowed nested agent type. Available: general-purpose, Explore, Plan, reviewer, scout.")
}

func TestTheAllowanceNamesAreMatchedCaseInsensitively(t *testing.T) {
	r, _ := scoutRig(t)
	r.app.nested.Allowed = []string{"SCOUT"}
	out := nestedAsync(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p"}, nil)
	r.fleet.wait(t, 1)[0].finish("ok")
	eq(t, recv(t, out).must(t).IsError, false)
	denied := nestedExec(r, "Agent", obj{"subagent_type": "reviewer", "description": "d", "prompt": "p"}, nil).must(t)
	eq(t, denied.Text, `Nested agent type "reviewer" is not allowed for this parent. Allowed: scout.`)
}

// The name an RPC caller gives an agent reaches the record, and so the way it is addressed. upstream: cross-extension-rpc.ts spawn options.
func TestSpawnRPCNameNamesTheAgent(t *testing.T) {
	r := startRig(t)
	reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "p", "options": obj{"description": "d", "name": "auth-audit"}})
	eq(t, reply["success"], true)
	id, _ := reply["data"].(map[string]any)["id"].(string)
	rec := r.app.mgr.get(id)
	eq(t, rec.Name, "auth-audit")
	eq(t, r.app.mgr.resolve("auth-audit") == rec, true)
}

// A nested child is resumed like a top-level one, inline, by the agent that owns it. upstream: nested-tools.ts agentTool (resume).
func TestNestedResumeContinuesAnOwnedChild(t *testing.T) {
	r, _ := scoutRig(t)
	first := nestedAsync(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p1"}, nil)
	c := r.fleet.waitPrompt(t, "p1")
	c.finish("one")
	eq(t, recv(t, first).must(t).Text, "one")
	rec := r.app.mgr.list()[0]
	// Not ours: the owner's check comes first.
	eq(t, nestedExec(r, "Agent", obj{"resume": "nobody", "prompt": "x", "description": "d", "subagent_type": "scout"}, nil).must(t).Text,
		`Nested agent not found or not owned by this parent: "nobody".`)
	resumed := nestedAsync(r, "Agent", obj{"resume": rec.ID, "prompt": "p2", "description": "d", "subagent_type": "scout"}, nil)
	for i := 0; i < 400; i++ {
		c.mu.Lock()
		n := len(c.resumed)
		c.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	eq(t, c.resumed, []string{"p2"})
	c.mu.Unlock()
	// Mid-run it cannot take another prompt: what pi's session.prompt throws.
	busy := nestedExec(r, "Agent", obj{"resume": rec.ID, "prompt": "p3", "description": "d", "subagent_type": "scout"}, nil).must(t)
	eq(t, []any{busy.Text, busy.IsError}, []any{"Agent failed: " + errStillProcessing, true})
	c.finish("two")
	o := recv(t, resumed).must(t)
	eq(t, []any{o.Text, o.IsError}, []any{"two", false})
	eq(t, r.fleet.count(), 1) // the same process took the prompt: no new agent
}

// The nested tool takes `isolation` and `inherit_context` like the top-level one. upstream: nested-tools.ts agentTool.
func TestNestedIsolationAndInheritContext(t *testing.T) {
	r, _ := scoutRig(t)
	// A workspace that is not a git repository fails the call, strictly, with the worktree text.
	o := nestedExec(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p", "isolation": "worktree"}, nil).must(t)
	eq(t, []any{o.Text, o.IsError}, []any{worktreeStartupError, true})
	eq(t, r.fleet.count(), 0)
	// inherit_context puts the conversation in front of the task when the run starts.
	out := nestedAsync(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p2", "inherit_context": true}, nil)
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.InheritContext != nil, true)
	c.finish("x")
	recv(t, out)
	// The project switch drops a worktree a caller asks for, as it does at the top level.
	cwd := t.TempDir()
	writeAgentFile(t, cwd, "scout", "")
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(`{"worktreeIsolation": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	off := startNestedRig(t, cwd, &nestedConfig{AgentID: "parent-1", Depth: 1, MaxDepth: 2, Allowed: "all", ConfigCwd: cwd})
	out = nestedAsync(off, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p3", "isolation": "worktree"}, nil)
	c = off.fleet.wait(t, 1)[0]
	eq(t, c.spec.Worktree, false)
	c.finish("x")
	eq(t, recv(t, out).must(t).IsError, false)
}

// Every transcript of a branch goes under the root session, whoever wrote it. upstream: nested-tools.ts rootSessionId.
func TestNestedTranscriptsAreFiledUnderTheRootSession(t *testing.T) {
	r, _ := scoutRig(t)
	r.app.nested.RootSessionID = "root-session-7"
	out := nestedAsync(r, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p"}, nil)
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.Transcript, true)
	eq(t, c.spec.SessionID, "root-session-7")
	c.finish("x")
	recv(t, out)
	// Without a root session there is nowhere to file one.
	r2, _ := scoutRig(t)
	out = nestedAsync(r2, "Agent", obj{"subagent_type": "scout", "description": "d", "prompt": "p"}, nil)
	c = r2.fleet.wait(t, 1)[0]
	eq(t, c.spec.Transcript, false)
	c.finish("x")
	recv(t, out)
}
