package pi_permission_system

// Port of src/service/permission-ui-prompt.ts (buildUiPrompt) and the permissions:ui_prompt half of src/service/permission-events.ts.

// PermissionsUIPromptChannel is emitted immediately before the active user-facing permission UI is shown
// (PERMISSIONS_UI_PROMPT_CHANNEL).
const PermissionsUIPromptChannel = "permissions:ui_prompt"

// PermissionUiPromptSource is the origin of a UI prompt: tool_call, skill_input or skill_read. Forwarding is orthogonal to origin: a
// forwarded subagent prompt keeps its original source and is identified by a non-nil Forwarding.
type PermissionUiPromptSource = string

// PermissionUiPromptEvent is the payload emitted on permissions:ui_prompt. Lean by design: Surface and Value are the normalized
// display projection a notification consumer reads, Request is the ask's invariant core verbatim (no evidence, no annotations: the
// bus is the narrowest renderer, ADR 0011 section 6), and Forwarding is non-nil only for forwarded subagent prompts. A nil pointer is
// the original's null.
type PermissionUiPromptEvent struct {
	RequestID  string
	Source     PermissionUiPromptSource
	Surface    *string
	Value      *string
	AgentName  *string
	Request    PromptRequestFacts
	Forwarding *ForwardedPromptContext
}

// JS is the event as the ordered object JSON.stringify writes: the field order of the original's interface.
func (e PermissionUiPromptEvent) JS() *jsObject {
	o := newObject()
	o.set("requestId", e.RequestID)
	o.set("source", e.Source)
	o.set("surface", strOrNull(e.Surface))
	o.set("value", strOrNull(e.Value))
	o.set("agentName", strOrNull(e.AgentName))
	o.set("request", PromptPayload{Request: e.Request}.JS().obj("request"))
	if e.Forwarding == nil {
		o.set("forwarding", nil)
	} else {
		f := newObject()
		f.set("requesterAgentName", strOrNull(e.Forwarding.RequesterAgentName))
		f.set("requesterSessionId", strOrNull(e.Forwarding.RequesterSessionID))
		o.set("forwarding", f)
	}
	return o
}

// UiPromptInput is the input for any UI prompt, direct or forwarded. A direct prompt supplies only the DirectPromptInput fields and
// lets Surface and Value derive and Forwarding default to null; a forwarded ask supplies the child's original surface and value
// projection explicitly (an explicit null is honoured, not treated as "derive") and a populated Forwarding.
type UiPromptInput struct {
	RequestID  string
	Source     PermissionUiPromptSource
	AgentName  *string
	Payload    PromptPayload
	ToolName   *string
	SkillName  *string
	Path       *string
	Command    *string
	Target     *string
	Surface    DisplayString
	Value      DisplayString
	Forwarding *ForwardedPromptContext
}

// BuildUiPrompt builds a permissions:ui_prompt event from either a direct or a forwarded ask. Surface and Value use the explicit
// override when the caller sets them, otherwise they fall back to the direct-prompt projection.
func BuildUiPrompt(input UiPromptInput) PermissionUiPromptEvent {
	surface, value := input.Surface.Value, input.Value.Value
	if !input.Surface.Set {
		surface = directSurface(input)
	}
	if !input.Value.Set {
		value = directValue(input)
	}
	return PermissionUiPromptEvent{
		RequestID:  input.RequestID,
		Source:     input.Source,
		Surface:    surface,
		Value:      value,
		AgentName:  input.AgentName,
		Request:    input.Payload.Request,
		Forwarding: input.Forwarding,
	}
}

// uiPromptInputOf is the details-to-input projection every call site shares. A nil details.Surface or Value is "derive": a forwarded
// ask whose wire field was an explicit null derives the same null, because its details carry no tool, command, path, target or
// skill name to derive from.
func uiPromptInputOf(d *PromptPermissionDetails) UiPromptInput {
	input := UiPromptInput{
		RequestID: d.RequestID,
		Source:    d.Source,
		AgentName: d.AgentName,
		Payload:   d.Payload,
		ToolName:  d.ToolName,
		SkillName: d.SkillName,
		Path:      d.Path,
		Command:   d.Command,
		Target:    d.Target,
	}
	if d.Surface != nil {
		input.Surface = DisplayString{Set: true, Value: d.Surface}
	}
	if d.Value != nil {
		input.Value = DisplayString{Set: true, Value: d.Value}
	}
	if d.Forwarding != nil {
		input.Forwarding = &ForwardedPromptContext{RequesterAgentName: d.Forwarding.RequesterAgentName, RequesterSessionID: d.Forwarding.RequesterSessionID}
	}
	return input
}

// BuildUiPromptFromDetails is buildUiPrompt(details): the ui_prompt event of an ask's details.
func BuildUiPromptFromDetails(details *PromptPermissionDetails) PermissionUiPromptEvent {
	return BuildUiPrompt(uiPromptInputOf(details))
}

// directSurface is the normalized display surface for a direct prompt.
func directSurface(input UiPromptInput) *string {
	if input.Source == "skill_input" || input.Source == "skill_read" {
		s := "skill"
		return &s
	}
	return input.ToolName
}

// directValue is the normalized display value for a direct prompt: command, then path, target, skill name, tool name.
func directValue(input UiPromptInput) *string {
	for _, v := range []*string{input.Command, input.Path, input.Target, input.SkillName, input.ToolName} {
		if v != nil {
			return v
		}
	}
	return nil
}

// EmitUiPromptEvent broadcasts a permissions:ui_prompt event, immediately before invoking the active user-facing permission UI.
// Observational: a failing or panicking consumer must not block the permission dialog itself.
func EmitUiPromptEvent(events PermissionEventBus, event PermissionUiPromptEvent) {
	defer func() { _ = recover() }()
	_ = events.Emit(PermissionsUIPromptChannel, event.JS())
}
