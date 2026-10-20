package pi_permission_system

// Port of src/presentation/fact-vocabulary.ts: the render vocabulary shared by every renderer over a PromptPayload.

// AllEvidence is every evidence entry carrying the given label, in payload order (allEvidence).
func AllEvidence(payload PromptPayload, label string) []PromptEvidence {
	out := []PromptEvidence{}
	for _, e := range payload.Evidence {
		if e.Label == label {
			out = append(out, e)
		}
	}
	return out
}

// FlaggedElements is what the ask is flagging: the decision-relevant value, except that a bash ask that escaped the working
// directory flags the paths it referenced.
func FlaggedElements(payload PromptPayload) []string {
	if payload.Kind == "bash_external_directory" {
		out := []string{}
		for _, e := range AllEvidence(payload, "external path") {
			out = append(out, e.Text)
		}
		return out
	}
	if payload.Request.Value == "" {
		return []string{}
	}
	return []string{payload.Request.Value}
}

// FlaggedElementLabel is what FlaggedElements returns is called.
func FlaggedElementLabel(payload PromptPayload) string {
	if payload.Kind == "bash_external_directory" {
		return "path"
	}
	return ValueLabel(payload)
}

// ValueLabel is what the decision-relevant value is called, per ask shape. A forwarded ask without a payload is labelled from its
// surface: the child's display projection.
func ValueLabel(payload PromptPayload) string {
	switch payload.Kind {
	case "bash", "bash_external_directory":
		return "command"
	case "mcp":
		return "target"
	case "tool":
		return "tool"
	case "path", "external_directory":
		return "path"
	case "skill", "skill_read":
		return "skill"
	case "forwarded":
		switch payload.Request.Surface {
		case "bash":
			return "command"
		case "skill":
			return "skill"
		}
		return "value"
	}
	return ""
}

// DescribeBashCommandContext is the human-readable label for a nested bash execution context, or ok false for a current-shell
// (top-level) command (nil context).
func DescribeBashCommandContext(context *string) (label string, ok bool) {
	if context == nil {
		return "", false
	}
	switch *context {
	case "command_substitution":
		return "command substitution", true
	case "process_substitution":
		return "process substitution", true
	case "subshell":
		return "subshell", true
	}
	return "", false
}
