package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The `status` action (src/runs/background/run-status.ts, async-status.ts,
// fleet-view.ts): the port renders the original's texts for the run shapes it
// runs (single async runs), without the workflow, nested-run and external-job
// sections those shapes cannot carry here.

const (
	defaultTranscriptLines = 80
	maxTranscriptLines     = 500
)

func init() {
	registerAction("status", []string{"id", "runId", "dir", "index", "view", "lines"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(inspectStatus(ctx, params))
	})
}

// isoTime is JavaScript's new Date(ms).toISOString().
func isoTime(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

// runView is the summary both live and disk-backed runs render from.
type runView struct {
	ID            string
	State         string
	Mode          string
	Cwd           string
	Dir           string
	StartedAt     int64
	LastUpdate    int64
	EndedAt       int64
	OutputFile    string
	CurrentStep   *int
	StepCount     int
	Steps         []*asyncStep
	Error         string
	TimedOut      bool
	Stopped       bool
	SessionID     string
	ResultPath    string
	fromDisk      bool
}

func viewOfRun(r *asyncRun) *runView {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := &runView{
		ID: r.ID, State: r.State, Mode: r.Mode, Cwd: r.Cwd, Dir: r.Dir,
		StartedAt: r.StartedAt, LastUpdate: r.LastUpdate, EndedAt: r.EndedAt,
		OutputFile: r.OutputFile, StepCount: len(r.Steps), Error: r.Error,
		TimedOut: r.TimedOut, Stopped: r.Stopped, SessionID: r.SessionID,
	}
	for _, s := range r.Steps {
		cp := *s
		v.Steps = append(v.Steps, &cp)
	}
	return v
}

func viewOfStatus(st *asyncStatusFile, dir string) *runView {
	v := &runView{
		ID: st.RunID, State: st.State, Mode: st.Mode, Cwd: st.Cwd, Dir: dir,
		StartedAt: st.StartedAt, LastUpdate: st.LastUpdate, EndedAt: st.EndedAt,
		OutputFile: st.OutputFile, CurrentStep: st.CurrentStep, StepCount: st.ChainStepCount,
		Error: st.Error, TimedOut: st.TimedOut, Stopped: st.Stopped, SessionID: st.SessionID,
		fromDisk: true,
	}
	if v.StepCount == 0 {
		v.StepCount = len(st.Steps)
	}
	for _, s := range st.Steps {
		cp := *s
		v.Steps = append(v.Steps, &cp)
	}
	return v
}

// stepStatuses lists the step states for the shared aggregate helpers.
func (v *runView) stepStatuses() []string {
	out := make([]string, 0, len(v.Steps))
	for _, s := range v.Steps {
		out = append(out, s.Status)
	}
	return out
}

// progressLabel is the original's formatAsyncRunProgressLabel for the port's
// run shapes: "step N/M" while a chain step runs, else "steps M".
func (v *runView) progressLabel() string {
	if v.Mode == "chain" && v.CurrentStep != nil {
		return fmt.Sprintf("step %d/%d", *v.CurrentStep+1, maxInt(1, v.StepCount))
	}
	return fmt.Sprintf("steps %d", maxInt(1, v.StepCount))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// stepLine is the original's formatStepLine (the list form).
func stepLine(index int, s *asyncStep, now int64) string {
	parts := []string{fmt.Sprintf("%d. %s", index+1, s.Agent), s.Status}
	if s.Status == "running" {
		if label := formatActivityLabel(ptrOf(s.StartedAt), now); label != "" {
			parts = append(parts, label)
		}
	}
	if s.DurationMs > 0 {
		parts = append(parts, formatDuration(s.DurationMs))
	}
	if s.WorktreePath != "" {
		parts = append(parts, fmt.Sprintf("worktree %s · branch %s", shortenPath(s.WorktreePath), s.Branch))
	}
	return strings.Join(parts, " | ")
}

func ptrOf(v int64) *int64 { return &v }

// runHeader is the original's formatRunHeader line.
func (v *runView) runHeader(now int64) string {
	activity := ""
	if v.State == "running" {
		last := v.LastUpdate
		if label := formatActivityLabel(&last, now); label != "" {
			activity = " | " + label
		}
	}
	cwd := v.Cwd
	if cwd == "" {
		cwd = v.Dir
	}
	return fmt.Sprintf("%s | %s%s | %s | %s | %s", v.ID, v.State, activity, v.Mode, v.progressLabel(), shortenPath(cwd))
}

// formatRunList is the original's formatAsyncRunList.
func formatRunList(views []*runView, heading string, now int64) string {
	if len(views) == 0 {
		return "No " + strings.ToLower(heading) + "."
	}
	lines := []string{fmt.Sprintf("%s: %d", heading, len(views)), ""}
	for _, v := range views {
		lines = append(lines, "- "+v.runHeader(now))
		for i, s := range v.Steps {
			lines = append(lines, "  "+stepLine(i, s, now))
		}
		if v.Error != "" {
			lines = append(lines, "  Error: "+v.Error)
		}
		if p := v.outputPath(); p != "" {
			lines = append(lines, "  output: "+shortenPath(p))
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// outputPath is the run's resolved output artifact, when it exists.
func (v *runView) outputPath() string {
	if v.OutputFile == "" {
		return ""
	}
	p := v.OutputFile
	if !filepath.IsAbs(p) {
		p = filepath.Join(v.Dir, p)
	}
	if fileExists(p) {
		return p
	}
	return ""
}

// activeViews lists this session's live runs plus repaired disk runs.
func activeViews(sessionID string) []*runView {
	views := []*runView{}
	for _, r := range runs.activeForSession(sessionID) {
		views = append(views, viewOfRun(r))
	}
	return views
}

// inspectStatus is the `status` action. fleet is the fleet view, transcript the
// run's transcript; without either it is the run's status or the active list.
func inspectStatus(ctx sdk.Context, params map[string]any) manageResult {
	if view, _ := params["view"].(string); view != "" && view != "fleet" && view != "transcript" {
		return manageResult{fmt.Sprintf("Unknown status view: %s. Valid: fleet, transcript.", view), true}
	}
	sessionID := sessionIDOf(ctx)
	if params["view"] == "fleet" {
		return inspectFleet(sessionID)
	}
	id, _ := params["id"].(string)
	runID, _ := params["runId"].(string)
	dir, _ := params["dir"].(string)
	lines, hasLines := intParam(params, "lines")
	index, hasIndex := intParam(params, "index")
	requested := id
	if requested == "" {
		requested = runID
	}

	if dir == "" && requested == "" {
		views := activeViews(sessionID)
		if params["view"] == "transcript" {
			if len(views) == 0 {
				return manageResult{"No active async run transcript is available.", true}
			}
			if len(views) == 1 {
				return runTranscript(views[0], index, hasIndex, lines, hasLines)
			}
			return manageResult{fmt.Sprintf("Transcript view requires an id when %d active async runs exist. Use subagent({ action: \"status\", view: \"fleet\" }) to choose one.", len(views)), true}
		}
		return manageResult{text: formatRunList(views, "Active async runs", nowMs())}
	}

	// Exact run in this session, else a run directory, else its result file.
	var v *runView
	if r := runs.resolveRun(requested); r != nil && (dir == "" || dir == r.Dir) {
		r.mu.Lock()
		owned := r.SessionID == "" || sessionID == "" || r.SessionID == sessionID
		r.mu.Unlock()
		if owned {
			v = viewOfRun(r)
		}
	}
	if v == nil {
		scanDir := dir
		if scanDir == "" {
			scanDir = filepath.Join(asyncRootDir(), requested)
		}
		if st := reconcileDiskRun(scanDir); st != nil {
			v = viewOfStatus(st, scanDir)
		}
	}
	if v == nil {
		if res := readResultFile(requested); res != nil {
			return resultFileStatus(res, resultPath(requested))
		}
		if requested == "" {
			return manageResult{"Async run not found. Provide id or dir.", true}
		}
		return manageResult{"Async run not found. Provide id or dir.", true}
	}
	if sessionID != "" && v.SessionID != "" && v.SessionID != sessionID && dir == "" {
		// A run another session owns is not listed here; an explicit dir still reads it.
		return manageResult{fmt.Sprintf("Async run '%s' was not found in the active session.", v.ID), true}
	}
	if params["view"] == "transcript" {
		return runTranscript(v, index, hasIndex, lines, hasLines)
	}
	return manageResult{text: formatRunStatus(v)}
}

func intParam(params map[string]any, name string) (int, bool) {
	switch v := params[name].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
}

// formatRunStatus is the original's per-run status text, the sections a
// single run of this port can carry.
func formatRunStatus(v *runView) string {
	lines := []string{"Run: " + v.ID, "State: " + v.State}
	if v.State == "running" && runHasPendingSupervisor(v.ID, v.SessionID) {
		// The original reports a child that waits on its supervisor as needing attention.
		lines = append(lines, "Activity: needs attention")
	}
	if v.Error != "" {
		lines = append(lines, "Error: "+v.Error)
	}
	lines = append(lines, "Mode: "+v.Mode, "Progress: "+v.progressLabel())
	if started := isoTime(v.StartedAt); started != "" {
		lines = append(lines, "Started: "+started)
	}
	updated := isoTime(v.LastUpdate)
	if updated == "" {
		updated = "n/a"
	}
	lines = append(lines, "Updated: "+updated)
	lines = append(lines, "Dir: "+v.Dir)
	if p := v.outputPath(); p != "" {
		lines = append(lines, "Output: "+p)
	}
	for i, s := range v.Steps {
		parts := []string{fmt.Sprintf("Step %d: %s %s", i+1, s.Agent, s.Status)}
		if s.Error != "" {
			parts = append(parts, ", error: "+s.Error)
		}
		lines = append(lines, strings.Join(parts, ""))
		if s.Status == "running" {
			lines = append(lines, fmt.Sprintf("  Steer: subagent({ action: \"steer\", id: \"%s\", index: %d, message: \"...\" })", v.ID, i))
		}
		if p := filepath.Join(v.Dir, fmt.Sprintf("output-%d.log", i)); fileExists(p) {
			lines = append(lines, "  Output: "+p)
		}
	}
	if v.State == "running" {
		lines = append(lines, fmt.Sprintf("Steer running child: subagent({ action: \"steer\", id: \"%s\", message: \"...\" })", v.ID))
	} else {
		lines = append(lines, resumeGuidance(v))
	}
	if p := filepath.Join(v.Dir, fmt.Sprintf("subagent-log-%s.md", v.ID)); fileExists(p) {
		lines = append(lines, "Log: "+p)
	}
	if p := filepath.Join(v.Dir, "events.jsonl"); fileExists(p) {
		lines = append(lines, "Events: "+p)
	}
	return strings.Join(lines, "\n")
}

// resumeGuidance is the original's formatResumeGuidance for a run whose children
// persisted no session file (this port's children are child processes).
func resumeGuidance(v *runView) string {
	if v.Stopped || v.State == "stopped" {
		return "Resume: unavailable; stopped runs are not resumable. Start a new run instead."
	}
	return "Resume: unavailable; no child session file was persisted."
}

// runTranscript is the original's formatAsyncRunTranscript over the port's
// artifacts: the child's output log tail.
func runTranscript(v *runView, index int, hasIndex bool, lines int, hasLines bool) manageResult {
	limit := defaultTranscriptLines
	if hasLines {
		limit = maxInt(1, minInt(maxTranscriptLines, lines))
	}
	selected := 0
	if hasIndex {
		if index < 0 || index >= maxInt(1, len(v.Steps)) {
			return manageResult{fmt.Sprintf("Transcript index %d is out of range for %d child step%s.", index, maxInt(1, len(v.Steps)), plural(maxInt(1, len(v.Steps)))), true}
		}
		selected = index
	}
	header := []string{"Run: " + v.ID, "State: " + v.State, "Mode: " + v.Mode,
		fmt.Sprintf("Step %d: %s %s", selected, v.Steps[selected].Agent, v.Steps[selected].Status)}
	artifacts := []string{}
	outputPath := filepath.Join(v.Dir, fmt.Sprintf("output-%d.log", selected))
	if fileExists(outputPath) {
		artifacts = append(artifacts, "Output: "+outputPath)
	} else if p := v.outputPath(); p != "" {
		artifacts = append(artifacts, "Output: "+p)
	}
	eventsPath := filepath.Join(v.Dir, "events.jsonl")
	if fileExists(eventsPath) {
		artifacts = append(artifacts, "Events: "+eventsPath)
	}
	if len(artifacts) > 0 {
		header = append(header, "Artifacts:")
		for _, a := range artifacts {
			header = append(header, "  "+a)
		}
	}
	source := "Transcript tail"
	tail := []string{}
	truncated := false
	if fileExists(outputPath) {
		tail, truncated = textTail(outputPath, limit)
		source = "Transcript tail from " + outputPath
	}
	body := []string{source + tailTruncation(truncated) + ":"}
	if len(tail) == 0 {
		body = append(body, "  (no transcript lines available yet)")
	} else {
		body = append(body, tail...)
	}
	return manageResult{text: strings.Join(append(header, body...), "\n")}
}

func tailTruncation(truncated bool) string {
	if truncated {
		return " (tail truncated)"
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// textTail reads the last limit lines of a text file.
func textTail(path string, limit int) ([]string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	all := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for len(all) > 0 && all[len(all)-1] == "" {
		all = all[:len(all)-1]
	}
	if len(all) <= limit {
		return all, false
	}
	return all[len(all)-limit:], true
}

// readResultFile loads async-subagent-results/<id>.json.
func readResultFile(id string) *asyncResultFile {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return nil
	}
	data, err := os.ReadFile(resultPath(id))
	if err != nil {
		return nil
	}
	var res asyncResultFile
	if err := json.Unmarshal(data, &res); err != nil {
		return nil
	}
	return &res
}

// resultFileStatus is the original's result-file branch of the status action.
func resultFileStatus(res *asyncResultFile, path string) manageResult {
	status := "failed"
	switch {
	case res.State == "stopped" || res.Stopped:
		status = "stopped"
	case res.Success:
		status = "complete"
	case res.State == "paused" || res.ExitCode == 0:
		status = "paused"
	}
	runID := res.RunID
	if runID == "" {
		runID = res.ID
	}
	lines := []string{"Run: " + runID, "State: " + status, "Result: " + path}
	lines = append(lines, resumeGuidance(&runView{Stopped: res.Stopped, State: res.State}))
	if res.Summary != "" {
		lines = append(lines, "", res.Summary)
	}
	return manageResult{text: strings.Join(lines, "\n")}
}

// inspectFleet is the original's inspectSubagentFleet for the port's runs.
func inspectFleet(sessionID string) manageResult {
	views := activeViews(sessionID)
	if len(views) == 0 {
		return manageResult{text: "No active subagent fleet. Background runs that already finished are available through completion notifications or subagent({ action: \"status\", id: \"...\" })."}
	}
	sort.SliceStable(views, func(i, j int) bool { return views[i].LastUpdate < views[j].LastUpdate })
	now := nowMs()
	lines := []string{fmt.Sprintf("Subagent fleet: %d tracked", len(views)), "", "Async runs:"}
	for _, v := range views {
		lines = append(lines, "- "+v.runHeader(now))
		lines = append(lines, fmt.Sprintf("  status: subagent({ action: \"status\", id: \"%s\" })", v.ID))
		lines = append(lines, fmt.Sprintf("  transcript: subagent({ action: \"status\", id: \"%s\", view: \"transcript\" })", v.ID))
		for i, s := range v.Steps {
			parts := []string{fmt.Sprintf("%d. %s", i, s.Agent), s.Status}
			if s.Status == "running" {
				if label := formatActivityLabel(ptrOf(s.StartedAt), now); label != "" {
					parts = append(parts, label)
				}
			}
			lines = append(lines, "  "+strings.Join(parts, " | "))
			if p := filepath.Join(v.Dir, fmt.Sprintf("output-%d.log", i)); fileExists(p) {
				lines = append(lines, "    output: "+shortenPath(p))
			}
			if s.Status == "running" || fileExists(filepath.Join(v.Dir, fmt.Sprintf("output-%d.log", i))) {
				lines = append(lines, fmt.Sprintf("    transcript: subagent({ action: \"status\", id: \"%s\", index: %d, view: \"transcript\" })", v.ID, i))
			}
		}
		if v.Error != "" {
			lines = append(lines, "  error: "+v.Error)
		}
		if p := v.outputPath(); p != "" {
			lines = append(lines, "  output: "+shortenPath(p))
		}
	}
	lines = append(lines, "", "Commands:")
	lines = append(lines, "  Refresh fleet: subagent({ action: \"status\", view: \"fleet\" })")
	lines = append(lines, "  Tail run transcript: subagent({ action: \"status\", id: \"<run-id>\", view: \"transcript\" })")
	lines = append(lines, "  Tail child transcript: subagent({ action: \"status\", id: \"<run-id>\", index: 0, view: \"transcript\" })")
	return manageResult{text: strings.Join(lines, "\n")}
}
