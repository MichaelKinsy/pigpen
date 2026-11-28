package webmcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	maxSpillBytes         = 32 << 20 // 32 MiB cap for stdout spill file
)

// CodeExecResult is the structured result returned by execute_code.
type CodeExecResult struct {
	Status          string  `json:"status"`
	Language        string  `json:"language"`
	ExitCode        int     `json:"exit_code"`
	Output          string  `json:"output"`
	Stderr          string  `json:"stderr,omitempty"`
	Truncated       bool    `json:"truncated,omitempty"`
	StdoutSpillPath string  `json:"stdout_spill_path,omitempty"`
	SpillCapped     bool    `json:"spill_capped,omitempty"`
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
	cmd.Env = scrubbedExecEnv(os.Environ())
	setProcGroup(cmd)
	cmd.WaitDelay = 2 * time.Second

	stdoutBuf := newSpillingBuffer(maxBytes)
	defer stdoutBuf.Close()
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
	stdoutText, stdoutTrunc, spillPath, spillCapped := stdoutBuf.Result()
	stderrText, stderrTrunc := stderrBuf.Result()
	truncated := stdoutTrunc || stderrTrunc

	result := CodeExecResult{
		Status:          "ok",
		Language:        lang,
		ExitCode:        0,
		Output:          stdoutText,
		Stderr:          stderrText,
		Truncated:       truncated,
		StdoutSpillPath: spillPath,
		SpillCapped:     spillCapped,
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

// scrubbedExecEnv removes sensitive secret environment variables from child subprocesses
// while retaining safe execution vars (PATH, HOME, TMPDIR, LANG, TERM, HERMES_HOME, HERMES_PROFILE, etc.).
func scrubbedExecEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if isSensitiveExecEnvKey(k) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

var safeExecEnvKeep = map[string]bool{
	"PATH":           true,
	"HOME":           true,
	"TMPDIR":         true,
	"TEMP":           true,
	"TMP":            true,
	"LANG":           true,
	"LC_ALL":         true,
	"TERM":           true,
	"HERMES_HOME":    true,
	"HERMES_PROFILE": true,
	"USER":           true,
	"SHELL":          true,
	"PWD":            true,
	"SYSTEMROOT":     true,
	"COMSPEC":        true,
	"PATHEXT":        true,
	"WINDIR":         true,
}

var sensitiveExecEnvSubstrings = []string{
	"KEY",
	"TOKEN",
	"SECRET",
	"PASSWORD",
	"PASSWD",
	"CREDENTIAL",
	"AUTH",
	"DSN",
	"WEBHOOK",
	"CREDS",
	"BEARER",
	"APIKEY",
	"COOKIE",
	"PAT",
	"DATABASE_URL",
}

func isSensitiveExecEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return true
	}
	if safeExecEnvKeep[upper] {
		return false
	}
	for _, sub := range sensitiveExecEnvSubstrings {
		if strings.Contains(upper, sub) {
			return true
		}
	}
	return false
}

// spillingBuffer captures stdout in memory up to maxBytes; once stdout exceeds maxBytes,
// it writes the complete stream to <tmpdir>/hermes-exec-stdout-<shorthex>.txt and retains
// a head/tail preview for the returned JSON payload.
type spillingBuffer struct {
	mu           sync.Mutex
	maxBytes     int
	maxSpill     int64
	buf          bytes.Buffer
	spilled      bool
	spillFile    *os.File
	spillPath    string
	spilledBytes int64
	spillCapped  bool
	head         []byte
	tail         []byte
	tailCap      int
	totalBytes   int64
}

func newSpillingBuffer(maxBytes int) *spillingBuffer {
	return newSpillingBufferWithCap(maxBytes, maxSpillBytes)
}

func newSpillingBufferWithCap(maxBytes int, maxSpill int64) *spillingBuffer {
	if maxBytes <= 0 {
		maxBytes = defaultMaxOutputBytes
	}
	if maxSpill <= 0 {
		maxSpill = maxSpillBytes
	}
	tailCap := maxBytes / 2
	if tailCap < 16 {
		tailCap = 16
	}
	return &spillingBuffer{
		maxBytes: maxBytes,
		maxSpill: maxSpill,
		tailCap:  tailCap,
	}
}

func (s *spillingBuffer) writeSpillLocked(p []byte) {
	if s.spillFile == nil || len(p) == 0 {
		return
	}
	if s.spillCapped {
		return
	}
	remaining := s.maxSpill - s.spilledBytes
	if remaining <= 0 {
		s.spillCapped = true
		return
	}
	if int64(len(p)) > remaining {
		if n, _ := s.spillFile.Write(p[:remaining]); n > 0 {
			s.spilledBytes += int64(n)
		}
		s.spillCapped = true
		return
	}
	if n, _ := s.spillFile.Write(p); n > 0 {
		s.spilledBytes += int64(n)
	}
}

func (s *spillingBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(p)
	s.totalBytes += int64(n)

	if !s.spilled {
		if s.buf.Len()+n <= s.maxBytes {
			_, _ = s.buf.Write(p)
			return n, nil
		}
		// Transition to spill mode.
		s.spilled = true
		existing := s.buf.Bytes()
		headCap := s.maxBytes / 2
		if headCap < 16 {
			headCap = 16
		}
		combined := make([]byte, 0, len(existing)+n)
		combined = append(combined, existing...)
		combined = append(combined, p...)
		if len(combined) <= headCap {
			s.head = append([]byte(nil), combined...)
		} else {
			s.head = append([]byte(nil), combined[:headCap]...)
		}
		s.appendTail(combined)

		spillPath := filepath.Join(os.TempDir(), fmt.Sprintf("hermes-exec-stdout-%s.txt", execShortHex()))
		if f, err := os.OpenFile(spillPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
			s.spillFile = f
			s.spillPath = spillPath
			s.writeSpillLocked(existing)
			s.writeSpillLocked(p)
		}
		s.buf.Reset()
		return n, nil
	}

	s.writeSpillLocked(p)
	s.appendTail(p)
	return n, nil
}

func (s *spillingBuffer) appendTail(p []byte) {
	if s.tailCap <= 0 || len(p) == 0 {
		return
	}
	if len(p) >= s.tailCap {
		s.tail = append(s.tail[:0], p[len(p)-s.tailCap:]...)
		return
	}
	s.tail = append(s.tail, p...)
	if len(s.tail) > s.tailCap {
		s.tail = append([]byte(nil), s.tail[len(s.tail)-s.tailCap:]...)
	}
}

func (s *spillingBuffer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spillFile != nil {
		_ = s.spillFile.Close()
		s.spillFile = nil
	}
}

func (s *spillingBuffer) Result() (output string, truncated bool, spillPath string, spillCapped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spillFile != nil {
		_ = s.spillFile.Close()
		s.spillFile = nil
	}
	if !s.spilled {
		return s.buf.String(), false, "", false
	}
	var b strings.Builder
	b.Write(s.head)
	fmt.Fprintf(&b, "\n... [output truncated] (total %d bytes) ...\n", s.totalBytes)
	b.Write(s.tail)
	return b.String(), true, s.spillPath, s.spillCapped
}

func execShortHex() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}
