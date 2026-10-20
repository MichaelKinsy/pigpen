package pi_permission_system

import (
	"testing"
)

// Go counterparts of the handler half of test/helpers/handler-fixtures.ts (makeHandler, makeCtx, the surface-check factories).

type gfEvents struct {
	emitted []struct {
		channel string
		data    any
	}
}

func (e *gfEvents) Emit(channel string, data any) error {
	e.emitted = append(e.emitted, struct {
		channel string
		data    any
	}{channel, data})
	return nil
}

func (e *gfEvents) decisions() []PermissionDecisionEvent {
	out := []PermissionDecisionEvent{}
	for _, x := range e.emitted {
		if x.channel == PermissionsDecisionChannel {
			out = append(out, x.data.(PermissionDecisionEvent))
		}
	}
	return out
}

type gfLogger struct {
	reviews []gfLogWrite
	debugs  []gfLogWrite
	warns   []string
}

func (l *gfLogger) Review(event string, d *jsObject) {
	l.reviews = append(l.reviews, gfLogWrite{event, d})
}
func (l *gfLogger) Debug(event string, d *jsObject) {
	l.debugs = append(l.debugs, gfLogWrite{event, d})
}
func (l *gfLogger) Warn(m string) { l.warns = append(l.warns, m) }

type gfSessionManager struct{ entries []SessionEntryView }

func (gfSessionManager) GetSessionID() (string, error)  { return "test-session", nil }
func (gfSessionManager) GetSessionDir() (string, error) { return "/sessions/test", nil }
func (m gfSessionManager) GetEntries() ([]SessionEntryView, error) {
	return append([]SessionEntryView{}, m.entries...), nil
}

// makeCtx is makeCtx of handler-fixtures.ts: cwd /test/project, hasUI true, an empty session.
func makeCtx(mut ...func(*SessionContext)) (SessionContext, *fakeNotifier) {
	n := &fakeNotifier{}
	c := SessionContext{AuthorityContext: AuthorityContext{ForwarderContext: ForwarderContext{Cwd: "/test/project", HasUI: true, SessionManager: gfSessionManager{}}}, Notifier: n}
	for _, m := range mut {
		m(&c)
	}
	return c, n
}

// makeToolCallEvent is the tool_call event of the fixtures: toolCallId tc-1, name, empty input, then the extra fields.
func makeToolCallEvent(toolName string, extra ...any) *jsObject {
	e := obj("type", "tool_call", "toolCallId", "tc-1", "name", toolName, "input", newObject())
	for i := 0; i < len(extra); i += 2 {
		e.set(extra[i].(string), extra[i+1])
	}
	return e
}

type gfSurfaceCheck func(surface string, input any, agent *string, rules Ruleset) PermissionCheckResult

// makeSurfaceCheck is the surface-dispatching checkPermission: a bare family key answers for its members.
func makeSurfaceCheck(bySurface map[string]PermissionCheckResult, defaultResult ...PermissionCheckResult) gfSurfaceCheck {
	def := PermissionCheckResult{State: "allow"}
	if len(defaultResult) > 0 {
		def = defaultResult[0]
	}
	return func(surface string, _ any, _ *string, _ Ruleset) PermissionCheckResult {
		key := surface
		if _, ok := bySurface[surface]; !ok {
			key = SurfaceFamilyOf(surface)
		}
		base, ok := bySurface[key]
		if !ok {
			base = def
		}
		out := base
		out.ToolName, out.Source, out.Origin = surface, "tool", "builtin"
		// the original spreads `base` last: its own fields win over the defaults
		if base.ToolName != "" {
			out.ToolName = base.ToolName
		}
		if base.Source != "" {
			out.Source = base.Source
		}
		if base.Origin != "" {
			out.Origin = base.Origin
		}
		return out
	}
}

// sc is a state-only check for makeSurfaceCheck.
func sc(state string, mut ...func(*PermissionCheckResult)) PermissionCheckResult {
	c := PermissionCheckResult{State: state}
	for _, m := range mut {
		m(&c)
	}
	return c
}

// makeBashCommandCheck dispatches the bash surface on a command regex.
func makeBashCommandCheck(deny func(string) bool, denyMatched, allowMatched string) gfSurfaceCheck {
	return func(surface string, input any, _ *string, _ Ruleset) PermissionCheckResult {
		if surface == "bash" {
			command, _ := ToRecord(input).str("command")
			if deny(command) {
				return makeCheckResult(ckState("deny"), func(c *PermissionCheckResult) {
					c.Source, c.Command, c.MatchedPattern = "bash", sp(command), sp(denyMatched)
				})
			}
			return makeCheckResult(ckState("allow"), func(c *PermissionCheckResult) {
				c.Source, c.Command = "bash", sp(command)
				if allowMatched != "" {
					c.MatchedPattern = sp(allowMatched)
				}
			})
		}
		return makeCheckResult(ckState("allow"))
	}
}

// gfSession overrides the session queries a test replaces (the original's vi.spyOn on the real session).
type gfSession struct {
	*PermissionSession
	skillEntries func() []SkillPromptEntry
	infraScope   func() InfrastructureReadScope
	resolveAgent func(ctx ActiveAgentContext, systemPrompt string) (*string, error)
}

func (s *gfSession) GetActiveSkillEntries() []SkillPromptEntry {
	if s.skillEntries != nil {
		return s.skillEntries()
	}
	return s.PermissionSession.GetActiveSkillEntries()
}
func (s *gfSession) GetInfrastructureReadScope() InfrastructureReadScope {
	if s.infraScope != nil {
		return s.infraScope()
	}
	return s.PermissionSession.GetInfrastructureReadScope()
}
func (s *gfSession) ResolveAgentName(ctx ActiveAgentContext, p string) (*string, error) {
	if s.resolveAgent != nil {
		return s.resolveAgent(ctx, p)
	}
	return s.PermissionSession.ResolveAgentName(ctx, p)
}

type gfToolRegistry struct {
	all    []any
	active []string
}

func (r *gfToolRegistry) GetAll() []any        { return r.all }
func (r *gfToolRegistry) GetActive() []string  { return r.active }
func (r *gfToolRegistry) SetActive(n []string) { r.active = n }

type gfHandlerOpts struct {
	checkPermission gfSurfaceCheck
	skillEntries    func() []SkillPromptEntry
	infraScope      func() InfrastructureReadScope
	resolveAgent    func(ActiveAgentContext, string) (*string, error)
	prompter        *gfEscalator
	tools           []string
	toolRegistryAll []any
	shellTools      *jsObject
	yolo            bool
}

type gfHandlerRig struct {
	handler    *PermissionGateHandler
	events     *gfEvents
	session    *gfSession
	logger     *gfLogger
	prompter   *gfEscalator
	recorder   *SessionRules
	pm         *fakeManager
	forwarding *fakeForwarding
}

// makeHandler constructs a PermissionGateHandler wired with real collaborators, as index.ts does.
func makeHandler(t *testing.T, opts ...gfHandlerOpts) *gfHandlerRig {
	t.Helper()
	var o gfHandlerOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	mockRealpath(t, identityRealpath)
	rig := makeRealSession(nil)
	if o.shellTools != nil {
		cfg := defaultExtensionConfig()
		cfg.ShellTools = o.shellTools
		rig.store.current = cfg
	}
	rules := rig.rules
	resolver := NewPermissionResolver(rig.pm, rules)
	if o.checkPermission != nil {
		check := o.checkPermission
		rig.pm.checkFn = func(intent ResolvedAccessIntent) PermissionCheckResult {
			switch it := intent.(type) {
			case PathValuesAccessIntent:
				path := "*"
				if len(it.Values) > 0 {
					path = it.Values[0]
				}
				return check(it.Surface, obj("path", path), it.AgentName, nil)
			case ToolAccessIntent:
				return check(it.Surface, it.Input, it.AgentName, nil)
			case BashCommandAccessIntent:
				return check("bash", obj("command", it.Command), it.AgentName, nil)
			}
			panic("unhandled intent")
		}
	}
	session := &gfSession{PermissionSession: rig.session, skillEntries: o.skillEntries, infraScope: o.infraScope, resolveAgent: o.resolveAgent}
	events, logger := &gfEvents{}, &gfLogger{}
	registry := &gfToolRegistry{active: []string{"read", "bash"}}
	switch {
	case o.tools != nil:
		for _, n := range o.tools {
			registry.all = append(registry.all, obj("name", n))
		}
	case o.toolRegistryAll != nil:
		registry.all = o.toolRegistryAll
	default:
		registry.all = []any{obj("name", "read", "promptGuidelines", []any{"Use read to examine files."}), obj("name", "bash", "promptGuidelines", []any{"Use bash for file operations."})}
	}
	recorder := &SessionRules{}
	prompter := o.prompter
	if prompter == nil {
		prompter = gfAnswer(PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: gfHuman})
	}
	reporter := NewGateDecisionReporter(logger, events)
	runner := NewGateRunner(resolver, recorder, prompter, reporter, func() bool { return o.yolo })
	handler := NewPermissionGateHandler(session, registry, NewToolCallGatePipeline(resolver, session, nil, nil),
		NewSkillInputGatePipeline(ResolverSkillInputInputs{Resolver: resolver}), runner)
	return &gfHandlerRig{handler: handler, events: events, session: session, logger: logger, prompter: prompter, recorder: recorder, pm: rig.pm, forwarding: rig.fwd}
}

// tcOutcome runs HandleToolCall and fails the test on an error.
func (r *gfHandlerRig) toolCall(t *testing.T, event any, ctx SessionContext) GateOutcome {
	t.Helper()
	out, err := r.handler.HandleToolCall(event, ctx)
	if err != nil {
		t.Fatalf("HandleToolCall: %v", err)
	}
	return out
}
