package tintinweb_subagents

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The conversation clone end to end: the hook, the real clone, the host's model API (scripted) and the registered
// Agent tool, with children scripted as everywhere else.

func toolCallEvents(args obj) []map[string]any {
	msg := map[string]any{"role": "assistant", "stopReason": "toolUse",
		"content": []any{map[string]any{"type": "toolCall", "id": "tc-1", "name": "Agent", "arguments": args}}}
	return []map[string]any{{"type": "done", "reason": "toolUse", "message": msg}}
}

// scriptModel makes the host's model answer replies in order, then in prose, and records every request.
func (m *mentionRig) scriptModel(replies ...[]map[string]any) *[]map[string]any {
	var seen []map[string]any
	m.rig.mu.Lock()
	m.rig.modelFn = func(model, request map[string]any) []map[string]any {
		m.rig.mu.Lock()
		defer m.rig.mu.Unlock()
		seen = append(seen, request)
		if len(replies) == 0 {
			return ModelText("done")
		}
		next := replies[0]
		replies = replies[1:]
		return next
	}
	m.rig.mu.Unlock()
	return &seen
}

func (m *mentionRig) requests(seen *[]map[string]any) []map[string]any {
	m.rig.mu.Lock()
	defer m.rig.mu.Unlock()
	return append([]map[string]any(nil), *seen...)
}

func TestMentionCloneThroughTheHost(t *testing.T) {
	agent := obj{"subagent_type": "Explore", "prompt": "go", "description": "find the flaky test"}
	history := []any{obj{"role": "user", "content": []any{obj{"type": "text", "text": "hi"}}}}

	t.Run("a mention in model mode reaches the model with the session's own level", func(t *testing.T) {
		m := startMention(t, mentionBoot{realClone: true})
		m.mu.Lock()
		m.level, m.conv = "high", history
		m.mu.Unlock()
		seen := m.scriptModel(toolCallEvents(agent))
		eq(t, m.send("@explore find the flaky test"), handled)
		c := m.fleet.wait(t, 1)[0]
		req := m.firstRequest(t, seen)
		eq(t, req["reasoning"], "high")
		eq(t, req["apiKey"], "the-key")
		eq(t, req["systemPrompt"], "the live system prompt")
		msgs := req["messages"].([]any)
		eq(t, len(msgs), 2)
		eq(t, msgs[0], history[0])
		text := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		eq(t, text, "find the flaky test\n\n"+agentMentionReminder("Explore"))
		eq(t, c.spec.Prompt, "go")
		// An ordinary top-level agent: in the background, and attributed to no tool call.
		rec := m.app.mgr.list()[0]
		m.app.mgr.mu.Lock()
		defer m.app.mgr.mu.Unlock()
		eq(t, [2]any{rec.Background, rec.ToolCallID}, [2]any{true, ""})
		eq(t, len(m.messages()), 0)
	})
	t.Run("a session on the off level asks for no reasoning", func(t *testing.T) {
		m := startMention(t, mentionBoot{realClone: true})
		m.mu.Lock()
		m.level = "off"
		m.mu.Unlock()
		seen := m.scriptModel(toolCallEvents(agent))
		m.send("@explore go")
		m.fleet.wait(t, 1)
		_, has := m.firstRequest(t, seen)["reasoning"]
		eq(t, has, false)
	})
	t.Run("the clone's turn after its spawn is answered, as Pi's loop would", func(t *testing.T) {
		m := startMention(t, mentionBoot{realClone: true})
		seen := m.scriptModel(toolCallEvents(agent))
		m.send("@explore go")
		m.fleet.wait(t, 1)
		waitFor(t, "second turn", func() bool { return len(m.requests(seen)) == 2 })
		last := m.requests(seen)[1]["messages"].([]any)
		res := last[len(last)-1].(map[string]any)
		eq(t, [2]any{res["role"], res["toolCallId"]}, [2]any{"toolResult", "tc-1"})
		eq(t, strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "Agent ID: "), true)
	})
	t.Run("a model that answers in prose falls back to a direct start", func(t *testing.T) {
		m := startMention(t, mentionBoot{realClone: true})
		m.scriptModel() // prose
		m.send("@explore find it")
		m.fleet.wait(t, 1)
		m.waitNotify(t, "Started @explore directly — the conversation clone did not start it", "warning")
	})
	t.Run("a model that fails falls back to a direct start naming the failure", func(t *testing.T) {
		m := startMention(t, mentionBoot{realClone: true})
		m.scriptModel(ModelError("overloaded"))
		m.send("@explore find it")
		m.fleet.wait(t, 1)
		got := m.waitNotifyContaining(t, "Started @explore directly — ", "warning")
		eq(t, strings.Contains(got, "overloaded"), true)
	})
}

func TestAMessageParkedForAQueuedAgentIsDeliveredWhenItStarts(t *testing.T) {
	m := startMention(t, mentionBoot{settings: obj{"maxConcurrent": 1}})
	first := m.spawnBg("Explore")
	m.spawnBg("Explore")
	m.fleet.wait(t, 1)
	eq(t, m.send("@explore-2 wait for me"), handled)
	waitFor(t, "parked", func() bool {
		m.app.mgr.mu.Lock()
		defer m.app.mgr.mu.Unlock()
		for _, r := range m.app.mgr.records {
			if len(r.PendingSteers) == 1 {
				return true
			}
		}
		return false
	})
	m.finish(t, first, 0, "done")
	kids := m.fleet.wait(t, 2)
	waitFor(t, "delivered", func() bool { return len(kids[1].steers()) == 1 })
	eq(t, kids[1].steers(), []string{"wait for me"})
}

func TestSummarize(t *testing.T) {
	eq(t, summarize("Fast codebase exploration. Read-only, medium breadth."), "Fast codebase exploration.")
	eq(t, summarize("No terminator at all"), "No terminator at all")
	eq(t, summarize("Ends with punctuation!"), "Ends with punctuation!")
	eq(t, summarize("Version 1.2 is not a sentence end. But this is."), "Version 1.2 is not a sentence end.")
	eq(t, summarize("  spaced \n out  words.  Next."), "spaced out words.")
	long := strings.Repeat("word ", 30) + "end."
	got := summarize(long)
	eq(t, len([]rune(got)), 60)
	eq(t, strings.HasSuffix(got, "…"), true)
}

func TestAgentMentionsSetting(t *testing.T) {
	mode := func(v any) string {
		s := sanitizeSettings(map[string]any{"agentMentions": v})
		if s.AgentMentions == nil {
			return "<unset>"
		}
		return *s.AgentMentions
	}
	eq(t, mode("model"), "model")
	eq(t, mode("direct"), "direct")
	eq(t, mode("off"), "off")
	eq(t, mode(true), "model") // the boolean it was before the `model` mode existed
	eq(t, mode(false), "off")
	eq(t, mode("bogus"), "<unset>")
	eq(t, mode(3.0), "<unset>")
	// The project file wins over the global one.
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	writeFile(t, filepath.Join(agentDir(), "subagents.json"), `{"agentMentions":"direct"}`)
	eq(t, *loadSettings(cwd).AgentMentions, "direct")
	writeFile(t, filepath.Join(cwd, ".pi", "subagents.json"), `{"agentMentions":"off"}`)
	eq(t, *loadSettings(cwd).AgentMentions, "off")
}

func TestListMentionRecordsIsNewestFirstAndSnapshotsTheRecords(t *testing.T) {
	fl := &fleet{}
	m := newManager(fl.start, nil)
	t.Cleanup(m.dispose)
	a := m.spawn("Explore", "a", "", childSpec{}, true)
	time.Sleep(3 * time.Millisecond)
	b := m.spawn("Explore", "b", "auth-audit", childSpec{}, true)
	got := m.listMentionRecords()
	eq(t, []string{got[0].Description, got[1].Description}, []string{"b", "a"})
	eq(t, [2]string{got[0].Handle, got[0].Alias}, [2]string{b.Handle, "auth-audit"})
	_ = a
}
