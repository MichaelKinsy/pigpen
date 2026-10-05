package extension_equivalence

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A model sees only a tool's schema. A parameter the handler reads but the schema does not declare
// is invisible to it: the README promised `unitOnly` for equivalence_run, and the schema had neither
// `unitOnly` nor `jobs` (review of the porter-driver lane).
func TestToolSchemasDeclareEveryParameterTheHandlersRead(t *testing.T) {
	b, err := os.ReadFile("extension.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	between := func(from, to string) string {
		t.Helper()
		i := strings.Index(src, from)
		if i < 0 {
			t.Fatalf("%q not found in extension.go", from)
		}
		j := strings.Index(src[i:], to)
		if j < 0 {
			t.Fatalf("%q not found after %q", to, from)
		}
		return src[i : i+j]
	}
	read := regexp.MustCompile(`(?:str|num)\(params, "([A-Za-z]+)"\)|params\["([A-Za-z]+)"\]`)
	declared := regexp.MustCompile(`"([A-Za-z]+)":\s+map\[string\]any\{"type"`)
	for _, tool := range []struct{ name, schema, handler string }{
		{"equivalence_run", between(`e.Tool("equivalence_run"`, "runTool)"), between("func runTool(", "func verdictText(")},
		{"equivalence_twins", between(`e.Tool("equivalence_twins"`, "twinsTool)"), between("func twinsTool(", "\n}\n")},
	} {
		props := map[string]bool{}
		for _, m := range declared.FindAllStringSubmatch(tool.schema, -1) {
			props[m[1]] = true
		}
		for _, m := range read.FindAllStringSubmatch(tool.handler, -1) {
			if name := m[1] + m[2]; !props[name] {
				t.Errorf("%s reads parameter %q, which its schema does not declare", tool.name, name)
			}
		}
	}
}
