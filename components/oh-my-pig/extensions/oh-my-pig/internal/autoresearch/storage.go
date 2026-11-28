package autoresearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var (
	leadingSlashRe = regexp.MustCompile(`^[/\\]`)
	pathSepRe      = regexp.MustCompile(`[/\\:]`)
)

// EncodeProjectKey encodes an absolute project path into a single filesystem-safe segment.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:17-19
func EncodeProjectKey(repoRoot string) string {
	s := leadingSlashRe.ReplaceAllString(repoRoot, "")
	s = pathSepRe.ReplaceAllString(s, "-")
	return "--" + s + "--"
}

// SessionRow represents a persisted autoresearch session.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:21-40
type SessionRow struct {
	ID               int             `json:"id"`
	Name             string          `json:"name"`
	Goal             *string         `json:"goal"`
	PrimaryMetric    string          `json:"primaryMetric"`
	MetricUnit       string          `json:"metricUnit"`
	Direction        MetricDirection `json:"direction"`
	PreferredCommand *string         `json:"preferredCommand"`
	Branch           *string         `json:"branch"`
	BaselineCommit   *string         `json:"baselineCommit"`
	CurrentSegment   int             `json:"currentSegment"`
	MaxIterations    *int            `json:"maxIterations"`
	ScopePaths       []string        `json:"scopePaths"`
	OffLimits        []string        `json:"offLimits"`
	Constraints      []string        `json:"constraints"`
	SecondaryMetrics []string        `json:"secondaryMetrics"`
	Notes            string          `json:"notes"`
	CreatedAt        int64           `json:"createdAt"`
	ClosedAt         *int64          `json:"closedAt"`
}

// RunRow represents a persisted benchmark run and optional logged experiment outcome.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:42-71
type RunRow struct {
	ID               int               `json:"id"`
	SessionID        int               `json:"sessionId"`
	Segment          int               `json:"segment"`
	Command          string            `json:"command"`
	StartedAt        int64             `json:"startedAt"`
	CompletedAt      *int64            `json:"completedAt"`
	DurationMs       *int64            `json:"durationMs"`
	ExitCode         *int              `json:"exitCode"`
	TimedOut         bool              `json:"timedOut"`
	ParsedPrimary    *float64          `json:"parsedPrimary"`
	ParsedMetrics    NumericMetricMap  `json:"parsedMetrics"`
	ParsedASI        ASIData           `json:"parsedAsi"`
	PreRunDirtyPaths []string          `json:"preRunDirtyPaths"`
	LogPath          string            `json:"logPath"`
	Status           *ExperimentStatus `json:"status"`
	Description      *string           `json:"description"`
	Metric           *float64          `json:"metric"`
	Metrics          NumericMetricMap  `json:"metrics"`
	ASI              ASIData           `json:"asi"`
	CommitHash       *string           `json:"commitHash"`
	Confidence       *float64          `json:"confidence"`
	ModifiedPaths    []string          `json:"modifiedPaths"`
	ScopeDeviations  []string          `json:"scopeDeviations"`
	Justification    *string           `json:"justification"`
	Flagged          bool              `json:"flagged"`
	FlaggedReason    *string           `json:"flaggedReason"`
	LoggedAt         *int64            `json:"loggedAt"`
	AbandonedAt      *int64            `json:"abandonedAt"`
}

// OpenSessionParams configures a newly opened autoresearch session.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:73-87
type OpenSessionParams struct {
	Name             string
	Goal             *string
	PrimaryMetric    string
	MetricUnit       string
	Direction        MetricDirection
	PreferredCommand *string
	Branch           *string
	BaselineCommit   *string
	MaxIterations    *int
	ScopePaths       []string
	OffLimits        []string
	Constraints      []string
	SecondaryMetrics []string
}

// UpdateSessionParams carries optional field updates for an existing session.
// Fields use presence flags or pointer-to-pointer semantics so callers can distinguish
// "unset" from "set to nil".
// upstream: packages/coding-agent/src/autoresearch/storage.ts:89-103
type UpdateSessionParams struct {
	HasGoal             bool
	Goal                *string
	HasPreferredCommand bool
	PreferredCommand    *string
	HasMaxIterations    bool
	MaxIterations       *int
	HasScopePaths       bool
	ScopePaths          []string
	HasOffLimits        bool
	OffLimits           []string
	HasConstraints      bool
	Constraints         []string
	HasSecondaryMetrics bool
	SecondaryMetrics    []string
	HasPrimaryMetric    bool
	PrimaryMetric       string
	HasMetricUnit       bool
	MetricUnit          string
	HasDirection        bool
	Direction           MetricDirection
	HasBranch           bool
	Branch              *string
	HasBaselineCommit   bool
	BaselineCommit      *string
	HasNotes            bool
	Notes               string
}

// InsertRunParams creates a pending run row.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:105-112
type InsertRunParams struct {
	SessionID        int
	Segment          int
	Command          string
	LogPath          string
	PreRunDirtyPaths []string
	StartedAt        int64
}

// MarkRunCompletedParams records benchmark process completion fields.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:114-123
type MarkRunCompletedParams struct {
	RunID         int
	CompletedAt   int64
	DurationMs    int64
	ExitCode      *int
	TimedOut      bool
	ParsedPrimary *float64
	ParsedMetrics NumericMetricMap
	ParsedASI     ASIData
}

// MarkRunLoggedParams records log_experiment outcome fields.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:125-138
type MarkRunLoggedParams struct {
	RunID           int
	Status          ExperimentStatus
	Description     string
	Metric          float64
	Metrics         NumericMetricMap
	ASI             ASIData
	CommitHash      *string
	Confidence      *float64
	ModifiedPaths   []string
	ScopeDeviations []string
	Justification   *string
	LoggedAt        int64
}

type storeSnapshot struct {
	SchemaVersion int          `json:"schemaVersion"`
	NextSessionID int          `json:"nextSessionId"`
	NextRunID     int          `json:"nextRunId"`
	Sessions      []SessionRow `json:"sessions"`
}

// Storage is a crash-safe, stdlib-only JSON + JSONL store backing autoresearch sessions
// and runs. It mirrors the logical schema and API of upstream `AutoresearchStorage`
// (which uses `bun:sqlite` WAL).
//
// Disk layout under `<projectDir>`:
//   - `state.json` — atomic temp+rename snapshot of sessions & sequence counters
//   - `runs.jsonl` — append-only JSONL journal of run mutations (replayed by run ID,
//     last write wins; torn trailing lines are ignored on recovery)
//   - `runs/0001/benchmark.log` — per-run captured benchmark output
//
// upstream: packages/coding-agent/src/autoresearch/storage.ts:255-550
type Storage struct {
	mu            sync.RWMutex
	projectDir    string
	statePath     string
	runsLogPath   string
	nextSessionID int
	nextRunID     int
	sessions      map[int]*SessionRow
	runs          map[int]*RunRow
	closed        bool
}

const currentSchemaVersion = 1

// NewStorage opens (or creates) a JSON+JSONL autoresearch store in projectDir.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:260-273
func NewStorage(projectDir string) (*Storage, error) {
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return nil, fmt.Errorf("autoresearch storage mkdir: %w", err)
	}
	s := &Storage{
		projectDir:    projectDir,
		statePath:     filepath.Join(projectDir, "state.json"),
		runsLogPath:   filepath.Join(projectDir, "runs.jsonl"),
		nextSessionID: 1,
		nextRunID:     1,
		sessions:      make(map[int]*SessionRow),
		runs:          make(map[int]*RunRow),
	}
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.statePath); errors.Is(err, os.ErrNotExist) {
		if err := s.persistStateLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// ProjectDir returns the storage root directory (also where `runs/NNNN/benchmark.log` lives).
func (s *Storage) ProjectDir() string { return s.projectDir }

// StatePath returns the path to state.json.
func (s *Storage) StatePath() string { return s.statePath }

// Close marks the storage closed.
func (s *Storage) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *Storage) loadLocked() error {
	if data, err := os.ReadFile(s.statePath); err == nil && len(data) > 0 {
		var snap storeSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			return fmt.Errorf("autoresearch corrupt state.json: %w", err)
		}
		if snap.NextSessionID > s.nextSessionID {
			s.nextSessionID = snap.NextSessionID
		}
		if snap.NextRunID > s.nextRunID {
			s.nextRunID = snap.NextRunID
		}
		for i := range snap.Sessions {
			row := cloneSession(&snap.Sessions[i])
			normalizeSessionRow(row)
			s.sessions[row.ID] = row
			if row.ID >= s.nextSessionID {
				s.nextSessionID = row.ID + 1
			}
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("autoresearch read state.json: %w", err)
	}

	f, err := os.Open(s.runsLogPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("autoresearch read runs.jsonl: %w", err)
	}

	reader := bufio.NewReaderSize(f, 64*1024)
	var offset int64
	var validEnd int64
	needsTruncate := false
	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			_ = f.Close()
			return fmt.Errorf("autoresearch scan runs.jsonl: %w", readErr)
		}
		lineLen := int64(len(lineBytes))
		if lineLen == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		trimmed := bytes.TrimSpace(lineBytes)
		if len(trimmed) == 0 {
			offset += lineLen
			validEnd = offset
			if errors.Is(readErr, io.EOF) {
				break
			}
			continue
		}
		var row RunRow
		if unmarshalErr := json.Unmarshal(trimmed, &row); unmarshalErr != nil {
			if errors.Is(readErr, io.EOF) {
				// Crash recovery: torn/truncated trailing line — truncate file back to validEnd
				// so subsequent appends never concatenate onto corrupt trailing bytes.
				needsTruncate = true
				break
			}
			_ = f.Close()
			return fmt.Errorf("autoresearch corrupt runs.jsonl at offset %d: %w", offset, unmarshalErr)
		}
		if row.ID > 0 {
			cp := cloneRun(&row)
			normalizeRunRow(cp)
			s.runs[cp.ID] = cp
			if cp.ID >= s.nextRunID {
				s.nextRunID = cp.ID + 1
			}
		}
		offset += lineLen
		validEnd = offset
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("autoresearch close runs.jsonl: %w", closeErr)
	}
	if needsTruncate {
		if truncErr := os.Truncate(s.runsLogPath, validEnd); truncErr != nil {
			return fmt.Errorf("autoresearch truncate torn runs.jsonl: %w", truncErr)
		}
	}
	return nil
}

func (s *Storage) persistStateLocked() error {
	sessions := make([]SessionRow, 0, len(s.sessions))
	for _, row := range s.sessions {
		sessions = append(sessions, *cloneSession(row))
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	snap := storeSnapshot{
		SchemaVersion: currentSchemaVersion,
		NextSessionID: s.nextSessionID,
		NextRunID:     s.nextRunID,
		Sessions:      sessions,
	}
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return atomicWriteFile(s.statePath, raw)
}

func (s *Storage) appendRunLocked(row *RunRow) error {
	raw, err := json.Marshal(row)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	f, err := os.OpenFile(s.runsLogPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	var preSize int64
	if info, statErr := f.Stat(); statErr == nil {
		preSize = info.Size()
		// Guarantee a newline boundary if an existing file does not end with '\n'.
		if preSize > 0 {
			var last [1]byte
			if _, rerr := f.ReadAt(last[:], preSize-1); rerr == nil && last[0] != '\n' {
				raw = append([]byte{'\n'}, raw...)
			}
		}
	}
	if n, err := f.Write(raw); err != nil || n != len(raw) {
		_ = f.Truncate(preSize)
		_ = f.Close()
		if err != nil {
			return err
		}
		return io.ErrShortWrite
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func atomicWriteFile(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// GetActiveSession returns the most recent unclosed session, or nil.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:287-293
func (s *Storage) GetActiveSession() *SessionRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *SessionRow
	for _, row := range s.sessions {
		if row.ClosedAt != nil {
			continue
		}
		if best == nil || row.ID > best.ID {
			best = row
		}
	}
	return cloneSession(best)
}

// GetActiveSessionForBranch returns the most recent unclosed session matching branch.
// A nil branch matches only sessions whose recorded branch is nil.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:295-311
func (s *Storage) GetActiveSessionForBranch(branch *string) *SessionRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *SessionRow
	for _, row := range s.sessions {
		if row.ClosedAt != nil {
			continue
		}
		if !stringPtrEqual(row.Branch, branch) {
			continue
		}
		if best == nil || row.ID > best.ID {
			best = row
		}
	}
	return cloneSession(best)
}

// GetSessionByID returns the session with the given ID, or nil.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:313-317
func (s *Storage) GetSessionByID(sessionID int) *SessionRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSession(s.sessions[sessionID])
}

// OpenSession inserts and persists a new session.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:319-348
func (s *Storage) OpenSession(params OpenSessionParams) (*SessionRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := params.Direction
	if dir != DirectionHigher {
		dir = DirectionLower
	}
	row := &SessionRow{
		ID:               s.nextSessionID,
		Name:             params.Name,
		Goal:             cloneStringPtr(params.Goal),
		PrimaryMetric:    params.PrimaryMetric,
		MetricUnit:       params.MetricUnit,
		Direction:        dir,
		PreferredCommand: cloneStringPtr(params.PreferredCommand),
		Branch:           cloneStringPtr(params.Branch),
		BaselineCommit:   cloneStringPtr(params.BaselineCommit),
		CurrentSegment:   0,
		MaxIterations:    cloneIntPtr(params.MaxIterations),
		ScopePaths:       cloneStringSlice(params.ScopePaths),
		OffLimits:        cloneStringSlice(params.OffLimits),
		Constraints:      cloneStringSlice(params.Constraints),
		SecondaryMetrics: cloneStringSlice(params.SecondaryMetrics),
		Notes:            "",
		CreatedAt:        time.Now().UnixMilli(),
		ClosedAt:         nil,
	}
	s.nextSessionID++
	s.sessions[row.ID] = row
	if err := s.persistStateLocked(); err != nil {
		return nil, err
	}
	return cloneSession(row), nil
}

// UpdateSession applies partial updates to an existing session and persists state.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:350-412
func (s *Storage) UpdateSession(sessionID int, updates UpdateSessionParams) (*SessionRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %d not found", sessionID)
	}
	if updates.HasGoal {
		row.Goal = cloneStringPtr(updates.Goal)
	}
	if updates.HasPreferredCommand {
		row.PreferredCommand = cloneStringPtr(updates.PreferredCommand)
	}
	if updates.HasMaxIterations {
		row.MaxIterations = cloneIntPtr(updates.MaxIterations)
	}
	if updates.HasScopePaths {
		row.ScopePaths = cloneStringSlice(updates.ScopePaths)
	}
	if updates.HasOffLimits {
		row.OffLimits = cloneStringSlice(updates.OffLimits)
	}
	if updates.HasConstraints {
		row.Constraints = cloneStringSlice(updates.Constraints)
	}
	if updates.HasSecondaryMetrics {
		row.SecondaryMetrics = cloneStringSlice(updates.SecondaryMetrics)
	}
	if updates.HasPrimaryMetric {
		row.PrimaryMetric = updates.PrimaryMetric
	}
	if updates.HasMetricUnit {
		row.MetricUnit = updates.MetricUnit
	}
	if updates.HasDirection {
		if updates.Direction == DirectionHigher {
			row.Direction = DirectionHigher
		} else {
			row.Direction = DirectionLower
		}
	}
	if updates.HasBranch {
		row.Branch = cloneStringPtr(updates.Branch)
	}
	if updates.HasBaselineCommit {
		row.BaselineCommit = cloneStringPtr(updates.BaselineCommit)
	}
	if updates.HasNotes {
		row.Notes = updates.Notes
	}
	if err := s.persistStateLocked(); err != nil {
		return nil, err
	}
	return cloneSession(row), nil
}

// BumpSegment increments CurrentSegment for the given session.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:414-419
func (s *Storage) BumpSegment(sessionID int) (*SessionRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %d not found", sessionID)
	}
	row.CurrentSegment++
	if err := s.persistStateLocked(); err != nil {
		return nil, err
	}
	return cloneSession(row), nil
}

// CloseSession marks a session closed.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:421-423
func (s *Storage) CloseSession(sessionID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	now := time.Now().UnixMilli()
	row.ClosedAt = &now
	return s.persistStateLocked()
}

// InsertRun creates a new pending run row.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:425-441
func (s *Storage) InsertRun(params InsertRunParams) (*RunRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := &RunRow{
		ID:               s.nextRunID,
		SessionID:        params.SessionID,
		Segment:          params.Segment,
		Command:          params.Command,
		StartedAt:        params.StartedAt,
		PreRunDirtyPaths: cloneStringSlice(params.PreRunDirtyPaths),
		LogPath:          params.LogPath,
	}
	s.nextRunID++
	s.runs[row.ID] = row
	if err := s.appendRunLocked(row); err != nil {
		return nil, err
	}
	if err := s.persistStateLocked(); err != nil {
		return nil, err
	}
	return cloneRun(row), nil
}

// UpdateRunLogPath patches LogPath on an existing run.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:443-446
func (s *Storage) UpdateRunLogPath(runID int, logPath string) (*RunRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %d not found", runID)
	}
	row.LogPath = logPath
	if err := s.appendRunLocked(row); err != nil {
		return nil, err
	}
	return cloneRun(row), nil
}

// UpdateRunConfidence updates the confidence score on a run.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:448-451
func (s *Storage) UpdateRunConfidence(runID int, confidence *float64) (*RunRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %d not found", runID)
	}
	row.Confidence = cloneFloatPtr(confidence)
	if err := s.appendRunLocked(row); err != nil {
		return nil, err
	}
	return cloneRun(row), nil
}

// MarkRunCompleted records completion status and parsed metrics/ASI on a run.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:453-472
func (s *Storage) MarkRunCompleted(params MarkRunCompletedParams) (*RunRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.runs[params.RunID]
	if !ok {
		return nil, fmt.Errorf("run %d not found", params.RunID)
	}
	completedAt := params.CompletedAt
	durationMs := params.DurationMs
	row.CompletedAt = &completedAt
	row.DurationMs = &durationMs
	row.ExitCode = cloneIntPtr(params.ExitCode)
	row.TimedOut = params.TimedOut
	row.ParsedPrimary = cloneFloatPtr(params.ParsedPrimary)
	if params.ParsedMetrics != nil {
		row.ParsedMetrics = EnsureNumericMetricMap(params.ParsedMetrics)
	} else {
		row.ParsedMetrics = nil
	}
	row.ParsedASI = cloneASI(params.ParsedASI)
	if err := s.appendRunLocked(row); err != nil {
		return nil, err
	}
	return cloneRun(row), nil
}

// MarkRunLogged records the final experiment decision on a run.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:474-498
func (s *Storage) MarkRunLogged(params MarkRunLoggedParams) (*RunRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.runs[params.RunID]
	if !ok {
		return nil, fmt.Errorf("run %d not found", params.RunID)
	}
	row.Status = new(params.Status)
	row.Description = new(params.Description)
	row.Metric = new(params.Metric)
	row.Metrics = EnsureNumericMetricMap(params.Metrics)
	row.ASI = cloneASI(params.ASI)
	row.CommitHash = cloneStringPtr(params.CommitHash)
	row.Confidence = cloneFloatPtr(params.Confidence)
	row.ModifiedPaths = cloneStringSlice(params.ModifiedPaths)
	row.ScopeDeviations = cloneStringSlice(params.ScopeDeviations)
	row.Justification = cloneStringPtr(params.Justification)
	row.LoggedAt = new(params.LoggedAt)
	if err := s.appendRunLocked(row); err != nil {
		return nil, err
	}
	return cloneRun(row), nil
}

// FlagRun marks a run as suspect so it is excluded from baseline and best-metric math.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:500-503
func (s *Storage) FlagRun(runID int, reason string) (*RunRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %d not found", runID)
	}
	row.Flagged = true
	r := reason
	row.FlaggedReason = &r
	if err := s.appendRunLocked(row); err != nil {
		return nil, err
	}
	return cloneRun(row), nil
}

// AbandonPendingRuns marks all unlogged, non-abandoned runs for sessionID as abandoned.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:505-517
func (s *Storage) AbandonPendingRuns(sessionID int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	count := 0
	var toUpdate []*RunRow
	for _, row := range s.runs {
		if row.SessionID == sessionID && row.Status == nil && row.AbandonedAt == nil {
			toUpdate = append(toUpdate, row)
		}
	}
	sort.Slice(toUpdate, func(i, j int) bool { return toUpdate[i].ID < toUpdate[j].ID })
	for _, row := range toUpdate {
		ts := now
		row.AbandonedAt = &ts
		if err := s.appendRunLocked(row); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// GetPendingRun returns the latest unlogged, non-abandoned run for sessionID, or nil.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:519-525
func (s *Storage) GetPendingRun(sessionID int) *RunRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *RunRow
	for _, row := range s.runs {
		if row.SessionID != sessionID || row.Status != nil || row.AbandonedAt != nil {
			continue
		}
		if best == nil || row.ID > best.ID {
			best = row
		}
	}
	return cloneRun(best)
}

// GetRunByID returns the run with the given ID, or nil.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:527-531
func (s *Storage) GetRunByID(runID int) *RunRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRun(s.runs[runID])
}

// ListRuns returns all runs for sessionID sorted by ascending ID.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:539-542
func (s *Storage) ListRuns(sessionID int) []RunRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []RunRow
	for _, row := range s.runs {
		if row.SessionID == sessionID {
			out = append(out, *cloneRun(row))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListLoggedRuns returns all logged (non-nil Status) runs for sessionID sorted by ascending ID.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:544-549
func (s *Storage) ListLoggedRuns(sessionID int) []RunRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []RunRow
	for _, row := range s.runs {
		if row.SessionID == sessionID && row.Status != nil {
			out = append(out, *cloneRun(row))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ── Storage cache & path resolution ──────────────────────────────────────────

var (
	storageCacheMu sync.Mutex
	storageCache   = map[string]*Storage{}
)

// ResolveAutoresearchProjectDir resolves the per-project storage directory for cwd.
// Respects OMP_AUTORESEARCH_DB_DIR, PIG_CODING_AGENT_DIR, sdk.AgentDir(), and ~/.pig/agent.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:574-588
func ResolveAutoresearchProjectDir(ctx context.Context, cwd string) (string, error) {
	repoRoot := GitRepoRoot(ctx, cwd)
	if repoRoot == "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			repoRoot = abs
		} else {
			repoRoot = cwd
		}
	}
	encoded := EncodeProjectKey(repoRoot)

	if override := strings.TrimSpace(os.Getenv("OMP_AUTORESEARCH_DB_DIR")); override != "" {
		return filepath.Join(override, encoded), nil
	}
	baseDir, err := resolveAgentBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "autoresearch", encoded), nil
}

func resolveAgentBaseDir() (string, error) {
	if envDir := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); envDir != "" {
		return envDir, nil
	}
	if dir, err := sdk.AgentDir(); err == nil && strings.TrimSpace(dir) != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pig", "agent"), nil
}

// OpenAutoresearchStorage opens or creates the project storage for cwd.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:554-562
func OpenAutoresearchStorage(ctx context.Context, cwd string) (*Storage, error) {
	projectDir, err := ResolveAutoresearchProjectDir(ctx, cwd)
	if err != nil {
		return nil, err
	}
	storageCacheMu.Lock()
	defer storageCacheMu.Unlock()
	if cached, ok := storageCache[projectDir]; ok && !cached.closed {
		return cached, nil
	}
	s, err := NewStorage(projectDir)
	if err != nil {
		return nil, err
	}
	storageCache[projectDir] = s
	return s, nil
}

// OpenAutoresearchStorageIfExists opens the project storage only if state.json already exists.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:564-572
func OpenAutoresearchStorageIfExists(ctx context.Context, cwd string) (*Storage, error) {
	projectDir, err := ResolveAutoresearchProjectDir(ctx, cwd)
	if err != nil {
		return nil, err
	}
	storageCacheMu.Lock()
	defer storageCacheMu.Unlock()
	if cached, ok := storageCache[projectDir]; ok && !cached.closed {
		return cached, nil
	}
	statePath := filepath.Join(projectDir, "state.json")
	if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	s, err := NewStorage(projectDir)
	if err != nil {
		return nil, err
	}
	storageCache[projectDir] = s
	return s, nil
}

// CloseAllAutoresearchStorages closes and clears all cached Storage instances.
// upstream: packages/coding-agent/src/autoresearch/storage.ts:590-602
func CloseAllAutoresearchStorages() {
	storageCacheMu.Lock()
	defer storageCacheMu.Unlock()
	for _, s := range storageCache {
		s.Close()
	}
	storageCache = map[string]*Storage{}
}

// ── Cloning & pointer helpers ────────────────────────────────────────────────

func normalizeSessionRow(row *SessionRow) {
	if row.Direction != DirectionHigher {
		row.Direction = DirectionLower
	}
	if row.ScopePaths == nil {
		row.ScopePaths = []string{}
	}
	if row.OffLimits == nil {
		row.OffLimits = []string{}
	}
	if row.Constraints == nil {
		row.Constraints = []string{}
	}
	if row.SecondaryMetrics == nil {
		row.SecondaryMetrics = []string{}
	}
}

func normalizeRunRow(row *RunRow) {
	if row.PreRunDirtyPaths == nil {
		row.PreRunDirtyPaths = []string{}
	}
}

func cloneSession(in *SessionRow) *SessionRow {
	if in == nil {
		return nil
	}
	cp := *in
	cp.Goal = cloneStringPtr(in.Goal)
	cp.PreferredCommand = cloneStringPtr(in.PreferredCommand)
	cp.Branch = cloneStringPtr(in.Branch)
	cp.BaselineCommit = cloneStringPtr(in.BaselineCommit)
	cp.MaxIterations = cloneIntPtr(in.MaxIterations)
	cp.ScopePaths = cloneStringSlice(in.ScopePaths)
	cp.OffLimits = cloneStringSlice(in.OffLimits)
	cp.Constraints = cloneStringSlice(in.Constraints)
	cp.SecondaryMetrics = cloneStringSlice(in.SecondaryMetrics)
	cp.ClosedAt = cloneInt64Ptr(in.ClosedAt)
	return &cp
}

func cloneRun(in *RunRow) *RunRow {
	if in == nil {
		return nil
	}
	cp := *in
	cp.CompletedAt = cloneInt64Ptr(in.CompletedAt)
	cp.DurationMs = cloneInt64Ptr(in.DurationMs)
	cp.ExitCode = cloneIntPtr(in.ExitCode)
	cp.ParsedPrimary = cloneFloatPtr(in.ParsedPrimary)
	if in.ParsedMetrics != nil {
		cp.ParsedMetrics = EnsureNumericMetricMap(in.ParsedMetrics)
	}
	cp.ParsedASI = cloneASI(in.ParsedASI)
	cp.PreRunDirtyPaths = cloneStringSlice(in.PreRunDirtyPaths)
	if in.Status != nil {
		st := *in.Status
		cp.Status = &st
	}
	cp.Description = cloneStringPtr(in.Description)
	cp.Metric = cloneFloatPtr(in.Metric)
	if in.Metrics != nil {
		cp.Metrics = EnsureNumericMetricMap(in.Metrics)
	}
	cp.ASI = cloneASI(in.ASI)
	cp.CommitHash = cloneStringPtr(in.CommitHash)
	cp.Confidence = cloneFloatPtr(in.Confidence)
	if in.ModifiedPaths != nil {
		cp.ModifiedPaths = cloneStringSlice(in.ModifiedPaths)
	}
	if in.ScopeDeviations != nil {
		cp.ScopeDeviations = cloneStringSlice(in.ScopeDeviations)
	}
	cp.Justification = cloneStringPtr(in.Justification)
	cp.FlaggedReason = cloneStringPtr(in.FlaggedReason)
	cp.LoggedAt = cloneInt64Ptr(in.LoggedAt)
	cp.AbandonedAt = cloneInt64Ptr(in.AbandonedAt)
	return &cp
}

func cloneStringPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneInt64Ptr(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneFloatPtr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func stringPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
