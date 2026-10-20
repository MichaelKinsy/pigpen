package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Twins of test/cost-display.test.ts: what `showCost` puts on the foreground result, get_subagent_result and the
// background completion notification the model reads. (The notification renderer group is in ui_misc_twin_test.go.)

const costDisplayCost = 0.0123

func costRig(t *testing.T, showCost bool) *rig {
	t.Helper()
	cwd := t.TempDir()
	body := `{"showCost": false}`
	if showCost {
		body = `{"showCost": true}`
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectSettings(t, cwd, body)
	return startRigIn(t, cwd)
}

// runSpending finishes the next child after one assistant message that spent `cost` on 1000 input and 200 output
// tokens (upstream: runSpending, `opts.onAssistantUsage` then a "done" result).
func runSpending(r *rig, cost float64) {
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for r.fleet.count() == 0 && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		r.fleet.mu.Lock()
		c := r.fleet.children[0]
		r.fleet.mu.Unlock()
		c.spec.Observer.observe(obj{"type": "message_end", "message": obj{"role": "assistant",
			"content": []any{obj{"type": "text", "text": "done"}},
			"usage":   obj{"input": 1000.0, "output": 200.0, "cacheWrite": 0.0, "cost": obj{"total": cost}}}}, nil)
		c.res = &childResult{Text: "done", ToolUses: 1, Tokens: 1200, Turns: 1}
		close(c.release)
	}()
}

func spawnSpending(r *rig, background bool) string {
	r.t.Helper()
	return r.must("Agent", obj{"prompt": "go", "description": "spend", "subagent_type": "general-purpose", "run_in_background": background})
}

// notificationText polls for the notification the model is sent (it is held behind the nudge debounce).
func notificationText(r *rig) string {
	r.t.Helper()
	for i := 0; i < 200 && len(r.messages()) == 0; i++ {
		time.Sleep(25 * time.Millisecond)
	}
	msgs := r.messages()
	if len(msgs) == 0 {
		return ""
	}
	content, _ := msgs[len(msgs)-1]["message"].(map[string]any)["content"].(string)
	return content
}

func TestCostDisplayTwins(t *testing.T) {
	const file = "cost-display"
	tw(t, file, "names the cost in the stats it already reports", func(t *testing.T) {
		r := costRig(t, true)
		runSpending(r, costDisplayCost)
		text := spawnSpending(r, false)
		hasStr(t, text, "~$0.0123")
		// Comma-joined with the rest, not glued to the token count with the "·" that the widget uses.
		hasStr(t, text, "1.2k token, ~$0.0123")
	})
	tw(t, file, "says nothing when the setting is off", func(t *testing.T) {
		r := costRig(t, false)
		runSpending(r, costDisplayCost)
		lacksStr(t, spawnSpending(r, false), "$")
	})
	tw(t, file, "says nothing for a model with no pricing data", func(t *testing.T) {
		r := costRig(t, true)
		runSpending(r, 0)
		text := spawnSpending(r, false)
		hasStr(t, text, "1.2k token") // tokens are still exact
		lacksStr(t, text, "$")
	})
	tw(t, file, "reports the cost as its own labelled field", func(t *testing.T) {
		r := costRig(t, true)
		runSpending(r, costDisplayCost)
		spawnSpending(r, false)
		// The agent ran in the foreground; it is reached by the handle its type gets.
		text := r.must("get_subagent_result", obj{"agent_id": "general-purpose"})
		// Pipe-separated `Label: value` fields, matching its neighbours.
		hasStr(t, text, "Cost: ~$0.0123")
		hasStr(t, text, "1.2k token | Cost: ~$0.0123 | Duration: ")
	})
	tw(t, file, "omits the field entirely when unpriced", func(t *testing.T) {
		r := costRig(t, true)
		runSpending(r, 0)
		spawnSpending(r, false)
		lacksStr(t, r.must("get_subagent_result", obj{"agent_id": "general-purpose"}), "Cost:")
	})
	tw(t, file, "includes the cost in the usage block when enabled", func(t *testing.T) {
		r := costRig(t, true)
		runSpending(r, costDisplayCost)
		spawnSpending(r, true)
		hasStr(t, notificationText(r), "<estimated_cost_usd>0.0123</estimated_cost_usd>")
	})
	tw(t, file, "omits it when disabled — this is LLM context, not a display", func(t *testing.T) {
		// A figure the orchestrator was not asked to track is one it may start reporting unprompted, so the setting
		// gates the context too, not just what a human sees.
		r := costRig(t, false)
		runSpending(r, costDisplayCost)
		spawnSpending(r, true)
		text := notificationText(r)
		hasStr(t, text, "<total_tokens>")
		lacksStr(t, text, "estimated_cost_usd")
	})
	tw(t, file, "omits it for a model with no pricing data", func(t *testing.T) {
		r := costRig(t, true)
		runSpending(r, 0)
		spawnSpending(r, true)
		text := notificationText(r)
		hasStr(t, text, "<total_tokens>")
		lacksStr(t, text, "estimated_cost_usd")
	})
}

// The steer reply carries the cost between the tokens and the tool uses. upstream: index.ts steer_subagent.
func TestSteerReplyShowsCostWhenEnabled(t *testing.T) {
	for _, show := range []bool{true, false} {
		r := costRig(t, show)
		id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "general-purpose"}))
		c := r.fleet.wait(t, 1)[0]
		c.spec.Observer.observe(obj{"type": "message_end", "message": obj{"role": "assistant",
			"usage": obj{"input": 1000.0, "output": 200.0, "cost": obj{"total": costDisplayCost}}}}, nil)
		out := r.must("steer_subagent", obj{"agent_id": id, "message": "go on"})
		if show {
			hasStr(t, out, "~$0.0123 · 0 tool uses")
		} else {
			lacksStr(t, out, "$")
		}
		c.abort()
	}
}
