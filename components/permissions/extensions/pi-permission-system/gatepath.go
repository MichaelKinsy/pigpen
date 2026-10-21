package pi_permission_system

// Port of src/handlers/gates/path.ts: the cross-cutting path permission gate (tools).

// DescribePathGate builds a pure descriptor for the path permission gate. It returns nil when the gate does not apply (the tool is not
// path-bearing, no extractable path, the `path` surface evaluates to allow, or no explicit `path` rule matched: only the universal
// default fired) and a descriptor when the path matches a deny or ask rule.
func DescribePathGate(tcc ToolCallContext, resolver ScopedPermissionResolver, normalizer *PathNormalizer, extractors ToolAccessExtractorLookup) GateResult {
	found := GetToolInputPath(tcc.ToolName, tcc.Input, extractors)
	if !found.HasPath || found.Path == "" {
		return nil
	}
	filePath := found.Path

	// The narrowest `path`-family surface this tool's identity proves; the bare family name folds both directions (ADR 0013 section 10).
	surface := CapabilitySurfaceForTool("path", tcc.ToolName)

	// Emit an access-path intent so the resolver matches the lexical aliases and the canonical (symlink-resolved) form, the same set
	// `external_directory` matches (#418, #486). A built-in tool's path is the file Pi's resolver opens, not the spelling typed.
	accessPath := normalizer.ForToolPath(tcc.ToolName, filePath)
	check := resolver.Resolve(AccessPathAccessIntent{Surface: surface, Path: accessPath, AgentName: tcc.AgentName})

	if check.State == "allow" {
		return nil
	}
	// No explicit path rule matched: only the universal default fired. Skip the gate so configs without a "path" key do not
	// trigger path-level prompts (#58).
	if check.MatchedPattern == nil {
		return nil
	}

	// Derive the approval patterns from the lexical absolute form so they match the policy values a later call produces.
	patterns := normalizer.ApprovalPatternsFor(accessPath)

	var resolvedPath *string
	if alias, ok := accessPath.ResolvedAlias(); ok {
		resolvedPath = &alias
	}
	payload := BuildPathAskPayload(PathAskFacts{
		ToolName: tcc.ToolName, PathValue: filePath, ResolvedPath: resolvedPath, AgentName: tcc.AgentName,
		MatchedPattern: check.MatchedPattern, Surface: surface,
	})

	input := newObject()
	input.set("path", filePath)
	return &GateDescriptor{
		Surface:         surface,
		Input:           input,
		Payload:         payload,
		SessionApproval: SessionApprovalForPatterns(surface, patterns),
		PromptDetails:   BuildPathGatePromptDetails(tcc.RequestFacts(), filePath, AccessFactsFromPath(surface, accessPath)),
		LogContext:      BuildPathGateLogContext(tcc.RequestFacts(), filePath, found.Source),
		Decision:        GateDecision{Surface: surface, Value: filePath},
		PreCheck:        &check,
	}
}
