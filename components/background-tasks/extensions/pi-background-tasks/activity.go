package pi_background_tasks

import "time"

// Activity events for external observers. In the original they go to a broker object another Node extension
// (pi-session-bridge) installs on `globalThis[Symbol.for("kendex.pi.activity")]`; a Go extension is another process, so
// nothing publishes them yet (port/PORT.md, "Process-global interop"). The event builder is ported and publishes to
// activityBroker, which is nil until an owner decision gives it a target.
// upstream: extensions/activity.ts.

type activityEvent struct {
	Type       string         `json:"type"`
	Source     string         `json:"source"`
	Severity   string         `json:"severity"`
	Importance string         `json:"importance"`
	Summary    string         `json:"summary"`
	Refs       map[string]any `json:"refs,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
	Ts         string         `json:"ts,omitempty"`
}

type activityOptions struct {
	eventAt        *int64
	matchedPattern string
	newOutputTail  string
	sequence       *int
}

const (
	activityCommandDetailMax = 200
	activityOutputTailMax    = 512
)

// activityBroker receives published events; nil publishes nothing.
var activityBroker func(activityEvent)

func publishBackgroundTaskStarted(t *task) {
	publishBackgroundTaskActivity("start", t, activityOptions{eventAt: &t.StartedAt, sequence: &t.WakeSequence})
}

func publishBackgroundTaskActivity(eventType string, t *task, o activityOptions) {
	if activityBroker == nil {
		return
	}
	activityBroker(buildBackgroundTaskActivity(eventType, t, o))
}

func buildBackgroundTaskActivity(eventType string, t *task, o activityOptions) activityEvent {
	typ := backgroundTaskActivityType(eventType, t.Status)
	sequence := t.WakeSequence
	if o.sequence != nil {
		sequence = *o.sequence
	}
	details := map[string]any{
		"command":      jsHead(t.Command, activityCommandDetailMax),
		"event_type":   eventType,
		"exit_code":    nil,
		"output_bytes": t.OutputBytes,
		"sequence":     sequence,
		"status":       t.Status,
	}
	if t.ExitCode != nil {
		details["exit_code"] = *t.ExitCode
	}
	if o.matchedPattern != "" {
		details["matched_pattern"] = o.matchedPattern
	}
	if o.newOutputTail != "" {
		details["new_output_tail"] = truncateActivityTail(o.newOutputTail)
	}
	if t.TerminationReason != "" {
		details["termination_reason"] = t.TerminationReason
	}
	at := t.UpdatedAt
	if o.eventAt != nil {
		at = *o.eventAt
	}
	return activityEvent{
		Type: typ, Source: "pi-bg-task", Severity: backgroundTaskSeverity(typ), Importance: backgroundTaskImportance(typ),
		Summary: "background task " + t.ID + " " + backgroundTaskSummaryVerb(typ), Refs: map[string]any{"bg_task_id": t.ID}, Details: details,
		Ts: time.UnixMilli(at).UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

func backgroundTaskActivityType(eventType, status string) string {
	switch {
	case eventType == "start":
		return "bg_task.started"
	case eventType == eventOutput:
		return "bg_task.output_matched"
	case status == statusTimedOut:
		return "bg_task.timed_out"
	case status == statusFailed:
		return "bg_task.failed"
	case status == statusStopped:
		return "bg_task.stopped"
	}
	return "bg_task.completed"
}

func backgroundTaskSeverity(typ string) string {
	switch typ {
	case "bg_task.failed", "bg_task.timed_out":
		return "error"
	case "bg_task.stopped":
		return "warning"
	case "bg_task.completed":
		return "success"
	}
	return "info"
}

func backgroundTaskImportance(typ string) string {
	switch typ {
	case "bg_task.output_matched", "bg_task.started":
		return "noisy"
	case "bg_task.completed":
		return "normal"
	}
	return "important"
}

func backgroundTaskSummaryVerb(typ string) string {
	switch typ {
	case "bg_task.started":
		return "started"
	case "bg_task.output_matched":
		return "matched output"
	case "bg_task.failed":
		return "failed"
	case "bg_task.timed_out":
		return "timed out"
	case "bg_task.stopped":
		return "stopped"
	}
	return "completed"
}

func truncateActivityTail(tail string) string {
	if jsLen(tail) <= activityOutputTailMax {
		return tail
	}
	return jsTail(tail, activityOutputTailMax)
}
