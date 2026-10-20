package pi_subagents

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Port of src/missions/types.ts and the record parsers of src/missions/store.ts. A record is validated on every read and
// write; its JSON is written in the order the parser builds it.

var missionStatuses = []string{"planned", "active", "waiting", "needs_decision", "completed", "failed", "cancelled"}

var (
	missionRunModes      = []string{"single", "parallel", "chain", "workflow", "scheduled", "external"}
	missionArtifactKinds = []string{"status", "output", "patch", "manifest", "review", "note", "other"}
	missionReceiptKinds  = []string{"pull_request", "ci", "deployment", "release"}
	missionReceiptStates = []string{"pending", "ready", "succeeded", "failed"}
)

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func isTerminalMissionStatus(s string) bool {
	return s == "completed" || s == "failed" || s == "cancelled"
}

// MissionGoal, MissionTokenBudget and MissionTokenUsage are the goal-mode state of a mission.
type MissionGoal struct {
	Status string // active, paused, budget-exhausted
}

type MissionTokenBudget struct{ Tokens int64 }

type MissionTokenUsage struct{ Tokens int64 }

// MissionRunLink is a run attached to a mission. An empty string is an absent field.
type MissionRunLink struct {
	RunID       string
	AsyncDir    string
	ChildIndex  *int64
	Agent       string
	Mode        string
	Status      string
	StartedAt   string
	CompletedAt string
	Usage       *MissionTokenUsage
}

// MissionDecision is an owner decision a mission waits for.
type MissionDecision struct {
	ID             string
	Status         string // open, resolved
	Title          string
	Prompt         string
	Options        []string // nil: absent
	Recommendation string
	CreatedAt      string
	ResolvedAt     string
	Resolution     string
}

type MissionChildHeartbeat struct {
	UpdatedAt string
	Status    string
	Phase     string
	Message   string
}

// MissionWorkflowChild is one keyed child attempt of a workflow run.
type MissionWorkflowChild struct {
	WorkflowRunID string
	Key           string
	Status        string
	StartedAt     string
	UpdatedAt     string
	RunID         string
	Agent         string
	Task          string
	Label         string
	Phase         string
	CompletedAt   string
	SessionPath   string
	ArtifactPaths []string
	Heartbeat     *MissionChildHeartbeat
}

type MissionArtifact struct {
	Kind        string
	Path        string
	Description string
}

type MissionReceipt struct {
	Kind        string
	Status      string
	Title       string
	URL         string
	CreatedAt   string
	Description string
}

// MissionRecord is one mission file.
type MissionRecord struct {
	ID               string
	Title            string
	Objective        string
	Goal             *MissionGoal
	Budget           *MissionTokenBudget
	Usage            *MissionTokenUsage
	Status           string
	CreatedAt        string
	UpdatedAt        string
	Cwd              string
	OwnerSessionID   string
	Runs             []MissionRunLink
	WorkflowChildren []MissionWorkflowChild
	Decisions        []MissionDecision
	Artifacts        []MissionArtifact
	Receipts         []MissionReceipt
	Summary          string
	Acceptance       any
	HasAcceptance    bool
	Labels           []string // nil: absent
}

// MissionStoreConfig is config.missions; a nil pointer is an unset option.
type MissionStoreConfig struct {
	Enabled        *bool
	Directory      string
	GlobalIndex    *bool
	GlobalIndexDir string
	RetainTerminal *int64
}

// MissionStoreLocation is where one project keeps its missions.
type MissionStoreLocation struct {
	ProjectRoot      string
	MissionDir       string
	GlobalIndexDir   string
	WriteGlobalIndex bool
	RetainTerminal   *int64
}

// MissionListResult is the records of a project and the files that were skipped.
type MissionListResult struct {
	Records  []*MissionRecord
	Warnings []string
}

// MissionIndexEntry is a global index pointer.
type MissionIndexEntry struct {
	MissionID   string
	ProjectRoot string
	RecordPath  string
	Title       string
	Status      string
	UpdatedAt   string
	LastRunID   string
}

// GlobalMissionIndexRecord is a pointer and whether its record still reads.
type GlobalMissionIndexRecord struct {
	MissionIndexEntry
	Stale       bool
	StaleReason string
}

type GlobalMissionListResult struct {
	Entries  []GlobalMissionIndexRecord
	Warnings []string
}

func stringArrayOf(v any, label string) ([]string, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of non-empty strings", label)
	}
	out := []string{}
	seen := map[string]bool{}
	items := make([]string, len(arr))
	for i, item := range arr {
		s, err := missionRequiredString(item, fmt.Sprintf("%s[%d]", label, i))
		if err != nil {
			return nil, err
		}
		items[i] = jsTrim(s)
	}
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func positiveTokenCount(v any, label string) (int64, error) {
	n, ok := safeInteger(v)
	if !ok || n < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", label)
	}
	return n, nil
}

func nonNegativeTokenCount(v any, label string) (int64, error) {
	n, ok := safeInteger(v)
	if !ok || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", label)
	}
	return n, nil
}

func parseMissionGoal(v any, label string) (*MissionGoal, error) {
	o, err := missionAsObject(v, label)
	if err != nil {
		return nil, err
	}
	s, _ := o.vals["status"].(string)
	if s != "active" && s != "paused" && s != "budget-exhausted" {
		return nil, fmt.Errorf("%s.status is invalid", label)
	}
	return &MissionGoal{Status: s}, nil
}

func parseMissionBudget(v any, label string) (*MissionTokenBudget, error) {
	o, err := missionAsObject(v, label)
	if err != nil {
		return nil, err
	}
	n, err := positiveTokenCount(o.vals["tokens"], label+".tokens")
	if err != nil {
		return nil, err
	}
	return &MissionTokenBudget{Tokens: n}, nil
}

func parseMissionUsage(v any, label string) (*MissionTokenUsage, error) {
	o, err := missionAsObject(v, label)
	if err != nil {
		return nil, err
	}
	n, err := nonNegativeTokenCount(o.vals["tokens"], label+".tokens")
	if err != nil {
		return nil, err
	}
	return &MissionTokenUsage{Tokens: n}, nil
}

// validateMissionID is validateMissionId.
func validateMissionID(v any, label string) (string, error) {
	id, err := missionRequiredString(v, label)
	if err != nil {
		return "", err
	}
	if !workflowKeyPattern.MatchString(id) || strings.Contains(id, "..") {
		return "", fmt.Errorf("%s must contain only letters, numbers, '.', '_', or '-' and cannot contain '..'", label)
	}
	return id, nil
}

func parseMissionRunLink(v any, label string) (MissionRunLink, error) {
	var run MissionRunLink
	o, err := missionAsObject(v, label)
	if err != nil {
		return run, err
	}
	if run.RunID, err = missionRequiredString(o.vals["runId"], label+".runId"); err != nil {
		return run, err
	}
	if run.Mode, err = missionRequiredString(o.vals["mode"], label+".mode"); err != nil {
		return run, err
	}
	if !inList(missionRunModes, run.Mode) {
		return run, fmt.Errorf("%s.mode is invalid", label)
	}
	if raw, present := dynGet(o, "childIndex"); present {
		f, isNum := raw.(float64)
		if !isNum || f != float64(int64(f)) || f < 0 {
			return run, fmt.Errorf("%s.childIndex must be a non-negative integer", label)
		}
		n := int64(f)
		run.ChildIndex = &n
	}
	if run.AsyncDir, _, err = missionOptionalString(o, "asyncDir", label+".asyncDir"); err != nil {
		return run, err
	}
	if run.Agent, _, err = missionOptionalString(o, "agent", label+".agent"); err != nil {
		return run, err
	}
	if run.Status, _, err = missionOptionalString(o, "status", label+".status"); err != nil {
		return run, err
	}
	if raw, present := dynGet(o, "startedAt"); present {
		if run.StartedAt, err = missionTimestamp(raw, label+".startedAt"); err != nil {
			return run, err
		}
	}
	if raw, present := dynGet(o, "completedAt"); present {
		if run.CompletedAt, err = missionTimestamp(raw, label+".completedAt"); err != nil {
			return run, err
		}
	}
	if raw, present := dynGet(o, "usage"); present {
		if run.Usage, err = parseMissionUsage(raw, label+".usage"); err != nil {
			return run, err
		}
	}
	return run, nil
}

func (r MissionRunLink) toObject() *jsObject {
	o := newObject()
	o.set("runId", r.RunID)
	o.set("mode", r.Mode)
	setIf(o, "asyncDir", r.AsyncDir)
	if r.ChildIndex != nil {
		o.set("childIndex", float64(*r.ChildIndex))
	}
	setIf(o, "agent", r.Agent)
	setIf(o, "status", r.Status)
	setIf(o, "startedAt", r.StartedAt)
	setIf(o, "completedAt", r.CompletedAt)
	if r.Usage != nil {
		o.set("usage", tokensObject(r.Usage.Tokens))
	}
	return o
}

func setIf(o *jsObject, k, v string) {
	if v != "" {
		o.set(k, v)
	}
}

func tokensObject(n int64) *jsObject {
	o := newObject()
	o.set("tokens", float64(n))
	return o
}

// mergeRun is `{ ...existing, ...next }` for run links: a field next does not carry stays.
func mergeRun(old, next MissionRunLink) MissionRunLink {
	merged := old
	merged.RunID, merged.Mode = next.RunID, next.Mode
	if next.AsyncDir != "" {
		merged.AsyncDir = next.AsyncDir
	}
	if next.ChildIndex != nil {
		merged.ChildIndex = next.ChildIndex
	}
	if next.Agent != "" {
		merged.Agent = next.Agent
	}
	if next.Status != "" {
		merged.Status = next.Status
	}
	if next.StartedAt != "" {
		merged.StartedAt = next.StartedAt
	}
	if next.CompletedAt != "" {
		merged.CompletedAt = next.CompletedAt
	}
	if next.Usage != nil {
		merged.Usage = next.Usage
	}
	return merged
}

func parseMissionDecision(v any, label string) (MissionDecision, error) {
	var d MissionDecision
	o, err := missionAsObject(v, label)
	if err != nil {
		return d, err
	}
	status, _ := o.vals["status"].(string)
	if status != "open" && status != "resolved" {
		return d, fmt.Errorf("%s.status must be \"open\" or \"resolved\"", label)
	}
	d.Status = status
	if d.ID, err = validateMissionID(o.vals["id"], label+".id"); err != nil {
		return d, err
	}
	if d.Title, err = missionRequiredString(o.vals["title"], label+".title"); err != nil {
		return d, err
	}
	if d.CreatedAt, err = missionTimestamp(o.vals["createdAt"], label+".createdAt"); err != nil {
		return d, err
	}
	if d.Prompt, _, err = missionOptionalString(o, "prompt", label+".prompt"); err != nil {
		return d, err
	}
	if raw, present := dynGet(o, "options"); present {
		if d.Options, err = stringArrayOf(raw, label+".options"); err != nil {
			return d, err
		}
	}
	if d.Recommendation, _, err = missionOptionalString(o, "recommendation", label+".recommendation"); err != nil {
		return d, err
	}
	if raw, present := dynGet(o, "resolvedAt"); present {
		if d.ResolvedAt, err = missionTimestamp(raw, label+".resolvedAt"); err != nil {
			return d, err
		}
	}
	if d.Resolution, _, err = missionOptionalString(o, "resolution", label+".resolution"); err != nil {
		return d, err
	}
	return d, nil
}

func (d MissionDecision) toObject() *jsObject {
	o := newObject()
	o.set("id", d.ID)
	o.set("status", d.Status)
	o.set("title", d.Title)
	o.set("createdAt", d.CreatedAt)
	setIf(o, "prompt", d.Prompt)
	if d.Options != nil {
		o.set("options", anyStrings(d.Options))
	}
	setIf(o, "recommendation", d.Recommendation)
	setIf(o, "resolvedAt", d.ResolvedAt)
	setIf(o, "resolution", d.Resolution)
	return o
}

func parseMissionWorkflowChild(v any, label string) (MissionWorkflowChild, error) {
	var c MissionWorkflowChild
	o, err := missionAsObject(v, label)
	if err != nil {
		return c, err
	}
	c.ArtifactPaths = []string{}
	if raw, present := dynGet(o, "artifactPaths"); present {
		if c.ArtifactPaths, err = stringArrayOf(raw, label+".artifactPaths"); err != nil {
			return c, err
		}
	}
	var heartbeat *jsObject
	if raw, present := dynGet(o, "heartbeat"); present {
		if heartbeat, err = missionAsObject(raw, label+".heartbeat"); err != nil {
			return c, err
		}
	}
	if c.WorkflowRunID, err = missionRequiredString(o.vals["workflowRunId"], label+".workflowRunId"); err != nil {
		return c, err
	}
	if c.Key, err = validateMissionID(o.vals["key"], label+".key"); err != nil {
		return c, err
	}
	if c.Status, err = missionRequiredString(o.vals["status"], label+".status"); err != nil {
		return c, err
	}
	if c.StartedAt, err = missionTimestamp(o.vals["startedAt"], label+".startedAt"); err != nil {
		return c, err
	}
	if c.UpdatedAt, err = missionTimestamp(o.vals["updatedAt"], label+".updatedAt"); err != nil {
		return c, err
	}
	for _, f := range []struct {
		key string
		dst *string
	}{{"runId", &c.RunID}, {"agent", &c.Agent}, {"task", &c.Task}, {"label", &c.Label}, {"phase", &c.Phase}} {
		if *f.dst, _, err = missionOptionalString(o, f.key, label+"."+f.key); err != nil {
			return c, err
		}
	}
	if raw, present := dynGet(o, "completedAt"); present {
		if c.CompletedAt, err = missionTimestamp(raw, label+".completedAt"); err != nil {
			return c, err
		}
	}
	if c.SessionPath, _, err = missionOptionalString(o, "sessionPath", label+".sessionPath"); err != nil {
		return c, err
	}
	if heartbeat != nil {
		hb := &MissionChildHeartbeat{}
		if hb.UpdatedAt, err = missionTimestamp(heartbeat.vals["updatedAt"], label+".heartbeat.updatedAt"); err != nil {
			return c, err
		}
		for _, f := range []struct {
			key string
			dst *string
		}{{"status", &hb.Status}, {"phase", &hb.Phase}, {"message", &hb.Message}} {
			if *f.dst, _, err = missionOptionalString(heartbeat, f.key, label+".heartbeat."+f.key); err != nil {
				return c, err
			}
		}
		c.Heartbeat = hb
	}
	return c, nil
}

func (c MissionWorkflowChild) toObject() *jsObject {
	o := newObject()
	o.set("workflowRunId", c.WorkflowRunID)
	o.set("key", c.Key)
	o.set("status", c.Status)
	o.set("startedAt", c.StartedAt)
	o.set("updatedAt", c.UpdatedAt)
	o.set("artifactPaths", anyStrings(c.ArtifactPaths))
	setIf(o, "runId", c.RunID)
	setIf(o, "agent", c.Agent)
	setIf(o, "task", c.Task)
	setIf(o, "label", c.Label)
	setIf(o, "phase", c.Phase)
	setIf(o, "completedAt", c.CompletedAt)
	setIf(o, "sessionPath", c.SessionPath)
	if c.Heartbeat != nil {
		h := newObject()
		h.set("updatedAt", c.Heartbeat.UpdatedAt)
		setIf(h, "status", c.Heartbeat.Status)
		setIf(h, "phase", c.Heartbeat.Phase)
		setIf(h, "message", c.Heartbeat.Message)
		o.set("heartbeat", h)
	}
	return o
}

func parseMissionArtifact(v any, label string) (MissionArtifact, error) {
	var a MissionArtifact
	o, err := missionAsObject(v, label)
	if err != nil {
		return a, err
	}
	if a.Kind, err = missionRequiredString(o.vals["kind"], label+".kind"); err != nil {
		return a, err
	}
	if !inList(missionArtifactKinds, a.Kind) {
		return a, fmt.Errorf("%s.kind is invalid", label)
	}
	if a.Path, err = missionRequiredString(o.vals["path"], label+".path"); err != nil {
		return a, err
	}
	if a.Description, _, err = missionOptionalString(o, "description", label+".description"); err != nil {
		return a, err
	}
	return a, nil
}

func (a MissionArtifact) toObject() *jsObject {
	o := newObject()
	o.set("kind", a.Kind)
	o.set("path", a.Path)
	setIf(o, "description", a.Description)
	return o
}

// isAbsoluteURL is `new URL(value)` not throwing.
func isAbsoluteURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return false
	}
	// WHATWG URL: a special scheme needs a host; others (mailto:, file:) parse with any path.
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ws", "wss", "ftp":
		return u.Host != ""
	}
	return true
}

func parseMissionReceipt(v any, label string) (MissionReceipt, error) {
	var r MissionReceipt
	o, err := missionAsObject(v, label)
	if err != nil {
		return r, err
	}
	if r.Kind, err = missionRequiredString(o.vals["kind"], label+".kind"); err != nil {
		return r, err
	}
	if r.Status, err = missionRequiredString(o.vals["status"], label+".status"); err != nil {
		return r, err
	}
	if !inList(missionReceiptKinds, r.Kind) {
		return r, fmt.Errorf("%s.kind is invalid", label)
	}
	if !inList(missionReceiptStates, r.Status) {
		return r, fmt.Errorf("%s.status is invalid", label)
	}
	if r.URL, err = missionRequiredString(o.vals["url"], label+".url"); err != nil {
		return r, err
	}
	if !isAbsoluteURL(r.URL) {
		return r, fmt.Errorf("%s.url must be an absolute URL", label)
	}
	if r.Title, err = missionRequiredString(o.vals["title"], label+".title"); err != nil {
		return r, err
	}
	if r.CreatedAt, err = missionTimestamp(o.vals["createdAt"], label+".createdAt"); err != nil {
		return r, err
	}
	if r.Description, _, err = missionOptionalString(o, "description", label+".description"); err != nil {
		return r, err
	}
	return r, nil
}

func (r MissionReceipt) toObject() *jsObject {
	o := newObject()
	o.set("kind", r.Kind)
	o.set("status", r.Status)
	o.set("title", r.Title)
	o.set("url", r.URL)
	o.set("createdAt", r.CreatedAt)
	setIf(o, "description", r.Description)
	return o
}

func missionStatusOf(v any, label string) (string, error) {
	s, ok := v.(string)
	if !ok || !inList(missionStatuses, s) {
		return "", fmt.Errorf("%s must be one of %s", label, strings.Join(missionStatuses, ", "))
	}
	return s, nil
}

func requireArray(o *jsObject, key, source string, required bool) ([]any, error) {
	v, present := dynGet(o, key)
	if !present && !required {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s.%s must be an array", source, key)
	}
	return arr, nil
}

// parseMissionRecord is parseMissionRecord.
func parseMissionRecord(value any, source string) (*MissionRecord, error) {
	o, err := missionAsObject(value, source)
	if err != nil {
		return nil, err
	}
	if v, ok := o.vals["schemaVersion"].(float64); !ok || v != 1 {
		return nil, fmt.Errorf("%s.schemaVersion must be 1", source)
	}
	runs, err := requireArray(o, "runs", source, true)
	if err != nil {
		return nil, err
	}
	workflowChildren, err := requireArray(o, "workflowChildren", source, false)
	if err != nil {
		return nil, err
	}
	decisions, err := requireArray(o, "decisions", source, true)
	if err != nil {
		return nil, err
	}
	artifacts, err := requireArray(o, "artifacts", source, true)
	if err != nil {
		return nil, err
	}
	receipts, err := requireArray(o, "receipts", source, false)
	if err != nil {
		return nil, err
	}
	r := &MissionRecord{Runs: []MissionRunLink{}, WorkflowChildren: []MissionWorkflowChild{}, Decisions: []MissionDecision{}, Artifacts: []MissionArtifact{}, Receipts: []MissionReceipt{}}
	if raw, present := dynGet(o, "goal"); present {
		if r.Goal, err = parseMissionGoal(raw, source+".goal"); err != nil {
			return nil, err
		}
	}
	if raw, present := dynGet(o, "budget"); present {
		if r.Budget, err = parseMissionBudget(raw, source+".budget"); err != nil {
			return nil, err
		}
	}
	if raw, present := dynGet(o, "usage"); present {
		if r.Usage, err = parseMissionUsage(raw, source+".usage"); err != nil {
			return nil, err
		}
	}
	objective, _, err := missionOptionalString(o, "objective", source+".objective")
	if err != nil {
		return nil, err
	}
	if objective = jsTrim(objective); objective == "" {
		return nil, fmt.Errorf("%s.objective must be a non-empty string", source)
	}
	r.Objective = objective
	if r.Goal != nil && r.Budget == nil {
		return nil, fmt.Errorf("%s.budget is required for a goal mission", source)
	}
	if r.ID, err = validateMissionID(o.vals["id"], source+".id"); err != nil {
		return nil, err
	}
	if r.Title, err = missionRequiredString(o.vals["title"], source+".title"); err != nil {
		return nil, err
	}
	if r.Status, err = missionStatusOf(o.vals["status"], source+".status"); err != nil {
		return nil, err
	}
	if r.CreatedAt, err = missionTimestamp(o.vals["createdAt"], source+".createdAt"); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = missionTimestamp(o.vals["updatedAt"], source+".updatedAt"); err != nil {
		return nil, err
	}
	for i, item := range runs {
		run, err := parseMissionRunLink(item, fmt.Sprintf("%s.runs[%d]", source, i))
		if err != nil {
			return nil, err
		}
		r.Runs = append(r.Runs, run)
	}
	for i, item := range workflowChildren {
		child, err := parseMissionWorkflowChild(item, fmt.Sprintf("%s.workflowChildren[%d]", source, i))
		if err != nil {
			return nil, err
		}
		r.WorkflowChildren = append(r.WorkflowChildren, child)
	}
	for i, item := range decisions {
		d, err := parseMissionDecision(item, fmt.Sprintf("%s.decisions[%d]", source, i))
		if err != nil {
			return nil, err
		}
		r.Decisions = append(r.Decisions, d)
	}
	for i, item := range artifacts {
		a, err := parseMissionArtifact(item, fmt.Sprintf("%s.artifacts[%d]", source, i))
		if err != nil {
			return nil, err
		}
		r.Artifacts = append(r.Artifacts, a)
	}
	for i, item := range receipts {
		rc, err := parseMissionReceipt(item, fmt.Sprintf("%s.receipts[%d]", source, i))
		if err != nil {
			return nil, err
		}
		r.Receipts = append(r.Receipts, rc)
	}
	if r.Cwd, _, err = missionOptionalString(o, "cwd", source+".cwd"); err != nil {
		return nil, err
	}
	if r.OwnerSessionID, _, err = missionOptionalString(o, "ownerSessionId", source+".ownerSessionId"); err != nil {
		return nil, err
	}
	if r.Summary, _, err = missionOptionalString(o, "summary", source+".summary"); err != nil {
		return nil, err
	}
	if raw, present := dynGet(o, "acceptance"); present {
		r.Acceptance, r.HasAcceptance = raw, true
	}
	if raw, present := dynGet(o, "labels"); present {
		if r.Labels, err = stringArrayOf(raw, source+".labels"); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *MissionRecord) toObject() *jsObject {
	o := newObject()
	o.set("schemaVersion", float64(1))
	o.set("id", r.ID)
	o.set("title", r.Title)
	o.set("objective", r.Objective)
	if r.Goal != nil {
		g := newObject()
		g.set("status", r.Goal.Status)
		o.set("goal", g)
	}
	if r.Budget != nil {
		o.set("budget", tokensObject(r.Budget.Tokens))
	}
	if r.Usage != nil {
		o.set("usage", tokensObject(r.Usage.Tokens))
	}
	o.set("status", r.Status)
	o.set("createdAt", r.CreatedAt)
	o.set("updatedAt", r.UpdatedAt)
	runs := make([]any, len(r.Runs))
	for i, run := range r.Runs {
		runs[i] = run.toObject()
	}
	o.set("runs", runs)
	children := make([]any, len(r.WorkflowChildren))
	for i, c := range r.WorkflowChildren {
		children[i] = c.toObject()
	}
	o.set("workflowChildren", children)
	decisions := make([]any, len(r.Decisions))
	for i, d := range r.Decisions {
		decisions[i] = d.toObject()
	}
	o.set("decisions", decisions)
	artifacts := make([]any, len(r.Artifacts))
	for i, a := range r.Artifacts {
		artifacts[i] = a.toObject()
	}
	o.set("artifacts", artifacts)
	receipts := make([]any, len(r.Receipts))
	for i, rc := range r.Receipts {
		receipts[i] = rc.toObject()
	}
	o.set("receipts", receipts)
	setIf(o, "cwd", r.Cwd)
	setIf(o, "ownerSessionId", r.OwnerSessionID)
	setIf(o, "summary", r.Summary)
	if r.HasAcceptance {
		o.set("acceptance", r.Acceptance)
	}
	if r.Labels != nil {
		o.set("labels", anyStrings(r.Labels))
	}
	return o
}

// MarshalJSON writes the record as a tool result carries it (compact, the parser's key order).
func (r *MissionRecord) MarshalJSON() ([]byte, error) {
	return []byte(marshalJSON(r.toObject(), "")), nil
}

// MarshalJSON of an index record is its pointer fields and the stale marker.
func (r GlobalMissionIndexRecord) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.set("schemaVersion", float64(1))
	o.set("missionId", r.MissionID)
	o.set("projectRoot", r.ProjectRoot)
	o.set("recordPath", r.RecordPath)
	o.set("title", r.Title)
	o.set("status", r.Status)
	o.set("updatedAt", r.UpdatedAt)
	setIf(o, "lastRunId", r.LastRunID)
	o.set("stale", r.Stale)
	setIf(o, "staleReason", r.StaleReason)
	return []byte(marshalJSON(o, "")), nil
}

var _ json.Marshaler = (*MissionRecord)(nil)
