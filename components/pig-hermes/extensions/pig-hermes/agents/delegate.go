package agents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/delegation"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

const (
	defaultMaxSpawnDepth = 1
	defaultMaxConcurrent = 10
)

var blockedDelegateTools = []string{
	"delegate_task",
	"clarify",
	"memory",
	"cronjob_manage",
	"send_message",
}

var blockedDelegateToolSet = map[string]bool{
	"delegate_task":  true,
	"clarify":        true,
	"memory":         true,
	"cronjob_manage": true,
	"send_message":   true,
}

// BlockedToolsList returns a fresh copy of the tools stripped from delegated child sessions.
func BlockedToolsList() []string {
	out := make([]string, len(blockedDelegateTools))
	copy(out, blockedDelegateTools)
	return out
}

// hermesDelegateToolsets maps Hermes toolset names to their member tool names.
var hermesDelegateToolsets = map[string][]string{
	"file":           {"read_file", "write_file", "patch", "search_files"},
	"terminal":       {"terminal", "process_manage"},
	"web":            {"web_search", "web_extract"},
	"search":         {"web_search"},
	"skills":         {"skills_list", "skill_view", "skill_manage"},
	"memory":         {"memory"},
	"todo":           {"todo_list"},
	"session_search": {"session_search"},
	"delegation":     {"delegate_task"},
	"code_execution": {"execute_code"},
	"cronjob":        {"cronjob_manage"},
	"clarify":        {"clarify"},
	"safe":           {"web_search", "web_extract", "read_file", "write_file", "patch", "search_files"},
	"debugging":      {"terminal", "process_manage", "web_search", "web_extract", "read_file", "write_file", "patch", "search_files"},
	"coding": {
		"web_search", "web_extract",
		"terminal", "process_manage",
		"read_file", "write_file", "patch", "search_files",
		"skills_list", "skill_view", "skill_manage",
		"todo_list", "memory",
		"session_search",
		"clarify",
		"execute_code", "delegate_task",
	},
	"hermes-cli": {
		"web_search", "web_extract",
		"terminal", "process_manage",
		"read_file", "write_file", "patch", "search_files",
		"skills_list", "skill_view", "skill_manage",
		"todo_list", "memory",
		"session_search",
		"clarify",
		"execute_code", "delegate_task",
		"cronjob_manage",
	},
	"hermes-acp": {
		"web_search", "web_extract",
		"terminal", "process_manage",
		"read_file", "write_file", "patch", "search_files",
		"skills_list", "skill_view", "skill_manage",
		"todo_list", "memory",
		"session_search",
		"execute_code", "delegate_task",
	},
}

// ExpandToolsetNames expands any Hermes toolset names (e.g. "file", "web", "terminal")
// into their constituent tool names, preserving unknown entries as literal tool names
// and deduplicating in order.
func ExpandToolsetNames(requested []string) []string {
	if requested == nil {
		return nil
	}
	out := make([]string, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, raw := range requested {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if members, ok := hermesDelegateToolsets[strings.ToLower(name)]; ok {
			for _, m := range members {
				add(m)
			}
			continue
		}
		add(name)
	}
	return out
}

// IsBlockedDelegateTool reports whether name is stripped from delegated child sessions.
func IsBlockedDelegateTool(name string) bool {
	return blockedDelegateToolSet[strings.TrimSpace(name)]
}

// FilterAllowedTools expands any Hermes toolset names and removes blocked delegation tools.
func FilterAllowedTools(requested []string) []string {
	if requested == nil {
		return nil
	}
	expanded := ExpandToolsetNames(requested)
	out := make([]string, 0, len(expanded))
	for _, t := range expanded {
		if t == "" || blockedDelegateToolSet[t] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// childRunOutcome captures the output and telemetry from one child agent session execution.
type childRunOutcome struct {
	ChildSessionID string
	Summary        string
	InputTokens    int
	OutputTokens   int
	Cost           float64
	Turns          int
	ToolCalls      int
	Err            error
	Cancelled      bool
}

// childSessionRunner executes a single delegated child session.
// onStart is invoked with the real child session ID immediately before Prompt.
type childSessionRunner func(
	runCtx context.Context,
	sdkCtx sdk.Context,
	req delegation.SpawnRequest,
	opts sdk.CreateAgentSessionOptions,
	prompt string,
	onStart func(realChildID string),
) childRunOutcome
// activeChildHandle is the live control surface for an in-flight delegated child session.
type activeChildHandle interface {
	Steer(text string, images ...sdk.PromptImage) error
	Abort() error
}

type activeChildEntry struct {
	SubagentID      string
	TaskID          string
	ParentSessionID string
	Goal            string
	Category        string
	Model           string
	StartedAt       time.Time
	handle          activeChildHandle
}

// DelegateManager coordinates delegated subagent execution, depth tracking,
// concurrency limits, live child control (list/steer/stop), route resolution,
// events, and usage accounting.
type DelegateManager struct {
	maxSpawnDepth int
	maxConcurrent int

	mu             sync.RWMutex
	sessionDepths  map[string]int
	activeChildren map[string]*activeChildEntry

	runner childSessionRunner
	emit   func(ctx sdk.Context, channel string, payload any)
	ledger *usage.Ledger
}

// NewDelegateManager constructs a DelegateManager using cfg defaults.
func NewDelegateManager(cfg *config.Config) *DelegateManager {
	maxDepth := defaultMaxSpawnDepth
	maxConc := defaultMaxConcurrent
	if cfg != nil {
		if cfg.MaxSpawnDepth > 0 {
			maxDepth = cfg.MaxSpawnDepth
		}
		if cfg.MaxConcurrent > 0 {
			maxConc = cfg.MaxConcurrent
		}
	}
	mgr := &DelegateManager{
		maxSpawnDepth:  maxDepth,
		maxConcurrent:  maxConc,
		sessionDepths:  make(map[string]int),
		activeChildren: make(map[string]*activeChildEntry),
		emit: func(ctx sdk.Context, channel string, payload any) {
			_ = ctx.Events().Emit(channel, payload)
		},
		ledger: usage.Default(),
	}
	mgr.runner = mgr.runSDKChildSession
	return mgr
}

// DepthForSession returns the tracked spawn depth for sessionID (0 for root).
func (m *DelegateManager) DepthForSession(sessionID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessionDepths[sessionID]
}

// SetSessionDepth records the spawn depth for sessionID (used for child tracking and tests).
func (m *DelegateManager) SetSessionDepth(sessionID string, depth int) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if depth <= 0 {
		delete(m.sessionDepths, sessionID)
		return
	}
	m.sessionDepths[sessionID] = depth
}

// registerActiveChild records a running child session handle for list/steer/stop.
func (m *DelegateManager) registerActiveChild(entry *activeChildEntry) {
	if entry == nil || strings.TrimSpace(entry.SubagentID) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeChildren == nil {
		m.activeChildren = make(map[string]*activeChildEntry)
	}
	m.activeChildren[entry.SubagentID] = entry
}

// unregisterActiveChild removes a finished child session handle.
func (m *DelegateManager) unregisterActiveChild(subagentID string) {
	if strings.TrimSpace(subagentID) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.activeChildren, subagentID)
}

func (m *DelegateManager) findActiveChild(idOrTask string) *activeChildEntry {
	key := strings.TrimSpace(idOrTask)
	if key == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if entry, ok := m.activeChildren[key]; ok {
		return entry
	}
	for _, entry := range m.activeChildren {
		if entry.TaskID == key {
			return entry
		}
	}
	return nil
}

func (m *DelegateManager) executeListAction() sdk.ToolResult {
	m.mu.RLock()
	entries := make([]*activeChildEntry, 0, len(m.activeChildren))
	for _, e := range m.activeChildren {
		entries = append(entries, e)
	}
	m.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].StartedAt.Equal(entries[j].StartedAt) {
			return entries[i].SubagentID < entries[j].SubagentID
		}
		return entries[i].StartedAt.Before(entries[j].StartedAt)
	})

	now := time.Now().UTC()
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"subagent_id":       e.SubagentID,
			"task_id":           e.TaskID,
			"parent_session_id": e.ParentSessionID,
			"goal":              e.Goal,
			"category":          e.Category,
			"model":             e.Model,
			"started_at":        e.StartedAt.Format(time.RFC3339),
			"running_ms":        now.Sub(e.StartedAt).Milliseconds(),
		})
	}
	return jsonResult(map[string]any{
		"success":   true,
		"action":    "list",
		"count":     len(out),
		"subagents": out,
	}, false)
}

func (m *DelegateManager) executeSteerAction(params map[string]any) sdk.ToolResult {
	id := strings.TrimSpace(paramString(params, "subagent_id"))
	if id == "" {
		id = strings.TrimSpace(paramString(params, "task_id"))
	}
	if id == "" {
		return errorResult("delegate_task steer requires 'subagent_id' (or 'task_id')")
	}
	msg := strings.TrimSpace(paramString(params, "message"))
	if msg == "" {
		return errorResult("delegate_task steer requires a non-empty 'message'")
	}
	entry := m.findActiveChild(id)
	if entry == nil {
		return errorResult(fmt.Sprintf("active subagent %q not found", id))
	}
	if entry.handle == nil {
		return errorResult(fmt.Sprintf("subagent %q does not support live steering", entry.SubagentID))
	}
	if err := entry.handle.Steer(msg); err != nil {
		return errorResult(fmt.Sprintf("steer subagent %q failed: %v", entry.SubagentID, err))
	}
	return jsonResult(map[string]any{
		"success":     true,
		"action":      "steer",
		"subagent_id": entry.SubagentID,
		"task_id":     entry.TaskID,
	}, false)
}

func (m *DelegateManager) executeStopAction(params map[string]any) sdk.ToolResult {
	id := strings.TrimSpace(paramString(params, "subagent_id"))
	if id == "" {
		id = strings.TrimSpace(paramString(params, "task_id"))
	}
	if id == "" {
		return errorResult("delegate_task stop requires 'subagent_id' (or 'task_id')")
	}
	entry := m.findActiveChild(id)
	if entry == nil {
		return errorResult(fmt.Sprintf("active subagent %q not found", id))
	}
	if entry.handle == nil {
		return errorResult(fmt.Sprintf("subagent %q does not support live stop", entry.SubagentID))
	}
	if err := entry.handle.Abort(); err != nil {
		return errorResult(fmt.Sprintf("stop subagent %q failed: %v", entry.SubagentID, err))
	}
	return jsonResult(map[string]any{
		"success":     true,
		"action":      "stop",
		"subagent_id": entry.SubagentID,
		"task_id":     entry.TaskID,
	}, false)
}

func registerDelegateTool(ext *sdk.Extension, mgr *DelegateManager) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "delegate_task",
		Label:         "Delegate Task",
		Description:   "Spawn one or more isolated child subagent sessions to execute tasks in parallel, or control active subagents via action ('spawn' default, 'list', 'steer', 'stop'). Pass either a single 'goal' or a 'tasks' array when spawning.",
		PromptSnippet: "Delegate focused subtasks to isolated child subagents in parallel or list/steer/stop active children with delegate_task.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"spawn", "list", "steer", "stop"},
					"default":     "spawn",
					"description": "Action to perform: 'spawn' (default), 'list' active subagents, 'steer' an active subagent, or 'stop' an active subagent.",
				},
				"subagent_id": map[string]any{
					"type":        "string",
					"description": "Target child subagent session ID (or task_id) for 'steer' or 'stop'.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Steering message to send to an active subagent when action is 'steer'.",
				},
				"task_id": map[string]any{
					"type":        "string",
					"description": "Optional caller-supplied task identifier forwarded in lifecycle events.",
				},
				"lane_id": map[string]any{
					"type":        "string",
					"description": "Optional caller-supplied lane identifier (used as task_id when task_id is omitted).",
				},
				"dag_run_id": map[string]any{
					"type":        "string",
					"description": "Optional DAG run identifier for orchestration correlation.",
				},
				"dag_node_id": map[string]any{
					"type":        "string",
					"description": "Optional DAG node identifier (used as task_id fallback when task_id and lane_id are omitted).",
				},
				"goal": map[string]any{
					"type":        "string",
					"description": "Goal for a single delegated subagent task (alternative to 'tasks').",
				},
				"context": map[string]any{
					"type":        "string",
					"description": "Background context for the single task (or shared fallback context).",
				},
				"category": map[string]any{
					"type":        "string",
					"description": "Optional routing category (e.g. 'quick', 'deep', 'research').",
				},
				"identity": map[string]any{
					"type":        "string",
					"description": "Optional role/persona identity for the child subagent.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional model override ('provider/model' or 'model').",
				},
				"thinking_level": map[string]any{
					"type":        "string",
					"description": "Optional thinking level override (e.g. 'low', 'medium', 'high').",
				},
				"toolsets": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional allowed tools/toolsets for the child (blocked tools are always stripped).",
				},
				"max_iterations": map[string]any{
					"type":        "integer",
					"description": "Optional iteration guidance for the child task.",
				},
				"tasks": map[string]any{
					"type":        "array",
					"description": "Parallel batch of tasks to delegate (capped at max_concurrent).",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"task_id": map[string]any{
								"type":        "string",
								"description": "Optional caller-supplied task identifier.",
							},
							"lane_id": map[string]any{
								"type":        "string",
								"description": "Optional caller-supplied lane identifier.",
							},
							"dag_run_id": map[string]any{
								"type":        "string",
								"description": "Optional DAG run identifier.",
							},
							"dag_node_id": map[string]any{
								"type":        "string",
								"description": "Optional DAG node identifier.",
							},
							"goal": map[string]any{
								"type":        "string",
								"description": "What this child subagent should accomplish.",
							},
							"context": map[string]any{
								"type":        "string",
								"description": "Background context for this child.",
							},
							"category": map[string]any{
								"type":        "string",
								"description": "Optional routing category.",
							},
							"identity": map[string]any{
								"type":        "string",
								"description": "Optional role/persona identity.",
							},
							"model": map[string]any{
								"type":        "string",
								"description": "Optional model override.",
							},
							"thinking_level": map[string]any{
								"type":        "string",
								"description": "Optional thinking level override.",
							},
							"toolsets": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Optional allowed tools/toolsets.",
							},
							"max_iterations": map[string]any{
								"type":        "integer",
								"description": "Optional iteration guidance.",
							},
						},
						"required": []string{"goal"},
					},
				},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return mgr.Execute(ctx, params), nil
		},
	})
}

// Execute parses params, enforces spawn depth and concurrency limits, resolves routes,
// executes child sessions in parallel, emits lifecycle events, and records usage.
func (m *DelegateManager) Execute(ctx sdk.Context, params map[string]any) sdk.ToolResult {
	action := strings.ToLower(strings.TrimSpace(paramString(params, "action")))
	switch action {
	case "", "spawn":
		// fall through to spawn flow
	case "list":
		return m.executeListAction()
	case "steer":
		return m.executeSteerAction(params)
	case "stop":
		return m.executeStopAction(params)
	default:
		return errorResult(fmt.Sprintf("unknown delegate_task action %q (expected spawn, list, steer, or stop)", action))
	}

	parentSessionID := resolveSessionID(ctx)
	parentDepth := m.DepthForSession(parentSessionID)
	if explicitDepth, ok := paramInt(params, "depth"); ok && explicitDepth > parentDepth {
		parentDepth = explicitDepth
	}
	nextDepth := parentDepth + 1
	if nextDepth > m.maxSpawnDepth {
		return errorResult(fmt.Sprintf(
			"delegation depth limit reached (current depth %d, max_spawn_depth %d): child agents cannot spawn further subagents",
			parentDepth, m.maxSpawnDepth,
		))
	}

	reqs, err := parseDelegateRequests(params, nextDepth)
	if err != nil {
		return errorResult(err.Error())
	}
	if len(reqs) > m.maxConcurrent {
		return errorResult(fmt.Sprintf(
			"requested %d parallel tasks, which exceeds max_concurrent (%d)",
			len(reqs), m.maxConcurrent,
		))
	}

	runCtx, cancel := contextFromDone(ctx.Done())
	defer cancel()

	results := make([]delegation.SpawnResult, len(reqs))
	sem := make(chan struct{}, m.maxConcurrent)
	var wg sync.WaitGroup

	for i, req := range reqs {
		wg.Add(1)
		go func(idx int, r delegation.SpawnRequest) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-runCtx.Done():
				results[idx] = delegation.SpawnResult{
					TaskID: r.TaskID,
					Status: "cancelled",
					Error:  runCtx.Err().Error(),
				}
				return
			}
			results[idx] = m.runSingleTask(runCtx, ctx, parentSessionID, r)
		}(i, req)
	}
	wg.Wait()

	succeeded := 0
	failed := 0
	for _, res := range results {
		if res.Status == "completed" {
			succeeded++
		} else {
			failed++
		}
	}

	return jsonResult(map[string]any{
		"success":   failed == 0,
		"total":     len(results),
		"succeeded": succeeded,
		"failed":    failed,
		"tasks":     results,
	}, failed > 0 && succeeded == 0)
}

func (m *DelegateManager) runSingleTask(
	runCtx context.Context,
	sdkCtx sdk.Context,
	parentSessionID string,
	req delegation.SpawnRequest,
) delegation.SpawnResult {
	started := time.Now().UTC()

	// Phase 2 OMH routing seam: consult registered route resolvers before spawning.
	if dec, ok, err := delegation.ResolveRoute(req); err != nil {
		return delegation.SpawnResult{
			TaskID:     req.TaskID,
			Status:     "error",
			DurationMs: time.Since(started).Milliseconds(),
			Error:      fmt.Sprintf("route resolution failed: %v", err),
		}
	} else if ok {
		if dec.Model != "" {
			req.Model = dec.Model
		}
		if dec.ThinkingLevel != "" {
			req.ThinkingLevel = dec.ThinkingLevel
		}
	}

	fallbackChildID := fmt.Sprintf("sa-%s", generateShortHex())

	var (
		startOnce       sync.Once
		startedChildID  string
		startedDepthSet bool
	)
	emitStart := func(realID string) {
		startOnce.Do(func() {
			id := strings.TrimSpace(realID)
			if id == "" {
				id = fallbackChildID
			}
			startedChildID = id
			m.SetSessionDepth(id, req.Depth)
			startedDepthSet = true
			if m.emit != nil {
				m.emit(sdkCtx, events.ChannelSubagentStart, events.SubagentStartPayload{
					ParentSessionID: parentSessionID,
					ChildSessionID:  id,
					TaskID:          req.TaskID,
					Goal:            req.Goal,
					Category:        req.Category,
					Model:           req.Model,
					ThinkingLevel:   req.ThinkingLevel,
					StartedAt:       started.Format(time.RFC3339),
				})
			}
		})
	}
	defer func() {
		if startedDepthSet && startedChildID != "" {
			m.SetSessionDepth(startedChildID, 0)
		}
	}()

	opts := buildChildSessionOptions(sdkCtx, req)
	prompt := buildChildPrompt(req)

	outcome := m.runner(runCtx, sdkCtx, req, opts, prompt, emitStart)
	actualChildID := startedChildID
	if actualChildID == "" {
		actualChildID = strings.TrimSpace(outcome.ChildSessionID)
	}
	if actualChildID == "" {
		actualChildID = fallbackChildID
	}
	// Ensure subagent_start is emitted even for custom test runners that don't invoke onStart.
	emitStart(actualChildID)

	durMs := time.Since(started).Milliseconds()
	status := "completed"
	errStr := ""
	if outcome.Err != nil {
		if outcome.Cancelled || runCtx.Err() != nil {
			status = "cancelled"
		} else {
			status = "error"
		}
		errStr = outcome.Err.Error()
	} else if outcome.Cancelled || runCtx.Err() != nil {
		status = "cancelled"
		if runCtx.Err() != nil {
			errStr = runCtx.Err().Error()
		} else {
			errStr = "child session aborted"
		}
	}
	if m.ledger != nil {
		m.ledger.Add(
			actualChildID,
			parentSessionID,
			req.Model,
			outcome.InputTokens,
			outcome.OutputTokens,
			outcome.Cost,
			outcome.Turns,
			outcome.ToolCalls,
		)
	}

	if m.emit != nil {
		m.emit(sdkCtx, events.ChannelSubagentStop, events.SubagentStopPayload{
			ParentSessionID: parentSessionID,
			ChildSessionID:  actualChildID,
			TaskID:          req.TaskID,
			Status:          status,
			DurationMs:      durMs,
			InputTokens:     outcome.InputTokens,
			OutputTokens:    outcome.OutputTokens,
			Cost:            outcome.Cost,
			Error:           errStr,
		})
	}

	return delegation.SpawnResult{
		TaskID:         req.TaskID,
		ChildSessionID: actualChildID,
		Status:         status,
		Summary:        outcome.Summary,
		DurationMs:     durMs,
		InputTokens:    outcome.InputTokens,
		OutputTokens:   outcome.OutputTokens,
		Cost:           outcome.Cost,
		Error:          errStr,
	}
}

func buildChildSessionOptions(ctx sdk.Context, req delegation.SpawnRequest) sdk.CreateAgentSessionOptions {
	opts := sdk.CreateAgentSessionOptions{
		InMemory:      true,
		ThinkingLevel: req.ThinkingLevel,
		ExcludeTools:  BlockedToolsList(),
	}
	if len(req.Toolsets) > 0 {
		opts.Tools = FilterAllowedTools(req.Toolsets)
	}
	// Safely read Cwd and resolve Model only if ctx is bound to an extension.
	if cwd := safeContextCwd(ctx); cwd != "" {
		opts.Cwd = cwd
	}
	if req.Model != "" {
		if modelObj := resolveModelObject(ctx, req.Model); modelObj != nil {
			opts.Model = modelObj
		}
	} else if modelObj := resolveParentModelObject(ctx); modelObj != nil {
		opts.Model = modelObj
	}
	return opts
}

func resolveParentModelObject(ctx sdk.Context) (model any) {
	defer func() {
		if recover() != nil {
			model = nil
		}
	}()
	provider := strings.TrimSpace(ctx.ModelProvider())
	modelID := strings.TrimSpace(ctx.Model())
	if provider == "" || modelID == "" {
		return nil
	}
	reg := ctx.ModelRegistry()
	if m, err := reg.Find(provider, modelID); err == nil && m != nil {
		return m
	}
	return nil
}

func safeContextCwd(ctx sdk.Context) (cwd string) {
	defer func() {
		if recover() != nil {
			cwd = ""
		}
	}()
	return ctx.Cwd()
}

func resolveModelObject(ctx sdk.Context, modelSpec string) (model any) {
	defer func() {
		if recover() != nil {
			model = nil
		}
	}()
	spec := strings.TrimSpace(modelSpec)
	if spec == "" {
		return nil
	}
	reg := ctx.ModelRegistry()
	if provider, id, ok := strings.Cut(spec, "/"); ok {
		if m, err := reg.Find(strings.TrimSpace(provider), strings.TrimSpace(id)); err == nil && m != nil {
			return m
		}
		return nil
	}
	if provider := ctx.ModelProvider(); provider != "" {
		if m, err := reg.Find(provider, spec); err == nil && m != nil {
			return m
		}
	}
	return nil
}

func buildChildPrompt(req delegation.SpawnRequest) string {
	var b strings.Builder
	if req.Identity != "" {
		fmt.Fprintf(&b, "Role / Identity: %s\n", req.Identity)
	}
	if req.Category != "" {
		fmt.Fprintf(&b, "Task Category: %s\n", req.Category)
	}
	if req.MaxIterations > 0 {
		fmt.Fprintf(&b, "Iteration Budget: complete within %d turns.\n", req.MaxIterations)
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Goal:\n%s", req.Goal)
	if req.Context != "" {
		fmt.Fprintf(&b, "\n\nContext:\n%s", req.Context)
	}
	return b.String()
}

func (m *DelegateManager) runSDKChildSession(
	runCtx context.Context,
	sdkCtx sdk.Context,
	req delegation.SpawnRequest,
	opts sdk.CreateAgentSessionOptions,
	prompt string,
	onStart func(realChildID string),
) childRunOutcome {
	session, err := sdkCtx.CreateAgentSession(opts)
	if err != nil {
		return childRunOutcome{Err: fmt.Errorf("create child session: %w", err)}
	}
	defer session.Dispose()
	m.registerActiveChild(&activeChildEntry{
		SubagentID:      session.SessionID,
		TaskID:          req.TaskID,
		ParentSessionID: resolveSessionID(sdkCtx),
		Goal:            req.Goal,
		Category:        req.Category,
		Model:           req.Model,
		StartedAt:       time.Now().UTC(),
		handle:          session,
	})
	defer m.unregisterActiveChild(session.SessionID)
	var (
		mu           sync.Mutex
		inTok        int
		outTok       int
		cost         float64
		turns        int
		tools        int
		lastText     string
		stopReason   string
		errorMessage string
	)

	unsub := session.Subscribe(func(ev map[string]any) {
		evType, _ := ev["type"].(string)
		mu.Lock()
		defer mu.Unlock()
		switch evType {
		case "turn_end":
			turns++
		case "tool_execution_end":
			tools++
		case "message_end":
			if msg, ok := ev["message"].(map[string]any); ok {
				if role, _ := msg["role"].(string); role == "assistant" {
					if txt := extractAssistantText(msg); txt != "" {
						lastText = txt
					}
					if sr, _ := msg["stopReason"].(string); sr != "" {
						stopReason = sr
					}
					if em, _ := msg["errorMessage"].(string); em != "" {
						errorMessage = em
					}
					if u, ok := msg["usage"].(map[string]any); ok {
						inTok += mapInt(u, "input")
						outTok += mapInt(u, "output")
						cost += mapFloat(u, "cost")
					}
				}
			}
		}
	})
	defer unsub()

	if onStart != nil {
		onStart(session.SessionID)
	}

	promptErr := session.Prompt(runCtx, prompt)

	summary := ""
	childSessionID := session.SessionID
	if st, err := session.State(); err == nil {
		if st.SessionID != "" {
			childSessionID = st.SessionID
		}
		if st.LastAssistantText != nil && *st.LastAssistantText != "" {
			summary = *st.LastAssistantText
		}
	}

	mu.Lock()
	if summary == "" {
		summary = lastText
	}
	out := childRunOutcome{
		ChildSessionID: childSessionID,
		Summary:        summary,
		InputTokens:    inTok,
		OutputTokens:   outTok,
		Cost:           cost,
		Turns:          turns,
		ToolCalls:      tools,
	}
	finalStop := stopReason
	finalErrMsg := errorMessage
	mu.Unlock()

	out.Err, out.Cancelled = classifyChildOutcome(promptErr, runCtx.Err(), finalStop, finalErrMsg)
	return out
}

// classifyChildOutcome determines whether a child session run errored or was cancelled
// based on Prompt's error, runCtx.Err(), and the last assistant message_end stopReason/errorMessage.
func classifyChildOutcome(promptErr, ctxErr error, stopReason, errorMessage string) (err error, cancelled bool) {
	if promptErr != nil {
		return promptErr, ctxErr != nil || strings.EqualFold(stopReason, "aborted")
	}
	if ctxErr != nil {
		return ctxErr, true
	}
	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "aborted":
		msg := strings.TrimSpace(errorMessage)
		if msg == "" {
			msg = "child session aborted"
		}
		return fmt.Errorf("%s", msg), true
	case "error":
		msg := strings.TrimSpace(errorMessage)
		if msg == "" {
			msg = "child session ended with error"
		}
		return fmt.Errorf("%s", msg), false
	}
	return nil, false
}

func parseDelegateRequests(params map[string]any, depth int) ([]delegation.SpawnRequest, error) {
	if params == nil {
		return nil, fmt.Errorf("delegate_task requires either 'goal' or a non-empty 'tasks' array")
	}

	sharedCtx := strings.TrimSpace(paramString(params, "context"))
	sharedCat := strings.TrimSpace(paramString(params, "category"))
	sharedID := strings.TrimSpace(paramString(params, "identity"))
	sharedModel := strings.TrimSpace(paramString(params, "model"))
	sharedThink := strings.TrimSpace(paramString(params, "thinking_level"))
	sharedToolsets := paramStringSlice(params, "toolsets")
	sharedMaxIter, _ := paramInt(params, "max_iterations")
	sharedDagRunID := strings.TrimSpace(paramString(params, "dag_run_id"))

	if rawTasks, ok := params["tasks"]; ok && rawTasks != nil {
		list, ok := rawTasks.([]any)
		if !ok {
			return nil, fmt.Errorf("'tasks' must be an array of task objects")
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("'tasks' array must contain at least one task")
		}
		reqs := make([]delegation.SpawnRequest, 0, len(list))
		for i, elem := range list {
			m, ok := elem.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("tasks[%d] must be an object", i)
			}
			goal := strings.TrimSpace(paramString(m, "goal"))
			if goal == "" {
				return nil, fmt.Errorf("tasks[%d] requires a non-empty 'goal'", i)
			}
			taskID := resolveCallerTaskID(m, i+1)
			taskCtx := firstNonEmpty(strings.TrimSpace(paramString(m, "context")), sharedCtx)
			dagRunID := firstNonEmpty(strings.TrimSpace(paramString(m, "dag_run_id")), sharedDagRunID)
			dagNodeID := strings.TrimSpace(paramString(m, "dag_node_id"))
			taskCtx = appendCorrelationContext(taskCtx, dagRunID, dagNodeID)
			req := delegation.SpawnRequest{
				TaskID:        taskID,
				Goal:          goal,
				Context:       taskCtx,
				Category:      firstNonEmpty(strings.TrimSpace(paramString(m, "category")), sharedCat),
				Identity:      firstNonEmpty(strings.TrimSpace(paramString(m, "identity")), sharedID),
				Model:         firstNonEmpty(strings.TrimSpace(paramString(m, "model")), sharedModel),
				ThinkingLevel: firstNonEmpty(strings.TrimSpace(paramString(m, "thinking_level")), sharedThink),
				Toolsets:      paramStringSlice(m, "toolsets"),
				MaxIterations: sharedMaxIter,
				Depth:         depth,
			}
			if len(req.Toolsets) == 0 && len(sharedToolsets) > 0 {
				req.Toolsets = sharedToolsets
			}
			if mi, ok := paramInt(m, "max_iterations"); ok && mi > 0 {
				req.MaxIterations = mi
			}
			reqs = append(reqs, req)
		}
		return reqs, nil
	}

	goal := strings.TrimSpace(paramString(params, "goal"))
	if goal == "" {
		return nil, fmt.Errorf("delegate_task requires either 'goal' or a non-empty 'tasks' array")
	}
	taskID := resolveCallerTaskID(params, 1)
	dagNodeID := strings.TrimSpace(paramString(params, "dag_node_id"))
	taskCtx := appendCorrelationContext(sharedCtx, sharedDagRunID, dagNodeID)
	return []delegation.SpawnRequest{
		{
			TaskID:        taskID,
			Goal:          goal,
			Context:       taskCtx,
			Category:      sharedCat,
			Identity:      sharedID,
			Model:         sharedModel,
			ThinkingLevel: sharedThink,
			Toolsets:      sharedToolsets,
			MaxIterations: sharedMaxIter,
			Depth:         depth,
		},
	}, nil
}

// resolveCallerTaskID preserves caller-supplied task_id / lane_id / dag_node_id
// (with precedence task_id > lane_id > dag_node_id) so OMH TeamStore and DAG node
// latching match live delegated subagent events.
func resolveCallerTaskID(m map[string]any, index int) string {
	if id := strings.TrimSpace(paramString(m, "task_id")); id != "" {
		return id
	}
	if id := strings.TrimSpace(paramString(m, "lane_id")); id != "" {
		return id
	}
	if id := strings.TrimSpace(paramString(m, "dag_node_id")); id != "" {
		return id
	}
	return fmt.Sprintf("task-%d-%s", index, generateShortHex())
}

func appendCorrelationContext(base, dagRunID, dagNodeID string) string {
	var tags []string
	if dagRunID != "" {
		tags = append(tags, "dag_run_id="+dagRunID)
	}
	if dagNodeID != "" {
		tags = append(tags, "dag_node_id="+dagNodeID)
	}
	if len(tags) == 0 {
		return base
	}
	tagLine := "[orchestration: " + strings.Join(tags, " ") + "]"
	if base == "" {
		return tagLine
	}
	return base + "\n" + tagLine
}
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func generateShortHex() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

func extractAssistantText(msg map[string]any) string {
	if s, ok := msg["content"].(string); ok {
		return s
	}
	blocks, ok := msg["content"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if bm, ok := b.(map[string]any); ok {
			if t, _ := bm["type"].(string); t == "text" {
				if txt, _ := bm["text"].(string); txt != "" {
					parts = append(parts, txt)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func mapInt(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func mapFloat(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case map[string]any:
		return mapFloat(n, "total")
	}
	return 0
}
