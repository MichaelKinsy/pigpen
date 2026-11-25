package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/eval/agent-bridge.ts,
// packages/coding-agent/src/eval/workpool-bridge.ts,
// packages/coding-agent/src/task/workpool.ts,
// packages/coding-agent/src/eval/js/shared/prelude.txt,
// packages/coding-agent/src/eval/py/prelude.py,
// packages/coding-agent/src/prompts/tools/eval-agents.md

// WorkPoolItem tracks one user-supplied item in a WorkPool.
// upstream: packages/coding-agent/src/task/workpool.ts:25
type WorkPoolItem struct {
	ID      string
	Seq     int
	Text    string
	AgentID string
	BatchID string
	Status  string // "queued", "running", "completed", "failed", "cancelled"
}

// WorkPoolBatch tracks one worker turn in a WorkPool.
type WorkPoolBatch struct {
	ID        string
	AgentID   string
	Items     []*WorkPoolItem
	StartedAt time.Time
	Status    string // "running", "completed", "failed", "cancelled"
	Output    string
}

// WorkPoolAgent tracks one worker inside a WorkPool.
type WorkPoolAgent struct {
	ID    string
	Index int
	State string // "running", "idle", "dead"
	Turns int
	Queue []*WorkPoolItem
}

// WorkPool dispatches pushed string items across worker subagents under a
// single aggregate `jobs.Job` that drains and closes once all items complete.
// upstream: packages/coding-agent/src/task/workpool.ts:111
type WorkPool struct {
	mu            sync.Mutex
	rt            *Runtime
	sdkCtx        sdk.Context
	sessKey       string
	parentAgentID string
	taskDepth     int
	planMode      bool
	cwd           string
	artifactsDir  string
	name          string
	agentDef      AgentDefinition
	contextText   string
	modelSel      []string
	limit         int
	closed        bool
	nextSeq       int
	nextWorker    int
	items         []*WorkPoolItem
	batches       []*WorkPoolBatch
	workers       []*WorkPoolAgent
	poolStarted   bool
	poolJob       *AgentJob
	poolCtx       context.Context
	poolCancel    context.CancelFunc
	drainCh       chan struct{}
	drainOnce     sync.Once
	wg            sync.WaitGroup
}

func (rt *Runtime) newWorkPoolLocked(ctx sdk.Context, sessKey, parentAgentID string, taskDepth int, planMode bool, cwd, artifactsDir, name string, ag AgentDefinition, contextText string, modelSel []string) *WorkPool {
	lim := rt.Config.MaxConcurrency
	if lim <= 0 {
		lim = 32
	}
	poolCtx, poolCancel := context.WithCancel(context.Background())
	rt.sessionCancels[sessKey] = append(rt.sessionCancels[sessKey], poolCancel)
	return &WorkPool{
		rt:            rt,
		sdkCtx:        ctx,
		sessKey:       sessKey,
		parentAgentID: parentAgentID,
		taskDepth:     taskDepth,
		planMode:      planMode,
		cwd:           cwd,
		artifactsDir:  artifactsDir,
		name:          name,
		agentDef:      ag,
		contextText:   contextText,
		modelSel:      modelSel,
		limit:         lim,
		nextSeq:       1,
		nextWorker:    1,
		poolCtx:       poolCtx,
		poolCancel:    poolCancel,
		drainCh:       make(chan struct{}),
	}
}

// Push queues string items into the pool and launches the aggregate pool job on
// the first non-empty push.
func (wp *WorkPool) Push(texts []string) ([]string, error) {
	wp.mu.Lock()
	if wp.closed {
		wp.mu.Unlock()
		return nil, fmt.Errorf("workpool %s is closed", wp.name)
	}
	if len(texts) == 0 {
		wp.mu.Unlock()
		return []string{}, nil
	}
	ids := make([]string, 0, len(texts))
	added := make([]*WorkPoolItem, 0, len(texts))
	for _, txt := range texts {
		seq := wp.nextSeq
		wp.nextSeq++
		item := &WorkPoolItem{
			ID:     fmt.Sprintf("%s#%d", wp.name, seq),
			Seq:    seq,
			Text:   txt,
			Status: "queued",
		}
		wp.items = append(wp.items, item)
		added = append(added, item)
		ids = append(ids, item.ID)
	}
	if !wp.poolStarted {
		wp.poolStarted = true
		wp.poolJob = NewAgentJob(wp.name, wp.name, wp.poolCancel, AgentProgress{
			ID:          wp.name,
			Agent:       wp.agentDef.Name,
			AgentSource: wp.agentDef.Source,
			Status:      "running",
		})
		jobs.Add(wp.sessKey, wp.poolJob)
		go wp.watchAggregateJob()
	}
	wp.mu.Unlock()

	for _, it := range added {
		wp.dispatch(it)
	}
	return ids, nil
}

func (wp *WorkPool) dispatch(item *WorkPoolItem) {
	wp.mu.Lock()
	if wp.closed || item.Status != "queued" {
		wp.mu.Unlock()
		return
	}
	// Spawn one worker per queued item up to limit; otherwise queue on a running worker.
	if len(wp.workers) < wp.limit {
		idx := wp.nextWorker
		wp.nextWorker++
		outMgr := wp.rt.outputManager(wp.sessKey, wp.artifactsDir, wp.rt.EffectiveConfig(wp.sdkCtx).CrossInstance)
		workerID := outMgr.Allocate(fmt.Sprintf("%s-%d", wp.name, idx))
		worker := &WorkPoolAgent{
			ID:    workerID,
			Index: idx,
			State: "running",
			Queue: []*WorkPoolItem{item},
		}
		item.AgentID = workerID
		wp.workers = append(wp.workers, worker)
		wp.startWorkerBatchLocked(worker)
		wp.mu.Unlock()
		return
	}
	// Find an idle worker first, else round-robin onto a running worker.
	for _, w := range wp.workers {
		if w.State == "idle" {
			item.AgentID = w.ID
			w.Queue = append(w.Queue, item)
			wp.startWorkerBatchLocked(w)
			wp.mu.Unlock()
			return
		}
	}
	target := wp.workers[(item.Seq-1)%len(wp.workers)]
	item.AgentID = target.ID
	target.Queue = append(target.Queue, item)
	wp.mu.Unlock()
}

func (wp *WorkPool) startWorkerBatchLocked(worker *WorkPoolAgent) {
	if len(worker.Queue) == 0 {
		worker.State = "idle"
		wp.checkDrainedLocked()
		return
	}
	batchItems := worker.Queue
	worker.Queue = nil
	worker.State = "running"
	batchID := fmt.Sprintf("%s-b%d", worker.ID, worker.Turns+1)
	batch := &WorkPoolBatch{
		ID:        batchID,
		AgentID:   worker.ID,
		Items:     batchItems,
		StartedAt: time.Now(),
		Status:    "running",
	}
	for _, it := range batchItems {
		it.Status = "running"
		it.AgentID = worker.ID
		it.BatchID = batchID
	}
	wp.batches = append(wp.batches, batch)

	yieldItems := make([]WorkPoolYieldItem, len(batchItems))
	var msgBuilder strings.Builder
	fmt.Fprintf(&msgBuilder, "<workpool pool=%q batch=%q>\n", wp.name, batch.ID)
	fmt.Fprintf(&msgBuilder, "You are a worker in pool `%s`. Complete every item below in order.\n", wp.name)
	msgBuilder.WriteString("After EACH item, call `yield` once as `{ key, data }` or `{ key, error }`:\n")
	msgBuilder.WriteString("- `key`: the item's 1-based number.\n- `data`: its self-contained outcome/evidence value.\n\n")
	msgBuilder.WriteString("The tool tells you which keys remain; the final key ends the turn automatically.\nNEVER combine several items into one yield.\n")
	for i, it := range batchItems {
		yieldItems[i] = WorkPoolYieldItem{ID: it.ID, Index: i + 1}
		fmt.Fprintf(&msgBuilder, "\n## Item %d\n%s\n", i+1, it.Text)
	}
	msgBuilder.WriteString("</workpool>")
	batchPrompt := msgBuilder.String()
	outSchema := BuildWorkPoolOutputSchema(yieldItems)

	wp.wg.Add(1)
	go func() {
		defer wp.wg.Done()
		var sr SingleResult
		if err := wp.rt.sem.Acquire(wp.poolCtx); err != nil {
			sr = SingleResult{
				ID:          worker.ID,
				Agent:       wp.agentDef.Name,
				AgentSource: wp.agentDef.Source,
				ExitCode:    1,
				Aborted:     true,
				AbortReason: "Pool cancelled",
				Error:       "Pool cancelled",
			}
		} else {
			sr = RunSubagent(wp.poolCtx, wp.rt.Registry, RunRequest{
				SDK:             wp.rt.sessionContextFor(wp.sessKey, wp.sdkCtx),
				ParentSessionID: wp.sessKey,
				ParentAgentID:   wp.parentAgentID,
				Cwd:             wp.cwd,
				TaskDepth:       wp.taskDepth,
				Index:           worker.Index - 1,
				ID:              worker.ID,
				Agent:           wp.agentDef,
				Assignment:      batchPrompt,
				Context:         wp.contextText,
				OutputSchema:    outSchema,
				SchemaMode:      "strict",
				SchemaSource:    "caller",
				ModelOverride:   wp.modelSel,
				MaxEffort:       wp.rt.Config.MaxEffort,
				ArtifactsDir:    wp.artifactsDir,
				MaxRuntimeMs:    wp.rt.Config.MaxRuntimeMs,
				SoftBudget:      wp.rt.Config.SoftRequestBudget,
				MaxDepth:        wp.rt.Config.MaxRecursionDepth,
				IRCEnabled:      !wp.planMode,
				KeepAlive:       false,
				WorkPoolItems:   yieldItems,
			})
			wp.rt.sem.Release()
		}

		wp.mu.Lock()
		defer wp.mu.Unlock()
		switch {
		case sr.Aborted:
			batch.Status = "cancelled"
		case sr.ExitCode != 0 || sr.Error != "":
			batch.Status = "failed"
		default:
			batch.Status = "completed"
		}
		batch.Output = sr.Output
		for _, it := range batch.Items {
			it.Status = batch.Status
		}
		worker.Turns++
		if !wp.closed && len(worker.Queue) > 0 {
			wp.startWorkerBatchLocked(worker)
		} else {
			worker.State = "idle"
			wp.checkDrainedLocked()
		}
	}()
}

func (wp *WorkPool) checkDrainedLocked() {
	for _, it := range wp.items {
		if it.Status == "queued" || it.Status == "running" {
			return
		}
	}
	wp.drainOnce.Do(func() { close(wp.drainCh) })
}

func (wp *WorkPool) watchAggregateJob() {
	select {
	case <-wp.drainCh:
	case <-wp.poolCtx.Done():
		wp.Close()
	}
	wp.wg.Wait()

	wp.mu.Lock()
	wp.closed = true
	agg := wp.renderAggregateLocked()
	job := wp.poolJob
	cancelled := wp.poolCtx.Err() != nil
	anyFailed := false
	for _, it := range wp.items {
		if it.Status == "failed" {
			anyFailed = true
		}
	}
	wp.mu.Unlock()

	if job != nil {
		status := "completed"
		if cancelled {
			status = "cancelled"
		} else if anyFailed {
			status = "failed"
		}
		job.finish(status, agg, "", nil, nil, false, nil)
	}
}

func (wp *WorkPool) renderAggregateLocked() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pool `%s` completed (%d item(s), %d batch(es)).\n", wp.name, len(wp.items), len(wp.batches))
	for _, batch := range wp.batches {
		fmt.Fprintf(&b, "\n## %s · agent `%s` · %s\n", batch.ID, batch.AgentID, batch.Status)
		for _, it := range batch.Items {
			fmt.Fprintf(&b, "- [%s] %s — %s\n", it.ID, it.Status, OneLineLabel(it.Text))
		}
		if out := strings.TrimSpace(batch.Output); out != "" {
			b.WriteByte('\n')
			b.WriteString(out)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "Transcript: history://%s · full output: agent://%s\n", batch.AgentID, batch.AgentID)
	}
	b.WriteString("\nPool queue drained.")
	return b.String()
}

// Status returns a snapshot matching omp's WorkPoolStatus.
func (wp *WorkPool) Status() map[string]any {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	counts := map[string]int{"queued": 0, "running": 0, "completed": 0, "failed": 0, "cancelled": 0}
	for _, it := range wp.items {
		counts[it.Status]++
	}
	agents := make([]map[string]any, len(wp.workers))
	for i, w := range wp.workers {
		agents[i] = map[string]any{
			"id":     w.ID,
			"state":  w.State,
			"queued": len(w.Queue),
			"turns":  w.Turns,
		}
	}
	return map[string]any{
		"name":        wp.name,
		"agent":       wp.agentDef.Name,
		"limit":       wp.limit,
		"closed":      wp.closed,
		"freshAgents": false,
		"agents":      agents,
		"items":       counts,
		"batches":     len(wp.batches),
	}
}

// Peek returns non-consuming batch snapshots matching omp's WorkPoolPeekResult.
func (wp *WorkPool) Peek() map[string]any {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	batches := make([]map[string]any, len(wp.batches))
	for i, b := range wp.batches {
		itemIDs := make([]string, len(b.Items))
		for j, it := range b.Items {
			itemIDs[j] = it.ID
		}
		entry := map[string]any{
			"id":     b.ID,
			"agent":  b.AgentID,
			"items":  itemIDs,
			"status": b.Status,
		}
		if b.Output != "" {
			entry["output"] = b.Output
		}
		batches[i] = entry
	}
	pending := 0
	for _, it := range wp.items {
		if it.Status == "queued" || it.Status == "running" {
			pending++
		}
	}
	return map[string]any{
		"batches": batches,
		"pending": pending,
	}
}

// Close stops accepting work and cancels queued items not yet assigned to a turn.
func (wp *WorkPool) Close() map[string]any {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	wp.closed = true
	dropped := []string{}
	for _, it := range wp.items {
		if it.Status == "queued" {
			it.Status = "cancelled"
			dropped = append(dropped, it.ID)
		}
	}
	for _, w := range wp.workers {
		w.Queue = nil
	}
	wp.checkDrainedLocked()
	return map[string]any{"dropped": dropped}
}

// ---------------------------------------------------------------------------
// Eval preludes: `agent` and `workpool`
// ---------------------------------------------------------------------------

// RenderEvalAgentsDoc returns omp's `prompts/tools/eval-agents.md` rendered for
// default settings (py+js, defaultAgent="task").
func RenderEvalAgentsDoc() string {
	return strings.TrimSpace(`
` + "```" + `
agent(prompt, *, agent?="task", label?=None, schema?=None, schema_mode?=None, isolated?=None, apply?=None, merge?=None) → AgentHandle
  Spawns a background subagent and returns immediately.
  - ` + "`agent`" + ` selects a discovered agent; omit it to use ` + "`task`" + `.
  - Handle: ` + "`.id`" + `, ` + "`.handle`" + ` ("agent://<id>"), ` + "`.status`" + `, ` + "`.done()`" + `, ` + "`.wait(timeout?)`" + ` → final text (parsed with ` + "`schema`" + `),
    ` + "`.send(message)`" + `, ` + "`.cancel()`" + `, ` + "`.output()`" + `.
  - Unwaited results auto-deliver like async jobs.
  - ` + "`schema`" + ` overrides agent/session schemas.
  - ` + "`schema_mode`" + `: "permissive" warns after retries, "strict" fails; omitted → session setting, else "permissive".
  - Python: call without ` + "`await`" + `; ` + "`await agent(…)`" + ` returns the final result, not a handle (` + "`await h`" + ` = ` + "`h.wait()`" + `).
  - JS: ONE trailing object — ` + "`h = await agent(prompt, { agent, label, schema, schemaMode })`" + `.
    - Without ` + "`await agent(…)`" + `, ` + "`.id`" + `/` + "`.handle`" + ` are undefined.
    - Every handle method returns a Promise; ` + "`.status`" + ` is ` + "`await h.status()`" + `.
workpool(agent?=None, *, name?=None, context?=None) → WorkPool
  Keep-alive worker pool for a batch of independent items.
  - ` + "`.push(*items)`" + ` (strings) → item ids; ` + "`.status()`" + `; ` + "`.peek()`" + `; ` + "`.close()`" + `.
  - Pool name = async job id; results auto-deliver.
  - The first full drain closes the pool: push the whole batch while it is active; next wave → new pool.
  - Completely blocked? Leave ` + "`eval`" + ` and call the ` + "`wait`" + ` tool (not kernel ` + "`wait()`" + `); NEVER poll.
  - JS: ` + "`pool = await workpool(agent, { name, context })`" + `; ` + "`await`" + ` each pool method.
` + "```" + `

The cell ` + "`timeout`" + ` counts only the cell's own work, not waits on ` + "`agent()`" + `/` + "`wait()`" + `/` + "`tool.task(…)`" + `, so a subagent can outlast it.
`)
}

// AgentPrelude returns the host-side `eval.Prelude` definition for `agent`.
// omp's embedded Python and JS eval preludes already define the `agent()` global
// and `AgentHandle` class and forward calls to the `__agent__` bridge, which
// `internal/eval` routes directly to `eval.LookupPrelude("agent").Invoke`.
// JavaScript, Python, and Exports are intentionally left empty so the eval
// kernels do not inject duplicate or conflicting snippets.
// upstream: packages/coding-agent/src/eval/agent-bridge.ts, handle-bridge.ts
func (rt *Runtime) AgentPrelude() eval.Prelude {
	return eval.Prelude{
		Name:          "agent",
		Documentation: RenderEvalAgentsDoc,
		Summary:       "background subagents, DAG waves",
		Approval:      "exec",
		Invoke:        rt.invokeAgentPrelude,
	}
}

// WorkpoolPrelude returns the host-side `eval.Prelude` definition for `workpool`.
// omp's embedded Python and JS eval preludes already define `workpool()` and
// `WorkPool` and forward calls to the `__workpool__` bridge, which
// `internal/eval` routes directly to `eval.LookupPrelude("workpool").Invoke`.
// JavaScript, Python, and Exports are intentionally left empty.
// upstream: packages/coding-agent/src/eval/workpool-bridge.ts
func (rt *Runtime) WorkpoolPrelude() eval.Prelude {
	return eval.Prelude{
		Name:          "workpool",
		Documentation: RenderEvalAgentsDoc,
		Summary:       "keep-alive worker pool for batches of independent items",
		Approval:      "exec",
		Invoke:        rt.invokeWorkpoolPrelude,
	}
}

// invokeAgentPrelude handles host calls forwarded from the `__agent__` bridge
// (and optional handle ops when invoked directly).
//
// Spawn request shape (sent by omp's py/prelude.py:1084 and js/shared/prelude.txt:628):
//
//	{ prompt, agent?, label?, schema?, schemaMode?, isolated?, apply?, merge?, tools?, model? }
//
// Spawn response (`PreludeResult.Details`):
//
//	{ "id": "<agent-id>", "agent": "<agent-name>" }
//
// Handle lifecycle ops (`__wait__`, `__status__`, `__cancel__`, `write agent://...`,
// `read agent://...`) are sent by omp's preludes to their dedicated bridges;
// `op: "status"|"cancel"|"wait"|"send"` are also accepted if dispatched through
// this handler, using omp's handle-bridge request/response shapes.
func (rt *Runtime) invokeAgentPrelude(call eval.PreludeCall, raw json.RawMessage) (*eval.PreludeResult, error) {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, fmt.Errorf("agent() received invalid arguments: expected JSON object")
	}

	sessKey := rt.sessionKey(call.SDK)
	jobs.BindSessionIfMissing(sessKey, rt.sessionContextFor(sessKey, call.SDK))

	op, _ := args["op"].(string)
	switch op {
	case "", "spawn":
		return rt.invokeAgentSpawn(call, sessKey, args)
	case "status":
		return rt.invokeAgentStatus(sessKey, args)
	case "cancel":
		return rt.invokeAgentCancel(sessKey, args)
	case "wait":
		return rt.invokeAgentWait(call, sessKey, args)
	case "send":
		return rt.invokeAgentSend(sessKey, args)
	default:
		return nil, fmt.Errorf("unknown agent prelude operation %q", op)
	}
}

func (rt *Runtime) invokeAgentSpawn(call eval.PreludeCall, sessKey string, args map[string]any) (*eval.PreludeResult, error) {
	rawPrompt, ok := args["prompt"]
	promptText, isStr := rawPrompt.(string)
	if !ok || !isStr || strings.TrimSpace(promptText) == "" {
		return nil, errors.New("agent() received invalid arguments: prompt must be a non-empty string")
	}

	// Reject unknown/badly typed fields matching omp's agentArgsSchema (`agent-bridge.ts:28`).
	var agentName string
	if rawAgent, exists := args["agent"]; exists && rawAgent != nil {
		s, ok := rawAgent.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, errors.New("agent() received invalid arguments: agent must be a non-empty string")
		}
		agentName = strings.TrimSpace(s)
	}
	var label string
	if rawLabel, exists := args["label"]; exists && rawLabel != nil {
		s, ok := rawLabel.(string)
		if !ok {
			return nil, errors.New("agent() received invalid arguments: label must be a string")
		}
		label = s
	}
	schemaVal, hasSchema := args["schema"]
	schemaMode := "permissive"
	if rawMode, exists := args["schemaMode"]; exists && rawMode != nil {
		s, ok := rawMode.(string)
		if !ok || (s != "permissive" && s != "strict") {
			return nil, errors.New("agent() received invalid arguments: schemaMode must be 'permissive' or 'strict'")
		}
		schemaMode = s
	}

	planMode := gate.PlanActive(call.SDK)

	// Validate and enforce isolation controls (`isolated`, `apply`, `merge`)
	// and plan-mode restrictions (`structured-subagent.ts:317-332,613`).
	hasIsolationControl := false
	var requestedIso bool
	if rawIso, exists := args["isolated"]; exists && rawIso != nil {
		iso, ok := rawIso.(bool)
		if !ok {
			return nil, errors.New("agent() received invalid arguments: isolated must be a boolean")
		}
		hasIsolationControl = true
		requestedIso = iso
	}
	hasApplyControl := false
	if rawApply, exists := args["apply"]; exists && rawApply != nil {
		if _, ok := rawApply.(bool); !ok {
			return nil, errors.New("agent() received invalid arguments: apply must be a boolean")
		}
		hasIsolationControl = true
		hasApplyControl = true
	}
	hasMergeControl := false
	if rawMerge, exists := args["merge"]; exists && rawMerge != nil {
		if _, ok := rawMerge.(bool); !ok {
			return nil, errors.New("agent() received invalid arguments: merge must be a boolean")
		}
		hasIsolationControl = true
		hasMergeControl = true
	}

	// Validate `tools` array (`agent-bridge.ts:38,185-187`).
	var evalToolNames []string
	if rawTools, exists := args["tools"]; exists && rawTools != nil {
		list, ok := rawTools.([]any)
		if !ok {
			return nil, errors.New("agent() received invalid arguments: tools must be an array of strings")
		}
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, errors.New("agent() received invalid arguments: tools must be an array of strings")
			}
			if s != "" {
				evalToolNames = append(evalToolNames, s)
			}
		}
	}

	if planMode {
		if len(evalToolNames) > 0 {
			return nil, errors.New("Eval-defined tools are unavailable in plan mode.")
		}
		if hasIsolationControl {
			return nil, errors.New("Subagent isolation, apply, and merge controls are unavailable in plan mode.")
		}
	}
	cfg := rt.EffectiveConfig(call.SDK)
	if requestedIso && !cfg.IsolationEnabled {
		return nil, errors.New("Subagent isolated execution requires task.isolation.enabled; it is currently false.")
	}
	if requestedIso {
		return nil, errors.New("Subagent isolated execution (worktree isolation) is not ported in oh-my-pig.")
	}
	if hasApplyControl {
		return nil, errors.New("Subagent isolated execution (`apply`) requires task.isolation.enabled; worktree isolation is not ported in oh-my-pig.")
	}
	if hasMergeControl {
		return nil, errors.New("Subagent isolated execution (`merge`) requires task.isolation.enabled; worktree isolation is not ported in oh-my-pig.")
	}
	if len(evalToolNames) > 0 {
		return nil, errors.New("Eval-defined tools are disabled; exposing kernel-defined @tool/tool() functions to subagents is not ported in oh-my-pig.")
	}
	var modelSel []string
	if rawModel, exists := args["model"]; exists && rawModel != nil && !isBlankModel(rawModel) {
		if cfg.PerCallModel == "off" {
			return nil, fmt.Errorf("Per-call model is disabled for eval calls (task.perCallModel: %s). Omit model to use the agent's configured model.", cfg.PerCallModel)
		}
		s, ok := rawModel.(string)
		if !ok || !strings.Contains(strings.TrimSpace(s), "/") {
			return nil, fmt.Errorf("agent() has an invalid `model` value %v. Use one exact provider/model-id selector.", rawModel)
		}
		modelSel = []string{strings.TrimSpace(s)}
	}

	taskDepth := rt.Registry.TaskDepthFor(sessKey)
	effMaxDepth := EffectiveMaxRecursionDepth(cfg.MaxRecursionDepth)
	if !CanSpawnAtDepth(effMaxDepth, taskDepth) {
		return nil, fmt.Errorf("Cannot spawn another agent at task depth %d; maximum depth is %d.", taskDepth, effMaxDepth)
	}
	parentSpawns := rt.Registry.SessionSpawnsFor(sessKey)
	policy := ResolveSpawnPolicy(parentSpawns)
	if agentName == "" {
		agentName = policy.DefaultAgent
	}
	if !policy.Allows(agentName) {
		return nil, fmt.Errorf("Cannot spawn '%s'. Allowed: %s", agentName, policy.AllowedErrorText)
	}

	cwd := call.SDK.Cwd()
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	disc := rt.discover(cwd)
	ag, ok := GetAgent(disc.Agents, agentName)
	if !ok {
		return nil, fmt.Errorf("Unknown agent %q", agentName)
	}
	if slices.Contains(cfg.DisabledAgents, agentName) {
		return nil, errors.New(formatDisabledAgentError(agentName, disc.Agents, cfg.DisabledAgents))
	}
	if planMode {
		ag = CreatePlanModeAgent(ag)
	}
	if len(modelSel) == 0 {
		if ov := cfg.ModelOverrides[agentName]; len(ov) > 0 {
			modelSel = ov
		} else {
			modelSel = ag.Model
		}
	}
	var outSchema any
	schemaSource := "none"
	overridesAgent := false
	if hasSchema && schemaVal != nil {
		outSchema = schemaVal
		schemaSource = "caller"
		overridesAgent = true
	} else if ag.Output != nil {
		outSchema = ag.Output
		schemaSource = "agent"
	}

	artifactsDir, _ := rt.sessionArtifactsDir(call.SDK)
	outMgr := rt.outputManager(sessKey, artifactsDir, cfg.CrossInstance)
	rawID := SanitizeAgentID(label)
	if rawID == "" {
		rawID = GenerateTaskName()
	}
	agentID := outMgr.Allocate(rawID)
	parentAgentID := MainAgentID
	if meta, ok := rt.Registry.ChildMeta(sessKey); ok && meta.AgentID != "" {
		parentAgentID = meta.AgentID
	}
	longLivedCtx := rt.sessionContextFor(sessKey, call.SDK)
	jobs.BindSessionIfMissing(sessKey, longLivedCtx)
	parentLocalRoot, _ := urls.LocalRoot(longLivedCtx)
	if taskDepth == 0 {
		rt.ensureMainRef(sessKey, longLivedCtx, artifactsDir)
	}

	jobCtx, jobCancel := context.WithCancel(context.Background())
	rt.trackCancel(sessKey, jobCancel)
	initial := AgentProgress{
		ID:          agentID,
		Agent:       ag.Name,
		AgentSource: ag.Source,
		Status:      "running",
		Task:        RenderSubagentUserPrompt(promptText),
		Assignment:  promptText,
	}
	job := NewAgentJob(agentID, agentID, jobCancel, initial)
	jobs.Add(sessKey, job)

	softBudget := ResolveSoftRequestBudget(ag.Name, cfg.SoftRequestBudget, cfg.AgentSoftRequestBudget)
	noticeEnabled := cfg.SoftRequestBudgetNotice
	go func() {
		defer jobCancel()
		defer func() {
			if r := recover(); r != nil {
				errStr := fmt.Sprintf("subagent panicked: %v", r)
				job.finish("failed", errStr, errStr, nil, nil, false, nil)
			}
		}()
		if err := rt.sem.Acquire(jobCtx); err != nil {
			job.finish("cancelled", "Aborted before execution", "Aborted before execution", nil, nil, false, nil)
			return
		}
		defer rt.sem.Release()

		sr := RunSubagent(jobCtx, rt.Registry, RunRequest{
			SDK:                        longLivedCtx,
			ParentSessionID:            sessKey,
			ParentAgentID:              parentAgentID,
			ParentLocalRoot:            parentLocalRoot,
			Cwd:                        cwd,
			TaskDepth:                  taskDepth,
			ID:                         agentID,
			Agent:                      ag,
			Assignment:                 promptText,
			OutputSchema:               outSchema,
			SchemaMode:                 schemaMode,
			SchemaSource:               schemaSource,
			OutputSchemaOverridesAgent: overridesAgent,
			ModelOverride:              modelSel,
			MaxEffort:                  cfg.MaxEffort,
			ArtifactsDir:               artifactsDir,
			MaxRuntimeMs:               cfg.MaxRuntimeMs,
			SoftBudget:                 softBudget,
			SoftBudgetNotice:           &noticeEnabled,
			AgentIdleTTLMs:             cfg.AgentIdleTTLMs,
			MaxDepth:                   cfg.MaxRecursionDepth,
			IRCEnabled:                 !planMode,
			KeepAlive:                  true,
			Semaphore:                  rt.sem,
			OnProgress: func(p AgentProgress) {
				job.updateProgress(p, fmt.Sprintf("Running agent %s...", p.ID))
			},
			OnToolResult: func(tool, intent, text string) {
				job.RecordToolCall(tool, intent, text)
			},
		})
		failed := sr.Aborted || sr.ExitCode != 0 || sr.Error != ""
		status := "completed"
		var errStr string
		if jobCtx.Err() != nil {
			status = "cancelled"
			errStr = sr.AbortReason
		} else if failed {
			status = "failed"
			errStr = sr.Error
			if errStr == "" {
				errStr = sr.AbortReason
			}
			if errStr == "" {
				errStr = sr.Stderr
			}
		}
		var evalData any
		hasEvalData := false
		if sr.StructuredOutput != nil && sr.StructuredOutput.HasData {
			evalData = sr.StructuredOutput.Data
			hasEvalData = true
		}
		job.finish(status, sr.Output, errStr, &sr, evalData, hasEvalData, nil)
	}()

	// Shape matches omp's EvalAgentHandleResult (`agent-bridge.ts:62`): { id, agent }.
	return &eval.PreludeResult{
		Text: agentID,
		Details: map[string]any{
			"id":    agentID,
			"agent": ag.Name,
		},
	}, nil
}

func extractHandleID(args map[string]any) (string, error) {
	if rawItem, ok := args["item"].(map[string]any); ok {
		kind, _ := rawItem["kind"].(string)
		id, _ := rawItem["id"].(string)
		if (kind != "" && kind != "agent") || id == "" {
			return "", errors.New("handle must contain kind agent|completion and a non-empty id")
		}
		return id, nil
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", errors.New("handle request requires an item")
	}
	return id, nil
}

func (rt *Runtime) invokeAgentStatus(sessKey string, args map[string]any) (*eval.PreludeResult, error) {
	id, err := extractHandleID(args)
	if err != nil {
		return nil, err
	}
	snap, err := rt.evalHandleSnapshot(sessKey, id)
	if err != nil {
		return nil, err
	}
	return &eval.PreludeResult{Details: snap}, nil
}

func (rt *Runtime) invokeAgentCancel(sessKey string, args map[string]any) (*eval.PreludeResult, error) {
	id, err := extractHandleID(args)
	if err != nil {
		return nil, err
	}
	j, ok := jobs.Get(sessKey, id)
	if !ok {
		return nil, fmt.Errorf("unknown agent handle %s; result expired — read agent://%s", id, id)
	}
	cancelled := j.Status() == "running"
	j.Cancel()
	return &eval.PreludeResult{Details: map[string]any{"cancelled": cancelled}}, nil
}

func (rt *Runtime) invokeAgentWait(call eval.PreludeCall, sessKey string, args map[string]any) (*eval.PreludeResult, error) {
	// Support omp's handle-bridge shape `{ items: [{kind, id}], timeoutMs? }` as well as `{ ids: [...] }`.
	var ids []string
	if rawItems, ok := args["items"].([]any); ok {
		for _, r := range rawItems {
			m, ok := r.(map[string]any)
			if !ok {
				return nil, errors.New("handle must be an object with kind and id")
			}
			kind, _ := m["kind"].(string)
			id, _ := m["id"].(string)
			if kind != "agent" || id == "" {
				return nil, errors.New("handle must contain kind agent|completion and a non-empty id")
			}
			ids = append(ids, strings.TrimPrefix(id, "agent://"))
		}
	} else if rawIDs, ok := args["ids"].([]any); ok {
		for _, r := range rawIDs {
			s, ok := r.(string)
			if !ok || s == "" {
				return nil, errors.New("wait() expects non-empty agent handle ids")
			}
			ids = append(ids, strings.TrimPrefix(s, "agent://"))
		}
	} else {
		return nil, errors.New("wait() requires an items array")
	}
	if len(ids) == 0 {
		return &eval.PreludeResult{Details: map[string]any{"items": []any{}}}, nil
	}
	var timeoutCh <-chan time.Time
	if rawMs, ok := args["timeoutMs"]; ok && rawMs != nil {
		ms, ok := asFloat(rawMs)
		if !ok || ms < 0 {
			return nil, errors.New("wait() timeoutMs must be a non-negative finite number")
		}
		t := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer t.Stop()
		timeoutCh = t.C
	}
	callDone := call.SDK.Done()
	for _, id := range ids {
		j, ok := jobs.Get(sessKey, id)
		if !ok {
			return nil, fmt.Errorf("unknown agent handle %s; result expired — read agent://%s", id, id)
		}
		select {
		case <-j.Done():
		case <-timeoutCh:
		case <-callDone:
			return nil, errors.New("wait aborted")
		}
	}
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		snap, err := rt.evalHandleSnapshot(sessKey, id)
		if err != nil {
			return nil, err
		}
		if snap["status"] != "running" {
			jobs.MarkConsumed(sessKey, id)
		}
		items = append(items, snap)
	}
	return &eval.PreludeResult{Details: map[string]any{"items": items}}, nil
}

func (rt *Runtime) invokeAgentSend(sessKey string, args map[string]any) (*eval.PreludeResult, error) {
	id, _ := args["id"].(string)
	msg, _ := args["message"].(string)
	if id == "" {
		return nil, errors.New("agent send requires id")
	}
	text, details, isErr := rt.Registry.SendMessageInSession(sessKey, MainAgentID, id, msg, false, "", false)
	if isErr {
		return nil, errors.New(text)
	}
	return &eval.PreludeResult{Text: text, Details: details}, nil
}

func (rt *Runtime) evalHandleSnapshot(sessKey, id string) (map[string]any, error) {
	j, ok := jobs.Get(sessKey, id)
	if !ok {
		return nil, fmt.Errorf("unknown agent handle %s; result expired — read agent://%s", id, id)
	}
	st := j.Status()
	snap := map[string]any{
		"kind":   "agent",
		"id":     id,
		"status": st,
	}
	if st == "running" {
		return snap, nil
	}
	if aj, ok := j.(*AgentJob); ok {
		aj.mu.Lock()
		errTxt := aj.errorText
		resTxt := aj.result
		hasData := aj.hasEvalData
		dataVal := aj.evalData
		aj.mu.Unlock()
		if st == "failed" || st == "cancelled" {
			if errTxt == "" {
				errTxt = resTxt
			}
			if errTxt == "" {
				errTxt = "Agent " + st
			}
			snap["error"] = errTxt
			return snap, nil
		}
		snap["text"] = resTxt
		if hasData {
			snap["data"] = dataVal
		}
		return snap, nil
	}
	if st == "failed" || st == "cancelled" {
		snap["error"] = j.Result()
		return snap, nil
	}
	snap["text"] = j.Result()
	return snap, nil
}

// invokeWorkpoolPrelude handles host calls forwarded from the `__workpool__` bridge.
//
// Request shapes (sent by omp's py/prelude.py:1102-1142 and js/shared/prelude.txt:646-697):
//   - create: { op: "create", agent?, name?, context?, tools?, model? } -> { name, agent, limit }
//   - push:   { op: "push", name, items: string[] } -> { ids: string[] }
//   - status: { op: "status", name } -> WorkPoolStatus
//   - peek:   { op: "peek", name } -> WorkPoolPeekResult
//   - close:  { op: "close", name } -> { dropped: string[] }
//
// upstream: packages/coding-agent/src/eval/workpool-bridge.ts:67
func (rt *Runtime) invokeWorkpoolPrelude(call eval.PreludeCall, raw json.RawMessage) (*eval.PreludeResult, error) {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, errors.New("workpool() arguments must be an object")
	}
	op, _ := args["op"].(string)
	if op == "" {
		return nil, errors.New("workpool() requires an op")
	}
	sessKey := rt.sessionKey(call.SDK)
	longLivedCtx := rt.sessionContextFor(sessKey, call.SDK)
	jobs.BindSessionIfMissing(sessKey, longLivedCtx)
	planMode := gate.PlanActive(call.SDK)
	if op == "create" {
		var agentName string
		if rawAgent, ok := args["agent"]; ok && rawAgent != nil {
			s, ok := rawAgent.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, errors.New("workpool agent must be a non-empty string")
			}
			agentName = strings.TrimSpace(s)
		}
		var reqName string
		if rawName, ok := args["name"]; ok && rawName != nil {
			s, ok := rawName.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, errors.New("workpool name must be a non-empty string")
			}
			reqName = strings.TrimSpace(s)
		}
		var contextText string
		if rawCtx, ok := args["context"]; ok && rawCtx != nil {
			s, ok := rawCtx.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, errors.New("workpool context must be a non-empty string")
			}
			contextText = strings.TrimSpace(s)
		}
		if rawTools, ok := args["tools"]; ok && rawTools != nil {
			list, ok := rawTools.([]any)
			if !ok {
				return nil, errors.New("workpool tools must be an array of non-empty strings")
			}
			var names []string
			for _, item := range list {
				s, ok := item.(string)
				if !ok || len(s) == 0 {
					return nil, errors.New("workpool tools must be an array of non-empty strings")
				}
				names = append(names, s)
			}
			if len(names) > 0 {
				if planMode {
					return nil, errors.New("Eval-defined tools are unavailable in plan mode.")
				}
				return nil, errors.New("Eval-defined tools are disabled; exposing kernel-defined @tool/tool() functions to subagents is not ported in oh-my-pig.")
			}
		}
		cfg := rt.EffectiveConfig(call.SDK)
		var modelSel []string
		if rawModel, exists := args["model"]; exists && rawModel != nil && !isBlankModel(rawModel) {
			if cfg.PerCallModel == "off" {
				return nil, fmt.Errorf("Per-call model is disabled for eval calls (task.perCallModel: %s). Omit model to use the agent's configured model.", cfg.PerCallModel)
			}
			s, ok := rawModel.(string)
			if !ok || !strings.Contains(strings.TrimSpace(s), "/") {
				return nil, fmt.Errorf("workpool() has an invalid `model` value %v. Use one exact provider/model-id selector.", rawModel)
			}
			modelSel = []string{strings.TrimSpace(s)}
		}

		taskDepth := rt.Registry.TaskDepthFor(sessKey)
		effMaxDepth := EffectiveMaxRecursionDepth(cfg.MaxRecursionDepth)
		if !CanSpawnAtDepth(effMaxDepth, taskDepth) {
			return nil, fmt.Errorf("Cannot spawn another agent at task depth %d; maximum depth is %d.", taskDepth, effMaxDepth)
		}
		parentSpawns := rt.Registry.SessionSpawnsFor(sessKey)
		policy := ResolveSpawnPolicy(parentSpawns)
		if agentName == "" {
			agentName = policy.DefaultAgent
		}
		if !policy.Allows(agentName) {
			return nil, fmt.Errorf("Cannot spawn '%s'. Allowed: %s", agentName, policy.AllowedErrorText)
		}

		cwd := call.SDK.Cwd()
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		disc := rt.discover(cwd)
		ag, ok := GetAgent(disc.Agents, agentName)
		if !ok {
			return nil, fmt.Errorf("Unknown agent %q", agentName)
		}
		if slices.Contains(cfg.DisabledAgents, agentName) {
			return nil, errors.New(formatDisabledAgentError(agentName, disc.Agents, cfg.DisabledAgents))
		}
		if planMode {
			ag = CreatePlanModeAgent(ag)
		}
		if len(modelSel) == 0 {
			if ov := cfg.ModelOverrides[agentName]; len(ov) > 0 {
				modelSel = ov
			} else {
				modelSel = ag.Model
			}
		}
		artifactsDir, _ := rt.sessionArtifactsDir(call.SDK)
		rt.mu.Lock()
		name := reqName
		if name == "" {
			base := ag.Name + "-pool"
			name = base
			for suffix := 2; ; suffix++ {
				key := sessKey + "\x00" + name
				_, existsPool := rt.workpools[key]
				_, existsJob := jobs.Get(sessKey, name)
				if !existsPool && !existsJob {
					break
				}
				name = fmt.Sprintf("%s-%d", base, suffix)
			}
		} else {
			key := sessKey + "\x00" + name
			if _, exists := rt.workpools[key]; exists {
				rt.mu.Unlock()
				return nil, fmt.Errorf("workpool %q already exists", name)
			}
			if _, existsJob := jobs.Get(sessKey, name); existsJob {
				rt.mu.Unlock()
				return nil, fmt.Errorf("workpool job id %q already exists", name)
			}
		}
		parentAgentID := MainAgentID
		if meta, ok := rt.Registry.ChildMeta(sessKey); ok && meta.AgentID != "" {
			parentAgentID = meta.AgentID
		}
		wp := rt.newWorkPoolLocked(longLivedCtx, sessKey, parentAgentID, taskDepth, planMode, cwd, artifactsDir, name, ag, contextText, modelSel)
		rt.workpools[sessKey+"\x00"+name] = wp
		rt.mu.Unlock()
		return &eval.PreludeResult{
			Text: name,
			Details: map[string]any{
				"name":  name,
				"agent": ag.Name,
				"limit": wp.limit,
			},
		}, nil
	}

	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("workpool operation requires a non-empty name")
	}
	rt.mu.Lock()
	wp := rt.workpools[sessKey+"\x00"+name]
	rt.mu.Unlock()
	if wp == nil {
		return nil, fmt.Errorf("unknown workpool %q", name)
	}

	switch op {
	case "push":
		rawItems, ok := args["items"].([]any)
		if !ok {
			return nil, errors.New("workpool push requires an items string array")
		}
		items := make([]string, len(rawItems))
		for i, v := range rawItems {
			s, ok := v.(string)
			if !ok {
				return nil, errors.New("workpool push requires an items string array")
			}
			items[i] = s
		}
		ids, err := wp.Push(items)
		if err != nil {
			return nil, err
		}
		return &eval.PreludeResult{Details: map[string]any{"ids": ids}}, nil
	case "status":
		return &eval.PreludeResult{Details: wp.Status()}, nil
	case "peek":
		return &eval.PreludeResult{Details: wp.Peek()}, nil
	case "close":
		return &eval.PreludeResult{Details: wp.Close()}, nil
	default:
		return nil, fmt.Errorf("unknown workpool operation %q", op)
	}
}
