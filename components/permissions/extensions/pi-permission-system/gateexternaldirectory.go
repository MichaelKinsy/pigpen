package pi_permission_system

// Port of src/handlers/gates/external-directory.ts: the external-directory permission gate.

// DescribeExternalDirectoryGate builds a pure descriptor for the external-directory permission gate. It returns nil when the gate does
// not apply (the tool is not path-bearing, or the path is inside the working directory), a *GateBypass for a Pi infrastructure read
// (unless a deny rule naming the path outranks it) and a descriptor for an external path needing a permission check.
func DescribeExternalDirectoryGate(tcc ToolCallContext, infraScope InfrastructureReadScope, resolver ScopedPermissionResolver, normalizer *PathNormalizer, extractors ToolAccessExtractorLookup) GateResult {
	found := GetToolInputPath(tcc.ToolName, tcc.Input, extractors)
	if !found.HasPath || found.Path == "" {
		return nil
	}
	externalDirectoryPath := found.Path

	// The boundary decision and the infrastructure-read containment check use the canonical, symlink-resolved path; pattern matching
	// uses the typed and resolved aliases (#418). A built-in tool's path is the file Pi's resolver opens, so a `file://` spelling of
	// an outside file is judged outside.
	accessPath := normalizer.ForToolPath(tcc.ToolName, externalDirectoryPath)
	if !normalizer.IsBoundaryOutsideWorkingDirectory(accessPath.BoundaryValue()) {
		return nil
	}

	// The narrowest `external_directory`-family surface this tool's identity proves; the bare family name folds both directions.
	surface := CapabilitySurfaceForTool("external_directory", tcc.ToolName)

	// Resolved before the infrastructure bypass so a rule can be weighed against it; the runner consumes this preCheck.
	preCheck := ResolveExternalDirectoryPolicy(accessPath, resolver, surface, tcc.AgentName)

	// Pi infrastructure read bypass.
	if normalizer.IsInfrastructureRead(tcc.ToolName, accessPath, infraScope) && !isTargetedDeny(preCheck) {
		return &GateBypass{
			// Containment allowed this, not a rule the operator wrote.
			DecidedBy: DecisionSource{Kind: "infrastructure_read"},
			Log: &GateBypassLog{
				Event:   "permission_request.infrastructure_auto_allowed",
				Details: BuildPathGateLogContext(tcc.RequestFacts(), externalDirectoryPath, found.Source),
			},
			Decision: &DecisionEventFacts{
				Surface: tcc.ToolName, Value: externalDirectoryPath, Result: "allow", Resolution: "infrastructure_auto_allowed",
				AgentName: tcc.AgentName,
			},
		}
	}

	// Build the descriptor for the permission check.
	var resolvedAlias *string
	if alias, ok := accessPath.ResolvedAlias(); ok {
		resolvedAlias = &alias
	}
	patterns := normalizer.ApprovalPatternsFor(accessPath)

	payload := BuildExternalDirectoryAskPayload(ExternalDirectoryAskFacts{
		PathAskFacts: PathAskFacts{
			ToolName: tcc.ToolName, PathValue: externalDirectoryPath, ResolvedPath: resolvedAlias, AgentName: tcc.AgentName,
			MatchedPattern: preCheck.MatchedPattern, Surface: surface,
		},
		Cwd: tcc.Cwd,
	})

	return &GateDescriptor{
		Surface:         surface,
		Input:           newObject(),
		PreCheck:        &preCheck,
		Payload:         payload,
		SessionApproval: SessionApprovalForPatterns(surface, patterns),
		PromptDetails:   BuildPathGatePromptDetails(tcc.RequestFacts(), externalDirectoryPath, AccessFactsFromPath(surface, accessPath)),
		LogContext:      BuildPathGateLogContext(tcc.RequestFacts(), externalDirectoryPath, found.Source),
		Decision:        GateDecision{Surface: surface, Value: externalDirectoryPath},
	}
}

// isTargetedDeny is true for a deny from a rule naming this path: not the family's catch-all (`*`, or `**`, which compiles
// identically), and not the universal fallback (no matched pattern). Only such a deny outranks the Pi infrastructure read bypass, so
// a deny-by-default policy keeps its skill and package reads.
func isTargetedDeny(check PermissionCheckResult) bool {
	return check.State == "deny" && check.MatchedPattern != nil && !isCatchAllPattern(*check.MatchedPattern)
}

// isCatchAllPattern is /^\*+$/: a pattern of wildcards alone, which matches every path.
func isCatchAllPattern(pattern string) bool {
	if pattern == "" {
		return false
	}
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '*' {
			return false
		}
	}
	return true
}
