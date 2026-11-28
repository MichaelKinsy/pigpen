// Package ctxnotes implements omp's opt-in experimental persistent context
// notebook (`context_notes`) and context-window rollover signal (`new_context`),
// gated by `compaction.experimentalContextManagement` (default false).
//
// upstream:
//   - packages/coding-agent/src/tools/context-notes.ts:1-184
//   - packages/coding-agent/src/session/context-notes.ts:1-59
//   - packages/coding-agent/src/session/context-settings.ts:118-128
//   - packages/coding-agent/src/prompts/tools/{context-notes,context-notes-text,new-context}.md
//   - packages/coding-agent/src/prompts/system/context-notes.md
package ctxnotes

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

const (
	// SettingExperimentalContextManagement gates `context_notes`, `new_context`,
	// and `<experimental-context-notes>` system prompt injection. Default: false.
	// upstream: packages/coding-agent/src/session/context-settings.ts:118-128
	SettingExperimentalContextManagement = "compaction.experimentalContextManagement"

	// EntryType is the custom session-entry type for persisted context notes.
	// upstream: packages/coding-agent/src/session/context-notes.ts:5
	EntryType = "experimental_context_notes"

	// ResetBoundaryType is the upstream reset_boundary session-entry type.
	// When PiG appends custom entries on rollover, it records a custom entry of
	// customType "reset_boundary" so backward scans stop at the rollover point.
	// upstream: packages/coding-agent/src/session/context-notes.ts:44
	ResetBoundaryType = "reset_boundary"

	// MaxContextNotesBytes is the hard UTF-8 byte cap for the notebook (16 KiB).
	// upstream: packages/coding-agent/src/session/context-notes.ts:6
	MaxContextNotesBytes = 16384

	emptyNotesMessage = "No context notes are stored for this session branch."
)

//go:embed prompts/context-notes.md
var contextNotesDescription string

//go:embed prompts/context-notes-text.md
var contextNotesTextDescriptionTemplate string

//go:embed prompts/new-context.md
var newContextDescription string

//go:embed prompts/system-context-notes.md
var systemContextNotesTemplate string

// Setting reads a raw setting value for ctx.
// Wired by wiring.go -> settings.Any. Defaults to nil (disabled).
var Setting func(ctx sdk.Context, key string) any

// IsSubagent reports whether ctx belongs to a subagent session (taskDepth > 0).
// Wired by wiring.go -> agents.IsSubagent. Defaults to nil (false).
var IsSubagent func(ctx sdk.Context) bool

// ContextNotesEntry is the persisted payload stored in `experimental_context_notes`.
// upstream: packages/coding-agent/src/session/context-notes.ts:8-11
type ContextNotesEntry struct {
	Version int    `json:"version"`
	Text    string `json:"text"`
}

// ContextNotesRevision is the resolved latest notebook revision for a branch.
// upstream: packages/coding-agent/src/session/context-notes.ts:13-16
type ContextNotesRevision struct {
	Text    string
	EntryID string
}

type sessionNotesState struct {
	writeMu           sync.Mutex
	loaded            bool
	hasNotes          bool
	revision          ContextNotesRevision
	rolloverRequested bool
}

var (
	stateMu       sync.Mutex
	sessionStates = map[string]*sessionNotesState{}

	closingContextNotesTagRe = regexp.MustCompile(`(?i)</experimental-context-notes`)
)

func neutralizeContextNotesTag(text string) string {
	return closingContextNotesTagRe.ReplaceAllStringFunc(text, func(m string) string {
		return "&lt;" + m[1:]
	})
}

// IsEnabled reports whether `compaction.experimentalContextManagement` is
// explicitly enabled (`true`) for ctx. Default is false.
// upstream: packages/coding-agent/src/tools/context-notes.ts:45-48
func IsEnabled(ctx sdk.Context) bool {
	if Setting == nil {
		return false
	}
	b, ok := Setting(ctx, SettingExperimentalContextManagement).(bool)
	return ok && b
}

func sessionIDFor(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	var sid string
	func() {
		defer func() { _ = recover() }()
		if id, err := ctx.GetSessionID(); err == nil {
			sid = strings.TrimSpace(id)
		}
	}()
	return sid
}

func getOrCreateStateLocked(sid string) *sessionNotesState {
	st := sessionStates[sid]
	if st == nil {
		st = &sessionNotesState{}
		sessionStates[sid] = st
	}
	return st
}

func cleanupSession(sid string) {
	if sid == "" {
		return
	}
	stateMu.Lock()
	delete(sessionStates, sid)
	stateMu.Unlock()
}

func invalidateSessionCache(ctx sdk.Context) {
	sid := sessionIDFor(ctx)
	if sid == "" {
		return
	}
	stateMu.Lock()
	if st := sessionStates[sid]; st != nil {
		st.loaded = false
	}
	stateMu.Unlock()
}

// ── Schemas & Tool Definitions ───────────────────────────────────────────────

func contextNotesTextDescription() string {
	return strings.TrimSpace(strings.ReplaceAll(
		contextNotesTextDescriptionTemplate,
		"{{maxBytes}}",
		strconv.Itoa(MaxContextNotesBytes),
	))
}

// ContextNotesSchema returns the JSON Schema for `context_notes`.
// `text` is optional so omitting it reads without modifying notes, while
// supplying `""` clears them.
// upstream: packages/coding-agent/src/tools/context-notes.ts:24-26,87-90
func ContextNotesSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": contextNotesTextDescription(),
			},
		},
		"additionalProperties": false,
	}
}

// NewContextSchema returns the JSON Schema for `new_context`.
// upstream: packages/coding-agent/src/tools/context-notes.ts:28
func NewContextSchema() sdk.Schema {
	return sdk.Schema{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
}

func contextNotesToolDef() sdk.ToolDefinition {
	defaultOff := false
	return schemaorder.Apply(sdk.ToolDefinition{
		Name:          "context_notes",
		Label:         "Context Notes",
		Description:   strings.TrimSpace(contextNotesDescription),
		Parameters:    ContextNotesSchema(),
		DefaultActive: &defaultOff,
		Execute:       executeContextNotesRaw,
	})
}

func newContextToolDef() sdk.ToolDefinition {
	defaultOff := false
	return schemaorder.Apply(sdk.ToolDefinition{
		Name:          "new_context",
		Label:         "New Context",
		Description:   strings.TrimSpace(newContextDescription),
		Parameters:    NewContextSchema(),
		DefaultActive: &defaultOff,
		Execute:       executeNewContextRaw,
	})
}

// ── Entry Scanning & Persistence ─────────────────────────────────────────────

type rawSessionEntry struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data"`
}

// ParseContextNotesFromEntries scans raw session JSON entries backwards until a
// `reset_boundary` entry (native or custom) is reached, returning the latest
// valid `experimental_context_notes` revision. Malformed entries are ignored.
// upstream: packages/coding-agent/src/session/context-notes.ts:18-49
func ParseContextNotesFromEntries(entries []json.RawMessage) (ContextNotesRevision, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		var entry rawSessionEntry
		if err := json.Unmarshal(entries[i], &entry); err != nil {
			continue
		}
		if entry.Type == ResetBoundaryType || (entry.Type == "custom" && entry.CustomType == ResetBoundaryType) {
			return ContextNotesRevision{}, false
		}
		if entry.Type != "custom" || entry.CustomType != EntryType {
			continue
		}
		rev, ok := decodeContextNotesData(entry.Data, entry.ID)
		if !ok {
			continue
		}
		return rev, true
	}
	return ContextNotesRevision{}, false
}

func decodeContextNotesData(raw json.RawMessage, entryID string) (ContextNotesRevision, bool) {
	if len(raw) == 0 {
		return ContextNotesRevision{}, false
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) != 2 {
		return ContextNotesRevision{}, false
	}
	verVal, hasVer := obj["version"]
	textVal, hasText := obj["text"]
	if !hasVer || !hasText {
		return ContextNotesRevision{}, false
	}
	verNum, ok := verVal.(float64)
	if !ok || verNum != 1 {
		return ContextNotesRevision{}, false
	}
	textStr, ok := textVal.(string)
	if !ok || len([]byte(textStr)) > MaxContextNotesBytes {
		return ContextNotesRevision{}, false
	}
	return ContextNotesRevision{Text: textStr, EntryID: entryID}, true
}

func filterEntriesToActiveBranch(rawEntries []json.RawMessage, branchEntries []sdk.BranchEntry) []json.RawMessage {
	if len(branchEntries) == 0 {
		return rawEntries
	}
	byID := make(map[string]json.RawMessage, len(rawEntries))
	for _, raw := range rawEntries {
		var env struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &env) == nil && env.ID != "" {
			byID[env.ID] = raw
		}
	}
	ordered := make([]json.RawMessage, 0, len(branchEntries))
	for _, be := range branchEntries {
		if be.ID == "" {
			continue
		}
		if raw, ok := byID[be.ID]; ok {
			ordered = append(ordered, raw)
		}
	}
	return ordered
}

func loadLatestNotes(ctx sdk.Context) (ContextNotesRevision, bool) {
	sid := sessionIDFor(ctx)
	if sid != "" {
		stateMu.Lock()
		st := sessionStates[sid]
		if st != nil && st.loaded {
			rev, ok := st.revision, st.hasNotes
			stateMu.Unlock()
			return rev, ok
		}
		stateMu.Unlock()
	}

	var entries []json.RawMessage
	var zero sdk.Context
	if ctx != zero {
		func() {
			defer func() { _ = recover() }()
			rawEntries, err := ctx.GetEntries()
			if err != nil {
				return
			}
			branchEntries, bErr := ctx.GetBranch()
			if bErr == nil && len(branchEntries) > 0 {
				entries = filterEntriesToActiveBranch(rawEntries, branchEntries)
			} else {
				entries = rawEntries
			}
		}()
	}

	rev, ok := ParseContextNotesFromEntries(entries)
	if sid != "" {
		stateMu.Lock()
		st := getOrCreateStateLocked(sid)
		if !st.loaded {
			st.loaded = true
			st.hasNotes = ok
			st.revision = rev
		} else {
			rev, ok = st.revision, st.hasNotes
		}
		stateMu.Unlock()
	}
	return rev, ok
}

func saveNotes(ctx sdk.Context, text string) (ContextNotesRevision, error) {
	sid := sessionIDFor(ctx)
	var st *sessionNotesState
	if sid != "" {
		stateMu.Lock()
		st = getOrCreateStateLocked(sid)
		stateMu.Unlock()
		st.writeMu.Lock()
		defer st.writeMu.Unlock()
	}

	entry := ContextNotesEntry{
		Version: 1,
		Text:    text,
	}
	var zero sdk.Context
	if ctx != zero {
		var appendErr error
		func() {
			defer func() { _ = recover() }()
			appendErr = ctx.AppendEntry(EntryType, entry)
		}()
		if appendErr != nil {
			return ContextNotesRevision{}, fmt.Errorf("persist context notes: %w", appendErr)
		}
	}

	rev := ContextNotesRevision{
		Text:    text,
		EntryID: fmt.Sprintf("ctxnotes-%d", time.Now().UTC().UnixNano()),
	}
	if st != nil {
		stateMu.Lock()
		st.loaded = true
		st.hasNotes = true
		st.revision = rev
		stateMu.Unlock()
	}
	return rev, nil
}

// ── Tool Execution ───────────────────────────────────────────────────────────

// ExecuteContextNotes runs the `context_notes` tool directly (exported for tests).
// upstream: packages/coding-agent/src/tools/context-notes.ts:100-150
func ExecuteContextNotes(ctx sdk.Context, raw map[string]any) (*sdk.ToolResult, error) {
	if !IsEnabled(ctx) {
		return nil, errors.New("Experimental context management is disabled.")
	}

	rawText, hasText := raw["text"]
	if !hasText {
		rev, ok := loadLatestNotes(ctx)
		if !ok {
			return &sdk.ToolResult{
				Content: emptyNotesMessage,
				Details: map[string]any{"text": ""},
			}, nil
		}
		details := map[string]any{"text": rev.Text}
		if rev.EntryID != "" {
			details["entryId"] = rev.EntryID
		}
		return &sdk.ToolResult{
			Content: rev.Text,
			Details: details,
		}, nil
	}

	text, ok := rawText.(string)
	if !ok {
		return nil, errors.New("context_notes text must be a string.")
	}
	byteLen := len([]byte(text))
	if byteLen > MaxContextNotesBytes {
		return nil, fmt.Errorf(
			"Context notes are %d bytes; the limit is %d UTF-8 bytes. Shorten the notebook and use history://current/full to recover raw detail.",
			byteLen, MaxContextNotesBytes,
		)
	}

	rev, err := saveNotes(ctx, text)
	if err != nil {
		return nil, err
	}
	details := map[string]any{
		"text":  text,
		"bytes": byteLen,
	}
	if rev.EntryID != "" {
		details["entryId"] = rev.EntryID
	}
	return &sdk.ToolResult{
		Content: "Context notes saved.",
		Details: details,
	}, nil
}

func executeContextNotesRaw(ctx sdk.Context, raw map[string]any) (any, error) {
	return ExecuteContextNotes(ctx, raw)
}

// ExecuteNewContext runs the `new_context` tool directly (exported for tests).
// upstream: packages/coding-agent/src/tools/context-notes.ts:170-183
func ExecuteNewContext(ctx sdk.Context, _ map[string]any) (*sdk.ToolResult, error) {
	if !IsEnabled(ctx) {
		return nil, errors.New("Experimental context management is disabled.")
	}
	sid := sessionIDFor(ctx)
	if sid == "" {
		return nil, errors.New("Cannot request new context: active session ID is unavailable.")
	}
	stateMu.Lock()
	st := getOrCreateStateLocked(sid)
	st.rolloverRequested = true
	stateMu.Unlock()
	return &sdk.ToolResult{
		Content: "New context window requested.",
		Details: map[string]any{"requested": true},
	}, nil
}

func executeNewContextRaw(ctx sdk.Context, raw map[string]any) (any, error) {
	return ExecuteNewContext(ctx, raw)
}

// IsRolloverRequested reports whether `new_context` has queued a pending
// rollover for ctx's session (exported for tests).
func IsRolloverRequested(ctx sdk.Context) bool {
	sid := sessionIDFor(ctx)
	if sid == "" {
		return false
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	st := sessionStates[sid]
	return st != nil && st.rolloverRequested
}

func consumeRolloverIfPending(ctx sdk.Context) {
	sid := sessionIDFor(ctx)
	if sid == "" {
		return
	}
	if !IsEnabled(ctx) {
		stateMu.Lock()
		if st := sessionStates[sid]; st != nil {
			st.rolloverRequested = false
		}
		stateMu.Unlock()
		return
	}
	stateMu.Lock()
	st := sessionStates[sid]
	if st == nil || !st.rolloverRequested {
		stateMu.Unlock()
		return
	}
	st.rolloverRequested = false
	preservedNotes := ""
	if st.loaded && st.hasNotes {
		preservedNotes = st.revision.Text
	}
	stateMu.Unlock()

	if preservedNotes == "" {
		if rev, ok := loadLatestNotes(ctx); ok {
			preservedNotes = rev.Text
		}
	}

	var zero sdk.Context
	if ctx == zero {
		return
	}

	// PiG SDK exposes `AppendEntry` + `CompactWithOptions` rather than a native
	// `reset_boundary` session-tree reset. We record a custom `reset_boundary`
	// marker and trigger host compaction preserving the latest context notes
	// (if any) as custom compaction instructions, then re-persist the notes
	// after the boundary so subsequent turns continue seeing the notebook.
	func() {
		defer func() { _ = recover() }()
		if err := ctx.AppendEntry(ResetBoundaryType, map[string]any{
			"source":    "new_context",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			ctx.Notify(fmt.Sprintf("Context rollover failed to persist reset boundary: %v", err), "error")
			return
		}
		if preservedNotes != "" {
			if err := ctx.AppendEntry(EntryType, ContextNotesEntry{
				Version: 1,
				Text:    preservedNotes,
			}); err != nil {
				ctx.Notify(fmt.Sprintf("Context rollover persisted reset boundary, but failed to re-persist context notes: %v", err), "error")
				return
			}
			ctx.CompactWithOptions(sdk.CompactOptions{
				CustomInstructions: "Preserve the experimental context notes verbatim as working context:\n\n" + preservedNotes,
				OnError: func(err error) {
					ctx.Notify(fmt.Sprintf("Context rollover compaction failed: %v", err), "error")
				},
			})
		} else {
			ctx.CompactWithOptions(sdk.CompactOptions{
				OnError: func(err error) {
					ctx.Notify(fmt.Sprintf("Context rollover compaction failed: %v", err), "error")
				},
			})
		}
	}()
}

// ── System Prompt Helper ─────────────────────────────────────────────────────

// BuildPromptInstructions returns the `<experimental-context-notes>` system
// prompt block when `compaction.experimentalContextManagement` is enabled and
// non-empty context notes exist for the active session branch. Returns ""
// when disabled or empty (zero default-session prompt drift).
// upstream: packages/coding-agent/src/session/context-notes.ts:55-59
func BuildPromptInstructions(ctx sdk.Context) string {
	if !IsEnabled(ctx) {
		return ""
	}
	rev, ok := loadLatestNotes(ctx)
	if !ok || len(rev.Text) == 0 {
		return ""
	}
	safeNotes := neutralizeContextNotesTag(rev.Text)
	return strings.TrimSpace(strings.ReplaceAll(systemContextNotesTemplate, "{{notes}}", safeNotes))
}

// ── Registration ─────────────────────────────────────────────────────────────

// Register installs `context_notes` and `new_context` with DefaultActive=false,
// registers AutoActivate gates keyed on `compaction.experimentalContextManagement`,
// wires rollover consumption on agent/turn end, and registers lifecycle cleanup.
func Register(e *sdk.Extension) {
	if e == nil {
		return
	}
	notesDef := contextNotesToolDef()
	newCtxDef := newContextToolDef()

	e.RegisterTool(intent.Tool(notesDef))
	e.RegisterTool(intent.Tool(newCtxDef))

	intent.RegisterSync(e, notesDef)
	intent.RegisterSync(e, newCtxDef)

	toolcmd.RegisterAutoActivate("context_notes", IsEnabled)
	toolcmd.RegisterAutoActivate("new_context", IsEnabled)

	e.OnEvent(sdk.EventAgentEnd, func(ctx sdk.Context, _ map[string]any) (any, error) {
		consumeRolloverIfPending(ctx)
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionTree, func(ctx sdk.Context, _ map[string]any) (any, error) {
		invalidateSessionCache(ctx)
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		invalidateSessionCache(ctx)
		return nil, nil
	})
	lifecycle.OnEnd("ctxnotes", cleanupSession)
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := sessionIDFor(ctx); sid != "" {
			lifecycle.End(sid)
		}
		return nil, nil
	})
}
