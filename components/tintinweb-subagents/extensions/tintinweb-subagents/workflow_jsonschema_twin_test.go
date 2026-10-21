package tintinweb_subagents

import (
	"math"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/workflow-json-schema.test.ts.

func wfFindingsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"findings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file": map[string]any{"type": "string"},
						"line": map[string]any{"type": "integer", "minimum": float64(1)},
					},
					"required": []any{"file"},
				},
			},
		},
		"required": []any{"findings"},
	}
}

func wfCompileOK(t *testing.T, schema any) *workflowCompiledSchema {
	t.Helper()
	r := compileWorkflowSchema(schema)
	if !r.OK {
		t.Fatalf("expected a compiled schema, got: %s", r.Message)
	}
	return r.Compiled
}

func TestWorkflowJSONSchema(t *testing.T) {
	const f = "workflow-json-schema"
	// compiling a script-supplied schema
	tw(t, f, "accepts a plain JSON Schema, with no TypeBox ceremony", func(t *testing.T) {
		eq(t, compileWorkflowSchema(wfFindingsSchema()).OK, true)
	})
	tw(t, f, "keeps the schema object as given, for the tool and the journal key", func(t *testing.T) {
		s := wfFindingsSchema()
		c := wfCompileOK(t, s)
		if wfReflectPointer(c.Schema) != wfReflectPointer(s) {
			t.Fatal("the schema was copied")
		}
	})
	tw(t, f, "refuses anything that cannot be a tool's input schema", func(t *testing.T) {
		for _, bad := range []any{float64(5), "x", nil, []any{}, map[string]any{"type": "array"}, map[string]any{"type": "string"}} {
			if compileWorkflowSchema(bad).OK {
				t.Fatalf("%#v should not compile", bad)
			}
		}
	})
	tw(t, f, "says why a non-object root is refused", func(t *testing.T) {
		r := compileWorkflowSchema(map[string]any{"type": "array"})
		eq(t, r.OK, false)
		if !strings.Contains(r.Message, `type: "object"`) {
			t.Fatal(r.Message)
		}
	})
	tw(t, f, "refuses a schema too large to be worth sending", func(t *testing.T) {
		huge := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string", "description": strings.Repeat("x", 70_000)}}}
		r := compileWorkflowSchema(huge)
		eq(t, r.OK, false)
		if !strings.Contains(r.Message, "too large") {
			t.Fatal(r.Message)
		}
	})
	tw(t, f, "refuses a schema it cannot walk, rather than failing at the first call", func(t *testing.T) {
		r := compileWorkflowSchema(map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "nonsense"}}})
		if r.OK {
			r.Compiled.Check(map[string]any{"a": float64(1)}) // must not panic
		} else if !regexp.MustCompile(`agent\(\) opts\.schema`).MatchString(r.Message) {
			t.Fatal(r.Message)
		}
	})
	// checking a payload
	tw(t, f, "passes a conforming value", func(t *testing.T) {
		eq(t, wfCompileOK(t, wfFindingsSchema()).Check(map[string]any{"findings": []any{map[string]any{"file": "a.ts", "line": float64(3)}}}), "")
	})
	tw(t, f, "passes when an optional field is absent", func(t *testing.T) {
		eq(t, wfCompileOK(t, wfFindingsSchema()).Check(map[string]any{"findings": []any{map[string]any{"file": "a.ts"}}}), "")
	})
	tw(t, f, "names the missing property at the root", func(t *testing.T) {
		m := wfCompileOK(t, wfFindingsSchema()).Check(map[string]any{})
		if m == "" || !strings.Contains(m, "findings") || !strings.Contains(m, "$:") {
			t.Fatal(m)
		}
	})
	tw(t, f, "names the offending path in JavaScript notation, not JSON Pointer", func(t *testing.T) {
		m := wfCompileOK(t, wfFindingsSchema()).Check(map[string]any{"findings": []any{map[string]any{"file": "a.ts", "line": float64(0)}}})
		if !strings.Contains(m, "$.findings.0.line") || strings.Contains(m, "/findings/") {
			t.Fatal(m)
		}
	})
	tw(t, f, "names every missing property, so one retry can fix them all", func(t *testing.T) {
		schema := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}, "b": map[string]any{"type": "string"}, "c": map[string]any{"type": "string"}},
			"required": []any{"a", "b", "c"}}
		m := wfCompileOK(t, schema).Check(map[string]any{})
		for _, missing := range []string{"a", "b", "c"} {
			if !strings.Contains(m, missing) {
				t.Fatal(m)
			}
		}
	})
	tw(t, f, "reports distinct problems separately, each with its own path", func(t *testing.T) {
		schema := map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer", "minimum": float64(1)}, "s": map[string]any{"type": "string"}},
			"required": []any{"s"}}
		m := wfCompileOK(t, schema).Check(map[string]any{"n": float64(0)})
		if !strings.Contains(m, "$: ") || !strings.Contains(m, "$.n: ") || len(strings.Split(m, "; ")) != 2 {
			t.Fatal(m)
		}
	})
	tw(t, f, "rejects a value of the wrong type entirely", func(t *testing.T) {
		c := wfCompileOK(t, wfFindingsSchema())
		if c.Check("not an object") == "" || c.Check(nil) == "" {
			t.Fatal("accepted")
		}
	})
	tw(t, f, "never throws, whatever it is handed", func(t *testing.T) {
		c := wfCompileOK(t, wfFindingsSchema())
		cyclic := map[string]any{"findings": []any{}}
		cyclic["self"] = cyclic
		for _, v := range []any{nil, math.NaN(), cyclic, map[any]any{}, func() {}, make(chan int)} {
			c.Check(v)
		}
	})
}

// wfReflectPointer identifies a map by identity.
func wfReflectPointer(m map[string]any) uintptr { return reflect.ValueOf(m).Pointer() }
