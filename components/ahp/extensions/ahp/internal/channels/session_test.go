package channels_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// Twins of the "session chat aggregation" describe of upstream test/session-summary.test.ts.

const (
	start = "2025-01-01T00:00:00.000Z"
	end   = "2025-01-01T00:00:01.000Z"
)

func summary(resource string, status ahptypes.SessionStatus, modifiedAt string) ahptypes.ChatSummary {
	return ahptypes.ChatSummary{Resource: resource, Title: resource, Status: status, ModifiedAt: modifiedAt}
}

func withActivity(s ahptypes.ChatSummary, activity string) ahptypes.ChatSummary {
	s.Activity = &activity
	return s
}

func sessionState(chats ...ahptypes.ChatSummary) *ahptypes.SessionState {
	return &ahptypes.SessionState{
		Provider: "pi", Title: "Session", Status: ahptypes.SessionStatusIdle, Lifecycle: ahptypes.SessionLifecycleReady,
		ActiveClients: []ahptypes.SessionActiveClient{}, Chats: chats,
	}
}

func sp(s string) *string { return &s }

func check(t *testing.T, got channels.SessionChatAggregate, status ahptypes.SessionStatus, activity *string, modifiedAt string) {
	t.Helper()
	want := channels.SessionChatAggregate{Status: status, Activity: activity, ModifiedAt: modifiedAt}
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		t.Fatalf("aggregate = %s, want %s", gj, wj)
	}
}

func TestSessionChatAggregation(t *testing.T) {
	twin.Run(t, "session-summary", "passes through single-chat activity but preserves session-owned and unknown flags", func(t *testing.T) {
		flags := ahptypes.SessionStatusIsRead | ahptypes.SessionStatusIsArchived | (1 << 10)
		for _, status := range []ahptypes.SessionStatus{
			ahptypes.SessionStatusIdle, ahptypes.SessionStatusInProgress, ahptypes.SessionStatusError, ahptypes.SessionStatusInputNeeded,
		} {
			state := sessionState(withActivity(summary("chat", status, start), "Working"))
			state.Status = ahptypes.SessionStatusIdle | flags
			before, _ := json.Marshal(state)
			check(t, channels.AggregateSessionChats(state), status|flags, sp("Working"), start)
			after, _ := json.Marshal(state)
			if string(before) != string(after) {
				t.Fatal("projection must not mutate source state")
			}
		}
		state := sessionState(summary("chat", ahptypes.SessionStatusIdle|ahptypes.SessionStatusIsRead, start))
		if got := channels.AggregateSessionChats(state).Status; got != ahptypes.SessionStatusIdle {
			t.Fatalf("chat flags must not leak into the session; status = %d", got)
		}
	})

	twin.Run(t, "session-summary", "uses default chat activity but the latest timestamp, comparing instants rather than strings", func(t *testing.T) {
		recent := "2024-12-31T19:01:00-05:00"
		state := sessionState(
			withActivity(summary("default", ahptypes.SessionStatusIdle, start), "Default"),
			withActivity(summary("recent", ahptypes.SessionStatusInProgress, recent), "Recent"),
		)
		state.DefaultChat = sp("default")
		check(t, channels.AggregateSessionChats(state), ahptypes.SessionStatusIdle, sp("Default"), recent)
		for _, defaultChat := range []*string{nil, sp("missing")} {
			state.DefaultChat = defaultChat
			check(t, channels.AggregateSessionChats(state), ahptypes.SessionStatusInProgress, sp("Recent"), recent)
		}
	})

	twin.Run(t, "session-summary", "promotes Error and InputNeeded, preferring the most recent blocker of the winning kind", func(t *testing.T) {
		state := sessionState(summary("default", ahptypes.SessionStatusIdle, start), withActivity(summary("error", ahptypes.SessionStatusError, start), "Error"))
		state.DefaultChat = sp("default")
		check(t, channels.AggregateSessionChats(state), ahptypes.SessionStatusError, sp("Error"), start)
		state.Chats = append(state.Chats,
			summary("old-input", ahptypes.SessionStatusInputNeeded, start),
			withActivity(summary("input", ahptypes.SessionStatusInputNeeded, end), "Approval"))
		check(t, channels.AggregateSessionChats(state), ahptypes.SessionStatusInputNeeded, sp("Approval"), end)
	})

	twin.Run(t, "session-summary", "handles an empty catalogue and chooses stable ties and valid timestamps", func(t *testing.T) {
		empty := sessionState()
		empty.Activity = sp("Starting")
		check(t, channels.AggregateSessionChats(empty), ahptypes.SessionStatusIdle, sp("Starting"), "")
		if got := channels.SessionSummaryOf("session", start, empty, nil).ModifiedAt; got != start {
			t.Fatalf("modifiedAt = %s", got)
		}
		state := sessionState(
			withActivity(summary("invalid", ahptypes.SessionStatusIdle, "invalid"), "Invalid"),
			withActivity(summary("first", ahptypes.SessionStatusInProgress, start), "First"),
			withActivity(summary("tie", ahptypes.SessionStatusIdle, start), "Tie"),
		)
		check(t, channels.AggregateSessionChats(state), ahptypes.SessionStatusInProgress, sp("First"), start)
	})
}
