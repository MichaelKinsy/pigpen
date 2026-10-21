package tintinweb_subagents

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// The file-backed store of scheduled subagents. Session-scoped: each session owns its schedules at
// <cwd>/.pi/subagent-schedules/<sessionId>.json. Every mutation takes a PID-based exclusion lock, re-reads the latest
// state from disk, applies the change, writes it atomically (temp file and rename) and releases the lock.
// upstream: src/schedule-store.ts, src/types.ts ScheduledSubagent.

const (
	lockRetry      = 50 * time.Millisecond
	lockMaxRetries = 100
)

// scheduledSubagent is a stored job. The field order is the order the original's records have on disk (a field added
// by a later update goes after the ones before it: lastStatus, lastRun, nextRun). upstream: src/types.ts ScheduledSubagent.
type scheduledSubagent struct {
	ID          string `json:"id"`
	Name        string `json:"name"` // unique within the store; the Agent tool uses the description
	Description string `json:"description"`
	// Schedule is the raw input (a cron expression, an interval "5m") or, for a one-shot, its ISO timestamp.
	Schedule     string   `json:"schedule"`
	ScheduleType string   `json:"scheduleType"` // "cron", "once" or "interval"
	IntervalMs   *float64 `json:"intervalMs,omitempty"`

	// The Agent parameters the job spawns with (no inherit_context, no resume).
	SubagentType string  `json:"subagent_type"`
	Prompt       string  `json:"prompt"`
	Model        *string `json:"model,omitempty"`
	Thinking     *string `json:"thinking,omitempty"`
	MaxTurns     *int    `json:"max_turns,omitempty"`
	Isolated     *bool   `json:"isolated,omitempty"`
	Isolation    string  `json:"isolation,omitempty"`

	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"createdAt"`
	RunCount   int    `json:"runCount"`
	LastStatus string `json:"lastStatus,omitempty"` // "success", "error" or "running"
	LastRun    string `json:"lastRun,omitempty"`
	NextRun    string `json:"nextRun,omitempty"`
}

// scheduleStoreData is the file: a version and the jobs. upstream: src/types.ts ScheduleStoreData.
type scheduleStoreData struct {
	Version int                 `json:"version"`
	Jobs    []scheduledSubagent `json:"jobs"`
}

// resolveStorePath is the store of a session. upstream: schedule-store.ts resolveStorePath.
func resolveStorePath(cwd, sessionID string) string {
	return filepath.Join(cwd, ".pi", "subagent-schedules", sessionID+".json")
}

// acquireLock creates the lock file holding this process's id, taking over a lock whose holder is gone.
// upstream: schedule-store.ts acquireLock.
func acquireLock(lockPath string) error {
	for range lockMaxRetries {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err == nil {
			_, werr := f.WriteString(strconv.Itoa(os.Getpid()))
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			return werr
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if data, rerr := os.ReadFile(lockPath); rerr == nil {
			if pid, ok := jsParseInt(string(data)); ok && pid != 0 && !processRunning(int(pid)) {
				if os.Remove(lockPath) == nil {
					continue
				}
			}
		}
		time.Sleep(lockRetry)
	}
	return fmt.Errorf("Failed to acquire schedule lock: %s", lockPath)
}

func releaseLock(lockPath string) { _ = os.Remove(lockPath) }

type scheduleStore struct {
	filePath, lockPath string

	mu   sync.Mutex // the in-memory cache; the lock file covers other processes
	jobs []scheduledSubagent
}

func newScheduleStore(filePath string) *scheduleStore {
	s := &scheduleStore{filePath: filePath, lockPath: filePath + ".lock"}
	s.load()
	return s
}

// load reads the file into the cache. Silent on a missing or corrupt file: a file that is not JSON, or is `null`,
// leaves the cache as it is; any other JSON replaces it with the jobs the file lists (none when it lists none, or
// something that is not a list). A job record that is not an object ends the list, and one with a field of the wrong
// type is skipped. The caller holds s.mu.
func (s *scheduleStore) load() {
	raw, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	var data any
	if json.Unmarshal(raw, &data) != nil || data == nil {
		return
	}
	s.jobs = s.jobs[:0:0]
	file, _ := data.(map[string]any)
	list, _ := file["jobs"].([]any)
	for _, item := range list {
		if _, isObject := item.(map[string]any); !isObject {
			return
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			continue
		}
		var job scheduledSubagent
		if json.Unmarshal(encoded, &job) == nil {
			s.set(job)
		}
	}
}

// set stores a job under its id: a known id keeps its place (a JavaScript Map's order). The caller holds s.mu.
func (s *scheduleStore) set(job scheduledSubagent) {
	for i := range s.jobs {
		if s.jobs[i].ID == job.ID {
			s.jobs[i] = job
			return
		}
	}
	s.jobs = append(s.jobs, job)
}

func (s *scheduleStore) index(id string) int {
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			return i
		}
	}
	return -1
}

// save writes the cache atomically: temp file, then rename. The caller holds s.mu.
func (s *scheduleStore) save() error {
	jobs := s.jobs
	if jobs == nil {
		jobs = []scheduledSubagent{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(scheduleStoreData{Version: 1, Jobs: jobs}); err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, bytes.TrimSuffix(buf.Bytes(), []byte("\n")), 0o666); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

// withLock is: acquire the lock, reload, mutate, save, release. The caller holds s.mu.
func (s *scheduleStore) withLock(fn func()) error {
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o777); err != nil {
		return err
	}
	if err := acquireLock(s.lockPath); err != nil {
		return err
	}
	defer releaseLock(s.lockPath)
	s.load()
	fn()
	return s.save()
}

// list is a snapshot of the cache.
func (s *scheduleStore) list() []scheduledSubagent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]scheduledSubagent{}, s.jobs...)
}

// hasName reports whether a job other than exceptID has this name.
func (s *scheduleStore) hasName(name, exceptID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID != exceptID && j.Name == name {
			return true
		}
	}
	return false
}

func (s *scheduleStore) get(id string) (scheduledSubagent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.index(id); i >= 0 {
		return s.jobs[i], true
	}
	return scheduledSubagent{}, false
}

func (s *scheduleStore) add(job scheduledSubagent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withLock(func() { s.set(job) })
}

// update applies patch to the job and persists it; it reports false for an unknown id without touching the disk (not
// even creating the directory).
func (s *scheduleStore) update(id string, patch func(*scheduledSubagent)) (scheduledSubagent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index(id) < 0 {
		return scheduledSubagent{}, false, nil
	}
	var updated scheduledSubagent
	found := false
	err := s.withLock(func() {
		if i := s.index(id); i >= 0 {
			patch(&s.jobs[i])
			updated, found = s.jobs[i], true
		}
	})
	return updated, found && err == nil, err
}

func (s *scheduleStore) remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index(id) < 0 {
		return false, nil
	}
	removed := false
	err := s.withLock(func() {
		if i := s.index(id); i >= 0 {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			removed = true
		}
	})
	return removed && err == nil, err
}

// deleteFileIfEmpty deletes the backing file when no jobs remain.
func (s *scheduleStore) deleteFileIfEmpty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jobs) == 0 {
		_ = os.Remove(s.filePath)
	}
}

// nanoid is a 10-character id of the default nanoid alphabet. upstream: src/schedule.ts `nanoid(10)`.
func nanoid() string {
	const alphabet = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict"
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[b[i]&63]
	}
	return string(b)
}
