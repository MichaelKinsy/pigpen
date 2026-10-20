package pi_subagents


// The `watchdog.*` actions of the subagent tool (watchdog/tool-actions.ts).

var watchdogToolActions = []string{"watchdog.status", "watchdog.check", "watchdog.configure", "watchdog.recommend-model"}

type watchdogToolParams struct {
	Scope  string
	Target string
	Agent  string
	Model  string
	// Thinking is what the model passed: nil, a string, or a boolean (the schema lets `true` through; the runtime does not).
	Thinking any
	Cwd      string
}

type watchdogToolResult struct {
	Text    string
	IsError bool
}

func wdToolOK(text string) watchdogToolResult  { panic("not implemented") }
func wdToolErr(text string) watchdogToolResult { panic("not implemented") }

func wdToolScope(raw string) (string, error) { panic("not implemented") }

func wdParseTarget(p watchdogToolParams) (wdTarget, error) { panic("not implemented") }

type wdErrString string

func (e wdErrString) Error() string { panic("not implemented") }
func wdErr(s string) error          { panic("not implemented") }

// wdConfiguredValue is the model and thinking a configure action sets: each is left alone, set or cleared.
type wdConfiguredValue struct {
	Model       wdPatch[string]
	Thinking    wdPatch[wdThinking]
	Description string
}

func wdThinkingDescription(p wdPatch[wdThinking]) string { panic("not implemented") }

func wdResolveConfiguredValue(ctx wdContext, p watchdogToolParams, rt *watchdogRuntime) (wdConfiguredValue, error) {
	panic("not implemented")
}

// wdMainConfig is the config the watchdog runs with: the runtime's, else the settings'.
func wdMainConfig(ctx wdContext, rt *watchdogRuntime) *watchdogConfig { panic("not implemented") }

func wdBuildRecommendationText(ctx wdContext, rt *watchdogRuntime) (string, error) {
	panic("not implemented")
}

func wdBuildCheckText(rt *watchdogRuntime, ctx wdContext) (string, error) { panic("not implemented") }

// handleWatchdogToolAction runs one watchdog action; a failure is an error result, never a thrown one.
func handleWatchdogToolAction(action string, p watchdogToolParams, ctx wdContext, rt *watchdogRuntime) watchdogToolResult {
	panic("not implemented")
}

func wdRunToolAction(action string, p watchdogToolParams, ctx wdContext, rt *watchdogRuntime) (watchdogToolResult, error) {
	panic("not implemented")
}
