package pi_subagents

import (
	"embed"
	"sort"
	"strings"
)

//go:embed builtin/*.md
var builtinFS embed.FS

func defaultSystemPromptMode(name string) string {
	if name == "delegate" {
		return "append"
	}
	return "replace"
}

func defaultInheritProjectContext(name string) bool { return name == "delegate" }

func splitTools(raw []string) (tools, mcp []string) {
	tools = []string{}
	for _, t := range raw {
		if strings.HasPrefix(t, "mcp:") {
			mcp = append(mcp, t[4:])
		} else {
			tools = append(tools, t)
		}
	}
	return
}

func normalizeAliases(raw []string, agentName string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range raw {
		a = jsTrim(a)
		if a == "" || seen[a] || a == agentName {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

func optional(fm Frontmatter, k string) *string {
	if !fm.Has(k) {
		return nil
	}
	v := fm.Get(k)
	return &v
}

type definitionFile struct{ path, content string }

// builtinAgents loads the agents that ship with the package (the original's agents directory, unmodified).
func builtinAgents() ([]AgentConfig, []Diagnostic) {
	entries, _ := builtinFS.ReadDir("builtin")
	var files []definitionFile
	for _, e := range entries {
		b, err := builtinFS.ReadFile("builtin/" + e.Name())
		if err == nil {
			files = append(files, definitionFile{"builtin/" + e.Name(), string(b)})
		}
	}
	sort.Slice(files, func(i, j int) bool { return localeLess(files[i].path, files[j].path) })
	return loadAgentsFromDefinitionFiles(files, SourceBuiltin, nil, nil)
}

// localeLess approximates String.prototype.localeCompare for file and agent names: case-insensitive first,
// lower case before upper case on a tie.
func localeLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a > b
}
