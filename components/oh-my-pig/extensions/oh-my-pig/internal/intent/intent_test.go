package intent

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

func resetAllForTest(t *testing.T) {
	t.Helper()
	origEnabled := Enabled
	origConfigured := Configured
	origParent := ParentSession
	ResetForTests()
	t.Cleanup(func() {
		Enabled = origEnabled
		Configured = origConfigured
		ParentSession = origParent
		ResetForTests()
	})
}

// extractWireProperties extracts the raw JSON object of `"properties"` from a
// wire schema so callers can check exact JSON key ordering.
func extractWireProperties(t *testing.T, rawSchema json.RawMessage) json.RawMessage {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(rawSchema, &top); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := top["properties"]
	if !ok {
		t.Fatalf("schema has no properties: %s", string(rawSchema))
	}
	return props
}

// upstream: packages/agent/test/agent-loop.test.ts: "injects and strips intent when intent tracing is enabled"
// upstream: packages/agent/test/append-only-context.test.ts: "injects required `i` into tool schemas when intentTracing is true"
func TestWireSchemaInjectsIntentFirst(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_DESC", "")
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	// Plain Go map schema (keys "z_last", "a_first" would normally serialize alphabetically
	// as "a_first", "i", "z_last" if left as a Go map — OrderedObject must put "i" FIRST).
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "read",
		Label:       "Read",
		Description: "Read files",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"a_first": map[string]any{"type": "string"},
				"z_last":  map[string]any{"type": "string"},
			},
			"required":             []string{"a_first"},
			"additionalProperties": false,
		},
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	}))

	// Ordered schema (as internal/lsp registers with sdk.Type.Object + sdk.NewOrderedObject).
	orderedProps := sdk.NewOrderedObject(
		"action", sdk.NewOrderedObject("type", "string"),
		"file", sdk.NewOrderedObject("type", "string"),
	)
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "lsp",
		Label:       "LSP",
		Description: "LSP query",
		Parameters:  sdk.Type.Object(orderedProps, sdk.NewOrderedObject("additionalProperties", false)).Schema(),
		Execute:     func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	}))

	// Optional mode tool (`wait`).
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "wait",
		Label:       "Wait",
		Description: "Wait for jobs",
		Parameters: sdk.Schema{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	}))

	h := startFakeHost(t, ext, "sess-wire-1")

	// 1. Plain map schema: `i` MUST be the very first key on the wire, BEFORE "a_first".
	readTool := h.tool("read")
	readPropKeys := jsonObjectKeys(t, extractWireProperties(t, readTool.Parameters))
	if len(readPropKeys) == 0 || readPropKeys[0] != Field {
		t.Fatalf("read wire properties order = %v, want %q first (raw=%s)", readPropKeys, Field, string(readTool.Parameters))
	}
	if !readTool.PreparesArguments {
		t.Fatalf("read must register prepares_arguments=true so missing/malformed i is handled before host validation")
	}
	var readSchema map[string]any
	_ = json.Unmarshal(readTool.Parameters, &readSchema)
	req := extractStringSlice(readSchema["required"])
	if !slices.Contains(req, Field) || !slices.Contains(req, "a_first") {
		t.Fatalf("read required = %v, want both a_first and i", req)
	}
	iProp := readSchema["properties"].(map[string]any)[Field].(map[string]any)
	if iProp["type"] != "string" || iProp["description"] != DefaultDescription {
		t.Fatalf("read i property = %+v, want string with %q", iProp, DefaultDescription)
	}

	// 2. Ordered schema: `i` first, then original authored order ("action", "file").
	lspTool := h.tool("lsp")
	lspPropKeys := jsonObjectKeys(t, extractWireProperties(t, lspTool.Parameters))
	wantLspOrder := []string{Field, "action", "file"}
	if !slices.Equal(lspPropKeys, wantLspOrder) {
		t.Fatalf("lsp wire properties order = %v, want %v (raw=%s)", lspPropKeys, wantLspOrder, string(lspTool.Parameters))
	}

	// 3. Optional mode (`wait`): `i` injected first, NOT in required.
	waitTool := h.tool("wait")
	waitPropKeys := jsonObjectKeys(t, extractWireProperties(t, waitTool.Parameters))
	if !slices.Equal(waitPropKeys, []string{Field}) {
		t.Fatalf("wait wire properties = %v, want [i]", waitPropKeys)
	}
	var waitSchema map[string]any
	_ = json.Unmarshal(waitTool.Parameters, &waitSchema)
	if slices.Contains(extractStringSlice(waitSchema["required"]), Field) {
		t.Fatalf("wait (optional) should not require %q; required=%v", Field, waitSchema["required"])
	}
}

// upstream: packages/agent/src/agent-loop.ts:1126-1128, 3202-3226
// Missing `i`, non-string `i` (object, array, number, null, bool), or empty `i: ""`
// must ALL pass host validation on a `require` tool without coercing `null`/`5`
// into `"null"`/`"5"` and without recording an intent.
func TestMissingMalformedAndEmptyIntentOnRequireTool(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	var lastArgs map[string]any
	var lastIntent string
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "read",
		Label:       "Read",
		Description: "Read files",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			lastArgs = params
			lastIntent = For(ctx, ctx.ToolCallID())
			return "ok", nil
		},
	}))

	var lastEventIntent string
	var lastWorkingLabel string
	ext.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		lastEventIntent = FromEvent(ctx, data)
		lastWorkingLabel = WorkingLabelFromEvent(ctx, data)
		return nil, nil
	})

	h := startFakeHost(t, ext, "sess-malformed")

	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing i (e.g. eval/toolcmd caller)", map[string]any{"path": "a.go"}},
		{"empty string i", map[string]any{"path": "a.go", "i": ""}},
		{"whitespace-only i", map[string]any{"path": "a.go", "i": "   ...  "}},
		{"null i (must not coerce to \"null\")", map[string]any{"path": "a.go", "i": nil}},
		{"number i (must not coerce to \"5\")", map[string]any{"path": "a.go", "i": 5}},
		{"bool i (must not coerce to \"true\")", map[string]any{"path": "a.go", "i": true}},
		{"object i (must not fail type check)", map[string]any{"path": "a.go", "i": map[string]any{"nested": 1}}},
		{"array i (must not fail type check)", map[string]any{"path": "a.go", "i": []any{"x"}}},
	}

	for idx, tc := range cases {
		callID := fmt.Sprintf("tc-mal-%d", idx)
		lastArgs = nil
		lastIntent = "sentinel"
		lastEventIntent = "sentinel"
		lastWorkingLabel = "sentinel"

		h.emitEvent(sdk.EventToolExecutionStart, map[string]any{
			"toolCallId": callID,
			"toolName":   "read",
			"args":       tc.args,
		})
		if lastEventIntent != "" {
			t.Errorf("%s: FromEvent = %q, want empty", tc.name, lastEventIntent)
		}
		if lastWorkingLabel != "" {
			t.Errorf("%s: WorkingLabelFromEvent = %q, want empty", tc.name, lastWorkingLabel)
		}

		if _, err := h.callTool("read", callID, tc.args); err != nil {
			t.Fatalf("%s: callTool rejected call: %v", tc.name, err)
		}
		if _, hasI := lastArgs[Field]; hasI {
			t.Errorf("%s: Execute saw %q in params: %+v", tc.name, Field, lastArgs)
		}
		if lastArgs["path"] != "a.go" {
			t.Errorf("%s: Execute path = %v, want a.go", tc.name, lastArgs["path"])
		}
		if lastIntent != "" {
			t.Errorf("%s: For(ctx, callID) = %q, want empty", tc.name, lastIntent)
		}
	}
}

// upstream: packages/agent/src/agent-loop.ts:3202-3226
// A stray `i` sent to closed omit/function-intent tools (`resolve`, `reject`,
// `checkpoint`, `rewind`, `propose`, `goal`) must be stripped BEFORE PiG's
// `additionalProperties: false` validator runs, while still surfacing in
// `FromEvent` / `WorkingLabelFromEvent`.
func TestStrayIntentOnClosedOmitToolsStrippedBeforeValidation(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	receivedByTool := map[string]map[string]any{}
	closedOmitTools := []string{"resolve", "reject", "checkpoint", "rewind", "propose", "goal"}

	for _, name := range closedOmitTools {
		toolName := name
		ext.RegisterTool(Tool(sdk.ToolDefinition{
			Name:        toolName,
			Label:       toolName,
			Description: "closed omit tool",
			Parameters: sdk.Schema{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{"type": "string"},
				},
				"required":             []string{"reason"},
				"additionalProperties": false,
			},
			Execute: func(_ sdk.Context, params map[string]any) (any, error) {
				receivedByTool[toolName] = params
				return "ok", nil
			},
		}))
	}

	var eventIntent string
	ext.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		eventIntent = WorkingLabelFromEvent(ctx, data)
		return nil, nil
	})

	h := startFakeHost(t, ext, "sess-stray")

	for i, name := range closedOmitTools {
		tl := h.tool(name)
		if !tl.PreparesArguments {
			t.Fatalf("%s: expected prepares_arguments=true to strip stray i before validation", name)
		}
		rawArgs := map[string]any{
			"i":      "Applying staged change: detail",
			"reason": "looks good",
		}
		h.emitEvent(sdk.EventToolExecutionStart, map[string]any{
			"toolCallId": fmt.Sprintf("stray-%d", i),
			"toolName":   name,
			"args":       rawArgs,
		})
		if eventIntent != "Applying staged change" {
			t.Errorf("%s: WorkingLabelFromEvent = %q, want %q", name, eventIntent, "Applying staged change")
		}
		if _, err := h.callTool(name, fmt.Sprintf("stray-%d", i), rawArgs); err != nil {
			t.Fatalf("%s: stray i rejected by host validator: %v", name, err)
		}
		got := receivedByTool[name]
		if _, hasI := got[Field]; hasI {
			t.Errorf("%s: Execute saw stray i: %+v", name, got)
		}
		if got["reason"] != "looks good" {
			t.Errorf("%s: Execute reason = %v, want looks good", name, got["reason"])
		}
	}
}

// upstream: packages/agent/test/normalize-tools-prune.test.ts:
//   - "injects the intent field into each anyOf branch"
//   - "does not add root-sibling properties next to a pure anyOf"
func TestPureUnionRootInjectsPerBranch(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_DESC", "")
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	var gotArgs map[string]any
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "discriminated",
		Label:       "Discriminated",
		Description: "Union root tool",
		Parameters: sdk.Schema{
			"anyOf": []any{
				map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{"const": "open"},
						"alpha":  map[string]any{"type": "string"},
					},
					"required":             []string{"action"},
					"additionalProperties": false,
				},
				map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{"const": "run"},
						"code":   map[string]any{"type": "string"},
					},
					"required":             []string{"action", "code"},
					"additionalProperties": false,
				},
			},
		},
		Execute: func(_ sdk.Context, params map[string]any) (any, error) {
			gotArgs = params
			return "ok", nil
		},
	}))

	h := startFakeHost(t, ext, "sess-union")
	tl := h.tool("discriminated")

	var rawRoot map[string]json.RawMessage
	if err := json.Unmarshal(tl.Parameters, &rawRoot); err != nil {
		t.Fatalf("unmarshal union root: %v", err)
	}
	if _, hasProps := rawRoot["properties"]; hasProps {
		t.Fatalf("pure anyOf root must NOT have sibling properties: %s", string(tl.Parameters))
	}
	if _, hasReq := rawRoot["required"]; hasReq {
		t.Fatalf("pure anyOf root must NOT have sibling required: %s", string(tl.Parameters))
	}
	var branches []json.RawMessage
	if err := json.Unmarshal(rawRoot["anyOf"], &branches); err != nil || len(branches) != 2 {
		t.Fatalf("expected 2 anyOf branches, got %d (err=%v)", len(branches), err)
	}
	for idx, br := range branches {
		keys := jsonObjectKeys(t, extractWireProperties(t, br))
		if len(keys) == 0 || keys[0] != Field {
			t.Fatalf("branch %d properties order = %v, want %q first", idx, keys, Field)
		}
		var bm map[string]any
		_ = json.Unmarshal(br, &bm)
		if !slices.Contains(extractStringSlice(bm["required"]), Field) {
			t.Fatalf("branch %d required = %v, want %q", idx, bm["required"], Field)
		}
	}

	// Executing through validation succeeds with `i` and strips it before Execute.
	if _, err := h.callTool("discriminated", "call-u1", map[string]any{
		"i":      "Running snippet",
		"action": "run",
		"code":   "1+1",
	}); err != nil {
		t.Fatalf("union callTool failed: %v", err)
	}
	if _, hasI := gotArgs[Field]; hasI {
		t.Fatalf("Execute received injected %q: %+v", Field, gotArgs)
	}
	if gotArgs["action"] != "run" || gotArgs["code"] != "1+1" {
		t.Fatalf("Execute args = %+v", gotArgs)
	}
}

// upstream: packages/agent/test/normalize-tools-prune.test.ts: "leaves a tool-owned intent field untouched when injecting"
// upstream: packages/agent/test/agent-loop.test.ts: "keeps a schema-owned `i` argument for the tool under intent tracing"
func TestExistingSchemaOwnedIntentUntouched(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_DESC", "")
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	var gotArgs map[string]any
	var recordedIntent string
	ownedProps := sdk.NewOrderedObject(
		"path", sdk.NewOrderedObject("type", "string", "description", "where to read"),
		"i", sdk.NewOrderedObject("type", "string", "description", "the tool's own index"),
	)
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "demo_owned",
		Label:       "Demo",
		Description: "Tool that owns i",
		Parameters: sdk.Schema{
			"type":       "object",
			"properties": ownedProps,
			"required":   []string{"path"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			gotArgs = params
			recordedIntent = For(ctx, ctx.ToolCallID())
			return "ok", nil
		},
	}))

	h := startFakeHost(t, ext, "sess-owned")
	tl := h.tool("demo_owned")

	propKeys := jsonObjectKeys(t, extractWireProperties(t, tl.Parameters))
	wantOrder := []string{"path", "i"}
	if !slices.Equal(propKeys, wantOrder) {
		t.Fatalf("tool-owned i was reordered: got %v, want %v", propKeys, wantOrder)
	}
	var parsed map[string]any
	_ = json.Unmarshal(tl.Parameters, &parsed)
	if slices.Contains(extractStringSlice(parsed["required"]), Field) {
		t.Fatalf("tool-owned optional i must NOT be added to required: %v", parsed["required"])
	}
	iDesc := parsed["properties"].(map[string]any)[Field].(map[string]any)["description"]
	if iDesc != "the tool's own index" {
		t.Fatalf("tool-owned i description = %q, want original", iDesc)
	}

	if _, err := h.callTool("demo_owned", "call-owned-1", map[string]any{
		"path": "a.txt",
		"i":    "INDEX",
	}); err != nil {
		t.Fatalf("callTool failed: %v", err)
	}
	if gotArgs["i"] != "INDEX" || gotArgs["path"] != "a.txt" {
		t.Fatalf("tool-owned i must NOT be stripped; got %+v", gotArgs)
	}
	if recordedIntent != "" {
		t.Fatalf("tool-owned i must NOT be recorded as harness intent; got %q", recordedIntent)
	}
}

// upstream: packages/agent/test/agent-loop.test.ts:485: "still derives intent for a tool that owns `i` as an argument"
func TestDerivesIntentForToolThatOwnsIArgument(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	// Register a temporary deriver for a tool whose own schema defines `i`,
	// matching agent-loop.test.ts:485-518 (`intent: args => "Labeling ${args.label} at ${args.i}"`).
	const toolName = "demo_owned_derived"
	toolDerivers[toolName] = func(args map[string]any) string {
		return fmt.Sprintf("Labeling %v at %v", args["label"], args["i"])
	}
	t.Cleanup(func() { delete(toolDerivers, toolName) })

	ext := sdk.New("test")
	var gotArgs map[string]any
	var gotIntent string
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        toolName,
		Label:       "Demo",
		Description: "Demo tool",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"label": map[string]any{"type": "string"},
				"i":     map[string]any{"type": "string"},
			},
			"required": []string{"label"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			gotArgs = params
			gotIntent = For(ctx, ctx.ToolCallID())
			return "ok", nil
		},
	}))

	h := startFakeHost(t, ext, "sess-owned-derive")
	if _, err := h.callTool(toolName, "tool-1", map[string]any{"label": "x", "i": "INDEX"}); err != nil {
		t.Fatalf("callTool failed: %v", err)
	}
	if gotArgs["label"] != "x" || gotArgs["i"] != "INDEX" {
		t.Fatalf("Execute args = %+v, want {label:x i:INDEX}", gotArgs)
	}
	if gotIntent != "Labeling x at INDEX" {
		t.Fatalf("For(ctx, tool-1) = %q, want %q", gotIntent, "Labeling x at INDEX")
	}
}

// upstream: packages/agent/test/normalize-tools-prune.test.ts:
//   - "wire-encodes arktype parameters when intent is a function"
//   - "wire-encodes arktype parameters when intent is the literal 'omit'"
func TestOmitModeDoesNotInjectIntent(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	for _, name := range []string{"resolve", "reject", "propose", "goal", "yield", "eval", "checkpoint", "rewind"} {
		toolName := name
		ext.RegisterTool(Tool(sdk.ToolDefinition{
			Name:        toolName,
			Label:       toolName,
			Description: "omit tool",
			Parameters: sdk.Schema{
				"type": "object",
				"properties": map[string]any{
					"arg": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			Execute: func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
		}))
	}

	h := startFakeHost(t, ext, "sess-omit")
	for _, name := range []string{"resolve", "reject", "propose", "goal", "yield", "eval", "checkpoint", "rewind"} {
		tl := h.tool(name)
		keys := jsonObjectKeys(t, extractWireProperties(t, tl.Parameters))
		if slices.Contains(keys, Field) {
			t.Fatalf("omit tool %q must not have %q injected; keys=%v", name, Field, keys)
		}
	}
}

// upstream: packages/agent/src/agent-loop.ts:1085 (`Bun.env.PI_NO_INTENT !== "1"`)
func TestEnvPINoIntentDisablesInjection(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "1")
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "read",
		Label:       "Read",
		Description: "Read files",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	}))

	h := startFakeHost(t, ext, "sess-no-intent")
	tl := h.tool("read")
	keys := jsonObjectKeys(t, extractWireProperties(t, tl.Parameters))
	if slices.Contains(keys, Field) {
		t.Fatalf("PI_NO_INTENT=1 must suppress injection; keys=%v", keys)
	}
}

// upstream: packages/agent/test/normalize-tools-prune.test.ts: "describes the intent field per caller over a shared schema, PI_INTENT_DESC overriding"
func TestEnvPIIntentDescOverridesDescription(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_DESC", "custom description from env")
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "bash",
		Label:       "Bash",
		Description: "Run shell",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
			},
			"required": []string{"command"},
		},
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	}))

	h := startFakeHost(t, ext, "sess-desc")
	var parsed map[string]any
	_ = json.Unmarshal(h.tool("bash").Parameters, &parsed)
	desc := parsed["properties"].(map[string]any)[Field].(map[string]any)["description"]
	if desc != "custom description from env" {
		t.Fatalf("i description = %q, want %q", desc, "custom description from env")
	}
}

// upstream: packages/agent/test/agent-loop.test.ts:
//   - "injects and strips intent when intent tracing is enabled"
//   - "normalizes trailing periods from intent at extraction site"
//   - "runs a tool call with a long intent instead of rejecting it"
func TestExecuteStripsIntentWithStrictParserAndPrepareArguments(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	var received map[string]any
	var intentDuringExec string
	var sawPrepareKeys []string

	// Simulates `glob`: has `additionalProperties: false`, a strict
	// PrepareArguments that rejects ANY undeclared key (just like
	// internal/search/register.go:253), and a strict Execute parser that
	// rejects unknown keys.
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "glob",
		Label:       "Glob",
		Description: "Find files",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"additionalProperties": false,
		},
		PrepareArguments: func(params map[string]any) (map[string]any, error) {
			for k := range params {
				sawPrepareKeys = append(sawPrepareKeys, k)
				if k != "path" && k != "pattern" {
					return nil, fmt.Errorf("glob has no `%s` argument; put the glob in `path`", k)
				}
			}
			if pat, ok := params["pattern"].(string); ok {
				return map[string]any{"path": pat}, nil
			}
			return params, nil
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			for k := range params {
				if k != "path" {
					return nil, fmt.Errorf("strict parser rejected unexpected key %q", k)
				}
			}
			received = params
			intentDuringExec = For(ctx, ctx.ToolCallID())
			return "ok", nil
		},
	}))

	var eventIntent string
	ext.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		eventIntent = FromEvent(ctx, data)
		return nil, nil
	})

	h := startFakeHost(t, ext, "sess-strict")

	rawArgs := map[string]any{
		"i":       "  Listing Go files in internal...  ",
		"pattern": "internal/**/*.go",
	}
	h.emitEvent(sdk.EventToolExecutionStart, map[string]any{
		"toolCallId": "tc-glob-1",
		"toolName":   "glob",
		"args":       rawArgs,
	})
	if eventIntent != "Listing Go files in internal" {
		t.Fatalf("FromEvent intent = %q, want %q", eventIntent, "Listing Go files in internal")
	}

	if _, err := h.callTool("glob", "tc-glob-1", rawArgs); err != nil {
		t.Fatalf("strict glob callTool failed: %v", err)
	}
	if slices.Contains(sawPrepareKeys, Field) {
		t.Fatalf("inner PrepareArguments must not see %q; saw %v", Field, sawPrepareKeys)
	}
	if _, hasI := received[Field]; hasI {
		t.Fatalf("Execute must not see %q; got %+v", Field, received)
	}
	if received["path"] != "internal/**/*.go" {
		t.Fatalf("Execute path = %v, want internal/**/*.go", received["path"])
	}
	if intentDuringExec != "Listing Go files in internal" {
		t.Fatalf("For(ctx, toolCallID) = %q, want %q", intentDuringExec, "Listing Go files in internal")
	}

	// Verify entry was cleaned up when Execute returned (review item 6).
	storeMu.RLock()
	_, stillStored := sessionStore["sess-strict"]["tc-glob-1"]
	storeMu.RUnlock()
	if stillStored {
		t.Fatalf("tc-glob-1 entry leaked in sessionStore after Execute returned")
	}
}

// upstream: packages/coding-agent/src/tools/eval.ts:386-390
// upstream: packages/coding-agent/src/tools/checkpoint.ts:62,98
func TestFallbackIntentDerivers(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_INTENT_TRACING", "")

	ext := sdk.New("test")
	recorded := map[string]string{}
	for _, name := range []string{"eval", "checkpoint", "rewind"} {
		toolName := name
		ext.RegisterTool(Tool(sdk.ToolDefinition{
			Name:        toolName,
			Label:       toolName,
			Description: toolName,
			Parameters:  sdk.Schema{"type": "object", "properties": map[string]any{}},
			Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
				recorded[ctx.ToolCallID()] = For(ctx, ctx.ToolCallID())
				return "ok", nil
			},
		}))
	}

	var eventIntents []string
	ext.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		eventIntents = append(eventIntents, FromEvent(ctx, data))
		return nil, nil
	})

	h := startFakeHost(t, ext, "sess-derive")

	cases := []struct {
		callID string
		tool   string
		args   map[string]any
		want   string
	}{
		{"c1", "eval", map[string]any{"language": "py", "code": "1+1"}, "running python"},
		{"c2", "eval", map[string]any{"language": "js", "code": "1+1"}, "running javascript"},
		{"c3", "eval", map[string]any{"language": "py", "title": "Inspecting AST", "code": "pass"}, "Inspecting AST"},
		{"c4", "eval", map[string]any{"code": "1"}, "running javascript"},
		{"c5", "checkpoint", map[string]any{"goal": "refactor parser"}, "checkpointing: refactor parser"},
		{"c6", "checkpoint", map[string]any{}, "checkpointing"},
		{"c7", "rewind", map[string]any{"report": "done"}, "rewinding"},
	}

	for _, tc := range cases {
		h.emitEvent(sdk.EventToolExecutionStart, map[string]any{
			"toolCallId": tc.callID,
			"toolName":   tc.tool,
			"args":       tc.args,
		})
		if _, err := h.callTool(tc.tool, tc.callID, tc.args); err != nil {
			t.Fatalf("%s(%s) callTool error: %v", tc.tool, tc.callID, err)
		}
		if got := recorded[tc.callID]; got != tc.want {
			t.Fatalf("For(%s %s) = %q, want %q", tc.tool, tc.callID, got, tc.want)
		}
	}

	for i, tc := range cases {
		if eventIntents[i] != tc.want {
			t.Fatalf("FromEvent[%d] (%s) = %q, want %q", i, tc.tool, eventIntents[i], tc.want)
		}
	}
}

func TestSessionIsolationDeleteOnReturnAndNoRecreateAfterRelease(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	// Synchronize concurrent calls across two sessions so both are in-flight
	// simultaneously with the SAME toolCallID.
	aInExec := make(chan struct{})
	releaseA := make(chan struct{})

	makeExt := func(block bool, ownOut, otherOut *string) *sdk.Extension {
		ext := sdk.New("test")
		RegisterLifecycle(ext)
		ext.RegisterTool(Tool(sdk.ToolDefinition{
			Name:        "read",
			Label:       "Read",
			Description: "Read",
			Parameters: sdk.Schema{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
			Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
				*ownOut = For(ctx, ctx.ToolCallID())
				if checkID, ok := params["path"].(string); ok && checkID != "" {
					*otherOut = For(ctx, checkID)
				}
				if block {
					close(aInExec)
					<-releaseA
				}
				return "ok", nil
			},
		}))
		return ext
	}

	var ownA, probeA, ownB, probeB string
	hA := startFakeHost(t, makeExt(true, &ownA, &probeA), "session-A")
	hB := startFakeHost(t, makeExt(false, &ownB, &probeB), "session-B")

	doneA := make(chan error, 1)
	go func() {
		_, err := hA.callTool("read", "shared-call-1", map[string]any{"i": "Reading A", "path": "shared-call-1"})
		doneA <- err
	}()
	<-aInExec

	// While session-A has "shared-call-1" live in sessionStore, session-B probes
	// "shared-call-1" under a different call ID and must NOT see session-A's intent.
	if _, err := hB.callTool("read", "call-b-probe", map[string]any{"i": "Probing B", "path": "shared-call-1"}); err != nil {
		t.Fatalf("session B probe failed: %v", err)
	}
	if probeB != "" {
		t.Fatalf("session B leaked session A's intent: got %q, want empty", probeB)
	}
	if ownB != "Probing B" {
		t.Fatalf("session B own intent = %q, want %q", ownB, "Probing B")
	}

	// Session B's completed call must already be deleted from sessionStore.
	storeMu.RLock()
	_, bLeaked := sessionStore["session-B"]
	storeMu.RUnlock()
	if bLeaked {
		t.Fatalf("completed session-B call remained in sessionStore")
	}

	// Release session-A and check its entry is deleted on Execute return too.
	close(releaseA)
	if err := <-doneA; err != nil {
		t.Fatalf("session A call failed: %v", err)
	}
	if ownA != "Reading A" {
		t.Fatalf("session A own intent = %q, want %q", ownA, "Reading A")
	}
	storeMu.RLock()
	_, aLeaked := sessionStore["session-A"]
	storeMu.RUnlock()
	if aLeaked {
		t.Fatalf("completed session-A call remained in sessionStore")
	}

	// End session-A via lifecycle.End, then verify a late Record / call cannot
	// recreate session-A's table in sessionStore.
	lifecycle.End("session-A")
	Record("session-A", "late-call", "Late background intent")
	storeMu.RLock()
	_, recreated := sessionStore["session-A"]
	storeMu.RUnlock()
	if recreated {
		t.Fatalf("Record recreated session-A table after lifecycle.End")
	}

	// Ending session-B via session_shutdown also releases and marks it released.
	hB.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "exit"})
	Record("session-B", "late-call-b", "Late B intent")
	storeMu.RLock()
	_, recreatedB := sessionStore["session-B"]
	storeMu.RUnlock()
	if recreatedB {
		t.Fatalf("Record recreated session-B table after session_shutdown")
	}
}

// PiG fires `session_shutdown` (reason: reload/resume/...) and then starts a
// fresh extension instance with `session_start` for the SAME session ID.
// Late writes after End and before the new `session_start` must still be
// ignored, while the new `session_start` clears the tombstone and recomputes
// the cached `Enabled` decision so the reloaded session works like a new one.
func TestReloadAndResumeClearTombstoneAndRecomputeEnabled(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	const sid = "sess-reload-1"

	makeReadDef := func(captured *string) sdk.ToolDefinition {
		return sdk.ToolDefinition{
			Name:        "read",
			Label:       "Read",
			Description: "Read files",
			Parameters: sdk.Schema{
				"type":                 "object",
				"properties":           map[string]any{"path": map[string]any{"type": "string"}},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
			Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
				*captured = For(ctx, ctx.ToolCallID())
				return "ok", nil
			},
		}
	}

	// 1. Initial interactive TUI session starts and records intent normally.
	var firstCaptured string
	ext1 := sdk.New("test")
	RegisterLifecycle(ext1)
	ext1.RegisterTool(Tool(makeReadDef(&firstCaptured)))
	h1 := startFakeHostWithMode(t, ext1, sid, "tui", true)
	h1.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})

	if _, err := h1.callTool("read", "call-before-reload", map[string]any{"i": "Reading before reload", "path": "a.go"}); err != nil {
		t.Fatalf("call before reload failed: %v", err)
	}
	if firstCaptured != "Reading before reload" {
		t.Fatalf("before reload For = %q, want %q", firstCaptured, "Reading before reload")
	}

	// 2. Session shuts down with reason "reload" -> tombstone set.
	h1.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "reload"})

	// 3. A late write arriving after shutdown and BEFORE the new session_start
	//    must still be ignored and must not recreate the session's table.
	Record(sid, "late-call-during-reload", "Stale late intent")
	storeMu.RLock()
	_, lateRecreated := sessionStore[sid]
	_, isTombstoned := releasedSessions[sid]
	storeMu.RUnlock()
	if lateRecreated {
		t.Fatalf("late Record before new session_start recreated sessionStore[%q]", sid)
	}
	if !isTombstoned {
		t.Fatalf("expected tombstone for %q after session_shutdown(reload)", sid)
	}

	// 4. Fresh extension instance starts for the SAME session ID (reload).
	//    First verify in interactive TUI mode: tombstone is cleared, Enabled is
	//    recomputed and cached, and intents record/read normally again.
	var secondCaptured string
	var enabledAfterReload bool
	ext2 := sdk.New("test")
	RegisterLifecycle(ext2)
	def2 := makeReadDef(&secondCaptured)
	ext2.RegisterTool(Tool(def2))
	ext2.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		enabledAfterReload = Enabled(ctx)
		return nil, nil
	})
	h2 := startFakeHostWithMode(t, ext2, sid, "tui", true)
	h2.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "reload"})

	if !enabledAfterReload {
		t.Fatalf("Enabled after reload in tui = false, want true")
	}
	storeMu.RLock()
	_, stillTombstoned := releasedSessions[sid]
	cachedAfterReload, hasCached := sessionEnabled[sid]
	storeMu.RUnlock()
	if stillTombstoned {
		t.Fatalf("tombstone for %q was not cleared by session_start(reload)", sid)
	}
	if !hasCached || !cachedAfterReload {
		t.Fatalf("sessionEnabled[%q] = (%v, %v), want (true, true)", sid, cachedAfterReload, hasCached)
	}
	if _, err := h2.callTool("read", "call-after-reload", map[string]any{"i": "Reading after reload", "path": "b.go"}); err != nil {
		t.Fatalf("call after reload failed: %v", err)
	}
	if secondCaptured != "Reading after reload" {
		t.Fatalf("after reload For = %q, want %q", secondCaptured, "Reading after reload")
	}

	// 5. End via lifecycle.End(sid), then resume the SAME session ID in print
	//    mode via RegisterSync: Enabled must recompute to false (not stale true)
	//    and drop `i` from the wire schema.
	lifecycle.End(sid)
	Record(sid, "late-call-after-end", "Ignored late intent")
	storeMu.RLock()
	_, lateRecreated2 := sessionStore[sid]
	storeMu.RUnlock()
	if lateRecreated2 {
		t.Fatalf("late Record after lifecycle.End recreated sessionStore[%q]", sid)
	}

	var thirdCaptured string
	var enabledAfterResume bool
	ext3 := sdk.New("test")
	def3 := makeReadDef(&thirdCaptured)
	ext3.RegisterTool(Tool(def3))
	RegisterSync(ext3, def3)
	ext3.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		enabledAfterResume = Enabled(ctx)
		return nil, nil
	})
	h3 := startFakeHostWithMode(t, ext3, sid, "print", false)
	h3.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "resume"})

	if enabledAfterResume {
		t.Fatalf("Enabled after resume in print mode = true, want false")
	}
	if keys := jsonObjectKeys(t, extractWireProperties(t, h3.tool("read").Parameters)); slices.Contains(keys, Field) {
		t.Fatalf("after resume in print mode read keys = %v, want no %q", keys, Field)
	}
	if _, err := h3.callTool("read", "call-after-resume", map[string]any{"path": "c.go"}); err != nil {
		t.Fatalf("call after resume in print mode failed: %v", err)
	}
	if thirdCaptured != "" {
		t.Fatalf("after resume in print mode For = %q, want empty", thirdCaptured)
	}
}

// upstream: packages/coding-agent/src/sdk.ts:492-499 (`intentTracingActive`)
// upstream: packages/utils/src/env.ts:493-505 (`isInteractiveHost`)
func TestDefaultEnabledInteractiveHostAndSubagentInheritance(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	// Track child -> parent mapping like internal/agents.ParentSession.
	parentMap := map[string]string{
		"child-of-tui": "top-tui",
		"child-of-rpc": "top-rpc",
	}
	ParentSession = func(ctx sdk.Context) (string, bool) {
		sid, _ := ctx.GetSessionID()
		if p, ok := parentMap[sid]; ok {
			return p, true
		}
		return "", false
	}

	readDef := sdk.ToolDefinition{
		Name:        "read",
		Label:       "Read",
		Description: "Read files",
		Parameters: sdk.Schema{
			"type":                 "object",
			"properties":           map[string]any{"path": map[string]any{"type": "string"}},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	}

	makeExt := func(enabledOut *bool) *sdk.Extension {
		ext := sdk.New("test")
		ext.RegisterTool(Tool(readDef))
		RegisterSync(ext, readDef)
		ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
			*enabledOut = Enabled(ctx)
			return nil, nil
		})
		return ext
	}

	var tuiOn, rpcOn, printOn, jsonOn, childTUIOn, childRPCOn bool
	hTUI := startFakeHostWithMode(t, makeExt(&tuiOn), "top-tui", "tui", true)
	// RPC has HasUI()==true in PiG (context.go:2384), yet omp MUST disable intent tracing in RPC!
	hRPC := startFakeHostWithMode(t, makeExt(&rpcOn), "top-rpc", "rpc", true)
	hPrint := startFakeHostWithMode(t, makeExt(&printOn), "top-print", "print", false)
	hJSON := startFakeHostWithMode(t, makeExt(&jsonOn), "top-json", "json", false)

	hTUI.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})
	hRPC.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})
	hPrint.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})
	hJSON.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})

	if !tuiOn {
		t.Fatal("interactive TUI session must enable intent tracing by default")
	}
	if rpcOn {
		t.Fatal("RPC session (HasUI=true, Mode=rpc) must NOT enable intent tracing by default")
	}
	if printOn {
		t.Fatal("print session must NOT enable intent tracing by default")
	}
	if jsonOn {
		t.Fatal("json session must NOT enable intent tracing by default")
	}

	// Wire schemas: TUI keeps `i`, RPC/print/json drop `i` via RegisterSync.
	if keys := jsonObjectKeys(t, extractWireProperties(t, hTUI.tool("read").Parameters)); len(keys) == 0 || keys[0] != Field {
		t.Fatalf("TUI read keys = %v, want %q first", keys, Field)
	}
	for name, host := range map[string]*fakeHost{"rpc": hRPC, "print": hPrint, "json": hJSON} {
		if keys := jsonObjectKeys(t, extractWireProperties(t, host.tool("read").Parameters)); slices.Contains(keys, Field) {
			t.Fatalf("%s read keys = %v, want no %q", name, keys, Field)
		}
	}

	// Child sessions inherit their top-level parent's interactive-host decision even
	// though child sessions themselves run headless (HasUI=false, Mode="print").
	hChildTUI := startFakeHostWithMode(t, makeExt(&childTUIOn), "child-of-tui", "print", false)
	hChildRPC := startFakeHostWithMode(t, makeExt(&childRPCOn), "child-of-rpc", "print", false)
	hChildTUI.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})
	hChildRPC.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})

	if !childTUIOn {
		t.Fatal("subagent of interactive TUI session must inherit intent tracing = true")
	}
	if childRPCOn {
		t.Fatal("subagent of RPC session must inherit intent tracing = false")
	}
	if keys := jsonObjectKeys(t, extractWireProperties(t, hChildTUI.tool("read").Parameters)); len(keys) == 0 || keys[0] != Field {
		t.Fatalf("child-of-tui read keys = %v, want %q first", keys, Field)
	}
	if keys := jsonObjectKeys(t, extractWireProperties(t, hChildRPC.tool("read").Parameters)); slices.Contains(keys, Field) {
		t.Fatalf("child-of-rpc read keys = %v, want no %q", keys, Field)
	}

	// Explicit Configured hook overrides interactive-host fallback in both directions.
	Configured = func(ctx sdk.Context) (bool, bool) {
		sid, _ := ctx.GetSessionID()
		switch sid {
		case "cfg-force-on":
			return true, true
		case "cfg-force-off":
			return false, true
		default:
			return false, false
		}
	}
	var forceOn, forceOff bool
	hForceOn := startFakeHostWithMode(t, makeExt(&forceOn), "cfg-force-on", "print", false)
	hForceOff := startFakeHostWithMode(t, makeExt(&forceOff), "cfg-force-off", "tui", true)
	hForceOn.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})
	hForceOff.emitEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})
	if !forceOn {
		t.Fatal("explicit Configured=true must override print mode")
	}
	if forceOff {
		t.Fatal("explicit Configured=false must override tui mode")
	}
	if keys := jsonObjectKeys(t, extractWireProperties(t, hForceOff.tool("read").Parameters)); slices.Contains(keys, Field) {
		t.Fatalf("cfg-force-off read keys = %v, want no %q", keys, Field)
	}
}

// upstream: packages/wire/src/index.ts:415-418 (`toolIntentLabel`)
// upstream: packages/coding-agent/src/modes/controllers/event-controller.ts:187-199 (`workingIntentLabel`)
func TestToolIntentLabelAndWorkingLabel(t *testing.T) {
	labelCases := []struct {
		in   string
		want string
	}{
		{"Reading file", "Reading file"},
		{"checkpointing: refactor parser", "checkpointing"},
		{"Reading src/main.go:42", "Reading src/main.go:42"},
		{"Fetching https://example.com/a", "Fetching https://example.com/a"},
		{"Running tests:", "Running tests"},
		{": leading colon", ": leading colon"},
	}
	for _, tc := range labelCases {
		if got := Label(tc.in); got != tc.want {
			t.Errorf("Label(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// WorkingLabel cuts colon-space first, THEN normalizes trailing periods.
	workingCases := []struct {
		in   string
		want string
	}{
		{"Running tests...: unit", "Running tests"},
		{"  Reading settings... : details.  ", "Reading settings"},
		{"Reading src/main.go:42.", "Reading src/main.go:42"},
		{"   ...   ", ""},
		{"", ""},
	}
	for _, tc := range workingCases {
		if got := WorkingLabel(tc.in); got != tc.want {
			t.Errorf("WorkingLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Verify WorkingLabelFromEvent applies label-then-normalize directly on raw event args.
	resetAllForTest(t)
	ext := sdk.New("test")
	var gotLabel string
	ext.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		gotLabel = WorkingLabelFromEvent(ctx, data)
		return nil, nil
	})
	h := startFakeHost(t, ext, "sess-wlabel")
	h.emitEvent(sdk.EventToolExecutionStart, map[string]any{
		"toolCallId": "wl-1",
		"toolName":   "bash",
		"args":       map[string]any{"i": "Running tests...: unit", "command": "go test"},
	})
	if gotLabel != "Running tests" {
		t.Fatalf("WorkingLabelFromEvent = %q, want %q", gotLabel, "Running tests")
	}
}

// upstream: packages/agent/src/agent-loop.ts:1053-1063 (existing `i` reorder & required in InjectSchema)
// upstream: packages/agent/test/normalize-tools-prune.test.ts: "injects the intent field WITHOUT a description when pruning"
func TestInjectSchemaEdgeCases(t *testing.T) {
	// Bare injection (empty description) omits description key.
	bare := InjectSchema(sdk.Schema{
		"type":       "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}},
	}, ModeRequire, "")
	rawBare, err := json.Marshal(bare)
	if err != nil {
		t.Fatalf("marshal bare: %v", err)
	}
	var bareMap map[string]any
	_ = json.Unmarshal(rawBare, &bareMap)
	iField := bareMap["properties"].(map[string]any)[Field].(map[string]any)
	if _, hasDesc := iField["description"]; hasDesc {
		t.Fatalf("bare injection must not include description: %+v", iField)
	}

	// Direct InjectSchema on a schema that already has `i` second reorders it first and adds to required.
	reordered := InjectSchema(sdk.Schema{
		"type":       "object",
		"properties": sdk.NewOrderedObject("path", map[string]any{"type": "string"}, "i", map[string]any{"type": "string", "description": "existing"}),
		"required":   []string{"path"},
	}, ModeRequire, "concise intent")
	rawReordered, err := json.Marshal(reordered)
	if err != nil {
		t.Fatalf("marshal reordered: %v", err)
	}
	keys := jsonObjectKeys(t, extractWireProperties(t, rawReordered))
	if !slices.Equal(keys, []string{"i", "path"}) {
		t.Fatalf("reordered keys = %v, want [i path]", keys)
	}
	var rm map[string]any
	_ = json.Unmarshal(rawReordered, &rm)
	if !slices.Contains(extractStringSlice(rm["required"]), "i") {
		t.Fatalf("reordered required = %v, want i", rm["required"])
	}
	if rm["properties"].(map[string]any)["i"].(map[string]any)["description"] != "existing" {
		t.Fatalf("existing i property should keep its schema: %+v", rm["properties"])
	}

	// Pure oneOf root injects into each branch.
	oneOf := InjectSchema(sdk.Schema{
		"oneOf": []any{
			map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
			map[string]any{"type": "object", "properties": map[string]any{"b": map[string]any{"type": "number"}}},
		},
	}, ModeOptional, DefaultDescription)
	rawOneOf, _ := json.Marshal(oneOf)
	var oneOfRoot map[string]json.RawMessage
	_ = json.Unmarshal(rawOneOf, &oneOfRoot)
	if _, hasProps := oneOfRoot["properties"]; hasProps {
		t.Fatalf("pure oneOf root should not gain root properties: %s", string(rawOneOf))
	}
	var branches []json.RawMessage
	_ = json.Unmarshal(oneOfRoot["oneOf"], &branches)
	for idx, br := range branches {
		bk := jsonObjectKeys(t, extractWireProperties(t, br))
		if len(bk) == 0 || bk[0] != Field {
			t.Fatalf("oneOf branch %d keys = %v, want i first", idx, bk)
		}
	}
}

// upstream: packages/ai/src/utils/schema/json-schema-validator.ts:168-326 (`schemaDefinesProperty`)
func TestSchemaDefinesProperty(t *testing.T) {
	// Direct property ownership.
	if !SchemaDefinesProperty(sdk.Schema{"type": "object", "properties": map[string]any{"i": map[string]any{"type": "string"}}}, Field) {
		t.Fatal("direct property i should be owned")
	}

	// Positive `properties: { i: false }` returns false IMMEDIATELY (even if a
	// later keyword like `patternProperties` or `required` also mentions `i`).
	if SchemaDefinesProperty(sdk.Schema{
		"type":              "object",
		"properties":        map[string]any{"i": false},
		"patternProperties": map[string]any{"^i$": map[string]any{"type": "string"}},
	}, Field) {
		t.Fatal("positive properties.i: false must return false immediately without falling through")
	}

	// Negated context: `not: { properties: { i: false } }` owns `i`.
	if !SchemaDefinesProperty(sdk.Schema{
		"type": "object",
		"not":  map[string]any{"properties": map[string]any{"i": false}},
	}, Field) {
		t.Fatal("negated properties.i: false should own i")
	}

	// `not: { required: ["i"] }` forbids `i` rather than owning it.
	if SchemaDefinesProperty(sdk.Schema{"type": "object", "not": map[string]any{"required": []string{"i"}}}, Field) {
		t.Fatal("not.required:[i] forbids i rather than owning it")
	}

	// `anyOf` branch defining `i`.
	if !SchemaDefinesProperty(sdk.Schema{
		"anyOf": []any{
			map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
			map[string]any{"type": "object", "properties": map[string]any{"i": map[string]any{"type": "number"}}},
		},
	}, Field) {
		t.Fatal("anyOf branch defining i should mark schema as owning i")
	}

	// Local `$ref` through object `$defs`.
	if !SchemaDefinesProperty(sdk.Schema{
		"$defs": map[string]any{
			"item": map[string]any{"type": "object", "required": []string{"i"}},
		},
		"$ref": "#/$defs/item",
	}, Field) {
		t.Fatal("local $ref defining i should mark schema as owning i")
	}

	// Local `$ref` indexing an array element (`#/anyOf/1`).
	if !SchemaDefinesProperty(sdk.Schema{
		"anyOf": []any{
			map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}},
			map[string]any{"type": "object", "properties": map[string]any{"i": map[string]any{"type": "integer"}}},
		},
		"$defs": map[string]any{
			"ptr": map[string]any{"$ref": "#/anyOf/1"},
		},
	}, Field) {
		t.Fatal("array-indexed local $ref should resolve")
	}
	if !SchemaDefinesProperty(sdk.Schema{
		"items": []any{
			map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
			map[string]any{"type": "object", "properties": map[string]any{"i": map[string]any{"type": "string"}}},
		},
		"$ref": "#/items/1",
	}, Field) {
		t.Fatal("direct $ref #/items/1 into slice must resolve and detect i")
	}

	// `propertyNames` matching `i` (with and without `additionalProperties: false`).
	if !SchemaDefinesProperty(sdk.Schema{
		"type":          "object",
		"propertyNames": map[string]any{"pattern": "^[a-z]$"},
	}, Field) {
		t.Fatal("propertyNames matching i should own i")
	}
	if SchemaDefinesProperty(sdk.Schema{
		"type":                 "object",
		"propertyNames":        map[string]any{"pattern": "^[a-z]$"},
		"additionalProperties": false,
	}, Field) {
		t.Fatal("propertyNames with additionalProperties: false should not own i")
	}
	if SchemaDefinesProperty(sdk.Schema{
		"type":          "object",
		"propertyNames": map[string]any{"enum": []any{"a", "b"}},
	}, Field) {
		t.Fatal("propertyNames enum not containing i should not own i")
	}

	// `patternProperties`.
	if !SchemaDefinesProperty(sdk.Schema{
		"type":              "object",
		"patternProperties": map[string]any{"^i$": map[string]any{"type": "number"}},
	}, Field) {
		t.Fatal("patternProperties matching i should own i")
	}
	if SchemaDefinesProperty(sdk.Schema{
		"type":              "object",
		"patternProperties": map[string]any{"^i$": false},
	}, Field) {
		t.Fatal("patternProperties with false schema should not own i")
	}

	// `if` / `then` / `else` (predicate context treats `properties.i: false` as owning).
	if !SchemaDefinesProperty(sdk.Schema{
		"if":   map[string]any{"properties": map[string]any{"i": false}},
		"then": map[string]any{"type": "object"},
	}, Field) {
		t.Fatal("if predicate checking properties.i: false should own i")
	}
	if !SchemaDefinesProperty(sdk.Schema{
		"if":   map[string]any{"properties": map[string]any{"mode": map[string]any{"const": "a"}}},
		"then": map[string]any{"properties": map[string]any{"x": map[string]any{"type": "string"}}},
		"else": map[string]any{"properties": map[string]any{"i": map[string]any{"type": "string"}}},
	}, Field) {
		t.Fatal("else branch defining i should own i")
	}

	// `dependentSchemas`, `dependentRequired`, `dependencies`.
	if !SchemaDefinesProperty(sdk.Schema{
		"dependentSchemas": map[string]any{"i": map[string]any{"required": []string{"x"}}},
	}, Field) {
		t.Fatal("dependentSchemas keyed by i should own i")
	}
	if !SchemaDefinesProperty(sdk.Schema{
		"dependentRequired": map[string]any{"x": []string{"i"}},
	}, Field) {
		t.Fatal("dependentRequired requiring i should own i")
	}
	if !SchemaDefinesProperty(sdk.Schema{
		"dependencies": map[string]any{"x": []string{"i"}},
	}, Field) {
		t.Fatal("dependencies requiring i should own i")
	}
	if !SchemaDefinesProperty(sdk.Schema{
		"dependencies": map[string]any{"x": map[string]any{"properties": map[string]any{"i": map[string]any{"type": "string"}}}},
	}, Field) {
		t.Fatal("dependencies subschema defining i should own i")
	}
}
