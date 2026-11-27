package autoresearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const harnessCommitTitle = "autoresearch: harness setup"

// ── Schemas ──────────────────────────────────────────────────────────────────

// upstream: packages/coding-agent/src/autoresearch/tools/init-experiment.ts:21-33
func initExperimentSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "experiment name",
			},
			"goal": map[string]any{
				"type":        "string",
				"description": "session goal",
			},
			"primary_metric": map[string]any{
				"type":        "string",
				"description": "primary metric name",
			},
			"metric_unit": map[string]any{
				"type":        "string",
				"description": "metric unit (e.g. ms, µs, mb)",
			},
			"direction": map[string]any{
				"type":        "string",
				"enum":        []any{"lower", "higher"},
				"description": "better direction (default lower)",
			},
			"secondary_metrics": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "secondary metric names",
			},
			"scope_paths": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "expected-to-modify paths",
			},
			"off_limits": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "off-limits paths",
			},
			"constraints": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "free-form constraints",
			},
			"max_iterations": map[string]any{
				"type":        "number",
				"description": "soft iteration cap per segment",
			},
			"new_segment": map[string]any{
				"type":        "boolean",
				"description": "bump to a new segment in existing session",
			},
		},
		"required":             []any{"name", "primary_metric"},
		"additionalProperties": false,
	}
}

// upstream: packages/coding-agent/src/autoresearch/tools/run-experiment.ts:37-39
func runExperimentSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"timeout_seconds": map[string]any{
				"type":        "number",
				"description": "timeout in seconds (default 600)",
			},
		},
		"additionalProperties": false,
	}
}

// upstream: packages/coding-agent/src/autoresearch/tools/log-experiment.ts:26-40
func logExperimentSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"metric": map[string]any{
				"type":        "number",
				"description": "primary metric value",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []any{"keep", "discard", "crash", "checks_failed"},
				"description": "run outcome",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "short run description",
			},
			"metrics": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": map[string]any{"type": "number"},
				"description":          "secondary metrics",
			},
			"asi": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": true,
				"description":          "free-form structured metadata",
			},
			"commit": map[string]any{
				"type":        "string",
				"description": "override recorded commit hash",
			},
			"justification": map[string]any{
				"type":        "string",
				"description": "required when keeping a scope-deviating run",
			},
			"flag_runs": map[string]any{
				"type":        "array",
				"description": "flag earlier runs as suspect",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"run_id": map[string]any{
							"type":        "integer",
							"description": "run id to flag",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "why this run is suspect",
						},
					},
					"required":             []any{"run_id", "reason"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []any{"metric", "status", "description"},
		"additionalProperties": false,
	}
}

// upstream: packages/coding-agent/src/autoresearch/tools/update-notes.ts:12-15
func updateNotesSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"body": map[string]any{
				"type":        "string",
				"description": "replacement notes body",
			},
			"append_idea": map[string]any{
				"type":        "string",
				"description": "append as bullet under Ideas instead of replacing body",
			},
		},
		"additionalProperties": false,
	}
}

// ── init_experiment ──────────────────────────────────────────────────────────

// upstream: packages/coding-agent/src/autoresearch/tools/init-experiment.ts:46-223
func (m *manager) handleInitExperiment(ctx sdk.Context, args map[string]any) (any, error) {
	cwd := ctx.Cwd()
	bg := context.Background()
	storage, err := OpenAutoresearchStorage(bg, cwd)
	if err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to open autoresearch storage: %v", err), nil), nil
	}
	rt := m.runtimeFor(ctx)

	name, _ := args["name"].(string)
	primaryMetric, _ := args["primary_metric"].(string)
	metricUnit, _ := args["metric_unit"].(string)
	direction := DirectionLower
	if d, ok := args["direction"].(string); ok && d == string(DirectionHigher) {
		direction = DirectionHigher
	}
	scopePaths := normalizeAndDedupePaths(stringSliceArg(args["scope_paths"]))
	offLimits := normalizeAndDedupePaths(stringSliceArg(args["off_limits"]))
	constraints := DedupeStrings(stringSliceArg(args["constraints"]))
	secondaryMetrics := DedupeStrings(stringSliceArg(args["secondary_metrics"]))

	var goal *string
	if g, ok := args["goal"].(string); ok && strings.TrimSpace(g) != "" {
		goal = new(strings.TrimSpace(g))
	}

	var maxIterations *int
	if mi, ok := numberArg(args["max_iterations"]); ok && !math.IsNaN(mi) && !math.IsInf(mi, 0) && mi > 0 {
		maxIterations = new(int(math.Floor(mi)))
	}
	newSegment, _ := args["new_segment"].(bool)

	branchStr := GitCurrentBranch(bg, cwd)
	var branch *string
	if branchStr != "" {
		branch = new(branchStr)
	}
	onAutoresearchBranch := strings.HasPrefix(branchStr, AutoresearchBranchPrefix)
	inGitRepo := GitRepoRoot(bg, cwd) != ""

	existing := storage.GetActiveSessionForBranch(branch)
	isNewSegmentInit := existing != nil && newSegment
	requiresHarness := existing == nil || isNewSegmentInit

	if requiresHarness {
		if _, err := os.Stat(filepath.Join(cwd, HarnessFilename)); err != nil {
			return textToolResult(
				fmt.Sprintf("Error: ./%s does not exist. Phase 1 of autoresearch is harness setup — write `./%s` so it exits 0 and prints `METRIC <name>=<value>`, validate it via `bash %s`, then call init_experiment again.", HarnessFilename, HarnessFilename, HarnessFilename),
				nil,
			), nil
		}
	}

	harnessCommitted := false
	var commitWarning string
	if requiresHarness && onAutoresearchBranch && inGitRepo {
		if detectPendingChanges(bg, cwd) {
			if err := gitStageAll(bg, cwd); err != nil {
				commitWarning = fmt.Sprintf("Failed to auto-commit harness changes: %v. Recording baseline at current HEAD; discard may not preserve uncommitted harness files.", err)
			} else {
				msg := buildHarnessCommitMessage(goal, name)
				if _, err := gitCommit(bg, cwd, msg, nil); err != nil {
					commitWarning = fmt.Sprintf("Failed to auto-commit harness changes: %v. Recording baseline at current HEAD; discard may not preserve uncommitted harness files.", err)
				} else {
					harnessCommitted = true
				}
			}
		}
	}

	var baselineCommit *string
	if sha := GitHeadSHA(bg, cwd); sha != "" {
		baselineCommit = new(sha)
	}

	var session *SessionRow
	createdSession := false
	bumpedSegment := false
	abandonedRuns := 0

	if existing == nil {
		session, err = storage.OpenSession(OpenSessionParams{
			Name:             name,
			Goal:             goal,
			PrimaryMetric:    primaryMetric,
			MetricUnit:       metricUnit,
			Direction:        direction,
			PreferredCommand: new(DefaultHarnessCommand),
			Branch:           branch,
			BaselineCommit:   baselineCommit,
			MaxIterations:    maxIterations,
			ScopePaths:       scopePaths,
			OffLimits:        offLimits,
			Constraints:      constraints,
			SecondaryMetrics: secondaryMetrics,
		})
		if err != nil {
			return textToolResult(fmt.Sprintf("Error: failed to open session: %v", err), nil), nil
		}
		createdSession = true
	} else {
		abandonedRuns, err = storage.AbandonPendingRuns(existing.ID)
		if err != nil {
			return textToolResult(fmt.Sprintf("Error: failed to abandon pending runs: %v", err), nil), nil
		}
		updates := UpdateSessionParams{
			HasGoal:             true,
			Goal:                goal,
			HasMaxIterations:    true,
			MaxIterations:       maxIterations,
			HasScopePaths:       true,
			ScopePaths:          scopePaths,
			HasOffLimits:        true,
			OffLimits:           offLimits,
			HasConstraints:      true,
			Constraints:         constraints,
			HasSecondaryMetrics: true,
			SecondaryMetrics:    secondaryMetrics,
			HasPrimaryMetric:    true,
			PrimaryMetric:       primaryMetric,
			HasMetricUnit:       true,
			MetricUnit:          metricUnit,
			HasDirection:        true,
			Direction:           direction,
			HasBranch:           true,
			Branch:              branch,
		}
		if isNewSegmentInit {
			updates.HasBaselineCommit = true
			updates.BaselineCommit = baselineCommit
		}
		updated, err := storage.UpdateSession(existing.ID, updates)
		if err != nil {
			return textToolResult(fmt.Sprintf("Error: failed to update session: %v", err), nil), nil
		}
		if isNewSegmentInit {
			updated, err = storage.BumpSegment(existing.ID)
			if err != nil {
				return textToolResult(fmt.Sprintf("Error: failed to bump segment: %v", err), nil), nil
			}
			bumpedSegment = true
		}
		session = updated
	}

	loggedRuns := storage.ListLoggedRuns(session.ID)
	state := BuildExperimentState(session, loggedRuns)

	m.mu.Lock()
	rt.State = state
	rt.Goal = cloneStringPtr(session.Goal)
	rt.AutoresearchMode = true
	rt.AutoResumeArmed = true
	rt.LastAutoResumePendingRunNumber = nil
	rt.LastRunDuration = nil
	rt.LastRunASI = nil
	rt.LastRunArtifactDir = nil
	rt.LastRunNumber = nil
	rt.LastRunSummary = nil
	m.mu.Unlock()

	var lines []string
	if abandonedRuns > 0 {
		plural := "s"
		if abandonedRuns == 1 {
			plural = ""
		}
		lines = append(lines, fmt.Sprintf("Abandoned %d pending run%s before reconfiguring.", abandonedRuns, plural))
	}
	if harnessCommitted && session.BaselineCommit != nil {
		lines = append(lines, fmt.Sprintf("Committed harness setup at %s.", shortSHA(*session.BaselineCommit)))
	}
	if commitWarning != "" {
		lines = append(lines, commitWarning)
	}
	if createdSession {
		lines = append(lines, fmt.Sprintf("Started session #%d: %s", session.ID, session.Name))
	} else if bumpedSegment {
		lines = append(lines, fmt.Sprintf("Bumped segment to %d for session #%d: %s", session.CurrentSegment, session.ID, session.Name))
	} else {
		lines = append(lines, fmt.Sprintf("Updated session #%d (segment %d): %s", session.ID, session.CurrentSegment, session.Name))
	}
	unitDisplay := session.MetricUnit
	if unitDisplay == "" {
		unitDisplay = "unitless"
	}
	lines = append(lines, fmt.Sprintf("Metric: %s (%s, %s is better)", session.PrimaryMetric, unitDisplay, session.Direction))
	lines = append(lines, fmt.Sprintf("Benchmark entrypoint: %s", DefaultHarnessCommand))
	if len(session.ScopePaths) > 0 {
		lines = append(lines, fmt.Sprintf("Files in scope: %s", strings.Join(session.ScopePaths, ", ")))
	}
	if len(session.OffLimits) > 0 {
		lines = append(lines, fmt.Sprintf("Off limits: %s", strings.Join(session.OffLimits, ", ")))
	}
	if session.MaxIterations != nil {
		lines = append(lines, fmt.Sprintf("Max iterations per segment: %d", *session.MaxIterations))
	}
	if session.Branch != nil && *session.Branch != "" {
		lines = append(lines, fmt.Sprintf("Active branch: %s", *session.Branch))
	}
	if session.BaselineCommit != nil && *session.BaselineCommit != "" {
		lines = append(lines, fmt.Sprintf("Baseline commit: %s", shortSHA(*session.BaselineCommit)))
	}
	if createdSession {
		lines = append(lines, "Phase 2: iteration loop is active. Run the baseline experiment with `run_experiment` and log it.")
	} else if bumpedSegment {
		lines = append(lines, "Run a fresh baseline for the new segment.")
	}
	if requiresHarness && !onAutoresearchBranch {
		lines = append(lines, "Note: not on a dedicated `autoresearch/*` branch — `log_experiment discard` will only revert run-modified files, not reset to baseline.")
	}

	return textToolResult(strings.Join(lines, "\n"), InitExperimentDetails{
		State:            state,
		CreatedSession:   createdSession,
		BumpedSegment:    bumpedSegment,
		AbandonedRuns:    abandonedRuns,
		HarnessCommitted: harnessCommitted,
		BaselineCommit:   cloneStringPtr(session.BaselineCommit),
	}), nil
}

func detectPendingChanges(ctx context.Context, cwd string) bool {
	statusText, err := GitStatusPorcelain(ctx, cwd)
	if err != nil {
		return false
	}
	prefix := TryGitPrefix(ctx, cwd)
	return len(ParseWorkDirDirtyPaths(statusText, prefix)) > 0
}

func buildHarnessCommitMessage(goal *string, name string) string {
	lines := []string{harnessCommitTitle, "", "Benchmark entrypoint: " + DefaultHarnessCommand}
	if goal != nil && *goal != "" {
		lines = append(lines, "Goal: "+*goal)
	} else {
		lines = append(lines, "Session: "+name)
	}
	return strings.Join(lines, "\n")
}

// ── run_experiment ───────────────────────────────────────────────────────────

// upstream: packages/coding-agent/src/autoresearch/tools/run-experiment.ts:72-243
func (m *manager) handleRunExperiment(ctx sdk.Context, args map[string]any) (any, error) {
	cwd := ctx.Cwd()
	bg := context.Background()
	storage, err := OpenAutoresearchStorageIfExists(bg, cwd)
	if err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to open autoresearch storage: %v", err), nil), nil
	}
	var currentBranch *string
	if b := GitCurrentBranch(bg, cwd); b != "" {
		currentBranch = new(b)
	}
	var session *SessionRow
	if storage != nil {
		session = storage.GetActiveSessionForBranch(currentBranch)
	}
	if storage == nil || session == nil {
		return textToolResult("Error: no active autoresearch session for the current branch. Call init_experiment first.", nil), nil
	}

	rt := m.runtimeFor(ctx)

	var abandonedPriorRun *int
	if pending := storage.GetPendingRun(session.ID); pending != nil {
		if _, err := storage.AbandonPendingRuns(session.ID); err == nil {
			abandonedPriorRun = new(pending.ID)
		}
	}

	resolvedCommand := DefaultHarnessCommand
	preRunStatus := TryGitStatus(bg, cwd)
	workDirPrefix := TryGitPrefix(bg, cwd)
	preRunDirtyPaths := ParseWorkDirDirtyPaths(preRunStatus, workDirPrefix)

	startedAt := time.Now().UnixMilli()
	insertedRun, err := storage.InsertRun(InsertRunParams{
		SessionID:        session.ID,
		Segment:          session.CurrentSegment,
		Command:          resolvedCommand,
		LogPath:          "",
		PreRunDirtyPaths: preRunDirtyPaths,
		StartedAt:        startedAt,
	})
	if err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to record run: %v", err), nil), nil
	}

	runDir := filepath.Join(storage.ProjectDir(), "runs", fmt.Sprintf("%04d", insertedRun.ID))
	benchmarkLogPath := filepath.Join(runDir, "benchmark.log")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to create run directory: %v", err), nil), nil
	}
	if _, err := storage.UpdateRunLogPath(insertedRun.ID, benchmarkLogPath); err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to update run log path: %v", err), nil), nil
	}

	m.mu.Lock()
	rt.LastRunDuration = nil
	rt.LastRunASI = nil
	rt.LastRunArtifactDir = new(runDir)
	rt.LastRunNumber = new(insertedRun.ID)
	rt.LastRunSummary = nil
	rt.RunningExperiment = &RunningExperiment{
		StartedAt:    startedAt,
		Command:      resolvedCommand,
		RunDirectory: runDir,
		RunNumber:    insertedRun.ID,
	}
	m.mu.Unlock()

	timeoutSec := 600.0
	if ts, ok := numberArg(args["timeout_seconds"]); ok && !math.IsNaN(ts) && !math.IsInf(ts, 0) && ts >= 0 {
		timeoutSec = ts
	}
	timeout := time.Duration(timeoutSec * float64(time.Second))

	abortCtx, abortCancel := contextFromSDK(ctx)
	defer abortCancel()

	onProgress := func(snap progressSnapshot) {
		var truncPtr *TruncationResult
		if snap.truncation != nil {
			t := *snap.truncation
			truncPtr = &t
		}
		_ = ctx.OnUpdate(sdk.ToolResult{
			Content: snap.tailOutput,
			Details: RunExperimentProgressDetails{
				Phase:          "running",
				Elapsed:        snap.elapsed,
				Truncation:     truncPtr,
				FullOutputPath: snap.fullOutputPath,
				RunDirectory:   snap.runDirectory,
			},
		})
	}

	execRes, execErr := executeHarness(abortCtx, cwd, benchmarkLogPath, timeout, onProgress)

	m.mu.Lock()
	rt.RunningExperiment = nil
	m.mu.Unlock()

	if execErr != nil {
		if errors.Is(execErr, errExperimentAborted) {
			return nil, errors.New("aborted")
		}
		return textToolResult(fmt.Sprintf("Error: failed to execute harness: %v", execErr), nil), nil
	}
	completedAt := time.Now().UnixMilli()
	durationMs := completedAt - startedAt
	if durationMs < 0 {
		durationMs = 0
	}
	durationSeconds := float64(durationMs) / 1000.0

	var parsedMetrics NumericMetricMap
	if len(execRes.metrics) > 0 {
		parsedMetrics = make(NumericMetricMap, len(execRes.metrics))
		for k, v := range execRes.metrics {
			parsedMetrics[k] = v
		}
	}
	var parsedPrimary *float64
	if v, ok := execRes.metrics[session.PrimaryMetric]; ok {
		parsedPrimary = new(v)
	}
	parsedASI := execRes.asi

	if _, err := storage.MarkRunCompleted(MarkRunCompletedParams{
		RunID:         insertedRun.ID,
		CompletedAt:   completedAt,
		DurationMs:    durationMs,
		ExitCode:      execRes.exitCode,
		TimedOut:      execRes.timedOut,
		ParsedPrimary: parsedPrimary,
		ParsedMetrics: parsedMetrics,
		ParsedASI:     parsedASI,
	}); err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to persist run completion: %v", err), nil), nil
	}

	passed := execRes.exitCode != nil && *execRes.exitCode == 0 && !execRes.timedOut
	crashed := !passed
	var truncPtr *TruncationResult
	if execRes.llmTruncation.Truncated {
		t := execRes.llmTruncation
		truncPtr = &t
	}
	details := RunDetails{
		RunNumber:         insertedRun.ID,
		RunDirectory:      runDir,
		BenchmarkLogPath:  benchmarkLogPath,
		Command:           resolvedCommand,
		ExitCode:          cloneIntPtr(execRes.exitCode),
		DurationSeconds:   durationSeconds,
		Passed:            passed,
		Crashed:           crashed,
		TimedOut:          execRes.timedOut,
		TailOutput:        execRes.displayTruncation.Content,
		ParsedMetrics:     parsedMetrics,
		ParsedPrimary:     cloneFloatPtr(parsedPrimary),
		ParsedASI:         cloneASI(parsedASI),
		MetricName:        session.PrimaryMetric,
		MetricUnit:        session.MetricUnit,
		PreRunDirtyPaths:  preRunDirtyPaths,
		AbandonedPriorRun: cloneIntPtr(abandonedPriorRun),
		Truncation:        truncPtr,
		FullOutputPath:    benchmarkLogPath,
	}

	refreshed := storage.GetSessionByID(session.ID)
	var bestMetric *float64
	m.mu.Lock()
	rt.LastRunDuration = new(durationSeconds)
	rt.LastRunASI = cloneASI(parsedASI)
	rt.LastRunSummary = &PendingRunSummary{
		Command:          resolvedCommand,
		DurationSeconds:  new(durationSeconds),
		ParsedASI:        cloneASI(parsedASI),
		ParsedMetrics:    parsedMetrics,
		ParsedPrimary:    cloneFloatPtr(parsedPrimary),
		Passed:           passed,
		PreRunDirtyPaths: cloneStringSlice(preRunDirtyPaths),
		RunDirectory:     runDir,
		RunNumber:        insertedRun.ID,
		ExitCode:         cloneIntPtr(execRes.exitCode),
		TimedOut:         execRes.timedOut,
	}
	rt.AutoResumeArmed = true
	rt.LastAutoResumePendingRunNumber = nil
	if refreshed != nil {
		rt.State = BuildExperimentState(refreshed, storage.ListLoggedRuns(session.ID))
	}
	bestMetric = cloneFloatPtr(rt.State.BestMetric)
	m.mu.Unlock()

	var warningPrefix string
	if abandonedPriorRun != nil {
		warningPrefix = fmt.Sprintf("Note: abandoned prior pending run #%d before starting this run.\n\n", *abandonedPriorRun)
	}
	text := warningPrefix + buildRunText(details, execRes.llmTruncation.Content, bestMetric)
	return textToolResult(text, details), nil
}

var errExperimentAborted = errors.New("aborted")

const (
	harnessWaitDelay        = 2 * time.Second
	defaultProgressInterval = 1 * time.Second
)

// progressInterval controls the cadence of live OnUpdate snapshots (overridable in tests).
var progressInterval = defaultProgressInterval

type progressSnapshot struct {
	elapsed        string
	runDirectory   string
	fullOutputPath string
	tailOutput     string
	truncation     *TruncationResult
}

type processExecResult struct {
	exitCode          *int
	timedOut          bool
	llmTruncation     TruncationResult
	displayTruncation TruncationResult
	metrics           map[string]float64
	asi               ASIData
}

// contextFromSDK bridges sdk.Context.Done() into a standard context.Context so
// user abort / request cancellation kills the running benchmark process group.
func contextFromSDK(sdkCtx sdk.Context) (context.Context, context.CancelFunc) {
	runCtx, cancel := context.WithCancel(context.Background())
	doneCh := sdkCtx.Done()
	if doneCh != nil {
		go func() {
			select {
			case <-doneCh:
				cancel()
			case <-runCtx.Done():
			}
		}()
	}
	return runCtx, cancel
}

// executeHarness runs `bash autoresearch.sh` in cwd inside its own process group,
// streaming combined stdout/stderr to logPath, scanning METRIC/ASI lines, emitting
// periodic progress snapshots via onProgress, and killing the entire process group
// on timeout or user abort.
// upstream: packages/coding-agent/src/autoresearch/tools/run-experiment.ts:262-354
func executeHarness(
	abortCtx context.Context,
	cwd, logPath string,
	timeout time.Duration,
	onProgress func(progressSnapshot),
) (processExecResult, error) {
	if abortCtx == nil {
		abortCtx = context.Background()
	}
	if abortCtx.Err() != nil {
		return processExecResult{}, errExperimentAborted
	}
	runCtx := abortCtx
	var timeoutCancel context.CancelFunc
	if timeout > 0 {
		runCtx, timeoutCancel = context.WithTimeout(abortCtx, timeout)
		defer timeoutCancel()
	}

	shellBin := "bash"
	if runtime.GOOS == "windows" {
		if bashPath, err := exec.LookPath("bash"); err == nil {
			shellBin = bashPath
		}
	}
	cmd := exec.CommandContext(runCtx, shellBin, HarnessFilename)
	cmd.Dir = cwd
	cmd.Env = cleanHarnessEnv()
	cmd.WaitDelay = harnessWaitDelay
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		return processExecResult{}, err
	}
	defer logFile.Close()

	var (
		mu           sync.Mutex
		tailBuf      bytes.Buffer
		scanner      = NewExperimentOutputScanner()
		totalBytes   int
		newlineCount int
	)
	const maxTailBufBytes = DefaultMaxBytes * 2

	startedAt := time.Now()
	snapshot := func() progressSnapshot {
		mu.Lock()
		tail := tailBuf.String()
		tb := totalBytes
		tl := newlineCount + 1
		mu.Unlock()
		tr := TruncateTail(tail, DefaultMaxLines, DefaultMaxBytes)
		tr.TotalBytes = tb
		tr.TotalLines = tl
		if tb > tr.OutputBytes || tl > tr.OutputLines {
			tr.Truncated = true
		}
		var truncPtr *TruncationResult
		if tr.Truncated {
			truncPtr = &tr
		}
		return progressSnapshot{
			elapsed:        FormatElapsed(time.Since(startedAt).Milliseconds()),
			runDirectory:   filepath.Dir(logPath),
			fullOutputPath: logPath,
			tailOutput:     tr.Content,
			truncation:     truncPtr,
		}
	}

	writer := &syncWriter{
		fn: func(p []byte) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			_, _ = logFile.Write(p)
			scanner.Append(string(p))
			totalBytes += len(p)
			newlineCount += bytes.Count(p, []byte{'\n'})
			tailBuf.Write(p)
			if tailBuf.Len() > maxTailBufBytes {
				b := tailBuf.Bytes()
				tailBuf.Reset()
				tailBuf.Write(b[len(b)-maxTailBufBytes:])
			}
			return len(p), nil
		},
	}
	cmd.Stdout = writer
	cmd.Stderr = writer

	var stopTicker chan struct{}
	var tickerDone chan struct{}
	if onProgress != nil && progressInterval > 0 {
		stopTicker = make(chan struct{})
		tickerDone = make(chan struct{})
		interval := progressInterval
		go func() {
			defer close(tickerDone)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-stopTicker:
					return
				case <-runCtx.Done():
					return
				case <-ticker.C:
					onProgress(snapshot())
				}
			}
		}()
	}

	runErr := cmd.Run()
	if stopTicker != nil {
		close(stopTicker)
		<-tickerDone
	}
	_ = logFile.Sync()

	// Distinguish user abort (abortCtx cancelled) from timeout (runCtx deadline exceeded).
	if abortCtx.Err() != nil {
		return processExecResult{}, errExperimentAborted
	}

	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	var exitCode *int
	if runErr == nil {
		exitCode = new(0)
	} else {
		var exitErr *exec.ExitError
		switch {
		case errors.As(runErr, &exitErr):
			if !timedOut {
				exitCode = new(exitErr.ExitCode())
			}
		case errors.Is(runErr, exec.ErrWaitDelay):
			// Harness exited while a background child still held stdout/stderr open.
			// Reap any lingering process-group children and preserve the harness's real exit code.
			killProcessGroup(cmd)
			if !timedOut && cmd.ProcessState != nil {
				exitCode = new(cmd.ProcessState.ExitCode())
			}
		case !timedOut:
			return processExecResult{}, runErr
		}
	}

	mu.Lock()
	tail := tailBuf.String()
	tb := totalBytes
	tl := newlineCount + 1
	metrics, asi := scanner.Finish()
	mu.Unlock()

	llmTrunc := TruncateTail(tail, ExperimentMaxLines, ExperimentMaxBytes)
	llmTrunc.TotalBytes = tb
	llmTrunc.TotalLines = tl
	if tb > llmTrunc.OutputBytes || tl > llmTrunc.OutputLines {
		llmTrunc.Truncated = true
	}
	dispTrunc := TruncateTail(tail, DefaultMaxLines, DefaultMaxBytes)
	dispTrunc.TotalBytes = tb
	dispTrunc.TotalLines = tl
	if tb > dispTrunc.OutputBytes || tl > dispTrunc.OutputLines {
		dispTrunc.Truncated = true
	}

	return processExecResult{
		exitCode:          exitCode,
		timedOut:          timedOut,
		llmTruncation:     llmTrunc,
		displayTruncation: dispTrunc,
		metrics:           metrics,
		asi:               asi,
	}, nil
}

type syncWriter struct {
	fn func([]byte) (int, error)
}

func (w *syncWriter) Write(p []byte) (int, error) {
	return w.fn(p)
}

var _ io.Writer = (*syncWriter)(nil)

func cleanHarnessEnv() []string {
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_DIR=") ||
			strings.HasPrefix(kv, "GIT_WORK_TREE=") ||
			strings.HasPrefix(kv, "GIT_INDEX_FILE=") ||
			strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// buildRunText formats the model-facing output of run_experiment.
// upstream: packages/coding-agent/src/autoresearch/tools/run-experiment.ts:356-395
func buildRunText(details RunDetails, outputPreview string, bestMetric *float64) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("Run #%d directory: %s", details.RunNumber, details.RunDirectory))
	switch {
	case details.TimedOut:
		lines = append(lines, fmt.Sprintf("TIMEOUT after %.1fs", details.DurationSeconds))
	case details.ExitCode == nil || *details.ExitCode != 0:
		codeStr := "null"
		if details.ExitCode != nil {
			codeStr = fmt.Sprintf("%d", *details.ExitCode)
		}
		lines = append(lines, fmt.Sprintf("FAILED with exit code %s in %.1fs", codeStr, details.DurationSeconds))
	default:
		lines = append(lines, fmt.Sprintf("PASSED in %.1fs", details.DurationSeconds))
	}
	if bestMetric != nil {
		lines = append(lines, fmt.Sprintf("Current baseline %s: %s", details.MetricName, FormatNum(bestMetric, details.MetricUnit)))
	}
	if details.ParsedPrimary != nil {
		primaryStr := JSNumberString(*details.ParsedPrimary)
		lines = append(lines, fmt.Sprintf("Parsed %s: %s", details.MetricName, primaryStr))
		lines = append(lines, fmt.Sprintf("Next log_experiment metric: %s", primaryStr))
	}
	if len(details.ParsedMetrics) > 0 {
		names := make([]string, 0, len(details.ParsedMetrics))
		for name := range details.ParsedMetrics {
			if name == details.MetricName {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 0 {
			secParts := make([]string, 0, len(names))
			secMap := make(map[string]float64, len(names))
			for _, name := range names {
				val := details.ParsedMetrics[name]
				secParts = append(secParts, fmt.Sprintf("%s=%s", name, JSNumberString(val)))
				secMap[name] = val
			}
			lines = append(lines, "Parsed metrics: "+strings.Join(secParts, ", "))
			if raw, err := json.Marshal(secMap); err == nil {
				lines = append(lines, "Next log_experiment metrics: "+string(raw))
			}
		}
	}
	if len(details.ParsedASI) > 0 {
		keys := make([]string, 0, len(details.ParsedASI))
		for k := range details.ParsedASI {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines = append(lines, "Parsed ASI keys: "+strings.Join(keys, ", "))
	}
	lines = append(lines, "")
	lines = append(lines, outputPreview)
	if details.Truncation != nil && details.FullOutputPath != "" {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("Output truncated (%s limit). Full output: %s", FormatBytes(ExperimentMaxBytes), details.FullOutputPath))
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\n\r")
}

// ── log_experiment ───────────────────────────────────────────────────────────

// upstream: packages/coding-agent/src/autoresearch/tools/log-experiment.ts:53-270
func (m *manager) handleLogExperiment(ctx sdk.Context, args map[string]any) (any, error) {
	cwd := ctx.Cwd()
	bg := context.Background()
	storage, err := OpenAutoresearchStorageIfExists(bg, cwd)
	if err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to open autoresearch storage: %v", err), nil), nil
	}
	var currentBranch *string
	if b := GitCurrentBranch(bg, cwd); b != "" {
		currentBranch = new(b)
	}
	var session *SessionRow
	if storage != nil {
		session = storage.GetActiveSessionForBranch(currentBranch)
	}
	if storage == nil || session == nil {
		return textToolResult("Error: no active autoresearch session for the current branch. Call init_experiment first.", nil), nil
	}
	pendingRun := storage.GetPendingRun(session.ID)
	if pendingRun == nil {
		return textToolResult("Error: no pending run available. Run run_experiment first.", nil), nil
	}

	metric, ok := numberArg(args["metric"])
	if !ok || math.IsNaN(metric) || math.IsInf(metric, 0) {
		return textToolResult("Error: metric must be a finite number.", nil), nil
	}
	statusStr, _ := args["status"].(string)
	status := ExperimentStatus(statusStr)
	switch status {
	case StatusKeep, StatusDiscard, StatusCrash, StatusChecksFailed:
	default:
		return textToolResult(fmt.Sprintf("Error: invalid status %q", statusStr), nil), nil
	}
	description, _ := args["description"].(string)

	var overrideMetrics NumericMetricMap
	if rawMetrics, ok := args["metrics"].(map[string]any); ok {
		overrideMetrics = make(NumericMetricMap)
		for k, v := range rawMetrics {
			if n, ok := numberArg(v); ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
				overrideMetrics[k] = n
			}
		}
	}
	var rawASI map[string]any
	if a, ok := args["asi"].(map[string]any); ok {
		rawASI = a
	}

	rt := m.runtimeFor(ctx)

	flaggedRuns := make([]FlaggedRunEntry, 0)
	if rawFlags, ok := args["flag_runs"].([]any); ok {
		for _, item := range rawFlags {
			fm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			runIDFloat, ok := numberArg(fm["run_id"])
			if !ok {
				continue
			}
			runID := int(runIDFloat)
			reason, _ := fm["reason"].(string)
			target := storage.GetRunByID(runID)
			if target == nil || target.SessionID != session.ID {
				continue
			}
			if _, err := storage.FlagRun(runID, reason); err == nil {
				flaggedRuns = append(flaggedRuns, FlaggedRunEntry{RunID: runID, Reason: reason})
			}
		}
	}

	branchName := GetCurrentAutoresearchBranch(bg, cwd)
	onAutoresearchBranch := branchName != ""

	var allModified []string
	if onAutoresearchBranch {
		statusText := TryGitStatus(bg, cwd)
		workDirPrefix := TryGitPrefix(bg, cwd)
		allModified = ParseWorkDirDirtyPaths(statusText, workDirPrefix)
	} else {
		statusText := TryGitStatus(bg, cwd)
		workDirPrefix := TryGitPrefix(bg, cwd)
		tracked, untracked := ComputeRunModifiedPaths(pendingRun.PreRunDirtyPaths, statusText, workDirPrefix)
		allModified = append(allModified, tracked...)
		allModified = append(allModified, untracked...)
	}
	scopeDeviations := computeScopeDeviations(allModified, session)

	var justification *string
	if j, ok := args["justification"].(string); ok && strings.TrimSpace(j) != "" {
		justification = new(strings.TrimSpace(j))
	}
	var warnings []string

	var commitHash *string
	if c, ok := args["commit"].(string); ok && strings.TrimSpace(c) != "" {
		commitHash = new(strings.TrimSpace(c))
	} else if sha := GitHeadSHA(bg, cwd); sha != "" {
		commitHash = new(sha)
	}

	var gitNote string
	if status == StatusKeep {
		if onAutoresearchBranch && len(allModified) > 0 {
			note, err := commitKeptExperiment(bg, cwd, description, status, metric, overrideMetrics, allModified, session.PrimaryMetric)
			if err != nil {
				return textToolResult(fmt.Sprintf("Error: %v", err), nil), nil
			}
			gitNote = note
			if newSHA := GitHeadSHA(bg, cwd); newSHA != "" {
				commitHash = new(newSHA)
			}
		} else if !onAutoresearchBranch {
			warnings = append(warnings, "Auto-commit skipped: not on a dedicated autoresearch branch. Modified files remain in the worktree.")
		} else {
			gitNote = "nothing to commit"
		}
		if len(scopeDeviations) > 0 {
			if justification == nil {
				warnings = append(warnings, fmt.Sprintf("Kept with unjustified scope deviations: %s. Pass `justification` next time or `flag_runs` this entry on a future log_experiment if it was a mistake.", strings.Join(scopeDeviations, ", ")))
			} else {
				warnings = append(warnings, fmt.Sprintf("Kept with scope deviations (justified): %s", strings.Join(scopeDeviations, ", ")))
			}
		}
	} else {
		note, err := revertFailedExperiment(bg, cwd, pendingRun.PreRunDirtyPaths, onAutoresearchBranch)
		if err != nil {
			return textToolResult(fmt.Sprintf("Error: %v", err), nil), nil
		}
		gitNote = note
	}

	secondaryMetrics := mergeMetrics(pendingRun.ParsedMetrics, overrideMetrics, session.PrimaryMetric)
	asi := MergeASI(pendingRun.ParsedASI, SanitizeASI(rawASI))

	if pendingRun.ParsedPrimary != nil && metric != *pendingRun.ParsedPrimary {
		warnings = append(warnings, fmt.Sprintf("Logged metric %s differs from parsed primary %s. Both values stored.", JSNumberString(metric), JSNumberString(*pendingRun.ParsedPrimary)))
	}

	loggedAt := time.Now().UnixMilli()
	tentativeRun, err := storage.MarkRunLogged(MarkRunLoggedParams{
		RunID:           pendingRun.ID,
		Status:          status,
		Description:     description,
		Metric:          metric,
		Metrics:         secondaryMetrics,
		ASI:             asi,
		CommitHash:      commitHash,
		Confidence:      nil,
		ModifiedPaths:   allModified,
		ScopeDeviations: scopeDeviations,
		Justification:   justification,
		LoggedAt:        loggedAt,
	})
	if err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to log run: %v", err), nil), nil
	}

	refreshedSession := storage.GetSessionByID(session.ID)
	if refreshedSession == nil {
		refreshedSession = session
	}
	stateForConfidence := BuildExperimentState(refreshedSession, storage.ListLoggedRuns(session.ID))
	confidence := ComputeConfidence(stateForConfidence.Results, stateForConfidence.CurrentSegment, stateForConfidence.BestDirection)
	_, _ = storage.UpdateRunConfidence(tentativeRun.ID, confidence)

	finalState := BuildExperimentState(refreshedSession, storage.ListLoggedRuns(session.ID))

	m.mu.Lock()
	rt.State = finalState
	rt.RunningExperiment = nil
	rt.LastRunSummary = nil
	rt.LastRunDuration = nil
	rt.LastRunASI = nil
	rt.LastRunArtifactDir = nil
	rt.LastRunNumber = nil
	rt.AutoResumeArmed = true
	rt.LastAutoResumePendingRunNumber = nil
	goalCopy := cloneStringPtr(rt.Goal)
	m.mu.Unlock()

	shortCommit := ""
	if commitHash != nil {
		shortCommit = shortSHA(*commitHash)
	}
	experiment := ExperimentResult{
		RunNumber:       new(tentativeRun.ID),
		Commit:          shortCommit,
		Metric:          metric,
		Metrics:         secondaryMetrics,
		Status:          status,
		Description:     description,
		Timestamp:       loggedAt,
		Segment:         pendingRun.Segment,
		Confidence:      cloneFloatPtr(confidence),
		ASI:             cloneASI(asi),
		ModifiedPaths:   cloneStringSlice(allModified),
		ScopeDeviations: cloneStringSlice(scopeDeviations),
		Justification:   cloneStringPtr(justification),
		Flagged:         false,
		FlaggedReason:   nil,
	}

	segmentRunCount := len(CurrentResults(finalState.Results, finalState.CurrentSegment))
	if finalState.MaxExperiments != nil && segmentRunCount >= *finalState.MaxExperiments {
		m.mu.Lock()
		rt.AutoresearchMode = false
		rt.AutoResumeArmed = false
		m.mu.Unlock()
		entryData := map[string]any{"mode": "off"}
		if goalCopy != nil && *goalCopy != "" {
			entryData["goal"] = *goalCopy
		}
		_ = ctx.AppendEntry("autoresearch-control", entryData)
		m.syncActiveTools(ctx, false)
	}

	var wallClockSeconds *float64
	if pendingRun.DurationMs != nil {
		wallClockSeconds = new(float64(*pendingRun.DurationMs) / 1000.0)
	}
	text := buildLogText(finalState, experiment, segmentRunCount, wallClockSeconds, gitNote, warnings, flaggedRuns)

	return textToolResult(text, LogDetails{
		Experiment:       experiment,
		State:            finalState,
		WallClockSeconds: wallClockSeconds,
		ScopeDeviations:  scopeDeviations,
		Justification:    justification,
		FlaggedRuns:      flaggedRuns,
	}), nil
}

// commitKeptExperiment stages and commits modified files on an autoresearch branch.
// upstream: packages/coding-agent/src/autoresearch/tools/log-experiment.ts:279-312
func commitKeptExperiment(
	ctx context.Context,
	cwd, description string,
	status ExperimentStatus,
	metric float64,
	metrics NumericMetricMap,
	files []string,
	primaryMetric string,
) (string, error) {
	if len(files) == 0 {
		return "nothing to commit", nil
	}
	if err := gitStageFiles(ctx, cwd, files); err != nil {
		return "", fmt.Errorf("git add failed: %w", err)
	}
	if !gitHasCachedDiff(ctx, cwd, files) {
		return "nothing to commit", nil
	}
	payload := map[string]any{
		"status":      string(status),
		primaryMetric: metric,
	}
	for k, v := range metrics {
		payload[k] = v
	}
	rawPayload, _ := json.Marshal(payload)
	commitMessage := fmt.Sprintf("%s\n\nResult: %s", description, string(rawPayload))
	sha, err := gitCommit(ctx, cwd, commitMessage, files)
	if err != nil {
		return "", fmt.Errorf("git commit failed: %w", err)
	}
	return "committed " + shortSHA(sha), nil
}

// revertFailedExperiment reverts uncommitted iteration edits (reset --hard HEAD + clean -fd
// on an autoresearch branch; selective restore + rm off-branch).
// upstream: packages/coding-agent/src/autoresearch/tools/log-experiment.ts:314-353
func revertFailedExperiment(ctx context.Context, cwd string, preRunDirtyPaths []string, onAutoresearchBranch bool) (string, error) {
	if onAutoresearchBranch {
		if err := gitResetHardAndClean(ctx, cwd, "HEAD"); err != nil {
			return "", fmt.Errorf("git reset/clean failed: %w", err)
		}
		return "worktree reset to HEAD", nil
	}
	statusText := TryGitStatus(ctx, cwd)
	workDirPrefix := TryGitPrefix(ctx, cwd)
	tracked, untracked := ComputeRunModifiedPaths(preRunDirtyPaths, statusText, workDirPrefix)
	total := len(tracked) + len(untracked)
	if total == 0 {
		return "nothing to revert", nil
	}
	if len(tracked) > 0 {
		if err := gitRestoreTracked(ctx, cwd, tracked); err != nil {
			return "", fmt.Errorf("git restore failed: %w", err)
		}
	}
	for _, relPath := range untracked {
		_ = os.RemoveAll(filepath.Join(cwd, filepath.FromSlash(relPath)))
	}
	plural := "s"
	if total == 1 {
		plural = ""
	}
	return fmt.Sprintf("reverted %d file%s", total, plural), nil
}

// upstream: packages/coding-agent/src/autoresearch/tools/log-experiment.ts:365-377
func computeScopeDeviations(modifiedPaths []string, session *SessionRow) []string {
	deviations := make([]string, 0)
	for _, filePath := range modifiedPaths {
		offLimit := false
		for _, spec := range session.OffLimits {
			if PathMatchesSpec(filePath, spec) {
				offLimit = true
				break
			}
		}
		if offLimit {
			deviations = append(deviations, filePath)
			continue
		}
		if len(session.ScopePaths) > 0 {
			inScope := false
			for _, spec := range session.ScopePaths {
				if PathMatchesSpec(filePath, spec) {
					inScope = true
					break
				}
			}
			if !inScope {
				deviations = append(deviations, filePath)
			}
		}
	}
	return deviations
}

// upstream: packages/coding-agent/src/autoresearch/tools/log-experiment.ts:379-393
func mergeMetrics(parsed, overrides NumericMetricMap, primaryMetricName string) NumericMetricMap {
	merged := make(NumericMetricMap)
	for k, v := range parsed {
		if k == primaryMetricName {
			continue
		}
		merged[k] = v
	}
	for k, v := range EnsureNumericMetricMap(overrides) {
		merged[k] = v
	}
	return merged
}

// upstream: packages/coding-agent/src/autoresearch/tools/log-experiment.ts:403-477
func buildLogText(
	state ExperimentState,
	experiment ExperimentResult,
	segmentRunCount int,
	wallClockSeconds *float64,
	gitNote string,
	warnings []string,
	flaggedRuns []FlaggedRunEntry,
) string {
	displayRunNumber := len(state.Results)
	if experiment.RunNumber != nil {
		displayRunNumber = *experiment.RunNumber
	}
	lines := []string{fmt.Sprintf("Logged run #%d: %s - %s", displayRunNumber, experiment.Status, experiment.Description)}
	if wallClockSeconds != nil {
		lines = append(lines, fmt.Sprintf("Wall clock: %.1fs", *wallClockSeconds))
	}
	if state.BestMetric != nil {
		lines = append(lines, fmt.Sprintf("Baseline %s: %s", state.MetricName, FormatNum(state.BestMetric, state.MetricUnit)))
	}
	if segmentRunCount > 1 && state.BestMetric != nil && experiment.Metric != *state.BestMetric {
		delta := ((experiment.Metric - *state.BestMetric) / *state.BestMetric) * 100
		sign := ""
		if delta > 0 {
			sign = "+"
		}
		var deltaStr string
		switch {
		case math.IsInf(delta, 1):
			deltaStr = "Infinity"
		case math.IsInf(delta, -1):
			deltaStr = "-Infinity"
		case math.IsNaN(delta):
			deltaStr = "NaN"
		default:
			deltaStr = fmt.Sprintf("%.1f", delta)
		}
		lines = append(lines, fmt.Sprintf("This run: %s (%s%s%%)", FormatNum(&experiment.Metric, state.MetricUnit), sign, deltaStr))
	} else {
		lines = append(lines, fmt.Sprintf("This run: %s", FormatNum(&experiment.Metric, state.MetricUnit)))
	}
	if len(experiment.Metrics) > 0 {
		baselineSec := FindBaselineSecondary(state.Results, state.CurrentSegment, state.SecondaryMetrics)
		names := make([]string, 0, len(experiment.Metrics))
		for k := range experiment.Metrics {
			names = append(names, k)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, name := range names {
			val := experiment.Metrics[name]
			unit := ""
			for _, m := range state.SecondaryMetrics {
				if m.Name == name {
					unit = m.Unit
					break
				}
			}
			base, hasBase := baselineSec[name]
			if !hasBase || base == 0 || segmentRunCount == 1 {
				parts = append(parts, fmt.Sprintf("%s: %s", name, FormatNum(&val, unit)))
			} else {
				delta := ((val - base) / base) * 100
				sign := ""
				if delta > 0 {
					sign = "+"
				}
				parts = append(parts, fmt.Sprintf("%s: %s (%s%.1f%%)", name, FormatNum(&val, unit), sign, delta))
			}
		}
		lines = append(lines, "Secondary metrics: "+strings.Join(parts, "  "))
	}
	bestKept := FindBestKeptMetric(state.Results, state.CurrentSegment, state.BestDirection)
	if bestKept != nil && state.BestMetric != nil && *bestKept != *state.BestMetric {
		lines = append(lines, fmt.Sprintf("Best kept %s: %s", state.MetricName, FormatNum(bestKept, state.MetricUnit)))
	}
	if len(experiment.ASI) > 0 {
		keys := make([]string, 0, len(experiment.ASI))
		for k := range experiment.ASI {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s: %s", k, truncateASIValue(experiment.ASI[k])))
		}
		lines = append(lines, "ASI: "+strings.Join(parts, " | "))
	}
	if state.Confidence != nil {
		label := "within noise"
		if *state.Confidence >= 2 {
			label = "likely real"
		} else if *state.Confidence >= 1 {
			label = "marginal"
		}
		lines = append(lines, fmt.Sprintf("Confidence: %.1fx noise floor (%s)", *state.Confidence, label))
	}
	if gitNote != "" {
		lines = append(lines, "Git: "+gitNote)
	}
	if state.MaxExperiments != nil {
		lines = append(lines, fmt.Sprintf("Progress: %d/%d runs in current segment", segmentRunCount, *state.MaxExperiments))
		if segmentRunCount >= *state.MaxExperiments {
			lines = append(lines, fmt.Sprintf("Maximum experiments reached (%d). Autoresearch mode is now off.", *state.MaxExperiments))
		}
	}
	if len(flaggedRuns) > 0 {
		parts := make([]string, 0, len(flaggedRuns))
		for _, f := range flaggedRuns {
			parts = append(parts, fmt.Sprintf("#%d (%s)", f.RunID, f.Reason))
		}
		lines = append(lines, "Flagged: "+strings.Join(parts, ", "))
	}
	for _, w := range warnings {
		lines = append(lines, "Warning: "+w)
	}
	return strings.Join(lines, "\n")
}

func truncateASIValue(value any) string {
	var text string
	if s, ok := value.(string); ok {
		text = s
	} else {
		raw, _ := json.Marshal(value)
		text = string(raw)
	}
	if _, truncated := SliceUTF16Prefix(text, 120); truncated {
		prefix, _ := SliceUTF16Prefix(text, 117)
		return prefix + "..."
	}
	return text
}

// ── update_notes ─────────────────────────────────────────────────────────────

const ideasHeading = "## Ideas"

var markdownHeadingRe = regexp.MustCompile(`^#{1,6}\s`)

// upstream: packages/coding-agent/src/autoresearch/tools/update-notes.ts:28-77
func (m *manager) handleUpdateNotes(ctx sdk.Context, args map[string]any) (any, error) {
	cwd := ctx.Cwd()
	bg := context.Background()
	storage, err := OpenAutoresearchStorageIfExists(bg, cwd)
	if err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to open autoresearch storage: %v", err), nil), nil
	}
	var currentBranch *string
	if b := GitCurrentBranch(bg, cwd); b != "" {
		currentBranch = new(b)
	}
	var session *SessionRow
	if storage != nil {
		session = storage.GetActiveSessionForBranch(currentBranch)
	}
	if storage == nil || session == nil {
		return textToolResult("Error: no active autoresearch session for the current branch. Call init_experiment first.", nil), nil
	}

	rawIdea, _ := args["append_idea"].(string)
	trimmedIdea := strings.TrimSpace(rawIdea)
	bodyVal, hasBody := args["body"].(string)

	var nextNotes string
	if trimmedIdea != "" {
		nextNotes = AppendIdea(session.Notes, trimmedIdea)
	} else if hasBody {
		nextNotes = bodyVal
	} else {
		return textToolResult("Error: pass `body` to replace notes or `append_idea` to add an idea.", nil), nil
	}

	if _, err := storage.UpdateSession(session.ID, UpdateSessionParams{
		HasNotes: true,
		Notes:    nextNotes,
	}); err != nil {
		return textToolResult(fmt.Sprintf("Error: failed to update notes: %v", err), nil), nil
	}

	refreshed := storage.GetSessionByID(session.ID)
	loggedRuns := storage.ListLoggedRuns(session.ID)
	rt := m.runtimeFor(ctx)
	if refreshed != nil {
		m.mu.Lock()
		rt.State = BuildExperimentState(refreshed, loggedRuns)
		m.mu.Unlock()
	}

	var msg string
	if trimmedIdea != "" {
		msg = fmt.Sprintf("Appended idea (%d chars total).", len(nextNotes))
	} else {
		msg = fmt.Sprintf("Notes updated (%d chars).", len(nextNotes))
	}
	return textToolResult(msg, UpdateNotesDetails{Notes: nextNotes}), nil
}

// AppendIdea appends a bullet under `## Ideas` (creating the section if absent).
// upstream: packages/coding-agent/src/autoresearch/tools/update-notes.ts:83-103
func AppendIdea(currentNotes, idea string) string {
	trimmed := strings.TrimRight(currentNotes, " \t\n\r")
	if trimmed == "" {
		return fmt.Sprintf("%s\n- %s\n", ideasHeading, idea)
	}
	lines := strings.Split(trimmed, "\n")
	ideasIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == ideasHeading {
			ideasIdx = i
			break
		}
	}
	if ideasIdx >= 0 {
		insertAt := len(lines)
		for i := ideasIdx + 1; i < len(lines); i++ {
			if markdownHeadingRe.MatchString(lines[i]) {
				insertAt = i
				break
			}
		}
		updated := make([]string, 0, len(lines)+1)
		updated = append(updated, lines[:insertAt]...)
		updated = append(updated, "- "+idea)
		updated = append(updated, lines[insertAt:]...)
		return strings.Join(updated, "\n") + "\n"
	}
	return fmt.Sprintf("%s\n\n%s\n- %s\n", trimmed, ideasHeading, idea)
}

// ── Arg & result helpers ─────────────────────────────────────────────────────

func textToolResult(text string, details any) sdk.ToolResult {
	return sdk.ToolResult{
		Content: text,
		Details: details,
	}
}

func stringSliceArg(raw any) []string {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func normalizeAndDedupePaths(in []string) []string {
	norm := make([]string, len(in))
	for i, p := range in {
		norm[i] = NormalizePathSpec(p)
	}
	return DedupeStrings(norm)
}

func numberArg(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
