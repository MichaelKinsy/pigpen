package websearch

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The video extractors start yt-dlp, ffmpeg and ffprobe the way the original does, with Node's
// execFileSync (youtube-extract.ts:157, :176, video-extract.ts:213, :228): no shell, stdout and
// stderr piped, a timeout and (ffmpeg) a stdout limit. They run through os/exec and not through
// Context.Exec because the host's executor cannot tell a program that is not installed from one
// that failed (both are code 1; the original's messages differ), returns stdout as a string (a
// JPEG on ffmpeg's stdout would not survive it) and has no output limit.

const mediaDefaultMaxBuffer = 1024 * 1024 // execFileSync's maxBuffer default

// mediaOptions are the execFileSync options the original sets.
type mediaOptions struct {
	// Timeout kills the child after this long.
	Timeout time.Duration
	// MaxBuffer is the limit of each output stream in bytes; zero is Node's default of 1 MiB.
	MaxBuffer int
}

// execError is what execFileSync throws: an error code (ENOENT, ETIMEDOUT, ENOBUFS, ...), the
// child's stderr and the message. readExecError reads it back.
type execError struct {
	Code    string
	Stderr  string
	Message string
	Killed  bool
}

func (e *execError) Error() string { return e.Message }

// MediaRunner runs one media program and returns its stdout. A failure is an *execError.
type MediaRunner func(ctx context.Context, name string, args []string, o mediaOptions) ([]byte, error)

var (
	mediaRunnerMu sync.Mutex
	mediaRunner   MediaRunner = runMediaProcess
)

// SetMediaRunner replaces the process runner of yt-dlp, ffmpeg and ffprobe (a test seam; nil
// restores os/exec) and returns a restore function.
func SetMediaRunner(r MediaRunner) func() {
	if r == nil {
		r = runMediaProcess
	}
	mediaRunnerMu.Lock()
	prev := mediaRunner
	mediaRunner = r
	mediaRunnerMu.Unlock()
	return func() {
		mediaRunnerMu.Lock()
		mediaRunner = prev
		mediaRunnerMu.Unlock()
	}
}

func runMedia(ctx context.Context, name string, args []string, o mediaOptions) ([]byte, error) {
	mediaRunnerMu.Lock()
	r := mediaRunner
	mediaRunnerMu.Unlock()
	return r(ctx, name, args, o)
}

// capBuffer collects a stream up to a limit and stops the child when the limit is passed
// (spawnSync's ENOBUFS).
type capBuffer struct {
	buf   bytes.Buffer
	limit int
	over  bool
	stop  context.CancelFunc
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); len(p) > room {
		c.over = true
		c.buf.Write(p[:max(room, 0)])
		c.stop()
		return len(p), nil
	}
	return c.buf.Write(p)
}

func runMediaProcess(ctx context.Context, name string, args []string, o mediaOptions) ([]byte, error) {
	limit := o.MaxBuffer
	if limit <= 0 {
		limit = mediaDefaultMaxBuffer
	}
	kctx, stop := context.WithCancel(ctx)
	defer stop()
	tctx := kctx
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		tctx, cancel = context.WithTimeout(kctx, o.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(tctx, name, args...)
	hideWindow(cmd)
	cmd.WaitDelay = 500 * time.Millisecond
	out := &capBuffer{limit: limit, stop: stop}
	errOut := &capBuffer{limit: limit, stop: stop}
	cmd.Stdout, cmd.Stderr = out, errOut
	runErr := cmd.Run()
	stderr := strings.ToValidUTF8(errOut.buf.String(), "\uFFFD")
	spawn := func(code string, killed bool) *execError {
		return &execError{Code: code, Stderr: stderr, Message: "spawnSync " + name + " " + code, Killed: killed}
	}
	switch {
	case out.over || errOut.over:
		return nil, spawn("ENOBUFS", false)
	case ctx.Err() != nil:
		return nil, &execError{Code: "ABORT_ERR", Stderr: stderr, Message: "The operation was aborted"}
	case o.Timeout > 0 && errors.Is(tctx.Err(), context.DeadlineExceeded):
		return nil, spawn("ETIMEDOUT", true)
	case runErr == nil:
		return out.buf.Bytes(), nil
	case errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist):
		return nil, spawn("ENOENT", false)
	case errors.Is(runErr, fs.ErrPermission):
		return nil, spawn("EACCES", false)
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		msg := "Command failed: " + strings.Join(append([]string{name}, args...), " ")
		if stderr != "" {
			msg += "\n" + stderr
		}
		return nil, &execError{Stderr: stderr, Message: msg}
	}
	return nil, &execError{Message: runErr.Error(), Stderr: stderr}
}

// readExecError is utils.ts readExecError.
func readExecError(err error) (code, stderr, message string) {
	var e *execError
	if errors.As(err, &e) {
		return e.Code, e.Stderr, e.Message
	}
	return "", "", err.Error()
}

// isTimeoutError is utils.ts isTimeoutError.
func isTimeoutError(err error) bool {
	var e *execError
	if errors.As(err, &e) {
		return e.Killed || e.Code == "ETIMEDOUT" || e.Code == "ABORT_ERR" || strings.Contains(strings.ToLower(e.Message), "timed out")
	}
	return strings.Contains(strings.ToLower(err.Error()), "timed out")
}

// collapseJSSpace replaces each run of whitespace by one space (replace(/\s+/g, " ")).
func collapseJSSpace(text string) string {
	var b strings.Builder
	space := false
	for _, r := range text {
		if isJSSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// trimErrorText is utils.ts trimErrorText: whitespace collapsed, the first 200 UTF-16 units.
func trimErrorText(text string) string {
	return jsSlice(jsTrim(collapseJSSpace(text)), 0, 200)
}

// mapFfmpegError is utils.ts mapFfmpegError.
func mapFfmpegError(err error) string {
	code, stderr, message := readExecError(err)
	switch {
	case code == "ENOENT":
		return "ffmpeg is not installed. Install with: brew install ffmpeg"
	case isTimeoutError(err):
		return "ffmpeg timed out extracting frame"
	case strings.Contains(stderr, "403"):
		return "Stream URL returned 403 — may have expired, try again"
	}
	return failureWithSnippet("ffmpeg", stderr, message)
}

// failureWithSnippet is `<tool> failed: <snippet>`, or `<tool> failed` when there is nothing to quote.
func failureWithSnippet(tool, stderr, message string) string {
	text := stderr
	if text == "" {
		text = message
	}
	if snippet := trimErrorText(text); snippet != "" {
		return tool + " failed: " + snippet
	}
	return tool + " failed"
}

// jsNumberString is String(n) for a number.
func jsNumberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	abs := math.Abs(f)
	if abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		mant, exp, _ := strings.Cut(s, "e")
		sign := exp[0]
		digits := strings.TrimLeft(exp[1:], "0")
		return mant + "e" + string(sign) + digits
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
