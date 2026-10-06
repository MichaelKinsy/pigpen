package tintinweb_subagents

import (
	"strings"
	"testing"
	"time"
)

func TestToolSurface(t *testing.T) {
	r := startRig(t)
	for _, n := range []string{"Agent", "get_subagent_result", "steer_subagent"} {
		eq(t, r.tools[n], true)
	}
	eq(t, r.cmds["agents"], true)
	// Nothing is started or announced by a plain session start except the ready event.
	r.waitEmitted("subagents:ready")
	eq(t, r.fleet.count(), 0)
}

func TestAgentBackgroundLifecycle(t *testing.T) {
	r := startRig(t)
	text := r.must("Agent", obj{"prompt": "find the bug", "description": "bug hunt", "subagent_type": "Explore"})
	for _, want := range []string{"Agent started in background.", "Type: Explore", "Description: bug hunt", "You will be notified when this agent completes."} {
		eq(t, strings.Contains(text, want), true)
	}
	id := idFrom(t, text)
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.Prompt, "find the bug")
	eq(t, c.spec.PromptMode, "replace")
	eq(t, c.spec.Tools, []string{"read", "bash", "grep", "find", "ls"})
	eq(t, c.spec.Model, "") // Explore's haiku default is never forced on the child
	// While it runs, the result tool says so.
	out := r.must("get_subagent_result", obj{"agent_id": id})
	eq(t, strings.Contains(out, "Agent is still running. Use wait: true or check back later."), true)
	c.finish("the bug is in main.go")
	r.waitEmitted("subagents:completed")
	// After the nudge hold the model is notified, with the XML block.
	deadline := time.Now().Add(3 * time.Second)
	for len(r.messages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	msgs := r.messages()
	eq(t, len(msgs), 1)
	m := msgs[0]["message"].(map[string]any)
	eq(t, m["customType"], "subagent-notification")
	content := m["content"].(string)
	for _, want := range []string{"<task-notification>", "<task-id>" + id + "</task-id>", "<status>Done</status>", `Agent "bug hunt" completed`, "<result>the bug is in main.go</result>", "<total_tokens>1500</total_tokens>", "<tool_uses>3</tool_uses>"} {
		eq(t, strings.Contains(content, want), true)
	}
	eq(t, msgs[0]["options"].(map[string]any)["deliverAs"], "followUp")
	// The full result is retrievable, with its stats.
	full := r.must("get_subagent_result", obj{"agent_id": id})
	eq(t, strings.HasPrefix(full, "Agent: "+id+"\nType: Explore | Status: completed | Tool uses: 3 | 1.5k token | Duration: "), true)
	eq(t, strings.HasSuffix(full, "\n\nthe bug is in main.go"), true)
}

func TestConsumedResultSkipsTheNotification(t *testing.T) {
	r := startRig(t)
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "general-purpose"}))
	c := r.fleet.wait(t, 1)[0]
	c.finish("done")
	out := r.must("get_subagent_result", obj{"agent_id": id, "wait": true}) // consumes before the hold ends
	eq(t, strings.HasSuffix(out, "done"), true)
	time.Sleep(nudgeHold + 300*time.Millisecond)
	eq(t, len(r.messages()), 0)
}

func TestAgentForeground(t *testing.T) {
	r := startRig(t)
	done := make(chan string, 1)
	go func() {
		done <- r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "run_in_background": false})
	}()
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.PromptMode, "replace")
	c.finish("plan text")
	out := <-done
	eq(t, strings.HasPrefix(out, "Agent completed in "), true)
	eq(t, strings.Contains(out, "(3 tool uses, 1.5k token)"), true)
	eq(t, strings.HasSuffix(out, ".\n\nplan text"), true)
	time.Sleep(nudgeHold + 100*time.Millisecond)
	eq(t, len(r.messages()), 0) // a foreground agent is never announced
}

func TestAgentForegroundFailure(t *testing.T) {
	r := startRig(t)
	r.fleet.setFail(errBoom)
	out := r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "run_in_background": false})
	eq(t, out, "Agent failed: boom")
}

func TestUnknownTypeFallsBackAndSaysSo(t *testing.T) {
	r := startRig(t)
	out := r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "typoo"})
	// upstream: index.ts:1796-1798; fallback-subagent-wiring.test.ts:142.
	eq(t, strings.HasPrefix(out, "Note: Unknown agent type \"typoo\" — using general-purpose.\n\n"), true)
	eq(t, strings.Contains(out, "Type: Agent"), true)
	setFallbackSubagent(ptr(noFallback))
	out = r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "typoo"})
	eq(t, strings.HasPrefix(out, `Unknown or disabled agent type: "typoo". Available: `), true)
	eq(t, r.fleet.count() <= 1, true)
}

func TestSteer(t *testing.T) {
	r := startRig(t)
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "general-purpose", "name": "scout"}))
	c := r.fleet.wait(t, 1)[0]
	// The name addresses the agent too.
	out := r.must("steer_subagent", obj{"agent_id": "scout", "message": "look at x.go"})
	eq(t, strings.HasPrefix(out, "Steering message sent to agent "+id+". The agent will process it after its current tool execution.\nCurrent state: "), true)
	eq(t, c.steered, []string{"look at x.go"})
	r.waitEmitted("subagents:steered")
	c.finish("ok")
	r.waitEmitted("subagents:completed")
	out = r.must("steer_subagent", obj{"agent_id": id, "message": "late"})
	eq(t, out, `Agent "`+id+`" is not running (status: completed). Cannot steer a non-running agent.`)
	eq(t, r.must("steer_subagent", obj{"agent_id": "nope", "message": "x"}), `Agent not found: "nope". It may have been cleaned up.`)
	eq(t, r.must("get_subagent_result", obj{"agent_id": "nope"}), `Agent not found: "nope". It may have been cleaned up.`)
}

func TestAgentsCommandListsTypesAndAgents(t *testing.T) {
	r := startRig(t)
	r.must("Agent", obj{"prompt": "p", "description": "scan", "subagent_type": "Explore"})
	r.fleet.wait(t, 1)
	r.Command("agents", "")
	var msg string
	for _, c := range r.CallsTo("ui.notify") {
		msg, _ = c.Args["message"].(string)
	}
	for _, want := range []string{"Agent types:", "general-purpose [default]", "Explore [default]", "Plan [default]", "Agents:", "scan"} {
		eq(t, strings.Contains(msg, want), true)
	}
}
