package autoresearch

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
)

//go:embed prompt.md
var promptTemplate string

//go:embed prompt-setup.md
var setupPromptTemplate string

//go:embed command-resume.md
var commandResumeTemplate string

//go:embed resume-message.md
var resumeMessageTemplate string

// legacyArtifacts are old repo-local files cleaned up on `/autoresearch clear`.
// upstream: packages/coding-agent/src/autoresearch/index.ts:464-473
var legacyArtifacts = []string{
	"autoresearch.md",
	"autoresearch.sh",
	"autoresearch.checks.sh",
	"autoresearch.program.md",
	"autoresearch.ideas.md",
	"autoresearch.jsonl",
	"autoresearch.config.json",
	".autoresearch",
}

type manager struct {
	mu    sync.Mutex
	store *RuntimeStore
}

var (
	defaultMgrMu sync.RWMutex
	defaultMgr   = &manager{store: NewRuntimeStore()}
)

// Register wires omp's autoresearch tools, `/autoresearch` command, and lifecycle
// event hooks onto e. All four experiment tools are registered with
// DefaultActive=false so default sessions advertise ZERO new tools and inject
// ZERO prompt lines unless `/autoresearch` is explicitly activated.
//
// upstream: packages/coding-agent/src/autoresearch/index.ts:32-462
func Register(e *sdk.Extension) {
	defaultMgrMu.RLock()
	m := defaultMgr
	defaultMgrMu.RUnlock()
	registerWithManager(e, m)
}

func registerWithManager(e *sdk.Extension, m *manager) {
	lifecycle.OnEnd("autoresearch", m.endSession)

	defaultOff := false

	initDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "init_experiment",
		Label:         "Init Experiment",
		Description:   "Initialize or reconfigure the autoresearch session. On first call (Phase 1 → Phase 2 transition), requires `./autoresearch.sh` to exist and pending harness changes are auto-committed on an autoresearch branch. Pass `new_segment: true` to start a fresh baseline within an existing session.",
		Parameters:    initExperimentSchema(),
		DefaultActive: &defaultOff,
		Execute:       m.handleInitExperiment,
	})
	runDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "run_experiment",
		Label:         "Run Experiment",
		Description:   "Run any benchmark command. Output is captured automatically; `METRIC name=value` and `ASI key=value` lines printed by the command are parsed.",
		Parameters:    runExperimentSchema(),
		DefaultActive: &defaultOff,
		Execute:       m.handleRunExperiment,
	})
	logDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "log_experiment",
		Label:         "Log Experiment",
		Description:   "Log the result of the latest run_experiment. Records the metric, optional ASI metadata, modified paths, and scope deviations. On `keep`, modified files are committed; on `discard`/`crash`/`checks_failed`, the worktree is reverted. Pass `flag_runs` to mark earlier runs as suspect; flagged runs are excluded from baseline and best-metric math.",
		Parameters:    logExperimentSchema(),
		DefaultActive: &defaultOff,
		Execute:       m.handleLogExperiment,
	})
	notesDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "update_notes",
		Label:         "Update Notes",
		Description:   "Persist the durable autoresearch playbook (goal, scope notes, hypotheses, ideas backlog) on the active session. Pass `body` to replace the entire notes blob, or `append_idea` to append a single bullet under an `## Ideas` section.",
		Parameters:    updateNotesSchema(),
		DefaultActive: &defaultOff,
		Execute:       m.handleUpdateNotes,
	})

	e.RegisterTool(intent.Tool(initDef))
	e.RegisterTool(intent.Tool(runDef))
	e.RegisterTool(intent.Tool(logDef))
	e.RegisterTool(intent.Tool(notesDef))
	intent.RegisterSync(e, initDef, runDef, logDef, notesDef)
	// upstream: packages/coding-agent/src/autoresearch/index.ts:125-225
	e.RegisterCommand("autoresearch", sdk.CommandOptions{
		Description:            "Toggle builtin autoresearch mode, or pass off / clear / status, or a goal message.",
		GetArgumentCompletions: autoresearchCompletions,
		Handler:                m.handleAutoresearchCommand,
	})

	// upstream: packages/coding-agent/src/autoresearch/index.ts:247-254
	e.OnSessionStart(m.onSessionStart)
	e.OnEvent(sdk.EventSessionTree, m.onSessionTree)
	e.OnSessionShutdown(m.onSessionShutdown)

	// upstream: packages/coding-agent/src/autoresearch/index.ts:256-416
	e.OnEvent(sdk.EventAgentEnd, m.onAgentEnd)
	e.OnEvent(sdk.EventBeforeAgentStart, m.onBeforeAgentStart)
}

func autoresearchCompletions(prefix string) ([]sdk.AutocompleteItem, error) {
	if strings.Contains(prefix, " ") {
		return nil, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(prefix))
	if normalized == "" {
		return nil, nil
	}
	items := []sdk.AutocompleteItem{
		{Label: "off", Value: "off", Description: "Leave autoresearch mode"},
		{Label: "clear", Value: "clear", Description: "Reset worktree to baseline and close the active session"},
		{Label: "status", Value: "status", Description: "Show current autoresearch session status and results table"},
	}
	var filtered []sdk.AutocompleteItem
	for _, item := range items {
		if strings.HasPrefix(item.Label, normalized) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func sessionKeyFor(ctx sdk.Context) string {
	if id, err := ctx.GetSessionID(); err == nil && strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	return "default"
}

func (m *manager) runtimeFor(ctx sdk.Context) *Runtime {
	return m.store.Ensure(sessionKeyFor(ctx))
}

func (m *manager) endSession(sessionID string) {
	if sessionID != "" {
		m.store.Clear(sessionID)
	}
}

func (m *manager) syncActiveTools(ctx sdk.Context, enable bool) {
	active, err := ctx.GetActiveTools()
	if err != nil {
		return
	}
	expSet := make(map[string]bool, len(ExperimentToolNames))
	for _, name := range ExperimentToolNames {
		expSet[name] = true
	}
	var next []string
	if enable {
		seen := make(map[string]bool, len(active)+len(ExperimentToolNames))
		for _, name := range active {
			if !seen[name] {
				seen[name] = true
				next = append(next, name)
			}
		}
		for _, name := range ExperimentToolNames {
			if !seen[name] {
				seen[name] = true
				next = append(next, name)
			}
		}
	} else {
		for _, name := range active {
			if !expSet[name] {
				next = append(next, name)
			}
		}
	}
	changed := len(next) != len(active)
	if !changed {
		for i := range next {
			if next[i] != active[i] {
				changed = true
				break
			}
		}
	}
	if changed {
		ctx.SetActiveTools(next)
	}
}

func (m *manager) setMode(ctx sdk.Context, enabled bool, goal *string, mode string) {
	rt := m.runtimeFor(ctx)
	m.mu.Lock()
	rt.AutoresearchMode = enabled
	rt.AutoResumeArmed = false
	rt.Goal = cloneStringPtr(goal)
	rt.LastAutoResumePendingRunNumber = nil
	m.mu.Unlock()

	entryData := map[string]any{"mode": mode}
	if goal != nil && strings.TrimSpace(*goal) != "" {
		entryData["goal"] = *goal
	}
	_ = ctx.AppendEntry("autoresearch-control", entryData)
}

func loadActiveSession(ctx context.Context, cwd string) (*SessionRow, *string, *Storage) {
	var currentBranch *string
	if b := GitCurrentBranch(ctx, cwd); b != "" {
		currentBranch = new(b)
	}
	storage, err := OpenAutoresearchStorageIfExists(ctx, cwd)
	if err != nil || storage == nil {
		return nil, currentBranch, nil
	}
	session := storage.GetActiveSessionForBranch(currentBranch)
	return session, currentBranch, storage
}

// rehydrate reconstructs autoresearch runtime state from the session branch and storage.
// upstream: packages/coding-agent/src/autoresearch/index.ts:49-104
func (m *manager) rehydrate(ctx sdk.Context) {
	rt := m.runtimeFor(ctx)
	rawEntries, _ := ctx.GetEntries()
	var branchIDs map[string]bool
	if branch, err := ctx.GetBranch(); err == nil && len(branch) > 0 {
		branchIDs = make(map[string]bool, len(branch))
		for _, b := range branch {
			if b.ID != "" {
				branchIDs[b.ID] = true
			}
		}
	}
	control := ReconstructControlState(rawEntries, branchIDs)
	everActivated := control.LastMode != ""
	var (
		session       *SessionRow
		currentBranch *string
		storage       *Storage
	)
	if everActivated {
		session, currentBranch, storage = loadActiveSession(context.Background(), ctx.Cwd())
	}

	onActiveBranch := session == nil || session.Branch == nil || stringPtrEqual(session.Branch, currentBranch)
	activeMode := control.AutoresearchMode && onActiveBranch

	var state ExperimentState
	var pending *PendingRunSummary
	goal := cloneStringPtr(control.Goal)
	if session != nil && onActiveBranch && storage != nil {
		state = BuildExperimentState(session, storage.ListLoggedRuns(session.ID))
		if goal == nil {
			goal = cloneStringPtr(session.Goal)
		}
		pending = pendingRunSummaryFromRow(storage.GetPendingRun(session.ID))
	} else {
		state = CreateExperimentState()
	}

	m.mu.Lock()
	rt.AutoresearchMode = activeMode
	rt.AutoResumeArmed = false
	rt.LastAutoResumePendingRunNumber = nil
	rt.Goal = goal
	rt.State = state
	rt.LastRunSummary = pending
	if pending != nil {
		rt.LastRunDuration = cloneFloatPtr(pending.DurationSeconds)
		rt.LastRunASI = cloneASI(pending.ParsedASI)
		rt.LastRunArtifactDir = new(pending.RunDirectory)
		rt.LastRunNumber = new(pending.RunNumber)
	} else {
		rt.LastRunDuration = nil
		rt.LastRunASI = nil
		rt.LastRunArtifactDir = nil
		rt.LastRunNumber = nil
	}
	rt.RunningExperiment = nil
	m.mu.Unlock()

	// Only touch active tools when autoresearch was ever activated or is currently on,
	// so untouched default sessions incur zero extra host round-trips.
	if everActivated || activeMode {
		m.syncActiveTools(ctx, activeMode)
	}
}

func (m *manager) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	m.rehydrate(ctx)
	return nil, nil
}

func (m *manager) onSessionTree(ctx sdk.Context, _ map[string]any) (any, error) {
	m.rehydrate(ctx)
	return nil, nil
}

func (m *manager) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	m.endSession(sessionKeyFor(ctx))
	return nil, nil
}

// handleAutoresearchCommand handles `/autoresearch [off|clear|status|<goal>]`.
// upstream: packages/coding-agent/src/autoresearch/index.ts:142-224
func (m *manager) handleAutoresearchCommand(ctx sdk.Context, args string) error {
	trimmed := strings.TrimSpace(args)
	rt := m.runtimeFor(ctx)

	m.mu.Lock()
	currentlyOn := rt.AutoresearchMode
	currentGoal := cloneStringPtr(rt.Goal)
	m.mu.Unlock()

	if trimmed == "status" {
		m.mu.Lock()
		summary := FormatStatusTable(rt)
		m.mu.Unlock()
		ctx.Notify(summary, "info")
		return nil
	}

	if (trimmed == "" && currentlyOn) || trimmed == "off" {
		m.setMode(ctx, false, currentGoal, "off")
		m.syncActiveTools(ctx, false)
		ctx.Notify("Autoresearch mode disabled", "info")
		return nil
	}

	if trimmed == "clear" || strings.HasPrefix(trimmed, "clear ") {
		flagPart := ""
		if len(trimmed) > len("clear ") {
			flagPart = strings.TrimSpace(trimmed[len("clear "):])
		}
		keepTree := strings.Contains(flagPart, "--keep-tree")
		resetTreeForce := strings.Contains(flagPart, "--reset-tree")
		m.handleClear(ctx, rt, keepTree, resetTreeForce)
		return nil
	}

	var goalArg *string
	if trimmed != "" {
		goalArg = new(trimmed)
	}
	branchGoal := goalArg
	if branchGoal == nil {
		branchGoal = currentGoal
	}
	bg := context.Background()
	cwd := ctx.Cwd()
	branchRes := EnsureAutoresearchBranch(bg, cwd, branchGoal)
	if !branchRes.OK {
		ctx.Notify(branchRes.Error, "error")
		return nil
	}
	if branchRes.Warning != "" {
		ctx.Notify(branchRes.Warning, "warning")
	}

	existingStorage, _ := OpenAutoresearchStorageIfExists(bg, cwd)
	var existingSession *SessionRow
	if existingStorage != nil {
		existingSession = existingStorage.GetActiveSessionForBranch(branchRes.BranchName)
	}

	var branchStatusLine string
	if branchRes.BranchName != nil && *branchRes.BranchName != "" {
		if branchRes.Created {
			branchStatusLine = fmt.Sprintf("Created and checked out dedicated git branch `%s` before resuming.", *branchRes.BranchName)
		} else {
			branchStatusLine = fmt.Sprintf("Using dedicated git branch `%s`.", *branchRes.BranchName)
		}
	} else {
		branchStatusLine = "Continuing on the current branch — no autoresearch branch was created."
	}

	if existingSession != nil && existingStorage != nil {
		updates := UpdateSessionParams{}
		needsUpdate := false
		if goalArg != nil {
			updates.HasGoal = true
			updates.Goal = goalArg
			needsUpdate = true
		}
		if branchRes.BranchName != nil {
			updates.HasBranch = true
			updates.Branch = branchRes.BranchName
			needsUpdate = true
		}
		if needsUpdate {
			_, _ = existingStorage.UpdateSession(existingSession.ID, updates)
		}
		refreshed := existingStorage.GetSessionByID(existingSession.ID)
		if refreshed == nil {
			refreshed = existingSession
		}
		state := BuildExperimentState(refreshed, existingStorage.ListLoggedRuns(refreshed.ID))
		nextGoal := cloneStringPtr(refreshed.Goal)
		if nextGoal == nil {
			nextGoal = cloneStringPtr(goalArg)
		}
		m.mu.Lock()
		rt.State = state
		m.mu.Unlock()
		m.setMode(ctx, true, nextGoal, "on")
		m.syncActiveTools(ctx, true)
		_ = ctx.SendUserMessage(renderCommandResume(branchStatusLine, trimmed), "")
		return nil
	}

	m.setMode(ctx, true, goalArg, "on")
	m.syncActiveTools(ctx, true)
	if goalArg != nil {
		_ = ctx.SendUserMessage(*goalArg, "")
	} else {
		ctx.Notify("Autoresearch enabled—describe what to optimize in your next message.", "info")
	}
	return nil
}

// handleClear resets worktree to baseline (when on autoresearch branch or --reset-tree),
// removes legacy artifacts, closes active session, and disables autoresearch mode.
// upstream: packages/coding-agent/src/autoresearch/index.ts:418-461
func (m *manager) handleClear(ctx sdk.Context, rt *Runtime, keepTree, resetTreeForce bool) {
	bg := context.Background()
	cwd := ctx.Cwd()
	storage, err := OpenAutoresearchStorage(bg, cwd)
	var session *SessionRow
	if err == nil && storage != nil {
		session = storage.GetActiveSession()
	}

	branchName := GitCurrentBranch(bg, cwd)
	onAutoresearchBranch := strings.HasPrefix(branchName, AutoresearchBranchPrefix)
	shouldResetTree := !keepTree && (onAutoresearchBranch || resetTreeForce)
	if shouldResetTree && session != nil && session.BaselineCommit != nil && *session.BaselineCommit != "" {
		if err := gitResetHardAndClean(bg, cwd, *session.BaselineCommit); err != nil {
			ctx.Notify(fmt.Sprintf("Failed to reset worktree to baseline: %v", err), "error")
		} else {
			ctx.Notify(fmt.Sprintf("Reset worktree to baseline %s.", shortSHA(*session.BaselineCommit)), "info")
		}
	} else if shouldResetTree {
		ctx.Notify("No baseline commit recorded — skipped worktree reset.", "warning")
	}

	removeLegacyArtifacts(cwd)

	if session != nil && storage != nil {
		_ = storage.CloseSession(session.ID)
	}

	m.mu.Lock()
	rt.State = CreateExperimentState()
	rt.Goal = nil
	rt.LastRunDuration = nil
	rt.LastRunASI = nil
	rt.LastRunArtifactDir = nil
	rt.LastRunNumber = nil
	rt.LastRunSummary = nil
	rt.RunningExperiment = nil
	m.mu.Unlock()

	m.setMode(ctx, false, nil, "clear")
	m.syncActiveTools(ctx, false)
	ctx.Notify("Autoresearch session cleared.", "info")
}

func removeLegacyArtifacts(workDir string) {
	for _, name := range legacyArtifacts {
		_ = os.RemoveAll(filepath.Join(workDir, name))
	}
}

// onAgentEnd implements the auto-resume loop when autoresearch is active and armed
// or an unlogged pending run remains.
// upstream: packages/coding-agent/src/autoresearch/index.ts:256-291
func (m *manager) onAgentEnd(ctx sdk.Context, _ map[string]any) (any, error) {
	rt := m.runtimeFor(ctx)
	m.mu.Lock()
	rt.RunningExperiment = nil
	modeOn := rt.AutoresearchMode
	m.mu.Unlock()

	if !modeOn {
		return nil, nil
	}
	if hasPending, err := ctx.HasPendingMessages(); err == nil && hasPending {
		m.mu.Lock()
		rt.AutoResumeArmed = false
		m.mu.Unlock()
		return nil, nil
	}

	session, _, storage := loadActiveSession(context.Background(), ctx.Cwd())
	var pendingRow *RunRow
	if session != nil && storage != nil {
		pendingRow = storage.GetPendingRun(session.ID)
	}
	pendingRun := pendingRunSummaryFromRow(pendingRow)

	m.mu.Lock()
	rt.LastRunSummary = pendingRun
	if pendingRun != nil {
		if pendingRun.DurationSeconds != nil {
			rt.LastRunDuration = cloneFloatPtr(pendingRun.DurationSeconds)
		}
		if pendingRun.ParsedASI != nil {
			rt.LastRunASI = cloneASI(pendingRun.ParsedASI)
		}
	}
	shouldResumePending := pendingRun != nil && (rt.LastAutoResumePendingRunNumber == nil || *rt.LastAutoResumePendingRunNumber != pendingRun.RunNumber)
	shouldResume := shouldResumePending || rt.AutoResumeArmed
	if !shouldResume {
		m.mu.Unlock()
		return nil, nil
	}
	rt.AutoResumeArmed = false
	if pendingRun != nil {
		rt.LastAutoResumePendingRunNumber = new(pendingRun.RunNumber)
	} else {
		rt.LastAutoResumePendingRunNumber = nil
	}
	m.mu.Unlock()

	content := renderResumeMessage(pendingRun != nil)
	_ = ctx.SendMessage("autoresearch-resume", content, false, sdk.SendMessageOptions{
		DeliverAs:   "nextTurn",
		TriggerTurn: new(true),
	})
	return nil, nil
}

// onBeforeAgentStart injects the Phase 1 setup prompt or Phase 2 experiment loop
// prompt when autoresearch mode is active. Returns (nil, nil) immediately when OFF
// so default sessions add ZERO prompt lines.
//
// upstream: packages/coding-agent/src/autoresearch/index.ts:293-416
func (m *manager) onBeforeAgentStart(ctx sdk.Context, data map[string]any) (any, error) {
	rt := m.runtimeFor(ctx)
	m.mu.Lock()
	modeOn := rt.AutoresearchMode
	m.mu.Unlock()
	if !modeOn {
		return nil, nil
	}

	bg := context.Background()
	cwd := ctx.Cwd()
	session, currentBranch, storage := loadActiveSession(bg, cwd)
	onActiveBranch := session == nil || session.Branch == nil || stringPtrEqual(session.Branch, currentBranch)
	if !onActiveBranch {
		m.mu.Lock()
		rt.AutoresearchMode = false
		rt.State = CreateExperimentState()
		rt.LastRunSummary = nil
		rt.RunningExperiment = nil
		m.mu.Unlock()
		m.syncActiveTools(ctx, false)
		return nil, nil
	}

	var pendingRow *RunRow
	if session != nil && storage != nil {
		state := BuildExperimentState(session, storage.ListLoggedRuns(session.ID))
		pendingRow = storage.GetPendingRun(session.ID)
		m.mu.Lock()
		rt.State = state
		m.mu.Unlock()
	}
	pendingRun := pendingRunSummaryFromRow(pendingRow)

	m.mu.Lock()
	rt.LastRunSummary = pendingRun
	if pendingRun != nil {
		if pendingRun.DurationSeconds != nil {
			rt.LastRunDuration = cloneFloatPtr(pendingRun.DurationSeconds)
		}
		if pendingRun.ParsedASI != nil {
			rt.LastRunASI = cloneASI(pendingRun.ParsedASI)
		}
	}
	state := rt.State
	goal := ""
	switch {
	case rt.Goal != nil && strings.TrimSpace(*rt.Goal) != "":
		goal = *rt.Goal
	case state.Goal != nil && strings.TrimSpace(*state.Goal) != "":
		goal = *state.Goal
	case state.Name != nil && strings.TrimSpace(*state.Name) != "":
		goal = *state.Name
	}
	m.mu.Unlock()

	// Read current base systemPrompt if provided (defensively handling nil / *sdk.SystemPrompt / string).
	basePrompt := readBaseSystemPrompt(data)

	var rendered string
	if session == nil {
		branchStr := GitCurrentBranch(bg, cwd)
		onAutoresearchBranch := strings.HasPrefix(branchStr, AutoresearchBranchPrefix)
		baselineWarning := ""
		if !onAutoresearchBranch {
			baselineWarning = "Heads up: you are not on a dedicated `autoresearch/*` branch. `log_experiment discard` will only revert run-modified files, not reset to baseline — so harness files written before `init_experiment` may not survive a discard. Clean the worktree and re-run `/autoresearch` if you want full revert safety."
		}
		rendered = renderSetupPrompt(basePrompt, goal, cwd, branchStr, baselineWarning)
	} else {
		rendered = renderActivePrompt(basePrompt, goal, cwd, state, pendingRun)
	}

	return map[string]any{
		"systemPrompt": rendered,
	}, nil
}

func readBaseSystemPrompt(data map[string]any) string {
	if data == nil {
		return ""
	}
	switch sp := data["systemPrompt"].(type) {
	case string:
		return sp
	case *sdk.SystemPrompt:
		if sp != nil {
			if s, err := sp.Get(); err == nil {
				return s
			}
		}
	case []string:
		return strings.Join(sp, "\n\n")
	case []any:
		parts := make([]string, 0, len(sp))
		for _, item := range sp {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

func pendingRunSummaryFromRow(row *RunRow) *PendingRunSummary {
	if row == nil || row.Status != nil || row.CompletedAt == nil {
		return nil
	}
	passed := row.ExitCode != nil && *row.ExitCode == 0 && !row.TimedOut
	var durSec *float64
	if row.DurationMs != nil {
		durSec = new(float64(*row.DurationMs) / 1000.0)
	}
	return &PendingRunSummary{
		Command:          row.Command,
		DurationSeconds:  durSec,
		ParsedASI:        cloneASI(row.ParsedASI),
		ParsedMetrics:    EnsureNumericMetricMap(row.ParsedMetrics),
		ParsedPrimary:    cloneFloatPtr(row.ParsedPrimary),
		Passed:           passed,
		PreRunDirtyPaths: cloneStringSlice(row.PreRunDirtyPaths),
		RunDirectory:     filepath.Dir(row.LogPath),
		RunNumber:        row.ID,
		ExitCode:         cloneIntPtr(row.ExitCode),
		TimedOut:         row.TimedOut,
	}
}

func summarizeExperimentASI(result ExperimentResult) string {
	if len(result.ASI) == 0 {
		return ""
	}
	var parts []string
	for _, key := range []string{"hypothesis", "rollback_reason", "next_action_hint"} {
		if s, ok := result.ASI[key].(string); ok {
			if t := strings.TrimSpace(s); t != "" {
				parts = append(parts, t)
			}
		}
	}
	summary := strings.Join(parts, " | ")
	prefix, _ := SliceUTF16Prefix(summary, 220)
	return prefix
}

// ── Template rendering (uses verbatim embedded upstream .md templates) ───────

func renderCommandResume(branchStatusLine, resumeContext string) string {
	return renderHandlebars(commandResumeTemplate, map[string]any{
		"branch_status_line": branchStatusLine,
		"has_resume_context": len(resumeContext) > 0,
		"resume_context":     resumeContext,
	})
}

func renderResumeMessage(hasPendingRun bool) string {
	return renderHandlebars(resumeMessageTemplate, map[string]any{
		"has_pending_run": hasPendingRun,
	})
}

func renderSetupPrompt(basePrompt, goal, workingDir, branch, baselineWarning string) string {
	return renderHandlebars(setupPromptTemplate, map[string]any{
		"base_system_prompt":   basePrompt,
		"has_goal":             strings.TrimSpace(goal) != "",
		"goal":                 goal,
		"working_dir":          workingDir,
		"has_branch":           branch != "",
		"branch":               branch,
		"has_baseline_warning": baselineWarning != "",
		"baseline_warning":     baselineWarning,
	})
}

func renderActivePrompt(basePrompt, goal, workingDir string, state ExperimentState, pendingRun *PendingRunSummary) string {
	segResults := CurrentResults(state.Results, state.CurrentSegment)
	baselineMetric := FindBaselineMetric(state.Results, state.CurrentSegment)
	baselineRunNum := FindBaselineRunNumber(state.Results, state.CurrentSegment)
	bestMetric := FindBestKeptMetric(state.Results, state.CurrentSegment, state.BestDirection)
	bestRes := BestKeptResult(state.Results, state.CurrentSegment, state.BestDirection)

	start := len(segResults) - 3
	if start < 0 {
		start = 0
	}
	recentResults := make([]map[string]any, 0, len(segResults)-start)
	for i := start; i < len(segResults); i++ {
		r := segResults[i]
		runNum := i + 1
		if r.RunNumber != nil {
			runNum = *r.RunNumber
		}
		asiSummary := summarizeExperimentASI(r)
		flaggedReason := ""
		if r.FlaggedReason != nil {
			flaggedReason = *r.FlaggedReason
		}
		recentResults = append(recentResults, map[string]any{
			"asi_summary":     asiSummary,
			"description":     r.Description,
			"has_asi_summary": asiSummary != "",
			"metric_display":  FormatNum(&r.Metric, state.MetricUnit),
			"run_number":      runNum,
			"status":          string(r.Status),
			"has_deviations":  len(r.ScopeDeviations) > 0,
			"deviations":      strings.Join(r.ScopeDeviations, ", "),
			"justified":       r.Justification != nil && strings.TrimSpace(*r.Justification) != "",
			"flagged":         r.Flagged,
			"flagged_reason":  flaggedReason,
		})
	}

	var unjustified []map[string]any
	for _, r := range segResults {
		if r.Status == StatusKeep && !r.Flagged && len(r.ScopeDeviations) > 0 && (r.Justification == nil || strings.TrimSpace(*r.Justification) == "") {
			runNum := 0
			if r.RunNumber != nil {
				runNum = *r.RunNumber
			}
			unjustified = append(unjustified, map[string]any{
				"run_number": runNum,
				"paths":      strings.Join(r.ScopeDeviations, ", "),
			})
		}
	}
	if len(unjustified) > 3 {
		unjustified = unjustified[len(unjustified)-3:]
	}

	branchStr := ""
	if state.Branch != nil {
		branchStr = *state.Branch
	}
	baselineCommit := ""
	if state.BaselineCommit != nil {
		baselineCommit = shortSHA(*state.BaselineCommit)
	}
	bestMetricDisplay := "-"
	if bestMetric != nil {
		bestMetricDisplay = FormatNum(bestMetric, state.MetricUnit)
	}
	var bestRunNum any
	if bestRes != nil {
		if bestRes.RunNumber != nil {
			bestRunNum = *bestRes.RunNumber
		}
	}
	var pendingRunNumber any
	pendingRunCommand := ""
	pendingRunPassed := false
	hasPendingMetric := false
	pendingMetricDisplay := ""
	if pendingRun != nil {
		pendingRunNumber = pendingRun.RunNumber
		pendingRunCommand = pendingRun.Command
		pendingRunPassed = pendingRun.Passed
		if pendingRun.ParsedPrimary != nil {
			hasPendingMetric = true
			pendingMetricDisplay = FormatNum(pendingRun.ParsedPrimary, state.MetricUnit)
		}
	}

	return renderHandlebars(promptTemplate, map[string]any{
		"base_system_prompt":         basePrompt,
		"has_goal":                   strings.TrimSpace(goal) != "",
		"goal":                       goal,
		"working_dir":                workingDir,
		"default_metric_name":        state.MetricName,
		"metric_name":                state.MetricName,
		"has_branch":                 branchStr != "",
		"branch":                     branchStr,
		"has_baseline_commit":        baselineCommit != "",
		"baseline_commit":            baselineCommit,
		"has_notes":                  strings.TrimSpace(state.Notes) != "",
		"notes":                      state.Notes,
		"current_segment":            state.CurrentSegment + 1,
		"current_segment_run_count":  len(segResults),
		"has_baseline_metric":        baselineMetric != nil,
		"baseline_metric_display":    FormatNum(baselineMetric, state.MetricUnit),
		"baseline_run_number":        baselineRunNum,
		"has_best_result":            bestRes != nil && bestMetric != nil,
		"best_metric_display":        bestMetricDisplay,
		"best_run_number":            bestRunNum,
		"has_recent_results":         len(recentResults) > 0,
		"recent_results":             recentResults,
		"has_unjustified_runs":       len(unjustified) > 0,
		"unjustified_runs":           unjustified,
		"has_pending_run":            pendingRun != nil,
		"pending_run_number":         pendingRunNumber,
		"pending_run_command":        pendingRunCommand,
		"pending_run_passed":         pendingRunPassed,
		"has_pending_run_metric":     hasPendingMetric,
		"pending_run_metric_display": pendingMetricDisplay,
	})
}
