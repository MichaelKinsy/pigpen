package powerline_footer

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The queue core: delivery, acknowledgement, the 60 s requeue, blocking on a failed compaction, the summary cache and the
// widget rows. Behavior of index.ts 1485-1750 and the handlers around it.

func (r *queueRig) deliver(item queueItem) bool {
	r.t.Helper()
	r.q.mu.Lock()
	defer r.q.mu.Unlock()
	ok, err := r.q.deliverQueueItem(item)
	if err != nil {
		r.t.Fatal(err)
	}
	return ok
}

func (r *queueRig) get(id string) queueItem {
	r.t.Helper()
	item, err := r.store.get(id)
	if err != nil || item == nil {
		r.t.Fatalf("item %s: %v %v", id, item, err)
	}
	return *item
}

func TestDeliveryModeFollowsTheSessionAndTheIntent(t *testing.T) {
	r := newQueueRig(t)
	cases := []struct {
		intent queueIntent
		idle   bool
		want   string
	}{
		{intentFollowUp, true, ""}, {intentSteer, true, ""}, {intentPostCompact, true, ""},
		{intentFollowUp, false, "followUp"}, {intentSteer, false, "steer"}, {intentPostCompact, false, "followUp"},
	}
	for _, c := range cases {
		r.host.idle, r.host.sends = c.idle, nil
		item := r.item("go "+string(c.intent), c.intent, queueTarget{Kind: targetGlobal})
		if !r.deliver(item) {
			t.Fatalf("%+v: not sent", c)
		}
		eq(t, r.host.sends, []sentMessage{{"go " + string(c.intent), c.want}}, "deliverAs")
		eq(t, r.get(item.ID).Status, statusDelivering, "delivering until the host starts it")
	}
	// A host that cannot say whether it is idle is not idle.
	r.host.idleHook = func() (bool, error) { return true, errors.New("no host") }
	r.host.sends = nil
	item := r.item("unsure", intentFollowUp, queueTarget{Kind: targetGlobal})
	r.deliver(item)
	eq(t, r.host.sends, []sentMessage{{"unsure", "followUp"}}, "unknown idleness")
}

func TestDeliveryFailureMarksTheItemFailed(t *testing.T) {
	r := newQueueRig(t)
	r.host.idle = true
	r.host.sendHook = func(string, string) error { return errors.New("boom") }
	item := r.item("doomed", intentFollowUp, queueTarget{Kind: targetGlobal})
	eq(t, r.deliver(item), true, "handed over: the host's refusal comes after the call returned")
	got := r.get(item.ID)
	eq(t, got.Status, statusFailed, "status")
	eq(t, got.Error, "boom", "error")
	eq(t, r.host.notices(), []string{"error: Failed to send " + item.ID + ": boom"}, "notice")
	eq(t, r.clock.active(), 0, "no 60 s timer for a message that was not sent")
	sum, _ := r.q.summary()
	eq(t, sum.BlockedCount, 1, "a failed item counts as blocked")
}

func TestDeliveryWaitsOutACompaction(t *testing.T) {
	r := newQueueRig(t)
	r.host.idle = true
	item := r.item("later", intentFollowUp, queueTarget{Kind: targetGlobal})
	r.p.compacting = true
	eq(t, r.deliver(item), false, "not sent while compacting")
	eq(t, len(r.host.sends), 0, "no message")
	eq(t, r.get(item.ID).Status, statusQueued, "stays queued")
}

func TestAnItemTheHostNeverStartsIsQueuedAgainAfterSixtySeconds(t *testing.T) {
	r := newQueueRig(t)
	r.host.idle = true
	item := r.item("stuck", intentFollowUp, queueTarget{Kind: targetGlobal})
	r.deliver(item)
	r.clock.tick(59 * time.Second)
	eq(t, r.get(item.ID).Status, statusDelivering, "59 s")
	r.clock.tick(time.Second)
	got := r.get(item.ID)
	eq(t, got.Status, statusQueued, "60 s")
	eq(t, got.Error, "Queued message did not start", "error")

	// An item the host did start is left alone, and its timer is gone.
	started := r.item("started", intentFollowUp, queueTarget{Kind: targetGlobal})
	r.deliver(started)
	r.emit("before_agent_start", map[string]any{"prompt": "started"})
	eq(t, r.clock.active(), 0, "the acknowledgement cancels the timer")
	r.clock.tick(2 * time.Minute)
	eq(t, r.get(started.ID).Status, statusSent, "sent stays sent")
}

func TestTheHostStartingAMessageMarksTheItemSent(t *testing.T) {
	r := newQueueRig(t)
	r.host.idle = true
	a := r.item("fix   the\ntest", intentFollowUp, queueTarget{Kind: targetGlobal})
	b := r.item("other", intentFollowUp, queueTarget{Kind: targetGlobal})
	r.deliver(a)
	r.deliver(b)
	// message_start of an assistant message, or of a user message with other text, is not an acknowledgement.
	r.emit("message_start", map[string]any{"message": map[string]any{"role": "assistant", "content": "fix the test"}})
	r.emit("message_start", map[string]any{"message": map[string]any{"role": "user", "content": "unrelated"}})
	eq(t, r.get(a.ID).Status, statusDelivering, "not acknowledged")
	// Text blocks are joined and white space collapsed on both sides.
	r.emit("message_start", map[string]any{"message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "image"}, map[string]any{"type": "text", "text": " fix the "}, map[string]any{"type": "text", "text": "test"}}}})
	eq(t, r.get(a.ID).Status, statusSent, "acknowledged")
	eq(t, r.get(b.ID).Status, statusDelivering, "the other one still waits")
	eq(t, r.host.notices(), []string{"info: Sent queued item " + a.ID}, "notice")
}

func TestShutdownQueuesUnstartedDeliveriesAgain(t *testing.T) {
	r := newQueueRig(t)
	r.host.idle = true
	item := r.item("mid-flight", intentFollowUp, queueTarget{Kind: targetGlobal})
	r.deliver(item)
	r.emit("session_shutdown", map[string]any{"reason": "quit"})
	got := r.get(item.ID)
	eq(t, got.Status, statusQueued, "status")
	eq(t, got.Error, "Session ended before queued message started", "error")
	eq(t, r.clock.active(), 0, "timer")
	eq(t, r.q.ctxLive, false, "no context after shutdown")
}

func TestFailedCompactionBlocksOnlyThePostCompactionItems(t *testing.T) {
	r := newQueueRig(t)
	r.emit("session_start", map[string]any{"reason": "startup"})
	a := r.item("one", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	b := r.item("two", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	other := r.item("three", intentFollowUp, queueTarget{Kind: targetCurrentSession})
	r.emit("session_before_compact", nil)
	eq(t, r.p.compacting, true, "compacting")
	r.emit("session_compact_failed", map[string]any{"errorMessage": "model refused"})
	eq(t, r.p.compacting, false, "compaction over")
	eq(t, r.get(a.ID).Status, statusBlocked, "a")
	eq(t, r.get(b.ID).Error, "model refused", "b error")
	eq(t, r.get(other.ID).Status, statusQueued, "a follow-up is not a post-compaction item")
	eq(t, r.host.notices(), []string{"warning: Kept 2 post-compaction items blocked"}, "notice")

	one := r.item("four", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	r.emit("session_compact_failed", map[string]any{})
	eq(t, r.get(one.ID).Error, "Compaction cancelled", "default message")
	eq(t, r.host.notices()[1], "warning: Kept 1 post-compaction item blocked", "singular")
}

func TestPostCompactionDeliversOneItemPerTimerInOrder(t *testing.T) {
	r := newQueueRig(t)
	r.emit("session_start", map[string]any{"reason": "startup"})
	r.host.sendHook = func(text, _ string) error {
		r.emit("before_agent_start", map[string]any{"prompt": text})
		return nil
	}
	a := r.item("first", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	b := r.item("second", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	r.emit("session_before_compact", nil)
	r.host.idle = true
	r.emit("session_compact", nil)
	r.clock.tick(49 * time.Millisecond)
	eq(t, r.host.texts(), []string(nil), "50 ms delay")
	r.clock.tick(time.Millisecond)
	eq(t, r.host.texts(), []string{"first"}, "first")
	r.clock.tick(50 * time.Millisecond)
	eq(t, r.host.texts(), []string{"first", "second"}, "second")
	eq(t, r.statuses()[a.ID], statusSent, "a")
	eq(t, r.statuses()[b.ID], statusSent, "b")
	eq(t, r.clock.active(), 0, "nothing left to wait for")
}

func TestPostCompactionDeliveryIsDroppedWhenTheContextIsGone(t *testing.T) {
	r := newQueueRig(t)
	r.emit("session_start", map[string]any{"reason": "startup"})
	item := r.item("orphan", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	r.host.idle = true
	r.emit("session_compact", nil)
	r.q.ctxLive = false
	r.clock.tick(time.Second)
	eq(t, r.host.texts(), []string(nil), "no send without a context")
	eq(t, r.get(item.ID).Status, statusQueued, "still queued")
	eq(t, r.clock.active(), 0, "no polling")
}

func TestSummaryIsCachedForQuarterOfASecond(t *testing.T) {
	old := clock
	defer func() { clock = old }()
	now := int64(5_000_000)
	clock = func() int64 { return now }
	r := newQueueRig(t)
	r.item("one", intentFollowUp, queueTarget{Kind: targetGlobal})
	sum := func() int {
		s, err := r.q.summary()
		if err != nil {
			t.Fatal(err)
		}
		return s.QueueCount
	}
	eq(t, sum(), 1, "first read")
	r.item("two", intentFollowUp, queueTarget{Kind: targetGlobal})
	now += 249
	eq(t, sum(), 1, "cached at 249 ms")
	now += 1
	eq(t, sum(), 2, "fresh at 250 ms")
	r.item("three", intentFollowUp, queueTarget{Kind: targetGlobal})
	eq(t, sum(), 2, "cached again")
	r.q.requestQueueRender()
	eq(t, sum(), 3, "requestQueueRender drops the cache")
	r.p.compacting = true
	s, _ := r.q.summary()
	eq(t, s.Compacting, true, "the compaction state is part of the key")
	r.host.sessionID = "other"
	r.item("scoped", intentFollowUp, queueTarget{Kind: targetCurrentSession})
}

func TestSegmentSummaryReadsTheInboxOnlyForTheQueueSegment(t *testing.T) {
	r := newQueueRig(t)
	r.item("one", intentFollowUp, queueTarget{Kind: targetGlobal})
	r.p.compacting = true
	eq(t, r.q.segmentSummary([]string{"model", "path"}), queueSummary{Compacting: true}, "no queue segment")
	eq(t, r.q.segmentSummary([]string{"model", "queue"}).QueueCount, 1, "queue segment")
}

func TestQueuePreviewLines(t *testing.T) {
	th := recordTheme{}
	line := func(s queueSummary, width int) []string { return renderQueuePreviewLines(s, width, th) }
	eq(t, line(queueSummary{}, 80), []string{}, "nothing queued")
	eq(t, line(queueSummary{LeadingText: "run   the\ntests", LeadingStatus: statusQueued}, 80), []string{" <dim>queued: run the tests</dim>"}, "queued")
	eq(t, line(queueSummary{LeadingText: "x", LeadingStatus: statusDelivering}, 80), []string{" <dim>sending: x</dim>"}, "delivering")
	eq(t, line(queueSummary{LeadingText: "x", LeadingStatus: statusBlocked}, 80), []string{" <warning>blocked: x</warning>"}, "blocked")
	eq(t, line(queueSummary{LeadingText: "x", LeadingStatus: statusFailed}, 80), []string{" <warning>blocked: x</warning>"}, "failed")
	got := renderQueuePreviewLines(queueSummary{LeadingText: strings.Repeat("a", 100), LeadingStatus: statusQueued}, 20, plainTheme{})
	eq(t, visibleWidth(got[0]) <= 20, true, "fits the width")
	eq(t, strings.Contains(got[0], "…"), true, "ellipsis")
	eq(t, len(line(queueSummary{LeadingText: "x", LeadingStatus: statusQueued}, 1)), 1, "never below one column")
}

func TestQueuePreviewWidgetNeedsALiveContext(t *testing.T) {
	r := newQueueRig(t)
	r.item("hello", intentFollowUp, queueTarget{Kind: targetGlobal})
	eq(t, r.p.renderQueuePreview(80), []string{}, "before session_start")
	r.emit("session_start", map[string]any{"reason": "startup"})
	eq(t, r.p.renderQueuePreview(80), []string{" queued: hello"}, "after session_start")
	r.emit("session_shutdown", map[string]any{"reason": "quit"})
	eq(t, r.p.renderQueuePreview(80), []string{}, "after shutdown")
}

func TestCapturePostCompactPrompt(t *testing.T) {
	r := newQueueRig(t)
	r.q.mu.Lock()
	blank, err := r.q.capturePostCompactPrompt(" \n ")
	r.q.mu.Unlock()
	eq(t, blank == nil && err == nil, true, "a blank prompt is not queued")
	r.q.mu.Lock()
	item, err := r.q.capturePostCompactPrompt("  run it  ")
	r.q.mu.Unlock()
	if err != nil || item == nil {
		t.Fatal(item, err)
	}
	eq(t, item.Text, "run it", "trimmed")
	eq(t, item.Intent, intentPostCompact, "intent")
	eq(t, item.Target, queueTarget{Kind: targetCurrentSession}, "target")
	eq(t, item.Source, queueSource{Cwd: r.root, SessionID: "readiness"}, "source")
	eq(t, r.host.notices(), []string{"info: Queued for after compaction (" + item.ID + ")"}, "notice")
}

func TestPromptTextOfAMessage(t *testing.T) {
	eq(t, queuePromptText(" a \n b "), "a b", "string")
	eq(t, queuePromptText([]any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "text", "text": "b"}, "x", map[string]any{"type": "text", "text": 1.0}}), "a b", "blocks")
	eq(t, queuePromptText(nil), "", "nothing")
	eq(t, queuePromptText(map[string]any{}), "", "object")
}

func TestPostCompactionDeliveryOfAnEarlierSessionIsDropped(t *testing.T) {
	r := newQueueRig(t)
	r.emit("session_start", map[string]any{"reason": "startup"})
	item := r.item("old session", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	r.host.idle = true
	r.q.mu.Lock()
	r.q.postCompaction = &postCompactionDelivery{generation: r.q.sessionGeneration - 1, context: r.q.context()}
	r.q.schedulePostCompactionDelivery()
	r.q.mu.Unlock()
	r.clock.tick(time.Second)
	eq(t, r.host.texts(), []string(nil), "nothing sent for a replaced session")
	eq(t, r.get(item.ID).Status, statusQueued, "still queued")
	eq(t, r.clock.active(), 0, "no polling")
}

func TestPostCompactionDeliveryContinuesAfterAnItemFailedToSend(t *testing.T) {
	r := newQueueRig(t)
	r.emit("session_start", map[string]any{"reason": "startup"})
	r.item("one", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	r.item("two", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	r.host.idle = true
	calls := 0
	r.host.sendHook = func(text, _ string) error {
		calls++
		if calls == 1 {
			return errors.New("refused")
		}
		r.emit("before_agent_start", map[string]any{"prompt": text})
		return nil
	}
	r.emit("session_compact", nil)
	r.clock.tick(time.Second)
	eq(t, r.host.texts(), []string{"one", "two"}, "a failed item does not stop the next one")
	eq(t, r.host.notices()[0], "error: Failed to send "+r.host.notices()[0][len("error: Failed to send "):len("error: Failed to send ")+8]+": refused", "failure notice")
}

func TestSessionStartResetsTheQueueState(t *testing.T) {
	r := newQueueRig(t)
	r.p.compacting = true
	r.item("x", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	r.emit("session_compact", nil)
	eq(t, r.clock.active(), 1, "a delivery is scheduled")
	r.emit("session_start", map[string]any{"reason": "reload"})
	eq(t, r.clock.active(), 0, "a new session drops it")
	eq(t, r.p.compacting, false, "and the compaction flag")
	eq(t, r.q.ctxLive, true, "and has a context")
	generation := r.q.sessionGeneration
	r.emit("session_shutdown", nil)
	eq(t, r.q.sessionGeneration, generation+1, "shutdown retires the session too")
}
