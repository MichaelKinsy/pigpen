package tintinweb_subagents

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The invocation snapshot of a spawn: which model and thinking level it ran with, and what the caller asked for that
// it did not get. upstream: invocation-config.ts resolveAgentInvocationConfig (the precedence), model-resolver.ts
// (resolveModel, describeModel) and the snapshot built in index.ts.

// resolvedInvocation is upstream resolveAgentInvocationConfig's model, thinking and maxTurns decisions: an agent file's
// value outranks the call's, which only fills gaps.
type resolvedInvocation struct {
	modelInput      string
	modelFromParams bool
	thinking        string
	maxTurns        *int
	// overriddenThinking and overriddenModel are the call's values an agent file's pin outranked.
	overriddenThinking, overriddenModel string
}

func resolveInvocation(cfg *agentConfig, callerModel, callerThinking string, callerMaxTurns int) resolvedInvocation {
	var out resolvedInvocation
	cfgModel, cfgThinking := "", ""
	var cfgMax *int
	if cfg != nil {
		cfgModel, cfgThinking, cfgMax = cfg.Model, cfg.Thinking, cfg.MaxTurns
	}
	if cfgThinking != "" && callerThinking != "" && cfgThinking != callerThinking {
		out.overriddenThinking = callerThinking
	}
	if cfgModel != "" && callerModel != "" && cfgModel != callerModel {
		out.overriddenModel = callerModel
	}
	out.modelInput = cfgModel
	if out.modelInput == "" {
		out.modelInput = callerModel
	}
	out.modelFromParams = cfgModel == "" && callerModel != ""
	out.thinking = cfgThinking
	if out.thinking == "" {
		out.thinking = callerThinking
	}
	switch {
	case cfgMax != nil:
		out.maxTurns = cfgMax
	case callerMaxTurns > 0:
		m := callerMaxTurns
		out.maxTurns = &m
	}
	return out
}

var (
	dotSeparator   = regexp.MustCompile(`\.`)
	partSeparators = regexp.MustCompile(`[\s\-/]+`)
	datePart       = regexp.MustCompile(`^\d{8}$`)
)

// resolveModelAmong resolves a model string against the available models: an exact "provider/modelId" first, then a
// fuzzy match (id exact > id contains > name contains > every part present), then the same model of another provider.
// It returns the model, or the "Model not found" text. upstream: model-resolver.ts resolveModel.
func resolveModelAmong(input string, available []map[string]any) (map[string]any, string) {
	sget := func(m map[string]any, k string) string { return msgStr(m[k]) }
	normalize := func(s string) string { return dotSeparator.ReplaceAllString(strings.ToLower(s), "-") }
	// 1. Exact match: "provider/modelId", only if available.
	if i := strings.Index(input, "/"); i >= 0 {
		for _, m := range available {
			if strings.EqualFold(sget(m, "provider")+"/"+sget(m, "id"), input) {
				return m, ""
			}
		}
	}
	// 2. Fuzzy match.
	query := normalize(input)
	var best map[string]any
	bestScore := 0.0
	for _, m := range available {
		id, name, full := normalize(sget(m, "id")), normalize(sget(m, "name")), normalize(sget(m, "provider")+"/"+sget(m, "id"))
		score := 0.0
		switch {
		case id == query || full == query:
			score = 100
		case strings.Contains(id, query) || strings.Contains(full, query):
			score = 60 + float64(len(query))/float64(len(id))*30
		case strings.Contains(name, query):
			score = 40 + float64(len(query))/float64(len(name))*20
		default:
			all := true
			for _, part := range partSeparators.Split(query, -1) {
				if !(datePart.MatchString(part) || strings.Contains(id, part) || strings.Contains(name, part) || strings.Contains(strings.ToLower(sget(m, "provider")), part)) {
					all = false
					break
				}
			}
			if all {
				score = 20
			}
		}
		if score > bestScore {
			bestScore, best = score, m
		}
	}
	if best != nil && bestScore >= 20 {
		return best, ""
	}
	// 3. Provider fallback: a "provider/modelId" query retries the bare id against all providers.
	if i := strings.Index(input, "/"); i >= 0 {
		if m, msg := resolveModelAmong(input[i+1:], available); msg == "" {
			return m, ""
		}
	}
	// 4. No match: list the available models.
	list := make([]string, 0, len(available))
	for _, m := range available {
		list = append(list, "  "+sget(m, "provider")+"/"+sget(m, "id"))
	}
	sort.Strings(list)
	return nil, fmt.Sprintf("Model not found: %q.\n\nAvailable models:\n%s", input, strings.Join(list, "\n"))
}

// buildInvocation is the snapshot of a spawn before its session reports the effective values. The model is the one the
// agent file or the call names (resolved against the available models), else the parent's; a spelling the caller used
// that names a different model than the one that won is kept as requestedModel.
func buildInvocation(ctx sdk.Context, res resolvedInvocation, background, isolated bool) *invocation {
	inv := &invocation{Thinking: res.thinking, RequestedThinking: res.overriddenThinking, Isolated: isolated, RunInBackground: background}
	// Explicit values only, and 0 (unlimited) is no limit to disclose rather than a misleading "max turns: 0".
	// upstream: index.ts normalizeMaxTurns(resolvedConfig.maxTurns).
	if res.maxTurns != nil && *res.maxTurns > 0 {
		inv.MaxTurns = res.maxTurns
	}
	var available []map[string]any
	guard(func() { available, _ = ctx.ModelRegistry().GetAvailable() })
	var model map[string]any
	if res.modelInput != "" {
		if m, msg := resolveModelAmong(res.modelInput, available); msg == "" {
			model = m
		}
	}
	if model != nil {
		d := describeModel(msgStr(model["provider"]), msgStr(model["id"]), msgStr(model["name"]))
		inv.ModelName, inv.ModelID = d.name, d.id
	} else if res.modelInput != "" {
		// Not resolvable here: the child resolves the spelling itself.
		inv.ModelName = modelLabel(res.modelInput)
	} else {
		guard(func() {
			if info, err := ctx.GetModelInfo(); err == nil && info != nil {
				d := describeModel(info.Provider, info.ID, info.Name)
				inv.ModelName, inv.ModelID = d.name, d.id
			}
		})
	}
	inv.RequestedModel = requestedModel(res.overriddenModel, available, model)
	return inv
}

// requestedModel is what the caller SPELLED, kept only if it names a different model than the one that won ("" when
// there is nothing to disclose). A spelling that resolves to nothing is still worth disclosing: it cannot have taken
// effect.
func requestedModel(asked string, available []map[string]any, won map[string]any) string {
	if asked == "" {
		return ""
	}
	m, msg := resolveModelAmong(asked, available)
	if msg != "" {
		return asked
	}
	if won == nil || msgStr(m["provider"]) != msgStr(won["provider"]) || msgStr(m["id"]) != msgStr(won["id"]) {
		return asked
	}
	return ""
}
