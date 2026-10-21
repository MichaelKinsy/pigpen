package pi_permission_system

import "strings"

// Port of src/handlers/before-agent-start.ts: tool filtering and prompt sanitization at before_agent_start.

// PromptSections is the ordered mutable `sections` collection of Pi's prompt options (sdk.SystemPromptSections satisfies it through
// its pointer receiver).
type PromptSections interface {
	Set(name, value string)
}

// PromptSkill is one entry of the skill catalogue Pi renders: only its name is read here, and the entry itself is carried through
// untouched (the host's own record).
type PromptSkill struct {
	Name string
	// Raw is the host's record of the skill, written back when the entry is kept.
	Raw any
}

// PromptOptions are the mutable parts Pi renders the prompt from: later handlers, and Pi itself, see what this handler writes here.
// CustomPrompt says whether Pi wrote a preamble at all (under one it writes no tool surface); ToolSnippets and PromptGuidelines are
// what a child's own tool surface renders from; Sections is where it is stated; Skills is the catalogue Pi renders, which policy
// narrows (the handler assigns the kept entries back to Skills).
type PromptOptions struct {
	CustomPrompt     string
	ToolSnippets     map[string]string
	PromptGuidelines []string
	Sections         PromptSections
	Skills           []PromptSkill
}

// BeforeAgentStartPayload is the minimal subset of BeforeAgentStartEvent the handler uses. SystemPrompt is the rendered prompt (a
// host that supplies ordered fragments has them joined with a newline before this point). Options is nil when the host supplies no
// systemPromptOptions: only tool filtering and skill path resolution run then, there being no mutable section or skill catalogue to
// narrow.
type BeforeAgentStartPayload struct {
	SystemPrompt string
	Options      *PromptOptions
}

// JoinSystemPromptFragments normalizes an ordered-fragments prompt to the string the handler reads.
func JoinSystemPromptFragments(fragments []string) string { return strings.Join(fragments, "\n") }

// ShouldExposeTool reports whether the tool should be exposed to the agent. A tool is withheld only when every value under its
// surface resolves to deny, so a blanket `bash: deny` hides the tool entirely while a partially permissive
// `bash: {"*": "deny", "git *": "ask"}` keeps it reachable (#815).
func ShouldExposeTool(toolName string, agentName *string, isToolFullyDenied func(toolName string, agentName *string) bool) bool {
	return !isToolFullyDenied(toolName, agentName)
}

// AgentToolRegistry is the Pi tool API subset the handler reads and writes. Unlike ToolRegistry, a host call can fail here, and a
// failed read must not be taken for an empty tool set: the handler leaves the active set alone and returns the error (the tool-call
// gate still denies a call to a denied tool).
type AgentToolRegistry interface {
	GetAll() ([]any, error)
	GetActive() ([]string, error)
	SetActive(names []string)
}

// AgentPrepSession is the part of PermissionSession the handler drives.
type AgentPrepSession interface {
	ResolveAgentName(ctx ActiveAgentContext, systemPrompt string) (*string, error)
	ResolveExposedTools(observation ToolSurfaceObservation, isExposed func(toolName string) bool) ToolSurfaceResolution
	SetActiveSkillEntries(entries []SkillPromptEntry)
	GetPathNormalizer() *PathNormalizer
}

// AgentPrepResolver is the part of the resolver the handler reads: the full-denial check, and the raw check (no session rules) the
// skill sanitizer judges names with.
type AgentPrepResolver interface {
	IsToolFullyDenied(toolName string, agentName *string) bool
	CheckPermission(surface string, input any, agentName *string, sessionRules Ruleset) PermissionCheckResult
}

// rawSkillChecker is the resolver's check with no session rules, which is what the skill sanitizer's checker contract is.
type rawSkillChecker struct{ resolver AgentPrepResolver }

func (c rawSkillChecker) CheckPermission(surface string, input any, agentName *string) PermissionCheckResult {
	return c.resolver.CheckPermission(surface, input, agentName, nil)
}

// AgentPrepHandler handles before_agent_start: tool filtering and prompt sanitization.
//
// Prompt changes are stated through the payload's options, never as a returned system prompt: a returned prompt is frozen for the
// run, so the sections an extension later in the chain adds (Pi's own `<mcp_servers>` among them) would never reach the provider
// (#999). Narrowing the active set is enough for a prompt Pi wrote: Pi renders its tools and rules from the reconciled active set. A
// subagent child's prompt is always a custom one, under which Pi writes neither, so a child states its own as sections. A root under
// a custom prompt states none, matching Pi. The active set is recomputed from the session's pre-filter tool surface every turn, so
// relaxing a rule restores the tool it had withheld (#873).
type AgentPrepHandler struct {
	turnPrep     TurnPreparation
	session      AgentPrepSession
	resolver     AgentPrepResolver
	toolRegistry AgentToolRegistry
	logger       DebugLogger
	detector     SubagentDetector
}

// NewAgentPrepHandler is the constructor.
func NewAgentPrepHandler(turnPrep TurnPreparation, session AgentPrepSession, resolver AgentPrepResolver, toolRegistry AgentToolRegistry,
	logger DebugLogger, detector SubagentDetector) *AgentPrepHandler {
	return &AgentPrepHandler{turnPrep: turnPrep, session: session, resolver: resolver, toolRegistry: toolRegistry, logger: logger, detector: detector}
}

// Handle prepares the turn, filters the tool surface and sanitizes the prompt options. An error is the original's rejection: a
// failing turn preparation, a failing agent-name lookup (never read as "no agent", which would apply the global scope), a failing
// tool-registry read or a failing subagent detection. Nothing is narrowed by a handler that failed.
func (h *AgentPrepHandler) Handle(event BeforeAgentStartPayload, ctx SessionContext) error {
	if err := h.turnPrep.Prepare(ctx); err != nil {
		return err
	}

	systemPrompt := event.SystemPrompt
	agentName, err := h.session.ResolveAgentName(ctx, systemPrompt)
	if err != nil {
		return err
	}
	all, err := h.toolRegistry.GetAll()
	if err != nil {
		return err
	}
	registered := ReadRegisteredTools(all)
	activeTools, err := h.toolRegistry.GetActive()
	if err != nil {
		return err
	}
	registeredSet := make(map[string]bool, len(registered.Names))
	for _, name := range registered.Names {
		registeredSet[name] = true
	}
	active := make([]any, len(activeTools))
	for i, name := range activeTools {
		active[i] = name
	}
	surface := h.session.ResolveExposedTools(
		ToolSurfaceObservation{Active: ReadRegisteredTools(active).Names, Registered: registeredSet},
		func(toolName string) bool { return ShouldExposeTool(toolName, agentName, h.resolver.IsToolFullyDenied) },
	)
	allowedTools := append([]string{}, surface.exposed...)

	h.toolRegistry.SetActive(allowedTools)
	if surface.changed {
		details := newObject()
		details.set("exposed", stringsToAny(surface.exposed))
		details.set("withheld", stringsToAny(surface.withheld))
		details.set("restored", stringsToAny(surface.restored))
		h.logger.Debug("tool_surface.changed", details)
	}

	options := event.Options
	if options != nil {
		underCustomPrompt, err := h.isSubagentUnderCustomPrompt(options, ctx)
		if err != nil {
			return err
		}
		if underCustomPrompt {
			sections := RenderToolSurfaceSections(ToolSurfaceInputs{
				AllowedTools:     allowedTools,
				ToolSnippets:     options.ToolSnippets,
				GuidelinesByTool: registered.GuidelinesByTool,
				PromptGuidelines: options.PromptGuidelines,
			})
			// An empty section is one Pi leaves out, so this also clears a stale peer list.
			tools := ""
			if sections.Tools != nil {
				tools = *sections.Tools
			}
			options.Sections.Set("tools", tools)
			options.Sections.Set("rules", sections.Rules)
		}
	}

	// Path-match entries come from every catalogue the rendered prompt lists, read before the skill list is narrowed.
	h.session.SetActiveSkillEntries(VisibleSkillPromptEntries(systemPrompt, rawSkillChecker{h.resolver}, agentName, h.session.GetPathNormalizer()))
	if options == nil {
		return nil
	}

	// Denials are judged on the list Pi renders `<skills>` from, not on the rendered prompt: that prompt predates this turn's tool
	// changes, and on the turn read and bash return from a full denial it lists no catalogue.
	options.Skills = WithoutDeniedSkills(options.Skills, func(s PromptSkill) string { return s.Name }, rawSkillChecker{h.resolver}, agentName)
	return nil
}

// isSubagentUnderCustomPrompt is whether this node states its own tool surface through sections. Only a subagent child does: its
// prompt is always a custom one, under which Pi writes no tool list or rules, and its inherited identity carries none either. A root
// needs none: Pi renders its own from the narrowed active set, or, under an operator's custom prompt, deliberately writes none.
// hasCustomPrompt is Pi's own `if (customPrompt)`, so an empty string reads as none.
func (h *AgentPrepHandler) isSubagentUnderCustomPrompt(options *PromptOptions, ctx SessionContext) (bool, error) {
	if options.CustomPrompt == "" {
		return false, nil
	}
	return h.detector.IsSubagent(detectionContext(ctx.AuthorityContext))
}

func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
