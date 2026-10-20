package pi_permission_system

// Port of src/handlers/gates/tool-call-gate-pipeline.ts: the ordered tool-call gate-producer assembly and the run loop.

// ToolCallGateInputs is the narrow interface the pipeline needs from its session-side dependency: the queries that assemble gate
// inputs. The resolver is injected separately. *PermissionSession satisfies it.
type ToolCallGateInputs interface {
	// GetActiveSkillEntries are the active skill prompt entries for the skill-read gate.
	GetActiveSkillEntries() []SkillPromptEntry
	// GetInfrastructureReadScope is where infrastructure reads are auto-allowed (static plus config-derived).
	GetInfrastructureReadScope() InfrastructureReadScope
	// GetToolPreviewLimits are the resolved tool-preview formatter options.
	GetToolPreviewLimits() ToolPreviewFormatterOptions
	// GetPathNormalizer is the session's path normalizer (platform and cwd baked in).
	GetPathNormalizer() *PathNormalizer
	// GetShellToolAliases are the configured shell-tool aliases (`shellTools`), nil when none are set. An aliased shell tool is gated
	// through the bash stack at parity with native `bash` (#574).
	GetShellToolAliases() ShellToolsConfig
}

// ToolCallGatePipeline owns the ordered tool-call gate-producer assembly and the run loop: bash-command extraction and a single
// BashProgram parse (#308), the ToolPreviewFormatter, the infrastructure-read scope, all six gate producers in their prescribed order,
// and the run loop, which runs an unconditionally denying gate ahead of the rest and returns the first block outcome, or allow.
type ToolCallGatePipeline struct {
	resolver         ScopedPermissionResolver
	inputs           ToolCallGateInputs
	customFormatters ToolInputFormatterLookup
	customExtractors ToolAccessExtractorLookup

	// The seams a twin replaces as the original's tests mock the modules: the BashProgram parse and the bash gate producers.
	parseBash                 func(command string, normalizer *PathNormalizer, options *BashProgramOptions) (*BashProgram, error)
	bashExternalDirectoryGate func(tcc ToolCallContext, program *BashProgram, resolver ScopedPermissionResolver, normalizer *PathNormalizer) GateResult
	bashPathGate              func(tcc ToolCallContext, program *BashProgram, resolver ScopedPermissionResolver, normalizer *PathNormalizer) GateResult
	bashCommandCheck          func(command string, commands []BashCommand, agentName *string, resolver ScopedPermissionResolver) PermissionCheckResult
}

// NewToolCallGatePipeline is the constructor; customFormatters and customExtractors may be nil.
func NewToolCallGatePipeline(resolver ScopedPermissionResolver, inputs ToolCallGateInputs, customFormatters ToolInputFormatterLookup, customExtractors ToolAccessExtractorLookup) *ToolCallGatePipeline {
	return &ToolCallGatePipeline{
		resolver: resolver, inputs: inputs, customFormatters: customFormatters, customExtractors: customExtractors,
		parseBash:                 ParseBashProgram,
		bashExternalDirectoryGate: DescribeBashExternalDirectoryGate,
		bashPathGate:              DescribeBashPathGate,
		bashCommandCheck:          ResolveBashCommandCheck,
	}
}

// Evaluate produces every gate, then runs them. A panic in a producer or the runner, or an error from the parse or an escalation,
// propagates: the tool-call boundary blocks on either, so a failing gate is never an allow.
func (p *ToolCallGatePipeline) Evaluate(tcc ToolCallContext, runner GateRunning) (GateOutcome, error) {
	// Resolve the shell invocation once: native `bash` and any tool recorded in `shellTools` both yield a command (and an optional
	// workdir); every other tool yields nil (#574). The three bash gates then share the single BashProgram parsed from that command
	// instead of each re-parsing (#308).
	shell := ResolveShellInvocation(tcc.ToolName, tcc.Input, p.inputs.GetShellToolAliases())
	normalizer := p.inputs.GetPathNormalizer()
	var bashProgram *BashProgram
	if shell != nil && shell.Command != "" {
		program, err := p.parseBash(shell.Command, normalizer, &BashProgramOptions{Workdir: shell.Workdir})
		if err != nil {
			return GateOutcome{}, err
		}
		bashProgram = program
	}

	formatter := NewToolPreviewFormatter(p.inputs.GetToolPreviewLimits(), p.customFormatters)
	infraScope := p.inputs.GetInfrastructureReadScope()

	producers := []func() GateResult{
		func() GateResult {
			if d := DescribeSkillReadGate(tcc, normalizer, p.inputs.GetActiveSkillEntries); d != nil {
				return d
			}
			return nil
		},
		func() GateResult { return DescribePathGate(tcc, p.resolver, normalizer, p.customExtractors) },
		func() GateResult {
			return DescribeExternalDirectoryGate(tcc, infraScope, p.resolver, normalizer, p.customExtractors)
		},
		func() GateResult { return p.bashExternalDirectoryGate(tcc, bashProgram, p.resolver, normalizer) },
		func() GateResult { return p.bashPathGate(tcc, bashProgram, p.resolver, normalizer) },
		func() GateResult {
			toolCheck, pathAccess := p.resolvePerToolCheck(tcc, shell, bashProgram, normalizer)
			toolDescriptor := DescribeToolGate(tcc, toolCheck, formatter, pathAccess, shell)
			toolDescriptor.PreCheck = &toolCheck
			return toolDescriptor
		},
	}

	// Produce every gate before running any of them, so an unconditional deny on a later gate is known before an earlier one
	// suspends the call on an `ask` nobody's answer could change (#899). Producing is side-effect-free: all logging and event
	// emission happens inside the runner.
	gates := make([]GateResult, 0, len(producers))
	for _, produce := range producers {
		gates = append(gates, produce())
	}

	for _, gate := range OrderDenyFirst(gates) {
		outcome, err := runner.Run(gate, tcc.AgentName)
		if err != nil {
			return GateOutcome{}, err
		}
		if outcome.Action == "block" {
			return outcome, nil
		}
	}
	return GateOutcome{Action: "allow"}, nil
}

// resolvePerToolCheck resolves the per-tool gate's check, choosing the intent by tool shape: bash chains its sub-commands; a
// path-bearing tool with a path emits an `access-path` intent (so the per-tool surface matches lexical and canonical, #502); every
// other tool (and a path-bearing tool with no path) keeps the raw `tool` intent the manager normalizes. It returns the resolved path
// alongside the check, already paired with the session scope approving it grants (#655).
func (p *ToolCallGatePipeline) resolvePerToolCheck(tcc ToolCallContext, shell *ShellInvocation, bashProgram *BashProgram, normalizer *PathNormalizer) (PermissionCheckResult, *ToolPathAccess) {
	if shell != nil {
		if bashProgram != nil {
			return p.bashCommandCheck(bashProgram.CommandText(), bashProgram.Commands(), tcc.AgentName, p.resolver), nil
		}
		// A shell invocation whose command did not parse (for example empty) still resolves on the `bash` surface, so an aliased
		// tool never falls through to its own extension-tool surface.
		input := newObject()
		input.set("command", shell.Command)
		return p.resolver.Resolve(ToolAccessIntent{Surface: "bash", Input: input, AgentName: tcc.AgentName}), nil
	}

	if filePath, ok := GetPathBearingToolPath(tcc.ToolName, tcc.Input); ok {
		accessPath := normalizer.ForToolPath(tcc.ToolName, filePath)
		access := &ToolPathAccess{Path: accessPath, ApprovalPatterns: normalizer.ApprovalPatternsFor(accessPath)}
		return p.resolver.Resolve(AccessPathAccessIntent{Surface: tcc.ToolName, Path: accessPath, AgentName: tcc.AgentName}), access
	}

	return p.resolver.Resolve(ToolAccessIntent{Surface: tcc.ToolName, Input: tcc.Input, AgentName: tcc.AgentName}), nil
}
