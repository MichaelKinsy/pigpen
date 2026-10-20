package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"regexp"
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
	eq(t, len(res.Messages), 2)
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
		eq(t, childArgs(childSpec{Tools: []string{"read"}, Schema: schema}), []string{"--mode", "rpc", "--no-session", "--tools", "read,StructuredOutput"})
		argsFile := useFakePig(t, "structured")
		c, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Schema: schema})
		eq(t, err, nil)
		_, _ = c.wait()
		_, _ = os.ReadFile(argsFile)
	})
	t.Run("an isolated child has no extension to carry the tool", func(t *testing.T) {
		useFakePig(t, "structured")
		_, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Schema: schema, Isolated: true})
		eq(t, err != nil, true)
	})
}

func TestAChildWithASchemaRegistersTheTool(t *testing.T) {
	t.Setenv(depthEnv, "1")
	t.Setenv(structuredSchemaEnv, `{"type":"object","required":["a"]}`)
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
	c.finishWith(&childResult{Text: "answer", ContextPercent: &pct, Messages: []map[string]any{
		{"role": "user", "content": []any{map[string]any{"type": "text", "text": " hi "}}},
		{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "yo"}, map[string]any{"type": "toolCall", "name": "bash"}}},
		{"role": "toolResult", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": strings.Repeat("z", 250)}}},
	}})
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
	t.Run("ensureOutputFile creates the file and never truncates it", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "a.output")
		ensureOutputFile(path)
		st, err := os.Stat(path)
		eq(t, err, nil)
		eq(t, st.Size(), int64(0))
		must(t, os.WriteFile(path, []byte("kept\n"), 0o644))
		ensureOutputFile(path)
		b, _ := os.ReadFile(path)
		eq(t, string(b), "kept\n")
	})
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

func TestJSONSchemaKeywords(t *testing.T) {
	cases := []struct {
		name   string
		schema map[string]any
		value  any
		want   string // "" is valid
	}{
		{"type union", obj{"type": []any{"string", "null"}}, 1.0, "$: must be either string or null"},
		{"type integer", obj{"type": "integer"}, 1.5, "$: must be integer"},
		{"type boolean", obj{"type": "boolean"}, "x", "$: must be boolean"},
		{"enum", obj{"enum": []any{"a", "b"}}, "c", "$: must be equal to one of the allowed values"},
		{"enum ok", obj{"enum": []any{"a", 1.0}}, 1.0, ""},
		{"const", obj{"const": "a"}, "b", "$: must be equal to constant"},
		{"additionalProperties false", obj{"type": "object", "properties": obj{"a": obj{}}, "additionalProperties": false}, obj{"a": 1.0, "b": 2.0}, "$: must not have additional properties"},
		{"additionalProperties schema", obj{"type": "object", "additionalProperties": obj{"type": "string"}}, obj{"k": 1.0}, "$.k: must be string"},
		{"items", obj{"type": "array", "items": obj{"type": "string"}}, []any{"a", 1.0}, "$.1: must be string"},
		{"minItems", obj{"type": "array", "minItems": 2.0}, []any{1.0}, "$: must not have fewer than 2 items"},
		{"maxItems", obj{"type": "array", "maxItems": 1.0}, []any{1.0, 2.0}, "$: must not have more than 1 items"},
		{"uniqueItems", obj{"type": "array", "uniqueItems": true}, []any{1.0, 1.0}, "$: must not have duplicate items"},
		{"minLength", obj{"type": "string", "minLength": 3.0}, "ab", "$: must not have fewer than 3 characters"},
		{"maxLength", obj{"type": "string", "maxLength": 1.0}, "ab", "$: must not have more than 1 characters"},
		{"pattern", obj{"type": "string", "pattern": "^a+$"}, "b", `$: must match pattern "^a+$"`},
		{"maximum", obj{"type": "number", "maximum": 5.0}, 6.0, "$: must be <= 5"},
		{"exclusiveMinimum", obj{"type": "number", "exclusiveMinimum": 5.0}, 5.0, "$: must be > 5"},
		{"exclusiveMaximum", obj{"type": "number", "exclusiveMaximum": 5.0}, 5.0, "$: must be < 5"},
		{"multipleOf", obj{"type": "number", "multipleOf": 3.0}, 7.0, "$: must be multiple of 3"},
		{"multipleOf ok", obj{"type": "number", "multipleOf": 3.0}, 9.0, ""},
		{"minProperties", obj{"type": "object", "minProperties": 1.0}, obj{}, "$: must not have fewer than 1 properties"},
		{"anyOf", obj{"anyOf": []any{obj{"type": "string"}, obj{"type": "number"}}}, true, "$: must match a schema in anyOf"},
		{"oneOf", obj{"oneOf": []any{obj{"type": "number"}, obj{"type": "integer"}}}, 1.0, "$: must match exactly one schema in oneOf"},
		{"allOf", obj{"allOf": []any{obj{"type": "number"}, obj{"minimum": 3.0}}}, 1.0, "$: must be >= 3"},
		{"not", obj{"not": obj{"type": "string"}}, "x", "$: must not be valid"},
		{"ref", obj{"type": "object", "properties": obj{"a": obj{"$ref": "#/$defs/n"}}, "$defs": obj{"n": obj{"type": "number"}}}, obj{"a": "x"}, "$.a: must be number"},
		{"false schema", obj{"type": "object", "properties": obj{"a": false}}, obj{"a": 1.0}, "$.a: schema is false"},
		{"at most five reported", obj{"type": "array", "items": obj{"type": "string"}}, []any{1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0},
			"$.0: must be string; $.1: must be string; $.2: must be string; $.3: must be string; $.4: must be string"},
		{"integer-valued float is an integer", obj{"type": "integer"}, 3.0, ""},
		{"a key with a slash is a JSON Pointer escape, as the original reports it", obj{"type": "object", "properties": obj{"a/b": obj{"type": "string"}}}, obj{"a/b": 1.0}, "$.a~1b: must be string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			schema := c.schema
			if schema["type"] != "object" {
				schema = obj{"type": "object", "properties": obj{"v": c.schema}}
				c.value = obj{"v": c.value}
				if c.want != "" {
					c.want = regexp.MustCompile(`\$([.:])`).ReplaceAllStringFunc(c.want, func(m string) string { // the paths only: they gain the property name
						if m == "$:" {
							return "$.v:"
						}
						return "$.v."
					})
				}
			}
			compiled, problem := compileJSONSchema(schema)
			eq(t, problem, "")
			msg, ok := compiled.check(c.value)
			eq(t, msg, c.want)
			eq(t, ok, c.want == "")
		})
	}
}

func TestTheSystemPromptMessageOfPigsStreamIsNotATranscriptEntry(t *testing.T) {
	// A real `pig --mode rpc` emits the system prompt as the first message_end; the prompt entry is the second.
	s, path := streamFixture(t, 1, map[string]any{"role": "system", "content": ""}, user("do the thing"))
	s.push(assistant("ok"))
	s.fire(obj{"type": "turn_end"})
	entries := readEntries(t, path)
	eq(t, len(entries), 2)
	eq(t, entries[1]["type"], "assistant")
}
