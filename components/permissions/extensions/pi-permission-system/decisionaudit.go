package pi_permission_system

import (
	"fmt"
	"sync"
)

// Port of src/logging/decision-audit.ts: the per-call terminal decision, so an evaluated-and-allowed call is distinguishable from
// a never-evaluated one.

// DecisionRecorder is what the fail-closed boundary calls: exactly one of RecordDecision / RecordError per call.
type DecisionRecorder interface {
	// RecordDecision records a terminal "allow" or "block" decision (also bumps the tool-call count).
	RecordDecision(action string)
	// RecordError records a gate error that blocked fail-closed (also bumps the count).
	RecordError()
}

// AuditLogger is the narrow logging surface the summary needs: a debug line and a warning.
type AuditLogger interface {
	Debug(event string, details *jsObject)
	Warn(message string)
}

// DecisionSummaryWriter is the narrow surface the session-shutdown handler depends on.
type DecisionSummaryWriter interface {
	WriteSummary(logger AuditLogger)
}

// DecisionAudit holds the per-session decision counters. The boundary produces exactly one terminal decision per tool call, so
// toolCalls must equal allowed + blocked + errors.
type DecisionAudit struct {
	mu                                  sync.Mutex
	toolCalls, allowed, blocked, errors int
}

func (a *DecisionAudit) RecordDecision(action string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolCalls++
	if action == "allow" {
		a.allowed++
	} else {
		a.blocked++
	}
}

func (a *DecisionAudit) RecordError() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolCalls++
	a.errors++
}

// WriteSummary emits one permission.session_summary debug line with the counters. When toolCalls != allowed + blocked + errors it
// also warns: a tool call resolved without a recorded terminal decision (a re-opened silent path).
func (a *DecisionAudit) WriteSummary(logger AuditLogger) {
	a.mu.Lock()
	toolCalls, allowed, blocked, errs := a.toolCalls, a.allowed, a.blocked, a.errors
	a.mu.Unlock()
	counts := newObject()
	counts.set("toolCalls", float64(toolCalls))
	counts.set("allowed", float64(allowed))
	counts.set("blocked", float64(blocked))
	counts.set("errors", float64(errs))
	logger.Debug("permission.session_summary", counts)
	if toolCalls != allowed+blocked+errs {
		logger.Warn(fmt.Sprintf("[pi-permission-system] decision audit invariant violated: %d tool calls != %d allowed + %d blocked + %d errors. ", toolCalls, allowed, blocked, errs) +
			"A tool call resolved without a recorded terminal decision.")
	}
}

var _ DecisionRecorder = (*DecisionAudit)(nil)
var _ DecisionSummaryWriter = (*DecisionAudit)(nil)
