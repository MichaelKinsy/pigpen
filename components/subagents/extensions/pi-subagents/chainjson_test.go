package pi_subagents

import (
	"path/filepath"
	"strings"
	"testing"
)

// The .chain.json format and the chain validators (acceptance, tool budget,
// dynamic fan-out shape, output bindings), as the original runs them before
// any child starts.

const jsonChainGood = `{
  "name": "review-flow",
  "description": "Review a change.",
  "chain": [
    { "agent": "worker", "task": "Implement {task}", "as": "impl", "toolBudget": { "hard": 7, "soft": 4, "block": ["read"] } },
    { "agent": "reviewer", "task": "Review {outputs.impl}", "acceptance": "checked" }
  ],
  "vendor_key": "kept"
}`

func TestParseJSONChain(t *testing.T) {
	c, err := jsonChain(jsonChainGood, "project", "/w/.pi/chains/review-flow.chain.json")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, c.Name, "review-flow", "name")
	eq(t, c.Description, "Review a change.", "description")
	eq(t, len(c.Steps), 2, "steps")
	eq(t, c.Steps[0].Agent, "worker", "step 1 agent")
	eq(t, c.Steps[0].Task, "Implement {task}", "step 1 task")
	eq(t, c.Steps[0].As, "impl", "step 1 as")
	eq(t, c.Steps[0].ToolBudget != nil, true, "step 1 toolBudget")
	eq(t, c.Steps[1].Agent, "reviewer", "step 2 agent")
	eq(t, c.Steps[1].Task, "Review {outputs.impl}", "step 2 references the output")
	extra, _ := c.ExtraFields.str("vendor_key")
	eq(t, extra, "kept", "extra field")

	// The writer round-trips the raw steps and the extras.
	out := serializeJsonChain(c)
	if !strings.Contains(out, `"agent": "worker"`) || !strings.Contains(out, `"acceptance": "checked"`) || !strings.Contains(out, `"vendor_key": "kept"`) {
		t.Errorf("round trip:\n%s", out)
	}
	again, err := jsonChain(out, "project", c.FilePath)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	eq(t, len(again.Steps), 2, "steps after round trip")
}

func TestJSONChainValidation(t *testing.T) {
	cases := []struct {
		json string
		want string
	}{
		{`{"name":"x"}`, "must include string description."},
		{`{"description":"d"}`, "must include string name."},
		{`{"name":"x","description":"d"}`, "must include array chain."},
		{`{"name":"x","description":"d","chain":[{"toolBudget":{"hard":0}}]}`, "step 1 toolBudget.hard must be an integer >= 1."},
		{`{"name":"x","description":"d","chain":[{"agent":"a","acceptance":"bogus"}]}`, "Invalid JSON chain 'f.chain.json': step 1 acceptance has invalid level 'bogus'."},
		{`{"name":"x","description":"d","chain":[{"agent":"a"},{"parallel":[{"agent":"b","acceptance":"reviewed"}]}]}`, "Invalid JSON chain 'f.chain.json': step 2 parallel task 1 acceptance is an achieved status"},
		{`{"name":"x","description":"d","chain":[5]}`, "step 1 must be an object."},
		{`{"name":"x","description":"d","chain":[{"agent":"a","task":"{outputs.none}"}]}`, "Invalid JSON chain 'f.chain.json': Unknown chain output reference '{outputs.none}' at step 1."},
	}
	for i, c := range cases {
		_, err := jsonChain(c.json, "project", "f.chain.json")
		if err == nil {
			t.Errorf("case %d: no error", i)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("case %d: %q does not contain %q", i, err.Error(), c.want)
		}
	}
}

func TestAcceptanceValidation(t *testing.T) {
	cases := []struct {
		input any
		want  string
	}{
		{nil, ""},
		{false, ""},
		{"auto", ""},
		{"checked", ""},
		{"reviewed", "is an achieved status, not a requestable acceptance level."},
		{"bogus", "has invalid level 'bogus'."},
		{"none", `level "none" requires a reason`},
		{"verified", `level "verified" requires object form`},
		{map[string]any{"level": "none", "reason": "disabled"}, ""},
		{map[string]any{"level": "none"}, ".reason is required when level is none."},
		{map[string]any{"bogus": true}, ".bogus is not supported."},
		{map[string]any{"level": "sometimes"}, ".level must be one of auto, none, attested, checked, verified."},
		{map[string]any{"report": "maybe"}, ".report must be on or off."},
		{map[string]any{"report": "on"}, ""},
		{map[string]any{"preserveStagedIndex": true}, ".preserveStagedIndex requires level checked or verified."},
		{map[string]any{"level": "checked", "preserveStagedIndex": true}, ""},
		{map[string]any{"evidence": []any{"commands-run", "made-up"}}, `.evidence[1] "made-up" is not a supported evidence kind.`},
		{map[string]any{"evidence": "commands-run"}, ".evidence must be an array."},
		{map[string]any{"level": "verified", "verify": []any{}}, ".verify must contain at least one runtime command when level is verified."},
		{map[string]any{"verify": []any{map[string]any{"command": "go test"}}}, ".verify[0].id is required."},
		{map[string]any{"verify": []any{map[string]any{"id": "t", "command": "go test", "timeoutMs": 0}}}, ".verify[0].timeoutMs must be an integer >= 1."},
		{map[string]any{"verify": []any{map[string]any{"id": "t", "command": "go test", "output": "yaml"}}}, `.verify[0].output must be "json" when present.`},
		{map[string]any{"verify": []any{map[string]any{"id": "t", "command": "go test", "schema": map[string]any{"type": "object"}}}}, `.verify[0].schema requires output: "json".`},
		{map[string]any{"review": "yes"}, ".review must be false or an object."},
		{map[string]any{"review": map[string]any{"required": true}}, ""},
		{map[string]any{"review": map[string]any{"agent": 5}}, ".review.agent must be a string."},
		{map[string]any{"stopRules": []any{5}}, ".stopRules[0] must be a string."},
		{map[string]any{"criteria": "x"}, ".criteria must be an array."},
		{map[string]any{"criteria": []any{map[string]any{"id": "Same Id", "must": "passes"}}},
			""},
		{map[string]any{"criteria": []any{map[string]any{"id": "same_id", "must": "passes"}, map[string]any{"id": "Same-Id", "must": "passes"}}},
			".criteria[1].id duplicates normalized criterion id 'same-id'."},
		{map[string]any{"criteria": []any{map[string]any{"id": "a"}}}, ".criteria[0].must is required."},
		{map[string]any{"criteria": []any{map[string]any{"id": "a", "must": "x", "severity": "maybe"}}}, ".criteria[0].severity must be required or recommended."},
		{`{"level": "checked"}`, ""},
		{`{"bogus": 1}`, ".bogus is not supported."},
		{`{nope`, "JSON string must encode a valid acceptance object"},
		{`[1]`, "JSON string must encode an object; got an array."},
	}
	for i, c := range cases {
		errs := ValidateAcceptanceInput(c.input, "")
		if c.want == "" {
			if len(errs) > 0 {
				t.Errorf("case %d: unexpected errors %v", i, errs)
			}
			continue
		}
		found := false
		for _, e := range errs {
			if strings.Contains(e, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("case %d: %v does not contain %q", i, errs, c.want)
		}
	}
	// The path label reaches every message.
	errs := ValidateAcceptanceInput(map[string]any{"report": "maybe"}, "step 2 acceptance")
	if len(errs) != 1 || errs[0] != "step 2 acceptance.report must be on or off." {
		t.Errorf("label: %v", errs)
	}
}

func TestDynamicFanoutShapeValidation(t *testing.T) {
	parse := func(t *testing.T, json string) *jsObject {
		t.Helper()
		v, err := parseJSON([]byte(json))
		if err != nil {
			t.Fatal(err)
		}
		obj, _ := v.(*jsObject)
		return obj
	}
	good := parse(t, `{
		"expand": { "from": { "output": "list", "path": "/items" }, "item": "entry", "key": "/name", "maxItems": 8 },
		"parallel": { "agent": "worker", "task": "Check {entry.name}" },
		"collect": { "as": "checks" }
	}`)
	if err := ValidateDynamicStepShape(good, 1, nil); err != nil {
		t.Errorf("good shape: %v", err)
	}
	cases := []struct {
		json string
		want string
	}{
		{`{"expand":{},"parallel":{"agent":"w"},"collect":{"as":"x"}}`, "requires expand.from."},
		{`{"expand":{"from":{"output":"bad name","path":"/i"}},"parallel":{"agent":"w"},"collect":{"as":"x"}}`, "has invalid expand.from.output 'bad name'."},
		{`{"expand":{"from":{"output":"list","path":"items"}},"parallel":{"agent":"w"},"collect":{"as":"x"}}`, "expand.from.path must be a JSON Pointer starting with '/'."},
		{`{"expand":{"from":{"output":"list","path":"/i"}},"parallel":{"agent":"w"},"collect":{"as":"x"}}`, "requires expand.maxItems"},
		{`{"expand":{"from":{"output":"list","path":"/i"},"maxItems":-1},"parallel":{"agent":"w"},"collect":{"as":"x"}}`, "expand.maxItems must be an integer >= 0."},
		{`{"expand":{"from":{"output":"list","path":"/i"},"maxItems":1},"parallel":{"agent":"w"}}`, "requires collect.as with a safe output name."},
		{`{"expand":{"from":{"output":"list","path":"/i"},"maxItems":1},"parallel":{"agent":"w"},"collect":{"as":"9bad"}}`, "requires collect.as with a safe output name."},
		{`{"expand":{"from":{"output":"list","path":"/i"},"maxItems":1},"parallel":{"task":"x"},"collect":{"as":"y"}}`, "parallel.agent is required."},
		{`{"expand":{"from":{"output":"list","path":"/i"},"maxItems":1},"parallel":{"agent":"w","task":"{other.name}"},"collect":{"as":"y"}}`, "Unsupported template reference '{other.name}'"},
		{`{"expand":{"from":{"output":"list","path":"/i"},"maxItems":1,"item":"entry"},"parallel":{"agent":"w","task":"{entry..name}"},"collect":{"as":"y"}}`, "Invalid item reference '{entry..name}'"},
		{`{"expand":{"from":{"output":"list","path":"/i"},"maxItems":1},"parallel":{"agent":"w","bogus":1},"collect":{"as":"y"}}`, "does not support field 'bogus'."},
	}
	for i, c := range cases {
		err := ValidateDynamicStepShape(parse(t, c.json), 1, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("case %d: %v does not contain %q", i, err, c.want)
		}
	}

	// Output bindings across steps.
	steps := func(json string) []*jsObject {
		v, _ := parseJSON([]byte(json))
		arr, _ := v.([]any)
		var out []*jsObject
		for _, s := range arr {
			o, _ := s.(*jsObject)
			out = append(out, o)
		}
		return out
	}
	if err := ValidateChainOutputBindings(steps(`[{"agent":"a","as":"one"},{"agent":"b","task":"use {outputs.one}"}]`), nil); err != nil {
		t.Errorf("valid bindings: %v", err)
	}
	bad := []struct {
		json string
		want string
	}{
		{`[{"agent":"a","as":"1bad"}]`, "Invalid chain output name '1bad' at step 1."},
		{`[{"agent":"a","as":"same"},{"agent":"b","as":"same"}]`, "Duplicate chain output name 'same'."},
		{`[{"agent":"b","task":"use {outputs.missing}"}]`, "Unknown chain output reference '{outputs.missing}' at step 1."},
		{`[{"agent":"a","parallel":[{"agent":"x","as":"p1"}]},{"agent":"b","task":"{outputs.p1}"}]`, ""},
		{`[{"expand":{"from":{"output":"none","path":"/x"},"maxItems":1},"parallel":{"agent":"w"},"collect":{"as":"y"}}]`, "Dynamic chain step 1 references unknown output 'none'"},
	}
	for i, c := range bad {
		err := ValidateChainOutputBindings(steps(c.json), nil)
		if c.want == "" {
			if err != nil {
				t.Errorf("bindings case %d: %v", i, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("bindings case %d: %v does not contain %q", i, err, c.want)
		}
	}
}

func TestChainJSONDiscoveryAndRun(t *testing.T) {
	home := withTempHome(t)
	_ = home
	dir := tmp(t)
	writeFile(t, filepath.Join(dir, ".pi", "chains", "flow.chain.json"), strings.Replace(jsonChainGood, `"name": "review-flow"`, `"name": "flow"`, 1))
	writeFile(t, filepath.Join(dir, ".pi", "chains", "flow.chain.md"), "---\nname: flow\ndescription: Markdown twin.\n---\n\n## worker\n\nDo it\n")
	writeFile(t, filepath.Join(dir, ".pi", "chains", "broken.chain.json"), `{"name":"broken"}`)
	d := DiscoverAgentsAll(dir)
	var found *ChainConfig
	for i := range d.Chains {
		if d.Chains[i].Name == "flow" {
			found = &d.Chains[i]
		}
	}
	if found == nil {
		t.Fatal("flow chain not discovered")
	}
	if !chainPathIsJSON(found.FilePath) {
		t.Errorf(".chain.json must win the name collision: %s", found.FilePath)
	}
	if found.Description != "Review a change." {
		t.Errorf("the JSON chain won: %q", found.Description)
	}
	hasBrokenDiag := false
	for _, diag := range d.ChainDiagnostics {
		if strings.HasSuffix(diag.FilePath, "broken.chain.json") && strings.Contains(diag.Error, "must include string description") {
			hasBrokenDiag = true
		}
	}
	if !hasBrokenDiag {
		t.Errorf("broken chain diagnostic: %+v", d.ChainDiagnostics)
	}

	// /run-chain runs the JSON chain's steps in order, threading the outputs.
	calls := fakeRunner(t, map[string]string{"Implement": "built", "Review built": "reviewed"}, "")
	agents := []AgentConfig{{Name: "worker"}, {Name: "reviewer"}}
	out, err := runChain(nil, ".", found, agents, "the feature", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, out, "reviewed", "the chain's answer")
	eq(t, len(*calls), 2, "two steps")
}
