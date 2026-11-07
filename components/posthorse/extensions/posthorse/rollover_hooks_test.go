package posthorse

// Layer-1 tests of the rollover lane's hooks beyond the upstream twins: the explicit reset and its
// deferral, the threshold fallback at turn_end, Pi's automatic compaction hook (drafts, the boundary
// sentinel, cancellation, failure reports) and the chronological retained tail of the `context` hook.
// Their oracle is rollover.ts and the real-host suites of the original (test/official-host.ts).

import (
	"io"
	"os"
	"strings"
	"testing"
)

func roToolResult() rec { return rec{"toolName": "new_context", "toolCallId": "id"} }

// roTurnEnd is a turn_end event with the given extras over a completed assistant turn.
func roTurnEnd(extra rec) rec {
	event := rec{
		"message":     rec{"role": "assistant", "stopReason": "toolUse", "content": []any{}},
		"toolResults": []any{},
		"continue":    true,
		"entries":     []any{},
		"context":     rec{"pendingMessages": []any{}, "contextEntries": []any{}},
	}
	for k, v := range extra {
		event[k] = v
	}
	return event
}

func TestRolloverExplicitReset(t *testing.T) {
	existing := rec{"type": "custom_message", "customType": "other", "display": false, "content": "x"}

	t.Run("a successful new_context drafts an explicit compaction and continues", func(t *testing.T) {
		f := newFixture(t)
		f.Run("new_context", rec{"handoff": "continue here"})
		got := f.TurnEndEvent(roTurnEnd(rec{"toolResults": []any{roToolResult()}, "continue": false, "entries": []any{existing}}))
		entries, _ := got["entries"].([]any)
		if len(entries) != 2 {
			t.Fatalf("want the existing entry and one compaction, got %v", got)
		}
		roSameJSON(t, entries[0], existing, "earlier drafts stay in front")
		roSameJSON(t, entries[1], rec{
			"type": "compaction", "summary": "continue here", "firstKeptEntryId": nil,
			"details": rec{"posthorse": float64(1), "reason": "explicit"},
		}, "the explicit compaction")
		roEqual(t, got["continue"], true, "an explicit reset continues the run")
	})

	t.Run("no handoff becomes the empty handoff text", func(t *testing.T) {
		f := newFixture(t)
		f.Run("new_context", rec{})
		got := f.TurnEnd(roToolResult())
		entries, _ := got["entries"].([]any)
		if len(entries) != 1 || roRec(entries[0])["summary"] != emptyHandoff {
			t.Fatalf("want the empty handoff, got %v", got)
		}
	})

	t.Run("an aborted outcome leaves the boundary alone", func(t *testing.T) {
		f := newFixture(t)
		f.Run("new_context", rec{"handoff": "continue here"})
		if got := f.TurnEndEvent(roTurnEnd(rec{"toolResults": []any{roToolResult()}, "outcome": "aborted"})); got != nil {
			t.Errorf("an aborted turn must not reset, got %v", got)
		}
	})

	t.Run("a handoff that no longer fits is deferred with a visible message", func(t *testing.T) {
		f := newFixture(t)
		f.Run("new_context", rec{"handoff": "continue here"})
		late := rec{"role": "user", "content": strings.Repeat("q", 400_000), "timestamp": float64(1)}
		got := f.TurnEndEvent(roTurnEnd(rec{
			"toolResults": []any{roToolResult()},
			"entries":     []any{existing},
			"context":     rec{"pendingMessages": []any{late}, "contextEntries": []any{}},
		}))
		entries, _ := got["entries"].([]any)
		if len(entries) != 2 {
			t.Fatalf("want the existing entry and the deferral, got %v", got)
		}
		roSameJSON(t, entries[1], rec{
			"type": "custom_message", "customType": "posthorse-reset-deferred", "display": true,
			"content": "Posthorse reset deferred: queued messages leave too little room for the requested handoff. The current context is unchanged; process those messages, then request a shorter handoff.",
		}, "the deferral")
		if _, ok := got["continue"]; ok {
			t.Errorf("a deferral must not set continue, got %v", got)
		}
	})

	t.Run("a failed or aborted assistant turn consumes the request without a boundary", func(t *testing.T) {
		f := newFixture(t)
		f.Run("new_context", rec{"handoff": "continue here"})
		if got := f.TurnEndEvent(roTurnEnd(rec{"message": rec{"role": "assistant", "stopReason": "error"}, "toolResults": []any{roToolResult()}})); got != nil {
			t.Errorf("an errored turn must not reset, got %v", got)
		}
		// The errored turn took the request: the same results one turn later reset nothing.
		if got := f.TurnEnd(roToolResult()); got != nil {
			t.Errorf("the request must have been consumed, got %v", got)
		}
	})
}

// roFreshBoundary is the preview of a window that just rolled over: the checkpoint, then these entries.
func roFreshBoundary(after ...rec) []any {
	entries := []any{rec{
		"sourceEntry": rec{"type": "compaction", "id": "boundary"},
		"messages":    []any{rec{"role": "compactionSummary", "summary": "Automatic context rollover recovery record.\nold", "timestamp": float64(1)}},
	}}
	for _, e := range after {
		entries = append(entries, e)
	}
	return entries
}

func roFallbackEvent(contextEntries []any, extra rec) rec {
	event := roTurnEnd(rec{
		"outcome": "completed",
		"message": rec{"role": "assistant", "stopReason": "stop", "content": []any{}},
		"context": rec{"pendingMessages": []any{}, "contextEntries": contextEntries},
	})
	for k, v := range extra {
		event[k] = v
	}
	return event
}

func TestRolloverThresholdFallback(t *testing.T) {
	const window, reserve = 100_000, 16_384
	rolloverAt := window - reserve + 1
	session := func() *fakeSession {
		return fxIndexedSession(fxMessageEntry("u1", fxUser("keep going on the fix")))
	}

	t.Run("a fresh window already over the line gets a threshold record", func(t *testing.T) {
		f := newFixture(t, withUsage(window, rolloverAt), withSession(session()))
		got := f.TurnEndEvent(roFallbackEvent(roFreshBoundary(), nil))
		entries, _ := got["entries"].([]any)
		if len(entries) != 1 {
			t.Fatalf("want one draft, got %v", got)
		}
		draft := roRec(entries[0])
		if draft["type"] != "compaction" || !strings.HasPrefix(draft["summary"].(string), autoHandoffPrefix) {
			t.Errorf("draft %v", draft)
		}
		if value, present := draft["firstKeptEntryId"]; !present || value != nil {
			t.Errorf("no retained tail: firstKeptEntryId must be null, got %v (%v)", value, present)
		}
		roSameJSON(t, draft["details"], rec{"posthorse": float64(1), "reason": "threshold"}, "details")
		roEqual(t, got["continue"], true, "the event's continue is carried")
	})

	t.Run("a retained tail names the first kept entry", func(t *testing.T) {
		call := rec{"type": "toolCall", "id": "c1", "name": "bash", "arguments": rec{"command": "ls"}}
		assistant := rec{"role": "assistant", "content": []any{call}, "stopReason": "toolUse", "timestamp": float64(1)}
		result := rec{"role": "toolResult", "toolCallId": "c1", "toolName": "bash", "content": []any{fxText("ok")}, "isError": false, "timestamp": float64(1)}
		f := newFixture(t, withUsage(window, rolloverAt+5), withSession(session()))
		got := f.TurnEndEvent(roFallbackEvent(roFreshBoundary(
			rec{"sourceEntry": rec{"type": "message", "id": "a1"}, "messages": []any{assistant}},
			rec{"sourceEntry": rec{"type": "message", "id": "r1"}, "messages": []any{result}},
		), nil))
		entries, _ := got["entries"].([]any)
		if len(entries) != 1 || roRec(entries[0])["firstKeptEntryId"] != "a1" {
			t.Fatalf("want the tail from a1 kept, got %v", got)
		}
	})

	delegated := map[string]func(t *testing.T) (*fixture, rec){
		"no outcome": func(t *testing.T) (*fixture, rec) {
			e := roFallbackEvent(roFreshBoundary(), nil)
			delete(e, "outcome")
			return newFixture(t, withUsage(window, rolloverAt)), e
		},
		"an aborted outcome": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsage(window, rolloverAt)), roFallbackEvent(roFreshBoundary(), rec{"outcome": "aborted"})
		},
		"a stop at the length limit": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsage(window, rolloverAt)), roFallbackEvent(roFreshBoundary(), rec{"message": rec{"role": "assistant", "stopReason": "length", "content": []any{}}})
		},
		"an earlier boundary draft": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsage(window, rolloverAt)), roFallbackEvent(roFreshBoundary(), rec{"entries": []any{rec{"type": "compaction", "summary": "x"}}})
		},
		"a context edit draft": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsage(window, rolloverAt)), roFallbackEvent(roFreshBoundary(), rec{"entries": []any{rec{"type": "context_edit"}}})
		},
		"usage below the line": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsage(window, rolloverAt-1)), roFallbackEvent(roFreshBoundary(), nil)
		},
		"unknown usage": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsageUnknown(window)), roFallbackEvent(roFreshBoundary(), nil)
		},
		"disabled compaction": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsage(window, rolloverAt), withEnabled(false)), roFallbackEvent(roFreshBoundary(), nil)
		},
		"an unsupported budget": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsage(5_000, 4_500), withReserve(4_000)), roFallbackEvent(roFreshBoundary(), nil)
		},
		"owner input after the boundary": func(t *testing.T) (*fixture, rec) {
			user := rec{"role": "user", "content": "next", "timestamp": float64(1)}
			return newFixture(t, withUsage(window, rolloverAt)), roFallbackEvent(roFreshBoundary(rec{"sourceEntry": rec{"type": "message", "id": "u2"}, "messages": []any{user}}), nil)
		},
		"no boundary in the projection": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsage(window, rolloverAt)), roFallbackEvent([]any{}, nil)
		},
		"no room for a record": func(t *testing.T) (*fixture, rec) {
			return newFixture(t, withUsage(window, rolloverAt), withSystemPrompt(strings.Repeat("x", 400_000))), roFallbackEvent(roFreshBoundary(), nil)
		},
	}
	for name, setup := range delegated {
		t.Run("delegates on "+name, func(t *testing.T) {
			f, event := setup(t)
			if got := f.TurnEndEvent(event); got != nil {
				t.Errorf("want Pi to own the turn, got %v", got)
			}
		})
	}
}

// roCompactEvent is a session_before_compact event of the given reason.
func roCompactEvent(reason string, keepRecent int, firstKept string, branch ...rec) rec {
	entries := make([]any, len(branch))
	for i, e := range branch {
		entries[i] = e
	}
	preparation := rec{
		"settings":     rec{"enabled": true, "reserveTokens": float64(16_384), "keepRecentTokens": float64(keepRecent)},
		"tokensBefore": float64(123),
	}
	if firstKept != "" {
		preparation["firstKeptEntryId"] = firstKept
	}
	return rec{"reason": reason, "branchEntries": entries, "preparation": preparation}
}

// roStderr captures what the extension writes to os.Stderr until the returned function is called.
func roStderr(t *testing.T) func() string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = writer
	done := make(chan string)
	go func() { text, _ := io.ReadAll(reader); done <- string(text) }()
	var once bool
	finish := func() string {
		if once {
			return ""
		}
		once = true
		os.Stderr = saved
		_ = writer.Close()
		return <-done
	}
	t.Cleanup(func() { finish() })
	return finish
}

func TestRolloverAutomaticCompaction(t *testing.T) {
	branch := []rec{fxMessageEntry("u1", fxUser("keep working on the fix"))}

	t.Run("a manual compaction is left to Pi", func(t *testing.T) {
		f := newFixture(t)
		if got := f.Fire("session_before_compact", rec{"reason": "manual"}); got != nil {
			t.Errorf("manual /compact must be Pi's, got %v", got)
		}
	})

	t.Run("a prepared first kept entry rides the draft", func(t *testing.T) {
		f := newFixture(t, withProjection(branch...))
		got := f.Fire("session_before_compact", roCompactEvent("threshold", 20_000, "u1", branch...))
		compaction := roRec(got["compaction"])
		if !strings.HasPrefix(compaction["summary"].(string), autoHandoffPrefix) {
			t.Errorf("summary %v", compaction["summary"])
		}
		roEqual(t, compaction["firstKeptEntryId"], "u1", "the retained tail starts at the prepared entry")
		roEqual(t, compaction["tokensBefore"], float64(123), "tokensBefore comes from the preparation")
		roSameJSON(t, compaction["details"], rec{"posthorse": float64(1), "reason": "threshold"}, "details")
		roEqual(t, len(f.Appended()), 0, "no sentinel entry is needed")
	})

	t.Run("the reason is carried to the details", func(t *testing.T) {
		f := newFixture(t, withProjection(branch...))
		got := f.Fire("session_before_compact", roCompactEvent("overflow", 20_000, "u1", branch...))
		roSameJSON(t, roRec(roRec(got["compaction"])["details"]), rec{"posthorse": float64(1), "reason": "overflow"}, "details")
	})

	t.Run("without a retained tail a boundary sentinel supplies the first kept entry", func(t *testing.T) {
		session := fxMemorySession("")
		f := newFixture(t, withSession(session), withAppendToSession(), withProjection(branch...))
		got := f.Fire("session_before_compact", roCompactEvent("threshold", 0, "", branch...))
		appended := f.Appended()
		if len(appended) != 1 || appended[0]["customType"] != "posthorse-boundary" {
			t.Fatalf("want one posthorse-boundary entry, got %v", appended)
		}
		roSameJSON(t, appended[0]["data"], rec{}, "the sentinel carries no data")
		leaf := session.getLeafID()
		if leaf == nil {
			t.Fatal("the sentinel must have been persisted")
		}
		roEqual(t, roRec(got["compaction"])["firstKeptEntryId"], *leaf, "the sentinel is the first kept entry")
	})

	t.Run("a boundary Pi did not persist cancels and is reported", func(t *testing.T) {
		f := newFixture(t, withSession(fxMemorySession("")), withProjection(branch...))
		got := f.Fire("session_before_compact", roCompactEvent("threshold", 0, "", branch...))
		roEqual(t, got["cancel"], true, "the hook cancels rather than letting Pi summarize")
		notes := f.CallsTo("ui.notify")
		if len(notes) != 1 || notes[0].Args["level"] != "error" || notes[0].Args["message"] != "Posthorse automatic rollover cancelled: Pi did not persist the context boundary." {
			t.Errorf("want one error notification, got %v", notes)
		}
	})

	t.Run("a headless failure goes to stderr", func(t *testing.T) {
		finish := roStderr(t)
		f := newFixture(t, withHostMode("print"), withProjection(branch...), withSystemPrompt(strings.Repeat("x", 400_000)))
		got := f.Fire("session_before_compact", roCompactEvent("threshold", 20_000, "u1", branch...))
		roEqual(t, got["cancel"], true, "too little capacity cancels")
		roEqual(t, len(f.CallsTo("ui.notify")), 0, "no UI to notify")
		stderr := finish()
		if want := "Posthorse automatic rollover cancelled: Too little fresh context capacity for an automatic recovery record.\n"; !strings.Contains(stderr, want) {
			t.Errorf("stderr %q does not hold %q", stderr, want)
		}
	})

	t.Run("an unsupported budget is left to Pi", func(t *testing.T) {
		f := newFixture(t, withUsage(5_000, 4_500), withReserve(4_000), withProjection(branch...))
		event := roCompactEvent("threshold", 20_000, "u1", branch...)
		event["preparation"].(rec)["settings"].(rec)["reserveTokens"] = float64(4_000)
		if got := f.Fire("session_before_compact", event); got != nil {
			t.Errorf("an unsupported budget must delegate, got %v", got)
		}
	})

	t.Run("a compaction cancelled while the record is built cancels the hook quietly", func(t *testing.T) {
		gate, reached := make(chan struct{}), make(chan struct{})
		f := newFixture(t, withProjection(branch...), withHostAnswer("getContextUsage", func(map[string]any) (any, string) {
			close(reached)
			<-gate
			return rec{"tokens": 1000, "contextWindow": 100_000, "percent": float64(1)}, ""
		}))
		host := f.H()
		host.mu.Lock()
		id := host.handlers["session_before_compact"]
		host.mu.Unlock()
		req := fxStartRequest(host, fxEventRequest("session_before_compact", id, roCompactEvent("threshold", 20_000, "u1", branch...)))
		<-reached
		req.cancel("test")
		// Requests are read in order: once the next event has been served, the cancellation has been applied.
		f.FireAll("turn_start", rec{})
		close(gate)
		raw, failure := req.wait()
		if failure != "" || !strings.Contains(string(raw), `"cancel":true`) {
			t.Errorf("want {cancel:true}, got %s (%q)", raw, failure)
		}
		roEqual(t, len(f.CallsTo("ui.notify")), 0, "a cancellation is not reported as a failure")
	})
}

func TestRolloverChronologicalRetainedTail(t *testing.T) {
	summary := autoHandoffPrefix + "\nContext rolled over after entry boundary."
	boundary := func() rec { return rec{"role": "compactionSummary", "summary": summary, "timestamp": float64(5)} }
	user := func(text string) rec { return rec{"role": "user", "content": text, "timestamp": float64(1)} }
	assistant := func(text string) rec {
		return rec{"role": "assistant", "content": []any{fxText(text)}, "stopReason": "stop", "timestamp": float64(1)}
	}

	t.Run("a retained assistant head is announced and the tail moves ahead of the record", func(t *testing.T) {
		session := fxMemorySession("")
		session.appendMessage(user("a"))
		kept := session.appendMessage(assistant("kept reply"))
		session.appendMessage(user("kept ask"))
		session.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
		f := newFixture(t, withSession(session), withRealProjection())
		after := user("after")
		got := f.ContextEvent(boundary(), assistant("kept reply"), user("kept ask"), after)
		want := []rec{
			{"role": "custom", "customType": "posthorse-retained-tail", "content": "[Retained pre-rollover tail]", "display": false, "timestamp": float64(5)},
			assistant("kept reply"), user("kept ask"), boundary(), after,
		}
		roSameJSON(t, got["messages"], want, "retained tail first, with the sentinel")
	})

	t.Run("a retained user head needs no sentinel", func(t *testing.T) {
		session := fxMemorySession("")
		kept := session.appendMessage(user("kept ask"))
		session.appendMessage(assistant("kept reply"))
		session.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
		f := newFixture(t, withSession(session), withRealProjection())
		got := f.ContextEvent(boundary(), user("kept ask"), assistant("kept reply"))
		roSameJSON(t, got["messages"], []rec{user("kept ask"), assistant("kept reply"), boundary()}, "retained tail first")
	})

	t.Run("a plain native compaction summary is left alone", func(t *testing.T) {
		session := fxMemorySession("")
		kept := session.appendMessage(user("kept ask"))
		session.appendCompaction("native summary", kept, 100, nil)
		f := newFixture(t, withSession(session), withRealProjection())
		native := rec{"role": "compactionSummary", "summary": "native summary", "timestamp": float64(5)}
		if got := f.ContextEvent(native, user("kept ask")); got != nil {
			t.Errorf("a native summary must keep Pi's order, got %v", got)
		}
	})

	t.Run("fewer messages than retained entries is left alone", func(t *testing.T) {
		session := fxMemorySession("")
		kept := session.appendMessage(user("kept ask"))
		session.appendMessage(assistant("kept reply"))
		session.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
		f := newFixture(t, withSession(session), withRealProjection())
		if got := f.ContextEvent(boundary(), user("kept ask")); got != nil {
			t.Errorf("a context without the whole retained tail must stay as it is, got %v", got)
		}
	})
}
