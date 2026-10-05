// Package ask is an end-to-end test fixture: the ask tool opens a select dialog and reports the choice.
package ask

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension registers the ask tool.
func Extension() *sdk.Extension {
	e := sdk.New("ask")
	e.RegisterTool(sdk.ToolDefinition{
		Name: "ask", Label: "Ask", Description: "Ask the user where to deploy",
		Parameters: sdk.Schema{"type": "object", "properties": map[string]any{}},
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			choice, ok, err := ctx.Select("Deploy where?", []string{"staging", "production"})
			if err != nil {
				return nil, err
			}
			if !ok {
				ctx.Notify("no choice", "warning")
				return "the user made no choice", nil
			}
			ctx.Notify("chose "+choice, "info")
			return "the user chose " + choice, nil
		},
	})
	return e
}
