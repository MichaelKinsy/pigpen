package pi_subagents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The children of a workflow: what one `runs.run` launch does. A child that is not explicitly async runs to its end in this
// process through the single-run executor (policy, a pig child process, result); `async: true` starts it as a background run
// of the async supervisor and returns the launch receipt, as the original's explicit async launch does.
//
// Differences from the original, recorded in port/PORT.md: the original awaits an async runner for a child with `async` omitted;
// the port runs it in the foreground (the background engine does not apply the output policy), so such a child's session is not
// retained and cannot be resumed with `resume`, only a child launched with `async: true` can.

// wfInternalChildParams are the fields of a launch request the workflow itself sets or reads: they are not options of the tool.
var wfInternalChildParams = map[string]bool{
	"workflowParentRunId": true, "workflowKey": true, "workflowAwaitAsync": true, "workflowAwaitDetached": true, "workflowParentDeadlineAt": true,
	"runFanoutBudget": true, "runFanoutAdmitted": true, "suppressRoutineResultIntercom": true, "capabilityCeiling": true, "workflowOutputClaimPath": true,
	"lane": true, "label": true, "phase": true, "as": true, "index": true, "resume": true, "mission": true, "control": true,
}

// childParamsMap is the launch request as the policy modules read it.
func childParamsMap(p *jsObject) map[string]any {
	out := map[string]any{}
	for _, k := range p.order() {
		if v := p.vals[k]; !isUndefined(v) {
			out[k] = v
		}
	}
	return out
}

// unsupportedChildParams lists the options of a launch request this port does not run, sorted: they are refused, never ignored.
func unsupportedChildParams(p map[string]any) []string {
	var bad []string
	for _, k := range present(p) {
		if !runParams[k] && !wfInternalChildParams[k] {
			bad = append(bad, k)
		}
	}
	return bad
}

// wfRunOutcome is what a run of one child gave back: the text of the tool result and, when it has them, its details.
type wfRunOutcome struct {
	Text    string
	IsError bool
	Details *jsObject
	RunID   string
	Agent   string
	Task    string
}

// outcomeOf reads the result of the single-run executor.
func outcomeOf(res any, err error) wfRunOutcome {
	if err != nil {
		return wfRunOutcome{Text: err.Error(), IsError: true}
	}
	switch r := res.(type) {
	case explicitOK:
		d, _ := asObject(r.Details)
		return wfRunOutcome{Text: r.Content, Details: d}
	case sdk.ToolResult:
		d, _ := asObject(r.Details)
		return wfRunOutcome{Text: r.Content, IsError: r.IsError, Details: d}
	}
	return wfRunOutcome{Text: fmt.Sprint(res)}
}

func wfStringListOf(v any) []string {
	a, _ := asArray(v)
	var out []string
	for _, e := range a {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func nonEmpty(s string) bool { return s != "" }

func wfUniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// singleResultOf is the SingleResult a child's receipt carries (types.ts SingleResult).
func singleResultOf(o wfRunOutcome, key string, ok bool, terminal bool) *jsObject {
	r := newObject()
	r.set("index", 0.0)
	r.set("agent", o.Agent)
	r.set("task", o.Task)
	if ok {
		r.set("exitCode", 0.0)
	} else {
		r.set("exitCode", 1.0)
	}
	r.set("usage", jo("input", 0.0, "output", 0.0, "cacheRead", 0.0, "cacheWrite", 0.0, "cost", 0.0, "turns", 0.0))
	r.set("finalOutput", o.Text)
	if strings.TrimSpace(o.Text) != "" {
		r.set("outputState", "present")
	} else {
		r.set("outputState", "absent")
	}
	if !ok {
		r.set("error", o.Text)
	}
	if terminal {
		r.set("timedOut", true)
	}
	if d := o.Details; d != nil {
		for _, k := range []string{"savedOutputPath", "outputReference", "outputSaveError", "artifactPaths", "structuredOutput", "acceptance", "outputPathMapping", "timeoutMs"} {
			if d.has(k) {
				r.set(k, d.vals[k])
			}
		}
	}
	r.set("workflowKey", key)
	return r
}

// workflowChildResultFromRun is workflowChildResult for a run of the single-run executor.
func workflowChildResultFromRun(key string, o wfRunOutcome, childParams *jsObject, forcedTerminal *jsObject) (*jsObject, error) {
	timedOut := strings.HasPrefix(o.Text, "Subagent timed out after ")
	ok := !o.IsError
	out := newObject()
	out.set("key", key)
	out.set("ok", ok)
	lane, err := NormalizeWorkflowLaneMetadata(laneOf(childParams), fmt.Sprintf("workflow child '%s'.lane", key))
	if err != nil {
		return nil, err
	}
	if err := AssertWorkflowLaneKey(lane, key, fmt.Sprintf("workflow child '%s'.lane", key)); err != nil {
		return nil, err
	}
	if lane != nil {
		out.set("lane", lane)
	}
	switch {
	case forcedTerminal != nil:
		out.set("terminalOutcome", forcedTerminal)
	case timedOut:
		out.set("terminalOutcome", jo("state", "partial", "reason", "timeout"))
	}
	if o.Agent != "" {
		out.set("agent", o.Agent)
	}
	if o.RunID != "" {
		out.set("runId", o.RunID)
	}
	out.set("output", o.Text)
	if !ok {
		errText := o.Text
		if errText == "" {
			errText = "Child run failed."
		}
		out.set("error", errText)
	}
	d := o.Details
	var saved, refPath, outputArtifact string
	if d != nil {
		if s, _ := d.vals["structuredOutput"]; s != nil && !isUndefined(s) {
			out.set("structuredOutput", s)
		}
		saved = objStr(d, "savedOutputPath")
		if r := d.obj("outputReference"); r != nil {
			refPath = objStr(r, "path")
		}
		if a := d.obj("artifactPaths"); a != nil {
			outputArtifact = objStr(a, "outputPath")
		}
	}
	ctxName := ""
	if c, _ := childParams.vals["context"].(string); c == "fresh" || c == "fork" {
		ctxName = c
		out.set("requestedContext", c)
		out.set("resolvedContext", c)
	}
	_ = ctxName
	reference := saved
	if reference == "" {
		reference = refPath
	}
	if reference != "" {
		out.set("outputReference", reference)
	}
	if outputArtifact != "" {
		out.set("outputArtifactPath", outputArtifact)
	}
	if task, _ := childParams.vals["task"].(string); task != "" {
		if m, ok := outputPathMappingFromTask(task, reference); ok {
			out.set("outputPathMapping", jo("requestedPath", m.RequestedPath, "savedPath", m.SavedPath))
		}
	}
	out.set("resumability", jo("state", "not-resumable", "reason", "foreground child sessions are not retained; launch the child with async: true to resume it"))
	ids := []any{}
	if o.RunID != "" {
		ids = append(ids, o.RunID)
	}
	out.set("continuation", jo("runIds", ids))
	var artifacts []string
	artifacts = append(artifacts, saved, refPath)
	out.set("artifactPaths", anyStrings(wfUniqueStrings(artifacts)))
	out.set("results", []any{singleResultOf(o, key, ok, timedOut)})
	return out, nil
}

func laneOf(p *jsObject) any {
	if v, ok := p.vals["lane"]; ok {
		return v
	}
	return undef{}
}

// asyncReceiptResult is workflowChildResult for the receipt of an explicit async launch: not final, so not ok.
func asyncReceiptResult(key string, o wfRunOutcome, childParams *jsObject) (*jsObject, error) {
	out := newObject()
	out.set("key", key)
	out.set("ok", false)
	out.set("state", "running")
	asyncDir, asyncID := "", ""
	if o.Details != nil {
		asyncDir, asyncID = objStr(o.Details, "asyncDir"), objStr(o.Details, "asyncId")
	}
	if asyncDir != "" {
		out.set("asyncDir", asyncDir)
	}
	lane, err := NormalizeWorkflowLaneMetadata(laneOf(childParams), fmt.Sprintf("workflow child '%s'.lane", key))
	if err != nil {
		return nil, err
	}
	if err := AssertWorkflowLaneKey(lane, key, fmt.Sprintf("workflow child '%s'.lane", key)); err != nil {
		return nil, err
	}
	if lane != nil {
		out.set("lane", lane)
	}
	if o.Agent != "" {
		out.set("agent", o.Agent)
	}
	if asyncID != "" {
		out.set("runId", asyncID)
	}
	out.set("output", "")
	out.set("resumability", jo("state", "not-resumable", "reason", "child is still running"))
	ids := []any{}
	if asyncID != "" {
		ids = append(ids, asyncID)
	}
	out.set("continuation", jo("runIds", ids))
	arts := []any{}
	if asyncDir != "" {
		arts = append(arts, asyncDir)
	}
	out.set("artifactPaths", arts)
	out.set("results", []any{})
	return out, nil
}

// buildRequestedModeError is the failed result of a launch that could not start.
func failedChildResult(key string, text string, childParams *jsObject) (*jsObject, error) {
	return workflowChildResultFromRun(key, wfRunOutcome{Text: text, IsError: true}, childParams, nil)
}

// childAgent resolves the agent of a child launch.
func (x *wfExec) childAgent(params map[string]any) (*AgentConfig, string, error) {
	name, _ := params["agent"].(string)
	name = jsTrim(name)
	if name == "" {
		return nil, "", errors.New("Structured single-child execution requires agent to be a non-empty string.")
	}
	scope := ResolveExecutionAgentScope(params["agentScope"])
	cwdBase := x.workflowCwd
	d, derr := discoverAgents(cwdBase, scope, x.ctx.ModelProvider(), adv.options())
	if derr != nil {
		return nil, "", derr
	}
	a, msg := ResolveAgentName(name, d.Agents)
	if a == nil {
		if diag := blockingDiagnostic(name, nil, d.Diagnostics); diag != nil {
			msg = fmt.Sprintf("Agent '%s' has invalid configuration: %s", name, diag.Error)
		} else if msg == "" {
			msg = fmt.Sprintf("Unknown agent '%s'. Available: %s.", name, strings.Join(sortedNames(d.Agents), ", "))
		}
		return nil, "", errors.New(msg)
	}
	return a, cwdBase, nil
}

// runChildForeground runs one child to its end through the single-run executor.
func (x *wfExec) runChildForeground(done <-chan struct{}, key string, params map[string]any) wfRunOutcome {
	fail := func(text string) wfRunOutcome { return wfRunOutcome{Text: text, IsError: true} }
	task, _ := params["task"].(string)
	a, _, err := x.childAgent(params)
	if err != nil {
		return fail(err.Error())
	}
	if task == "" {
		return fail(refusal("a run without `task`"))
	}
	requested, _ := params["cwd"].(string)
	cwd, err := resolveRunCwd(x.workflowCwd, requested)
	if err != nil {
		return fail(err.Error())
	}
	model, _ := params["model"].(string)
	// the workflow's remaining time is the child's deadline when nothing else sets one
	if _, has := params["timeoutMs"]; !has {
		if _, has := params["maxRuntimeMs"]; !has && (a.DefaultTimeoutMs == nil || *a.DefaultTimeoutMs <= 0) {
			if at, ok := params["workflowParentDeadlineAt"].(float64); ok {
				remaining := at - float64(time.Now().UnixMilli())
				if remaining < 1 {
					remaining = 1
				}
				params["timeoutMs"] = float64(int64(remaining))
			}
		}
	}
	pol, err := applyRunPolicy(x.ctx, params, a, task, cwd)
	if err != nil {
		return fail(err.Error())
	}
	pol.writeInputArtifact()
	defer activeRunStart()()
	x.rememberForegroundChild(key, pol.RunID, a.Name)
	out, runErr := runAgentOpts(done, cwd, pol.Agent, pol.Task, model, runOptions{TimeoutMs: pol.TimeoutMs})
	res, ferr := finishRun(pol, runOutcome{Stdout: out, Err: runErr})
	o := outcomeOf(res, ferr)
	o.RunID, o.Agent, o.Task = pol.RunID, a.Name, task
	return o
}

// launchAsyncChild starts one child as a background run and returns its receipt.
func (x *wfExec) launchAsyncChild(key string, params map[string]any) wfRunOutcome {
	fail := func(text string) wfRunOutcome { return wfRunOutcome{Text: text, IsError: true} }
	a, _, err := x.childAgent(params)
	if err != nil {
		return fail(err.Error())
	}
	task, _ := params["task"].(string)
	if task == "" {
		return fail(refusal("a run without `task`"))
	}
	// `mission: false` is the workflow's own record that its mission, not the child, tracks the run
	if params["mission"] == false {
		delete(params, "mission")
	}
	if p := asyncUnsupportedPolicyParam(params); p != "" {
		return fail(refusal("the `" + p + "` option of a background (async) run; pass async: false to run in the foreground"))
	}
	requested, _ := params["cwd"].(string)
	cwd, err := resolveRunCwd(x.workflowCwd, requested)
	if err != nil {
		return fail(err.Error())
	}
	model, _ := params["model"].(string)
	if bgRT == nil {
		return fail("Background runs are unavailable in this host.")
	}
	res, lerr := bgRT.launchBackground(x.ctx, params, a, task, cwd, model, loadAsyncConfig())
	o := outcomeOf(res, lerr)
	o.Agent, o.Task = a.Name, task
	if o.Details != nil {
		o.RunID = objStr(o.Details, "asyncId")
		if o.RunID != "" {
			bgRT.mu.Lock()
			if j := bgRT.state.AsyncJobs[o.RunID]; j != nil {
				j.ParentWorkflowRunID, j.WorkflowKey = x.workflowRunID, key
			}
			bgRT.mu.Unlock()
		}
		if dir := objStr(o.Details, "asyncDir"); dir != "" && x.mission != nil {
			_ = writeMissionAsyncBinding(dir, *x.mission)
		}
	}
	return o
}

// rememberForegroundChild records a foreground child run so `status` and `bg_wait` know it (rememberForegroundRun).
func (x *wfExec) rememberForegroundChild(key, runID, agent string) {
	if x.state == nil {
		return
	}
	x.state.Mu.Lock()
	defer x.state.Mu.Unlock()
	if x.state.ForegroundRuns == nil {
		x.state.ForegroundRuns = map[string]*ForegroundRunRecord{}
	}
	x.state.ForegroundRuns[runID] = &ForegroundRunRecord{RunID: runID, SessionID: x.sessionID, Children: []ForegroundChildRecord{{Index: 0, Agent: agent, Status: "running"}}}
	if x.state.ForegroundControls == nil {
		x.state.ForegroundControls = map[string]*ForegroundControl{}
	}
	x.state.ForegroundControls[runID] = &ForegroundControl{RunID: runID, ParentWorkflowRunID: x.workflowRunID, WorkflowKey: key, ActiveChildren: map[int]*ForegroundChildControl{0: {Index: 0, Agent: agent}}}
}

// forgetForegroundChild is the end of the control of a foreground child; the run stays remembered with its end status.
func (x *wfExec) forgetForegroundChild(runID, status string) {
	if x.state == nil || runID == "" {
		return
	}
	x.state.Mu.Lock()
	defer x.state.Mu.Unlock()
	delete(x.state.ForegroundControls, runID)
	if r := x.state.ForegroundRuns[runID]; r != nil && len(r.Children) > 0 {
		r.Children[0].Status = status
	}
}

// launch is the Launch callback of the workflow run: one child, from its params to its receipt.
func (x *wfExec) launch(ctx context.Context, key string, childParams *jsObject, adm WorkflowAdmission) *WorkflowFuture[*jsObject] {
	return WorkflowAsync(func() (*jsObject, error) { return x.launchSync(ctx, key, childParams, adm) })
}

func (x *wfExec) launchSync(ctx context.Context, key string, childParams *jsObject, adm WorkflowAdmission) (*jsObject, error) {
	phase, _ := childParams.vals["phase"].(string)
	phase = jsTrim(phase)
	label, _ := childParams.vals["label"].(string)
	label = jsTrim(label)
	agentName, _ := childParams.vals["agent"].(string)
	agentName = jsTrim(agentName)
	x.recordMissionChild(key, MissionWorkflowChildUpdate{Status: "running", Agent: agentName, Label: label, Phase: phase, Heartbeat: &MissionHeartbeatUpdate{Status: "running", Phase: phase}})
	x.mu.Lock()
	override, hasOverride := x.childOutputOverrides[key]
	claimPath := x.childOutputClaimPaths[key]
	x.mu.Unlock()
	opt := WorkflowLaunchOptions{MissionDetached: x.detachMissions, AwaitDetachedChild: !x.foreground, RunFanoutBudget: x.fanout.object(), ParentDeadlineAt: x.deadlineAt, CapabilityCeiling: nil}
	if x.foreground {
		opt.SuppressRoutineResultIntercom = x.chatProgressLiveCard
	}
	prepared, err := prepareWorkflowChildLaunchParams(wfChildLaunchInput{Env: x.env, WorkflowDefaults: x.defaults, ChildParams: childParams, ParentWorkflowRun: x.workflowRunID, WorkflowKey: key, OutputOverride: override, HasOutputOverride: hasOverride, OutputClaimPath: claimPath, Options: opt})
	if err != nil {
		x.recordMissionChild(key, MissionWorkflowChildUpdate{Status: "failed", CompletedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Heartbeat: &MissionHeartbeatUpdate{Status: "failed", Phase: phase, Message: err.Error()}})
		return nil, err
	}
	prepared.set("runFanoutAdmitted", adm.Admitted)
	params := childParamsMap(prepared)
	if bad := unsupportedChildParams(params); len(bad) > 0 {
		text := refusal(optionsFeature(bad))
		x.recordMissionChild(key, MissionWorkflowChildUpdate{Status: "failed", CompletedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Heartbeat: &MissionHeartbeatUpdate{Status: "failed", Phase: phase, Message: text}})
		return failedChildResult(key, text, childParams)
	}
	if params["resume"] != nil {
		text := refusal("retained `resume` of a workflow child")
		return failedChildResult(key, text, childParams)
	}
	x.childStarted(key, agentName)
	done := ctx.Done()
	var o wfRunOutcome
	asyncChild := params["async"] == true
	if asyncChild {
		o = x.launchAsyncChild(key, params)
	} else {
		o = x.runChildForeground(done, key, params)
		x.forgetForegroundChild(o.RunID, map[bool]string{true: "failed", false: "completed"}[o.IsError])
	}
	var child *jsObject
	if asyncChild && !o.IsError {
		child, err = asyncReceiptResult(key, o, prepared)
	} else {
		child, err = workflowChildResultFromRun(key, o, prepared, nil)
	}
	if err != nil {
		return nil, err
	}
	x.childFinished(key, child)
	status := "completed"
	switch {
	case objStr(child, "state") == "running":
		status = "running"
	case !objBool(child, "ok"):
		status = "failed"
	}
	upd := MissionWorkflowChildUpdate{Status: status, RunID: objStr(child, "runId"), Agent: o.Agent, ArtifactPaths: wfStringListOf(child.vals["artifactPaths"]), Heartbeat: &MissionHeartbeatUpdate{Status: status, Phase: phase}}
	if status == "completed" || status == "failed" {
		upd.CompletedAt = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	x.recordMissionChild(key, upd)
	return child, nil
}

// recordMissionChild is recordMissionWorkflowChild: the child's progress in the mission; a failure to record is a warning.
func (x *wfExec) recordMissionChild(key string, u MissionWorkflowChildUpdate) {
	if x.mission == nil {
		return
	}
	x.mu.Lock()
	missing := x.missionMissing
	x.mu.Unlock()
	if missing {
		return
	}
	u.WorkflowRunID, u.Key = x.workflowRunID, key
	u.Task = ""
	_, err := updateMission(x.mission.Location, x.mission.MissionID, MissionUpdateInput{UpsertWorkflowChildren: []MissionWorkflowChildUpdate{u}}, time.Now(), -1)
	if err == nil {
		return
	}
	var nf *MissionNotFoundError
	if errors.As(err, &nf) {
		x.mu.Lock()
		x.missionMissing = true
		x.mu.Unlock()
		fmt.Fprintf(os.Stderr, "[pi-subagents] Mission '%s' is no longer in the mission store; stopped recording its workflow children. Terminal-mission retention can prune a mission while its run is still active.\n", x.mission.MissionID)
		return
	}
	fmt.Fprintf(os.Stderr, "[pi-subagents] Failed to record mission workflow child '%s': %s\n", key, err.Error())
}
