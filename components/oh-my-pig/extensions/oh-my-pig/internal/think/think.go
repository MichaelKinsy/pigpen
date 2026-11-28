// Package think ports omp's hidden `think` private-scratchpad tool (`externalThinking`).
//
// upstream: packages/coding-agent/src/tools/think.ts:1-72
// upstream: packages/coding-agent/src/tools/index.ts:779-781,826
package think

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

const (
	// SettingExternalThinking is the setting key gating the `think` tool (default false).
	// upstream: packages/coding-agent/src/config/settings-schema (externalThinking)
	SettingExternalThinking = "externalThinking"

	thinkDescription = "private scratchpad; not shown to user"
)

// Setting reads a raw setting value for ctx (e.g. `externalThinking`).
// Wired by wiring.go -> settings.Any. Defaults to nil (false).
var Setting func(ctx sdk.Context, key string) any

// IsSubagent reports whether ctx belongs to a subagent session (taskDepth > 0).
// Wired by wiring.go -> agents.IsSubagent. Defaults to nil (false).
var IsSubagent func(ctx sdk.Context) bool

// Params holds the arguments for the `think` tool.
// upstream: packages/coding-agent/src/tools/think.ts:39-44
type Params struct {
	Thoughts string `json:"thoughts"`
}

func thinkSchema() sdk.Schema {
	// upstream: packages/coding-agent/src/tools/think.ts:39-42
	return sdk.Schema{
		"type":        "object",
		"description": thinkDescription,
		"properties": map[string]any{
			"thoughts": map[string]any{
				"type":        "string",
				"description": thinkDescription,
			},
		},
		"required":             []any{"thoughts"},
		"additionalProperties": false,
	}
}

// SupportsModel reports whether the active model (when populated on ctx) is
// compatible with external thinking. Returns true when model info is absent
// (e.g. unit tests or before first model snapshot) or when the model is not a
// known reasoning-incompatible transport.
// upstream: packages/coding-agent/src/tools/think.ts:6-37
func SupportsModel(ctx sdk.Context) bool {
	var prov, id string
	// A zero sdk.Context (used in unit tests) has no attached extension/host
	// connection; guard against nil dereference when reading model metadata.
	func() {
		defer func() { _ = recover() }()
		if info, err := ctx.GetModelInfo(); err == nil && info != nil {
			prov = strings.ToLower(strings.TrimSpace(info.Provider))
			id = strings.ToLower(strings.TrimSpace(info.ID))
		}
	}()
	if prov == "" && id == "" {
		return true
	}
	// OpenAI o-series / Codex Responses models require native reasoning streams.
	if strings.Contains(id, "codex") {
		return false
	}
	if prov == "openai" && (strings.HasPrefix(id, "o1") || strings.HasPrefix(id, "o3") || strings.HasPrefix(id, "o4")) {
		return false
	}
	return true
}

// IsEnabled reports whether `externalThinking` is enabled for ctx and the
// active model (when populated) supports external thinking.
// Default-off: returns false when Setting is nil or not true.
// upstream: packages/coding-agent/src/tools/index.ts:779-781,826
func IsEnabled(ctx sdk.Context) bool {
	if Setting == nil {
		return false
	}
	v, ok := Setting(ctx, SettingExternalThinking).(bool)
	if !ok || !v {
		return false
	}
	return SupportsModel(ctx)
}

// Register installs the hidden `think` tool with DefaultActive=false and
// registers a `toolcmd.RegisterAutoActivate` gate behind `externalThinking`.
// Intentionally NOT wrapped with `intent.Tool` because upstream declares
// `readonly intent = "omit"` (packages/coding-agent/src/tools/think.ts:59).
//
// upstream: packages/coding-agent/src/tools/think.ts:50-72
func Register(e *sdk.Extension) {
	defaultOff := false
	def := sdk.ToolDefinition{
		Name:          "think",
		Label:         "Think",
		Description:   thinkDescription,
		Parameters:    thinkSchema(),
		DefaultActive: &defaultOff,
		Execute:       executeThink,
	}
	e.RegisterTool(schemaorder.Apply(def))
	toolcmd.RegisterAutoActivate("think", IsEnabled)
}

// Execute runs the `think` tool directly (exported for unit tests).
// upstream: packages/coding-agent/src/tools/think.ts:61-71
func Execute(_ sdk.Context, _ Params) (*sdk.ToolResult, error) {
	return &sdk.ToolResult{
		Content: "------",
		Details: map[string]any{"recorded": true},
	}, nil
}

func executeThink(ctx sdk.Context, raw map[string]any) (any, error) {
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid think arguments: %w", err)
	}
	var params Params
	if err := json.Unmarshal(buf, &params); err != nil {
		return nil, fmt.Errorf("invalid think arguments: %w", err)
	}
	if _, hasThoughts := raw["thoughts"]; !hasThoughts {
		return nil, fmt.Errorf("think requires \"thoughts\" parameter")
	}
	if s, ok := raw["thoughts"].(string); ok {
		params.Thoughts = s
	} else {
		return nil, fmt.Errorf("think \"thoughts\" must be a string")
	}
	// Reject unexpected keys (mirrors `"+": "reject"` / `additionalProperties: false`).
	for k := range raw {
		if k != "thoughts" {
			return nil, fmt.Errorf("unexpected parameter %q for think", strings.TrimSpace(k))
		}
	}
	return Execute(ctx, params)
}
