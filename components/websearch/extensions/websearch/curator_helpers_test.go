package websearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fixtures of the curator workflow tests: a host that records notifications, messages and exec
// calls, fake providers, a model registry and a client of the curator server.

type hostNote struct{ message, level string }

type hostCustomMessage struct {
	customType  string
	text        string
	details     map[string]any
	triggerTurn bool
	deliverAs   string
}

type hostExec struct {
	command string
	args    []string
}

// curatorHostFake is a Host that is also a CuratorHost.
type curatorHostFake struct {
	fakeHost
	cmu       sync.Mutex
	notes     []hostNote
	custom    []hostCustomMessage
	execs     []hostExec
	execReply func(command string, args []string) (int, string, error)
}

func (h *curatorHostFake) Notify(message, level string) {
	h.cmu.Lock()
	defer h.cmu.Unlock()
	h.notes = append(h.notes, hostNote{message, level})
}

func (h *curatorHostFake) SendCustomMessage(customType string, content []map[string]any, details map[string]any, triggerTurn bool, deliverAs string) {
	h.cmu.Lock()
	defer h.cmu.Unlock()
	var text strings.Builder
	for _, b := range content {
		if s, ok := b["text"].(string); ok {
			text.WriteString(s)
		}
	}
	h.custom = append(h.custom, hostCustomMessage{customType, text.String(), details, triggerTurn, deliverAs})
}

func (h *curatorHostFake) Exec(command string, args []string) (int, string, error) {
	h.cmu.Lock()
	h.execs = append(h.execs, hostExec{command, args})
	reply := h.execReply
	h.cmu.Unlock()
	if reply != nil {
		return reply(command, args)
	}
	return 0, "", nil
}

func (h *curatorHostFake) notifications() []hostNote {
	h.cmu.Lock()
	defer h.cmu.Unlock()
	return append([]hostNote(nil), h.notes...)
}

func (h *curatorHostFake) messages() []hostCustomMessage {
	h.cmu.Lock()
	defer h.cmu.Unlock()
	return append([]hostCustomMessage(nil), h.custom...)
}

// waitMessage waits for the n-th custom message.
func (h *curatorHostFake) waitMessage(t *testing.T, n int) hostCustomMessage {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := h.messages(); len(msgs) >= n {
			return msgs[n-1]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no message %d, have %+v", n, h.messages())
	return hostCustomMessage{}
}

// waitNote waits for a notification that contains text.
func (h *curatorHostFake) waitNote(t *testing.T, text string) hostNote {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range h.notifications() {
			if strings.Contains(n.message, text) {
				return n
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no notification with %q, have %+v", text, h.notifications())
	return hostNote{}
}

type curatorEnv struct {
	t      *testing.T
	rt     *Runtime
	host   *curatorHostFake
	side   *fakeSide
	reg    *fakeSideRegistry
	opened chan string

	omu     sync.Mutex
	openErr error
}

const curatorSummaryModel = "claude-haiku-4-5"

// newCuratorEnv builds a runtime on an isolated config. The default model registry offers the
// first preferred summary model and answers every completion with "generated summary".
func newCuratorEnv(t *testing.T, config string) *curatorEnv {
	t.Helper()
	extractEnv(t, config)
	ClearResults()
	t.Cleanup(ClearResults)
	host := &curatorHostFake{}
	rt, err := NewRuntime(host)
	noErr(t, err)
	model := scModel("anthropic", curatorSummaryModel)
	reg := &fakeSideRegistry{models: []map[string]any{model}, completeFn: func(context.Context, map[string]any, map[string]any, CompleteOptions) (map[string]any, error) {
		return stopText("generated summary"), nil
	}}
	e := &curatorEnv{t: t, rt: rt, host: host, reg: reg, side: &fakeSide{model: model, reg: reg}, opened: make(chan string, 16)}
	rt.openBrowser = func(u string) error {
		e.omu.Lock()
		err := e.openErr
		e.omu.Unlock()
		e.opened <- u
		return err
	}
	t.Cleanup(func() { rt.CloseCurator("") })
	return e
}

func (e *curatorEnv) failBrowser(err error) {
	e.omu.Lock()
	e.openErr = err
	e.omu.Unlock()
}

// useFakeProvider replaces a provider of the table for the test.
func useFakeProvider(t *testing.T, name string, search func(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error)) {
	t.Helper()
	old := providerTable[name]
	label := name
	if old != nil {
		label = old.label
	}
	providerTable[name] = &providerDef{name: name, label: label, available: func() bool { return true }, search: search}
	t.Cleanup(func() { providerTable[name] = old })
}

func answerWith(answer string, sources ...SearchResult) func(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return func(context.Context, string, SearchOptions) (*SearchResponse, error) {
		return &SearchResponse{Answer: answer, Results: sources}, nil
	}
}

func source(title, u string) SearchResult {
	return SearchResult{Title: title, URL: u, Snippet: title + " snippet"}
}

// toolRun is a running web_search call.
type toolRun struct {
	t       *testing.T
	cancel  context.CancelFunc
	out     chan ToolOutput
	failure chan error
	mu      sync.Mutex
	updates []ToolOutput
}

func (r *toolRun) allUpdates() []ToolOutput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ToolOutput(nil), r.updates...)
}

// curatorURL waits for the first update that names the curator page.
func (r *toolRun) curatorURL() string {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, u := range r.allUpdates() {
			if s, ok := u.Details["curatorUrl"].(string); ok && s != "" {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("the call never named its curator page, updates: %+v", r.allUpdates())
	return ""
}

func (r *toolRun) waitUpdate(pred func(ToolOutput) bool) ToolOutput {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, u := range r.allUpdates() {
			if pred(u) {
				return u
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("no matching update, have %+v", r.allUpdates())
	return ToolOutput{}
}

func (r *toolRun) result() ToolOutput {
	r.t.Helper()
	select {
	case out := <-r.out:
		return out
	case err := <-r.failure:
		r.t.Fatalf("the call failed: %v", err)
	case <-time.After(20 * time.Second):
		r.t.Fatal("the call never finished")
	}
	return ToolOutput{}
}

// runWebSearch starts web_search under tool call id callID with the session's model context.
func (e *curatorEnv) runWebSearch(callID string, params map[string]any) *toolRun {
	return e.runTool("web_search", callID, params, e.side)
}

func (e *curatorEnv) runTool(tool, callID string, params map[string]any, sc SideCallContext) *toolRun {
	e.t.Helper()
	ctx, cancel := context.WithCancel(WithToolCallID(WithSideCallContext(bg(), sc), callID))
	run := &toolRun{t: e.t, cancel: cancel, out: make(chan ToolOutput, 1), failure: make(chan error, 1)}
	e.t.Cleanup(cancel)
	spec := mustTool(e.t, e.rt, tool)
	go func() {
		out, err := spec.Execute(ctx, params, func(u ToolOutput) {
			run.mu.Lock()
			run.updates = append(run.updates, u)
			run.mu.Unlock()
		})
		if err != nil {
			run.failure <- err
			return
		}
		run.out <- out
	}()
	return run
}

// curatorClient talks to a curator page URL.
type curatorClient struct {
	t      *testing.T
	origin string
	token  string
}

func newCuratorClient(t *testing.T, pageURL string) *curatorClient {
	t.Helper()
	u, err := url.Parse(pageURL)
	noErr(t, err)
	return &curatorClient{t: t, origin: u.Scheme + "://" + u.Host, token: u.Query().Get("session")}
}

func (c *curatorClient) post(path string, body map[string]any) (int, map[string]any) {
	c.t.Helper()
	b := map[string]any{"token": c.token}
	for k, v := range body {
		b[k] = v
	}
	return postJSON(c.t, c.origin+path, b)
}

func (c *curatorClient) state() map[string]any {
	c.t.Helper()
	_, body := getJSON(c.t, c.origin+"/state?session="+c.token)
	return body
}

// waitDone waits until the curator's search stream is done.
func (c *curatorClient) waitDone() map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := c.state(); s["done"] == true {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.t.Fatal("the search stream never finished")
	return nil
}

func (c *curatorClient) page() string {
	c.t.Helper()
	resp, err := http.Get(c.origin + "/?session=" + c.token)
	noErr(c.t, err)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	noErr(t, err)
	return string(data)
}

func recvURL(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case u := <-ch:
		return u
	case <-time.After(10 * time.Second):
		t.Fatal("the browser was never opened")
		return ""
	}
}

func noURL(t *testing.T, ch <-chan string) {
	t.Helper()
	select {
	case u := <-ch:
		t.Fatalf("the browser was opened: %s", u)
	case <-time.After(200 * time.Millisecond):
	}
}

var errBrowser = errors.New("spawn xdg-open ENOENT")

func detailString(o ToolOutput, key string) string { s, _ := o.Details[key].(string); return s }

// readReadme reads the Package's README; a module copied alone (the mutation runs) has none.
func readReadme(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Skip("the README is not next to the module")
	}
	return string(data)
}
