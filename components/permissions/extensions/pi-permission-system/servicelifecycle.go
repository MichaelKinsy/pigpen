package pi_permission_system

import "sync"

// Port of src/service/service-lifecycle.ts: the process-global service publication lifecycle of one node (ADR 0012).

// ServiceLifecycle is the session-scoped service lifecycle the lifecycle handler drives.
type ServiceLifecycle interface {
	Activate(ctx SessionContext)
	Teardown()
}

// ReadyAnnouncer announces this node's ready facts at most once per session, whatever the caller does (ADR 0012 decision 3, the ready
// latch). Kept apart from ServiceLifecycle because the two roles have different callers: the session lifecycle starts and tears the
// node down, while the latch fires from the turn about to start.
type ReadyAnnouncer interface {
	AnnounceReady(ctx SessionContext)
}

// PermissionServiceLifecycle owns the service publication lifecycle for one extension instance, that is for one node.
//
//   - Activate publishes the service under this node's own session id, so a sibling extension loaded into this node registers into the
//     registries this node's gates and chain read; then it announces both facts a consumer needs (the session id and the chain role) on
//     the ready channel and re-arms the latch, so the turn about to start announces once more.
//   - AnnounceReady is that second announcement: it fires at most once per activation cycle, so permissions:ready reaches a consumer
//     whose own session_start ran after this node's. The channel's contract is "at least once per session, and may repeat".
//   - Teardown runs the session-scoped subscription cleanups in order, then unpublishes this node's entry. The unpublish is
//     identity-scoped, so a superseded /reload generation cannot evict the fresh one.
type PermissionServiceLifecycle struct {
	service       PermissionsService
	role          AdjudicationRole
	events        PermissionEventBus
	subscriptions []func()

	mu sync.Mutex
	// publishedSessionID is the key this instance last published under; nil until it publishes.
	publishedSessionID *string
	// announced is whether the latch has already announced for the current session.
	announced bool
}

// NewPermissionServiceLifecycle is `new PermissionServiceLifecycle(service, role, events, subscriptions)`.
func NewPermissionServiceLifecycle(service PermissionsService, role AdjudicationRole, events PermissionEventBus, subscriptions []func()) *PermissionServiceLifecycle {
	return &PermissionServiceLifecycle{service: service, role: role, events: events, subscriptions: subscriptions}
}

// CurrentSessionID is this node's own session id, false before it has published. It makes the lifecycle the NodeIdentity the
// fact-shaping lookups read: it already reads the id from the context at Activate and holds it, so identity keeps one home.
func (l *PermissionServiceLifecycle) CurrentSessionID() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.publishedSessionID == nil {
		return "", false
	}
	return *l.publishedSessionID, true
}

// Activate publishes the service under the node's session id and announces ready.
func (l *PermissionServiceLifecycle) Activate(ctx SessionContext) {
	// Re-arm: a new session generation gets its own post-session_start announcement, so a consumer that loaded after this node still
	// hears one.
	l.mu.Lock()
	l.announced = false
	l.mu.Unlock()
	if sessionID, ok := ReadSessionID(ctx.SessionManager); ok {
		PublishPermissionsService(sessionID, l.service)
		l.mu.Lock()
		l.publishedSessionID = &sessionID
		l.mu.Unlock()
	}
	l.emitReady(ctx)
}

// AnnounceReady fires the ready announcement at most once per activation cycle.
func (l *PermissionServiceLifecycle) AnnounceReady(ctx SessionContext) {
	l.mu.Lock()
	if l.announced {
		l.mu.Unlock()
		return
	}
	l.announced = true
	l.mu.Unlock()
	l.emitReady(ctx)
}

// Teardown unsubscribes every session listener and unpublishes this node's entry.
func (l *PermissionServiceLifecycle) Teardown() {
	for _, unsubscribe := range l.subscriptions {
		unsubscribe()
	}
	l.mu.Lock()
	published := l.publishedSessionID
	l.publishedSessionID = nil
	l.mu.Unlock()
	if published != nil {
		UnpublishPermissionsService(*published, l.service)
	}
}

// emitReady is the one place a ready payload is built, so the session_start emission and the latch emission cannot drift in shape.
func (l *PermissionServiceLifecycle) emitReady(ctx SessionContext) {
	var id *string
	if sessionID, ok := ReadSessionID(ctx.SessionManager); ok {
		id = &sessionID
	}
	EmitReadyEvent(l.events, PermissionsReadyEvent{SessionID: id, AdjudicatesLocally: l.role.AdjudicatesLocally()})
}
