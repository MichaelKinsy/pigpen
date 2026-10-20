package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The child role of this extension. A child `pig` process is started with `-e <this extension>` and PIG_SUBAGENT_CHILD_ROLE naming
// a JSON file; the extension then registers what the original's child runtime registers for a child
// (src/runs/shared/subagent-prompt-runtime.ts), and none of the parent's tools:
//
//   - `structured_output`: validates the value against the step's outputSchema, persists it (and the acceptance report) to the
//     capture files the parent reads after the child exits (the original's file capture, policy_structured.go), and ends the step
//     (`terminate`).
//
// The parent finds this extension's path through the SourceInfo of its own `subagent` tool (extensionSourcePath).

const childRoleEnv = "PIG_SUBAGENT_CHILD_ROLE"

// structuredRoleConfig is what the child needs to capture its structured output: the files of the parent's
// structuredOutputRuntime.
type structuredRoleConfig struct {
	SchemaPath               string `json:"schemaPath"`
	OutputPath               string `json:"outputPath"`
	AcceptanceReportPath     string `json:"acceptanceReportPath,omitempty"`
	AcceptanceReportRequired bool   `json:"acceptanceReportRequired,omitempty"`
}

// childRoleConfig is the content of the file PIG_SUBAGENT_CHILD_ROLE names.
type childRoleConfig struct {
	Structured *structuredRoleConfig `json:"structuredOutput,omitempty"`
	// Watchdog is the child watchdog's config (the original's ChildWatchdogConfig, JSON), WatchdogStatusFile the file the child
	// appends its status events to, one JSON line each; the watchdog slice reads both (watchdog_child.go).
	Watchdog           json.RawMessage `json:"watchdog,omitempty"`
	WatchdogStatusFile string          `json:"watchdogStatusFile,omitempty"`
	// RequiredTools are the tool names the child's runtime must register besides structured_output (`watchdog_diff`, ...).
	RequiredTools []string `json:"requiredTools,omitempty"`
	// Err is why the role file could not be read (the child then registers nothing it cannot honor).
	Err error `json:"-"`
}

// childRole is the role this process was started with, nil for the parent.
func childRole() *childRoleConfig {
	path := os.Getenv(childRoleEnv)
	if path == "" {
		return nil
	}
	cfg := &childRoleConfig{}
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, cfg)
	}
	if err != nil {
		cfg.Err = fmt.Errorf("%s=%s: %w", childRoleEnv, path, err)
	}
	return cfg
}

// writeChildRoleConfig stores the role file next to the capture files of a structured output runtime and returns its path.
func writeChildRoleConfig(rt *structuredOutputRuntime) (string, error) {
	cfg := childRoleConfig{Structured: &structuredRoleConfig{SchemaPath: rt.SchemaPath, OutputPath: rt.OutputPath,
		AcceptanceReportPath: rt.AcceptanceReportPath, AcceptanceReportRequired: rt.AcceptanceReportRequired}}
	return writeChildRole(filepath.Dir(rt.SchemaPath), &cfg)
}

// writeChildRole stores cfg as role.json in dir and returns its path (what PIG_SUBAGENT_CHILD_ROLE names).
func writeChildRole(dir string, cfg *childRoleConfig) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "role.json")
	return path, os.WriteFile(path, b, 0o600)
}

// childRoleExtras are the registrations other slices add to a child (the child-side watchdog, its diff tool); each is called
// with the child's extension and role, in order, after the structured output tool. A returned error is reported on stderr.
var childRoleExtras []func(e *sdk.Extension, cfg *childRoleConfig) error

// childExtension is the extension of a child process: the tools of its role.
func childExtension(cfg *childRoleConfig) *sdk.Extension {
	e := sdk.New(extensionID)
	if cfg.Err != nil {
		fmt.Fprintln(os.Stderr, "pi-subagents child role:", cfg.Err)
		return e
	}
	if cfg.Structured != nil {
		if err := registerStructuredOutputTool(e, cfg.Structured); err != nil {
			fmt.Fprintln(os.Stderr, "pi-subagents child role:", err)
		}
	}
	for _, extra := range childRoleExtras {
		if err := extra(e, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "pi-subagents child role:", err)
		}
	}
	return e
}

// structuredOutputAcceptanceMode is the report mode of the role: "" (off), "optional" or "required".
func (c *structuredRoleConfig) acceptanceMode() string {
	switch {
	case c.AcceptanceReportPath == "":
		return ""
	case c.AcceptanceReportRequired:
		return "required"
	}
	return "optional"
}

const structuredOutputToolDescription = "Submit the required final structured output for this subagent step. This terminates the step."

func registerStructuredOutputTool(e *sdk.Extension, cfg *structuredRoleConfig) error {
	raw, err := os.ReadFile(cfg.SchemaPath)
	if err != nil {
		return err
	}
	schema, msg := jsJSONParse(string(raw))
	if msg != "" {
		return fmt.Errorf("%s: %s", cfg.SchemaPath, msg)
	}
	rt := &structuredOutputRuntime{Schema: schema, SchemaPath: cfg.SchemaPath, OutputPath: cfg.OutputPath,
		AcceptanceReportPath: cfg.AcceptanceReportPath, AcceptanceReportRequired: cfg.AcceptanceReportRequired}
	var params sdk.Schema
	if err := json.Unmarshal([]byte(marshalJSON(createStructuredOutputToolParameters(schema, cfg.acceptanceMode()), "")), &params); err != nil {
		return err
	}
	capture := createStructuredOutputFileCapture(rt)
	e.RegisterTool(sdk.ToolDefinition{
		Name: "structured_output", Label: "Structured Output", Description: structuredOutputToolDescription, Parameters: params,
		Execute: func(_ sdk.Context, in map[string]any) (any, error) {
			return executeStructuredOutput(rt, capture, in)
		},
	})
	return nil
}

// executeStructuredOutput is the original's structured_output tool: the value is validated before anything is captured, a
// required acceptance report must be there and valid, and the result ends the step.
func executeStructuredOutput(rt *structuredOutputRuntime, capture func(value, report any, hasReport bool) error, in map[string]any) (any, error) {
	value := in["value"]
	if ok, msg := validateStructuredOutputValue(rt.Schema, value); !ok {
		return nil, sdk.NewToolError("Structured output validation failed: " + msg)
	}
	report, hasReport := in["acceptanceReport"]
	if rt.AcceptanceReportRequired && !hasReport {
		return nil, sdk.NewToolError(missingStructuredAcceptanceReportError)
	}
	if rt.AcceptanceReportRequired {
		if validated, errs := validateAcceptanceReport(report, "acceptanceReport"); validated == nil {
			return nil, sdk.NewToolError("Invalid structured output acceptance report: " + strings.Join(errs, "; "))
		}
	}
	if err := capture(value, report, hasReport && rt.AcceptanceReportPath != ""); err != nil {
		return nil, err
	}
	return sdk.ToolResult{Content: "Structured output captured.", Details: map[string]any{}, Terminate: true}, nil
}
