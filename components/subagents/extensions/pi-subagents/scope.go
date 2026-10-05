package pi_subagents

import "strings"

// ResolveExecutionAgentScope returns the scope a tool call asked for, "both" when it is absent or not one of the three.
func ResolveExecutionAgentScope(scope any) AgentScope {
	if s, ok := scope.(string); ok && (s == "user" || s == "project" || s == "both") {
		return AgentScope(s)
	}
	return ScopeBoth
}

// MergeAgentsForScope merges the sources by name; a later one wins and keeps the earlier one's position
// (a JavaScript Map): builtin, package, then user and project as the scope allows.
func MergeAgentsForScope(scope AgentScope, user, project, builtin, pkg []AgentConfig) []AgentConfig {
	var order []string
	byName := map[string]AgentConfig{}
	put := func(list []AgentConfig) {
		for _, a := range list {
			if _, ok := byName[a.Name]; !ok {
				order = append(order, a.Name)
			}
			byName[a.Name] = a
		}
	}
	put(builtin)
	put(pkg)
	switch scope {
	case ScopeBoth:
		put(user)
		put(project)
	case ScopeUser:
		put(user)
	default:
		put(project)
	}
	out := make([]AgentConfig, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out
}

var sourceRank = map[AgentSource]int{SourceBuiltin: 0, SourcePackage: 1, SourceUser: 2, SourceProject: 3}

// effectiveMatch picks the highest-precedence definition when every match carries the same name.
func effectiveMatch(matches []*AgentConfig) *AgentConfig {
	for _, m := range matches[1:] {
		if m.Name != matches[0].Name {
			return nil
		}
	}
	best := matches[0]
	for _, m := range matches[1:] {
		if sourceRank[m.Source] > sourceRank[best.Source] {
			best = m
		}
	}
	return best
}

// ResolveAgentName finds an agent by canonical name, then local (package-less) name, then alias; an ambiguous
// name returns the message and no agent, an unknown one neither.
func ResolveAgentName(name string, agents []AgentConfig) (*AgentConfig, string) {
	raw := jsTrim(name)
	pick := func(match func(a *AgentConfig) bool, what string) (*AgentConfig, string, bool) {
		var ms []*AgentConfig
		for i := range agents {
			if match(&agents[i]) {
				ms = append(ms, &agents[i])
			}
		}
		switch {
		case len(ms) == 1:
			return ms[0], "", true
		case len(ms) > 1:
			if e := effectiveMatch(ms); e != nil {
				return e, "", true
			}
			names := make([]string, len(ms))
			for i, m := range ms {
				names[i] = m.Name
			}
			return nil, "Ambiguous " + what + " '" + name + "': " + strings.Join(names, ", "), true
		}
		return nil, "", false
	}
	if a, msg, ok := pick(func(a *AgentConfig) bool { return a.Name == raw }, "agent name"); ok {
		return a, msg
	}
	if a, msg, ok := pick(func(a *AgentConfig) bool { return a.LocalName != "" && a.LocalName == raw }, "local agent name"); ok {
		return a, msg
	}
	if a, msg, ok := pick(func(a *AgentConfig) bool {
		for _, x := range a.Aliases {
			if x == raw {
				return true
			}
		}
		return false
	}, "agent alias"); ok {
		return a, msg
	}
	return nil, ""
}
