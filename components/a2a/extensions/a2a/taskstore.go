package a2aext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

const (
	// taskFileVersion is the format of one task file; a file of another version is skipped, not guessed at.
	taskFileVersion = 1
	// writeInterval bounds how often a task whose state did not change is rewritten: a streamed reply updates the
	// task once per chunk, and writing the whole task for every chunk would be quadratic.
	writeInterval = time.Second
	// pruneInterval bounds how often Create looks for finished tasks past the retention.
	pruneInterval = time.Minute

	defaultListPageSize = 50
	maxListPageSize     = 100
	defaultHistoryLen   = 100
)

// storeOptions configures the task store.
type storeOptions struct {
	// Dir holds one file per task. Empty keeps tasks in memory only.
	Dir string
	// Retention is how long a finished task is kept. Zero keeps them for ever.
	Retention time.Duration
	Now       func() time.Time
	Auth      taskstore.Authenticator
	Logf      func(format string, args ...any)
}

// taskRec is one stored task. The task itself is kept as the JSON that is also the file's content, so a write
// does not encode it again and a copy for the caller is one decode.
type taskRec struct {
	raw       []byte
	version   taskstore.TaskVersion
	user      string
	updated   time.Time
	state     a2a.TaskState
	contextID string
	statusAt  *time.Time

	// persisted is the state and time of the last write of this task's file.
	persistedState a2a.TaskState
	persistedAt    time.Time
}

// taskFile is the on-disk form of a taskRec.
type taskFile struct {
	Format  int             `json:"format"`
	User    string          `json:"user"`
	Version int64           `json:"version"`
	Updated time.Time       `json:"updated"`
	Task    json.RawMessage `json:"task"`
}

// fileStore is the task store: tasks survive a restart of the listener. Each task is one owner-only file named
// by the digest of its id (an id is chosen by the peer and must never reach a file name), written atomically.
// It implements a2a-go's taskstore.Store with the same ownership rules as its in-memory store: a task belongs
// to the principal that created it, and another principal gets "not found".
type fileStore struct {
	dir       string
	retention time.Duration
	now       func() time.Time
	auth      taskstore.Authenticator
	logf      func(format string, args ...any)

	mu        sync.Mutex
	tasks     map[a2a.TaskID]*taskRec
	lastPrune time.Time
	nWrites   atomic.Int64
	nInterr   int
}

func openTaskStore(o storeOptions) (*fileStore, error) {
	s := &fileStore{dir: o.Dir, retention: o.Retention, now: o.Now, auth: o.Auth, logf: o.Logf, tasks: map[a2a.TaskID]*taskRec{}}
	if s.now == nil {
		s.now = time.Now
	}
	if s.auth == nil {
		s.auth = func(context.Context) (string, error) { return "", nil }
	}
	if s.logf == nil {
		s.logf = func(string, ...any) {}
	}
	if s.dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("a2a: task store: %w", err)
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("a2a: task store: %w", err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("a2a: task store: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir():
		case strings.HasSuffix(name, ".tmp"):
			_ = os.Remove(filepath.Join(s.dir, name)) // a write a crash cut short
		case strings.HasSuffix(name, ".json"):
			s.load(name)
		}
	}
	now := s.now()
	for _, rec := range s.tasks {
		if !rec.state.Terminal() {
			if err := s.interrupt(rec, now); err != nil {
				return nil, err
			}
		}
	}
	s.prune(now)
	return s, nil
}

// load reads one task file; a file it cannot use is reported and skipped, never fatal.
func (s *fileStore) load(name string) {
	raw, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		s.logf("task store: skipping %s: %v", name, err)
		return
	}
	var f taskFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Format != taskFileVersion || f.User == "" {
		if err == nil {
			err = fmt.Errorf("not a task file of format %d", taskFileVersion)
		}
		s.logf("task store: skipping %s: %v", name, err)
		return
	}
	var t a2a.Task
	if err := json.Unmarshal(f.Task, &t); err != nil || t.ID == "" {
		if err == nil {
			err = errors.New("the task has no id")
		}
		s.logf("task store: skipping %s: %v", name, err)
		return
	}
	rec := &taskRec{raw: bytes.Clone(f.Task), version: taskstore.TaskVersion(f.Version), user: f.User, updated: f.Updated,
		persistedState: t.Status.State, persistedAt: f.Updated}
	rec.index(&t)
	s.tasks[t.ID] = rec
}

func (r *taskRec) index(t *a2a.Task) {
	r.state, r.contextID, r.statusAt = t.Status.State, t.ContextID, t.Status.Timestamp
}

// interrupt marks a task that no process is running as failed: it was left behind by a listener that stopped
// (or was killed) while the task was going.
func (s *fileStore) interrupt(rec *taskRec, now time.Time) error {
	var t a2a.Task
	if err := json.Unmarshal(rec.raw, &t); err != nil {
		return fmt.Errorf("a2a: task store: %w", err)
	}
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("The listener restarted while the task was running; PiG did not finish it."))
	msg.TaskID, msg.ContextID = t.ID, t.ContextID
	t.Status = a2a.TaskStatus{State: a2a.TaskStateFailed, Message: msg, Timestamp: &now}
	raw, err := json.Marshal(&t)
	if err != nil {
		return fmt.Errorf("a2a: task store: %w", err)
	}
	rec.raw, rec.version, rec.updated = raw, rec.version+1, now
	rec.index(&t)
	s.nInterr++
	if err := s.persist(&t, rec); err != nil {
		s.logf("task store: could not record the failure of interrupted task: %v", err)
	}
	return nil
}

// writes counts the task files written since the store was opened.
func (s *fileStore) writes() int { return int(s.nWrites.Load()) }

// interrupted counts the tasks that were still running when the previous listener stopped.
func (s *fileStore) interrupted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nInterr
}

func (s *fileStore) fileOf(id a2a.TaskID) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

// persist writes the task file atomically with owner-only permissions. The caller holds s.mu.
func (s *fileStore) persist(t *a2a.Task, rec *taskRec) error {
	if s.dir == "" {
		return nil
	}
	body, err := json.Marshal(&taskFile{Format: taskFileVersion, User: rec.user, Version: int64(rec.version), Updated: rec.updated, Task: rec.raw})
	if err != nil {
		return err
	}
	path := s.fileOf(t.ID)
	tmp := path[:len(path)-len(".json")] + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(body)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return werr
	}
	rec.persistedState, rec.persistedAt = rec.state, s.now()
	s.nWrites.Add(1)
	return nil
}

// prune drops finished tasks past the retention. An unfinished task is never dropped. The caller holds s.mu.
func (s *fileStore) prune(now time.Time) {
	s.lastPrune = now
	if s.retention <= 0 {
		return
	}
	for id, rec := range s.tasks {
		if rec.state.Terminal() && now.Sub(rec.updated) > s.retention {
			delete(s.tasks, id)
			if s.dir != "" {
				if err := os.Remove(s.fileOf(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
					s.logf("task store: could not remove an expired task: %v", err)
				}
			}
		}
	}
}

func (s *fileStore) user(ctx context.Context) (string, error) {
	u, err := s.auth(ctx)
	if err != nil {
		return "", fmt.Errorf("taskstore auth failed: %w", err)
	}
	return u, nil
}

// Create implements taskstore.Store.
func (s *fileStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	user, err := s.user(ctx)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	raw, err := json.Marshal(task)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks[task.ID] != nil {
		return taskstore.TaskVersionMissing, taskstore.ErrTaskAlreadyExists
	}
	now := s.now()
	if now.Sub(s.lastPrune) >= pruneInterval {
		s.prune(now)
	}
	rec := &taskRec{raw: raw, version: 1, user: user, updated: now}
	rec.index(task)
	if err := s.persist(task, rec); err != nil {
		return taskstore.TaskVersionMissing, fmt.Errorf("a2a: could not store the task: %w", err)
	}
	s.tasks[task.ID] = rec
	return rec.version, nil
}

// Update implements taskstore.Store. A change of state, and the end of a task, are on disk when it returns; a
// task that only grew (streamed text) is written at most once per writeInterval, so a listener killed mid-task
// loses at most the last interval of text of a task that is reported failed anyway.
func (s *fileStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	user, err := s.user(ctx)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	raw, err := json.Marshal(req.Task)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.tasks[req.Task.ID]
	if rec == nil || rec.user != user {
		return taskstore.TaskVersionMissing, a2a.ErrTaskNotFound
	}
	if req.PrevVersion != taskstore.TaskVersionMissing && rec.version != req.PrevVersion {
		return taskstore.TaskVersionMissing, taskstore.ErrConcurrentModification
	}
	now := s.now()
	rec.raw, rec.version, rec.updated = raw, rec.version+1, now
	rec.index(req.Task)
	if rec.state != rec.persistedState || rec.state.Terminal() || now.Sub(rec.persistedAt) >= writeInterval {
		if err := s.persist(req.Task, rec); err != nil {
			// The task is still served from memory; the next update tries again.
			s.logf("task store: could not write task: %v", err)
		}
	}
	return rec.version, nil
}

// Get implements taskstore.Store.
func (s *fileStore) Get(ctx context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
	s.mu.Lock()
	rec := s.tasks[id]
	var raw []byte
	var version taskstore.TaskVersion
	var owner string
	if rec != nil {
		raw, version, owner = rec.raw, rec.version, rec.user
	}
	s.mu.Unlock()
	if rec == nil {
		return nil, a2a.ErrTaskNotFound
	}
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	if owner != user {
		return nil, a2a.ErrTaskNotFound // ownership is masked as "not found" (A2A §3.3.2)
	}
	var t a2a.Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("task copy failed: %w", err)
	}
	return &taskstore.StoredTask{Task: &t, Version: version, User: owner}, nil
}

// List implements taskstore.Store.
func (s *fileStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	user, err := s.auth(ctx)
	if user == "" || err != nil {
		return nil, a2a.ErrUnauthenticated
	}
	pageSize := req.PageSize
	switch {
	case pageSize == 0:
		pageSize = defaultListPageSize
	case pageSize < 1 || pageSize > maxListPageSize:
		return nil, fmt.Errorf("page size must be between 1 and %d inclusive, got %d: %w", maxListPageSize, pageSize, a2a.ErrInvalidRequest)
	}
	type row struct {
		id      a2a.TaskID
		updated time.Time
		raw     []byte
	}
	var rows []row
	s.mu.Lock()
	for id, rec := range s.tasks {
		switch {
		case rec.user != user:
		case req.ContextID != "" && rec.contextID != req.ContextID:
		case req.Status != a2a.TaskStateUnspecified && rec.state != req.Status:
		case req.StatusTimestampAfter != nil && rec.statusAt != nil && rec.statusAt.Before(*req.StatusTimestampAfter):
		default:
			rows = append(rows, row{id, rec.updated, rec.raw})
		}
	}
	s.mu.Unlock()
	total := len(rows)
	slices.SortFunc(rows, func(a, b row) int {
		if c := b.updated.Compare(a.updated); c != 0 {
			return c
		}
		return strings.Compare(string(b.id), string(a.id))
	})
	if req.PageToken != "" {
		at, id, err := decodeListToken(req.PageToken)
		if err != nil {
			return nil, err
		}
		// Rows are newest first: the page starts at the first row strictly after the cursor row.
		start := slices.IndexFunc(rows, func(r row) bool {
			c := r.updated.Compare(at)
			return c < 0 || (c == 0 && r.id < id)
		})
		if start < 0 {
			rows = nil
		} else {
			rows = rows[start:]
		}
	}
	next := ""
	if pageSize < len(rows) {
		last := rows[pageSize-1]
		next = encodeListToken(last.updated, last.id)
		rows = rows[:pageSize]
	}
	out := make([]*a2a.Task, 0, len(rows))
	historyLen := defaultHistoryLen
	if req.HistoryLength != nil {
		historyLen = *req.HistoryLength
	}
	for _, r := range rows {
		var t a2a.Task
		if err := json.Unmarshal(r.raw, &t); err != nil {
			return nil, err
		}
		if historyLen <= 0 {
			t.History = []*a2a.Message{}
		} else if len(t.History) > historyLen {
			t.History = t.History[len(t.History)-historyLen:]
		}
		if !req.IncludeArtifacts {
			t.Artifacts = nil
		}
		out = append(out, &t)
	}
	return &a2a.ListTasksResponse{Tasks: out, TotalSize: total, PageSize: pageSize, NextPageToken: next}, nil
}

func encodeListToken(updated time.Time, id a2a.TaskID) string {
	return base64.URLEncoding.EncodeToString(fmt.Appendf(nil, "%s_%s", updated.Format(time.RFC3339Nano), id))
}

func decodeListToken(tok string) (time.Time, a2a.TaskID, error) {
	b, err := base64.URLEncoding.DecodeString(tok)
	if err != nil {
		return time.Time{}, "", a2a.ErrParseError
	}
	ts, id, ok := strings.Cut(string(b), "_") // an RFC 3339 time has no "_"; the id may
	if !ok {
		return time.Time{}, "", a2a.ErrParseError
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", a2a.ErrParseError
	}
	return at, a2a.TaskID(id), nil
}

var _ taskstore.Store = (*fileStore)(nil)
