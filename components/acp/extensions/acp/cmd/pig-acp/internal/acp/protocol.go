package acp

// Request and response shapes of the ACP methods the adapter implements. They follow
// schema.json of @agentclientprotocol/sdk 0.26.0 (protocol version 1).

// InitializeRequest is `initialize` params.
type InitializeRequest struct {
	ProtocolVersion    int            `json:"protocolVersion"`
	ClientCapabilities map[string]any `json:"clientCapabilities,omitempty"`
	ClientInfo         map[string]any `json:"clientInfo,omitempty"`
}

// InitializeResponse is `initialize` result.
type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentInfo         map[string]any    `json:"agentInfo"`
	AuthMethods       []map[string]any  `json:"authMethods"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
}

// AgentCapabilities are the capabilities the adapter advertises: only what it implements.
type AgentCapabilities struct {
	LoadSession         bool                `json:"loadSession"`
	McpCapabilities     McpCapabilities     `json:"mcpCapabilities"`
	PromptCapabilities  PromptCapabilities  `json:"promptCapabilities"`
	SessionCapabilities SessionCapabilities `json:"sessionCapabilities"`
}

// McpCapabilities: MCP servers are accepted and stored but never started by the adapter.
type McpCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

// PromptCapabilities lists the prompt content the adapter accepts.
type PromptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// SessionCapabilities advertises the session/list and session/delete methods.
type SessionCapabilities struct {
	List   map[string]any `json:"list"`
	Delete map[string]any `json:"delete"`
}

// NewSessionRequest is `session/new` params.
type NewSessionRequest struct {
	Cwd        string `json:"cwd"`
	McpServers []any  `json:"mcpServers"`
}

// LoadSessionRequest is `session/load` params.
type LoadSessionRequest struct {
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	McpServers []any  `json:"mcpServers"`
}

// ConfigSelectOption is one option of a select config option.
type ConfigSelectOption struct {
	Value       string  `json:"value"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

// ConfigOption is a session config option (this adapter only advertises selects).
type ConfigOption struct {
	Type         string               `json:"type"`
	ID           string               `json:"id"`
	Category     string               `json:"category"`
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	CurrentValue string               `json:"currentValue"`
	Options      []ConfigSelectOption `json:"options"`
}

// AdvertisedModel is one model in the unstable `models` state.
type AdvertisedModel struct {
	ModelID     string  `json:"modelId"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

// ModelState is the unstable session model state.
type ModelState struct {
	AvailableModels []AdvertisedModel `json:"availableModels"`
	CurrentModelID  string            `json:"currentModelId"`
}

// Mode is one thinking level, advertised as an ACP session mode.
type Mode struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

// ModeState is the session mode state.
type ModeState struct {
	AvailableModes []Mode `json:"availableModes"`
	CurrentModeID  string `json:"currentModeId"`
}

// NewSessionResponse is `session/new` result.
type NewSessionResponse struct {
	SessionID     string         `json:"sessionId"`
	ConfigOptions []ConfigOption `json:"configOptions"`
	Models        *ModelState    `json:"models"`
	Modes         ModeState      `json:"modes"`
	Meta          map[string]any `json:"_meta"`
}

// LoadSessionResponse is `session/load` result.
type LoadSessionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
	Models        *ModelState    `json:"models"`
	Modes         ModeState      `json:"modes"`
	Meta          map[string]any `json:"_meta"`
}

// PromptRequest is `session/prompt` params.
type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// PromptResponse is `session/prompt` result.
type PromptResponse struct {
	StopReason StopReason `json:"stopReason"`
}

// ListSessionsRequest is `session/list` params.
type ListSessionsRequest struct {
	Cwd    *string `json:"cwd"`
	Cursor *string `json:"cursor"`
}

// SessionInfo is one entry of `session/list`.
type SessionInfo struct {
	SessionID string  `json:"sessionId"`
	Cwd       string  `json:"cwd"`
	Title     *string `json:"title"`
	UpdatedAt *string `json:"updatedAt"`
}

// ListSessionsResponse is `session/list` result.
type ListSessionsResponse struct {
	Sessions   []SessionInfo  `json:"sessions"`
	NextCursor *string        `json:"nextCursor"`
	Meta       map[string]any `json:"_meta"`
}

// DeleteSessionRequest is `session/delete` params.
type DeleteSessionRequest struct {
	SessionID string `json:"sessionId"`
}

// SetSessionModeRequest is `session/set_mode` params. ModeID is any so a non-string value
// reaches the adapter's own validation.
type SetSessionModeRequest struct {
	SessionID string `json:"sessionId"`
	ModeID    any    `json:"modeId"`
}

// SetSessionConfigOptionRequest is `session/set_config_option` params. Value is any so a
// non-string value reaches the adapter's own validation.
type SetSessionConfigOptionRequest struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     any    `json:"value"`
}

// SetSessionConfigOptionResponse is `session/set_config_option` result.
type SetSessionConfigOptionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// SetSessionModelRequest is the unstable `session/set_model` params.
type SetSessionModelRequest struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

// AuthenticateRequest is `authenticate` params.
type AuthenticateRequest struct {
	MethodID string `json:"methodId"`
}
