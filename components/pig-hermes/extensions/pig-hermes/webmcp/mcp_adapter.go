package webmcp

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
)

var validMCPServerName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func normalizeMCPServerName(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", "_")
}

// MCPRegistrationDiagnostic records the outcome for one configured Hermes MCP server.
type MCPRegistrationDiagnostic struct {
	Name    string
	Action  string // "registered", "skipped", "error"
	Message string
}

// TranslateMCPServerConfig converts a Hermes MCPServerConfig into a PiG SDK
// McpServerConfig (stdio or Streamable HTTP). Returns ok=false with a diagnostic
// reason when the server is disabled or invalid.
func TranslateMCPServerConfig(name string, srv config.MCPServerConfig, cfg ...*config.Config) (sdk.McpServerConfig, bool, string) {
	var c *config.Config
	if len(cfg) > 0 {
		c = cfg[0]
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = strings.TrimSpace(srv.Name)
	}
	if trimmedName == "" {
		return sdk.McpServerConfig{}, false, "server name is empty"
	}
	if !validMCPServerName.MatchString(trimmedName) {
		return sdk.McpServerConfig{}, false, fmt.Sprintf("invalid server name %q (must match ^[A-Za-z0-9_-]+$)", trimmedName)
	}
	if !srv.Enabled {
		return sdk.McpServerConfig{}, false, "disabled in config"
	}

	cmd := strings.TrimSpace(expandEnvPlaceholders(srv.Command, c))
	rawURL := strings.TrimSpace(expandEnvPlaceholders(srv.URL, c))

	if cmd == "" && rawURL == "" {
		return sdk.McpServerConfig{}, false, "missing both command (stdio) and url (http)"
	}

	enabled := true
	out := sdk.McpServerConfig{
		Enabled:  &enabled,
		Exposure: sdk.McpExposureDirect,
	}

	if rawURL != "" {
		if strings.Contains(rawURL, "${") {
			return sdk.McpServerConfig{}, false, fmt.Sprintf("unresolved environment placeholder in url %q", rawURL)
		}
		u, err := url.Parse(rawURL)
		if err != nil || u == nil || u.Host == "" {
			return sdk.McpServerConfig{}, false, fmt.Sprintf("invalid url %q", rawURL)
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "http" && scheme != "https" {
			return sdk.McpServerConfig{}, false, fmt.Sprintf("unsupported url scheme %q (must be http or https)", u.Scheme)
		}
		out.Type = "http"
		out.URL = rawURL
		if len(srv.Headers) > 0 {
			out.Headers = mapToOrderedStrings(srv.Headers, c)
		}
		return out, true, ""
	}

	out.Type = "stdio"
	out.Command = cmd
	if len(srv.Args) > 0 {
		args := make([]string, len(srv.Args))
		for i, a := range srv.Args {
			args[i] = expandEnvPlaceholders(a, c)
		}
		out.Args = args
	}
	if len(srv.Env) > 0 {
		out.Env = mapToOrderedStrings(srv.Env, c)
	}
	return out, true, ""
}

// RegisterHermesMCPServers translates cfg.MCPServers into PiG's native
// ext.RegisterMcpServer calls, skipping disabled or invalid servers and
// returning structured diagnostics for each server.
func RegisterHermesMCPServers(ext *sdk.Extension, cfg *config.Config) []MCPRegistrationDiagnostic {
	if ext == nil || cfg == nil || len(cfg.MCPServers) == 0 {
		return nil
	}

	names := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	diags := make([]MCPRegistrationDiagnostic, 0, len(names))
	seenNormalized := make(map[string]string, len(names))
	for _, name := range names {
		srv := cfg.MCPServers[name]
		mcpCfg, ok, reason := TranslateMCPServerConfig(name, srv, cfg)
		if !ok {
			diags = append(diags, MCPRegistrationDiagnostic{
				Name:    name,
				Action:  "skipped",
				Message: reason,
			})
			continue
		}
		norm := normalizeMCPServerName(name)
		if prev, exists := seenNormalized[norm]; exists {
			diags = append(diags, MCPRegistrationDiagnostic{
				Name:    name,
				Action:  "skipped",
				Message: fmt.Sprintf("normalized name %q collides with %q", norm, prev),
			})
			continue
		}
		if err := ext.RegisterMcpServer(name, mcpCfg); err != nil {
			diags = append(diags, MCPRegistrationDiagnostic{
				Name:    name,
				Action:  "error",
				Message: fmt.Sprintf("RegisterMcpServer failed: %v", err),
			})
			continue
		}
		seenNormalized[norm] = name
		diags = append(diags, MCPRegistrationDiagnostic{
			Name:    name,
			Action:  "registered",
			Message: mcpCfg.Type,
		})
	}
	return diags
}

func mapToOrderedStrings(m map[string]string, cfg *config.Config) *sdk.OrderedStrings {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		pairs = append(pairs, k, expandEnvPlaceholders(m[k], cfg))
	}
	return sdk.NewOrderedStrings(pairs...)
}

// expandEnvPlaceholders expands ${VAR} and ${env:VAR} references using process env
// and ~/.hermes/.env (via cfg.Getenv), matching Hermes mcp_tool_config conventions.
func expandEnvPlaceholders(s string, cfg *config.Config) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		start := strings.Index(s[i:], "${")
		if start < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+start])
		exprStart := i + start + 2
		end := strings.IndexByte(s[exprStart:], '}')
		if end < 0 {
			b.WriteString(s[i+start:])
			break
		}
		expr := s[exprStart : exprStart+end]
		if strings.HasPrefix(expr, "env:") {
			expr = strings.TrimPrefix(expr, "env:")
		}
		if expr == "userHome" {
			if home, err := os.UserHomeDir(); err == nil {
				b.WriteString(home)
			}
		} else if val, ok := lookupConfigEnv(cfg, expr); ok {
			b.WriteString(val)
		} else {
			// Preserve unresolved placeholder verbatim rather than silently dropping it.
			b.WriteString("${")
			b.WriteString(s[exprStart : exprStart+end])
			b.WriteByte('}')
		}
		i = exprStart + end + 1
	}
	return b.String()
}

func lookupConfigEnv(cfg *config.Config, key string) (string, bool) {
	if val, ok := os.LookupEnv(key); ok {
		return val, true
	}
	if cfg != nil {
		if v := cfg.Getenv(key); v != "" {
			return v, true
		}
	}
	return "", false
}
