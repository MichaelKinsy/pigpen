package tintinweb_subagents

import (
	"strings"
	"testing"
	"time"
)

func TestPromptAssembly(t *testing.T) {
	plan := defaultAgents().m["Plan"]
	env := envInfo{Platform: "linux"}
	got := buildAgentPrompt(plan, "/w", env, "")
	eq(t, got, "<active_agent name=\"Plan\"/>\n\nYou are a pi coding agent sub-agent.\nYou have been invoked to handle a specific task autonomously.\n\n"+
		"# Environment\nWorking directory: /w\nNot a git repository\nPlatform: linux\n\n"+plan.SystemPrompt)
	git := buildAgentPrompt(plan, "/w", envInfo{IsGitRepo: true, Branch: "main", Platform: "darwin"}, "")
	eq(t, strings.Contains(git, "# Environment\nWorking directory: /w\nGit repository: yes\nBranch: main\nPlatform: darwin\n"), true)
	gp := defaultAgents().m["general-purpose"]
	app := buildAgentPrompt(gp, "/w", env, "PARENT PROMPT")
	eq(t, strings.HasPrefix(app, "PARENT PROMPT\n\n<sub_agent_context>\nYou are operating as a sub-agent invoked to handle a specific task.\n- Use the read tool"), true)
	eq(t, strings.Contains(app, "</sub_agent_context>\n\n<active_agent name=\"general-purpose\"/>\n\n# Environment\nWorking directory: /w\nNot a git repository\nPlatform: linux"), true)
	eq(t, strings.Contains(app, "<agent_instructions>\n"+gp.SystemPrompt+"\n</agent_instructions>"), true)
	// without a parent prompt the generic base is the identity; an agent with no instructions adds no section
	bare := buildAgentPrompt(&agentConfig{Name: "bare", PromptMode: "append"}, "/w", env, "")
	eq(t, strings.HasPrefix(bare, genericBase+"\n\n<sub_agent_context>"), true)
	eq(t, strings.Contains(bare, "agent_instructions"), false)
	eq(t, platformName("windows"), "win32")
	eq(t, platformName("linux"), "linux")
}

func TestDetectEnvAsksGitThroughTheHost(t *testing.T) {
	r := startRig(t)
	var calls []string
	r.exec = func(command string, args []string) (string, int) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if args[0] == "rev-parse" {
			return "true\n", 0
		}
		return "feature/x\n", 0
	}
	ctx, _ := r.app.context()
	env := detectEnv(ctx, "/w")
	eq(t, env, envInfo{IsGitRepo: true, Branch: "feature/x", Platform: platformName(env.Platform)})
	eq(t, calls, []string{"git rev-parse --is-inside-work-tree", "git branch --show-current"})
	// a failing branch lookup reads "unknown"; a failing rev-parse means no repository, and no second call
	r.exec = func(command string, args []string) (string, int) {
		if args[0] == "rev-parse" {
			return "true\n", 0
		}
		return "", 1
	}
	eq(t, detectEnv(ctx, "/w").Branch, "unknown")
	n := 0
	r.exec = func(string, []string) (string, int) { n++; return "", 128 }
	eq(t, detectEnv(ctx, "/w").IsGitRepo, false)
	eq(t, n, 1)
}

func TestTheChildGetsTheAgentsPromptAndEnvironment(t *testing.T) {
	r := startRig(t)
	r.exec = func(c string, args []string) (string, int) {
		if args[0] == "rev-parse" {
			return "true\n", 0
		}
		return "main\n", 0
	}
	r.must("Agent", obj{"prompt": "plan it", "description": "d", "subagent_type": "Plan"})
	spec := r.fleet.wait(t, 1)[0].spec
	eq(t, strings.HasPrefix(spec.SystemPrompt, "<active_agent name=\"Plan\"/>\n\nYou are a pi coding agent sub-agent."), true)
	eq(t, strings.Contains(spec.SystemPrompt, "Git repository: yes\nBranch: main\n"), true)
	eq(t, spec.PromptMode, "replace")
	eq(t, spec.Prompt, "plan it")
}

func TestIsolationFollowsTheAgentFile(t *testing.T) {
	r := startRig(t)
	tr := true
	registerAgents(reg(mk("solo", func(c *agentConfig) { c.Isolated = &tr; c.Extensions = true }), mk("noext", func(c *agentConfig) { c.Extensions = false }),
		mk("open", func(c *agentConfig) { c.Extensions = true })))
	for i, name := range []string{"solo", "noext", "open"} {
		r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": name, "run_in_background": true})
		eq(t, r.fleet.wait(t, i+1)[i].spec.Isolated, name != "open")
	}
	eq(t, childArgs(childSpec{Isolated: true})[3], "--no-extensions")
}

func TestQueueIsFirstInFirstOut(t *testing.T) {
	r := startRig(t)
	r.app.mgr.maxConcurrent = 1
	r.app.mgr.spawn("Plan", "1", "", childSpec{Prompt: "1"}, true)
	r.app.mgr.spawn("Plan", "2", "", childSpec{Prompt: "2"}, true)
	r.app.mgr.spawn("Plan", "3", "", childSpec{Prompt: "3"}, true)
	r.fleet.wait(t, 1)[0].finish("a")
	cs := r.fleet.wait(t, 2)
	eq(t, cs[1].spec.Prompt, "2")
	cs[1].finish("b")
	eq(t, r.fleet.wait(t, 3)[2].spec.Prompt, "3")
}

func TestAbortedQueuedAgentLeavesTheQueue(t *testing.T) {
	r := startRig(t)
	r.app.mgr.maxConcurrent = 1
	r.app.mgr.spawn("Plan", "1", "", childSpec{Prompt: "1"}, true)
	q := r.app.mgr.spawn("Plan", "2", "", childSpec{Prompt: "2"}, true)
	r.fleet.wait(t, 1)
	r.app.mgr.abort(q)
	r.app.mgr.mu.Lock()
	eq(t, len(r.app.mgr.queue), 0)
	r.app.mgr.mu.Unlock()
}

func TestManagerSteerRefusesAFinishedAgent(t *testing.T) {
	r := startRig(t)
	a := r.app.mgr.spawn("Plan", "1", "", childSpec{}, true)
	c := r.fleet.wait(t, 1)[0]
	c.finish("x")
	<-a.done
	err := r.app.mgr.steer(a, "late")
	eq(t, err != nil && strings.Contains(err.Error(), "is not running (status: completed)"), true)
	eq(t, c.steered, []string(nil))
}

func TestAmbiguousReferencesResolveToNothing(t *testing.T) {
	m := newManager(nil, nil)
	for _, id := range []string{"abc1", "abc2", "xyz9"} {
		m.records[id] = &agentRecord{ID: id}
		m.order = append(m.order, id)
	}
	eq(t, m.resolve("abc") == nil, true)
	eq(t, m.resolve("abc1").ID, "abc1")
	eq(t, m.resolve("xy").ID, "xyz9")
	eq(t, m.resolve("") == nil, true)
}

func TestResultAndSteerAcceptAnAgentsName(t *testing.T) {
	r := startRig(t)
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "name": "scout"}))
	c := r.fleet.wait(t, 1)[0]
	c.finish("found it")
	r.waitEmitted("subagents:completed")
	out := r.must("get_subagent_result", obj{"agent_id": "scout"})
	eq(t, strings.HasPrefix(out, "Agent: "+id+"\n"), true)
	out = r.must("get_subagent_result", obj{"agent_id": "plan"}) // by type handle
	eq(t, strings.HasPrefix(out, "Agent: "+id+"\n"), true)
}

func TestFormatting(t *testing.T) {
	eq(t, formatMs(999), "1.0s") // upstream: ui/agent-widget.ts formatMs, (ms / 1000).toFixed(1)
	eq(t, formatMs(40), "0.0s")
	eq(t, formatTokens(2_500_000), "2.5M token")
	eq(t, formatMs(1000), "1.0s")
	eq(t, formatMs(1234), "1.2s")
	eq(t, formatTokens(0), "")
	eq(t, formatTokens(999), "999 token")
	eq(t, formatTokens(1500), "1.5k token")
	eq(t, formatTokens(1234), "1.2k token")
}

func TestFallbackNoneIsCaseAndSpaceInsensitive(t *testing.T) {
	resetTypes()
	t.Cleanup(resetTypes)
	registerAgents(newRegistry())
	setFallbackSubagent(ptr("  NoNe "))
	res := resolveSpawnType("typoo")
	eq(t, res.OK, false)
	eq(t, strings.Contains(res.Message, "fallbackSubagent"), false) // refused as "none", not as an unknown agent name
}

func TestOutputTranscriptIsTrueUnlessFalse(t *testing.T) {
	r := newCARig(t)
	r.writeAgent("a", "---\noutput_transcript: maybe\n---\nx")
	r.writeAgent("b", "---\noutput_transcript: false\n---\nx")
	eq(t, *r.agent("a").OutputTranscript, true)
	eq(t, *r.agent("b").OutputTranscript, false)
}

func TestDescriptionDefaultsToTheDeclaredName(t *testing.T) {
	r := newCARig(t)
	r.writeAgent("file", "---\nname: declared\n---\nx")
	eq(t, r.agent("declared").Description, "declared")
}

func TestSettingsTrimTheFallbackName(t *testing.T) {
	eq(t, *sanitizeSettings(map[string]any{"fallbackSubagent": "  router "}).FallbackSubagent, "router")
	_ = time.Second
}
