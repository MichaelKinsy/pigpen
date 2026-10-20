package pi_subagents

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Subagent model resolution (src/runs/shared/model-resolution.ts).

// inheritModel is INHERIT_MODEL: the sentinel model value that asks a subagent to inherit the parent session's model.
const inheritModel = "inherit"

// ModelSelectionEvidence is model-resolution.ts ModelSelectionEvidence.
type ModelSelectionEvidence struct{ Model, RequestedModel string }

// formatSubagentModelVerificationError is model-resolution.ts:14: "" when the observed model matches the launch candidate.
func formatSubagentModelVerificationError(expectedModel, observedModel string, available []ModelInfo, aliases map[string][]string) string {
	if len(available) == 0 {
		return ""
	}
	expectedBase, _ := splitKnownThinkingSuffix(expectedModel)
	if list, ok := aliases[expectedBase]; ok {
		for _, a := range list {
			if a == observedModel {
				return ""
			}
		}
	}
	observedBase, _ := splitKnownThinkingSuffix(observedModel)
	if expectedBase == observedBase {
		return ""
	}
	for _, e := range available {
		if e.FullID != expectedBase {
			continue
		}
		if e.ID == observedBase {
			return ""
		}
		idLeaf := e.ID[strings.LastIndex(e.ID, "/")+1:]
		fullLeaf := e.FullID[strings.LastIndex(e.FullID, "/")+1:]
		if idLeaf == observedBase || fullLeaf == observedBase {
			return ""
		}
		break
	}
	return fmt.Sprintf("model_verification_failed: native Pi child reported a different model than the launch candidate. Expected '%s' but observed '%s'. If you have independently verified this response ID identifies the requested model, declare the exact mapping in modelResponseAliases in ~/.pi/agent/extensions/subagent/config.json (see docs/configuration.md#modelresponsealiases). Use the resolved provider/model ID without its thinking suffix as the key. This leaves the outgoing request unchanged. Configuration changes affect new independent native runs; resumed native runs retain their launch-time declaration. External CLI adapters do not use this setting.", expectedModel, observedModel)
}

// normalizeParentModel is model-resolution.ts:45 for a decoded value.
func normalizeParentModel(v any) *ParentModel {
	switch x := v.(type) {
	case *jsObject:
		p, ok1 := x.str("provider")
		id, ok2 := x.str("id")
		if ok1 && ok2 && p != "" && id != "" {
			return &ParentModel{p, id}
		}
	case map[string]any:
		p, ok1 := x["provider"].(string)
		id, ok2 := x["id"].(string)
		if ok1 && ok2 && p != "" && id != "" {
			return &ParentModel{p, id}
		}
	}
	return nil
}

var (
	segSepRe     = regexp.MustCompile(`[._]+`)
	segDashRe    = regexp.MustCompile(`-+`)
	dashedDateRe = regexp.MustCompile(`^(.*)-(\d{4})-(\d{2})-(\d{2})$`)
	compactDate  = regexp.MustCompile(`^(.*)-(\d{4})(\d{2})(\d{2})$`)
)

// normalizeModelSegment case-folds, treats dots and underscores as dashes and collapses repeated separators.
func normalizeModelSegment(s string) string {
	s = strings.ToLower(s)
	s = segSepRe.ReplaceAllString(s, "-")
	s = segDashRe.ReplaceAllString(s, "-")
	s = strings.TrimPrefix(s, "-")
	return strings.TrimSuffix(s, "-")
}

func isPlausibleDateStamp(y, m, d string) bool {
	yy, _ := strconv.Atoi(y)
	mm, _ := strconv.Atoi(m)
	dd, _ := strconv.Atoi(d)
	return yy >= 1900 && yy <= 2099 && mm >= 1 && mm <= 12 && dd >= 1 && dd <= 31
}

func stripTrailingDateStamp(s string) string {
	if m := dashedDateRe.FindStringSubmatch(s); m != nil && isPlausibleDateStamp(m[2], m[3], m[4]) {
		return m[1]
	}
	if m := compactDate.FindStringSubmatch(s); m != nil && isPlausibleDateStamp(m[2], m[3], m[4]) {
		return m[1]
	}
	return s
}

func isRegisteredProvider(provider string, available []ModelInfo) bool {
	n := normalizeModelSegment(provider)
	for _, e := range available {
		if normalizeModelSegment(e.Provider) == n {
			return true
		}
	}
	return false
}

// splitQualifiedModelQuery splits `provider/id` only when the first path segment is a registered provider.
func splitQualifiedModelQuery(base string, available []ModelInfo) (queryProvider *string, queryIDRaw string) {
	if i := strings.Index(base, "/"); i != -1 {
		part := base[:i]
		if isRegisteredProvider(part, available) {
			p := normalizeModelSegment(part)
			return &p, base[i+1:]
		}
		return nil, base
	}
	for _, sep := range []string{":", "."} {
		i := strings.Index(base, sep)
		if i <= 0 {
			continue
		}
		part := base[:i]
		if !isRegisteredProvider(part, available) {
			continue
		}
		p := normalizeModelSegment(part)
		return &p, base[i+1:]
	}
	return nil, base
}

func resolveExactIDMatches(base string, available []ModelInfo, preferred string) string {
	var exact []ModelInfo
	for _, e := range available {
		if e.ID == base {
			exact = append(exact, e)
		}
	}
	if preferred != "" {
		for _, e := range exact {
			if e.Provider == preferred {
				return e.FullID
			}
		}
	}
	if len(exact) == 1 {
		return exact[0].FullID
	}
	return ""
}

func resolveBaseModelCandidate(base string, available []ModelInfo, preferred string) string {
	for _, e := range available {
		if e.FullID == base {
			return e.FullID
		}
	}
	queryProvider, _ := splitQualifiedModelQuery(base, available)
	if queryProvider == nil {
		if id := resolveExactIDMatches(base, available, preferred); id != "" {
			return id
		}
	}
	fuzzy := fuzzyResolveModel(base, available, preferred)
	if fuzzy != "" || queryProvider == nil {
		return fuzzy
	}
	// Some catalogs (OpenRouter's `openrouter/auto-beta`) repeat the provider inside the id.
	queryID := normalizeModelSegment(base)
	var matches []ModelInfo
	for _, e := range available {
		if normalizeModelSegment(e.ID) == queryID && normalizeModelSegment(e.Provider) == *queryProvider {
			matches = append(matches, e)
		}
	}
	if len(matches) == 1 {
		return matches[0].FullID
	}
	return ""
}

// fuzzyResolveModel resolves a base model id against the registry, tolerating separator, case and date-stamp differences.
// It returns the matched fullId, or "" when there is no match or the match is ambiguous across providers.
func fuzzyResolveModel(base string, available []ModelInfo, preferred string) string {
	queryProvider, queryIDRaw := splitQualifiedModelQuery(base, available)
	queryID := normalizeModelSegment(queryIDRaw)
	noDate := stripTrailingDateStamp(queryID)
	var candidates []ModelInfo
	for _, e := range available {
		id := normalizeModelSegment(e.ID)
		if id != queryID && stripTrailingDateStamp(id) != noDate {
			continue
		}
		if queryProvider != nil && normalizeModelSegment(e.Provider) != *queryProvider {
			continue
		}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		return ""
	}
	if preferred != "" {
		pn := normalizeModelSegment(preferred)
		for _, e := range candidates {
			if normalizeModelSegment(e.Provider) == pn {
				return e.FullID
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0].FullID
	}
	return ""
}

// resolveModelCandidate resolves a possibly-loose model id to a canonical `provider/id` (plus any thinking suffix).
func resolveModelCandidate(model string, available []ModelInfo, preferred string) string {
	if model == "" {
		return ""
	}
	if len(available) == 0 {
		return model
	}
	if whole := resolveBaseModelCandidate(model, available, preferred); whole != "" {
		return whole
	}
	base, suffix := splitKnownThinkingSuffix(model)
	if suffix == "" {
		return model
	}
	if b := resolveBaseModelCandidate(base, available, preferred); b != "" {
		return b + suffix
	}
	return model
}

func resolveSubagentModelCandidate(model string, available []ModelInfo, preferred string) string {
	if len(available) == 0 {
		return model
	}
	if whole := resolveBaseModelCandidate(model, available, preferred); whole != "" {
		return whole
	}
	base, suffix := splitKnownThinkingSuffix(model)
	if suffix != "" {
		if b := resolveBaseModelCandidate(base, available, preferred); b != "" {
			return b + suffix
		}
	}
	return ""
}

func suggestAlternateProviderModel(model string, available []ModelInfo) string {
	if len(available) == 0 {
		return ""
	}
	base, suffix := splitKnownThinkingSuffix(model)
	qp, idRaw := splitQualifiedModelQuery(base, available)
	if qp == nil {
		return ""
	}
	suggestion := resolveBaseModelCandidate(idRaw, available, "")
	if suggestion == "" {
		return ""
	}
	for _, e := range available {
		if e.FullID == suggestion {
			if normalizeModelSegment(e.Provider) == *qp {
				return ""
			}
			return suggestion + suffix
		}
	}
	return ""
}

func resolveRequiredSubagentModelCandidate(model string, available []ModelInfo, preferred string) (string, error) {
	if r := resolveSubagentModelCandidate(model, available, preferred); r != "" {
		return r, nil
	}
	hint := ""
	if s := suggestAlternateProviderModel(model, available); s != "" {
		hint = " Did you mean '" + s + "'?"
	}
	return "", fmt.Errorf("Unknown subagent model '%s' in the active Pi model registry.%s", model, hint)
}

// ResolveSubagentModelOverrideOptions is model-resolution.ts ResolveSubagentModelOverrideOptions.
type ResolveSubagentModelOverrideOptions struct {
	Scope  []ModelScopeCheckRule
	Source string // "explicit" or "inherited"; "" is inherited
	OnWarn func(ModelScopeViolation)
}

func defaultScopeWarn(ModelScopeViolation) {}

func throwForUnresolvedEnforcedInheritScope(scopes []ModelScopeCheckRule, includeMixed bool) error {
	for _, e := range scopes {
		if e.Enforce == nil || !*e.Enforce {
			continue
		}
		hit := false
		if includeMixed {
			for _, a := range e.Allow {
				hit = hit || a == inheritModel
			}
		} else {
			hit = len(e.Allow) == 1 && e.Allow[0] == inheritModel
		}
		if hit {
			origin := e.Origin
			if origin == "" {
				origin = "modelScope"
			}
			return fmt.Errorf("Cannot enforce subagent model scope (%s): 'inherit' requires a current parent session model.", origin)
		}
	}
	return nil
}

func enforceModelScopes(model string, scopes []ModelScopeCheckRule, source string, onWarn func(ModelScopeViolation)) error {
	var violations []*ModelScopeViolation
	for i := range scopes {
		if v := checkModelScope(model, &scopes[i], source); v != nil {
			violations = append(violations, v)
		}
	}
	for _, v := range violations {
		if v.Severity == "error" {
			return fmt.Errorf("%s", v.Message)
		}
	}
	for _, v := range violations {
		if onWarn != nil {
			onWarn(*v)
		} else {
			defaultScopeWarn(*v)
		}
	}
	return nil
}

func requestedModelString(v any) string {
	if s, ok := v.(string); ok {
		return jsTrim(s)
	}
	return ""
}

// resolveSubagentModelOverride is model-resolution.ts:310: the `--model` override passed to a spawned subagent. requested is
// nil, a bool or a string. An empty result is undefined.
func resolveSubagentModelOverride(requested any, parent *ParentModel, available []ModelInfo, preferred string, opts *ResolveSubagentModelOverrideOptions) (string, error) {
	if opts == nil {
		opts = &ResolveSubagentModelOverrideOptions{}
	}
	trimmed := requestedModelString(requested)
	explicit := ""
	if trimmed != "" && trimmed != inheritModel {
		explicit = trimmed
	}
	if parent == nil {
		if err := throwForUnresolvedEnforcedInheritScope(opts.Scope, explicit == "" || opts.Source == "inherited"); err != nil {
			return "", err
		}
	}
	var resolved string
	fromRegistry := explicit == ""
	if explicit == "" {
		if parent != nil {
			resolved = parent.Provider + "/" + parent.ID
		}
	} else {
		candidate := resolveSubagentModelCandidate(explicit, available, preferred)
		if opts.Source == "explicit" {
			if candidate != "" {
				resolved = candidate
			} else {
				r, err := resolveRequiredSubagentModelCandidate(explicit, available, preferred)
				if err != nil {
					return "", err
				}
				resolved = r
			}
			fromRegistry = true
		} else if candidate != "" {
			resolved, fromRegistry = candidate, true
		} else {
			resolved = explicit
		}
	}
	if resolved != "" && len(opts.Scope) > 0 && fromRegistry {
		source := "inherited"
		if explicit != "" && opts.Source != "" {
			source = opts.Source
		}
		if err := enforceModelScopes(resolved, opts.Scope, source, opts.OnWarn); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

// resolveEffectiveSubagentModel is model-resolution.ts:343.
func resolveEffectiveSubagentModel(explicitModel, agentModel any, parent *ParentModel, available []ModelInfo, preferred string, opts *ResolveSubagentModelOverrideOptions) (string, error) {
	o := ResolveSubagentModelOverrideOptions{}
	if opts != nil {
		o = *opts
	}
	source := o.Source
	if source == "" {
		source = "inherited"
		if explicitModel != nil {
			source = "explicit"
		}
	}
	req := explicitModel
	if req == nil {
		req = agentModel
	}
	first := o
	first.Source = source
	resolved, err := resolveSubagentModelOverride(req, parent, available, preferred, &first)
	if err != nil {
		return "", err
	}
	if resolved != "" || explicitModel == nil {
		return resolved, nil
	}
	second := o
	if second.Source == "" {
		second.Source = "inherited"
	}
	return resolveSubagentModelOverride(agentModel, parent, available, preferred, &second)
}

// ResolveModelSelectionOptions is model-resolution.ts ResolveModelSelectionOptions.
type ResolveModelSelectionOptions struct {
	Scope                  []ModelScopeCheckRule
	OnWarn                 func(ModelScopeViolation)
	PrimaryModelFromParent bool
	Origin                 string // "explicit", "inherited" or "configured"
}

// inheritsParentModel is model-resolution.ts:394.
func inheritsParentModel(explicitModel, agentModel any, parent *ParentModel) bool {
	req := explicitModel
	if req == nil {
		req = agentModel
	}
	t := requestedModelString(req)
	return parent != nil && (t == "" || t == inheritModel)
}

// resolveModelOrigin is model-resolution.ts:380.
func resolveModelOrigin(explicitModel, agentModel any, parent *ParentModel, fromParent bool, storedOrigin string) string {
	if storedOrigin != "" {
		return storedOrigin
	}
	if fromParent || inheritsParentModel(explicitModel, agentModel, parent) {
		return "inherited"
	}
	if t := requestedModelString(explicitModel); t != "" && t != inheritModel {
		return "explicit"
	}
	return "configured"
}

// resolveModelSelection is model-resolution.ts:404.
func resolveModelSelection(model string, available []ModelInfo, preferred string, opts *ResolveModelSelectionOptions) (ModelSelectionEvidence, error) {
	if opts == nil {
		opts = &ResolveModelSelectionOptions{}
	}
	if model == "" {
		if err := throwForUnresolvedEnforcedInheritScope(opts.Scope, true); err != nil {
			return ModelSelectionEvidence{}, err
		}
	}
	origin := opts.Origin
	if origin == "" {
		origin = "configured"
		if opts.PrimaryModelFromParent {
			origin = "inherited"
		}
	}
	requested := model
	if origin == "inherited" {
		requested = ""
	}
	if origin == "explicit" && model != "" {
		n, err := resolveRequiredSubagentModelCandidate(jsTrim(model), available, preferred)
		if err != nil {
			return ModelSelectionEvidence{}, err
		}
		if err := enforceModelScopes(n, opts.Scope, "explicit", opts.OnWarn); err != nil {
			return ModelSelectionEvidence{}, err
		}
		model = n
	}
	var resolved string
	if model != "" && (origin == "inherited" || origin == "explicit" || opts.PrimaryModelFromParent) {
		resolved = jsTrim(model)
	} else if model != "" {
		r, err := resolveRequiredSubagentModelCandidate(jsTrim(model), available, preferred)
		if err != nil {
			return ModelSelectionEvidence{}, err
		}
		resolved = r
	}
	if resolved != "" {
		for _, s := range opts.Scope {
			if s.Enforce != nil && *s.Enforce && s.Strict != nil && *s.Strict {
				if err := enforceModelScopes(resolved, opts.Scope, "inherited", opts.OnWarn); err != nil {
					return ModelSelectionEvidence{}, err
				}
				break
			}
		}
	}
	return ModelSelectionEvidence{Model: resolved, RequestedModel: requested}, nil
}

var (
	contextOverflowPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)context(?: length| window| limit)? (?:exceed|overflow|too long)`),
		regexp.MustCompile(`(?i)maximum context length`),
		regexp.MustCompile(`(?i)too many tokens`),
		regexp.MustCompile(`(?i)token limit`),
		regexp.MustCompile(`(?i)context_length_exceeded`),
		regexp.MustCompile(`(?i)length_required`),
		regexp.MustCompile(`(?i)maximum.*tokens`),
		regexp.MustCompile(`(?i)prompt.*too long`),
		regexp.MustCompile(`(?i)input.*too long`),
		regexp.MustCompile(`(?i)exceeded.*context`),
		regexp.MustCompile(`(?i)context.*overflow`),
	}
	toolFailureRe = regexp.MustCompile(`(?i)^[\w.:@/-]+ failed (?:(?:\(exit \d+\):)|(?:with exit code \d+))(?:\s|$)`)
)

// isContextOverflow classifies an error as a context overflow (model-resolution.ts:442).
func isContextOverflow(err string) bool {
	if err == "" {
		return false
	}
	if toolFailureRe.MatchString(jsTrim(err)) {
		return false
	}
	for _, p := range contextOverflowPatterns {
		if p.MatchString(err) {
			return true
		}
	}
	return false
}
