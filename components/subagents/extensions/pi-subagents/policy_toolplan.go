package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Port of src/runs/shared/child-tool-plan.ts: which builtin tools, MCP tools, runtime extensions and extension files a child
// launch gets, and how that resolution is reported.
//
// The runtime extensions of the original are three TypeScript files that Pi loads into a child session. In this port the child
// is a pig process, and these names identify the child roles of this extension (the way the original names its supervisor
// channel, `native:pi-subagents-supervisor-channel`): the prompt runtime, the fast mode extension and the fan-out child.

const (
	promptRuntimeExtension = "native:pi-subagents-prompt-runtime"
	fastModeExtension      = "native:pi-subagents-fast-mode"
	fanoutChildExtension   = "native:pi-subagents-fanout-child"

	// fastModeProviderPrefix: the priority tier is an OpenAI-Codex request field; other providers reject or ignore it.
	fastModeProviderPrefix = "openai-codex/"
)

// isSubagentRuntimeExtensionPath is true for the extensions pi-subagents itself installs in child sessions.
func isSubagentRuntimeExtensionPath(p string) bool {
	return p == promptRuntimeExtension || p == fastModeExtension || p == fanoutChildExtension
}

// applyThinkingSuffix appends `:<thinking>` to a model that has no thinking suffix; replaceExisting replaces one it has.
// thinking is nil, false or a level string.
func applyThinkingSuffix(model string, thinking any, replaceExisting bool) string {
	level, ok := thinking.(string)
	if model == "" || !ok || level == "" {
		return model
	}
	if base, suffix := splitKnownThinkingSuffix(model); suffix != "" {
		if replaceExisting {
			return base + ":" + level
		}
		return model
	}
	return model + ":" + level
}

func resolveFastModeExtension(fast *bool, model, agentName string) ([]string, error) {
	if fast == nil || !*fast {
		return nil, nil
	}
	if model == "" {
		forAgent := ""
		if agentName != "" {
			forAgent = " for agent '" + agentName + "'"
		}
		return nil, fmt.Errorf("fast mode requires an explicit supported native OpenAI-Codex model%s.", forAgent)
	}
	if m := stripThinkingSuffix(model); !strings.HasPrefix(m, fastModeProviderPrefix) {
		return nil, fmt.Errorf("fast mode supports only native %s* models; unsupported model: %s.", fastModeProviderPrefix, m)
	}
	return []string{fastModeExtension}, nil
}

// capabilityAudit is SubagentCapabilityAudit: what a ceiling removed from a launch.
type capabilityAudit struct {
	Ceiling                 *resolvedCeiling
	RequestedTools          []string // nil: unset
	EffectiveTools          []string
	RemovedTools            []string
	ExcludeTools            []string // nil: unset
	InternalTools           []string
	ExtensionsDenied        bool
	RemovedExtensionCount   int
	RequestedMcpToolCount   int
	EffectiveMcpTools       []string
	AgentAllowed            bool
	AgentRestrictionSources []string // nil: unset
}

func (a *capabilityAudit) tree() map[string]any {
	m := map[string]any{
		"ceiling": a.Ceiling.object(), "effectiveTools": anyStrings(a.EffectiveTools), "removedTools": anyStrings(a.RemovedTools),
		"internalTools": anyStrings(a.InternalTools), "extensionsDenied": a.ExtensionsDenied, "removedExtensionCount": float64(a.RemovedExtensionCount),
		"requestedMcpToolCount": float64(a.RequestedMcpToolCount), "effectiveMcpTools": anyStrings(a.EffectiveMcpTools), "agentAllowed": a.AgentAllowed,
	}
	if a.RequestedTools != nil {
		m["requestedTools"] = anyStrings(a.RequestedTools)
	}
	if a.ExcludeTools != nil {
		m["excludeTools"] = anyStrings(a.ExcludeTools)
	}
	if a.AgentRestrictionSources != nil {
		m["agentRestrictionSources"] = anyStrings(a.AgentRestrictionSources)
	}
	return m
}

// piLaunchToolPlanInput is ResolvePiLaunchToolPlanInput (the runtime snapshot host is not ported; see policy_mcpconfig.go).
type piLaunchToolPlanInput struct {
	Tools                  []string // nil: unset
	ExcludeTools           []string
	AllowNestedSubagents   *bool
	Extensions             []string // nil: unset; empty: disable every ambient extension
	SubagentOnlyExtensions []string
	RequiredExtensions     []requiredChildExtension
	McpDirectTools         []string
	Cwd                    string
	RequireReadTool        bool
	StructuredOutput       bool
	Fast                   *bool
	Model                  string
	CapabilityCeiling      *resolvedCeiling
	InheritedCeiling       *resolvedCeiling
	AgentName              string
	PermissionRules        permissionRules
}

// piLaunchToolPlan is PiLaunchToolPlan. Its lists are never nil: the launch identity hashes an empty list as [].
type piLaunchToolPlan struct {
	CapabilityCeiling        *resolvedCeiling
	RequestedBuiltinTools    []string
	DeclaredBuiltinTools     []string
	ExcludeTools             []string
	ToolExtensionPaths       []string
	ResolvedMcpSelections    []resolvedMcpSelection
	EffectiveMcpSelections   []resolvedMcpSelection
	EffectiveMcpTools        []string
	ExplicitToolAllowlist    bool
	InternalTools            []string
	EffectiveToolAllowlist   []string
	RequiredChildTools       []string
	FanoutAuthorized         bool
	RuntimeExtensions        []string
	ConfiguredExtensions     []string
	RequiredExtensions       []requiredChildExtension
	ExtensionArgs            []string
	DisableAmbientExtensions bool
	CapabilityAudit          *capabilityAudit
	Warnings                 []string
}

func isExtensionToolPath(tool string) bool {
	return strings.Contains(tool, "/") || strings.HasSuffix(tool, ".ts") || strings.HasSuffix(tool, ".js")
}

func uniqueUnion(lists ...[]string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range lists {
		for _, s := range l {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

func stringSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, s := range list {
		m[s] = true
	}
	return m
}

// resolvePermissionSystemExtension is the permission-system extension's entry point when installed ("" when it is not). An
// installed package with a broken manifest is an error.
func resolvePermissionSystemExtension() (string, error) {
	dir := agentDir()
	var first error
	for _, extDir := range []string{filepath.Join(dir, "npm", "node_modules", "@gotgenes", "pi-permission-system"), filepath.Join(dir, "extensions", "pi-permission-system")} {
		if _, err := os.Stat(extDir); err != nil {
			continue
		}
		pkgPath := filepath.Join(extDir, "package.json")
		note := func(err error) {
			if first == nil {
				first = err
			}
		}
		if _, err := os.Stat(pkgPath); err != nil {
			note(fmt.Errorf("Permission-system package manifest is missing at %s.", pkgPath))
			continue
		}
		b, err := os.ReadFile(pkgPath)
		var parsed any
		msg := ""
		if err == nil {
			parsed, msg = jsJSONParse(string(b))
		} else {
			msg = err.Error()
		}
		manifest, isObj := asObject(parsed)
		switch {
		case msg != "":
			note(fmt.Errorf("Cannot read permission-system package manifest at %s: %s", pkgPath, msg))
			continue
		case !isObj:
			note(fmt.Errorf("Cannot read permission-system package manifest at %s: manifest root must be an object", pkgPath))
			continue
		}
		var entry string
		if pi := manifest.obj("pi"); pi != nil {
			if exts, ok := asArray(pi.vals["extensions"]); ok && len(exts) > 0 {
				entry, _ = exts[0].(string)
			}
		}
		if jsTrim(entry) == "" {
			note(fmt.Errorf("Permission-system package manifest at %s must declare pi.extensions[0] as a non-empty string.", pkgPath))
			continue
		}
		resolved := nodeResolve(extDir, entry)
		if _, err := os.Stat(resolved); err == nil {
			return resolved, nil
		}
		note(fmt.Errorf("Permission-system extension entry %s in %s does not exist at %s.", marshalJSON(entry, ""), pkgPath, resolved))
	}
	return "", first
}

// legacyUnderscoreMcpToolName: capability ceilings persisted before the adapter's prefix fix may name hyphenated server
// prefixes with underscores.
func legacyUnderscoreMcpToolName(s resolvedMcpSelection) string {
	slash := strings.Index(s.Selector, "/")
	if slash < 1 {
		return s.Name
	}
	suffix := "_" + s.Selector[slash+1:]
	if !strings.HasSuffix(s.Name, suffix) {
		return s.Name
	}
	prefix := strings.TrimSuffix(s.Name, suffix)
	return strings.ReplaceAll(prefix, "-", "_") + suffix
}

func extensionIdentifier(v string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(jsTrim(v))))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

// resolvePiLaunchToolPlan is child-tool-plan.ts:resolvePiLaunchToolPlan; its errors are the original's thrown ones.
func resolvePiLaunchToolPlan(in piLaunchToolPlanInput) (*piLaunchToolPlan, error) {
	ceiling := intersectSubagentCapabilityCeilings(in.CapabilityCeiling, in.InheritedCeiling)
	required, err := snapshotRequiredChildExtensions(in.RequiredExtensions, "Required child extensions", false)
	if err != nil {
		return nil, err
	}
	denyExt := ceiling != nil && ceiling.DenyExtensions
	ceilingSources := func() string {
		if ceiling != nil && len(ceiling.Sources) > 0 {
			return strings.Join(ceiling.Sources, ", ")
		}
		return "unknown source"
	}
	if len(required) > 0 && denyExt {
		ids := make([]string, len(required))
		for i, r := range required {
			ids[i] = r.ID
		}
		return nil, fmt.Errorf("Capability ceiling from %s denies extensions but this host requires: %s.", ceilingSources(), strings.Join(ids, ", "))
	}
	hasAllowed := ceiling != nil && ceiling.HasAllowedTools
	var allowed map[string]bool
	if hasAllowed {
		allowed = stringSet(ceiling.AllowedTools)
	}
	requestedBuiltin := []string{}
	for _, t := range in.Tools {
		if !isExtensionToolPath(t) {
			requestedBuiltin = append(requestedBuiltin, t)
		}
	}
	if in.RequireReadTool && hasAllowed && !allowed["read"] {
		return nil, fmt.Errorf("Capability ceiling from %s excludes required tool 'read' for lazy skill loading.", ceilingSources())
	}
	var base []string
	switch {
	case in.Tools == nil:
		if hasAllowed {
			base = uniqueUnion(ceiling.AllowedTools)
		}
	case in.RequireReadTool && len(requestedBuiltin) > 0 && !contains(requestedBuiltin, "read") && !hasAllowed:
		base = append([]string{"read"}, requestedBuiltin...)
	default:
		base = requestedBuiltin
	}
	declared := []string{}
	for _, t := range base {
		if !hasAllowed || allowed[t] {
			declared = append(declared, t)
		}
	}
	excludeTools := []string{}
	for _, t := range in.ExcludeTools {
		if t = jsTrim(t); t != "" && !contains(excludeTools, t) {
			excludeTools = append(excludeTools, t)
		}
	}
	excluded := stringSet(excludeTools)
	effectiveDeclared := []string{}
	for _, t := range declared {
		if !excluded[t] {
			effectiveDeclared = append(effectiveDeclared, t)
		}
	}
	fanout := contains(effectiveDeclared, "subagent") || (in.AllowNestedSubagents != nil && *in.AllowNestedSubagents && !excluded["subagent"] && (!hasAllowed || allowed["subagent"]))
	if contains(effectiveDeclared, "subagent_supervisor") && !fanout {
		return nil, fmt.Errorf("Tool 'subagent_supervisor' requires fanout authorization: include 'subagent' in the effective tools allowlist or enable allowNestedSubagents.")
	}
	toolExtensionPaths := []string{}
	if !denyExt {
		for _, t := range in.Tools {
			if isExtensionToolPath(t) {
				toolExtensionPaths = append(toolExtensionPaths, t)
			}
		}
	}
	mcp := mcpDirectToolResolution{Selections: []resolvedMcpSelection{}, UnresolvedSelectors: []string{}}
	if !denyExt {
		if mcp, err = resolveMcpDirectToolResolution(in.McpDirectTools, in.Cwd); err != nil {
			return nil, err
		}
	}
	if len(mcp.UnresolvedSelectors) > 0 {
		return nil, fmt.Errorf("%s", formatUnresolvedMcpDirectToolSelectors(mcp.UnresolvedSelectors))
	}
	resolvedMcp := mcp.Selections
	resolvedNames := map[string]bool{}
	legacyCounts := map[string]int{}
	for _, s := range resolvedMcp {
		resolvedNames[s.Name] = true
	}
	for _, s := range resolvedMcp {
		if l := legacyUnderscoreMcpToolName(s); l != s.Name {
			legacyCounts[l]++
		}
	}
	effectiveMcp := []resolvedMcpSelection{}
	effectiveMcpTools := []string{}
	for _, s := range resolvedMcp {
		legacy := legacyUnderscoreMcpToolName(s)
		ok := !hasAllowed || allowed[s.Name] || (legacyCounts[legacy] == 1 && !resolvedNames[legacy] && allowed[legacy])
		if ok && !excluded[s.Name] {
			effectiveMcp = append(effectiveMcp, s)
			effectiveMcpTools = append(effectiveMcpTools, s.Name)
		}
	}
	explicit := in.Tools != nil || len(in.McpDirectTools) > 0 || hasAllowed
	internal := []string{}
	if in.StructuredOutput && !excluded["structured_output"] {
		internal = append(internal, "structured_output")
	}
	effectiveAllowlist := uniqueUnion(effectiveDeclared, effectiveMcpTools, internal)
	// Upward contact stays in the --tools allowlist but is not a strict requirement: children register contact_supervisor at
	// runtime through the supervisor channel. A lone intercom entry stays strictly required.
	legacyPairing := contains(effectiveDeclared, "contact_supervisor")
	requiredChildTools := []string{}
	if explicit {
		var parts [][]string
		if in.Tools != nil {
			parts = append(parts, effectiveDeclared)
		}
		if len(in.McpDirectTools) > 0 {
			parts = append(parts, effectiveMcpTools)
		}
		parts = append(parts, internal)
		for _, t := range uniqueUnion(parts...) {
			if t != "contact_supervisor" && (!legacyPairing || t != "intercom") {
				requiredChildTools = append(requiredChildTools, t)
			}
		}
	}
	permExt := ""
	if !denyExt && len(in.PermissionRules) > 0 {
		if permExt, err = resolvePermissionSystemExtension(); err != nil {
			return nil, err
		}
	}
	if in.Fast != nil && *in.Fast && denyExt {
		return nil, fmt.Errorf("fast mode requires a child runtime extension, but this launch denies extensions.")
	}
	fastExt, err := resolveFastModeExtension(in.Fast, in.Model, in.AgentName)
	if err != nil {
		return nil, err
	}
	runtimeExtensions := append([]string{promptRuntimeExtension}, fastExt...)
	if fanout {
		runtimeExtensions = append(runtimeExtensions, fanoutChildExtension)
	}
	if permExt != "" {
		runtimeExtensions = append(runtimeExtensions, permExt)
	}
	disableAmbient := denyExt || in.Extensions != nil
	warnings := []string{}
	if !denyExt && in.Extensions != nil && len(in.Extensions) == 0 {
		forAgent := ""
		if in.AgentName != "" {
			forAgent = " for agent '" + in.AgentName + "'"
		}
		warnings = append(warnings, "extensions: [] override"+forAgent+` disables ALL ambient extensions for this child (not just "adds nothing"), `+
			"including any model-provider extension needed to resolve a provider-qualified model. "+
			"List the extensions this child actually needs instead of an empty array.")
	}
	configured := []string{}
	if !denyExt {
		configured = append(append(append(configured, toolExtensionPaths...), in.Extensions...), in.SubagentOnlyExtensions...)
	}
	var ordinaryArgs []string
	if disableAmbient {
		ordinaryArgs = uniqueUnion(runtimeExtensions, configured)
	} else {
		ordinaryArgs = uniqueUnion(runtimeExtensions, toolExtensionPaths, in.SubagentOnlyExtensions)
	}
	requiredPaths := make([]string, len(required))
	for i, r := range required {
		requiredPaths[i] = r.Path
	}
	extensionArgs := uniqueUnion(ordinaryArgs, requiredPaths)
	plan := &piLaunchToolPlan{
		CapabilityCeiling: ceiling, RequestedBuiltinTools: requestedBuiltin, DeclaredBuiltinTools: declared, ExcludeTools: excludeTools,
		ToolExtensionPaths: toolExtensionPaths, ResolvedMcpSelections: resolvedMcp, EffectiveMcpSelections: effectiveMcp, EffectiveMcpTools: effectiveMcpTools,
		ExplicitToolAllowlist: explicit, InternalTools: internal, EffectiveToolAllowlist: effectiveAllowlist, RequiredChildTools: requiredChildTools,
		FanoutAuthorized: fanout, RuntimeExtensions: runtimeExtensions, ConfiguredExtensions: configured, RequiredExtensions: required,
		ExtensionArgs: extensionArgs, DisableAmbientExtensions: disableAmbient, Warnings: warnings,
	}
	if ceiling != nil {
		audit := &capabilityAudit{
			Ceiling: ceiling, EffectiveTools: effectiveAllowlist, InternalTools: internal, ExtensionsDenied: ceiling.DenyExtensions,
			RequestedMcpToolCount: len(in.McpDirectTools), EffectiveMcpTools: effectiveMcpTools,
			AgentAllowed: in.AgentName == "" || isAgentAllowedByCapabilityCeiling(in.AgentName, ceiling), RemovedTools: []string{},
			AgentRestrictionSources: capabilityCeilingAgentRestrictionSources(ceiling),
		}
		if len(excludeTools) > 0 {
			audit.ExcludeTools = excludeTools
		}
		if in.Tools != nil {
			names := []string{}
			for _, s := range resolvedMcp {
				names = append(names, s.Name)
			}
			audit.RequestedTools = uniqueUnion(requestedBuiltin, names)
			for _, t := range audit.RequestedTools {
				if !contains(effectiveAllowlist, t) {
					audit.RemovedTools = append(audit.RemovedTools, t)
				}
			}
		}
		if denyExt {
			n := len(in.Extensions) + len(in.SubagentOnlyExtensions)
			for _, t := range in.Tools {
				if isExtensionToolPath(t) {
					n++
				}
			}
			audit.RemovedExtensionCount = n
		}
		plan.CapabilityAudit = audit
	}
	return plan, nil
}
