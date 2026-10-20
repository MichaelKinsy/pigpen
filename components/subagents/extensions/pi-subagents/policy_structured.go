package pi_subagents

import "fmt"

// Port of src/runs/shared/structured-output.ts.

// assertJSONSchemaObject is the original's assertJsonSchemaObject: schema must be a JSON Schema object (not null, not an
// array). label names it in the error ("outputSchema" by default in the original).
func assertJSONSchemaObject(schema any, label string) error {
	if _, ok := asObject(schema); !ok {
		return fmt.Errorf("%s must be a JSON Schema object.", label)
	}
	return nil
}
