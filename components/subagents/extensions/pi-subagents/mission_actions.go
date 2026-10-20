package pi_subagents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Port of src/missions/actions.ts: the `mission.*` actions of the subagent tool, the shape validators for the `mission`
// launch parameter and `missionUpdate`, and the text a mission is shown as.

// MissionActions are the original's MISSION_ACTIONS.
var MissionActions = []string{"mission.create", "mission.list", "mission.show", "mission.update", "mission.resolve-decision", "mission.attach-run", "mission.close"}

// MissionLaunchInput is a validated `mission` launch parameter.
type MissionLaunchInput struct {
	Title     string
	Objective string // "": absent
	Goal      bool
	Budget    *MissionTokenBudget
	Labels    []string // nil: absent
}

// MissionActionContext is what an action runs in.
type MissionActionContext struct {
	Cwd              string
	CurrentSessionID string
	Config           *MissionStoreConfig
	AgentDir         string
}

// MissionActionResult is a tool result: the text and the structured details.
type MissionActionResult struct {
	Text    string
	Details map[string]any
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	}
	return true
}

func validateMissionStatus(v any, label string) (string, error) { return missionStatusOf(v, label) }

func missionNonBlank(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && jsTrim(s) != ""
}

func stringsAllNonBlank(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		s, ok := missionNonBlank(e)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

// budgetTokens reads {tokens: positive safe integer}.
func budgetTokens(v any) (int64, bool) {
	o, ok := v.(*jsObject)
	if !ok {
		return 0, false
	}
	n, ok := safeInteger(o.vals["tokens"])
	return n, ok && n >= 1
}

// validateMissionLaunch is validateMissionLaunch.
func validateMissionLaunch(value any) (MissionLaunchInput, error) {
	var out MissionLaunchInput
	input, ok := value.(*jsObject)
	if !ok {
		return out, errors.New("mission must be an object")
	}
	for _, k := range input.order() {
		switch k {
		case "title", "summary", "objective", "goal", "budget", "labels":
		default:
			return out, fmt.Errorf("mission.%s is unknown", k)
		}
	}
	titleV, hasTitle := dynGet(input, "title")
	summaryV, hasSummary := dynGet(input, "summary")
	if hasTitle && hasSummary {
		return out, errors.New("mission.title and mission.summary cannot both be set")
	}
	candidate := titleV
	if candidate == nil {
		candidate = summaryV
	}
	title, ok := missionNonBlank(candidate)
	if !ok {
		return out, errors.New("mission.title or mission.summary must be a non-empty string")
	}
	objectiveV, hasObjective := dynGet(input, "objective")
	if hasObjective {
		if _, ok := missionNonBlank(objectiveV); !ok {
			return out, errors.New("mission.objective must be a non-empty string")
		}
	}
	goalV, hasGoal := dynGet(input, "goal")
	if hasGoal && goalV != true {
		return out, errors.New("mission.goal must be true when supplied")
	}
	budgetV, hasBudget := dynGet(input, "budget")
	var tokens int64
	if hasBudget {
		if tokens, ok = budgetTokens(budgetV); !ok {
			return out, errors.New("mission.budget.tokens must be a positive integer")
		}
	}
	if goalV == true && !hasBudget {
		return out, errors.New("mission.budget is required when mission.goal is true")
	}
	labelsV, hasLabels := dynGet(input, "labels")
	var labels []string
	if hasLabels {
		if labels, ok = stringsAllNonBlank(labelsV); !ok {
			return out, errors.New("mission.labels must contain only non-empty strings")
		}
	}
	out.Title = jsTrim(title)
	if s, isString := objectiveV.(string); isString {
		out.Objective = jsTrim(s)
	}
	out.Goal = goalV == true
	if hasBudget {
		out.Budget = &MissionTokenBudget{Tokens: tokens}
	}
	if hasLabels {
		out.Labels = make([]string, len(labels))
		for i, l := range labels {
			out.Labels[i] = jsTrim(l)
		}
	}
	return out, nil
}

func validateMissionArtifactInput(v any, index int) (MissionArtifact, error) {
	var a MissionArtifact
	input, ok := v.(*jsObject)
	if !ok {
		return a, fmt.Errorf("missionUpdate.artifacts[%d] must be an object", index)
	}
	kind, _ := input.vals["kind"].(string)
	if !inList(missionArtifactKinds, kind) {
		return a, fmt.Errorf("missionUpdate.artifacts[%d].kind is invalid", index)
	}
	path, ok := missionNonBlank(input.vals["path"])
	if !ok {
		return a, fmt.Errorf("missionUpdate.artifacts[%d].path must be a non-empty string", index)
	}
	a.Kind, a.Path = kind, path
	if d, has := dynGet(input, "description"); has {
		s, ok := missionNonBlank(d)
		if !ok {
			return a, fmt.Errorf("missionUpdate.artifacts[%d].description must be a non-empty string", index)
		}
		a.Description = s
	}
	return a, nil
}

func validateMissionReceiptInput(v any, index int) (MissionReceipt, error) {
	var r MissionReceipt
	input, ok := v.(*jsObject)
	if !ok {
		return r, fmt.Errorf("missionUpdate.receipts[%d] must be an object", index)
	}
	for _, k := range input.order() {
		switch k {
		case "kind", "status", "title", "url", "description":
		default:
			return r, fmt.Errorf("missionUpdate.receipts[%d].%s is unknown", index, k)
		}
	}
	kind, _ := input.vals["kind"].(string)
	if !inList(missionReceiptKinds, kind) {
		return r, fmt.Errorf("missionUpdate.receipts[%d].kind is invalid", index)
	}
	status, _ := input.vals["status"].(string)
	if !inList(missionReceiptStates, status) {
		return r, fmt.Errorf("missionUpdate.receipts[%d].status is invalid", index)
	}
	title, ok := missionNonBlank(input.vals["title"])
	if !ok {
		return r, fmt.Errorf("missionUpdate.receipts[%d].title must be a non-empty string", index)
	}
	rawURL, ok := missionNonBlank(input.vals["url"])
	if !ok {
		return r, fmt.Errorf("missionUpdate.receipts[%d].url must be a non-empty string", index)
	}
	if !isAbsoluteURL(rawURL) {
		return r, fmt.Errorf("missionUpdate.receipts[%d].url must be an absolute URL", index)
	}
	r.Kind, r.Status, r.Title, r.URL = kind, status, jsTrim(title), jsTrim(rawURL)
	if d, has := dynGet(input, "description"); has {
		s, ok := missionNonBlank(d)
		if !ok {
			return r, fmt.Errorf("missionUpdate.receipts[%d].description must be a non-empty string", index)
		}
		r.Description = jsTrim(s)
	}
	return r, nil
}

// validateMissionUpdate is validateMissionUpdate (the tool's `missionUpdate` patch).
func validateMissionUpdate(value any) (MissionUpdateInput, error) {
	var update MissionUpdateInput
	input, ok := value.(*jsObject)
	if !ok {
		return update, errors.New("missionUpdate must be an object")
	}
	for _, k := range input.order() {
		switch k {
		case "title", "objective", "goal", "budget", "status", "summary", "labels", "artifacts", "receipts", "decisions":
		default:
			return update, fmt.Errorf("missionUpdate.%s is unknown", k)
		}
	}
	set := false
	for _, field := range []string{"title", "objective", "summary"} {
		candidate, has := dynGet(input, field)
		if !has {
			continue
		}
		s, ok := missionNonBlank(candidate)
		if !ok {
			return update, fmt.Errorf("missionUpdate.%s must be a non-empty string", field)
		}
		trimmed := jsTrim(s)
		switch field {
		case "title":
			update.Title = &trimmed
		case "objective":
			update.Objective = &trimmed
		default:
			update.Summary = &trimmed
		}
		set = true
	}
	if goal, has := dynGet(input, "goal"); has {
		switch g := goal.(type) {
		case bool:
			if g {
				update.Goal = &MissionGoalUpdate{Status: "active"}
			} else {
				update.Goal = &MissionGoalUpdate{Off: true}
			}
		default:
			o, isObj := goal.(*jsObject)
			paused, isBool := false, false
			if isObj {
				paused, isBool = o.vals["paused"].(bool)
			}
			if !isObj || !isBool || len(o.order()) != 1 {
				return update, errors.New("missionUpdate.goal must be boolean or { paused: boolean }")
			}
			status := "active"
			if paused {
				status = "paused"
			}
			update.Goal = &MissionGoalUpdate{Status: status}
		}
		set = true
	}
	if budget, has := dynGet(input, "budget"); has {
		n, ok := budgetTokens(budget)
		if !ok {
			return update, errors.New("missionUpdate.budget.tokens must be a positive integer")
		}
		update.Budget = &MissionTokenBudget{Tokens: n}
		set = true
	}
	if status, has := dynGet(input, "status"); has {
		s, err := validateMissionStatus(status, "missionUpdate.status")
		if err != nil {
			return update, err
		}
		update.Status = s
		set = true
	}
	if labels, has := dynGet(input, "labels"); has {
		l, ok := stringsAllNonBlank(labels)
		if !ok {
			return update, errors.New("missionUpdate.labels must contain only non-empty strings")
		}
		update.Labels = &l
		set = true
	}
	if artifacts, has := dynGet(input, "artifacts"); has {
		arr, ok := artifacts.([]any)
		if !ok {
			return update, errors.New("missionUpdate.artifacts must be an array")
		}
		update.AddArtifacts = []MissionArtifact{}
		for i, item := range arr {
			a, err := validateMissionArtifactInput(item, i)
			if err != nil {
				return update, err
			}
			update.AddArtifacts = append(update.AddArtifacts, a)
		}
		set = true
	}
	if receipts, has := dynGet(input, "receipts"); has {
		arr, ok := receipts.([]any)
		if !ok {
			return update, errors.New("missionUpdate.receipts must be an array")
		}
		update.AddReceipts = []MissionReceipt{}
		for i, item := range arr {
			r, err := validateMissionReceiptInput(item, i)
			if err != nil {
				return update, err
			}
			update.AddReceipts = append(update.AddReceipts, r)
		}
		set = true
	}
	if decisions, has := dynGet(input, "decisions"); has {
		arr, ok := decisions.([]any)
		if !ok {
			return update, errors.New("missionUpdate.decisions must be an array")
		}
		update.AddDecisions = []MissionDecisionInput{}
		for i, item := range arr {
			d, ok := item.(*jsObject)
			if !ok {
				return update, fmt.Errorf("missionUpdate.decisions[%d] must be an object", i)
			}
			title, ok := missionNonBlank(d.vals["title"])
			if !ok {
				return update, fmt.Errorf("missionUpdate.decisions[%d].title must be a non-empty string", i)
			}
			in := MissionDecisionInput{Title: jsTrim(title)}
			if options, has := dynGet(d, "options"); has {
				if _, ok := stringsAllNonBlank(options); !ok {
					return update, fmt.Errorf("missionUpdate.decisions[%d].options must contain only non-empty strings", i)
				}
			}
			if _, ok := missionNonBlank(d.vals["prompt"]); ok {
				in.Prompt = d.vals["prompt"].(string)
			}
			if arr, ok := d.vals["options"].([]any); ok {
				in.Options = make([]string, len(arr))
				for j, o := range arr {
					in.Options[j] = o.(string)
				}
			}
			if _, ok := missionNonBlank(d.vals["recommendation"]); ok {
				in.Recommendation = d.vals["recommendation"].(string)
			}
			update.AddDecisions = append(update.AddDecisions, in)
		}
		set = true
	}
	if !set {
		return update, errors.New("missionUpdate must include at least one supported field")
	}
	return update, nil
}

// refreshLinkedRunStatus reads the status files of the runs a mission links and records what changed.
func refreshLinkedRunStatus(loc MissionStoreLocation, record *MissionRecord) (*MissionRecord, []string, error) {
	warnings := []string{}
	var updates []MissionRunLink
	for _, run := range record.Runs {
		if run.AsyncDir == "" {
			continue
		}
		statusPath := filepath.Join(run.AsyncDir, "status.json")
		raw, err := os.ReadFile(statusPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("Failed to read linked run status '%s': %s", statusPath, err.Error()))
			continue
		}
		status, msg := missionParseJSON(string(raw))
		if msg != "" {
			warnings = append(warnings, fmt.Sprintf("Failed to read linked run status '%s': %s", statusPath, msg))
			continue
		}
		o, ok := status.(*jsObject)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("Linked run status '%s' must be an object", statusPath))
			continue
		}
		state, ok := missionNonBlank(o.vals["state"])
		if !ok {
			warnings = append(warnings, fmt.Sprintf("Linked run status '%s' is missing state", statusPath))
			continue
		}
		if state == run.Status {
			continue
		}
		next := run
		next.Status = state
		if state != "queued" && state != "running" && run.CompletedAt == "" {
			next.CompletedAt = isoMillis(time.Now())
		}
		updates = append(updates, next)
	}
	if len(updates) == 0 {
		return record, warnings, nil
	}
	states := make([]string, len(record.Runs))
	for i, run := range record.Runs {
		merged := run
		for _, u := range updates {
			if u.RunID == run.RunID && sameChildIndex(u.ChildIndex, run.ChildIndex) {
				merged = u
				break
			}
		}
		states[i] = merged.Status
	}
	anyOf := func(want ...string) bool {
		for _, s := range states {
			for _, w := range want {
				if s == w {
					return true
				}
			}
		}
		return false
	}
	allComplete := len(states) > 0
	for _, s := range states {
		allComplete = allComplete && (s == "complete" || s == "completed")
	}
	status := record.Status
	switch {
	case isTerminalMissionStatus(record.Status):
	case anyOf("queued", "running", "active"):
		status = "active"
	case anyOf("paused"):
		status = "waiting"
	case anyOf("failed"):
		status = "failed"
	case anyOf("stopped", "rejected", "cancelled"):
		status = "cancelled"
	case allComplete:
		status = "completed"
	}
	updated, err := updateMission(loc, record.ID, MissionUpdateInput{Status: status, AddRuns: updates}, time.Now(), -1)
	return updated, warnings, err
}

func formatMission(r *MissionRecord) string {
	lines := []string{"Mission: " + r.ID, "Title: " + r.Title, "Status: " + r.Status, "Objective: " + r.Objective, "Updated: " + r.UpdatedAt}
	if r.Goal != nil && r.Budget != nil {
		used := int64(0)
		if r.Usage != nil {
			used = r.Usage.Tokens
		}
		lines = append(lines, "Goal mode: "+r.Goal.Status, fmt.Sprintf("Budget: %d/%d tokens", used, r.Budget.Tokens))
	}
	if r.Summary != "" {
		lines = append(lines, "Summary: "+r.Summary)
	}
	if len(r.Labels) > 0 {
		lines = append(lines, "Labels: "+strings.Join(r.Labels, ", "))
	}
	if len(r.Runs) > 0 {
		lines = append(lines, "Runs:")
		for _, run := range r.Runs {
			status := ""
			if run.Status != "" {
				status = ", " + run.Status
			}
			dir := ""
			if run.AsyncDir != "" {
				dir = " — " + run.AsyncDir
			}
			lines = append(lines, fmt.Sprintf("  %s (%s%s)%s", run.RunID, run.Mode, status, dir))
		}
	}
	if len(r.WorkflowChildren) > 0 {
		lines = append(lines, "Workflow children:")
		for _, c := range r.WorkflowChildren {
			identity := c.Key
			if c.RunID != "" {
				identity = c.Key + " (" + c.RunID + ")"
			}
			heartbeat := ""
			if c.Heartbeat != nil {
				hs := c.Heartbeat.Status
				if hs == "" {
					hs = c.Status
				}
				phase := ""
				if c.Heartbeat.Phase != "" {
					phase = "/" + c.Heartbeat.Phase
				}
				heartbeat = "; heartbeat " + hs + phase + " at " + c.Heartbeat.UpdatedAt
			}
			recovery := ""
			if c.SessionPath != "" {
				recovery = "; session " + c.SessionPath
			} else if len(c.ArtifactPaths) > 0 {
				recovery = fmt.Sprintf("; artifacts %d", len(c.ArtifactPaths))
			}
			agent, phase := "", ""
			if c.Agent != "" {
				agent = " — " + c.Agent
			}
			if c.Phase != "" {
				phase = " [" + c.Phase + "]"
			}
			lines = append(lines, fmt.Sprintf("  %s: %s%s%s; updated %s%s%s", identity, c.Status, agent, phase, c.UpdatedAt, heartbeat, recovery))
		}
	}
	if len(r.Decisions) > 0 {
		lines = append(lines, "Decisions:")
		for _, d := range r.Decisions {
			resolution := ""
			if d.Resolution != "" {
				resolution = "; resolution: " + d.Resolution
			}
			lines = append(lines, fmt.Sprintf("  %s: %s — %s%s", d.ID, d.Status, d.Title, resolution))
		}
	}
	if len(r.Artifacts) > 0 {
		lines = append(lines, "Artifacts:")
		for _, a := range r.Artifacts {
			lines = append(lines, "  "+a.Kind+": "+a.Path)
		}
	}
	if len(r.Receipts) > 0 {
		lines = append(lines, "Delivery receipts:")
		for _, rc := range r.Receipts {
			lines = append(lines, fmt.Sprintf("  %s (%s): %s — %s", rc.Kind, rc.Status, rc.Title, rc.URL))
		}
	}
	return strings.Join(lines, "\n")
}

func missionResultDetails(loc MissionStoreLocation, record *MissionRecord) map[string]any {
	return map[string]any{"mode": "management", "results": []any{}, "missionId": record.ID, "missionPath": mustRecordPath(loc, record.ID), "mission": record}
}

func requireMissionID(params *jsObject) (string, error) {
	return validateMissionID(params.vals["missionId"], "missionId")
}

func paramString(params *jsObject, k string) (string, bool) {
	s, ok := params.vals[k].(string)
	return s, ok
}

// handleMissionAction is handleMissionAction.
func handleMissionAction(action string, params *jsObject, ctx MissionActionContext) (MissionActionResult, error) {
	loc := resolveMissionStoreLocation(ctx.Cwd, ctx.Config, ctx.AgentDir)
	switch action {
	case "mission.create":
		mission, err := validateMissionLaunch(params.vals["mission"])
		if err != nil {
			return MissionActionResult{}, err
		}
		status := "planned"
		if truthy(params.vals["missionStatus"]) {
			if status, err = validateMissionStatus(params.vals["missionStatus"], "missionStatus"); err != nil {
				return MissionActionResult{}, err
			}
		}
		objective := mission.Objective
		if objective == "" {
			objective = mission.Title
		}
		record, err := createMission(loc, MissionCreateInput{Title: mission.Title, Objective: objective, Goal: mission.Goal, Budget: mission.Budget, Status: status, Labels: mission.Labels, HasLabels: mission.Labels != nil, OwnerSessionID: ctx.CurrentSessionID}, time.Now(), -1)
		if err != nil {
			return MissionActionResult{}, err
		}
		return MissionActionResult{"Created mission " + record.ID + ": " + record.Title, missionResultDetails(loc, record)}, nil
	case "mission.list":
		scope := params.vals["missionScope"]
		if _, has := dynGet(params, "missionScope"); has && scope != "project" && scope != "global" {
			return MissionActionResult{}, errors.New(`missionScope must be "project" or "global"`)
		}
		if scope == "global" {
			listed := listGlobalMissions(loc.GlobalIndexDir)
			lines := []string{"No indexed missions."}
			if len(listed.Entries) > 0 {
				lines = lines[:0]
				for _, e := range listed.Entries {
					stale := ""
					if e.Stale {
						stale = " [stale]"
					}
					lines = append(lines, fmt.Sprintf("%s  %s%s  %s  %s", e.MissionID, e.Status, stale, e.Title, e.ProjectRoot))
				}
			}
			if len(listed.Warnings) > 0 {
				lines = append(lines, "")
				for _, w := range listed.Warnings {
					lines = append(lines, "Warning: "+w)
				}
			}
			return MissionActionResult{strings.Join(lines, "\n"), map[string]any{"mode": "management", "results": []any{}, "missions": map[string]any{"globalEntries": listed.Entries, "warnings": listed.Warnings}}}, nil
		}
		listed, err := listMissions(loc)
		if err != nil {
			return MissionActionResult{}, err
		}
		lines := []string{"No project missions."}
		if len(listed.Records) > 0 {
			lines = lines[:0]
			for _, r := range listed.Records {
				open := 0
				for _, d := range r.Decisions {
					if d.Status == "open" {
						open++
					}
				}
				decisions := ""
				if len(r.Decisions) > 0 {
					decisions = fmt.Sprintf("  decisions: %d open, %d resolved", open, len(r.Decisions)-open)
				}
				lines = append(lines, fmt.Sprintf("%s  %s  %s  %s%s", r.ID, r.Status, r.Title, r.UpdatedAt, decisions))
			}
		}
		if len(listed.Warnings) > 0 {
			lines = append(lines, "")
			for _, w := range listed.Warnings {
				lines = append(lines, "Warning: "+w)
			}
		}
		return MissionActionResult{strings.Join(lines, "\n"), map[string]any{"mode": "management", "results": []any{}, "missions": map[string]any{"records": listed.Records, "warnings": listed.Warnings}}}, nil
	case "mission.show":
		id, err := requireMissionID(params)
		if err != nil {
			return MissionActionResult{}, err
		}
		current, err := readMission(loc, id)
		if err != nil {
			return MissionActionResult{}, err
		}
		record, warnings, err := refreshLinkedRunStatus(loc, current)
		if err != nil {
			return MissionActionResult{}, err
		}
		statePath, _ := missionStatePath(loc, record.ID)
		lines := []string{formatMission(record), "State: " + statePath}
		if len(warnings) > 0 {
			lines = append(lines, "")
			for _, w := range warnings {
				lines = append(lines, "Warning: "+w)
			}
		}
		details := missionResultDetails(loc, record)
		details["missions"] = map[string]any{"warnings": warnings}
		return MissionActionResult{strings.Join(lines, "\n"), details}, nil
	case "mission.update":
		id, err := requireMissionID(params)
		if err != nil {
			return MissionActionResult{}, err
		}
		update, err := validateMissionUpdate(params.vals["missionUpdate"])
		if err != nil {
			return MissionActionResult{}, err
		}
		record, err := updateMission(loc, id, update, time.Now(), -1)
		if err != nil {
			return MissionActionResult{}, err
		}
		return MissionActionResult{"Updated mission " + record.ID + ".\n\n" + formatMission(record), missionResultDetails(loc, record)}, nil
	case "mission.resolve-decision":
		id, err := requireMissionID(params)
		if err != nil {
			return MissionActionResult{}, err
		}
		decisionID, err := validateMissionID(params.vals["id"], "id")
		if err != nil {
			return MissionActionResult{}, err
		}
		summary, ok := missionNonBlank(params.vals["summary"])
		if !ok {
			return MissionActionResult{}, errors.New("mission.resolve-decision requires a non-empty summary")
		}
		record, err := updateMission(loc, id, MissionUpdateInput{ResolveDecision: &MissionResolveDecision{ID: decisionID, Resolution: jsTrim(summary)}}, time.Now(), -1)
		if err != nil {
			return MissionActionResult{}, err
		}
		return MissionActionResult{fmt.Sprintf("Resolved decision %s for mission %s.\n\n%s", decisionID, record.ID, formatMission(record)), missionResultDetails(loc, record)}, nil
	case "mission.attach-run":
		id, err := requireMissionID(params)
		if err != nil {
			return MissionActionResult{}, err
		}
		runIDV := params.vals["runId"]
		if runIDV == nil {
			runIDV = params.vals["id"]
		}
		runID, ok := missionNonBlank(runIDV)
		if !ok {
			return MissionActionResult{}, errors.New("mission.attach-run requires runId or id")
		}
		mode := "external"
		if v := params.vals["runMode"]; v != nil {
			s, _ := v.(string)
			if !inList(missionRunModes, s) {
				return MissionActionResult{}, errors.New("runMode is invalid")
			}
			mode = s
		}
		dir, hasDir := dynGet(params, "dir")
		if _, ok := missionNonBlank(dir); hasDir && !ok {
			return MissionActionResult{}, errors.New("dir must be a non-empty string")
		}
		runStatus, hasStatus := dynGet(params, "runStatus")
		if _, ok := missionNonBlank(runStatus); hasStatus && !ok {
			return MissionActionResult{}, errors.New("runStatus must be a non-empty string")
		}
		link := MissionRunLink{RunID: jsTrim(runID), Mode: mode, StartedAt: isoMillis(time.Now())}
		link.AsyncDir, _ = dir.(string)
		link.Agent, _ = params.vals["agent"].(string)
		link.Status, _ = runStatus.(string)
		record, err := updateMission(loc, id, MissionUpdateInput{Status: "active", AddRuns: []MissionRunLink{link}}, time.Now(), -1)
		if err != nil {
			return MissionActionResult{}, err
		}
		return MissionActionResult{fmt.Sprintf("Attached run %s to mission %s.", runID, record.ID), missionResultDetails(loc, record)}, nil
	}
	id, err := requireMissionID(params)
	if err != nil {
		return MissionActionResult{}, err
	}
	status := "completed"
	if v := params.vals["missionStatus"]; v != nil {
		status, _ = v.(string)
	}
	if status != "completed" && status != "failed" && status != "cancelled" {
		return MissionActionResult{}, errors.New("mission.close missionStatus must be completed, failed, or cancelled")
	}
	update := MissionUpdateInput{Status: status}
	if summaryV, has := dynGet(params, "summary"); has {
		s, ok := missionNonBlank(summaryV)
		if !ok {
			return MissionActionResult{}, errors.New("summary must be a non-empty string")
		}
		trimmed := jsTrim(s)
		update.Summary = &trimmed
	}
	record, err := updateMission(loc, id, update, time.Now(), -1)
	if err != nil {
		return MissionActionResult{}, err
	}
	return MissionActionResult{fmt.Sprintf("Closed mission %s as %s.", record.ID, record.Status), missionResultDetails(loc, record)}, nil
}
