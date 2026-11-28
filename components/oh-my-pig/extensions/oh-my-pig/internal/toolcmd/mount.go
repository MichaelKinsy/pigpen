package toolcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// upstream: packages/coding-agent/src/tools/tool-commands.ts
// upstream: packages/coding-agent/src/tools/index.ts (createTools, toolCommandsEnabled)
// upstream: packages/coding-agent/src/exec/tool-command-host.ts

// Enabled reads boolean settings (notably "tools.commands").
// Default returns def (omp's default for "tools.commands" is true).
// The settings area supplies a real getter at integration time.
var Enabled = func(ctx sdk.Context, key string, def bool) bool {
	return def
}

// DocsModeSetting reads the `tools.commandDocs` setting ("inline" | "builtins" | "catalog").
// Default returns DefaultDocsMode ("catalog", omp v19.1.1 default).
var DocsModeSetting = func(ctx sdk.Context) DocsMode {
	return DefaultDocsMode
}

// InlinePatternsSetting reads the `tools.commandInline` glob allowlist setting.
// Default returns nil (empty list, omp v19.1.1 default).
var InlinePatternsSetting = func(ctx sdk.Context) []string {
	return nil
}

// ExplicitToolOrder, when set by the integrator (e.g. wired from internal/agents
// for child sessions with frontmatter `tools:`), reports whether ctx's session
// has an explicit tool list whose caller order must be preserved rather than
// sorted into omp's default BUILTIN_TOOLS order (`tools/index.ts:862-875`,
// `sdk.ts:4701-4762`). When nil or returning nil/empty, syncSession treats the
// session as using omp's default toolset and orders active tools at session
// start by BuiltinToolOrder.
var ExplicitToolOrder func(ctx sdk.Context) []string

// DispatchRecord mirrors omp's ToolCommandDispatch recorded on a bash result.
// CallID carries the nested ExecuteTool call's own tool-call ID (distinct from
// the outer bash call's ID; PiG coding/nested_tool_calls.go:260-275) so
// downstream consumers such as `checkpoint` can correlate the nested result
// entry; omp's ToolCommandDispatch has no callId field.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:100-122
type DispatchRecord struct {
	Tool     string         `json:"tool"`
	Mode     string         `json:"mode"` // "help" | "execute"
	ExitCode int            `json:"exitCode"`
	Args     map[string]any `json:"args,omitempty"`
	Inner    any            `json:"inner,omitempty"`
	CallID   string         `json:"callId,omitempty"`
}

// ToolCommandDispatches extracts DispatchRecord entries from a completed bash
// tool result's details, matching omp's `toolCommandDispatches`.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:517-522
func ToolCommandDispatches(toolName string, result map[string]any) []DispatchRecord {
	if !TransportTools[toolName] || result == nil {
		return []DispatchRecord{}
	}
	details, ok := result["details"].(map[string]any)
	if !ok || details == nil {
		return []DispatchRecord{}
	}
	rawList, ok := details["toolCommands"].([]any)
	if !ok {
		if typed, ok := details["toolCommands"].([]DispatchRecord); ok {
			return append([]DispatchRecord(nil), typed...)
		}
		return []DispatchRecord{}
	}
	out := make([]DispatchRecord, 0, len(rawList))
	for _, item := range rawList {
		m, ok := item.(map[string]any)
		if !ok || m == nil {
			continue
		}
		tool, tOK := m["tool"].(string)
		mode, mOK := m["mode"].(string)
		if !tOK || !mOK || (mode != "help" && mode != "execute") {
			continue
		}
		var exitCode int
		switch ec := m["exitCode"].(type) {
		case int:
			exitCode = ec
		case int64:
			exitCode = int(ec)
		case float64:
			exitCode = int(ec)
		default:
			continue
		}
		var args map[string]any
		if rawArgs, exists := m["args"]; exists && rawArgs != nil {
			am, ok := rawArgs.(map[string]any)
			if !ok {
				continue
			}
			args = am
		}
		callID, _ := m["callId"].(string)
		out = append(out, DispatchRecord{
			Tool:     tool,
			Mode:     mode,
			ExitCode: exitCode,
			Args:     args,
			Inner:    m["inner"],
			CallID:   callID,
		})
	}
	return out
}

// mountableEntry records an area's declaration that a tool may mount as a bash
// tool command whenever enabled(ctx) returns true and the command transport is active.
type mountableEntry struct {
	name    string
	enabled func(ctx sdk.Context) bool
}

// specialEntry records a special staged-action command (resolve, reject,
// propose, report_issue) that is exposed in the bash preamble outside the
// catalog while the command transport is active, and activated top-level when
// the transport is off and applies(ctx) is true.
type specialEntry struct {
	name    string
	applies func(ctx sdk.Context) bool
}

// autoActivateEntry records a conditional top-level tool (registered with
// DefaultActive: false, e.g. `find` when `find.enabled` is "on" or a native
// judge is available) that must be activated at session_start inside
// toolcmd's initial BUILTIN_TOOLS sort so it lands at its upstream
// BUILTIN_TOOLS position (tools/index.ts:622-652) before any turn runs.
type autoActivateEntry struct {
	name    string
	enabled func(ctx sdk.Context) bool
}

// cmdRuntime manages the package-level registries (mountable declarations,
// special commands, conditional auto-activated built-ins, and per-session
// state + loopback listeners keyed strictly by session ID).
type cmdRuntime struct {
	mu                sync.RWMutex
	mountables        map[string]mountableEntry
	mountableOrder    []string
	specials          map[string]specialEntry
	specialOrder      []string
	autoActivates     map[string]autoActivateEntry
	autoActivateOrder []string
	sessions          map[string]*sessionState
}
type sessionState struct {
	mu              sync.RWMutex
	id              string
	state           *State
	specialNames    []string // special command names active in preamble (outside catalog)
	specialSet      map[string]bool
	active          map[string]bool
	transportActive bool
	commandsEnabled bool
	// explicitOrder holds the session's caller-supplied explicit tool list
	// (e.g. subagent frontmatter `tools:` from ExplicitToolOrder), pinned
	// top-level so explicitly requested built-ins are never mounted as bash
	// commands (omp: tools/index.ts:930-934, sdk.ts:4747-4754,4835-4838).
	explicitOrder []string
	explicitSet   map[string]bool
	// managedActive tracks tools that this area itself activated (mountables
	// and codemode/deferred extension/MCP tools). If a tool in managedActive
	// disappears from PiG's activeTools while still enabled, another party
	// (user tool picker or another extension) deactivated it; we record it in
	// externallyDeactivated and do not re-activate it on subsequent turns
	// (mirroring omp's SessionTools.#enabledToolNames / reconcileBuiltinTools,
	// session/session-tools.ts:1557, 2554-2577).
	managedActive         map[string]bool
	prevMountableEnabled  map[string]bool
	externallyDeactivated map[string]bool
	sessionCtx            sdk.Context // live context captured at session_start (never a per-request/tool context)
	hasSessCtx            bool
	listener              *bridgeServer

	dispatchesMu sync.Mutex
	dispatches   map[string]*dispatchRecorder // keyed by outer bash toolCallID
}

// dispatchRecorder accumulates tool-command dispatch records for one bash tool
// call in invocation order (matching omp's ToolCommandRecorder,
// packages/coding-agent/src/tools/bash.ts:762-788).
type dispatchRecorder struct {
	mu      sync.Mutex
	records []DispatchRecord
	present []bool
}

// reserve claims the next invocation slot in invocation order before execution
// starts, so concurrent invocations inside a pipeline or `&` still land in
// invocation order when they complete.
func (d *dispatchRecorder) reserve() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	idx := len(d.records)
	d.records = append(d.records, DispatchRecord{})
	d.present = append(d.present, false)
	return idx
}

// finish stores the completed dispatch record at its reserved invocation slot.
func (d *dispatchRecorder) finish(index int, rec DispatchRecord) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if index < 0 {
		return
	}
	if index >= len(d.records) {
		grow := index + 1 - len(d.records)
		d.records = append(d.records, make([]DispatchRecord, grow)...)
		d.present = append(d.present, make([]bool, grow)...)
	}
	d.records[index] = rec
	d.present[index] = true
}

// snapshot returns all completed dispatches in invocation order.
func (d *dispatchRecorder) snapshot() []DispatchRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []DispatchRecord
	for i, ok := range d.present {
		if ok {
			out = append(out, d.records[i])
		}
	}
	return out
}

func (s *sessionState) reserveDispatch(callID string) (*dispatchRecorder, int) {
	if callID == "" {
		return nil, -1
	}
	s.dispatchesMu.Lock()
	if s.dispatches == nil {
		s.dispatches = make(map[string]*dispatchRecorder)
	}
	rec := s.dispatches[callID]
	if rec == nil {
		rec = &dispatchRecorder{}
		s.dispatches[callID] = rec
	}
	s.dispatchesMu.Unlock()
	return rec, rec.reserve()
}

func (s *sessionState) drainDispatches(callID string) []DispatchRecord {
	if callID == "" {
		return nil
	}
	s.dispatchesMu.Lock()
	rec := s.dispatches[callID]
	delete(s.dispatches, callID)
	s.dispatchesMu.Unlock()
	if rec == nil {
		return nil
	}
	return rec.snapshot()
}

func (s *sessionState) dropDispatches(callID string) {
	if callID == "" {
		return
	}
	s.dispatchesMu.Lock()
	delete(s.dispatches, callID)
	s.dispatchesMu.Unlock()
}

func (s *sessionState) clearAllDispatches() {
	s.dispatchesMu.Lock()
	s.dispatches = nil
	s.dispatchesMu.Unlock()
}

// dispatchRecordToWire converts a DispatchRecord into omp's wire map shape
// (`tool`, `mode`, `exitCode`, optional `args`, `inner`, and `callId`).
// upstream: packages/coding-agent/src/tools/tool-commands.ts:100-122
func dispatchRecordToWire(rec DispatchRecord) map[string]any {
	m := map[string]any{
		"tool":     rec.Tool,
		"mode":     rec.Mode,
		"exitCode": rec.ExitCode,
	}
	if rec.Args != nil {
		m["args"] = rec.Args
	}
	if !isEmptyInnerDetails(rec.Inner) {
		m["inner"] = rec.Inner
	}
	if rec.CallID != "" {
		m["callId"] = rec.CallID
	}
	return m
}

// isEmptyInnerDetails reports whether a wrapped tool's Result.Details is absent
// or an empty object (`{}`), which PiG's host/tool wrappers often produce where
// omp leaves `result.details` undefined (and therefore omits `dispatch.inner`).
// upstream: packages/coding-agent/src/tools/tool-commands.ts:475
func isEmptyInnerDetails(v any) bool {
	if v == nil {
		return true
	}
	switch d := v.(type) {
	case map[string]any:
		return len(d) == 0
	case *sdk.OrderedObject:
		return d == nil || len(d.Keys()) == 0
	case json.RawMessage:
		trimmed := strings.TrimSpace(string(d))
		return trimmed == "" || trimmed == "null" || trimmed == "{}"
	}
	return false
}

// DrainDispatches returns and clears all tool-command dispatch records
// captured for ctx's active bash tool call (both direct and bridge paths),
// in invocation order, in omp's `ToolCommandDispatch` wire shape. Returns nil
// when no dispatches were recorded for this call or ctx has no session/call ID.
//
// Wired by the integrator to `shell.ToolCommandDispatches = toolcmd.DrainDispatches`.
// upstream: packages/coding-agent/src/tools/bash.ts:762-788, 1345
func DrainDispatches(ctx sdk.Context) []map[string]any {
	key := sessionKeyFor(ctx)
	if key == "" {
		return nil
	}
	var zero sdk.Context
	if ctx == zero {
		return nil
	}
	callID := ctx.ToolCallID()
	if callID == "" {
		return nil
	}
	s := defaultRuntime.getSession(key)
	if s == nil {
		return nil
	}
	recs := s.drainDispatches(callID)
	if len(recs) == 0 {
		return nil
	}
	out := make([]map[string]any, len(recs))
	for i, rec := range recs {
		out[i] = dispatchRecordToWire(rec)
	}
	return out
}

var defaultRuntime = newCmdRuntime()

func newCmdRuntime() *cmdRuntime {
	return &cmdRuntime{
		mountables:    make(map[string]mountableEntry),
		specials:      make(map[string]specialEntry),
		autoActivates: make(map[string]autoActivateEntry),
		sessions:      make(map[string]*sessionState),
	}
}

// Mountable declares that tool `name` is mountable as a bash tool command.
// Keyed by name and safe to call from every `Register` (later calls replace
// the predicate in place).
//
// Each tool area that owns a mountable discoverable tool (e.g. `lsp`, `debug`,
// `ast_edit`, `ast_grep`, `github`, `checkpoint`, `rewind`) registers its SDK
// tool with `Exposure: sdk.ToolExposureCodemode` (so PiG keeps it callable via
// `Context.ExecuteTool` while not declaring it to the model by default) and
// calls `toolcmd.Mountable(name, enabled)` during `Register`.
//
// When `enabled` is nil, the tool is treated as always enabled once registered.
func Mountable(name string, enabled func(ctx sdk.Context) bool) {
	defaultRuntime.registerMountable(name, enabled)
}

// RegisterSpecialCommand declares a special command (`resolve`, `reject`,
// `propose`, `report_issue`) that runs as a bash command outside the
// `# Tool Commands` catalog while the command transport is active, and is
// activated top-level only when the transport is off and `applies(ctx)` is true.
// Keyed by name and safe to call from every `Register`.
// upstream: packages/coding-agent/src/tools/index.ts:944-987, exec/tool-command-host.ts:22-28
func RegisterSpecialCommand(name string, applies func(ctx sdk.Context) bool) {
	defaultRuntime.registerSpecial(name, applies)
}

// RegisterAutoActivate declares a conditional top-level tool (registered with
// DefaultActive: false, e.g. `find` in `internal/search`) whose enabled
// predicate is evaluated at `session_start` inside `toolcmd`'s initial
// `BUILTIN_TOOLS` sort. When `enabled(ctx)` returns true at `session_start`
// and the tool is registered (and not overridden by an explicit caller tool
// list that omits it), `toolcmd` activates it in its exact `BUILTIN_TOOLS`
// position (e.g. `find` immediately after `grep`, `tools/index.ts:635`) before
// any turn runs — working around PiG's `planToolLoadout` limitation
// (`coding/session_loadout.go:178-189`) that always appends tools activated
// after `session_start` to the end of the active list.
// Keyed by name and safe to call from every `Register`.
func RegisterAutoActivate(name string, enabled func(ctx sdk.Context) bool) {
	defaultRuntime.registerAutoActivate(name, enabled)
}

// ResetForTests clears all sessions, closes active listeners, and removes all
// Mountable, RegisterSpecialCommand, and RegisterAutoActivate declarations.
// Intended for unit tests.
func ResetForTests() {
	defaultRuntime.reset()
}

func (r *cmdRuntime) registerMountable(name string, enabled func(ctx sdk.Context) bool) {
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.mountables[name]; !exists {
		r.mountableOrder = append(r.mountableOrder, name)
	}
	r.mountables[name] = mountableEntry{name: name, enabled: enabled}
}

func (r *cmdRuntime) registerSpecial(name string, applies func(ctx sdk.Context) bool) {
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specials[name]; !exists {
		r.specialOrder = append(r.specialOrder, name)
	}
	r.specials[name] = specialEntry{name: name, applies: applies}
}
func (r *cmdRuntime) registerAutoActivate(name string, enabled func(ctx sdk.Context) bool) {
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.autoActivates[name]; !exists {
		r.autoActivateOrder = append(r.autoActivateOrder, name)
	}
	r.autoActivates[name] = autoActivateEntry{name: name, enabled: enabled}
}

func (r *cmdRuntime) snapshotRegistrations() ([]mountableEntry, []specialEntry, []autoActivateEntry) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ms := make([]mountableEntry, 0, len(r.mountableOrder))
	for _, n := range r.mountableOrder {
		if e, ok := r.mountables[n]; ok {
			ms = append(ms, e)
		}
	}
	ss := make([]specialEntry, 0, len(r.specialOrder))
	for _, n := range r.specialOrder {
		if e, ok := r.specials[n]; ok {
			ss = append(ss, e)
		}
	}
	as := make([]autoActivateEntry, 0, len(r.autoActivateOrder))
	for _, n := range r.autoActivateOrder {
		if e, ok := r.autoActivates[n]; ok {
			as = append(as, e)
		}
	}
	return ms, ss, as
}

func (r *cmdRuntime) reset() {
	r.mu.Lock()
	sessions := make([]*sessionState, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.sessions = make(map[string]*sessionState)
	r.mountables = make(map[string]mountableEntry)
	r.mountableOrder = nil
	r.specials = make(map[string]specialEntry)
	r.specialOrder = nil
	r.autoActivates = make(map[string]autoActivateEntry)
	r.autoActivateOrder = nil
	r.mu.Unlock()
	for _, s := range sessions {
		s.closeListener()
	}
}

func (r *cmdRuntime) getSession(sessionKey string) *sessionState {
	r.mu.RLock()
	s := r.sessions[sessionKey]
	r.mu.RUnlock()
	return s
}

func (r *cmdRuntime) getOrCreateSession(sessionKey string) *sessionState {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sessionKey]
	if !ok {
		s = &sessionState{
			id:         sessionKey,
			active:     make(map[string]bool),
			specialSet: make(map[string]bool),
		}
		r.sessions[sessionKey] = s
	}
	return s
}

func (r *cmdRuntime) closeSession(sessionKey string) {
	if sessionKey == "" {
		return
	}
	r.mu.Lock()
	s, ok := r.sessions[sessionKey]
	if ok {
		delete(r.sessions, sessionKey)
	}
	r.mu.Unlock()
	if s != nil {
		s.closeListener()
	}
}

func (s *sessionState) closeListener() {
	s.mu.Lock()
	l := s.listener
	s.listener = nil
	s.mu.Unlock()
	if l != nil {
		l.Close()
	}
	s.clearAllDispatches()
}

// sessionKeyFor resolves the session's unique ID from ctx. Returns "" when ctx
// is zero or the session ID cannot be resolved; callers must never collapse ""
// into a shared "default" bucket or treat it as "all sessions".
func sessionKeyFor(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	if id, err := ctx.GetSessionID(); err == nil && id != "" {
		return id
	}
	if id, err := ctx.SessionManager().GetSessionID(); err == nil && id != "" {
		return id
	}
	return ""
}

// Register installs omp's tool-command lifecycle hooks on e and registers the
// area's per-session cleanup with internal/lifecycle. It is safe to call on
// every fresh extension instance (session start, reload, /new, /resume, /fork),
// performs no I/O, and never panics.
//
// Hooks installed:
//   - lifecycle.OnEnd("toolcmd", ...): releases ONLY the ending session's state
//     and loopback bridge listener in the package-level registry (covers both
//     top-level and child sessions).
//   - session_start: computes the mounted set from Mountable declarations and
//     registered tools, keeps mounted tools active for direct fallback dispatch
//     (matching omp's resolveFallbackTool, sdk.ts:5228, agent-loop.ts:2978-2992)
//     while keeping special commands inactive when transport is active, sorts
//     default-set active tools in omp's BUILTIN_TOOLS order (tools/index.ts:622-652,
//     862-875; explicit lists keep caller order), and records the session's live context.
//   - before_agent_start: re-syncs mounted state in case tools or settings changed.
//   - context: rewrites PiG's bare `Tool <name> not found` error toolResult
//     messages in place to append omp's `Did you mean <tool>?` /
//     `Closest available: ...` suggestions before they reach the model
//     (`packages/agent/src/agent-loop.ts:3019-3069`, `sdk.ts:4327, 5229`).
//   - mcp_servers_change: re-syncs mounted state when MCP tools change.
//   - session_shutdown: calls `lifecycle.End(sessionID)` for ONLY the ending
//     session's ID (no-op if the ID cannot be resolved).
//
// Declaration hiding uses PiG's host `PrepareLoadout` / `HiddenDeclarations`
// mechanism: `PrepareLoadout` (exported below) is wired to `shell.PrepareLoadout`
// by the integrator so mounted tools stay active and callable while their
// declarations are omitted from every provider request and prompt before
// provider conversion (`coding/session_loadout.go:288-327`, `coding/session.go:566`).
func Register(e *sdk.Extension) {
	if e == nil {
		return
	}
	lifecycle.OnEnd("toolcmd", func(sessionID string) {
		defaultRuntime.closeSession(sessionID)
	})
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		defaultRuntime.syncSession(ctx, true, true)
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		defaultRuntime.syncSession(ctx, true, false)
		return nil, nil
	})
	e.OnEvent(sdk.EventContext, func(ctx sdk.Context, data map[string]any) (any, error) {
		messages, ok := data["messages"].([]any)
		if !ok || len(messages) == 0 {
			return nil, nil
		}
		rewriteUnknownToolResultsInPlace(ctx, messages)
		// Return (nil, nil) so PiG's contextEventResult keeps message-list
		// identity (_pigContextUnchanged: true) while propagating in-place
		// edits on message maps to the provider request.
		return nil, nil
	})
	e.OnEvent(sdk.EventMcpServersChange, func(ctx sdk.Context, _ map[string]any) (any, error) {
		defaultRuntime.syncSession(ctx, true, false)
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		// Release ONLY the ending session's state via lifecycle.End; never close
		// other live sessions in the same process, and release nothing if the
		// ending session's ID cannot be resolved.
		if id := sessionKeyFor(ctx); id != "" {
			lifecycle.End(id)
		}
		return nil, nil
	})
}

// Sync refreshes mounted tool-command state for ctx's session from the host's
// registered and active tool lists and Mountable/Special declarations.
func Sync(ctx sdk.Context) {
	defaultRuntime.syncSession(ctx, true, false)
}

// PromptDocs syncs the session's mounted tool state (calling SetActiveTools
// if needed so the prompt and active tool list never lag by one turn) and
// returns the mounted command names and rendered `# Tool Commands` body.
// Exported for the prompt area (`prompt.ToolCommands`).
// upstream: packages/coding-agent/src/sdk.ts:4543-4640
func PromptDocs(ctx sdk.Context) (mounted []string, docs string) {
	return defaultRuntime.promptDocs(ctx)
}

func (r *cmdRuntime) promptDocs(ctx sdk.Context) ([]string, string) {
	key := sessionKeyFor(ctx)
	if key == "" {
		return nil, ""
	}
	// Always re-sync before building the prompt so prompt.Register (even when
	// registered before toolcmd.Register) never reads stale mounts one turn late.
	r.syncSession(ctx, true, false)
	s := r.getSession(key)
	if s == nil {
		return nil, ""
	}
	s.mu.RLock()
	st := s.state
	s.mu.RUnlock()
	if st == nil || len(st.MountedOrder) == 0 {
		return nil, ""
	}
	mode := DefaultDocsMode
	if DocsModeSetting != nil {
		if m := DocsModeSetting(ctx); m != "" {
			mode = m
		}
	}
	var inlinePatterns []string
	if InlinePatternsSetting != nil {
		inlinePatterns = InlinePatternsSetting(ctx)
	}
	entries := ToolCommandEntries(st)
	mounted := make([]string, len(entries))
	for i, e := range entries {
		mounted[i] = e.Name
	}
	docs := ToolCommandDocsAll(st, mode, inlinePatterns)
	return mounted, docs
}

// syncSession recomputes the mounted and special command sets from scratch on
// every call (never sticky via prevMounted):
//   - Built-in mountable tools declared via `Mountable(name, enabled)` (registered
//     with `sdk.ToolExposureCodemode` or `sdk.ToolExposureDeferred`): mounted iff
//     transport is active (`tools.commands` && `bash` active) and `enabled(ctx)`
//     is true. While mounted, they stay in PiG's active tool list so direct
//     model calls still dispatch (omp's resolveFallbackTool, sdk.ts:5228,
//     agent-loop.ts:2978-2992) while `PrepareLoadout` returns them in
//     `HiddenDeclarations` so PiG strips them before any provider conversion
//     (`coding/session_loadout.go:288-327`, `coding/session.go:566`). When
//     transport is off and `enabled(ctx)` is true, they remain active and
//     advertised top-level; when `enabled(ctx)` is false, they are neither
//     mounted nor active.
//   - Registered non-built-in (extension/MCP) tools with `codemode`/`deferred`
//     exposure and valid discoverable names mount automatically when transport is
//     active (and likewise stay active for fallback dispatch while hidden via
//     `PrepareLoadout.HiddenDeclarations`).
//   - Special tools (`resolve`, `reject`, `propose`, `report_issue`) are NEVER
//     added to `mountedNames` or `# Tool Commands` and are NOT kept active while
//     the transport is active (matching omp's `resolveMountedToolCommand`:
//     `tools/tool-commands.ts:237-254`, `sdk.ts:4301-4305, 4755-4761` — direct
//     model calls answer "Tool <name> not found", while bash preamble calls run
//     through `ExecuteTool` via codemode/deferred exposure). While transport is
//     off, registered special commands whose `applies` predicate returns true
//     are activated top-level.
//   - At `fromSessionStart`, when the session has no explicit tool order
//     (`ExplicitToolOrder`), active tools are sorted by omp's `BuiltinToolOrder`
//     (`tools/index.ts:622-652, 862-875`), followed by non-built-ins in relative
//     order. Mid-session syncs never emit an empty `SetActiveTools([])` reset.
func (r *cmdRuntime) syncSession(ctx sdk.Context, applyActiveTools, fromSessionStart bool) {
	key := sessionKeyFor(ctx)
	if key == "" {
		return
	}
	sess := r.getOrCreateSession(key)

	allTools, err := ctx.GetAllTools()
	if err != nil {
		return
	}
	activeTools, err := ctx.GetActiveTools()
	if err != nil {
		return
	}

	activeSet := make(map[string]bool, len(activeTools))
	for _, name := range activeTools {
		activeSet[name] = true
	}

	commandsEnabled := true
	if Enabled != nil {
		commandsEnabled = Enabled(ctx, "tools.commands", true)
	}
	transportActive := commandsEnabled && activeSet["bash"]

	mountables, specials, autoActivates := r.snapshotRegistrations()
	mountableByName := make(map[string]mountableEntry, len(mountables))
	for _, m := range mountables {
		mountableByName[m.name] = m
	}
	specialByName := make(map[string]specialEntry, len(specials))
	for _, sp := range specials {
		specialByName[sp.name] = sp
	}

	// Snapshot per-session state from previous syncs.
	sess.mu.RLock()
	hasPrevSync := sess.state != nil
	prevExplicitOrder := append([]string(nil), sess.explicitOrder...)
	prevManagedActive := copyBoolMap(sess.managedActive)
	prevMountableEnabled := copyBoolMap(sess.prevMountableEnabled)
	externallyDeactivated := copyBoolMap(sess.externallyDeactivated)
	sess.mu.RUnlock()

	var explicitOrder []string
	if fromSessionStart {
		if ExplicitToolOrder != nil {
			explicitOrder = append([]string(nil), ExplicitToolOrder(ctx)...)
		}
		// Fresh session_start resets per-session external deactivation tracking.
		prevManagedActive = make(map[string]bool)
		prevMountableEnabled = make(map[string]bool)
		externallyDeactivated = make(map[string]bool)
	} else {
		explicitOrder = prevExplicitOrder
		if len(explicitOrder) == 0 && !hasPrevSync && ExplicitToolOrder != nil {
			explicitOrder = append([]string(nil), ExplicitToolOrder(ctx)...)
		}
	}
	hasExplicitList := len(explicitOrder) > 0
	explicitSet := make(map[string]bool, len(explicitOrder))
	for _, name := range explicitOrder {
		explicitSet[name] = true
	}
	// Detect tools that toolcmd previously activated but another party (user
	// tool picker or another extension) subsequently removed from activeTools.
	// If the user/extension later re-activates such a tool, clear the mark.
	if hasPrevSync && !fromSessionStart {
		for name := range prevManagedActive {
			if !activeSet[name] {
				externallyDeactivated[name] = true
			}
		}
		for name := range externallyDeactivated {
			if activeSet[name] {
				delete(externallyDeactivated, name)
			}
		}
	}

	toolInfoByName := make(map[string]sdk.ToolInfo, len(allTools))
	toolMap := make(map[string]*ToolDescriptor, len(allTools))
	builtInNames := make(map[string]bool)
	for _, info := range allTools {
		toolInfoByName[info.Name] = info
		isBuiltin := BuiltinToolNames[info.Name] || info.Source == "builtin"
		if isBuiltin {
			builtInNames[info.Name] = true
		}
		lm := BuiltinLoadMode(info.Name)
		if _, declared := mountableByName[info.Name]; declared && !SpecialToolNames[info.Name] {
			lm = LoadModeDiscoverable
		}
		summary := BuiltinToolSummaries[info.Name]
		toolMap[info.Name] = &ToolDescriptor{
			Name:           info.Name,
			Label:          info.Name,
			Summary:        summary,
			Description:    info.Description,
			HasDescription: info.Description != "",
			Parameters:     json.RawMessage(info.Parameters),
			BuiltIn:        isBuiltin,
			LoadMode:       lm,
		}
	}

	// Evaluate enabled predicates for declared mountable tools.
	mountableEnabled := make(map[string]bool, len(mountables))
	for _, m := range mountables {
		if _, registered := toolInfoByName[m.name]; !registered {
			continue
		}
		isEnabled := true
		if m.enabled != nil {
			isEnabled = m.enabled(ctx)
		}
		mountableEnabled[m.name] = isEnabled
	}

	mountedNames := make(map[string]bool)
	var mountedCandidates []string
	specialNames := make([]string, 0, len(specials))
	specialSet := make(map[string]bool)

	// Start keptActive from current activeTools, then adjust mountable and
	// special tools according to transportActive and enabled/applies predicates.
	removeFromActive := make(map[string]bool)
	addToActive := make([]string, 0)
	addToActiveSet := make(map[string]bool)

	// 1. Handle explicitly declared Mountable tools.
	// Upstream (tools/index.ts:930-938, sdk.ts:4747-4754, 4835-4838):
	//   - When the session has an explicit tool list (`requestedTools !== undefined`,
	//     e.g. a subagent with frontmatter `tools:`), explicitly requested
	//     built-ins stay top-level (NOT mounted as bash commands), and unlisted
	//     built-ins are NEVER activated.
	//   - When a mountable tool's enabled predicate flips from false -> true
	//     (or at initial session_start), clear any prior external deactivation
	//     and activate it (mirroring omp's reconcileBuiltinTools delta.added,
	//     session/session-tools.ts:2554-2577).
	//   - When another party deactivated a previously-activated mountable tool
	//     while its enabled predicate stayed true, do NOT re-activate or mount it.
	nextManagedActive := make(map[string]bool)
	for _, m := range mountables {
		info, registered := toolInfoByName[m.name]
		if !registered {
			continue
		}
		if SpecialToolNames[m.name] {
			continue
		}
		desc := toolMap[m.name]
		enabledNow := mountableEnabled[m.name]
		enabledBefore := prevMountableEnabled[m.name]

		if hasExplicitList {
			// Explicit tool list (e.g. reviewer subagent): only tools listed in
			// explicitOrder may be active, and listed built-ins stay top-level
			// (never mounted as bash commands).
			if !explicitSet[m.name] || !enabledNow {
				if activeSet[m.name] {
					removeFromActive[m.name] = true
				}
				continue
			}
			// Listed AND enabled: activate at session_start (or when predicate
			// newly flips true) unless externally deactivated after we activated it.
			if enabledNow && !enabledBefore {
				delete(externallyDeactivated, m.name)
			}
			if activeSet[m.name] {
				nextManagedActive[m.name] = true
			} else if (fromSessionStart || !hasPrevSync || !enabledBefore) && !externallyDeactivated[m.name] {
				if !addToActiveSet[m.name] {
					addToActive = append(addToActive, m.name)
					addToActiveSet[m.name] = true
				}
				nextManagedActive[m.name] = true
			}
			continue
		}

		if !enabledNow {
			// Disabled by its area: never mounted and never active.
			removeFromActive[m.name] = true
			delete(externallyDeactivated, m.name)
			continue
		}
		if enabledNow && !enabledBefore {
			// Setting flipped off -> on: treat as newly added (reconcileBuiltinTools).
			delete(externallyDeactivated, m.name)
		}
		shouldBePresent := activeSet[m.name] || (!externallyDeactivated[m.name] && (fromSessionStart || !hasPrevSync || !enabledBefore))
		if !shouldBePresent {
			// Deactivated by a user or another extension: respect removal.
			continue
		}
		// Only mount tools that PiG's ExecuteTool can invoke (`codemode` or
		// `deferred` exposure). While mounted, keep them in PiG's active tool
		// list so direct model calls still dispatch (omp resolveFallbackTool,
		// sdk.ts:5228, agent-loop.ts:2978-2992); `PrepareLoadout` returns them
		// in `HiddenDeclarations` so PiG omits them from every provider
		// request and prompt before provider conversion.
		canExecInactive := info.Exposure == sdk.ToolExposureCodemode || info.Exposure == sdk.ToolExposureDeferred
		if transportActive && canExecInactive && IsMountableAsToolCommand(desc.Name, desc.LoadMode) {
			mountedNames[m.name] = true
			mountedCandidates = append(mountedCandidates, m.name)
		}
		if !activeSet[m.name] && !addToActiveSet[m.name] {
			addToActive = append(addToActive, m.name)
			addToActiveSet[m.name] = true
		}
		nextManagedActive[m.name] = true
	}

	// 2. Handle non-built-in (extension/MCP) tools registered with codemode/deferred
	// exposure that were not declared via Mountable. Direct tools stay top-level
	// because deactivating a direct tool makes it uncallable in PiG.
	// Respect external deactivation (only auto-activate newly seen tools that
	// were not previously managed and removed by another party).
	for _, info := range allTools {
		if _, declared := mountableByName[info.Name]; declared {
			continue
		}
		if BuiltinToolNames[info.Name] || SpecialToolNames[info.Name] || HiddenToolNames[info.Name] {
			continue
		}
		if info.Exposure != sdk.ToolExposureCodemode && info.Exposure != sdk.ToolExposureDeferred {
			continue
		}
		// Explicitly requested extension/MCP tools stay top-level (sdk.ts:4835-4838).
		if explicitSet[info.Name] {
			continue
		}
		desc := toolMap[info.Name]
		if transportActive && IsMountableAsToolCommand(desc.Name, desc.LoadMode) {
			shouldBePresent := activeSet[info.Name] || !externallyDeactivated[info.Name]
			if !shouldBePresent {
				continue
			}
			mountedNames[info.Name] = true
			mountedCandidates = append(mountedCandidates, info.Name)
			if !activeSet[info.Name] && !addToActiveSet[info.Name] {
				addToActive = append(addToActive, info.Name)
				addToActiveSet[info.Name] = true
			}
			nextManagedActive[info.Name] = true
		}
	}
	// 3. Handle special commands (resolve, reject, propose, report_issue):
	// never mounted in `# Tool Commands`; exposed in bash preamble when transport
	// is active, and kept INACTIVE in PiG's active tool list (omp's
	// resolveFallbackTool resolves mountedNames only, tools/tool-commands.ts:237-254,
	// sdk.ts:4301-4305,4755-4761 — direct model calls must answer "Tool <name> not found",
	// while bash runs reach them via ExecuteTool on codemode/deferred exposure).
	// Presented top-level only when transport is off and applies(ctx) is true.
	for _, sp := range specials {
		info, registered := toolInfoByName[sp.name]
		if !registered {
			continue
		}
		canExecInactive := info.Exposure == sdk.ToolExposureCodemode || info.Exposure == sdk.ToolExposureDeferred
		if transportActive {
			if canExecInactive || activeSet[sp.name] {
				if IsToolCommandName(sp.name) {
					specialNames = append(specialNames, sp.name)
					specialSet[sp.name] = true
				}
				if canExecInactive {
					removeFromActive[sp.name] = true
				}
			}
		} else {
			applies := sp.applies == nil || sp.applies(ctx)
			if applies {
				if !activeSet[sp.name] && !addToActiveSet[sp.name] {
					addToActive = append(addToActive, sp.name)
					addToActiveSet[sp.name] = true
				}
			} else {
				removeFromActive[sp.name] = true
			}
		}
	}
	// Also protect unregistered-in-specials built-in SpecialToolNames (e.g. propose
	// activated by plan mode) from ever being swallowed into mountedNames.

	// Sort mountedCandidates in omp presentation order: built-ins in BuiltinToolOrder
	// first (matching real omp's `ast_edit`, `debug`, `lsp`), then non-built-ins in
	// registry order.
	registryPos := make(map[string]int, len(allTools))
	for i, info := range allTools {
		registryPos[info.Name] = i
	}
	sort.SliceStable(mountedCandidates, func(i, j int) bool {
		ni, nj := mountedCandidates[i], mountedCandidates[j]
		bi, hasI := builtinOrderIndex[ni]
		bj, hasJ := builtinOrderIndex[nj]
		if hasI && hasJ {
			return bi < bj
		}
		if hasI != hasJ {
			return hasI
		}
		return registryPos[ni] < registryPos[nj]
	})

	// 4. At session_start, include conditional top-level tools registered via
	// RegisterAutoActivate (e.g. `find` when `find.enabled: "on"` or a native
	// judge is available) BEFORE the initial BUILTIN_TOOLS sort so they land at
	// their exact upstream BUILTIN_TOOLS position (`tools/index.ts:622-652`,
	// e.g. `find` immediately after `grep`) without needing a mid-session reset.
	// Explicit caller tool lists (`ExplicitToolOrder`) control their own membership.
	if fromSessionStart && len(explicitOrder) == 0 {
		for _, aa := range autoActivates {
			if _, registered := toolInfoByName[aa.name]; !registered {
				continue
			}
			if activeSet[aa.name] || addToActiveSet[aa.name] || removeFromActive[aa.name] {
				continue
			}
			if aa.enabled == nil || aa.enabled(ctx) {
				addToActive = append(addToActive, aa.name)
				addToActiveSet[aa.name] = true
			}
		}
	}

	keptActive := make([]string, 0, len(activeTools)+len(addToActive))
	for _, name := range activeTools {
		if !removeFromActive[name] {
			keptActive = append(keptActive, name)
		}
	}
	keptActive = append(keptActive, addToActive...)

	// Order active tools ONLY at session start, and ONLY when the caller did not
	// supply an explicit tool order (omp sorts only the default set:
	// tools/index.ts:622-652, 862-875; sdk.ts:4701-4762). Mid-session syncs
	// (mcp_servers_change, toolcmd.Sync, PromptDocs) never reorder or reset the
	// live active list, so an in-flight turn can never see an empty tool list.
	if fromSessionStart {
		if len(explicitOrder) > 0 {
			explicitPos := make(map[string]int, len(explicitOrder))
			for i, name := range explicitOrder {
				if _, seen := explicitPos[name]; !seen {
					explicitPos[name] = i
				}
			}
			sort.SliceStable(keptActive, func(i, j int) bool {
				pi, hasI := explicitPos[keptActive[i]]
				pj, hasJ := explicitPos[keptActive[j]]
				if hasI && hasJ {
					return pi < pj
				}
				if hasI != hasJ {
					return hasI
				}
				return false
			})
		} else {
			sort.SliceStable(keptActive, func(i, j int) bool {
				ni, nj := keptActive[i], keptActive[j]
				bi, hasI := builtinOrderIndex[ni]
				bj, hasJ := builtinOrderIndex[nj]
				if hasI && hasJ {
					return bi < bj
				}
				if hasI != hasJ {
					return hasI
				}
				return false
			})
		}
	}

	// activeCopy tracks tools advertised top-level (excluding mounted tools that
	// stay in PiG's active list for direct-call fallback and are hidden from
	// provider requests via PrepareLoadout.HiddenDeclarations).
	activeCopy := make(map[string]bool, len(keptActive))
	for _, name := range keptActive {
		if !mountedNames[name] && !specialSet[name] {
			activeCopy[name] = true
		}
	}

	newState := &State{
		Tools:        toolMap,
		MountedOrder: mountedCandidates,
		MountedNames: mountedNames,
		BuiltInNames: builtInNames,
		IsActive: func(name string) bool {
			return activeCopy[name]
		},
	}

	sess.mu.Lock()
	prevHadState := sess.state != nil
	prevTransport := sess.transportActive
	prevCommandsEnabled := sess.commandsEnabled
	sess.state = newState
	sess.specialNames = specialNames
	sess.specialSet = specialSet
	sess.active = activeCopy
	sess.transportActive = transportActive
	sess.commandsEnabled = commandsEnabled
	sess.explicitOrder = explicitOrder
	sess.explicitSet = explicitSet
	sess.managedActive = nextManagedActive
	sess.prevMountableEnabled = mountableEnabled
	sess.externallyDeactivated = externallyDeactivated
	if fromSessionStart || !sess.hasSessCtx {
		sess.sessionCtx = ctx
		sess.hasSessCtx = true
	}
	if len(mountedCandidates) == 0 && len(specialNames) == 0 && sess.listener != nil {
		l := sess.listener
		sess.listener = nil
		sess.mu.Unlock()
		l.Close()
	} else {
		sess.mu.Unlock()
	}
	transportChanged := prevHadState && (prevTransport != transportActive || prevCommandsEnabled != commandsEnabled)
	if applyActiveTools && (!stringSlicesEqual(activeTools, keptActive) || transportChanged) {
		// PiG's planToolLoadout preserves the existing order of already-active
		// tools on SetActiveTools (coding/session_loadout.go:178-189). Only at
		// session_start (when no agent turn is in flight and nothing has been
		// declared to a provider yet) may we clear with an empty list to
		// establish initial order. After session_start we never pass an empty
		// list, so no in-flight turn can ever observe an empty tool set.
		if fromSessionStart && len(keptActive) > 0 && needsActiveToolReorder(activeTools, keptActive) {
			ctx.SetActiveTools([]string{})
		}
		ctx.SetActiveTools(keptActive)
	}
}

// needsActiveToolReorder reports whether PiG's planToolLoadout rule (keep
// already-active tools in their current order, then append newly added tools;
// coding/session_loadout.go:178-189) would produce a different slice order than
// want. That happens whenever either (a) two kept tools are out of relative
// order, or (b) any newly added tool appears before a kept already-active tool
// in want (e.g. `find` inserted after `grep` or reviewer's `lsp`/`ast_grep`
// inserted before `web_search`/`wait`).
func needsActiveToolReorder(cur, want []string) bool {
	wantSet := make(map[string]bool, len(want))
	for _, name := range want {
		wantSet[name] = true
	}
	seen := make(map[string]bool, len(want))
	simulated := make([]string, 0, len(want))
	for _, name := range cur {
		if wantSet[name] && !seen[name] {
			seen[name] = true
			simulated = append(simulated, name)
		}
	}
	for _, name := range want {
		if !seen[name] {
			seen[name] = true
			simulated = append(simulated, name)
		}
	}
	return !stringSlicesEqual(simulated, want)
}

// PrepareLoadout implements sdk.ToolPrepareLoadoutFunc for the `bash` tool
// definition (`shell.PrepareLoadout = toolcmd.PrepareLoadout`). It returns
// `ToolLoadoutChanges{HiddenDeclarations: ...}` naming every declared tool that
// is mounted as a bash tool command, so PiG keeps those tools active and
// directly callable (matching omp's resolveFallbackTool, sdk.ts:5228,
// agent-loop.ts:2978-2992) while omitting their declarations from the
// transcript projection before EVERY provider converts messages (Anthropic
// messages + tool_addition, OpenAI Completions, OpenAI Responses +
// additional_tools, Google Gemini/Vertex, AWS Bedrock, pi-messages, faux) and
// passing them in `hiddenTools` (`coding/session_loadout.go:288-327`,
// `coding/session.go:566`).
//
// Because `sdk.ToolPrepareLoadoutFunc` receives no `sdk.Context` and multiple
// sessions can share one process, `PrepareLoadout` derives the hidden set
// strictly from `loadout` itself plus the process-wide immutable mountable
// registry (with zero cross-session state leakage):
//   - If `bash` is not in `loadout.Declared`, transport is inactive -> nil.
//   - If every live session has `tools.commands == false`, transport is off -> nil.
//   - Otherwise, a tool in `loadout.Declared` is hidden iff its exposure is
//     `codemode` or `deferred`, it is not a special or hidden tool, and
//     `IsMountableAsToolCommand(name, loadMode)` holds (the exact predicate
//     `syncSession` uses to mount tools while keeping them active). Because
//     disabled mountable tools are never active (hence not in `loadout.Declared`)
//     and special commands are kept inactive while transport is on (hence not in
//     `loadout.Declared`), every declared tool matching that predicate is an
//     enabled mounted command of THIS session's loadout.
//   - Note on multi-session mixed `tools.commands` override: when two live
//     sessions in the same process disagree on `tools.commands` (one true, one
//     false), `loadout` carries no session ID, so `PrepareLoadout` checks whether
//     any matching live session's `MountedNames` / `commandsEnabled` unambiguously
//     identifies the session from `loadout.Declared` before falling back to the
//     loadout-derived predicate.
//
// Integrator wiring: `shell.PrepareLoadout = toolcmd.PrepareLoadout`.
// upstream: packages/coding-agent/src/sdk.ts:4301-4324, 5228
// upstream: PiG coding/session_loadout.go:129-155, 288-327
func PrepareLoadout(loadout sdk.ToolLoadout) *sdk.ToolLoadoutChanges {
	return defaultRuntime.prepareLoadout(loadout)
}

func (r *cmdRuntime) prepareLoadout(loadout sdk.ToolLoadout) *sdk.ToolLoadoutChanges {
	if len(loadout.Declared) == 0 {
		return nil
	}
	hasBash := false
	for _, t := range loadout.Declared {
		if t.Name == "bash" {
			hasBash = true
			break
		}
	}
	if !hasBash {
		return nil
	}

	// Check live sessions to see if an exact session match tells us its
	// mounted set / explicit-pin / commandsEnabled state directly. This handles
	// both `tools.commands = false` and explicit-tool-list subagents (e.g.
	// reviewer with `tools: ..., lsp, ast_grep`) where explicitly listed
	// built-ins stay top-level rather than hidden.
	if matchedHidden, matched := r.hiddenFromLiveSessions(loadout.Declared); matched {
		if len(matchedHidden) == 0 {
			return nil
		}
		return &sdk.ToolLoadoutChanges{
			HiddenDeclarations: matchedHidden,
		}
	}

	mountables, _, _ := r.snapshotRegistrations()
	mountableSet := make(map[string]bool, len(mountables))
	for _, m := range mountables {
		mountableSet[m.name] = true
	}

	var hidden []string
	for _, t := range loadout.Declared {
		name := t.Name
		if SpecialToolNames[name] || HiddenToolNames[name] {
			continue
		}
		if loadout.GetExposure == nil {
			continue
		}
		exp := loadout.GetExposure(name)
		if exp != sdk.ToolExposureCodemode && exp != sdk.ToolExposureDeferred {
			continue
		}
		lm := BuiltinLoadMode(name)
		if mountableSet[name] {
			lm = LoadModeDiscoverable
		} else if BuiltinToolNames[name] {
			// Built-ins not declared Mountable follow BuiltinLoadMode; non-built-ins
			// with codemode/deferred exposure mount automatically (matching syncSession).
			continue
		}
		if IsMountableAsToolCommand(name, lm) {
			hidden = append(hidden, name)
		}
	}
	if len(hidden) == 0 {
		return nil
	}
	return &sdk.ToolLoadoutChanges{
		HiddenDeclarations: hidden,
	}
}

// hiddenFromLiveSessions resolves HiddenDeclarations from live session state
// when live sessions unambiguously determine the hidden set for `declared`.
//
// Because `sdk.ToolPrepareLoadoutFunc` receives no `sdk.Context`:
//  1. If no live sessions have synced yet (pre-session_start), returns (nil, false)
//     so `prepareLoadout` falls back to the loadout-derived default predicate.
//  2. Match live sessions whose active+mounted declared set equals `declared`.
//     If one or more sessions match and all matching sessions agree on the exact
//     hidden set (in `declared` order), return that set immediately — this
//     preserves explicit-list top-level built-ins (e.g. reviewer's `lsp` and
//     `ast_grep`) and `tools.commands = false` even when another concurrent
//     default session has `tools.commands = true` in the same process.
//  3. If no session matched `declared` (e.g. a foreign SetActiveTools mutated
//     active tools before syncSession ran) and every live session agrees that
//     `commandsEnabled == false`, return (nil, true) (hide nothing).
func (r *cmdRuntime) hiddenFromLiveSessions(declared []sdk.AgentTool) ([]string, bool) {
	r.mu.RLock()
	sessions := make([]*sessionState, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.mu.RUnlock()
	if len(sessions) == 0 {
		return nil, false
	}

	declSet := make(map[string]bool, len(declared))
	for _, t := range declared {
		declSet[t.Name] = true
	}

	anySynced := false
	allCommandsOff := true
	var matchedHidden [][]string
	for _, s := range sessions {
		s.mu.RLock()
		st := s.state
		on := s.commandsEnabled
		active := s.active
		s.mu.RUnlock()
		if st == nil {
			continue
		}
		anySynced = true
		if on {
			allCommandsOff = false
		}
		if sessionMatchesDeclared(declSet, active, st.MountedNames) {
			var h []string
			if on && len(st.MountedNames) > 0 {
				for _, t := range declared {
					if st.MountedNames[t.Name] {
						h = append(h, t.Name)
					}
				}
			}
			matchedHidden = append(matchedHidden, h)
		}
	}
	if !anySynced {
		return nil, false
	}
	if len(matchedHidden) > 0 {
		first := matchedHidden[0]
		allAgree := true
		for _, other := range matchedHidden[1:] {
			if !stringSlicesEqual(first, other) {
				allAgree = false
				break
			}
		}
		if allAgree {
			return first, true
		}
	}
	if allCommandsOff {
		return nil, true
	}
	return nil, false
}

func copyBoolMap(src map[string]bool) map[string]bool {
	if len(src) == 0 {
		return make(map[string]bool)
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		if v {
			out[k] = true
		}
	}
	return out
}

func sessionMatchesDeclared(declSet, active, mounted map[string]bool) bool {
	expected := len(active) + len(mounted)
	if len(declSet) != expected {
		return false
	}
	for name := range active {
		if !declSet[name] {
			return false
		}
	}
	for name := range mounted {
		if !declSet[name] {
			return false
		}
	}
	return true
}

// AdvertisedTools filters a session's active tool list by removing mounted and
// special tool commands (leaving only top-level advertised tools in order).
// Consumers such as `internal/prompt`, `internal/eval`, or `internal/modes`
// call this to obtain the exact advertised top-level tool set.
func AdvertisedTools(ctx sdk.Context, active []string) []string {
	hidden := make(map[string]bool)
	for _, name := range CommandNames(ctx) {
		hidden[name] = true
	}
	out := make([]string, 0, len(active))
	for _, name := range active {
		if !hidden[name] {
			out = append(out, name)
		}
	}
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// MountedNames returns the mounted tool names for ctx's session in presentation order.
func MountedNames(ctx sdk.Context) []string {
	key := sessionKeyFor(ctx)
	if key == "" {
		return nil
	}
	s := defaultRuntime.getSession(key)
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state == nil {
		return nil
	}
	return append([]string(nil), s.state.MountedOrder...)
}

// CommandNames returns all bash tool command names installed for ctx's session:
// mounted catalog commands followed by any active special commands.
// upstream: packages/coding-agent/src/exec/tool-command-host.ts:22-28
func CommandNames(ctx sdk.Context) []string {
	key := sessionKeyFor(ctx)
	if key == "" {
		return nil
	}
	s := defaultRuntime.getSession(key)
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	seen := make(map[string]bool)
	if s.state != nil {
		for _, n := range s.state.MountedOrder {
			if s.state.MountedNames[n] && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	for _, n := range s.specialNames {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// DefaultMountedBuiltinNames returns the built-in tools that mount as bash
// commands in omp v19.1.1 (all discoverable built-ins minus KeepTopLevel),
// in omp's BuiltinToolOrder.
func DefaultMountedBuiltinNames() []string {
	var out []string
	for _, name := range BuiltinToolOrder {
		if DiscoverableBuiltinToolNames[name] && IsMountableAsToolCommand(name, LoadModeDiscoverable) {
			out = append(out, name)
		}
	}
	return out
}

// executeMountedTool runs a validated mounted or special tool call exclusively
// through PiG's host `Context.ExecuteTool` so host JSON-Schema validation,
// `tool_call`/`tool_result` hooks, permission gates, `tool_execution_*` events,
// nested-call records, and `Details` all run identically to model-issued calls.
// executedToolDispatch carries the rendered shell result together with the
// dispatch metadata captured from PiG's host `Context.ExecuteTool` outcome.
type executedToolDispatch struct {
	result ToolCliResult
	callID string
	inner  any
}

// projectDispatchArgs mirrors omp's renderArgs projection in
// dispatchToolCommand (packages/coding-agent/src/tools/tool-commands.ts:451-455):
// `github` `pr_create` keeps only `op` and `repo` in result details (dropping
// large PR bodies), and harness-injected empty intent `"i"` is omitted so
// dispatch args match omp's validated renderer args.
func projectDispatchArgs(name string, args map[string]any, strippedIntent bool) map[string]any {
	if args == nil {
		return nil
	}
	if name == "github" {
		if op, _ := args["op"].(string); op == "pr_create" {
			return map[string]any{
				"op":   args["op"],
				"repo": args["repo"],
			}
		}
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if strippedIntent && k == "i" {
			continue
		}
		out[k] = v
	}
	return out
}

func (r *cmdRuntime) executeMountedTool(
	runCtx context.Context,
	sdkCtx sdk.Context,
	name string,
	args map[string]any,
) (executedToolDispatch, error) {
	if runCtx != nil && runCtx.Err() != nil {
		return executedToolDispatch{}, fmt.Errorf("%w: %v", ErrAborted, runCtx.Err())
	}
	var zero sdk.Context
	if sdkCtx == zero || sdkCtx.ToolCallID() == "" {
		return executedToolDispatch{
			result: ToolCliResult{
				Stdout:   "",
				Stderr:   fmt.Sprintf("%s: tool commands must run inside an active bash tool call\n", name),
				ExitCode: 1,
			},
		}, nil
	}

	outcome, err := sdkCtx.ExecuteTool(name, args, &sdk.ExecuteToolOptions{Signal: runCtx})
	if err != nil {
		if isAbortError(runCtx, err) {
			return executedToolDispatch{}, err
		}
		return executedToolDispatch{
			result: ToolCliResult{Stdout: "", Stderr: appendLine("", err.Error()), ExitCode: 1},
		}, nil
	}
	// Check runCtx after ExecuteTool returns: PiG's ExecuteTool wraps tool
	// aborts into an error outcome rather than returning a Go error.
	// upstream: packages/coding-agent/src/exec/tool-command-host.ts:123 (throwIfAborted)
	if runCtx != nil && runCtx.Err() != nil {
		return executedToolDispatch{}, fmt.Errorf("%w: %v", ErrAborted, runCtx.Err())
	}
	return executedToolDispatch{
		result: renderAgentToolResult(outcome.Result, outcome.IsError),
		callID: outcome.ToolCall.ID,
		inner:  outcome.Result.Details,
	}, nil
}

// renderAgentToolResult formats an AgentToolResult into shell stdout/stderr/exitCode
// matching omp's `renderToolResult` (exec/tool-command-host.ts:42-57): each text
// block gets a trailing newline appended if missing (never extra blank lines
// between blocks); isError routes text to stderr with exit code 1.
func renderAgentToolResult(result sdk.AgentToolResult, outcomeIsError bool) ToolCliResult {
	var text string
	for _, block := range result.Content {
		if block.Type == "text" {
			text = appendLine(text, block.Text)
		}
	}
	if outcomeIsError || result.IsError {
		return ToolCliResult{Stdout: "", Stderr: text, ExitCode: 1}
	}
	return ToolCliResult{Stdout: text, Stderr: "", ExitCode: 0}
}

func appendLine(output, text string) string {
	if text == "" {
		return output
	}
	if strings.HasSuffix(text, "\n") {
		return output + text
	}
	return output + text + "\n"
}

// sessionCliHost implements ToolCliHost backed by a live sessionState,
// resolving both mounted catalog commands and registered special commands,
// and capturing the last executed dispatch metadata for the caller to record.
type sessionCliHost struct {
	rt            *cmdRuntime
	sess          *sessionState
	sdkCtx        sdk.Context
	lastHasIntent bool
	lastDispatch  *DispatchRecord
}

func (h *sessionCliHost) lookup(name string) *ToolDescriptor {
	h.sess.mu.RLock()
	st := h.sess.state
	isSpecial := h.sess.specialSet[name]
	h.sess.mu.RUnlock()
	if st == nil {
		return nil
	}
	if isSpecial {
		return st.Tools[name]
	}
	return ResolveMountedToolCommand(st, name)
}

func (h *sessionCliHost) Definition(_ context.Context, name string) (ToolCliDefinition, error) {
	tool := h.lookup(name)
	if tool == nil {
		return ToolCliDefinition{}, errors.New("command is no longer available")
	}
	h.lastHasIntent = schemaHasHarnessIntent(tool.Parameters)
	return ToolCliDefinition{
		Name:        tool.Name,
		Description: tool.Description,
		HasDesc:     tool.HasDescription || tool.Description != "",
		Schema:      tool.Parameters,
	}, nil
}

func (h *sessionCliHost) Call(ctx context.Context, name string, args map[string]any) (ToolCliResult, error) {
	tool := h.lookup(name)
	if tool == nil {
		return ToolCliResult{}, errors.New("command is no longer available")
	}
	execRes, err := h.rt.executeMountedTool(ctx, h.sdkCtx, tool.Name, args)
	if err != nil {
		return ToolCliResult{}, err
	}
	var inner any
	if !isEmptyInnerDetails(execRes.inner) {
		inner = execRes.inner
	}
	rec := DispatchRecord{
		Tool:     tool.Name,
		Mode:     "execute",
		ExitCode: execRes.result.ExitCode,
		Args:     projectDispatchArgs(tool.Name, args, h.lastHasIntent),
		Inner:    inner,
		CallID:   execRes.callID,
	}
	h.lastDispatch = &rec
	return execRes.result, nil
}

// fallbackDispatch mirrors omp's createToolCommandHandler fallback record
// (`packages/coding-agent/src/exec/tool-command-host.ts:82-89`): used for
// `--help`/`-h` invocations, usage errors, and unavailable-tool failures where
// Call was never reached.
func fallbackDispatch(req ToolCliRequest) DispatchRecord {
	mode := "execute"
	if len(req.Argv) == 1 && (req.Argv[0] == "-h" || req.Argv[0] == "--help") {
		mode = "help"
	}
	return DispatchRecord{
		Tool:     req.Name,
		Mode:     mode,
		ExitCode: 1,
	}
}

// runAndRecordCli executes one tool-command invocation via RunToolCli and
// records its DispatchRecord in sess under outerCallID in invocation order,
// matching omp's createToolCommandHandler (`exec/tool-command-host.ts:75-138`).
func (r *cmdRuntime) runAndRecordCli(
	runCtx context.Context,
	sess *sessionState,
	sdkCtx sdk.Context,
	outerCallID string,
	req ToolCliRequest,
) (ToolCliResult, error) {
	rec, idx := sess.reserveDispatch(outerCallID)
	fb := fallbackDispatch(req)
	host := &sessionCliHost{
		rt:     r,
		sess:   sess,
		sdkCtx: sdkCtx,
	}
	res, err := RunToolCli(runCtx, req, host)
	if err != nil {
		if rec != nil {
			d := fb
			if host.lastDispatch != nil {
				d = *host.lastDispatch
			}
			d.ExitCode = 1
			rec.finish(idx, d)
		}
		return ToolCliResult{}, err
	}
	if rec != nil {
		d := fb
		if host.lastDispatch != nil {
			d = *host.lastDispatch
		}
		d.ExitCode = res.ExitCode
		rec.finish(idx, d)
	}
	return res, nil
}
