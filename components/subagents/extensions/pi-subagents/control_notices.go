package pi_subagents

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/extension/control-notices.ts and src/extension/steering-notices.ts: the notices a run's attention events and
// steering outcomes put into the parent conversation.

const (
	subagentControlMessageType  = "subagent_control_notice"
	subagentSteeringMessageType = "subagent_steering_notice"
)

// messageSender delivers one custom message to the parent session (ctx.SendCustomMessage; the original's pi.sendMessage).
type messageSender func(msg sdk.CustomMessage, triggerTurn bool) error

// controlMessageDetails is SubagentControlMessageDetails.
type controlMessageDetails struct {
	Event               controlEvent `json:"event"`
	Source              string       `json:"source,omitempty"` // "foreground", "async" or "goal"
	AsyncDir            string       `json:"asyncDir,omitempty"`
	ChildIntercomTarget string       `json:"childIntercomTarget,omitempty"`
	NoticeText          string       `json:"noticeText,omitempty"`
}

func formatSubagentControlNotice(d controlMessageDetails, content string) string {
	if d.NoticeText != "" {
		return d.NoticeText
	}
	if content != "" {
		return content
	}
	return formatControlNoticeMessage(d.Event, d.ChildIntercomTarget)
}

// handleSubagentControlNotice delivers a needs-attention notice once per key. A long-running notice is not delivered; a foreground
// one is not either (a foreground tool blocks the parent from showing it, and the run can finish before the message is flushed,
// and a queued message cannot be withdrawn). Only an async notice starts a turn.
func handleSubagentControlNotice(send messageSender, visible map[string]bool, d controlMessageDetails) error {
	if d.Event.Type == "active_long_running" || d.Source == "foreground" {
		return nil
	}
	key := controlNotificationKey(d.Event, d.ChildIntercomTarget)
	if visible[key] {
		return nil
	}
	visible[key] = true
	text := d.NoticeText
	if text == "" {
		text = formatControlNoticeMessage(d.Event, d.ChildIntercomTarget)
	}
	d.NoticeText = text
	return send(sdk.CustomMessage{CustomType: subagentControlMessageType, Content: text, Display: true, Details: d}, d.Source == "async")
}

// steeringNotice is SteeringNotice (an `subagent.steering.notice` event of a run's events.jsonl).
type steeringNotice struct {
	Type             string `json:"type"`
	TS               int64  `json:"ts"`
	RunID            string `json:"runId"`
	RequestID        string `json:"requestId"`
	State            string `json:"state"` // "failed", "partial" or "recovered"
	Message          string `json:"message"`
	CurrentSessionID string `json:"currentSessionId,omitempty"`
}

// steeringMessageDetails is SubagentSteeringMessageDetails.
type steeringMessageDetails struct {
	steeringNotice
	Source     string `json:"source,omitempty"`
	AsyncDir   string `json:"asyncDir,omitempty"`
	NoticeText string `json:"noticeText,omitempty"`
}

func formatSteeringNotice(d steeringNotice) string {
	return "Subagent steering " + d.State + ": " + d.RunID + "\nRequest: " + d.RequestID + "\n" + d.Message + "\nInspect the run status before sending another correction."
}

// handleSubagentSteeringNotice delivers a failed, partial or recovered steering outcome to the parent session that owns the run, and
// to no other (a notice with no session, or another one, is dropped).
func handleSubagentSteeringNotice(send messageSender, currentSessionID string, d steeringMessageDetails) error {
	if d.State != "failed" && d.State != "partial" && d.State != "recovered" {
		return nil
	}
	if currentSessionID == "" || d.CurrentSessionID != currentSessionID {
		return nil
	}
	text := d.NoticeText
	if text == "" {
		text = formatSteeringNotice(d.steeringNotice)
	}
	d.NoticeText = text
	return send(sdk.CustomMessage{CustomType: subagentSteeringMessageType, Content: text, Display: true, Details: d}, true)
}
