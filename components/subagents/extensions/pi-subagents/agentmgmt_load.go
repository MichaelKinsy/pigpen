package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Agent definition loading (src/agents/agents.ts:1958-2295, src/agents/agent-serializer.ts KNOWN_FIELDS,
// src/runs/shared/external-cli-contract.ts parsing and validation).

// knownFields is agent-serializer.ts KNOWN_FIELDS.
var knownFields = map[string]bool{
	"name": true, "package": true, "description": true, "advertise": true, "alias": true, "aliases": true, "tools": true,
	"excludeTools": true, "allowNestedSubagents": true, "allowedAgents": true, "model": true, "fast": true, "thinking": true,
	"systemPromptMode": true, "inheritProjectContext": true, "inheritGlobalContext": true, "inheritSkills": true,
	"defaultContext": true, "async": true, "timeoutMs": true, "toolTimeoutMs": true, "acceptance": true, "acceptanceRole": true,
	"skill": true, "skills": true, "skillPath": true, "extensions": true, "subagentOnlyExtensions": true, "mutationTools": true,
	"machine": true, "output": true, "outputMode": true, "outputSchema": true, "defaultReads": true, "defaultProgress": true,
	"interactive": true, "maxSubagentDepth": true, "toolBudget": true, "permission": true, "permissions": true, "memory": true,
	"runner": true,
}

// codeOwnedExternalCliAdapterIDs is CODE_OWNED_EXTERNAL_CLI_ADAPTER_IDS.
var codeOwnedExternalCliAdapterIDs = []string{"codex-exec", "codex-exec-writer", "claude-code", "claude-code-writer", "cursor-agent", "cursor-agent-writer"}

func isCodeOwnedExternalCliAdapterID(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, id := range codeOwnedExternalCliAdapterIDs {
		if id == s {
			return true
		}
	}
	return false
}

// codeOwnedExternalCliAdapterLabel is CODE_OWNED_EXTERNAL_CLI_ADAPTER_LABEL.
func codeOwnedExternalCliAdapterLabel() string {
	q := make([]string, len(codeOwnedExternalCliAdapterIDs))
	for i, id := range codeOwnedExternalCliAdapterIDs {
		q[i] = "'" + id + "'"
	}
	return strings.Join(q, ", ")
}

var externalCliCapabilityKeys = map[string]bool{"steer": true, "resume": true, "structuredOutput": true, "toolEvents": true, "supervisor": true, "forkContext": true, "extensionBindings": true}

// parseExternalCliCapabilityNarrowing is external-cli-contract.ts:67; present false is undefined.
func parseExternalCliCapabilityNarrowing(v any, present bool, label string) (*jsObject, error) {
	if !present {
		return nil, nil
	}
	in, ok := v.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("%s must be an object.", label)
	}
	var unknown []string
	for _, k := range in.order() {
		if !externalCliCapabilityKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("%s has unsupported fields: %s.", label, strings.Join(unknown, ", "))
	}
	for _, k := range in.order() {
		if b, isB := in.vals[k].(bool); !isB || b {
			return nil, fmt.Errorf("%s.%s may only be false; user config cannot widen code-owned external adapter capabilities.", label, k)
		}
	}
	return in, nil
}

// validateCodeOwnedProfileRunner is external-cli-contract.ts:50: the error for a reserved selection name, or "".
func validateCodeOwnedProfileRunner(name, localName string, aliases []string, runner *Runner) string {
	names := []string{name}
	if localName != "" {
		names = append(names, localName)
	}
	names = append(names, aliases...)
	for _, a := range []struct{ name, writer, access string }{
		{"claude-code", "claude-code-writer", "file-write"}, {"codex-exec", "codex-exec-writer", "workspace-write"}, {"cursor-agent", "cursor-agent-writer", "workspace-write"},
	} {
		in := false
		for _, n := range names {
			in = in || n == a.name
		}
		if in && !(runner != nil && runner.Type == "external-cli" && runner.Adapter == a.name) {
			return fmt.Sprintf("Selection name '%s' is reserved for the read-only '%s' adapter. Use '%s' for explicit %s access.", a.name, a.name, a.writer, a.access)
		}
	}
	return ""
}

func isJSONSerializable(v any) bool {
	switch x := v.(type) {
	case nil, string, bool:
		return true
	case float64:
		return finite(x)
	case []any:
		for _, e := range x {
			if !isJSONSerializable(e) {
				return false
			}
		}
		return true
	case *jsObject:
		for _, k := range x.keys {
			if !isJSONSerializable(x.vals[k]) {
				return false
			}
		}
		return true
	}
	return false
}

func runnerErr(agent, format string, args ...any) error {
	return fmt.Errorf("Agent '%s' "+format, append([]any{agent}, args...)...)
}

// parseAgentRunnerFrontmatter is agents.ts:1966.
func parseAgentRunnerFrontmatter(raw string, agentName string) (*Runner, error) {
	if jsTrim(raw) == "" {
		return nil, nil
	}
	parsed, err := parseYAML(raw)
	if err != nil {
		return nil, fmt.Errorf("Agent '%s' has invalid runner frontmatter: %s", agentName, err.Error())
	}
	r, ok := parsed.(*jsObject)
	if !ok {
		return nil, runnerErr(agentName, "has invalid runner frontmatter; expected an object.")
	}
	typ, _ := r.vals["type"].(string)
	keysOf := func() []string { return r.order() }
	switch {
	case typ == "pi" && r.vals["type"] == "pi":
		for _, k := range keysOf() {
			if k != "type" {
				return nil, runnerErr(agentName, "has invalid Pi runner frontmatter; only 'type' is supported.")
			}
		}
		return &Runner{Type: "pi"}, nil
	case typ == "external-job":
		prov, isS := r.vals["provider"].(string)
		if !isS || jsTrim(prov) == "" || jsTrim(prov) != prov {
			return nil, runnerErr(agentName, "external-job runner requires a non-empty trimmed provider string.")
		}
		var opts *jsObject
		if ov, has := r.vals["options"]; has && ov != nil && ov != false {
			o, isO := ov.(*jsObject)
			if !isO || !isJSONSerializable(o) {
				return nil, runnerErr(agentName, "external-job runner options must be a JSON-serializable object.")
			}
			opts = o
		} else if has && ov != nil {
			// a falsy non-nil value (false) is still not an object
			return nil, runnerErr(agentName, "external-job runner options must be a JSON-serializable object.")
		}
		var unknown []string
		for _, k := range keysOf() {
			if k != "type" && k != "provider" && k != "options" {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			return nil, runnerErr(agentName, "external-job runner has unsupported fields: %s.", strings.Join(unknown, ", "))
		}
		return &Runner{Type: "external-job", Provider: prov, Options: opts}, nil
	}
	if r.vals["type"] != "external-cli" {
		return nil, runnerErr(agentName, "has invalid runner.type; expected 'pi', 'external-cli', or 'external-job'.")
	}
	cmd, isS := r.vals["command"].(string)
	if !isS || jsTrim(cmd) == "" {
		return nil, runnerErr(agentName, "external-cli runner requires a non-empty command string.")
	}
	var args []string
	if av, has := r.vals["args"]; has && av != nil {
		arr, isArr := av.([]any)
		if !isArr {
			return nil, runnerErr(agentName, "external-cli runner args must be an array of strings.")
		}
		for _, a := range arr {
			s, isS := a.(string)
			if !isS {
				return nil, runnerErr(agentName, "external-cli runner args must be an array of strings.")
			}
			args = append(args, s)
		}
	} else if has && av == nil {
		// null is not undefined: the original rejects it too
		return nil, runnerErr(agentName, "external-cli runner args must be an array of strings.")
	}
	adapterV, hasAdapter := r.vals["adapter"]
	if hasAdapter && !isCodeOwnedExternalCliAdapterID(adapterV) {
		return nil, runnerErr(agentName, "external-cli runner adapter must be %s.", codeOwnedExternalCliAdapterLabel())
	}
	if hasAdapter && len(args) > 0 {
		return nil, runnerErr(agentName, "%s adapter owns its argv; runner args are not supported.", adapterV)
	}
	pd, hasPD := r.vals["promptDelivery"]
	if hasPD && pd != "stdin" {
		return nil, runnerErr(agentName, "external-cli runner promptDelivery must be 'stdin'.")
	}
	capV, capOK := r.vals["capabilities"]
	caps, err := parseExternalCliCapabilityNarrowing(capV, capOK, fmt.Sprintf("Agent '%s' external-cli runner capabilities", agentName))
	if err != nil {
		return nil, err
	}
	var unknown []string
	for _, k := range keysOf() {
		switch k {
		case "type", "adapter", "command", "args", "promptDelivery", "capabilities":
		default:
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, runnerErr(agentName, "external-cli runner has unsupported fields: %s.", strings.Join(unknown, ", "))
	}
	out := &Runner{Type: "external-cli", Command: jsTrim(cmd), Args: args, Capabilities: caps}
	if hasAdapter {
		out.Adapter, _ = adapterV.(string)
	}
	if hasPD && pd != nil && pd != "" && pd != false {
		out.PromptDelivery = "stdin"
	}
	return out, nil
}

// validateExternalRunnerProfile is agents.ts:2027.
func validateExternalRunnerProfile(fm Frontmatter, agentName string, runner *Runner) error {
	if runner == nil || (runner.Type != "external-cli" && runner.Type != "external-job") {
		return nil
	}
	var unsupported []string
	for _, f := range []string{"tools", "excludeTools", "allowNestedSubagents", "allowedAgents", "model", "thinking", "extensions", "subagentOnlyExtensions", "mutationTools", "maxSubagentDepth", "skills", "skill", "skillPath", "toolBudget", "permission", "permissions"} {
		if fm.Has(f) {
			unsupported = append(unsupported, f)
		}
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("Agent '%s' uses runner.type='%s' and declares unsupported Pi-only fields: %s.", agentName, runner.Type, strings.Join(unsupported, ", "))
	}
	return nil
}

// parseAgentAcceptanceFrontmatter is agents.ts:2036; ok false is undefined.
func parseAgentAcceptanceFrontmatter(raw string, agentName string) (any, bool, error) {
	if jsTrim(raw) == "" {
		return nil, false, nil
	}
	parsed, err := parseYAML(raw)
	if err != nil {
		return nil, false, fmt.Errorf("Agent '%s' has invalid acceptance frontmatter: %s", agentName, err.Error())
	}
	if errs := validateAcceptanceInput(parsed, fmt.Sprintf("Agent '%s' acceptance frontmatter", agentName)); len(errs) > 0 {
		return nil, false, fmt.Errorf("%s", strings.Join(errs, " "))
	}
	return parsed, true, nil
}

// resolveAgentRelativeExtensionPaths is agents.ts:2070.
func resolveAgentRelativeExtensionPaths(paths []string, agentFilePath string) []string {
	if paths == nil {
		return nil
	}
	base := filepath.Dir(agentFilePath)
	out := make([]string, len(paths))
	for i, e := range paths {
		t := jsTrim(e)
		if t == "." || t == ".." || strings.HasPrefix(t, "./") || strings.HasPrefix(t, "../") {
			abs, _ := filepath.Abs(filepath.Join(base, t))
			out[i] = abs
		} else {
			out[i] = e
		}
	}
	return out
}

func tri(fm Frontmatter, key, local string) (*bool, error) {
	if !fm.Has(key) {
		return nil, nil
	}
	switch fm.Get(key) {
	case "true":
		return new(true), nil
	case "false":
		return new(false), nil
	}
	return nil, fmt.Errorf("Agent '%s' has invalid %s frontmatter; expected true or false.", local, key)
}

// packageSource is the package metadata an agent from a package carries.
type packageSource struct{ name, version, root string }

// loadOne turns one definition file into an agent (the body of agents.ts:2086-2288's loop). It returns nil, nil for a file
// that is not an agent definition (no name or description).
func loadOne(f definitionFile, source AgentSource, priority *int, pkg *packageSource) (a *AgentConfig, diag *Diagnostic) {
	var name, runtimeName string
	packageSpecified := false
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(loadError)
			if !ok {
				panic(r)
			}
			a = nil
			d := Diagnostic{Source: source, FilePath: f.path, Name: name, Error: e.err.Error(), PackageSpecified: packageSpecified}
			if runtimeName != "" && runtimeName != name {
				d.RuntimeName = runtimeName
			}
			if priority != nil {
				d.DiscoveryPriority, d.HasDiscoveryPriority = *priority, true
			}
			diag = &d
		}
	}()
	check := func(err error) {
		if err != nil {
			panic(loadError{err})
		}
	}
	fm, body := ParseFrontmatter(f.content)
	if fm.Get("name") == "" || fm.Get("description") == "" {
		return nil, nil
	}
	local := fm.Get("name")
	name = local
	pkgName, perr := parsePackageName(fm.Get("package"), fm.Has("package"), fmt.Sprintf("Agent '%s' package", local))
	packageSpecified = pkgName != "" || perr != ""
	if perr != "" {
		check(fmt.Errorf("%s", perr))
	}
	runtimeName = buildRuntimeName(local, pkgName)

	var runner *Runner
	if fm.Has("runner") {
		r, err := parseAgentRunnerFrontmatter(fm.Get("runner"), local)
		check(err)
		runner = r
	}
	check(validateExternalRunnerProfile(fm, local, runner))
	advertise, err := tri(fm, "advertise", local)
	check(err)
	rawTools := ParseFrontmatterList(optional(fm, "tools"))
	tools, mcp := splitToolList(rawTools, rawTools != nil)
	if tools == nil {
		tools = []string{}
	}
	excludeTools := ParseFrontmatterList(optional(fm, "excludeTools"))
	var allowedAgents []string
	if parsed := ParseFrontmatterList(optional(fm, "allowedAgents")); parsed != nil {
		norm, err := normalizeCapabilityCeilingAllowedAgents(anyStrings(parsed))
		check(err)
		allowedAgents = norm
	}
	defaultReads := ParseFrontmatterList(optional(fm, "defaultReads"))
	aliasKey := "aliases"
	if !fm.Has("aliases") {
		aliasKey = "alias"
	}
	aliases := normalizeAliases(ParseFrontmatterList(optional(fm, aliasKey)), runtimeName)
	if msg := validateCodeOwnedProfileRunner(runtimeName, local, aliases, runner); msg != "" {
		check(fmt.Errorf("%s", msg))
	}
	skillStr := fm.Get("skill")
	if skillStr == "" {
		skillStr = fm.Get("skills")
	}
	var skills []string
	if skillStr != "" || fm.Has("skill") || fm.Has("skills") {
		skills = ParseFrontmatterList(&skillStr)
	}
	skillPath := ParseFrontmatterList(optional(fm, "skillPath"))
	if fm.Has("fallbackModels") {
		check(fmt.Errorf("Agent '%s' uses removed frontmatter field 'fallbackModels'. Configure one model instead.", f.path))
	}
	spm := defaultSystemPromptMode(local)
	if v := fm.Get("systemPromptMode"); v == "replace" || v == "append" {
		spm = v
	}
	ipc := defaultInheritProjectContext(local)
	switch fm.Get("inheritProjectContext") {
	case "true":
		ipc = true
	case "false":
		ipc = false
	}
	igc := fm.Get("inheritGlobalContext") == "true"
	isk := false
	switch fm.Get("inheritSkills") {
	case "true":
		isk = true
	}
	defaultContext := ""
	if c := fm.Get("defaultContext"); c == "fork" || c == "fresh" {
		defaultContext = c
	}
	defaultAsync, err := tri(fm, "async", local)
	check(err)
	var timeoutMs *int
	if fm.Has("timeoutMs") {
		n := jsToNumber(fm.Get("timeoutMs"))
		if !isIntegerNum(n) || n <= 0 {
			check(fmt.Errorf("Agent '%s' has invalid timeoutMs frontmatter; expected a positive integer.", local))
		}
		timeoutMs = new(int(n))
	}
	var toolTimeoutMs *int
	if fm.Has("toolTimeoutMs") {
		n := jsToNumber(fm.Get("toolTimeoutMs"))
		if !isIntegerNum(n) || n <= 0 || n > 2147483647 {
			check(fmt.Errorf("Agent '%s' has invalid toolTimeoutMs frontmatter; expected a positive integer no larger than 2147483647.", local))
		}
		toolTimeoutMs = new(int(n))
	}
	var defaultAcceptance any
	if fm.Has("acceptance") {
		v, ok, err := parseAgentAcceptanceFrontmatter(fm.Get("acceptance"), local)
		check(err)
		if ok {
			defaultAcceptance = v
		}
	}
	outputMode := ""
	if fm.Has("outputMode") {
		if v := fm.Get("outputMode"); v == "inline" || v == "file-only" {
			outputMode = v
		} else {
			check(fmt.Errorf("Agent '%s' has invalid outputMode frontmatter; expected 'inline' or 'file-only'.", local))
		}
	}
	acceptanceRole := ""
	if fm.Has("acceptanceRole") && jsTrim(fm.Get("acceptanceRole")) != "" {
		if v := fm.Get("acceptanceRole"); v == "read-only" || v == "writer" {
			acceptanceRole = v
		} else {
			check(fmt.Errorf("Agent '%s' has invalid acceptanceRole frontmatter; expected 'read-only' or 'writer'.", local))
		}
	}
	extensions := resolveAgentRelativeExtensionPaths(ParseFrontmatterList(optional(fm, "extensions")), f.path)
	subagentOnly := resolveAgentRelativeExtensionPaths(ParseFrontmatterList(optional(fm, "subagentOnlyExtensions")), f.path)
	mutationTools := ParseFrontmatterList(optional(fm, "mutationTools"))
	fast, err := tri(fm, "fast", local)
	check(err)
	allowNested, err := tri(fm, "allowNestedSubagents", local)
	check(err)

	var extra []KV
	for _, k := range fm.Keys() {
		if !knownFields[k] {
			extra = append(extra, KV{k, fm.Get(k)})
		}
	}
	depth := jsToNumber(fm.Get("maxSubagentDepth"))
	if !fm.Has("maxSubagentDepth") {
		depth = nan()
	}
	if fm.Has("permission") && fm.Has("permissions") {
		check(fmt.Errorf("Agent '%s' cannot declare both permission and permissions frontmatter.", local))
	}
	permSrc, permKey := "", ""
	if fm.Has("permissions") {
		permSrc, permKey = fm.Get("permissions"), "permissions"
	} else if fm.Has("permission") {
		permSrc, permKey = fm.Get("permission"), "permission"
	}
	var permissions any
	if permKey != "" && jsTrim(permSrc) != "" {
		pv, err := parseYAML(permSrc)
		check(err)
		rules, err := validatePermissionRules(pv, fmt.Sprintf("Agent '%s' permissions", local))
		check(err)
		if rules != nil {
			permissions = rules
		}
	}
	var toolBudget any
	if fm.Has("toolBudget") && jsTrim(fm.Get("toolBudget")) != "" {
		pv, msg := jsJSONParse(fm.Get("toolBudget"))
		if msg != "" {
			check(fmt.Errorf("%s", msg))
		}
		o, ok := pv.(*jsObject)
		if !ok {
			check(fmt.Errorf("Agent '%s' has invalid toolBudget frontmatter; expected a JSON object.", local))
		}
		toolBudget = o
	}
	var outputSchema any
	if fm.Has("outputSchema") && jsTrim(fm.Get("outputSchema")) != "" {
		pv, msg := jsJSONParse(fm.Get("outputSchema"))
		if msg != "" {
			check(fmt.Errorf("%s", msg))
		}
		check(assertJSONSchemaObject(pv, fmt.Sprintf("Agent '%s' outputSchema", local)))
		outputSchema = pv
	}
	var maxDepth *int
	if isIntegerNum(depth) && depth >= 0 {
		maxDepth = new(int(depth))
	}
	memory := parseMemoryFrontmatter(fm.Get("memory"))
	mv := fm.Get("machine")
	machine, err := validateOptionalMachine(mv, fm.Has("machine"), fmt.Sprintf("Agent '%s' frontmatter 'machine'", runtimeName))
	check(err)

	agent := &AgentConfig{
		Name: runtimeName, Runner: runner, LocalName: local, PackageName: pkgName, Description: fm.Get("description"),
		Advertise: advertise, Aliases: aliases, ExcludeTools: excludeTools, AllowNestedSubagents: allowNested, AllowedAgents: allowedAgents,
		Fast: fast, SystemPromptMode: spm, InheritProjectContext: ipc, InheritGlobalContext: igc, InheritSkills: isk,
		DefaultContext: defaultContext, DefaultAsync: defaultAsync, DefaultTimeoutMs: timeoutMs, DefaultToolTimeoutMs: toolTimeoutMs,
		DefaultAcceptance: defaultAcceptance, AcceptanceRole: acceptanceRole, SystemPrompt: body, Source: source, FilePath: f.path,
		Extensions: extensions, SubagentOnlyExtensions: subagentOnly, Machine: machine, OutputMode: outputMode, OutputSchema: outputSchema,
		DefaultProgress: fm.Get("defaultProgress") == "true", Interactive: fm.Get("interactive") == "true", MaxSubagentDepth: maxDepth,
		ToolBudget: toolBudget, Permissions: permissions, Memory: memory, ExtraFields: extra,
	}
	if rawTools != nil {
		agent.Tools = tools
	}
	if len(mcp) > 0 {
		agent.McpDirectTools = mcp
	}
	if pkg != nil {
		agent.PackageSourceName, agent.PackageSourceVersion, agent.PackageSourceRoot = pkg.name, pkg.version, pkg.root
	}
	if fm.Has("model") {
		agent.Model = fm.Get("model")
	}
	if fm.Has("thinking") {
		if fm.Get("thinking") == "false" {
			agent.Thinking = false
		} else {
			agent.Thinking = fm.Get("thinking")
		}
	}
	if priority != nil {
		agent.discoveryPriority, agent.hasDiscoveryPriority = *priority, true
	}
	if len(skills) > 0 {
		agent.Skills = skills
	}
	if len(skillPath) > 0 {
		agent.SkillPath = skillPath
	}
	if len(mutationTools) > 0 {
		agent.MutationTools = mutationTools
	}
	if fm.Has("output") {
		agent.Output = fm.Get("output")
	}
	if len(defaultReads) > 0 {
		agent.DefaultReads = defaultReads
	}
	agent.FrontmatterFields = map[string]bool{}
	for _, k := range fm.Keys() {
		agent.FrontmatterFields[k] = true
	}
	return agent, nil
}

type loadError struct{ err error }

func nan() float64 { return jsToNumber("x") }

// loadAgentsFromDefinitionFiles is agents.ts:2082.
func loadAgentsFromDefinitionFiles(files []definitionFile, source AgentSource, priority *int, pkg *packageSource) ([]AgentConfig, []Diagnostic) {
	var agents []AgentConfig
	var diags []Diagnostic
	for _, f := range files {
		a, d := loadOne(f, source, priority, pkg)
		if d != nil {
			diags = append(diags, *d)
		} else if a != nil {
			agents = append(agents, *a)
		}
	}
	return agents, diags
}

// ---- memory frontmatter (agent-memory.ts parseMemoryFrontmatter) ----

var (
	memInlineRe = regexp.MustCompile(`(?s)^\{(.*)\}$`)
	memPartRe   = regexp.MustCompile(`^([\w-]+)\s*:\s*(.*)$`)
	memLineRe   = regexp.MustCompile(`^\s*([\w-]+):\s*(.*)$`)
)

func unquoteFrontmatterValue(v string) string {
	t := jsTrim(v)
	if len(t) >= 2 && ((t[0] == '"' && t[len(t)-1] == '"') || (t[0] == '\'' && t[len(t)-1] == '\'')) {
		return t[1 : len(t)-1]
	}
	return t
}

// parseMemoryFrontmatter parses a `memory` frontmatter block into a typed config, nil when invalid.
func parseMemoryFrontmatter(raw string) *AgentMemoryConfig {
	if raw == "" {
		return nil
	}
	entries := map[string]string{}
	trimmed := jsTrim(raw)
	if m := memInlineRe.FindStringSubmatch(trimmed); m != nil {
		for _, part := range strings.Split(m[1], ",") {
			if pm := memPartRe.FindStringSubmatch(jsTrim(part)); pm != nil {
				entries[pm[1]] = unquoteFrontmatterValue(pm[2])
			}
		}
	} else {
		for _, line := range strings.Split(raw, "\n") {
			if lm := memLineRe.FindStringSubmatch(line); lm != nil {
				entries[lm[1]] = unquoteFrontmatterValue(lm[2])
			}
		}
	}
	scope, path := entries["scope"], entries["path"]
	if scope != "project" && scope != "user" {
		return nil
	}
	if path == "" {
		return nil
	}
	return &AgentMemoryConfig{Scope: scope, Path: path}
}

var _ = os.Stat
