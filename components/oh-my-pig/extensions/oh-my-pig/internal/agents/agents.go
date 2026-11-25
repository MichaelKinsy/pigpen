package agents

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

//go:embed prompts/agents/*.md
var bundledAgentsFS embed.FS

//go:embed prompts/tools/*.md
var toolPromptsFS embed.FS

//go:embed prompts/urls/*.md
var urlPromptsFS embed.FS

// DefaultSpawnAgent is omp's DEFAULT_SPAWN_AGENT.
// upstream: packages/coding-agent/src/task/spawn-policy.ts:2
const DefaultSpawnAgent = "task"

// ReadOnlyToolNames is the set of built-in tools whose approval tier is "read".
// An agent is read-only iff its declared tools are a non-empty subset of this set.
// upstream: packages/coding-agent/src/task/read-only-policy.ts:7
var ReadOnlyToolNames = map[string]bool{
	"read":        true,
	"wait":        true,
	"grep":        true,
	"glob":        true,
	"find":        true,
	"web_search":  true,
	"ast_grep":    true,
	"yield":       true,
	"ask":         true,
	"todo":        true,
	"recall":      true,
	"reflect":     true,
	"retain":      true,
	"memory_edit": true,
	"checkpoint":  true,
	"rewind":      true,
}

// IsReadOnlyAgent reports whether agent declares a non-empty tools list
// containing exclusively read-only tools.
// upstream: packages/coding-agent/src/task/read-only-policy.ts:26
func IsReadOnlyAgent(agent AgentDefinition) bool {
	if len(agent.Tools) == 0 {
		return false
	}
	for _, t := range agent.Tools {
		if !ReadOnlyToolNames[t] {
			return false
		}
	}
	return true
}

// PlanModeTools is omp's PLAN_MODE_TOOLS allowlist for subagents spawned while
// plan mode is active.
// upstream: packages/coding-agent/src/task/structured-subagent.ts:263
var PlanModeTools = []string{"read", "grep", "glob", "web_search"}

// PlanModeSubagentPrompt returns omp's embedded prompts/system/plan-mode-subagent.md.
func PlanModeSubagentPrompt() string {
	data, err := toolPromptsFS.ReadFile("prompts/tools/plan-mode-subagent.md")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// CreatePlanModeAgent transforms agent into omp's read-only plan-mode agent:
// prepends prompts/system/plan-mode-subagent.md to SystemPrompt, restricts
// Tools to PLAN_MODE_TOOLS plus `ast_grep` if the agent already declared it,
// and clears Spawns.
// upstream: packages/coding-agent/src/task/structured-subagent.ts:306-315
func CreatePlanModeAgent(agent AgentDefinition) AgentDefinition {
	tools := make([]string, 0, len(PlanModeTools)+1)
	tools = append(tools, PlanModeTools...)
	for _, t := range agent.Tools {
		if t == "ast_grep" {
			tools = append(tools, "ast_grep")
			break
		}
	}
	out := agent
	if prefix := PlanModeSubagentPrompt(); prefix != "" {
		if strings.TrimSpace(agent.SystemPrompt) != "" {
			out.SystemPrompt = prefix + "\n\n" + agent.SystemPrompt
		} else {
			out.SystemPrompt = prefix
		}
	}
	out.Tools = tools
	out.Spawns = nil
	return out
}

// canonicalToolNames matches omp's BUILTIN_TOOL_NAMES + HIDDEN_TOOL_NAMES.
// upstream: packages/coding-agent/src/tools/builtin-names.ts
var canonicalToolNames = map[string]bool{
	"read":          true,
	"bash":          true,
	"edit":          true,
	"ast_grep":      true,
	"ast_edit":      true,
	"ask":           true,
	"debug":         true,
	"eval":          true,
	"github":        true,
	"glob":          true,
	"grep":          true,
	"find":          true,
	"lsp":           true,
	"checkpoint":    true,
	"rewind":        true,
	"context_notes": true,
	"new_context":   true,
	"security_scan": true,
	"task":          true,
	"wait":          true,
	"todo":          true,
	"web_search":    true,
	"write":         true,
	"memory_edit":   true,
	"retain":        true,
	"recall":        true,
	"reflect":       true,
	"learn":         true,
	"manage_skill":  true,
	"yield":         true,
	"goal":          true,
	"think":         true,
}

// NormalizeToolName canonicalizes omp built-in tool names and legacy aliases.
// upstream: packages/coding-agent/src/tools/builtin-names.ts:96
func NormalizeToolName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "search" {
		return "grep"
	}
	if canonicalToolNames[lower] {
		return lower
	}
	return strings.TrimSpace(name)
}

// NormalizeToolNames deduplicates and canonicalizes tool names in first-seen order.
// upstream: packages/coding-agent/src/tools/builtin-names.ts:132
func NormalizeToolNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		norm := NormalizeToolName(n)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	return out
}

// SpawnPolicy is the resolved spawn policy derived from a parent's `spawns` setting.
// upstream: packages/coding-agent/src/task/spawn-policy.ts:5
type SpawnPolicy struct {
	Enabled           bool
	DefaultAgent      string
	AllowedAgents     []string // nil means unrestricted ("*")
	AllowedErrorText  string
	AllowedPromptText string
}

// ResolveSpawnPolicy parses a session spawns string ("*", "", "scout,reviewer").
// upstream: packages/coding-agent/src/task/spawn-policy.ts:19
func ResolveSpawnPolicy(parentSpawns string) SpawnPolicy {
	normalized := strings.TrimSpace(parentSpawns)
	if normalized == "*" {
		return SpawnPolicy{
			Enabled:          true,
			DefaultAgent:     DefaultSpawnAgent,
			AllowedAgents:    nil,
			AllowedErrorText: "*",
		}
	}
	var allowed []string
	for _, part := range strings.Split(normalized, ",") {
		if p := strings.TrimSpace(part); p != "" {
			allowed = append(allowed, p)
		}
	}
	if len(allowed) == 0 {
		return SpawnPolicy{
			Enabled:          false,
			DefaultAgent:     DefaultSpawnAgent,
			AllowedAgents:    []string{},
			AllowedErrorText: "none (spawns disabled for this agent)",
		}
	}
	quoted := make([]string, len(allowed))
	for i, a := range allowed {
		quoted[i] = "`" + a + "`"
	}
	return SpawnPolicy{
		Enabled:           true,
		DefaultAgent:      allowed[0],
		AllowedAgents:     allowed,
		AllowedErrorText:  strings.Join(allowed, ","),
		AllowedPromptText: strings.Join(quoted, ", "),
	}
}

// Allows reports whether agentName is permitted by this policy.
func (p SpawnPolicy) Allows(agentName string) bool {
	if !p.Enabled {
		return false
	}
	if p.AllowedAgents == nil {
		return true
	}
	for _, a := range p.AllowedAgents {
		if a == agentName {
			return true
		}
	}
	return false
}

// IsScoutSpawnable reports whether `scout` is enabled and permitted.
// upstream: packages/coding-agent/src/task/spawn-policy.ts:64
func IsScoutSpawnable(disabledAgents []string, parentSpawns string) bool {
	for _, d := range disabledAgents {
		if d == "scout" {
			return false
		}
	}
	return ResolveSpawnPolicy(parentSpawns).Allows("scout")
}

// ParseAgent parses an agent markdown file with YAML frontmatter.
// upstream: packages/coding-agent/src/task/agents.ts:103, discovery/helpers.ts:321
func ParseAgent(filePath, content string, source AgentSource) (AgentDefinition, error) {
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		return AgentDefinition{}, fmt.Errorf("failed to parse agent %s: %w", filePath, err)
	}
	name, _ := fm["name"].(string)
	desc, _ := fm["description"].(string)
	name = strings.TrimSpace(name)
	desc = strings.TrimSpace(desc)
	if name == "" || desc == "" {
		return AgentDefinition{}, fmt.Errorf("invalid agent field (missing name or description): %s", filePath)
	}
	lower := strings.ToLower(name)
	if lower == "main" || lower == "sub" {
		return AgentDefinition{}, fmt.Errorf("reserved agent name %q in %s", name, filePath)
	}

	def := AgentDefinition{
		Name:         name,
		Description:  desc,
		SystemPrompt: strings.TrimSpace(body),
		Source:       source,
		FilePath:     filePath,
	}

	if rawTools, ok := fm["tools"]; ok && rawTools != nil {
		def.Tools = NormalizeToolNames(parseStringListOrCSV(rawTools))
	}

	if rawSpawns, ok := fm["spawns"]; ok && rawSpawns != nil {
		def.Spawns = parseSpawnsField(rawSpawns)
	}
	// Backward compat: infer spawns: "*" when tools includes "task".
	// upstream: discovery/helpers.ts:367
	if def.Spawns == nil {
		for _, t := range def.Tools {
			if t == "task" {
				def.Spawns = &SpawnsSpec{All: true}
				break
			}
		}
	}

	if rawModel, ok := fm["model"]; ok && rawModel != nil {
		def.Model = parseStringListOrCSV(rawModel)
	}

	if rawThink, ok := fm["thinking-level"]; ok {
		if s, ok := rawThink.(string); ok {
			def.ThinkingLevel = normalizeThinkingLevel(s)
		}
	} else if rawThink, ok := fm["thinkingLevel"]; ok {
		if s, ok := rawThink.(string); ok {
			def.ThinkingLevel = normalizeThinkingLevel(s)
		}
	} else if rawThink, ok := fm["thinking"]; ok {
		if s, ok := rawThink.(string); ok {
			def.ThinkingLevel = normalizeThinkingLevel(s)
		}
	}

	if rawOut, ok := fm["output"]; ok && rawOut != nil {
		def.Output = rawOut
	}
	if b, ok := parseBoolField(fm["blocking"]); ok {
		def.Blocking = b
	}
	if b, ok := parseBoolField(fm["structured"]); ok {
		def.Structured = b
	}
	if b, ok := parseBoolField(fm["read-summarize"]); ok {
		def.ReadSummarize = &b
	} else if b, ok := parseBoolField(fm["readSummarize"]); ok {
		def.ReadSummarize = &b
	}
	if rawSkills, ok := fm["autoloadSkills"]; ok && rawSkills != nil {
		def.AutoloadSkills = parseStringListOrCSV(rawSkills)
	} else if rawSkills, ok := fm["autoload-skills"]; ok && rawSkills != nil {
		def.AutoloadSkills = parseStringListOrCSV(rawSkills)
	}

	return def, nil
}

func normalizeThinkingLevel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max", "auto", "inherit":
		return strings.ToLower(strings.TrimSpace(s))
	case "med":
		return "medium"
	case "lo":
		return "low"
	case "hi":
		return "high"
	default:
		return strings.TrimSpace(s)
	}
}

func parseBoolField(v any) (bool, bool) {
	switch tv := v.(type) {
	case bool:
		return tv, true
	case string:
		switch strings.ToLower(strings.TrimSpace(tv)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func parseSpawnsField(v any) *SpawnsSpec {
	switch tv := v.(type) {
	case string:
		trimmed := strings.TrimSpace(tv)
		if trimmed == "*" {
			return &SpawnsSpec{All: true}
		}
		list := parseCSV(trimmed)
		if len(list) == 0 {
			return nil
		}
		return &SpawnsSpec{Agents: list}
	case []any:
		list := parseStringListOrCSV(tv)
		if len(list) == 1 && list[0] == "*" {
			return &SpawnsSpec{All: true}
		}
		if len(list) == 0 {
			return nil
		}
		return &SpawnsSpec{Agents: list}
	case []string:
		if len(tv) == 1 && strings.TrimSpace(tv[0]) == "*" {
			return &SpawnsSpec{All: true}
		}
		var out []string
		for _, s := range tv {
			if p := strings.TrimSpace(s); p != "" {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return &SpawnsSpec{Agents: out}
	}
	return nil
}

func parseCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseStringListOrCSV(v any) []string {
	switch tv := v.(type) {
	case string:
		return parseCSV(tv)
	case []string:
		var out []string
		for _, s := range tv {
			if p := strings.TrimSpace(s); p != "" {
				out = append(out, p)
			}
		}
		return out
	case []any:
		var out []string
		for _, item := range tv {
			if s, ok := item.(string); ok {
				if p := strings.TrimSpace(s); p != "" {
					out = append(out, p)
				}
			}
		}
		return out
	}
	return nil
}

// bundledAgentsOnce caches parsed bundled agents.
var (
	bundledOnce   sync.Once
	bundledAgents []AgentDefinition
)

// LoadBundledAgents returns omp v19.1.1's five bundled agents:
// scout, reviewer, security-reviewer, task, sonic.
// upstream: packages/coding-agent/src/task/agents.ts:44-73
func LoadBundledAgents() []AgentDefinition {
	bundledOnce.Do(func() {
		readEmbedded := func(name string) string {
			b, err := bundledAgentsFS.ReadFile("prompts/agents/" + name)
			if err != nil {
				panic("missing embedded agent prompt " + name + ": " + err.Error())
			}
			return string(b)
		}

		// scout, reviewer, security-reviewer carry full YAML frontmatter in their .md files.
		for _, file := range []string{"scout.md", "reviewer.md", "security-reviewer.md"} {
			def, err := ParseAgent("embedded:"+file, readEmbedded(file), SourceBundled)
			if err != nil {
				panic("invalid embedded agent " + file + ": " + err.Error())
			}
			bundledAgents = append(bundledAgents, def)
		}

		// task and sonic wrap prompts/agents/task.md with frontmatter defined in agents.ts:49-72.
		taskBody := strings.TrimSpace(readEmbedded("task.md"))
		bundledAgents = append(bundledAgents,
			AgentDefinition{
				Name:          "task",
				Description:   "General-purpose subagent with full capabilities for delegated multi-step tasks",
				SystemPrompt:  taskBody,
				Spawns:        &SpawnsSpec{All: true},
				Model:         []string{"@task"},
				ThinkingLevel: "auto",
				Source:        SourceBundled,
				FilePath:      "embedded:task.md",
			},
			AgentDefinition{
				Name:          "sonic",
				Description:   "Low-reasoning agent for strictly mechanical updates or data collection only",
				SystemPrompt:  taskBody,
				Model:         []string{"@smol"},
				ThinkingLevel: "medium",
				Source:        SourceBundled,
				FilePath:      "embedded:sonic.md",
			},
		)
	})
	out := make([]AgentDefinition, len(bundledAgents))
	copy(out, bundledAgents)
	return out
}

// GetBundledAgent returns a bundled agent by exact name.
func GetBundledAgent(name string) (AgentDefinition, bool) {
	for _, a := range LoadBundledAgents() {
		if a.Name == name {
			return a, true
		}
	}
	return AgentDefinition{}, false
}

// DiscoverOptions overrides paths used by DiscoverAgents (primarily for tests).
type DiscoverOptions struct {
	Cwd           string
	Home          string
	AgentDir      string
	ConfigDirName string
}

// DiscoverAgents loads project, user, and bundled agents in omp precedence order
// (project > user > bundled).
//
// Config directory mapping to PiG:
//   - Project agents: nearest ancestor `<configDirName>/agents` (from sdk.ConfigDirName(),
//     typically `.pig/agents`), falling back to nearest `.omp/agents` if distinct.
//   - User agents: `<agentDir>/agents` (from sdk.AgentDir(), typically `~/.pig/agent/agents`),
//     falling back to `~/.omp/agent/agents` if distinct.
//   - Direct cross-harness roots such as `.claude/agents` are intentionally skipped,
//     matching omp's discovery.ts contract.
//
// upstream: packages/coding-agent/src/task/discovery.ts
func DiscoverAgents(opts DiscoverOptions) DiscoveryResult {
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	home := opts.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	configDirName := opts.ConfigDirName
	if configDirName == "" {
		if name, err := sdk.ConfigDirName(); err == nil && name != "" {
			configDirName = name
		} else {
			configDirName = ".pig"
		}
	}
	agentDir := opts.AgentDir
	if agentDir == "" {
		if dir, err := sdk.AgentDir(); err == nil && dir != "" {
			agentDir = dir
		} else if home != "" {
			agentDir = filepath.Join(home, configDirName, "agent")
		}
	}

	type agentDirEntry struct {
		dir    string
		source AgentSource
	}
	var ordered []agentDirEntry
	seenDirs := map[string]bool{}
	addDir := func(dir string, source AgentSource) {
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		if seenDirs[clean] {
			return
		}
		seenDirs[clean] = true
		ordered = append(ordered, agentDirEntry{dir: clean, source: source})
	}

	// Project agents: nearest ancestor <configDirName>/agents, then nearest .omp/agents.
	var projectAgentsDir string
	if cwd != "" {
		if d := findNearestDir(cwd, filepath.Join(configDirName, "agents"), home); d != "" {
			projectAgentsDir = d
			addDir(d, SourceProject)
		}
		if configDirName != ".omp" {
			if d := findNearestDir(cwd, filepath.Join(".omp", "agents"), home); d != "" {
				if projectAgentsDir == "" {
					projectAgentsDir = d
				}
				addDir(d, SourceProject)
			}
		}
	}

	// User agents: <agentDir>/agents, then ~/.omp/agent/agents if distinct.
	if agentDir != "" {
		addDir(filepath.Join(agentDir, "agents"), SourceUser)
	}
	if home != "" {
		addDir(filepath.Join(home, ".omp", "agent", "agents"), SourceUser)
	}

	seenNames := map[string]bool{}
	var result []AgentDefinition
	var searched []string
	for _, entry := range ordered {
		searched = append(searched, entry.dir)
		loaded := loadAgentsFromDir(entry.dir, entry.source)
		for _, ag := range loaded {
			if seenNames[ag.Name] {
				continue
			}
			seenNames[ag.Name] = true
			result = append(result, ag)
		}
	}

	for _, ag := range LoadBundledAgents() {
		if seenNames[ag.Name] {
			continue
		}
		seenNames[ag.Name] = true
		result = append(result, ag)
	}

	return DiscoveryResult{
		Agents:           result,
		ProjectAgentsDir: projectAgentsDir,
		SearchedDirs:     searched,
	}
}

func findNearestDir(startDir, relSubdir, home string) string {
	cur := filepath.Clean(startDir)
	absHome := ""
	if home != "" {
		absHome = filepath.Clean(home)
	}
	for {
		candidate := filepath.Join(cur, relSubdir)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		if absHome != "" && cur == absHome {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return ""
}

func loadAgentsFromDir(dir string, source AgentSource) []AgentDefinition {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []AgentDefinition
	for _, name := range names {
		full := filepath.Join(dir, name)
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		ag, err := ParseAgent(full, string(data), source)
		if err != nil {
			continue
		}
		out = append(out, ag)
	}
	return out
}

// GetAgent looks up an agent by name.
func GetAgent(list []AgentDefinition, name string) (AgentDefinition, bool) {
	for _, a := range list {
		if a.Name == name {
			return a, true
		}
	}
	return AgentDefinition{}, false
}

// parseFrontmatter parses leading YAML frontmatter delimited by `---`.
// Handles scalars, quoted strings, flow arrays (`[a, b]`), block sequences (`- item`),
// and nested maps (such as JTD/JSON `output:` schemas) using stdlib only.
func parseFrontmatter(content string) (map[string]any, string, error) {
	s := strings.TrimPrefix(content, "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") && s != "---" {
		return map[string]any{}, s, nil
	}
	rest := s[4:]
	lines := strings.Split(rest, "\n")
	endIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		return nil, "", errors.New("unterminated YAML frontmatter")
	}
	fmLines := lines[:endIdx]
	body := strings.Join(lines[endIdx+1:], "\n")
	val, _, err := parseYAMLMapping(fmLines, 0, 0)
	if err != nil {
		return nil, "", err
	}
	return val, body, nil
}

func lineIndent(line string) int {
	n := 0
	for n < len(line) && line[n] == ' ' {
		n++
	}
	return n
}

func parseYAMLMapping(lines []string, start, minIndent int) (map[string]any, int, error) {
	out := map[string]any{}
	i := start
	for i < len(lines) {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}
		indent := lineIndent(raw)
		if indent < minIndent {
			break
		}
		if indent > minIndent && len(out) > 0 {
			break
		}
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			break
		}
		colon := findMappingColon(trimmed)
		if colon < 0 {
			i++
			continue
		}
		key := unquoteYAMLScalar(strings.TrimSpace(trimmed[:colon]))
		valPart := strings.TrimSpace(trimmed[colon+1:])
		i++
		if valPart != "" && valPart != "|" && valPart != ">" {
			out[key] = parseYAMLScalarOrFlow(valPart)
			continue
		}
		if valPart == "|" || valPart == ">" {
			var blockLines []string
			blockIndent := -1
			for i < len(lines) {
				braw := lines[i]
				if strings.TrimSpace(braw) == "" {
					blockLines = append(blockLines, "")
					i++
					continue
				}
				bi := lineIndent(braw)
				if bi <= indent {
					break
				}
				if blockIndent < 0 {
					blockIndent = bi
				}
				if bi >= blockIndent {
					blockLines = append(blockLines, braw[blockIndent:])
				} else {
					blockLines = append(blockLines, strings.TrimSpace(braw))
				}
				i++
			}
			if valPart == ">" {
				out[key] = strings.Join(blockLines, " ")
			} else {
				out[key] = strings.Join(blockLines, "\n")
			}
			continue
		}
		// Nested block value: peek next non-empty line.
		next := nextNonEmptyLine(lines, i)
		if next < 0 {
			out[key] = nil
			continue
		}
		nextIndent := lineIndent(lines[next])
		if nextIndent <= indent {
			out[key] = nil
			continue
		}
		nextTrim := strings.TrimSpace(lines[next])
		if strings.HasPrefix(nextTrim, "- ") || nextTrim == "-" {
			seq, ni, err := parseYAMLSequence(lines, next, nextIndent)
			if err != nil {
				return nil, ni, err
			}
			out[key] = seq
			i = ni
		} else {
			sub, ni, err := parseYAMLMapping(lines, next, nextIndent)
			if err != nil {
				return nil, ni, err
			}
			out[key] = sub
			i = ni
		}
	}
	return out, i, nil
}

func parseYAMLSequence(lines []string, start, seqIndent int) ([]any, int, error) {
	var out []any
	i := start
	for i < len(lines) {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}
		indent := lineIndent(raw)
		if indent < seqIndent {
			break
		}
		if indent != seqIndent || !(strings.HasPrefix(trimmed, "- ") || trimmed == "-") {
			break
		}
		itemText := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		i++
		if itemText == "" {
			next := nextNonEmptyLine(lines, i)
			if next >= 0 && lineIndent(lines[next]) > seqIndent {
				sub, ni, err := parseYAMLMapping(lines, next, lineIndent(lines[next]))
				if err != nil {
					return nil, ni, err
				}
				out = append(out, sub)
				i = ni
			} else {
				out = append(out, nil)
			}
			continue
		}
		out = append(out, parseYAMLScalarOrFlow(itemText))
	}
	return out, i, nil
}

func nextNonEmptyLine(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t != "" && !strings.HasPrefix(t, "#") {
			return i
		}
	}
	return -1
}

func findMappingColon(s string) int {
	inSingle := false
	inDouble := false
	for i := range len(s) {
		c := s[i]
		if c == '\'' && !inDouble {
			inSingle = !inSingle
		} else if c == '"' && !inSingle {
			inDouble = !inDouble
		} else if c == ':' && !inSingle && !inDouble {
			if i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t' {
				return i
			}
		}
	}
	return -1
}

func unquoteYAMLScalar(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var u string
		if err := json.Unmarshal([]byte(s), &u); err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

func parseYAMLScalarOrFlow(s string) any {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || trimmed == "null" || trimmed == "~" {
		return nil
	}
	// Strip trailing inline comment outside quotes/brackets.
	if len(trimmed) > 0 && trimmed[0] != '"' && trimmed[0] != '\'' && trimmed[0] != '[' && trimmed[0] != '{' {
		if idx := strings.Index(trimmed, " #"); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		inner := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		if inner == "" {
			return []any{}
		}
		var out []any
		for _, part := range splitFlowList(inner) {
			out = append(out, parseYAMLScalarOrFlow(part))
		}
		return out
	}
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			return obj
		}
	}
	if (strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"")) ||
		(strings.HasPrefix(trimmed, "'") && strings.HasSuffix(trimmed, "'")) {
		return unquoteYAMLScalar(trimmed)
	}
	switch strings.ToLower(trimmed) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return int(n)
	}
	if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return f
	}
	return trimmed
}

func splitFlowList(s string) []string {
	var parts []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	depth := 0
	for i := range len(s) {
		c := s[i]
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			cur.WriteByte(c)
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			cur.WriteByte(c)
		case '[', '{':
			if !inSingle && !inDouble {
				depth++
			}
			cur.WriteByte(c)
		case ']', '}':
			if !inSingle && !inDouble && depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case ',':
			if !inSingle && !inDouble && depth == 0 {
				parts = append(parts, strings.TrimSpace(cur.String()))
				cur.Reset()
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	if tail := strings.TrimSpace(cur.String()); tail != "" {
		parts = append(parts, tail)
	}
	return parts
}
