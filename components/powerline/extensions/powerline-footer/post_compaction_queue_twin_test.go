package powerline_footer

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// Twins of port/oracle/tests/post-compaction-queue.test.ts. The upstream harness drives the extension through a fake pi
// and mocked timers; this one drives the queue the same way through the scripted host and a hand-driven clock.

// readinessHarness is readinessHarness of the upstream test: an extension with a started session and one queued post-compaction
// item; the host sends only while idle, without options, and acknowledges the message at before_agent_start.
type readinessHarness struct {
	*queueRig
	item queueItem
	idle bool
}

func newReadinessHarness(t *testing.T) *readinessHarness {
	r := newQueueRig(t)
	h := &readinessHarness{queueRig: r}
	r.host.idleHook = func() (bool, error) { return h.idle, nil }
	r.host.sendHook = func(text, deliverAs string) error {
		if !h.idle {
			t.Fatal("never send while busy")
		}
		if deliverAs != "" {
			t.Fatalf("never leave a late Pi follow-up: deliverAs = %q", deliverAs)
		}
		r.emit("before_agent_start", map[string]any{"prompt": text})
		h.idle = false
		return nil
	}
	r.emit("session_start", map[string]any{"reason": "new"})
	h.item = r.item("after retry", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	t.Cleanup(func() { r.emit("session_shutdown", map[string]any{"reason": "quit"}) })
	return h
}

func (h *readinessHarness) setIdle() { h.idle = true }
func (h *readinessHarness) tick()    { h.clock.tick(time.Second) }

func TestPostCompactionQueueTwins(t *testing.T) {
	tw(t, "post-compaction-queue", "successful retry keeps readiness through busy settlement without inbox I/O", func(t *testing.T) {
		h := newReadinessHarness(t)
		second, err := h.store.add(createQueueItemInput{Text: "last queued prompt", Intent: h.item.Intent, Target: h.item.Target, Source: h.item.Source, Now: new(h.item.CreatedAt + 1)})
		if err != nil {
			t.Fatal(err)
		}
		h.emit("session_before_compact", nil)
		h.emit("session_compact", map[string]any{"willRetry": true})
		// Reject filesystem access, rather than counting private queue/cache calls.
		reject := func(path string) {
			if strings.HasSuffix(path, "inbox.jsonl") {
				t.Errorf("busy readiness must not inspect the inbox: %s", path)
			}
		}
		read, stat := queueReadFile, queueStatFile
		queueReadFile = func(path string) ([]byte, error) { reject(path); return read(path) }
		queueStatFile = func(path string) (os.FileInfo, error) { reject(path); return stat(path) }
		func() {
			defer func() { queueReadFile, queueStatFile = read, stat }()
			h.tick()
			h.emit("agent_settled", nil)
			h.tick()
			eq(t, h.host.texts(), []string(nil), "no send while busy")
		}()
		eq(t, h.status(h.item.ID), statusQueued, "still queued")
		h.setIdle()
		h.tick()
		h.tick()
		eq(t, h.host.texts(), []string{"after retry"}, "first send")
		eq(t, h.status(h.item.ID), statusSent, "first sent")
		eq(t, h.status(second.ID), statusQueued, "second queued")
		h.setIdle()
		h.tick()
		eq(t, h.host.texts(), []string{"after retry", "last queued prompt"}, "second send")
		eq(t, h.status(second.ID), statusSent, "second sent")
		h.host.idleHook = func() (bool, error) { t.Fatal("no readiness polling after the last queued item"); return false, nil }
		h.emit("agent_settled", nil)
		h.tick()
	})
	tw(t, "post-compaction-queue", "new compaction cancellation and session replacement retire pending readiness", func(t *testing.T) {
		h := newReadinessHarness(t)
		h.emit("session_before_compact", nil)
		h.emit("session_compact", map[string]any{"willRetry": false})
		h.emit("session_before_compact", nil)
		h.emit("session_compact_failed", map[string]any{"aborted": true})
		h.emit("session_compact_failed", map[string]any{"aborted": true})
		h.setIdle()
		h.tick()
		eq(t, h.host.texts(), []string(nil), "nothing sent")
		eq(t, h.status(h.item.ID), statusBlocked, "blocked by the failed compaction")
		if _, err := h.store.update(h.item.ID, queueUpdates{Status: statusQueued}); err != nil {
			t.Fatal(err)
		}
		h.emit("session_compact", map[string]any{"willRetry": false})
		h.emit("session_shutdown", map[string]any{"reason": "reload"})
		h.emit("session_start", map[string]any{"reason": "reload"})
		h.emit("agent_settled", nil)
		h.tick()
		eq(t, h.host.texts(), []string(nil), "nothing sent after the session was replaced")
		eq(t, h.status(h.item.ID), statusQueued, "still queued")
	})
	tskip(t, "post-compaction-queue", "reload -> editor /compact -> captured prompt starts after delayed manual completion without agent_settled",
		"SDK: setEditorComponent. The case types /compact and a prompt into Pi's real CustomEditor (index.ts 3342-3384) inside a real Pi SDK session (PI_COMPACTION_SDK, skipped upstream without it); an extension cannot install or wrap the editor. The delivery half it ends with is covered by the two twins above and by TestPostCompactionDeliveryAfterAManualCompaction")
}

// The delivery after a manual compaction without agent_settled, as the skipped upstream case ends: the compaction completes
// (session_compact), the 50 ms timer sends the queued item once the session is idle, and readiness retires afterwards.
func TestPostCompactionDeliveryAfterAManualCompaction(t *testing.T) {
	h := newReadinessHarness(t)
	h.emit("session_before_compact", nil)
	h.setIdle()
	h.emit("session_compact", map[string]any{})
	eq(t, h.host.texts(), []string(nil), "the timer has not run")
	h.clock.tick(50 * time.Millisecond)
	eq(t, h.host.texts(), []string{"after retry"}, "sent without another user action")
	eq(t, h.status(h.item.ID), statusSent, "acknowledged at before_agent_start")
	eq(t, h.clock.active(), 0, "readiness retires after the acknowledgement")
	if slices.Contains(h.host.notices(), "info: Sent queued item "+h.item.ID) == false {
		t.Errorf("notices = %q, want the sent notice", h.host.notices())
	}
}
