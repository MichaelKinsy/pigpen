package pi_permission_system

import (
	"sort"
	"sync"
)

// Port of src/authority/serving-registry.ts: which sessions are draining a forwarded-permission inbox.
//
// PROCESS MODEL. The original keeps the single registry on globalThis so a parent and its in-process child, loaded by separate jiti
// instances, see one set. Here the registry is package-level state of one Go process: it is shared by everything inside this
// extension's process and by nothing else. A child in another process cannot read it; the filesystem heartbeat
// (forwarding_liveness.go) is the only channel that crosses processes.

// ServingAnnouncer is the announce-side seam: the polling session marks and clears itself. MarkServing is idempotent and called on
// every poll tick.
type ServingAnnouncer interface {
	MarkServing(sessionID string)
	ClearServing(sessionID string)
}

// ComposeServingAnnouncers fans an announcement out to every channel a serving session publishes on.
func ComposeServingAnnouncers(announcers ...ServingAnnouncer) ServingAnnouncer {
	return composedAnnouncer(announcers)
}

type composedAnnouncer []ServingAnnouncer

func (c composedAnnouncer) MarkServing(sessionID string) {
	for _, a := range c {
		a.MarkServing(sessionID)
	}
}

func (c composedAnnouncer) ClearServing(sessionID string) {
	for _, a := range c {
		a.ClearServing(sessionID)
	}
}

// ServingLookup is the query-side seam: a forwarding child asks whether its target is draining. ServingIDs exists for the
// diagnostic review entry a child writes when it abandons an unserved request.
type ServingLookup interface {
	IsServing(sessionID string) bool
	ServingIDs() []string
}

// ServingSessionRegistry is the registry of sessions currently draining a forwarded-permission inbox. Obtain the process-global one
// with GetServingSessionRegistry; it is safe for concurrent use. ServingIDs lists ids in insertion order, as a JavaScript Set
// iterates.
type ServingSessionRegistry struct {
	mu      sync.Mutex
	serving map[string]int
	seq     int
}

// NewServingSessionRegistry is `new ServingSessionRegistry()`.
func NewServingSessionRegistry() *ServingSessionRegistry {
	return &ServingSessionRegistry{serving: map[string]int{}}
}

// MarkServing records that sessionID is polling its inbox. Idempotent (a re-mark keeps its place, as Set.add does).
func (r *ServingSessionRegistry) MarkServing(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serving[sessionID]; !ok {
		r.seq++
		r.serving[sessionID] = r.seq
	}
}

// ClearServing records that sessionID has stopped polling; a no-op if unmarked.
func (r *ServingSessionRegistry) ClearServing(sessionID string) {
	r.mu.Lock()
	delete(r.serving, sessionID)
	r.mu.Unlock()
}

// IsServing reports whether sessionID is currently polling its inbox.
func (r *ServingSessionRegistry) IsServing(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.serving[sessionID]
	return ok
}

// ServingIDs lists every currently-serving session id, for diagnostics.
func (r *ServingSessionRegistry) ServingIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.serving))
	for id := range r.serving {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return r.serving[ids[i]] < r.serving[ids[j]] })
	return ids
}

var (
	servingRegistryMu   sync.Mutex
	servingRegistrySlot *ServingSessionRegistry
)

// GetServingSessionRegistry is getServingSessionRegistry: the process-global registry, created on first call. It has no teardown
// hook, so a child's session_shutdown cannot wipe the parent's mark.
func GetServingSessionRegistry() *ServingSessionRegistry {
	servingRegistryMu.Lock()
	defer servingRegistryMu.Unlock()
	if servingRegistrySlot == nil {
		servingRegistrySlot = NewServingSessionRegistry()
	}
	return servingRegistrySlot
}
