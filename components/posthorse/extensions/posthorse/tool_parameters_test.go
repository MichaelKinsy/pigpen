package posthorse

import (
	"os"
	"strings"
	"testing"
)

// TestToolParametersMatchTypeBox: the `parameters` of every registered tool go on the wire as the
// JSON text TypeBox writes in Pi, member order included (the model sees these bytes).
// testdata/typebox_tool_parameters.txt holds `JSON.stringify(tool.parameters)` of each tool of the
// unmodified original (port/oracle/index.ts), captured under Node 24 with TypeBox 1.3.27 by loading
// the original with a recording `registerTool`.
func TestToolParametersMatchTypeBox(t *testing.T) {
	raw, err := os.ReadFile("testdata/typebox_tool_parameters.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		name, schema, _ := strings.Cut(line, " ")
		want[name] = schema
	}
	tools, _ := fxCaptureRegistration(t, Extension())
	seen := 0
	for _, tool := range tools {
		expected, ok := want[tool.Name]
		if !ok {
			t.Errorf("tool %q has no captured TypeBox parameters", tool.Name)
			continue
		}
		seen++
		if got := string(tool.Parameters); got != expected {
			t.Errorf("%s parameters on the wire:\n got %s\nwant %s", tool.Name, got, expected)
		}
	}
	if seen != len(want) {
		t.Errorf("registered %d of the %d original tools", seen, len(want))
	}
}
