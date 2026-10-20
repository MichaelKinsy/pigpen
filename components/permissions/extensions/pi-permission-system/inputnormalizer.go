package pi_permission_system

import "strings"

// Port of src/access-intent/input-normalizer.ts.

// stripBashCommentLines removes leading and interior shell comment lines (src/access-intent/bash/bash-arity.ts): lines whose first
// non-space character is `#` are dropped and the rest trimmed.
func stripBashCommentLines(command string) string {
	var meaningful []string
	for _, line := range strings.Split(command, "\n") {
		if !strings.HasPrefix(strings.TrimLeftFunc(line, isJSSpace), "#") {
			meaningful = append(meaningful, line)
		}
	}
	return jsTrim(strings.Join(meaningful, "\n"))
}

// StripBashCommentLines is stripBashCommentLines for the layers above.
func StripBashCommentLines(command string) string { return stripBashCommentLines(command) }

// BuildAccessIntentForSurface builds the intent an external policy query feeds to the resolver from a (surface, value) pair: an
// access-path intent for a path-shaped surface with a value, path-values for an mcp query with a value, else a tool intent.
// value nil is undefined; agentName nil is undefined.
func BuildAccessIntentForSurface(surface string, value *string, normalizer *PathNormalizer, agentName *string) QueryIntent {
	var raw any
	if value != nil {
		raw = *value
	}
	pathValue, hasPath := GetNonEmptyString(raw)
	if hasPath && PathSurfaces[surface] {
		return AccessPathAccessIntent{Surface: surface, Path: normalizer.ForToolPath(surface, pathValue), AgentName: agentName}
	}
	if hasPath && surface == "mcp" {
		return PathValuesAccessIntent{Surface: surface, Values: []string{pathValue}, AgentName: agentName}
	}
	return ToolAccessIntent{Surface: surface, Input: buildInputForSurface(surface, value), AgentName: agentName}
}

// BuildResolvedIntentFromMatchValues builds a ResolvedAccessIntent directly from a forwarded request's child-fixed match values:
// a path-shaped surface or mcp gets a path-values intent carrying them as-is, every other surface a tool intent from the first
// value (nil when there is none).
func BuildResolvedIntentFromMatchValues(surface string, matchValues []string, agentName string) ResolvedAccessIntent {
	name := agentName
	if PathSurfaces[surface] || surface == "mcp" {
		return PathValuesAccessIntent{Surface: surface, Values: append([]string{}, matchValues...), AgentName: &name}
	}
	var first *string
	if len(matchValues) > 0 {
		first = &matchValues[0]
	}
	return ToolAccessIntent{Surface: surface, Input: buildInputForSurface(surface, first), AgentName: &name}
}

// buildInputForSurface is the inverse of NormalizeInput: the minimal input object the manager expects for a surface.
func buildInputForSurface(surface string, value *string) *jsObject {
	v := ""
	if value != nil {
		v = *value
	}
	o := newObject()
	switch {
	case surface == "bash":
		o.set("command", v)
	case surface == "skill":
		o.set("name", v)
	case SurfaceFamilyOf(surface) == "external_directory":
		o.set("path", v)
	}
	return o
}

// NormalizedInput is the surface-normalized representation of a tool invocation fed to a single EvaluateAnyValue call.
type NormalizedInput struct {
	// Surface is the permission surface for Evaluate (bash, mcp, skill, ...).
	Surface string
	// Values are the alternative lookup names for this one access, most specific first.
	Values []string
	// ResultExtras are surface-specific fields forwarded into PermissionCheckResult ({ command } for bash, { target } for mcp).
	ResultExtras *jsObject
}

// NormalizeInput maps a raw tool invocation to the surface/values/extras triple CheckPermission needs.
func NormalizeInput(toolName string, input any, configuredMcpServerNames []string) NormalizedInput {
	switch ClassifyToolKind(toolName) {
	case ToolKindSkill:
		lookup := "*"
		if v, _ := ToRecord(input).get("name"); v != nil {
			if s, ok := v.(string); ok {
				lookup = s
			}
		}
		return NormalizedInput{Surface: "skill", Values: []string{lookup}, ResultExtras: newObject()}
	case ToolKindBash:
		command := ""
		if v, _ := ToRecord(input).get("command"); v != nil {
			if s, ok := v.(string); ok {
				command = s
			}
		}
		return NormalizeBashCommand(command, nil)
	case ToolKindMcp:
		return normalizeMcpTargets(CreateMcpPermissionTargets(input, configuredMcpServerNames))
	case ToolKindMcpTool:
		return normalizeMcpTargets(CreatePiMcpToolTargets(toolName, configuredMcpServerNames))
	}
	return NormalizedInput{Surface: toolName, Values: []string{"*"}, ResultExtras: newObject()}
}

func normalizeMcpTargets(targets []string) NormalizedInput {
	values := append(append([]string{}, targets...), "mcp")
	extras := newObject()
	extras.set("target", values[0])
	return NormalizedInput{Surface: "mcp", Values: values, ResultExtras: extras}
}

// NormalizeBashCommand is the bash surface's normalized form: the command as typed (leading comment lines stripped for matching;
// an all-comment command evaluates against its literal text), then the other spellings, matched as aliases of one invocation.
func NormalizeBashCommand(command string, spellings []string) NormalizedInput {
	matchValue := stripBashCommentLines(command)
	if matchValue == "" {
		matchValue = command
	}
	extras := newObject()
	extras.set("command", command)
	return NormalizedInput{Surface: "bash", Values: uniqueStrings(append([]string{matchValue}, spellings...)), ResultExtras: extras}
}
