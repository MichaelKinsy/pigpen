package pi_subagents

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/workflow-json-errors.json holds V8's own JSON.parse messages (Node 24) for a corpus of malformed inputs,
// recorded with port/gen-workflow-json-fixtures.mjs: the sandbox's JSON.parse must fail with the same text.
func TestWorkflowJSONErrorsMatchV8(t *testing.T) {
	raw, err := os.ReadFile("testdata/workflow-json-errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Text  string  `json:"text"`
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := workflowJSONError(c.Text)
		if c.Error == nil {
			if got != "" {
				t.Errorf("%q: valid JSON reported as %q", c.Text, got)
			}
			continue
		}
		if got != *c.Error {
			t.Errorf("%q:\n got  %q\n want %q", c.Text, got, *c.Error)
		}
	}
}
