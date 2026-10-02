package contextinfo

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Best-effort host reads for the status footer and the event handlers.
//
// The footer refreshes on every turn and tool call. A host read that fails there
// (a transport hiccup during shutdown, a session that is being replaced) must not
// raise an error into the agent loop, so these return the zero value and the
// footer shows what it has. The slash commands do not use them: they read the host
// directly and return the error, so a failure reaches the user.

func softContextUsage(ctx sdk.Context) *sdk.ContextUsage {
	usage, err := ctx.GetContextUsage()
	if err != nil {
		return nil
	}
	return usage
}

func softSystemPrompt(ctx sdk.Context) string {
	prompt, err := ctx.GetSystemPrompt()
	if err != nil {
		return ""
	}
	return prompt
}

func softAllTools(ctx sdk.Context) []sdk.ToolInfo {
	tools, err := ctx.GetAllTools()
	if err != nil {
		return nil
	}
	return tools
}

func softActiveTools(ctx sdk.Context) []string {
	tools, err := ctx.GetActiveTools()
	if err != nil {
		return nil
	}
	return tools
}

func softThinkingLevel(ctx sdk.Context) string {
	level, err := ctx.GetThinkingLevel()
	if err != nil {
		return ""
	}
	return level
}

func softBranch(ctx sdk.Context) []sdk.BranchEntry {
	branch, err := ctx.GetBranch()
	if err != nil {
		return nil
	}
	return branch
}

// softModelInfo returns the active model with fallback pricing applied, or nil.
func softModelInfo(ctx sdk.Context) *sdk.ModelInfo {
	info, err := ctx.GetModelInfo()
	if err != nil {
		return nil
	}
	return patchModelCosts(info)
}
