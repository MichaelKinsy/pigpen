package pi_permission_system

// Port of src/handlers/gates/skill-input-gate-pipeline.ts: the skill-input gate assembly.

// GateRunning is the runner seam the pipelines and the handler depend on; *GateRunner satisfies it. The original passes the concrete
// GateRunner and a test spies on its `run`: the Go twin spies through this interface.
type GateRunning interface {
	Run(gate GateResult, agentName *string) (GateOutcome, error)
}

// SkillInputGateInputs is the narrow interface the pipeline needs from its session-side dependency: a raw check with no session
// rules, which preserves the skill-input semantics established in #326 (the skill-input gate intentionally bypasses session-rule
// resolution).
type SkillInputGateInputs interface {
	CheckPermission(surface string, input any, agentName *string) PermissionCheckResult
}

// GateNotifier is the narrow UI seam: warn the user when a skill is denied. The handler builds it per event from the context,
// encapsulating the hasUI guard.
type GateNotifier interface {
	Warn(message string)
}

// SkillInputGatePipeline owns the skill-input gate assembly: raw permission pre-check, deny notify, the descriptor and the runner.
type SkillInputGatePipeline struct{ inputs SkillInputGateInputs }

// NewSkillInputGatePipeline is `new SkillInputGatePipeline(inputs)`.
func NewSkillInputGatePipeline(inputs SkillInputGateInputs) *SkillInputGatePipeline {
	return &SkillInputGatePipeline{inputs: inputs}
}

// Evaluate checks the skill, warns on a deny and runs the descriptor.
func (p *SkillInputGatePipeline) Evaluate(skillName string, agentName *string, notifier GateNotifier, runner GateRunning) (GateOutcome, error) {
	check := p.inputs.CheckPermission("skill", skillInputObject(skillName), agentName)
	if check.State == "deny" {
		notifier.Warn(FormatSkillDenyNotice(skillName, agentName))
	}
	return runner.Run(DescribeSkillInputGate(skillName, agentName, check), agentName)
}

func skillInputObject(skillName string) *jsObject {
	o := newObject()
	o.set("name", skillName)
	return o
}

// FormatSkillDenyNotice is the deny warning shown in the UI when a skill is blocked. Intentionally untagged (no
// `[pi-permission-system]` prefix): a UI notify distinct from the agent-facing deny reasons the runner routes through
// RenderPolicyDenial.
func FormatSkillDenyNotice(skillName string, agentName *string) string {
	if agentName != nil && *agentName != "" {
		return "Skill '" + skillName + "' is not permitted for agent '" + *agentName + "'."
	}
	return "Skill '" + skillName + "' is not permitted by the current skill policy."
}

// ResolverSkillInputInputs adapts the resolver's raw check (no session rules) to SkillInputGateInputs: the original's resolver
// satisfies the interface structurally with an optional fourth argument, which Go spells out here.
type ResolverSkillInputInputs struct{ Resolver *PermissionResolver }

// CheckPermission is the resolver's raw check with no session rules.
func (r ResolverSkillInputInputs) CheckPermission(surface string, input any, agentName *string) PermissionCheckResult {
	return r.Resolver.CheckPermission(surface, input, agentName, nil)
}
