package pi_permission_system

import "strings"

// Port of src/presentation/agent-renderer.ts: the agent-facing render of a refused permission ask (ADR 0011 section 7).
//
// The rule that governs this renderer and no other: the agent renderer identifies the call; it does not reproduce it. The agent
// authored the tool call, and the harness returns this text as that call's own tool result with its arguments still in context.
// What is new is the verdict: which surface gated the call, which rule matched, which of the call's operands tripped it, and what
// the human said. The command is the one value never rendered; the flagged element (a path, an MCP target, a skill) is rendered,
// capped rather than structurally bounded.

// ExtensionTag is the attribution tag on every block reason this extension produces (EXTENSION_TAG).
const ExtensionTag = "[" + extensionID + "]"

// AgentRenderBudget is how much room the flagged element has, as the operator configured it.
type AgentRenderBudget struct {
	// FieldMaxWidth is the maximum characters (UTF-16 units) of the flagged element's text.
	FieldMaxWidth float64
}

// DefaultAgentRenderBudget is the budget every render function falls back to when the caller passes none (DEFAULT_RENDER_BUDGET).
var DefaultAgentRenderBudget = AgentRenderBudget{FieldMaxWidth: DefaultRenderBudget.FieldMaxWidth}

// RenderRefusal is the agent-facing render a refused ask earns, chosen by what refused it. It is the single dispatch point for the
// refusal renderers below, so which sentence the agent gets is decided once from the decider stamped on the decision. A forwarded
// decision is dispatched on the decider inside the responding session: the hop says where, and the agent is told what.
//
// As in the original, the gate_error render is called without the caller's budget (it renders at the default budget).
func RenderRefusal(payload PromptPayload, decidedBy DecisionSource, denialReason *string, budget AgentRenderBudget) string {
	decider := EffectiveDecider(decidedBy)
	// The hop the unwrap discarded: where the decision was made. Kept as a bare fact rather than the responder's identity.
	decidedElsewhere := decidedBy.Kind == "forwarded"
	switch decider.Kind {
	case "authorizer":
		return RenderAuthorizerDenial(payload, decider.Name, denialReason, budget)
	case "unavailable":
		return RenderUnavailableDenial(payload, denialReason, budget)
	case "rule":
		return RenderEscalatedPolicyDenial(payload, EscalatedRule{Pattern: decider.Pattern, DecidedElsewhere: decidedElsewhere}, denialReason, budget)
	case "gate_error":
		reason := ""
		if decider.Reason != nil {
			reason = *decider.Reason
		}
		return RenderGateErrorDenial(payload, GateFailure{Reason: reason, DecidedElsewhere: decidedElsewhere}, DefaultAgentRenderBudget)
	}
	// user is the render's true subject: a human refused. session_approval, infrastructure_read and yolo only ever allow, so they
	// are unreachable here; a forwarded decision that reaches this far named no inner decider (an older responder), so the hop is
	// all that is known and the user render is the fail-soft answer.
	return RenderUserDenial(payload, denialReason, budget)
}

// RenderPolicyDenial is the agent-facing render of a policy deny.
func RenderPolicyDenial(payload PromptPayload, ruleReason *string, budget AgentRenderBudget) string {
	return taggedReason("Denied by policy: "+identification(payload, budget, "", askRuleClause(payload))+boundaryClause(payload)+provenanceClause(payload)+".", ruleReason)
}

// RenderUserDenial is the agent-facing render of a human's denial at an interactive prompt.
func RenderUserDenial(payload PromptPayload, denialReason *string, budget AgentRenderBudget) string {
	return taggedReason("The user denied this "+identification(payload, budget, "call", askRuleClause(payload))+boundaryClause(payload)+provenanceClause(payload)+".", denialReason)
}

// RenderAuthorizerDenial is the agent-facing render of a registered authorizerChain link's refusal. It names the link, because "a
// policy extension the operator configured refused this" and "the operator refused this" are different facts. The name is operator
// configuration rather than agent input, so it is not capped.
func RenderAuthorizerDenial(payload PromptPayload, linkName string, denialReason *string, budget AgentRenderBudget) string {
	return taggedReason("The '"+linkName+"' authorizer denied this "+identification(payload, budget, "call", askRuleClause(payload))+boundaryClause(payload)+provenanceClause(payload)+".", denialReason)
}

// EscalatedRule is the rule that refused an escalated ask, and where it sat.
type EscalatedRule struct {
	// Pattern is the pattern the deciding node's rule matched; nil when none was recorded.
	Pattern *string
	// DecidedElsewhere is whether that node was reached through a forwarding hop.
	DecidedElsewhere bool
}

// RenderEscalatedPolicyDenial is the agent-facing render of a policy denial an escalation came back with. It differs from
// RenderPolicyDenial in which rule it names: here the ask was escalated, so the payload's rule is the one that raised the ask and
// the rule that denied lives on the decision.
func RenderEscalatedPolicyDenial(payload PromptPayload, rule EscalatedRule, denialReason *string, budget AgentRenderBudget) string {
	decidedRule := ruleClause(rule.Pattern, payload.Request.CommandContext)
	return taggedReason("A policy rule"+servingClause(rule.DecidedElsewhere)+" denied this "+identification(payload, budget, "call", decidedRule)+boundaryClause(payload)+provenanceClause(payload)+".", denialReason)
}

// GateFailure is the escalation failure that blocked an ask fail-closed, and where it happened.
type GateFailure struct {
	// Reason is the error text stamped on the decision, which carries the detail here.
	Reason string
	// DecidedElsewhere is whether the failing authority was reached through a forwarding hop.
	DecidedElsewhere bool
}

// RenderGateErrorDenial is the agent-facing render of an escalation that threw rather than ruling. Nobody denied this call: the
// authority that would have answered broke, and the boundary blocked rather than allowed.
func RenderGateErrorDenial(payload PromptPayload, failure GateFailure, budget AgentRenderBudget) string {
	reason := failure.Reason
	return taggedReason("The permission authority"+servingClause(failure.DecidedElsewhere)+" failed to answer this "+identification(payload, budget, "call", askRuleClause(payload))+", so it was blocked (fail-closed).", &reason)
}

// RenderUnavailableDenial is the agent-facing render when no live authority could answer the ask.
func RenderUnavailableDenial(payload PromptPayload, denialReason *string, budget AgentRenderBudget) string {
	return taggedReason("This "+identification(payload, budget, "call", askRuleClause(payload))+" requires approval, but no interactive UI is available.", denialReason)
}

// servingClause is where the deciding authority sat, when it was not this session. It says that another session decided and never
// which one.
func servingClause(decidedElsewhere bool) string {
	if decidedElsewhere {
		return " in the session serving this request"
	}
	return ""
}

func taggedReason(sentence string, reason *string) string {
	return ExtensionTag + " " + sentence + reasonClause(reason)
}

// identification is what was refused, in the order a reader needs it: the gate surface, the tool that reached it, who asked, which
// of the call's operands was flagged, and the rule that fired. callWord is the noun the verdict needs after the surface; ruleText is
// supplied rather than read off the payload, because an escalated refusal names the rule that decided it.
func identification(payload PromptPayload, budget AgentRenderBudget, callWord, ruleText string) string {
	clauses := []string{
		"'" + payload.Request.Surface + "'",
		callWord,
		invokedAsClause(payload),
		toolClause(payload),
		agentClause(payload),
		flaggedClause(payload, budget),
		ruleText,
	}
	kept := clauses[:0]
	for _, clause := range clauses {
		if clause != "" {
			kept = append(kept, clause)
		}
	}
	return strings.Join(kept, " ")
}

// toolClause is the gated tool, named only when the surface has not already named it.
func toolClause(payload PromptPayload) string {
	request := payload.Request
	if request.ToolName == nil || *request.ToolName == request.Surface {
		return ""
	}
	return "for tool '" + *request.ToolName + "'"
}

// invokedAsClause is the name the agent actually called, when a shell alias re-exposed bash.
func invokedAsClause(payload PromptPayload) string {
	if payload.Request.InvokedToolName == nil {
		return ""
	}
	return "(invoked as '" + *payload.Request.InvokedToolName + "')"
}

// agentClause is which agent asked, when the ask carries a name.
func agentClause(payload PromptPayload) string {
	if agentName := payload.Request.Requester.AgentName; agentName != nil && *agentName != "" {
		return "for agent '" + *agentName + "'"
	}
	return ""
}

// flaggedClause is which of the call's operands the rule fired on. It is omitted for a bash ask, whose flagged element is the
// command the renderer never echoes; for a generic tool ask, whose value is the tool name an earlier clause already stated; and for
// a payload-less forwarded relay, whose value shape is unknown, so it cannot be shown to not be a command.
func flaggedClause(payload PromptPayload, budget AgentRenderBudget) string {
	if payload.Kind == "bash" || payload.Kind == "forwarded" {
		return ""
	}
	label := FlaggedElementLabel(payload)
	var elements []string
	for _, element := range FlaggedElements(payload) {
		if payload.Request.ToolName == nil || element != *payload.Request.ToolName {
			elements = append(elements, element)
		}
	}
	if len(elements) == 0 {
		return ""
	}
	noun := label
	if len(elements) != 1 {
		noun = label + "s"
	}
	rendered := make([]string, len(elements))
	for i, element := range elements {
		rendered[i] = "'" + capElement(element, budget) + "'" + resolvedAlias(payload, element)
	}
	return "for " + noun + " " + strings.Join(rendered, ", ")
}

// resolvedAlias is the canonical target of a flagged path, when it names somewhere else.
func resolvedAlias(payload PromptPayload, element string) string {
	var resolved string
	if entry := FindEvidence(payload, "resolves to"); entry != nil {
		resolved = entry.Text
	} else {
		for _, entry := range AllEvidence(payload, "external path") {
			if entry.Text == element {
				if entry.Detail != nil {
					resolved = *entry.Detail
				}
				break
			}
		}
	}
	if resolved == "" {
		return ""
	}
	return " (resolves to '" + resolved + "')"
}

// askRuleClause is the rule that raised this session's own ask.
func askRuleClause(payload PromptPayload) string {
	return ruleClause(payload.Request.MatchedPattern, payload.Request.CommandContext)
}

// ruleClause is the rule that fired, with the nested context that makes it intelligible. The pattern is a parameter rather than a
// payload read: an escalated refusal renders the rule that decided it. The command context is a fact about the call either way, so
// it always comes from the payload.
func ruleClause(pattern *string, commandContext *string) string {
	var parts []string
	if pattern != nil {
		parts = append(parts, "rule '"+*pattern+"'")
	}
	if context, ok := DescribeBashCommandContext(commandContext); ok {
		parts = append(parts, "inside "+context)
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// boundaryClause is the working directory the flagged paths escaped.
func boundaryClause(payload PromptPayload) string {
	if entry := FindEvidence(payload, "working directory"); entry != nil && entry.Text != "" {
		return ": outside working directory '" + entry.Text + "'"
	}
	return ""
}

// provenanceClause is the path a skill read reached its skill through.
func provenanceClause(payload PromptPayload) string {
	if entry := FindEvidence(payload, "read path"); entry != nil && entry.Text != "" {
		return ", reached via '" + entry.Text + "'"
	}
	return ""
}

func reasonClause(reason *string) string {
	if reason != nil && *reason != "" {
		return " Reason: " + *reason + "."
	}
	return ""
}

// capElement narrows the flagged element to the budget. The command is never rendered, so this bounds the only agent-supplied
// value that reaches the agent. A quantity bound applied uniformly, never a content filter, with the same bare-ellipsis marker the
// dialog uses.
func capElement(text string, budget AgentRenderBudget) string {
	n := utf16LenWTF(text)
	if float64(n) <= budget.FieldMaxWidth {
		return text
	}
	return utf16Prefix(text, jsSliceEnd(budget.FieldMaxWidth, n)) + "\u2026"
}
