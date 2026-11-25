package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// packages/coding-agent/src/irc/messaging.ts,
// packages/coding-agent/src/irc/bus.ts

// MainAgentID is omp's MAIN_AGENT_ID.
const MainAgentID = "Main"

// AgentRef is one entry in the process-wide agent registry.
// upstream: packages/coding-agent/src/registry/agent-registry.ts:67
type AgentRef struct {
	mu             sync.Mutex
	ID             string
	DisplayName    string
	Kind           string // "main" | "sub"
	ParentID       string
	Status         string // "running" | "idle" | "parked" | "aborted"
	SessionID      string // parent PiG session key (for main: the root session ID)
	ChildSessionID string // child PiG session ID (`AgentSession.SessionID`)
	Session        *sdk.AgentSession
	SessionFile    string
	OutputDir      string
	OutputPath     string
	CreatedAt      time.Time
	LastActive     time.Time
	Activity       string
	Messages       []map[string]any // retained transcript snapshot for parked/in-memory reads
	// PendingIRC holds messages queued before a child session opened.
	PendingIRC []IRCMessage
	// OnDeliver delivers an IRC message to a host-owned session (used for Main).
	OnDeliver func(msg IRCMessage) IrcDeliveryReceipt
	// OnWake wakes an idle/parked subagent to process a newly delivered message.
	// Returns true iff it took ownership of the live session and launched the wake turn.
	OnWake    func(msg IRCMessage) bool
	idleTimer *time.Timer
	// insertSeq preserves Map insertion order (matching JS Map iteration order in
	// packages/coding-agent/src/registry/agent-registry.ts:202,448-461) as the
	// stable tie-breaker when two peers share status and LastActive.
	insertSeq uint64
}

// IRCMessage is a peer message sent via `write agent://<id>`.
// upstream: packages/tui/src/tools/irc.ts (IrcMessage)
type IRCMessage struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Body      string `json:"body"`
	ReplyTo   string `json:"replyTo,omitempty"`
	Timestamp int64  `json:"ts"`
	Broadcast bool   `json:"broadcast,omitempty"`
	Progress  bool   `json:"progress,omitempty"`
	WakeRelay bool   `json:"wakeRelay,omitempty"`
}

// FormatIncomingIRC renders prompts/system/irc-incoming.md for delivery to a live session.
// upstream: packages/coding-agent/src/prompts/system/irc-incoming.md, session/irc-bridge.ts:231-242
func FormatIncomingIRC(msg IRCMessage, relayOnStop bool) string {
	return FormatIncomingIRCWithOptions(msg, relayOnStop, false)
}

// FormatIncomingIRCWithOptions renders prompts/system/irc-incoming.md with full options.
func FormatIncomingIRCWithOptions(msg IRCMessage, relayOnStop, interrupting bool) string {
	var b strings.Builder
	b.WriteString("<irc>\nIncoming IRC message from agent `")
	b.WriteString(msg.From)
	b.WriteByte('`')
	if msg.ReplyTo != "" {
		b.WriteString(" (reply to ")
		b.WriteString(msg.ReplyTo)
		b.WriteByte(')')
	}
	if msg.Progress {
		b.WriteString(" (progress; its reply follows later)")
	}
	b.WriteString(":\n\n")
	b.WriteString(EscapeHarnessTags(msg.Body))
	b.WriteString("\n")
	if interrupting {
		b.WriteString("\nSent while waiting/working. Active interruptible wait stopped early for immediate reading.\n")
	}
	if !msg.Progress {
		b.WriteString("\n")
		if relayOnStop {
			b.WriteString("If response expected, reply via `write` (`path: \"agent://")
			b.WriteString(msg.From)
			b.WriteString("\"`, `content: \"…\"`), when available; otherwise what you `yield` or say last this turn is delivered to `")
			b.WriteString(msg.From)
			b.WriteString("` when you stop.")
		} else {
			b.WriteString("If response expected, reply via `write` (`path: \"agent://")
			b.WriteString(msg.From)
			b.WriteString("\"`, `content: \"…\"`); may finish current step first. No one replies on your behalf.")
		}
		b.WriteString(" An acknowledgement or progress note before the real answer goes to `agent://")
		b.WriteString(msg.From)
		b.WriteString("?progress`, so `")
		b.WriteString(msg.From)
		b.WriteString("` keeps waiting for the answer.\n")
	}
	b.WriteString("</irc>")
	return b.String()
}

// FormatParentIRCSteer renders omp's prompts/steering/parent-irc.md for a
// parent steering a currently running child.
// upstream: packages/coding-agent/src/prompts/steering/parent-irc.md, session/irc-bridge.ts:263-266
func FormatParentIRCSteer(from, body string) string {
	var b strings.Builder
	b.WriteString("[Wait interrupted by message]\n<irc from=\"parent\" agent=\"")
	b.WriteString(from)
	b.WriteString("\">\n")
	b.WriteString(EscapeHarnessTags(body))
	b.WriteString("\n</irc>")
	return b.String()
}

// ChildSessionMeta records per-child-session metadata keyed by the host-assigned
// child SessionID (`AgentSession.SessionID`). It lets the fused extension
// instance loaded inside the child enforce recursion depth, spawn policy,
// parent local:// root sharing, and `IsSubagent(ctx)` checks across nesting depths.
type ChildSessionMeta struct {
	ParentSessionID string
	LocalRoot       string
	Depth           int
	Spawns          string // "*", "scout,reviewer", or ""
	AgentID         string
	AgentName       string
	// ExplicitTools records the child session's resolved explicit tool list in
	// caller order when the agent declared frontmatter `tools:`, matching omp's
	// `toolNames` passed to `createAgentSession` (`task/executor.ts:4800-4830`,
	// `tools/index.ts:863-869`). Nil when the child uses omp's default toolset.
	ExplicitTools []string
}

// Registry is the process-wide registry of live, idle, and parked agents plus
// extra artifact directories for `agent://` and `history://` resolution.
// upstream: packages/coding-agent/src/registry/agent-registry.ts
type Registry struct {
	mu   sync.RWMutex
	refs map[string]*AgentRef
	// extraArtDirs maps sessionID ("" for unscoped legacy callers) -> dir -> refcount.
	extraArtDirs map[string]map[string]int
	// childSessions maps a child session's host SessionID (`AgentSession.SessionID`)
	// to its lineage, recursion depth, spawn policy, and shared local:// root.
	childSessions map[string]ChildSessionMeta
	nextMsgID     int64
	nextInsert    uint64
	listeners     map[int]func()
	nextListenID  int
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		refs:          map[string]*AgentRef{},
		extraArtDirs:  map[string]map[string]int{},
		childSessions: map[string]ChildSessionMeta{},
		listeners:     map[int]func(){},
	}
}

// RegisterChildSession records lineage and policy metadata for a newly created
// child AgentSession before BindExtensions / Prompt runs, so every hook and
// tool invoked inside the child (including session_start and before_agent_start)
// observes IsSubagent, ParentSession, depth, and spawns.
func (r *Registry) RegisterChildSession(childSessionID string, meta ChildSessionMeta) {
	if childSessionID == "" {
		return
	}
	if len(meta.ExplicitTools) > 0 {
		meta.ExplicitTools = append([]string(nil), meta.ExplicitTools...)
	} else {
		meta.ExplicitTools = nil
	}
	r.mu.Lock()
	r.childSessions[childSessionID] = meta
	r.mu.Unlock()
}

// UnregisterChildSession removes lineage metadata for a disposed child session.
func (r *Registry) UnregisterChildSession(childSessionID string) {
	if childSessionID == "" {
		return
	}
	r.mu.Lock()
	delete(r.childSessions, childSessionID)
	r.mu.Unlock()
}

// ChildMeta returns the recorded ChildSessionMeta for sessionID, if any.
func (r *Registry) ChildMeta(sessionID string) (ChildSessionMeta, bool) {
	if sessionID == "" {
		return ChildSessionMeta{}, false
	}
	r.mu.RLock()
	meta, ok := r.childSessions[sessionID]
	r.mu.RUnlock()
	if ok && len(meta.ExplicitTools) > 0 {
		meta.ExplicitTools = append([]string(nil), meta.ExplicitTools...)
	}
	return meta, ok
}

// ResolveTopParent walks the recorded child→parent chain for sessionID and
// returns the top-level ancestor session ID plus whether sessionID is a child.
func (r *Registry) ResolveTopParent(sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	meta, isChild := r.childSessions[sessionID]
	if !isChild {
		return "", false
	}
	cur := meta.ParentSessionID
	visited := map[string]bool{sessionID: true}
	for cur != "" {
		if visited[cur] {
			break
		}
		visited[cur] = true
		next, ok := r.childSessions[cur]
		if !ok || next.ParentSessionID == "" {
			break
		}
		cur = next.ParentSessionID
	}
	return cur, true
}

// ChildLocalRoot returns the top-level local:// root recorded for a child session.
func (r *Registry) ChildLocalRoot(sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cur := sessionID
	visited := map[string]bool{}
	for cur != "" {
		if visited[cur] {
			break
		}
		visited[cur] = true
		meta, ok := r.childSessions[cur]
		if !ok {
			break
		}
		if meta.ParentSessionID != "" {
			if _, parentIsChild := r.childSessions[meta.ParentSessionID]; parentIsChild {
				cur = meta.ParentSessionID
				continue
			}
		}
		if meta.LocalRoot != "" {
			return meta.LocalRoot, true
		}
		break
	}
	return "", false
}

// TaskDepthFor returns the recursion depth of sessionID (0 for top-level).
func (r *Registry) TaskDepthFor(sessionID string) int {
	if meta, ok := r.ChildMeta(sessionID); ok && meta.Depth > 0 {
		return meta.Depth
	}
	return 0
}

// SessionSpawnsFor returns the spawn policy string ("*", "scout", or "") for sessionID.
// Top-level sessions default to "*".
func (r *Registry) SessionSpawnsFor(sessionID string) string {
	if meta, ok := r.ChildMeta(sessionID); ok {
		return meta.Spawns
	}
	return "*"
}

// ParentSession reports whether ctx belongs to a child subagent session created
// by the agents executor (at any nesting depth) and returns the top-level
// parent session ID.
func (r *Registry) ParentSession(ctx sdk.Context) (parentSessionID string, isSubagent bool) {
	var zero sdk.Context
	if ctx == zero {
		return "", false
	}
	sid, err := ctx.SessionManager().GetSessionID()
	if err != nil || sid == "" {
		return "", false
	}
	return r.ResolveTopParent(sid)
}

// ParentSession reports whether ctx belongs to a child subagent session created
// by DefaultRuntime's executor (at any nesting depth) and returns the top-level
// parent session ID.
// upstream: packages/coding-agent/src/task/executor.ts:5309 (shared parent local:// root)
func ParentSession(ctx sdk.Context) (parentSessionID string, isSubagent bool) {
	return defaultRuntime.Registry.ParentSession(ctx)
}

// IsSubagent reports whether ctx belongs to a child subagent session created by
// DefaultRuntime's executor (at any depth).
func IsSubagent(ctx sdk.Context) bool {
	_, isSub := ParentSession(ctx)
	return isSub
}

// ParentLocalRoot returns the top-level parent's `local://` directory for a
// child subagent session, suitable for wiring into `urls.SetLocalRootProvider`.
func ParentLocalRoot(ctx sdk.Context) (string, bool) {
	var zero sdk.Context
	if ctx == zero {
		return "", false
	}
	sid, err := ctx.SessionManager().GetSessionID()
	if err != nil || sid == "" {
		return "", false
	}
	return defaultRuntime.Registry.ChildLocalRoot(sid)
}

// ChildExplicitTools returns a copy of the explicit tool list recorded for
// sessionID, or nil when sessionID has no explicit tool list (including
// top-level sessions and subagents without frontmatter `tools:`).
// upstream: packages/coding-agent/src/tools/index.ts:862-869, task/executor.ts:4800-4830
func (r *Registry) ChildExplicitTools(sessionID string) []string {
	meta, ok := r.ChildMeta(sessionID)
	if !ok || len(meta.ExplicitTools) == 0 {
		return nil
	}
	return meta.ExplicitTools
}

// ExplicitTools returns the explicit tool order for ctx's session when the
// session was created with an explicit tool list (e.g. a subagent whose agent
// definition declares frontmatter `tools:`, with upstream's appended `task` /
// `wait` / `yield` names preserved in order), or nil otherwise (including the
// top-level session and subagents without frontmatter `tools:`).
//
// Suitable for wiring into `toolcmd.ExplicitToolOrder = agents.ExplicitTools`.
// Released automatically with the session through `internal/lifecycle`.
// upstream: packages/coding-agent/src/tools/index.ts:862-869, task/executor.ts:4800-4830
func ExplicitTools(ctx sdk.Context) []string {
	var zero sdk.Context
	if ctx == zero {
		return nil
	}
	sid, err := ctx.SessionManager().GetSessionID()
	if err != nil || sid == "" {
		return nil
	}
	return defaultRuntime.Registry.ChildExplicitTools(sid)
}

func (r *Registry) notify() {
	r.mu.RLock()
	fns := make([]func(), 0, len(r.listeners))
	for _, fn := range r.listeners {
		fns = append(fns, fn)
	}
	r.mu.RUnlock()
	for _, fn := range fns {
		fn()
	}
}

// OnChange subscribes to registry status/message changes.
func (r *Registry) OnChange(fn func()) func() {
	r.mu.Lock()
	r.nextListenID++
	id := r.nextListenID
	r.listeners[id] = fn
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.listeners, id)
		r.mu.Unlock()
	}
}

// refKey builds the internal map key for (sessionID, agentID) so concurrent
// top-level sessions in one process never collide when both spawn an agent
// with the same ID (e.g. `Alpha` or `Main`).
func refKey(sessionID, id string) string {
	if sessionID == "" {
		return id
	}
	return sessionID + "\x00" + id
}

// Register adds or replaces an AgentRef, keyed by (ref.SessionID, ref.ID).
// Matching JS Map.set semantics (`registry/agent-registry.ts:236`), inserting a
// new key appends to insertion order while replacing an existing key preserves
// its original insertion slot.
func (r *Registry) Register(ref *AgentRef) {
	if ref == nil {
		return
	}
	now := time.Now()
	if ref.CreatedAt.IsZero() {
		ref.CreatedAt = now
	}
	if ref.LastActive.IsZero() {
		ref.LastActive = now
	}
	if ref.Status == "" {
		ref.Status = "running"
	}
	key := refKey(ref.SessionID, ref.ID)
	r.mu.Lock()
	if prev, ok := r.refs[key]; ok && prev != nil {
		prev.mu.Lock()
		prevSeq := prev.insertSeq
		prev.mu.Unlock()
		if prevSeq != 0 {
			ref.insertSeq = prevSeq
		}
	}
	if ref.insertSeq == 0 {
		r.nextInsert++
		ref.insertSeq = r.nextInsert
	}
	r.refs[key] = ref
	r.mu.Unlock()
	r.notify()
}

// Get looks up an agent by exact ID across all sessions (for legacy/unscoped callers).
// Prefer GetInSession when a session scope is known.
func (r *Registry) Get(id string) (*AgentRef, bool) {
	return r.GetInSession("", id)
}

// GetInSession looks up an agent by exact ID scoped to sessionID.
// When sessionID is non-empty, only refs belonging to sessionID (or unscoped refs
// with empty SessionID) are returned; another session's ref with the same ID is
// never returned. When sessionID is empty, an unscoped ref is checked first and
// then a unique exact-ID match across sessions.
func (r *Registry) GetInSession(sessionID, id string) (*AgentRef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sessionID != "" {
		if ref, ok := r.refs[refKey(sessionID, id)]; ok {
			return ref, true
		}
		if ref, ok := r.refs[id]; ok && ref.SessionID == "" {
			return ref, true
		}
		return nil, false
	}
	if ref, ok := r.refs[id]; ok {
		return ref, true
	}
	var match *AgentRef
	for _, ref := range r.refs {
		if ref.ID == id {
			if match != nil {
				return nil, false // ambiguous across sessions
			}
			match = ref
		}
	}
	return match, match != nil
}

// Lookup finds an agent by exact ID, falling back to case-insensitive match.
// Prefer LookupInSession when a session scope is known.
func (r *Registry) Lookup(id string) (*AgentRef, bool) {
	return r.LookupInSession("", id)
}

// List returns all registered refs sorted by creation time, then ID.
// Prefer ListInSession when a session scope is known.
func (r *Registry) List() []*AgentRef {
	return r.ListInSession("")
}

// ListInSession returns registered refs belonging to sessionID (or all refs when
// sessionID == ""), sorted by creation time, then ID.
func (r *Registry) ListInSession(sessionID string) []*AgentRef {
	r.mu.RLock()
	out := make([]*AgentRef, 0, len(r.refs))
	for _, ref := range r.refs {
		if sessionID != "" && ref.SessionID != "" && ref.SessionID != sessionID {
			continue
		}
		out = append(out, ref)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// ListVisibleTo returns alive peers visible to senderID (excluding senderID itself).
func (r *Registry) ListVisibleTo(senderID string) []*AgentRef {
	return r.ListVisibleInSession("", senderID)
}

// Unregister removes an agent from the registry (scoped to sessionID when non-empty).
func (r *Registry) Unregister(id string) {
	r.UnregisterInSession("", id)
}

// UnregisterInSession removes (sessionID, id) from the registry.
func (r *Registry) UnregisterInSession(sessionID, id string) {
	r.mu.Lock()
	if sessionID != "" {
		delete(r.refs, refKey(sessionID, id))
	} else {
		delete(r.refs, id)
		for k, ref := range r.refs {
			if ref.ID == id {
				delete(r.refs, k)
			}
		}
	}
	r.mu.Unlock()
	r.notify()
}

// RegisterArtifactsDir tracks a temporary artifacts directory for unscoped URL lookups.
func (r *Registry) RegisterArtifactsDir(dir string) func() {
	return r.RegisterArtifactsDirForSession("", dir)
}

// RegisterArtifactsDirForSession tracks a temporary artifacts directory scoped
// to sessionID so concurrent sessions do not scan each other's artifacts.
func (r *Registry) RegisterArtifactsDirForSession(sessionID, dir string) func() {
	if dir == "" {
		return func() {}
	}
	clean := filepath.Clean(dir)
	r.mu.Lock()
	m := r.extraArtDirs[sessionID]
	if m == nil {
		m = map[string]int{}
		r.extraArtDirs[sessionID] = m
	}
	m[clean]++
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if m := r.extraArtDirs[sessionID]; m != nil {
			if n := m[clean]; n <= 1 {
				delete(m, clean)
				if len(m) == 0 {
					delete(r.extraArtDirs, sessionID)
				}
			} else {
				m[clean] = n - 1
			}
		}
		r.mu.Unlock()
	}
}

// ArtifactsDirs returns registered artifact directories in priority order,
// preferredDir first if non-empty.
// upstream: packages/coding-agent/src/internal-urls/registry-helpers.ts:46
func (r *Registry) ArtifactsDirs(preferredDir string) []string {
	return r.ArtifactsDirsForSession("", preferredDir)
}

// ArtifactsDirsForSession returns artifact directories scoped to sessionID
// (or all registered directories when sessionID == ""), preferredDir first.
func (r *Registry) ArtifactsDirsForSession(sessionID, preferredDir string) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" {
			return
		}
		clean := filepath.Clean(d)
		if seen[clean] {
			return
		}
		seen[clean] = true
		dirs = append(dirs, clean)
	}
	add(preferredDir)
	for _, ref := range r.ListInSession(sessionID) {
		ref.mu.Lock()
		outDir := ref.OutputDir
		sessFile := ref.SessionFile
		ref.mu.Unlock()
		add(outDir)
		if sessFile != "" {
			add(artifactsDirForSession(sessFile))
		}
	}
	r.mu.RLock()
	var extras []string
	if sessionID != "" {
		for d := range r.extraArtDirs[sessionID] {
			extras = append(extras, d)
		}
		for d := range r.extraArtDirs[""] {
			extras = append(extras, d)
		}
	} else {
		for _, m := range r.extraArtDirs {
			for d := range m {
				extras = append(extras, d)
			}
		}
	}
	r.mu.RUnlock()
	sort.Strings(extras)
	for _, d := range extras {
		add(d)
	}
	return dirs
}

// SetStatus transitions a registered agent's status safely.
func (r *Registry) SetStatus(id, status string) {
	r.SetStatusInSession("", id, status)
}

// SetStatusInSession transitions (sessionID, id)'s status safely.
func (r *Registry) SetStatusInSession(sessionID, id, status string) {
	ref, ok := r.GetInSession(sessionID, id)
	if !ok {
		return
	}
	ref.mu.Lock()
	if ref.Status == status {
		ref.mu.Unlock()
		return
	}
	ref.Status = status
	if status != "running" {
		ref.Activity = ""
	}
	ref.LastActive = time.Now()
	ref.mu.Unlock()
	r.notify()
}

// SettleIfRunning transitions (sessionID, id) from "running" to status and
// drops any PendingIRC messages held for a child that never opened a session.
// No-op if the ref is absent or has already settled (e.g. "idle", "parked",
// "aborted"). Safe to call from a defer on every child launch exit path.
func (r *Registry) SettleIfRunning(sessionID, id, status string) {
	if r == nil || id == "" {
		return
	}
	ref, ok := r.GetInSession(sessionID, id)
	if !ok || ref == nil {
		return
	}
	ref.mu.Lock()
	if ref.Status != "running" {
		ref.mu.Unlock()
		return
	}
	ref.Status = status
	ref.Session = nil
	ref.OnWake = nil
	ref.PendingIRC = nil
	ref.LastActive = time.Now()
	ref.mu.Unlock()
	r.notify()
}

// SetActivity records a short display-only activity string for the peer roster.
func (r *Registry) SetActivity(id, activity string) {
	r.SetActivityInSession("", id, activity)
}

// SetActivityInSession records a short display-only activity string for (sessionID, id).
func (r *Registry) SetActivityInSession(sessionID, id, activity string) {
	ref, ok := r.GetInSession(sessionID, id)
	if !ok {
		return
	}
	ref.mu.Lock()
	if ref.Status != "running" {
		ref.mu.Unlock()
		return
	}
	ref.Activity = OneLineLabel(activity)
	ref.LastActive = time.Now()
	ref.mu.Unlock()
}

// SnapshotMessages returns the best-effort transcript messages for ref:
// live session Messages() first, then retained in-memory snapshot, then JSONL file.
func (ref *AgentRef) SnapshotMessages() (msgs []map[string]any, source string) {
	ref.mu.Lock()
	sess := ref.Session
	cached := ref.Messages
	file := ref.SessionFile
	status := ref.Status
	ref.mu.Unlock()

	if sess != nil {
		if live, err := sess.Messages(); err == nil && len(live) > 0 {
			return live, "live session"
		}
	}
	if len(cached) > 0 {
		if sess != nil {
			return cached, "live session"
		}
		return cached, "in-memory transcript (" + status + ")"
	}
	if file != "" {
		if loaded, err := LoadSessionMessagesReadOnly(file); err == nil {
			return loaded, "session file (read-only, " + status + ")"
		}
	}
	return nil, "no transcript"
}

// IsResumable reports whether messaging ref can resume or follow up with it.
// upstream: packages/coding-agent/src/registry/agent-registry.ts:102
func (ref *AgentRef) IsResumable() bool {
	if ref == nil {
		return false
	}
	ref.mu.Lock()
	defer ref.mu.Unlock()
	if ref.Status == "idle" && ref.Session != nil {
		return true
	}
	return (ref.Status == "parked" || ref.Status == "aborted") && (ref.Session != nil || ref.SessionFile != "")
}

// SendMessage delivers an IRC message to targetID (or broadcasts to "all")
// within the calling session scope.
// upstream: packages/coding-agent/src/irc/messaging.ts:23
func (r *Registry) SendMessage(senderID, target, body string, progress bool) (string, CoordinationDetails, bool) {
	return r.SendMessageInSession("", senderID, target, body, progress, "", false)
}

// SendMessageInSession delivers an IRC message scoped to sessionID (when non-empty)
// so multiple concurrent top-level sessions in one process each resolve their own Main
// and peers without cross-session leakage.
func (r *Registry) SendMessageInSession(sessionID, senderID, target, body string, progress bool, replyTo string, wakeRelay bool) (string, CoordinationDetails, bool) {
	to := strings.TrimSpace(target)
	if to == "" {
		return "A recipient is required.", CoordinationDetails{Op: "send", From: senderID}, true
	}
	if strings.TrimSpace(body) == "" {
		return "A non-empty message is required.", CoordinationDetails{Op: "send", From: senderID, To: to}, true
	}
	if to == senderID {
		return "Cannot send a message to yourself.", CoordinationDetails{Op: "send", From: senderID, To: to}, true
	}

	isBroadcast := to == "all"
	if isBroadcast {
		if progress {
			return "agent://all broadcasts cannot be ?progress updates.", CoordinationDetails{Op: "send", From: senderID, To: to}, true
		}
		if senderID != MainAgentID {
			if senderRef, ok := r.GetInSession(sessionID, senderID); ok && senderRef.Kind != "main" {
				return "Only the main agent can broadcast. Message the one agent that can answer via `agent://<id>`.", CoordinationDetails{Op: "send", From: senderID, To: to}, true
			}
		}
	}

	r.mu.Lock()
	r.nextMsgID++
	msgID := "irc-" + itoa(r.nextMsgID)
	r.mu.Unlock()

	var targets []*AgentRef
	var targetNames []string
	if isBroadcast {
		targets = r.ListVisibleInSession(sessionID, senderID)
		for _, t := range targets {
			targetNames = append(targetNames, t.ID)
		}
	} else {
		// Resolve "parent" alias and case-insensitive match.
		resolvedName := to
		if strings.EqualFold(to, "parent") {
			if senderRef, ok := r.GetInSession(sessionID, senderID); ok && senderRef.ParentID != "" {
				resolvedName = senderRef.ParentID
			} else {
				resolvedName = MainAgentID
			}
		}
		var ref *AgentRef
		var ok bool
		if strings.EqualFold(resolvedName, MainAgentID) {
			ref, ok = r.LookupMain(sessionID)
		} else {
			ref, ok = r.LookupInSession(sessionID, resolvedName)
		}
		if !ok {
			return fmt.Sprintf("Failed: %s is not running.", to), CoordinationDetails{
				Op:       "send",
				From:     senderID,
				To:       to,
				Receipts: []IrcDeliveryReceipt{{To: to, Outcome: "failed", Error: "not found"}},
			}, true
		}
		targets = []*AgentRef{ref}
		targetNames = []string{ref.ID}
		to = ref.ID
	}

	if isBroadcast && len(targets) == 0 {
		return "No live peers to broadcast to.", CoordinationDetails{
			Op:       "send",
			From:     senderID,
			To:       "all",
			Receipts: []IrcDeliveryReceipt{},
		}, false
	}

	receipts := make([]IrcDeliveryReceipt, 0, len(targets))
	delivered := 0
	for i, ref := range targets {
		msg := IRCMessage{
			ID:        msgID,
			From:      senderID,
			To:        targetNames[i],
			Body:      body,
			ReplyTo:   replyTo,
			Timestamp: time.Now().UnixMilli(),
			Broadcast: isBroadcast,
			Progress:  progress,
			WakeRelay: wakeRelay,
		}
		receipt := deliverToRef(ref, msg)
		receipts = append(receipts, receipt)
		if receipt.Outcome != "failed" {
			delivered++
		}
	}

	details := CoordinationDetails{
		Op:       "send",
		From:     senderID,
		To:       to,
		Receipts: receipts,
	}

	if isBroadcast {
		var b strings.Builder
		fmt.Fprintf(&b, "Broadcast delivered to %d of %d peer(s).", delivered, len(targets))
		for _, rec := range receipts {
			if rec.Outcome == "failed" {
				errTxt := rec.Error
				if errTxt == "" {
					errTxt = "not running"
				}
				fmt.Fprintf(&b, "\n- %s: failed — %s", rec.To, errTxt)
			} else {
				fmt.Fprintf(&b, "\n- %s: %s", rec.To, rec.Outcome)
			}
		}
		return b.String(), details, delivered == 0 && len(targets) > 0
	}

	rec := receipts[0]
	if rec.Outcome == "failed" {
		msg := fmt.Sprintf("Failed: %s could not receive the message.", to)
		if rec.Error != "" {
			msg = fmt.Sprintf("Failed: %s is not running. %s", to, rec.Error)
		}
		return msg, details, true
	}
	if rec.Outcome == "revived" {
		return fmt.Sprintf("Queued for %s (was parked; revived).", to), details, false
	}
	return fmt.Sprintf("Delivered to %s.", to), details, false
}

// RegisterMain installs or updates the Main AgentRef for a specific top-level
// sessionID, keyed by (sessionID, MainAgentID) so concurrent top-level sessions
// each deliver `agent://Main` to their own sdk.Context without overwriting each other.
// When Main is already registered for sessionID, updating its delivery callback
// preserves its original CreatedAt, LastActive (unless explicitly provided), and
// insertion slot — matching omp, where Main is registered once at session creation
// (`sdk.ts:4794`) rather than re-stamped on every `task` tool call.
func (r *Registry) RegisterMain(sessionID string, ref *AgentRef) {
	if ref == nil {
		return
	}
	now := time.Now()
	explicitCreated := !ref.CreatedAt.IsZero()
	explicitLastActive := !ref.LastActive.IsZero()
	ref.ID = MainAgentID
	ref.Kind = "main"
	ref.SessionID = sessionID
	if ref.Status == "" {
		ref.Status = "running"
	}
	key := refKey(sessionID, MainAgentID)
	r.mu.Lock()
	if prev, ok := r.refs[key]; ok && prev != nil {
		prev.mu.Lock()
		prevCreated := prev.CreatedAt
		prevLastActive := prev.LastActive
		prevSeq := prev.insertSeq
		prev.mu.Unlock()
		if !explicitCreated && !prevCreated.IsZero() {
			ref.CreatedAt = prevCreated
		}
		if !explicitLastActive && !prevLastActive.IsZero() {
			ref.LastActive = prevLastActive
		}
		if prevSeq != 0 {
			ref.insertSeq = prevSeq
		}
	}
	if ref.CreatedAt.IsZero() {
		ref.CreatedAt = now
	}
	if ref.LastActive.IsZero() {
		ref.LastActive = now
	}
	if ref.insertSeq == 0 {
		r.nextInsert++
		ref.insertSeq = r.nextInsert
	}
	r.refs[key] = ref
	r.mu.Unlock()
	r.notify()
}

// LookupMain finds the Main AgentRef for sessionID (falling back to any registered
// Main ref when sessionID is empty).
func (r *Registry) LookupMain(sessionID string) (*AgentRef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sessionID != "" {
		if ref, ok := r.refs[refKey(sessionID, MainAgentID)]; ok {
			return ref, true
		}
	}
	if ref, ok := r.refs[MainAgentID]; ok {
		return ref, true
	}
	if sessionID == "" {
		var match *AgentRef
		for _, ref := range r.refs {
			if ref.Kind == "main" {
				if match != nil {
					return nil, false
				}
				match = ref
			}
		}
		return match, match != nil
	}
	return nil, false
}

// LookupInSession resolves id within sessionID (exact match, then case-insensitive
// match). When sessionID is non-empty, only refs belonging to sessionID (or unscoped
// refs with empty SessionID) are considered; Main is also resolved within sessionID.
// When sessionID is empty, all refs are searched and ambiguous matches return false.
func (r *Registry) LookupInSession(sessionID, id string) (*AgentRef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sessionID != "" {
		if ref, ok := r.refs[refKey(sessionID, id)]; ok {
			return ref, true
		}
		if ref, ok := r.refs[id]; ok && ref.SessionID == "" {
			return ref, true
		}
	} else if ref, ok := r.refs[id]; ok {
		return ref, true
	}
	// Exact-ID scan (needed when sessionID == "" and refs are stored under session keys).
	if sessionID == "" {
		var exact *AgentRef
		for _, ref := range r.refs {
			if ref.ID == id {
				if exact != nil {
					return nil, false
				}
				exact = ref
			}
		}
		if exact != nil {
			return exact, true
		}
	}
	lower := strings.ToLower(id)
	var match *AgentRef
	for _, ref := range r.refs {
		if sessionID != "" && ref.SessionID != "" && ref.SessionID != sessionID {
			continue
		}
		if strings.ToLower(ref.ID) == lower {
			if match != nil {
				return nil, false
			}
			match = ref
		}
	}
	return match, match != nil
}

// ListVisibleInSession returns live (running/idle) peers visible to senderID within sessionID.
// Advisor refs are excluded matching `AgentRegistry.listVisibleTo` (`agent-registry.ts:457-461`).
func (r *Registry) ListVisibleInSession(sessionID, senderID string) []*AgentRef {
	var out []*AgentRef
	for _, ref := range r.ListInSession(sessionID) {
		if ref.ID == senderID || ref.Kind == "advisor" {
			continue
		}
		ref.mu.Lock()
		status := ref.Status
		ref.mu.Unlock()
		if status == "running" || status == "idle" {
			out = append(out, ref)
		}
	}
	return out
}

// StopIdleTimer cancels any armed idle-parking timer on ref.
func (ref *AgentRef) StopIdleTimer() {
	if ref == nil {
		return
	}
	ref.mu.Lock()
	if ref.idleTimer != nil {
		ref.idleTimer.Stop()
		ref.idleTimer = nil
	}
	ref.mu.Unlock()
}

// ArmIdleParkTimer schedules parking (session.Dispose + lifecycle.End + status="parked")
// after ttlMs if ref remains idle. Zero/negative ttlMs disables parking.
// upstream: packages/coding-agent/src/task/executor.ts:4789-4791 (task.agentIdleTtlMs)
func (r *Registry) ArmIdleParkTimer(ref *AgentRef, ttlMs int) {
	if ref == nil || ttlMs <= 0 {
		return
	}
	ref.mu.Lock()
	if ref.idleTimer != nil {
		ref.idleTimer.Stop()
	}
	ref.idleTimer = time.AfterFunc(time.Duration(ttlMs)*time.Millisecond, func() {
		r.ParkIdleRef(ref)
	})
	ref.mu.Unlock()
}

// ParkIdleRef transitions an idle live child ref to parked, disposing its child
// session and calling lifecycle.End(childSessionID) while retaining its JSONL
// transcript for history://.
func (r *Registry) ParkIdleRef(ref *AgentRef) {
	if ref == nil {
		return
	}
	ref.mu.Lock()
	if ref.Status != "idle" || ref.Session == nil {
		ref.idleTimer = nil
		ref.mu.Unlock()
		return
	}
	sess := ref.Session
	childSID := ref.ChildSessionID
	if childSID == "" {
		childSID = sess.SessionID
	}
	ref.Session = nil
	ref.OnWake = nil
	ref.idleTimer = nil
	ref.Status = "parked"
	ref.LastActive = time.Now()
	ref.mu.Unlock()

	sess.Dispose()
	if childSID != "" {
		lifecycle.End(childSID)
	}
	if r != nil {
		r.notify()
	}
}

func deliverToRef(ref *AgentRef, msg IRCMessage) IrcDeliveryReceipt {
	ref.mu.Lock()
	status := ref.Status
	kind := ref.Kind
	parentID := ref.ParentID
	sess := ref.Session
	onDeliver := ref.OnDeliver
	onWake := ref.OnWake
	if status == "aborted" && sess == nil && onWake == nil && onDeliver == nil {
		ref.mu.Unlock()
		return IrcDeliveryReceipt{To: ref.ID, Outcome: "failed", Error: "agent stopped"}
	}
	// Host-owned Main session: deliver through OnDeliver (never park in PendingIRC).
	if kind == "main" {
		ref.LastActive = time.Now()
		ref.mu.Unlock()
		if onDeliver == nil {
			return IrcDeliveryReceipt{To: ref.ID, Outcome: "failed", Error: "no live parent session context"}
		}
		return onDeliver(msg)
	}
	if status == "running" && sess == nil {
		// Child spawn queued behind semaphore or opening its session: park until child attaches.
		ref.PendingIRC = append(ref.PendingIRC, msg)
		ref.LastActive = time.Now()
		ref.mu.Unlock()
		return IrcDeliveryReceipt{To: ref.ID, Outcome: "injected"}
	}
	// Atomically stop the idle timer AND claim the idle -> running transition
	// under ref.mu when waking an idle live child so a concurrent ParkIdleRef
	// cannot slip into the gap and dispose the session before onWake runs.
	var claimedWake bool
	if status == "idle" && sess != nil {
		if ref.idleTimer != nil {
			ref.idleTimer.Stop()
			ref.idleTimer = nil
		}
		ref.Status = "running"
		claimedWake = true
	}
	ref.LastActive = time.Now()
	ref.mu.Unlock()

	// Upstream (`session/irc-bridge.ts:217-223`): relayOnStop is true ONLY when
	// waking an idle subagent for a non-broadcast, non-progress, non-relay message.
	// While the child is streaming, relayOnStop is false (and a steer from its
	// parent uses prompts/steering/parent-irc.md).
	if status == "running" && sess != nil {
		fromParent := parentID != "" && parentID == msg.From && !msg.Broadcast
		var formatted string
		if fromParent {
			formatted = FormatParentIRCSteer(msg.From, msg.Body)
		} else {
			quiet := msg.Broadcast || msg.Progress
			formatted = FormatIncomingIRCWithOptions(msg, false, !quiet)
		}
		if err := sess.Steer(formatted); err != nil {
			// If the turn just transitioned to idle, wake it instead.
			if onWake != nil && onWake(msg) {
				return IrcDeliveryReceipt{To: ref.ID, Outcome: "woken"}
			}
			return IrcDeliveryReceipt{To: ref.ID, Outcome: "failed", Error: err.Error()}
		}
		return IrcDeliveryReceipt{To: ref.ID, Outcome: "injected"}
	}

	if claimedWake {
		if onWake != nil {
			if !onWake(msg) {
				return IrcDeliveryReceipt{To: ref.ID, Outcome: "failed", Error: "agent parked or disposed"}
			}
			return IrcDeliveryReceipt{To: ref.ID, Outcome: "woken"}
		}
		relayOnStop := kind == "sub" && msg.To != MainAgentID && !msg.WakeRelay && !msg.Broadcast && !msg.Progress
		formatted := FormatIncomingIRCWithOptions(msg, relayOnStop, false)
		go func() {
			_ = sess.Prompt(context.Background(), formatted, sdk.PromptOptions{Source: "extension"})
		}()
		return IrcDeliveryReceipt{To: ref.ID, Outcome: "woken"}
	}

	if onWake != nil {
		if onWake(msg) {
			return IrcDeliveryReceipt{To: ref.ID, Outcome: "revived"}
		}
		return IrcDeliveryReceipt{To: ref.ID, Outcome: "failed", Error: "agent parked or disposed"}
	}
	return IrcDeliveryReceipt{To: ref.ID, Outcome: "failed", Error: "no live session"}
}

// CancelAllRunning aborts and disposes all child sessions belonging to sessionID
// (or all sessions if sessionID == ""), calling lifecycle.End for each disposed
// child session ID so every area releases that child's per-session state, and
// drops ended-session refs (and Main ref) so long-lived processes don't leak.
func (r *Registry) CancelAllRunning(sessionID string) {
	var toRemove []string
	r.mu.RLock()
	type entry struct {
		key string
		ref *AgentRef
	}
	entries := make([]entry, 0, len(r.refs))
	for k, ref := range r.refs {
		entries = append(entries, entry{key: k, ref: ref})
	}
	r.mu.RUnlock()

	for _, e := range entries {
		ref := e.ref
		if sessionID != "" && ref.SessionID != sessionID {
			continue
		}
		if ref.Kind == "main" {
			if sessionID != "" {
				toRemove = append(toRemove, e.key)
			}
			continue
		}
		ref.mu.Lock()
		if ref.idleTimer != nil {
			ref.idleTimer.Stop()
			ref.idleTimer = nil
		}
		sess := ref.Session
		childSID := ref.ChildSessionID
		if childSID == "" && sess != nil {
			childSID = sess.SessionID
		}
		ref.Session = nil
		ref.OnWake = nil
		if ref.Status == "running" || ref.Status == "idle" {
			ref.Status = "aborted"
		}
		ref.mu.Unlock()
		if sess != nil {
			_ = sess.Abort()
			sess.Dispose()
			if childSID != "" {
				lifecycle.End(childSID)
			}
		}
		if sessionID != "" {
			toRemove = append(toRemove, e.key)
		}
	}
	if len(toRemove) > 0 {
		r.mu.Lock()
		for _, k := range toRemove {
			delete(r.refs, k)
		}
		r.mu.Unlock()
		r.notify()
	}
}

// KillAgent kills a subagent registration in sessionID (aborting any active
// turn, disposing its child session, calling lifecycle.End on the child session
// ID, and leaving a terminal "aborted" tombstone so its transcript remains at
// history://<id> while no queued message wakes it). Returns (status, message)
// matching omp's cancelAgentRegistration (async/job-control.ts:641-681):
// status is "cancelled", "already_completed", or "not_found".
// Also cancels any still-running jobs.KindAgent job for id and marks it
// consumed only by the caller (proc://<id>/kill).
// upstream: packages/coding-agent/src/async/job-control.ts:641-681
func (r *Registry) KillAgent(sessionID, id string) (status string, message string) {
	if id == "" {
		return "not_found", "Background job not found: "
	}
	ref, ok := r.GetInSession(sessionID, id)
	if !ok || ref == nil || ref.Kind != "sub" {
		return "not_found", fmt.Sprintf("Background job not found: %s", id)
	}
	ref.mu.Lock()
	if ref.idleTimer != nil {
		ref.idleTimer.Stop()
		ref.idleTimer = nil
	}
	sess := ref.Session
	childSID := ref.ChildSessionID
	if childSID == "" && sess != nil {
		childSID = sess.SessionID
	}
	prevStatus := ref.Status
	ref.Session = nil
	ref.OnWake = nil
	ref.Status = "aborted"
	ref.LastActive = time.Now()
	ref.mu.Unlock()

	if sess != nil {
		if prevStatus == "running" {
			_ = sess.Abort()
		}
		sess.Dispose()
		if childSID != "" {
			lifecycle.End(childSID)
		}
	}
	r.notify()
	return "cancelled", fmt.Sprintf("Killed agent %s; its transcript stays at history://%s.", id, id)
}

// KillAgent kills a subagent registration in DefaultRuntime's Registry for
// sessionID, matching omp's cancelAgentRegistration (async/job-control.ts:641-681).
// Suitable for wiring into shell.KillAgentHook so `proc://<id>/kill` kills the
// agent registration before cancelling its task job.
func KillAgent(sessionID, id string) (status string, message string) {
	return defaultRuntime.Registry.KillAgent(sessionID, id)
}

// LoadSessionMessagesReadOnly parses Pi/PiG/omp JSONL session files into a
// slice of message maps without opening any writer or lock.
// upstream: packages/coding-agent/src/session/session-loader.ts
func LoadSessionMessagesReadOnly(filePath string) ([]map[string]any, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var msgs []map[string]any
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(trimmed), &entry); err != nil {
			continue
		}
		entryType, _ := entry["type"].(string)
		switch entryType {
		case "message":
			if m, ok := entry["message"].(map[string]any); ok {
				msgs = append(msgs, m)
			}
		case "custom_message":
			msgs = append(msgs, map[string]any{
				"role":       "custom",
				"customType": entry["customType"],
				"content":    entry["content"],
				"display":    entry["display"],
				"details":    entry["details"],
			})
		case "compaction":
			msgs = append(msgs, map[string]any{
				"role":         "compactionSummary",
				"summary":      entry["summary"],
				"tokensBefore": entry["tokensBefore"],
			})
		case "branch_summary":
			msgs = append(msgs, map[string]any{
				"role":    "branchSummary",
				"fromId":  entry["fromId"],
				"summary": entry["summary"],
			})
		}
	}
	return msgs, nil
}

// CollectPeerRoster formats live peers for a child agent's system prompt.
// upstream: packages/coding-agent/src/task/executor.ts (collectIrcPeerRoster)
type PeerRoster struct {
	Peers        []PeerRosterEntry
	OmittedCount int
	ParkedCount  int
}

type PeerRosterEntry struct {
	ID          string
	DisplayName string
	Kind        string
	Status      string
	Activity    string
}

const defaultPeerRosterLimit = 32

// CollectPeerRoster formats live peers for a child agent's system prompt,
// scoped to sessionID when non-empty so concurrent sessions never see each other's peers.
func (r *Registry) CollectPeerRoster(selfID string) PeerRoster {
	return r.CollectPeerRosterForSession("", selfID)
}

// CollectPeerRosterForSession formats live peers in sessionID (excluding selfID).
//
// Ordering matches omp's `collectIrcPeerRoster` (`packages/coding-agent/src/task/executor.ts:415-450`):
//  1. Status bucket: `running` (0) before `idle` (1) (`LIST_STATUS_ORDER`, `packages/tui/src/tools/irc.ts:45`).
//  2. Recency descending: newest `LastActive` first (`b.lastActivity - a.lastActivity`),
//     so newly spawned siblings and recently active peers sort ahead of older ancestors
//     (such as `Main`) and the 32-peer cap keeps the newest relevant peers.
//  3. Ties preserve registry insertion order (matching JS `Map.prototype.values()` +
//     ES2019 stable `Array.prototype.sort`).
//
// Advisor refs (`Kind == "advisor"`) are excluded from both live peers and parkedCount.
func (r *Registry) CollectPeerRosterForSession(sessionID, selfID string) PeerRoster {
	type rosterCandidate struct {
		entry      PeerRosterEntry
		lastActive time.Time
		insertSeq  uint64
	}
	r.mu.RLock()
	refs := make([]*AgentRef, 0, len(r.refs))
	for _, ref := range r.refs {
		if sessionID != "" && ref.SessionID != "" && ref.SessionID != sessionID {
			continue
		}
		refs = append(refs, ref)
	}
	r.mu.RUnlock()

	var live []rosterCandidate
	parked := 0
	for _, ref := range refs {
		if ref.ID == selfID || ref.Kind == "advisor" {
			continue
		}
		ref.mu.Lock()
		st := ref.Status
		la := ref.LastActive
		seq := ref.insertSeq
		entry := PeerRosterEntry{
			ID:          ref.ID,
			DisplayName: ref.DisplayName,
			Kind:        ref.Kind,
			Status:      st,
			Activity:    ref.Activity,
		}
		ref.mu.Unlock()
		switch st {
		case "running", "idle":
			live = append(live, rosterCandidate{
				entry:      entry,
				lastActive: la,
				insertSeq:  seq,
			})
		case "parked":
			parked++
		}
	}
	// Establish JS Map insertion order before stable sort (matching `registry.listVisibleTo(selfId)`).
	sort.Slice(live, func(i, j int) bool {
		if live[i].insertSeq != live[j].insertSeq {
			return live[i].insertSeq < live[j].insertSeq
		}
		return live[i].entry.ID < live[j].entry.ID
	})
	statusRank := func(s string) int {
		switch s {
		case "running":
			return 0
		case "idle":
			return 1
		case "parked":
			return 2
		default:
			return 9
		}
	}
	sort.SliceStable(live, func(i, j int) bool {
		ri, rj := statusRank(live[i].entry.Status), statusRank(live[j].entry.Status)
		if ri != rj {
			return ri < rj
		}
		// Newest LastActive first (`b.lastActivity - a.lastActivity`);
		// equal timestamps preserve insertion order via SliceStable.
		if !live[i].lastActive.Equal(live[j].lastActive) {
			return live[i].lastActive.After(live[j].lastActive)
		}
		return false
	})
	omitted := 0
	if len(live) > defaultPeerRosterLimit {
		omitted = len(live) - defaultPeerRosterLimit
		live = live[:defaultPeerRosterLimit]
	}
	peers := make([]PeerRosterEntry, len(live))
	for i, c := range live {
		peers[i] = c.entry
	}
	return PeerRoster{
		Peers:        peers,
		OmittedCount: omitted,
		ParkedCount:  parked,
	}
}
