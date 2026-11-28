// Package vibe ports omp v19.1.1's Vibe mode subsystem: persistent addressable
// worker sessions ("fast" / "good" CLIs), the persisted lifecycle journal
// vocabulary, the five ephemeral director tools (`vibe_spawn`, `vibe_send`,
// `vibe_wait`, `vibe_kill`, `vibe_list`), and the `/vibe` slash command.
//
// upstream:
//   - packages/coding-agent/src/vibe/state.ts
//   - packages/coding-agent/src/vibe/lifecycle.ts
//   - packages/coding-agent/src/vibe/runtime.ts
//   - packages/coding-agent/src/tools/vibe.ts
//   - packages/tui/src/tools/vibe.ts
//   - packages/coding-agent/src/slash-commands/builtin-modes.ts:316-332
//   - packages/coding-agent/src/modes/interactive-mode.ts:5883-5906, 6115-6134, 7391-7604
//   - packages/coding-agent/src/session/agent-session.ts:8618-8653, 8966-8978
package vibe

// ModeState is Vibe mode session-level state, mirroring plan-mode/state.ts.
// upstream: packages/coding-agent/src/vibe/state.ts:1-4
type ModeState struct {
	Enabled bool `json:"enabled"`
}

// ModeContextMessageType is the custom message type tag used for the hidden
// Vibe director directive injected while Vibe mode is active.
// upstream: packages/tui/src/chat/messages.ts:30
const ModeContextMessageType = "vibe-mode-context"

// CLI identifies one of the two worker CLI flavors the director drives.
// upstream: packages/tui/src/tools/vibe.ts:109
type CLI string

const (
	CLIFast CLI = "fast"
	CLIGood CLI = "good"
)

// Valid reports whether c is one of the two supported CLI flavors.
func (c CLI) Valid() bool {
	return c == CLIFast || c == CLIGood
}

// SessionState is the worker session lifecycle state shown to the director.
// upstream: packages/tui/src/tools/vibe.ts:52
type SessionState string

const (
	StateStarting SessionState = "starting"
	StateRunning  SessionState = "running"
	StateIdle     SessionState = "idle"
	StateDead     SessionState = "dead"
)

// Op identifies one of the five Vibe tool operations.
// upstream: packages/tui/src/tools/vibe.ts:30
type Op string

const (
	OpSpawn Op = "spawn"
	OpSend  Op = "send"
	OpWait  Op = "wait"
	OpKill  Op = "kill"
	OpList  Op = "list"
)

// ScreenSnapshot is the live per-session "TV wall" snapshot returned in Vibe
// tool details. Every string field is already one-line sanitized.
// upstream: packages/tui/src/tools/vibe.ts:59-80
type ScreenSnapshot struct {
	ID              string       `json:"id"`
	CLI             CLI          `json:"cli"`
	State           SessionState `json:"state"`
	Killed          bool         `json:"killed,omitempty"`
	Model           string       `json:"model,omitempty"`
	Turns           int          `json:"turns"`
	Queued          int          `json:"queued"`
	TurnStartedAt   int64        `json:"turnStartedAt,omitempty"`
	TurnMessage     string       `json:"turnMessage,omitempty"`
	CurrentTool     string       `json:"currentTool,omitempty"`
	CurrentToolArgs string       `json:"currentToolArgs,omitempty"`
	LastIntent      string       `json:"lastIntent,omitempty"`
	Trace           []string     `json:"trace"`
	OutputTail      []string     `json:"outputTail"`
	LastActivity    string       `json:"lastActivity,omitempty"`
	LastActivityAt  int64        `json:"lastActivityAt"`
}

// SpawnOutcome is the worker identity and turn job handle returned by Spawn.
// upstream: packages/tui/src/tools/vibe.ts:83-86
type SpawnOutcome struct {
	ID    string `json:"id"`
	JobID string `json:"jobId"`
}

// SendMode classifies how Send delivered a message to a worker.
// upstream: packages/tui/src/tools/vibe.ts:90-98
type SendMode string

const (
	SendModeTurn    SendMode = "turn"
	SendModeSteered SendMode = "steered"
	SendModeQueued  SendMode = "queued"
)

// SendOutcome is the delivery mode and optional turn job handle returned by Send.
// upstream: packages/tui/src/tools/vibe.ts:89-99
type SendOutcome struct {
	ID    string   `json:"id"`
	Mode  SendMode `json:"mode"`
	JobID string   `json:"jobId,omitempty"`
}

// KillOutcome records the result of terminating one worker session.
// upstream: packages/tui/src/tools/vibe.ts:102-106
type KillOutcome struct {
	ID            string `json:"id"`
	CancelledTurn bool   `json:"cancelledTurn"`
}

// SettledTurn is one watched turn that settled before or during Wait.
// upstream: packages/tui/src/tools/vibe.ts:109-115
type SettledTurn struct {
	ID         string `json:"id"`
	JobID      string `json:"jobId"`
	Status     string `json:"status"` // "completed" | "failed" | "cancelled"
	ResultText string `json:"resultText"`
}

// WaitOutcome is the settled and still-running snapshot returned by Wait.
// upstream: packages/tui/src/tools/vibe.ts:109-117
type WaitOutcome struct {
	Settled      []SettledTurn `json:"settled"`
	StillRunning []string      `json:"stillRunning"`
	TimedOut     bool          `json:"timedOut"`
}

// WaitSettledDetail is the compact settled summary carried in ToolDetails.Wait.
// upstream: packages/tui/src/tools/vibe.ts:43
type WaitSettledDetail struct {
	ID     string `json:"id"`
	JobID  string `json:"jobId"`
	Status string `json:"status"`
}

// WaitDetails is the wait-specific details payload in ToolDetails.
// upstream: packages/tui/src/tools/vibe.ts:42-48
type WaitDetails struct {
	Settled      []WaitSettledDetail `json:"settled"`
	StillRunning []string            `json:"stillRunning"`
	TimedOut     bool                `json:"timedOut"`
	Waiting      bool                `json:"waiting,omitempty"`
}

// SpawnedDetail is the spawn-specific details payload in ToolDetails.
// upstream: packages/tui/src/tools/vibe.ts:40
type SpawnedDetail struct {
	ID    string `json:"id"`
	CLI   CLI    `json:"cli"`
	JobID string `json:"jobId"`
}

// ToolDetails is the details payload shared by all five Vibe tools.
// upstream: packages/tui/src/tools/vibe.ts:33-50
type ToolDetails struct {
	Op           Op               `json:"op"`
	Screens      []ScreenSnapshot `json:"screens"`
	HiddenKilled []string         `json:"hiddenKilled,omitempty"`
	Spawned      *SpawnedDetail   `json:"spawned,omitempty"`
	Send         *SendOutcome     `json:"send,omitempty"`
	Wait         *WaitDetails     `json:"wait,omitempty"`
	Killed       *KillOutcome     `json:"killed,omitempty"`
}
