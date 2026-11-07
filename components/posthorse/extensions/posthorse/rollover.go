package posthorse

// Port of src/rollover.ts: the per-turn reset requests, the stale-reminder context filter, the
// explicit new_context reset and the threshold fallback at turn_end, and Pi's automatic
// compaction hook. Pi owns persistence and scheduling; Posthorse owns the drafts.

import (
	"errors"
	"fmt"
	"os"
	"slices"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// rolloverResources is RolloverResources (rollover.ts:29-32).
type rolloverResources struct {
	resetPages func()
	notes      func(ctx sdk.Context) (*notesRecovery, error)
}

// reminderDetails is the fingerprint a reminder message carries, in the member order of
// ReminderFingerprint (reminders.ts, contracts.ts:86-90): windowId, contextWindow, reserveTokens.
// JSON.stringify drops an undefined member, so a nil number is omitted.
type reminderDetails struct {
	WindowID      string `json:"windowId"`
	ContextWindow *int   `json:"contextWindow,omitempty"`
	ReserveTokens *int   `json:"reserveTokens,omitempty"`
}

// runAborted is `ctx.signal?.aborted === true` (rollover.ts:41): the run's signal, nil outside a run.
func runAborted(ctx sdk.Context) bool {
	signal := ctx.Signal()
	return signal != nil && signal.Err() != nil
}

// recsOf lists the records of a decoded JSON array, keeping positions (a non-object stays a nil record).
func recsOf(v any) []rec {
	items, _ := arrOf(v)
	out := make([]rec, len(items))
	for i, item := range items {
		out[i], _ = recOf(item)
	}
	return out
}

// projectedOf converts decoded `{sourceEntry, messages}` items (a turn_end preview's contextEntries).
func projectedOf(v any) []projectedEntry {
	items, _ := arrOf(v)
	out := make([]projectedEntry, len(items))
	for i, item := range items {
		entry, _ := recOf(item)
		out[i].sourceEntry, _ = recOf(entry["sourceEntry"])
		out[i].messages = recsOf(entry["messages"])
	}
	return out
}

// projectedFromSDK converts ReadonlySessionManager.BuildSessionProjection entries.
func projectedFromSDK(entries []sdk.ProjectedSessionEntry) []projectedEntry {
	out := make([]projectedEntry, len(entries))
	for i, entry := range entries {
		out[i] = projectedEntry{sourceEntry: entry.SourceEntry, messages: entry.Messages}
	}
	return out
}

// explicitReset is rollover.ts:39-74: the turn_end BoundaryResult of a new_context request, or nil
// when Pi will not commit the boundary.
func explicitReset(event rec, ctx sdk.Context, summary string, limit func() (int, error)) (rec, error) {
	if outcome, _ := str(event, "outcome"); outcome == "aborted" || runAborted(ctx) {
		return nil, nil
	}
	max, err := limit()
	if err != nil {
		return nil, err
	}
	entries, _ := arrOf(event["entries"])
	if u16len(summary) > max {
		return rec{"entries": append(slices.Clone(entries), rec{
			"type":       "custom_message",
			"customType": "posthorse-reset-deferred",
			"display":    true,
			"content":    "Posthorse reset deferred: queued messages leave too little room for the requested handoff. The current context is unchanged; process those messages, then request a shorter handoff.",
		})}, nil
	}
	return rec{
		"entries": append(slices.Clone(entries), rec{
			"type":             "compaction",
			"summary":          summary,
			"firstKeptEntryId": nil,
			"details":          rec{"posthorse": 1, "reason": "explicit"},
		}),
		"continue": true,
	}, nil
}

// scanReminderMessages is the opening of staleReminderPredicate (rollover.ts:84-91): whether any
// reminder message is present, and the first window marker.
func scanReminderMessages(messages []any) (reminderPresent bool, marker rec) {
	for _, item := range messages {
		message, _ := recOf(item)
		if role, _ := str(message, "role"); role != "custom" {
			continue
		}
		if isReminderType(message["customType"]) {
			reminderPresent = true
		} else if marker == nil && message["customType"] == "context-window" {
			marker = message
		}
	}
	return reminderPresent, marker
}

// staleReminderPredicate is rollover.ts:76-115; a nil predicate is NO_STALE_REMINDER (rollover.ts:75).
func staleReminderPredicate(messages []any, ctx sdk.Context, policy snapshotReader, reminders *reminderLookup) (staleReminder, error) {
	hasReminderMessage, marker := scanReminderMessages(messages)
	if !hasReminderMessage {
		return nil, nil
	}
	var windowID any
	if marker != nil {
		details, _ := recOf(marker["details"])
		windowID = details["windowId"]
	} else {
		state, err := reminders.read(ctx.SessionManager())
		if err != nil {
			return nil, err
		}
		windowID = state.windowId
	}
	window, ok := windowID.(string)
	if !ok {
		return nil, nil
	}
	host := policy(ctx, nil)
	b, err := budgetFor(host, nil)
	if err != nil {
		return nil, err
	}
	fingerprint := reminderFingerprint{windowId: window}
	if b != nil {
		fingerprint.contextWindow, fingerprint.reserveTokens = &b.contextWindow, &b.reserveTokens
	}
	settings, err := host.getCompactionSettings()
	if err != nil {
		return nil, err
	}
	enabled := settings.enabled
	return func(message rec) bool {
		role, _ := str(message, "role")
		return role == "custom" && isReminderType(message["customType"]) &&
			(!enabled || !reminderMatches(message["details"], fingerprint))
	}, nil
}

// retainedTailSentinel is the hidden message that keeps a retained assistant tail from opening
// the context (rollover.ts:132-139).
const retainedTailContent = "[Retained pre-rollover tail]"

// chronologicalRetainedTail is rollover.ts:116-149: the retained pre-rollover messages move ahead
// of the recovery record, so the model reads them in order. changed is false when messages is
// returned as is (the TS reference-identity test).
func chronologicalRetainedTail(messages []any, ctx sdk.Context, stale staleReminder) ([]any, bool, error) {
	if len(messages) < 2 {
		return messages, false, nil
	}
	first, _ := recOf(messages[0])
	if role, _ := str(first, "role"); role != "compactionSummary" {
		return messages, false, nil
	}
	if summary, _ := str(first, "summary"); !hasRetainedRolloverMarker(summary) {
		return messages, false, nil
	}
	count, err := countRetainedMessages(ctx, stale)
	if err != nil {
		return nil, false, err
	}
	if count <= 0 || 1+count > len(messages) {
		return messages, false, nil
	}
	retained, after := messages[1:1+count], messages[1+count:]
	ordered := make([]any, 0, len(messages)+1)
	head, _ := recOf(retained[0])
	if role, _ := str(head, "role"); role == "assistant" {
		sentinel := rec{
			"role":       "custom",
			"customType": "posthorse-retained-tail",
			"content":    retainedTailContent,
			"display":    false,
		}
		if timestamp, ok := first["timestamp"]; ok {
			sentinel["timestamp"] = timestamp
		}
		ordered = append(ordered, sentinel)
	}
	ordered = append(ordered, retained...)
	ordered = append(ordered, first)
	ordered = append(ordered, after...)
	return ordered, true, nil
}

// dropStale is `messages.some(stale) ? messages.filter(m => !stale(m)) : messages` (rollover.ts:155-157);
// changed is false when messages is returned as is.
func dropStale(messages []any, stale staleReminder) (filtered []any, changed bool) {
	if stale == nil || !slices.ContainsFunc(messages, func(item any) bool { message, _ := recOf(item); return stale(message) }) {
		return messages, false
	}
	filtered = make([]any, 0, len(messages))
	for _, item := range messages {
		if message, _ := recOf(item); !stale(message) {
			filtered = append(filtered, item)
		}
	}
	return filtered, true
}

// registerReminderFilter is rollover.ts:150-166.
func registerReminderFilter(ext *sdk.Extension, policy snapshotReader, reminders *reminderLookup) {
	ext.OnEvent(sdk.EventContext, func(ctx sdk.Context, event map[string]any) (any, error) {
		messages, _ := arrOf(event["messages"])
		stale, err := staleReminderPredicate(messages, ctx, policy, reminders)
		if err != nil {
			return nil, err
		}
		filtered, changed := dropStale(messages, stale)
		ordered, reordered, err := chronologicalRetainedTail(filtered, ctx, stale)
		if err != nil {
			return nil, err
		}
		if changed || reordered {
			return rec{"messages": ordered}, nil
		}
		return nil, nil
	})
}

// automaticDraftInput is the recovery argument of automaticDraft (rollover.ts:170-175).
type automaticDraftInput struct {
	handoff          string
	tokensBefore     any // a number, nil (undefined) when the event had none
	hasTokensBefore  bool
	reason           string
	firstKeptEntryID string
	hasFirstKept     bool
}

// automaticDraft is rollover.ts:167-194: the session_before_compact result.
func automaticDraft(ctx sdk.Context, recovery automaticDraftInput) (rec, error) {
	firstKept, hasFirstKept := recovery.firstKeptEntryID, recovery.hasFirstKept
	if !hasFirstKept {
		// Public compaction requires an entry id even when no prior conversation safely fits.
		if err := ctx.AppendEntry("posthorse-boundary", rec{}); err != nil {
			return nil, err
		}
		leaf, err := ctx.SessionManager().GetLeafID()
		if err != nil {
			return nil, err
		}
		if leaf != nil {
			firstKept, hasFirstKept = *leaf, true
		}
	}
	if !hasFirstKept || firstKept == "" {
		return nil, errors.New("Pi did not persist the context boundary.")
	}
	compaction := rec{
		"summary":          recovery.handoff,
		"firstKeptEntryId": firstKept,
		"details":          rec{"posthorse": 1, "reason": recovery.reason},
	}
	if recovery.hasTokensBefore {
		compaction["tokensBefore"] = recovery.tokensBefore
	}
	return rec{"compaction": compaction}, nil
}

// reportAutomaticFailure is rollover.ts:195-203. Pi's `console.error` of a headless run is the
// extension process's stderr here: PiG drains it into a bounded log and has no other diagnostic
// channel without a UI (see lane notes).
func reportAutomaticFailure(ctx sdk.Context, err error) {
	message := "Posthorse automatic rollover cancelled: " + err.Error()
	if ctx.HasUI() {
		ctx.Notify(message, "error")
	} else {
		fmt.Fprintln(os.Stderr, message)
	}
}

// freshWindowWithoutInput is rollover.ts:204-225.
func freshWindowWithoutInput(projected []projectedEntry) bool {
	boundary := slices.IndexFunc(projected, func(entry projectedEntry) bool {
		return entryType(entry.sourceEntry) == "compaction" && len(entry.messages) > 0
	})
	if boundary < 0 {
		return false
	}
	return !slices.ContainsFunc(projected[boundary+1:], func(entry projectedEntry) bool {
		if entryType(entry.sourceEntry) == "compaction" {
			return false
		}
		return slices.ContainsFunc(entry.messages, func(message rec) bool {
			switch role, _ := str(message, "role"); role {
			case "user", "custom", "bashExecution", "branchSummary", "compactionSummary":
				return true
			}
			return false
		})
	})
}

// reachedThreshold is rollover.ts:226-236.
func reachedThreshold(host policyContext) (bool, error) {
	usage, err := host.getContextUsage()
	if err != nil {
		return false, err
	}
	b, err := budgetFor(host, nil)
	if err != nil {
		return false, err
	}
	return b != nil && b.supported && b.enabled && usage != nil && usage.tokens != nil && *usage.tokens >= b.rolloverAt, nil
}

// fallbackTurnEligible is rollover.ts:237-244.
func fallbackTurnEligible(event rec, ctx sdk.Context) bool {
	if outcome, _ := str(event, "outcome"); outcome != "completed" {
		return false
	}
	message, _ := recOf(event["message"])
	if role, _ := str(message, "role"); role == "assistant" {
		if stop, _ := str(message, "stopReason"); stop == "length" {
			return false
		}
	}
	if runAborted(ctx) {
		return false
	}
	entries, _ := arrOf(event["entries"])
	return !slices.ContainsFunc(entries, func(item any) bool {
		kind := entryType(asRec(item))
		return kind == "compaction" || kind == "context_edit"
	})
}

// asRec is recOf without the flag.
func asRec(v any) rec { m, _ := recOf(v); return m }

// askQuestionRegistered is `pi.getAllTools().some(tool => tool.name === "ask_question" && tool.namespace === undefined)`.
func askQuestionRegistered(ctx sdk.Context) (bool, error) {
	tools, err := ctx.GetAllTools()
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(tools, func(tool sdk.ToolInfo) bool { return tool.Name == "ask_question" && tool.Namespace == nil }), nil
}

// thresholdFallback is rollover.ts:248-303: the turn_end draft of a rollover that Pi's own
// threshold check has not started, or nil to delegate.
func thresholdFallback(event rec, ctx sdk.Context, host policyContext, toolTokens toolTokensFunc, notes func(sdk.Context) (*notesRecovery, error)) (rec, error) {
	if !fallbackTurnEligible(event, ctx) {
		return nil, nil
	}
	reached, err := reachedThreshold(host)
	if err != nil || !reached {
		return nil, err
	}
	eventContext, _ := recOf(event["context"])
	projected := projectedOf(eventContext["contextEntries"])
	if !freshWindowWithoutInput(projected) {
		return nil, nil
	}
	owner, err := askQuestionRegistered(ctx)
	if err != nil {
		return nil, err
	}
	branch, err := ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return nil, err
	}
	tokens, err := toolTokens(ctx)
	if err != nil {
		return nil, err
	}
	recovered, err := notes(ctx)
	if err != nil {
		return nil, err
	}
	recovery, err := automaticRecovery(branch, projected, recoveryCapacity{
		host:            host,
		toolTokens:      tokens,
		pendingMessages: recsOf(eventContext["pendingMessages"]),
		notes:           recovered,
	}, owner)
	if err != nil || recovery == nil {
		return nil, err
	}
	// ponytail (rollover.ts:290): turn_end drafts are eager. Later projection editors must
	// replace/remove this draft; upgrade to a host post-boundary hook if recovery must include
	// later handler edits.
	var firstKept any
	if recovery.hasFirstKept {
		firstKept = recovery.firstKeptEntryID
	}
	entries, _ := arrOf(event["entries"])
	result := rec{"entries": append(slices.Clone(entries), rec{
		"type":             "compaction",
		"summary":          recovery.handoff,
		"firstKeptEntryId": firstKept,
		"details":          rec{"posthorse": 1, "reason": "threshold"},
	})}
	if cont, ok := event["continue"]; ok {
		result["continue"] = cont
	}
	return result, nil
}

// compactionCancelled is rollover.ts:307-309: event.signal is the request's cancellation.
func compactionCancelled(ctx sdk.Context) bool {
	return ctx.Err() != nil || runAborted(ctx)
}

// policyFromSettings reads event.preparation.settings (a CompactionSettings record) as a live
// policy; nil is undefined.
func policyFromSettings(v any) *compactionPolicy {
	settings, ok := recOf(v)
	if !ok {
		return nil
	}
	policy := compactionPolicy{}
	policy.enabled, _ = boolean(settings, "enabled")
	if reserve, ok := num(settings, "reserveTokens"); ok {
		policy.reserveTokens = jsToInt(reserve)
	}
	if keep, ok := num(settings, "keepRecentTokens"); ok {
		n := jsToInt(keep)
		policy.keepRecentTokens = &n
	}
	return &policy
}

// prepareAutomatic is rollover.ts:310-358. A nil draft with cancel false is "delegate".
func prepareAutomatic(event rec, ctx sdk.Context, policy snapshotReader, toolTokens toolTokensFunc, notes func(sdk.Context) (*notesRecovery, error)) (draft *automaticDraftInput, cancel bool, err error) {
	reason, _ := str(event, "reason")
	if reason == "manual" {
		return nil, false, nil
	}
	if compactionCancelled(ctx) {
		return nil, true, nil
	}
	preparation, ok := recOf(event["preparation"])
	if !ok {
		return nil, false, errors.New("Cannot read properties of undefined (reading 'settings')")
	}
	host := policy(ctx, policyFromSettings(preparation["settings"]))
	b, err := budgetFor(host, nil)
	if err != nil {
		return nil, false, err
	}
	if b == nil || !b.supported {
		return nil, false, nil
	}
	owner, err := askQuestionRegistered(ctx)
	if err != nil {
		return nil, false, err
	}
	projection, err := ctx.SessionManager().BuildSessionProjection()
	if err != nil {
		return nil, false, err
	}
	tokens, err := toolTokens(ctx)
	if err != nil {
		return nil, false, err
	}
	recovered, err := notes(ctx)
	if err != nil {
		return nil, false, err
	}
	firstKept, hasFirstKept := str(preparation, "firstKeptEntryId")
	recovery, err := automaticRecovery(recsOf(event["branchEntries"]), projectedFromSDK(projection.Entries), recoveryCapacity{
		host:             host,
		toolTokens:       tokens,
		firstKeptEntryID: firstKept,
		hasFirstKept:     hasFirstKept,
		notes:            recovered,
	}, owner)
	if err != nil {
		return nil, false, err
	}
	if recovery == nil {
		return nil, false, errors.New("Too little fresh context capacity for an automatic recovery record.")
	}
	if compactionCancelled(ctx) {
		return nil, true, nil
	}
	tokensBefore, hasTokensBefore := preparation["tokensBefore"]
	return &automaticDraftInput{
		handoff:          recovery.handoff,
		tokensBefore:     tokensBefore,
		hasTokensBefore:  hasTokensBefore,
		reason:           reason,
		firstKeptEntryID: recovery.firstKeptEntryID,
		hasFirstKept:     recovery.hasFirstKept,
	}, false, nil
}

// automaticCompactionHook is the session_before_compact handler of registerAutomaticRollover
// (rollover.ts:362-391), a named function so that a test can run it on an already-cancelled request.
func automaticCompactionHook(policy snapshotReader, toolTokens toolTokensFunc, notes func(sdk.Context) (*notesRecovery, error)) sdk.EventFunc {
	return func(ctx sdk.Context, event map[string]any) (any, error) {
		draft, cancel, err := prepareAutomatic(event, ctx, policy, toolTokens, notes)
		if err == nil && draft != nil {
			var result rec
			if result, err = automaticDraft(ctx, *draft); err == nil {
				return result, nil
			}
		}
		if err != nil {
			// Hook errors are swallowed by Pi, which would run its summarizer; cancel instead.
			if !compactionCancelled(ctx) {
				reportAutomaticFailure(ctx, err)
			}
			return rec{"cancel": true}, nil
		}
		if cancel {
			return rec{"cancel": true}, nil
		}
		return nil, nil
	}
}

// registerAutomaticRollover is rollover.ts:359-392.
func registerAutomaticRollover(ext *sdk.Extension, policy snapshotReader, toolTokens toolTokensFunc, notes func(sdk.Context) (*notesRecovery, error)) {
	ext.OnEvent(sdk.EventSessionBeforeCompact, automaticCompactionHook(policy, toolTokens, notes))
}

// registerRollover is rollover.ts:393-456. It owns per-turn reset requests and leaf-certified
// reminder state; Pi owns persistence and scheduling. The handlers register in the TS order
// (the reset-request tool hooks first), which is the order Pi runs them in.
func registerRollover(ext *sdk.Extension, policy snapshotReader, toolTokens toolTokensFunc, resources rolloverResources) {
	resetRequests := newResetRequests(ext)
	reminders := createReminderLookup()
	resetTurn := func() {
		resetRequests.clear()
		resources.resetPages()
	}
	ext.OnEvent(sdk.EventTurnStart, func(sdk.Context, map[string]any) (any, error) {
		resetTurn()
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionStart, func(sdk.Context, map[string]any) (any, error) {
		resetTurn()
		reminders.reset()
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionTree, func(sdk.Context, map[string]any) (any, error) {
		reminders.reset()
		return nil, nil
	})
	registerGuidance(ext, policy)
	registerReminderFilter(ext, policy, reminders)
	registerAutomaticRollover(ext, policy, toolTokens, resources.notes)
	ext.OnEvent(sdk.EventTurnEnd, func(ctx sdk.Context, event map[string]any) (any, error) {
		results, _ := arrOf(event["toolResults"])
		summary, hasSummary := resetRequests.take(results)
		host := policy(ctx, nil)
		message, _ := recOf(event["message"])
		if role, _ := str(message, "role"); role == "assistant" {
			if stop, _ := str(message, "stopReason"); stop == "error" || stop == "aborted" {
				return nil, nil
			}
		}
		if hasSummary {
			result, err := explicitReset(event, ctx, summary, func() (int, error) {
				tokens, err := toolTokens(ctx)
				if err != nil {
					return 0, err
				}
				return freshPayloadChars(host, tokens, recsOf(asRec(event["context"])["pendingMessages"]), maxHandoffChars)
			})
			if result == nil {
				return nil, err
			}
			return result, err
		}
		fallback, err := thresholdFallback(event, ctx, host, toolTokens, resources.notes)
		if err != nil {
			return nil, err
		}
		if fallback != nil {
			return fallback, nil
		}
		decision, err := reminderDecision(event, ctx, host, reminders)
		if err != nil {
			return nil, err
		}
		if decision.kind == "send" {
			err = ctx.SendCustomMessage(sdk.CustomMessage{
				CustomType: reminderType,
				Content:    "[posthorse] Routine task-note update.\n" + checkpointGuidance,
				Display:    true,
				Details: reminderDetails{
					WindowID:      decision.fingerprint.windowId,
					ContextWindow: decision.fingerprint.contextWindow,
					ReserveTokens: decision.fingerprint.reserveTokens,
				},
			}, sdk.SendMessageOptions{DeliverAs: "steer"})
			if err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	registerContextTools(ext, policy, toolTokens, func(id, handoff string) error {
		return resetRequests.request(id, handoff)
	})
}
