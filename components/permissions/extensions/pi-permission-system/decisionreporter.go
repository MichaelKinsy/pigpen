package pi_permission_system

// Port of src/logging/decision-reporter.ts.

// DecisionBroadcaster announces a terminal outcome on the permissions:decision channel; a collaborator that only announces (the
// serving session answering a forwarded request) depends on this rather than on the review-log half.
type DecisionBroadcaster interface {
	EmitDecision(event PermissionDecisionEvent)
}

// DecisionReporter reports a gate's outcome to the review log and the decision channel.
type DecisionReporter interface {
	DecisionBroadcaster
	WriteReviewLog(event string, details *jsObject)
}

// GateDecisionReporter owns the SessionLogger and the event bus.
type GateDecisionReporter struct {
	logger SessionLogger
	events PermissionEventBus
}

// NewGateDecisionReporter builds the reporter.
func NewGateDecisionReporter(logger SessionLogger, events PermissionEventBus) *GateDecisionReporter {
	return &GateDecisionReporter{logger: logger, events: events}
}

func (r *GateDecisionReporter) WriteReviewLog(event string, details *jsObject) {
	r.logger.Review(event, details)
}

func (r *GateDecisionReporter) EmitDecision(event PermissionDecisionEvent) {
	EmitDecisionEvent(r.events, event)
}

var _ DecisionReporter = (*GateDecisionReporter)(nil)
