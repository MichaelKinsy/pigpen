package pi_subagents

import (
	"fmt"
	"regexp"
	"strings"
)

// Port of src/runs/shared/mcp-direct-tool-grant.ts: which direct MCP tools an agent's `mcp:` selectors grant, from the servers
// of the adapter's configuration and the metadata cache of their tools and resources.

var mcpBuiltinToolNames = map[string]bool{"read": true, "bash": true, "edit": true, "write": true, "grep": true, "find": true, "ls": true, "mcp": true}

// resolvedMcpSelection is ResolvedMcpDirectToolSelection: the child tool name and the selector that granted it.
type resolvedMcpSelection struct{ Name, Selector string }

// mcpGrantServer is the per-server facts the grant reads (McpGrantServerFacts); nil lists are unset.
type mcpGrantServer struct {
	ExposeResources *bool
	IncludeTools    []any
	ExcludeTools    []any
	ToolPrefix      any
}

type mcpGrantTool struct{ Name *string }

type mcpGrantResource struct{ Name, URI *string }

// mcpGrantMetadata is the cached tools and resources of one server (McpGrantServerMetadata).
type mcpGrantMetadata struct {
	Tools     []mcpGrantTool
	Resources []mcpGrantResource
}

// mcpGrantServerEntry keeps the servers in the order JavaScript iterates them.
type mcpGrantServerEntry struct {
	Name   string
	Server mcpGrantServer
}

type mcpDirectToolGrantInput struct {
	Selectors  []string
	Servers    []mcpGrantServerEntry
	Metadata   map[string]mcpGrantMetadata
	ToolPrefix any
}

type mcpDirectToolGrant struct {
	Selections          []resolvedMcpSelection
	UnresolvedSelectors []string
}

// normalizeMcpDirectToolSelectors drops trailing slashes and empty and repeated selectors.
func normalizeMcpDirectToolSelectors(selectors []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range selectors {
		s = strings.TrimRight(s, "/")
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// mcpSelectorSet is parseMcpDirectToolSelectors: whole servers, and the named tools of a server.
type mcpSelectorSet struct {
	Servers map[string]bool
	Tools   map[string]map[string]bool
	// ToolServers keeps the order servers were first named by a tool selector.
	ToolServers []string
	ServerOrder []string
}

func parseMcpDirectToolSelectors(selectors []string) mcpSelectorSet {
	set := mcpSelectorSet{Servers: map[string]bool{}, Tools: map[string]map[string]bool{}}
	addServer := func(s string) {
		if !set.Servers[s] {
			set.Servers[s] = true
			set.ServerOrder = append(set.ServerOrder, s)
		}
	}
	for _, item := range selectors {
		switch {
		case strings.Contains(item, "/"):
			parts := strings.Split(item, "/") // JavaScript split("/", 2) keeps the first two parts
			server, tool := parts[0], parts[1]
			if server != "" && tool != "" {
				if set.Tools[server] == nil {
					set.Tools[server] = map[string]bool{}
					set.ToolServers = append(set.ToolServers, server)
				}
				set.Tools[server][tool] = true
			} else if server != "" {
				addServer(server)
			}
		case item != "":
			addServer(item)
		}
	}
	return set
}

func normalizeMcpToolPrefix(v any) string {
	if s, ok := v.(string); ok && (s == "none" || s == "short" || s == "mcp" || s == "server") {
		return s
	}
	return "server"
}

func formatUnresolvedMcpDirectToolSelectors(selectors []string) string {
	return "Unresolved MCP direct-tool selectors: " + strings.Join(selectors, ", ") + ". Direct MCP tools require a matching configured server and fresh metadata cache; runtime-registered servers require a host/pi-mcp-adapter handoff before child launch."
}

var (
	mcpShortSuffix = regexp.MustCompile(`(?i)-?mcp$`)
	mcpDigitStart  = regexp.MustCompile(`^\d`)
)

func getMcpServerPrefix(serverName, mode string) string {
	switch mode {
	case "none":
		return ""
	case "short":
		if p := sanitizeMcpServerPrefix(mcpShortSuffix.ReplaceAllString(serverName, "")); p != "" {
			return p
		}
		return "mcp"
	case "mcp":
		return "mcp__" + sanitizeMcpServerPrefix(serverName)
	}
	return sanitizeMcpServerPrefix(serverName)
}

// sanitizeMcpServerPrefix is the adapter's rule: characters outside [A-Za-z0-9_-] become `_<code point in hex>_`.
func sanitizeMcpServerPrefix(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 128 && (r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "_%x_", r)
		}
	}
	return b.String()
}

func formatMcpToolName(toolName, serverName, prefix string) string {
	serverPrefix := getMcpServerPrefix(serverName, prefix)
	sanitized := strings.ReplaceAll(toolName, ".", "_")
	// Some servers prefix their tool names with the server name; the adapter registers such a name unchanged.
	if serverPrefix != "" && strings.HasPrefix(sanitized, serverPrefix+"_") && jsLen(sanitized) > jsLen(serverPrefix)+1 {
		return sanitized
	}
	if serverPrefix != "" {
		return serverPrefix + "_" + sanitized
	}
	return sanitized
}

func jsLen(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func mcpToolNameCandidates(toolName, serverName, prefix string) []string {
	return []string{
		toolName,
		"mcp_" + toolName,
		formatMcpToolName(toolName, serverName, prefix),
		formatMcpToolName(toolName, serverName, "server"),
		formatMcpToolName(toolName, serverName, "short"),
		formatMcpToolName(toolName, serverName, "mcp"),
		formatMcpToolName(toolName, serverName, "none"),
	}
}

var mcpGlobSpecials = regexp.MustCompile(`[.+^${}()|\[\]\\]`)

func mcpGlobToRegexp(pattern string) *regexp.Regexp {
	escaped := mcpGlobSpecials.ReplaceAllStringFunc(pattern, func(m string) string { return `\` + m })
	escaped = strings.ReplaceAll(escaped, "*", ".*")
	escaped = strings.ReplaceAll(escaped, "?", ".")
	return regexp.MustCompile("^" + escaped + "$")
}

func normalizeMcpToolName(v string) string { return strings.ReplaceAll(v, "-", "_") }

func matchesMcpToolPatterns(candidates []string, patterns []any) bool {
	for _, p := range patterns {
		pattern, ok := p.(string)
		if !ok {
			continue
		}
		np := normalizeMcpToolName(pattern)
		var matcher *regexp.Regexp
		if strings.ContainsAny(np, "*?") {
			matcher = mcpGlobToRegexp(np)
		}
		for _, c := range candidates {
			nc := normalizeMcpToolName(c)
			if matcher != nil {
				if matcher.MatchString(nc) {
					return true
				}
			} else if nc == np {
				return true
			}
		}
	}
	return false
}

func isMcpToolAllowed(toolName, serverName, prefix string, include, exclude []any) bool {
	candidates := mcpToolNameCandidates(toolName, serverName, prefix)
	return (len(include) == 0 || matchesMcpToolPatterns(candidates, include)) && !matchesMcpToolPatterns(candidates, exclude)
}

var (
	mcpResourceNonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)
	mcpUnderscoreRuns   = regexp.MustCompile(`_+`)
)

func mcpResourceNameToToolName(name string) string {
	r := mcpResourceNonAlnum.ReplaceAllString(name, "_")
	r = mcpUnderscoreRuns.ReplaceAllString(r, "_")
	r = strings.Trim(r, "_")
	r = strings.ToLower(r)
	if r == "" || mcpDigitStart.MatchString(r) {
		if r != "" {
			r = "_" + r
		}
		r = "resource" + r
	}
	return r
}

// planMcpDirectToolGrant resolves the selectors against the servers and their validated metadata.
func planMcpDirectToolGrant(in mcpDirectToolGrantInput) mcpDirectToolGrant {
	selectors := normalizeMcpDirectToolSelectors(in.Selectors)
	if len(selectors) == 0 {
		return mcpDirectToolGrant{Selections: []resolvedMcpSelection{}, UnresolvedSelectors: []string{}}
	}
	sel := parseMcpDirectToolSelectors(selectors)
	selections := []resolvedMcpSelection{}
	seen := map[string]bool{}
	for _, entry := range in.Servers {
		serverName, server := entry.Name, entry.Server
		meta, ok := in.Metadata[serverName]
		if !ok {
			continue
		}
		var filter map[string]bool // nil: not selected
		all := sel.Servers[serverName]
		if !all {
			filter = sel.Tools[serverName]
			if filter == nil {
				continue
			}
		}
		prefixSource := server.ToolPrefix
		if prefixSource == nil {
			prefixSource = in.ToolPrefix
		}
		prefix := normalizeMcpToolPrefix(prefixSource)
		for _, tool := range meta.Tools {
			if tool.Name == nil || *tool.Name == "" {
				continue
			}
			if !all && !filter[*tool.Name] {
				continue
			}
			if !isMcpToolAllowed(*tool.Name, serverName, prefix, server.IncludeTools, server.ExcludeTools) {
				continue
			}
			name := formatMcpToolName(*tool.Name, serverName, prefix)
			if mcpBuiltinToolNames[name] || seen[name] {
				continue
			}
			seen[name] = true
			selections = append(selections, resolvedMcpSelection{Name: name, Selector: serverName + "/" + *tool.Name})
		}
		if server.ExposeResources != nil && !*server.ExposeResources {
			continue
		}
		for _, res := range meta.Resources {
			if res.Name == nil || *res.Name == "" || res.URI == nil || *res.URI == "" {
				continue
			}
			base := "get_" + mcpResourceNameToToolName(*res.Name)
			if !all && !filter[base] {
				continue
			}
			if !isMcpToolAllowed(base, serverName, prefix, server.IncludeTools, server.ExcludeTools) {
				continue
			}
			name := formatMcpToolName(base, serverName, prefix)
			if mcpBuiltinToolNames[name] || seen[name] {
				continue
			}
			seen[name] = true
			selections = append(selections, resolvedMcpSelection{Name: name, Selector: serverName + "/" + base})
		}
	}
	unresolved := []string{}
	for _, s := range selectors {
		found := false
		for _, sl := range selections {
			if strings.Contains(s, "/") {
				found = found || sl.Selector == s
			} else {
				found = found || strings.HasPrefix(sl.Selector, s+"/")
			}
		}
		if !found {
			unresolved = append(unresolved, s)
		}
	}
	return mcpDirectToolGrant{Selections: selections, UnresolvedSelectors: unresolved}
}
