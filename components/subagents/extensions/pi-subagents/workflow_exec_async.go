package pi_subagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The async half of the workflow executor (subagent-executor.ts:5421-6250): the call returns at once with the run id; the script
// runs on a goroutine of this process, and its state is kept in the async directory so a later session, `status`, `bg_wait` and
// the completion notice find it like any background run.

func (s *wfStatus) setFanout(snap runFanoutBudgetSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	setExtra(&s.st.Extra, "runFanoutBudget", jo("used", float64(snap.Used), "limit", float64(snap.Limit), "remaining", float64(snap.Remaining)))
	_ = s.persistLocked(false)
}

func (x *wfExec) fanoutSnapshotRaw() any {
	return x.fanoutSnapshotObject()
}

func preview1000(s string) (string, bool) {
	u := jsUTF16(s)
	if len(u) <= 1000 {
		return s, false
	}
	return string(utf16Decode(u[:1000])) + "…", true
}

func (x *wfExec) liveWorkflowIDs() map[string]bool {
	x.state.Mu.Lock()
	defer x.state.Mu.Unlock()
	live := map[string]bool{}
	for id := range x.state.WorkflowControllers {
		live[id] = true
	}
	return live
}

// runAsync starts the workflow in the background and answers with its id.
func (x *wfExec) runAsync(p map[string]any) (any, error) {
	x.bindRun()
	rt := bgRT
	if rt == nil {
		return nil, sdk.NewToolError("Background runs are unavailable in this host.")
	}
	rt.setCtx(x.ctx)
	cfg := loadAsyncConfig()
	if cfg.ConfigErr != nil {
		return nil, sdk.NewToolError(cfg.ConfigErr.Error())
	}
	rt.mu.Lock()
	rt.closed = false
	if rt.state.CurrentSessionID == "" {
		rt.state.CurrentSessionID = x.sessionID
	}
	rt.mu.Unlock()
	runID := x.workflowRunID
	asyncDir := filepath.Join(rt.dirs.Async, runID)
	depth, _ := subagentDepth(os.Getenv)
	var capacity *ActiveAsyncCapacityHandle
	if depth == 0 && cfg.MaxActiveAsync != nil {
		var err error
		capacity, err = acquireActiveAsyncCapacity(x.sessionID, cfg.MaxActiveAsync, runID, "workflow", asyncDir, CapacityOptions{AbandonedSlotReleaseAfterMs: cfg.AbandonedSlotRelease, LiveWorkflowRunIDs: x.liveWorkflowIDs()})
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
	}
	rollback := func() {
		if capacity != nil {
			capacity.Rollback()
		}
	}
	if err := os.MkdirAll(asyncDir, 0o755); err != nil {
		rollback()
		return nil, sdk.NewToolError("Failed to create async workflow storage: " + err.Error())
	}
	if err := writeRunFanoutBudgetDescriptor(asyncDir, x.fanout); err != nil {
		rollback()
		return nil, sdk.NewToolError("Failed to create async workflow storage: " + err.Error())
	}
	if err := os.MkdirAll(rt.dirs.Results, 0o755); err != nil {
		rollback()
		return nil, sdk.NewToolError("Failed to create async workflow storage: " + err.Error())
	}

	runCtx, cancel := context.WithCancelCause(context.Background())
	x.state.Mu.Lock()
	if x.state.WorkflowControllers == nil {
		x.state.WorkflowControllers = map[string]context.CancelCauseFunc{}
	}
	if x.state.WorkflowChildStops == nil {
		x.state.WorkflowChildStops = map[string]func(key, message string) bool{}
	}
	x.state.WorkflowControllers[runID] = cancel
	x.state.Mu.Unlock()
	if capacity != nil {
		_ = capacity.MarkWorkflowStarted()
	}

	started := ms(nowMs())
	pid := os.Getpid()
	owner := rt.ownerID
	x.state.CompletionOwnerID = owner
	st := AsyncStatus{LifecycleArtifactVersion: lifecycleArtifactVersion, RunID: runID, CompletionOwnerID: owner, ToolCallID: x.toolCallID, SessionID: x.sessionID, Mode: "workflow", State: "running",
		StartedAt: started, LastUpdate: &started, Cwd: x.workflowCwd, PID: &pid, PidNamespaceScope: readPidNamespaceScope(), Steps: []AsyncStep{}}
	if x.toolCallID == runID {
		st.ToolCallID = ""
	}
	if x.timeoutMs != nil {
		t := ms(*x.timeoutMs)
		d := started + t
		st.TimeoutMs, st.DeadlineAt = &t, &d
	}
	if root, ok := p["sessionDir"].(string); ok && root != "" {
		st.SessionRoot = nodeResolve(root)
	}
	workflow := jo("trace", []any{}, "emits", []any{}, "console", []any{})
	if x.args != nil {
		workflow.set("args", x.args)
		workflow.set("argsDigest", x.argsDigest)
	}
	if x.resource != nil {
		workflow.set("resource", x.resource.provenanceObject())
	}
	if x.preflight != nil {
		setExtra(&st.Extra, "preflight", x.preflight)
	}
	if x.scheduleOriginObject() != nil {
		setExtra(&st.Extra, "scheduleOrigin", x.scheduleOriginObject())
	}
	ws := &wfStatus{x: x, asyncDir: asyncDir, resultDir: rt.dirs.Results, st: st, steps: map[string]*AsyncStep{}, workflow: workflow}
	x.st = ws
	if snap, err := getRunFanoutBudgetSnapshot(x.fanout); err == nil {
		setExtra(&ws.st.Extra, "runFanoutBudget", jo("used", float64(snap.Used), "limit", float64(snap.Limit), "remaining", float64(snap.Remaining)))
	}
	abort := func() {
		x.state.Mu.Lock()
		delete(x.state.WorkflowControllers, runID)
		x.state.Mu.Unlock()
		rollback()
	}
	ws.mu.Lock()
	err := ws.persistLocked(true)
	ws.mu.Unlock()
	if err != nil {
		abort()
		return nil, sdk.NewToolError("Failed to create async workflow storage: " + err.Error())
	}
	ws.indexed = ws.st.State
	if err := updateActiveRunIndex(asyncDir, ws.st.State, ws.st.ToolCallID, indexOptions{retryCapacityErrors: true}); err != nil {
		logAsyncError("Failed to update async workflow index:", err)
	}
	rt.mu.Lock()
	job := jobFromSummary(statusToSummary(asyncDir, &ws.st))
	rt.state.AsyncJobs[runID] = job
	rt.state.rememberFleetJob(job)
	rt.mu.Unlock()
	rt.refreshWidget()
	ws.appendEvent(newObjectWith("type", "subagent.workflow.started", x.argsDigest != "", "argsDigest", x.argsDigest))
	started2 := map[string]any{"lifecycleArtifactVersion": lifecycleArtifactVersion, "id": runID, "asyncDir": asyncDir, "cwd": x.workflowCwd, "pid": pid, "sessionId": x.sessionID, "completionOwnerId": owner, "mode": "workflow", "agent": "workflow", "goal": promptRedacted}
	if x.timeoutMs != nil {
		started2["timeoutMs"] = int64(*x.timeoutMs)
		started2["deadlineAt"] = int64(started) + int64(*x.timeoutMs)
	}
	rt.bus.Emit(subagentAsyncStartedEvent, started2)
	_ = x.ctx.Events().Emit(subagentAsyncStartedEvent, started2)

	stopWatch := make(chan struct{})
	go x.watchStopRequests(asyncDir, cancel, stopWatch)
	go x.runAsyncScript(runCtx, cancel, capacity, stopWatch)

	text := formatAsyncStartedMessage(fmt.Sprintf("Async workflow [%s]", runID), x.ctx.HasUI())
	if x.preflight != nil {
		text = FormatWorkflowPreflight(x.preflight, "") + "\n\n" + text
	}
	details := jo("mode", "workflow", "runId", runID, "toolCallId", x.toolCallID, "asyncId", runID, "asyncDir", asyncDir, "results", []any{})
	if x.preflight != nil {
		details.set("preflight", jsAny(x.preflight))
	}
	details.set("workflow", ws.workflow)
	ws.mu.Lock()
	if raw, ok := ws.st.Extra["workflowChildren"]; ok {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			details.set("workflowChildren", toDyn(v))
		}
	}
	ws.mu.Unlock()
	details.set("chatProgress", jsAny(x.chatProgress))
	details.set("runFanoutBudget", x.fanoutSnapshotObject())
	text, isErr := x.attachMission(text, false, runID, runID, asyncDir, details)
	return resultOf(text, isErr, details)
}

func newObjectWith(k, v any, cond bool, k2 string, v2 any) *jsObject {
	o := jo(k, v)
	if cond {
		o.set(k2, v2)
	}
	return o
}

func (x *wfExec) scheduleOriginObject() *jsObject {
	if x.scheduleOrigin == nil {
		return nil
	}
	o, _ := asObject(toDyn(x.scheduleOrigin))
	return o
}

const workflowStopMessage = "Subagent stopped by user."

// watchStopRequests consumes the stop requests of the run's control inbox (`subagent {action: "stop"}` writes them): a request
// for the run stops the workflow, one for a child stops that child.
func (x *wfExec) watchStopRequests(asyncDir string, cancel context.CancelCauseFunc, done <-chan struct{}) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
		}
		entries, err := os.ReadDir(stopRequestsDir(asyncDir))
		if err != nil {
			continue
		}
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			path := filepath.Join(stopRequestsDir(asyncDir), n)
			var req controlRequest
			if readJSONFile(path, &req) != nil || os.Remove(path) != nil {
				continue
			}
			key := ""
			if req.ChildID != "" || req.TargetIndex != nil {
				key = x.keyOfChild(req.ChildID, req.TargetIndex)
			}
			if key == "" && (req.ChildID != "" || req.TargetIndex != nil) {
				continue
			}
			if key != "" {
				x.state.Mu.Lock()
				stop := x.state.WorkflowChildStops[x.workflowRunID]
				x.state.Mu.Unlock()
				if stop != nil {
					stop(key, fmt.Sprintf("Workflow child '%s' stopped by user.", key))
				}
				continue
			}
			cancel(errors.New(workflowStopMessage))
			return
		}
	}
}

func (x *wfExec) keyOfChild(childID string, index *int) string {
	x.st.mu.Lock()
	defer x.st.mu.Unlock()
	for i := range x.st.st.Steps {
		s := &x.st.st.Steps[i]
		if s.WorkflowKey == "" {
			continue
		}
		if (index != nil && *index == i) || (childID != "" && (childID == s.WorkflowKey || childID == s.RunID || childID == s.ChildID)) {
			return s.WorkflowKey
		}
	}
	return ""
}

func (x *wfExec) onChildSettled(n WorkflowChildSettledNotification) {
	ws := x.st
	ev := jo("type", "subagent.workflow.child_settled", "childKey", n.ChildKey)
	if n.ChildRunID != "" {
		ev.set("childRunId", n.ChildRunID)
	}
	ev.set("outcome", n.Outcome)
	ev.set("workflowRunning", n.WorkflowRunning)
	ws.appendEvent(ev)
	rt := bgRT
	if rt == nil {
		return
	}
	c := incrementalChild{Outcome: n.Outcome, ChildKey: n.ChildKey, WorkflowRunID: n.WorkflowRunID, ChildRunID: n.ChildRunID, OutputReference: n.OutputReference, Error: n.Error, WorkflowRunning: n.WorkflowRunning}
	quiet := false
	if o := x.scheduleOriginObject(); o != nil {
		quiet = objBool(o, "quiet")
	}
	if err := rt.sendNotice("subagent-incremental-child-notify", formatIncrementalChildCompletion(c), n.Outcome != "completed", incrementalChildCompletionTriggersTurn(c, quiet)); err != nil {
		logAsyncError(fmt.Sprintf("Failed to send incremental child completion notification for '%s':", n.ChildKey), err)
	}
}

// runAsyncScript runs the script and publishes its end.
func (x *wfExec) runAsyncScript(ctx context.Context, cancel context.CancelCauseFunc, capacity *ActiveAsyncCapacityHandle, stopWatch chan struct{}) {
	ws := x.st
	runID := x.workflowRunID
	defer func() {
		close(stopWatch)
		cancel(nil)
		x.state.Mu.Lock()
		delete(x.state.WorkflowControllers, runID)
		delete(x.state.WorkflowChildStops, runID)
		x.state.Mu.Unlock()
		if capacity != nil {
			capacity.Reconcile(x.liveWorkflowIDs())
		}
	}()
	opt := x.scriptOptions(ctx)
	opt.OnTrace = ws.updateTrace
	opt.OnLanePlan = ws.lanePlan
	opt.OnHostStep = ws.hostStep
	opt.OnEmit = func(emits []any) error { ws.emits(emits); return nil }
	opt.OnChildSettled = x.onChildSettled
	opt.RegisterStopChild = func(stop func(key, message string) bool) {
		x.state.Mu.Lock()
		defer x.state.Mu.Unlock()
		if stop != nil {
			x.state.WorkflowChildStops[runID] = stop
		} else {
			delete(x.state.WorkflowChildStops, runID)
		}
	}
	opt.ContinueAfterAbortWhenChildrenSettled = func(abortErr error) bool {
		if abortErr.Error() != "Workflow stopped because the extension session was replaced or reloaded." {
			return false
		}
		x.state.Mu.Lock()
		defer x.state.Mu.Unlock()
		for _, c := range x.state.ForegroundControls {
			if c.ParentWorkflowRunID == runID && c.LiveChildCount() > 0 {
				return false
			}
		}
		return true
	}
	res, err := RunWorkflowScript(opt)
	if err != nil {
		x.publishFailure(ctx, err)
		return
	}
	x.publishSuccess(res)
}

func (x *wfExec) publishResult(payload map[string]any, state string) bool {
	rt := bgRT
	ws := x.st
	path := resultFilePath(rt.dirs.Results, x.workflowRunID)
	if _, err := writeAsyncResultFile(path, payload); err != nil {
		msg := fmt.Sprintf("Failed to write async workflow result %s: %s", path, err.Error())
		logAsyncError(msg, err)
		ws.appendEvent(newObjectWith("type", "subagent.workflow.result_write_failed", true, "error", msg))
		_ = x.ctx.SendMessage("subagent-workflow-result-write-failed", msg, true, sdk.SendMessageOptions{TriggerTurn: new(true)})
		return false
	}
	return true
}

func (x *wfExec) finalizeStatus(state string, mutate func(st *AsyncStatus), workflow *jsObject, summary WorkflowChildSummary, receiptPath string) (AsyncStatus, error) {
	ws := x.st
	ws.mu.Lock()
	defer ws.mu.Unlock()
	end := ms(nowMs())
	ws.st.State = state
	ws.st.EndedAt = &end
	ws.st.ActivityState = ""
	ws.workflow = workflow
	if mutate != nil {
		mutate(&ws.st)
	}
	setExtra(&ws.st.Extra, "workflowChildren", summary)
	if receiptPath != "" {
		setExtra(&ws.st.Extra, "workflowReceiptPath", receiptPath)
	} else {
		delExtra(&ws.st.Extra, "workflowReceiptPath")
	}
	setExtra(&ws.st.Extra, "workflow", ws.workflow)
	if ws.graph != nil {
		setExtra(&ws.st.Extra, "workflowGraph", ws.graph)
	}
	now := ms(nowMs())
	ws.st.LastUpdate = &now
	return ws.st, nil
}

func (x *wfExec) writeFinalStatus() error {
	ws := x.st
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if err := writeStatus(ws.asyncDir, &ws.st); err != nil {
		return err
	}
	if err := updateActiveRunIndex(ws.asyncDir, ws.st.State, ws.st.ToolCallID, indexOptions{retryCapacityErrors: true, terminalIndexBeforeRelease: true}); err != nil {
		logAsyncError("Failed to update async workflow index:", err)
	}
	ws.closed = true
	return nil
}

// deliver publishes the end of the run to the session.
func (x *wfExec) deliver(payload map[string]any) {
	rt := bgRT
	if x.mission != nil {
		if ev, ok := jsAny(payload).(*jsObject); ok {
			if _, err := syncMissionFromAsyncCompletion(ev); err != nil {
				logAsyncError("Failed to sync the mission of workflow "+x.workflowRunID+":", err)
			}
		}
	}
	st := x.st.st
	cfg := &RunConfig{ID: x.workflowRunID, AsyncDir: x.st.asyncDir, ResultsDir: rt.dirs.Results, SessionID: x.sessionID, CompletionOwnerID: rt.ownerID}
	rt.onRunComplete(cfg, payload, &st)
}

func (x *wfExec) writeReceipt(state string, children []*jsObject, summary WorkflowChildSummary, outcome *jsObject) (string, *jsObject) {
	receipt, err := BuildWorkflowReceipt(WorkflowReceiptInput{WorkflowRunID: x.workflowRunID, State: state, Children: children, WorkflowChildren: &summary, TerminalOutcome: outcome, HostSteps: x.graphHostSteps(), Resource: x.resourceObject(), ArgsDigest: x.argsDigest})
	if err != nil {
		x.st.appendEvent(newObjectWith("type", "subagent.workflow.receipt_write_failed", true, "error", "Failed to persist async workflow receipt: "+err.Error()))
		return "", nil
	}
	path, werr := WriteWorkflowReceipt(x.st.asyncDir, receipt)
	if werr != nil {
		x.st.appendEvent(newObjectWith("type", "subagent.workflow.receipt_write_failed", true, "error", "Failed to persist async workflow receipt: "+werr.Error()))
		return "", nil
	}
	return path, receipt
}

// graphHostSteps are the host rows of the workflow graph, as the receipt records them.
func (x *wfExec) graphHostSteps() []any {
	x.st.mu.Lock()
	defer x.st.mu.Unlock()
	var out []any
	if x.st.graph == nil {
		return out
	}
	for _, n := range graphNodes(x.st.graph) {
		if no, ok := n.(*jsObject); ok && objStr(no, "kind") == "host-step" {
			if h := no.obj("hostStep"); h != nil {
				out = append(out, h)
			}
		}
	}
	return out
}

func (x *wfExec) sessionNameOf(key string) string {
	x.st.mu.Lock()
	defer x.st.mu.Unlock()
	if s := x.st.childStep(key); s != nil {
		return s.SessionName
	}
	return ""
}

func (x *wfExec) publishSuccess(res WorkflowScriptResult) {
	ws := x.st
	runID := x.workflowRunID
	warnings := WorkflowPreflightWarnings(x.preflight, traceLike(res.Trace), true)
	finalTrace := annotatedTrace(res.Trace, x.preflight)
	returnText := formatWorkflowValue(res.Value)
	var emitParts []string
	for _, v := range res.Emits {
		emitParts = append(emitParts, formatWorkflowValue(v))
	}
	emitText := strings.Join(emitParts, ", ")
	returnPreview, rcut := preview1000(returnText)
	emitPreview := ""
	ecut := false
	if len(res.Emits) > 0 {
		var ep string
		ep, ecut = preview1000(emitText)
		emitPreview = " Emitted: " + ep
	}
	note := ""
	statusPath := filepath.Join(ws.asyncDir, "status.json")
	if rcut || ecut {
		note = fmt.Sprintf(" (truncated; full return value and emits: %s (workflow.value, workflow.emits))", statusPath)
	}
	running := workflowRunningChildrenSummary(res.Children)
	head := fmt.Sprintf("Workflow completed with %d child run(s).", len(res.Children))
	if running != "" {
		head = "Workflow dispatch completed; " + running
	}
	summaryText := fmt.Sprintf("%s Return: %s%s%s Trace: %d event(s).%s", head, returnPreview, emitPreview, note, len(res.Trace), workflowOutputPathMappingSummary(res.Children))
	if len(warnings) > 0 {
		summaryText += " " + strings.Join(warnings, " ")
	}
	warn := writeWorkflowAggregateOutput(x.aggregateOutput, summaryText, x.producedCopy())
	resultSummary := appendWorkflowOutputWarning(summaryText, warn)
	summary, _ := WorkflowChildSummaryOf(WorkflowChildSummaryInput{ParentToolCallID: x.toolCallID, WorkflowRunID: runID, WorkflowState: "completed", InventoryComplete: true, Trace: res.Trace, Children: res.Children, Steps: ws.stepViews()})
	wf := newObject()
	wf.set("value", res.Value)
	wf.set("trace", traceJSON(finalTrace))
	wf.set("emits", res.Emits)
	wf.set("console", consoleJSON(res.Console))
	if x.args != nil {
		wf.set("args", x.args)
		wf.set("argsDigest", x.argsDigest)
	}
	if x.resource != nil {
		wf.set("resource", x.resource.provenanceObject())
	}
	if len(warnings) > 0 {
		wf.set("preflightWarnings", anyStrings(warnings))
	}
	receiptPath, receipt := x.writeReceipt("complete", res.Children, summary, nil)
	st, _ := x.finalizeStatus("complete", nil, wf, summary, receiptPath)
	_ = st
	payload := x.resultPayload(true, "complete", resultSummary, "", wf, summary, workflowResultChildren(res.Children, x.sessionNameOf, "complete", false), nil, receiptPath, receipt)
	if !x.publishResult(payload, "complete") {
		return
	}
	if err := x.writeFinalStatus(); err != nil {
		logAsyncError("Failed to persist async workflow state:", err)
	}
	x.deliver(payload)
	ws.appendEvent(newObjectWith("type", "subagent.workflow.completed", true, "state", "complete"))
}

func (x *wfExec) resultPayload(success bool, state, summary, errText string, wf *jsObject, children WorkflowChildSummary, results []any, outcome *jsObject, receiptPath string, receipt *jsObject) map[string]any {
	p := map[string]any{"id": x.workflowRunID, "runId": x.workflowRunID, "toolCallId": x.toolCallID, "agent": "workflow", "mode": "workflow", "success": success, "state": state, "summary": summary,
		"workflowChildren": jsAny(children), "results": results, "workflow": wf, "asyncDir": x.st.asyncDir, "cwd": x.workflowCwd, "sessionId": x.sessionID, "completionOwnerId": bgRT.ownerID,
		"timestamp": nowMs(), "durationMs": time.Since(x.startedAt).Milliseconds()}
	if success {
		p["output"] = summary
	} else {
		p["error"] = errText
		if state == "stopped" {
			p["stopped"] = true
		}
	}
	if outcome != nil {
		p["terminalOutcome"] = outcome
	}
	if receipt != nil {
		p["workflowReceipt"] = jo("path", receiptPath, "receipt", receipt)
	}
	if o := x.scheduleOriginObject(); o != nil {
		p["scheduleOrigin"] = o
	}
	if x.toolCallID == x.workflowRunID {
		delete(p, "toolCallId")
	}
	return p
}

func (x *wfExec) publishFailure(ctx context.Context, err error) {
	ws := x.st
	runID := x.workflowRunID
	partial, kind := partialOf(err)
	stopped := ctx.Err() != nil
	state := "failed"
	if stopped {
		state = "stopped"
	}
	msg := workflowFailureMessage(err, runID, partial.Children)
	if stopped && msg == "" {
		msg = workflowStopMessage
	}
	ws.mu.Lock()
	for i := range ws.st.Steps {
		s := &ws.st.Steps[i]
		if stopped && s.WorkflowKey != "" && (s.Status == "running" || s.Status == "pending") {
			s.Status = "stopped"
			t := true
			s.Stopped = &t
		}
	}
	ws.mu.Unlock()
	warnings := WorkflowPreflightWarnings(x.preflight, traceLike(partial.Trace), true)
	finalTrace := annotatedTrace(partial.Trace, x.preflight)
	wfState := "failed"
	if stopped {
		wfState = "stopped"
	}
	summary, _ := WorkflowChildSummaryOf(WorkflowChildSummaryInput{ParentToolCallID: x.toolCallID, WorkflowRunID: runID, WorkflowState: wfState, InventoryComplete: true, Trace: partial.Trace, Children: partial.Children, Steps: ws.stepViews()})
	wf := newObject()
	wf.set("trace", traceJSON(finalTrace))
	wf.set("emits", partial.Emits)
	wf.set("console", consoleJSON(partial.Console))
	if kind != "" {
		wf.set("failureKind", kind)
	}
	if x.args != nil {
		wf.set("args", x.args)
		wf.set("argsDigest", x.argsDigest)
	}
	if x.resource != nil {
		wf.set("resource", x.resource.provenanceObject())
	}
	if len(warnings) > 0 {
		wf.set("preflightWarnings", anyStrings(warnings))
	}
	errorPreview := msg
	if u := jsUTF16(msg); len(u) > 1000 {
		errorPreview = string(utf16Decode(u[:1000])) + fmt.Sprintf("… (truncated; full error: %s (error))", filepath.Join(ws.asyncDir, "status.json"))
	}
	terminal := errorPreview
	if terminal == "" {
		terminal = "Workflow failed."
	}
	terminal += workflowOutputPathMappingSummary(partial.Children)
	if len(warnings) > 0 {
		terminal += " " + strings.Join(warnings, " ")
	}
	warn := writeWorkflowAggregateOutput(x.aggregateOutput, terminal, x.producedCopy())
	resultSummary := appendWorkflowOutputWarning(terminal, warn)
	outcome := workflowFailureTerminalOutcome(err)
	receiptPath, receipt := x.writeReceipt(state, partial.Children, summary, outcome)
	_, _ = x.finalizeStatus(state, func(st *AsyncStatus) {
		st.Error = msg
		if stopped {
			t := true
			st.Stopped = &t
		}
	}, wf, summary, receiptPath)
	payload := x.resultPayload(false, state, resultSummary, msg, wf, summary, workflowResultChildren(partial.Children, x.sessionNameOf, state, true), outcome, receiptPath, receipt)
	if !x.publishResult(payload, state) {
		return
	}
	if werr := x.writeFinalStatus(); werr != nil {
		logAsyncError("Failed to persist async workflow state:", werr)
	}
	x.deliver(payload)
	ev := jo("type", "subagent.workflow.completed", "state", state)
	if msg != "" {
		ev.set("error", msg)
	}
	ws.appendEvent(ev)
}
