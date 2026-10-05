package acp

// Twins of the usage_update tests in test/component/session-events.test.ts, and of
// test/component/session-queue-cancel.test.ts, session-slash-commands.test.ts.

import (
	"errors"
	"math"
	"testing"
	"time"
)

func usageUpdates(c *fakeConn) []Update {
	var out []Update
	for _, u := range c.ofKind("usage_update") {
		out = append(out, u.Update)
	}
	return out
}

func TestSessionUsage(t *testing.T) {
	tw(t, "component/session-events", "PiAcpSession: emits usage_update from contextUsage before resolving prompt on agent_settled", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		proc.sessionStats = SessionStats{"tokens": map[string]any{"total": 999999}, "contextUsage": map[string]any{"tokens": 12345, "contextWindow": 200000}}
		s := newTestSession(cwdNow(t), proc, conn)
		started := make(chan struct{})
		release := make(chan struct{})
		conn.sessionUpdateHook = func(u Update) {
			if u["sessionUpdate"] == "usage_update" {
				close(started)
				<-release
			}
		}
		p := s.Prompt("hello", nil)
		proc.emit(Event{"type": "agent_start"})
		proc.emit(Event{"type": "agent_end"})
		proc.emit(Event{"type": "agent_settled"})
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("usage_update delivery never started")
		}
		select {
		case <-p:
			t.Fatal("prompt resolved before usage_update landed")
		case <-time.After(50 * time.Millisecond):
		}
		close(release)
		if r := wait(t, p); r.Reason != StopEndTurn {
			t.Fatalf("reason = %v", r)
		}
		if proc.statsCount() != 1 {
			t.Errorf("get_session_stats called %d times", proc.statsCount())
		}
		jsonEqual(t, usageUpdates(conn), []Update{{"sessionUpdate": "usage_update", "used": 12345, "size": 200000}})
	})

	tw(t, "component/session-events", "PiAcpSession: skips usage_update when contextUsage tokens are null", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		proc.sessionStats = SessionStats{"tokens": map[string]any{"total": 4000}, "contextUsage": map[string]any{"tokens": nil, "contextWindow": 200000}}
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("hello", nil)
		proc.emit(Event{"type": "agent_settled"})
		if r := wait(t, p); r.Reason != StopEndTurn {
			t.Fatalf("reason = %v", r)
		}
		if len(usageUpdates(conn)) != 0 {
			t.Error("usage_update emitted for null tokens")
		}
	})

	tw(t, "component/session-events", "PiAcpSession: skips usage_update for invalid contextUsage values", func(t *testing.T) {
		invalid := []struct {
			name  string
			usage any
		}{
			{"missing contextUsage", nil},
			{"negative tokens", map[string]any{"tokens": -1, "contextWindow": 100}},
			{"fractional tokens", map[string]any{"tokens": 1.5, "contextWindow": 100}},
			{"NaN tokens", map[string]any{"tokens": math.NaN(), "contextWindow": 100}},
			{"zero contextWindow", map[string]any{"tokens": 10, "contextWindow": 0}},
			{"negative contextWindow", map[string]any{"tokens": 10, "contextWindow": -1}},
			{"fractional contextWindow", map[string]any{"tokens": 10, "contextWindow": 100.5}},
			{"null contextWindow", map[string]any{"tokens": 10, "contextWindow": nil}},
			{"infinite contextWindow", map[string]any{"tokens": 10, "contextWindow": math.Inf(1)}},
		}
		for _, tc := range invalid {
			conn, proc := newFakeConn(), newFakeProc()
			stats := SessionStats{}
			if tc.usage != nil {
				stats["contextUsage"] = tc.usage
			}
			proc.sessionStats = stats
			s := newTestSession(cwdNow(t), proc, conn)
			p := s.Prompt("hello", nil)
			proc.emit(Event{"type": "agent_settled"})
			if r := wait(t, p); r.Reason != StopEndTurn {
				t.Fatalf("%s: reason = %v", tc.name, r)
			}
			if len(usageUpdates(conn)) != 0 {
				t.Errorf("%s: usage_update emitted", tc.name)
			}
		}
	})

	tw(t, "component/session-events", "PiAcpSession: get_session_stats rejection does not break the prompt", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		proc.sessionStatsError = errors.New("pi get_session_stats failed: unsupported")
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("hello", nil)
		proc.emit(Event{"type": "agent_settled"})
		if r := wait(t, p); r.Reason != StopEndTurn {
			t.Fatalf("reason = %v", r)
		}
		if proc.statsCount() != 1 || len(usageUpdates(conn)) != 0 {
			t.Errorf("stats=%d updates=%v", proc.statsCount(), usageUpdates(conn))
		}
	})

	tw(t, "component/session-events", "PiAcpSession: get_session_stats timeout does not block the prompt", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		// The timeout lives in Process.Request; the session sees it as an error.
		proc.sessionStatsError = errors.New("pi get_session_stats timed out after 1000ms")
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("hello", nil)
		proc.emit(Event{"type": "agent_settled"})
		if r := wait(t, p); r.Reason != StopEndTurn {
			t.Fatalf("reason = %v", r)
		}
		if proc.statsCount() != 1 || len(usageUpdates(conn)) != 0 {
			t.Errorf("stats=%d updates=%v", proc.statsCount(), usageUpdates(conn))
		}
	})

	tw(t, "component/session-events", "PiAcpSession: cancelled turn still reports cancelled after usage publish", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		proc.sessionStats = SessionStats{"contextUsage": map[string]any{"tokens": 42, "contextWindow": 100}}
		s := newTestSession(cwdNow(t), proc, conn)
		p := s.Prompt("hello", nil)
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
		proc.emit(Event{"type": "agent_settled"})
		if r := wait(t, p); r.Reason != StopCancelled {
			t.Fatalf("reason = %v", r)
		}
		jsonEqual(t, usageUpdates(conn), []Update{{"sessionUpdate": "usage_update", "used": 42, "size": 100}})
	})
}

// test/component/session-queue-cancel.test.ts
func TestSessionQueueCancel(t *testing.T) {
	tw(t, "component/session-queue-cancel", "PiAcpSession: cancel clears queued prompts", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn)
		first := s.Prompt("one", nil)
		second := s.Prompt("two", nil)
		third := s.Prompt("three", nil)
		eventually(t, "the first prompt", func() bool { return len(proc.promptList()) == 1 })
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
		if proc.aborts() != 1 {
			t.Errorf("abort count = %d", proc.aborts())
		}
		if r := wait(t, second); r.Reason != StopCancelled {
			t.Errorf("second = %v", r)
		}
		if r := wait(t, third); r.Reason != StopCancelled {
			t.Errorf("third = %v", r)
		}
		settledTurn(proc)
		if r := wait(t, first); r.Reason != StopCancelled {
			t.Errorf("first = %v", r)
		}
		// The queue was cleared, so no further prompt starts.
		time.Sleep(20 * time.Millisecond)
		if n := len(promptsOf(t, proc, 1)); n != 1 {
			t.Errorf("%d prompts reached pi", n)
		}
	})
}

// test/component/session-slash-commands.test.ts
func TestSessionSlashCommands(t *testing.T) {
	tw(t, "component/session-slash-commands", "PiAcpSession: expands /command before sending to pi", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		s := newTestSession(cwdNow(t), proc, conn, FileSlashCommand{Name: "hello", Description: "(user)", Content: "Expanded $1", Source: "(user)"})
		p := s.Prompt("/hello world", nil)
		settledTurn(proc)
		if r := wait(t, p); r.Reason != StopEndTurn {
			t.Fatalf("reason = %v", r)
		}
		if got := promptsOf(t, proc, 1); len(got) != 1 || got[0].Message != "Expanded world" {
			t.Fatalf("prompts = %v", got)
		}
	})
}
