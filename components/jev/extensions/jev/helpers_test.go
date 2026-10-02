package jev_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	jev "github.com/MichaelKinsy/pigpen/jev"
)

// ---- a fake Jev HTTP endpoint ------------------------------------------------

// jevReply is the scripted reply to one request.
type jevReply struct {
	status int
	body   any // marshalled unless it is a string
	delay  time.Duration
}

type recordedRequest struct {
	Path string
	Auth string
	Body map[string]any
	Raw  string
}

type fakeJev struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []recordedRequest
	// next decides the reply for request number i (0-based).
	next func(i int, req recordedRequest) jevReply
}

func newFakeJev(t *testing.T, next func(i int, req recordedRequest) jevReply) *fakeJev {
	t.Helper()
	f := &fakeJev{next: next}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec := recordedRequest{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Raw: string(raw)}
		_ = json.Unmarshal(raw, &rec.Body)
		f.mu.Lock()
		i := len(f.got)
		f.got = append(f.got, rec)
		f.mu.Unlock()
		reply := f.next(i, rec)
		if reply.delay > 0 {
			time.Sleep(reply.delay)
		}
		if reply.status == 0 {
			reply.status = 200
		}
		w.WriteHeader(reply.status)
		if s, ok := reply.body.(string); ok {
			_, _ = io.WriteString(w, s)
			return
		}
		_ = json.NewEncoder(w).Encode(reply.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJev) requests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.got...)
}

func (f *fakeJev) count() int { return len(f.requests()) }

// always replies with the same body.
func always(body any) func(int, recordedRequest) jevReply {
	return func(int, recordedRequest) jevReply { return jevReply{body: body} }
}

func noul(v float64) map[string]any { return map[string]any{"type": "noul", "noul": v} }

func score(v, conf float64) map[string]any {
	return map[string]any{"type": "score", "score": v, "confidence": conf,
		"legend": map[string]any{"0": "a", "1": "b", "2": "c", "3": "d"}, "probabilities": map[string]any{"0": 0.1, "1": 0.2, "2": 0.3, "3": 0.4}}
}

func choice(name string, conf float64) map[string]any {
	return map[string]any{"type": "choice", "choice": name, "confidence": conf, "probabilities": map[string]any{name: conf}}
}

// gateBody is a Jev response for the four gate questions.
func gateBody(destructive, exfil, beyond, impact, impactConf float64) map[string]any {
	return map[string]any{"model": "jev-test", "answers": map[string]any{
		"destructive": noul(destructive), "exfiltration": noul(exfil), "beyond_scope": noul(beyond), "impact": score(impact, impactConf),
	}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 2}}
}

func clearGate() map[string]any   { return gateBody(0.03, 0.04, 0.4, 0.02, 0.9) }
func flaggedGate() map[string]any { return gateBody(0.99, 0.79, 0.98, 3.0, 0.91) }

func outBody(leak float64, class string, conf float64) map[string]any {
	return map[string]any{"model": "jev-test", "answers": map[string]any{
		"leaks_secret": noul(leak), "failure_class": choice(class, conf),
	}}
}

// ---- environment ---------------------------------------------------------------

const testKey = "tsk-test-key-0123456789"

type env struct {
	agent string
	cwd   string
}

// newEnv points the agent config directory at a temp dir, clears the API key
// variable and returns the directories. Tests are not parallel: the extension
// reads process environment like the real one.
func newEnv(t *testing.T) *env {
	t.Helper()
	agent := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("HOME", t.TempDir())
	return &env{agent: agent, cwd: t.TempDir()}
}

func (e *env) writeGlobal(t *testing.T, cfg map[string]any) {
	t.Helper()
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(e.agent, "pi-jev.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *env) writeProject(t *testing.T, cfg map[string]any) {
	t.Helper()
	dir := filepath.Join(e.cwd, ".pig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "pi-jev.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// jevConfig is a global config that opts in to the Jev HTTP backend against srv.
// display "plain" reproduces the original's wording; the disclosure banner is
// acknowledged so a trace holds only what the original also produced.
func jevConfig(srv *fakeJev, extra map[string]any) map[string]any {
	cfg := map[string]any{
		"enabled": true, "acknowledged": true, "display": "plain", "backend": "typesafe",
		"endpoint": srv.srv.URL, "model": "jev-test", "retries": 0, "timeoutMs": 5000,
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

// hostState is what the fake host answers for the calls the extension makes.
type hostState struct {
	mu        sync.Mutex
	confirm   bool
	user      string // initial user message in the session branch
	entries   []map[string]any
	model     map[string]any
	completes []map[string]any
	complete  func(model, request map[string]any) map[string]any
	confirms  []map[string]any
}

func newHostState() *hostState {
	return &hostState{model: map[string]any{"id": "judge-1", "name": "judge-1", "provider": "acme"}}
}

func (s *hostState) onCall(method string, args map[string]any) (map[string]any, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case "ui.confirm":
		s.confirms = append(s.confirms, args)
		return map[string]any{"confirmed": s.confirm}, ""
	case "getModelInfo":
		return s.model, ""
	case "getModel":
		return map[string]any{"provider": args["provider"], "id": args["modelId"], "api": "openai-completions"}, ""
	case "getModelAuth":
		return map[string]any{"ok": true, "apiKey": "host-held-key"}, ""
	case "watchSessionLog":
		if len(s.entries) == 0 && s.user != "" {
			s.entries = append(s.entries, userEntry("e1", "", s.user))
		}
		cursor, _ := args["cursor"].(float64)
		if int(cursor) > len(s.entries) {
			cursor = float64(len(s.entries))
		}
		leaf := ""
		if n := len(s.entries); n > 0 {
			leaf, _ = s.entries[n-1]["id"].(string)
		}
		return map[string]any{"entries": s.entries[int(cursor):], "entryCount": len(s.entries), "hasMore": false, "leafId": leaf}, ""
	case "complete":
		s.completes = append(s.completes, args)
		if s.complete != nil {
			m, _ := args["model"].(map[string]any)
			r, _ := args["request"].(map[string]any)
			return s.complete(m, r), ""
		}
		return nil, "no completion scripted"
	}
	return nil, ""
}

func (s *hostState) completeCalls() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.completes...)
}

func (s *hostState) confirmCalls() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.confirms...)
}

func textMessage(text string) map[string]any {
	return map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": text}}}
}

// start builds the host, fires session_start and returns.
func start(t *testing.T, e *env, hs *hostState, opts HostOptions) *Host {
	t.Helper()
	opts.Cwd = e.cwd
	if opts.OnCall == nil {
		opts.OnCall = hs.onCall
	}
	h := StartHost(t, jev.Extension(), opts)
	h.Fire("session_start", map[string]any{"reason": "startup"})
	return h
}

// ---- reading what the extension did --------------------------------------------

func (h *Host) notifications() []string {
	var out []string
	for _, c := range h.CallsTo("ui.notify") {
		msg, _ := c.Args["message"].(string)
		level, _ := c.Args["level"].(string)
		out = append(out, level+": "+msg)
	}
	return out
}

func (h *Host) statuses() []string {
	var out []string
	for _, c := range h.CallsTo("ui.setStatus") {
		text, _ := c.Args["text"].(string)
		out = append(out, text)
	}
	return out
}

func (h *Host) lastStatus() string {
	s := h.statuses()
	if len(s) == 0 {
		return "<none>"
	}
	return s[len(s)-1]
}

func (h *Host) anyNotification(substr string) bool {
	for _, n := range h.notifications() {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

// toolCall fires a tool_call event and decodes the {block, reason} result.
func (h *Host) toolCall(name string, input map[string]any) (block bool, reason string) {
	h.t.Helper()
	raw := h.Fire("tool_call", map[string]any{"toolName": name, "toolCallId": "c1", "input": input})
	if len(raw) == 0 || string(raw) == "null" {
		return false, ""
	}
	var r struct {
		Block  bool   `json:"block"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		h.t.Fatalf("tool_call result %s: %v", raw, err)
	}
	return r.Block, r.Reason
}

// toolResult fires tool_result and returns the appended text blocks ("" when the
// handler left the result alone).
func (h *Host) toolResult(name string, input map[string]any, content []any, isError bool) (patched []string, none bool) {
	h.t.Helper()
	raw := h.Fire("tool_result", map[string]any{"toolName": name, "toolCallId": "c1", "input": input, "content": content, "isError": isError})
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		h.t.Fatalf("tool_result result %s: %v", raw, err)
	}
	if r.Content == nil {
		return nil, true
	}
	for _, c := range r.Content {
		patched = append(patched, c.Text)
	}
	return patched, false
}

func text(s string) []any { return []any{map[string]any{"type": "text", "text": s}} }

// stateOf returns the decoded "state" of a recorded gate request.
func stateOf(t *testing.T, r recordedRequest) map[string]any {
	t.Helper()
	st, ok := r.Body["state"].(map[string]any)
	if !ok {
		t.Fatalf("request state is not an object: %s", r.Raw)
	}
	return st
}

func mustContain(t *testing.T, what, got string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(got, s) {
			t.Errorf("%s = %q, want it to contain %q", what, got, s)
		}
	}
}

func userEntry(id, parent, text string) map[string]any {
	e := map[string]any{"type": "message", "id": id, "message": map[string]any{
		"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}}
	if parent != "" {
		e["parentId"] = parent
	}
	return e
}

// pushUser appends a user message to the session the way the host does after the
// extension subscribed: a state_update notification with the appended entry.
func (h *Host) pushUser(hs *hostState, text string) {
	h.t.Helper()
	hs.mu.Lock()
	parent := ""
	if n := len(hs.entries); n > 0 {
		parent, _ = hs.entries[n-1]["id"].(string)
	}
	id := "e" + string(rune('1'+len(hs.entries)))
	entry := userEntry(id, parent, text)
	hs.entries = append(hs.entries, entry)
	count := len(hs.entries)
	hs.mu.Unlock()
	// Make sure the extension has subscribed (first read) before the push.
	session, _ := json.Marshal(map[string]any{"leafId": id, "entriesAppended": []any{entry}, "entryCount": count})
	h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": map[string]any{"session": json.RawMessage(session)}}}})
}

// first returns the first recorded request or fails the test (a red run must
// report a missing request, not panic on an index).
func (f *fakeJev) first(t *testing.T) recordedRequest {
	t.Helper()
	r := f.requests()
	if len(r) == 0 {
		t.Fatal("the extension made no request")
	}
	return r[0]
}

// adoptDynamicTools tells the fake host about tools the extension registered at
// run time (registerTool), which the host would otherwise learn from the register frame.
func (h *Host) adoptDynamicTools() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if c.Method == "registerTool" {
			if name, _ := c.Args["name"].(string); name != "" {
				h.tools[name] = true
			}
		}
	}
}

func sleepMs(n int) { time.Sleep(time.Duration(n) * time.Millisecond) }
