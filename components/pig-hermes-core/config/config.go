// Package config parses and writes Hermes config.yaml and .env files using pure Go stdlib.
// Matches hermes_cli/config_defaults.py and hermes_cli/config_env_routing.py.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)
// Config holds the core Hermes configuration keys used by pig-hermes and derived Piglets.
type Config struct {
	Model          string
	Provider       string
	BaseURL        string
	Skin           string
	Toolset        string
	DisabledTools  []string
	MemoryLimit    int
	UserLimit      int
	MaxReadChars   int
	MaxOutputBytes int
	MaxOutputLines int
	MaxLineLength  int
	MaxSpawnDepth  int
	MaxConcurrent  int
	Yolo           bool
	yoloFlag       atomic.Bool
	Raw            map[string]string
	MCPServers     map[string]MCPServerConfig
}

// IsYolo returns the live approval-bypass state (thread-safe).
func (c *Config) IsYolo() bool {
	if c == nil {
		return false
	}
	return c.yoloFlag.Load() || c.Yolo
}

// SetYolo updates the live approval-bypass state (thread-safe).
func (c *Config) SetYolo(v bool) {
	if c == nil {
		return
	}
	c.Yolo = v
	c.yoloFlag.Store(v)
}

// Getenv returns os.Getenv(key), falling back to values loaded from ~/.hermes/.env.
func (c *Config) Getenv(key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	if c != nil && c.Raw != nil {
		return c.Raw["env."+key]
	}
	return ""
}

// MCPServerConfig represents an entry under mcp_servers in ~/.hermes/config.yaml.
type MCPServerConfig struct {
	Name    string
	Command string
	Args    []string
	URL     string
	Env     map[string]string
	Headers map[string]string
	Enabled bool
}

// Default returns Hermes's documented defaults.
func Default() *Config {
	return &Config{
		Skin:           "default",
		Toolset:        "hermes-cli",
		MemoryLimit:    2200,
		UserLimit:      1375,
		MaxReadChars:   100000,
		MaxOutputBytes: 50000,
		MaxOutputLines: 2000,
		MaxLineLength:  2000,
		MaxSpawnDepth:  1,
		MaxConcurrent:  10,
		Raw:            make(map[string]string),
		MCPServers:     make(map[string]MCPServerConfig),
	}
}

// Load reads config.yaml and .env from the given paths (missing files are not errors).
func Load(configFile, envFile string) (*Config, error) {
	cfg := Default()
	if data, err := os.ReadFile(configFile); err == nil {
		parseSimpleYAML(string(data), cfg)
	} else if !os.IsNotExist(err) {
		return cfg, err
	}
	if data, err := os.ReadFile(envFile); err == nil {
		envMap := ParseDotEnv(string(data))
		for k, v := range envMap {
			if _, exists := os.LookupEnv(k); !exists {
				cfg.Raw["env."+k] = v
			}
		}
	} else if !os.IsNotExist(err) {
		return cfg, err
	}
	applyEnvOverrides(cfg)
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("HERMES_MODEL")); v != "" {
		cfg.Model = v
	}
	if v := strings.TrimSpace(os.Getenv("HERMES_SKIN")); v != "" {
		cfg.Skin = v
	}
	if v := strings.TrimSpace(os.Getenv("HERMES_TOOLSET")); v != "" {
		cfg.Toolset = v
	}
	if v := strings.TrimSpace(cfg.Getenv("HERMES_YOLO_MODE")); v == "1" || strings.EqualFold(v, "true") {
		cfg.SetYolo(true)
	}
	if v := strings.TrimSpace(cfg.Getenv("HERMES_YOLO")); v == "1" || strings.EqualFold(v, "true") {
		cfg.SetYolo(true)
	}
}

// ParseDotEnv parses a KEY=VALUE .env file.
func ParseDotEnv(content string) map[string]string {
	out := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// parseSimpleYAML extracts dotted scalar keys and basic mcp_servers blocks without external YAML deps.
func parseSimpleYAML(content string, cfg *Config) {
	var stack []struct {
		indent int
		key    string
	}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		// Handle list items under mcp_servers.<name>.args or disabled_toolsets
		if strings.HasPrefix(trimmed, "- ") {
			item := unquoteYAML(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			prefix := currentPrefix(stack)
			if prefix == "disabled_toolsets" {
				cfg.DisabledTools = append(cfg.DisabledTools, item)
			} else if strings.HasPrefix(prefix, "mcp_servers.") && strings.HasSuffix(prefix, ".args") {
				parts := strings.Split(prefix, ".")
				if len(parts) == 3 {
					srv := ensureMCPServer(cfg, parts[1])
					srv.Args = append(srv.Args, item)
					cfg.MCPServers[parts[1]] = srv
				}
			}
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if v == "" {
			stack = append(stack, struct {
				indent int
				key    string
			}{indent: indent, key: k})
			continue
		}
		v = unquoteYAML(stripInlineComment(v))
		fullKey := k
		if p := currentPrefix(stack); p != "" {
			fullKey = p + "." + k
		}
		cfg.Raw[fullKey] = v
		applyScalar(cfg, fullKey, v)
	}
}

func currentPrefix(stack []struct {
	indent int
	key    string
}) string {
	if len(stack) == 0 {
		return ""
	}
	parts := make([]string, len(stack))
	for i, s := range stack {
		parts[i] = s.key
	}
	return strings.Join(parts, ".")
}

func stripInlineComment(v string) string {
	if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
		return v
	}
	if idx := strings.Index(v, " #"); idx >= 0 {
		return strings.TrimSpace(v[:idx])
	}
	return v
}

func unquoteYAML(v string) string {
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	return v
}

func applyScalar(cfg *Config, key, val string) {
	switch key {
	case "model", "model.default":
		cfg.Model = val
	case "model.provider", "provider":
		cfg.Provider = val
	case "model.base_url", "base_url":
		cfg.BaseURL = val
	case "display.skin", "skin":
		cfg.Skin = val
	case "toolset", "default_toolset":
		cfg.Toolset = val
	case "memory.memory_char_limit":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.MemoryLimit = n
		}
	case "memory.user_char_limit":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.UserLimit = n
		}
	case "tool_output.max_bytes":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.MaxOutputBytes = n
		}
	case "tool_output.max_lines":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.MaxOutputLines = n
		}
	case "tool_output.max_line_length":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.MaxLineLength = n
		}
	case "delegation.max_spawn_depth":
		if n, err := strconv.Atoi(val); err == nil && n >= 0 {
			cfg.MaxSpawnDepth = n
		}
	case "delegation.max_concurrent_children":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.MaxConcurrent = n
		}
	case "yolo", "approvals.yolo":
		cfg.SetYolo(parseBoolish(val))
	}
	if strings.HasPrefix(key, "mcp_servers.") {
		parts := strings.Split(key, ".")
		if len(parts) >= 3 {
			name := parts[1]
			srv := ensureMCPServer(cfg, name)
			switch parts[2] {
			case "command":
				srv.Command = val
			case "args":
				if parsed := parseFlowList(val); len(parsed) > 0 {
					srv.Args = parsed
				}
			case "url":
				srv.URL = val
			case "enabled":
				srv.Enabled = parseBoolish(val)
			case "env":
				if len(parts) == 4 {
					srv.Env[parts[3]] = val
				}
			case "headers":
				if len(parts) == 4 {
					srv.Headers[parts[3]] = val
				}
			}
			cfg.MCPServers[name] = srv
		}
	}
}

func ensureMCPServer(cfg *Config, name string) MCPServerConfig {
	srv, ok := cfg.MCPServers[name]
	if !ok {
		srv = MCPServerConfig{Name: name, Enabled: true, Env: map[string]string{}, Headers: map[string]string{}}
	}
	if srv.Env == nil {
		srv.Env = map[string]string{}
	}
	if srv.Headers == nil {
		srv.Headers = map[string]string{}
	}
	return srv
}

func parseBoolish(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "no", "off", "0", "":
		return false
	default:
		return true
	}
}

func parseFlowList(v string) []string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil
	}
	inner := strings.TrimSpace(v[1 : len(v)-1])
	if inner == "" {
		return nil
	}
	rawParts := strings.Split(inner, ",")
	out := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if item := unquoteYAML(strings.TrimSpace(part)); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// SetKey writes or updates a simple top-level key in config.yaml atomically.
func SetKey(configFile, key, value string) error {
	if err := os.MkdirAll(filepath.Dir(configFile), 0o755); err != nil {
		return err
	}
	var lines []string
	if raw, err := os.ReadFile(configFile); err == nil {
		lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	}
	prefix := key + ":"
	replaced := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) && !strings.HasPrefix(l, " ") {
			lines[i] = key + ": " + value
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, key+": "+value)
	}
	out := strings.Join(lines, "\n") + "\n"
	tmp, err := os.CreateTemp(filepath.Dir(configFile), "config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, configFile)
}
