package pi_permission_system

import "strings"

// Go counterpart of test/helpers/external-directory-fixtures.ts.

const (
	extDirCwd    = "/test/project"
	externalPath = "/outside/project/file.ts"
)

var (
	allPathBearingTools = []string{"read", "write", "edit", "find", "grep", "ls"}
	optionalPathTools   = []string{"find", "grep", "ls"}
	allExtTools         = append(append([]string{}, allPathBearingTools...), "bash")
)

// makeExtDirCheck routes external_directory to its state, path to allow (source special) and every other surface to toolState.
func makeExtDirCheck(externalDirectoryState string, toolState ...string) gfSurfaceCheck {
	ts := "allow"
	if len(toolState) > 0 {
		ts = toolState[0]
	}
	return makeSurfaceCheck(map[string]PermissionCheckResult{
		"external_directory": sc(externalDirectoryState),
		"path":               sc("allow", func(c *PermissionCheckResult) { c.Source = "special" }),
	}, sc(ts))
}

func makeApprovingPrompter() *gfEscalator { return gfDecision(true, DecisionApproved, gfHuman) }

func makeDenyingPrompter(denialReason ...string) *gfEscalator {
	return gfDecision(false, DecisionDenied, gfHuman, func(p *PermissionPromptDecision) {
		if len(denialReason) > 0 {
			p.DenialReason = &denialReason[0]
		}
	})
}

func makeUnavailablePrompter() *gfEscalator {
	return gfDecision(false, DecisionDenied, gfAbsent, func(p *PermissionPromptDecision) { p.ConfirmationUnavailable = true })
}

// findExtDirDecision is the first external_directory-family decision event: a direction-proven tool decides on a directional member,
// so the lookup is by family (#806).
func findExtDirDecision(rig *gfHandlerRig) *PermissionDecisionEvent {
	for _, d := range rig.events.decisions() {
		if SurfaceFamilyOf(d.Surface) == "external_directory" {
			d := d
			return &d
		}
	}
	return nil
}

// blockReviewEntries are the permission_request.blocked review-log entries.
func blockReviewEntries(rig *gfHandlerRig) []gfLogWrite {
	out := []gfLogWrite{}
	for _, l := range rig.logger.reviews {
		if l.event == "permission_request.blocked" {
			out = append(out, l)
		}
	}
	return out
}

// makeExtDirDedupCheck is the session-aware check: ask for external_directory on first access, allow (source session) once a recorded
// session rule covers the path.
func makeExtDirDedupCheck() func(ResolvedAccessIntent, Ruleset) PermissionCheckResult {
	return func(intent ResolvedAccessIntent, rules Ruleset) PermissionCheckResult {
		surface := intentSurface(intent)
		var pathValue string
		if pv, ok := intent.(PathValuesAccessIntent); ok && len(pv.Values) > 0 {
			pathValue = pv.Values[0]
		}
		if SurfaceFamilyOf(surface) == "external_directory" {
			if pathValue != "" && len(rules) > 0 {
				for i := len(rules) - 1; i >= 0; i-- {
					if rules[i].Surface == surface && WildcardMatch(rules[i].Pattern, pathValue, nil) {
						return PermissionCheckResult{State: "allow", ToolName: surface, Source: "session", Origin: "session", MatchedPattern: sp(rules[i].Pattern)}
					}
				}
			}
			return PermissionCheckResult{State: "ask", ToolName: surface, Source: "special", Origin: "global"}
		}
		return PermissionCheckResult{State: "allow", ToolName: surface, Source: "tool", Origin: "builtin"}
	}
}

func extDirToolEvent(toolName, path string) *jsObject {
	return obj("type", "tool_call", "toolCallId", "tc-1", "toolName", toolName, "input", obj("path", path))
}

func extDirBashEvent(command string) *jsObject {
	return obj("type", "tool_call", "toolCallId", "tc-1", "toolName", "bash", "input", obj("command", command))
}

var _ = strings.Contains
