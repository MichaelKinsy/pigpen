package acp

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeSession is the structural fake session of the JS tests ({sessionId, proc, ...}).
type fakeSession struct {
	mu          sync.Mutex
	id, cwd     string
	proc        Proc
	promptFn    func(msg string, images []Image) TurnResult
	prompts     []promptCall
	startupInfo []string
	startupSent int
	publishFn   func()
	cancelled   bool
}

func (s *fakeSession) ID() string               { return s.id }
func (s *fakeSession) Cwd() string              { return s.cwd }
func (s *fakeSession) Proc() Proc               { return s.proc }
func (s *fakeSession) Cancel() error            { s.cancelled = true; return nil }
func (s *fakeSession) WasCancelRequested() bool { return false }
func (s *fakeSession) PublishContextUsage() {
	if s.publishFn != nil {
		s.publishFn()
	}
}
func (s *fakeSession) SetStartupInfo(text string) {
	s.mu.Lock()
	s.startupInfo = append(s.startupInfo, text)
	s.mu.Unlock()
}
func (s *fakeSession) SendStartupInfoIfPending() {
	s.mu.Lock()
	s.startupSent++
	s.mu.Unlock()
}
func (s *fakeSession) Prompt(message string, images []Image) <-chan TurnResult {
	s.mu.Lock()
	s.prompts = append(s.prompts, promptCall{message, images})
	fn := s.promptFn
	s.mu.Unlock()
	ch := make(chan TurnResult, 1)
	if fn != nil {
		ch <- fn(message, images)
	} else {
		ch <- TurnResult{Reason: StopEndTurn}
	}
	return ch
}

// fakeSessions is the FakeSessions class of the JS tests.
type fakeSessions struct {
	mu          sync.Mutex
	session     ActiveSession
	restored    ActiveSession
	closeCalls  []string
	closeOthers []string
	createErr   error
	// build makes the session GetOrCreate registers (session-restore.test.ts).
	build func(id string, p SessionCreateParams) ActiveSession
	// anyID makes MaybeGet/Get return the session for every id (agent tests that ignore the id).
	anyID bool
}

func (f *fakeSessions) Create(SessionCreateParams) (ActiveSession, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.session, nil
}
func (f *fakeSessions) match(id string) ActiveSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.restored != nil && f.restored.ID() == id {
		return f.restored
	}
	if f.session != nil && (f.anyID || f.session.ID() == id) {
		return f.session
	}
	return nil
}
func (f *fakeSessions) MaybeGet(id string) ActiveSession { return f.match(id) }
func (f *fakeSessions) Get(id string) (ActiveSession, error) {
	if s := f.match(id); s != nil {
		return s, nil
	}
	return nil, errors.New("Unknown sessionId: " + id)
}
func (f *fakeSessions) GetOrCreate(id string, p SessionCreateParams) ActiveSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.restored == nil && f.build != nil {
		f.restored = f.build(id, p)
	}
	return f.restored
}
func (f *fakeSessions) Close(id string) {
	f.mu.Lock()
	f.closeCalls = append(f.closeCalls, id)
	f.mu.Unlock()
}
func (f *fakeSessions) CloseAllExcept(keep string) {
	f.mu.Lock()
	f.closeOthers = append(f.closeOthers, keep)
	f.mu.Unlock()
}
func (f *fakeSessions) DisposeAll() {}

// memStore is a Store double recording calls; the JS tests stub `store` with plain objects.
type memStore struct {
	mu      sync.Mutex
	entries map[string]StoredSession
	upserts []StoredSession
	deletes []string
}

func newMemStore(entries ...StoredSession) *memStore {
	s := &memStore{entries: map[string]StoredSession{}}
	for _, e := range entries {
		s.entries[e.SessionID] = e
	}
	return s
}
func (s *memStore) Get(id string) *StoredSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[id]; ok {
		return &e
	}
	return nil
}
func (s *memStore) Upsert(e StoredSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts = append(s.upserts, e)
	s.entries[e.SessionID] = e
}
func (s *memStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, id)
	delete(s.entries, id)
}

// scheduler replaces Agent.schedule: it collects the deferred work instead of running it,
// as `t.mock.method(globalThis, 'setTimeout', ...)` does, and runs it on demand.
type scheduler struct {
	mu  sync.Mutex
	fns []func()
}

func (s *scheduler) add(fn func()) {
	s.mu.Lock()
	s.fns = append(s.fns, fn)
	s.mu.Unlock()
}
func (s *scheduler) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.fns)
}

// drain is drainScheduledWork: it runs what was scheduled after session/new or session/load.
func (s *scheduler) drain() {
	for {
		s.mu.Lock()
		fns := s.fns
		s.fns = nil
		s.mu.Unlock()
		if len(fns) == 0 {
			return
		}
		for _, fn := range fns {
			fn()
		}
	}
}

// testAgent is `new PiAcpAgent(conn)` with the seams the twins replace.
func testAgent(conn Conn) (*Agent, *scheduler) {
	a := NewAgent(conn)
	sch := &scheduler{}
	a.schedule = sch.add
	a.store = newMemStore()
	return a, sch
}

// withAgentDir points PiG's agent directory at dir (PI_CODING_AGENT_DIR in the original).
func withAgentDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PIG_USE_PI_DIRS", "")
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	t.Setenv("PIG_HOME", filepath.Join(t.TempDir(), "pighome"))
}

func writeSessionFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
