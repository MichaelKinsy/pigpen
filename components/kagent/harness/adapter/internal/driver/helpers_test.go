package driver

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
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/config"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
	"github.com/kagent-dev/kagent/go/harness/runtime"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_PIG_MODE"); mode != "" {
		os.Exit(fakePig(mode))
	}
	os.Exit(m.Run())
}

// fakeMCP is a streamable-HTTP MCP server with one tool, kagent_probe, that
// records every tools/call and the headers it arrived with.
type fakeMCP struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []map[string]any
	header []http.Header
	// failCalls makes tools/call answer HTTP 500 after the request was received.
	failCalls bool
	// hold, when set, makes tools/call block until its request context ends; arrived is signalled first.
	hold    bool
	arrived chan struct{}
}

func newFakeMCP(t *testing.T) *fakeMCP { return startFakeMCP(t, false) }

func startFakeMCP(t *testing.T, tls bool) *fakeMCP {
	t.Helper()
	f := &fakeMCP{arrived: make(chan struct{}, 4)}
	server := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	type probeArgs struct {
		Note string `json:"note"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "kagent_probe", Description: "probe"}, func(_ context.Context, _ *mcp.CallToolRequest, args probeArgs) (*mcp.CallToolResult, any, error) {
		f.mu.Lock()
		f.calls = append(f.calls, map[string]any{"note": args.Note})
		f.mu.Unlock()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "probe:" + args.Note}}}, nil, nil
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	handle := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"tools/call"`)) {
			f.mu.Lock()
			f.header = append(f.header, r.Header.Clone())
			fail := f.failCalls
			f.mu.Unlock()
			if fail {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			if f.hold {
				f.arrived <- struct{}{}
				<-r.Context().Done()
				return
			}
		}
		inner.ServeHTTP(w, r)
	})
	if tls {
		f.server = httptest.NewTLSServer(handle)
	} else {
		f.server = httptest.NewServer(handle)
	}
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeMCP) url() string {
	return strings.Replace(f.server.URL, "127.0.0.1", "localhost", 1) + "/mcp"
}

func (f *fakeMCP) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// recorder is an EventSink that keeps every event as a readable string.
type recorder struct {
	mu         sync.Mutex
	events     []string
	calls      []runtime.ToolCall
	results    []runtime.ToolResult
	models     []runtime.ModelCall
	started    []runtime.ToolCall
	finished   []runtime.ToolResult
	onToolCall func(runtime.ToolCall)
	// observe controls whether the sink also implements runtime.ToolExecutionSink.
}

func (r *recorder) add(format string, args ...any) {
	r.mu.Lock()
	r.events = append(r.events, fmt.Sprintf(format, args...))
	r.mu.Unlock()
}

func (r *recorder) SessionStarted(e runtime.SessionStarted) error {
	r.add("session:%s", e.ContinuationID)
	return nil
}
func (r *recorder) TextDelta(e runtime.TextDelta) error { r.add("text:%s", e.Text); return nil }
func (r *recorder) ToolCall(e runtime.ToolCall) error {
	r.mu.Lock()
	r.calls = append(r.calls, e)
	hook := r.onToolCall
	r.mu.Unlock()
	r.add("call:%s:%s", e.ID, e.Name)
	if hook != nil {
		hook(e)
	}
	return nil
}
func (r *recorder) ToolResult(e runtime.ToolResult) error {
	r.mu.Lock()
	r.results = append(r.results, e)
	r.mu.Unlock()
	r.add("result:%s:%s:err=%v:%s", e.ID, e.Name, e.IsError, e.Status)
	return nil
}
func (r *recorder) ModelCall(e runtime.ModelCall) {
	r.mu.Lock()
	r.models = append(r.models, e)
	r.mu.Unlock()
	r.add("model:%s", e.StopReason)
}
func (r *recorder) ToolExecutionStarted(e runtime.ToolCall) {
	r.mu.Lock()
	r.started = append(r.started, e)
	r.mu.Unlock()
}
func (r *recorder) ToolExecutionFinished(e runtime.ToolResult) {
	r.mu.Lock()
	r.finished = append(r.finished, e)
	r.mu.Unlock()
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, e := range r.events {
		if strings.HasPrefix(e, "text:") {
			b.WriteString(strings.TrimPrefix(e, "text:"))
		}
	}
	return b.String()
}

func (r *recorder) has(prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

// harness is one Actor's directories, MCP server and driver.
type harness struct {
	t       *testing.T
	data    string
	pigHome string
	log     string
	marker  string
	child   string
	mcp     *fakeMCP
	cfg     *config.Compiled
	d       *Driver
	extra   []string
}

func compiledFor(t *testing.T, mcpURL string, env map[string]string) *config.Compiled {
	t.Helper()
	raw := fmt.Sprintf(`{"agent":{"name":"agent","instruction":"Be brief.","model":{"model":"mock-model","openai":{"base_url":"http://127.0.0.1:1/v1"}},
	"http_tools":[{"params":{"url":%q,"headers":{"X-Test":"yes"}},"tools":["kagent_probe"],"require_approval":true}]},
	"runtime":{"control_plane_url":"http://ctl:8083"}}`, mcpURL)
	compiled, err := config.Parse([]byte(raw), func(name string) (string, bool) { v, ok := env[name]; return v, ok }, config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func newHarness(t *testing.T, mode string, edit ...func(*Config)) *harness {
	t.Helper()
	root := t.TempDir()
	h := &harness{t: t, data: filepath.Join(root, "data"), pigHome: filepath.Join(root, "run", "pig"),
		log: filepath.Join(root, "fake.log"), marker: filepath.Join(root, "exit-marker"), child: filepath.Join(root, "child.pid"), mcp: newFakeMCP(t)}
	h.cfg = compiledFor(t, h.mcp.url(), map[string]string{"OPENAI_API_KEY": config.CredentialPlaceholder})
	cfg := Config{
		Executable: os.Args[0], Workspace: filepath.Join(h.data, "workspace"), SessionDir: filepath.Join(h.data, "pig", "sessions"),
		PigHome: h.pigHome, AdapterDir: filepath.Join(h.data, "adapter"), Compiled: h.cfg,
		PassEnv: []string{"GORACE", "FAKE_PIG_MODE", "FAKE_PIG_LOG", "FAKE_PIG_EXIT_MARKER", "FAKE_PIG_CHILD_PID"},
		Tools:   mustCaller(t, HeaderValues(h.cfg), ""), Grace: 2 * time.Second, ReapTimeout: 2 * time.Second,
	}
	for _, e := range edit {
		e(&cfg)
	}
	cfg.Environment = h.environment(mode)
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.d = d
	return h
}

func (h *harness) environment(mode string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=/var/lib/actor", "TZ=UTC", "SSL_CERT_FILE=/run/kagent/egress/trust-bundle.pem", "HTTPS_PROXY=http://proxy:3128",
		"KAGENT_BOOTSTRAP_JSON=SECRET-DESCRIPTOR", "OPENAI_API_KEY=outer-value", "SOMETHING_ELSE=nope",
		"GORACE=atexit_sleep_ms=0", "FAKE_PIG_MODE=" + mode, "FAKE_PIG_LOG=" + h.log, "FAKE_PIG_EXIT_MARKER=" + h.marker, "FAKE_PIG_CHILD_PID=" + h.child,
	}
	return append(env, h.extra...)
}

// setMode changes what the fake pig does from the next spawn on.
func (h *harness) setMode(mode string) { h.d.cfg.Environment = h.environment(mode) }

// restart builds a new driver over the same directories, as a restarted Actor would.
func (h *harness) restart(mode string) *Driver {
	cfg := h.d.cfg
	cfg.Environment = h.environment(mode)
	d, err := New(cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	h.d = d
	return d
}

func (h *harness) turn(prompt string) runtime.Turn {
	return runtime.Turn{TaskID: "task-1", ContextID: "ctx-1", Prompt: prompt}
}

func (h *harness) logs(kind string) []map[string]any {
	h.t.Helper()
	data, err := os.ReadFile(h.log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		h.t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) == nil && entry["kind"] == kind {
			out = append(out, entry)
		}
	}
	return out
}

func (h *harness) sessionFile() string {
	matches, _ := filepath.Glob(filepath.Join(h.d.cfg.SessionDir, "*.jsonl"))
	if len(matches) != 1 {
		h.t.Fatalf("session files = %v", matches)
	}
	return matches[0]
}

func (h *harness) sessionRows() []map[string]any {
	data, err := os.ReadFile(h.sessionFile())
	if err != nil {
		h.t.Fatal(err)
	}
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var row map[string]any
		if json.Unmarshal(line, &row) == nil {
			rows = append(rows, row)
		}
	}
	return rows
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// assertReaped checks that every fake pig process recorded in the log is gone.
func (h *harness) assertReaped() {
	h.t.Helper()
	for _, start := range h.logs("start") {
		pid := int(start["pid"].(float64))
		eventually(h.t, fmt.Sprintf("pig %d to be gone", pid), func() bool { return !alive(pid) })
	}
}

func (h *harness) approvalRequest(out runtime.Outcome) *runtime.ApprovalRequest {
	h.t.Helper()
	if out.Pending == nil {
		h.t.Fatalf("no pending turn: %+v", out)
	}
	request, ok := out.Pending.Request().(*runtime.ApprovalRequest)
	if !ok {
		h.t.Fatalf("request = %T", out.Pending.Request())
	}
	return request
}

func mustCaller(t testing.TB, values map[string]string, caBundle string) *MCPCaller {
	t.Helper()
	caller, err := NewMCPCaller(values, caBundle)
	if err != nil {
		t.Fatal(err)
	}
	return caller
}

func outcomeOf(r *mcp.CallToolResult) journal.Outcome {
	out, _ := OutcomeFromMCP(r)
	return out
}
