package pi_subagents


// The model review of a turn delta (watchdog/review.ts): a read-only agent that reads the delta, may inspect files, and records
// findings by calling watchdog_warn.

var wdAllowedToolNames = []string{"read", "grep", "find", "ls", "watchdog_warn", watchdogDiffToolName}

var wdWarnSchema = wdToolSchema(map[string]any{
	"severity":          map[string]any{"type": "string", "enum": wdEnumList(watchdogWarningSeverities), "description": "concern for actionable risk, blocker for a likely wrong or unsafe outcome"},
	"importance":        map[string]any{"type": "string", "enum": wdEnumList(watchdogWarningImportance), "description": "low or medium for user-only visibility; high when the parent model must receive the finding"},
	"summary":           map[string]any{"type": "string", "description": "One concise sentence naming the issue."},
	"evidence":          map[string]any{"type": "string", "description": "Specific evidence from the turn delta or inspected files."},
	"recommendedAction": map[string]any{"type": "string", "description": "Specific action the parent should take before accepting or continuing."},
	"category":          map[string]any{"type": "string", "enum": wdEnumList(watchdogWarningCategories)},
}, []string{"severity", "importance", "summary", "evidence", "recommendedAction"})

var wdAskSchema = wdToolSchema(map[string]any{
	"question": map[string]any{"type": "string"},
	"evidence": map[string]any{"type": "string"},
}, []string{"question", "evidence"})

// watchdogReviewSelection is the model a review runs on.
type watchdogReviewSelection struct {
	Model         map[string]any
	ThinkingLevel string
	Auth          wdAuth
	Explicit      bool
}

// wdAuthError is a failed model authentication; Cause is the registry's own error when it had one.
type wdAuthError struct {
	message string
	cause   error
}

func (e *wdAuthError) Error() string { panic("not implemented") }
func (e *wdAuthError) Unwrap() error { panic("not implemented") }

func wdAssertThinkingLevel(value, source string) (string, error) { panic("not implemented") }

func wdContextThinkingLevel(ctx wdContext, current string) string { panic("not implemented") }

func wdResolveReviewThinking(modelString string, configThinking wdThinking, ctx wdContext, allowContextThinking bool, current string) (string, error) {
	panic("not implemented")
}

func wdResolveConfiguredModel(ctx wdContext, rawModel string) (model map[string]any, modelString string, err error) {
	panic("not implemented")
}

func wdResolveReviewAuth(ctx wdContext, model map[string]any) (wdAuth, error) {
	panic("not implemented")
}

func resolveWatchdogReviewModel(ctx wdContext, config *watchdogConfig, currentThinking string) (watchdogReviewSelection, error) {
	panic("not implemented")
}

func wdNonEmptyField(value, field string) (string, error) { panic("not implemented") }

func wdToWatchdogWarning(args *jsObject) (wdWarning, error) { panic("not implemented") }

func wdWarnTool(emit func(wdWarning) bool) wdAgentTool { panic("not implemented") }

// formatWatchdogCwdSection refuses a cwd that could escape the system prompt section it is placed in.
func formatWatchdogCwdSection(cwd string) (string, error) { panic("not implemented") }

type wdPromptOptions struct {
	HasScope bool
	Guidance string
	HasDiff  bool
}

func buildWatchdogSystemPrompt(cwd string, opts wdPromptOptions) (string, error) {
	panic("not implemented")
}

func wdBuildReviewPrompt(req watchdogReviewRequest, sel watchdogReviewSelection) string {
	panic("not implemented")
}

// wdReviewOptions are the seams of the main review (CreateMainWatchdogReviewOptions).
type wdReviewOptions struct {
	StreamFn         wdStreamFn
	ReadOnlyTools    func(cwd string) []wdAgentTool
	GetThinkingLevel func() string
	DiffBaseline     func() *watchdogDiffBaseline
}

// wdStreamWithAuth is the stream function the agent calls: the options of the model request carry the review model's credentials.
func wdStreamWithAuth(ctx wdContext, base wdStreamFn, auth wdAuth, guard func() error) wdStreamFn {
	panic("not implemented")
}

// createMainWatchdogReview returns the review function: it runs the agent on the delta and reports the stop reason, a failure's
// text, or the clarification the reviewer asked for. Warnings reach the runtime through the request's emitWarning.
func createMainWatchdogReview(provider func() wdContext, opts wdReviewOptions) watchdogReviewFunc {
	panic("not implemented")
}

func runWatchdogAttempt(ctx wdContext, req watchdogReviewRequest, opts wdReviewOptions) (*watchdogReviewResult, error) {
	panic("not implemented")
}

// an intentional yield also stops mixed tool batches

// The agent may enter one final loop iteration after an aborted mixed tool batch: never send it to the provider after an
// intentional yield.
