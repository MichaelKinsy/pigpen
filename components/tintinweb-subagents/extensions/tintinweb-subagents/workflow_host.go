package tintinweb_subagents

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Binds a workflow run to the agent manager: everything the script can reach through agent(), resume and gate ends up
// here and nowhere else. Four mappings carry most of the weight (see the original's header): the runtime's own
// wf-agent-N handles to the manager's record ids; agent type and model through the same dispatch the Agent tool uses;
// a failure to start as a failed agent, not a failed run; and when a gate runs (for an isolated child, inside the
// child's own settle, before its worktree is removed). upstream: src/workflow/host.ts.

// defaultGateTimeout bounds a gate command: generous, because a gate is routinely a test suite, but not unbounded.
// upstream: DEFAULT_GATE_TIMEOUT_MS.
const defaultGateTimeout = 10 * time.Minute

// workflowInvocation is the effective configuration of a child, written by the manager once its session exists
// (AgentRecord.invocation, #168).
type workflowInvocation struct {
	ModelName, ModelID                          string
	Thinking, RequestedThinking, RequestedModel string
}

// workflowRecord is the part of an agent record a workflow reads. upstream: AgentRecord.
type workflowRecord struct {
	ID     string
	Status string
	Result string
	Error  string
	// StructuredJSON is the schema'd payload, when the child produced one.
	StructuredJSON    string
	StructuredRetried bool
	ToolUses          int
	// LifetimeTokens is the lifetime total (input + output + cache writes); OutputTokens the output alone.
	LifetimeTokens, OutputTokens int
	// WorktreePath is where an isolated child worked.
	WorktreePath string
	// Invocation is nil until a session has reported a model.
	Invocation *workflowInvocation
}

// workflowSpawnOptions are the options of a workflow child's spawn. upstream: the options of AgentManager.spawnAndWait.
type workflowSpawnOptions struct {
	// Ctx is the run's: killing the workflow kills its children.
	Ctx         context.Context
	Description string
	// WorkflowID stamps the child as the workflow's: out of the session's concurrency pool, off the fleet list, widget
	// and menus, no top-level events. Empty for a host driven without a run.
	WorkflowID string
	// Model is the model to run on ("provider/id"); empty leaves it to the agent definition and the parent.
	Model string
	// ThinkingLevel is the effort the script asked for; empty leaves it to the definition and the parent.
	ThinkingLevel string
	// Invocation seeds the record with the REQUEST (the thinking level), so the manager can compare it with the
	// effective one when the session exists.
	Invocation workflowInvocation
	// OnSessionCreated fires once the child's session exists: where the model and the clamped level become knowable.
	OnSessionCreated func()
	StructuredOutput *workflowCompiledSchema
	Isolation        string
	RootSessionID    string
	// OnBeforeWorktreeCleanup runs inside the child's own settle, while its worktree still exists; nil for an ungated agent.
	OnBeforeWorktreeCleanup func(worktreePath string) error
}

// workflowManager is the agent manager as a workflow sees it. upstream: AgentManager.
type workflowManager interface {
	// SpawnAndWait starts a child and returns when it has settled. onSpawned is called with the record id as soon as
	// there is one. An error is a child that never ran (a strict isolation failure, a refusal).
	SpawnAndWait(typ, prompt string, opts workflowSpawnOptions, onSpawned func(id string)) (workflowRecord, error)
	Abort(id string)
	// Resume continues a finished child's session; ok is false when no session is left to resume.
	Resume(ctx context.Context, id, prompt string) (rec workflowRecord, ok bool, err error)
	GetRecord(id string) (workflowRecord, bool)
}

// workflowExecResult is pi.exec's result.
type workflowExecResult struct {
	Stdout, Stderr string
	Code           int
	Killed         bool
}

// workflowHostDeps are the host's dependencies. upstream: WorkflowHostOptions.
type workflowHostDeps struct {
	Manager workflowManager
	// Exec runs a command in cwd, killed after timeout (pi.exec).
	Exec func(ctx context.Context, command string, args []string, cwd string, timeout time.Duration) (workflowExecResult, error)
	// Cwd is the session's directory, where a non-isolated child works.
	Cwd string
	// Notify raises a notification (the scopeModels warnings).
	Notify func(message, level string)
	// Registry resolves a model the script named; nil passes the string through for the child to resolve.
	Registry workflowModelRegistry
	// ParentModel is the session's model ("provider/id"), the floor of the precedence; empty when unknown.
	ParentModel func() *workflowModel
	// CheckScope is the scopeModels policy; nil means scoping is off.
	CheckScope func(workflowScopeInput) workflowScopeVerdict
	// Signal is the run's abort signal, so killing the workflow kills its children.
	Signal        context.Context
	RootSessionID string
	// WorkflowID is the run id every child is stamped with.
	WorkflowID  string
	GateTimeout time.Duration
}

// workflowAgentHost is the WorkflowHost of one run.
type workflowAgentHost struct {
	deps workflowHostDeps
	// records maps the runtime's agent id to the manager record it spawned. Never pruned mid-run: resume reaches back
	// to a child that has already finished.
	records map[string]string
	// warnedScope are scopeModels warnings already raised, so a fan-out that pins one out-of-scope agent file raises
	// one notification rather than one per child.
	warnedScope map[string]bool
	mu          sync.Mutex
}

// newWorkflowHost is createWorkflowHost.
func newWorkflowHost(deps workflowHostDeps) *workflowAgentHost {
	return &workflowAgentHost{deps: deps, records: map[string]string{}, warnedScope: map[string]bool{}}
}

// wfChildSucceeded: the child itself succeeded, the same condition toSpawnResult turns into ok.
func wfChildSucceeded(r workflowRecord) bool {
	return r.Status == statusCompleted || r.Status == statusSteered
}

// wfChildCwd is where the child worked, when that directory still exists: the manager removes an isolated child's
// copy before the spawn resolves, so handing a stale path to a command would fail every gated worktree agent with a
// spawn error instead of a test result. upstream: childCwd.
func wfChildCwd(r workflowRecord) string {
	if r.WorktreePath == "" {
		return ""
	}
	if _, err := os.Stat(r.WorktreePath); err != nil {
		return ""
	}
	return r.WorktreePath
}

// wfResolvedInfo is the effective-configuration half of a record in the runtime's shape; ok is false until a session
// has reported a model. upstream: resolvedInfo.
func wfResolvedInfo(r workflowRecord, found bool) (workflowResolvedInfo, bool) {
	if !found || r.Invocation == nil || r.Invocation.ModelName == "" {
		return workflowResolvedInfo{}, false
	}
	i := r.Invocation
	return workflowResolvedInfo{ModelName: i.ModelName, ModelID: i.ModelID, Thinking: i.Thinking, RequestedThinking: i.RequestedThinking, RequestedModel: i.RequestedModel}, true
}

// wfToSpawnResult translates a settled record into what the script sees. upstream: toSpawnResult.
func wfToSpawnResult(r workflowRecord) workflowSpawnResult {
	var res workflowSpawnResult
	// Reported separately from the lifetime total: the script's budget counts output tokens, as Claude Code's does.
	res.OutputTokens = r.OutputTokens
	if r.LifetimeTokens > 0 {
		res.Tokens = &r.LifetimeTokens
	}
	if r.ToolUses > 0 {
		res.ToolCalls = &r.ToolUses
	}
	res.Cwd = wfChildCwd(r)
	if wfChildSucceeded(r) {
		res.OK = true
		// The schema'd payload when there is one: result is prose, and for a worktree child it has the branch note appended.
		res.Text = r.Result
		if r.StructuredJSON != "" {
			res.Text = r.StructuredJSON
		}
		res.StructuredRetried = r.StructuredRetried
		return res
	}
	// "stopped" is someone reaching in and stopping this child (/agents, the fleet list, a workflow abort): the same
	// thing the dialog's skip action means, so it renders as skipped rather than failed.
	if r.Status == statusStopped {
		res.Skipped, res.Error = true, r.Error
		if res.Error == "" {
			res.Error = "Stopped."
		}
		return res
	}
	res.Error = r.Error
	if res.Error == "" {
		res.Error = "Agent " + r.Status + "."
	}
	return res
}

// wfGateShell is the shell a gate command runs in, mirroring how a user would type it.
func wfGateShell() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "sh", "-c"
}

// executeGate runs a gate command in cwd: the only place a gate is executed, so "gate passed" is decided from one
// behaviour whichever route the command took. upstream: executeGate.
func (h *workflowAgentHost) executeGate(ctx context.Context, command, cwd string) (workflowGateResult, error) {
	timeout := h.deps.GateTimeout
	if timeout == 0 {
		timeout = defaultGateTimeout
	}
	shell, flag := wfGateShell()
	res, err := h.deps.Exec(ctx, shell, []string{flag, command}, cwd, timeout)
	if err != nil {
		return workflowGateResult{}, err
	}
	var parts []string
	for _, s := range []string{res.Stdout, res.Stderr} {
		if t := strings.TrimSpace(s); t != "" {
			parts = append(parts, t)
		}
	}
	output := strings.Join(parts, "\n")
	// pi.exec reports a timeout as killed with exit code 0, so the code alone would read a killed gate as a passing one.
	if res.Killed {
		if output == "" {
			output = "Gate command timed out: " + command
		}
		return workflowGateResult{Output: output}, nil
	}
	return workflowGateResult{OK: res.Code == 0, Output: output}, nil
}

func (h *workflowAgentHost) record(agentID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id, ok := h.records[agentID]
	return id, ok
}

// SpawnAgent maps a request onto a manager spawn. upstream: spawnAgent.
func (h *workflowAgentHost) SpawnAgent(req *workflowSpawnRequest) (workflowSpawnResult, error) {
	d := h.deps
	onResolved := req.OnResolved
	if onResolved == nil {
		onResolved = func(workflowResolvedInfo) {}
	}
	dispatch := resolveSpawnType(req.AgentType)
	if !dispatch.OK {
		return workflowSpawnResult{Error: dispatch.Message}, nil
	}
	// Same precedence as the Agent tool: the caller's model wins, the agent definition's is next, and the parent's is
	// the floor. A model the script named and cannot be resolved is an error; one the definition named falls back to the
	// parent silently, because the script never asked for it.
	var model *workflowModel
	if d.ParentModel != nil {
		model = d.ParentModel()
	}
	modelRef := ""
	config := getAgentConfig(dispatch.Type)
	modelInput := req.Model
	if modelInput == "" && config != nil {
		modelInput = config.Model
	}
	if modelInput != "" {
		if d.Registry != nil {
			resolved, errText := wfResolveModel(modelInput, d.Registry)
			if errText != "" {
				if req.Model != "" {
					return workflowSpawnResult{Error: errText}, nil
				}
			} else {
				model = resolved
			}
		} else if req.Model != "" {
			// No registry to resolve against: the child resolves the script's spelling itself.
			modelRef = req.Model
		}
	}
	if model != nil {
		modelRef = model.Provider + "/" + model.ID
	}

	// Same scopeModels policy as the Agent tool and the nested delegation tools: a script's agent({ model }) is a
	// runtime LLM choice, and the script is written by the model, so it must not reach a model the user's
	// enabledModels list excludes. callerSupplied keys off req.Model and NOT modelInput: the latter has already absorbed
	// the agent file's own model:, which is user-authored config and so earns the warn-and-proceed branch.
	if d.CheckScope != nil {
		label := dispatch.Type
		if config != nil && config.DisplayName != "" {
			label = config.DisplayName
		}
		verdict := d.CheckScope(workflowScopeInput{Model: model, CallerSupplied: req.Model != "", AgentLabel: label, ModelInput: modelInput})
		// This agent's failure, not the run's: the script sees null and its siblings carry on.
		if verdict.Kind == "error" {
			return workflowSpawnResult{Error: verdict.Message}, nil
		}
		if verdict.Kind == "warn" {
			h.mu.Lock()
			first := !h.warnedScope[verdict.Message]
			h.warnedScope[verdict.Message] = true
			h.mu.Unlock()
			if first && d.Notify != nil {
				d.Notify(verdict.Message, "warning")
			}
		}
	}

	// gate is the verdict set only if the hook below actually ran the command. Its presence is what stops the runtime
	// running the gate a second time, so it is set on the failure route too: a gate we tried and could not complete is
	// a failed gate, never an un-run one that then re-runs against the wrong tree.
	var gate *workflowGateResult
	var spawnedID string
	sessionReady := false
	// reportResolved hands the child's EFFECTIVE configuration back to the run. Called from both the session hook and
	// the spawn hook, because their order is not guaranteed; each fires once, so the first call finds a half missing.
	reportResolved := func() {
		if !sessionReady || spawnedID == "" {
			return
		}
		rec, found := d.Manager.GetRecord(spawnedID)
		if info, ok := wfResolvedInfo(rec, found); ok {
			onResolved(info)
		}
	}
	opts := workflowSpawnOptions{Ctx: d.Signal, Description: req.Label, WorkflowID: d.WorkflowID, Model: modelRef,
		ThinkingLevel: req.Effort, Invocation: workflowInvocation{Thinking: req.Effort}, StructuredOutput: req.Schema,
		Isolation: req.Isolation, RootSessionID: d.RootSessionID,
		OnSessionCreated: func() { sessionReady = true; reportResolved() }}
	if opts.Ctx == nil {
		opts.Ctx = req.Ctx
	}
	if command := req.Gate; command != "" {
		// Verify the child's work while its worktree still exists: the manager destroys that copy inside the child's own
		// settle, so this is the last (and only) moment at which `npm test` can mean "the code this child just wrote".
		opts.OnBeforeWorktreeCleanup = func(worktreePath string) error {
			// A failed child's gate is never consulted: running it would be pure cost.
			if spawnedID == "" {
				return nil
			}
			if rec, found := d.Manager.GetRecord(spawnedID); !found || !wfChildSucceeded(rec) {
				return nil
			}
			g, err := h.executeGate(req.Ctx, command, worktreePath)
			if err != nil {
				g = workflowGateResult{Output: err.Error()}
			}
			gate = &g
			return nil
		}
	}
	rec, err := d.Manager.SpawnAndWait(dispatch.Type, req.Prompt, opts, func(id string) {
		spawnedID = id
		h.mu.Lock()
		h.records[req.AgentID] = id
		h.mu.Unlock()
		// Ahead of reportResolved, and not folded into it: that one waits for the child's session so it can name the
		// effective model, while the record id is known here and is what the inspector opens a conversation on.
		onResolved(workflowResolvedInfo{RecordID: id})
		reportResolved()
	})
	if err != nil {
		if wfIsRefusal(err) {
			return workflowSpawnResult{}, err
		}
		// Strict worktree isolation rejects out of the start: the child never ran. That is this agent's failure, not the
		// run's: the script sees null and its siblings carry on.
		return workflowSpawnResult{Error: err.Error()}, nil
	}
	res := wfToSpawnResult(rec)
	res.Gate = gate
	return res, nil
}

// AbortAgent aborts the manager record the runtime's agent id stands for. upstream: abortAgent.
func (h *workflowAgentHost) AbortAgent(agentID string) {
	// Nothing to abort before the manager has issued an id: the child is still in startup, and the run's own signal reaches it there.
	if id, ok := h.record(agentID); ok {
		h.deps.Manager.Abort(id)
	}
}

// ResumeAgent continues the same manager record, long after that agent settled. upstream: resumeAgent.
func (h *workflowAgentHost) ResumeAgent(ctx context.Context, agentID, prompt string, onResolved func(workflowResolvedInfo)) (workflowSpawnResult, error) {
	id, ok := h.record(agentID)
	if !ok {
		return workflowSpawnResult{Error: `Cannot resume "` + agentID + `" — it never started.`}, nil
	}
	rec, found, err := h.deps.Manager.Resume(ctx, id, prompt)
	if err != nil {
		if wfIsRefusal(err) {
			return workflowSpawnResult{}, err
		}
		return workflowSpawnResult{Error: err.Error()}, nil
	}
	if !found {
		return workflowSpawnResult{Error: "Agent " + id + " has no session left to resume — records are dropped ten minutes after they finish."}, nil
	}
	// The resumed row is built from scratch, so it has to be told the same thing the first one was.
	if onResolved != nil {
		onResolved(workflowResolvedInfo{RecordID: id})
		if info, ok := wfResolvedInfo(rec, true); ok {
			onResolved(info)
		}
	}
	return wfToSpawnResult(rec), nil
}

// LoadWorkflow resolves a nested workflow() reference. The runtime decides whether what comes back is a workflow;
// this only finds it. upstream: loadWorkflow.
func (h *workflowAgentHost) LoadWorkflow(ref workflowRef) (workflowScriptSource, error) {
	r := resolveWorkflowSource(ref, h.deps.Cwd)
	return workflowScriptSource{OK: r.OK, Script: r.Script, Path: r.Path, Message: r.Message}, nil
}

// RunGate is reached only for a gate the spawn did not already run: a child with no worktree of its own. cwd is the
// child's worktree when it had one and it survived; otherwise the session's own directory. upstream: runGate.
func (h *workflowAgentHost) RunGate(ctx context.Context, command, _ string, cwd string) (workflowGateResult, error) {
	if cwd == "" {
		cwd = h.deps.Cwd
	}
	return h.executeGate(ctx, command, cwd)
}

// workflowRefusalError is a feature this port does not do yet, asked for by a script (a worktree, a schema, a resume
// of a finished session). Unlike an agent's own failure it fails the RUN, with the refusal text: a script must never
// be handed a null that means "not supported" and carry on as if it had run.
type workflowRefusalError struct{ feature string }

func (e *workflowRefusalError) Error() string { return refusalText(e.feature) }

func wfIsRefusal(err error) bool {
	var r *workflowRefusalError
	return errors.As(err, &r)
}
