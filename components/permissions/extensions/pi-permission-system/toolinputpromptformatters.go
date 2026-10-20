package pi_permission_system

import "strings"

// Port of src/tool-input/tool-input-prompt-formatters.ts: the built-in previews of the edit, write and read tool inputs.

// GetPromptPath is getPromptPath: the trimmed `path`, else the trimmed `file_path`, ok false when neither is a non-empty string.
func GetPromptPath(input *jsObject) (string, bool) {
	if path, ok := recordString(input, "path"); ok {
		return path, true
	}
	return recordString(input, "file_path")
}

// FormatEditInputForPrompt summarizes an edit input: the path, the replacement count and the first edit's line counts.
func FormatEditInputForPrompt(input *jsObject) string {
	path, hasPath := GetPromptPath(input)
	var rawEdits []any
	if edits, ok := input.vals["edits"].([]any); ok {
		rawEdits = edits
	} else {
		oldText, okOld := input.vals["oldText"].(string)
		newText, okNew := input.vals["newText"].(string)
		if okOld && okNew {
			pair := newObject()
			pair.set("oldText", oldText)
			pair.set("newText", newText)
			rawEdits = []any{pair}
		}
	}
	type edit struct{ oldText, newText string }
	var edits []edit
	for _, raw := range rawEdits {
		record := ToRecord(raw)
		oldText, okOld := record.vals["oldText"].(string)
		newText, okNew := record.vals["newText"].(string)
		if okOld && okNew {
			edits = append(edits, edit{oldText, newText})
		}
	}

	pathPart := ""
	if hasPath {
		pathPart = "for '" + path + "'"
	}
	if len(edits) == 0 {
		if pathPart != "" {
			return pathPart + " with edit input"
		}
		return "with edit input"
	}

	first := edits[0]
	firstEditSummary := "edit #1 replaces " + FormatCount(CountTextLines(first.oldText), "line", "lines") + " with " +
		FormatCount(CountTextLines(first.newText), "line", "lines")
	extraEdits := ""
	if len(edits) > 1 {
		extraEdits = ", plus " + FormatCount(len(edits)-1, "additional edit", "additional edits")
	}
	summary := "(" + FormatCount(len(edits), "replacement", "replacements") + ": " + firstEditSummary + extraEdits + ")"
	if pathPart != "" {
		return pathPart + " " + summary
	}
	return summary
}

// FormatWriteInputForPrompt summarizes a write input: the path, the line count and the character count (UTF-16 units).
func FormatWriteInputForPrompt(input *jsObject) string {
	path, hasPath := GetPromptPath(input)
	content, _ := input.vals["content"].(string)
	summary := "(" + FormatCount(CountTextLines(content), "line", "lines") + ", " + FormatCount(utf16LenWTF(content), "character", "characters") + ")"
	if hasPath {
		return "for '" + path + "' " + summary
	}
	return summary
}

// FormatReadInputForPrompt summarizes a read input: the path, the offset and the limit.
func FormatReadInputForPrompt(input *jsObject) string {
	var parts []string
	if path, ok := GetPromptPath(input); ok {
		parts = append(parts, "path '"+path+"'")
	}
	if offset, ok := input.vals["offset"].(float64); ok {
		parts = append(parts, "offset "+jsNumber(offset))
	}
	if limit, ok := input.vals["limit"].(float64); ok {
		parts = append(parts, "limit "+jsNumber(limit))
	}
	if len(parts) == 0 {
		return ""
	}
	return "for " + strings.Join(parts, ", ")
}
