package pi_permission_system

// Port of src/handlers/permission-gate-handler.ts: handles the permission gate events, tool_call and input.

// RequestedToolValidation is the discriminated result of validating a tool-call event's name and registration: Status "ok" carries
// ToolName, Status "block" carries Reason.
type RequestedToolValidation struct {
	Status   string
	ToolName string
	Reason   string
}

// ToolCallEvaluator is the tool-call pipeline seam; *ToolCallGatePipeline satisfies it.
type ToolCallEvaluator interface {
	Evaluate(tcc ToolCallContext, runner GateRunning) (GateOutcome, error)
}

// SkillInputEvaluator is the skill-input pipeline seam; *SkillInputGatePipeline satisfies it.
type SkillInputEvaluator interface {
	Evaluate(skillName string, agentName *string, notifier GateNotifier, runner GateRunning) (GateOutcome, error)
}

// GateHandlerSession is the part of PermissionSession the handler drives: bind the per-event context, resolve the agent name.
type GateHandlerSession interface {
	Activate(ctx SessionContext) error
	ResolveAgentName(ctx ActiveAgentContext, systemPrompt string) (*string, error)
}

// InputEventResult is the result of the input event: Action "continue" or "handled".
type InputEventResult struct{ Action string }

// PermissionGateHandler handles the permission gate events tool_call and input. Its dependencies: the session (state and lifecycle
// owner), the Pi tool API subset, the pipeline that owns tool-call gate assembly and the run loop, the pipeline that owns skill-input
// gate assembly, and the pre-built gate runner.
type PermissionGateHandler struct {
	session            GateHandlerSession
	toolRegistry       ToolRegistry
	pipeline           ToolCallEvaluator
	skillInputPipeline SkillInputEvaluator
	runner             GateRunning
}

// NewPermissionGateHandler is the constructor.
func NewPermissionGateHandler(session GateHandlerSession, toolRegistry ToolRegistry, pipeline ToolCallEvaluator, skillInputPipeline SkillInputEvaluator, runner GateRunning) *PermissionGateHandler {
	return &PermissionGateHandler{session: session, toolRegistry: toolRegistry, pipeline: pipeline, skillInputPipeline: skillInputPipeline, runner: runner}
}

// HandleToolCall decides a tool_call event. An error is the original's rejection: the tool-call boundary blocks on it (never an
// allow), as it does on a panic.
func (h *PermissionGateHandler) HandleToolCall(event any, ctx SessionContext) (GateOutcome, error) {
	if err := h.session.Activate(ctx); err != nil {
		return GateOutcome{}, err
	}

	validation := ValidateRequestedTool(event, h.toolRegistry.GetAll())
	if validation.Status == "block" {
		return GateOutcome{Action: "block", Reason: validation.Reason}, nil
	}
	toolName := validation.ToolName

	agentName, err := h.session.ResolveAgentName(ctx, "")
	if err != nil {
		return GateOutcome{}, err
	}

	input := GetEventInput(event)
	toolCallID := ""
	if id, ok := ToRecord(event).str("toolCallId"); ok {
		toolCallID = id
	}

	tcc := ToolCallContext{ToolName: toolName, AgentName: agentName, Input: input, ToolCallID: toolCallID, Cwd: ctx.Cwd}
	return h.pipeline.Evaluate(tcc, h.runner)
}

// ctxNotifier warns through the context's UI, when it has one.
type ctxNotifier struct{ ctx SessionContext }

func (n ctxNotifier) Warn(message string) {
	if n.ctx.HasUI && n.ctx.Notifier != nil {
		n.ctx.Notifier.Notify(message, "warning")
	}
}

// HandleInput decides an input event: a `/skill:<name>` input is gated on the skill surface, and a block is "handled" (swallowed).
func (h *PermissionGateHandler) HandleInput(text string, ctx SessionContext) (InputEventResult, error) {
	if err := h.session.Activate(ctx); err != nil {
		return InputEventResult{}, err
	}

	skillName, ok := ExtractSkillNameFromInput(text)
	if !ok {
		return InputEventResult{Action: "continue"}, nil
	}

	agentName, err := h.session.ResolveAgentName(ctx, "")
	if err != nil {
		return InputEventResult{}, err
	}
	outcome, err := h.skillInputPipeline.Evaluate(skillName, agentName, ctxNotifier{ctx: ctx}, h.runner)
	if err != nil {
		return InputEventResult{}, err
	}
	if outcome.Action == "block" {
		return InputEventResult{Action: "handled"}, nil
	}
	return InputEventResult{Action: "continue"}, nil
}

// ValidateRequestedTool validates the tool name from a raw event against the registered tool list. It returns the raw tool name (not
// the normalised form) so ToolCallContext.ToolName stays identical to the pre-extraction behaviour.
func ValidateRequestedTool(event any, availableTools []any) RequestedToolValidation {
	toolName, ok := GetToolNameFromValue(event)
	if !ok {
		return RequestedToolValidation{Status: "block", Reason: FormatMissingToolNameReason()}
	}
	check := CheckRequestedToolRegistration(&toolName, availableTools, nil)
	switch check.Status {
	case "missing-tool-name":
		return RequestedToolValidation{Status: "block", Reason: FormatMissingToolNameReason()}
	case "unregistered":
		return RequestedToolValidation{Status: "block", Reason: FormatUnknownToolReason(check.RequestedToolName, check.AvailableToolNames)}
	}
	return RequestedToolValidation{Status: "ok", ToolName: toolName}
}

// GetEventInput extracts the tool input from an event, checking both `input` and `arguments` (different Pi SDK versions use
// different names); an event with neither has an empty input object.
func GetEventInput(event any) any {
	record := ToRecord(event)
	for _, key := range []string{"input", "arguments"} {
		if v, ok := record.get(key); ok {
			if _, undefined := v.(undef); !undefined {
				return v
			}
		}
	}
	return newObject()
}

// ExtractSkillNameFromInput parses a `/skill:<name>` prefix from user input: the skill name, ok false when the text is not a skill
// invocation.
func ExtractSkillNameFromInput(text string) (string, bool) {
	trimmed := jsTrim(text)
	const prefix = "/skill:"
	if len(trimmed) < len(prefix) || trimmed[:len(prefix)] != prefix {
		return "", false
	}
	afterPrefix := trimmed[len(prefix):]
	if afterPrefix == "" {
		return "", false
	}
	end := len(afterPrefix)
	for i, r := range afterPrefix {
		if isJSSpace(r) {
			end = i
			break
		}
	}
	skillName := jsTrim(afterPrefix[:end])
	return skillName, skillName != ""
}
