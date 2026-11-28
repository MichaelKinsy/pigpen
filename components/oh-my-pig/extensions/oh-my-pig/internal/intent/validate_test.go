package intent

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// loadCapturedSchemas reads the 25 real omp v19.1.1 captured wire schemas from
// ../schemaorder/omp_schemas.json so the validation table tests exercise the
// exact schemas advertised by omp.
func loadCapturedSchemas(t *testing.T) map[string]sdk.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "schemaorder", "omp_schemas.json"))
	if err != nil {
		t.Fatalf("read omp_schemas.json: %v", err)
	}
	var parsed map[string]struct {
		Parameters sdk.Schema `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse omp_schemas.json: %v", err)
	}
	out := make(map[string]sdk.Schema, len(parsed))
	for name, entry := range parsed {
		out[name] = entry.Parameters
	}
	return out
}

// TestValidationDropsUndeclaredKeys verifies omp's unrecognized-key repair:
// undeclared keys on closed (`additionalProperties: false`) schemas are dropped
// rather than rejected, matching oracle case argument-validation #0
// (`read {path: "v.txt", offset: 1}`).
//
// upstream: packages/ai/src/utils/validation.ts:1661-1699
func TestValidationDropsUndeclaredKeys(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	schemas := loadCapturedSchemas(t)
	var seenArgs map[string]any

	ext := sdk.New("test")
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "read",
		Label:       "Read",
		Description: "Read files",
		Parameters:  schemas["read"],
		Execute: func(_ sdk.Context, params map[string]any) (any, error) {
			seenArgs = params
			return "ok", nil
		},
	}))

	h := startFakeHost(t, ext, "sess-drop-extra")
	res, err := h.callTool("read", "c1", map[string]any{
		"path":   "v.txt",
		"offset": 1,
		"limit":  50,
	})
	if err != nil {
		t.Fatalf("read with extra keys failed: %v", err)
	}
	if string(res) != `"ok"` {
		t.Fatalf("result = %s, want %q", string(res), `"ok"`)
	}
	if seenArgs["path"] != "v.txt" {
		t.Fatalf("seenArgs = %+v, want path=v.txt", seenArgs)
	}
	if _, hasOffset := seenArgs["offset"]; hasOffset {
		t.Fatalf("undeclared key offset was not dropped: %+v", seenArgs)
	}
	if _, hasLimit := seenArgs["limit"]; hasLimit {
		t.Fatalf("undeclared key limit was not dropped: %+v", seenArgs)
	}
}

// TestValidationDropsNestedUndeclaredKeys verifies recursive unrecognized-key
// dropping on nested closed objects (`bash.ready`).
//
// upstream: packages/ai/src/utils/validation.ts:1661-1699
func TestValidationDropsNestedUndeclaredKeys(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	schemas := loadCapturedSchemas(t)
	var seenArgs map[string]any

	ext := sdk.New("test")
	ext.RegisterTool(Tool(sdk.ToolDefinition{
		Name:        "bash",
		Label:       "Bash",
		Description: "Run bash",
		Parameters:  schemas["bash"],
		Execute: func(_ sdk.Context, params map[string]any) (any, error) {
			seenArgs = params
			return "ok", nil
		},
	}))

	h := startFakeHost(t, ext, "sess-drop-nested")
	_, err := h.callTool("bash", "c1", map[string]any{
		"command": "echo hi",
		"ready": map[string]any{
			"port":  8080,
			"extra": "drop-me",
		},
	})
	if err != nil {
		t.Fatalf("bash with nested extra key failed: %v", err)
	}
	ready, _ := seenArgs["ready"].(map[string]any)
	if _, hasExtra := ready["extra"]; hasExtra {
		t.Fatalf("nested extra key was not dropped: %+v", ready)
	}
	if ready["port"] != float64(8080) {
		t.Fatalf("ready.port = %#v, want 8080", ready["port"])
	}
}

// TestValidationMatchesOmpWordingTable verifies omp v19.1.1's exact omptype
// validation error wording across every JSON Schema construct used in the 25
// captured schemas (`internal/schemaorder/omp_schemas.json`). Expected lines
// were generated directly from real omp v19.1.1's `packages/omptype`.
//
// upstream:
//   - packages/ai/src/utils/validation.ts:1814-1834, 2172-2188
//   - packages/omptype/src/errors.ts:90-198
//   - packages/omptype/src/ir.ts:1726-1798
func TestValidationMatchesOmpWordingTable(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	schemas := loadCapturedSchemas(t)

	cases := []struct {
		name     string
		tool     string
		args     map[string]any
		wantLine string
	}{
		{
			name:     "read/missing_path",
			tool:     "read",
			args:     map[string]any{},
			wantLine: "  - path: path must be Local path, internal URI, or URL; selectors inline. (was missing)",
		},
		// Note: `read {path:{a:1}}` is ACCEPTED by omp (`path: "{\"a\":1}"`),
		// covered in TestValidationCoercionsAndRepairs below.
		{
			name:     "write/missing_content",
			tool:     "write",
			args:     map[string]any{"path": "v2.txt"},
			wantLine: "  - content: content must be a string (was missing)",
		},
		{
			name:     "bash/timeout_string",
			tool:     "bash",
			args:     map[string]any{"command": "echo hi", "timeout": "soon"},
			wantLine: "  - timeout: timeout must be timeout in seconds; 0 disables the command deadline; nonzero values are clamped to 1-3600 (was a string)",
		},
		{
			name:     "bash/pty_bad_string",
			tool:     "bash",
			args:     map[string]any{"command": "echo hi", "pty": "maybe"},
			wantLine: "  - pty: pty must be boolean (was a string)",
		},
		{
			name:     "bash/name_pattern",
			tool:     "bash",
			args:     map[string]any{"command": "echo hi", "name": "!bad"},
			wantLine: `  - name: name must be starts a long-lived supervised service under this name; omit for finite commands, async jobs included. 1-48 of [A-Za-z0-9._-] (was "!bad")`,
		},
		{
			name:     "bash/ready_nested_port_string",
			tool:     "bash",
			args:     map[string]any{"command": "echo hi", "ready": map[string]any{"port": "bad"}},
			wantLine: "  - ready/port: ready.port must be TCP port the service accepts connections on once ready (was a string)",
		},
		{
			name:     "eval/bad_enum",
			tool:     "eval",
			args:     map[string]any{"language": "rb", "code": "puts 1"},
			wantLine: `  - language: language must be "py": Python; "js": Bun (was "rb")`,
		},
		{
			name:     "eval/missing_language",
			tool:     "eval",
			args:     map[string]any{"code": "puts 1"},
			wantLine: `  - language: language must be "py": Python; "js": Bun (was missing)`,
		},
		{
			name:     "web_search/bad_enum_no_desc",
			tool:     "web_search",
			args:     map[string]any{"query": "q", "recency": "hour"},
			wantLine: `  - recency: recency must be "day", "week", "month" or "year" (was "hour")`,
		},
		{
			name:     "goal/bad_enum_with_desc",
			tool:     "goal",
			args:     map[string]any{"op": "bogus"},
			wantLine: `  - op: op must be goal operation (was "bogus")`,
		},
		{
			name:     "goal/token_budget_float",
			tool:     "goal",
			args:     map[string]any{"op": "create", "token_budget": 1.5},
			wantLine: "  - token_budget: token_budget must be token budget (was 1.5)",
		},
		{
			name:     "goal/token_budget_string",
			tool:     "goal",
			args:     map[string]any{"op": "create", "token_budget": "abc"},
			wantLine: `  - token_budget: token_budget must be token budget (was "abc")`,
		},
		{
			name:     "lsp/timeout_below_min",
			tool:     "lsp",
			args:     map[string]any{"action": "status", "timeout": 2},
			wantLine: "  - timeout: timeout must be Timeout in seconds; raises or lowers the default (range 5–300). (was 2)",
		},
		{
			name:     "lsp/timeout_above_max",
			tool:     "lsp",
			args:     map[string]any{"action": "status", "timeout": 500},
			wantLine: "  - timeout: timeout must be Timeout in seconds; raises or lowers the default (range 5–300). (was 500)",
		},
		{
			name:     "grep/skip_bad_union",
			tool:     "grep",
			args:     map[string]any{"pattern": "x", "skip": "abc"},
			wantLine: "  - skip: skip must be a number or null (was a string)",
		},
		{
			name:     "ast_edit/empty_ops",
			tool:     "ast_edit",
			args:     map[string]any{"ops": []any{}, "paths": []any{"a.go"}},
			wantLine: "  - ops: ops must be rewrite ops (was 0)",
		},
		{
			name:     "ast_edit/nested_missing_out",
			tool:     "ast_edit",
			args:     map[string]any{"ops": []any{map[string]any{"pat": "foo"}}, "paths": []any{"a.go"}},
			wantLine: "  - ops/0/out: ops[0].out must be replacement template (was missing)",
		},
		// Note: `github {op:"pr_checkout", pr:123}` and `pr:[{bad:1}]` are
		// ACCEPTED by omp (`pr: "123"`, `pr: "[{\"bad\":1}]"`), covered in
		// TestValidationCoercionsAndRepairs below.
		{
			name:     "eval/described_boolean_reports_value",
			tool:     "eval",
			args:     map[string]any{"language": "py", "code": "1", "reset": "abc"},
			wantLine: `  - reset: reset must be Wipe only this kernel. (was "abc")`,
		},
		{
			name:     "bash/name_html_no_escape",
			tool:     "bash",
			args:     map[string]any{"command": "echo hi", "name": "<bad>"},
			wantLine: `  - name: name must be starts a long-lived supervised service under this name; omit for finite commands, async jobs included. 1-48 of [A-Za-z0-9._-] (was "<bad>")`,
		},
		{
			name:     "bash/name_utf16_length_over_40",
			tool:     "bash",
			args:     map[string]any{"command": "echo hi", "name": strings.Repeat("😀", 21)},
			wantLine: `  - name: name must be starts a long-lived supervised service under this name; omit for finite commands, async jobs included. 1-48 of [A-Za-z0-9._-] (was a string (length 42))`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := schemas[tc.tool]
			if schema == nil {
				t.Fatalf("missing captured schema for %q", tc.tool)
			}
			def := Tool(sdk.ToolDefinition{
				Name:        tc.tool,
				Label:       tc.tool,
				Description: tc.tool,
				Parameters:  schema,
				Execute:     func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
			})
			_, err := def.PrepareArguments(tc.args)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			msg := err.Error()
			wantHeader := `Validation failed for tool "` + tc.tool + `":`
			if !strings.HasPrefix(msg, wantHeader+"\n") {
				t.Fatalf("missing header %q in:\n%s", wantHeader, msg)
			}
			if !strings.Contains(msg, tc.wantLine) {
				t.Fatalf("got:\n%s\n\nwant line:\n%s", msg, tc.wantLine)
			}
			if !strings.Contains(msg, "\n\nReceived arguments:\n") {
				t.Fatalf("missing Received arguments block in:\n%s", msg)
			}
		})
	}
}

// TestValidationLenientToolsBypassGenericValidator verifies that lenient tools
// (glob, todo, ask, task, yield, or any tool opted in via
// SetLenientArgValidation) keep unknown keys and handle their own validation
// error wording rather than being intercepted by intent's generic validator.
//
// upstream: packages/agent/src/tool-arguments.ts:16-35
func TestValidationLenientToolsBypassGenericValidator(t *testing.T) {
	resetAllForTest(t)
	schemas := loadCapturedSchemas(t)

	// glob is lenient by default: PrepareArguments sees the unknown key.
	var globSaw map[string]any
	globDef := Tool(sdk.ToolDefinition{
		Name:       "glob",
		Parameters: schemas["glob"],
		PrepareArguments: func(params map[string]any) (map[string]any, error) {
			globSaw = params
			return params, nil
		},
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	})
	out, err := globDef.PrepareArguments(map[string]any{"bogus": 1, "limit": 0})
	if err != nil {
		t.Fatalf("lenient glob should not fail in generic validator: %v", err)
	}
	if globSaw["bogus"] != 1 {
		t.Fatalf("lenient glob did not receive unknown key: %+v", globSaw)
	}
	if out["limit"] != 0 {
		t.Fatalf("lenient glob limit = %#v, want 0", out["limit"])
	}

	// Custom opt-in via SetLenientArgValidation.
	SetLenientArgValidation("custom_lenient", true)
	t.Cleanup(func() { SetLenientArgValidation("custom_lenient", false) })
	if !IsLenientArgValidation("custom_lenient") {
		t.Fatalf("expected custom_lenient to be lenient")
	}
	customDef := Tool(sdk.ToolDefinition{
		Name:       "custom_lenient",
		Parameters: schemas["read"],
		Execute:    func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	})
	if _, err := customDef.PrepareArguments(map[string]any{}); err != nil {
		t.Fatalf("custom_lenient should bypass generic validator, got: %v", err)
	}
}

// TestMidSessionIntentTracingToggleReSyncsSchemas verifies WIRING REVIEW
// finding 7: changing `tools.intentTracing` mid-session (e.g. via
// `settings.OnChange(intent.NotifySettingChange)`) updates tool schemas on the
// next `before_agent_start` in both directions (on -> off and off -> on).
//
// upstream: packages/coding-agent/src/sdk.ts:5660-5666
func TestMidSessionIntentTracingToggleReSyncsSchemas(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	enabled := true
	Configured = func(_ sdk.Context) (bool, bool) {
		return enabled, true
	}

	readDef := sdk.ToolDefinition{
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
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	}

	ext := sdk.New("test")
	ext.RegisterTool(Tool(readDef))
	RegisterSync(ext, readDef)

	h := startFakeHostWithMode(t, ext, "sess-mid-toggle", "tui", true)
	h.emitEvent("session_start", map[string]any{})

	// Initially ON: schema advertises `i`.
	props := jsonObjectKeys(t, extractWireProperties(t, h.tool("read").Parameters))
	if len(props) == 0 || props[0] != Field {
		t.Fatalf("initial read properties = %v, want %q first", props, Field)
	}

	// Toggle OFF mid-session, notify, and fire before_agent_start.
	enabled = false
	NotifySettingChange("sess-mid-toggle", "tools.intentTracing")
	h.emitEvent(sdk.EventBeforeAgentStart, map[string]any{"systemPromptOptions": map[string]any{}})

	props = jsonObjectKeys(t, extractWireProperties(t, h.tool("read").Parameters))
	for _, k := range props {
		if k == Field {
			t.Fatalf("after turning intentTracing OFF, read schema still advertises %q: %v", Field, props)
		}
	}

	// Toggle back ON mid-session, notify, and fire before_agent_start.
	enabled = true
	NotifySettingChange("sess-mid-toggle", "tools.intentTracing")
	h.emitEvent(sdk.EventBeforeAgentStart, map[string]any{"systemPromptOptions": map[string]any{}})

	props = jsonObjectKeys(t, extractWireProperties(t, h.tool("read").Parameters))
	if len(props) == 0 || props[0] != Field {
		t.Fatalf("after turning intentTracing back ON, read properties = %v, want %q first", props, Field)
	}
}

// TestPINoIntentSplitsPromptAndSchemaInjection verifies WIRING REVIEW finding
// 10: `PI_NO_INTENT=1` disables wire-schema injection (`SchemaInjectionEnabled`
// == false) while leaving `PromptEnabled` == true when `tools.intentTracing` is
// active, matching omp (`packages/agent/src/agent-loop.ts:1085` vs
// `packages/coding-agent/src/sdk.ts:4655`).
func TestPINoIntentSplitsPromptAndSchemaInjection(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "1")
	t.Setenv("PI_INTENT_TRACING", "")

	Configured = func(_ sdk.Context) (bool, bool) {
		return true, true
	}

	var zero sdk.Context
	if !PromptEnabled(zero) {
		t.Fatalf("PromptEnabled = false under PI_NO_INTENT=1, want true (sdk.ts:4655)")
	}
	if SchemaInjectionEnabled(zero) {
		t.Fatalf("SchemaInjectionEnabled = true under PI_NO_INTENT=1, want false (agent-loop.ts:1085)")
	}

	// When tools.intentTracing is false, both are false.
	Configured = func(_ sdk.Context) (bool, bool) {
		return false, true
	}
	if PromptEnabled(zero) {
		t.Fatalf("PromptEnabled = true when tools.intentTracing=false, want false")
	}
	if SchemaInjectionEnabled(zero) {
		t.Fatalf("SchemaInjectionEnabled = true when tools.intentTracing=false, want false")
	}
}

// TestValidationCoercionsAndRepairs covers omp's validation.ts coercion and
// normalization passes: container-to-string, single-value-to-array, union
// scalar coercion, string-encoded array unions, identifier trailing-newline
// trim, double-encoded keys, flattened array properties, single-string field
// remap, padded "null ", malformed JSON healing, in-band arg spill healing,
// JS number formatting, and `original` before `normalized` in error output.
//
// upstream: packages/ai/src/utils/validation.ts:79-2290
func TestValidationCoercionsAndRepairs(t *testing.T) {
	resetAllForTest(t)
	schemas := loadCapturedSchemas(t)

	prep := func(tool string, args map[string]any) (map[string]any, error) {
		def := Tool(sdk.ToolDefinition{
			Name:       tool,
			Parameters: schemas[tool],
			Execute:    func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
		})
		return def.PrepareArguments(args)
	}

	// 1. Container-to-string (validation.ts:145-171): read {path:{a:1}} -> path:'{"a":1}'.
	if out, err := prep("read", map[string]any{"path": map[string]any{"a": 1}}); err != nil {
		t.Fatalf("read {path:{a:1}} rejected: %v", err)
	} else if out["path"] != `{"a":1}` {
		t.Fatalf("read path = %#v, want %q", out["path"], `{"a":1}`)
	}

	// 2. Single-value-to-array (validation.ts:1708-1721): ast_edit {ops:{pat,out}, paths:"src"}.
	if out, err := prep("ast_edit", map[string]any{
		"ops":   map[string]any{"pat": "foo", "out": "bar"},
		"paths": "src",
	}); err != nil {
		t.Fatalf("ast_edit singleton wrap rejected: %v", err)
	} else if paths, _ := out["paths"].([]any); len(paths) != 1 || paths[0] != "src" {
		t.Fatalf("ast_edit paths = %#v, want [src]", out["paths"])
	}

	// 3. Union scalar & container coercion (validation.ts:1677-1706): github pr:123 and pr:[{bad:1}].
	if out, err := prep("github", map[string]any{"op": "pr_checkout", "pr": 123}); err != nil {
		t.Fatalf("github pr:123 rejected: %v", err)
	} else if out["pr"] != "123" {
		t.Fatalf("github pr = %#v, want \"123\"", out["pr"])
	}
	if out, err := prep("github", map[string]any{"op": "pr_checkout", "pr": []any{map[string]any{"bad": 1}}}); err != nil {
		t.Fatalf("github pr:[{bad:1}] rejected: %v", err)
	} else if out["pr"] != `[{"bad":1}]` {
		t.Fatalf("github pr = %#v, want %q", out["pr"], `[{"bad":1}]`)
	}

	// 4. String-encoded array union (validation.ts:1260-1360): github pr:'["1","2"]'.
	if out, err := prep("github", map[string]any{"op": "pr_checkout", "pr": `["1","2"]`}); err != nil {
		t.Fatalf("github pr JSON array string rejected: %v", err)
	} else if arr, _ := out["pr"].([]any); len(arr) != 2 || arr[0] != "1" || arr[1] != "2" {
		t.Fatalf("github pr = %#v, want [1 2]", out["pr"])
	}

	// 5. Identifier trailing-newline trim (validation.ts:1047-1160): skips content keys.
	if out, err := prep("write", map[string]any{"path": "a.txt\r\n", "content": "hello\n"}); err != nil {
		t.Fatalf("write trailing newline rejected: %v", err)
	} else if out["path"] != "a.txt" || out["content"] != "hello\n" {
		t.Fatalf("write = %#v, want path=a.txt content=hello\\n", out)
	}

	// 6. Double-encoded keys (validation.ts:1186-1259): bash {"command":"x", "\"timeout\"":7}.
	if out, err := prep("bash", map[string]any{"command": "x", `"timeout"`: float64(7)}); err != nil {
		t.Fatalf("bash double-encoded key rejected: %v", err)
	} else if out["timeout"] != float64(7) {
		t.Fatalf("bash timeout = %#v, want 7", out["timeout"])
	}

	// 7. Flattened array properties (validation.ts:1488-1641): debug {"action":"launch", "args[0]":"abc"}.
	if out, err := prep("debug", map[string]any{"action": "launch", "args[0]": "abc"}); err != nil {
		t.Fatalf("debug flattened args[0] rejected: %v", err)
	} else if arr, _ := out["args"].([]any); len(arr) != 1 || arr[0] != "abc" {
		t.Fatalf("debug args = %#v, want [abc]", out["args"])
	}

	// 8. Single-string field remap (validation.ts:1410-1463): read {file_path:"a.txt"}.
	if out, err := prep("read", map[string]any{"file_path": "a.txt"}); err != nil {
		t.Fatalf("read single-string remap rejected: %v", err)
	} else if out["path"] != "a.txt" {
		t.Fatalf("read path = %#v, want a.txt", out["path"])
	}

	// 9. Padded "null " on optional field (validation.ts:422-438, 2197-2290).
	if out, err := prep("bash", map[string]any{"command": "x", "timeout": "null "}); err != nil {
		t.Fatalf("bash timeout:\"null \" rejected: %v", err)
	} else if _, has := out["timeout"]; has {
		t.Fatalf("bash timeout should be stripped, got %#v", out["timeout"])
	}

	// 10. Malformed JSON container healing (validation.ts:197-418): trailing junk.
	if out, err := prep("debug", map[string]any{"action": "launch", "args": `["a"] trailing`}); err != nil {
		t.Fatalf("debug trailing-junk array rejected: %v", err)
	} else if arr, _ := out["args"].([]any); len(arr) != 1 || arr[0] != "a" {
		t.Fatalf("debug args = %#v, want [a]", out["args"])
	}

	// 11. In-band <arg_key>/<arg_value> spill healing (validation.ts:1849-1999).
	if out, err := prep("write", map[string]any{"path": "a.txt</arg_key><arg_key>content</arg_key><arg_value>hi</arg_value>"}); err != nil {
		t.Fatalf("write spill heal rejected: %v", err)
	} else if out["path"] != "a.txt" || out["content"] != "hi" {
		t.Fatalf("write spill = %#v, want path=a.txt content=hi", out)
	}

	// 12. JS Number.toString semantics: 1e21 -> "1e+21", 1e-7 -> "1e-7", -0 -> "0".
	if got := jsNumberToString(1e21); got != "1e+21" {
		t.Fatalf("jsNumberToString(1e21) = %q, want 1e+21", got)
	}
	if got := jsNumberToString(1e-7); got != "1e-7" {
		t.Fatalf("jsNumberToString(1e-7) = %q, want 1e-7", got)
	}
	negZero := math.Copysign(0, -1)
	if got := jsNumberToString(negZero); got != "0" {
		t.Fatalf("jsNumberToString(-0) = %q, want 0", got)
	}

	// 13. `original` appears before `normalized` when arguments were changed and still failed.
	_, err := prep("lsp", map[string]any{"action": "status", "timeout": "2"})
	if err == nil {
		t.Fatalf("expected lsp timeout:\"2\" error")
	}
	msg := err.Error()
	origIdx := strings.Index(msg, `"original":`)
	normIdx := strings.Index(msg, `"normalized":`)
	if origIdx == -1 || normIdx == -1 || origIdx >= normIdx {
		t.Fatalf("expected \"original\" before \"normalized\" in:\n%s", msg)
	}
}

// TestValidationStringSliceEnum verifies finding 2: schemas whose `enum` is
// declared as Go `[]string` (as in debug, github, goal) are validated,
// whitespace-trimmed, and optional-empty-stripped by the wrapper.
func TestValidationStringSliceEnum(t *testing.T) {
	resetAllForTest(t)
	schema := sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type":        "string",
				"description": "goal operation",
				"enum":        []string{"create", "list"},
			},
			"access_type": map[string]any{
				"type": "string",
				"enum": []string{"read", "write"},
			},
		},
		"required":             []string{"op"},
		"additionalProperties": false,
	}
	def := Tool(sdk.ToolDefinition{
		Name:       "goal",
		Parameters: schema,
		Execute:    func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	})

	// Rejects bogus enum value in omp wording.
	if _, err := def.PrepareArguments(map[string]any{"op": "bogus"}); err == nil || !strings.Contains(err.Error(), `op must be goal operation (was "bogus")`) {
		t.Fatalf("got err = %v, want omp enum error", err)
	}
	// Trims whitespace around valid enum and strips optional empty enum string.
	out, err := def.PrepareArguments(map[string]any{"op": " create ", "access_type": ""})
	if err != nil {
		t.Fatalf("trimmed enum + optional empty rejected: %v", err)
	}
	if out["op"] != "create" {
		t.Fatalf("op = %#v, want create", out["op"])
	}
	if _, has := out["access_type"]; has {
		t.Fatalf("optional empty enum access_type was not stripped: %#v", out)
	}
}

// TestValidationLenientToolsNormalizeBeforePrepare verifies finding 4: lenient
// tools run omp's normalization with keepUnknownRootKeys=true and pass the
// normalized arguments to the tool's own PrepareArguments when validation
// succeeds, or raw arguments (minus sentinels) on failure.
func TestValidationLenientToolsNormalizeBeforePrepare(t *testing.T) {
	resetAllForTest(t)
	schemas := loadCapturedSchemas(t)

	var saw map[string]any
	globDef := Tool(sdk.ToolDefinition{
		Name:       "glob",
		Parameters: schemas["glob"],
		PrepareArguments: func(params map[string]any) (map[string]any, error) {
			saw = params
			return params, nil
		},
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	})

	// Valid after normalization: limit:"5" -> 5, hidden:null stripped, path newline trimmed.
	if _, err := globDef.PrepareArguments(map[string]any{
		"path":   "src\n",
		"limit":  "5",
		"hidden": nil,
	}); err != nil {
		t.Fatalf("lenient glob normalization failed: %v", err)
	}
	if saw["path"] != "src" || saw["limit"] != float64(5) {
		t.Fatalf("lenient glob saw %#v, want path=src limit=5", saw)
	}
	if _, hasHidden := saw["hidden"]; hasHidden {
		t.Fatalf("lenient glob did not strip optional null hidden: %#v", saw)
	}
}

// TestSyncSessionIsolationAndNoSpuriousReRegister verifies findings 3, 5, 14:
//   - Tool() definitions (like a subagent's `yield`) are NEVER registered into
//     another session by SyncSessionTools.
//   - ToolForContext() variants are scoped strictly to their own session.
//   - Calling RegisterLifecycle repeatedly installs hooks only once per
//     extension, and session_start followed by before_agent_start with unchanged
//     state issues 0 registerTool calls.
//   - Ending a session via session_shutdown releases its extDefs/sessionDefs.
func TestSyncSessionIsolationAndNoSpuriousReRegister(t *testing.T) {
	resetAllForTest(t)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	enabled := true
	Configured = func(_ sdk.Context) (bool, bool) { return enabled, true }

	// Simulate a subagent wrapping a custom `yield` tool via Tool().
	_ = Tool(sdk.ToolDefinition{
		Name:       "yield",
		Parameters: sdk.Schema{"type": "object", "properties": map[string]any{}},
		Execute:    func(_ sdk.Context, _ map[string]any) (any, error) { return "yield", nil },
	})

	readDef := sdk.ToolDefinition{
		Name:       "read",
		Parameters: sdk.Schema{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		Execute:    func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
	}

	ext := sdk.New("test")
	ext.RegisterTool(Tool(readDef))
	RegisterSync(ext, readDef)
	RegisterLifecycle(ext)
	RegisterLifecycle(ext) // duplicate calls must be no-ops

	h := startFakeHostWithMode(t, ext, "sess-iso-1", "tui", true)
	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	h.resetRegisterCalls()

	// First before_agent_start with unchanged state -> 0 registerTool calls.
	h.emitEvent(sdk.EventBeforeAgentStart, map[string]any{"systemPromptOptions": map[string]any{}})
	if calls := h.registeredCallNames(); len(calls) != 0 {
		t.Fatalf("unchanged before_agent_start issued registerTool calls: %v", calls)
	}

	// Toggle OFF -> only `read` is re-registered, NEVER `yield`.
	enabled = false
	NotifySettingChange("sess-iso-1", "tools.intentTracing")
	h.emitEvent(sdk.EventBeforeAgentStart, map[string]any{"systemPromptOptions": map[string]any{}})
	calls := h.registeredCallNames()
	if len(calls) != 1 || calls[0] != "read" {
		t.Fatalf("after toggle, registerTool calls = %v, want [read]", calls)
	}
	// Shut down session -> extDefs and lifecycleHooked for ext are released.
	h.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "exit"})
	wp := weak.Make(ext)
	storeMu.RLock()
	_, hasExtDefs := extDefs[wp]
	_, hasHooked := lifecycleHooked[wp]
	_, hasSessDefs := sessionDefs["sess-iso-1"]
	storeMu.RUnlock()
	if hasExtDefs || hasHooked || hasSessDefs {
		t.Fatalf("session state leaked after shutdown: extDefs=%v hooked=%v sessionDefs=%v", hasExtDefs, hasHooked, hasSessDefs)
	}
}

// TestReleasedSessionsTombstoneRingBounded verifies Final Code Review finding 9:
// ending more than maxEndedSessionTombstones sessions evicts the oldest
// tombstones so releasedSessions stays bounded instead of growing forever,
// while still tombstoning recent ended sessions and removing reopened entries
// from both the map and ring.
func TestReleasedSessionsTombstoneRingBounded(t *testing.T) {
	resetAllForTest(t)

	total := maxEndedSessionTombstones + 128
	for i := range total {
		releaseSession(fmt.Sprintf("ended-%d", i))
	}

	storeMu.RLock()
	mapLen := len(releasedSessions)
	ringLen := len(releasedOrder)
	oldestStillPresent := releasedSessions["ended-0"]
	newestStillPresent := releasedSessions[fmt.Sprintf("ended-%d", total-1)]
	storeMu.RUnlock()

	if mapLen != maxEndedSessionTombstones || ringLen != maxEndedSessionTombstones {
		t.Fatalf("tombstone sizes = (map=%d, ring=%d), want (%d, %d)", mapLen, ringLen, maxEndedSessionTombstones, maxEndedSessionTombstones)
	}
	if oldestStillPresent {
		t.Fatalf("oldest ended session ended-0 was not evicted from releasedSessions")
	}
	if !newestStillPresent {
		t.Fatalf("newest ended session was not present in releasedSessions")
	}

	// Reopening a tombstoned session removes it from both map and ring.
	newestID := fmt.Sprintf("ended-%d", total-1)
	reopenSession(newestID)
	storeMu.RLock()
	reopenedMapLen := len(releasedSessions)
	reopenedRingLen := len(releasedOrder)
	stillReleased := releasedSessions[newestID]
	storeMu.RUnlock()
	if stillReleased || reopenedMapLen != maxEndedSessionTombstones-1 || reopenedRingLen != maxEndedSessionTombstones-1 {
		t.Fatalf("reopenSession left inconsistent state: released=%v map=%d ring=%d", stillReleased, reopenedMapLen, reopenedRingLen)
	}
}

// TestUnstartedExtensionCleanupFreesTrackedDefs verifies Final Code Review
// finding 9: an *sdk.Extension registered via RegisterSync/RegisterLifecycle
// that never receives session_start is still garbage-collectible and has its
// extDefs/lifecycleHooked entries freed by runtime.AddCleanup.
func TestUnstartedExtensionCleanupFreesTrackedDefs(t *testing.T) {
	resetAllForTest(t)

	var wp weak.Pointer[sdk.Extension]
	func() {
		ext := sdk.New("never-started")
		def := sdk.ToolDefinition{
			Name:       "read",
			Parameters: sdk.Schema{"type": "object", "properties": map[string]any{}},
			Execute:    func(_ sdk.Context, _ map[string]any) (any, error) { return "ok", nil },
		}
		RegisterSync(ext, def)
		wp = weak.Make(ext)
		storeMu.RLock()
		_, hasDefs := extDefs[wp]
		_, hasHook := lifecycleHooked[wp]
		storeMu.RUnlock()
		if !hasDefs || !hasHook {
			t.Fatalf("expected extDefs=%v lifecycleHooked=%v before GC", hasDefs, hasHook)
		}
		runtime.KeepAlive(ext)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		storeMu.RLock()
		_, hasDefs := extDefs[wp]
		_, hasHook := lifecycleHooked[wp]
		_, hasArmed := extCleanupArmed[wp]
		storeMu.RUnlock()
		if !hasDefs && !hasHook && !hasArmed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	storeMu.RLock()
	_, hasDefs := extDefs[wp]
	_, hasHook := lifecycleHooked[wp]
	_, hasArmed := extCleanupArmed[wp]
	storeMu.RUnlock()
	t.Fatalf("unstarted extension entries leaked after GC: extDefs=%v lifecycleHooked=%v extCleanupArmed=%v", hasDefs, hasHook, hasArmed)
}
