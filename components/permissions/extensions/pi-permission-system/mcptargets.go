package pi_permission_system

import (
	"strings"
	"unicode/utf16"
)

// Port of src/access-intent/mcp-targets.ts: derive the ordered MCP permission-lookup candidates for a call.

// McpTargetList is an ordered accumulator that owns the uniqueness invariant: Add ignores an empty value and a duplicate.
type McpTargetList struct{ targets []string }

// Add appends value unless it is empty or already present.
func (l *McpTargetList) Add(value string) {
	if value == "" {
		return
	}
	for _, t := range l.targets {
		if t == value {
			return
		}
	}
	l.targets = append(l.targets, value)
}

// ToArray returns the ordered result as an independent copy (never nil).
func (l *McpTargetList) ToArray() []string { return append([]string{}, l.targets...) }

// QualifiedMcpToolName is the result of ParseQualifiedMcpToolName.
type QualifiedMcpToolName struct{ Server, Tool string }

// ParseQualifiedMcpToolName parses `server:tool`: exactly the first colon, non-empty text on both sides.
func ParseQualifiedMcpToolName(value string) (QualifiedMcpToolName, bool) {
	trimmed := jsTrim(value)
	if trimmed == "" {
		return QualifiedMcpToolName{}, false
	}
	colon := strings.IndexByte(trimmed, ':')
	if colon <= 0 || colon >= len(trimmed)-1 {
		return QualifiedMcpToolName{}, false
	}
	server := jsTrim(trimmed[:colon])
	tool := jsTrim(trimmed[colon+1:])
	if server == "" || tool == "" {
		return QualifiedMcpToolName{}, false
	}
	return QualifiedMcpToolName{Server: server, Tool: tool}, true
}

// findLongestPrefixServer finds the configured server whose `<server>_` prefixes toolName, the longest one (UTF-16 length).
func findLongestPrefixServer(toolName string, configured []string) (string, bool) {
	longest, found := "", false
	for _, name := range configured {
		t := jsTrim(name)
		if t == "" || !strings.HasPrefix(toolName, t+"_") {
			continue
		}
		if !found || jsLen(t) > jsLen(longest) {
			longest, found = t, true
		}
	}
	return longest, found
}

func addDerivedMcpServerTargets(toolName string, configured []string, targets *McpTargetList) {
	tool := jsTrim(toolName)
	if tool == "" {
		return
	}
	if prefix, ok := findLongestPrefixServer(tool, configured); ok {
		targets.Add(tool)
		targets.Add(prefix)
		return
	}
	for _, name := range configured {
		server := jsTrim(name)
		if server == "" || !strings.HasSuffix(tool, "_"+server) {
			continue
		}
		targets.Add(server + "_" + tool)
		targets.Add(server + ":" + tool)
		targets.Add(server)
	}
}

func pushMcpToolPermissionTargets(rawReference string, serverHint string, hasHint bool, configured []string, targets *McpTargetList) {
	qualified, isQualified := ParseQualifiedMcpToolName(rawReference)
	server, haveServer := serverHint, hasHint
	if !haveServer && isQualified {
		server, haveServer = qualified.Server, true
	}
	tool := rawReference
	if isQualified {
		tool = qualified.Tool
	}
	if haveServer {
		if strings.HasPrefix(tool, server+"_") {
			targets.Add(tool)
		} else {
			targets.Add(server + "_" + tool)
			targets.Add(server + ":" + tool)
		}
		targets.Add(server)
	} else {
		addDerivedMcpServerTargets(tool, configured, targets)
	}
	targets.Add(tool)
	targets.Add(rawReference)
}

// CreateMcpPermissionTargets derives the ordered list of MCP lookup candidates from a raw MCP tool invocation input, most specific
// first.
func CreateMcpPermissionTargets(input any, configuredServerNames []string) []string {
	record := ToRecord(input)
	tool, hasTool := recordString(record, "tool")
	server, hasServer := recordString(record, "server")
	connect, hasConnect := recordString(record, "connect")
	describe, hasDescribe := recordString(record, "describe")
	search, hasSearch := recordString(record, "search")

	targets := &McpTargetList{}
	switch {
	case hasTool:
		pushMcpToolPermissionTargets(tool, server, hasServer, configuredServerNames, targets)
		targets.Add("mcp_call")
	case hasConnect:
		targets.Add("mcp_connect_" + connect)
		targets.Add(connect)
		targets.Add("mcp_connect")
	case hasDescribe:
		pushMcpToolPermissionTargets(describe, server, hasServer, configuredServerNames, targets)
		targets.Add("mcp_describe")
	case hasSearch:
		if hasServer {
			targets.Add("mcp_server_" + server)
			targets.Add(server)
		}
		targets.Add(search)
		targets.Add("mcp_search")
	case hasServer:
		targets.Add("mcp_server_" + server)
		targets.Add(server)
		targets.Add("mcp_list")
	default:
		targets.Add("mcp_status")
	}
	return targets.ToArray()
}

// CreatePiMcpToolTargets derives the ordered candidates for a tool Pi's built-in MCP registered under mcp__<server>__<tool>: the
// proxied call's candidates plus the full Pi name, the server in both its configured and its sanitized spelling.
func CreatePiMcpToolTargets(toolName string, configuredServerNames []string) []string {
	rest := jsSlice(jsTrim(toolName), len(PiMcpToolPrefix), jsEnd)
	spellings, tool := resolvePiMcpServer(rest, configuredServerNames)
	targets := &McpTargetList{}
	for _, server := range spellings {
		targets.Add(server + "_" + tool)
		targets.Add(server + ":" + tool)
		targets.Add(server)
	}
	targets.Add(tool)
	targets.Add(jsTrim(toolName))
	targets.Add("mcp_call")
	return targets.ToArray()
}

func resolvePiMcpServer(rest string, configured []string) (spellings []string, tool string) {
	sanitizedServer, haveServer := "", false
	var configuredSpellings []string
	for _, name := range configured {
		trimmed := jsTrim(name)
		sanitized := sanitizePiMcpName(trimmed)
		if trimmed == "" || !strings.HasPrefix(rest, sanitized+"__") {
			continue
		}
		if !haveServer || jsLen(sanitized) > jsLen(sanitizedServer) {
			sanitizedServer, haveServer = sanitized, true
			configuredSpellings = []string{trimmed}
		} else if sanitized == sanitizedServer {
			configuredSpellings = append(configuredSpellings, trimmed)
		}
	}
	if !haveServer {
		sanitizedServer = jsSlice(rest, 0, jsIndexOf(rest, "__"))
	}
	return uniqueStrings(append(configuredSpellings, sanitizedServer)), jsSlice(rest, jsLen(sanitizedServer)+2, jsEnd)
}

// sanitizePiMcpName is Pi's name sanitization: every UTF-16 unit outside [A-Za-z0-9_] becomes `_`.
func sanitizePiMcpName(name string) string {
	var b strings.Builder
	if isASCII(name) { // one unit per byte
		b.Grow(len(name))
		for i := 0; i < len(name); i++ {
			if c := name[i]; c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				b.WriteByte(c)
			} else {
				b.WriteByte('_')
			}
		}
		return b.String()
	}
	units := utf16.Encode([]rune(name))
	for _, u := range units {
		if u < 0x80 && (u == '_' || (u >= '0' && u <= '9') || (u >= 'a' && u <= 'z') || (u >= 'A' && u <= 'Z')) {
			b.WriteByte(byte(u))
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
