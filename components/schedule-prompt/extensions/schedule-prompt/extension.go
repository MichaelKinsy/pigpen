// Package schedule_prompt is the Go port of pi-schedule-prompt ("Pi's Heartbeat") by tintinweb. This revision is the
// honest-refusal skeleton: the tool and the command are registered with the original's definition, and every use of
// either is refused with the program's refusal message until its slice is ported (port/slices.json).
package schedule_prompt

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	toolFeature    = "the schedule_prompt tool (scheduling, listing and managing prompts)"
	commandFeature = "the /schedule-prompt command (the Jobs overlay and Settings)"
)

// Extension returns the schedule-prompt extension.
func Extension() *sdk.Extension {
	e := sdk.New("schedule-prompt")
	e.RegisterTool(sdk.ToolDefinition{
		Name: toolName, Label: toolLabel, Description: toolDesc, Parameters: toolParameters(),
		Execute: func(sdk.Context, map[string]any) (any, error) {
			return nil, sdk.NewToolError(unsupported(toolFeature).Error())
		},
	})
	e.Command("schedule-prompt", "Manage scheduled prompts interactively", func(ctx sdk.Context, _ string) error {
		ctx.Notify(unsupported(commandFeature).Error(), "error")
		return nil
	})
	return e
}
