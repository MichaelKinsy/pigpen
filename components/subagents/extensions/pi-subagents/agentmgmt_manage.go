package pi_subagents

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Agent management (src/agents/agent-management.ts): the list, get, models, create, update, delete, eject, disable, enable and
// reset actions of the `subagent` tool.

// mgmtContext is agent-management.ts ManagementContext.
type mgmtContext struct {
	cwd string
	// available lists the models of the session's registry (modelRegistry.getAvailable()).
	available func() []ModelInfo
	// model is the session's current model (ctx.model), nil when unavailable.
	model     *ParentModel
	sessionID string
	// proactive is config.proactiveSkillSubagents: nil, the bool false, or *ProactiveSkillSubagentsConfig.
	proactive       any
	onAgentsChanged func()
	// discoverAll replaces discoverAgentsAll (the original's ctx.discoverAgentsAll).
	discoverAll func(cwd, provider string) (DiscoveryAll, error)
	// runtimeAgents merges the extension-registered agents into the effective list (mergeRuntimeAgents); nil: none.
	runtimeAgents func(agents []AgentConfig, all []AgentConfig, cwd string, scope AgentScope, provider string) []AgentConfig
}

// manageResult is the outcome of a management action: the text the model sees, whether it is an error, and the details.
type manageResult struct {
	text    string
	isError bool
	details *jsObject
}

func mgmtResultOf(text string, isError bool) manageResult {
	return manageResult{text: text, isError: isError}
}

func (c mgmtContext) provider() string {
	if c.model != nil {
		return c.model.Provider
	}
	return ""
}

func (c mgmtContext) catalog(cwd, provider string) (DiscoveryAll, error) {
	if c.discoverAll != nil {
		return c.discoverAll(cwd, provider)
	}
	return discoverAgentsAll(cwd, provider, DiscoveryOptions{})
}

func (c mgmtContext) changed() {
	if c.onAgentsChanged != nil {
		c.onAgentsChanged()
	}
}

func parseCSV(v string) []string {
	var parts []string
	for _, p := range strings.Split(v, ",") {
		parts = append(parts, p)
	}
	return dedupeTrimmed(parts)
}

type configResult struct {
	status string // "ok", "missing" or "error"
	value  *jsObject
	msg    string
}

// configObject is agent-management.ts:82: the `config` parameter, parsed from a JSON string when it is one.
func configObject(config any) configResult {
	v := config
	if s, ok := v.(string); ok {
		parsed, msg := jsJSONParse(s)
		if msg != "" {
			return configResult{status: "error", msg: "config must be valid JSON: " + msg}
		}
		v = parsed
	}
	switch x := v.(type) {
	case *jsObject:
		return configResult{status: "ok", value: x}
	case map[string]any:
		return configResult{status: "ok", value: deepJS(x).(*jsObject)}
	}
	return configResult{status: "missing"}
}

func hasKey(o *jsObject, k string) bool { _, ok := o.vals[k]; return ok }

func asDisambiguationScope(v any) string {
	if s, ok := v.(string); ok && (s == "user" || s == "project") {
		return s
	}
	return ""
}

// actionScope is agent-management.ts:113.
func actionScope(v any, action string) (string, *manageResult) {
	if v == nil || isUndefined(v) {
		return "user", nil
	}
	if s := asDisambiguationScope(v); s != "" {
		return s, nil
	}
	r := mgmtResultOf(fmt.Sprintf("agentScope must be 'user' or 'project' for %s.", action), true)
	return "", &r
}

func allAgentsOf(d DiscoveryAll) []AgentConfig {
	var out []AgentConfig
	for _, l := range [][]AgentConfig{d.Builtin, d.Package, d.User, d.Project} {
		out = append(out, l...)
	}
	return out
}

func (c mgmtContext) effectiveAgentsForScope(scope AgentScope, d DiscoveryAll, provider string) []AgentConfig {
	agents := MergeAgentsForScope(scope, d.User, d.Project, d.Builtin, d.Package)
	if c.runtimeAgents != nil {
		agents = c.runtimeAgents(agents, allAgentsOf(d), d.Cwd, scope, provider)
	}
	return agents
}

func sortedUniqueNames(agents []AgentConfig) []string {
	seen := map[string]bool{}
	var names []string
	for _, a := range agents {
		if !seen[a.Name] {
			seen[a.Name] = true
			names = append(names, a.Name)
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return localeLess(names[i], names[j]) })
	return names
}

func (c mgmtContext) availableAgentNamesFromDiscovery(d DiscoveryAll, provider string) []string {
	agents := allAgentsOf(d)
	if c.runtimeAgents != nil {
		agents = c.effectiveAgentsForScope(ScopeBoth, d, provider)
	}
	return sortedUniqueNames(agents)
}

func (c mgmtContext) availableAgentNames(cwd string) []string {
	d, err := c.catalog(cwd, "")
	if err != nil {
		return nil
	}
	return c.availableAgentNamesFromDiscovery(d, "")
}

func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// findAgentsInDiscovery is agent-management.ts:165: the agents a name (or alias) resolves to in a scope.
func (c mgmtContext) findAgentsInDiscovery(name string, d DiscoveryAll, scope AgentScope, provider string) []AgentConfig {
	raw := jsTrim(name)
	sanitized := sanitizeName(raw)
	scoped := c.effectiveAgentsForScope(scope, d, provider)
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
	sort.SliceStable(out, func(i, j int) bool { return localeSourceLess(out[i].Source, out[j].Source) })
	return out
}

func localeSourceLess(a, b AgentSource) bool { return localeLess(string(a), string(b)) }

func (c mgmtContext) findAgents(name string, cwd string, scope AgentScope) []AgentConfig {
	d, err := c.catalog(cwd, "")
	if err != nil {
		return nil
	}
	return c.findAgentsInDiscovery(name, d, scope, "")
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

var agentSourcePrecedence = map[AgentSource]int{SourceBuiltin: 0, SourcePackage: 1, SourceUser: 2, SourceProject: 3, SourceRuntime: 4}

func agentDefinitionPriority(source AgentSource, priority int) int {
	return agentSourcePrecedence[source]*1_000_000 + priority
}

// blockingDiagnostic is agents.ts:277 findBlockingAgentDiagnostic: a malformed definition of the name that is not shadowed by a
// valid one of equal or higher precedence.
func blockingDiagnostic(name string, agents []AgentConfig, diags []Diagnostic) *Diagnostic {
	n := jsTrim(name)
	var match *Diagnostic
	for i := range diags {
		d := &diags[i]
		hit := d.RuntimeName == n
		if !hit && d.Name == n {
			hit = !d.PackageSpecified || d.RuntimeName == ""
			if !hit {
				for _, a := range agents {
					if a.Name == d.RuntimeName && a.LocalName == d.Name {
						hit = true
					}
				}
			}
		}
		if hit && (match == nil || agentDefinitionPriority(d.Source, d.DiscoveryPriority) > agentDefinitionPriority(match.Source, match.DiscoveryPriority)) {
			match = d
		}
	}
	highest := -1 << 62
	for _, a := range agents {
		if p := agentDefinitionPriority(a.Source, a.discoveryPriority); p > highest {
			highest = p
		}
	}
	if len(agents) == 0 || (match != nil && agentDefinitionPriority(match.Source, match.DiscoveryPriority) > highest) {
		return match
	}
	return nil
}

// resolveEffectiveAgent is agent-management.ts:198: the highest-precedence definition of a name, disabled ones included.
func resolveEffectiveAgent(d DiscoveryAll, name string) (*AgentConfig, string) {
	raw := jsTrim(name)
	candidates := allAgentsOf(d)
	a, msg := ResolveAgentName(raw, candidates)
	if a == nil && msg == "" {
		if s := sanitizeName(raw); s != raw {
			a, msg = ResolveAgentName(s, candidates)
		}
	}
	if msg != "" {
		return nil, msg
	}
	if a == nil {
		return nil, ""
	}
	var best *AgentConfig
	for i := range candidates {
		if candidates[i].Name == a.Name && (best == nil || agentSourcePrecedence[candidates[i].Source] > agentSourcePrecedence[best.Source]) {
			best = &candidates[i]
		}
	}
	return best, ""
}

func (c mgmtContext) nameExistsInScope(scope, name, excludePath string) bool {
	d, err := c.catalog(c.cwd, "")
	if err != nil {
		return false
	}
	list := d.User
	if scope == "project" {
		list = d.Project
	}
	for _, a := range list {
		if a.Name == name && a.FilePath != excludePath {
			return true
		}
	}
	return false
}

func isMutableSource(s AgentSource) bool { return s == SourceUser || s == SourceProject }

func (c mgmtContext) modelWarning(model string) string {
	if model == "" || c.available == nil {
		return ""
	}
	for _, m := range c.available() {
		if m.Provider+"/"+m.ID == model || m.ID == model {
			return ""
		}
	}
	return fmt.Sprintf("Warning: model '%s' is not in the current model registry. Run subagent({ action: \"models\" }) to list valid provider/id selectors, then use the exact provider/id form (bare ids resolve only when unique).", model)
}

func skillsWarning(cwd string, a AgentConfig) string {
	if len(a.Skills) == 0 {
		return ""
	}
	base := cwd
	if a.FilePath != "" {
		base = filepath.Dir(a.FilePath)
	}
	_, missing, err := resolveSkills(a.Skills, cwd, a.SkillPath, base)
	if err != nil || len(missing) == 0 {
		return ""
	}
	return "Warning: skills not found: " + strings.Join(missing, ", ") + "."
}

func readAgentFrontmatterFields(filePath string) map[string]bool {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return map[string]bool{}
	}
	fm, _ := ParseFrontmatter(string(b))
	out := map[string]bool{}
	for _, k := range fm.Keys() {
		out[k] = true
	}
	return out
}

// withDeclaredExtensionPaths is agent-management.ts:242: the extension fields as the file declares them, unresolved.
func withDeclaredExtensionPaths(c AgentConfig, filePath string) (AgentConfig, error) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return c, err
	}
	fm, _ := ParseFrontmatter(string(b))
	c.Extensions, c.SubagentOnlyExtensions = nil, nil
	if fm.Has("extensions") {
		c.Extensions = ParseFrontmatterList(optional(fm, "extensions"))
	}
	if fm.Has("subagentOnlyExtensions") {
		c.SubagentOnlyExtensions = ParseFrontmatterList(optional(fm, "subagentOnlyExtensions"))
	}
	return c, nil
}

// editableAgentConfig is agent-management.ts:254: the agent as its file (or the base before an override) defines it.
func editableAgentConfig(a AgentConfig) (AgentConfig, error) {
	base := (*OverrideBase)(nil)
	if a.Override != nil {
		b := a.Override.Base
		base = &b
	}
	description := a.Description
	if base != nil {
		description = base.Description
	}
	var fields map[string]bool
	if a.Source != SourceBuiltin && a.Source != SourceRuntime {
		fields = readAgentFrontmatterFields(a.FilePath)
	}
	declared := func(names ...string) bool {
		if fields == nil {
			return true
		}
		for _, n := range names {
			if fields[n] {
				return true
			}
		}
		return false
	}
	if base == nil {
		next := a
		next.Extensions = nil
		if !a.ExtensionsFromDefault && a.Extensions != nil {
			next.Extensions = cloneStrings(a.Extensions)
		}
		if fields != nil {
			if !declared("model") {
				next.Model = ""
			}
			if !declared("thinking") {
				next.Thinking = nil
			}
		}
		return withDeclaredExtensionPaths(next, a.FilePath)
	}
	e := a
	e.Override = nil
	e.Description = description
	e.Output, e.OutputMode, e.DefaultReads = base.Output, base.OutputMode, cloneStrings(base.DefaultReads)
	e.Model = ""
	if base.Model != "" && declared("model") {
		e.Model = base.Model
	}
	e.Fast = cloneBoolPtr(base.Fast)
	e.Thinking = nil
	if base.Thinking != nil && declared("thinking") {
		e.Thinking = base.Thinking
	}
	e.SystemPromptMode, e.InheritProjectContext, e.InheritGlobalContext, e.InheritSkills = base.SystemPromptMode, base.InheritProjectContext, base.InheritGlobalContext, base.InheritSkills
	e.DefaultContext, e.AcceptanceRole, e.Disabled, e.SystemPrompt = base.DefaultContext, base.AcceptanceRole, cloneBoolPtr(base.Disabled), base.SystemPrompt
	e.Skills, e.SkillPath, e.Tools, e.ExcludeTools = cloneStrings(base.Skills), cloneStrings(base.SkillPath), cloneStrings(base.Tools), cloneStrings(base.ExcludeTools)
	e.McpDirectTools, e.AllowNestedSubagents, e.AllowedAgents = cloneStrings(base.McpDirectTools), cloneBoolPtr(base.AllowNestedSubagents), cloneStrings(base.AllowedAgents)
	e.Extensions, e.SubagentOnlyExtensions, e.MutationTools = cloneStrings(base.Extensions), cloneStrings(base.SubagentOnlyExtensions), cloneStrings(base.MutationTools)
	e.ToolBudget = base.ToolBudget
	return withDeclaredExtensionPaths(e, a.FilePath)
}

// preservedAgentFrontmatterFields is agent-management.ts:343: the frontmatter keys of the file an update keeps as written.
func preservedAgentFrontmatterFields(a AgentConfig, cfg *jsObject) map[string]bool {
	fields := readAgentFrontmatterFields(a.FilePath)
	changed := func(names ...string) {
		for _, n := range names {
			delete(fields, n)
		}
	}
	for _, c := range []struct {
		key     string
		cleared []string
		re      bool // the key is written again even when set
	}{
		{"name", []string{"name"}, false}, {"package", []string{"package"}, false}, {"description", []string{"description"}, false},
		{"advertise", []string{"advertise"}, false}, {"aliases", []string{"alias", "aliases"}, false}, {"systemPrompt", []string{"systemPrompt"}, false},
		{"runner", []string{"runner"}, false}, {"model", []string{"model"}, false}, {"tools", []string{"tools"}, false},
		{"excludeTools", []string{"excludeTools"}, false}, {"skills", []string{"skill", "skills"}, false}, {"skillPath", []string{"skillPath"}, false},
		{"extensions", []string{"extensions"}, false}, {"subagentOnlyExtensions", []string{"subagentOnlyExtensions"}, false},
		{"mutationTools", []string{"mutationTools"}, false},
	} {
		if hasKey(cfg, c.key) {
			changed(c.cleared...)
		}
	}
	if hasKey(cfg, "thinking") {
		changed("thinking")
		if s, _ := cfg.vals["thinking"].(string); s == "off" {
			fields["thinking"] = true
		}
	}
	for _, k := range []string{"systemPromptMode", "inheritProjectContext", "inheritGlobalContext", "inheritSkills"} {
		if hasKey(cfg, k) {
			changed(k)
			fields[k] = true
		}
	}
	for _, p := range [][2]string{{"defaultContext", "defaultContext"}, {"async", "async"}, {"timeoutMs", "timeoutMs"}, {"acceptance", "acceptance"}, {"acceptanceRole", "acceptanceRole"},
		{"output", "output"}, {"outputMode", "outputMode"}, {"reads", "defaultReads"}, {"progress", "defaultProgress"}, {"maxSubagentDepth", "maxSubagentDepth"}, {"toolBudget", "toolBudget"}} {
		if hasKey(cfg, p[0]) {
			changed(p[1])
		}
	}
	return fields
}

func parseTools(raw string) (tools, mcp []string) {
	for _, item := range parseCSV(raw) {
		if strings.HasPrefix(item, "mcp:") {
			if d := jsTrim(item[4:]); d != "" {
				mcp = append(mcp, d)
			}
		} else {
			tools = append(tools, item)
		}
	}
	return
}

func isFalseOrEmpty(v any) bool {
	if b, ok := v.(bool); ok && !b {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

func isFalse(v any) bool { b, ok := v.(bool); return ok && !b }

func stringList(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// applyAgentConfig is agent-management.ts:414: it applies a `config` object to an agent, returning the message of the first
// invalid field.
func applyAgentConfig(t *AgentConfig, cfg *jsObject) string {
	v := func(k string) any { return cfg.vals[k] }
	if hasKey(cfg, "advertise") {
		switch x := v("advertise").(type) {
		case string:
			if x == "" {
				t.Advertise = nil
			} else {
				return "config.advertise must be a boolean or empty string when provided."
			}
		case bool:
			t.Advertise = new(x)
		default:
			return "config.advertise must be a boolean or empty string when provided."
		}
	}
	if hasKey(cfg, "aliases") {
		x := v("aliases")
		clean := func(in []string) []string {
			var out []string
			for _, a := range in {
				if a != t.Name {
					out = append(out, a)
				}
			}
			return out
		}
		if isFalseOrEmpty(x) {
			t.Aliases = nil
		} else if s, ok := x.(string); ok {
			t.Aliases = clean(parseCSV(s))
		} else if list, ok := stringList(x); ok {
			t.Aliases = clean(dedupeTrimmed(list))
		} else {
			return "config.aliases must be a comma-separated string, string array, or false when provided."
		}
		if len(t.Aliases) == 0 {
			t.Aliases = nil
		}
	}
	if hasKey(cfg, "systemPrompt") {
		x := v("systemPrompt")
		if isFalseOrEmpty(x) {
			t.SystemPrompt = ""
		} else if s, ok := x.(string); ok {
			t.SystemPrompt = s
		} else {
			return "config.systemPrompt must be a string or false when provided."
		}
	}
	if hasKey(cfg, "runner") {
		x := v("runner")
		if isFalseOrEmpty(x) {
			t.Runner = nil
		} else if r, ok := x.(*jsObject); ok {
			runner, errMsg := applyRunnerConfig(r)
			if errMsg != "" {
				return errMsg
			}
			t.Runner = runner
		} else {
			return "config.runner must be an object, false, or empty string when provided."
		}
	}
	if hasKey(cfg, "model") {
		x := v("model")
		if isFalseOrEmpty(x) {
			t.Model = ""
		} else if s, ok := x.(string); ok {
			t.Model = jsTrim(s)
		} else {
			return "config.model must be a string or false when provided."
		}
	}
	if hasKey(cfg, "fallbackModels") {
		return "config.fallbackModels was removed; configure one model instead."
	}
	if hasKey(cfg, "tools") {
		x := v("tools")
		if isFalseOrEmpty(x) {
			t.Tools, t.McpDirectTools = nil, nil
		} else if s, ok := x.(string); ok {
			t.Tools, t.McpDirectTools = parseTools(s)
		} else {
			return "config.tools must be a comma-separated string or false when provided."
		}
	}
	if hasKey(cfg, "excludeTools") {
		x := v("excludeTools")
		if isFalseOrEmpty(x) {
			t.ExcludeTools = nil
		} else if s, ok := x.(string); ok {
			t.ExcludeTools = dedupeTrimmed(parseCSV(s))
		} else if list, ok := stringList(x); ok {
			t.ExcludeTools = dedupeTrimmed(list)
		} else {
			return "config.excludeTools must be a comma-separated string, string array, or false when provided."
		}
		if len(t.ExcludeTools) == 0 {
			t.ExcludeTools = nil
		}
	}
	if hasKey(cfg, "skills") {
		x := v("skills")
		if isFalseOrEmpty(x) {
			t.Skills = nil
		} else if s, ok := x.(string); ok {
			t.Skills = parseCSV(s)
		} else {
			return "config.skills must be a comma-separated string or false when provided."
		}
		if len(t.Skills) == 0 {
			t.Skills = nil
		}
	}
	if hasKey(cfg, "skillPath") {
		x := v("skillPath")
		if isFalseOrEmpty(x) {
			t.SkillPath = nil
		} else if s, ok := x.(string); ok {
			t.SkillPath = parseCSV(s)
		} else if list, ok := stringList(x); ok {
			t.SkillPath = dedupeTrimmed(list)
		} else {
			return "config.skillPath must be a comma-separated string, string array, or false when provided."
		}
		if len(t.SkillPath) == 0 {
			t.SkillPath = nil
		}
	}
	for _, f := range []struct {
		key string
		dst *[]string
	}{{"extensions", &t.Extensions}, {"subagentOnlyExtensions", &t.SubagentOnlyExtensions}, {"mutationTools", &t.MutationTools}} {
		if !hasKey(cfg, f.key) {
			continue
		}
		x := v(f.key)
		if isFalse(x) {
			*f.dst = nil
		} else if s, ok := x.(string); ok && s == "" {
			*f.dst = []string{}
		} else if s, ok := x.(string); ok {
			*f.dst = parseCSV(s)
		} else {
			return fmt.Sprintf("config.%s must be a comma-separated string, empty string, or false when provided.", f.key)
		}
	}
	if hasKey(cfg, "thinking") {
		x := v("thinking")
		if isFalseOrEmpty(x) {
			t.Thinking = nil
		} else if s, ok := x.(string); ok {
			if th := jsTrim(s); th != "" {
				t.Thinking = th
			} else {
				t.Thinking = nil
			}
		} else {
			return "config.thinking must be a string or false when provided."
		}
	}
	if hasKey(cfg, "systemPromptMode") {
		if s, _ := v("systemPromptMode").(string); s == "append" || s == "replace" {
			t.SystemPromptMode = s
		} else {
			return "config.systemPromptMode must be 'append' or 'replace' when provided."
		}
	}
	for _, f := range []struct {
		key string
		dst *bool
	}{{"inheritProjectContext", &t.InheritProjectContext}, {"inheritGlobalContext", &t.InheritGlobalContext}, {"inheritSkills", &t.InheritSkills}} {
		if hasKey(cfg, f.key) {
			b, ok := v(f.key).(bool)
			if !ok {
				return fmt.Sprintf("config.%s must be a boolean when provided.", f.key)
			}
			*f.dst = b
		}
	}
	if hasKey(cfg, "defaultContext") {
		x := v("defaultContext")
		if isFalseOrEmpty(x) {
			t.DefaultContext = ""
		} else if s, _ := x.(string); s == "fresh" || s == "fork" {
			t.DefaultContext = s
		} else {
			return "config.defaultContext must be 'fresh', 'fork', or false when provided."
		}
	}
	if hasKey(cfg, "async") {
		switch x := v("async").(type) {
		case string:
			if x != "" {
				return "config.async must be a boolean or empty string when provided."
			}
			t.DefaultAsync = nil
		case bool:
			t.DefaultAsync = new(x)
		default:
			return "config.async must be a boolean or empty string when provided."
		}
	}
	if hasKey(cfg, "timeoutMs") {
		x := v("timeoutMs")
		if isFalseOrEmpty(x) {
			t.DefaultTimeoutMs = nil
		} else if n, ok := x.(float64); ok && isIntegerNum(n) && n > 0 {
			t.DefaultTimeoutMs = new(int(n))
		} else {
			return "config.timeoutMs must be a positive integer or false when provided."
		}
	}
	if hasKey(cfg, "acceptance") {
		x := v("acceptance")
		if s, ok := x.(string); ok && s == "" {
			t.DefaultAcceptance = nil
		} else {
			if errs := validateAcceptanceInput(x, "config.acceptance"); len(errs) > 0 {
				return strings.Join(errs, " ")
			}
			t.DefaultAcceptance = x
		}
	}
	if hasKey(cfg, "acceptanceRole") {
		x := v("acceptanceRole")
		if isFalseOrEmpty(x) {
			t.AcceptanceRole = ""
		} else if s, _ := x.(string); s == "read-only" || s == "writer" {
			t.AcceptanceRole = s
		} else {
			return "config.acceptanceRole must be 'read-only', 'writer', or false when provided."
		}
	}
	if hasKey(cfg, "output") {
		x := v("output")
		if isFalseOrEmpty(x) {
			t.Output = ""
		} else if s, ok := x.(string); ok {
			t.Output = s
		} else {
			return "config.output must be a string or false when provided."
		}
	}
	if hasKey(cfg, "outputMode") {
		if s, _ := v("outputMode").(string); s == "inline" || s == "file-only" {
			t.OutputMode = s
		} else {
			return "config.outputMode must be 'inline' or 'file-only' when provided."
		}
	}
	if hasKey(cfg, "reads") {
		x := v("reads")
		if isFalseOrEmpty(x) {
			t.DefaultReads = nil
		} else if s, ok := x.(string); ok {
			t.DefaultReads = parseCSV(s)
			if len(t.DefaultReads) == 0 {
				t.DefaultReads = nil
			}
		} else {
			return "config.reads must be a comma-separated string or false when provided."
		}
	}
	if hasKey(cfg, "progress") {
		b, ok := v("progress").(bool)
		if !ok {
			return "config.progress must be a boolean when provided."
		}
		t.DefaultProgress = b
	}
	if hasKey(cfg, "maxSubagentDepth") {
		x := v("maxSubagentDepth")
		if isFalseOrEmpty(x) {
			t.MaxSubagentDepth = nil
		} else if n, ok := x.(float64); ok && isIntegerNum(n) && n >= 0 {
			t.MaxSubagentDepth = new(int(n))
		} else {
			return "config.maxSubagentDepth must be an integer >= 0 or false when provided."
		}
	}
	if hasKey(cfg, "toolBudget") {
		x := v("toolBudget")
		if isFalseOrEmpty(x) {
			t.ToolBudget = nil
		} else {
			if msg := validateToolBudget(x, "config.toolBudget"); msg != "" {
				return msg
			}
			t.ToolBudget = x
		}
	}
	if t.Runner != nil && (t.Runner.Type == "external-cli" || t.Runner.Type == "external-job") {
		var unsupported []string
		add := func(cond bool, name string) {
			if cond {
				unsupported = append(unsupported, name)
			}
		}
		add(len(t.Tools) > 0 || len(t.McpDirectTools) > 0, "tools")
		add(len(t.ExcludeTools) > 0, "excludeTools")
		add(t.Model != "", "model")
		add(thinkingTruthy(t.Thinking), "thinking")
		add(len(t.Extensions) > 0, "extensions")
		add(len(t.SubagentOnlyExtensions) > 0, "subagentOnlyExtensions")
		add(len(t.MutationTools) > 0, "mutationTools")
		add(len(t.Skills) > 0 || len(t.SkillPath) > 0, "skills")
		add(t.MaxSubagentDepth != nil, "maxSubagentDepth")
		add(t.ToolBudget != nil, "toolBudget")
		if len(unsupported) > 0 {
			return fmt.Sprintf("config.runner type '%s' does not support Pi-only fields: %s.", t.Runner.Type, strings.Join(unsupported, ", "))
		}
	}
	return ""
}

func thinkingTruthy(v any) bool {
	switch x := v.(type) {
	case string:
		return x != ""
	case bool:
		return x
	}
	return false
}

// applyRunnerConfig is the `runner` branch of applyAgentConfig (agent-management.ts:437-456).
func applyRunnerConfig(r *jsObject) (*Runner, string) {
	typ, _ := r.vals["type"].(string)
	only := func(allowed ...string) bool {
		for _, k := range r.order() {
			ok := false
			for _, a := range allowed {
				ok = ok || a == k
			}
			if !ok {
				return false
			}
		}
		return true
	}
	if typ == "pi" && only("type") {
		return &Runner{Type: "pi"}, ""
	}
	if typ == "external-cli" {
		cmd, isS := r.vals["command"].(string)
		args, hasArgs := r.vals["args"]
		argList, argsOK := stringList(args)
		adapterV, hasAdapter := r.vals["adapter"]
		pd, hasPD := r.vals["promptDelivery"]
		ok := isS && jsTrim(cmd) != "" &&
			(!hasArgs || argsOK) &&
			(!hasAdapter || isCodeOwnedExternalCliAdapterID(adapterV)) &&
			(!hasAdapter || !hasArgs || len(argList) == 0) &&
			(!hasPD || pd == "stdin") &&
			only("type", "adapter", "command", "args", "promptDelivery")
		if ok {
			out := &Runner{Type: "external-cli", Command: jsTrim(cmd), Args: argList}
			if hasAdapter {
				out.Adapter, _ = adapterV.(string)
			}
			if hasPD && pd != nil && pd != "" && pd != false {
				out.PromptDelivery = "stdin"
			}
			return out, ""
		}
	}
	if typ == "external-job" {
		prov, isS := r.vals["provider"].(string)
		optsV, hasOpts := r.vals["options"]
		var opts *jsObject
		optsOK := !hasOpts
		if hasOpts {
			if o, isO := optsV.(*jsObject); isO && isJSONSerializable(o) {
				opts, optsOK = o, true
			}
		}
		if isS && jsTrim(prov) == prov && prov != "" && optsOK && only("type", "provider", "options") {
			return &Runner{Type: "external-job", Provider: prov, Options: opts}, ""
		}
	}
	return nil, fmt.Sprintf("config.runner must be { type: 'pi' }, { type: 'external-cli', adapter?: %s, command: string, args?: string[], promptDelivery?: 'stdin' }, or { type: 'external-job', provider: string, options?: object }.", codeOwnedExternalCliAdapterLabel())
}

// resolveMgmtTarget is agent-management.ts:631: the single user or project agent an action edits.
func (c mgmtContext) resolveTarget(name string, matches []AgentConfig, cwd, scopeHint string) (*AgentConfig, *manageResult) {
	seen := map[string]bool{}
	var distinct []string
	for _, m := range matches {
		if !seen[m.Name] {
			seen[m.Name] = true
			distinct = append(distinct, m.Name)
		}
	}
	if len(distinct) > 1 {
		sort.SliceStable(distinct, func(i, j int) bool { return localeLess(distinct[i], distinct[j]) })
		r := mgmtResultOf(fmt.Sprintf("Ambiguous agent alias or name '%s': %s", name, strings.Join(distinct, ", ")), true)
		return nil, &r
	}
	var mutable []AgentConfig
	for _, m := range matches {
		if isMutableSource(m.Source) {
			mutable = append(mutable, m)
		}
	}
	if len(mutable) == 0 {
		if len(matches) > 0 {
			r := mgmtResultOf(fmt.Sprintf("Agent '%s' is read-only and cannot be modified. Create a same-named agent in user or project scope to override it.", name), true)
			return nil, &r
		}
		r := mgmtResultOf(fmt.Sprintf("Agent '%s' not found. Available: %s.", name, joinOrNone(c.availableAgentNames(cwd))), true)
		return nil, &r
	}
	if len(mutable) == 1 {
		return &mutable[0], nil
	}
	scope := asDisambiguationScope(scopeHint)
	if scope == "" {
		var paths []string
		for _, m := range mutable {
			paths = append(paths, fmt.Sprintf("%s: %s", m.Source, m.FilePath))
		}
		r := mgmtResultOf(fmt.Sprintf("Agent '%s' exists in both scopes. Specify agentScope: 'user' or 'project'.\n%s", name, strings.Join(paths, "\n")), true)
		return nil, &r
	}
	var scoped []AgentConfig
	for _, m := range mutable {
		if string(m.Source) == scope {
			scoped = append(scoped, m)
		}
	}
	if len(scoped) == 0 {
		r := mgmtResultOf(fmt.Sprintf("Agent '%s' not found in scope '%s'.", name, scope), true)
		return nil, &r
	}
	if len(scoped) > 1 {
		var paths []string
		for _, m := range scoped {
			paths = append(paths, m.FilePath)
		}
		r := mgmtResultOf(fmt.Sprintf("Multiple agents named '%s' found in scope '%s': %s", name, scope, strings.Join(paths, ", ")), true)
		return nil, &r
	}
	return &scoped[0], nil
}

func (c mgmtContext) renamePath(currentPath, newName, scope string) (string, string) {
	if c.nameExistsInScope(scope, newName, currentPath) {
		return "", fmt.Sprintf("Name '%s' already exists in %s scope.", newName, scope)
	}
	filePath := filepath.Join(filepath.Dir(currentPath), newName+".md")
	if _, err := os.Stat(filePath); err == nil && filePath != currentPath {
		return "", fmt.Sprintf("File already exists at %s but is not a valid agent definition. Remove or rename it first.", filePath)
	}
	if err := os.Rename(currentPath, filePath); err != nil {
		return "", err.Error()
	}
	return filePath, ""
}

// ---- external job providers and external CLI availability ----

// externalJobProviders is this process's registry of external-job providers (api/external-job-provider.ts). The Go extension has no
// cross-extension registry: nothing registers here, so every external-job agent reads as `missing`.
var externalJobProviders = map[string]bool{}

func listExternalJobProviders() (map[string]bool, error) { return externalJobProviders, nil }

type externalCliAvailability struct {
	available bool
	reason    string
}

const maxAvailabilityReasonLength = 256

// resolveExternalCliBinaryAvailability is external-cli-preflight.ts:64 (resolveBinary): it never starts a child process.
func resolveExternalCliBinaryAvailability(command string) externalCliAvailability {
	if filepath.IsAbs(command) || strings.ContainsRune(command, filepath.Separator) {
		abs, _ := filepath.Abs(command)
		if _, err := exec.LookPath(abs); err != nil {
			return externalCliAvailability{false, truncateUnits("EACCES: permission denied, access '"+abs+"'", maxAvailabilityReasonLength)}
		}
		return externalCliAvailability{available: true}
	}
	if _, err := exec.LookPath(command); err != nil {
		return externalCliAvailability{false, truncateUnits(fmt.Sprintf("External CLI binary '%s' was not found on PATH.", command), maxAvailabilityReasonLength)}
	}
	return externalCliAvailability{available: true}
}

func truncateUnits(s string, n int) string {
	if utf16Length(s) <= n {
		return s
	}
	return truncateDisplayText(s, n)
}

func externalCliAvailabilityKey(command, machine string) string {
	if machine != "" {
		return "ssh@" + machine
	}
	return command
}

func externalCliAvailabilityForAgents(agents []AgentConfig) map[string]externalCliAvailability {
	out := map[string]externalCliAvailability{}
	for _, a := range agents {
		if a.Runner == nil || a.Runner.Type != "external-cli" {
			continue
		}
		key := externalCliAvailabilityKey(a.Runner.Command, a.Machine)
		if _, ok := out[key]; ok {
			continue
		}
		cmd := a.Runner.Command
		if a.Machine != "" {
			cmd = "ssh"
		}
		out[key] = resolveExternalCliBinaryAvailability(cmd)
	}
	return out
}

func externalJobProviderSuffix(provider string, names map[string]bool) string {
	if names == nil {
		return "?"
	}
	if names[provider] {
		return "✓"
	}
	return "missing"
}

func packageSourceLabel(a AgentConfig) string {
	if a.PackageSourceName == "" {
		return string(a.Source)
	}
	if a.PackageSourceVersion != "" {
		return a.PackageSourceName + "@" + a.PackageSourceVersion
	}
	return a.PackageSourceName
}

func runnerListBadge(a AgentConfig, providers map[string]bool, avail map[string]externalCliAvailability) string {
	if a.Runner != nil && a.Runner.Type == "external-job" {
		return fmt.Sprintf("external-job:%s %s", a.Runner.Provider, externalJobProviderSuffix(a.Runner.Provider, providers))
	}
	if a.Runner != nil && a.Runner.Type == "external-cli" {
		placed := a.Runner.Command
		if a.Machine != "" {
			placed = a.Runner.Command + " @ " + a.Machine
		}
		av, ok := avail[externalCliAvailabilityKey(a.Runner.Command, a.Machine)]
		if !ok {
			return "external-cli:" + placed
		}
		mark := "missing"
		if av.available {
			mark = "✓"
		}
		if a.Machine != "" {
			return fmt.Sprintf("external-cli:%s saved Herdr placement; transport %s; machine not preflighted", placed, mark)
		}
		return fmt.Sprintf("external-cli:%s %s", placed, mark)
	}
	if a.Machine != "" {
		return fmt.Sprintf("machine: %s (saved Herdr placement)", a.Machine)
	}
	return ""
}

func agentListMetadata(a AgentConfig, providers map[string]bool, avail map[string]externalCliAvailability) string {
	source := string(a.Source)
	if a.Source == SourcePackage {
		source = packageSourceLabel(a)
	}
	parts := []string{source}
	if b := runnerListBadge(a, providers, avail); b != "" {
		parts = append(parts, b)
	}
	if a.DefaultContext != "" {
		parts = append(parts, "context: "+a.DefaultContext)
	}
	if len(a.Aliases) > 0 {
		parts = append(parts, "aliases: "+strings.Join(a.Aliases, ", "))
	}
	return strings.Join(parts, ", ")
}

func formatAgentListLine(a AgentConfig, providers map[string]bool, avail map[string]externalCliAvailability) string {
	return fmt.Sprintf("- %s (%s): %s", a.Name, agentListMetadata(a, providers, avail), a.Description)
}

func formatAcceptanceDisplayLabel(v string) string {
	return marshalJSON(previewDisplayText(v, 80), "")
}

func formatReviewGateLabel(review *jsObject) string {
	status := "required"
	if req, ok := review.vals["required"].(bool); ok && !req {
		status = "optional"
	}
	if ag, _ := review.vals["agent"].(string); ag != "" {
		return status + " by " + ag
	}
	return status
}

func formatAcceptanceSummary(a AgentConfig) string {
	var summary []string
	switch p := a.DefaultAcceptance.(type) {
	case nil:
	case bool:
		if !p {
			summary = append(summary, "Acceptance: disabled")
		}
	case string:
		summary = append(summary, "Acceptance: "+p)
	case *jsObject:
		var mods []string
		if ev, ok := p.vals["evidence"].([]any); ok {
			for _, e := range ev {
				mods = append(mods, jsString(e))
			}
		}
		if vf, ok := p.vals["verify"].([]any); ok {
			for _, cmd := range vf {
				if o, ok := cmd.(*jsObject); ok {
					mods = append(mods, "verify: "+formatAcceptanceDisplayLabel(jsString(o.vals["id"])))
				}
			}
		}
		if cr, ok := p.vals["criteria"].([]any); ok && len(cr) > 0 {
			mods = append(mods, fmt.Sprintf("criteria: %d", len(cr)))
		}
		if sr, ok := p.vals["stopRules"].([]any); ok && len(sr) > 0 {
			mods = append(mods, fmt.Sprintf("stopRules: %d", len(sr)))
		}
		if rv, has := p.vals["review"]; has {
			if isFalse(rv) {
				mods = append(mods, "review: off")
			} else if review, ok := rv.(*jsObject); ok {
				display := review
				if ag, _ := review.vals["agent"].(string); ag != "" {
					display = review.clone()
					display.set("agent", formatAcceptanceDisplayLabel(ag))
				}
				mods = append(mods, "review: "+formatReviewGateLabel(display))
			}
		}
		if rp, _ := p.vals["report"].(string); rp != "" {
			mods = append(mods, "report: "+rp)
		}
		level := "auto"
		if l, _ := p.vals["level"].(string); l != "" {
			level = l
		}
		s := "Acceptance: " + level
		if len(mods) > 0 {
			s += " (" + strings.Join(mods, ", ") + ")"
		}
		summary = append(summary, s)
	}
	if a.AcceptanceRole != "" {
		summary = append(summary, "Acceptance role: "+a.AcceptanceRole)
	}
	return strings.Join(summary, "; ")
}

func formatAgentCapabilitiesLine(a AgentConfig, providers map[string]bool, avail map[string]externalCliAvailability) string {
	declared := append(append([]string{}, a.Tools...), prefixAll("mcp:", a.McpDirectTools)...)
	tools := "none"
	if a.Tools == nil && a.McpDirectTools == nil {
		tools = "default/ambient"
	} else if len(declared) > 0 {
		tools = strings.Join(declared, ", ")
	}
	if len(a.ExcludeTools) > 0 {
		tools += "; excludes: " + strings.Join(a.ExcludeTools, ", ")
	}
	model := "inherits current session"
	if a.Model != "" {
		model = a.Model
		if a.ModelProvider != "" && !strings.Contains(a.Model, "/") {
			model = a.ModelProvider + "/" + a.Model
		}
	}
	thinking := "default"
	switch x := a.Thinking.(type) {
	case bool:
		if !x {
			thinking = "off"
		}
	case string:
		thinking = x
	}
	machine := ""
	if a.Machine != "" {
		machine = fmt.Sprintf("; Machine: %s (saved Herdr placement)", a.Machine)
	}
	acceptance := formatAcceptanceSummary(a)
	if acceptance != "" {
		acceptance = "; " + acceptance
	}
	return fmt.Sprintf("- %s (%s): Description: %s; Tools: %s; Model: %s; Thinking: %s%s%s", a.Name, agentListMetadata(a, providers, avail), previewDisplayText(a.Description, 240), tools, model, thinking, machine, acceptance)
}

var externalCliCapabilities = func() *jsObject {
	o := newObject()
	o.set("stop", true)
	o.set("steer", false)
	o.set("resume", false)
	o.set("structuredOutput", false)
	o.set("toolEvents", false)
	o.set("supervisor", "unsupported")
	o.set("forkContext", false)
	o.set("extensionBindings", false)
	return o
}()

func cloneObject(o *jsObject) *jsObject {
	c := newObject()
	for _, k := range o.keys {
		c.set(k, o.vals[k])
	}
	return c
}

// obj builds an ordered object from key/value pairs, leaving out the nil values (the original's undefined).
func obj(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != nil {
			o.set(kv[i].(string), kv[i+1])
		}
	}
	return o
}

func presentObj(o *jsObject) any {
	if len(o.keys) == 0 {
		return nil
	}
	return o
}

func agentCapabilityRunner(a AgentConfig, providers map[string]bool, avail map[string]externalCliAvailability) *jsObject {
	r := a.Runner
	if r == nil || r.Type == "pi" {
		return obj("type", "pi")
	}
	if r.Type == "external-cli" {
		av := avail[externalCliAvailabilityKey(r.Command, a.Machine)]
		var adapter, machine, reason any
		if r.Adapter != "" {
			adapter = r.Adapter
		}
		if a.Machine != "" {
			machine = a.Machine
		}
		if !av.available {
			reason = av.reason
		}
		return obj("type", "external-cli", "adapter", adapter, "command", r.Command, "machine", machine, "available", av.available, "unavailableReason", reason, "capabilities", cloneObject(externalCliCapabilities))
	}
	var availableV any
	if providers != nil {
		availableV = providers[r.Provider]
	}
	jobCaps := obj("stop", false, "steer", false, "resume", false, "structuredOutput", false, "toolEvents", false)
	return obj("type", "external-job", "provider", r.Provider, "available", availableV, "capabilities", jobCaps)
}

func strList(s []string) any {
	if s == nil {
		return nil
	}
	return anyStrings(s)
}

func agentCapabilityRow(a AgentConfig, executable bool, providers map[string]bool, avail map[string]externalCliAvailability, restrictionSources []string) *jsObject {
	var tools *jsObject
	{
		names := anyStrings(append([]string{}, a.Tools...))
		mcp := anyStrings(append([]string{}, a.McpDirectTools...))
		tools = obj("ambient", a.Tools == nil && a.McpDirectTools == nil, "names", names, "excludeTools", strList(a.ExcludeTools), "mcpDirectTools", mcp, "mutationTools", strList(a.MutationTools))
	}
	var restriction any
	if !executable {
		restriction = anyStrings(append([]string{}, restrictionSources...))
	}
	var thinking any
	if a.Thinking != nil {
		thinking = a.Thinking
	}
	var model any
	if a.Model != "" {
		model = a.Model
	}
	var dAsync, timeout any
	if a.DefaultAsync != nil {
		dAsync = *a.DefaultAsync
	}
	if a.DefaultTimeoutMs != nil {
		timeout = float64(*a.DefaultTimeoutMs)
	}
	var role, outPath, outMode any
	if a.AcceptanceRole != "" {
		role = a.AcceptanceRole
	}
	if a.Output != "" {
		outPath = a.Output
	}
	if a.OutputMode != "" {
		outMode = a.OutputMode
	}
	return obj("name", a.Name, "description", previewDisplayText(a.Description, 1000), "source", string(a.Source), "executable", executable,
		"restrictionSources", restriction, "aliases", strList(a.Aliases), "runner", agentCapabilityRunner(a, providers, avail), "tools", tools,
		"model", presentObj(obj("value", model, "thinking", thinking)), "execution", presentObj(obj("defaultAsync", dAsync, "timeoutMs", timeout)),
		"acceptance", presentObj(obj("policy", a.DefaultAcceptance, "role", role)), "output", presentObj(obj("path", outPath, "mode", outMode)),
		"extensions", presentObj(obj("names", strList(a.Extensions), "subagentOnly", strList(a.SubagentOnlyExtensions), "skills", strList(a.Skills))))
}

func formatAgentListSections(agents []AgentConfig, providers map[string]bool, format func(AgentConfig) string) []string {
	if len(agents) == 0 {
		return []string{"- (none)"}
	}
	var lines []string
	for _, sec := range []struct {
		src   AgentSource
		label string
	}{{SourcePackage, "Package agents"}, {SourceUser, "User agents"}, {SourceProject, "Project agents"}, {SourceRuntime, "Runtime agents"}, {SourceBuiltin, "Builtin agents"}} {
		var matches []string
		for _, a := range agents {
			if a.Source == sec.src {
				matches = append(matches, format(a))
			}
		}
		if len(matches) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, sec.label)
		lines = append(lines, matches...)
	}
	return lines
}

func formatRunnerDetail(a AgentConfig, providers map[string]bool) string {
	if a.Runner == nil {
		return ""
	}
	switch a.Runner.Type {
	case "external-job":
		return fmt.Sprintf("Runner: external-job via %s %s", a.Runner.Provider, externalJobProviderSuffix(a.Runner.Provider, providers))
	case "external-cli":
		return "Runner: external-cli " + a.Runner.Command
	}
	return "Runner: " + marshalJSON(runnerToJS(a.Runner), "")
}

// formatAgentDetail is agent-management.ts:923.
func formatAgentDetail(a AgentConfig) string {
	tools := append(append([]string{}, a.Tools...), prefixAll("mcp:", a.McpDirectTools)...)
	providers, _ := listExternalJobProviders()
	lines := []string{fmt.Sprintf("Agent: %s (%s)", a.Name, a.Source), "Path: " + a.FilePath, "Description: " + a.Description}
	if a.Source == SourcePackage && a.PackageSourceName != "" {
		lines = append(lines, "Source package: "+packageSourceLabel(a))
		if a.PackageSourceRoot != "" {
			lines = append(lines, "Package root: "+a.PackageSourceRoot)
		}
	}
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
	if len(a.ExcludeTools) > 0 {
		lines = append(lines, "Excluded tools: "+strings.Join(a.ExcludeTools, ", "))
	}
	if len(a.Skills) > 0 {
		lines = append(lines, "Skills: "+strings.Join(a.Skills, ", "))
	}
	if len(a.SkillPath) > 0 {
		lines = append(lines, "Skill paths: "+strings.Join(a.SkillPath, ", "))
	}
	lines = append(lines, "System prompt mode: "+a.SystemPromptMode)
	if d := formatRunnerDetail(a, providers); d != "" {
		lines = append(lines, d)
		if a.Runner.Type == "external-job" && a.Runner.Options != nil {
			lines = append(lines, "Runner options: "+marshalJSON(a.Runner.Options, ""))
		}
	}
	lines = append(lines, "Inherit project context: "+boolText(a.InheritProjectContext), "Inherit global context: "+boolText(a.InheritGlobalContext), "Inherit skills: "+boolText(a.InheritSkills))
	if a.DefaultContext != "" {
		lines = append(lines, "Default context: "+a.DefaultContext)
	}
	if a.DefaultAsync != nil {
		lines = append(lines, "Async: "+boolText(*a.DefaultAsync))
	}
	if a.DefaultTimeoutMs != nil {
		lines = append(lines, fmt.Sprintf("Timeout: %dms", *a.DefaultTimeoutMs))
	}
	if a.DefaultAcceptance != nil {
		switch x := a.DefaultAcceptance.(type) {
		case *jsObject, []any:
			lines = append(lines, "Acceptance: "+marshalJSON(x, ""))
		default:
			lines = append(lines, "Acceptance: "+jsString(x))
		}
	}
	if a.AcceptanceRole != "" {
		lines = append(lines, "Acceptance role: "+a.AcceptanceRole)
	}
	if a.Source == SourceBuiltin {
		lines = append(lines, "Disabled: "+boolText(a.Disabled != nil && *a.Disabled))
	}
	noneOr := func(l []string) string {
		if len(l) == 0 {
			return "(none)"
		}
		return strings.Join(l, ", ")
	}
	if a.Extensions != nil {
		lines = append(lines, "Extensions: "+noneOr(a.Extensions))
	}
	if a.SubagentOnlyExtensions != nil {
		lines = append(lines, "Subagent-only extensions: "+noneOr(a.SubagentOnlyExtensions))
	}
	if a.MutationTools != nil {
		lines = append(lines, "Mutation tools: "+noneOr(a.MutationTools))
	}
	if thinkingTruthy(a.Thinking) {
		lines = append(lines, "Thinking: "+jsString(a.Thinking))
	}
	if a.Output != "" {
		lines = append(lines, "Output: "+a.Output)
	}
	if a.OutputMode != "" {
		lines = append(lines, "Output mode: "+a.OutputMode)
	}
	if len(a.DefaultReads) > 0 {
		lines = append(lines, "Reads: "+strings.Join(a.DefaultReads, ", "))
	}
	if a.DefaultProgress {
		lines = append(lines, "Progress: true")
	}
	if a.MaxSubagentDepth != nil {
		lines = append(lines, fmt.Sprintf("Max subagent depth: %d", *a.MaxSubagentDepth))
	}
	if a.ToolBudget != nil {
		lines = append(lines, "Tool budget: "+marshalJSON(a.ToolBudget, ""))
	}
	if a.Memory != nil {
		lines = append(lines, fmt.Sprintf("Memory: %s scope, path: %s", a.Memory.Scope, a.Memory.Path))
	}
	if jsTrim(a.SystemPrompt) != "" {
		lines = append(lines, "", "System Prompt:", a.SystemPrompt)
	}
	return strings.Join(lines, "\n")
}

func appendAgentDiagnosticLines(lines []string, diags []Diagnostic) []string {
	if len(diags) == 0 {
		return lines
	}
	lines = append(lines, "", "Invalid agent definitions:")
	for _, d := range diags {
		name := d.Name
		if name == "" {
			name = d.FilePath
		}
		lines = append(lines, fmt.Sprintf("- %s (%s): %s", name, d.Source, d.Error))
	}
	return lines
}

// handleList is agent-management.ts:972.
func (c mgmtContext) handleList(params map[string]any) manageResult {
	scope, ok := normalizeListScope(params["agentScope"])
	if !ok || scope == "" {
		scope = ScopeBoth
	}
	d, err := c.catalog(c.cwd, c.provider())
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	scoped := c.effectiveAgentsForScope(scope, d, c.provider())
	sort.SliceStable(scoped, func(i, j int) bool { return localeLess(scoped[i].Name, scoped[j].Name) })
	ceiling := resolveCurrentSubagentCapabilityCeiling(c.sessionID)
	var visible, agents, restricted []AgentConfig
	for _, a := range scoped {
		if a.Disabled == nil || !*a.Disabled {
			visible = append(visible, a)
		}
	}
	for _, a := range visible {
		if isAgentAllowedByCapabilityCeiling(a.Name, ceiling) {
			agents = append(agents, a)
		} else {
			restricted = append(restricted, a)
		}
	}
	sources := capabilityCeilingAgentRestrictionSources(ceiling)
	proactive := buildProactiveSkillSubagentRecommendationLines(agents, nil, c.proactive, func() ([]ProactiveAvailableSkill, error) {
		skills, err := discoverAvailableSkills(c.cwd)
		var out []ProactiveAvailableSkill
		for _, s := range skills {
			out = append(out, ProactiveAvailableSkill{s.Name, s.Description, s.DisableModelInvocation})
		}
		return out, err
	})
	providers, _ := listExternalJobProviders()
	capabilityMode := params["capabilities"] == true
	var avail map[string]externalCliAvailability
	if capabilityMode {
		avail = externalCliAvailabilityForAgents(append(append([]AgentConfig{}, agents...), restricted...))
	}
	format := func(a AgentConfig) string { return formatAgentListLine(a, providers, avail) }
	header := "Executable agents:"
	if capabilityMode {
		header = "Executable agents (capabilities):"
		format = func(a AgentConfig) string { return formatAgentCapabilitiesLine(a, providers, avail) }
	}
	lines := append([]string{header}, formatAgentListSections(agents, providers, format)...)
	if len(restricted) > 0 {
		suffix := ""
		if len(sources) > 0 {
			suffix = "; capability ceiling: " + strings.Join(sources, ", ")
		}
		lines = append(lines, "", "Restricted agents (not executable in this session"+suffix+"):")
		for _, a := range restricted {
			lines = append(lines, format(a))
		}
	}
	lines = appendAgentDiagnosticLines(lines, d.Diagnostics)
	if len(proactive) > 0 {
		lines = append(lines, "")
		lines = append(lines, proactive...)
	}
	res := mgmtResultOf(strings.Join(lines, "\n"), false)
	if capabilityMode {
		rows := []any{}
		for _, a := range agents {
			rows = append(rows, agentCapabilityRow(a, true, providers, avail, nil))
		}
		for _, a := range restricted {
			rows = append(rows, agentCapabilityRow(a, false, providers, avail, sources))
		}
		snap := obj("agents", rows, "restrictedCount", float64(len(restricted)))
		if len(sources) > 0 {
			snap.set("capabilityCeilingSources", anyStrings(sources))
		}
		res.details = obj("agentCapabilities", snap)
	}
	return res
}

func formatModelSource(a AgentConfig, current *ParentModel) string {
	if a.Override != nil && a.Model != a.Override.Base.Model {
		return a.Override.Scope + " override"
	}
	if a.ModelSource != nil && a.ModelSource.Type == "subagents.defaultModel" && a.Model == a.ModelSource.Model {
		return a.ModelSource.Scope + " defaultModel"
	}
	if a.Model != "" {
		return string(a.Source) + " agent config"
	}
	if current != nil {
		return "inherits current session model"
	}
	return "inherit requested, but no current session model is available"
}

// handleModels is agent-management.ts:1025.
func (c mgmtContext) handleModels(params map[string]any) manageResult {
	requested := ""
	if s, ok := params["agent"].(string); ok {
		requested = jsTrim(s)
	}
	scope, ok := normalizeListScope(params["agentScope"])
	if !ok {
		return mgmtResultOf("agentScope must be 'user', 'project', or 'both' for models.", true)
	}
	provider := c.provider()
	discovered, err := c.catalog(c.cwd, provider)
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	effective := c.effectiveAgentsForScope(scope, discovered, provider)
	sort.SliceStable(effective, func(i, j int) bool { return localeLess(effective[i].Name, effective[j].Name) })
	var available []ModelInfo
	if c.available != nil {
		available = c.available()
	}
	current := c.model
	ceiling := resolveCurrentSubagentCapabilityCeiling(c.sessionID)
	selected := effective
	if requested != "" {
		matches := c.findAgentsInDiscovery(requested, discovered, scope, provider)
		diags := diagnosticsForScope(discovered.Diagnostics, scope)
		diag := blockingDiagnostic(requested, matches, diags)
		if diag == nil {
			if n := sanitizeName(requested); n != requested {
				diag = blockingDiagnostic(n, matches, diags)
			}
		}
		raw, _ := params["agent"].(string)
		if diag != nil {
			return mgmtResultOf(fmt.Sprintf("Agent '%s' has invalid configuration: %s", raw, diag.Error), true)
		}
		names := sortedUniqueNames(matches)
		if len(names) > 1 {
			return mgmtResultOf(fmt.Sprintf("Ambiguous agent alias or name '%s': %s", raw, strings.Join(names, ", ")), true)
		}
		if len(matches) == 0 {
			return mgmtResultOf(fmt.Sprintf("Agent '%s' not found. Available: %s.", raw, joinOrNone(c.availableAgentNamesFromDiscovery(discovered, provider))), true)
		}
		selected = []AgentConfig{matches[0]}
	}
	currentText := "(unavailable)"
	if current != nil {
		currentText = current.Provider + "/" + current.ID
	}
	var lines []string
	if requested != "" {
		lines = append(lines, "Subagent model", "")
	} else {
		lines = append(lines, "Subagent models", "", "Current session model:", "  "+currentText, "")
	}
	type entry struct {
		agent AgentConfig
		name  string
	}
	var entries []entry
	for _, a := range selected {
		if requested != "" {
			entries = append(entries, entry{a, requested})
			continue
		}
		entries = append(entries, entry{a, a.Name})
		for _, al := range a.Aliases {
			entries = append(entries, entry{a, al})
		}
	}
	for _, e := range entries {
		a := e.agent
		prefProvider := a.ModelProvider
		if prefProvider == "" {
			prefProvider = provider
		}
		resolved, _ := resolveSubagentModelOverride(a.Model, current, available, prefProvider, nil)
		effThinking := resolveEffectiveThinking(resolved, a.Thinking)
		if effThinking == "" {
			if b, ok := a.Thinking.(bool); ok && !b {
				effThinking = "off"
			}
		}
		disabled, restrictedText := "", ""
		if a.Disabled != nil && *a.Disabled {
			disabled = "; disabled"
		}
		if !isAgentAllowedByCapabilityCeiling(a.Name, ceiling) {
			restrictedText = "; restricted"
		}
		source := formatModelSource(a, current) + disabled + restrictedText
		resolvedText := resolved
		if resolvedText == "" {
			resolvedText = "(unresolved)"
		}
		thinkingText := effThinking
		if thinkingText == "" {
			thinkingText = "default"
		}
		if requested != "" {
			lines = append(lines, "Agent: "+requested, "Effective model:", "  "+resolvedText, "Source: "+source, "Thinking: "+thinkingText)
			if a.Override != nil {
				lines = append(lines, "Override file:", "  "+a.Override.Path)
			}
			if a.Model != "" && resolved != "" && a.Model != resolved {
				lines = append(lines, "Requested model setting:", "  "+a.Model)
			}
			if a.Disabled != nil && *a.Disabled {
				lines = append(lines, "Disabled: true")
			}
			if !isAgentAllowedByCapabilityCeiling(a.Name, ceiling) {
				lines = append(lines, "Restricted: true")
			}
			lines = append(lines, "Current session model:", "  "+currentText)
			break
		}
		lines = append(lines, e.name, "  model:", "    "+resolvedText, "  source: "+source, "  thinking: "+thinkingText)
		if a.Override != nil {
			lines = append(lines, "  override file:", "    "+a.Override.Path)
		}
		if a.Model != "" && resolved != "" && a.Model != resolved {
			lines = append(lines, "  requested model setting:", "    "+a.Model)
		}
		lines = append(lines, "")
	}
	var ids []string
	for _, m := range available {
		ids = append(ids, m.FullID)
	}
	sort.Strings(ids)
	if requested == "" && len(ids) > 0 {
		lines = append(lines, "Available models in this session's registry (copy an exact provider/id when passing model):", "")
		shown := ids
		if len(shown) > 80 {
			shown = ids[:80]
		}
		for _, id := range shown {
			lines = append(lines, "  "+id)
		}
		if len(ids) > len(shown) {
			lines = append(lines, fmt.Sprintf("  ... and %d more", len(ids)-len(shown)))
		}
		lines = append(lines, "", "Use an exact provider/id from this list when you pass model; bare ids resolve only when unique in the registry.")
	}
	if requested == "" {
		lines = appendAgentDiagnosticLines(lines, diagnosticsForScope(discovered.Diagnostics, scope))
	}
	return mgmtResultOf(strings.Join(lines, "\n"), false)
}

// handleGet is agent-management.ts:1122.
func (c mgmtContext) handleGet(params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if name == "" {
		return mgmtResultOf("Specify 'agent' for get.", true)
	}
	scope, ok := normalizeListScope(params["agentScope"])
	if !ok {
		return mgmtResultOf("agentScope must be 'user', 'project', or 'both' for get.", true)
	}
	d, err := c.catalog(c.cwd, c.provider())
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	matches := c.findAgentsInDiscovery(name, d, scope, "")
	diags := diagnosticsForScope(d.Diagnostics, scope)
	raw := jsTrim(name)
	diag := blockingDiagnostic(raw, matches, diags)
	if diag == nil {
		if n := sanitizeName(raw); n != raw {
			diag = blockingDiagnostic(n, matches, diags)
		}
	}
	if diag != nil {
		return mgmtResultOf(fmt.Sprintf("Agent '%s' has invalid configuration: %s", name, diag.Error), true)
	}
	names := sortedUniqueNames(matches)
	if len(names) > 1 {
		return mgmtResultOf(fmt.Sprintf("Ambiguous agent alias or name '%s': %s", name, strings.Join(names, ", ")), true)
	}
	if len(matches) == 0 {
		return mgmtResultOf(fmt.Sprintf("Agent '%s' not found. Available: %s.", name, joinOrNone(c.availableAgentNamesFromDiscovery(d, ""))), true)
	}
	details := make([]string, len(matches))
	for i, m := range matches {
		details[i] = formatAgentDetail(m)
	}
	return mgmtResultOf(strings.Join(details, "\n\n"), false)
}

func (c mgmtContext) targetDir(d DiscoveryAll, scope string) string {
	if scope == "user" {
		return d.UserDir
	}
	if d.ProjectDir != "" {
		return d.ProjectDir
	}
	return filepath.Join(getProjectConfigDir(c.cwd), "agents")
}

const chainsRemoved = "Durable chain definitions were removed; use workflowScript or /prompt-workflow for repeatable workflows."

// handleCreate is agent-management.ts:1142.
func (c mgmtContext) handleCreate(params map[string]any) manageResult {
	pc := configObject(params["config"])
	if pc.status == "error" {
		return mgmtResultOf(pc.msg, true)
	}
	if pc.status == "missing" {
		return mgmtResultOf("config required for create.", true)
	}
	cfg := pc.value
	nameV, _ := cfg.vals["name"].(string)
	if jsTrim(nameV) == "" {
		return mgmtResultOf("config.name is required and must be a non-empty string.", true)
	}
	descV, _ := cfg.vals["description"].(string)
	if jsTrim(descV) == "" {
		return mgmtResultOf("config.description is required and must be a non-empty string.", true)
	}
	name := sanitizeName(nameV)
	if name == "" {
		return mgmtResultOf("config.name is invalid after sanitization. Use letters, numbers, spaces, or hyphens.", true)
	}
	pkgRaw, hasPkg := cfg.vals["package"]
	pkgStr, pkgIsStr := pkgRaw.(string)
	pkg, perr := parsePackageName(pkgStr, hasPkg && pkgIsStr || (hasPkg && pkgRaw != nil && pkgRaw != false), "config.package")
	if hasPkg && !pkgIsStr && pkgRaw != nil && pkgRaw != false {
		return mgmtResultOf("config.package is invalid after sanitization.", true)
	}
	if perr != "" {
		return mgmtResultOf(perr, true)
	}
	runtimeName := buildRuntimeName(name, pkg)
	var scope string
	switch s := cfg.vals["scope"].(type) {
	case nil:
		if hasKey(cfg, "scope") {
			return mgmtResultOf("config.scope must be 'user' or 'project'.", true)
		}
		scope = "user"
	case string:
		if s != "user" && s != "project" {
			return mgmtResultOf("config.scope must be 'user' or 'project'.", true)
		}
		scope = s
	default:
		return mgmtResultOf("config.scope must be 'user' or 'project'.", true)
	}
	if hasKey(cfg, "steps") {
		return mgmtResultOf(chainsRemoved, true)
	}
	d, err := c.catalog(c.cwd, "")
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	targetDir := c.targetDir(d, scope)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	if c.nameExistsInScope(scope, runtimeName, "") {
		return mgmtResultOf(fmt.Sprintf("Name '%s' already exists in %s scope. Use update instead.", runtimeName, scope), true)
	}
	targetPath := filepath.Join(targetDir, runtimeName+".md")
	if _, err := os.Stat(targetPath); err == nil {
		return mgmtResultOf(fmt.Sprintf("File already exists at %s but is not a valid agent definition. Remove or rename it first.", targetPath), true)
	}
	var warnings []string
	for _, a := range d.Builtin {
		if a.Name == runtimeName {
			warnings = append(warnings, fmt.Sprintf("Note: this shadows the builtin agent '%s'.", runtimeName))
			break
		}
	}
	agent := AgentConfig{Name: runtimeName, LocalName: name, PackageName: pkg, Description: jsTrim(descV), Source: AgentSource(scope), FilePath: targetPath,
		SystemPromptMode: defaultSystemPromptMode(name), InheritProjectContext: defaultInheritProjectContext(name)}
	if msg := applyAgentConfig(&agent, cfg); msg != "" {
		return mgmtResultOf(msg, true)
	}
	if msg := validateCodeOwnedProfileRunner(agent.Name, agent.LocalName, agent.Aliases, agent.Runner); msg != "" {
		return mgmtResultOf(msg, true)
	}
	if w := c.modelWarning(agent.Model); w != "" {
		warnings = append(warnings, w)
	}
	if w := skillsWarning(c.cwd, agent); w != "" {
		warnings = append(warnings, w)
	}
	if err := os.WriteFile(targetPath, []byte(serializeAgent(agent, SerializeAgentOptions{})), 0o644); err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	c.changed()
	return mgmtResultOf(strings.Join(append([]string{fmt.Sprintf("Created agent '%s' at %s.", runtimeName, targetPath)}, warnings...), "\n"), false)
}

// handleUpdate is agent-management.ts:1193.
func (c mgmtContext) handleUpdate(params map[string]any) manageResult {
	agentName, _ := params["agent"].(string)
	if agentName == "" {
		return mgmtResultOf("Specify 'agent' for update.", true)
	}
	pc := configObject(params["config"])
	if pc.status == "error" {
		return mgmtResultOf(pc.msg, true)
	}
	if pc.status == "missing" {
		return mgmtResultOf("config required for update.", true)
	}
	cfg := pc.value
	if hasKey(cfg, "steps") {
		return mgmtResultOf(chainsRemoved, true)
	}
	var warnings []string
	hint := asDisambiguationScope(params["agentScope"])
	findScope := ScopeBoth
	if hint != "" {
		findScope = AgentScope(hint)
	}
	target, errRes := c.resolveTarget(agentName, c.findAgents(agentName, c.cwd, findScope), c.cwd, hint)
	if errRes != nil {
		return *errRes
	}
	if target.Source != SourceUser && target.Source != SourceProject {
		return mgmtResultOf(fmt.Sprintf("Cannot update %s agent '%s'. Eject it to user or project scope first.", target.Source, target.Name), true)
	}
	updated, err := editableAgentConfig(*target)
	if err != nil {
		return mgmtResultOf(fmt.Sprintf("Could not reread agent definition %s before updating '%s': %s", target.FilePath, target.Name, err.Error()), true)
	}
	oldName := target.Name
	if hasKey(cfg, "name") {
		if s, ok := cfg.vals["name"].(string); !ok || jsTrim(s) == "" {
			return mgmtResultOf("config.name must be a non-empty string when provided.", true)
		}
	}
	if hasKey(cfg, "description") {
		if s, ok := cfg.vals["description"].(string); !ok || jsTrim(s) == "" {
			return mgmtResultOf("config.description must be a non-empty string when provided.", true)
		}
	}
	newLocal := target.LocalName
	if newLocal == "" {
		newLocal = frontmatterName(target.Name, target.LocalName, target.PackageName)
	}
	if hasKey(cfg, "name") {
		newLocal = sanitizeName(cfg.vals["name"].(string))
		if newLocal == "" {
			return mgmtResultOf("config.name is invalid after sanitization.", true)
		}
	}
	newPkg := target.PackageName
	if hasKey(cfg, "package") {
		raw := cfg.vals["package"]
		s, isS := raw.(string)
		present := isS || (raw != nil && raw != false)
		if present && !isS {
			return mgmtResultOf("config.package is invalid after sanitization.", true)
		}
		pkg, perr := parsePackageName(s, present, "config.package")
		if perr != "" {
			return mgmtResultOf(perr, true)
		}
		newPkg = pkg
	}
	if msg := applyAgentConfig(&updated, cfg); msg != "" {
		return mgmtResultOf(msg, true)
	}
	preserve := preservedAgentFrontmatterFields(*target, cfg)
	updated.LocalName = newLocal
	updated.PackageName = newPkg
	updated.Name = buildRuntimeName(newLocal, newPkg)
	if msg := validateCodeOwnedProfileRunner(updated.Name, updated.LocalName, updated.Aliases, updated.Runner); msg != "" {
		return mgmtResultOf(msg, true)
	}
	if hasKey(cfg, "description") {
		updated.Description = jsTrim(cfg.vals["description"].(string))
	}
	if hasKey(cfg, "model") {
		if w := c.modelWarning(updated.Model); w != "" {
			warnings = append(warnings, w)
		}
	}
	if hasKey(cfg, "skills") || hasKey(cfg, "skillPath") {
		if w := skillsWarning(c.cwd, updated); w != "" {
			warnings = append(warnings, w)
		}
	}
	if updated.Name != oldName {
		p, e := c.renamePath(target.FilePath, updated.Name, string(target.Source))
		if e != "" {
			return mgmtResultOf(e, true)
		}
		updated.FilePath = p
	}
	if err := os.WriteFile(updated.FilePath, []byte(serializeAgent(updated, SerializeAgentOptions{PreserveFrontmatterFields: preserve})), 0o644); err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	c.changed()
	headline := fmt.Sprintf("Updated agent '%s' at %s.", updated.Name, updated.FilePath)
	if updated.Name != oldName {
		headline = fmt.Sprintf("Updated agent '%s' to '%s' at %s.", oldName, updated.Name, updated.FilePath)
	}
	return mgmtResultOf(strings.Join(append([]string{headline}, warnings...), "\n"), false)
}

// handleDelete is agent-management.ts:1258.
func (c mgmtContext) handleDelete(params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if name == "" {
		return mgmtResultOf("Specify 'agent' for delete.", true)
	}
	hint := asDisambiguationScope(params["agentScope"])
	findScope := ScopeBoth
	if hint != "" {
		findScope = AgentScope(hint)
	}
	target, errRes := c.resolveTarget(name, c.findAgents(name, c.cwd, findScope), c.cwd, hint)
	if errRes != nil {
		return *errRes
	}
	if err := os.Remove(target.FilePath); err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	c.changed()
	return mgmtResultOf(fmt.Sprintf("Deleted agent '%s' at %s.", target.Name, target.FilePath), false)
}

func findBundled(d DiscoveryAll, raw, sanitized string) *AgentConfig {
	for _, list := range [][]AgentConfig{d.Package, d.Builtin} {
		for i := range list {
			if list[i].Name == raw || list[i].Name == sanitized {
				return &list[i]
			}
		}
	}
	return nil
}

// handleEject is agent-management.ts:1269.
func (c mgmtContext) handleEject(params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if name == "" {
		return mgmtResultOf("Specify 'agent' for eject.", true)
	}
	raw := jsTrim(name)
	sanitized := sanitizeName(raw)
	scope, errRes := actionScope(params["agentScope"], "eject")
	if errRes != nil {
		return *errRes
	}
	d, err := c.catalog(c.cwd, "")
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	source := findBundled(d, raw, sanitized)
	if source == nil {
		return mgmtResultOf(fmt.Sprintf("Agent '%s' not found or is not a bundled/package agent. eject copies a builtin or package agent to %s scope so it can be customized. Available: %s.", raw, scope, joinOrNone(c.availableAgentNames(c.cwd))), true)
	}
	runtimeName := source.Name
	custom := d.User
	if scope == "project" {
		custom = d.Project
	}
	for _, a := range custom {
		if a.Name == runtimeName {
			return mgmtResultOf(fmt.Sprintf("Agent '%s' is already a custom %s agent at %s. Edit it with { action: \"update\", agent: \"%s\" } or delete it first.", runtimeName, scope, a.FilePath, runtimeName), true)
		}
	}
	if c.nameExistsInScope(scope, runtimeName, "") {
		return mgmtResultOf(fmt.Sprintf("An agent named '%s' already exists in %s scope. Remove or rename it first.", runtimeName, scope), true)
	}
	targetDir := c.targetDir(d, scope)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	targetPath := filepath.Join(targetDir, runtimeName+".md")
	if _, err := os.Stat(targetPath); err == nil {
		return mgmtResultOf(fmt.Sprintf("File already exists at %s but is not a valid agent definition. Remove or rename it first.", targetPath), true)
	}
	content, err := readAgentSource(*source)
	if err != nil {
		return mgmtResultOf(fmt.Sprintf("Failed to read source agent at %s: %s", source.FilePath, err.Error()), true)
	}
	if err := os.WriteFile(targetPath, content, 0o644); err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	c.changed()
	return mgmtResultOf(fmt.Sprintf("Ejected agent '%s' from %s to %s scope at %s. Edit it there to customize; it shadows the bundled %s agent of the same name.", runtimeName, source.Source, scope, targetPath, source.Source), false)
}

// readAgentSource reads a bundled agent's definition: the embedded file for a builtin, the file on disk for a package agent.
func readAgentSource(a AgentConfig) ([]byte, error) {
	if a.Source == SourceBuiltin {
		return builtinFS.ReadFile(a.FilePath)
	}
	return os.ReadFile(a.FilePath)
}

const noProjectRoot = "Project override is not available here: no project config root (.pi or .agents) was found above the cwd. Use agentScope: 'user' or run from inside a project."

// handleDisable is agent-management.ts:1308.
func (c mgmtContext) handleDisable(params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if name == "" {
		return mgmtResultOf("Specify 'agent' for disable.", true)
	}
	raw := jsTrim(name)
	scope, errRes := actionScope(params["agentScope"], "disable")
	if errRes != nil {
		return *errRes
	}
	d, err := c.catalog(c.cwd, "")
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	if scope == "project" && d.ProjectSettingsPath == "" {
		return mgmtResultOf(noProjectRoot, true)
	}
	effective, msg := resolveEffectiveAgent(d, raw)
	if msg != "" {
		return mgmtResultOf(msg, true)
	}
	if effective == nil {
		return mgmtResultOf(fmt.Sprintf("Agent '%s' not found. Available: %s.", raw, joinOrNone(c.availableAgentNames(c.cwd))), true)
	}
	runtimeName := effective.Name
	disabled := newObject()
	disabled.set("disabled", true)
	settingsPath, err := mergeBuiltinAgentOverride(c.cwd, runtimeName, scope, disabled)
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	d2, err := c.catalog(c.cwd, "")
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	after, _ := resolveEffectiveAgent(d2, raw)
	if after != nil && after.Disabled != nil && *after.Disabled {
		c.changed()
		return mgmtResultOf(fmt.Sprintf("Disabled agent '%s' via %s settings override at %s. It is now hidden from runtime discovery and { action: \"list\" }.", runtimeName, scope, settingsPath), false)
	}
	scopeOf := "project"
	if after != nil && after.Override != nil {
		scopeOf = after.Override.Scope
	}
	return mgmtResultOf(fmt.Sprintf("Wrote a disabled override for '%s' at %s, but the agent is still enabled. A higher-precedence %s override is likely winning. Try agentScope: '%s'.", runtimeName, settingsPath, scopeOf, scopeOf), true)
}

// handleEnable is agent-management.ts:1333.
func (c mgmtContext) handleEnable(params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if name == "" {
		return mgmtResultOf("Specify 'agent' for enable.", true)
	}
	raw := jsTrim(name)
	scope, errRes := actionScope(params["agentScope"], "enable")
	if errRes != nil {
		return *errRes
	}
	d, err := c.catalog(c.cwd, "")
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	if scope == "project" && d.ProjectSettingsPath == "" {
		return mgmtResultOf(noProjectRoot, true)
	}
	effective, msg := resolveEffectiveAgent(d, raw)
	if msg != "" {
		return mgmtResultOf(msg, true)
	}
	if effective == nil {
		return mgmtResultOf(fmt.Sprintf("Agent '%s' not found. Available: %s.", raw, joinOrNone(c.availableAgentNames(c.cwd))), true)
	}
	runtimeName := effective.Name
	settingsPath, removed, err := removeBuiltinAgentOverrideFields(c.cwd, runtimeName, scope, []string{"disabled"})
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	d2, err := c.catalog(c.cwd, "")
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	after, _ := resolveEffectiveAgent(d2, raw)
	if after != nil && (after.Disabled == nil || !*after.Disabled) {
		if removed {
			c.changed()
			return mgmtResultOf(fmt.Sprintf("Enabled agent '%s' (removed disabled override at %s).", runtimeName, settingsPath), false)
		}
		return mgmtResultOf(fmt.Sprintf("Agent '%s' is already enabled.", runtimeName), false)
	}
	if after != nil && after.Override != nil && after.Override.Scope != "" && after.Override.Scope != scope {
		return mgmtResultOf(fmt.Sprintf("Agent '%s' is still disabled via a %s scope override at %s. Specify agentScope: '%s' to enable it.", runtimeName, after.Override.Scope, after.Override.Path, after.Override.Scope), true)
	}
	oScope, oPath := scope, settingsPath
	if after != nil && after.Override != nil {
		oScope, oPath = after.Override.Scope, after.Override.Path
	}
	return mgmtResultOf(fmt.Sprintf("Agent '%s' is still disabled after removing the %s disabled override. It may be hidden via subagents.disableBuiltins in %s settings at %s.", runtimeName, scope, oScope, oPath), true)
}

// handleReset is agent-management.ts:1362.
func (c mgmtContext) handleReset(params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if name == "" {
		return mgmtResultOf("Specify 'agent' for reset.", true)
	}
	raw := jsTrim(name)
	sanitized := sanitizeName(raw)
	scope, errRes := actionScope(params["agentScope"], "reset")
	if errRes != nil {
		return *errRes
	}
	d, err := c.catalog(c.cwd, "")
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	if scope == "project" && d.ProjectSettingsPath == "" {
		return mgmtResultOf(noProjectRoot, true)
	}
	find := func(list []AgentConfig) *AgentConfig {
		for i := range list {
			if list[i].Name == raw || list[i].Name == sanitized {
				return &list[i]
			}
		}
		return nil
	}
	bundled := findBundled(d, raw, sanitized)
	if bundled == nil {
		if custom := find(append(append([]AgentConfig{}, d.User...), d.Project...)); custom != nil {
			return mgmtResultOf(fmt.Sprintf("Agent '%s' has no bundled default to reset to. Use { action: \"delete\", agent: \"%s\" } to remove the custom %s agent.", raw, custom.Name, custom.Source), true)
		}
		return mgmtResultOf(fmt.Sprintf("Agent '%s' not found. Available: %s.", raw, joinOrNone(c.availableAgentNames(c.cwd))), true)
	}
	runtimeName := bundled.Name
	scopeList := d.User
	if scope == "project" {
		scopeList = d.Project
	}
	var lines []string
	if custom := find(scopeList); custom != nil {
		if err := os.Remove(custom.FilePath); err != nil {
			return mgmtResultOf(err.Error(), true)
		}
		lines = append(lines, fmt.Sprintf("Deleted custom %s agent file at %s.", scope, custom.FilePath))
	}
	path, removed, preserved, err := removeBuiltinAgentOverride(c.cwd, runtimeName, scope, true)
	if err != nil {
		return mgmtResultOf(err.Error(), true)
	}
	if removed {
		verb, tail := "Removed", ""
		if preserved {
			verb, tail = "Cleared customization in", " Retained machine placement."
		}
		lines = append(lines, fmt.Sprintf("%s %s settings override at %s.%s", verb, scope, path, tail))
	}
	if len(lines) == 0 {
		other := "user"
		otherList := d.User
		if scope == "user" {
			other, otherList = "project", d.Project
		}
		note := ""
		if find(otherList) != nil || (bundled.Override != nil && bundled.Override.Scope == other) {
			note = fmt.Sprintf(" Customization exists in %s scope; specify agentScope: '%s' to reset it.", other, other)
		}
		return mgmtResultOf(fmt.Sprintf("Agent '%s' has no %s customization to reset.%s It is at its bundled %s default.", runtimeName, scope, note, bundled.Source), false)
	}
	lines = append(lines, fmt.Sprintf("Reset agent '%s' to its bundled %s default.", runtimeName, bundled.Source))
	c.changed()
	return mgmtResultOf(strings.Join(lines, "\n"), false)
}

// handleManagementAction is agent-management.ts:1404.
func handleManagementAction(action string, params map[string]any, ctx mgmtContext) manageResult {
	switch action {
	case "list":
		return ctx.handleList(params)
	case "get":
		return ctx.handleGet(params)
	case "models":
		return ctx.handleModels(params)
	case "create":
		return ctx.handleCreate(params)
	case "update":
		return ctx.handleUpdate(params)
	case "delete":
		return ctx.handleDelete(params)
	case "eject":
		return ctx.handleEject(params)
	case "disable":
		return ctx.handleDisable(params)
	case "enable":
		return ctx.handleEnable(params)
	case "reset":
		return ctx.handleReset(params)
	}
	return mgmtResultOf("Unknown action: "+action, true)
}

// deepJS converts the nested maps and slices of a decoded tool call to the ordered objects the config code reads (a map has no
// order, so its keys are visited sorted).
func deepJS(v any) any {
	switch x := v.(type) {
	case map[string]any:
		o := newObject()
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			o.set(k, deepJS(x[k]))
		}
		return o
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepJS(e)
		}
		return out
	}
	return v
}
