package pi_subagents

import (
	"strings"
	"testing"
)

func invalidMessage(t *testing.T, schema, value string) string {
	t.Helper()
	ok, msg := validateStructuredOutputValue(js(t, schema), js(t, value))
	if ok {
		t.Fatalf("valid: %s <- %s", schema, value)
	}
	return msg
}

func TestStructuredOutputValidation(t *testing.T) {
	cond := `{"properties":{"kind":{"const":"full"}},"required":["kind"]}`
	en := `{"properties":{"enabled":{"const":true}},"required":["enabled"]}`
	tw(t, "structured-output-validation", "reports the missing field selected by a root then branch", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"object","properties":{"kind":{"type":"string"}},"if":`+cond+`,"then":{"required":["detail"]}}`, `{"kind":"full"}`), "detail: is required", "message")
	})
	tw(t, "structured-output-validation", "reports an invalid field selected by an else branch", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"object","properties":{"kind":{"type":"string"},"count":{}},"if":`+cond+`,"else":{"properties":{"count":{"type":"string"}}}}`, `{"kind":"brief","count":2}`), "count: must be string", "message")
	})
	tw(t, "structured-output-validation", "roots selected-branch errors for a conditional nested under a property", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"object","properties":{"config":{"type":"object","properties":{"enabled":{"type":"boolean"}},"if":`+en+`,"then":{"required":["token"]}}}}`, `{"config":{"enabled":true}}`), "config.token: is required", "message")
	})
	tw(t, "structured-output-validation", "preserves root definitions for refs in the selected branch and at the root", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"$defs":{"count":{"type":"number"}},"type":"object","properties":{"enabled":{"type":"boolean"},"count":{}},"if":`+en+`,"then":{"properties":{"count":{"$ref":"#/$defs/count"}}}}`, `{"enabled":true,"count":"many"}`), "count: must be number", "branch ref")
		equal(t, invalidMessage(t, `{"$ref":"#/$defs/conditional","$defs":{"conditional":{"type":"object","properties":{"enabled":{"type":"boolean"}},"if":`+en+`,"then":{"required":["token"]}}}}`, `{"enabled":true}`), "token: is required", "root ref")
	})
	c := `{"type":"object","properties":{"enabled":{"type":"boolean"}},"if":` + en + `,"then":{"required":["token"]}}`
	tw(t, "structured-output-validation", "roots a conditional reached through a nested property ref", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"object","properties":{"config":{"$ref":"#/$defs/C"}},"$defs":{"C":`+c+`}}`, `{"config":{"enabled":true}}`), "config.token: is required", "message")
	})
	tw(t, "structured-output-validation", "expands a conditional inside a tuple element", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"array","prefixItems":[{"type":"object","if":`+en+`,"then":{"required":["token"]}}]}`, `[{"enabled":true}]`), "0.token: is required", "message")
	})
	tw(t, "structured-output-validation", "treats a numeric object property as a property path", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"object","properties":{"0":{"type":"object","if":`+en+`,"then":{"required":["token"]}}}}`, `{"0":{"enabled":true}}`), "0.token: is required", "message")
	})
	tw(t, "structured-output-validation", "expands reused and recursive refs only at the failing instance", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"object","properties":{"left":{"$ref":"#/$defs/C"},"right":{"$ref":"#/$defs/C"}},"$defs":{"C":`+c+`}}`, `{"left":{"enabled":false},"right":{"enabled":true}}`), "right.token: is required", "reused")
		equal(t, invalidMessage(t, `{"$ref":"#/$defs/Node","$defs":{"Node":{"type":"object","properties":{"enabled":{"type":"boolean"},"next":{"$ref":"#/$defs/Node"}},"if":`+en+`,"then":{"required":["token"]}}}}`, `{"enabled":false,"next":{"enabled":true}}`), "next.token: is required", "recursive")
	})
	tw(t, "structured-output-validation", "emits an unrelated root baseline error once before a nested conditional leaf", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"object","required":["name"],"properties":{"config":{"type":"object","if":`+en+`,"then":{"required":["token"]}}}}`, `{"config":{"enabled":true}}`), "name: is required; config.token: is required", "message")
	})
	tw(t, "structured-output-validation", "expands nested selected conditionals until reaching an actionable field", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"object","if":{"properties":{"mode":{"const":"configured"}},"required":["mode"]},"then":{"if":`+en+`,"then":{"required":["token"]}}}`, `{"mode":"configured","enabled":true}`), "token: is required", "message")
	})
	tw(t, "structured-output-validation", "uses the selected branch's own diagnostic when no field path is available", func(t *testing.T) {
		equal(t, invalidMessage(t, `{"type":"object","if":{"const":{}},"then":false}`, `{}`), "root: schema is false", "message")
	})
	tw(t, "structured-output-validation", "keeps selected-branch diagnostics deterministic and bounded to eight", func(t *testing.T) {
		var required, all, value []string
		for i := 0; i < 10; i++ {
			required = append(required, `"field`+string(rune('0'+i))+`"`)
		}
		for i := 0; i < 20; i++ {
			n := itoa(i)
			all = append(all, `{"if":{"properties":{"later`+n+`":{"const":true}},"required":["later`+n+`"]},"then":{"required":["detail`+n+`"]}}`)
			value = append(value, `"later`+n+`":true`)
		}
		msg := invalidMessage(t, `{"type":"object","required":[`+strings.Join(required, ",")+`],"allOf":[`+strings.Join(all, ",")+`]}`, `{`+strings.Join(value, ",")+`}`)
		var want []string
		for i := 0; i < 8; i++ {
			want = append(want, "field"+itoa(i)+": is required")
		}
		equal(t, msg, strings.Join(want, "; "), "message")
	})
}

func itoa(i int) string {
	return jsNumber(float64(i))
}

func TestStructuredOutputRejection(t *testing.T) {
	msgs := func(t *testing.T, vs ...string) []any {
		var out []any
		for _, v := range vs {
			out = append(out, js(t, v))
		}
		return out
	}
	tw(t, "structured-output-rejection", "uses the latest failed result and correlates results without names by toolCallId", func(t *testing.T) {
		got := formatStructuredOutputRejectionError(msgs(t,
			`{"role":"assistant","content":[{"type":"toolCall","id":"structured-1","name":"structured_output","arguments":{"value":{}}}]}`,
			`{"role":"toolResult","toolCallId":"structured-1","isError":true,"content":[{"type":"text","text":"Structured output validation failed: first: is required"}]}`,
			`{"role":"toolResult","toolName":"structured_output","toolCallId":"structured-2","isError":true,"content":[{"type":"text","text":"Structured output validation failed: second: is required"}]}`))
		equal(t, got, "Structured output validation failed: second: is required", "message")
	})
	tw(t, "structured-output-rejection", "ignores unrelated failures and returns a truthful fallback", func(t *testing.T) {
		got := formatStructuredOutputRejectionError(msgs(t, `{"role":"toolResult","toolName":"read","isError":true,"content":[{"type":"text","text":"EISDIR"}]}`))
		equal(t, got, structuredOutputRejectionError, "message")
	})
	tw(t, "structured-output-rejection", "does not expose schema compiler diagnostics", func(t *testing.T) {
		sentinel := "PRIVATE_SCHEMA_SENTINEL"
		got := formatStructuredOutputRejectionError(msgs(t, `{"role":"toolResult","toolName":"structured_output","isError":true,"content":[{"type":"text","text":"Structured output validation failed: invalid outputSchema: Invalid regular expression: /`+sentinel+`_[invalid/u"}]}`))
		equal(t, got, invalidStructuredOutputSchemaError, "message")
		equal(t, strings.Contains(got, sentinel), false, "sentinel")
	})
	tw(t, "structured-output-rejection", "categorizes unavailable validation without exposing setup details", func(t *testing.T) {
		sentinel := "PRIVATE_SETUP_SENTINEL"
		got := formatStructuredOutputRejectionError(msgs(t, `{"role":"toolResult","toolName":"structured_output","isError":true,"content":[{"type":"text","text":"Cannot load typebox/compile for structured output validation (direct import failed: `+sentinel+` at /private/compiler.ts)"}]}`))
		equal(t, got, structuredOutputValidatorUnavailableErr, "message")
		equal(t, strings.Contains(got, sentinel), false, "sentinel")
	})
	tw(t, "structured-output-rejection", "redacts payload and stack details and clamps complete UTF-8 characters", func(t *testing.T) {
		secret := "do-not-leak"
		oversized := "Structured output validation failed: " + strings.Repeat("界", 2000) + "\nsubmitted value: " + secret + "\n    at /private/project/file.ts:1:1"
		o := newObject()
		o.set("role", "toolResult")
		o.set("toolName", "structured_output")
		o.set("isError", true)
		part := newObject()
		part.set("type", "text")
		part.set("text", oversized)
		o.set("content", []any{part})
		got := formatStructuredOutputRejectionError([]any{o})
		if len(got) > maxStructuredOutputRejectionErrorBytes {
			t.Errorf("%d bytes", len(got))
		}
		equal(t, strings.Contains(got, "\uFFFD"), false, "replacement character")
		equal(t, strings.Contains(got, secret), false, "secret")
		equal(t, strings.Contains(got, "/private/project"), false, "stack")
	})
}

func TestStructuredOutputToolParameters(t *testing.T) {
	schema := js(t, `{"type":"object","properties":{"a":{"$ref":"#/$defs/x"},"b":{"$ref":"#"}},"$defs":{"x":{"type":"string","properties":{"n":{"$ref":"#/$defs/x"}}}}}`)
	p := createStructuredOutputToolParameters(schema, "required")
	got := marshalJSON(p, "")
	want := `{"type":"object","properties":{"value":{"type":"object","properties":{"a":{"$ref":"#/properties/value/$defs/x"},"b":{"$ref":"#/properties/value"}},"$defs":{"x":{"type":"string","properties":{"n":{"$ref":"#/properties/value/$defs/x"}}}}},"acceptanceReport":{"type":"object"}},"required":["value","acceptanceReport"],"additionalProperties":false}`
	equal(t, got, want, "parameters")
}
