package ask_user_question

import (
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/ask-user-question/internal/ui"
)

const (
	errNoUIText = "Error: UI not available (running in non-interactive mode)"
	toolLabel   = "Ask User Question"
)

// app holds nothing between calls: a questionnaire lives inside one tool call.
type app struct{}

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
	return a.run(ctxHost{ctx}, args)
}

func (a *app) run(h host, args map[string]any) (any, error) {
	// Line-terminator normalisation runs once, ahead of validation, so the validator, the dialogs, the envelope and
	// the prompt event all see the same clean text (#192).
	typed := normalizeQuestionParams(paramsFromArgs(args))
	if !h.HasUI() {
		return toResult(buildToolResult(errNoUIText, questionnaireResult{Cancelled: true, Error: errNoUI}))
	}
	if v := validateQuestionnaire(typed); !v.OK {
		return toResult(buildToolResult(v.Message, questionnaireResult{Cancelled: true, Error: v.Error}))
	}
	// The original translates only with rpiv-i18n installed; otherwise every string is its English fallback.
	cwd, _ := os.Getwd()
	ui.SetI18nInstalled(rpivI18nInstalled(cwd))
	h.Emit(promptEvent, promptEventPayload(typed))
	// RPC hosts (VSCode pendant, ACP clients like Zed/Paseo, issue #78) cannot render a custom component, but the
	// select and input dialogs work there: the sequential dialog walker. See rpc.go.
	if h.Mode() == "rpc" {
		return a.runDialogs(h, typed)
	}
	return a.runQuestionnaire(h, typed)
}

// runDialogs walks the questions with native select and input dialogs, bracketed by the blocked event pair.
func (a *app) runDialogs(h host, typed questionParams) (any, error) {
	h.Emit(blockedEvent, map[string]any{"active": true})
	defer h.Emit(blockedEvent, map[string]any{"active": false})
	return a.walkDialogs(h, typed)
}

func (a *app) walkDialogs(h host, typed questionParams) (any, error) {
	result, err := runRpcQuestionnaire(h, typed)
	if err != nil {
		return nil, err
	}
	return toResult(buildQuestionnaireResponse(&result, typed))
}

// runQuestionnaire opens the interactive questionnaire in a focused overlay and waits for its result. A questionnaire
// always resolves a result (cancel included), so nil means the host cannot render a component: the dialog walker
// asks instead. upstream: the custom() branch of execute, resolveUndefinedResult.
func (a *app) runQuestionnaire(h host, typed questionParams) (any, error) {
	// Pi loads every highlight.js language in the background when its interactive mode starts, so a preview's fenced
	// code is highlighted in any language by the time a questionnaire opens. Start that load now; a preview that
	// renders before it ends waits for it (once, bounded), never for a host call.
	go ui.LoadHighlightLanguages()
	itemsByTab := make([][]ui.WrappingSelectItem, 0, len(typed.Questions))
	for _, q := range typed.Questions {
		itemsByTab = append(itemsByTab, buildItemsForQuestion(q))
	}
	// Resolve the collapse/expand key spec from config (default ctrl+]; "off" disables the shortcut).
	collapseKey := resolveCollapseKey()

	// Capture the overlay handle so the session can hide the overlay when the user collapses it, and register a raw
	// terminal input listener for the same key so the toggle still works while the overlay is hidden (the host routes
	// no input to a hidden overlay's component).
	session := &sessionRef{}
	handle := &overlayHandleRef{}
	removeListener := registerCollapseKeyListener(h, collapseKey, session, handle)
	// Hiding the overlay is only reversible through the raw listener above, so the session may hide it only when it
	// was registered; otherwise collapse falls back to the visible one-line row.
	canReopenWhileHidden := removeListener != nil

	h.Emit(blockedEvent, map[string]any{"active": true})
	defer func() {
		if removeListener != nil {
			removeListener()
		}
		h.Emit(blockedEvent, map[string]any{"active": false})
	}()
	component := newQuestionnaireComponent(h, typed, itemsByTab, collapseKey, canReopenWhileHidden, session)
	raw, err := h.Custom(component, func(oh ui.OverlayHandle) {
		handle.set(oh)
		if s := session.get(); s != nil {
			s.SetOverlayHandle(oh)
		}
	})
	if err != nil {
		return nil, err
	}
	if raw == nil {
		// The host cannot render a component: the dialog walker asks, and what it cannot carry is refused.
		if err := refuseUnported(typed); err != nil {
			return nil, err
		}
		return a.walkDialogs(h, typed)
	}
	result, ok := decodeResult(raw)
	if !ok {
		return toResult(buildQuestionnaireResponse(nil, typed))
	}
	return toResult(buildQuestionnaireResponse(result, typed))
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
