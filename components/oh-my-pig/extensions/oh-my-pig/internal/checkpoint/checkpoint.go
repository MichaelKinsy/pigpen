// Package checkpoint ports omp's context checkpoint and rewind tools.
//
// In omp v19.1.1, checkpoint/rewind are pure session-context operations (no git
// working-tree mutations): `checkpoint` records the checkpoint toolResult's
// session entry ID and steers a transient reminder; `rewind` captures the
// model's concise report and at turn_end replaces intermediate exploration
// messages strictly after that boundary with the retained report.
//
// upstream:
//   - packages/coding-agent/src/tools/checkpoint.ts
//   - packages/coding-agent/src/session/checkpoint-entries.ts
//   - packages/coding-agent/src/session/agent-session.ts (#applyRewind, #enforceRewindBeforeYield, #rehydrateCheckpointRewindState)
//   - packages/coding-agent/src/session/messages.ts (CHECKPOINT_ACTIVE_REMINDER_TYPE, REWIND_REPORT_MESSAGE_TYPE)
//   - packages/coding-agent/src/prompts/tools/{checkpoint,rewind}.md
//   - packages/coding-agent/src/prompts/system/{checkpoint-active-notice,rewind-report}.md
package checkpoint

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

//go:embed checkpoint.md
var checkpointPrompt string

//go:embed rewind.md
var rewindPrompt string

//go:embed checkpoint-active-notice.md
var checkpointActiveNotice string

//go:embed rewind-report.md
var rewindReportTemplate string

const (
	// SettingKey is the omp setting gating checkpoint and rewind.
	// upstream: packages/coding-agent/src/tools/settings.ts:855
	SettingKey = "checkpoint.enabled"

	// RewindReportMessageType is the custom_message customType persisted for
	// completed rewinds. upstream: packages/coding-agent/src/session/messages.ts:468
	RewindReportMessageType = "rewind-report"

	// CheckpointActiveReminderType is the custom_message customType for the
	// transient checkpoint-active steering notice.
	// upstream: packages/coding-agent/src/session/messages.ts:465
	CheckpointActiveReminderType = "checkpoint-active-reminder"

	// CheckpointRewindReminderType is the custom_message customType for the
	// settle-time reminder when a checkpoint is still active.
	CheckpointRewindReminderType = "checkpoint-rewind-reminder"

	// CheckpointStateEntryType is the custom session entry type persisted when
	// a checkpoint is created so branch replay can recover active state when
	// custom_message details or bash tool-command dispatches are inspected.
	CheckpointStateEntryType = "omp-checkpoint-state"
)

// Enabled reads a boolean setting key with default def. The settings lane
// replaces this variable with a host-backed reader; the default returns def
// (which is false for checkpoint.enabled, matching omp's default).
var Enabled = func(_ sdk.Context, _ string, def bool) bool {
	return def
}

// CheckpointState mirrors omp's CheckpointState in tools/checkpoint.ts:11-18.
type CheckpointState struct {
	Goal              string `json:"goal,omitempty"`
	CheckpointEntryID string `json:"checkpointEntryId,omitempty"`
	CheckpointCallID  string `json:"checkpointCallId,omitempty"`
	StartedAt         string `json:"startedAt"`
}

// CompletedRewindState mirrors omp's CompletedRewindState in tools/checkpoint.ts:20-27.
type CompletedRewindState struct {
	Report    string `json:"report"`
	StartedAt string `json:"startedAt"`
	RewoundAt string `json:"rewoundAt"`
}

// CheckpointToolDetails mirrors omp's CheckpointToolDetails in tools/checkpoint.ts:41-45.
type CheckpointToolDetails struct {
	Goal      string `json:"goal"`
	StartedAt string `json:"startedAt"`
}

// RewindToolDetails mirrors omp's RewindToolDetails in tools/checkpoint.ts:47-51.
type RewindToolDetails struct {
	Report  string `json:"report"`
	Rewound bool   `json:"rewound"`
}

type pendingRewind struct {
	report     string
	active     CheckpointState
	rewoundAt  string
	dispatched bool
}

type sessionState struct {
	active              *CheckpointState
	activatedThisTurn   bool
	lastCompletedRewind *CompletedRewindState
	pendingRewindReport string
	awaitingCommit      *pendingRewind
}

type manager struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
	now      func() time.Time
}

func newManager() *manager {
	return &manager{
		sessions: make(map[string]*sessionState),
		now:      time.Now,
	}
}

// defaultManager is shared across all Register calls in one process, with all
// mutable state strictly keyed by session ID so concurrent sessions and
// reloads never overwrite each other's toolcmd providers or state.
var defaultManager = newManager()

// ResetForTests clears all per-session state in defaultManager.
func ResetForTests() {
	defaultManager.mu.Lock()
	clear(defaultManager.sessions)
	defaultManager.now = time.Now
	defaultManager.mu.Unlock()
}
func sessionKey(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return "default"
	}
	if id, err := ctx.GetSessionID(); err == nil && id != "" {
		return id
	}
	return "default"
}

func (m *manager) stateLocked(key string) *sessionState {
	st, ok := m.sessions[key]
	if !ok {
		st = &sessionState{}
		m.sessions[key] = st
	}
	return st
}

// checkpointSchema matches omp's checkpointSchema in tools/checkpoint.ts:29-31.
func checkpointSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"goal": map[string]any{
				"type":        "string",
				"description": "investigation goal",
			},
		},
		"required":             []string{"goal"},
		"additionalProperties": false,
	}
}

// rewindSchema matches omp's rewindSchema in tools/checkpoint.ts:35-37.
func rewindSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"report": map[string]any{
				"type":        "string",
				"description": "investigation findings",
			},
		},
		"required":             []string{"report"},
		"additionalProperties": false,
	}
}

// syncCommittedState checks whether a previously proposed rewind-report was
// persisted onto the active branch WITHOUT holding m.mu during host reads.
// If not (e.g. host rejected boundary drafts), the pre-rewind active checkpoint
// is restored instead of silently claiming the checkpoint completed.
func (m *manager) syncCommittedState(ctx sdk.Context, key string) {
	m.mu.Lock()
	st := m.stateLocked(key)
	pending := st.awaitingCommit
	if pending == nil {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	persisted := branchContainsRewindReport(ctx, pending.active.StartedAt, pending.rewoundAt)

	m.mu.Lock()
	defer m.mu.Unlock()
	st = m.stateLocked(key)
	if st.awaitingCommit != pending {
		return
	}
	st.awaitingCommit = nil
	if persisted {
		st.active = nil
		st.activatedThisTurn = false
		st.pendingRewindReport = ""
		st.lastCompletedRewind = &CompletedRewindState{
			Report:    pending.report,
			StartedAt: pending.active.StartedAt,
			RewoundAt: pending.rewoundAt,
		}
		return
	}
	// Boundary drafts were not applied; restore active checkpoint state so the
	// model does not lose track of the unrewound exploration.
	cp := pending.active
	st.active = &cp
	st.pendingRewindReport = ""
}

func (m *manager) handleCheckpoint(ctx sdk.Context, params map[string]any) (any, error) {
	goal, ok := params["goal"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid checkpoint arguments: goal must be a string")
	}

	key := sessionKey(ctx)
	m.syncCommittedState(ctx, key)
	m.mu.Lock()
	st := m.stateLocked(key)
	if st.active != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("Checkpoint already active.")
	}
	startedAt := m.now().UTC().Format(time.RFC3339Nano)
	callID := ""
	var zero sdk.Context
	if ctx != zero {
		callID = ctx.ToolCallID()
	}

	// CheckpointEntryID is intentionally left empty here and backfilled ONLY at
	// this turn's turn_end (gated by activatedThisTurn) from toolResultEntryIds
	// so the checkpoint toolResult itself stays in context after rewind,
	// matching agent-session.ts:5284-5302.
	st.active = &CheckpointState{
		Goal:             goal,
		CheckpointCallID: callID,
		StartedAt:        startedAt,
	}
	st.activatedThisTurn = true
	st.pendingRewindReport = ""
	st.lastCompletedRewind = nil
	m.mu.Unlock()
	if ctx != zero {
		// Queue the transient checkpoint-active reminder as steering so the next
		// provider call sees it. upstream: agent-session.ts:4691-4709, 5024-5045.
		triggerFalse := false
		_ = ctx.SendMessage(CheckpointActiveReminderType, strings.TrimSpace(checkpointActiveNotice), false, sdk.SendMessageOptions{
			DeliverAs:   "steer",
			TriggerTurn: &triggerFalse,
			Details: map[string]any{
				"goal":      goal,
				"startedAt": startedAt,
			},
		})
	}

	return sdk.ToolResult{
		Content: fmt.Sprintf("Checkpoint: %s\nFinish exploration and formulate findings.", goal),
		Details: CheckpointToolDetails{
			Goal:      goal,
			StartedAt: startedAt,
		},
	}, nil
}

func (m *manager) handleRewind(ctx sdk.Context, params map[string]any) (any, error) {
	rawReport, ok := params["report"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid rewind arguments: report must be a string")
	}

	key := sessionKey(ctx)
	m.syncCommittedState(ctx, key)
	m.mu.Lock()
	st := m.stateLocked(key)
	if st.active == nil {
		hasCompleted := st.lastCompletedRewind != nil
		m.mu.Unlock()
		if hasCompleted {
			return nil, fmt.Errorf("Checkpoint already completed; continue from the retained rewind report instead of calling rewind again.")
		}
		return nil, fmt.Errorf("No active checkpoint. Create a checkpoint before calling rewind.")
	}
	report := strings.TrimSpace(rawReport)
	if report == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("Report cannot be empty.")
	}
	st.pendingRewindReport = report
	m.mu.Unlock()

	return sdk.ToolResult{
		Content: "Rewind requested.\nReport captured for context replacement.",
		Details: RewindToolDetails{
			Report:  report,
			Rewound: true,
		},
	}, nil
}

// isAbortedTurn reports whether a turn_end event ended due to abort or error.
// upstream: agent-session.ts:2619-2625 (`if (signal?.aborted) return;`)
func isAbortedTurn(data map[string]any) bool {
	if outcome, _ := data["outcome"].(string); outcome == "aborted" || outcome == "error" {
		return true
	}
	if msg, ok := data["message"].(map[string]any); ok {
		if sr, _ := msg["stopReason"].(string); sr == "aborted" || sr == "error" {
			return true
		}
	}
	return false
}

// onTurnEnd backfills the checkpoint toolResult entry ID on the checkpoint
// turn and applies a pending rewind on the rewind turn.
//
// In omp (agent-session.ts:#applyRewind), rewind calls
// sessionManager.branchWithSummary back to the checkpoint toolResult entry and
// appends a rewind-report custom_message. In PiG's extension SDK, NavigateTree
// is restricted to command contexts (refused inside event/tool handlers), so we
// emit `context_edit` omissions (`replacement: null`) for every editable entry
// strictly after the checkpoint toolResult entry, followed by the
// `rewind-report` `custom_message` draft.
func (m *manager) onTurnEnd(ctx sdk.Context, data map[string]any) (any, error) {
	key := sessionKey(ctx)
	if isAbortedTurn(data) || (ctx.Signal() != nil && ctx.Signal().Err() != nil) {
		// Aborted checkpoint turn must not carry activatedThisTurn into a later turn.
		m.mu.Lock()
		if st, ok := m.sessions[key]; ok {
			st.activatedThisTurn = false
		}
		m.mu.Unlock()
		return nil, nil
	}

	m.syncCommittedState(ctx, key)
	m.mu.Lock()
	st := m.stateLocked(key)

	// Backfill CheckpointEntryID ONLY at the turn_end of the turn in which
	// handleCheckpoint ran (activatedThisTurn). Never scan older cycles or
	// retry on a later turn. upstream: agent-session.ts:5284-5302.
	var checkpointBoundaryDraft map[string]any
	if st.activatedThisTurn {
		st.activatedThisTurn = false
		if st.active != nil && st.active.CheckpointEntryID == "" {
			if entryID := resolveCheckpointToolResultEntryID(st.active.CheckpointCallID, data); entryID != "" {
				st.active.CheckpointEntryID = entryID
				checkpointBoundaryDraft = map[string]any{
					"type":       "custom",
					"customType": CheckpointStateEntryType,
					"data": map[string]any{
						"active":            true,
						"goal":              st.active.Goal,
						"checkpointEntryId": entryID,
						"startedAt":         st.active.StartedAt,
					},
				}
			}
		}
	}

	if st.active == nil || st.pendingRewindReport == "" {
		m.mu.Unlock()
		if checkpointBoundaryDraft != nil {
			existing, _ := data["entries"].([]any)
			drafts := make([]any, 0, len(existing)+1)
			drafts = append(drafts, existing...)
			drafts = append(drafts, checkpointBoundaryDraft)
			return map[string]any{"entries": drafts}, nil
		}
		return nil, nil
	}

	active := *st.active
	report := st.pendingRewindReport
	rewoundAt := m.now().UTC().Format(time.RFC3339Nano)
	m.mu.Unlock()
	// Locate the checkpoint boundary on the ACTIVE branch. If the boundary is
	// missing from the active branch (e.g. after /tree navigation away from the
	// checkpoint), NEVER fall back to omitting from index 0; drop stale state
	// instead so the user's conversation is never wiped.
	omitIDs, foundBoundary := collectEditableEntriesAfterCheckpoint(ctx, active.CheckpointEntryID, data)
	if !foundBoundary {
		m.mu.Lock()
		st := m.stateLocked(key)
		st.active = nil
		st.pendingRewindReport = ""
		st.awaitingCommit = nil
		m.mu.Unlock()
		return nil, nil
	}

	existing, _ := data["entries"].([]any)
	drafts := make([]any, 0, len(existing)+len(omitIDs)+2)
	drafts = append(drafts, existing...)
	if checkpointBoundaryDraft != nil {
		drafts = append(drafts, checkpointBoundaryDraft)
	}
	for _, id := range omitIDs {
		drafts = append(drafts, map[string]any{
			"type":        "context_edit",
			"targetId":    id,
			"replacement": nil,
		})
	}

	// Persist the rewind-report custom message with omp's exact template and details.
	// upstream: agent-session.ts:13689-13697.
	rendered := renderRewindReport(report)
	drafts = append(drafts, map[string]any{
		"type":       "custom_message",
		"customType": RewindReportMessageType,
		"content":    rendered,
		"display":    false,
		"details": map[string]any{
			"report":    report,
			"startedAt": active.StartedAt,
			"rewoundAt": rewoundAt,
		},
	})

	// Do NOT mark the checkpoint completed before the host accepts the drafts:
	// record awaitingCommit so the next handler verifies persistence on the
	// branch and restores active state if the host rejected the proposal.
	m.mu.Lock()
	st = m.stateLocked(key)
	st.pendingRewindReport = ""
	st.awaitingCommit = &pendingRewind{
		report:     report,
		active:     active,
		rewoundAt:  rewoundAt,
		dispatched: true,
	}
	m.mu.Unlock()

	return map[string]any{
		"entries": drafts,
	}, nil
}

// resolveCheckpointToolResultEntryID finds the persisted session entry ID for
// the checkpoint toolResult strictly among the ending turn's own tool results:
// first matching by exact toolCallId or semantic dispatch (direct `checkpoint`
// or `bash` with `details.toolCommands`), otherwise falling back to the turn's
// last non-empty tool-result entry ID (e.g. a mounted bash call whose
// `details.toolCommands` was not stamped). Never scans older branch cycles.
func resolveCheckpointToolResultEntryID(callID string, turnData map[string]any) string {
	toolResults, _ := turnData["toolResults"].([]any)
	entryIDs, _ := turnData["toolResultEntryIds"].([]any)
	for i, raw := range toolResults {
		if i >= len(entryIDs) {
			break
		}
		eid, _ := entryIDs[i].(string)
		if eid == "" {
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if callID != "" {
			if tcID, _ := m["toolCallId"].(string); tcID == callID {
				return eid
			}
		}
		if semanticToolNameFromMap(m) == "checkpoint" {
			return eid
		}
	}
	for i := len(entryIDs) - 1; i >= 0; i-- {
		if eid, _ := entryIDs[i].(string); eid != "" {
			return eid
		}
	}
	return ""
}

// isEditableBranchEntry mirrors PiG's contextEditTargetRole
// (internal/codingagent/session_manager_projection.go:149-168): only
// custom_message entries and message entries with role user/assistant/toolResult
// are valid context_edit targets. Roles such as bashExecution are rejected by
// PiG's AppendContextEdit.
func isEditableBranchEntry(e sdk.BranchEntry) bool {
	if e.ID == "" {
		return false
	}
	if e.Type == "custom_message" {
		return true
	}
	if e.Type == "message" {
		switch e.Role {
		case "user", "assistant", "toolResult":
			return true
		}
	}
	return false
}

// collectEditableEntriesAfterCheckpoint returns editable entry IDs on the active
// branch strictly after checkpointEntryID. ok is false when checkpointEntryID is
// empty or not found on the active branch (never falls back to index 0).
func collectEditableEntriesAfterCheckpoint(ctx sdk.Context, checkpointEntryID string, turnData map[string]any) ([]string, bool) {
	if checkpointEntryID == "" {
		return nil, false
	}
	branch, err := ctx.GetBranch()
	if err != nil || len(branch) == 0 {
		return nil, false
	}

	foundIdx := -1
	for i, entry := range branch {
		if entry.ID == checkpointEntryID {
			foundIdx = i
			break
		}
	}
	if foundIdx < 0 {
		return nil, false
	}

	var ids []string
	seen := make(map[string]struct{})
	for i := foundIdx + 1; i < len(branch); i++ {
		e := branch[i]
		if !isEditableBranchEntry(e) {
			continue
		}
		if _, dup := seen[e.ID]; dup {
			continue
		}
		seen[e.ID] = struct{}{}
		ids = append(ids, e.ID)
	}

	// Include the current turn's persisted assistant/toolResult IDs if GetBranch
	// has not yet surfaced them, skipping checkpointEntryID itself (e.g. if
	// checkpoint and rewind ran in the same turn).
	for _, id := range turnDataEditableEntryIDs(turnData) {
		if id == checkpointEntryID {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, true
}

func turnDataEditableEntryIDs(data map[string]any) []string {
	var ids []string
	if id, ok := data["messageEntryId"].(string); ok && id != "" {
		ids = append(ids, id)
	}
	if rawList, ok := data["toolResultEntryIds"].([]any); ok {
		for _, item := range rawList {
			if id, ok := item.(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// onAgentBeforeSettle enforces omp's rule that an active checkpoint must be
// rewound before yielding to the user.
// upstream: packages/coding-agent/src/session/agent-session.ts:13626-13646 (#enforceRewindBeforeYield)
func (m *manager) onAgentBeforeSettle(ctx sdk.Context, data map[string]any) (any, error) {
	key := sessionKey(ctx)
	m.syncCommittedState(ctx, key)
	m.mu.Lock()
	st := m.stateLocked(key)
	needsReminder := st.active != nil && st.pendingRewindReport == ""
	m.mu.Unlock()
	if !needsReminder {
		return nil, nil
	}

	reminder := strings.Join([]string{
		"<system-warning>",
		"You are in an active checkpoint. You MUST call rewind with your investigation findings before yielding. Do NOT yield without completing the checkpoint.",
		"</system-warning>",
	}, "\n")

	existing, _ := data["entries"].([]any)
	nextEntries := make([]any, 0, len(existing)+1)
	nextEntries = append(nextEntries, existing...)
	nextEntries = append(nextEntries, map[string]any{
		"type":       "custom_message",
		"customType": CheckpointRewindReminderType,
		"content":    reminder,
		"display":    false,
	})
	return map[string]any{
		"entries":  nextEntries,
		"continue": true,
	}, nil
}

// onSessionStart replays active-branch checkpoint/rewind state.
// Activation/mounting of checkpoint & rewind is owned by internal/toolcmd via
// toolcmd.Mountable.
func (m *manager) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	m.replayFromBranch(ctx)
	return nil, nil
}

func (m *manager) onSessionReplay(ctx sdk.Context, _ map[string]any) (any, error) {
	m.replayFromBranch(ctx)
	return nil, nil
}

func (m *manager) endSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
}

// activeBranchRawEntries returns the raw JSON entries on the active branch in
// root-to-leaf order by intersecting ctx.GetEntries() with ctx.GetBranch() IDs
// (since sdk.BranchEntry flattens custom/details fields).
func activeBranchRawEntries(ctx sdk.Context) ([]json.RawMessage, bool) {
	branch, err := ctx.GetBranch()
	if err != nil {
		return nil, false
	}
	if len(branch) == 0 {
		return nil, true
	}
	rawEntries, err := ctx.GetEntries()
	if err != nil {
		return nil, false
	}
	byID := make(map[string]json.RawMessage, len(rawEntries))
	for _, raw := range rawEntries {
		var hdr struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &hdr) == nil && hdr.ID != "" {
			byID[hdr.ID] = raw
		}
	}
	ordered := make([]json.RawMessage, 0, len(branch))
	for _, be := range branch {
		if raw, ok := byID[be.ID]; ok {
			ordered = append(ordered, raw)
		}
	}
	return ordered, true
}

func branchContainsRewindReport(ctx sdk.Context, startedAt, rewoundAt string) bool {
	entries, ok := activeBranchRawEntries(ctx)
	if !ok {
		return false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		var hdr struct {
			Type       string          `json:"type"`
			CustomType string          `json:"customType"`
			Content    json.RawMessage `json:"content"`
			Details    json.RawMessage `json:"details"`
		}
		if json.Unmarshal(entries[i], &hdr) != nil {
			continue
		}
		if hdr.Type == "custom_message" && hdr.CustomType == RewindReportMessageType {
			if completed := completedRewindFromEntry(hdr.Details, hdr.Content); completed != nil {
				if completed.StartedAt == startedAt && completed.RewoundAt == rewoundAt {
					return true
				}
			}
		}
	}
	return false
}

// replayFromBranch restores active checkpoint and last completed rewind state
// strictly from the ACTIVE branch (root to current leaf), never from abandoned
// sibling branches in the session file.
// upstream: packages/coding-agent/src/session/agent-session.ts:8732-8763
// upstream: packages/coding-agent/src/session/checkpoint-entries.ts:44-91
func (m *manager) replayFromBranch(ctx sdk.Context) {
	entries, ok := activeBranchRawEntries(ctx)
	if !ok {
		return
	}

	var active *CheckpointState
	var lastCompleted *CompletedRewindState

	for _, raw := range entries {
		var hdr struct {
			ID         string          `json:"id"`
			Type       string          `json:"type"`
			CustomType string          `json:"customType"`
			Timestamp  string          `json:"timestamp"`
			Content    json.RawMessage `json:"content"`
			Data       json.RawMessage `json:"data"`
			Details    json.RawMessage `json:"details"`
			Message    json.RawMessage `json:"message"`
		}
		if json.Unmarshal(raw, &hdr) != nil {
			continue
		}

		switch {
		case hdr.Type == "message" && len(hdr.Message) > 0:
			var msg map[string]any
			if json.Unmarshal(hdr.Message, &msg) == nil {
				if role, _ := msg["role"].(string); role == "toolResult" {
					if name, details := semanticToolResultFromMap(msg); name == "checkpoint" {
						startedAt := hdr.Timestamp
						goal := ""
						if dm, ok := details.(map[string]any); ok {
							if s, _ := dm["startedAt"].(string); s != "" {
								startedAt = s
							}
							if g, _ := dm["goal"].(string); g != "" {
								goal = g
							}
						}
						active = &CheckpointState{
							Goal:              goal,
							CheckpointEntryID: hdr.ID,
							StartedAt:         startedAt,
						}
						lastCompleted = nil
					}
				}
			}
		case hdr.Type == "custom" && hdr.CustomType == CheckpointStateEntryType:
			var d struct {
				Active            bool   `json:"active"`
				Goal              string `json:"goal"`
				CheckpointEntryID string `json:"checkpointEntryId"`
				StartedAt         string `json:"startedAt"`
			}
			if json.Unmarshal(hdr.Data, &d) == nil && d.Active {
				startedAt := d.StartedAt
				if startedAt == "" {
					startedAt = hdr.Timestamp
				}
				entryID := d.CheckpointEntryID
				if entryID == "" {
					entryID = hdr.ID
				}
				active = &CheckpointState{
					Goal:              d.Goal,
					CheckpointEntryID: entryID,
					StartedAt:         startedAt,
				}
				lastCompleted = nil
			}
		case hdr.Type == "custom_message" && hdr.CustomType == RewindReportMessageType:
			if completed := completedRewindFromEntry(hdr.Details, hdr.Content); completed != nil {
				active = nil
				lastCompleted = completed
			}
		}
	}

	key := sessionKey(ctx)
	m.mu.Lock()
	st := m.stateLocked(key)
	st.active = active
	st.lastCompletedRewind = lastCompleted
	st.pendingRewindReport = ""
	st.awaitingCommit = nil
	m.mu.Unlock()
}

// semanticToolResultFromMap normalizes direct checkpoint/rewind toolResult
// messages and bash tool-command dispatches (`toolcmd.ToolCommandDispatches`),
// mirroring checkpoint-entries.ts:44-58.
func semanticToolResultFromMap(msg map[string]any) (string, any) {
	if msg == nil {
		return "", nil
	}
	toolName, _ := msg["toolName"].(string)
	if toolName == "checkpoint" || toolName == "rewind" {
		if isErr, _ := msg["isError"].(bool); isErr {
			return "", nil
		}
		return toolName, msg["details"]
	}
	dispatches := toolcmd.ToolCommandDispatches(toolName, msg)
	for i := len(dispatches) - 1; i >= 0; i-- {
		d := dispatches[i]
		if d.Mode != "execute" || d.ExitCode != 0 {
			continue
		}
		if d.Tool == "checkpoint" || d.Tool == "rewind" {
			return d.Tool, d.Inner
		}
	}
	return "", nil
}

func semanticToolNameFromMap(msg map[string]any) string {
	name, _ := semanticToolResultFromMap(msg)
	return name
}

// completedRewindFromEntry mirrors checkpoint-entries.ts:61-72.
func completedRewindFromEntry(detailsRaw, contentRaw json.RawMessage) *CompletedRewindState {
	var details struct {
		Report    string `json:"report"`
		StartedAt string `json:"startedAt"`
		RewoundAt string `json:"rewoundAt"`
	}
	if len(detailsRaw) == 0 || json.Unmarshal(detailsRaw, &details) != nil {
		return nil
	}
	if details.StartedAt == "" || details.RewoundAt == "" {
		return nil
	}
	report := strings.TrimSpace(details.Report)
	if report == "" {
		report = reportFromRewindReportContent(extractContentText(contentRaw))
	}
	if report == "" {
		return nil
	}
	return &CompletedRewindState{
		Report:    report,
		StartedAt: details.StartedAt,
		RewoundAt: details.RewoundAt,
	}
}

func extractContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var out []string
		for _, p := range parts {
			if p.Type == "text" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// reportFromRewindReportContent mirrors checkpoint-entries.ts:20-25.
func reportFromRewindReportContent(content string) string {
	const marker = "\nReport:\n"
	if idx := strings.LastIndex(content, marker); idx >= 0 {
		return strings.TrimSpace(content[idx+len(marker):])
	}
	return strings.TrimSpace(content)
}

func renderRewindReport(report string) string {
	return strings.ReplaceAll(strings.TrimSpace(rewindReportTemplate), "{{report}}", report)
}

// Register wires omp's checkpoint and rewind tools (discoverable via
// `toolcmd.Mountable`, default off via `checkpoint.enabled`, registered with
// `ToolExposureCodemode` so `Context.ExecuteTool` can invoke them when mounted
// as bash commands), package-level lifecycle cleanup, and their session hooks.
func Register(e *sdk.Extension) {
	m := defaultManager
	defaultOff := false

	e.RegisterTool(intent.Tool(schemaorder.Apply(sdk.ToolDefinition{
		Name:          "checkpoint",
		Label:         "Checkpoint",
		Description:   strings.TrimSpace(checkpointPrompt),
		Parameters:    checkpointSchema(),
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: &defaultOff,
		Execute:       m.handleCheckpoint,
	})))

	e.RegisterTool(intent.Tool(schemaorder.Apply(sdk.ToolDefinition{
		Name:          "rewind",
		Label:         "Rewind",
		Description:   strings.TrimSpace(rewindPrompt),
		Parameters:    rewindSchema(),
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: &defaultOff,
		Execute:       m.handleRewind,
	})))

	enabledPred := func(ctx sdk.Context) bool {
		return Enabled(ctx, SettingKey, false)
	}
	toolcmd.Mountable("checkpoint", enabledPred)
	toolcmd.Mountable("rewind", enabledPred)
	lifecycle.OnEnd("checkpoint", m.endSession)

	e.OnSessionStart(m.onSessionStart)
	e.OnEvent(sdk.EventSessionCompact, m.onSessionReplay)
	e.OnEvent(sdk.EventSessionTree, m.onSessionReplay)
	e.OnEvent(sdk.EventTurnEnd, m.onTurnEnd)
	e.OnEvent(sdk.EventAgentBeforeSettle, m.onAgentBeforeSettle)
}
