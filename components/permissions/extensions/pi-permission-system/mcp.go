package pi_permission_system

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The MCP surface (original: access-intent/mcp-targets.ts, access-intent/input-normalizer.ts's
// normalizeMcpTargets, policy/permission-manager.ts buildCheckResult): a `mcp` proxy call and a built-in
// `mcp__<server>__<tool>` call are decided on the `mcp` surface against the candidate targets their input and
// name derive, most specific first. The last rule matching any candidate wins and the reported target is the
// first candidate that rule matches, so an `mcp` rule written for either client names the same call.

// mcpTargetList is McpTargetList: an ordered accumulator that ignores empty values and duplicates, the first
// insertion winning.
type mcpTargetList struct {
	values []string
	seen   map[string]bool
}

func newMcpTargetList() *mcpTargetList { return &mcpTargetList{seen: map[string]bool{}} }

// add records value unless it is empty or already held (a null reads as "" here: the gate's input is Go data).
func (l *mcpTargetList) add(value string) {
	if value == "" || l.seen[value] {
		return
	}
	l.seen[value] = true
	l.values = append(l.values, value)
}

// toArray is the ordered result, an independent copy.
func (l *mcpTargetList) toArray() []string {
	return append([]string{}, l.values...)
}

// parseQualifiedMcpToolName splits `server:tool` at its first colon, trimmed, when both sides hold text; the
// two indexes are UTF-16 units in the original and bytes here, which only differs for a non-ASCII neighbour of
// the colon (server names hold letters, digits, `_` and `-`).
func parseQualifiedMcpToolName(value string) (server, tool string, ok bool) {
	trimmed := jsTrim(value)
	if trimmed == "" {
		return "", "", false
	}
	colon := strings.IndexByte(trimmed, ':')
	if colon <= 0 || colon >= len(trimmed)-1 {
		return "", "", false
	}
	server, tool = jsTrim(trimmed[:colon]), jsTrim(trimmed[colon+1:])
	if server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

// sanitizePiMcpName is Pi's MCP name sanitization: every character outside [A-Za-z0-9_] becomes `_`. The
// original maps one UTF-16 unit to one `_`, a rune here, which differs only for non-ASCII input PiG's server
// names cannot hold.
func sanitizePiMcpName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// findLongestPrefixServer is the configured server that owns toolName by the prefix convention: the longest
// configured name that is the tool's leading `<server>_` segment, so `foo_bar_baz` belongs to `foo_bar` and
// never also to `foo`. An empty answer is "none" (a configured name is never empty).
func findLongestPrefixServer(toolName string, configured []string) string {
	longest := ""
	for _, serverName := range configured {
		trimmed := jsTrim(serverName)
		if trimmed == "" || !strings.HasPrefix(toolName, trimmed+"_") {
			continue
		}
		if longest == "" || len(trimmed) > len(longest) {
			longest = trimmed
		}
	}
	return longest
}

// addDerivedMcpServerTargets derives the server-qualified candidates of a tool name: the bare server for a
// configured leading segment (the prefix convention, which settles the name's convention so the suffix pass
// does not run), else the qualified forms for every configured server the name ends in (the suffix
// convention). A prefix hit means a suffix that names another server is a coincidence, not a second owner.
func addDerivedMcpServerTargets(toolName string, configured []string, targets *mcpTargetList) {
	trimmed := jsTrim(toolName)
	if trimmed == "" {
		return
	}
	if prefixServer := findLongestPrefixServer(trimmed, configured); prefixServer != "" {
		targets.add(trimmed)
		targets.add(prefixServer)
		return
	}
	for _, serverName := range configured {
		trimmedServer := jsTrim(serverName)
		if trimmedServer == "" || !strings.HasSuffix(trimmed, "_"+trimmedServer) {
			continue
		}
		targets.add(trimmedServer + "_" + trimmed)
		targets.add(trimmedServer + ":" + trimmed)
		targets.add(trimmedServer)
	}
}

// pushMcpToolPermissionTargets adds the candidates of one raw MCP tool reference: the qualified forms of a
// server that arrives explicitly or in the reference itself (a name already carrying its server needs no
// re-prefixing — `github_github_search_code` names nothing a rule can usefully match — so the tool name leads
// instead), the derived candidates when no server is known, then the tool name and the reference itself.
func pushMcpToolPermissionTargets(rawReference, serverHint string, configured []string, targets *mcpTargetList) {
	qualifiedServer, qualifiedTool, qualified := parseQualifiedMcpToolName(rawReference)
	resolvedServer := serverHint
	if resolvedServer == "" && qualified {
		resolvedServer = qualifiedServer
	}
	resolvedTool := rawReference
	if qualified {
		resolvedTool = qualifiedTool
	}
	if resolvedServer != "" {
		if strings.HasPrefix(resolvedTool, resolvedServer+"_") {
			targets.add(resolvedTool)
		} else {
			targets.add(resolvedServer + "_" + resolvedTool)
			targets.add(resolvedServer + ":" + resolvedTool)
		}
		targets.add(resolvedServer)
	} else {
		addDerivedMcpServerTargets(resolvedTool, configured, targets)
	}
	targets.add(resolvedTool)
	targets.add(rawReference)
}

// nonEmptyString is value-guards.ts getNonEmptyString: the trimmed text of a string value, "" for anything else.
func nonEmptyString(v any) string {
	s, _ := v.(string)
	return jsTrim(s)
}

// createMcpPermissionTargets derives the ordered candidates of a `mcp` proxy call from its input, most specific
// first (the order decides which name a winning rule is reported under, never which rule wins): a tool call
// (or describe) names the call, connect/search/server their operation, and an input that names nothing is the
// status probe.
func createMcpPermissionTargets(input map[string]any, configured []string) []string {
	tool := nonEmptyString(input["tool"])
	server := nonEmptyString(input["server"])
	connect := nonEmptyString(input["connect"])
	describe := nonEmptyString(input["describe"])
	search := nonEmptyString(input["search"])
	targets := newMcpTargetList()
	switch {
	case tool != "":
		pushMcpToolPermissionTargets(tool, server, configured, targets)
		targets.add("mcp_call")
	case connect != "":
		targets.add("mcp_connect_" + connect)
		targets.add(connect)
		targets.add("mcp_connect")
	case describe != "":
		pushMcpToolPermissionTargets(describe, server, configured, targets)
		targets.add("mcp_describe")
	case search != "":
		if server != "" {
			targets.add("mcp_server_" + server)
			targets.add(server)
		}
		targets.add(search)
		targets.add("mcp_search")
	case server != "":
		targets.add("mcp_server_" + server)
		targets.add(server)
		targets.add("mcp_list")
	default:
		targets.add("mcp_status")
	}
	return targets.toArray()
}

// createPiMcpToolTargets derives the ordered candidates of a tool Pi's built-in MCP registered under its own
// name, `mcp__<server>__<tool>`: the candidates mirror a proxied call's (`<server>_<tool>`, `<server>:<tool>`,
// `<server>`, `<tool>`, `mcp_call`) plus the full Pi name, so an `mcp` rule written for either client names the
// same call. Pi rewrites every character outside [A-Za-z0-9_] to `_` in the name, so the server is emitted in
// both spellings: the configured one from mcp.json (`danger-srv`) and the one in the name (`danger_srv`).
func createPiMcpToolTargets(toolName string, configured []string) []string {
	rest := jsTrim(toolName)
	if len(rest) < len(piMcpToolPrefix) {
		rest = ""
	} else {
		rest = rest[len(piMcpToolPrefix):]
	}
	spellings, tool := resolvePiMcpServer(rest, configured)
	targets := newMcpTargetList()
	for _, server := range spellings {
		targets.add(server + "_" + tool)
		targets.add(server + ":" + tool)
		targets.add(server)
	}
	targets.add(tool)
	targets.add(jsTrim(toolName))
	targets.add("mcp_call")
	return targets.toArray()
}

// resolvePiMcpServer is the server spellings and tool a Pi MCP tool name (prefix removed) holds: the longest
// configured name whose sanitized form prefixes the tool name (every configured spelling of it, so a rule
// written for either spelling fires), and with none the name split at its first `__`.
func resolvePiMcpServer(rest string, configured []string) (spellings []string, tool string) {
	var sanitized string
	var owned []string
	found := false
	for _, serverName := range configured {
		trimmed := jsTrim(serverName)
		name := sanitizePiMcpName(trimmed)
		if trimmed == "" || !strings.HasPrefix(rest, name+"__") {
			continue
		}
		switch {
		case !found || len(name) > len(sanitized):
			sanitized, owned, found = name, []string{trimmed}, true
		case name == sanitized:
			owned = append(owned, trimmed)
		}
	}
	if !found {
		// The name splits at its first `__`; with none (no caller of a bare
		// name) JavaScript's slice(0, -1) drops the last unit instead.
		sep := strings.Index(rest, "__")
		switch {
		case sep >= 0:
			sanitized = rest[:sep]
		case rest == "":
			sanitized = ""
		default:
			sanitized = rest[:len(rest)-1]
		}
	}
	seen := map[string]bool{}
	for _, spelling := range append(owned, sanitized) {
		if spelling == "" || seen[spelling] {
			continue
		}
		seen[spelling] = true
		spellings = append(spellings, spelling)
	}
	if at := len(sanitized) + 2; at <= len(rest) {
		tool = rest[at:]
	}
	return spellings, tool
}

// normalizeMcpTargets is input-normalizer.ts's normalizeMcpTargets for the mcp and mcp-tool kinds: the `mcp`
// surface, the candidates with the surface-wide `mcp` name appended (the fallback every lookup ends at), and
// the target the dialog shows before a rule has matched (the first candidate).
func normalizeMcpTargets(targets []string) (surface, target string, values []string) {
	values = make([]string, 0, len(targets)+1)
	values = append(values, targets...)
	values = append(values, "mcp")
	return "mcp", values[0], values
}

// mcpServerNames is loader.getConfiguredMcpServerNames: the server names of the agent's mcp.json and the
// project's, longest name first so the longest prefix decides whatever order they arrive in, deduplicated and
// cached on the files' stamps the way the original caches them. PiG names the project file `.pig/mcp.json`
// (and reads `.pi/mcp.json` for compatibility); the agent directory is the host's own answer (agentDir).
func (p *Policy) mcpServerNames(cwd string) []string {
	paths := []string{filepath.Join(agentDir(), "mcp.json")}
	if cwd != "" {
		paths = append(paths, filepath.Join(cwd, ".pig", "mcp.json"), filepath.Join(cwd, ".pi", "mcp.json"))
	}
	var stamp strings.Builder
	for _, path := range paths {
		stamp.WriteString(path)
		if st, err := os.Stat(path); err == nil {
			stamp.WriteString(strconv.FormatInt(st.ModTime().UnixNano(), 10))
		} else {
			stamp.WriteString("missing")
		}
		stamp.WriteByte('|')
	}
	p.serversMu.Lock()
	defer p.serversMu.Unlock()
	if p.serversStamp == stamp.String() {
		return p.servers
	}
	seen := map[string]bool{}
	var names []string
	for _, path := range paths {
		for _, name := range readMcpServerNames(path) {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	// The loader's order: longest first, then by name (localeCompare here is Go's byte order; the names are
	// ASCII), so equal-length names arrive deterministically.
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})
	p.serversStamp, p.servers = stamp.String(), names
	return names
}

// readMcpServerNames is readConfiguredMcpServerNamesFromConfigPath: the keys of `mcpServers` (or the
// `mcp-servers` spelling) in one mcp.json, comments stripped, trimmed; an unreadable or unparsable file names
// nothing.
func readMcpServerNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	root, err := parseJSON([]byte(stripJSONComments(string(data))))
	if err != nil {
		return nil
	}
	obj, _ := root.(*jsObject)
	if obj == nil {
		return nil
	}
	servers := obj.obj("mcpServers")
	if servers == nil {
		servers = obj.obj("mcp-servers")
	}
	if servers == nil {
		return nil
	}
	var names []string
	for _, name := range servers.order() {
		if trimmed := jsTrim(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// decideMcp gates one call on the MCP surface: the candidates decide through the rules with the session
// approvals appended (last match wins, the reported target the first candidate the winning rule matches), and
// an ask carries the invoked tool and the session option the surface suggests. No path or external-directory
// gate applies: an MCP call's input is the MCP arguments, not a path (describeToolGate's pathAccess is for
// path-bearing tools alone).
func (p *Policy) decideMcp(tool string, targets []string, session Ruleset) Verdict {
	surface, _, values := normalizeMcpTargets(targets)
	found := EvaluateAnyValue(surface, values, p.sessionWith(session), PosixPathFlavor)
	return p.outcome(found.Rule, mcpAsk(tool, found))
}

// mcpToolCandidates is the candidates of a tool-level MCP query: a Pi MCP tool's identity is its name, so its
// tool-level state and whether it is fully denied come from the name alone (resolvePiMcpTool).
func mcpToolCandidates(tool string, configured []string) (surface, target string, values []string) {
	return normalizeMcpTargets(createPiMcpToolTargets(tool, configured))
}
