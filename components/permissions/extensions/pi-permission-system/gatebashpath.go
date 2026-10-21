package pi_permission_system

// Port of src/handlers/gates/bash-path.ts and bash-external-directory.ts: the two path gates of a parsed bash program.

func stringsAsJSON(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

// restrictivenessRank orders states deny over ask over allow.
func firstMostRestrictive(states []string) int {
	worst := 0
	for i, s := range states {
		if restrictiveness[s] > restrictiveness[states[worst]] {
			worst = i
		}
	}
	return worst
}

// DescribeBashPathGate builds a pure descriptor for the cross-cutting path permission gate of a bash program. Each path-rule candidate
// is evaluated on the narrowest `path`-family surface its proven effect names; the most restrictive result is described, with the
// raw token for prompts, logs and decisions. It returns nil when the gate does not apply (no program, no candidates, or every token
// allowed or unruled), a *GateBypass when every token is session-covered, else a descriptor.
func DescribeBashPathGate(tcc ToolCallContext, program *BashProgram, resolver ScopedPermissionResolver, normalizer *PathNormalizer) GateResult {
	if program == nil {
		return nil
	}
	command := program.CommandText()
	candidates := program.PathRuleCandidates()
	if len(candidates) == 0 {
		return nil
	}
	tokens := make([]string, len(candidates))
	for i, c := range candidates {
		tokens[i] = c.Token
	}

	type uncoveredToken struct {
		candidate BashPathRuleCandidate
		surface   string
		check     PermissionCheckResult
	}
	var uncovered []uncoveredToken
	allSessionCovered := true

	for _, c := range candidates {
		surface := CapabilitySurfaceForEffect("path", c.Effect.Effect)
		check := resolver.Resolve(AccessPathAccessIntent{Surface: surface, Path: c.Path, AgentName: tcc.AgentName})

		// No explicit path rule matched: only the universal default fired (#58).
		if check.MatchedPattern == nil && check.Source != "session" {
			allSessionCovered = false
			continue
		}
		if check.Source != "session" {
			allSessionCovered = false
		}
		if check.State == "deny" {
			uncovered = append(uncovered, uncoveredToken{c, surface, check})
			break
		}
		if check.State == "ask" {
			uncovered = append(uncovered, uncoveredToken{c, surface, check})
		}
	}

	if allSessionCovered {
		details := newObject()
		details.set("source", "tool_call")
		details.set("toolCallId", tcc.ToolCallID)
		details.set("toolName", tcc.ToolName)
		details.set("agentName", strOrNull(tcc.AgentName))
		details.set("command", command)
		details.set("tokens", stringsAsJSON(tokens))
		details.set("resolution", "session_approved")
		return &GateBypass{
			DecidedBy: DecisionSource{Kind: "session_approval", Surface: "path"},
			Log:       &GateBypassLog{Event: "permission_request.session_approved", Details: details},
		}
	}

	if len(uncovered) == 0 {
		return nil
	}
	states := make([]string, len(uncovered))
	for i, u := range uncovered {
		states[i] = u.check.State
	}
	worst := uncovered[firstMostRestrictive(states)]
	token, surface, check := worst.candidate.Token, worst.surface, worst.check

	patterns := normalizer.ApprovalPatternsFor(worst.candidate.Path)
	payload := BuildPathAskPayload(PathAskFacts{
		ToolName: tcc.ToolName, PathValue: token, AgentName: tcc.AgentName, MatchedPattern: check.MatchedPattern, Surface: surface,
	})
	toolCallID, toolName := tcc.ToolCallID, tcc.ToolName
	access := AccessFactsFromPath(surface, worst.candidate.Path)

	input := newObject()
	input.set("path", token)
	logContext := newObject()
	logContext.set("source", "tool_call")
	logContext.set("toolCallId", tcc.ToolCallID)
	logContext.set("toolName", tcc.ToolName)
	logContext.set("agentName", strOrNull(tcc.AgentName))
	logContext.set("command", command)
	logContext.set("path", token)
	logContext.set("effect", string(worst.candidate.Effect.Effect))
	logContext.set("effectSource", string(worst.candidate.Effect.Source))

	return &GateDescriptor{
		Surface:         surface,
		Input:           input,
		Payload:         payload,
		SessionApproval: SessionApprovalForPatterns(surface, patterns),
		PromptDetails: PromptPermissionDetails{
			Source: "tool_call", AgentName: tcc.AgentName, ToolCallID: &toolCallID, ToolName: &toolName, Command: &command, AccessIntent: &access,
		},
		LogContext: logContext,
		Decision:   GateDecision{Surface: surface, Value: token},
		PreCheck:   &check,
	}
}

// DescribeBashExternalDirectoryGate builds a pure descriptor for the bash external-directory gate. Each external path resolves on the
// narrowest `external_directory`-family surface its own effect names, and the session approval records one grant per uncovered path
// at that surface (#810). It returns nil when the gate does not apply or policy alone allowed every path, a *GateBypass when every
// path is allowed and a session grant covered at least one, else a descriptor for the uncovered paths.
func DescribeBashExternalDirectoryGate(tcc ToolCallContext, program *BashProgram, resolver ScopedPermissionResolver, normalizer *PathNormalizer) GateResult {
	if program == nil {
		return nil
	}
	command := program.CommandText()
	accesses := program.ExternalAccesses()
	if len(accesses) == 0 {
		return nil
	}

	coverage := SelectUncoveredExternalPaths(accesses, resolver, tcc.AgentName)
	if len(coverage.Uncovered) == 0 {
		if len(coverage.SessionCovered) == 0 {
			return nil
		}
		covered := make([]any, len(coverage.SessionCovered))
		for i, p := range coverage.SessionCovered {
			covered[i] = p.Value()
		}
		details := newObject()
		details.set("source", "tool_call")
		details.set("toolCallId", tcc.ToolCallID)
		details.set("toolName", tcc.ToolName)
		details.set("agentName", strOrNull(tcc.AgentName))
		details.set("command", command)
		details.set("externalPaths", covered)
		details.set("resolution", "session_approved")
		return &GateBypass{
			DecidedBy: DecisionSource{Kind: "session_approval", Surface: "external_directory"},
			Log:       &GateBypassLog{Event: "permission_request.session_approved", Details: details},
		}
	}

	states := make([]string, len(coverage.Uncovered))
	uncoveredPaths := make([]any, len(coverage.Uncovered))
	disclosures := make([]ExternalPathDisclosure, len(coverage.Uncovered))
	for i, u := range coverage.Uncovered {
		states[i] = u.Check.State
		uncoveredPaths[i] = u.Path.Value()
		disclosure := ExternalPathDisclosure{Path: u.Path.Value()}
		if alias, ok := u.Path.ResolvedAlias(); ok {
			disclosure.ResolvedPath = &alias
		}
		disclosures[i] = disclosure
	}
	worst := coverage.Uncovered[firstMostRestrictive(states)]
	preCheck, surface := worst.Check, worst.Surface

	payload := BuildBashExternalDirectoryAskPayload(BashExternalDirectoryAskFacts{
		Command: command, ExternalPaths: disclosures, Cwd: tcc.Cwd, AgentName: tcc.AgentName, ToolName: tcc.ToolName,
		MatchedPattern: preCheck.MatchedPattern, Surface: surface,
	})
	var grants []ApprovalGrant
	for _, u := range coverage.Uncovered {
		for _, pattern := range normalizer.ApprovalPatternsFor(u.Path) {
			grants = append(grants, ApprovalGrant{Surface: u.Surface, Pattern: pattern})
		}
	}
	toolCallID, toolName := tcc.ToolCallID, tcc.ToolName
	access := AccessFactsFromPath(surface, worst.Path)

	logContext := newObject()
	logContext.set("source", "tool_call")
	logContext.set("toolCallId", tcc.ToolCallID)
	logContext.set("toolName", tcc.ToolName)
	logContext.set("agentName", strOrNull(tcc.AgentName))
	logContext.set("command", command)
	logContext.set("externalPaths", uncoveredPaths)
	logContext.set("effect", string(worst.Effect.Effect))
	logContext.set("effectSource", string(worst.Effect.Source))

	return &GateDescriptor{
		Surface:         surface,
		Input:           newObject(),
		Payload:         payload,
		SessionApproval: SessionApprovalForGrants(grants),
		PromptDetails: PromptPermissionDetails{
			Source: "tool_call", AgentName: tcc.AgentName, ToolCallID: &toolCallID, ToolName: &toolName, Command: &command, AccessIntent: &access,
		},
		LogContext: logContext,
		Decision:   GateDecision{Surface: surface, Value: command},
		PreCheck:   &preCheck,
	}
}
