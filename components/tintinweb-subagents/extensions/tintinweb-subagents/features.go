package tintinweb_subagents

// What of the original's optional features this port offers. A slice that ports one flips its line to true (and
// removes the matching refusal in refuse.go); the Agent tool's text and the registered tools follow from these and
// from subagents.json (descriptionFlagsNow). Each constant is on its own line so slices do not conflict.
var (
	featureScheduling        = false // the `schedule` parameter, the scheduler. upstream: schedule.ts
	featureWorktreeIsolation = true  // the `isolation` parameter, worktrees. upstream: worktree.ts
	featureWorkflows         = false // the SubagentWorkflow tool. upstream: workflow/*
)
