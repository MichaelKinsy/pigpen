package ohmyhermes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

const (
	DAGContractVersion  = "omh_dag/v1"
	TeamContractVersion = "omh_team/v1"
	LoopContractVersion = "loop_cycle/v2"
	MaxDAGNodes         = 48
	MaxLoopIterations   = 200
)

// --- DAG Store & Topology Engine ---

// DAGNode represents one work unit in a directed acyclic graph.
type DAGNode struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Role       string   `json:"role,omitempty"`
	Category   string   `json:"category,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
	WriteScope []string `json:"write_scope,omitempty"`
	Status     string   `json:"status"` // pending, running, completed, failed, skipped
	Result     string   `json:"result,omitempty"`
	Error      string   `json:"error,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

// DAGRun is a persisted DAG execution record under $OMH_HOME/runtime/dag/<run_id>.json.
type DAGRun struct {
	Schema    string    `json:"schema"`
	RunID     string    `json:"run_id"`
	SessionID string    `json:"session_id,omitempty"`
	Title     string    `json:"title"`
	Status    string    `json:"status"` // active, completed, failed, cancelled
	Nodes     []DAGNode `json:"nodes"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

// ValidateDAG checks node count, unique IDs, unknown/self dependencies, and cycles (Kahn's algorithm).
// Returns topological layers (each layer is a slice of node IDs) on success.
func ValidateDAG(nodes []DAGNode) ([][]string, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("dag requires at least one node")
	}
	if len(nodes) > MaxDAGNodes {
		return nil, fmt.Errorf("dag exceeds maximum of %d nodes (got %d)", MaxDAGNodes, len(nodes))
	}
	index := make(map[string]int, len(nodes))
	for i, n := range nodes {
		id := strings.TrimSpace(n.ID)
		if id == "" {
			return nil, fmt.Errorf("node[%d].id is required", i)
		}
		if _, dup := index[id]; dup {
			return nil, fmt.Errorf("duplicate node id %q", id)
		}
		index[id] = i
	}

	inDegree := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		inDegree[n.ID] = 0
	}
	for _, n := range nodes {
		seenDep := make(map[string]bool)
		for _, dep := range n.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if dep == n.ID {
				return nil, fmt.Errorf("self_dependency: node %q depends on itself", n.ID)
			}
			if _, ok := index[dep]; !ok {
				return nil, fmt.Errorf("unknown_dependency: node %q depends on missing node %q", n.ID, dep)
			}
			if !seenDep[dep] {
				seenDep[dep] = true
				inDegree[n.ID]++
				dependents[dep] = append(dependents[dep], n.ID)
			}
		}
	}

	var layers [][]string
	remaining := len(nodes)
	var current []string
	for _, n := range nodes {
		if inDegree[n.ID] == 0 {
			current = append(current, n.ID)
		}
	}
	sort.Strings(current)

	for len(current) > 0 {
		layers = append(layers, current)
		remaining -= len(current)
		var next []string
		for _, id := range current {
			for _, child := range dependents[id] {
				inDegree[child]--
				if inDegree[child] == 0 {
					next = append(next, child)
				}
			}
		}
		sort.Strings(next)
		current = next
	}

	if remaining > 0 {
		return nil, fmt.Errorf("cycle_detected: %d node(s) belong to a dependency cycle", remaining)
	}
	return layers, nil
}

// ReadyNodes returns IDs of pending nodes whose dependencies are all completed.
func ReadyNodes(run *DAGRun) []string {
	if run == nil {
		return nil
	}
	statusByID := make(map[string]string, len(run.Nodes))
	for _, n := range run.Nodes {
		statusByID[n.ID] = n.Status
	}
	var ready []string
	for _, n := range run.Nodes {
		if n.Status != "pending" {
			continue
		}
		allDone := true
		for _, dep := range n.DependsOn {
			if statusByID[dep] != "completed" {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, n.ID)
		}
	}
	sort.Strings(ready)
	return ready
}

// DAGStore persists DAGRun records atomically under $OMH_HOME/runtime/dag/.
type DAGStore struct {
	mu    sync.RWMutex
	paths func() OMHPaths
}

func NewDAGStore(pathsFn func() OMHPaths) *DAGStore {
	if pathsFn == nil {
		pathsFn = func() OMHPaths { return ResolvePaths("") }
	}
	return &DAGStore{paths: pathsFn}
}

func (s *DAGStore) runPath(runID string) (string, error) {
	id, err := sanitizeID(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.paths().DAGDir, id+".json"), nil
}

// Create validates and saves a new DAGRun.
func (s *DAGStore) Create(sessionID, runID, title string, nodes []DAGNode) (*DAGRun, [][]string, error) {
	if strings.TrimSpace(runID) == "" {
		runID = newShortID("dag")
	}
	id, err := sanitizeID(runID)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	normalized := make([]DAGNode, len(nodes))
	for i, n := range nodes {
		n.ID = strings.TrimSpace(n.ID)
		n.Title = strings.TrimSpace(n.Title)
		if n.Title == "" {
			n.Title = n.ID
		}
		if n.Status == "" {
			n.Status = "pending"
		}
		n.UpdatedAt = now
		normalized[i] = n
	}
	layers, err := ValidateDAG(normalized)
	if err != nil {
		return nil, nil, err
	}
	run := &DAGRun{
		Schema:    DAGContractVersion,
		RunID:     id,
		SessionID: sessionID,
		Title:     strings.TrimSpace(title),
		Status:    "active",
		Nodes:     normalized,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.runPath(id)
	if err != nil {
		return nil, nil, err
	}
	if err := atomicWriteJSON(p, run); err != nil {
		return nil, nil, err
	}
	return run, layers, nil
}

// Get loads a DAGRun by ID.
func (s *DAGStore) Get(runID string) (*DAGRun, error) {
	p, err := s.runPath(runID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var run DAGRun
	exists, err := readJSON(p, &run)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("dag %q not found", runID)
	}
	return &run, nil
}

// List returns all persisted DAGRuns sorted by RunID.
func (s *DAGStore) List() ([]*DAGRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := s.paths().DAGDir
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*DAGRun
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var run DAGRun
		if ok, err := readJSON(filepath.Join(dir, e.Name()), &run); err == nil && ok {
			cp := run
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunID < out[j].RunID })
	return out, nil
}

// UpdateNode updates a single node's status/result/error and recomputes DAG status.
func (s *DAGStore) UpdateNode(runID, nodeID, status, result, errMsg string) (*DAGRun, error) {
	validStatus := map[string]bool{
		"pending": true, "running": true, "completed": true, "failed": true, "skipped": true,
	}
	if !validStatus[status] {
		return nil, fmt.Errorf("invalid node status %q", status)
	}
	p, err := s.runPath(runID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var run DAGRun
	exists, err := readJSON(p, &run)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("dag %q not found", runID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	found := false
	for i := range run.Nodes {
		if run.Nodes[i].ID == nodeID {
			run.Nodes[i].Status = status
			if result != "" {
				run.Nodes[i].Result = result
			}
			if errMsg != "" {
				run.Nodes[i].Error = errMsg
			}
			run.Nodes[i].UpdatedAt = now
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("node %q not found in dag %q", nodeID, runID)
	}
	run.Status = deriveDAGStatus(run.Nodes)
	run.UpdatedAt = now
	if err := atomicWriteJSON(p, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// SetStatus transitions the entire DAGRun to completed or failed.
func (s *DAGStore) SetStatus(runID, status string) (*DAGRun, error) {
	p, err := s.runPath(runID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var run DAGRun
	exists, err := readJSON(p, &run)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("dag %q not found", runID)
	}
	run.Status = status
	run.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := atomicWriteJSON(p, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func deriveDAGStatus(nodes []DAGNode) string {
	allDone := true
	anyFailed := false
	for _, n := range nodes {
		if n.Status == "failed" {
			anyFailed = true
		}
		if n.Status != "completed" && n.Status != "skipped" {
			allDone = false
		}
	}
	if allDone {
		return "completed"
	}
	if anyFailed {
		return "failed"
	}
	return "active"
}

// --- Team & Delegation Ledger Store ---

// TeamLane represents one parallel worker lane inside an omh_team.
type TeamLane struct {
	LaneID         string `json:"lane_id"`
	Title          string `json:"title"`
	Role           string `json:"role,omitempty"`
	Category       string `json:"category,omitempty"`
	ChildSessionID string `json:"child_session_id,omitempty"`
	Status         string `json:"status"` // pending, running, completed, failed
	Result         string `json:"result,omitempty"`
	DurationMs     int64  `json:"duration_ms,omitempty"`
	InputTokens    int    `json:"input_tokens,omitempty"`
	OutputTokens   int    `json:"output_tokens,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

// TeamRecord is a persisted parallel team under $OMH_HOME/runtime/team/<team_id>.json.
type TeamRecord struct {
	Schema    string     `json:"schema"`
	TeamID    string     `json:"team_id"`
	SessionID string     `json:"session_id,omitempty"`
	Title     string     `json:"title"`
	Status    string     `json:"status"` // active, completed, failed
	Lanes     []TeamLane `json:"lanes"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
}

// DelegationEventRecord stores subagent start/stop ledger entries observed on EventBus.
type DelegationEventRecord struct {
	TaskID          string  `json:"task_id"`
	ParentSessionID string  `json:"parent_session_id,omitempty"`
	ChildSessionID  string  `json:"child_session_id,omitempty"`
	Goal            string  `json:"goal,omitempty"`
	Category        string  `json:"category,omitempty"`
	Model           string  `json:"model,omitempty"`
	ThinkingLevel   string  `json:"thinking_level,omitempty"`
	Status          string  `json:"status"` // running, completed, error, cancelled
	DurationMs      int64   `json:"duration_ms,omitempty"`
	InputTokens     int     `json:"input_tokens,omitempty"`
	OutputTokens    int     `json:"output_tokens,omitempty"`
	Cost            float64 `json:"cost,omitempty"`
	Error           string  `json:"error,omitempty"`
	StartedAt       string  `json:"started_at,omitempty"`
	FinishedAt      string  `json:"finished_at,omitempty"`
}

// TeamStore persists team records and tracks live subagent delegations.
type TeamStore struct {
	mu          sync.RWMutex
	paths       func() OMHPaths
	delegations map[string]*DelegationEventRecord // keyed by taskID or childSessionID
}

func NewTeamStore(pathsFn func() OMHPaths) *TeamStore {
	if pathsFn == nil {
		pathsFn = func() OMHPaths { return ResolvePaths("") }
	}
	return &TeamStore{
		paths:       pathsFn,
		delegations: make(map[string]*DelegationEventRecord),
	}
}

func (s *TeamStore) teamPath(teamID string) (string, error) {
	id, err := sanitizeID(teamID)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.paths().TeamDir, id+".json"), nil
}

// Create creates and persists a TeamRecord.
func (s *TeamStore) Create(sessionID, teamID, title string, lanes []TeamLane) (*TeamRecord, error) {
	if strings.TrimSpace(teamID) == "" {
		teamID = newShortID("team")
	}
	id, err := sanitizeID(teamID)
	if err != nil {
		return nil, err
	}
	if len(lanes) == 0 {
		return nil, fmt.Errorf("team requires at least one lane")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	norm := make([]TeamLane, len(lanes))
	for i, l := range lanes {
		l.LaneID = strings.TrimSpace(l.LaneID)
		if l.LaneID == "" {
			l.LaneID = fmt.Sprintf("lane-%d", i+1)
		}
		if strings.TrimSpace(l.Title) == "" {
			l.Title = l.LaneID
		}
		if l.Status == "" {
			l.Status = "pending"
		}
		l.UpdatedAt = now
		norm[i] = l
	}
	rec := &TeamRecord{
		Schema:    TeamContractVersion,
		TeamID:    id,
		SessionID: sessionID,
		Title:     strings.TrimSpace(title),
		Status:    "active",
		Lanes:     norm,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.teamPath(id)
	if err != nil {
		return nil, err
	}
	if err := atomicWriteJSON(p, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// Get loads a TeamRecord by ID.
func (s *TeamStore) Get(teamID string) (*TeamRecord, error) {
	p, err := s.teamPath(teamID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var rec TeamRecord
	exists, err := readJSON(p, &rec)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("team %q not found", teamID)
	}
	return &rec, nil
}

// List returns all persisted teams sorted by TeamID.
func (s *TeamStore) List() ([]*TeamRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := s.paths().TeamDir
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*TeamRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var rec TeamRecord
		if ok, err := readJSON(filepath.Join(dir, e.Name()), &rec); err == nil && ok {
			cp := rec
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TeamID < out[j].TeamID })
	return out, nil
}

// RecordLane updates a lane's status/result/childSessionID.
func (s *TeamStore) RecordLane(teamID, laneID, status, result, childSessionID string) (*TeamRecord, error) {
	p, err := s.teamPath(teamID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var rec TeamRecord
	exists, err := readJSON(p, &rec)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("team %q not found", teamID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	found := false
	for i := range rec.Lanes {
		if rec.Lanes[i].LaneID == laneID {
			if status != "" {
				rec.Lanes[i].Status = status
			}
			if result != "" {
				rec.Lanes[i].Result = result
			}
			if childSessionID != "" {
				rec.Lanes[i].ChildSessionID = childSessionID
			}
			rec.Lanes[i].UpdatedAt = now
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("lane %q not found in team %q", laneID, teamID)
	}
	rec.Status = deriveTeamStatus(rec.Lanes)
	rec.UpdatedAt = now
	if err := atomicWriteJSON(p, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func deriveTeamStatus(lanes []TeamLane) string {
	allDone := true
	anyFailed := false
	for _, l := range lanes {
		if l.Status == "failed" || l.Status == "error" {
			anyFailed = true
		}
		if l.Status != "completed" {
			allDone = false
		}
	}
	if allDone {
		return "completed"
	}
	if anyFailed {
		return "failed"
	}
	return "active"
}

// OnSubagentStart records a hermes:subagent_start event into the delegation ledger
// and latches matching team lanes if task_id matches a lane_id.
func (s *TeamStore) OnSubagentStart(p events.SubagentStartPayload) {
	key := p.TaskID
	if key == "" {
		key = p.ChildSessionID
	}
	if key == "" {
		key = newShortID("sub")
	}
	rec := &DelegationEventRecord{
		TaskID:          p.TaskID,
		ParentSessionID: p.ParentSessionID,
		ChildSessionID:  p.ChildSessionID,
		Goal:            p.Goal,
		Category:        p.Category,
		Model:           p.Model,
		ThinkingLevel:   p.ThinkingLevel,
		Status:          "running",
		StartedAt:       p.StartedAt,
	}
	if rec.StartedAt == "" {
		rec.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	s.mu.Lock()
	s.delegations[key] = rec
	s.mu.Unlock()

	if safeKey, err := sanitizeID(key); err == nil {
		_ = atomicWriteJSON(filepath.Join(s.paths().DelegationDir, safeKey+".json"), rec)
	}
	s.latchTeamLane(p.TaskID, p.ChildSessionID, "running", 0, 0, 0)
}

// OnSubagentStop records a hermes:subagent_stop event into the delegation ledger,
// updates usage.Default(), and settles matching team lanes.
func (s *TeamStore) OnSubagentStop(p events.SubagentStopPayload) {
	key := p.TaskID
	if key == "" {
		key = p.ChildSessionID
	}
	if key == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	s.mu.Lock()
	rec, ok := s.delegations[key]
	if !ok {
		rec = &DelegationEventRecord{
			TaskID:          p.TaskID,
			ParentSessionID: p.ParentSessionID,
			ChildSessionID:  p.ChildSessionID,
		}
		s.delegations[key] = rec
	}
	rec.Status = p.Status
	if rec.Status == "" {
		rec.Status = "completed"
	}
	rec.DurationMs = p.DurationMs
	rec.InputTokens = p.InputTokens
	rec.OutputTokens = p.OutputTokens
	rec.Cost = p.Cost
	rec.Error = p.Error
	rec.FinishedAt = now
	snapshot := *rec
	s.mu.Unlock()

	if p.ChildSessionID != "" && (p.InputTokens > 0 || p.OutputTokens > 0 || p.Cost > 0) {
		usage.Default().Add(p.ChildSessionID, p.ParentSessionID, snapshot.Model, p.InputTokens, p.OutputTokens, p.Cost, 1, 0)
	}
	if safeKey, err := sanitizeID(key); err == nil {
		_ = atomicWriteJSON(filepath.Join(s.paths().DelegationDir, safeKey+".json"), &snapshot)
	}
	laneStatus := "completed"
	if p.Status == "error" || p.Status == "failed" || p.Status == "cancelled" {
		laneStatus = "failed"
	}
	s.latchTeamLane(p.TaskID, p.ChildSessionID, laneStatus, p.DurationMs, p.InputTokens, p.OutputTokens)
}

func (s *TeamStore) latchTeamLane(taskID, childSessionID, status string, durationMs int64, inTok, outTok int) {
	teams, err := s.List()
	if err != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, team := range teams {
		changed := false
		for i := range team.Lanes {
			lane := &team.Lanes[i]
			match := (taskID != "" && lane.LaneID == taskID) || (childSessionID != "" && lane.ChildSessionID == childSessionID)
			if match {
				lane.Status = status
				if childSessionID != "" {
					lane.ChildSessionID = childSessionID
				}
				if durationMs > 0 {
					lane.DurationMs = durationMs
				}
				if inTok > 0 {
					lane.InputTokens = inTok
				}
				if outTok > 0 {
					lane.OutputTokens = outTok
				}
				lane.UpdatedAt = now
				changed = true
			}
		}
		if changed {
			team.Status = deriveTeamStatus(team.Lanes)
			team.UpdatedAt = now
			if p, err := s.teamPath(team.TeamID); err == nil {
				s.mu.Lock()
				_ = atomicWriteJSON(p, team)
				s.mu.Unlock()
			}
		}
	}
}

// Delegations returns all observed subagent delegation records (memory + disk).
func (s *TeamStore) Delegations() []DelegationEventRecord {
	s.mu.RLock()
	merged := make(map[string]DelegationEventRecord, len(s.delegations))
	for k, v := range s.delegations {
		merged[k] = *v
	}
	s.mu.RUnlock()

	dir := s.paths().DelegationDir
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			var rec DelegationEventRecord
			if ok, err := readJSON(filepath.Join(dir, e.Name()), &rec); err == nil && ok {
				k := rec.TaskID
				if k == "" {
					k = rec.ChildSessionID
				}
				if k != "" {
					if _, exists := merged[k]; !exists {
						merged[k] = rec
					}
				}
			}
		}
	}

	out := make([]DelegationEventRecord, 0, len(merged))
	for _, v := range merged {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}

// --- Durable Bounded Loop Store ---

// LoopRecord is a persisted bounded iteration loop under $OMH_HOME/runtime/loops/<loop_id>.json.
type LoopRecord struct {
	Schema          string   `json:"schema"`
	LoopID          string   `json:"loop_id"`
	SessionID       string   `json:"session_id,omitempty"`
	Goal            string   `json:"goal"`
	PermissionScope string   `json:"permission_scope"` // read-only, workspace-write, full
	MaxIterations   int      `json:"max_iterations"`
	Iteration       int      `json:"iteration"`
	Status          string   `json:"status"` // active, stopped, exhausted, completed
	StopReason      string   `json:"stop_reason,omitempty"`
	History         []string `json:"history,omitempty"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

// LoopStore persists LoopRecords atomically under $OMH_HOME/runtime/loops/.
type LoopStore struct {
	mu    sync.RWMutex
	paths func() OMHPaths
}

func NewLoopStore(pathsFn func() OMHPaths) *LoopStore {
	if pathsFn == nil {
		pathsFn = func() OMHPaths { return ResolvePaths("") }
	}
	return &LoopStore{paths: pathsFn}
}

func (s *LoopStore) loopPath(loopID string) (string, error) {
	id, err := sanitizeID(loopID)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.paths().LoopsDir, id+".json"), nil
}

// Create creates a new durable loop with validated permission scope and max_iterations cap.
func (s *LoopStore) Create(sessionID, loopID, goal, permissionScope string, maxIter int) (*LoopRecord, error) {
	if strings.TrimSpace(loopID) == "" {
		loopID = newShortID("loop")
	}
	id, err := sanitizeID(loopID)
	if err != nil {
		return nil, err
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil, fmt.Errorf("loop goal is required")
	}
	if maxIter <= 0 {
		maxIter = 5
	}
	if maxIter > MaxLoopIterations {
		return nil, fmt.Errorf("max_iterations exceeds cap of %d (got %d)", MaxLoopIterations, maxIter)
	}
	scope := strings.ToLower(strings.TrimSpace(permissionScope))
	if scope == "" {
		scope = "workspace-write"
	}
	switch scope {
	case "read-only", "workspace-write", "full":
	default:
		return nil, fmt.Errorf("invalid permission_scope %q (must be read-only, workspace-write, or full)", scope)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rec := &LoopRecord{
		Schema:          LoopContractVersion,
		LoopID:          id,
		SessionID:       sessionID,
		Goal:            goal,
		PermissionScope: scope,
		MaxIterations:   maxIter,
		Iteration:       0,
		Status:          "active",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loopPath(id)
	if err != nil {
		return nil, err
	}
	if err := atomicWriteJSON(p, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// Get loads a LoopRecord by ID.
func (s *LoopStore) Get(loopID string) (*LoopRecord, error) {
	p, err := s.loopPath(loopID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var rec LoopRecord
	exists, err := readJSON(p, &rec)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("loop %q not found", loopID)
	}
	return &rec, nil
}

// List returns all LoopRecords sorted by LoopID.
func (s *LoopStore) List() ([]*LoopRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := s.paths().LoopsDir
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*LoopRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var rec LoopRecord
		if ok, err := readJSON(filepath.Join(dir, e.Name()), &rec); err == nil && ok {
			cp := rec
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LoopID < out[j].LoopID })
	return out, nil
}

// Step advances a loop by one iteration, enforcing the max_iterations cap.
func (s *LoopStore) Step(loopID, note string) (*LoopRecord, error) {
	p, err := s.loopPath(loopID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var rec LoopRecord
	exists, err := readJSON(p, &rec)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("loop %q not found", loopID)
	}
	if rec.Status != "active" {
		return nil, fmt.Errorf("loop %q is %s and cannot step", loopID, rec.Status)
	}
	if rec.Iteration >= rec.MaxIterations {
		rec.Status = "exhausted"
		rec.StopReason = "max_iterations_reached"
		rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		_ = atomicWriteJSON(p, &rec)
		return nil, fmt.Errorf("loop %q reached max_iterations (%d)", loopID, rec.MaxIterations)
	}
	rec.Iteration++
	if strings.TrimSpace(note) != "" {
		rec.History = append(rec.History, fmt.Sprintf("[%d] %s", rec.Iteration, strings.TrimSpace(note)))
	}
	if rec.Iteration >= rec.MaxIterations {
		rec.Status = "exhausted"
		rec.StopReason = "max_iterations_reached"
	}
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := atomicWriteJSON(p, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// Stop halts a loop with an optional reason.
func (s *LoopStore) Stop(loopID, reason string) (*LoopRecord, error) {
	p, err := s.loopPath(loopID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var rec LoopRecord
	exists, err := readJSON(p, &rec)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("loop %q not found", loopID)
	}
	rec.Status = "stopped"
	rec.StopReason = strings.TrimSpace(reason)
	if rec.StopReason == "" {
		rec.StopReason = "user_stopped"
	}
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := atomicWriteJSON(p, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// --- Tool & EventBus Registration ---

func registerDAGTeamLoopTools(ext *sdk.Extension, dags *DAGStore, teams *TeamStore, loops *LoopStore) {
	// Subscribe to pig-hermes-core subagent lifecycle channels.
	_, _ = ext.Events().On(events.ChannelSubagentStart, func(ctx sdk.Context, data any) error {
		var payload events.SubagentStartPayload
		if raw, err := json.Marshal(data); err == nil {
			if err := json.Unmarshal(raw, &payload); err == nil {
				teams.OnSubagentStart(payload)
			}
		}
		return nil
	})
	_, _ = ext.Events().On(events.ChannelSubagentStop, func(ctx sdk.Context, data any) error {
		var payload events.SubagentStopPayload
		if raw, err := json.Marshal(data); err == nil {
			if err := json.Unmarshal(raw, &payload); err == nil {
				teams.OnSubagentStop(payload)
			}
		}
		return nil
	})

	// 1. omh_dag
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_dag",
		Label:         "OMH DAG",
		Description:   "Create and manage topological work DAGs (cycle detection, dependency readiness, frontier computation) under $OMH_HOME/runtime/dag/.",
		PromptSnippet: "Manage dependency DAGs with omh_dag (create, list, get, ready, update_node, complete, fail).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"create", "list", "get", "ready", "update_node", "complete", "fail"},
				},
				"run_id":  map[string]any{"type": "string"},
				"title":   map[string]any{"type": "string"},
				"node_id": map[string]any{"type": "string"},
				"status":  map[string]any{"type": "string"},
				"result":  map[string]any{"type": "string"},
				"error":   map[string]any{"type": "string"},
				"nodes": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":          map[string]any{"type": "string"},
							"title":       map[string]any{"type": "string"},
							"role":        map[string]any{"type": "string"},
							"category":    map[string]any{"type": "string"},
							"depends_on":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							"write_scope": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
						"required": []string{"id"},
					},
				},
			},
			"required": []string{"action"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			runID := paramString(params, "run_id")
			switch action {
			case "create":
				nodes := parseDAGNodes(params["nodes"])
				run, layers, err := dags.Create(resolveSessionID(ctx), runID, paramString(params, "title"), nodes)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":     true,
					"dag":    run,
					"layers": layers,
					"ready":  ReadyNodes(run),
				}, false), nil
			case "list":
				runs, err := dags.List()
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "dags": runs, "count": len(runs)}, false), nil
			case "get":
				run, err := dags.Get(runID)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				layers, _ := ValidateDAG(run.Nodes)
				return jsonToolResult(map[string]any{
					"ok":     true,
					"dag":    run,
					"layers": layers,
					"ready":  ReadyNodes(run),
				}, false), nil
			case "ready":
				run, err := dags.Get(runID)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":     true,
					"run_id": run.RunID,
					"status": run.Status,
					"ready":  ReadyNodes(run),
				}, false), nil
			case "update_node":
				run, err := dags.UpdateNode(runID, paramString(params, "node_id"), paramString(params, "status"), paramString(params, "result"), paramString(params, "error"))
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":    true,
					"dag":   run,
					"ready": ReadyNodes(run),
				}, false), nil
			case "complete":
				run, err := dags.SetStatus(runID, "completed")
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "dag": run}, false), nil
			case "fail":
				run, err := dags.SetStatus(runID, "failed")
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "dag": run}, false), nil
			default:
				return errorToolResult(fmt.Sprintf("unsupported omh_dag action %q", action)), nil
			}
		},
	})

	// 2. omh_team
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_team",
		Label:         "OMH Team",
		Description:   "Create and track checked parallel team lanes, automatically latching subagent start/stop events.",
		PromptSnippet: "Track parallel team lanes with omh_team (create, status, record, list).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action":           map[string]any{"type": "string", "enum": []string{"create", "status", "record", "list"}},
				"team_id":          map[string]any{"type": "string"},
				"title":            map[string]any{"type": "string"},
				"lane_id":          map[string]any{"type": "string"},
				"status":           map[string]any{"type": "string"},
				"result":           map[string]any{"type": "string"},
				"child_session_id": map[string]any{"type": "string"},
				"lanes": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"lane_id":  map[string]any{"type": "string"},
							"title":    map[string]any{"type": "string"},
							"role":     map[string]any{"type": "string"},
							"category": map[string]any{"type": "string"},
						},
					},
				},
			},
			"required": []string{"action"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			teamID := paramString(params, "team_id")
			switch action {
			case "create":
				lanes := parseTeamLanes(params["lanes"])
				rec, err := teams.Create(resolveSessionID(ctx), teamID, paramString(params, "title"), lanes)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "team": rec}, false), nil
			case "status":
				rec, err := teams.Get(teamID)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "team": rec}, false), nil
			case "record":
				rec, err := teams.RecordLane(teamID, paramString(params, "lane_id"), paramString(params, "status"), paramString(params, "result"), paramString(params, "child_session_id"))
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "team": rec}, false), nil
			case "list":
				list, err := teams.List()
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "teams": list, "count": len(list)}, false), nil
			default:
				return errorToolResult(fmt.Sprintf("unsupported omh_team action %q", action)), nil
			}
		},
	})

	// 3. omh_loop
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_loop",
		Label:         "OMH Loop",
		Description:   "Manage durable bounded iteration loops with permission scopes and max-iteration caps under $OMH_HOME/runtime/loops/.",
		PromptSnippet: "Control bounded iteration loops with omh_loop (create, status, step, stop, list).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action":           map[string]any{"type": "string", "enum": []string{"create", "status", "step", "stop", "list"}},
				"loop_id":          map[string]any{"type": "string"},
				"goal":             map[string]any{"type": "string"},
				"permission_scope": map[string]any{"type": "string", "enum": []string{"read-only", "workspace-write", "full"}},
				"max_iterations":   map[string]any{"type": "integer"},
				"note":             map[string]any{"type": "string"},
				"reason":           map[string]any{"type": "string"},
			},
			"required": []string{"action"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			loopID := paramString(params, "loop_id")
			switch action {
			case "create":
				rec, err := loops.Create(
					resolveSessionID(ctx),
					loopID,
					paramString(params, "goal"),
					paramString(params, "permission_scope"),
					paramInt(params, "max_iterations", 5),
				)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "loop": rec}, false), nil
			case "status":
				rec, err := loops.Get(loopID)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "loop": rec}, false), nil
			case "step":
				rec, err := loops.Step(loopID, paramString(params, "note"))
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "loop": rec}, false), nil
			case "stop":
				rec, err := loops.Stop(loopID, paramString(params, "reason"))
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "loop": rec}, false), nil
			case "list":
				list, err := loops.List()
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "loops": list, "count": len(list)}, false), nil
			default:
				return errorToolResult(fmt.Sprintf("unsupported omh_loop action %q", action)), nil
			}
		},
	})
}

func parseDAGNodes(raw any) []DAGNode {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]DAGNode, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		node := DAGNode{
			ID:         paramString(m, "id"),
			Title:      paramString(m, "title"),
			Role:       paramString(m, "role"),
			Category:   paramString(m, "category"),
			DependsOn:  paramStringSlice(m, "depends_on"),
			WriteScope: paramStringSlice(m, "write_scope"),
		}
		out = append(out, node)
	}
	return out
}

func parseTeamLanes(raw any) []TeamLane {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]TeamLane, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, TeamLane{
			LaneID:   paramString(m, "lane_id"),
			Title:    paramString(m, "title"),
			Role:     paramString(m, "role"),
			Category: paramString(m, "category"),
		})
	}
	return out
}
