package pi_subagents

// Run summaries and the run list: the projection of status.json that the wait tool, the status action and the widget read.
// Ports the parts of async-status.ts and child-identity.ts for runs without workflow, nested or parallel-group state.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AsyncRunStepSummary is one child of a run summary.
type AsyncRunStepSummary struct {
	AsyncStep
	Index   int
	ChildID string
}

// AsyncRunSummary is a run as the listing sees it.
type AsyncRunSummary struct {
	ID              string
	AsyncDir        string
	ToolCallID      string
	SessionID       string
	State           string
	Error           string
	ActivityState   string
	LastActivityAt  *ms
	CurrentTool     string
	CurrentToolAt   *ms
	CurrentPath     string
	TurnCount       *int
	ToolCount       *int
	Mode            string
	Context         string
	Cwd             string
	SessionRoot     string
	StartedAt       int64
	LastUpdate      *ms
	EndedAt         *ms
	TimeoutMs       *ms
	DeadlineAt      *ms
	TimedOut        *bool
	Stopped         *bool
	CurrentStep     *int
	ChainStepCount  *int
	Steps           []AsyncRunStepSummary
	SessionDir      string
	OutputFile      string
	TotalTokens     *TokenUsage
	TotalCost       *CostSummary
	SessionFile     string
	ProcessTerminal *ProcessTerminal
}

// asyncStatusChildIdentity is child-identity.ts: the stable caller-facing id of a step.
func asyncStatusChildIdentity(step AsyncStep, index int) string {
	return asyncStatusChildIdentityCandidates(step, index)[0]
}

func asyncStatusChildIdentityCandidates(step AsyncStep, index int) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range []string{step.ChildID, step.WorkflowKey, step.RunID, fmt.Sprintf("step:%d", index)} {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func outputFileMtime(outputFile string) (int64, bool) {
	if outputFile == "" {
		return 0, false
	}
	st, err := os.Stat(outputFile)
	if err != nil {
		return 0, false
	}
	return st.ModTime().UnixMilli(), true
}

// deriveAsyncActivity: a running run's last activity falls back to its output file's mtime, then its current step.
func deriveAsyncActivity(asyncDir string, st *AsyncStatus) (string, *ms) {
	if st.State != "running" {
		return st.ActivityState, st.LastActivityAt
	}
	last := st.LastActivityAt
	if last == nil {
		outputPath := st.OutputFile
		if outputPath != "" && !filepath.IsAbs(outputPath) {
			outputPath = filepath.Join(asyncDir, outputPath)
		}
		if m, ok := outputFileMtime(outputPath); ok {
			last = msPtrOf(m)
		} else if st.CurrentStep != nil && *st.CurrentStep >= 0 && *st.CurrentStep < len(st.Steps) {
			step := st.Steps[*st.CurrentStep]
			switch {
			case step.LastActivityAt != nil:
				last = step.LastActivityAt
			case step.StartedAt != nil:
				last = step.StartedAt
			default:
				last = msPtrOf(int64(st.StartedAt))
			}
		} else {
			last = msPtrOf(int64(st.StartedAt))
		}
	}
	return st.ActivityState, last
}

// statusToSummary projects a status into a summary.
func statusToSummary(asyncDir string, st *AsyncStatus) AsyncRunSummary {
	activity, last := deriveAsyncActivity(asyncDir, st)
	runnerID := ""
	if st.ProcessTerminal != nil {
		runnerID = st.ProcessTerminal.RunnerProcessInstanceID
	}
	pt := readProcessTerminal(asyncDir, st.RunID, runnerID)
	if pt == nil {
		pt = sanitizeProcessTerminal(st.ProcessTerminal, st.RunID, runnerID, filepath.Join(asyncDir, "status.json"))
	}
	s := AsyncRunSummary{ID: firstNonEmpty(st.RunID, filepath.Base(asyncDir)), AsyncDir: asyncDir, ToolCallID: st.ToolCallID, SessionID: st.SessionID, State: st.State, Error: st.Error,
		ActivityState: activity, LastActivityAt: last, CurrentTool: st.CurrentTool, CurrentToolAt: st.CurrentToolStartedAt, CurrentPath: st.CurrentPath, TurnCount: st.TurnCount, ToolCount: st.ToolCount,
		Mode: st.Mode, Context: st.Context, Cwd: st.Cwd, SessionRoot: st.SessionRoot, StartedAt: int64(st.StartedAt), LastUpdate: st.LastUpdate, EndedAt: st.EndedAt, TimeoutMs: st.TimeoutMs, DeadlineAt: st.DeadlineAt,
		TimedOut: st.TimedOut, Stopped: st.Stopped, CurrentStep: st.CurrentStep, ChainStepCount: st.ChainStepCount, SessionDir: st.SessionDir, OutputFile: st.OutputFile,
		TotalTokens: st.TotalTokens, TotalCost: st.TotalCost, SessionFile: st.SessionFile, ProcessTerminal: pt}
	for i, step := range st.Steps {
		s.Steps = append(s.Steps, AsyncRunStepSummary{AsyncStep: step, Index: i, ChildID: asyncStatusChildIdentity(step, i)})
	}
	return s
}

func sortRuns(runs []AsyncRunSummary) {
	rank := func(state string) int {
		switch state {
		case "running":
			return 0
		case "queued":
			return 1
		case "failed", "partial", "stopped", "paused":
			return 2
		case "complete":
			return 3
		}
		return 4
	}
	when := func(r AsyncRunSummary) int64 {
		switch {
		case r.LastUpdate != nil:
			return int64(*r.LastUpdate)
		case r.EndedAt != nil:
			return int64(*r.EndedAt)
		}
		return r.StartedAt
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if ri, rj := rank(runs[i].State), rank(runs[j].State); ri != rj {
			return ri < rj
		}
		return when(runs[i]) > when(runs[j])
	})
}

// TargetedRun is the resolution of an id against the async root.
type TargetedRun struct {
	Kind string // exact, prefix, reject
	ID   string
}

// resolveTargetedAsyncRun resolves an exact run without following a symlinked run directory or accepting a path whose
// canonical location left the root.
func resolveTargetedAsyncRun(asyncDirRoot, id, sessionID string) (TargetedRun, error) {
	if id == "" || id == "." || id == ".." || id == activeRunIndexDir || id == terminalRunIndexDir || filepath.Base(id) != id {
		return TargetedRun{Kind: "reject"}, nil
	}
	dir := filepath.Join(asyncDirRoot, id)
	st, err := os.Lstat(dir)
	if err != nil {
		if isNotExist(err) {
			if canScanAsyncRunPrefix(id) {
				return TargetedRun{Kind: "prefix"}, nil
			}
			return TargetedRun{Kind: "reject"}, nil
		}
		return TargetedRun{}, fmt.Errorf("Failed to inspect async run path '%s': %s", dir, err.Error())
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return TargetedRun{Kind: "reject"}, nil
	}
	canonRoot, err1 := filepath.EvalSymlinks(asyncDirRoot)
	canonDir, err2 := filepath.EvalSymlinks(dir)
	if err1 != nil || err2 != nil {
		if isNotExist(err1) || isNotExist(err2) {
			return TargetedRun{Kind: "reject"}, nil
		}
		return TargetedRun{}, fmt.Errorf("Failed to resolve async run path '%s'", dir)
	}
	if canonDir != canonRoot && !strings.HasPrefix(canonDir, canonRoot+string(filepath.Separator)) {
		return TargetedRun{Kind: "reject"}, nil
	}
	if sessionID != "" {
		status, _ := readStatus(dir)
		if status == nil || status.SessionID != sessionID {
			if canScanAsyncRunPrefix(id) {
				return TargetedRun{Kind: "prefix"}, nil
			}
			return TargetedRun{Kind: "reject"}, nil
		}
	}
	return TargetedRun{Kind: "exact", ID: id}, nil
}

// AsyncRunListOptions selects runs for listAsyncRuns.
type AsyncRunListOptions struct {
	States      []string
	SessionID   string
	Limit       int // 0: no limit
	EntryLimit  int // 0: no limit
	ResultsDir  string
	Kill        killFunc
	Now         func() int64
	NoReconcile bool
	RunID       string
	ExactRunID  bool
	RepairScan  bool
	// Observe is called with each raw status read (nil for a missing or unreadable one): the drain's read-only observation.
	Observe func(*AsyncStatus)
}

func isAsyncRunDir(root, entry string) bool {
	st, err := os.Stat(filepath.Join(root, entry))
	return err == nil && st.IsDir()
}

// isolateCorruptActiveRun logs nothing (an extension must not write to stdout); an observed process end releases the marker.
func isolateCorruptActiveRun(asyncDir, runID string, now func() int64) {
	pt := readProcessTerminal(asyncDir, runID, "")
	at := int64(0)
	if now != nil {
		at = now()
	}
	age := activeRunMarkerAgeMs(asyncDir, at)
	if (pt != nil && pt.State == "observed") || (age >= 0 && age > defaultStaleTerminalActiveMarker) {
		_ = releaseActiveRunIndex(asyncDir)
	}
}

func isStatusIsolationError(asyncDir string, err error) bool {
	msg := err.Error()
	statusPath := filepath.Join(asyncDir, "status.json")
	for _, p := range []string{"Failed to inspect async status file '", "Failed to read async status file '", "Failed to parse async status file '", "Failed to validate async status file '", "Invalid async status file '"} {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return strings.HasPrefix(msg, "Invalid async status '"+statusPath+"'")
}

// listAsyncRuns lists the runs of a root: the active ones from the active index, the finished ones from the terminal index.
func listAsyncRuns(asyncDirRoot string, o AsyncRunListOptions) ([]AsyncRunSummary, error) {
	wantsActive, wantsTerminal := o.States == nil, o.States == nil
	for _, s := range o.States {
		if isActiveAsyncState(s) {
			wantsActive = true
		} else {
			wantsTerminal = true
		}
	}
	active := map[string]bool{}
	var entries []string
	switch {
	case o.RunID != "":
		res, err := resolveTargetedAsyncRun(asyncDirRoot, o.RunID, o.SessionID)
		if err != nil {
			return nil, err
		}
		if res.Kind == "exact" {
			entries = []string{res.ID}
		} else if res.Kind == "prefix" && !o.ExactRunID {
			names, rerr := os.ReadDir(asyncDirRoot)
			if rerr != nil {
				if isNotExist(rerr) {
					return nil, nil
				}
				return nil, fmt.Errorf("Failed to list async runs in '%s': %s", asyncDirRoot, rerr.Error())
			}
			for _, n := range names {
				name := n.Name()
				if name == o.RunID || strings.HasPrefix(name, o.RunID) {
					if r, _ := resolveTargetedAsyncRun(asyncDirRoot, name, o.SessionID); r.Kind == "exact" {
						entries = append(entries, name)
					}
				}
			}
		}
	case o.RepairScan:
		names, err := os.ReadDir(asyncDirRoot)
		if err != nil {
			if isNotExist(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("Failed to list async runs in '%s': %s", asyncDirRoot, err.Error())
		}
		for _, n := range names {
			if n.Name() != activeRunIndexDir && n.Name() != terminalRunIndexDir && isAsyncRunDir(asyncDirRoot, n.Name()) {
				entries = append(entries, n.Name())
			}
		}
	default:
		indexed := map[string]bool{}
		var order []string
		add := func(e string) {
			if !indexed[e] {
				indexed[e] = true
				order = append(order, e)
			}
		}
		if wantsActive {
			names, _ := readActiveRunIndex(asyncDirRoot)
			for _, e := range names {
				if r, _ := resolveTargetedAsyncRun(asyncDirRoot, e, ""); r.Kind == "exact" {
					add(e)
					active[e] = true
				} else {
					if o.Observe != nil {
						o.Observe(nil)
					}
					_ = updateActiveRunIndex(filepath.Join(asyncDirRoot, e), "failed", "", indexOptions{})
				}
			}
		}
		if wantsTerminal {
			limit := -1
			if o.EntryLimit > 0 {
				limit = o.EntryLimit
			}
			for _, e := range readRecentTerminalRunIndex(asyncDirRoot, o.SessionID, limit) {
				add(e)
			}
		}
		entries = order
	}
	allowed := map[string]bool{}
	for _, s := range o.States {
		allowed[s] = true
	}
	var runs []AsyncRunSummary
	for _, entry := range entries {
		asyncDir := filepath.Join(asyncDirRoot, entry)
		var st *AsyncStatus
		var err error
		if o.SessionID != "" {
			stored, rerr := readStatus(asyncDir)
			if rerr == nil && stored != nil && stored.SessionID != o.SessionID {
				if o.Observe != nil {
					o.Observe(stored)
				}
				continue
			}
		}
		if o.NoReconcile {
			st, err = readStatus(asyncDir)
			if err == nil && o.Observe != nil {
				o.Observe(st)
			}
		} else {
			var rec ReconcileResult
			if rec, err = reconcileAsyncRun(asyncDir, ReconcileOptions{ResultsDir: o.ResultsDir, Kill: o.Kill, Now: o.Now}); err == nil {
				st = rec.Status
				if st == nil {
					st, err = readStatus(asyncDir)
				}
			}
			if o.Observe != nil {
				if raw, _ := readStatusQuiet(asyncDir); raw != nil {
					o.Observe(raw)
				} else {
					o.Observe(nil)
				}
			}
		}
		if err != nil {
			if o.Observe != nil {
				o.Observe(nil)
			}
			if !active[entry] || !isStatusIsolationError(asyncDir, err) {
				return nil, err
			}
			isolateCorruptActiveRun(asyncDir, entry, o.Now)
			continue
		}
		if st == nil {
			if o.Observe != nil {
				o.Observe(nil)
			}
			if active[entry] {
				_ = updateActiveRunIndex(asyncDir, "failed", "", indexOptions{})
			}
			continue
		}
		if active[entry] && !isActiveAsyncState(st.State) {
			runnerID := ""
			if st.ProcessTerminal != nil {
				runnerID = st.ProcessTerminal.RunnerProcessInstanceID
			}
			pt := readProcessTerminal(asyncDir, st.RunID, runnerID)
			at := int64(0)
			if o.Now != nil {
				at = o.Now()
			}
			if (pt != nil && pt.State == "observed") || activeRunMarkerAgeMs(asyncDir, at) > defaultStaleTerminalActiveMarker {
				_ = updateActiveRunIndex(asyncDir, st.State, st.ToolCallID, indexOptions{terminalIndexBeforeRelease: true})
			}
		}
		if st.DisplayDismissedAt != nil {
			continue
		}
		if len(allowed) > 0 && !allowed[st.State] {
			continue
		}
		if o.SessionID != "" && st.SessionID != o.SessionID {
			continue
		}
		runs = append(runs, statusToSummary(asyncDir, st))
	}
	sortRuns(runs)
	if o.Limit > 0 && len(runs) > o.Limit {
		runs = runs[:o.Limit]
	}
	return runs, nil
}

// ── text ──

func formatActivityFacts(r *AsyncRunSummary) string {
	return activityFactsOf(r.ActivityState, r.LastActivityAt, r.CurrentTool, r.CurrentToolAt, r.CurrentPath, r.TurnCount, r.ToolCount)
}

func activityFactsOf(activity string, last *ms, tool string, toolAt *ms, path string, turns, tools *int) string {
	var facts []string
	switch {
	case tool != "" && toolAt != nil:
		facts = append(facts, fmt.Sprintf("tool %s %s", tool, bgFormatDuration(max(0, nowMs()-int64(*toolAt)))))
	case tool != "":
		facts = append(facts, "tool "+tool)
	}
	if path != "" {
		facts = append(facts, bgShortenPath(path))
	}
	if turns != nil {
		facts = append(facts, fmt.Sprintf("%d turns", *turns))
	}
	if tools != nil {
		facts = append(facts, fmt.Sprintf("%d tools", *tools))
	}
	label := bgActivityLabel(last, activity, nowMs())
	if label == "" && len(facts) == 0 {
		return ""
	}
	var parts []string
	if label != "" {
		parts = append(parts, label)
	}
	return strings.Join(append(parts, facts...), " | ")
}

func formatStepLine(s AsyncRunStepSummary) string {
	display := jsTrim(s.SessionName)
	if display == "" {
		display = s.Agent
	}
	parts := []string{fmt.Sprintf("%d. %s", s.Index+1, display), s.Status}
	if a := activityFactsOf(s.ActivityState, s.LastActivityAt, s.CurrentTool, s.CurrentToolStart, s.CurrentPath, s.TurnCount, s.ToolCount); a != "" {
		parts = append(parts, a)
	}
	if mt := bgFormatModelThinking(s.Model, s.Thinking); mt != "" {
		parts = append(parts, mt)
	}
	if s.DurationMs != nil {
		parts = append(parts, bgFormatDuration(int64(*s.DurationMs)))
	}
	if s.Tokens != nil {
		parts = append(parts, bgFormatTokens(s.Tokens.Total)+" tok")
	}
	return strings.Join(parts, " | ")
}

// formatAsyncRunOutputPath is the run's output file, made absolute against the run directory.
func formatAsyncRunOutputPath(asyncDir, outputFile string) string {
	if outputFile == "" {
		return ""
	}
	if filepath.IsAbs(outputFile) {
		return outputFile
	}
	return filepath.Join(asyncDir, outputFile)
}

// formatAsyncRunProgressLabel is "step N/M" (or "steps M" before the first step starts).
func formatAsyncRunProgressLabel(mode string, current *int, stepCount int) string {
	n := stepCount
	if n == 0 {
		n = 1
	}
	if current != nil {
		return fmt.Sprintf("step %d/%d", *current+1, n)
	}
	return fmt.Sprintf("steps %d", n)
}

func formatRunHeader(r AsyncRunSummary) string {
	cwd := bgShortenPath(r.AsyncDir)
	if r.Cwd != "" {
		cwd = bgShortenPath(r.Cwd)
	}
	activity := formatActivityFacts(&r)
	act := ""
	if activity != "" {
		act = " | " + activity
	}
	ctx := ""
	if r.Context != "" {
		ctx = " " + r.Context
	}
	return fmt.Sprintf("%s | %s%s | %s%s | %s | %s", r.ID, r.State, act, r.Mode, ctx, formatAsyncRunProgressLabel(r.Mode, r.CurrentStep, len(r.Steps)), cwd)
}

// formatAsyncRunList renders the runs (heading defaults to "Active async runs").
func formatAsyncRunList(runs []AsyncRunSummary, heading string) string {
	if heading == "" {
		heading = "Active async runs"
	}
	if len(runs) == 0 {
		return "No " + strings.ToLower(heading) + "."
	}
	lines := []string{fmt.Sprintf("%s: %d", heading, len(runs)), ""}
	for _, r := range runs {
		lines = append(lines, "- "+formatRunHeader(r))
		for _, s := range r.Steps {
			lines = append(lines, "  "+formatStepLine(s))
			lines = append(lines, formatTimeoutRecoveryLines(s.TimeoutRecovery, "    ")...)
		}
		if r.Error != "" {
			lines = append(lines, "  Error: "+r.Error)
		}
		if p := formatAsyncRunOutputPath(r.AsyncDir, r.OutputFile); p != "" {
			lines = append(lines, "  output: "+bgShortenPath(p))
		}
		if r.SessionFile != "" {
			lines = append(lines, "  session: "+bgShortenPath(r.SessionFile))
		}
		lines = append(lines, "")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n")
}
