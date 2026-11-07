package langsmith

// Port of src/index.ts (the factory and every hook of @langchain/langsmith-pi-extension 0.2.1).
//
// The original is one async function per hook over shared closure state. Pi awaits a hook before it fires the next
// event, but fire-and-forget emits (tool_execution_update) can interleave at an await; this port serialises every
// hook with one mutex, so the state machine (active, staleSettles, userTurnByThread, deferNextLlmToNextTurn,
// pendingLlm, loopResult) is only ever observed between whole hooks.
//
// Not ported, by decision: the TypeScript embedding options `client`, `config` and `getCurrentRunTree` of
// LangSmithExtensionOptions (index.ts:383-404, 407-418, 471-476): a Go Piglet has no embedding entry point. With no
// traceable context `getCurrentRunTree(true)` is undefined, so every agent run is a new root trace.
// src/sdk.ts is the README's embedding example, not extension code.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// statusKey is index.ts:14.
const statusKey = "langsmith"

// piEmitsAgentSettled is index.ts:16-22: Pi 0.80.4 added `agent_settled`. A version that does not start with
// <digits>.<digits>.<digits> (or none at all) gives false.
func piEmitsAgentSettled(version string) bool {
	var nums [3]float64
	pos := 0
	for i := range nums {
		start := pos
		for pos < len(version) && version[pos] >= '0' && version[pos] <= '9' {
			pos++
		}
		if pos == start {
			return false
		}
		nums[i], _ = strconv.ParseFloat(version[start:pos], 64)
		if i < 2 {
			if pos >= len(version) || version[pos] != '.' {
				return false
			}
			pos++
		}
	}
	major, minor, patch := nums[0], nums[1], nums[2]
	return major > 0 || minor > 80 || (minor == 80 && patch >= 4)
}

// hostCtx makes a request Context a context.Context for the client and run-tree calls of one hook.
type hostCtx struct{ c sdk.Context }

func (h hostCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (h hostCtx) Done() <-chan struct{}       { return h.c.Done() }
func (h hostCtx) Err() error                  { return h.c.Err() }
func (h hostCtx) Value(any) any               { return nil }

var _ context.Context = hostCtx{}

// runMap is a JavaScript Map<_, RunTree>: insertion order, a re-set key keeps its place.
type runMap struct {
	keys []string
	vals map[string]*runTree
}

func newRunMap() *runMap { return &runMap{vals: map[string]*runTree{}} }

func (m *runMap) set(k string, r *runTree) {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = r
}

func (m *runMap) get(k string) *runTree { return m.vals[k] }

func (m *runMap) delete(k string) {
	if _, ok := m.vals[k]; !ok {
		return
	}
	delete(m.vals, k)
	for i, key := range m.keys {
		if key == k {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			break
		}
	}
}

func (m *runMap) clear() {
	m.keys = m.keys[:0]
	clear(m.vals)
}

// values is a snapshot in insertion order.
func (m *runMap) values() []*runTree {
	out := make([]*runTree, 0, len(m.keys))
	for _, k := range m.keys {
		out = append(out, m.vals[k])
	}
	return out
}

// mapKey is the SameValueZero identity of a Map key holding a decoded JSON value.
func mapKey(v any) string {
	switch x := v.(type) {
	case string:
		return "s" + x
	case float64:
		if x == 0 {
			x = 0 // -0 and +0 are one key
		}
		return "n" + jsNumberString(x)
	case bool:
		return "b" + strconv.FormatBool(x)
	case nil:
		return "l"
	case undefinedT:
		return "u"
	}
	return fmt.Sprintf("o%p", &v) // objects and arrays are keys by identity; Pi never sends one
}

// pendingLlmRun is index.ts:24-28.
type pendingLlmRun struct {
	name     string
	payload  any
	metadata *omap
}

// loopResult is TraceContext.loopResult (index.ts:40).
type loopResult struct {
	outputs *omap
	err     *string
}

// traceContext is index.ts:30-41.
type traceContext struct {
	root  *runTree
	turns *runMap

	currentTurn            *runTree
	currentLlm             *runTree
	pendingLlm             *pendingLlmRun
	deferNextLlmToNextTurn bool

	tools      *runMap
	loopResult *loopResult
}

// tracer is the closure state of the default export (index.ts:406-712).
type tracer struct {
	mu     sync.Mutex
	config *config
	client *client
	// piVersion is Pi's VERSION as the host reports it (nil when it reports none).
	piVersion    *string
	emitsSettled bool

	active *traceContext
	// staleSettles counts `agent_settled` events still owed for runs already ended by the next `before_agent_start`.
	staleSettles int
	// userTurnByThread is the 1-based user-turn counter keyed by session id (pi exposes none).
	userTurnByThread map[string]int
}

func (t *tracer) nextUserTurn(threadID *string) int {
	key := ""
	if threadID != nil {
		key = *threadID
	}
	next := t.userTurnByThread[key] + 1
	t.userTurnByThread[key] = next
	return next
}

// factoryError is the text of a failed factory. The host shows the first line of a panic only, so a ZodError (multi-line
// JSON) gets a one-line summary from its first issue (path joined with `.`, then the message) before the full text.
func factoryError(err error) error {
	var ze *zodError
	if errors.As(err, &ze) && len(ze.Issues) > 0 {
		first := ze.Issues[0]
		parts := make([]string, len(first.Path))
		for i, p := range first.Path {
			parts[i] = fmt.Sprint(p)
		}
		summary := first.Message
		if len(parts) > 0 {
			summary = strings.Join(parts, ".") + ": " + summary
		}
		return fmt.Errorf("langsmith: invalid configuration: %s\n%s", summary, ze.Error())
	}
	return fmt.Errorf("langsmith: %w", err)
}

// Extension returns the LangSmith tracing extension. It reads the configuration, as the original's factory does; a
// configuration the original's factory rejects (a ZodError from the environment) or a client the SDK constructor
// rejects makes the factory panic, which the host reports as an extension that failed to load.
func Extension() *sdk.Extension {
	cfg, err := getConfig(nil)
	if err != nil {
		panic(factoryError(err))
	}
	var cl *client
	if cfg.Enabled {
		cl, err = newClient(clientOptions{APIKey: cfg.APIKey, APIURL: cfg.APIURL})
		if err != nil {
			panic(factoryError(err))
		}
	}
	var piVersion *string
	if v, verr := sdk.Version(); verr == nil {
		piVersion = &v
	}
	return newExtension(cfg, cl, piVersion)
}

// newExtension registers the hooks for an already resolved configuration (index.ts:414 onward). client is nil when
// tracing is disabled.
func newExtension(cfg *config, cl *client, piVersion *string) *sdk.Extension {
	t := &tracer{config: cfg, client: cl, piVersion: piVersion, userTurnByThread: map[string]int{}}
	if piVersion != nil {
		t.emitsSettled = piEmitsAgentSettled(*piVersion)
	}
	ext := sdk.New("langsmith")
	enabled := cfg.Enabled

	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if enabled {
			ctx.SetStatus(statusKey, "LangSmith: tracing")
		} else {
			ctx.SetStatus(statusKey, "LangSmith: disabled")
		}
		return nil, nil
	})

	ext.RegisterCommand("langsmith-tracing", sdk.CommandOptions{
		Description: "Show LangSmith tracing status for this Pi session",
		Handler: func(ctx sdk.Context, _ string) error {
			var message string
			if enabled {
				t.mu.Lock()
				active := t.active
				var id string
				if active != nil {
					id = active.root.ID
				}
				t.mu.Unlock()
				message = "LangSmith tracing is enabled"
				if active != nil {
					message += " (active trace " + id + ")"
				}
				message += "."
				ctx.Notify(message, "info")
				return nil
			}
			message = "LangSmith tracing is disabled. Set TRACE_TO_LANGSMITH=true and configure LANGSMITH_PI_API_KEY (or LANGSMITH_PI_ENDPOINT for self-hosted) to enable it."
			ctx.Notify(message, "warning")
			return nil
		},
	})

	if !enabled || cl == nil {
		return ext
	}

	hook := func(event string, fn func(ctx sdk.Context, hc context.Context, data map[string]any) error) {
		ext.OnEvent(event, func(ctx sdk.Context, data map[string]any) (any, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			return nil, fn(ctx, hostCtx{ctx}, data)
		})
	}
	hook(sdk.EventBeforeAgentStart, t.beforeAgentStart)
	hook(sdk.EventSessionCompact, t.sessionCompact)
	hook(sdk.EventTurnStart, t.turnStart)
	hook(sdk.EventBeforeProviderRequest, t.beforeProviderRequest)
	hook(sdk.EventMessageEnd, t.messageEnd)
	hook(sdk.EventToolExecutionStart, t.toolExecutionStart)
	hook(sdk.EventToolExecutionUpdate, t.toolExecutionUpdate)
	hook(sdk.EventToolExecutionEnd, t.toolExecutionEnd)
	hook(sdk.EventTurnEnd, t.turnEnd)
	hook(sdk.EventAgentEnd, t.agentEnd)
	hook(sdk.EventAgentSettled, t.agentSettled)
	hook(sdk.EventSessionShutdown, t.sessionShutdown)
	return ext
}

// ── helpers (index.ts:317-381) ──────────────────────────────────────────────────────────────────────────────

func ptrOrUndef(p *string) any {
	if p == nil {
		return undef
	}
	return *p
}

// modelInfo is `ctx.model?.provider` and `ctx.model?.name?.toLocaleLowerCase()`: nil pointers when there is no model
// (or the model has no name).
func modelInfo(ctx sdk.Context) (provider, lowerName *string, err error) {
	info, err := ctx.GetModelInfo()
	if err != nil || info == nil {
		return nil, nil, err
	}
	provider = &info.Provider
	if info.Name != "" {
		l := jsToLowerCase(info.Name)
		lowerName = &l
	}
	return provider, lowerName, nil
}

// startLlmRun is index.ts:317-332.
func (t *tracer) startLlmRun(hc context.Context, a *traceContext, parent *runTree, pending *pendingLlmRun) error {
	inputs, err := convertProviderPayload(pending.payload)
	if err != nil {
		return err
	}
	llm, err := parent.createChild(runTreeConfig{Name: pending.name, RunType: "llm", Inputs: inputs, Metadata: pending.metadata})
	if err != nil {
		return err
	}
	a.currentLlm = llm
	llm.postRun(hc)
	return nil
}

// safeEnd is index.ts:338-348: end the run and patch it; nothing for an undefined run. A patchRun failure the run
// tree returns (replicas configured) rejects in the original, so it is returned.
func safeEnd(hc context.Context, run *runTree, outputs any, errMsg *string, metadata *omap) error {
	if run == nil {
		return nil
	}
	run.end(outputs, errMsg, metadata)
	return run.patchRun(hc)
}

// createRootRun is index.ts:350-381 (parent is always undefined here).
func (t *tracer) createRootRun(hc context.Context, prompt any, imageCount int, cwd string, threadID *string, turnNumber int) (*runTree, error) {
	metadata := codingAgentMetadata(hc, codingAgentMetadataInput{AgentType: "root", ThreadID: threadID, Cwd: cwd, TurnNumber: &turnNumber}, t.piVersion)
	metadata.spread(t.config.Metadata)
	cfg := runTreeConfig{
		Name:        "Pi agent run",
		RunType:     "chain",
		ProjectName: t.config.Project,
		Client:      t.client,
		Inputs:      obj("prompt", prompt, "imageCount", imageCount),
		Metadata:    metadata,
		Tags:        []string{"pi", "coding-agent"},
	}
	if t.config.Replicas != nil {
		cfg.Replicas = make([]replicaConfig, len(t.config.Replicas))
		for i, r := range t.config.Replicas {
			var rc replicaConfig
			if r.APIURL != nil && *r.APIURL != "" {
				rc.APIURL = r.APIURL
			}
			if r.APIKey != nil && *r.APIKey != "" {
				rc.APIKey = r.APIKey
			}
			if r.Project != nil && *r.Project != "" {
				rc.ProjectName = r.Project
			}
			rc.Updates = r.Updates
			cfg.Replicas[i] = rc
		}
	}
	return newRunTree(cfg)
}

// ── hooks ───────────────────────────────────────────────────────────────────────────────────────────────────

// beforeAgentStart is index.ts:453-495.
func (t *tracer) beforeAgentStart(ctx sdk.Context, hc context.Context, data map[string]any) error {
	previous := t.active
	t.active = nil
	if previous != nil && previous.loopResult != nil {
		// The previous run's loop already ended, but its `agent_settled` has not reached us: on Pi <0.87.0 an
		// extension's `agent_settled` handler can start this prompt before ours runs (index.ts:457-461).
		t.staleSettles++
		if err := safeEnd(hc, previous.root, previous.loopResult.outputs, previous.loopResult.err, nil); err != nil {
			return err
		}
	} else if previous != nil {
		if err := safeEnd(hc, previous.root, obj("interruptedByNextRun", true), new("Trace replaced by a new Pi run"), nil); err != nil {
			return err
		}
	}

	var threadID *string
	id, err := ctx.GetSessionID()
	if err != nil {
		return err
	}
	threadID = &id

	imageCount := 0
	if n, ok := jsLength(recGet(data, "images")); ok {
		imageCount = n
	}
	root, err := t.createRootRun(hc, recGet(data, "prompt"), imageCount, ctx.Cwd(), threadID, t.nextUserTurn(threadID))
	if err != nil {
		return err
	}
	t.active = &traceContext{root: root, turns: newRunMap(), tools: newRunMap()}
	root.postRun(hc)
	ctx.SetStatus(statusKey, "LangSmith: tracing run")
	return nil
}

// sessionCompact is index.ts:497-512.
func (t *tracer) sessionCompact(_ sdk.Context, hc context.Context, data map[string]any) error {
	a := t.active
	if a == nil {
		return nil
	}
	reason := recGet(data, "reason")
	run, err := a.root.createChild(runTreeConfig{
		Name:     "Context Compaction (" + jsToString(reason) + ")",
		RunType:  "chain",
		Inputs:   obj("reason", reason, "willRetry", recGet(data, "willRetry")),
		Metadata: obj("ls_agent_type", "compaction"),
	})
	if err != nil {
		return err
	}
	run.postRun(hc)
	entry := recGet(data, "compactionEntry")
	if !isRecord(entry) { // event.compactionEntry.summary on a non-object throws
		return fmt.Errorf("Cannot read properties of %s (reading 'summary')", jsToString(entry))
	}
	return safeEnd(hc, run, obj("summary", recGet(entry, "summary"), "tokensBefore", recGet(entry, "tokensBefore")), nil, nil)
}

// turnStart is index.ts:514-538.
func (t *tracer) turnStart(ctx sdk.Context, hc context.Context, data map[string]any) error {
	a := t.active
	if a == nil {
		return nil
	}
	provider, lowerName, err := modelInfo(ctx)
	if err != nil {
		return err
	}
	turnIndex := recGet(data, "turnIndex")
	turn, err := a.root.createChild(runTreeConfig{
		Name:    "Pi turn " + jsToString(turnIndex),
		RunType: "chain",
		Inputs:  obj("turnIndex", turnIndex),
		Metadata: obj(
			"ls_provider", ptrOrUndef(provider),
			"ls_model_name", ptrOrUndef(lowerName),
			"timestamp", recGet(data, "timestamp"),
			// Inner agent-loop (ReAct) index, for debugging only: not turn_number (index.ts:524).
			"pi_loop_index", turnIndex,
		),
	})
	if err != nil {
		return err
	}
	a.turns.set(mapKey(turnIndex), turn)
	a.currentTurn = turn
	a.deferNextLlmToNextTurn = false
	turn.postRun(hc)

	if a.pendingLlm != nil {
		pending := a.pendingLlm
		a.pendingLlm = nil
		return t.startLlmRun(hc, a, turn, pending)
	}
	return nil
}

// beforeProviderRequest is index.ts:540-558.
func (t *tracer) beforeProviderRequest(ctx sdk.Context, hc context.Context, data map[string]any) error {
	a := t.active
	if a == nil {
		return nil
	}
	provider, lowerName, err := modelInfo(ctx)
	if err != nil {
		return err
	}
	name := "provider request"
	switch {
	case provider != nil:
		name = *provider
	case lowerName != nil:
		name = *lowerName
	}
	pending := &pendingLlmRun{
		name:     name,
		payload:  recGet(data, "payload"),
		metadata: obj("ls_provider", ptrOrUndef(provider), "ls_model_name", ptrOrUndef(lowerName)),
	}
	if a.deferNextLlmToNextTurn {
		a.pendingLlm = pending
		return nil
	}
	parent := a.currentTurn
	if parent == nil {
		parent = a.root
	}
	return t.startLlmRun(hc, a, parent, pending)
}

// messageOf is `event.message`; reading a property of undefined or null throws in the original.
func messageOf(data map[string]any, prop string) (any, error) {
	m := recGet(data, "message")
	if !isRecord(m) && isNullish(m) {
		return nil, fmt.Errorf("Cannot read properties of %s (reading '%s')", jsToString(m), prop)
	}
	return m, nil
}

// errorString is a string error value; the original passes whatever Pi sent, typed as a string.
func errorString(v any) *string {
	if isNullish(v) {
		return nil
	}
	s := jsToString(v)
	return &s
}

// messageEnd is index.ts:560-577.
func (t *tracer) messageEnd(_ sdk.Context, hc context.Context, data map[string]any) error {
	a := t.active
	if a == nil {
		return nil
	}
	message, err := messageOf(data, "role")
	if err != nil {
		return err
	}
	if recGet(message, "role") != "assistant" || a.currentLlm == nil {
		return nil
	}
	var errMsg *string
	if recGet(message, "stopReason") == "error" {
		if e := recGet(message, "errorMessage"); !isNullish(e) {
			errMsg = errorString(e)
		} else {
			errMsg = new("Assistant message ended with stopReason=error")
		}
	}
	messages, err := convertMessages([]any{message})
	if err != nil {
		return err
	}
	usage, err := extractUsageMetadata(message)
	if err != nil {
		return err
	}
	if err := safeEnd(hc, a.currentLlm, obj("messages", messages), errMsg, obj(
		"stop_reason", recGet(message, "stopReason"),
		"usage_metadata", usage,
	)); err != nil {
		return err
	}
	a.currentLlm = nil
	a.deferNextLlmToNextTurn = true
	return nil
}

// toolExecutionStart is index.ts:579-590.
func (t *tracer) toolExecutionStart(_ sdk.Context, hc context.Context, data map[string]any) error {
	a := t.active
	if a == nil {
		return nil
	}
	parent := a.currentTurn
	if parent == nil {
		parent = a.root
	}
	toolName, toolCallID := recGet(data, "toolName"), recGet(data, "toolCallId")
	tool, err := parent.createChild(runTreeConfig{
		Name:     jsToString(toolName),
		RunType:  "tool",
		Inputs:   obj("toolCallId", toolCallID, "args", recGet(data, "args")),
		Metadata: obj("toolName", toolName, "toolCallId", toolCallID),
	})
	if err != nil {
		return err
	}
	a.tools.set(mapKey(toolCallID), tool)
	tool.postRun(hc)
	return nil
}

// toolExecutionUpdate is index.ts:592-597.
func (t *tracer) toolExecutionUpdate(_ sdk.Context, _ context.Context, data map[string]any) error {
	a := t.active
	if a == nil {
		return nil
	}
	tool := a.tools.get(mapKey(recGet(data, "toolCallId")))
	if tool == nil {
		return nil
	}
	tool.addEvent("tool_update", obj("partialResult", recGet(data, "partialResult")))
	return nil
}

// toolExecutionEnd is index.ts:599-609.
func (t *tracer) toolExecutionEnd(_ sdk.Context, hc context.Context, data map[string]any) error {
	a := t.active
	if a == nil {
		return nil
	}
	key := mapKey(recGet(data, "toolCallId"))
	tool := a.tools.get(key)
	if tool == nil {
		return nil
	}
	a.tools.delete(key)
	var errMsg *string
	if jsTruthy(recGet(data, "isError")) {
		errMsg = new("Tool execution failed")
	}
	return safeEnd(hc, tool, convertToolOutputs(recGet(data, "result")), errMsg, nil)
}

// turnEnd is index.ts:611-625. convertMessages([event.message]) is evaluated for both runs, as in the original, even
// when there is no run to end; the two results are equal, so it is computed once.
func (t *tracer) turnEnd(_ sdk.Context, hc context.Context, data map[string]any) error {
	a := t.active
	if a == nil {
		return nil
	}
	turn := a.turns.get(mapKey(recGet(data, "turnIndex")))
	if turn == nil {
		turn = a.currentTurn
	}
	message := recGet(data, "message")
	messages, err := convertMessages([]any{message})
	if err != nil {
		return err
	}

	usage, err := extractUsageMetadata(message)
	if err != nil {
		return err
	}
	if err := safeEnd(hc, a.currentLlm, obj("messages", messages), nil, obj("usage_metadata", usage)); err != nil {
		return err
	}
	a.currentLlm = nil

	if err := safeEnd(hc, turn, obj("messages", messages, "toolResults", recGet(data, "toolResults")), nil, nil); err != nil {
		return err
	}
	if turn == a.currentTurn {
		a.currentTurn = nil
	}
	return nil
}

// agentEnd is index.ts:627-672.
func (t *tracer) agentEnd(ctx sdk.Context, hc context.Context, data map[string]any) error {
	a := t.active
	if a == nil {
		return nil
	}

	for _, tool := range a.tools.values() {
		if err := safeEnd(hc, tool, undef, new("Pi run ended before tool_execution_end"), nil); err != nil {
			return err
		}
	}
	a.tools.clear()

	if a.pendingLlm != nil {
		pending := a.pendingLlm
		a.pendingLlm = nil
		parent := a.currentTurn
		if parent == nil {
			parent = a.root
		}
		if err := t.startLlmRun(hc, a, parent, pending); err != nil {
			return err
		}
	}
	if err := safeEnd(hc, a.currentLlm, undef, new("Pi run ended before LLM message finalized"), nil); err != nil {
		return err
	}
	for _, turn := range a.turns.values() {
		if err := safeEnd(hc, turn, obj("incomplete", true), nil, nil); err != nil {
			return err
		}
	}
	a.currentLlm = nil
	a.currentTurn = nil
	a.turns.clear()

	messages, isArr := recGet(data, "messages").([]any)
	if !isArr {
		return fmt.Errorf("Cannot read properties of %s (reading 'at')", jsToString(recGet(data, "messages")))
	}
	var errMsg *string
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		if recGet(last, "role") == "assistant" {
			switch recGet(last, "stopReason") {
			case "error":
				errMsg = errorString(recGet(last, "errorMessage"))
			case "aborted":
				errMsg = new("Assistant message was aborted")
			}
		}
	}

	converted, err := convertMessages(messages)
	if err != nil {
		return err
	}
	usage, err := ctx.GetContextUsage()
	if err != nil {
		return err
	}
	var contextUsage any = undef
	if usage != nil {
		var tokens, percent any
		if usage.Tokens != nil {
			tokens = *usage.Tokens
		}
		if usage.Percent != nil {
			percent = *usage.Percent
		}
		contextUsage = obj("tokens", tokens, "contextWindow", usage.ContextWindow, "percent", percent)
	}
	a.loopResult = &loopResult{outputs: obj("messages", converted, "context_usage", contextUsage), err: errMsg}
	// Pi may compact and re-enter the loop without `before_agent_start`; the next loop joins this root and
	// `agent_settled` ends it (index.ts:664-666).
	if t.emitsSettled {
		return nil
	}

	if err := safeEnd(hc, a.root, a.loopResult.outputs, a.loopResult.err, nil); err != nil {
		return err
	}
	t.active = nil
	ctx.SetStatus(statusKey, "LangSmith: traced")
	return nil
}

// agentSettled is index.ts:674-686.
func (t *tracer) agentSettled(ctx sdk.Context, hc context.Context, _ map[string]any) error {
	if t.staleSettles > 0 {
		// Late settle for a run `before_agent_start` already ended.
		t.staleSettles--
		return nil
	}
	settled := t.active
	if settled == nil {
		return nil
	}
	t.active = nil
	if settled.loopResult != nil {
		if err := safeEnd(hc, settled.root, settled.loopResult.outputs, settled.loopResult.err, nil); err != nil {
			return err
		}
	} else {
		if err := safeEnd(hc, settled.root, obj("incomplete", true), nil, nil); err != nil {
			return err
		}
	}
	ctx.SetStatus(statusKey, "LangSmith: traced")
	return nil
}

// sessionShutdown is index.ts:688-711. The wait for the queued batches ends with the request.
func (t *tracer) sessionShutdown(ctx sdk.Context, hc context.Context, _ map[string]any) error {
	if a := t.active; a != nil {
		if a.pendingLlm != nil {
			pending := a.pendingLlm
			a.pendingLlm = nil
			parent := a.currentTurn
			if parent == nil {
				parent = a.root
			}
			if err := t.startLlmRun(hc, a, parent, pending); err != nil {
				return err
			}
		}
		if err := safeEnd(hc, a.currentLlm, undef, new("Pi session shut down before LLM message finalized"), nil); err != nil {
			return err
		}
		if a.loopResult != nil {
			if err := safeEnd(hc, a.root, a.loopResult.outputs, a.loopResult.err, nil); err != nil {
				return err
			}
		} else {
			if err := safeEnd(hc, a.root, obj("shutdown", true), new("Pi session shut down before run completed"), nil); err != nil {
				return err
			}
		}
		t.active = nil
	}
	t.staleSettles = 0
	err := t.client.awaitPendingTraceBatches(hc)
	ctx.ClearStatus(statusKey)
	return err
}
