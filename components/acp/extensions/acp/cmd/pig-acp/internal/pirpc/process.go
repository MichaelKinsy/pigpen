// Package pirpc drives `pig --mode rpc` as a child process (src/pi-rpc/* of pi-acp).
package pirpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SessionStatsTimeoutMs bounds the auxiliary context-usage request.
const SessionStatsTimeoutMs = 1000

// DefaultCommand is the executable started when none is configured.
func DefaultCommand(goos string) string {
	if goos == "windows" {
		return "pig.cmd"
	}
	return "pig"
}

// Command returns the override, or the default for this OS.
func Command(override string) string {
	if override != "" {
		return override
	}
	return DefaultCommand(runtime.GOOS)
}

// ShouldUseShell reports whether a launcher needs a shell (Windows .cmd and .bat).
func ShouldUseShell(goos, cmd string) bool {
	if goos != "windows" {
		return false
	}
	l := strings.ToLower(cmd)
	return strings.HasSuffix(l, ".cmd") || strings.HasSuffix(l, ".bat")
}

// SpawnError is a failure to start the child (ENOENT, EACCES).
type SpawnError struct {
	Message string
	Code    string
	cause   error
}

func (e *SpawnError) Error() string { return e.Message }
func (e *SpawnError) Unwrap() error { return e.cause }

// SpawnCode is the underlying error code (ENOENT, EACCES) or "".
func (e *SpawnError) SpawnCode() string { return e.Code }

// Response is a pi RPC response.
type Response struct {
	Success bool
	Data    any
	Error   string
}

type wireResponse struct {
	Type    string          `json:"type"`
	ID      *string         `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

type pendingCall struct {
	ch chan result
}

type result struct {
	res Response
	err error
}

// Process is one running pig RPC child.
type Process struct {
	stdin io.WriteCloser
	kill  func()

	writeMu sync.Mutex

	mu       sync.Mutex
	pending  map[string]*pendingCall
	handlers map[int]func(map[string]any)
	nextH    int
	prelude  []string
	closed   bool
	exitErr  error
	seq      atomic.Uint64

	// requestFn replaces the transport in tests.
	requestFn func(map[string]any, time.Duration) (Response, error)
}

// NewProcess builds a Process around already connected pipes (the tests' seam).
func NewProcess(stdin io.WriteCloser, stdout io.Reader, kill func()) *Process {
	p := &Process{stdin: stdin, kill: kill, pending: map[string]*pendingCall{}, handlers: map[int]func(map[string]any){}}
	go p.readLoop(stdout, nil)
	return p
}

var ansiRE = regexp.MustCompile("[\x1b\u009b][\\[\\]()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]")

func (p *Process) readLoop(stdout io.Reader, wait func() error) {
	br := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := readLine(br)
		if t := bytes.TrimSpace(line); len(t) > 0 {
			p.handleLine(t)
		}
		if err != nil {
			break
		}
	}
	code := "pi process exited"
	if wait != nil {
		if werr := wait(); werr != nil {
			code = fmt.Sprintf("pi process exited (%v)", werr)
		}
	}
	p.mu.Lock()
	p.closed = true
	p.exitErr = errors.New(code)
	pend := p.pending
	p.pending = map[string]*pendingCall{}
	p.mu.Unlock()
	for _, c := range pend {
		c.ch <- result{err: p.exitErr}
	}
}

func readLine(br *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		part, err := br.ReadSlice('\n')
		out = append(out, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return out, err
	}
}

func (p *Process) handleLine(line []byte) {
	var msg map[string]any
	if err := json.Unmarshal(line, &msg); err != nil {
		// pig may print a human-readable prelude before the JSON lines start.
		cleaned := strings.TrimRight(ansiRE.ReplaceAllString(string(line), ""), " \t\r\n")
		if cleaned != "" {
			p.mu.Lock()
			p.prelude = append(p.prelude, cleaned)
			p.mu.Unlock()
		}
		return
	}
	if msg["type"] == "response" {
		// A response is never an event: unknown or timed-out ids are dropped.
		id, ok := msg["id"].(string)
		if !ok {
			return
		}
		p.mu.Lock()
		c := p.pending[id]
		delete(p.pending, id)
		p.mu.Unlock()
		if c == nil {
			return
		}
		var w wireResponse
		_ = json.Unmarshal(line, &w)
		r := Response{Success: w.Success, Error: w.Error}
		if len(w.Data) > 0 {
			_ = json.Unmarshal(w.Data, &r.Data)
		}
		c.ch <- result{res: r}
		return
	}
	p.mu.Lock()
	hs := make([]func(map[string]any), 0, len(p.handlers))
	for i := 0; i < p.nextH; i++ {
		if h, ok := p.handlers[i]; ok {
			hs = append(hs, h)
		}
	}
	p.mu.Unlock()
	for _, h := range hs {
		h(msg)
	}
}

// Spawn starts the child.
func Spawn(cwd, command, sessionPath string) (*Process, error) {
	return SpawnWithArgs(cwd, command, sessionPath, nil)
}

// SpawnWithArgs is Spawn with extra pig arguments (--piglet ...) placed before the session flag.
func SpawnWithArgs(cwd, command, sessionPath string, extra []string) (*Process, error) {
	cmdName := Command(command)
	cmd := exec.Command(cmdName, BuildArgs(extra, sessionPath)...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, spawnFailure(cmdName, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, spawnFailure(cmdName, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, spawnFailure(cmdName, err)
	}
	p := &Process{stdin: stdin, pending: map[string]*pendingCall{}, handlers: map[int]func(map[string]any){}}
	p.kill = func() { terminate(cmd) }
	go p.readLoop(stdout, cmd.Wait)

	// Best-effort handshake. pig may report a sessionFile in a directory that is created lazily;
	// create it now so later commands (export_html) do not fail on a missing parent.
	if state, err := p.GetState(); err == nil {
		if f, ok := state["sessionFile"].(string); ok && f != "" {
			_ = os.MkdirAll(filepath.Dir(f), 0o755)
		}
	}
	return p, nil
}

// BuildArgs is the pig argument list for RPC mode.
func BuildArgs(extra []string, sessionPath string) []string {
	args := []string{"--mode", "rpc", "--no-themes"}
	args = append(args, extra...)
	if sessionPath != "" {
		args = append(args, "--session", sessionPath)
	}
	return args
}

func spawnFailure(cmd string, err error) *SpawnError {
	code := ""
	switch {
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
		code = "ENOENT"
	case errors.Is(err, os.ErrPermission):
		code = "EACCES"
	}
	switch code {
	case "ENOENT":
		return &SpawnError{Code: code, cause: err, Message: fmt.Sprintf("Could not start pig: executable not found (command: %s). PiG needs to be installed before it can run in ACP clients. Install it from https://github.com/MichaelKinsy/PiG or ensure `pig` is on your PATH (or point --pig at a Piglet Binary). Then try again.", cmd)}
	case "EACCES":
		return &SpawnError{Code: code, cause: err, Message: fmt.Sprintf("Could not start pig: permission denied (command: %s).", cmd)}
	}
	return &SpawnError{Code: code, cause: err, Message: fmt.Sprintf("Could not start pig (command: %s).", cmd)}
}

// OnEvent registers an event handler.
func (p *Process) OnEvent(h func(map[string]any)) func() {
	p.mu.Lock()
	id := p.nextH
	p.nextH++
	p.handlers[id] = h
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.handlers, id)
		p.mu.Unlock()
	}
}

// Dispose kills the child.
func (p *Process) Dispose() {
	if p.kill != nil {
		p.kill()
	}
}

// PendingCount is the number of unanswered requests.
func (p *Process) PendingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}

// ConsumePreludeLines returns, and clears, the human-readable lines pig printed before its JSON.
func (p *Process) ConsumePreludeLines() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.prelude
	p.prelude = nil
	return out
}

// Request sends a raw command and waits for its response; timeout 0 waits without a limit.
func (p *Process) Request(cmd map[string]any, timeout time.Duration) (Response, error) {
	if p.requestFn != nil {
		return p.requestFn(cmd, timeout)
	}
	id := fmt.Sprintf("acp-%d-%d", os.Getpid(), p.seq.Add(1))
	full := make(map[string]any, len(cmd)+1)
	for k, v := range cmd {
		full[k] = v
	}
	full["id"] = id
	line, err := json.Marshal(full)
	if err != nil {
		return Response{}, err
	}
	call := &pendingCall{ch: make(chan result, 1)}
	p.mu.Lock()
	if p.closed {
		err := p.exitErr
		p.mu.Unlock()
		return Response{}, err
	}
	p.pending[id] = call
	p.mu.Unlock()

	drop := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		_, ok := p.pending[id]
		delete(p.pending, id)
		return ok
	}
	if err := p.writeLine(append(line, '\n')); err != nil {
		if drop() {
			return Response{}, err
		}
	}
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	select {
	case r := <-call.ch:
		return r.res, r.err
	case <-timer:
		if !drop() {
			r := <-call.ch
			return r.res, r.err
		}
		return Response{}, fmt.Errorf("pi %v timed out after %dms", cmd["type"], timeout.Milliseconds())
	}
}

func (p *Process) writeLine(b []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err := p.stdin.Write(b)
	return err
}

func (p *Process) call(cmd map[string]any, timeout time.Duration) (Response, error) {
	res, err := p.Request(cmd, timeout)
	if err != nil {
		return res, err
	}
	if !res.Success {
		return res, fmt.Errorf("pi %v failed: %s", cmd["type"], failure(res))
	}
	return res, nil
}

func failure(res Response) string {
	if res.Error != "" {
		return res.Error
	}
	b, _ := json.Marshal(res.Data)
	return string(b)
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// Prompt sends a prompt; the turn's events arrive through OnEvent.
func (p *Process) Prompt(message string, images []map[string]any) error {
	if images == nil {
		images = []map[string]any{}
	}
	_, err := p.call(map[string]any{"type": "prompt", "message": message, "images": images}, 0)
	return err
}

// Abort stops the running turn.
func (p *Process) Abort() error { _, err := p.call(map[string]any{"type": "abort"}, 0); return err }

// GetState returns get_state data.
func (p *Process) GetState() (map[string]any, error) {
	res, err := p.call(map[string]any{"type": "get_state"}, 0)
	return asMap(res.Data), err
}

// GetAvailableModels returns get_available_models data.
func (p *Process) GetAvailableModels() (map[string]any, error) {
	res, err := p.call(map[string]any{"type": "get_available_models"}, 0)
	return asMap(res.Data), err
}

// SetModel switches the model.
func (p *Process) SetModel(provider, modelID string) error {
	_, err := p.call(map[string]any{"type": "set_model", "provider": provider, "modelId": modelID}, 0)
	return err
}

// GetAvailableThinkingLevels lists the levels the current model supports.
func (p *Process) GetAvailableThinkingLevels() ([]string, error) {
	res, err := p.call(map[string]any{"type": "get_available_thinking_levels"}, 0)
	if err != nil {
		return nil, err
	}
	raw, _ := asMap(res.Data)["levels"].([]any)
	if len(raw) == 0 {
		return nil, errors.New("pi get_available_thinking_levels returned invalid levels")
	}
	levels := make([]string, 0, len(raw))
	for _, l := range raw {
		s, ok := l.(string)
		if !ok || s == "" {
			return nil, errors.New("pi get_available_thinking_levels returned invalid levels")
		}
		levels = append(levels, s)
	}
	return levels, nil
}

// SetThinkingLevel sets the thinking level.
func (p *Process) SetThinkingLevel(level string) error {
	_, err := p.call(map[string]any{"type": "set_thinking_level", "level": level}, 0)
	return err
}

// SetFollowUpMode sets how follow-up messages are delivered.
func (p *Process) SetFollowUpMode(mode string) error {
	_, err := p.call(map[string]any{"type": "set_follow_up_mode", "mode": mode}, 0)
	return err
}

// SetSteeringMode sets how steering messages are delivered.
func (p *Process) SetSteeringMode(mode string) error {
	_, err := p.call(map[string]any{"type": "set_steering_mode", "mode": mode}, 0)
	return err
}

// Compact runs manual compaction; customInstructions "" sends none.
func (p *Process) Compact(customInstructions string) (map[string]any, error) {
	cmd := map[string]any{"type": "compact"}
	if customInstructions != "" {
		cmd["customInstructions"] = customInstructions
	}
	res, err := p.call(cmd, 0)
	return asMap(res.Data), err
}

// SetAutoCompaction toggles automatic compaction.
func (p *Process) SetAutoCompaction(enabled bool) error {
	_, err := p.call(map[string]any{"type": "set_auto_compaction", "enabled": enabled}, 0)
	return err
}

// GetSessionStats asks for get_session_stats; timeoutMs 0 waits without a limit.
func (p *Process) GetSessionStats(timeoutMs int) (map[string]any, error) {
	res, err := p.call(map[string]any{"type": "get_session_stats"}, time.Duration(timeoutMs)*time.Millisecond)
	m := asMap(res.Data)
	if m == nil && err == nil {
		m = map[string]any{}
	}
	return m, err
}

// SetSessionName names the session.
func (p *Process) SetSessionName(name string) error {
	_, err := p.call(map[string]any{"type": "set_session_name", "name": name}, 0)
	return err
}

// ExportHTML returns the path pig wrote; outputPath "" lets pig choose.
func (p *Process) ExportHTML(outputPath string) (string, error) {
	cmd := map[string]any{"type": "export_html"}
	if outputPath != "" {
		cmd["outputPath"] = outputPath
	}
	res, err := p.call(cmd, 0)
	if err != nil {
		return "", err
	}
	if s, ok := asMap(res.Data)["path"].(string); ok {
		return s, nil
	}
	return "", nil
}

// GetMessages returns get_messages data.
func (p *Process) GetMessages() (map[string]any, error) {
	res, err := p.call(map[string]any{"type": "get_messages"}, 0)
	return asMap(res.Data), err
}

// GetCommands returns get_commands data.
func (p *Process) GetCommands() (map[string]any, error) {
	res, err := p.call(map[string]any{"type": "get_commands"}, 0)
	return asMap(res.Data), err
}

// SendExtensionUIResponse answers an extension_ui_request.
func (p *Process) SendExtensionUIResponse(resp map[string]any) error {
	msg := map[string]any{"type": "extension_ui_response"}
	for k, v := range resp {
		msg[k] = v
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.writeLine(append(b, '\n'))
}
