package pi_subagents

import (
	"embed"
	"fmt"
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

// parseRunner reads the `runner:` block (a flat YAML mapping): the type and the command.
func parseRunner(raw string, agent string) (*Runner, error) {
	if jsTrim(raw) == "" {
		return nil, nil
	}
	kv := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		if k, v, ok := matchKeyLine(line); ok {
			kv[k] = strings.Trim(jsTrim(v), `"'`)
		}
	}
	switch kv["type"] {
	case "pi":
		return &Runner{Type: "pi"}, nil
	case "external-job":
		return &Runner{Type: "external-job", Command: kv["provider"]}, nil
	case "external-cli":
		if jsTrim(kv["command"]) == "" {
			return nil, fmt.Errorf("Agent '%s' external-cli runner requires a non-empty command string.", agent)
		}
		return &Runner{Type: "external-cli", Command: jsTrim(kv["command"])}, nil
	}
	return nil, fmt.Errorf("Agent '%s' has invalid runner.type; expected 'pi', 'external-cli', or 'external-job'.", agent)
}

type definitionFile struct{ path, content string }

// loadAgents turns definition files into agents; a file that fails becomes a diagnostic and does not stop the others.
func loadAgents(files []definitionFile, source AgentSource, priority int) ([]AgentConfig, []Diagnostic) {
	var agents []AgentConfig
	var diags []Diagnostic
	for _, f := range files {
		a, d := loadAgent(f, source, priority)
		if d != nil {
			diags = append(diags, *d)
		} else if a != nil {
			agents = append(agents, *a)
		}
	}
	return agents, diags
}

func loadAgent(f definitionFile, source AgentSource, priority int) (*AgentConfig, *Diagnostic) {
	fm, body := ParseFrontmatter(f.content)
	if fm.Get("name") == "" || fm.Get("description") == "" {
		return nil, nil
	}
	local := fm.Get("name")
	diag := func(err string) *Diagnostic {
		return &Diagnostic{Source: source, Name: local, FilePath: f.path, Error: err}
	}
	pkg, perr := parsePackageName(fm.Get("package"), fm.Has("package"), fmt.Sprintf("Agent '%s' package", local))
	if perr != "" {
		return nil, diag(perr)
	}
	runtime := buildRuntimeName(local, pkg)
	var runner *Runner
	if fm.Has("runner") {
		r, err := parseRunner(fm.Get("runner"), local)
		if err != nil {
			return nil, diag(err.Error())
		}
		runner = r
	}
	a := &AgentConfig{Name: runtime, LocalName: local, PackageName: pkg, Description: fm.Get("description"), Runner: runner,
		SystemPrompt: body, Source: source, FilePath: f.path, discoveryPriority: priority}
	if raw := ParseFrontmatterList(optional(fm, "tools")); raw != nil {
		a.Tools, a.McpDirectTools = splitTools(raw)
	}
	aliasKey := "aliases"
	if !fm.Has("aliases") {
		aliasKey = "alias"
	}
	a.Aliases = normalizeAliases(ParseFrontmatterList(optional(fm, aliasKey)), runtime)
	skillKey := fm.Get("skill")
	if skillKey == "" {
		skillKey = fm.Get("skills")
	}
	if skillKey != "" || fm.Has("skill") || fm.Has("skills") {
		if s := ParseFrontmatterList(&skillKey); len(s) > 0 {
			a.Skills = s
		}
	}
	if fm.Has("model") {
		a.Model = fm.Get("model")
	}
	if fm.Has("thinking") {
		if fm.Get("thinking") == "false" {
			a.Thinking = false
		} else {
			a.Thinking = fm.Get("thinking")
		}
	}
	switch fm.Get("systemPromptMode") {
	case "replace", "append":
		a.SystemPromptMode = fm.Get("systemPromptMode")
	default:
		a.SystemPromptMode = defaultSystemPromptMode(local)
	}
	switch fm.Get("inheritProjectContext") {
	case "true":
		a.InheritProjectContext = true
	case "false":
	default:
		a.InheritProjectContext = defaultInheritProjectContext(local)
	}
	a.InheritGlobalContext = fm.Get("inheritGlobalContext") == "true"
	a.InheritSkills = fm.Get("inheritSkills") == "true"
	if c := fm.Get("defaultContext"); c == "fork" || c == "fresh" {
		a.DefaultContext = c
	}
	if fm.Has("async") {
		switch fm.Get("async") {
		case "true", "false":
			b := fm.Get("async") == "true"
			a.DefaultAsync = &b
		default:
			return nil, diag(fmt.Sprintf("Agent '%s' has invalid async frontmatter; expected true or false.", local))
		}
	}
	if fm.Has("acceptanceRole") {
		a.AcceptanceRole = fm.Get("acceptanceRole")
	}
	if reads := ParseFrontmatterList(optional(fm, "defaultReads")); len(reads) > 0 {
		a.DefaultReads = reads
	}
	a.DefaultProgress = fm.Get("defaultProgress") == "true"
	return a, nil
}

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
	return loadAgents(files, SourceBuiltin, 0)
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
