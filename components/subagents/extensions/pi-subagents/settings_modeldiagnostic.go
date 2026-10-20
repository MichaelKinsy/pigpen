package pi_subagents

import (
	"regexp"
	"strings"
)

// Child model resolution diagnostics (src/runs/shared/model-resolution-diagnostic.ts).

// ChildModelResolutionDiagnostic is model-resolution-diagnostic.ts ChildModelResolutionDiagnostic. Host is "parent" or "runner".
type ChildModelResolutionDiagnostic struct {
	Agent, Model, Host string
	// CapabilityCeiling is the launch's resolved ceiling, when one is active.
	CapabilityCeiling *DiagnosticCeiling
}

// DiagnosticCeiling is the part of a capability ceiling the diagnostic names.
type DiagnosticCeiling struct {
	DenyExtensions bool
	Sources        []string
}

var modelResolutionFailurePatterns = []*regexp.Regexp{regexp.MustCompile(`(?i)^Model .+ not found\b`), regexp.MustCompile(`(?i)^Unknown provider "`)}

// isChildModelResolutionFailure is true for a core model-resolution failure, which a missing extension can explain.
func isChildModelResolutionFailure(err string) bool {
	text := jsTrim(err)
	for _, p := range modelResolutionFailurePatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

func denyExtensionsClause(d ChildModelResolutionDiagnostic) string {
	sources := "an unnamed source"
	if d.CapabilityCeiling != nil && len(d.CapabilityCeiling.Sources) > 0 {
		sources = strings.Join(d.CapabilityCeiling.Sources, ", ")
	}
	return "Capability ceiling from " + sources + " denies extensions"
}

// formatChildModelResolutionDiagnostic explains a child model that did not resolve because the extension serving its provider
// never loaded (model-resolution-diagnostic.ts:50).
func formatChildModelResolutionDiagnostic(d ChildModelResolutionDiagnostic) string {
	subject := "Subagent"
	if d.Agent != "" {
		subject = "Agent '" + d.Agent + "'"
	}
	model := "this model"
	if d.Model != "" {
		model = "'" + d.Model + "'"
	}
	if d.CapabilityCeiling != nil && d.CapabilityCeiling.DenyExtensions {
		if d.Host == "parent" {
			return strings.Join([]string{
				subject + " ran as a foreground child, which never loads the parent's ambient extensions.",
				denyExtensionsClause(d) + ", so if " + model + " is served by a provider extension, that extension cannot load for this child: `async: true` does not help either, and listed `subagentOnlyExtensions` or `extensions` entries are suppressed.",
				"Relax the capability ceiling to allow extensions, or use a model that the parent's model registry already resolves.",
			}, "\n")
		}
		return strings.Join([]string{
			subject + " ran as a background child without the ambient extensions.",
			denyExtensionsClause(d) + ", so if " + model + " is served by a provider extension, that extension cannot load for this child: listed `subagentOnlyExtensions` or `extensions` entries are suppressed, and leaving `extensions` unset does not enable ambient loading.",
			"Relax the capability ceiling to allow extensions, or use a model that the child's model registry already resolves.",
		}, "\n")
	}
	if d.Host == "parent" {
		return strings.Join([]string{
			subject + " ran as a foreground child, which never loads the parent's ambient extensions but inherits the providers they registered.",
			"If " + model + " is served by a provider extension, check the parent's `/model` list and the child extension diagnostics, or load the extension for this child with `subagentOnlyExtensions` or `extensions` in the agent frontmatter.",
		}, "\n")
	}
	return strings.Join([]string{
		subject + " ran as a background child without the ambient extensions.",
		"If " + model + " is served by a provider extension, that extension is not loaded for this child: list it in `subagentOnlyExtensions` or `extensions` in the agent frontmatter, or leave `extensions` unset so the child loads the ambient extensions.",
	}, "\n")
}
