package pi_permission_system

// Shared fixtures for the forwarding lane's twins: the Go counterparts of the original's test/helpers/forwarding-fixtures.ts,
// session-fixtures.ts (makeLogger), decision-fixtures.ts and prompt-details-fixtures.ts, plus the vitest matchers the original
// asserts with (toHaveBeenCalledWith, objectContaining, toMatchObject) as plain functions.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── logger fake (vi.fn() review/debug/warn) ────────────────────────────────

type fwLogCall struct {
	event   string
	details *jsObject
}

type fwLogger struct {
	mu     sync.Mutex
	review []fwLogCall
	debug  []fwLogCall
	warn   []string
}

func (l *fwLogger) Review(event string, details *jsObject) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.review = append(l.review, fwLogCall{event, details})
}

func (l *fwLogger) Debug(event string, details *jsObject) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.debug = append(l.debug, fwLogCall{event, details})
}

func (l *fwLogger) Warn(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warn = append(l.warn, message)
}

func (l *fwLogger) reviews() []fwLogCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]fwLogCall(nil), l.review...)
}

func (l *fwLogger) debugs() []fwLogCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]fwLogCall(nil), l.debug...)
}

func (l *fwLogger) clearReview() {
	l.mu.Lock()
	l.review = nil
	l.mu.Unlock()
}

// reviewCalled is toHaveBeenCalledWith(event, expect.anything()) when want is nil, else with the subset matcher.
func (l *fwLogger) reviewMatching(event string, want any) bool {
	for _, c := range l.reviews() {
		if c.event == event && (want == nil || fwMatch(c.details, want)) {
			return true
		}
	}
	return false
}

// ── object helpers ─────────────────────────────────────────────────────────

// fwObj builds an ordered object from key, value pairs; ints become float64 as JSON numbers do.
func fwObj(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i < len(kv); i += 2 {
		o.set(kv[i].(string), fwNorm(kv[i+1]))
	}
	return o
}

func fwNorm(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = fwNorm(e)
		}
		return out
	}
	return v
}

// fwCanon turns ordered objects into maps so key order does not matter, as toEqual ignores it.
func fwCanon(v any) any {
	switch x := v.(type) {
	case *jsObject:
		m := map[string]any{}
		for k, val := range x.vals {
			if _, u := val.(undef); u {
				continue
			}
			m[k] = fwCanon(val)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = fwCanon(e)
		}
		return out
	}
	return v
}

// fwEqual is toEqual over decoded JSON values.
func fwEqual(a, b any) bool { return reflect.DeepEqual(fwCanon(a), fwCanon(b)) }

// fwMatch is toMatchObject / expect.objectContaining: every key of want is present in got and matches, recursively; arrays and
// scalars compare whole.
func fwMatch(got, want any) bool {
	w, ok := want.(*jsObject)
	if !ok {
		return fwEqual(got, want)
	}
	g, ok := got.(*jsObject)
	if !ok {
		return false
	}
	for _, k := range w.keys {
		gv, present := g.vals[k]
		if _, u := gv.(undef); present && u {
			present = false
		}
		if !present || !fwMatch(gv, w.vals[k]) {
			return false
		}
	}
	return true
}

func fwParse(t *testing.T, raw string) any {
	t.Helper()
	v, err := parseJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return v
}

func fwSP(s string) *string { return &s }

// ── errno error (an fs-shaped error: a plain error carrying a code) ────────

type fwErrno struct{ code, msg string }

func (e *fwErrno) Error() string     { return e.msg }
func (e *fwErrno) ErrnoCode() string { return e.code }

func fwErrnoError(code, operation string) error {
	return &fwErrno{code: code, msg: code + ": operation not permitted, " + operation}
}

// ── temp forwarding directory (forwarding-fixtures.ts createForwardingTempDir) ──

type fwTempDir struct {
	t             *testing.T
	root          string
	forwardingDir string
	location      PermissionForwardingLocation
}

func fwCreateTempDir(t *testing.T, sessionID string, createResponsesDir bool) *fwTempDir {
	t.Helper()
	root := t.TempDir()
	forwardingDir := filepath.Join(root, "forwarding")
	location, err := CreatePermissionForwardingLocation(forwardingDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(location.RequestsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if createResponsesDir {
		if err := os.MkdirAll(location.ResponsesDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &fwTempDir{t: t, root: root, forwardingDir: forwardingDir, location: location}
}

// writeRequest writes a request with the fixture defaults (the original's `{id: "req-forwarded", requesterSessionId: "child-session",
// requesterAgentName: "Explore", payload: makePromptPayload(), ...overrides}`), after the overrides have edited it.
func (d *fwTempDir) writeRequest(overrides ...func(*ForwardedPermissionRequest)) ForwardedPermissionRequest {
	d.t.Helper()
	payload := fwMakePromptPayload()
	request := ForwardedPermissionRequest{
		ID:                 "req-forwarded",
		CreatedAt:          float64(time.Now().UnixMilli()),
		RequesterSessionID: "child-session",
		TargetSessionID:    d.location.SessionID,
		RequesterAgentName: "Explore",
		Payload:            &payload,
	}
	for _, o := range overrides {
		o(&request)
	}
	path := filepath.Join(d.location.RequestsDir, request.ID+".json")
	if err := os.WriteFile(path, []byte(marshalJSON(request.JS(), "")), 0o644); err != nil {
		d.t.Fatal(err)
	}
	return request
}

// readResponse reads the response file of requestID as decoded JSON.
func (d *fwTempDir) readResponse(requestID string) *jsObject {
	d.t.Helper()
	raw, err := os.ReadFile(filepath.Join(d.location.ResponsesDir, requestID+".json"))
	if err != nil {
		d.t.Fatal(err)
	}
	o, ok := fwParse(d.t, string(raw)).(*jsObject)
	if !ok {
		d.t.Fatalf("response is not an object: %s", raw)
	}
	return o
}

// ── fixtures ───────────────────────────────────────────────────────────────

// fwMakePromptPayload is makePromptPayload(): a minimal complete payload for a test that does not render it.
func fwMakePromptPayload() PromptPayload {
	tool, read := "read", "read"
	return PromptPayload{
		Kind: "tool",
		Request: PromptRequestFacts{
			Requester: PromptRequester{},
			Surface:   "read",
			ToolName:  &tool,
			Value:     read,
		},
		Evidence:    []PromptEvidence{},
		Annotations: []PromptAnnotation{},
	}
}

// fwMakeAccessIntent is makeForwardedAccessIntent.
func fwMakeAccessIntent(overrides ...func(*ForwardedAccessIntent)) ForwardedAccessIntent {
	intent := ForwardedAccessIntent{
		ForwardedAccessFacts: ForwardedAccessFacts{Surface: "bash", MatchValues: []string{"git status"}},
		RequesterCwd:         "/worktree/issue-42",
		Principal:            ForwardedPrincipal{SessionID: "child-session", AgentName: "Explore"},
	}
	for _, o := range overrides {
		o(&intent)
	}
	return intent
}

// fwSessionManager is a session manager whose readers are functions a test can swap, as vi.fn() mocks are.
type fwSessionManager struct {
	mu         sync.Mutex
	sessionID  func() (string, error)
	sessionDir func() (string, error)
	entries    func() ([]SessionEntryView, error)
}

func (m *fwSessionManager) GetSessionID() (string, error) {
	m.mu.Lock()
	f := m.sessionID
	m.mu.Unlock()
	if f == nil {
		return "", nil
	}
	return f()
}

func (m *fwSessionManager) GetSessionDir() (string, error) {
	m.mu.Lock()
	f := m.sessionDir
	m.mu.Unlock()
	if f == nil {
		return "", nil
	}
	return f()
}

func (m *fwSessionManager) GetEntries() ([]SessionEntryView, error) {
	if m.entries == nil {
		return nil, nil
	}
	return m.entries()
}

func (m *fwSessionManager) setSessionID(f func() (string, error)) {
	m.mu.Lock()
	m.sessionID = f
	m.mu.Unlock()
}

func fwID(id string) func() (string, error) { return func() (string, error) { return id, nil } }

type fwUI struct{}

func (fwUI) Select(string, []string) (string, bool, error) { return "", false, nil }
func (fwUI) Input(string, string) (string, bool, error)    { return "", false, nil }

// fwMakeForwarderContext is makeForwarderContext.
func fwMakeForwarderContext(hasUI bool, sessionID string) ForwarderContext {
	return ForwarderContext{
		HasUI:          hasUI,
		UI:             fwUI{},
		Cwd:            "/repo",
		SessionManager: &fwSessionManager{sessionID: fwID(sessionID), sessionDir: fwID("")},
	}
}

// fwClearHintEnv blanks every subagent env hint, as the original's beforeEach does with vi.stubEnv(key, undefined).
func fwClearHintEnv(t *testing.T) {
	t.Helper()
	for _, key := range SubagentEnvHintKeys {
		t.Setenv(key, "")
	}
}

// fwPoll waits for cond, for tests that must observe state a goroutine reaches (never to hide a missing synchronization).
func fwPoll(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func fwJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func fwHasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }
