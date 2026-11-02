package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Missions (src/missions/*): a durable record of the work a session drives —
// its title, objective, status, linked runs and open decisions — stored as
// JSON under the agent directory, with the original's action texts. The
// workflow children, receipts and global index belong to the workflow runtime
// this port does not carry and stay empty/absent.

const (
	missionStoreVersion = 1
	missionRunModes     = "single, parallel, chain, workflow, scheduled, external"
)

var (
	missionStatuses    = []string{"planned", "active", "waiting", "needs_decision", "completed", "failed", "cancelled"}
	missionIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	missionCloseStates = []string{"completed", "failed", "cancelled"}
)

func init() {
	registerAction("mission.create", []string{"mission", "missionStatus"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(missionAction(ctx.Cwd(), "mission.create", params))
	})
	registerAction("mission.list", []string{"missionScope"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(missionAction(ctx.Cwd(), "mission.list", params))
	})
	registerAction("mission.show", []string{"missionId", "id"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(missionAction(ctx.Cwd(), "mission.show", params))
	})
	registerAction("mission.update", []string{"missionId", "id", "missionUpdate"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(missionAction(ctx.Cwd(), "mission.update", params))
	})
	registerAction("mission.resolve-decision", []string{"missionId", "id", "summary"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(missionAction(ctx.Cwd(), "mission.resolve-decision", params))
	})
	registerAction("mission.attach-run", []string{"missionId", "id", "runId", "runMode", "dir", "agent", "runStatus"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(missionAction(ctx.Cwd(), "mission.attach-run", params))
	})
	registerAction("mission.close", []string{"missionId", "id", "missionStatus", "summary"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(missionAction(ctx.Cwd(), "mission.close", params))
	})
}

// missionDecision is one open or resolved decision inside a mission.
type missionDecision struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"` // open | resolved
	Resolution string `json:"resolution,omitempty"`
}

// missionRunLink is one run attached to a mission.
type missionRunLink struct {
	RunID     string `json:"runId"`
	Mode      string `json:"mode"`
	Status    string `json:"status,omitempty"`
	Agent     string `json:"agent,omitempty"`
	AsyncDir  string `json:"asyncDir,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
}

// missionRecord is the persisted mission (the original's MissionRecord subset).
type missionRecord struct {
	SchemaVersion int               `json:"schemaVersion"`
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	Objective     string            `json:"objective"`
	Status        string            `json:"status"`
	CreatedAt     string            `json:"createdAt"`
	UpdatedAt     string            `json:"updatedAt"`
	Cwd           string            `json:"cwd"`
	Summary       string            `json:"summary,omitempty"`
	Labels        []string          `json:"labels,omitempty"`
	Runs          []missionRunLink  `json:"runs"`
	Decisions     []missionDecision `json:"decisions"`
	Artifacts     []any             `json:"artifacts"`
	Receipts      []any             `json:"receipts"`
}

// missionDir is the project's mission store (the original's
// projectMissionDirectory: a hash of the project root under the agent dir).
func missionDir(cwd string) string {
	sum := sha256.Sum256([]byte(cwd))
	return filepath.Join(agentDir(), "missions", "projects", hex.EncodeToString(sum[:]))
}

func missionIDValid(id string) bool {
	return missionIDPattern.MatchString(id) && !strings.Contains(id, "..")
}

// missionTime is the original's ISO timestamp format.
func missionTime(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

// formatMission is the original's formatMission for the port's fields.
func formatMission(r *missionRecord) string {
	lines := []string{
		"Mission: " + r.ID,
		"Title: " + r.Title,
		"Status: " + r.Status,
		"Objective: " + r.Objective,
		"Updated: " + r.UpdatedAt,
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
			line := fmt.Sprintf("  %s (%s", run.RunID, run.Mode)
			if run.Status != "" {
				line += ", " + run.Status
			}
			line += ")"
			if run.AsyncDir != "" {
				line += " — " + run.AsyncDir
			}
			lines = append(lines, line)
		}
	}
	if len(r.Decisions) > 0 {
		lines = append(lines, "Decisions:")
		for _, d := range r.Decisions {
			line := fmt.Sprintf("  %s: %s — %s", d.ID, d.Status, d.Title)
			if d.Resolution != "" {
				line += "; resolution: " + d.Resolution
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// writeMission persists one record.
func writeMission(dir string, r *missionRecord) string {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err.Error()
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err.Error()
	}
	if err := os.WriteFile(filepath.Join(dir, r.ID+".json"), data, 0o644); err != nil {
		return err.Error()
	}
	return ""
}

// readMission loads one record.
func readMission(dir, id string) (*missionRecord, string) {
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, fmt.Sprintf("Mission '%s' not found.", id)
	}
	var r missionRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Sprintf("Mission record '%s' is invalid.", id)
	}
	return &r, ""
}

// listMissions reads every mission of the store, oldest first.
func listMissions(dir string) []*missionRecord {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*missionRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if r, errMsg := readMission(dir, strings.TrimSuffix(e.Name(), ".json")); errMsg == "" {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// missionAction dispatches the original's mission actions.
func missionAction(cwd, action string, params map[string]any) manageResult {
	dir := missionDir(cwd)
	switch action {
	case "mission.create":
		launch, errMsg := validateMissionLaunch(params["mission"])
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		status := "planned"
		if v, present := params["missionStatus"]; present {
			s, ok := v.(string)
			if !ok || !containsString(missionStatuses, s) {
				return manageResult{fmt.Sprintf("missionStatus must be one of %s", strings.Join(missionStatuses, ", ")), true}
			}
			status = s
		}
		now := missionTime(nowMs())
		record := &missionRecord{
			SchemaVersion: missionStoreVersion, ID: newRunID(), Title: launch.title,
			Objective: launch.objective, Status: status, CreatedAt: now, UpdatedAt: now,
			Cwd: cwd, Labels: launch.labels, Runs: []missionRunLink{}, Decisions: []missionDecision{},
			Artifacts: []any{}, Receipts: []any{},
		}
		if launch.summary != "" {
			record.Summary = launch.summary
		}
		if errMsg := writeMission(dir, record); errMsg != "" {
			return manageResult{errMsg, true}
		}
		return manageResult{text: fmt.Sprintf("Created mission %s: %s", record.ID, record.Title)}
	case "mission.list":
		scope, _ := params["missionScope"].(string)
		if scope == "global" {
			// The port maintains no cross-project index (its workflows do not run).
			return manageResult{text: "No indexed missions."}
		}
		if scope != "" && scope != "project" {
			return manageResult{"missionScope must be \"project\" or \"global\"", true}
		}
		records := listMissions(dir)
		if len(records) == 0 {
			return manageResult{text: "No project missions."}
		}
		lines := make([]string, 0, len(records))
		for _, r := range records {
			line := fmt.Sprintf("%s  %s  %s  %s", r.ID, r.Status, r.Title, r.UpdatedAt)
			if len(r.Decisions) > 0 {
				open := 0
				for _, d := range r.Decisions {
					if d.Status == "open" {
						open++
					}
				}
				line += fmt.Sprintf("  decisions: %d open, %d resolved", open, len(r.Decisions)-open)
			}
			lines = append(lines, line)
		}
		return manageResult{text: strings.Join(lines, "\n")}
	case "mission.show":
		id, errMsg := requireMissionID(params)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		record, errMsg := readMission(dir, id)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		return manageResult{text: formatMission(record) + "\nState: " + filepath.Join(dir, "state.json")}
	case "mission.update":
		id, errMsg := requireMissionID(params)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		record, errMsg := readMission(dir, id)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		update, errMsg := validateMissionUpdate(params["missionUpdate"])
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		applyMissionUpdate(record, update)
		record.UpdatedAt = missionTime(nowMs())
		if errMsg := writeMission(dir, record); errMsg != "" {
			return manageResult{errMsg, true}
		}
		return manageResult{text: fmt.Sprintf("Updated mission %s.\n\n%s", record.ID, formatMission(record))}
	case "mission.resolve-decision":
		id, errMsg := requireMissionID(params)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		decisionID, _ := params["id"].(string)
		if !missionIDValid(jsTrim(decisionID)) {
			return manageResult{"id must contain only letters, numbers, '.', '_', or '-' and cannot contain '..'", true}
		}
		summary, _ := params["summary"].(string)
		if jsTrim(summary) == "" {
			return manageResult{"mission.resolve-decision requires a non-empty summary", true}
		}
		record, errMsg := readMission(dir, id)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		for i := range record.Decisions {
			if record.Decisions[i].ID == jsTrim(decisionID) {
				record.Decisions[i].Status = "resolved"
				record.Decisions[i].Resolution = jsTrim(summary)
			}
		}
		record.UpdatedAt = missionTime(nowMs())
		if errMsg := writeMission(dir, record); errMsg != "" {
			return manageResult{errMsg, true}
		}
		return manageResult{text: fmt.Sprintf("Resolved decision %s for mission %s.\n\n%s", jsTrim(decisionID), record.ID, formatMission(record))}
	case "mission.attach-run":
		id, errMsg := requireMissionID(params)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		runID := strParam(params, "runId")
		if jsTrim(runID) == "" {
			runID = strParam(params, "id")
		}
		if jsTrim(runID) == "" {
			return manageResult{"mission.attach-run requires runId or id", true}
		}
		mode := strParam(params, "runMode")
		if mode == "" {
			mode = "external"
		}
		if !containsString(strings.Split(missionRunModes, ", "), mode) {
			return manageResult{"runMode is invalid", true}
		}
		dirParam := strParam(params, "dir")
		if present := params["dir"]; present != nil && jsTrim(dirParam) == "" {
			return manageResult{"dir must be a non-empty string", true}
		}
		runStatus := strParam(params, "runStatus")
		if present := params["runStatus"]; present != nil && jsTrim(runStatus) == "" {
			return manageResult{"runStatus must be a non-empty string", true}
		}
		record, errMsg := readMission(dir, id)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		record.Status = "active"
		record.Runs = append(record.Runs, missionRunLink{
			RunID: jsTrim(runID), Mode: mode, Status: jsTrim(runStatus),
			Agent: strParam(params, "agent"), AsyncDir: jsTrim(dirParam), StartedAt: missionTime(nowMs()),
		})
		record.UpdatedAt = missionTime(nowMs())
		if errMsg := writeMission(dir, record); errMsg != "" {
			return manageResult{errMsg, true}
		}
		return manageResult{text: fmt.Sprintf("Attached run %s to mission %s.", jsTrim(runID), record.ID)}
	case "mission.close":
		id, errMsg := requireMissionID(params)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		status := strParam(params, "missionStatus")
		if status == "" {
			status = "completed"
		}
		if !containsString(missionCloseStates, status) {
			return manageResult{"mission.close missionStatus must be completed, failed, or cancelled", true}
		}
		summary, _ := params["summary"].(string)
		if present := params["summary"]; present != nil && jsTrim(summary) == "" {
			return manageResult{"summary must be a non-empty string", true}
		}
		record, errMsg := readMission(dir, id)
		if errMsg != "" {
			return manageResult{errMsg, true}
		}
		record.Status = status
		if jsTrim(summary) != "" {
			record.Summary = jsTrim(summary)
		}
		record.UpdatedAt = missionTime(nowMs())
		if errMsg := writeMission(dir, record); errMsg != "" {
			return manageResult{errMsg, true}
		}
		return manageResult{text: fmt.Sprintf("Closed mission %s as %s.", record.ID, record.Status)}
	}
	return manageResult{fmt.Sprintf("Unknown mission action: %s", action), true}
}

// requireMissionID is the original's requireMissionId.
func requireMissionID(params map[string]any) (string, string) {
	id := strParam(params, "missionId")
	if id == "" {
		id = strParam(params, "id")
	}
	id = jsTrim(id)
	if id == "" {
		return "", "missionId must be a non-empty string"
	}
	if !missionIDValid(id) {
		return "", "missionId must contain only letters, numbers, '.', '_', or '-' and cannot contain '..'"
	}
	return id, ""
}

// missionLaunch is the validated create input.
type missionLaunch struct {
	title     string
	objective string
	summary   string
	labels    []string
}

// asJSObject converts a host-decoded Go map into the jsObject the validators read.
func asJSObject(value any) (*jsObject, bool) {
	if obj, isObj := value.(*jsObject); isObj {
		return obj, true
	}
	if _, isMap := value.(map[string]any); isMap {
		if round, err := parseJSON([]byte(marshalJSON(value, ""))); err == nil {
			if obj, isObj := round.(*jsObject); isObj {
				return obj, true
			}
		}
	}
	return nil, false
}

// validateMissionLaunch is the original's validateMissionLaunch.
func validateMissionLaunch(value any) (missionLaunch, string) {
	var out missionLaunch
	obj, isObj := asJSObject(value)
	if !isObj {
		return out, "mission must be an object"
	}
	for _, key := range obj.order() {
		if key != "title" && key != "summary" && key != "objective" && key != "goal" && key != "budget" && key != "labels" {
			return out, fmt.Sprintf("mission.%s is unknown", key)
		}
	}
	title, hasTitle := obj.get("title")
	summary, hasSummary := obj.get("summary")
	if hasTitle && hasSummary {
		return out, "mission.title and mission.summary cannot both be set"
	}
	rawTitle := title
	if !hasTitle {
		rawTitle = summary
	}
	titleStr, isString := rawTitle.(string)
	if !isString || jsTrim(titleStr) == "" {
		return out, "mission.title or mission.summary must be a non-empty string"
	}
	out.title = jsTrim(titleStr)
	if s, isString := obj.str("objective"); isString {
		if jsTrim(s) == "" {
			return out, "mission.objective must be a non-empty string"
		}
		out.objective = jsTrim(s)
	} else if _, present := obj.get("objective"); present {
		return out, "mission.objective must be a non-empty string"
	} else {
		out.objective = out.title
	}
	if goal, present := obj.get("goal"); present {
		if b, isBool := goal.(bool); !isBool || !b {
			return out, "mission.goal must be true when supplied"
		}
	}
	budget, hasBudget := obj.get("budget")
	if hasBudget {
		budgetObj, isObj := budget.(*jsObject)
		tokens, isNum := 0.0, false
		if isObj {
			rawTokens, present := budgetObj.get("tokens")
			tokens, isNum = rawTokens.(float64)
			_ = present
		}
		if !isObj || !isNum || tokens != float64(int(tokens)) || tokens < 1 {
			return out, "mission.budget.tokens must be a positive integer"
		}
	}
	if goal, present := obj.get("goal"); present {
		if b, isBool := goal.(bool); isBool && b && !hasBudget {
			return out, "mission.budget is required when mission.goal is true"
		}
	}
	if labels, present := obj.get("labels"); present {
		items, isArr := labels.([]any)
		if !isArr {
			return out, "mission.labels must contain only non-empty strings"
		}
		for _, item := range items {
			s, isString := item.(string)
			if !isString || jsTrim(s) == "" {
				return out, "mission.labels must contain only non-empty strings"
			}
			out.labels = append(out.labels, jsTrim(s))
		}
	}
	if s, isString := obj.str("summary"); isString {
		out.summary = jsTrim(s)
	}
	return out, ""
}

// missionUpdate is a validated mission.update patch.
type missionUpdate struct {
	title, objective, status, summary string
	setSummary                        bool
	labels                            []string
	setLabels                         bool
	addDecisions                      []missionDecision
}

// validateMissionUpdate is the original's validateMissionUpdate (subset).
func validateMissionUpdate(value any) (missionUpdate, string) {
	var out missionUpdate
	obj, isObj := asJSObject(value)
	if !isObj {
		return out, "missionUpdate must be an object"
	}
	if s, present := obj.str("title"); present {
		if jsTrim(s) == "" {
			return out, "missionUpdate.title must be a non-empty string"
		}
		out.title = jsTrim(s)
	}
	if s, present := obj.str("objective"); present {
		if jsTrim(s) == "" {
			return out, "missionUpdate.objective must be a non-empty string"
		}
		out.objective = jsTrim(s)
	}
	if s, present := obj.str("status"); present {
		if !containsString(missionStatuses, s) {
			return out, fmt.Sprintf("missionStatus must be one of %s", strings.Join(missionStatuses, ", "))
		}
		out.status = s
	}
	if s, present := obj.str("summary"); present {
		out.summary = jsTrim(s)
		out.setSummary = true
	}
	if labels, present := obj.get("labels"); present {
		items, isArr := labels.([]any)
		if !isArr {
			return out, "missionUpdate.labels must contain only non-empty strings"
		}
		for _, item := range items {
			s, isString := item.(string)
			if !isString || jsTrim(s) == "" {
				return out, "missionUpdate.labels must contain only non-empty strings"
			}
			out.labels = append(out.labels, jsTrim(s))
		}
		out.setLabels = true
	}
	if decisions, present := obj.get("decisions"); present {
		items, isArr := decisions.([]any)
		if !isArr {
			return out, "missionUpdate.decisions must be an array"
		}
		for _, item := range items {
			d, isObj := item.(*jsObject)
			if !isObj {
				return out, "missionUpdate.decisions entries must be objects"
			}
			id, _ := d.str("id")
			title, _ := d.str("title")
			if !missionIDValid(jsTrim(id)) || jsTrim(title) == "" {
				return out, "missionUpdate.decisions need a valid id and a non-empty title"
			}
			out.addDecisions = append(out.addDecisions, missionDecision{ID: jsTrim(id), Title: jsTrim(title), Status: "open"})
		}
	}
	return out, ""
}

// applyMissionUpdate patches a record, as the original's updateMission does.
func applyMissionUpdate(r *missionRecord, u missionUpdate) {
	if u.title != "" {
		r.Title = u.title
	}
	if u.objective != "" {
		r.Objective = u.objective
	}
	if u.status != "" {
		r.Status = u.status
	}
	if u.setSummary {
		r.Summary = u.summary
	}
	if u.setLabels {
		r.Labels = u.labels
	}
	r.Decisions = append(r.Decisions, u.addDecisions...)
}

func containsString(list []string, value string) bool {
	for _, s := range list {
		if s == value {
			return true
		}
	}
	return false
}
