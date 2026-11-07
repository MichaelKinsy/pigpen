// Package kagentpolicy is the PiG side of the kagent harness: it registers the MCP servers a kagent Agent binds,
// holds the approval gate for the tools that need approval, and offers the ask_user tool.
//
// It does nothing unless KAGENT_PIG_POLICY names a policy file, so it is safe to fuse into any Binary. When the
// variable is set and the file is not a valid policy, the extension fails closed: constructing it panics, PiG
// refuses to start and prints the reason. It never runs ungated.
package kagentpolicy

import (
	"fmt"
	"io"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// EnvPolicy names the policy file. Unset means the extension is inert.
const EnvPolicy = "KAGENT_PIG_POLICY"

// Options replaces the extension's environment access (tests).
type Options struct {
	// LookupEnv reads the environment (default os.LookupEnv).
	LookupEnv func(string) (string, bool)
	// ReadFile reads the policy file, at most limit+1 bytes (default os.Open).
	ReadFile func(path string, limit int64) ([]byte, error)
}

// Extension returns the kagent policy extension. When KAGENT_PIG_POLICY is set and the policy cannot be used it
// panics: PiG then fails to construct the extension and exits with the message, so no session runs ungated.
func Extension() *sdk.Extension { return ExtensionWith(Options{}) }

// ExtensionWith is Extension with replaceable dependencies.
func ExtensionWith(o Options) *sdk.Extension {
	ext, err := New(o)
	if err != nil {
		panic("kagent-policy: refusing to start ungated: " + err.Error())
	}
	return ext
}

// New builds the extension. With KAGENT_PIG_POLICY unset it returns an inert extension (no handler, no tool, no
// server). With it set it returns the policy's extension, or the error that makes the policy unusable.
func New(o Options) (*sdk.Extension, error) {
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	if o.ReadFile == nil {
		o.ReadFile = readLimited
	}
	ext := sdk.New("kagent-policy")
	path, set := o.LookupEnv(EnvPolicy)
	if !set {
		return ext, nil
	}
	policy, err := loadPolicy(path, o)
	if err != nil {
		return nil, err
	}
	if err := policy.install(ext); err != nil {
		return nil, err
	}
	return ext, nil
}

func loadPolicy(path string, o Options) (Policy, error) {
	if path == "" {
		return Policy{}, fmt.Errorf("%s is set but empty", EnvPolicy)
	}
	data, err := o.ReadFile(path, maxPolicyBytes)
	if err != nil {
		return Policy{}, fmt.Errorf("read the policy file named by %s: %w", EnvPolicy, err)
	}
	if len(data) > maxPolicyBytes {
		return Policy{}, fmt.Errorf("the policy file named by %s is larger than %d bytes", EnvPolicy, maxPolicyBytes)
	}
	return ParsePolicy(data, o.LookupEnv)
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// install registers the policy's servers, the gate and, when asked for, ask_user.
func (p Policy) install(ext *sdk.Extension) error {
	g := &gate{}
	for _, s := range p.Servers {
		if err := ext.RegisterMcpServer(s.Name, s.config()); err != nil {
			return fmt.Errorf("register the MCP server %q: %w", s.Name, err)
		}
		g.servers = append(g.servers, gatedServer{prefix: namespacePrefix(s.Name), approval: s.RequireApproval})
	}
	if p.AskUser {
		g.ask = &askState{answers: map[string]string{}}
		registerAskUser(ext, g.ask)
	}
	// Always: with a policy loaded the handler is the default deny for MCP tools of servers the policy does not name.
	ext.OnEvent(sdk.EventToolCall, g.onToolCall)
	return nil
}

// config is the MCP server entry PiG registers: every tool direct when the allowlist is empty, otherwise the server
// hidden and each allowlisted tool direct. Header values go through unchanged; ParsePolicy guarantees `${VAR}`.
func (s Server) config() sdk.McpServerConfig {
	cfg := sdk.McpServerConfig{Type: "http", URL: s.URL}
	if len(s.Headers) > 0 {
		pairs := make([]string, 0, 2*len(s.Headers))
		for _, h := range s.Headers {
			pairs = append(pairs, h.Name, "${"+h.Var+"}")
		}
		cfg.Headers = sdk.NewOrderedStrings(pairs...)
	}
	if s.TimeoutSeconds > 0 {
		t := s.TimeoutSeconds
		cfg.Timeout = &t
	}
	if len(s.Tools) == 0 {
		cfg.Exposure = sdk.McpExposureDirect
		return cfg
	}
	cfg.Exposure = sdk.McpExposureHidden
	pairs := make([]string, 0, 2*len(s.Tools))
	for _, tool := range s.Tools {
		pairs = append(pairs, tool, string(sdk.McpExposureDirect))
	}
	cfg.ToolExposure = sdk.NewOrderedExposures(pairs...)
	return cfg
}

// namespacePrefix is the start every tool name of an MCP server has: PiG names a tool `mcp__<server>__<tool>` with
// `-` in the server name written as `_`. Matching a prefix, not a computed tool name, also covers a tool whose name PiG
// sanitized, cut to 64 characters with a hash suffix or suffixed after a collision: with a server name of at most 40
// characters the 14 character prefix and the name always survive the cut (PiG keeps 55 characters).
func namespacePrefix(server string) string {
	return "mcp__" + strings.ReplaceAll(server, "-", "_") + "__"
}
