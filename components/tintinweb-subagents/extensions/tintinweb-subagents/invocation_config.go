package tintinweb_subagents

// Resolution of an Agent call against its agent file. upstream: src/invocation-config.ts.

type invocationParams struct {
	Model, Thinking                           *string
	MaxTurns                                  *int
	InheritContext, RunInBackground, Isolated *bool
	Isolation                                 any
}

type resolveOptions struct {
	WorktreeAllowed        *bool
	DefaultRunInBackground bool
}

type overriddenParams struct{ Thinking, Model string }

type invocationConfig struct {
	ModelInput      string
	ModelFromParams bool
	Thinking        string
	MaxTurns        *int
	InheritContext  bool
	RunInBackground bool
	Isolated        bool
	Isolation       string
	Overridden      *overriddenParams
}

func resolveAgentInvocationConfig(cfg *agentConfig, p invocationParams, opts resolveOptions) invocationConfig {
	return invocationConfig{}
}

func resolveJoinMode(defaultJoinMode string, runInBackground bool) string { return "" }

// isolationParam is the schema of the `isolation` Agent parameter, nil when worktrees are disabled.
func isolationParam(enabled bool) map[string]any { return nil }
