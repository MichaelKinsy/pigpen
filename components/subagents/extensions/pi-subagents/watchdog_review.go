package pi_subagents

import (
	"errors"
	"strings"
	"unicode"
)

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

func (e *wdAuthError) Error() string { return e.message }
func (e *wdAuthError) Unwrap() error { return e.cause }

func wdAssertThinkingLevel(value, source string) (string, error) {
	if wdContains(thinkingLevelList, value) {
		return value, nil
	}
	return "", errors.New("Unsupported watchdog thinking level '" + value + "' from " + source + "; expected " + strings.Join(thinkingLevelList, ", ") + " or false.")
}

func wdContextThinkingLevel(ctx wdContext, current string) string {
	if current != "" {
		return current
	}
	if l := ctx.ThinkingLevel(); wdContains(thinkingLevelList, l) {
		return l
	}
	return ""
}

func wdResolveReviewThinking(modelString string, configThinking wdThinking, ctx wdContext, allowContextThinking bool, current string) (string, error) {
	var cfg any
	if configThinking.Off {
		cfg = false
	} else if configThinking.Level != "" {
		cfg = configThinking.Level
	}
	if eff := resolveEffectiveThinking(modelString, cfg); eff != "" {
		return wdAssertThinkingLevel(eff, "watchdog model/config")
	}
	if configThinking.Off {
		return "off", nil
	}
	if configThinking.Level != "" {
		return wdAssertThinkingLevel(configThinking.Level, "watchdog config")
	}
	if allowContextThinking {
		if l := wdContextThinkingLevel(ctx, current); l != "" {
			return l, nil
		}
	}
	return "off", nil
}

func wdResolveConfiguredModel(ctx wdContext, rawModel string) (model map[string]any, modelString string, err error) {
	preferred := ""
	if cur := ctx.CurrentModel(); cur != nil {
		preferred = wdModelProvider(cur)
	}
	noMatch := errors.New("Configured watchdog model '" + rawModel + "' did not match exactly one authenticated available model. Use provider/model or configure credentials for the intended provider.")
	resolved := resolveModelCandidate(rawModel, wdRegistryEntries(ctx), preferred)
	if resolved == "" {
		return nil, "", noMatch
	}
	base, _ := splitKnownThinkingSuffix(resolved)
	provider, id, ok := wdSplitProviderModel(base)
	if !ok {
		return nil, "", noMatch
	}
	model = ctx.Registry().Find(provider, id)
	if model == nil {
		return nil, "", errors.New("Configured watchdog model '" + rawModel + "' was not found as '" + base + "'.")
	}
	if !ctx.Registry().HasConfiguredAuth(model) {
		return nil, "", errors.New("Configured watchdog model '" + base + "' is not authenticated. Configure credentials for provider '" + provider + "' or choose an authenticated model.")
	}
	return model, resolved, nil
}

func wdResolveReviewAuth(ctx wdContext, model map[string]any) (wdAuth, error) {
	auth, err := ctx.Registry().GetAPIKeyAndHeaders(model)
	if err != nil {
		return wdAuth{}, &wdAuthError{message: "Watchdog model auth failed for " + wdFullModelID(model) + ": " + err.Error(), cause: err}
	}
	if !auth.OK {
		return wdAuth{}, &wdAuthError{message: "Watchdog model auth failed for " + wdFullModelID(model) + ": " + auth.Error}
	}
	return auth, nil
}

func resolveWatchdogReviewModel(ctx wdContext, config *watchdogConfig, currentThinking string) (watchdogReviewSelection, error) {
	if config.Main.Model != "" {
		model, modelString, err := wdResolveConfiguredModel(ctx, config.Main.Model)
		if err != nil {
			return watchdogReviewSelection{}, err
		}
		thinking, err := wdResolveReviewThinking(modelString, config.Main.Thinking, ctx, false, currentThinking)
		if err != nil {
			return watchdogReviewSelection{}, err
		}
		auth, err := wdResolveReviewAuth(ctx, model)
		if err != nil {
			return watchdogReviewSelection{}, err
		}
		return watchdogReviewSelection{Model: model, ThinkingLevel: thinking, Auth: auth, Explicit: true}, nil
	}
	current := ctx.CurrentModel()
	if current == nil {
		return watchdogReviewSelection{}, errors.New("Main watchdog review cannot run because the current Pi session model is unavailable and subagents.watchdog.main.model is not configured.")
	}
	thinking, err := wdResolveReviewThinking(wdFullModelID(current), config.Main.Thinking, ctx, true, currentThinking)
	if err != nil {
		return watchdogReviewSelection{}, err
	}
	auth, err := wdResolveReviewAuth(ctx, current)
	if err != nil {
		return watchdogReviewSelection{}, err
	}
	return watchdogReviewSelection{Model: current, ThinkingLevel: thinking, Auth: auth}, nil
}

func wdNonEmptyField(value, field string) (string, error) {
	trimmed := jsTrim(value)
	if trimmed == "" {
		return "", errors.New("watchdog_warn." + field + " must be a non-empty string.")
	}
	return trimmed, nil
}

func wdToWatchdogWarning(args *jsObject) (wdWarning, error) {
	w := wdWarning{Severity: wdPropStr(args, "severity"), Importance: wdPropStr(args, "importance"), Category: wdPropStr(args, "category"), Source: "main"}
	if w.Category == "" {
		w.Category = "other"
	}
	var err error
	if w.Summary, err = wdNonEmptyField(wdPropStr(args, "summary"), "summary"); err != nil {
		return w, err
	}
	if w.Evidence, err = wdNonEmptyField(wdPropStr(args, "evidence"), "evidence"); err != nil {
		return w, err
	}
	if w.RecommendedAction, err = wdNonEmptyField(wdPropStr(args, "recommendedAction"), "recommendedAction"); err != nil {
		return w, err
	}
	return w, nil
}

func wdWarnTool(emit func(wdWarning) bool) wdAgentTool {
	return wdAgentTool{
		Spec: wdToolSpec{Name: "watchdog_warn", Parameters: wdWarnSchema, Description: strings.Join([]string{
			"Emit one actionable main-session watchdog warning.",
			"Set importance explicitly: low or medium for user-only visibility, high only when the parent model must receive the finding.",
			"Do not use for nits, praise, informational notes, or clean reviews.",
		}, " ")},
		Execute: func(args *jsObject) (string, error) {
			w, err := wdToWatchdogWarning(args)
			if err != nil {
				return "", err
			}
			if emit(w) {
				return "Watchdog warning recorded.", nil
			}
			return "Watchdog warning was ignored by the runtime guard because it was stale, duplicate, or over budget.", nil
		},
	}
}

// formatWatchdogCwdSection refuses a cwd that could escape the system prompt section it is placed in.
func formatWatchdogCwdSection(cwd string) (string, error) {
	for _, r := range cwd {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r == '<' || r == '>' {
			return "", errors.New("Watchdog cwd cannot contain control, line-separator, or angle-bracket characters.")
		}
	}
	return "<cwd>\n" + cwd + "\n</cwd>", nil
}

type wdPromptOptions struct {
	HasScope bool
	Guidance string
	HasDiff  bool
}

func buildWatchdogSystemPrompt(cwd string, opts wdPromptOptions) (string, error) {
	guidance := jsTrim(opts.Guidance)
	section, err := formatWatchdogCwdSection(cwd)
	if err != nil {
		return "", err
	}
	tools := "read, grep, find, and ls"
	if opts.HasDiff {
		tools = "read, grep, find, ls, and watchdog_diff (the full repo diff since the session baseline; pass a path to narrow it)"
	}
	lines := []string{
		"You are the main-session subagent watchdog for Pi.",
		"Review only the supplied parent turn delta. Inspect repository files only when needed to verify a concrete concern.",
	}
	if opts.HasScope {
		lines = append(lines, "Use the Current scope record alongside supplied activity evidence; an unrelated user question does not cancel older authorized work.")
	}
	lines = append(lines,
		"You are read-only. You may use "+tools+". Do not edit files, run shell commands, spawn agents, or mutate state.",
		"Emit warnings only by calling watchdog_warn. Freeform assistant text is ignored and must not be used to report warnings.",
		"Emit only actionable concerns or blockers: missed user constraints, correctness risks, test gaps that matter, unsafe changes, stale facts, loop risks, or scope drift.",
		"Do not emit nits, style preferences, unsupported guesses, informational notes, praise, or summaries.",
		"If the turn is clean, call no tools and end normally.",
		"Use severity='blocker' only when the issue should stop acceptance until addressed; otherwise use severity='concern'.",
	)
	if guidance != "" {
		lines = append(lines, "\nStanding instructions from WATCHDOG.md (project first, then user):\n"+guidance)
	}
	lines = append(lines, "\n"+section)
	return strings.Join(lines, "\n"), nil
}

func wdBuildReviewPrompt(req watchdogReviewRequest, sel watchdogReviewSelection) string {
	return strings.Join([]string{
		"Review this parent-session turn delta for subagent-watchdog-worthy issues.",
		"Review id: " + itoa(req.ReviewID) + "; epoch: " + itoa(req.Epoch) + "; review model: " + wdFullModelID(sel.Model) + "; thinking: " + sel.ThinkingLevel + ".",
		"Call watchdog_warn for each qualifying concern or blocker. Call no tools when clean.",
		"<turn_delta>",
		req.Delta,
		"</turn_delta>",
	}, "\n\n")
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
	if base == nil {
		base = func(model map[string]any, llm wdLLMContext, opts wdStreamOptions) map[string]any {
			return ctx.Registry().StreamSimple(model, llm, opts)
		}
	}
	return func(model map[string]any, llm wdLLMContext, opts wdStreamOptions) map[string]any {
		if guard != nil {
			if err := guard(); err != nil {
				return map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": err.Error()}
			}
		}
		if auth.APIKey != "" {
			opts.APIKey = auth.APIKey
		}
		if auth.Env != nil || opts.Env != nil {
			env := map[string]any{}
			for k, v := range auth.Env {
				env[k] = v
			}
			for k, v := range opts.Env {
				env[k] = v
			}
			opts.Env = env
		}
		headers := map[string]any{}
		baseURL, _ := model["baseUrl"].(string)
		for k, v := range opencodeSessionHeaders(wdModelProvider(model), baseURL, ctx.SessionID()) {
			headers[k] = v
		}
		for k, v := range opts.Headers {
			headers[k] = v
		}
		for k, v := range auth.Headers {
			headers[k] = v
		}
		opts.Headers = headers
		return base(model, llm, opts)
	}
}

// createMainWatchdogReview returns the review function: it runs the agent on the delta and reports the stop reason, a failure's
// text, or the clarification the reviewer asked for. Warnings reach the runtime through the request's emitWarning.
func createMainWatchdogReview(provider func() wdContext, opts wdReviewOptions) watchdogReviewFunc {
	return func(req watchdogReviewRequest) (*watchdogReviewResult, error) {
		ctx := provider()
		if ctx == nil {
			return nil, errors.New("Main watchdog review cannot run without an active Pi extension context.")
		}
		if wdClosed(ctx.Signal()) || wdClosed(req.Signal) {
			return &watchdogReviewResult{StopReason: "aborted"}, nil
		}
		result, err := runWatchdogAttempt(ctx, req, opts)
		var authErr *wdAuthError
		if errors.As(err, &authErr) && authErr.cause != nil {
			return nil, authErr.cause
		}
		return result, err
	}
}

func runWatchdogAttempt(ctx wdContext, req watchdogReviewRequest, opts wdReviewOptions) (*watchdogReviewResult, error) {
	current := ""
	if opts.GetThinkingLevel != nil {
		current = opts.GetThinkingLevel()
	}
	selection, err := resolveWatchdogReviewModel(ctx, req.Config, current)
	if err != nil {
		return nil, err
	}
	aborted := func() bool { return wdClosed(ctx.Signal()) || wdClosed(req.Signal) }
	if aborted() {
		return &watchdogReviewResult{StopReason: "aborted"}, nil
	}
	var baseline *watchdogDiffBaseline
	if opts.DiffBaseline != nil {
		baseline = opts.DiffBaseline()
	}
	var clarification *watchdogClarification
	warned := false
	var agent *wdAgent

	emit := req.EmitWarning
	if req.AllowClarification {
		emit = func(w wdWarning) bool {
			if clarification != nil {
				return false
			}
			accepted := req.EmitWarning(w)
			warned = warned || accepted
			return accepted
		}
	}
	readOnly := opts.ReadOnlyTools
	if readOnly == nil {
		readOnly = wdReadOnlyTools
	}
	var tools []wdAgentTool
	for _, tool := range readOnly(ctx.Cwd()) {
		if wdContains(wdAllowedToolNames, tool.Spec.Name) && tool.Spec.Name != "watchdog_warn" {
			tools = append(tools, tool)
		}
	}
	tools = append(tools, wdWarnTool(emit))
	if baseline != nil {
		diffTool := createWatchdogDiffTool(baseline, false)
		def := watchdogDiffToolDefinition(diffTool)
		tools = append(tools, wdAgentTool{Spec: wdToolSpec{Name: def.Name, Description: def.Description, Parameters: def.Parameters},
			Execute: func(args *jsObject) (string, error) {
				return diffTool.execute(watchdogDiffParams{Path: wdPropStr(args, "path"), Stat: wdPropV(args, "stat") == true})
			}})
	}
	if req.AllowClarification {
		tools = append(tools, wdAgentTool{
			Spec: wdToolSpec{Name: "watchdog_ask", Parameters: wdAskSchema, Description: "Send one focused question when missing task status, intent or other orchestrator context prevents a concrete review judgment. Yields and ends this review so the orchestrator can handle the message and continue; no answer or follow-up review is required. Never ask for approval or permission, or after recording a warning. Question and evidence are capped at 1000 and 2000 characters."},
			Execute: func(args *jsObject) (string, error) {
				question, evidence := wdPropStr(args, "question"), wdPropStr(args, "evidence")
				if warned || clarification != nil || aborted() {
					return "", errors.New("Clarification unavailable after warning, yield, or cancellation.")
				}
				if jsTrim(question) == "" || jsTrim(evidence) == "" {
					return "", errors.New("A focused question and concrete evidence are required.")
				}
				clarification = &watchdogClarification{Question: boundWatchdogReviewText(jsTrim(question), 1_000), Evidence: boundWatchdogReviewText(jsTrim(evidence), 2_000)}
				agent.abort() // an intentional yield also stops mixed tool batches
				return "Review yielded for clarification.", nil
			},
		})
	}
	prompt, err := buildWatchdogSystemPrompt(ctx.Cwd(), wdPromptOptions{HasScope: req.HasScope, Guidance: loadWatchdogGuidance(ctx.Cwd(), req.Config.GuidanceWatchdogMd), HasDiff: baseline != nil})
	if err != nil {
		return nil, err
	}
	stream := wdStreamWithAuth(ctx, opts.StreamFn, selection.Auth, func() error {
		// The agent may enter one final loop iteration after an aborted mixed tool batch: never send it to the provider after an
		// intentional yield.
		if clarification != nil {
			return errors.New("Watchdog review yielded for clarification.")
		}
		return nil
	})
	agent = newWdAgent(prompt, selection.Model, selection.ThinkingLevel, tools, stream)
	agent.apiKey = selection.Auth.APIKey
	agent.beforeToolCall = func(name string) (string, bool) {
		if clarification == nil && (wdContains(wdAllowedToolNames, name) || (req.AllowClarification && name == "watchdog_ask")) {
			return "", false
		}
		return "Watchdog reviews are read-only; tool '" + name + "' is not allowed.", true
	}
	stopWatching := make(chan struct{})
	for _, sig := range []<-chan struct{}{ctx.Signal(), req.Signal} {
		if sig == nil {
			continue
		}
		go func(sig <-chan struct{}) {
			select {
			case <-sig:
				agent.abort()
			case <-stopWatching:
			}
		}(sig)
	}
	func() {
		defer close(stopWatching)
		if aborted() {
			return
		}
		agent.prompt(wdBuildReviewPrompt(req, selection))
	}()
	if aborted() {
		return &watchdogReviewResult{StopReason: "aborted"}, nil
	}
	if clarification != nil {
		return &watchdogReviewResult{Clarification: clarification}, nil
	}
	terminal := agent.lastAssistant()
	if terminal == nil {
		return &watchdogReviewResult{StopReason: "stop"}, nil
	}
	reason, message := wdStopReason(terminal)
	if message != "" {
		return &watchdogReviewResult{StopReason: reason, ErrorMessage: message}, nil
	}
	return &watchdogReviewResult{StopReason: reason}, nil
}
