package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Ports of the launch identity pieces of pi-subagents that preflight (policy_preflight.go) and every launch path share:
// src/shared/launch-contract.ts (stable digests of the agent definition and of the resolved launch inputs),
// src/runs/shared/extension-bindings.ts, src/shared/required-child-extensions.ts, src/intercom/intercom-bridge.ts,
// the launch context of src/shared/fork-context.ts, resolveStepBehavior of src/runs/shared/child-launch-plan.ts and the child tool
// plan of src/runs/shared/child-tool-plan.ts.

// ---- launch-contract.ts: stable digests -------------------------------------------------------------------------------------

const (
	agentDefinitionProjectionVersion = 2
	launchBindingProjectionVersion   = 2
)

// stableAny turns a value into the tree stableJSON writes: maps and slices stay, an ordered object becomes a map, any other
// type goes through its JSON form.
func stableAny(v any) any {
	switch x := v.(type) {
	case nil, string, bool, float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = stableAny(e)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = stableAny(e)
		}
		return out
	case *jsObject:
		if x == nil {
			return nil
		}
		out := map[string]any{}
		for k, e := range x.vals {
			if _, u := e.(undef); u {
				continue
			}
			out[k] = stableAny(e)
		}
		return out
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return fmt.Sprint(v)
	}
	return out
}

// stableJSON is launch-contract.ts stableJson: keys sorted by locale order, no undefined (absent) entries.
func stableJSON(v any) string {
	switch x := stableAny(v).(type) {
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = stableJSON(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return localeLess(keys[i], keys[j]) })
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = marshalJSON(k, "") + ":" + stableJSON(x[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return marshalJSON(x, "")
	}
}

func stableJSONDigest(v any) string {
	sum := sha256.Sum256([]byte(stableJSON(v)))
	return hex.EncodeToString(sum[:])
}

func fileDigest(filePath string) (string, bool) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), true
}

// putIf sets m[k] = v when v is not the zero "undefined" of its Go type.
func putIf(m map[string]any, k string, v any) {
	switch x := v.(type) {
	case nil:
		return
	case string:
		if x == "" {
			return
		}
	case []string:
		if x == nil {
			return
		}
	case *bool:
		if x == nil {
			return
		}
		v = *x
	case *int:
		if x == nil {
			return
		}
		v = *x
	case *jsObject:
		if x == nil {
			return
		}
	}
	m[k] = v
}

func runnerProjection(r *Runner) any {
	if r == nil {
		return nil
	}
	m := map[string]any{}
	putIf(m, "type", r.Type)
	putIf(m, "command", r.Command)
	putIf(m, "adapter", r.Adapter)
	putIf(m, "args", r.Args)
	putIf(m, "promptDelivery", r.PromptDelivery)
	putIf(m, "capabilities", r.Capabilities)
	putIf(m, "provider", r.Provider)
	putIf(m, "options", r.Options)
	return m
}

// projectAgentDefinition is public-safe, deterministic evidence for the parsed launch-affecting agent definition.
func projectAgentDefinition(a AgentConfig) map[string]any {
	m := map[string]any{"version": agentDefinitionProjectionVersion, "name": a.Name, "filePath": a.FilePath,
		"systemPrompt": a.SystemPrompt, "systemPromptMode": a.SystemPromptMode, "inheritProjectContext": a.InheritProjectContext,
		"inheritGlobalContext": a.InheritGlobalContext, "inheritSkills": a.InheritSkills, "defaultProgress": a.DefaultProgress, "interactive": a.Interactive}
	putIf(m, "localName", a.LocalName)
	putIf(m, "packageName", a.PackageName)
	if d, ok := fileDigest(a.FilePath); ok {
		m["fileContentDigest"] = d
	}
	putIf(m, "runner", runnerProjection(a.Runner))
	putIf(m, "model", a.Model)
	putIf(m, "modelProvider", a.ModelProvider)
	putIf(m, "fast", a.Fast)
	putIf(m, "thinking", a.Thinking)
	putIf(m, "tools", a.Tools)
	putIf(m, "excludeTools", a.ExcludeTools)
	putIf(m, "allowNestedSubagents", a.AllowNestedSubagents)
	putIf(m, "allowedAgents", a.AllowedAgents)
	putIf(m, "mcpDirectTools", a.McpDirectTools)
	putIf(m, "extensions", a.Extensions)
	putIf(m, "subagentOnlyExtensions", a.SubagentOnlyExtensions)
	putIf(m, "mutationTools", a.MutationTools)
	putIf(m, "skills", a.Skills)
	putIf(m, "skillPath", a.SkillPath)
	putIf(m, "output", a.Output)
	putIf(m, "outputSchema", a.OutputSchema)
	putIf(m, "defaultReads", a.DefaultReads)
	putIf(m, "defaultContext", a.DefaultContext)
	putIf(m, "defaultAsync", a.DefaultAsync)
	putIf(m, "defaultTimeoutMs", a.DefaultTimeoutMs)
	putIf(m, "defaultAcceptance", a.DefaultAcceptance)
	putIf(m, "acceptanceRole", a.AcceptanceRole)
	putIf(m, "maxSubagentDepth", a.MaxSubagentDepth)
	putIf(m, "toolBudget", a.ToolBudget)
	if a.Memory != nil {
		m["memory"] = map[string]any{"scope": a.Memory.Scope, "path": a.Memory.Path}
	}
	return m
}

// agentDefinitionDigest is the digest of the parsed definition; a runtime overlay that already captured it wins.
func agentDefinitionDigest(a AgentConfig) string {
	if a.DefinitionDigest != "" {
		return a.DefinitionDigest
	}
	return stableJSONDigest(projectAgentDefinition(a))
}

// launchBindingInput is the resolved inputs handed to the child, as launch-contract.ts LaunchBindingInput.
type launchBindingInput struct {
	DefinitionDigest                                                    string
	Task                                                                *string
	Model                                                               string
	Fast                                                                *bool
	Thinking                                                            string
	SystemPrompt                                                        *string
	SystemPromptMode                                                    string
	InheritProjectContext, InheritGlobalContext, InheritSkills          bool
	Skills, Tools, ExcludeTools, Extensions, SubagentOnlyExtensions     []string
	McpDirectTools                                                      []string
	OutputPath, OutputMode                                              string
	StructuredOutputSchema                                              any
	ExtensionBindings                                                   *jsObject
}

// projectLaunchBinding is the canonical projection of the resolved inputs handed to the child.
func projectLaunchBinding(in launchBindingInput) map[string]any {
	m := map[string]any{"version": launchBindingProjectionVersion, "definitionDigest": in.DefinitionDigest,
		"inheritProjectContext": in.InheritProjectContext, "inheritGlobalContext": in.InheritGlobalContext, "inheritSkills": in.InheritSkills}
	if in.Task != nil {
		m["taskDigest"] = stableJSONDigest(*in.Task)
	}
	putIf(m, "model", in.Model)
	putIf(m, "fast", in.Fast)
	putIf(m, "thinking", in.Thinking)
	if in.SystemPrompt != nil {
		m["systemPromptDigest"] = stableJSONDigest(*in.SystemPrompt)
	}
	putIf(m, "systemPromptMode", in.SystemPromptMode)
	putIf(m, "skills", in.Skills)
	putIf(m, "tools", in.Tools)
	putIf(m, "excludeTools", in.ExcludeTools)
	putIf(m, "extensions", in.Extensions)
	putIf(m, "subagentOnlyExtensions", in.SubagentOnlyExtensions)
	putIf(m, "mcpDirectTools", in.McpDirectTools)
	putIf(m, "outputPath", in.OutputPath)
	putIf(m, "outputMode", in.OutputMode)
	putIf(m, "structuredOutputSchema", in.StructuredOutputSchema)
	putIf(m, "extensionBindings", in.ExtensionBindings)
	return m
}

func launchBindingDigest(in launchBindingInput) string { return stableJSONDigest(projectLaunchBinding(in)) }

// launchBindingSource is what resolveLaunchBinding assembles a launch identity from.
type launchBindingSource struct {
	Agent                  AgentConfig
	Task                   string
	Model                  string
	Fast                   *bool
	Thinking               string
	SystemPrompt           string
	Skills                 []string
	ToolPlan               *piLaunchToolPlan
	OutputPath, OutputMode string
	StructuredOutputSchema any
	ExtensionBindings      *jsObject
}

type launchBinding struct{ DefinitionDigest, LaunchContractDigest string }

// resolveLaunchBinding assembles launch identity from resolved preflight or execution inputs.
func resolveLaunchBinding(s launchBindingSource) launchBinding {
	def := agentDefinitionDigest(s.Agent)
	task, prompt := s.Task, s.SystemPrompt
	in := launchBindingInput{DefinitionDigest: def, Task: &task, Model: s.Model, Fast: s.Fast, Thinking: s.Thinking, SystemPrompt: &prompt,
		SystemPromptMode: s.Agent.SystemPromptMode, InheritProjectContext: s.Agent.InheritProjectContext, InheritGlobalContext: s.Agent.InheritGlobalContext,
		InheritSkills: s.Agent.InheritSkills, Skills: s.Skills, Tools: s.ToolPlan.EffectiveToolAllowlist, Extensions: s.ToolPlan.ExtensionArgs,
		McpDirectTools: s.ToolPlan.EffectiveMcpTools, OutputPath: s.OutputPath, OutputMode: s.OutputMode, StructuredOutputSchema: s.StructuredOutputSchema,
		ExtensionBindings: s.ExtensionBindings}
	if len(s.ToolPlan.ExcludeTools) > 0 {
		in.ExcludeTools = s.ToolPlan.ExcludeTools
	}
	return launchBinding{DefinitionDigest: def, LaunchContractDigest: launchBindingDigest(in)}
}

// ---- extension-bindings.ts ---------------------------------------------------------------------------------------------------

const (
	maxExtensionBindingNamespaces = 16
	maxExtensionBindingsBytes     = 16 * 1024
	maxExtensionBindingsDepth     = 16
	maxExtensionBindingsProps     = 256
)

var extensionBindingNamespace = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,62})/[1-9][0-9]{0,8}$`)

func canonicalizeBindingJSON(v any, path string, depth int, count *int) (any, error) {
	switch x := v.(type) {
	case nil, string, bool:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("%s must contain only finite JSON numbers.", path)
		}
		return x, nil
	case int:
		return float64(x), nil
	}
	if arr, ok := asArray(v); ok {
		if depth > maxExtensionBindingsDepth {
			return nil, fmt.Errorf("%s exceeds the maximum nesting depth of %d.", path, maxExtensionBindingsDepth)
		}
		out := make([]any, len(arr))
		for i, e := range arr {
			c, err := canonicalizeBindingJSON(e, fmt.Sprintf("%s[%d]", path, i), depth+1, count)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	}
	if obj, ok := asObject(v); ok {
		if depth > maxExtensionBindingsDepth {
			return nil, fmt.Errorf("%s exceeds the maximum nesting depth of %d.", path, maxExtensionBindingsDepth)
		}
		keys := append([]string(nil), obj.order()...)
		sort.Strings(keys)
		out := newObject()
		for _, k := range keys {
			*count++
			if *count > maxExtensionBindingsProps {
				return nil, fmt.Errorf("extensionBindings exceeds %d total properties.", maxExtensionBindingsProps)
			}
			c, err := canonicalizeBindingJSON(obj.vals[k], path+"."+k, depth+1, count)
			if err != nil {
				return nil, err
			}
			out.set(k, c)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s must contain only plain JSON values.", path)
}

// normalizeExtensionBindings validates and canonicalizes per-launch extension metadata; ok is false for an undefined input.
func normalizeExtensionBindings(input any) (value *jsObject, jsonText string, ok bool, err error) {
	if input == nil {
		return nil, "", false, nil
	}
	obj, isObj := asObject(input)
	if !isObj {
		return nil, "", false, fmt.Errorf("extensionBindings must be a plain JSON object.")
	}
	keys := obj.order()
	if len(keys) > maxExtensionBindingNamespaces {
		return nil, "", false, fmt.Errorf("extensionBindings supports at most %d namespaces.", maxExtensionBindingNamespaces)
	}
	for _, k := range keys {
		if !extensionBindingNamespace.MatchString(k) {
			return nil, "", false, fmt.Errorf("extensionBindings namespace '%s' must use a package-like name followed by '/<positive-version>'.", k)
		}
	}
	count := 0
	c, err := canonicalizeBindingJSON(obj, "extensionBindings", 0, &count)
	if err != nil {
		return nil, "", false, err
	}
	canon := c.(*jsObject)
	text := marshalJSON(canon, "")
	if n := len(text); n > maxExtensionBindingsBytes {
		return nil, "", false, fmt.Errorf("extensionBindings canonical JSON is %d bytes; maximum is %d.", n, maxExtensionBindingsBytes)
	}
	return canon, text, true, nil
}

// ---- required-child-extensions.ts --------------------------------------------------------------------------------------------

const (
	maxRequiredExtensions = 32
	maxRequiredPathBytes  = 4096
)

var requiredExtensionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// requiredChildExtension is a host-required extension: a safe id and an importable module path.
type requiredChildExtension struct{ ID, Path string }

// snapshotRequiredChildExtensions validates a snapshot; canonicalize resolves each path to the real file it names (registration
// does it once, retained launches keep that identity).
func snapshotRequiredChildExtensions(value []requiredChildExtension, label string, canonicalize bool) ([]requiredChildExtension, error) {
	if len(value) > maxRequiredExtensions {
		return nil, fmt.Errorf("%s must be an array of at most %d entries.", label, maxRequiredExtensions)
	}
	ids, paths := map[string]bool{}, map[string]bool{}
	out := make([]requiredChildExtension, 0, len(value))
	for i, e := range value {
		if !requiredExtensionIDPattern.MatchString(e.ID) {
			return nil, fmt.Errorf("%s entry %d requires a safe id of at most 128 characters.", label, i)
		}
		if ids[e.ID] {
			return nil, fmt.Errorf("%s id '%s' is duplicated.", label, e.ID)
		}
		if jsTrim(e.Path) == "" || strings.ContainsRune(e.Path, 0) || len(e.Path) > maxRequiredPathBytes || (!canonicalize && !filepath.IsAbs(e.Path)) {
			article := "an absolute"
			if canonicalize {
				article = "a"
			}
			return nil, fmt.Errorf("%s '%s' requires %s non-empty path of at most %d bytes without NUL.", label, e.ID, article, maxRequiredPathBytes)
		}
		p := e.Path
		if canonicalize {
			real, err := filepath.EvalSymlinks(nodeResolve(".", e.Path))
			if err == nil {
				var st os.FileInfo
				if st, err = os.Stat(real); err == nil && !st.Mode().IsRegular() {
					err = fmt.Errorf("not a file")
				}
			}
			if err != nil {
				return nil, fmt.Errorf("%s '%s' is not an importable file: %s", label, e.ID, err.Error())
			}
			p = real
		}
		if paths[p] {
			return nil, fmt.Errorf("%s path '%s' is duplicated.", label, p)
		}
		ids[e.ID], paths[p] = true, true
		out = append(out, requiredChildExtension{ID: e.ID, Path: p})
	}
	return out, nil
}

var (
	requiredExtMu        sync.Mutex
	requiredExtBySession = map[string]*requiredRegistration{}
)

// requiredRegistration is one registered snapshot; a disposer removes only its own.
type requiredRegistration struct{ extensions []requiredChildExtension }

// registerRequiredChildExtensions registers one immutable host-required extension snapshot for a parent session and returns its
// disposer. The registry lives in this extension's process: another extension cannot reach it.
func registerRequiredChildExtensions(sessionID string, extensions []requiredChildExtension) (dispose func(), err error) {
	if sessionID == "" || jsTrim(sessionID) != sessionID || len(sessionID) > 256 || strings.ContainsRune(sessionID, 0) {
		return nil, fmt.Errorf("Required child extension registration requires a non-empty trimmed sessionId of at most 256 characters without NUL.")
	}
	frozen, err := snapshotRequiredChildExtensions(extensions, "Required child extensions", true)
	if err != nil {
		return nil, err
	}
	requiredExtMu.Lock()
	defer requiredExtMu.Unlock()
	if _, has := requiredExtBySession[sessionID]; has {
		return nil, fmt.Errorf("Required child extensions are already registered for session '%s'; dispose them first.", sessionID)
	}
	reg := &requiredRegistration{extensions: frozen}
	requiredExtBySession[sessionID] = reg
	return func() {
		requiredExtMu.Lock()
		defer requiredExtMu.Unlock()
		if requiredExtBySession[sessionID] == reg {
			delete(requiredExtBySession, sessionID)
		}
	}, nil
}

func resolveRequiredChildExtensions(sessionID string) []requiredChildExtension {
	requiredExtMu.Lock()
	defer requiredExtMu.Unlock()
	if reg := requiredExtBySession[sessionID]; reg != nil {
		return reg.extensions
	}
	return nil
}

// ---- intercom-bridge.ts ------------------------------------------------------------------------------------------------------

const (
	nativeIntercomExtensionDir = "native:pi-subagents-supervisor-channel"
	intercomBridgeMarker       = "Intercom orchestration channel:"
	orchestratorTargetToken    = "{orchestratorTarget}"
)

// The default template stays session-independent: the child reads the supervisor target from its runtime config, and a prompt
// that names the parent session would make the launch digest vary per session.
const defaultIntercomBridgeTemplate = `The inherited thread is reference-only. Do not continue that conversation or send questions, status updates, or completion handoffs to the supervisor in normal assistant text.

Use contact_supervisor first. It resolves the supervisor session and run metadata automatically.
- Need a decision, blocked, approval, or product/API/scope ambiguity: contact_supervisor({ reason: "need_decision", message: "<question>" })
- Need structured supervisor input rather than a freeform reply: contact_supervisor({ reason: "interview_request", message: "<what input is needed>", interview: { title: "...", questions: [] } })
- After contact_supervisor with reason "need_decision" or "interview_request", stay alive and continue only after the reply arrives. Do not finish your final response with a choose-one question.
- Do not ask for clarification when the only conflict is review-only/no-edit versus progress-writing or artifact-writing instructions. If an output path is configured but no write-capable tool is available, return the complete artifact in your final response; the runtime will persist it. Do not contact the supervisor merely because you cannot write that output path directly.
- Meaningful progress or unexpected discoveries that change the plan: contact_supervisor({ reason: "progress_update", message: "UPDATE: <summary>" })

Do not use contact_supervisor for routine completion handoffs. If no coordination is needed, return a focused task result.`

// intercomBridgeConfig is the original's IntercomBridgeConfig; nil fields are unset.
type intercomBridgeConfig struct {
	Mode            string
	InstructionFile *string
	ResultDelivery  *bool
}

type intercomBridgeState struct {
	Active                         bool
	Mode                           string
	ResultDelivery                 bool
	OrchestratorTarget             string
	ExtensionDir                   string
	Instruction                    string
	InterpolatesOrchestratorTarget bool
}

// validateIntercomBridgeConfig validates untrusted bridge config from descriptors or delegation requests; label prefixes each error.
func validateIntercomBridgeConfig(value any, label string) (intercomBridgeConfig, string) {
	obj, ok := asObject(value)
	if !ok {
		return intercomBridgeConfig{}, label + " must be an object."
	}
	for _, f := range obj.order() {
		if f != "mode" && f != "instructionFile" && f != "resultDelivery" {
			return intercomBridgeConfig{}, fmt.Sprintf("%s.%s is not supported.", label, f)
		}
	}
	var cfg intercomBridgeConfig
	if v, has := obj.vals["mode"]; has {
		m, _ := v.(string)
		if m != "off" && m != "fork-only" && m != "always" {
			return cfg, label + ".mode is invalid."
		}
		cfg.Mode = m
	}
	if v, has := obj.vals["instructionFile"]; has {
		s, isStr := v.(string)
		if !isStr {
			return cfg, label + ".instructionFile must be a string."
		}
		cfg.InstructionFile = &s
	}
	if v, has := obj.vals["resultDelivery"]; has {
		b, isBool := v.(bool)
		if !isBool {
			return cfg, label + ".resultDelivery must be a boolean."
		}
		cfg.ResultDelivery = &b
	}
	return cfg, ""
}

func resolveIntercomBridgeMode(v any) string {
	if s, _ := v.(string); s == "off" || s == "always" || s == "fork-only" {
		return s
	}
	return "always"
}

// intercomBridgeOf is the resolved config: the override replaces the global config when supplied.
func intercomBridgeOf(global any, override *intercomBridgeConfig) (mode, instructionFile string, resultDelivery bool) {
	if override != nil {
		mode = resolveIntercomBridgeMode(nilIfEmpty(override.Mode))
		if override.InstructionFile != nil {
			instructionFile = *override.InstructionFile
		}
		return mode, instructionFile, override.ResultDelivery != nil && *override.ResultDelivery
	}
	obj, ok := asObject(global)
	if !ok {
		return "always", "", false
	}
	mode = resolveIntercomBridgeMode(obj.vals["mode"])
	instructionFile, _ = obj.vals["instructionFile"].(string)
	return mode, instructionFile, obj.vals["resultDelivery"] == true
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func resolveInstructionTemplate(instructionFile, settingsDir string) string {
	if instructionFile == "" {
		return defaultIntercomBridgeTemplate
	}
	p := instructionFile
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(homeDir(), p[2:])
	}
	if !filepath.IsAbs(p) {
		p = nodeResolve(settingsDir, p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return defaultIntercomBridgeTemplate // the original warns and uses the default instructions
	}
	return string(b)
}

func buildIntercomBridgeInstruction(target, template string) string {
	instruction := jsTrim(strings.ReplaceAll(template, orchestratorTargetToken, target))
	if strings.HasPrefix(instruction, intercomBridgeMarker) {
		return instruction
	}
	return intercomBridgeMarker + "\n" + instruction
}

func intercomInactiveReason(mode, context, target string) string {
	switch {
	case mode == "off":
		return "bridge mode is off"
	case mode == "fork-only" && context != "fork":
		return "bridge mode is fork-only and context is not fork"
	case target == "":
		return "orchestrator target is not available"
	}
	return ""
}

// resolveIntercomBridge resolves whether the bridge is active for a launch and the instruction it adds to the child's prompt.
func resolveIntercomBridge(global any, override *intercomBridgeConfig, context, orchestratorTarget string) intercomBridgeState {
	mode, instructionFile, resultDelivery := intercomBridgeOf(global, override)
	target := jsTrim(orchestratorTarget)
	if intercomInactiveReason(mode, context, target) != "" || target == "" {
		return intercomBridgeState{Mode: mode, ResultDelivery: resultDelivery, ExtensionDir: nativeIntercomExtensionDir,
			Instruction: buildIntercomBridgeInstruction(orchestratorTargetToken, defaultIntercomBridgeTemplate)}
	}
	template := resolveInstructionTemplate(instructionFile, filepath.Join(agentDir(), "extensions", "subagent"))
	return intercomBridgeState{Active: true, Mode: mode, ResultDelivery: resultDelivery, OrchestratorTarget: target, ExtensionDir: nativeIntercomExtensionDir,
		Instruction: buildIntercomBridgeInstruction(target, template), InterpolatesOrchestratorTarget: strings.Contains(template, orchestratorTargetToken)}
}

// applyIntercomBridgeToAgent rewrites the launch prompt and tools for an active bridge. The parsed definition digest is captured
// first so launch identity keeps describing the agent file rather than this runtime overlay.
func applyIntercomBridgeToAgent(a AgentConfig, b intercomBridgeState) AgentConfig {
	if !b.Active || b.OrchestratorTarget == "" {
		return a
	}
	tools := a.Tools
	if len(a.Tools) > 0 && !contains(a.Tools, "contact_supervisor") {
		tools = append(append([]string{}, a.Tools...), "contact_supervisor")
	}
	prompt := jsTrim(a.SystemPrompt)
	switch {
	case strings.Contains(prompt, intercomBridgeMarker):
	case prompt != "":
		prompt = prompt + "\n\n" + b.Instruction
	default:
		prompt = b.Instruction
	}
	if len(tools) == len(a.Tools) && prompt == a.SystemPrompt {
		return a
	}
	out := a
	out.DefinitionDigest = agentDefinitionDigest(a)
	out.Tools = tools
	out.SystemPrompt = prompt
	return out
}

// ---- fork-context.ts: launch context ------------------------------------------------------------------------------------------

// resolveSubagentLaunchContext resolves the actual launch context from explicit, global, and agent preferences ("" is unset).
func resolveSubagentLaunchContext(explicit, agentDefault, defaultSubagent string, canUseImplicitFork bool) string {
	if explicit != "" {
		return explicit
	}
	preferred := defaultSubagent
	if preferred == "" {
		preferred = agentDefault
	}
	if preferred == "" {
		preferred = "fresh"
	}
	if preferred == "fork" && canUseImplicitFork {
		return "fork"
	}
	return "fresh"
}

// canPreferForkFromSnapshot is true when an implicit `defaultContext: fork` can create a real branch now (a persisted parent
// session file and a current leaf).
func canPreferForkFromSnapshot(parentSessionFile, leafID string) bool {
	if parentSessionFile == "" || leafID == "" {
		return false
	}
	_, err := os.Stat(parentSessionFile)
	return err == nil
}

// ---- child-launch-plan.ts: resolveStepBehavior ---------------------------------------------------------------------------------

// stepBehavior is the original's ResolvedStepBehavior as far as launch identity reads it. Output is a string path or false.
type stepBehavior struct {
	Output       any // string, or false
	OutputMode   string
	Skills       []string
	SkillsFalse  bool
	Model        string
	Fast         *bool
	OutputSchema any // nil: none
}

// stepOverrides are the per-call overrides of resolveStepBehavior; an unset field is "" or nil.
type stepOverrides struct {
	Output       any // nil (unset), a string path or false
	OutputMode   string
	Skills       SkillInput
	Model        string
	OutputSchema any // nil: unset; false: disabled; a schema object
}

// normalizeOutputOverride is child-launch-plan.ts:58: false and "false" disable output, true and "true" and anything but a
// non-empty string leave it unset (nil).
func normalizeOutputOverride(output any) any {
	switch v := output.(type) {
	case bool:
		if !v {
			return false
		}
	case string:
		switch v {
		case "false":
			return false
		case "true", "":
			return nil
		}
		return v
	}
	return nil
}

// resolveEffectiveOutputSchema is child-launch-plan.ts:64.
func resolveEffectiveOutputSchema(a AgentConfig, override any) any {
	if b, ok := override.(bool); ok && !b {
		return nil
	}
	if !isUndefined(override) {
		return override
	}
	if isUndefined(a.OutputSchema) {
		return nil
	}
	return a.OutputSchema
}

// resolveStepBehavior is child-launch-plan.ts:72 (no chain skills: preflight and the single run pass none).
func resolveStepBehavior(a AgentConfig, o stepOverrides) stepBehavior {
	b := stepBehavior{}
	if out := normalizeOutputOverride(o.Output); out != nil {
		b.Output = out
	} else if out := normalizeOutputOverride(a.Output); out != nil {
		b.Output = out
	} else {
		b.Output = false
	}
	switch {
	case o.Skills.Set && o.Skills.False:
		b.SkillsFalse = true
	case o.Skills.Set:
		b.Skills = append([]string{}, o.Skills.Items...)
	default:
		b.Skills = append([]string{}, a.Skills...)
	}
	b.OutputMode = o.OutputMode
	if b.OutputMode == "" {
		b.OutputMode = a.OutputMode
	}
	if b.OutputMode == "" {
		b.OutputMode = "inline"
	}
	b.Model = o.Model
	if b.Model == "" {
		b.Model = a.Model
	}
	b.Fast = a.Fast
	b.OutputSchema = resolveEffectiveOutputSchema(a, o.OutputSchema)
	return b
}
