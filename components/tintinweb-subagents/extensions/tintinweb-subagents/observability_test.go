package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The observability of a real child process (the fake pig): usage per message, tool uses, context percent, the
// conversation, compaction, and the StructuredOutput retry.

func TestRPCChildReportsUsageAndCompactionFromItsStream(t *testing.T) {
	useFakePig(t, "ok")
	var spent []lifetimeUsage
	c, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Hooks: &childHooks{Usage: func(u lifetimeUsage) { spent = append(spent, u) }}})
	eq(t, err, nil)
	res, err := c.wait()
	eq(t, err, nil)
	// One delta per assistant message, with cacheRead and cost.total read the way agent-runner.ts:1082 reads them.
	eq(t, spent, []lifetimeUsage{{30, 12, 3, 5, 0.01}, {30, 12, 3, 5, 0.01}})
	eq(t, *res.ContextPercent, 10.4)
	eq(t, res.Conversation, "[User]: x\n\n[Assistant]: step")
}

func TestRPCChildReportsOnlySuccessfulCompactions(t *testing.T) {
	useFakePig(t, "compaction")
	var got [][2]any
	c, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Hooks: &childHooks{Compaction: func(reason string, before int) { got = append(got, [2]any{reason, before}) }}})
	eq(t, err, nil)
	_, err = c.wait()
	eq(t, err, nil)
	eq(t, got, [][2]any{{"manual", 4321}})
}

func TestStructuredOutputRetryAndFailure(t *testing.T) {
	schema := `{"type":"object","properties":{"file":{"type":"string"}},"required":["file"]}`
	t.Run("a run that answers in prose is asked once more, and the payload is taken", func(t *testing.T) {
		useFakePig(t, "structured")
		prompts := filepath.Join(t.TempDir(), "prompts")
		t.Setenv("FAKE_PIG_PROMPTS", prompts)
		c, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Schema: schema})
		eq(t, err, nil)
		res, err := c.wait()
		eq(t, err, nil)
		eq(t, res.StructuredJSON, `{"file":"a.ts"}`)
		eq(t, res.StructuredRetried, true)
		eq(t, res.Failure, "")
		b, _ := os.ReadFile(prompts)
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		eq(t, len(lines), 2)
		eq(t, lines[1], "You did not call StructuredOutput, so your answer was not recorded.  Call StructuredOutput now with your complete final answer. Do not reply with prose.")
	})
	t.Run("a run that never validates has failed, naming the last rejection", func(t *testing.T) {
		useFakePig(t, "structured-none")
		c, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Schema: schema})
		eq(t, err, nil)
		res, err := c.wait()
		eq(t, err, nil)
		eq(t, res.StructuredJSON, "")
		eq(t, res.Failure, "The agent's StructuredOutput call did not match the required schema: $: must have required properties file")
	})
	t.Run("the child gets the tool in its allow-list and the schema in its environment", func(t *testing.T) {
		eq(t, childArgs(childSpec{AllowTools: []string{"read"}, Schema: schema}), []string{"--mode", "rpc", "--no-session", "--no-context-files", "--no-prompt-templates", "--no-themes", "--append-system-prompt", "", "--tools", "read,StructuredOutput"})
		argsFile := useFakePig(t, "structured")
		c, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Schema: schema})
		eq(t, err, nil)
		_, _ = c.wait()
		_, _ = os.ReadFile(argsFile)
	})
	t.Run("an isolated child has no extension to carry the tool", func(t *testing.T) {
		useFakePig(t, "structured")
		_, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Schema: schema, NoExtensions: true})
		eq(t, err != nil, true)
	})
}

func TestAChildWithASchemaRegistersTheTool(t *testing.T) {
	t.Setenv(depthEnv, "1")
	schemaFile := filepath.Join(t.TempDir(), "schema.json")
	must(t, os.WriteFile(schemaFile, []byte(`{"type":"object","required":["a"]}`), 0o600))
	t.Setenv(structuredSchemaFileEnv, schemaFile)
	e := Extension()
	h := StartHost(t, e, HostOptions{})
	res, failure := h.Tool("StructuredOutput", obj{"a": 1})
	eq(t, failure, "")
	eq(t, strings.Contains(string(res), `"json":"{\"a\":1}"`), true)
}

func TestNotificationCarriesTranscriptContextCompactionsAndCost(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, `{"showCost": true}`, ""))
	n := r.fleet.count()
	id := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
	c := r.fleet.wait(t, n+1)[n]
	c.spend(lifetimeUsage{Input: 1000, Output: 200, Cost: 0.0123})
	c.spec.Hooks.Compaction("threshold", 9)
	pct := 41.5
	c.finishWith(&childResult{Text: "done", ContextPercent: &pct})
	for i := 0; i < 400 && len(r.messages()) == 0; i++ {
		sleepMs(10)
	}
	rec := r.app.mgr.get(id)
	content := r.messages()[0]["message"].(map[string]any)["content"].(string)
	// Order, as upstream: tool-use id, output file, status, ..., usage with context_percent, compactions and cost.
	eq(t, strings.Contains(content, "<output-file>"+rec.OutputFile+"</output-file>\n<status>Done</status>"), true)
	eq(t, strings.Contains(content, "<usage><total_tokens>1200</total_tokens><tool_uses>0</tool_uses><context_percent>42</context_percent><compactions>1</compactions><estimated_cost_usd>0.0123</estimated_cost_usd><duration_ms>"), true)
	eq(t, strings.HasSuffix(content, "\nFull transcript available at: "+rec.OutputFile), true)
}

func TestResultAndSteerShowContextCompactionsAndTheConversation(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, "", ""))
	n := r.fleet.count()
	id := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
	c := r.fleet.wait(t, n+1)[n]
	c.spend(lifetimeUsage{Input: 1000, Output: 200})
	c.spec.Hooks.Compaction("overflow", 9)
	c.spec.Hooks.Compaction("overflow", 9)
	steer := r.must("steer_subagent", obj{"agent_id": id, "message": "left"})
	eq(t, strings.HasSuffix(steer, "Current state: 1.2k token · 0 tool uses · 2 compactions"), true)
	pct := 7.5
	c.finishWith(&childResult{Text: "answer", ContextPercent: &pct, Conversation: getAgentConversation([]map[string]any{
		{"role": "user", "content": []any{map[string]any{"type": "text", "text": " hi "}}},
		{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "yo"}, map[string]any{"type": "toolCall", "name": "bash"}}},
		{"role": "toolResult", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": strings.Repeat("z", 250)}}},
	})})
	<-r.app.mgr.get(id).done
	out := r.must("get_subagent_result", obj{"agent_id": id, "verbose": true})
	eq(t, strings.Contains(out, "| 1.2k token | Context: 8% | Compactions: 2 | Duration: "), true)
	eq(t, strings.HasSuffix(out, "answer\n\n--- Agent Conversation ---\n[User]: hi\n\n[Assistant]: yo\n\n[Tool Calls]:\n  Tool: bash\n\n[Tool Result (bash)]: "+strings.Repeat("z", 200)+"..."), true)
	plain := r.must("get_subagent_result", obj{"agent_id": id})
	eq(t, strings.Contains(plain, "Agent Conversation"), false)
}

func TestTokensAreTheLifetimeTotalNotTheSessionStats(t *testing.T) {
	// cacheRead is re-read on every call: it is excluded from the display total (#38).
	r := startObsRig(t, obsWorkspace(t, "", ""))
	text := fgAgent(t, r, func(c *fakeChild) {
		c.spend(lifetimeUsage{Input: 100, Output: 50, CacheWrite: 10, CacheRead: 500000})
		c.finishWith(&childResult{Text: "x"})
	})
	eq(t, strings.Contains(text, "(0 tool uses, 160 token)"), true)
}

func TestRoundPercent(t *testing.T) {
	eq(t, []int{roundPercent(0.5), roundPercent(41.49), roundPercent(41.5), roundPercent(99.9)}, []int{1, 41, 42, 100})
}

func TestSettingsOfThisSliceAreHonoredNotRefused(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, `{"outputTranscript": true, "reportUsage": true, "showCost": false}`, ""))
	eq(t, len(notices(r)), 0)
	r = startObsRig(t, obsWorkspace(t, `{"outputTranscript": false, "reportUsage": false}`, "output_transcript: true"))
	eq(t, len(notices(r)), 0)
}

func TestTranscriptFileDetails(t *testing.T) {
	t.Run("lines are JSON as JavaScript writes it: no HTML escaping, ISO millisecond timestamps", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("go"))
		s.push(assistant("a < b && c > d"))
		s.fire(obj{"type": "turn_end"})
		b, _ := os.ReadFile(path)
		eq(t, strings.Contains(string(b), "a < b && c > d"), true)
		for _, e := range readEntries(t, path) {
			ts := e["timestamp"].(string)
			eq(t, len(ts), len("2026-10-20T12:00:00.000Z"))
			eq(t, strings.HasSuffix(ts, "Z") && ts[10] == 'T' && ts[19] == '.', true)
		}
	})
	t.Run("a message that arrives while a compaction is aborted or failed is still written", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("go"))
		s.fire(obj{"type": "compaction_start", "reason": "manual"})
		s.push(assistant("during"))
		s.fire(obj{"type": "compaction_end", "reason": "manual", "aborted": true})
		s.fire(obj{"type": "compaction_end", "reason": "manual", "aborted": false, "errorMessage": "boom"})
		s.fire(obj{"type": "turn_end"})
		eq(t, len(readEntries(t, path)), 2)
	})
	t.Run("the initial entry is the prompt as a sidechain user entry", func(t *testing.T) {
		_, path := streamFixture(t, 1)
		e := readEntries(t, path)[0]
		eq(t, e["isSidechain"], true)
		eq(t, e["type"], "user")
		eq(t, e["agentId"], "agent-1")
		eq(t, e["cwd"], "/work")
		eq(t, e["message"], any(obj{"role": "user", "content": "do the thing"}))
	})
}
