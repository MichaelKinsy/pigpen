// Package intent ports omp's tool-call intent tracing (`i` wire parameter
// injection, stripping before execution, dynamic intent derivation, label
// formatting, and per-session intent lookup) onto PiG's Go extension SDK.
//
// upstream:
//   - packages/wire/src/index.ts:412-418 (`INTENT_FIELD`, `toolIntentLabel`)
//   - packages/agent/src/agent-loop.ts:986-1135, 3198-3220
//   - packages/ai/src/utils/schema/json-schema-validator.ts:168-326
//   - packages/coding-agent/src/sdk.ts:492-499 (`intentTracingActive`)
//   - packages/utils/src/env.ts:493-505 (`isInteractiveHost`)
//   - packages/coding-agent/src/tools/{checkpoint,eval,resolve,report-tool-issue,think,wait,yield}.ts
//   - packages/coding-agent/src/goals/tools/goal-tool.ts
//   - packages/coding-agent/src/modes/controllers/event-controller.ts:187-199, 2129-2134
package intent

import (
	"maps"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"weak"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// Field is the wire parameter name injected for tool-call intent tracing.
// upstream: packages/wire/src/index.ts:412 (`INTENT_FIELD`)
const Field = "i"

// DefaultDescription is the schema description of the injected `i` property
// when neither `PI_INTENT_DESC` nor an explicit override is set.
// upstream: packages/agent/src/agent-loop.ts:987 (`DEFAULT_INTENT_FIELD_DESCRIPTION`)
const DefaultDescription = "concise intent"

// Mode controls how `i` is handled for a tool.
// upstream: packages/agent/src/types.ts:1247 (`AgentTool.intent`)
type Mode string

const (
	// ModeRequire injects `i` as a required string property (the default).
	ModeRequire Mode = "require"
	// ModeOptional injects `i` as an optional string property.
	ModeOptional Mode = "optional"
	// ModeOmit does not inject `i` into the wire schema.
	ModeOmit Mode = "omit"
)

// Deriver computes a fallback intent label from a tool's (stripped) arguments
// when the model did not supply `i` (or when the tool's mode is omit-with-fn).
// upstream: packages/agent/src/types.ts:1247
type Deriver func(args map[string]any) string

// Configured returns whether `tools.intentTracing` is explicitly configured
// for ctx's session (any settings layer or `PI_INTENT_TRACING`) and its boolean
// value. When nil or configured == false, [DefaultEnabled] falls back to
// interactive-TUI host detection inherited from the top-level session, matching
// omp's `intentTracingActive` (`packages/coding-agent/src/sdk.ts:492-499`).
var Configured func(ctx sdk.Context) (value bool, configured bool)

// ParentSession reports whether ctx belongs to a child subagent session and
// returns the top-level parent session ID. Assigned by the integrator (e.g.
// `agents.ParentSession`) so subagents inherit the top-level interactive-TUI
// decision when `tools.intentTracing` is unset.
// upstream: packages/coding-agent/src/sdk.ts:492-499, packages/utils/src/env.ts:493-505
var ParentSession func(ctx sdk.Context) (parentSessionID string, isSubagent bool)

// Enabled reports whether `tools.intentTracing` is active for ctx's session.
// Defaults to [DefaultEnabled]: an explicit configuration (via [Configured] or
// `PI_INTENT_TRACING`) wins; when unset, tracing is ON only when the top-level
// session runs in the interactive TUI (`ctx.HasUI() && ctx.Mode() == "tui"`),
// inherited by child sessions via [ParentSession], and OFF in print, JSON, RPC,
// ACP, and SDK sessions.
//
// Integrators may either assign [Configured] + [ParentSession] (recommended,
// keeping the cached interactive-host fallback) or replace Enabled directly.
//
// upstream: packages/coding-agent/src/sdk.ts:492-499 (`intentTracingActive`)
var Enabled = DefaultEnabled

// DefaultEnabled implements omp's `intentTracingActive`:
//   - Explicit `Configured(ctx)` wins (settings layers or `PI_INTENT_TRACING`).
//   - If `Configured` is nil, `PI_INTENT_TRACING` env var (`true`/`1`/`false`/`0`)
//     is still honoured directly.
//   - Otherwise, falls back to whether the top-level host session is the
//     interactive TUI (`ctx.HasUI() && ctx.Mode() == "tui"`), never RPC/print/json.
//     Child sessions inherit their top-level parent's cached interactive-host
//     decision via [ParentSession]. Decisions are cached per session ID and
//     released via `internal/lifecycle`.
//
// upstream: packages/coding-agent/src/sdk.ts:492-499, packages/utils/src/env.ts:493-505
func DefaultEnabled(ctx sdk.Context) bool {
	if Configured != nil {
		if v, ok := Configured(ctx); ok {
			return v
		}
	} else if v, ok := parseEnvBool("PI_INTENT_TRACING"); ok {
		return v
	}

	sid := sessionIDFor(ctx)
	if sid != "" {
		storeMu.RLock()
		if cached, ok := sessionEnabled[sid]; ok {
			storeMu.RUnlock()
			return cached
		}
		storeMu.RUnlock()
	}

	var decision bool
	if ParentSession != nil {
		if parentID, isSub := ParentSession(ctx); isSub && parentID != "" {
			storeMu.RLock()
			if parentVal, ok := sessionInteractive[parentID]; ok {
				decision = parentVal
				storeMu.RUnlock()
				cacheSessionDecision(sid, decision, false)
				return decision
			}
			storeMu.RUnlock()
		}
	}

	decision = isInteractiveTUIContext(ctx)
	cacheSessionDecision(sid, decision, true)
	return decision
}

func cacheSessionDecision(sid string, decision bool, recordTopLevel bool) {
	if sid == "" {
		return
	}
	ensureLifecycle()
	storeMu.Lock()
	if !releasedSessions[sid] {
		sessionEnabled[sid] = decision
		if recordTopLevel {
			sessionInteractive[sid] = decision
		}
	}
	storeMu.Unlock()
}

func isInteractiveTUIContext(ctx sdk.Context) bool {
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	var interactive bool
	func() {
		defer func() { _ = recover() }()
		interactive = ctx.HasUI() && ctx.Mode() == "tui"
	}()
	return interactive
}

func parseEnvBool(name string) (bool, bool) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// toolModes lists every omp tool whose intent mode differs from the default
// (`ModeRequire`), including tools with dynamic `intent(args)` functions
// (`resolveIntentMode` returns `ModeOmit` for function intents).
// upstream:
//   - packages/agent/src/agent-loop.ts:1115-1119 (`resolveIntentMode`)
//   - packages/coding-agent/src/tools/wait.ts:321 (`readonly intent = "optional"`)
//   - packages/coding-agent/src/tools/resolve.ts:342,359,377 (`resolve`, `reject`, `propose`: `"omit"`)
//   - packages/coding-agent/src/tools/report-tool-issue.ts:712 (`report_issue`: `"omit"`)
//   - packages/coding-agent/src/tools/think.ts:59 (`think`: `"omit"`)
//   - packages/coding-agent/src/tools/yield.ts:318 (`yield`: `"omit"`)
//   - packages/coding-agent/src/goals/tools/goal-tool.ts:71 (`goal`: `"omit"`)
//   - packages/coding-agent/src/advisor/advise-tool.ts:210 (`advise`: `"omit"`)
//   - packages/coding-agent/src/tools/eval.ts:386 (function -> `"omit"`)
//   - packages/coding-agent/src/tools/checkpoint.ts:62,98 (functions -> `"omit"`)
var toolModes = map[string]Mode{
	"wait":         ModeOptional,
	"resolve":      ModeOmit,
	"reject":       ModeOmit,
	"propose":      ModeOmit,
	"report_issue": ModeOmit,
	"think":        ModeOmit,
	"yield":        ModeOmit,
	"goal":         ModeOmit,
	"advise":       ModeOmit,
	"eval":         ModeOmit,
	"checkpoint":   ModeOmit,
	"rewind":       ModeOmit,
}

// toolDerivers ports every omp tool's `intent(args)` function.
// upstream:
//   - packages/coding-agent/src/tools/eval.ts:283-287, 386-390
//   - packages/coding-agent/src/tools/checkpoint.ts:62, 98
var toolDerivers = map[string]Deriver{
	"eval": func(args map[string]any) string {
		if title, ok := args["title"].(string); ok && title != "" {
			return title
		}
		language := "javascript"
		if raw, ok := args["language"].(string); ok {
			language = formatEvalInputLanguage(raw)
		}
		return "running " + language
	},
	"checkpoint": func(args map[string]any) string {
		if goal, ok := args["goal"].(string); ok && goal != "" {
			return "checkpointing: " + goal
		}
		return "checkpointing"
	},
	"rewind": func(_ map[string]any) string {
		return "rewinding"
	},
}

// formatEvalInputLanguage mirrors `formatEvalInputLanguage` in
// packages/coding-agent/src/tools/eval.ts:283-287.
func formatEvalInputLanguage(value string) string {
	switch value {
	case "py", "python":
		return "python"
	case "js", "javascript":
		return "javascript"
	default:
		return value
	}
}

// ModeFor returns the resolved intent mode for toolName (defaults to ModeRequire).
// upstream: packages/agent/src/agent-loop.ts:1115-1119 (`resolveIntentMode`)
func ModeFor(toolName string) Mode {
	if m, ok := toolModes[toolName]; ok {
		return m
	}
	return ModeRequire
}

// Derive returns the fallback intent derived by omp's per-tool `intent(args)`
// function for toolName, trimmed, or "" if the tool has none or returned empty.
// Panics inside a deriver are recovered so intent derivation can never break
// tool execution or UI rendering (upstream: agent-loop.ts:3216-3218).
func Derive(toolName string, args map[string]any) (derived string) {
	fn, ok := toolDerivers[toolName]
	if !ok || fn == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			derived = ""
		}
	}()
	return strings.TrimSpace(fn(args))
}

var (
	trailingPeriodsRE = regexp.MustCompile(`\s*\.+$`)
	intentLabelSepRE  = regexp.MustCompile(`:(?:\s|$)`)
)

// Normalize trims whitespace and drops trailing periods, matching omp's
// `extractIntent` (`packages/agent/src/agent-loop.ts:1130-1134`) and
// `normalizeIntent` (`packages/coding-agent/src/modes/controllers/event-controller.ts:187-195`).
func Normalize(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	trimmed = trailingPeriodsRE.ReplaceAllString(trimmed, "")
	return strings.TrimSpace(trimmed)
}

// Label returns the activity label before the first colon-space (or trailing
// colon) in an intent, preserving bare labels and embedded URL or `file:line`
// references.
// upstream: packages/wire/src/index.ts:415-418 (`toolIntentLabel`)
func Label(intent string) string {
	loc := intentLabelSepRE.FindStringIndex(intent)
	if loc != nil && loc[0] > 0 {
		if prefix := strings.TrimSpace(intent[:loc[0]]); prefix != "" {
			return prefix
		}
	}
	return intent
}

// WorkingLabel computes the working-line label for an intent string in omp's
// exact order: cut the label prefix first (`toolIntentLabel`), then normalize
// whitespace and trailing periods (`normalizeIntent`). For example,
// `"Running tests...: unit"` becomes `"Running tests"`.
//
// upstream: packages/coding-agent/src/modes/controllers/event-controller.ts:187-199 (`workingIntentLabel`)
func WorkingLabel(raw string) string {
	return Normalize(Label(raw))
}

// Extract splits a traced tool call's `i` field from args without mutating the
// input map, normalizing `i` (trimmed, trailing periods dropped).
// upstream: packages/agent/src/agent-loop.ts:1122-1135 (`extractIntent`)
func Extract(args map[string]any) (intent string, stripped map[string]any) {
	if args == nil {
		return "", nil
	}
	raw, has := args[Field]
	if !has {
		return "", args
	}
	stripped = make(map[string]any, len(args)-1)
	for k, v := range args {
		if k == Field {
			continue
		}
		stripped[k] = v
	}
	if s, ok := raw.(string); ok {
		intent = Normalize(s)
	}
	return intent, stripped
}

// Description returns the effective description for the injected `i` field:
// `PI_INTENT_DESC` if non-empty, else `DefaultDescription` ("concise intent").
// upstream: packages/agent/src/agent-loop.ts:1086
func Description() string {
	if desc := os.Getenv("PI_INTENT_DESC"); desc != "" {
		return desc
	}
	return DefaultDescription
}

// DisabledByEnv reports whether `PI_NO_INTENT=1` globally disables schema
// injection.
// upstream: packages/agent/src/agent-loop.ts:1085
func DisabledByEnv() bool {
	return os.Getenv("PI_NO_INTENT") == "1"
}

// PromptEnabled reports whether the system prompt should advertise the intent
// field for ctx's session. In omp, `PI_NO_INTENT=1` suppresses wire-schema
// injection only (`packages/agent/src/agent-loop.ts:1085`), while the system
// prompt reads `intentTracingActive(settings)` without checking `PI_NO_INTENT`
// (`packages/coding-agent/src/sdk.ts:4655`, `system-prompt.ts:1138-1139`).
//
// Integrators wiring `prompt.Intent` should call `intent.PromptEnabled(ctx)`
// rather than `intent.Enabled(ctx) && !intent.DisabledByEnv()`.
func PromptEnabled(ctx sdk.Context) bool {
	if Enabled == nil {
		return true
	}
	return Enabled(ctx)
}

// SchemaInjectionEnabled reports whether wire tool schemas should have `i`
// injected for ctx's session: both `Enabled(ctx)` and `!DisabledByEnv()`
// (`PI_NO_INTENT != "1"`).
//
// upstream: packages/agent/src/agent-loop.ts:1085, packages/coding-agent/src/sdk.ts:492-499
func SchemaInjectionEnabled(ctx sdk.Context) bool {
	if DisabledByEnv() {
		return false
	}
	return PromptEnabled(ctx)
}

// NotifySettingChange invalidates any cached intent-tracing decision for
// sessionID when `tools.intentTracing` changes mid-session (e.g. from
// `settings.OnChange`). Re-syncing schemas on the next `before_agent_start`
// (installed by [RegisterSync] and [RegisterLifecycle]) will then re-register
// tools with the updated `i` injection state from the next run, matching omp's
// `cfgToolCallSwitches.listen` (`packages/coding-agent/src/sdk.ts:5660-5666`).
//
// Passing an empty sessionID invalidates all cached sessions. Non-matching
// keys are ignored so this function can be registered directly with
// `settings.OnChange(intent.NotifySettingChange)`.
func NotifySettingChange(sessionID, key string) {
	if key != "" && key != "tools.intentTracing" {
		return
	}
	storeMu.Lock()
	if sessionID == "" {
		clear(sessionEnabled)
		clear(sessionSyncedState)
	} else {
		delete(sessionEnabled, sessionID)
		delete(sessionSyncedState, sessionID)
	}
	storeMu.Unlock()
}

// Package-level per-session intent state, keyed strictly by session ID and
// released through `internal/lifecycle`. One table for the whole process so
// repeated Register calls across PiG session instances never lose state or
// leak across sessions.
//
// `releasedSessions` prevents a late-finishing background call from recreating
// a session's map after `lifecycle.End(sessionID)` has run, until a new
// `session_start` reopens that session ID (e.g. after `/reload` or resume).
const maxEndedSessionTombstones = 4096

var (
	storeMu            sync.RWMutex
	sessionStore       = map[string]map[string]string{}
	sessionEnabled     = map[string]bool{}
	sessionInteractive = map[string]bool{}
	sessionSyncedState = map[string]bool{}
	// extDefs holds the unwrapped sdk.ToolDefinition registered via
	// RegisterSync on a given extension instance, keyed by weak pointer so an
	// extension instance that never receives session_start is still eligible
	// for garbage collection and cleaned up via runtime.AddCleanup.
	extDefs = map[weak.Pointer[sdk.Extension]]map[string]sdk.ToolDefinition{}
	// sessionDefs holds per-session unwrapped sdk.ToolDefinition variants
	// recorded by ToolForContext(ctx, def), keyed by session ID and released
	// through internal/lifecycle.
	sessionDefs = map[string]map[string]sdk.ToolDefinition{}
	// sessionExt maps a session ID to the extension instance(s) that started
	// on it so releaseSession can free extDefs and lifecycleHooked entries
	// immediately on session shutdown.
	sessionExt = map[string][]weak.Pointer[sdk.Extension]{}
	// lifecycleHooked ensures RegisterLifecycle installs its hooks at most
	// once per *sdk.Extension instance. Keyed by weak pointer and cleaned up
	// on GC or session shutdown so unstarted extensions never leak.
	lifecycleHooked  = map[weak.Pointer[sdk.Extension]]bool{}
	extCleanupArmed  = map[weak.Pointer[sdk.Extension]]bool{}
	releasedSessions = map[string]bool{}
	releasedOrder    []string
	lifecycleOnce    sync.Once
)

// armExtCleanupLocked registers a GC cleanup once per *sdk.Extension so
// extDefs and lifecycleHooked entries for an extension that never receives
// session_start are freed automatically once the extension is unreachable.
// Caller must hold storeMu.Lock().
func armExtCleanupLocked(e *sdk.Extension, wp weak.Pointer[sdk.Extension]) {
	if extCleanupArmed[wp] {
		return
	}
	extCleanupArmed[wp] = true
	runtime.AddCleanup(e, func(key weak.Pointer[sdk.Extension]) {
		storeMu.Lock()
		delete(extDefs, key)
		delete(lifecycleHooked, key)
		delete(extCleanupArmed, key)
		storeMu.Unlock()
	}, wp)
}

func ensureLifecycle() {
	lifecycleOnce.Do(func() {
		lifecycle.OnEnd("intent", releaseSession)
	})
}

func releaseSession(sessionID string) {
	if sessionID == "" {
		return
	}
	storeMu.Lock()
	delete(sessionStore, sessionID)
	delete(sessionEnabled, sessionID)
	delete(sessionInteractive, sessionID)
	delete(sessionSyncedState, sessionID)
	delete(sessionDefs, sessionID)
	if exts, ok := sessionExt[sessionID]; ok {
		for _, wp := range exts {
			delete(extDefs, wp)
			delete(lifecycleHooked, wp)
			delete(extCleanupArmed, wp)
		}
		delete(sessionExt, sessionID)
	}
	if !releasedSessions[sessionID] {
		releasedSessions[sessionID] = true
		releasedOrder = append(releasedOrder, sessionID)
		if len(releasedOrder) > maxEndedSessionTombstones {
			evict := releasedOrder[0]
			releasedOrder = releasedOrder[1:]
			delete(releasedSessions, evict)
		}
	}
	storeMu.Unlock()
}

// reopenSession clears a session's end tombstone and cached Enabled decision
// when `session_start` fires for that session ID (e.g. after `/reload` or
// resuming back to a previous session). It intentionally does NOT clear
// sessionSyncedState if a RegisterSync handler has already synced this session.
func reopenSession(sessionID string) {
	if sessionID == "" {
		return
	}
	storeMu.Lock()
	if releasedSessions[sessionID] {
		delete(releasedSessions, sessionID)
		if idx := slices.Index(releasedOrder, sessionID); idx >= 0 {
			releasedOrder = slices.Delete(releasedOrder, idx, idx+1)
		}
	}
	delete(sessionEnabled, sessionID)
	delete(sessionInteractive, sessionID)
	storeMu.Unlock()
}

// Empty sessionID, toolCallID, or intent is a no-op. If sessionID has already
// been released via `lifecycle.End`, Record does not recreate its table.
func Record(sessionID, toolCallID, value string) {
	if sessionID == "" || toolCallID == "" || value == "" {
		return
	}
	ensureLifecycle()
	storeMu.Lock()
	if releasedSessions[sessionID] {
		storeMu.Unlock()
		return
	}
	calls := sessionStore[sessionID]
	if calls == nil {
		calls = map[string]string{}
		sessionStore[sessionID] = calls
	}
	calls[toolCallID] = value
	storeMu.Unlock()
}

// deleteCall removes a single completed tool call's intent entry and cleans up
// the session bucket when empty.
func deleteCall(sessionID, toolCallID string) {
	if sessionID == "" || toolCallID == "" {
		return
	}
	storeMu.Lock()
	if calls := sessionStore[sessionID]; calls != nil {
		delete(calls, toolCallID)
		if len(calls) == 0 {
			delete(sessionStore, sessionID)
		}
	}
	storeMu.Unlock()
}

// For returns the recorded intent for toolCallID in ctx's session, or "" if
// none was recorded or the session ID cannot be resolved.
func For(ctx sdk.Context, toolCallID string) string {
	if toolCallID == "" {
		return ""
	}
	sid := sessionIDFor(ctx)
	if sid == "" {
		return ""
	}
	storeMu.RLock()
	val := sessionStore[sid][toolCallID]
	storeMu.RUnlock()
	return val
}

// FromEvent resolves the raw display intent for a `tool_execution_start` (or
// similar) event in ctx's session:
//  1. Returns "" when `Enabled(ctx)` is false.
//  2. Uses an explicit `event["intent"]` string if present (normalized).
//  3. Uses `event["args"]["i"]` if present (normalized).
//  4. Falls back to `For(ctx, toolCallID)` if previously recorded.
//  5. Falls back to omp's per-tool `Derive(toolName, args)` function (e.g.
//     `eval`, `checkpoint`, `rewind`).
//
// Consumers rendering a working indicator should use [WorkingLabelFromEvent]
// (or pass raw strings to [WorkingLabel]), which cuts the colon-space label
// BEFORE normalizing trailing periods, matching omp's `workingIntentLabel`.
func FromEvent(ctx sdk.Context, data map[string]any) string {
	if data == nil {
		return ""
	}
	if Enabled != nil && !Enabled(ctx) {
		return ""
	}
	if s, ok := data["intent"].(string); ok {
		if norm := Normalize(s); norm != "" {
			return norm
		}
	}
	toolName, _ := data["toolName"].(string)
	toolCallID, _ := data["toolCallId"].(string)
	args, _ := data["args"].(map[string]any)
	if args != nil {
		if raw, ok := args[Field].(string); ok {
			if norm := Normalize(raw); norm != "" {
				return norm
			}
		}
	}
	if toolCallID != "" {
		if stored := For(ctx, toolCallID); stored != "" {
			return stored
		}
	}
	if toolName != "" {
		if derived := Derive(toolName, args); derived != "" {
			return derived
		}
	}
	return ""
}

// WorkingLabelFromEvent resolves the working-indicator label for a
// `tool_execution_start` event, applying [WorkingLabel] (`Normalize(Label(raw))`)
// to the un-normalized `event["intent"]` / `event["args"]["i"]` (or fallback
// `For` / `Derive`).
//
// upstream: packages/coding-agent/src/modes/controllers/event-controller.ts:187-199, 2132-2134
func WorkingLabelFromEvent(ctx sdk.Context, data map[string]any) string {
	if data == nil {
		return ""
	}
	if Enabled != nil && !Enabled(ctx) {
		return ""
	}
	if s, ok := data["intent"].(string); ok {
		if lbl := WorkingLabel(s); lbl != "" {
			return lbl
		}
	}
	toolName, _ := data["toolName"].(string)
	toolCallID, _ := data["toolCallId"].(string)
	args, _ := data["args"].(map[string]any)
	if args != nil {
		if raw, ok := args[Field].(string); ok {
			if lbl := WorkingLabel(raw); lbl != "" {
				return lbl
			}
		}
	}
	if toolCallID != "" {
		if stored := For(ctx, toolCallID); stored != "" {
			return WorkingLabel(stored)
		}
	}
	if toolName != "" {
		if derived := Derive(toolName, args); derived != "" {
			return WorkingLabel(derived)
		}
	}
	return ""
}

// sessionIDFor resolves the session ID strictly from ctx without falling back
// to a shared process bucket.
func sessionIDFor(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	var id string
	func() {
		defer func() { _ = recover() }()
		if v, err := ctx.GetSessionID(); err == nil && v != "" {
			id = v
		}
	}()
	return id
}

// ResetForTests clears all recorded session state and re-installs the
// lifecycle cleanup hook. Intended for tests only.
func ResetForTests() {
	storeMu.Lock()
	sessionStore = map[string]map[string]string{}
	sessionEnabled = map[string]bool{}
	sessionInteractive = map[string]bool{}
	sessionSyncedState = map[string]bool{}
	extDefs = map[weak.Pointer[sdk.Extension]]map[string]sdk.ToolDefinition{}
	sessionDefs = map[string]map[string]sdk.ToolDefinition{}
	sessionExt = map[string][]weak.Pointer[sdk.Extension]{}
	lifecycleHooked = map[weak.Pointer[sdk.Extension]]bool{}
	extCleanupArmed = map[weak.Pointer[sdk.Extension]]bool{}
	releasedSessions = map[string]bool{}
	releasedOrder = nil
	storeMu.Unlock()
	lifecycle.OnEnd("intent", releaseSession)
}

// Tool wraps def with omp's intent tracing and omp-compatible argument
// validation:
//   - Injects `i` as the FIRST property on the wire schema (using
//     `*sdk.OrderedObject` so JSON encoding puts `"i"` first) according to
//     `ModeFor(def.Name)` (`require` or `optional`), unless `PI_NO_INTENT=1`,
//     the mode is `omit`, or the original schema already defines `i`.
//   - For every tool that does NOT own `i`, ALWAYS installs a `PrepareArguments`
//     wrapper (chaining `def.PrepareArguments` when present):
//   - The tool's own `PrepareArguments` never sees the harness `i`.
//   - For strict tools, runs omp's argument validation/normalization pipeline
//     (`validate.go`) before PiG's host validator answers; for lenient tools
//     (`glob`, `todo`, `ask`, `task`, `yield`), runs normalization with
//     `keepUnknownRootKeys: true` and passes normalized arguments on success or
//     raw arguments (minus sentinels) on failure.
//   - When `doInject` is true, returns `i` as the model's string, or `""` when
//     `i` was absent or not a string (preventing host validation from
//     rejecting missing/malformed `i`, preventing PiG's TypeBox coercer from
//     turning `null`/`5` into `"null"`/`"5"`, and keeping in-process callers
//     like `internal/eval` and `internal/toolcmd` working without passing `i`).
//   - When `doInject` is false (e.g. `omit`, function-intent, `PI_NO_INTENT=1`,
//     or `tools.intentTracing: false`), drops any stray `i` before host
//     validation so closed (`additionalProperties: false`) schemas do not
//     reject calls where the model emitted `i` out of habit.
//   - Strips `i` immediately before calling `def.Execute`, recording the
//     normalized intent (or derived fallback from `toolDerivers`) in the
//     per-session table for the duration of `Execute` and deleting the call's
//     entry via `defer` when `Execute` returns.
//   - When the schema itself already defines `i` (`SchemaDefinesProperty`),
//     leaves the schema, `PrepareArguments`, and `Execute` arguments untouched
//     (`i` belongs to the tool, never the harness), still deriving intent if a
//     deriver exists.
//
// Registration contract (for mid-session `tools.intentTracing` sync):
//   - Tool does NOT record def in any process-global table (so per-subagent
//     custom tools such as `yield` never leak into other sessions).
//   - Every area that registers a non-omit tool on `*sdk.Extension` with
//     `e.RegisterTool(intent.Tool(def))` MUST also call
//     `intent.RegisterSync(e, def)` so the extension tracks `def` for its
//     session and reconciles `i` on `session_start` and mid-session setting
//     changes.
//   - Any area that later replaces a tool for a specific session (e.g. in
//     `OnSessionStart` or `OnEvent`) MUST wrap the replacement with
//     `intent.ToolForContext(ctx, variantDef)`, which records that session's
//     exact variant (overriding the extension-level default for that session).
//   - Tools whose `ModeFor(name) == ModeOmit` (`resolve`, `reject`, `propose`,
//     `goal`, `checkpoint`, `rewind`, `yield`) never inject `i` and need not
//     call `RegisterSync`.
//
// Tool does not mutate def or def.Parameters in place.
func Tool(def sdk.ToolDefinition) sdk.ToolDefinition {
	return toolWithOptions(def, true)
}

// ToolForContext wraps def like [Tool], additionally honouring `Enabled(ctx)`
// so a `session_start` or `before_agent_start` handler can register a
// session-specific tool variant with or without `i`.
//
// Registration contract: call ToolForContext whenever registering or replacing
// a tool via `ctx.RegisterTool(...)`. It records `def` in the per-session table
// for ctx's session (released via `internal/lifecycle`) so [SyncSessionTools]
// re-registers that session's exact variant if `tools.intentTracing` toggles
// mid-session, without affecting any other session.
func ToolForContext(ctx sdk.Context, def sdk.ToolDefinition) sdk.ToolDefinition {
	if sid := sessionIDFor(ctx); sid != "" && def.Name != "" {
		recordSessionDef(sid, def)
	}
	inject := Enabled == nil || Enabled(ctx)
	return toolWithOptions(def, inject)
}
func recordExtDef(e *sdk.Extension, def sdk.ToolDefinition) {
	if e == nil || def.Name == "" {
		return
	}
	wp := weak.Make(e)
	storeMu.Lock()
	armExtCleanupLocked(e, wp)
	m := extDefs[wp]
	if m == nil {
		m = map[string]sdk.ToolDefinition{}
		extDefs[wp] = m
	}
	m[def.Name] = def
	storeMu.Unlock()
}

func recordSessionDef(sid string, def sdk.ToolDefinition) {
	if sid == "" || def.Name == "" {
		return
	}
	ensureLifecycle()
	storeMu.Lock()
	if !releasedSessions[sid] {
		m := sessionDefs[sid]
		if m == nil {
			m = map[string]sdk.ToolDefinition{}
			sessionDefs[sid] = m
		}
		m[def.Name] = def
	}
	storeMu.Unlock()
}

func bindSessionExt(sid string, wp weak.Pointer[sdk.Extension]) {
	if sid == "" || wp == (weak.Pointer[sdk.Extension]{}) {
		return
	}
	storeMu.Lock()
	exts := sessionExt[sid]
	if !slices.Contains(exts, wp) {
		sessionExt[sid] = append(exts, wp)
	}
	storeMu.Unlock()
}

// SyncSessionTools reconciles registered tool schemas with the current
// `SchemaInjectionEnabled(ctx)` state for ctx's session. When the effective
// injection state differs from the last synced state for this session (or
// has been invalidated by [NotifySettingChange]), every tool tracked for THIS
// session via [ToolForContext] or [RegisterSync] is re-registered via
// `ctx.RegisterTool`.
//
// Called automatically on `before_agent_start` when [RegisterLifecycle] or
// [RegisterSync] is installed, and also exported so integrators can invoke it
// directly when wiring custom hooks.
//
// upstream: packages/coding-agent/src/sdk.ts:5660-5666
func SyncSessionTools(ctx sdk.Context) {
	syncSessionToolsFor(weak.Pointer[sdk.Extension]{}, ctx)
}

func syncSessionToolsFor(wp weak.Pointer[sdk.Extension], ctx sdk.Context) {
	sid := sessionIDFor(ctx)
	wantInject := SchemaInjectionEnabled(ctx)

	storeMu.Lock()
	if sid != "" {
		if prev, known := sessionSyncedState[sid]; known && prev == wantInject {
			storeMu.Unlock()
			return
		}
		sessionSyncedState[sid] = wantInject
	}
	merged := make(map[string]sdk.ToolDefinition)
	if wp != (weak.Pointer[sdk.Extension]{}) {
		if m := extDefs[wp]; len(m) > 0 {
			for k, d := range m {
				merged[k] = d
			}
		}
	} else if sid != "" {
		for _, boundExt := range sessionExt[sid] {
			if m := extDefs[boundExt]; len(m) > 0 {
				for k, d := range m {
					merged[k] = d
				}
			}
		}
	}
	if sid != "" {
		if m := sessionDefs[sid]; len(m) > 0 {
			for k, d := range m {
				merged[k] = d
			}
		}
	}
	defs := make([]sdk.ToolDefinition, 0, len(merged))
	for _, d := range merged {
		defs = append(defs, d)
	}
	storeMu.Unlock()

	if len(defs) == 0 {
		return
	}
	for _, def := range defs {
		ctx.RegisterTool(toolWithOptions(def, wantInject))
	}
}

// RegisterLifecycle installs the package-level `internal/lifecycle` cleanup
// handler under the name `"intent"`, hooks `session_start` on e to clear any
// prior tombstone and cached `Enabled` decision for the starting session ID
// (needed across `/reload` and session resume where the same session ID
// continues on a fresh extension instance), hooks `before_agent_start` to
// reconcile schemas when `tools.intentTracing` changes mid-session, and hooks
// `session_shutdown` on e to call `lifecycle.End(sessionID)`. Safe to call
// repeatedly: hooks are installed at most once per `*sdk.Extension`.
func RegisterLifecycle(e *sdk.Extension) {
	ensureLifecycle()
	if e == nil {
		return
	}
	wp := weak.Make(e)
	storeMu.Lock()
	if lifecycleHooked[wp] {
		storeMu.Unlock()
		return
	}
	armExtCleanupLocked(e, wp)
	lifecycleHooked[wp] = true
	storeMu.Unlock()

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := sessionIDFor(ctx); sid != "" {
			reopenSession(sid)
			bindSessionExt(sid, wp)
			wantInject := SchemaInjectionEnabled(ctx)
			storeMu.Lock()
			if _, alreadySet := sessionSyncedState[sid]; !alreadySet {
				sessionSyncedState[sid] = wantInject
			}
			storeMu.Unlock()
		}
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		syncSessionToolsFor(wp, ctx)
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := sessionIDFor(ctx); sid != "" {
			lifecycle.End(sid)
		}
		return nil, nil
	})
}

func toolWithOptions(def sdk.ToolDefinition, allowInject bool) sdk.ToolDefinition {
	ensureLifecycle()

	out := def
	ownsIntent := SchemaDefinesProperty(def.Parameters, Field)
	mode := ModeFor(def.Name)
	doInject := allowInject && !DisabledByEnv() && mode != ModeOmit && !ownsIntent

	if doInject && def.Parameters != nil {
		out.Parameters = InjectSchema(def.Parameters, mode, Description())
	}

	// For every tool that does not own `i`, install PrepareArguments (chaining
	// any tool-owned PrepareArguments) before PiG's host schema validation
	// (`agent/tool_execution.go:376-402`):
	//   - Inner PrepareArguments never sees `i`.
	//   - When `doInject` is true, always attach `i` as the model's string or ""
	//     when absent or non-string, matching omp's strip-before-validate
	//     semantics (`agent-loop.ts:3202-3226`) and preventing TypeBox coercion
	//     of `null`/`5` to `"null"`/`"5"`.
	//   - When `doInject` is false (omit, function-intent, PI_NO_INTENT=1, or
	//     tools.intentTracing off), drop any stray `i` before validation so
	//     closed schemas (`additionalProperties: false`) never fail on `i`.
	if !ownsIntent {
		origPrepare := def.PrepareArguments
		toolName := def.Name
		origSchema := def.Parameters
		out.PrepareArguments = func(params map[string]any) (map[string]any, error) {
			var intentStr string
			hasIntent := false
			withoutIntent := params
			if params != nil {
				if rawIntent, ok := params[Field]; ok {
					hasIntent = true
					if s, ok := rawIntent.(string); ok {
						intentStr = s
					}
					withoutIntent = make(map[string]any, len(params)-1)
					for k, v := range params {
						if k == Field {
							continue
						}
						withoutIntent[k] = v
					}
				}
			}

			prepared := withoutIntent
			if IsLenientArgValidation(toolName) {
				// Upstream (packages/agent/src/tool-arguments.ts:29-42):
				// run validateToolArguments with keepUnknownRootKeys=true.
				// On validation success, pass the NORMALIZED arguments (with
				// unknown root keys still present) to the tool's own
				// PrepareArguments so tools like `glob` can reject unknown keys
				// in their own words. After origPrepare succeeds, drop any
				// remaining undeclared root keys on closed schemas before PiG's
				// host validator runs (matching omp where ask/todo/task ignore
				// extra root keys instead of failing with PiG's `unknown field`).
				// On validation failure, pass raw arguments (minus sentinels)
				// to origPrepare so the tool owns its refusal.
				validOK := false
				if origSchema != nil && withoutIntent != nil {
					if validated, _, err := validateAndNormalizeArgsOpts(toolName, origSchema, withoutIntent, validateOpts{keepUnknownRootKeys: true}); err == nil {
						prepared = withoutSentinels(validated)
						validOK = true
					} else {
						prepared = withoutSentinels(withoutIntent)
					}
				}
				if origPrepare != nil {
					var err error
					prepared, err = origPrepare(prepared)
					if err != nil {
						return nil, err
					}
				}
				if validOK && origSchema != nil && prepared != nil {
					prepared = dropUndeclaredRootKeysIfClosed(origSchema, prepared)
				}
			} else {
				if origPrepare != nil {
					var err error
					prepared, err = origPrepare(withoutIntent)
					if err != nil {
						return nil, err
					}
				}
				if origSchema != nil {
					validated, _, err := validateAndNormalizeArgs(toolName, origSchema, prepared)
					if err != nil {
						return nil, err
					}
					prepared = validated
				}
			}
			if !doInject {
				if !hasIntent && origPrepare == nil && prepared == nil {
					return nil, nil
				}
				return prepared, nil
			}
			if prepared == nil {
				return map[string]any{Field: intentStr}, nil
			}
			if _, already := prepared[Field]; already {
				return prepared, nil
			}
			// Clone before writing `Field` so we never mutate `params` or a
			// map owned by `origPrepare`.
			reattached := maps.Clone(prepared)
			reattached[Field] = intentStr
			return reattached, nil
		}
	}

	if origExec := def.Execute; origExec != nil {
		toolName := def.Name
		out.Execute = func(ctx sdk.Context, params map[string]any) (any, error) {
			tracingEnabled := Enabled == nil || Enabled(ctx)
			execParams := params
			var captured string

			if ownsIntent {
				// upstream: agent-loop.ts:3202-3206 — schema-owned `i` is a
				// tool argument, never stripped and never treated as harness
				// intent, though a deriver function may still run.
				if tracingEnabled {
					captured = Derive(toolName, execParams)
				}
			} else {
				var extracted string
				extracted, execParams = Extract(params)
				if tracingEnabled {
					if extracted != "" {
						captured = extracted
					} else {
						captured = Derive(toolName, execParams)
					}
				}
			}

			var recSID, recCallID string
			if tracingEnabled && captured != "" {
				if sid := sessionIDFor(ctx); sid != "" {
					if callID := ctx.ToolCallID(); callID != "" {
						Record(sid, callID, captured)
						recSID, recCallID = sid, callID
					}
				}
			}
			if recSID != "" && recCallID != "" {
				defer deleteCall(recSID, recCallID)
			}
			return origExec(ctx, execParams)
		}
	}

	return out
}

// RegisterSync registers defs as the extension-level tracked tools for e and
// installs a `session_start` hook on e that binds e to the starting session
// and re-registers defs without `i` whenever `SchemaInjectionEnabled(ctx)` is
// false (e.g. `tools.intentTracing: false` or non-interactive host such as
// print/RPC/JSON).
//
// Registration contract: every area that registers an injectable tool via
// `e.RegisterTool(intent.Tool(def))` MUST call `intent.RegisterSync(e, def)`
// (even if the area also replaces the tool conditionally in `OnSessionStart`
// via [ToolForContext]). Definitions tracked under e are released when the
// session bound to e ends via `internal/lifecycle`.
//
// Because PiG runs a fresh extension instance per session and
// `ctx.RegisterTool` replaces a tool definition in place and refreshes the
// session's tool registry (`coding/extension/host/subprocess/tool_registration.go:14-43`),
// this achieves true per-session schema control.
func RegisterSync(e *sdk.Extension, defs ...sdk.ToolDefinition) {
	if e == nil || len(defs) == 0 {
		return
	}
	RegisterLifecycle(e)
	wp := weak.Make(e)
	snapshot := slices.Clone(defs)
	for _, def := range snapshot {
		recordExtDef(e, def)
	}
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		sid := sessionIDFor(ctx)
		if sid != "" {
			reopenSession(sid)
			bindSessionExt(sid, wp)
		}
		wantInject := SchemaInjectionEnabled(ctx)
		if sid != "" {
			storeMu.Lock()
			sessionSyncedState[sid] = wantInject
			storeMu.Unlock()
		}
		if wantInject {
			return nil, nil
		}
		for _, def := range snapshot {
			ctx.RegisterTool(toolWithOptions(def, false))
		}
		return nil, nil
	})
}

// TrackedToolNames returns the sorted list of tool names currently tracked for
// sessionID (combining extension-level RegisterSync definitions bound to
// sessionID and per-session ToolForContext definitions). Intended for tests
// that assert every injectable tool registered on a session is tracked.
func TrackedToolNames(sessionID string) []string {
	if sessionID == "" {
		return nil
	}
	storeMu.RLock()
	seen := make(map[string]bool)
	for _, boundExt := range sessionExt[sessionID] {
		for name := range extDefs[boundExt] {
			seen[name] = true
		}
	}
	for name := range sessionDefs[sessionID] {
		seen[name] = true
	}
	storeMu.RUnlock()
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
