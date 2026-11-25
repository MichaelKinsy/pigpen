// Package agents ports omp's multi-agent orchestration subsystem:
// the `task` and `wait` tools, bundled and discovered agents, the `agent://`
// and `history://` URL handlers, and the `agent` / `workpool` eval preludes.
//
// upstream: packages/coding-agent/src/task/*, tools/wait.ts,
// internal-urls/{agent,history}-protocol.ts, eval/{agent,workpool}-bridge.ts
package agents

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
)

// AgentSource identifies where an agent definition was loaded from.
// upstream: packages/tui/src/tools/task.ts (AgentSource)
type AgentSource string

const (
	SourceBundled AgentSource = "bundled"
	SourceUser    AgentSource = "user"
	SourceProject AgentSource = "project"
)

// AgentDefinition is a bundled or discovered subagent definition.
// upstream: packages/coding-agent/src/task/types.ts (AgentDefinition)
type AgentDefinition struct {
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	SystemPrompt   string      `json:"systemPrompt"`
	Tools          []string    `json:"tools,omitempty"`
	Spawns         *SpawnsSpec `json:"spawns,omitempty"`
	Model          []string    `json:"model,omitempty"`
	ThinkingLevel  string      `json:"thinkingLevel,omitempty"`
	Output         any         `json:"output,omitempty"`
	Blocking       bool        `json:"blocking,omitempty"`
	Structured     bool        `json:"structured,omitempty"`
	AutoloadSkills []string    `json:"autoloadSkills,omitempty"`
	ReadSummarize  *bool       `json:"readSummarize,omitempty"`
	Source         AgentSource `json:"source"`
	FilePath       string      `json:"filePath,omitempty"`
}

// SpawnsSpec represents an agent's `spawns` frontmatter: "*" (all) or an explicit list.
type SpawnsSpec struct {
	All    bool
	Agents []string
}

// String formats a SpawnsSpec as omp's session spawns string ("*", "a,b", or "").
func (s *SpawnsSpec) String() string {
	if s == nil {
		return ""
	}
	if s.All {
		return "*"
	}
	return strings.Join(s.Agents, ",")
}

// DiscoveryResult is returned by DiscoverAgents.
// upstream: packages/coding-agent/src/task/discovery.ts (DiscoveryResult)
type DiscoveryResult struct {
	Agents           []AgentDefinition
	ProjectAgentsDir string
	SearchedDirs     []string
}

// TaskItem is one entry in `task`'s batch `tasks[]` array.
// upstream: packages/tui/src/tools/task.ts (TaskItem)
type TaskItem struct {
	Name          string   `json:"name,omitempty"`
	Agent         string   `json:"agent,omitempty"`
	Task          string   `json:"task,omitempty"`
	HasTask       bool     `json:"-"`
	Steps         []string `json:"steps,omitempty"`
	HasSteps      bool     `json:"-"`
	SolutionSpace string   `json:"solutionSpace,omitempty"`
	OutputSchema  any      `json:"outputSchema,omitempty"`
	HasOutput     bool     `json:"-"`
	SchemaMode    string   `json:"schemaMode,omitempty"`
	Tools         []string `json:"tools,omitempty"`
	Effort        string   `json:"effort,omitempty"`
	Isolated      *bool    `json:"isolated,omitempty"`
	Model         any      `json:"model,omitempty"`
	HasModel      bool     `json:"-"`
}

// TaskParams is the decoded input to the `task` tool (batch or flat).
// upstream: packages/tui/src/tools/task.ts (TaskParams)
type TaskParams struct {
	Context       string     `json:"context,omitempty"`
	HasContext    bool       `json:"-"`
	Tasks         []TaskItem `json:"tasks,omitempty"`
	HasTasks      bool       `json:"-"`
	Name          string     `json:"name,omitempty"`
	Agent         string     `json:"agent,omitempty"`
	HasAgent      bool       `json:"-"`
	Task          string     `json:"task,omitempty"`
	HasTask       bool       `json:"-"`
	Steps         []string   `json:"steps,omitempty"`
	HasSteps      bool       `json:"-"`
	SolutionSpace string     `json:"solutionSpace,omitempty"`
	OutputSchema  any        `json:"outputSchema,omitempty"`
	HasOutput     bool       `json:"-"`
	SchemaMode    string     `json:"schemaMode,omitempty"`
	Tools         []string   `json:"tools,omitempty"`
	Effort        string     `json:"effort,omitempty"`
	Isolated      *bool      `json:"isolated,omitempty"`
	Model         any        `json:"model,omitempty"`
	HasModel      bool       `json:"-"`
	HasSchema     bool       `json:"-"` // stale `schema` field rejected by validateShapeParams
}

// UsageCost mirrors pi-ai Usage.cost.
type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// Usage mirrors pi-ai Usage.
// upstream: packages/coding-agent/src/task/index.ts (createUsageTotals)
type Usage struct {
	Input           int       `json:"input"`
	Output          int       `json:"output"`
	CacheRead       int       `json:"cacheRead"`
	CacheWrite      int       `json:"cacheWrite"`
	TotalTokens     int       `json:"totalTokens"`
	ReasoningTokens int       `json:"reasoningTokens,omitempty"`
	Cost            UsageCost `json:"cost"`
}

func (u *Usage) add(other Usage) {
	u.Input += other.Input
	u.Output += other.Output
	u.CacheRead += other.CacheRead
	u.CacheWrite += other.CacheWrite
	if other.TotalTokens > 0 {
		u.TotalTokens += other.TotalTokens
	} else {
		u.TotalTokens += other.Input + other.Output + other.CacheRead + other.CacheWrite
	}
	u.ReasoningTokens += other.ReasoningTokens
	u.Cost.Input += other.Cost.Input
	u.Cost.Output += other.Cost.Output
	u.Cost.CacheRead += other.Cost.CacheRead
	u.Cost.CacheWrite += other.Cost.CacheWrite
	u.Cost.Total += other.Cost.Total
}

// RecentTool is one completed tool entry in AgentProgress.recentTools.
// upstream: packages/tui/src/tools/task.ts (AgentProgress.recentTools)
type RecentTool struct {
	Tool    string `json:"tool"`
	Args    string `json:"args"`
	ArgsKey string `json:"argsKey,omitempty"`
	Intent  string `json:"intent,omitempty"`
	IsError bool   `json:"isError,omitempty"`
	EndMs   int64  `json:"endMs"`
}

// AgentStreaming describes what the model is currently streaming.
// upstream: packages/tui/src/tools/task.ts (AgentStreaming)
type AgentStreaming struct {
	Kind    string `json:"kind"` // "thinking", "text", "call"
	Tool    string `json:"tool,omitempty"`
	Args    string `json:"args,omitempty"`
	ArgsKey string `json:"argsKey,omitempty"`
	Intent  string `json:"intent,omitempty"`
}

// YieldItem records one `yield` call from a subagent.
// upstream: packages/tui/src/tools/task.ts (YieldItem)
type YieldItem struct {
	Data             any    `json:"data,omitempty"`
	HasData          bool   `json:"-"`
	Status           string `json:"status,omitempty"` // "success" or "aborted"
	Error            string `json:"error,omitempty"`
	Type             any    `json:"type,omitempty"` // string (terminal) or []string (incremental)
	Complete         bool   `json:"complete,omitempty"`
	SchemaOverridden bool   `json:"schemaOverridden,omitempty"`
}

// StructuredSubagentOutput carries structured yield validation metadata.
// upstream: packages/tui/src/tools/task.ts (StructuredSubagentOutput)
type StructuredSubagentOutput struct {
	Source  string `json:"source"` // "caller", "agent", "session", "none"
	Mode    string `json:"mode"`   // "permissive", "strict"
	Status  string `json:"status"` // "valid", "invalid", "unavailable"
	Data    any    `json:"data,omitempty"`
	HasData bool   `json:"-"`
	Error   string `json:"error,omitempty"`
}

// OutputMeta records output line/char counts for agent:// results.
type OutputMeta struct {
	LineCount int `json:"lineCount"`
	CharCount int `json:"charCount"`
}

// AgentRetryState mirrors omp's AgentProgress.retryState populated on `auto_retry_start`.
// upstream: packages/coding-agent/src/task/executor.ts:2489-2495
type AgentRetryState struct {
	Attempt      int    `json:"attempt"`
	MaxAttempts  int    `json:"maxAttempts"`
	DelayMs      int64  `json:"delayMs"`
	ErrorMessage string `json:"errorMessage"`
	StartedAtMs  int64  `json:"startedAtMs"`
}

// AgentRetryFailure mirrors omp's AgentProgress.retryFailure / SingleResult.retryFailure
// populated when `auto_retry_end` reports `success: false`.
// upstream: packages/coding-agent/src/task/executor.ts:2503-2508
type AgentRetryFailure struct {
	Attempt      int    `json:"attempt"`
	ErrorMessage string `json:"errorMessage"`
}

// AgentProgress tracks live progress for one subagent in TaskToolDetails.progress.
// upstream: packages/tui/src/tools/task.ts (AgentProgress)
type AgentProgress struct {
	Index                 int                `json:"index"`
	ID                    string             `json:"id"`
	Agent                 string             `json:"agent"`
	AgentSource           AgentSource        `json:"agentSource"`
	Status                string             `json:"status"` // "pending", "running", "completed", "failed", "aborted"
	Task                  string             `json:"task"`
	Assignment            string             `json:"assignment,omitempty"`
	Description           string             `json:"description,omitempty"`
	LastIntent            string             `json:"lastIntent,omitempty"`
	CurrentTool           string             `json:"currentTool,omitempty"`
	CurrentToolArgs       string             `json:"currentToolArgs,omitempty"`
	CurrentToolArgsKey    string             `json:"currentToolArgsKey,omitempty"`
	CurrentToolIntent     string             `json:"currentToolIntent,omitempty"`
	CurrentToolStartMs    int64              `json:"currentToolStartMs,omitempty"`
	Streaming             *AgentStreaming    `json:"streaming,omitempty"`
	RecentTools           []RecentTool       `json:"recentTools"`
	RecentOutput          []string           `json:"recentOutput"`
	ToolCount             int                `json:"toolCount"`
	Requests              int                `json:"requests"`
	Tokens                int                `json:"tokens"`
	ContextTokens         int                `json:"contextTokens,omitempty"`
	ContextWindow         int                `json:"contextWindow,omitempty"`
	Cost                  float64            `json:"cost"`
	DurationMs            int64              `json:"durationMs"`
	ModelOverride         []string           `json:"modelOverride,omitempty"`
	ModelRole             string             `json:"modelRole,omitempty"`
	ResolvedModel         string             `json:"resolvedModel,omitempty"`
	ResolvedModelIdentity string             `json:"resolvedModelIdentity,omitempty"`
	ResolvedThinkingLevel string             `json:"resolvedThinkingLevel,omitempty"`
	RetryState            *AgentRetryState   `json:"retryState,omitempty"`
	RetryFailure          *AgentRetryFailure `json:"retryFailure,omitempty"`
	ExtractedToolData     map[string][]any   `json:"extractedToolData,omitempty"`
	InflightTaskDetails   *TaskToolDetails   `json:"inflightTaskDetails,omitempty"`
}

func (p AgentProgress) clone() AgentProgress {
	out := p
	if len(p.RecentTools) > 0 {
		out.RecentTools = make([]RecentTool, len(p.RecentTools))
		copy(out.RecentTools, p.RecentTools)
	} else {
		out.RecentTools = []RecentTool{}
	}
	if len(p.RecentOutput) > 0 {
		out.RecentOutput = make([]string, len(p.RecentOutput))
		copy(out.RecentOutput, p.RecentOutput)
	} else {
		out.RecentOutput = []string{}
	}
	if p.ModelOverride != nil {
		out.ModelOverride = append([]string(nil), p.ModelOverride...)
	}
	if p.RetryState != nil {
		rs := *p.RetryState
		out.RetryState = &rs
	}
	if p.RetryFailure != nil {
		rf := *p.RetryFailure
		out.RetryFailure = &rf
	}
	if p.ExtractedToolData != nil {
		out.ExtractedToolData = make(map[string][]any, len(p.ExtractedToolData))
		for k, v := range p.ExtractedToolData {
			out.ExtractedToolData[k] = append([]any(nil), v...)
		}
	}
	return out
}

// SingleResult is the settled result of one subagent execution.
// upstream: packages/tui/src/tools/task.ts (SingleResult)
type SingleResult struct {
	Index                 int                       `json:"index"`
	ID                    string                    `json:"id"`
	Agent                 string                    `json:"agent"`
	AgentSource           AgentSource               `json:"agentSource"`
	Task                  string                    `json:"task"`
	Assignment            string                    `json:"assignment,omitempty"`
	Description           string                    `json:"description,omitempty"`
	LastIntent            string                    `json:"lastIntent,omitempty"`
	ExitCode              int                       `json:"exitCode"`
	Output                string                    `json:"output"`
	Stderr                string                    `json:"stderr"`
	Truncated             bool                      `json:"truncated"`
	StructuredOutput      *StructuredSubagentOutput `json:"structuredOutput,omitempty"`
	DurationMs            int64                     `json:"durationMs"`
	Tokens                int                       `json:"tokens"`
	Requests              int                       `json:"requests"`
	ContextTokens         int                       `json:"contextTokens,omitempty"`
	ContextWindow         int                       `json:"contextWindow,omitempty"`
	ModelOverride         []string                  `json:"modelOverride,omitempty"`
	ModelRole             string                    `json:"modelRole,omitempty"`
	ResolvedModel         string                    `json:"resolvedModel,omitempty"`
	ResolvedModelIdentity string                    `json:"resolvedModelIdentity,omitempty"`
	ResolvedThinkingLevel string                    `json:"resolvedThinkingLevel,omitempty"`
	RetryFailure          *AgentRetryFailure        `json:"retryFailure,omitempty"`
	Error                 string                    `json:"error,omitempty"`
	Aborted               bool                      `json:"aborted,omitempty"`
	AbortReason           string                    `json:"abortReason,omitempty"`
	Usage                 *Usage                    `json:"usage,omitempty"`
	OutputPath            string                    `json:"outputPath,omitempty"`
	Isolated              bool                      `json:"isolated,omitempty"`
	ExtractedToolData     map[string][]any          `json:"extractedToolData,omitempty"`
	OutputMeta            *OutputMeta               `json:"outputMeta,omitempty"`
}

// TaskAsyncState mirrors TaskToolDetails.async.
type TaskAsyncState struct {
	State string `json:"state"` // "running", "completed", "failed"
	JobID string `json:"jobId"`
	Type  string `json:"type"` // "task"
}

// TaskToolDetails is the `Details` payload returned by `task` for TUI/Tern renderers.
// upstream: packages/tui/src/tools/task.ts (TaskToolDetails)
type TaskToolDetails struct {
	ProjectAgentsDir *string         `json:"projectAgentsDir"`
	Results          []SingleResult  `json:"results"`
	TotalDurationMs  int64           `json:"totalDurationMs"`
	Usage            *Usage          `json:"usage,omitempty"`
	OutputPaths      []string        `json:"outputPaths,omitempty"`
	Progress         []AgentProgress `json:"progress,omitempty"`
	Async            *TaskAsyncState `json:"async,omitempty"`
}

// JobSnapshot is a background job row surfaced in CoordinationDetails.jobs.
// upstream: packages/tui/src/tools/wait.ts (JobSnapshot)
type JobSnapshot struct {
	ID                    string                    `json:"id"`
	Type                  string                    `json:"type"`   // "bash", "task", "eval", "service"
	Status                string                    `json:"status"` // "running", "completed", "failed", "cancelled"
	Label                 string                    `json:"label"`
	Intent                string                    `json:"intent,omitempty"`
	DurationMs            int64                     `json:"durationMs"`
	SettledAtMs           int64                     `json:"settledAtMs,omitempty"`
	ExitCode              *int                      `json:"exitCode,omitempty"`
	ResolvedModel         string                    `json:"resolvedModel,omitempty"`
	ResolvedModelIdentity string                    `json:"resolvedModelIdentity,omitempty"`
	ResolvedThinkingLevel string                    `json:"resolvedThinkingLevel,omitempty"`
	Requests              int                       `json:"requests,omitempty"`
	Cost                  float64                   `json:"cost,omitempty"`
	ContextTokens         int                       `json:"contextTokens,omitempty"`
	ContextWindow         int                       `json:"contextWindow,omitempty"`
	Widget                *jobs.WidgetView          `json:"widget,omitempty"`
	ResultText            string                    `json:"resultText,omitempty"`
	ErrorText             string                    `json:"errorText,omitempty"`
	Structured            *StructuredSubagentOutput `json:"structured,omitempty"`
	AgentURLID            string                    `json:"agentUrlId,omitempty"`
	AgentOutputPublished  *bool                     `json:"agentOutputPublished,omitempty"`
}

// IrcDeliveryReceipt records the outcome of delivering a peer message.
// upstream: packages/tui/src/tools/irc.ts (IrcDeliveryReceipt)
type IrcDeliveryReceipt struct {
	To      string `json:"to"`
	Outcome string `json:"outcome"` // "injected", "woken", "revived", "failed"
	Error   string `json:"error,omitempty"`
}

// AgentActivitySnapshot describes a live subagent not covered by an owned job row.
// upstream: packages/tui/src/tools/wait.ts (AgentActivitySnapshot)
type AgentActivitySnapshot struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId,omitempty"`
	Activity string `json:"activity,omitempty"`
	AgeMs    int64  `json:"ageMs"`
	Live     bool   `json:"live"`
}

// CoordinationDetails is the `Details` payload of `wait` and `agent://` message writes.
// upstream: packages/tui/src/tools/wait.ts (CoordinationDetails), async/job-control.ts:490-497
type CoordinationDetails struct {
	Op          string                  `json:"op"` // "send", "wait", "cancel"
	Meta        map[string]any          `json:"meta,omitempty"`
	From        string                  `json:"from,omitempty"`
	To          string                  `json:"to,omitempty"`
	Receipts    []IrcDeliveryReceipt    `json:"receipts,omitempty"`
	Jobs        []JobSnapshot           `json:"jobs,omitempty"`
	Cancelled   []CancelSummary         `json:"cancelled,omitempty"`
	Agents      []AgentActivitySnapshot `json:"agents,omitempty"`
	Interrupted bool                    `json:"interrupted,omitempty"`
}

// MarshalJSON preserves `"jobs": []` on `wait`/`cancel` details (where omp
// always emits the `jobs` array even when empty, `tools/wait.ts:428`,
// `async/job-control.ts:494`) while omitting `jobs` on `send` details.
func (d CoordinationDetails) MarshalJSON() ([]byte, error) {
	if d.Jobs != nil {
		type alias struct {
			Op          string                  `json:"op"`
			Meta        map[string]any          `json:"meta,omitempty"`
			From        string                  `json:"from,omitempty"`
			To          string                  `json:"to,omitempty"`
			Receipts    []IrcDeliveryReceipt    `json:"receipts,omitempty"`
			Jobs        []JobSnapshot           `json:"jobs"`
			Cancelled   []CancelSummary         `json:"cancelled,omitempty"`
			Agents      []AgentActivitySnapshot `json:"agents,omitempty"`
			Interrupted bool                    `json:"interrupted,omitempty"`
		}
		return json.Marshal(alias(d))
	}
	type alias CoordinationDetails
	return json.Marshal(alias(d))
}

// CancelSummary records one cancelled job in CoordinationDetails.cancelled.
type CancelSummary struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "cancelled", "not_found", "already_completed"
}

// Config configures the agents runtime. Defaults match omp v19.1.1.
// upstream: packages/coding-agent/src/task/settings.ts
type Config struct {
	AsyncEnabled            bool
	BatchEnabled            bool
	Structured              bool
	StepsEnabled            bool
	EnableEffort            bool
	MaxEffort               string // "minimal"|"low"|"medium"|"high"|"xhigh"|"max"; default "max"
	IsolationEnabled        bool
	MaxConcurrency          int // 0 = unlimited; default 32
	MaxRecursionDepth       int // <0 = unlimited; default 2 (host capped to 1)
	MaxRuntimeMs            int // 0 = unlimited
	SoftRequestBudget       int // 0 = disabled; default 200
	SoftRequestBudgetNotice bool
	AgentSoftRequestBudget  map[string]int
	AgentIdleTTLMs          int // 0 = disabled; default 420_000 (7 min)
	DisabledAgents          []string
	ModelOverrides          map[string][]string
	PerCallModel            string // "off", "eval", "on"; default "off"
	EvalToolsEnabled        bool
	CrossInstance           bool
	// WaitFloorMs and WaitTopMs control wait's idle ladder windows.
	// Defaults match omp WAIT_LADDER_MS: 5_000ms and 240_000ms.
	WaitFloorMs  int
	WaitTopMs    int
	WaitSettleMs int
}

// DefaultConfig returns omp v19.1.1 default settings for task and wait.
func DefaultConfig() Config {
	return Config{
		AsyncEnabled:            true,
		BatchEnabled:            true,
		Structured:              false,
		StepsEnabled:            false,
		EnableEffort:            false,
		MaxEffort:               "max",
		IsolationEnabled:        false,
		MaxConcurrency:          32,
		MaxRecursionDepth:       2,
		MaxRuntimeMs:            0,
		SoftRequestBudget:       200,
		SoftRequestBudgetNotice: true,
		AgentIdleTTLMs:          420_000,
		PerCallModel:            "off",
		EvalToolsEnabled:        false,
		CrossInstance:           false,
		WaitFloorMs:             5_000,
		WaitTopMs:               240_000,
		WaitSettleMs:            1_500,
	}
}

// CanSpawnAtDepth reports whether an agent at taskDepth may still spawn children.
// maxRecursionDepth < 0 disables the cap.
// upstream: packages/coding-agent/src/task/types.ts:288
func CanSpawnAtDepth(maxRecursionDepth, taskDepth int) bool {
	return maxRecursionDepth < 0 || taskDepth < maxRecursionDepth
}

func formatDurationMs(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	if ms < 1000 {
		return itoa(ms) + "ms"
	}
	secs := float64(ms) / 1000.0
	if secs < 60 {
		tenths := int64(math.Round(secs * 10))
		return itoa(tenths/10) + "." + itoa(tenths%10) + "s"
	}
	totalSecs := int64(math.Round(secs))
	mins := totalSecs / 60
	remSecs := totalSecs % 60
	if mins < 60 {
		if remSecs == 0 {
			return itoa(mins) + "m"
		}
		return itoa(mins) + "m" + itoa(remSecs) + "s"
	}
	hours := mins / 60
	remMins := mins % 60
	if remMins == 0 {
		return itoa(hours) + "h"
	}
	return itoa(hours) + "h" + itoa(remMins) + "m"
}

func formatBytesSize(n int) string {
	if n < 1024 {
		return itoa(int64(n)) + "B"
	}
	if n < 1024*1024 {
		tenths := int64(math.Round(float64(n) * 10 / 1024.0))
		return itoa(tenths/10) + "." + itoa(tenths%10) + "KB"
	}
	tenths := int64(math.Round(float64(n) * 10 / (1024.0 * 1024.0)))
	return itoa(tenths/10) + "." + itoa(tenths%10) + "MB"
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
