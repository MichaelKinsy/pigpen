// Package pi_permission_system is a Go port (partial) of @gotgenes/pi-permission-system: allow, ask and deny rules for tool calls,
// read from the global config. It ports the rule engine and the tool_call gate for tools whose decision does not depend on a path;
// see port/PORT.md for what is not ported.
package pi_permission_system

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type state struct {
	mu       sync.Mutex
	policy   *Policy
	config   Config
	baseline surfaceBaseline
	skills   []skillEntry // the listed skills the policy does not deny, from the last system prompt
	cwd      string
	// the config system (src/config): the store holds the merged runtime config, the loader the policy file paths of the
	// session's cwd (withheld for an untrusted project), the reporter the once-per-issue warnings
	store    *ConfigStore
	loader   *FilePolicyLoader
	reporter *ConfigIssueReporter
	notifier *sessionNotifier
}

// sessionNotifier is the log seam the config system writes to: warnings reach the operator through the session's context
// (PermissionSession.notify). The debug and review logs are not ported (see PORT.md), so those entries are dropped.
type sessionNotifier struct {
	mu  sync.Mutex
	ctx *sdk.Context
}

func (n *sessionNotifier) bind(ctx sdk.Context) {
	n.mu.Lock()
	n.ctx = &ctx
	n.mu.Unlock()
}

func (n *sessionNotifier) unbind() {
	n.mu.Lock()
	n.ctx = nil
	n.mu.Unlock()
}

func (n *sessionNotifier) Warn(message string) {
	n.mu.Lock()
	ctx := n.ctx
	n.mu.Unlock()
	if ctx != nil {
		ctx.Notify(message, "warning")
	}
}

func (n *sessionNotifier) Debug(string, *jsObject)  {}
func (n *sessionNotifier) Review(string, *jsObject) {}

// untrustedProjectMessage is shown when project config is skipped because the project is untrusted, so the reduced-scope state is
// never silent (lifecycle.ts UNTRUSTED_PROJECT_MESSAGE, #644).
const untrustedProjectMessage = "pi-permission-system: project is not trusted \u2014 skipping project-scoped " +
	"permission configuration. Only global policy applies. Grant project trust " +
	"to load this project's permission rules."

// projectTrusted asks the host whether the project is trusted; a failed answer is not trust.
func projectTrusted(ctx sdk.Context) bool {
	trusted, err := ctx.IsProjectTrusted()
	return err == nil && trusted
}

// configureForCwd rebuilds the policy loader for a session's cwd, "" for global-only (permission-manager.ts configureForCwd).
func (s *state) configureForCwd(cwd string) {
	s.mu.Lock()
	s.loader = NewFilePolicyLoader(derivePolicyLoaderOptions(agentDir(), cwd))
	s.mu.Unlock()
}

// refreshConfig reloads the merged runtime config for the session's cwd and trust and brings the status bar in step with it
// (PermissionSession.refreshConfig).
func (s *state) refreshConfig(ctx sdk.Context, trusted bool) {
	s.store.Refresh(ctx.Cwd(), trusted)
	if ctx.HasUI() {
		syncPermissionSystemStatus(ctx, s.store.Current())
	}
}

func (s *state) warnProjectUntrusted() {
	s.notifier.Warn(untrustedProjectMessage)
}

// policyPaths adapts the current loader to the store's ResolvedPolicyPathProvider.
type policyPaths struct{ s *state }

func (p policyPaths) GetResolvedPolicyPaths() ResolvedPolicyPaths {
	p.s.mu.Lock()
	l := p.s.loader
	p.s.mu.Unlock()
	return l.GetResolvedPolicyPaths()
}

func (s *state) reload() *Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = loadConfig()
	s.policy = NewPolicy(s.config)
	return s.policy
}

func (s *state) current() *Policy {
	s.mu.Lock()
	p := s.policy
	s.mu.Unlock()
	if p == nil {
		return s.reload()
	}
	return p
}

// Extension returns the permission gate.
func Extension() *sdk.Extension {
	e, _ := newExtension()
	return e
}

// newExtension builds the extension and the state its handlers share (the tests read the state).
func newExtension() (*sdk.Extension, *state) {
	e := sdk.New(extensionID)
	st := &state{notifier: &sessionNotifier{}}
	st.loader = NewFilePolicyLoader(derivePolicyLoaderOptions(agentDir(), ""))
	st.store = NewConfigStore(ConfigStoreDeps{AgentDir: agentDir(), PolicyPaths: policyPaths{st}, Logger: st.notifier})
	st.reporter = NewConfigIssueReporter(st.store, st.notifier)
	// No cwd or trust decision exists at factory init, so there is no project scope to withhold from (fail closed); session_start
	// reloads with the real cwd and trust decision. This load reports nothing: it cannot, and it must not consume the warning (#933).
	st.store.Refresh("", false)
	registerPermissionSystemCommand(e, PermissionSystemConfigController{
		Config:     st.store,
		ConfigPath: getGlobalConfigPath(agentDir()),
		GetActiveAgentConfigRules: func() Ruleset {
			var rules Ruleset
			for _, r := range st.current().rules {
				if r.Layer == "config" {
					rules = append(rules, r)
				}
			}
			return rules
		},
	})

	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		trusted := projectTrusted(ctx)
		st.reload()
		st.mu.Lock()
		st.baseline.reset()
		st.skills = nil
		st.mu.Unlock()
		// The project cwd is withheld for an untrusted project so its policy scopes are not loaded (#644). The notifier is bound
		// first: it activates the session, so the warnings below have a UI to reach (#933).
		st.notifier.bind(ctx)
		if trusted {
			st.configureForCwd(ctx.Cwd())
		} else {
			st.configureForCwd("")
		}
		st.refreshConfig(ctx, trusted)
		st.store.LogResolvedPaths(ctx.Cwd())
		st.reporter.Report()
		if !trusted {
			st.warnProjectUntrusted()
		}
		return nil, nil
	})
	e.OnEvent(sdk.EventResourcesDiscover, func(ctx sdk.Context, data map[string]any) (any, error) {
		if reason, _ := data["reason"].(string); reason != "reload" {
			return nil, nil
		}
		trusted := projectTrusted(ctx)
		if trusted {
			st.configureForCwd(ctx.Cwd())
		} else {
			st.configureForCwd("")
		}
		st.mu.Lock()
		st.skills = nil
		st.mu.Unlock()
		if !trusted {
			st.warnProjectUntrusted()
		}
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		p := st.reload()
		// Turn preparation: activate, then refresh the config under the current trust and say what the refresh found, latched per
		// issue (session-turn-prep.ts). The project-trust warning is not repeated every turn.
		st.notifier.bind(ctx)
		st.refreshConfig(ctx, projectTrusted(ctx))
		st.reporter.Report()
		// The skills the prompt lists, for the skill-read gate (before-agent-start.ts setActiveSkillEntries).
		// PiG 0.5 hands the handler a *sdk.SystemPrompt in data["systemPrompt"], not a string; GetSystemPrompt reads its rendered text.
		prompt, _ := ctx.GetSystemPrompt()
		skills := p.visibleSkillEntries(prompt, ctx.Cwd())
		st.mu.Lock()
		st.skills, st.cwd = skills, ctx.Cwd()
		st.mu.Unlock()
		// Withhold the tools the policy denies outright, and restore ones it no longer denies.
		active, err := ctx.GetActiveTools()
		if err != nil {
			return nil, nil
		}
		registered := map[string]bool{}
		if all, err := ctx.GetAllTools(); err == nil {
			for _, t := range all {
				registered[t.Name] = true
			}
		}
		st.mu.Lock()
		res := st.baseline.resolveExposed(active, registered, func(n string) bool { return !p.IsToolFullyDenied(n) })
		st.mu.Unlock()
		ctx.SetActiveTools(res.exposed)
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.SetStatus(extensionID, "")
		st.notifier.unbind()
		return nil, nil
	})
	e.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) {
		name, _ := data["toolName"].(string)
		input, _ := data["input"].(map[string]any)
		v := st.current().Decide(name, input)
		if !v.Block {
			st.mu.Lock()
			v = skillRead(name, input, st.skills, st.cwd)
			st.mu.Unlock()
		}
		if v.Block {
			return map[string]any{"block": true, "reason": v.Reason}, nil
		}
		return nil, nil
	})
	return e, st
}
