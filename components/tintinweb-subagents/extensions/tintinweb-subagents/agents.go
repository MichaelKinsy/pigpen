// Package tintinweb_subagents is a Go port of @tintinweb/pi-subagents 0.19.0 (partial): agent types, custom agent
// files, the Agent, get_subagent_result and steer_subagent tools, background agents, and the event-bus protocol
// @tintinweb/pi-tasks speaks. See port/PORT.md for what is and is not ported.
package tintinweb_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// agentConfig is one agent type. upstream: src/types.ts AgentConfig.
type agentConfig struct {
	Name, DisplayName, Color, Description string
	BuiltinToolNames                      []string // nil: all built-in tools
	ExtSelectors                          []string
	DisallowedTools                       []string
	Extensions                            any // true, false or []string
	ExcludeExtensions                     []string
	Skills                                any // true, false or []string
	Model, Thinking                       string
	MaxTurns                              *int
	SystemPrompt                          string
	PromptMode                            string // "replace" or "append"
	InheritContext, RunInBackground       *bool
	Isolated                              *bool
	PersistSession, OutputTranscript      *bool
	SessionDir                            string
	AllowedSubagents                      any // nil, "all" or []string. Parsed; nested delegation is not part of this port
	Memory, Isolation                     string
	Enabled                               bool
	IsDefault                             bool
	Source, SourcePath                    string
}

// builtinToolNames are the built-in tools an agent can be given. upstream: agent-types.ts BUILTIN_TOOL_NAMES.
var builtinToolNames = []string{"read", "bash", "edit", "write", "grep", "find", "ls"}

var readOnlyTools = []string{"read", "bash", "grep", "find", "ls"}

// defaultAgents are the three built-in agent types, in the original's order. upstream: src/default-agents.ts.
func defaultAgents() *agentRegistry {
	r := newRegistry()
	r.set(&agentConfig{Name: "general-purpose", DisplayName: "Agent", Description: defaultGeneralPurposeDescription, Extensions: true, Skills: true,
		SystemPrompt: defaultGeneralPurposePrompt, PromptMode: "append", IsDefault: true, Enabled: true})
	r.set(&agentConfig{Name: "Explore", DisplayName: "Explore", Description: defaultExploreDescription, BuiltinToolNames: readOnlyTools, Extensions: true, Skills: true,
		Model: "anthropic/claude-haiku-4-5", SystemPrompt: defaultExplorePrompt, PromptMode: "replace", IsDefault: true, Enabled: true})
	r.set(&agentConfig{Name: "Plan", DisplayName: "Plan", Description: defaultPlanDescription, BuiltinToolNames: readOnlyTools, Extensions: true, Skills: true,
		SystemPrompt: defaultPlanPrompt, PromptMode: "replace", IsDefault: true, Enabled: true})
	return r
}

// agentRegistry is an insertion-ordered map of agent types.
type agentRegistry struct {
	keys []string
	m    map[string]*agentConfig
}

func newRegistry() *agentRegistry { return &agentRegistry{m: map[string]*agentConfig{}} }

func (r *agentRegistry) set(c *agentConfig) {
	if _, ok := r.m[c.Name]; !ok {
		r.keys = append(r.keys, c.Name)
	}
	r.m[c.Name] = c
}

// resolveKey is an exact name, else a case-insensitive match. upstream: agent-types.ts resolveKeyIn.
func (r *agentRegistry) resolveKey(name string) (string, bool) {
	if _, ok := r.m[name]; ok {
		return name, true
	}
	lower := strings.ToLower(name)
	for _, k := range r.keys {
		if strings.ToLower(k) == lower {
			return k, true
		}
	}
	return "", false
}

// unambiguous is the case-insensitive match that exactly one key has.
func (r *agentRegistry) unambiguous(name string) (string, bool) {
	if _, ok := r.m[name]; ok {
		return name, true
	}
	var found []string
	for _, k := range r.keys {
		if strings.ToLower(k) == strings.ToLower(name) {
			found = append(found, k)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return "", false
}

func (r *agentRegistry) get(name string) *agentConfig {
	if k, ok := r.resolveKey(name); ok {
		return r.m[k]
	}
	return nil
}

// availableTypes are the enabled agent types. upstream: getAvailableTypesIn.
func (r *agentRegistry) availableTypes() []string {
	var out []string
	for _, k := range r.keys {
		if r.m[k].Enabled {
			out = append(out, k)
		}
	}
	return out
}

// agentDir is PiG's writable agent directory (PIG_CODING_AGENT_DIR, else <PIG_HOME or ~/.pig>/agent; with
// PIG_USE_PI_DIRS=1 Pi's), the counterpart of getAgentDir() under divergence D2.
func agentDir() string {
	if os.Getenv("PIG_USE_PI_DIRS") == "1" {
		if v := os.Getenv("PI_CODING_AGENT_DIR"); v != "" {
			return expandTilde(v)
		}
		h, _ := os.UserHomeDir()
		return filepath.Join(h, ".pi", "agent")
	}
	if v := os.Getenv("PIG_CODING_AGENT_DIR"); v != "" {
		return expandTilde(v)
	}
	if v := os.Getenv("PIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "agent")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "pig", "agent")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".pig", "agent")
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[1:])
	}
	return p
}

// loadCustomAgents reads <agent dir>/agents, <cwd>/.agents/agents and <cwd>/.pi/agents, later ones overriding.
// A file that cannot be read is skipped with a warning. The warnings returned are the ones not already reported by
// the previous load: a problem is reported when it appears, not on every reload while it stays. upstream:
// custom-agents.ts loadCustomAgents.
func loadCustomAgents(cwd string) (*agentRegistry, []string) {
	agents, warnings, _ := loadCustomAgentsStrict(cwd, false)
	return agents, warnings
}

var (
	warnMu     sync.Mutex
	warnedLast = map[string]bool{}
)

// loadCustomAgentsStrict fails on the first agent file that cannot be parsed, naming it, when strict is set.
func loadCustomAgentsStrict(cwd string, strict bool) (*agentRegistry, []string, error) {
	agents := newRegistry()
	var all []string
	for _, d := range []struct{ dir, source string }{
		{filepath.Join(agentDir(), "agents"), "global"},
		{filepath.Join(cwd, ".agents", "agents"), "project"},
		{filepath.Join(cwd, ".pi", "agents"), "project"},
	} {
		if err := loadFromDir(d.dir, d.source, agents, &all, strict); err != nil {
			return nil, nil, err
		}
	}
	warnMu.Lock()
	defer warnMu.Unlock()
	this := map[string]bool{}
	var fresh []string
	for _, w := range all {
		if !this[w] && !warnedLast[w] {
			fresh = append(fresh, w)
		}
		this[w] = true
	}
	warnedLast = this
	return agents, fresh, nil
}

func loadFromDir(dir, source string, agents *agentRegistry, warnings *[]string, strict bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			files = append(files, e.Name())
		}
	}
	slices.Sort(files)
	for _, file := range files {
		filenameType := strings.TrimSuffix(file, ".md")
		path := filepath.Join(dir, file)
		data, err := os.ReadFile(path)
		var fm map[string]any
		var body string
		if err == nil {
			fm, body, err = parseFrontmatter(string(data))
		}
		if err != nil {
			if strict {
				return fmt.Errorf("%s: %v", path, err)
			}
			*warnings = append(*warnings, fmt.Sprintf("Skipping agent file %s: %v", path, err))
			if surviving := agents.m[filenameType]; surviving != nil && surviving.SourcePath != "" && surviving.Enabled {
				*warnings = append(*warnings, fmt.Sprintf("Agent \"%s\" now loads from %s instead", filenameType, surviving.SourcePath))
			}
			continue
		}
		declared := strings.TrimSpace(str(fm["name"]))
		if strings.Contains(declared, ":") {
			*warnings = append(*warnings, fmt.Sprintf("Agent file %s declares name \"%s\", which contains \":\" — reserved for plugin-scoped identifiers. Rename it, or move the label to `display_name:`. Skipping.", path, declared))
			continue
		}
		name := declared
		if name == "" {
			name = filenameType
		}
		builtin, ext := parseToolsField(fm["tools"])
		desc, hasDesc := fm["description"].(string)
		if !hasDesc {
			desc = name
		}
		cfg := &agentConfig{
			Name: name, DisplayName: str(fm["display_name"]), Color: str(fm["color"]), Description: desc,
			BuiltinToolNames: builtin, ExtSelectors: ext, DisallowedTools: csvOptional(fm["disallowed_tools"]),
			Extensions: inheritField(first(fm, "extensions", "inherit_extensions")), ExcludeExtensions: csvOptional(fm["exclude_extensions"]),
			Skills: inheritField(first(fm, "skills", "inherit_skills")), Model: str(fm["model"]), Thinking: str(fm["thinking"]),
			MaxTurns: nonNegativeInt(fm["max_turns"]), PersistSession: boolPtr(fm, "persist_session", true), OutputTranscript: notFalsePtr(fm, "output_transcript"),
			SessionDir: str(fm["session_dir"]), AllowedSubagents: parseAllowedSubagents(fm["allowed_subagents"]), Memory: parseMemory(fm["memory"]), Isolation: parseIsolation(fm["isolation"]), SystemPrompt: strings.TrimSpace(body), PromptMode: "replace",
			InheritContext: boolPtr(fm, "inherit_context", true), RunInBackground: boolPtr(fm, "run_in_background", true), Isolated: boolPtr(fm, "isolated", true),
			Enabled: fm["enabled"] != false, Source: source, SourcePath: path,
		}
		if fm["prompt_mode"] == "append" {
			cfg.PromptMode = "append"
		}
		agents.set(cfg)
	}
	return nil
}

func first(m map[string]any, a, b string) any {
	if v, ok := m[a]; ok && v != nil {
		return v
	}
	return m[b]
}

func str(v any) string { s, _ := v.(string); return s }

func nonNegativeInt(v any) *int {
	if f, ok := v.(float64); ok && f >= 0 {
		n := int(f)
		return &n
	}
	return nil
}

// boolPtr is `fm.k != null ? fm.k === true : undefined`.
func boolPtr(fm map[string]any, key string, equalsTrue bool) *bool {
	v, ok := fm[key]
	if !ok || v == nil {
		return nil
	}
	b := v == true
	return &b
}

// notFalsePtr is `fm.k != null ? fm.k !== false : undefined`.
func notFalsePtr(fm map[string]any, key string) *bool {
	v, ok := fm[key]
	if !ok || v == nil {
		return nil
	}
	b := v != false
	return &b
}

func parseAllowedSubagents(v any) any {
	if b, ok := v.(bool); ok {
		if b {
			return "all"
		}
		return nil
	}
	items := parseCsvField(v)
	if items == nil {
		return nil
	}
	for _, i := range items {
		if i == "*" || strings.ToLower(i) == "all" {
			return "all"
		}
	}
	return items
}

func parseMemory(v any) string {
	if s, ok := v.(string); ok && (s == "user" || s == "project" || s == "local") {
		return s
	}
	return ""
}

func parseIsolation(v any) string {
	switch v {
	case "worktree":
		return "worktree"
	case "off", "none", "no", false:
		return "off"
	}
	return ""
}

// parseCsvField is a comma list; "none" and empty read as none. upstream: custom-agents.ts parseCsvField.
func parseCsvField(v any) []string {
	if v == nil {
		return nil
	}
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case bool:
		s = fmt.Sprint(t)
	case float64:
		s = fmt.Sprint(t)
	case []any:
		var parts []string
		for _, e := range t {
			parts = append(parts, fmt.Sprint(e))
		}
		s = strings.Join(parts, ",")
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "none" {
		return nil
	}
	var items []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			items = append(items, p)
		}
	}
	return items
}

func csvOptional(v any) []string { return parseCsvField(v) }

func csvList(v any, defaults []string) []string {
	if v == nil {
		return defaults
	}
	if l := parseCsvField(v); l != nil {
		return l
	}
	return []string{}
}

func isWildcard(e string) bool { return e == "*" || strings.ToLower(e) == "all" }

// parseToolsField splits `tools:` into built-in names and `ext:` selectors. upstream: custom-agents.ts parseToolsField.
func parseToolsField(v any) (builtin, ext []string) {
	entries := csvList(v, builtinToolNames)
	hasWildcard := slices.ContainsFunc(entries, isWildcard)
	var plain, extEntries []string
	for _, e := range entries {
		switch {
		case isWildcard(e):
		case strings.HasPrefix(e, "ext:"):
			extEntries = append(extEntries, e)
		default:
			plain = append(plain, e)
		}
	}
	if hasWildcard {
		builtin = slices.Clone(builtinToolNames)
		for _, p := range plain {
			if !slices.Contains(builtin, p) {
				builtin = append(builtin, p)
			}
		}
	} else {
		builtin = plain
		if builtin == nil {
			builtin = []string{}
		}
	}
	if len(extEntries) > 0 {
		ext = extEntries
	}
	return builtin, ext
}

// inheritField is true (inherit all), false (none) or an explicit list. upstream: custom-agents.ts inheritField.
func inheritField(v any) any {
	if v == nil || v == true {
		return true
	}
	if v == false || v == "none" {
		return false
	}
	if items := csvList(v, []string{}); len(items) > 0 {
		return items
	}
	return false
}
