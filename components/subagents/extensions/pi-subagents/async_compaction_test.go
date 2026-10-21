//go:build unix

package pi_subagents_test

import (
	"strings"
	"testing"
	"time"
)

// Twins of compaction-resume.test.ts and the host-level cases of subagent-action-recovery.test.ts. The original drives the
// extension through a fake Pi API and the `subagent:async-started`/`async-complete` bus events; here a background run is
// really started (a fake pig that sleeps) and really ended, and the widgets are the ones the host receives.

func compactionResumes(h *Host) int {
	n := 0
	for _, c := range h.CallsTo("sendMessage") {
		m, _ := c.Args["message"].(map[string]any)
		if m["customType"] == "subagent-compaction-resume" {
			n++
		}
	}
	return n
}

// asyncWidgetCleared counts how often the subagent-async widget was removed (a ui.setWidget call without lines).
func asyncWidgetCleared(h *Host) int {
	n := 0
	for _, c := range h.CallsTo("ui.setWidget") {
		if c.Args["key"] == "subagent-async" && c.Args["content"] == nil {
			n++
		}
	}
	return n
}

func asyncWidgetDrawn(h *Host) int {
	n := 0
	for _, w := range h.Widgets() {
		if w.Key == "subagent-async" {
			n++
		}
	}
	return n
}

func completionNotices(h *Host) int {
	n := 0
	for _, c := range h.CallsTo("sendMessage") {
		if m, _ := c.Args["message"].(map[string]any); m["customType"] == "subagent-notify" {
			n++
		}
	}
	return n
}

func fireAll(h *Host, event, reason string) {
	h.FireAll(event, map[string]any{"reason": reason})
}

func TestCompactionResume(t *testing.T) {
	tw(t, "compaction-resume", "only re-drives manual compaction while async work is active", func(t *testing.T) {
		e := newAsyncEnv(t)
		t.Setenv("FAKE_PIG_SLEEP", "60")
		h := e.host(t)
		h.FireAll("session_start", map[string]any{"reason": "startup"})
		id, _ := launch(t, h, map[string]any{})
		eventually(t, "the widget", func() bool { return asyncWidgetDrawn(h) > 0 })

		// a threshold compaction clears the widget, keeps it cleared while runs start, and brings it back when the agent starts
		h.ClearWidgets()
		clearedBefore := asyncWidgetCleared(h)
		fireAll(h, "session_before_compact", "threshold")
		eq(t, asyncWidgetCleared(h), clearedBefore+1, "cleared by a threshold compaction")
		id2, _ := launch(t, h, map[string]any{})
		time.Sleep(400 * time.Millisecond)
		eq(t, asyncWidgetDrawn(h), 0, "widgets repainted during compaction")
		fireAll(h, "session_compact", "threshold")
		eq(t, asyncWidgetDrawn(h), 0, "widgets restored inside the compaction hook")
		h.FireAll("agent_start", nil)
		eventually(t, "the widget restored when the agent starts", func() bool { return asyncWidgetDrawn(h) > 0 })
		eq(t, compactionResumes(h), 0, "resumed after threshold compaction")

		// a manual compaction leaves the widget alone and resumes the parent while work is active
		h.ClearWidgets()
		clearedBefore = asyncWidgetCleared(h)
		fireAll(h, "session_before_compact", "manual")
		eq(t, asyncWidgetCleared(h), clearedBefore, "manual compaction changed widget state")
		fireAll(h, "session_compact", "manual")
		eq(t, compactionResumes(h), 1, "manual compaction resumes")
		var sent map[string]any
		for _, c := range h.CallsTo("sendMessage") {
			if m, _ := c.Args["message"].(map[string]any); m["customType"] == "subagent-compaction-resume" {
				sent = c.Args
			}
		}
		opts, _ := sent["options"].(map[string]any)
		eq(t, opts["triggerTurn"], true, "wakes the session")

		// with no active work a manual compaction resumes nothing
		for _, runID := range []string{id, id2} {
			if _, failure := h.Tool("subagent", map[string]any{"action": "stop", "id": runID}); failure != "" {
				t.Fatalf("stop %s: %s", runID, failure)
			}
		}
		eventually(t, "both runs stopped", func() bool { return completionNotices(h) >= 2 })
		before := compactionResumes(h)
		fireAll(h, "session_before_compact", "manual")
		fireAll(h, "session_compact", "manual")
		h.FireAll("agent_settled", nil)
		eq(t, compactionResumes(h), before, "resumed without active work")

		// an overflow compaction does not resume, and the widget recovers when the agent settles after a failed compaction
		launch(t, h, map[string]any{})
		fireAll(h, "session_before_compact", "overflow")
		h.ClearWidgets()
		fireAll(h, "session_compact", "overflow")
		eq(t, compactionResumes(h), before, "resumed after overflow compaction")
		h.FireAll("agent_start", nil)
		fireAll(h, "session_before_compact", "overflow")
		h.ClearWidgets()
		h.FireAll("agent_settled", nil)
		eventually(t, "the widget recovered after the compaction failure", func() bool { return asyncWidgetDrawn(h) > 0 })
		h.FireAll("session_shutdown", nil)
	})

	tw(t, "compaction-resume", "ignores stale cached UI context during compaction lifecycle callbacks", func(t *testing.T) {
		// A session context that was shut down (or replaced) is the Go counterpart of a stale cached ctx: the runtime holds no
		// context for it, so a manual compaction after it resumes nothing.
		e := newAsyncEnv(t)
		t.Setenv("FAKE_PIG_SLEEP", "60")
		h := e.host(t)
		h.FireAll("session_start", map[string]any{"reason": "startup"})
		launch(t, h, map[string]any{})
		h.FireAll("session_shutdown", nil)
		fireAll(h, "session_compact", "manual")
		eq(t, compactionResumes(h), 0, "resumed with a stale session context")
		fireAll(h, "session_before_compact", "threshold")
	})
}

func TestSubagentActionRecoveryThroughTheTool(t *testing.T) {
	e := newAsyncEnv(t)
	h := e.host(t)
	tw(t, "subagent-action-recovery", "returns a concise recovery error for an invalid action", func(t *testing.T) {
		_, failure := h.Tool("subagent", map[string]any{"action": "statsu"})
		if !strings.HasPrefix(failure, "Unknown action: statsu. Did you mean status? Use subagent({ action: \"status\" })") {
			t.Errorf("failure: %q", failure)
		}
	})
	tw(t, "subagent-action-recovery", "routes invalid schedule actions through common recovery", func(t *testing.T) {
		_, failure := h.Tool("subagent", map[string]any{"action": "schedule.lsit"})
		if !strings.HasPrefix(failure, "Unknown action: schedule.lsit. Did you mean schedule.list?") {
			t.Errorf("failure: %q", failure)
		}
	})
}

// Ending the session removes the async widget (the original's session_shutdown handler clears it).
func TestSessionShutdownClearsTheAsyncWidget(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "60")
	h := e.host(t)
	h.FireAll("session_start", map[string]any{"reason": "startup"})
	launch(t, h, map[string]any{})
	eventually(t, "the widget", func() bool { return asyncWidgetDrawn(h) > 0 })
	before := asyncWidgetCleared(h)
	h.FireAll("session_shutdown", nil)
	eq(t, asyncWidgetCleared(h), before+1, "cleared when the session ends")
}
