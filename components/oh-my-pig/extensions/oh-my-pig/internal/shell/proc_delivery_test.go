package shell

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// Fake host harness mirroring internal/ask/fakehost_test.go so async job
// delivery via sdk.Context.SendMessage can be verified end-to-end.

type shellHostCall struct {
	Method string
	Args   map[string]any
}

type shellWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
}

type shellFakeHost struct {
	t          *testing.T
	nc         net.Conn
	cwd        string
	sessionID  string
	runDone    chan error
	writeMu    sync.Mutex
	mu         sync.Mutex
	calls      []shellHostCall
	callSignal chan shellHostCall
	pending    map[string]chan json.RawMessage
	failures   map[string]string
	nextID     int
	handlers   map[string]int
	onSelect   func(title string, options []string) (any, bool)
}

func startShellFakeHost(t *testing.T, ext *sdk.Extension, cwd, sessionID string) *shellFakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &shellFakeHost{
		t:          t,
		nc:         hostSide,
		cwd:        cwd,
		sessionID:  sessionID,
		runDone:    make(chan error, 1),
		callSignal: make(chan shellHostCall, 32),
		pending:    map[string]chan json.RawMessage{},
		failures:   map[string]string{},
		handlers:   map[string]int{},
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()
	first := h.read()
	for first.Type == "call" && first.Call != nil {
		h.answer(first)
		first = h.read()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register, got %+v", first)
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = hd.HandlerID
	}
	state, _ := json.Marshal(map[string]any{"hasUI": true})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": sessionID,
		"cwd":          cwd,
		"mode":         "tui",
		"width":        100,
		"height":       40,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *shellFakeHost) read() shellWireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var f shellWireFrame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("decode frame %s: %v", data, err)
	}
	return f
}

func (h *shellFakeHost) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(data)
}

func (h *shellFakeHost) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, data); err != nil {
			return
		}
		var f shellWireFrame
		if err := json.Unmarshal(data, &f); err != nil {
			return
		}
		switch f.Type {
		case "call":
			if f.Call != nil {
				h.answer(f)
			}
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			if f.Response != nil && f.Response.Error != nil {
				h.failures[f.ID] = f.Response.Error.Message
			}
			h.mu.Unlock()
			if ch != nil {
				var res json.RawMessage
				if f.Response != nil {
					res = f.Response.Result
				}
				ch <- res
			}
		}
	}
}

func (h *shellFakeHost) answer(f shellWireFrame) {
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	c := shellHostCall{Method: f.Call.Method, Args: args}
	h.mu.Lock()
	h.calls = append(h.calls, c)
	h.mu.Unlock()
	select {
	case h.callSignal <- c:
	default:
	}

	go func() {
		var result any = map[string]any{}
		switch f.Call.Method {
		case "getSessionID", "session_get_id", "get_session_id":
			result = map[string]any{"sessionId": h.sessionID, "session_id": h.sessionID}
		case "getSessionFile", "session_get_file", "get_session_file":
			result = map[string]any{"sessionFile": filepath.Join(h.cwd, "session.jsonl")}
		case "ui.select":
			h.mu.Lock()
			cb := h.onSelect
			h.mu.Unlock()
			if cb != nil {
				title, _ := args["title"].(string)
				rawOpts, _ := args["options"].([]any)
				opts := make([]string, 0, len(rawOpts))
				for _, o := range rawOpts {
					if s, ok := o.(string); ok {
						opts = append(opts, s)
					}
				}
				val, ok := cb(title, opts)
				if ok {
					result = map[string]any{"ok": true, "selected": val}
				} else {
					result = map[string]any{"ok": false}
				}
			} else {
				result = map[string]any{"ok": false}
			}
		}
		h.write(map[string]any{
			"type": "call_result",
			"id":   f.ID,
			"call_result": map[string]any{
				"result": result,
			},
		})
	}()
}

func (h *shellFakeHost) emitEvent(eventType string, event map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	handlerID, ok := h.handlers[eventType]
	if !ok {
		h.t.Fatalf("no registered handler for event %q (have %v)", eventType, h.handlers)
	}
	evJSON, _ := json.Marshal(event)
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("ev-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	h.write(map[string]any{
		"type": "request",
		"id":   id,
		"request": map[string]any{
			"method":     "event",
			"event":      eventType,
			"handler_id": handlerID,
			"args":       json.RawMessage(evJSON),
		},
	})

	select {
	case res := <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		h.mu.Unlock()
		return res, errMsg
	case <-time.After(30 * time.Second):
		h.t.Fatalf("timed out waiting for event %s", eventType)
		return nil, ""
	}
}

func (h *shellFakeHost) callTool(name string, params map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	argv, _ := json.Marshal(params)
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	h.write(map[string]any{
		"type": "request",
		"id":   id,
		"request": map[string]any{
			"method":       "tool_call",
			"tool":         name,
			"tool_call_id": "call-" + id,
			"args":         json.RawMessage(argv),
		},
	})

	select {
	case res := <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		h.mu.Unlock()
		return res, errMsg
	case <-time.After(30 * time.Second):
		h.t.Fatalf("timed out waiting for tool_call %s", name)
		return nil, ""
	}
}

func (h *shellFakeHost) waitForCall(method string, timeout time.Duration) (shellHostCall, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	// Check already-recorded calls first.
	h.mu.Lock()
	for _, c := range h.calls {
		if c.Method == method {
			h.mu.Unlock()
			return c, true
		}
	}
	h.mu.Unlock()
	for {
		select {
		case c := <-h.callSignal:
			if c.Method == method {
				return c, true
			}
		case <-deadline.C:
			return shellHostCall{}, false
		}
	}
}

func (h *shellFakeHost) stop() {
	h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case <-h.runDone:
	case <-time.After(3 * time.Second):
	}
	_ = h.nc.Close()
}

// upstream: agent-session-async-delivery.test.ts: "owned job completions inject async-result follow-up turns"
func TestFakeHostAsyncJobCompletionAndDelivery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX async shell test")
	}
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
	})

	dir := t.TempDir()
	rt := NewRuntime(filepath.Join(dir, "artifacts"))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rt.Jobs.Dispose(ctx)
		_ = rt.Services.Dispose(ctx)
		rt.DisposeSessions()
	})

	ext := sdk.New("test-shell-delivery")
	jobs.EnsureDeliveryListener()
	tool := NewBashTool(DefaultConfig(), rt)
	ext.RegisterTool(tool.Definition())

	h := startShellFakeHost(t, ext, dir, "sess-async-1")
	pidFile := filepath.Join(dir, "bg.pid")
	cmd := fmt.Sprintf("echo $$ > %s; sleep 0.08; printf 'hello-from-bg'", singleQuotePosix(pidFile))

	raw, errMsg := h.callTool("bash", map[string]any{
		"command": cmd,
		"cwd":     dir,
		"async":   true,
	})
	if errMsg != "" {
		t.Fatalf("tool call failed: %s", errMsg)
	}
	var parsed struct {
		Content string         `json:"content"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal tool result %s: %v", raw, err)
	}
	asyncInfo, _ := parsed.Details["async"].(map[string]any)
	jobID, _ := asyncInfo["jobId"].(string)
	if !strings.HasPrefix(jobID, "bg_") {
		t.Fatalf("expected bg_N jobId, got %v", parsed.Details)
	}

	// Verify the host receives a sendMessage call with customType=async-result,
	// triggerTurn=true, deliverAs=followUp, and omp's <system-notice> body.
	call, ok := h.waitForCall("sendMessage", 30*time.Second)
	if !ok {
		t.Fatalf("timed out waiting for sendMessage delivery")
	}
	msgMap, _ := call.Args["message"].(map[string]any)
	optsMap, _ := call.Args["options"].(map[string]any)
	customType, _ := msgMap["customType"].(string)
	if customType != jobs.AsyncResultMessageType {
		t.Fatalf("expected customType=%q, got %q (args=%v)", jobs.AsyncResultMessageType, customType, call.Args)
	}
	if optsMap["triggerTurn"] != true || optsMap["deliverAs"] != "followUp" {
		t.Fatalf("expected triggerTurn=true deliverAs=followUp, got %v", optsMap)
	}
	content, _ := msgMap["content"].(string)
	if !strings.Contains(content, "<system-notice>") ||
		!strings.Contains(content, "Background job "+jobID) ||
		!strings.Contains(content, "hello-from-bg") ||
		!strings.Contains(content, "</system-notice>") {
		t.Fatalf("unexpected async-result content: %q", content)
	}

	// Ensure no process left behind.
	if rawPID, rErr := os.ReadFile(pidFile); rErr == nil {
		if pid, pErr := strconv.Atoi(strings.TrimSpace(string(rawPID))); pErr == nil && pid > 0 {
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) && isProcessAlive(pid) {
				time.Sleep(20 * time.Millisecond)
			}
			if isProcessAlive(pid) {
				forceKillPID(pid)
				t.Fatalf("async process %d still alive", pid)
			}
		}
	}
}

// upstream: tools/proc-service.test.ts: "lists owned jobs, preserves delivery on read, and kills via /kill"
func TestProcProtocolLifecycleAndKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX proc:// test")
	}
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	urls.ResetForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
		urls.ResetForTests()
	})

	tool, dir := newTestTool(t, nil)
	handler := NewProcProtocolHandler(tool.runtime)
	if err := urls.Register(handler); err != nil {
		t.Fatalf("urls.Register(proc): %v", err)
	}
	urlCtx := urls.Context{Cwd: dir}

	// Empty listing.
	emptyRes, err := urls.Resolve(urlCtx, "proc://")
	if err != nil {
		t.Fatalf("resolve proc://: %v", err)
	}
	if emptyRes.Content != "No background jobs or services." {
		t.Fatalf("unexpected empty listing: %q", emptyRes.Content)
	}

	// Start a long-running async bash job.
	pidFile := filepath.Join(dir, "job.pid")
	cmd := fmt.Sprintf("echo $$ > %s; echo job-running; sleep 30", singleQuotePosix(pidFile))
	res, err := runTool(t, tool, map[string]any{
		"command": cmd,
		"cwd":     dir,
		"async":   true,
		"timeout": 0,
	})
	if err != nil {
		t.Fatalf("start async job: %v", err)
	}
	jobID := res.Details.(map[string]any)["async"].(map[string]any)["jobId"].(string)

	// Wait until pid file appears.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	// Listing shows the job.
	listRes, err := urls.Resolve(urlCtx, "proc://")
	if err != nil {
		t.Fatalf("resolve proc://: %v", err)
	}
	if !strings.Contains(listRes.Content, jobID+" [bash] running") {
		t.Fatalf("expected %s in listing, got %q", jobID, listRes.Content)
	}

	// Single job read.
	singleRes, err := urls.Resolve(urlCtx, "proc://"+jobID)
	if err != nil {
		t.Fatalf("resolve proc://%s: %v", jobID, err)
	}
	if !strings.Contains(singleRes.Content, jobID+" [bash] — running") {
		t.Fatalf("unexpected single job content: %q", singleRes.Content)
	}

	// Reading /kill refuses with write-only guidance without stopping the job.
	if _, err := urls.Resolve(urlCtx, "proc://"+jobID+"/kill"); err == nil || !strings.Contains(err.Error(), "write-only") {
		t.Fatalf("expected write-only error reading /kill, got %v", err)
	}

	// Writing stdin or /mode to a bash job fails with services-only error.
	if _, err := urls.Write(urlCtx, "proc://"+jobID, "hello"); err == nil || !strings.Contains(err.Error(), "in-process job") {
		t.Fatalf("expected services-only error on job stdin, got %v", err)
	}
	if _, err := urls.Write(urlCtx, "proc://"+jobID+"/mode", "persist"); err == nil || !strings.Contains(err.Error(), "in-process job") {
		t.Fatalf("expected services-only error on job mode, got %v", err)
	}

	// Reading /streams on a bash job returns empty-streams message.
	streamsRes, err := urls.Resolve(urlCtx, "proc://"+jobID+"/streams")
	if err != nil {
		t.Fatalf("resolve streams: %v", err)
	}
	if !strings.Contains(streamsRes.Content, "Read proc://"+jobID) {
		t.Fatalf("unexpected streams output: %q", streamsRes.Content)
	}

	// Kill via proc://<id>/kill.
	killRes, err := urls.Write(urlCtx, "proc://"+jobID+"/kill", "")
	if err != nil {
		t.Fatalf("write proc://%s/kill: %v", jobID, err)
	}
	if !strings.Contains(killRes.Text, "Cancelled background job "+jobID) {
		t.Fatalf("unexpected kill text: %q", killRes.Text)
	}

	// Verify OS process is gone.
	rawPID, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if pid > 0 {
		waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _, _ = tool.runtime.Jobs.Wait(waitCtx, jobID)
		waitUntil := time.Now().Add(30 * time.Second)
		for time.Now().Before(waitUntil) && isProcessAlive(pid) {
			time.Sleep(20 * time.Millisecond)
		}
		if isProcessAlive(pid) {
			forceKillPID(pid)
			t.Fatalf("killed job process %d still alive", pid)
		}
	}
}

// upstream: tools/proc-service.test.ts: service ready, stdin, mode, and proc://<name>/kill
func TestProcServiceReadyStdinModeAndKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX service test")
	}
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	urls.ResetForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
		urls.ResetForTests()
	})

	tool, dir := newTestTool(t, nil)
	handler := NewProcProtocolHandler(tool.runtime)
	if err := urls.Register(handler); err != nil {
		t.Fatalf("urls.Register(proc): %v", err)
	}
	urlCtx := urls.Context{Cwd: dir}

	// Start a named service that announces READY, then echoes stdin lines.
	res, err := runTool(t, tool, map[string]any{
		"name":    "echo-svc",
		"command": "echo READY; while IFS= read -r line; do echo \"GOT:$line\"; done",
		"cwd":     dir,
		"pty":     false,
		"ready": map[string]any{
			"log":     "READY",
			"timeout": 20,
		},
	})
	if err != nil {
		t.Fatalf("start service: %v", err)
	}
	if !strings.Contains(res.Content, "echo-svc: ready") || !strings.Contains(res.Content, "ready") {
		t.Fatalf("unexpected service start content: %q", res.Content)
	}
	snap, ok := tool.runtime.Services.Get("echo-svc")
	if !ok || snap.PID <= 0 {
		t.Fatalf("expected service snapshot with pid, got %+v", snap)
	}

	// Send stdin through proc://echo-svc.
	stdinRes, err := urls.Write(urlCtx, "proc://echo-svc", "ping-123")
	if err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if !strings.Contains(stdinRes.Text, "Sent input to echo-svc") {
		t.Fatalf("unexpected stdin result: %q", stdinRes.Text)
	}

	// Poll proc://echo-svc until GOT:ping-123 appears in output.
	deadline := time.Now().Add(30 * time.Second)
	var readContent string
	for time.Now().Before(deadline) {
		r, rErr := urls.Resolve(urlCtx, "proc://echo-svc")
		if rErr == nil && strings.Contains(r.Content, "GOT:ping-123") {
			readContent = r.Content
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(readContent, "GOT:ping-123") {
		t.Fatalf("expected service output to echo stdin, got %q", readContent)
	}

	// Mode "session" succeeds; unsupported modes persist/detached fail honestly.
	modeRes, err := urls.Write(urlCtx, "proc://echo-svc/mode", "session")
	if err != nil {
		t.Fatalf("write mode session: %v", err)
	}
	if !strings.Contains(modeRes.Text, "mode=session") {
		t.Fatalf("unexpected mode result: %q", modeRes.Text)
	}
	if _, err := urls.Write(urlCtx, "proc://echo-svc/mode", "persist"); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected unsupported mode error for persist, got %v", err)
	}

	// Kill service through proc://echo-svc/kill and confirm process exits.
	stopRes, err := urls.Write(urlCtx, "proc://echo-svc/kill", "")
	if err != nil {
		t.Fatalf("stop service via proc://: %v", err)
	}
	if !strings.Contains(stopRes.Text, "Stopped echo-svc") {
		t.Fatalf("unexpected stop result: %q", stopRes.Text)
	}
	waitUntil := time.Now().Add(30 * time.Second)
	for time.Now().Before(waitUntil) && isProcessAlive(snap.PID) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(snap.PID) {
		forceKillPID(snap.PID)
		t.Fatalf("service process %d still alive after proc:// kill", snap.PID)
	}
}

func TestBashServiceReadyPort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX port readiness test")
	}
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
	})

	// Pick an ephemeral free port, close it, and have the service bind it or
	// bind a listener directly and verify port readiness succeeds.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	tool, dir := newTestTool(t, nil)
	res, err := runTool(t, tool, map[string]any{
		"name":    "port-svc",
		"command": "echo UP; sleep 30",
		"cwd":     dir,
		"pty":     false,
		"ready": map[string]any{
			"log":     "UP",
			"port":    port,
			"timeout": 20,
		},
	})
	if err != nil {
		t.Fatalf("start port service: %v", err)
	}
	if !strings.Contains(res.Content, "port-svc: ready") || !strings.Contains(res.Content, "ready") {
		t.Fatalf("expected ready service, got %q", res.Content)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = tool.runtime.Services.Stop(stopCtx, "port-svc")
}

// upstream: async-notify.test.ts: "reports each matching line whole" + bash notify integration
func TestBashNotifyRegexDelivery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX notify test")
	}
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
	})

	notifyCh := make(chan string, 4)
	jobs.SetDeliverySink(func(_ string, customType, content string, _ bool, _ sdk.SendMessageOptions) error {
		if customType == jobs.AsyncNotifyMessageType {
			notifyCh <- content
		}
		return nil
	})

	tool, dir := newTestTool(t, nil)
	res, err := runTool(t, tool, map[string]any{
		"command": "echo 'ok'; echo 'ERROR boom'; sleep 0.4; echo 'done'",
		"cwd":     dir,
		"notify":  "ERROR",
	})
	if err != nil {
		t.Fatalf("notify bash call: %v", err)
	}
	if !strings.Contains(res.Content, "Output lines matching /ERROR/ arrive as follow-ups while it runs.") {
		t.Fatalf("expected notify start notice, got %q", res.Content)
	}

	select {
	case msg := <-notifyCh:
		if !strings.Contains(msg, "<system-notice>") ||
			!strings.Contains(msg, "Its output matched /ERROR/:") ||
			!strings.Contains(msg, "ERROR boom") {
			t.Fatalf("unexpected notify message: %q", msg)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for async-notify message")
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tool.runtime.Jobs.WaitAll(waitCtx)
}

func TestBashSpillWriterHook(t *testing.T) {
	var capturedName string
	var capturedData []byte
	prev := SpillWriter
	SpillWriter = func(_ sdk.Context, name string, data []byte) (string, error) {
		capturedName = name
		capturedData = append([]byte(nil), data...)
		return "local://" + name, nil
	}
	t.Cleanup(func() { SpillWriter = prev })

	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.SpillThreshold = 1024
		cfg.HeadBytes = 256
		cfg.MaxColumns = 0
	})

	res, err := runTool(t, tool, map[string]any{
		"command": "i=1; while [ $i -le 120 ]; do printf 'row-%03d-0123456789abcdef\\n' $i; i=$((i+1)); done",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedName != "0.log" {
		t.Fatalf("expected SpillWriter name 0.log, got %q", capturedName)
	}
	if !strings.Contains(string(capturedData), "row-060-") {
		t.Fatalf("expected full output in SpillWriter data")
	}
	if !strings.Contains(res.Content, "Read local://0.log for full output") {
		t.Fatalf("expected local://0.log notice, got %q", res.Content)
	}
}

func TestBashCommandPreambleAndDirectCommandHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX preamble test")
	}

	prevPreamble := CommandPreamble
	prevDirect := DirectCommand
	prevNames := CommandNames
	t.Cleanup(func() {
		CommandPreamble = prevPreamble
		DirectCommand = prevDirect
		CommandNames = prevNames
	})

	// Track release calls for sync, async, and service runs.
	var relMu sync.Mutex
	releases := 0
	releaseCh := make(chan struct{}, 8)
	CommandPreamble = func(_ sdk.Context) (string, func()) {
		return "echo SHOULD_NOT_APPEAR\n__tc_var=ephemeral_cred_token_999\nmy_tool_cmd() { printf 'TOOL:%s' \"$1\"; }\n", func() {
			relMu.Lock()
			releases++
			relMu.Unlock()
			releaseCh <- struct{}{}
		}
	}

	tool, dir := newTestTool(t, nil)

	// 1) Sync call: preamble runs in pipes & subshells, produces no output, releases on finish.
	res, err := runTool(t, tool, map[string]any{
		"command": "my_tool_cmd hello | cat; printf ' '; (my_tool_cmd sub)",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("preamble sync run failed: %v", err)
	}
	if strings.Contains(res.Content, "SHOULD_NOT_APPEAR") || strings.Contains(res.Content, "ephemeral_cred_token_999") {
		t.Fatalf("preamble output or secret leaked: %q", res.Content)
	}
	if res.Content != "TOOL:hello TOOL:sub" {
		t.Fatalf("unexpected preamble command output: %q", res.Content)
	}
	select {
	case <-releaseCh:
	case <-time.After(30 * time.Second):
		t.Fatalf("sync release() was not called")
	}

	// Verify preamble secret/function was NOT written into the saved snapshot file.
	st := tool.runtime.sessionState("default")
	st.mu.Lock()
	sf := st.file
	st.mu.Unlock()
	if sf != "" {
		if raw, rErr := os.ReadFile(sf); rErr == nil {
			if strings.Contains(string(raw), "ephemeral_cred_token_999") || strings.Contains(string(raw), "my_tool_cmd") {
				t.Fatalf("preamble leaked into state snapshot %s:\n%s", sf, string(raw))
			}
		}
	}

	// 2) Async job: preamble callable in async job and release() fires when job finishes.
	aRes, err := runTool(t, tool, map[string]any{
		"command": "sleep 0.08; my_tool_cmd async-ok",
		"cwd":     dir,
		"async":   true,
	})
	if err != nil {
		t.Fatalf("preamble async start failed: %v", err)
	}
	aJobID := aRes.Details.(map[string]any)["async"].(map[string]any)["jobId"].(string)
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	snap, _, _ := tool.runtime.Jobs.Wait(waitCtx, aJobID)
	if !strings.Contains(snap.ResultText, "TOOL:async-ok") {
		t.Fatalf("async job did not invoke preamble function: %q", snap.ResultText)
	}
	select {
	case <-releaseCh:
	case <-time.After(30 * time.Second):
		t.Fatalf("async job release() was not called")
	}

	// 3) Service: preamble callable in service and release() fires only after service stops.
	sRes, err := runTool(t, tool, map[string]any{
		"name":    "tc-svc",
		"command": "my_tool_cmd svc-ready; echo ''; sleep 30",
		"cwd":     dir,
		"pty":     false,
		"ready":   map[string]any{"log": "TOOL:svc-ready", "timeout": 20},
	})
	if err != nil {
		t.Fatalf("preamble service start failed: %v", err)
	}
	if !strings.Contains(sRes.Content, "TOOL:svc-ready") {
		t.Fatalf("service did not invoke preamble function: %q", sRes.Content)
	}
	// Verify service log file does not contain preamble credential text.
	if sSnap, ok := tool.runtime.Services.Get("tc-svc"); ok && sSnap.LogPath != "" {
		if raw, rErr := os.ReadFile(sSnap.LogPath); rErr == nil && strings.Contains(string(raw), "ephemeral_cred_token_999") {
			t.Fatalf("preamble credential leaked into service log")
		}
	}
	relMu.Lock()
	beforeStop := releases
	relMu.Unlock()
	if beforeStop != 2 {
		t.Fatalf("service release() called prematurely while service running: releases=%d", beforeStop)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	_, _ = tool.runtime.Services.Stop(stopCtx, "tc-svc")
	select {
	case <-releaseCh:
	case <-time.After(30 * time.Second):
		t.Fatalf("service release() was not called on stop")
	}

	// Now disable CommandPreamble and verify neither my_tool_cmd nor __tc_var persisted.
	CommandPreamble = nil
	res, err = runTool(t, tool, map[string]any{
		"command": `type my_tool_cmd >/dev/null 2>&1 && echo LEAKED_FN || echo NO_FN; printf 'var=%s' "${__tc_var:-unset}"`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("post-preamble check failed: %v", err)
	}
	if res.Content != "NO_FN\nvar=unset" {
		t.Fatalf("preamble leaked into session snapshot: %q", res.Content)
	}

	// 4) CommandNames refuses pty: true with omp's exact text (tools/bash.ts:1903).
	CommandNames = func(_ sdk.Context) []string { return []string{"lsp", "ast_grep"} }
	for _, cmd := range []string{"lsp hover foo.go", "echo hi && FOO=1 lsp refs bar.go", "if true; then ast_grep pattern; fi"} {
		_, err := runTool(t, tool, map[string]any{
			"command": cmd,
			"cwd":     dir,
			"pty":     true,
		})
		if err == nil || !strings.Contains(err.Error(), "is a session tool command and cannot run in a PTY; run it without `pty`.") {
			t.Fatalf("pty refusal for %q: got err=%v", cmd, err)
		}
	}

	// 5) DirectCommand fast path receives (ctx, command, cwd, timeout) for zero,
	// non-zero, timed-out, and cancelled outcomes.
	var gotCwd string
	var gotTimeout time.Duration
	DirectCommand = func(_ sdk.Context, command, cwd string, timeout time.Duration) (string, int, bool, bool, bool) {
		gotCwd = cwd
		gotTimeout = timeout
		switch command {
		case "direct-ok":
			return "direct-output", 0, false, false, true
		case "direct-fail":
			return "bad args", 2, false, false, true
		case "direct-timeout":
			return "partial out", 0, true, false, true
		case "direct-cancel":
			return "partial cancel", 130, false, true, true
		default:
			return "", 0, false, false, false
		}
	}

	res, err = runTool(t, tool, map[string]any{"command": "direct-ok", "cwd": dir, "timeout": 12})
	if err != nil || res.IsError || res.Content != "direct-output" {
		t.Fatalf("direct-ok: res=%+v err=%v", res, err)
	}
	wantDir := dirs.StandardizeMacOSPath(dir)
	if gotCwd != wantDir || gotTimeout != 12*time.Second {
		t.Fatalf("DirectCommand args: cwd=%q timeout=%v, want cwd=%q timeout=12s", gotCwd, gotTimeout, wantDir)
	}
	res, err = runTool(t, tool, map[string]any{"command": "direct-fail", "cwd": dir})
	if err != nil || !res.IsError || !strings.Contains(res.Content, "bad args") || !strings.Contains(res.Content, "Command exited with code 2") {
		t.Fatalf("direct-fail: res=%+v err=%v", res, err)
	}
	if det, _ := res.Details.(map[string]any); det["exitCode"] != 2 {
		t.Fatalf("direct-fail exitCode=%v, want 2", det["exitCode"])
	}
	res, err = runTool(t, tool, map[string]any{"command": "direct-timeout", "cwd": dir, "timeout": 5})
	if err != nil || !res.IsError || !strings.Contains(res.Content, "Command timed out after 5 seconds") {
		t.Fatalf("direct-timeout: res=%+v err=%v", res, err)
	}
	if det, _ := res.Details.(map[string]any); det["timedOut"] != true {
		t.Fatalf("direct-timeout details.timedOut=%v, want true", det["timedOut"])
	}
	_, err = runTool(t, tool, map[string]any{"command": "direct-cancel", "cwd": dir})
	if err == nil || !strings.Contains(err.Error(), "Command cancelled") {
		t.Fatalf("direct-cancel: expected abort ToolError, got err=%v", err)
	}
}

func TestServiceUnixPTYAndCRStdin(t *testing.T) {
	if !ptySupported {
		t.Skip("PTY not supported on this platform")
	}
	t.Setenv("PI_NO_PTY", "")
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	urls.ResetForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
		urls.ResetForTests()
	})
	tool, dir := newTestTool(t, nil)
	_ = urls.Register(NewProcProtocolHandler(tool.runtime))
	res, err := runTool(t, tool, map[string]any{
		"name":    "pty-svc",
		"command": `if [ -t 1 ]; then echo IS_TTY; else echo NOT_TTY; fi; IFS= read -r line; echo "ECHO:$line"; sleep 30`,
		"cwd":     dir,
		"pty":     true,
		"ready":   map[string]any{"log": "^IS_TTY$", "timeout": 5},
	})
	if err != nil {
		t.Fatalf("pty service failed: %v", err)
	}
	if !strings.Contains(res.Content, "IS_TTY") || !strings.Contains(res.Content, "ready") {
		t.Fatalf("expected PTY service to see a TTY and become ready, got %q", res.Content)
	}
	urlCtx := urls.Context{Cwd: dir}
	if _, err := urls.Write(urlCtx, "proc://pty-svc", "hello-pty"); err != nil {
		t.Fatalf("write stdin to pty service: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if r, rErr := urls.Resolve(urlCtx, "proc://pty-svc"); rErr == nil && strings.Contains(r.Content, "ECHO:hello-pty") {
			got = r.Content
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(got, "ECHO:hello-pty") {
		t.Fatalf("expected PTY service to receive CR-terminated stdin, got %q", got)
	}
	_, _ = urls.Write(urlCtx, "proc://pty-svc/kill", "")
}

// upstream: shell-tokenize.test.ts: extractLeadingCdTarget with semicolon and exclusions
func TestExtractLeadingCdTargetV19(t *testing.T) {
	p, rest, sep, ok := extractLeadingCdTarget("cd /tmp; ls")
	if !ok || p != "/tmp" || rest != "ls" || sep != ";" {
		t.Fatalf("semicolon cd: p=%q rest=%q sep=%q ok=%v", p, rest, sep, ok)
	}
	for _, reject := range []string{
		"cd - && pwd",
		"cd +1 && pwd",
		"cd ~other && pwd",
		"cd foo* && pwd",
		"cd /tmp;; ls",
	} {
		if _, _, _, ok := extractLeadingCdTarget(reject); ok {
			t.Errorf("expected extractLeadingCdTarget(%q) to bail", reject)
		}
	}
}

func TestBashGatePolicyBlocksSyncAsyncAndService(t *testing.T) {
	gate.ResetForTests()
	t.Cleanup(gate.ResetForTests)

	var seen []gate.Request
	gate.SetPolicy(func(_ sdk.Context, r gate.Request) error {
		seen = append(seen, r)
		return fmt.Errorf("Plan mode active: bash execution is blocked (%s)", r.Detail)
	})

	tool, dir := newTestTool(t, nil)
	marker := filepath.Join(dir, "should-not-exist")
	cmd := fmt.Sprintf("touch %s", singleQuotePosix(marker))

	for _, tc := range []struct {
		label  string
		params map[string]any
	}{
		{"sync", map[string]any{"command": cmd, "cwd": dir}},
		{"async", map[string]any{"command": cmd, "cwd": dir, "async": true}},
		{"service", map[string]any{"name": "blocked-svc", "command": cmd, "cwd": dir}},
	} {
		_, err := runTool(t, tool, tc.params)
		if err == nil || !strings.Contains(err.Error(), "Plan mode active") {
			t.Fatalf("%s: expected gate error, got %v", tc.label, err)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 gate checks, got %d", len(seen))
	}
	for i, r := range seen {
		if r.Tool != "bash" || r.Op != gate.OpExec || r.Detail != cmd {
			t.Fatalf("gate check %d = %+v, want Tool=bash Op=exec Detail=%q", i, r, cmd)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("blocked command created %s", marker)
	}
}
