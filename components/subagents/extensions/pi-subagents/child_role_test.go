package pi_subagents

import (
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestChildRoleStructuredOutput(t *testing.T) {
	t.Setenv(childRoleEnv, "")
	equal(t, childRole() == nil, true, "a parent has no role")
	schema := js(t, `{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}},"additionalProperties":false}`)
	rt, err := createStructuredOutputRuntime(schema, t.TempDir(), "required")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupStructuredOutputRuntime(rt)
	rolePath, err := writeChildRoleConfig(rt)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(childRoleEnv, rolePath)
	role := childRole()
	if role == nil || role.Err != nil || role.Structured == nil {
		t.Fatalf("role: %+v", role)
	}
	equal(t, *role.Structured, structuredRoleConfig{SchemaPath: rt.SchemaPath, OutputPath: rt.OutputPath, AcceptanceReportPath: rt.AcceptanceReportPath, AcceptanceReportRequired: true}, "role file")
	equal(t, role.Structured.acceptanceMode(), "required", "mode")
	equal(t, (&structuredRoleConfig{AcceptanceReportPath: "x"}).acceptanceMode(), "optional", "optional mode")
	equal(t, (&structuredRoleConfig{}).acceptanceMode(), "", "no report")

	capture := createStructuredOutputFileCapture(rt)
	_, err = executeStructuredOutput(rt, capture, map[string]any{"value": map[string]any{"ok": "yes"}})
	if err == nil {
		t.Fatal("a value that does not match the schema was captured")
	}
	polMatches(t, err.Error(), `^Structured output validation failed: `)
	_, err = executeStructuredOutput(rt, capture, map[string]any{"value": map[string]any{"ok": true}})
	equal(t, err.Error(), missingStructuredAcceptanceReportError, "a required report")
	if _, statErr := os.Stat(rt.OutputPath); !os.IsNotExist(statErr) {
		t.Error("nothing is captured before the call is accepted")
	}
	_, err = executeStructuredOutput(rt, capture, map[string]any{"value": map[string]any{"ok": true}, "acceptanceReport": "nope"})
	polMatches(t, err.Error(), `^Invalid structured output acceptance report: `)

	report := map[string]any{}
	res, err := executeStructuredOutput(rt, capture, map[string]any{"value": map[string]any{"ok": true}, "acceptanceReport": report})
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := res.(sdk.ToolResult)
	equal(t, ok && tr.Terminate && tr.Content == "Structured output captured.", true, "the result ends the step")
	value, msg := readStructuredOutput(rt)
	equal(t, msg, "", "read back")
	equal(t, marshalJSON(value, ""), `{"ok":true}`, "captured value")
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(rt.OutputPath), "acceptance-report.json"))
	equal(t, len(b) > 0, true, "captured report")

	// a role file that cannot be read leaves the child with nothing it cannot honor
	t.Setenv(childRoleEnv, filepath.Join(t.TempDir(), "missing.json"))
	bad := childRole()
	equal(t, bad != nil && bad.Err != nil, true, "unreadable role")
}
