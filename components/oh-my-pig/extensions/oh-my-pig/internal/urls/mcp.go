package urls

import (
	_ "embed"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/internal-urls/mcp-protocol.ts

//go:embed prompts/mcp.md
var mcpPromptDoc string

// MCPHandler is a placeholder for `mcp://` and the MCP resource fallback scheme.
// PiG's extension SDK exposes MCP server registration (RegisterMcpServer,
// GetMcpServers) but does not expose MCP client resource listing or
// resources/read calls to an extension. Attempting to resolve an MCP resource
// returns an explicit diagnostic error naming the missing SDK capability.
// upstream: mcp-protocol.ts McpProtocolHandler.
type MCPHandler struct{}

func (MCPHandler) Scheme() string { return "mcp" }

func (MCPHandler) Spec() Spec {
	return Spec{
		Backing:   BackingRemote,
		Selectors: SelectorsOpaque,
		Immutable: true,
	}
}

func (MCPHandler) PromptDoc(_ SchemeHost) string {
	// Do not advertise mcp:// while PiG's extension SDK cannot list or read
	// MCP resources (see UPSTREAM.md). Kept embedded in mcpPromptDoc for when
	// the SDK gains resource-read support.
	_ = mcpPromptDoc
	return ""
}

func extractMCPResourceURI(u URL) (string, error) {
	if u.Scheme != "mcp" {
		return u.RawHref, nil
	}
	host := u.Host
	hasPath := u.Path != "" && u.Path != "/"
	var sb strings.Builder
	sb.WriteString(host)
	if hasPath {
		sb.WriteString(u.Path)
	}
	if u.RawQuery != "" {
		sb.WriteByte('?')
		sb.WriteString(u.RawQuery)
	}
	if hash := extractHash(u); hash != "" {
		sb.WriteString(hash)
	}
	uri := strings.TrimSpace(sb.String())
	if uri == "" {
		return "", fmt.Errorf("mcp:// URL requires a resource URI: mcp://<resource-uri>")
	}
	return uri, nil
}

func (MCPHandler) Resolve(c Context, u URL) (*Resource, error) {
	uri, err := extractMCPResourceURI(u)
	if err != nil {
		return nil, err
	}
	var servers []string
	if c.SDK != (sdk.Context{}) {
		if list, err := c.SDK.GetMcpServers(); err == nil {
			for _, s := range list {
				if s.Name != "" {
					servers = append(servers, s.Name)
				}
			}
		}
	}
	if len(servers) > 0 {
		return nil, fmt.Errorf(
			"MCP resource '%s' cannot be read from extension (configured servers: %s): PiG's extension SDK does not expose MCP resources/list or resources/read.",
			uri, strings.Join(servers, ", "),
		)
	}
	return nil, fmt.Errorf(
		"MCP resource '%s' cannot be read: PiG's extension SDK does not expose MCP resources/list or resources/read.",
		uri,
	)
}
