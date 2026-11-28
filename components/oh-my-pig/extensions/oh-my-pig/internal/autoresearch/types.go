// Package autoresearch ports omp's builtin autoresearch extension onto PiG's Go SDK.
//
// upstream:
//   - packages/coding-agent/src/autoresearch/types.ts
//   - packages/coding-agent/src/autoresearch/helpers.ts
//   - packages/coding-agent/src/autoresearch/state.ts
//   - packages/coding-agent/src/autoresearch/storage.ts
//   - packages/coding-agent/src/autoresearch/git.ts
//   - packages/coding-agent/src/autoresearch/index.ts
//   - packages/coding-agent/src/autoresearch/tools/{init,run,log}-experiment.ts
//   - packages/coding-agent/src/autoresearch/tools/update-notes.ts
//   - packages/tui/src/tools/autoresearch.ts
//   - packages/tui/src/apps/autoresearch-data.ts
package autoresearch

// MetricDirection specifies whether a lower or higher metric value is better.
// upstream: packages/tui/src/tools/autoresearch.ts:14
type MetricDirection string

const (
	DirectionLower  MetricDirection = "lower"
	DirectionHigher MetricDirection = "higher"
)

// ExperimentStatus is the recorded disposition of an experiment run.
// upstream: packages/tui/src/tools/autoresearch.ts:17
type ExperimentStatus string

const (
	StatusKeep         ExperimentStatus = "keep"
	StatusDiscard      ExperimentStatus = "discard"
	StatusCrash        ExperimentStatus = "crash"
	StatusChecksFailed ExperimentStatus = "checks_failed"
)

// ASIData holds named structured experiment observations (JSON-compatible values).
// upstream: packages/tui/src/tools/autoresearch.ts:23-25
type ASIData map[string]any

// NumericMetricMap maps metric names to finite numeric values.
// upstream: packages/tui/src/tools/autoresearch.ts:28-30
type NumericMetricMap map[string]float64

// MetricDef pairs a metric name with its inferred or configured unit.
// upstream: packages/tui/src/tools/autoresearch.ts:33-36
type MetricDef struct {
	Name string `json:"name"`
	Unit string `json:"unit"`
}

// HarnessFilename is the canonical benchmark entrypoint filename.
// upstream: packages/tui/src/tools/autoresearch.ts:149
const HarnessFilename = "autoresearch.sh"

// DefaultHarnessCommand is the recorded command that executes the harness.
// upstream: packages/tui/src/tools/autoresearch.ts:152
const DefaultHarnessCommand = "bash " + HarnessFilename

// ExperimentToolNames lists the four autoresearch tools, gated OFF by default.
// upstream: packages/coding-agent/src/autoresearch/index.ts:30
var ExperimentToolNames = []string{
	"init_experiment",
	"run_experiment",
	"log_experiment",
	"update_notes",
}

// ExperimentResult records the metric and disposition of a logged experiment run.
// upstream: packages/tui/src/tools/autoresearch.ts:39-55
type ExperimentResult struct {
	RunNumber       *int             `json:"runNumber"`
	Commit          string           `json:"commit"`
	Metric          float64          `json:"metric"`
	Metrics         NumericMetricMap `json:"metrics"`
	Status          ExperimentStatus `json:"status"`
	Description     string           `json:"description"`
	Timestamp       int64            `json:"timestamp"`
	Segment         int              `json:"segment"`
	Confidence      *float64         `json:"confidence"`
	ASI             ASIData          `json:"asi,omitempty"`
	ModifiedPaths   []string         `json:"modifiedPaths"`
	ScopeDeviations []string         `json:"scopeDeviations"`
	Justification   *string          `json:"justification"`
	Flagged         bool             `json:"flagged"`
	FlaggedReason   *string          `json:"flaggedReason"`
}

// ExperimentState holds the current experiment baseline, history, and scope.
// upstream: packages/tui/src/tools/autoresearch.ts:58-77
type ExperimentState struct {
	Results          []ExperimentResult `json:"results"`
	BestMetric       *float64           `json:"bestMetric"`
	BestDirection    MetricDirection    `json:"bestDirection"`
	MetricName       string             `json:"metricName"`
	MetricUnit       string             `json:"metricUnit"`
	SecondaryMetrics []MetricDef        `json:"secondaryMetrics"`
	Name             *string            `json:"name"`
	Goal             *string            `json:"goal"`
	CurrentSegment   int                `json:"currentSegment"`
	MaxExperiments   *int               `json:"maxExperiments"`
	Confidence       *float64           `json:"confidence"`
	ScopePaths       []string           `json:"scopePaths"`
	OffLimits        []string           `json:"offLimits"`
	Constraints      []string           `json:"constraints"`
	Notes            string             `json:"notes"`
	Branch           *string            `json:"branch"`
	BaselineCommit   *string            `json:"baselineCommit"`
	SessionID        *int               `json:"sessionId"`
}

// PendingRunSummary summarizes an unlogged completed run.
// upstream: packages/coding-agent/src/autoresearch/types.ts:7-19
type PendingRunSummary struct {
	Command          string           `json:"command"`
	DurationSeconds  *float64         `json:"durationSeconds"`
	ParsedASI        ASIData          `json:"parsedAsi"`
	ParsedMetrics    NumericMetricMap `json:"parsedMetrics"`
	ParsedPrimary    *float64         `json:"parsedPrimary"`
	Passed           bool             `json:"passed"`
	PreRunDirtyPaths []string         `json:"preRunDirtyPaths"`
	RunDirectory     string           `json:"runDirectory"`
	RunNumber        int              `json:"runNumber"`
	ExitCode         *int             `json:"exitCode"`
	TimedOut         bool             `json:"timedOut"`
}

// RunningExperiment tracks an in-flight benchmark run.
// upstream: packages/coding-agent/src/autoresearch/types.ts:21-26
type RunningExperiment struct {
	StartedAt    int64  `json:"startedAt"`
	Command      string `json:"command"`
	RunDirectory string `json:"runDirectory"`
	RunNumber    int    `json:"runNumber"`
}

// Runtime holds per-session autoresearch runtime state.
// upstream: packages/coding-agent/src/autoresearch/types.ts:28-41
type Runtime struct {
	AutoresearchMode               bool               `json:"autoresearchMode"`
	AutoResumeArmed                bool               `json:"autoResumeArmed"`
	DashboardExpanded              bool               `json:"dashboardExpanded"`
	LastAutoResumePendingRunNumber *int               `json:"lastAutoResumePendingRunNumber"`
	LastRunDuration                *float64           `json:"lastRunDuration"`
	LastRunASI                     ASIData            `json:"lastRunAsi"`
	LastRunArtifactDir             *string            `json:"lastRunArtifactDir"`
	LastRunNumber                  *int               `json:"lastRunNumber"`
	LastRunSummary                 *PendingRunSummary `json:"lastRunSummary"`
	RunningExperiment              *RunningExperiment `json:"runningExperiment"`
	State                          ExperimentState    `json:"state"`
	Goal                           *string            `json:"goal"`
}

// ControlEntryData is persisted in the session log under customType "autoresearch-control".
// upstream: packages/coding-agent/src/autoresearch/types.ts:43-46
type ControlEntryData struct {
	Mode string  `json:"mode"` // "on" | "off" | "clear"
	Goal *string `json:"goal,omitempty"`
}

// ReconstructedControlState is replayed from session branch entries.
// upstream: packages/coding-agent/src/autoresearch/types.ts:48-52
type ReconstructedControlState struct {
	AutoresearchMode bool
	Goal             *string
	LastMode         string // "" | "on" | "off" | "clear"
}

// TruncationResult describes tail truncation of benchmark output.
// upstream: packages/tui/src/tools/streaming-output.ts
type TruncationResult struct {
	Content     string `json:"content"`
	Truncated   bool   `json:"truncated"`
	OutputLines int    `json:"outputLines"`
	OutputBytes int    `json:"outputBytes"`
	TotalLines  int    `json:"totalLines"`
	TotalBytes  int    `json:"totalBytes"`
}

// InitExperimentDetails is the structured details payload of init_experiment.
// upstream: packages/tui/src/tools/autoresearch.ts:155-162
type InitExperimentDetails struct {
	State            ExperimentState `json:"state"`
	CreatedSession   bool            `json:"createdSession"`
	BumpedSegment    bool            `json:"bumpedSegment"`
	AbandonedRuns    int             `json:"abandonedRuns"`
	HarnessCommitted bool            `json:"harnessCommitted"`
	BaselineCommit   *string         `json:"baselineCommit"`
}

// RunExperimentProgressDetails carries live elapsed time and tail output snapshots
// emitted via ctx.OnUpdate while run_experiment is executing.
// upstream: packages/tui/src/tools/autoresearch.ts:80-86
type RunExperimentProgressDetails struct {
	Phase          string            `json:"phase"`
	Elapsed        string            `json:"elapsed"`
	Truncation     *TruncationResult `json:"truncation,omitempty"`
	FullOutputPath string            `json:"fullOutputPath,omitempty"`
	RunDirectory   string            `json:"runDirectory,omitempty"`
}

// RunDetails is the structured details payload of run_experiment.
// upstream: packages/tui/src/tools/autoresearch.ts:89-109
type RunDetails struct {
	RunNumber         int               `json:"runNumber"`
	RunDirectory      string            `json:"runDirectory"`
	BenchmarkLogPath  string            `json:"benchmarkLogPath"`
	Command           string            `json:"command"`
	ExitCode          *int              `json:"exitCode"`
	DurationSeconds   float64           `json:"durationSeconds"`
	Passed            bool              `json:"passed"`
	Crashed           bool              `json:"crashed"`
	TimedOut          bool              `json:"timedOut"`
	TailOutput        string            `json:"tailOutput"`
	ParsedMetrics     NumericMetricMap  `json:"parsedMetrics"`
	ParsedPrimary     *float64          `json:"parsedPrimary"`
	ParsedASI         ASIData           `json:"parsedAsi"`
	MetricName        string            `json:"metricName"`
	MetricUnit        string            `json:"metricUnit"`
	PreRunDirtyPaths  []string          `json:"preRunDirtyPaths"`
	AbandonedPriorRun *int              `json:"abandonedPriorRun"`
	Truncation        *TruncationResult `json:"truncation,omitempty"`
	FullOutputPath    string            `json:"fullOutputPath,omitempty"`
}

// FlaggedRunEntry records a single flagged run in LogDetails.
// upstream: packages/tui/src/tools/autoresearch.ts:118
type FlaggedRunEntry struct {
	RunID  int    `json:"runId"`
	Reason string `json:"reason"`
}

// LogDetails is the structured details payload of log_experiment.
// upstream: packages/tui/src/tools/autoresearch.ts:112-119
type LogDetails struct {
	Experiment       ExperimentResult  `json:"experiment"`
	State            ExperimentState   `json:"state"`
	WallClockSeconds *float64          `json:"wallClockSeconds"`
	ScopeDeviations  []string          `json:"scopeDeviations"`
	Justification    *string           `json:"justification"`
	FlaggedRuns      []FlaggedRunEntry `json:"flaggedRuns"`
}

// UpdateNotesDetails is the structured details payload of update_notes.
// upstream: packages/tui/src/tools/autoresearch.ts
type UpdateNotesDetails struct {
	Notes string `json:"notes"`
}
