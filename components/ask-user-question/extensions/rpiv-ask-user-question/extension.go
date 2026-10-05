package ask_user_question

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	errNoUIText = "Error: UI not available (running in non-interactive mode)"
	toolLabel   = "Ask User Question"
)

// app holds nothing between calls: a questionnaire lives inside one tool call.
type app struct{}

// ctxDialogs adapts the SDK context to the dialog walker.
type ctxDialogs struct{ ctx sdk.Context }

func (d ctxDialogs) Select(title string, options []string) (string, bool, error) {
	return d.ctx.Select(title, options)
}

func (d ctxDialogs) Input(title, placeholder string) (string, bool, error) {
	return d.ctx.Input(title, placeholder)
}

// promptEventPayload is what listeners (notification plugins) receive when a questionnaire starts.
// upstream: ask-user-question.ts emitAskUserPromptEvent.
func promptEventPayload(p questionParams) map[string]any {
	qs := make([]any, 0, len(p.Questions))
	for _, q := range p.Questions {
		opts := make([]any, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, map[string]any{"label": o.Label, "description": o.Description, "hasPreview": o.Preview != ""})
		}
		qs = append(qs, map[string]any{"question": q.Question, "header": q.Header, "multiSelect": q.MultiSelect, "options": opts})
	}
	return map[string]any{"questions": qs}
}

// emit sends an event to listeners on the shared bus. Delivery is best effort, as it is for the original: a
// listener's failure is not the questionnaire's.
func emit(ctx sdk.Context, channel string, payload any) {
	_ = ctx.Events().Emit(channel, payload)
}

func toResult(o toolOutput) (sdk.ToolResult, error) {
	return sdk.ToolResult{Content: o.Text, Details: o.Details}, nil
}

// execute runs one questionnaire. upstream: ask-user-question.ts registerAskUserQuestionTool.
func (a *app) execute(ctx sdk.Context, args map[string]any) (any, error) {
	// Line-terminator normalisation runs once, ahead of validation, so the validator, the dialogs, the envelope and
	// the prompt event all see the same clean text (#192).
	typed := normalizeQuestionParams(paramsFromArgs(args))
	if !ctx.HasUI() {
		return toResult(buildToolResult(errNoUIText, questionnaireResult{Cancelled: true, Error: errNoUI}))
	}
	if v := validateQuestionnaire(typed); !v.OK {
		return toResult(buildToolResult(v.Message, questionnaireResult{Cancelled: true, Error: v.Error}))
	}
	emit(ctx, promptEvent, promptEventPayload(typed))
	return a.runDialogs(ctx, typed)
}

// runDialogs walks the questions with native select and input dialogs, bracketed by the blocked event pair.
func (a *app) runDialogs(ctx sdk.Context, typed questionParams) (any, error) {
	emit(ctx, blockedEvent, map[string]any{"active": true})
	defer emit(ctx, blockedEvent, map[string]any{"active": false})
	result, err := runRpcQuestionnaire(ctxDialogs{ctx}, typed)
	if err != nil {
		return nil, err
	}
	return toResult(buildQuestionnaireResponse(&result, typed))
}

// reconcile keeps the tool in the active set exactly when the host has a UI: a host without one has nobody to ask.
// RPC hosts have a UI (the dialog walker). upstream: reconcile.ts.
func reconcile(ctx sdk.Context, _ map[string]any) (any, error) {
	active, err := ctx.GetActiveTools()
	if err != nil {
		return nil, err
	}
	has := hasString(active, toolName)
	switch {
	case !ctx.HasUI() && has:
		kept := make([]string, 0, len(active))
		for _, n := range active {
			if n != toolName {
				kept = append(kept, n)
			}
		}
		ctx.SetActiveTools(kept)
	case ctx.HasUI() && !has:
		ctx.SetActiveTools(append(append([]string{}, active...), toolName))
	}
	return nil, nil
}

// Extension is the questionnaire extension.
func Extension() *sdk.Extension {
	e := sdk.New("rpiv-ask-user-question")
	a := &app{}
	g := validateGuidanceFields(loadConfig().Guidance)
	def := sdk.ToolDefinition{
		Name:             toolName,
		Label:            toolLabel,
		Description:      defaultToolDescription,
		PromptSnippet:    defaultPromptSnippet,
		PromptGuidelines: defaultPromptGuidelines,
		Parameters:       toolParameters(),
		Execute:          a.execute,
	}
	if g.Description != "" {
		def.Description = g.Description
	}
	if g.PromptSnippet != "" {
		def.PromptSnippet = g.PromptSnippet
	}
	if g.PromptGuidelines != nil {
		def.PromptGuidelines = g.PromptGuidelines
	}
	e.RegisterTool(def)
	e.OnEvent(sdk.EventBeforeAgentStart, reconcile)
	return e
}
