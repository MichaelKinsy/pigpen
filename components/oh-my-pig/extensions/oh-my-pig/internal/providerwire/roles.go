// Package providerwire provides model-role resolution and PiG v0.8.0 provider
// request/header middleware (service tiers, extended context, forced tool_choice).
package providerwire

import (
	"maps"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/config/model-roles.ts
// upstream: packages/coding-agent/src/config/model-resolver.ts
// upstream: packages/catalog/src/compat/rules/runtime/behavior.kdl:431-549

const (
	// ModelRoleAliasPrefix is the canonical selector prefix for a model role.
	ModelRoleAliasPrefix = "@"
	// LegacyModelRoleAliasPrefix is the legacy selector prefix accepted for backwards compatibility.
	LegacyModelRoleAliasPrefix = "pi/"
	// DefaultModelRoleAlias is the shorthand selector for the default role.
	DefaultModelRoleAlias = "*"

	defaultModelRole = "default"
)

// KnownRoleIDs is the canonical display order of built-in omp model roles.
// upstream: packages/tui/src/overlays/model-browser.ts:61-77
var KnownRoleIDs = []string{
	"default",
	"smol",
	"slow",
	"vision",
	"plan",
	"commit",
	"tiny",
	"memory",
	"task",
	"advisor",
	"widget",
	"image",
	"web",
	"speech",
	"dictation",
	"judge",
}

var knownRoleSet = func() map[string]bool {
	m := make(map[string]bool, len(KnownRoleIDs))
	for _, id := range KnownRoleIDs {
		m[id] = true
	}
	return m
}()

var kindRoleSet = map[string]bool{
	"image":     true,
	"web":       true,
	"speech":    true,
	"dictation": true,
	"judge":     true,
}

var validThinkingLevels = map[string]bool{
	"off":     true,
	"minimal": true,
	"low":     true,
	"medium":  true,
	"high":    true,
	"xhigh":   true,
	"max":     true,
	"auto":    true,
}

// rolePriorityAlias maps roles without their own catalog default list to another role's list.
// upstream: packages/coding-agent/src/config/model-resolver.ts:1294-1299
var rolePriorityAlias = map[string]string{
	"advisor": "slow",
	"memory":  "smol",
	"tiny":    "smol",
	"widget":  "smol",
}

type configuredRoleFallback struct {
	role           string
	configuredOnly bool
}

// roleConfiguredFallback mirrors ROLE_CONFIGURED_FALLBACK in model-resolver.ts:1307-1312.
var roleConfiguredFallback = map[string]configuredRoleFallback{
	"advisor": {role: "slow", configuredOnly: true},
	"memory":  {role: "tiny", configuredOnly: false},
	"tiny":    {role: "smol", configuredOnly: false},
	"widget":  {role: "smol", configuredOnly: false},
}

// builtinRoleDefaults mirrors packages/catalog/src/compat/rules/runtime/behavior.kdl:431-549.
var builtinRoleDefaults = map[string][]string{
	"smol": {
		"claude-haiku-5-5",
		"claude-haiku-5.5",
		"haiku-5-5",
		"haiku-5.5",
		"google-antigravity/gemini-3.8-flash",
		"gemini-3.8-flash",
		"gemini-3-8-flash",
		"gemini-3.7-flash",
		"gemini-3-7-flash",
		"gemini-3.6-flash",
		"gemini-3-6-flash",
		"gemini-3.5-flash",
		"gemini-3-5-flash",
		"openai-codex/gpt-5.3-codex-spark",
		"gpt-5.3-codex-spark",
		"5.3-spark",
		"spark",
		"zai/glm-5.3-flash",
		"glm-5.3-flash",
		"gemini-3.1-flash-lite",
		"gemini-3-1-flash-lite",
		"flash-lite",
		"cerebras/gpt-oss-120b",
		"gpt-oss-120b",
		"oss-120b",
		"cerebras/zai-glm-4.7",
		"cerebras/zai-glm-4.6",
		"claude-haiku-4-5",
		"claude-haiku-4.5",
		"haiku-4-5",
		"haiku-4.5",
		"haiku",
		"openai-codex/gpt-5.6-luna",
		"gpt-5.6-luna",
		"openai-codex/gpt-5.4-mini",
		"gpt-5.4-mini",
		"-mini",
	},
	"slow": {
		"openai-codex/gpt-5.6-sol",
		"gpt-5.6-sol",
		"anthropic/claude-fable-5-2",
		"claude-fable-5-2",
		"claude-fable-5.2",
		"fable-5.2",
		"fable-5-2",
		"anthropic/claude-fable-5-1",
		"claude-fable-5-1",
		"claude-fable-5.1",
		"fable-5.1",
		"fable-5-1",
		"anthropic/claude-fable-5",
		"claude-fable-5",
		"fable-5",
		"kimi-code/k3",
		"kimi-k3",
		"moonshotai/kimi-k3",
		"k3",
		"zai/glm-5.3",
		"glm-5.3",
		"anthropic/claude-opus-5-5",
		"claude-opus-5-5",
		"opus-5-5",
		"anthropic/claude-opus-5",
		"claude-opus-5",
		"opus-5",
		"gpt-5.5",
		"claude-opus-4-8",
		"claude-opus-4.8",
		"opus-4-8",
		"opus-4.8",
	},
	"image": {
		"openai/gpt-image-2",
		"openai-codex/gpt-image-2",
		"google-antigravity/gemini-3-pro-image",
		"xai/grok-imagine-image",
		"xai-oauth/grok-imagine-image",
		"openrouter/google/gemini-3-pro-image",
		"google/gemini-3-pro-image",
		"deepinfra/black-forest-labs/FLUX-2-pro",
		"factory-droid/gpt-image-2.6-sunburst",
		"factory-droid/gpt-image-2.6-flare",
		"factory-droid/gpt-image-2.5-sunburst",
		"factory-droid/gpt-image-2.5-flare",
		"factory-droid/gemini-3-pro-image-preview",
	},
	"web": {
		"web/parallel",
		"web/hosted",
		"web/exa",
		"web/firecrawl",
		"web/searxng",
		"web/startpage",
		"web/duckduckgo",
		"web/ecosia",
		"web/google",
		"web/mojeek",
		"web/public",
	},
	"speech": {
		"local/phonon-31m",
		"xai/grok-tts",
		"xai-oauth/grok-tts",
	},
	"dictation": {
		"local/parakeet-tdt-0.6b-v3",
	},
	"judge": {
		"typesafe/jev-1.14.0",
		"openrouter/typesafe/jev-1.14",
		"@tiny",
		"@smol",
		"@default",
	},
}

// IsKnownRole reports whether role is one of omp's built-in model role IDs.
func IsKnownRole(role string) bool {
	return knownRoleSet[strings.ToLower(strings.TrimSpace(role))]
}

// ParseRoleAlias parses a role selector (`@smol`, `@plan:high`, `pi/slow`, `*`, `*:xhigh`).
// If raw is not a role alias, isAlias is false.
func ParseRoleAlias(raw string) (role, thinking string, isAlias bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", false
	}
	base, level := splitThinkingSuffix(s)
	switch {
	case base == DefaultModelRoleAlias:
		return defaultModelRole, level, true
	case strings.HasPrefix(base, ModelRoleAliasPrefix):
		name := strings.TrimSpace(strings.TrimPrefix(base, ModelRoleAliasPrefix))
		if name == "" || strings.Contains(name, "/") {
			return "", "", false
		}
		return strings.ToLower(name), level, true
	case strings.HasPrefix(base, LegacyModelRoleAliasPrefix):
		name := strings.TrimSpace(strings.TrimPrefix(base, LegacyModelRoleAliasPrefix))
		if name == "" || strings.Contains(name, "/") {
			return "", "", false
		}
		return strings.ToLower(name), level, true
	default:
		return "", "", false
	}
}

// splitThinkingSuffix splits an optional trailing `:<level>` thinking selector
// when `<level>` is a recognized omp thinking level.
func splitThinkingSuffix(selector string) (base, level string) {
	idx := strings.LastIndexByte(selector, ':')
	if idx <= 0 || idx == len(selector)-1 {
		return selector, ""
	}
	cand := strings.ToLower(strings.TrimSpace(selector[idx+1:]))
	if validThinkingLevels[cand] {
		return strings.TrimSpace(selector[:idx]), cand
	}
	return selector, ""
}

// configuredModelRoles reads `modelRoles` from Setting(ctx, "modelRoles").
func configuredModelRoles(ctx sdk.Context) map[string]string {
	if Setting == nil {
		return nil
	}
	raw := Setting(ctx, "modelRoles")
	if raw == nil {
		return nil
	}
	out := map[string]string{}
	switch m := raw.(type) {
	case map[string]any:
		for k, v := range m {
			if s, ok := v.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out[strings.ToLower(strings.TrimSpace(k))] = trimmed
				}
			}
		}
	case map[string]string:
		for k, v := range m {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				out[strings.ToLower(strings.TrimSpace(k))] = trimmed
			}
		}
	}
	return out
}

// rolePriorityDefaults returns built-in default selector patterns for role,
// following rolePriorityAlias.
func rolePriorityDefaults(role string) []string {
	key := role
	if alias, ok := rolePriorityAlias[role]; ok {
		key = alias
	}
	return builtinRoleDefaults[key]
}

// shouldInheritDefaultBeforePriority mirrors model-resolver.ts:1279-1281.
func shouldInheritDefaultBeforePriority(role string) bool {
	return role == "smol" || role == "slow"
}

// normalizePatternList splits a comma-separated model pattern string into trimmed entries.
func normalizePatternList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// expandRolePatterns resolves a role name (or alias/pattern) to ordered concrete
// model selector patterns, guarding against cycles.
// upstream: packages/coding-agent/src/config/model-resolver.ts:1327-1418
func expandRolePatterns(role string, configured map[string]string, visited map[string]bool) []string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" || visited[role] {
		return nil
	}
	nextVisited := make(map[string]bool, len(visited)+1)
	maps.Copy(nextVisited, visited)
	nextVisited[role] = true

	defaults := expandNestedPatterns(rolePriorityDefaults(role), nil, configured, nextVisited)
	cfgVal := configured[role]
	cfgDefault := configured[defaultModelRole]

	var resolved []string
	if cfgVal != "" {
		resolved = expandNestedPatterns(normalizePatternList(cfgVal), defaults, configured, nextVisited)
	} else if fb, ok := roleConfiguredFallback[role]; ok && (!fb.configuredOnly || configured[fb.role] != "") {
		resolved = expandRolePatterns(fb.role, configured, nextVisited)
	} else if shouldInheritDefaultBeforePriority(role) && cfgDefault != "" {
		resolved = expandNestedPatterns(normalizePatternList(cfgDefault), defaults, configured, nextVisited)
	}

	if len(resolved) == 0 {
		resolved = append(resolved, defaults...)
	}
	// Chat roles like plan/commit/task with no explicit entry inherit configured default.
	if len(resolved) == 0 && !kindRoleSet[role] && role != defaultModelRole && cfgDefault != "" {
		resolved = expandNestedPatterns(normalizePatternList(cfgDefault), nil, configured, nextVisited)
	}
	return resolved
}

func expandNestedPatterns(patterns []string, roleDefaults []string, configured map[string]string, visited map[string]bool) []string {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		aliasRole, level, isAlias := ParseRoleAlias(pattern)
		if !isAlias || (!knownRoleSet[aliasRole] && configured[aliasRole] == "") {
			out = append(out, pattern)
			continue
		}
		if visited[aliasRole] {
			for _, def := range roleDefaults {
				if level != "" {
					out = append(out, def+":"+level)
				} else {
					out = append(out, def)
				}
			}
			continue
		}
		recursed := expandRolePatterns(aliasRole, configured, visited)
		for _, r := range recursed {
			if level != "" {
				if _, existing := splitThinkingSuffix(r); existing == "" {
					out = append(out, r+":"+level)
					continue
				}
			}
			out = append(out, r)
		}
	}
	return out
}

// ResolveRole resolves a model role (`smol`, `slow`, `plan`, `commit`, `task`,
// `web`, `judge`, `tiny`, `default`, `@role`, `pi/role`, `*`, or explicit
// `provider/id`) against `modelRoles` settings, the host `ModelRegistry`, and
// built-in omp fallbacks.
func ResolveRole(ctx sdk.Context, role string) (provider, id string, ok bool) {
	provider, id, _, ok = ResolveRoleWithThinking(ctx, role)
	return provider, id, ok
}

// ResolveRoleWithThinking resolves role like ResolveRole and also returns any
// explicit thinking-level suffix (e.g. from `@plan:high` or `modelRoles.plan`).
func ResolveRoleWithThinking(ctx sdk.Context, role string) (provider, id, thinking string, ok bool) {
	raw := strings.TrimSpace(role)
	if raw == "" {
		return "", "", "", false
	}

	configured := configuredModelRoles(ctx)

	var patterns []string
	var outerThinking string
	isChatRole := false

	if aliasRole, level, isAlias := ParseRoleAlias(raw); isAlias {
		outerThinking = level
		if knownRoleSet[aliasRole] || configured[aliasRole] != "" {
			patterns = expandRolePatterns(aliasRole, configured, nil)
			isChatRole = !kindRoleSet[aliasRole]
		}
	} else {
		base, level := splitThinkingSuffix(raw)
		lowerBase := strings.ToLower(base)
		if knownRoleSet[lowerBase] || configured[lowerBase] != "" {
			outerThinking = level
			patterns = expandRolePatterns(lowerBase, configured, nil)
			isChatRole = !kindRoleSet[lowerBase]
		} else {
			// Direct provider/id or bare model pattern.
			outerThinking = level
			patterns = normalizePatternList(base)
		}
	}

	reg := ctx.ModelRegistry()
	var available []map[string]any
	availLoaded := false
	loadAvailable := func() []map[string]any {
		if availLoaded {
			return available
		}
		availLoaded = true
		if models, err := reg.GetAvailable(); err == nil && len(models) > 0 {
			available = models
			return available
		}
		if models, err := reg.GetAll(); err == nil {
			available = models
		}
		return available
	}

	for _, pat := range patterns {
		basePat, patThinking := splitThinkingSuffix(pat)
		effThinking := patThinking
		if outerThinking != "" {
			effThinking = outerThinking
		}
		if p, mID, found := resolveSelector(reg, loadAvailable, basePat); found {
			return p, mID, effThinking, true
		}
	}

	// Fallback to active session model for chat roles when no configured or
	// built-in priority candidate is available in the registry.
	if isChatRole {
		if p, mID, found := activeSessionModel(ctx); found {
			return p, mID, outerThinking, true
		}
	}
	return "", "", "", false
}

func resolveSelector(reg sdk.ModelRegistry, loadAvailable func() []map[string]any, selector string) (provider, id string, ok bool) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return "", "", false
	}

	// Explicit provider/modelId selector: try exact ModelRegistry.Find first.
	if slash := strings.IndexByte(selector, '/'); slash > 0 && slash < len(selector)-1 {
		wantProvider := strings.TrimSpace(selector[:slash])
		wantID := strings.TrimSpace(selector[slash+1:])
		if found, err := reg.Find(wantProvider, wantID); err == nil && found != nil {
			p, mID := extractModelIdentity(found)
			if p == "" {
				p = wantProvider
			}
			if mID == "" {
				mID = wantID
			}
			return p, mID, true
		}
		// Fallback: case-insensitive / dot-dash revision match in available models.
		models := loadAvailable()
		wantKey := revisionSpellingKey(wantID)
		for _, m := range models {
			mp, mid := extractModelIdentity(m)
			if strings.EqualFold(mp, wantProvider) && (strings.EqualFold(mid, wantID) || revisionSpellingKey(mid) == wantKey) {
				return mp, mid, true
			}
		}
		return "", "", false
	}

	// Bare model ID or fragment: match against available models.
	models := loadAvailable()
	if len(models) == 0 {
		return "", "", false
	}
	wantLower := strings.ToLower(selector)
	wantKey := revisionSpellingKey(selector)

	// Pass 1: exact ID or dot/dash-normalized revision match.
	for _, m := range models {
		mp, mid := extractModelIdentity(m)
		if mid == "" {
			continue
		}
		if strings.EqualFold(mid, selector) || revisionSpellingKey(mid) == wantKey {
			return mp, mid, true
		}
	}
	// Pass 2: substring / fragment match (e.g. "-mini", "haiku", "opus-5").
	for _, m := range models {
		mp, mid := extractModelIdentity(m)
		if mid == "" {
			continue
		}
		if strings.Contains(strings.ToLower(mid), wantLower) || strings.Contains(revisionSpellingKey(mid), wantKey) {
			return mp, mid, true
		}
	}
	return "", "", false
}

// revisionSpellingKey collapses dotted numeric revisions (`4.5` -> `4-5`) to
// match omp's model-resolver.ts:411 revisionSpellingKey.
func revisionSpellingKey(id string) string {
	lower := strings.ToLower(id)
	if !strings.ContainsRune(lower, '.') {
		return lower
	}
	b := []byte(lower)
	for i := 1; i < len(b)-1; i++ {
		if b[i] == '.' && isASCIIDigit(b[i-1]) && isASCIIDigit(b[i+1]) {
			b[i] = '-'
		}
	}
	return string(b)
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func extractModelIdentity(m map[string]any) (provider, id string) {
	if m == nil {
		return "", ""
	}
	if p, ok := m["provider"].(string); ok {
		provider = strings.TrimSpace(p)
	} else if pm, ok := m["provider"].(map[string]any); ok {
		if pid, ok := pm["id"].(string); ok {
			provider = strings.TrimSpace(pid)
		}
	}
	if mid, ok := m["id"].(string); ok {
		id = strings.TrimSpace(mid)
	} else if mid, ok := m["modelId"].(string); ok {
		id = strings.TrimSpace(mid)
	}
	return provider, id
}

func activeSessionModel(ctx sdk.Context) (provider, id string, ok bool) {
	if info, err := ctx.GetModelInfo(); err == nil && info != nil {
		if mid := strings.TrimSpace(info.ID); mid != "" {
			return strings.TrimSpace(info.Provider), mid, true
		}
	}
	if mid := strings.TrimSpace(ctx.Model()); mid != "" {
		return strings.TrimSpace(ctx.ModelProvider()), mid, true
	}
	return "", "", false
}

// listKnownRoles returns KnownRoleIDs plus any custom configured roles sorted alphabetically.
func listKnownRoles(configured map[string]string) []string {
	out := make([]string, 0, len(KnownRoleIDs)+len(configured))
	out = append(out, KnownRoleIDs...)
	var custom []string
	for k := range configured {
		if !knownRoleSet[k] {
			custom = append(custom, k)
		}
	}
	slices.Sort(custom)
	out = append(out, custom...)
	return out
}
