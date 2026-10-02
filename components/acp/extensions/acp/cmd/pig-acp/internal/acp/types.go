// Package acp is the Agent Client Protocol side of pig-acp: the translation layer between
// an ACP client (an editor) and a PiG agent driven over `pig --mode rpc`. It ports the
// src/acp/* tree of pi-acp (Sergii Kozak, MIT); see the Package's CREDITS.md.
package acp

import "fmt"

// ProtocolVersion is the only ACP protocol version this adapter speaks. It is pinned to the
// schema of @agentclientprotocol/sdk 0.26.0 (PROTOCOL_VERSION = 1), the one pi-acp builds on.
const ProtocolVersion = 1

// Update is one ACP session update (`session/update` params.update). It is a JSON object,
// so an omitted field and a null field stay distinct, as in the original.
type Update = map[string]any

// Event is one record pig writes in RPC mode (an agent event or an extension UI request).
type Event = map[string]any

// ContentBlock is one block of an ACP prompt.
type ContentBlock = map[string]any

// StopReason is how a prompt turn ended. "error" is internal: ACP has no such stop reason.
type StopReason string

// Stop reasons.
const (
	StopEndTurn   StopReason = "end_turn"
	StopCancelled StopReason = "cancelled"
	StopError     StopReason = "error"
)

// RequestError is a JSON-RPC error with an ACP code (the SDK's RequestError).
type RequestError struct {
	Code    int
	Message string
	Data    any
}

func (e *RequestError) Error() string { return e.Message }

func withDetail(base, detail string) string {
	if detail == "" {
		return base
	}
	return fmt.Sprintf("%s: %s", base, detail)
}

// ErrParse is JSON-RPC -32700.
func ErrParse(data any, detail string) *RequestError {
	return &RequestError{Code: -32700, Message: withDetail("Parse error", detail), Data: data}
}

// ErrInvalidRequest is JSON-RPC -32600.
func ErrInvalidRequest(data any, detail string) *RequestError {
	return &RequestError{Code: -32600, Message: withDetail("Invalid request", detail), Data: data}
}

// ErrMethodNotFound is JSON-RPC -32601.
func ErrMethodNotFound(method string) *RequestError {
	return &RequestError{Code: -32601, Message: fmt.Sprintf("%q: %s", "Method not found", method), Data: map[string]any{"method": method}}
}

// ErrInvalidParams is JSON-RPC -32602.
func ErrInvalidParams(data any, detail string) *RequestError {
	return &RequestError{Code: -32602, Message: withDetail("Invalid params", detail), Data: data}
}

// ErrInternal is JSON-RPC -32603.
func ErrInternal(data any, detail string) *RequestError {
	return &RequestError{Code: -32603, Message: withDetail("Internal error", detail), Data: data}
}

// ErrAuthRequired is the ACP "authentication required" error, -32000.
func ErrAuthRequired(data any, detail string) *RequestError {
	return &RequestError{Code: -32000, Message: withDetail("Authentication required", detail), Data: data}
}

// PermissionOption is one choice offered in `session/request_permission`.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// PermissionRequest is `session/request_permission` params.
type PermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  map[string]any     `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// PermissionOutcome is the client's answer: Outcome "selected" (with OptionID) or "cancelled".
type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// PermissionResponse is `session/request_permission` result.
type PermissionResponse struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// Conn is what the adapter needs from the ACP connection to the client
// (the SDK's AgentSideConnection subset pi-acp uses).
type Conn interface {
	// SessionUpdate sends a `session/update` notification.
	SessionUpdate(sessionID string, update Update) error
	// RequestPermission sends `session/request_permission` and waits for the answer.
	RequestPermission(req PermissionRequest) (PermissionResponse, error)
}

// Proc is the pi RPC child process as the session sees it (pirpc.Process implements it).
type Proc interface {
	OnEvent(handler func(Event)) (unsubscribe func())
	Prompt(message string, images []Image) error
	Abort() error
	GetState() (map[string]any, error)
	GetAvailableModels() (map[string]any, error)
	SetModel(provider, modelID string) error
	GetAvailableThinkingLevels() ([]string, error)
	SetThinkingLevel(level string) error
	SetFollowUpMode(mode string) error
	SetSteeringMode(mode string) error
	// Compact runs manual compaction; customInstructions "" sends none.
	Compact(customInstructions string) (map[string]any, error)
	SetAutoCompaction(enabled bool) error
	// GetSessionStats asks for get_session_stats; timeout 0 waits without a limit.
	GetSessionStats(timeoutMs int) (SessionStats, error)
	SetSessionName(name string) error
	// ExportHTML returns the path pig wrote; outputPath "" lets pig choose.
	ExportHTML(outputPath string) (string, error)
	GetMessages() (map[string]any, error)
	GetCommands() (map[string]any, error)
	SendExtensionUIResponse(resp map[string]any) error
	Dispose()
}

// Image is a pi image attachment: {type:"image", mimeType, data}.
type Image = map[string]any

// SessionStats is `get_session_stats` data, kept as a JSON object.
type SessionStats = map[string]any

// SessionStatsTimeoutMs bounds the auxiliary context-usage request.
const SessionStatsTimeoutMs = 1000

// SpawnParams are the options for starting a pi child.
type SpawnParams struct {
	Cwd string
	// PiCommand overrides the executable ("" = default).
	PiCommand string
	// SessionPath makes pi persist to this exact session file (--session).
	SessionPath string
}

// SpawnFunc starts a pi RPC child. The agent uses pirpc.Spawn unless a test injects another.
type SpawnFunc func(SpawnParams) (Proc, error)

// TurnResult is the outcome of one prompt turn.
type TurnResult struct {
	Reason StopReason
	Err    error
}

// ActiveSession is a live session as the agent uses it (Session implements it).
type ActiveSession interface {
	ID() string
	Cwd() string
	Proc() Proc
	// Prompt starts the turn now, or queues it behind the running one.
	Prompt(message string, images []Image) <-chan TurnResult
	Cancel() error
	WasCancelRequested() bool
	PublishContextUsage()
	SetStartupInfo(text string)
	SendStartupInfoIfPending()
}

// SessionCreateParams describe a session to create or restore.
type SessionCreateParams struct {
	Cwd          string
	McpServers   []any
	Conn         Conn
	FileCommands []FileSlashCommand
	PiCommand    string
	// Proc is set when restoring around an already started child.
	Proc Proc
}

// SessionRegistry is the SessionManager of the original.
type SessionRegistry interface {
	Create(p SessionCreateParams) (ActiveSession, error)
	MaybeGet(sessionID string) ActiveSession
	Get(sessionID string) (ActiveSession, error)
	GetOrCreate(sessionID string, p SessionCreateParams) ActiveSession
	Close(sessionID string)
	CloseAllExcept(keepSessionID string)
	DisposeAll()
}

// RPCError implements jsonrpc.Coded.
func (e *RequestError) RPCError() (int, string, any) { return e.Code, e.Message, e.Data }
