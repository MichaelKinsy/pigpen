package tintinweb_subagents

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// Cases the original's tests do not carry: this port's own behavior (its child process, handles, the notification
// text) and branches a mutation of it showed unchecked (port/mutations.json).

func TestTypeListText(t *testing.T) {
	resetTypes()
	t.Cleanup(resetTypes)
	two := 2
	f, tr := false, true
	registerAgents(reg(
		mk("scout", func(c *agentConfig) {
			c.Description = "Scouts"
			c.BuiltinToolNames = nil
			c.Model = "anthropic/claude-haiku-4-5-20251001"
		}),
		mk("narrow", func(c *agentConfig) {
			c.Description = "Narrow"
			c.BuiltinToolNames = []string{"read", "grep"}
			c.MaxTurns = &two
		}),
		mk("full", func(c *agentConfig) {
			c.Description = "Full"
			c.BuiltinToolNames = []string{"ls", "find", "grep", "write", "edit", "bash", "read"}
		}),
		mk("none-at-all", func(c *agentConfig) { c.Description = "Nothing"; c.BuiltinToolNames = []string{}; c.Extensions = false }),
		mk("ext-only", func(c *agentConfig) { c.Description = "Ext"; c.BuiltinToolNames = []string{}; c.Extensions = true }),
		mk("isolated-empty", func(c *agentConfig) { c.Description = "Iso"; c.BuiltinToolNames = []string{}; c.Isolated = &tr }),
		mk("off", func(c *agentConfig) { c.Description = "Off"; c.Enabled = f }),
	))
	got := buildTypeListText(currentRegistry())
	for _, want := range []string{
		"- scout: Scouts (claude-haiku-4-5) (Tools: *)",
		"- narrow: Narrow (Tools: read, grep)",
		"- full: Full (Tools: *)",
		"- none-at-all: Nothing (Tools: none)",
		"- ext-only: Ext (Tools: no built-ins, extension tools only)",
		"- isolated-empty: Iso (Tools: none)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q not in\n%s", want, got)
		}
	}
	eq(t, strings.Contains(got, "- off:"), false)
	eq(t, modelLabel("claude-sonnet-4-6"), "claude-sonnet-4-6")
	eq(t, modelLabel("p/claude-haiku-4-5-20251001"), "claude-haiku-4-5")
	eq(t, modelLabel("p/m-1234567"), "m-1234567")
}

func TestDescriptionNamesTheTypesAndTheAgentDir(t *testing.T) {
	resetTypes()
	t.Cleanup(resetTypes)
	t.Setenv("PIG_CODING_AGENT_DIR", "/the/agent")
	d := agentToolDescription(currentRegistry())
	eq(t, strings.Contains(d, "- Plan: Software architect agent"), true)
	eq(t, strings.Contains(d, "/the/agent/agents/<name>.md (global)"), true)
	eq(t, strings.Contains(d, "{{"), false)
	specs := loadToolSpecs()
	s := agentToolSchema(currentRegistry(), specs["Agent"])
	props := s["properties"].(map[string]any)
	eq(t, props["subagent_type"].(map[string]any)["description"],
		"The type of specialized agent to use. Available types: general-purpose, Explore, Plan. Custom agents from .pi/agents/*.md (project) or /the/agent/agents/*.md (global) are also available.")
}

func TestHandles(t *testing.T) {
	r := startRig(t)
	a := r.app.mgr.spawn("Explore", "a", "", childSpec{}, true)
	b := r.app.mgr.spawn("Explore", "b", "auth-audit", childSpec{}, true)
	c := r.app.mgr.spawn("Explore", "c", "auth-audit", childSpec{}, true)
	eq(t, [3]string{a.Handle, b.Handle, c.Handle}, [3]string{"explore", "explore-2", "explore-3"})
	eq(t, [2]string{b.Alias, c.Alias}, [2]string{"auth-audit", "auth-audit-2"})
	eq(t, r.app.mgr.resolve("explore"), a)
	eq(t, r.app.mgr.resolve("explore-2"), b)
	eq(t, r.app.mgr.resolve("auth-audit-2"), c)
	eq(t, handleBase("My Agent!"), "my-agent")
	eq(t, handleBase("!!!"), "agent")
	eq(t, handleBase(strings.Repeat("a", 40)), strings.Repeat("a", 32))
	eq(t, handleBase(strings.Repeat("a", 31)+"-b"), strings.Repeat("a", 31))
}

func TestWaitBlocksUntilTheAgentFinishes(t *testing.T) {
	r := startRig(t)
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"}))
	c := r.fleet.wait(t, 1)[0]
	done := make(chan string, 1)
	go func() { done <- r.must("get_subagent_result", obj{"agent_id": id, "wait": true}) }()
	select {
	case out := <-done:
		t.Fatalf("returned before the agent finished: %s", out)
	case <-time.After(150 * time.Millisecond):
	}
	c.finish("late answer")
	out := <-done
	eq(t, strings.HasSuffix(out, "late answer"), true)
	eq(t, regexp.MustCompile(`Duration: \d+\.\ds\n`).MatchString(out), true)
}

func TestFailedAgentsAndStoppedAgentsUseTheFailedChannel(t *testing.T) {
	r := startRig(t)
	got := make(chan obj, 4)
	r.onBus("subagents:failed", func(d any) { got <- d.(map[string]any) })
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"}))
	r.fleet.wait(t, 1)
	r.app.mgr.abort(r.app.mgr.get(id))
	d := <-got
	eq(t, d["status"], statusStopped)
	eq(t, d["id"], id)
	eq(t, r.emitted("subagents:completed"), false)
	out := r.must("get_subagent_result", obj{"agent_id": id, "wait": true})
	// upstream: status-note-wiring.test.ts:202 (background user-stop flags STOPPED BY THE USER).
	eq(t, strings.Contains(out, "Status: stopped (STOPPED BY THE USER before completion — output is partial; the task was NOT finished) |"), true)
	eq(t, strings.Contains(out, "Done"), false)
}

func TestNotificationText(t *testing.T) {
	start := time.Now()
	rec := &agentRecord{ID: "abc", Description: `say "hi" <now>`, Status: statusCompleted, Result: "a < b & c > d", ToolUses: 2, Tokens: 5,
		StartedAt: start, CompletedAt: start.Add(1500 * time.Millisecond)}
	n := formatTaskNotification(rec, 500)
	// upstream: index.ts formatTaskNotification, getStatusLabel; xml.ts escapes &, < and > only.
	eq(t, n, "<task-notification>\n<task-id>abc</task-id>\n<status>Done</status>\n"+
		"<summary>Agent \"say \"hi\" &lt;now&gt;\" completed</summary>\n"+
		"<result>a &lt; b &amp; c &gt; d</result>\n"+
		"<usage><total_tokens>5</total_tokens><tool_uses>2</tool_uses><duration_ms>1500</duration_ms></usage>\n</task-notification>")
	rec.Result = strings.Repeat("x", 600)
	n = formatTaskNotification(rec, 500)
	eq(t, strings.Contains(n, strings.Repeat("x", 500)+"\n...(truncated, use get_subagent_result for full output)</result>"), true)
	eq(t, strings.Contains(n, strings.Repeat("x", 501)), false)
	rec.Result = ""
	eq(t, strings.Contains(formatTaskNotification(rec, 500), "<result>No output.</result>"), true)
	rec.Status, rec.Error, rec.Result = statusError, "boom", ""
	eq(t, strings.Contains(formatTaskNotification(rec, 500), "<status>Error: boom</status>"), true)
	rec.Error = ""
	eq(t, strings.Contains(formatTaskNotification(rec, 500), "<status>Error: unknown</status>"), true)
	rec.Status = statusSteered
	eq(t, strings.Contains(formatTaskNotification(rec, 500), "<status>Wrapped up (turn limit)</status>\n<summary>Agent \"say \"hi\" &lt;now&gt;\" steered (wrapped up at the turn limit — output may be partial)</summary>"), true)
	rec.Status = statusAborted
	eq(t, strings.Contains(formatTaskNotification(rec, 500), "<status>Aborted (max turns exceeded)</status>\n<summary>Agent \"say \"hi\" &lt;now&gt;\" aborted (aborted — hit the turn limit before completion; output may be incomplete)</summary>"), true)
	rec.Status = statusStopped
	eq(t, strings.Contains(formatTaskNotification(rec, 500), "<status>Stopped</status>\n<summary>Agent \"say \"hi\" &lt;now&gt;\" stopped (STOPPED BY THE USER before completion — output is partial; the task was NOT finished)</summary>"), true)
	rec.ToolCallID = "call<1>"
	eq(t, strings.Contains(formatTaskNotification(rec, 500), "<task-id>abc</task-id>\n<tool-use-id>call&lt;1&gt;</tool-use-id>\n<status>"), true)
}

func TestNotificationIsAFollowUpThatStartsATurn(t *testing.T) {
	r := startRig(t)
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"})
	r.fleet.wait(t, 1)[0].finish("x")
	for i := 0; i < 300 && len(r.messages()) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	opts := r.messages()[0]["options"].(map[string]any)
	eq(t, opts["triggerTurn"], true)
	eq(t, opts["deliverAs"], "followUp")
}

func TestUnsupportedAndEmulatedParameters(t *testing.T) {
	r := startRig(t)
	out := r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "inherit_context": true})
	eq(t, strings.HasPrefix(out, "inherit_context is not supported by this port"), true)
	eq(t, r.fleet.count(), 0)
	// resume: an unknown id, a running agent, and a finished one (its answer goes into the new prompt)
	eq(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "resume": "nope"}), `Agent not found: "nope". It may have been cleaned up.`)
	id := idFrom(t, r.must("Agent", obj{"prompt": "first", "description": "d", "subagent_type": "Plan"}))
	c := r.fleet.wait(t, 1)[0]
	out = r.must("Agent", obj{"prompt": "more", "description": "d", "subagent_type": "Plan", "resume": id})
	eq(t, strings.HasPrefix(out, "Agent "+id+" is still running; use steer_subagent"), true)
	c.finish("the first answer")
	r.waitEmitted("subagents:completed")
	r.must("Agent", obj{"prompt": "more", "description": "d", "subagent_type": "Plan", "resume": id})
	c2 := r.fleet.wait(t, 2)[1]
	eq(t, c2.spec.Prompt, "You are continuing earlier work. Your previous run ended with this answer:\n\nthe first answer\n\nContinue with this: more")
	// isolated reaches the child's command line
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "isolated": true})
	eq(t, r.fleet.wait(t, 3)[2].spec.Isolated, true)
	eq(t, c2.spec.Isolated, false)
}

func TestProcessCrashDuringStartupIsReportedByTheTool(t *testing.T) {
	r := startRig(t)
	r.fleet.setFail(errBoom)
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"}))
	<-r.app.mgr.get(id).done
	out := r.must("get_subagent_result", obj{"agent_id": id})
	eq(t, strings.Contains(out, "Status: error | "), true)
	eq(t, strings.HasSuffix(out, "Error: boom"), true)
}

// abort reads the agent's cancel func under the lock while run is still setting it: stopping an agent the moment it
// is spawned (the session ending) was a data race, found by `go test -race -count=24` (about one run in four).
func TestAbortRightAfterSpawnIsRaceFree(t *testing.T) {
	r := startRig(t)
	for i := 0; i < 50; i++ {
		rec := r.app.mgr.spawn("Explore", "d", "", childSpec{}, true)
		r.app.mgr.abort(rec)
	}
	r.app.mgr.abortAll()
}
