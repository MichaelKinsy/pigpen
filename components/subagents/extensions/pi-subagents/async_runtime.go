package pi_subagents

// The runtime of the background runs inside the extension: configuration, launch, result delivery, the widget, the session
// hooks and the `subagent` actions status, interrupt, stop and resume, with the bg_wait tool. Ports the glue of index.ts
// (session_start, session_shutdown, agent_end, session_compact), executeAsyncSingle's result, async-job-tracker.ts (the
// widget and job restore), result-watcher.ts (delivery), inspectSubagentStatus (run-status.ts), stopAsyncRun,
// interruptAsyncRun and resumeAsyncRun.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	widgetKeyAsync        = "subagent-async"
	maxWidgetJobs         = 4
	resultScanIntervalMs  = 3000
	completionRetentionMs = 10_000
)

// bgRT is the runtime of the extension instance being built (one per Extension()).
var bgRT *asyncRuntime

func init() {
	registerRunParams("async", "timeoutMs", "maxRuntimeMs")
	registerAction("status", []string{"id", "runId", "dir"}, func(ctx sdk.Context, p map[string]any) (any, error) { return bgRT.statusAction(ctx, p) })
	registerAction("interrupt", []string{"id", "runId", "dir"}, func(ctx sdk.Context, p map[string]any) (any, error) { return bgRT.interruptAction(ctx, p) })
	registerAction("stop", []string{"id", "runId", "dir"}, func(ctx sdk.Context, p map[string]any) (any, error) { return bgRT.stopAction(ctx, p) })
	registerAction("resume", []string{"id", "runId", "dir", "message", "task"}, func(ctx sdk.Context, p map[string]any) (any, error) { return bgRT.resumeAction(ctx, p) })
	extensionHooks = append(extensionHooks, installAsyncRuntime)
}

// asyncConfig are the settings of config.json the background runs read.
type asyncConfig struct {
	AsyncByDefault       bool
	WaitTool             ResolvedWaitToolConfig
	WaitToolErr          error
	MaxActiveAsync       *int
	AbandonedSlotRelease any
	DefaultTimeoutMs     *int64
	CompletionBatch      map[string]any
	ConfigErr            error
}

func asyncConfigPath() string {
	return filepath.Join(agentDir(), "extensions", "subagent", "config.json")
}

// loadAsyncConfig reads <agent dir>/extensions/subagent/config.json (absent: defaults).
func loadAsyncConfig() asyncConfig {
	c := asyncConfig{AsyncByDefault: true}
	data, err := os.ReadFile(asyncConfigPath())
	var raw map[string]any
	if err == nil {
		if jerr := json.Unmarshal(data, &raw); jerr != nil || raw == nil {
			c.ConfigErr = fmt.Errorf("Subagent config at '%s' must be a JSON object", asyncConfigPath())
		}
	}
	if b, ok := raw["asyncByDefault"].(bool); ok && !b {
		c.AsyncByDefault = false
	}
	c.WaitTool, c.WaitToolErr = resolveWaitToolConfig(raw["waitTool"], processEnvMap())
	if n, ok := isInteger(raw["maxActiveAsyncRunsPerSession"]); ok {
		c.MaxActiveAsync = resolveMaxActiveAsyncRunsPerSession(float64(n))
	}
	if cap, ok := raw["capacity"].(map[string]any); ok {
		if v, has := cap["abandonedSlotReleaseAfterMs"]; has {
			c.AbandonedSlotRelease = v
		}
	}
	if n, ok := isInteger(raw["timeoutMs"]); ok && n > 0 && n <= 2147483647 {
		v := int64(n)
		c.DefaultTimeoutMs = &v
	}
	if cb, ok := raw["completionBatch"].(map[string]any); ok {
		c.CompletionBatch = cb
	}
	return c
}

// asyncRuntime is the per-extension state of the background runs.
type asyncRuntime struct {
	mu       sync.Mutex
	state    *SubagentState
	dirs     asyncDirs
	bus      *localBus
	ctx      sdk.Context
	hasCtx   bool
	ownerID  string
	handles  map[string]*RunHandle
	capacity map[string]*ActiveAsyncCapacityHandle
	notifier *CompletionNotifier
	scanStop chan struct{}
	// retentionTimer starts the delayed retention pass once per runtime; retentionCancel ends a pass that is running (index.ts asyncRetentionTimer/Abort).
	retentionTimer  *time.Timer
	retentionCancel chan struct{}
	closed          bool
	hasUI           bool
}

func installAsyncRuntime(e *sdk.Extension) {
	rt := &asyncRuntime{state: newSubagentState(), dirs: defaultAsyncDirs(), bus: newLocalBus(), ownerID: completionOwnerID(), handles: map[string]*RunHandle{}, capacity: map[string]*ActiveAsyncCapacityHandle{}}
	bgRT = rt
	rt.notifier = newCompletionNotifier(rt.sendNotice, NotifierOptions{BatchConfig: loadAsyncConfig().CompletionBatch,
		Owns: rt.ownsResult, CurrentSessionID: func() string { rt.mu.Lock(); defer rt.mu.Unlock(); return rt.state.CurrentSessionID }})
	cfg := loadAsyncConfig()
	e.RegisterTool(bgWaitToolDef(rt, cfg))
	e.MessageRenderer(notifyMessageType, renderNotifyMessage)
	e.OnEvent(sdk.EventSessionStart, rt.onSessionStart)
	e.OnEvent(sdk.EventSessionShutdown, rt.onSessionShutdown)
	e.OnEvent(sdk.EventAgentEnd, rt.onAgentEnd)
	e.OnEvent(sdk.EventSessionBeforeCompact, rt.onSessionBeforeCompact)
	e.OnEvent(sdk.EventSessionCompact, rt.onSessionCompact)
	e.OnEvent(sdk.EventAgentStart, rt.onAgentStart)
	e.OnEvent(sdk.EventAgentSettled, rt.onAgentSettled)
}

// completionOwnerID identifies the parent process: stable across this extension's reloads (the host is its parent), so a run
// started before a reload is still delivered by the new runtime.
func completionOwnerID() string { return fmt.Sprintf("host-%d", os.Getppid()) }

func (rt *asyncRuntime) setCtx(ctx sdk.Context) {
	rt.mu.Lock()
	rt.ctx, rt.hasCtx = ctx, true
	rt.hasUI = ctx.HasUI()
	rt.mu.Unlock()
}

func (rt *asyncRuntime) context() (sdk.Context, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.ctx, rt.hasCtx && !rt.closed
}

// currentSessionID is resolveCurrentSessionId: the session file, else the session id.
func currentSessionID(ctx sdk.Context) string {
	if f, err := ctx.GetSessionFile(); err == nil && f != nil && *f != "" {
		return *f
	}
	if id, err := ctx.GetSessionID(); err == nil {
		return id
	}
	return ""
}

func (rt *asyncRuntime) ownsResult(sessionID, ownerID string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return ownerID != "" && ownerID == rt.ownerID && sessionID == rt.state.CurrentSessionID
}

// sendNotice delivers a notification message: it wakes the session (triggerTurn) unless the result was quiet.
func (rt *asyncRuntime) sendNotice(customType, content string, display, triggerTurn bool) error {
	ctx, ok := rt.context()
	if !ok {
		return errors.New("no session to notify")
	}
	return ctx.SendCustomMessage(sdk.CustomMessage{CustomType: customType, Content: content, Display: display}, sdk.SendMessageOptions{TriggerTurn: &triggerTurn})
}

// ── session hooks ──

func (rt *asyncRuntime) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	rt.setCtx(ctx)
	rt.mu.Lock()
	rt.closed = false
	rt.state.BaseCwd = ctx.Cwd()
	rt.state.CurrentSessionID = currentSessionID(ctx)
	rt.state.AsyncJobs = map[string]*AsyncJobState{}
	rt.state.FleetJobs = map[string]*AsyncJobState{}
	rt.state.WidgetsSuspended = false // resetSessionState (index.ts:1122)
	rt.mu.Unlock()
	rt.startRetention()
	rt.restoreActiveJobs()
	rt.startResultScan()
	rt.refreshWidget()
	return nil, nil
}

// startRetention schedules the one delayed pass of the retention cleaner (startSessionMaintenance in index.ts). It never
// deletes a run this session tracks.
func (rt *asyncRuntime) startRetention() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.retentionTimer != nil {
		return
	}
	cancel := make(chan struct{})
	rt.retentionCancel = cancel
	rt.retentionTimer = time.AfterFunc(asyncRetentionDelayMs*time.Millisecond, func() {
		_, err := cleanupAsyncRetention(AsyncRetentionOptions{AsyncDirRoot: rt.dirs.Async, ResultsDir: rt.dirs.Results, Cancel: cancel,
			ProtectedRunIDs: func() []string {
				rt.mu.Lock()
				defer rt.mu.Unlock()
				return sortedKeys(rt.state.AsyncJobs)
			}})
		if err != nil {
			logAsyncError("Failed to clean retained async subagent state:", err)
		}
	})
}

func (rt *asyncRuntime) stopRetention() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.retentionTimer != nil {
		rt.retentionTimer.Stop()
		rt.retentionTimer = nil
	}
	if rt.retentionCancel != nil {
		close(rt.retentionCancel)
		rt.retentionCancel = nil
	}
}

func (rt *asyncRuntime) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	rt.mu.Lock()
	rt.closed = true
	stop := rt.scanStop
	rt.scanStop = nil
	handles := make([]*RunHandle, 0, len(rt.handles))
	for _, h := range rt.handles {
		handles = append(handles, h)
	}
	rt.handles = map[string]*RunHandle{}
	rt.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	for _, h := range handles {
		h.Detach() // the children live on; the next session adopts them
	}
	rt.stopRetention()
	rt.notifier.Dispose()
	_ = ctx.SetWidget(widgetKeyAsync, nil)
	return nil, nil
}

func (rt *asyncRuntime) onAgentEnd(ctx sdk.Context, _ map[string]any) (any, error) {
	rt.setCtx(ctx)
	if ctx.HasUI() {
		return nil, nil
	}
	err := drainOutstandingWork(AutoDrainDeps{State: rt.state, Events: rt.bus, AsyncDirRoot: rt.dirs.Async, ResultsDir: rt.dirs.Results})
	if err != nil {
		ctx.Notify(err.Error(), "error")
	}
	return nil, nil
}

// suspendWidgetsForCompaction and resumeWidgetsAfterCompaction are index.ts:980-992: the widget is cleared while a threshold or
// overflow compaction runs and drawn again when the agent starts or settles; a manual compaction leaves it alone.
func (rt *asyncRuntime) onSessionBeforeCompact(ctx sdk.Context, ev map[string]any) (any, error) {
	if reason, _ := ev["reason"].(string); reason == "manual" {
		return nil, nil
	}
	rt.setCtx(ctx)
	rt.mu.Lock()
	if rt.state.WidgetsSuspended {
		rt.mu.Unlock()
		return nil, nil
	}
	rt.state.WidgetsSuspended = true
	rt.mu.Unlock()
	if c, ok := rt.context(); ok && c.HasUI() {
		_ = c.SetWidget(widgetKeyAsync, nil)
	}
	return nil, nil
}

func (rt *asyncRuntime) resumeWidgetsAfterCompaction() {
	rt.mu.Lock()
	if !rt.state.WidgetsSuspended {
		rt.mu.Unlock()
		return
	}
	rt.state.WidgetsSuspended = false
	rt.mu.Unlock()
	rt.refreshWidget()
}

func (rt *asyncRuntime) onAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	rt.setCtx(ctx)
	rt.resumeWidgetsAfterCompaction()
	return nil, nil
}

func (rt *asyncRuntime) onAgentSettled(ctx sdk.Context, _ map[string]any) (any, error) {
	rt.setCtx(ctx)
	rt.resumeWidgetsAfterCompaction()
	return nil, nil
}

// onSessionCompact re-drives the parent after a manual compaction while background work is active (index.ts:1183-1196), through
// the session context this runtime holds: none once the session was shut down or replaced.
func (rt *asyncRuntime) onSessionCompact(_ sdk.Context, ev map[string]any) (any, error) {
	if reason, _ := ev["reason"].(string); reason != "manual" {
		return nil, nil
	}
	rt.mu.Lock()
	active := false
	for _, j := range rt.state.AsyncJobs {
		active = active || j.Status == "queued" || j.Status == "running"
	}
	hasUI := rt.hasUI
	rt.mu.Unlock()
	ctx, ok := rt.context()
	if !active || !ok || !hasUI {
		return nil, nil
	}
	yes := true
	_ = ctx.SendCustomMessage(sdk.CustomMessage{CustomType: "subagent-compaction-resume", Display: false,
		Content: "Compaction is complete. Resume the parent task now; background subagent results will arrive separately when ready."}, sdk.SendMessageOptions{TriggerTurn: &yes})
	return nil, nil
}

// ── jobs, adoption and the widget ──

func jobFromSummary(r AsyncRunSummary) *AsyncJobState {
	j := &AsyncJobState{AsyncID: r.ID, AsyncDir: r.AsyncDir, ToolCallID: r.ToolCallID, Cwd: r.Cwd, SessionRoot: r.SessionRoot, Status: r.State, SessionID: r.SessionID,
		ActivityState: r.ActivityState, LastActivityAt: r.LastActivityAt, CurrentTool: r.CurrentTool, CurrentToolAt: r.CurrentToolAt, CurrentPath: r.CurrentPath,
		TurnCount: r.TurnCount, ToolCount: r.ToolCount, Mode: r.Mode, CurrentStep: r.CurrentStep, Steps: r.Steps, StartedAt: r.StartedAt, UpdatedAt: r.StartedAt,
		TimeoutMs: r.TimeoutMs, DeadlineAt: r.DeadlineAt, SessionFile: r.SessionFile, OutputFile: r.OutputFile, SessionDir: r.SessionDir, TotalTokens: r.TotalTokens}
	if r.LastUpdate != nil {
		j.UpdatedAt = int64(*r.LastUpdate)
	}
	for _, s := range r.Steps {
		j.Agents = append(j.Agents, s.Agent)
	}
	return j
}

// restoreActiveJobs lists the session's active runs, tracks them and adopts those no live supervisor owns.
func (rt *asyncRuntime) restoreActiveJobs() {
	rt.mu.Lock()
	sessionID := rt.state.CurrentSessionID
	rt.mu.Unlock()
	if sessionID == "" {
		return
	}
	runs, err := listAsyncRuns(rt.dirs.Async, AsyncRunListOptions{States: activeWaitStates, SessionID: sessionID, ResultsDir: rt.dirs.Results})
	if err != nil {
		return
	}
	for _, r := range runs {
		rt.mu.Lock()
		rt.state.AsyncJobs[r.ID] = jobFromSummary(r)
		rt.state.rememberFleetJob(rt.state.AsyncJobs[r.ID])
		_, owned := rt.handles[r.ID]
		rt.mu.Unlock()
		if owned || !backgroundSupported {
			continue
		}
		cfg, cerr := readRunConfig(r.AsyncDir)
		st, _ := readStatusQuiet(r.AsyncDir)
		if cerr != nil || st == nil || st.PID == nil {
			continue // a run of another runner (the original's, say) is tracked, not supervised
		}
		h := adoptRun(cfg, *st.PID, rt.hooks())
		rt.mu.Lock()
		rt.handles[r.ID] = h
		rt.mu.Unlock()
	}
}

func (rt *asyncRuntime) hooks() runHooks {
	return runHooks{OnProgress: func(dir string) { rt.refreshJob(dir); rt.refreshWidget() }, OnComplete: rt.onRunComplete, OnProcessTerminal: rt.onProcessTerminal}
}

func (rt *asyncRuntime) refreshJob(asyncDir string) {
	st, _ := readStatusQuiet(asyncDir)
	if st == nil {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if j := rt.state.AsyncJobs[st.RunID]; j != nil {
		sum := statusToSummary(asyncDir, st)
		*j = *jobFromSummary(sum)
		rt.state.rememberFleetJob(j)
	}
}

// refreshWidget draws the running jobs (the original's renderWidget); no jobs clears it.
func (rt *asyncRuntime) refreshWidget() {
	ctx, ok := rt.context()
	if !ok || !ctx.HasUI() {
		return
	}
	rt.mu.Lock()
	suspended := rt.state.WidgetsSuspended
	var jobs []*AsyncJobState
	for _, id := range sortedKeys(rt.state.AsyncJobs) {
		jobs = append(jobs, rt.state.AsyncJobs[id])
	}
	rt.mu.Unlock()
	if suspended {
		return
	}
	if ctx.Mode() == "rpc" && len(jobs) > 0 {
		// renderWidget (render.ts): an RPC host reads the versioned snapshot, not terminal lines
		_ = ctx.SetWidget(widgetKeyAsync, encodeAsyncStatusSnapshotWidget(jobs, SnapshotOptions{}))
		return
	}
	lines := widgetLines(jobs)
	if len(lines) == 0 {
		_ = ctx.SetWidget(widgetKeyAsync, nil)
		return
	}
	_ = ctx.SetWidget(widgetKeyAsync, lines)
}

func widgetGlyph(status string) string {
	switch status {
	case "running", "queued":
		return "●"
	case "complete":
		return "✓"
	case "paused", "stopped":
		return "■"
	}
	return "✗"
}

// widgetLines is the text of the async widget: a header and up to four jobs with their activity.
func widgetLines(jobs []*AsyncJobState) []string {
	if len(jobs) == 0 {
		return nil
	}
	running := 0
	for _, j := range jobs {
		if isActiveAsyncState(j.Status) {
			running++
		}
	}
	lines := []string{fmt.Sprintf("Async subagents · %d running", running)}
	for i, j := range jobs {
		if i >= maxWidgetJobs {
			lines = append(lines, fmt.Sprintf("… +%d more", len(jobs)-maxWidgetJobs))
			break
		}
		agent := strings.Join(j.Agents, ", ")
		line := fmt.Sprintf("%s %s · %s · %s", widgetGlyph(j.Status), agent, j.Status, bgFormatDuration(max(0, nowMs()-j.StartedAt)))
		if a := activityFactsOf(j.ActivityState, j.LastActivityAt, j.CurrentTool, j.CurrentToolAt, j.CurrentPath, j.TurnCount, j.ToolCount); a != "" && isActiveAsyncState(j.Status) {
			line += " · " + a
		}
		lines = append(lines, line)
	}
	return lines
}

// staleExtensionContext is extension-context.ts isStaleExtensionContextError's message test.
var staleExtensionContext = regexp.MustCompile(`(?i)extension ctx is stale|extension context no longer active|stale after session replacement or reload`)

// logAsyncError reports a failure the background code survives (the original's console.error).
var logAsyncError = func(args ...any) { fmt.Fprintln(os.Stderr, args...) }

// emitProcessTerminalEvent publishes the process-terminal proof (async-execution.ts emitProcessTerminalEvent): a stale context
// drops it silently, any other failure is logged, none throws.
func emitProcessTerminalEvent(emit func(channel string, payload any) error, proof any) {
	if err := emit(subagentProcessTerminalEvt, proof); err != nil && !staleExtensionContext.MatchString(err.Error()) {
		logAsyncError("Failed to emit subagent process-terminal event:", err)
	}
}

func (rt *asyncRuntime) onProcessTerminal(proof *ProcessTerminal) {
	ctx, ok := rt.context()
	if !ok {
		return
	}
	emitProcessTerminalEvent(ctx.Events().Emit, proof)
}

// ── results: delivery of what finished ──

func (rt *asyncRuntime) onRunComplete(cfg *RunConfig, result map[string]any, st *AsyncStatus) {
	rt.mu.Lock()
	delete(rt.handles, cfg.ID)
	if j := rt.state.AsyncJobs[cfg.ID]; j != nil && st != nil {
		*j = *jobFromSummary(statusToSummary(cfg.AsyncDir, st))
		rt.state.rememberFleetJob(j)
	}
	cap := rt.capacity[cfg.ID]
	delete(rt.capacity, cfg.ID)
	rt.mu.Unlock()
	if cap != nil {
		cap.Reconcile(nil)
	}
	rt.refreshWidget()
	go func() {
		time.Sleep(completionRetentionMs * time.Millisecond)
		rt.mu.Lock()
		if j := rt.state.AsyncJobs[cfg.ID]; j != nil && !isActiveAsyncState(j.Status) {
			delete(rt.state.AsyncJobs, cfg.ID)
		}
		rt.mu.Unlock()
		rt.refreshWidget()
	}()
	if cfg.SessionID != "" {
		rt.processResult(resultFileName(cfg.ID), true)
	}
	rt.bus.Emit(subagentAsyncCompleteEvent, result)
	if ctx, ok := rt.context(); ok {
		_ = ctx.Events().Emit(subagentAsyncCompleteEvent, result)
	}
}

// processResult delivers one result file of the session: it records the completion for bg_wait, sends the notification, and
// removes the file once the session accepted it.
func (rt *asyncRuntime) processResult(file string, triggerTurn bool) {
	rt.mu.Lock()
	sessionID := rt.state.CurrentSessionID
	rt.mu.Unlock()
	if sessionID == "" || file != filepath.Base(file) || !strings.HasSuffix(file, ".json") {
		return
	}
	runID := strings.TrimSuffix(file, ".json")
	path, err := resultPayloadPathForSessionRun(rt.dirs.Results, sessionID, runID)
	if err != nil || path == "" {
		return
	}
	if promoted := promotePendingResultFile(rt.dirs.Results, sessionID, runID, file); promoted == "promoted" {
		path = resultFilePath(rt.dirs.Results, runID)
	}
	var data map[string]any
	if err := readJSONFile(path, &data); err != nil || data == nil {
		return
	}
	if nonEmptyString(data["sessionId"]) != sessionID || !rt.ownsResult(sessionID, nonEmptyString(data["completionOwnerId"])) {
		return
	}
	if _, delivered := data["notificationDeliveredAt"].(float64); delivered {
		removeDeliveredResult(rt.dirs.Results, file, sessionID, runID, nonEmptyString(data["toolCallId"]))
		return
	}
	rt.mu.Lock()
	recordWaitCompletion(rt.state, runID, data, nowMs(), completionSendTTLMs, &struct{ ResultsDir, SessionID string }{rt.dirs.Results, sessionID})
	rt.mu.Unlock()
	rt.bus.Emit(subagentAsyncCompleteEvent, data)
	data["triggerTurn"] = triggerTurn
	go func() {
		if accepted := <-rt.notifier.Deliver(data); accepted {
			removeDeliveredResult(rt.dirs.Results, file, sessionID, runID, nonEmptyString(data["toolCallId"]))
		}
	}()
}

func removeDeliveredResult(resultsDir, file, sessionID, runID, toolCallID string) {
	_ = os.Remove(filepath.Join(resultsDir, file))
	removeResultIndex(resultsDir, sessionID, runID, toolCallID)
}

// startResultScan delivers the results the session has not seen (runs that ended while no one was watching) and keeps
// scanning: a crashed supervisor or a run another process finished is still delivered.
func (rt *asyncRuntime) startResultScan() {
	rt.mu.Lock()
	if rt.scanStop != nil {
		rt.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	rt.scanStop = stop
	rt.mu.Unlock()
	scan := func(trigger bool) {
		rt.mu.Lock()
		sessionID := rt.state.CurrentSessionID
		rt.mu.Unlock()
		if sessionID == "" {
			return
		}
		for _, f := range resultCandidateFilesForSession(rt.dirs.Results, sessionID) {
			rt.processResult(f, trigger)
		}
	}
	scan(true)
	go func() {
		t := time.NewTicker(resultScanIntervalMs * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				rt.restoreOrphanedRuns()
				scan(true)
			}
		}
	}()
}

// restoreOrphanedRuns repairs the runs of this session whose runner is gone and that no supervisor watches.
func (rt *asyncRuntime) restoreOrphanedRuns() {
	rt.mu.Lock()
	sessionID := rt.state.CurrentSessionID
	rt.mu.Unlock()
	if sessionID == "" {
		return
	}
	_, _ = listAsyncRuns(rt.dirs.Async, AsyncRunListOptions{States: activeWaitStates, SessionID: sessionID, ResultsDir: rt.dirs.Results})
}

// ── the tool-call result of a launch ──

// formatAsyncStartedMessage is the guidance a launch returns.
func formatAsyncStartedMessage(headline string, interactive bool) string {
	var guidance []string
	if interactive {
		guidance = []string{
			"The async run is detached and running in the background.",
			"You are in an interactive session. Return control to the user now; Pi will wake you through the native completion notification when this subagent completes or needs attention. Do not run sleep/polling loops to wait for this async subagent; it does not need a wait call.",
			"Use bg_wait only for provider, detached, or other background work that lacks a native completion notification.",
			"If the current turn must receive results from work without a native notification before it ends, call blocking bg_wait(); ordinary async subagent runs do not need a wait call because their completion is delivered natively.",
			"Otherwise, continue any independent work or return control to the user. Use subagent({ action: \"status\", id: \"...\" }) for a one-shot status/result or to inspect a blocked/stale run, never as a wait loop.",
		}
	} else {
		guidance = []string{
			"The async run is detached. Do not run sleep timers or polling loops just to wait for it.",
			"This is a non-interactive run: Pi auto-drains current-session subagent work at agent_end so detached children are not abandoned. Use bg_wait only when this turn must receive provider, detached, or other background-work results that have no native completion notification.",
			"Use subagent({ action: \"status\", id: \"...\" }) when you need a one-shot status/result or to inspect a blocked/stale run; do not poll in a loop.",
		}
	}
	return strings.Join(append([]string{headline, ""}, guidance...), "\n")
}

// timeoutParams is resolveForegroundTimeout: timeoutMs and its alias maxRuntimeMs.
func timeoutParams(params map[string]any, def *int64) (*int64, error) {
	raw, rawMax := params["timeoutMs"], params["maxRuntimeMs"]
	if raw == nil && rawMax == nil {
		return def, nil
	}
	for _, p := range []struct {
		name string
		v    any
	}{{"timeoutMs", raw}, {"maxRuntimeMs", rawMax}} {
		if p.v == nil {
			continue
		}
		f, ok := p.v.(float64)
		if !ok || f != float64(int64(f)) || f <= 0 {
			return nil, fmt.Errorf("%s must be a positive integer.", p.name)
		}
		if f > 2147483647 {
			return nil, fmt.Errorf("%s must be a positive integer no larger than 2147483647.", p.name)
		}
	}
	if raw != nil && rawMax != nil && raw != rawMax {
		return nil, errors.New("timeoutMs and maxRuntimeMs are aliases; provide only one value or use the same value for both.")
	}
	v := raw
	if v == nil {
		v = rawMax
	}
	n := int64(v.(float64))
	return &n, nil
}

// wantsAsync is the original's requestedAsync: an explicit async wins, else asyncByDefault (true unless config says false).
func wantsAsync(params map[string]any, cfg asyncConfig) bool {
	if b, ok := params["async"].(bool); ok {
		return b
	}
	return cfg.AsyncByDefault
}

// launchBackground starts a background single-agent run and returns the tool result of the launch.
func (rt *asyncRuntime) launchBackground(ctx sdk.Context, params map[string]any, a *AgentConfig, task, cwd, model string, cfg asyncConfig) (any, error) {
	if cfg.ConfigErr != nil {
		return nil, sdk.NewToolError(cfg.ConfigErr.Error())
	}
	rt.setCtx(ctx)
	sessionID := currentSessionID(ctx)
	rt.mu.Lock()
	rt.closed = false
	if rt.state.CurrentSessionID == "" {
		rt.state.CurrentSessionID = sessionID
	}
	rt.mu.Unlock()
	timeout, err := timeoutParams(params, firstInt64(cfg.DefaultTimeoutMs, asyncDefaultTimeoutMs))
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}
	id := randomUUID()
	asyncDir := filepath.Join(rt.dirs.Async, id)
	var capacity *ActiveAsyncCapacityHandle
	if limit := cfg.MaxActiveAsync; limit != nil {
		capacity, err = acquireActiveAsyncCapacity(sessionID, limit, id, "runner", asyncDir, CapacityOptions{AbandonedSlotReleaseAfterMs: cfg.AbandonedSlotRelease})
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
	}
	started := nowMs()
	var deadline *int64
	if timeout != nil {
		deadline = new(started + *timeout)
	}
	sessionDir := filepath.Join(asyncDir, "session")
	args := bgChildArgs(a, model, sessionDir, "")
	depth, _ := subagentDepth(os.Getenv)
	digest := launchDigest(a, task, model)
	runCfg := RunConfig{ID: id, AsyncDir: asyncDir, ResultsDir: rt.dirs.Results, Agent: a.Name, Task: childPrompt(task), Cwd: cwd, Model: model, Thinking: thinkingOf(a), SessionID: sessionID,
		CompletionOwnerID: rt.ownerID, ToolCallID: ctx.ToolCallID(), StartedAt: started, TimeoutMs: timeout, DeadlineAt: deadline, SessionDir: sessionDir, LaunchContractDigest: digest}
	spec := launchSpec{Cfg: runCfg, Bin: pigBinary(), Args: args, Env: childEnv(os.Environ(), depth, a.Name), Agent: a, Digest: digest, Recovery: recoveryDescriptorFor(a, runCfg, model, cwd, timeout)}
	h, err := startBackgroundRun(spec, rt.hooks())
	if err != nil {
		if capacity != nil {
			capacity.Rollback()
		}
		return nil, sdk.NewToolError(err.Error())
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
	rt.refreshJob(asyncDir)
	if st, _ := readStatusQuiet(asyncDir); st != nil {
		rt.mu.Lock()
		rt.state.AsyncJobs[id] = jobFromSummary(statusToSummary(asyncDir, st))
		rt.state.rememberFleetJob(rt.state.AsyncJobs[id])
		rt.mu.Unlock()
	}
	rt.refreshWidget()
	started2 := map[string]any{"lifecycleArtifactVersion": lifecycleArtifactVersion, "id": id, "pid": 0, "sessionId": sessionID, "completionOwnerId": rt.ownerID, "mode": "single", "agent": a.Name,
		"cwd": cwd, "asyncDir": asyncDir, "launchContractDigest": digest}
	if jobTask := jsTrim(task); jobTask != "" {
		started2["task"], started2["goal"] = promptRedacted, promptRedacted
	}
	if st, _ := readStatusQuiet(asyncDir); st != nil && st.PID != nil {
		started2["pid"] = *st.PID
	}
	if timeout != nil {
		started2["timeoutMs"], started2["deadlineAt"] = *timeout, *deadline
	}
	rt.bus.Emit(subagentAsyncStartedEvent, started2)
	_ = ctx.Events().Emit(subagentAsyncStartedEvent, started2)
	details := map[string]any{"mode": "single", "runId": id, "results": []any{}, "asyncId": id, "asyncDir": asyncDir, "launchContractDigest": digest}
	if timeout != nil {
		details["timeoutMs"], details["deadlineAt"] = *timeout, *deadline
	}
	return explicitOK{Content: formatAsyncStartedMessage(fmt.Sprintf("Async: %s [%s]", a.Name, id), ctx.HasUI()), Details: details}, nil
}

func firstInt64(p *int64, def int64) *int64 {
	if p != nil {
		return p
	}
	return &def
}

func thinkingOf(a *AgentConfig) string {
	switch t := a.Thinking.(type) {
	case string:
		return t
	case bool:
		if !t {
			return "off"
		}
	}
	return ""
}

// bgChildArgs is the argument list of a background child: the foreground one with JSON output (the events the supervisor
// reads) in place of print mode, and the session kept in sessionDir so the child can be revived.
func bgChildArgs(a *AgentConfig, model, sessionDir, sessionFile string) []string {
	base := buildChildArgs(a, model)
	var args []string
	for _, x := range base {
		if x == "--print" {
			args = append(args, "--mode", "json")
			continue
		}
		args = append(args, x)
	}
	if sessionFile != "" {
		args = append(args, "--session", sessionFile)
	}
	return append(args, "--session-dir", sessionDir)
}

func launchDigest(a *AgentConfig, task, model string) string {
	b, _ := json.Marshal(map[string]any{"agent": a.Name, "task": task, "model": model, "tools": a.Tools, "systemPrompt": a.SystemPrompt, "systemPromptMode": a.SystemPromptMode, "thinking": a.Thinking})
	return shortDigest(b)
}

func recoveryDescriptorFor(a *AgentConfig, cfg RunConfig, model, cwd string, timeout *int64) *RecoveryDescriptor {
	d := &RecoveryDescriptor{Version: 1, LaunchContractDigest: cfg.LaunchContractDigest, SourceRunID: cfg.ID, Agent: a.Name, Cwd: cwd, Model: model, Thinking: cfg.Thinking, Tools: a.Tools,
		SystemPrompt: a.SystemPrompt, SystemPromptMode: firstNonEmpty(a.SystemPromptMode, "replace"), InheritProjectContext: a.InheritProjectContext, InheritGlobalContext: a.InheritGlobalContext,
		InheritSkills: a.InheritSkills, Skills: a.Skills, AgentFilePath: a.FilePath, OutputMode: "inline", SessionDir: cfg.SessionDir, MaxSubagentDepth: defaultMaxDepth}
	if model != "" {
		d.ModelOrigin = "configured"
	}
	if cfg.DeadlineAt != nil {
		d.AbsoluteDeadlineAt = msPtrOf(*cfg.DeadlineAt)
	}
	return d
}
