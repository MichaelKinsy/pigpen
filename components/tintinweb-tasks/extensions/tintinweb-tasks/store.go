package tintinweb_tasks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// File-backed task store with CRUD, dependency management and file locking. upstream: task-store.ts.
//
// In memory with no path; otherwise a JSON file that every mutation locks, re-reads, applies and writes back,
// so several sessions can share one list.

const (
	lockRetry      = 50 * time.Millisecond
	lockMaxRetries = 100 // 5 s at most
)

// clockFn and renameHookFn are test seams (the original's tests use fake timers and a mocked rename); they
// are atomic because the extension's own goroutines read them while a test swaps them.
var (
	clockFn      atomic.Pointer[func() int64]
	renameHookFn atomic.Pointer[func()]
)

// nowMs is the clock in milliseconds.
func nowMs() int64 {
	if f := clockFn.Load(); f != nil {
		return (*f)()
	}
	return time.Now().UnixMilli()
}

// setClock replaces the clock and returns the function that restores the previous one.
func setClock(f func() int64) (restore func()) {
	prev := clockFn.Swap(&f)
	return func() { clockFn.Store(prev) }
}

// setRenameHook runs fn just before the store's atomic rename, inside the critical section (nil clears it).
func setRenameHook(fn func()) {
	if fn == nil {
		renameHookFn.Store(nil)
		return
	}
	renameHookFn.Store(&fn)
}

type updateFields struct {
	Status, Subject, Description, ActiveForm, Owner *string
	Metadata                                        map[string]any // a null value deletes the key
	AddBlocks, AddBlockedBy                         []string
}

type updateResult struct {
	Task          *task
	ChangedFields []string
	Warnings      []string
}

type taskStore struct {
	filePath, lockPath string
	nextID             int
	order              []string // insertion order of the ids (a JavaScript Map iterates in it)
	tasks              map[string]*task
}

// newTaskStore opens a store: "" is in memory, an absolute path is that file, anything else a list name under
// <agent dir>/tasks (the original's ~/.pi/tasks, moved under PiG's agent directory). The directory is created lazily, on the first write. upstream: task-store.ts:101-112.
func newTaskStore(listIDOrPath string) *taskStore {
	s := &taskStore{nextID: 1, tasks: map[string]*task{}}
	if listIDOrPath == "" {
		return s
	}
	path := listIDOrPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(agentDir(), "tasks", listIDOrPath+".json")
	}
	s.filePath = path
	s.lockPath = path + ".lock"
	s.load()
	return s
}

// jsParseInt is parseInt(s, 10): the leading integer of a string, NaN-as-ok=false when there is none.
func jsParseInt(s string) (int, bool) {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	end := 0
	if end < len(s) && (s[end] == '+' || s[end] == '-') {
		end++
	}
	digits := end
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == digits {
		return 0, false
	}
	n, err := strconv.Atoi(s[:end])
	return n, err == nil
}

// acquireLock takes the lock file with O_EXCL and returns the token written into it (`<pid>:<random>`: the
// pid is what the staleness check parses, the random part lets a holder tell its lock from a successor's).
// A lock naming a dead process is stale; so is one with no readable pid, after a couple of polls (the file is
// created before the pid is written, so a live acquirer can look unparseable for a moment). upstream: task-store.ts:25-60.
func acquireLock(lockPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return "", err
	}
	token := strconv.Itoa(os.Getpid()) + ":" + newRequestID()
	for i := 0; i < lockMaxRetries; i++ {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := f.WriteString(token)
			f.Close()
			if werr != nil {
				return "", werr
			}
			return token, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		if data, rerr := os.ReadFile(lockPath); rerr == nil {
			pid, ok := jsParseInt(string(data))
			stale := i >= 2
			if ok && pid > 0 {
				stale = !isProcessRunning(pid)
			}
			if stale {
				os.Remove(lockPath)
				continue
			}
		}
		time.Sleep(lockRetry)
	}
	return "", fmt.Errorf("Failed to acquire lock: %s", lockPath)
}

// releaseLock removes the lock only if this holder still owns it: a lock can be reclaimed from under a live
// holder (another PID namespace reads our pid as dead), and deleting the successor's would let two sessions
// write at once. upstream: task-store.ts:62-71.
func releaseLock(lockPath, token string) {
	if data, err := os.ReadFile(lockPath); err == nil && string(data) == token {
		os.Remove(lockPath)
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func asStrings(v any) []string {
	out := []string{}
	if list, ok := v.([]any); ok {
		for _, e := range list {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func asInt64(v any, fallback int64) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return fallback
}

// normalizeTask builds a task from a persisted record, filling the defaults of older versions and guarding
// against wrong types in hand-edited files. upstream: task-store.ts:77-87.
func normalizeTask(m map[string]any) *task {
	now := nowMs()
	meta, ok := m["metadata"].(map[string]any)
	if !ok {
		meta = map[string]any{}
	}
	return &task{
		ID: asString(m["id"]), Subject: asString(m["subject"]), Description: asString(m["description"]), Status: asString(m["status"]),
		ActiveForm: asString(m["activeForm"]), Owner: asString(m["owner"]), Metadata: meta,
		Blocks: asStrings(m["blocks"]), BlockedBy: asStrings(m["blockedBy"]),
		CreatedAt: asInt64(m["createdAt"], now), UpdatedAt: asInt64(m["updatedAt"], now),
	}
}

// load reads the store from disk (file-backed only). Anything unusable leaves the current state alone: a
// missing task array or counter once corrupted the store (the task ID "NaN", IDs restarting at "0" and
// colliding). upstream: task-store.ts:100-130.
func (s *taskStore) load() {
	if s.filePath == "" {
		return
	}
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	var parsed any
	if json.Unmarshal(data, &parsed) != nil {
		return
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return
	}
	list, ok := obj["tasks"].([]any)
	if !ok {
		return
	}
	loaded := map[string]*task{}
	var order []string
	maxID := 0.0
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		id, ok := m["id"].(string)
		if !ok {
			continue
		}
		if _, dup := loaded[id]; !dup {
			order = append(order, id)
		}
		loaded[id] = normalizeTask(m)
		if n := jsNumber(id); !math.IsNaN(n) && !math.IsInf(n, 0) && n > maxID {
			maxID = n
		}
	}
	s.tasks, s.order = loaded, order
	// Every future task ID comes from this counter, so it has to clear the IDs already in use.
	if n, ok := obj["nextId"].(float64); ok && n == math.Trunc(n) && n > maxID {
		s.nextID = int(n)
	} else {
		s.nextID = int(maxID) + 1
	}
}

// save writes the store to disk atomically (file-backed only). upstream: task-store.ts:133-143.
func (s *taskStore) save() error {
	if s.filePath == "" {
		return nil
	}
	data := storeData{NextID: s.nextID, Tasks: make([]task, 0, len(s.order))}
	for _, id := range s.order {
		data.Tasks = append(data.Tasks, *s.tasks[id])
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, bytes.TrimRight(buf.Bytes(), "\n"), 0o644); err != nil {
		return err
	}
	if h := renameHookFn.Load(); h != nil {
		(*h)()
	}
	return os.Rename(tmp, s.filePath)
}

// withLock runs a mutation under the file lock: acquire, re-read the latest state, apply, write, release. A
// failure to lock or write panics with the error, as the original throws; the tools turn it into an error result.
func withLock[T any](s *taskStore, fn func() T) T {
	if s.lockPath == "" {
		return fn()
	}
	token, err := acquireLock(s.lockPath)
	if err != nil {
		panic(err)
	}
	defer releaseLock(s.lockPath, token)
	s.load()
	result := fn()
	if err := s.save(); err != nil {
		panic(err)
	}
	return result
}

func (s *taskStore) remove(id string) {
	delete(s.tasks, id)
	s.order = slices.DeleteFunc(s.order, func(o string) bool { return o == id })
}

// dropEdgesTo removes every dependency edge that points at a task id.
func (s *taskStore) dropEdgesTo(id string) {
	for _, t := range s.tasks {
		t.Blocks = slices.DeleteFunc(t.Blocks, func(b string) bool { return b == id })
		t.BlockedBy = slices.DeleteFunc(t.BlockedBy, func(b string) bool { return b == id })
	}
}

func (s *taskStore) create(subject, description, activeForm string, metadata map[string]any) *task {
	return withLock(s, func() *task {
		now := nowMs()
		if metadata == nil {
			metadata = map[string]any{}
		}
		t := &task{ID: strconv.Itoa(s.nextID), Subject: subject, Description: description, Status: statusPending,
			ActiveForm: activeForm, Metadata: metadata, Blocks: []string{}, BlockedBy: []string{}, CreatedAt: now, UpdatedAt: now}
		s.nextID++
		s.tasks[t.ID] = t
		s.order = append(s.order, t.ID)
		return t
	})
}

func (s *taskStore) get(id string) *task {
	s.load()
	return s.tasks[id]
}

// list returns all tasks, sorted by the given order (nil is ID ascending).
func (s *taskStore) list(order any) []*task {
	s.load()
	all := make([]*task, 0, len(s.order))
	for _, id := range s.order {
		all = append(all, s.tasks[id])
	}
	return sortBy(all, func(t *task) *task { return t }, order)
}

// update applies fields to a task. upstream: task-store.ts:187-278.
func (s *taskStore) update(id string, f updateFields) updateResult {
	return withLock(s, func() updateResult {
		t, ok := s.tasks[id]
		changed, warnings := []string{}, []string{}
		if !ok {
			return updateResult{ChangedFields: changed, Warnings: warnings}
		}
		if f.Status != nil && *f.Status == statusDeleted {
			s.remove(id)
			s.dropEdgesTo(id)
			return updateResult{ChangedFields: []string{"deleted"}, Warnings: warnings}
		}
		if f.Status != nil {
			t.Status = *f.Status
			changed = append(changed, "status")
		}
		if f.Subject != nil {
			t.Subject = *f.Subject
			changed = append(changed, "subject")
		}
		if f.Description != nil {
			t.Description = *f.Description
			changed = append(changed, "description")
		}
		if f.ActiveForm != nil {
			t.ActiveForm = *f.ActiveForm
			changed = append(changed, "activeForm")
		}
		if f.Owner != nil {
			t.Owner = *f.Owner
			changed = append(changed, "owner")
		}
		// Metadata: a shallow merge in which null deletes a key.
		if f.Metadata != nil {
			if t.Metadata == nil {
				t.Metadata = map[string]any{}
			}
			for key, value := range f.Metadata {
				if value == nil {
					delete(t.Metadata, key)
				} else {
					t.Metadata[key] = value
				}
			}
			changed = append(changed, "metadata")
		}
		// Bidirectional dependency edges, with warnings for problematic ones.
		if len(f.AddBlocks) > 0 {
			for _, target := range f.AddBlocks {
				if !slices.Contains(t.Blocks, target) {
					t.Blocks = append(t.Blocks, target)
				}
				other := s.tasks[target]
				if other != nil && !slices.Contains(other.BlockedBy, id) {
					other.BlockedBy = append(other.BlockedBy, id)
					other.UpdatedAt = nowMs()
				}
				switch {
				case target == id:
					warnings = append(warnings, "#"+id+" blocks itself")
				case other == nil:
					warnings = append(warnings, "#"+target+" does not exist")
				case slices.Contains(other.Blocks, id):
					warnings = append(warnings, "cycle: #"+id+" and #"+target+" block each other")
				}
			}
			changed = append(changed, "blocks")
		}
		if len(f.AddBlockedBy) > 0 {
			for _, target := range f.AddBlockedBy {
				if !slices.Contains(t.BlockedBy, target) {
					t.BlockedBy = append(t.BlockedBy, target)
				}
				other := s.tasks[target]
				if other != nil && !slices.Contains(other.Blocks, id) {
					other.Blocks = append(other.Blocks, id)
					other.UpdatedAt = nowMs()
				}
				switch {
				case target == id:
					warnings = append(warnings, "#"+id+" blocks itself")
				case other == nil:
					warnings = append(warnings, "#"+target+" does not exist")
				case slices.Contains(t.Blocks, target):
					warnings = append(warnings, "cycle: #"+id+" and #"+target+" block each other")
				}
			}
			changed = append(changed, "blockedBy")
		}
		t.UpdatedAt = nowMs()
		return updateResult{Task: t, ChangedFields: changed, Warnings: warnings}
	})
}

// delete removes a task by ID and reports whether it existed.
func (s *taskStore) delete(id string) bool {
	return withLock(s, func() bool {
		if _, ok := s.tasks[id]; !ok {
			return false
		}
		s.remove(id)
		s.dropEdgesTo(id)
		return true
	})
}

// clearAll removes every task and returns how many there were.
func (s *taskStore) clearAll() int {
	return withLock(s, func() int {
		n := len(s.tasks)
		s.tasks, s.order = map[string]*task{}, nil
		return n
	})
}

// clearCompleted removes every completed task and the edges that pointed at them.
func (s *taskStore) clearCompleted() int {
	return withLock(s, func() int {
		count := 0
		for _, id := range slices.Clone(s.order) {
			if s.tasks[id].Status == statusCompleted {
				s.remove(id)
				count++
			}
		}
		if count > 0 {
			for _, t := range s.tasks {
				t.Blocks = slices.DeleteFunc(t.Blocks, func(b string) bool { return s.tasks[b] == nil })
				t.BlockedBy = slices.DeleteFunc(t.BlockedBy, func(b string) bool { return s.tasks[b] == nil })
			}
		}
		return count
	})
}

// snapshot captures the full store state (a copy), to carry tasks into a forked session.
func (s *taskStore) snapshot() storeData {
	s.load()
	d := storeData{NextID: s.nextID, Tasks: make([]task, 0, len(s.order))}
	for _, id := range s.order {
		t := *s.tasks[id]
		t.Metadata = cloneMap(t.Metadata)
		t.Blocks, t.BlockedBy = slices.Clone(t.Blocks), slices.Clone(t.BlockedBy)
		d.Tasks = append(d.Tasks, t)
	}
	return d
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// seed fills an empty store from a snapshot; it is a no-op on a store that already has tasks, so re-pointing
// at an already-seeded fork file never duplicates.
func (s *taskStore) seed(d storeData) {
	if len(s.tasks) > 0 {
		return
	}
	withLock(s, func() struct{} {
		s.nextID = d.NextID
		s.tasks, s.order = map[string]*task{}, nil
		for i := range d.Tasks {
			t := d.Tasks[i]
			s.tasks[t.ID] = &t
			s.order = append(s.order, t.ID)
		}
		return struct{}{}
	})
}

// deleteFileIfEmpty deletes the backing file of an emptied file-backed store.
func (s *taskStore) deleteFileIfEmpty() bool {
	if s.filePath == "" || len(s.tasks) > 0 {
		return false
	}
	os.Remove(s.filePath)
	return true
}
