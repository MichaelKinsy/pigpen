package tintinweb_subagents

import (
	"testing"
	"time"
)

func TestAlreadyConsumedResultIsNotAnnounced(t *testing.T) {
	// Consumed before the agent settles, so no timer is cancelled by the read: the settle itself has to notice.
	r := startRig(t)
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "general-purpose"}))
	c := r.fleet.wait(t, 1)[0]
	rec := r.app.mgr.get(id)
	r.app.mgr.mu.Lock()
	rec.Consumed = true
	r.app.mgr.mu.Unlock()
	c.finish("done")
	<-rec.done
	time.Sleep(nudgeHold + 300*time.Millisecond)
	eq(t, len(r.messages()), 0)
}
