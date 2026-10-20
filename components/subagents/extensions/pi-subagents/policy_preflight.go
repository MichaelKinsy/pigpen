package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Port of src/api/preflight.ts: the launch contract of a single-agent launch, resolved without launching anything or creating
// any directory. The original exports it as `pi-subagents/preflight`; here it is the in-package function the tests and the
// execution paths share.

// subagentLaunchContractVersion: v3 reports the resolved Intercom bridge state and binds its prompt and tools into
// launchContractDigest, matching execution.
const subagentLaunchContractVersion = 3

// originalPackageVersion is the version of the original package this port follows; protocol.packageVersion reports it.
const originalPackageVersion = "0.73.1"

// preflightOrchestratorTarget stands in for the parent session target when the host does not supply one; only custom
// templates that name the session read it.
const preflightOrchestratorTarget = "preflight"

type launchContractDiagnostic struct{ Code, Severity, Message string }

func (d launchContractDiagnostic) tree() map[string]any {
	return map[string]any{"code": d.Code, "severity": d.Severity, "message": d.Message}
}

// launchContractInput is SubagentLaunchContractInput. A nil pointer, nil slice, nil interface or empty string is "undefined".
type launchContractInput struct {
	Agent, Cwd, Task string
	AgentScope       AgentScope
	Context          *string
	Model            string
	Fast             *bool
	Thinking         any // nil, false or a level
	ThinkingCeiling, InheritedThinkingCeiling string
	ParentModel        *ParentModel
	AvailableModels    []ModelInfo // nil: no host registry snapshot
	PreferredProvider  string
	Skill              any
	Output             any
	OutputMode         string
	OutputSchema       any // nil, false or a schema object
	ExtensionBindings  any
	Artifacts          *bool
	ArtifactDir        string
	ParentSessionFile  string
	ParentSessionID    string
	ParentLeafID       string
	SessionRoot        string
	SessionDir         string
	RunID              string
	NestedRootRunID    string
	CapabilityCeiling  *resolvedCeiling
	InheritedCeiling   *resolvedCeiling
	IntercomBridge     any // nil, or the per-launch config to validate
	OrchestratorTarget any // nil, or the value to validate
}

type launchContractCandidate struct {
	Name, LocalName, PackageName string
	Source                       AgentSource
	FilePath                     string
	Disabled, Selected           bool
}

type launchContractAgent struct {
	Name, LocalName, PackageName string
	Source                       AgentSource
	FilePath                     string
	DefinitionProjectionVersion  int
	DefinitionDigest             string
	ShadowedCandidates           []launchContractCandidate
}

type launchContractSkill struct{ Name, Path, Source string }

type launchContractSkills struct {
	Requested []string
	Resolved  []launchContractSkill
	Missing   []string
}

type launchContractTools struct {
	RequestedBuiltin, DeclaredBuiltin []string
	ExcludeTools                      []string // nil: absent
	EffectiveAllowlist                []string
	ExplicitAllowlist                 bool
	RequiredChildTools, InternalTools []string
	Mcp                               []resolvedMcpSelection
	EffectiveMcpTools                 []string
	ToolExtensionPaths                []string
	RuntimeExtensions                 []string
	ConfiguredExtensions              []string
	RequiredExtensionIDs              []string
	ExtensionArgs                     []string
	DisableAmbientExtensions          bool
	FanoutAuthorized                  bool
	CapabilityCeiling                 *resolvedCeiling
	CapabilityAudit                   *capabilityAudit
}

type launchContractLifecycle struct{ AsyncDir, ResultPath, StatusPath, EventsPath, ProcessTerminalPath, ProcessTerminalCandidatePath string }

type launchContractRoots struct {
	Cwd, SessionRoot, SessionDir, SessionFile, ArtifactsDir string
	ArtifactPaths                                           *artifactPaths
	OutputPath                                              string
	Lifecycle                                               launchContractLifecycle
}

// launchContract is SubagentLaunchContract.
type launchContract struct {
	Version                                                       int
	RunID                                                         string
	Agent                                                         launchContractAgent
	Context, Model, Thinking, ThinkingCeiling                     string
	SystemPromptMode                                              string
	InheritProjectContext, InheritGlobalContext, InheritSkills    bool
	Skills                                                        launchContractSkills
	Tools                                                         launchContractTools
	BridgeActive                                                  bool
	BridgeMode                                                    string
	Roots                                                         launchContractRoots
	PackageVersion                                                string
	Diagnostics                                                   []launchContractDiagnostic
	LaunchContractDigest, Digest                                  string
}

// launchContractResult is SubagentLaunchContractResult.
type launchContractResult struct {
	OK          bool
	Contract    *launchContract
	Code        string
	Message     string
	Diagnostics []launchContractDiagnostic
}

func launchFail(code, message string, diagnostics []launchContractDiagnostic) launchContractResult {
	return launchContractResult{Code: code, Message: message, Diagnostics: diagnostics}
}

var (
	wsRunRe        = regexp.MustCompile(`[\t-\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
	createsPkgRe1  = regexp.MustCompile(`(?i)\b(?:add|create|introduce|make|set up)\b.{0,80}\b(?:new\s+)?(?:workspace\s+)?package\b`)
	createsPkgRe2  = regexp.MustCompile(`(?i)\b(?:new\s+)?package\b.{0,80}\b(?:workspace|monorepo)\b`)
	pkgOnlyAuthRe1 = regexp.MustCompile(`(?i)\b(?:only|solely)\b.{0,40}\b(?:edit|change|modify|touch|write(?:\s+to)?)\b.{0,80}\b(?:package(?:\s+directory)?|packages/[\w.-]+)\b`)
	pkgOnlyAuthRe2 = regexp.MustCompile(`(?i)\b(?:do not|don't|must not)\b.{0,40}\b(?:edit|change|modify|touch|write(?:\s+to)?)\b.{0,80}\b(?:root|workspace|lockfile|metadata)\b`)
	pkgOnlyAuthRe3 = regexp.MustCompile(`(?i)\bwithout\b.{0,40}\b(?:root|workspace|lockfile|metadata)\b.{0,40}\b(?:edit|change|modification|write)s?\b`)
)

func taskWorkspaceScopeAuthorityDiagnostic(task string) *launchContractDiagnostic {
	if task == "" {
		return nil
	}
	text := jsTrim(wsRunRe.ReplaceAllString(task, " "))
	if text == "" {
		return nil
	}
	if !createsPkgRe1.MatchString(text) && !createsPkgRe2.MatchString(text) {
		return nil
	}
	if !pkgOnlyAuthRe1.MatchString(text) && !pkgOnlyAuthRe2.MatchString(text) && !pkgOnlyAuthRe3.MatchString(text) {
		return nil
	}
	return &launchContractDiagnostic{Code: "workspace_scope_authority", Severity: "warning",
		Message: "Task asks for a workspace package change while limiting authority to package-scope edits. New workspace packages often need root workspace metadata or lockfile changes, so confirm that authority before launch."}
}

func launchContractCandidates(inputAgent string, selected *AgentConfig, all DiscoveryAll) []launchContractCandidate {
	var out []launchContractCandidate
	for _, group := range [][]AgentConfig{all.Builtin, all.Package, all.User, all.Project} {
		for i := range group {
			a := group[i]
			if m, _ := ResolveAgentName(inputAgent, []AgentConfig{a}); m == nil {
				continue
			}
			c := launchContractCandidate{Name: a.Name, LocalName: a.LocalName, PackageName: a.PackageName, Source: a.Source, FilePath: a.FilePath,
				Disabled: a.Disabled != nil && *a.Disabled}
			c.Selected = selected != nil && a.FilePath == selected.FilePath && a.Name == selected.Name
			out = append(out, c)
		}
	}
	return out
}

// formatUnknownAgentError renders local discovery evidence without exposing filesystem error details.
func formatUnknownAgentError(name string, d Discovery) string {
	dirs := []string{}
	for _, dir := range d.Directories {
		var state string
		switch dir.State {
		case "candidates":
			state = fmt.Sprintf("%d candidate", dir.CandidateCount)
			if dir.CandidateCount != 1 {
				state += "s"
			}
		case "empty":
			state = "present but empty"
		case "not-directory":
			state = "not a directory"
		default:
			state = dir.State
		}
		dirs = append(dirs, fmt.Sprintf("- %s: %s (%s)", dir.Source, dir.Path, state))
	}
	agents := append([]AgentConfig{}, d.Agents...)
	sort.SliceStable(agents, func(i, j int) bool {
		if agents[i].Name != agents[j].Name {
			return localeLess(agents[i].Name, agents[j].Name)
		}
		return localeLess(string(agents[i].Source), string(agents[j].Source))
	})
	lines := []string{"Unknown agent: " + name, "Effective cwd: " + nodeResolve(d.Cwd), "Consulted agent-definition directories:"}
	if len(dirs) == 0 {
		dirs = []string{"- (none)"}
	}
	lines = append(lines, dirs...)
	lines = append(lines, "Discovered agents:")
	if len(agents) == 0 {
		lines = append(lines, "- (none)")
	}
	for _, a := range agents {
		lines = append(lines, fmt.Sprintf("- %s (%s)", a.Name, a.Source))
	}
	return strings.Join(lines, "\n")
}

// resolveLaunchContractContext resolves the launch context from the explicit, global and agent preferences.
func resolveLaunchContractContext(in launchContractInput, a AgentConfig, cfg extensionConfig) string {
	explicit := ""
	if in.Context != nil {
		explicit = *in.Context
	}
	return resolveSubagentLaunchContext(explicit, a.DefaultContext, cfg.DefaultSubagentContext, canPreferForkFromSnapshot(in.ParentSessionFile, in.ParentLeafID))
}

func withSliceDefault(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// resolveSubagentLaunchContract resolves the launch contract of one agent. An error is what the original throws: a model that
// the host registry does not know, or a malformed settings file.
func resolveSubagentLaunchContract(in launchContractInput) (launchContractResult, error) {
	diagnostics := []launchContractDiagnostic{}
	if d := taskWorkspaceScopeAuthorityDiagnostic(in.Task); d != nil {
		diagnostics = append(diagnostics, *d)
	}
	effectiveCwd := nodeResolve(in.Cwd)
	st, err := os.Stat(effectiveCwd)
	switch {
	case err != nil:
		return launchFail("invalid_cwd", fmt.Sprintf("cwd '%s' is not a directory. %s", effectiveCwd, nodeFSErrorText(err, "stat", effectiveCwd)), diagnostics), nil
	case !st.IsDir():
		return launchFail("invalid_cwd", fmt.Sprintf("cwd '%s' is not a directory.", effectiveCwd), diagnostics), nil
	}
	if in.Context != nil && *in.Context != "fresh" && *in.Context != "fork" {
		return launchFail("unsupported_mode", fmt.Sprintf("Unsupported context '%s'; expected 'fresh' or 'fork'.", *in.Context), diagnostics), nil
	}
	if in.ArtifactDir != "" && in.ArtifactDir != "project" && in.ArtifactDir != "session" && in.ArtifactDir != "temp" {
		return launchFail("invalid_artifact_dir", fmt.Sprintf("Unsupported artifactDir '%s'; expected 'project', 'session', or 'temp'.", in.ArtifactDir), diagnostics), nil
	}
	var bridgeOverride *intercomBridgeConfig
	if in.IntercomBridge != nil {
		cfg, msg := validateIntercomBridgeConfig(in.IntercomBridge, "intercomBridge")
		if msg != "" {
			return launchFail("invalid_intercom_bridge", msg, diagnostics), nil
		}
		bridgeOverride = &cfg
	}
	// Execution always derives a non-empty target, so an empty one here would silently deactivate the bridge and break parity
	// instead of proving it.
	orchestratorTarget := ""
	if in.OrchestratorTarget != nil {
		s, ok := in.OrchestratorTarget.(string)
		if !ok || jsTrim(s) == "" {
			return launchFail("invalid_intercom_bridge", "orchestratorTarget must be a non-empty string when provided.", diagnostics), nil
		}
		orchestratorTarget = s
	}
	cfg := loadExtensionConfig()
	var scopeInput any
	if in.AgentScope != "" {
		scopeInput = string(in.AgentScope)
	}
	scope := ResolveExecutionAgentScope(scopeInput)
	parentProvider := in.PreferredProvider
	if parentProvider == "" && in.ParentModel != nil {
		parentProvider = in.ParentModel.Provider
	}
	discovered, all, err := discoverAgentSnapshot(effectiveCwd, scope, parentProvider, DiscoveryOptions{NoChains: true})
	if err != nil {
		return launchContractResult{}, err
	}
	resolved, resolveErr := ResolveAgentName(in.Agent, discovered.Agents)
	var candidates []AgentConfig
	if resolveErr != "" {
		for i := range discovered.Agents {
			if m, _ := ResolveAgentName(in.Agent, discovered.Agents[i:i+1]); m != nil {
				candidates = append(candidates, discovered.Agents[i])
			}
		}
	} else if resolved != nil {
		candidates = []AgentConfig{*resolved}
	}
	if diag := blockingDiagnostic(in.Agent, candidates, discovered.Diagnostics); diag != nil {
		message := fmt.Sprintf("Agent '%s' has invalid configuration: %s", in.Agent, diag.Error)
		return launchFail("missing_agent", message, []launchContractDiagnostic{{Code: "missing_agent", Severity: "error", Message: message}}), nil
	}
	if resolveErr != "" {
		return launchFail("ambiguous_agent", resolveErr, diagnostics), nil
	}
	if resolved == nil {
		return launchFail("missing_agent", formatUnknownAgentError(in.Agent, discovered), diagnostics), nil
	}
	definitionAgent := *resolved
	var bindings *jsObject
	var bindingsText string
	if in.ExtensionBindings != nil {
		var set bool
		bindings, bindingsText, set, err = normalizeExtensionBindings(in.ExtensionBindings)
		_ = bindingsText
		if err != nil {
			return launchFail("invalid_extension_bindings", err.Error(), diagnostics), nil
		}
		if !set {
			bindings = nil
		}
	}
	if bindings != nil && definitionAgent.Runner != nil && (definitionAgent.Runner.Type == "external-cli" || definitionAgent.Runner.Type == "external-job") {
		return launchFail("unsupported_mode", fmt.Sprintf("extensionBindings is not supported for runner.type='%s'.", definitionAgent.Runner.Type), diagnostics), nil
	}
	context := resolveLaunchContractContext(in, definitionAgent, cfg)
	if context == "fork" {
		diagnostics = append(diagnostics, launchContractDiagnostic{"host_required", "host-required", "Exact fork session branching requires Pi host session snapshots."})
	}
	// Execution rewrites the discovered agent through the bridge before any other launch resolution, so preflight must hash the
	// same rewritten agent.
	target := orchestratorTarget
	if target == "" {
		target = preflightOrchestratorTarget
	}
	bridge := resolveIntercomBridge(cfg.IntercomBridge, bridgeOverride, context, target)
	if bridge.Active && bridge.InterpolatesOrchestratorTarget && orchestratorTarget == "" {
		diagnostics = append(diagnostics, launchContractDiagnostic{"host_required", "host-required", "The intercomBridge instruction file names the supervisor session; supply orchestratorTarget to bind the exact child prompt."})
	}
	agent := applyIntercomBridgeToAgent(definitionAgent, bridge)
	effectiveCeiling := intersectSubagentCapabilityCeilings(in.CapabilityCeiling, in.InheritedCeiling)
	if msg := capabilityCeilingAgentRestrictionMessage(agent.Name, effectiveCeiling); msg != "" {
		return launchFail("restricted_agent", msg, diagnostics), nil
	}
	runID := in.RunID
	if runID == "" {
		runID = "preflight"
	}
	skillInput := normalizeSkillInput(in.Skill)
	overrides := stepOverrides{Output: in.Output, OutputMode: in.OutputMode, Skills: skillInput, Model: in.Model, OutputSchema: in.OutputSchema}
	if out, disabled, ok := normalizeSingleOutputOverride(in.Output, agent.Output, agent.Output != ""); ok {
		if disabled {
			overrides.Output = false
		} else {
			overrides.Output = out
		}
	} else {
		overrides.Output = nil
	}
	behavior := resolveStepBehavior(agent, overrides)
	requestedSkills := behavior.Skills
	if behavior.SkillsFalse || requestedSkills == nil {
		requestedSkills = []string{}
	}
	agentDirOfFile := effectiveCwd
	if agent.FilePath != "" {
		agentDirOfFile = filepath.Dir(agent.FilePath)
	}
	resolvedSkills, missing, err := resolveSkillsWithFallback(requestedSkills, effectiveCwd, effectiveCwd, agent.SkillPath, agentDirOfFile)
	if err != nil {
		return launchContractResult{}, err
	}
	if contains(missing, "pi-subagents") {
		return launchFail("missing_skill", "The pi-subagents orchestration skill is not child-injectable.", diagnostics), nil
	}
	if len(missing) > 0 {
		diagnostics = append(diagnostics, launchContractDiagnostic{"missing_skill", "error", "Missing skills: " + strings.Join(missing, ", ")})
	}
	runnerType := ""
	if agent.Runner != nil {
		runnerType = agent.Runner.Type
	}
	externalRunner := runnerType == "external-cli" || runnerType == "external-job"
	if externalRunner && !isUndefined(behavior.OutputSchema) {
		return launchFail("unsupported_mode", fmt.Sprintf("Agent '%s' uses runner.type='%s' and does not support: structured output.", agent.Name, runnerType), diagnostics), nil
	}
	available := make([]ModelInfo, len(in.AvailableModels))
	for i, m := range in.AvailableModels {
		if m.FullID == "" {
			m.FullID = m.Provider + "/" + m.ID
		}
		available[i] = m
	}
	preferredProvider := agent.ModelProvider
	if preferredProvider == "" {
		preferredProvider = parentProvider
	}
	modelScopes := resolveModelScopesForAgent(discovered.ModelScope, agent.Name, in.ParentModel)
	var explicitModel, agentModel any
	if in.Model != "" {
		explicitModel = in.Model
	}
	if agent.Model != "" {
		agentModel = agent.Model
	}
	modelOrigin := resolveModelOrigin(explicitModel, agentModel, in.ParentModel, false, "")
	thinkingConfig := in.Thinking
	if thinkingConfig == nil {
		thinkingConfig = agent.Thinking
	}
	model, thinkingCeiling := "", ""
	if !externalRunner {
		source := "inherited"
		if modelOrigin == "explicit" {
			source = "explicit"
		}
		primary, err := resolveEffectiveSubagentModel(explicitModel, agentModel, in.ParentModel, available, preferredProvider, &ResolveSubagentModelOverrideOptions{Scope: modelScopes, Source: source})
		if err != nil {
			return launchContractResult{}, err
		}
		if thinkingCeiling, err = intersectThinkingCeilings(discovered.MaxThinking, in.ThinkingCeiling, in.InheritedThinkingCeiling); err != nil {
			return launchContractResult{}, err
		}
		selection, err := resolveModelSelection(primary, available, preferredProvider, &ResolveModelSelectionOptions{Scope: modelScopes,
			PrimaryModelFromParent: modelOrigin == "inherited" || inheritsParentModel(explicitModel, agentModel, in.ParentModel), Origin: modelOrigin})
		if err != nil {
			return launchContractResult{}, err
		}
		model = applyThinkingSuffix(selection.Model, thinkingConfig, in.Thinking != nil)
		if err := assertThinkingWithinCeiling(ThinkingCeilingCheck{Model: model, ConfigThinking: thinkingConfig, Ceiling: thinkingCeiling, Agent: agent.Name, RunID: runID}); err != nil {
			diagnostics = append(diagnostics, launchContractDiagnostic{"thinking_ceiling", "error", err.Error()})
			return launchFail("thinking_ceiling", err.Error(), diagnostics), nil
		}
	}
	var rules permissionRules
	if !isUndefined(agent.Permissions) {
		if rules, err = validatePermissionRules(agent.Permissions, "Agent permissions"); err != nil {
			return launchContractResult{}, err
		}
	}
	var globalPerms *permissionConfig
	if !isUndefined(cfg.Permissions) {
		if globalPerms, err = validatePermissionConfig(cfg.Permissions, "config.permissions"); err != nil {
			return launchContractResult{}, err
		}
	}
	permissionRulesOf := resolvePermissionRules(globalPerms, rules)
	fast := agent.Fast
	if in.Fast != nil {
		fast = in.Fast
	}
	var requiredExtensions []requiredChildExtension
	if !externalRunner {
		requiredExtensions = resolveRequiredChildExtensions(in.ParentSessionID)
	}
	toolPlan, err := resolvePiLaunchToolPlan(piLaunchToolPlanInput{
		Tools: agent.Tools, ExcludeTools: agent.ExcludeTools, AllowNestedSubagents: agent.AllowNestedSubagents, Extensions: agent.Extensions,
		SubagentOnlyExtensions: agent.SubagentOnlyExtensions, RequiredExtensions: requiredExtensions, McpDirectTools: agent.McpDirectTools, Cwd: effectiveCwd,
		RequireReadTool: len(resolvedSkills) > 0, StructuredOutput: !isUndefined(behavior.OutputSchema), Fast: fast, Model: model, CapabilityCeiling: effectiveCeiling,
		AgentName: agent.Name, PermissionRules: permissionRulesOf,
	})
	if err != nil {
		diagnostics = append(diagnostics, launchContractDiagnostic{"denied_required_tool", "error", err.Error()})
		return launchFail("denied_required_tool", err.Error(), diagnostics), nil
	}
	artifactsDir := ""
	var artifactPathsOut *artifactPaths
	if in.Artifacts == nil || *in.Artifacts {
		if artifactsDir, err = getArtifactsDir(in.ParentSessionFile, effectiveCwd, in.ArtifactDir); err != nil {
			return launchContractResult{}, err
		}
		zero := 0
		p := getArtifactPaths(artifactsDir, runID, agent.Name, &zero)
		artifactPathsOut = &p
	}
	relativeBase := ""
	if artifactsDir != "" {
		relativeBase = filepath.Join(artifactsDir, "outputs", runID)
	}
	outputPath := resolveSingleOutputPath(behavior.Output, effectiveCwd, effectiveCwd, relativeBase)
	// An explicit sessionDir is a root keyed by the child run id, matching the sibling sessionRoot derivation; hosts omitting
	// runId get the documented deterministic "preflight" placeholder.
	sessionRoot := ""
	switch {
	case in.SessionDir != "":
		sessionRoot = filepath.Join(nodeResolve(in.SessionDir), runID)
	case in.SessionRoot != "":
		sessionRoot = filepath.Join(nodeResolve(in.SessionRoot), runID)
	}
	sessionDir := ""
	if sessionRoot != "" {
		sessionDir = filepath.Join(sessionRoot, "run-0")
	}
	var lifecycleAsyncDir, lifecycleResultPath string
	if in.NestedRootRunID != "" {
		for _, id := range [][2]string{{"rootRunId", in.NestedRootRunID}, {"id", runID}} {
			if !isSafeNestedID(id[1]) {
				return launchContractResult{}, fmt.Errorf("%s must be a non-empty safe id token.", id[0])
			}
		}
		lifecycleAsyncDir = filepath.Join(tempRootDir(), "nested-subagent-runs", in.NestedRootRunID, runID)
		lifecycleResultPath = filepath.Join(DIRS.Results, "nested", in.NestedRootRunID, runID+".json")
	} else {
		lifecycleAsyncDir = filepath.Join(DIRS.Async, runID)
		lifecycleResultPath = resultFilePath(DIRS.Results, runID)
	}
	if sessionDir == "" {
		diagnostics = append(diagnostics, launchContractDiagnostic{"host_required", "host-required", "No sessionRoot/sessionDir was supplied; exact child session paths require the Pi host session-root policy."})
	}
	if !externalRunner && in.AvailableModels == nil && (in.Model != "" || agent.Model != "" || in.ParentModel != nil) {
		diagnostics = append(diagnostics, launchContractDiagnostic{"host_required", "host-required", "No availableModels snapshot was supplied; model resolution may differ from the active Pi host registry."})
	}
	if len(missing) > 0 {
		return launchFail("missing_skill", "Missing skills: "+strings.Join(missing, ", "), diagnostics), nil
	}
	effectiveThinking := resolveEffectiveThinking(model, thinkingConfig)
	task := in.Task
	binding := resolveLaunchBinding(launchBindingSource{
		Agent: agent, Task: task, Model: model, Fast: fast, Thinking: effectiveThinking,
		SystemPrompt: buildEffectiveSystemPrompt(agent, resolvedSkills, effectiveCwd, outputPath), Skills: requestedSkills, ToolPlan: toolPlan,
		OutputPath: outputPath, OutputMode: behavior.OutputMode, StructuredOutputSchema: behavior.OutputSchema, ExtensionBindings: bindings,
	})
	var shadowed []launchContractCandidate
	for _, c := range launchContractCandidates(in.Agent, &agent, all) {
		if !c.Selected {
			shadowed = append(shadowed, c)
		}
	}
	contract := &launchContract{
		Version: subagentLaunchContractVersion, RunID: runID,
		Agent: launchContractAgent{Name: agent.Name, LocalName: agent.LocalName, PackageName: agent.PackageName, Source: agent.Source, FilePath: agent.FilePath,
			DefinitionProjectionVersion: agentDefinitionProjectionVersion, DefinitionDigest: binding.DefinitionDigest, ShadowedCandidates: shadowed},
		Context: context, Model: model, Thinking: effectiveThinking, ThinkingCeiling: thinkingCeiling,
		SystemPromptMode: agent.SystemPromptMode, InheritProjectContext: agent.InheritProjectContext, InheritGlobalContext: agent.InheritGlobalContext, InheritSkills: agent.InheritSkills,
		Skills: launchContractSkills{Requested: requestedSkills, Missing: withSliceDefault(missing)},
		BridgeActive: bridge.Active && bridge.Mode != "off", BridgeMode: bridge.Mode,
		PackageVersion: originalPackageVersion, Diagnostics: diagnostics, LaunchContractDigest: binding.LaunchContractDigest,
	}
	contract.Skills.Resolved = []launchContractSkill{}
	for _, s := range resolvedSkills {
		contract.Skills.Resolved = append(contract.Skills.Resolved, launchContractSkill{Name: s.Name, Path: s.Path, Source: string(s.Source)})
	}
	requiredIDs := []string{}
	privatePaths := map[string]bool{}
	for _, r := range requiredExtensions {
		privatePaths[r.Path] = true
	}
	for _, r := range toolPlan.RequiredExtensions {
		requiredIDs = append(requiredIDs, r.ID)
	}
	publicArgs := []string{}
	for _, a := range toolPlan.ExtensionArgs {
		if !privatePaths[a] { // required paths are private launch authority; preflight exposes their safe IDs
			publicArgs = append(publicArgs, a)
		}
	}
	contract.Tools = launchContractTools{
		RequestedBuiltin: toolPlan.RequestedBuiltinTools, DeclaredBuiltin: toolPlan.DeclaredBuiltinTools, EffectiveAllowlist: toolPlan.EffectiveToolAllowlist,
		ExplicitAllowlist: toolPlan.ExplicitToolAllowlist, RequiredChildTools: toolPlan.RequiredChildTools, InternalTools: toolPlan.InternalTools,
		Mcp: toolPlan.EffectiveMcpSelections, EffectiveMcpTools: toolPlan.EffectiveMcpTools, ToolExtensionPaths: toolPlan.ToolExtensionPaths,
		RuntimeExtensions: toolPlan.RuntimeExtensions, ConfiguredExtensions: toolPlan.ConfiguredExtensions, RequiredExtensionIDs: requiredIDs, ExtensionArgs: publicArgs,
		DisableAmbientExtensions: toolPlan.DisableAmbientExtensions, FanoutAuthorized: toolPlan.FanoutAuthorized, CapabilityCeiling: toolPlan.CapabilityCeiling,
		CapabilityAudit: toolPlan.CapabilityAudit,
	}
	if len(toolPlan.ExcludeTools) > 0 {
		contract.Tools.ExcludeTools = toolPlan.ExcludeTools
	}
	contract.Roots = launchContractRoots{Cwd: effectiveCwd, SessionRoot: sessionRoot, SessionDir: sessionDir, ArtifactsDir: artifactsDir, ArtifactPaths: artifactPathsOut, OutputPath: outputPath,
		Lifecycle: launchContractLifecycle{AsyncDir: lifecycleAsyncDir, ResultPath: lifecycleResultPath, StatusPath: filepath.Join(lifecycleAsyncDir, "status.json"),
			EventsPath: filepath.Join(lifecycleAsyncDir, "events.jsonl"), ProcessTerminalPath: processTerminalPath(lifecycleAsyncDir),
			ProcessTerminalCandidatePath: processTerminalCandidatePath(lifecycleAsyncDir)}}
	if sessionDir != "" {
		contract.Roots.SessionFile = filepath.Join(sessionDir, "session.jsonl")
	}
	contract.Digest = stableJSONDigest(contract.tree())
	return launchContractResult{OK: true, Contract: contract}, nil
}

func isSafeNestedID(v string) bool {
	return v != "" && len(v) <= 128 && !filepath.IsAbs(v) && !strings.ContainsAny(v, `/\`) && !strings.Contains(v, "..")
}

func strTree(s []string) []any { return anyStrings(withSliceDefault(s)) }

func (c *launchContract) tree() map[string]any {
	agent := map[string]any{"name": c.Agent.Name, "source": string(c.Agent.Source), "filePath": c.Agent.FilePath,
		"definitionProjectionVersion": float64(c.Agent.DefinitionProjectionVersion), "definitionDigest": c.Agent.DefinitionDigest}
	putIf(agent, "localName", c.Agent.LocalName)
	putIf(agent, "packageName", c.Agent.PackageName)
	shadowed := []any{}
	for _, s := range c.Agent.ShadowedCandidates {
		m := map[string]any{"name": s.Name, "source": string(s.Source), "filePath": s.FilePath, "selected": s.Selected}
		putIf(m, "localName", s.LocalName)
		putIf(m, "packageName", s.PackageName)
		if s.Disabled {
			m["disabled"] = true
		}
		shadowed = append(shadowed, m)
	}
	agent["shadowedCandidates"] = shadowed
	resolved := []any{}
	for _, s := range c.Skills.Resolved {
		resolved = append(resolved, map[string]any{"name": s.Name, "path": s.Path, "source": s.Source})
	}
	t := c.Tools
	mcp := []any{}
	for _, m := range t.Mcp {
		mcp = append(mcp, map[string]any{"name": m.Name, "selector": m.Selector})
	}
	tools := map[string]any{
		"requestedBuiltin": strTree(t.RequestedBuiltin), "declaredBuiltin": strTree(t.DeclaredBuiltin), "effectiveAllowlist": strTree(t.EffectiveAllowlist),
		"explicitAllowlist": t.ExplicitAllowlist, "requiredChildTools": strTree(t.RequiredChildTools), "internalTools": strTree(t.InternalTools), "mcp": mcp,
		"effectiveMcpTools": strTree(t.EffectiveMcpTools), "toolExtensionPaths": strTree(t.ToolExtensionPaths), "runtimeExtensions": strTree(t.RuntimeExtensions),
		"configuredExtensions": strTree(t.ConfiguredExtensions), "requiredExtensionIds": strTree(t.RequiredExtensionIDs), "extensionArgs": strTree(t.ExtensionArgs),
		"disableAmbientExtensions": t.DisableAmbientExtensions, "fanoutAuthorized": t.FanoutAuthorized,
	}
	if t.ExcludeTools != nil {
		tools["excludeTools"] = strTree(t.ExcludeTools)
	}
	if t.CapabilityCeiling != nil {
		tools["capabilityCeiling"] = t.CapabilityCeiling.object()
	}
	if t.CapabilityAudit != nil {
		tools["capabilityAudit"] = t.CapabilityAudit.tree()
	}
	r := c.Roots
	roots := map[string]any{"cwd": r.Cwd, "lifecycle": map[string]any{"asyncDir": r.Lifecycle.AsyncDir, "resultPath": r.Lifecycle.ResultPath, "statusPath": r.Lifecycle.StatusPath,
		"eventsPath": r.Lifecycle.EventsPath, "processTerminalPath": r.Lifecycle.ProcessTerminalPath, "processTerminalCandidatePath": r.Lifecycle.ProcessTerminalCandidatePath}}
	putIf(roots, "sessionRoot", r.SessionRoot)
	putIf(roots, "sessionDir", r.SessionDir)
	putIf(roots, "sessionFile", r.SessionFile)
	putIf(roots, "artifactsDir", r.ArtifactsDir)
	putIf(roots, "outputPath", r.OutputPath)
	if r.ArtifactPaths != nil {
		roots["artifactPaths"] = r.ArtifactPaths.tree()
	}
	diags := []any{}
	for _, d := range c.Diagnostics {
		diags = append(diags, d.tree())
	}
	m := map[string]any{
		"version": float64(c.Version), "runId": c.RunID, "agent": agent, "context": c.Context, "systemPromptMode": c.SystemPromptMode,
		"inheritProjectContext": c.InheritProjectContext, "inheritGlobalContext": c.InheritGlobalContext, "inheritSkills": c.InheritSkills,
		"skills": map[string]any{"requested": strTree(c.Skills.Requested), "resolved": resolved, "missing": strTree(c.Skills.Missing)}, "tools": tools,
		"intercomBridge": map[string]any{"active": c.BridgeActive, "mode": c.BridgeMode}, "roots": roots,
		"protocol":    map[string]any{"lifecycleArtifactVersion": float64(lifecycleArtifactVersion), "packageVersion": c.PackageVersion},
		"diagnostics": diags, "launchContractDigest": c.LaunchContractDigest,
	}
	putIf(m, "model", c.Model)
	putIf(m, "thinking", c.Thinking)
	putIf(m, "thinkingCeiling", c.ThinkingCeiling)
	return m
}

func (p artifactPaths) tree() map[string]any {
	return map[string]any{"inputPath": p.InputPath, "outputPath": p.OutputPath, "jsonlPath": p.JSONLPath, "transcriptPath": p.TranscriptPath, "metadataPath": p.MetadataPath}
}
