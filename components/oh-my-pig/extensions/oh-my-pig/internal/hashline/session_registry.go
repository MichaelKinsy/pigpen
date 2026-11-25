package hashline

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// Session-scoped EditStore registry keyed by PiG session ID.
// PiG creates a fresh extension instance per session while sharing package-level
// state across all sessions in the process. Registering a single package-level
// lifecycle cleanup ensures ending one session releases only that session's store
// and leaves all other sessions intact.
//
// Subagent isolation: in omp (packages/coding-agent/src/edit/store.ts and
// tools/tool-state.ts), each ToolSession owns its own ToolConversationState and
// therefore its own EditStore. A subagent session gets a distinct session ID and
// a fresh empty Store — it never inherits the parent session's snapshots, seen-line
// provenance, clipboard registers, or no-op counters. A child that supplies a tag
// copied from its parent gets omp's standard mismatch / content-hash rules (matching
// live hash applies short lines if no other path holds the tag, wide lines require
// a read; stale tags cannot recover because the child has no prior snapshot).
// upstream: packages/coding-agent/src/edit/store.ts:13, tools/tool-state.ts:34

var (
	sessionStoresMu sync.Mutex
	sessionStores   = map[string]*Store{}
)

// safeSessionID resolves ctx.GetSessionID without panicking on a zero sdk.Context
// (in PiG SDK 0.8.0, a zero sdk.Context has nil c.ext so hostConnection() panics).
func safeSessionID(ctx sdk.Context) (sid string, ok bool) {
	defer func() {
		if recover() != nil {
			sid = ""
			ok = false
		}
	}()
	id, err := ctx.GetSessionID()
	if err != nil || id == "" {
		return "", false
	}
	return id, true
}

// ForSession returns the one Store of the calling session, keyed by session ID.
// If ctx carries no resolvable session ID (zero context, err != nil, or empty),
// ForSession returns a fresh private Store (marked private) that is neither shared nor retained —
// never a shared default. Reads still work on a private store, while anchored edits refuse
// rather than silently disabling the seen-line guard.
// upstream: packages/coding-agent/src/edit/store.ts:13
func ForSession(ctx sdk.Context) *Store {
	sid, ok := safeSessionID(ctx)
	if !ok {
		s := NewStore()
		s.privateFallback = true
		return s
	}
	return StoreForSession(sid)
}

// HasSessionID reports whether ctx resolves to a non-empty session ID.
func HasSessionID(ctx sdk.Context) bool {
	_, ok := safeSessionID(ctx)
	return ok
}

// StoreForSession returns the Store for sessionID, creating one if absent.
// An empty sessionID returns a fresh private Store (not shared, not retained).
func StoreForSession(sessionID string) *Store {
	if sessionID == "" {
		s := NewStore()
		s.privateFallback = true
		return s
	}
	sessionStoresMu.Lock()
	defer sessionStoresMu.Unlock()
	if s, ok := sessionStores[sessionID]; ok {
		return s
	}
	s := NewStore()
	sessionStores[sessionID] = s
	return s
}

// ReleaseSession drops the Store associated with sessionID.
// Safe to call twice or with an unknown/empty sessionID; never touches other sessions.
func ReleaseSession(sessionID string) {
	if sessionID == "" {
		return
	}
	sessionStoresMu.Lock()
	delete(sessionStores, sessionID)
	sessionStoresMu.Unlock()
}

// RegisterLifecycle installs the package-level lifecycle cleanup for session-scoped stores.
// Calling it multiple times (e.g. from every Register) is safe and idempotent.
func RegisterLifecycle() {
	lifecycle.OnEnd("hashline", ReleaseSession)
}

func init() {
	RegisterLifecycle()
}
