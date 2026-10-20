package tintinweb_subagents

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of structured-output.test.ts. The original's tool.execute(id, params) is the Execute of the definition.

var structuredSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"file": map[string]any{"type": "string"},
		"line": map[string]any{"type": "integer", "minimum": float64(1)},
	},
	"required": []any{"file"},
}

func buildStructured(t *testing.T) (sdk.ToolDefinition, *structuredCapture) {
	t.Helper()
	compiled, problem := compileJSONSchema(structuredSchema)
	if compiled == nil {
		t.Fatal(problem)
	}
	capture := &structuredCapture{}
	return structuredOutputTool(compiled, capture), capture
}

func callStructured(t *testing.T, tool sdk.ToolDefinition, params map[string]any) sdk.ToolResult {
	t.Helper()
	out, err := tool.Execute(sdk.Context{}, params)
	eq(t, err, nil)
	return out.(sdk.ToolResult)
}

func parsed(t *testing.T, s string) any {
	t.Helper()
	var v any
	must(t, json.Unmarshal([]byte(s), &v))
	return v
}

func TestStructuredOutputToolTwins(t *testing.T) {
	tw(t, "structured-output", "carries Claude Code's name, so a ported prompt stays true", func(t *testing.T) {
		tool, _ := buildStructured(t)
		eq(t, structuredOutputToolName, "StructuredOutput")
		eq(t, tool.Name, "StructuredOutput")
	})
	tw(t, "structured-output", "uses the caller's schema as its own parameters, verbatim", func(t *testing.T) {
		tool, _ := buildStructured(t)
		eq(t, map[string]any(tool.Parameters), structuredSchema)
	})
	tw(t, "structured-output", "asks the provider to constrain sampling, but does not require it", func(t *testing.T) {
		tool, _ := buildStructured(t)
		eq(t, tool.ConstrainedSampling, sdk.ToolConstrainedSampling(sdk.ConstrainedSampling{Type: "json_schema", Strict: "prefer"}))
	})
	tw(t, "structured-output", "captures a conforming payload as canonical JSON", func(t *testing.T) {
		tool, capture := buildStructured(t)
		res := callStructured(t, tool, map[string]any{"file": "a.ts", "line": float64(3)})
		eq(t, res.IsError, false)
		eq(t, capture.Called, true)
		eq(t, parsed(t, capture.JSON), map[string]any{"file": "a.ts", "line": float64(3)})
		eq(t, capture.HasError, false)
	})
	tw(t, "structured-output", "answers a bad payload in-band, so the model can correct itself", func(t *testing.T) {
		tool, capture := buildStructured(t)
		res := callStructured(t, tool, map[string]any{"line": float64(0)})
		eq(t, res.IsError, true)
		eq(t, strings.Contains(res.Content, "did not match the required schema"), true)
		eq(t, strings.Contains(res.Content, "file"), true) // the reason has to reach the model, or the retry is a guess
		eq(t, capture.JSON, "")
		eq(t, capture.Called, true)
		eq(t, capture.HasError, true)
	})
	tw(t, "structured-output", "lets a corrected second call win", func(t *testing.T) {
		tool, capture := buildStructured(t)
		callStructured(t, tool, map[string]any{"line": float64(0)})
		callStructured(t, tool, map[string]any{"file": "a.ts"})
		eq(t, parsed(t, capture.JSON), map[string]any{"file": "a.ts"})
		eq(t, capture.HasError, false) // cleared, or the retry prompt would report a problem already fixed
	})
	tw(t, "structured-output", "takes the last valid call when a model answers twice", func(t *testing.T) {
		tool, capture := buildStructured(t)
		callStructured(t, tool, map[string]any{"file": "first.ts"})
		callStructured(t, tool, map[string]any{"file": "second.ts"})
		eq(t, parsed(t, capture.JSON), map[string]any{"file": "second.ts"})
	})
	tw(t, "structured-output", "recovers a payload sent as a JSON string", func(t *testing.T) {
		tool, capture := buildStructured(t)
		prepared := prepareStructuredArguments(`{"file":"a.ts"}`)
		callStructured(t, tool, prepared.(map[string]any))
		eq(t, parsed(t, capture.JSON), map[string]any{"file": "a.ts"})
	})
	tw(t, "structured-output", "leaves an unparseable string alone for validation to reject", func(t *testing.T) {
		eq(t, prepareStructuredArguments("not json at all"), any("not json at all"))
	})
}

func TestStructuredRetryPromptTwins(t *testing.T) {
	tw(t, "structured-output", "distinguishes never answering from answering wrongly", func(t *testing.T) {
		silent := &structuredCapture{}
		eq(t, strings.Contains(strings.ToLower(structuredRetryPrompt(silent)), "did not call"), true)
		wrong := &structuredCapture{Called: true, HasError: true, LastError: "$: must have required properties file"}
		prompt := structuredRetryPrompt(wrong)
		eq(t, strings.Contains(prompt, "did not match the required schema"), true)
		// Telling a model it got the shape wrong when it never answered would send it hunting for a mistake it did not make.
		eq(t, strings.Contains(strings.ToLower(prompt), "did not call"), false)
		eq(t, strings.Contains(prompt, "must have required properties file"), true)
	})
	tw(t, "structured-output", "always ends by asking for the call", func(t *testing.T) {
		for _, c := range []*structuredCapture{{}, {Called: true, HasError: true, LastError: "x"}} {
			eq(t, strings.Contains(structuredRetryPrompt(c), "Call StructuredOutput now"), true)
		}
	})
}

// The validator is this port's own (the original's is typebox); these pin the messages the model reads and the
// path notation, as workflow-json-schema.test.ts does for the original's.
func TestJSONSchemaValidator(t *testing.T) {
	findings := map[string]any{"type": "object", "properties": map[string]any{
		"findings": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []any{"file"},
			"properties": map[string]any{"file": map[string]any{"type": "string"}, "line": map[string]any{"type": "integer", "minimum": float64(1)}}}}},
		"required": []any{"findings"}}
	c, problem := compileJSONSchema(findings)
	eq(t, problem, "")
	if msg, ok := c.check(map[string]any{"findings": []any{map[string]any{"file": "a.ts", "line": float64(3)}}}); !ok {
		t.Fatal(msg)
	}
	msg, ok := c.check(map[string]any{})
	eq(t, ok, false)
	eq(t, msg, "$: must have required properties findings")
	msg, _ = c.check(map[string]any{"findings": []any{map[string]any{"file": "a.ts", "line": float64(0)}}})
	eq(t, msg, "$.findings.0.line: must be >= 1")
	_, ok = c.check("not an object")
	eq(t, ok, false)
	_, ok = c.check(nil)
	eq(t, ok, false)
	for _, bad := range []any{nil, "x", []any{}, map[string]any{"type": "string"}, map[string]any{}} {
		got, p := compileJSONSchema(bad)
		eq(t, got == nil && p != "", true)
	}
	_, p := compileJSONSchema(map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"$ref": "#/nope"}}})
	eq(t, strings.HasPrefix(p, "agent() opts.schema is not a schema this runtime can validate"), true)
	_, p = compileJSONSchema(map[string]any{"type": "object", "description": strings.Repeat("x", 70000)})
	eq(t, strings.Contains(p, "too large"), true)
	// Every missing property is named; distinct problems are reported separately, each with its path.
	c, _ = compileJSONSchema(map[string]any{"type": "object", "required": []any{"a", "b", "c"}, "properties": map[string]any{"n": map[string]any{"type": "number"}}})
	msg, _ = c.check(map[string]any{"n": "x"})
	eq(t, msg, "$: must have required properties a, b, c; $.n: must be number")
}
