package lsp

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

// upstream: packages/coding-agent/src/lsp/tool.ts, packages/coding-agent/src/lsp/types.ts,
//           packages/coding-agent/src/prompts/tools/lsp.md

//go:embed lsp.md
var lspPromptTemplate string

// ToolSchema returns omp's exact JSON Schema for the `lsp` tool at v19.1.1.
// Uses sdk.Type.Object with sdk.NewOrderedObject so property order, required
// fields, enums, bounds, and descriptions match arkToWireSchema(lspSchema)
// from packages/coding-agent/src/lsp/types.ts.
func ToolSchema() sdk.Schema {
	props := sdk.NewOrderedObject(
		"action", sdk.NewOrderedObject(
			"type", "string",
			"enum", []any{
				"diagnostics",
				"definition",
				"references",
				"hover",
				"symbols",
				"rename",
				"rename_file",
				"code_actions",
				"type_definition",
				"implementation",
				"status",
				"reload",
				"capabilities",
				"request",
			},
		),
		"file", sdk.Type.Optional(sdk.Type.String()),
		"line", sdk.Type.Optional(sdk.Type.Number()),
		"symbol", sdk.Type.Optional(sdk.Type.String()),
		"query", sdk.Type.Optional(sdk.Type.String()),
		"new_name", sdk.Type.Optional(sdk.Type.String()),
		"apply", sdk.Type.Optional(sdk.Type.Boolean()),
		"timeout", sdk.Type.Optional(sdk.Type.Number(sdk.NewOrderedObject(
			"minimum", ToolTimeoutMinSec,
			"maximum", ToolTimeoutMaxSec,
			"description", "Timeout in seconds; raises or lowers the default (range 5–300).",
		))),
		"payload", sdk.Type.Optional(sdk.Type.String()),
	)
	return sdk.Type.Object(props, sdk.NewOrderedObject("additionalProperties", false)).Schema()
}

// endSession releases only sessionID's server references, dedupe ledger, and
// trust-note state, leaving every other active session's state intact.
// Registered on package-level lifecycle.OnEnd("lsp", endSession) so repeated
// Register calls replace with an identical function targeting the shared
// package-level registries.
func endSession(sessionID string) {
	if sessionID == "" {
		return
	}
	defaultManager.ReleaseSession(sessionID)
	deleteLedgerForSession(sessionID)
	deleteUntrustedNotedForSession(sessionID)
}

// Register registers omp's `lsp` tool and session lifecycle hooks on e.
// Safe to call many times (once per PiG session instance): no I/O happens at
// registration time, global cleanup is registered idempotently via
// lifecycle.OnEnd("lsp", endSession), and session_shutdown releases only the
// ending session's ID.
func Register(e *sdk.Extension) {
	lifecycle.OnEnd("lsp", endSession)
	toolcmd.Mountable("lsp", Enabled)

	defaultDesc := renderLspDescription(lspPromptTemplate, DefaultSettings())

	lspDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "lsp",
		Label:         "LSP",
		Description:   defaultDesc,
		PromptSnippet: "Query LSP (language server) for diagnostics, hover info, and references",
		Parameters:    ToolSchema(),
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: new(false),
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			parsed, err := parseLspParams(params)
			if err != nil {
				return nil, err
			}
			reqCtx := contextFromSDK(ctx)
			return executeLspTool(reqCtx, ctx, defaultManager, safeCwd(ctx), parsed)
		},
	})
	e.RegisterTool(intent.Tool(lspDef))
	intent.RegisterSync(e, lspDef)
	// Optional eager warmup when lsp.lazy is explicitly set to false.
	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		settings := readSettings(ctx)
		if !settings.Enabled || settings.Lazy {
			return nil, nil
		}
		cwd := safeCwd(ctx)
		if cwd == "" {
			return nil, nil
		}
		sessionID := resolveSessionID(ctx)
		if sessionID == "" {
			// Never start an ownerless warmup server that outlives the session.
			return nil, nil
		}
		trusted := isProjectTrusted(ctx)
		go warmupLspServers(context.Background(), defaultManager, sessionID, trusted, cwd)
		return nil, nil
	})

	// Release only the ending session's state via lifecycle.End(sessionID).
	e.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sessionID := resolveSessionID(ctx); sessionID != "" {
			lifecycle.End(sessionID)
		}
		return nil, nil
	})
}

func warmupLspServers(ctx context.Context, m *ClientManager, sessionID string, trusted bool, cwd string) {
	if sessionID == "" {
		return
	}
	cfg := GetConfigWithTrust(cwd, trusted)
	idleTimeoutMs := configIdleTimeoutMs(cfg)
	for _, srv := range getLspServers(cfg) {
		t := warmupTimeout
		if srv.Config.WarmupTimeoutMs > 0 {
			t = time.Duration(srv.Config.WarmupTimeoutMs) * time.Millisecond
		}
		go func(s NamedServer, timeout time.Duration) {
			wCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			_, _ = m.GetOrCreateClientForSessionWithTrust(wCtx, sessionID, trusted, idleTimeoutMs, s.Config, cwd, timeout)
		}(srv, t)
	}
}

func parseLspParams(raw map[string]any) (LspParams, error) {
	var p LspParams
	action, ok := raw["action"].(string)
	if !ok || action == "" {
		return p, fmt.Errorf("invalid lsp arguments: action must be a non-empty string")
	}
	p.Action = action
	if v, ok := raw["file"].(string); ok {
		p.File = v
	}
	if v, exists := raw["line"]; exists && v != nil {
		switch n := v.(type) {
		case int:
			line := n
			p.Line = &line
		case int64:
			line := int(n)
			p.Line = &line
		case float64:
			line := int(n)
			p.Line = &line
		default:
			return p, fmt.Errorf("invalid lsp arguments: line must be a number")
		}
	}
	if v, ok := raw["symbol"].(string); ok {
		p.Symbol = v
	}
	if v, ok := raw["query"].(string); ok {
		p.Query = v
	}
	if v, ok := raw["new_name"].(string); ok {
		p.NewName = v
	}
	if v, exists := raw["apply"]; exists && v != nil {
		b, ok := v.(bool)
		if !ok {
			return p, fmt.Errorf("invalid lsp arguments: apply must be a boolean")
		}
		p.Apply = &b
	}
	if v, exists := raw["timeout"]; exists && v != nil {
		switch n := v.(type) {
		case float64:
			t := n
			p.Timeout = &t
		case int:
			t := float64(n)
			p.Timeout = &t
		case int64:
			t := float64(n)
			p.Timeout = &t
		default:
			return p, fmt.Errorf("invalid lsp arguments: timeout must be a number")
		}
	}
	if v, exists := raw["payload"]; exists && v != nil {
		s, ok := v.(string)
		if !ok {
			return p, fmt.Errorf("invalid lsp arguments: payload must be a string")
		}
		p.Payload = &s
	}
	return p, nil
}
