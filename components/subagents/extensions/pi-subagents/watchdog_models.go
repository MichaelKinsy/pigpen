package pi_subagents

import (
	"errors"
	"regexp"
	"strings"
)

// Watchdog model selection (watchdog/model-selection.ts): which model reviews, and which one to recommend.

const strongWatchdogThinking = "high"

type wdStrongFamily struct {
	key     string
	label   string
	queries []string
}

var wdStrongFamilies = map[string]wdStrongFamily{
	"opus48": {"opus48", "Opus 4.8", []string{"anthropic/claude-opus-4-8", "anthropic/claude-opus-4.8", "anthropic/opus-4-8", "anthropic/opus-4.8"}},
	"gpt55":  {"gpt55", "GPT 5.5", []string{"openai-codex/gpt-5.5", "openai-codex/gpt-5-5", "openai/gpt-5.5", "openai/gpt-5-5"}},
}

type wdResolvedModelInput struct {
	Model         string
	Thinking      string // "" when the input carried no suffix
	RegistryModel map[string]any
}

type watchdogModelRecommendation struct {
	Source        string // "configured" or "suggested"
	Model         string
	Thinking      string
	Label         string
	Reason        string
	RegistryModel map[string]any
}

func wdRegistryEntries(ctx wdContext) []ModelInfo {
	available := ctx.Registry().GetAvailable()
	out := make([]ModelInfo, 0, len(available))
	for _, m := range available {
		out = append(out, modelInfoFromRegistry(m))
	}
	return out
}

func wdSplitProviderModel(value string) (provider, id string, ok bool) {
	i := strings.Index(value, "/")
	if i <= 0 || i == len(value)-1 {
		return "", "", false
	}
	return value[:i], value[i+1:], true
}

func wdAssertSupportedThinking(value, source string) (string, error) {
	if wdContains(thinkingLevelList, value) {
		return value, nil
	}
	return "", errors.New("Unsupported watchdog thinking '" + value + "' from " + source + "; expected " + strings.Join(thinkingLevelList, ", ") + ", false, or inherit.")
}

// parseWatchdogThinkingInput: undefined (nil or "") is unset; false and "false" are off; a level is itself; anything else is an
// error (the tool schema lets `true` through, the runtime does not).
func parseWatchdogThinkingInput(value any, source string) (wdThinking, error) {
	switch v := value.(type) {
	case nil:
		return wdThinking{}, nil
	case bool:
		if !v {
			return wdThinkingOff, nil
		}
		return wdThinking{}, errors.New("Unsupported watchdog thinking 'true' from " + source + "; expected " + strings.Join(thinkingLevelList, ", ") + ", false, or inherit.")
	case string:
		if v == "" {
			return wdThinking{}, nil
		}
		if v == "false" {
			return wdThinkingOff, nil
		}
		level, err := wdAssertSupportedThinking(v, source)
		return wdThinking{Level: level}, err
	}
	return wdThinking{}, errors.New("Unsupported watchdog thinking from " + source + ".")
}

func resolveWatchdogModelInput(ctx wdContext, rawModel string) (wdResolvedModelInput, error) {
	trimmed := jsTrim(rawModel)
	if trimmed == "" {
		return wdResolvedModelInput{}, errors.New("Watchdog model must be a non-empty provider/model value.")
	}
	preferred := ""
	if cur := ctx.CurrentModel(); cur != nil {
		preferred = wdModelProvider(cur)
	}
	resolved := resolveModelCandidate(trimmed, wdRegistryEntries(ctx), preferred)
	if resolved == "" {
		resolved = trimmed
	}
	baseModel, suffix := splitKnownThinkingSuffix(resolved)
	provider, id, ok := wdSplitProviderModel(baseModel)
	if !ok {
		return wdResolvedModelInput{}, errors.New("Watchdog model '" + rawModel + "' did not resolve to provider/model. Use a provider-qualified model such as openai-codex/gpt-5.5:high or anthropic/claude-opus-4-8:high.")
	}
	registryModel := ctx.Registry().Find(provider, id)
	if registryModel == nil {
		return wdResolvedModelInput{}, errors.New("Watchdog model '" + rawModel + "' was not found as '" + baseModel + "'.")
	}
	if !ctx.Registry().HasConfiguredAuth(registryModel) {
		return wdResolvedModelInput{}, errors.New("Watchdog model '" + baseModel + "' is not authenticated. Configure credentials for provider '" + provider + "' or choose an authenticated model.")
	}
	out := wdResolvedModelInput{Model: provider + "/" + id, RegistryModel: registryModel}
	if suffix != "" {
		thinking, err := wdAssertSupportedThinking(suffix[1:], "watchdog model suffix")
		if err != nil {
			return wdResolvedModelInput{}, err
		}
		out.Thinking = thinking
	}
	return out, nil
}

var (
	wdGPT55Re  = regexp.MustCompile(`^gpt-5-5(-\d{8}|-\d{4}-\d{2}-\d{2})?$`)
	wdOpus48Re = regexp.MustCompile(`^(claude-opus-4-8|opus-4-8)(-\d{8}|-\d{4}-\d{2}-\d{2})?$`)
)

func wdFamilyForModel(provider, id string) string {
	p, i := normalizeModelSegment(provider), normalizeModelSegment(id)
	if strings.Contains(p, "openai") && wdGPT55Re.MatchString(i) {
		return "gpt55"
	}
	if strings.Contains(p, "anthropic") && wdOpus48Re.MatchString(i) {
		return "opus48"
	}
	return ""
}

func wdCurrentProviderFamily(ctx wdContext) string {
	cur := ctx.CurrentModel()
	if cur == nil {
		return ""
	}
	p := normalizeModelSegment(wdModelProvider(cur))
	switch {
	case strings.Contains(p, "openai"):
		return "openai"
	case strings.Contains(p, "anthropic"):
		return "anthropic"
	}
	return ""
}

func wdStrongFamilyOrder(ctx wdContext) []string {
	current := ""
	if cur := ctx.CurrentModel(); cur != nil {
		current = wdFamilyForModel(wdModelProvider(cur), wdModelID(cur))
	}
	switch current {
	case "gpt55":
		return []string{"opus48"}
	case "opus48":
		return []string{"gpt55"}
	}
	switch wdCurrentProviderFamily(ctx) {
	case "openai":
		return []string{"opus48", "gpt55"}
	case "anthropic":
		return []string{"gpt55", "opus48"}
	}
	return []string{"gpt55", "opus48"}
}

func wdFindFamilyMatch(family string, available []ModelInfo) string {
	var matches []ModelInfo
	for _, m := range available {
		if wdFamilyForModel(m.Provider, m.ID) == family {
			matches = append(matches, m)
		}
	}
	if len(matches) == 1 {
		return matches[0].FullID
	}
	return ""
}

func wdResolveStrongCandidate(ctx wdContext, family string) *watchdogModelRecommendation {
	available := wdRegistryEntries(ctx)
	preference := wdStrongFamilies[family]
	queries := append([]string{}, preference.queries...)
	if match := wdFindFamilyMatch(family, available); match != "" {
		queries = append(queries, match)
	}
	for _, query := range queries {
		resolved, err := resolveWatchdogModelInput(ctx, query)
		if err != nil {
			continue
		}
		info := modelInfoFromRegistry(resolved.RegistryModel)
		if wdFamilyForModel(info.Provider, info.ID) != family {
			continue
		}
		if !wdContains(getSupportedThinkingLevels(&info), strongWatchdogThinking) {
			continue
		}
		current := "no current session model"
		if cur := ctx.CurrentModel(); cur != nil {
			current = wdFullModelID(cur)
		}
		return &watchdogModelRecommendation{Source: "suggested", Model: resolved.Model, Thinking: strongWatchdogThinking, Label: preference.label,
			Reason:        "Use " + preference.label + " with thinking high as a strong independent watchdog for " + current + ".",
			RegistryModel: resolved.RegistryModel}
	}
	return nil
}

func recommendStrongWatchdogModel(ctx wdContext) (watchdogModelRecommendation, error) {
	for _, family := range wdStrongFamilyOrder(ctx) {
		if rec := wdResolveStrongCandidate(ctx, family); rec != nil {
			return *rec, nil
		}
	}
	current := "the current session"
	if cur := ctx.CurrentModel(); cur != nil {
		current = wdFullModelID(cur)
	}
	return watchdogModelRecommendation{}, errors.New("No authenticated strong complementary watchdog model was found for " + current + ". Configure access to Opus 4.8 or GPT 5.5, then run the recommendation again.")
}

func recommendWatchdogModel(ctx wdContext, configured wdEndpoint) (watchdogModelRecommendation, error) {
	if configured.Model == "" {
		return recommendStrongWatchdogModel(ctx)
	}
	resolved, err := resolveWatchdogModelInput(ctx, configured.Model)
	if err != nil {
		return watchdogModelRecommendation{}, err
	}
	thinking := resolved.Thinking
	if thinking == "" {
		var arg any
		if configured.Thinking.Off {
			arg = false
		} else if configured.Thinking.Level != "" {
			arg = configured.Thinking.Level
		}
		parsed, err := parseWatchdogThinkingInput(arg, "watchdog input")
		if err != nil {
			return watchdogModelRecommendation{}, err
		}
		switch {
		case parsed.Level != "":
			thinking = parsed.Level
		default:
			thinking = "off"
		}
	}
	return watchdogModelRecommendation{Source: "configured", Model: resolved.Model, Thinking: thinking, Label: "configured watchdog",
		Reason:        "Keep the configured watchdog model and thinking; no replacement recommendation is needed. This is not a strength or independence assessment.",
		RegistryModel: resolved.RegistryModel}, nil
}

func formatWatchdogRecommendation(r watchdogModelRecommendation) string {
	label := "Recommended strong watchdog"
	if r.Source == "configured" {
		label = "Keep configured watchdog"
	}
	return label + ": " + r.Model + ":" + r.Thinking
}
