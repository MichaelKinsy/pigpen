package pi_subagents

import (
	"errors"
	"regexp"
	"testing"
)

// Twins of test/unit/dynamic-fanout.test.ts.

func dfOutputs(t *testing.T, structured string) map[string]ChainOutputEntry {
	e := ChainOutputEntry{Text: `{"items":[{"path":"src/a.ts"},{"path":"src/b.ts"}]}`, Agent: "scout", StepIndex: 0}
	if structured != "" {
		e.Structured, e.HasStructured = obj(t, structured), true
	}
	return map[string]ChainOutputEntry{"targets": e}
}

func dfThrows(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil || !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Errorf("error = %v, want /%s/", err, pattern)
	}
}

const dfTargets = `{"items":[{"path":"src/a.ts"},{"path":"src/b.ts"}]}`

func dfBase(t *testing.T, mod func(step *jsObject)) *jsObject {
	step := obj(t, `{"expand":{"from":{"output":"targets","path":"/items"},"item":"target","key":"/path","maxItems":4},"parallel":{"agent":"reviewer","task":"Review {target.path}"},"collect":{"as":"reviews"}}`)
	if mod != nil {
		mod(step)
	}
	return step
}

func dfTexts(group DynamicMaterializedGroup, key string) []any {
	var out []any
	for _, p := range group.Parallel {
		out = append(out, p.vals[key])
	}
	return out
}

func TestDynamicFanoutTwins(t *testing.T) {
	const f = "dynamic-fanout"
	tw(t, f, "resolves JSON Pointers and materializes item templates", func(t *testing.T) {
		v, err := resolveJSONPointer(obj(t, `{"items":[1,2]}`), "/items/1", "path")
		eq(t, err, nil, "pointer")
		eq(t, v, 2.0, "value")
		step := dfBase(t, func(s *jsObject) {
			s.obj("parallel").set("label", "Review {target.path}")
		})
		m, err := materializeDynamicParallelStep(step, dfOutputs(t, dfTargets), 1, DynamicFanoutConfig{})
		eq(t, err, nil, "materialize")
		var keys []string
		for _, i := range m.Items {
			keys = append(keys, i.Key)
		}
		eq(t, keys, []string{"src/a.ts", "src/b.ts"}, "keys")
		eq(t, dfTexts(m, "task"), []any{"Review src/a.ts", "Review src/b.ts"}, "tasks")
		eq(t, dfTexts(m, "label"), []any{"Review src/a.ts", "Review src/b.ts"}, "labels")
	})
	tw(t, f, "rejects missing structured sources, over-limit arrays, duplicate keys, colliding ids, and bad templates", func(t *testing.T) {
		mat := func(step *jsObject, outputs map[string]ChainOutputEntry) error {
			_, err := materializeDynamicParallelStep(step, outputs, 1, DynamicFanoutConfig{})
			return err
		}
		over := dfBase(t, func(s *jsObject) { s.obj("expand").set("maxItems", float64(1)) })
		dfThrows(t, mat(over, dfOutputs(t, dfTargets)), "exceeding maxItems")
		dfThrows(t, mat(dfBase(t, nil), dfOutputs(t, "")), "requires structured output")
		dfThrows(t, mat(dfBase(t, nil), dfOutputs(t, `{"items":[{"path":"x"},{"path":"x"}]}`)), "duplicate item key")
		dfThrows(t, mat(dfBase(t, nil), dfOutputs(t, `{"items":[{"path":"a/b"},{"path":"a-b"}]}`)), "colliding item id")
		dfThrows(t, mat(dfBase(t, func(s *jsObject) { s.obj("parallel").set("task", "Review {other.path}") }), dfOutputs(t, dfTargets)), "Unsupported template reference")
		dfThrows(t, mat(dfBase(t, func(s *jsObject) { s.obj("parallel").set("task", "Review {target[path]}") }), dfOutputs(t, dfTargets)), "Invalid item reference")
		dfThrows(t, mat(dfBase(t, func(s *jsObject) { s.obj("parallel").set("task", "Review {target.path") }), dfOutputs(t, dfTargets)), "Invalid item reference")
	})
	tw(t, f, "allows config maxItems defaults and handles empty arrays deterministically", func(t *testing.T) {
		base := obj(t, `{"expand":{"from":{"output":"targets","path":"/items"},"item":"target","key":"/path"},"parallel":{"agent":"reviewer","task":"Review {target.path}"},"collect":{"as":"reviews"}}`)
		four := 4.0
		cfg := DynamicFanoutConfig{MaxItems: &four}
		m, err := materializeDynamicParallelStep(base, dfOutputs(t, dfTargets), 1, cfg)
		eq(t, err, nil, "materialize")
		eq(t, len(m.Parallel), 2, "tasks")
		eq(t, validateChainOutputBindings([]*jsObject{obj(t, `{"agent":"scout","task":"Return targets","as":"targets","outputSchema":{"type":"object"}}`), base}, cfg), nil, "bindings")
		empty, err := materializeDynamicParallelStep(base, dfOutputs(t, `{"items":[]}`), 1, cfg)
		eq(t, err, nil, "empty")
		eq(t, len(empty.Parallel), 0, "empty tasks")
		failing := base.clone()
		failing.set("expand", obj(t, `{"from":{"output":"targets","path":"/items"},"item":"target","key":"/path","onEmpty":"fail"}`))
		_, err = materializeDynamicParallelStep(failing, dfOutputs(t, `{"items":[]}`), 1, cfg)
		dfThrows(t, err, "source array is empty")
	})
	tw(t, f, "rejects malformed dynamic-like shapes before they can run as static parallel", func(t *testing.T) {
		malformed := []string{
			`{"expand":{"from":{"output":"targets","path":"/items"},"maxItems":4},"parallel":[{"agent":"reviewer","task":"Review"}],"collect":{"as":"reviews"}}`,
			`{"expand":{"from":{"output":"targets","path":"/items"},"maxItems":4},"parallel":{"agent":"reviewer","task":"Review {item.path}"}}`,
			`{"expand":{"from":{"output":"targets","path":"/items"},"maxItems":4},"parallel":{"agent":"reviewer","task":"Review {item.path}"},"collect":{"as":"reviews"},"when":"later"}`,
			`{"expand":{"from":{"output":"targets","path":"/items"},"maxItems":4},"parallel":{"agent":"reviewer","task":"Review {item.path}","as":"child"},"collect":{"as":"reviews"}}`,
		}
		for _, step := range malformed {
			err := validateChainOutputBindings([]*jsObject{obj(t, `{"agent":"scout","task":"Return targets","as":"targets","outputSchema":{"type":"object"}}`), obj(t, step)}, DynamicFanoutConfig{})
			var ce *ChainOutputValidationError
			if !errors.As(err, &ce) {
				t.Errorf("%s: error = %v, want a ChainOutputValidationError", step, err)
			}
		}
	})
	tw(t, f, "preserves runner-injected parent authority without admitting it in user-facing validation", func(t *testing.T) {
		step := obj(t, `{"expand":{"from":{"output":"targets","path":"/items"},"maxItems":4},"parallel":{"agent":"reviewer","task":"Review {item.path}","parentSessionId":"session-parent","requiredExtensions":[{"id":"provider","path":"/private/provider.mjs"}]},"collect":{"as":"reviews"}}`)
		runner := DynamicFanoutConfig{AllowRunnerFields: true}
		eq(t, validateDynamicStepShape(step, 1, runner), nil, "runner validation")
		m, err := materializeDynamicParallelStep(step, dfOutputs(t, dfTargets), 1, runner)
		eq(t, err, nil, "materialize")
		jsonEq(t, m.Parallel[0].vals["requiredExtensions"], `[{"id":"provider","path":"/private/provider.mjs"}]`)
		err = validateDynamicStepShape(step, 1, DynamicFanoutConfig{})
		var dfe *DynamicFanoutError
		if !errors.As(err, &dfe) || !regexp.MustCompile(`parentSessionId`).MatchString(err.Error()) {
			t.Errorf("user-facing validation = %v", err)
		}
	})
	tw(t, f, "accepts fast and toolBudget on dynamic parallel templates", func(t *testing.T) {
		step := obj(t, `{"expand":{"from":{"output":"targets","path":"/items"},"maxItems":4},"parallel":{"agent":"reviewer","task":"Review {item.path}","fast":true,"toolBudget":{"hard":3}},"collect":{"as":"reviews"}}`)
		eq(t, validateDynamicStepShape(step, 1, DynamicFanoutConfig{}), nil, "validation")
		m, err := materializeDynamicParallelStep(step, dfOutputs(t, dfTargets), 1, DynamicFanoutConfig{})
		eq(t, err, nil, "materialize")
		eq(t, dfTexts(m, "fast"), []any{true, true}, "fast")
	})
	tw(t, f, "validates source ordering and collect name collisions", func(t *testing.T) {
		scout := obj(t, `{"agent":"scout","task":"Return targets","as":"targets","outputSchema":{"type":"object"}}`)
		collide := obj(t, `{"expand":{"from":{"output":"targets","path":"/items"},"maxItems":4},"parallel":{"agent":"reviewer","task":"Review {item.path}"},"collect":{"as":"targets"}}`)
		var ce *ChainOutputValidationError
		if err := validateChainOutputBindings([]*jsObject{scout, collide}, DynamicFanoutConfig{}); !errors.As(err, &ce) {
			t.Errorf("collision = %v", err)
		}
		dfThrows(t, validateChainOutputBindings([]*jsObject{collide}, DynamicFanoutConfig{}), `unknown output 'targets'`)
	})
	tw(t, f, "validates appended steps against prior and reserved output names", func(t *testing.T) {
		ctx := ChainOutputValidationContext{PriorOutputNames: []string{"targets"}, StartStepIndex: 2}
		eq(t, validateChainOutputBindingsWithContext([]*jsObject{obj(t, `{"agent":"worker","task":"Use {outputs.targets}","as":"summary"}`)}, DynamicFanoutConfig{}, ctx), nil, "prior output")
		dfThrows(t, validateChainOutputBindingsWithContext([]*jsObject{obj(t, `{"agent":"worker","task":"Use {outputs.missing}"}`)}, DynamicFanoutConfig{}, ctx), `Unknown chain output reference '\{outputs\.missing\}' at step 3`)
		dfThrows(t, validateChainOutputBindingsWithContext([]*jsObject{obj(t, `{"agent":"worker","task":"Use {previous}","as":"targets"}`)}, DynamicFanoutConfig{}, ctx), `Duplicate chain output name 'targets'`)
		eq(t, validateChainOutputBindingsWithContext([]*jsObject{obj(t, `{"expand":{"from":{"output":"targets","path":"/items"},"maxItems":4},"parallel":{"agent":"reviewer","task":"Review {item.path}"},"collect":{"as":"reviews"}}`)}, DynamicFanoutConfig{}, ctx), nil, "dynamic step")
	})
	tw(t, f, "collects ordered child result records and validates aggregate schema", func(t *testing.T) {
		step := obj(t, `{"expand":{"from":{"output":"targets","path":"/items"},"key":"/path","maxItems":4},"parallel":{"agent":"reviewer","task":"Review {item.path}"},"collect":{"as":"reviews"}}`)
		m, err := materializeDynamicParallelStep(step, dfOutputs(t, dfTargets), 1, DynamicFanoutConfig{})
		eq(t, err, nil, "materialize")
		zero, one := 0, 1
		first := &DynamicChildResult{Agent: "reviewer", ExitCode: &zero, Text: "ok", Structured: obj(t, `{"ok":"a"}`), HasStructured: true}
		timedOut := &DynamicChildResult{Agent: "reviewer", ExitCode: &one, Text: "ok", Structured: obj(t, `{"ok":"b"}`), HasStructured: true, Error: "Subagent timed out after 300ms.", TimedOut: true}
		collected := collectDynamicResults(step, m.Items, []*DynamicChildResult{first, timedOut})
		eq(t, []string{collected[0].Key, collected[1].Key}, []string{"src/a.ts", "src/b.ts"}, "keys")
		jsonEq(t, []any{collected[0].Structured, collected[1].Structured}, `[{"ok":"a"},{"ok":"b"}]`)
		eq(t, collected[1].TimedOut, true, "timedOut")
		eq(t, validateDynamicCollection(obj(t, `{"type":"array","minItems":2}`), collected), nil, "array schema")
		err = validateDynamicCollection(obj(t, `{"type":"object"}`), collected)
		var dfe *DynamicFanoutError
		if !errors.As(err, &dfe) {
			t.Errorf("object schema = %v, want a DynamicFanoutError", err)
		}
	})
}
