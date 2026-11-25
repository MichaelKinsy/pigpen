// Package debug ports omp's `debug` tool and DAP client/session subsystem.
//
// upstream: packages/coding-agent/src/dap/types.ts
// upstream: packages/coding-agent/src/tools/debug.ts
package debug

import (
	"encoding/json"
	"strings"
)

// DapSessionStatus represents the lifecycle state of a DAP debug session.
type DapSessionStatus string

const (
	StatusLaunching   DapSessionStatus = "launching"
	StatusConfiguring DapSessionStatus = "configuring"
	StatusStopped     DapSessionStatus = "stopped"
	StatusRunning     DapSessionStatus = "running"
	StatusTerminated  DapSessionStatus = "terminated"
)

// ConnectMode is how the client connects to a DAP adapter.
type ConnectMode string

const (
	ConnectModeStdio  ConnectMode = "stdio"
	ConnectModeSocket ConnectMode = "socket"
	ConnectModeTCP    ConnectMode = "tcp"
)

// LaunchProgramKind describes how a launch program path resolves on disk.
type LaunchProgramKind string

const (
	ProgramKindFile      LaunchProgramKind = "file"
	ProgramKindDirectory LaunchProgramKind = "directory"
	ProgramKindMissing   LaunchProgramKind = "missing"
)

// DapProtocolMessage is the envelope for all DAP wire messages.
type DapProtocolMessage struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Command    string          `json:"command,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	RequestSeq int             `json:"request_seq,omitempty"`
	Success    bool            `json:"success,omitempty"`
	Message    string          `json:"message,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Event      string          `json:"event,omitempty"`
}

// DapRequestMessage represents a DAP request sent by client or adapter.
type DapRequestMessage struct {
	Seq       int    `json:"seq"`
	Type      string `json:"type"`
	Command   string `json:"command"`
	Arguments any    `json:"arguments,omitempty"`
}

// DapResponseMessage represents a DAP response.
type DapResponseMessage struct {
	Seq        int    `json:"seq"`
	Type       string `json:"type"`
	RequestSeq int    `json:"request_seq"`
	Success    bool   `json:"success"`
	Command    string `json:"command"`
	Message    string `json:"message,omitempty"`
	Body       any    `json:"body,omitempty"`
}

// DapEventMessage represents a DAP event.
type DapEventMessage struct {
	Seq   int             `json:"seq"`
	Type  string          `json:"type"`
	Event string          `json:"event"`
	Body  json.RawMessage `json:"body,omitempty"`
}

// DapSource describes a source file in DAP.
type DapSource struct {
	Name             string `json:"name,omitempty"`
	Path             string `json:"path,omitempty"`
	SourceReference  *int   `json:"sourceReference,omitempty"`
	PresentationHint string `json:"presentationHint,omitempty"`
	Origin           string `json:"origin,omitempty"`
	AdapterData      any    `json:"adapterData,omitempty"`
}

// DapBreakpoint represents a DAP breakpoint returned by an adapter.
type DapBreakpoint struct {
	ID                   *int       `json:"id,omitempty"`
	Verified             bool       `json:"verified"`
	Message              string     `json:"message,omitempty"`
	Source               *DapSource `json:"source,omitempty"`
	Line                 *int       `json:"line,omitempty"`
	Column               *int       `json:"column,omitempty"`
	EndLine              *int       `json:"endLine,omitempty"`
	EndColumn            *int       `json:"endColumn,omitempty"`
	InstructionReference string     `json:"instructionReference,omitempty"`
	Offset               *int       `json:"offset,omitempty"`
}

// DapSourceBreakpoint is one source breakpoint request entry.
type DapSourceBreakpoint struct {
	Line         int    `json:"line"`
	Column       *int   `json:"column,omitempty"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
	LogMessage   string `json:"logMessage,omitempty"`
}

// DapFunctionBreakpoint is one function breakpoint request entry.
type DapFunctionBreakpoint struct {
	Name         string `json:"name"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
}

// DapInstructionBreakpoint is one instruction breakpoint request entry.
type DapInstructionBreakpoint struct {
	InstructionReference string `json:"instructionReference"`
	Offset               *int   `json:"offset,omitempty"`
	Condition            string `json:"condition,omitempty"`
	HitCondition         string `json:"hitCondition,omitempty"`
}

// DapDataBreakpoint is one data breakpoint request entry.
type DapDataBreakpoint struct {
	DataID       string `json:"dataId"`
	AccessType   string `json:"accessType,omitempty"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
}

// DapDataBreakpointInfoResponse mirrors DAP's dataBreakpointInfo response body.
type DapDataBreakpointInfoResponse struct {
	DataID      *string  `json:"dataId"`
	Description string   `json:"description"`
	AccessTypes []string `json:"accessTypes,omitempty"`
	CanPersist  *bool    `json:"canPersist,omitempty"`
}

// DapInitializeArguments mirrors DAP initialize request arguments.
type DapInitializeArguments struct {
	ClientID                          string `json:"clientID,omitempty"`
	ClientName                        string `json:"clientName,omitempty"`
	AdapterID                         string `json:"adapterID,omitempty"`
	Locale                            string `json:"locale,omitempty"`
	LinesStartAt1                     bool   `json:"linesStartAt1,omitempty"`
	ColumnsStartAt1                   bool   `json:"columnsStartAt1,omitempty"`
	PathFormat                        string `json:"pathFormat,omitempty"`
	SupportsVariableType              bool   `json:"supportsVariableType,omitempty"`
	SupportsVariablePaging            bool   `json:"supportsVariablePaging,omitempty"`
	SupportsRunInTerminalRequest      bool   `json:"supportsRunInTerminalRequest,omitempty"`
	SupportsStartDebuggingRequest     bool   `json:"supportsStartDebuggingRequest,omitempty"`
	SupportsMemoryReferences          bool   `json:"supportsMemoryReferences,omitempty"`
	SupportsProgressReporting         bool   `json:"supportsProgressReporting,omitempty"`
	SupportsInvalidatedEvent          bool   `json:"supportsInvalidatedEvent,omitempty"`
	SupportsArgsCanBeInterpretedShell bool   `json:"supportsArgsCanBeInterpretedByShell,omitempty"`
}

// DapCapabilities mirrors DAP initialize response capabilities.
type DapCapabilities struct {
	SupportsConfigurationDoneRequest bool           `json:"supportsConfigurationDoneRequest,omitempty"`
	SupportsFunctionBreakpoints      bool           `json:"supportsFunctionBreakpoints,omitempty"`
	SupportsConditionalBreakpoints   bool           `json:"supportsConditionalBreakpoints,omitempty"`
	SupportsTerminateRequest         bool           `json:"supportsTerminateRequest,omitempty"`
	SupportsTerminateThreadsRequest  bool           `json:"supportsTerminateThreadsRequest,omitempty"`
	SupportsEvaluateForHovers        bool           `json:"supportsEvaluateForHovers,omitempty"`
	SupportsSetVariable              bool           `json:"supportsSetVariable,omitempty"`
	SupportsRestartRequest           bool           `json:"supportsRestartRequest,omitempty"`
	SupportsCompletionsRequest       bool           `json:"supportsCompletionsRequest,omitempty"`
	SupportsLogPoints                bool           `json:"supportsLogPoints,omitempty"`
	SupportsDisassembleRequest       bool           `json:"supportsDisassembleRequest,omitempty"`
	SupportsReadMemoryRequest        bool           `json:"supportsReadMemoryRequest,omitempty"`
	SupportsWriteMemoryRequest       bool           `json:"supportsWriteMemoryRequest,omitempty"`
	SupportsModulesRequest           bool           `json:"supportsModulesRequest,omitempty"`
	SupportsLoadedSourcesRequest     bool           `json:"supportsLoadedSourcesRequest,omitempty"`
	SupportsExceptionInfoRequest     bool           `json:"supportsExceptionInfoRequest,omitempty"`
	SupportsInstructionBreakpoints   bool           `json:"supportsInstructionBreakpoints,omitempty"`
	SupportsDataBreakpoints          bool           `json:"supportsDataBreakpoints,omitempty"`
	SupportsSteppingGranularity      bool           `json:"supportsSteppingGranularity,omitempty"`
	SupportsClipboardContext         bool           `json:"supportsClipboardContext,omitempty"`
	Extra                            map[string]any `json:"-"`
}

// UnmarshalJSON decodes both known capability booleans and arbitrary extra fields.
func (c *DapCapabilities) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	type alias DapCapabilities
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*c = DapCapabilities(a)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err == nil {
		c.Extra = raw
	}
	return nil
}

// Has reports whether the named DAP capability is true.
func (c *DapCapabilities) Has(name string) bool {
	if c == nil {
		return false
	}
	switch name {
	case "supportsConfigurationDoneRequest":
		return c.SupportsConfigurationDoneRequest
	case "supportsFunctionBreakpoints":
		return c.SupportsFunctionBreakpoints
	case "supportsConditionalBreakpoints":
		return c.SupportsConditionalBreakpoints
	case "supportsTerminateRequest":
		return c.SupportsTerminateRequest
	case "supportsTerminateThreadsRequest":
		return c.SupportsTerminateThreadsRequest
	case "supportsEvaluateForHovers":
		return c.SupportsEvaluateForHovers
	case "supportsSetVariable":
		return c.SupportsSetVariable
	case "supportsRestartRequest":
		return c.SupportsRestartRequest
	case "supportsCompletionsRequest":
		return c.SupportsCompletionsRequest
	case "supportsLogPoints":
		return c.SupportsLogPoints
	case "supportsDisassembleRequest":
		return c.SupportsDisassembleRequest
	case "supportsReadMemoryRequest":
		return c.SupportsReadMemoryRequest
	case "supportsWriteMemoryRequest":
		return c.SupportsWriteMemoryRequest
	case "supportsModulesRequest":
		return c.SupportsModulesRequest
	case "supportsLoadedSourcesRequest":
		return c.SupportsLoadedSourcesRequest
	case "supportsExceptionInfoRequest":
		return c.SupportsExceptionInfoRequest
	case "supportsInstructionBreakpoints":
		return c.SupportsInstructionBreakpoints
	case "supportsDataBreakpoints":
		return c.SupportsDataBreakpoints
	case "supportsSteppingGranularity":
		return c.SupportsSteppingGranularity
	case "supportsClipboardContext":
		return c.SupportsClipboardContext
	default:
		if c.Extra != nil {
			b, _ := c.Extra[name].(bool)
			return b
		}
		return false
	}
}

// DapStackFrame mirrors DAP StackFrame.
type DapStackFrame struct {
	ID                          int        `json:"id"`
	Name                        string     `json:"name"`
	Source                      *DapSource `json:"source,omitempty"`
	Line                        int        `json:"line"`
	Column                      int        `json:"column"`
	EndLine                     *int       `json:"endLine,omitempty"`
	EndColumn                   *int       `json:"endColumn,omitempty"`
	InstructionPointerReference string     `json:"instructionPointerReference,omitempty"`
	ModuleID                    any        `json:"moduleId,omitempty"`
	PresentationHint            string     `json:"presentationHint,omitempty"`
}

// DapScope mirrors DAP Scope.
type DapScope struct {
	Name               string     `json:"name"`
	PresentationHint   string     `json:"presentationHint,omitempty"`
	VariablesReference int        `json:"variablesReference"`
	Expensive          bool       `json:"expensive"`
	Source             *DapSource `json:"source,omitempty"`
	Line               *int       `json:"line,omitempty"`
	Column             *int       `json:"column,omitempty"`
	EndLine            *int       `json:"endLine,omitempty"`
	EndColumn          *int       `json:"endColumn,omitempty"`
}

// DapPresentationHint mirrors DAP VariablePresentationHint.
type DapPresentationHint struct {
	Kind       string   `json:"kind,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	Lazy       *bool    `json:"lazy,omitempty"`
}

// DapVariable mirrors DAP Variable.
type DapVariable struct {
	Name               string               `json:"name"`
	Value              string               `json:"value"`
	Type               string               `json:"type,omitempty"`
	PresentationHint   *DapPresentationHint `json:"presentationHint,omitempty"`
	EvaluateName       string               `json:"evaluateName,omitempty"`
	VariablesReference int                  `json:"variablesReference"`
	NamedVariables     *int                 `json:"namedVariables,omitempty"`
	IndexedVariables   *int                 `json:"indexedVariables,omitempty"`
	MemoryReference    string               `json:"memoryReference,omitempty"`
}

// DapDisassembledInstruction mirrors DAP DisassembledInstruction.
type DapDisassembledInstruction struct {
	Address          string     `json:"address"`
	InstructionBytes string     `json:"instructionBytes,omitempty"`
	Instruction      string     `json:"instruction"`
	Symbol           string     `json:"symbol,omitempty"`
	Location         *DapSource `json:"location,omitempty"`
	Line             *int       `json:"line,omitempty"`
	Column           *int       `json:"column,omitempty"`
	EndLine          *int       `json:"endLine,omitempty"`
	EndColumn        *int       `json:"endColumn,omitempty"`
}

// DapReadMemoryResponse mirrors DAP readMemory response body.
type DapReadMemoryResponse struct {
	Address         string  `json:"address"`
	UnreadableBytes *int    `json:"unreadableBytes,omitempty"`
	Data            *string `json:"data,omitempty"`
}

// DapWriteMemoryResponse mirrors DAP writeMemory response body.
type DapWriteMemoryResponse struct {
	Offset       *int `json:"offset,omitempty"`
	BytesWritten *int `json:"bytesWritten,omitempty"`
}

// DapModule mirrors DAP Module.
type DapModule struct {
	ID             any    `json:"id"`
	Name           string `json:"name"`
	Path           string `json:"path,omitempty"`
	IsOptimized    *bool  `json:"isOptimized,omitempty"`
	IsUserCode     *bool  `json:"isUserCode,omitempty"`
	Version        string `json:"version,omitempty"`
	SymbolStatus   string `json:"symbolStatus,omitempty"`
	SymbolFilePath string `json:"symbolFilePath,omitempty"`
	DateTimeStamp  string `json:"dateTimeStamp,omitempty"`
	AddressRange   string `json:"addressRange,omitempty"`
}

// DapEvaluateResponse mirrors DAP evaluate response body.
type DapEvaluateResponse struct {
	Result             string               `json:"result"`
	Type               string               `json:"type,omitempty"`
	PresentationHint   *DapPresentationHint `json:"presentationHint,omitempty"`
	VariablesReference int                  `json:"variablesReference"`
	NamedVariables     *int                 `json:"namedVariables,omitempty"`
	IndexedVariables   *int                 `json:"indexedVariables,omitempty"`
	MemoryReference    string               `json:"memoryReference,omitempty"`
}

// DapThread mirrors DAP Thread.
type DapThread struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// DapOutputEventBody mirrors DAP output event body.
type DapOutputEventBody struct {
	Category           string     `json:"category,omitempty"`
	Output             string     `json:"output"`
	Group              string     `json:"group,omitempty"`
	VariablesReference *int       `json:"variablesReference,omitempty"`
	Source             *DapSource `json:"source,omitempty"`
	Line               *int       `json:"line,omitempty"`
	Column             *int       `json:"column,omitempty"`
	Data               any        `json:"data,omitempty"`
}

// DapStoppedEventBody mirrors DAP stopped event body.
type DapStoppedEventBody struct {
	Reason            string `json:"reason"`
	Description       string `json:"description,omitempty"`
	ThreadID          *int   `json:"threadId,omitempty"`
	PreserveFocusHint bool   `json:"preserveFocusHint,omitempty"`
	Text              string `json:"text,omitempty"`
	AllThreadsStopped bool   `json:"allThreadsStopped,omitempty"`
	HitBreakpointIDs  []int  `json:"hitBreakpointIds,omitempty"`
}

// DapContinuedEventBody mirrors DAP continued event body.
type DapContinuedEventBody struct {
	ThreadID            *int `json:"threadId,omitempty"`
	AllThreadsContinued bool `json:"allThreadsContinued,omitempty"`
}

// DapExitedEventBody mirrors DAP exited event body.
type DapExitedEventBody struct {
	ExitCode *int `json:"exitCode,omitempty"`
}

// DapRunInTerminalArguments mirrors DAP runInTerminal reverse-request arguments.
type DapRunInTerminalArguments struct {
	Kind  string             `json:"kind,omitempty"`
	Title string             `json:"title,omitempty"`
	Cwd   string             `json:"cwd,omitempty"`
	Args  []string           `json:"args"`
	Env   map[string]*string `json:"env,omitempty"`
}

// DapRunInTerminalResponse mirrors DAP runInTerminal response body.
type DapRunInTerminalResponse struct {
	ProcessID      int `json:"processId,omitempty"`
	ShellProcessID int `json:"shellProcessId,omitempty"`
}

// DapStartDebuggingArguments mirrors DAP startDebugging reverse-request arguments.
type DapStartDebuggingArguments struct {
	Request       string         `json:"request"`
	Configuration map[string]any `json:"configuration"`
}

// DapAdapterConfig describes one configured DAP adapter (defaults.json or user dap.json/yaml).
type DapAdapterConfig struct {
	Command                 string         `json:"command"`
	Args                    []string       `json:"args,omitempty"`
	Languages               []string       `json:"languages,omitempty"`
	FileTypes               []string       `json:"fileTypes,omitempty"`
	RootMarkers             []string       `json:"rootMarkers,omitempty"`
	LaunchDefaults          map[string]any `json:"launchDefaults,omitempty"`
	AttachDefaults          map[string]any `json:"attachDefaults,omitempty"`
	ConnectMode             ConnectMode    `json:"connectMode,omitempty"`
	AcceptsDirectoryProgram bool           `json:"acceptsDirectoryProgram,omitempty"`
}

// DapResolvedAdapter is an adapter whose executable path has been resolved.
type DapResolvedAdapter struct {
	Name                    string         `json:"name"`
	Command                 string         `json:"command"`
	Args                    []string       `json:"args"`
	ResolvedCommand         string         `json:"resolvedCommand"`
	Languages               []string       `json:"languages"`
	FileTypes               []string       `json:"fileTypes"`
	RootMarkers             []string       `json:"rootMarkers"`
	LaunchDefaults          map[string]any `json:"launchDefaults"`
	AttachDefaults          map[string]any `json:"attachDefaults"`
	ConnectMode             ConnectMode    `json:"connectMode"`
	AcceptsDirectoryProgram bool           `json:"acceptsDirectoryProgram"`
}

// DapBreakpointRecord records a source breakpoint and its verification state.
type DapBreakpointRecord struct {
	ID        *int   `json:"id,omitempty"`
	Verified  bool   `json:"verified"`
	Line      int    `json:"line"`
	Condition string `json:"condition,omitempty"`
	Message   string `json:"message,omitempty"`
}

// DapFunctionBreakpointRecord records a function breakpoint and its verification state.
type DapFunctionBreakpointRecord struct {
	ID        *int   `json:"id,omitempty"`
	Verified  bool   `json:"verified"`
	Name      string `json:"name"`
	Condition string `json:"condition,omitempty"`
	Message   string `json:"message,omitempty"`
}

// DapInstructionBreakpointRecord records an instruction breakpoint and its verification state.
type DapInstructionBreakpointRecord struct {
	ID                   *int   `json:"id,omitempty"`
	Verified             bool   `json:"verified"`
	InstructionReference string `json:"instructionReference"`
	Offset               *int   `json:"offset,omitempty"`
	Condition            string `json:"condition,omitempty"`
	HitCondition         string `json:"hitCondition,omitempty"`
	Message              string `json:"message,omitempty"`
}

// DapDataBreakpointRecord records a data breakpoint and its verification state.
type DapDataBreakpointRecord struct {
	ID           *int   `json:"id,omitempty"`
	Verified     bool   `json:"verified"`
	DataID       string `json:"dataId"`
	AccessType   string `json:"accessType,omitempty"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
	Message      string `json:"message,omitempty"`
}

// DapStopLocation holds the session's current stopped thread/frame/location.
type DapStopLocation struct {
	ThreadID                    *int
	FrameID                     *int
	Reason                      string
	Description                 string
	Text                        string
	FrameName                   string
	InstructionPointerReference string
	Source                      *DapSource
	Line                        *int
	Column                      *int
}

// DapSessionSummary mirrors omp's DapSessionSummary / DebugSessionSnapshot.
// Field names match packages/tui/src/tools/debug.ts so renderers can read them.
type DapSessionSummary struct {
	ID                          string           `json:"id"`
	Adapter                     string           `json:"adapter"`
	Cwd                         string           `json:"cwd"`
	Program                     string           `json:"program,omitempty"`
	Status                      DapSessionStatus `json:"status"`
	LaunchedAt                  string           `json:"launchedAt"`
	LastUsedAt                  string           `json:"lastUsedAt"`
	ThreadID                    *int             `json:"threadId,omitempty"`
	FrameID                     *int             `json:"frameId,omitempty"`
	StopReason                  string           `json:"stopReason,omitempty"`
	StopDescription             string           `json:"stopDescription,omitempty"`
	FrameName                   string           `json:"frameName,omitempty"`
	InstructionPointerReference string           `json:"instructionPointerReference,omitempty"`
	Source                      *DapSource       `json:"source,omitempty"`
	Line                        *int             `json:"line,omitempty"`
	Column                      *int             `json:"column,omitempty"`
	BreakpointFiles             int              `json:"breakpointFiles"`
	BreakpointCount             int              `json:"breakpointCount"`
	FunctionBreakpointCount     int              `json:"functionBreakpointCount"`
	OutputBytes                 int              `json:"outputBytes"`
	OutputTruncated             bool             `json:"outputTruncated"`
	ExitCode                    *int             `json:"exitCode,omitempty"`
	NeedsConfigurationDone      bool             `json:"needsConfigurationDone"`
	ParentSessionID             string           `json:"parentSessionId,omitempty"`
	ChildSessionIDs             []string         `json:"childSessionIds,omitempty"`
}

// DapContinueOutcome is returned by continue and step operations.
type DapContinueOutcome struct {
	Snapshot DapSessionSummary
	State    string // "running" | "stopped" | "terminated"
	TimedOut bool
}

// DapLaunchSessionOptions are inputs to SessionManager.Launch.
type DapLaunchSessionOptions struct {
	Adapter              DapResolvedAdapter
	Program              string
	Args                 []string
	Cwd                  string
	ExtraLaunchArguments map[string]any
}

// DapAttachSessionOptions are inputs to SessionManager.Attach.
type DapAttachSessionOptions struct {
	Adapter DapResolvedAdapter
	Cwd     string
	PID     *int
	Port    *int
	Host    string
}

// DapAttachTarget carries attach targeting inputs for adapter selection.
type DapAttachTarget struct {
	PID  *int
	Port *int
	Host string
}

// LaunchAdapterSelection is the tagged union returned by SelectLaunchAdapter.
type LaunchAdapterSelection struct {
	Kind        string // "adapter" | "unavailable" | "none"
	Adapter     *DapResolvedAdapter
	AdapterName string
	Command     string
}

// DebugReadonlyActions mirrors omp's DEBUG_READONLY_ACTIONS (tools/debug.ts:57).
var DebugReadonlyActions = map[string]bool{
	"output":         true,
	"threads":        true,
	"stack_trace":    true,
	"scopes":         true,
	"variables":      true,
	"disassemble":    true,
	"read_memory":    true,
	"loaded_sources": true,
	"modules":        true,
	"sessions":       true,
}

// IsReadonlyAction reports whether a debug action only reads program state.
func IsReadonlyAction(action string) bool {
	return DebugReadonlyActions[strings.ToLower(action)]
}

// AllDebugActions lists every action in omp's debugActionSchema (tools/debug.ts:69).
var AllDebugActions = []string{
	"launch",
	"attach",
	"set_breakpoint",
	"remove_breakpoint",
	"set_instruction_breakpoint",
	"remove_instruction_breakpoint",
	"data_breakpoint_info",
	"set_data_breakpoint",
	"remove_data_breakpoint",
	"continue",
	"step_over",
	"step_in",
	"step_out",
	"pause",
	"evaluate",
	"stack_trace",
	"threads",
	"scopes",
	"variables",
	"disassemble",
	"read_memory",
	"write_memory",
	"modules",
	"loaded_sources",
	"custom_request",
	"output",
	"terminate",
	"sessions",
}
