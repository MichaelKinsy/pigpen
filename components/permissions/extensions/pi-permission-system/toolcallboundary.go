package pi_permission_system

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/handlers/tool-call-boundary.ts: the only tool_call handler the host sees. It guarantees fail-closed: it owns the
// recover-and-block and is the sole place a GateOutcome becomes the host result, so "we didn't decide" can never mean "allow".

// ToolCallResult is the host-facing result of a tool_call handler: the zero value lets the call run.
type ToolCallResult struct {
	Block  bool
	Reason string
}

// Value is the handler return the SDK expects: nil to allow, {block, reason} to block.
func (r ToolCallResult) Value() any {
	if !r.Block {
		return nil
	}
	return map[string]any{"block": true, "reason": r.Reason}
}

// DecisionTracer is the narrow debug surface for the per-call decision trace. The concrete logger self-gates on debugLog.
type DecisionTracer interface {
	Debug(event string, details *jsObject)
}

// GateFunc is a gate: it returns its outcome, or an error (a panic is read the same way).
type GateFunc func(event any, ctx sdk.Context) (GateOutcome, error)

// CreateFailClosedToolCall wraps gate. A gate that errors or panics, or a recorder or tracer that panics on the success path,
// blocks the call, records the error and writes a gate_error review-log entry.
func CreateFailClosedToolCall(gate GateFunc, reporter DecisionReporter, audit DecisionRecorder, tracer DecisionTracer) func(event any, ctx sdk.Context) ToolCallResult {
	return func(event any, ctx sdk.Context) ToolCallResult {
		result, thrown, failed := runGate(gate, audit, tracer, event, ctx)
		if !failed {
			return result
		}
		recordGateError(reporter, audit, event, thrown)
		return ToolCallResult{Block: true, Reason: formatGateErrorReason(thrown)}
	}
}

func runGate(gate GateFunc, audit DecisionRecorder, tracer DecisionTracer, event any, ctx sdk.Context) (result ToolCallResult, thrown any, failed bool) {
	defer func() {
		if r := recover(); r != nil {
			thrown, failed = r, true
		}
	}()
	outcome, err := gate(event, ctx)
	if err != nil {
		return ToolCallResult{}, err, true
	}
	audit.RecordDecision(outcome.Action)
	details := newObject()
	details.set("toolName", bestEffortToolName(event))
	details.set("action", outcome.Action)
	if outcome.Action == "block" {
		details.set("reason", outcome.Reason)
	}
	tracer.Debug("permission.decision", details)
	if outcome.Action == "block" {
		return ToolCallResult{Block: true, Reason: outcome.Reason}, nil, false
	}
	return ToolCallResult{}, nil, false
}

// recordGateError records a gate error without ever panicking: the block must be reached. The request id is minted here and
// shared by the review entry and the broadcast so they join; no rule won and the boundary holds no session, so origin,
// matchedPattern and agentName are null (#753).
func recordGateError(reporter DecisionReporter, audit DecisionRecorder, event any, thrown any) {
	defer func() { _ = recover() }()
	audit.RecordError()
	reason := errorMessage(thrown)
	requestID := CreatePermissionRequestId()
	toolName := bestEffortToolName(event)
	command, hasCommand := bestEffortCommand(event)

	details := newObject()
	details.set("requestId", requestID)
	details.set("toolName", toolName)
	if hasCommand {
		details.set("command", command)
	} else {
		details.set("command", undef{})
	}
	details.set("resolution", "gate_error")
	details.set("error", reason)
	decidedBy := newObject()
	decidedBy.set("kind", "gate_error")
	decidedBy.set("reason", reason)
	details.set("decidedBy", decidedBy)
	reporter.WriteReviewLog("permission_request.blocked", details)

	value := toolName
	if hasCommand {
		value = command
	}
	reporter.EmitDecision(PermissionDecisionEvent{
		RequestID: requestID, Surface: toolName, Value: value, Result: "deny", Resolution: "gate_error",
	})
}

// logToRecord reads value as a record: an object itself, anything else an empty record (toRecord).
func logToRecord(value any) *jsObject {
	switch v := value.(type) {
	case *jsObject:
		if v != nil {
			return v
		}
	case map[string]any:
		return orderedRecord(v)
	}
	return newObject()
}

// firstDefined is `a ?? b` for a record's two keys.
func firstDefined(r *jsObject, a, b string) any {
	if v, _ := r.get(a); !isNullish(v) {
		return v
	}
	v, _ := r.get(b)
	return v
}

// bestEffortToolName is the tool name from a raw event, or "<unknown>"; never panics.
func bestEffortToolName(event any) string {
	if name, ok := firstDefined(logToRecord(event), "name", "toolName").(string); ok && name != "" {
		return name
	}
	return "<unknown>"
}

// bestEffortCommand is the bash command from a raw event; never panics.
func bestEffortCommand(event any) (string, bool) {
	input := logToRecord(firstDefined(logToRecord(event), "input", "arguments"))
	v, _ := input.get("command")
	s, ok := v.(string)
	return s, ok
}

func errorMessage(thrown any) string {
	if e, ok := thrown.(error); ok {
		return e.Error()
	}
	return fmt.Sprint(thrown)
}

func formatGateErrorReason(thrown any) string {
	return "Permission gate failed and blocked the tool call (fail-closed): " + errorMessage(thrown)
}
