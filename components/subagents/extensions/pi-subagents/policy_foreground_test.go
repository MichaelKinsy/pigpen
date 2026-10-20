package pi_subagents

import (
	"encoding/json"
	"strings"
	"testing"
)

func fgProgress(index int, agent string, tokens int) *agentProgress {
	i := func(n int) *int { return &n }
	return &agentProgress{
		Index: index, Agent: agent, SessionName: "  " + agent + ": named task  ", Status: "running", Task: agent + " task",
		ToolCount: i(index + 1), Tokens: i(tokens), Model: "openai/gpt-5.6-terra:high", Thinking: "high",
		InputTokens: i(tokens - 20), OutputTokens: i(20), Window: i(75), WindowPeak: i(90),
	}
}

func TestForegroundChildControl(t *testing.T) {
	tw(t, "foreground-control", "tracks concurrent children independently and promotes the latest active child", func(t *testing.T) {
		control := &foregroundRunControl{RunID: "parallel-run", Mode: "parallel", StartedAt: 1, UpdatedAt: 1, ActiveChildren: map[int]*foregroundChildControl{}}
		firstInterrupts, secondInterrupts, firstDetaches := 0, 0, 0
		beginForegroundChild(control, beginForegroundChildInput{
			Index: 0, Agent: "reviewer", AuthoredTask: "review", EffectivePrompt: "review", Description: "Review correctness",
			Interrupt: func() bool { firstInterrupts++; return true },
			Detach:    func() bool { firstDetaches++; return true },
		})
		beginForegroundChild(control, beginForegroundChildInput{
			Index: 1, Agent: "reviewer", AuthoredTask: "review next", EffectivePrompt: "review next", Description: "Review quality",
			Rerun:     &promptAuditRerun{Params: map[string]any{"agent": "reviewer", "task": "review next"}},
			Interrupt: func() bool { secondInterrupts++; return true },
		})

		equal(t, len(control.ActiveChildren), 2, "active children")
		equal(t, getLivePromptAudit(control, 1).AuthoredTask, "review next", "live prompt")
		equal(t, getLivePromptAudit(control, 1).Rerun.Params, map[string]any{"agent": "reviewer", "task": "review next"}, "rerun params")
		serialized, _ := json.Marshal(control)
		if strings.Contains(string(serialized), "review next") {
			t.Errorf("the control must not carry the prompts: %s", serialized)
		}
		equal(t, *control.CurrentIndex, 1, "current index")
		updateForegroundChild(control, 0, fgProgress(0, "reviewer", 120))
		equal(t, *control.CurrentIndex, 0, "current index after the update")
		equal(t, control.SessionName, "  reviewer: named task  ", "session name")
		equal(t, control.ActiveChildren[0].SessionName, "  reviewer: named task  ", "child session name")
		equal(t, *control.Tokens, 120, "tokens")
		equal(t, *control.InputTokens, 100, "input tokens")
		equal(t, *control.OutputTokens, 20, "output tokens")
		equal(t, *control.Window, 75, "window")
		equal(t, *control.WindowPeak, 90, "window peak")
		equal(t, control.Model, "openai/gpt-5.6-terra:high", "model")
		equal(t, control.Thinking, "high", "thinking")
		equal(t, control.ActiveChildren[1].Tokens == nil, true, "the other child has no tokens")
		equal(t, control.Interrupt(), true, "interrupt")
		equal(t, firstInterrupts, 1, "first interrupts")
		equal(t, secondInterrupts, 0, "second interrupts")
		equal(t, control.Detach(), true, "detach")
		equal(t, firstDetaches, 1, "first detaches")

		updateForegroundChild(control, 1, fgProgress(1, "reviewer", 240))
		finishForegroundChild(control, 1)
		equal(t, getLivePromptAudit(control, 1) == nil, true, "live prompt removed")
		equal(t, *control.CurrentIndex, 0, "current index kept")
		equal(t, *control.Tokens, 120, "tokens kept")
		equal(t, len(control.ActiveChildren), 1, "one child left")
		equal(t, control.ActiveChildren[0] != nil, true, "child 0 left")

		finishForegroundChild(control, 0)
		equal(t, len(control.ActiveChildren), 0, "no children")
		equal(t, control.CurrentIndex == nil, true, "no current index")
		equal(t, control.SessionName, "", "session name")
		equal(t, control.Model, "", "model")
		equal(t, control.InputTokens == nil, true, "input tokens")
		equal(t, control.OutputTokens == nil, true, "output tokens")
		equal(t, control.Window == nil, true, "window")
		equal(t, control.WindowPeak == nil, true, "window peak")
		equal(t, control.Interrupt == nil, true, "interrupt")
		equal(t, control.Detach == nil, true, "detach")
	})

	tw(t, "foreground-control", "settles scheduling only after every owner releases", func(t *testing.T) {
		one := 1
		control := &foregroundRunControl{RunID: "owned-run", Mode: "parallel", StartedAt: 1, UpdatedAt: 1, SchedulingOwners: &one}
		retainForegroundSchedulingOwner(control)
		settleForegroundSchedulingOwner(control)
		equal(t, foregroundSchedulingSettled(control), false, "settled with an owner left")
		settleForegroundSchedulingOwner(control)
		equal(t, foregroundSchedulingSettled(control), true, "settled")
		equal(t, *control.SchedulingOwners, 0, "owners")
	})
}

func TestLivePromptAuditRuntimeAdditions(t *testing.T) {
	c := &foregroundRunControl{RunID: "r"}
	registerLivePromptAudit(c, 0, "do it", "Preamble\n\ndo it\n\nEpilogue", livePromptMetadata{Cwd: "/w", OutputPath: "/o"})
	p := getLivePromptAudit(c, 0)
	equal(t, p.RuntimeAdditions, "Preamble\n\nEpilogue", "before and after")
	equal(t, p.Cwd+p.OutputPath, "/w/o", "metadata")
	updateLiveEffectivePrompt(c, 0, "do it")
	equal(t, getLivePromptAudit(c, 0).RuntimeAdditions, "(none)", "no additions")
	updateLiveEffectivePrompt(c, 0, "something else")
	equal(t, getLivePromptAudit(c, 0).RuntimeAdditions, "(runtime additions unavailable)", "task missing")
	registerLivePromptAudit(c, 1, "", "whole prompt", livePromptMetadata{})
	equal(t, getLivePromptAudit(c, 1).RuntimeAdditions, "whole prompt", "empty task")
	removeLivePromptAudit(c, 0)
	removeLivePromptAudit(c, 1)
	livePromptsMu.Lock()
	_, leaked := livePrompts[c]
	livePromptsMu.Unlock()
	equal(t, leaked, false, "the registry entry goes with its last prompt")
}
