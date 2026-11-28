package urls

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// sessionState holds all per-session state owned by internal/urls in one table
// keyed by session ID, released together by lifecycle.OnEnd("urls", ...).
type sessionState struct {
	skills      []SkillEntry
	hasSkills   bool
	attachments []Attachment
	conflicts   *ConflictHistory
}

var (
	sessionStateMu sync.RWMutex
	sessionStates  = map[string]*sessionState{}
)

// resolveSessionKey returns the session map key for c.
// For a live (non-zero) sdk.Context whose session ID cannot be resolved, ok is
// false so callers never collide on a shared default bucket. A zero sdk.Context
// (standalone/unit-test calls without a host) maps to ("", true).
func resolveSessionKey(c Context) (string, bool) {
	if c.SDK != (sdk.Context{}) {
		id, err := c.SDK.GetSessionID()
		if err != nil || id == "" {
			return "", false
		}
		return id, true
	}
	return "", true
}

func getOrCreateSessionStateLocked(sessionID string) *sessionState {
	st, ok := sessionStates[sessionID]
	if !ok {
		st = &sessionState{}
		sessionStates[sessionID] = st
	}
	return st
}

func clearSessionState(sessionID string) {
	if sessionID == "" {
		return
	}
	sessionStateMu.Lock()
	delete(sessionStates, sessionID)
	sessionStateMu.Unlock()
}

func setSessionSkills(sessionID string, skills []SkillEntry) {
	if sessionID == "" {
		return
	}
	sessionStateMu.Lock()
	st := getOrCreateSessionStateLocked(sessionID)
	st.skills = append([]SkillEntry(nil), skills...)
	st.hasSkills = true
	sessionStateMu.Unlock()
}

func getSessionSkills(sessionID string) ([]SkillEntry, bool) {
	if sessionID == "" {
		return nil, false
	}
	sessionStateMu.RLock()
	st, ok := sessionStates[sessionID]
	if !ok || !st.hasSkills {
		sessionStateMu.RUnlock()
		return nil, false
	}
	cp := append([]SkillEntry(nil), st.skills...)
	sessionStateMu.RUnlock()
	return cp, true
}
