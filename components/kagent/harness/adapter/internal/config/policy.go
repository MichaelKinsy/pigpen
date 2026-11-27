package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Limits of the policy extension's parser (components/kagent/extensions/kagent-policy/policy.go). A policy beyond
// them would stop PiG at start, so the adapter rejects the compiled configuration first and names the field.
const (
	MaxServers    = 64
	MaxHeaders    = 32
	MaxTools      = 256
	MaxToolBytes  = 128
	MaxURLBytes   = 2048
	MaxTimeoutSec = 3600
	// MaxServerName keeps the tool name prefix mcp__<server>__ inside the 55 characters PiG keeps when it shortens a
	// long tool name with a hash suffix. A longer server name would shorten the prefix away and the approval gate,
	// which matches the prefix, would no longer see the tool.
	MaxServerName = 40
)

// HeaderEnvPrefix names the pig environment variables that carry every header
// value. PiG treats a header starting with "!" as a shell command and expands
// "$VAR", and the compiled configuration is untrusted for that purpose, so no
// compiled string is ever written into a PiG-readable file as a header value.
const HeaderEnvPrefix = "KAGENT_PIG_HDR_"

// CredentialPlaceholder is what the controller puts in credential headers and
// provider key variables. The Substrate gateway replaces it on the wire only
// when the request already carries the header.
const CredentialPlaceholder = "kagent-credential-injected"

// Policy is the file the adapter hands to the policy extension (design section
// 4). Every header value is a ${KAGENT_PIG_HDR_n} reference.
type Policy struct {
	MCPServers []PolicyServer `json:"mcp_servers"`
	AskUser    bool           `json:"ask_user"`
}

// PolicyServer is one kagent-bound MCP server.
type PolicyServer struct {
	Name            string            `json:"name"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers"`
	TimeoutSeconds  int               `json:"timeout_seconds,omitempty"`
	Tools           []string          `json:"tools,omitempty"`
	RequireApproval bool              `json:"require_approval"`
}

// MarshalPolicy encodes the policy file contents.
func MarshalPolicy(policy Policy) ([]byte, error) {
	if policy.MCPServers == nil {
		policy.MCPServers = []PolicyServer{}
	}
	return json.MarshalIndent(policy, "", "  ")
}

// Timeout is the per-request deadline for this server, defaulting to PiG's 60 s.
func (s PolicyServer) Timeout() time.Duration {
	if s.TimeoutSeconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(s.TimeoutSeconds) * time.Second
}

var (
	// headerNamePattern is the policy extension's header name rule (narrower than RFC 9110 tokens).
	headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	serverNameClean   = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

// headerAllocator hands out HDR variable names and remembers their values.
type headerAllocator struct {
	values map[string]string
	next   int
}

func newHeaderAllocator() *headerAllocator {
	return &headerAllocator{values: map[string]string{}, next: 1}
}

// reference stores value under a fresh variable and returns the ${...} reference.
func (a *headerAllocator) reference(value string) string {
	name := fmt.Sprintf("%s%d", HeaderEnvPrefix, a.next)
	a.next++
	a.values[name] = value
	return "${" + name + "}"
}

func validHeaderValue(value string) bool {
	return !strings.ContainsAny(value, "\x00\r\n")
}

// serverName derives the PiG server name (tools become mcp__<name>__<tool>)
// from the URL, since compiled MCP servers carry no name. Only letters, digits
// and "-" are used: PiG folds "-" and "_" together and "__" separates the parts.
func serverName(rawURL string, taken map[string]bool) string {
	name := "mcp"
	if parsed, err := url.Parse(rawURL); err == nil && parsed.Hostname() != "" {
		host := parsed.Hostname()
		label, _, _ := strings.Cut(host, ".")
		if strings.Trim(label, "0123456789") == "" {
			label = host
		}
		if cleaned := strings.Trim(serverNameClean.ReplaceAllString(label, "-"), "-"); cleaned != "" {
			name = strings.ToLower(cleaned)
		}
	}
	if len(name) > MaxServerName-3 {
		name = strings.TrimRight(name[:MaxServerName-3], "-")
	}
	candidate := name
	for i := 2; taken[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
	taken[candidate] = true
	return candidate
}

// validHTTPURL requires an absolute http(s) URL with a host, no credentials,
// no fragment, and none of the characters PiG would expand.
func validHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	return len(raw) <= MaxURLBytes && !strings.ContainsAny(raw, "$\x00\r\n ") && !strings.HasPrefix(raw, "!")
}
