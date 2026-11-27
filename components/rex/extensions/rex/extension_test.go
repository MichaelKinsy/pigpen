// SPDX-License-Identifier: MIT

package rex_test

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	rex "github.com/MichaelKinsy/pigpen/components/rex/extensions/rex"
)

// ── Wire-level fake PiG host ─────────────────────────────────────────────────

type wireEnvelope struct {
	Type     string        `json:"type"`
	ID       string        `json:"id,omitempty"`
	Register *wireRegister `json:"register,omitempty"`
	Response *wireResponse `json:"response,omitempty"`
	Call     *wireCall     `json:"call,omitempty"`
}

type wireRegister struct {
	Name     string `json:"name"`
	Handlers []struct {
		Event     string `json:"event"`
		HandlerID int    `json:"handler_id"`
	} `json:"handlers"`
	Tools    []json.RawMessage `json:"tools"`
	Commands []json.RawMessage `json:"commands"`
}

type wireResponse struct {
	Result map[string]any `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type wireCall struct {
	Method string          `json:"method"`
	Args   json.RawMessage `json:"args"`
}

type fakeHost struct {
	t        *testing.T
	nc       net.Conn
	register wireRegister
	runDone  chan error

	writeMu         sync.Mutex
	mu              sync.Mutex
	systemPrompt    string
	systemPromptErr string
	pending         map[string]chan *wireResponse
	nextID          int
	handlers        map[string]int
}

func startHost(t *testing.T, ext *sdk.Extension, basePrompt string) *fakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:            t,
		nc:           hostSide,
		runDone:      make(chan error, 1),
		systemPrompt: basePrompt,
		pending:      map[string]chan *wireResponse{},
		handlers:     map[string]int{},
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	reg := h.readFrame()
	if reg.Type != "register" || reg.Register == nil {
		t.Fatalf("expected register frame, got %+v", reg)
	}
	h.register = *reg.Register
	for _, handler := range h.register.Handlers {
		h.handlers[handler.Event] = handler.HandlerID
	}
	h.write(map[string]any{
		"type":  "ready",
		"ready": map[string]any{"session_name": "", "cwd": t.TempDir(), "mode": "interactive", "width": 80},
	})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) readFrame() wireEnvelope {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var env wireEnvelope
	if err := json.Unmarshal(buf, &env); err != nil {
		h.t.Fatalf("unmarshal frame %s: %v", buf, err)
	}
	return env
}

func (h *fakeHost) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Errorf("marshal: %v", err)
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := h.nc.Write(hdr[:]); err != nil {
		return
	}
	_, _ = h.nc.Write(data)
}

func (h *fakeHost) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, buf); err != nil {
			return
		}
		var env wireEnvelope
		if json.Unmarshal(buf, &env) != nil {
			continue
		}
		switch env.Type {
		case "call":
			go h.answerCall(env)
		case "response":
			h.mu.Lock()
			ch := h.pending[env.ID]
			delete(h.pending, env.ID)
			h.mu.Unlock()
			if ch != nil {
				ch <- env.Response
			}
		}
	}
}

func (h *fakeHost) answerCall(env wireEnvelope) {
	h.mu.Lock()
	prompt := h.systemPrompt
	promptErr := h.systemPromptErr
	h.mu.Unlock()
	if env.Call != nil && (env.Call.Method == "getSystemPrompt" || env.Call.Method == "renderSystemPrompt") && promptErr != "" {
		h.write(map[string]any{
			"type": "call_result",
			"id":   env.ID,
			"call_result": map[string]any{
				"error": map[string]any{"message": promptErr},
			},
		})
		return
	}
	result := map[string]any{}
	if env.Call != nil && env.Call.Method == "getSystemPrompt" {
		result["prompt"] = prompt
	}
	h.write(map[string]any{
		"type":        "call_result",
		"id":          env.ID,
		"call_result": map[string]any{"result": result},
	})
}

func (h *fakeHost) fire(event string, data map[string]any) *wireResponse {
	h.t.Helper()
	hid, ok := h.handlers[event]
	if !ok {
		return nil
	}
	h.mu.Lock()
	h.nextID++
	id := "req-" + string(rune('0'+h.nextID))
	ch := make(chan *wireResponse, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	if data == nil {
		data = map[string]any{}
	}
	data["type"] = event
	args, _ := json.Marshal(data)
	h.write(map[string]any{
		"type": "request",
		"id":   id,
		"request": map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(args),
		},
	})

	select {
	case resp := <-ch:
		return resp
	case <-time.After(5 * time.Second):
		h.t.Fatalf("timed out waiting for %s response", event)
		return nil
	}
}

func (h *fakeHost) fireBeforeAgentStart(prompt string) (string, *wireResponse) {
	h.t.Helper()
	h.mu.Lock()
	base := h.systemPrompt
	h.mu.Unlock()
	resp := h.fire("before_agent_start", map[string]any{
		"prompt":              prompt,
		"systemPrompt":        base,
		"systemPromptOptions": map[string]any{},
	})
	if resp == nil || resp.Error != nil {
		return "", resp
	}
	if inner, ok := resp.Result["_pigPromptResult"].(map[string]any); ok {
		got, _ := inner["systemPrompt"].(string)
		return got, resp
	}
	got, _ := resp.Result["systemPrompt"].(string)
	return got, resp
}

func (h *fakeHost) stop() {
	if _, ok := h.handlers["session_shutdown"]; ok {
		h.fire("session_shutdown", nil)
	}
	h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{}})
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(2 * time.Second):
	}
}

// ── Fake `rex` executable helper ─────────────────────────────────────────────

func writeFakeRex(t *testing.T, scriptBody string) (dir string, callsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake rex helper")
	}
	dir = t.TempDir()
	callsFile = filepath.Join(dir, "calls.log")
	path := filepath.Join(dir, "rex")
	script := "#!/bin/sh\nprintf \"%s\\n\" \"$*\" >> " + shellQuote(callsFile) + "\n" + scriptBody + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake rex: %v", err)
	}
	return dir, callsFile
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func readCalls(t *testing.T, callsFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(callsFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read calls file: %v", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func lookPathInDir(dir string) func(string) (string, error) {
	return func(name string) (string, error) {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
		return "", errors.New("executable file not found in $PATH")
	}
}

func getenvMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// ── Tests ────────────────────────────────────────────────────────────────────

func TestInertWhenRexBlockUnsetOrEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "unset", env: map[string]string{}},
		{name: "empty", env: map[string]string{"REX_BLOCK": ""}},
		{name: "whitespace", env: map[string]string{"REX_BLOCK": "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, callsFile := writeFakeRex(t, `echo '{"session_id":"session:1","block_id":"block:1"}'`)
			ext := rex.NewForTest(getenvMap(tc.env), lookPathInDir(dir), time.Second)
			h := startHost(t, ext, "base prompt")

			if len(h.register.Handlers) != 0 {
				t.Fatalf("expected 0 handlers when REX_BLOCK is %s, got %+v", tc.name, h.register.Handlers)
			}
			if len(h.register.Tools) != 0 {
				t.Fatalf("expected 0 tools, got %d", len(h.register.Tools))
			}
			if calls := readCalls(t, callsFile); len(calls) != 0 {
				t.Fatalf("fake rex must never be called when REX_BLOCK is %s; got %v", tc.name, calls)
			}
		})
	}
}

func TestInertWhenRexAbsentFromPath(t *testing.T) {
	emptyDir := t.TempDir()
	ext := rex.NewForTest(
		getenvMap(map[string]string{"REX_BLOCK": "block:abc"}),
		lookPathInDir(emptyDir),
		time.Second,
	)
	h := startHost(t, ext, "base prompt")
	if len(h.register.Handlers) != 0 {
		t.Fatalf("expected 0 handlers when rex is absent from PATH, got %+v", h.register.Handlers)
	}
	if len(h.register.Tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(h.register.Tools))
	}
}

func TestWhoamiSessionTabAndPane(t *testing.T) {
	dir, callsFile := writeFakeRex(t, `cat <<'JSON'
{
  "account": "alice",
  "endpoint": "unix:///tmp/rex/server.sock",
  "session_count": 1,
  "session_id": "session:demo-1",
  "window_id": "window:demo-2",
  "block_id": "block:demo-3"
}
JSON`)

	ext := rex.NewForTest(
		getenvMap(map[string]string{"REX_BLOCK": "block:demo-3"}),
		lookPathInDir(dir),
		2*time.Second,
	)
	h := startHost(t, ext, "BASE PROMPT")

	if len(h.register.Tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(h.register.Tools))
	}

	resp := h.fire("session_start", map[string]any{"reason": "startup"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session_start returned error: %+v", resp)
	}

	// Second session_start must not re-invoke rex whoami.
	resp = h.fire("session_start", map[string]any{"reason": "reload"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("second session_start returned error: %+v", resp)
	}

	got, before := h.fireBeforeAgentStart("hi")
	if before == nil || before.Error != nil {
		t.Fatalf("before_agent_start error: %+v", before)
	}
	wantLine := "You are running in a Rex terminal: session session:demo-1, tab window:demo-2, pane block:demo-3."
	want := "BASE PROMPT\n\n" + wantLine
	if got != want {
		t.Fatalf("systemPrompt mismatch:\ngot:  %q\nwant: %q", got, want)
	}

	calls := readCalls(t, callsFile)
	if len(calls) != 1 {
		t.Fatalf("expected rex to be invoked once, got %d calls: %v", len(calls), calls)
	}
	if calls[0] != "whoami --autostart=false --json" {
		t.Fatalf("unexpected rex args: %q", calls[0])
	}
}

func TestWhoamiSessionAndPaneWithoutWindowID(t *testing.T) {
	dir, _ := writeFakeRex(t, `cat <<'JSON'
{
  "session_id": "session:demo-1",
  "block_id": "block:demo-3"
}
JSON`)

	ext := rex.NewForTest(
		getenvMap(map[string]string{"REX_BLOCK": "block:demo-3"}),
		lookPathInDir(dir),
		2*time.Second,
	)
	h := startHost(t, ext, "BASE")

	h.fire("session_start", map[string]any{"reason": "startup"})
	got, before := h.fireBeforeAgentStart("hi")
	if before == nil || before.Error != nil {
		t.Fatalf("before_agent_start error: %+v", before)
	}
	want := "BASE\n\nYou are running in a Rex terminal: session session:demo-1, pane block:demo-3."
	if got != want {
		t.Fatalf("systemPrompt mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestBeforeAgentStartDoesNotReplacePromptOnGetSystemPromptError(t *testing.T) {
	res, err := rex.AppendContextLineForTest(
		"You are running in a Rex terminal: session session:1, tab window:2, pane block:3.",
		func() (string, error) { return "", errors.New("host unavailable") },
	)
	if err != nil {
		t.Fatalf("before_agent_start must not return an error on GetSystemPrompt failure: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result when GetSystemPrompt fails (must not replace system prompt), got %#v", res)
	}
}

func TestSessionStartAsyncProbeNonBlockingAndSecondRunReceivesResult(t *testing.T) {
	dir, _ := writeFakeRex(t, `sleep 1; echo '{"session_id":"session:async-1","window_id":"window:async-2","block_id":"block:async-3"}'`)

	ext := rex.NewForTestWithWait(
		getenvMap(map[string]string{"REX_BLOCK": "block:async-3"}),
		lookPathInDir(dir),
		3*time.Second,
		50*time.Millisecond,
	)
	h := startHost(t, ext, "BASE")

	start := time.Now()
	resp := h.fire("session_start", map[string]any{"reason": "startup"})
	elapsed := time.Since(start)
	t.Logf("session_start duration with 1s fake rex: %v", elapsed)
	if resp == nil || resp.Error != nil {
		t.Fatalf("session_start returned error: %+v", resp)
	}
	if elapsed >= 5*time.Millisecond {
		t.Fatalf("session_start took %v; expected < 5ms (must not block on rex whoami)", elapsed)
	}

	// Run 1: before_agent_start stops waiting after 50ms and injects nothing.
	got1, before1 := h.fireBeforeAgentStart("run 1")
	if before1 == nil || before1.Error != nil {
		t.Fatalf("run 1 before_agent_start error: %+v", before1)
	}
	if got1 != "" || before1.Result["_pigPromptResult"] != nil {
		t.Fatalf("run 1 expected nil _pigPromptResult while probe still running, got %q (%+v)", got1, before1.Result)
	}

	// Poll until the 1s fake rex probe finishes (shell startup + sleep 1 can exceed 1.1s on macOS),
	// then run 2 must receive the result.
	want := "BASE\n\nYou are running in a Rex terminal: session session:async-1, tab window:async-2, pane block:async-3."
	var got2 string
	deadline := time.Now().Add(3 * time.Second)
	for {
		var before2 *wireResponse
		got2, before2 = h.fireBeforeAgentStart("run 2")
		if before2 == nil || before2.Error != nil {
			t.Fatalf("run 2 before_agent_start error: %+v", before2)
		}
		if got2 == want || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got2 != want {
		t.Fatalf("run 2 systemPrompt mismatch:\ngot:  %q\nwant: %q", got2, want)
	}
}

func TestWhoamiMissingPaneIDEmitsUnknownPaneFallback(t *testing.T) {
	dir, _ := writeFakeRex(t, `cat <<'JSON'
{
  "account": "alice",
  "endpoint": "unix:///tmp/rex/server.sock",
  "session_count": 1
}
JSON`)

	ext := rex.NewForTest(
		getenvMap(map[string]string{"REX_BLOCK": "block:stale"}),
		lookPathInDir(dir),
		2*time.Second,
	)
	h := startHost(t, ext, "BASE")

	resp := h.fire("session_start", map[string]any{"reason": "startup"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session_start must not error: %+v", resp)
	}
	got, before := h.fireBeforeAgentStart("hi")
	if before == nil || before.Error != nil {
		t.Fatalf("before_agent_start must not error: %+v", before)
	}
	want := "BASE\n\n" + rex.UnknownPaneFallbackLine
	if got != want {
		t.Fatalf("systemPrompt mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestWhoamiSlowTimesOutCleanlyWithNoAnswerFallback(t *testing.T) {
	dir, _ := writeFakeRex(t, `sleep 5; echo '{"session_id":"session:late","block_id":"block:late"}'`)

	ext := rex.NewForTest(
		getenvMap(map[string]string{"REX_BLOCK": "block:slow"}),
		lookPathInDir(dir),
		120*time.Millisecond,
	)
	h := startHost(t, ext, "BASE")

	resp := h.fire("session_start", map[string]any{"reason": "startup"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session_start must not error on timeout: %+v", resp)
	}

	got, before := h.fireBeforeAgentStart("hi")
	if before == nil || before.Error != nil {
		t.Fatalf("before_agent_start must not error: %+v", before)
	}
	want := "BASE\n\n" + rex.NoAnswerFallbackLine
	if got != want {
		t.Fatalf("systemPrompt mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestWhoamiGarbageAndNonZeroExitEmitNoAnswerFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
	}{
		{name: "garbage-stdout", script: `echo "not valid json at all"`},
		{name: "nonzero-exit", script: `echo "server unreachable" >&2; exit 1`},
		{name: "empty-stdout", script: `exit 0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := writeFakeRex(t, tc.script)
			ext := rex.NewForTest(
				getenvMap(map[string]string{"REX_BLOCK": "block:err"}),
				lookPathInDir(dir),
				time.Second,
			)
			h := startHost(t, ext, "BASE")

			resp := h.fire("session_start", map[string]any{"reason": "startup"})
			if resp == nil || resp.Error != nil {
				t.Fatalf("session_start must not error: %+v", resp)
			}
			got, before := h.fireBeforeAgentStart("hi")
			if before == nil || before.Error != nil {
				t.Fatalf("before_agent_start must not error: %+v", before)
			}
			want := "BASE\n\n" + rex.NoAnswerFallbackLine
			if got != want {
				t.Fatalf("systemPrompt mismatch:\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}

func TestSessionShutdownDuringSlowProbeAllowsNextSessionStartToProbeAgain(t *testing.T) {
	stateDir := t.TempDir()
	startedFile := filepath.Join(stateDir, "call1-started")
	countFile := filepath.Join(stateDir, "call-count")
	body := `n=0
if [ -f "` + countFile + `" ]; then
  n=$(cat "` + countFile + `")
fi
n=$((n + 1))
echo "$n" > "` + countFile + `"
if [ "$n" -eq 1 ]; then
  touch "` + startedFile + `"
  sleep 10
  exit 0
fi
printf '{"session_id":"session:01next","window_id":"window:02next","block_id":"block:03next"}\n'`
	dir, _ := writeFakeRex(t, body)

	ext := rex.NewForTest(
		getenvMap(map[string]string{"REX_BLOCK": "block:03next"}),
		lookPathInDir(dir),
		2*time.Second,
	)
	h := startHost(t, ext, "BASE")

	// First session_start launches call 1 (which sleeps 10s).
	if resp := h.fire("session_start", map[string]any{"reason": "startup"}); resp == nil || resp.Error != nil {
		t.Fatalf("first session_start: %+v", resp)
	}
	// Wait until call 1 has actually started so session_shutdown cancels an in-flight probe.
	waitDeadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("first fake rex probe did not start in time")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Shut down the session while call 1 is still running: must cancel without publishing noAnswerFallbackLine.
	if resp := h.fire("session_shutdown", map[string]any{"reason": "new"}); resp == nil || resp.Error != nil {
		t.Fatalf("session_shutdown: %+v", resp)
	}

	// Next session_start must probe again (call 2) and get the real session/tab/pane line.
	if resp := h.fire("session_start", map[string]any{"reason": "new"}); resp == nil || resp.Error != nil {
		t.Fatalf("second session_start: %+v", resp)
	}
	got, before := h.fireBeforeAgentStart("hi")
	if before == nil || before.Error != nil {
		t.Fatalf("before_agent_start: %+v", before)
	}
	want := "BASE\n\nYou are running in a Rex terminal: session session:01next, tab window:02next, pane block:03next."
	if got != want {
		t.Fatalf("systemPrompt mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
