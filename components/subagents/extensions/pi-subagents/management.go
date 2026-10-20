package pi_subagents

import (
	"sort"
	"strings"
)

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
