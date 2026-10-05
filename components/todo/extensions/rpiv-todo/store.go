package rpiv_todo

import "sync"

// store is the per-session live state: a map partitioned by session id, so a detached or child session
// can never read or clobber another session's tasks, plus the ctx-less render pointer (which slot the
// overlay and renderCall show). Only commitState, replaceState and evictSession write the map; the
// reducer stays pure. Handlers run concurrently, so every access holds the lock. upstream: state/store.ts.
type store struct {
	mu                  sync.Mutex
	sessions            map[string]taskState
	activeRenderSession string
}

func newStore() *store { return &store{sessions: map[string]taskState{}} }

// freshState is an empty state that aliases nothing.
func freshState() taskState { return taskState{Tasks: []task{}, NextID: 1} }

// slotFor is the committed slot, or a fresh empty state (not stored) when the slot is absent.
func (s *store) slotFor(sessionID string) taskState {
	if st, ok := s.sessions[sessionID]; ok {
		return st
	}
	return freshState()
}

// getTodos is the live tasks of a session; callers must not modify them.
func (s *store) getTodos(sessionID string) []task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slotFor(sessionID).Tasks
}

func (s *store) getNextID(sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slotFor(sessionID).NextID
}

// getState is the snapshot reducer callers pass in.
func (s *store) getState(sessionID string) taskState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slotFor(sessionID)
}

// replaceState is the replay seam: lifecycle handlers publish a rebuilt slot.
func (s *store) replaceState(sessionID string, next taskState) {
	s.mu.Lock()
	s.sessions[sessionID] = next
	s.mu.Unlock()
}

// commitState is the post-reducer seam: the tool publishes the new state, keyed to the calling session.
func (s *store) commitState(sessionID string, next taskState) {
	s.mu.Lock()
	s.sessions[sessionID] = next
	s.mu.Unlock()
}

// evictSession drops a session's slot on shutdown; an absent slot is a no-op.
func (s *store) evictSession(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

// getRenderState is what the ctx-less readers show: the foreground slot, or a fresh empty state.
func (s *store) getRenderState() taskState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slotFor(s.activeRenderSession)
}

func (s *store) setActiveRenderSession(sessionID string) {
	s.mu.Lock()
	s.activeRenderSession = sessionID
	s.mu.Unlock()
}

func (s *store) getActiveRenderSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeRenderSession
}

// clearActiveRenderSession is the foreground teardown: the next UI session reclaims the foreground.
func (s *store) clearActiveRenderSession() {
	s.mu.Lock()
	s.activeRenderSession = ""
	s.mu.Unlock()
}

// reset clears the map and the render pointer (the original's test-setup reset).
func (s *store) reset() {
	s.mu.Lock()
	s.sessions = map[string]taskState{}
	s.activeRenderSession = ""
	s.mu.Unlock()
}

// sessionIDSource is the part of the host's session manager sid() reads (sdk.SessionManager satisfies it).
type sessionIDSource interface{ GetSessionID() (string, error) }

// sid is the session-id extractor: an unknown session reads as "". A host failure is returned, not
// turned into an empty id, so the lifecycle handlers can tell a stale context from a real fault.
// upstream: state/store.ts:23-25.
func sid(src sessionIDSource) (string, error) { return src.GetSessionID() }
