package tintinweb_subagents

// Resolution of an Agent call against its agent file. upstream: src/invocation-config.ts.

// isolationDescription is the model-facing description of the `isolation` parameter. upstream: invocation-config.ts
// isolationParamShape. "off" is listed first so the harmless value is the obvious one to reach for.
const isolationDescription = `Isolation mode. Default "off". "off" runs the agent in the current checkout, the same as omitting the field. "worktree" creates a temporary git worktree so the agent works on an isolated copy of the repo (a copy cannot see uncommitted or staged changes in the main checkout).`

// isolationParam is the `isolation` parameter of the Agent tool schema, or nothing when the project disabled
// worktrees (`worktreeIsolation: false`): with no field there is nothing to pass or to explain.
func isolationParam(enabled bool) map[string]any {
	if !enabled {
		return map[string]any{}
	}
	return map[string]any{"isolation": map[string]any{
		"anyOf": []any{
			map[string]any{"const": "off", "type": "string"},
			map[string]any{"const": "worktree", "type": "string"},
		},
		"description": isolationDescription,
	}}
}

// invocationParams are the Agent call's parameters that an agent file can outrank. A nil field is an omitted one.
type invocationParams struct {
	Model, Thinking                           *string
	MaxTurns                                  *int
	InheritContext, RunInBackground, Isolated *bool
	// Isolation is untyped: options arrive unvalidated over the cross-extension RPC, so the resolver compares.
	Isolation any
}

type resolveOptions struct {
	// WorktreeAllowed is false when the project set `worktreeIsolation: false`; nil means allowed. A requested
	// worktree is dropped rather than failing the call.
	WorktreeAllowed *bool
	// DefaultRunInBackground is what an unqualified spawn means (the `backgroundByDefault` setting at the top level).
	DefaultRunInBackground bool
}

// overriddenParams are the caller's values an agent file outranked, so surfaces can say "(asked X)".
type overriddenParams struct{ Thinking, Model string }

type invocationConfig struct {
	ModelInput      string
	ModelFromParams bool
	Thinking        string
	MaxTurns        *int
	InheritContext  bool
	RunInBackground bool
	Isolated        bool
	// Isolation is "worktree" or "" (off and unset collapse).
	Isolation  string
	Overridden *overriddenParams
}

func resolveAgentInvocationConfig(cfg *agentConfig, p invocationParams, opts resolveOptions) invocationConfig {
	var out invocationConfig
	str := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	if cfg == nil {
		cfg = &agentConfig{}
	}
	// Precedence first, collapse second: an agent file's "off" only outranks a caller's "worktree" while it is
	// still a value.
	var requested any = p.Isolation
	if cfg.Isolation != "" {
		requested = cfg.Isolation
	}
	if requested == "worktree" && (opts.WorktreeAllowed == nil || *opts.WorktreeAllowed) {
		out.Isolation = "worktree"
	}
	if cfg.Thinking != "" && p.Thinking != nil && cfg.Thinking != *p.Thinking || cfg.Model != "" && p.Model != nil && cfg.Model != *p.Model {
		o := overriddenParams{}
		if cfg.Thinking != "" && p.Thinking != nil && cfg.Thinking != *p.Thinking {
			o.Thinking = *p.Thinking
		}
		if cfg.Model != "" && p.Model != nil && cfg.Model != *p.Model {
			o.Model = *p.Model
		}
		out.Overridden = &o
	}
	out.ModelInput = cfg.Model
	if out.ModelInput == "" {
		out.ModelInput = str(p.Model)
	}
	out.ModelFromParams = cfg.Model == "" && p.Model != nil
	out.Thinking = cfg.Thinking
	if out.Thinking == "" {
		out.Thinking = str(p.Thinking)
	}
	out.MaxTurns = cfg.MaxTurns
	if out.MaxTurns == nil {
		out.MaxTurns = p.MaxTurns
	}
	out.InheritContext = firstBool(cfg.InheritContext, p.InheritContext, false)
	out.RunInBackground = firstBool(cfg.RunInBackground, p.RunInBackground, opts.DefaultRunInBackground)
	out.Isolated = firstBool(cfg.Isolated, p.Isolated, false)
	return out
}

// firstBool is the first set value, else the default (the original's `a ?? b ?? default`).
func firstBool(a, b *bool, def bool) bool {
	switch {
	case a != nil:
		return *a
	case b != nil:
		return *b
	}
	return def
}

// resolveJoinMode is the join mode of a background agent; a foreground one has none ("").
func resolveJoinMode(defaultJoinMode string, runInBackground bool) string {
	if runInBackground {
		return defaultJoinMode
	}
	return ""
}
