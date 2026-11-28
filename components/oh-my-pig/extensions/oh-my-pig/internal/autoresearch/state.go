package autoresearch

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

// CreateExperimentState returns a fresh empty ExperimentState.
// upstream: packages/coding-agent/src/autoresearch/state.ts:19-40
func CreateExperimentState() ExperimentState {
	return ExperimentState{
		Results:          []ExperimentResult{},
		BestMetric:       nil,
		BestDirection:    DirectionLower,
		MetricName:       "metric",
		MetricUnit:       "",
		SecondaryMetrics: []MetricDef{},
		Name:             nil,
		Goal:             nil,
		CurrentSegment:   0,
		MaxExperiments:   nil,
		Confidence:       nil,
		ScopePaths:       []string{},
		OffLimits:        []string{},
		Constraints:      []string{},
		Notes:            "",
		Branch:           nil,
		BaselineCommit:   nil,
		SessionID:        nil,
	}
}

// CreateSessionRuntime returns a fresh per-session autoresearch runtime.
// upstream: packages/coding-agent/src/autoresearch/state.ts:42-57
func CreateSessionRuntime() *Runtime {
	return &Runtime{
		State: CreateExperimentState(),
	}
}

// IsBetter compares current against best using direction.
// upstream: packages/tui/src/apps/autoresearch-data.ts:15-17
func IsBetter(current, best float64, direction MetricDirection) bool {
	if direction == DirectionHigher {
		return current > best
	}
	return current < best
}

// CurrentResults returns all results belonging to segment.
// upstream: packages/tui/src/apps/autoresearch-data.ts:20-22
func CurrentResults(results []ExperimentResult, segment int) []ExperimentResult {
	var out []ExperimentResult
	for _, r := range results {
		if r.Segment == segment {
			out = append(out, r)
		}
	}
	return out
}

// FindBaselineResult finds the first unflagged kept experiment in segment.
// upstream: packages/tui/src/apps/autoresearch-data.ts:25-27
func FindBaselineResult(results []ExperimentResult, segment int) *ExperimentResult {
	for i := range results {
		r := &results[i]
		if r.Segment == segment && r.Status == StatusKeep && !r.Flagged {
			return r
		}
	}
	return nil
}

// FindBaselineMetric returns the baseline primary metric for segment, or nil.
// upstream: packages/tui/src/apps/autoresearch-data.ts:30-33
func FindBaselineMetric(results []ExperimentResult, segment int) *float64 {
	b := FindBaselineResult(results, segment)
	if b == nil {
		return nil
	}
	return new(b.Metric)
}

// FindBaselineRunNumber resolves the baseline run number in segment.
// upstream: packages/tui/src/apps/autoresearch-data.ts:36-42
func FindBaselineRunNumber(results []ExperimentResult, segment int) *int {
	b := FindBaselineResult(results, segment)
	if b == nil {
		return nil
	}
	if b.RunNumber != nil {
		return new(*b.RunNumber)
	}
	for i := range results {
		if &results[i] == b {
			return new(i + 1)
		}
	}
	return nil
}

// FindBaselineSecondary collects secondary baselines from the earliest unflagged samples.
// upstream: packages/tui/src/apps/autoresearch-data.ts:45-64
func FindBaselineSecondary(results []ExperimentResult, segment int, knownMetrics []MetricDef) NumericMetricMap {
	values := make(NumericMetricMap)
	if baseline := FindBaselineResult(results, segment); baseline != nil {
		for k, v := range baseline.Metrics {
			values[k] = v
		}
	}
	segResults := CurrentResults(results, segment)
	for _, metric := range knownMetrics {
		if _, ok := values[metric.Name]; ok {
			continue
		}
		for _, r := range segResults {
			if r.Flagged {
				continue
			}
			if v, ok := r.Metrics[metric.Name]; ok {
				values[metric.Name] = v
				break
			}
		}
	}
	return values
}

// FindBestKeptMetric returns the best unflagged kept metric in segment for direction.
// upstream: packages/coding-agent/src/autoresearch/state.ts:80-93
func FindBestKeptMetric(results []ExperimentResult, segment int, direction MetricDirection) *float64 {
	var best *float64
	for _, r := range CurrentResults(results, segment) {
		if r.Status != StatusKeep || r.Flagged {
			continue
		}
		if best == nil || IsBetter(r.Metric, *best, direction) {
			best = new(r.Metric)
		}
	}
	return best
}

// BestKeptResult returns the best unflagged kept ExperimentResult in segment for direction.
// upstream: packages/coding-agent/src/autoresearch/index.ts:517-533
func BestKeptResult(results []ExperimentResult, segment int, direction MetricDirection) *ExperimentResult {
	var best *ExperimentResult
	for i := range results {
		r := &results[i]
		if r.Segment != segment || r.Status != StatusKeep || r.Flagged {
			continue
		}
		if best == nil || IsBetter(r.Metric, best.Metric, direction) {
			best = r
		}
	}
	return best
}

// SortedMedian returns the median of values without mutating the input slice.
// upstream: packages/coding-agent/src/autoresearch/state.ts:95-103
func SortedMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// ComputeConfidence computes the MAD-based confidence score:
// `abs(bestKept - baseline) / MAD(current unflagged positive metrics)`.
// Returns nil when fewer than 3 valid runs exist, MAD == 0, baseline is nil,
// or bestKept equals baseline.
// upstream: packages/coding-agent/src/autoresearch/state.ts:105-131
func ComputeConfidence(results []ExperimentResult, segment int, direction MetricDirection) *float64 {
	var current []ExperimentResult
	for _, r := range CurrentResults(results, segment) {
		if !r.Flagged && r.Metric > 0 {
			current = append(current, r)
		}
	}
	if len(current) < 3 {
		return nil
	}
	values := make([]float64, len(current))
	for i, r := range current {
		values[i] = r.Metric
	}
	median := SortedMedian(values)
	deviations := make([]float64, len(values))
	for i, v := range values {
		deviations[i] = math.Abs(v - median)
	}
	mad := SortedMedian(deviations)
	if mad == 0 {
		return nil
	}
	baseline := FindBaselineMetric(results, segment)
	if baseline == nil {
		return nil
	}
	var bestKept *float64
	for _, r := range current {
		if r.Status != StatusKeep || r.Metric <= 0 {
			continue
		}
		if bestKept == nil || IsBetter(r.Metric, *bestKept, direction) {
			bestKept = new(r.Metric)
		}
	}
	if bestKept == nil || *bestKept == *baseline {
		return nil
	}
	return new(math.Abs(*bestKept-*baseline) / mad)
}

// BuildExperimentState rebuilds ExperimentState from persisted session and logged runs.
// upstream: packages/coding-agent/src/autoresearch/state.ts:133-179
func BuildExperimentState(session *SessionRow, loggedRuns []RunRow) ExperimentState {
	state := CreateExperimentState()
	if session == nil {
		return state
	}
	state.Name = new(session.Name)
	state.Goal = cloneStringPtr(session.Goal)
	state.MetricName = session.PrimaryMetric
	state.MetricUnit = session.MetricUnit
	state.BestDirection = session.Direction
	state.ScopePaths = cloneStringSlice(session.ScopePaths)
	state.OffLimits = cloneStringSlice(session.OffLimits)
	state.Constraints = cloneStringSlice(session.Constraints)
	state.Notes = session.Notes
	state.Branch = cloneStringPtr(session.Branch)
	state.BaselineCommit = cloneStringPtr(session.BaselineCommit)
	state.SessionID = new(session.ID)
	state.MaxExperiments = cloneIntPtr(session.MaxIterations)
	state.CurrentSegment = session.CurrentSegment
	state.SecondaryMetrics = make([]MetricDef, 0, len(session.SecondaryMetrics))
	for _, name := range session.SecondaryMetrics {
		state.SecondaryMetrics = append(state.SecondaryMetrics, MetricDef{
			Name: name,
			Unit: InferMetricUnitFromName(name),
		})
	}

	for _, run := range loggedRuns {
		if run.Status == nil {
			continue
		}
		commit := ""
		if run.CommitHash != nil {
			commit = *run.CommitHash
		}
		metric := 0.0
		if run.Metric != nil {
			metric = *run.Metric
		}
		metrics := make(NumericMetricMap)
		for k, v := range run.Metrics {
			metrics[k] = v
		}
		desc := ""
		if run.Description != nil {
			desc = *run.Description
		}
		ts := run.StartedAt
		if run.LoggedAt != nil {
			ts = *run.LoggedAt
		}
		result := ExperimentResult{
			RunNumber:       new(run.ID),
			Commit:          commit,
			Metric:          metric,
			Metrics:         metrics,
			Status:          *run.Status,
			Description:     desc,
			Timestamp:       ts,
			Segment:         run.Segment,
			Confidence:      cloneFloatPtr(run.Confidence),
			ASI:             cloneASI(run.ASI),
			ModifiedPaths:   cloneStringSlice(run.ModifiedPaths),
			ScopeDeviations: cloneStringSlice(run.ScopeDeviations),
			Justification:   cloneStringPtr(run.Justification),
			Flagged:         run.Flagged,
			FlaggedReason:   cloneStringPtr(run.FlaggedReason),
		}
		state.Results = append(state.Results, result)
		if run.Segment == state.CurrentSegment {
			registerSecondaryMetrics(&state.SecondaryMetrics, result.Metrics)
		}
	}

	state.BestMetric = FindBaselineMetric(state.Results, state.CurrentSegment)
	state.Confidence = ComputeConfidence(state.Results, state.CurrentSegment, state.BestDirection)
	return state
}

func registerSecondaryMetrics(metrics *[]MetricDef, values NumericMetricMap) {
	if len(values) == 0 {
		return
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		found := false
		for _, existing := range *metrics {
			if existing.Name == name {
				found = true
				break
			}
		}
		if !found {
			*metrics = append(*metrics, MetricDef{
				Name: name,
				Unit: InferMetricUnitFromName(name),
			})
		}
	}
}

// ReconstructControlState replays "autoresearch-control" custom entries from a session branch.
// ReconstructControlState replays "autoresearch-control" custom entries from raw session entries,
// optionally filtering to branchIDs when non-nil.
// upstream: packages/coding-agent/src/autoresearch/state.ts:181-197
func ReconstructControlState(entries []json.RawMessage, branchIDs map[string]bool) ReconstructedControlState {
	var out ReconstructedControlState
	for _, raw := range entries {
		var env struct {
			ID         string          `json:"id"`
			Type       string          `json:"type"`
			CustomType string          `json:"customType"`
			Data       json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		if env.Type != "custom" || env.CustomType != "autoresearch-control" {
			continue
		}
		if branchIDs != nil && env.ID != "" && !branchIDs[env.ID] {
			continue
		}
		data, ok := parseControlEntry(env.Data)
		if !ok {
			continue
		}
		out.LastMode = data.Mode
		out.AutoresearchMode = data.Mode == "on"
		if data.Goal != nil {
			out.Goal = cloneStringPtr(data.Goal)
		}
		if data.Mode == "clear" {
			out.Goal = nil
		}
	}
	return out
}
func parseControlEntry(raw json.RawMessage) (ControlEntryData, bool) {
	if len(raw) == 0 {
		return ControlEntryData{}, false
	}
	var candidate struct {
		Mode string `json:"mode"`
		Goal string `json:"goal"`
	}
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return ControlEntryData{}, false
	}
	if candidate.Mode != "on" && candidate.Mode != "off" && candidate.Mode != "clear" {
		return ControlEntryData{}, false
	}
	data := ControlEntryData{Mode: candidate.Mode}
	if strings.TrimSpace(candidate.Goal) != "" {
		data.Goal = new(candidate.Goal)
	}
	return data, true
}

// RuntimeStore maps session keys to their live Runtime.
// upstream: packages/coding-agent/src/autoresearch/state.ts:199-213
type RuntimeStore struct {
	mu       sync.Mutex
	runtimes map[string]*Runtime
}

// NewRuntimeStore creates an empty RuntimeStore.
func NewRuntimeStore() *RuntimeStore {
	return &RuntimeStore{
		runtimes: make(map[string]*Runtime),
	}
}

// Ensure returns the Runtime for sessionKey, creating one if absent.
func (s *RuntimeStore) Ensure(sessionKey string) *Runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.runtimes[sessionKey]; ok {
		return existing
	}
	rt := CreateSessionRuntime()
	s.runtimes[sessionKey] = rt
	return rt
}

// Clear removes sessionKey from the store.
func (s *RuntimeStore) Clear(sessionKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runtimes, sessionKey)
}

// FormatStatusTable renders a plain-text status summary of the active autoresearch
// session for `/autoresearch status` (replacing upstream's fullscreen TUI overlay).
func FormatStatusTable(rt *Runtime) string {
	if rt == nil || !rt.AutoresearchMode {
		return "Autoresearch mode is OFF."
	}
	st := rt.State
	var b strings.Builder
	name := "(setup phase — call init_experiment)"
	if st.Name != nil && *st.Name != "" {
		name = *st.Name
	}
	fmt.Fprintf(&b, "Autoresearch: %s\n", name)
	if rt.Goal != nil && strings.TrimSpace(*rt.Goal) != "" {
		fmt.Fprintf(&b, "Goal: %s\n", *rt.Goal)
	}
	if st.Branch != nil && *st.Branch != "" {
		fmt.Fprintf(&b, "Branch: %s\n", *st.Branch)
	}
	if st.BaselineCommit != nil && *st.BaselineCommit != "" {
		short := *st.BaselineCommit
		if len(short) > 12 {
			short = short[:12]
		}
		fmt.Fprintf(&b, "Baseline commit: %s\n", short)
	}
	if st.SessionID == nil {
		b.WriteString("Status: awaiting init_experiment (Phase 1: harness setup)\n")
		return strings.TrimRight(b.String(), "\n")
	}

	segResults := CurrentResults(st.Results, st.CurrentSegment)
	baseline := FindBaselineMetric(st.Results, st.CurrentSegment)
	best := FindBestKeptMetric(st.Results, st.CurrentSegment, st.BestDirection)
	fmt.Fprintf(&b, "Segment: %d | Runs: %d | Metric: %s (%s is better)\n",
		st.CurrentSegment+1, len(segResults), st.MetricName, st.BestDirection)
	if baseline != nil {
		fmt.Fprintf(&b, "Baseline: %s", FormatNum(baseline, st.MetricUnit))
		if best != nil {
			fmt.Fprintf(&b, " | Best kept: %s", FormatNum(best, st.MetricUnit))
		}
		if st.Confidence != nil {
			label := "within noise"
			if *st.Confidence >= 2 {
				label = "likely real"
			} else if *st.Confidence >= 1 {
				label = "marginal"
			}
			fmt.Fprintf(&b, " | Confidence: %.1fx (%s)", *st.Confidence, label)
		}
		b.WriteByte('\n')
	}
	if rt.LastRunSummary != nil {
		fmt.Fprintf(&b, "Pending run: #%d (%s)\n", rt.LastRunSummary.RunNumber, rt.LastRunSummary.Command)
	}
	if len(segResults) == 0 {
		b.WriteString("No runs logged in current segment yet.\n")
		return strings.TrimRight(b.String(), "\n")
	}
	b.WriteString("\nRun   Status         Metric          Description\n")
	b.WriteString("----  -------------  --------------  -----------\n")
	for i, r := range segResults {
		runNum := i + 1
		if r.RunNumber != nil {
			runNum = *r.RunNumber
		}
		status := string(r.Status)
		if r.Flagged {
			status += "*"
		}
		fmt.Fprintf(&b, "#%-3d  %-13s  %-14s  %s\n",
			runNum,
			status,
			FormatNum(&r.Metric, st.MetricUnit),
			r.Description,
		)
	}
	return strings.TrimRight(b.String(), "\n")
}
