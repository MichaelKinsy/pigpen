package pi_subagents

import (
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Launch rules (watchdog/rules.ts): subagents.watchdog.rules.roleModels says which models a role may be launched with.

type watchdogRuleViolation struct {
	Agent             string
	Summary           string
	Evidence          string
	RecommendedAction string
}

func loadWatchdogLaunchRules(cwd string) *wdRules {
	result := resolveWatchdogConfig(cwd, nil)
	if result.OK {
		return result.Config.Rules
	}
	return nil
}

// watchdogGlobMatch: `*` matches any run of characters, `?` one character; anchored, case-sensitive.
func watchdogGlobMatch(pattern, value string) bool {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(`[^\n\r\x{2028}\x{2029}]*`)
		case '?':
			b.WriteString(`[^\n\r\x{2028}\x{2029}]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

// wdModelMatches returns the first pattern matching the model or its base (the model without a thinking suffix).
func wdModelMatches(patterns []string, model string) (string, bool) {
	base, _ := splitKnownThinkingSuffix(model)
	for _, p := range patterns {
		if watchdogGlobMatch(p, model) || watchdogGlobMatch(p, base) {
			return p, true
		}
	}
	return "", false
}

// evaluateLaunchRule: deny wins over allow; an unknown model cannot be judged.
func evaluateLaunchRule(rules *wdRules, agent, model string) *watchdogRuleViolation {
	roleRule := rules.roleRule(agent)
	if roleRule == nil || model == "" {
		return nil
	}
	note := ""
	if roleRule.Note != "" {
		note = " " + roleRule.Note
	}
	if len(roleRule.Deny) > 0 {
		if denied, ok := wdModelMatches(roleRule.Deny, model); ok {
			action := "Choose a different model for this role."
			if len(roleRule.Allow) > 0 {
				action = "Use one of: " + strings.Join(roleRule.Allow, ", ") + "."
			}
			return &watchdogRuleViolation{
				Agent:             agent,
				Summary:           "Agent '" + agent + "' was launched with denied model '" + model + "'.",
				Evidence:          "subagents.watchdog.rules.roleModels." + agent + ".deny matches '" + denied + "'." + note,
				RecommendedAction: action,
			}
		}
	}
	if len(roleRule.Allow) == 0 {
		return nil
	}
	if _, ok := wdModelMatches(roleRule.Allow, model); ok {
		return nil
	}
	return &watchdogRuleViolation{
		Agent:             agent,
		Summary:           "Agent '" + agent + "' was launched with model '" + model + "', which is not in its allowed list.",
		Evidence:          "subagents.watchdog.rules.roleModels." + agent + ".allow is [" + strings.Join(roleRule.Allow, ", ") + "]." + note,
		RecommendedAction: "Use one of: " + strings.Join(roleRule.Allow, ", ") + ".",
	}
}

func ruleViolationWarning(v watchdogRuleViolation) wdWarning {
	return wdWarning{Severity: "concern", Category: "missed-constraint", Importance: "high", Source: "main",
		Agent: v.Agent, Summary: v.Summary, Evidence: v.Evidence, RecommendedAction: v.RecommendedAction}
}

// applyWatchdogLaunchRules returns the refusal of a blocked launch; under the `warn` action it calls warn and returns "".
func applyWatchdogLaunchRules(cwd, agent, model string, warn func(watchdogRuleViolation)) string {
	rules := loadWatchdogLaunchRules(cwd)
	violation := evaluateLaunchRule(rules, agent, model)
	if violation == nil {
		return ""
	}
	if rules != nil && rules.Action == "block" {
		return "Launch blocked by subagents.watchdog.rules: " + violation.Summary
	}
	if warn != nil {
		warn(*violation)
	}
	return ""
}

// sendRuleViolationWarning is for launch paths without a main watchdog runtime (background chain steps).
func sendRuleViolationWarning(ctx sdk.Context, v watchdogRuleViolation) {
	w := ruleViolationWarning(v)
	msg := createWatchdogWarningMessage(w, wdMessageOptions{Details: wdWarning{State: "displayed", DisplayedAt: isoNow()}})
	_ = ctx.SendCustomMessage(sdk.CustomMessage{CustomType: msg.CustomType, Content: msg.Content, Display: msg.Display, Details: msg.Details}, sdk.SendMessageOptions{DeliverAs: "steer"})
}

// watchdogLaunchRuleCheck applies subagents.watchdog.rules to a launch of agent a: the model it will run with is the call's, else
// the agent's, else the parent session's. A blocked launch returns its refusal; under the `warn` action the main watchdog (a
// foreground run) or the session (a background run, which has no watchdog runtime) shows the violation.
func watchdogLaunchRuleCheck(ctx sdk.Context, cwd string, a *AgentConfig, explicitModel string, background bool) string {
	model := jsTrim(explicitModel)
	if model == "" {
		model = a.Model
	}
	if model == "" && ctx.Model() != "" {
		model = ctx.ModelProvider() + "/" + ctx.Model()
	}
	return applyWatchdogLaunchRules(cwd, a.Name, model, func(v watchdogRuleViolation) {
		if !background && watchdogMain != nil {
			watchdogMain.rt.displayRuleWarning(v)
			return
		}
		sendRuleViolationWarning(ctx, v)
	})
}
