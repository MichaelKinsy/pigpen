// Package terminal implements Hermes Agent's terminal and process_manage tools
// for pig-hermes using pure Go stdlib and the PiG v0.8.0 SDK.
package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
)

const (
	defaultTimeoutSec  = 180
	maxForegroundSec   = 600
	defaultOutputBytes = 50000
	defaultOutputLines = 2000
	cwdSentinelPrefix  = "__PIG_HERMES_CWD__:"
)

type state struct {
	cfg      *config.Config
	registry *processRegistry

	mu  sync.RWMutex
	cwd string
}

func newState(cfg *config.Config) *state {
	if cfg == nil {
		cfg = config.Default()
	}
	return &state{
		cfg:      cfg,
		registry: newProcessRegistry(),
	}
}

func (s *state) maxOutputBytes() int {
	if s.cfg != nil && s.cfg.MaxOutputBytes > 0 {
		return s.cfg.MaxOutputBytes
	}
	return defaultOutputBytes
}

func (s *state) maxOutputLines() int {
	if s.cfg != nil && s.cfg.MaxOutputLines > 0 {
		return s.cfg.MaxOutputLines
	}
	return defaultOutputLines
}

func (s *state) resolveWorkdir(ctx sdk.Context, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		dir := strings.TrimSpace(explicit)
		if dir == "~" || strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, "~\\") {
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				if dir == "~" {
					dir = home
				} else {
					dir = filepath.Join(home, dir[2:])
				}
			}
		}
		if !filepath.IsAbs(dir) {
			base := s.currentCwd(ctx)
			if base != "" {
				dir = filepath.Join(base, dir)
			}
		}
		dir = filepath.Clean(dir)
		info, err := os.Stat(dir)
		if err != nil {
			return "", fmt.Errorf("invalid workdir %q: %w", explicit, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("workdir %q is not a directory", explicit)
		}
		return dir, nil
	}
	return s.currentCwd(ctx), nil
}

func (s *state) currentCwd(ctx sdk.Context) string {
	s.mu.RLock()
	cur := s.cwd
	s.mu.RUnlock()
	if cur != "" {
		if info, err := os.Stat(cur); err == nil && info.IsDir() {
			return cur
		}
	}
	if ctxCwd := safeCwd(ctx); ctxCwd != "" {
		return ctxCwd
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func (s *state) setCwd(dir string) {
	if dir == "" {
		return
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		s.mu.Lock()
		s.cwd = filepath.Clean(dir)
		s.mu.Unlock()
	}
}

// Register registers terminal and process_manage tools and hooks session shutdown cleanup.
func Register(ext *sdk.Extension, cfg *config.Config) {
	st := newState(cfg)
	ext.RegisterTool(st.terminalToolDef())
	ext.RegisterTool(st.processManageToolDef())
	ext.OnSessionShutdown(func(ctx sdk.Context, data map[string]any) (any, error) {
		st.registry.shutdownAll()
		return nil, nil
	})
}

func (s *state) terminalToolDef() sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name:        "terminal",
		Label:       "Terminal",
		Description: "Execute a shell command with persistent working directory across calls. Supports foreground execution (default 180s timeout, max 600s) and background execution tracked via process_manage.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute.",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Working directory for the command (defaults to persistent session cwd).",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Foreground timeout in seconds (default 180, max 600; >600 auto-promotes to background).",
					"default":     defaultTimeoutSec,
				},
				"background": map[string]any{
					"type":        "boolean",
					"description": "Run command in background and return a session_id for process_manage.",
					"default":     false,
				},
				"check_interval": map[string]any{
					"type":        "integer",
					"description": "Optional polling hint in seconds for background commands.",
				},
			},
			"required": []string{"command"},
		},
		Execute: s.executeTerminal,
	}
}

func (s *state) executeTerminal(ctx sdk.Context, params map[string]any) (any, error) {
	command, ok := paramString(params, "command")
	if !ok || strings.TrimSpace(command) == "" {
		if _, hasCode := params["code"]; hasCode {
			return errResult("terminal received 'code' instead of 'command'; use execute_code for Python or pass 'command' for shell"), nil
		}
		return errResult("terminal: missing required field 'command'"), nil
	}

	if err := s.guardCommand(ctx, command); err != nil {
		return errResult(fmt.Sprintf("terminal: %v", err)), nil
	}

	explicitWorkdir, _ := paramString(params, "workdir")
	workdir, err := s.resolveWorkdir(ctx, explicitWorkdir)
	if err != nil {
		return errResult(fmt.Sprintf("terminal: %v", err)), nil
	}

	background := paramBool(params, "background", false)
	timeoutSec := paramInt(params, "timeout", defaultTimeoutSec)
	if timeoutSec <= 0 {
		timeoutSec = defaultTimeoutSec
	}
	autoPromoted := false
	autoPromotedReason := ""
	if !background && timeoutSec > maxForegroundSec {
		background = true
		autoPromoted = true
		autoPromotedReason = fmt.Sprintf("timeout %ds exceeded foreground cap (%ds); started in background", timeoutSec, maxForegroundSec)
	}
	if !background {
		if stripped, ok := stripTrailingBackgroundAmp(command); ok {
			background = true
			autoPromoted = true
			autoPromotedReason = "trailing '&' detected; started in background"
			command = stripped
		}
	}

	if background {
		mp, err := s.registry.start(command, workdir, os.Environ())
		if err != nil {
			return errResult(fmt.Sprintf("terminal: %v", err)), nil
		}
		res := mp.summary()
		if autoPromoted {
			res["auto_promoted"] = true
			res["note"] = autoPromotedReason
		}
		if checkInterval := paramInt(params, "check_interval", 0); checkInterval > 0 {
			res["check_interval"] = checkInterval
		}
		payload, _ := json.Marshal(res)
		return okResult(string(payload), res), nil
	}

	return s.runForeground(ctx, command, workdir, time.Duration(timeoutSec)*time.Second, explicitWorkdir == "")
}

// stripTrailingBackgroundAmp detects an unquoted trailing background '&'
// (ignoring '&&', fd redirects like '2>&1', '&>', '|&', escaped '\&', or quoted '&')
// and returns the command with the trailing '&' stripped.
func stripTrailingBackgroundAmp(command string) (string, bool) {
	trimmed := strings.TrimRight(command, " \t\r\n")
	if trimmed == "" || !strings.HasSuffix(trimmed, "&") {
		return "", false
	}

	// Walk command tracking quotes and escapes to verify the final '&' is unquoted
	// and not escaped.
	var inSingle, inDouble, escaped bool
	lastIdx := len(trimmed) - 1
	for i := range lastIdx {
		ch := trimmed[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
	}
	if inSingle || inDouble || escaped {
		return "", false
	}

	if lastIdx > 0 {
		prev := trimmed[lastIdx-1]
		// Reject &&, >& (e.g. 2>&1, >&), <& , |&
		if prev == '&' || prev == '>' || prev == '<' || prev == '|' {
			return "", false
		}
	}

	stripped := strings.TrimRight(trimmed[:lastIdx], " \t\r\n")
	if stripped == "" {
		return "", false
	}
	return stripped, true
}

func (s *state) guardCommand(ctx sdk.Context, command string) error {
	reason, hardline := checkDangerousCommand(command)
	if reason == "" {
		return nil
	}
	if hardline {
		return fmt.Errorf("blocked hardline dangerous command (%s)", reason)
	}
	if s.cfg != nil && s.cfg.IsYolo() {
		return nil
	}
	if safeHasUI(ctx) {
		confirmed, err := ctx.Confirm(
			"Confirm dangerous command",
			fmt.Sprintf("Allow command (%s)?\n\n%s", reason, command),
		)
		if err != nil {
			return fmt.Errorf("dangerous command (%s): confirmation failed: %v", reason, err)
		}
		if ok, _ := confirmed.(bool); ok {
			return nil
		}
		return fmt.Errorf("dangerous command (%s) denied by user", reason)
	}
	return fmt.Errorf("blocked dangerous command (%s); enable yolo mode or interactive approval to run", reason)
}

func (s *state) runForeground(ctx sdk.Context, command, workdir string, timeout time.Duration, trackCwd bool) (sdk.ToolResult, error) {
	runCmd := command
	if trackCwd && runtime.GOOS != "windows" {
		// Preserve exit code while capturing final working directory.
		runCmd = fmt.Sprintf(
			"{\n%s\n}\n__pig_rc=$?\nprintf '\\n%s%%s\\n' \"$PWD\"\nexit $__pig_rc",
			command, cwdSentinelPrefix,
		)
	}

	shell, args := shellCommand(runCmd)
	execCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Propagate tool cancellation from PiG SDK context.
	if doneCh := safeDone(ctx); doneCh != nil {
		go func() {
			select {
			case <-doneCh:
				cancel()
			case <-execCtx.Done():
			}
		}()
	}

	cmd := exec.Command(shell, args...)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	cmd.WaitDelay = 2 * time.Second
	setProcGroup(cmd)
	var combined syncBuffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	if err := cmd.Start(); err != nil {
		return errResult(fmt.Sprintf("terminal: failed to start command: %v", err)), nil
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var waitErr error
	timedOut := false
	select {
	case waitErr = <-waitDone:
	case <-execCtx.Done():
		timedOut = true
		_ = killProcGroup(cmd)
		select {
		case waitErr = <-waitDone:
		case <-time.After(2 * time.Second):
		}
	}

	rawOutput := combined.String()
	if trackCwd && runtime.GOOS != "windows" {
		var newCwd string
		rawOutput, newCwd = extractCwdSentinel(rawOutput)
		if newCwd != "" {
			s.setCwd(newCwd)
		}
	}

	exitCode := 0
	if timedOut {
		exitCode = 124
	} else if waitErr != nil && !errors.Is(waitErr, exec.ErrWaitDelay) {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	truncatedOut, wasTruncated := truncateOutput(rawOutput, s.maxOutputBytes(), s.maxOutputLines())
	if timedOut {
		if truncatedOut != "" && !strings.HasSuffix(truncatedOut, "\n") {
			truncatedOut += "\n"
		}
		truncatedOut += fmt.Sprintf("[command timed out after %s]", timeout)
	}

	details := map[string]any{
		"exit_code": exitCode,
		"workdir":   s.currentCwd(ctx),
		"timed_out": timedOut,
		"truncated": wasTruncated,
	}

	header := fmt.Sprintf("exit_code=%d", exitCode)
	body := header
	if truncatedOut != "" {
		body = truncatedOut + "\n" + header
	}

	return sdk.ToolResult{
		Content: body,
		Details: details,
		IsError: exitCode != 0,
	}, nil
}

func extractCwdSentinel(out string) (string, string) {
	idx := strings.LastIndex(out, cwdSentinelPrefix)
	if idx < 0 {
		return out, ""
	}
	after := out[idx+len(cwdSentinelPrefix):]
	newCwd := strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])
	cleaned := out[:idx]
	cleaned = strings.TrimSuffix(cleaned, "\n")
	return cleaned, newCwd
}

func truncateOutput(out string, maxBytes, maxLines int) (string, bool) {
	if out == "" {
		return "", false
	}
	truncated := false
	res := out

	if maxLines > 0 {
		lines := strings.Split(res, "\n")
		if len(lines) > maxLines {
			keepHead := maxLines / 2
			keepTail := maxLines - keepHead
			omitted := len(lines) - maxLines
			head := strings.Join(lines[:keepHead], "\n")
			tail := strings.Join(lines[len(lines)-keepTail:], "\n")
			res = fmt.Sprintf("%s\n... [%d lines truncated] ...\n%s", head, omitted, tail)
			truncated = true
		}
	}

	if maxBytes > 0 && len(res) > maxBytes {
		keepHead := maxBytes / 2
		keepTail := maxBytes - keepHead
		omitted := len(res) - maxBytes
		res = fmt.Sprintf("%s\n... [%d bytes truncated] ...\n%s", res[:keepHead], omitted, res[len(res)-keepTail:])
		truncated = true
	}

	return res, truncated
}

func (s *state) processManageToolDef() sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name:        "process_manage",
		Label:       "Process Manage",
		Description: "Manage background processes started by terminal(background=true): list, poll, log, wait, write, submit, close, or kill.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "poll", "log", "wait", "write", "submit", "close", "kill"},
					"description": "Action to perform on background process(es).",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Background process session_id (or unique prefix). Required except for 'list'.",
				},
				"data": map[string]any{
					"type":        "string",
					"description": "Stdin data for 'write' (raw) or 'submit' (appends newline).",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Max seconds to wait for 'wait' action (default 30).",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Log line offset for 'log' action (default: last 200 lines).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum log lines to return for 'log' action (default 200).",
				},
			},
			"required": []string{"action"},
		},
		Execute: s.executeProcessManage,
	}
}

func (s *state) executeProcessManage(ctx sdk.Context, params map[string]any) (any, error) {
	action, _ := paramString(params, "action")
	action = strings.TrimSpace(strings.ToLower(action))
	sessionID, _ := paramString(params, "session_id")

	var (
		res any
		err error
	)

	switch action {
	case "list":
		res = map[string]any{"processes": s.registry.list()}
	case "poll":
		res, err = s.registry.poll(sessionID)
	case "log":
		_, hasOffset := params["offset"]
		offset := paramInt(params, "offset", 0)
		limit := paramInt(params, "limit", defaultLogLimit)
		res, err = s.registry.log(sessionID, offset, limit, hasOffset)
	case "wait":
		timeoutSec := paramInt(params, "timeout", 30)
		res, err = s.registry.wait(sessionID, time.Duration(timeoutSec)*time.Second, safeDone(ctx))
	case "write":
		data, _ := paramString(params, "data")
		res, err = s.registry.writeStdin(sessionID, data, false)
	case "submit":
		data, _ := paramString(params, "data")
		res, err = s.registry.writeStdin(sessionID, data, true)
	case "close":
		res, err = s.registry.closeStdin(sessionID)
	case "kill":
		res, err = s.registry.kill(sessionID)
	default:
		return errResult(fmt.Sprintf("process_manage: unknown action %q (use: list, poll, log, wait, write, submit, close, kill)", action)), nil
	}

	if err != nil {
		return errResult(fmt.Sprintf("process_manage: %v", err)), nil
	}

	// Truncate output field if present and oversized.
	if m, ok := res.(map[string]any); ok {
		if out, ok := m["output"].(string); ok && out != "" {
			trunc, wasTrunc := truncateOutput(out, s.maxOutputBytes(), s.maxOutputLines())
			m["output"] = trunc
			if wasTrunc {
				m["truncated"] = true
			}
		}
	}

	payload, _ := json.Marshal(res)
	return okResult(string(payload), res), nil
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func safeCwd(ctx sdk.Context) string {
	defer func() { _ = recover() }()
	return ctx.Cwd()
}

func safeHasUI(ctx sdk.Context) bool {
	defer func() { _ = recover() }()
	return ctx.HasUI()
}

func safeDone(ctx sdk.Context) <-chan struct{} {
	defer func() { _ = recover() }()
	return ctx.Done()
}

func paramString(params map[string]any, key string) (string, bool) {
	v, ok := params[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func paramInt(params map[string]any, key string, def int) int {
	v, ok := params[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return def
	}
}

func paramBool(params map[string]any, key string, def bool) bool {
	v, ok := params[key]
	if !ok || v == nil {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

func errResult(msg string) sdk.ToolResult {
	return sdk.ToolResult{
		Content: msg,
		IsError: true,
	}
}

func okResult(content string, details any) sdk.ToolResult {
	return sdk.ToolResult{
		Content: content,
		Details: details,
	}
}
