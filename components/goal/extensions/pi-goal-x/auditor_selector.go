package pi_goal_x

// ported from auditor-selector.ts: auditor model/thinking selector helpers.

import (
	"sort"
	"strings"
)

type auditorModelSummary struct{ provider, id, name string }

// auditorChoice is the AuditorChoice union: kind is "default", "model" or "manual".
type auditorChoice struct {
	kind     string
	label    string
	provider string
	model    string
}

var auditorThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh"}

func auditorModelLabel(m auditorModelSummary) string {
	if m.name != "" && m.name != m.id {
		return m.provider + "/" + m.id + " — " + m.name
	}
	return m.provider + "/" + m.id
}

// configuredAuditorModelKey is `${provider}/${model}` for the explicit configured model, or "" (undefined).
func configuredAuditorModelKey(provider, model string) string {
	if provider != "" && model != "" {
		return provider + "/" + model
	}
	return ""
}

func buildAuditorModelChoices(available []auditorModelSummary, configured, session string) []auditorChoice {
	models := append([]auditorModelSummary(nil), available...)
	sort.SliceStable(models, func(i, j int) bool {
		return localeCompare(auditorModelLabel(models[i]), auditorModelLabel(models[j])) < 0
	})
	marker := "  "
	if configured == "" && session != "" {
		marker = "✓ "
	}
	sessionText := " (system default)"
	if session != "" {
		sessionText = " (" + session + ")"
	}
	out := []auditorChoice{{kind: "default", label: marker + "Current session / default" + sessionText}}
	for _, m := range models {
		key := m.provider + "/" + m.id
		mark := "  "
		if configured == key {
			mark = "✓ "
		}
		out = append(out, auditorChoice{kind: "model", provider: m.provider, model: m.id, label: mark + auditorModelLabel(m)})
	}
	return append(out, auditorChoice{kind: "manual", label: "✎ Enter provider/model manually (advanced)"})
}

func filterAuditorModelChoices(choices []auditorChoice, filter string) []auditorChoice {
	normalized := strings.ToLower(jsTrim(filter))
	if normalized == "" {
		return choices
	}
	var out []auditorChoice
	for _, c := range choices {
		if c.kind == "default" || c.kind == "manual" || strings.Contains(strings.ToLower(c.label), normalized) {
			out = append(out, c)
		}
	}
	return out
}

// parseManualAuditorModel returns provider, model and "" on success, or an error message.
func parseManualAuditorModel(input string) (provider, model, errMsg string) {
	value := jsTrim(input)
	slash := strings.Index(value, "/")
	if slash <= 0 || slash == len(value)-1 {
		return "", "", "Auditor model must use provider/model format; unavailable models will fail clearly at audit time."
	}
	return value[:slash], value[slash+1:], ""
}

func thinkingLevelChoices(current string) []string {
	out := []string{}
	for _, level := range append([]string{"(default)"}, auditorThinkingLevels...) {
		if level == current {
			out = append(out, "✓ "+level)
		} else {
			out = append(out, "  "+level)
		}
	}
	return out
}
