package pi_subagents

// The tool and action surface of the background runs: bg_wait, the notification renderer, and the status, interrupt, stop and
// resume actions of `subagent`. Ports wait-tool.ts, the renderer of index.ts, inspectSubagentStatus (run-status.ts),
// stopAsyncRun (async-stop-action.ts), interruptAsyncRun and resumeAsyncRun (subagent-executor.ts).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func shortDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mgmtError(text string) error { return sdk.NewToolError(text) }

func mgmtOK(text string) any {
	return explicitOK{Content: text, Details: map[string]any{"mode": "management", "results": []any{}}}
}

// ── bg_wait ──

func bgWaitDescription(defs map[string]toolDef, cfg asyncConfig) string {
	d := defs["bg_wait"].Description
	if !cfg.WaitTool.Enabled && !strings.Contains(d, "Configured behavior") {
		d += "\n\nConfigured behavior: bg_wait is disabled by config.waitTool or PI_SUBAGENT_WAIT_TOOL_ENABLED and returns immediately without blocking."
	}
	return d
}

func bgWaitToolDef(rt *asyncRuntime, cfg asyncConfig) sdk.ToolDefinition {
	defs := toolDefs()
	return sdk.ToolDefinition{Name: "bg_wait", Label: "Background Wait", Description: bgWaitDescription(defs, cfg), Parameters: defs["bg_wait"].Parameters,
		Execute: func(ctx sdk.Context, p map[string]any) (any, error) { return rt.bgWait(ctx, p) }}
}

func (rt *asyncRuntime) bgWait(ctx sdk.Context, p map[string]any) (any, error) {
	rt.setCtx(ctx)
	cfg := loadAsyncConfig()
	if cfg.WaitToolErr != nil {
		return nil, sdk.NewToolError(cfg.WaitToolErr.Error())
	}
	rt.mu.Lock()
	if rt.state.CurrentSessionID == "" {
		rt.state.CurrentSessionID = currentSessionID(ctx)
	}
	rt.mu.Unlock()
	params := WaitParams{}
	params.ID, _ = p["id"].(string)
	params.NonBlocking, _ = p["nonBlocking"].(bool)
	params.All, _ = p["all"].(bool)
	if f, ok := p["timeoutMs"].(float64); ok {
		params.TimeoutMs = new(int64(f))
	}
	if b, ok := p["stopOnAttention"].(bool); ok {
		params.StopOnAttention = &b
	}
	enabled := cfg.WaitTool.Enabled
	deps := WaitDeps{State: rt.state, Events: rt.bus, AsyncDirRoot: rt.dirs.Async, ResultsDir: rt.dirs.Results, Enabled: &enabled}
	if cfg.WaitTool.DefaultTimeoutMs != nil {
		deps.DefaultTimeoutMs = new(int64(*cfg.WaitTool.DefaultTimeoutMs))
	}
	if ctx.HasUI() {
		deps.Subscribe = func(string, string, string, int64) (string, int64, error) {
			return "", 0, fmt.Errorf("%s", refusal("non-blocking wait subscriptions (`nonBlocking`)"))
		}
	}
	signal, stop := cancellation(ctx)
	defer stop()
	res := waitForSubagents(params, signal, deps)
	return waitToolResult(res)
}

// waitToolResult is the tool result of a wait; a failed wait is a failed tool call (the host drops the details of a failed result).
func waitToolResult(r WaitResult) (any, error) {
	if r.IsError {
		return nil, sdk.NewToolError(r.Text)
	}
	details := map[string]any{"mode": "management", "results": []any{}}
	if len(r.Completions) > 0 {
		details["completions"] = r.Completions
	}
	if r.Wait != nil {
		details["wait"] = r.Wait
	}
	tr := sdk.ToolResult{Content: r.Text, Details: details}
	if r.Usage != nil {
		tr.Usage = r.Usage
	}
	return tr, nil
}

// ── the notification renderer ──

func renderNotifyMessage(_ sdk.Context, message map[string]any, opts sdk.MessageRenderOptions, _ int) ([]string, error) {
	content, _ := message["content"].(string)
	d := parseSubagentNotifyContent(content)
	if d == nil {
		return strings.Split(content, "\n"), nil
	}
	icon := "✓"
	switch d.Status {
	case "paused":
		icon = "■"
	case "failed", "stopped":
		icon = "✗"
	}
	head := fmt.Sprintf("%s %s %s", icon, d.Agent, d.Status)
	var parts []string
	if d.TaskInfo != "" {
		parts = append(parts, d.TaskInfo)
	}
	if len(parts) > 0 {
		head += " · " + strings.Join(parts, " · ")
	}
	lines := []string{head}
	preview := jsTrim(d.ResultPreview)
	var pl []string
	for _, l := range strings.Split(preview, "\n") {
		if jsTrim(l) != "" {
			pl = append(pl, l)
		}
	}
	if !opts.Expanded && len(pl) > 1 {
		pl = pl[:1]
	}
	if len(pl) == 0 {
		pl = []string{"(no output)"}
	}
	for _, l := range pl {
		lines = append(lines, "  ⎿  "+l)
	}
	if !opts.Expanded && strings.Contains(preview, "\n") {
		lines = append(lines, "  full notification (expand)")
	}
	if d.WorkflowRunID != "" {
		lines = append(lines, "  workflow: "+d.WorkflowRunID)
	}
	if d.SessionLabel != "" && d.SessionValue != "" {
		lines = append(lines, fmt.Sprintf("  %s: %s", d.SessionLabel, bgShortenPath(d.SessionValue)))
	}
	return lines, nil
}

// ── status ──

func strParam(p map[string]any, k string) string {
	s, _ := p[k].(string)
	return s
}

func formatStatusTargetLabel(id, dir string) string {
	target := "active runs"
	switch {
	case id != "":
		target = "run " + id
	case dir != "":
		target = "dir " + dir
	}
	return "Status target: " + target
}

func formatResumeGuidance(runID string, steps []AsyncStep, fallbackSession string, stopped bool) string {
	if stopped {
		return "Resume: unavailable; stopped runs are not resumable. Start a new run instead."
	}
	if runID == "" || len(steps) == 0 {
		return "Resume: unavailable; no child session file was persisted."
	}
	session := steps[0].SessionFile
	if session == "" {
		session = fallbackSession
	}
	if len(steps) == 1 && session != "" && regularFileExists(session) {
		return fmt.Sprintf("Revive: subagent({ action: \"resume\", id: %q, message: \"...\" })", runID)
	}
	for i, s := range steps {
		if s.SessionFile != "" && regularFileExists(s.SessionFile) {
			return fmt.Sprintf("Revive child: subagent({ action: \"resume\", id: %q, index: %d, message: \"...\" })", runID, i)
		}
	}
	return "Resume: unavailable; no child session file was persisted."
}

func isoTime(msv int64) string { return time.UnixMilli(msv).UTC().Format("2006-01-02T15:04:05.000Z") }

// statusLines is the text of one run (inspectSubagentStatus for a single async child).
func (rt *asyncRuntime) statusText(asyncDir, resolvedID string, rec ReconcileResult) string {
	st := rec.Status
	effective := firstNonEmpty(st.RunID, resolvedID, "unknown")
	logPath := filepath.Join(asyncDir, fmt.Sprintf("subagent-log-%s.md", effective))
	eventsPath := filepath.Join(asyncDir, "events.jsonl")
	outputPath := formatAsyncRunOutputPath(asyncDir, st.OutputFile)
	stepCount := len(st.Steps)
	if stepCount == 0 {
		stepCount = 1
	}
	progress := formatAsyncRunProgressLabel(st.Mode, st.CurrentStep, len(st.Steps))
	updated := "n/a"
	if st.LastUpdate != nil {
		updated = isoTime(int64(*st.LastUpdate))
	}
	var lines []string
	add := func(l string) {
		if l != "" {
			lines = append(lines, l)
		}
	}
	add("Run: " + st.RunID)
	if st.ToolCallID != "" {
		add("Tool call: " + st.ToolCallID)
	}
	add("State: " + st.State)
	if pt := readProcessTerminal(asyncDir, st.RunID, runnerIDOf(st)); pt != nil || st.ProcessTerminal != nil {
		if pt == nil {
			pt = sanitizeProcessTerminal(st.ProcessTerminal, st.RunID, runnerIDOf(st), filepath.Join(asyncDir, "status.json"))
		}
		reason := ""
		if pt.Reason != "" {
			reason = " (" + pt.Reason + ")"
		}
		add("Process terminal: " + pt.State + reason)
	}
	if st.Error != "" {
		add("Error: " + st.Error)
	}
	if st.State == "running" {
		if a := bgActivityLabel(st.LastActivityAt, st.ActivityState, nowMs()); a != "" {
			add("Activity: " + a)
		}
	}
	add("Mode: " + st.Mode)
	add("Progress: " + progress)
	add("Started: " + isoTime(int64(st.StartedAt)))
	add("Updated: " + updated)
	add("Dir: " + asyncDir)
	add("Output: " + outputPath)
	if rec.Message != "" {
		add("Diagnosis: " + rec.Message)
	}
	if rec.ResultPath != "" && regularFileExists(rec.ResultPath) {
		add("Result: " + rec.ResultPath)
	}
	for i, step := range st.Steps {
		var act string
		if step.Status == "running" {
			act = bgActivityLabel(step.LastActivityAt, step.ActivityState, nowMs())
		}
		mt := ""
		if m := bgFormatModelThinking(step.Model, step.Thinking); m != "" {
			mt = " (" + m + ")"
		}
		display := firstNonEmpty(jsTrim(step.SessionName), step.Agent)
		line := fmt.Sprintf("Step %d: %s %s%s", i+1, display, step.Status, mt)
		if act != "" {
			line += ", " + act
		}
		if step.Error != "" {
			line += ", error: " + step.Error
		}
		add(line)
		lines = append(lines, formatTimeoutRecoveryLines(step.TimeoutRecovery, "  ")...)
		if sp := filepath.Join(asyncDir, fmt.Sprintf("output-%d.log", i)); sp != outputPath && regularFileExists(sp) {
			add("  Output: " + sp)
		}
	}
	if st.SessionFile != "" {
		add("Session: " + st.SessionFile)
	}
	if st.State != "running" {
		add(formatResumeGuidance(st.RunID, st.Steps, st.SessionFile, st.State == "stopped" || (st.Stopped != nil && *st.Stopped)))
	}
	if regularFileExists(logPath) {
		add("Log: " + logPath)
	}
	if regularFileExists(eventsPath) {
		add("Events: " + eventsPath)
	}
	return strings.Join(lines, "\n")
}

func runnerIDOf(st *AsyncStatus) string {
	if st.ProcessTerminal != nil {
		return st.ProcessTerminal.RunnerProcessInstanceID
	}
	return ""
}

func (rt *asyncRuntime) statusAction(ctx sdk.Context, p map[string]any) (any, error) {
	rt.setCtx(ctx)
	sessionID := currentSessionID(ctx)
	rt.mu.Lock()
	rt.state.CurrentSessionID = sessionID
	rt.mu.Unlock()
	targetID := firstNonEmpty(strParam(p, "id"), strParam(p, "runId"))
	dir := strParam(p, "dir")
	// withBudget (subagent-executor.ts): the target label, the budget lines and the budget details; a failed status is a failed tool
	// call (the host drops the result's details).
	wrap := func(text string, isErr bool) (any, error) {
		budget, spawnBudget, capacity := statusBudget(sessionID)
		full := fmt.Sprintf("%s\n%s\n%s", formatStatusTargetLabel(targetID, dir), budget, text)
		if isErr {
			return nil, sdk.NewToolError(full)
		}
		return sdk.ToolResult{Content: full, Details: obj("mode", "single", "results", []any{}, "spawnBudget", spawnBudget, "activeAsyncCapacity", capacity)}, nil
	}
	if targetID == "" && dir == "" {
		runs, err := listAsyncRuns(rt.dirs.Async, AsyncRunListOptions{States: activeWaitStates, SessionID: sessionID, ResultsDir: rt.dirs.Results})
		if err != nil {
			return wrap(err.Error(), true)
		}
		return wrap(formatAsyncRunList(runs, ""), false)
	}
	var loc AsyncRunLocation
	var err error
	if dir == "" {
		resolved, rerr := resolveSubagentRunID(targetID, rt.state, rt.dirs.Async, rt.dirs.Results)
		if rerr != nil {
			return wrap(rerr.Error(), true)
		}
		switch {
		case resolved != nil && resolved.Kind == "foreground":
			return wrap(fmt.Sprintf("Run: %s\nState: remembered foreground", resolved.ID), false)
		case resolved != nil:
			loc = resolved.Location
		default:
			loc = AsyncRunLocation{ResolvedID: targetID}
		}
	} else if loc, err = resolveAsyncRunLocation(RunLocationParams{ID: strParam(p, "id"), RunID: strParam(p, "runId"), Dir: dir}, rt.dirs.Async, rt.dirs.Results); err != nil {
		return wrap(err.Error(), true)
	}
	if loc.AsyncDir == "" && loc.ResultPath == "" {
		return wrap("Async run not found. Provide id or dir.", true)
	}
	if loc.AsyncDir != "" {
		rec, rerr := reconcileAsyncRun(loc.AsyncDir, ReconcileOptions{ResultsDir: rt.dirs.Results})
		if rerr != nil {
			return wrap(rerr.Error(), true)
		}
		if rec.Status != nil {
			return wrap(rt.statusText(loc.AsyncDir, loc.ResolvedID, rec), false)
		}
	}
	if loc.ResultPath != "" {
		return wrap(rt.resultOnlyStatus(loc))
	}
	return wrap("Status file not found.", true)
}

// resultOnlyStatus is the status of a run whose directory is gone but whose result file remains.
func (rt *asyncRuntime) resultOnlyStatus(loc AsyncRunLocation) (string, bool) {
	var data map[string]any
	if err := readJSONFile(loc.ResultPath, &data); err != nil {
		return "Failed to read async result file: " + err.Error(), true
	}
	state, _ := data["state"].(string)
	exit, hasExit := finiteNumber(data["exitCode"])
	status := "failed"
	switch {
	case state == "stopped" || data["stopped"] == true:
		status = "stopped"
	case data["success"] == true:
		status = "complete"
	case state == "paused" || (hasExit && exit == 0):
		status = "paused"
	}
	runID := firstNonEmpty(nonEmptyString(data["runId"]), nonEmptyString(data["id"]), loc.ResolvedID)
	lines := []string{"Run: " + runID}
	if tc := nonEmptyString(data["toolCallId"]); tc != "" {
		lines = append(lines, "Tool call: "+tc)
	}
	lines = append(lines, "State: "+status, "Result: "+loc.ResultPath)
	var children []AsyncStep
	if arr, ok := data["results"].([]any); ok {
		for _, e := range arr {
			if m, ok := e.(map[string]any); ok {
				children = append(children, AsyncStep{Agent: nonEmptyString(m["agent"]), SessionFile: nonEmptyString(m["sessionFile"])})
				if tr, ok := m["timeoutRecovery"].(map[string]any); ok {
					lines = append(lines, formatTimeoutRecoveryLines(tr, "  ")...)
				}
			}
		}
	} else if a := nonEmptyString(data["agent"]); a != "" {
		children = []AsyncStep{{Agent: a, SessionFile: nonEmptyString(data["sessionFile"])}}
	}
	lines = append(lines, formatResumeGuidance(runID, children, nonEmptyString(data["sessionFile"]), status == "stopped"))
	if s := nonEmptyString(data["summary"]); s != "" {
		lines = append(lines, "", s)
	}
	return strings.Join(lines, "\n"), false
}

// ── interrupt and stop ──

func (rt *asyncRuntime) targetRun(ctx sdk.Context, p map[string]any) (asyncDir, id string, errText string) {
	rt.setCtx(ctx)
	rt.mu.Lock()
	rt.state.CurrentSessionID = currentSessionID(ctx)
	rt.mu.Unlock()
	targetID := firstNonEmpty(strParam(p, "runId"), strParam(p, "id"))
	if dir := strParam(p, "dir"); dir != "" {
		loc, err := resolveAsyncRunLocation(RunLocationParams{ID: strParam(p, "id"), RunID: strParam(p, "runId"), Dir: dir}, rt.dirs.Async, rt.dirs.Results)
		if err != nil {
			return "", "", err.Error()
		}
		return loc.AsyncDir, firstNonEmpty(loc.ResolvedID, filepath.Base(loc.AsyncDir)), ""
	}
	if targetID == "" {
		return "", "", ""
	}
	resolved, err := resolveSubagentRunID(targetID, rt.state, rt.dirs.Async, rt.dirs.Results)
	if err != nil {
		return "", "", err.Error()
	}
	if resolved != nil && resolved.Kind == "async" {
		return resolved.Location.AsyncDir, resolved.ID, ""
	}
	if resolved != nil && resolved.Kind == "foreground" {
		return "", resolved.ID, "foreground"
	}
	if j := rt.state.AsyncJobs[targetID]; j != nil {
		return j.AsyncDir, j.AsyncID, ""
	}
	return "", targetID, ""
}

func (rt *asyncRuntime) interruptAction(ctx sdk.Context, p map[string]any) (any, error) {
	asyncDir, id, why := rt.targetRun(ctx, p)
	if why != "" && why != "foreground" {
		return nil, mgmtError(why)
	}
	if asyncDir == "" && why == "" && id == "" {
		// newest running job of this session (the original's fallback)
		rt.mu.Lock()
		var newest *AsyncJobState
		for _, j := range rt.state.AsyncJobs {
			if j.Status == "running" && (newest == nil || j.UpdatedAt > newest.UpdatedAt) {
				newest = j
			}
		}
		rt.mu.Unlock()
		if newest != nil {
			asyncDir, id = newest.AsyncDir, newest.AsyncID
		}
	}
	if why == "foreground" || asyncDir == "" {
		return nil, mgmtError("No interrupt-capable run found in this session.")
	}
	rec, err := reconcileAsyncRun(asyncDir, ReconcileOptions{ResultsDir: rt.dirs.Results, Kill: asyncKill})
	if err != nil {
		return nil, mgmtError(err.Error())
	}
	st := rec.Status
	if st == nil || st.State != "running" || st.PID == nil {
		return nil, mgmtError(fmt.Sprintf("No running async run with an interrupt-capable pid was found for '%s'.", firstNonEmpty(id, "current")))
	}
	if external := runningStepsAreExternal(st); external {
		return nil, mgmtError(fmt.Sprintf("Interrupt is unsupported for external async run %s; use stop instead.", id))
	}
	if st.Mode == "workflow" {
		return nil, mgmtError(fmt.Sprintf("Interrupt is unsupported for async workflow %s; use stop instead.", id))
	}
	if _, err := requestAsyncInterrupt(asyncDir, "interrupt-action"); err != nil {
		return nil, mgmtError(fmt.Sprintf("Failed to interrupt async run %s: %s", id, err.Error()))
	}
	rt.mu.Lock()
	if j := rt.state.AsyncJobs[id]; j != nil {
		j.ActivityState, j.UpdatedAt = "", nowMs()
	}
	rt.mu.Unlock()
	return mgmtOK(fmt.Sprintf("Interrupt requested for async run %s.", id)), nil
}

const stopMessageText = stopMessage

// asyncKill is the process probe the control actions give the reconciler (nil: the system's); a test replaces it.
var asyncKill killFunc

// runningStepsAreExternal is whether every running step is an external CLI or job runner, which cannot be interrupted.
func runningStepsAreExternal(st *AsyncStatus) bool {
	running, external := 0, 0
	for _, s := range st.Steps {
		if s.Status != "running" {
			continue
		}
		running++
		var runner struct {
			Type string `json:"type"`
		}
		if raw, ok := s.Extra["runner"]; ok && json.Unmarshal(raw, &runner) == nil && (runner.Type == "external-cli" || runner.Type == "external-job") {
			external++
		}
	}
	return running > 0 && running == external
}

func (rt *asyncRuntime) stopAction(ctx sdk.Context, p map[string]any) (any, error) {
	asyncDir, id, why := rt.targetRun(ctx, p)
	if why == "foreground" {
		return nil, mgmtError("action='stop' supports async runs only. Use action='interrupt' for foreground runs.")
	}
	if why != "" {
		return nil, mgmtError(why)
	}
	if asyncDir == "" && strParam(p, "dir") == "" && id == "" {
		return nil, mgmtError("action='stop' requires id or dir.")
	}
	if asyncDir == "" {
		if strParam(p, "dir") != "" {
			return nil, mgmtError(fmt.Sprintf("No running or queued async run was found for '%s'.", firstNonEmpty(id, strParam(p, "dir"))))
		}
		return nil, mgmtError("No stoppable async run found in this session.")
	}
	rec, err := reconcileAsyncRun(asyncDir, ReconcileOptions{ResultsDir: rt.dirs.Results, Kill: asyncKill})
	if err != nil {
		return nil, mgmtError(err.Error())
	}
	st := rec.Status
	rt.mu.Lock()
	session := rt.state.CurrentSessionID
	rt.mu.Unlock()
	if session != "" && (st == nil || st.SessionID != session) {
		return nil, mgmtError(fmt.Sprintf("Async run '%s' was not found in the active session.", id))
	}
	pausedWhole := st != nil && st.State == "paused" && strParam(p, "childId") == ""
	if st == nil || (st.State != "running" && st.State != "queued" && !pausedWhole) {
		return nil, mgmtError(fmt.Sprintf("No running or queued async run was found for '%s'.", firstNonEmpty(id, "current")))
	}
	var idx *int
	childID := strParam(p, "childId")
	if childID != "" {
		matches := 0
		for i, s := range st.Steps {
			for _, c := range asyncStatusChildIdentityCandidates(s, i) {
				if c == childID {
					matches++
					idx = intPtr(i)
					if s.Status != "pending" && s.Status != "running" {
						return nil, mgmtError(fmt.Sprintf("Child '%s' in async run '%s' is %s; stop only supports pending or running children.", childID, st.RunID, s.Status))
					}
					break
				}
			}
		}
		switch {
		case matches == 0:
			return nil, mgmtError(fmt.Sprintf("Child '%s' was not found under async run '%s'.", childID, st.RunID))
		case matches > 1:
			return nil, mgmtError(fmt.Sprintf("Child '%s' is ambiguous under async run '%s'.", childID, st.RunID))
		}
	}
	if pausedWhole && regularFileExists(stopInboxClosedPath(asyncDir)) {
		// the runner closed its inbox: only exact process proof can seal a paused run
	} else if _, err := requestAsyncStop(asyncDir, "stop-action", idx, childID); err != nil {
		return nil, mgmtError(fmt.Sprintf("Failed to stop async run %s: %s", id, err.Error()))
	}
	if pausedWhole {
		if failure := rt.sealPausedRun(asyncDir, st); failure != "" {
			return nil, mgmtError(fmt.Sprintf("Stop request persisted for paused async run %s, but terminal proof is not ready (%s). Retry stop after runner shutdown is observed.", id, failure))
		}
	}
	rt.mu.Lock()
	if j := rt.state.AsyncJobs[id]; j != nil {
		j.ActivityState, j.UpdatedAt = "", nowMs()
	}
	rt.mu.Unlock()
	switch {
	case pausedWhole:
		return mgmtOK(fmt.Sprintf("Stopped paused async run %s.", id)), nil
	case childID != "":
		return mgmtOK(fmt.Sprintf("Stop requested for child %s in async run %s.", asyncStatusChildIdentity(st.Steps[*idx], *idx), id)), nil
	}
	return mgmtOK(fmt.Sprintf("Stop requested for async run %s.", id)), nil
}

// sealPausedRun turns a paused run into a stopped one, but only with exact observed runner proof; it returns why not.
func (rt *asyncRuntime) sealPausedRun(asyncDir string, st *AsyncStatus) string {
	runnerID := runnerIDOf(st)
	if runnerID == "" {
		return "runner process identity is missing"
	}
	proof := readProcessTerminal(asyncDir, st.RunID, runnerID)
	if proof == nil || proof.State != "observed" || proof.RunID != st.RunID || proof.RunnerProcessInstanceID != runnerID {
		if proof != nil && proof.State == "unknown" {
			return fmt.Sprintf("process-terminal proof is unknown (%s)", proof.Reason)
		}
		state := "missing"
		if proof != nil {
			state = proof.State
		}
		return "process-terminal proof is " + state
	}
	if st.SessionID == "" {
		return "session identity is missing"
	}
	existing, _ := resultPayloadPathForSessionRun(rt.dirs.Results, st.SessionID, st.RunID)
	if existing == "" {
		return "paused result is missing"
	}
	var res map[string]any
	if err := readJSONFile(existing, &res); err != nil {
		return "paused result is unavailable: " + err.Error()
	}
	if firstNonEmpty(nonEmptyString(res["runId"]), nonEmptyString(res["id"])) != st.RunID || res["sessionId"] != st.SessionID {
		return "paused result identity does not match the run"
	}
	now := nowMs()
	for i := range st.Steps {
		s := &st.Steps[i]
		if s.Status == "pending" || s.Status == "running" || s.Status == "paused" {
			s.Status, s.Error, s.ExitCode, s.Stopped, s.EndedAt, s.ActivityState = "stopped", stopMessage, intPtr(1), new(true), msPtrOf(now), ""
			if s.StartedAt != nil {
				s.DurationMs = msPtrOf(now - int64(*s.StartedAt))
			} else {
				s.DurationMs = msPtrOf(0)
			}
			s.LastActivityAt = msPtrOf(now)
		}
	}
	if arr, ok := res["results"].([]any); ok {
		for i, e := range arr {
			if m, ok := e.(map[string]any); ok && i < len(st.Steps) && st.Steps[i].Status == "stopped" {
				delete(m, "interrupted")
				m["output"], m["error"], m["success"], m["exitCode"], m["stopped"] = stopMessage, stopMessage, false, 1, true
			}
		}
	}
	res["success"], res["state"], res["summary"], res["error"], res["stopped"], res["exitCode"], res["timestamp"] = false, "stopped", stopMessage, stopMessage, true, 1, now
	st.State, st.Stopped, st.Error, st.EndedAt, st.LastUpdate, st.ActivityState = "stopped", new(true), stopMessage, msPtrOf(now), msPtrOf(now), ""
	_, _ = writeAsyncResultFile(resultFilePath(rt.dirs.Results, st.RunID), res)
	_ = writeStatus(asyncDir, st)
	_ = updateActiveRunIndex(asyncDir, "stopped", st.ToolCallID, indexOptions{retryCapacityErrors: true, terminalIndexBeforeRelease: true})
	return ""
}

// ── resume ──

func buildRevivedAsyncTask(runID, agent, sessionFile, message string) string {
	lines := []string{"You are reviving a previous subagent conversation.", "", "Original run: " + runID, "Original agent: " + agent}
	if sessionFile != "" {
		lines = append(lines, "Original session file: "+sessionFile)
	}
	lines = append(lines, "", "Use the stored session context as background. Answer the orchestrator's follow-up below. Do not assume the original child session is still running.", "", "Follow-up:", message)
	return strings.Join(lines, "\n")
}

func (rt *asyncRuntime) resumeAction(ctx sdk.Context, p map[string]any) (any, error) {
	rt.setCtx(ctx)
	followUp := jsTrim(firstNonEmpty(strParam(p, "message"), strParam(p, "task")))
	if followUp == "" {
		return nil, mgmtError("action='resume' requires message.")
	}
	if p["model"] != nil {
		return nil, mgmtError("action='resume' reuses the persisted child model and does not accept a model override.")
	}
	sessionID := currentSessionID(ctx)
	rt.mu.Lock()
	rt.state.CurrentSessionID = sessionID
	rt.mu.Unlock()
	asyncDir, id, why := rt.targetRun(ctx, p)
	if why != "" {
		return nil, mgmtError(why)
	}
	if asyncDir == "" && id == "" && strParam(p, "dir") == "" {
		return nil, mgmtError("Run not found. Provide id or runId.")
	}
	target, err := rt.resolveResumeTarget(asyncDir, id, p, sessionID)
	if err != nil {
		return nil, mgmtError(err.Error())
	}
	if target.live {
		return nil, mgmtError(fmt.Sprintf("Async child '%s' index %d is still running. action='resume' only revives paused, completed, or failed children.\nSend live input with subagent({ action: \"steer\", id: \"%s\", index: %d, message: \"...\" }).", target.runID, target.index, target.runID, target.index))
	}
	if msg := nestedBlocked(os.Getenv); msg != "" {
		depth, max := subagentDepth(os.Getenv)
		return nil, mgmtError(fmt.Sprintf("Nested subagent resume blocked (depth=%d, max=%d). Complete the follow-up directly instead.", depth, max))
	}
	if len(target.unsupported) > 0 {
		return nil, mgmtError(refusal("resuming a run that carries " + optionsFeature(target.unsupported)))
	}
	cfg := loadAsyncConfig()
	d := DiscoverAgents(firstNonEmpty(target.cwd, ctx.Cwd()), ResolveExecutionAgentScope(p["agentScope"]))
	a, _ := ResolveAgentName(target.agent, d.Agents)
	if a == nil && target.descriptor != nil {
		a = &AgentConfig{Name: target.descriptor.Agent, SystemPromptMode: target.descriptor.SystemPromptMode, Source: SourceProject, FilePath: target.descriptor.AgentFilePath}
	}
	if a == nil {
		return nil, mgmtError(fmt.Sprintf("Unknown agent for resume '%s'.", target.agent))
	}
	cwd := firstNonEmpty(target.cwd, ctx.Cwd())
	task := buildRevivedAsyncTask(target.runID, target.agent, target.sessionFile, followUp)
	res, err := rt.launchBackgroundResume(ctx, p, a, task, cwd, target, cfg)
	return res, err
}

// resumeTarget is the child a resume revives (kind "revive") or the live child it points at (kind "live").
type resumeTarget struct {
	kind                 string
	runID                string
	asyncDir             string
	state                string
	mode                 string
	agent                string
	sessionName          string
	index                int
	cwd                  string
	sessionFile          string
	model                string
	thinking             string
	launchContractDigest string
	live                 bool
	descriptor           *RecoveryDescriptor
	unsupported          []string
	// carriesCeiling is whether the run recorded a capability ceiling (a slice the port does not have).
	carriesCeiling bool
}

// resolveResumeTarget is resolveAsyncResumeTarget bound to the runtime's directories and the active session.
func (rt *asyncRuntime) resolveResumeTarget(_, _ string, p map[string]any, sessionID string) (*resumeTarget, error) {
	rp := resumeParams{ID: strParam(p, "id"), RunID: strParam(p, "runId"), Dir: strParam(p, "dir")}
	if v, ok := p["index"].(float64); ok {
		rp.Index = &v
	}
	return resolveAsyncResumeTarget(rp, rt.dirs, resumeOptions{SessionID: sessionID})
}

func steps0Agent(s []AsyncStep) string {
	if len(s) > 0 {
		return s[0].Agent
	}
	return ""
}

// launchBackgroundResume starts the revived child: the persisted session continues with the follow-up.
func (rt *asyncRuntime) launchBackgroundResume(ctx sdk.Context, p map[string]any, a *AgentConfig, task, cwd string, target *resumeTarget, cfg asyncConfig) (any, error) {
	if cfg.ConfigErr != nil {
		return nil, mgmtError(cfg.ConfigErr.Error())
	}
	sessionID := currentSessionID(ctx)
	timeout, err := timeoutParams(p, firstInt64(cfg.DefaultTimeoutMs, asyncDefaultTimeoutMs))
	if err != nil {
		return nil, mgmtError(err.Error())
	}
	id := randomUUID()
	asyncDir := filepath.Join(rt.dirs.Async, id)
	var capacity *ActiveAsyncCapacityHandle
	if limit := cfg.MaxActiveAsync; limit != nil {
		capacity, err = transferActiveAsyncCapacity(sessionID, limit, target.runID, id, asyncDir, CapacityOptions{AbandonedSlotReleaseAfterMs: cfg.AbandonedSlotRelease})
		if err != nil {
			return nil, mgmtError(err.Error())
		}
	}
	started := nowMs()
	var deadline *int64
	if timeout != nil {
		deadline = new(started + *timeout)
	}
	sessionDir := filepath.Join(asyncDir, "session")
	model := target.model
	args := bgChildArgs(a, model, sessionDir, target.sessionFile)
	depth, _ := subagentDepth(os.Getenv)
	digest := launchDigest(a, task, model)
	runCfg := RunConfig{ID: id, AsyncDir: asyncDir, ResultsDir: rt.dirs.Results, Agent: a.Name, Task: childPrompt(task), Cwd: cwd, Model: model, Thinking: thinkingOf(a), SessionID: sessionID,
		CompletionOwnerID: rt.ownerID, ToolCallID: ctx.ToolCallID(), StartedAt: started, TimeoutMs: timeout, DeadlineAt: deadline, SessionDir: sessionDir, LaunchContractDigest: digest, ResumeOf: target.runID}
	spec := launchSpec{Cfg: runCfg, Bin: pigBinary(), Args: args, Env: childEnv(os.Environ(), depth, a.Name), Agent: a, Digest: digest, Recovery: recoveryDescriptorFor(a, runCfg, model, cwd, timeout)}
	spec.Recovery.SessionFile = target.sessionFile
	h, err := startBackgroundRun(spec, rt.hooks())
	if err != nil {
		if capacity != nil {
			capacity.Rollback()
		}
		return nil, mgmtError(err.Error())
	}
	if capacity != nil {
		_ = capacity.MarkStarted(h.Config.RunnerProcessInstanceID)
	}
	rt.mu.Lock()
	rt.handles[id] = h
	if capacity != nil {
		rt.capacity[id] = capacity
	}
	rt.mu.Unlock()
	if st, _ := readStatusQuiet(asyncDir); st != nil {
		rt.mu.Lock()
		rt.state.AsyncJobs[id] = jobFromSummary(statusToSummary(asyncDir, st))
		rt.state.rememberFleetJob(rt.state.AsyncJobs[id])
		rt.mu.Unlock()
	}
	rt.refreshWidget()
	text := formatAsyncStartedMessage(fmt.Sprintf("Async: %s [%s]", a.Name, id), ctx.HasUI())
	return explicitOK{Content: fmt.Sprintf("Revived %s run %s from its persisted session as async run %s.\n\n%s", a.Name, target.runID, id, text),
		Details: map[string]any{"mode": "single", "runId": id, "results": []any{}, "asyncId": id, "asyncDir": asyncDir}}, nil
}
