// Package events defines the versioned EventBus channel names and JSON payload
// schemas shared between pig-hermes and derived Piglets such as oh-my-hermes-pig.
package events

const (
	// ChannelSubagentStart is emitted before a delegated child agent runs.
	ChannelSubagentStart = "hermes:subagent_start"
	// ChannelSubagentStop is emitted when a delegated child agent finishes.
	ChannelSubagentStop = "hermes:subagent_stop"
	// ChannelDelegateRouteQuery requests a model/thinking route override.
	ChannelDelegateRouteQuery = "hermes:delegate_route_query"
	// ChannelTodoUpdated is emitted whenever the session todo list mutates.
	ChannelTodoUpdated = "hermes:todo_updated"
	// ChannelMemoryWrite is emitted whenever MEMORY.md or USER.md is written.
	ChannelMemoryWrite = "hermes:memory_write"
	// ChannelChildContext carries subagent lineage metadata for child sessions.
	ChannelChildContext = "hermes:child_context"
)

// Prompt section names registered by pig-hermes via sdk.SystemPromptSections.Set.
// Derived Piglets (e.g. oh-my-hermes-pig) append after these without overwriting them.
const (
	SectionIdentity    = "hermes_identity"
	SectionMemory      = "hermes_memory"
	SectionSkills      = "hermes_skills"
	SectionContext     = "hermes_context"
	SectionTools       = "hermes_tools"
	SectionEnvironment = "hermes_environment"
)

type SubagentStartPayload struct {
	ParentSessionID string `json:"parentSessionId"`
	ChildSessionID  string `json:"childSessionId"`
	TaskID          string `json:"taskId"`
	Goal            string `json:"goal"`
	Category        string `json:"category,omitempty"`
	Model           string `json:"model,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	StartedAt       string `json:"startedAt"`
}

type SubagentStopPayload struct {
	ParentSessionID string  `json:"parentSessionId"`
	ChildSessionID  string  `json:"childSessionId"`
	TaskID          string  `json:"taskId"`
	Status          string  `json:"status"`
	DurationMs      int64   `json:"durationMs"`
	InputTokens     int     `json:"inputTokens,omitempty"`
	OutputTokens    int     `json:"outputTokens,omitempty"`
	Cost            float64 `json:"cost,omitempty"`
	Error           string  `json:"error,omitempty"`
}

type MemoryWritePayload struct {
	SessionID   string `json:"sessionId,omitempty"`
	Target      string `json:"target"`
	Action      string `json:"action"`
	EntrySHA256 string `json:"entrySha256"`
	CharsUsed   int    `json:"charsUsed"`
	CharsCap    int    `json:"charsCap"`
}
