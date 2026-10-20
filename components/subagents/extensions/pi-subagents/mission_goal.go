package pi_subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Port of src/missions/goal-driver.ts: after a turn, a goal mission whose runs are idle and whose budget is not spent gets a
// notice that says what to do next, built only from persisted state.

const maxGoalActionLength = 180

// GoalRetainedChild is the part of a retained child a goal notice reads (retained-children.ts RetainedChild).
type GoalRetainedChild struct {
	RunID       string
	ParentRunID string
	Agent       string
	Resumable   bool // resumability.state === "resumable"
}

// GoalControlEvent is the `needs_attention` control event a notice carries.
type GoalControlEvent struct {
	Type    string `json:"type"`
	To      string `json:"to"`
	Ts      int64  `json:"ts"`
	RunID   string `json:"runId"`
	Agent   string `json:"agent"`
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

// GoalContinuationNotice is one nudge.
type GoalContinuationNotice struct {
	MissionID string
	Message   string
	Event     GoalControlEvent
}

func boundedGoalText(value string) string {
	normalized := jsCollapseSpaces(value)
	if utf16Units(normalized) > maxGoalActionLength {
		return utf16Prefix(normalized, maxGoalActionLength-1) + "…"
	}
	return normalized
}

func readLinkedRun(run MissionRunLink) (MissionRunLink, error) {
	if run.AsyncDir == "" {
		return run, nil
	}
	statusPath := filepath.Join(run.AsyncDir, "status.json")
	raw, err := os.ReadFile(statusPath)
	if err != nil {
		if os.IsNotExist(err) {
			return run, nil
		}
		return run, err
	}
	parsed, msg := missionParseJSON(string(raw))
	if msg != "" {
		return run, errors.New(msg)
	}
	if parsed == nil {
		return run, errors.New("Cannot read properties of null (reading 'state')")
	}
	status, _ := parsed.(*jsObject)
	if status == nil {
		status = newObject()
	}
	state, ok := missionNonBlank(status.vals["state"])
	if !ok {
		return run, fmt.Errorf("Linked run status '%s' is missing state", statusPath)
	}
	tokens, hasTokens := usageFromUnknown(status.vals["totalTokens"])
	if !hasTokens {
		if steps, isArray := status.vals["steps"].([]any); isArray {
			hasTokens = true
			for _, step := range steps {
				if o, ok := step.(*jsObject); ok {
					if n, ok := usageFromUnknown(o.vals["tokens"]); ok {
						tokens += n
					}
				}
			}
		}
	}
	next := run
	next.Status = state
	if !isActiveRunStatus(state) && run.CompletedAt == "" {
		next.CompletedAt = isoMillis(time.Now())
	}
	if hasTokens {
		next.Usage = &MissionTokenUsage{Tokens: tokens}
	}
	return next, nil
}

func refreshGoalMission(loc MissionStoreLocation, record *MissionRecord) (*MissionRecord, error) {
	runs := make([]MissionRunLink, len(record.Runs))
	changed := false
	for i, run := range record.Runs {
		next, err := readLinkedRun(run)
		if err != nil {
			return nil, err
		}
		runs[i] = next
		changed = changed || marshalJSON(next.toObject(), "") != marshalJSON(run.toObject(), "")
	}
	if !changed {
		return record, nil
	}
	active := false
	for _, run := range runs {
		active = active || (run.Status != "" && isActiveRunStatus(run.Status))
	}
	status := record.Status
	if active || record.Goal != nil {
		status = "active"
	}
	return updateMission(loc, record.ID, MissionUpdateInput{Status: status, AddRuns: runs}, time.Now(), -1)
}

func readyActionFromValue(value any, pathLabel string, depth int) string {
	if depth > 8 {
		return ""
	}
	switch v := value.(type) {
	case []any:
		for i, e := range v {
			if found := readyActionFromValue(e, fmt.Sprintf("%s[%d]", pathLabel, i), depth+1); found != "" {
				return found
			}
		}
		return ""
	case *jsObject:
		for _, key := range []string{"nextReadyAction", "nextAction"} {
			if s, ok := missionNonBlank(v.vals[key]); ok {
				return boundedGoalText(s)
			}
		}
		if v.vals["status"] == "ready" {
			for _, key := range []string{"action", "task", "title", "summary"} {
				if s, ok := missionNonBlank(v.vals[key]); ok {
					return boundedGoalText(s)
				}
			}
			return "Continue ready mission state at " + pathLabel
		}
		for _, key := range v.order() {
			if found := readyActionFromValue(v.vals[key], pathLabel+"."+key, depth+1); found != "" {
				return found
			}
		}
	}
	return ""
}

func missionStateAction(loc MissionStoreLocation, record *MissionRecord) (string, error) {
	statePath, err := missionStatePath(loc, record.ID)
	if err != nil {
		return "", err
	}
	if raw, rerr := os.ReadFile(statePath); rerr == nil {
		parsed, msg := missionParseJSON(string(raw))
		if msg != "" {
			return "", errors.New(msg)
		}
		if action := readyActionFromValue(parsed, "state", 0); action != "" {
			return action, nil
		}
	}
	for _, d := range record.Decisions {
		if d.Status == "open" {
			if d.Recommendation != "" {
				return boundedGoalText(d.Recommendation), nil
			}
			return boundedGoalText("Resolve decision: " + d.Title), nil
		}
	}
	return "", nil
}

func nextReadyAction(loc MissionStoreLocation, record *MissionRecord, retained []GoalRetainedChild) (string, error) {
	action, err := missionStateAction(loc, record)
	if err != nil {
		return "", err
	}
	if action == "" {
		action = "Continue objective: " + record.Objective
		if n := len(record.Runs); n > 0 && (record.Runs[n-1].Status == "failed" || record.Runs[n-1].Status == "paused") {
			action = "Inspect linked run " + record.Runs[n-1].RunID + " and continue the mission"
		}
	}
	if n := len(record.Runs); n > 0 {
		latest := record.Runs[n-1]
		for _, child := range retained {
			if child.Resumable && (child.RunID == latest.RunID || child.ParentRunID == latest.RunID) {
				return fmt.Sprintf("Resume retained child %s (%s) for: %s", child.RunID, child.Agent, boundedGoalText(action)), nil
			}
		}
	}
	return boundedGoalText(action), nil
}

// collectGoalContinuationNotices is collectGoalContinuationNotices; now 0 is the current time.
func collectGoalContinuationNotices(loc MissionStoreLocation, ownerSessionID string, retained []GoalRetainedChild, turnID int, now int64) ([]GoalContinuationNotice, error) {
	notices := []GoalContinuationNotice{}
	seen := map[string]bool{}
	listed, err := listMissions(loc)
	if err != nil {
		return nil, err
	}
	for _, l := range listed.Records {
		if l.OwnerSessionID != ownerSessionID || l.Goal == nil || isTerminalMissionStatus(l.Status) {
			continue
		}
		record, err := readMission(loc, l.ID)
		if err != nil {
			return nil, err
		}
		if record, err = refreshGoalMission(loc, record); err != nil {
			return nil, err
		}
		if record.Goal == nil || record.Goal.Status != "active" || record.Budget == nil {
			continue
		}
		used := int64(0)
		if record.Usage != nil {
			used = record.Usage.Tokens
		}
		if used >= record.Budget.Tokens {
			usage := record.Usage
			if usage == nil {
				usage = &MissionTokenUsage{}
			}
			if record, err = updateMission(loc, record.ID, MissionUpdateInput{Usage: usage}, time.Now(), -1); err != nil {
				return nil, err
			}
			if record.Goal != nil && record.Goal.Status == "budget-exhausted" {
				continue
			}
		}
		active := false
		for _, run := range record.Runs {
			active = active || (run.Status != "" && isActiveRunStatus(run.Status))
		}
		if active || seen[record.ID] || record.Budget == nil {
			continue
		}
		seen[record.ID] = true
		budget := record.Budget.Tokens
		used = 0
		if record.Usage != nil {
			used = record.Usage.Tokens
		}
		remaining := budget - used
		if remaining < 0 {
			remaining = 0
		}
		next, err := nextReadyAction(loc, record, retained)
		if err != nil {
			return nil, err
		}
		message := strings.Join([]string{
			"Goal mission needs attention: " + boundedGoalText(record.Title),
			"Mission: " + record.ID,
			fmt.Sprintf("Remaining budget: %d tokens (%d/%d used)", remaining, used, budget),
			"Next ready action: " + next,
		}, "\n")
		ts := now
		if ts == 0 {
			ts = time.Now().UnixMilli()
		}
		notices = append(notices, GoalContinuationNotice{MissionID: record.ID, Message: message, Event: GoalControlEvent{
			Type: "needs_attention", To: "needs_attention", Ts: ts, RunID: fmt.Sprintf("goal-%s-turn-%d", record.ID, turnID), Agent: "goal mission", Message: message, Reason: "idle",
		}})
	}
	return notices, nil
}
