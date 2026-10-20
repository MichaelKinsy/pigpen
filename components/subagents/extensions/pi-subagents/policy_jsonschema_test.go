package pi_subagents

import (
	"os"
	"testing"
)

// TestJSONSchemaErrorsMatchTypeBox compares the validator with TypeBox 1.3.27 (the version the original compiles its schemas
// with): for each case the verdict and the list of errors (keyword, paths, params, message), in order. The golden file is what
// `Compile(schema)` gave on Node 24.19.0 (see port/PORT.md).
func TestJSONSchemaErrorsMatchTypeBox(t *testing.T) {
	raw, err := os.ReadFile("testdata/jsonschema_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	cases, _ := asArray(doc)
	if len(cases) < 100 {
		t.Fatalf("only %d cases", len(cases))
	}
	for i, c := range cases {
		co, _ := asObject(c)
		schema, value := co.vals["schema"], co.vals["value"]
		wantValid := co.vals["valid"] == true
		valid, errs, _ := schemaErrors(schema, value)
		name := marshalJSON(schema, "") + " <- " + marshalJSON(value, "")
		if valid != wantValid {
			t.Errorf("case %d %s: valid=%v want %v (got %v)", i, name, valid, wantValid, errs)
			continue
		}
		want, _ := asArray(co.vals["errors"])
		if len(errs) != len(want) {
			t.Errorf("case %d %s: %d errors, want %d\n got %v\nwant %s", i, name, len(errs), len(want), errs, marshalJSON(co.vals["errors"], ""))
			continue
		}
		for j, w := range want {
			wo, _ := asObject(w)
			e := errs[j]
			p := e.Params
			if p == nil {
				p = newObject()
			}
			if e.Keyword != wo.vals["keyword"] || e.SchemaPath != wo.vals["schemaPath"] || e.InstancePath != wo.vals["instancePath"] || e.Message != wo.vals["message"] || canonicalJSON(p) != canonicalJSON(wo.vals["params"]) {
				t.Errorf("case %d %s error %d:\n got %s %s %s %q %s\nwant %s", i, name, j, e.Keyword, e.SchemaPath, e.InstancePath, e.Message, marshalJSON(p, ""), marshalJSON(w, ""))
			}
		}
	}
}
