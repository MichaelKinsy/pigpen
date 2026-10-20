package pi_permission_system

// Port of src/authority/subagent-detection.ts.

// SubagentDetector is the narrow seam for the ask-path consumers: "is the current session a subagent?". It answers "is this process
// a child", which is not "should this node relay rather than decide": a UI host answers true whenever its process carries a
// parent-session marker, and no consumer may read it as a relay decision (#907, #909). The error is a failing session-directory read.
type SubagentDetector interface {
	IsSubagent(ctx SubagentDetectionContext) (bool, error)
}

// SubagentDetectionDeps are the composition-root inputs for SubagentDetection.
type SubagentDetectionDeps struct {
	SubagentSessionsDir string
	Flavor              SubagentPathFlavor
	Registry            *SubagentSessionRegistry
}

// SubagentDetection is the single owner of subagent detection, constructed once with the detection inputs and shared across every
// consumer. It delegates to IsSubagentExecutionContext, holding only the deps.
type SubagentDetection struct{ deps SubagentDetectionDeps }

// NewSubagentDetection is `new SubagentDetection(deps)`.
func NewSubagentDetection(deps SubagentDetectionDeps) *SubagentDetection {
	return &SubagentDetection{deps: deps}
}

// IsSubagent implements SubagentDetector.
func (d *SubagentDetection) IsSubagent(ctx SubagentDetectionContext) (bool, error) {
	return IsSubagentExecutionContext(ctx, d.deps.SubagentSessionsDir, d.deps.Flavor, d.deps.Registry)
}
