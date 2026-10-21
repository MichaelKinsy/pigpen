package pi_goal_x

import (
	"strings"
	"testing"
)

func TestGoalAuditorSelector(t *testing.T) {
	tw(t, "goal-auditor-selector", "auditor selector offers current/default, authenticated models with ✓ marker, and manual fallback", func(t *testing.T) {
		choices := buildAuditorModelChoices([]auditorModelSummary{{"zeta", "slow", "Slow"}, {"alpha", "fast", ""}}, "alpha/fast", "zeta/slow")
		eq(t, choices[0].kind, "default", "kind")
		eq(t, strings.Contains(choices[0].label, "Current session / default"), true, "label")
		eq(t, strings.Contains(choices[0].label, "(zeta/slow)"), true, "default entry shows the session model")
		var models []auditorChoice
		manual := false
		for _, c := range choices {
			if c.kind == "model" {
				models = append(models, c)
			}
			if c.kind == "manual" {
				manual = true
			}
		}
		eq(t, len(models), 2, "both authenticated models listed")
		check, name := false, false
		for _, c := range models {
			if strings.HasPrefix(c.label, "✓ ") && strings.Contains(c.label, "alpha/fast") {
				check = true
			}
			if strings.Contains(c.label, "zeta/slow — Slow") {
				name = true
			}
		}
		eq(t, check, true, "exact configured model carries the ✓ marker")
		eq(t, name, true, "label includes the display name")
		eq(t, manual, true, "manual entry present")
	})
	tw(t, "goal-auditor-selector", "auditor selector: no ✓ when nothing is configured, default shows (system default)", func(t *testing.T) {
		choices := buildAuditorModelChoices([]auditorModelSummary{{"alpha", "fast", ""}}, "", "")
		eq(t, strings.Contains(choices[0].label, "(system default)"), true, "system default")
		eq(t, strings.HasPrefix(choices[0].label, "✓ "), false, "no ✓ on default when unset")
		eq(t, choices[1].kind == "model" && !strings.HasPrefix(choices[1].label, "✓ "), true, "no ✓ on models when unset")
	})
	tw(t, "goal-auditor-selector", "filterAuditorModelChoices narrows by provider/id/name and keeps default+manual", func(t *testing.T) {
		choices := buildAuditorModelChoices([]auditorModelSummary{{"zeta", "slow", ""}, {"alpha", "fast", ""}}, "", "")
		filtered := filterAuditorModelChoices(choices, "zeta")
		n, def, man := 0, false, false
		for _, c := range filtered {
			switch c.kind {
			case "model":
				n++
			case "default":
				def = true
			case "manual":
				man = true
			}
		}
		eq(t, n, 1, "one model")
		eq(t, def && man, true, "default+manual kept")
		eq(t, len(filterAuditorModelChoices(choices, "  ")), len(choices), "blank filter keeps everything")
	})
	tw(t, "goal-auditor-selector", "parseManualAuditorModel requires provider/model format", func(t *testing.T) {
		p, m, e := parseManualAuditorModel(" fireworks/accounts/model ")
		eq(t, [3]string{p, m, e}, [3]string{"fireworks", "accounts/model", ""}, "parsed")
		for _, bad := range []string{"model-without-provider", "/leading", "trailing/"} {
			_, _, e := parseManualAuditorModel(bad)
			eq(t, e != "", true, bad)
		}
	})
	tw(t, "goal-auditor-selector", "configuredAuditorModelKey joins provider/model only when both set", func(t *testing.T) {
		eq(t, configuredAuditorModelKey("a", "b"), "a/b", "both")
		eq(t, configuredAuditorModelKey("a", ""), "", "provider only")
		eq(t, configuredAuditorModelKey("", ""), "", "none")
	})
	tw(t, "goal-auditor-selector", "thinkingLevelChoices marks the current level and offers (default)", func(t *testing.T) {
		eq(t, thinkingLevelChoices("high")[5], "✓ high", "high")
		eq(t, thinkingLevelChoices("high")[0], "  (default)", "default")
		eq(t, thinkingLevelChoices("")[0], "  (default)", "default unset")
		eq(t, len(thinkingLevelChoices("")), 7, "default + six levels")
	})
}
