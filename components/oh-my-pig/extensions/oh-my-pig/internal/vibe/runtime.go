package vibe

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/agents"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/prompt"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
)

// upstream: packages/coding-agent/src/vibe/runtime.ts:1-1577
// upstream: packages/coding-agent/src/utils/token-rate.ts:1-60
// upstream: packages/tui/src/tools/task.ts:2324-2331 (oneLineLabel)

//go:embed prompts/vibe-turn-result.md
var vibeTurnResultTemplate string

//go:embed prompts/vibe-mode-active.md
var vibeModeActiveTemplate string

//go:embed prompts/vibe-spawn.md
var vibeSpawnPrompt string

//go:embed prompts/vibe-send.md
var vibeSendPrompt string

//go:embed prompts/vibe-wait.md
var vibeWaitPrompt string

//go:embed prompts/vibe-kill.md
var vibeKillPrompt string

//go:embed prompts/vibe-list.md
var vibeListPrompt string

// CLIAgent maps Vibe CLI flavor to the bundled subagent definition name.
// `fast` -> `sonic` (`@smol`), `good` -> `task` (`@task`).
// upstream: packages/coding-agent/src/vibe/runtime.ts:68-71
var CLIAgent = map[CLI]string{
	CLIFast: "sonic",
	CLIGood: "task",
}

const (
	// TurnTraceCap is the cap on completed tool calls retained per turn.
	// upstream: packages/coding-agent/src/vibe/runtime.ts:81
	TurnTraceCap = 40
	// TraceLineMax caps a single rendered trace line.
	// upstream: packages/coding-agent/src/vibe/runtime.ts:83
	TraceLineMax = 120
	// DefaultWaitTimeout is the default vibe_wait window when timeout is omitted.
	// upstream: packages/coding-agent/src/vibe/runtime.ts:85
	DefaultWaitTimeout = 30 * time.Second
	// ResponsePreviewMax caps response text inside a delivered turn result.
	// upstream: packages/coding-agent/src/vibe/runtime.ts:87
	ResponsePreviewMax = 6000
	// TeardownGrace is the grace period for cancellation/release cleanup before detaching.
	// upstream: packages/coding-agent/src/vibe/runtime.ts:89
	TeardownGrace = 5 * time.Second
	// MinTokenRateDurationMs matches upstream utils/token-rate.ts MIN_DURATION_MS.
	MinTokenRateDurationMs = 100
)

// OwnerScope scopes a Vibe worker session to its owner agent and parent session.
// upstream: packages/coding-agent/src/vibe/runtime.ts:91-95
type OwnerScope struct {
	OwnerID           string
	ParentSessionID   string
	ParentSessionFile string
}

func scopeKey(scope OwnerScope, id string) string {
	return scope.ParentSessionID + "\x00" + scope.ParentSessionFile + "\x00" + scope.OwnerID + "\x00" + id
}

func (s OwnerScope) matches(other OwnerScope) bool {
	return s.OwnerID == other.OwnerID &&
		s.ParentSessionID == other.ParentSessionID &&
		s.ParentSessionFile == other.ParentSessionFile
}

// ParentSession is the minimal parent-session surface needed by SessionRegistry.
// Production callers build one from an `sdk.Context`; tests may supply a stub.
// upstream: packages/coding-agent/src/vibe/runtime.ts:97-111
type ParentSession struct {
	SDK            sdk.Context
	OwnerID        string
	SessionID      string
	SessionFile    string
	Cwd            string
	ActiveModel    string
	FallbackModel  string
	AppendEntry    func(customType string, data any) error
	GetEntries     func() ([]json.RawMessage, error)
	GetBranch      func() ([]sdk.BranchEntry, error)
	SettingsStore  *settings.Store
	ModelOverrides map[string][]string
	ModelRoles     map[string]string
	AgentRegistry  *agents.Registry
	AgentsRuntime  *agents.Runtime
	SubagentRunner SubagentRunner
	FollowUpRunner FollowUpRunner
	SteerWorker    SteerFunc
}

// SpawnRequest carries resolved parameters passed to a first-turn SubagentRunner.
type SpawnRequest struct {
	ParentSession     *ParentSession
	Scope             OwnerScope
	ID                string
	CLI               CLI
	Agent             agents.AgentDefinition
	Prompt            string
	ModelOverride     []string
	ModelRole         string
	ArtifactsDir      string
	ChildSessionFile  string
	OnProgress        func(agents.AgentProgress)
	AgentRegistry     *agents.Registry
	AgentsRuntime     *agents.Runtime
	ParentSessionFile string
}

// FollowUpRequest carries parameters passed to a follow-up-turn FollowUpRunner.
type FollowUpRequest struct {
	ParentSession    *ParentSession
	Scope            OwnerScope
	ID               string
	CLI              CLI
	Agent            agents.AgentDefinition
	Message          string
	ModelOverride    []string
	ModelRole        string
	ArtifactsDir     string
	ChildSessionFile string
	OnProgress       func(agents.AgentProgress)
	AgentRegistry    *agents.Registry
}

// SubagentRunner executes a worker's initial turn (keep-alive).
type SubagentRunner func(ctx context.Context, req SpawnRequest) (agents.SingleResult, error)

// FollowUpRunner executes a subsequent turn on a persistent worker session.
type FollowUpRunner func(ctx context.Context, req FollowUpRequest) (agents.SingleResult, error)

// SteerFunc steers a currently streaming worker session mid-turn.
// Returns (true, nil) when steered, (false, nil) when the worker is not
// currently streaming (caller falls back to queueing), or an error.
type SteerFunc func(ref *agents.AgentRef, message string, attribution string) (bool, error)

// TurnError marks a failed worker turn while carrying the formatted result body.
// upstream: packages/coding-agent/src/vibe/runtime.ts:242
type TurnError struct {
	Text string
}

func (e *TurnError) Error() string { return e.Text }

type traceEntry struct {
	Tool  string
	Args  string
	EndMs int64
}

type liveView struct {
	CurrentTool     string
	CurrentToolArgs string
	LastIntent      string
	OutputTail      []string
}

type vibeTurn struct {
	JobID     string
	Message   string
	StartedAt int64
	Trace     []traceEntry
	ToolCount int
}

type vibeRecord struct {
	ID                string
	CLI               CLI
	Scope             OwnerScope
	ChildSessionFile  string
	Agent             agents.AgentDefinition
	ModelOverride     []string
	ModelRole         string
	State             SessionState
	CreatedAt         int64
	LastActivityAt    int64
	LastActivity      string
	ResolvedModel     string
	Turn              *vibeTurn
	Live              *liveView
	LastJobID         string
	Queue             []string
	TurnCount         int
	Killed            bool
	Suspended         bool
	TerminalPersisted bool
	// Direct pointer to the worker's own follow-up/child session if not in AgentRegistry.
	AgentRegistry *agents.Registry
}

// VibeTurnJob implements `jobs.Job` and `jobs.JobDetailer` for one Vibe worker turn.
// Registered in `internal/jobs` so async delivery, `jobs.Watch`, `jobs.MarkConsumed`,
// and `proc://` work identically to upstream's `AsyncJobManager`.
type VibeTurnJob struct {
	mu          sync.Mutex
	id          string
	agentID     string
	ownerID     string
	label       string
	started     time.Time
	ended       time.Time
	done        chan struct{}
	doneOnce    sync.Once
	status      string // "running", "completed", "failed", "cancelled"
	result      string
	errorText   string
	cancel      context.CancelFunc
	published   *bool
	lastIntent  string
	resolvedMod string
}

var (
	_ jobs.Job         = (*VibeTurnJob)(nil)
	_ jobs.JobDetailer = (*VibeTurnJob)(nil)
)

func newVibeTurnJob(id, agentID, ownerID, label string, cancel context.CancelFunc) *VibeTurnJob {
	return &VibeTurnJob{
		id:      id,
		agentID: agentID,
		ownerID: ownerID,
		label:   label,
		started: time.Now(),
		done:    make(chan struct{}),
		status:  "running",
		cancel:  cancel,
	}
}

func (j *VibeTurnJob) ID() string            { return j.id }
func (j *VibeTurnJob) Kind() jobs.Kind       { return jobs.KindAgent }
func (j *VibeTurnJob) Label() string         { return j.label }
func (j *VibeTurnJob) Started() time.Time    { return j.started }
func (j *VibeTurnJob) Done() <-chan struct{} { return j.done }

func (j *VibeTurnJob) Status() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

func (j *VibeTurnJob) Output() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.result != "" {
		return j.result
	}
	return j.errorText
}

func (j *VibeTurnJob) Result() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.result
}

func (j *VibeTurnJob) Ended() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.ended
}

func (j *VibeTurnJob) ErrorText() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.errorText
}

func (j *VibeTurnJob) Command() string { return "" }

func (j *VibeTurnJob) Intent() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.lastIntent
}

func (j *VibeTurnJob) SkipDelivery() bool { return false }

func (j *VibeTurnJob) Details() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	det := map[string]any{
		"agentId": j.agentID,
	}
	if j.resolvedMod != "" {
		det["resolvedModel"] = j.resolvedMod
	}
	if j.published != nil {
		det["agentOutputPublished"] = *j.published
	}
	return det
}

func (j *VibeTurnJob) Cancel() {
	j.mu.Lock()
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (j *VibeTurnJob) finish(status, resultText, errorText string, published *bool) {
	j.mu.Lock()
	if j.status != "running" {
		j.mu.Unlock()
		return
	}
	j.status = status
	j.result = resultText
	j.errorText = errorText
	if published != nil {
		v := *published
		j.published = &v
	}
	j.ended = time.Now()
	j.mu.Unlock()
	j.doneOnce.Do(func() { close(j.done) })
}

// OneLineLabel normalizes a text fragment onto one bounded line, stripping
// control/format characters and collapsing whitespace, matching upstream's
// `oneLineLabel` (`packages/tui/src/tools/task.ts:2324-2331`).
func OneLineLabel(text string, maxLen int) string {
	if maxLen < 1 {
		maxLen = 1
	}
	var b strings.Builder
	b.Grow(len(text))
	inSpace := true
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteRune(r)
		inSpace = false
	}
	s := strings.TrimSpace(b.String())
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen-1]) + "…"
	}
	return s
}

func firstLine(text string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 100
	}
	return OneLineLabel(text, maxLen)
}

func mergeTrace(turn *vibeTurn, progress agents.AgentProgress) {
	turn.ToolCount = progress.ToolCount
	// RecentTools is newest-first; iterate oldest-first to append to turn.Trace.
	for i := len(progress.RecentTools) - 1; i >= 0; i-- {
		entry := progress.RecentTools[i]
		dup := false
		for _, seen := range turn.Trace {
			if seen.EndMs == entry.EndMs && seen.Tool == entry.Tool && seen.Args == entry.Args {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		turn.Trace = append(turn.Trace, traceEntry{
			Tool:  entry.Tool,
			Args:  entry.Args,
			EndMs: entry.EndMs,
		})
		if len(turn.Trace) > TurnTraceCap {
			turn.Trace = turn.Trace[1:]
		}
	}
}

// ResolvedWorker holds the bundled agent definition and expanded model routing
// for a Vibe CLI flavor.
// upstream: packages/coding-agent/src/vibe/runtime.ts:121-126
type ResolvedWorker struct {
	Agent         agents.AgentDefinition
	ModelOverride []string
	ModelRole     string
}

// ResolveWorkerModelSelection resolves model override patterns and the
// pre-expansion role alias (`@smol` -> `smol`, `@task` -> `task`) for a Vibe
// worker, matching upstream `resolveAgentModelSelection`
// (`packages/coding-agent/src/config/model-resolver.ts:1506-1511`).
func ResolveWorkerModelSelection(
	agentName string,
	agentModel []string,
	settingsOverride []string,
	modelRoles map[string]string,
	activeModelPattern string,
	fallbackModelPattern string,
) (patterns []string, role string) {
	expandPattern := func(raw string) []string {
		s := strings.TrimSpace(raw)
		if s == "" {
			return nil
		}
		if strings.HasPrefix(s, "@") {
			roleName := strings.TrimSpace(s[1:])
			if roleName != "" && modelRoles != nil {
				if val := strings.TrimSpace(modelRoles[roleName]); val != "" {
					return []string{val}
				}
			}
			return nil
		}
		if strings.HasPrefix(s, "pi/") {
			roleName := strings.TrimSpace(s[3:])
			if roleName != "" && modelRoles != nil {
				if val := strings.TrimSpace(modelRoles[roleName]); val != "" {
					return []string{val}
				}
			}
			return nil
		}
		return []string{s}
	}
	expandList := func(list []string) []string {
		var out []string
		for _, item := range list {
			out = append(out, expandPattern(item)...)
		}
		return out
	}
	explicitRole := func(list []string) string {
		if len(list) != 1 {
			return ""
		}
		s := strings.TrimSpace(list[0])
		if strings.HasPrefix(s, "@") {
			return strings.TrimSpace(s[1:])
		}
		if strings.HasPrefix(s, "pi/") {
			return strings.TrimSpace(s[3:])
		}
		return ""
	}

	if ov := expandList(settingsOverride); len(ov) > 0 {
		return ov, explicitRole(settingsOverride)
	}
	if len(agentModel) > 0 {
		if exp := expandList(agentModel); len(exp) > 0 {
			return exp, explicitRole(agentModel)
		}
	}
	// Fallback chain: active model -> fallback model -> default role.
	for _, cand := range []string{activeModelPattern, fallbackModelPattern} {
		if exp := expandPattern(cand); len(exp) > 0 {
			return exp, ""
		}
	}
	if modelRoles != nil {
		if def := strings.TrimSpace(modelRoles["default"]); def != "" {
			return []string{def}, ""
		}
	}
	// Preserve the raw alias if no concrete model was configured (downstream
	// ResolveModelForChild falls back to parent active model).
	return append([]string(nil), agentModel...), explicitRole(agentModel)
}

// SessionRegistry is the process-global registry of Vibe worker sessions,
// scoped by owner agent ID, parent session ID, and parent session file.
// upstream: packages/coding-agent/src/vibe/runtime.ts:249-1542
type SessionRegistry struct {
	mu               sync.Mutex
	records          map[string]*vibeRecord
	terminatedScopes map[string]bool
	scopeLocks       map[string]*sync.Mutex
	outputManagers   map[string]*agents.OutputManager
	teardownGrace    time.Duration
	now              func() time.Time
	// NameGenerator overrides agents.GenerateTaskName in tests.
	NameGenerator func() string
}

var (
	globalRegistryMu sync.RWMutex
	globalRegistry   = NewSessionRegistry()
)

// Global returns the process-global Vibe SessionRegistry.
// upstream: packages/coding-agent/src/vibe/runtime.ts:252-257
func Global() *SessionRegistry {
	globalRegistryMu.RLock()
	r := globalRegistry
	globalRegistryMu.RUnlock()
	return r
}

// ResetGlobalForTests replaces the global registry with a fresh empty instance.
// upstream: packages/coding-agent/src/vibe/runtime.ts:260-262
func ResetGlobalForTests() {
	globalRegistryMu.Lock()
	globalRegistry = NewSessionRegistry()
	globalRegistryMu.Unlock()
}

// NewSessionRegistry creates an isolated Vibe SessionRegistry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		records:          make(map[string]*vibeRecord),
		terminatedScopes: make(map[string]bool),
		scopeLocks:       make(map[string]*sync.Mutex),
		outputManagers:   make(map[string]*agents.OutputManager),
		teardownGrace:    TeardownGrace,
		now:              time.Now,
	}
}

// SetTeardownGraceForTesting overrides the teardown grace timeout for tests.
// upstream: packages/coding-agent/src/vibe/runtime.ts:311-313
func (r *SessionRegistry) SetTeardownGraceForTesting(d time.Duration) {
	if d < time.Millisecond {
		d = time.Millisecond
	}
	r.mu.Lock()
	r.teardownGrace = d
	r.mu.Unlock()
}

// TestRecordInput specifies a bare record to inject via RegisterRecordForTests.
type TestRecordInput struct {
	ID      string
	CLI     CLI
	OwnerID string
	Scope   *OwnerScope
	State   SessionState
	Killed  bool
	JobID   string
	Turns   int
	Model   string
}

// RegisterRecordForTests inserts a bare worker record without running spawn
// machinery. Mirrors upstream `registerRecordForTests`
// (`packages/coding-agent/src/vibe/runtime.ts:271-303`).
func (r *SessionRegistry) RegisterRecordForTests(in TestRecordInput) {
	nowMs := r.now().UnixMilli()
	scope := OwnerScope{
		OwnerID:           in.OwnerID,
		ParentSessionID:   "test-parent-session",
		ParentSessionFile: "",
	}
	if in.Scope != nil {
		scope = *in.Scope
	}
	if scope.OwnerID == "" {
		scope.OwnerID = agents.MainAgentID
	}
	cli := in.CLI
	if cli == "" {
		cli = CLIFast
	}
	state := in.State
	if state == "" {
		state = StateRunning
	}
	ag, _ := agents.GetBundledAgent(CLIAgent[cli])
	rec := &vibeRecord{
		ID:             in.ID,
		CLI:            cli,
		Scope:          scope,
		Agent:          ag,
		State:          state,
		CreatedAt:      nowMs,
		LastActivityAt: nowMs,
		ResolvedModel:  in.Model,
		TurnCount:      in.Turns,
		Killed:         in.Killed,
	}
	if in.JobID != "" {
		rec.Turn = &vibeTurn{
			JobID:     in.JobID,
			Message:   "test turn",
			StartedAt: nowMs,
		}
	}
	r.mu.Lock()
	r.records[scopeKey(scope, in.ID)] = rec
	r.mu.Unlock()
}

// OwnerScope derives the canonical OwnerScope from parent.
// upstream: packages/coding-agent/src/vibe/runtime.ts:315-326
func (r *SessionRegistry) OwnerScope(parent *ParentSession) (OwnerScope, error) {
	if parent == nil {
		return OwnerScope{}, sdk.NewToolError("Vibe sessions require a stable parent session id.")
	}
	sid := strings.TrimSpace(parent.SessionID)
	if sid == "" {
		return OwnerScope{}, sdk.NewToolError("Vibe sessions require a stable parent session id.")
	}
	ownerID := strings.TrimSpace(parent.OwnerID)
	if ownerID == "" {
		ownerID = agents.MainAgentID
	}
	sf := strings.TrimSpace(parent.SessionFile)
	if sf != "" {
		if abs, err := filepath.Abs(sf); err == nil {
			sf = abs
		}
	}
	return OwnerScope{
		OwnerID:           ownerID,
		ParentSessionID:   sid,
		ParentSessionFile: sf,
	}, nil
}

// ActivateScope re-opens spawn admission after an explicit Vibe-mode entry.
// upstream: packages/coding-agent/src/vibe/runtime.ts:329-331
func (r *SessionRegistry) ActivateScope(scope OwnerScope) {
	r.mu.Lock()
	delete(r.terminatedScopes, scopeKey(scope, ""))
	r.mu.Unlock()
}

func (r *SessionRegistry) scopeMutex(scope OwnerScope) *sync.Mutex {
	key := scopeKey(scope, "")
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.scopeLocks[key]
	if m == nil {
		m = &sync.Mutex{}
		r.scopeLocks[key] = m
	}
	return m
}

func (r *SessionRegistry) agentRegistryFor(parent *ParentSession) *agents.Registry {
	if parent != nil && parent.AgentRegistry != nil {
		return parent.AgentRegistry
	}
	if parent != nil && parent.AgentsRuntime != nil && parent.AgentsRuntime.Registry != nil {
		return parent.AgentsRuntime.Registry
	}
	return agents.DefaultRuntime().Registry
}

func (r *SessionRegistry) resolveWorker(parent *ParentSession, cli CLI) (ResolvedWorker, error) {
	agentName, ok := CLIAgent[cli]
	if !ok {
		return ResolvedWorker{}, sdk.NewToolError(fmt.Sprintf(`Invalid vibe cli %q; expected "fast" or "good".`, cli))
	}
	ag, ok := agents.GetBundledAgent(agentName)
	if !ok {
		return ResolvedWorker{}, sdk.NewToolError(fmt.Sprintf(`Bundled agent %q for vibe cli %q is unavailable.`, agentName, cli))
	}
	var overrides []string
	var modelRoles map[string]string
	if parent != nil {
		if parent.ModelOverrides != nil {
			overrides = parent.ModelOverrides[agentName]
		}
		if parent.ModelRoles != nil {
			modelRoles = parent.ModelRoles
		}
		if st := parent.SettingsStore; st != nil {
			if len(overrides) == 0 {
				if rawOv := st.Any("task.agentModelOverrides"); rawOv != nil {
					overrides = extractModelOverrideFor(rawOv, agentName)
				}
			}
			if modelRoles == nil {
				if rawRoles := st.Any("modelRoles"); rawRoles != nil {
					modelRoles = extractStringMap(rawRoles)
				}
			}
		}
	}
	var activeModel, fallbackModel string
	if parent != nil {
		activeModel = parent.ActiveModel
		fallbackModel = parent.FallbackModel
	}
	patterns, role := ResolveWorkerModelSelection(agentName, ag.Model, overrides, modelRoles, activeModel, fallbackModel)
	return ResolvedWorker{
		Agent:         ag,
		ModelOverride: patterns,
		ModelRole:     role,
	}, nil
}

func extractModelOverrideFor(raw any, agentName string) []string {
	var val any
	switch m := raw.(type) {
	case map[string]any:
		val = m[agentName]
	case *settings.OrderedMap:
		if m != nil {
			val = m.Values[agentName]
		}
	}
	switch v := val.(type) {
	case string:
		if s := strings.TrimSpace(v); s != "" {
			return []string{s}
		}
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

func extractStringMap(raw any) map[string]string {
	out := map[string]string{}
	switch m := raw.(type) {
	case map[string]any:
		for k, v := range m {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out[k] = strings.TrimSpace(s)
			}
		}
	case *settings.OrderedMap:
		if m != nil {
			for _, k := range m.Keys {
				if s, ok := m.Values[k].(string); ok && strings.TrimSpace(s) != "" {
					out[k] = strings.TrimSpace(s)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func eventBase(rec *vibeRecord) LifecycleEvent {
	return LifecycleEvent{
		Version:         LifecycleVersion,
		ID:              rec.ID,
		OwnerID:         rec.Scope.OwnerID,
		ParentSessionID: rec.Scope.ParentSessionID,
	}
}

func (r *SessionRegistry) appendLifecycleEvent(parent *ParentSession, ev LifecycleEvent, expectedSessionFile string) (bool, error) {
	if expectedSessionFile == "" || parent == nil || parent.AppendEntry == nil {
		return false, nil
	}
	curScope, err := r.OwnerScope(parent)
	if err != nil {
		return false, nil
	}
	if curScope.ParentSessionID != ev.ParentSessionID ||
		curScope.OwnerID != ev.OwnerID ||
		curScope.ParentSessionFile != expectedSessionFile {
		return false, nil
	}
	if err := parent.AppendEntry(LifecycleCustomType, ev.ToMap()); err != nil {
		return false, err
	}
	return true, nil
}

func (r *SessionRegistry) appendTombstone(parent *ParentSession, rec *vibeRecord, reason TombstoneReason) (bool, error) {
	ev := eventBase(rec)
	ev.Action = ActionTombstone
	ev.Reason = reason
	return r.appendLifecycleEvent(parent, ev, rec.Scope.ParentSessionFile)
}

func (r *SessionRegistry) listIDsLocked(scope OwnerScope) []string {
	var ids []string
	for _, rec := range r.records {
		if rec.Scope.matches(scope) && rec.State != StateDead {
			ids = append(ids, rec.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// ListIDs returns all non-dead worker IDs in parent's scope.
// upstream: packages/coding-agent/src/vibe/runtime.ts:522-524
func (r *SessionRegistry) ListIDs(parent *ParentSession) ([]string, error) {
	scope, err := r.OwnerScope(parent)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listIDsLocked(scope), nil
}

// ListIDsByOwner returns every non-dead worker ID owned by ownerID across all
// parent scopes (used for tok/s aggregation).
// upstream: packages/coding-agent/src/vibe/runtime.ts:527-533
func (r *SessionRegistry) ListIDsByOwner(ownerID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for _, rec := range r.records {
		if rec.Scope.OwnerID == ownerID && rec.State != StateDead {
			ids = append(ids, rec.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (r *SessionRegistry) recordLocked(scope OwnerScope, id string) (*vibeRecord, error) {
	trimmed := strings.TrimSpace(id)
	rec, ok := r.records[scopeKey(scope, trimmed)]
	if !ok || !rec.Scope.matches(scope) {
		roster := r.listIDsLocked(scope)
		if len(roster) > 0 {
			return nil, sdk.NewToolError(fmt.Sprintf(`Unknown vibe session %q. Active sessions: %s`, id, strings.Join(roster, ", ")))
		}
		return nil, sdk.NewToolError(fmt.Sprintf(`Unknown vibe session %q. No sessions — spawn one with vibe_spawn.`, id))
	}
	return rec, nil
}

func registeredAgentFor(reg *agents.Registry, rec *vibeRecord) *agents.AgentRef {
	if reg == nil || rec == nil {
		return nil
	}
	ref, ok := reg.GetInSession(rec.Scope.ParentSessionID, rec.ID)
	if !ok || ref == nil {
		return nil
	}
	if ref.Kind != "sub" || ref.ParentID != rec.Scope.OwnerID {
		return nil
	}
	if rec.ChildSessionFile != "" && ref.SessionFile != "" && ref.SessionFile != rec.ChildSessionFile {
		return nil
	}
	return ref
}

// Screens returns live TV-wall screen snapshots in creation order.
// upstream: packages/coding-agent/src/vibe/runtime.ts:541-576
func (r *SessionRegistry) Screens(parent *ParentSession, ids []string) ([]ScreenSnapshot, error) {
	scope, err := r.OwnerScope(parent)
	if err != nil {
		return nil, err
	}
	var wanted map[string]bool
	if len(ids) > 0 {
		wanted = make(map[string]bool, len(ids))
		for _, id := range ids {
			wanted[strings.TrimSpace(id)] = true
		}
	}
	r.mu.Lock()
	var recs []*vibeRecord
	for _, rec := range r.records {
		if !rec.Scope.matches(scope) {
			continue
		}
		if wanted != nil && !wanted[rec.ID] {
			continue
		}
		recs = append(recs, rec)
	}
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].CreatedAt != recs[j].CreatedAt {
			return recs[i].CreatedAt < recs[j].CreatedAt
		}
		return recs[i].ID < recs[j].ID
	})
	out := make([]ScreenSnapshot, 0, len(recs))
	for _, rec := range recs {
		snap := ScreenSnapshot{
			ID:             rec.ID,
			CLI:            rec.CLI,
			State:          rec.State,
			Killed:         rec.Killed,
			Model:          rec.ResolvedModel,
			Turns:          rec.TurnCount,
			Queued:         len(rec.Queue),
			Trace:          []string{},
			OutputTail:     []string{},
			LastActivity:   rec.LastActivity,
			LastActivityAt: rec.LastActivityAt,
		}
		if rec.Turn != nil {
			snap.TurnStartedAt = rec.Turn.StartedAt
			snap.TurnMessage = firstLine(rec.Turn.Message, 80)
			start := 0
			if len(rec.Turn.Trace) > 6 {
				start = len(rec.Turn.Trace) - 6
			}
			for _, tr := range rec.Turn.Trace[start:] {
				label := tr.Tool
				if tr.Args != "" {
					label += "(" + tr.Args + ")"
				}
				snap.Trace = append(snap.Trace, firstLine(label, TraceLineMax))
			}
		}
		if rec.Live != nil {
			snap.CurrentTool = rec.Live.CurrentTool
			if rec.Live.CurrentToolArgs != "" {
				snap.CurrentToolArgs = firstLine(rec.Live.CurrentToolArgs, 60)
			}
			if rec.Live.LastIntent != "" {
				snap.LastIntent = firstLine(rec.Live.LastIntent, 80)
			}
			for _, line := range rec.Live.OutputTail {
				snap.OutputTail = append(snap.OutputTail, firstLine(line, 100))
			}
		}
		out = append(out, snap)
	}
	r.mu.Unlock()
	return out, nil
}

func (r *SessionRegistry) collectPersistedIDs(parent *ParentSession, scope OwnerScope) []string {
	seen := map[string]bool{}
	// Perform host RPC (`GetEntries`) without holding r.mu.
	if parent != nil && parent.GetEntries != nil {
		if rawEntries, err := parent.GetEntries(); err == nil {
			for _, raw := range rawEntries {
				var env struct {
					Type       string          `json:"type"`
					CustomType string          `json:"customType"`
					Data       json.RawMessage `json:"data"`
				}
				if json.Unmarshal(raw, &env) != nil || env.Type != "custom" || env.CustomType != LifecycleCustomType {
					continue
				}
				if ev, ok := ParseLifecycleEvent(env.Data); ok && ev.OwnerID == scope.OwnerID && ev.ParentSessionID == scope.ParentSessionID {
					seen[ev.ID] = true
				}
			}
		}
	}
	r.mu.Lock()
	for _, rec := range r.records {
		if rec.Scope.matches(scope) {
			seen[rec.ID] = true
		}
	}
	r.mu.Unlock()
	if reg := r.agentRegistryFor(parent); reg != nil {
		for _, ref := range reg.ListInSession(scope.ParentSessionID) {
			seen[ref.ID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

func (r *SessionRegistry) outputManagerLocked(scope OwnerScope, artifactsDir string) *agents.OutputManager {
	key := scopeKey(scope, "")
	if m, ok := r.outputManagers[key]; ok {
		return m
	}
	m := agents.NewOutputManager(func() string { return artifactsDir }, "", false)
	r.outputManagers[key] = m
	return m
}

func artifactsDirForParentFile(parentSessionFile string) string {
	if parentSessionFile == "" {
		return ""
	}
	if strings.HasSuffix(parentSessionFile, ".jsonl") {
		return parentSessionFile[:len(parentSessionFile)-len(".jsonl")]
	}
	return strings.TrimSuffix(parentSessionFile, filepath.Ext(parentSessionFile))
}

func resolvePersistedChildFile(parentSessionFile string, spawn LifecycleEvent, requireAgentMatch bool) string {
	if requireAgentMatch && spawn.Agent != CLIAgent[spawn.CLI] {
		return ""
	}
	if !validChildIDRE.MatchString(spawn.ID) || spawn.ChildSessionFile != spawn.ID+".jsonl" {
		return ""
	}
	artifactsDir := artifactsDirForParentFile(parentSessionFile)
	if artifactsDir == "" {
		return ""
	}
	absDir, err := filepath.Abs(artifactsDir)
	if err != nil {
		return ""
	}
	childFile := filepath.Join(absDir, spawn.ChildSessionFile)
	rel, err := filepath.Rel(absDir, childFile)
	if err != nil || rel == "" || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	if info, err := os.Stat(childFile); err != nil || info.IsDir() || info.Size() == 0 {
		return ""
	}
	return childFile
}

// Rehydrate reconciles resumable and terminal workers from the persisted parent
// session journal.
// upstream: packages/coding-agent/src/vibe/runtime.ts:690-790
func (r *SessionRegistry) Rehydrate(parent *ParentSession) (int, error) {
	if parent == nil || parent.SessionFile == "" || parent.GetEntries == nil {
		return 0, nil
	}
	scope, err := r.OwnerScope(parent)
	if err != nil {
		return 0, err
	}
	rawEntries, err := parent.GetEntries()
	if err != nil {
		return 0, err
	}
	var branchIDs map[string]bool
	if parent.GetBranch != nil {
		if branch, err := parent.GetBranch(); err == nil && len(branch) > 0 {
			branchIDs = make(map[string]bool, len(branch))
			for _, be := range branch {
				if be.ID != "" {
					branchIDs[be.ID] = true
				}
			}
		}
	}

	type restoreCandidate struct {
		spawn           LifecycleEvent
		turnCount       int
		lastActivityAt  int64
		inFlight        bool
		tombstoneReason TombstoneReason
	}

	allSpawns := map[string]LifecycleEvent{}
	terminalIntents := map[string]TombstoneReason{}
	candidates := map[string]*restoreCandidate{}

	for _, raw := range rawEntries {
		var env struct {
			ID         string          `json:"id"`
			Type       string          `json:"type"`
			CustomType string          `json:"customType"`
			Timestamp  string          `json:"timestamp"`
			Data       json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &env) != nil || env.Type != "custom" || env.CustomType != LifecycleCustomType {
			continue
		}
		ev, ok := ParseLifecycleEvent(env.Data)
		if !ok || ev.OwnerID != scope.OwnerID || ev.ParentSessionID != scope.ParentSessionID {
			continue
		}
		switch ev.Action {
		case ActionSpawn:
			allSpawns[ev.ID] = ev
		case ActionTombstone:
			terminalIntents[ev.ID] = ev.Reason
		case ActionTombstoneRevoked:
			if terminalIntents[ev.ID] == TombstoneModeExit {
				delete(terminalIntents, ev.ID)
			}
		}

		// Branch-scoped candidate replay.
		if branchIDs != nil && env.ID != "" && !branchIDs[env.ID] {
			continue
		}
		eventTime := ev.CreatedAt
		if parsed, err := time.Parse(time.RFC3339, env.Timestamp); err == nil {
			eventTime = parsed.UnixMilli()
		}
		if ev.Action == ActionSpawn {
			if eventTime == 0 {
				eventTime = ev.CreatedAt
			}
			candidates[ev.ID] = &restoreCandidate{
				spawn:          ev,
				turnCount:      0,
				lastActivityAt: eventTime,
				inFlight:       false,
			}
			continue
		}
		cand := candidates[ev.ID]
		if cand == nil {
			continue
		}
		if eventTime > 0 {
			cand.lastActivityAt = eventTime
		}
		switch ev.Action {
		case ActionTurnStarted:
			if ev.Turn >= cand.turnCount {
				cand.turnCount = ev.Turn
				cand.inFlight = true
			}
		case ActionTurnSettled:
			if ev.Turn >= cand.turnCount {
				cand.turnCount = ev.Turn
				cand.inFlight = false
			}
		case ActionTombstone:
			cand.tombstoneReason = ev.Reason
		case ActionTombstoneRevoked:
			if cand.tombstoneReason == TombstoneModeExit {
				cand.tombstoneReason = ""
			}
		}
	}

	reg := r.agentRegistryFor(parent)
	for id := range terminalIntents {
		spawn, ok := allSpawns[id]
		if !ok {
			continue
		}
		childFile := resolvePersistedChildFile(scope.ParentSessionFile, spawn, false)
		if childFile == "" {
			continue
		}
		if reg != nil {
			markTerminalRef(reg, scope, id, childFile)
		}
		r.mu.Lock()
		delete(r.records, scopeKey(scope, id))
		r.mu.Unlock()
	}

	restored := 0
	for _, cand := range candidates {
		spawn := cand.spawn
		if cand.tombstoneReason != "" || terminalIntents[spawn.ID] != "" || cand.turnCount < 1 {
			continue
		}
		childFile := resolvePersistedChildFile(scope.ParentSessionFile, spawn, true)
		if childFile == "" {
			continue
		}
		key := scopeKey(scope, spawn.ID)
		r.mu.Lock()
		_, exists := r.records[key]
		r.mu.Unlock()
		if exists {
			continue
		}

		resolved, err := r.resolveWorker(parent, spawn.CLI)
		if err != nil {
			continue
		}

		var blockedByCollision bool
		hasLiveSession := false
		if reg != nil {
			existing, hasExisting := reg.GetInSession(scope.ParentSessionID, spawn.ID)
			if hasExisting && existing != nil {
				sameWorker := existing.Kind == "sub" &&
					existing.ParentID == scope.OwnerID &&
					(existing.SessionFile == "" || existing.SessionFile == childFile)
				if sameWorker && existing.Status == "idle" && existing.Session != nil {
					hasLiveSession = true
				} else if !sameWorker || (existing.Status != "idle" && existing.Status != "parked") {
					blockedByCollision = true
				}
			} else {
				reg.Register(&agents.AgentRef{
					ID:          spawn.ID,
					DisplayName: spawn.ID,
					Kind:        "sub",
					ParentID:    scope.OwnerID,
					SessionID:   scope.ParentSessionID,
					SessionFile: childFile,
					Status:      "parked",
				})
			}
		}

		// Without port-wide cold revival (agents/UPSTREAM.md), a parked worker
		// with no live child session cannot accept follow-up turns; present it
		// truthfully as dead (transcript remains at history://<id>) rather than
		// as a permanent idle zombie.
		state := StateDead
		lastActivity := "parked across restart (cold revival unsupported; transcript at history://" + spawn.ID + ")"
		if blockedByCollision {
			lastActivity = "blocked by an agent id collision"
		} else if hasLiveSession {
			state = StateIdle
			lastActivity = ""
			if cand.inFlight {
				lastActivity = fmt.Sprintf("turn %d interrupted by process restart", cand.turnCount)
			}
		} else if cand.inFlight {
			lastActivity = fmt.Sprintf("turn %d interrupted by process restart (cold revival unsupported)", cand.turnCount)
		}

		r.mu.Lock()
		if _, exists := r.records[key]; !exists {
			r.records[key] = &vibeRecord{
				ID:               spawn.ID,
				CLI:              spawn.CLI,
				Scope:            scope,
				ChildSessionFile: childFile,
				Agent:            resolved.Agent,
				ModelOverride:    resolved.ModelOverride,
				ModelRole:        resolved.ModelRole,
				State:            state,
				CreatedAt:        spawn.CreatedAt,
				LastActivityAt:   cand.lastActivityAt,
				LastActivity:     lastActivity,
				TurnCount:        cand.turnCount,
				AgentRegistry:    reg,
			}
			restored++
		}
		r.mu.Unlock()
	}
	return restored, nil
}

// markTerminalRef mirrors upstream `VibeSessionRegistry.#markTerminalRef`
// (`packages/coding-agent/src/vibe/runtime.ts:653-687`): guards existing
// registry entries on kind, parent, session file, and already-terminal status
// before replacing with a terminal `"aborted"` transcript-only ref.
func markTerminalRef(reg *agents.Registry, scope OwnerScope, id, childFile string) {
	if reg == nil {
		return
	}
	if existing, ok := reg.GetInSession(scope.ParentSessionID, id); ok && existing != nil {
		if existing.Kind != "sub" || existing.ParentID != scope.OwnerID || (existing.SessionFile != "" && existing.SessionFile != childFile) {
			return
		}
		if existing.Status == "aborted" && existing.Session == nil {
			return
		}
		reg.KillAgent(scope.ParentSessionID, id)
	}
	reg.Register(&agents.AgentRef{
		ID:          id,
		DisplayName: id,
		Kind:        "sub",
		ParentID:    scope.OwnerID,
		SessionID:   scope.ParentSessionID,
		SessionFile: childFile,
		Status:      "aborted",
	})
}

// SpawnParams are the validated parameters to SessionRegistry.Spawn.
type SpawnParams struct {
	CLI    CLI
	Name   string
	Prompt string
}

// Spawn starts a persistent worker session and kicks off its first turn in the
// background, returning immediately with the worker ID and turn job ID.
// upstream: packages/coding-agent/src/vibe/runtime.ts:793-876
func (r *SessionRegistry) Spawn(parent *ParentSession, params SpawnParams) (SpawnOutcome, error) {
	scope, err := r.OwnerScope(parent)
	if err != nil {
		return SpawnOutcome{}, err
	}
	sl := r.scopeMutex(scope)
	sl.Lock()
	defer sl.Unlock()

	return r.spawnLocked(parent, scope, params)
}

func (r *SessionRegistry) spawnLocked(parent *ParentSession, scope OwnerScope, params SpawnParams) (SpawnOutcome, error) {
	r.mu.Lock()
	terminated := r.terminatedScopes[scopeKey(scope, "")]
	r.mu.Unlock()
	if terminated {
		return SpawnOutcome{}, sdk.NewToolError("Vibe mode has exited; enter Vibe mode again before spawning a worker.")
	}
	if !params.CLI.Valid() {
		return SpawnOutcome{}, sdk.NewToolError(fmt.Sprintf(`Invalid vibe cli %q; expected "fast" or "good".`, params.CLI))
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return SpawnOutcome{}, sdk.NewToolError("Prompt must not be empty.")
	}
	resolved, err := r.resolveWorker(parent, params.CLI)
	if err != nil {
		return SpawnOutcome{}, err
	}

	artifactsDir := artifactsDirForParentFile(scope.ParentSessionFile)
	reserved := r.collectPersistedIDs(parent, scope)
	r.mu.Lock()
	outMgr := r.outputManagerLocked(scope, artifactsDir)
	nameGen := r.NameGenerator
	r.mu.Unlock()
	if nameGen == nil {
		nameGen = agents.GenerateTaskName
	}
	outMgr.Reserve(reserved)
	baseName := agents.SanitizeAgentID(params.Name)
	if baseName == "" {
		baseName = nameGen()
	}
	id := outMgr.Allocate(baseName)

	childSessionName := id + ".jsonl"
	var childSessionFile string
	if artifactsDir != "" {
		childSessionFile = filepath.Join(artifactsDir, childSessionName)
	}
	createdAt := r.now().UnixMilli()
	reg := r.agentRegistryFor(parent)
	rec := &vibeRecord{
		ID:               id,
		CLI:              params.CLI,
		Scope:            scope,
		ChildSessionFile: childSessionFile,
		Agent:            resolved.Agent,
		ModelOverride:    resolved.ModelOverride,
		ModelRole:        resolved.ModelRole,
		State:            StateStarting,
		CreatedAt:        createdAt,
		LastActivityAt:   createdAt,
		AgentRegistry:    reg,
	}
	key := scopeKey(scope, id)
	r.mu.Lock()
	r.records[key] = rec
	r.mu.Unlock()

	if childSessionFile != "" {
		ev := eventBase(rec)
		ev.Action = ActionSpawn
		ev.CLI = params.CLI
		ev.Agent = resolved.Agent.Name
		ev.ChildSessionFile = childSessionName
		ev.CreatedAt = createdAt
		ok, appendErr := r.appendLifecycleEvent(parent, ev, scope.ParentSessionFile)
		if appendErr != nil || !ok {
			r.mu.Lock()
			rec.Killed = true
			rec.State = StateDead
			rec.LastActivityAt = r.now().UnixMilli()
			rec.LastActivity = "spawn failed"
			r.mu.Unlock()
			tombOK, _ := r.appendTombstone(parent, rec, TombstoneSpawnFailed)
			r.mu.Lock()
			rec.TerminalPersisted = tombOK
			if tombOK {
				delete(r.records, key)
			}
			r.mu.Unlock()
			if appendErr != nil {
				return SpawnOutcome{}, appendErr
			}
			return SpawnOutcome{}, sdk.NewToolError("Vibe parent session changed before the worker could start.")
		}
	}

	jobID, ok := r.claimAndStartTurn(parent, rec, params.Prompt, true)
	if !ok {
		return SpawnOutcome{}, sdk.NewToolError(fmt.Sprintf("Vibe session %q already has a turn in flight.", id))
	}
	return SpawnOutcome{ID: id, JobID: jobID}, nil
}

// SendParams are the parameters to SessionRegistry.Send.
type SendParams struct {
	Session string
	Message string
}

// Send delivers a message to a worker session:
//   - mid-turn & streaming -> live steering (`attribution: "agent"`)
//   - mid-turn & not steerable -> queued for automatic follow-up turn
//   - idle / parked -> starts a new background turn immediately
//
// upstream: packages/coding-agent/src/vibe/runtime.ts:883-920
func (r *SessionRegistry) Send(parent *ParentSession, params SendParams) (SendOutcome, error) {
	scope, err := r.OwnerScope(parent)
	if err != nil {
		return SendOutcome{}, err
	}
	msg := strings.TrimSpace(params.Message)
	if msg == "" {
		return SendOutcome{}, sdk.NewToolError("Message must not be empty.")
	}

	for {
		r.mu.Lock()
		rec, err := r.recordLocked(scope, params.Session)
		if err != nil {
			r.mu.Unlock()
			return SendOutcome{}, err
		}
		if rec.State == StateDead {
			id := rec.ID
			r.mu.Unlock()
			return SendOutcome{}, sdk.NewToolError(fmt.Sprintf(`Vibe session %q is dead. Spawn a new one with vibe_spawn.`, id))
		}
		hasTurn := rec.Turn != nil
		recID := rec.ID
		reg := rec.AgentRegistry
		if reg == nil {
			reg = r.agentRegistryFor(parent)
		}
		r.mu.Unlock()

		var rawRef *agents.AgentRef
		if reg != nil {
			rawRef, _ = reg.GetInSession(scope.ParentSessionID, recID)
		}
		registered := registeredAgentFor(reg, rec)
		if rawRef != nil && registered == nil {
			return SendOutcome{}, sdk.NewToolError(fmt.Sprintf(`Vibe session %q no longer resolves to this parent session.`, recID))
		}

		if hasTurn {
			steerFn := defaultSteerWorker
			if parent != nil && parent.SteerWorker != nil {
				steerFn = parent.SteerWorker
			}
			if registered != nil {
				steered, steerErr := steerFn(registered, msg, "agent")
				if steerErr != nil {
					return SendOutcome{}, steerErr
				}
				if steered {
					r.mu.Lock()
					rec.LastActivityAt = r.now().UnixMilli()
					r.mu.Unlock()
					return SendOutcome{ID: recID, Mode: SendModeSteered}, nil
				}
			}
			r.mu.Lock()
			if rec.State == StateDead {
				r.mu.Unlock()
				return SendOutcome{}, sdk.NewToolError(fmt.Sprintf(`Vibe session %q is dead. Spawn a new one with vibe_spawn.`, recID))
			}
			if rec.Turn != nil {
				rec.Queue = append(rec.Queue, msg)
				rec.LastActivityAt = r.now().UnixMilli()
				r.mu.Unlock()
				return SendOutcome{ID: recID, Mode: SendModeQueued}, nil
			}
			// The in-flight turn settled while we probed steering; loop to
			// re-validate resumability and atomically claim the next turn.
			r.mu.Unlock()
			continue
		}

		// Idle path: verify resumability BEFORE claiming the turn.
		// Without a custom test FollowUpRunner, a real worker requires a live
		// idle session (`registered.Session != nil`); only missing, sessionless,
		// or terminal refs (`aborted`/`parked`/`completed`) are marked dead.
		// A live session whose Status is `"running"` (e.g. handling a peer IRC
		// wake turn) must NOT be marked dead so a later Send or `/vibe` exit
		// still reaches and cleans it up.
		customFollowUp := parent != nil && parent.FollowUpRunner != nil
		if !customFollowUp {
			if registered == nil || registered.Session == nil ||
				registered.Status == "aborted" || registered.Status == "parked" || registered.Status == "completed" {
				r.markUnrecoverable(parent, rec)
				if registered != nil && registered.Session == nil && (registered.Status == "parked" || registered.Status == "idle") {
					return SendOutcome{}, sdk.NewToolError(fmt.Sprintf(
						"Vibe session %q has no live worker session (cold revival is not supported; transcript remains at history://%s). Spawn a new one with vibe_spawn.",
						recID, recID,
					))
				}
				return SendOutcome{}, sdk.NewToolError(fmt.Sprintf(`Vibe session %q no longer resolves to this parent session.`, recID))
			}
			if registered.Status != "idle" {
				return SendOutcome{}, sdk.NewToolError(fmt.Sprintf(
					"Worker session %q is currently busy (%s); retry vibe_send once it returns to idle.",
					recID, registered.Status,
				))
			}
		} else if registered != nil && registered.Status != "idle" && registered.Status != "parked" {
			return SendOutcome{}, sdk.NewToolError(fmt.Sprintf(`Vibe session %q no longer resolves to this parent session.`, recID))
		}

		// Atomically check idle state and claim rec.Turn under r.mu so two
		// concurrent Sends (or Send racing finishTurn's queue drain) can never
		// start two overlapping turns on the same worker.
		r.mu.Lock()
		if rec.State == StateDead {
			r.mu.Unlock()
			return SendOutcome{}, sdk.NewToolError(fmt.Sprintf(`Vibe session %q is dead. Spawn a new one with vibe_spawn.`, recID))
		}
		if rec.Turn != nil {
			// Another caller claimed a turn first; retry loop to steer/queue.
			r.mu.Unlock()
			continue
		}
		turn, turnIndex, jobID := r.claimTurnLocked(rec, msg)
		r.mu.Unlock()

		r.startClaimedTurn(parent, rec, turn, turnIndex, msg, false)
		return SendOutcome{ID: recID, Mode: SendModeTurn, JobID: jobID}, nil
	}
}

func (r *SessionRegistry) markUnrecoverable(parent *ParentSession, rec *vibeRecord) {
	r.mu.Lock()
	if rec.State == StateDead {
		r.mu.Unlock()
		return
	}
	rec.State = StateDead
	rec.LastActivityAt = r.now().UnixMilli()
	rec.LastActivity = "worker session no longer live"
	alreadyPersisted := rec.TerminalPersisted
	r.mu.Unlock()
	if !alreadyPersisted {
		if ok, _ := r.appendTombstone(parent, rec, TombstoneUnrecoverable); ok {
			r.mu.Lock()
			rec.TerminalPersisted = true
			r.mu.Unlock()
		}
	}
}

func defaultSteerWorker(ref *agents.AgentRef, message string, _ string) (bool, error) {
	if ref == nil || ref.Session == nil {
		return false, nil
	}
	streaming, err := ref.Session.IsStreaming()
	if err != nil || !streaming {
		return false, nil
	}
	if err := ref.Session.Steer(message); err != nil {
		return false, err
	}
	return true, nil
}

// WaitParams configures SessionRegistry.Wait.
type WaitParams struct {
	Sessions  []string
	Timeout   time.Duration // <=0 uses DefaultWaitTimeout
	HasCustom bool          // true if Timeout was explicitly provided
}

// Wait blocks until at least one watched session's in-flight turn settles,
// the timeout elapses, or ctx is cancelled. Settled turns are marked consumed
// in `internal/jobs` so their results are not delivered twice.
// upstream: packages/coding-agent/src/vibe/runtime.ts:928-1003
func (r *SessionRegistry) Wait(ctx context.Context, parent *ParentSession, params WaitParams) (WaitOutcome, error) {
	scope, err := r.OwnerScope(parent)
	if err != nil {
		return WaitOutcome{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	type snapEntry struct {
		rec   *vibeRecord
		jobID string
	}

	r.mu.Lock()
	var watched []*vibeRecord
	if len(params.Sessions) > 0 {
		for _, id := range params.Sessions {
			rec, err := r.recordLocked(scope, id)
			if err != nil {
				r.mu.Unlock()
				return WaitOutcome{}, err
			}
			watched = append(watched, rec)
		}
	} else {
		for _, rec := range r.records {
			if rec.Scope.matches(scope) && rec.Turn != nil {
				watched = append(watched, rec)
			}
		}
		sort.SliceStable(watched, func(i, j int) bool {
			if watched[i].CreatedAt != watched[j].CreatedAt {
				return watched[i].CreatedAt < watched[j].CreatedAt
			}
			return watched[i].ID < watched[j].ID
		})
	}

	// Snapshot each watched turn's job at entry so a queued follow-up turn
	// installed inside finishTurn cannot mask the settling job's result.
	snapshots := make([]snapEntry, 0, len(watched))
	for _, rec := range watched {
		jobID := ""
		if rec.Turn != nil {
			jobID = rec.Turn.JobID
		} else if rec.LastJobID != "" {
			jobID = rec.LastJobID
		}
		if jobID != "" {
			snapshots = append(snapshots, snapEntry{rec: rec, jobID: jobID})
		}
	}
	r.mu.Unlock()

	// Watch ALL snapshotted jobs BEFORE checking status so a job settling
	// between the status scan and Watch cannot trigger a duplicate async
	// delivery, and claim settled jobs via jobs.Claim (matching agents/wait_tool.go).
	allSnapIDs := make([]string, len(snapshots))
	for i, sn := range snapshots {
		allSnapIDs[i] = sn.jobID
	}
	unwatch := func() {}
	if len(allSnapIDs) > 0 {
		unwatch = jobs.Watch(ctx, scope.ParentSessionID, allSnapIDs)
	}
	var releases []func()
	defer func() {
		for _, rel := range releases {
			rel()
		}
	}()

	claimed := map[string]bool{}
	collectSettled := func() []SettledTurn {
		var out []SettledTurn
		for _, sn := range snapshots {
			j, ok := jobs.Get(scope.ParentSessionID, sn.jobID)
			if !ok || j == nil {
				continue
			}
			status := j.Status()
			if status == "running" {
				continue
			}
			if !claimed[sn.jobID] && status != "cancelled" {
				if rel, ok := jobs.Claim(ctx, scope.ParentSessionID, sn.jobID); ok {
					releases = append(releases, rel)
					claimed[sn.jobID] = true
				}
			}
			text := j.Result()
			if text == "" {
				if det, ok := j.(jobs.JobDetailer); ok {
					text = det.ErrorText()
				}
			}
			if text == "" {
				text = j.Output()
			}
			if text == "" {
				text = "(no output)"
			}
			out = append(out, SettledTurn{
				ID:         sn.rec.ID,
				JobID:      sn.jobID,
				Status:     status,
				ResultText: text,
			})
		}
		return out
	}

	var runningJobs []jobs.Job
	for _, sn := range snapshots {
		if j, ok := jobs.Get(scope.ParentSessionID, sn.jobID); ok && j != nil && j.Status() == "running" {
			runningJobs = append(runningJobs, j)
		}
	}

	waitEndedByTimeout := false
	if len(runningJobs) > 0 && len(collectSettled()) == 0 {
		timeout := DefaultWaitTimeout
		if params.Timeout > 0 {
			timeout = params.Timeout
		}
		if timeout < time.Millisecond {
			timeout = time.Millisecond
		}

		anyDone := make(chan struct{}, 1)
		stopWatchers := make(chan struct{})
		for _, j := range runningJobs {
			ch := j.Done()
			go func() {
				select {
				case <-ch:
					select {
					case anyDone <- struct{}{}:
					default:
					}
				case <-stopWatchers:
				}
			}()
		}

		// Prioritize immediate cancellation if ctx is already done (matching
		// upstream's synchronous `if (args.signal.aborted) onAbort()`).
		if ctx.Err() != nil {
			waitEndedByTimeout = false
		} else {
			timer := time.NewTimer(timeout)
			select {
			case <-anyDone:
				waitEndedByTimeout = false
			case <-ctx.Done():
				waitEndedByTimeout = false
			case <-timer.C:
				waitEndedByTimeout = true
			}
			timer.Stop()
		}
		close(stopWatchers)
	}

	settled := collectSettled()
	for _, s := range settled {
		jobs.MarkConsumed(scope.ParentSessionID, s.JobID)
	}
	// Unwatch AFTER marking settled jobs consumed so any watched job that
	// settled during an interrupted wait still self-delivers, while consumed
	// jobs are never delivered twice.
	unwatch()
	r.mu.Lock()
	stillRunning := make([]string, 0, len(watched))
	for _, rec := range watched {
		if rec.Turn != nil {
			stillRunning = append(stillRunning, rec.ID)
		}
	}
	r.mu.Unlock()

	if settled == nil {
		settled = []SettledTurn{}
	}
	return WaitOutcome{
		Settled:      settled,
		StillRunning: stillRunning,
		TimedOut:     waitEndedByTimeout && len(settled) == 0,
	}, nil
}

// SuspendScope detaches all process-local workers for scope without tombstoning
// their persisted conversations (used on session switch / reload).
// upstream: packages/coding-agent/src/vibe/runtime.ts:1006-1051
func (r *SessionRegistry) SuspendScope(scope OwnerScope) int {
	r.mu.Lock()
	var recs []*vibeRecord
	for key, rec := range r.records {
		if !rec.Scope.matches(scope) {
			continue
		}
		rec.Suspended = true
		rec.Queue = nil
		rec.State = StateDead
		rec.LastActivityAt = r.now().UnixMilli()
		rec.LastActivity = "suspended for parent-session switch"
		delete(r.records, key)
		recs = append(recs, rec)
	}
	grace := r.teardownGrace
	r.mu.Unlock()

	var waitChs []<-chan struct{}
	for _, rec := range recs {
		r.mu.Lock()
		turn := rec.Turn
		reg := rec.AgentRegistry
		r.mu.Unlock()
		if turn != nil {
			if j, ok := jobs.Get(scope.ParentSessionID, turn.JobID); ok && j != nil {
				j.Cancel()
				waitChs = append(waitChs, j.Done())
			}
		}
		if ref := registeredAgentFor(reg, rec); ref != nil {
			if reg != nil {
				reg.ParkIdleRef(ref)
				reg.UnregisterInSession(scope.ParentSessionID, rec.ID)
			}
		}
	}
	waitChannelsWithDeadline(waitChs, time.Now().Add(grace))
	return len(recs)
}

// Kill terminates one worker session, cancelling any in-flight turn, disposing
// its live subagent session, and leaving a terminal tombstone so its transcript
// remains readable at `history://<id>`.
// upstream: packages/coding-agent/src/vibe/runtime.ts:1069-1075
func (r *SessionRegistry) Kill(parent *ParentSession, id string) (KillOutcome, error) {
	scope, err := r.OwnerScope(parent)
	if err != nil {
		return KillOutcome{}, err
	}
	sl := r.scopeMutex(scope)
	sl.Lock()
	defer sl.Unlock()

	r.mu.Lock()
	rec, err := r.recordLocked(scope, id)
	r.mu.Unlock()
	if err != nil {
		return KillOutcome{}, err
	}
	return r.killRecord(parent, rec, TombstoneExplicitKill, true, time.Now().Add(r.grace()))
}

// KillAll terminates every non-terminal worker in parent's scope and marks the
// scope terminated until ActivateScope is called again.
// upstream: packages/coding-agent/src/vibe/runtime.ts:1078-1129
func (r *SessionRegistry) KillAll(parent *ParentSession, scopeOverride *OwnerScope) (int, error) {
	var scope OwnerScope
	var err error
	if scopeOverride != nil {
		scope = *scopeOverride
	} else {
		scope, err = r.OwnerScope(parent)
		if err != nil {
			return 0, err
		}
	}
	sl := r.scopeMutex(scope)
	sl.Lock()
	defer sl.Unlock()

	r.mu.Lock()
	var recs []*vibeRecord
	for _, rec := range r.records {
		if rec.Scope.matches(scope) && !(rec.State == StateDead && rec.TerminalPersisted) {
			recs = append(recs, rec)
		}
	}
	r.mu.Unlock()

	// Persist mode-exit tombstones BEFORE tearing down workers, matching
	// upstream `#killAllLocked` (`runtime.ts:1100-1129`). If persistence fails,
	// abort exit without killing workers or marking the scope terminated so the
	// caller can retry cleanly.
	if parent != nil && parent.AppendEntry != nil {
		for _, rec := range recs {
			r.mu.Lock()
			alreadyPersisted := rec.TerminalPersisted
			childFile := rec.ChildSessionFile
			r.mu.Unlock()
			if alreadyPersisted || childFile == "" {
				continue
			}
			ok, appendErr := r.appendTombstone(parent, rec, TombstoneModeExit)
			if appendErr != nil {
				return 0, appendErr
			}
			if !ok {
				return 0, sdk.NewToolError(fmt.Sprintf(`Vibe session %q changed parent scope before termination.`, rec.ID))
			}
			r.mu.Lock()
			rec.TerminalPersisted = true
			r.mu.Unlock()
		}
	}
	// Mark every pending record terminal (including fileless sessions where
	// childFile == "") once persistence succeeds, matching upstream
	// `#persistModeExit` (`runtime.ts:446,485`) so a second KillAll does not
	// recount the same worker.
	r.mu.Lock()
	for _, rec := range recs {
		rec.TerminalPersisted = true
	}
	r.mu.Unlock()

	// Tear down all workers concurrently under one shared deadline (matching
	// upstream `Promise.all` at `runtime.ts:1122-1126`).
	deadline := time.Now().Add(r.grace())
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	for _, rec := range recs {
		wg.Add(1)
		go func(rec *vibeRecord) {
			defer wg.Done()
			if _, err := r.killRecord(parent, rec, TombstoneModeExit, false, deadline); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(rec)
	}
	wg.Wait()

	if firstErr == nil {
		r.mu.Lock()
		r.terminatedScopes[scopeKey(scope, "")] = true
		r.mu.Unlock()
	}

	return len(recs), firstErr
}

// EndSession releases all Vibe worker records and cancels running turns for
// sessionID without touching any other session. Invoked from `lifecycle.OnEnd`.
func (r *SessionRegistry) EndSession(sessionID string) {
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	var recs []*vibeRecord
	for key, rec := range r.records {
		if rec.Scope.ParentSessionID != sessionID {
			continue
		}
		rec.Killed = true
		rec.Queue = nil
		rec.State = StateDead
		rec.LastActivityAt = r.now().UnixMilli()
		rec.LastActivity = "killed"
		delete(r.records, key)
		recs = append(recs, rec)
	}
	prefix := sessionID + "\x00"
	for k := range r.terminatedScopes {
		if strings.HasPrefix(k, prefix) {
			delete(r.terminatedScopes, k)
		}
	}
	for k := range r.outputManagers {
		if strings.HasPrefix(k, prefix) {
			delete(r.outputManagers, k)
		}
	}
	for k := range r.scopeLocks {
		if strings.HasPrefix(k, prefix) {
			delete(r.scopeLocks, k)
		}
	}
	r.mu.Unlock()

	for _, rec := range recs {
		r.mu.Lock()
		turn := rec.Turn
		reg := rec.AgentRegistry
		r.mu.Unlock()
		if turn != nil {
			if j, ok := jobs.Get(sessionID, turn.JobID); ok && j != nil {
				j.Cancel()
			}
		}
		if reg != nil {
			reg.KillAgent(sessionID, rec.ID)
		}
	}
}

func (r *SessionRegistry) grace() time.Duration {
	r.mu.Lock()
	g := r.teardownGrace
	r.mu.Unlock()
	if g <= 0 {
		return TeardownGrace
	}
	return g
}

func (r *SessionRegistry) killRecord(
	parent *ParentSession,
	rec *vibeRecord,
	reason TombstoneReason,
	persistTerminal bool,
	deadline time.Time,
) (KillOutcome, error) {
	var persistErr error
	r.mu.Lock()
	alreadyPersisted := rec.TerminalPersisted
	childFile := rec.ChildSessionFile
	r.mu.Unlock()

	if persistTerminal && !alreadyPersisted && childFile != "" && parent != nil && parent.AppendEntry != nil {
		ok, err := r.appendTombstone(parent, rec, reason)
		if err != nil {
			persistErr = err
		} else if !ok {
			persistErr = sdk.NewToolError(fmt.Sprintf(`Vibe session %q changed parent scope before termination.`, rec.ID))
		} else {
			r.mu.Lock()
			rec.TerminalPersisted = true
			r.mu.Unlock()
		}
	} else if persistTerminal && childFile == "" {
		r.mu.Lock()
		rec.TerminalPersisted = true
		r.mu.Unlock()
	}

	r.mu.Lock()
	rec.Killed = true
	rec.Queue = nil
	turn := rec.Turn
	reg := rec.AgentRegistry
	rec.State = StateDead
	rec.LastActivityAt = r.now().UnixMilli()
	rec.LastActivity = "killed"
	r.mu.Unlock()

	cancelledTurn := false
	var waitChs []<-chan struct{}
	if turn != nil {
		if j, ok := jobs.Get(rec.Scope.ParentSessionID, turn.JobID); ok && j != nil {
			if j.Status() == "running" {
				cancelledTurn = true
				j.Cancel()
			}
			waitChs = append(waitChs, j.Done())
		}
	}

	if reg == nil {
		reg = r.agentRegistryFor(parent)
	}
	if reg != nil {
		reg.KillAgent(rec.Scope.ParentSessionID, rec.ID)
	}

	waitChannelsWithDeadline(waitChs, deadline)

	if persistErr != nil {
		return KillOutcome{ID: rec.ID, CancelledTurn: cancelledTurn}, persistErr
	}
	return KillOutcome{ID: rec.ID, CancelledTurn: cancelledTurn}, nil
}

func waitChannelsWithDeadline(chs []<-chan struct{}, deadline time.Time) bool {
	if len(chs) == 0 {
		return true
	}
	rem := time.Until(deadline)
	if rem <= 0 {
		for _, ch := range chs {
			select {
			case <-ch:
			default:
				return false
			}
		}
		return true
	}
	timer := time.NewTimer(rem)
	defer timer.Stop()
	for _, ch := range chs {
		select {
		case <-ch:
		case <-timer.C:
			return false
		}
	}
	return true
}

// claimTurnLocked atomically installs rec.Turn and advances TurnCount while
// holding r.mu. Caller MUST verify rec.Turn == nil and rec.State != StateDead.
func (r *SessionRegistry) claimTurnLocked(rec *vibeRecord, message string) (*vibeTurn, int, string) {
	turnIndex := rec.TurnCount + 1
	jobID := fmt.Sprintf("%s-t%d", rec.ID, turnIndex)
	startedAt := r.now().UnixMilli()
	turn := &vibeTurn{
		JobID:     jobID,
		Message:   message,
		StartedAt: startedAt,
	}
	rec.Turn = turn
	rec.State = StateRunning
	rec.TurnCount = turnIndex
	rec.LastActivityAt = startedAt
	return turn, turnIndex, jobID
}

func (r *SessionRegistry) claimAndStartTurn(parent *ParentSession, rec *vibeRecord, message string, first bool) (string, bool) {
	r.mu.Lock()
	if rec.Turn != nil || rec.State == StateDead || rec.Killed || rec.Suspended {
		r.mu.Unlock()
		return "", false
	}
	turn, turnIndex, jobID := r.claimTurnLocked(rec, message)
	r.mu.Unlock()
	r.startClaimedTurn(parent, rec, turn, turnIndex, message, first)
	return jobID, true
}

func (r *SessionRegistry) startClaimedTurn(parent *ParentSession, rec *vibeRecord, turn *vibeTurn, turnIndex int, message string, first bool) {
	jobCtx, jobCancel := context.WithCancel(context.Background())
	label := fmt.Sprintf("vibe %s %s: %s", rec.CLI, rec.ID, firstLine(message, 60))
	job := newVibeTurnJob(turn.JobID, rec.ID, rec.Scope.OwnerID, label, jobCancel)
	jobs.Add(rec.Scope.ParentSessionID, job)
	onProgress := func(p agents.AgentProgress) {
		r.mu.Lock()
		if rec.Turn == turn {
			mergeTrace(turn, p)
			if p.ResolvedModel != "" {
				rec.ResolvedModel = p.ResolvedModel
			}
			// RecentOutput in agents.AgentProgress is oldest-first (up to 8 lines);
			// take the latest 3 lines for live TV-wall display.
			var tail []string
			if len(p.RecentOutput) > 0 {
				start := 0
				if len(p.RecentOutput) > 3 {
					start = len(p.RecentOutput) - 3
				}
				tail = append([]string(nil), p.RecentOutput[start:]...)
			}
			rec.Live = &liveView{
				CurrentTool:     p.CurrentTool,
				CurrentToolArgs: p.CurrentToolArgs,
				LastIntent:      p.LastIntent,
				OutputTail:      tail,
			}
			gist := p.LastIntent
			if gist == "" && p.CurrentTool != "" {
				if p.CurrentToolArgs != "" {
					gist = p.CurrentTool + " " + p.CurrentToolArgs
				} else {
					gist = p.CurrentTool
				}
			}
			if gist != "" {
				rec.LastActivity = firstLine(gist, 100)
			}
			rec.LastActivityAt = r.now().UnixMilli()
		}
		r.mu.Unlock()
		job.mu.Lock()
		if p.LastIntent != "" {
			job.lastIntent = p.LastIntent
		}
		if p.ResolvedModel != "" {
			job.resolvedMod = p.ResolvedModel
		}
		job.mu.Unlock()
	}

	go func() {
		defer jobCancel()
		r.runTurnJob(jobCtx, parent, rec, turn, job, message, turnIndex, first, onProgress)
	}()
}

func (r *SessionRegistry) runTurnJob(
	ctx context.Context,
	parent *ParentSession,
	rec *vibeRecord,
	turn *vibeTurn,
	job *VibeTurnJob,
	message string,
	turnIndex int,
	first bool,
	onProgress func(agents.AgentProgress),
) {
	defer func() {
		if pv := recover(); pv != nil {
			reason := fmt.Sprintf("vibe turn panicked: %v", pv)
			r.finishTurn(parent, rec, job.ID(), false)
			r.mu.Lock()
			rec.LastActivity = firstLine("turn failed: "+reason, 100)
			r.mu.Unlock()
			errText := fmt.Sprintf("[vibe:%s cli=%s turn=%d] turn failed: %s", rec.ID, rec.CLI, turnIndex, reason)
			job.finish("failed", "", errText, nil)
		}
	}()

	if rec.ChildSessionFile != "" && parent != nil && parent.AppendEntry != nil {
		ev := eventBase(rec)
		ev.Action = ActionTurnStarted
		ev.Turn = turnIndex
		ok, err := r.appendLifecycleEvent(parent, ev, rec.Scope.ParentSessionFile)
		if err != nil || !ok {
			reason := fmt.Sprintf("Vibe session %q changed parent scope before its turn started.", rec.ID)
			if err != nil {
				reason = err.Error()
			}
			r.finishTurn(parent, rec, job.ID(), false)
			r.mu.Lock()
			rec.LastActivity = firstLine("turn failed: "+reason, 100)
			r.mu.Unlock()
			errText := fmt.Sprintf("[vibe:%s cli=%s turn=%d] turn failed: %s", rec.ID, rec.CLI, turnIndex, reason)
			job.finish("failed", "", errText, nil)
			return
		}
	}

	artifactsDir := artifactsDirForParentFile(rec.Scope.ParentSessionFile)
	reg := r.agentRegistryFor(parent)
	var (
		sr  agents.SingleResult
		err error
	)
	if first {
		runner := defaultRunSubagent
		if parent != nil && parent.SubagentRunner != nil {
			runner = parent.SubagentRunner
		}
		sr, err = runner(ctx, SpawnRequest{
			ParentSession:     parent,
			Scope:             rec.Scope,
			ID:                rec.ID,
			CLI:               rec.CLI,
			Agent:             rec.Agent,
			Prompt:            message,
			ModelOverride:     append([]string(nil), rec.ModelOverride...),
			ModelRole:         rec.ModelRole,
			ArtifactsDir:      artifactsDir,
			ChildSessionFile:  rec.ChildSessionFile,
			OnProgress:        onProgress,
			AgentRegistry:     reg,
			AgentsRuntime:     agentsRuntimeFor(parent),
			ParentSessionFile: rec.Scope.ParentSessionFile,
		})
	} else {
		runner := defaultRunFollowUpTurn
		if parent != nil && parent.FollowUpRunner != nil {
			runner = parent.FollowUpRunner
		}
		sr, err = runner(ctx, FollowUpRequest{
			ParentSession:    parent,
			Scope:            rec.Scope,
			ID:               rec.ID,
			CLI:              rec.CLI,
			Agent:            rec.Agent,
			Message:          message,
			ModelOverride:    append([]string(nil), rec.ModelOverride...),
			ModelRole:        rec.ModelRole,
			ArtifactsDir:     artifactsDir,
			ChildSessionFile: rec.ChildSessionFile,
			OnProgress:       onProgress,
			AgentRegistry:    reg,
		})
	}

	if err != nil {
		r.finishTurn(parent, rec, job.ID(), false)
		reason := err.Error()
		r.mu.Lock()
		rec.LastActivity = firstLine("turn failed: "+reason, 100)
		r.mu.Unlock()
		errText := fmt.Sprintf("[vibe:%s cli=%s turn=%d] turn failed: %s", rec.ID, rec.CLI, turnIndex, reason)
		status := "failed"
		if ctx.Err() != nil {
			status = "cancelled"
		}
		job.finish(status, "", errText, nil)
		return
	}

	customRunners := parent != nil && (parent.SubagentRunner != nil || parent.FollowUpRunner != nil)
	r.settleTurn(ctx, parent, rec, turn, job, turnIndex, sr, customRunners)
}

func agentsRuntimeFor(parent *ParentSession) *agents.Runtime {
	if parent != nil && parent.AgentsRuntime != nil {
		return parent.AgentsRuntime
	}
	return agents.DefaultRuntime()
}

func (r *SessionRegistry) finishTurn(parent *ParentSession, rec *vibeRecord, settledJobID string, customRunners bool) {
	r.mu.Lock()
	rec.LastJobID = settledJobID
	rec.Turn = nil
	rec.Live = nil
	rec.LastActivityAt = r.now().UnixMilli()
	killedOrSuspended := rec.Killed || rec.Suspended
	if killedOrSuspended {
		rec.State = StateDead
		r.mu.Unlock()
		return
	}
	reg := rec.AgentRegistry
	if reg == nil {
		reg = r.agentRegistryFor(parent)
	}
	r.mu.Unlock()

	registered := registeredAgentFor(reg, rec)
	var alive bool
	if customRunners {
		// Unit tests with custom runners may register a synthetic AgentRef
		// without a real *sdk.AgentSession or skip AgentRegistry entirely.
		alive = registered == nil || registered.Status == "idle" || registered.Status == "parked"
	} else {
		// Real workers require a live idle child session (`Session != nil`);
		// parked/sessionless refs cannot be revived (agents/UPSTREAM.md).
		alive = registered != nil && registered.Status == "idle" && registered.Session != nil
	}

	r.mu.Lock()
	if rec.Killed || rec.Suspended {
		rec.State = StateDead
		r.mu.Unlock()
		return
	}
	if !alive {
		rec.State = StateDead
		r.mu.Unlock()
		if ok, _ := r.appendTombstone(parent, rec, TombstoneUnrecoverable); ok {
			r.mu.Lock()
			rec.TerminalPersisted = true
			r.mu.Unlock()
		}
		return
	}
	rec.State = StateIdle
	turnCount := rec.TurnCount
	childFile := rec.ChildSessionFile
	r.mu.Unlock()

	if childFile != "" && parent != nil && parent.AppendEntry != nil {
		ev := eventBase(rec)
		ev.Action = ActionTurnSettled
		ev.Turn = turnCount
		ok, err := r.appendLifecycleEvent(parent, ev, rec.Scope.ParentSessionFile)
		if err != nil || !ok {
			r.mu.Lock()
			rec.State = StateDead
			r.mu.Unlock()
			return
		}
	}

	// Atomically drain the queue and claim the next turn in the same critical
	// section so a concurrent Send cannot race and start a second turn.
	r.mu.Lock()
	if len(rec.Queue) == 0 || rec.Turn != nil || rec.Killed || rec.Suspended || rec.State == StateDead {
		r.mu.Unlock()
		return
	}
	nextMessage := strings.Join(rec.Queue, "\n\n")
	rec.Queue = nil
	nextTurn, nextTurnIndex, _ := r.claimTurnLocked(rec, nextMessage)
	r.mu.Unlock()

	r.startClaimedTurn(parent, rec, nextTurn, nextTurnIndex, nextMessage, false)
}

func (r *SessionRegistry) settleTurn(
	ctx context.Context,
	parent *ParentSession,
	rec *vibeRecord,
	turn *vibeTurn,
	job *VibeTurnJob,
	turnIndex int,
	result agents.SingleResult,
	customRunners bool,
) {
	r.finishTurn(parent, rec, job.ID(), customRunners)

	failed := result.ExitCode != 0 || result.Aborted
	status := "completed"
	if result.Aborted {
		status = "aborted"
	} else if failed {
		status = "failed"
	}

	r.mu.Lock()
	if failed {
		reason := result.AbortReason
		if reason == "" {
			reason = result.Error
		}
		rec.LastActivity = firstLine(fmt.Sprintf("turn %d %s: %s", turnIndex, status, reason), 100)
	} else {
		gist := result.LastIntent
		if gist == "" {
			gist = result.Output
		}
		rec.LastActivity = firstLine(gist, 100)
	}
	if result.ResolvedModel != "" {
		rec.ResolvedModel = result.ResolvedModel
	}
	model := result.ResolvedModel
	if model == "" {
		model = rec.ResolvedModel
	}
	alive := rec.State != StateDead
	traceLines := make([]string, 0, len(turn.Trace))
	for _, entry := range turn.Trace {
		line := entry.Tool
		if entry.Args != "" {
			line += "(" + entry.Args + ")"
		}
		traceLines = append(traceLines, firstLine(line, TraceLineMax))
	}
	toolCount := turn.ToolCount
	traceOverflow := toolCount - len(turn.Trace)
	if traceOverflow < 0 {
		traceOverflow = 0
	}
	r.mu.Unlock()

	response := strings.TrimSpace(result.Output)
	if response == "" {
		response = "(no output)"
	}
	response, responseTruncated := truncateResponseUTF16(response, ResponsePreviewMax)
	errDetail := ""
	if failed {
		errDetail = result.AbortReason
		if errDetail == "" {
			errDetail = result.Error
		}
		if errDetail == "" {
			errDetail = result.Stderr
		}
	}

	text := RenderTurnResult(TurnResultData{
		ID:                rec.ID,
		CLI:               rec.CLI,
		Turn:              turnIndex,
		Status:            status,
		Duration:          jobs.FormatDuration(time.Duration(result.DurationMs) * time.Millisecond),
		Requests:          result.Requests,
		ToolCount:         toolCount,
		Model:             model,
		Trace:             traceLines,
		TraceOverflow:     traceOverflow,
		Response:          response,
		ResponseTruncated: responseTruncated,
		Error:             errDetail,
		Alive:             alive,
	})

	published := result.OutputPath != ""
	if ctx.Err() != nil {
		job.finish("cancelled", text, text, &published)
		return
	}
	if failed {
		job.finish("failed", text, text, &published)
		return
	}
	job.finish("completed", text, "", &published)
}

// TurnResultData is the template data for `prompts/vibe-turn-result.md`.
// upstream: packages/coding-agent/src/vibe/runtime.ts:1491-1508
type TurnResultData struct {
	ID                string
	CLI               CLI
	Turn              int
	Status            string
	Duration          string
	Requests          int
	ToolCount         int
	Model             string
	Trace             []string
	TraceOverflow     int
	Response          string
	ResponseTruncated bool
	Error             string
	Alive             bool
}

// truncateResponseUTF16 enforces upstream's UTF-16 `response.length > RESPONSE_PREVIEW_MAX`
// check and newline snap-back (`packages/coding-agent/src/vibe/runtime.ts:1482-1488`)
// without ever slicing mid-UTF-8 rune.
func truncateResponseUTF16(s string, maxUnits int) (string, bool) {
	units := 0
	cutByte := -1
	for i, r := range s {
		ru := utf16.RuneLen(r)
		if ru < 0 {
			ru = 1
		}
		if units+ru > maxUnits {
			cutByte = i
			break
		}
		units += ru
	}
	if cutByte < 0 {
		return s, false
	}
	slice := s[:cutByte]
	if nl := strings.LastIndexByte(slice, '\n'); nl > 0 {
		slice = slice[:nl]
	}
	return slice, true
}

// RenderTurnResult renders the embedded `prompts/vibe-turn-result.md` template
// and applies omp's post-render prompt formatter (`prompt.FormatPrompt`).
// upstream: packages/coding-agent/src/prompts/tools/vibe-turn-result.md,
// packages/coding-agent/src/vibe/runtime.ts:1491-1508
func RenderTurnResult(d TurnResultData) string {
	vars := map[string]string{
		"id":        d.ID,
		"cli":       string(d.CLI),
		"turn":      strconv.Itoa(d.Turn),
		"status":    d.Status,
		"duration":  d.Duration,
		"requests":  strconv.Itoa(d.Requests),
		"toolCount": strconv.Itoa(d.ToolCount),
		"model":     d.Model,
		"response":  d.Response,
		"error":     d.Error,
	}
	if d.TraceOverflow > 0 {
		vars["traceOverflow"] = strconv.Itoa(d.TraceOverflow)
	}
	bools := map[string]bool{
		"model":             d.Model != "",
		"traceOverflow":     d.TraceOverflow > 0,
		"responseTruncated": d.ResponseTruncated,
		"error":             d.Error != "",
		"alive":             d.Alive,
	}
	lists := map[string][]string{
		"trace": d.Trace,
	}
	rendered := renderVibeTemplate(vibeTurnResultTemplate, vars, bools, lists)
	return strings.TrimSpace(prompt.FormatPrompt(rendered))
}

// RenderModeActivePrompt renders `prompts/vibe-mode-active.md` for todoAvailable
// and applies omp's post-render prompt formatter (`prompt.FormatPrompt`).
// upstream: packages/coding-agent/src/prompts/system/vibe-mode-active.md
func RenderModeActivePrompt(todoAvailable bool) string {
	rendered := renderVibeTemplate(vibeModeActiveTemplate, nil, map[string]bool{
		"todoAvailable": todoAvailable,
	}, nil)
	return strings.TrimSpace(prompt.FormatPrompt(rendered))
}

// renderVibeTemplate evaluates the minimal Handlebars subset used by Vibe's
// embedded templates (`{{#if key}}...{{/if}}`, `{{#each key}}...{{/each}}`,
// `{{key}}`, `{{this}}`) in a single pass so substituted values are never
// re-scanned or escaped.
func renderVibeTemplate(tpl string, vars map[string]string, bools map[string]bool, lists map[string][]string) string {
	var b strings.Builder
	i := 0
	for i < len(tpl) {
		openIdx := strings.Index(tpl[i:], "{{")
		if openIdx < 0 {
			b.WriteString(tpl[i:])
			break
		}
		openIdx += i
		b.WriteString(tpl[i:openIdx])
		closeIdx := strings.Index(tpl[openIdx+2:], "}}")
		if closeIdx < 0 {
			b.WriteString(tpl[openIdx:])
			break
		}
		closeIdx += openIdx + 2
		tag := strings.TrimSpace(tpl[openIdx+2 : closeIdx])
		next := closeIdx + 2

		switch {
		case strings.HasPrefix(tag, "#if "):
			key := strings.TrimSpace(tag[len("#if "):])
			endTag := "{{/if}}"
			endIdx := strings.Index(tpl[next:], endTag)
			if endIdx < 0 {
				b.WriteString(tpl[openIdx:next])
				i = next
				continue
			}
			body := tpl[next : next+endIdx]
			afterEnd := next + endIdx + len(endTag)
			standalone := isStandaloneTagLine(tpl, openIdx, afterEnd)
			if standalone {
				body = strings.TrimPrefix(body, "\n")
				if afterEnd < len(tpl) && tpl[afterEnd] == '\n' {
					afterEnd++
				}
			}
			if bools[key] {
				b.WriteString(renderVibeTemplate(body, vars, bools, lists))
			}
			i = afterEnd

		case strings.HasPrefix(tag, "#each "):
			key := strings.TrimSpace(tag[len("#each "):])
			endTag := "{{/each}}"
			endIdx := strings.Index(tpl[next:], endTag)
			if endIdx < 0 {
				b.WriteString(tpl[openIdx:next])
				i = next
				continue
			}
			body := tpl[next : next+endIdx]
			afterEnd := next + endIdx + len(endTag)
			standalone := isStandaloneTagLine(tpl, openIdx, afterEnd)
			if standalone {
				body = strings.TrimPrefix(body, "\n")
				if afterEnd < len(tpl) && tpl[afterEnd] == '\n' {
					afterEnd++
				}
			}
			for _, item := range lists[key] {
				b.WriteString(strings.ReplaceAll(body, "{{this}}", item))
			}
			i = afterEnd

		default:
			if val, ok := vars[tag]; ok {
				b.WriteString(val)
			}
			i = next
		}
	}
	return b.String()
}

func isStandaloneTagLine(s string, openIdx, afterEnd int) bool {
	if openIdx > 0 && s[openIdx-1] != '\n' {
		return false
	}
	return afterEnd >= len(s) || s[afterEnd] == '\n'
}

// defaultRunSubagent executes a worker's initial keep-alive turn using
// `internal/agents.RunSubagent` on PiG's child session API.
func defaultRunSubagent(ctx context.Context, req SpawnRequest) (agents.SingleResult, error) {
	if req.ParentSession == nil {
		return agents.SingleResult{}, errors.New("vibe: missing parent session")
	}
	var zero sdk.Context
	if req.ParentSession.SDK == zero {
		return agents.SingleResult{}, errors.New("vibe: child agent sessions require a live PiG host context")
	}
	cfg := agents.DefaultConfig()
	if req.AgentsRuntime != nil {
		cfg = req.AgentsRuntime.EffectiveConfig(req.ParentSession.SDK)
	}
	artifactsDir := req.ArtifactsDir
	if artifactsDir == "" {
		var err error
		artifactsDir, err = os.MkdirTemp("", "omp-vibe-*")
		if err != nil {
			return agents.SingleResult{}, err
		}
		if req.AgentRegistry != nil {
			req.AgentRegistry.RegisterArtifactsDirForSession(req.Scope.ParentSessionID, artifactsDir)
		}
	}
	cwd := req.ParentSession.Cwd
	if cwd == "" {
		cwd = req.ParentSession.SDK.Cwd()
	}
	noticeEnabled := cfg.SoftRequestBudgetNotice
	sr := agents.RunSubagent(ctx, req.AgentRegistry, agents.RunRequest{
		SDK:              req.ParentSession.SDK,
		ParentSessionID:  req.Scope.ParentSessionID,
		ParentAgentID:    req.Scope.OwnerID,
		Cwd:              cwd,
		TaskDepth:        0,
		Index:            0,
		ID:               req.ID,
		Agent:            req.Agent,
		Assignment:       req.Prompt,
		ModelOverride:    req.ModelOverride,
		MaxEffort:        cfg.MaxEffort,
		ArtifactsDir:     artifactsDir,
		MaxRuntimeMs:     cfg.MaxRuntimeMs,
		SoftBudget:       agents.ResolveSoftRequestBudget(req.Agent.Name, cfg.SoftRequestBudget, cfg.AgentSoftRequestBudget),
		SoftBudgetNotice: &noticeEnabled,
		// Disable idle parking for Vibe workers: port-wide cold revival is not
		// implemented (agents/UPSTREAM.md), so parking would dispose a live
		// worker session that the director still expects to continue with vibe_send.
		AgentIdleTTLMs: 0,
		MaxDepth:       cfg.MaxRecursionDepth,
		IRCEnabled:     true,
		KeepAlive:      true,
		OnProgress:     req.OnProgress,
	})
	sr.ModelRole = req.ModelRole
	return sr, nil
}

// defaultRunFollowUpTurn runs a follow-up turn directly on an idle live child
// `sdk.AgentSession` held in `agents.Registry`. If the worker has no live child
// session (e.g. parked or from a prior process), it fails truthfully because
// port-wide cold revival (`task/persisted-revive.ts`, see `internal/agents/UPSTREAM.md`)
// is not implemented.
func defaultRunFollowUpTurn(ctx context.Context, req FollowUpRequest) (agents.SingleResult, error) {
	if req.AgentRegistry == nil {
		return agents.SingleResult{}, fmt.Errorf("vibe: worker %q has no agent registry", req.ID)
	}
	ref, ok := req.AgentRegistry.GetInSession(req.Scope.ParentSessionID, req.ID)
	if !ok || ref == nil {
		return agents.SingleResult{}, fmt.Errorf("vibe: worker %q is no longer registered", req.ID)
	}
	if ref.Session == nil {
		return agents.SingleResult{}, fmt.Errorf(
			"vibe: worker %q is parked (transcript at history://%s); cold revival of parked child sessions is not ported (see internal/agents/UPSTREAM.md) — spawn a new worker with vibe_spawn",
			req.ID, req.ID,
		)
	}
	ref.StopIdleTimer()
	req.AgentRegistry.SetStatusInSession(req.Scope.ParentSessionID, req.ID, "running")

	session := ref.Session
	startTime := time.Now()
	var (
		mu             sync.Mutex
		progress       = agents.AgentProgress{ID: req.ID, Agent: req.Agent.Name, AgentSource: req.Agent.Source, Status: "running", Assignment: req.Message}
		activeTools    = map[string]struct{ tool, args, intent string }{}
		outputChunks   []string
		lastReportText string
		lastStopReason string
		lastErrorMsg   string
	)
	emit := func() {
		if req.OnProgress == nil {
			return
		}
		mu.Lock()
		p := progress
		p.DurationMs = time.Since(startTime).Milliseconds()
		p.RecentTools = append([]agents.RecentTool(nil), progress.RecentTools...)
		p.RecentOutput = append([]string(nil), progress.RecentOutput...)
		mu.Unlock()
		req.OnProgress(p)
	}

	unsub := session.Subscribe(func(event map[string]any) {
		evType, _ := event["type"].(string)
		nowMs := time.Now().UnixMilli()
		mu.Lock()
		switch evType {
		case "tool_execution_start":
			toolName, _ := event["toolName"].(string)
			callID, _ := event["toolCallId"].(string)
			argsMap, _ := event["args"].(map[string]any)
			intentStr, _ := event["intent"].(string)
			if intentStr == "" && argsMap != nil {
				intentStr, _ = argsMap["i"].(string)
			}
			argPreview := summarizeArgPreview(argsMap)
			progress.ToolCount++
			progress.CurrentTool = toolName
			progress.CurrentToolArgs = argPreview
			if intentStr != "" {
				progress.LastIntent = intentStr
			}
			activeTools[callID] = struct{ tool, args, intent string }{toolName, argPreview, intentStr}
		case "tool_execution_end":
			callID, _ := event["toolCallId"].(string)
			isErr, _ := event["isError"].(bool)
			if started, ok := activeTools[callID]; ok {
				delete(activeTools, callID)
				progress.RecentTools = append([]agents.RecentTool{{
					Tool:    started.tool,
					Args:    started.args,
					Intent:  started.intent,
					IsError: isErr,
					EndMs:   nowMs,
				}}, progress.RecentTools...)
				if len(progress.RecentTools) > 5 {
					progress.RecentTools = progress.RecentTools[:5]
				}
			}
			if len(activeTools) == 0 {
				progress.CurrentTool = ""
				progress.CurrentToolArgs = ""
			}
		case "message_update":
			if ame, ok := event["assistantMessageEvent"].(map[string]any); ok {
				if subType, _ := ame["type"].(string); subType == "text_delta" {
					if delta, _ := ame["delta"].(string); delta != "" {
						outputChunks = append(outputChunks, delta)
						progress.RecentOutput = appendTailLines(progress.RecentOutput, delta)
					}
				}
			}
		case "message_end":
			if msg, ok := event["message"].(map[string]any); ok && msg["role"] == "assistant" {
				progress.Requests++
				if sr, ok := msg["stopReason"].(string); ok && sr != "" {
					lastStopReason = sr
				}
				if em, ok := msg["errorMessage"].(string); ok && em != "" {
					lastErrorMsg = em
				}
				if txt := extractAssistantText(msg); txt != "" {
					lastReportText = txt
				}
			}
		}
		mu.Unlock()
		emit()
	})

	expand := false
	promptErr := session.Prompt(ctx, req.Message, sdk.PromptOptions{
		ExpandPromptTemplates: &expand,
		Source:                "extension",
	})
	unsub()

	if liveMsgs, err := session.Messages(); err == nil && len(liveMsgs) > 0 {
		ref.Messages = liveMsgs
	}

	mu.Lock()
	outText := strings.TrimSpace(lastReportText)
	if outText == "" {
		outText = strings.TrimSpace(strings.Join(outputChunks, ""))
	}
	exitCode := 0
	aborted := ctx.Err() != nil
	errMsg := ""
	abortReason := ""
	if aborted {
		exitCode = 1
		abortReason = "Cancelled by caller"
		errMsg = abortReason
	} else if promptErr != nil {
		exitCode = 1
		errMsg = promptErr.Error()
	} else if lastStopReason == "error" {
		exitCode = 1
		errMsg = lastErrorMsg
		if errMsg == "" {
			errMsg = "Subagent failed"
		}
	} else if lastStopReason == "aborted" {
		aborted = true
		exitCode = 1
		abortReason = lastErrorMsg
		if abortReason == "" {
			abortReason = "Aborted"
		}
		errMsg = abortReason
	}
	reqCount := progress.Requests
	lastIntent := progress.LastIntent
	mu.Unlock()

	if !aborted {
		req.AgentRegistry.SetStatusInSession(req.Scope.ParentSessionID, req.ID, "idle")
	} else {
		req.AgentRegistry.SetStatusInSession(req.Scope.ParentSessionID, req.ID, "aborted")
		session.Dispose()
		if session.SessionID != "" {
			lifecycle.End(session.SessionID)
		}
	}

	var outputPath string
	if req.ArtifactsDir != "" && outText != "" {
		mdPath := filepath.Join(req.ArtifactsDir, req.ID+".md")
		if err := os.WriteFile(mdPath, []byte(outText), 0o644); err == nil {
			outputPath = mdPath
		}
	}

	return agents.SingleResult{
		ID:          req.ID,
		Agent:       req.Agent.Name,
		AgentSource: req.Agent.Source,
		Assignment:  req.Message,
		LastIntent:  lastIntent,
		ExitCode:    exitCode,
		Output:      outText,
		Stderr:      errMsg,
		Error:       errMsg,
		Aborted:     aborted,
		AbortReason: abortReason,
		DurationMs:  time.Since(startTime).Milliseconds(),
		Requests:    reqCount,
		ModelRole:   req.ModelRole,
		OutputPath:  outputPath,
	}, nil
}

func summarizeArgPreview(args map[string]any) string {
	for _, k := range []string{"path", "file_path", "command", "pattern", "query", "message", "prompt"} {
		if s, ok := args[k].(string); ok && s != "" {
			return OneLineLabel(s, 60)
		}
	}
	return ""
}

func appendTailLines(lines []string, delta string) []string {
	parts := strings.Split(delta, "\n")
	if len(lines) == 0 {
		lines = append(lines, parts[0])
	} else {
		lines[len(lines)-1] += parts[0]
	}
	lines = append(lines, parts[1:]...)
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	return lines
}

func extractAssistantText(msg map[string]any) string {
	switch c := msg["content"].(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, item := range c {
			b, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := b["type"].(string); t == "text" {
				if s, ok := b["text"].(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// CalculateTokensPerSecond mirrors upstream `utils/token-rate.ts`
// `calculateTokensPerSecond`, reading the last assistant message's output
// tokens and duration.
// upstream: packages/coding-agent/src/utils/token-rate.ts:43-60
func CalculateTokensPerSecond(messages []map[string]any, isStreaming bool, nowMs int64) (float64, bool) {
	var assistant map[string]any
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m == nil {
			continue
		}
		if role, _ := m["role"].(string); role != "assistant" {
			continue
		}
		if _, ok := floatVal(m["timestamp"]); !ok {
			continue
		}
		usage, ok := m["usage"].(map[string]any)
		if !ok || usage == nil {
			continue
		}
		if _, ok := floatVal(usage["output"]); !ok {
			continue
		}
		assistant = m
		break
	}
	if assistant == nil {
		return 0, false
	}
	usage := assistant["usage"].(map[string]any)
	outputTokens, _ := floatVal(usage["output"])
	if math.IsNaN(outputTokens) || math.IsInf(outputTokens, 0) || outputTokens <= 0 {
		return 0, false
	}
	ts, _ := floatVal(assistant["timestamp"])
	var durationMs float64
	hasDuration := false
	if d, ok := floatVal(assistant["duration"]); ok && !math.IsNaN(d) && !math.IsInf(d, 0) && d > 0 {
		durationMs = d
		hasDuration = true
	} else if isStreaming {
		durationMs = float64(nowMs) - ts
		hasDuration = true
	}
	if !hasDuration || durationMs < MinTokenRateDurationMs {
		return 0, false
	}
	rate := (outputTokens * 1000) / durationMs
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return 0, false
	}
	return rate, true
}

func floatVal(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// AggregateWorkerTokensPerSecond sums tok/s across every live streaming Vibe
// worker owned by ownerID, returning (0, false) when none are streaming.
// upstream: packages/coding-agent/src/vibe/runtime.ts:1559-1576
func (r *SessionRegistry) AggregateWorkerTokensPerSecond(
	ownerID string,
	reg *agents.Registry,
	inspect func(ref *agents.AgentRef) (msgs []map[string]any, streaming bool),
) (float64, bool) {
	ids := r.ListIDsByOwner(ownerID)
	if len(ids) == 0 {
		return 0, false
	}
	if reg == nil {
		reg = agents.DefaultRuntime().Registry
	}
	if inspect == nil {
		inspect = func(ref *agents.AgentRef) ([]map[string]any, bool) {
			if ref == nil || ref.Session == nil {
				return nil, false
			}
			streaming, err := ref.Session.IsStreaming()
			if err != nil || !streaming {
				return nil, false
			}
			msgs, err := ref.Session.Messages()
			if err != nil {
				return nil, false
			}
			return msgs, true
		}
	}
	nowMs := r.now().UnixMilli()
	var total float64
	any := false
	for _, id := range ids {
		ref, ok := reg.Get(id)
		if !ok || ref == nil {
			continue
		}
		msgs, streaming := inspect(ref)
		if !streaming {
			continue
		}
		if rate, ok := CalculateTokensPerSecond(msgs, true, nowMs); ok {
			total += rate
			any = true
		}
	}
	return total, any
}

// AggregateVibeWorkerTokensPerSecond calls AggregateWorkerTokensPerSecond on
// the global Vibe SessionRegistry.
func AggregateVibeWorkerTokensPerSecond(ownerID string) (float64, bool) {
	return Global().AggregateWorkerTokensPerSecond(ownerID, agents.DefaultRuntime().Registry, nil)
}
