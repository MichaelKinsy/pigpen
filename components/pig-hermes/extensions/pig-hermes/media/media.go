// Package media registers Hermes progressive-disclosure tool bridge,
// browser automation, and multimodal/media tools for pig-hermes.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

const defaultMaxOutputBytes = 50000

// Register attaches the 7 Wave-3 Hermes tools:
// tool_search, tool_describe, tool_call, browser_exec,
// vision_analyze, image_generate, and text_to_speech.
func Register(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config) {
	if ext == nil {
		return
	}
	if cfg == nil {
		cfg = config.Default()
	}

	registerToolSearchTools(ext)
	registerBrowserExecTool(ext, cfg)
	registerVisionAnalyzeTool(ext, cfg)
	registerImageGenerateTool(ext, p, cfg)
	registerTextToSpeechTool(ext, p, cfg)
}

// contextFromSDK derives a standard context.Context with timeout that also
// cancels when the PiG SDK request context is cancelled.
func contextFromSDK(sdkCtx sdk.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	base, cancel := context.WithTimeout(context.Background(), timeout)
	done := sdkCtx.Done()
	if done == nil {
		return base, cancel
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-done:
			cancel()
		case <-base.Done():
		case <-stop:
		}
	}()
	return base, func() {
		close(stop)
		cancel()
	}
}

func stringParam(params map[string]any, key string) string {
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func intParam(params map[string]any, key string, def int) (int, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return def, nil
	}
	switch t := v.(type) {
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		return int(t), nil
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return def, fmt.Errorf("invalid integer for %s: %w", key, err)
		}
		return int(n), nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return def, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return def, fmt.Errorf("invalid integer for %s: %w", key, err)
		}
		return n, nil
	default:
		return def, fmt.Errorf("invalid integer for %s", key)
	}
}

func stringSliceParam(params map[string]any, key string) ([]string, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return nil, nil
	}
	switch t := v.(type) {
	case []string:
		out := make([]string, 0, len(t))
		for _, s := range t {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s entries must be strings", key)
			}
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out, nil
	case string:
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			return []string{trimmed}, nil
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}

func toolErrorResult(msg string) sdk.ToolResult {
	return sdk.ToolResult{
		Content: msg,
		IsError: true,
	}
}

func maxOutputBytes(cfg *config.Config) int {
	if cfg != nil && cfg.MaxOutputBytes > 0 {
		return cfg.MaxOutputBytes
	}
	return defaultMaxOutputBytes
}

func jsonToolResult(v any, isError bool) sdk.ToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolErrorResult(fmt.Sprintf("marshal result: %v", err))
	}
	return sdk.ToolResult{
		Content:           string(data),
		StructuredContent: v,
		IsError:           isError,
	}
}

// apiEndpointURL joins an OpenAI-compatible base URL with a v1 path such as
// "/v1/chat/completions" without duplicating "/v1".
func apiEndpointURL(baseURL, v1Path string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://api.openai.com"
	}
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(v1Path, "/v1/") {
		return base + strings.TrimPrefix(v1Path, "/v1")
	}
	return base + v1Path
}

// expandHome expands a leading "~" or "~/" path prefix.
func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if len(path) > 1 && (path[1] == '/' || path[1] == filepath.Separator) {
		return filepath.Join(home, path[2:])
	}
	return path
}

// atomicWriteFile writes data to a temporary file in the destination directory
// and renames it into place atomically.
func atomicWriteFile(dst string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".hermes-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	cleanup = false
	return nil
}

// boundedBuffer caps captured subprocess output while remaining concurrency-safe.
type boundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	if limit <= 0 {
		limit = defaultMaxOutputBytes
	}
	return &boundedBuffer{limit: limit}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rem := b.limit - b.buf.Len()
	if rem <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > rem {
		_, _ = b.buf.Write(p[:rem])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *boundedBuffer) Result() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String(), b.truncated
}
