package pi_permission_system

// Port of src/presentation/path-ask-payload.ts: the payloads of the path-shaped asks.

// ExternalPathDisclosure is a displayed external path paired with its resolved target, when distinct.
type ExternalPathDisclosure struct {
	// Path is the path as displayed (typed for tools, lexical-absolute for bash).
	Path string
	// ResolvedPath is the canonical symlink-resolved target; set only when it differs.
	ResolvedPath *string
}

// PathAskFacts are the facts a path-shaped gate holds when it raises an ask.
type PathAskFacts struct {
	ToolName string
	// PathValue is the path as the caller typed it: what the user recognizes.
	PathValue string
	// ResolvedPath is where the typed path lands when that is somewhere else: its symlink target, or the file a built-in tool's
	// resolver opens for a rewritten spelling.
	ResolvedPath   *string
	AgentName      *string
	MatchedPattern *string
	// Surface is the surface that decided: a directional member when the tool's identity proved a direction, the bare family
	// otherwise. Distinct from the payload kind, which stays coarse so renderer dispatch is untouched.
	Surface string
}

// BuildPathAskPayload builds a tool ask gated by an explicit path-family rule.
func BuildPathAskPayload(facts PathAskFacts) PromptPayload {
	return pathPayload("path", facts, resolvedAliasEvidence(facts.ResolvedPath))
}

// ExternalDirectoryAskFacts are the facts the external-directory gate adds: the boundary.
type ExternalDirectoryAskFacts struct {
	PathAskFacts
	// Cwd is the working directory the path escapes.
	Cwd string
}

// BuildExternalDirectoryAskPayload builds a tool ask for a path outside the working directory.
func BuildExternalDirectoryAskPayload(facts ExternalDirectoryAskFacts) PromptPayload {
	evidence := append(resolvedAliasEvidence(facts.ResolvedPath), workingDirectoryEvidence(facts.Cwd))
	return pathPayload("external_directory", facts.PathAskFacts, evidence)
}

// BashExternalDirectoryAskFacts are the facts the bash external-directory gate holds: one command, many paths.
type BashExternalDirectoryAskFacts struct {
	Command string
	// ExternalPaths is every uncovered path the command references, with its canonical alias.
	ExternalPaths  []ExternalPathDisclosure
	Cwd            string
	AgentName      *string
	ToolName       string
	MatchedPattern *string
	// Surface is the surface that decided: a directional member when the deciding path's effect was proven, the bare family
	// otherwise.
	Surface string
}

// BuildBashExternalDirectoryAskPayload builds a bash ask whose command references paths outside the working directory.
func BuildBashExternalDirectoryAskPayload(facts BashExternalDirectoryAskFacts) PromptPayload {
	toolName := facts.ToolName
	evidence := []PromptEvidence{workingDirectoryEvidence(facts.Cwd)}
	for _, external := range facts.ExternalPaths {
		evidence = append(evidence, externalPathEvidence(external))
	}
	return PromptPayload{
		Kind: "bash_external_directory",
		Request: PromptRequestFacts{
			Requester:      LocalRequester(facts.AgentName),
			Surface:        facts.Surface,
			ToolName:       &toolName,
			Value:          facts.Command,
			MatchedPattern: facts.MatchedPattern,
		},
		Evidence:    evidence,
		Annotations: []PromptAnnotation{},
	}
}

// pathPayload is the payload common to the single-path asks: the typed path is the decision-relevant value, and the gate surface
// distinguishes them.
func pathPayload(kind string, facts PathAskFacts, evidence []PromptEvidence) PromptPayload {
	toolName := facts.ToolName
	return PromptPayload{
		Kind: kind,
		Request: PromptRequestFacts{
			Requester:      LocalRequester(facts.AgentName),
			Surface:        facts.Surface,
			ToolName:       &toolName,
			Value:          facts.PathValue,
			MatchedPattern: facts.MatchedPattern,
		},
		Evidence:    evidence,
		Annotations: []PromptAnnotation{},
	}
}

// resolvedAliasEvidence is the canonical location, as its own entry rather than folded into the value: the user decides on the
// path they typed, and the alias is what that path turns out to name.
func resolvedAliasEvidence(resolvedPath *string) []PromptEvidence {
	if resolvedPath == nil {
		return []PromptEvidence{}
	}
	return []PromptEvidence{{Label: "resolves to", Text: *resolvedPath}}
}

func workingDirectoryEvidence(cwd string) PromptEvidence {
	return PromptEvidence{Label: "working directory", Text: cwd}
}

// externalPathEvidence is one escaping path. The canonical alias rides as the entry's detail so a render cannot separate a path
// from what it resolves to.
func externalPathEvidence(external ExternalPathDisclosure) PromptEvidence {
	return PromptEvidence{Label: "external path", Text: external.Path, Detail: external.ResolvedPath}
}
