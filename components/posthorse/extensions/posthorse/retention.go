package posthorse

// Port of src/retention.ts: the recent-message tail an automatic rollover retains, and the
// recovery record that shares half of the fresh operational budget with it.

import (
	"math"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// recoveryCapacity is RecoveryCapacity (retention.ts:20-26). hasFirstKept tells a prepared
// firstKeptEntryId from undefined.
type recoveryCapacity struct {
	host             policyContext
	toolTokens       int
	pendingMessages  []rec
	firstKeptEntryID string
	hasFirstKept     bool
	notes            *notesRecovery
}

// retainedTail is RetainedTail (retention.ts:27-30).
type retainedTail struct {
	firstKeptEntryID string
	hasFirstKept     bool
	ids              map[string]struct{}
}

// autoRecovery is the result of automaticRecovery (retention.ts:189).
type autoRecovery struct {
	handoff          string
	firstKeptEntryID string
	hasFirstKept     bool
}

// cutRoles is CUT_ROLES (retention.ts:39-46).
func isCutRole(role string) bool {
	switch role {
	case "user", "assistant", "bashExecution", "custom", "branchSummary", "compactionSummary":
		return true
	}
	return false
}

// emptyUserMessage is nativeMessage's answer for a system message (retention.ts:84-86): compaction
// checkpoints replace earlier system declarations. It is shared and read-only.
var emptyUserMessage = rec{"role": "user", "content": ""}

// safeStart is retention.ts:50-55.
func safeStart(entry projectedEntry) bool {
	if entryType(entry.sourceEntry) == "compaction" {
		return false
	}
	for _, message := range entry.messages {
		role, _ := str(message, "role")
		if role != "system" && isCutRole(role) {
			return true
		}
	}
	return false
}

// messageCost is `estimateTokens(nativeMessage(message))` (retention.ts:56-95). nativeMessage only
// rebuilds the members estimateTokens reads (role and content, or the same members for the
// summary and bash roles), so Pi's estimate of the projected message itself is the same number
// (the SDK's EstimateTokens is Pi 1.5.0's, proved by retention_pi_test.go).
func messageCost(message rec) int { return sdk.EstimateTokens(message) }

// projectedCut is retention.ts:96-112. The native cut finder reads raw entries, so only the
// projected contributions are materialized, as the minimal records it reads: a message entry per
// projected message, and an inert custom entry for an entry that projects nothing or is a
// compaction. The TS carries the source entry's id along to map the cut back; the owner index
// does the same.
func projectedCut(entries []projectedEntry, keep int) int {
	materialized := make([]rec, 0, len(entries))
	owner := make([]int, 0, len(entries))
	for i, entry := range entries {
		if len(entry.messages) == 0 || entryType(entry.sourceEntry) == "compaction" {
			materialized = append(materialized, rec{"type": "custom"})
			owner = append(owner, i)
			continue
		}
		for _, message := range entry.messages {
			if role, _ := str(message, "role"); role == "system" {
				message = emptyUserMessage
			}
			materialized = append(materialized, rec{"type": "message", "message": message})
			owner = append(owner, i)
		}
	}
	cut := sdk.FindCutPoint(materialized, 0, len(materialized), keep)
	return owner[cut.FirstKeptEntryIndex]
}

// pairedTail is retention.ts:113-127: every tool call in the tail has its result and no result
// is orphaned.
func pairedTail(entries []projectedEntry) bool {
	var calls map[string]struct{}
	for _, entry := range entries {
		for _, message := range entry.messages {
			switch role, _ := str(message, "role"); role {
			case "assistant":
				content, _ := arrOf(message["content"])
				for _, part := range content {
					block, _ := recOf(part)
					if kind, _ := str(block, "type"); kind != "toolCall" {
						continue
					}
					id, _ := str(block, "id")
					if calls == nil {
						calls = map[string]struct{}{}
					}
					calls[id] = struct{}{}
				}
			case "toolResult":
				id, _ := str(message, "toolCallId")
				if _, ok := calls[id]; !ok {
					return false
				}
				delete(calls, id)
			}
		}
	}
	return len(calls) == 0
}

// boundedTail is retention.ts:128-158.
func boundedTail(entries []projectedEntry, start, maxTokens int) retainedTail {
	costs := make([]int, len(entries)-start)
	tokens := 0
	for offset, entry := range entries[start:] {
		if entryType(entry.sourceEntry) == "compaction" {
			continue
		}
		for _, message := range entry.messages {
			if role, _ := str(message, "role"); role != "system" {
				costs[offset] += messageCost(message)
			}
		}
		tokens += costs[offset]
	}
	for offset, cost := range costs {
		index := start + offset
		entry := entries[index]
		if tokens > 0 && tokens <= maxTokens && safeStart(entry) {
			tail := entries[index:]
			if pairedTail(tail) {
				ids := make(map[string]struct{}, len(tail))
				for _, retained := range tail {
					if id, ok := str(retained.sourceEntry, "id"); ok {
						ids[id] = struct{}{}
					}
				}
				id, ok := str(entry.sourceEntry, "id")
				return retainedTail{firstKeptEntryID: id, hasFirstKept: ok, ids: ids}
			}
		}
		tokens -= cost
	}
	return retainedTail{}
}

// retainedTailOf is retainedTail (retention.ts:159-182).
func retainedTailOf(projected []projectedEntry, capacity recoveryCapacity, maxTokens int) (retainedTail, error) {
	settings, err := capacity.host.getCompactionSettings()
	if err != nil {
		return retainedTail{}, err
	}
	keep := sdk.DefaultCompactionSettings.KeepRecentTokens
	if settings.keepRecentTokens != nil {
		keep = *settings.keepRecentTokens
	}
	if keep <= 0 || maxTokens <= 0 {
		return retainedTail{}, nil
	}
	boundary := -1
	for i, entry := range projected {
		if entryType(entry.sourceEntry) == "compaction" && len(entry.messages) > 0 {
			boundary = i
			break
		}
	}
	entries := projected[boundary+1:]
	if len(entries) == 0 {
		return retainedTail{}, nil
	}
	prepared := -1
	for i, entry := range entries {
		id, present := entry.sourceEntry["id"]
		if sourceIDMatches(id, present, capacity.firstKeptEntryID, capacity.hasFirstKept) {
			prepared = i
			break
		}
	}
	start := prepared
	if start < 0 {
		start = projectedCut(entries, keep)
	}
	if start < 0 {
		return retainedTail{}, nil
	}
	return boundedTail(entries, start, maxTokens), nil
}

// sourceIDMatches is `entry.sourceEntry.id === firstKeptEntryId` (retention.ts:178-180) with
// undefined as an absent key.
func sourceIDMatches(id any, present bool, want string, wantPresent bool) bool {
	if !wantPresent {
		return !present
	}
	s, ok := id.(string)
	return ok && s == want
}

// automaticRecovery is retention.ts:183-209. Recent messages and recovery share half the fresh
// operational budget, leaving work headroom. nil means there is no room for a record.
func automaticRecovery(entries []rec, projected []projectedEntry, capacity recoveryCapacity, ownerQuestionRegistered bool) (*autoRecovery, error) {
	payload, err := freshPayloadChars(capacity.host, capacity.toolTokens, capacity.pendingMessages, maxSafeInteger)
	if err != nil {
		return nil, err
	}
	limit := min(maxHandoffChars, payload)
	if limit < minPageChars {
		return nil, nil
	}
	retained, err := retainedTailOf(projected, capacity, int(math.Floor(float64(payload-limit)/4)))
	if err != nil {
		return nil, err
	}
	handoff, err := buildAutoHandoff(entries, projected, limit, autoHandoffOptions{
		ownerQuestionRegistered: ownerQuestionRegistered,
		retainedEntryIds:        retained.ids,
		notes:                   capacity.notes,
	})
	if err != nil {
		return nil, err
	}
	if u16len(handoff) > limit {
		return nil, nil
	}
	return &autoRecovery{handoff: handoff, firstKeptEntryID: retained.firstKeptEntryID, hasFirstKept: retained.hasFirstKept}, nil
}
