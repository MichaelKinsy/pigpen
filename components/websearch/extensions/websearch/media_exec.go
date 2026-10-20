package websearch

import (
	"context"
	"time"
)

// Stub of the process runner of yt-dlp, ffmpeg and ffprobe: the twins fail until it is written.

type mediaOptions struct {
	Timeout   time.Duration
	MaxBuffer int
}

type execError struct {
	Code    string
	Stderr  string
	Message string
	Killed  bool
}

func (e *execError) Error() string { return e.Message }

type MediaRunner func(ctx context.Context, name string, args []string, o mediaOptions) ([]byte, error)

func SetMediaRunner(r MediaRunner) func() { return func() {} }

func runMedia(ctx context.Context, name string, args []string, o mediaOptions) ([]byte, error) {
	return nil, &execError{Message: "not implemented"}
}

func runMediaProcess(ctx context.Context, name string, args []string, o mediaOptions) ([]byte, error) {
	return nil, &execError{Message: "not implemented"}
}

func readExecError(err error) (code, stderr, message string) { return "", "", err.Error() }
func isTimeoutError(err error) bool                          { return false }
func collapseJSSpace(text string) string                     { return text }
func trimErrorText(text string) string                       { return text }
func mapFfmpegError(err error) string                        { return "" }
func failureWithSnippet(tool, stderr, message string) string { return "" }
func jsNumberString(f float64) string                        { return "" }
