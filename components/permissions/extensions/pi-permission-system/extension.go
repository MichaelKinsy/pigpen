// Package pi_permission_system is a Go port of @gotgenes/pi-permission-system: allow, ask and deny rules for tool calls, skills,
// paths and bash commands, the approval dialog and its authorizer chain, subagent forwarding and the cross-extension service. See
// port/PORT.md for what differs from the original.
package pi_permission_system

import (
	"runtime"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Port of src/index.ts: the composition root. It builds every collaborator once per extension instance, in the original's order,
// and registers the event handlers. What differs from the original, and why:
//
//   - The SDK context is per event, so the pieces the original builds from `pi` alone (the tool registry, the session context) are
//     built per event from the context the host hands the handler; hostBinder carries the latest one to the collaborators that
//     reach the host between events.
//   - getPackageDir() (Pi's install directory) has no SDK counterpart: the infrastructure-read root for it is absent (reported as an
//     SDK gap, see PORT.md).
//   - The host's keybindings manager has no SDK counterpart either: the dialog's key map is the defaults.

// hostBinder carries the most recent event context to the collaborators that call the host outside a handler's argument list (the
// tool registry the gate handler holds). The handlers bind before they use it; all events of a session share one host.
type hostBinder struct {
	mu  sync.Mutex
	ctx *sdk.Context
}

func (b *hostBinder) bind(ctx sdk.Context) {
	b.mu.Lock()
	b.ctx = &ctx
	b.mu.Unlock()
}

func (b *hostBinder) current() (sdk.Context, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctx == nil {
		return sdk.Context{}, false
	}
	return *b.ctx, true
}

// toolRecord is one registered tool as the registry readers see it: a record with a name and its prompt guidelines.
func toolRecord(tool sdk.ToolInfo) any {
	o := newObject()
	o.set("name", tool.Name)
	if len(tool.PromptGuidelines) > 0 {
		o.set("promptGuidelines", stringsToAny(tool.PromptGuidelines))
	}
	return o
}

// hostGateTools is the gate handler's ToolRegistry. A host that cannot answer reads as no tools: the requested-tool validation then
// blocks (fail closed), never allows.
type hostGateTools struct{ binder *hostBinder }

func (t hostGateTools) GetAll() []any {
	ctx, ok := t.binder.current()
	if !ok {
		return []any{}
	}
	all, err := ctx.GetAllTools()
	if err != nil {
		return []any{}
	}
	out := make([]any, len(all))
	for i, tool := range all {
		out[i] = toolRecord(tool)
	}
	return out
}

func (t hostGateTools) GetActive() []string {
	ctx, ok := t.binder.current()
	if !ok {
		return nil
	}
	active, _ := ctx.GetActiveTools()
	return active
}

func (t hostGateTools) SetActive(names []string) {
	if ctx, ok := t.binder.current(); ok {
		ctx.SetActiveTools(names)
	}
}

// hostAgentTools is the before_agent_start handler's AgentToolRegistry: a failing read is an error, not an empty tool set.
type hostAgentTools struct{ binder *hostBinder }

func (t hostAgentTools) GetAll() ([]any, error) {
	ctx, ok := t.binder.current()
	if !ok {
		return nil, errNoHostContext
	}
	all, err := ctx.GetAllTools()
	if err != nil {
		return nil, err
	}
	out := make([]any, len(all))
	for i, tool := range all {
		out[i] = toolRecord(tool)
	}
	return out, nil
}

func (t hostAgentTools) GetActive() ([]string, error) {
	ctx, ok := t.binder.current()
	if !ok {
		return nil, errNoHostContext
	}
	return ctx.GetActiveTools()
}

func (t hostAgentTools) SetActive(names []string) {
	if ctx, ok := t.binder.current(); ok {
		ctx.SetActiveTools(names)
	}
}

type hostContextError struct{}

func (hostContextError) Error() string { return "pi-permission-system: no host context is bound" }

var errNoHostContext error = hostContextError{}

// detectionFlavor adapts the host path flavor to what subagent detection reads (the flavor's own normalize, fold and containment).
type detectionFlavor struct{ PathFlavor }

func (f detectionFlavor) Normalize(path string) string { return f.impl().Normalize(path) }

// permissionSystem is one extension instance: the collaborators the composition root builds and the handlers share (the tests read
// them).
type permissionSystem struct {
	paths      ExtensionPaths
	hostFlavor PathFlavor
	binder     *hostBinder

	sessionRules     *SessionRules
	manager          *PermissionManager
	configStore      *ConfigStore
	logger           *PermissionSessionLogger
	session          *PermissionSession
	resolver         *PermissionResolver
	selection        *AuthorizerSelection
	dialogQueue      *AskDialogQueue
	service          *LocalPermissionsService
	serviceLifecycle *PermissionServiceLifecycle
	audit            *DecisionAudit
	reporter         *GateDecisionReporter
	lifecycle        *SessionLifecycleHandler
	agentPrep        *AgentPrepHandler
	gates            *PermissionGateHandler
	toolCall         func(event any, ctx sdk.Context) ToolCallResult
	keys             tui.Keybindings
	// contextOf builds the port's session context from the host's; tests replace it to drive the handlers with a synthetic one.
	contextOf func(ctx sdk.Context) SessionContext
	// gateTools and agentTools are the tool registries the handlers read; tests replace them.
	gateTools  ToolRegistry
	agentTools AgentToolRegistry
}

// gateToolsRef and agentToolsRef hand the handlers whatever registry the system currently holds.
type gateToolsRef struct{ p *permissionSystem }

func (r gateToolsRef) GetAll() []any            { return r.p.gateTools.GetAll() }
func (r gateToolsRef) GetActive() []string      { return r.p.gateTools.GetActive() }
func (r gateToolsRef) SetActive(names []string) { r.p.gateTools.SetActive(names) }

type agentToolsRef struct{ p *permissionSystem }

func (r agentToolsRef) GetAll() ([]any, error)       { return r.p.agentTools.GetAll() }
func (r agentToolsRef) GetActive() ([]string, error) { return r.p.agentTools.GetActive() }
func (r agentToolsRef) SetActive(names []string)     { r.p.agentTools.SetActive(names) }

// sessionContext builds the port's session context from the host's: the narrow readers the session, the forwarding subsystem and the
// authorizer selection use. A failing host call never reads as an affirmative: the project is untrusted unless the host says so, and
// the system prompt accessor reports its error.
func (p *permissionSystem) hostSessionContext(ctx sdk.Context) SessionContext {
	sm := ctx.SessionManager()
	ui := NewSDKPromptUi(ctx, p.keys, nil)
	return SessionContext{
		AuthorityContext: AuthorityContext{
			ForwarderContext: ForwarderContext{HasUI: ctx.HasUI(), UI: ui, Cwd: ctx.Cwd(), SessionManager: sm},
			Mode:             ctx.Mode(),
			PromptUI:         ui,
			SessionName: func() *string {
				name, err := sm.GetSessionName()
				if err != nil {
					return nil
				}
				return name
			},
			GetSystemPrompt: func() (string, bool, error) {
				prompt, err := ctx.GetSystemPrompt()
				if err != nil {
					return "", false, err
				}
				return prompt, true, nil
			},
		},
		Notifier: ctx,
		IsProjectTrusted: func() bool {
			trusted, err := ctx.IsProjectTrusted()
			return err == nil && trusted
		},
	}
}

// Extension returns the permission gate.
func Extension() *sdk.Extension {
	e, _ := newExtension()
	return e
}

// buildOptions are the seams the tests replace; the zero value is production: the SDK's event bus, and the host-backed tool
// registries and session context.
type buildOptions struct {
	// events is the bus the node emits on (nil: the extension's pi.events).
	events PermissionEventBus
	// lifecycleBus is where the node subscribes to pi-subagents' child events (nil: the extension's pi.events).
	lifecycleBus LifecycleEventBus
}

// newExtension is the composition root (index.ts piPermissionSystemExtension): it builds the extension and the state its handlers
// share.
func newExtension() (*sdk.Extension, *permissionSystem) { return newExtensionWith(buildOptions{}) }

func newExtensionWith(opts buildOptions) (*sdk.Extension, *permissionSystem) {
	ext := sdk.New(extensionID)
	p := &permissionSystem{binder: &hostBinder{}}
	p.contextOf = p.hostSessionContext
	p.gateTools, p.agentTools = hostGateTools{p.binder}, hostAgentTools{p.binder}
	var events PermissionEventBus = ext.Events()
	if opts.events != nil {
		events = opts.events
	}
	var lifecycleBus LifecycleEventBus = NewSDKLifecycleBus(ext.Events())
	if opts.lifecycleBus != nil {
		lifecycleBus = opts.lifecycleBus
	}

	dir := agentDir()
	// getPackageDir() is Pi's own install dir; the SDK has no counterpart, so the auto-allowed infrastructure root for it is absent.
	p.paths = computeExtensionPaths(dir, "")
	// The single platform read for the whole extension, resolved once into the path-language flavor every consumer shares.
	p.hostFlavor = PathFlavorForPlatform(runtime.GOOS)
	p.keys = tui.NewKeybindings(nil)
	p.sessionRules = &SessionRules{}
	subagentRegistry := GetSubagentSessionRegistry()
	// Process-global, like subagentRegistry: an in-process child reads it to learn whether its parent is draining its inbox.
	servingRegistry := GetServingSessionRegistry()
	// The single owner of subagent detection, shared across every consumer.
	subagentDetection := NewSubagentDetection(SubagentDetectionDeps{SubagentSessionsDir: p.paths.SubagentSessionsDir, Flavor: detectionFlavor{p.hostFlavor}, Registry: subagentRegistry})
	formatterRegistry := &ToolInputFormatterRegistry{}
	_ = RegisterBuiltinToolInputFormatters(formatterRegistry)
	accessExtractorRegistry := &ToolAccessExtractorRegistry{}
	// One registry instance backs both the registerAuthorizer service surface and AuthorizerSelection's chain resolution.
	authorizerRegistry := &AuthorizerRegistry{}

	isYoloEnabled := func() bool { return isYoloModeEnabled(p.configStore.Current()) }
	p.manager = NewPermissionManager(PermissionManagerOptions{AgentDir: dir, Flavor: p.hostFlavor, IsYoloEnabled: isYoloEnabled})
	p.logger = NewPermissionSessionLogger(SessionLoggerDeps{
		GlobalLogsDir: p.paths.GlobalLogsDir,
		GetConfig: func() LogConfig {
			c := p.configStore.Current()
			return LogConfig{DebugLog: c.DebugLog, PermissionReviewLog: c.PermissionReviewLog, ReviewLogFieldMaxWidth: c.ReviewLogFieldMaxWidth}
		},
		Notify: func(message string) { p.session.Notify(message) },
	})
	p.configStore = NewConfigStore(ConfigStoreDeps{AgentDir: dir, PolicyPaths: p.manager, Logger: p.logger})

	prompter := NewPermissionPrompter(PermissionPrompterDeps{Logger: p.logger})
	// One queue per factory invocation, so it is rebuilt with the session rather than outliving it: the host holds a single inline
	// dialog slot (#965).
	p.dialogQueue = NewAskDialogQueue()

	// The filesystem half of the serving announcement: a child in its own process shares nothing but this directory (#721).
	servingHeartbeats := NewServingHeartbeatStore(ServingHeartbeatStoreDeps{ForwardingDir: p.paths.ForwardingDir, Logger: p.logger})
	// The read side of both channels, routed by how the target was resolved.
	servingLiveness := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: servingRegistry, Heartbeats: servingHeartbeats})

	var service *LocalPermissionsService
	p.selection = NewAuthorizerSelection(AuthorizerSelectionConstructorDeps{
		AuthorizerSelectionDeps: AuthorizerSelectionDeps{
			Detection: subagentDetection,
			Events:    events,
			Dialogs:   p.dialogQueue,
			GetPromptPreferences: func() PromptPreferences {
				c := p.configStore.Current()
				return PromptPreferences{
					DoublePressToConfirm: c.DoublePressToConfirm,
					Budget:               ResolveRenderBudget(PromptBudgetConfig{PromptMaxRows: c.PromptMaxRows, PromptFieldMaxWidth: c.PromptFieldMaxWidth}),
					DialogKeys:           resolveDialogKeys(c.PermissionDialogKeys).Keys,
					PromptNotifications:  append([]string{}, c.PromptNotifications...),
				}
			},
			RequestPermissionDecision: RequestPermissionDecision,
			ForwardingDir:             p.paths.ForwardingDir,
			Registry:                  subagentRegistry,
			Serving:                   servingLiveness,
			GetForwardingTimeoutMs: func() float64 {
				if t := p.configStore.Current().ForwardingTimeoutMs; t != nil {
					return *t
				}
				return PermissionForwardingTimeoutMs
			},
			Logger: p.logger,
		},
		Prompter: prompter,
		// The published service is the narrow, session-scoped PermissionQuery a chain link is handed. A thunk because the service is
		// constructed below; it resolves at session_start (activate), well after assignment.
		GetPermissionQuery: func() PermissionQuery { return service },
		AuthorizerRegistry: authorizerRegistry,
		GetAuthorizerChain: func() []string { return p.configStore.Current().AuthorizerChain },
		// Records each configured name this node could not resolve, and tells the operator once per name (#861).
		ChainAudit: NewAuthorizerChainAudit(p.logger),
	})

	// The resolver composes the manager and session ruleset; the service and gates share this one instance.
	p.resolver = NewPermissionResolver(p.manager, p.sessionRules)

	// Serving a forwarded request is resolution: the child-fixed ForwardedAccessIntent (ADR 0008) resolves directly against the
	// serving node's composed ruleset, agent-scoped to the requester; the match values are used as fixed by the child (#597).
	servingPolicy := servingPolicyFunc(func(intent ForwardedAccessIntent) PermissionCheckResult {
		switch resolved := BuildResolvedIntentFromMatchValues(intent.Surface, intent.MatchValues, intent.Principal.AgentName).(type) {
		case PathValuesAccessIntent:
			return p.resolver.ResolvePrefixed(resolved)
		case AccessIntent:
			return p.resolver.Resolve(resolved)
		}
		panic("serving policy: unknown resolved intent")
	})

	// Constructed here rather than beside the gate runner: the serving side broadcasts its own decisions, so both readers share one
	// reporter over this session's event bus.
	p.reporter = NewGateDecisionReporter(p.logger, events)

	requestServer := NewForwardedRequestServer(ForwardedRequestServerDeps{
		ForwardingDir: p.paths.ForwardingDir,
		Logger:        p.logger,
		Policy:        servingPolicy,
		Escalator:     p.selection,
		Broadcaster:   p.reporter,
		Recorder:      p.sessionRules,
		Registry:      subagentRegistry,
	})

	p.session = NewPermissionSession(p.paths,
		NewForwardingManager(ForwardingManagerDeps{Forwarder: requestServer, Serving: ComposeServingAnnouncers(servingRegistry, servingHeartbeats), Logger: p.logger}),
		p.manager, p.sessionRules, p.configStore, p.selection, p.hostFlavor)

	// No cwd or trust decision exists at factory init, so there is no project scope to withhold from (fail closed); session_start
	// reloads with the real cwd and trust decision. This load reports nothing: it cannot, and it must not consume the warning (#933).
	p.configStore.Refresh("", false)

	registerPermissionSystemCommand(ext, PermissionSystemConfigController{
		Config:     p.configStore,
		ConfigPath: getGlobalConfigPath(dir),
		GetActiveAgentConfigRules: func() Ruleset {
			return p.manager.GetComposedConfigRules(p.session.LastKnownActiveAgentName())
		},
	})

	// Sibling extensions register through the observing decorator, so a link offered to a node whose chain never runs is accepted
	// and recorded rather than vanishing (ADR 0012 decision 4). Chain resolution keeps reading the undecorated registry.
	service = NewLocalPermissionsService(p.resolver, p.session, formatterRegistry, accessExtractorRegistry,
		NewObservedAuthorizerRegistrar(authorizerRegistry, p.selection, p.logger))
	p.service = service

	// Subscribe to pi-subagents' child lifecycle events so child sessions register and unregister without the core calling us (ADR
	// 0002), and so a child that bound its extensions without loading one of ours is reported rather than silently ungated (#792).
	childNodeAudit := NewChildNodeAudit(func(sessionID string) bool { return GetPermissionsService(sessionID) != nil }, p.logger)
	unsubSubagentLifecycle, err := SubscribeSubagentLifecycle(lifecycleBus, subagentRegistry, childNodeAudit)
	if err != nil {
		unsubSubagentLifecycle = func() {}
		details := newObject()
		details.set("error", err.Error())
		p.logger.Review("subagent_lifecycle.subscribe_failed", details)
	}

	// PermissionServiceLifecycle owns the service publication: Activate publishes this node's service under its own session id and
	// announces it on the ready channel; Teardown unsubscribes all session listeners and unpublishes. Deferred to session_start
	// because both facts come from the context.
	p.serviceLifecycle = NewPermissionServiceLifecycle(service, p.selection, events, []func(){unsubSubagentLifecycle})

	p.audit = &DecisionAudit{}
	// Reads the store's issue list and tells the operator what is new, through the logger's warn sink (#933). Driven at
	// session_start and on every turn; the latch keeps an unchanged issue quiet.
	configIssueReporter := NewConfigIssueReporter(p.configStore, p.logger)
	p.lifecycle = NewSessionLifecycleHandler(p.session, p.resolver, p.serviceLifecycle, p.logger, p.audit, configIssueReporter, p.dialogQueue)
	turnPrep := NewSessionTurnPrep(p.session, func() { go bashparse.WarmBashParser() }, p.serviceLifecycle, configIssueReporter)
	p.agentPrep = NewAgentPrepHandler(turnPrep, p.session, p.resolver, agentToolsRef{p}, p.logger, subagentDetection)

	gateRunner := NewGateRunner(p.resolver, p.sessionRules, p.selection, p.reporter, isYoloEnabled)
	// This node's ancestors in the current process: the gates read their fact-shaping registrations through the inheriting lookups,
	// so a child whose own registry is missing an extractor still sees the path its tool touches (ADR 0012 decision 1, #793).
	ancestorNodes := NewAncestorNodes(p.serviceLifecycle, subagentRegistry, GetPermissionsService)
	toolCallPipeline := NewToolCallGatePipeline(p.resolver, p.session,
		NewInheritingToolInputFormatterLookup(formatterRegistry, ancestorNodes),
		NewInheritingToolAccessExtractorLookup(accessExtractorRegistry, ancestorNodes))
	p.gates = NewPermissionGateHandler(p.session, gateToolsRef{p}, toolCallPipeline, NewSkillInputGatePipeline(ResolverSkillInputInputs{Resolver: p.resolver}), gateRunner)
	p.toolCall = CreateFailClosedToolCall(func(event any, ctx sdk.Context) (GateOutcome, error) {
		p.binder.bind(ctx)
		return p.gates.HandleToolCall(event, p.contextOf(ctx))
	}, p.reporter, p.audit, p.logger)

	ext.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		p.binder.bind(ctx)
		reason, _ := data["reason"].(string)
		return nil, p.lifecycle.HandleSessionStart(SessionStartPayload{Reason: reason}, p.contextOf(ctx))
	})
	ext.OnEvent(sdk.EventResourcesDiscover, func(ctx sdk.Context, data map[string]any) (any, error) {
		p.binder.bind(ctx)
		reason, _ := data["reason"].(string)
		p.lifecycle.HandleResourcesDiscover(ResourcesDiscoverPayload{Reason: reason}, p.contextOf(ctx))
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, _ map[string]any) (any, error) {
		p.binder.bind(ctx)
		p.lifecycle.HandleSessionShutdown()
		return nil, nil
	})
	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		p.binder.bind(ctx)
		payload, err := beforeAgentStartPayload(ctx, data)
		if err != nil {
			return nil, err
		}
		if err := p.agentPrep.Handle(payload, p.contextOf(ctx)); err != nil {
			return nil, err
		}
		writeBackSkills(data, payload.Options)
		return nil, nil
	})
	ext.OnEvent(sdk.EventInput, func(ctx sdk.Context, data map[string]any) (any, error) {
		p.binder.bind(ctx)
		text, _ := data["text"].(string)
		result, err := p.gates.HandleInput(text, p.contextOf(ctx))
		if err != nil {
			// The gate could not decide (for example the active agent could not be read): the input is swallowed, never let through.
			ctx.Notify(UntrustedInputMessage(err), "warning")
			return map[string]any{"action": "handled"}, nil
		}
		if result.Action == "handled" {
			return map[string]any{"action": "handled"}, nil
		}
		return nil, nil
	})
	ext.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) {
		return p.toolCall(data, ctx).Value(), nil
	})
	return ext, p
}

// servingPolicyFunc adapts a function to ServingPolicy.
type servingPolicyFunc func(intent ForwardedAccessIntent) PermissionCheckResult

func (f servingPolicyFunc) Resolve(intent ForwardedAccessIntent) PermissionCheckResult {
	return f(intent)
}

// UntrustedInputMessage is the warning shown when an input could not be gated and was swallowed.
func UntrustedInputMessage(err error) string {
	return "pi-permission-system: could not decide whether this input is allowed (" + err.Error() + "); it was not sent."
}

// beforeAgentStartPayload reads the event the host hands the handler: the rendered system prompt (a string, ordered fragments, or
// the host's accessor when the event carries neither) and the mutable prompt options when the host supplies them.
func beforeAgentStartPayload(ctx sdk.Context, data map[string]any) (BeforeAgentStartPayload, error) {
	payload := BeforeAgentStartPayload{}
	switch prompt := data["systemPrompt"].(type) {
	case string:
		payload.SystemPrompt = prompt
	case []any:
		fragments := make([]string, 0, len(prompt))
		for _, fragment := range prompt {
			s, _ := fragment.(string)
			fragments = append(fragments, s)
		}
		payload.SystemPrompt = JoinSystemPromptFragments(fragments)
	default:
		rendered, err := ctx.GetSystemPrompt()
		if err != nil {
			return payload, err
		}
		payload.SystemPrompt = rendered
	}
	payload.Options = promptOptionsOf(data)
	return payload, nil
}

// promptOptionsOf reads data["systemPromptOptions"]; nil when the host supplies none (or its sections are not the ordered
// collection the SDK hands handlers, so nothing could be stated through them).
func promptOptionsOf(data map[string]any) *PromptOptions {
	raw, ok := data["systemPromptOptions"].(map[string]any)
	if !ok {
		return nil
	}
	sections, ok := raw["sections"].(PromptSections)
	if !ok {
		return nil
	}
	view := &PromptOptions{Sections: sections, ToolSnippets: map[string]string{}}
	view.CustomPrompt, _ = raw["customPrompt"].(string)
	if snippets, ok := raw["toolSnippets"].(map[string]any); ok {
		for name, snippet := range snippets {
			if s, ok := snippet.(string); ok {
				view.ToolSnippets[name] = s
			}
		}
	}
	if guidelines, ok := raw["promptGuidelines"].([]any); ok {
		for _, g := range guidelines {
			if s, ok := g.(string); ok {
				view.PromptGuidelines = append(view.PromptGuidelines, s)
			}
		}
	}
	if skills, ok := raw["skills"].([]any); ok {
		for _, skill := range skills {
			name := ""
			if record, ok := skill.(map[string]any); ok {
				name, _ = record["name"].(string)
			}
			view.Skills = append(view.Skills, PromptSkill{Name: name, Raw: skill})
		}
	}
	return view
}

// writeBackSkills stores the narrowed catalogue in the host's options, which Pi renders `<skills>` from after the chain.
func writeBackSkills(data map[string]any, options *PromptOptions) {
	if options == nil {
		return
	}
	raw, ok := data["systemPromptOptions"].(map[string]any)
	if !ok {
		return
	}
	kept := make([]any, len(options.Skills))
	for i, skill := range options.Skills {
		kept[i] = skill.Raw
	}
	raw["skills"] = kept
}
