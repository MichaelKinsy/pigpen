package posthorse

// Port of src/context-tools.ts: the new_context and get_context_remaining tools.

import (
	"errors"
	"strconv"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// rolloverStatus is `rolloverStatus` (context-tools.ts:12-17).
func rolloverStatus(b *budget) string {
	if b == nil || !b.enabled {
		return "disabled"
	}
	if b.supported {
		return "enabled"
	}
	return "unsupported"
}

// jsIntegerString prints an integer-valued number as JavaScript does (-0 prints 0).
func jsIntegerString(x float64) string {
	if x == 0 {
		return "0"
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}

// contextReport is `contextReport` (context-tools.ts:18-50).
func contextReport(host policyContext) (toolResult, error) {
	usage, err := host.getContextUsage()
	if err != nil {
		return toolResult{}, err
	}
	if usage == nil || usage.tokens == nil {
		return textResult("Context usage is not known until the next model response.", nil, displayKind("context")), nil
	}
	tokens := *usage.tokens
	n := jsToLocaleInt
	b, err := budgetFor(host, &usage.contextWindow)
	if err != nil {
		return toolResult{}, err
	}
	rollover := rolloverStatus(b)
	var rolloverAt *int
	if b != nil {
		rolloverAt = &b.rolloverAt
	}
	display := displayContext(usage, rollover, rolloverAt)
	percent := 0.0
	if usage.percent != nil {
		percent = *usage.percent
	}
	configured := "≈" + n(max(0, usage.contextWindow-tokens)) + " tokens until the configured context limit (" +
		n(tokens) + "/" + n(usage.contextWindow) + " used, " + jsIntegerString(jsRound(percent)) + "%). Best available native estimate."
	if b == nil || !b.enabled {
		return textResult("Automatic rollover is disabled in the available settings (Pi compaction.enabled=false). "+configured, nil, display), nil
	}
	if !b.supported {
		return textResult(unsupportedMessage(b)+" "+configured, nil, display), nil
	}
	return textResult("≈"+n(max(0, b.rolloverAt-tokens))+" tokens until automatic rollover (line at "+n(b.rolloverAt)+"); "+configured, nil, display), nil
}

// registerContextTools is `registerContextTools` (context-tools.ts:51-103).
func registerContextTools(ext *sdk.Extension, policy snapshotReader, activeToolTokens toolTokensFunc, requestReset func(id, handoff string) error) {
	newContext := sdk.ToolDefinition{
		Name:  "new_context",
		Label: "New Context",
		// context-tools.ts:61-62
		Description:   "Optionally request a deliberate clean context reset once the current foreground tools succeed; retains no prior messages, unlike automatic rollover. Not required after a notes reminder. Background work continues. Earlier conversation stays recoverable through history. Put continuation state in handoff, or save fuller state in notes first.",
		PromptSnippet: "optionally request a clean context reset with an optional handoff",
		Parameters: sdk.Type.Object(sdk.NewOrderedObject(
			"handoff", sdk.Type.Optional(sdk.Type.String(sdk.NewOrderedObject(
				"description", "What the fresh window needs to continue, as readable prose",
				"maxLength", maxHandoffChars,
			))),
		)).Schema(),
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			trimmed := ""
			if handoff, ok := str(params, "handoff"); ok {
				trimmed = jsTrim(handoff)
			}
			tokens, err := activeToolTokens(ctx)
			if err != nil {
				return nil, err
			}
			limit, err := freshPayloadChars(policy(ctx, nil), tokens, nil, maxHandoffChars)
			if err != nil {
				return nil, err
			}
			if size := u16len(trimmed); trimmed != "" && size > limit {
				return nil, errors.New("Handoff is too large for the active model (" + jsToLocaleInt(size) + " characters; limit " + jsToLocaleInt(limit) + "). Save fuller state in notes, then retry with a shorter handoff or no handoff.")
			}
			result := textResult(
				"Requested a fresh Pi context after the current foreground tools succeed. Background work continues across the reset. Earlier conversation stays in session history.",
				nil, displayKind("new-context"))
			handoff := trimmed
			if handoff == "" {
				handoff = emptyHandoff
			}
			if err := requestReset(ctx.ToolCallID(), handoff); err != nil {
				return nil, err
			}
			return result, nil
		},
	}
	applyToolCards(&newContext, "new_context")
	ext.RegisterTool(newContext)

	remaining := sdk.ToolDefinition{
		Name:  "get_context_remaining",
		Label: "Context Remaining",
		// context-tools.ts:92-95
		Description:   "Estimate tokens left before Pi's automatic rollover and the configured context limit. Check before reporting remaining space or changing your work plan because of context limits. A notes-only reminder may arrive before rollover on a working turn; continue afterward and let rollover happen automatically. No routine checks are needed.",
		PromptSnippet: "verify remaining context before reporting a token count or changing plans because of context limits",
		Parameters:    sdk.Type.Object(sdk.NewOrderedObject()).Schema(),
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			return contextReport(policy(ctx, nil))
		},
	}
	applyToolCards(&remaining, "get_context_remaining")
	ext.RegisterTool(remaining)
}
