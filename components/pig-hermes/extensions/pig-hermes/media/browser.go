package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
)

const (
	defaultBrowserTimeoutSec = 60
	maxBrowserTimeoutSec     = 600
)

var validBrowserSessionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// BrowserExecResult is the structured result returned by browser_exec.
type BrowserExecResult struct {
	Status          string  `json:"status"`
	Command         string  `json:"command"`
	Session         string  `json:"session,omitempty"`
	ExitCode        int     `json:"exit_code"`
	Stdout          string  `json:"stdout"`
	Stderr          string  `json:"stderr,omitempty"`
	Truncated       bool    `json:"truncated,omitempty"`
	DurationSeconds float64 `json:"duration_seconds"`
	Error           string  `json:"error,omitempty"`
}

func registerBrowserExecTool(ext *sdk.Extension, cfg *config.Config) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "browser_exec",
		Label:         "Browser Exec",
		Description:   "Run a browser-use CLI command (e.g. 'open <url>', 'snapshot', 'click <ref>', 'type <ref> <text>', 'screenshot', 'close') with optional named session and clean timeout/process-group cleanup.",
		PromptSnippet: "Drive a browser via the browser-use CLI",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Browser command to execute (e.g. 'open https://example.com', 'snapshot', 'click 3', 'type 4 hello', 'screenshot', 'close').",
				},
				"session": map[string]any{
					"type":        "string",
					"description": "Optional named isolated browser session (alphanumeric, '-', '_').",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Timeout in seconds (default 60, max 600).",
					"minimum":     1,
					"maximum":     maxBrowserTimeoutSec,
					"default":     defaultBrowserTimeoutSec,
				},
			},
			"required": []string{"command"},
		},
		Execute: func(sdkCtx sdk.Context, params map[string]any) (any, error) {
			timeoutSec, err := intParam(params, "timeout", defaultBrowserTimeoutSec)
			if err != nil {
				return toolErrorResult(fmt.Sprintf("browser_exec: %v", err)), nil
			}
			if timeoutSec <= 0 {
				timeoutSec = defaultBrowserTimeoutSec
			}
			if timeoutSec > maxBrowserTimeoutSec {
				timeoutSec = maxBrowserTimeoutSec
			}
			ctx, cancel := contextFromSDK(sdkCtx, time.Duration(timeoutSec)*time.Second)
			defer cancel()
			return handleBrowserExec(ctx, params, cfg, timeoutSec), nil
		},
	})
}

func handleBrowserExec(ctx context.Context, params map[string]any, cfg *config.Config, timeoutSec int) sdk.ToolResult {
	rawCmd := strings.TrimSpace(stringParam(params, "command"))
	if rawCmd == "" {
		return toolErrorResult("browser_exec: command is required")
	}

	session := strings.TrimSpace(stringParam(params, "session"))
	if session != "" && !validBrowserSessionRe.MatchString(session) {
		return toolErrorResult(fmt.Sprintf("browser_exec: invalid session name %q (must match ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$)", session))
	}

	cliName := ""
	if cfg != nil {
		cliName = strings.TrimSpace(cfg.Getenv("HERMES_BROWSER_CLI"))
	}
	if cliName == "" {
		cliName = strings.TrimSpace(os.Getenv("HERMES_BROWSER_CLI"))
	}
	if cliName == "" {
		cliName = "browser-use"
	}

	cliPath, err := exec.LookPath(cliName)
	if err != nil {
		msg := fmt.Sprintf(
			"browser_exec: CLI %q not found on PATH. Install browser-use CLI (e.g. `uv tool install browser-use` or `pip install browser-use`) or set HERMES_BROWSER_CLI to an executable path.",
			cliName,
		)
		res := BrowserExecResult{
			Status:   "unavailable",
			Command:  rawCmd,
			Session:  session,
			ExitCode: -1,
			Error:    msg,
		}
		return jsonToolResult(res, true)
	}

	cmdArgs, err := splitShellArgs(rawCmd)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("browser_exec: parse command: %v", err))
	}
	if len(cmdArgs) == 0 {
		return toolErrorResult("browser_exec: command is required")
	}

	fullArgs := make([]string, 0, len(cmdArgs)+2)
	if session != "" {
		fullArgs = append(fullArgs, "--session", session)
	}
	fullArgs = append(fullArgs, cmdArgs...)

	maxBytes := maxOutputBytes(cfg)
	start := time.Now()
	cmd := exec.Command(cliPath, fullArgs...)
	setProcGroup(cmd)
	cmd.WaitDelay = 2 * time.Second

	if session != "" {
		cmd.Env = append(os.Environ(), "BU_NAME="+session)
	}

	stdoutBuf := newBoundedBuffer(maxBytes)
	stderrBuf := newBoundedBuffer(maxBytes)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return toolErrorResult(fmt.Sprintf("browser_exec: start %s: %v", cliName, err))
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	timedOut := false
	cancelled := false

	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			timedOut = true
		} else {
			cancelled = true
		}
		killProcGroup(cmd)
		select {
		case waitErr = <-waitCh:
		case <-time.After(2 * time.Second):
		}
	}

	duration := time.Since(start).Seconds()
	stdoutText, stdoutTrunc := stdoutBuf.Result()
	stderrText, stderrTrunc := stderrBuf.Result()

	res := BrowserExecResult{
		Status:          "ok",
		Command:         rawCmd,
		Session:         session,
		ExitCode:        0,
		Stdout:          stdoutText,
		Stderr:          stderrText,
		Truncated:       stdoutTrunc || stderrTrunc,
		DurationSeconds: duration,
	}

	isErr := false
	switch {
	case timedOut:
		res.Status = "timeout"
		res.ExitCode = -1
		res.Error = fmt.Sprintf("browser_exec timed out after %ds", timeoutSec)
		isErr = true
	case cancelled:
		res.Status = "cancelled"
		res.ExitCode = -1
		res.Error = "browser_exec cancelled"
		isErr = true
	case waitErr != nil:
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
		}
		res.Status = "error"
		res.Error = waitErr.Error()
		isErr = true
	}

	return jsonToolResult(res, isErr)
}

// splitShellArgs splits a command string into argv tokens respecting single quotes,
// double quotes, and backslash escapes.
func splitShellArgs(input string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inToken := false
	var quote rune
	escaped := false

	for _, r := range input {
		if escaped {
			cur.WriteRune(r)
			inToken = true
			escaped = false
			continue
		}
		if quote != 0 {
			if r == '\\' && quote == '"' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
			inToken = true
			continue
		}
		switch r {
		case '\\':
			escaped = true
			inToken = true
		case '\'', '"':
			quote = r
			inToken = true
		case ' ', '\t', '\n', '\r':
			if inToken {
				args = append(args, cur.String())
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}

	if escaped {
		return nil, fmt.Errorf("dangling backslash escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inToken {
		args = append(args, cur.String())
	}
	return args, nil
}
