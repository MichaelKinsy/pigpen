package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The original's extension config (<agent dir>/extensions/subagent/config.json,
// src/extension/config.ts). Only the keys this port runs are read; a config the
// port cannot parse keeps the built-in defaults, as the original's loadConfig
// does for a validation failure it does not own.

const (
	waitToolEnabledEnv    = "PI_SUBAGENT_WAIT_TOOL_ENABLED"
	waitToolTimeoutEnv    = "PI_SUBAGENT_WAIT_TOOL_DEFAULT_TIMEOUT_MS"
	defaultAsyncTimeoutMs = int64(30 * 60 * 1000) // the original's DEFAULT_ASYNC_TIMEOUT_MS
)

// subagentConfig is the subset of the original's ExtensionConfig this port reads.
type subagentConfig struct {
	AsyncByDefault            *bool           `json:"asyncByDefault"`
	TimeoutMs                 *int64          `json:"timeoutMs"`
	MaxActiveAsyncRunsPerSess *int64          `json:"maxActiveAsyncRunsPerSession"`
	WaitTool                  json.RawMessage `json:"waitTool"` // bool or {enabled, defaultTimeoutMs}
	DefaultSubagentContext    *string         `json:"defaultSubagentContext"`
}

// configPath is the original's getConfigPath.
func configPath() string {
	return filepath.Join(agentDir(), "extensions", "subagent", "config.json")
}

// loadSubagentConfig reads the config file; an unreadable or unparseable file
// keeps the defaults, as in the original.
func loadSubagentConfig() subagentConfig {
	var cfg subagentConfig
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return subagentConfig{}
	}
	return cfg
}

// resolveAsyncByDefault is the original's: async unless the config says false.
func resolveAsyncByDefault(cfg subagentConfig) bool {
	return cfg.AsyncByDefault == nil || *cfg.AsyncByDefault
}

// resolveMaxActiveAsyncRunsPerSession is the original's: a non-negative integer,
// 0 meaning unlimited; anything else leaves the limit unset.
func resolveMaxActiveAsyncRunsPerSession(value *int64) int {
	if value == nil || *value < 0 {
		return 0
	}
	if *value == 0 {
		return 0
	}
	return int(*value)
}

// waitToolConfig is the original's ResolvedWaitToolConfig.
type waitToolConfig struct {
	Enabled        bool
	DefaultTimeout int64 // 0 when unset
}

// resolveWaitToolConfig is the original's (runs/background/wait-config.ts):
// the environment overrides the config, enabled defaults to true.
func resolveWaitToolConfig(cfg subagentConfig, getenv func(string) string) waitToolConfig {
	out := waitToolConfig{Enabled: true}
	var enabled *bool
	var configuredTimeout int64
	if len(cfg.WaitTool) > 0 {
		var asBool bool
		if err := json.Unmarshal(cfg.WaitTool, &asBool); err == nil {
			enabled = &asBool
		} else {
			var obj struct {
				Enabled          *bool  `json:"enabled"`
				DefaultTimeoutMs *int64 `json:"defaultTimeoutMs"`
			}
			if err := json.Unmarshal(cfg.WaitTool, &obj); err == nil {
				enabled = obj.Enabled
				if obj.DefaultTimeoutMs != nil && *obj.DefaultTimeoutMs > 0 {
					configuredTimeout = *obj.DefaultTimeoutMs
				}
			}
		}
	}
	if env := envBool(getenv(waitToolEnabledEnv)); env != nil {
		out.Enabled = *env
	} else if enabled != nil {
		out.Enabled = *enabled
	}
	if t, ok := envPositiveInt(getenv(waitToolTimeoutEnv)); ok {
		out.DefaultTimeout = t
	} else {
		out.DefaultTimeout = configuredTimeout
	}
	return out
}

// envBool reads the original's TRUE_VALUES / FALSE_VALUES sets.
func envBool(v string) *bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "enabled":
		t := true
		return &t
	case "0", "false", "no", "off", "disabled":
		f := false
		return &f
	}
	return nil
}

func envPositiveInt(v string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	return n, err == nil && n >= 1
}

// resolveRunTimeoutMs is the single-run default the original applies
// (config timeoutMs, else DEFAULT_ASYNC_TIMEOUT_MS).
func resolveRunTimeoutMs(cfg subagentConfig) int64 {
	if cfg.TimeoutMs != nil && *cfg.TimeoutMs > 0 {
		return *cfg.TimeoutMs
	}
	return defaultAsyncTimeoutMs
}
