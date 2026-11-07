package posthorse

// Port of src/budget.ts: the policy snapshot, rollover budget and guidance text.

import (
	"errors"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// minUsableTokens is MIN_USABLE_TOKENS (budget.ts:14): Math.ceil(MAX_HANDOFF_CHARS / 4) * 2.
const minUsableTokens = (maxHandoffChars + 3) / 4 * 2

// checkpointGuidance is CHECKPOINT_GUIDANCE (budget.ts:15-21), character for character.
const checkpointGuidance = `Keep working on the assigned task; Pi handles context rollover automatically.

The current context window is not a deadline for finishing your task. Do not reduce scope, skip prescribed steps or verification, or issue a final progress summary because rollover is approaching.

Update the task's current-state note if needed: goal, progress, decisions, and next steps. Use available file-editing tools at its absolute path; use notes write only for creation, substantial restructuring, or when editing tools are unavailable. Leave an already-current note unchanged. Then immediately execute the next prescribed task step in this run. No new_context call, waiting for rollover, or user "continue" is needed.

Finish when the requested work and required verification are complete, or report a genuine blocker. Approaching context rollover is not a blocker.`

// budget is the TS Budget (budget.ts:22-29).
type budget struct {
	contextWindow int
	reserveTokens int
	enabled       bool
	usable        int
	rolloverAt    int
	supported     bool
}

// policySnap is the object policySnapshot returns (budget.ts:31-52). It owns one synchronous
// evaluation: the settings and the context usage are read from the host at most once. The
// handler's context rides along for the host reads of later stages (hostOf).
type policySnap struct {
	ctx        sdk.Context
	getPolicy  policyReader
	livePolicy *compactionPolicy
	settings   *compactionPolicy
	usageRead  bool
	usage      *contextUsage
}

// policySnapshot is `policySnapshot` (budget.ts:31-52).
func policySnapshot(ctx sdk.Context, getPolicy policyReader, livePolicy *compactionPolicy) policyContext {
	return &policySnap{ctx: ctx, getPolicy: getPolicy, livePolicy: livePolicy}
}

// getCompactionSettings is `settings ??= { ...(livePolicy ?? getPolicy(ctx)) }` (budget.ts:39-42);
// a failing reader is not cached.
func (p *policySnap) getCompactionSettings() (compactionPolicy, error) {
	if p.settings == nil {
		var settings compactionPolicy
		if p.livePolicy != nil {
			settings = *p.livePolicy
		} else {
			read, err := p.getPolicy(p.ctx)
			if err != nil {
				return compactionPolicy{}, err
			}
			settings = read
		}
		if settings.keepRecentTokens != nil {
			keep := *settings.keepRecentTokens
			settings.keepRecentTokens = &keep
		}
		p.settings = &settings
	}
	return *p.settings, nil
}

// getContextUsage reads `ctx.getContextUsage()` once (budget.ts:43-49).
func (p *policySnap) getContextUsage() (*contextUsage, error) {
	if !p.usageRead {
		usage, err := p.ctx.GetContextUsage()
		if err != nil {
			return nil, err
		}
		if usage != nil {
			p.usage = &contextUsage{tokens: usage.Tokens, contextWindow: usage.ContextWindow, percent: usage.Percent}
		}
		p.usageRead = true
	}
	return p.usage, nil
}

// getSystemPrompt is `ctx.getSystemPrompt()` (budget.ts:50).
func (p *policySnap) getSystemPrompt() (string, error) { return p.ctx.GetSystemPrompt() }

// hostOf returns the handler context behind a policy snapshot; the host reads of toolTokens
// (index.ts:48) need it.
func hostOf(ctx policyContext) (sdk.Context, error) {
	snap, ok := ctx.(*policySnap)
	if !ok {
		return sdk.Context{}, errors.New("posthorse: the policy context has no host context")
	}
	return snap.ctx, nil
}

// budgetFor is `budgetFor` (budget.ts:53-70). A nil contextWindow is undefined: the usage
// supplies it. The result is nil when no positive window is known.
func budgetFor(ctx policyContext, contextWindow *int) (*budget, error) {
	if contextWindow == nil {
		usage, err := ctx.getContextUsage()
		if err != nil {
			return nil, err
		}
		if usage != nil {
			contextWindow = &usage.contextWindow
		}
	}
	if contextWindow == nil || *contextWindow <= 0 {
		return nil, nil
	}
	settings, err := ctx.getCompactionSettings()
	if err != nil {
		return nil, err
	}
	usable := *contextWindow - settings.reserveTokens
	return &budget{
		contextWindow: *contextWindow,
		reserveTokens: settings.reserveTokens,
		enabled:       settings.enabled,
		usable:        usable,
		rolloverAt:    usable + 1,
		supported:     !settings.enabled || usable >= minUsableTokens,
	}, nil
}

// unsupportedMessage is `unsupportedMessage` (budget.ts:71-74).
func unsupportedMessage(b *budget) string {
	return "Posthorse: unsupported configuration. The model's context window (" + jsToLocaleInt(b.contextWindow) +
		" tokens) minus Pi's compaction.reserveTokens (" + jsToLocaleInt(b.reserveTokens) + ") leaves " +
		jsToLocaleInt(b.usable) + " usable tokens; Posthorse needs at least " + jsToLocaleInt(minUsableTokens) +
		". Automatic Posthorse rollover and checkpoint reminders are off for this model, so Pi's own compaction applies. Lower compaction.reserveTokens in Pi settings or use a larger-context model. new_context remains available with a model-aware handoff limit."
}

// automaticGuidance is `automaticGuidance` (budget.ts:75-90).
func automaticGuidance(b *budget, enabled bool) string {
	if !enabled {
		return "Pi compaction is disabled in the available settings, so Posthorse sends no checkpoint reminder. new_context remains available."
	}
	if b != nil && !b.supported {
		return unsupportedMessage(b)
	}
	line := ""
	if b != nil {
		line = " The rollover line is " + jsToLocaleInt(b.rolloverAt) + " tokens used under the available Pi settings."
	}
	return checkpointGuidance + "\nAutomatic rollover follows Pi's compaction setting and retains a safe recent-message tail using compaction.keepRecentTokens, without model summarization." + line + " At most one routine task-note reminder may arrive at the end of a working turn, never on a turn that ends the run. new_context is an optional deliberate clean reset, not a checkpoint requirement."
}

// buildGuidance is `buildGuidance` (budget.ts:91-104).
func buildGuidance(ctx policyContext) (string, error) {
	b, err := budgetFor(ctx, nil)
	if err != nil {
		return "", err
	}
	var enabled bool
	if b != nil {
		enabled = b.enabled
	} else {
		settings, err := ctx.getCompactionSettings()
		if err != nil {
			return "", err
		}
		enabled = settings.enabled
	}
	capacity := "Configured context capacity is unknown."
	if b != nil {
		capacity = "Configured context capacity: " + jsToLocaleInt(b.contextWindow) + " tokens. This is Pi's best available native limit, not remaining space or a measured provider boundary. Fresh windows use the active configuration; system instructions, tools, the handoff, retained recent messages, and new messages consume part of it."
	}
	return "## Automatic context management (Posthorse)\n" + automaticGuidance(b, enabled) + "\n" + capacity + "\n" +
		"Do not guess context capacity or remaining space from response-token limits or reasoning budgets. Before reporting a remaining-token count or changing your work plan because of context limits, call get_context_remaining. If usage is unknown, report it as unknown. No routine budget checks are needed.\n" +
		"After a rollover, earlier conversation stays in history. Restore notes and todos, then verify live state before stateful or external work; automatic handoffs record inputs, not progress.\n" +
		"Keep one concise current-state note per task. Preserve decisions and safety constraints; link fuller evidence and history instead of copying them. Edit changed sections rather than resending unchanged content.\n" +
		"Write handoffs and notes as normal readable prose; notes and history search match literal text.", nil
}

// guidanceTokens is `guidanceTokens` (budget.ts:105-112): Pi's estimateTokens of the request-local
// system message.
func guidanceTokens(ctx policyContext) (int, error) {
	guidance, err := buildGuidance(ctx)
	if err != nil {
		return 0, err
	}
	return sdk.EstimateTokens(rec{
		"role":      "system",
		"content":   "<posthorse>\n" + guidance + "\n</posthorse>",
		"timestamp": float64(0),
	}), nil
}

// ceilQuarter is Math.ceil(n / 4) for n >= 0.
func ceilQuarter(n int) int { return (n + 3) / 4 }

// freshPayloadChars is `freshPayloadChars` (budget.ts:113-136); pass maxHandoffChars as cap for
// the TS default.
func freshPayloadChars(ctx policyContext, toolTokens int, pendingMessages []rec, cap int) (int, error) {
	b, err := budgetFor(ctx, nil)
	if err != nil {
		return 0, err
	}
	if b == nil {
		return cap, nil
	}
	line := b.contextWindow
	if b.enabled && b.supported {
		line = b.rolloverAt
	}
	system, err := ctx.getSystemPrompt()
	if err != nil {
		return 0, err
	}
	guidance, err := guidanceTokens(ctx)
	if err != nil {
		return 0, err
	}
	promptTokens := ceilQuarter(u16len(system)) + guidance
	pendingTokens := 0
	for _, message := range pendingMessages {
		pendingTokens += ceilQuarter(u16len(textOf(message))) + len(imagesOf(message["content"]))*(estimatedImageChars/4)
	}
	return min(cap, max(0, (line-pageMarginTokens-promptTokens-toolTokens-pendingTokens)/2)*4), nil
}

// pageCapacity is `pageCapacity` (budget.ts:137-150): the tokens left for one page.
func pageCapacity(ctx policyContext, toolTokens toolTokensFunc) (int, error) {
	usage, err := ctx.getContextUsage()
	if err != nil {
		return 0, err
	}
	if usage == nil || usage.tokens == nil {
		host, err := hostOf(ctx)
		if err != nil {
			return 0, err
		}
		tokens, err := toolTokens(host)
		if err != nil {
			return 0, err
		}
		chars, err := freshPayloadChars(ctx, tokens, nil, maxPageChars)
		if err != nil {
			return 0, err
		}
		return chars/4 + pageMarginTokens, nil
	}
	b, err := budgetFor(ctx, &usage.contextWindow)
	if err != nil {
		return 0, err
	}
	line := usage.contextWindow
	if b != nil && b.enabled && b.supported {
		line = b.rolloverAt
	}
	guidance, err := guidanceTokens(ctx)
	if err != nil {
		return 0, err
	}
	return line - *usage.tokens - guidance, nil
}
