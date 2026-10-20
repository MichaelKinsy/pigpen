package pi_permission_system

import "sync"

// Port of src/authority/subagent-registry.ts: the in-process registry of subagent child sessions.
//
// PROCESS MODEL. The original stores one registry on globalThis (Symbol.for) so the parent's permission-system instance, which
// registers children on the parent's event bus, and each child's separate jiti instance, which reads it to detect itself, share one
// store inside ONE Node process. PiG runs every extension in its own process, so the package-level registry below is shared only by
// the code of this extension's own process: a child session whose permission extension runs in another process reads its own,
// empty, registry. See PORT.md ("Forwarding: what the process model cannot reproduce").

// SubagentSessionInfo is the signal stored per registered in-process subagent session. A nil ParentSessionID is undefined.
type SubagentSessionInfo struct {
	// ParentSessionID is the parent session id for permission forwarding.
	ParentSessionID *string
}

// ParentChainRegistry is the read shape the parent-chain walk and target resolution need from the subagent registry (ISP): which
// node spawned the one named by sessionID.
type ParentChainRegistry interface {
	Get(sessionID string) (SubagentSessionInfo, bool)
}

// SubagentSessionRegistry is the registry of active in-process subagent sessions, keyed by child session id so concurrent siblings
// of one parent occupy distinct keys. Safe for concurrent use: the lifecycle subscription writes while gates read.
type SubagentSessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]SubagentSessionInfo
}

// NewSubagentSessionRegistry is `new SubagentSessionRegistry()`.
func NewSubagentSessionRegistry() *SubagentSessionRegistry {
	return &SubagentSessionRegistry{sessions: map[string]SubagentSessionInfo{}}
}

// Register registers an in-process subagent session, overwriting a previous entry for the same id (last write wins).
func (r *SubagentSessionRegistry) Register(sessionID string, info SubagentSessionInfo) {
	r.mu.Lock()
	r.sessions[sessionID] = info
	r.mu.Unlock()
}

// Unregister removes a registered session; a no-op if the key is absent.
func (r *SubagentSessionRegistry) Unregister(sessionID string) {
	r.mu.Lock()
	delete(r.sessions, sessionID)
	r.mu.Unlock()
}

// Get returns the registered info for sessionID.
func (r *SubagentSessionRegistry) Get(sessionID string) (SubagentSessionInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.sessions[sessionID]
	return info, ok
}

// Has reports whether sessionID has a registered entry.
func (r *SubagentSessionRegistry) Has(sessionID string) bool {
	_, ok := r.Get(sessionID)
	return ok
}

var (
	subagentRegistryMu   sync.Mutex
	subagentRegistrySlot *SubagentSessionRegistry
)

// GetSubagentSessionRegistry is getSubagentSessionRegistry: the process-global registry, created on first call. It has no
// shutdown hook: a child's session_shutdown must not wipe the parent's registrations.
func GetSubagentSessionRegistry() *SubagentSessionRegistry {
	subagentRegistryMu.Lock()
	defer subagentRegistryMu.Unlock()
	if subagentRegistrySlot == nil {
		subagentRegistrySlot = NewSubagentSessionRegistry()
	}
	return subagentRegistrySlot
}
