package pi_permission_system

// Lane-local stand-ins for what other lanes of the port own (the lead dedups at integration; the names mirror the TypeScript):
//   - config: PermissionSystemExtensionConfig (here the three fields the writer reads);
//   - authority/prompt: the PromptPayload family (presentation/prompt-payload.ts).

// LogConfig is what the log writer reads of the extension config on every write (the original's getConfig thunk result).
type LogConfig struct {
	DebugLog            bool
	PermissionReviewLog bool
	// ReviewLogFieldMaxWidth is nil when the config names none.
	ReviewLogFieldMaxWidth *float64
}

// PromptPayload is the complete, structured description of a permission ask (ADR 0011 section 2).
type PromptPayload struct {
	// Kind is one of bash, mcp, tool, path, external_directory, bash_external_directory, skill, skill_read, forwarded.
	Kind        string
	Request     PromptRequestFacts
	Evidence    []PromptEvidence
	Annotations []PromptAnnotation
}

// PromptRequestFacts is the invariant core of an ask. A nil pointer is the original's null.
type PromptRequestFacts struct {
	Requester       PromptRequester
	Surface         string
	ToolName        *string
	InvokedToolName *string
	Value           string
	MatchedPattern  *string
	// CommandContext is command_substitution, process_substitution or subshell; nil for a current-shell command.
	CommandContext *string
	ExecutedUnit   *string
}

// PromptRequester is who is asking, one hop below when the ask was forwarded.
type PromptRequester struct {
	AgentName *string
	Forwarded bool
	SessionID *string
}

// PromptEvidence is one piece of decision evidence.
type PromptEvidence struct {
	Label, Text string
	Detail      *string
}

// PromptAnnotation is a model-generated advisory.
type PromptAnnotation struct{ Source, Text string }
