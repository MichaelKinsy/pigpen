package pi_subagents

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// MissionGoalUpdate is update.goal: false (Off) disables goal mode, otherwise the goal takes Status.
type MissionGoalUpdate struct {
	Off    bool
	Status string
}

// MissionHeartbeatUpdate is a child heartbeat before the store stamps its time.
type MissionHeartbeatUpdate struct{ Status, Phase, Message string }

// MissionWorkflowChildUpdate is one child of an upsert: the fields the caller knows, the rest is stamped by the store.
type MissionWorkflowChildUpdate struct {
	WorkflowRunID string
	Key           string
	Status        string
	RunID         string
	Agent         string
	Task          string
	Label         string
	Phase         string
	CompletedAt   string
	SessionPath   string
	StartedAt     string
	ArtifactPaths []string
	Heartbeat     *MissionHeartbeatUpdate
}

func (u MissionWorkflowChildUpdate) toObject(nowIso string) *jsObject {
	o := newObject()
	o.set("workflowRunId", u.WorkflowRunID)
	o.set("key", u.Key)
	o.set("status", u.Status)
	setIf(o, "runId", u.RunID)
	setIf(o, "agent", u.Agent)
	setIf(o, "task", u.Task)
	setIf(o, "label", u.Label)
	setIf(o, "phase", u.Phase)
	setIf(o, "completedAt", u.CompletedAt)
	setIf(o, "sessionPath", u.SessionPath)
	started := u.StartedAt
	if started == "" {
		started = nowIso
	}
	o.set("startedAt", started)
	o.set("updatedAt", nowIso)
	paths := u.ArtifactPaths
	if paths == nil {
		paths = []string{}
	}
	o.set("artifactPaths", anyStrings(paths))
	if u.Heartbeat != nil {
		h := newObject()
		setIf(h, "status", u.Heartbeat.Status)
		setIf(h, "phase", u.Heartbeat.Phase)
		setIf(h, "message", u.Heartbeat.Message)
		h.set("updatedAt", nowIso)
		o.set("heartbeat", h)
	}
	return o
}

// MissionDecisionInput is a decision to open.
type MissionDecisionInput struct {
	Title          string
	Prompt         string
	Options        []string
	Recommendation string
}

// MissionResolveDecision resolves one decision.
type MissionResolveDecision struct{ ID, Resolution string }

// MissionUpdateInput is MissionUpdateInput: a nil pointer or empty string is a field the update leaves alone.
type MissionUpdateInput struct {
	Title                  *string
	Objective              *string
	Goal                   *MissionGoalUpdate
	Budget                 *MissionTokenBudget
	Usage                  *MissionTokenUsage
	Status                 string
	Summary                *string
	Labels                 *[]string
	Acceptance             *any
	AddRuns                []MissionRunLink
	UpsertWorkflowChildren []MissionWorkflowChildUpdate
	AddArtifacts           []MissionArtifact
	AddDecisions           []MissionDecisionInput
	ResolveDecision        *MissionResolveDecision
	AddReceipts            []MissionReceipt
}

func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

func mergeWorkflowChild(existing, parsed MissionWorkflowChild) (MissionWorkflowChild, error) {
	merged, over := existing.toObject(), parsed.toObject()
	for _, k := range over.order() {
		merged.set(k, over.vals[k])
	}
	merged.set("startedAt", existing.StartedAt)
	union := make([]any, 0, len(existing.ArtifactPaths)+len(parsed.ArtifactPaths))
	for _, p := range append(append([]string{}, existing.ArtifactPaths...), parsed.ArtifactPaths...) {
		union = append(union, p)
	}
	merged.set("artifactPaths", union) // the parse below keeps each path once
	return parseMissionWorkflowChild(merged, "mission.update.upsertWorkflowChildren[]")
}

// updateMission is updateMission; retain < 0 uses the location's bound.
func updateMission(loc MissionStoreLocation, missionID string, update MissionUpdateInput, now time.Time, retain int) (*MissionRecord, error) {
	current, err := readMission(loc, missionID)
	if err != nil {
		return nil, err
	}
	runs := append([]MissionRunLink{}, current.Runs...)
	for _, candidate := range update.AddRuns {
		run, err := parseMissionRunLink(candidate.toObject(), "mission.update.addRuns[]")
		if err != nil {
			return nil, err
		}
		found := -1
		for i, item := range runs {
			if item.RunID == run.RunID && sameChildIndex(item.ChildIndex, run.ChildIndex) {
				found = i
				break
			}
		}
		if found == -1 {
			runs = append(runs, run)
		} else {
			runs[found] = mergeRun(runs[found], run)
		}
	}
	workflowChildren := append([]MissionWorkflowChild{}, current.WorkflowChildren...)
	for _, candidate := range update.UpsertWorkflowChildren {
		nowIso := isoMillis(now)
		parsed, err := parseMissionWorkflowChild(candidate.toObject(nowIso), "mission.update.upsertWorkflowChildren[]")
		if err != nil {
			return nil, err
		}
		found := -1
		for i, child := range workflowChildren {
			if child.WorkflowRunID == parsed.WorkflowRunID && child.Key == parsed.Key {
				found = i
				break
			}
		}
		if found == -1 {
			workflowChildren = append(workflowChildren, parsed)
		} else if workflowChildren[found], err = mergeWorkflowChild(workflowChildren[found], parsed); err != nil {
			return nil, err
		}
	}
	artifacts := append([]MissionArtifact{}, current.Artifacts...)
	for _, candidate := range update.AddArtifacts {
		artifact, err := parseMissionArtifact(candidate.toObject(), "mission.update.addArtifacts[]")
		if err != nil {
			return nil, err
		}
		found := -1
		for i, item := range artifacts {
			if item.Kind == artifact.Kind && absPath(item.Path) == absPath(artifact.Path) {
				found = i
				break
			}
		}
		if found == -1 {
			artifacts = append(artifacts, artifact)
		} else {
			merged := artifacts[found]
			merged.Kind, merged.Path = artifact.Kind, artifact.Path
			if artifact.Description != "" {
				merged.Description = artifact.Description
			}
			artifacts[found] = merged
		}
	}
	createdAt := isoMillis(now)
	receipts := append([]MissionReceipt{}, current.Receipts...)
	for _, candidate := range update.AddReceipts {
		candidate.CreatedAt = createdAt
		receipt, err := parseMissionReceipt(candidate.toObject(), "mission.update.addReceipts[]")
		if err != nil {
			return nil, err
		}
		found := -1
		for i, item := range receipts {
			if item.Kind == receipt.Kind && item.URL == receipt.URL {
				found = i
				break
			}
		}
		if found == -1 {
			receipts = append(receipts, receipt)
		} else {
			receipt.CreatedAt = receipts[found].CreatedAt
			receipts[found] = receipt
		}
	}
	decisions := append([]MissionDecision{}, current.Decisions...)
	for _, in := range update.AddDecisions {
		d := MissionDecision{ID: missionUUID(), Status: "open", CreatedAt: createdAt}
		if d.Title, err = missionRequiredString(in.Title, "mission.update.addDecisions[].title"); err != nil {
			return nil, err
		}
		if in.Prompt != "" {
			if d.Prompt, err = missionRequiredString(in.Prompt, "mission.update.addDecisions[].prompt"); err != nil {
				return nil, err
			}
		}
		if in.Options != nil {
			if d.Options, err = stringArrayOf(anyStrings(in.Options), "mission.update.addDecisions[].options"); err != nil {
				return nil, err
			}
		}
		if in.Recommendation != "" {
			if d.Recommendation, err = missionRequiredString(in.Recommendation, "mission.update.addDecisions[].recommendation"); err != nil {
				return nil, err
			}
		}
		decisions = append(decisions, d)
	}
	if update.ResolveDecision != nil {
		decisionID, err := validateMissionID(update.ResolveDecision.ID, "mission.update.resolveDecision.id")
		if err != nil {
			return nil, err
		}
		index := -1
		for i, d := range decisions {
			if d.ID == decisionID {
				index = i
				break
			}
		}
		if index == -1 {
			return nil, fmt.Errorf("Decision '%s' was not found in mission '%s'", decisionID, missionID)
		}
		if decisions[index].Status == "resolved" {
			return nil, fmt.Errorf("Decision '%s' is already resolved", decisionID)
		}
		resolution, err := missionRequiredString(update.ResolveDecision.Resolution, "mission.update.resolveDecision.resolution")
		if err != nil {
			return nil, err
		}
		decisions[index].Status = "resolved"
		decisions[index].ResolvedAt = createdAt
		decisions[index].Resolution = jsTrim(resolution)
	}
	budget := current.Budget
	if update.Budget != nil {
		if budget, err = parseMissionBudget(tokensObject(update.Budget.Tokens), "mission.update.budget"); err != nil {
			return nil, err
		}
	}
	var usage *MissionTokenUsage
	if update.Usage != nil {
		if usage, err = parseMissionUsage(tokensObject(update.Usage.Tokens), "mission.update.usage"); err != nil {
			return nil, err
		}
	} else {
		total := int64(0)
		for _, run := range runs {
			if run.Usage != nil {
				total += run.Usage.Tokens
			}
		}
		usage = &MissionTokenUsage{Tokens: total}
	}
	goal := current.Goal
	switch {
	case update.Goal != nil && update.Goal.Off:
		goal = nil
	case update.Goal != nil:
		if goal, err = parseMissionGoal(goalObject(update.Goal.Status), "mission.update.goal"); err != nil {
			return nil, err
		}
	}
	if goal != nil && budget == nil {
		return nil, errors.New("mission.update.budget is required when enabling a goal mission")
	}
	if goal != nil && budget != nil {
		switch {
		case usage.Tokens >= budget.Tokens:
			goal = &MissionGoal{Status: "budget-exhausted"}
		case goal.Status == "budget-exhausted":
			goal = &MissionGoal{Status: "active"}
		}
	}
	hasOpenDecisions := false
	for _, d := range decisions {
		hasOpenDecisions = hasOpenDecisions || d.Status == "open"
	}
	candidateStatus := current.Status
	if update.Status != "" {
		if candidateStatus, err = missionStatusOf(update.Status, "mission.update.status"); err != nil {
			return nil, err
		}
	} else if len(update.AddDecisions) > 0 && current.Status == "active" {
		candidateStatus = "needs_decision"
	} else if update.ResolveDecision != nil && current.Status == "needs_decision" && !hasOpenDecisions {
		candidateStatus = "active"
	}
	decisionStatus := candidateStatus
	if hasOpenDecisions && (candidateStatus == "active" || candidateStatus == "completed") {
		decisionStatus = "needs_decision"
	}
	next := *current
	next.UpdatedAt, next.Runs, next.WorkflowChildren, next.Artifacts, next.Receipts, next.Decisions = createdAt, runs, workflowChildren, artifacts, receipts, decisions
	if update.Title != nil {
		title, err := missionRequiredString(*update.Title, "mission.update.title")
		if err != nil {
			return nil, err
		}
		next.Title = jsTrim(title)
	}
	if update.Objective != nil {
		objective, err := missionRequiredString(*update.Objective, "mission.update.objective")
		if err != nil {
			return nil, err
		}
		next.Objective = jsTrim(objective)
	}
	if budget != nil {
		next.Budget = budget
	}
	if goal != nil {
		next.Goal, next.Usage = goal, usage
	}
	next.Status = decisionStatus
	if update.Summary != nil {
		if next.Summary, err = missionRequiredString(*update.Summary, "mission.update.summary"); err != nil {
			return nil, err
		}
	}
	if update.Labels != nil {
		if next.Labels, err = stringArrayOf(anyStrings(*update.Labels), "mission.update.labels"); err != nil {
			return nil, err
		}
	}
	if update.Acceptance != nil {
		next.Acceptance, next.HasAcceptance = *update.Acceptance, true
	}
	if goal == nil {
		next.Goal = nil
	}
	updated, err := writeMission(loc, &next)
	if err != nil {
		return nil, err
	}
	if isTerminalMissionStatus(updated.Status) {
		if retain < 0 {
			retain = loc.retention()
		}
		pruneTerminalMissions(loc, retain)
	}
	return updated, nil
}

func goalObject(status string) *jsObject {
	o := newObject()
	o.set("status", status)
	return o
}

func sameChildIndex(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
