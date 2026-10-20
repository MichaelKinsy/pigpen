package pi_goal_x

// ported from goal-activity.ts: the human-readable activity feed derived from ledger events.

import (
	"cmp"
	"slices"
)

const (
	conciseEvidenceMax = 48
	activityReasonMax  = 80
)

type goalActivityItem struct {
	at, kind, text string
	marker         string // "done" | "current" | "skipped"; "" = none
}

type deriveGoalActivityOptions struct {
	taskTitles map[string]string // taskId -> title; nil = undefined
	limit      *int              // nil = the default of 5
}

func activityQuote(title string) string { return "\u201c" + title + "\u201d" }

func activityTitleFor(taskTitles map[string]string, taskID string) string {
	if t, ok := taskTitles[taskID]; ok {
		return t
	}
	return taskID
}

// activityOneLine is value.replace(/\s+/g, " ").trim().
func activityOneLine(value string) string { return jsTrim(wsRun.ReplaceAllString(value, " ")) }

func mapActivityEvent(event *jsObject, taskTitles map[string]string) (goalActivityItem, bool) {
	at := evStr(event, "at")
	reason := evStr(event, "reason")
	item := func(kind, text string) (goalActivityItem, bool) {
		return goalActivityItem{at: at, kind: kind, text: text}, true
	}
	marked := func(kind, marker, text string) (goalActivityItem, bool) {
		return goalActivityItem{at: at, kind: kind, marker: marker, text: text}, true
	}
	switch evStr(event, "type") {
	case "goal_created":
		return item("goal", "Created and focused the goal.")
	case "goal_tweaked":
		return item("goal", "Updated the goal objective and task plan.")
	case "auditor_toggled":
		if on, _ := event.vals["enabled"].(bool); on {
			return item("goal", "Turned the independent auditor on.")
		}
		return item("goal", "Turned the independent auditor off.")
	case "goal_paused":
		if reason != "" {
			return item("goal", "Paused the goal \u2014 "+truncateText(activityOneLine(reason), activityReasonMax))
		}
		return item("goal", "Paused the goal.")
	case "goal_resumed":
		return item("goal", "Resumed the goal.")
	case "goal_blocked":
		if reason != "" {
			return item("goal", "Blocked \u2014 "+truncateText(activityOneLine(reason), activityReasonMax))
		}
		return item("goal", "Reported a blocker.")
	case "goal_budget_limited":
		return item("goal", "Reached the configured token budget.")
	case "goal_completed":
		return item("goal", "Completed the goal.")
	case "goal_aborted":
		if reason != "" {
			return item("goal", "Aborted the goal \u2014 "+truncateText(activityOneLine(reason), activityReasonMax))
		}
		return item("goal", "Aborted the goal.")
	case "task_started":
		return marked("task", "current", "Started "+activityQuote(activityTitleFor(taskTitles, evStr(event, "taskId")))+".")
	case "task_complete":
		title := activityTitleFor(taskTitles, evStr(event, "taskId"))
		suffix := ""
		if ev := evStr(event, "evidence"); ev != "" {
			evidence := activityOneLine(ev)
			// Evidence only when concise; never bloat the feed.
			if evidence != "" && utf16Len(evidence) <= conciseEvidenceMax {
				suffix = " \u2014 " + evidence
			}
		}
		return marked("task", "done", "Completed "+activityQuote(title)+"."+suffix)
	case "task_skipped":
		title := activityTitleFor(taskTitles, evStr(event, "taskId"))
		why := ""
		if reason != "" {
			why = " \u2014 " + truncateText(activityOneLine(reason), activityReasonMax)
		}
		return marked("task", "skipped", "Skipped "+activityQuote(title)+why+".")
	case "task_reopened":
		return item("task", "Reopened "+activityQuote(activityTitleFor(taskTitles, evStr(event, "taskId")))+".")
	case "completion_requested":
		return item("verification", "Requested completion review.")
	case "audit_started":
		return item("audit", "Started independent completion review.")
	case "audit_result":
		switch evStr(event, "verdict") {
		case "approved":
			return item("audit", "Independent auditor approved completion.")
		case "disapproved":
			return item("audit", "Completion review requested additional work.")
		}
		return item("audit", "Completion review could not finish.")
	case "audit_skipped":
		if reason == "user_aborted" {
			return item("audit", "Completion review was aborted by the user.")
		}
		return item("audit", "Skipped independent completion review (auditor disabled).")
	case "goal_archived":
		return marked("archive", "done", "Archived the completed goal.")
	}
	// goal_focused, goal_unfocused, goal_stalled, goal_budget_warning, task_list_set: low-value noise.
	return goalActivityItem{}, false
}

// indexedActivity remembers the slices handed out by indexedActivityEvents (JavaScript's WeakSet of arrays): the runtime
// index already deduplicates and sorts them. A slice is identified by its first element and length.
var indexedActivity = map[*jsObject]int{}

func indexedActivityEvents(events []*jsObject) []*jsObject {
	if len(events) > 0 {
		if len(indexedActivity) > 256 {
			indexedActivity = map[*jsObject]int{}
		}
		indexedActivity[events[0]] = len(events)
	}
	return events
}

func isIndexedActivity(events []*jsObject) bool {
	if len(events) == 0 {
		return false
	}
	n, ok := indexedActivity[events[0]]
	return ok && n == len(events)
}

// activityEventKey is the stable event identity for append-order deduplication, independent of title edits.
func activityEventKey(event *jsObject) (string, bool) {
	item, ok := mapActivityEvent(event, nil)
	if !ok {
		return "", false
	}
	return item.kind + ":" + item.text, true
}

var activityTypes = map[string]bool{
	"goal_created": true, "goal_tweaked": true, "auditor_toggled": true, "goal_paused": true, "goal_resumed": true, "goal_blocked": true,
	"goal_budget_limited": true, "goal_completed": true, "goal_aborted": true, "task_started": true, "task_complete": true, "task_skipped": true,
	"task_reopened": true, "completion_requested": true, "audit_started": true, "audit_result": true, "audit_skipped": true, "goal_archived": true,
}

func isActivityEvent(event *jsObject) bool { return activityTypes[evStr(event, "type")] }

func sameActivityEvent(left, right *jsObject) bool {
	if evStr(left, "type") != evStr(right, "type") {
		return false
	}
	if lt, lok := left.vals["taskId"]; lok {
		if rt, rok := right.vals["taskId"]; rok && lt != rt {
			return false
		}
	}
	lk, lok := activityEventKey(left)
	rk, rok := activityEventKey(right)
	return lok == rok && lk == rk
}

// sliceLast is items.slice(-limit): limit 0 keeps everything, a negative limit drops from the front.
func sliceLast(items []goalActivityItem, limit int) []goalActivityItem {
	start := -limit
	if start < 0 {
		start += len(items)
		if start < 0 {
			start = 0
		}
	}
	if start > len(items) {
		start = len(items)
	}
	return items[start:]
}

// deriveGoalActivity returns the activity of one goal, oldest first, capped to the latest `limit` entries (default 5), with
// adjacent duplicates merged.
func deriveGoalActivity(events []*jsObject, goalID string, o deriveGoalActivityOptions) []goalActivityItem {
	limit := 5
	if o.limit != nil {
		limit = *o.limit
	}
	items := []goalActivityItem{}
	if isIndexedActivity(events) {
		for i := len(events) - 1; i >= 0; i-- {
			e := events[i]
			if !hasGoalId(e) || e.vals["goalId"] != any(goalID) {
				continue
			}
			if item, ok := mapActivityEvent(e, o.taskTitles); ok {
				items = append(items, item)
			}
			if limit > 0 && len(items) >= limit {
				break
			}
		}
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		return sliceLast(items, limit)
	}
	for _, e := range events {
		if !hasGoalId(e) || e.vals["goalId"] != any(goalID) {
			continue
		}
		item, ok := mapActivityEvent(e, o.taskTitles)
		if !ok {
			continue
		}
		if n := len(items); n > 0 && items[n-1].kind == item.kind && items[n-1].text == item.text {
			continue
		}
		items = append(items, item)
	}
	// Stable sort by timestamp: append order is expected, but the feed is ordered defensively.
	activitySortStable(items)
	return sliceLast(items, limit)
}

// activitySortStable is items.sort((a, b) => a.at < b.at ? -1 : a.at > b.at ? 1 : 0): stable, and O(n log n) like the engine's sort
// (an insertion sort takes quadratic time on a ledger whose timestamps run backwards, for example after a clock change).
func activitySortStable(s []goalActivityItem) {
	slices.SortStableFunc(s, func(a, b goalActivityItem) int { return cmp.Compare(a.at, b.at) })
}
