package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func init() {
	registerAction("create", []string{"config"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(createAction(ctx.Cwd(), params, registryModels(ctx)))
	})
	registerAction("update", []string{"agent", "agentScope", "config"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(updateAction(ctx.Cwd(), params, registryModels(ctx)))
	})
	registerAction("delete", []string{"agent", "agentScope"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(deleteAction(ctx.Cwd(), params))
	})
	registerAction("eject", []string{"agent", "agentScope"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(ejectAction(ctx.Cwd(), params))
	})
}

// The management actions the original runs besides list and get
// (src/agents/agent-management.ts): create, update, delete and eject write the
// agent definition files through the original's serializer, with the original's
// texts. Model-registry and skills warnings need the original's registries and
// are not part of this slice.

// configObject is the original's configObject: the call's `config`, as an
// object or a JSON string. ok is false when it is absent or not an object.
func configObject(v any) (cfg map[string]any, errText string, ok bool) {
	if s, isString := v.(string); isString {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, "config must be valid JSON: " + err.Error(), false
		}
		v = parsed
	}
	m, isMap := v.(map[string]any)
	if !isMap {
		return nil, "", false
	}
	return m, "", true
}

func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func parseCsv(value string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(value, ",") {
		if p := jsTrim(part); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// parseTools splits a csv into tools and mcp: direct tools (the original's parseTools).
func parseTools(raw string) (tools, mcp []string) {
	for _, item := range parseCsv(raw) {
		if strings.HasPrefix(item, "mcp:") {
			if direct := jsTrim(item[4:]); direct != "" {
				mcp = append(mcp, direct)
			}
		} else {
			tools = append(tools, item)
		}
	}
	return
}

func stringList(v any) ([]string, bool) {
	arr, isArr := v.([]any)
	if !isArr {
		return nil, false
	}
	var out []string
	for _, e := range arr {
		s, isString := e.(string)
		if !isString {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func stringListTrimmed(v any) ([]string, bool) {
	list, ok := stringList(v)
	if !ok {
		return nil, false
	}
	var out []string
	for _, s := range list {
		if t := jsTrim(s); t != "" {
			out = append(out, t)
		}
	}
	return out, true
}

// applyAgentConfig is the original's applyAgentConfig for the fields this port
// models; it returns the message of the first problem.
func applyAgentConfig(target *AgentConfig, cfg map[string]any) string {
	strOrDelete := func(key string, dst *string) string {
		v, present := cfg[key]
		if !present {
			return ""
		}
		if s, isString := v.(string); isString {
			*dst = s
			return ""
		}
		return fmt.Sprintf("config.%s must be a string or false when provided.", key)
	}
	if v, present := cfg["advertise"]; present {
		switch t := v.(type) {
		case string:
			if t == "" {
				target.Advertise = nil
			} else {
				return "config.advertise must be a boolean or empty string when provided."
			}
		case bool:
			b := t
			target.Advertise = &b
		default:
			return "config.advertise must be a boolean or empty string when provided."
		}
	}
	if v, present := cfg["aliases"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.Aliases = nil
			} else {
				return "config.aliases must be a comma-separated string, string array, or false when provided."
			}
		case string:
			if t == "" {
				target.Aliases = nil
			} else {
				var kept []string
				for _, alias := range parseCsv(t) {
					if alias != target.Name {
						kept = append(kept, alias)
					}
				}
				target.Aliases = normalizeAliases(kept, target.Name)
			}
		default:
			list, ok := stringListTrimmed(v)
			if !ok {
				return "config.aliases must be a comma-separated string, string array, or false when provided."
			}
			target.Aliases = normalizeAliases(list, target.Name)
		}
	}
	if v, present := cfg["systemPrompt"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.SystemPrompt = ""
			} else {
				return "config.systemPrompt must be a string or false when provided."
			}
		case string:
			target.SystemPrompt = t
		default:
			return "config.systemPrompt must be a string or false when provided."
		}
	}
	if v, present := cfg["runner"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.Runner = nil
			} else {
				return runnerConfigError()
			}
		case string:
			if t == "" {
				target.Runner = nil
			} else {
				return runnerConfigError()
			}
		case map[string]any:
			switch t["type"] {
			case "pi":
				if len(t) == 1 {
					target.Runner = &Runner{Type: "pi"}
				} else {
					return runnerConfigError()
				}
			case "external-cli":
				command, isString := t["command"].(string)
				_, hasArgs := t["args"]
				_, hasAdapter := t["adapter"]
				_, hasPrompt := t["promptDelivery"]
				allowed := map[string]bool{"type": true, "command": true, "args": hasArgs, "adapter": hasAdapter, "promptDelivery": hasPrompt}
				known := true
				for k := range t {
					if !allowed[k] {
						known = false
					}
				}
				if isString && jsTrim(command) != "" && known && (!hasArgs || argListOK(t["args"])) && (!hasPrompt || t["promptDelivery"] == "stdin") {
					target.Runner = &Runner{Type: "external-cli", Command: jsTrim(command)}
				} else {
					return runnerConfigError()
				}
			case "external-job":
				provider, isString := t["provider"].(string)
				options, hasOptions := t["options"]
				_, optionsAreObject := options.(map[string]any)
				known := true
				for k := range t {
					if k != "type" && k != "provider" && k != "options" {
						known = false
					}
				}
				if isString && provider != "" && provider == jsTrim(provider) && known && (!hasOptions || optionsAreObject) {
					target.Runner = &Runner{Type: "external-job", Command: provider}
				} else {
					return runnerConfigError()
				}
			default:
				return runnerConfigError()
			}
		default:
			return "config.runner must be an object, false, or empty string when provided."
		}
	}
	if v, present := cfg["model"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.Model = ""
			} else {
				return "config.model must be a string or false when provided."
			}
		case string:
			if m := jsTrim(t); m != "" {
				target.Model = m
			} else {
				target.Model = ""
			}
		default:
			return "config.model must be a string or false when provided."
		}
	}
	if hasKey(cfg, "fallbackModels") {
		return "config.fallbackModels was removed; configure one model instead."
	}
	if v, present := cfg["tools"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.Tools, target.McpDirectTools = nil, nil
			} else {
				return "config.tools must be a comma-separated string or false when provided."
			}
		case string:
			if t == "" {
				target.Tools, target.McpDirectTools = nil, nil
			} else {
				target.Tools, target.McpDirectTools = parseTools(t)
			}
		default:
			return "config.tools must be a comma-separated string or false when provided."
		}
	}
	if v, present := cfg["excludeTools"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.ExcludeTools = nil
			} else {
				return "config.excludeTools must be a comma-separated string, string array, or false when provided."
			}
		case string:
			if list := parseCsv(t); len(list) > 0 {
				target.ExcludeTools = list
			} else {
				target.ExcludeTools = nil
			}
		default:
			list, ok := stringListTrimmed(v)
			if !ok {
				return "config.excludeTools must be a comma-separated string, string array, or false when provided."
			}
			target.ExcludeTools = list
		}
	}
	if v, present := cfg["skills"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.Skills = nil
			} else {
				return "config.skills must be a comma-separated string or false when provided."
			}
		case string:
			if list := parseCsv(t); len(list) > 0 {
				target.Skills = list
			} else {
				target.Skills = nil
			}
		default:
			return "config.skills must be a comma-separated string or false when provided."
		}
	}
	if v, present := cfg["skillPath"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.SkillPath = nil
			} else {
				return "config.skillPath must be a comma-separated string, string array, or false when provided."
			}
		case string:
			if list := parseCsv(t); len(list) > 0 {
				target.SkillPath = list
			} else {
				target.SkillPath = nil
			}
		default:
			list, ok := stringListTrimmed(v)
			if !ok {
				return "config.skillPath must be a comma-separated string, string array, or false when provided."
			}
			target.SkillPath = list
		}
	}
	for _, key := range []string{"extensions", "subagentOnlyExtensions", "mutationTools"} {
		v, present := cfg[key]
		if !present {
			continue
		}
		dst := map[string]*[]string{"extensions": &target.Extensions, "subagentOnlyExtensions": &target.SubagentOnlyExtensions, "mutationTools": &target.MutationTools}[key]
		switch t := v.(type) {
		case bool:
			if !t {
				*dst = nil
			} else {
				return fmt.Sprintf("config.%s must be a comma-separated string, empty string, or false when provided.", key)
			}
		case string:
			if t == "" {
				*dst = []string{}
			} else {
				*dst = parseCsv(t)
			}
		default:
			return fmt.Sprintf("config.%s must be a comma-separated string, empty string, or false when provided.", key)
		}
	}
	if v, present := cfg["thinking"]; present {
		if s, isString := v.(string); isString {
			if t := jsTrim(s); t != "" {
				target.Thinking = t
			} else {
				target.Thinking = nil
			}
		} else if b, isBool := v.(bool); isBool {
			if !b {
				target.Thinking = nil
			} else {
				return "config.thinking must be a string or false when provided."
			}
		} else {
			return "config.thinking must be a string or false when provided."
		}
	}
	if v, present := cfg["systemPromptMode"]; present {
		if s, isString := v.(string); isString && (s == "append" || s == "replace") {
			target.SystemPromptMode = s
		} else {
			return "config.systemPromptMode must be 'append' or 'replace' when provided."
		}
	}
	for _, key := range []string{"inheritProjectContext", "inheritGlobalContext", "inheritSkills"} {
		if v, present := cfg[key]; present {
			b, isBool := v.(bool)
			if !isBool {
				return fmt.Sprintf("config.%s must be a boolean when provided.", key)
			}
			switch key {
			case "inheritProjectContext":
				target.InheritProjectContext = b
			case "inheritGlobalContext":
				target.InheritGlobalContext = b
			case "inheritSkills":
				target.InheritSkills = b
			}
		}
	}
	if v, present := cfg["defaultContext"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.DefaultContext = ""
			} else {
				return "config.defaultContext must be 'fresh', 'fork', or false when provided."
			}
		case string:
			if t == "" {
				target.DefaultContext = ""
			} else if t == "fresh" || t == "fork" {
				target.DefaultContext = t
			} else {
				return "config.defaultContext must be 'fresh', 'fork', or false when provided."
			}
		default:
			return "config.defaultContext must be 'fresh', 'fork', or false when provided."
		}
	}
	if v, present := cfg["async"]; present {
		switch t := v.(type) {
		case string:
			if t == "" {
				target.DefaultAsync = nil
			} else {
				return "config.async must be a boolean or empty string when provided."
			}
		case bool:
			target.DefaultAsync = &t
		default:
			return "config.async must be a boolean or empty string when provided."
		}
	}
	if v, present := cfg["timeoutMs"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.DefaultTimeoutMs = nil
			} else {
				return "config.timeoutMs must be a positive integer or false when provided."
			}
		case string:
			if t == "" {
				target.DefaultTimeoutMs = nil
			} else {
				return "config.timeoutMs must be a positive integer or false when provided."
			}
		case float64:
			if t == float64(int64(t)) && t > 0 {
				n := int64(t)
				target.DefaultTimeoutMs = &n
			} else {
				return "config.timeoutMs must be a positive integer or false when provided."
			}
		default:
			return "config.timeoutMs must be a positive integer or false when provided."
		}
	}
	if v, present := cfg["acceptance"]; present {
		if s, isString := v.(string); isString && s == "" {
			target.DefaultAcceptance = ""
		} else {
			target.DefaultAcceptance = marshalJSON(v, "")
		}
	}
	if v, present := cfg["acceptanceRole"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.AcceptanceRole = ""
			} else {
				return "config.acceptanceRole must be 'read-only', 'writer', or false when provided."
			}
		case string:
			if t == "" {
				target.AcceptanceRole = ""
			} else if t == "read-only" || t == "writer" {
				target.AcceptanceRole = t
			} else {
				return "config.acceptanceRole must be 'read-only', 'writer', or false when provided."
			}
		default:
			return "config.acceptanceRole must be 'read-only', 'writer', or false when provided."
		}
	}
	if msg := strOrDelete("output", &target.Output); msg != "" {
		return msg
	}
	if v, present := cfg["outputMode"]; present {
		switch t := v.(type) {
		case string:
			if t == "" {
				target.OutputMode = ""
			} else if t == "inline" || t == "file-only" {
				target.OutputMode = t
			} else {
				return "config.outputMode must be 'inline' or 'file-only' when provided."
			}
		default:
			return "config.outputMode must be 'inline' or 'file-only' when provided."
		}
	}
	if v, present := cfg["reads"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.DefaultReads = nil
			} else {
				return "config.reads must be a comma-separated string or false when provided."
			}
		case string:
			if list := parseCsv(t); len(list) > 0 {
				target.DefaultReads = list
			} else {
				target.DefaultReads = nil
			}
		default:
			return "config.reads must be a comma-separated string or false when provided."
		}
	}
	if v, present := cfg["progress"]; present {
		b, isBool := v.(bool)
		if !isBool {
			return "config.progress must be a boolean when provided."
		}
		target.DefaultProgress = b
	}
	if v, present := cfg["maxSubagentDepth"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				target.MaxSubagentDepth = nil
			} else {
				return "config.maxSubagentDepth must be an integer >= 0 or false when provided."
			}
		case string:
			if t == "" {
				target.MaxSubagentDepth = nil
			} else {
				return "config.maxSubagentDepth must be an integer >= 0 or false when provided."
			}
		case float64:
			if t == float64(int(t)) && t >= 0 {
				n := int(t)
				target.MaxSubagentDepth = &n
			} else {
				return "config.maxSubagentDepth must be an integer >= 0 or false when provided."
			}
		default:
			return "config.maxSubagentDepth must be an integer >= 0 or false when provided."
		}
	}
	if v, present := cfg["toolBudget"]; present {
		if s, isString := v.(string); isString && s == "" {
			target.ToolBudget = ""
		} else if b, isBool := v.(bool); isBool && !b {
			target.ToolBudget = ""
		} else {
			parsed, err := parseJSON([]byte(marshalJSON(v, "")))
			if err != nil {
				return "config.toolBudget must be an object with hard and optional soft/block."
			}
			if msg := validateToolBudget(parsed, "config.toolBudget"); msg != "" {
				return msg
			}
			target.ToolBudget = marshalJSON(v, "")
		}
	}
	// An external runner does not support the Pi-only fields (the original's check).
	if target.Runner != nil && (target.Runner.Type == "external-cli" || target.Runner.Type == "external-job") {
		var unsupported []string
		if len(target.Tools) > 0 || len(target.McpDirectTools) > 0 {
			unsupported = append(unsupported, "tools")
		}
		if len(target.ExcludeTools) > 0 {
			unsupported = append(unsupported, "excludeTools")
		}
		if target.Model != "" {
			unsupported = append(unsupported, "model")
		}
		if s, isString := target.Thinking.(string); isString && s != "" {
			unsupported = append(unsupported, "thinking")
		}
		if len(target.Extensions) > 0 {
			unsupported = append(unsupported, "extensions")
		}
		if len(target.SubagentOnlyExtensions) > 0 {
			unsupported = append(unsupported, "subagentOnlyExtensions")
		}
		if len(target.MutationTools) > 0 {
			unsupported = append(unsupported, "mutationTools")
		}
		if len(target.Skills) > 0 || len(target.SkillPath) > 0 {
			unsupported = append(unsupported, "skills")
		}
		if target.MaxSubagentDepth != nil {
			unsupported = append(unsupported, "maxSubagentDepth")
		}
		if target.ToolBudget != "" {
			unsupported = append(unsupported, "toolBudget")
		}
		if len(unsupported) > 0 {
			return fmt.Sprintf("config.runner type '%s' does not support Pi-only fields: %s.", target.Runner.Type, strings.Join(unsupported, ", "))
		}
	}
	return ""
}

func argListOK(v any) bool {
	_, ok := stringList(v)
	return ok
}

func runnerConfigError() string {
	return "config.runner must be { type: 'pi' }, { type: 'external-cli', adapter?: <code-owned adapter>, command: string, args?: string[], promptDelivery?: 'stdin' }, or { type: 'external-job', provider: string, options?: object }."
}

// resolveTarget is the original's resolveTarget: one mutable definition of the
// name, or the message that disambiguates it.
func resolveTarget(name string, matches []AgentConfig, cwd string, scopeHint string) (*AgentConfig, string) {
	seen := map[string]bool{}
	var distinct []string
	for _, m := range matches {
		if !seen[m.Name] {
			seen[m.Name] = true
			distinct = append(distinct, m.Name)
		}
	}
	if len(distinct) > 1 {
		sort.Slice(distinct, func(i, j int) bool { return localeLess(distinct[i], distinct[j]) })
		return nil, fmt.Sprintf("Ambiguous agent alias or name '%s': %s", name, strings.Join(distinct, ", "))
	}
	var mutable []AgentConfig
	for _, m := range matches {
		if m.Source == SourceUser || m.Source == SourceProject {
			mutable = append(mutable, m)
		}
	}
	if len(mutable) == 0 {
		if len(matches) > 0 {
			return nil, fmt.Sprintf("Agent '%s' is read-only and cannot be modified. Create a same-named agent in user or project scope to override it.", name)
		}
		return nil, fmt.Sprintf("Agent '%s' not found. Available: %s.", name, availableNames(cwd))
	}
	if len(mutable) == 1 {
		return &mutable[0], ""
	}
	scope := scopeHint
	if scope != "user" && scope != "project" {
		var paths []string
		for _, m := range mutable {
			paths = append(paths, fmt.Sprintf("%s: %s", m.Source, m.FilePath))
		}
		return nil, fmt.Sprintf("Agent '%s' exists in both scopes. Specify agentScope: 'user' or 'project'.\n%s", name, strings.Join(paths, "\n"))
	}
	var scoped []AgentConfig
	for _, m := range mutable {
		if string(m.Source) == scope {
			scoped = append(scoped, m)
		}
	}
	if len(scoped) == 0 {
		return nil, fmt.Sprintf("Agent '%s' not found in scope '%s'.", name, scope)
	}
	if len(scoped) > 1 {
		var paths []string
		for _, m := range scoped {
			paths = append(paths, m.FilePath)
		}
		return nil, fmt.Sprintf("Multiple agents named '%s' found in scope '%s': %s", name, scope, strings.Join(paths, ", "))
	}
	return &scoped[0], ""
}

// availableNames is the original's availableAgentNames list.
func availableNames(cwd string) string {
	d := DiscoverAgentsAll(cwd)
	names := sortedNames(allAgents(d))
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// nameExistsInScope reports whether a name is taken in one scope.
func nameExistsInScope(cwd string, scope string, name string, excludePath string) bool {
	d := DiscoverAgentsAll(cwd)
	set := d.User
	if scope == "project" {
		set = d.Project
	}
	for _, a := range set {
		if a.Name == name && a.FilePath != excludePath {
			return true
		}
	}
	return false
}

// renamePath is the original's renamePath.
func renamePath(currentPath, newName, scope, cwd string) (string, string) {
	if nameExistsInScope(cwd, scope, newName, currentPath) {
		return "", fmt.Sprintf("Name '%s' already exists in %s scope.", newName, scope)
	}
	filePath := filepath.Join(filepath.Dir(currentPath), newName+".md")
	if fileExists(filePath) && filePath != currentPath {
		return "", fmt.Sprintf("File already exists at %s but is not a valid agent definition. Remove or rename it first.", filePath)
	}
	if err := os.Rename(currentPath, filePath); err != nil {
		return "", err.Error()
	}
	return filePath, ""
}

// actionScope is the original's actionScope: user unless stated, validated.
func actionScope(v any, action string) (string, string) {
	if v == nil {
		return "user", ""
	}
	if s, isString := v.(string); isString && (s == "user" || s == "project") {
		return s, ""
	}
	return "", fmt.Sprintf("agentScope must be 'user' or 'project' for %s.", action)
}

// createAction is `subagent({action:"create", config})`.
func createAction(cwd string, params map[string]any, registry ...[]availableModel) manageResult {
	var models []availableModel
	if len(registry) > 0 {
		models = registry[0]
	}
	cfg, errText, ok := configObject(params["config"])
	if errText != "" {
		return manageResult{errText, true}
	}
	if !ok {
		return manageResult{"config required for create.", true}
	}
	nameValue, isString := cfg["name"].(string)
	if !isString || jsTrim(nameValue) == "" {
		return manageResult{"config.name is required and must be a non-empty string.", true}
	}
	descValue, isString := cfg["description"].(string)
	if !isString || jsTrim(descValue) == "" {
		return manageResult{"config.description is required and must be a non-empty string.", true}
	}
	name := sanitizeName(nameValue)
	if name == "" {
		return manageResult{"config.name is invalid after sanitization. Use letters, numbers, spaces, or hyphens.", true}
	}
	pkg, pkgErr := configPackageName(cfg)
	if pkgErr != "" {
		return manageResult{pkgErr, true}
	}
	runtimeName := buildRuntimeName(name, pkg)
	scope, scopeErr := configScope(cfg)
	if scopeErr != "" {
		return manageResult{scopeErr, true}
	}
	if hasKey(cfg, "steps") {
		return manageResult{"Durable chain definitions were removed; use workflowScript or /prompt-workflow for repeatable workflows.", true}
	}
	d := DiscoverAgentsAll(cwd)
	targetDir := d.UserDir
	if scope == "project" {
		targetDir = d.ProjectDir
		if targetDir == "" {
			targetDir = filepath.Join(projectConfigDir(cwd), "agents")
		}
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return manageResult{err.Error(), true}
	}
	if nameExistsInScope(cwd, scope, runtimeName, "") {
		return manageResult{fmt.Sprintf("Name '%s' already exists in %s scope. Use update instead.", runtimeName, scope), true}
	}
	targetPath := filepath.Join(targetDir, runtimeName+".md")
	if fileExists(targetPath) {
		return manageResult{fmt.Sprintf("File already exists at %s but is not a valid agent definition. Remove or rename it first.", targetPath), true}
	}
	var warnings []string
	for _, b := range d.Builtin {
		if b.Name == runtimeName {
			warnings = append(warnings, fmt.Sprintf("Note: this shadows the builtin agent '%s'.", runtimeName))
		}
	}
	agent := &AgentConfig{
		Name: runtimeName, LocalName: name, PackageName: pkg,
		Description: jsTrim(descValue), Source: AgentSource(scope), FilePath: targetPath,
		SystemPrompt: "", SystemPromptMode: defaultSystemPromptMode(name),
		InheritProjectContext: defaultInheritProjectContext(name),
		InheritSkills:         false,
	}
	if msg := applyAgentConfig(agent, cfg); msg != "" {
		return manageResult{msg, true}
	}
	if err := os.WriteFile(targetPath, []byte(serializeAgent(agent, nil)), 0o644); err != nil {
		return manageResult{err.Error(), true}
	}
	if mw := modelWarning(agent.Model, models); mw != "" {
		warnings = append(warnings, mw)
	}
	return manageResult{text: strings.Join(append([]string{fmt.Sprintf("Created agent '%s' at %s.", runtimeName, targetPath)}, warnings...), "\n")}
}

func configPackageName(cfg map[string]any) (string, string) {
	v, present := cfg["package"]
	if !present || v == nil || v == false {
		return "", ""
	}
	if v == true {
		return "", "config.package is invalid after sanitization."
	}
	return parsePackageName(fmt.Sprint(v), true, "config.package")
}

func configScope(cfg map[string]any) (string, string) {
	scopeRaw := cfg["scope"]
	if scopeRaw == nil {
		return "user", ""
	}
	if s, isString := scopeRaw.(string); isString && (s == "user" || s == "project") {
		return s, ""
	}
	return "", "config.scope must be 'user' or 'project'."
}

// updateAction is `subagent({action:"update", agent, config})`.
func updateAction(cwd string, params map[string]any, registry ...[]availableModel) manageResult {
	var models []availableModel
	if len(registry) > 0 {
		models = registry[0]
	}
	name, _ := params["agent"].(string)
	if jsTrim(name) == "" {
		return manageResult{"Specify 'agent' for update.", true}
	}
	cfg, errText, ok := configObject(params["config"])
	if errText != "" {
		return manageResult{errText, true}
	}
	if !ok {
		return manageResult{"config required for update.", true}
	}
	if hasKey(cfg, "steps") {
		return manageResult{"Durable chain definitions were removed; use workflowScript or /prompt-workflow for repeatable workflows.", true}
	}
	scopeHint, _ := params["agentScope"].(string)
	if scopeHint != "user" && scopeHint != "project" {
		scopeHint = ""
	}
	d := DiscoverAgentsAll(cwd)
	matches := findAgentsScoped(name, scopeHint, d)
	target, targetErr := resolveTarget(name, matches, cwd, scopeHint)
	if targetErr != "" {
		return manageResult{targetErr, true}
	}
	if target.Source != SourceUser && target.Source != SourceProject {
		return manageResult{fmt.Sprintf("Cannot update %s agent '%s'. Eject it to user or project scope first.", target.Source, target.Name), true}
	}
	updated := *target
	oldName := target.Name
	if v, present := cfg["name"]; present {
		s, isString := v.(string)
		if !isString || jsTrim(s) == "" {
			return manageResult{"config.name must be a non-empty string when provided.", true}
		}
	}
	if v, present := cfg["description"]; present {
		s, isString := v.(string)
		if !isString || jsTrim(s) == "" {
			return manageResult{"config.description must be a non-empty string when provided.", true}
		}
	}
	newLocalName := updated.LocalName
	if newLocalName == "" {
		newLocalName = frontmatterName(updated.Name, updated.LocalName, updated.PackageName)
	}
	if v, present := cfg["name"]; present {
		newLocalName = sanitizeName(v.(string))
		if newLocalName == "" {
			return manageResult{"config.name is invalid after sanitization.", true}
		}
	}
	newPackageName := updated.PackageName
	if v, present := cfg["package"]; present {
		if v == false || v == nil {
			newPackageName = ""
		} else {
			pkg, pkgErr := configPackageName(cfg)
			if pkgErr != "" {
				return manageResult{pkgErr, true}
			}
			newPackageName = pkg
		}
	}
	preserve := preservedAgentFrontmatterFields(target, cfg)
	if msg := applyAgentConfig(&updated, cfg); msg != "" {
		return manageResult{msg, true}
	}
	updated.LocalName = newLocalName
	updated.PackageName = newPackageName
	updated.Name = buildRuntimeName(newLocalName, newPackageName)
	if v, present := cfg["description"]; present {
		updated.Description = jsTrim(v.(string))
	}
	if updated.Name != oldName {
		renamed, renameErr := renamePath(target.FilePath, updated.Name, string(target.Source), cwd)
		if renameErr != "" {
			return manageResult{renameErr, true}
		}
		updated.FilePath = renamed
	}
	if err := os.WriteFile(updated.FilePath, []byte(serializeAgent(&updated, preserve)), 0o644); err != nil {
		return manageResult{err.Error(), true}
	}
	headline := fmt.Sprintf("Updated agent '%s' at %s.", updated.Name, updated.FilePath)
	if updated.Name != oldName {
		headline = fmt.Sprintf("Updated agent '%s' to '%s' at %s.", oldName, updated.Name, updated.FilePath)
	}
	warnings := []string{}
	if v, present := cfg["model"]; present {
		if s, isString := v.(string); isString {
			if mw := modelWarning(s, models); mw != "" {
				warnings = append(warnings, mw)
			}
		}
	}
	return manageResult{text: strings.Join(append([]string{headline}, warnings...), "\n")}
}

// preservedAgentFrontmatterFields is the original's: the declared frontmatter
// keys the call does not change.
func preservedAgentFrontmatterFields(agent *AgentConfig, cfg map[string]any) map[string]bool {
	fields := map[string]bool{}
	if agent.Source == SourceBuiltin {
		return nil
	}
	fm, _ := ParseFrontmatter(mustRead(agent.FilePath))
	for _, k := range fm.Keys() {
		fields[k] = true
	}
	changed := func(names ...string) {
		for _, n := range names {
			delete(fields, n)
		}
	}
	if hasKey(cfg, "name") {
		changed("name")
	}
	if hasKey(cfg, "package") {
		changed("package")
	}
	if hasKey(cfg, "description") {
		changed("description")
	}
	if hasKey(cfg, "advertise") {
		changed("advertise")
	}
	if hasKey(cfg, "aliases") {
		changed("alias", "aliases")
	}
	if hasKey(cfg, "systemPrompt") {
		changed("systemPrompt")
	}
	if hasKey(cfg, "runner") {
		changed("runner")
	}
	if hasKey(cfg, "model") {
		changed("model")
	}
	if hasKey(cfg, "tools") {
		changed("tools")
	}
	if hasKey(cfg, "excludeTools") {
		changed("excludeTools")
	}
	if hasKey(cfg, "skills") {
		changed("skill", "skills")
	}
	if hasKey(cfg, "skillPath") {
		changed("skillPath")
	}
	if hasKey(cfg, "extensions") {
		changed("extensions")
	}
	if hasKey(cfg, "subagentOnlyExtensions") {
		changed("subagentOnlyExtensions")
	}
	if hasKey(cfg, "mutationTools") {
		changed("mutationTools")
	}
	if hasKey(cfg, "thinking") {
		changed("thinking")
		if cfg["thinking"] == "off" {
			fields["thinking"] = true
		}
	}
	if hasKey(cfg, "systemPromptMode") {
		changed("systemPromptMode")
		fields["systemPromptMode"] = true
	}
	if hasKey(cfg, "inheritProjectContext") {
		changed("inheritProjectContext")
		fields["inheritProjectContext"] = true
	}
	if hasKey(cfg, "inheritGlobalContext") {
		changed("inheritGlobalContext")
		fields["inheritGlobalContext"] = true
	}
	if hasKey(cfg, "inheritSkills") {
		changed("inheritSkills")
		fields["inheritSkills"] = true
	}
	if hasKey(cfg, "defaultContext") {
		changed("defaultContext")
	}
	if hasKey(cfg, "async") {
		changed("async")
	}
	if hasKey(cfg, "timeoutMs") {
		changed("timeoutMs")
	}
	if hasKey(cfg, "acceptance") {
		changed("acceptance")
	}
	if hasKey(cfg, "acceptanceRole") {
		changed("acceptanceRole")
	}
	if hasKey(cfg, "output") {
		changed("output")
	}
	if hasKey(cfg, "outputMode") {
		changed("outputMode")
	}
	if hasKey(cfg, "reads") {
		changed("defaultReads")
	}
	if hasKey(cfg, "progress") {
		changed("defaultProgress")
	}
	if hasKey(cfg, "maxSubagentDepth") {
		changed("maxSubagentDepth")
	}
	if hasKey(cfg, "toolBudget") {
		changed("toolBudget")
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func mustRead(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// deleteAction is `subagent({action:"delete", agent})`.
func deleteAction(cwd string, params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if jsTrim(name) == "" {
		return manageResult{"Specify 'agent' for delete.", true}
	}
	scopeHint, _ := params["agentScope"].(string)
	if scopeHint != "user" && scopeHint != "project" {
		scopeHint = ""
	}
	d := DiscoverAgentsAll(cwd)
	matches := findAgentsScoped(name, scopeHint, d)
	target, targetErr := resolveTarget(name, matches, cwd, scopeHint)
	if targetErr != "" {
		return manageResult{targetErr, true}
	}
	if err := os.Remove(target.FilePath); err != nil {
		return manageResult{err.Error(), true}
	}
	return manageResult{text: fmt.Sprintf("Deleted agent '%s' at %s.", target.Name, target.FilePath)}
}

// ejectAction is `subagent({action:"eject", agent, agentScope})`: copy a builtin
// or package agent into a customizable scope.
func ejectAction(cwd string, params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if jsTrim(name) == "" {
		return manageResult{"Specify 'agent' for eject.", true}
	}
	raw := jsTrim(name)
	sanitized := sanitizeName(raw)
	scope, scopeErr := actionScope(params["agentScope"], "eject")
	if scopeErr != "" {
		return manageResult{scopeErr, true}
	}
	d := DiscoverAgentsAll(cwd)
	var source *AgentConfig
	for _, set := range [][]AgentConfig{d.Package, d.Builtin} {
		for i := range set {
			if set[i].Name == raw || set[i].Name == sanitized {
				source = &set[i]
				break
			}
		}
		if source != nil {
			break
		}
	}
	if source == nil {
		return manageResult{fmt.Sprintf("Agent '%s' not found or is not a bundled/package agent. eject copies a builtin or package agent to %s scope so it can be customized. Available: %s.", raw, scope, availableNames(cwd)), true}
	}
	runtimeName := source.Name
	for _, a := range scopeSet(d, scope) {
		if a.Name == runtimeName {
			return manageResult{fmt.Sprintf("Agent '%s' is already a custom %s agent at %s. Edit it with { action: \"update\", agent: \"%s\" } or delete it first.", runtimeName, scope, a.FilePath, runtimeName), true}
		}
	}
	if nameExistsInScope(cwd, scope, runtimeName, "") {
		return manageResult{fmt.Sprintf("An agent named '%s' already exists in %s scope. Remove or rename it first.", runtimeName, scope), true}
	}
	targetDir := d.UserDir
	if scope == "project" {
		targetDir = d.ProjectDir
		if targetDir == "" {
			targetDir = filepath.Join(projectConfigDir(cwd), "agents")
		}
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return manageResult{err.Error(), true}
	}
	targetPath := filepath.Join(targetDir, runtimeName+".md")
	if fileExists(targetPath) {
		return manageResult{fmt.Sprintf("File already exists at %s but is not a valid agent definition. Remove or rename it first.", targetPath), true}
	}
	content, err := os.ReadFile(source.FilePath)
	if err != nil {
		return manageResult{fmt.Sprintf("Failed to read source agent at %s: %s", source.FilePath, err.Error()), true}
	}
	if err := os.WriteFile(targetPath, content, 0o644); err != nil {
		return manageResult{err.Error(), true}
	}
	return manageResult{text: fmt.Sprintf("Ejected agent '%s' from %s to %s scope at %s. Edit it there to customize; it shadows the bundled %s agent of the same name.", runtimeName, source.Source, scope, targetPath, source.Source)}
}

func scopeSet(d DiscoveryAll, scope string) []AgentConfig {
	if scope == "project" {
		return d.Project
	}
	return d.User
}

// findAgentsScoped is the management find: the definitions a name resolves to, in a scope.
func findAgentsScoped(name string, scopeHint string, d DiscoveryAll) []AgentConfig {
	scope := ScopeBoth
	if scopeHint == "user" {
		scope = ScopeUser
	} else if scopeHint == "project" {
		scope = ScopeProject
	}
	return findAgents(name, scope, d)
}
