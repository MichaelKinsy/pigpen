package pi_permission_system

import (
	"strings"
	"sync"
)

// Port of src/policy/permission-manager.ts: the permission manager. It loads the config scopes through a PolicyLoader, composes
// defaults, baseline and config rules per agent scope, and answers checks and tool-level questions. It is string-based by design
// (ADR 0002): it consumes a ResolvedAccessIntent and never imports AccessPath.

// defaultUniversalFallback is the universal fallback when permission["*"] is absent from all scopes.
const defaultUniversalFallback = "ask"

// ScopedPermissionManager is the narrow interface for session-scoped permission checking: PermissionSession and the resolver depend
// on it, not on the concrete type, so test fakes satisfy it.
type ScopedPermissionManager interface {
	// ConfigureForCwd rebuilds the policy loader for a working directory; "" is the original's undefined or null (no project scope).
	ConfigureForCwd(cwd string)
	// Check is the unified resolution entry point. sessionRules nil is undefined.
	Check(intent ResolvedAccessIntent, sessionRules Ruleset) PermissionCheckResult
	// GetToolPermission and the other agent-scoped calls take a nil agentName for the global scope.
	GetToolPermission(toolName string, agentName *string) string
	IsToolFullyDenied(toolName string, agentName *string) bool
	GetConfigIssues(agentName *string) []string
}

// PermissionManagerOptions configure a PermissionManager.
type PermissionManagerOptions struct {
	// PolicyLoaderOptions configure the FilePolicyLoader the manager builds when PolicyLoader and AgentDir are not set.
	PolicyLoaderOptions
	// PolicyLoader replaces the file loader.
	PolicyLoader PolicyLoader
	// AgentDir is Pi's agent directory. When set, the manager derives every loader path from it (ignoring the path options) and
	// supports ConfigureForCwd.
	AgentDir string
	// Flavor decides whether path-surface rule matching folds case and separators (win32). The zero value is the POSIX flavor.
	Flavor PathFlavor
	// IsYoloEnabled reports yolo mode; read on every Check so a mid-session change takes effect. nil is disabled.
	IsYoloEnabled func() bool
}

type resolvedPermissions struct {
	// composedRules are the defaults, baseline and config rules (floored when a non-global scope failed). Session rules are appended
	// per check.
	composedRules Ruleset
	// failClosedScopes are the non-global scopes whose config failed to load or validate (#646).
	failClosedScopes []string
	// legacyMcpToolKeys are the top-level `mcp__...` keys relocated onto the mcp surface.
	legacyMcpToolKeys []string
}

type resolvedCacheEntry struct {
	stamp string
	value *resolvedPermissions
}

// PermissionManager implements ScopedPermissionManager. It is safe for concurrent use.
type PermissionManager struct {
	agentDir      string
	flavor        PathFlavor
	isYoloEnabled func() bool

	mu     sync.Mutex
	loader PolicyLoader
	cache  map[string]resolvedCacheEntry
}

// NewPermissionManager builds a manager from options.
func NewPermissionManager(o PermissionManagerOptions) *PermissionManager {
	m := &PermissionManager{agentDir: o.AgentDir, flavor: o.Flavor, isYoloEnabled: o.IsYoloEnabled, cache: map[string]resolvedCacheEntry{}}
	switch {
	case o.PolicyLoader != nil:
		m.loader = o.PolicyLoader
	case o.AgentDir != "":
		m.loader = NewFilePolicyLoader(derivePolicyLoaderOptions(o.AgentDir, ""))
	default:
		m.loader = NewFilePolicyLoader(o.PolicyLoaderOptions)
	}
	return m
}

func (m *PermissionManager) yolo() bool { return m.isYoloEnabled != nil && m.isYoloEnabled() }

func (m *PermissionManager) currentLoader() PolicyLoader {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loader
}

// ConfigureForCwd rebuilds the policy loader for a new working directory ("" is none) and clears the resolved-permissions cache.
// When no AgentDir was given at construction only the cache is cleared.
func (m *PermissionManager) ConfigureForCwd(cwd string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.agentDir != "" {
		m.loader = NewFilePolicyLoader(derivePolicyLoaderOptions(m.agentDir, cwd))
	}
	m.cache = map[string]resolvedCacheEntry{}
}

func agentNameOrEmpty(agentName *string) string {
	if agentName == nil {
		return ""
	}
	return *agentName
}

// GetConfigIssues is getConfigIssues: the loader's accumulated issues, plus the fail-closed notice and the Pi MCP tool key port
// notice.
func (m *PermissionManager) GetConfigIssues(agentName *string) []string {
	// Trigger a load/resolve to ensure issues are collected.
	resolved := m.resolvePermissions(agentName)
	issues := m.currentLoader().GetConfigIssues()
	issues = append([]string{}, issues...)
	if len(resolved.failClosedScopes) > 0 {
		issues = append(issues, "Invalid "+strings.Join(resolved.failClosedScopes, ", ")+" configuration detected \u2014 "+
			"failing closed: 'allow' rules are clamped to 'ask' for this session "+
			"until the configuration is corrected.")
	}
	if len(resolved.legacyMcpToolKeys) > 0 {
		issues = append(issues, formatMcpToolKeyPortNotice(resolved.legacyMcpToolKeys))
	}
	return issues
}

// GetResolvedPolicyPaths answers the loader's resolved paths.
func (m *PermissionManager) GetResolvedPolicyPaths() ResolvedPolicyPaths {
	return m.currentLoader().GetResolvedPolicyPaths()
}

func scopeOf(c *ScopeConfig) ScopeConfig {
	if c == nil {
		return ScopeConfig{}
	}
	return *c
}

func (m *PermissionManager) resolvePermissions(agentName *string) *resolvedPermissions {
	cacheKey := "__global__"
	if agentName != nil {
		cacheKey = *agentName
	}
	name := agentNameOrEmpty(agentName)

	m.mu.Lock()
	defer m.mu.Unlock()
	stamp := m.loader.GetCacheStamp(name)
	if cached, ok := m.cache[cacheKey]; ok && cached.stamp == stamp {
		return cached.value
	}

	globalConfig := scopeOf(m.loader.LoadGlobalConfig())
	projectConfig := scopeOf(m.loader.LoadProjectConfig())
	agentConfig := scopeOf(m.loader.LoadAgentConfig(name))
	projectAgentConfig := scopeOf(m.loader.LoadProjectAgentConfig(name))

	// Merge permission objects across scopes (lowest to highest precedence), tracking which scope contributed each (surface, pattern).
	merged := MergeScopesWithOrigins([]Scope{
		{Name: "global", Permission: globalConfig.Permission},
		{Name: "project", Permission: projectConfig.Permission},
		{Name: "agent", Permission: agentConfig.Permission},
		{Name: "project-agent", Permission: projectAgentConfig.Permission},
	})

	// The "*" key feeds SynthesizeDefaults only: it is not a config rule, so extension tools fall through to source "default".
	universalFallback := defaultUniversalFallback
	if state, ok := isPermissionState(merged.Permission.vals["*"]); ok {
		universalFallback = state
	}
	universalFallbackOrigin := "builtin"
	if o, ok := merged.Origins["*"]["*"]; ok {
		universalFallbackOrigin = o
	}

	configRules := buildConfigRules(merged.Permission, merged.Origins)
	// A top-level `mcp__...` key now names an `mcp` candidate. The baseline reads the rules as written, so a relocated allow on one Pi
	// MCP tool does not newly open the proxy's discovery targets.
	relocation := RelocateMcpToolKeyRules(configRules)

	composed := ComposeRuleset(SynthesizeDefaults(universalFallback, universalFallbackOrigin), SynthesizeBaseline(configRules), relocation.Rules)

	// Fail closed when a non-global scope's config is invalid: floor every allow (including one inherited from a lower scope) to ask so
	// a higher scope meant to tighten policy cannot silently fail open (#646). Global is excluded.
	failClosedScopes := []string{}
	if projectConfig.Invalid {
		failClosedScopes = append(failClosedScopes, "project")
	}
	if agentConfig.Invalid {
		failClosedScopes = append(failClosedScopes, "agent")
	}
	if projectAgentConfig.Invalid {
		failClosedScopes = append(failClosedScopes, "project-agent")
	}
	if len(failClosedScopes) > 0 {
		composed = FloorAllowsToAsk(composed)
	}

	value := &resolvedPermissions{composedRules: composed, failClosedScopes: failClosedScopes, legacyMcpToolKeys: uniqueStrings(relocation.RelocatedKeys)}
	m.cache[cacheKey] = resolvedCacheEntry{stamp, value}
	return value
}

// GetComposedConfigRules is getComposedConfigRules: the config-layer rules for the agent scope, for the `/permission-system show`
// command. Session rules are not included.
func (m *PermissionManager) GetComposedConfigRules(agentName *string) Ruleset {
	out := Ruleset{}
	for _, r := range m.resolvePermissions(agentName).composedRules {
		if r.Layer == "config" {
			out = append(out, r)
		}
	}
	return out
}

// GetToolPermission is getToolPermission: the tool-level state for a tool, without considering command-level rules.
func (m *PermissionManager) GetToolPermission(toolName string, agentName *string) string {
	composed := m.resolvePermissions(agentName).composedRules
	name := jsTrim(toolName)
	if ClassifyToolKind(name) == ToolKindMcpTool {
		return m.resolvePiMcpTool(name, composed)
	}
	// Every other surface resolves its tool-level state identically: evaluate the surface name against the "*" catch-all value.
	return Evaluate(name, "*", composed, m.flavor, "").Action
}

// IsToolFullyDenied is isToolFullyDenied: whether every value under a tool's surface resolves to deny (#815). It reads the same
// composed rules the catch-all query does, so it inherits the fail-closed floor and not the yolo rewrite; neither can create or
// remove the deny it turns on.
func (m *PermissionManager) IsToolFullyDenied(toolName string, agentName *string) bool {
	composed := m.resolvePermissions(agentName).composedRules
	name := jsTrim(toolName)
	if ClassifyToolKind(name) == ToolKindMcpTool {
		return m.resolvePiMcpTool(name, composed) == "deny"
	}
	return IsSurfaceFullyDenied(name, composed, m.flavor)
}

// resolvePiMcpTool is the action a Pi MCP tool resolves to on the mcp surface; its candidates come from its name alone.
func (m *PermissionManager) resolvePiMcpTool(toolName string, composed Ruleset) string {
	n := NormalizeInput(toolName, nil, m.currentLoader().GetConfiguredMcpServerNames())
	return EvaluateAnyValue(n.Surface, n.Values, composed, m.flavor).Rule.Action
}

// Check is the unified resolution entry point, dispatching on the intent kind: a tool intent is normalized through NormalizeInput,
// path-values are evaluated as given, and a bash-command unit is evaluated with its spellings as aliases.
func (m *PermissionManager) Check(intent ResolvedAccessIntent, sessionRules Ruleset) PermissionCheckResult {
	composed := m.resolvePermissions(intentAgentName(intent)).composedRules
	fullRules := composed
	if len(sessionRules) > 0 {
		fullRules = make(Ruleset, 0, len(composed)+len(sessionRules))
		fullRules = append(append(fullRules, composed...), sessionRules...)
	}
	// The yolo rewrite is applied after the cache so the resolved-permissions cache and the display surfaces stay yolo-free (#526).
	if m.yolo() {
		fullRules = RewriteAsksToYolo(fullRules)
	}

	switch it := intent.(type) {
	case PathValuesAccessIntent:
		lookup := it.Values
		if len(lookup) == 0 {
			lookup = []string{"*"}
		}
		return buildCheckResult(it.Surface, lookup, nil, it.Surface, it.Surface, fullRules, m.flavor)
	case BashCommandAccessIntent:
		n := NormalizeBashCommand(it.Command, it.Spellings)
		return buildCheckResult(n.Surface, n.Values, n.ResultExtras, n.Surface, n.Surface, fullRules, m.flavor)
	case ToolAccessIntent:
		toolName := jsTrim(it.Surface)
		n := NormalizeInput(toolName, it.Input, m.currentLoader().GetConfiguredMcpServerNames())
		return buildCheckResult(n.Surface, n.Values, n.ResultExtras, toolName, it.Surface, fullRules, m.flavor)
	}
	panic("permission manager: unknown access intent")
}

// intentAgentName is the agentName of a resolved intent (nil is undefined).
func intentAgentName(intent ResolvedAccessIntent) *string {
	switch it := intent.(type) {
	case ToolAccessIntent:
		return it.AgentName
	case PathValuesAccessIntent:
		return it.AgentName
	case BashCommandAccessIntent:
		return it.AgentName
	}
	return nil
}

// buildCheckResult evaluates a normalized surface/values triple and shapes the result. Every surface resolves through
// EvaluateAnyValue, so a rule's position in the config decides and the candidate list only determines which name the decision is
// reported under.
func buildCheckResult(surface string, values []string, resultExtras *jsObject, normalizedToolName, toolName string, fullRules Ruleset, flavor PathFlavor) PermissionCheckResult {
	found := EvaluateAnyValue(surface, values, fullRules, flavor)
	rule := found.Rule

	res := PermissionCheckResult{
		ToolName: toolName,
		State:    rule.Action,
		Reason:   rule.Reason,
		Source:   deriveSource(rule, normalizedToolName),
		Origin:   rule.Origin,
	}
	if rule.Layer == "config" || rule.Layer == "session" {
		p := rule.Pattern
		res.MatchedPattern = &p
	}
	if resultExtras != nil {
		if c, ok := resultExtras.vals["command"].(string); ok {
			res.Command = &c
		}
		if t, ok := resultExtras.vals["target"].(string); ok {
			res.Target = &t
		}
	}
	// For MCP, replace the normalizer's fallback target with the actual matched candidate value.
	if ClassifyToolKind(surface) == ToolKindMcp {
		t := found.Value
		res.Target = &t
	}
	return res
}

const mcpToolKeyMigrationGuide = "https://github.com/gotgenes/pi-packages/blob/main/packages/pi-permission-system/docs/migration/1001-pi-mcp-tools-on-mcp-surface.md"

// formatMcpToolKeyPortNotice is the notice asking the operator to move top-level `mcp__...` keys under `mcp`.
func formatMcpToolKeyPortNotice(keys []string) string {
	named := make([]string, len(keys))
	for i, k := range keys {
		named[i] = "\"" + k + "\""
	}
	return "Top-level permission keys naming Pi MCP tools are applied as \"mcp\" rules: " + strings.Join(named, ", ") + ". " +
		"Move them under \"mcp\" \u2014 see " + mcpToolKeyMigrationGuide
}

// buildConfigRules builds the config-layer rules from the merged permission object: every key except the universal "*", each rule
// tagged with the config layer and the scope that contributed it.
func buildConfigRules(merged *jsObject, origins map[string]map[string]string) Ruleset {
	withoutUniversal := newObject()
	for _, k := range merged.order() {
		if k != "*" {
			withoutUniversal.set(k, merged.vals[k])
		}
	}
	rules := NormalizeFlatConfig(withoutUniversal)
	for i := range rules {
		rules[i].Layer = "config"
		if o, ok := origins[rules[i].Surface][rules[i].Pattern]; ok {
			rules[i].Origin = o
		} else {
			rules[i].Origin = "builtin"
		}
	}
	return rules
}

// deriveSource maps a matched rule and tool name to PermissionCheckResult.Source: session always "session", the special families
// "special" (by family membership, so a directional surface keeps reporting it), then by tool kind.
func deriveSource(rule Rule, toolName string) string {
	if rule.Layer == "session" {
		return "session"
	}
	if family := SurfaceFamilyOf(toolName); family == "external_directory" || family == "path" {
		return "special"
	}
	switch ClassifyToolKind(toolName) {
	case ToolKindMcp, ToolKindMcpTool:
		if rule.Layer == "default" {
			return "default"
		}
		return "mcp"
	case ToolKindSkill:
		return "skill"
	case ToolKindBash:
		return "bash"
	case ToolKindPath:
		return "tool"
	}
	// Extension tools distinguish a synthesized-default match from a rule.
	if rule.Layer == "default" {
		return "default"
	}
	return "tool"
}
