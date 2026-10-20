package pi_subagents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The workflow executor: a `subagent` call that carries a `workflowScript`, `workflowScriptPath` or `workflow` runs the script
// in the goja sandbox (workflow_sandbox*.go) and answers its `runs` calls by running real children (workflow_exec_child.go).
// Port of the workflow half of createSubagentExecutor's execute (subagent-executor.ts:5266-6478). An async workflow (the
// default; `async` omitted or true) returns at once and keeps its state in an async run directory; a foreground one (`async:
// false`) blocks the call and reports the whole run.

func init() {
	registerRunParams("workflow", "workflowScript", "workflowScriptPath", "args", "preflight", "globalConcurrencyLimit", "maxSubagentSpawnsPerRun", "chatProgress")
}

// wfExec is one workflow run.
type wfExec struct {
	ctx        sdk.Context
	toolCallID string
	state      *SubagentState
	sessionID  string

	script               string
	args                 *jsObject
	argsDigest           string
	resource             *wfResourceUse
	preflight            *WorkflowPreflight
	workflowRunID        string
	foreground           bool
	workflowCwd          string
	parentCwd            string
	defaults             *jsObject
	env                  *wfOutputEnv
	fanout               runFanoutBudgetDescriptor
	artifactsDir         string
	aggregateOutput      string
	fullResultPath       string
	maxOutput            MaxOutputConfig
	timeoutMs            *float64
	deadlineAt           *float64
	globalLimit          *float64
	chatProgress         *WorkflowChatProgressProjection
	chatProgressLiveCard bool
	scheduleOrigin       any

	mission         *MissionLaunchBinding
	missionWarning  string
	explicitMission bool
	detachMissions  bool
	missionMissing  bool
	missionState    *MissionWorkflowState

	mu                    sync.Mutex
	claimed               map[string]string
	childOutputOverrides  map[string]string
	childOutputClaimPaths map[string]string
	produced              map[string]bool
	childRunIDs           map[string]string
	hostSteps             map[string]*WorkflowHostStep
	hostStepOrder         []string
	announced             map[string]bool
	st                    *wfStatus
	startedAt             time.Time
}

// wfResourceUse is a named workflow resource a call resolved to.
type wfResourceUse struct {
	Permit     *WorkflowResourcePermit
	Provenance WorkflowResourceProvenance
	Authority  WorkflowResourceAuthority
}

func (r *wfResourceUse) provenanceObject() *jsObject { return jsAny(r.Provenance).(*jsObject) }

func parseParamJSON(v any) any {
	out, err := parseJSON([]byte(marshalAny(v)))
	if err != nil {
		return undef{}
	}
	return out
}

func resultOf(text string, isError bool, details any) (any, error) {
	if isError {
		return sdk.ToolResult{Content: text, IsError: true, Details: details}, nil
	}
	return explicitOK{Content: text, Details: details}, nil
}

// executeWorkflowRun is the workflow branch of the subagent tool; params have passed the public boundary.
func executeWorkflowRun(ctx sdk.Context, params map[string]any) (any, error) {
	if msg := nestedBlocked(os.Getenv); msg != "" {
		return nil, sdk.NewToolError(msg)
	}
	x := &wfExec{ctx: ctx, toolCallID: ctx.ToolCallID(), announced: map[string]bool{}, claimed: map[string]string{}, childOutputOverrides: map[string]string{}, childOutputClaimPaths: map[string]string{},
		produced: map[string]bool{}, childRunIDs: map[string]string{}, hostSteps: map[string]*WorkflowHostStep{}, startedAt: time.Now()}
	x.sessionID = currentSessionID(ctx)
	if bgRT != nil {
		x.state = bgRT.state
	} else {
		x.state = newSubagentState()
	}
	p := make(map[string]any, len(params))
	for k, v := range params {
		p[k] = v
	}
	fail := func(text string) (any, error) { return nil, sdk.NewToolError(text) }

	// the script: a named resource, a path, or the text
	hasArgs := false
	var permit *WorkflowResourcePermit
	switch {
	case defined(p, "workflow"):
		argsV := any(undef{})
		if defined(p, "args") {
			argsV = parseParamJSON(p["args"])
		}
		res, errText := ResolveWorkflowResource(p["workflow"], argsV, x.sessionID)
		if res == nil {
			return fail(errText)
		}
		delete(p, "workflow")
		delete(p, "args")
		p["workflowScript"] = res.Script
		permit = res.Permit
	case defined(p, "workflowScript") || defined(p, "workflowScriptPath"):
		if defined(p, "args") {
			args, err := NormalizeWorkflowArgs(parseParamJSON(p["args"]))
			if err != nil {
				return fail(err.Error())
			}
			x.args, hasArgs = args, true
		}
	}
	if defined(p, "workflowScriptPath") {
		script, errText := loadWorkflowScriptPath(ctx.Cwd(), p)
		if errText != "" {
			return fail(errText)
		}
		delete(p, "workflowScriptPath")
		p["workflowScript"] = script
	}
	script, _ := p["workflowScript"].(string)
	x.script = script
	if hasArgs {
		x.argsDigest = stableJSONDigest(x.args)
	}

	// the request, with its gate normalized
	req, _ := toDyn(p).(*jsObject)
	req, gerr := normalizeWorkflowGateParams(req)
	if gerr != nil {
		return fail(gerr.Error())
	}
	if msg := validateWorkflowCapacityOverrides(p); msg != "" {
		return fail(msg)
	}
	if defined(p, "preflight") {
		pf, err := NormalizeWorkflowPreflight(parseParamJSON(p["preflight"]))
		if err != nil {
			return fail(err.Error())
		}
		x.preflight = pf
	}
	if permit != nil {
		prov, auth, msg := ConsumeWorkflowResourcePermit(permit, script)
		if msg != "" {
			return fail(msg)
		}
		x.resource = &wfResourceUse{Permit: permit, Provenance: prov, Authority: auth}
	}

	// the static validation: a script that cannot launch its children as written never starts
	x.parentCwd = ctx.Cwd()
	x.workflowCwd = ctx.Cwd()
	if c, ok := p["cwd"].(string); ok && c != "" {
		x.workflowCwd = nodeResolve(x.parentCwd, c)
	}
	scope := ResolveExecutionAgentScope(p["agentScope"])
	cfg := loadExtensionConfig()
	budget := resolveMaxSubagentSpawnsPerRun(cfg.MaxSubagentSpawnsPerRun, osEnv)
	if f, ok := positiveSafeInt(p["maxSubagentSpawnsPerRun"]); ok {
		budget = int(f)
	}
	discovery, derr := discoverAgents(x.workflowCwd, scope, ctx.ModelProvider(), adv.options())
	if derr != nil {
		return fail(derr.Error())
	}
	validation := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{MaxSubagentSpawnsPerRun: &budget, AgentNameError: workflowAgentNameError(discovery.Agents)})
	if !validation.OK {
		return nil, sdk.NewToolError(marshalJSON(validationObject(validation, x.preflight, ""), ""))
	}
	for _, w := range validation.Warnings {
		fmt.Fprintf(os.Stderr, "[pi-subagents] %s\n", w.Message)
	}
	if errs := validateAcceptanceInput(p["acceptance"], "acceptance"); len(errs) > 0 {
		return fail(strings.Join(errs, " "))
	}
	if ext, ok := p["extensionBindings"]; ok && !isUndefined(ext) {
		n, err := normalizeExtensionBindings(toDyn(ext))
		if err != nil {
			return fail(err.Error())
		}
		if n != nil {
			p["extensionBindings"] = n.value
			req.set("extensionBindings", n.value)
		}
	}
	for _, name := range []string{"timeoutMs", "maxRuntimeMs"} {
		if v, ok := p[name]; ok && !isUndefined(v) {
			if msg := timerDelayOverflowError(name, v); msg != "" {
				return fail(msg)
			}
		}
	}
	x.foreground = p["async"] == false
	if t, ok := p["timeoutMs"].(float64); ok {
		x.timeoutMs = &t
	} else if t, ok := p["maxRuntimeMs"].(float64); ok {
		x.timeoutMs = &t
	} else if x.foreground {
		d := float64(defaultForegroundTimeoutMs)
		if c := resolveConfigDefaultTimeoutMs(cfg.TimeoutMs); c != 0 {
			d = float64(c)
		}
		x.timeoutMs = &d
	}
	if f, ok := positiveSafeInt(p["globalConcurrencyLimit"]); ok {
		x.globalLimit = &f
	}
	x.maxOutput = DefaultMaxOutput

	sessionFile := ""
	if f, err := ctx.GetSessionFile(); err == nil && f != nil {
		sessionFile = *f
	}
	if dir, err := getArtifactsDir(sessionFile, x.workflowCwd, "session"); err == nil {
		x.artifactsDir = dir
	}
	cp, cpErr := resolveWorkflowChatProgress(WorkflowChatProgressInput{Requested: nilIfUndefined(p["chatProgress"]), ParentCwd: x.parentCwd, WorkflowCwd: x.workflowCwd, Background: !x.foreground})
	if cpErr != "" {
		return nil, sdk.NewToolError(cpErr)
	}
	x.chatProgress = cp
	x.chatProgressLiveCard = cp.Mode == "live-card"

	// the fan-out budget and the mission
	var ferr error
	x.fanout, ferr = createRunFanoutBudget(x.toolCallID, budget)
	if ferr != nil {
		return fail(ferr.Error())
	}
	explicit := defined(p, "missionId") || defined(p, "mission")
	x.explicitMission = explicit
	derived := ""
	if !explicit {
		if pv, ok := PreviewSimpleWorkflowRun(script); ok && pv.Agent != nil && jsTrim(*pv.Agent) != "" {
			derived = "Workflow: " + jsTrim(*pv.Agent)
		} else {
			first := "Workflow"
			for _, line := range strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n") {
				if t := jsTrim(line); t != "" {
					first = t
					break
				}
			}
			if u := jsUTF16(first); len(u) > 100 {
				first = string(utf16Decode(u[:97])) + "..."
			}
			derived = first
		}
	}
	launch := newObject()
	if !explicit {
		launch.set("task", derived)
	}
	for _, k := range []string{"missionId", "mission"} {
		if defined(p, k) {
			launch.set(k, toDyn(p[k]))
		}
	}
	binding, merr := prepareMissionLaunch(launch, x.workflowCwd, loadMissionStoreConfig(), currentSessionIdentity(ctx))
	if merr != nil {
		if explicit {
			return fail(merr.Error())
		}
		x.missionWarning = "Mission tracking unavailable: " + merr.Error()
		binding = nil
	}
	x.mission = binding
	x.detachMissions = !explicit || binding != nil || p["mission"] == false
	if binding != nil {
		mws, err := NewMissionWorkflowState(binding.Location, binding.MissionID, MissionWorkflowStateOptions{})
		if err == nil {
			x.missionState = mws
		}
	}

	// what children inherit: the request without the workflow's own options
	drop := []string{"workflowScript", "async", "chatProgress", "args", "action", "agent", "task", "resume", "tasks", "chain", "concurrency", "foregroundOnly", "clarify",
		"timeoutMs", "maxRuntimeMs", "usageBudget", "missionId", "mission", "preflight", "globalConcurrencyLimit", "maxSubagentSpawnsPerRun"}
	x.defaults = lpWithout(req, drop...)
	x.setupEnv(discovery.Agents, p)

	if x.foreground {
		x.workflowRunID = encodeIndexSegment(x.toolCallID)
		return x.runForeground()
	}
	x.workflowRunID = randomUUID()
	return x.runAsync(p)
}

func nilIfUndefined(v any) any {
	if isUndefined(v) {
		return nil
	}
	return v
}

func utf16Decode(u []uint16) []rune {
	var out []rune
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xd800 && c < 0xdc00 && i+1 < len(u) && u[i+1] >= 0xdc00 && u[i+1] < 0xe000 {
			c = 0x10000 + (c-0xd800)<<10 + rune(u[i+1]) - 0xdc00
			i++
		}
		out = append(out, c)
	}
	return out
}

// setupEnv fixes the output paths of the run: its own, and where each child's output goes by default.
func (x *wfExec) setupEnv(agents []AgentConfig, p map[string]any) {
	x.env = &wfOutputEnv{CtxCwd: x.parentCwd, WorkflowCwd: x.workflowCwd, ArtifactsDir: x.artifactsDir, WorkflowRunID: x.workflowRunID, SessionID: x.sessionID, Agents: agents,
		WorkflowAgentScope: x.defaults.vals["agentScope"],
		Discover: func(cwd string, scope AgentScope) []AgentConfig {
			d, err := discoverAgents(cwd, scope, x.ctx.ModelProvider(), adv.options())
			if err != nil {
				return nil
			}
			return d.Agents
		}}
	if base := configuredSingleRunOutputBaseDir(); base != "" {
		x.env.ConfiguredOutputBaseDir = base
	}
}

// bindRun fixes what depends on the run id (known once the mode is).
func (x *wfExec) bindRun() {
	x.env.WorkflowRunID = x.workflowRunID
	var output any
	if o, ok := x.defaults.vals["output"]; ok && !isUndefinedValue(o) {
		switch o.(type) {
		case string, bool:
			output = o
		}
	}
	x.aggregateOutput = resolveSingleOutputPath(output, x.parentCwd, x.workflowCwd, x.env.outputBase())
	x.env.AggregateOutputPath = x.aggregateOutput
	x.fullResultPath = filepath.Join(x.artifactsDir, sanitizeRunPathSegment(x.workflowRunID, 120)+"_workflow-result.md")
	if x.timeoutMs != nil {
		d := float64(time.Now().UnixMilli()) + *x.timeoutMs
		x.deadlineAt = &d
	}
}

func configuredSingleRunOutputBaseDir() string {
	var raw map[string]any
	if readJSONFile(asyncConfigPath(), &raw) != nil {
		return ""
	}
	s, _ := raw["singleRunOutputBaseDir"].(string)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "~") {
		s = filepath.Join(homeDir(), strings.TrimPrefix(s, "~"))
	}
	abs, err := filepath.Abs(s)
	if err != nil {
		return s
	}
	return abs
}

// scriptOptions are the options of the script run: its children, its companions and its reports.
func (x *wfExec) scriptOptions(ctx context.Context) RunWorkflowScriptOptions {
	opt := RunWorkflowScriptOptions{Script: x.script, Args: x.args, WorkflowRunID: x.workflowRunID, ProcessCwd: x.ctx.Cwd(), Ctx: ctx, TimeoutMs: x.timeoutMs, GlobalConcurrencyLimit: x.globalLimit}
	opt.Admit = x.admit
	opt.Launch = x.launch
	opt.Status = x.statusCall
	opt.Steer = x.steerCall
	if x.missionState != nil {
		opt.State = &WorkflowState{Get: x.missionState.GetState, Set: x.missionState.SetState}
	}
	if x.resource != nil && x.resource.Authority.Host != nil {
		runner := &wfHostRunner{WorkflowCwd: x.workflowCwd, ArtifactsDir: x.artifactsDir, WorkflowRunID: x.workflowRunID, Claimed: x.claimed, Produced: x.produced,
			Authorize: func(key string, p WorkflowHostCommandParams) string {
				return AuthorizeWorkflowResourceHost(x.resource.Permit, key, p.Command)
			}}
		opt.Host = runner.Host
	}
	return opt
}

func (x *wfExec) admit(ctx context.Context, calls []WorkflowCallSpec) *WorkflowFuture[struct{}] {
	return WorkflowAsync(func() (struct{}, error) {
		x.mu.Lock()
		defer x.mu.Unlock()
		claims, err := x.env.workflowChildOutputClaims(x.claimed, calls)
		if err != nil {
			return struct{}{}, err
		}
		paths := make([]string, len(calls))
		for i, c := range calls {
			paths[i] = "workflow[" + c.Key + "]"
		}
		snap, err := claimRunFanoutBatch(x.fanout, paths)
		if err != nil {
			return struct{}{}, err
		}
		applyWorkflowChildOutputClaims(x.claimed, claims.Claims)
		for k, v := range claims.ChildClaims {
			x.childOutputClaimPaths[k] = v
		}
		for k, v := range claims.Overrides {
			x.childOutputOverrides[k] = v
		}
		if x.st != nil {
			x.st.setFanout(snap)
		}
		return struct{}{}, nil
	})
}

// statusCall is runs.status: the status action's answer for a run id, as a child result.
func (x *wfExec) statusCall(ctx context.Context, keyOrRunID string) *WorkflowFuture[*jsObject] {
	return WorkflowAsync(func() (*jsObject, error) {
		a := actions["status"]
		res, err := a.run(x.ctx, map[string]any{"action": "status", "id": keyOrRunID})
		o := outcomeOf(res, err)
		child, cerr := workflowChildResultFromRun(keyOrRunID, o, newObject(), nil)
		if cerr != nil {
			return nil, cerr
		}
		child.set("resumability", jo("state", "not-resumable", "reason", "child produced no run id"))
		return child, nil
	})
}

// workflowSteerHook delivers a steer to a child of a workflow that is not a foreground child of this process (a background child
// of the supervisor); the steering slice sets it. It reports false when it has no route.
var workflowSteerHook func(ctx sdk.Context, workflowRunID, key, message string, o WorkflowSteerOptions, childRunID string) (WorkflowSteerResult, bool)

// steerCall is runs.steer: a live child takes the message through its control; a child that cannot take one, or that has ended,
// is reported as the original reports it.
func (x *wfExec) steerCall(ctx context.Context, key, message string, o WorkflowSteerOptions) *WorkflowFuture[WorkflowSteerResult] {
	return WorkflowAsync(func() (WorkflowSteerResult, error) {
		x.mu.Lock()
		childRunID := x.childRunIDs[key]
		x.mu.Unlock()
		x.state.Mu.Lock()
		var live *ForegroundControl
		for _, c := range x.state.ForegroundControls {
			if c.ParentWorkflowRunID == x.workflowRunID && c.WorkflowKey == key && c.LiveChildCount() > 0 {
				live = c
			}
		}
		x.state.Mu.Unlock()
		if live != nil {
			for _, ch := range live.ActiveChildren {
				if ch.Steer != nil {
					mode := o.Mode
					if mode == "" {
						mode = "auto"
					}
					state, reason := ch.Steer(message, mode)
					switch state {
					case "delivered":
						return WorkflowSteerResult{Key: key, State: "delivered", DeliveryStatus: "delivered"}, nil
					case "queued":
						return WorkflowSteerResult{Key: key, State: "queued", DeliveryStatus: "queued"}, nil
					}
					return WorkflowSteerResult{Key: key, State: "failed", Error: reason}, nil
				}
			}
			return WorkflowSteerResult{Key: key, State: "failed", Error: refusal("steering a foreground workflow child (a child `pig --print` process reads no input after its task)")}, nil
		}
		if workflowSteerHook != nil {
			if r, ok := workflowSteerHook(x.ctx, x.workflowRunID, key, message, o, childRunID); ok {
				return r, nil
			}
		}
		if childRunID != "" {
			if st, _ := readStatusQuiet(filepath.Join(defaultAsyncDirs().Async, childRunID)); st != nil && !isActiveAsyncState(st.State) {
				return WorkflowSteerResult{Key: key, State: "missed", Error: fmt.Sprintf("Workflow child '%s' is %s.", key, st.State)}, nil
			}
			return WorkflowSteerResult{Key: key, State: "failed", Error: refusal("steering a background workflow child")}, nil
		}
		return WorkflowSteerResult{Key: key, State: "missed", Error: fmt.Sprintf("Workflow child '%s' had no live steering route.", key)}, nil
	})
}

// childStarted and childFinished keep the workflow's record of a child up to date.
func (x *wfExec) childStarted(key, agent string) {}

func (x *wfExec) childFinished(key string, child *jsObject) {
	if runID := objStr(child, "runId"); runID != "" {
		x.mu.Lock()
		x.childRunIDs[key] = runID
		x.mu.Unlock()
	}
	for _, r := range asResults(child) {
		if saved := objStr(r, "savedOutputPath"); saved != "" {
			x.mu.Lock()
			x.produced[workflowHostOutputClaimPath(saved)] = true
			x.mu.Unlock()
		}
	}
	if x.st != nil {
		asyncChild := objStr(child, "state") == "running" || objStr(child, "asyncDir") != ""
		var lane any
		if l := child.obj("lane"); l != nil {
			lane = l
		}
		x.st.childLaunched(key, objStr(child, "agent"), objStr(child, "runId"), asyncChild, lane)
	}
}

func asResults(child *jsObject) []*jsObject {
	var out []*jsObject
	rs, _ := child.vals["results"].([]any)
	for _, r := range rs {
		if ro, ok := r.(*jsObject); ok {
			out = append(out, ro)
		}
	}
	return out
}

// attachMission records the result in the mission of the run and returns the text and details it adds.
func (x *wfExec) attachMission(text string, isError bool, runID, asyncID, asyncDir string, details *jsObject) (string, bool) {
	if x.mission == nil {
		if x.missionWarning != "" {
			details.set("missionWarning", x.missionWarning)
		}
		return text, isError
	}
	res := MissionToolResult{Content: []MissionContent{{Type: "text", Text: text}}, IsError: isError, Mode: "workflow", RunID: runID, AsyncID: asyncID, AsyncDir: asyncDir}
	attached, err := attachMissionToLaunchResult(*x.mission, res)
	if err != nil {
		warning := "Mission tracking unavailable after launch: " + err.Error()
		details.set("missionWarning", warning)
		if x.explicitMission {
			return text + "\n" + warning, true
		}
		return text, isError
	}
	details.set("missionId", attached.MissionID)
	details.set("missionPath", attached.MissionPath)
	details.set("mission", attached.Mission)
	if len(attached.Content) > 0 {
		text = attached.Content[0].Text
	}
	return text, isError
}

func formatTraceLines(trace []WorkflowScriptTraceEntry, withDuration bool) []workflowTraceLine {
	out := make([]workflowTraceLine, len(trace))
	for i, e := range trace {
		line := fmt.Sprintf("- %s %s: %s", e.Operation, e.Key, e.State)
		if e.RunID != "" {
			line += " (" + e.RunID + ")"
		}
		if withDuration && e.DurationMs != nil {
			line += fmt.Sprintf(" in %dms", *e.DurationMs)
		}
		if e.Warning != "" {
			line += " — " + e.Warning
		}
		out[i] = workflowTraceLine{Line: line, Error: e.Error}
	}
	return out
}

func consoleJSON(c []WorkflowConsoleEntry) []any {
	out := make([]any, len(c))
	for i, e := range c {
		out[i] = jo("level", e.Level, "text", e.Text)
	}
	return out
}

// runForeground blocks the call until the script ends and reports the whole run.
func (x *wfExec) runForeground() (any, error) {
	x.bindRun()
	done, stop := cancellation(x.ctx)
	defer stop()
	runCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	go func() {
		select {
		case <-done:
			cancel(errors.New("This operation was aborted"))
		case <-runCtx.Done():
		}
	}()
	opt := x.scriptOptions(runCtx)
	prog := &wfFgProgress{x: x}
	defer prog.close()
	opt.OnTrace = prog.onTrace
	opt.OnEmit = func(emits []any) error { prog.onEmit(emits); return nil }
	opt.OnHostStep = func(n WorkflowHostStepNode) {
		x.mu.Lock()
		defer x.mu.Unlock()
		if _, ok := x.hostSteps[n.ID]; !ok {
			x.hostStepOrder = append(x.hostStepOrder, n.ID)
		}
		x.hostSteps[n.ID] = hostStepNodeOf(n)
	}
	res, err := RunWorkflowScript(opt)
	if err != nil {
		return x.foregroundFailure(err)
	}
	return x.foregroundSuccess(res)
}

func (x *wfExec) hostStepList() []any {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make([]any, 0, len(x.hostStepOrder))
	for _, id := range x.hostStepOrder {
		out = append(out, x.hostSteps[id].Object())
	}
	return out
}

func (x *wfExec) workflowDetails(value any, hasValue bool, trace []WorkflowScriptTraceEntry, emits []any, console []WorkflowConsoleEntry, failureKind string, warnings []string, receipt *jsObject) *jsObject {
	wf := newObject()
	if hasValue {
		wf.set("value", value)
	}
	wf.set("trace", traceJSON(annotatedTrace(trace, x.preflight)))
	wf.set("emits", emits)
	wf.set("console", consoleJSON(console))
	if failureKind != "" {
		wf.set("failureKind", failureKind)
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
	if receipt != nil {
		wf.set("receipt", receipt)
	}
	return wf
}

func (x *wfExec) head() []string {
	if x.preflight == nil {
		return nil
	}
	return []string{FormatWorkflowPreflight(x.preflight, "")}
}

func (x *wfExec) tail(warnings []string, children []*jsObject) []string {
	var out []string
	if len(warnings) > 0 {
		out = append(out, FormatWorkflowPreflightWarnings(warnings, ""))
	}
	if m := jsTrim(workflowOutputPathMappingSummary(children)); m != "" {
		out = append(out, m)
	}
	return out
}

func (x *wfExec) foregroundSuccess(res WorkflowScriptResult) (any, error) {
	warnings := WorkflowPreflightWarnings(x.preflight, traceLike(res.Trace), true)
	summary, _ := WorkflowChildSummaryOf(WorkflowChildSummaryInput{ParentToolCallID: x.toolCallID, WorkflowRunID: x.workflowRunID, WorkflowState: "completed", InventoryComplete: true, Trace: res.Trace, Children: res.Children})
	receipt, rerr := BuildWorkflowReceipt(WorkflowReceiptInput{WorkflowRunID: x.workflowRunID, State: "complete", Children: res.Children, WorkflowChildren: &summary, HostSteps: x.hostStepList(), Resource: x.resourceObject(), ArgsDigest: x.argsDigest})
	if rerr != nil {
		return nil, sdk.NewToolError(rerr.Error())
	}
	running := workflowRunningChildrenSummary(res.Children)
	head := "Workflow completed."
	if running != "" {
		head = "Workflow dispatch completed; " + running
	}
	script := []string{"Return:\n" + formatWorkflowValue(res.Value)}
	if len(res.Emits) > 0 {
		var e []string
		for _, v := range res.Emits {
			e = append(e, formatWorkflowValue(v))
		}
		script = append(script, "Emitted:\n"+strings.Join(e, "\n"))
	}
	if len(res.Console) > 0 {
		var c []string
		for _, e := range res.Console {
			c = append(c, fmt.Sprintf("[%s] %s", e.Level, e.Text))
		}
		script = append(script, "Console:\n"+strings.Join(c, "\n"))
	}
	text := formatWorkflowResultText(workflowResultTextInput{Head: append(x.head(), head), Script: script, Trace: formatTraceLines(annotatedTrace(res.Trace, x.preflight), true), Tail: x.tail(warnings, res.Children),
		MaxOutput: x.maxOutput, AggregateOutput: x.aggregateOutput, FullResultPath: x.fullResultPath, ProducedChildPaths: x.producedCopy()})
	details := jo("mode", "workflow", "runId", x.workflowRunID, "results", workflowDetailsResults(res.Children))
	if x.preflight != nil {
		details.set("preflight", jsAny(x.preflight))
	}
	details.set("workflowChildren", jsAny(summary))
	details.set("workflow", x.workflowDetails(res.Value, true, res.Trace, res.Emits, res.Console, "", warnings, receipt))
	details.set("chatProgress", jsAny(x.chatProgress))
	details.set("runFanoutBudget", x.fanoutSnapshotObject())
	text, isErr := x.attachMission(text, false, x.workflowRunID, "", "", details)
	return resultOf(text, isErr, details)
}

func (x *wfExec) producedCopy() map[string]bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make(map[string]bool, len(x.produced))
	for k, v := range x.produced {
		out[k] = v
	}
	return out
}

func (x *wfExec) resourceObject() *jsObject {
	if x.resource == nil {
		return nil
	}
	return x.resource.provenanceObject()
}

func (x *wfExec) fanoutSnapshotObject() any {
	snap, err := getRunFanoutBudgetSnapshot(x.fanout)
	if err != nil {
		return nil
	}
	return jo("used", float64(snap.Used), "limit", float64(snap.Limit), "remaining", float64(snap.Remaining))
}

func partialOf(err error) (WorkflowScriptResult, string) {
	var we *WorkflowScriptError
	if errors.As(err, &we) {
		return we.Partial, we.Kind
	}
	return WorkflowScriptResult{}, ""
}

func (x *wfExec) foregroundFailure(err error) (any, error) {
	partial, kind := partialOf(err)
	text := workflowFailureMessage(err, x.workflowRunID, partial.Children)
	warnings := WorkflowPreflightWarnings(x.preflight, traceLike(partial.Trace), true)
	script := []string{"Workflow failed: " + text}
	if len(partial.Emits) > 0 {
		var e []string
		for _, v := range partial.Emits {
			e = append(e, formatWorkflowValue(v))
		}
		script = append(script, "Emitted:\n"+strings.Join(e, "\n"))
	}
	if len(partial.Console) > 0 {
		var c []string
		for _, e := range partial.Console {
			c = append(c, fmt.Sprintf("[%s] %s", e.Level, e.Text))
		}
		script = append(script, "Console:\n"+strings.Join(c, "\n"))
	}
	display := formatWorkflowResultText(workflowResultTextInput{Head: x.head(), Script: script, Trace: formatTraceLines(annotatedTrace(partial.Trace, x.preflight), false), Tail: x.tail(warnings, partial.Children),
		MaxOutput: x.maxOutput, AggregateOutput: x.aggregateOutput, FullResultPath: x.fullResultPath, ProducedChildPaths: x.producedCopy()})
	summary, _ := WorkflowChildSummaryOf(WorkflowChildSummaryInput{ParentToolCallID: x.toolCallID, WorkflowRunID: x.workflowRunID, WorkflowState: "failed", InventoryComplete: true, Trace: partial.Trace, Children: partial.Children})
	outcome := workflowFailureTerminalOutcome(err)
	receipt, rerr := BuildWorkflowReceipt(WorkflowReceiptInput{WorkflowRunID: x.workflowRunID, State: "failed", Children: partial.Children, WorkflowChildren: &summary, TerminalOutcome: outcome, HostSteps: x.hostStepList(), Resource: x.resourceObject(), ArgsDigest: x.argsDigest})
	if rerr != nil {
		return nil, sdk.NewToolError(rerr.Error())
	}
	details := jo("mode", "workflow", "runId", x.workflowRunID, "results", workflowDetailsResults(partial.Children))
	if x.preflight != nil {
		details.set("preflight", jsAny(x.preflight))
	}
	details.set("workflowChildren", jsAny(summary))
	details.set("workflow", x.workflowDetails(nil, false, partial.Trace, partial.Emits, partial.Console, kind, warnings, receipt))
	details.set("chatProgress", jsAny(x.chatProgress))
	details.set("runFanoutBudget", x.fanoutSnapshotObject())
	display, _ = x.attachMission(display, true, x.workflowRunID, "", "", details)
	return resultOf(display, true, details)
}

// wfFgProgress streams the live state of a foreground workflow to the tool call (the original's sendWorkflowProgress: at most one
// update per 100 ms, the last state always delivered). chatProgress picks how a view draws it, not whether it is sent.
type wfFgProgress struct {
	x      *wfExec
	mu     sync.Mutex
	trace  []WorkflowScriptTraceEntry
	emits  []any
	last   time.Time
	timer  *time.Timer
	closed bool
}

func (p *wfFgProgress) onTrace(t []WorkflowScriptTraceEntry) {
	p.mu.Lock()
	p.trace = t
	p.mu.Unlock()
	p.schedule()
}

func (p *wfFgProgress) onEmit(e []any) {
	p.mu.Lock()
	p.emits = e
	p.mu.Unlock()
	p.schedule()
}

func (p *wfFgProgress) schedule() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	delay := 100*time.Millisecond - time.Since(p.last)
	if delay <= 0 {
		p.mu.Unlock()
		p.send()
		return
	}
	if p.timer == nil {
		p.timer = time.AfterFunc(delay, p.send)
	}
	p.mu.Unlock()
}

func (p *wfFgProgress) send() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.timer = nil
	p.last = time.Now()
	trace, emits := p.trace, p.emits
	p.mu.Unlock()
	x := p.x
	warnings := WorkflowPreflightWarnings(x.preflight, traceLike(trace), false)
	summary, _ := WorkflowChildSummaryOf(WorkflowChildSummaryInput{ParentToolCallID: x.toolCallID, WorkflowRunID: x.workflowRunID, WorkflowState: "running", InventoryComplete: false, Trace: trace})
	details := jo("mode", "workflow", "runId", x.workflowRunID, "results", []any{})
	if x.preflight != nil {
		details.set("preflight", jsAny(x.preflight))
	}
	details.set("workflowChildren", jsAny(summary))
	wf := newObject()
	wf.set("trace", traceJSON(annotatedTrace(trace, x.preflight)))
	wf.set("emits", append([]any{}, emits...))
	wf.set("console", []any{})
	if len(warnings) > 0 {
		wf.set("preflightWarnings", anyStrings(warnings))
	}
	details.set("workflow", wf)
	details.set("chatProgress", jsAny(x.chatProgress))
	_ = x.ctx.OnUpdate(sdk.ToolResult{Content: "Workflow running.", Details: details})
}

func (p *wfFgProgress) close() {
	p.mu.Lock()
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
	p.mu.Unlock()
}
