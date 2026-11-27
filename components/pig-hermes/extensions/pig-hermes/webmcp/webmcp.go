// Package webmcp registers Hermes web_search, web_extract, execute_code tools
// and translates Hermes mcp_servers configuration into PiG's native MCP registry.
package webmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

const defaultMaxOutputBytes = 50000

// Register attaches web_search, web_extract, execute_code tools and registers
// configured Hermes MCP servers with PiG's native MCP host.
func Register(ext *sdk.Extension, _ paths.ProfilePaths, cfg *config.Config) {
	if ext == nil {
		return
	}
	if cfg == nil {
		cfg = config.Default()
	}

	registerWebSearchTool(ext, cfg)
	registerWebExtractTool(ext, cfg)
	registerExecuteCodeTool(ext, cfg)
	_ = RegisterHermesMCPServers(ext, cfg)
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
