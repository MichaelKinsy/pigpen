// Package config turns the ADKConfig that kagent compiles for a BYO Harness
// into what PiG needs: a generated models.json, a policy file for the policy
// extension, a system prompt file and an environment. Fields the harness cannot
// honour fail startup with their names; fields it accepts but does not honour
// are reported as warnings.
package config

import (
	"cmp"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	runtimeconfig "github.com/kagent-dev/kagent/go/adk/pkg/config"
	configv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/config/v1"
	"github.com/kagent-dev/kagent/go/pkg/telemetry"
	"github.com/kagent-dev/kagent/go/pkg/tracing"
)

// Tool names. AskUserTool is registered by the policy extension and has to be
// in PiG's --tools allowlist to reach the model.
const (
	AskUserTool  = "ask_user"
	DefaultTools = "read,bash,edit,write,grep,find,ls"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var toolListPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(,[A-Za-z0-9_-]+)*$`)

// Environment variables the adapter reads (all optional).
const (
	EnvBin           = "KAGENT_PIG_BIN"
	EnvTools         = "KAGENT_PIG_TOOLS"
	EnvLogLevel      = "KAGENT_PIG_LOG_LEVEL"
	EnvContextWindow = "KAGENT_PIG_CONTEXT_WINDOW"
	EnvMaxTokens     = "KAGENT_PIG_MAX_TOKENS"
	EnvPassEnv       = "KAGENT_PIG_PASS_ENV"
	EnvPigHome       = "KAGENT_PIG_HOME"
)

// Compiled is everything the driver needs, derived once at startup.
type Compiled struct {
	AgentName    string
	Provider     string // PiG provider id
	Model        string // PiG model id
	Thinking     string // PiG thinking level, empty when unset
	SystemPrompt string
	ModelsJSON   []byte
	Policy       Policy
	PolicyJSON   []byte
	// Env holds the variables pig needs beyond the inherited allowlist: provider
	// keys and the KAGENT_PIG_HDR_n header values.
	Env      []EnvVar
	Tools    []string
	Warnings []string

	ControlPlaneURL  string
	MaxContentLength *int64
	LogLevel         string
	Telemetry        tracing.RuntimeTelemetry
}

// ToolsArg is the value of pig's --tools flag: the built-in tools, ask_user, and one mcp__<server>__* pattern per
// bound MCP server.
func (c *Compiled) ToolsArg() string { return strings.Join(c.Tools, ",") }

// OptionsFromEnv reads the adapter tunables. lookup reports presence.
func OptionsFromEnv(getenv func(string) string) (Options, error) {
	var options Options
	var problems []string
	number := func(name string, into *int) {
		raw := getenv(name)
		if raw == "" {
			return
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			problems = append(problems, name+" must be a positive integer")
			return
		}
		*into = value
	}
	number(EnvContextWindow, &options.ContextWindow)
	number(EnvMaxTokens, &options.MaxTokens)
	options.Tools = getenv(EnvTools)
	if options.Tools != "" && !toolListPattern.MatchString(options.Tools) {
		problems = append(problems, EnvTools+" must be a comma separated list of tool names: name(,name)*")
	}
	options.LogLevel = getenv(EnvLogLevel)
	switch options.LogLevel {
	case "", "debug", "info", "warn", "error":
	default:
		problems = append(problems, EnvLogLevel+" must be debug, info, warn or error")
	}
	if raw := getenv(EnvPassEnv); raw != "" {
		for _, name := range strings.Split(raw, ",") {
			if !envNamePattern.MatchString(name) || strings.HasPrefix(name, "KAGENT_") {
				problems = append(problems, EnvPassEnv+" entries must be variable names not starting with KAGENT_")
				break
			}
			options.PassEnv = append(options.PassEnv, name)
		}
	}
	if len(problems) > 0 {
		return Options{}, fmt.Errorf("invalid environment: %s", strings.Join(problems, "; "))
	}
	return options, nil
}

// Parse validates and compiles the bootstrap ADKConfig JSON. lookup resolves
// the controller's __KAGENT_ENV[NAME]__ placeholders and provider key variables.
func Parse(raw []byte, lookup func(string) (string, bool), options Options) (*Compiled, error) {
	expanded, err := expandEnvPlaceholders(raw, lookup)
	if err != nil {
		return nil, err
	}
	cfg := new(configv1.ADKConfig)
	if err := runtimeconfig.Unmarshal(expanded, cfg); err != nil {
		return nil, err
	}
	return compile(cfg, lookup, options)
}

func compile(cfg *configv1.ADKConfig, lookup func(string) (string, bool), options Options) (*Compiled, error) {
	getenv := func(name string) string { value, _ := lookup(name); return value }
	agent := cfg.GetAgent()
	if agent == nil {
		return nil, &UnsupportedError{Problems: []Problem{{Field: "agent", Reason: "an agent section is required"}}}
	}
	c := &collector{}
	headers := newHeaderAllocator()
	out := &Compiled{AgentName: agent.Name, SystemPrompt: agent.Instruction}

	rejectAgentFields(c, agent)
	warnAgentFields(c, agent)

	runtime := cfg.GetRuntime()
	if runtime == nil {
		runtime = &configv1.ADKRuntimeConfig{}
	}
	if runtime.GetTokenPropagation().GetEnabled() {
		c.reject("runtime.token_propagation", "no caller credential reaches a Harness turn")
	}
	builtin := runtime.GetBuiltinTools()
	if builtin.GetBashVenvPath() != "" {
		c.warn("runtime.builtin_tools.bash_venv_path is accepted but not honoured: PiG's bash tool does not activate a virtualenv")
	}
	if runtime.GetHost() != "" || runtime.Port != nil {
		c.warn("runtime.host and runtime.port are accepted but not honoured: the private A2A endpoint is fixed at port 80")
	}

	model := mapModel(c, headers, getenv, options, agent.Model)
	servers := mapMCP(c, headers, agent.HttpTools)

	tools := DefaultTools
	if options.Tools != "" {
		tools = options.Tools
	}
	for _, name := range strings.Split(tools, ",") {
		switch {
		case name == "bash" && builtin.GetDisableBash():
		case (name == "grep" || name == "find" || name == "ls") && builtin.GetDisableFileSearch():
		default:
			out.Tools = append(out.Tools, name)
		}
	}
	askUser := !builtin.GetDisableAskUser()
	if askUser && !slices.Contains(out.Tools, AskUserTool) {
		out.Tools = append(out.Tools, AskUserTool)
	}
	// A non-empty --tools list offers the model only what it names; MCP tools stay registered but are not offered
	// unless an mcp__ entry is present. One pattern per server keeps exactly the bound servers' tools visible
	// (PiG folds - to _ in the whole tool name).
	for _, server := range servers {
		out.Tools = append(out.Tools, "mcp__"+strings.ReplaceAll(server.Name, "-", "_")+"__*")
	}
	out.Policy = Policy{MCPServers: servers, AskUser: askUser}

	if err := c.err(); err != nil {
		return nil, err
	}
	out.Provider, out.Model, out.Thinking, out.ModelsJSON = model.provider, model.model, model.thinking, model.modelsRaw
	out.Env = append(out.Env, model.env...)
	names := make([]string, 0, len(headers.values))
	for name := range headers.values {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int { return headerIndex(a) - headerIndex(b) })
	for _, name := range names {
		out.Env = append(out.Env, EnvVar{Name: name, Value: headers.values[name]})
	}
	policyJSON, err := MarshalPolicy(out.Policy)
	if err != nil {
		return nil, fmt.Errorf("encode policy file: %w", err)
	}
	out.PolicyJSON = policyJSON
	out.Warnings = c.warnings
	out.ControlPlaneURL = runtime.GetControlPlaneUrl()
	out.MaxContentLength = runtime.MaxContentLength
	out.LogLevel = cmp.Or(options.LogLevel, runtime.GetLogLevel())
	out.Telemetry = runtimeTelemetry(runtime.GetTelemetry(), model)
	return out, nil
}

func headerIndex(name string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(name, HeaderEnvPrefix))
	return n
}

// rejectAgentFields fails on compiled agent features PiG cannot honour.
func rejectAgentFields(c *collector, agent *configv1.ADKAgentConfig) {
	if len(agent.SseTools) > 0 {
		c.reject("agent.sse_tools", "PiG does not support the legacy SSE MCP transport")
	}
	if len(agent.StdioTools) > 0 {
		c.reject("agent.stdio_tools", "the harness runs only kagent-bound HTTP MCP servers; a compiled command would run inside the Actor with the Actor's egress grant")
	}
	if len(agent.RemoteAgents) > 0 {
		c.reject("agent.remote_agents", "PiG has no remote-agent delegation")
	}
	if len(agent.SubAgents) > 0 {
		c.reject("agent.sub_agents", "PiG has no sub-agent delegation in this harness")
	}
	if agent.Memory != nil {
		c.reject("agent.memory", "kagent memory belongs to the ADK runtime")
	}
	if agent.AgentPlugins != nil && (len(agent.AgentPlugins.Skills) > 0 || len(agent.AgentPlugins.Plugins) > 0) {
		c.reject("agent.agent_plugins", "skill and plugin materialization is not implemented for PiG")
	}
	if agent.SkillsDirectory != "" {
		c.reject("agent.skills_directory", "skill discovery from a compiled directory is not implemented for PiG")
	}
	if agent.Output != nil && (len(agent.Output.JsonSchema) > 0 || agent.Output.Sha256 != "") {
		c.reject("agent.output", "PiG cannot constrain its final answer to a JSON schema")
	}
}

// warnAgentFields records fields that are accepted without being honoured.
func warnAgentFields(c *collector, agent *configv1.ADKAgentConfig) {
	if agent.ContextConfig != nil && agent.ContextConfig.Compaction != nil {
		c.warn("agent.context_config.compaction is accepted but not honoured: PiG compacts its own session by the model contextWindow (set %s to tune it)", EnvContextWindow)
	}
	if agent.Stream != nil && !*agent.Stream {
		c.warn("agent.stream=false is accepted but not honoured: PiG always streams")
	}
	if agent.SessionDbUrl != "" {
		c.warn("agent.session_db_url is ignored by design: conversation state is PiG's own session file under /data/pig/sessions")
	}
}

func mapMCP(c *collector, headers *headerAllocator, tools []*configv1.ADKHTTPMCPServerConfig) []PolicyServer {
	taken := map[string]bool{}
	servers := make([]PolicyServer, 0, len(tools))
	if len(tools) > MaxServers {
		c.reject("agent.http_tools", "%d MCP servers; at most %d are supported", len(tools), MaxServers)
		return nil
	}
	for i, tool := range tools {
		field := fmt.Sprintf("agent.http_tools[%d]", i)
		params := tool.GetParams()
		if tool.TokenPassthrough {
			c.reject(field+".token_passthrough", "no caller credential reaches a Harness turn")
		}
		if len(tool.AllowedHeaders) > 0 {
			c.reject(field+".allowed_headers", "no incoming request headers reach a Harness turn")
		}
		if (params.TlsInsecureSkipVerify != nil && *params.TlsInsecureSkipVerify) || (params.TlsCaCertPath != nil && *params.TlsCaCertPath != "") || (params.TlsDisableSystemCas != nil && *params.TlsDisableSystemCas) {
			c.reject(field+".params.tls", "PiG has no per-server TLS option; trust comes from the egress trust bundle")
		}
		if params.SseReadTimeout != nil || params.TerminateOnClose != nil {
			c.warn("%s.params.sse_read_timeout and terminate_on_close are accepted but not honoured", field)
		}
		if !validHTTPURL(params.GetUrl()) {
			c.reject(field+".params.url", "must be an absolute http(s) URL without credentials, fragment or $")
			continue
		}
		server := PolicyServer{
			Name: serverName(params.Url, taken), URL: params.Url, Headers: map[string]string{},
			RequireApproval: tool.RequireApproval,
		}
		if d := params.Timeout; d != nil && d.AsDuration() > 0 {
			server.TimeoutSeconds = int((d.AsDuration() + time.Second - 1) / time.Second)
			if server.TimeoutSeconds > MaxTimeoutSec {
				c.reject(field+".params.timeout", "at most %d seconds are supported", MaxTimeoutSec)
			}
		}
		if len(tool.Tools) > MaxTools {
			c.reject(field+".tools", "%d tools; at most %d are supported", len(tool.Tools), MaxTools)
		}
		seen := map[string]bool{}
		for _, name := range tool.Tools {
			if name == "" || len(name) > MaxToolBytes || seen[name] || strings.ContainsAny(name, "*\x00\r\n") {
				c.reject(field+".tools", "tool names must be 1 to %d bytes, unique, without '*' and without control characters", MaxToolBytes)
				break
			}
			seen[name] = true
			server.Tools = append(server.Tools, name)
		}
		hasAuth := false
		lower := map[string]bool{}
		for name, value := range params.Headers {
			if !headerNamePattern.MatchString(name) || !validHeaderValue(value) {
				c.reject(field+".params.headers", "header %q has an invalid name or value", name)
				continue
			}
			if lower[strings.ToLower(name)] {
				c.reject(field+".params.headers", "header %q appears twice with different case", name)
				continue
			}
			lower[strings.ToLower(name)] = true
			if strings.EqualFold(name, "authorization") {
				hasAuth = true
			}
			server.Headers[name] = headers.reference(value)
		}
		if !hasAuth {
			// Without an Authorization header PiG tries OAuth discovery against
			// origins the egress policy blocks. The placeholder is inert.
			server.Headers["Authorization"] = headers.reference("Bearer " + CredentialPlaceholder)
		}
		if len(server.Headers) > MaxHeaders {
			c.reject(field+".params.headers", "%d headers; at most %d are supported", len(server.Headers), MaxHeaders)
		}
		servers = append(servers, server)
	}
	return servers
}

// runtimeTelemetry projects the compiled telemetry settings. kagent's runtime
// enum has no value for PiG, and identity is all or nothing, so the identity
// attributes are left empty rather than mislabelled as another runtime.
func runtimeTelemetry(cfg *configv1.TelemetryConfig, model *resolvedModel) tracing.RuntimeTelemetry {
	result := tracing.RuntimeTelemetry{Provider: model.provider, Model: model.model}
	if cfg == nil {
		return result
	}
	exporters := telemetry.Config{Traces: otlp(cfg.GetTraces()), Metrics: otlp(cfg.GetMetrics()), Logs: otlp(cfg.GetLogs())}
	if len(cfg.GetResourceAttributes()) > 0 {
		exporters.ResourceAttributes = make(map[string]string, len(cfg.GetResourceAttributes()))
		for k, v := range cfg.GetResourceAttributes() {
			exporters.ResourceAttributes[k] = v
		}
	}
	result.Exporters = &exporters
	result.CaptureContent = cfg.GetCaptureContent()
	result.CaptureRawAPIBodies = cfg.GetCaptureRawApiBodies()
	result.OpenAINativeTracing = cfg.GetOpenaiNativeTracing()
	result.MaxCaptureBytes = int(cfg.GetMaxCaptureBytes())
	return result
}

func otlp(cfg *configv1.OTLPConfig) *telemetry.OTLPConfig {
	if cfg == nil {
		return nil
	}
	protocol := cfg.GetProtocol()
	if protocol == "" {
		protocol = "grpc"
	}
	return &telemetry.OTLPConfig{Endpoint: cfg.GetEndpoint(), Protocol: protocol, TimeoutMillis: cfg.GetTimeout().AsDuration().Milliseconds()}
}

// LookupEnv is os.LookupEnv, named so callers pass it explicitly.
var LookupEnv = os.LookupEnv
