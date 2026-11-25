package files

import (
	_ "embed"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
)

//go:embed read.md
var readDescription string

//go:embed write.md
var writeDescription string

//go:embed edit.md
var editDescription string

// Register registers omp's read, write, and edit tools on e.
func Register(e *sdk.Extension) {
	hashline.RegisterLifecycle()
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid, err := ctx.GetSessionID(); err == nil && sid != "" {
			lifecycle.End(sid)
		}
		return nil, nil
	})
	readDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:        "read",
		Label:       "Read",
		Description: strings.TrimSpace(readDescription),
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Local path, internal URI, or URL; selectors inline.",
				},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			path, ok := params["path"].(string)
			if !ok {
				return nil, fmt.Errorf("invalid read arguments: path must be a string")
			}
			return executeReadWithCtx(ctx, hashline.ForSession(ctx), ctx.Cwd(), path)
		},
	})
	e.RegisterTool(intent.Tool(readDef))

	writeDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "write",
		Label:         "Write",
		Description:   strings.TrimSpace(writeDescription),
		ExecutionMode: "sequential",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type": "string",
				},
				"content": map[string]any{
					"type": "string",
				},
			},
			"required":             []string{"path", "content"},
			"additionalProperties": false,
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			path, ok := params["path"].(string)
			if !ok {
				return nil, fmt.Errorf("invalid write arguments: path must be a string")
			}
			var content *string
			if raw, exists := params["content"]; exists && raw != nil {
				s, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf("invalid write arguments: content must be a string")
				}
				content = &s
			}
			return executeWrite(ctx, hashline.ForSession(ctx), ctx.Cwd(), path, content)
		},
	})
	e.RegisterTool(intent.Tool(writeDef))

	editDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "edit",
		Label:         "Edit",
		Description:   strings.TrimSpace(editDescription),
		ExecutionMode: "sequential",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type": "string",
				},
			},
			"required":             []string{"input"},
			"additionalProperties": false,
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			input, ok := params["input"].(string)
			if !ok {
				return nil, fmt.Errorf("invalid edit arguments: input must be a string")
			}
			return executeEdit(ctx, hashline.ForSession(ctx), ctx.Cwd(), input)
		},
	})
	e.RegisterTool(intent.Tool(editDef))
	intent.RegisterSync(e, readDef, writeDef, editDef)
}
