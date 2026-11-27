// Package agents implements Hermes Agent's subagent delegation, todo list,
// interactive clarification, and cron job management tools for pig-hermes.
package agents

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

// Register registers delegate_task, todo_list, clarify, and cronjob_manage tools
// plus lifecycle hooks on ext.
func Register(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config) {
	if ext == nil {
		return
	}
	if cfg == nil {
		cfg = config.Default()
	}

	delegateMgr := NewDelegateManager(cfg)
	registerDelegateTool(ext, delegateMgr)

	registerTodoTool(ext)
	registerClarifyTool(ext)

	cronStore := NewCronStore(p.CronJobsFile)
	registerCronTool(ext, cronStore)
}

func jsonResult(v any, isError bool) sdk.ToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return sdk.ToolResult{
			Content: fmt.Sprintf(`{"error": %q}`, err.Error()),
			IsError: true,
		}
	}
	return sdk.ToolResult{
		Content: string(b),
		Details: v,
		IsError: isError,
	}
}

func errorResult(msg string) sdk.ToolResult {
	return jsonResult(map[string]any{
		"success": false,
		"error":   msg,
	}, true)
}

func paramString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func paramBool(params map[string]any, key string) (bool, bool) {
	if params == nil {
		return false, false
	}
	v, ok := params[key]
	if !ok || v == nil {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func paramInt(params map[string]any, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	v, ok := params[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

func paramStringSlice(params map[string]any, key string) []string {
	if params == nil {
		return nil
	}
	v, ok := params[key]
	if !ok || v == nil {
		return nil
	}
	switch s := v.(type) {
	case []string:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(str); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	}
	return nil
}

func resolveSessionID(ctx sdk.Context) (sid string) {
	defer func() {
		if recover() != nil || strings.TrimSpace(sid) == "" {
			sid = "default"
		}
	}()
	if id, err := ctx.GetSessionID(); err == nil && strings.TrimSpace(id) != "" {
		return id
	}
	return "default"
}
