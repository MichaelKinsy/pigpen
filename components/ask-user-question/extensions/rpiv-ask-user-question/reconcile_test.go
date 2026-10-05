package ask_user_question_test

import (
	"testing"
)

const fReconcile = "reconcile"

// fire runs before_agent_start on the rig and returns once the handler has returned.
func (r *rig) beforeAgentStart() {
	r.Fire("before_agent_start", map[string]any{"prompt": "x", "systemPrompt": "", "systemPromptOptions": map[string]any{}})
}

func TestReconcile(t *testing.T) {
	const tool = "ask_user_question"
	print := HostOptions{Mode: "print"}
	tw(t, fReconcile, "strips ask_user_question when !hasUI and the tool is active", func(t *testing.T) {
		r := startRig(t, print)
		r.setActive(tool)
		r.beforeAgentStart()
		eq(t, r.activeNow(), []string{}, "active")
	})
	tw(t, fReconcile, "no-ops when !hasUI and the tool is already absent", func(t *testing.T) {
		r := startRig(t, print)
		r.setActive("other")
		r.beforeAgentStart()
		eq(t, len(r.toolSets()), 0, "setActiveTools calls")
	})
	tw(t, fReconcile, "restores ask_user_question when hasUI and the tool is absent", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "tui"})
		r.setActive()
		r.beforeAgentStart()
		eq(t, r.activeNow(), []string{tool}, "active")
	})
	tw(t, fReconcile, "no-ops when hasUI and the tool is already active", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "tui"})
		r.setActive(tool)
		r.beforeAgentStart()
		eq(t, len(r.toolSets()), 0, "setActiveTools calls")
	})
	tw(t, fReconcile, "does not clobber sibling tools on strip — ['ask_user_question','other'] + !hasUI → ['other']", func(t *testing.T) {
		r := startRig(t, print)
		r.setActive(tool, "other")
		r.beforeAgentStart()
		eq(t, r.activeNow(), []string{"other"}, "active")
	})
	tw(t, fReconcile, "does not clobber sibling tools on restore — ['other'] + hasUI → ['other','ask_user_question']", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "tui"})
		r.setActive("other")
		r.beforeAgentStart()
		eq(t, r.activeNow(), []string{"other", tool}, "active")
	})
	tw(t, fReconcile, "strip→restore round-trips back to present", func(t *testing.T) {
		off := startRig(t, print)
		off.setActive(tool, "other")
		off.beforeAgentStart()
		on := startRig(t, HostOptions{Mode: "tui"})
		on.setActive(off.activeNow()...)
		on.beforeAgentStart()
		eq(t, on.activeNow(), []string{"other", tool}, "active")
	})
	tw(t, fReconcile, "is idempotent — two stripped-mode calls make one setActiveTools and leave the set stable", func(t *testing.T) {
		r := startRig(t, print)
		r.setActive(tool, "other")
		r.beforeAgentStart()
		r.beforeAgentStart()
		eq(t, len(r.toolSets()), 1, "setActiveTools calls")
		eq(t, r.activeNow(), []string{"other"}, "active")
	})
	tw(t, fReconcile, "is idempotent — two restored-mode calls (already correct) make zero setActiveTools and leave the set stable", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "tui"})
		r.setActive(tool, "other")
		r.beforeAgentStart()
		r.beforeAgentStart()
		eq(t, len(r.toolSets()), 0, "setActiveTools calls")
	})
	tw(t, fReconcile, "keeps ask_user_question active in RPC mode (dialog-walker fallback renders it)", func(t *testing.T) {
		r := startRig(t, rpcOpts())
		r.setActive(tool)
		r.beforeAgentStart()
		eq(t, len(r.toolSets()), 0, "setActiveTools calls")
		eq(t, r.activeNow(), []string{tool}, "active")
	})
	tw(t, fReconcile, "restores ask_user_question in RPC mode when the tool is absent", func(t *testing.T) {
		r := startRig(t, rpcOpts())
		r.setActive("other")
		r.beforeAgentStart()
		eq(t, r.activeNow(), []string{"other", tool}, "active")
	})
	tw(t, fReconcile, "restores ask_user_question in TUI mode (mode: 'interactive' + hasUI)", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "tui"})
		r.setActive()
		r.beforeAgentStart()
		eq(t, r.activeNow(), []string{tool}, "active")
	})
	tw(t, fReconcile, "registers exactly one before_agent_start handler", func(t *testing.T) {
		r := startRig(t, rpcOpts())
		eq(t, r.Registered("before_agent_start"), true, "registered")
	})
	tw(t, fReconcile, "invoking the handler with !hasUI strips the tool", func(t *testing.T) {
		r := startRig(t, print)
		r.setActive(tool)
		r.beforeAgentStart()
		eq(t, r.activeNow(), []string{}, "active")
	})
	tw(t, fReconcile, "invoking the handler with hasUI restores the tool", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "tui"})
		r.setActive()
		r.beforeAgentStart()
		eq(t, r.activeNow(), []string{tool}, "active")
	})
	tw(t, fReconcile, "wires both registerAskUserQuestionTool and registerAskUserQuestionReconciler", func(t *testing.T) {
		r := startRig(t, rpcOpts())
		eq(t, r.Registered("before_agent_start"), true, "reconciler")
		eq(t, r.ToolDef("ask_user_question").Description != "", true, "tool")
	})
}
