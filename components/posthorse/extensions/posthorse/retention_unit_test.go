package posthorse

// Unit tests of the retained-tail rules of retention.go (retention.ts:113-182) on hand-built projections.

import (
	"strings"
	"testing"
)

func ruUser(id string, chars int) projectedEntry {
	message := rec{"role": "user", "content": strings.Repeat("x", chars)}
	return projectedEntry{sourceEntry: rec{"type": "message", "id": id}, messages: []rec{message}}
}

func ruAssistant(id string, chars int, calls ...string) projectedEntry {
	content := []any{rec{"type": "text", "text": strings.Repeat("y", chars)}}
	for _, call := range calls {
		content = append(content, rec{"type": "toolCall", "id": call, "name": "bash", "arguments": rec{}})
	}
	return projectedEntry{sourceEntry: rec{"type": "message", "id": id}, messages: []rec{{"role": "assistant", "content": content}}}
}

func ruResult(id, call string) projectedEntry {
	return projectedEntry{sourceEntry: rec{"type": "message", "id": id}, messages: []rec{{"role": "toolResult", "toolCallId": call, "toolName": "bash", "content": []any{}}}}
}

func TestBoundedTailBudget(t *testing.T) {
	entries := []projectedEntry{ruUser("m0", 40), ruAssistant("m1", 40)} // 10 tokens each
	for _, c := range []struct {
		max  int
		want string
	}{
		{20, "m0"}, // the whole tail fits exactly
		{19, "m1"}, // one token short: drop the oldest
		{10, "m1"},
		{9, ""}, // nothing fits
	} {
		got := boundedTail(entries, 0, c.max)
		if got.firstKeptEntryID != c.want || got.hasFirstKept != (c.want != "") {
			t.Errorf("max %d: first kept %q (%v), want %q", c.max, got.firstKeptEntryID, got.hasFirstKept, c.want)
		}
		if c.want != "" && len(got.ids) == 0 {
			t.Errorf("max %d: the retained ids must list the tail", c.max)
		}
	}
}

func TestBoundedTailNeedsPairedToolCalls(t *testing.T) {
	// A tail that starts at the result would orphan it, and one that ends before its result would leave a call open.
	entries := []projectedEntry{ruUser("m0", 4), ruAssistant("m1", 4, "c1"), ruResult("m2", "c1"), ruAssistant("m3", 4)}
	if got := boundedTail(entries, 0, 1_000); got.firstKeptEntryID != "m0" {
		t.Errorf("a paired tail keeps everything, got %q", got.firstKeptEntryID)
	}
	// 1+3+0+1 tokens: m1 starts a paired tail of 4, so it wins at 4 and loses at 3 (the result m2 is no start).
	if got := boundedTail(entries, 0, 4); got.firstKeptEntryID != "m1" {
		t.Errorf("the call with its result is a paired tail, got %q", got.firstKeptEntryID)
	}
	if got := boundedTail(entries, 0, 3); got.firstKeptEntryID != "m3" {
		t.Errorf("only the tail after the pair fits, got %q", got.firstKeptEntryID)
	}
	if pairedTail(entries[2:]) {
		t.Errorf("a result without its call is not a paired tail")
	}
	if pairedTail(entries[:2]) {
		t.Errorf("a call without its result is not a paired tail")
	}
}

func TestRetainedTailUsesCompactionBoundaryAndPreparedEntry(t *testing.T) {
	boundary := projectedEntry{
		sourceEntry: rec{"type": "compaction", "id": "boundary"},
		messages:    []rec{{"role": "compactionSummary", "summary": "s"}},
	}
	entries := []projectedEntry{ruUser("before-boundary", 4), boundary, ruUser("m0", 40), ruAssistant("m1", 40)}
	keep := 20_000
	capacity := recoveryCapacity{host: roPolicy{settings: compactionPolicy{enabled: true, reserveTokens: 1, keepRecentTokens: &keep}}}
	got, err := retainedTailOf(entries, capacity, 1_000)
	if err != nil || got.firstKeptEntryID != "m0" || len(got.ids) != 2 {
		t.Errorf("only entries after the boundary are retained, got %+v (%v)", got, err)
	}
	capacity.firstKeptEntryID, capacity.hasFirstKept = "m1", true
	got, _ = retainedTailOf(entries, capacity, 1_000)
	if got.firstKeptEntryID != "m1" {
		t.Errorf("a prepared first kept entry wins over the native cut, got %q", got.firstKeptEntryID)
	}
	zero := 0
	capacity.host = roPolicy{settings: compactionPolicy{enabled: true, reserveTokens: 1, keepRecentTokens: &zero}}
	if got, _ = retainedTailOf(entries, capacity, 1_000); got.hasFirstKept || len(got.ids) != 0 {
		t.Errorf("keepRecentTokens 0 retains nothing, got %+v", got)
	}
	if got, _ = retainedTailOf(entries, recoveryCapacity{host: roPolicy{}}, 0); got.hasFirstKept {
		t.Errorf("no token room retains nothing, got %+v", got)
	}
}
