package schedule_prompt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// memStorage is the in-memory CronStorage stand-in of the original's tests.
type memStorage struct {
	mu   sync.Mutex
	jobs []*jsobj.Object
}

func makeStorage(seed ...*jsobj.Object) *memStorage {
	return &memStorage{jobs: append([]*jsobj.Object(nil), seed...)}
}

func (m *memStorage) HasJobWithName(name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if strictEqualString(j.Get("name"), name) {
			return true, nil
		}
	}
	return false, nil
}
func (m *memStorage) AddJob(job *jsobj.Object) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs = append(m.jobs, job)
	return nil
}
func (m *memStorage) RemoveJob(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, j := range m.jobs {
		if strictEqualString(j.Get("id"), id) {
			m.jobs = append(m.jobs[:i:i], m.jobs[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}
func (m *memStorage) UpdateJob(id string, partial *jsobj.Object) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if strictEqualString(j.Get("id"), id) {
			j.Assign(partial)
			return true, nil
		}
	}
	return false, nil
}

// GetJob and GetAllJobs return copies, as the real storage does (it parses the file on every call); the original's mock returns the
// live objects, which a Go test reading them while a timer goroutine updates them would turn into a data race.
func (m *memStorage) GetJob(id string) (*jsobj.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if strictEqualString(j.Get("id"), id) {
			return j.Clone(), nil
		}
	}
	return nil, nil
}
func (m *memStorage) GetAllJobs() ([]*jsobj.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*jsobj.Object, len(m.jobs))
	for i, j := range m.jobs {
		out[i] = j.Clone()
	}
	return out, nil
}

// sentMessage is one pi.sendMessage call.
type sentMessage struct {
	msg  customMessage
	opts *deliverOptions
}

type sentUser struct{ text, deliverAs string }

// mockPi is the minimal ExtensionAPI of the original's tests: sendMessage, sendUserMessage and events.emit.
type mockPi struct {
	mu        sync.Mutex
	messages  []sentMessage
	users     []sentUser
	emitted   []*jsobj.Object
	sendError func(call int) error // nil: every sendMessage succeeds
}

func (p *mockPi) SendMessage(msg customMessage, opts *deliverOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, sentMessage{msg, opts})
	if p.sendError != nil {
		return p.sendError(len(p.messages))
	}
	return nil
}
func (p *mockPi) SendUserMessage(text, deliverAs string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.users = append(p.users, sentUser{text, deliverAs})
	return nil
}
func (p *mockPi) Emit(_ string, data any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if o, ok := data.(*jsobj.Object); ok {
		p.emitted = append(p.emitted, o)
	}
}
func (p *mockPi) messageCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messages)
}
func (p *mockPi) message(i int) sentMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.messages[i]
}

type fakeSession struct {
	id      string
	entries []map[string]any
}

func (f fakeSession) SessionID() string { return f.id }
func makeCtx(id ...string) fakeSession {
	if len(id) > 0 {
		return fakeSession{id: id[0]}
	}
	return fakeSession{id: "test-session"}
}
func (f fakeSession) Entries() ([]map[string]any, error) { return f.entries, nil }

// exampleJob is the `exampleJob(overrides)` of the original's tests (scheduler.test.ts: id "job-1").
func exampleJob(overrides map[string]any) *jsobj.Object {
	j := jsobj.New()
	j.Set("id", "job-1")
	j.Set("name", "demo")
	j.Set("schedule", "+10s")
	j.Set("prompt", "do the thing")
	j.Set("enabled", true)
	j.Set("type", "once")
	j.Set("createdAt", isoNow())
	j.Set("runCount", float64(0))
	for k, v := range overrides {
		j.Set(k, v)
	}
	return j
}

type obj = map[string]any

// waitFor polls until cond holds (vitest's vi.waitFor: 1 s, every 50 ms).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("waitFor: the condition did not hold")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stubRunner returns a runner that answers result and records its calls.
type runnerCalls struct {
	mu    sync.Mutex
	calls []runnerCall
}
type runnerCall struct {
	prompt, model string
	abort         context.Context
	opts          subagentOptions
}

func (r *runnerCalls) runner(result subagentResult) subagentRunner {
	return func(prompt, model string, abort context.Context, opts subagentOptions) subagentResult {
		r.mu.Lock()
		r.calls = append(r.calls, runnerCall{prompt, model, abort, opts})
		r.mu.Unlock()
		return result
	}
}
func (r *runnerCalls) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// captureLog replaces the console for a test.
func captureLog(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var lines []string
	old := logf
	logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { logf = old })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
}

var errSimulated = errors.New("pi is stale (simulated teardown)")

func newSched(storage jobStore, pi piAPI, sess sessionInfo, run subagentRunner) *scheduler {
	if run == nil {
		run = func(string, string, context.Context, subagentOptions) subagentResult {
			panic("the subagent runner was not expected to run")
		}
	}
	return newScheduler(storage, pi, sess, run)
}

func getStatus(j *jsobj.Object) any { return j.Get("lastStatus") }
