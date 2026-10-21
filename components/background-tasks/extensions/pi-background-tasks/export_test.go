package pi_background_tasks

// Test seams: the refusal text and the embedded tool definitions.
func Refusal(feature string) string { return refusal(feature) }
func ToolDefsJSON() []byte          { return tooldefsJSON }

// ResetSettings drops the settings store's memo, as a new process would start with.
func ResetSettings() { settings.clear() }
