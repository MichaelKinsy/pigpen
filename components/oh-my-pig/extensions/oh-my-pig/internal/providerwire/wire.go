package providerwire

import (
	"fmt"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:76-167,508-645
// upstream: packages/ai/src/providers/anthropic-headers.ts:25-53,82,109
// upstream: packages/ai/src/providers/anthropic.ts:840-849,2157,5683-5699
// upstream: packages/ai/src/providers/openai-shared.ts:391-398
// upstream: packages/ai/src/providers/google-shared.ts:1053-1080,1384
// upstream: packages/ai/src/utils/tool-choice.ts:47-101

const (
	// FastModeBeta is the Anthropic beta token required alongside `speed: "fast"`.
	FastModeBeta = "fast-mode-2026-02-01"
	// ExtendedContextBeta is the Anthropic beta token for 1M extended context.
	ExtendedContextBeta = "context-1m-2025-08-07"

	// Canonical service tier values.
	TierNone      = "none"
	TierPriority  = "priority"
	TierUltrafast = "ultrafast"
	TierFlex      = "flex"

	fastUsage            = "Usage: /fast [on|ultra|off|status]"
	slowUsage            = "Usage: /slow [on|off|status]"
	extendedContextUsage = "Usage: /extended-context [on|off|status]"
	roleUsage            = "Usage: /role [list | <role> | @<role>]"
)

// Setting reads an effective extension setting for ctx (wired by wiring.go to settings.Any).
var Setting func(ctx sdk.Context, key string) any

// SetSetting updates an extension setting for ctx (wired by wiring.go to settings.Set).
var SetSetting func(ctx sdk.Context, key string, value any, persist bool) error

type wireFamily int

const (
	familyUnknown wireFamily = iota
	familyAnthropic
	familyOpenAICompletions
	familyOpenAIResponses
	familyGoogle
)

type sessionState struct {
	// serviceTier holds a session-level override from /fast or /slow:
	// "" = inherit from settings, "none" = explicitly off, "priority", "ultrafast", "flex".
	serviceTier string

	// extendedContext holds a session-level override from /extended-context:
	// nil = inherit from settings.
	extendedContext *bool

	// forcedTool holds a turn-scoped tool name injected as forced tool_choice
	// on the next provider request and cleared automatically on turn/agent end.
	forcedTool string
}

type manager struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
}

func newManager() *manager {
	return &manager{sessions: make(map[string]*sessionState)}
}

var defaultManager = newManager()

// Register installs the providerwire hooks and slash commands on e.
func Register(e *sdk.Extension) {
	lifecycle.OnEnd("providerwire", defaultManager.cleanupSession)
	registerWithManager(e, defaultManager)
}

func registerWithManager(e *sdk.Extension, m *manager) {
	e.OnEvent(sdk.EventBeforeProviderRequest, m.onBeforeProviderRequest)
	e.OnEvent(sdk.EventBeforeProviderHeaders, m.onBeforeProviderHeaders)
	e.OnEvent(sdk.EventTurnEnd, m.onTurnEnd)
	e.OnEvent(sdk.EventAgentEnd, m.onAgentEnd)
	e.OnEvent(sdk.EventSessionShutdown, m.onSessionShutdown)

	e.RegisterCommand("fast", sdk.CommandOptions{
		Description: "Toggle fast service (OpenAI service_tier=priority or ultrafast, Anthropic speed=fast, Google priority)",
		GetArgumentCompletions: staticCompletions([]sdk.AutocompleteItem{
			{Value: "on", Label: "on", Description: "Enable fast mode (priority tier)"},
			{Value: "ultra", Label: "ultra", Description: "Enable Ultrafast (OpenAI API, or Codex and relay models that offer it)"},
			{Value: "off", Label: "off", Description: "Disable fast mode"},
			{Value: "status", Label: "status", Description: "Show fast mode status"},
		}),
		Handler: m.handleFastCommand,
	})
	e.RegisterCommand("slow", sdk.CommandOptions{
		Description: "Toggle slow mode: flex tier on OpenAI/Google; on Anthropic, standard/low priority",
		GetArgumentCompletions: staticCompletions([]sdk.AutocompleteItem{
			{Value: "on", Label: "on", Description: "Enable slow mode (flex tier)"},
			{Value: "off", Label: "off", Description: "Disable slow mode (standard service)"},
			{Value: "status", Label: "status", Description: "Show slow mode status"},
		}),
		Handler: m.handleSlowCommand,
	})
	e.RegisterCommand("extended-context", sdk.CommandOptions{
		Description: "Toggle extended context windows",
		GetArgumentCompletions: staticCompletions([]sdk.AutocompleteItem{
			{Value: "on", Label: "on", Description: "Enable larger context windows"},
			{Value: "off", Label: "off", Description: "Use default or standard-pricing context windows"},
			{Value: "status", Label: "status", Description: "Show extended context status"},
		}),
		Handler: m.handleExtendedContextCommand,
	})
	e.RegisterCommand("role", sdk.CommandOptions{
		Description: "Inspect or switch to a configured model role (@smol, @slow, @plan, @commit, @task, …)",
		GetArgumentCompletions: staticCompletions([]sdk.AutocompleteItem{
			{Value: "list", Label: "list", Description: "List model roles and their resolved models"},
			{Value: "default", Label: "default", Description: "Switch to the default role"},
			{Value: "smol", Label: "smol", Description: "Switch to the fast/smol role"},
			{Value: "slow", Label: "slow", Description: "Switch to the slow/thinking role"},
			{Value: "plan", Label: "plan", Description: "Switch to the plan/architect role"},
			{Value: "commit", Label: "commit", Description: "Switch to the commit role"},
			{Value: "task", Label: "task", Description: "Switch to the subtask role"},
		}),
		Handler: m.handleRoleCommand,
	})
}

// SetForcedToolChoice arms a turn-scoped forced tool_choice injection for sessionID.
// It is cleared automatically on EventTurnEnd and EventAgentEnd.
func SetForcedToolChoice(sessionID, toolName string) {
	defaultManager.setForcedToolChoice(sessionID, toolName)
}

// ClearForcedToolChoice clears any pending forced tool_choice for sessionID.
func ClearForcedToolChoice(sessionID string) {
	defaultManager.clearForcedToolChoice(sessionID)
}

// ForcedToolChoice returns the currently armed forced tool name for sessionID, or "".
func ForcedToolChoice(sessionID string) string {
	return defaultManager.getForcedTool(sessionID)
}

func (m *manager) stateLocked(sid string) *sessionState {
	st, ok := m.sessions[sid]
	if !ok {
		st = &sessionState{}
		m.sessions[sid] = st
	}
	return st
}

func (m *manager) cleanupSession(sid string) {
	if sid == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, sid)
	m.mu.Unlock()
}

func (m *manager) setForcedToolChoice(sid, toolName string) {
	if sid == "" {
		return
	}
	toolName = strings.TrimSpace(toolName)
	m.mu.Lock()
	defer m.mu.Unlock()
	if toolName == "" {
		if st, ok := m.sessions[sid]; ok {
			st.forcedTool = ""
		}
		return
	}
	m.stateLocked(sid).forcedTool = toolName
}

func (m *manager) clearForcedToolChoice(sid string) {
	if sid == "" {
		return
	}
	m.mu.Lock()
	if st, ok := m.sessions[sid]; ok {
		st.forcedTool = ""
	}
	m.mu.Unlock()
}

func (m *manager) getForcedTool(sid string) string {
	if sid == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.sessions[sid]; ok {
		return st.forcedTool
	}
	return ""
}

func (m *manager) getSessionTierOverride(sid string) string {
	if sid == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.sessions[sid]; ok {
		return st.serviceTier
	}
	return ""
}

func (m *manager) setSessionTierOverride(sid, tier string) {
	if sid == "" {
		return
	}
	m.mu.Lock()
	m.stateLocked(sid).serviceTier = tier
	m.mu.Unlock()
}

func (m *manager) getSessionExtendedContext(sid string) (bool, bool) {
	if sid == "" {
		return false, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.sessions[sid]; ok && st.extendedContext != nil {
		return *st.extendedContext, true
	}
	return false, false
}

func (m *manager) setSessionExtendedContext(sid string, enabled bool) {
	if sid == "" {
		return
	}
	m.mu.Lock()
	m.stateLocked(sid).extendedContext = new(enabled)
	m.mu.Unlock()
}

func sessionIDOf(ctx sdk.Context) string {
	id, err := ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return id
}

func normalizeTierValue(v any) string {
	s, _ := v.(string)
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none", "off", "normal", "standard":
		return ""
	case "fast", "priority":
		return TierPriority
	case "ultra", "ultrafast":
		return TierUltrafast
	case "slow", "flex":
		return TierFlex
	case "auto":
		return "auto"
	case "default":
		return "default"
	case "scale":
		return "scale"
	default:
		return ""
	}
}

func (m *manager) effectiveServiceTier(ctx sdk.Context, sid string, family wireFamily) (tier string, explicitOff bool) {
	if ov := m.getSessionTierOverride(sid); ov != "" {
		if ov == TierNone {
			return "", true
		}
		return ov, false
	}
	if Setting == nil {
		return "", false
	}
	// Check family-specific omp tier.* setting first, then generic serviceTier.
	var familyKey string
	switch family {
	case familyAnthropic:
		familyKey = "tier.anthropic"
	case familyOpenAICompletions, familyOpenAIResponses:
		familyKey = "tier.openai"
	case familyGoogle:
		familyKey = "tier.google"
	}
	if familyKey != "" {
		if t := normalizeTierValue(Setting(ctx, familyKey)); t != "" {
			return t, false
		}
	}
	if t := normalizeTierValue(Setting(ctx, "serviceTier")); t != "" {
		return t, false
	}
	return "", false
}

func (m *manager) hasAnyServiceTierConfigured(ctx sdk.Context, sid string) bool {
	if ov := m.getSessionTierOverride(sid); ov != "" {
		return true
	}
	if Setting == nil {
		return false
	}
	for _, key := range []string{"serviceTier", "tier.anthropic", "tier.openai", "tier.google"} {
		if normalizeTierValue(Setting(ctx, key)) != "" {
			return true
		}
	}
	return false
}

func (m *manager) isExtendedContextEnabled(ctx sdk.Context, sid string) bool {
	if v, ok := m.getSessionExtendedContext(sid); ok {
		return v
	}
	if Setting == nil {
		return false
	}
	if b, ok := Setting(ctx, "extendedContext").(bool); ok {
		return b
	}
	return false
}

// onBeforeProviderRequest mutates data["payload"] in place only when an active
// service tier, extended context, or forced tool_choice requires a wire change.
// When nothing is active, it returns (nil, nil) immediately without touching payload.
func (m *manager) onBeforeProviderRequest(ctx sdk.Context, data map[string]any) (any, error) {
	sid := sessionIDOf(ctx)
	forced := m.getForcedTool(sid)
	hasTier := m.hasAnyServiceTierConfigured(ctx, sid)
	extCtx := m.isExtendedContextEnabled(ctx, sid)

	// Fast no-op path: keep default/oracle requests 100% untouched.
	if forced == "" && !hasTier && !extCtx {
		return nil, nil
	}

	payload, ok := data["payload"].(map[string]any)
	if !ok || payload == nil {
		return nil, nil
	}

	family := detectWireFamily(ctx, payload, nil)
	provider := activeProviderLower(ctx)

	if hasTier && providerSupportsServiceTier(provider, family) {
		tier, explicitOff := m.effectiveServiceTier(ctx, sid, family)
		applyServiceTierToPayload(payload, family, tier, explicitOff)
	}

	if family == familyAnthropic && providerSupportsAnthropicBetas(provider) {
		var betas []string
		if hasTier {
			if tier, _ := m.effectiveServiceTier(ctx, sid, familyAnthropic); tier == TierPriority {
				betas = append(betas, FastModeBeta)
			}
		}
		if extCtx {
			betas = append(betas, ExtendedContextBeta)
		}
		if len(betas) > 0 {
			mergePayloadBetas(payload, betas...)
		}
	}

	if forced != "" {
		applyForcedToolChoice(payload, family, forced)
	}

	return nil, nil
}

// onBeforeProviderHeaders merges Anthropic beta headers (`fast-mode-2026-02-01`
// and/or `context-1m-2025-08-07`) into data["headers"] ONLY when an explicit
// `anthropic-beta` header is already present (PiG's getBetaFeatures treats any
// header as replacing computed OAuth/thinking betas; when absent, payload["betas"]
// set in onBeforeProviderRequest merges cleanly with PiG's computed betas).
func (m *manager) onBeforeProviderHeaders(ctx sdk.Context, data map[string]any) (any, error) {
	sid := sessionIDOf(ctx)
	extCtx := m.isExtendedContextEnabled(ctx, sid)
	hasTier := m.hasAnyServiceTierConfigured(ctx, sid)

	// Fast no-op path: do not touch headers at all when nothing is active.
	if !extCtx && !hasTier {
		return nil, nil
	}

	headers, ok := data["headers"].(map[string]any)
	if !ok || headers == nil {
		return nil, nil
	}

	family := detectWireFamily(ctx, nil, headers)
	if family != familyAnthropic || !providerSupportsAnthropicBetas(activeProviderLower(ctx)) {
		return nil, nil
	}
	if !hasAnthropicBetaHeader(headers) {
		return nil, nil
	}

	var betas []string
	if hasTier {
		tier, _ := m.effectiveServiceTier(ctx, sid, familyAnthropic)
		if tier == TierPriority {
			betas = append(betas, FastModeBeta)
		}
	}
	if extCtx {
		betas = append(betas, ExtendedContextBeta)
	}
	if len(betas) == 0 {
		return nil, nil
	}

	mergeAnthropicBetaHeaders(headers, betas...)
	return nil, nil
}

func (m *manager) onTurnEnd(ctx sdk.Context, _ map[string]any) (any, error) {
	m.clearForcedToolChoice(sessionIDOf(ctx))
	return nil, nil
}

func (m *manager) onAgentEnd(ctx sdk.Context, _ map[string]any) (any, error) {
	m.clearForcedToolChoice(sessionIDOf(ctx))
	return nil, nil
}

func (m *manager) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	m.cleanupSession(sessionIDOf(ctx))
	return nil, nil
}

// detectWireFamily determines the wire protocol family from model metadata
// (ctx.GetModelInfo / ctx.ModelProvider) with structural fallback on payload
// or headers.
func detectWireFamily(ctx sdk.Context, payload, headers map[string]any) wireFamily {
	var api, provider string
	if info, err := ctx.GetModelInfo(); err == nil && info != nil {
		provider = strings.TrimSpace(info.Provider)
		if mid := strings.TrimSpace(info.ID); mid != "" {
			if reg, rerr := ctx.ModelRegistry().Find(provider, mid); rerr == nil && reg != nil {
				api, _ = reg["api"].(string)
			}
		}
	}
	if provider == "" {
		provider = ctx.ModelProvider()
	}
	apiLower := strings.ToLower(strings.TrimSpace(api))
	provLower := strings.ToLower(strings.TrimSpace(provider))

	switch apiLower {
	case "anthropic-messages":
		return familyAnthropic
	case "openai-responses", "openai-codex-responses", "azure-openai-responses":
		return familyOpenAIResponses
	case "openai-completions":
		return familyOpenAICompletions
	}
	if strings.HasPrefix(apiLower, "google-") {
		return familyGoogle
	}

	// Structural detection from payload when API is absent or ambiguous.
	if payload != nil {
		if f := detectWireFamilyFromPayload(payload); f != familyUnknown {
			return f
		}
	}

	// Provider name fallback.
	switch provLower {
	case "anthropic":
		return familyAnthropic
	case "openai-codex", "azure-openai-responses":
		return familyOpenAIResponses
	case "openai":
		if payload != nil {
			if _, hasInput := payload["input"]; hasInput {
				return familyOpenAIResponses
			}
		}
		return familyOpenAICompletions
	case "google", "google-vertex", "google-gemini-cli", "google-antigravity":
		return familyGoogle
	}

	// Header fallback for before_provider_headers when model info is absent.
	if headers != nil {
		for k := range headers {
			kl := strings.ToLower(k)
			if kl == "anthropic-version" || kl == "anthropic-beta" {
				return familyAnthropic
			}
		}
	}

	return familyUnknown
}

func detectWireFamilyFromPayload(payload map[string]any) wireFamily {
	if tools, ok := payload["tools"].([]any); ok && len(tools) > 0 {
		for _, raw := range tools {
			tm, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, hasInputSchema := tm["input_schema"]; hasInputSchema {
				return familyAnthropic
			}
			if _, hasFD := tm["functionDeclarations"]; hasFD {
				return familyGoogle
			}
			if _, hasFD := tm["function_declarations"]; hasFD {
				return familyGoogle
			}
			if typ, _ := tm["type"].(string); typ == "function" {
				if _, hasFn := tm["function"].(map[string]any); hasFn {
					return familyOpenAICompletions
				}
				if _, hasName := tm["name"].(string); hasName {
					return familyOpenAIResponses
				}
			}
		}
	}
	if _, hasVer := payload["anthropic_version"]; hasVer {
		return familyAnthropic
	}
	if _, hasContents := payload["contents"]; hasContents {
		return familyGoogle
	}
	if _, hasInput := payload["input"]; hasInput {
		if _, hasMessages := payload["messages"]; !hasMessages {
			return familyOpenAIResponses
		}
	}
	return familyUnknown
}

// applyServiceTierToPayload applies the resolved service tier to payload in place.
func applyServiceTierToPayload(payload map[string]any, family wireFamily, tier string, explicitOff bool) {
	switch family {
	case familyAnthropic:
		if tier == TierPriority {
			payload["speed"] = "fast"
		} else if explicitOff || tier == TierFlex || tier == "" {
			// Remove speed when explicitly turned off or switched to slow/normal.
			if _, exists := payload["speed"]; exists {
				delete(payload, "speed")
			}
		}
	case familyOpenAICompletions, familyOpenAIResponses:
		if tier != "" {
			payload["service_tier"] = tier
		} else if explicitOff {
			if _, exists := payload["service_tier"]; exists {
				delete(payload, "service_tier")
			}
		}
	case familyGoogle:
		if tier == TierPriority || tier == TierFlex {
			payload["serviceTier"] = tier
		} else if explicitOff {
			if _, exists := payload["serviceTier"]; exists {
				delete(payload, "serviceTier")
			}
		}
	}
}

// mergeAnthropicBetaHeaders merges one or more beta tokens into headers'
// `anthropic-beta` CSV header, preserving key casing and deduplicating tokens.
// upstream: packages/ai/src/providers/anthropic-headers.ts:26-53
func mergeAnthropicBetaHeaders(headers map[string]any, extraBetas ...string) {
	if len(extraBetas) == 0 {
		return
	}
	targetKey := "anthropic-beta"
	var existing []string
	for k, v := range headers {
		if strings.EqualFold(k, "anthropic-beta") {
			targetKey = k
			if s, ok := v.(string); ok && s != "" {
				for part := range strings.SplitSeq(s, ",") {
					if trimmed := strings.TrimSpace(part); trimmed != "" {
						existing = append(existing, trimmed)
					}
				}
			}
			break
		}
	}

	seen := make(map[string]bool, len(existing)+len(extraBetas))
	merged := make([]string, 0, len(existing)+len(extraBetas))
	for _, b := range existing {
		if !seen[b] {
			seen[b] = true
			merged = append(merged, b)
		}
	}
	for _, b := range extraBetas {
		b = strings.TrimSpace(b)
		if b != "" && !seen[b] {
			seen[b] = true
			merged = append(merged, b)
		}
	}
	if len(merged) > 0 {
		headers[targetKey] = strings.Join(merged, ",")
	}
}

func activeProviderLower(ctx sdk.Context) string {
	if info, err := ctx.GetModelInfo(); err == nil && info != nil {
		if p := strings.ToLower(strings.TrimSpace(info.Provider)); p != "" {
			return p
		}
	}
	return strings.ToLower(strings.TrimSpace(ctx.ModelProvider()))
}

func providerSupportsAnthropicBetas(provider string) bool {
	return provider == "" || provider == "anthropic"
}

func providerSupportsServiceTier(provider string, family wireFamily) bool {
	if provider == "" {
		return true
	}
	switch family {
	case familyAnthropic:
		return provider == "anthropic"
	case familyOpenAICompletions, familyOpenAIResponses:
		switch provider {
		case "openai", "openai-codex", "azure-openai-responses", "azure-openai", "openrouter":
			return true
		}
		return false
	case familyGoogle:
		return provider == "google" || provider == "google-vertex"
	default:
		return false
	}
}

func hasAnthropicBetaHeader(headers map[string]any) bool {
	for k := range headers {
		if strings.EqualFold(k, "anthropic-beta") {
			return true
		}
	}
	return false
}

// mergePayloadBetas appends extraBetas into payload["betas"] (deduped), which PiG's
// ai/anthropic_client.go:379-411 converts to the anthropic-beta header while keeping
// PiG's computed OAuth/interleaved-thinking betas intact.
func mergePayloadBetas(payload map[string]any, extraBetas ...string) {
	var existing []string
	switch raw := payload["betas"].(type) {
	case []any:
		for _, item := range raw {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					existing = append(existing, trimmed)
				}
			}
		}
	case []string:
		for _, s := range raw {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				existing = append(existing, trimmed)
			}
		}
	case string:
		for part := range strings.SplitSeq(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				existing = append(existing, trimmed)
			}
		}
	}
	seen := make(map[string]bool, len(existing)+len(extraBetas))
	merged := make([]any, 0, len(existing)+len(extraBetas))
	for _, b := range existing {
		if !seen[b] {
			seen[b] = true
			merged = append(merged, b)
		}
	}
	for _, b := range extraBetas {
		b = strings.TrimSpace(b)
		if b != "" && !seen[b] {
			seen[b] = true
			merged = append(merged, b)
		}
	}
	if len(merged) > 0 {
		payload["betas"] = merged
	}
}

// applyForcedToolChoice injects wire-specific forced tool_choice into payload
// only when payload["tools"] is present and contains toolName.
func applyForcedToolChoice(payload map[string]any, family wireFamily, toolName string) {
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) == 0 {
		return
	}

	// Refine family from tools structure when possible so tool_choice matches
	// the exact wire shape of the tools array.
	toolFamily := detectToolChoiceShape(tools, family)
	if !payloadHasTool(tools, toolFamily, toolName) {
		return
	}

	switch toolFamily {
	case familyAnthropic:
		payload["tool_choice"] = map[string]any{
			"type": "tool",
			"name": toolName,
		}
	case familyOpenAICompletions:
		payload["tool_choice"] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": toolName,
			},
		}
	case familyOpenAIResponses:
		payload["tool_choice"] = map[string]any{
			"type": "function",
			"name": toolName,
		}
	case familyGoogle:
		tc, ok := payload["toolConfig"].(map[string]any)
		if !ok || tc == nil {
			tc = map[string]any{}
			payload["toolConfig"] = tc
		}
		tc["functionCallingConfig"] = map[string]any{
			"mode":                 "ANY",
			"allowedFunctionNames": []any{toolName},
		}
	}
}

func detectToolChoiceShape(tools []any, fallback wireFamily) wireFamily {
	for _, raw := range tools {
		tm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasInputSchema := tm["input_schema"]; hasInputSchema {
			return familyAnthropic
		}
		if _, hasFD := tm["functionDeclarations"]; hasFD {
			return familyGoogle
		}
		if _, hasFD := tm["function_declarations"]; hasFD {
			return familyGoogle
		}
		if typ, _ := tm["type"].(string); typ == "function" {
			if _, hasFn := tm["function"].(map[string]any); hasFn {
				return familyOpenAICompletions
			}
			if _, hasName := tm["name"].(string); hasName {
				return familyOpenAIResponses
			}
		}
	}
	return fallback
}

func payloadHasTool(tools []any, family wireFamily, toolName string) bool {
	for _, raw := range tools {
		tm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch family {
		case familyAnthropic:
			if name, _ := tm["name"].(string); name == toolName {
				return true
			}
		case familyOpenAICompletions:
			if fn, ok := tm["function"].(map[string]any); ok {
				if name, _ := fn["name"].(string); name == toolName {
					return true
				}
			}
		case familyOpenAIResponses:
			if name, _ := tm["name"].(string); name == toolName {
				return true
			}
		case familyGoogle:
			for _, key := range []string{"functionDeclarations", "function_declarations"} {
				decls, ok := tm[key].([]any)
				if !ok {
					continue
				}
				for _, d := range decls {
					if dm, ok := d.(map[string]any); ok {
						if name, _ := dm["name"].(string); name == toolName {
							return true
						}
					}
				}
			}
		default:
			if name, _ := tm["name"].(string); name == toolName {
				return true
			}
			if fn, ok := tm["function"].(map[string]any); ok {
				if name, _ := fn["name"].(string); name == toolName {
					return true
				}
			}
		}
	}
	return false
}

// --- Slash Commands ---

func (m *manager) handleFastCommand(ctx sdk.Context, args string) error {
	sid := sessionIDOf(ctx)
	arg := strings.ToLower(strings.TrimSpace(args))
	family := detectWireFamily(ctx, nil, nil)
	cur, _ := m.effectiveServiceTier(ctx, sid, family)

	switch arg {
	case "", "toggle":
		if cur == TierPriority || cur == TierUltrafast {
			m.setSessionTierOverride(sid, TierNone)
			notify(ctx, "Fast mode disabled.", "info")
		} else {
			m.setSessionTierOverride(sid, TierPriority)
			notify(ctx, "Fast mode enabled.", "info")
		}
	case "on":
		m.setSessionTierOverride(sid, TierPriority)
		notify(ctx, "Fast mode enabled.", "info")
	case "ultra", "ultrafast":
		m.setSessionTierOverride(sid, TierUltrafast)
		notify(ctx, "Ultrafast mode enabled.", "info")
	case "off":
		m.setSessionTierOverride(sid, TierNone)
		notify(ctx, "Fast mode disabled.", "info")
	case "status":
		status := "off"
		switch cur {
		case TierUltrafast:
			status = "ultra"
		case TierPriority:
			status = "on"
		}
		notify(ctx, fmt.Sprintf("Fast mode is %s.", status), "info")
	default:
		notify(ctx, fastUsage, "info")
	}
	return nil
}

func (m *manager) handleSlowCommand(ctx sdk.Context, args string) error {
	sid := sessionIDOf(ctx)
	arg := strings.ToLower(strings.TrimSpace(args))
	family := detectWireFamily(ctx, nil, nil)
	cur, _ := m.effectiveServiceTier(ctx, sid, family)

	switch arg {
	case "", "toggle":
		if cur == TierFlex {
			m.setSessionTierOverride(sid, TierNone)
			notify(ctx, "Slow mode off.", "info")
		} else {
			m.setSessionTierOverride(sid, TierFlex)
			notify(ctx, "Slow mode on: requests use the flex tier (lower cost, higher latency).", "info")
		}
	case "on":
		m.setSessionTierOverride(sid, TierFlex)
		notify(ctx, "Slow mode on: requests use the flex tier (lower cost, higher latency).", "info")
	case "off":
		m.setSessionTierOverride(sid, TierNone)
		notify(ctx, "Slow mode off.", "info")
	case "status":
		if cur == TierFlex {
			notify(ctx, "Slow mode is on (flex tier).", "info")
		} else {
			notify(ctx, "Slow mode is off.", "info")
		}
	default:
		notify(ctx, slowUsage, "info")
	}
	return nil
}

func (m *manager) handleExtendedContextCommand(ctx sdk.Context, args string) error {
	sid := sessionIDOf(ctx)
	arg := strings.ToLower(strings.TrimSpace(args))
	cur := m.isExtendedContextEnabled(ctx, sid)

	if arg == "status" {
		state := "off"
		if cur {
			state = "on"
		}
		notify(ctx, fmt.Sprintf("Extended context is %s.", state), "info")
		return nil
	}

	var next bool
	switch arg {
	case "", "toggle":
		next = !cur
	case "on":
		next = true
	case "off":
		next = false
	default:
		notify(ctx, extendedContextUsage, "info")
		return nil
	}

	m.setSessionExtendedContext(sid, next)
	if SetSetting != nil {
		_ = SetSetting(ctx, "extendedContext", next, false)
	}
	if next {
		notify(ctx, "Extended context enabled.", "info")
	} else {
		notify(ctx, "Extended context disabled.", "info")
	}
	return nil
}

func (m *manager) handleRoleCommand(ctx sdk.Context, args string) error {
	trimmed := strings.TrimSpace(args)
	lower := strings.ToLower(trimmed)
	if trimmed == "" || lower == "list" {
		configured := configuredModelRoles(ctx)
		roles := listKnownRoles(configured)
		var lines []string
		lines = append(lines, "Model roles:")
		for _, r := range roles {
			p, id, ok := ResolveRole(ctx, r)
			target := "(unset)"
			if ok {
				if p != "" {
					target = p + "/" + id
				} else {
					target = id
				}
			}
			if cfg := configured[r]; cfg != "" {
				lines = append(lines, fmt.Sprintf("  @%s -> %s (configured: %s)", r, target, cfg))
			} else if ok {
				lines = append(lines, fmt.Sprintf("  @%s -> %s", r, target))
			}
		}
		notify(ctx, strings.Join(lines, "\n"), "info")
		return nil
	}

	if strings.ContainsAny(trimmed, " \t") {
		notify(ctx, roleUsage, "info")
		return nil
	}

	provider, id, ok := ResolveRole(ctx, trimmed)
	if !ok {
		notify(ctx, fmt.Sprintf("Could not resolve model role %q.", trimmed), "warning")
		return nil
	}

	qualified := id
	if provider != "" {
		qualified = provider + "/" + id
	}

	switched := false
	if provider != "" {
		if model, err := ctx.ModelRegistry().Find(provider, id); err == nil && model != nil {
			if okSet, err := ctx.SetModel(model); err == nil && okSet {
				switched = true
			}
		}
	}
	if !switched {
		if okSet, err := ctx.SetModelNamed(qualified); err == nil && okSet {
			switched = true
		}
	}

	if switched {
		notify(ctx, fmt.Sprintf("Switched model to %s (role %s).", qualified, trimmed), "info")
	} else {
		notify(ctx, fmt.Sprintf("Resolved role %s to %s, but failed to switch model.", trimmed, qualified), "warning")
	}
	return nil
}

func notify(ctx sdk.Context, msg, level string) {
	ctx.Notify(msg, level)
}

func staticCompletions(items []sdk.AutocompleteItem) sdk.ArgumentCompletionsFunc {
	return func(prefix string) ([]sdk.AutocompleteItem, error) {
		p := strings.ToLower(strings.TrimSpace(prefix))
		if p == "" {
			return items, nil
		}
		var out []sdk.AutocompleteItem
		for _, item := range items {
			if strings.HasPrefix(strings.ToLower(item.Label), p) || strings.HasPrefix(strings.ToLower(item.Value), p) {
				out = append(out, item)
			}
		}
		return out, nil
	}
}
