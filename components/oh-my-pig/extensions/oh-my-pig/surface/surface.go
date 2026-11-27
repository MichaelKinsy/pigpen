// Package surface says which drawing surface the session is on: Tern's native
// surface, or a terminal. omp has two code paths per tool, a terminal renderer
// and a native description; a kit view can serve only one of them well, so
// the renderers ask here which one to build.
//
// It is a public package of the extension module, not under internal/, so
// that the Piglet's frontend member (a separate Go module fused into the same
// Binary) can import it and report what it is doing. This file is the
// contract between that member and the renderers. Exported names and
// signatures are fixed; add, do not change.
package surface

import (
	"os"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
)

// Version is the oh-my-pig release version, the one place the terminal banner
// and the Tern welcome card take it from. It equals `version` in the Package's
// package.json and `release.version` in the Piglet's piglet.yaml; a test in the
// module root fails when it drifts from package.json.
const Version = "0.1.0"

// state is 0 until the frontend member reports, then 1 (terminal) or 2 (native).
var state atomic.Int32

var (
	mu        sync.Mutex
	listeners = map[int]func(native bool){}
	nextID    int
	// modelNames maps a model id to the name the extension shows for it.
	modelNames = map[string]string{}
	// modelCatalog maps "provider/id" (and fallback "id") to ModelMeta
	// published from PiG's ModelRegistry for the Tern model picker.
	modelCatalog = map[string]ModelMeta{}
	// pendingThinking holds a thinking level chosen on the model picker's
	// slider, applied by the extension when model_select fires or immediately.
	pendingThinking string
	thinkingApplier func(level string)
	// agentHubSnapshot holds the latest session subagent roster and cost summary
	// published by internal/agents for Tern's HUD pill, composer billing, and
	// native Agent Hub overlay.
	agentHubSnapshot  AgentHubSnapshot
	agentHubListeners = map[int]func(AgentHubSnapshot){}
	agentHubNextID    int
	agentHubHandlers  AgentHubHandlers
	agentHubOpenHook  func()
	// compactionMetas holds recent compaction metadata (method, tokensBefore,
	// tokensAfter, frameCount, summary) published by internal/snapcompact so
	// Tern draws omp's collapsible `section[omp.compaction]` divider instead of
	// PiG's purple ANSI `[compaction]` box.
	compactionMetas []CompactionMeta
)

// maxModelNames bounds modelNames; a session uses a handful of models.
const maxModelNames = 256

// SetModelDisplayName records the name the extension shows for a model id, so
// that the frontend member, which the host hands only the id, can show the
// same name. An empty id or name is ignored.
func SetModelDisplayName(id, name string) {
	if id == "" || name == "" {
		return
	}
	mu.Lock()
	if _, known := modelNames[id]; !known && len(modelNames) >= maxModelNames {
		clear(modelNames)
	}
	modelNames[id] = name
	mu.Unlock()
}

// ModelDisplayName is the name recorded for a model id, or "" when none was.
func ModelDisplayName(id string) string {
	mu.Lock()
	defer mu.Unlock()
	return modelNames[id]
}

// ModelMeta carries per-model catalog metadata published by the extension for
// Tern's native model picker (`Ctx`, `$/M`, `free` / `ctx>...` badges, and the
// per-model thinking slider).
type ModelMeta struct {
	ID                  string
	Provider            string
	Name                string
	ContextWindow       int
	InputCostPer1M      float64
	OutputCostPer1M     float64
	CacheReadCostPer1M  float64
	CacheWriteCostPer1M float64
	Reasoning           bool
	ThinkingLevels      []string
}

// maxModelCatalog bounds modelCatalog against unbounded growth.
const maxModelCatalog = 4096

// SetModels replaces the model metadata catalog used by the frontend's model
// picker (e.g. `Ctx`, `$/M`, `free` badge, and supported thinking levels).
// Also populates modelNames for any model with a non-empty Name.
func SetModels(models []ModelMeta) {
	mu.Lock()
	clear(modelCatalog)
	limit := min(len(models), maxModelCatalog)
	for i := range limit {
		m := models[i]
		if m.ID == "" {
			continue
		}
		if len(m.ThinkingLevels) > 0 {
			lvls := make([]string, len(m.ThinkingLevels))
			copy(lvls, m.ThinkingLevels)
			m.ThinkingLevels = lvls
		}
		if m.Provider != "" {
			modelCatalog[m.Provider+"/"+m.ID] = m
		}
		if _, exists := modelCatalog[m.ID]; !exists {
			modelCatalog[m.ID] = m
		}
		if m.Name != "" {
			if _, known := modelNames[m.ID]; !known && len(modelNames) < maxModelNames {
				modelNames[m.ID] = m.Name
			}
		}
	}
	mu.Unlock()
}

// UpsertModel records or updates one ModelMeta entry in the catalog.
func UpsertModel(m ModelMeta) {
	if m.ID == "" {
		return
	}
	if len(m.ThinkingLevels) > 0 {
		lvls := make([]string, len(m.ThinkingLevels))
		copy(lvls, m.ThinkingLevels)
		m.ThinkingLevels = lvls
	}
	mu.Lock()
	if len(modelCatalog) >= maxModelCatalog {
		clear(modelCatalog)
	}
	if m.Provider != "" {
		modelCatalog[m.Provider+"/"+m.ID] = m
	}
	modelCatalog[m.ID] = m
	mu.Unlock()
}

// LookupModel returns metadata for provider/id (falling back to bare id).
func LookupModel(provider, id string) (ModelMeta, bool) {
	if id == "" {
		return ModelMeta{}, false
	}
	mu.Lock()
	defer mu.Unlock()
	if provider != "" {
		if m, ok := modelCatalog[provider+"/"+id]; ok {
			return m, true
		}
	}
	if m, ok := modelCatalog[id]; ok {
		return m, true
	}
	return ModelMeta{}, false
}

// SetThinkingApplier registers the active extension's hook that applies a
// thinking level immediately via the host SDK (`ctx.SetThinkingLevel`). Passing
// nil clears it.
func SetThinkingApplier(fn func(level string)) {
	mu.Lock()
	thinkingApplier = fn
	mu.Unlock()
}

// RequestThinkingLevel records a thinking level chosen on Tern's model picker
// slider and applies it immediately if an extension hook is registered.
func RequestThinkingLevel(level string) {
	if level == "" {
		return
	}
	mu.Lock()
	pendingThinking = level
	fn := thinkingApplier
	mu.Unlock()
	if fn != nil {
		fn(level)
	}
}

// ConsumePendingThinkingLevel returns and clears any thinking level requested
// while the model picker was open.
func ConsumePendingThinkingLevel() string {
	mu.Lock()
	v := pendingThinking
	pendingThinking = ""
	mu.Unlock()
	return v
}

// AgentHubItem is one subagent row published by internal/agents for Tern's
// native HUD counter, composer subagent spend (+X.XX), and Agent Hub overlay.
type AgentHubItem struct {
	ID            string
	DisplayName   string
	AgentType     string // e.g. "scout", "reviewer", "task", "sonic"
	ParentID      string
	Status        string // "running" | "idle" | "parked" | "aborted"
	Task          string
	Activity      string
	Model         string
	ModelRole     string
	ThinkingLevel string
	Cost          float64
	DurationMs    int64
	StartedAtMs   int64 // Unix ms when current running turn began (for live elapsed clock)
	Requests      int
	Tools         int
	Tokens        int
	ContextTokens int
	ContextWindow int
	CreatedAtMs   int64
	LastActiveMs  int64
	SessionFile   string
	CanRevive     bool
}

// AgentHubActivity is one activity timeline event for the Agent Hub Activity tab.
type AgentHubActivity struct {
	ID          string
	AgentID     string
	Kind        string // "tool" | "response" | "irc" | "status"
	Title       string
	Detail      string
	Status      string // "ok" | "pending" | "error" | "aborted"
	TimestampMs int64
}

// AgentHubTranscriptEntry is one rendered message entry for an agent's transcript
// viewer overlay.
type AgentHubTranscriptEntry struct {
	Role        string // "user" | "assistant" | "tool" | "system"
	Text        string
	Thinking    string
	ToolName    string
	ToolArgs    string
	ToolOutput  string
	IsError     bool
	TimestampMs int64
}

// AgentHubSnapshot is the immutable subagent state snapshot for the current session.
type AgentHubSnapshot struct {
	RunningCount     int
	InspectableCount int
	SubagentCost     float64
	TotalDurationMs  int64
	TotalRequests    int
	TotalTokens      int
	TotalTools       int
	Agents           []AgentHubItem
	Activity         []AgentHubActivity
}

// AgentHubHandlers connects Tern's native Agent Hub overlay actions back to
// internal/agents (transcript loading, revive, kill, and steering/IRC send).
type AgentHubHandlers struct {
	LoadTranscript func(agentID string) []AgentHubTranscriptEntry
	Kill           func(agentID string) (string, error)
	Revive         func(agentID string) (string, error)
	SendMessage    func(agentID, body string) error
}

// SetAgentHubSnapshot publishes the latest subagent roster and aggregate metrics
// from internal/agents and notifies subscribed frontend listeners.
func SetAgentHubSnapshot(snap AgentHubSnapshot) {
	cp := cloneAgentHubSnapshot(snap)
	mu.Lock()
	agentHubSnapshot = cp
	fns := make([]func(AgentHubSnapshot), 0, len(agentHubListeners))
	for _, fn := range agentHubListeners {
		fns = append(fns, fn)
	}
	mu.Unlock()
	for _, fn := range fns {
		fn(cloneAgentHubSnapshot(cp))
	}
}

// GetAgentHubSnapshot returns a copy of the latest published AgentHubSnapshot.
func GetAgentHubSnapshot() AgentHubSnapshot {
	mu.Lock()
	cp := cloneAgentHubSnapshot(agentHubSnapshot)
	mu.Unlock()
	return cp
}

// OnAgentHubChange subscribes fn to subagent roster/metric updates and returns
// an unsubscribe function.
func OnAgentHubChange(fn func(AgentHubSnapshot)) func() {
	if fn == nil {
		return func() {}
	}
	mu.Lock()
	agentHubNextID++
	id := agentHubNextID
	agentHubListeners[id] = fn
	mu.Unlock()
	return func() {
		mu.Lock()
		delete(agentHubListeners, id)
		mu.Unlock()
	}
}

// SetAgentHubHandlers registers the extension's callbacks for Agent Hub actions.
func SetAgentHubHandlers(h AgentHubHandlers) {
	mu.Lock()
	agentHubHandlers = h
	mu.Unlock()
}

// GetAgentHubHandlers returns the registered Agent Hub callbacks.
func GetAgentHubHandlers() AgentHubHandlers {
	mu.Lock()
	h := agentHubHandlers
	mu.Unlock()
	return h
}

// SetAgentHubOpener registers the frontend's callback that opens the native
// Agent Hub overlay in Tern (used by `/agents`).
func SetAgentHubOpener(fn func()) {
	mu.Lock()
	agentHubOpenHook = fn
	mu.Unlock()
}

// OpenAgentHub triggers the registered frontend Agent Hub opener and reports
// whether a native opener handled it.
func OpenAgentHub() bool {
	mu.Lock()
	fn := agentHubOpenHook
	mu.Unlock()
	if fn == nil {
		return false
	}
	fn()
	return true
}

func cloneAgentHubSnapshot(s AgentHubSnapshot) AgentHubSnapshot {
	if len(s.Agents) > 0 {
		cp := make([]AgentHubItem, len(s.Agents))
		copy(cp, s.Agents)
		s.Agents = cp
	}
	if len(s.Activity) > 0 {
		cp := make([]AgentHubActivity, len(s.Activity))
		copy(cp, s.Activity)
		s.Activity = cp
	}
	return s
}

// CompactionMeta holds display metadata for one compaction entry published by
// internal/snapcompact so Tern renders omp's native `section[omp.compaction]`
// divider with exact method label, before → after tokens, frame count, and
// expandable summary.
type CompactionMeta struct {
	Method       string // "snapcompact" | "soft" | "remote" | "handoff" | "shake" | ""
	TokensBefore int
	TokensAfter  int
	FrameCount   int
	Summary      string
	Warning      string
}

const maxCompactionMetas = 64

// RecordCompaction records metadata for a completed compaction entry.
func RecordCompaction(m CompactionMeta) {
	if m.TokensBefore <= 0 && m.Summary == "" && m.Method == "" {
		return
	}
	mu.Lock()
	updated := false
	for i := len(compactionMetas) - 1; i >= 0; i-- {
		if compactionMetas[i].TokensBefore == m.TokensBefore && (m.Summary == "" || compactionMetas[i].Summary == "" || compactionMetas[i].Summary == m.Summary) {
			compactionMetas[i] = m
			updated = true
			break
		}
	}
	if !updated {
		compactionMetas = append(compactionMetas, m)
		if len(compactionMetas) > maxCompactionMetas {
			compactionMetas = compactionMetas[len(compactionMetas)-maxCompactionMetas:]
		}
	}
	mu.Unlock()
}

// SetCompactions replaces the recorded compaction metadata list (e.g. on
// session start/resume/tree switch).
func SetCompactions(list []CompactionMeta) {
	mu.Lock()
	if len(list) > maxCompactionMetas {
		list = list[len(list)-maxCompactionMetas:]
	}
	if len(list) == 0 {
		compactionMetas = nil
	} else {
		cp := make([]CompactionMeta, len(list))
		copy(cp, list)
		compactionMetas = cp
	}
	mu.Unlock()
}

// LookupCompaction finds the most recent CompactionMeta matching tokensBefore
// (or the latest entry when tokensBefore <= 0).
func LookupCompaction(tokensBefore int) (CompactionMeta, bool) {
	mu.Lock()
	defer mu.Unlock()
	for i := len(compactionMetas) - 1; i >= 0; i-- {
		if tokensBefore > 0 && compactionMetas[i].TokensBefore == tokensBefore {
			return compactionMetas[i], true
		}
	}
	if tokensBefore <= 0 && len(compactionMetas) > 0 {
		return compactionMetas[len(compactionMetas)-1], true
	}
	return CompactionMeta{}, false
}

// DisplayPath is p as omp spells it for the user and the model: on macOS
// without the leading /private when both spellings name the same place. The
// frontend member uses it for the paths and links it draws, so that they match
// what the extension's tools print.
func DisplayPath(p string) string { return dirs.StandardizeMacOSPath(p) }

// SetNative is called by the frontend member: true once the native surface is
// open and drawing, false when it has fallen back to PiG's terminal renderer
// or closed.
func SetNative(native bool) {
	next := int32(1)
	if native {
		next = 2
	}
	if state.Swap(next) == next {
		return
	}
	mu.Lock()
	fns := make([]func(bool), 0, len(listeners))
	for _, fn := range listeners {
		fns = append(fns, fn)
	}
	mu.Unlock()
	for _, fn := range fns {
		fn(native)
	}
}

// Native reports whether results are being drawn on Tern's native surface.
// Before the frontend member has reported (or in a Binary without it) it
// answers from the environment the way the member decides to try: inside a
// Tern pane and not switched off.
func Native() bool {
	switch state.Load() {
	case 1:
		return false
	case 2:
		return true
	}
	if os.Getenv("PIG_TUI_NATIVE") == "0" {
		return false
	}
	return os.Getenv("TERM_PROGRAM") == "tern" && os.Getenv("TERN_PANE") != ""
}

// OnChange registers fn to be called when the surface changes, and returns
// the function that removes it. Renderers use it to invalidate what they drew
// for the other surface.
func OnChange(fn func(native bool)) func() {
	mu.Lock()
	nextID++
	id := nextID
	listeners[id] = fn
	mu.Unlock()
	return func() {
		mu.Lock()
		delete(listeners, id)
		mu.Unlock()
	}
}

// ResetForTests forgets what the member reported and every listener.
func ResetForTests() {
	state.Store(0)
	mu.Lock()
	listeners = map[int]func(bool){}
	clear(modelNames)
	clear(modelCatalog)
	pendingThinking = ""
	thinkingApplier = nil
	agentHubSnapshot = AgentHubSnapshot{}
	agentHubListeners = map[int]func(AgentHubSnapshot){}
	agentHubHandlers = AgentHubHandlers{}
	agentHubOpenHook = nil
	compactionMetas = nil
	mu.Unlock()
}
