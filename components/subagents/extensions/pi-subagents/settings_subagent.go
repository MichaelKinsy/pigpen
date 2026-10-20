package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// Subagent settings and agent overrides (src/agents/agents.ts:55-1796).

// OverrideBase is the agent as it was before a settings override (agents.ts BuiltinAgentOverrideBase).
type OverrideBase struct {
	Description            string
	Machine                string
	Output                 string
	OutputMode             string
	DefaultReads           []string
	Model                  string
	ModelProvider          string
	Fast                   *bool
	Thinking               any
	SystemPromptMode       string
	InheritProjectContext  bool
	InheritGlobalContext   bool
	InheritSkills          bool
	DefaultContext         string
	AcceptanceRole         string
	Disabled               *bool
	SystemPrompt           string
	Skills                 []string
	SkillPath              []string
	Tools                  []string
	ExcludeTools           []string
	AllowNestedSubagents   *bool
	AllowedAgents          []string
	McpDirectTools         []string
	Extensions             []string
	SubagentOnlyExtensions []string
	MutationTools          []string
	ToolBudget             any
}

// subagentSettings is agents.ts SubagentSettings. An override is an ordered object whose values are the parsed field values:
// a string, a boolean (false clears), a list of strings (as []any), or the toolBudget object.
type subagentSettings struct {
	overrides                     map[string]*jsObject
	providerOverrides             map[string]map[string]*jsObject
	agentScanDirs                 []string
	agentExcludeDirs              []string
	defaultModel                  string
	defaultProvider               string
	defaultThinking               string
	maxThinking                   string
	defaultExtensions             []string
	defaultSubagentOnlyExtensions []string
	disableBuiltins               *bool
	disableThinking               *bool
	modelScope                    *ModelScopeConfig
}

func emptySubagentSettings() subagentSettings {
	return subagentSettings{overrides: map[string]*jsObject{}, providerOverrides: map[string]map[string]*jsObject{}}
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ovList reads a list-or-false override value: (list, present). false yields (nil, true) and isFalse.
func ovList(o *jsObject, k string) (list []string, isFalse, present bool) {
	v, ok := o.get(k)
	if !ok {
		return nil, false, false
	}
	if b, isB := v.(bool); isB && !b {
		return nil, true, true
	}
	return toStrings(v), false, true
}

// ovString reads a string-or-false override value.
func ovString(o *jsObject, k string) (s string, isFalse, present bool) {
	v, ok := o.get(k)
	if !ok {
		return "", false, false
	}
	if b, isB := v.(bool); isB && !b {
		return "", true, true
	}
	str, _ := v.(string)
	return str, false, true
}

func overrideErr(name, filePath, field, expected string) error {
	return fmt.Errorf("Builtin override '%s' in '%s' has invalid '%s'; expected %s.", name, filePath, field, expected)
}

// parseOverrideStringArrayOrFalse is agents.ts:949.
func parseOverrideStringArrayOrFalse(v any, present bool, filePath, name, field string) (any, bool, error) {
	if !present {
		return nil, false, nil
	}
	if b, ok := v.(bool); ok && !b {
		return false, true, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false, overrideErr(name, filePath, field, "an array of strings or false")
	}
	items := []any{}
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, false, overrideErr(name, filePath, field, "an array of strings or false")
		}
		if t := jsTrim(s); t != "" {
			items = append(items, t)
		}
	}
	return items, true, nil
}

func hasControlChar(s string) bool {
	for _, r := range s {
		if r <= 0x1f || r == 0x7f {
			return true
		}
	}
	return false
}

// validateOptionalMachine is agents.ts:981. present false is an absent or false value.
func validateOptionalMachine(v any, present bool, label string) (string, error) {
	if !present {
		return "", nil
	}
	if b, ok := v.(bool); ok && !b {
		return "", nil
	}
	s, ok := v.(string)
	if !ok || jsTrim(s) == "" {
		return "", fmt.Errorf("%s must be a non-empty string or false.", label)
	}
	m := jsTrim(s)
	if utf16Length(m) > 128 {
		return "", fmt.Errorf("%s must be 128 characters or fewer.", label)
	}
	if hasControlChar(m) {
		return "", fmt.Errorf("%s contains control characters.", label)
	}
	return m, nil
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16Len(r)
	}
	return n
}

// parseBuiltinOverrideEntry is agents.ts:990. It returns nil for an entry without fields.
func parseBuiltinOverrideEntry(name string, value any, filePath string) (*jsObject, error) {
	in, ok := value.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("Builtin override '%s' in '%s' must be an object.", name, filePath)
	}
	if _, ok := in.get("fallbackModels"); ok {
		return nil, fmt.Errorf("Builtin override '%s' in '%s' uses removed field 'fallbackModels'; configure one model instead.", name, filePath)
	}
	out := newObject()
	bad := func(field, expected string) (*jsObject, error) {
		return nil, overrideErr(name, filePath, field, expected)
	}
	isBool := func(v any) bool { _, ok := v.(bool); return ok }
	isFalse := func(v any) bool { b, ok := v.(bool); return ok && !b }
	nonEmptyStr := func(v any) (string, bool) { s, ok := v.(string); return s, ok && jsTrim(s) != "" }

	if v, ok := in.get("description"); ok {
		s, good := nonEmptyStr(v)
		if !good {
			return bad("description", "a non-empty string")
		}
		out.set("description", jsTrim(s))
	}
	if v, ok := in.get("output"); ok {
		if _, good := nonEmptyStr(v); good || isFalse(v) {
			out.set("output", v)
		} else {
			return bad("output", "a non-empty string or false")
		}
	}
	if v, ok := in.get("outputMode"); ok {
		if s, _ := v.(string); s == "inline" || s == "file-only" {
			out.set("outputMode", s)
		} else {
			return bad("outputMode", "'inline' or 'file-only'")
		}
	}
	if v, ok := in.get("model"); ok {
		if _, isS := v.(string); isS || isFalse(v) {
			out.set("model", v)
		} else {
			return bad("model", "a string or false")
		}
	}
	if v, ok := in.get("fast"); ok {
		if !isBool(v) {
			return bad("fast", "a boolean")
		}
		out.set("fast", v)
	}
	if v, ok := in.get("thinking"); ok {
		if _, isS := v.(string); isS || isFalse(v) {
			out.set("thinking", v)
		} else {
			return bad("thinking", "a string or false")
		}
	}
	if v, ok := in.get("systemPromptMode"); ok {
		if s, _ := v.(string); s == "append" || s == "replace" {
			out.set("systemPromptMode", s)
		} else {
			return bad("systemPromptMode", "'append' or 'replace'")
		}
	}
	for _, f := range []string{"inheritProjectContext", "inheritGlobalContext", "inheritSkills"} {
		if v, ok := in.get(f); ok {
			if !isBool(v) {
				return bad(f, "a boolean")
			}
			out.set(f, v)
		}
	}
	if v, ok := in.get("defaultContext"); ok {
		if s, _ := v.(string); s == "fresh" || s == "fork" || isFalse(v) {
			out.set("defaultContext", v)
		} else {
			return bad("defaultContext", "'fresh', 'fork', or false")
		}
	}
	if v, ok := in.get("acceptanceRole"); ok {
		if s, _ := v.(string); s == "read-only" || s == "writer" || isFalse(v) {
			out.set("acceptanceRole", v)
		} else {
			return bad("acceptanceRole", "'read-only', 'writer', or false")
		}
	}
	if v, ok := in.get("disabled"); ok {
		if !isBool(v) {
			return bad("disabled", "a boolean")
		}
		out.set("disabled", v)
	}
	if v, ok := in.get("toolBudget"); ok {
		if o, isO := v.(*jsObject); isO || isFalse(v) {
			if isO {
				out.set("toolBudget", o)
			} else {
				out.set("toolBudget", false)
			}
		} else {
			return bad("toolBudget", "an object or false")
		}
	}
	if v, ok := in.get("systemPrompt"); ok {
		if _, isS := v.(string); !isS {
			return bad("systemPrompt", "a string")
		}
		out.set("systemPrompt", v)
	}
	mv, mok := in.get("machine")
	if isFalse(mv) {
		out.set("machine", false)
	} else {
		machine, err := validateOptionalMachine(mv, mok && mv != nil, fmt.Sprintf("Builtin override '%s' in '%s' field 'machine'", name, filePath))
		if err != nil {
			return nil, err
		}
		if mok && mv == nil {
			// null is neither undefined nor false: the original rejects it
			return nil, fmt.Errorf("Builtin override '%s' in '%s' field 'machine' must be a non-empty string or false.", name, filePath)
		}
		if machine != "" {
			out.set("machine", machine)
		}
	}
	list := func(field string) error {
		v, ok := in.get(field)
		res, set, err := parseOverrideStringArrayOrFalse(v, ok, filePath, name, field)
		if err != nil {
			return err
		}
		if set {
			out.set(field, res)
		}
		return nil
	}
	if err := list("defaultReads"); err != nil {
		return nil, err
	}
	if v, ok := in.get("defaultProvider"); ok {
		if isFalse(v) {
			out.set("defaultProvider", false)
		} else if s, good := nonEmptyStr(v); good {
			out.set("defaultProvider", jsTrim(s))
		} else {
			return bad("defaultProvider", "a non-empty string or false")
		}
	}
	if err := list("skills"); err != nil {
		return nil, err
	}
	// tools: "inherit", false, or a list
	tv, tok := in.get("tools")
	if s, isS := tv.(string); isS && jsTrim(s) == "inherit" {
		out.set("tools", "inherit")
	} else if tok {
		if _, isArr := tv.([]any); isArr || isFalse(tv) {
			res, set, err := parseOverrideStringArrayOrFalse(tv, true, filePath, name, "tools")
			if err != nil {
				return nil, err
			}
			if set {
				out.set("tools", res)
			}
		} else {
			return nil, fmt.Errorf("Builtin override '%s' in '%s' has invalid 'tools'; expected an array of strings, \"inherit\", or false.", name, filePath)
		}
	}
	if err := list("excludeTools"); err != nil {
		return nil, err
	}
	if v, ok := in.get("allowNestedSubagents"); ok {
		if !isBool(v) {
			return bad("allowNestedSubagents", "a boolean")
		}
		out.set("allowNestedSubagents", v)
	}
	av, aok := in.get("allowedAgents")
	res, set, err := parseOverrideStringArrayOrFalse(av, aok, filePath, name, "allowedAgents")
	if err != nil {
		return nil, err
	}
	if set {
		if b, ok := res.(bool); ok && !b {
			out.set("allowedAgents", false)
		} else {
			norm, err := normalizeCapabilityCeilingAllowedAgents(res)
			if err != nil {
				return nil, err
			}
			out.set("allowedAgents", anyStrings(norm))
		}
	}
	for _, f := range []string{"extensions", "subagentOnlyExtensions", "mutationTools"} {
		if err := list(f); err != nil {
			return nil, err
		}
	}
	if len(out.keys) == 0 {
		return nil, nil
	}
	return out, nil
}

func parseThinkingLevel(v any, field string) (string, error) {
	if s, ok := v.(string); ok {
		t := jsTrim(s)
		for _, l := range thinkingLevelList {
			if l == t {
				return t, nil
			}
		}
	}
	return "", fmt.Errorf("Invalid %s; expected one of %s.", field, strings.Join(thinkingLevelList, ", "))
}

func settingsErr(filePath, field, expected string) error {
	return fmt.Errorf("Subagent settings in '%s' have invalid '%s'; expected %s.", filePath, field, expected)
}

// readSubagentSettings is agents.ts:1155. An empty path is no settings file.
func readSubagentSettings(filePath string) (subagentSettings, error) {
	if filePath == "" {
		return emptySubagentSettings(), nil
	}
	settings, err := readSettingsFileStrict(filePath)
	if err != nil {
		return subagentSettings{}, err
	}
	sub := settings.obj("subagents")
	if sub == nil {
		return emptySubagentSettings(), nil
	}
	out := emptySubagentSettings()
	boolField := func(k string) (*bool, error) {
		v, ok := sub.get(k)
		if !ok {
			return nil, nil
		}
		b, isB := v.(bool)
		if !isB {
			return nil, settingsErr(filePath, k, "a boolean")
		}
		return &b, nil
	}
	strField := func(k string) (string, error) {
		v, ok := sub.get(k)
		if !ok {
			return "", nil
		}
		s, isS := v.(string)
		if !isS || jsTrim(s) == "" {
			return "", settingsErr(filePath, k, "a non-empty string")
		}
		return jsTrim(s), nil
	}
	listField := func(k string) ([]string, error) {
		v, ok := sub.get(k)
		if !ok {
			return nil, nil
		}
		arr, isArr := v.([]any)
		res := []string{}
		if !isArr {
			return nil, settingsErr(filePath, k, "an array of non-empty strings")
		}
		for _, e := range arr {
			s, isS := e.(string)
			if !isS || jsTrim(s) == "" {
				return nil, settingsErr(filePath, k, "an array of non-empty strings")
			}
			res = append(res, jsTrim(s))
		}
		return res, nil
	}
	var e error
	if out.disableBuiltins, e = boolField("disableBuiltins"); e != nil {
		return subagentSettings{}, e
	}
	if out.disableThinking, e = boolField("disableThinking"); e != nil {
		return subagentSettings{}, e
	}
	if out.defaultModel, e = strField("defaultModel"); e != nil {
		return subagentSettings{}, e
	}
	if out.defaultProvider, e = strField("defaultProvider"); e != nil {
		return subagentSettings{}, e
	}
	if out.defaultThinking, e = strField("defaultThinking"); e != nil {
		return subagentSettings{}, e
	}
	if v, ok := sub.get("maxThinking"); ok {
		lvl, err := parseThinkingLevel(v, fmt.Sprintf("'%s' subagents.maxThinking", filePath))
		if err != nil {
			return subagentSettings{}, fmt.Errorf("Subagent settings in '%s' have invalid 'maxThinking'; expected one of off, minimal, low, medium, high, xhigh, or max.", filePath)
		}
		out.maxThinking = lvl
	}
	if out.defaultExtensions, e = listField("defaultExtensions"); e != nil {
		return subagentSettings{}, e
	}
	if out.defaultSubagentOnlyExtensions, e = listField("defaultSubagentOnlyExtensions"); e != nil {
		return subagentSettings{}, e
	}
	if out.agentScanDirs, e = listField("agentScanDirs"); e != nil {
		return subagentSettings{}, e
	}
	if out.agentExcludeDirs, e = listField("agentExcludeDirs"); e != nil {
		return subagentSettings{}, e
	}
	msv, msok := sub.get("modelScope")
	if out.modelScope, e = parseModelScopeConfig(msv, msok, filePath); e != nil {
		return subagentSettings{}, e
	}
	if ao := sub.obj("agentOverrides"); ao != nil {
		for _, name := range ao.order() {
			ov, err := parseBuiltinOverrideEntry(name, ao.vals[name], filePath)
			if err != nil {
				return subagentSettings{}, err
			}
			if ov != nil {
				out.overrides[name] = ov
			}
		}
	}
	if pv, ok := sub.get("agentOverridesByProvider"); ok {
		po, isO := pv.(*jsObject)
		if !isO {
			return subagentSettings{}, settingsErr(filePath, "agentOverridesByProvider", "an object keyed by provider")
		}
		for _, provider := range po.order() {
			agents, isO := po.vals[provider].(*jsObject)
			if !isO {
				return subagentSettings{}, settingsErr(filePath, "agentOverridesByProvider."+provider, "an object keyed by agent")
			}
			entries := map[string]*jsObject{}
			for _, agentName := range agents.order() {
				ov, err := parseBuiltinOverrideEntry("agentOverridesByProvider."+provider+"."+agentName, agents.vals[agentName], filePath)
				if err != nil {
					return subagentSettings{}, err
				}
				if ov != nil {
					entries[agentName] = ov
				}
			}
			out.providerOverrides[provider] = entries
		}
	}
	return out, nil
}

// selectProviderOverrides is agents.ts:1288.
func selectProviderOverrides(s subagentSettings, provider string) subagentSettings {
	if provider == "" {
		return s
	}
	selected, ok := s.providerOverrides[provider]
	if !ok {
		return s
	}
	merged := map[string]*jsObject{}
	for k, v := range s.overrides {
		merged[k] = v
	}
	for name, ov := range selected {
		m := newObject()
		if base := merged[name]; base != nil {
			for _, k := range base.keys {
				m.set(k, base.vals[k])
			}
		}
		for _, k := range ov.keys {
			m.set(k, ov.vals[k])
		}
		merged[name] = m
	}
	s.overrides = merged
	return s
}

func resolveSubagentDefaultProvider(user, project subagentSettings, projectPath string) string {
	if projectPath != "" && project.defaultProvider != "" {
		return project.defaultProvider
	}
	return user.defaultProvider
}

func resolveSubagentDefaultModel(user, project subagentSettings, userPath, projectPath, defaultProvider string) *ModelSource {
	if projectPath != "" && project.defaultModel != "" {
		return &ModelSource{Type: "subagents.defaultModel", Scope: "project", Path: projectPath, Model: project.defaultModel, DefaultProvider: defaultProvider}
	}
	if user.defaultModel != "" {
		return &ModelSource{Type: "subagents.defaultModel", Scope: "user", Path: userPath, Model: user.defaultModel, DefaultProvider: defaultProvider}
	}
	return nil
}

func applySubagentDefaultModel(agents []AgentConfig, dm *ModelSource, dp string) []AgentConfig {
	if dm == nil && dp == "" {
		return agents
	}
	out := make([]AgentConfig, len(agents))
	for i, a := range agents {
		if a.Model != "" && (a.ModelProvider != "" || dp == "") {
			out[i] = a
			continue
		}
		if a.Model == "" && dm != nil {
			a.Model = dm.Model
			src := *dm
			a.ModelSource = &src
		}
		if dp != "" {
			a.ModelProvider = dp
		}
		out[i] = a
	}
	return out
}

func resolveSubagentDefaultThinking(user, project subagentSettings, projectPath string) string {
	if projectPath != "" && project.defaultThinking != "" {
		return project.defaultThinking
	}
	return user.defaultThinking
}

func applySubagentDefaultThinking(agents []AgentConfig, dt string) []AgentConfig {
	if dt == "" {
		return agents
	}
	out := make([]AgentConfig, len(agents))
	for i, a := range agents {
		if a.Thinking == nil {
			a.Thinking = dt
		}
		out[i] = a
	}
	return out
}

func resolveSubagentMaxThinking(user, project subagentSettings, projectPath string) string {
	if projectPath != "" && project.maxThinking != "" {
		return project.maxThinking
	}
	return user.maxThinking
}

func applySubagentMaxThinking(agents []AgentConfig, mt string) []AgentConfig {
	if mt == "" {
		return agents
	}
	out := make([]AgentConfig, len(agents))
	for i, a := range agents {
		a.MaxThinking = mt
		out[i] = a
	}
	return out
}

func resolveDefaultList(user, project []string, projectSet bool, projectPath string) []string {
	if projectPath != "" && projectSet {
		return project
	}
	return user
}

func applySubagentDefaultExtensions(agents []AgentConfig, de []string) []AgentConfig {
	if de == nil {
		return agents
	}
	out := make([]AgentConfig, len(agents))
	for i, a := range agents {
		if a.Extensions == nil {
			a.Extensions = append([]string{}, de...)
			a.ExtensionsFromDefault = true
		}
		out[i] = a
	}
	return out
}

func applySubagentDefaultSubagentOnlyExtensions(agents []AgentConfig, de []string) []AgentConfig {
	if de == nil {
		return agents
	}
	out := make([]AgentConfig, len(agents))
	for i, a := range agents {
		if a.SubagentOnlyExtensions == nil {
			a.SubagentOnlyExtensions = append([]string{}, de...)
		}
		out[i] = a
	}
	return out
}

type subagentDefaults struct {
	model                  *ModelSource
	provider               string
	thinking               string
	extensions             []string
	subagentOnlyExtensions []string
}

func (d subagentDefaults) apply(agents []AgentConfig) []AgentConfig {
	return applySubagentDefaultSubagentOnlyExtensions(
		applySubagentDefaultExtensions(applySubagentDefaultThinking(applySubagentDefaultModel(agents, d.model, d.provider), d.thinking), d.extensions),
		d.subagentOnlyExtensions)
}

// splitToolList is agents.ts:735: rawSet false is an unset list.
func splitToolList(raw []string, rawSet bool) (tools, mcp []string) {
	if rawSet {
		tools = []string{}
	}
	for _, t := range raw {
		if strings.HasPrefix(t, "mcp:") {
			mcp = append(mcp, t[4:])
		} else {
			tools = append(tools, t)
		}
	}
	return
}

func joinToolList(tools, mcp []string) []string {
	var joined []string
	joined = append(joined, tools...)
	for _, t := range mcp {
		joined = append(joined, "mcp:"+t)
	}
	if len(joined) == 0 {
		return nil
	}
	return joined
}

func arraysEqual(a, b []string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string{}, s...)
}

func cloneBoolPtr(b *bool) *bool {
	if b == nil {
		return nil
	}
	return new(*b)
}

// cloneOverrideBase is agents.ts:769.
func cloneOverrideBase(a AgentConfig) OverrideBase {
	b := OverrideBase{
		Description: a.Description, Machine: a.Machine, Output: a.Output, OutputMode: a.OutputMode,
		DefaultReads: cloneStrings(a.DefaultReads), Model: a.Model, ModelProvider: a.ModelProvider, Fast: cloneBoolPtr(a.Fast),
		Thinking: a.Thinking, SystemPromptMode: a.SystemPromptMode, InheritProjectContext: a.InheritProjectContext,
		InheritGlobalContext: a.InheritGlobalContext, InheritSkills: a.InheritSkills, DefaultContext: a.DefaultContext,
		AcceptanceRole: a.AcceptanceRole, Disabled: cloneBoolPtr(a.Disabled), SystemPrompt: a.SystemPrompt,
		Skills: cloneStrings(a.Skills), SkillPath: cloneStrings(a.SkillPath), Tools: cloneStrings(a.Tools),
		ExcludeTools: cloneStrings(a.ExcludeTools), AllowNestedSubagents: cloneBoolPtr(a.AllowNestedSubagents),
		AllowedAgents: cloneStrings(a.AllowedAgents), McpDirectTools: cloneStrings(a.McpDirectTools),
		SubagentOnlyExtensions: cloneStrings(a.SubagentOnlyExtensions), MutationTools: cloneStrings(a.MutationTools), ToolBudget: a.ToolBudget,
	}
	if !a.ExtensionsFromDefault {
		b.Extensions = cloneStrings(a.Extensions)
	}
	return b
}

// applyToolsOverride is agents.ts:1429.
func applyToolsOverride(t *AgentConfig, v any) {
	if s, ok := v.(string); ok && s == "inherit" {
		t.Tools, t.McpDirectTools = nil, nil
		return
	}
	var list []string
	if _, isArr := v.([]any); isArr {
		list = toStrings(v)
	} else {
		list = []string{}
	}
	t.Tools, t.McpDirectTools = splitToolList(list, true)
}

func setBoolPtr(v any) *bool {
	b, _ := v.(bool)
	return new(b)
}

// applyBuiltinOverride is agents.ts:1440. scope is "user" or "project".
func applyBuiltinOverride(agent AgentConfig, ov *jsObject, scope, path string) AgentConfig {
	info := &OverrideInfo{Scope: scope, Path: path, FieldScopes: map[string][]string{}}
	fields := map[string]bool{}
	if agent.Override != nil {
		info.Base = agent.Override.Base
		for _, f := range agent.Override.Fields {
			fields[f] = true
		}
		for f, sc := range agent.Override.FieldScopes {
			info.FieldScopes[f] = append([]string{}, sc...)
		}
	} else {
		info.Base = cloneOverrideBase(agent)
	}
	for _, f := range ov.keys {
		fields[f] = true
	}
	for f := range fields {
		info.Fields = append(info.Fields, f)
	}
	sort.Strings(info.Fields)
	for _, f := range ov.keys {
		set := map[string]bool{scope: true}
		for _, s := range info.FieldScopes[f] {
			set[s] = true
		}
		var scopes []string
		for s := range set {
			scopes = append(scopes, s)
		}
		sort.Strings(scopes)
		info.FieldScopes[f] = scopes
	}
	next := agent
	next.Override = info
	has := func(k string) bool { _, ok := ov.get(k); return ok }
	if has("description") {
		next.Description, _ = ov.str("description")
	}
	if s, f, ok := ovString(ov, "machine"); ok {
		next.Machine = map[bool]string{true: "", false: s}[f]
	}
	if s, f, ok := ovString(ov, "output"); ok {
		next.Output = map[bool]string{true: "", false: s}[f]
	}
	if has("outputMode") {
		next.OutputMode, _ = ov.str("outputMode")
	}
	if l, f, ok := ovList(ov, "defaultReads"); ok {
		if f {
			next.DefaultReads = nil
		} else {
			next.DefaultReads = l
		}
	}
	if s, f, ok := ovString(ov, "model"); ok {
		if f {
			next.Model = ""
		} else {
			next.Model = s
		}
		next.ModelSource = nil
	}
	if s, f, ok := ovString(ov, "defaultProvider"); ok {
		if f {
			next.ModelProvider = ""
		} else {
			next.ModelProvider = s
		}
	}
	if has("fast") {
		v, _ := ov.get("fast")
		next.Fast = setBoolPtr(v)
	}
	if v, ok := ov.get("thinking"); ok {
		if b, isB := v.(bool); isB && !b {
			next.Thinking = nil
		} else {
			next.Thinking = v
		}
	}
	if has("systemPromptMode") {
		next.SystemPromptMode, _ = ov.str("systemPromptMode")
	}
	if v, ok := ov.flag("inheritProjectContext"); ok {
		next.InheritProjectContext = v
	}
	if v, ok := ov.flag("inheritGlobalContext"); ok {
		next.InheritGlobalContext = v
	}
	if v, ok := ov.flag("inheritSkills"); ok {
		next.InheritSkills = v
	}
	if s, f, ok := ovString(ov, "defaultContext"); ok {
		next.DefaultContext = map[bool]string{true: "", false: s}[f]
	}
	if s, f, ok := ovString(ov, "acceptanceRole"); ok {
		next.AcceptanceRole = map[bool]string{true: "", false: s}[f]
	}
	if has("disabled") {
		v, _ := ov.get("disabled")
		next.Disabled = setBoolPtr(v)
	}
	if has("systemPrompt") {
		next.SystemPrompt, _ = ov.str("systemPrompt")
	}
	if l, f, ok := ovList(ov, "skills"); ok {
		if f {
			next.Skills = nil
		} else {
			next.Skills = l
		}
	}
	if v, ok := ov.get("tools"); ok {
		applyToolsOverride(&next, v)
	}
	if l, f, ok := ovList(ov, "excludeTools"); ok {
		if f {
			next.ExcludeTools = nil
		} else {
			next.ExcludeTools = l
		}
	}
	if has("allowNestedSubagents") {
		v, _ := ov.get("allowNestedSubagents")
		next.AllowNestedSubagents = setBoolPtr(v)
	}
	if l, f, ok := ovList(ov, "allowedAgents"); ok {
		if f {
			next.AllowedAgents = nil
		} else {
			next.AllowedAgents = l
		}
	}
	if l, f, ok := ovList(ov, "extensions"); ok {
		if f {
			next.Extensions = nil
		} else {
			next.Extensions = l
		}
	}
	if l, f, ok := ovList(ov, "subagentOnlyExtensions"); ok {
		if f {
			next.SubagentOnlyExtensions = nil
		} else {
			next.SubagentOnlyExtensions = l
		}
	}
	if l, f, ok := ovList(ov, "mutationTools"); ok {
		if f {
			next.MutationTools = nil
		} else {
			next.MutationTools = l
		}
	}
	if v, ok := ov.get("toolBudget"); ok {
		if b, isB := v.(bool); isB && !b {
			next.ToolBudget = nil
		} else {
			next.ToolBudget = v
		}
	}
	return next
}

// applyBuiltinOverrides is agents.ts:1502.
func applyBuiltinOverrides(builtin []AgentConfig, user, project subagentSettings, userPath, projectPath string) []AgentConfig {
	projectBulkDisabled := project.disableBuiltins != nil && *project.disableBuiltins && projectPath != ""
	userBulkDisabled := project.disableBuiltins == nil && user.disableBuiltins != nil && *user.disableBuiltins
	projectThinkingConfigured := project.disableThinking != nil && projectPath != ""
	var disableThinking bool
	var dtScope, dtPath string
	if projectThinkingConfigured {
		disableThinking = *project.disableThinking
		dtScope, dtPath = "project", projectPath
	} else {
		disableThinking = user.disableThinking != nil && *user.disableThinking
		dtScope, dtPath = "user", userPath
	}
	applyGlobalThinking := func(a AgentConfig, explicit bool) AgentConfig {
		if !disableThinking || explicit {
			return a
		}
		// clearBuiltinThinking keeps the agent's own base: build the override info from the agent before the clear
		if a.Thinking == nil {
			return a
		}
		base := a.Override
		cleared := a
		cleared.Thinking = nil
		if base == nil {
			cleared.Override = &OverrideInfo{Scope: dtScope, Path: dtPath, Base: cloneOverrideBase(a)}
		}
		return cleared
	}
	hasThinking := func(o *jsObject) bool { _, ok := o.get("thinking"); return ok }
	out := make([]AgentConfig, len(builtin))
	for i, a := range builtin {
		po := project.overrides[a.Name]
		uo := user.overrides[a.Name]
		switch {
		case po != nil && projectPath != "":
			out[i] = applyGlobalThinking(applyBuiltinOverride(a, po, "project", projectPath), hasThinking(po))
		case projectBulkDisabled:
			d := newObject()
			d.set("disabled", true)
			out[i] = applyGlobalThinking(applyBuiltinOverride(a, d, "project", projectPath), false)
		case uo != nil:
			out[i] = applyGlobalThinking(applyBuiltinOverride(a, uo, "user", userPath), !projectThinkingConfigured && hasThinking(uo))
		case userBulkDisabled:
			d := newObject()
			d.set("disabled", true)
			out[i] = applyGlobalThinking(applyBuiltinOverride(a, d, "user", userPath), false)
		default:
			out[i] = applyGlobalThinking(a, false)
		}
	}
	return out
}

// applyCustomAgentOverrides is agents.ts:1568: user then project, so project fields win without dropping user-only fields.
func applyCustomAgentOverrides(agents []AgentConfig, user, project subagentSettings, userPath, projectPath string) []AgentConfig {
	out := make([]AgentConfig, len(agents))
	for i, a := range agents {
		if uo := user.overrides[a.Name]; uo != nil {
			a = applyBuiltinOverride(a, uo, "user", userPath)
		}
		if po := project.overrides[a.Name]; po != nil && projectPath != "" {
			a = applyBuiltinOverride(a, po, "project", projectPath)
		}
		out[i] = a
	}
	return out
}

// RuntimeAgentSettingsContext is agents.ts RuntimeAgentSettingsContext.
type RuntimeAgentSettingsContext struct {
	Cwd                    string
	Scope                  AgentScope
	PreferredModelProvider string
}

func runtimeAgentOverrides(s subagentSettings) subagentSettings {
	out := s
	out.overrides = map[string]*jsObject{}
	for name, ov := range s.overrides {
		n := newObject()
		for _, k := range []string{"model", "defaultProvider", "fast", "thinking"} {
			if v, ok := ov.get(k); ok {
				n.set(k, v)
			}
		}
		if len(n.keys) > 0 {
			out.overrides[name] = n
		}
	}
	return out
}

func thinkingEq(a, b any) bool { return reflect.DeepEqual(a, b) }

func boolPtrEq(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// buildBuiltinOverrideConfig is agents.ts:1630: the override that turns base into draft, nil when they are equal.
func buildBuiltinOverrideConfig(base OverrideBase, d AgentConfig) *jsObject {
	o := newObject()
	if d.Machine != base.Machine {
		if d.Machine == "" {
			o.set("machine", false)
		} else {
			o.set("machine", d.Machine)
		}
	}
	if desc := jsTrim(d.Description); desc != "" && desc != base.Description {
		o.set("description", desc)
	}
	if d.Output != base.Output {
		if d.Output == "" {
			o.set("output", false)
		} else {
			o.set("output", d.Output)
		}
	}
	if d.OutputMode != "" && d.OutputMode != base.OutputMode {
		o.set("outputMode", d.OutputMode)
	}
	if !arraysEqual(d.DefaultReads, base.DefaultReads) {
		if d.DefaultReads != nil {
			o.set("defaultReads", anyStrings(d.DefaultReads))
		} else {
			o.set("defaultReads", false)
		}
	}
	if d.Model != base.Model {
		if d.Model == "" {
			o.set("model", false)
		} else {
			o.set("model", d.Model)
		}
	}
	if d.ModelProvider != base.ModelProvider {
		if d.ModelProvider == "" {
			o.set("defaultProvider", false)
		} else {
			o.set("defaultProvider", d.ModelProvider)
		}
	}
	if !boolPtrEq(d.Fast, base.Fast) {
		o.set("fast", d.Fast != nil && *d.Fast)
	}
	if !thinkingEq(d.Thinking, base.Thinking) {
		if d.Thinking == nil {
			o.set("thinking", false)
		} else {
			o.set("thinking", d.Thinking)
		}
	}
	if d.SystemPromptMode != base.SystemPromptMode {
		o.set("systemPromptMode", d.SystemPromptMode)
	}
	if d.InheritProjectContext != base.InheritProjectContext {
		o.set("inheritProjectContext", d.InheritProjectContext)
	}
	if d.InheritGlobalContext != base.InheritGlobalContext {
		o.set("inheritGlobalContext", d.InheritGlobalContext)
	}
	if d.InheritSkills != base.InheritSkills {
		o.set("inheritSkills", d.InheritSkills)
	}
	if d.DefaultContext != base.DefaultContext {
		if d.DefaultContext == "" {
			o.set("defaultContext", false)
		} else {
			o.set("defaultContext", d.DefaultContext)
		}
	}
	if d.AcceptanceRole != base.AcceptanceRole {
		if d.AcceptanceRole == "" {
			o.set("acceptanceRole", false)
		} else {
			o.set("acceptanceRole", d.AcceptanceRole)
		}
	}
	if !boolPtrEq(d.Disabled, base.Disabled) {
		if d.Disabled == nil {
			o.set("disabled", false)
		} else {
			o.set("disabled", *d.Disabled)
		}
	}
	if d.SystemPrompt != base.SystemPrompt {
		o.set("systemPrompt", d.SystemPrompt)
	}
	if !arraysEqual(d.Skills, base.Skills) {
		if d.Skills != nil {
			o.set("skills", anyStrings(d.Skills))
		} else {
			o.set("skills", false)
		}
	}
	baseTools, draftTools := joinToolList(base.Tools, base.McpDirectTools), joinToolList(d.Tools, d.McpDirectTools)
	if !arraysEqual(draftTools, baseTools) {
		if draftTools != nil {
			o.set("tools", anyStrings(draftTools))
		} else {
			o.set("tools", false)
		}
	}
	if !arraysEqual(d.ExcludeTools, base.ExcludeTools) {
		if d.ExcludeTools != nil {
			o.set("excludeTools", anyStrings(d.ExcludeTools))
		} else {
			o.set("excludeTools", false)
		}
	}
	if !boolPtrEq(d.AllowNestedSubagents, base.AllowNestedSubagents) {
		o.set("allowNestedSubagents", d.AllowNestedSubagents != nil && *d.AllowNestedSubagents)
	}
	if !arraysEqual(d.Extensions, base.Extensions) {
		if d.Extensions != nil {
			o.set("extensions", anyStrings(d.Extensions))
		} else {
			o.set("extensions", false)
		}
	}
	if !arraysEqual(d.SubagentOnlyExtensions, base.SubagentOnlyExtensions) {
		if d.SubagentOnlyExtensions != nil {
			o.set("subagentOnlyExtensions", anyStrings(d.SubagentOnlyExtensions))
		} else {
			o.set("subagentOnlyExtensions", false)
		}
	}
	if !arraysEqual(d.MutationTools, base.MutationTools) {
		if d.MutationTools != nil {
			o.set("mutationTools", anyStrings(d.MutationTools))
		} else {
			o.set("mutationTools", false)
		}
	}
	if marshalJSON(d.ToolBudget, "") != marshalJSON(base.ToolBudget, "") {
		if d.ToolBudget != nil {
			o.set("toolBudget", d.ToolBudget)
		} else {
			o.set("toolBudget", false)
		}
	}
	if len(o.keys) == 0 {
		return nil
	}
	return o
}

// cloneOverrideValue is agents.ts:802: the override with its keys in the order the settings file keeps them.
func cloneOverrideValue(ov *jsObject) *jsObject {
	order := []string{"description", "machine", "output", "outputMode", "defaultReads", "model", "defaultProvider", "fast", "thinking",
		"systemPromptMode", "inheritProjectContext", "inheritGlobalContext", "inheritSkills", "defaultContext", "acceptanceRole", "disabled",
		"systemPrompt", "skills", "tools", "excludeTools", "allowNestedSubagents", "allowedAgents", "extensions", "subagentOnlyExtensions",
		"mutationTools", "toolBudget"}
	out := newObject()
	for _, k := range order {
		v, ok := ov.get(k)
		if !ok {
			continue
		}
		if arr, isArr := v.([]any); isArr {
			v = append([]any{}, arr...)
		}
		if o, isO := v.(*jsObject); isO {
			c := o.clone()
			if blk, isArr := o.vals["block"].([]any); isArr {
				c.set("block", append([]any{}, blk...))
			}
			v = c
		}
		out.set(k, v)
	}
	return out
}

func userAgentSettingsPath() string { return agentDirJoin("settings.json") }

func agentDirJoin(parts ...string) string {
	return filepath.Join(append([]string{agentDir()}, parts...)...)
}

func projectAgentSettingsPath(cwd string) string {
	if root := FindConfiguredProjectRoot(cwd); root != "" {
		return filepath.Join(getProjectConfigDir(root), "settings.json")
	}
	return ""
}

func settingsPathFor(cwd, scope string) (string, error) {
	var p string
	if scope == "project" {
		p = projectAgentSettingsPath(cwd)
	} else {
		p = userAgentSettingsPath()
	}
	if p == "" {
		return "", fmt.Errorf("Project override is not available here. No project config root was found.")
	}
	return p, nil
}

func objectOrNew(o *jsObject, k string) *jsObject {
	if x := o.obj(k); x != nil {
		return x.clone()
	}
	return newObject()
}

// saveBuiltinAgentOverride is agents.ts:1673.
func saveBuiltinAgentOverride(cwd, name, scope string, override *jsObject) (string, error) {
	filePath, err := settingsPathFor(cwd, scope)
	if err != nil {
		return "", err
	}
	settings, err := readSettingsFileStrict(filePath)
	if err != nil {
		return "", err
	}
	sub := objectOrNew(settings, "subagents")
	ao := objectOrNew(sub, "agentOverrides")
	ao.set(name, cloneOverrideValue(override))
	sub.set("agentOverrides", ao)
	settings.set("subagents", sub)
	return filePath, writeSettingsFile(filePath, settings)
}

func deleteKey(o *jsObject, k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// removeBuiltinAgentOverride is agents.ts:1697.
func removeBuiltinAgentOverride(cwd, name, scope string, preserveMachine bool) (path string, removed, machinePreserved bool, err error) {
	filePath, err := settingsPathFor(cwd, scope)
	if err != nil {
		return "", false, false, err
	}
	if _, serr := os.Stat(filePath); serr != nil {
		return filePath, false, false, nil
	}
	settings, err := readSettingsFileStrict(filePath)
	if err != nil {
		return "", false, false, err
	}
	sub := settings.obj("subagents")
	if sub == nil {
		return filePath, false, false, nil
	}
	next := sub.clone()
	ao := next.obj("agentOverrides")
	if ao == nil {
		return filePath, false, false, nil
	}
	nextAO := ao.clone()
	cur, exists := nextAO.vals[name]
	if !exists {
		return filePath, false, false, nil
	}
	var machine any
	if c, ok := cur.(*jsObject); ok {
		cm, _ := c.get("machine")
		if b, isB := cm.(bool); isB && !b {
			machine = false
		} else if s, isS := cm.(string); isS && jsTrim(s) != "" {
			machine = s
		}
	}
	if preserveMachine && machine != nil {
		m := newObject()
		m.set("machine", machine)
		nextAO.set(name, m)
	} else {
		deleteKey(nextAO, name)
	}
	if len(nextAO.keys) > 0 {
		next.set("agentOverrides", nextAO)
	} else {
		deleteKey(next, "agentOverrides")
	}
	if len(next.keys) > 0 {
		settings.set("subagents", next)
	} else {
		deleteKey(settings, "subagents")
	}
	if err := writeSettingsFile(filePath, settings); err != nil {
		return "", false, false, err
	}
	return filePath, true, preserveMachine && machine != nil, nil
}

// mergeBuiltinAgentOverride is agents.ts:1726.
func mergeBuiltinAgentOverride(cwd, name, scope string, fields *jsObject) (string, error) {
	filePath, err := settingsPathFor(cwd, scope)
	if err != nil {
		return "", err
	}
	settings, err := readSettingsFileStrict(filePath)
	if err != nil {
		return "", err
	}
	sub := objectOrNew(settings, "subagents")
	ao := objectOrNew(sub, "agentOverrides")
	merged := newObject()
	if existing := ao.obj(name); existing != nil {
		for _, k := range existing.keys {
			merged.set(k, existing.vals[k])
		}
	}
	cv := cloneOverrideValue(fields)
	for _, k := range cv.keys {
		merged.set(k, cv.vals[k])
	}
	ao.set(name, merged)
	sub.set("agentOverrides", ao)
	settings.set("subagents", sub)
	return filePath, writeSettingsFile(filePath, settings)
}

// removeBuiltinAgentOverrideFields is agents.ts:1754.
func removeBuiltinAgentOverrideFields(cwd, name, scope string, fields []string) (path string, removed bool, err error) {
	filePath, err := settingsPathFor(cwd, scope)
	if err != nil {
		return "", false, err
	}
	if _, serr := os.Stat(filePath); serr != nil {
		return filePath, false, nil
	}
	settings, err := readSettingsFileStrict(filePath)
	if err != nil {
		return "", false, err
	}
	sub := settings.obj("subagents")
	if sub == nil {
		return filePath, false, nil
	}
	ao := sub.obj("agentOverrides")
	if ao == nil {
		return filePath, false, nil
	}
	entry := ao.obj(name)
	if entry == nil {
		return filePath, false, nil
	}
	nextEntry := entry.clone()
	for _, f := range fields {
		if _, ok := nextEntry.vals[f]; ok {
			deleteKey(nextEntry, f)
			removed = true
		}
	}
	if !removed {
		return filePath, false, nil
	}
	nextSub := sub.clone()
	nextAO := ao.clone()
	if len(nextEntry.keys) > 0 {
		nextAO.set(name, nextEntry)
		nextSub.set("agentOverrides", nextAO)
	} else {
		deleteKey(nextAO, name)
		if len(nextAO.keys) > 0 {
			nextSub.set("agentOverrides", nextAO)
		} else {
			deleteKey(nextSub, "agentOverrides")
		}
	}
	if len(nextSub.keys) > 0 {
		settings.set("subagents", nextSub)
	} else {
		deleteKey(settings, "subagents")
	}
	return filePath, true, writeSettingsFile(filePath, settings)
}
