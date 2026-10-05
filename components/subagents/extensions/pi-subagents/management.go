package pi_subagents

import (
	"fmt"
	"sort"
	"strings"
)

// manageResult is a management action's outcome: the text the model sees, and whether it is an error.
type manageResult struct {
	text    string
	isError bool
}

func normalizeListScope(v any) (AgentScope, bool) {
	if v == nil {
		return ScopeBoth, true
	}
	if s, ok := v.(string); ok && (s == "user" || s == "project" || s == "both") {
		return AgentScope(s), true
	}
	return "", false
}

// sanitizeName is the original's sanitizeName: lower case, white space to dashes, only [a-z0-9-].
func sanitizeName(name string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range strings.ToLower(jsTrim(name)) {
		if isJSSpace(r) {
			if !inSpace {
				b.WriteByte('-')
			}
			inSpace = true
			continue
		}
		inSpace = false
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	s := dashRun.ReplaceAllString(b.String(), "-")
	return strings.Trim(s, "-")
}

func allAgents(d DiscoveryAll) []AgentConfig {
	var out []AgentConfig
	for _, l := range [][]AgentConfig{d.Builtin, d.Package, d.User, d.Project} {
		out = append(out, l...)
	}
	return out
}

func sortedNames(agents []AgentConfig) []string {
	seen := map[string]bool{}
	var names []string
	for _, a := range agents {
		if !seen[a.Name] {
			seen[a.Name] = true
			names = append(names, a.Name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return localeLess(names[i], names[j]) })
	return names
}

func runnerBadge(a AgentConfig) string {
	if a.Runner != nil && a.Runner.Type == "external-cli" {
		return "external-cli:" + a.Runner.Command
	}
	return ""
}

func listLine(a AgentConfig) string {
	parts := []string{string(a.Source)}
	if b := runnerBadge(a); b != "" {
		parts = append(parts, b)
	}
	if a.DefaultContext != "" {
		parts = append(parts, "context: "+a.DefaultContext)
	}
	if len(a.Aliases) > 0 {
		parts = append(parts, "aliases: "+strings.Join(a.Aliases, ", "))
	}
	return fmt.Sprintf("- %s (%s): %s", a.Name, strings.Join(parts, ", "), a.Description)
}

func diagnosticLines(diags []Diagnostic) []string {
	if len(diags) == 0 {
		return nil
	}
	lines := []string{"", "Invalid agent definitions:"}
	for _, d := range diags {
		name := d.Name
		if name == "" {
			name = d.FilePath
		}
		lines = append(lines, fmt.Sprintf("- %s (%s): %s", name, d.Source, d.Error))
	}
	return lines
}

func diagnosticsForScope(diags []Diagnostic, scope AgentScope) []Diagnostic {
	if scope == ScopeBoth {
		return diags
	}
	excluded := SourceProject
	if scope == ScopeProject {
		excluded = SourceUser
	}
	var out []Diagnostic
	for _, d := range diags {
		if d.Source != excluded {
			out = append(out, d)
		}
	}
	return out
}

// listAgents is the `list` action without capabilities.
func listAgents(cwd string, params map[string]any) manageResult {
	scope, _ := normalizeListScope(params["agentScope"])
	if scope == "" {
		scope = ScopeBoth // the original lists everything for a scope it does not know
	}
	d := DiscoverAgentsAll(cwd)
	agents := MergeAgentsForScope(scope, d.User, d.Project, d.Builtin, d.Package)
	sort.SliceStable(agents, func(i, j int) bool { return localeLess(agents[i].Name, agents[j].Name) })
	lines := []string{"Executable agents:"}
	if len(agents) == 0 {
		lines = append(lines, "- (none)")
	}
	first := true
	for _, sec := range []struct {
		src   AgentSource
		label string
	}{{SourcePackage, "Package agents"}, {SourceUser, "User agents"}, {SourceProject, "Project agents"}, {"runtime", "Runtime agents"}, {SourceBuiltin, "Builtin agents"}} {
		var section []string
		for _, a := range agents {
			if a.Source == sec.src {
				section = append(section, listLine(a))
			}
		}
		if len(section) == 0 {
			continue
		}
		if !first {
			lines = append(lines, "")
		}
		first = false
		lines = append(lines, sec.label)
		lines = append(lines, section...)
	}
	lines = append(lines, diagnosticLines(d.Diagnostics)...)
	return manageResult{text: strings.Join(lines, "\n")}
}

func formatDetail(a AgentConfig) string {
	var tools []string
	tools = append(tools, a.Tools...)
	for _, t := range a.McpDirectTools {
		tools = append(tools, "mcp:"+t)
	}
	lines := []string{fmt.Sprintf("Agent: %s (%s)", a.Name, a.Source), "Path: " + a.FilePath, "Description: " + a.Description}
	if a.PackageName != "" {
		lines = append(lines, "Local name: "+frontmatterName(a.Name, a.LocalName, a.PackageName), "Package: "+a.PackageName)
	}
	if len(a.Aliases) > 0 {
		lines = append(lines, "Aliases: "+strings.Join(a.Aliases, ", "))
	}
	if a.Model != "" {
		lines = append(lines, "Model: "+a.Model)
	}
	if len(tools) > 0 {
		lines = append(lines, "Tools: "+strings.Join(tools, ", "))
	}
	if len(a.Skills) > 0 {
		lines = append(lines, "Skills: "+strings.Join(a.Skills, ", "))
	}
	lines = append(lines, "System prompt mode: "+a.SystemPromptMode)
	if a.Runner != nil {
		switch a.Runner.Type {
		case "external-cli":
			lines = append(lines, "Runner: external-cli "+a.Runner.Command)
		case "pi":
			lines = append(lines, `Runner: {"type":"pi"}`)
		}
	}
	yn := func(b bool) string { return map[bool]string{true: "true", false: "false"}[b] }
	lines = append(lines, "Inherit project context: "+yn(a.InheritProjectContext), "Inherit global context: "+yn(a.InheritGlobalContext), "Inherit skills: "+yn(a.InheritSkills))
	if a.DefaultContext != "" {
		lines = append(lines, "Default context: "+a.DefaultContext)
	}
	if a.DefaultAsync != nil {
		lines = append(lines, "Async: "+yn(*a.DefaultAsync))
	}
	if a.AcceptanceRole != "" {
		lines = append(lines, "Acceptance role: "+a.AcceptanceRole)
	}
	if a.Source == SourceBuiltin {
		lines = append(lines, "Disabled: false")
	}
	if s, ok := a.Thinking.(string); ok && s != "" {
		lines = append(lines, "Thinking: "+s)
	}
	if len(a.DefaultReads) > 0 {
		lines = append(lines, "Reads: "+strings.Join(a.DefaultReads, ", "))
	}
	if a.DefaultProgress {
		lines = append(lines, "Progress: true")
	}
	if jsTrim(a.SystemPrompt) != "" {
		lines = append(lines, "", "System Prompt:", a.SystemPrompt)
	}
	return strings.Join(lines, "\n")
}

// findAgents is the original's findAgentsInDiscovery: the agents a name resolves to in a scope.
func findAgents(name string, scope AgentScope, d DiscoveryAll) []AgentConfig {
	raw := jsTrim(name)
	sanitized := sanitizeName(raw)
	scoped := MergeAgentsForScope(scope, d.User, d.Project, d.Builtin, d.Package)
	a, msg := ResolveAgentName(raw, scoped)
	if a == nil && msg == "" && sanitized != raw {
		a, msg = ResolveAgentName(sanitized, scoped)
	}
	var out []AgentConfig
	if a != nil {
		for _, x := range scoped {
			if x.Name == a.Name {
				out = append(out, x)
			}
		}
	} else {
		for _, x := range scoped {
			one := []AgentConfig{x}
			if m, _ := ResolveAgentName(raw, one); m != nil {
				out = append(out, x)
			} else if sanitized != raw {
				if m, _ := ResolveAgentName(sanitized, one); m != nil {
					out = append(out, x)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// blockingDiagnostic: a malformed definition of the name that is not shadowed by a valid one of equal or higher precedence.
func blockingDiagnostic(name string, agents []AgentConfig, diags []Diagnostic) *Diagnostic {
	name = jsTrim(name)
	var match *Diagnostic
	for i := range diags {
		if diags[i].Name == name && (match == nil || sourceRank[diags[i].Source] > sourceRank[match.Source]) {
			match = &diags[i]
		}
	}
	best := -1
	for _, a := range agents {
		if r := sourceRank[a.Source]; r > best {
			best = r
		}
	}
	if len(agents) == 0 || (match != nil && sourceRank[match.Source] > best) {
		return match
	}
	return nil
}

// getAgent is the `get` action.
func getAgent(cwd string, params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if name == "" {
		return manageResult{"Specify 'agent' for get.", true}
	}
	scope, ok := normalizeListScope(params["agentScope"])
	if !ok {
		return manageResult{"agentScope must be 'user', 'project', or 'both' for get.", true}
	}
	d := DiscoverAgentsAll(cwd)
	matches := findAgents(name, scope, d)
	diags := diagnosticsForScope(d.Diagnostics, scope)
	raw := jsTrim(name)
	diag := blockingDiagnostic(raw, matches, diags)
	if diag == nil && sanitizeName(raw) != raw {
		diag = blockingDiagnostic(sanitizeName(raw), matches, diags)
	}
	if diag != nil {
		return manageResult{fmt.Sprintf("Agent '%s' has invalid configuration: %s", name, diag.Error), true}
	}
	names := sortedNames(matches)
	if len(names) > 1 {
		return manageResult{fmt.Sprintf("Ambiguous agent alias or name '%s': %s", name, strings.Join(names, ", ")), true}
	}
	if len(matches) == 0 {
		avail := strings.Join(sortedNames(allAgents(d)), ", ")
		if avail == "" {
			avail = "none"
		}
		return manageResult{fmt.Sprintf("Agent '%s' not found. Available: %s.", name, avail), true}
	}
	details := make([]string, len(matches))
	for i, m := range matches {
		details[i] = formatDetail(m)
	}
	return manageResult{text: strings.Join(details, "\n\n")}
}
