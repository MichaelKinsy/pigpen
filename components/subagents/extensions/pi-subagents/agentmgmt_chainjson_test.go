package pi_subagents

import (
	"testing"
)

// Twins of the JSON chain cases of test/unit/chain-serializer.test.ts (the markdown ones are in chain_test.go).

func parseJSONChainOf(t *testing.T, v any, file string) (*ChainConfig, error) {
	t.Helper()
	return parseJSONChain(marshalJSON(v, ""), SourceProject, file)
}

func wantErrRe(t *testing.T, err error, re string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want /%s/", re)
	}
	hasRe(t, err.Error(), re)
}

func TestChainSerializerJSON(t *testing.T) {
	const f = "chain-serializer"
	dyn := func(parallel any, extra ...any) any {
		return m{"name": "dynamic-review", "description": "Review dynamic targets", "chain": l{
			m{"agent": "scout", "task": "Return targets", "as": "targets", "outputSchema": m{"type": "object"}},
			m{"expand": m{"from": m{"output": "targets", "path": "/items"}, "item": "target", "key": "/path", "maxItems": 4.0}, "parallel": parallel, "collect": m{"as": "reviews"}},
		}}
	}
	_ = dyn
	tw(t, f, "rejects invalid dynamic JSON chains with useful diagnostics", func(t *testing.T) {
		_, err := parseJSONChainOf(t, m{"name": "bad-dynamic-review", "description": "Bad dynamic targets", "chain": l{
			m{"agent": "scout", "task": "Return targets", "as": "targets", "outputSchema": m{"type": "object"}},
			m{"expand": m{"from": m{"output": "targets", "path": "/items"}, "maxItems": 4.0}, "parallel": l{m{"agent": "reviewer", "task": "Review"}}, "collect": m{"as": "reviews"}},
		}}, "/tmp/bad-dynamic-review.chain.json")
		wantErrRe(t, err, `static parallel arrays`)
		_, err = parseJSONChainOf(t, m{"name": "bad", "description": "Bad", "chain": l{1.0}}, "/tmp/bad.chain.json")
		wantErrRe(t, err, `step 1 must be an object`)
	})
	tw(t, f, "serializes JSON chains back to JSON", func(t *testing.T) {
		in := m{"name": "dynamic-review", "package": "code-analysis", "description": "Review dynamic targets", "chain": l{
			m{"agent": "scout", "task": "Return targets", "as": "targets", "outputSchema": m{"type": "object"}},
			m{"expand": m{"from": m{"output": "targets", "path": "/items"}, "item": "target", "key": "/path", "maxItems": 4.0},
				"parallel": m{"agent": "reviewer", "task": "Review {target.path}", "outputSchema": m{"type": "object"}}, "collect": m{"as": "reviews"}},
		}}
		parsed, err := parseJSONChainOf(t, in, "/tmp/dynamic-review.chain.json")
		if err != nil {
			t.Fatal(err)
		}
		serialized := serializeJSONChain(parsed)
		noRe(t, serialized, `^---`)
		v := parseJSONMust(t, serialized).(*jsObject)
		eq(t, v.vals["name"], "dynamic-review", "name")
		eq(t, v.vals["package"], "code-analysis", "package")
		steps := v.vals["chain"].([]any)
		eq(t, steps[1].(*jsObject).obj("collect").vals["as"], "reviews", "collect.as")
	})
	tw(t, f, "parses declarative JSON chains with dynamic fanout toolBudget", func(t *testing.T) {
		in := m{"name": "dynamic-review", "description": "Review dynamic targets", "chain": l{
			m{"agent": "scout", "task": "Return targets", "as": "targets", "outputSchema": m{"type": "object"}, "toolBudget": m{"hard": 4.0}},
			m{"expand": m{"from": m{"output": "targets", "path": "/items"}, "item": "target", "key": "/path", "maxItems": 4.0},
				"parallel": m{"agent": "reviewer", "task": "Review {target.path}", "outputSchema": m{"type": "object"}, "toolBudget": m{"soft": 3.0, "hard": 5.0}}, "collect": m{"as": "reviews"}},
		}}
		parsed, err := parseJSONChainOf(t, in, "/tmp/dynamic-review.chain.json")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, canonJSON(parsed.JSONSteps[0].(*jsObject).vals["toolBudget"]), `{"hard":4}`, "step 0")
		eq(t, canonJSON(parsed.JSONSteps[1].(*jsObject).obj("parallel").vals["toolBudget"]), `{"hard":5,"soft":3}`, "step 1")
	})
	tw(t, f, "rejects invalid JSON chain toolBudget", func(t *testing.T) {
		_, err := parseJSONChainOf(t, m{"name": "bad-tool-budget", "description": "Bad tool budget", "chain": l{m{"agent": "worker", "toolBudget": m{"hard": 0.0}}}}, "/tmp/bad-tool-budget.chain.json")
		wantErrRe(t, err, `step 1 toolBudget\.hard must be an integer >= 1`)
	})
	tw(t, f, "parses declarative JSON chains with dynamic fanout", func(t *testing.T) {
		in := m{"name": "dynamic-review", "description": "Review dynamic targets", "chain": l{
			m{"agent": "scout", "task": "Return targets", "as": "targets", "outputSchema": m{"type": "object"}},
			m{"expand": m{"from": m{"output": "targets", "path": "/items"}, "item": "target", "key": "/path", "maxItems": 4.0},
				"parallel": m{"agent": "reviewer", "task": "Review {target.path}", "outputSchema": m{"type": "object"}}, "collect": m{"as": "reviews"}},
		}}
		parsed, err := parseJSONChainOf(t, in, "/tmp/dynamic-review.chain.json")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, parsed.Name, "dynamic-review", "name")
		eq(t, len(parsed.JSONSteps), 2, "steps")
		eq(t, canonJSON(parsed.JSONSteps[1].(*jsObject).vals["collect"]), `{"as":"reviews"}`, "collect")
		eq(t, canonJSON(parsed.JSONSteps[0].(*jsObject).vals["outputSchema"]), `{"type":"object"}`, "schema")
	})
	tw(t, f, "parses and validates acceptance in JSON chains", func(t *testing.T) {
		parsed, err := parseJSONChainOf(t, m{"name": "accepted-chain", "description": "Chain with acceptance gates", "chain": l{
			m{"agent": "worker", "task": "Fix bug", "acceptance": m{"level": "checked", "evidence": l{"changed-files", "commands-run"}}},
			m{"parallel": l{m{"agent": "reviewer", "task": "Review", "acceptance": "attested"}}},
		}}, "/tmp/accepted-chain.chain.json")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, canonJSON(parsed.JSONSteps[0].(*jsObject).vals["acceptance"]), `{"evidence":["changed-files","commands-run"],"level":"checked"}`, "acceptance")
		par := parsed.JSONSteps[1].(*jsObject).vals["parallel"].([]any)
		eq(t, par[0].(*jsObject).vals["acceptance"], "attested", "attested")
		_, err = parseJSONChainOf(t, m{"name": "bad-acceptance", "description": "Bad acceptance", "chain": l{m{"agent": "worker", "acceptance": m{"level": "none"}}}}, "/tmp/bad-acceptance.chain.json")
		wantErrRe(t, err, `step 1 acceptance\.reason is required`)
	})
}
