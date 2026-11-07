package driver

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/config"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxToolNameLength is PiG's limit for a provider tool name.
const maxToolNameLength = 64

// SanitizeToolName is PiG's rule for MCP tool names: every character outside
// [A-Za-z0-9_] becomes "_", one per UTF-16 code unit (as the JavaScript regular
// expression does). PiG applies it to the whole name mcp__<server>__<tool>, so
// the server part is folded too: a server called kagent-tools is
// mcp__kagent_tools__...
func SanitizeToolName(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			b.WriteRune(r)
			continue
		}
		b.WriteString(strings.Repeat("_", utf16.RuneLen(r)))
	}
	return b.String()
}

// PigToolNames returns the names PiG can give an MCP tool: the sanitised name,
// when it fits in 64 characters, and the hash-suffixed form it takes when too
// long or when sanitising made two tools collide (sha256 of server NUL tool,
// first 8 hex digits).
func PigToolNames(server, tool string) []string {
	name := SanitizeToolName("mcp__" + server + "__" + tool)
	digest := sha256.Sum256([]byte(server + "\x00" + tool))
	hash := hex.EncodeToString(digest[:])[:8]
	hashed := name[:min(len(name), maxToolNameLength-len(hash)-1)] + "_" + hash
	if len(name) <= maxToolNameLength {
		return []string{name, hashed}
	}
	return []string{hashed}
}

// allowlistedRaw maps PiG's name back to a raw MCP name through the server's
// allowlist. Because PiG's names are lossy, only an exact reproduction counts.
func allowlistedRaw(server config.PolicyServer, pigName string) (string, bool) {
	match := ""
	for _, raw := range server.Tools {
		if slices.Contains(PigToolNames(server.Name, raw), pigName) {
			if match != "" {
				return "", false
			}
			match = raw
		}
	}
	return match, match != ""
}

// RawToolName is the raw MCP tool name for a PiG tool name of a configured
// server: from the allowlist when it decides, otherwise the tool part of the
// name as PiG wrote it.
func RawToolName(servers []config.PolicyServer, serverName, pigName string) string {
	for _, s := range servers {
		if s.Name != serverName {
			continue
		}
		if raw, ok := allowlistedRaw(s, pigName); ok {
			return raw
		}
		return strings.TrimPrefix(pigName, "mcp__"+SanitizeToolName(s.Name)+"__")
	}
	return pigName
}

// ToolCaller executes the approved MCP call itself.
type ToolCaller interface {
	Call(ctx context.Context, server config.PolicyServer, pigName string, args map[string]any) (*mcp.CallToolResult, error)
}

// notExecutedError proves that no tools/call request was sent. Any other
// error leaves the outcome uncertain because a remote side effect may have run.
type notExecutedError struct{ err error }

func (e *notExecutedError) Error() string { return e.err.Error() }
func (e *notExecutedError) Unwrap() error { return e.err }

// MCPCaller calls approved tools over streamable HTTP with the placeholder
// headers from the policy. The HTTP client is the Go default transport, which
// honours the egress trust bundle (SSL_CERT_FILE and SSL_CERT_DIR) and the
// proxy variables of the adapter's own environment.
type MCPCaller struct {
	headerValues map[string]string
	client       *http.Client
}

// NewMCPCaller builds a caller. values holds the KAGENT_PIG_HDR_n values. caBundle, when not empty, is a PEM file
// of system roots plus the egress trust bundle that becomes the caller's root set: Go reads SSL_CERT_FILE as the
// whole root set, and in the adapter's own environment that names the egress bundle only.
func NewMCPCaller(values map[string]string, caBundle string) (*MCPCaller, error) {
	transport := http.DefaultTransport
	if caBundle != "" {
		pemBytes, err := os.ReadFile(caBundle)
		if err != nil {
			return nil, fmt.Errorf("read the CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, errors.New("the CA bundle holds no certificate")
		}
		clone := http.DefaultTransport.(*http.Transport).Clone()
		clone.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		transport = clone
	}
	return &MCPCaller{headerValues: values, client: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

type headerTransport struct {
	base    http.RoundTripper
	headers http.Header
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	for name, values := range t.headers {
		request.Header[name] = append([]string(nil), values...)
	}
	return t.base.RoundTrip(request)
}

// resolveHeaders expands the ${KAGENT_PIG_HDR_n} references of a policy server.
func (c *MCPCaller) resolveHeaders(server config.PolicyServer) (http.Header, error) {
	headers := make(http.Header, len(server.Headers))
	for name, ref := range server.Headers {
		if !strings.HasPrefix(ref, "${") || !strings.HasSuffix(ref, "}") {
			return nil, fmt.Errorf("header %q is not an environment reference", name)
		}
		value, ok := c.headerValues[strings.TrimSuffix(strings.TrimPrefix(ref, "${"), "}")]
		if !ok {
			return nil, fmt.Errorf("header %q references an unset variable", name)
		}
		headers.Set(name, value)
	}
	return headers, nil
}

// Call connects, resolves the raw tool name when PiG's sanitising was lossy,
// and issues tools/call. Failures before tools/call return *notExecutedError.
func (c *MCPCaller) Call(ctx context.Context, server config.PolicyServer, pigName string, args map[string]any) (result *mcp.CallToolResult, returnErr error) {
	called := false
	defer func() {
		if !called && returnErr != nil {
			returnErr = &notExecutedError{err: returnErr}
		}
	}()
	headers, err := c.resolveHeaders(server)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, server.Timeout())
	defer cancel()
	httpClient := &http.Client{
		Transport:     headerTransport{base: c.client.Transport, headers: headers},
		CheckRedirect: c.client.CheckRedirect,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "kagent-pig", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect MCP server %s: %w", server.Name, err)
	}
	defer session.Close()
	raw, known := allowlistedRaw(server, pigName)
	if !known && len(server.Tools) > 0 {
		// PiG exposed only allowlisted tools, so a name outside the allowlist is not a call this server may get.
		return nil, fmt.Errorf("tool %q is not in the allowlist of MCP server %s", pigName, server.Name)
	}
	if !known {
		raw, err = c.listedName(ctx, session, server, pigName)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	called = true
	return session.CallTool(ctx, &mcp.CallToolParams{Name: raw, Arguments: args})
}

// listedName finds the one tool of the server that PiG would have given this name.
func (c *MCPCaller) listedName(ctx context.Context, session *mcp.ClientSession, server config.PolicyServer, pigName string) (string, error) {
	match := ""
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return "", fmt.Errorf("list MCP tools: %w", err)
		}
		if slices.Contains(PigToolNames(server.Name, tool.Name), pigName) {
			if match != "" {
				return "", fmt.Errorf("MCP tool name %q is ambiguous after sanitising", pigName)
			}
			match = tool.Name
		}
	}
	if match == "" {
		return "", fmt.Errorf("MCP server %s has no tool PiG would call %q", server.Name, pigName)
	}
	return match, nil
}

// Bounds on what an approved call may put into the session. PiG truncates its own tool output; the adapter does
// the same for the result it injects, so one call cannot fill the context window or the pending record.
const (
	MaxResultTextBytes  = 50 << 10
	MaxResultImageBytes = 1 << 20
)

// OutcomeFromMCP converts an MCP result into the session's tool-result content.
func OutcomeFromMCP(result *mcp.CallToolResult) (journal.Outcome, int) {
	out := journal.Outcome{IsError: result.IsError, Content: []journal.Block{}}
	textLeft, imageLeft, dropped := MaxResultTextBytes, MaxResultImageBytes, 0
	text := func(s string) {
		if len(s) > textLeft {
			dropped += len(s) - textLeft
			s = strings.ToValidUTF8(s[:textLeft], "")
		}
		textLeft -= len(s)
		if s != "" || len(out.Content) == 0 {
			out.Content = append(out.Content, journal.Block{Type: "text", Text: s})
		}
	}
	for _, content := range result.Content {
		switch c := content.(type) {
		case *mcp.TextContent:
			text(c.Text)
		case *mcp.ImageContent:
			encoded := base64.StdEncoding.EncodeToString(c.Data)
			if len(encoded) > imageLeft {
				dropped += len(encoded)
				continue
			}
			imageLeft -= len(encoded)
			out.Content = append(out.Content, journal.Block{Type: "image", Data: encoded, MIMEType: c.MIMEType})
		case *mcp.EmbeddedResource:
			if c.Resource != nil && c.Resource.Text != "" {
				text(c.Resource.Text)
			} else {
				text("[binary resource omitted]")
			}
		case *mcp.ResourceLink:
			text("[resource link: " + c.URI + "]")
		default:
			text(fmt.Sprintf("[%T content omitted]", content))
		}
	}
	if len(out.Content) == 0 && result.StructuredContent != nil {
		if data, err := json.Marshal(result.StructuredContent); err == nil {
			text(string(data))
		}
	}
	if len(out.Content) == 0 {
		out.Content = append(out.Content, journal.Block{Type: "text", Text: ""})
	}
	if dropped > 0 {
		out.Content = append(out.Content, journal.Block{Type: "text", Text: fmt.Sprintf("[truncated: %d bytes of the result were not kept]", dropped)})
	}
	return out, dropped
}
