package pi_permission_system

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"
)

// Port of src/presentation/pattern-suggest.ts: the session-approval patterns and the labels of the "for this session" options.

// SessionApprovalSuggestion is the suggestion returned for a "Yes, for this session" dialog option.
type SessionApprovalSuggestion struct {
	// Surface is the permission surface this approval applies to.
	Surface string
	// Pattern is the wildcard pattern to store as a session rule.
	Pattern string
	// Label is the human-readable label for the "for session" dialog option.
	Label string
}

// jsSplitWhitespace is s.split(/\s+/) for a string with no leading or trailing whitespace: JavaScript's \s is wider than Go's.
func jsSplitWhitespace(s string) []string {
	var tokens []string
	start := -1
	for i, r := range s {
		if isJSSpace(r) {
			if start >= 0 {
				tokens = append(tokens, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		tokens = append(tokens, s[start:])
	}
	return tokens
}

// SuggestBashPattern suggests a bash session-approval pattern from a command string. It uses the arity table to find the
// semantically meaningful prefix tokens, then produces a wildcard pattern:
//   - a single bare token (no args): the exact command (`ls`);
//   - an arity prefix that covers all tokens: a trailing wildcard (`npm run build*`);
//   - an arity prefix shorter than the token list: a space and a wildcard (`git checkout *`);
//   - an unknown command: the first token and a space wildcard (`mytool *`).
func SuggestBashPattern(command string) string {
	trimmed := jsTrim(command)
	if trimmed == "" {
		return ""
	}
	// Strip leading shell comment lines so the suggestion is based on the actual command.
	stripped := bashanalysis.StripBashCommentLines(trimmed)
	if stripped == "" {
		return ""
	}
	tokens := jsSplitWhitespace(stripped)
	if len(tokens) == 1 {
		return stripped
	}
	meaningful := bashanalysis.Prefix(tokens)
	if len(meaningful) >= len(tokens) {
		return stripped + "*"
	}
	return strings.Join(meaningful, " ") + " *"
}

// SuggestMcpPattern suggests an MCP session-approval pattern from a resolved target string: `server:tool` gives `server:*`,
// `server_tool` gives `server_*`, a bare target gives `*`.
func SuggestMcpPattern(target string) string {
	head, tail := mcpPatternParts(target)
	return head + tail
}

// mcpPatternParts splits SuggestMcpPattern's answer into the trimmed target's prefix and the wildcard suffix (":*", "_*" or the whole
// answer "*"), so a caller that also builds a label around the pattern can concatenate once and slice the pattern out of the label.
func mcpPatternParts(target string) (head, tail string) {
	trimmed := jsTrim(target)
	if i := strings.Index(trimmed, ":"); i > 0 {
		return trimmed[:i], ":*"
	}
	if i := strings.Index(trimmed, "_"); i > 0 {
		return trimmed[:i], "_*"
	}
	return "", "*"
}

// ForwardedScopeLabels are the scope labels for the forwarded-approval two-step scope select.
type ForwardedScopeLabels struct {
	// SubagentLabel is the least-privilege default: record on the requesting subagent only.
	SubagentLabel string
	// ServingSessionLabel records on the serving node: it covers the parent and all subagents.
	ServingSessionLabel string
}

// DescribeGrantTarget is what an approval's grants cover, as one phrase: a single target names its pattern, several name their
// count. It needs at least one grant.
func DescribeGrantTarget(grants []ApprovalGrant) string {
	targets := GrantTargets(grants)
	if len(targets) == 1 {
		return `"` + targets[0] + `"`
	}
	return jsNumber(float64(len(targets))) + " paths"
}

// DirectionalSessionLabels are the two session-option labels for an ask whose grants prove one direction.
type DirectionalSessionLabels struct {
	// SessionLabel is the proven-direction grant, the least-privilege default.
	SessionLabel string
	// WidenedLabel is the both-directions grant, offered beside it.
	WidenedLabel string
}

var directionNouns = map[CapabilityDirection]string{"read": "reads", "write": "writes"}

// BuildDirectionalSessionLabels labels the two widths a directional ask's session grant can take. Both rows name the direction and
// the target.
func BuildDirectionalSessionLabels(direction CapabilityDirection, target string) DirectionalSessionLabels {
	return DirectionalSessionLabels{
		SessionLabel: "Yes, allow " + directionNouns[direction] + " to " + target + " for this session",
		WidenedLabel: "Yes, allow " + directionNouns["read"] + " and " + directionNouns["write"] + " to " + target + " for this session",
	}
}

// BuildPathAccessSessionLabel labels a session grant on the path / external_directory families that proves no direction, so the
// option still names its scope. It is nil when any grant lies outside those families (their gates supply their own label).
func BuildPathAccessSessionLabel(grants []ApprovalGrant) *string {
	if len(grants) == 0 {
		return nil
	}
	for _, grant := range grants {
		if _, ok := SurfaceFamilyMembers(SurfaceFamilyOf(grant.Surface)); !ok {
			return nil
		}
	}
	label := "Yes, allow access to " + DescribeGrantTarget(grants) + " for this session"
	return &label
}

// BuildForwardedScopeLabels builds the two scope labels shown when a human grants a forwarded request "for this session". The
// subagent option names the requester; the whole-session option restates every grant. The surface is named as the grants' shared
// family, never a directional member; grants that share no family name no surface at all.
func BuildForwardedScopeLabels(agentName *string, grants []ApprovalGrant) ForwardedScopeLabels {
	subagentLabel := "This subagent only"
	if agentName != nil && *agentName != "" {
		subagentLabel = "This subagent ('" + *agentName + "') only"
	}
	granted := DescribeGrantTarget(grants)
	if family, ok := sharedSurfaceFamilyOf(grants); ok {
		granted = family + " " + granted
	}
	return ForwardedScopeLabels{
		SubagentLabel:       subagentLabel,
		ServingSessionLabel: "The whole session \u2014 allow " + granted + " for parent and all subagents",
	}
}

// sharedSurfaceFamilyOf is the family every grant belongs to, or ok false when they disagree (or there are none). A family is
// the empty string only for the empty surface, which the original's null check would treat as no family.
func sharedSurfaceFamilyOf(grants []ApprovalGrant) (string, bool) {
	if len(grants) == 0 {
		return "", false
	}
	family := SurfaceFamilyOf(grants[0].Surface)
	for _, grant := range grants {
		if SurfaceFamilyOf(grant.Surface) != family {
			return "", false
		}
	}
	return family, family != ""
}

// buildLabel is the surface-aware human-readable label for the session-approval option.
func buildLabel(pattern, surface string) string {
	switch surface {
	case "bash":
		return `Yes, allow bash "` + pattern + `" for this session`
	case "mcp":
		return `Yes, allow mcp tool "` + pattern + `" for this session`
	case "skill":
		return `Yes, allow skill "` + pattern + `" for this session`
	case "external_directory":
		return `Yes, allow access to external directory "` + pattern + `" for this session`
	case "path":
		return `Yes, allow path "` + pattern + `" for this session`
	}
	// Path-bearing tools with a specific path pattern show the pattern.
	if PathBearingTools[surface] && pattern != "*" {
		return "Yes, allow " + surface + ` "` + pattern + `" for this session`
	}
	// Tool surfaces with catch-all or extension tools.
	return `Yes, allow tool "` + surface + `" for this session`
}

// SuggestSessionPattern suggests a session-approval pattern from a surface's own value vocabulary: a bash command, an MCP target, a
// skill name. Any surface with no vocabulary of its own falls back to the catch-all wildcard. A path surface goes through
// SuggestPathSessionPattern instead.
func SuggestSessionPattern(surface, value string) SessionApprovalSuggestion {
	var pattern string
	switch surface {
	case "bash":
		pattern = SuggestBashPattern(value)
	case "mcp":
		// One allocation instead of two: the label is built around the pattern, so the pattern is a slice of it.
		head, tail := mcpPatternParts(value)
		const before = `Yes, allow mcp tool "`
		label := before + head + tail + `" for this session`
		return SessionApprovalSuggestion{Surface: surface, Pattern: label[len(before) : len(before)+len(head)+len(tail)], Label: label}
	case "skill":
		pattern = value
	default:
		// Extension tools, and path-bearing tools invoked without a path.
		pattern = "*"
	}
	return SessionApprovalSuggestion{Surface: surface, Pattern: pattern, Label: buildLabel(pattern, surface)}
}

// PathSessionSuggestion is the suggestion for a path surface: every pattern to record, one label.
type PathSessionSuggestion struct {
	// Surface is the permission surface this approval applies to.
	Surface string
	// Patterns are the wildcard patterns to store as session rules.
	Patterns []string
	// Label is the human-readable label for the "for session" dialog option.
	Label string
}

// SuggestPathSessionPattern builds the suggestion for a path surface from patterns the caller already derived through its
// PathNormalizer. The label names the approval's target, so a directory's pair reads as its contents glob.
func SuggestPathSessionPattern(surface string, approvalPatterns []string) PathSessionSuggestion {
	grants := make([]ApprovalGrant, len(approvalPatterns))
	for i, pattern := range approvalPatterns {
		grants[i] = ApprovalGrant{Surface: surface, Pattern: pattern}
	}
	return PathSessionSuggestion{
		Surface:  surface,
		Patterns: approvalPatterns,
		Label:    buildLabel(strings.Join(GrantTargets(grants), `", "`), surface),
	}
}

// SuggestExactSessionPattern builds the suggestion for a pattern the caller already holds verbatim, such as a Pi MCP tool's full
// name on the mcp surface: no wildcard is derived.
func SuggestExactSessionPattern(surface, pattern string) SessionApprovalSuggestion {
	return SessionApprovalSuggestion{Surface: surface, Pattern: pattern, Label: buildLabel(pattern, surface)}
}
