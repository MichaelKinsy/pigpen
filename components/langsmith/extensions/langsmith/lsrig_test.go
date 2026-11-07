package langsmith_test

// Test rig shared by the twins and the hook tests: a loopback LangSmith endpoint that records the requests (the role of
// upstream's mockClient fetch spy), the fake PiG host with the answers a Pi ctx gives synchronously in the original
// (ctx.model, ctx.getContextUsage(), ctx.sessionManager.getSessionId()), upstream's replay driver and tree builder
// (test/utils/replay.ts, test/utils/tree.ts) and Jest's asymmetric matchers.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	langsmith "github.com/MichaelKinsy/pigpen/langsmith"
)

// lsCall is one request the extension's client sent.
type lsCall struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type mockLS struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []lsCall
}

func newMockLS(t *testing.T) *mockLS {
	t.Helper()
	m := &mockLS{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.calls = append(m.calls, lsCall{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockLS) Calls() []lsCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]lsCall(nil), m.calls...)
}

// rig is the extension under a fake host plus the loopback endpoint.
type rig struct {
	t  *testing.T
	h  *Host
	ls *mockLS
	te *langsmith.TestExtension

	mu        sync.Mutex
	model     map[string]any
	usage     map[string]any
	sessionID string
	noSession bool
}

type rigOptions struct {
	Env       map[string]string // on top of TRACE_TO_LANGSMITH=true
	PiVersion string            // default "1.5.0"
	Cwd       string            // the session's working directory; default a temporary one
	SessionID string
	NoSession bool // getSessionID fails (a host that gives no session id)
	Model     map[string]any
	Mode      string
	HasUI     *bool
	// HomeConfig and CwdConfig are the contents of ~/.pi/langsmith.json and <cwd>/.pi/langsmith.json (the extension's
	// config sources, not the session's working directory).
	HomeConfig string
	CwdConfig  string
}

var defaultModel = map[string]any{"id": "claude-opus-4-8", "name": "Claude Opus 4.8", "provider": "anthropic"}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	// Nothing of the machine's LangSmith setup may reach the extension.
	t.Setenv("HOME", t.TempDir())
	for _, k := range []string{"LANGSMITH_API_KEY", "LANGCHAIN_API_KEY", "LANGSMITH_ENDPOINT", "LANGCHAIN_ENDPOINT", "LANGSMITH_PROJECT", "LANGCHAIN_PROJECT", "LANGSMITH_RUNS_ENDPOINTS", "LANGSMITH_CONFIG_FILE", "LANGSMITH_PROFILE", "LANGSMITH_TRACING_MODE", "LANGSMITH_OTEL_ENABLED", "OTEL_ENABLED"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	ls := newMockLS(t)
	env := map[string]string{"TRACE_TO_LANGSMITH": "true"}
	for k, v := range o.Env {
		env[k] = v
	}
	version := o.PiVersion
	if version == "" {
		version = "1.5.0"
	}
	home, cfgCwd := t.TempDir(), t.TempDir()
	for dir, content := range map[string]string{home: o.HomeConfig, cfgCwd: o.CwdConfig} {
		if content == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Join(dir, ".pi"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".pi", "langsmith.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	te, err := langsmith.NewTestExtension(langsmith.TestOptions{Env: env, Home: home, Cwd: cfgCwd, ServerURL: ls.srv.URL, Direct: true, PiVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(te.Close)
	r := &rig{t: t, ls: ls, te: te, model: o.Model, sessionID: o.SessionID, noSession: o.NoSession}
	if r.model == nil {
		r.model = defaultModel
	}
	if r.sessionID == "" {
		r.sessionID = "sess_test"
	}
	r.h = StartHost(t, te.Ext, HostOptions{Mode: o.Mode, HasUI: o.HasUI, Cwd: o.Cwd, OnCall: r.answer})
	return r
}

func (r *rig) answer(method string, _ map[string]any) (map[string]any, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch method {
	case "getModelInfo":
		return r.model, ""
	case "getContextUsage":
		return r.usage, ""
	case "getSessionID":
		if r.noSession {
			return nil, "no session"
		}
		return map[string]any{"sessionId": r.sessionID}, ""
	}
	return map[string]any{}, ""
}

func (r *rig) setModel(m map[string]any) {
	r.mu.Lock()
	r.model = m
	r.mu.Unlock()
}

func (r *rig) setUsage(u map[string]any) {
	r.mu.Lock()
	r.usage = u
	r.mu.Unlock()
}

// flush is client.awaitPendingTraceBatches().
func (r *rig) flush() {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.te.Flush(ctx); err != nil {
		r.t.Fatalf("awaitPendingTraceBatches: %v", err)
	}
}

// statuses are the ui.setStatus calls, in order, as "key=text" (a cleared status has no text).
func (r *rig) statuses() []string {
	var out []string
	for _, c := range r.h.CallsTo("ui.setStatus") {
		text, _ := c.Args["text"].(string)
		out = append(out, fmt.Sprintf("%v=%s", c.Args["key"], text))
	}
	return out
}

func (r *rig) notifies() [][2]string {
	var out [][2]string
	for _, c := range r.h.CallsTo("ui.notify") {
		out = append(out, [2]string{fmt.Sprint(c.Args["message"]), fmt.Sprint(c.Args["level"])})
	}
	return out
}

// ── replay (test/utils/replay.ts) ───────────────────────────────────────────────────────────────────────────────

type recordedEvent struct {
	Event string
	Arg   map[string]any
	Ctx   map[string]any
}

func readRecording(t *testing.T, name string) []recordedEvent {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "port", "oracle", "test", "recordings", name))
	if err != nil {
		t.Fatal(err)
	}
	return parseRecording(t, string(data))
}

func parseRecording(t *testing.T, text string) []recordedEvent {
	t.Helper()
	var out []recordedEvent
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		var rec [4]json.RawMessage
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("recording line: %v", err)
		}
		var ev recordedEvent
		_ = json.Unmarshal(rec[1], &ev.Event)
		_ = json.Unmarshal(rec[2], &ev.Arg)
		_ = json.Unmarshal(rec[3], &ev.Ctx)
		out = append(out, ev)
	}
	return out
}

// replay fires every recorded event at the extension, with the recorded ctx (model and context usage) in force.
func (r *rig) replay(events []recordedEvent) {
	r.t.Helper()
	for _, ev := range events {
		if m, ok := ev.Ctx["model"].(map[string]any); ok {
			r.setModel(m)
		} else {
			r.setModel(map[string]any{})
		}
		if u, ok := ev.Ctx["getContextUsage"].(map[string]any); ok {
			r.setUsage(u)
		} else {
			r.setUsage(nil)
		}
		r.h.Fire(ev.Event, ev.Arg)
	}
}

func recordingCwd(events []recordedEvent) string {
	if len(events) > 0 {
		if cwd, ok := events[0].Ctx["cwd"].(string); ok {
			return cwd
		}
	}
	return ""
}

// ── tree (test/utils/tree.ts) ───────────────────────────────────────────────────────────────────────────────────

type tree struct {
	Nodes []string
	Edges [][2]string
	Data  map[string]map[string]any
}

func assumedTree(t *testing.T, calls []lsCall) tree {
	t.Helper()
	nodeMap := map[string]map[string]any{}
	var idMap []string
	upsert := func(id string) int {
		for i, x := range idMap {
			if x == id {
				return i
			}
		}
		idMap = append(idMap, id)
		return len(idMap) - 1
	}
	var rawEdges [][2]string
	for _, c := range calls {
		var body map[string]any
		if err := json.Unmarshal(c.Body, &body); err != nil {
			// A body that does not start with `{` is skipped, as upstream skips it.
			continue
		}
		path := strings.TrimPrefix(c.Path, "/api/v1")
		switch {
		case c.Method == "POST" && path == "/runs":
			id, _ := body["id"].(string)
			upsert(id)
			merged := map[string]any{}
			for k, v := range nodeMap[id] {
				merged[k] = v
			}
			for k, v := range body {
				merged[k] = v
			}
			nodeMap[id] = merged
			if p, _ := merged["parent_run_id"].(string); p != "" {
				rawEdges = append(rawEdges, [2]string{p, id})
			}
		case c.Method == "PATCH" && strings.HasPrefix(path, "/runs/"):
			id := strings.TrimPrefix(path, "/runs/")
			upsert(id)
			merged := map[string]any{}
			for k, v := range nodeMap[id] {
				merged[k] = v
			}
			for k, v := range body {
				merged[k] = v
			}
			nodeMap[id] = merged
		}
	}
	label := func(id string) string {
		idx := upsert(id)
		name, _ := nodeMap[id]["name"].(string)
		return fmt.Sprintf("%s:%d", name, idx)
	}
	out := tree{Data: map[string]map[string]any{}}
	for _, id := range idMap {
		out.Nodes = append(out.Nodes, label(id))
	}
	for _, e := range rawEdges {
		out.Edges = append(out.Edges, [2]string{label(e[0]), label(e[1])})
	}
	for id, v := range nodeMap {
		out.Data[label(id)] = v
	}
	return out
}

// expectedTree is one entry of testdata/ext_expected_trees.json: upstream's asTree blocks, evaluated by node.
type expectedTree struct {
	Nodes []string       `json:"nodes"`
	Edges [][2]string    `json:"edges"`
	Data  map[string]any `json:"data"`
}

func loadExpectedTrees(t *testing.T) map[string]expectedTree {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "ext_expected_trees.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]expectedTree
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// requireTree is `expect(tree.nodes).toEqual(...)`, `.edges`, and `expect(tree.data).toMatchObject(...)`.
func requireTree(t *testing.T, got tree, want expectedTree) {
	t.Helper()
	if !reflect.DeepEqual(nonNil(got.Nodes), nonNil(want.Nodes)) {
		t.Fatalf("nodes\n got %v\nwant %v", got.Nodes, want.Nodes)
	}
	if fmt.Sprint(got.Edges) != fmt.Sprint(want.Edges) {
		t.Fatalf("edges\n got %v\nwant %v", got.Edges, want.Edges)
	}
	actualData := map[string]any{}
	for k, v := range got.Data {
		actualData[k] = v
	}
	if err := toMatchObject(actualData, want.Data, "data"); err != nil {
		t.Fatal(err)
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ── Jest matchers ───────────────────────────────────────────────────────────────────────────────────────────────

// asymmetric returns the matcher of a {"$sc"|"$oc"|"$ac": ...} marker.
func asymmetric(v any) (kind string, arg any, ok bool) {
	m, isMap := v.(map[string]any)
	if !isMap || len(m) != 1 {
		return "", nil, false
	}
	for _, k := range []string{"$sc", "$oc", "$ac"} {
		if a, found := m[k]; found {
			return k, a, true
		}
	}
	return "", nil, false
}

// equals is Jest's recursive equality with asymmetric matchers; undefined properties do not exist in decoded JSON.
func equals(actual, expected any) bool {
	if kind, arg, ok := asymmetric(expected); ok {
		return matchAsymmetric(actual, kind, arg)
	}
	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok || len(a) != len(e) {
			return false
		}
		for k, ev := range e {
			av, found := a[k]
			if !found || !equals(av, ev) {
				return false
			}
		}
		return true
	case []any:
		a, ok := actual.([]any)
		if !ok || len(a) != len(e) {
			return false
		}
		for i := range e {
			if !equals(a[i], e[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(actual, expected)
}

func matchAsymmetric(actual any, kind string, arg any) bool {
	switch kind {
	case "$sc":
		s, ok := actual.(string)
		return ok && strings.Contains(s, arg.(string))
	case "$oc":
		a, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, ev := range arg.(map[string]any) {
			av, found := a[k]
			if !found || !equals(av, ev) {
				return false
			}
		}
		return true
	case "$ac":
		a, ok := actual.([]any)
		if !ok {
			return false
		}
		for _, ev := range arg.([]any) {
			found := false
			for _, av := range a {
				if equals(av, ev) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	return false
}

// toMatchObject is Jest's subset match: objects need the expected keys, arrays the same length, scalars equality.
func toMatchObject(actual, expected any, path string) error {
	if kind, arg, ok := asymmetric(expected); ok {
		if !matchAsymmetric(actual, kind, arg) {
			return fmt.Errorf("%s: %s does not match %s %v", path, brief(actual), kind, brief(arg))
		}
		return nil
	}
	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want an object, got %s", path, brief(actual))
		}
		keys := make([]string, 0, len(e))
		for k := range e {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			av, found := a[k]
			if !found {
				return fmt.Errorf("%s: missing key %q (have %s)", path, k, brief(a))
			}
			if err := toMatchObject(av, e[k], path+"."+k); err != nil {
				return err
			}
		}
		return nil
	case []any:
		a, ok := actual.([]any)
		if !ok || len(a) != len(e) {
			return fmt.Errorf("%s: want %d elements, got %s", path, len(e), brief(actual))
		}
		for i := range e {
			if err := toMatchObject(a[i], e[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("%s: got %s, want %s", path, brief(actual), brief(expected))
	}
	return nil
}

func brief(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 300 {
		b = append(b[:300], "..."...)
	}
	return string(bytes.TrimSpace(b))
}

// jsonOf decodes a JSON text into the shapes a host delivers.
func jsonOf(t testing.TB, text string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return m
}

func jsonAny(t testing.TB, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return v
}
