package pi

import (
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
)

// SessionStore is the slice of Pi's session manager the host uses. *pisession.Manager satisfies
// it; the extension adapter provides one over the live PiG session.
type SessionStore interface {
	// File is the session file, "" when the session is not persisted; it may not exist yet.
	File() string
	LeafEntry() pisession.Entry
	SessionName() string
	AppendSessionInfo(name string) (string, error)
	// Branch is the path from the root to the leaf.
	Branch(fromID string) []pisession.Entry
	// ContextEntries is the compaction-aware entry list along the current branch.
	ContextEntries() []pisession.Entry
}

// SessionManagerFactory creates the storage for a new session.
type SessionManagerFactory func(workingDirectory, sessionID string) (SessionStore, error)

// InMemoryStorage is a factory for sessions that never touch disk.
func InMemoryStorage(workingDirectory, sessionID string) (SessionStore, error) {
	return pisession.InMemory(workingDirectory, pisession.Options{ID: sessionID})
}

// PersistentStorage is a factory for sessions written under dir.
func PersistentStorage(dir string) SessionManagerFactory {
	return func(workingDirectory, sessionID string) (SessionStore, error) {
		return pisession.Create(workingDirectory, dir, pisession.Options{ID: sessionID})
	}
}

// HistorySource is what history serving needs from a live session.
type HistorySource interface {
	Store() SessionStore
	ID() string
	// Anchor is the recorded truncation anchor of a turn.
	Anchor(turnID string) (string, bool)
}

// TruncationAnchor is the session entry a truncation to turnID would move the agent to ("" turnID
// clears everything). ok is false when no entry matches: accepting an impossible truncation would
// shorten the client's view while Pi kept using context the user believes is gone.
func TruncationAnchor(source HistorySource, turnID *string) (string, bool) {
	key := ClearAllAnchor
	if turnID != nil {
		key = *turnID
	}
	if known, ok := source.Anchor(key); ok && known != "" {
		return known, true
	}
	// A turn this host never ran: recompute from the file, which is where a hydrated session's ids
	// came from in the first place.
	a, ok := RebuildHistoryFromSession(source.Store(), RebuildOptions{TurnIDPrefix: source.ID()}).Anchors[key]
	return a, ok && a != ""
}

// DefaultSessionDir is where Pi keeps the sessions of a working directory: one sub-directory of
// <agentDir>/sessions per cwd, named after the encoded path (Pi's getDefaultSessionDirPath).
func DefaultSessionDir(agentDir, cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	abs = strings.TrimLeft(abs, `/\`)
	safe := "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(abs) + "--"
	return filepath.Join(agentDir, "sessions", safe)
}

// DefaultStorage keeps new sessions where Pi itself would, under <agentDir>/sessions, and pairs the
// catalogue root with the same profile so the catalogue and the writer never disagree (port of
// createDefaultPiSessionStorage).
func DefaultStorage(agentDir string) (catalogueRoot string, create SessionManagerFactory) {
	return filepath.Join(agentDir, "sessions"), func(workingDirectory, sessionID string) (SessionStore, error) {
		return pisession.Create(workingDirectory, DefaultSessionDir(agentDir, workingDirectory), pisession.Options{ID: sessionID})
	}
}
