package tintinweb_subagents

import (
	"encoding/json"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The synthetic `StructuredOutput` tool behind a schema-bound child: the caller's schema is the tool's input schema,
// so the provider fills the fields, and what validated is the agent's result. The name matches Claude Code's.
// upstream: src/structured-output.ts.

// structuredOutputToolName is deliberately not an excluded or built-in tool name. upstream: STRUCTURED_OUTPUT_TOOL_NAME.
const structuredOutputToolName = "StructuredOutput"

// structuredSchemaEnv carries the caller's schema (JSON) to a child, which registers the tool when it is set.
const structuredSchemaEnv = "TINTINWEB_SUBAGENT_STRUCTURED_SCHEMA"

// structuredCapture is what the child produced, filled in as the tool is called. upstream: StructuredCapture.
type structuredCapture struct {
	mu        sync.Mutex
	JSON      string // the last payload that validated, canonicalised ("" until one does)
	LastError string // why the most recent attempt was rejected, for the retry prompt
	HasError  bool   // LastError is set (an empty reason is still a rejection)
	Called    bool
}

// structuredToolDescription and friends are the original's texts, verbatim.
const (
	structuredToolDescription = "Report your final answer. Call this exactly once, with the complete result, and put everything the " +
		"caller needs inside the arguments — text written outside this call is discarded. If a call is " +
		"rejected for not matching the schema, fix the reported fields and call it again."
	structuredToolSnippet   = "Report your final answer as structured data"
	structuredToolGuideline = "Your final answer MUST be reported by calling StructuredOutput. Prose outside that call is discarded."
)

// prepareStructuredArguments recovers a payload a model sent as one JSON string instead of an object.
// upstream: createStructuredOutputTool prepareArguments.
func prepareStructuredArguments(args any) any {
	s, ok := args.(string)
	if !ok {
		return args
	}
	var out any
	if json.Unmarshal([]byte(s), &out) != nil {
		return args
	}
	return out
}

// execute is the tool's body: validate, record the last valid call, answer a bad payload in-band so the model can
// correct itself inside the run. upstream: createStructuredOutputTool execute.
func (c *structuredCapture) execute(compiled *compiledSchema, params any) (text string, isError bool, canonical string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Called = true
	if verdict, ok := compiled.check(params); !ok {
		c.LastError, c.HasError = verdict, true
		return "StructuredOutput did not match the required schema:\n" + verdict + "\nCall it again with a corrected value.", true, ""
	}
	// Last valid call wins: a model that calls twice meant the second one.
	canonical = canonicalJSON(params)
	c.JSON = canonical
	c.LastError, c.HasError = "", false
	return "Recorded.", false, canonical
}

// canonicalJSON is the payload as JSON (object keys sorted: a Go map has no insertion order, JSON.stringify keeps it).
func canonicalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// structuredRetryPrompt is the one extra prompt sent when a run ended with nothing captured; it tells "never called
// it" from "called it wrongly". upstream: structuredRetryPrompt.
func structuredRetryPrompt(c *structuredCapture) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	reason := "You did not call " + structuredOutputToolName + ", so your answer was not recorded."
	if c.Called && c.HasError {
		reason = "Your last " + structuredOutputToolName + " call did not match the required schema: " + c.LastError
	}
	return reason + "\n\nCall " + structuredOutputToolName + " now with your complete final answer. Do not reply with prose."
}

// structuredFailure is what a child asked for structured output that never gave any reports. upstream: runAgent.
func structuredFailure(c *structuredCapture) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.JSON != "" {
		return ""
	}
	if c.HasError {
		return "The agent's StructuredOutput call did not match the required schema: " + c.LastError
	}
	return "The agent did not report its answer through StructuredOutput."
}

// structuredOutputTool builds the tool for one child: the caller's schema is its parameters verbatim; sampling is
// constrained when the provider can, not required. upstream: createStructuredOutputTool.
func structuredOutputTool(compiled *compiledSchema, capture *structuredCapture) sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name: structuredOutputToolName, Label: "Structured Output", Description: structuredToolDescription,
		PromptSnippet:    structuredToolSnippet,
		PromptGuidelines: []string{structuredToolGuideline},
		Parameters:       sdk.Schema(compiled.Schema),
		// "prefer", not "require": a provider that cannot constrain sampling falls through to validate-and-retry.
		ConstrainedSampling: sdk.ConstrainedSampling{Type: "json_schema", Strict: "prefer"},
		Execute: func(_ sdk.Context, p map[string]any) (any, error) {
			text, isError, canonical := capture.execute(compiled, p)
			return sdk.ToolResult{Content: text, IsError: isError, Details: map[string]any{"json": canonical, "error": isError}}, nil
		},
	}
}

// registerStructuredOutput registers the tool in a child that was given a schema (a schema that does not compile
// registers nothing: the parent compiled it first).
func registerStructuredOutput(e *sdk.Extension, schemaJSON string) {
	var raw any
	if json.Unmarshal([]byte(schemaJSON), &raw) != nil {
		return
	}
	if compiled, _ := compileJSONSchema(raw); compiled != nil {
		e.RegisterTool(structuredOutputTool(compiled, &structuredCapture{}))
	}
}
