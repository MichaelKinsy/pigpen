package pi_subagents

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Launch rules (watchdog/rules.ts): subagents.watchdog.rules.roleModels says which models a role may be launched with.

type watchdogRuleViolation struct {
	Agent             string
	Summary           string
	Evidence          string
	RecommendedAction string
}

func loadWatchdogLaunchRules(cwd string) *wdRules { panic("not implemented") }

// watchdogGlobMatch: `*` matches any run of characters, `?` one character; anchored, case-sensitive.
func watchdogGlobMatch(pattern, value string) bool { panic("not implemented") }

// wdModelMatches returns the first pattern matching the model or its base (the model without a thinking suffix).
func wdModelMatches(patterns []string, model string) (string, bool) { panic("not implemented") }

// evaluateLaunchRule: deny wins over allow; an unknown model cannot be judged.
func evaluateLaunchRule(rules *wdRules, agent, model string) *watchdogRuleViolation {
	panic("not implemented")
}

func ruleViolationWarning(v watchdogRuleViolation) wdWarning { panic("not implemented") }

// applyWatchdogLaunchRules returns the refusal of a blocked launch; under the `warn` action it calls warn and returns "".
func applyWatchdogLaunchRules(cwd, agent, model string, warn func(watchdogRuleViolation)) string {
	panic("not implemented")
}

// sendRuleViolationWarning is for launch paths without a main watchdog runtime (background chain steps).
func sendRuleViolationWarning(ctx sdk.Context, v watchdogRuleViolation) { panic("not implemented") }

// watchdogLaunchRuleCheck applies subagents.watchdog.rules to a launch of agent a: the model it will run with is the call's, else
// the agent's, else the parent session's. A blocked launch returns its refusal; under the `warn` action the main watchdog (a
// foreground run) or the session (a background run, which has no watchdog runtime) shows the violation.
func watchdogLaunchRuleCheck(ctx sdk.Context, cwd string, a *AgentConfig, explicitModel string, background bool) string {
	panic("not implemented")
}
