package posthorse

// Tests that pin the rollover lane's behaviour where the mutation check (port/mutations/RolloverLane.json)
// found a branch no other test reached: the retention rules on hand-built projections, the reminder
// predicates, the walk and cache of the reminder lookup, the abort and cancellation checks, and the exact
// shapes of the turn_end drafts. Each test names the TS code it follows.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ---- harness helpers ---------------------------------------------------------------------------------------

// roRawEvent fires an event at the first handler and returns the raw reply and the failure text of the request.
func roRawEvent(f *fixture, event string, data rec) (json.RawMessage, string) {
	f.g.t.Helper()
	host, leave := f.enter()
	defer leave()
	host.h.mu.Lock()
	ids := append([]int(nil), host.h.allHandlers[event]...)
	host.h.mu.Unlock()
	if len(ids) == 0 {
		f.g.t.Fatalf("no handler for %s", event)
	}
	return fxStartRequest(host.h, fxEventRequest(event, ids[0], data)).wait()
}

// roRunSignal sends the host's run_signal frame: the run in progress is active and, or not, aborted. Frames are
// read in order, so the next request sees it.
func roRunSignal(f *fixture, active, aborted bool) {
	f.g.t.Helper()
	host := f.H()
	host.write(map[string]any{"type": "notify", "notify": map[string]any{
		"method": "run_signal", "args": map[string]any{"run": 1, "active": active, "aborted": aborted},
	}})
}

// roCustom builds a fixture factory whose extension is made by build, over the policy snapshot of newPosthorse.
func roCustom(build func(e *sdk.Extension, policy snapshotReader)) func(policyReader) *sdk.Extension {
	return func(read policyReader) *sdk.Extension {
		e := sdk.New("posthorse")
		build(e, func(ctx sdk.Context, live *compactionPolicy) policyContext { return policySnapshot(ctx, read, live) })
		return e
	}
}

func roNoNotes(sdk.Context) (*notesRecovery, error) { return nil, nil }

func roTokens(n int) toolTokensFunc { return func(sdk.Context) (int, error) { return n, nil } }

func roMessageText(m rec) string { s, _ := m["content"].(string); return s }

// ---- retention.ts: tail rules ------------------------------------------------------------------------------

func ruCompaction(id string, summaryChars int) projectedEntry {
	return projectedEntry{
		sourceEntry: rec{"type": "compaction", "id": id},
		messages:    []rec{{"role": "compactionSummary", "summary": strings.Repeat("s", summaryChars)}},
	}
}

func ruSystem(id string, chars int) projectedEntry {
	return projectedEntry{sourceEntry: rec{"type": "message", "id": id}, messages: []rec{{"role": "system", "content": strings.Repeat("z", chars)}}}
}

// retention.ts:50-55 safeStart and 128-158 boundedTail.
func TestBoundedTailSkipsInertAndSystemEntries(t *testing.T) {
	first := func(entries []projectedEntry, max int) string {
		got := boundedTail(entries, 0, max)
		if !got.hasFirstKept {
			return ""
		}
		return got.firstKeptEntryID
	}
	if got := first([]projectedEntry{ruCompaction("c", 4), ruUser("u", 40)}, 1_000); got != "u" {
		t.Errorf("a compaction entry is never a safe start, got %q", got)
	}
	if got := first([]projectedEntry{ruSystem("s", 40), ruUser("u", 40)}, 1_000); got != "u" {
		t.Errorf("a system message is never a safe start, got %q", got)
	}
	// Compaction entries cost nothing (retention.ts:138-140): 10 + 0 + 10 tokens fit 20.
	if got := first([]projectedEntry{ruUser("u0", 40), ruCompaction("c", 400), ruAssistant("a1", 40)}, 20); got != "u0" {
		t.Errorf("a compaction summary must not be counted, got %q", got)
	}
	// System messages cost nothing either (retention.ts:42).
	if got := first([]projectedEntry{ruUser("u0", 40), ruSystem("s", 400), ruAssistant("a1", 40)}, 20); got != "u0" {
		t.Errorf("a system message must not be counted, got %q", got)
	}
	// A tail with no tokens is not retained (`tokens > 0`).
	if got := first([]projectedEntry{ruUser("empty", 0)}, 1_000); got != "" {
		t.Errorf("a zero-token tail is not a tail, got %q", got)
	}
}

// retention.ts:96-112 projectedCut.
func TestProjectedCutMaterialization(t *testing.T) {
	// A compaction entry is materialized as an inert entry, not as its summary message: the cut moves back over it
	// to its own index (a message there would be a cut point and stop the walk at the next entry).
	if got := projectedCut([]projectedEntry{ruUser("u0", 40), ruCompaction("c", 4), ruAssistant("a1", 40)}, 1); got != 1 {
		t.Errorf("compaction entry: cut at %d, want 1", got)
	}
	// An entry that projects nothing is inert but present: the cut moves back over it (findCutPoint's metadata walk).
	meta := projectedEntry{sourceEntry: rec{"type": "label", "id": "meta"}}
	if got := projectedCut([]projectedEntry{ruUser("u1", 40), meta, ruAssistant("a1", 40)}, 1); got != 1 {
		t.Errorf("empty entry: cut at %d, want 1", got)
	}
	// A system message becomes an empty user message, a valid cut point (retention.ts:84-86).
	if got := projectedCut([]projectedEntry{ruSystem("s", 40), ruAssistant("a1", 40)}, 20_000); got != 0 {
		t.Errorf("system message: cut at %d, want 0", got)
	}
	// The cut maps back to the entry that owns the message, not to the materialized index.
	two := projectedEntry{sourceEntry: rec{"type": "message", "id": "two"}, messages: []rec{{"role": "user", "content": strings.Repeat("a", 40)}, {"role": "assistant", "content": []any{rec{"type": "text", "text": strings.Repeat("b", 40)}}}}}
	if got := projectedCut([]projectedEntry{two, ruUser("u1", 40)}, 1); got != 1 {
		t.Errorf("owner mapping: cut at %d, want 1", got)
	}
	// The native estimate is over the whole list: a larger keep moves the cut earlier.
	long := []projectedEntry{ruUser("u0", 400), ruAssistant("a1", 400), ruUser("u2", 400), ruAssistant("a3", 400)}
	if a, b := projectedCut(long, 150), projectedCut(long, 20_000); a != 2 || b != 0 {
		t.Errorf("keep 150 cuts at %d (want 2), keep 20000 at %d (want 0)", a, b)
	}
}

// retention.ts:159-182 retainedTail: the boundary, the prepared entry and the native cut.
func TestRetainedTailPreparedEntryAndBoundary(t *testing.T) {
	keep := 150
	host := roPolicy{settings: compactionPolicy{enabled: true, reserveTokens: 1, keepRecentTokens: &keep}}
	run := func(entries []projectedEntry, prepared string) (string, int) {
		capacity := recoveryCapacity{host: host}
		if prepared != "" {
			capacity.firstKeptEntryID, capacity.hasFirstKept = prepared, true
		}
		got, err := retainedTailOf(entries, capacity, 10_000)
		if err != nil {
			t.Fatal(err)
		}
		return got.firstKeptEntryID, len(got.ids)
	}
	tail := []projectedEntry{ruUser("u0", 400), ruAssistant("a1", 400), ruUser("u2", 400), ruAssistant("a3", 400)}
	// keep 150 tokens: the native cut starts at u2 (100 + 100 >= 150 counting back from a3).
	if id, n := run(tail, ""); id != "u2" || n != 2 {
		t.Errorf("native cut: first kept %q with %d ids, want u2 with 2", id, n)
	}
	// A prepared id at index 0 is honoured (`prepared >= 0`), not recomputed.
	if id, _ := run(tail, "u0"); id != "u0" {
		t.Errorf("prepared first entry: first kept %q, want u0", id)
	}
	if id, _ := run(tail, "a1"); id != "a1" {
		t.Errorf("prepared entry: first kept %q, want a1", id)
	}
	// The boundary entry itself is not searched: naming it as the prepared id falls back to the native cut.
	if id, _ := run(append([]projectedEntry{ruCompaction("B", 4)}, tail...), "B"); id != "u2" {
		t.Errorf("prepared id of the boundary: first kept %q, want the native cut u2", id)
	}
	// The boundary is the first compaction that has messages; an earlier empty one is ordinary history.
	empty := projectedEntry{sourceEntry: rec{"type": "compaction", "id": "E"}}
	if id, _ := run(append([]projectedEntry{empty, ruCompaction("B", 4)}, tail...), "B"); id != "u2" {
		t.Errorf("after an empty compaction: first kept %q, want the native cut u2", id)
	}
	// Nothing after the boundary retains nothing.
	if id, n := run([]projectedEntry{ruCompaction("B", 4)}, ""); id != "" || n != 0 {
		t.Errorf("an empty window retains %q with %d ids", id, n)
	}
}

// ---- retention.ts:183-209 automaticRecovery ----------------------------------------------------------------

// roCountingPolicy fails its n-th read of one getter.
type roCountingPolicy struct {
	base   roPolicy
	mu     sync.Mutex
	calls  map[string]int
	failOn string
	failAt int
}

func (p *roCountingPolicy) hit(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[name]++
	if p.failOn == name && p.calls[name] == p.failAt {
		return errors.New("host read failed: " + name)
	}
	return nil
}

func (p *roCountingPolicy) getCompactionSettings() (compactionPolicy, error) {
	return p.base.settings, p.hit("settings")
}
func (p *roCountingPolicy) getContextUsage() (*contextUsage, error) {
	return p.base.usage, p.hit("usage")
}
func (p *roCountingPolicy) getSystemPrompt() (string, error) { return p.base.prompt, p.hit("prompt") }

func roRecoveryHost(window int) roPolicy {
	return roPolicy{settings: compactionPolicy{enabled: true, reserveTokens: 1000}, usage: &contextUsage{contextWindow: window}, prompt: "You are a test assistant."}
}

func roBranchOf(entries ...projectedEntry) ([]rec, []projectedEntry) {
	branch := make([]rec, len(entries))
	for i, e := range entries {
		branch[i] = rec{"type": "message", "id": e.sourceEntry["id"], "message": e.messages[0]}
	}
	return branch, entries
}

// retention.ts:196-207: the recent tail gets a quarter of what the handoff limit leaves of the payload.
func TestAutomaticRecoveryTailShare(t *testing.T) {
	var host roPolicy
	var payload int
	for window := 13_000; ; window += 25 {
		host = roRecoveryHost(window)
		var err error
		if payload, err = freshPayloadChars(host, 0, nil, maxSafeInteger); err != nil {
			t.Fatal(err)
		}
		if payload-maxHandoffChars >= 400 {
			break
		}
		if window > 60_000 {
			t.Fatal("no window leaves a tail share")
		}
	}
	share := (payload - maxHandoffChars) / 4
	recover := func(chars int) *autoRecovery {
		branch, projected := roBranchOf(ruUser("m0", chars))
		got, err := automaticRecovery(branch, projected, recoveryCapacity{host: host}, false)
		if err != nil || got == nil {
			t.Fatalf("recovery: %v, %v", got, err)
		}
		return got
	}
	if got := recover(4 * (share / 2)); !got.hasFirstKept || got.firstKeptEntryID != "m0" {
		t.Errorf("a tail of half the share must be retained, got %+v", got)
	}
	if got := recover(4 * (share + share/2)); got.hasFirstKept {
		t.Errorf("a tail of 1.5 shares must not be retained, got %+v", got)
	}
}

// retention.ts:183-209 under host read failures: every read of the policy is propagated.
func TestAutomaticRecoveryPropagatesHostErrors(t *testing.T) {
	branch, projected := roBranchOf(ruUser("m0", 40))
	run := func(p *roCountingPolicy) error {
		_, err := automaticRecovery(branch, projected, recoveryCapacity{host: p}, false)
		return err
	}
	counting := &roCountingPolicy{base: roRecoveryHost(30_000), calls: map[string]int{}}
	if err := run(counting); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings", "usage", "prompt"} {
		for n := 1; n <= counting.calls[name]; n++ {
			p := &roCountingPolicy{base: roRecoveryHost(30_000), calls: map[string]int{}, failOn: name, failAt: n}
			if err := run(p); err == nil {
				t.Errorf("read %d of %s failed but automaticRecovery returned no error", n, name)
			}
		}
	}
}

// retention.ts:183-187: the owner-question flag selects which answers count as owner input.
func TestAutomaticRecoveryOwnerQuestionFlag(t *testing.T) {
	branch := []rec{
		rcMsg("ask", rec{"role": "assistant", "content": []any{rcCall("q1", "ask_question", rec{"question": "Which?"})}}),
		rcMsg("answer", rec{"role": "toolResult", "toolCallId": "q1", "toolName": "ask_question", "isError": false, "content": "THE_OWNER_ANSWER"}),
	}
	projected := []projectedEntry{
		{sourceEntry: branch[0], messages: []rec{branch[0]["message"].(rec)}},
		{sourceEntry: branch[1], messages: []rec{branch[1]["message"].(rec)}},
	}
	none := 0
	host := roRecoveryHost(30_000)
	host.settings.keepRecentTokens = &none // nothing is retained, so the inputs are listed in the record
	for _, owner := range []bool{false, true} {
		got, err := automaticRecovery(branch, projected, recoveryCapacity{host: host}, owner)
		if err != nil || got == nil {
			t.Fatal(err)
		}
		if has := strings.Contains(got.handoff, "owner answer via ask_question"); has != owner {
			t.Errorf("ask_question registered=%v: owner answer in record = %v\n%s", owner, has, got.handoff)
		}
	}
}

// ---- reminders.ts: predicates ------------------------------------------------------------------------------

func TestReminderMatchesIsStrict(t *testing.T) {
	cw, rs := 100_000, 16_384
	fp := reminderFingerprint{windowId: "w", contextWindow: &cw, reserveTokens: &rs}
	full := func(mod func(rec)) rec {
		d := rec{"windowId": "w", "contextWindow": float64(cw), "reserveTokens": float64(rs)}
		mod(d)
		return d
	}
	for name, c := range map[string]struct {
		details any
		fp      reminderFingerprint
		want    bool
	}{
		"equal":                  {full(func(rec) {}), fp, true},
		"not a record":           {"w", fp, false},
		"nil details":            {nil, fp, false},
		"other window":           {full(func(d rec) { d["windowId"] = "x" }), fp, false},
		"no window":              {full(func(d rec) { delete(d, "windowId") }), fp, false},
		"numeric window":         {full(func(d rec) { d["windowId"] = float64(1) }), fp, false},
		"other context window":   {full(func(d rec) { d["contextWindow"] = float64(1) }), fp, false},
		"other reserve":          {full(func(d rec) { d["reserveTokens"] = float64(1) }), fp, false},
		"missing context window": {full(func(d rec) { delete(d, "contextWindow") }), fp, false},
		"missing reserve":        {full(func(d rec) { delete(d, "reserveTokens") }), fp, false},
		"string context window":  {full(func(d rec) { d["contextWindow"] = "100000" }), fp, false},
		"undefined numbers match only absent members": {rec{"windowId": "w"}, reminderFingerprint{windowId: "w"}, true},
		"undefined number never matches null":         {rec{"windowId": "w", "contextWindow": nil}, reminderFingerprint{windowId: "w"}, false},
		"undefined number never matches a value":      {full(func(rec) {}), reminderFingerprint{windowId: "w"}, false},
	} {
		if got := reminderMatches(c.details, c.fp); got != c.want {
			t.Errorf("%s: reminderMatches = %v, want %v", name, got, c.want)
		}
	}
}

func TestHasReminderNeedsTheReminderMessage(t *testing.T) {
	cw, rs := 100_000, 16_384
	fp := reminderFingerprint{windowId: "w", contextWindow: &cw, reserveTokens: &rs}
	details := rec{"windowId": "w", "contextWindow": float64(cw), "reserveTokens": float64(rs)}
	entry := func(kind, customType string, d rec) rec {
		return rec{"type": kind, "customType": customType, "details": d}
	}
	if !hasReminder([]rec{entry("message", "x", nil), entry("custom_message", reminderType, details)}, fp) {
		t.Errorf("a matching reminder message must be found")
	}
	for name, e := range map[string]rec{
		"a plain custom entry":    entry("custom", reminderType, details),
		"another custom type":     entry("custom_message", "other", details),
		"details of another term": entry("custom_message", reminderType, rec{"windowId": "w"}),
		"no details":              entry("custom_message", reminderType, nil),
	} {
		if hasReminder([]rec{e}, fp) {
			t.Errorf("%s must not count as the reminder", name)
		}
	}
}

// reminders.ts:60-78 reminderBudget.
func TestReminderBudgetSkips(t *testing.T) {
	tokens := 80_000
	policy := func(window int, tok *int, enabled bool, reserve int) roPolicy {
		return roPolicy{settings: compactionPolicy{enabled: enabled, reserveTokens: reserve}, usage: &contextUsage{contextWindow: window, tokens: tok}}
	}
	for name, c := range map[string]struct {
		host roPolicy
		skip string
	}{
		"in the band":        {policy(100_000, &tokens, true, 16_384), ""},
		"unknown tokens":     {policy(100_000, nil, true, 16_384), "unknown-usage"},
		"no window":          {policy(0, &tokens, true, 16_384), "unknown-usage"},
		"compaction off":     {policy(100_000, &tokens, false, 16_384), "unavailable-budget"},
		"unsupported budget": {policy(5_000, &tokens, true, 4_000), "unavailable-budget"},
		"below the band":     {policy(100_000, new(1_000), true, 16_384), "outside-band"},
	} {
		skip, b, err := reminderBudget(c.host)
		if err != nil || skip != c.skip || (skip == "") != (b != nil) {
			t.Errorf("%s: skip %q, budget %v, err %v; want %q", name, skip, b, err, c.skip)
		}
	}
	if skip, _, _ := reminderBudget(roPolicy{}); skip != "unknown-usage" {
		t.Errorf("no usage at all must skip as unknown-usage, got %q", skip)
	}
	failing := &roCountingPolicy{base: policy(100_000, &tokens, true, 16_384), calls: map[string]int{}, failOn: "usage", failAt: 1}
	if _, _, err := reminderBudget(failing); err == nil {
		t.Errorf("a failing usage read must be propagated")
	}
}

// ---- reminders.ts: decision, run end, hooks --------------------------------------------------------------

func roInBand(t *testing.T, opts ...fixOpt) *fixture {
	t.Helper()
	return newFixture(t, append([]fixOpt{withUsage(100_000, 80_000)}, opts...)...)
}

func roEnd(extra rec) rec {
	event := rec{
		"message":     rec{"role": "assistant", "stopReason": "stop", "content": []any{}},
		"toolResults": []any{}, "entries": []any{}, "continue": false,
		"context": rec{"pendingMessages": []any{}},
	}
	for k, v := range extra {
		event[k] = v
	}
	return event
}

// reminders.ts:79-90 endsRun and 91-123 reminderDecision.
func TestReminderDecisionRunEndAndPending(t *testing.T) {
	pendingReminder := func(window string) any {
		return rec{"role": "custom", "customType": reminderType, "details": roDetails(window, 100_000, 16_384)}
	}
	for name, c := range map[string]struct {
		event rec
		want  int
	}{
		"the final answer ends the run":            {roEnd(nil), 0},
		"a tool result turn is not a final answer": {roEnd(rec{"message": rec{"role": "toolResult", "content": []any{}}}), 1},
		"a custom message turn is not an answer":   {roEnd(rec{"message": rec{"role": "custom", "content": "x"}}), 1},
		"continue keeps the run alive":             {roEnd(rec{"continue": true}), 1},
		"queued messages keep the run alive":       {roEnd(rec{"context": rec{"pendingMessages": []any{rec{"role": "user", "content": "more"}}}}), 1},
		"a matching queued reminder is enough":     {roEnd(rec{"continue": true, "context": rec{"pendingMessages": []any{pendingReminder("initial")}}}), 0},
		"a queued reminder of another window":      {roEnd(rec{"continue": true, "context": rec{"pendingMessages": []any{pendingReminder("old")}}}), 1},
		"a queued non-reminder custom message":     {roEnd(rec{"continue": true, "context": rec{"pendingMessages": []any{rec{"role": "custom", "customType": "other", "details": roDetails("initial", 100_000, 16_384)}}}}), 1},
	} {
		t.Run(name, func(t *testing.T) {
			f := roInBand(t)
			f.TurnEndEvent(c.event)
			roEqual(t, len(f.Messages()), c.want, "reminders sent")
		})
	}
	t.Run("a host pending message keeps the run alive", func(t *testing.T) {
		f := roInBand(t, withPending(true))
		f.TurnEndEvent(roEnd(nil))
		roEqual(t, len(f.Messages()), 1, "reminders sent")
	})
	t.Run("a failing pending read is propagated", func(t *testing.T) {
		f := roInBand(t, withHostError("hasPendingMessages", "pending read failed"))
		if _, failure := roRawEvent(f, "turn_end", roEnd(nil)); !strings.Contains(failure, "pending read failed") {
			t.Errorf("failure %q", failure)
		}
	})
	t.Run("a failing usage read is propagated", func(t *testing.T) {
		f := roInBand(t, withHostError("getContextUsage", "usage read failed"))
		if _, failure := roRawEvent(f, "turn_end", roEnd(rec{"continue": true})); !strings.Contains(failure, "usage read failed") {
			t.Errorf("failure %q", failure)
		}
	})
	t.Run("a failing lookup is propagated", func(t *testing.T) {
		f := roInBand(t, withHostError("sessionRead.getLeafEntry", "leaf read failed"))
		if _, failure := roRawEvent(f, "turn_end", roEnd(rec{"continue": true})); !strings.Contains(failure, "leaf read failed") {
			t.Errorf("failure %q", failure)
		}
	})
}

// rollover.ts:429-447 the reminder message.
func TestReminderMessageShape(t *testing.T) {
	f := roInBand(t)
	f.TurnEndEvent(roEnd(rec{"continue": true}))
	sent := f.Messages()
	if len(sent) != 1 {
		t.Fatalf("want one reminder, got %d", len(sent))
	}
	if sent[0].CustomType != "posthorse-reminder" || sent[0].Content != "[posthorse] Routine task-note update.\n"+checkpointGuidance || !sent[0].Display || sent[0].Options["deliverAs"] != "steer" {
		t.Errorf("reminder %+v", sent[0])
	}
	roSameJSON(t, sent[0].Details, roDetails("initial", 100_000, 16_384), "fingerprint")
	g := roInBand(t, withHostError("sendMessage", "send failed"))
	if _, failure := roRawEvent(g, "turn_end", roEnd(rec{"continue": true})); !strings.Contains(failure, "send failed") {
		t.Errorf("a failing send must be propagated, got %q", failure)
	}
}

// rollover.ts:429-433: the request is consumed by an errored or aborted turn, and an aborted outcome ends the
// explicit path without falling through to the reminder.
func TestTurnEndStopsAndAbortsOwnTheTurn(t *testing.T) {
	t.Run("an aborted stop sends no reminder", func(t *testing.T) {
		f := roInBand(t)
		f.TurnEndEvent(roEnd(rec{"message": rec{"role": "assistant", "stopReason": "aborted", "content": []any{}}, "continue": true}))
		roEqual(t, len(f.Messages()), 0, "reminders sent")
	})
	t.Run("an errored stop sends no reminder", func(t *testing.T) {
		f := roInBand(t)
		f.TurnEndEvent(roEnd(rec{"message": rec{"role": "assistant", "stopReason": "error", "content": []any{}}, "continue": true}))
		roEqual(t, len(f.Messages()), 0, "reminders sent")
	})
	t.Run("an aborted outcome of a reset sends no reminder either", func(t *testing.T) {
		f := roInBand(t)
		f.Run("new_context", rec{"handoff": "continue"})
		got := f.TurnEndEvent(roEnd(rec{"toolResults": []any{roToolResult()}, "continue": true, "outcome": "aborted"}))
		if got != nil || len(f.Messages()) != 0 {
			t.Errorf("an aborted reset must do nothing, got %v and %d reminders", got, len(f.Messages()))
		}
	})
	t.Run("an aborted run does not reset", func(t *testing.T) {
		f := roInBand(t)
		f.Run("new_context", rec{"handoff": "continue"})
		roRunSignal(f, true, true)
		defer roRunSignal(f, false, false)
		if got := f.TurnEndEvent(roEnd(rec{"toolResults": []any{roToolResult()}, "continue": true})); got != nil {
			t.Errorf("the run's abort signal must stop the reset, got %v", got)
		}
	})
	t.Run("an aborted run gets no threshold record", func(t *testing.T) {
		f := newFixture(t, withUsage(100_000, 83_617), withSession(fxIndexedSession(fxMessageEntry("u1", fxUser("go")))))
		roRunSignal(f, true, true)
		defer roRunSignal(f, false, false)
		if got := f.TurnEndEvent(roFallbackEvent(roFreshBoundary(), nil)); got != nil {
			t.Errorf("an aborted run must delegate, got %v", got)
		}
	})
	t.Run("an aborted run cancels the compaction before any work", func(t *testing.T) {
		f := newFixture(t, withProjection(fxMessageEntry("u1", fxUser("go"))))
		roRunSignal(f, true, true)
		defer roRunSignal(f, false, false)
		got := f.Fire("session_before_compact", roCompactEvent("threshold", 0, "", fxMessageEntry("u1", fxUser("go"))))
		roEqual(t, got["cancel"], true, "cancel")
		roEqual(t, len(f.CallsTo("getContextUsage"))+len(f.CallsTo("appendEntry")), 0, "no host work after an aborted run")
	})
}

// rollover.ts:50-60 the explicit reset limit: a handoff of exactly the limit fits, one more does not, and the
// limit counts the tool declarations and the queued messages.
func TestExplicitResetLimit(t *testing.T) {
	const window, reserve = 12_000, 1_000
	const toolTokens = 500
	queued := rec{"role": "user", "content": strings.Repeat("q", 2_000), "timestamp": float64(1)}
	host := roPolicy{settings: compactionPolicy{enabled: true, reserveTokens: reserve}, usage: &contextUsage{contextWindow: window}, prompt: "You are a test assistant."}
	limit, err := freshPayloadChars(host, toolTokens, []rec{queued}, maxHandoffChars)
	if err != nil || limit <= 0 || limit >= maxHandoffChars {
		t.Fatalf("limit %d, %v", limit, err)
	}
	toolLimit, _ := freshPayloadChars(host, toolTokens, nil, maxHandoffChars)
	if toolLimit <= limit+1 {
		t.Fatalf("the queued message must shrink the limit (%d vs %d)", toolLimit, limit)
	}
	boundary := func(handoff int) rec {
		f := newFixture(t, withUsage(window, 1_000), withReserve(reserve), withFactory(roCustom(func(e *sdk.Extension, policy snapshotReader) {
			registerRollover(e, policy, roTokens(toolTokens), rolloverResources{resetPages: func() {}, notes: roNoNotes})
		})))
		f.Run("new_context", rec{"handoff": strings.Repeat("h", handoff)})
		return f.TurnEndEvent(roEnd(rec{
			"toolResults": []any{roToolResult()}, "continue": false,
			"context": rec{"pendingMessages": []any{queued}},
		}))
	}
	kind := func(got rec) string {
		entries, _ := got["entries"].([]any)
		if len(entries) != 1 {
			return "none"
		}
		return roRec(entries[0])["type"].(string)
	}
	if got := kind(boundary(limit)); got != "compaction" {
		t.Errorf("a handoff of exactly the limit (%d) must reset, got %s", limit, got)
	}
	if got := kind(boundary(limit + 1)); got != "custom_message" {
		t.Errorf("a handoff one over the limit must be deferred, got %s", got)
	}
	// A failing tool-declaration read while sizing the limit is propagated.
	calls := 0
	f := newFixture(t, withUsage(window, 1_000), withReserve(reserve), withFactory(roCustom(func(e *sdk.Extension, policy snapshotReader) {
		failing := func(sdk.Context) (int, error) {
			calls++
			if calls > 1 {
				return 0, errors.New("tools read failed")
			}
			return toolTokens, nil
		}
		registerRollover(e, policy, failing, rolloverResources{resetPages: func() {}, notes: roNoNotes})
	})))
	f.Run("new_context", rec{"handoff": "continue"})
	if _, failure := roRawEvent(f, "turn_end", roEnd(rec{"toolResults": []any{roToolResult()}})); !strings.Contains(failure, "tools read failed") {
		t.Errorf("failure %q", failure)
	}
}

// A handoff of 20,000 characters is the schema maximum and the cap of the limit: it fits when nothing else is queued.
func TestExplicitResetAtTheHandoffCap(t *testing.T) {
	f := newFixture(t, withUsage(400_000, 1_000), withReserve(16_384))
	f.Run("new_context", rec{"handoff": strings.Repeat("h", maxHandoffChars)})
	got := f.TurnEnd(roToolResult())
	entries, _ := got["entries"].([]any)
	if len(entries) != 1 || roRec(entries[0])["type"] != "compaction" {
		t.Errorf("a handoff at the cap must reset, got %v", got)
	}
}

// rollover.ts:429-433 a request outside a foreground tool batch fails the tool.
func TestNewContextOutsideABatchFails(t *testing.T) {
	f := newFixture(t)
	if _, failure := f.H().Tool("new_context", rec{"handoff": "x"}); !strings.Contains(failure, "Request new_context from the current foreground tool batch.") {
		t.Errorf("failure %q", failure)
	}
}

// ---- rollover.ts: registration and resets ------------------------------------------------------------------

func TestRegisterRolloverOrder(t *testing.T) {
	resets := 0
	f := newFixture(t, withFactory(roCustom(func(e *sdk.Extension, policy snapshotReader) {
		registerRollover(e, policy, roTokens(0), rolloverResources{resetPages: func() { resets++ }, notes: roNoNotes})
	})))
	want := []string{
		"tool_execution_start", "tool_execution_end", "turn_start", "session_start", "session_tree",
		"before_agent_start", "agent_settled", "context_with_system", "context", "session_before_compact", "turn_end",
	}
	roSameJSON(t, f.RegisteredEvents(), want, "handler registration order")
	roSameJSON(t, f.ToolNames(), []string{"new_context", "get_context_remaining"}, "tools")
	f.FireAll("turn_start", rec{})
	roEqual(t, resets, 1, "turn_start resets the pages")
	f.FireAll("session_start", rec{})
	roEqual(t, resets, 2, "session_start resets the pages")
	f.FireAll("session_tree", rec{})
	roEqual(t, resets, 2, "navigation does not reset the pages")
}

func TestTurnAndSessionStartDropReset_Requests(t *testing.T) {
	for _, event := range []string{"turn_start", "session_start"} {
		t.Run(event, func(t *testing.T) {
			f := newFixture(t)
			f.Run("new_context", rec{"handoff": "continue"})
			f.FireAll(event, rec{})
			if got := f.TurnEnd(roToolResult()); got != nil {
				t.Errorf("%s must clear the pending request, got %v", event, got)
			}
		})
	}
}

// reminders.ts:179-202: session lifecycle and navigation drop the cached branch facts; an unchanged leaf does not.
func TestReminderCacheLifecycle(t *testing.T) {
	session := func() *fakeSession {
		return fxIndexedSession(rec{"type": "custom", "id": "a"}, rec{"type": "custom", "id": "b"},
			rec{"type": "context_window", "id": "w"}, rec{"type": "custom", "id": "c"}, rec{"type": "custom", "id": "d"})
	}
	reads := func(f *fixture) int { return roEntryReads(f) }
	t.Run("a cold lookup stops at a context_window boundary", func(t *testing.T) {
		f := newFixture(t, withUsage(100_000, 80_000), withSession(session()), withEmptyProjection())
		f.TurnEnd()
		// leaf d, then c, then the window w: three reads, not the five of a walk to the root.
		roEqual(t, reads(f), 3, "cold reads")
	})
	for _, event := range []string{"session_tree", "session_start"} {
		t.Run(event+" invalidates", func(t *testing.T) {
			f := newFixture(t, withUsage(100_000, 80_000), withSession(session()), withEmptyProjection())
			f.TurnEnd()
			f.ResetReads()
			f.TurnEnd()
			roEqual(t, reads(f), 1, "certified reads")
			f.FireAll(event, rec{})
			f.ResetReads()
			f.TurnEnd()
			roEqual(t, reads(f), 3, "reads after "+event)
		})
	}
}

// ---- reminders.ts:129-202 the walk -------------------------------------------------------------------------

func roEntries(parentless ...rec) *roMemorySession {
	s := &roMemorySession{id: "walk", entries: map[string]rec{}}
	var parent any
	for _, e := range parentless {
		e["parentId"] = parent
		s.entries[e["id"].(string)] = e
		parent = e["id"]
		s.leaf = e["id"].(string)
	}
	return s
}

func roReminderEntry(id string) rec {
	return rec{"type": "custom_message", "id": id, "customType": reminderType, "details": rec{"windowId": "w"}}
}

func roIDs(entries []rec) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i], _ = e["id"].(string)
	}
	return ids
}

func TestScanAncestryBoundaries(t *testing.T) {
	scan := func(s *roMemorySession) (string, []string) {
		window, reminders, err := scanAncestry(&roSession{roMemorySession: s}, s.entries[s.leaf], nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		return window, roIDs(reminders)
	}
	check := func(name string, s *roMemorySession, window string, reminders ...string) {
		t.Helper()
		gotWindow, got := scan(s)
		if gotWindow != window || strings.Join(got, ",") != strings.Join(reminders, ",") {
			t.Errorf("%s: window %q reminders %v, want %q %v", name, gotWindow, got, window, reminders)
		}
	}
	check("no boundary keeps every reminder, oldest first",
		roEntries(roReminderEntry("r1"), rec{"type": "custom", "id": "x"}, roReminderEntry("r2")), "initial", "r1", "r2")
	check("a context_window cuts the history before it",
		roEntries(roReminderEntry("r1"), rec{"type": "context_window", "id": "W"}, roReminderEntry("r2"), rec{"type": "custom", "id": "x"}), "W", "r2")
	check("a Posthorse compaction is a window and a boundary",
		roEntries(roReminderEntry("r1"), rec{"type": "compaction", "id": "C", "firstKeptEntryId": "C", "details": rec{"posthorse": float64(1)}}, roReminderEntry("r2")), "C", "r2")
	check("only a reminder message counts",
		roEntries(rec{"type": "custom", "id": "x", "customType": reminderType}, rec{"type": "context_window", "id": "W"}, rec{"type": "custom", "id": "y", "customType": reminderType}), "W")
	check("an unnamed window is initial", roEntries(rec{"type": "custom", "id": "x"}), "initial")
	// Native compaction keeps a tail before the Posthorse boundary: the reminder inside the kept range counts, the
	// walk continues past the window to reach the first kept entry.
	check("a reminder in a kept range before the window",
		roEntries(rec{"type": "custom", "id": "K"}, roReminderEntry("R"), rec{"type": "compaction", "id": "W", "firstKeptEntryId": "W", "details": rec{"posthorse": float64(1)}},
			rec{"type": "custom", "id": "X"}, rec{"type": "compaction", "id": "N", "firstKeptEntryId": "K"}), "W", "R")
	check("a reminder before the first kept entry is summarized away",
		roEntries(roReminderEntry("R0"), rec{"type": "custom", "id": "K"}, roReminderEntry("R"), rec{"type": "compaction", "id": "N", "firstKeptEntryId": "K"}), "initial", "R")
	check("a compaction of null keeps nothing before it",
		roEntries(roReminderEntry("R0"), rec{"type": "compaction", "id": "N", "firstKeptEntryId": nil}, roReminderEntry("R1")), "initial", "R1")
	if got := sameEntry(nil, rec{"id": "a"}) || sameEntry(rec{"id": "a"}, nil) || sameEntry(nil, nil); got {
		t.Errorf("a missing entry never certifies")
	}
	if !sameEntry(rec{"id": "a", "x": float64(1)}, rec{"id": "a", "x": float64(1)}) || sameEntry(rec{"id": "a", "x": float64(1)}, rec{"id": "a", "x": float64(2)}) || sameEntry(rec{"id": "a"}, rec{"id": "b"}) {
		t.Errorf("an entry certifies exactly when its whole record is equal")
	}
}

func TestScanAncestryCertifiedLeafExtendsTheCache(t *testing.T) {
	s := roEntries(rec{"type": "custom", "id": "a"}, roReminderEntry("r1"), rec{"type": "custom", "id": "b"}, roReminderEntry("r2"), rec{"type": "custom", "id": "c"})
	previous := &reminderState{leaf: s.entries["b"], windowId: "cached", reminders: []rec{{"id": "kept"}}}
	counting := &roSession{roMemorySession: s}
	window, reminders, err := scanAncestry(counting, s.entries["c"], previous, 0)
	if err != nil || window != "cached" || strings.Join(roIDs(reminders), ",") != "kept,r2" {
		t.Errorf("window %q reminders %v (%v), want cached [kept r2]", window, roIDs(reminders), err)
	}
	roEqual(t, counting.entries.Load(), int64(2), "the walk stops at the certified leaf")
	// A boundary between the leaf and the previous leaf voids the certificate.
	s = roEntries(rec{"type": "custom", "id": "a"}, rec{"type": "custom", "id": "b"}, rec{"type": "context_window", "id": "W"}, rec{"type": "custom", "id": "c"})
	previous = &reminderState{leaf: s.entries["b"], windowId: "cached"}
	if window, _, _ := scanAncestry(&roSession{roMemorySession: s}, s.entries["c"], previous, 0); window != "W" {
		t.Errorf("a window after the certified leaf must win, got %q", window)
	}
}

// roFailing fails one named read of an in-memory session.
type roFailing struct {
	*roSession
	fail string
}

func (s roFailing) GetSessionID() (string, error) {
	if s.fail == "id" {
		return "", errors.New("id failed")
	}
	return s.roSession.GetSessionID()
}

func (s roFailing) GetSessionFile() (*string, error) {
	if s.fail == "file" {
		return nil, errors.New("file failed")
	}
	return s.roSession.GetSessionFile()
}

func (s roFailing) GetLeafEntry() (map[string]any, error) {
	if s.fail == "leaf" {
		return nil, errors.New("leaf failed")
	}
	return s.roSession.GetLeafEntry()
}

func (s roFailing) GetEntry(id string) (map[string]any, error) {
	if s.fail == "entry" {
		return nil, errors.New("entry failed")
	}
	return s.roSession.GetEntry(id)
}

func TestReminderLookupPropagatesAndResets(t *testing.T) {
	for _, fail := range []string{"id", "file", "leaf", "entry"} {
		s := roFailing{roSession: &roSession{roMemorySession: roLongSession(6, -1)}, fail: fail}
		if _, err := createReminderLookup().read(s); err == nil || !strings.Contains(err.Error(), fail+" failed") {
			t.Errorf("a failing %s read must be propagated, got %v", fail, err)
		}
	}
	s := &roSession{roMemorySession: roLongSession(10, -1)}
	lookup := createReminderLookup()
	first, err := lookup.read(s)
	if err != nil {
		t.Fatal(err)
	}
	cold := s.entries.Load()
	again, _ := lookup.read(s)
	if s.entries.Load() != cold || again != first {
		t.Errorf("an unchanged leaf is served from the cache (%d by-id reads after %d)", s.entries.Load(), cold)
	}
	lookup.reset()
	if _, err := lookup.read(s); err != nil || s.entries.Load() != 2*cold {
		t.Errorf("a reset forgets the branch facts: %d by-id reads after %d and %d", s.entries.Load(), cold, cold)
	}
	// A session or file change forgets them too (the TS manager identity).
	other := &roSession{roMemorySession: roLongSession(10, -1)}
	other.id = "another"
	if _, err := lookup.read(other); err != nil || other.entries.Load() != cold {
		t.Errorf("another session must be scanned from scratch: %d reads, want %d", other.entries.Load(), cold)
	}
}

// ---- reminders.ts:204-288 the retained span ----------------------------------------------------------------

func TestRetainedProjectionNeedsANewestCompaction(t *testing.T) {
	msg := func(id, parent string) sdk.ProjectedSessionEntry {
		return sdk.ProjectedSessionEntry{SourceEntry: rec{"type": "message", "id": id, "parentId": parent}, Messages: []rec{{"role": "user", "content": id}}}
	}
	compaction := func(id string) sdk.ProjectedSessionEntry {
		return sdk.ProjectedSessionEntry{SourceEntry: rec{"type": "compaction", "id": id}, Messages: []rec{{"role": "compactionSummary", "summary": "s"}}}
	}
	if got := retainedProjection([]sdk.ProjectedSessionEntry{msg("u0", ""), msg("u1", "zzz")}); got != nil {
		t.Errorf("no compaction: no retained span, got %v", got)
	}
	if got := retainedProjection([]sdk.ProjectedSessionEntry{compaction("C")}); got != nil {
		t.Errorf("a lone compaction retains nothing, got %v", got)
	}
	// The kept entries are those before the compaction's child.
	span := retainedProjection([]sdk.ProjectedSessionEntry{compaction("C"), msg("k1", "a"), msg("k2", "k1"), msg("after", "C"), msg("later", "after")})
	if len(span) != 2 || span[0].SourceEntry["id"] != "k1" || span[1].SourceEntry["id"] != "k2" {
		t.Errorf("span %v, want k1 k2", span)
	}
	// Without a child every following entry is kept.
	if span := retainedProjection([]sdk.ProjectedSessionEntry{compaction("C"), msg("k1", "a"), msg("k2", "k1")}); len(span) != 2 {
		t.Errorf("without a child the whole tail is kept, got %v", span)
	}
	stale := func(m rec) bool { return m["content"] == "k2" }
	if got := countRetainedFromProjected(span, stale); got != 1 {
		t.Errorf("a stale reminder is not counted, got %d", got)
	}
	sys := []sdk.ProjectedSessionEntry{{SourceEntry: rec{"id": "s"}, Messages: []rec{{"role": "system"}, {"role": "user"}}}}
	if got := countRetainedFromProjected(sys, nil); got != 1 {
		t.Errorf("a system message is not counted, got %d", got)
	}
	if got := countRetainedFromProjected(sys, func(rec) bool { return true }); got != 0 {
		t.Errorf("every message stale: %d", got)
	}
}

// retainedFailure: a failing projection read is propagated (reminders.ts:271-286).
func TestRetainedTailContextPropagatesProjectionFailure(t *testing.T) {
	summary := autoHandoffPrefix + "\nContext rolled over after entry boundary."
	f := newFixture(t, withHostError("sessionRead.buildSessionProjection", "projection failed"))
	_, failure := roRawEvent(f, "context", rec{"messages": []any{
		rec{"role": "compactionSummary", "summary": summary, "timestamp": float64(1)}, fxUser("kept"),
	}})
	if !strings.Contains(failure, "projection failed") {
		t.Errorf("failure %q", failure)
	}
}

// ---- rollover.ts: the context filter -------------------------------------------------------------------------

func TestContextFilterWindowAndRoleRules(t *testing.T) {
	stale := roMessage("posthorse-reminder", "stale", roDetails("old", 100_000, 16_384))
	t.Run("a marker without a string window id filters nothing", func(t *testing.T) {
		f := newFixture(t)
		marker := roMessage("context-window", "new", rec{"windowId": float64(5)})
		if got := f.ContextEvent(marker, stale); got != nil {
			t.Errorf("an unknown window must leave the list alone, got %v", got)
		}
	})
	t.Run("a marker of another role is no marker", func(t *testing.T) {
		f := newFixture(t)
		notMarker := rec{"role": "user", "customType": "context-window", "details": rec{"windowId": "new"}}
		current := roMessage("posthorse-reminder", "current", roDetails("initial", 100_000, 16_384))
		// With no window marker the window comes from the session ("initial"): the reminder of window "new" is stale.
		got := f.ContextEvent(notMarker, roMessage("posthorse-reminder", "other", roDetails("new", 100_000, 16_384)), current)
		roSameJSON(t, got["messages"], []rec{notMarker, current}, "filtered")
	})
	t.Run("the first marker wins", func(t *testing.T) {
		f := newFixture(t)
		first := roMessage("context-window", "a", rec{"windowId": "a"})
		second := roMessage("context-window", "b", rec{"windowId": "b"})
		keepA := roMessage("posthorse-reminder", "ka", roDetails("a", 100_000, 16_384))
		dropB := roMessage("posthorse-reminder", "kb", roDetails("b", 100_000, 16_384))
		got := f.ContextEvent(first, second, keepA, dropB)
		roSameJSON(t, got["messages"], []rec{first, second, keepA}, "filtered")
	})
	t.Run("a custom message of another type is not a reminder", func(t *testing.T) {
		f := newFixture(t)
		other := rec{"role": "custom", "customType": "other", "details": roDetails("old", 100_000, 16_384)}
		if got := f.ContextEvent(roMessage("context-window", "x", rec{"windowId": "new"}), other); got != nil {
			t.Errorf("only reminders are filtered, got %v", got)
		}
	})
	t.Run("a reminder is only a reminder as a custom message", func(t *testing.T) {
		f := newFixture(t)
		asUser := rec{"role": "user", "customType": reminderType, "details": roDetails("old", 100_000, 16_384)}
		current := roMessage("posthorse-reminder", "current", roDetails("new", 100_000, 16_384))
		// A real reminder is in the list, so the predicate runs over every message.
		if got := f.ContextEvent(roMessage("context-window", "x", rec{"windowId": "new"}), current, asUser); got != nil {
			t.Errorf("a user message is never a stale reminder, got %v", got)
		}
	})
	t.Run("without a marker the window is the session's", func(t *testing.T) {
		session := fxIndexedSession(rec{"type": "custom", "id": "a"}, rec{"type": "context_window", "id": "W9"}, rec{"type": "custom", "id": "b"})
		f := newFixture(t, withSession(session))
		mine := roMessage("posthorse-reminder", "mine", roDetails("W9", 100_000, 16_384))
		initial := roMessage("posthorse-reminder", "initial", roDetails("initial", 100_000, 16_384))
		got := f.ContextEvent(mine, initial)
		roSameJSON(t, got["messages"], []rec{mine}, "only the reminder of the session's window stays")
	})
	t.Run("a failing lookup is propagated", func(t *testing.T) {
		f := newFixture(t, withHostError("sessionRead.getSessionId", "id failed"))
		if _, failure := roRawEvent(f, "context", rec{"messages": []any{stale}}); !strings.Contains(failure, "id failed") {
			t.Errorf("failure %q", failure)
		}
	})
	t.Run("a failing usage read is propagated", func(t *testing.T) {
		f := newFixture(t, withHostError("getContextUsage", "usage failed"))
		marker := roMessage("context-window", "x", rec{"windowId": "new"})
		if _, failure := roRawEvent(f, "context", rec{"messages": []any{marker, stale}}); !strings.Contains(failure, "usage failed") {
			t.Errorf("failure %q", failure)
		}
	})
	t.Run("the filter runs before the reorder", func(t *testing.T) {
		summary := autoHandoffPrefix + "\nContext rolled over after entry boundary."
		session := fxMemorySession("")
		kept := session.appendMessage(fxUser("kept"))
		session.appendMessage(fxAssistant([]any{fxText("reply")}))
		session.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
		f := newFixture(t, withSession(session), withRealProjection())
		boundary := rec{"role": "compactionSummary", "summary": summary, "timestamp": float64(5)}
		old := roMessage("posthorse-reminder", "old", roDetails("old", 100_000, 16_384))
		marker := roMessage("context-window", "x", rec{"windowId": "initial"})
		// Pi's list: the record, the retained tail (user, assistant), then the later messages.
		got := f.ContextEvent(boundary, fxUser("kept"), fxAssistant([]any{fxText("reply")}), old, marker)
		roSameJSON(t, got["messages"], []rec{fxUser("kept"), fxAssistant([]any{fxText("reply")}), boundary, marker}, "stale reminder dropped, tail reordered")
	})
}

func TestChronologicalRetainedTailGuards(t *testing.T) {
	summary := autoHandoffPrefix + "\nContext rolled over after entry boundary."
	session := func() *fakeSession {
		s := fxMemorySession("")
		kept := s.appendMessage(fxUser("kept"))
		s.appendMessage(fxAssistant([]any{fxText("reply")}))
		s.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
		return s
	}
	t.Run("the first message must be a compaction summary", func(t *testing.T) {
		f := newFixture(t, withSession(session()), withRealProjection())
		notSummary := rec{"role": "custom", "customType": "x", "summary": summary, "content": "x"}
		if got := f.ContextEvent(notSummary, fxUser("kept"), fxAssistant([]any{fxText("reply")})); got != nil {
			t.Errorf("only a compaction summary opens a retained tail, got %v", got)
		}
	})
	t.Run("a record with no retained messages is left alone", func(t *testing.T) {
		s := fxMemorySession("")
		s.appendMessage(fxUser("old"))
		s.appendCompaction(summary, "", 100, rec{"posthorse": float64(1)})
		f := newFixture(t, withSession(s), withRealProjection())
		boundary := rec{"role": "compactionSummary", "summary": summary, "timestamp": float64(5)}
		if got := f.ContextEvent(boundary, fxUser("new")); got != nil {
			t.Errorf("no retained messages: nothing to move, got %v", got)
		}
	})
	t.Run("exactly the retained messages follow the record", func(t *testing.T) {
		f := newFixture(t, withSession(session()), withRealProjection())
		boundary := rec{"role": "compactionSummary", "summary": summary, "timestamp": float64(5)}
		got := f.ContextEvent(boundary, fxUser("kept"), fxAssistant([]any{fxText("reply")}))
		if got == nil {
			t.Fatalf("the tail must move when it is the whole rest of the list")
		}
		roSameJSON(t, got["messages"], []rec{fxUser("kept"), fxAssistant([]any{fxText("reply")}), boundary}, "tail first")
	})
	t.Run("a record without a timestamp gives the sentinel none", func(t *testing.T) {
		s := fxMemorySession("")
		s.appendMessage(fxUser("old"))
		kept := s.appendMessage(fxAssistant([]any{fxText("reply")}))
		s.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
		f := newFixture(t, withSession(s), withRealProjection())
		got := f.ContextEvent(rec{"role": "compactionSummary", "summary": summary}, fxAssistant([]any{fxText("reply")}))
		messages, _ := got["messages"].([]any)
		if len(messages) != 3 {
			t.Fatalf("want sentinel, tail, record; got %v", got)
		}
		if _, has := roRec(messages[0])["timestamp"]; has {
			t.Errorf("the sentinel copies the record's timestamp only when it has one: %v", messages[0])
		}
		roEqual(t, roRec(messages[0])["role"], "custom", "sentinel role")
	})
	t.Run("the sentinel is only for an assistant head", func(t *testing.T) {
		s := fxMemorySession("")
		kept := s.appendMessage(fxUser("kept"))
		s.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
		f := newFixture(t, withSession(s), withRealProjection())
		got := f.ContextEvent(rec{"role": "compactionSummary", "summary": summary}, fxUser("kept"))
		messages, _ := got["messages"].([]any)
		if len(messages) != 2 || roRec(messages[0])["role"] != "user" {
			t.Errorf("a user head needs no sentinel, got %v", got)
		}
	})
}

// ---- rollover.ts: the freshness rules of the threshold fallback -----------------------------------------------

func TestFreshWindowRules(t *testing.T) {
	boundary := ruCompaction("B", 4)
	msg := func(role string) projectedEntry {
		return projectedEntry{sourceEntry: rec{"type": "message", "id": role}, messages: []rec{{"role": role}}}
	}
	for role, fresh := range map[string]bool{
		"assistant": true, "toolResult": true, "system": true,
		"user": false, "custom": false, "bashExecution": false, "branchSummary": false, "compactionSummary": false,
	} {
		if got := freshWindowWithoutInput([]projectedEntry{boundary, msg(role)}); got != fresh {
			t.Errorf("a %s message after the boundary: fresh = %v, want %v", role, got, fresh)
		}
	}
	if freshWindowWithoutInput(nil) || freshWindowWithoutInput([]projectedEntry{msg("assistant")}) {
		t.Errorf("without a boundary the window is not a fresh rollover")
	}
	if freshWindowWithoutInput([]projectedEntry{{sourceEntry: rec{"type": "compaction", "id": "E"}}, msg("assistant")}) {
		t.Errorf("a compaction without messages is not the boundary")
	}
	// A later compaction entry is not input, even though its summary message is a turn start.
	if !freshWindowWithoutInput([]projectedEntry{boundary, ruCompaction("B2", 4), msg("assistant")}) {
		t.Errorf("a later compaction entry is inert")
	}
	if !freshWindowWithoutInput([]projectedEntry{boundary}) {
		t.Errorf("a boundary alone is a fresh window")
	}
}

// rollover.ts:349-356: ask_question is the owner-question tool only without a namespace.
func TestAskQuestionRegistered(t *testing.T) {
	answer := func(name string, namespace any) fixOpt {
		return withHostAnswer("getAllTools", func(map[string]any) (any, string) {
			tool := rec{"name": name, "description": "", "parameters": rec{}}
			if namespace != nil {
				tool["namespace"] = namespace
			}
			return rec{"tools": []any{rec{"name": "bash", "description": "", "parameters": rec{}}, tool}}, ""
		})
	}
	var got bool
	var gotErr error
	factory := withFactory(roCustom(func(e *sdk.Extension, _ snapshotReader) {
		e.OnEvent(sdk.EventTurnStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
			got, gotErr = askQuestionRegistered(ctx)
			return nil, nil
		})
	}))
	for name, c := range map[string]struct {
		opt  fixOpt
		want bool
	}{
		"registered":           {answer("ask_question", nil), true},
		"another name":         {answer("ask_questions", nil), false},
		"a namespaced tool":    {answer("ask_question", rec{"name": "mcp", "description": "x"}), false},
		"no tools":             {answer("bash", nil), false},
		"the tools read fails": {withHostError("getAllTools", "tools failed"), false},
	} {
		got, gotErr = !c.want, nil
		f := newFixture(t, factory, c.opt)
		f.FireAll("turn_start", rec{})
		if name == "the tools read fails" {
			if gotErr == nil || !strings.Contains(gotErr.Error(), "tools failed") {
				t.Errorf("%s: err %v", name, gotErr)
			}
			continue
		}
		if gotErr != nil || got != c.want {
			t.Errorf("%s: got %v (%v), want %v", name, got, gotErr, c.want)
		}
	}
}

// ---- rollover.ts:249-303 the threshold fallback -------------------------------------------------------------------

func TestThresholdFallbackInputsAndErrors(t *testing.T) {
	const window, reserve = 100_000, 16_384
	rolloverAt := window - reserve + 1
	base := func(opts ...fixOpt) *fixture {
		return newFixture(t, append([]fixOpt{withUsage(window, rolloverAt), withSession(fxIndexedSession(fxMessageEntry("u1", fxUser("keep going on the fix"))))}, opts...)...)
	}
	t.Run("queued messages shrink the record or remove it", func(t *testing.T) {
		huge := rec{"role": "user", "content": strings.Repeat("q", 800_000), "timestamp": float64(1)}
		event := roFallbackEvent(roFreshBoundary(), rec{"context": rec{"pendingMessages": []any{huge}, "contextEntries": roFreshBoundary()}})
		if got := base().TurnEndEvent(event); got != nil {
			t.Errorf("no room left after the queue: delegate, got %v", got)
		}
	})
	t.Run("the owner-question tool decides which answers are owner input", func(t *testing.T) {
		ask := rcMsg("ask", rec{"role": "assistant", "content": []any{rcCall("q1", "ask_question", rec{"question": "Which?"})}})
		answer := rcMsg("answer", rec{"role": "toolResult", "toolCallId": "q1", "toolName": "ask_question", "isError": false, "content": "THE_OWNER_ANSWER"})
		session := fxIndexedSession(ask, answer)
		toolsWith := func(name string) fixOpt {
			return withHostAnswer("getAllTools", func(map[string]any) (any, string) {
				return rec{"tools": []any{rec{"name": name, "description": "", "parameters": rec{}}}}, ""
			})
		}
		none := 0
		noTail := withPolicyReader(func(sdk.Context) (compactionPolicy, error) {
			return compactionPolicy{enabled: true, reserveTokens: reserve, keepRecentTokens: &none}, nil
		})
		projected := roFreshBoundary(
			rec{"sourceEntry": ask, "messages": []any{ask["message"]}},
			rec{"sourceEntry": answer, "messages": []any{answer["message"]}},
		)
		for _, c := range []struct {
			tool string
			want bool
		}{{"ask_question", true}, {"other", false}} {
			f := newFixture(t, withUsage(window, rolloverAt), withSession(session), toolsWith(c.tool), noTail)
			got := f.TurnEndEvent(roFallbackEvent(projected, nil))
			entries, _ := got["entries"].([]any)
			if len(entries) != 1 || strings.Contains(roRec(entries[0])["summary"].(string), "owner answer via ask_question") != c.want {
				t.Errorf("tool %q: want owner answer in record = %v, got %v", c.tool, c.want, got)
			}
		}
	})
	failures := map[string]fixOpt{
		"the tools read":       withHostError("getAllTools", "tools failed"),
		"the branch read":      withHostError("sessionRead.getBranch", "branch failed"),
		"the system prompt":    withHostError("getSystemPrompt", "prompt failed"),
		"the active tool read": withHostError("getActiveTools", "active failed"),
	}
	for name, opt := range failures {
		t.Run("a failing "+name+" is propagated", func(t *testing.T) {
			f := base(opt)
			_, failure := roRawEvent(f, "turn_end", roFallbackEvent(roFreshBoundary(), nil))
			if !strings.Contains(failure, "failed") {
				t.Errorf("failure %q", failure)
			}
		})
	}
	t.Run("a failing notes read is propagated", func(t *testing.T) {
		f := newFixture(t, withUsage(window, rolloverAt), withSession(fxIndexedSession(fxMessageEntry("u1", fxUser("go")))), withFactory(roCustom(func(e *sdk.Extension, policy snapshotReader) {
			registerRollover(e, policy, roTokens(0), rolloverResources{resetPages: func() {}, notes: func(sdk.Context) (*notesRecovery, error) {
				return nil, errors.New("notes failed")
			}})
		})))
		if _, failure := roRawEvent(f, "turn_end", roFallbackEvent(roFreshBoundary(), nil)); !strings.Contains(failure, "notes failed") {
			t.Errorf("failure %q", failure)
		}
	})
	t.Run("a failing threshold read is propagated", func(t *testing.T) {
		f := base(withHostError("getContextUsage", "usage failed"))
		if _, failure := roRawEvent(f, "turn_end", roFallbackEvent(roFreshBoundary(), nil)); !strings.Contains(failure, "usage failed") {
			t.Errorf("failure %q", failure)
		}
	})
	t.Run("the continue flag is carried when present and omitted when absent", func(t *testing.T) {
		with := base().TurnEndEvent(roFallbackEvent(roFreshBoundary(), rec{"continue": false}))
		roEqual(t, with["continue"], false, "an explicit false is carried")
		event := roFallbackEvent(roFreshBoundary(), nil)
		delete(event, "continue")
		without := base().TurnEndEvent(event)
		if _, has := without["continue"]; has {
			t.Errorf("no continue in the event, none in the result: %v", without)
		}
	})
	t.Run("earlier drafts stay in front", func(t *testing.T) {
		earlier := rec{"type": "custom", "customType": "other", "data": rec{"k": "v"}}
		got := base().TurnEndEvent(roFallbackEvent(roFreshBoundary(), rec{"entries": []any{earlier}}))
		entries, _ := got["entries"].([]any)
		if len(entries) != 2 {
			t.Fatalf("want the earlier draft and ours, got %v", got)
		}
		roSameJSON(t, entries[0], earlier, "earlier draft")
	})
	t.Run("a Posthorse draft from an earlier handler is not touched by eligibility", func(t *testing.T) {
		got := base().TurnEndEvent(roFallbackEvent(roFreshBoundary(), rec{"entries": []any{rec{"type": "custom", "customType": "other"}}}))
		if got == nil {
			t.Errorf("an unrelated earlier draft must not block the fallback")
		}
	})
}

// ---- rollover.ts:310-358 prepareAutomatic ----------------------------------------------------------------------

func TestPrepareAutomaticDetails(t *testing.T) {
	user := fxMessageEntry("u1", fxUser("OWNER_REQUEST_TEXT keep working"))
	t.Run("the compaction's own settings decide the budget", func(t *testing.T) {
		f := newFixture(t, withProjection(user))
		event := roCompactEvent("threshold", 20_000, "u1", user)
		event["preparation"].(rec)["settings"].(rec)["reserveTokens"] = float64(99_000)
		if got := f.Fire("session_before_compact", event); got != nil {
			t.Errorf("the settings of the event make the budget unsupported: delegate, got %v", got)
		}
	})
	t.Run("the branch of the event is the record's source", func(t *testing.T) {
		f := newFixture(t, withProjection(user), withSession(fxMemorySession("")), withAppendToSession())
		got := f.Fire("session_before_compact", roCompactEvent("threshold", 0, "", user))
		if summary := roRec(got["compaction"])["summary"].(string); !strings.Contains(summary, "OWNER_REQUEST_TEXT") {
			t.Errorf("the record must carry the owner input of the branch entries: %q", summary)
		}
	})
	t.Run("the projection of the session is the evidence source", func(t *testing.T) {
		call := fxMessageEntry("a1", fxAssistant([]any{fxToolCall("c1", "bash", rec{"command": "ls"})}))
		result := fxMessageEntry("r1", fxToolResult("c1", "bash", []any{fxText("TOOL_OUTPUT_TEXT")}, false))
		f := newFixture(t, withProjection(call, result), withSession(fxMemorySession("")), withAppendToSession())
		event := roCompactEvent("threshold", 0, "", call, result)
		got := f.Fire("session_before_compact", event)
		summary, _ := roRec(got["compaction"])["summary"].(string)
		if !strings.Contains(summary, "TOOL_OUTPUT_TEXT") {
			t.Errorf("the projected tool evidence must reach the record: %q", summary)
		}
	})
	t.Run("an absent tokensBefore stays absent", func(t *testing.T) {
		f := newFixture(t, withProjection(user))
		event := roCompactEvent("threshold", 20_000, "u1", user)
		delete(event["preparation"].(rec), "tokensBefore")
		got := f.Fire("session_before_compact", event)
		compaction := roRec(got["compaction"])
		if _, has := compaction["tokensBefore"]; has || compaction["summary"] == nil {
			t.Errorf("no tokensBefore in the event, none in the draft: %v", compaction)
		}
	})
	t.Run("a failing prompt read cancels with its own message", func(t *testing.T) {
		f := newFixture(t, withProjection(user), withHostError("getSystemPrompt", "prompt failed"))
		got := f.Fire("session_before_compact", roCompactEvent("threshold", 20_000, "u1", user))
		roEqual(t, got["cancel"], true, "cancel")
		notes := f.CallsTo("ui.notify")
		if len(notes) != 1 || !strings.Contains(notes[0].Args["message"].(string), "prompt failed") {
			t.Errorf("notifications %v", notes)
		}
	})
	t.Run("a preparation is required", func(t *testing.T) {
		f := newFixture(t, withProjection(user))
		got := f.Fire("session_before_compact", rec{"reason": "threshold", "branchEntries": []any{}})
		roEqual(t, got["cancel"], true, "cancel")
		notes := f.CallsTo("ui.notify")
		if len(notes) != 1 || !strings.Contains(notes[0].Args["message"].(string), "Cannot read properties of undefined (reading 'settings')") {
			t.Errorf("notifications %v", notes)
		}
	})
	failures := map[string]fixOpt{
		"the tools read":       withHostError("getAllTools", "tools failed"),
		"the projection read":  withHostError("sessionRead.buildSessionProjection", "projection failed"),
		"the usage read":       withHostError("getContextUsage", "usage failed"),
		"the active tool read": withHostError("getActiveTools", "active failed"),
	}
	for name, opt := range failures {
		t.Run("a failing "+name+" cancels", func(t *testing.T) {
			f := newFixture(t, withProjection(user), opt)
			got := f.Fire("session_before_compact", roCompactEvent("threshold", 20_000, "u1", user))
			roEqual(t, got["cancel"], true, "cancel")
			notes := f.CallsTo("ui.notify")
			if len(notes) != 1 || !strings.Contains(notes[0].Args["message"].(string), "failed") {
				t.Errorf("notifications %v", notes)
			}
		})
	}
}

// rollover.ts:362-392 cancellation while the record is built, and the failure report.
func TestAutomaticCompactionCancellation(t *testing.T) {
	user := fxMessageEntry("u1", fxUser("keep working"))
	run := func(t *testing.T, notes func(sdk.Context) (*notesRecovery, error), gate, reached chan struct{}) (string, string) {
		t.Helper()
		finish := roStderr(t)
		// The policy answers without host reads, so after the last read (the notes) the hook needs the host no more:
		// a cancellation then is seen only by its own check (every host call of a cancelled request fails).
		offline := func(_ sdk.Context, live *compactionPolicy) policyContext {
			settings := compactionPolicy{enabled: true, reserveTokens: 16_384}
			if live != nil {
				settings = *live
			}
			return roPolicy{settings: settings, usage: &contextUsage{contextWindow: 100_000}, prompt: "You are a test assistant."}
		}
		f := newFixture(t, withHostMode("print"), withProjection(user), withFactory(roCustom(func(e *sdk.Extension, _ snapshotReader) {
			registerAutomaticRollover(e, offline, roTokens(0), notes)
			e.OnEvent(sdk.EventTurnStart, func(sdk.Context, map[string]any) (any, error) { return nil, nil })
		})))
		host := f.H()
		host.mu.Lock()
		id := host.handlers["session_before_compact"]
		host.mu.Unlock()
		req := fxStartRequest(host, fxEventRequest("session_before_compact", id, roCompactEvent("threshold", 20_000, "u1", user)))
		<-reached
		req.cancel("test")
		// Requests are read in order: once the next event has been served, the cancellation has been applied.
		f.FireAll("turn_start", rec{})
		close(gate)
		raw, failure := req.wait()
		if failure != "" {
			t.Fatalf("request failed: %s", failure)
		}
		return string(raw), finish()
	}
	t.Run("a cancellation after the last host read cancels quietly", func(t *testing.T) {
		gate, reached := make(chan struct{}), make(chan struct{})
		raw, stderr := run(t, func(sdk.Context) (*notesRecovery, error) {
			close(reached)
			<-gate
			return nil, nil
		}, gate, reached)
		if !strings.Contains(raw, `"cancel":true`) || strings.Contains(raw, "compaction") {
			t.Errorf("want {cancel:true}, got %s", raw)
		}
		if stderr != "" {
			t.Errorf("a cancellation is not a failure, stderr %q", stderr)
		}
	})
	t.Run("an error after a cancellation is not reported", func(t *testing.T) {
		gate, reached := make(chan struct{}), make(chan struct{})
		raw, stderr := run(t, func(sdk.Context) (*notesRecovery, error) {
			close(reached)
			<-gate
			return nil, errors.New("late failure")
		}, gate, reached)
		if !strings.Contains(raw, `"cancel":true`) {
			t.Errorf("want {cancel:true}, got %s", raw)
		}
		if stderr != "" {
			t.Errorf("a failure of a cancelled compaction is not reported, stderr %q", stderr)
		}
	})
}

// official-host.ts:2199 "automatic cancellation checks the compaction signal before its sentinel": a compaction that is
// already cancelled when the hook starts does no work: no read, no sentinel entry, no report, and answers
// {cancel:true} (rollover.ts:318-320 and 331-334). The request is cancelled before the hook body runs by a wrapper
// that holds the call until the host's cancel frame has been applied.
func TestCompactionCancelledBeforeTheHookRuns(t *testing.T) {
	user := fxMessageEntry("u1", fxUser("keep working"))
	finish := roStderr(t)
	gate, reached := make(chan struct{}), make(chan struct{})
	f := newFixture(t, withHostMode("print"), withProjection(user), withSession(fxMemorySession("")), withAppendToSession(), withFactory(roCustom(func(e *sdk.Extension, policy snapshotReader) {
		hook := automaticCompactionHook(policy, roTokens(0), roNoNotes)
		e.OnEvent(sdk.EventSessionBeforeCompact, func(ctx sdk.Context, event map[string]any) (any, error) {
			close(reached)
			<-gate
			return hook(ctx, event)
		})
		e.OnEvent(sdk.EventTurnStart, func(sdk.Context, map[string]any) (any, error) { return nil, nil })
	})))
	host := f.H()
	host.mu.Lock()
	id := host.handlers["session_before_compact"]
	host.mu.Unlock()
	// keepRecentTokens 0 and no prepared entry: an uncancelled hook would append its sentinel.
	req := fxStartRequest(host, fxEventRequest("session_before_compact", id, roCompactEvent("threshold", 0, "", user)))
	<-reached
	req.cancel("test")
	f.FireAll("turn_start", rec{}) // requests are read in order: the cancellation has been applied
	callsBefore := len(host.Calls())
	close(gate)
	raw, failure := req.wait()
	if failure != "" || !strings.Contains(string(raw), `"cancel":true`) || strings.Contains(string(raw), "compaction") {
		t.Errorf("want {cancel:true}, got %s (%q)", raw, failure)
	}
	roEqual(t, len(host.Calls()), callsBefore, "the hook made no host call after the cancellation")
	roEqual(t, len(f.Appended()), 0, "no posthorse-boundary sentinel")
	if stderr := finish(); stderr != "" {
		t.Errorf("a cancellation is not reported, stderr %q", stderr)
	}
}

// rollover.ts:429-433: a failing notes read cancels the compaction and is reported once.
func TestAutomaticCompactionNotesFailureIsReported(t *testing.T) {
	user := fxMessageEntry("u1", fxUser("keep working"))
	finish := roStderr(t)
	f := newFixture(t, withHostMode("print"), withProjection(user), withFactory(roCustom(func(e *sdk.Extension, policy snapshotReader) {
		registerAutomaticRollover(e, policy, roTokens(0), func(sdk.Context) (*notesRecovery, error) { return nil, errors.New("notes failed") })
	})))
	got := f.Fire("session_before_compact", roCompactEvent("threshold", 20_000, "u1", user))
	roEqual(t, got["cancel"], true, "cancel")
	if stderr := finish(); stderr != "Posthorse automatic rollover cancelled: notes failed\n" {
		t.Errorf("stderr %q", stderr)
	}
}

// rollover.ts:195-203: with a UI the failure is a notification of level error and nothing goes to stderr.
func TestAutomaticFailureWithAUIUsesNotify(t *testing.T) {
	user := fxMessageEntry("u1", fxUser("keep working"))
	finish := roStderr(t)
	f := newFixture(t, withProjection(user), withFactory(roCustom(func(e *sdk.Extension, policy snapshotReader) {
		registerAutomaticRollover(e, policy, roTokens(0), func(sdk.Context) (*notesRecovery, error) { return nil, errors.New("notes failed") })
	})))
	f.Fire("session_before_compact", roCompactEvent("threshold", 20_000, "u1", user))
	notes := f.CallsTo("ui.notify")
	if len(notes) != 1 || notes[0].Args["level"] != "error" || notes[0].Args["message"] != "Posthorse automatic rollover cancelled: notes failed" {
		t.Errorf("notifications %v", notes)
	}
	if stderr := finish(); stderr != "" {
		t.Errorf("a UI run must not write to stderr, got %q", stderr)
	}
}

// rollover.ts:167-194: the sentinel is the boundary entry and its id is the leaf Pi reports afterwards.
func TestAutomaticDraftBoundaryEntry(t *testing.T) {
	user := fxMessageEntry("u1", fxUser("keep working"))
	t.Run("the appended entry is the posthorse-boundary custom entry with an empty object", func(t *testing.T) {
		session := fxMemorySession("")
		f := newFixture(t, withSession(session), withAppendToSession(), withProjection(user))
		f.Fire("session_before_compact", roCompactEvent("threshold", 0, "", user))
		appended := f.Appended()
		if len(appended) != 1 || appended[0]["customType"] != "posthorse-boundary" {
			t.Fatalf("appended %v", appended)
		}
		roSameJSON(t, appended[0]["data"], rec{}, "data")
	})
	t.Run("a failing append cancels", func(t *testing.T) {
		f := newFixture(t, withSession(fxMemorySession("")), withHostError("appendEntry", "append failed"), withProjection(user))
		got := f.Fire("session_before_compact", roCompactEvent("threshold", 0, "", user))
		roEqual(t, got["cancel"], true, "cancel")
		notes := f.CallsTo("ui.notify")
		if len(notes) != 1 || !strings.Contains(notes[0].Args["message"].(string), "append failed") {
			t.Errorf("notifications %v", notes)
		}
	})
	t.Run("a failing leaf read cancels", func(t *testing.T) {
		f := newFixture(t, withSession(fxMemorySession("")), withAppendToSession(), withHostError("sessionRead.getLeafId", "leaf failed"), withProjection(user))
		got := f.Fire("session_before_compact", roCompactEvent("threshold", 0, "", user))
		roEqual(t, got["cancel"], true, "cancel")
		notes := f.CallsTo("ui.notify")
		if len(notes) != 1 || !strings.Contains(notes[0].Args["message"].(string), "leaf failed") {
			t.Errorf("notifications %v", notes)
		}
	})
	t.Run("the details carry the marker and the reason", func(t *testing.T) {
		f := newFixture(t, withProjection(user))
		got := f.Fire("session_before_compact", roCompactEvent("overflow", 20_000, "u1", user))
		roSameJSON(t, roRec(got["compaction"])["details"], rec{"posthorse": float64(1), "reason": "overflow"}, "details")
		roEqual(t, roRec(got["compaction"])["firstKeptEntryId"], "u1", "first kept")
		roEqual(t, roRec(got["compaction"])["tokensBefore"], float64(123), "tokensBefore")
	})
}

// roWithCtx runs fn on the handler context of a fixture's host (a turn_start handler of a custom extension).
func roWithCtx(t *testing.T, opts ...fixOpt) (*fixture, func(func(ctx sdk.Context))) {
	t.Helper()
	var current func(sdk.Context)
	f := newFixture(t, append([]fixOpt{withFactory(roCustom(func(e *sdk.Extension, _ snapshotReader) {
		e.OnEvent(sdk.EventTurnStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
			current(ctx)
			return nil, nil
		})
	}))}, opts...)...)
	return f, func(fn func(sdk.Context)) { current = fn; f.FireAll("turn_start", rec{}) }
}

// rollover.ts:167-194: the boundary Pi must have persisted.
func TestAutomaticDraftRejectsAnUnpersistedBoundary(t *testing.T) {
	const want = "Pi did not persist the context boundary."
	_, call := roWithCtx(t)
	call(func(ctx sdk.Context) {
		// An empty first kept entry id is no boundary.
		if _, err := automaticDraft(ctx, automaticDraftInput{handoff: "h", hasFirstKept: true}); err == nil || err.Error() != want {
			t.Errorf("an empty first kept id: err %v", err)
		}
		// No retained tail and no leaf after the sentinel: nothing to keep from.
		if _, err := automaticDraft(ctx, automaticDraftInput{handoff: "h"}); err == nil || err.Error() != want {
			t.Errorf("no leaf: err %v", err)
		}
		got, err := automaticDraft(ctx, automaticDraftInput{handoff: "h", reason: "threshold", firstKeptEntryID: "k", hasFirstKept: true})
		if err != nil {
			t.Fatal(err)
		}
		roSameJSON(t, got, rec{"compaction": rec{"summary": "h", "firstKeptEntryId": "k", "details": rec{"posthorse": float64(1), "reason": "threshold"}}}, "draft without tokensBefore")
		got, _ = automaticDraft(ctx, automaticDraftInput{handoff: "h", tokensBefore: float64(7), hasTokensBefore: true, firstKeptEntryID: "k", hasFirstKept: true})
		roEqual(t, roRec(got["compaction"])["tokensBefore"], float64(7), "tokensBefore")
	})
}

// rollover.ts:437-449: the policy of a compaction event is read from its settings.
func TestPolicyFromSettings(t *testing.T) {
	if policyFromSettings(nil) != nil || policyFromSettings("x") != nil {
		t.Errorf("settings that are not a record are undefined")
	}
	got := policyFromSettings(rec{"enabled": true, "reserveTokens": float64(4_000), "keepRecentTokens": float64(1_500)})
	if got == nil || !got.enabled || got.reserveTokens != 4_000 || got.keepRecentTokens == nil || *got.keepRecentTokens != 1_500 {
		t.Errorf("policy %+v", got)
	}
	got = policyFromSettings(rec{})
	if got == nil || got.enabled || got.reserveTokens != 0 || got.keepRecentTokens != nil {
		t.Errorf("an empty record is an empty policy, got %+v", got)
	}
}

// ---- the notes of the recovery (retention.ts:206-208, rollover.ts:248-303, 310-358) --------------------------------

// roForkNotes is the notes recovery of a fork session whose note "gone.md" is its own (deleted in the fork): the
// session's evidence names the source scope's path, and the record must name the fork's private copy. The second
// result is the evidence entries, the third the path the record names with the recovery.
func roForkNotes(t *testing.T) (*notesRecovery, []rec, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".pi", "notes")
	owner := noteOwner{id: "s1", timestamp: "2026-01-02T03:04:05.678Z"}
	scope, err := forkScope(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, scope), 0o755); err != nil {
		t.Fatal(err)
	}
	binding := &noteBinding{kind: "fork", owner: owner, root: root, scope: scope, absent: []string{"gone.md"}, source: &noteScope{root: root}}
	entries := []rec{
		rcMsg("w", rec{"role": "assistant", "content": []any{rcNotesCall("n1", "write", "gone.md", "state")}}),
		rcNotesResult("r", "n1", "notes", "Wrote "+filepath.Join(root, "gone.md")),
	}
	return &notesRecovery{binding: binding, entries: entries}, entries, filepath.Join(root, scope, "gone.md")
}

func roProjectionOfEntries(entries []rec) []projectedEntry {
	projected := make([]projectedEntry, len(entries))
	for i, e := range entries {
		projected[i] = projectedEntry{sourceEntry: e, messages: []rec{e["message"].(rec)}}
	}
	return projected
}

func TestAutomaticRecoveryUsesTheNotesRecovery(t *testing.T) {
	recovery, entries, own := roForkNotes(t)
	none := 0
	host := roRecoveryHost(30_000)
	host.settings.keepRecentTokens = &none
	run := func(r *notesRecovery) string {
		got, err := automaticRecovery(entries, roProjectionOfEntries(entries), recoveryCapacity{host: host, notes: r}, false)
		if err != nil || got == nil {
			t.Fatalf("recovery: %v, %v", got, err)
		}
		return got.handoff
	}
	if with := run(recovery); !strings.Contains(with, own) {
		t.Errorf("the record must name the fork's private copy %s:\n%s", own, with)
	}
	if without := run(nil); strings.Contains(without, own) {
		t.Errorf("without a notes recovery the record names the evidence's own path:\n%s", without)
	}
}

func TestThresholdFallbackUsesTheNotesRecovery(t *testing.T) {
	recovery, entries, own := roForkNotes(t)
	none := 0
	f := newFixture(t, withUsage(100_000, 83_617), withSession(fxIndexedSession(entries...)),
		withPolicyReader(func(sdk.Context) (compactionPolicy, error) {
			return compactionPolicy{enabled: true, reserveTokens: 16_384, keepRecentTokens: &none}, nil
		}),
		withFactory(roCustom(func(e *sdk.Extension, policy snapshotReader) {
			registerRollover(e, policy, roTokens(0), rolloverResources{resetPages: func() {}, notes: func(sdk.Context) (*notesRecovery, error) { return recovery, nil }})
		})))
	projected := roFreshBoundary(
		rec{"sourceEntry": entries[0], "messages": []any{entries[0]["message"]}},
		rec{"sourceEntry": entries[1], "messages": []any{entries[1]["message"]}},
	)
	got := f.TurnEndEvent(roFallbackEvent(projected, nil))
	list, _ := got["entries"].([]any)
	if len(list) != 1 || !strings.Contains(roRec(list[0])["summary"].(string), own) {
		t.Errorf("the fallback record must use the notes recovery (%s): %v", own, got)
	}
}

func TestPrepareAutomaticUsesTheNotesRecovery(t *testing.T) {
	recovery, entries, own := roForkNotes(t)
	f := newFixture(t, withProjection(entries...), withSession(fxMemorySession("")), withAppendToSession(), withFactory(roCustom(func(e *sdk.Extension, policy snapshotReader) {
		registerAutomaticRollover(e, policy, roTokens(0), func(sdk.Context) (*notesRecovery, error) { return recovery, nil })
	})))
	got := f.Fire("session_before_compact", roCompactEvent("threshold", 0, "", entries...))
	if summary, _ := roRec(got["compaction"])["summary"].(string); !strings.Contains(summary, own) {
		t.Errorf("the automatic record must use the notes recovery (%s): %v", own, got)
	}
}

// A note scope that cannot be read fails the record (rolloverPreambleWithNotes returns the error).
func TestAutomaticRecoveryPropagatesNoteScopeErrors(t *testing.T) {
	recovery, entries, _ := roForkNotes(t)
	root := recovery.binding.root
	if err := os.Chmod(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	if _, err := os.Stat(filepath.Join(root, "gone.md")); err == nil {
		t.Skip("directory permissions are not enforced here (running as a privileged user)")
	}
	none := 0
	host := roRecoveryHost(30_000)
	host.settings.keepRecentTokens = &none
	if _, err := automaticRecovery(entries, roProjectionOfEntries(entries), recoveryCapacity{host: host, notes: recovery}, false); err == nil {
		t.Errorf("an unreadable note scope must fail the record")
	}
}
