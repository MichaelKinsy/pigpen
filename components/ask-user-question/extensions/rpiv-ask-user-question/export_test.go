package ask_user_question

// Test-only access to the defaults for the external test package.
var (
	DefaultToolDescription  = func() string { return defaultToolDescription }
	DefaultPromptSnippet    = func() string { return defaultPromptSnippet }
	DefaultPromptGuidelines = func() []string { return defaultPromptGuidelines }
)
