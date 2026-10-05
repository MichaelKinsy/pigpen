package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StoredSession is one entry of the adapter's session map.
type StoredSession struct {
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	SessionFile string `json:"sessionFile"`
	UpdatedAt   string `json:"updatedAt"`
}

// Store maps ACP session ids to pi session files (SessionStore of the original).
type Store interface {
	Get(sessionID string) *StoredSession
	Upsert(entry StoredSession)
	Delete(sessionID string)
}

type sessionMapFile struct {
	Version  int                      `json:"version"`
	Sessions map[string]StoredSession `json:"sessions"`
}

// FileStore is a Store kept in one JSON file. Every operation reads the file again, so two
// adapter processes (two editor windows) see each other's entries.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a store at path ("" = the default location under PIG_HOME).
func NewFileStore(path string) *FileStore {
	if path == "" {
		path = SessionMapPath()
	}
	return &FileStore{path: path}
}

func (s *FileStore) load() sessionMapFile {
	empty := sessionMapFile{Version: 1, Sessions: map[string]StoredSession{}}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return empty
	}
	var f struct {
		Version  int                        `json:"version"`
		Sessions map[string]json.RawMessage `json:"sessions"`
	}
	if json.Unmarshal(raw, &f) != nil || f.Version != 1 || f.Sessions == nil {
		return empty
	}
	for id, r := range f.Sessions {
		var e StoredSession
		if json.Unmarshal(r, &e) == nil {
			empty.Sessions[id] = e
		}
	}
	return empty
}

func (s *FileStore) save(f sessionMapFile) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	// Write to a sibling and rename, so a second adapter never reads half a file.
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o600) == nil {
		if os.Rename(tmp, s.path) != nil {
			_ = os.WriteFile(s.path, append(b, '\n'), 0o600)
			_ = os.Remove(tmp)
		}
	}
}

// Get returns the entry for sessionID, or nil.
func (s *FileStore) Get(sessionID string) *StoredSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.load().Sessions[sessionID]; ok {
		return &e
	}
	return nil
}

// Upsert records or refreshes an entry.
func (s *FileStore) Upsert(entry StoredSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.load()
	f.Sessions[entry.SessionID] = StoredSession{
		SessionID: entry.SessionID, Cwd: entry.Cwd, SessionFile: entry.SessionFile,
		UpdatedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	s.save(f)
}

// Delete removes an entry.
func (s *FileStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.load()
	if _, ok := f.Sessions[sessionID]; !ok {
		return
	}
	delete(f.Sessions, sessionID)
	s.save(f)
}
