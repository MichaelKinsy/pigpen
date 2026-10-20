package pi_subagents

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// testdata/workflow-validate.json is the original's own validateWorkflowScript (scripted-workflow.ts:1904) recorded over a
// corpus of scripts with port/gen-workflow-fixtures.mjs; the Go validator, which reads goja's tree, must answer the same.
type workflowValidateCase struct {
	Script string `json:"script"`
	Opts   *struct {
		Agent  bool `json:"agent"`
		Budget *int `json:"budget"`
	} `json:"opts"`
	Result struct {
		OK       bool                              `json:"ok"`
		Errors   []WorkflowScriptValidationError   `json:"errors"`
		Warnings []WorkflowScriptValidationWarning `json:"warnings"`
	} `json:"result"`
}

func TestWorkflowValidateMatchesTheOriginal(t *testing.T) {
	raw, err := os.ReadFile("testdata/workflow-validate.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []workflowValidateCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		opts := WorkflowScriptValidationOptions{}
		if c.Opts != nil {
			if c.Opts.Agent {
				opts.AgentNameError = func(name string) string {
					if name == "ghost" {
						return "Unknown agent '" + name + "'."
					}
					return ""
				}
			}
			opts.MaxSubagentSpawnsPerRun = c.Opts.Budget
		}
		got := ValidateWorkflowScript(c.Script, opts)
		if got.OK != c.Result.OK || !reflect.DeepEqual(nilIfEmpty(got.Errors), nilIfEmpty(c.Result.Errors)) || !reflect.DeepEqual(nilIfEmptyW(got.Warnings), nilIfEmptyW(c.Result.Warnings)) {
			gj, _ := json.Marshal(map[string]any{"ok": got.OK, "errors": got.Errors, "warnings": got.Warnings})
			wj, _ := json.Marshal(c.Result)
			t.Errorf("case %d %q\n got  %s\n want %s", i, c.Script, gj, wj)
		}
	}
}

func nilIfEmpty(e []WorkflowScriptValidationError) []WorkflowScriptValidationError {
	if len(e) == 0 {
		return nil
	}
	return e
}

func nilIfEmptyW(e []WorkflowScriptValidationWarning) []WorkflowScriptValidationWarning {
	if len(e) == 0 {
		return nil
	}
	return e
}
