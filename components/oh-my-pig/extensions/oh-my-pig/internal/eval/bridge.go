package eval

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
)

// upstream: packages/coding-agent/src/eval/py/tool-bridge.ts,
//           packages/coding-agent/src/eval/js/tool-bridge.ts,
//           packages/coding-agent/src/eval/shell-bridge.ts,
//           packages/coding-agent/src/eval/handle-bridge.ts,
//           packages/coding-agent/src/eval/tool-bridge-policy.ts

const maxShellOutputChars = 16 * 1024 * 1024

// ToolExecutor is an optional in-package fallback for calling tools when
// sdk.Context.ExecuteTool is unavailable or in unit tests without a host tool registry.
type ToolExecutor func(ctx context.Context, name string, args map[string]any) (sdk.AgentToolResult, error)

// bridgeRun holds host-side execution state for one active eval cell.
type bridgeRun struct {
	sdkCtx             sdk.Context
	ctx                context.Context
	cwd                string
	sessionID          string
	language           string // "py" or "js"
	cellTimeoutMs      int
	emitStatus         func(EvalStatusEvent)
	onUpdate           func(any)
	onReply            func(string)
	onFirstCallPrelude func(Prelude)
	toolFallback       ToolExecutor
	// activeEvalKeys tracks session+language slots currently executing in this call chain
	// so nested `tool.eval` on the same kernel fails cleanly instead of deadlocking.
	activeSlotKey string
}

// bridgeServer is the shared loopback HTTP bridge for Python and JS subprocesses.
// Both kernels POST JSON `{session, run, name, args}` to `/v1/tool` with Bearer token auth.
type bridgeServer struct {
	url   string
	token string

	srv *http.Server
	ln  net.Listener

	mu   sync.RWMutex
	runs map[string]*bridgeRun // key: sessionID + ":" + runID
}

var (
	sharedBridgeMu sync.Mutex
	sharedBridge   *bridgeServer
)

func ensureBridgeServer() (*bridgeServer, error) {
	sharedBridgeMu.Lock()
	defer sharedBridgeMu.Unlock()
	if sharedBridge != nil {
		return sharedBridge, nil
	}
	b, err := startBridgeServer()
	if err != nil {
		return nil, err
	}
	sharedBridge = b
	return b, nil
}

func startBridgeServer() (*bridgeServer, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("generate bridge token: %w", err)
	}
	token := hex.EncodeToString(raw[:])

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen eval tool bridge: %w", err)
	}
	b := &bridgeServer{
		url:   "http://" + ln.Addr().String(),
		token: token,
		ln:    ln,
		runs:  make(map[string]*bridgeRun),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tool", b.handleTool)
	b.srv = &http.Server{Handler: mux}
	go func() {
		_ = b.srv.Serve(ln)
	}()
	return b, nil
}

func (b *bridgeServer) stop() {
	if b == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = b.srv.Shutdown(ctx)
}

func (b *bridgeServer) register(sessionID, runID string, run *bridgeRun) func() {
	key := sessionID + ":" + runID
	b.mu.Lock()
	b.runs[key] = run
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		if b.runs[key] == run {
			delete(b.runs, key)
		}
		b.mu.Unlock()
	}
}

func (b *bridgeServer) lookup(sessionID, runID string) (*bridgeRun, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r, ok := b.runs[sessionID+":"+runID]
	return r, ok
}

type bridgeRequest struct {
	Session string          `json:"session"`
	Run     string          `json:"run"`
	Name    string          `json:"name"`
	Args    json.RawMessage `json:"args"`
}

type bridgeResponse struct {
	OK    bool   `json:"ok"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (b *bridgeServer) handleTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+b.token {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32*1024*1024))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, bridgeResponse{OK: false, Error: "Failed to read request body"})
		return
	}
	var req bridgeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, bridgeResponse{OK: false, Error: "Invalid JSON body"})
		return
	}
	if req.Session == "" || req.Run == "" || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, bridgeResponse{OK: false, Error: "Missing session/run/name"})
		return
	}
	run, ok := b.lookup(req.Session, req.Run)
	if !ok {
		writeJSON(w, http.StatusOK, bridgeResponse{
			OK:    false,
			Error: fmt.Sprintf("No active eval tool bridge session: %s:%s", req.Session, req.Run),
		})
		return
	}
	if run.ctx.Err() != nil {
		writeJSON(w, http.StatusOK, bridgeResponse{
			OK:    false,
			Error: fmt.Sprintf("bridge call %q aborted: eval cell was interrupted", req.Name),
		})
		return
	}

	// Race dispatch against run cancellation so tools ignoring context don't wedge the kernel.
	type outcome struct {
		val any
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		v, callErr := dispatchBridgeCall(run, req.Name, req.Args)
		ch <- outcome{val: v, err: callErr}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			writeJSON(w, http.StatusOK, bridgeResponse{OK: false, Error: res.err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, bridgeResponse{OK: true, Value: res.val})
	case <-run.ctx.Done():
		writeJSON(w, http.StatusOK, bridgeResponse{
			OK:    false,
			Error: fmt.Sprintf("bridge call %q aborted: eval cell was interrupted", req.Name),
		})
	case <-r.Context().Done():
		return
	}
}

func isEvalBridgeToolCallable(name string) bool {
	// upstream: packages/coding-agent/src/eval/tool-bridge-policy.ts:24
	return name != "checkpoint" && name != "rewind" && name != "yield"
}

// isBuiltinBridgePrelude reports whether a prelude supplies host-only backing for
// omp's built-in `agent()` / `workpool()` helpers (whose globals already live in
// omp's embedded prelude.py and prelude.txt).
func isBuiltinBridgePrelude(name string) bool {
	return name == "agent" || name == "workpool"
}

func dispatchBridgeCall(run *bridgeRun, name string, rawArgs json.RawMessage) (any, error) {
	switch name {
	case "__prelude__":
		return dispatchPreludeCall(run, rawArgs)
	case "__agent__":
		return dispatchBuiltinPreludeBridge(run, "agent", rawArgs)
	case "__workpool__":
		return dispatchBuiltinPreludeBridge(run, "workpool", rawArgs)
	case "__shell__":
		return dispatchShellCall(run, rawArgs)
	case "__wait__":
		return dispatchWaitCall(run, rawArgs)
	case "__status__":
		return dispatchStatusCall(run, rawArgs)
	case "__cancel__":
		return dispatchCancelCall(run, rawArgs)
	case "__budget__":
		return map[string]any{"total": nil, "spent": 0, "hard": false}, nil
	case "__completion__", "__judge__", "__judge_batch__":
		return nil, fmt.Errorf("%s is not available in this session", strings.Trim(name, "_"))
	default:
		return dispatchSessionToolCall(run, name, rawArgs)
	}
}

// dispatchBuiltinPreludeBridge routes omp's built-in `__agent__` and `__workpool__`
// bridge calls directly to registered `agent` / `workpool` preludes (host-only Invoke).
// Returns PreludeResult.Details directly as the bridge JSON value (falling back to
// JSON-decoding PreludeResult.Text if Details is nil).
// upstream: packages/coding-agent/src/eval/agent-bridge.ts, workpool-bridge.ts
func dispatchBuiltinPreludeBridge(run *bridgeRun, preludeName string, rawArgs json.RawMessage) (any, error) {
	p, ok := LookupPrelude(preludeName)
	if !ok || (p.Enabled != nil && !p.Enabled(run.sdkCtx)) || p.Invoke == nil {
		return nil, fmt.Errorf("%s is not available in this session", preludeName)
	}
	params := rawArgs
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	call := PreludeCall{
		SDK:           run.sdkCtx,
		CallID:        fmt.Sprintf("prelude-%s-%s", preludeName, newRandomID()),
		Language:      run.language,
		CellTimeoutMs: run.cellTimeoutMs,
		OnUpdate:      run.onUpdate,
	}
	if p.Approval != "" {
		if err := approval.CheckPrelude(run.sdkCtx, preludeName, p.Approval, params); err != nil {
			if run.emitStatus != nil {
				run.emitStatus(EvalStatusEvent{"op": preludeName, "error": err.Error()})
			}
			return nil, err
		}
	}
	res, err := withBridgeTimeoutPause(run.emitStatus, func() (*PreludeResult, error) {
		return p.Invoke(call, params)
	})
	if err != nil {
		if run.emitStatus != nil {
			run.emitStatus(EvalStatusEvent{"op": preludeName, "error": err.Error()})
		}
		return nil, err
	}
	if res == nil {
		return map[string]any{}, nil
	}
	if run.emitStatus != nil && p.Status != nil {
		if detail := p.Status(params, res); detail != "" {
			run.emitStatus(EvalStatusEvent{"op": preludeName, "detail": detail})
		}
	}
	if res.Details != nil {
		return res.Details, nil
	}
	if strings.TrimSpace(res.Text) != "" {
		var decoded any
		if err := json.Unmarshal([]byte(res.Text), &decoded); err == nil {
			return decoded, nil
		}
	}
	return res.Text, nil
}

func dispatchPreludeCall(run *bridgeRun, rawArgs json.RawMessage) (any, error) {
	var req struct {
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(rawArgs, &req); err != nil || req.Name == "" {
		return nil, errors.New("Invalid eval prelude bridge request")
	}
	p, ok := LookupPrelude(req.Name)
	if !ok || (p.Enabled != nil && !p.Enabled(run.sdkCtx)) {
		err := fmt.Errorf(`Eval prelude "%s" is not enabled in the current session.`, req.Name)
		if run.emitStatus != nil {
			run.emitStatus(EvalStatusEvent{"op": req.Name, "error": err.Error()})
		}
		return nil, err
	}
	if p.Invoke == nil {
		err := fmt.Errorf(`Eval prelude "%s" has no host calls.`, req.Name)
		if run.emitStatus != nil {
			run.emitStatus(EvalStatusEvent{"op": req.Name, "error": err.Error()})
		}
		return nil, err
	}

	params := req.Parameters
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	// upstream: packages/coding-agent/src/eval/preludes.ts:219
	if strings.TrimSpace(p.FirstCallDocumentation) != "" && run.onFirstCallPrelude != nil {
		run.onFirstCallPrelude(p)
	}
	call := PreludeCall{
		SDK:           run.sdkCtx,
		CallID:        fmt.Sprintf("prelude-%s-%s", req.Name, newRandomID()),
		Language:      run.language,
		CellTimeoutMs: run.cellTimeoutMs,
		OnUpdate:      run.onUpdate,
	}

	if p.Approval != "" {
		if err := approval.CheckPrelude(run.sdkCtx, req.Name, p.Approval, params); err != nil {
			if run.emitStatus != nil {
				run.emitStatus(EvalStatusEvent{"op": req.Name, "error": err.Error()})
			}
			return nil, err
		}
	}
	res, err := withBridgeTimeoutPause(run.emitStatus, func() (*PreludeResult, error) {
		return p.Invoke(call, params)
	})
	if err != nil {
		if run.emitStatus != nil {
			run.emitStatus(EvalStatusEvent{"op": req.Name, "error": err.Error()})
		}
		return nil, err
	}
	if res == nil {
		res = &PreludeResult{}
	}
	if run.emitStatus != nil && p.Status != nil {
		if detail := p.Status(params, res); detail != "" {
			run.emitStatus(EvalStatusEvent{"op": req.Name, "detail": detail})
		}
	}
	return bridgeValueFromPreludeResult(res), nil
}

func bridgeValueFromPreludeResult(res *PreludeResult) any {
	if res.Details == nil && len(res.Images) == 0 {
		return res.Text
	}
	out := map[string]any{
		"text": res.Text,
	}
	if res.Details != nil {
		out["details"] = res.Details
	}
	if len(res.Images) > 0 {
		imgs := make([]map[string]any, 0, len(res.Images))
		for _, img := range res.Images {
			imgs = append(imgs, map[string]any{
				"mimeType": img.MimeType,
				"data":     img.Data,
			})
		}
		out["images"] = imgs
	}
	return out
}

func dispatchSessionToolCall(run *bridgeRun, name string, rawArgs json.RawMessage) (any, error) {
	if !isEvalBridgeToolCallable(name) {
		return nil, fmt.Errorf("`%s` cannot run through the eval bridge; call the direct `%s` tool.", name, name)
	}

	var args any
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments for tool %s: %w", name, err)
		}
	}
	// FREEFORM tools (e.g. edit) accept raw string as {input: ...}.
	if s, ok := args.(string); ok {
		args = map[string]any{"input": s}
	}
	argsMap, ok := args.(map[string]any)
	if !ok || argsMap == nil {
		argsMap = map[string]any{}
	}

	// Guard against re-entrant `tool.eval` targeting the same kernel currently executing,
	// which would otherwise deadlock waiting on slot.mu while the outer cell's watchdog is paused.
	if name == "eval" {
		if targetLang, _ := argsMap["language"].(string); targetLang == run.language {
			err := fmt.Errorf("cannot invoke nested tool.eval for the same language (%s) while a cell is already running on this kernel", targetLang)
			if run.emitStatus != nil {
				run.emitStatus(EvalStatusEvent{"op": name, "error": err.Error()})
			}
			return nil, err
		}
	}

	type toolExecOutcome struct {
		result     sdk.AgentToolResult
		outcomeErr bool
	}
	out, err := withBridgeTimeoutPause(run.emitStatus, func() (toolExecOutcome, error) {
		if run.toolFallback != nil {
			res, ferr := run.toolFallback(run.ctx, name, argsMap)
			return toolExecOutcome{result: res, outcomeErr: res.IsError}, ferr
		}
		outcome, execErr := run.sdkCtx.ExecuteTool(name, argsMap, &sdk.ExecuteToolOptions{
			Signal: run.ctx,
		})
		if execErr != nil {
			return toolExecOutcome{}, execErr
		}
		return toolExecOutcome{result: outcome.Result, outcomeErr: outcome.IsError || outcome.Result.IsError}, nil
	})
	if err != nil {
		if run.emitStatus != nil {
			run.emitStatus(EvalStatusEvent{"op": name, "error": err.Error()})
		}
		return nil, err
	}

	// Upstream js/tool-bridge.ts:97-105, 353-366, 428-434 throws on unknown tool,
	// validation failure, permission denial, or thrown tool error so the cell gets
	// an exception. PiG's ExecuteTool folds all of those into outcome.IsError; raise
	// when outcomeErr is set unless details.isError marks a structured non-throwing
	// error result (like bash exit code / eval cell failure).
	detailsIsError := detailsHasError(out.result.Details)
	if out.outcomeErr && !detailsIsError {
		msg := strings.TrimSpace(out.result.Text())
		if msg == "" {
			msg = fmt.Sprintf("tool %s failed", name)
		}
		if run.emitStatus != nil {
			run.emitStatus(EvalStatusEvent{"op": name, "error": truncateStr(msg, 500)})
		}
		return nil, errors.New(msg)
	}
	return bridgeValueFromToolResult(name, argsMap, out.result, detailsIsError, run.emitStatus), nil
}

func detailsHasError(details any) bool {
	if details == nil {
		return false
	}
	switch d := details.(type) {
	case map[string]any:
		b, _ := d["isError"].(bool)
		return b
	case *sdk.OrderedObject:
		if d == nil {
			return false
		}
		v, ok := d.Get("isError")
		if !ok {
			return false
		}
		b, _ := v.(bool)
		return b
	}
	return false
}

func bridgeValueFromToolResult(name string, args map[string]any, result sdk.AgentToolResult, hasError bool, emitStatus func(EvalStatusEvent)) any {
	var textBuf strings.Builder
	var imgs []map[string]any
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			textBuf.WriteString(block.Text)
		case "image":
			if block.MimeType != "" && block.Data != "" {
				imgs = append(imgs, map[string]any{
					"mimeType": block.MimeType,
					"data":     block.Data,
				})
			}
		}
	}
	text := textBuf.String()
	if emitStatus != nil {
		if ev := summarizeToolStatus(name, args, result, text, hasError); ev != nil {
			emitStatus(ev)
		}
	}
	if result.Details == nil && len(imgs) == 0 && !hasError {
		return text
	}
	out := map[string]any{
		"text": text,
	}
	if result.Details != nil {
		out["details"] = result.Details
	}
	if len(imgs) > 0 {
		out["images"] = imgs
	}
	if hasError {
		out["hasError"] = true
	}
	return out
}

func summarizeToolStatus(name string, args map[string]any, result sdk.AgentToolResult, text string, hasError bool) EvalStatusEvent {
	ev := EvalStatusEvent{"op": name}
	switch name {
	case "read":
		ev["path"] = args["path"]
		ev["chars"] = len(text)
		ev["preview"] = truncateStr(text, 500)
	case "write":
		ev["path"] = args["path"]
		if c, ok := args["content"].(string); ok {
			ev["chars"] = len(c)
		} else {
			ev["chars"] = 0
		}
	case "grep":
		ev["pattern"] = args["pattern"]
		ev["path"] = args["path"]
	case "glob":
		ev["pattern"] = args["pattern"]
	case "bash":
		ev["op"] = "run"
		ev["cmd"] = args["command"]
		ev["output"] = truncateStr(text, 500)
	default:
		ev["chars"] = len(text)
	}
	if hasError {
		ev["hasError"] = true
		ev["error"] = truncateStr(text, 500)
	}
	return ev
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// dispatchShellCall executes `sh(command, cwd?, timeout?)` directly without auto-backgrounding.
// upstream: packages/coding-agent/src/eval/shell-bridge.ts:57
func dispatchShellCall(run *bridgeRun, rawArgs json.RawMessage) (any, error) {
	var rawMap map[string]any
	if err := json.Unmarshal(rawArgs, &rawMap); err != nil || rawMap == nil {
		return nil, errors.New("sh() expects a command string")
	}
	command, ok := rawMap["command"].(string)
	if !ok {
		return nil, errors.New("sh() expects a command string")
	}
	var cwdArg string
	if rawCwd, exists := rawMap["cwd"]; exists && rawCwd != nil {
		s, ok := rawCwd.(string)
		if !ok {
			return nil, errors.New("sh() cwd must be a string")
		}
		cwdArg = s
	}
	var timeoutSec *float64
	if rawTimeout, exists := rawMap["timeout"]; exists && rawTimeout != nil {
		n, ok := rawTimeout.(float64)
		if !ok || n < 0 {
			return nil, errors.New("sh() timeout must be a non-negative number of seconds")
		}
		timeoutSec = &n
	}

	cmdCwd := run.cwd
	if cwdArg != "" {
		c := expandHome(cwdArg)
		if filepath.IsAbs(c) {
			cmdCwd = filepath.Clean(c)
		} else {
			cmdCwd = filepath.Join(run.cwd, c)
		}
	}

	ctx := run.ctx
	var cancel context.CancelFunc
	var timedOut bool
	if timeoutSec != nil && *timeoutSec > 0 {
		ctx, cancel = context.WithTimeout(run.ctx, time.Duration(*timeoutSec*float64(time.Second)))
		defer cancel()
	}

	shellBin, shellArgs := defaultShellCommand(command)
	cmd := exec.Command(shellBin, shellArgs...)
	cmd.Dir = cmdCwd
	cmd.WaitDelay = 500 * time.Millisecond
	configureProcessGroup(cmd)

	var outBuf boundedBuffer
	outBuf.limit = maxShellOutputChars
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("sh() failed to start: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		if run.ctx.Err() == nil && ctx.Err() == context.DeadlineExceeded {
			timedOut = true
		}
		killProcessTreeGraceful(cmd.Process, func() <-chan struct{} {
			c := make(chan struct{})
			go func() {
				waitErr = <-done
				close(c)
			}()
			return c
		}(), 200*time.Millisecond)
	}

	output := sanitizeText(outBuf.String())
	if outBuf.truncated {
		output += fmt.Sprintf("\n[output truncated: sh() keeps the first %d characters]\n", maxShellOutputChars)
	}
	if timedOut {
		if run.emitStatus != nil {
			run.emitStatus(EvalStatusEvent{
				"op":       "run",
				"cmd":      command,
				"output":   truncateStr(output, 500),
				"hasError": true,
				"error":    "timed out",
			})
		}
		return map[string]any{"output": output, "timedOut": true}, nil
	}
	if run.ctx.Err() != nil {
		return nil, errors.New("sh() command was cancelled")
	}
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	if run.emitStatus != nil {
		run.emitStatus(EvalStatusEvent{
			"op":     "run",
			"cmd":    command,
			"code":   exitCode,
			"output": truncateStr(output, 500),
		})
	}
	return map[string]any{"output": output, "exitCode": exitCode}, nil
}

type boundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return b.buf.Write(p)
	}
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		_, _ = b.buf.Write(p[:room])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type handleRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// jobSnapshot converts a jobs.Job (and optional jobs.JobDetailer) to omp's
// EvalHandleSnapshot shape, preserving structured evalResult.data when present.
// upstream: packages/coding-agent/src/eval/handle-bridge.ts:90-106
func jobSnapshot(ref handleRef, j jobs.Job) map[string]any {
	status := j.Status()
	out := map[string]any{
		"kind":   ref.Kind,
		"id":     ref.ID,
		"status": status,
	}
	var detailer jobs.JobDetailer
	if d, ok := j.(jobs.JobDetailer); ok {
		detailer = d
	}
	switch status {
	case "failed":
		msg := ""
		if detailer != nil {
			msg = detailer.ErrorText()
		}
		if msg == "" {
			msg = j.Result()
		}
		if msg == "" {
			msg = "Agent failed"
		}
		out["error"] = msg
	case "cancelled":
		msg := ""
		if detailer != nil {
			msg = detailer.ErrorText()
		}
		if msg == "" {
			msg = j.Result()
		}
		if msg == "" {
			msg = "Agent cancelled"
		}
		out["error"] = msg
	case "completed":
		out["text"] = j.Result()
		if detailer != nil {
			if det := detailer.Details(); det != nil {
				if ev, ok := det["evalResult"].(map[string]any); ok {
					if t, ok := ev["text"].(string); ok {
						out["text"] = t
					}
					if data, exists := ev["data"]; exists {
						out["data"] = data
					}
				}
			}
		}
	}
	return out
}

func dispatchWaitCall(run *bridgeRun, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Items     []handleRef `json:"items"`
		TimeoutMs *float64    `json:"timeoutMs"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil || args.Items == nil {
		return nil, errors.New("wait() requires an items array")
	}
	resolved := make([]jobs.Job, len(args.Items))
	for i, item := range args.Items {
		if item.Kind != "agent" {
			return nil, fmt.Errorf("unknown %s handle %s; result expired", item.Kind, item.ID)
		}
		j, ok := jobs.Get(run.sessionID, item.ID)
		if !ok {
			return nil, fmt.Errorf("unknown agent handle %s; result expired — read agent://%s", item.ID, item.ID)
		}
		resolved[i] = j
	}
	return withBridgeTimeoutPause(run.emitStatus, func() (any, error) {
		ctx := run.ctx
		var cancel context.CancelFunc
		if args.TimeoutMs != nil && *args.TimeoutMs >= 0 {
			ctx, cancel = context.WithTimeout(run.ctx, time.Duration(*args.TimeoutMs)*time.Millisecond)
			defer cancel()
		}
		for _, j := range resolved {
			select {
			case <-j.Done():
			case <-ctx.Done():
				if run.ctx.Err() != nil {
					return nil, errors.New("wait() aborted")
				}
				// Timed out: return current snapshots (running ones trigger TimeoutError in prelude).
				goto done
			}
		}
	done:
		snaps := make([]map[string]any, len(args.Items))
		for i, item := range args.Items {
			snaps[i] = jobSnapshot(item, resolved[i])
		}
		return map[string]any{"items": snaps}, nil
	})
}

func dispatchStatusCall(run *bridgeRun, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Item handleRef `json:"item"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil || args.Item.ID == "" {
		return nil, errors.New("handle request requires an item")
	}
	if args.Item.Kind != "agent" {
		return nil, fmt.Errorf("unknown %s handle %s; result expired", args.Item.Kind, args.Item.ID)
	}
	j, ok := jobs.Get(run.sessionID, args.Item.ID)
	if !ok {
		return nil, fmt.Errorf("unknown agent handle %s; result expired — read agent://%s", args.Item.ID, args.Item.ID)
	}
	return jobSnapshot(args.Item, j), nil
}

func dispatchCancelCall(run *bridgeRun, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Item handleRef `json:"item"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil || args.Item.ID == "" {
		return nil, errors.New("handle request requires an item")
	}
	if args.Item.Kind != "agent" {
		return nil, fmt.Errorf("unknown %s handle %s; result expired", args.Item.Kind, args.Item.ID)
	}
	j, ok := jobs.Get(run.sessionID, args.Item.ID)
	if !ok {
		return nil, fmt.Errorf("unknown agent handle %s; result expired — read agent://%s", args.Item.ID, args.Item.ID)
	}
	if j.Status() != "running" {
		return map[string]any{"cancelled": false}, nil
	}
	j.Cancel()
	return map[string]any{"cancelled": true}, nil
}

func newRandomID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// newUUIDv4 generates an RFC-4122 version 4 UUID string, matching crypto.randomUUID().
func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
