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

// Port of src/missions/lifecycle.ts: binding a launch to a mission, recording its result, and completing a mission from the
// result of a run that finished later (an async run's result file).

// MissionBindingFile is MISSION_BINDING_FILE.
const MissionBindingFile = "mission.json"

// promptRedacted is PROMPT_REDACTED: what a mission is titled when its objective is the user's prompt.
const promptRedacted = "[prompt redacted]"

// MissionLaunchBinding ties a launch to its mission.
type MissionLaunchBinding struct {
	MissionID         string
	Location          MissionStoreLocation
	AutoCreated       bool
	AnnounceInContent bool
}

// MissionContent is a text block of a tool result.
type MissionContent struct{ Type, Text string }

// MissionChildResult is the part of a child's result a mission records.
type MissionChildResult struct {
	Index                int
	Agent                string
	Task                 string
	ExitCode             int
	InputTokens          int64
	OutputTokens         int64
	Interrupted          bool
	Detached             bool
	OutputPath           string // artifactPaths.outputPath
	SavedOutputPath      string
	TranscriptPath       string
	StructuredOutputPath string
	Acceptance           any // nil: none
}

// MissionToolResult is the tool result of a launch, with the fields a mission reads and adds.
type MissionToolResult struct {
	Content             []MissionContent
	IsError             bool
	Mode                string
	RunID               string
	AsyncID             string
	AsyncDir            string
	Results             []MissionChildResult
	ParallelHandoffPath string
	// Set by attachMissionToLaunchResult.
	MissionID   string
	MissionPath string
	Mission     *MissionRecord
}

func objectString(o *jsObject, k string) string {
	if o == nil {
		return ""
	}
	s, _ := o.vals[k].(string)
	return s
}

func trimmedTask(v any) string {
	if o, ok := v.(*jsObject); ok {
		if s, ok := o.vals["task"].(string); ok {
			return jsTrim(s)
		}
	}
	return ""
}

// workflowObjective is the first task a launch names.
func workflowObjective(params *jsObject) string {
	if s, ok := params.vals["task"].(string); ok && jsTrim(s) != "" {
		return jsTrim(s)
	}
	tasks, _ := params.vals["tasks"].([]any)
	for _, t := range tasks {
		if s := trimmedTask(t); s != "" {
			return s
		}
	}
	chain, _ := params.vals["chain"].([]any)
	for _, step := range chain {
		if s := trimmedTask(step); s != "" {
			return s
		}
	}
	for _, step := range chain {
		o, ok := step.(*jsObject)
		if !ok {
			continue
		}
		var parallel []any
		switch p := o.vals["parallel"].(type) {
		case []any:
			parallel = p
		case nil, bool, string, float64:
			if truthy(p) {
				parallel = []any{p}
			}
		default:
			parallel = []any{p}
		}
		for _, child := range parallel {
			if s := trimmedTask(child); s != "" {
				return s
			}
		}
	}
	return ""
}

// prepareMissionLaunch is prepareMissionLaunch: the binding of a launch, nil when it has no mission. params is the decoded
// subagent tool call (missionId, mission, task, tasks, chain).
func prepareMissionLaunch(params *jsObject, projectRoot string, config *MissionStoreConfig, ownerSessionID string) (*MissionLaunchBinding, error) {
	missionIDV, hasMissionID := dynGet(params, "missionId")
	missionV, hasMission := dynGet(params, "mission")
	if hasMissionID && hasMission {
		return nil, errors.New("Use missionId or mission, not both")
	}
	if missionV == false {
		return nil, nil
	}
	objective := workflowObjective(params)
	missionsEnabled := config == nil || config.Enabled == nil || *config.Enabled
	shouldCreate := hasMission || (missionsEnabled && objective != "")
	if !hasMissionID && !shouldCreate {
		return nil, nil
	}
	loc := resolveMissionStoreLocation(projectRoot, config, "")
	if hasMissionID {
		id, err := validateMissionID(missionIDV, "missionId")
		if err != nil {
			return nil, err
		}
		if _, err := readMission(loc, id); err != nil {
			return nil, err
		}
		if _, err := updateMission(loc, id, MissionUpdateInput{Status: "active"}, time.Now(), -1); err != nil {
			return nil, err
		}
		return &MissionLaunchBinding{MissionID: id, Location: loc, AnnounceInContent: true}, nil
	}
	var mission *MissionLaunchInput
	if hasMission {
		m, err := validateMissionLaunch(missionV)
		if err != nil {
			return nil, err
		}
		mission = &m
	}
	derived, title := "", promptRedacted
	switch {
	case mission != nil && mission.Objective != "":
		derived = mission.Objective
	case mission != nil:
		derived = mission.Title
	case objective != "":
		derived = promptRedacted
	}
	if mission != nil && mission.Title != "" {
		title = mission.Title
	}
	if derived == "" {
		derived = title
	}
	in := MissionCreateInput{Title: title, Objective: derived, Status: "active", OwnerSessionID: ownerSessionID}
	if mission != nil {
		in.Goal, in.Budget = mission.Goal, mission.Budget
		in.Labels, in.HasLabels = mission.Labels, mission.Labels != nil
	}
	record, err := createMission(loc, in, time.Now(), -1)
	if err != nil {
		return nil, err
	}
	return &MissionLaunchBinding{MissionID: record.ID, Location: loc, AutoCreated: true, AnnounceInContent: true}, nil
}

func runStatusForResult(r MissionToolResult) string {
	if r.AsyncDir != "" {
		return "active"
	}
	failed := false
	for _, c := range r.Results {
		if c.Interrupted || c.Detached {
			return "paused"
		}
		failed = failed || c.ExitCode != 0
	}
	if r.IsError || failed {
		return "failed"
	}
	return "completed"
}

func isActiveRunStatus(s string) bool { return s == "queued" || s == "running" || s == "active" }

func missionStatusForRun(record *MissionRecord, runID, runStatus string) string {
	switch {
	case isTerminalMissionStatus(record.Status):
		return record.Status
	case isActiveRunStatus(runStatus):
		return "active"
	case record.Goal != nil:
		return "active"
	case runStatus == "paused":
		return "waiting"
	}
	for _, run := range record.Runs {
		if run.RunID != runID && isActiveRunStatus(run.Status) {
			return "active"
		}
	}
	switch runStatus {
	case "completed", "complete":
		return "completed"
	case "stopped", "rejected", "cancelled":
		return "cancelled"
	}
	return "failed"
}

func firstText(r MissionToolResult) string {
	for _, c := range r.Content {
		if c.Type != "text" {
			continue
		}
		text := jsTrim(c.Text)
		if text == "" {
			return ""
		}
		if utf16Units(text) > 2000 {
			return utf16Prefix(text, 1997) + "..."
		}
		return text
	}
	return ""
}

func artifactsForResult(r MissionToolResult) []MissionArtifact {
	var out []MissionArtifact
	if r.AsyncDir != "" {
		out = append(out, MissionArtifact{Kind: "status", Path: filepath.Join(r.AsyncDir, "status.json")}, MissionArtifact{Kind: "other", Path: filepath.Join(r.AsyncDir, "events.jsonl"), Description: "Lifecycle events"})
	}
	for _, c := range r.Results {
		if c.OutputPath != "" {
			out = append(out, MissionArtifact{Kind: "output", Path: c.OutputPath})
		}
		if c.SavedOutputPath != "" {
			out = append(out, MissionArtifact{Kind: "output", Path: c.SavedOutputPath})
		}
		if c.TranscriptPath != "" {
			out = append(out, MissionArtifact{Kind: "other", Path: c.TranscriptPath, Description: "Child transcript"})
		}
		if c.StructuredOutputPath != "" {
			out = append(out, MissionArtifact{Kind: "output", Path: c.StructuredOutputPath, Description: "Structured output"})
		}
	}
	if r.ParallelHandoffPath != "" {
		out = append(out, MissionArtifact{Kind: "manifest", Path: r.ParallelHandoffPath})
	}
	return out
}

func persistedBinding(b MissionLaunchBinding) *jsObject {
	o := newObject()
	o.set("schemaVersion", float64(1))
	o.set("missionId", b.MissionID)
	o.set("projectRoot", b.Location.ProjectRoot)
	o.set("missionDir", b.Location.MissionDir)
	o.set("globalIndexDir", b.Location.GlobalIndexDir)
	o.set("writeGlobalIndex", b.Location.WriteGlobalIndex)
	if b.Location.RetainTerminal != nil {
		o.set("retainTerminal", float64(*b.Location.RetainTerminal))
	}
	return o
}

// writeMissionAsyncBinding is writeMissionAsyncBinding: the file that tells the run's completion which mission to update.
func writeMissionAsyncBinding(asyncDir string, b MissionLaunchBinding) error {
	return writePrivateAtomicJSON(filepath.Join(asyncDir, MissionBindingFile), persistedBinding(b))
}

// attachMissionToLaunchResult is attachMissionToLaunchResult: it records the result of a launch in its mission and returns
// the result with the mission in its details (and, for a finished run, its id and status in the text).
func attachMissionToLaunchResult(binding MissionLaunchBinding, result MissionToolResult) (MissionToolResult, error) {
	loc := binding.Location
	runID := result.RunID
	if runID == "" {
		runID = result.AsyncID
	}
	if runID == "" {
		current, err := readMission(loc, binding.MissionID)
		if err != nil {
			return result, err
		}
		mission := current
		if result.IsError {
			active := false
			for _, run := range current.Runs {
				active = active || isActiveRunStatus(run.Status)
			}
			update := MissionUpdateInput{Status: "failed"}
			if active {
				update.Status = "active"
			}
			if text := firstText(result); text != "" {
				update.Summary = &text
			}
			if mission, err = updateMission(loc, binding.MissionID, update, time.Now(), -1); err != nil {
				return result, err
			}
		}
		result.MissionID, result.MissionPath, result.Mission = binding.MissionID, mustRecordPath(loc, binding.MissionID), mission
		return result, nil
	}
	runStatus := runStatusForResult(result)
	current, err := readMission(loc, binding.MissionID)
	if err != nil {
		return result, err
	}
	startedAt := isoMillis(time.Now())
	var tokens int64
	for _, c := range result.Results {
		tokens += c.InputTokens + c.OutputTokens
	}
	mode := result.Mode
	if mode == "management" {
		mode = "external"
	}
	run := MissionRunLink{RunID: runID, Mode: mode, Status: runStatus, StartedAt: startedAt, AsyncDir: result.AsyncDir}
	if len(result.Results) == 1 {
		run.Agent = result.Results[0].Agent
	}
	if runStatus != "active" {
		run.CompletedAt = startedAt
	}
	if tokens > 0 {
		run.Usage = &MissionTokenUsage{Tokens: tokens}
	}
	update := MissionUpdateInput{Status: missionStatusForRun(current, runID, runStatus), AddRuns: []MissionRunLink{run}, AddArtifacts: artifactsForResult(result)}
	if text := firstText(result); text != "" && runStatus != "active" {
		update.Summary = &text
	}
	if len(result.Results) == 1 && truthy(result.Results[0].Acceptance) {
		acceptance := result.Results[0].Acceptance
		update.Acceptance = &acceptance
	}
	mission, err := updateMission(loc, binding.MissionID, update, time.Now(), -1)
	if err != nil {
		return result, err
	}
	if result.AsyncDir != "" {
		if err := writeMissionAsyncBinding(result.AsyncDir, binding); err != nil {
			return result, err
		}
		statusPath := filepath.Join(result.AsyncDir, "status.json")
		if raw, rerr := os.ReadFile(statusPath); rerr == nil {
			status, msg := missionParseJSON(string(raw))
			if msg != "" {
				return result, fmt.Errorf("Failed to reconcile mission from terminal async status '%s': %s", statusPath, msg)
			}
			if o, ok := status.(*jsObject); ok {
				if state, ok := o.vals["state"].(string); ok && state != "queued" && state != "running" {
					event := newObject()
					event.set("runId", runID)
					event.set("asyncDir", result.AsyncDir)
					event.set("mode", result.Mode)
					event.set("state", state)
					if text := firstText(result); text != "" {
						event.set("summary", text)
					}
					synced, err := syncMissionFromAsyncCompletion(event)
					if err != nil {
						return result, fmt.Errorf("Failed to reconcile mission from terminal async status '%s': %s", statusPath, err.Error())
					}
					if synced != nil {
						mission = synced
					}
				}
			}
		}
	}
	last := -1
	for i, c := range result.Content {
		if c.Type == "text" {
			last = i
		}
	}
	structured := false
	for _, c := range result.Results {
		structured = structured || c.StructuredOutputPath != ""
	}
	isJSON := last >= 0 && workflowJSONError(result.Content[last].Text) == ""
	if binding.AnnounceInContent && last >= 0 && !structured && !isJSON {
		content := append([]MissionContent{}, result.Content...)
		content[last].Text += fmt.Sprintf("\nMission: %s (%s)", mission.ID, mission.Status)
		result.Content = content
	}
	result.MissionID, result.MissionPath, result.Mission = mission.ID, mustRecordPath(loc, mission.ID), mission
	return result, nil
}

func parsePersistedBinding(value any, source string) (*MissionLaunchBinding, error) {
	o, ok := value.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", source)
	}
	if v, ok := o.vals["schemaVersion"].(float64); !ok || v != 1 {
		return nil, fmt.Errorf("%s.schemaVersion must be 1", source)
	}
	for _, f := range []string{"projectRoot", "missionDir", "globalIndexDir"} {
		if _, ok := missionNonBlank(o.vals[f]); !ok {
			return nil, fmt.Errorf("%s.%s must be a non-empty string", source, f)
		}
	}
	writeIndex, ok := o.vals["writeGlobalIndex"].(bool)
	if !ok {
		return nil, fmt.Errorf("%s.writeGlobalIndex must be boolean", source)
	}
	var retain *int64
	if v, has := dynGet(o, "retainTerminal"); has {
		f, isNum := v.(float64)
		if !isNum || f != float64(int64(f)) || f < 1 {
			return nil, fmt.Errorf("%s.retainTerminal must be a positive integer", source)
		}
		n := int64(f)
		retain = &n
	}
	id, err := validateMissionID(o.vals["missionId"], source+".missionId")
	if err != nil {
		return nil, err
	}
	return &MissionLaunchBinding{MissionID: id, Location: MissionStoreLocation{
		ProjectRoot: o.vals["projectRoot"].(string), MissionDir: o.vals["missionDir"].(string), GlobalIndexDir: o.vals["globalIndexDir"].(string), WriteGlobalIndex: writeIndex, RetainTerminal: retain,
	}}, nil
}

// readMissionBinding is readMissionBinding: nil when the run has no mission.
func readMissionBinding(asyncDir string) (*MissionLaunchBinding, error) {
	bindingPath := filepath.Join(asyncDir, MissionBindingFile)
	raw, err := os.ReadFile(bindingPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	parsed, msg := missionParseJSON(string(raw))
	if msg != "" {
		return nil, errors.New(msg)
	}
	return parsePersistedBinding(parsed, bindingPath)
}

func usageFromUnknown(v any) (int64, bool) {
	o, ok := v.(*jsObject)
	if !ok {
		return 0, false
	}
	n, ok := safeInteger(o.vals["total"])
	return n, ok && n >= 0
}

// syncMissionFromAsyncCompletion is syncMissionFromAsyncCompletion: the completion event (a result file) of a run bound to
// a mission updates that mission; nil when the run has no mission or its record is gone.
func syncMissionFromAsyncCompletion(value any) (*MissionRecord, error) {
	event, ok := value.(*jsObject)
	if !ok {
		return nil, nil
	}
	asyncDir, ok := missionNonBlank(event.vals["asyncDir"])
	if !ok {
		return nil, nil
	}
	binding, err := readMissionBinding(asyncDir)
	if err != nil || binding == nil {
		return nil, err
	}
	runID, ok := event.vals["runId"].(string)
	if !ok {
		if runID, ok = event.vals["id"].(string); !ok {
			return nil, errors.New("Async mission completion is missing runId")
		}
	}
	success := event.vals["success"] == true
	runStatus, isState := event.vals["state"].(string)
	if !isState {
		runStatus = "failed"
		if success {
			runStatus = "completed"
		}
	}
	current, err := readMission(binding.Location, binding.MissionID)
	if err != nil {
		var notFound *MissionNotFoundError
		if !errors.As(err, &notFound) {
			return nil, err
		}
		recordMissingMissionSync(asyncDir, runID, binding)
		return nil, nil
	}
	completedAt := isoMillis(time.Now())
	artifacts := []MissionArtifact{
		{Kind: "status", Path: filepath.Join(asyncDir, "status.json")},
		{Kind: "other", Path: filepath.Join(asyncDir, "events.jsonl"), Description: "Lifecycle events"},
	}
	if h, ok := event.vals["parallelHandoff"].(*jsObject); ok {
		if p, ok := h.vals["path"].(string); ok {
			artifacts = append(artifacts, MissionArtifact{Kind: "manifest", Path: p})
		}
	}
	results, hasResults := event.vals["results"].([]any)
	var resultTokens int64
	for _, r := range results {
		child, ok := r.(*jsObject)
		if !ok {
			continue
		}
		if p, ok := child.vals["artifactPath"].(string); ok {
			artifacts = append(artifacts, MissionArtifact{Kind: "output", Path: p})
		}
		if ap, ok := child.vals["artifactPaths"].(*jsObject); ok {
			if p, ok := ap.vals["outputPath"].(string); ok {
				artifacts = append(artifacts, MissionArtifact{Kind: "output", Path: p})
			}
		}
		if n, ok := usageFromUnknown(child.vals["tokens"]); ok {
			resultTokens += n
		}
	}
	summary := ""
	if s, ok := event.vals["summary"].(string); ok && jsTrim(s) != "" {
		summary = utf16Prefix(s, 2000)
	}
	tokens, hasTokens := usageFromUnknown(event.vals["totalTokens"])
	if !hasTokens && hasResults {
		tokens, hasTokens = resultTokens, true
	}
	workflowRunID, _ := missionNonBlank(event.vals["parentWorkflowRunId"])
	workflowKey, _ := missionNonBlank(event.vals["workflowKey"])
	childStatus := "failed"
	switch {
	case runStatus == "complete" || runStatus == "completed" || success:
		childStatus = "completed"
	case runStatus == "paused":
		childStatus = "paused"
	case runStatus == "stopped":
		childStatus = "stopped"
	}
	mode := "external"
	if m, ok := event.vals["mode"].(string); ok && inList(missionRunModes[:4], m) {
		mode = m
	}
	run := MissionRunLink{RunID: runID, Mode: mode, AsyncDir: asyncDir, Status: runStatus, CompletedAt: completedAt}
	if hasTokens && tokens > 0 {
		run.Usage = &MissionTokenUsage{Tokens: tokens}
	}
	update := MissionUpdateInput{Status: missionStatusForRun(current, runID, runStatus), AddRuns: []MissionRunLink{run}, AddArtifacts: artifacts}
	if jsTrim(workflowRunID) != "" && jsTrim(workflowKey) != "" {
		child := MissionWorkflowChildUpdate{WorkflowRunID: jsTrim(workflowRunID), Key: jsTrim(workflowKey), RunID: runID, Status: childStatus, Heartbeat: &MissionHeartbeatUpdate{Status: childStatus, Message: summary}}
		for _, a := range artifacts {
			child.ArtifactPaths = append(child.ArtifactPaths, a.Path)
		}
		if childStatus != "running" && childStatus != "queued" && childStatus != "active" && childStatus != "paused" {
			child.CompletedAt = completedAt
		}
		update.UpsertWorkflowChildren = []MissionWorkflowChildUpdate{child}
	}
	if summary != "" {
		update.Summary = &summary
	}
	return updateMission(binding.Location, binding.MissionID, update, time.Now(), -1)
}

// recordMissingMissionSync notes once per run that its mission record is gone; mission bookkeeping never fails the run.
func recordMissingMissionSync(asyncDir, runID string, binding *MissionLaunchBinding) {
	const reason = "mission-record-missing"
	markerID := sha256Hex(marshalJSON([]any{runID, binding.MissionID, reason}, ""))
	marker := newObject()
	marker.set("runId", runID)
	marker.set("missionId", binding.MissionID)
	marker.set("reason", reason)
	f, err := os.OpenFile(filepath.Join(asyncDir, ".mission-sync-skipped-"+markerID+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	_, err = f.WriteString(marshalJSON(marker, "") + "\n")
	f.Close()
	if err != nil {
		return
	}
	event := newObject()
	event.set("type", "subagent.mission.sync.skipped")
	event.set("ts", float64(time.Now().UnixMilli()))
	event.set("runId", runID)
	event.set("missionId", binding.MissionID)
	event.set("reason", reason)
	event.set("missionPath", mustRecordPath(binding.Location, binding.MissionID))
	events, err := os.OpenFile(filepath.Join(asyncDir, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	if err != nil {
		return
	}
	defer events.Close()
	_, _ = events.WriteString(strings.TrimSpace(marshalJSON(event, "")) + "\n")
}
