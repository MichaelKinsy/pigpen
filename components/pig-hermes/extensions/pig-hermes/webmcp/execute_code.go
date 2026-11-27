package webmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
)

const (
	defaultExecTimeoutSec = 60
	maxExecTimeoutSec     = 300
)

// CodeExecResult is the structured result returned by execute_code.
type CodeExecResult struct {
	Status          string  `json:"status"`
	Language        string  `json:"language"`
	ExitCode        int     `json:"exit_code"`
	Output          string  `json:"output"`
	Stderr          string  `json:"stderr,omitempty"`
	Truncated       bool    `json:"truncated,omitempty"`
	DurationSeconds float64 `json:"duration_seconds"`
	Error           string  `json:"error,omitempty"`
}

func registerExecuteCodeTool(ext *sdk.Extension, cfg *config.Config) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "execute_code",
		Label:         "Execute Code",
		Description:   "Execute a script in an isolated temporary file via python3 (default), bash, or node with strict timeout, output truncation, and clean process-group teardown.",
		PromptSnippet: "Execute Python, Bash, or Node.js scripts in an isolated subprocess",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "Source code to execute.",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Execution language: python (default), bash, or node.",
					"enum":        []string{"python", "bash", "node"},
					"default":     "python",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Timeout in seconds (default 60, max 300).",
					"minimum":     1,
					"maximum":     maxExecTimeoutSec,
					"default":     defaultExecTimeoutSec,
				},
			},
			"required": []string{"code"},
		},
		Execute: func(sdkCtx sdk.Context, params map[string]any) (any, error) {
			timeoutSec, err := intParam(params, "timeout", defaultExecTimeoutSec)
			if err != nil {
				return toolErrorResult(fmt.Sprintf("execute_code: %v", err)), nil
			}
			if timeoutSec <= 0 {
				timeoutSec = defaultExecTimeoutSec
			}
			if timeoutSec > maxExecTimeoutSec {
				timeoutSec = maxExecTimeoutSec
			}
			ctx, cancel := contextFromSDK(sdkCtx, time.Duration(timeoutSec)*time.Second)
			defer cancel()
			return handleExecuteCode(ctx, params, timeoutSec, maxOutputBytes(cfg)), nil
		},
	})
}

func handleExecuteCode(ctx context.Context, params map[string]any, timeoutSec, maxBytes int) sdk.ToolResult {
	code := stringParam(params, "code")
	if strings.TrimSpace(code) == "" {
		return toolErrorResult("execute_code: code is required")
	}

	lang := strings.ToLower(strings.TrimSpace(stringParam(params, "language")))
	if lang == "" {
		lang = "python"
	}

	interp, ext, err := resolveInterpreter(lang)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("execute_code: %v", err))
	}

	if _, lookErr := exec.LookPath(interp); lookErr != nil {
		return toolErrorResult(fmt.Sprintf("execute_code: interpreter %q not found in PATH", interp))
	}

	tmpDir, err := os.MkdirTemp("", "hermes-exec-*")
	if err != nil {
		return toolErrorResult(fmt.Sprintf("execute_code: create temp dir: %v", err))
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "script"+ext)
	if err := os.WriteFile(scriptPath, []byte(code), 0o600); err != nil {
		return toolErrorResult(fmt.Sprintf("execute_code: write script: %v", err))
	}

	start := time.Now()
	cmd := exec.Command(interp, scriptPath)
	cmd.Dir = tmpDir
	setProcGroup(cmd)
	cmd.WaitDelay = 2 * time.Second

	stdoutBuf := newBoundedBuffer(maxBytes)
	stderrBuf := newBoundedBuffer(maxBytes)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return toolErrorResult(fmt.Sprintf("execute_code: start %s: %v", interp, err))
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
		// Reap child process after kill so no zombie remains.
		select {
		case waitErr = <-waitCh:
		case <-time.After(2 * time.Second):
		}
	}

	duration := time.Since(start).Seconds()
	stdoutText, stdoutTrunc := stdoutBuf.Result()
	stderrText, stderrTrunc := stderrBuf.Result()
	truncated := stdoutTrunc || stderrTrunc

	result := CodeExecResult{
		Status:          "ok",
		Language:        lang,
		ExitCode:        0,
		Output:          stdoutText,
		Stderr:          stderrText,
		Truncated:       truncated,
		DurationSeconds: roundSeconds(duration),
	}

	isErr := false
	switch {
	case timedOut:
		result.Status = "timeout"
		result.ExitCode = 124
		result.Error = fmt.Sprintf("execution timed out after %ds", timeoutSec)
		isErr = true
	case cancelled:
		result.Status = "cancelled"
		result.ExitCode = 130
		result.Error = "execution cancelled"
		isErr = true
	case waitErr != nil:
		result.Status = "error"
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
		result.Error = waitErr.Error()
		isErr = true
	}

	encoded, _ := json.Marshal(result)
	return sdk.ToolResult{
		Content:           string(encoded),
		StructuredContent: result,
		Details:           result,
		IsError:           isErr,
	}
}

func resolveInterpreter(lang string) (bin string, fileExt string, err error) {
	switch lang {
	case "python", "py", "python3":
		return "python3", ".py", nil
	case "bash", "sh":
		return "bash", ".sh", nil
	case "node", "js", "javascript":
		return "node", ".js", nil
	default:
		return "", "", fmt.Errorf("unsupported language %q (expected python, bash, or node)", lang)
	}
}

func roundSeconds(s float64) float64 {
	return float64(int(s*100+0.5)) / 100
}

// boundedBuffer captures up to maxBytes of output without unbounded memory growth.
type boundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	maxBytes  int
	truncated bool
}

func newBoundedBuffer(maxBytes int) *boundedBuffer {
	if maxBytes <= 0 {
		maxBytes = defaultMaxOutputBytes
	}
	return &boundedBuffer{maxBytes: maxBytes}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	total := len(p)
	remaining := b.maxBytes - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return total, nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return total, nil
	}
	_, _ = b.buf.Write(p)
	return total, nil
}

func (b *boundedBuffer) Result() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.truncated {
		return b.buf.String() + "\n[output truncated]", true
	}
	return b.buf.String(), false
}
