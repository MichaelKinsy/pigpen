package tintinweb_subagents

// What an Agent call means once the agent file's own settings have had their say: the file's fields outrank the
// call's, the call only fills the gaps. upstream: src/invocation-config.ts.

// invocationParams are the Agent call's parameters that an agent file can also set. A nil pointer or "" is unset.
type invocationParams struct {
	Model, Thinking string
	MaxTurns        *int
	RunInBackground *bool
	InheritContext  *bool
	Isolated        *bool
	Isolation       any // compared by value, not trusted: it arrives unvalidated through the RPC
}

// invocationOptions: worktreeAllowed false is the project's `worktreeIsolation: false` (a requested worktree is
// dropped, not refused); defaultRunInBackground is what an unqualified spawn means (the backgroundByDefault setting
// for a top-level call, false for a nested one).
type invocationOptions struct {
	worktreeDisallowed     bool
	defaultRunInBackground bool
}

type resolvedInvocation struct {
	modelInput      string
	modelFromParams bool
	thinking        string
	maxTurns        *int
	inheritContext  bool
	runInBackground bool
	isolated        bool
	isolation       string // "worktree" or ""
	// overridden: caller parameters an agent file's fields outranked ("asked X" on the surfaces); only where both
	// sides named something and they disagree.
	overriddenThinking, overriddenModel string
}

func (r resolvedInvocation) hasOverride() bool {
	return r.overriddenThinking != "" || r.overriddenModel != ""
}

// resolveInvocationConfig applies the precedence: agent file, then call, then default. cfg is nil for no file.
func resolveInvocationConfig(cfg *agentConfig, p invocationParams, o invocationOptions) resolvedInvocation {
	if cfg == nil {
		cfg = &agentConfig{}
	}
	var out resolvedInvocation
	// Precedence first, collapse second: an agent file's "off" outranks a caller's "worktree" only while it is still
	// a value. Everything downstream then sees "worktree" or nothing.
	var requested any = p.Isolation
	if cfg.Isolation != "" {
		requested = cfg.Isolation
	}
	if requested == "worktree" && !o.worktreeDisallowed {
		out.isolation = "worktree"
	}
	if cfg.Thinking != "" && p.Thinking != "" && cfg.Thinking != p.Thinking {
		out.overriddenThinking = p.Thinking
	}
	if cfg.Model != "" && p.Model != "" && cfg.Model != p.Model {
		out.overriddenModel = p.Model
	}
	out.modelInput = firstNonEmpty(cfg.Model, p.Model)
	out.modelFromParams = cfg.Model == "" && p.Model != ""
	out.thinking = firstNonEmpty(cfg.Thinking, p.Thinking)
	out.maxTurns = p.MaxTurns
	if cfg.MaxTurns != nil {
		out.maxTurns = cfg.MaxTurns
	}
	out.inheritContext = boolFirst(cfg.InheritContext, p.InheritContext, false)
	out.runInBackground = boolFirst(cfg.RunInBackground, p.RunInBackground, o.defaultRunInBackground)
	out.isolated = boolFirst(cfg.Isolated, p.Isolated, false)
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func boolFirst(a, b *bool, d bool) bool {
	if a != nil {
		return *a
	}
	if b != nil {
		return *b
	}
	return d
}
