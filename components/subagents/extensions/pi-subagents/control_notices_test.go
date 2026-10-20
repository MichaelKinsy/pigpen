package pi_subagents

import (
	"encoding/json"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of control-notices.test.ts and steering-notices.test.ts.

type sentMessage struct {
	msg     sdk.CustomMessage
	trigger bool
}

func noticeRecorder() (*[]sentMessage, messageSender) {
	var sent []sentMessage
	return &sent, func(m sdk.CustomMessage, trigger bool) error {
		sent = append(sent, sentMessage{m, trigger})
		return nil
	}
}

func attentionEvent() controlEvent {
	zero := 0
	return controlEvent{Type: "needs_attention", To: "needs_attention", TS: 1, RunID: "run-1", Agent: "worker", Index: &zero, Message: "worker needs attention", Reason: "idle"}
}

func TestControlNoticeTwins(t *testing.T) {
	const f = "control-notices"
	tw(t, f, "delivers async needs-attention notices immediately", func(t *testing.T) {
		sent, send := noticeRecorder()
		if err := handleSubagentControlNotice(send, map[string]bool{}, controlMessageDetails{Source: "async", Event: attentionEvent()}); err != nil {
			t.Fatal(err)
		}
		equal(t, len(*sent), 1, "sent")
		equal(t, (*sent)[0].trigger, true, "triggerTurn")
		equal(t, (*sent)[0].msg.CustomType, "subagent_control_notice", "type")
	})
	tw(t, f, "delivers goal notices without starting a new turn", func(t *testing.T) {
		sent, send := noticeRecorder()
		if err := handleSubagentControlNotice(send, map[string]bool{}, controlMessageDetails{Source: "goal", Event: attentionEvent(), NoticeText: "Goal is ready."}); err != nil {
			t.Fatal(err)
		}
		equal(t, len(*sent), 1, "sent")
		equal(t, (*sent)[0].trigger, false, "triggerTurn")
		equal(t, (*sent)[0].msg.Content, "Goal is ready.", "content")
	})
	tw(t, f, "does not queue a foreground notice that Pi could flush after completion", func(t *testing.T) {
		sent, send := noticeRecorder()
		if err := handleSubagentControlNotice(send, map[string]bool{}, controlMessageDetails{Source: "foreground", Event: attentionEvent()}); err != nil {
			t.Fatal(err)
		}
		equal(t, len(*sent), 0, "visible")
	})
}

func TestSteeringNoticeTwins(t *testing.T) {
	const f = "steering-notices"
	notice := func(state, session string) steeringMessageDetails {
		return steeringMessageDetails{steeringNotice: steeringNotice{RunID: "run", RequestID: "request", State: state, Message: "not delivered", CurrentSessionID: session}}
	}
	tw(t, f, "delivers a failure notice to the matching parent session", func(t *testing.T) {
		sent, send := noticeRecorder()
		if err := handleSubagentSteeringNotice(send, "session-1", notice("failed", "session-1")); err != nil {
			t.Fatal(err)
		}
		equal(t, len(*sent), 1, "sent")
		b, _ := json.Marshal((*sent)[0].msg)
		polMatches(t, string(b), "not delivered")
	})
	tw(t, f, "suppresses notices owned by another or missing parent session", func(t *testing.T) {
		sent, send := noticeRecorder()
		for _, session := range []string{"session-2", ""} {
			handleSubagentSteeringNotice(send, "session-1", notice("partial", session))
		}
		equal(t, len(*sent), 0, "sent")
	})
	tw(t, f, "delivers recovered outcomes without changing their state", func(t *testing.T) {
		sent, send := noticeRecorder()
		handleSubagentSteeringNotice(send, "session-1", notice("recovered", "session-1"))
		equal(t, len(*sent), 1, "sent")
		d, ok := (*sent)[0].msg.Details.(steeringMessageDetails)
		equal(t, ok && d.State == "recovered", true, "state")
	})
}
